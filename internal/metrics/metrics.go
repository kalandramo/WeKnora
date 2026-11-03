// Package metrics exposes WeKnora runtime metrics for scraping by Prometheus.
//
// All collectors are registered with the default Prometheus registry via
// promauto, so they are automatically served by promhttp.Handler() at the
// /metrics endpoint (see internal/router/router.go). Because Prometheus
// samples the gauge on its scrape interval, the in-flight counter becomes a
// historical time-series: you can read WeKnora's LLM request concurrency at
// any past scrape timestamp without any extra retention logic.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// LLMInflight is the number of OpenAI-compatible LLM requests currently
	// in flight — from request send until the response body is fully consumed
	// (including streaming reads that happen after send() returns). This is
	// WeKnora's own outbound concurrency and is intentionally independent of
	// the model-service-side concurrency limiter (the "模型并发占用" panel).
	LLMInflight = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "weknora_llm_requests_inflight",
		Help: "Number of in-flight LLM (OpenAI-compatible) requests currently being sent by WeKnora.",
	})

	// LLMTotal counts total LLM requests sent, labeled by call purpose
	// (e.g. answer, query_rewrite, entity_extraction, document_summary).
	// rate(weknora_llm_requests_total[1m]) yields requests-per-second.
	LLMTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "weknora_llm_requests_total",
		Help: "Total LLM requests sent by WeKnora, labeled by call purpose.",
	}, []string{"purpose"})

	// LLMRequestErrors counts failed LLM send attempts, labeled by call
	// purpose and status. status is the vendor's HTTP status code as a string
	// (e.g. "403", "429", "503"), "transport" for network-layer failures, or
	// "error" when no status applies. Use it to alert on the 403 quota wall:
	// rate(weknora_llm_request_errors_total{status="403"}[5m]).
	LLMRequestErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "weknora_llm_request_errors_total",
		Help: "Total failed LLM requests sent by WeKnora, labeled by purpose and status.",
	}, []string{"purpose", "status"})
)
