package runner

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/repourl"
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

// remoteOffTheKindsHost is the host (with its port, when it names one) of a
// remote that names a host other than exactly the derived kind's own
// (repourl.ProviderHost: github.com, bitbucket.org), and "" for a remote on
// that host with no port, or one that names no host at all. A remote with no
// host is a local path or a file URL: the run sends it no credential
// (gitops.CredentialURL is empty for it), and the end-to-end and runner
// fixtures clone from one.
//
// The comparison is on the hostname as the URL spells it, case-insensitively
// (DNS is), with nothing else: "github.com." and "www.github.com" are other
// names, however they resolve, and an explicit port is refused too, since the
// deployer never writes one (repourl.CloneURL refuses it) and a credential
// helper keyed on the bare host would not match it anyway. A derived kind
// with no host of its own is refused for any host (fail closed).
func remoteOffTheKindsHost(derived, remote string) string {
	host, port := remoteHost(remote)
	if host == "" {
		return ""
	}
	if want := repourl.ProviderHost(derived); want != "" && strings.EqualFold(host, want) && port == "" {
		return ""
	}
	if port != "" {
		return host + ":" + port
	}
	return host
}

// remoteHost is the host and port a remote names: a URL's, or the host of an
// scp-like remote ("git@github.com:acme/app.git", which has no port). Both are
// "" for a remote that names no host, such as a local path.
func remoteHost(remote string) (host, port string) {
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		return u.Hostname(), u.Port()
	}
	if at, rest, ok := strings.Cut(remote, "@"); ok && !strings.Contains(at, "/") {
		if h, _, ok := strings.Cut(rest, ":"); ok {
			return h, ""
		}
	}
	return "", ""
}

