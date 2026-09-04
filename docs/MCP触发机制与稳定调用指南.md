# Agent 如何触发 MCP：机制解析与稳定调用指南

> 面向问题：**智能体会如何触发 MCP？如何稳定触发调用 MCP？**
> 代码基准：`internal/agent/`、`internal/application/service/agent_service.go`、`internal/application/service/session_agent_qa.go`、`internal/agent/tools/mcp_tool.go`。
> 相关文档：`MCP功能使用说明.md`（界面操作）、`BUILTIN_MCP_SERVICES.md`（内置服务）、`zh/mcp-approval.md`（人工审批）、`api/mcp-service.md`（REST API）。

---

## 1. 核心结论（先看这里）

> **WeKnora 没有任何"强制调用 MCP"的开关。MCP 工具是否被调用，完全由 LLM 在每一轮 ReAct 推理中自主决定。**

因此，**触发能否稳定，本质是一个"提示词 + 工具集竞争"问题，而非配置开关问题**。

目前唯一能显著提高触发确定性的**官方机制**是：**在对话中 `@` 提及 MCP 服务**（或 API 传 `mcp_service_ids`）。它会同时做两件事：

1. **收窄作用域** —— 本轮只注册被提及的 MCP 服务，其他 MCP 工具不进入工具列表，消除竞争；
2. **注入强制指令** —— 生成 `<must_use>` 块置于用户问题之前，系统提示词要求"以最高优先级遵循，且优先于本地知识库检索"。

---

## 2. 触发链路（代码级）

```
Agent 创建 → 注册 MCP 工具 → 转 function calling schema → LLM 自主选择 → 执行工具
```

### 2.1 步骤一：注册 MCP 工具

入口在 `CreateAgentEngine`：

```194:201:internal/application/service/agent_service.go
	toolRegistry := tools.NewToolRegistry()
	if config.MaxToolOutputChars > 0 {
		toolRegistry.SetMaxToolOutputSize(config.MaxToolOutputChars)
	}
	if err := s.registerTools(ctx, toolRegistry, config, rerankModel, chatModel, sessionID); err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}
	s.registerMCPTools(ctx, toolRegistry, config, eventBus, sessionID, assistantMessageID)
```

`registerMCPTools` 依据 `MCPSelectionMode` 决定注册范围：

| `mcp_selection_mode` | 行为 |
|---|---|
| `all`（默认） | 注册该租户下**所有**已启用的 MCP 服务 |
| `selected` | 仅注册 `mcp_services` 中指定的服务 |
| `none` | 不注册任何 MCP 工具，直接返回 |

随后调用 `tools.RegisterMCPTools`，对每个服务执行 `ListTools` 发现工具，包装为 `MCPTool` 后注册进 `ToolRegistry`：

- `ListTools` 超时 30 秒；失败会**断开重连并重试一次**（陈旧连接场景）；
- 工具名冲突时采用 **first-wins** 策略，后来的同名工具被跳过并告警；
- 返回注册数量，日志形如 `Registered %d MCP tool(s) from %d enabled service(s)`。

### 2.2 步骤二：转成 function calling schema

模型看到的工具定义来自 `MCPTool` 的三个方法：

```55:58:internal/agent/tools/mcp_tool.go
	name := fmt.Sprintf("mcp_%s_%s", serviceName, toolName)
```

```82:88:internal/agent/tools/mcp_tool.go
func (t *MCPTool) Description() string {
	serviceDesc := fmt.Sprintf("[MCP Service: %s (external)] ", t.service.Name)
	if t.mcpTool.Description != "" {
		return serviceDesc + t.mcpTool.Description
	}
	return serviceDesc + t.mcpTool.Name
}
```

