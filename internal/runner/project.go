package runner

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
)

// checkProject refuses a job whose repository belongs to another Fugaro
// project than the job does (design §2.5). It runs before the lock, so a
// refusal changes nothing remote. cfg is the configuration the run uses:
// a first run's is the ref's, a follow-up's the base branch's.
//
// It reads project: from the base branch's fugaro.yaml leniently (a base
// that doesn't parse still says which project it belongs to), and, for a
// first run at another ref, from the ref's as well.
func (r *run) checkProject(ctx context.Context, cfg *config.Config) error {
	if r.d.Project == "" {
		if r.d.RequireProject {
			return fmt.Errorf("job environment lacks FUGARO_PROJECT; run fugaro init --repo from the repository's checkout")
		}
		r.d.Log.Info("FUGARO_PROJECT is not set: not checking which project fugaro.yaml belongs to")
		return nil
	}
	base := cfg.Git.BaseBranch
	data, err := r.repo.ShowFile(ctx, "origin/"+base, "fugaro.yaml")
	if err != nil {
		return fmt.Errorf("reading fugaro.yaml at origin/%s: %w", base, err)
	}
	if err := r.projectOfFile(base, data); err != nil {
		return err
	}
	if r.follow == nil && r.spec.Ref != base {
		// The ref's own file, already parsed into cfg.
		return r.sameProject(r.spec.Ref, cfg.Project)
	}
	return nil
}

// projectOfFile checks the project: of fugaro.yaml's bytes on branch.
func (r *run) projectOfFile(branch string, data []byte) error {
	got, err := config.ProjectOf(data)
	if err != nil {
		return fmt.Errorf("reading project: from fugaro.yaml on %s: %w", branch, err)
	}
	return r.sameProject(branch, got)
}

func (r *run) sameProject(branch, got string) error {
	switch got {
	case r.d.Project:
		return nil
	case "":
		return fmt.Errorf("project mismatch: fugaro.yaml on %s names no project; this job belongs to project %s", branch, r.d.Project)
	}
	return fmt.Errorf("project mismatch: fugaro.yaml on %s names project %s; this job belongs to project %s", branch, got, r.d.Project)
}
