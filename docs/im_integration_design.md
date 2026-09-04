# WeKnora IM 对接设计文档

> 适用范围：`internal/im` 包及其各平台子目录（`feishu` / `wecom` / `dingtalk` / `telegram` / `slack` / `qqbot` / `wechat` / `mattermost` / `yunzhijia`）。
> 目标：把多种即时通讯（IM）平台接入 WeKnora 的问答（QA）能力，复用既有 session / message / knowledge 服务，对用户保持统一的对话体验。

---

## 1. 设计目标与原则

1. **平台无关的核心层**：所有平台共用一个 `im.Service`，平台差异被收敛进"适配器（Adapter）"。
2. **最小改动复用**：直接调用既有的 `SessionService` / `MessageService` / `KnowledgeService` / `StreamManager`，IM 层只做"协议转换 + 会话映射 + 输出规整"，不重复实现问答逻辑。
3. **可插拔渠道**：每个平台用 `AdapterFactory` 注册，新增平台只需实现一个 `Adapter` 并在启动时 `RegisterAdapterFactory`。
4. **多实例安全**：WebSocket / 长轮询类渠道通过 Redis 分布式锁做"领导者选举"，保证同一渠道只有一个实例维护长连接；停止指令跨实例通过 Redis 传递。
5. **可观测、可中断**：QA 请求进入有界队列 + 工作池；支持 `/stop` 取消正在进行的 ReAct 推理；命令与问答分流清晰。

---

## 2. 分层架构

```
                  ┌──────────────────────────────────────────────┐
                  │                  IM 平台                       │
   (钉钉/飞书/企微/Telegram/Slack/QQ/微信/ Mattermost / 云之家)      │
                  └───────────────┬──────────────────────────────┘
                                  │ 平台原生协议 (webhook / ws / longpoll)
                                  ▼
                  ┌──────────────────────────────────────────────┐
                  │            Adapter（每平台一个）               │
                  │  - 接收原生消息 → *IncomingMessage              │
                  │  - 发送回复   ← *ReplyMessage                  │
                  │  - 可选：StreamSender / FileDownloader         │
                  └───────────────┬──────────────────────────────┘
                                  │ HandleMessage(msgCtx, msg, channelID)
                                  ▼
                  ┌──────────────────────────────────────────────┐
                  │            im.Service（核心层）                 │
                  │  - 渠道生命周期（加载/启动/停止/热重载）         │
                  │  - 消息去重 / 限流                             │
                  │  - 命令解析（CommandRegistry）                 │
                  │  - 会话解析（user / thread 模式）              │
                  │  - QA 队列 + 工作池（qaQueue）                 │
                  │  - 流式 / 全量输出规整                         │
                  │  - 附件下载与写入知识库                         │
                  └───────────────┬──────────────────────────────┘
                                  │ 复用既有领域服务
                                  ▼
   ┌──────────────┬──────────────┬────────────────┬─────────────────┐
   │ SessionSvc   │ MessageSvc   │ KnowledgeSvc   │ StreamManager   │
   └──────────────┴──────────────┴────────────────┴─────────────────┘
                                  │
                                  ▼
                          PostgreSQL / Redis / 对象存储
```

---

## 3. 核心数据模型

定义在 `internal/im/types.go`。

### 3.1 `IMChannel`（表 `im_channels`）
一个"渠道"绑定一个 Agent，并持有平台凭证。

| 字段 | 说明 |
|------|------|
| `TenantID` / `AgentID` | 租户与绑定的智能体 |
| `Platform` | 平台标识（`feishu`/`wecom`/`dingtalk`/`telegram`/`slack`/`qqbot`/`wechat`/`mattermost`/`yunzhijia`） |
| `Mode` | 接入模式：`websocket` / `webhook` / `longpoll`（默认值按平台而定，`mattermost`/`yunzhijia` 默认 `webhook`） |
| `OutputMode` | `stream`（流式）或 `full`（全量后发送） |
| `KnowledgeBaseID` | 可选，收到文件附件时自动写入该知识库 |
| `SessionMode` | `user`（按 用户+聊天 区分会话）或 `thread`（按 话题 区分会话） |
| `Credentials` | 平台凭证，JSONB 存储，对外列表接口一律脱敏 |
| `BotIdentity` | 由平台 + 凭证派生的唯一机器人身份（带唯一索引，防止同一机器人重复接入） |

> `BotIdentity` 是并发安全的关键：在 `BeforeCreate` / `BeforeSave` 中通过 `computeBotIdentity()` 从凭证计算（如 `wecom:ws:<bot_id>`、`telegram:<bot_id>`、`feishu:<app_id>`），用于去重与跨实例识别。

### 3.2 `ChannelSession`（表 `im_channel_sessions`）
把"IM 平台的一路对话（用户×聊天 / 话题）"映射到"一个 WeKnora session"。

