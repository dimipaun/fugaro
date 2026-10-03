package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// Past timeouts.total, writeback's uploads, its lock release and the final
// record must all fit in the task timeout's slack, with the container's
// startup (StartedAt comes after Cloud Run's task start) to spare.
func TestWritebackFitsTheTaskTimeoutSlack(t *testing.T) {
	if sum := writebackGrace + releaseDeferredTimeout + recordWriteTimeout + startupMargin; sum > backend.TaskTimeoutSlack {
		t.Fatalf("writeback grace %v + lock release %v + final record %v + startup %v = %v, past the task timeout's slack %v",
			writebackGrace, releaseDeferredTimeout, recordWriteTimeout, startupMargin, sum, backend.TaskTimeoutSlack)
	}
	if writebackGrace < writebackFloor {
		t.Fatalf("writeback grace %v is below its floor %v", writebackGrace, writebackFloor)
	}
}

func TestModelByAddsUpToTheModelCost(t *testing.T) {
	by := map[string]pricing.Micros{"a": 40_000, "b": 100_000}
	got := modelByUSD(by, 150_000)
	if len(got) != 3 || got["a"] != 0.04 || got["b"] != 0.1 || got[unattributedModel] != 0.01 {
		t.Fatalf("model_by = %v, want the 10,000 µ$ of a late call as %q", got, unattributedModel)
	}
	if got := modelByUSD(by, 140_000); len(got) != 2 {
		t.Fatalf("an exact split gained an entry: %v", got)
	}
	if got := modelByUSD(nil, 0); len(got) != 0 {
		t.Fatalf("no spend: %v", got)
	}
}

func TestRouteByCarriesLateSettledSpend(t *testing.T) {
	modelBy := map[string]pricing.Micros{"deepseek/x": 40_000, "claude": 100_000}
	routeBy := map[string]pricing.Micros{"openrouter": 40_000}
	got := routeByUSD(routeBy, modelBy, 150_000, true)
	if len(got) != 2 || got["openrouter"] != 0.04 || got[unattributedModel] != 0.01 {
		t.Fatalf("route_by = %v, want the 10,000 µ$ of a late call as %q", got, unattributedModel)
	}
	if got := routeByUSD(routeBy, modelBy, 140_000, true); len(got) != 1 {
		t.Fatalf("an exact split gained an entry: %v", got)
	}
	// Without routes there is no route_by, late spend or not.
	if got := routeByUSD(nil, modelBy, 150_000, false); got != nil {
		t.Fatalf("a run without routes has route_by %v", got)
	}
	if got := routeByUSD(nil, nil, 0, true); got != nil {
		t.Fatalf("no spend: %v", got)
	}
}
