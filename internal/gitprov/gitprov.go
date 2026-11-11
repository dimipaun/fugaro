// Package gitprov abstracts the git hosting provider (design §3.1). The
// github and bitbucket subpackages implement it against the real REST APIs;
// fake is a file-backed stand-in for tests.
package gitprov

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"
)

// PRSpec describes the pull request a run wants.
type PRSpec struct {
	// Number, when non-zero, makes EnsurePR update pull request Number's
	// draft state only: it never creates one, and never touches the title
	// or body beyond the draft prefix. It returns ErrPRNotOpen (wrapped,
	// with the PR) when that PR isn't open, or its source branch isn't
	// Branch.
	Number int    `json:"number,omitempty"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Draft  bool   `json:"draft"`
	// Labels and Reviewers are applied only to a pull request EnsurePR
	// creates with Draft false; a draft carries neither, and ApplyReady
	// is how a PR that became ready gets them.
	Labels    []string `json:"labels,omitempty"`
	Reviewers []string `json:"reviewers,omitempty"`
}

// PR is a pull request on the provider.
type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Draft  bool   `json:"draft"`
	// DraftFallback is set when Draft is true only because the title
	// carries DraftPrefix: the host refused real draft pull requests, so
	// the PR is an ordinary one that merely looks like a draft (and the
	// host may already have requested reviews, such as CODEOWNERS).
	DraftFallback bool `json:"draft_fallback,omitempty"`
}

// PRUpdate is a change to an existing pull request by UpdatePR; a nil
// field is left alone.
type PRUpdate struct {
	Title *string
	Body  *string
	// Draft, when set, also moves the PR to that draft state (the same
	// semantics as EnsurePR by number, including the DraftPrefix fallback).
	Draft *bool
}

// ErrPRNotOpen means an update by number found the pull request merged
// or closed, or on another source branch: a follow-up must never bring
// back or reuse a pull request that isn't open on its branch.
var ErrPRNotOpen = errors.New("pull request is not open on this branch")

// PRHead is a pull request's head as review mode (design generic-tool §3.6,
// G12) needs it: just enough to decide whether the run may check it out,
// never the rest of PRInfo (title, body, comments), which review mode
// reads from the checkout itself, not from the provider.
//
// Review mode is trusted-author-only (owner ruling 2026-10-12): every
// field below but SHA and Open exists to decide that, never to drive the
// checkout itself.
type PRHead struct {
	// SHA is the head commit; review mode fetches and checks it out.
	SHA string
	// Base is the branch pull request Number targets now, as PRInfo.Base
	// carries it: review mode diffs the head against this branch (a
	// retargeted pull request — an integration or release branch — must not
	// be reviewed against the branch its first run happened to assume).
	// "" when the provider reported none, which the runner reads as "use
	// the run's own ref".
	Base string
	// Fork is whether the head repository differs from the one the
	// provider was opened for. A fork is reviewed only when its author is
	// trusted AND SourceRepo's owner is that same author (reviewmode.go):
	// a trusted person's shared fork must not be able to carry someone
	// else's commits.
	Fork bool
	// Open is whether the pull request is still open; a merged or closed
	// one is refused.
	Open bool
	// AuthorID is the PR author's stable account ID (GitHub numeric user
	// ID as a string; Bitbucket account_id), for followup.trusted — the
	// same list and the same meaning a follow-up comment's trust already
	// uses (internal/followup/trust.go).
	AuthorID string
	// AuthorLogin is the PR author's login (GitHub) or display name
	// (Bitbucket, which has no stable login), for the refusal message and
	// the fork-ownership check only — never a trust decision by itself:
	// a login can be renamed or reused.
	AuthorLogin string
	// Association is the raw author_association GitHub reports (OWNER,
	// MEMBER, COLLABORATOR, CONTRIBUTOR, NONE, ...), for the refusal
	// message; "" when the provider has no such signal (Bitbucket).
	Association string
	// Collaborator is whether Association counts as a collaborator
	// (OWNER, MEMBER or COLLABORATOR — github.collaborator, the same
	// classification gitprov.Comment.Collaborator uses); always false on
	// Bitbucket, which has no such signal and fails closed on a fork
	// instead of relying on this (reviewmode.go).
	Collaborator bool
	// Self is whether the author is Fugaro's own identity (the provider's
	// own bot/app account) — the same signal gitprov.Comment.Self uses.
	// Only meaningful, and only ever true, when Fork is false: Fugaro
	// never opens a pull request from a fork.
	Self bool
	// SourceRepo is owner/name of the head repository, as the provider
	// spells it; "" when it no longer exists (a deleted fork, which fails
	// closed: Fork is true with no SourceRepo to match AuthorLogin
	// against).
	SourceRepo string
}

// RepoInfo is what a follow-up needs to know about the repository.
type RepoInfo struct {
	Private bool
}

// PRState is the lifecycle state of a pull request.
type PRState string

const (
	PROpen   PRState = "open"
	PRMerged PRState = "merged"
	PRClosed PRState = "closed" // closed unmerged: declined or superseded on Bitbucket
)

// PRInfo is a pull request as the provider shows it.
type PRInfo struct {
	Number int
	URL    string
	State  PRState
	Draft  bool
	Title  string // as the host shows it: with DraftPrefix on a prefix-marked draft
	Body   string // GitHub's body, Bitbucket's description; untrusted text
	// Base is the branch pull request Number targets RIGHT NOW, as the
	// provider's own answer gives it (GitHub's base.ref, Bitbucket's
	// destination.branch.name); "" when the provider reported none. A run
	// reads this rather than assuming, because a pull request's base can be
	// retargeted after it was opened — an integration branch, a release
	// branch — and a review that diffs against the base the FIRST run
	// opened against reviews the whole of the new base's divergence.
	Base         string
	AuthorID     string // the PR author's account ID (GitHub numeric user ID; Bitbucket account_id)
	SourceBranch string
	SourceRepo   string // owner/name of the head repository, as the provider spells it
	HeadSHA      string // may be abbreviated (Bitbucket gives 12 hex); compare with SameCommit
}

// CommentKind is where a comment sits on a pull request.
type CommentKind string

const (
	CommentInline  CommentKind = "inline"  // on a line of the diff, in a review thread
	CommentReview  CommentKind = "review"  // a review's top-level body
	CommentGeneral CommentKind = "general" // on the pull request's conversation
)

// Comment is one comment on a pull request. Its Body is untrusted text.
type Comment struct {
	ID           string      `json:"id"`
	Kind         CommentKind `json:"kind"`
	Author       string      `json:"author,omitempty"`       // display name or login, for the report
	AuthorID     string      `json:"author_id,omitempty"`    // stable account ID, for the trust rule
	Collaborator bool        `json:"collaborator,omitempty"` // GitHub: author_association OWNER, MEMBER or COLLABORATOR; Bitbucket: always true
	Self         bool        `json:"self,omitempty"`         // posted as the identity Fugaro uses
	SelfKnown    bool        `json:"self_known,omitempty"`   // the adapter could tell (its identity lookup worked)
	Resolved     bool        `json:"resolved,omitempty"`     // inline only: its thread is resolved
	Outdated     bool        `json:"outdated,omitempty"`     // inline only: the line it was on has changed
	Truncated    bool        `json:"truncated,omitempty"`    // inline only: its thread had more comments than were read
	Deleted      bool        `json:"deleted,omitempty"`
	Path         string      `json:"path,omitempty"`
	Line         int         `json:"line,omitempty"`
	Body         string      `json:"body"`
	CreatedAt    time.Time   `json:"created_at"`
	URL          string      `json:"url,omitempty"`
}

// GitAuth is how git, the runner's and the agent's, authenticates to the
// provider over HTTPS, plus any extra variables the agent's tools need.
type GitAuth struct {
	Username string            // HTTPS username, such as x-token-auth or x-access-token
	Token    string            // HTTPS password; empty means git needs no credentials
	Expires  time.Time         // zero when the token does not expire
	Env      map[string]string // extra agent variables, such as GH_TOKEN
}

// TokenSource is where a provider's installation tokens come from (C2.2):
// one token scoped to the run's repository, valid for at least minValid, and
// its expiry. The GitHub adapter's own mint with the App key is one, and the
// installation's credentials service is the other (github.ServiceTokens).
type TokenSource interface {
	Token(ctx context.Context, minValid time.Duration) (string, time.Time, error)
}

// TokenHolder is implemented by a TokenSource that can name the token it is
// holding, and it exists for one reason: a host that echoes the credential back
// in a failure. A reader that mints through a source it does not own -- the
// credentials service's read mints are the ones (github.ServiceReadTokens,
// bitbucket.ServiceReadTokens) -- cannot otherwise learn the value to redact,
// because the token is minted on the provider's first call and never passes
// through the reader's hands.
//
// The empty string is a source holding nothing, which is the state of one that
// has not minted yet and is not a value to redact.
type TokenHolder interface {
	HeldToken() string
}

// ServiceTokenSetter is implemented by a provider whose installation tokens
// can be taken from the installation's credentials service instead of a
// credential of the job's own (C2.2, D145): UseServiceTokens installs the
// source every token then comes from. The runner installs one on every
// provider of a run whose installation has the service on, because the
// identities a mint carries are the run's -- the job's Google ID token and
// the run's own Firebase one -- and not anything a git adapter holds; a
// provider that does not implement this (Bitbucket's, whose repository access
// token no service can mint per job, D145/F7) is left exactly as it was, and
// so is the file-backed test provider.
type ServiceTokenSetter interface {
	UseServiceTokens(TokenSource)
}

// BotAccount is a git host's own account, the one the host attributes commits
// to: GitHub links a commit to a GitHub App's bot account when the commit's
// author and committer are that account's noreply identity,
// "<ID>+<Login>@users.noreply.github.com".
type BotAccount struct {
	ID    string // the account's stable numeric user ID
	Login string // its login, "<slug>[bot]" on GitHub
}

// BotAccountReader is implemented by a provider whose host links a commit to
// an account of its own, and is how a run's commits carry that account's
// identity instead of Fugaro's own (gitops.BotIdentity). The runner reads it
// once per run, before the first commit. A provider that does not implement
// it -- Bitbucket's, whose commits are the run's own and have no account to be
// linked to, and the file-backed test provider -- keeps gitops.Identity, and
// so does a run whose read fails.
type BotAccountReader interface {
	BotAccount(ctx context.Context) (BotAccount, error)
}

// ContentsReader is implemented by a provider that can read one file out of
// its repository, which is how a catalog entry's dockerfile source is
// fetched before a build (D1140, PIC3). path is a repository-relative path
// and ref a branch or tag ("" for the default branch); the caller validates
// both, because they come from a published catalog. The read is a read of
// this repository alone, made with the provider's own credential (the
// project's existing one — no new access is added), and it returns no more
// than MaxContentsBytes.
//
// It is not part of Provider: a host without a contents API has no way to
// answer it, and a caller that does not need it must keep working there. The
// GitHub and Bitbucket adapters and the file-backed test provider implement
// it; catalog.Readable is the offline list of the kinds whose adapter does,
// for a caller that must refuse a dockerfile source before a provider is
// opened.
type ContentsReader interface {
	Contents(ctx context.Context, path, ref string) ([]byte, error)
}

// PRBranchReader is implemented by a provider that can find the pull request
// a branch is the source of, whatever its state, which is how `fugaro
// followups` looks the parent of a run's deferred findings up (D1843, F30):
// by the run's own launcher-written branch, and never by a pull request
// number the job-written run record carries, so a job that wrote a record
// cannot name somebody else's merged pull request as its parent.
//
// It is not part of Provider for the reason ContentsReader is not: a host
// that cannot answer it must keep working everywhere else, and a caller that
// needs it refuses rather than guesses. The GitHub and Bitbucket adapters and
// the file-backed test provider implement it.
type PRBranchReader interface {
	// PullRequestByBranch reads the pull request whose source branch is
	// branch, however it ended: found is false when no pull request was ever
	// opened from that branch. The read is bounded to one request per host
	// plus one read of the pull request itself, and it is a read of this
	// repository alone.
	PullRequestByBranch(ctx context.Context, branch string) (info PRInfo, found bool, err error)
}

// MaxContentsBytes bounds what one Contents read returns: 1 MiB, GitHub's
// own ceiling for a contents reply, so the bound a caller applies is the
// bound the API already applies and no larger.
const MaxContentsBytes = 1 << 20

// The two facts about a file that make a Contents read answer an error
// without the file being absent: it holds nothing, or it is over
// MaxContentsBytes. Both adapters refuse a read of either — a definition read
// has no use for an empty definition, and neither host serves a reply over
// its own ceiling — and a caller that reads bytes wants both refused. They are
// named here because a caller that asks whether a repository HAS a file, and
// never reads its bytes, must not read either as "there is nothing at that
// path": a package-lock.json of two megabytes, and an empty requirements.txt,
// are both repositories whose image still has to carry that manager.
var (
	ErrContentsEmpty    = errors.New("the file is there and holds nothing")
	ErrContentsTooLarge = errors.New("the file is larger than a contents read returns")
)

// CommitReader is implemented by a provider that can resolve a ref to the
// commit it names, which is what a catalog entry's Dockerfile and its build
// context are read at (D1394, D1395). A source names a branch, and a read at
// that branch answers whatever the branch is at the moment of the read, so the
// copy resolves it once and reads both at the commit it got: one read, one
// commit, and the record's value is the commit the definition was taken from
// rather than the operator's own checkout.
//
// ref is a branch or a tag, "" for the repository's default branch; the caller
// validates it, because it comes from a published catalog. What comes back is
// a full commit, and a caller that puts it in a build, a record or a label
// holds it to that shape: the value reaches `git fetch` and the revision
// label, where a ref that is not a commit means something else.
//
// It is not part of Provider, for ContentsReader's own reason: a host without
// a commit read has no way to answer it, and a caller that does not need one
// must keep working there. The GitHub and Bitbucket adapters and the
// file-backed test provider implement it.
type CommitReader interface {
	Commit(ctx context.Context, ref string) (string, error)
}

// TreeReader is implemented by a provider that can read a directory of its
// repository at one commit, which is how a catalog entry's build context is
// read before a build: the files under path, at ref, each with the bytes the
// context's identity is taken over (D1401).
//
// path is a repository-relative directory — the catalog's own rule allows ".",
// the repository root — ref is the commit the caller resolved (CommitReader),
// and the paths that come back are relative to path, so moving a context
// inside its repository does not move its identity. limit bounds the read
// before anything is fetched: a tree over it is refused with ErrTreeTooLarge
// rather than carried, and a caller that puts these bytes into a build gets a
// bounded tree or an error.
//
// It is not part of Provider: a host without a tree read has no way to answer
// it, and a caller that does not need one must keep working there. The GitHub
// and Bitbucket adapters and the file-backed test provider implement it.
type TreeReader interface {
	Tree(ctx context.Context, path, ref string, limit TreeLimit) ([]TreeFile, error)
}

// TreeFile is one file of a tree read: its path relative to the directory that
// was read, and its bytes. A file of no bytes is a file (a repository holds
// them), which is why a tree read keeps one where a definition read refuses
// one.
type TreeFile struct {
	Path string
	Data []byte
}

// TreeLimit bounds one Tree read: how many files it may return and how many
// bytes they may hold together. Both are read off the host's own listing
// before a file is fetched, so a tree over the limit costs one listing and no
// content, and the refusal names the ceiling rather than a build that grew
// past it.
type TreeLimit struct {
	MaxFiles int
	MaxBytes int64
}

// ErrTreeTooLarge means a Tree read was refused because the tree is over the
// limit its caller gave: more files, or more bytes, than the read returns.
var ErrTreeTooLarge = errors.New("the tree is larger than a tree read returns")

// Provider is what the runner needs from the git host.
type Provider interface {
	// EnsurePR creates the pull request for spec.Branch or, if one already
	// exists (the agent may have opened it), updates only its draft state.
	// It is safe to call again after a failure: it finds what an earlier
	// call created. A *PartialError means the pull request exists (the
	// returned PR is valid) but some settings, such as labels or
	// reviewers, could not be applied — including a draft or title state
	// that could not be fully applied in the conservative direction (a PR
	// left looking more like a draft than requested is safe to leave for
	// a retry to heal; a PR left looking more ready than requested is not,
	// so that direction is reported as a plain, retryable error instead).
	// With spec.Number set it only updates that pull request's draft
	// state, and never creates one (see PRSpec.Number).
	EnsurePR(ctx context.Context, spec PRSpec) (PR, error)
	// Comment posts a comment on the pull request.
	Comment(ctx context.Context, pr PR, body string) error
	// GitAuth returns git credentials that stay valid for at least
	// minValid, refreshing them first if needed (design §6.2).
	GitAuth(ctx context.Context, minValid time.Duration) (GitAuth, error)
	// Repository reads the repository's visibility.
	Repository(ctx context.Context) (RepoInfo, error)
	// UpdatePR changes pull request number's title and/or body (and, when
	// u.Draft is set, its draft state) and nothing else: reviewers, labels,
	// the draft state when u.Draft is nil, and any field u leaves nil keep
	// their values. It reads the PR first: a PR that isn't open is not
	// written and gives ErrPRNotOpen (wrapped, with the PR), and an update
	// that would change nothing sends no write. A title update keeps the
	// DraftPrefix on a prefix-marked draft. The returned PR's Draft is the
	// state after the call.
	UpdatePR(ctx context.Context, number int, u PRUpdate) (PR, error)
	// ApplyReady requests reviewers and applies labels on pull request
	// number, which the caller has just made ready (a draft carries
	// neither; EnsurePR with Draft true never applies them). It is
	// idempotent: reviewers or labels already on the PR are not asked for
	// again, so a retry is safe. Bitbucket has no labels: they are dropped
	// with a warning, once per Provider. A failure, such as an unknown
	// reviewer, returns a *PartialError: the PR stays as it is (ready) and
	// the caller reports it. A PR that isn't open gives ErrPRNotOpen.
	ApplyReady(ctx context.Context, number int, reviewers, labels []string) error
	// PullRequest reads pull request number, whatever its state.
	PullRequest(ctx context.Context, number int) (PRInfo, error)
	// Comments returns every published comment on pull request number,
	// oldest first; filtering is the caller's. Unpublished ones (pending
	// reviews and drafts) and reviews with no body are left out.
	//
	// Self and SelfKnown say whether a comment was posted as the identity
	// Fugaro uses, which the adapter looks up (GitHub: the App, GET /app
	// with its JWT, which only this process holding the App's key can sign
	// — with the credentials service the identity is unknown, C2.2;
	// Bitbucket: the token's user, GET /user). A lookup the provider
	// answers definitely (a 4xx other than 408 and 429) leaves SelfKnown
	// false on every comment, and the answer is kept. A passing failure (a
	// network error, a 408, 429 or 5xx, a cancelled context) is not kept:
	// the GitHub adapter then returns the comments with SelfKnown false,
	// the Bitbucket adapter fails the call. Callers must work either way:
	// trust never rests on Self.
	Comments(ctx context.Context, number int) ([]Comment, error)
	// FetchPRHead reads pull request number's head for review mode (design
	// generic-tool §3.6, G12): its commit, whether it is open, and whether
	// its head repository is a fork of the one this Provider was opened
	// for. It never writes anything.
	FetchPRHead(ctx context.Context, number int) (PRHead, error)
	// MarkReady marks pull request number ready for review, bound to the
	// head expectedHeadSHA (E8, D2041, G2): it re-reads the head, refuses
	// with ErrHeadMoved when the pull request is not at that commit, and
	// only then clears the draft state. The credential comes from the merge
	// mint (UseMergeMint, E16/D2049); a provider without one refuses with
	// ErrNoMergeMint rather than reaching for its own, and a host with no
	// mint to gate says so with MintExempt and ErrMintNotApplicable rather
	// than being taken for one that does. It is not ApplyReady:
	// that one adds reviewers and labels to a pull request somebody else
	// made ready, and this one is the daemon's own new authority to un-draft
	// a pull request a run left as a draft.
	MarkReady(ctx context.Context, number int, expectedHeadSHA string) error
	// Merge merges pull request number at exactly the head expectedHeadSHA
	// (E8, G2, G14): it re-reads the head and refuses with ErrHeadMoved when
	// it has moved, sends the expected head to the host as the merge's own
	// precondition, and never sends an admin or bypass flag. The credential
	// is the merge mint's (UseMergeMint), so a merge is a merge the gate
	// minted for; a provider without one refuses with ErrNoMergeMint, and a
	// host with no mint to gate says so with MintExempt and
	// ErrMintNotApplicable. A provider whose merge API has no expected-head
	// precondition refuses with ErrMergeNoHeadPrecondition (F24): an unbound
	// merge is the one thing this call exists to make impossible.
	Merge(ctx context.Context, number int, expectedHeadSHA string) error
	// Checks reads the checks reported at commit sha of pull request number
	// (E8, F25). The state is ChecksState's to decide and the caller's to
	// fail closed on: an empty slice is a commit whose checks report
	// nothing, which is CIUnknown and never green, and an error is a read
	// that did not happen rather than a commit that passed. A provider that
	// cannot report checks at all refuses with ErrChecksUnsupported. A host
	// with more than one way to report CI reports all of them: a commit is
	// green only if every report on it passed, so a read that took one
	// report and not the other would read a failing commit as a passing one
	// (GitHub has two: check runs and commit statuses). Reading them takes a
	// permission the run's installation token does not ask for, so a host
	// that refuses the read for that reason reports
	// ErrChecksPermission — the same refusal as any other failed read, named
	// so it is not read as a commit with no checks.
	Checks(ctx context.Context, number int, sha string) ([]CheckRun, error)
}

// Opener opens the provider of kind (KindGitHub or KindBitbucket) for repo
// ("owner/name"). It returns the provider and the credential values it
// holds, which the runner adds to its redaction list.
type Opener func(ctx context.Context, kind, repo string) (Provider, []string, error)

// Static returns an Opener that always yields p, whatever the kind.
func Static(p Provider) Opener {
	return func(context.Context, string, string) (Provider, []string, error) { return p, nil, nil }
}

// PartialError reports that EnsurePR produced the pull request but could
// not apply all of its settings, in a direction safe to leave for a retry
// to heal (design §4.2) — for example a draft or title state that still
// looks more like a draft than requested, never more ready than requested.
type PartialError struct{ Err error }

func (e *PartialError) Error() string { return "pull request opened, but " + e.Err.Error() }
func (e *PartialError) Unwrap() error { return e.Err }

// DraftPrefix marks a draft on a repository that cannot make real draft
// pull requests (design §15).
const DraftPrefix = "[DRAFT] "

// DraftTitle returns title with DraftPrefix when draft is true, and without
// it otherwise, so the prefix can be added and removed as a PR's state changes.
func DraftTitle(title string, draft bool) string {
	title = strings.TrimPrefix(title, DraftPrefix)
	if draft {
		return DraftPrefix + title
	}
	return title
}

// SplitRemote splits a git remote into the host it points at and the path
// that names the repository there, in the two shapes git accepts: a URL
// ("https://github.com/acme/app.git", "ssh://git@github.com/acme/app.git"),
// and the scp-like form git reads as ssh, which is a host, a colon and a path
// ("git@github.com:acme/app.git").
//
// host is the authority's host *as written*, which is what git connects to and
// what KindForHost compares: a host naming a port is "github.com:8443" and not
// "github.com", because the port is part of where a git credential is scoped
// (internal/gitops' CredentialURL) and KindForHost answers "" for a host with a
// port — a SplitRemote that stripped it read
// https://github.com:8443/acme/app.git as github.com's, so the run's host-kind
// check admitted a remote whose credential goes to an endpoint the provider
// does not serve (the review finding on this change). The userinfo in front of
// it is not part of the host (SplitRemoteUserinfo is where it ends), and a
// URL's host is what url.Parse read the authority as, less that userinfo.
//
// ok is false for a remote that names no host at all: a local path, an empty
// string, a URL with an empty authority, and the scp-like forms whose
// userinfo is the whole of what stands in front of the colon ("@:path",
// "git@:path") — every one of them answers this function's own question the
// same way, rather than leaving an empty host for a caller to notice.
//
// It is *also* false for an authority git will not clone from at all: an
// authority whose userinfo holds a second "@" (https://a@b@github.com/acme/app.git,
// which libcurl refuses outright as "Bad hostname" — it does not end the
// userinfo at any "@" of that spelling, the way SplitRemoteRaw's doc used to
// claim) and a URL whose scheme is not a scheme (SplitRemoteRaw, through
// SplitRemoteURL). Reading a host out of either would name a host git never
// connects to, and a caller that bounds what a remote may be is asking this for
// the host git *uses* (the review finding on this change).
//
// ok is *also* false for a URL with an authority this parser cannot read,
// because url.Parse refuses the string in a spelling git accepts — a backslash
// before the authority's "@" (https://github.com\@evil.example/acme/app.git,
// which libcurl reads as the userinfo "github.com\" and the host
// evil.example) and an escape it will not decode
// (https://evil.example/acme/app%ZZ.git) are two (the review finding on this
// change). The two answers are not the same question, and a caller that bounds
// what a remote may be must not read this one as "nothing to check": git
// accepts the spelling and connects to the host the authority names, so
// SplitRemoteRaw reads that shape instead.
//
// The scp-like form is recognised by a colon *before the first "/"* and not
// followed by "//" (which is a URL's authority, not a separator). It is not
// recognised by an "@": git makes the userinfo optional — it ssh's to
// github.com and asks for the same repository in "github.com:owner/name.git"
// and in "git@github.com:owner/name.git" alike — and keying on the "@" read
// both spellings as a URL whose *scheme* is "github.com", with no host and no
// path, so both halves of the run's remote binding skipped them (the review
// finding on this change). Both halves of the run's remote binding — the host
// kind here and the owner/name in internal/runner — ask this one question
// rather than each keeping its own copy of it.
func SplitRemote(remote string) (host, path string, ok bool) {
	if remote == "" {
		return "", "", false
	}
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		// The host is the authority as written, which is the one git connects to
		// and the one KindForHost compares: u.Hostname() drops the port, and a
		// port is exactly what makes the host no provider kind's (the review
		// finding on this change). The userinfo in front of it is cut by the one
		// helper SplitRemoteRaw and the runner read it with, and an authority
		// whose userinfo holds a second "@" is not a remote git clones at all.
		_, authority, _, shaped := SplitRemoteURL(remote)
		if !shaped {
			return "", "", false
		}
		userinfo, host, _ := SplitRemoteUserinfo(authority)
		if strings.Contains(userinfo, "@") {
			return "", "", false
		}
		// EscapedPath, not Path: url.Parse decodes "%2F" into a separator, and
		// a caller comparing the path must see the remote as git will send it.
		return host, u.EscapedPath(), true
	}
	if host, rest, ok := strings.Cut(remote, ":"); ok && host != "" &&
		!strings.Contains(host, "/") && !strings.HasPrefix(rest, "//") {
		if _, h, ok := SplitRemoteUserinfo(host); ok {
			// scp-like with userinfo: user@host:path. The userinfo is cut at the
			// authority's last "@", the same place SplitRemoteRaw and the
			// runner's remoteSplitUserinfo cut it: "a@b@github.com:acme/app.git"
			// is ssh's user "a@b" and host github.com, which a cut at the first
			// "@" read as the host "b@github.com" — a host no kind owns, so the
			// run refused a remote git connects to github.com with (the review
			// finding on this change). The userinfo may be the whole of it
			// ("@host:path", and "git@:path"), which names no host at all: ok is
			// false for that, as it is for a URL without an authority, rather
			// than true with an empty host a caller has to remember to refuse
			// for itself.
			host = h
		}
		if host == "" {
			return "", "", false
		}
		return host, rest, true
	}
	return "", "", false
}

