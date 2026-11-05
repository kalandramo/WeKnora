# MCP 工具群聊级授权 — 实施说明

| 项目 | 内容 |
| --- | --- |
| 状态 | 已实施（后端 + API，无 UI） |
| 日期 | 2026-11-05 |
| 基线 | `watsons` @ `240a4ba61` |
| 决策 | 严格语义（全 IM 平台生效，群聊 + 私聊均受限，Web/API 走原规则）；`platform` 必填；独立表 + 装饰器；不做前端 |
| 替代 | 取代《方案-MCP工具群聊级授权.md》中的"挂载 YAML"与"复用 MCPToolApproval 加列"两条路线 |

---

## 1. 最终语义

| 来源 | 受限工具的行为 |
| --- | --- |
| 白名单内的群 | 可用（仍需通过工具 `enabled`、风险分级、访问级别、人工审批） |
| 白名单外的群 | 不可用 |
| 私聊 | 不可用（需显式配 `chat_type: direct` 才放行） |
| Web / API / 定时任务 | **不受影响**，走原策略链 |

只有显式配置过 `(service, tool)` 的工具才受约束；未配置的工具行为完全不变。机制只能做减法，任何情况下都不能放宽既有检查。

“未配置即放行”在代码里是两处提前返回，而不是靠事后判断：

| 情形 | 结果 |
| --- | --- |
| 该工具没有 scope 行 | `decide` 返回允许，上游结论原样透传 |
| 本次运行没有 IM 会话身份（Web / API / 定时） | `allowed` 与 `EnabledTools` 直接返回上游结论，**不查 scope 表**——零查询、零新增故障面 |
| 有 scope 行但 allow 为空 | IM 内一律不可用（这是显式封禁，不是“未配置”） |
| 有 scope 行、当前群不在 allow 内 | 本群不可用；其它未配置的工具照旧 |

删除配置（`DELETE`）即移除 scope 行，工具立刻回到上游策略链。

### 1.1 作用平台：全部 IM 渠道，不限于飞书

判定只读 `IncomingMessage` 的 `Platform` / `ChatType` / `ChatID`，代码里没有硬编码任何平台。项目内 12 个 IM adapter（飞书、企微长连接、企微 webhook、钉钉、Slack、Telegram、Mattermost、QQ 机器人、云之家、微信、Lark 等）都会带这三个字段，因此**都会被这套机制约束**。

由此有一条必须知道的连带影响：

> 给某个工具配了 `platform: feishu` 的白名单后，该工具在企微群、钉钉群里**同样不可用**——`platform` 不匹配即视为不在白名单内。

这是「白名单是唯一入口」的必然结果，也堵住了「换个 IM 问一遍」的绕过路径。要在其它平台放行，显式补一条对应条目：

```json
{"platform": "wecom", "chat_type": "group", "chat_id": "*", "label": "企微全部群放行"}
```

`platform` 与 `chat_type` 在写入时**均为必填**，空值直接返回 400。运行时对历史遗留的空值按「不匹配任何平台」处理，而不是当作通配——宁可让一条坏规则失效并暴露出来，也不能让它悄悄变成全平台通配。

---

## 2. 为什么是"独立表 + 装饰器"

原始方案想把规则加在 `mcp_tool_approvals` 上（复用现有工具策略 UI）。调研后放弃，原因是升级成本：

| 文件 | 近 90 天上游改动 | 复用策略表要改 | 本次方案要改 |
| --- | --- | --- | --- |
| `internal/handler/mcp_service.go` | 18 | ✅ 必须 | ❌ |
| `internal/types/mcp.go` | 10 | ✅ 必须 | ❌ |
| `internal/agent/approval/gate.go` | 9 | ❌ | ❌ |
| `internal/agent/tools/mcp_catalog.go` | 7 | ✅ 必须 | ❌ |
| `internal/container/container.go` | 68 | ✅ 1 行 | ✅ 1 行 |
| `internal/router/router.go` | 30 | ❌ | ✅ 1 行 |

关键事实：`approval.Checker` 只有两个方法，而三处强制点（`visibleTools`、`checkEnabled`、`MCPTool.Execute`）都经 `Gate` 调用它。唯一装配点在 `container.go`。因此用一个装饰器包住 checker，catalog 与 gate 一行都不用改。

```
container.go:378  approval.NewGate(cfg, mcpscope.NewDecorator(&approval.Adapter{Svc: s}, scopes), broker)
                                          ↑ IsRequired / IsEnabled / EnabledTools / Policy / ObserveRisk
```

`Decorator` 同时实现 `approval.BulkEnabledChecker`（枚举时一次查询覆盖整批工具，避免 20 个工具打 20 次库）与 `approval.RiskChecker`（不透传会让风险分级静默失效——`mcp_risk.go:61` 用类型断言取它）。

---

## 3. 数据结构

表 `mcp_chat_scopes`（PostgreSQL 迁移 `000135`，SQLite 迁移 `000052`）：

| 列 | 类型 | 说明 |
| --- | --- | --- |
| `id` | varchar(36) | 主键 |
| `tenant_id` | bigint | 租户 |
| `service_id` | varchar(36) | MCP 服务 |
| `tool_name` | varchar(512) | MCP 服务器上报的**原始工具名**（不是带哈希的注册名） |
| `spec` | text | JSON：`{"allow":[{platform,chat_type,chat_id,label}]}` |
| `created_at` / `updated_at` | timestamptz | |

唯一索引 `(tenant_id, service_id, tool_name)` 支撑 upsert；`service_id` 单列索引支撑每次工具调用的读取。

`allow` 为空数组 = 该工具在所有 IM 会话不可用（保留行、只关灯，用作应急熔断）。

### 匹配规则

