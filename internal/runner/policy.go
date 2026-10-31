package runner

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// defaultBranchData is fugaro.yaml as the repository's default branch has
// it, read once per run.
type defaultBranchData struct {
	done bool
	name string // the default branch
	data []byte
	err  error
}

// defaultBranchFile reads fugaro.yaml at origin/<default branch>: the file a
// run's project and policy are checked against, which no file in the
// checkout can choose. It asks origin for the default branch, fetches it and
// shows the file, once; later calls return the first answer (or error). The
// error messages are the project check's, unchanged.
func (r *run) defaultBranchFile(ctx context.Context) ([]byte, string, error) {
	d := &r.defFile
	if d.done {
		return d.data, d.name, d.err
	}
	d.done = true
	def, err := r.repo.DefaultBranch(ctx)
	if err != nil {
		d.err = fmt.Errorf("finding the default branch to read fugaro.yaml from: %w", err)
		return nil, "", d.err
	}
	d.name = def
	if err := r.repo.FetchBase(ctx, def); err != nil {
		d.err = fmt.Errorf("fetching default branch %s: %w", def, err)
		return nil, def, d.err
	}
	if d.data, err = r.repo.ShowFile(ctx, "refs/remotes/origin/"+def, "fugaro.yaml"); err != nil {
		d.err = fmt.Errorf("reading fugaro.yaml at origin/%s: %w", def, err)
		d.data = nil
	}
	return d.data, def, d.err
}

