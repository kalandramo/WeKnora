package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Tencent/WeKnora/internal/metrics"
)

// scrape renders the /metrics payload served by metrics.Handler.
func scrape(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metrics.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d, want %d", rec.Code, http.StatusOK)
	}
	return rec.Body.String()
}

// TestMetricsAreExposed verifies that every collector this package registers is
// actually served, guarding the wiring between internal/metrics and the route
// in internal/router/router.go.
func TestMetricsAreExposed(t *testing.T) {
	// Materialize each series: gauges export their value once a label
	// combination exists, but a CounterVec only exports after an increment.
	metrics.LLMInflight.WithLabelValues("test-model").Inc()
	metrics.LLMInflight.WithLabelValues("test-model").Dec()
	metrics.LLMBackgroundInflight.WithLabelValues("test-model").Inc()
	metrics.LLMBackgroundInflight.WithLabelValues("test-model").Dec()
	metrics.LLMTotal.WithLabelValues("test-model", "answer").Inc()
	metrics.LLMRequestErrors.WithLabelValues("test-model", "entity_extraction", "403").Inc()
	metrics.SetModelConcurrencyLimit("model-id-1", "test-model", 4)
	metrics.SetBuildInfo("v0.9.0-watsons", "88721da0e")

	body := scrape(t)
	for _, name := range []string{
		"weknora_llm_requests_inflight",
		"weknora_llm_requests_total",
		"weknora_llm_request_errors_total",
		"weknora_llm_background_requests_inflight",
		"weknora_llm_model_concurrency_limit",
		"weknora_build_info",
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
	// Build identity must be readable from the labels, not just be present.
	if !strings.Contains(body, `weknora_build_info{commit_id="88721da0e",version="v0.9.0-watsons"} 1`) {
		t.Errorf("/metrics did not expose the expected build labels; body sample:\n%s", firstLines(body, 20))
	}
}

// TestRegistryIgnoresGlobalCollectors pins the reason Registry exists. WeKnora
// links libraries (etcd server, via the Milvus client) that call
// prometheus.MustRegister from package init, which put ~200 permanently-zero
// etcd_* series on the default registry — enough for a bare
// `etcd_server_has_leader == 0` rule to fire against a WeKnora pod. A
// collector registered globally must therefore never reach /metrics.
func TestRegistryIgnoresGlobalCollectors(t *testing.T) {
	leak := &constCollector{desc: prometheus.NewDesc(
		"weknora_test_global_leak",
		"Registered on the default registry only; must not appear in /metrics.",
		nil, nil,
	)}
	prometheus.MustRegister(leak)
	t.Cleanup(func() { prometheus.Unregister(leak) })

	// Sanity check: the collector really is live on the global registry.
	global := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(global, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(global.Body.String(), "weknora_test_global_leak") {
		t.Fatal("test setup failed: collector was not registered globally")
	}

	if body := scrape(t); strings.Contains(body, "weknora_test_global_leak") {
		t.Errorf("a globally registered collector leaked into /metrics:\n%s", firstLines(body, 20))
	}
}

// constCollector is a minimal collector exporting a constant 1.
type constCollector struct{ desc *prometheus.Desc }

func (c *constCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *constCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, 1)
}

// firstLines returns the first n lines of s, for readable test failure output.
func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
