# 飞书流式输出结构化改造（折叠思考 + 工具汇总）

> 状态：代码已实现并通过构建 / vet / 纯逻辑测试，尚未 `git commit`。
> 范围：`internal/im/`（飞书专属路径），非飞书平台行为零改动。

---

## 一、背景与痛点

飞书流式卡片当前把**思考过程铺平展示、默认不折叠**，导致：

1. 思考过程较长时卡片被拉得很长，阅读体验差；
2. 最终回答阶段**看不到工具调用次数与调用详情**，用户无法判断"AI 到底调了哪些工具、花了多久"。

参考对象：**明堂（mingtang，`D:\code\tj\cmd\mingtang`）** 在飞书流式展示上的做法——思考折叠、工具调用汇总清晰。

目标：参考明堂设计，优化 WeKnora 在飞书的流输出体验，增强可读性；尽量**小改动、低影响面**。

---

## 二、方案设计

### 2.1 三区域卡片（核心）

把原来"单元素流式 markdown"的卡片，拆成三个由 `element_id` 独立寻址的区域，复用飞书 CardKit 的 `streaming_mode` 按 `element_id` 增量更新：

| element_id         | 类型        | 作用                                   | 默认状态        |
| ------------------ | ----------- | -------------------------------------- | --------------- |
| `stream_thinking_panel` → `stream_thinking` | `collapsible_panel` + 内嵌 `markdown` | 思考过程，可折叠，避免卡片过长 | `expanded: false`（默认收起） |
| `stream_tools`     | `markdown`  | 工具进度（流式）+ 末尾汇总（收尾）      | 空              |
| `stream_answer`    | `markdown`  | 流式 / 最终回答，置顶展示               | 空              |

`streamingElementID` 保留为 `streamAnswerElemID`，以兼容老的 `UpdateStreamContent` 单元素路径。

### 2.2 可选接口隔离（影响面圈死在飞书）

- 新增可选接口 `StructuredStreamSender`（`internal/im/adapter.go`）：
  - `UpdateStructuredStream(ctx, incoming, streamID, render)` —— 推送中间帧（三区域分别写各自元素）；
  - `FinalizeStructuredStream(ctx, incoming, streamID, render)` —— 推送收尾帧（答案 + 工具汇总，思考区保持已推送内容）。
- 仅飞书 adapter 实现该接口。
- `service.go` 通过类型断言分流：

  ```go
  if ss, ok := streamer.(StructuredStreamSender); ok {
      // 结构化三区域路径（飞书）
      ...
      return
  }
  // 其余平台继续走老的 UpdateStreamContent 单元素路径
  ```

- **未实现该接口的钉钉 / Slack / Telegram / Mattermost / WeCom / Web 等**：完全走原有逻辑，**零改动、行为不变**。

### 2.3 工具调用可观测性

- `IMToolStep` 新增字段 `DurationMs int64`；在 `service.go` 两处 `EventAgentToolResult` 回调里从事件层 `AgentToolResultData.Duration` 落库（`step.DurationMs = data.Duration`）。
- `FormatIMToolLine` 改造：
  - 完成后带状态前缀：`[完成] ` / `[失败] `（执行中无前缀）；
  - 末尾追加耗时：`imToolDurationLabel` —— `<1000ms` 显示 `xxxms`，否则 `x.xxs`。
  - 示例：`[完成] 搜索关键词：「文明、策略」 · 找到 5 个匹配片段，来自 2 个文档 · 1.20s`
- 新增 `FormatIMToolSummary(steps)`：统计可见工具步骤的总数、成功 / 失败、累计耗时，输出一行汇总：
  - 有失败时：`共调用 3 个工具 · 成功 2 · 失败 1 · 耗时 2.30s`
  - 全成功时：`共调用 3 个工具 · 全部成功 · 耗时 2.30s`
  - 无可见步骤时返回 `""`。

### 2.4 兼容性：静默思考策略

