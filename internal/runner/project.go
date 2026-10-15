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
// refusal takes no lease and writes nothing remote but the run's own record
// (bootstrap's deferred handler). cfg is the configuration the run uses:
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

// remoteDisplay is the remote as a refusal may quote it: an scp-like remote as
// its host alone, anything else as the URL without its userinfo, its query and
// its fragment. Deps.Remote is a launcher's to set and a remote URL may carry a
// credential in any of the three — https://user:token@host/repo.git,
// https://host/repo.git?access_token=SECRET (the shape Bitbucket's forge URLs
// use), and the same in a fragment — and what a refusal quotes should carry
// none of them. The run registers what it can recognise with its redactor as
// well (addRemoteSecrets), so that git's own messages about a remote it could
// not reach are redacted too, but the display drops them rather than relying
// on that.
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
	u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
	return u.String()
}

// remoteSecretParams are the query parameters a git remote URL carries a
// credential in, by the convention of the forges that write them that way:
// Bitbucket's ?access_token=, GitLab's ?private_token=, and the names a
// hand-written remote reaches for.
var remoteSecretParams = map[string]bool{
	"access_token": true, "private_token": true, "token": true, "auth": true,
	"key": true, "password": true, "client_secret": true, "x-token-auth": true,
}

// remoteAnonymousUsers are the usernames a remote URL puts in front of a
// credential, or in front of nothing at all: a bare git@ says nothing, and
// x-access-token (GitHub) and x-token-auth (Bitbucket) are the names those
// forges expect with the token as the password. None of them is a secret, and
// registering one would redact it out of every line the run publishes.
var remoteAnonymousUsers = map[string]bool{
	"git": true, "oauth2": true, "x-access-token": true, "x-token-auth": true,
	"token": true, "user": true,
}

// addRemoteSecrets registers with the run's redactor every credential a remote
// URL can carry: its userinfo — the password, and the username when there is
// none, because https://<token>@host/repo.git is a shape the forges accept —
// and the value of any query parameter named like one. A remote reaches error
// text and result.json (git's own "unable to access ..." lines quote the URL
// it was given), and remoteDisplay is what keeps it out of the refusals this
// package writes, so this is the other half: what the run can recognise, it
// redacts wherever it appears.
func (r *run) addRemoteSecrets(remote string) {
	u, err := url.Parse(remote)
	if err != nil {
		return
	}
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			r.addSecret(pw)
			// The pair, as a URL writes it: that is how git echoes it back.
			r.addSecret(u.User.String())
		} else if name := u.User.Username(); name != "" && !remoteAnonymousUsers[strings.ToLower(name)] {
			r.addSecret(name)
		}
	}
	for k, vs := range u.Query() {
		if remoteSecretParams[strings.ToLower(k)] {
			for _, v := range vs {
				r.addSecret(v)
			}
		}
	}
}

// admissionCheck admits the repository this run was launched for, before any
// git credential exists and before the clone (plan task 1.5): the run's slug
// and its repository must agree, and the project's policy, when it has
// published one, must have the repository on its allow-list. A run with no
// Cloud Run execution is not checked: the binding exists because a job's
// identity is what an admission rests on (R2-9).
//
// It runs after the project's policy has been read (Task 1.3) and before the
// provider is opened, so no git credential is requested and nothing has been
// cloned; and before the lock and the budget session, so a run that is not
// admitted writes no lease, no outcome and no spend. The claim is the one
// write that precedes it — it is what makes this execution the run's owner,
// and a duplicate is turned away there, before any of this is read — so the
// trace a refusal leaves is its own run record, carrying the refusal, and
// nothing else anywhere.
//
// The claim stays above this check, and that order is a ruling, not an
// accident (the owner delegated it to the analysis below, which this follows):
//
//   - The claim is create-only. It can never overwrite another execution's
//     record — a second execution is refused by the store, not allowed to
//     replace what is there — so claiming before admitting takes nothing away
//     from any run and leaves a refused one exactly one trace, its own record.
//   - A run that is not admitted takes no lock, no lease, no ledger entry and
//     no outcome either way: the refusal is before all four, whichever side of
//     the claim it lands on.
//   - An admitted repository gains no hole from the claim. The claim is not
//     what admits a run — this check is — so moving it below changes nothing
//     about what may clone, push or spend, only about what a refusal looks
//     like afterwards.
//
// What the other order would break is the shipped duplicate contract: two
// executions of one run are told apart at the claim, and an execution that
// another one owns has no record of its own to be refused into. Swapping the
// two put the admission refusal in front of the duplicate check, and
// TestRunnerTellsADuplicateApartBeforeAdmission (with internal/cli's
// TestExecDuplicateExecutionExitsRemoteError as the end-to-end half) is what
// failed on it.
//
// Its refusals are the run's own: the slug it was
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
	if remote := r.originURL(ctx); remoteOfAnotherKind(kind, remote) != "" {
		// The remote is where the clone comes from, and it is the job's own
		// environment (Deps.Remote, RemoteEnv) or the origin of a checkout
		// already in the work directory — a launcher's to set, both of them.
		// The launcher refuses a repository URL off the provider's host before
		// it deploys anything (internal/infra's repoURL, and the build's own
		// CheckRepoURL), so a run meets this when the job's environment was
		// edited after launch: a repository of the same owner and name on
		// another provider's host is another repository, and the run's slug —
		// which keys its locks, its cache and its budget — would not name it.
		return fmt.Errorf("the run would clone from %s, which is %s's, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for, so the run stops before any git credential is requested",
			remoteDisplay(remote), remoteOfAnotherKind(kind, remote), slug, repo, kind, RemoteEnv)
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
