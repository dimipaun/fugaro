package runner

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
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
	if d.data, err = r.repo.ShowFile(ctx, "origin/"+def, "fugaro.yaml"); err != nil {
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
		def = policy.Layer{Mode: p.Mode, PerRunUSD: p.PerRunUSD, MaxRunTokens: p.MaxRunTokens, AllowedModels: p.AllowedModels}
	}

	branch := policy.Layer{MaxRunTokens: cfg.Agent.MaxRunTokens}
	if b := cfg.Budget; b != nil {
		branch.Mode, branch.PerRunUSD, branch.AllowedModels = b.Mode, b.PerRunUSD, b.AllowedModels
	}

	e := policy.Merge(ceiling, def, branch)
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
		// The ceiling was off, so the owner set no price overrides; a
		// committed observe or enforce charges the built-in table.
		s.Prices = pricing.Embedded()
	}
	r.spend, r.policy = s, e
	return nil
}
