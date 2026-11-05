# 方案：MCP 工具的群聊级授权（Chat-scoped MCP Tool Exposure）

| 项目 | 内容 |
| --- | --- |
| 状态 | **已实施，方案有修订** → 见《MCP群聊授权-实施说明.md》 |
| 日期 | 2026-11-04（2026-11-05 修订实施） |
| 基线 | `watsons` @ `0cc53ee5a`（上游 `v0.9.0`） |
| 目标 | 指定群聊才能使用某些 MCP 工具；未在配置中出现的工具按原规则判断 |
| 影响面 | 4 个既有文件各插入 4~6 行 + 1 个新包，不触碰 `agent_service.go` / `session_agent_qa.go` |

> **实施修订（2026-11-05）**：配置载体由"挂载 YAML 文件"改为"独立表 `mcp_chat_scopes` + 后端 REST API"（不挂文件、不加前端、不改上游 MCP 策略表）；判定由"四处强制点各加判断"收敛为"用 `approval.Checker` 装饰器包一层"。语义采用严格模式：IM 群聊与私聊均受限，Web/API 走原规则。详见《MCP群聊授权-实施说明.md》。

---

## 1. 需求解读

原始表述可拆成两条约束：

1. **指定群聊才能使用某些 MCP 工具** —— 存在一批"高权"工具，只允许在列出的群里出现。
2. **不在配置上的按原规则判断** —— 未被规则覆盖的工具，保持现有行为（Agent 的 MCP 选择、工具级 `enabled`、风险分级、访问级别、人工审批），不因引入本机制而改变。

由此可以定义为一条**只做减法**的增量策略：

> 规则命中某批工具时，为该批工具建立"允许出现的会话白名单"；白名单外的会话既看不到也调不动。未被任何规则命中的工具，完全走原有判定链。规则**不能**放宽任何既有检查——即使群在白名单内，仍需通过工具开关、访问级别与审批。

### 1.1 与另一种解读的差别

| 解读 | 语义 | 运维成本 | 风险 |
| --- | --- | --- | --- |
| **A（推荐）工具为中心** | 规则 = 一批工具 + 允许的群；其他群失去这批工具 | 只维护"例外"，新增群/工具时增量编辑 | 漏配一个群 → 该群少一个工具（可发现、可恢复） |
| B 群为中心 | 每个群维护一份完整工具白名单，未列出即禁用 | 群 × 工具矩阵，随 MCP 增长迅速失控 | 漏配 → 该群大面积不可用（影响面大） |

本方案按 A 实现。若确实需要 B，可在同一份配置里增加 `chat_allowlists`（见 §3.3 扩展位），但不建议作为首版。

---

## 2. 现状事实（已核实）

| 事实 | 位置 | 对本方案的意义 |
| --- | --- | --- |
| MCP 服务是 **租户级**配置，Agent 通过 `mcp_services`（`all` / `selected`）选择范围 | `internal/types/agent.go:125`、`internal/application/service/agent_service.go:420-490` | 现有粒度止于"哪个 Agent 能用哪些服务"，与群聊无关 |
| 每次问答都**新建 ToolRegistry 与 MCPCatalog** | `agent_service.go:255`、`:262`；`internal/agent/tools/mcp_tool.go:686` | 天然是"每轮一策"的执行边界，适合做 per-run 过滤 |
| Catalog 构造时从 ctx 取 `tenantID` 与 `principal`，并要求执行期 ctx 与之一致 | `internal/agent/tools/mcp_catalog.go:191-208`、`:237-248` | 已有"上下文鉴权"范式，可直接沿用 |
| 工具可见性统一走 `visibleTools`（含 `describe` / `list_tools` / `RefreshMCPTools`） | `mcp_catalog.go:319`；调用点 `:279`、`:705` | 一处过滤即可覆盖列举与发布 |
| 调用解析走 `checkEnabled` | `mcp_catalog.go:368`；调用点 `:715`、`:918` | 第二道闸，覆盖 `call_mcp_tool` 代理 |
| 真正执行前，`MCPTool.Execute` 会再查一次 `gate.IsEnabled` | `internal/agent/tools/mcp_tool.go:146-160` | 第三道闸的**现成插入点**，语义完全对应 |
| 存在"按运行上下文收缩能力"的先例：只读运行不展示写工具 | `mcp_catalog.go:343-366`（读 `AccessLevelFromContext`） | 证明该层做策略过滤是被上游认可的模式 |
| 上下文传参范式：`WithAccessLevel` / `AccessLevelFromContext`，文件仅 1 次改动/180 天 | `internal/types/run_mode.go:49-61` | 群聊身份按同范式写入 ctx |
| IM 侧已有 `ChatID` / `ChatType`；**飞书私聊 `ChatID` 为空**，仅群聊赋值 | `internal/im/adapter.go:57-60`、`internal/im/feishu/longconn.go:199-207` | 规则需按 `chat_type` 区分，不能只按 `chat_id` |
| 两条 IM 路径（流式 / 非流式）共用同一请求构造 | `internal/im/service.go:549`、`:2893`、`:3256` | 单点透传即可覆盖两条链路 |
| AgentQA 会派生出整轮 ctx（sessionID / runMode / accessLevel） | `internal/application/service/session_agent_qa.go:63-67`、`:116` | 在 IM 侧 stamp 的 ctx 会随派生一路传到工具执行 |

