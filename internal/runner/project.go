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
// knows (gitprov.KindForURL), and the ones that do name the derived kind. A
// remote on the derived kind's host is no disagreement *here* whatever
// repository it names — that is the other half of the guard, on the host being
// the only thing this function reads: remoteOfAnotherRepository, which the
// admission check asks beside this one.
func remoteOfAnotherKind(derived, remote string) string {
	if derived == "" {
		return ""
	}
	if kind := gitprov.KindForURL(remote); kind != "" && kind != derived {
		return kind
	}
	return ""
}

// remoteOfAnotherRepository names the repository a remote points at, when the
// remote is on the derived kind's own host and points at a repository other
// than the one the run's task names. It is the same disagreement as
// remoteOfAnotherKind's one host over: the kind check catches acme/app on
// another provider's host, and this catches another owner or name — or another
// case of it — on the same one, which is what editing FUGARO_REMOTE to a
// second repository of the same forge looks like. Both are the slug naming one
// repository while the clone comes from another, and the slug keys the run's
// locks, its cache, its budget session and (through the policy's allow-list)
// whether it runs at all.
//
// "" is no disagreement: the remote names this repository, the task's
// repository cannot be named (the check above refuses that run already), or
// the remote names no repository this parser can pick out of it. Only a remote
// whose host is the derived kind's reaches the comparison at all, so a local
// path, an httptest URL and a self-hosted installation keep the reading they
// had: a remote this Fugaro cannot place is the launcher's own business
// (internal/infra's repoURL), not a claim about another repository.
func remoteOfAnotherRepository(derived, repo, remote string) string {
	if derived == "" || gitprov.KindForURL(remote) != derived {
		return ""
	}
	got := repoOfRemote(remote)
	if got == "" {
		return ""
	}
	want, err := task.CanonicalRepo(repo)
	if err != nil || got == want {
		return ""
	}
	return got
}

// repoOfRemote names the repository a remote URL points at, as owner/name in
// task.CanonicalRepo's spelling, or "" when the URL does not say it. The three
// shapes a remote is written in are read: https://host/owner/repo.git (any
// scheme, a credential in the userinfo, a query and a fragment dropped),
// git@host:owner/repo.git and ssh://git@host/owner/repo.git. A path with
// anything but one owner and one name — a group or a project path, as a
// self-hosted forge or a GitLab-style host uses — is left "" rather than
// guessed at: the caller compares this to a repository, and half of a path
// compared to a whole one would refuse a run that clones exactly what it says.
func repoOfRemote(remote string) string {
	var path string
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		path = u.Path
	} else if at, rest, ok := strings.Cut(remote, "@"); ok && !strings.Contains(at, "/") {
		_, path, _ = strings.Cut(rest, ":") // scp-like: git@host:owner/repo.git, and the path is what follows the colon
	}
	owner, name, ok := gitprov.SplitRepo(strings.Trim(path, "/"))
	if !ok {
		return ""
	}
	canonical, err := task.CanonicalRepo(owner + "/" + name)
	if err != nil {
		return ""
	}
	return canonical
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
// the value of any credential-named parameter in its query and in its
// fragment, and a query or fragment that is a bare token with no name in front
// of it (addBareRemoteSecret, whose doc has the argument and the one limit).
// Those three are the places remoteDisplay drops for the refusals
// this package writes, and this is the other half: what the run can recognise,
// it redacts wherever it appears, including git's own "unable to access ..."
// lines, which quote the URL it was given verbatim (the fragment with them —
// git is handed https://host/repo.git#access_token=SECRET and prints it).
//
// A remote url.Parse refuses is read off the string as written instead
// (addRemoteSecretRaw): the parse is how this function finds the three places,
// not a condition on registering them, and the shapes it refuses are exactly
// the ones a forge-shaped credential hides in — an scp-like remote with a
// query (git@github.com:acme/app.git?access_token=SECRET), a password holding a
// "/" (https://user:tok/en@host/repo.git) or an escape url.Parse will not
// decode (https://user:pa%zz@host/repo.git). Returning there would leave the
// credential in no list at all while git is still handed the URL verbatim,
// which is the one outcome this function's doc rules out.
func (r *run) addRemoteSecrets(remote string) {
	u, err := url.Parse(remote)
	if err != nil {
		r.addRemoteSecretRaw(remote)
		return
	}
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			r.addSecret(pw)
			// The pair, as a URL writes it: that is how git echoes it back.
			r.addSecret(u.User.String())
		} else if name := u.User.Username(); name != "" && !remoteAnonymousUsers[strings.ToLower(name)] {
			r.addSecret(name)
			// And the spelling the URL was written with: git echoes the URL it
			// was given, escapes and all, so https://ghp%40x@host/repo.git
			// comes back exactly like that and a redactor holding only the
			// decoded username would miss it — the reason
			// addRemoteSecretParams registers both spellings too.
			r.addSecret(u.User.String())
		}
	}
	r.addRemoteSecretParams(u.RawQuery)
	// A fragment carries no query of its own to Go: it is one opaque string
	// here, so the same parameter parsing is applied to it, and to both
	// spellings of it, because either can be the one git echoes back.
	r.addRemoteSecretParams(u.RawFragment)
	if u.RawFragment != u.Fragment {
		r.addRemoteSecretParams(u.Fragment)
	}
}

