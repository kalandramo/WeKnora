package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Tencent/WeKnora/internal/metrics"
)

// TestMetricsAreExposed verifies that the collectors backing the /metrics
// endpoint are registered with the default Prometheus registry that
// promhttp.Handler() serves. This guards the wiring between internal/metrics
// and the route registered in internal/router/router.go.
func TestMetricsAreExposed(t *testing.T) {
	// Exercise both collectors so every series has been materialized: gauges
	// export their current value (even 0), but a CounterVec only exports a
	// label combination after it has been incremented once.
	metrics.LLMInflight.WithLabelValues("test-model").Inc()
	metrics.LLMInflight.WithLabelValues("test-model").Dec()
	metrics.LLMTotal.WithLabelValues("test-model", "answer").Inc()
	metrics.LLMRequestErrors.WithLabelValues("test-model", "entity_extraction", "403").Inc()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	promhttp.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, name := range []string{
		"weknora_llm_requests_inflight",
		"weknora_llm_requests_total",
		"weknora_llm_request_errors_total",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("/metrics did not expose %q", name)
		}
	}
	// The model dimension is what lets a multi-model deployment tell which
	// backend saturated its own quota; a missing label silently degrades every
	// per-model panel and alert into a global average.
	if !strings.Contains(body, `model="test-model"`) {
		t.Errorf("/metrics did not expose the model label; body sample:\n%s", firstLines(body, 20))
	}
}

// firstLines returns the first n lines of s, for readable test failure output.
func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