// resolvePolicy computes the run's effective budget once, at bootstrap, as
// the tightest of three layers: the owner's ceiling (the job environment,
// r.d.Spend), the default branch's fugaro.yaml and the run's own cfg. A
// layer can only tighten, so a branch (or the agent, which can edit
// fugaro.yaml mid-run) never loosens what the owner or the team set. The
// result is stored on the run (r.spend, r.policy) and never recomputed.
//
// A default-branch file whose policy keys are invalid is an error: the
// ceiling alone would silently drop the team's rules. When the job has no
// project (a local run) the default branch is not read, as in checkProject.
func (r *run) resolvePolicy(ctx context.Context, cfg *config.Config) error {
	c := r.d.Spend
	// The mode this run resolved at bootstrap is the per-run default under
	// the ceiling (D16, plan T6.1): a fraction of the owner's cap, and never
	// above it. It is read once, here, with the rest of the policy — the mode
	// a run has reached later in the run only ever rises, and a policy a
	// run's own stages could move would not be the ceiling it is.
	mode := r.reachedRigor()
	// The ceiling is the job environment's copy of the owner's config, and
	// sets no output limits (the project config has none).
	ceiling := envCeiling(c, mode)
	// The project layer's governance: block joins the ceiling, the way the
	// project's budget does: it is the one layer that may loosen Fugaro's own
	// defaults for those keys, and every layer under it only tightens (T7.1,
	// D331). It is placed here rather than in envCeiling because it does not
	// travel in the job environment — the project config carries no governance
	// — it arrives with the task, in the layer the run resolved. The policy
	// node has no governance either, so policyCeiling leaves it standing.
	//
	// Fugaro's own defaults are filled in here, because this is the layer every
	// later one is held to and none of them may loosen (Governance.WithDefaults,
	// T7.1). Without them the first layer to write a governance key would be the
	// one that wins with it: a repository's fugaro.yaml — on its default branch,
	// and on the branch the run checks out, which the agent can edit mid-run —
	// could raise a guardrail Fugaro ships and nothing would say so. A key the
	// project layer does write is kept exactly as it stands, looser than the
	// default included: that layer is where the guardrails are loosened.
	var gov policy.Governance
	if l := cfg.Layer; l != nil {
		gov = l.Governance.Policy()
	}
	ceiling.Governance = gov.WithDefaults()
	// The project's own policy, when it has published one, *is* the ceiling
	// for the keys it sets: the node is what the owner says now, where the
	// copy is what he said when the job was deployed (design §5, plan task
	// 1.3 step 3, 12a item 13). Every key the node is silent about stays the
	// environment's — the node replaces the ceiling key by key, never whole
	// (H1: a policy published for allow_repos alone used to drop the mode,
	// the cap and the kill switches the environment carried).
	//
	// A read that fails is returned as it is: the run does not start rather
	// than start on a ceiling the policy did not set.
	pp, found, err := r.projectPolicy(ctx)
	if err != nil {
		return err
	}
	var fromPolicy map[string]bool
	if found {
		if ceiling, c, fromPolicy, err = r.policyCeiling(pp, ceiling, c); err != nil {
			return err
		}
	}
	r.ceiling = ceiling

	var def policy.Layer
	if r.d.Project == "" {
		r.d.Log.Info("no project: not reading the budget policy from the default branch")
	} else {
		data, branch, err := r.defaultBranchFile(ctx)
		if err != nil {
			return err
		}
		p, err := config.PolicyOf(data)
		if err != nil {
			return fmt.Errorf("fugaro.yaml on the default branch (%s) has an invalid policy, so the run can't tell what the team allows: %w", branch, err)
		}
		def = policy.Layer{Mode: p.Mode, PerRunUSD: p.PerRunUSD, PerDayUSD: p.PerDayUSD, MaxRunTokens: p.MaxRunTokens, AllowedModels: p.AllowedModels,
			MaxOutputCoder: p.MaxOutputTokens.Coder, MaxOutputReviewer: p.MaxOutputTokens.Reviewer, Governance: p.Governance}
	}

	layers := []policy.Layer{ceiling, def, FileLayer(cfg)}
	r.govLayers = layers
	e := policy.Merge(layers[0], layers[1:]...)
	if found {
		// The merge names the first layer "ceiling": for the keys the node
		// set, that layer is the node and not the job environment, and a
		// run's result.json has to say which of the two each value was
		// (H1: the keys the node is silent about are still the environment's,
		// and saying "policy" for those would name the wrong owner).
		policySources(&e, fromPolicy)
	}
	// Merge lists the default branch's ignored values before the branch's,
	// so the number the first two layers drop says who asked for each.
	nDef := len(policy.Merge(ceiling, def).Ignored)
	s := Spend{Mode: e.Mode, MaxRunTokens: e.MaxRunTokens, AllowedModels: e.AllowedModels, Prices: c.Prices}
	if s.Mode == "" {
		s.Mode = policy.ModeOff
	}
	if e.PerRunUSD > 0 {
		if src := e.Sources[policy.KeyPerRunUSD]; src == policy.SourceCeiling {
			// The ceiling's own cap, scaled by the run's mode and not by a
			// float round trip through the layer's dollar figure (D16). A cap
			// some other layer wrote is that layer's own number and is taken
			// as it is: the mode's fraction is the default a run gets where
			// nothing says otherwise, never a multiplier over a number the
			// owner, the team or the branch set.
			s.Cap = config.RigorBudgetCap(mode, c.Cap)
		} else {
			var err error
			if s.Cap, err = pricing.FromUSD(e.PerRunUSD); err != nil || s.Cap < 1 {
				return fmt.Errorf("budget.per_run_usd %v: not a usable cap: %v", e.PerRunUSD, err)
			}
		}
	}
	if s.On() && s.Prices == nil {
		// The ceiling was off with no price overrides (they would have
		// been loaded); a committed observe or enforce charges the same
		// table, built with no overrides.
		prices, err := r.priceTable(nil)
		if err != nil {
			return fmt.Errorf("pricing the models: %w", err)
		}
		s.Prices = prices
	}
	// The stages read their per-call output limits from the run's
	// configuration: it carries the merged limits from here on.
	cfg.Agent.MaxOutputTokens = config.RoleTokens{Coder: e.MaxOutputCoder, Reviewer: e.MaxOutputReviewer}
	r.spend, r.policy = s, e
	// The routing switch is applied here, once, after r.spend and before
	// anything reads the run's pins: the policy is where D1a delivers it, and
	// the pins it rewrites are what the pins renderer renders, the gateway
	// routes and prices, and the record names (Task 1b.2).
	//
	// It comes *after* the assignment above because `gatewayOn` — which
	// decides whether the switch has anything to act on at all — is read off
	// r.spend: applied before it, every run would look like a run with the
	// gateway off, and the switch would silently never take effect (the
	// critical finding of this PR's review). Nothing between here and the
	// loop reads a pin: the price table this function built is keyed by the
	// routes and the catalog rather than by the pins, and the plan — the one
	// decision that *is* read off a pin, through Agent.FirstLineOn — is
	// derived by the caller after this returns, so it sees the pins the
	// switch produced rather than the ones it replaced.
	if err := r.applyRouting(pp, found); err != nil {
		return err
	}
	r.rec.Policy = r.recordPolicy(nDef)
	return nil
}