`IM_STREAM_HIDE_THINKING` 开启时走 quiet-thinking：`BuildIMStructuredRender` 直接返回仅含 `Answer` 的 `IMStreamRender`，thinking / tools 区域被抑制——结构化路径与老路径表现一致。

---

## 三、改动清单

| 文件 | 改动说明 |
| ---- | -------- |
| `internal/im/adapter.go` | 新增 `IMStreamRender` 结构体与可选接口 `StructuredStreamSender`。 |
| `internal/im/think.go` | 新增 `BuildIMStructuredRender`（三区域拆分，兼容 hideThinking）、`formatAgentRenderThinking`、`formatQARenderThinking`。 |
| `internal/im/tool_display.go` | `IMToolStep` 加 `DurationMs`；`FormatIMToolLine` 加状态前缀 + 耗时后缀；新增 `imToolDurationLabel`、`FormatIMToolSummary`。 |
| `internal/im/service.go` | ① 两处 `EventAgentToolResult` 回调落 `DurationMs`；② `flush()` 内接入 `StructuredStreamSender` 分支（含 `cleanIMContent` 清洗）；③ 收尾 `FinalizeStream` 改为按是否实现接口分流。 |
| `internal/im/feishu/adapter.go` | 常量改为三元素 ID；`feishuStreamState` 加 `regionContent` 幂等缓存；`buildStreamingCardJSON` 重写为三区域（`collapsible_panel` + 顶层 `expanded: false`）；新增 `UpdateStructuredStream` / `FinalizeStructuredStream` / `updateStreamElement`（内容未变则跳过 CardKit 请求）；`SendStreamChunk` 保留为 `UpdateStreamContent` 别名。 |
| `internal/im/feishu/longconn_test.go` | **修复仓库既有 bug**：`convertEvent(RegionFeishu, event)` 补第三参。 |
| `internal/im/tool_display_test.go` | 更新 `TestFormatIMToolLine_grepPatterns` 断言以适配新的 `[完成]` 前缀。 |
| `internal/im/feishu/region_test.go` | 适配三区域卡片结构（原断言期望 1 个 element）；修正 `Summary.Content` 的 JSON tag（`summary` → `content`，此前该断言长期"假通过"）；新增 `collapsible_panel` 与 `expanded:false` 断言。 |
| `internal/im/feishu/stream_summary_test.go`（新增） | 收尾摘要取自答案区域（结构化路径）与回退旧内容缓冲（旧路径）两条路径的覆盖。 |
| `internal/im/structured_stream_test.go`（新增） | 4 个纯函数测试：`TestBuildIMStructuredRenderAgent`、`TestBuildIMStructuredRenderHideThinking`、`TestFormatIMToolLineStatusAndDuration`、`TestFormatIMToolSummary`。 |

> 注：`helm/templates/app.yaml`、`helm/values.yaml` 的未提交变更属于仓库既有状态，与本次 IM 改动无关。

改动规模：`internal/im/` 下 7 个文件 **+310 / −22** 行，新增 1 个测试文件。

---

## 四、影响面与兼容性

- **非飞书平台**：类型断言不命中，行为与改造前完全一致，无回归风险。
- **飞书老路径**：`UpdateStreamContent` / `SendStreamChunk` 保留为兼容入口，未删除。
- **飞书新卡片**：基于既有 CardKit 2.0 + `streaming_mode`，复用已有的 `cardkitUpdateElement` 与 `nextSeq()`；单卡 `seq` 共享单调自增，通过 `regionContent` 缓存做幂等跳过，降低无谓的 CardKit 往返。

---

## 五、风险点

