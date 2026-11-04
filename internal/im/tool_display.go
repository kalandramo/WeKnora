package im

import (
	"encoding/json"
	"fmt"
	"strings"
)

// IMToolStep tracks one tool invocation for IM display (mirrors Web agent stream events).
type IMToolStep struct {
	ToolCallID string
	ToolName   string
	Pending    bool
	Success    bool
	DurationMs int64
	Arguments  map[string]any
	Data       map[string]interface{}
	Output     string
	// Thought is the reasoning round that immediately preceded this call. Replaying
	// it in front of the tool line lets the folded region read as the interleaved
	// timeline the Web client shows, instead of "all reasoning, then all tools".
	Thought string
}

// imLocalizedToolName returns zh-CN labels aligned with frontend agentStream.tools.
func imLocalizedToolName(toolName string) string {
	if name, ok := imToolNameLabels[toolName]; ok {
		return name
	}
	if strings.HasPrefix(toolName, "mcp_") {
		return formatMCPToolName(toolName)
	}
	return toolName
}

var imToolNameLabels = map[string]string{
	"search_knowledge":        "知识库检索",
	"knowledge_search":        "知识库检索",
	"grep_chunks":             "搜索关键词",
	"read_document":           "阅读文档",
	"list_documents":          "浏览文档列表",
	"web_search":              "网络搜索",
	"web_fetch":               "网页抓取",
	"get_document_info":       "获取文档信息",
	"list_knowledge_chunks":   "查看知识分块",
	"get_related_documents":   "查找相关文档",
	"get_document_content":    "获取文档内容",
	"wiki_search":             "Wiki 搜索",
	"wiki_read_page":          "Wiki 阅读",
	"wiki_read_source_doc":    "精读源文档",
	"todo_write":              "计划管理",
	"ask_user":                "向用户提问",
	"knowledge_graph_extract": "知识图谱抽取",
	"thinking":                "思考",
	"image_analysis":          "查看图片内容",
	"query_understand":        "理解问题",
	"query_knowledge_graph":   "知识图谱查询",
	"read_skill":              "读取技能",
	"execute_skill_script":    "执行技能脚本",
	"list_sandbox_files":      "列出沙箱文件",
	"read_sandbox_file":       "读取沙箱文件",
	"write_sandbox_file":      "写入沙箱文件",
	"edit_sandbox_file":       "编辑沙箱文件",
	"shell_exec":              "执行沙箱命令",
	"data_analysis":           "数据分析",
	"data_schema":             "数据结构",
	"database_query":          "数据库查询",
}

