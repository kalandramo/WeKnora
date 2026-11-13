# watsons 分支定制能力清单与升级指南

> 目的：把 `watsons` 分支相对上游的 29 个定制提交按主题聚类，标注每簇的冲突风险与升级处理方式，方便后续升级版本时快速 rebase。
> 本文不提交 issue / PR，仅作内部升级参考。
> **2026-11-12 增补**：新增簇 H（Qwen chat template 兼容，模型级可选开关，提交 `872a93c55`）。该簇尚未计入上方「29 提交」统计。
> **2026-11-13 增补**：簇 H 新增「Helm 部署落地说明」小节（明确 `config/models.json` 是文件非数据库；UI「协议兼容覆盖(JSON)」= 数据库 `spec.compat`，已现网验证 400 消失；provider 级 Helm 挂载为备忘、未采用）。同日另有构建修复提交 `54b933c56`（anydoc LTO strip，见簇 I 候选），本文元数据计数尚未刷新。

---

## 0. 元信息（实测，2026-11-09）

| 项 | 值 |
| --- | --- |
| 上游基线 | `f17ee8a8d` — v0.9.0 release notes（#4864） |
| 本地 HEAD | `89c6dfea7` — `fix(agent): require platform on chat-scope entries` |
| 定制提交数 | 29（无合并提交） |
| 改动总量 | 53 文件，+5803 / −61 |
| 新增文件 | 31（绝大多数是测试与自有新包，冲突风险低） |
| 修改既有文件 | 22（冲突风险源） |
| 提交时间跨度 | 2026-10-15 → 2026-11-05 |
| 上游 main 现状 | `4eea8b8f1`（持续 129+ 提交领先基线） |

**关键事实**
- PR #5159（Helm `app.podAnnotations`）已于 **2026-11-09 合入上游 main**。上游 `helm/values.yaml:178`、`helm/templates/app.yaml:40`、`helm/README.md:303` 已含该功能。→ watsons 的 `9a90b039b` 在 rebase 时必须**丢弃**。
- MCP 群聊授权的设计已在上游建 issue #5157（OPEN），但代码**未**回流上游，仍需保留在 watsons。

---

## 1. 升级总体策略

**不要用 `git merge upstream/main` 把上游合进 watsons**（会产生大量无意义的合并提交，且每次升级都重演冲突）。推荐做法：

1. 从目标上游版本（如新的 `main` 或某个 release tag）新建临时分支 `watsons-rebase`。
2. **按簇顺序**将下面的定制提交 cherry-pick 过去（每簇内部保持提交顺序）。
3. 遇到冲突，优先看本文第 3 节的「冲突热点」与该簇的「处理建议」。
4. 验证：`go build ./...`（注意 duckdb cgo 环境限制，见下）、`go test ./internal/im/... ./internal/agent/...`、Helm `helm template` 渲染。
5. 本地 `git branch -m watsons watsons-<oldver>`，把 `watsons-rebase` 改名为 `watsons` 并强制推送（需团队协调）。

**环境注意**：Windows 本地 `go build ./...` 会因 duckdb 的 cgo 静态库缺 pthread 链接失败，与定制无关。用 `go vet ./internal/im/...` + 分包 `go test` 验证即可；完整编译在 CI / 容器内进行。

---

## 2. 按簇梳理（29 提交 → 7 簇 + 簇 H）

风险等级口径：**Trivial**（1 行级）/ **Low**（新增文件为主 + 1 处钩子）/ **Medium**（改动上游高频文件或重构既有逻辑）/ **High**（结构性重写且上游可能独立演进）。

### 簇 A — 飞书结构化流式卡片（折叠执行详情 + 时间线 + 节流 + 占位）
- **提交**：`92f66f7f7` `1e55b1982` `169972345` `4471fe3c8` `0cc53ee5a` `999c46ce1` `9ee6ec337`（文档）
- **新增文件**：`internal/im/feishu/region_throttle_test.go`、`stream_answer_region_test.go`、`stream_summary_test.go`、`internal/im/structured_stream_test.go`、`CHANGE_FEISHU_STRUCTURED_STREAM.md`
- **修改文件**：`internal/im/adapter.go`、`internal/im/feishu/adapter.go`、`internal/im/feishu/longconn.go`、`internal/im/service.go`、`internal/im/think.go`、`internal/im/tool_display.go` 及对应 `_test.go`
- **上游自基线改动**：`feishu/adapter.go` 1、`think.go` 1、`tool_display.go` 1、其余 0
- **规模**：生产代码约 1592 行 + `service.go` 约 177 行，是最大的一簇
- **风险**：**Medium**（体量大但文件层面冲突少；真正风险在于上游可能已独立重构流式路径，需逐帧比对行为）
- **处理建议**：作为一整个提交栈保留并 cherry-pick。升级时重点核对上游是否新增了原生结构化流式——若有，需做语义对齐而非机械合并。