1. **`collapsible_panel` 在 `streaming_mode` 下的兼容性**：飞书官方对 "streaming_mode + collapsible_panel 折叠块按 `element_id` 增量更新" 这一组合的支持度需实卡确认。用户已拍板"直接全量实施"并承担该风险；**需在飞书真实卡片上验证**：折叠块默认是否收起、能否按 `element_id` 正常更新、收尾后是否保持折叠。
2. **多区域共享 `seq`**：单卡所有区域共用一个单调递增 `seq`，`updateStreamElement` 已通过 `state.mu` 保护 + `nextSeq()` 保证顺序；逻辑自洽，但实卡确认一次更稳妥。
3. **整仓构建环境**：整仓 `go build ./...` 受本机 CGO 工具链缺失影响（`sqlite-vec` 缺 `sqlite3.h`、`duckdb` 链接缺 `pthread`）无法跑通，**与本次改动无关**；`internal/im/...` 单独构建已证明改动自洽。

### 代码评审遗留项（不阻断，但需部署时确认）

以下为评审中确认存在、当前**未修复**的行为差异，建议部署后重点验证：

1. **收尾时 thinking 区域不刷新**：`FinalizeStructuredStream` 有意不更新 thinking 区域（内容已在流式期间推送）。若整轮思考为空，卡片中该折叠块会保留初始占位文案 "💭 正在思考..."，不会被清空。
2. **`flush()` 结构化分支提前 `return`，跳过了 `holdbackCutoff` 截断与 `ShouldPushStreamFrame` 节流**：思考/工具区域不含未闭合的 markdown 引用，实际影响很小；但答案区域未做"半截引用"保护，极端情况下可能渲染出未闭合的图片/链接标记（`resolveMarkdownImages` 会做二次校验，实际风险低）。

### 已在评审阶段修复

1. **收尾摘要取不到答案文本**（原遗留项 1）：`EndStream` 原先用 `state.content` 生成聊天列表摘要，而该字段仅由旧路径 `UpdateStreamContent` 写入；结构化路径走 `updateStreamElement`，只更新 `state.regionContent`，导致飞书会话列表摘要停留在 "正在思考..."。已修复为**优先取答案区域内容（`regionContent[stream_answer]`），回退旧的 `state.content`**，旧路径行为不变。新增 `internal/im/feishu/stream_summary_test.go` 覆盖两条路径。

---

## 六、验证结论

| 验证项 | 命令 / 范围 | 结果 |
| ------ | ----------- | ---- |
| 编译 | `go build ./internal/im/...` | ✅ exit 0 |
| 静态检查 | `go vet ./internal/im/...` | ✅ exit 0 |
| IM 包测试 | `go test ./internal/im/` | ✅ ok |
| 飞书子包测试 | `go test ./internal/im/feishu/` | ✅ ok |
| 受影响纯逻辑测试 | `go test ./internal/im -run 'TestBuildIMStructuredRender\|TestFormatIMToolSummary\|TestFormatIMToolLine\|...'` | ✅ 12 项全 PASS |

测试覆盖：新增 `BuildIMStructuredRender`（agent / hideThinking 两态）、`FormatIMToolLine` 状态与耗时、`FormatIMToolSummary`；`region_test.go` 覆盖三区域结构与 collapsible 默认折叠。

### 代码评审发现并已修复

评审阶段发现 `internal/im/feishu` 子包存在 2 个测试失败（此前只验证了 `internal/im` 包，未覆盖子包），均已修复：

1. **`TestBuildStreamingCardJSON_PlaceholderFollowsRegion`** —— 卡片由 1 个 element 改为 3 个，原断言 `len(elements) != 1` 失败。已重写为校验三区域结构，并顺带修正 `Summary.Content` 的 JSON tag（`json:"summary"` → `json:"content"`，此前该断言长期取不到值而"假通过"），新增 collapsible `folded:true` 断言。
2. **`TestConvertEventKeepsAtAllAlongsideSenderType`** —— `convertEvent` 第三参为 `botOpenID`，原补参 `""` 导致群聊消息被"未 @ 机器人"校验丢弃、返回 nil。已按 `longconn_test.go` 既有写法补入 `MentionedType:"bot"` 的 mention 并传 `ou_bot`。

