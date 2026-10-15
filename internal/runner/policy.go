package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
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
	// The ceiling sets no output limits (the project config has none).
	ceiling := policy.Layer{MaxRunTokens: c.MaxRunTokens, AllowedModels: c.AllowedModels}
	if c.Mode != "" && c.Mode != policy.ModeOff {
		ceiling.Mode = c.Mode
	}
	if c.Cap > 0 {
		ceiling.PerRunUSD = c.Cap.USD()
	}
	// The project's own policy, when it has published one, *is* the ceiling:
	// the node replaces the job environment's copy of it whole, because the
	// copy is what the owner's config said when the job was deployed and the
	// node is what he says now (design §5, plan task 1.3 step 3, 12a item 13).
	// The environment stays the ceiling for an installation that has published
	// nothing, which is the behaviour of every install today.
	//
	// A read that fails is returned as it is: the run does not start rather
	// than start on a ceiling the policy did not set.
	pp, found, err := r.projectPolicy(ctx)
	if err != nil {
		return err
	}
	if found {
		if ceiling, c, err = policyCeiling(pp, c); err != nil {
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
			MaxOutputCoder: p.MaxOutputTokens.Coder, MaxOutputReviewer: p.MaxOutputTokens.Reviewer}
	}

	e := policy.Merge(ceiling, def, FileLayer(cfg))
	if found {
		// The merge names the first layer "ceiling": with a published policy
		// that layer is the node, not the job environment, and a run's
		// result.json has to say which of the two its ceiling was.
		policySources(&e)
	}
	// Merge lists the default branch's ignored values before the branch's,
	// so the number the first two layers drop says who asked for each.
	nDef := len(policy.Merge(ceiling, def).Ignored)
	s := Spend{Mode: e.Mode, MaxRunTokens: e.MaxRunTokens, AllowedModels: e.AllowedModels, Prices: c.Prices}
	if s.Mode == "" {
		s.Mode = policy.ModeOff
	}
	if e.PerRunUSD > 0 {
		if src := e.Sources[policy.KeyPerRunUSD]; src == policy.SourceCeiling || src == policy.SourcePolicy {
			s.Cap = c.Cap // exactly the owner's, not a float round trip
		} else {
			var err error
			if s.Cap, err = pricing.FromUSD(e.PerRunUSD); err != nil || s.Cap < 1 {
				return fmt.Errorf("budget.per_run_usd %v: not a usable cap: %v", e.PerRunUSD, err)
			}
		}
	}
	if s.On() && s.Prices == nil {
		// The ceiling was off with no price overrides (they would have
		// been loaded); a committed observe or enforce charges the
		// built-in table.
		s.Prices = pricing.Embedded()
	}
	// The stages read their per-call output limits from the run's
	// configuration: it carries the merged limits from here on.
	cfg.Agent.MaxOutputTokens = config.RoleTokens{Coder: e.MaxOutputCoder, Reviewer: e.MaxOutputReviewer}
	r.spend, r.policy = s, e
	r.rec.Policy = r.recordPolicy(nDef)
	return nil
}

// policyCeiling is the ceiling a published policy gives a run, and the
// environment's own values it replaces: the budget block whole (mode, per-run
// ceiling, token ceiling, allow-list), the prices the run is metered by, and
// the cap Spend.Cap carries (which is the same ceiling in micro-dollars, kept
// exact rather than through a float round trip).
//
// A key the policy does not set is a key the ceiling does not set: the node is
// the owner's ceiling now, and its silence is not the environment's word. What
// that means is the run's own, unchanged: mode enforce with no cap halts
// (no_cap), an allow-list the policy does not set constrains nothing. The name
// of the node goes in the message because the fix for a cap that is wrong is
// publishing a new policy, not another init --repo.
func policyCeiling(pp *projectpolicy.Policy, c Spend) (policy.Layer, Spend, error) {
	ceiling := policy.Layer{
		Mode:          pp.Budget.Mode,
		PerRunUSD:     pp.Budget.PerRunUSD,
		MaxRunTokens:  pp.Budget.MaxRunTokens,
		AllowedModels: pp.Budget.AllowedModels,
	}
	prices, err := policyPrices(pp)
	if err != nil {
		return policy.Layer{}, c, fmt.Errorf("%s: model_prices: %w", projectpolicy.PathShared(), err)
	}
	c.Prices, c.Cap = prices, 0
	if pp.Budget.PerRunUSD > 0 {
		if c.Cap, err = pricing.FromUSD(pp.Budget.PerRunUSD); err != nil || c.Cap < 1 {
			return policy.Layer{}, c, fmt.Errorf("%s: budget.per_run_usd %v: not a usable cap: %v", projectpolicy.PathShared(), pp.Budget.PerRunUSD, err)
		}
	}
	return ceiling, c, nil
}