// SplitRemoteRaw splits the URL shape SplitRemote refuses to read: a remote
// written with a scheme, "//" and an authority that url.Parse will not parse,
// which git accepts and connects to all the same. host is the authority's host
// as written — the text after its last "@", which is where git and curl end the
// userinfo of an authority they accept, so it may carry a port and it is never
// parsed further — and path is what follows the authority, up to a "?" or a
// "#". Both are as written, not decoded: the parse that would have decoded them
// is the one that failed.
//
// The last "@" is where the userinfo ends only for an authority with *one* of
// them. An authority with two (https://a@b@github.com/acme/app.git) is one git
// never connects to: libcurl refuses it outright, "URL rejected: Bad hostname",
// and no userinfo is ended at all — so this reader names no host for it either,
// rather than reporting github.com for a spelling whose clone fails (the review
// finding on this change; SplitRemoteUserinfo is the one cut, and it is the one
// SplitRemote's scp-like branch and the runner's remoteSplitUserinfo use too).
// A remote whose scheme does not read as a scheme is not this shape either
// (SplitRemoteURL): " https://github.com/acme/app.git", which a whitespace
// slip produces, is "Malformed input" to libcurl and never a clone. And an
// authority holding a control character — in its host or in its userinfo: a
// newline, a NUL, a tab, an ESC, a C1 control, a line separator — names no
// host here: git clones none of those (libcurl refuses the URL, and a
// credential url holding one "cannot be parsed"), and a caller that quotes what
// this returns must not be handed a string that is two lines long.
//
// ok is false for every remote SplitRemote already answers for (a readable URL,
// the scp-like form, a local path, an empty string) and for an authority with
// nothing in it, so a caller asks this only after SplitRemote said no and gets
// ok = true only for the shape whose parse failed. The one thing a caller may
// do with the host is compare it to the host it expects: a spelling this
// package could not read can only be the expected host when it *is* that text
// (https://github.com/acme/app%ZZ.git names github.com and git clones from
// there), and a refusal that quotes it quotes the host alone — everything
// before it, userinfo included, is dropped here rather than left for the
// caller to forget (the review finding on this change).
func SplitRemoteRaw(remote string) (host, path string, ok bool) {
	if remote == "" {
		return "", "", false
	}
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		return "", "", false // readable: SplitRemote's question, not this one
	}
	_, authority, path, shaped := SplitRemoteURL(remote)
	if !shaped {
		return "", "", false // not a URL with an authority: the scp-like form's shape, or no host at all
	}
	userinfo, host, _ := SplitRemoteUserinfo(authority)
	if strings.Contains(userinfo, "@") {
		return "", "", false // two "@": an authority libcurl refuses outright, which git never clones
	}
	if host == "" {
		return "", "", false // an authority with nothing in it: no host, SplitRemote's answer
	}
	if strings.ContainsFunc(authority, isControl) {
		// A control character is not a host name and not a userinfo either (a
		// newline, a NUL, a tab, an ESC, a C1 control, a line separator):
		// libcurl answers the whole URL "Malformed input to a URL function",
		// and a credential URL holding one is one git refuses to parse at all
		// (https://x\n@github.com/acme/web.git aborts with "credential url
		// cannot be parsed"), so no clone happens for any of them. This reader
		// exists to name the host git *uses*, so it names none for a spelling
		// git does not use — and a caller that quotes what it gets is not
		// handed a string that is two lines long or that carries a byte no log
		// can hold. The check is over the whole authority and not the host
		// alone because the userinfo is where an edited remote puts one
		// (the review finding on this change). A space is not control and is
		// still read: the display quotes it, and KindForHost refuses it as the
		// unknown host it is.
		return "", "", false
	}
	return host, path, true
}