| 字段 | 说明 |
|------|------|
| `Platform` / `UserID` / `ChatID` / `ThreadID` | 平台侧定位键 |
| `SessionID` | 对应的 WeKnora 内部会话 |
| `TenantID` / `AgentID` / `IMChannelID` | 归属 |
| `Status` / `Metadata` | 状态与扩展 |

会话解析策略（`resolveSession`）：

- **User 模式**（`SessionModeUser`）：`resolveUserSession` —— 同一用户在同一聊天内的消息共享会话（群聊中按 `UserID+ChatID` 隔离）。
- **Thread 模式**（`SessionModeThread`）：`resolveThreadSession` —— 同一话题内的消息共享会话（适合多人协作，如 Slack 线程、飞书话题）。

`/clear` 通过软删除当前 `ChannelSession` 实现"开新会话"，历史由 WeKnora 会话库按需重建，无需额外缓存失效逻辑。

---

## 4. 适配器（Adapter）抽象

`internal/im/adapter.go` 定义平台无关接口：

```go
type Adapter interface {
    // 发送最终/普通回复
    SendReply(ctx, msg *IncomingMessage, reply *ReplyMessage) error
    // 可选能力（通过类型断言判断）
}

// 可选：流式发送
type StreamSender interface {
    StartStream(ctx, msg) (streamID string, err error)
    UpdateStreamContent(ctx, msg, streamID, content) error
    FinalizeStream(ctx, msg, streamID, content) error
    EndStream(ctx, msg, streamID) error
}

// 可选：文件下载
type FileDownloader interface {
    DownloadFile(ctx, url, token string) (io.ReadCloser, string, error)
}
```

- 所有平台适配器通过编译期断言确保接口落实，例如 `dingtalk/adapter.go`：
  ```go
  var _ im.Adapter = (*Adapter)(nil)
  var _ im.StreamSender = (*Adapter)(nil)
  var _ im.FileDownloader = (*Adapter)(nil)
  ```

### 4.1 两种接入模式

| 模式 | 典型平台 | 运作方式 |
|------|----------|----------|
| `websocket` | 飞书、企微(WS)、Telegram、钉钉、Slack、QQ、微信 | Adapter 维持长连接，主动收消息；需 Redis 领导者选举避免多实例双写 |
| `webhook` | 钉钉(可选)、Mattermost、云之家 | 平台回调 HTTP 接口，Adapter 仅暴露回调端点与发送 API |
| `longpoll` | 企微(部分) | 长轮询，停止时保留锁 TTL 以避免新旧实例双写窗口 |

Adapter 由 `AdapterFactory` 创建，工厂签名：
```go
type AdapterFactory func(ctx, channel *IMChannel, msgHandler MessageHandler) (Adapter, CancelFunc, error)
```
工厂在创建时拿到一个 `msgHandler`（即 `Service.HandleMessage`），形成"平台收消息 → 调用核心层"的回调闭环。

---

## 5. 核心层 `im.Service` 处理流程

### 5.1 启动与渠道生命周期

1. `NewService(...)`：注入领域服务、构建 `CommandRegistry`、初始化 QA 队列（`qaQueue`）与限流器；无 Redis 时为单实例模式，有 Redis 时为多实例模式。
2. `RegisterAdapterFactory(platform, factory)`：各平台在启动时注册工厂（如 `feishu` 包提供 `factory.go`）。
3. `LoadAndStartChannels()`：从 DB 加载所有启用的渠道，逐个 `StartChannel`；同时启动 Redis Pub/Sub 订阅，监听其它副本的渠道配置变更（热重载）。
4. `StartChannel` → `startChannelInternal`：调用工厂创建 Adapter，记录 `channelState`，对 WS/longpoll 渠道启动领导者续租协程。

**热重载**：通过 Redis Pub/Sub 接收配置变更事件，调用 `reloadChannelFromDB` 重新加载并重启该渠道；回调校验与 DB 续租检查作为遗漏事件的兜底。

### 5.2 消息接收与去重

- 平台消息经 Adapter 转为 `*IncomingMessage`，回调 `Service.HandleMessage`。
- 进入先按 `userKey`（`channelID + userID + chatID + threadID`）做**去重**，防止平台重复投递造成重复回答。
- 命中限流则返回友好提示，不进入队列。

### 5.3 命令 vs 问答分流

`CommandRegistry.Parse` 判断消息是否为已注册斜杠命令：

- `/help`、`/info`、`/search <kw>`、`/stop`、`/clear` 走命令分支（`handleCommand`）。
- 未注册的 `/xxx`：若形似命令（`LooksLikeCommand`，即首 token 不含 `/`）则提示未知命令；若为路径（如 `/api/v2/users`）则**透传进 QA 管线**，避免误判。

命令副作用示例：
- `ActionClear`：软删当前 `ChannelSession`，下次消息开新会话。
- `ActionStop`：本地从队列移除或取消在途请求；同时向 `StreamManager` 写停止事件；并对尚未执行的请求在 Redis 写停止标记（`RedisKeyStop`）作兜底。