// addRemoteSecretRaw reads the three places off a remote url.Parse refuses, as
// the string was written: the fragment, the query, and the userinfo ahead of
// the first "@". The cuts are the ones a URL is written with — "?" opens the
// query, "#" the fragment — and both parts are handed to the same parameter
// parsing the parsed path uses, so a credential-named parameter and a bare
// token are registered the same way either way.
//
// The userinfo is everything in front of the first "@" of the authority, with
// the "//" of a scheme cut off when there is one; a remote with no "@" has no
// userinfo to register. An unparseable remote may hold an "@" in its path as
// well (https://host/acme@app.git), and that part is registered too: the
// parse that would have told the two apart is the one that failed, and
// registering a substring that was never a credential costs a redaction while
// missing one costs the credential. An scp-like remote's "git@" is the
// anonymous user every forge writes, and is skipped as it is on the parsed
// path; a "name:value" userinfo registers the value and the pair, for the
// reason the parsed path does.
//
// Only the spelling as written is registered here: this path exists because
// url.Parse refused the string, so there is no decoded spelling to be had, and
// the spelling git echoes back is the one the remote was written with.
func (r *run) addRemoteSecretRaw(remote string) {
	head, frag, hasFrag := strings.Cut(remote, "#")
	head, query, hasQuery := strings.Cut(head, "?")
	if hasQuery {
		r.addRemoteSecretParams(query)
	}
	if hasFrag {
		r.addRemoteSecretParams(frag)
	}
	authority := head
	if i := strings.Index(authority, "//"); i >= 0 {
		authority = authority[i+2:]
	}
	at := strings.Index(authority, "@")
	if at < 0 {
		return
	}
	userinfo := authority[:at]
	if _, pw, ok := strings.Cut(userinfo, ":"); ok {
		r.addSecret(pw)
		r.addSecret(userinfo)
		return
	}
	if userinfo != "" && !remoteAnonymousUsers[strings.ToLower(userinfo)] {
		r.addSecret(userinfo)
	}
}

// addRemoteSecretParams registers the value of every parameter in raw — a
// query or a fragment — whose name is one a forge writes a credential under,
// in both spellings: the raw one and the percent-decoded one. Git echoes the
// URL it was given, escapes and all (?access_token=S%20ECRET comes back
// spelled exactly like that), so registering only the decoded value would
// leave that echo unredacted; registering both costs one extra value in the
// redactor and covers a remote written either way.
//
// A field with no name in front of it — a bare token, ?glpat-secret or
// #glpat-secret — has no name to be recognised by, so the whole field is
// registered (addBareRemoteSecret). A fragment carries no meaning for git and
// a query little more: git copies both into the URL it requests and echoes the
// URL verbatim when it cannot reach it, so nothing a remote states that way is
// worth leaving unredacted, and remoteDisplay's own test already takes
// #glpat-secret for a credential.
func (r *run) addRemoteSecretParams(raw string) {
	for _, field := range strings.Split(raw, "&") {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			r.addBareRemoteSecret(field)
			continue
		}
		if v == "" {
			continue
		}
		name := k
		if decoded, err := url.QueryUnescape(k); err == nil {
			name = decoded
		}
		if !remoteSecretParams[strings.ToLower(name)] {
			continue
		}
		r.addSecret(v)
		if decoded, err := url.QueryUnescape(v); err == nil && decoded != v {
			r.addSecret(decoded)
		}
	}
}

// addBareRemoteSecret registers field — one &-separated part of a query or a
// fragment with no "name=" in it — as a credential, in both spellings, for the
// reason addRemoteSecretParams gives: there is no name to recognise it by, so
// what is left is the value itself, and git echoes it back in the URL it was
// given. minSecretLen is the floor, the same one addMountedSecrets applies:
// below it a field is registered nowhere, because redacting three characters
// out of everything the run writes costs more than the token they could be.
// The floor is not a judgement about shape — a four-character bare fragment is
// registered, and that is the cost of not being able to recognise one — only
// about how much a mis-registration would take with it.
func (r *run) addBareRemoteSecret(field string) {
	if len(field) < minSecretLen {
		return
	}
	r.addSecret(field)
	if decoded, err := url.QueryUnescape(field); err == nil && decoded != field && len(decoded) >= minSecretLen {
		r.addSecret(decoded)
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
	if remote := r.originURL(ctx); remote != "" {
		// The remote is where the clone comes from, and it is the job's own
		// environment (Deps.Remote, RemoteEnv) or the origin of a checkout
		// already in the work directory — a launcher's to set, both of them.
		// The launcher refuses a repository URL off the provider's host before
		// it deploys anything (internal/infra's repoURL, and the build's own
		// CheckRepoURL), so a run meets either of these when the job's
		// environment was edited after launch: a repository of the same owner
		// and name on another provider's host is another repository, and so is
		// another repository on the same host, and the run's slug — which keys
		// its locks, its cache and its budget — would not name either of them.
		//
		// The kind is what the check can name a remote *by* (gitprov.KindForURL
		// reads a host and nothing else), so a remote on the derived kind's own
		// host is the second refusal, on the repository the remote's path
		// names. Both are asked of the same remote, so the one URL is read
		// once (originURL runs git).
		if other := remoteOfAnotherKind(kind, remote); other != "" {
			return fmt.Errorf("the run would clone from %s, which is %s's, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for, so the run stops before any git credential is requested",
				remoteDisplay(remote), other, slug, repo, kind, RemoteEnv)
		}
		if otherRepo := remoteOfAnotherRepository(kind, repo, remote); otherRepo != "" {
			return fmt.Errorf("the run would clone from %s, which is the repository %s, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for, so the run stops before any git credential is requested",
				remoteDisplay(remote), otherRepo, slug, repo, kind, RemoteEnv)
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
