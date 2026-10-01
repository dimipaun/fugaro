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
		def = policy.Layer{Mode: p.Mode, PerRunUSD: p.PerRunUSD, MaxRunTokens: p.MaxRunTokens, AllowedModels: p.AllowedModels,
			MaxOutputCoder: p.MaxOutputTokens.Coder, MaxOutputReviewer: p.MaxOutputTokens.Reviewer}
	}

	branch := policy.Layer{MaxRunTokens: cfg.Agent.MaxRunTokens,
		MaxOutputCoder: cfg.Agent.MaxOutputTokens.Coder, MaxOutputReviewer: cfg.Agent.MaxOutputTokens.Reviewer}
	if b := cfg.Budget; b != nil {
		branch.Mode, branch.PerRunUSD, branch.AllowedModels = b.Mode, b.PerRunUSD, b.AllowedModels
	}

	e := policy.Merge(ceiling, def, branch)
	// Merge lists the default branch's ignored values before the branch's,
	// so the number the first two layers drop says who asked for each.
	nDef := len(policy.Merge(ceiling, def).Ignored)
	s := Spend{Mode: e.Mode, MaxRunTokens: e.MaxRunTokens, AllowedModels: e.AllowedModels, Prices: c.Prices}
	if s.Mode == "" {
		s.Mode = policy.ModeOff
	}
	if e.PerRunUSD > 0 {
		if e.Sources[policy.KeyPerRunUSD] == policy.SourceCeiling {
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
		PerRunUSD: e.PerRunUSD, Mode: e.Mode, MaxRunTokens: e.MaxRunTokens, AllowedModels: e.AllowedModels}}
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
	r.d.Log.Info("policy", "per_run_usd", e.PerRunUSD, "mode", e.Mode, "max_run_tokens", e.MaxRunTokens,
		"allowed_models", allowed, "sources", sourcesText(rec.Sources))
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