### 簇 B — 飞书卡片标题品牌化（标题 WeKnora → Infra 智能助手）
- **提交**：`a6e787964`
- **修改文件**：`internal/im/feishu/region.go`（1 行）
- **上游自基线改动**：0
- **风险**：**Trivial**
- **处理建议**：一行改动，cherry-pick 即可。注意品牌名属内部标识，如上游化需脱敏。

### 簇 C — IM 入站过滤（@所有人丢弃 + thinking 帧静默 + bot-sender 白名单）
- **提交**：`b97074b77`（at_all）、`09ed9ff0d`（hideThinking）、`2ff4dbb67`（bot sender_type 修正）、`e9a2e4e90`（bot-sender 白名单）
- **新增文件**：`internal/im/at_all.go(+test)`、`internal/im/sender_acl.go(+test)`、`internal/im/stream_visibility.go(+test)`
- **修改文件**：`internal/im/service.go`（钩子 1 处）、`internal/im/feishu/longconn.go`、`internal/im/feishu/adapter.go`
- **上游自基线改动**：`service.go` 0、`longconn.go` 0、`feishu/adapter.go` 1
- **风险**：**Low**（基本是新增文件 + service.go 一处钩子）
- **处理建议**：整簇保留。bot-sender 修正（`2ff4dbb67`，把 `sender_type="bot"` 也纳入判定）是真实缺陷修复，优先合入。

### 簇 D — MCP 群聊授权（工具级会话白名单）
- **提交**：`240a4ba61`、`124a0d62b`（文档）、`89c6dfea7`
- **新增文件**：`internal/agent/mcpscope/`、`internal/application/repository/mcp_chat_scope_repository.go(+test)`、`internal/handler/mcp_chat_scope.go(+test)`、`internal/router/routes_mcp_chat_scope.go`、`internal/types/mcp_chat_scope.go`、迁移 4 个（PG `000135` + SQLite `000052`）
- **修改文件**：`internal/container/container.go`（**7 次上游改动，最高风险**）、`internal/router/router.go`（1）、`internal/im/service.go`（0）
- **上游自基线改动**：`container.go` 7、`router.go` 1、`service.go` 0
- **风险**：**Medium**（装饰器主体是自有新文件，但 `container.go` 的装配点是上游高频文件）
- **处理建议**：保留。rebase 时 `container.go` 的 Provide / Gate 包装饰器两处插入最可能冲突，按「保持装饰器结构、只重接装配点」原则处理。迁移文件注意版本号不与上游冲突（PG `000135` / SQLite `000052` 当前未被上游占用，升级时再核对一次）。
- **回流状态**：设计 issue #5157 已在上游，代码未合并，仍属 watsons 私有。

### 簇 E — 可观测性（Prometheus /metrics + 按模型标注的 LLM 并发指标）
- **提交**：`0b7e48516`、`a0a31db7e`、`88721da0e`、`a98cd1b34`
- **新增文件**：`internal/metrics/metrics.go(+test)`、`internal/models/api/openaicompletions/client_metrics_test.go`、`internal/models/limiter/metrics_publish_test.go`
- **修改文件**：`internal/models/api/openaicompletions/client.go`（1）、`internal/models/limiter/governor.go`（1）、`internal/models/limiter/limiter.go`（0）、`internal/middleware/logger.go`（2）、`internal/middleware/auth.go`（0）、`cmd/download/duckdb/duckdb.go`（0）
- **上游自基线改动**：`logger.go` 2、其余 ≤1
- **风险**：**Medium-Low**（新包 `internal/metrics` 自包含；但改动了 limiter / openaicompletions client 的内部，上游若重构限流会冲突）
- **处理建议**：`internal/metrics` 包整体保留（自包含、零上游逻辑耦合）。limiter 与 client 的埋点改动（各约 10 行）cherry-pick 时重点比对上游是否改过限流语义。
- **附带影响**：`go.mod` 将 `prometheus/client_golang` 从 indirect 提升为 direct（metrics 包直接引用）。见簇 F 的 go.mod 说明。