### 5.4 QA 队列与执行

`qaQueue`（`qaqueue.go`）是有界队列 + 工作池：

- 参数可控：`workers`（每实例并发）、`maxQueue`（队列上限）、`maxPerUser`（单用户并发）、`globalMaxWorkers`（跨实例通过 Redis 计数限制全局并发）。
- 队列满 / 用户超限时回退为单并发排队，避免雪崩。

`executeQARequest`（工作协程）：

1. 记录 `inflight` 条目（便于 `/stop` 取消）。
2. 检查预置停止标记。
3. `prepareIMAttachments`：下载消息中的文件/图片，必要时异步写入渠道绑定的知识库。
4. 按 `OutputMode` 决定：
   - **流式**（`stream` 且 Adapter 实现 `StreamSender`）：`handleMessageStream` 边生成边更新卡片/消息。
   - **全量**（`full` 或不支持流式）：`runQA` 收集完整回答后一次性发送。
5. 出站答案经 `formatIMOutboundAnswer` 规整：去除引用标签、重写 `storage://` 资源 URL 为可访问地址、截断超长文本。

### 5.5 流式输出与工具/思考展示

- `stream_section.go` / `stream_display.go`：把 ReAct 的"思考""工具调用""引用"等分段渲染成平台友好的卡片更新。
- `tool_display.go`：把工具调用转成可读步骤；IM 平台通常不支持原生 tool-call，因此封装为文字/卡片。
- 支持 `think.go` 中的"思考过程"折叠展示。

### 5.6 多实例与领导者选举

- WS/longpoll 渠道：`tryAcquireWSLeader` 用 Redis `SetNX` 抢锁（`wsLeaderTTL`），仅领导者维持长连接并续租（`wsLeaderRenewLoop`）。
- 失去领导权（`handleWSLeadershipLoss`）或停止：WS 同步关闭并立即释放锁；**longpoll 故意保留锁 TTL**，让旧轮询协程自然排空，避免新旧实例双写窗口。
- 多实例 `/stop`：`StreamManager` 的停止事件 + Redis 在途映射（`inflightMapping`）+ Redis 停止标记三重保证。

---

## 6. 凭证与身份

- `credentials.go`：统一读取/校验各平台凭证；对外列表（`IMChannelSummary.CredentialsConfigured`）只返回"是否已配置"，绝不泄露明文。
- `computeBotIdentity()`：从凭证派生唯一身份，作为去重与跨实例识别锚点（见 §3.1）。

---

## 7. 扩展一个新平台

以现有平台为模板（如 `feishu/`）：

1. 新建 `internal/im/<platform>/` 包，实现 `im.Adapter`（及可选的 `StreamSender` / `FileDownloader`）。
2. 提供 `AdapterFactory`（`factory.go`），在其中把 `msgHandler` 接入平台收消息逻辑。
3. 在 `IMChannel.computeBotIdentity` 增加该平台的身份派生分支（确保 `BotIdentity` 唯一）。
4. 在 `Service` 启动处 `RegisterAdapterFactory("<platform>", factory)`。
5. 在 `BeforeCreate` 中按需设置该平台的默认 `Mode`。
6. 补充单测（参考各平台已有的 `adapter_test.go`）。

新增平台**无需改动核心层** `im.Service`，满足开闭原则。

---

## 8. 关键设计要点小结

| 关注点 | 方案 |
|--------|------|
| 平台差异隔离 | Adapter 接口 + 工厂注册 |
| 会话连续性 | `ChannelSession` 映射 WeKnora session（user / thread 两模式） |
| 并发与限流 | 有界队列 + 工作池 + 单用户/全局并发限制 |
| 长任务中断 | `/stop` → 队列移除 / 取消 / StreamManager 停止事件 / Redis 标记 |
| 多实例安全 | Redis 领导者选举（WS/longpoll）、Pub/Sub 热重载、`stop` 跨实例传递 |
| 凭证安全 | JSONB 存储、对外脱敏、身份派生去重 |
| 输出适配 | 流式卡片 vs 全量文本，引用/图片 URL 规整 |

---

## 9. 相关源码索引

| 文件 | 职责 |
|------|------|
| `internal/im/service.go` | 核心服务：生命周期、去重、限流、QA 执行、流式、停止 |
| `internal/im/adapter.go` | Adapter / StreamSender / FileDownloader 接口 |
| `internal/im/types.go` | `IMChannel` / `ChannelSession` / 消息结构体与身份派生 |
| `internal/im/command.go` `command_registry.go` `cmd_*.go` | 斜杠命令体系 |
| `internal/im/qaqueue.go` | 有界队列 + 工作池 |
| `internal/im/supervisor.go` | 监督/健康检查 |
| `internal/im/session.go` `think.go` `stream_section.go` `tool_display.go` | 会话解析、思考/工具/流式展示 |
| `internal/im/credentials.go` | 凭证校验 |
| `internal/im/<platform>/*` | 各平台适配器与工厂 |