// isControl reports whether r is a character no host or userinfo can hold and
// no one-line refusal may quote: the C0 controls (below the space) and DEL, the
// C1 controls (U+0080 to U+009F, NEL among them), and the Unicode line and
// paragraph separators (U+2028, U+2029) — the last three are not control
// characters by Go's unicode.IsControl, and each of them is a line break to a
// terminal or a log reader all the same. A URL parser refuses the first two
// outright ("Malformed input to a URL function" is libcurl's answer to each),
// and the separators are what a host name can never contain.
func isControl(r rune) bool {
	return r < ' ' || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0x2028 || r == 0x2029
}

// SplitRemoteURL separates a remote written as a URL — a scheme, ":" and "//",
// then an authority and a path — into those three parts as written, and ok says
// whether the remote is that shape at all. It is the one cut of that shape:
// SplitRemoteRaw reads the authority and the path off it, the runner's
// remoteEndpoint the scheme and the port (internal/runner/project.go), and the
// run's remote binding asks it whether a remote it could not otherwise read is
// written as a URL at all (remoteHost) — one reader, so the three cannot
// disagree about where a URL's authority ends.
//
// The scheme is checked against its RFC 3986 spelling — a letter, then letters,
// digits, "+", "-" and "." — because that is what makes the text in front of the
// colon a scheme rather than a slip: " https://github.com/acme/app.git" (a
// leading space) and "github.com/x://y" are not URLs, they are strings a URL
// parser refuses (libcurl answers both "Malformed input to a URL function") and
// git never clones. Reading the host out of one named a host git does not use.
//
// The authority ends at the first "/", "?" or "#", and the path is what follows
// it; the query and the fragment are not part of the path git clones
// (SplitRemote's EscapedPath excludes them too), and a "?" or "#" that opens
// the authority's remainder means the remote names no path at all. The path is
// as written, not decoded, for the reason SplitRemoteRaw is: the parse that
// would have decoded it is the one that failed.
func SplitRemoteURL(remote string) (scheme, authority, path string, ok bool) {
	scheme, rest, cut := strings.Cut(remote, ":")
	if !cut || !isScheme(scheme) || !strings.HasPrefix(rest, "//") {
		return "", "", "", false
	}
	authority = rest[2:]
	if i := strings.IndexAny(authority, "/?#"); i >= 0 {
		authority, path = authority[:i], authority[i:]
	}
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if !strings.HasPrefix(path, "/") {
		path = ""
	}
	return scheme, authority, path, true
}