| 条目字段 | 规则 |
| --- | --- |
| `platform` | 必填，必须与消息来源平台一致（`feishu` / `wecom` / `dingtalk` / `slack` / `telegram` / `mattermost` / `qqbot` / `yunzhijia` / `wechat` / `lark` 等）。空值写入报 400；存量空值按不匹配处理 |
| `chat_type` | 必填，`group` 或 `direct` |
| `chat_id`（群） | 必填，`*` 表示该平台任意群 |
| `chat_id`（私聊） | 必须为空或 `*`（私聊没有群 ID） |
| 多条 `allow` | 取并集：任一命中即放行；不同平台可各配一条 |

配置错误的处理：`spec` 无法解析 → **拒绝**（不因坏数据放开）；DB 读取失败 → 返回错误，由 catalog 转成 "permissions are temporarily unavailable"（fail-closed）。

---

## 4. 运维操作

启动迁移随 Pod 自动执行（`AUTO_MIGRATE` 默认开启），无需手工步骤。生效无需重启：装饰器每次判定都读库。

### 锁定一个工具

```bash
curl -X PUT https://<weknora>/api/v1/mcp-chat-scopes \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "service": "CMDB 资产平台",
    "tool_name": "cmdb_asset_update",
    "allow": [
      {"platform":"feishu","chat_type":"group","chat_id":"oc_48467056a5930ea64cb232f60cf27948","label":"基础设施运维群"}
    ]
  }'
```

`service` 支持服务名（租户内唯一）或 `service_id`；名字路径内部换 ID，省去查 UUID。

### 查看

```bash
curl "https://<weknora>/api/v1/mcp-chat-scopes?service_id=<id>" -H "Authorization: Bearer <token>"
```

不传 `service_id` 返回整个工作区的规则。

### 解除限制

```bash
curl -X DELETE "https://<weknora>/api/v1/mcp-chat-scopes?service_id=<id>&tool_name=cmdb_asset_update" \
  -H "Authorization: Bearer <token>"
```

### 应急熔断（工具全面下线但保留配置）

```bash
curl -X PUT https://<weknora>/api/v1/mcp-chat-scopes \
  -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
  -d '{"service":"CMDB 资产平台","tool_name":"cmdb_asset_update","allow":[]}'
```

### 群 ID 从哪来

飞书群设置 → 群信息 → 群号可复制 `oc_...`；也可从网关日志的 `chat_id="oc_..."` 字段取。

---

## 5. 可观测性

命中拒绝时一行日志：

```
[MCP-SCOPE] denied tool=cmdb_asset_update service=<id> platform=feishu chat_type=group chat=oc_xxx
```

配置保存/删除也有 `[MCP-SCOPE] saved/removed` 记录。未接入 Prometheus 指标（原方案 §5 的 counter/gauge 未实现，按"只做后端入口"的范围裁剪）。

---

## 6. 已知限制

| 限制 | 影响 | 处理 |
| --- | --- | --- |
| `discover_tools` 的 `list_servers` 不过滤 | 被全禁的服务名仍出现在列表里，模型可看到服务存在，但 `list_tools` 返回空 | 属于信息可见性而非能力泄漏；修它需要改 `mcp_catalog.go`，与"零上游逻辑改动"冲突，暂不做 |
| 无管理界面 | 配置靠 curl | 有意为之，避免前端随上游迭代反复合并 |
| 私聊无法区分具体用户 | 只能整体允许/禁止某平台的私聊 | 用户维度属于访问级别/审批层，不并入本机制 |
| 新工具默认不受限 | 新增高风险工具时容易忘记加锁 | 靠运维流程约束；后续可在 MCP 工具列表加提示（需前端） |
| 每轮一次 DB 读取 | 单工具调用多一次索引查询；枚举走批量，一次 | 索引查询 <1ms，未加缓存 |

---

## 7. 改动清单

| 文件 | 类型 | 说明 |
| --- | --- | --- |
| `internal/types/mcp_chat_scope.go` | 新增 | 表模型、scope schema、ctx 身份 |
| `internal/application/repository/mcp_chat_scope_repository.go` | 新增 | Upsert / Delete / List / ListByService |
| `internal/agent/mcpscope/mcpscope.go` | 新增 | 装饰器、匹配、spec 解析 |
| `internal/handler/mcp_chat_scope.go` | 新增 | List / Upsert / Delete + 校验 |
| `internal/router/routes_mcp_chat_scope.go` | 新增 | 路由注册 |
| `migrations/versioned/000135_mcp_chat_scope*` | 新增 | PostgreSQL |
| `migrations/sqlite/000052_mcp_chat_scope*` | 新增 | SQLite |
| `internal/container/container.go` | 改 4 行 | Provide repository / handler，Gate 包装饰器 |
| `internal/router/router.go` | 改 2 行 | params 字段 + 注册调用 |
| `internal/im/service.go` | 改 6 行 | `mcpChatIdentityOf` + 两条链路 ctx stamp |

测试：`mcpscope`（11 例，含 Gate 通路验证）、仓储（4 例，SQLite 内存库）、handler 校验（2 例）、IM 身份映射（1 例）。

---

## 8. 升级注意

1. **迁移编号**：PostgreSQL 用 `000135`、SQLite 用 `000052`。上游若在同一区间新增迁移，把本文件重编号即可（本表可随时 drop 重建，无数据依赖）。
2. **唯一上游接触点**：`container.go` 的 Gate Provide 与 `router.go` 的两行。上游改动这两个文件时，冲突解决方式是重新加回这两处，不涉及逻辑取舍。
3. **回滚**：删除 `container.go` 的装饰器包装即恢复原行为（表留着也无副作用）；彻底回滚再执行 down 迁移。
