package runner

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/safetext"
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
// A run in a project job carries the selection: the job's own name was written
// from a (profile, provider, account) triple of the policy (task 3.2's layout,
// matched by jobIdentity), so the profile the run is *in* is read from the job
// and compared with the one its workflow selects. They disagree when the
// workflow was edited after the launch, when the job was chosen by hand, or
// when the repository's workflow and the job it was launched as came from
// different configs; the run stops rather than running the workflow's work on
// a job nobody selected for it. A per-repository job carries no profile and
// nothing is compared (R2-9).
//
// An older binary (0.8.0, which does not know the key) refuses a fugaro.yaml
// that sets job_profile: the key is a config field, and the strict decoder
// refuses a name it does not know. The skew is therefore a refusal at
// validation, never a run on a profile nobody selected.
func (r *run) checkJobProfile(ctx context.Context, workflow string, wf config.Workflow) error {
	p, found, err := r.projectPolicy(ctx)
	if err != nil {
		return err
	}
	if job, project := r.projectJob(); project && job.Profile != wf.JobProfileOrDefault() {
		return fmt.Errorf("this run is in the Cloud Run job %s, which is the project job of the profile %q, and the workflow %s selects the job profile %q: the job a run is launched as is the one its workflow's selection built, so the two disagreeing means the selection changed after the launch, or the job was chosen by hand. The run stops rather than running the workflow's work on a job nobody selected for it",
			gcp.ProjectJobName(job.Profile, job.Provider, job.Account), job.Profile, workflow, wf.JobProfileOrDefault())
	}
	if wf.JobProfile == "" || wf.JobProfile == projectpolicy.DefaultProfileName {
		return nil
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

// remoteHost reports the host a git remote names, the provider kind that host
// belongs to (gitprov.KindForHost; "" is a host no kind of this Fugaro's owns)
// and whether the remote names a host at all.
//
// The three answers are deliberately three, not two. A remote that names no
// host at all — a local path, an empty Deps.Remote, a bare "/", an scp-like
// remote whose userinfo is everything in front of its colon — is left to the
// checks that were always there: the binding does not extend the launcher's URL
// rules to every remote a local run may use. A remote that names a host is
// refused by the caller unless that host is the derived kind's, which is one
// answer for both of the ways it can differ: another provider's host (github.com
// for a bitbucket job) and a host no kind owns at all
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
//
// A remote *written as a URL* whose authority yields no host this binding will
// read is the same answer, and it is the one that was still missing: an empty
// authority (https:///github.com/attacker/malware.git, https:////github.com/a/b.git),
// an authority git refuses outright (a userinfo holding a second "@", a
// whitespace or control character, a scheme that is not a scheme), and a file
// URL *with an empty authority* (file:///tmp/web.git, whose "file:" is a scheme
// and whose authority is nothing) — not every file URL: "file://host/path"
// names the host host as plainly as "https://host/path" does, and this reader
// answers the same for it (SplitRemote reads its authority), so it is refused
// for that host's kind and not as an unreadable shape. None of them is the
// local path the first answer is for: git does not read
// https:///github.com/a/b.git as a path — libcurl connects it to github.com and
// clones github.com/a/b.git — and a spelling libcurl refuses
// (https://a@b@github.com/a/b.git is "Bad hostname" to it) is no remote either.
// Both halves of the binding were skipped for these, the host kind and the
// owner/name alike, while git cloned whatever the remote named under this run's
// slug: the same fail-open, reached through a spelling no reader answered for
// (the review finding on this change). A *local* run never meets this — the
// admission check is the cloud's (admissionCheck) — and the launcher writes no
// such remote (internal/infra's repoURL takes an https URL on the provider's
// host, u.Host != "", and nothing else).
func remoteHost(remote string) (host, kind string, named bool) {
	if h, _, ok := gitprov.SplitRemote(remote); ok {
		return h, gitprov.KindForHost(h), true
	}
	if h, _, ok := gitprov.SplitRemoteRaw(remote); ok {
		return h, gitprov.KindForHost(h), true
	}
	if strings.Contains(remote, "://") {
		// Written as a URL — a scheme's colon and an authority's "//", the
		// shape git hands to a URL parser — and not one either reader will name
		// a host for. Two spellings reach here, and both are refused:
		//
		//   - A URL whose authority yields no host: an empty authority
		//     (https:///github.com/a/b.git, https:////github.com/a/b.git), an
		//     authority git refuses outright (a userinfo holding a second "@"),
		//     a file URL with an empty authority, and — since the authority is
		//     what gitprov.SplitRemoteRaw checks — one holding a control
		//     character in its host *or* its userinfo (https://x\n@github.com/a/b.git,
		//     which git refuses to parse at all: "credential url cannot be
		//     parsed").
		//   - A scheme that is not a scheme: " https://github.com/acme/app.git",
		//     a whitespace slip, is "Malformed input to a URL function" to
		//     libcurl.
		//
		// This branch is the text alone because that is the whole of what the
		// two shapes have in common: SplitRemoteURL answers "not a URL shape"
		// for the second, so asking it first and falling through to a text test
		// was one question asked twice (the review finding on this change).
		//
		// The third answer — a remote that names no host, which the checks that
		// were always there are left to judge — is a local path, an empty
		// Deps.Remote, and an scp-like remote whose userinfo is all there is in
		// front of the colon ("@:path"), and it is *not* this shape: git does
		// not read "https:///github.com/a/b.git" as a path, it reads a host out
		// of it (libcurl connects https:///github.com/acme/app.git to github.com
		// and clones github.com/acme/app.git). Reading it as "no host, nothing
		// to check" skipped both halves of the binding — the host kind and the
		// owner/name — while git cloned the repository the remote named, with
		// this run's slug and this repository's git credential scoped to that
		// host: the fail-open this branch closes (the review finding on this
		// change). A URL this check cannot read is refused, never skipped.
		return "", "", true
	}
	return "", "", false
}

// remoteEndpoint reads the scheme and the port off a remote written as a URL —
// a scheme, "//" and an authority — as written rather than as url.Parse reads
// it, so this and the helpers above read one remote one way (SplitRemoteRaw
// makes the same cut, for the same reason). endpoint is false for every other
// remote: the scp-like form ("git@host:owner/name"), a local path, a bare host,
// and a "scheme://" with nothing after it, none of which names an http(s)
// endpoint for a credential to be scoped to at all.
//
// The two answers are the half of a run's git credential scope that is not the
// host: internal/gitops' CredentialURL scopes the credential to scheme + "://" +
// the authority, with no allow-list of its own, so these are where the token
// goes. The launcher's rule about a repository URL is the same three clauses
// (internal/infra's repoURL refuses one that is not https, off the provider's
// host, with a port), and what was compared before this was the host alone
// (remoteHost, above) — an http:// remote on the provider's own host, and an
// https:// one naming a port, were admitted, with the credential sent in
// cleartext or to an endpoint the provider does not serve (the review finding
// on this change).
func remoteEndpoint(remote string) (scheme, port string, endpoint bool) {
	scheme, authority, _, ok := gitprov.SplitRemoteURL(remote)
	if !ok {
		return "", "", false
	}
	if _, host, ok := gitprov.SplitRemoteUserinfo(authority); ok {
		// The userinfo, where one is written: git and curl end it at the last
		// "@" of the authority, so what follows is the host and the port — the
		// one cut of a userinfo, shared with the readers of this remote's shape
		// (gitprov.SplitRemoteUserinfo).
		authority = host
	}
	if authority == "" {
		return "", "", false
	}
	host := authority
	if strings.HasPrefix(authority, "[") {
		// An IPv6 literal: its own colons are the address's, and a port is
		// what follows the "]".
		if i := strings.Index(authority, "]"); i >= 0 {
			host = authority[:i+1]
		}
	} else if i := strings.LastIndex(authority, ":"); i >= 0 {
		host = authority[:i]
	}
	return scheme, strings.TrimPrefix(authority[len(host):], ":"), true
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
//
// A remote written as a URL whose authority yields no host either — an empty
// authority, an authority git refuses outright, a file URL — has its path read
// off the same cut (gitprov.SplitRemoteURL), and for the same reason: the host
// half of this binding refuses that shape (remoteHost), and a path half that
// read it as "names nothing" would leave the owner/name comparison of a remote
// the host half already stopped asking a question no one answered. The path a
// URL like that names is not what git would clone (libcurl reads
// https:///github.com/acme/app.git's path as /acme/app.git, not as the
// /github.com/acme/app.git written here), which is exactly why nothing rests on
// reading it: it is read so that no remote goes uncompared, and what it compares
// against is this run's repository either way.
func remoteRepoPath(remote string) (string, bool) {
	_, p, ok := gitprov.SplitRemote(remote)
	if !ok {
		if _, p, ok = gitprov.SplitRemoteRaw(remote); !ok {
			_, _, p, ok = gitprov.SplitRemoteURL(remote)
		}
	}
	if !ok {
		return "", false
	}
	// Exactly one leading and one trailing "/" is the URL's own punctuation,
	// not a segment: "https://github.com/acme/app.git/" is the same remote as
	// without the last one. Anything else is left exactly as written, so an
	// empty segment ("github.com//acme/app") reaches the refusal below instead
	// of being normalized away — normalizing is what a reader of the URL would
	// do and git may not. The trim is gitprov.TrimRepoPath, which the
	// launcher's rule for a repository URL asks too (internal/infra's repoURL):
	// one function, or the launcher accepts a URL the run then refuses (the
	// review finding on this change).
	p = gitprov.TrimRepoPath(p)
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
//
// named is also false for a remote naming no path at all, and the caller
// decides what that means: for a remote written as a URL it is a refusal
// (remoteAdmissionErr — a URL always names where it clones from), while for a
// local path or an scp-like remote it keeps the behaviour it had before this
// check existed, because the binding does not extend the launcher's URL rules
// to every remote a local run may use.
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

// remoteAdmissionErr is remoteAdmissionRefusal, one line: a refusal is a line
// of the run's record, of the job's log and of the error bootstrap returns, and
// every value the refusal quotes comes from the job's own environment — the
// remote itself, the host it names, the port, the path — which is a string a
// person can edit after the job is launched, so it can hold anything a string
// can hold. A host with a newline in it (git@github.com\n:acme/app.git, which
// gitprov.SplitRemote reads as written rather than answering "no host") wrote a
// second line into the record, and an ESC in one (git@github.com\x1b[31m:...)
// wrote a terminal sequence the reader's terminal obeys: a run's record is
// where a job's environment must not be able to write lines of its own (the
// review finding on this change).
//
// The pass is over the *whole refusal* at the one place every refusal is built,
// rather than over each value at each "%s": the values are assembled into one
// sentence by ten branches, and a branch that quotes a value this function does
// not know about today is covered by construction. safetext.OneLine is the
// package's one answer to "a string from an untrusted source, printed" — it
// drops ESC/CSI/OSC sequences and turns every other control character, C1
// control, bidi control and line separator into "?" — and this is the same
// treatment every other line the run publishes gets.
func remoteAdmissionErr(remote, kind, repo, slug string) error {
	err := remoteAdmissionRefusal(remote, kind, repo, slug)
	if err == nil {
		return nil
	}
	// errors.New of the sanitized text: the refusals below carry no wrapped
	// cause (every one of them is a sentence about this run's own
	// environment), so there is nothing for a %w to preserve.
	return errors.New(safetext.OneLine(err.Error()))
}

// remoteAdmissionRefusal refuses a remote that is not exactly the repository
// this run is for, cloned from exactly the endpoint the launcher writes: the
// half of admissionCheck that reads the job's own environment (Deps.Remote,
// RemoteEnv) or the origin of a checkout already in the work directory. It is
// one function because the refusals below are one question — is this remote the
// remote this run was launched with? — asked of one string several ways, and
// because the table of spellings a caller outside this package may hand it is
// the audit's own test (project_internal_test.go's TestRemoteAdmissionErr): a
// remote that does not parse to exactly (the derived kind's host, this run's
// owner/name) is refused, never skipped, and the two older shapes of this check
// each skipped one half of that (the review findings on this change).
//
// Its text is not one line by construction — it quotes the remote, its host,
// its port and the path it names as they were written — which is why the caller
// public in this package is remoteAdmissionErr, the one-line wrapper above.
//
// It is called before the provider is opened, so a refusal here means no git
// credential exists and nothing was cloned: every sentence below ends the same
// way for that reason.
func remoteAdmissionRefusal(remote, kind, repo, slug string) error {
	// The endpoint the remote names comes first, and not only its host: what this
	// run's git credential is scoped to is scheme://host[:port] (internal/gitops'
	// CredentialURL, which has no allow-list of its own), so a remote on the
	// provider's own host over http has this repository's credential sent in
	// cleartext, and one naming a port has it sent to an endpoint the provider
	// does not serve. Both agree with the slug by host and by owner/name —
	// gitprov.KindForHost reads a host as written and answers "" for a host with
	// a port, but nothing above compares the port — so nothing else sees them
	// (the review finding on this change). The launcher writes neither:
	// internal/infra's repoURL refuses a repository URL that is not https, on the
	// provider's host, with no port, before it deploys anything, and
	// internal/backend/gcp's CheckRepoURL the same for a build. So a
	// launcher-deployed job cannot carry one and a job environment edited after
	// launch — the case this whole check exists for — can, which is why this
	// refuses rather than skipping. A remote naming no URL at all (the scp-like
	// form, a local path) is not this check's: it names no endpoint for a
	// credential to be scoped to.
	//
	// It is asked before the host is compared because the two refusals overlap
	// on exactly the case the port is: a host with a port is no provider kind's
	// (gitprov.KindForHost, which gitprov.SplitRemote no longer strips it for),
	// so the host comparison would refuse github.com:8443 as an unknown host —
	// true, and less useful than the sentence here, which says where the
	// credential would go instead (the review finding on this change).
	//
	// Only the two schemes a credential is scoped to are this check's: gitops'
	// CredentialURL answers "" for everything that is not http or https, so an
	// ssh:// remote — the URL spelling of the scp-like form above, which this
	// check passes when it names the provider's host as written
	// (ssh://git@github.com/acme/app.git, and git@github.com:acme/app.git in its
	// scp-like spelling) — has no credential to send anywhere. It passes because
	// the switch below names http and https and nothing else, not because
	// remoteEndpoint answers endpoint = false for it: that function reports an
	// endpoint for every scheme with an authority (ssh://git@github.com/acme/app.git
	// is scheme "ssh", endpoint = true), which is what lets this refusal read the
	// scheme off the string rather than off CredentialURL's own parse (so a
	// spelling url.Parse refuses is still refused here). A reader who took the
	// switch for "any endpoint must be https" would start refusing ssh remotes
	// that pass today.
	//
	// The port an ssh:// remote names is not this check's either — no credential
	// is scoped to an ssh endpoint — but that does not make
	// ssh://git@github.com:22/acme/app.git a remote that passes: git connects to
	// the host and port as written, so the host is "github.com:22", which
	// gitprov.KindForHost answers "" for ("a host with a port" is no kind's host)
	// and the host comparison further down refuses. Refusing it is right — the
	// launcher writes no ssh remote at all, and a port is not one it wrote — and
	// it is this check's silence about an ssh port — the switch below is asked
	// only of http and https — that leaves that refusal to the host comparison
	// rather than to the port sentence below.
	//
	// The first shape of this check refused every other scheme, which read one
	// remote two ways by the way it was written: an ssh spelling's pair got one
	// answer from the scp-like branch and the opposite from this one, on a
	// premise ("the credential sent in cleartext") that does not hold where no
	// credential is scoped at all (the review finding on this change).
	if scheme, port, endpoint := remoteEndpoint(remote); endpoint {
		switch strings.ToLower(scheme) {
		case "http":
			return fmt.Errorf("the run would clone from %s, which is written over %s and not https: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change where its git credential is sent — and the run scopes this repository's git credential to the remote's endpoint (internal/gitops' CredentialURL, which has no allow-list of its own), so an http:// remote has the provider's credential for %s sent in cleartext over a connection nothing authenticates, while the launcher writes no other scheme (internal/infra's repoURL refuses one before it deploys anything) — so the run stops before any git credential is requested",
				remoteDisplay(remote), scheme, RemoteEnv, repo)
		case "https":
			if port != "" {
				return fmt.Errorf("the run would clone from %s, whose authority names the port %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change where its git credential is sent — and the run scopes this repository's git credential to the remote's endpoint (internal/gitops' CredentialURL, which has no allow-list of its own), so the credential for %s would go to the endpoint that port names and not to the one the launcher wrote, while the launcher writes no port at all (internal/infra's repoURL refuses a repository URL with one before it deploys anything) — so the run stops before any git credential is requested",
					remoteDisplay(remote), port, RemoteEnv, repo)
			}
		}
	}
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
		if host == "" {
			// Written as a URL whose authority yields no host this check will
			// read: an empty authority (libcurl connects
			// https:///github.com/acme/app.git to github.com and clones
			// github.com/acme/app.git, so git does not read it as the local
			// path the checks that were always there are left to judge), an
			// authority git refuses outright (a userinfo holding a second "@"
			// is "Bad hostname" to libcurl, a scheme that is not a scheme
			// "Malformed input"), and a file URL. Every one of them was
			// *skipped* — remoteHost answered "names no host" — so both halves
			// of this binding went unasked while git cloned whatever the remote
			// named, under this run's slug and with this repository's git
			// credential scoped to that host: the fail-open this branch closes
			// (remoteHost's doc has the whole argument; the review finding on
			// this change).
			return fmt.Errorf("the run would clone from %s, which is written as a URL whose host this check cannot read, and git does not read such a spelling as the local path a remote naming no host at all is: libcurl connects https:///github.com/acme/app.git to github.com and clones github.com/acme/app.git, so the spelling decides which host this run's git credential is scoped to and which repository runs under the slug %s (the slug of %s under %s), while the spellings this check cannot read are ones git does not clone at all (a two-'@' authority is \"Bad hostname\" to libcurl, a scheme that is not a scheme \"Malformed input\") — the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for, and a URL this check cannot read is refused rather than admitted unread — so the run stops before any git credential is requested",
				remoteDisplay(remote), slug, repo, kind, RemoteEnv)
		}
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
			// disagreement", because gitprov.KindForHost answers "" for a host
			// no kind owns and for no host at all alike).
			return fmt.Errorf("the run would clone from %s, whose host %s is not a host any git provider kind this Fugaro uses owns, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for — and the git credential for this repository is scoped to the remote's host, so a remote on another host is where %s's credential would be sent, which is what the launcher refuses before it deploys anything (internal/infra's repoURL, internal/backend/gcp's CheckRepoURL) — so the run stops before any git credential is requested",
				remoteDisplay(remote), host, slug, repo, kind, RemoteEnv, kind)
		}
		return fmt.Errorf("the run would clone from %s, which is %s's, and its slug %s is the slug of %s under %s: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for, so the run stops before any git credential is requested",
			remoteDisplay(remote), hostKind, slug, repo, kind, RemoteEnv)
	}
	named, namedOK, namedErr := repoNamedByRemote(remote)
	if !namedOK && namedErr == nil {
		// A remote written as a URL that names no repository path at all:
		// https://github.com and https://github.com?/attacker/malware.git are
		// the two shapes (the query and the fragment are not a path, and a bare
		// "/" trims to nothing), and git asks that host for a repository that is
		// not there — GET /?/info/refs, a 404 the server answers for every one
		// of them. The run halts further down for a reason that names neither
		// the remote nor the edit that wrote it, and the binding's own doc says
		// such a remote is refused, never skipped: the comparison simply not
		// running is the shape this check exists to make impossible (the review
		// finding on this change). The rule is the *URL* shape's, because
		// naming no path is not always a URL's to judge: a local path, an empty
		// Deps.Remote and an scp-like remote naming no path are the checks'
		// that were always there (a local run clones from its local paths), and
		// the launcher writes none of them for a cloud run
		// (internal/infra's repoURL takes an https URL with an owner/name).
		if _, _, _, shaped := gitprov.SplitRemoteURL(remote); shaped {
			return fmt.Errorf("the run would clone from %s, which is written as a URL and names no repository at all: the remote the run clones from (the job's environment, %s, or the origin of a checkout in the work directory) may only tighten the run's admission, never change which repository it is for, and this run is for %s (%s under %s) — the launcher writes the whole URL (https://%s/%s.git), so a URL naming no owner/name is a job environment edited after launch, and git would ask that host for the repository such a path names, which serves none — so the run stops before any git credential is requested",
				remoteDisplay(remote), RemoteEnv, repo, slug, kind, host, repo)
		}
	}
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
	return nil
}

// remoteDisplay is the remote as a refusal may quote it, and it is one line:
// remoteDisplayLine reads the form off the remote as written, and this is that
// form through safetext.OneLine, so a remote holding a control character —
// "git@github.com\n:acme/app.git" is read as written (SplitRemote's scp-like
// branch), and its host is what the display returns — cannot put a second line,
// or an ESC sequence, into whatever quotes it. Refusals get the same treatment
// once more as a whole (remoteAdmissionErr's own pass, which is the guarantee
// for the values a refusal quotes without going through here: the host, the
// port, the path); this one is for the next caller that quotes a remote, so the
// display is safe to publish on its own (the review finding on this change).
func remoteDisplay(remote string) string {
	return safetext.OneLine(remoteDisplayLine(remote))
}

// remoteDisplayLine is the remote as a refusal may quote it: an scp-like remote
// as its host alone, anything else as the URL without its userinfo, its query and
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
func remoteDisplayLine(remote string) string {
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
		// prevent. The "@" that ends the userinfo is the authority's last
		// (remoteSplitUserinfo says why), and what follows it is what may be
		// quoted; the query and the fragment go with the userinfo, for the
		// reason the URL branch drops them.
		head, _, _ := strings.Cut(remote, "#")
		head, _, _ = strings.Cut(head, "?")
		_, rest, ok := remoteSplitUserinfo(head)
		if ok {
			if rest == "" {
				return "the remote"
			}
			return rest
		}
		return head
	}
	return "the remote"
}

// remoteSplitUserinfo splits a remote with its query and fragment already cut
// off at the end of its userinfo: userinfo is what the authority's start and
// its last "@" enclose, rest is everything after that "@", and ok says whether
// there was one at all. The "@" is the authority's last because that is where
// git and curl end the userinfo — a password may hold one, and
// https://user:a@b@host/repo.git is a credential whose halves a cut at the
// first "@" splits into a registered fragment and a quoted tail (the review
// finding on this change). The authority ends at the first "/", so an "@" in
// the path is no authority's: read from the first "@" of
// https://host/p@x, the authority looked like "host/p@x" and registered
// "host/p", which is the host and the head of the path, a credential in
// neither spelling.
//
// A remote with no "//" has its authority at the start of the string: the
// scp-like form (git@host:owner/name.git) and the scheme-less one
// (token@host/repo.git) both write the userinfo in front of the whole thing.
//
// The cut itself is gitprov.SplitRemoteUserinfo's, the one all three readers of
// a remote's shape ask: a display that ended the userinfo somewhere else than
// the readers of the same string did would drop a credential's head into a
// refusal, or quote the tail of one (the review finding on this change).
//
// An authority with nothing in it is read off the whole head instead
// (gitRawUserinfo), because there is no authority-scoped reading of it to make:
// https:///x:TOKEN@github.com/acme/app.git names github.com and acme/app.git to
// git (libcurl reads the first path segment as the host), its userinfo is
// "x:TOKEN" to git all the same — and the empty authority is the one shape that
// reaches this cut with a credential and no authority to hold it, so returning
// "no userinfo" here quoted the token back in the refusal and registered it
// with no redactor entry at all (the review finding on this change). An
// authority that is *not* empty answers alone: an "@" in the path is no
// authority's, and reading https://host/p@x from the whole head registered
// "host/p" as a credential.
func remoteSplitUserinfo(head string) (userinfo, rest string, ok bool) {
	off := 0
	if i := strings.Index(head, "//"); i >= 0 {
		off = i + 2
	}
	authority := head[off:]
	if i := strings.Index(authority, "/"); i >= 0 {
		authority = authority[:i]
	}
	userinfo, host, ok := gitprov.SplitRemoteUserinfo(authority)
	if !ok {
		if authority == "" {
			return gitRawUserinfo(head)
		}
		return "", "", false
	}
	// The rest keeps whatever followed the authority — the path, usually —
	// because the display quotes the host's own string (host/repo.git for a
	// remote written without a scheme), not the host alone.
	return userinfo, host + head[off+len(authority):], true
}

// gitRawUserinfo reads a remote head the way git and curl read it when the
// shape carries no authority to delimit a userinfo in: the userinfo is the text
// in front of the last "@", the tail is what follows that "@", and both are
// read off the whole head with the "//" of a scheme and the "/" an empty
// authority leaves in front of it removed — and with no cut at the first "/",
// because the authority's end is exactly what this shape does not write. An
// empty authority's "https:////x:TOKEN@host/path", whose first path segment is
// the host, is read the same way: "x:TOKEN" is the credential there too.
func gitRawUserinfo(head string) (userinfo, tail string, ok bool) {
	rest := head
	if i := strings.Index(rest, "//"); i >= 0 {
		rest = rest[i+2:]
	}
	rest = strings.TrimLeft(rest, "/")
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return "", "", false
	}
	return rest[:at], rest[at+1:], true
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
// the last "@". The cuts are the ones a URL is written with — "?" opens the
// query, "#" the fragment — and both parts are handed to the same parameter
// parsing the parsed path uses, so a credential-named parameter and a bare
// token are registered the same way either way.
//
// The userinfo is read as git reads it rather than as an authority is
// delimited (addRemoteSecretUserinfoGitRaw has the reading and the shapes that
// made it a separate one); a remote with no "@" has no userinfo to register.
// An unparseable remote may hold an "@" in its path as well
// (https://host/acme@app.git), and that part is registered too: the parse that
// would have told the two apart is the one that failed, and registering a
// substring that was never a credential costs a redaction while missing one
// costs the credential. An scp-like remote's "git@" is the
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
	r.addRemoteSecretUserinfoGitRaw(head)
}