### 簇 F — 内网 / 离线构建适配（DuckDB 扩展仓库、apt / Rust / cargo / browserskill 源）
- **提交**：`079b1b460`、`d6fb3ce4d`、`a8af6fcd3`、`8d1f250cd`、`6038b11a2`、`ec3c2e697`、`e4641754f`、`2dbdfcdd0`
- **新增文件**：`cmd/download/duckdb/duckdb_test.go`
- **修改文件**：`docker/Dockerfile.app`（2）、`cmd/download/duckdb/duckdb.go`（0）、`scripts/build_browserskill.sh`（1）、`go.mod`（2）
- **上游自基线改动**：`Dockerfile.app` 2、`go.mod` 2、`browserskill.sh` 1、`duckdb.go` 0
- **风险**：**Low-Medium**（纯构建期改动，不影响运行时；但 `go.mod` 与 `Dockerfile.app` 是升级高频文件）
- **处理建议**：
  - `go.mod`：本簇只改了 `prometheus/client_golang` 的 // indirect 注释（见簇 E）。升级时若上游已改动 go.mod（版本提升、依赖增删），必须重做 `go mod tidy`，**不能原样 cherry-pick go.mod**——应 cherry-pick 源码改动后用 `go mod tidy` 让工具链重新生成。
  - `Dockerfile.app`：镜像源 / 扩展仓库均为环境特定值，上游若重构 Dockerfile 会冲突，建议当成「环境补丁」单独维护，必要时手工重贴。
  - 其余（duckdb.go 参数化、browserskill.sh 源覆盖）为良好封装的改动，冲突风险低。

### 簇 G — Helm 配置扩展（podAnnotations）— ⚠️ 已上游化，须丢弃
- **提交**：`9a90b039b`（仅存在于 watsons 历史）
- **状态**：对应 PR #5159 已于 **2026-11-09 合入 upstream/main**，上游已含 `helm/values.yaml:178`、`helm/templates/app.yaml:40`、`helm/README.md:303`。
- **风险**：如保留会**重复定义 + 冲突**。
- **处理建议**：**rebase 时跳过 `9a90b039b`**，不要 cherry-pick。上游版本已含 README 参数表，比 watsons 本地提交更完整。

### 簇 H — Qwen chat template 兼容（`single_leading_system`）— 2026-11-12 新增
- **提交**：`872a93c55`（feat(models): add single_leading_system compat for Qwen chat templates）
- **问题**：Qwen 3.6/3.8 系列的 `chat_template.jinja` 含硬校验——除 `messages[0]` 外，任何 `system`/`developer` 消息都会触发 `raise_exception('System message must be at the beginning.')`，vLLM/SGLang 在渲染 prompt 阶段即返回 400，请求到不了模型。WeKnora 有两处会制造非首位 system：
  1. `openaicompletions/request.go` 把 `RuntimeNotice`（空响应重试、协作事件、最终答案合成）作为 `system` **追加到数组末尾**——这是刻意设计，为保持 prompt cache 前缀（见 `options.go` 注释与 `runtime_notice_test.go`）；
  2. `agent/engine.go`、`agent/collaboration.go` 在恢复/协作时直接往 live transcript 追加 `system` 指令。
- **改动文件**（5 个，全部是既有文件，无新增包）：
  | 文件 | 改动 |
  | --- | --- |
  | `internal/models/api/compat_settings.go` | `OpenAICompletionsCompat` / `OpenAICompletionsSettings` 增加同名字段 `SingleLeadingSystem`（默认 false） |
  | `internal/models/api/openaicompletions/request.go` | 开关开启时 RuntimeNotice 追加为 `user`；出线点新增 `demoteTrailingInstructions`，把 index>0 的 `system`/`developer` 降级为 `user` |
  | `.../openaicompletions/golden_test.go`、`internal/models/api/runtime_notice_test.go`、`internal/models/runtime/overlay_test.go` | 单测（含默认行为不变、cache 前缀等价、compat 解码与 overlay 生效） |