// envCeiling is the ceiling the job environment deploys: the owner's mode, cap,
// token ceiling and allow-list as fugaro update wrote them. The mode "off" is
// unset — it is the absence of a mode, not a ceiling that says nothing may be
// accounted — and a mode that is not one of the three is refused by
// SpendFromEnv long before this.
//
// The cap comes through the run's rigor mode (D16, plan T6.1): the ceiling is
// the ceiling and a mode's per-run default is a fraction of it, so what the
// layer carries is the owner's cap scaled by the mode, and every later layer
// still only tightens it. A run whose mode contributes no default — Critical,
// which is today's behaviour, or one no resolution recorded — carries the
// owner's cap exactly.
func envCeiling(c Spend, mode rigor.Mode) policy.Layer {
	l := policy.Layer{MaxRunTokens: c.MaxRunTokens, AllowedModels: c.AllowedModels}
	if c.Mode != "" && c.Mode != policy.ModeOff {
		l.Mode = c.Mode
	}
	if c.Cap > 0 {
		l.PerRunUSD = config.RigorBudgetCap(mode, c.Cap).USD()
	}
	return l
}

// policyCeiling is the ceiling a published policy gives a run: the job
// environment's ceiling with the keys the node sets replaced, and nothing else
// touched. It returns the keys the node set as well, because the merge's first
// layer is named "ceiling" and result.json has to say which of the two each
// effective value actually was.
//
// The replacement is per key, not whole (H1, the security review of this PR).
// A node published for allow_repos alone used to replace the ceiling whole —
// mode, cap, token ceiling, allow-list, price table — so an installation that
// enforces a $50 cap with its kill switches on ran with no session, no cap and
// no switches at all, and nothing said so. The node is the owner's word about
// the keys it names; about the keys it does not, the deployed ceiling is the
// only thing that was ever said. The mode is the key this matters most for,
// and it is why the decoder refuses a budget block that names no mode: a node
// that meant to enforce cannot silently inherit the environment's "off".
//
// Spend.Cap carries the same ceiling in micro-dollars, kept exact rather than
// through a float round trip, so it is replaced only by the node's own cap and
// keeps the environment's when the node sets none.
//
// It is a method on the run because the price table it builds for the node's
// model_prices is the run's own: r.priceTable is config.PriceTable over the
// routes the job carries (FUGARO_MODEL_PROVIDERS) and the built-in catalog,
// with the node's overrides applied on top. Building it as
// pricing.Embedded().With(overrides) instead would drop every catalog row those
// routes contributed, so a run pinned to a catalog-priced provider model would
// be refused for "having no price" under a policy that priced some other model,
// and an override over a catalog row would come back with no token limits. The
// node replaces the owner's price OVERRIDES; the routes and the catalog
// underneath them are the job's, exactly as they are with no node at all.
func (r *run) policyCeiling(pp *projectpolicy.Policy, env policy.Layer, c Spend) (policy.Layer, Spend, map[string]bool, error) {
	ceiling, set := env, map[string]bool{}
	if m := pp.Budget.Mode; m != "" {
		ceiling.Mode, set[policy.KeyMode] = m, true
	}
	if v := pp.Budget.PerRunUSD; v > 0 {
		var err error
		if c.Cap, err = pricing.FromUSD(v); err != nil || c.Cap < 1 {
			return env, c, nil, fmt.Errorf("%s: budget.per_run_usd %v: not a usable cap: %v", projectpolicy.PathShared(), v, err)
		}
		// The node's cap is the owner's word about the ceiling, and it is
		// taken exactly as it stands: the run's rigor mode is the default a
		// run gets where no layer says otherwise (D16), and multiplying a
		// number the owner published by it would put a figure in result.json
		// the node never wrote — the very thing the source labels exist to
		// prevent (H1). The ceiling's own cap is the one the mode scales, and
		// it has no node to speak for it.
		ceiling.PerRunUSD, set[policy.KeyPerRunUSD] = v, true
	}
	if v := pp.Budget.MaxRunTokens; v > 0 {
		ceiling.MaxRunTokens, set[policy.KeyMaxRunTokens] = v, true
	}
	if pp.Budget.AllowedModels != nil {
		// A non-nil empty list is deny-all, which the decoder has already
		// refused: ValidBudget holds a policy's list to at least one model.
		ceiling.AllowedModels, set[policy.KeyAllowedModels] = pp.Budget.AllowedModels, true
	}
	// The review-level floor rides the ceiling layer, and this is the only
	// place it is read from (D722, G1): it is the owner's, it lives in the
	// owner-only node, and every other layer is written by the launcher. It is
	// not recorded in `set`, which names the keys result.json sources as the
	// node's: the floor is not a key of the merge's own vocabulary, and the
	// record has no field for it yet (L4.1). Merge unions it into the
	// effective policy (policy.Merge), so a later layer could add a level and
	// none can take one away.
	ceiling.ReviewFloor = pp.ReviewFloor
	if len(pp.Prices) > 0 {
		o, err := policyOverrides(pp)
		if err != nil {
			return env, c, nil, fmt.Errorf("%s: model_prices: %w", projectpolicy.PathShared(), err)
		}
		prices, err := r.priceTable(o)
		if err != nil {
			return env, c, nil, fmt.Errorf("%s: model_prices: %w", projectpolicy.PathShared(), err)
		}
		c.Prices = prices
	}
	return ceiling, c, set, nil
}

