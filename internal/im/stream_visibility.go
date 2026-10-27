package im

import (
	"os"
	"strconv"
	"strings"
)

// EnvIMStreamHideThinking enables the "quiet thinking" stream policy: while the
// answer has not started, the IM card keeps its placeholder ("正在思考…")
// instead of being repainted with retrieval/tool progress on every flush tick.
// Answer text still streams normally once the model begins writing it.
//
// Unset or empty keeps the upstream behaviour (thinking and tool progress are
// visible while the pipeline runs).
const EnvIMStreamHideThinking = "IM_STREAM_HIDE_THINKING"

// streamHideThinking is read once at process start, matching the other IM
// environment switches. Only Go boolean literals are honoured
// (1/0/t/f/true/false, case-insensitive); anything else counts as off, so a
// typo never changes what users see without the operator noticing.
var streamHideThinking = parseIMBoolEnv(os.Getenv(EnvIMStreamHideThinking))

func parseIMBoolEnv(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return parsed
}

// IMStreamHideThinking reports whether thinking-phase frames must be withheld.
func IMStreamHideThinking() bool {
	return streamHideThinking
}

// IMAnswerText returns the answer text available so far, with no thinking
// content attached. Agent mode streams the reply into LiveAnswer until the run
// completes and into Answer afterwards; whichever is non-empty is what the card
// may show under the quiet-thinking policy.
func IMAnswerText(parts IMStreamParts) string {
	if answer := strings.TrimSpace(parts.Answer); answer != "" {
		return answer
	}
	if parts.Mode == IMStreamModeAgent {
		return strings.TrimSpace(parts.LiveAnswer)
	}
	return ""
}

// IMAnswerStarted reports whether parts already carry answer text, i.e. the
// model has finished thinking and is writing (or has written) the reply.
//
// Thinking-phase content does not count: agent thoughts, preambles retracted
// after a tool call, tool progress rows, and quick-QA pipeline rows all live
// outside Answer / LiveAnswer.
func IMAnswerStarted(parts IMStreamParts) bool {
	return IMAnswerText(parts) != ""
}

// ShouldPushStreamFrame decides whether an intermediate frame may be sent to
// the platform. With hideThinking off every frame passes, preserving upstream
// behaviour; with it on only frames that already contain answer text pass.
func ShouldPushStreamFrame(parts IMStreamParts, hideThinking bool) bool {
	if !hideThinking {
		return true
	}
	return IMAnswerStarted(parts)
}

// ThinkingPlaceholderProvider is implemented by streaming adapters that can
// name the placeholder their card shows before the first answer chunk. It
// exists so the quiet-thinking policy can restore that placeholder after
// withholding a frame the card had already rendered (a preamble that a tool
// call then retracted).
type ThinkingPlaceholderProvider interface {
	ThinkingPlaceholder() string
}

// IMThinkingPlaceholder returns the placeholder text for the adapter, or "" when
// the adapter cannot name one. Callers must not overwrite the card in that
// case: a placeholder in the wrong language is worse than leaving the last
// rendered text in place.
func IMThinkingPlaceholder(adapter Adapter) string {
	provider, ok := adapter.(ThinkingPlaceholderProvider)
	if !ok {
		return ""
	}
	return strings.TrimSpace(provider.ThinkingPlaceholder())
}