- **开启方式**（`config/models.json`；vendor 级 `compat` 无条件生效，也可写在 model 级 `compat`）：
  ```json
  {
    "providers": {
      "self-hosted-qwen": {
        "api": "openai-completions",
        "base_url": "http://<endpoint>/v1",
        "compat": { "single_leading_system": true },
        "models": [{ "id": "Qwen/Qwen3-32B" }]
      }
    }
  }
  ```
  对已有 provider（如 `aliyun`）可用 `models` 打补丁：`{"id": "<model-id>", "compat": {"single_leading_system": true}}`（upsert 是 patch 语义，不会覆盖其它字段）。文件位置为 `config/models.json`，可用 `MODELS_CONFIG` 覆盖。
- **模型从 UI / API 创建时**（compat 存在模型行的 `spec` 字段，而非 `config/models.json`）：
  ```json
  {
    "spec": {
      "api": "openai-completions",
      "compat": { "single_leading_system": true }
    }
  }
  ```
  两条路径分别由 `applyRawCompat(overrideCompat)` 与 vendor compat 合并，优先级为「协议默认 → vendor → catalog 条目 → 模型行 override」。已为两条路径各加解析测试。

**Helm 部署落地说明**（2026-11-13 补）
- **`config/models.json` 是文件，不是数据库**：它位于容器文件系统，WeKnora 启动时读取（优先 `MODELS_CONFIG` 环境变量，否则默认 `config/models.json`），解析后在内存做覆盖合并，**不写回数据库**。Helm chart **没有原生 value** 暴露这个 compat 字段。
- **UI 字段配置（已验证，2026-11-13）**：在 WeKnora 界面打开该 Qwen 模型 →「协议兼容覆盖(JSON)」填 `{"single_leading_system": true}` → 保存，即写入模型行 `spec.compat`（数据库 `models` 表）。该方式零 Helm 改动、可审计，**已在现网验证 400 `System message must be at the beginning.` 不再出现**。Helm 部署推荐走这条路径。
- **provider 级替代（未采用，仅备忘）**：若需对整租 provider 统一开启，可借 watsons 已合入的 `app.extraVolumes`/`extraVolumeMounts`/`extraEnv`（Fixes #4331）挂载含 provider `compat` 片段的 ConfigMap 并设 `MODELS_CONFIG`，属覆盖合并、只放片段即可。本次未采用，仅作备份参考。

- **设计约束**：
  - **默认关闭**：未配置的模型与其它厂商行为完全不变，现有 golden 测试和 `TestRuntimeNoticePreservesWireCachePrefix` 原样通过。
  - **只改 role，不合并内容**：首位 system 及其后所有既有 wire 消息保持字节不变，RuntimeNotice 原有的 cache 前缀等价性仍成立；运行时内容全部保留，不丢上下文。
  - 归一化放在**协议出线点**，一处兜住 RuntimeNotice 与 agent 引擎直接追加的 system，无需改动 agent 层。
  - 仅作用于 **OpenAI Chat Completions** 协议；其它适配器本就把 RuntimeNotice 追加为 `user`/`developer`，不受影响。
  - 首位 instruction 保持原样（若模型另配了 `supports_developer_role`，首位会是 `developer`；本开关不处理首位，需按 Qwen 模板实际接受情况配置）。
- **风险**：**Low**（生产代码约 34 行、集中在 2 个文件；但 `compat_settings.go` 与 `openaicompletions/request.go` 属上游协议层，升级时若上游重构 compat 字段或 `buildBody` 需重贴）。
- **处理建议**：整簇保留。升级时先确认上游是否已原生处理该约束（例如把 RuntimeNotice 改为 user 或提供同类开关）；若已处理，本簇可丢弃。
- **回流状态**：未提 issue / PR（按要求保持 watsons 私有）。

---

## 3. 冲突热点排名（修改的既有文件，按上游自基线改动次数）

