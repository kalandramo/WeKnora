package openaicompletions

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// TestLLMErrorStatus locks in the status classification the 403 alert relies
// on: HTTP status codes must be read off the typed error (including when it is
// wrapped), never sniffed from the error text.
func TestLLMErrorStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"http 403", &api.HTTPError{StatusCode: 403}, "403"},
		{"http 429", &api.HTTPError{StatusCode: 429}, "429"},
		{
			"wrapped http 403",
			fmt.Errorf("create chat completion: %w", &api.HTTPError{StatusCode: 403}),
			"403",
		},
		{
			"transport",
			&api.TransportError{Op: "send request", Err: errors.New("connection refused")},
			"transport",
		},
		{"generic", errors.New("boom"), "error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := llmErrorStatus(c.err); got != c.want {
				t.Fatalf("llmErrorStatus(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

// TestMetricModelLabel locks in the model-label value: the wire model name is
// what the vendor bills quotas against, so it must win over WeKnora's internal
// ID; empty configuration must never produce an empty label value.
func TestMetricModelLabel(t *testing.T) {
	cases := []struct {
		name string
		ep   api.Endpoint
		want string
	}{
		{"wire name preferred", api.Endpoint{Model: "qwen3-27b", ModelID: "uuid-1"}, "qwen3-27b"},
		{"trims spaces", api.Endpoint{Model: "  qwen3-27b  "}, "qwen3-27b"},
		{"falls back to model id", api.Endpoint{ModelID: "uuid-1"}, "uuid-1"},
		{"unknown when both empty", api.Endpoint{}, "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := New(Config{Endpoint: c.ep}).metricModel(); got != c.want {
				t.Fatalf("metricModel() = %q, want %q", got, c.want)
			}
		})
	}
}
