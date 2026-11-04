package im

import (
	"strings"
	"testing"
)

func TestBuildIMStructuredRenderAgent(t *testing.T) {
	parts := IMStreamParts{
		Mode:            IMStreamModeAgent,
		AgentInner:      "先检索知识库再总结",
		AgentToolSteps:  []IMToolStep{{Thought: "先检索知识库再总结", ToolName: "web_search", Success: true, DurationMs: 1500}},
		TrailingThought: "检索到了三条记录",
		LiveAnswer:      "这是最终回答",
		ElapsedMs:       153000,
	}
	r := BuildIMStructuredRender(parts, true, false)
	// The folded region is one interleaved timeline: the reasoning round, then the
	// call it led to, then the trailing reasoning. Two separated blocks would lose
	// the causal order the Web timeline shows.
	if !strings.Contains(r.Thinking, "💡 思考 · 先检索知识库再总结") {
		t.Fatalf("timeline should open with the reasoning round, got %q", r.Thinking)
	}
	if !strings.Contains(r.Thinking, "🛠 ") {
		t.Fatalf("timeline should carry the tool row, got %q", r.Thinking)
	}
	if strings.Index(r.Thinking, "💡") > strings.Index(r.Thinking, "🛠") {
		t.Fatalf("tool row must follow the reasoning that led to it, got %q", r.Thinking)
	}
	if !strings.Contains(r.Thinking, "💡 思考 · 检索到了三条记录") {
		t.Fatalf("timeline must end with the trailing reasoning, got %q", r.Thinking)
	}
	// Length is not a concern inside the folded block, but the blockquote markers of
	// the old layout must stay gone (the panel header already carries the title).
	if strings.Contains(r.Thinking, "> ") {
		t.Fatalf("timeline must not be a card blockquote, got %q", r.Thinking)
	}
	if !strings.Contains(r.Tools, "网络搜索") {
		t.Fatalf("tools region should still hold the flat log, got %q", r.Tools)
	}
	// The summary matches the Web header: rounds (not thinking chunks), invocations
	// (not distinct tools), and wall-clock duration (not the sum of tool times).
	if r.Summary != "思考 2 轮 · 调用 1 次工具 · 耗时 2m 33s" {
		t.Fatalf("summary = %q, want the Web header wording", r.Summary)
	}
	if r.Answer != "这是最终回答" {
		t.Fatalf("answer region mismatch: %q", r.Answer)
	}
}

func TestBuildIMStructuredRenderHideThinking(t *testing.T) {
	parts := IMStreamParts{
		Mode:           IMStreamModeAgent,
		AgentInner:     "should be hidden",
		AgentToolSteps: []IMToolStep{{ToolName: "web_search", Success: true}},
		LiveAnswer:     "only answer",
	}
	r := BuildIMStructuredRender(parts, true, true)
	if r.Thinking != "" || r.Tools != "" || r.Summary != "" {
		t.Fatalf("hideThinking must suppress thinking/tools/summary, got thinking=%q tools=%q summary=%q",
			r.Thinking, r.Tools, r.Summary)
	}
	if r.Answer != "only answer" {
		t.Fatalf("answer should survive, got %q", r.Answer)
	}
}

func TestFormatIMToolLineStatusAndDuration(t *testing.T) {
	pending := FormatIMToolLine(IMToolStep{ToolName: "web_search", Pending: true})
	if !strings.Contains(pending, "网络搜索") || strings.Contains(pending, "[完成]") {
		t.Fatalf("pending line should have no success marker, got %q", pending)
	}

	ok := FormatIMToolLine(IMToolStep{ToolName: "web_search", Success: true, DurationMs: 1500})
	if !strings.Contains(ok, "[完成]") || !strings.Contains(ok, "1.50s") {
		t.Fatalf("success line should show marker and duration, got %q", ok)
	}

	fail := FormatIMToolLine(IMToolStep{ToolName: "web_search", Success: false, DurationMs: 200})
	if !strings.Contains(fail, "[失败]") || !strings.Contains(fail, "200ms") {
		t.Fatalf("failure line should show marker and duration, got %q", fail)
	}
}

func TestFormatIMExecutionSummary(t *testing.T) {
	// Wording and ordering must match the Web timeline header exactly.
	if got := FormatIMExecutionSummary(8, 10, 153000); got != "思考 8 轮 · 调用 10 次工具 · 耗时 2m 33s" {
		t.Fatalf("summary = %q, want the Web header wording", got)
	}
	// Zero buckets drop out instead of rendering "思考 0 轮".
	if got := FormatIMExecutionSummary(0, 3, 45000); got != "调用 3 次工具 · 耗时 45s" {
		t.Fatalf("summary without rounds = %q", got)
	}
	if got := FormatIMExecutionSummary(2, 0, 820); got != "思考 2 轮 · 耗时 820ms" {
		t.Fatalf("summary without tools = %q", got)
	}
	if got := FormatIMExecutionSummary(0, 0, 0); got != "" {
		t.Fatalf("empty run should yield an empty summary, got %q", got)
	}
}