| 上游改动次数 | 文件 | 所属簇 | 升级处理 |
| --- | --- | --- | --- |
| **7** | `internal/container/container.go` | D | 重接装饰器装配点，保持结构 |
| 2 | `internal/middleware/logger.go` | E | 比对上游日志改动 |
| 2 | `go.mod` | E/F | **重做 tidy**，勿原样 cherry-pick |
| 2 | `docker/Dockerfile.app` | F | 环境补丁，手工重贴 |
| 1 | `internal/router/router.go` | D | 重加路由注册 1 行 |
| 1 | `scripts/build_browserskill.sh` | F | 低 |
| 1 | `internal/models/limiter/governor.go` | E | 比对限流语义 |
| 1 | `internal/models/api/openaicompletions/client.go` | E | 比对埋点 |
| 待核对 | `internal/models/api/compat_settings.go` | H | 上游若已加同名字段或重构 compat，需重贴 |
| 待核对 | `internal/models/api/openaicompletions/request.go` | H | 比对 `buildBody` / `convertMessages` 是否被重构 |
| 1 | `internal/im/tool_display.go` / `_test.go` | A | 比对上游是否重构流式 |
| 1 | `internal/im/think.go` | A | 同上 |
| 1 | `internal/im/feishu/adapter.go` | A/C | 同上 |
| 1 | `helm/values.yaml`、`helm/templates/app.yaml` | G | **丢弃**（已上游化） |
| 0 | `internal/im/service.go`、`internal/im/adapter.go`、`internal/im/feishu/longconn.go`、`internal/im/feishu/region_test.go`、`internal/im/feishu/longconn_test.go`、`internal/middleware/auth.go`、`internal/models/limiter/limiter.go`、`cmd/download/duckdb/duckdb.go` | 多簇 | 冲突概率低，常规 cherry-pick |

> 注：上表「上游改动次数」是**自基线 `f17ee8a8d` 起**的实测值。从更长周期看，`container.go` / `router.go` / `mcp_*.go` / `types/mcp.go` / `gate.go` / `im/service.go` 一直是上游高频文件，未来版本这些文件的冲突概率仍最高，处理相应簇时务必人工介入。

---

## 4. 已回流 / 可回流上游的部分（避免重复劳动）

| 能力 | 上游状态 | watsons 动作 |
| --- | --- | --- |
| Helm `app.podAnnotations` | **PR #5159 已合并**（2026-11-09） | rebase 时**丢弃** `9a90b039b` |
| Helm `app.extraVolumes` | PR #4332 已合并（更早） | 早已不在 watsons |
| MCP 群聊授权（设计） | issue #5157 OPEN | 设计已同步；代码保留 watsons |
| MCP 群聊授权（实现） | 未实现 / 未合并 | 保持自有实现 |
| Qwen `single_leading_system` 兼容（簇 H） | 未回流（未提 issue / PR） | 保持 watsons 私有；升级时先查上游是否已原生修复 |

---

## 5. 升级检查清单

- [ ] 从目标上游版本新建 `watsons-rebase` 分支
- [ ] 跳过簇 G（`9a90b039b`），已上游化
- [ ] 按 A → B → C → D → E → F → **H** 顺序 cherry-pick 其余提交栈（G 跳过）
- [ ] `container.go`（簇 D）装配点冲突 → 重接装饰器，不删逻辑
- [ ] `go.mod`（簇 E/F）冲突 → 丢弃 go.mod 改动，cherry-pick 源码后 `go mod tidy`
- [ ] `Dockerfile.app`（簇 F）冲突 → 手工重贴镜像源 / 扩展仓库
- [ ] 迁移版本号（`000135` / `000052`）与上游当前最大版本无冲突
- [ ] `go vet ./internal/im/... ./internal/agent/...` 通过
- [ ] `go test ./internal/im/... ./internal/agent/mcpscope/ ./internal/agent/approval/ ./internal/application/repository/` 通过
- [ ] `go test ./internal/models/api/... ./internal/models/runtime/...` 通过（`runtime` 包的 `TestApplyOverlay_IconRejectsUncontainedPaths` 在 Windows 上是既有平台失败，与定制无关）
- [ ] 簇 H：确认 `config/models.json` 已为 Qwen 模型配置 `single_leading_system: true`，其它模型保持默认关闭
- [ ] 现网复测：触发一次「空响应重试 / 长答案截断」路径，确认不再出现 400 `System message must be at the beginning.`
- [ ] `helm template` 渲染验证（确认 podAnnotations 来自上游、未重复）
- [ ] 现网验证：飞书卡片折叠 + 占位、@所有人丢弃、bot 互答拦截、MCP 群聊白名单、/metrics 端点
- [ ] 重打镜像 tag（沿用递增规则），`helm upgrade` 部署
