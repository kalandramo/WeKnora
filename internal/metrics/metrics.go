// Package metrics exposes WeKnora runtime metrics for scraping by Prometheus.
//
// All collectors are registered with the default Prometheus registry via
// promauto, so they are automatically served by promhttp.Handler() at the
// /metrics endpoint (see internal/router/router.go). Because Prometheus
// samples the gauge on its scrape interval, the in-flight counter becomes a
// historical time-series: you can read WeKnora's LLM request concurrency at
// any past scrape timestamp without any extra retention logic.
//
// Every series carries a "model" label holding the wire model name
// (Endpoint.Model), because a deployment typically talks to several models
// with independent quotas and concurrency limits. Aggregating across models
// hides which backend actually hit its wall, so dashboards and alerts should
// group by model rather than assume a single global ceiling.
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
	//
	// Labeled by model: each backend has its own concurrency limit, so a
	// single global number cannot tell you which one is saturated. Use
	// sum(weknora_llm_requests_inflight) for the process-wide total.
	LLMInflight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "weknora_llm_requests_inflight",
		Help: "Number of in-flight LLM (OpenAI-compatible) requests currently being sent by WeKnora, by model.",
	}, []string{"model"})

	// LLMTotal counts total LLM requests sent, labeled by model and call
	// purpose (e.g. answer, query_rewrite, entity_extraction,
	// document_summary). rate(weknora_llm_requests_total[1m]) yields
	// requests-per-second.
	LLMTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "weknora_llm_requests_total",
		Help: "Total LLM requests sent by WeKnora, by model and call purpose.",
	}, []string{"model", "purpose"})

	// LLMRequestErrors counts failed LLM send attempts, labeled by model,
	// call purpose and status. status is the vendor's HTTP status code as a
	// string (e.g. "403", "429", "503"), "transport" for network-layer
	// failures, or "error" when no status applies. Use it to alert on the
	// 403 quota wall per model:
	// rate(weknora_llm_request_errors_total{status="403"}[5m]).
	LLMRequestErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "weknora_llm_request_errors_total",
		Help: "Total failed LLM requests sent by WeKnora, by model, purpose and status.",
	}, []string{"model", "purpose", "status"})
)