func formatMCPToolName(rawName string) string {
	rest := strings.TrimPrefix(rawName, "mcp_")
	if rest == "" {
		return rawName
	}
	parts := strings.Split(rest, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func collectQueryStrings(value any) []string {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		if strings.HasPrefix(trimmed, "[") {
			var parsed []any
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				var out []string
				for _, item := range parsed {
					if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
						out = append(out, strings.TrimSpace(s))
					}
				}
				return out
			}
		}
		return []string{trimmed}
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		var out []string
		for _, s := range v {
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}

func imGetQueryText(args any) string {
	if args == nil {
		return ""
	}
	parsed := args
	if s, ok := args.(string); ok {
		var obj map[string]any
		if err := json.Unmarshal([]byte(s), &obj); err != nil {
			return ""
		}
		parsed = obj
	}
	record, ok := parsed.(map[string]any)
	if !ok {
		return ""
	}
	seen := make(map[string]struct{})
	var queries []string
	for _, q := range collectQueryStrings(record["query"]) {
		if _, dup := seen[q]; dup {
			continue
		}
		seen[q] = struct{}{}
		queries = append(queries, q)
	}
	for _, q := range collectQueryStrings(record["queries"]) {
		if _, dup := seen[q]; dup {
			continue
		}
		seen[q] = struct{}{}
		queries = append(queries, q)
	}
	return strings.Join(queries, "，")
}

func imGetWikiPageText(args any) string {
	if args == nil {
		return ""
	}
	parsed := args
	if s, ok := args.(string); ok {
		var obj map[string]any
		if err := json.Unmarshal([]byte(s), &obj); err != nil {
			return ""
		}
		parsed = obj
	}
	record, ok := parsed.(map[string]any)
	if !ok {
		return ""
	}
	seen := make(map[string]struct{})
	var slugs []string
	for _, slug := range collectQueryStrings(record["slug"]) {
		if _, dup := seen[slug]; dup {
			continue
		}
		seen[slug] = struct{}{}
		slugs = append(slugs, slug)
	}
	for _, slug := range collectQueryStrings(record["slugs"]) {
		if _, dup := seen[slug]; dup {
			continue
		}
		seen[slug] = struct{}{}
		slugs = append(slugs, slug)
	}
	return strings.Join(slugs, "、")
}

func imGetGrepPatterns(args any) []string {
	if args == nil {
		return nil
	}
	record, ok := args.(map[string]any)
	if !ok {
		return nil
	}
	if queries := collectQueryStrings(record["queries"]); len(queries) > 0 {
		return queries
	}
	if patterns := collectQueryStrings(record["patterns"]); len(patterns) > 0 {
		return patterns
	}
	if q := imGetQueryText(record); q != "" {
		return []string{q}
	}
	if pattern, ok := record["pattern"].(string); ok && strings.TrimSpace(pattern) != "" {
		return []string{strings.TrimSpace(pattern)}
	}
	return nil
}

func imGetWebSearchQuery(step IMToolStep) string {
	if q := imGetQueryText(step.Arguments); q != "" {
		return q
	}
	return imGetQueryText(step.Data)
}

func imGetGrepPatternsFromStep(step IMToolStep) []string {
	if patterns := imGetGrepPatterns(step.Arguments); len(patterns) > 0 {
		return patterns
	}
	return imGetGrepPatterns(step.Data)
}

func imAppendQueryTitle(base, query string) string {
	if query == "" {
		return base
	}
	return fmt.Sprintf("%s：「%s」", base, query)
}

func imAppendPatternsTitle(base string, patterns []string) string {
	if len(patterns) == 0 {
		return base
	}
	display := patterns
	more := ""
	if len(patterns) > 2 {
		display = patterns[:2]
		more = fmt.Sprintf(" +%d", len(patterns)-2)
	}
	return fmt.Sprintf("%s：「%s%s」", base, strings.Join(display, "、"), more)
}

// FormatIMToolLine formats one agent tool step. A leading text marker shows
// success/failure once the call has completed, and a trailing duration is
// appended so users can see how long each tool took (aligned with Web getToolTitle).
func FormatIMToolLine(step IMToolStep) string {
	title := imAgentToolTitle(step)
	if title == "" {
		return ""
	}
	var mark string
	switch {
	case step.Pending:
		// no marker while running
	case step.Success:
		mark = "[完成] "
	default:
		mark = "[失败] "
	}
	line := mark + title
	if !step.Pending {
		if summary := imToolResultSummary(step); summary != "" {
			line += " · " + summary
		}
		if d := imToolDurationLabel(step.DurationMs); d != "" {
			line += " · " + d
		}
	}
	return line
}

// imToolDurationLabel renders a human-friendly duration for an IM tool step.
func imToolDurationLabel(ms int64) string {
	if ms <= 0 {
		return ""
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.2fs", float64(ms)/1000.0)
}

// FormatIMRagPipelineLine formats quick-QA RAG pipeline steps (Web RagPipelineProgress).
func FormatIMRagPipelineLine(step IMToolStep) string {
	toolName := step.ToolName
	query := imGetQueryText(step.Arguments)
	if query == "" {
		query = imGetQueryText(step.Data)
	}

	switch toolName {
	case "query_understand":
		if step.Pending {
			return "正在理解问题..."
		}
		return "已完成问题理解"
	case "knowledge_search", "search_knowledge":
		source := imRetrievalSearchSource(step)
		if step.Pending {
			switch source {
			case imRetrievalSourceWeb:
				if query != "" {
					return fmt.Sprintf("正在检索网络：「%s」", query)
				}
				return "正在检索网络..."
			case imRetrievalSourceMixed:
				if query != "" {
					return fmt.Sprintf("正在检索知识库和网络：「%s」", query)
				}
				return "正在检索知识库和网络..."
			default:
				if query != "" {
					return fmt.Sprintf("正在检索知识库：「%s」", query)
				}
				return "正在检索知识库..."
			}
		}
		base := imRetrievalDoneTitle(source, step.Success)
		line := imAppendQueryTitle(base, query)
		if summary := imKnowledgeSearchSummary(step.Data); summary != "" {
			return line + " · " + summary
		}
		return line
	default:
		return ""
	}
}

func imSandboxMutationTitle(step IMToolStep, pending bool) string {
	name := imLocalizedToolName(step.ToolName)
	path := imSandboxFilePath(step)
	stat := imSandboxDiffStat(step)
	base := name
	if pending {
		base = name + "..."
	}
	if path != "" {
		base = imAppendQueryTitle(name, path)
		if pending {
			base += "..."
		}
	}
	if stat != "" {
		return base + " " + stat
	}
	return base
}

func imSandboxFilePath(step IMToolStep) string {
	if step.Data != nil {
		if p, ok := step.Data["path"].(string); ok && strings.TrimSpace(p) != "" {
			return strings.TrimSpace(p)
		}
	}
	if step.Arguments != nil {
		if p, ok := step.Arguments["path"].(string); ok && strings.TrimSpace(p) != "" {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

func imSandboxDiffStat(step IMToolStep) string {
	added := imIntField(step.Arguments, "added_lines")
	removed := imIntField(step.Arguments, "removed_lines")
	if added == 0 && removed == 0 {
		added = imIntField(step.Data, "added_lines")
		removed = imIntField(step.Data, "removed_lines")
	}
	switch {
	case added > 0 && removed > 0:
		return fmt.Sprintf("+%d -%d", added, removed)
	case added > 0:
		return fmt.Sprintf("+%d", added)
	case removed > 0:
		return fmt.Sprintf("-%d", removed)
	default:
		return ""
	}
}

func imAgentToolTitle(step IMToolStep) string {
	if step.Pending {
		switch step.ToolName {
		case "image_analysis":
			return "正在查看图片内容..."
		case "wiki_search", "wiki_read_page":
			return imLocalizedToolName(step.ToolName) + "..."
		case "write_sandbox_file", "edit_sandbox_file":
			return imSandboxMutationTitle(step, true)
		default:
			return fmt.Sprintf("正在调用 %s...", imLocalizedToolName(step.ToolName))
		}
	}

	toolName := step.ToolName
	isSearchTool := toolName == "search_knowledge" || toolName == "knowledge_search" || toolName == "wiki_search"
	if isSearchTool {
		base := imToolStatusDescription(step)
		query := imGetQueryText(step.Arguments)
		if query == "" {
			query = imGetQueryText(step.Data)
		}
		return imAppendQueryTitle(base, query)
	}

	if toolName == "web_search" {
		base := imToolStatusDescription(step)
		return imAppendQueryTitle(base, imGetWebSearchQuery(step))
	}

	if toolName == "grep_chunks" {
		base := imToolStatusDescription(step)
		return imAppendPatternsTitle(base, imGetGrepPatternsFromStep(step))
	}

	if toolName == "wiki_read_page" {
		pageLabel := ""
		if step.Data != nil {
			if title, ok := step.Data["title"].(string); ok {
				pageLabel = strings.TrimSpace(title)
			}
		}
		if pageLabel == "" {
			pageLabel = imGetWikiPageText(step.Arguments)
		}
		if pageLabel == "" {
			pageLabel = imGetWikiPageText(step.Data)
		}
		base := imToolStatusDescription(step)
		return imAppendQueryTitle(base, pageLabel)
	}

	if toolName == "write_sandbox_file" || toolName == "edit_sandbox_file" {
		return imSandboxMutationTitle(step, false)
	}

	if summary := imToolHeaderSummary(step); summary != "" {
		return summary
	}
	return imToolStatusDescription(step)
}

func imToolStatusDescription(step IMToolStep) string {
	success := step.Success
	toolName := step.ToolName

	switch toolName {
	case "search_knowledge", "knowledge_search":
		if success {
			return "检索知识库"
		}
		return "检索知识库失败"
	case "wiki_search", "wiki_read_page":
		name := imLocalizedToolName(toolName)
		if success {
			return name
		}
		return fmt.Sprintf("调用 %s 失败", name)
	case "web_search":
		if success {
			return "网络搜索"
		}
		return "网络搜索失败"
	case "grep_chunks":
		if success {
			return "搜索关键词"
		}
		return "搜索关键词失败"
	case "get_document_info":
		if success {
			return "获取文档信息"
		}
		return "获取文档信息失败"
	case "get_document_content", "wiki_read_source_doc", "read_document":
		if success {
			return "阅读文档"
		}
		return "阅读文档失败"
	case "list_documents":
		if success {
			return "浏览文档列表"
		}
		return "浏览文档列表失败"
	case "thinking":
		if success {
			return "完成思考"
		}
		return "思考失败"
	case "todo_write":
		if success {
			return "更新任务列表"
		}
		return "更新任务列表失败"
	case "image_analysis":
		if success {
			return "已查看图片内容"
		}
		return "图片内容查看失败"
	case "query_understand":
		if success {
			return "已完成问题理解"
		}
		return fmt.Sprintf("调用 %s 失败", imLocalizedToolName(toolName))
	default:
		name := imLocalizedToolName(toolName)
		if success {
			return fmt.Sprintf("调用 %s", name)
		}
		return fmt.Sprintf("调用 %s 失败", name)
	}
}

func imToolHeaderSummary(step IMToolStep) string {
	if step.Pending || !step.Success {
		return ""
	}
	toolName := step.ToolName
	data := step.Data

	switch toolName {
	case "search_knowledge", "knowledge_search":
		return ""
	case "get_document_info":
		if data != nil {
			if title, ok := data["title"].(string); ok && strings.TrimSpace(title) != "" {
				return fmt.Sprintf("获取文档：%s", strings.TrimSpace(title))
			}
		}
	case "list_knowledge_chunks", "read_document":
		if data != nil {
			if question, ok := data["faq_question"].(string); ok && strings.TrimSpace(question) != "" {
				return fmt.Sprintf("查看 FAQ：%s", strings.TrimSpace(question))
			}
			if _, ok := data["fetched_chunks"]; ok {
				title := "文档"
				if t, ok := data["knowledge_title"].(string); ok && strings.TrimSpace(t) != "" {
					title = strings.TrimSpace(t)
				} else if id, ok := data["knowledge_id"].(string); ok && strings.TrimSpace(id) != "" {
					title = strings.TrimSpace(id)
				}
				return fmt.Sprintf("查看 %s", title)
			}
		}
	}
	return ""
}

func imToolResultSummary(step IMToolStep) string {
	if step.Pending || !step.Success {
		return ""
	}
	switch step.ToolName {
	case "search_knowledge", "knowledge_search":
		return imKnowledgeSearchSummary(step.Data)
	case "web_search":
		return imWebSearchSummary(step.Data)
	case "grep_chunks":
		return imGrepSearchSummary(step.Data)
	case "list_knowledge_chunks", "read_document":
		return imKnowledgeChunksSummary(step.Data)
	case "list_documents":
		return imDocumentListSummary(step.Data)
	default:
		return briefToolSummary(step.Output)
	}
}

const (
	imRetrievalSourceKnowledge = "knowledge"
	imRetrievalSourceWeb       = "web"
	imRetrievalSourceMixed     = "mixed"
)

func imRetrievalSearchSource(step IMToolStep) string {
	if source := imSearchSourceFromData(step.Data); source != "" {
		return source
	}
	if step.Arguments != nil {
		if source, ok := step.Arguments["search_source"].(string); ok && source != "" {
			return source
		}
	}
	return imRetrievalSourceKnowledge
}

func imSearchSourceFromData(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	if source, ok := data["search_source"].(string); ok {
		return strings.TrimSpace(source)
	}
	return ""
}

func imRetrievalDoneTitle(source string, success bool) string {
	switch source {
	case imRetrievalSourceWeb:
		if success {
			return "网络检索"
		}
		return "网络检索失败"
	case imRetrievalSourceMixed:
		if success {
			return "检索知识库和网络"
		}
		return "检索失败"
	default:
		if success {
			return "检索知识库"
		}
		return "检索知识库失败"
	}
}

func imIntField(data map[string]interface{}, key string) int {
	if data == nil {
		return 0
	}
	switch v := data[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func imKnowledgeSearchSummary(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	count := imResultCount(data)
	if count == 0 {
		return "未找到匹配的内容"
	}
	source := imSearchSourceFromData(data)
	webCount := imIntField(data, "web_count")
	docCount := imIntField(data, "doc_count")
	if source == imRetrievalSourceWeb || (webCount > 0 && docCount == 0) {
		return fmt.Sprintf("找到 %d 条网页", count)
	}
	if kbCounts, ok := data["kb_counts"].(map[string]interface{}); ok && len(kbCounts) > 0 {
		return fmt.Sprintf("找到 %d 个结果，来自 %d 个文件", count, len(kbCounts))
	}
	if source == imRetrievalSourceMixed && docCount > 0 && webCount > 0 {
		return fmt.Sprintf("找到 %d 个结果（%d 篇文档，%d 条网页）", count, docCount, webCount)
	}
	return fmt.Sprintf("找到 %d 个结果", count)
}

func imWebSearchSummary(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	count := imResultCount(data)
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("找到 %d 个网络搜索结果", count)
}

func imGrepSearchSummary(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	totalChunks := 0
	if v, ok := data["total_matches"].(float64); ok {
		totalChunks = int(v)
	} else if v, ok := data["total_matches"].(int); ok {
		totalChunks = v
	}
	if totalChunks == 0 {
		return "未找到匹配的内容"
	}
	docCount := imGrepDocumentCount(data)
	return fmt.Sprintf("找到 %d 个匹配片段，来自 %d 个文档", totalChunks, docCount)
}

func imGrepDocumentCount(data map[string]interface{}) int {
	if v, ok := data["document_count"].(float64); ok && v >= 0 {
		return int(v)
	}
	if v, ok := data["document_count"].(int); ok && v >= 0 {
		return v
	}
	if results, ok := data["knowledge_results"].([]interface{}); ok && len(results) > 0 {
		return len(results)
	}
	if results, ok := data["chunk_results"].([]interface{}); ok && len(results) > 0 {
		return len(results)
	}
	return 0
}

func imDocumentListSummary(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	total := imNumericValue(data["total_docs"])
	listed := 0
	if docs, ok := data["documents"].([]interface{}); ok {
		listed = len(docs)
	}
	if total == 0 && listed == 0 {
		return "知识库中没有文档"
	}
	return fmt.Sprintf("列出 %d / %d 个文档", listed, total)
}

func imKnowledgeChunksSummary(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	fetched, ok := data["fetched_chunks"]
	if !ok {
		return ""
	}
	fetchedN := imNumericValue(fetched)
	totalN := imNumericValue(data["total_chunks"])
	summary := fmt.Sprintf("已加载 %d / %v 个分块", fetchedN, formatIMOptionalInt(totalN, data["total_chunks"]))
	pageSize := imNumericValue(data["page_size"])
	if totalN > pageSize && pageSize > 0 {
		page := imNumericValue(data["page"])
		if page <= 0 {
			page = 1
		}
		summary += fmt.Sprintf(" · 第 %d 页，每页 %d 个", page, pageSize)
	}
	return summary
}

func imResultCount(data map[string]interface{}) int {
	if results, ok := data["results"].([]interface{}); ok && len(results) > 0 {
		return len(results)
	}
	if count, ok := data["count"].(float64); ok && count > 0 {
		return int(count)
	}
	if count, ok := data["count"].(int); ok && count > 0 {
		return count
	}
	return 0
}

func imNumericValue(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func formatIMOptionalInt(n int, raw any) string {
	if raw == nil {
		return "?"
	}
	if _, ok := raw.(string); ok && n == 0 {
		return "?"
	}
	if n == 0 {
		return "?"
	}
	return fmt.Sprintf("%d", n)
}

func renderIMToolSteps(steps []IMToolStep, format func(IMToolStep) string) string {
	if len(steps) == 0 {
		return ""
	}
	var b strings.Builder
	for _, step := range steps {
		line := format(step)
		if line == "" {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// visibleIMToolSteps returns the steps that produce a user-facing line, applied
// consistently so the count in the summary matches the rows in the detail.
func visibleIMToolSteps(steps []IMToolStep) []IMToolStep {
	visible := make([]IMToolStep, 0, len(steps))
	for _, s := range steps {
		if imAgentToolTitle(s) == "" {
			continue
		}
		visible = append(visible, s)
	}
	return visible
}

// FormatIMDuration renders a wall-clock duration the way the Web client does:
// "820ms", "45s", "2m 33s". Tool rows keep their own two-decimal label, because
// a single tool is measured in seconds while a whole run is measured in minutes.
func FormatIMDuration(ms int64) string {
	if ms <= 0 {
		return ""
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	seconds := ms / 1000
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
}

// FormatIMExecutionSummary renders the header of the folded execution block,
// matching the Web client: "思考 8 轮 · 调用 10 次工具 · 耗时 2m 33s".
//
// rounds counts reasoning rounds (one segment per tool call), not thinking
// chunks, and toolCalls counts invocations rather than distinct tools — both
// match what the Web timeline header reports.
//
// elapsedMs must be wall-clock. Summing the per-call durations under-reports a
// multi-round run badly: model latency between calls and the final answer
// generation are the larger part of the turn, and they belong to no single tool.
func FormatIMExecutionSummary(rounds, toolCalls int, elapsedMs int64) string {
	parts := make([]string, 0, 3)
	if rounds > 0 {
		parts = append(parts, fmt.Sprintf("思考 %d 轮", rounds))
	}
	if toolCalls > 0 {
		parts = append(parts, fmt.Sprintf("调用 %d 次工具", toolCalls))
	}
	if d := FormatIMDuration(elapsedMs); d != "" {
		parts = append(parts, "耗时 "+d)
	}
	return strings.Join(parts, " · ")
}

// IMThinkingRounds counts reasoning rounds for a step list that records the
// reasoning preceding each call plus the trailing (answer-producing) segment.
func IMThinkingRounds(steps []IMToolStep, trailing string) int {
	rounds := 0
	for _, s := range steps {
		if strings.TrimSpace(s.Thought) != "" {
			rounds++
		}
	}
	if strings.TrimSpace(trailing) != "" {
		rounds++
	}
	return rounds
}

// renderIMAgentTimeline interleaves reasoning rounds with the tool lines that
// followed them, mirroring the Web client's execution timeline. A single merged
// block is used because a card markdown region has no per-row widgets: the 💡
// and 🛠 markers carry the "reasoning vs action" distinction the Web icons do.
func renderIMAgentTimeline(steps []IMToolStep, trailing string) string {
	var b strings.Builder
	appendBlock := func(marker, text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(marker)
		b.WriteString(text)
	}
	for _, step := range steps {
		appendBlock("💡 ", step.Thought)
		if line := FormatIMToolLine(step); line != "" {
			appendBlock("🛠 ", line)
		}
	}
	appendBlock("💡 ", trailing)
	return strings.TrimSpace(b.String())
}

func mergeIMNarrativeAndTools(narrative string, toolLines string) string {
	narrative = strings.TrimSpace(narrative)
	toolLines = strings.TrimSpace(toolLines)
	switch {
	case narrative != "" && toolLines != "":
		return narrative + "\n" + toolLines
	case toolLines != "":
		return toolLines
	default:
		return narrative
	}
}

func upsertIMToolStep(steps *[]IMToolStep, index map[string]int, id string, update func(*IMToolStep)) {
	if i, ok := index[id]; ok {
		update(&(*steps)[i])
		return
	}
	step := IMToolStep{ToolCallID: id}
	update(&step)
	index[id] = len(*steps)
	*steps = append(*steps, step)
}
