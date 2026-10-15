package runner

import (
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// stageTiming is the StageTiming for the stage that just ended, so that
// result.json says what each stage cost and which model it ran on
// (generic-tool plan Task 24; the cost preview reads it). The cost is the
// gateway's figure for the stage, which endGatewayStage has already taken
// into r.lastStage, or, without a gateway, what the agent's own result
// event reported (an oauth run). Read this right after the stage's agent
// returned, before anything else closes a stage in the gateway.
//
// The stages sum to cost.model_usd only up to gateway spend no stage
// report carried: a call that settles after its stage's wait ran out is in
// the ledger's total (gwUsed, so in cost.model_usd) and in the split only
// as cost.model_by's "unattributed" (modelByUSD). Precisely, over the
// run's stages minus that residual, and to the micro-dollar:
//
//	sum(stages[].model_usd) + cost.model_by["unattributed"] == cost.model_usd
//
// which TestStageTimingLateSpendIsUnattributed pins. Model names the model
// the stage was pinned to, never what the upstream served, and a gateway
// stage may also have made background-model calls: both are inside this
// stage's ModelUSD, so it is the stage's spend, not the pinned model's.
func (r *run) stageTiming(name string, started time.Time, claudeUSD float64) runstore.StageTiming {
	st := runstore.StageTiming{
		Name:      name,
		StartedAt: started.UTC(),
		DurationS: r.d.Now().Sub(started).Seconds(),
		Model:     r.cfg.Agent.ModelFor(config.StageRole(name)),
	}
	r.mu.Lock()
	gateway, rep := r.gw != nil, r.lastStage
	r.mu.Unlock()
	if !gateway {
		st.ModelUSD = claudeUSD
		return st
	}
	if rep != nil {
		st.ModelUSD = rep.Used.USD()
	}
	return st
}

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
		c.RouteBy = routeByUSD(r.routeBy, r.modelBy, r.gwUsed, r.routed)
		// The catalog only prices the routes' models, so its staleness is a
		// fact about what this run charged: a table that holds a stale row for
		// a model the run never called, or priced by hand, over-counted
		// nothing (design §7, P7).
		c.CatalogStale = catalogStale(r.spend.Prices, chargedModels(r.modelBy))
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

// routeByUSD is the split of the provider routes' spend in USD. Spend that no
// stage report carried (a call that settled after its stage's wait ran out)
// is in the ledger's total but has no stage, hence no route to be told from:
// as with modelByUSD it is "unattributed", and only a run that has routes
// can have it (it may be a Claude call's; the figure is never dropped). nil
// when the run has no routes or spent nothing through them.
func routeByUSD(by, modelBy map[string]pricing.Micros, total pricing.Micros, routed bool) map[string]float64 {
	if !routed {
		return nil
	}
	out := make(map[string]float64, len(by)+1)
	for m, v := range by {
		out[m] = v.USD()
	}
	var sum pricing.Micros
	for _, v := range modelBy {
		sum += v
	}
	if rest := total - sum; rest > 0 {
		out[unattributedModel] = rest.USD()
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// CostLine renders a cost breakdown for reports (design §10.1). It keys
// off ComputeEstimated, never off a zero compute figure.
//
// A compute estimate that came from a price table naming itself gets its
// source line underneath, which is where a reader learns that the figure is
// the Compute Engine VM table's -- and that the VM's boot disk and Cloud NAT
// are not in it (design compute-backend.md §5.2, C17). Cloud Run's list
// price records no source, so every line it renders is unchanged.
func CostLine(c runstore.Cost) string {
	line := costLine(c)
	if c.ComputeEstimated && c.ComputeSource != "" {
		line += "\n**Compute source:** " + c.ComputeSource
	}
	return line
}

// costLine is the cost sentence itself, without the estimate's source.
func costLine(c runstore.Cost) string {
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
