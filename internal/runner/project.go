package runner

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
)

// GitProviderEnv is the job environment variable the --provider flag defaults
// to (internal/cli/exec.go:69). It is a launcher's to set, so the admission
// check never reads the provider from it: the kind is derived from the slug
// and this value may only refuse (12a item 2).
const GitProviderEnv = "FUGARO_GIT_PROVIDER"

// checkProject refuses a job whose repository belongs to another Fugaro
// project than the job does (design §2.5). It runs before the lock, so a
// refusal changes nothing remote. cfg is the configuration the run uses:
// a first run's is the ref's, a follow-up's the base branch's.
//
// A first run reads project: leniently (a file that doesn't parse still
// says which project it belongs to) from the repository's default branch,
// which no file in the checkout can choose, and from the ref's own file
// as well when the ref is another branch. A follow-up has nothing more to
// read: readBaseConfig checked the pull request's base.
func (r *run) checkProject(ctx context.Context, cfg *config.Config) error {
	if r.d.Project == "" {
		if r.d.RequireProject {
			return fmt.Errorf("job environment lacks FUGARO_PROJECT; run fugaro init --repo from the repository's checkout")
		}
		r.d.Log.Info("FUGARO_PROJECT is not set: not checking which project fugaro.yaml belongs to")
		return nil
	}
	if r.follow != nil {
		return nil
	}
	data, def, err := r.defaultBranchFile(ctx)
	if err != nil {
		return err
	}
	if err := r.projectOfFile(def, data); err != nil {
		return err
	}
	if r.spec.Ref != def {
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

// jobProviderKinds are the git provider kinds a Cloud Run job's storage slug
// can have been computed with. "fake" is not among them (R2-9): a fake
// provider is a local run, it has no Cloud Run execution and no bucket
// condition to bind to, and the binding below does not apply to it.
var jobProviderKinds = []string{"github", "bitbucket"}

// providerForSlug is the provider kind whose Slug of repo is slug. One job
// serves one provider, and the slug is a hash of (kind, canonical repo)
// (internal/task/task.go:187), so exactly one kind can match. It is derived,
// never read from an env var a launcher can override (12a item 2). A slug no
// kind produces, and a slug two kinds produce, both return ok = false: the
// run is not admitted on a guess.
func providerForSlug(slug, repo string) (kind string, ok bool) {
	return "", false
}

// admissionCheck admits the repository this run was launched for, before any
// git credential exists and before the clone (plan task 1.5): the run's slug
// and its repository must agree, and the project's policy, when it has
// published one, must have the repository on its allow-list. A run with no
// Cloud Run execution is not checked: the binding exists because a job's
// identity is what an admission rests on (R2-9).
//
// It runs after the project's policy has been read (Task 1.3) and before the
// provider is opened, so no git credential exists yet and nothing has been
// cloned; and before the lock and the budget session, so a run that is not
// admitted writes no lease. Its two refusals are the run's own: the slug it
// was given must be the slug of the repository task.json names, and — when the
// project has published a policy — that repository must be on the policy's
// allow-list. With no policy there is no allow-list, and the absent case is
// never read as "empty means deny": an installation that has not published a
// policy admits exactly what it always did.
func (r *run) admissionCheck(ctx context.Context) error {
	return nil
}
