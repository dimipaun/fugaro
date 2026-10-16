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

// remoteRepoPath is the path a git remote URL names — the part where the
// repository goes — and whether the remote names one at all: named is false
// for a local path, a bare host, and a path with nothing in it (a bare "/"),
// the remotes that name no repository for a check to compare.
//
// It is deliberately *not* a canonicalization. Whether the path is an
// owner/name is task.CanonicalRepo's question, and its answer is the
// admission's: this function hands the path over unread (as git will) and
// callers must refuse rather than skip when canonicalization fails.
// Skipping is the hole this split closed — a remote whose path contains a
// dot segment, an empty segment, an encoded separator or anything else outside
// the segment alphabet came back "names nothing to compare" and was admitted,
// and git (over HTTP, where libcurl resolves dot segments per RFC 3986 §5.2.4;
// or through a server that does) then fetched the repository the path resolves
// to: https://github.com/acme/app.git/../../attacker/malware is
// https://github.com/attacker/malware, and the run cloned it under acme/app's
// slug — the same hole as naming another owner/name plainly, reached through a
// path that only *looks* like this run's (the review finding on this change).
// The path is read from EscapedPath, not Path: url.Parse decodes "%2F" into a
// separator, so a remote that encodes one would otherwise be compared as a
// repository it does not plainly name.
func remoteRepoPath(remote string) (path string, named bool) {
	if remote == "" {
		return "", false
	}
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		path = u.EscapedPath()
	} else if at, rest, ok := strings.Cut(remote, "@"); ok && !strings.Contains(at, "/") {
		// scp-like: git@github.com:owner/name.git (url.Parse reads this as a
		// scheme-less opaque URL with no host).
		if _, p, ok := strings.Cut(rest, ":"); ok {
			path = p
		}
	}
	// Exactly one leading and one trailing "/" is the URL's own punctuation,
	// not a segment: "https://github.com/acme/app.git/" is the same remote as
	// without the last one. Anything else is left exactly as written, so an
	// empty segment ("github.com//acme/app") reaches the refusal below instead
	// of being normalized away — normalizing is what a reader of the URL would
	// do and git may not.
	path = strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/")
	if path == "" {
		return "", false
	}
	return path, true
}

// repoNamedByRemote is the repository a git remote URL names, canonicalized
// exactly as task.CanonicalRepo canonicalizes a task's own repository (ASCII
// lower case, no ".git"). named is false for a remote that names no repository
// path at all (remoteRepoPath); err is task.CanonicalRepo's refusal of a path
// that does name one but is not an owner/name, which is a *refusal* for the
// admission check and never a reason to skip the comparison.
//
// It is the second half of the remote binding. remoteOfAnotherKind compares
// the remote's *host kind* alone, which binds the provider and nothing else: a
// job for slug(github, acme/app) whose FUGARO_REMOTE is
// https://github.com/attacker/malware.git is on the derived kind's host and was
// admitted, and the run then cloned and ran attacker/malware under acme/app's
// slug — its locks, its cache, its budget session and the policy's allow_repos
// admission all naming a repository whose work was not the work being run (the
// review finding on this change). The launcher already refuses that URL
// (internal/infra's repoURL compares the canonical owner/name, D1a's own rule),
// so a run only meets it when the job's environment was edited after launch,
// which is exactly what this check is for.
//
// A remote path that is not canonicalizable is refused, not skipped: the
// binding refuses every remote that names a repository other than this run's,
// and a path git would resolve to one this check cannot read is exactly that.
// A remote naming no path at all keeps the behaviour it had before this check
// existed — the binding does not extend the launcher's URL rules to every
// remote a local run may use.
func repoNamedByRemote(remote string) (string, bool, error) {
	path, named := remoteRepoPath(remote)
	if !named {
		return "", false, nil
	}
	c, err := task.CanonicalRepo(path)
	if err != nil {
		return "", false, fmt.Errorf("%q is not a repository as owner/name", path)
	}
	return c, true, nil
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
	remote := r.originURL(ctx)
	if remoteOfAnotherKind(kind, remote) != "" {
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
	named, namedOK, namedErr := repoNamedByRemote(remote)
	if namedOK || namedErr != nil {
		// The remote names a repository path, and reading it is what this
		// check runs on: a path canonicalization refuses is a refusal, not a
		// reason to skip the comparison. A remote whose path is not an
		// owner/name (dot segments, empty segments, an encoded separator) is
		// one git may well resolve to a repository this run is not for —
		// https://github.com/acme/app.git/../../attacker/malware is
		// attacker/malware — so it stops here rather than passing unchecked,
		// which is what the first shape of this check did (the review finding
		// on this change).
		if namedErr != nil {
			return fmt.Errorf("the run would clone from %s, and %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for, and this run is for %s (%s under %s) — dot segments, empty segments, an encoded separator and any character a repository name cannot hold are a path git or the server may resolve to a repository this run is not for — so the run stops before any git credential is requested",
				remoteDisplay(remote), namedErr, RemoteEnv, repo, slug, kind)
		}
		// The remote names a repository, and that repository must be this
		// run's. Same owner and name on another host is another repository,
		// and a different owner or name on the *same* host — the case the
		// host-kind check above cannot see — is one too: the slug keys the
		// run's locks, its cache and its budget session, and it names the
		// repository this run's task does.
		if want, err := task.CanonicalRepo(repo); err == nil && named != want {
			return fmt.Errorf("the run would clone from %s, which is the repository %s, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for, so the run stops before any git credential is requested",
				remoteDisplay(remote), named, slug, repo, kind, RemoteEnv)
		}
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
