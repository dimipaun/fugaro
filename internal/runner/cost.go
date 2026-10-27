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
		// Without a gateway the figure is Claude Code's own, and a
		// subscription run's every dollar of it is notional (C1.16, P12):
		// that is the same statement cost.ModelNotionalUSD makes for the
		// run, and a stage's own split is the only place a mixed run's
		// notional dollars can be told from its money.
		if runstore.ModelBasis(r.cfg.Agent.Auth) == runstore.BasisSubscription {
			st.ModelNotionalUSD = claudeUSD
		}
		return st
	}
	if rep != nil {
		// The stage's whole model figure and the notional part of it, as
		// the gateway settled them (C1.16, D140): Used is the money its
		// calls paid and Notional the tier-A calls, and the two are
		// disjoint.
		st.ModelUSD = (rep.Used + rep.Notional).USD()
		st.ModelNotionalUSD = rep.Notional.USD()
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
	var notional float64
	if gateway {
		// The splits are of the run's whole model figure, notional dollars
		// included: the gateway reports a model's amount whatever paid for
		// the call, so model_by adds up to model_usd -- with the late
		// "unattributed" residual -- only when the total it is measured
		// against is the same Used + Notional the stages carry.
		model := r.gwUsed + r.gwNotional
		c.Unreconciled = r.unreconciled.USD()
		c.ModelBy = modelByUSD(r.modelBy, model)
		c.ReportedUSD = r.reported.USD()
		c.RouteBy = routeByUSD(r.routeBy, r.modelBy, model, r.routed)
		// The run's prompt-cache token totals, which only the gateway can
		// count: they are of its per-call usage and of nothing else (P12).
		c.CacheReadTokens, c.CacheWriteTokens = r.gwCacheRead, r.gwCacheWrite
		notional = r.gwNotional.USD()
		// The catalog only prices the routes' models, so its staleness is a
		// fact about what this run charged: a table that holds a stale row for
		// a model the run never called, or priced by hand, over-counted
		// nothing (design §7, P7).
		c.CatalogStale = catalogStale(r.spend.Prices, chargedModels(r.modelBy))
	} else if basis == runstore.BasisSubscription {
		// No gateway and a subscription: Claude Code's own figure is the
		// run's model spend and all of it is notional (C1.16, P12).
		notional = c.ModelUSD
	}
	r.mu.Unlock()
	// The notional part is what tells the run's money from what its
	// subscription paid (C1.16, D140). It is applied through WithNotional so
	// cost.total_usd -- billed dollars -- cannot drift from it: a mixed run's
	// model_basis is api-list for the whole run while its tier-A dollars are
	// in no total, which is why a reader splits on model_notional_usd and
	// never on the basis (P12).
	c = c.WithNotional(notional)
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

// costLine is the cost sentence itself, without the estimate's source. Its
// model figure is the money part of the model spend, so the arithmetic in it
// adds up -- TotalUSD holds no notional dollar (C1.16, P12) -- and a mixed
// run's notional part is said beside it rather than counted into it.
func costLine(c runstore.Cost) string {
	sub := c.AllNotional()
	notional := ""
	if !sub && c.ModelSubscriptionUSD() > 0 {
		notional = fmt.Sprintf("; model $%.2f notional, counted against the Claude subscription", c.ModelSubscriptionUSD())
	}
	switch {
	case !c.ComputeEstimated && sub:
		return fmt.Sprintf("**Cost:** model $%.2f notional, counted against the Claude subscription (compute not estimated)", c.ModelSubscriptionUSD())
	case !c.ComputeEstimated:
		return fmt.Sprintf("**Cost:** model $%.2f (compute not estimated)%s", c.ModelMoneyUSD(), notional)
	case sub:
		return fmt.Sprintf("**Cost:** ≈ $%.2f compute (estimate); model $%.2f notional, counted against the Claude subscription", c.ComputeUSD, c.ModelSubscriptionUSD())
	}
	return fmt.Sprintf("**Cost:** ≈ $%.2f (model $%.2f + compute $%.2f, estimate)%s", c.TotalUSD, c.ModelMoneyUSD(), c.ComputeUSD, notional)
}
