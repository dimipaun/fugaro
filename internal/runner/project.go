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

// remoteHost reports the host a git remote names, the provider kind that host
// belongs to (gitprov.KindForURL; "" is a host no kind of this Fugaro's owns)
// and whether the remote names a host at all.
//
// The three answers are deliberately three, not two. A remote that names no
// host — a local path, an empty Deps.Remote, a bare "/" — is left to the checks
// that were always there: the binding does not extend the launcher's URL rules
// to every remote a local run may use. A remote that names a host is refused by
// the caller unless that host is the derived kind's, which is one answer for
// both of the ways it can differ: another provider's host (github.com for a
// bitbucket job) and a host no kind owns at all
// (https://github.com.evil.example/acme/app.git, https://evil.example.com/...).
//
// Collapsing the second into the first is what the first shape of this check
// did: remoteOfAnotherKind answered "" ("no disagreement") for every host
// gitprov.KindForURL does not know, so a tampered FUGARO_REMOTE on an unknown
// host was admitted on the owner/name comparison alone, and the run then scoped
// this repository's git credential to that host (internal/gitops'
// CredentialURL, which has no allow-list of its own) and cloned from it: the
// provider's credential went to the attacker's server and attacker-supplied
// code ran under the repository's slug — its locks, its cache, its budget
// session and its allow_repos admission. A deployed job cannot meet this: the
// launcher writes the remote from the provider's own host and refuses any other
// URL before it deploys anything (internal/infra's repoURL, and the build's own
// CheckRepoURL: "the build would send its credential there"), so an unknown
// host at run time is a job environment edited after launch, which is exactly
// what this check is for (the review finding on this change).
//
// A remote that names a host the URL parser cannot read is one of the remotes
// that names a host, not one that names none: git accepts spellings url.Parse
// refuses and connects to the host in the authority all the same
// (https://github.com\@evil.example/acme/app.git is evil.example's, and so is
// https://evil.example/acme/app%ZZ.git), so gitprov.SplitRemoteRaw reads that
// shape where SplitRemote answers "no host at all" — and the kind it derives is
// "" for it (gitprov.KindForHost), which is the refusal above. Skipping it
// instead is what the first shape of this check did, and it is a fail-open: the
// remote git was handed names a host this binding never looked at, and the
// run's git credential is scoped to that host (the review finding on this
// change).
func remoteHost(remote string) (host, kind string, named bool) {
	if h, _, ok := gitprov.SplitRemote(remote); ok {
		return h, gitprov.KindForURL(remote), true
	}
	if h, _, ok := gitprov.SplitRemoteRaw(remote); ok {
		return h, gitprov.KindForHost(h), true
	}
	return "", "", false
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
//
// The shape is gitprov.SplitRemote's question, not a second copy of it: the
// host half of this binding (remoteHost, through gitprov.KindForURL) reads the
// same remote, and the two must not disagree
// about what it names. Keying the scp-like form on "@" here made them agree on
// the wrong answer for a userinfo-less scp remote — "github.com:attacker/malware.git"
// came back as naming nothing, from both halves, while git ssh'd to github.com
// and asked for attacker/malware.git (the review finding on this change).
//
// The URL shape SplitRemote cannot read is read here rather than skipped
// (gitprov.SplitRemoteRaw): git reads its path the same way, so a path
// canonicalization refuses on it — https://github.com/acme/app%ZZ.git names no
// repository this check can compare — is a refusal, which is what the caller
// does with the error. Skipping the whole remote because its URL parser
// refused it left both halves of this binding unasked (the review finding on
// this change).
func remoteRepoPath(remote string) (string, bool) {
	_, p, ok := gitprov.SplitRemote(remote)
	if !ok {
		if _, p, ok = gitprov.SplitRemoteRaw(remote); !ok {
			return "", false
		}
	}
	// Exactly one leading and one trailing "/" is the URL's own punctuation,
	// not a segment: "https://github.com/acme/app.git/" is the same remote as
	// without the last one. Anything else is left exactly as written, so an
	// empty segment ("github.com//acme/app") reaches the refusal below instead
	// of being normalized away — normalizing is what a reader of the URL would
	// do and git may not.
	p = strings.TrimSuffix(strings.TrimPrefix(p, "/"), "/")
	if p == "" {
		return "", false
	}
	return p, true
}

// repoNamedByRemote is the repository a git remote URL names, canonicalized
// exactly as task.CanonicalRepo canonicalizes a task's own repository (ASCII
// lower case, no ".git"). named is false for a remote that names no repository
// path at all (remoteRepoPath); err is task.CanonicalRepo's refusal of a path
// that does name one but is not an owner/name, which is a *refusal* for the
// admission check and never a reason to skip the comparison.
//
// It is the second half of the remote binding. remoteHost compares the
// remote's *host* alone, which binds the provider and nothing else: a
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
// its host alone, anything else as the URL without its userinfo, its query and
// its fragment — including a remote url.Parse reads as a plain path, with no
// scheme and no host, which is the shape a hand-written remote takes
// (token@host/repo.git: the "@" is still where git and curl end the userinfo,
// whether or not there is a scheme in front of it). Deps.Remote is a launcher's
// to set and a remote URL may carry a
// credential in any of the three — https://user:token@host/repo.git,
// https://host/repo.git?access_token=SECRET (the shape Bitbucket's forge URLs
// use), and the same in a fragment — and what a refusal quotes should carry
// none of them. The run registers what it can recognise with its redactor as
// well (addRemoteSecrets), so that git's own messages about a remote it could
// not reach are redacted too, but the display drops them rather than relying
// on that.
func remoteDisplay(remote string) string {
	u, err := url.Parse(remote)
	if err == nil && u.Host != "" {
		u.User = nil
		u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
		return u.String()
	}
	// Not a URL with a host: the scp-like form git reads as ssh, whose display
	// is the host alone — the userinfo before it may be a token, and the path
	// is what the refusal names. A remote with neither shape is quoted as it
	// was written, minus any userinfo, query and fragment.
	if host, _, ok := gitprov.SplitRemote(remote); ok {
		return host
	}
	if host, _, ok := gitprov.SplitRemoteRaw(remote); ok {
		// A URL authority url.Parse refuses: the host alone, as the scp-like
		// branch quotes it, because that is the only part of it that is not a
		// credential's to hold — everything before the authority's last "@" is
		// userinfo as git and curl read it, and this branch is where the
		// refusal that quotes such a remote gets a form that carries none of it
		// (the review finding on this change).
		return host
	}
	if err == nil {
		// A string url.Parse read and found no host in, and that neither split
		// branch answers for: a plain path as far as the parse is concerned,
		// which is what a remote written without a scheme looks like
		// (token@host/repo.git, or pw@host). u.String() returns such a string
		// verbatim — u.User is nil, because the parse never saw an authority —
		// so quoting it here is exactly the refusal this function exists to
		// prevent. The "@" is where git and curl end the userinfo with or
		// without a scheme, and what follows the first one is what may be
		// quoted; the query and the fragment go with the userinfo, for the
		// reason the URL branch drops them.
		head, _, _ := strings.Cut(remote, "#")
		head, _, _ = strings.Cut(head, "?")
		if _, rest, ok := strings.Cut(head, "@"); ok {
			if rest == "" {
				return "the remote"
			}
			return rest
		}
		return head
	}
	return "the remote"
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
	// And the userinfo as the remote was written, because the parse above is
	// lossy in exactly the place a credential sits: url.Parse decodes what it
	// read, and u.User.String() re-encodes it — https://x-token-auth:t%6fk@host
	// comes back as x-token-auth:tok — while git prints the URL it was handed,
	// escapes and all. The decoded spelling alone leaves "t%6fk" in every line
	// git writes about that remote (the review finding on this change); this
	// path, the one an unparseable remote already took, is the raw spelling.
	r.addRemoteSecretUserinfoRaw(remote)
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
// Only the spelling as written is registered here: the parse that would have
// given the decoded one is the one that failed, and the spelling git echoes
// back is the one the remote was written with.
func (r *run) addRemoteSecretRaw(remote string) {
	head, frag, hasFrag := strings.Cut(remote, "#")
	head, query, hasQuery := strings.Cut(head, "?")
	if hasQuery {
		r.addRemoteSecretParams(query)
	}
	if hasFrag {
		r.addRemoteSecretParams(frag)
	}
	r.addRemoteSecretUserinfoRaw(head)
}

// addRemoteSecretUserinfoRaw registers the userinfo of remote as it was
// written, for the paths that hold the string rather than a parse of it: the
// one url.Parse refused, and the one it read but decoded (its u.User.String()
// re-encodes what it read, so a password written with escapes comes back
// spelled differently — addRemoteSecrets's own call says why that matters).
// remote is the whole string here or the query- and fragment-less head of one;
// cutting the two off is what keeps a token in a query from being read as a
// password in front of an "@" that is not an authority's.
func (r *run) addRemoteSecretUserinfoRaw(remote string) {
	head, _, _ := strings.Cut(remote, "#")
	head, _, _ = strings.Cut(head, "?")
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
// published one, must have the repository on its allow-list. A local
// invocation is not checked: the binding exists because a job's identity is
// what an admission rests on (R2-9), and `fugaro exec --task-file` has none to
// bind. Deps.Local is what says so, and it is the invocation's own answer, not
// a missing marker: a process that carries no Cloud Run execution and is not
// local either — one no backend claims, the shape an edited job environment
// takes — is in the cloud and is checked.
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
	if r.d.Local && r.d.Execution == "" {
		// A local invocation is the one platform this binding does not apply
		// to (R2-9): `fugaro exec --task-file`, the file-backed fake provider
		// and the end-to-end tests keep the path they have always had. The
		// skip needs *both* halves, and the second is what the first shape of
		// this guard got wrong (the review finding on this change): reading
		// the absence of a Cloud Run execution name as "local" exempted a
		// process no backend claims — Local's zero value means the cloud
		// (runner.go), and a job whose environment was edited after launch
		// carries no execution name either — from everything below, the
		// slug↔task binding, the remote's host and the policy's allow-list,
		// with the git credential still scoped to whatever remote that
		// environment named. A process the invocation did not name local does
		// not reach here whatever its Execution says: the two are resolved
		// together by the CLI (exec.go's execPlatform, Local only where no
		// platform claims the process), so the pair Local = true with an
		// execution name is a contradiction of the fixtures' making, and
		// checking it is the fail-closed direction.
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
	host, hostKind, hostNamed := remoteHost(remote)
	if hostNamed && hostKind != kind {
		// The remote is where the clone comes from, and it is the job's own
		// environment (Deps.Remote, RemoteEnv) or the origin of a checkout
		// already in the work directory — a launcher's to set, both of them.
		// The launcher refuses a repository URL off the provider's host before
		// it deploys anything (internal/infra's repoURL, and the build's own
		// CheckRepoURL), so a run meets this when the job's environment was
		// edited after launch: a repository of the same owner and name on
		// another provider's host is another repository, and the run's slug —
		// which keys its locks, its cache and its budget — would not name it.
		if hostKind == "" {
			// Not another provider's host but no provider's at all. The
			// refusal is not the owner/name comparison's: the run scopes this
			// repository's git credential to the remote's host
			// (internal/gitops' CredentialURL, with no allow-list of its own),
			// so a remote on an unknown host hands the provider's credential
			// to that host and runs whatever it serves, under this slug. A
			// deployed job cannot carry one — the launcher writes the remote
			// from the provider's own host and the build refuses any other URL
			// before it deploys — so this is a job environment edited after
			// launch, the case this check exists for (the review finding on
			// this change; the first shape of it read this as "no
			// disagreement", because gitprov.KindForURL answers "" for a host
			// no kind owns and for no host at all alike).
			return fmt.Errorf("the run would clone from %s, whose host %s is not a host any git provider kind this Fugaro uses owns, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for — and the git credential for this repository is scoped to the remote's host, so a remote on another host is where %s's credential would be sent, which is what the launcher refuses before it deploys anything (internal/infra's repoURL, internal/backend/gcp's CheckRepoURL) — so the run stops before any git credential is requested",
				remoteDisplay(remote), host, slug, repo, kind, RemoteEnv, kind)
		}
		return fmt.Errorf("the run would clone from %s, which is %s's, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for, so the run stops before any git credential is requested",
			remoteDisplay(remote), hostKind, slug, repo, kind, RemoteEnv)
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
