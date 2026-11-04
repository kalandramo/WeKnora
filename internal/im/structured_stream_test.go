package im

import (
	"strings"
	"testing"
)

func TestBuildIMStructuredRenderAgent(t *testing.T) {
	parts := IMStreamParts{
		Mode:           IMStreamModeAgent,
		AgentInner:     "先检索知识库再总结",
		AgentToolSteps: []IMToolStep{{Thought: "先检索知识库再总结", ToolName: "web_search", Success: true, DurationMs: 1500}},
		TrailingThought: "检索到了三条记录",
		LiveAnswer:      "这是最终回答",
		ElapsedMs:       153000,
	}
	r := BuildIMStructuredRender(parts, true, false)
	// The folded region is one interleaved timeline: the reasoning round, then the
	// call it led to, then the trailing reasoning. Two separated blocks would lose
	// the causal order the Web timeline shows.
	if !strings.Contains(r.Thinking, "💡 先检索知识库再总结") {
		t.Fatalf("timeline should open with the reasoning round, got %q", r.Thinking)
	}
	if !strings.Contains(r.Thinking, "🛠 ") {
		t.Fatalf("timeline should carry the tool row, got %q", r.Thinking)
	}
	if strings.Index(r.Thinking, "💡") > strings.Index(r.Thinking, "🛠") {
		t.Fatalf("tool row must follow the reasoning that led to it, got %q", r.Thinking)
	}
	if !strings.Contains(r.Thinking, "💡 检索到了三条记录") {
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
