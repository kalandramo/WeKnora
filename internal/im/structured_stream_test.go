package im

import (
	"strings"
	"testing"
)

func TestBuildIMStructuredRenderAgent(t *testing.T) {
	parts := IMStreamParts{
		Mode:           IMStreamModeAgent,
		AgentInner:     "先检索知识库再总结",
		AgentToolSteps: []IMToolStep{{ToolName: "web_search", Success: true, DurationMs: 1500}},
		LiveAnswer:     "这是最终回答",
	}
	r := BuildIMStructuredRender(parts, true, false)
	if !strings.Contains(r.Thinking, "思考") {
		t.Fatalf("thinking region should contain 思考, got %q", r.Thinking)
	}
	if !strings.Contains(r.Tools, "网络搜索") {
		t.Fatalf("tools region should mention the tool, got %q", r.Tools)
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
	if r.Thinking != "" || r.Tools != "" {
		t.Fatalf("hideThinking must suppress thinking/tools, got thinking=%q tools=%q", r.Thinking, r.Tools)
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

func TestFormatIMToolSummary(t *testing.T) {
	steps := []IMToolStep{
		{ToolName: "web_search", Success: true, DurationMs: 1500},
		{ToolName: "knowledge_search", Success: true, DurationMs: 500},
		{ToolName: "read_document", Success: false, DurationMs: 300},
	}
	summary := FormatIMToolSummary(steps)
	if !strings.Contains(summary, "共调用 3 个工具") {
		t.Fatalf("summary should report total count, got %q", summary)
	}
	if !strings.Contains(summary, "失败 1") {
		t.Fatalf("summary should report failure count, got %q", summary)
	}
	if !strings.Contains(summary, "2.30s") {
		t.Fatalf("summary should report total duration, got %q", summary)
	}

	if FormatIMToolSummary(nil) != "" {
		t.Fatalf("empty steps should yield empty summary")
	}
}