// addRemoteSecretUserinfoGitRaw registers the userinfo of a remote head the way
// git reads it off one url.Parse refused: the text in front of the last "@" of
// the string, with the query and the fragment already cut off and the "//" of a
// scheme removed — no other cut, because the shape that defeated the parse is
// the one where the authority ends somewhere else than the first "/" says. A
// password holding a "/" is exactly that shape
// (https://user:ghp_a/b@github.com/acme/app.git: git connects to github.com
// and hands over the whole password), and the authority-scoped cut
// (remoteSplitUserinfo) ends the authority inside it, which would leave a
// credential git is still handed in no list at all. Reading the whole head
// registers the path as userinfo for the shapes that are not that one
// (https://host/acme@app.git), which is the trade addRemoteSecretRaw's doc
// makes: a redaction too many costs nothing, a credential missed costs
// everything.
//
// The read itself is gitRawUserinfo's, shared with the empty authority the
// authority-scoped cut hands here (remoteSplitUserinfo's doc): one reading of
// "where the userinfo ends when there is no authority to end it", so the
// display and the redactor cannot disagree about one string.
func (r *run) addRemoteSecretUserinfoGitRaw(head string) {
	userinfo, _, ok := gitRawUserinfo(head)
	if !ok {
		return
	}
	r.addUserinfoSecret(userinfo)
}