// policyPrices is the owner's price overrides as the policy node carries them,
// on the built-in table. The node spells model_prices exactly as the price
// catalogue spells them (projectpolicy.Price), so the catalogue's own parser
// reads them here too — the same one FUGARO_MODEL_PRICES goes through, which
// this replaces: the policy is the owner's price list, whole.
func policyPrices(pp *projectpolicy.Policy) (*pricing.Table, error) {
	if len(pp.Prices) == 0 {
		return pricing.Embedded(), nil
	}
	raw, err := json.Marshal(pp.Prices)
	if err != nil {
		return nil, err
	}
	o, err := pricing.ParseOverrides(string(raw))
	if err != nil {
		return nil, err
	}
	return pricing.Embedded().With(o)
}

// policySources renames the merge's first layer when the ceiling came from the
// project's policy node: result.json's policy.sources then says
// "policy=<config/policy/shared>" for the keys the node set, and the refusals
// and the report can send the reader to the node rather than to a job
// environment that no longer holds the value.
func policySources(e *policy.Effective) {
	for k, src := range e.Sources {
		if src == policy.SourceCeiling {
			e.Sources[k] = policy.SourcePolicy
		}
	}
	for i := range e.Ignored {
		if e.Ignored[i].Source == policy.SourceCeiling {
			e.Ignored[i].Source = policy.SourcePolicy
		}
	}
}

// FileLayer is what a fugaro.yaml says about policy: the run's own layer,
// and what `fugaro validate` checks, built in one place so the two agree.
func FileLayer(cfg *config.Config) policy.Layer {
	l := policy.Layer{MaxRunTokens: cfg.Agent.MaxRunTokens,
		MaxOutputCoder: cfg.Agent.MaxOutputTokens.Coder, MaxOutputReviewer: cfg.Agent.MaxOutputTokens.Reviewer}
	if b := cfg.Budget; b != nil {
		l.Mode, l.PerRunUSD, l.PerDayUSD, l.AllowedModels = b.Mode, b.PerRunUSD, b.PerDayUSD, b.AllowedModels
	}
	return l
}

// policyRecord builds result.json's policy object from the merge: nil when
// no layer set anything that bears on money or models and nothing was
// ignored, so a repository without policy writes the same record as before.
// The per-call output limits are not by themselves a policy.
func policyRecord(e policy.Effective, nDef int) *runstore.PolicyRecord {
	sources := map[string]string{}
	for k, v := range e.Sources {
		sources[k] = v
	}
	delete(sources, policy.KeyMaxOutputCoder)
	delete(sources, policy.KeyMaxOutputReviewer)
	if len(sources) == 0 && len(e.Ignored) == 0 {
		return nil
	}
	rec := &runstore.PolicyRecord{Effective: runstore.PolicyEffective{
		PerRunUSD: e.PerRunUSD, PerDayUSD: e.PerDayUSD, Mode: e.Mode, MaxRunTokens: e.MaxRunTokens, AllowedModels: e.AllowedModels}}
	if e.MaxOutputCoder > 0 || e.MaxOutputReviewer > 0 {
		rec.Effective.MaxOutputTokens = &runstore.PolicyOutput{Coder: e.MaxOutputCoder, Reviewer: e.MaxOutputReviewer}
	}
	if len(e.Sources) > 0 {
		rec.Sources = maps.Clone(e.Sources)
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

// PolicyKeyPath is where a policy key lives in fugaro.yaml.
func PolicyKeyPath(key string) string {
	switch key {
	case policy.KeyMaxRunTokens, policy.KeyMaxOutputCoder, policy.KeyMaxOutputReviewer:
		return "agent." + key
	}
	return "budget." + key
}