结论：**这是一个有清晰接缝的需求**——不需要改 MCP 连接层、不需要改租户模型，只在 Catalog 的三次策略判定上追加一个"会话白名单"条件。

---

## 3. 方案设计

### 3.1 配置载体

| 选项 | 改动面 | 生效方式 | 评价 |
| --- | --- | --- | --- |
| 环境变量内联 JSON | 无依赖 | 重启 | 3 条规则以上即不可读，不推荐 |
| **挂载 YAML 文件（推荐）** | `MCP_CHAT_SCOPE_FILE` 指向路径；复用已有 `app.extraVolumes` / `app.extraEnv` | 重启或按 mtime 热加载 | 可进 Git、可审计、改动不重建镜像 |
| DB 表 + 管理 UI | 迁移 + 仓储 + 接口 + 前端 | 即时 | 最正规，但跨越上游 schema，升级成本最高 |

推荐文件方案，理由是它与现有部署方式零摩擦：`helm/templates/app.yaml:163`（`extraEnv`）与 `:192`（`extraVolumes`）已经提供了挂载能力，**无需改 Helm chart**。

```yaml
# values 追加片段（示意）
app:
  extraEnv:
    - name: MCP_CHAT_SCOPE_FILE
      value: /etc/weknora/mcp-scope.yaml
  extraVolumes:
    - name: mcp-scope
      configMap: { name: weknora-mcp-scope }
  extraVolumeMounts:
    - name: mcp-scope
      mountPath: /etc/weknora
      readOnly: true
```

### 3.2 配置 Schema

```yaml
version: 1

# 一次运行不属于任何 IM 会话（Web / API / 定时任务）时，被规则覆盖的工具如何处理：
#   inherit —— 走原规则（默认，符合"不在配置上的按原规则判断"）
#   deny    —— 一并禁用，使白名单成为唯一入口（更严格）
outside_im: inherit

rules:
  # 规则 1：CMDB 写操作只在运维群可用
  - name: cmdb-write-ops-only
    effect: allow                 # allow（默认）| deny
    enabled: true
    match:
      services: ["CMDB 资产平台"]  # 服务名（租户内唯一）或 service_id；省略表示全部服务
      tools:                      # 工具名 glob，匹配 MCP 服务器上报的原始名；省略表示该服务全部工具
        - cmdb_asset_create
        - cmdb_asset_update
        - cmdb_asset_delete
    allow_in:
      - platform: feishu
        chat_type: group
        chat_id: "oc_48467056a5930ea64cb232f60cf27948"
        label: "基础设施运维群"      # 仅用于日志与评审，不参与匹配

  # 规则 2：整个服务只在两个会话可见（群 + 私聊各一）
  - name: k8s-prod-probe-groups
    match:
      services: ["K8s 生产集群"]
    allow_in:
      - { platform: feishu, chat_type: group, chat_id: "oc_..." }
      - { platform: feishu, chat_type: direct, chat_id: "" }

  # 规则 3：应急熔断（deny 恒优先于 allow）
  - name: freeze-cmdb-writes
    effect: deny
    enabled: false
    match: { services: ["CMDB 资产平台"], tools: ["cmdb_asset_*"] }
    allow_in: []
```