// isScheme reports whether s reads as a URL scheme: RFC 3986's spelling,
// ALPHA *( ALPHA / DIGIT / "+" / "-" / "." ). A "scheme" that holds anything
// else — whitespace, a backslash, a "~" — is text in front of a colon, not a
// scheme, and a URL parser refuses the string it is in.
func isScheme(s string) bool {
	if s == "" || !isASCIIAlpha(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !isASCIIAlpha(c) && (c < '0' || c > '9') && c != '+' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

func isASCIIAlpha(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

// SplitRemoteUserinfo splits a remote authority at the "@" that ends its
// userinfo: userinfo is the text in front of the authority's last "@", host is
// what follows it, and ok says whether there was one at all. It is the one cut
// of a userinfo, asked by all three readers of a remote's shape: SplitRemote's
// URL and scp-like branches, SplitRemoteRaw, and internal/runner's
// remoteSplitUserinfo (for the display a refusal quotes).
//
// The last "@" is where git and curl end the userinfo of an authority they
// accept — a password may hold one, and https://user:a@b@host/repo.git is a
// credential whose halves a cut at the first "@" splits — and the *authority* is
// the caller's to isolate first: it ends at the first "/", so an "@" in the path
// is no authority's (remoteSplitUserinfo reads https://host/p@x that way).
//
// An authority with two "@" in its userinfo is not one git clones (libcurl
// refuses it as "Bad hostname"), so the userinfo this returns may hold one and
// the callers that read a host off the authority refuse that shape rather than
// report a host out of it (SplitRemote, SplitRemoteRaw).
func SplitRemoteUserinfo(authority string) (userinfo, host string, ok bool) {
	i := strings.LastIndex(authority, "@")
	if i < 0 {
		return "", authority, false
	}
	return authority[:i], authority[i+1:], true
}

// KindForHost names the provider hosting a git host: KindGitHub for github.com,
// KindBitbucket for bitbucket.org, and "" for anything else (another host, a
// host with a port, or nothing). It is the half of KindForURL that reads a host
// on its own, for the caller holding one SplitRemoteRaw read.
//
// The host is compared as written, which is how both readers return it: a host
// naming a port ("github.com:443") is "" here, because the port is not part of a
// host any provider kind owns and a caller that bounds what a remote may be
// refuses it rather than comparing the host alone (the review finding on this
// change — SplitRemote used to strip the port, so this never saw it).
func KindForHost(host string) string {
	switch strings.ToLower(host) {
	case "github.com":
		return KindGitHub
	case "bitbucket.org":
		return KindBitbucket
	}
	return ""
}

// KindForURL names the provider hosting a git remote URL: KindGitHub for
// github.com, KindBitbucket for bitbucket.org, and "" for anything else
// (a local path, another host, a host naming a port). A URL SplitRemote
// refuses reads as "" too, which for a caller that refuses an unknown host is
// the refusal it wants and for one that would admit "" is not: SplitRemoteRaw is
// what tells the two apart.
func KindForURL(remote string) string {
	host, _, _ := SplitRemote(remote)
	return KindForHost(host)
}

// TrimRepoPath trims the punctuation a remote path carries for being part of a
// URL off the path SplitRemote or SplitRemoteRaw returned: exactly one leading
// and one trailing "/", never more. "https://github.com/acme/app.git/" is the
// same remote as the path without the last "/", and the leading one is the
// URL's own separator. Anything else is left exactly as written: an empty
// segment (https://github.com//acme/app.git) is not punctuation, and it is
// what the launcher's rule (internal/infra's repoURL) and the run's admission
// (internal/runner's remoteRepoPath) both compare their repository against —
// one function, so the two cannot read one remote two ways, which they did
// (the review finding on this change): the launcher trimmed every leading and
// trailing "/" and accepted a URL the admission's one-per-side trim refused,
// deploying a job that refused every run.
func TrimRepoPath(p string) string {
	return strings.TrimSuffix(strings.TrimPrefix(p, "/"), "/")
}

// SplitRepo splits "owner/name" into its two parts.
func SplitRepo(repo string) (owner, name string, ok bool) {
	owner, name, ok = strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return owner, name, true
}

// ValidRepoPath reports whether p is one clean relative file path inside
// a repository: non-empty, relative, with no "." or ".." segment, already
// clean (no "//" and no trailing "/") and no control character. It is the
// shape a ContentsReader accepts and the shape an adapter may put into an
// API path, stated once so a caller and an adapter cannot disagree about
// what one is — a path that reached the API unrefused would be resolved by
// the server against paths the caller never named.
//
// It is deliberately less than the grammar a config key is held to (no
// length bound, no formatting-character rule): those are the rules of the
// key that carries the path (config.ValidateCatalog), and this is the bound
// of the read itself.
func ValidRepoPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return !strings.ContainsFunc(p, unicode.IsControl)
}
