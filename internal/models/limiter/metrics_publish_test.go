package limiter

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Tencent/WeKnora/internal/metrics"
)

// TestAcquirePublishesConcurrencyLimit locks in the link between the runtime
// governor and the exported limit gauge. Dashboards divide background in-flight
// by this value to get utilisation, so a stale or missing limit silently turns
// every "接近上限" alert into a lie.
func TestAcquirePublishesConcurrencyLimit(t *testing.T) {
	l := NewLocalLimiter().(*localLimiter)
	l.SetModelName("m1", "qwen3-27b")

	release, err := l.Acquire(context.Background(), "m1", 4)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	if got := gatherLimit(t, "m1"); got != 4 {
		t.Fatalf("limit gauge for m1 = %v, want 4", got)
	}

	// Retuning the limit must refresh the gauge on the next acquire; that is
	// how an operator's model.max_concurrency change becomes visible without a
	// restart.
	release2, err := l.Acquire(context.Background(), "m1", 7)
	if err != nil {
		t.Fatalf("acquire after retune: %v", err)
	}
	defer release2()

	if got := gatherLimit(t, "m1"); got != 7 {
		t.Fatalf("limit gauge after retune = %v, want 7", got)
	}
}

// TestSetModelNameAfterAcquireRepublishes covers the ordering where the limit is
// recorded before the display name is known (GateN passes an empty name): the
// gauge must not stay stuck under "unknown".
func TestSetModelNameAfterAcquireRepublishes(t *testing.T) {
	l := NewLocalLimiter().(*localLimiter)

	release, err := l.Acquire(context.Background(), "m2", 3)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	l.SetModelName("m2", "named-later")

	if got := gatherLimitByModel(t, "m2", "named-later"); got != 3 {
		t.Fatalf("limit gauge for m2/named-later = %v, want 3", got)
	}
}

// gatherLimit returns the limit exposed for modelID, or -1 when absent.
func gatherLimit(t *testing.T, modelID string) float64 {
	t.Helper()
	return gather(t, "", modelID)
}

// gatherLimitByModel returns the limit exposed for the modelID/model pair, or
// -1 when absent.
func gatherLimitByModel(t *testing.T, modelID, modelName string) float64 {
	t.Helper()
	return gather(t, modelName, modelID)
}

func gather(t *testing.T, wantModel, wantID string) float64 {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := reg.Register(metrics.LLMModelConcurrencyLimit); err != nil {
		t.Fatalf("register: %v", err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "weknora_llm_model_concurrency_limit" {
			continue
		}
		for _, metric := range family.GetMetric() {
			model, id := "", ""
			for _, pair := range metric.GetLabel() {
				switch pair.GetName() {
				case "model":
					model = pair.GetValue()
				case "model_id":
					id = pair.GetValue()
				}
			}
			if id != wantID {
				continue
			}
			if wantModel != "" && model != wantModel {
				continue
			}
			return metric.GetGauge().GetValue()
		}
	}
	return -1
}