| 组成 | 来源 | 优化点 |
|---|---|---|
| **工具名** | `mcp_{sanitize(服务名)}_{工具名}`，超 64 字符会截断服务名 | 服务名会被编入工具名，取名要有辨识度 |
| **描述** | `[MCP Service: {服务名} (external)] ` + **MCP server 自带的 description** | ⚠️ **直接决定模型命中率**，见方案 3 |
| **参数 Schema** | MCP server 的 `inputSchema`，缺省返回空 object | — |

### 2.3 步骤三：LLM 自主决策（关键——没有强制）

ReAct 主循环在 `internal/agent/engine.go`，每轮调用 `think.go` 的 `streamThinkingToEventBus`。构造请求参数时：

```172:180:internal/agent/think.go
	parallelToolCalls := true
	opts := &chat.ChatOptions{
		Temperature:         e.config.Temperature,
		MaxTokens:           budget,
		MaxCompletionTokens: budget,
		Tools:               tools,
		Thinking:            e.config.Thinking,
		ParallelToolCalls:   &parallelToolCalls,
	}
```

**注意：这里没有 `ToolChoice` 字段。** `internal/models/chat` 包本身支持 `ToolChoice`，但 Agent 层并未暴露该能力。

后续判定逻辑（`engine.go`）：

- 模型返回 `tool_calls > 0` → 执行工具，进入下一轮；
- 模型返回 `tool_calls == 0` 且有 content → `analyzeResponse` 判定为 **natural stop**，直接作为最终答案输出，**MCP 不会被执行**；
- 连续多轮返回相同内容 → 判定卡死循环，提前中断。

---

## 3. 为什么会触发不稳定

| 根因 | 说明 |
|---|---|
| **无 `tool_choice` 强制** | 模型"想调才调"，是设计上的自主决策 |
| **RAG 提示词是「知识库优先」** | `config/prompt_templates/agent_system_prompt.yaml` 的 rag 模板规定：先穷尽 KB 检索（含 Deep Read），不足才考虑其他。MCP 工具需与 `knowledge_search` / `grep_chunks` / `list_knowledge_chunks` 竞争 |
| **工具数量稀释** | `mcp_selection_mode=all` 会把所有 MCP 服务的全部工具塞进列表，工具越多越难被选中 |
| **描述含糊** | 描述原样来自外部 MCP server，写得笼统模型就不认 |
| **`mcp_` 前缀增加判断成本** | 模型需自行推断"该服务提供什么能力、何时该用" |
| **模型能力** | 不支持 function calling 的模型完全无法触发 |

---

## 4. 稳定触发方案（按强度排序）

### 方案 1（强烈推荐）：`@` 提及 MCP 服务 —— 官方 `must_use` 机制

**前端操作**：在输入框输入 `@` → 选择目标 MCP 服务 → 提问。

**API 调用**：

```http
POST /api/v1/agent-chat/{session_id}
Content-Type: application/json
X-API-Key: sk-xxxxx

{
  "query": "查一下工单 12345 的当前状态",
  "agent_id": "你的 Agent ID",
  "mcp_service_ids": ["目标MCP服务ID"]
}
```

> 路由定义见 `internal/router/routes_chat.go:114`（`agentChat.POST("/:session_id", handler.AgentQA)`），前缀为 `/api/v1`。

#### 生效原理（三重保障）

**① 作用域收窄**

`applyPerRequestMCPScope` 把本轮注册范围收敛到被提及的服务，且**强制把 mode 改为 `selected`**：

```522:535:internal/application/service/session_agent_qa.go
	switch selectionMode {
	case "none":
		return nil, selectionMode
	case "selected":
		effective = intersectPreservingRequestOrder(mentioned, agentMCPs)
	case "all", "":
		effective = mentioned
	default:
		effective = mentioned
	}
	if len(effective) == 0 {
		return nil, selectionMode
	}
	return effective, "selected"
```

效果：其他 MCP 工具**根本不出现在工具列表中**，竞争被彻底消除。

**② 注入 `<must_use>` 强制块**

