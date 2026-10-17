package runner

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
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
		if !r.d.Local {
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
// can have been computed with. "fake" is not among them (R2-9), and that costs
// the admission check nothing: a fake provider is a local run — no Cloud Run
// execution, so the binding does not apply to it — and `--provider fake` never
// reaches Deps.ProviderKind as "fake" anyway (internal/cli's providerOptions
// maps it to the empty kind, "the provider fugaro.yaml names").
var jobProviderKinds = []string{"github", "bitbucket"}

// ProviderForSlug is providerForSlug for the launcher's advisory twin
// (internal/cli's pre-launch check, plan task 1.5 step 4): the launch refuses
// a repository the run itself would refuse, and it must derive the provider
// kind exactly as the run does — from the slug and the repository, never from
// anything a launcher can set. One implementation, so the two cannot disagree
// about which repository a job is for.
func ProviderForSlug(slug, repo string) (kind string, ok bool) {
	return providerForSlug(slug, repo)
}

// ProviderKinds is the kinds ProviderForSlug derives from, for a refusal that
// names them (the same list the run's own messages name) and for the launcher
// side, whose slug is computed with internal/config's Providers: the two must
// be one list, and internal/cli's TestProviderKindsAgreeWithTheConfigsList
// holds them to it.
func ProviderKinds() []string {
	return append([]string(nil), jobProviderKinds...)
}

// providerForSlug is the provider kind whose Slug of repo is slug. One job
// serves one provider, and the slug is a hash of (kind, canonical repo)
// (internal/task/task.go:187), so exactly one kind can match. It is derived,
// never read from an env var a launcher can override (12a item 2). A slug no
// kind produces, and a slug two kinds produce, both return ok = false: the
// run is not admitted on a guess.
func providerForSlug(slug, repo string) (kind string, ok bool) {
	var found []string
	for _, k := range jobProviderKinds {
		if s, err := task.Slug(k, repo); err == nil && s == slug {
			found = append(found, k)
		}
	}
	// Exactly one, or nothing: a slug two kinds produce (a hash collision, or
	// a kind added without the "\x00" domain separator task.Slug uses) is a
	// programming error made visible, and returning no kind at all means a
	// caller that forgot ok cannot act on the ambiguity either.
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

// checkJobProfile halts a run whose workflow selected a job profile the
// published policy does not define (plan task 3.1 step 1: a workflow naming an
// undefined profile is refused at publish time *and* at run start, fail
// closed). The alternative — falling back to the default or to another profile
// — would run a repository's work on a job whose shape, secrets and admission
// its owner did not choose for it.
//
// Only a selection other than the default is checked. A workflow that names
// none runs on the default (standard), and a policy that carries a profiles
// block must define it (projectpolicy.validProfiles), so the default can only
// be defined; a policy with no profiles block at all defines nothing, and a run
// has no job layout to apply one with yet (task 3.2), so nothing is refused for
// the default. A workflow that writes `job_profile: standard` says exactly what
// omitting the key says (docs/project-layer.md), so it is the default too and
// runs wherever the omission runs. An installation that has published no
// policy is the phase-2 installation: naming any other job profile there is a
// claim about a document that does not exist, and it is refused rather than
// ignored.
//
// The name is the whole check this release can make: the profile's resources,
// secrets and admission list describe the job the run is launched as, and the
// job this run is in does not carry the selection yet — that is task 3.2's job
// layout. So this is the plan's fail-closed half, and the enforcement half
// arrives with the layout.
//
// An older binary (0.8.0, which does not know the key) refuses a fugaro.yaml
// that sets job_profile: the key is a config field, and the strict decoder
// refuses a name it does not know. The skew is therefore a refusal at
// validation, never a run on a profile nobody selected.
func (r *run) checkJobProfile(ctx context.Context, workflow string, wf config.Workflow) error {
	if wf.JobProfile == "" || wf.JobProfile == projectpolicy.DefaultProfileName {
		return nil
	}
	p, found, err := r.projectPolicy(ctx)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("workflow %s selects the job profile %q, and this project has published no policy: a job profile is a profile of the published policy, which names the job the run is launched as. Nothing published defines it, so the run stops rather than running on a job nobody selected",
			workflow, wf.JobProfile)
	}
	if _, err := p.ProfileFor(wf.JobProfile); err != nil {
		return fmt.Errorf("workflow %s: %w. The run stops rather than falling back to another profile", workflow, err)
	}
	return nil
}

// remoteOfAnotherKind names the provider kind the remote this run would clone
// from belongs to, when that is not the kind its slug derives. "" is no
// disagreement, and it is the answer for most remotes: a local path, a
// self-hosted installation and every test remote name no host this Fugaro
// knows (gitprov.KindForURL), and the ones that do name the derived kind.
func remoteOfAnotherKind(derived, remote string) string {
	if derived == "" {
		return ""
	}
	if kind := gitprov.KindForURL(remote); kind != "" && kind != derived {
		return kind
	}
	return ""
}

// remoteOfAnotherRepo names the repository a remote on the derived kind's own
// host actually is, when that is not the run's repository. "" is no
// disagreement, and it is the answer for most remotes, including every remote
// remoteOfAnotherKind already refuses and every remote that names no
// repository path at all.
//
// The kind alone is not enough (a review finding): a provider's host carries
// every repository of that provider, so "https://github.com/evil/other.git" is
// github's and is still not this run's. internal/infra's repoURL refuses
// exactly that when it builds the job ("it must be this repository, not only
// this provider"); a launcher that edits the job's environment after launch
// would otherwise hand the run another repository while the run keeps the slug
// of its own — and the slug is what keys its locks, its cache and its budget
// session.
func remoteOfAnotherRepo(derived, repo, remote string) string {
	if derived == "" || gitprov.KindForURL(remote) != derived {
		return ""
	}
	path, ok := remoteRepoPath(remote)
	if !ok {
		return ""
	}
	want, err := task.CanonicalRepo(repo)
	if err != nil {
		return "" // no canonical form to compare against; the slug check refuses it
	}
	got, err := task.CanonicalRepo(path)
	if err != nil || got == want {
		// Not a repository path at all ("/tree/main" of a web URL, say):
		// whatever it is, it is not one the run's slug could name, and the
		// kind's own host is where the clone would go, so the run is admitted
		// as it always was.
		return ""
	}
	return got
}

// remoteRepoPath is the repository path a remote names on its host: the path
// of a URL ("https://github.com/acme/app.git"), or what follows the colon of
// an scp-like remote ("git@github.com:acme/app.git"). ok is false for a remote
// that carries no such path, and for one that is a local path.
func remoteRepoPath(remote string) (string, bool) {
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		path := strings.Trim(u.Path, "/")
		return path, path != ""
	}
	if at, rest, ok := strings.Cut(remote, "@"); ok && !strings.Contains(at, "/") {
		if _, path, ok := strings.Cut(rest, ":"); ok {
			path = strings.Trim(path, "/")
			return path, path != ""
		}
	}
	return "", false
}

