package runner

import (
	"fmt"
	"math"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/routing"
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
// A stage the run's stage-scoped Claude OAuth account served is the third
// case and the one that is about the stage rather than about the run (P11):
// the gateway is on for the run and saw none of this stage's calls, so its
// figure is Claude Code's own result event and all of it is notional, with no
// call count and the credential that says so (docs/mixed-credentials/spec.md
// §5). Asking the run instead — `r.gw != nil`, true for every mixed run — would
// give the stage r.lastStage's figure, which is the PREVIOUS stage's.
//
// subAgents and repeats are the relay's counts over the stage's own stream:
// its sub-agent tool calls, and the most times one identical shell command
// ran (task-governance T1.3, T1.4).
//
// The stages sum to cost.model_usd only up to gateway spend no stage
// report carried: a call that settles after its stage's wait ran out is in
// the ledger's total (gwUsed, so in cost.model_usd) and in the split only
// as cost.model_by's "unattributed" (modelByUSD). Precisely, over the
// run's stages minus that residual, and to the micro-dollar:
//
//	sum(stages[].model_usd) + cost.model_by["unattributed"] == cost.model_usd
//
// which TestStageTimingLateSpendIsUnattributed pins, and which holds for a
// mixed run because r.direct is in both sides of it (updateCost). Model names
// the model the stage was pinned to, never what the upstream served, and a
// gateway stage may also have made background-model calls: both are inside
// this stage's ModelUSD, so it is the stage's spend, not the pinned model's.
func (r *run) stageTiming(name string, started time.Time, claudeUSD float64, subAgents, repeats int) runstore.StageTiming {
	// The stage's chain facts, taken and cleared here because this row is
	// their only place (C1, CS8): which source the credentials service served
	// the stage's calls from and whether any of them came from a source that
	// is not the chain's first. The next stage starts with none of this
	// one's.
	source, fallback := r.chainStage()
	st := runstore.StageTiming{
		// The source and the flag are the service's answers about the stage's
		// own calls, nil and false for every stage it never answered one of.
		Source:    source,
		Fallback:  fallback,
		Name:      name,
		StartedAt: started.UTC(),
		DurationS: r.d.Now().Sub(started).Seconds(),
		Model:     r.stageModel(name),
		// The credential that served this stage, which for every stage but an
		// account's is the run's own: the auth its model path takes, "" for
		// the auths that name no single credential class (F18, D1415). The
		// account arm below overrides it, because the credential that served
		// that stage is the account's and not the run's.
		ServedBy: runstore.CredentialForAuth(r.servedAuth()),
		// The stage's sub-agent tool calls, counted by the relay from its
		// own stream for every auth mode. Always set: the relay counts even
		// a stage that spawned none, and 0 sub-agents is a figure, unlike an
		// absent call count (G5).
		Agents: &subAgents,
		// The relay's count of the most times one identical shell command ran
		// in the stage, always set like Agents and for the same reason: 0 is a
		// stage that ran no command, not a stage nobody counted (G5).
		Repeats: &repeats,
	}
	if r.stageDirect(name) {
		st.ModelUSD = claudeUSD
		st.ModelNotionalUSD = claudeUSD
		st.Credential = runstore.CredentialOAuth
		// The account's credential is not the run's own, so this is the one
		// stage whose ServedBy is not the auth above -- and the field carries
		// it for every stage, where Credential carries it for this one alone.
		st.ServedBy = runstore.CredentialOAuth
		return st
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
		// How many model calls the stage made, as the gateway counted the
		// calls it forwarded. It is absent without a gateway: an oauth run
		// has no call count, and that is not the same fact as zero (G5).
		calls := rep.Calls
		st.Calls = &calls
		// The stage's whole model figure and the notional part of it, as
		// the gateway settled them (C1.16, D140): Used is the money its
		// calls paid and Notional the tier-A calls, and the two are
		// disjoint.
		st.ModelUSD = (rep.Used + rep.Notional).USD()
		st.ModelNotionalUSD = rep.Notional.USD()
	}
	return st
}

// chainAnswered folds one call's answer into the stage running now: the source
// the credentials service served that call from, and whether it was a fallback
// (C1's ChainAnswer, CS8).
//
// It is the run's hook -- a gateway.ChainAnswerFunc the run's gateway is given
// when the credentials service serves its calls (CS2's wiring) -- and it is
// called from the gateway's own goroutines, once per call the service answered,
// with no lock of either side held: everything it touches is under r.mu. What
// it keeps is exactly what the contract leaves to the step's record and no more
// than one stage's worth, because the gateway holds no per-stage figure at all
// -- it does not know where a stage begins or ends. The flag is folded, so the
// stage is a fallback stage once ANY of its calls came from a source that is
// not the chain's first, and the source is the last answered call's, which is
// the one the stage ended on.
// The same two facts reach the live registry entry from here (D3070, CS9), so
// that a run in flight shows what its calls go out on: `source` as the wire
// spells it and `fallback` as the stage's own. The entry takes them to the
// stage's row being built, which is where chainStage clears them.
//
// The hook's shape is the contract's: held here at compile time, so a change to
// C1's ChainAnswerFunc fails in this package rather than in the wiring that
// hands the run's hook to its gateway.
var _ gateway.ChainAnswerFunc = (&run{}).chainAnswered

func (r *run) chainAnswered(a gateway.ChainAnswer) {
	source := a.Source
	r.mu.Lock()
	r.chainSource = &source
	if a.Fallback {
		r.chainFallback = true
	}
	text, fallback := chainSourceText(source), r.chainFallback
	r.mu.Unlock()
	r.noteRegistry(func(e *budget.AgentEntry) {
		e.Source, e.Fallback = text, fallback
	})
}

// chainSourceText is a routing.SourceName as the live registry entry and
// `fugaro watch --json` spell it: the kind, and the name after a colon when the
// kind names one (`pool:team`, `anthropic`; contract-table.md, D3070). The
// record's own member is the object (runstore's stages[].source) and keeps the
// shape routing.SourceName declares; this is the one string a row prints.
func chainSourceText(s routing.SourceName) string {
	if s.Name == "" {
		return string(s.Kind)
	}
	return string(s.Kind) + ":" + s.Name
}

// chainStage is what the service's answers said about the stage that has just
// ended, taken once by that stage's row and cleared so the next stage starts
// with none of it. A stage the service answered no call of gets an absent
// source and a false flag, which is the same thing the record carries for a
// run whose calls do not go through the service at all.
//
// Clearing with the row is what makes the fold a stage's rather than the run's,
// and it is where a late answer goes: a call that settled after its stage's row
// was taken -- the case closeGateway records money for -- can name the next
// stage's source. Nothing is decided by the figure, it is a name the run's own
// record carries, and naming the next stage's source is the direction to keep
// against dropping a stage's source altogether. The live registry entry is
// cleared here too, so the two never name different stages.
func (r *run) chainStage() (source *routing.SourceName, fallback bool) {
	r.mu.Lock()
	source, fallback = r.chainSource, r.chainFallback
	r.chainSource, r.chainFallback = nil, false
	r.mu.Unlock()
	// The live entry is cleared with them and for the same reason: the stage
	// they were folded over is the one whose row has just been built, and a
	// row that kept its source would draw the next stage's answer beside this
	// one's flag (chainAnswered). An absent member is "not recorded", which is
	// what a stage that has answered nothing yet is.
	r.noteRegistry(func(e *budget.AgentEntry) { e.Source, e.Fallback = "", false })
	return source, fallback
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
	// The run's token counts by model, from whichever path filled them
	// (G1.2, F8): what a reader prices again on another model. Copied, so a
	// later stage's counts cannot change a cost a reader already holds. nil
	// when the run recorded none, which is also what the record writes for an
	// empty map -- a run that made no model call has no figures, and the
	// difference between that and a call count nobody kept is the record's
	// (G5).
	c.ModelTokens = copyModelTokens(r.modelTokens)
	var notional float64
	if gateway {
		// The splits are of the run's whole model figure, notional dollars
		// included: the gateway reports a model's amount whatever paid for
		// the call, so model_by adds up to model_usd -- with the late
		// "unattributed" residual -- only when the total it is measured
		// against is the same Used + Notional the stages carry.
		//
		// r.direct is in that total and in the split beside them: the account
		// stage's dollars are Claude Code's own figure and no ledger's, and
		// leaving them out of the split would bring them back as an
		// "unattributed" residual (modelByUSD) that attributes them to
		// nobody (P12).
		model := r.gwUsed + r.gwNotional + r.direct
		// The real money the run's fallback calls spent (C1, CS8, D140), the
		// ledger's figure. It is a part of model_usd above and of nothing
		// else: the calls are charged like the calls they fell back from, so
		// adding this figure to the run's totals would charge them twice.
		c.FallbackUSD = r.gwFallback.USD()
		c.Unreconciled = r.unreconciled.USD()
		c.ModelBy = modelByUSD(r.modelBy, model)
		c.ReportedUSD = r.reported.USD()
		// The provider reports and the table band behind them, by serving
		// model, are the ledger's figures and never a sum of the stage
		// reports: a call that settled after its stage's wait ran out is in
		// the ledger's and in no report's, so a figure built by adding the
		// two would count that call twice (F24, D2650, SA1.5). The band's
		// ends are sums over the models that reported a cost, with no route
		// fee, so a reader comparing a report against them compares the same
		// quantity the charge is measured in.
		c.ReportedBy = microsByUSD(r.reportedBy)
		c.TableLoUSD = sumMicros(r.tableLoBy).USD()
		c.TableHiUSD = sumMicros(r.tableHiBy).USD()
		c.UnreportedCalls = r.unreportedCalls
		c.RouteBy = routeByUSD(r.routeBy, r.modelBy, model, r.routed)
		// The run's prompt-cache token totals, which only the gateway can
		// count: they are of its per-call usage and of nothing else (P12).
		c.CacheReadTokens, c.CacheWriteTokens = r.gwCacheRead, r.gwCacheWrite
		// The tiers a call came back at that the table has no multiplier
		// for: their calls were charged conservatively (design §6.1), so
		// the record naming them is what explains the figure a reader sees
		// (on a provider route nothing else does: the provider's own
		// reported cost is recorded, and floors that charge rather than
		// pricing it). List bounds the names the record carries, however
		// many distinct tiers the calls came back at.
		c.ServiceTiers = r.serviceTiers.List()
		notional = r.gwNotional.USD() + r.direct.USD()
		// The catalog only prices the routes' models, so its staleness is a
		// fact about what this run charged: a table that holds a stale row for
		// a model the run never called, or priced by hand, over-counted
		// nothing (design §7, P7).
		c.CatalogStale = catalogStale(r.spend.Prices, chargedModels(r.modelBy))
	} else if basis == runstore.BasisSubscription {
		// No gateway and a subscription: Claude Code's own figure is the
		// run's model spend and all of it is notional (C1.16, P12).
		notional = c.ModelUSD
	} else {
		// No gateway and a list-price basis: every stage's figure is Claude
		// Code's own, and the account stage's is still notional -- its calls
		// were carried by the subscription whoever else was paying, so it is
		// in no cap and in no bill (P12, P15).
		notional = r.direct.USD()
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

// recordAccountStageCost is the run's own account of a stage the stage-scoped
// Claude OAuth account served, taken where runner.go takes a gateway stage's
// figure: the gateway settled nothing for it, so its figure is Claude Code's
// own result event, and it is kept in an accumulator of its own beside the
// ledger's two.
//
// The accumulator is separate because both of a gateway stage's figures are
// written from the ledger, which knows nothing of this stage (P11): accumulated
// into r.rec.CostUSD alone -- the obvious reading of the ledger's overwrite --
// the figure would be gone the moment the next gateway stage ran. It goes into
// model_by under the model the stage ran on for the same reason the gateway's
// spend does: the split is of the run's whole model figure, and a figure left
// out of it comes back as the "unattributed" residual (modelByUSD).
//
// With no gateway the ledger's two figures are always zero, so overwriting
// from them would erase every earlier stage's dollars: that run adds instead.
func (r *run) recordAccountStageCost(stage string, claudeUSD float64) {
	m := r.usdMicros(claudeUSD)
	r.mu.Lock()
	r.direct += m
	if r.modelBy != nil {
		r.modelBy[r.stageModel(stage)] += m
	}
	if r.gw == nil {
		r.rec.CostUSD += m.USD()
	} else {
		r.rec.CostUSD = (r.gwUsed + r.gwNotional + r.direct).USD()
	}
	r.mu.Unlock()
}

// recordAgentModelTokens takes a stage's own per-model usage into the run's
// counts. It is the arm of a run with no gateway: an oauth run and an api-key
// or Vertex run whose budget mode is off send every call straight to the
// model, so the agent's own result event is the only account of them (F8).
//
// A run whose gateway is on takes its counts from the gateway's ledger, in
// closeGateway, and never from here: the record's contract is that such a
// run's counts are what its cache totals are summed from, and the calls of a
// stage the run's stage-scoped Claude OAuth account served are in no such
// total, so adding this stage's report beside the ledger's would break that
// sum for exactly the mixed runs that have both arms (P11, P12).
func (r *run) recordAgentModelTokens(res agent.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gw != nil {
		return
	}
	for model, u := range res.ModelUsage {
		// Claude Code reports a stage's cache creations as one count where the
		// gateway's usage has a 5m write and a 1h write: with no gateway there
		// is no TTL to keep, so all of it is booked as a 5m write, the way the
		// budget books the same figure (G1.1, F8).
		r.addModelTokens(model, runstore.ModelTokenCounts{
			Input:        u.Input,
			Output:       u.Output,
			CacheRead:    u.CacheRead,
			CacheWrite5m: u.CacheCreation,
		})
	}
}

// addModelTokens adds one stage's counts to the run's per-model map, kind by
// kind. The caller holds r.mu.
func (r *run) addModelTokens(model string, add runstore.ModelTokenCounts) {
	if r.modelTokens == nil {
		r.modelTokens = map[string]runstore.ModelTokenCounts{}
	}
	c := r.modelTokens[model]
	c.Input = satTokens(c.Input, add.Input)
	c.Output = satTokens(c.Output, add.Output)
	c.CacheRead = satTokens(c.CacheRead, add.CacheRead)
	c.CacheWrite5m = satTokens(c.CacheWrite5m, add.CacheWrite5m)
	c.CacheWrite1h = satTokens(c.CacheWrite1h, add.CacheWrite1h)
	r.modelTokens[model] = c
}

// modelTokenCounts is the gateway's per-model usage as the record carries it:
// the kinds a repricing reads, with the web searches the gateway also prices
// left out -- they are charged apart and no token figure carries them
// (runstore.ModelTokenCounts). nil for a ledger that recorded none, so the
// record omits the key rather than writing an empty map.
func modelTokenCounts(by map[string]pricing.Usage) map[string]runstore.ModelTokenCounts {
	if len(by) == 0 {
		return nil
	}
	out := make(map[string]runstore.ModelTokenCounts, len(by))
	for m, u := range by {
		out[m] = runstore.ModelTokenCounts{
			Input:        u.Input,
			Output:       u.Output,
			CacheRead:    u.CacheRead,
			CacheWrite5m: u.CacheWrite5m,
			CacheWrite1h: u.CacheWrite1h,
		}
	}
	return out
}

// satTokens adds two token counts, saturating at the largest int64 and
// clamping a negative to zero, the way every other sum of these figures does
// (pricing's satAdd, the gateway's, the budget's). A count that arrives
// absurd -- a provider reporting near MaxInt64, a result event spelling one
// negative -- must leave the run's entry bounded and non-negative: the
// record's schema refuses a negative count, and a result.json a reader
// refuses is worse than a figure pinned at the ceiling.
func satTokens(a, b int64) int64 {
	a, b = max(a, 0), max(b, 0)
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// copyModelTokens is the run's per-model counts as the record carries them,
// copied so a reader's map and a later stage's accumulation are not the same
// map. nil for a run that recorded none, so the record omits the key rather
// than writing the empty map a reader would have to know to read as none.
func copyModelTokens(by map[string]runstore.ModelTokenCounts) map[string]runstore.ModelTokenCounts {
	if len(by) == 0 {
		return nil
	}
	out := make(map[string]runstore.ModelTokenCounts, len(by))
	for m, c := range by {
		out[m] = c
	}
	return out
}

// usdMicros is a stage's USD figure as the micro-dollars the ledger and the
// splits are kept in. A figure that cannot be one is 0 and a warning, never a
// failure: a stage that reported an unreadable cost is recorded as having cost
// nothing rather than refused, which is the direction reportNotional takes with
// the same figure.
func (r *run) usdMicros(usd float64) pricing.Micros {
	if usd <= 0 {
		return 0
	}
	m, err := pricing.FromUSD(usd)
	if err != nil {
		r.d.Log.Warn("a stage's cost figure is not usable as a notional amount", "usd", usd)
		return 0
	}
	return m
}

// unattributedModel is the model_by entry for spend no stage report
// carried: a call that settled after its stage's wait ran out is in the
// ledger's total but in no stage's split.
const unattributedModel = "unattributed"

// microsByUSD is a per-model figure as the record's maps carry it: no
// residual and no model added, because the ledger's keys are the whole of
// what the figure covers -- the "unattributed" entry modelByUSD adds is a
// fact about a split of a total, and a report or a band has no such total
// to fall short of. nil for an empty map, so the record omits the key
// rather than writing the empty map a reader would have to know to read as
// none.
func microsByUSD(by map[string]pricing.Micros) map[string]float64 {
	if len(by) == 0 {
		return nil
	}
	out := make(map[string]float64, len(by))
	for m, v := range by {
		out[m] = v.USD()
	}
	return out
}

// sumMicros is a per-model figure's run total: what the record's band ends
// carry, which are one figure each rather than a split.
func sumMicros(by map[string]pricing.Micros) pricing.Micros {
	var sum pricing.Micros
	for _, v := range by {
		sum += v
	}
	return sum
}

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
