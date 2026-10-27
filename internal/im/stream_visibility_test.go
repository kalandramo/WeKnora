package im

import "testing"

func TestParseIMBoolEnv(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"t", true},
		// Anything that is not a Go boolean literal counts as off: a typo must
		// not silently change what end users see.
		{"yes", false},
		{"off", false},
		{"2", false},
	}
	for _, c := range cases {
		if got := parseIMBoolEnv(c.raw); got != c.want {
			t.Errorf("parseIMBoolEnv(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestIMAnswerStarted(t *testing.T) {
	toolStep := []IMToolStep{{ToolName: "search_knowledge"}}

	cases := []struct {
		name  string
		parts IMStreamParts
		want  bool
	}{
		{
			name:  "agent thinking only",
			parts: IMStreamParts{Mode: IMStreamModeAgent, AgentInner: "我先确认一下环境", AgentToolSteps: toolStep},
			want:  false,
		},
		{
			name:  "agent live answer streaming",
			parts: IMStreamParts{Mode: IMStreamModeAgent, LiveAnswer: "DCE 权限申请需要"},
			want:  true,
		},
		{
			name:  "agent answer after completion",
			parts: IMStreamParts{Mode: IMStreamModeAgent, Answer: "完整答案"},
			want:  true,
		},
		{
			name:  "agent whitespace live answer is not an answer",
			parts: IMStreamParts{Mode: IMStreamModeAgent, LiveAnswer: "   \n"},
			want:  false,
		},
		{
			name:  "quick qa pipeline only",
			parts: IMStreamParts{Mode: IMStreamModeQuickQA, ReasoningInner: "分析问题", PipelineToolSteps: toolStep},
			want:  false,
		},
		{
			name:  "quick qa answer",
			parts: IMStreamParts{Mode: IMStreamModeQuickQA, Answer: "答案"},
			want:  true,
		},
		{
			name:  "quick qa live answer does not count",
			parts: IMStreamParts{Mode: IMStreamModeQuickQA, LiveAnswer: "不应出现"},
			want:  false,
		},
	}
	for _, c := range cases {
		if got := IMAnswerStarted(c.parts); got != c.want {
			t.Errorf("%s: IMAnswerStarted = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIMAnswerText(t *testing.T) {
	cases := []struct {
		name  string
		parts IMStreamParts
		want  string
	}{
		{
			name:  "agent answer wins over live answer",
			parts: IMStreamParts{Mode: IMStreamModeAgent, LiveAnswer: "片段", Answer: "完整答案"},
			want:  "完整答案",
		},
		{
			name:  "agent live answer while streaming",
			parts: IMStreamParts{Mode: IMStreamModeAgent, AgentInner: "思考", LiveAnswer: " 片段 "},
			want:  "片段",
		},
		{
			name:  "agent thinking only",
			parts: IMStreamParts{Mode: IMStreamModeAgent, AgentInner: "思考", AgentToolSteps: []IMToolStep{{ToolName: "wiki_search"}}},
			want:  "",
		},
		{
			name:  "quick qa ignores live answer",
			parts: IMStreamParts{Mode: IMStreamModeQuickQA, LiveAnswer: "不应出现"},
			want:  "",
		},
	}
	for _, c := range cases {
		if got := IMAnswerText(c.parts); got != c.want {
			t.Errorf("%s: IMAnswerText = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestShouldPushStreamFrame(t *testing.T) {
	thinking := IMStreamParts{Mode: IMStreamModeAgent, AgentInner: "思考中", AgentToolSteps: []IMToolStep{{ToolName: "wiki_search"}}}
	answering := IMStreamParts{Mode: IMStreamModeAgent, LiveAnswer: "答案片段"}

	if !ShouldPushStreamFrame(thinking, false) {
		t.Error("with hideThinking off every frame must pass (upstream behaviour)")
	}
	if ShouldPushStreamFrame(thinking, true) {
		t.Error("with hideThinking on a thinking-only frame must be withheld")
	}
	if !ShouldPushStreamFrame(answering, true) {
		t.Error("with hideThinking on an answer frame must pass")
	}
}

// placeholderAdapter mimics a streaming adapter that knows its placeholder.
type placeholderAdapter struct {
	Adapter
	text string
}

func (a placeholderAdapter) ThinkingPlaceholder() string { return a.text }

// plainAdapter mimics a streaming adapter that does not implement the optional
// interface; the embedded nil Adapter satisfies the rest of the interface.
type plainAdapter struct {
	Adapter
}

func TestIMThinkingPlaceholder(t *testing.T) {
	if got := IMThinkingPlaceholder(placeholderAdapter{text: "💭 正在思考..."}); got != "💭 正在思考..." {
		t.Errorf("placeholder = %q, want %q", got, "💭 正在思考...")
	}
	if got := IMThinkingPlaceholder(placeholderAdapter{text: "  "}); got != "" {
		t.Errorf("blank placeholder = %q, want empty", got)
	}
	if got := IMThinkingPlaceholder(plainAdapter{}); got != "" {
		t.Errorf("adapter without the interface = %q, want empty", got)
	}
}