约定：

- `tools` 只支持 `*` 通配（前缀/中缀/后缀），不支持 `?` / `[]`，避免运维猜语法。
- 匹配对象是 MCP 服务器上报的**原始工具名**，不是注册后带哈希的函数名（`mcp_<service>_<tool>_<hash>`）——后者运维无从得知。
- `chat_id` 精确匹配；`"*"` 表示该 `chat_type` 下任意会话（用于"允许所有群、仅禁私聊"）。
- 配置按文件名约定 `mcp-scope.yaml`，路径由 `MCP_CHAT_SCOPE_FILE` 指定；未设置该环境变量 = 功能关闭。

### 3.3 匹配与优先级

对每个 `(service, tool)` 计算：

```
denied   = 存在 effect=deny 且 enabled 且 match 命中的规则                    → 拒绝（最高优先级）
scoped   = 存在 effect=allow 且 enabled 且 match 命中的规则
allowed  = !scoped || chat ∈ ⋃(命中规则的 allow_in)      # 多条 allow 规则取并集
否则                                                                          → 既有判定链
```

要点：

1. **deny 全局优先**：不受会话影响，可直接当熔断开关；这也是唯一能压过 allow 的手段。
2. **多条 allow 规则取并集**：避免"同工具在不同规则里各自授予不同群"时需要合并表达式。
3. 规则命中但 `allow_in` 为空 → 等价于"该工具在 IM 中全面禁用"，语义明确，允许使用。
4. 匹配不区分大小写；服务名比对使用 `TrimSpace` 后的精确值，不做模糊匹配（防止"CMDB 资产"误命中"CMDB 资产平台"）。

### 3.4 上下文传递与四道强制点

```
飞书事件
  chat_id / chat_type            longconn.go:199-207（私聊 chat_id 为空，仅记 chat_type）
        │
        ▼
IncomingMessage → HandleMessage  im/service.go
        │
        ▼
buildIMQARequest                 im/service.go:549（新增 IM 来源字段）
        │
        ├─ ctx = types.WithMCPChatScope(ctx, scope)      ← 新增，2 行
        ▼
AgentQA → BuildEngine → registerMCPTools → NewMCPCatalog(读 ctx 中的 scope)
        │
        ▼
工具可见性 / 调用 / 执行  ← 四处判定
```

| # | 强制点 | 位置 | 效果 | 改动量 |
| --- | --- | --- | --- | --- |
| 1 | `MCPCatalog.visibleTools` | `mcp_catalog.go:319` | `describe` / `list_tools` / 注册发布都不出现被禁工具 | ~4 行 |
| 2 | `MCPCatalog.checkEnabled` | `mcp_catalog.go:368` | `call_mcp_tool("tool_ref")` 被拒，防止绕过清单直接构造引用 | ~5 行 |
| 3 | `MCPTool.Execute` | `mcp_tool.go:146` | 执行前复核，与 `gate.IsEnabled` 同处；防引擎跨配置变更存活 | ~6 行 |
| 4 | `MCPDiscoverTool` 的 `list_servers` | `mcp_catalog.go:661` | 整服务被禁时不列出，避免模型反复试探耗尽 `MaxIterations` | ~4 行 |

第 1 项自动覆盖 `RefreshMCPTools`（`:279`）与 describe（`:705`），无需单独处理。

### 3.5 与既有机制的关系（全部为"与"逻辑）

| 既有机制 | 位置 | 本方案的关系 |
| --- | --- | --- |
| Agent 的 MCP 服务选择（`all`/`selected`） | `agent_service.go:420-490` | 先决定"本轮装了哪些服务"，再用规则在服务内部裁工具 |
| 工具级 `enabled` | `types/mcp.go:180`、`approval.Gate.IsEnabled` | 两者都必须通过；任一否决即不可用 |
| 风险分级 / 只读访问级别 | `mcp_catalog.go:343` | 叠加，不替代 |
| 人工审批（HITL） | `mcp_tool.go:186` | 不受影响；白名单内仍需审批 |
| 发送者 ACL（机器人消息） | `im/sender_acl.go` | 无关，但在同一层，可复用其"包级配置 + 启动加载"实现风格 |

