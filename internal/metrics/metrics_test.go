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
	metrics.LLMInflight.Inc()
	metrics.LLMInflight.Dec()
	metrics.LLMTotal.WithLabelValues("answer").Inc()
	metrics.LLMRequestErrors.WithLabelValues("entity_extraction", "403").Inc()

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
}