// addUserinfoSecret registers one userinfo as a credential: the password
// alone and the "name:value" pair for a userinfo that carries one, and the
// whole of it for one that does not, unless it is a name the forges put in
// front of a token (remoteAnonymousUsers) — none of those is a secret, and
// registering one would redact it out of every line the run publishes.
func (r *run) addUserinfoSecret(userinfo string) {
	if _, pw, ok := strings.Cut(userinfo, ":"); ok {
		r.addSecret(pw)
		r.addSecret(userinfo)
		return
	}
	if userinfo != "" && !remoteAnonymousUsers[strings.ToLower(userinfo)] {
		r.addSecret(userinfo)
	}
}

// addRemoteSecretUserinfoRaw registers the userinfo of remote as it was
// written, for the paths that hold the string rather than a parse of it: the
// one url.Parse refused, and the one it read but decoded (its u.User.String()
// re-encodes what it read, so a password written with escapes comes back
// spelled differently — addRemoteSecrets's own call says why that matters).
// remote is the whole string here or the query- and fragment-less head of one;
// cutting the two off is what keeps a token in a query from being read as a
// password in front of an "@" that is not an authority's. The authority and
// the "@" that ends its userinfo are remoteSplitUserinfo's to find, so this
// registers the same text remoteDisplay drops from the same string.
func (r *run) addRemoteSecretUserinfoRaw(remote string) {
	head, _, _ := strings.Cut(remote, "#")
	head, _, _ = strings.Cut(head, "?")
	userinfo, _, ok := remoteSplitUserinfo(head)
	if !ok {
		return
	}
	r.addUserinfoSecret(userinfo)
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
// git credential exists and before the clone (plan tasks 1.5 and 3.3): the run
// is placed in the job it is in, the run's slug and its repository must agree
// under that job's provider, and the policy must admit the repository — for the
// project, and on a project job for the profile the job's own name pins. A
// local invocation is not checked: the binding exists because a job's identity
// is what an admission rests on (R2-9). A process with no Cloud Run execution
// that is not local is in the cloud, and it is refused (jobName).
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
// Its refusals are the run's own: the job it is in
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
		if err := remoteAdmissionErr(remote, kind, repo, slug); err != nil {
			return err
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