---

## 4. 失败模式与默认行为

| 情形 | 处理 | 理由 |
| --- | --- | --- |
| 未设置 `MCP_CHAT_SCOPE_FILE` | 功能关闭，行为与当前完全一致 | 零影响的升级路径 |
| 启动时文件存在但解析/校验失败 | **进程启动失败**（Fatal，带行号） | 配置是运维显式挂载的；语法错误应在发布阶段暴露，而不是静默失效 |
| 运行期热加载失败 | 保留上一次有效快照 + Error 日志 + 指标自增 | 单个 ConfigMap 编辑错误不该导致机器人不可用 |
| 规则引用了不存在的服务名 | 启动告警（不阻断），该规则永不命中 | 允许先写配置、后建服务 |
| 规则覆盖的服务被禁用 | 自然无效，无需额外处理 | 由既有 `Enabled` 决定 |
| 运行 ctx 中无 IM 身份（Web/API/定时） | 按 `outside_im` 处理（默认 `inherit`） | 见 §10 决策点 2 |

---

## 5. 可观测性

**结构化日志**（拒绝时一行，含定位所需全部要素）：

```
[MCP-SCOPE] denied tool=cmdb_asset_update service="CMDB 资产平台"
            rule=cmdb-write-ops-only platform=feishu chat_type=group chat=oc_xxxx
```

**指标**（复用已有的 `internal/metrics` 自建体系）：

| 指标 | 类型 | 用途 |
| --- | --- | --- |
| `weknora_mcp_scope_denied_total{rule,service,tool}` | counter | 观察是否有群在频繁撞墙（多为配置遗漏） |
| `weknora_mcp_scope_reload_total{result}` | counter | 热加载成功/失败 |
| `weknora_mcp_scope_rules` | gauge | 当前生效规则数 |

**返回给模型的文案**必须明确"不可用且不要重试"，例如：

> 该工具在当前会话不可用（策略限制）。不要重试，也不要换用其他工具绕过；直接告知用户此能力需要在指定群聊中使用。

否则 Agent 会在 `MaxIterations=40` 的预算里反复试探，既浪费 token 又拖长回答。

---

## 6. 边界情形

| 情形 | 行为 | 说明 |
| --- | --- | --- |
| 飞书私聊 | `ChatID` 为空、`ChatType=direct`（`longconn.go:199-207`） | 未显式配置 `chat_type: direct` 时，受限工具在私聊**不可用** |
| 群内多用户 | 会话按 `(platform, user, chat, agent)` 分 | 同一群的不同用户命中同一规则，符合预期 |
| 线程模式 | `ThreadID` 不同、`ChatID` 相同 | 仍命中规则 |
| Web 打开 IM 创建的会话 | 无 IM 来源戳 | 默认走原规则（`outside_im: inherit`）；要封堵则设 `deny` |
| 定时任务 / 自动化 / 委派子会话 | 无 IM 来源戳；委派子会话继承父 ctx | 同上 |
| 审批后恢复（resume） | `ApprovalCtx` 派生自请求 ctx | 白名单信息不丢 |
| 工具定义元数据缓存 | 按 `(tenant, service)` 缓存，与策略无关 | 过滤发生在缓存之后，不会把受限工具定义泄漏给模型 |
| 多副本部署 | 每个 Pod 各自读挂载文件 | 共享 ConfigMap 时天然一致；本地手动改文件则需同步（不推荐） |

**一个必须说明的定位问题**：群聊在本系统里**不是授权主体**。Principal 是用户，会话是 `(用户, 群, Agent)` 三元组。因此本方案是"暴露面控制"，不是"身份鉴权"——租户内的成员仍可通过 Web 直接使用同一工具，除非开启 `outside_im: deny`。若需要"运维群里普通成员也不能用写工具"，应叠加现有的访问级别/审批，而不是把用户维度塞进本方案。

---

## 7. 升级成本（相对上游）