> 关于 `convertEvent`：HEAD 版本的该测试文件**无法编译**（`not enough arguments in call to convertEvent`），属仓库既有缺陷；本次一并修复至可编译且语义正确。

---

## 七、待办 / 后续

- [ ] `git commit`（用户当前选择暂不提交，保持本地未提交状态）。
- [ ] 部署后在飞书实卡验证：折叠块默认折叠、按 `element_id` 增量更新、工具汇总行位置与文案、会话列表摘要是否显示最终答案。
- [ ] 若实卡发现 `collapsible_panel` + `streaming_mode` 不兼容，需回退思考区为普通 markdown 或调整折叠策略。


---

## 八、部署后故障定位与修复（2026-11-04）

### 现象

飞书侧收到的回答**完全不是卡片**：无卡片标题栏、无折叠块，正文里 `**粗体**`、反引号、`| 表格 |`、`- 列表` 全部原样显示，段落之间间距异常放大。

### 根因

`buildStreamingCardJSON` 用了不存在的组件 tag 与折叠字段：

| 字段 | 错误写法 | 正确写法 |
| --- | --- | --- |
| 折叠容器 tag | `"tag": "collapsible"` | `"tag": "collapsible_panel"` |
| 默认折叠状态 | `header.folded: true` | 顶层 `expanded: false` |

卡片 JSON 校验不通过 → `POST /cardkit/v1/cards` 返回 `200220 Failed to generate card content` → `StartStream` 失败 → `service.go` 走「非流式降级」：**重新跑一遍 QA**，再用 `msg_type=text` 把答案当纯文本发出去。纯文本消息不解析 Markdown，所以看到的是源码形态；也正因为重跑了一次 QA，该轮模型的 token 消耗翻倍。

### 修复

1. `internal/im/feishu/adapter.go`
   - 折叠容器改为 `collapsible_panel`，折叠状态改为顶层 `expanded: false`，并为容器本身补 `element_id`（`stream_thinking_panel`）。
   - 卡片创建失败时把卡片 JSON 打进 Warn 日志，避免同类问题再次只剩一句 `falling back to non-streaming`。
   - 容器 `element_id` 由 `stream_thinking_panel`（21 字符）改为 `stream_think_panel`（18 字符）：飞书限制 element_id ≤20 字符且须以字母开头，超长返回 `300301 / code 1002`，同样会导致整卡创建失败。新增 `TestBuildStreamingCardJSON_ElementIDsAreValid` 遍历整个卡片 JSON 树校验格式与唯一性，杜绝同类问题。
2. `internal/im/think.go`
   - 新增 `CollapsibleThinkStyle`：思考区正文不再输出 `> ` 引用前缀与重复的 `**思考过程**` 标题（折叠面板 header 已有标题）。
3. 测试：`region_test.go` 改断 `collapsible_panel` / `expanded` / 面板标题；`structured_stream_test.go` 改为断言思考区为纯段落、不含 `> `。
4. 思考区增量阈值节流（`streamRegionThrottle`）：思考区仅在**增量 ≥200 字符**或**距上次推送 ≥1s** 时重推；内容变短（撤回前言）与收尾帧**不受节流**，前者避免卡片残留已撤回文本，后者保证被节流的尾段不会丢。答案区与工具区仍即时推送。
   - 单卡出站峰值由约 10 次/秒（正好顶到飞书上限）降到约 4.5 次/秒。

验证：`go vet ./internal/im/...` 与 `go test ./internal/im/ ./internal/im/feishu/` 均通过。

### 仍未处理（建议纳入下次迭代）

1. `collapsible_panel` 需要飞书客户端 **V7.9+**，低版本客户端会显示"请升级客户端查看内容"的占位图（按用户决定暂不处理）。
2. `flush()` 结构化分支绕过了 `holdbackCutoff`，答案区可能短暂出现未闭合的链接/图片标记。
3. 思考区内容参与单卡 30KB 体积上限，长推理需截断或摘要。