// remoteDisplay is the remote as a refusal may quote it: an scp-like remote as
// its host alone, anything else as the URL without its userinfo. Deps.Remote is
// a launcher's to set and a remote URL may carry a token in its userinfo; the
// run never registers that one as a secret, so it is dropped here rather than
// redacted later.
func remoteDisplay(remote string) string {
	if at, rest, ok := strings.Cut(remote, "@"); ok && !strings.Contains(at, "/") {
		if host, _, ok := strings.Cut(rest, ":"); ok {
			return host
		}
	}
	u, err := url.Parse(remote)
	if err != nil {
		return "the remote"
	}
	u.User = nil
	return u.String()
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
// admitted writes no lease. Its refusals are the run's own: the slug it was
// given must be the slug of the repository task.json names; the provider kind
// and the remote — the two environment values a launcher can set that would
// say otherwise — may disagree with it only by refusing; and, when the project
// has published a policy, that repository must be on the policy's allow-list.
// With no policy there is no allow-list, and the absent case is never read as
// "empty means deny": an installation that has not published a policy admits
// exactly what it always did.
func (r *run) admissionCheck(ctx context.Context) error {
	if r.d.Execution == "" {
		// No Cloud Run execution: no job identity to bind to, so no binding
		// (R2-9). fugaro exec --provider fake, a local run and the end-to-end
		// tests keep the path they have always had.
		return nil
	}
	p, found, err := r.projectPolicy(ctx)
	if err != nil {
		return err
	}
	slug, repo := r.d.Store.Slug(), r.spec.Repo
	kind, ok := providerForSlug(slug, repo)
	if !ok {
		// Zero matches: FUGARO_RUN and the task disagree. Two matches cannot
		// happen through a launcher (task.Slug separates the kinds), and are
		// refused the same way rather than resolved to one.
		return fmt.Errorf("the run was launched for the slug %s, and that is not the slug of the repository this run's task names (%s) under any provider kind (%s): the slug and the task must name one repository. Nothing bound the two together before this check — a per-repository job's bucket condition was what stood in for it — so the run stops before any git credential is requested or anything is cloned",
			slug, repo, strings.Join(jobProviderKinds, ", "))
	}
	if envKind := r.d.ProviderKind; envKind != "" && envKind != kind {
		// The flag (or FUGARO_GIT_PROVIDER) is an environment value a launcher
		// can set, so it may only tighten the admission and never change which
		// repository this run is for (12a item 2). Naming another kind is a
		// claim that this run is for another repository's job — there is no
		// kind outside jobProviderKinds to except: the file-backed test
		// provider is not one (internal/cli's providerOptions maps --provider
		// fake to an empty kind), and a local run has no Cloud Run execution,
		// so it never reaches this check at all — and it stops here.
		return fmt.Errorf("the git provider in the job's environment is %s (the --provider flag, or %s), and the run's slug %s is the slug of %s under %s: the flag may only tighten the run's admission, never change which repository it is for, so the run stops before any git credential is requested",
			envKind, GitProviderEnv, slug, repo, kind)
	}
	// The remote is where the clone comes from, and it is the job's own
	// environment (Deps.Remote, RemoteEnv) or the origin of a checkout already
	// in the work directory — a launcher's to set, both of them. The launcher
	// refuses a repository URL that is not this repository before it deploys
	// anything (internal/infra's repoURL, and the build's own CheckRepoURL), so
	// a run meets these when the job's environment was edited after launch. The
	// two questions are the kind's and the repository's: another provider's
	// host is another repository — the same owner and name is the case worth
	// naming — and so is another repository on the same provider's host, where
	// the kind alone would agree. The run's slug keys its locks, its cache and
	// its budget session, so it names neither.
	remote := r.originURL(ctx)
	if other := remoteOfAnotherKind(kind, remote); other != "" {
		return fmt.Errorf("the run would clone from %s, which is %s's, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for, so the run stops before any git credential is requested",
			remoteDisplay(remote), other, slug, repo, kind, RemoteEnv)
	}
	if other := remoteOfAnotherRepo(kind, repo, remote); other != "" {
		// internal/infra's repoURL refuses this at deploy time — "it must be
		// this repository, not only this provider" — and the run refuses it
		// here for the same reason: it clones, caches, locks and accounts under
		// its slug, and another repository's URL under that slug is another
		// repository's work.
		return fmt.Errorf("the run would clone from %s, which is the repository %s on %s's host, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, and it must be this repository, not only this provider, so the run stops before any git credential is requested",
			remoteDisplay(remote), other, kind, slug, repo, kind, RemoteEnv)
	}
	if want, err := task.Slug(kind, repo); err != nil || want != slug {
		// Implied by the derivation; re-checked so the two values the run
		// actually uses (the slug under which it reads and writes the bucket,
		// and the provider kind it opens) are the ones that were compared.
		got := want
		if err != nil {
			got = err.Error()
		}
		return fmt.Errorf("the run's slug %s is not the slug task.Slug(%s, %s) computes (%s): the run stops before any git credential is requested", slug, kind, repo, got)
	}
	if !found {
		// No policy published here: the binding is the whole check (Task 1.3
		// step 1's absent case). Reading this as an empty allow-list would
		// refuse every run of every installation that has published nothing.
		return nil
	}
	canonical, err := projectpolicy.Canonical(kind, repo)
	if err != nil {
		return fmt.Errorf("the repository this run was launched for cannot be named in the project's allow-list: %w", err)
	}
	if !projectpolicy.AdmitRepo(p.AllowRepos, canonical) {
		// The refusal itself is projectpolicy's, so the launcher's advisory
		// twin (internal/cli) refuses a launch with the same message; the
		// sentence about what stops is this check's own.
		return fmt.Errorf("%s. The run stops before any lease exists", projectpolicy.NotAdmitted(p.AllowRepos, canonical))
	}
	return nil
}