| 文件 | 上游近 90 天改动 | 本方案插入 | 冲突概率 |
| --- | --- | --- | --- |
| `internal/agent/mcpscope/`（**新包**） | — | 全部逻辑 | 无 |
| `internal/types/mcp_scope.go`（**新文件**） | — | ctx 键 + 结构体 | 无 |
| `internal/agent/tools/mcp_catalog.go` | 7 | ~13 行（4 处） | 低，插入点均为独立函数首部 |
| `internal/agent/tools/mcp_tool.go` | 13 | ~6 行（1 处） | 低 |
| `internal/im/service.go` | 18 | ~2 行（两条链路各 1 行 ctx stamp） | 低 |
| `internal/application/service/agent_service.go` | **58** | **0** | 无 |
| `internal/application/service/session_agent_qa.go` | **42** | **0** | 无 |

关键取舍：**不改最容易冲突的两个编排文件**。群聊身份在 IM 侧 stamp 进 ctx，后续全部通过 ctx 传递，因此 `BuildEngine` 与 `AgentQA` 一行都不用动。

> 备选（零 IM 改动）：让工具层按 `sessionID` 反查 `im_channel_sessions.chat_id`。代价是 agent 层依赖 IM 表结构、每轮多一次查询、失败时静默降级为"不限制"——不如显式 stamp 可审计，故不推荐。

---

## 8. 备选方案对比

| 方案 | 可行性与代价 |
| --- | --- |
| **每群一个 Agent**（仅用现有能力） | 不可行。IM 渠道（`im_channels`）只绑定一个 `agent_id`，粒度是"机器人 × 渠道"，不是群。要实现只能给每个群单独申请机器人，运维不可接受 |
| **DB 表 + 管理 UI** | 正规，支持多租户自助配置与审计。代价：迁移 + 仓储 + 接口 + 前端，且跨越上游 schema，升级成本最高。建议作为二期（若其他团队也要用） |
| **独立 MCP 网关做统一鉴权** | 不可行。网关看不到"群"这个维度，MCP 调用本身不携带 IM 会话信息 |
| **按工具 `enabled` 全租户关闭** | 粒度太粗，一关全关，无法满足"只在某些群可用" |

---

## 9. 分期与工作量

| 阶段 | 内容 | 交付物 | 估算 |
| --- | --- | --- | --- |
| P0 | `mcpscope` 包：schema、校验（含行号报错）、glob 匹配、deny/allow 优先级、mtime 热加载 | 单测覆盖：通配、并集、deny 优先、空 `allow_in`、坏配置、未知服务 | 0.5 人日 |
| P1 | ctx 键 + IM 两处 stamp + 四处强制点 | 单测：白名单内可见、白名单外不可见、代理调用被拒、执行期复核 | 0.5 人日 |
| P2 | 日志、指标、模型侧文案、Helm 挂载示例、运维手册 | 验收清单 + 现网灰度验证 | 0.5 人日 |

合计约 **450 行生产代码 + 300 行测试，1.5 人日**。

### 验收方式

1. 不挂配置文件 → 行为与当前完全一致（回归基线）；
2. 挂载规则 → 白名单群内工具正常，其他群 `describe` 看不到该工具，强行 `call_mcp_tool` 被拒且文案明确；
3. 改 ConfigMap 后（≤2 分钟内）新策略生效，无需重启；
4. 塞入一条语法错误 → Pod 启动失败并给出行号；运行期塞错 → 保留旧策略 + 告警。

---

## 10. 待确认的决策点

1. **语义**：确认采用解读 A（工具为中心的白名单收紧）？还是你实际想要 B（每个群维护完整工具清单）？
2. **非 IM 来源**：Web / API / 定时任务调同一工具时，默认 `inherit`（沿用原规则，可用）还是 `deny`（一并禁用）？这是本方案唯一的安全强度开关。
3. **是否需要用户维度**：群里所有成员是否都可以用？若需要"仅群内特定角色可用"，那属于访问级别/审批层，应单独评估，不建议并入本方案。
4. **配置载体**：挂载 YAML（推荐）还是最终要落到 DB + UI？若已预期其他团队共用，建议直接二期规划 DB 版。