被提及的服务记为 `PinnedMCPServiceIDs`，经 `attachPinnedMCPToolNames` 补全工具名后，由 `buildMustUseBlock` 生成提示块：

```447:447:internal/agent/observe.go
		lines = append(lines, fmt.Sprintf("Must use MCP tools whose names start with %s (@%s) to answer the question below.", prefix, display))
```

最终作为 **sibling block** 注入到用户消息中、问题**之前**：

```
<must_use>
Must use MCP tools whose names start with mcp_iwiki_ (@企业Wiki) to answer the question below.
</must_use>
```

**③ 系统提示词赋予最高优先级**

rag 模板（`config/prompt_templates/agent_system_prompt.yaml` 第 92 行）：

> **If `<must_use>` is present:** follow it first — must use the MCP tool prefixes it names **before local KB search**; still run KB retrieval afterward if needed.

pure 模板（第 49 行）：

> When the user @MCP or @Skill, a short `<must_use>` block appears before their question... Follow it with **highest priority** for tool selection.

#### 使用约束（务必注意）

| 约束 | 说明 |
|---|---|
| 必须已在 Agent 中授权 | `selected` 模式下会与 Agent preset 求交集，未授权的 ID 被剔除 |
| 共享 Agent 限制更严 | 只能提及 preset 内的服务，否则整条提及被静默忽略 |
| `mode=none` 时无效 | 日志提示 `Ignoring @MCP mention: agent MCP selection is disabled (mode=none)` |
| **embed 渠道不支持** | 前端硬编码 `mcp_service_ids: []`（`frontend/src/composables/useEmbedChatSession.ts:317`），无法 `@` 提及，需改用方案 2 |

---

### 方案 2：Agent 配置收敛 + 自定义系统提示词

适用于无法 `@` 提及的场景（embed 接入、程序化调用、希望"默认就触发"）。

**配置**：

- `mcp_selection_mode: "selected"`
- `mcp_services: ["目标服务ID"]` —— **只挂一个**服务，最小化工具集
- 在 Agent 的系统提示词中写死硬指令，例如：

```
当用户询问工单状态时，必须先调用 mcp_ticket_get_status 工具获取实时数据，
禁止直接回答或依赖知识库内容。
若工具调用失败或返回为空，如实说明未能获取实时数据。
```

**前端位置**：Agent 编辑弹窗 → MCP 服务选择（`mcp_selection_mode` / `mcp_services`，见 `AgentEditorModal.vue`）+ 系统提示词配置。

**注意**：即使 `selected` 模式，用户仍可在对话中 `@` 提及来做本轮收窄（取交集）。

---

### 方案 3：优化 MCP 工具自身描述

工具描述会**原样传给模型**，是模型判断的唯一依据。

优化前：
```
Get status
```

优化后：
```
Get the real-time status of a support ticket by ticket ID.
Use this whenever the user asks about ticket state, progress, or assignment —
do not answer from prior knowledge.
Example input: {"ticket_id": "12345"}
```

要点：写清**触发场景**（when to use）+ **输入示例** + **禁止行为**（如"不得依赖先验知识"）。

同时，MCP 服务的**名称**会被编入工具名（`mcp_{服务名}_{工具名}`）和描述前缀 `[MCP Service: {服务名} (external)]`，建议取有辨识度的名字。

---

### 方案 4：模型与参数调优

| 项 | 建议 |
|---|---|
| 模型能力 | **必须**支持 function calling / tool use，这是硬性前提 |
| `temperature` | 调低（如 0.1~0.3），提高决策确定性 |
| `max_iterations` | 确保足够大，避免工具尚未执行完就达到轮次上限 |
| `MaxToolOutputChars` | 输出被截断会影响后续推理，按需调大 |
| `thinking` 工具 | 开启后模型会先规划，通常有助于提高工具选择准确率 |

---

### 方案 5（需改代码）：暴露 `ToolChoice` 强制开关

