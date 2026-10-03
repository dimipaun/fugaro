package runner

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// updateCost refreshes the record's cost breakdown from the model spend so
// far and the elapsed compute time at list price.
func (r *run) updateCost() {
	if r.cfg == nil {
		return
	}
	basis := runstore.ModelBasis(r.cfg.Agent.Auth)
	c := runstore.ModelOnlyCost(r.rec.CostUSD, basis)
	if r.d.Prices != nil && r.wf.Resources.CPU > 0 {
		if gib, err := backend.MemoryGiB(r.wf.Resources.Memory); err == nil {
			compute := r.d.Prices.ComputeUSD(float64(r.wf.Resources.CPU), gib, r.d.Now().Sub(r.rec.StartedAt))
			c = runstore.NewCost(r.rec.CostUSD, compute, basis)
		}
	}
	r.mu.Lock()
	gateway := r.gw != nil
	c.UsageUnparsed = r.unparsed
	if gateway {
		c.Unreconciled = r.unreconciled.USD()
		c.ModelBy = modelByUSD(r.modelBy, r.gwUsed)
		c.ReportedUSD = r.reported.USD()
		if len(r.routeBy) > 0 {
			c.RouteBy = make(map[string]float64, len(r.routeBy))
			for k, v := range r.routeBy {
				c.RouteBy[k] = v.USD()
			}
		}
	}
	r.mu.Unlock()
	c.ModelSource = "claude-code"
	if gateway {
		c.ModelSource = "gateway"
	}
	r.rec.Cost = &c
}

// unattributedModel is the model_by entry for spend no stage report
// carried: a call that settled after its stage's wait ran out is in the
// ledger's total but in no stage's split.
const unattributedModel = "unattributed"

// modelByUSD is the split of total by model in USD, with what the stage
// reports don't account for under "unattributed", so the split adds up to
// the model cost.
func modelByUSD(by map[string]pricing.Micros, total pricing.Micros) map[string]float64 {
	out := make(map[string]float64, len(by)+1)
	var sum pricing.Micros
	for m, v := range by {
		out[m] = v.USD()
		sum += v
	}
	if rest := total - sum; rest > 0 {
		out[unattributedModel] = rest.USD()
	}
	return out
}

// CostLine renders a cost breakdown for reports (design §10.1). It keys
// off ComputeEstimated, never off a zero compute figure.
func CostLine(c runstore.Cost) string {
	sub := c.ModelBasis == runstore.BasisSubscription
	switch {
	case !c.ComputeEstimated && sub:
		return fmt.Sprintf("**Cost:** model $%.2f notional, counted against the Claude subscription (compute not estimated)", c.ModelUSD)
	case !c.ComputeEstimated:
		return fmt.Sprintf("**Cost:** model $%.2f (compute not estimated)", c.ModelUSD)
	case sub:
		return fmt.Sprintf("**Cost:** ≈ $%.2f compute (estimate); model $%.2f notional, counted against the Claude subscription", c.ComputeUSD, c.ModelUSD)
	}
	return fmt.Sprintf("**Cost:** ≈ $%.2f (model $%.2f + compute $%.2f, estimate)", c.TotalUSD, c.ModelUSD, c.ComputeUSD)
}
