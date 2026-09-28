package runner

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// updateCost refreshes the record's cost breakdown from the model spend so
// far and the elapsed compute time at list price.
func (r *run) updateCost() {
	if r.cfg == nil {
		return
	}
	var compute float64
	if r.d.Prices != nil {
		if gib, err := backend.MemoryGiB(r.wf.Resources.Memory); err == nil {
			compute = r.d.Prices.ComputeUSD(float64(r.wf.Resources.CPU), gib, r.d.Now().Sub(r.rec.StartedAt))
		}
	}
	c := runstore.NewCost(r.rec.CostUSD, compute, runstore.ModelBasis(r.cfg.Agent.Auth))
	r.rec.Cost = &c
}

// CostLine renders a cost breakdown for reports (design §10.1). A zero
// compute figure means compute was not estimated, never that it was free.
func CostLine(c runstore.Cost) string {
	sub := c.ModelBasis == runstore.BasisSubscription
	switch {
	case c.ComputeUSD == 0 && sub:
		return fmt.Sprintf("**Cost:** model $%.2f notional, counted against the Claude subscription (compute not estimated)", c.ModelUSD)
	case c.ComputeUSD == 0:
		return fmt.Sprintf("**Cost:** model $%.2f (compute not estimated)", c.ModelUSD)
	case sub:
		return fmt.Sprintf("**Cost:** ≈ $%.2f compute (estimate); model $%.2f notional, counted against the Claude subscription", c.ComputeUSD, c.ModelUSD)
	}
	return fmt.Sprintf("**Cost:** ≈ $%.2f (model $%.2f + compute $%.2f, estimate)", c.TotalUSD, c.ModelUSD, c.ComputeUSD)
}