若要**硬性保证**某一轮必定调用指定工具，需修改 `internal/agent/think.go` 的 `ChatOptions`：

```go
ToolChoice: &chat.ToolChoice{
    Type:     "function",
    Function: &chat.ToolChoiceFunction{Name: "mcp_ticket_get_status"},
},
```

- `internal/models/chat` 已支持该字段（`openai_request.go` / `chat.go` 中可见），无需改模型层；
- 建议仅在**专用 Agent** 场景使用，并通过 Agent 配置（如 `forced_tool_name`）按需开启；
- 代价：牺牲灵活性，强制调用会绕过模型的自主判断，参数错误时无法回退到直接回答。

---

## 5. 排查清单（不触发时按顺序查）

| 现象 / 日志 | 定位与处理 |
|---|---|
| `Registered 0 MCP tool(s)` 或 `No MCP tools registered from N enabled service(s)` | 服务未启用或注册失败 → 到「设置 → MCP 服务」执行连接测试 |
| `Failed to list tools from MCP service xxx` | 连通性 / 鉴权 / OAuth 问题；服务端会重连重试一次 |
| `Failed to create MCP client for service xxx` | 传输方式或凭证错误；`stdio` 已被服务端禁用 |
| `[Agent][Round-1] LLM responded: finish=stop, content=N chars, tool_calls=0` | **模型未选择任何工具** → 采用方案 1（`@` 提及）或方案 2 |
| `Ignoring @MCP mention: agent MCP selection is disabled (mode=none)` | Agent 的 `mcp_selection_mode` 为 `none`，需改为 `all` 或 `selected` |
| `Ignoring @MCP scope outside agent preset` | `@` 提及的服务不在 Agent 授权列表内（`selected` 求交集后为空） |
| `MCP tool name collision: ... skipped (first-wins)` | 两个服务提供了同名工具，后者被跳过 |
| 工具执行了但答案没用到结果 | 检查 `MaxToolOutputChars` 截断；检查提示词是否强调"必须基于工具结果作答" |
| embed / iframe 接入完全不触发 | 前端硬编码 `mcp_service_ids: []`，不支持 `@` 提及 → 走方案 2 |

---

## 6. 相关源码索引

| 文件 | 职责 |
|---|---|
| `internal/application/service/agent_service.go` | `CreateAgentEngine`、`registerMCPTools`、`resolvePinnedMCPServiceInfos`、`attachPinnedMCPToolNames` |
| `internal/application/service/session_agent_qa.go` | `applyPerRequestMCPScope`、`resolvePerRequestMCPScope`（`@` 提及作用域收敛） |
| `internal/agent/tools/mcp_tool.go` | `MCPTool` 定义、`RegisterMCPTools`、工具名/描述/参数生成、审批门 |
| `internal/agent/engine.go` | ReAct 主循环、`analyzeResponse`（natural stop 判定）、`SetPinnedMentions` |
| `internal/agent/think.go` | `ChatOptions` 构造（**无 ToolChoice**）、流式推理 |
| `internal/agent/observe.go` | `buildMustUseBlock`（`<must_use>` 生成）、`mcpToolNamePrefix` |
| `internal/agent/prompts.go` | 系统提示词构建、`GetPureAgentSystemPrompt` / `GetProgressiveRAGSystemPrompt` |
| `config/prompt_templates/agent_system_prompt.yaml` | 提示词模板（pure / rag 等模式，`<must_use>` 优先级声明） |
| `internal/types/agent.go`、`internal/types/custom_agent.go` | `MCPSelectionMode`、`MCPServices`、`PinnedMCPServiceIDs` 字段定义 |
| `internal/handler/session/types.go` | 请求体 `mcp_service_ids` 字段 |
| `internal/router/routes_chat.go` | `POST /api/v1/agent-chat/{session_id}` 路由 |
| `internal/models/chat/` | `ChatOptions.ToolChoice`（已支持，Agent 层未使用） |