func TestFormatIMDuration(t *testing.T) {
	cases := map[int64]string{
		0:      "",
		820:    "820ms",
		1000:   "1s",
		45000:  "45s",
		60000:  "1m 0s",
		153000: "2m 33s",
	}
	for ms, want := range cases {
		if got := FormatIMDuration(ms); got != want {
			t.Errorf("FormatIMDuration(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestIMThinkingRounds(t *testing.T) {
	steps := []IMToolStep{
		{Thought: "先看看有哪些工具", ToolName: "web_search"},
		{ToolName: "read_document"}, // no reasoning before this one
	}
	if got := IMThinkingRounds(steps, ""); got != 1 {
		t.Fatalf("rounds = %d, want 1", got)
	}
	if got := IMThinkingRounds(steps, "最后总结一下"); got != 2 {
		t.Fatalf("rounds with trailing reasoning = %d, want 2", got)
	}
	if got := IMThinkingRounds(nil, "  "); got != 0 {
		t.Fatalf("blank trailing reasoning must not count, got %d", got)
	}
}

// The folded panel is read, not parsed: every row must stay on one line. The
// previous renderer dumped the reasoning body verbatim, paragraph breaks and
// markdown markers included, which is what made the block look like a raw
// transcript instead of the compact timeline the Web client shows.
func TestRenderIMAgentTimelineRows(t *testing.T) {
	longThought := "This is a documentation-type question — how to apply for permission to view OC logs on the Daoke platform. " +
		"Let me search the knowledge base for the permission application process.\n\n" +
		"**Note**: the answer must stay concise."
	steps := []IMToolStep{{
		Thought:    longThought,
		ToolName:   "knowledge_search",
		Success:    true,
		DurationMs: 1140,
		Arguments:  map[string]any{"query": "如何申请开通查看日志的权限，道客平台日志查看权限申请流程和表单填写"},
	}}
	got := renderIMAgentTimeline(steps, "")

	if strings.Contains(got, "\n\n") {
		t.Fatalf("rows must not be separated by blank lines, got %q", got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 rows (reasoning + tool), got %d: %q", len(lines), got)
	}

	thought := strings.TrimPrefix(lines[0], "💡 思考 · ")
	if thought == lines[0] {
		t.Fatalf("first row must be the labelled reasoning row, got %q", lines[0])
	}
	if !strings.HasSuffix(thought, "…") {
		t.Fatalf("over-long reasoning must end with an ellipsis, got %q", thought)
	}
	if n := len([]rune(strings.TrimSuffix(thought, "…"))); n > imTimelineThoughtLimit {
		t.Fatalf("thought snippet is %d runes, want <= %d", n, imTimelineThoughtLimit)
	}
	if strings.Contains(lines[0], "**") || strings.Contains(lines[0], "Note") {
		t.Fatalf("markdown markers must be stripped and the tail dropped, got %q", lines[0])
	}

	if !strings.HasPrefix(lines[1], "🛠 ") {
		t.Fatalf("second row must be the tool row, got %q", lines[1])
	}
	if n := len([]rune(lines[1])); n > 80 {
		t.Fatalf("tool row is %d runes, too wide for one card line: %q", n, lines[1])
	}
}

// MaxIterations allows 40 iterations; two rows each would overflow a card.
func TestRenderIMAgentTimelineCapsRows(t *testing.T) {
	steps := make([]IMToolStep, 0, 40)
	for i := 0; i < 40; i++ {
		steps = append(steps, IMToolStep{Thought: "round", ToolName: "web_search", Success: true})
	}
	lines := strings.Split(renderIMAgentTimeline(steps, ""), "\n")
	if len(lines) != imTimelineMaxRows+1 {
		t.Fatalf("want %d rows incl. the omission note, got %d", imTimelineMaxRows+1, len(lines))
	}
	if !strings.HasPrefix(lines[0], "… 前面 ") {
		t.Fatalf("first line must report how many rows were dropped, got %q", lines[0])
	}
}

// A run that used neither tools nor reasoning still created the panel at card
// creation with a "正在思考..." placeholder, and region updates skip empty content
// — so without a quiet replacement the folded block kept advertising work that
// had already finished.
func TestBuildIMStructuredRenderEmptyDetail(t *testing.T) {
	agent := BuildIMStructuredRender(IMStreamParts{Mode: IMStreamModeAgent, Answer: "直接回答"}, true, false)
	if agent.Thinking != imEmptyDetailAgent {
		t.Fatalf("agent empty detail = %q, want %q", agent.Thinking, imEmptyDetailAgent)
	}
	qa := BuildIMStructuredRender(IMStreamParts{Mode: IMStreamModeQuickQA, Answer: "直接回答"}, false, false)
	if qa.Thinking != imEmptyDetailQuickQA {
		t.Fatalf("quick-QA empty detail = %q, want %q", qa.Thinking, imEmptyDetailQuickQA)
	}
}