// remoteOfAnotherRepo names what a remote on the derived kind's own host
// points at, when that is not the run's repository: the canonical repository
// the path names, or the path itself when it is not a canonical repository
// name. "" is no disagreement, and it is the answer for most remotes,
// including every remote remoteOfAnotherKind already refuses and every remote
// that names no repository path at all.
//
// The kind alone is not enough (a review finding): a provider's host carries
// every repository of that provider, so "https://github.com/evil/other.git" is
// github's and is still not this run's. internal/infra's repoURL refuses
// exactly that when it builds the job ("it must be this repository, not only
// this provider"); a launcher that edits the job's environment after launch
// would otherwise hand the run another repository while the run keeps the slug
// of its own — and the slug is what keys its locks, its cache and its budget
// session.
//
// A path on that host that CanonicalRepo refuses is a refusal too, and that is
// deliberate (a review finding): "acme/app/../other" and "acme//other" are not
// repository names, but what the host resolves them to is, and it is another
// one — the run would clone, cache, lock and account under its own slug while
// doing that repository's work, which is exactly what this function exists to
// stop. A path this Fugaro cannot read is a path whose repository the run
// cannot know, so it is refused rather than admitted ("fail closed"); the
// returned value is then the raw path, which is what the refusal quotes.
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
	if err != nil {
		return path
	}
	if got == want {
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
// git credential exists and before the clone (plan tasks 1.5 and 3.3): the run
// is placed in the job it is in, the run's slug and its repository must agree
// under that job's provider, and the policy must admit the repository — for the
// project, and on a project job for the profile the job's own name pins. A
// local invocation is not checked: the binding exists because a job's identity
// is what an admission rests on (R2-9). A process with no Cloud Run execution
// that is not local is in the cloud, and it is refused (jobName).
//
// It runs after the project's policy has been read (Task 1.3) and before the
// provider is opened, so no git credential exists yet and nothing has been
// cloned; and before the lock and the budget session, so a run that is not
// admitted writes no lease. Its refusals are the run's own: the job it is in
// must be one the policy computes or its own per-repository job; the slug it
// was given must be the slug of the repository task.json names under that job's
// provider; the provider kind in the environment — a launcher's to set — may
// disagree with it only by refusing; and, when the project has published a
// policy, that repository must be on the policy's allow-list, and on a project
// job on the profile's own. With no policy there is no allow-list, and the
// absent case is never read as "empty means deny": an installation that has not
// published a policy admits exactly what it always did.
//
// The order is the plan's and it is the security property: the job identity
// first, because everything below is keyed by what it says; then the slug; then
// the environment; then the admission — all of it before openProvider and
// before the budget session takes a lease.
func (r *run) admissionCheck(ctx context.Context) error {
	if r.d.Local && r.d.Execution == "" {
		// A local invocation (`fugaro exec --task-file`, the file-backed fake
		// provider, the end-to-end tests) has no job identity to bind to, so
		// no binding (R2-9). The skip needs both halves (review C2, and main's
		// #453 for the phase-1 shape): Local's zero value means the cloud
		// (runner.go), and a process no backend claims — a platform this
		// build does not recognise, or a job whose environment was edited —
		// carries no execution name either. Reading the missing name alone as
		// "local" exempted that process from everything below while the git
		// credential stayed scoped to whatever remote its environment named;
		// it now reaches jobIdentity, which halts it (jobName).
		return nil
	}
	p, found, err := r.projectPolicy(ctx)
	if err != nil {
		return err
	}
	// Which job this run is in, and with it the provider (and, on a project
	// job, the profile and the account) it may act as. jobIdentity halts for a
	// name the policy neither computes nor recognises as this run's own
	// per-repository job, so a run that gets past this line has a job it can
	// place (projectjob.go).
	if _, _, _, err := r.jobIdentity(ctx); err != nil {
		return err
	}
	job, project := r.projectJob()
	slug, repo := r.d.Store.Slug(), r.spec.Repo
	var kind string
	if project {
		// The provider is the one the job's name pins — the profile's, from the
		// matched candidate — and never a value a launcher wrote (finding 4).
		// FUGARO_GIT_PROVIDER is not consulted as a source: it is read below
		// only to let it refuse.
		kind = job.Provider
	} else {
		var ok bool
		kind, ok = providerForSlug(slug, repo)
		if !ok {
			// Zero matches: FUGARO_RUN and the task disagree. Two matches cannot
			// happen through a launcher (task.Slug separates the kinds), and are
			// refused the same way rather than resolved to one.
			return fmt.Errorf("the run was launched for the slug %s, and that is not the slug of the repository this run's task names (%s) under any provider kind (%s): the slug and the task must name one repository. Nothing bound the two together before this check — a per-repository job's bucket condition was what stood in for it — so the run stops before any git credential is requested or anything is cloned",
				slug, repo, strings.Join(jobProviderKinds, ", "))
		}
	}
	if envKind := r.d.ProviderKind; envKind != "" && envKind != kind {
		// The flag (or FUGARO_GIT_PROVIDER) is an environment value a launcher
		// can set, so it may only tighten the admission and never change which
		// repository this run is for (12a item 2). Naming another kind is a
		// claim that this run is for another repository's job — there is no
		// kind outside jobProviderKinds to except: the file-backed test
		// provider is not one (internal/cli's providerOptions maps --provider
		// fake to an empty kind), and a local run has no Cloud Run execution,
		// so it never reaches this check at all — and it stops here. On a
		// project job this is also what keeps the variable from being read as a
		// source at all: it can refuse, and it can change nothing else.
		if project {
			return fmt.Errorf("the git provider in the job's environment is %s (the --provider flag, or %s), and this run is in the project job %s, which is the %s job of the profile %q: the provider of a project job is the one its own name pins, and the flag may only tighten the run's admission, never change which repository it is for, so the run stops before any git credential is requested",
				envKind, GitProviderEnv, gcp.ProjectJobName(job.Profile, job.Provider, job.Account), kind, job.Profile)
		}
		return fmt.Errorf("the git provider in the job's environment is %s (the --provider flag, or %s), and the run's slug %s is the slug of %s under %s: the flag may only tighten the run's admission, never change which repository it is for, so the run stops before any git credential is requested",
			envKind, GitProviderEnv, slug, repo, kind)
	}
	if !project {
		// The remote is where the clone comes from, and on a per-repository job
		// it is the job's own environment (Deps.Remote, RemoteEnv) or the origin
		// of a checkout already in the work directory — a launcher's to set, both
		// of them. The launcher refuses a repository URL that is not this
		// repository before it deploys anything (internal/infra's repoURL, and
		// the build's own CheckRepoURL), so a run meets these when the job's
		// environment was edited after launch. The two questions are the kind's
		// and the repository's: another provider's host is another repository —
		// the same owner and name is the case worth naming — and so is another
		// repository on the same provider's host, where the kind alone would
		// agree. The run's slug keys its locks, its cache and its budget
		// session, so it names neither.
		//
		// A project job never reaches this: its clone URL is computed from
		// task.json and the policy (cloneURL, design §4.1(1)), so the job's
		// environment carries nothing to compare — FUGARO_REMOTE stops being
		// read on a project job (plan task 3.3 step 3), and a value a launcher
		// wrote there can neither redirect the run nor halt it.
		remote := r.originURL(ctx)
		if other := remoteOfAnotherKind(kind, remote); other != "" {
			return fmt.Errorf("the run would clone from %s, which is %s's, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for, so the run stops before any git credential is requested",
				remoteDisplay(remote), other, slug, repo, kind, RemoteEnv)
		}
		if host := remoteOffTheKindsHost(kind, remote); host != "" {
			// The kind check above knows only the hosts gitprov.KindForURL
			// recognises; every other host was "no disagreement" (review C3).
			// The git credential is scoped to the remote's host
			// (gitops.CredentialURL), so a host that is not exactly the
			// provider's own would receive the provider's token, and an alias
			// spelling of it (a trailing dot, www., the ssh-over-443 host)
			// names a repository the run cannot verify.
			return fmt.Errorf("the run would clone from %s, whose host %s is not %s's own host %s, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, and the provider's credential goes only to the provider's own host, so the run stops before any git credential is requested",
				remoteDisplay(remote), host, kind, repourl.ProviderHost(kind), slug, repo, kind, RemoteEnv)
		}
		if other := remoteOfAnotherRepo(kind, repo, remote); other != "" {
			// internal/infra's repoURL refuses this at deploy time — "it must be
			// this repository, not only this provider" — and the run refuses it
			// here for the same reason: it clones, caches, locks and accounts under
			// its slug, and another repository's URL under that slug is another
			// repository's work. other is a canonical repository name, or the path
			// itself when the path is one CanonicalRepo refuses — the sentence
			// names what the remote points at either way.
			return fmt.Errorf("the run would clone from %s, which names %s on %s's host and is not this run's repository %s, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, and it must be this repository, not only this provider, so the run stops before any git credential is requested",
				remoteDisplay(remote), other, kind, repo, slug, repo, kind, RemoteEnv)
		}
	}
	if want, err := task.Slug(kind, repo); err != nil || want != slug {
		// The binding both paths keep (design §12a item 2, plan task 1.5 step 1
		// and task 3.3 step 2): the slug under which the run reads and writes
		// the bucket, takes its locks and opens its budget session must be the
		// slug of the repository its task names under the provider it is about
		// to open. On a per-repository job it is implied by the derivation
		// above and re-checked so the values the run actually uses are the ones
		// that were compared; on a project job it is the whole binding, since
		// the provider comes from the job's name and nothing else ties the slug
		// to the repository — the job serves every repository of its profile.
		got := want
		if err != nil {
			got = err.Error()
		}
		if project {
			return fmt.Errorf("the run was launched for the slug %s, and this run is in the project job %s, whose provider is %s: the slug must be the slug task.Slug(%s, %s) the task's repository computes (%s), because it keys the bucket paths, the locks, the cache and the budget session of this run. The run stops before any git credential is requested or any lease exists",
				slug, gcp.ProjectJobName(job.Profile, job.Provider, job.Account), kind, kind, repo, got)
		}
		return fmt.Errorf("the run's slug %s is not the slug task.Slug(%s, %s) computes (%s): the run stops before any git credential is requested", slug, kind, repo, got)
	}
	if !found {
		// No policy published here: the binding is the whole check (Task 1.3
		// step 1's absent case). Reading this as an empty allow-list would
		// refuse every run of every installation that has published nothing.
		// A project job cannot be here — its candidates are the policy's — so
		// this is the per-repository path's end.
		return nil
	}
	canonical, err := projectpolicy.Canonical(kind, repo)
	if err != nil {
		return fmt.Errorf("the repository this run was launched for cannot be named in the project's allow-list: %w", err)
	}
	if project {
		// The profile's admission (plan task 3.3 step 4, design §4.1): the
		// repository must be admitted for the (profile, provider, account) this
		// run is actually on, so a repository's own fugaro.yaml cannot widen its
		// access by selecting a profile whose list is wider. One function words
		// it (projectpolicy.AdmissionRefusal), and its project-list branch is
		// the very sentence the launcher's advisory twin prints
		// (projectpolicy.NotAdmitted), so the two cannot drift apart.
		refusal, err := p.AdmissionRefusal(job.Profile, canonical)
		if err != nil {
			return fmt.Errorf("this run is in the project job %s: %w", gcp.ProjectJobName(job.Profile, job.Provider, job.Account), err)
		}
		if refusal != "" {
			return fmt.Errorf("%s. The run stops before any lease exists", refusal)
		}
		return nil
	}
	if !projectpolicy.AdmitRepo(p.AllowRepos, canonical) {
		// The refusal itself is projectpolicy's, so the launcher's advisory
		// twin (internal/cli) refuses a launch with the same message; the
		// sentence about what stops is this check's own.
		return fmt.Errorf("%s. The run stops before any lease exists", projectpolicy.NotAdmitted(p.AllowRepos, canonical))
	}
	return nil
}