// policyOverrides is the owner's price overrides as the policy node carries
// them. The node spells model_prices exactly as the price catalogue spells them
// (projectpolicy.Price), so the catalogue's own parser reads them here too —
// the same one FUGARO_MODEL_PRICES goes through, whose overrides these replace:
// the policy is the owner's price OVERRIDES, whole.
//
// Prices.CatalogJSON is the spelling the parser reads: the node's own member
// names escape what an RTDB key may not hold (the "/" and "." of a model ID),
// and the decoded map is keyed by the real ID. The table the run prices with is
// the run's own (r.priceTable), because the routes the job carries and the
// built-in catalog are not the node's to replace — see policyCeiling.
func policyOverrides(pp *projectpolicy.Policy) (pricing.Overrides, error) {
	if len(pp.Prices) == 0 {
		return nil, nil
	}
	raw, err := pp.Prices.CatalogJSON()
	if err != nil {
		return nil, err
	}
	return pricing.ParseOverrides(string(raw))
}

// policySources renames the merge's first layer for the keys the project's
// policy node set: result.json's policy.sources then says
// "policy=<config/policy/shared>" for those keys, and the refusals and the
// report can send the reader to the node rather than to a job environment that
// no longer holds the value. The keys in fromPolicy are the node's; the rest
// of the first layer is the job environment's and keeps the name "ceiling"
// (H1). Nothing else changes: every later layer is still the same branch or
// file, and still only tightens.
func policySources(e *policy.Effective, fromPolicy map[string]bool) {
	for k := range fromPolicy {
		if e.Sources[k] == policy.SourceCeiling {
			e.Sources[k] = policy.SourcePolicy
		}
	}
	for i := range e.Ignored {
		if e.Ignored[i].Source == policy.SourceCeiling && fromPolicy[e.Ignored[i].Key] {
			e.Ignored[i].Source = policy.SourcePolicy
		}
	}
}

// FileLayer is what a fugaro.yaml says about policy: the run's own layer,
// and what `fugaro validate` checks, built in one place so the two agree.
func FileLayer(cfg *config.Config) policy.Layer {
	l := policy.Layer{MaxRunTokens: cfg.Agent.MaxRunTokens,
		MaxOutputCoder: cfg.Agent.MaxOutputTokens.Coder, MaxOutputReviewer: cfg.Agent.MaxOutputTokens.Reviewer,
		Governance: cfg.Governance.Policy()}
	if b := cfg.Budget; b != nil {
		l.Mode, l.PerRunUSD, l.PerDayUSD, l.AllowedModels = b.Mode, b.PerRunUSD, b.PerDayUSD, b.AllowedModels
	}
	return l
}

