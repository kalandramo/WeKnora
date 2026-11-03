// Package metrics exposes WeKnora runtime metrics for scraping by Prometheus.
//
// Collectors are registered with Registry — a registry private to WeKnora —
// and served by Handler at the /metrics endpoint (see
// internal/router/router.go). Because Prometheus samples the gauges on its
// scrape interval, the in-flight counter becomes a historical time-series: you
// can read WeKnora's LLM request concurrency at any past scrape timestamp
// without any extra retention logic.
//
// Every LLM series carries a "model" label holding the wire model name
// (Endpoint.Model), because a deployment typically talks to several models
// with independent quotas and concurrency limits. Aggregating across models
// hides which backend actually hit its wall, so dashboards and alerts should
// group by model rather than assume a single global ceiling.
package metrics

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the registry served at /metrics. It is deliberately private
// rather than prometheus.DefaultRegisterer.
//
// WeKnora links libraries that register collectors on the default registry
// from their package init(). The clearest case is go.etcd.io/etcd/server/v3,
// pulled in transitively through the Milvus client
// (github.com/milvus-io/milvus/pkg/v2/util/etcd): its metrics packages call
// prometheus.MustRegister unconditionally, so a WeKnora pod that never runs
// etcd still exported ~200 etcd_* series, all permanently zero. That is not
// merely noise — a bare rule such as `etcd_server_has_leader == 0` would match
// those samples and fire against a WeKnora pod, because the series carry only
// job/instance labels to tell them apart. Serving a private registry keeps
// /metrics limited to what this package registers, whatever future dependency
// decides to touch the global one.
var Registry = prometheus.NewRegistry()

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
	LLMInflight = promauto.With(Registry).NewGaugeVec(prometheus.GaugeOpts{
		Name: "weknora_llm_requests_inflight",
		Help: "Number of in-flight LLM (OpenAI-compatible) requests currently being sent by WeKnora, by model.",
	}, []string{"model"})

	// LLMTotal counts total LLM requests sent, labeled by model and call
	// purpose (e.g. answer, query_rewrite, entity_extraction,
	// document_summary). rate(weknora_llm_requests_total[1m]) yields
	// requests-per-second.
	LLMTotal = promauto.With(Registry).NewCounterVec(prometheus.CounterOpts{
		Name: "weknora_llm_requests_total",
		Help: "Total LLM requests sent by WeKnora, by model and call purpose.",
	}, []string{"model", "purpose"})

	// LLMRequestErrors counts failed LLM send attempts, labeled by model,
	// call purpose and status. status is the vendor's HTTP status code as a
	// string (e.g. "403", "429", "503"), "transport" for network-layer
	// failures, or "error" when no status applies. Use it to alert on the
	// 403 quota wall per model:
	// rate(weknora_llm_request_errors_total{status="403"}[5m]).
	LLMRequestErrors = promauto.With(Registry).NewCounterVec(prometheus.CounterOpts{
		Name: "weknora_llm_request_errors_total",
		Help: "Total failed LLM requests sent by WeKnora, by model, purpose and status.",
	}, []string{"model", "purpose", "status"})

	// LLMBackgroundInflight is the subset of LLMInflight that originates from
	// asynq background workers (ingestion, summary, graph extraction). Only
	// these calls are gated by the per-model concurrency governor, so this is
	// the correct numerator when comparing against
	// weknora_llm_model_concurrency_limit: interactive chat bypasses the
	// semaphore by design and would otherwise push utilisation past 100%.
	LLMBackgroundInflight = promauto.With(Registry).NewGaugeVec(prometheus.GaugeOpts{
		Name: "weknora_llm_background_requests_inflight",
		Help: "Number of in-flight LLM requests originating from background workers, by model.",
	}, []string{"model"})

	// LLMModelConcurrencyLimit is the effective per-model background
	// concurrency cap WeKnora resolved at runtime: the model's own
	// MaxConcurrency when set, otherwise the process-wide default
	// (WEKNORA_MODEL_MAX_CONCURRENCY, 32). Exposed so dashboards can plot
	// utilisation and alerts can fire on a ratio instead of a hard-coded
	// number that changes with every model.
	//
	// model_id is kept alongside model because the semaphore is keyed by model
	// ID: two records sharing one wire model name get independent slots whose
	// caps add up, so sum by (model) over this gauge is the backend's real cap.
	LLMModelConcurrencyLimit = promauto.With(Registry).NewGaugeVec(prometheus.GaugeOpts{
		Name: "weknora_llm_model_concurrency_limit",
		Help: "Effective per-model background concurrency limit configured in WeKnora, by model and model ID.",
	}, []string{"model", "model_id"})

	// BuildInfo is a constant-1 gauge identifying the running build. Without
	// it, an idle pod is indistinguishable from a pod running an old image:
	// every other series here is a vector that stays silent until a label
	// combination is instantiated, so a freshly started process with no LLM
	// traffic exports no weknora_* series at all. Querying
	// weknora_build_info answers "which build is actually serving" in one
	// step.
	BuildInfo = promauto.With(Registry).NewGaugeVec(prometheus.GaugeOpts{
		Name: "weknora_build_info",
		Help: "Build identity of the running WeKnora process. Always 1; read the labels.",
	}, []string{"version", "commit_id"})
)

// Handler serves Registry and backs GET /metrics.
//
// It is a package-level value so it is constructed exactly once: promhttp
// registers its own scrape counters (promhttp_metric_handler_*) into the
// registry named in HandlerOpts, which is what keeps those counters scoped to
// our endpoint instead of the global one.
var Handler = promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})

func init() {
	// promhttp.Handler() would have served these from the default registry;
	// with a private registry they must be registered explicitly, otherwise
	// Go runtime and process metrics silently disappear from /metrics.
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}

// SetModelConcurrencyLimit records the effective background concurrency limit
// for one model record. Called from the concurrency governor, so the value
// tracks runtime retuning of model.max_concurrency without a restart. An empty
// name is rendered as "unknown" to keep the label joinable with the other
// series.
func SetModelConcurrencyLimit(modelID, modelName string, limit int) {
	if modelID == "" || limit <= 0 {
		return
	}
	if modelName == "" {
		modelName = "unknown"
	}
	LLMModelConcurrencyLimit.WithLabelValues(modelName, modelID).Set(float64(limit))
}

// SetBuildInfo publishes the build identity injected at link time. Values are
// trimmed and blank ones are rendered as "unknown" so the series is always
// present and its labels stay joinable.
func SetBuildInfo(version, commitID string) {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "unknown"
	}
	commitID = strings.TrimSpace(commitID)
	if commitID == "" {
		commitID = "unknown"
	}
	BuildInfo.WithLabelValues(version, commitID).Set(1)
}