// policyRecord builds result.json's policy object from the merge: nil when
// no layer set anything that bears on money or models and nothing was
// ignored, so a repository without policy writes the same record as before.
// The per-call output limits are not by themselves a policy.
//
// A governance key still at the figure Fugaro ships is not a policy either,
// even though the ceiling carries it: the ceiling is built with the defaults
// (Governance.WithDefaults), so a key no layer wrote has the ceiling as its
// source for the same reason it has a value at all — because Fugaro put it
// there. Sourcing it as "ceiling" would name the owner of a guardrail he never
// chose (H1), and it would put all eighteen governance keys in every record,
// including the records of repositories that write no policy at all. A key a
// layer did write is kept: the value is then a decision somebody made.
func policyRecord(e policy.Effective, nDef int) *runstore.PolicyRecord {
	sources := maps.Clone(e.Sources)
	for k := range e.Governance.DefaultGovernanceKeys() {
		if sources[k] == policy.SourceCeiling {
			delete(sources, k)
		}
	}
	bears := false
	for k := range sources {
		if k != policy.KeyMaxOutputCoder && k != policy.KeyMaxOutputReviewer {
			bears = true
			break
		}
	}
	if !bears && len(e.Ignored) == 0 {
		return nil
	}
	rec := &runstore.PolicyRecord{Effective: runstore.PolicyEffective{
		PerRunUSD: e.PerRunUSD, PerDayUSD: e.PerDayUSD, Mode: e.Mode, MaxRunTokens: e.MaxRunTokens, AllowedModels: e.AllowedModels}}
	if e.MaxOutputCoder > 0 || e.MaxOutputReviewer > 0 {
		rec.Effective.MaxOutputTokens = &runstore.PolicyOutput{Coder: e.MaxOutputCoder, Reviewer: e.MaxOutputReviewer}
	}
	if len(sources) > 0 {
		rec.Sources = sources
	}
	for i, ig := range e.Ignored {
		from := policy.SourceBranch
		if i < nDef {
			from = policy.SourceDefaultBranch
		}
		rec.Ignored = append(rec.Ignored, runstore.PolicyIgnored{Key: ig.Key, Value: ig.Value, Effective: ig.Effective, Source: ig.Source, From: from})
	}
	return rec
}

// recordPolicy builds the record and, once per run, logs the policy and
// warns once per key about each value that was dropped.
func (r *run) recordPolicy(nDef int) *runstore.PolicyRecord {
	rec := policyRecord(r.policy, nDef)
	if rec == nil {
		return nil
	}
	e := rec.Effective
	allowed := "any"
	if e.AllowedModels != nil {
		allowed = strings.Join(e.AllowedModels, ",")
		if allowed == "" {
			allowed = "none"
		}
	}
	args := []any{"per_run_usd", e.PerRunUSD}
	if e.PerDayUSD > 0 {
		args = append(args, "per_day_usd", e.PerDayUSD)
	}
	if e.Mode != "" {
		args = append(args, "mode", e.Mode)
	}
	args = append(args, "max_run_tokens", e.MaxRunTokens, "allowed_models", allowed, "sources", sourcesText(rec.Sources))
	r.d.Log.Info("policy", args...)
	warned := map[string]bool{}
	for _, ig := range rec.Ignored {
		if warned[ig.Key] {
			continue
		}
		warned[ig.Key] = true
		r.d.Log.Warn("policy: a looser value in fugaro.yaml was ignored", "key", PolicyKeyPath(ig.Key),
			"value", ig.Value, "effective", ig.Effective, "from", ig.From, "limit_from", ig.Source)
	}
	return rec
}

func sourcesText(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, " ")
}

// PolicyKeyPath is where a policy key lives in fugaro.yaml: the governance
// keys under a governance: block of their own — a tier's own keys under
// governance.tiers.<tier>, the block that carries them (policy.TierKey) — the
// agent's output limits under agent:, and everything else — the budget's cost
// and model policy — under budget:.
func PolicyKeyPath(key string) string {
	switch key {
	case policy.KeyMaxRunTokens, policy.KeyMaxOutputCoder, policy.KeyMaxOutputReviewer:
		return "agent." + key
	case policy.KeyReviewRatio, policy.KeyReviewFloorCalls, policy.KeyMaxSubagents,
		policy.KeyStageBudgetImplement, policy.KeyStageBudgetReview, policy.KeyStageBudgetFix,
		policy.KeyStageTokensImplement, policy.KeyStageTokensReview, policy.KeyStageTokensFix,
		policy.KeyStageTimeoutImplement, policy.KeyStageTimeoutReview, policy.KeyStageTimeoutFix:
		return "governance." + key
	}
	if strings.HasPrefix(key, "tiers.") {
		return "governance." + key
	}
	return "budget." + key
}
