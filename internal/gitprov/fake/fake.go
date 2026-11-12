// Package fake is an in-memory gitprov.Provider, optionally persisted to a
// JSON file so separate processes (tests, `fugaro exec --provider fake`) share it.
package fake

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
)

// PRState is a pull request and everything done to it.
type PRState struct {
	gitprov.PR
	Spec gitprov.PRSpec `json:"spec"`
	// Comments are the comments Fugaro posted, in order.
	Comments []string `json:"comments"`
	// CommentTimes[i] is when Comments[i] was posted; a comment without
	// one (a state file from before times were kept) sorts first.
	CommentTimes []time.Time `json:"comment_times,omitempty"`
	// State is the PR's state; empty means open.
	State gitprov.PRState `json:"state,omitempty"`
	// Source is the source repository; empty means the provider's Repo.
	Source string `json:"source,omitempty"`
	// AuthorID is the PR author's account ID; empty means the provider's
	// SelfID (Fugaro opened it).
	AuthorID string `json:"author_id,omitempty"`
	// AuthorLogin is the PR author's login, for a test that checks the
	// trust-refusal message's text; empty means "fugaro" (a plausible
	// bot login, when AuthorID also defaults to SelfID) or, when AuthorID
	// is set without a login, simply "".
	AuthorLogin string `json:"author_login,omitempty"`
	// Collaborator is review mode's trust signal (gitprov.PRHead.Collaborator):
	// a test sets it true for a PR whose author should be trusted by
	// association alone, without relying on AuthorID defaulting to SelfID
	// or being listed in followup.trusted.
	Collaborator bool `json:"collaborator,omitempty"`
	// Foreign are comments a test injects as if people wrote them.
	Foreign []gitprov.Comment `json:"foreign,omitempty"`
	// Head, when set, overrides the head PullRequest reports, as when
	// someone pushed to the branch.
	Head string `json:"head,omitempty"`
	// Title and Body are the PR's current text: the spec's at creation,
	// then whatever UpdatePR set. An old state file without them reads
	// as the spec's.
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
	// Reviewers and Labels are what the PR actually carries. A draft
	// never gets any; they arrive at creation of a ready PR or through
	// ApplyReady.
	Reviewers []string `json:"reviewers,omitempty"`
	Labels    []string `json:"labels,omitempty"`
}

// Call is one entry of the call log, which holds the calls that write
// (reads leave the state file untouched): Op is the Provider method
// (EnsurePR, UpdatePR, ApplyReady, Comment, MarkReady, Merge), PR the PR number involved
// (0 for an EnsurePR that finds or creates by branch, until it knows),
// Detail what was asked, such as "draft=true" or "title,body".
type Call struct {
	Op     string `json:"op"`
	PR     int    `json:"pr,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// State is the fake provider's data.
type State struct {
	PRs []PRState `json:"prs"`
	// Public makes the repository public.
	Public bool `json:"public,omitempty"`
	// Calls is every call made, in order (failed injected ones included),
	// so tests can assert what the runner did and in which order.
	Calls []Call `json:"calls,omitempty"`
	// Files is the repository's file tree at its default branch (gitprov.
	// ContentsReader, PIC3): a path relative to the repository's root ->
	// the file's bytes. Branches is the same tree per named branch, for a
	// read that names one; a branch not here is a ref this repository has
	// nothing at (treeOf), which is a read that fails rather than one that
	// quietly answers the default branch's files.
	Files    map[string]string            `json:"files,omitempty"`
	Branches map[string]map[string]string `json:"branches,omitempty"`
	// Commits is the commit a ref names (gitprov.CommitReader): a branch
	// name, or "" for the default branch, -> a full commit. A ref it does
	// not name resolves to a commit derived from the ref (commitOf), so a
	// test that does not care which commit a branch is at writes no entry
	// and every read still happens at a commit.
	Commits map[string]string `json:"commits,omitempty"`
	// Trees is the tree a commit holds, for a test that needs one ref's
	// read to answer a different tree than another's: the branch's tree in
	// Branches and the resolved commit's in Trees. A commit not here sees
	// the tree of the branch that resolves to it — a read at the commit a
	// branch is at is a read of that branch — so a test that pins no tree
	// and reads at a resolved commit reads the tree of the ref it came from.
	Trees map[string]map[string]string `json:"trees,omitempty"`
	// Checks is the checks reported at a commit (gitprov.Provider.Checks):
	// a commit SHA -> what the host reports there. A commit this map does
	// not name has no checks at all, which is not the same as checks that
	// passed: the caller's ChecksState reads an empty set as CIUnknown, and
	// a test that wants a green commit must say which checks said so.
	Checks map[string][]gitprov.CheckRun `json:"checks,omitempty"`
}

// Provider is a fake git host.
type Provider struct {
	Path string // optional; when set, State is loaded and saved on every call
	// Auth, when set, supplies GitAuth's result for each minValid asked
	// for. When nil, GitAuth returns no credentials (local remotes need none).
	Auth func(minValid time.Duration) gitprov.GitAuth
	// FailEnsure makes that many EnsurePR calls fail before any succeeds,
	// without creating or touching the PR: it models a request that never
	// reached the provider at all.
	FailEnsure int
	// FailEnsureAfterCreate makes that many EnsurePR calls create or
	// update the PR as usual and still report failure: it models a
	// request the provider actually applied, whose success response was
	// then lost (a dropped connection, a 5xx after the write landed). The
	// PR created or found this way must survive in State.
	FailEnsureAfterCreate int
	// PartialEnsure, when set, makes EnsurePR create or update the PR
	// with its Draft state forced to PartialEnsureDraft — regardless of
	// spec.Draft — and report a *gitprov.PartialError wrapping this
	// error, simulating a provider that could only partly apply the
	// requested PR settings.
	PartialEnsure      error
	PartialEnsureDraft bool
	// SelfID is the account ID Fugaro posts as.
	SelfID string
	// Repo is the repository the fake hosts ("owner/name"), reported as a
	// PR's SourceRepo unless the PR names another.
	Repo string
	// Remote is a local bare repository: PullRequest reports the tip of
	// refs/heads/<branch> there as the PR's head, unless PRState.Head
	// overrides it. Empty, or a branch Remote lacks, means no head.
	Remote string
	// FailComments, FailPullRequest and FailRepository make that many
	// calls of each fail.
	FailComments    int
	FailPullRequest int
	FailRepository  int
	// FailPullRequestByBranch makes that many PullRequestByBranch calls fail.
	FailPullRequestByBranch int
	// FailUpdatePR and FailApplyReady make that many calls fail without
	// changing anything.
	FailUpdatePR   int
	FailApplyReady int
	// RejectReviewers names reviewers the host does not know: ApplyReady
	// (and a ready creation) still applies the others and the labels, and
	// reports a *gitprov.PartialError.
	RejectReviewers []string
	// NoDrafts models a host that refuses real drafts: a draft is created
	// as a normal PR whose title carries gitprov.DraftPrefix, reported
	// with Draft and DraftFallback set.
	NoDrafts bool
	mu       sync.Mutex
	State    State
}

// gitWaitDelay bounds how long a git subprocess's leaked children may hold
// its output open after it exits.
const gitWaitDelay = 5 * time.Second

// Load reads a persisted state file; a missing file is an empty state.
func Load(path string) (State, error) {
	var st State
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(data, &st)
}

func (p *Provider) load() error {
	if p.Path == "" {
		return nil
	}
	st, err := Load(p.Path)
	p.State = st
	return err
}

func (p *Provider) save() error {
	if p.Path == "" {
		return nil
	}
	data, err := json.MarshalIndent(p.State, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.Path, data, 0o644)
}

// Snapshot is a deep copy of the state taken under the provider's lock,
// for a test that reads it while the runner may be calling the provider
// from another goroutine (a checkpoint). Every slice of every PR and of
// the call log is cloned, so the caller may read or modify the copy
// without touching the provider.
func (p *Provider) Snapshot() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.State // with Path set, every call has already saved and loaded it
	st.PRs = make([]PRState, len(p.State.PRs))
	for i, pr := range p.State.PRs {
		pr.Spec.Labels = slices.Clone(pr.Spec.Labels)
		pr.Spec.Reviewers = slices.Clone(pr.Spec.Reviewers)
		pr.Comments = slices.Clone(pr.Comments)
		pr.CommentTimes = slices.Clone(pr.CommentTimes)
		pr.Foreign = slices.Clone(pr.Foreign)
		pr.Reviewers = slices.Clone(pr.Reviewers)
		pr.Labels = slices.Clone(pr.Labels)
		st.PRs[i] = pr
	}
	st.Calls = slices.Clone(p.State.Calls)
	if p.State.Checks != nil {
		st.Checks = make(map[string][]gitprov.CheckRun, len(p.State.Checks))
		for sha, runs := range p.State.Checks {
			st.Checks[sha] = slices.Clone(runs)
		}
	}
	return st
}

// EnsurePR implements gitprov.Provider.
func (p *Provider) EnsurePR(_ context.Context, spec gitprov.PRSpec) (gitprov.PR, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return gitprov.PR{}, err
	}
	p.logLocked(Call{Op: "EnsurePR", PR: spec.Number, Detail: fmt.Sprintf("draft=%t", spec.Draft)})
	defer func() { _ = p.save() }()
	if p.FailEnsureAfterCreate > 0 {
		p.FailEnsureAfterCreate--
		pr, err := p.ensureLocked(spec.Branch, spec.Draft, spec)
		if err != nil {
			return pr, err
		}
		return pr, errors.New("fake provider: injected EnsurePR failure after creating the PR")
	}
	if p.FailEnsure > 0 {
		p.FailEnsure--
		return gitprov.PR{}, errors.New("fake provider: injected EnsurePR failure")
	}
	if p.PartialEnsure != nil {
		pr, err := p.ensureLocked(spec.Branch, p.PartialEnsureDraft, spec)
		if err != nil {
			return pr, err
		}
		return pr, &gitprov.PartialError{Err: p.PartialEnsure}
	}
	return p.ensureLocked(spec.Branch, spec.Draft, spec)
}

// ensureLocked finds the PR for branch and updates its draft state, or
// creates it, saving the state either way; with spec.Number set it only
// updates that PR. Callers hold p.mu and have already called p.load.
func (p *Provider) ensureLocked(branch string, draft bool, spec gitprov.PRSpec) (gitprov.PR, error) {
	if spec.Number != 0 {
		st, err := p.findLocked(spec.Number)
		if err != nil {
			return gitprov.PR{}, err
		}
		if state := stateOf(st); state != gitprov.PROpen {
			return st.PR, fmt.Errorf("PR #%d is %s: %w", st.Number, state, gitprov.ErrPRNotOpen)
		}
		if st.Spec.Branch != branch {
			return st.PR, fmt.Errorf("PR #%d is on %s, not %s: %w", st.Number, st.Spec.Branch, branch, gitprov.ErrPRNotOpen)
		}
		p.setDraftLocked(st, draft)
		return st.PR, p.save()
	}
	for i := range p.State.PRs {
		if p.State.PRs[i].Spec.Branch == branch {
			p.setDraftLocked(&p.State.PRs[i], draft)
			return p.State.PRs[i].PR, p.save()
		}
	}
	n := len(p.State.PRs) + 1
	pr := gitprov.PR{Number: n, URL: fmt.Sprintf("https://example.invalid/pr/%d", n), Draft: draft}
	st := PRState{PR: pr, Spec: spec, Title: spec.Title, Body: spec.Body}
	if draft && p.NoDrafts {
		st.Title, st.Draft, st.DraftFallback = gitprov.DraftTitle(spec.Title, true), true, true
	}
	var perr error
	if !draft {
		// Only a PR created ready carries reviewers and labels.
		perr = p.applyLocked(&st, spec.Reviewers, spec.Labels)
	}
	p.State.PRs = append(p.State.PRs, st)
	if err := p.save(); err != nil {
		return st.PR, err
	}
	return st.PR, perr
}

// setDraftLocked moves st to the wanted draft state the way a host does:
// a real flag, or the title prefix when NoDrafts. Reviewers are not
// touched either way.
func (p *Provider) setDraftLocked(st *PRState, draft bool) {
	title := p.titleOf(st)
	switch {
	case p.NoDrafts:
		st.Title, st.Draft, st.DraftFallback = gitprov.DraftTitle(title, draft), draft, draft
	default:
		st.Title, st.Draft, st.DraftFallback = gitprov.DraftTitle(title, false), draft, false
	}
}

func (p *Provider) titleOf(st *PRState) string {
	if st.Title != "" {
		return st.Title
	}
	return st.Spec.Title
}

func (p *Provider) logLocked(c Call) {
	p.State.Calls = append(p.State.Calls, c)
}

// applyLocked adds labels and reviewers st lacks, except RejectReviewers,
// which gives a *gitprov.PartialError.
func (p *Provider) applyLocked(st *PRState, reviewers, labels []string) error {
	var rejected []string
	for _, r := range reviewers {
		switch {
		case slices.Contains(p.RejectReviewers, r):
			rejected = append(rejected, r)
		case !slices.Contains(st.Reviewers, r):
			st.Reviewers = append(st.Reviewers, r)
		}
	}
	for _, l := range labels {
		if !slices.Contains(st.Labels, l) {
			st.Labels = append(st.Labels, l)
		}
	}
	if len(rejected) > 0 {
		return &gitprov.PartialError{Err: fmt.Errorf("fake provider: reviewers %s were rejected", strings.Join(rejected, ", "))}
	}
	return nil
}

// findLocked returns the PR numbered number. Callers hold p.mu.
func (p *Provider) findLocked(number int) (*PRState, error) {
	for i := range p.State.PRs {
		if p.State.PRs[i].Number == number {
			return &p.State.PRs[i], nil
		}
	}
	return nil, fmt.Errorf("fake provider: no PR #%d", number)
}

// stateOf is st's state, open when unset (state files from before PR
// states were kept).
func stateOf(st *PRState) gitprov.PRState {
	if st.State == "" {
		return gitprov.PROpen
	}
	return st.State
}

// Comment implements gitprov.Provider.
func (p *Provider) Comment(_ context.Context, pr gitprov.PR, body string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return err
	}
	p.logLocked(Call{Op: "Comment", PR: pr.Number})
	st, err := p.findLocked(pr.Number)
	if err != nil {
		return err
	}
	// Keep CommentTimes aligned with Comments: earlier comments without a
	// time get the zero time, which sorts first.
	for len(st.CommentTimes) < len(st.Comments) {
		st.CommentTimes = append(st.CommentTimes, time.Time{})
	}
	st.Comments = append(st.Comments, body)
	st.CommentTimes = append(st.CommentTimes[:len(st.Comments)-1], time.Now().UTC())
	return p.save()
}

// Repository implements gitprov.Provider.
func (p *Provider) Repository(context.Context) (gitprov.RepoInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return gitprov.RepoInfo{}, err
	}
	if p.FailRepository > 0 {
		p.FailRepository--
		return gitprov.RepoInfo{}, errors.New("fake provider: injected Repository failure")
	}
	return gitprov.RepoInfo{Private: !p.State.Public}, nil
}

// Contents implements gitprov.ContentsReader: the file at path, at ref, out
// of the tree that ref sees (treeOf: a branch's own tree, a commit's, or the
// default branch's). A file the tree does not hold is a failure naming what
// was read, exactly as the real adapter's 404 is: a caller that ignored the
// ref or the path could not quietly get the definition it meant to (PIC3).
func (p *Provider) Contents(_ context.Context, path, ref string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return nil, err
	}
	data, ok := p.treeOf(ref)[path]
	if !ok {
		return nil, fmt.Errorf("fake provider: %s holds no %s", p.Repo, path)
	}
	return []byte(data), nil
}

// Commit implements gitprov.CommitReader: the commit ref names. A ref the
// state pins (State.Commits) is answered as it is, a ref that is already a
// full commit is its own commit, and anything else resolves to commitOf's
// derived commit — so two refs are two commits, and the same ref is the same
// commit on every call, which is what makes a read at it reproducible.
func (p *Provider) Commit(_ context.Context, ref string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return "", err
	}
	return p.commitOf(ref), nil
}

// commitOf is the commit a ref names: the pinned one, the ref itself when it
// is already a full commit, and otherwise a hash of the ref.
func (p *Provider) commitOf(ref string) string {
	if sha := p.State.Commits[ref]; sha != "" {
		return sha
	}
	if gitops.ValidCommitSHA(ref) {
		return ref
	}
	sum := sha256.Sum256([]byte("fake provider commit: " + ref))
	return hex.EncodeToString(sum[:])[:40]
}

// treeOf is the tree a read at ref sees: the default branch's own tree for a
// read that names no ref, Trees[ref] for a commit whose tree the test pinned,
// Branches[ref] for a branch, and the tree of the branch that resolves to ref
// for a read at a resolved commit (a read at the commit a branch is at is a
// read of that branch).
//
// A ref none of those name is the default branch's tree when the state models
// no branch at all — a test that pins only Files is a repository with one
// branch, and reads them at whatever ref it names — and nil when the state
// does model branches: there, a read at a branch that does not exist, or at a
// commit no branch is at, fails as the host's 404 does rather than quietly
// answering the default branch's files.
//
// So a test that needs two refs to answer different trees pins the commit in
// Commits and the tree it holds in Trees; one that pins only Files writes
// nothing else and reads them whichever ref it names.
func (p *Provider) treeOf(ref string) map[string]string {
	if ref == "" {
		return p.State.Files
	}
	if t, ok := p.State.Trees[ref]; ok {
		return t
	}
	if t, ok := p.State.Branches[ref]; ok {
		return t
	}
	for branch, t := range p.State.Branches {
		if p.commitOf(branch) == ref {
			return t
		}
	}
	if len(p.State.Branches) == 0 {
		return p.State.Files
	}
	return nil
}

// Tree implements gitprov.TreeReader: the files under path, at ref, out of the
// tree that ref sees (treeOf), each with the path it has relative to path and
// its bytes. The limit is checked against the whole tree before anything is
// handed back, as a host's listing is read before its content is, and the
// entries are sorted by path so a caller's identity over them does not depend
// on the order a map iterates in.
func (p *Provider) Tree(_ context.Context, path, ref string, limit gitprov.TreeLimit) ([]gitprov.TreeFile, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return nil, err
	}
	prefix := ""
	if path != "" && path != "." {
		prefix = strings.TrimSuffix(path, "/") + "/"
	}
	var files []gitprov.TreeFile
	var total int64
	for name, data := range p.treeOf(ref) {
		rel, ok := strings.CutPrefix(name, prefix)
		if !ok || rel == "" {
			continue
		}
		files = append(files, gitprov.TreeFile{Path: rel, Data: []byte(data)})
		total += int64(len(data))
	}
	if len(files) > limit.MaxFiles || total > limit.MaxBytes {
		return nil, fmt.Errorf("fake provider: %s holds %d files under %s, %d bytes: %w", p.Repo, len(files), path, total, gitprov.ErrTreeTooLarge)
	}
	slices.SortFunc(files, func(a, b gitprov.TreeFile) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// PullRequest implements gitprov.Provider. The head is PRState.Head when
// set, else the tip of the PR's branch in Remote, else empty.
func (p *Provider) PullRequest(ctx context.Context, number int) (gitprov.PRInfo, error) {
	info, head, err := p.pullRequest(number)
	if err != nil || info.HeadSHA != "" || p.Remote == "" {
		return info, err
	}
	// git runs outside the lock: the branch is read from the PR's own spec.
	tip, err := p.branchTip(ctx, head)
	if err != nil {
		return gitprov.PRInfo{}, fmt.Errorf("fake provider: PR #%d's branch %s is not in %s: %w", number, head, p.Remote, err)
	}
	info.HeadSHA = tip
	return info, nil
}

// PullRequestByBranch implements gitprov.PRBranchReader: the pull request
// whose spec's branch is branch, whatever its state, the latest one when a
// branch was opened twice. found is false when no pull request names it.
func (p *Provider) PullRequestByBranch(_ context.Context, branch string) (gitprov.PRInfo, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return gitprov.PRInfo{}, false, err
	}
	if p.FailPullRequestByBranch > 0 {
		p.FailPullRequestByBranch--
		return gitprov.PRInfo{}, false, errors.New("fake provider: injected PullRequestByBranch failure")
	}
	number := 0
	for _, st := range p.State.PRs {
		if st.Spec.Branch == branch && st.Number >= number {
			number = st.Number
		}
	}
	if number == 0 {
		return gitprov.PRInfo{}, false, nil
	}
	info, _, err := p.pullRequestLocked(number)
	if err != nil {
		return gitprov.PRInfo{}, false, err
	}
	return info, true, nil
}

// pullRequestLocked is pullRequest's body with the lock already held.
func (p *Provider) pullRequestLocked(number int) (info gitprov.PRInfo, branch string, err error) {
	st, err := p.findLocked(number)
	if err != nil {
		return gitprov.PRInfo{}, "", err
	}
	return gitprov.PRInfo{
		Number:       st.Number,
		URL:          st.URL,
		State:        stateOf(st),
		Draft:        st.Draft,
		Title:        p.titleOf(st),
		Body:         p.bodyOf(st),
		AuthorID:     cmp.Or(st.AuthorID, p.SelfID),
		Base:         st.Spec.Base,
		SourceBranch: st.Spec.Branch,
		SourceRepo:   cmp.Or(st.Source, p.Repo),
		HeadSHA:      st.Head,
	}, st.Spec.Branch, nil
}

// pullRequest reads PR number from the state; branch is its source branch.
func (p *Provider) pullRequest(number int) (info gitprov.PRInfo, branch string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return gitprov.PRInfo{}, "", err
	}
	if p.FailPullRequest > 0 {
		p.FailPullRequest--
		return gitprov.PRInfo{}, "", errors.New("fake provider: injected PullRequest failure")
	}
	return p.pullRequestLocked(number)
}

// Comments implements gitprov.Provider: the injected foreign comments and
// the ones Fugaro posted, merged oldest first. Fugaro's are general
// comments by SelfID, marked Self; the fake always knows who it is.
func (p *Provider) Comments(_ context.Context, number int) ([]gitprov.Comment, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return nil, err
	}
	if p.FailComments > 0 {
		p.FailComments--
		return nil, errors.New("fake provider: injected Comments failure")
	}
	st, err := p.findLocked(number)
	if err != nil {
		return nil, err
	}
	out := make([]gitprov.Comment, 0, len(st.Comments)+len(st.Foreign))
	for i, body := range st.Comments {
		var at time.Time
		if i < len(st.CommentTimes) {
			at = st.CommentTimes[i]
		}
		out = append(out, gitprov.Comment{
			ID: fmt.Sprintf("fugaro-%d", i+1), Kind: gitprov.CommentGeneral, Author: "fugaro", AuthorID: p.SelfID,
			Collaborator: true, Self: true, SelfKnown: true, Body: body, CreatedAt: at,
			URL: fmt.Sprintf("%s#fugaro-%d", st.URL, i+1),
		})
	}
	for _, c := range st.Foreign {
		c.SelfKnown = true
		out = append(out, c)
	}
	slices.SortStableFunc(out, func(a, b gitprov.Comment) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

// FetchPRHead implements gitprov.Provider: the same lookup PullRequest uses
// (PRState.Head when set, else the tip of the PR's branch in Remote), plus
// the fork test injects (PRState.Source set to something other than Repo),
// and the author-trust fields (PRState.AuthorID/AuthorLogin/Collaborator)
// a trust test sets. Self is AuthorID == SelfID: the same default
// PullRequest/pullRequest already give AuthorID when a test leaves it
// unset (cmp.Or(st.AuthorID, p.SelfID)), so an existing test that never
// mentions trust at all keeps reviewing its own (self) PR exactly as
// before. Self is forced false whenever Fork is true (PRHead's own
// documented invariant, gitprov.go): Fugaro never opens a pull request
// from a fork, so a fork PR is never self regardless of what its author
// fields happen to be set to.
func (p *Provider) FetchPRHead(ctx context.Context, number int) (gitprov.PRHead, error) {
	info, err := p.PullRequest(ctx, number)
	if err != nil {
		return gitprov.PRHead{}, err
	}
	p.mu.Lock()
	st, ferr := p.findLocked(number)
	p.mu.Unlock()
	if ferr != nil {
		return gitprov.PRHead{}, ferr
	}
	fork := !strings.EqualFold(info.SourceRepo, p.Repo)
	// st.AuthorID unset is PRState's own documented default ("Fugaro
	// opened it"), regardless of whether the harness bothered to set a
	// non-empty SelfID at all (most don't): that must count as self on
	// its own, not just when it happens to equal a configured SelfID.
	self := (st.AuthorID == "" || (p.SelfID != "" && st.AuthorID == p.SelfID)) && !fork
	login := st.AuthorLogin
	if login == "" && self {
		login = "fugaro"
	}
	return gitprov.PRHead{
		SHA: info.HeadSHA, Base: info.Base, Fork: fork, Open: info.State == gitprov.PROpen,
		AuthorID: info.AuthorID, AuthorLogin: login, Collaborator: st.Collaborator,
		Self: self, SourceRepo: info.SourceRepo,
	}, nil
}

// GitAuth implements gitprov.Provider.
func (p *Provider) GitAuth(_ context.Context, minValid time.Duration) (gitprov.GitAuth, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Auth == nil {
		return gitprov.GitAuth{}, nil
	}
	return p.Auth(minValid), nil
}

func (p *Provider) bodyOf(st *PRState) string {
	if st.Body != "" {
		return st.Body
	}
	return st.Spec.Body
}

// UpdatePR implements gitprov.Provider.
func (p *Provider) UpdatePR(_ context.Context, number int, u gitprov.PRUpdate) (gitprov.PR, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return gitprov.PR{}, err
	}
	var what []string
	if u.Title != nil {
		what = append(what, "title")
	}
	if u.Body != nil {
		what = append(what, "body")
	}
	if u.Draft != nil {
		what = append(what, fmt.Sprintf("draft=%t", *u.Draft))
	}
	p.logLocked(Call{Op: "UpdatePR", PR: number, Detail: strings.Join(what, ",")})
	if p.FailUpdatePR > 0 {
		p.FailUpdatePR--
		_ = p.save()
		return gitprov.PR{}, errors.New("fake provider: injected UpdatePR failure")
	}
	st, err := p.findLocked(number)
	if err != nil {
		return gitprov.PR{}, err
	}
	if state := stateOf(st); state != gitprov.PROpen {
		_ = p.save()
		return st.PR, fmt.Errorf("PR #%d is %s: %w", st.Number, state, gitprov.ErrPRNotOpen)
	}
	if u.Title != nil {
		title := *u.Title
		if st.DraftFallback && (u.Draft == nil || *u.Draft) {
			title = gitprov.DraftTitle(title, true)
		}
		st.Title = title
	}
	if u.Body != nil {
		st.Body = *u.Body
	}
	if u.Draft != nil {
		p.setDraftLocked(st, *u.Draft)
	}
	return st.PR, p.save()
}

// ApplyReady implements gitprov.Provider. Unlike a real host, the fake
// refuses a PR that is still a draft: the runner must flip it first, and
// a test should notice when it does not.
func (p *Provider) ApplyReady(_ context.Context, number int, reviewers, labels []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return err
	}
	p.logLocked(Call{Op: "ApplyReady", PR: number, Detail: fmt.Sprintf("reviewers=%s labels=%s", strings.Join(reviewers, ","), strings.Join(labels, ","))})
	if p.FailApplyReady > 0 {
		p.FailApplyReady--
		_ = p.save()
		return errors.New("fake provider: injected ApplyReady failure")
	}
	st, err := p.findLocked(number)
	if err != nil {
		return err
	}
	if state := stateOf(st); state != gitprov.PROpen {
		_ = p.save()
		return fmt.Errorf("PR #%d is %s: %w", st.Number, state, gitprov.ErrPRNotOpen)
	}
	if st.Draft {
		_ = p.save()
		return fmt.Errorf("fake provider: ApplyReady on PR #%d, which is still a draft", number)
	}
	perr := p.applyLocked(st, reviewers, labels)
	if err := p.save(); err != nil {
		return err
	}
	return perr
}

// MergeMintExemption implements gitprov.MintExempt. A double stands in for a
// host, and it holds no credential for one: there is no mint for it to take a
// token from and nothing for a merge token to be bound to, so it says it is
// exempt rather than leaving the same question the Bitbucket adapter answers
// open here. A test that needs the mint's refusal wires the real provider.
func (p *Provider) MergeMintExemption() error { return gitprov.ErrMintNotApplicable }

// MarkReady implements gitprov.Provider: it refuses a PR whose head is not
// the commit the call named (TestFakeMarkReadyRefusesOnHeadChange), and only
// then clears the draft state. A state that records no head is a PR no
// head-bound call can be checked against, so it refuses too: a mock that
// answered "yes, that head" for a head nobody recorded would let a test pass
// a gate the real provider never would.
func (p *Provider) MarkReady(ctx context.Context, number int, expectedHeadSHA string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return err
	}
	p.logLocked(Call{Op: "MarkReady", PR: number, Detail: "sha=" + expectedHeadSHA})
	defer func() { _ = p.save() }()
	st, err := p.findLocked(number)
	if err != nil {
		return err
	}
	if state := stateOf(st); state != gitprov.PROpen {
		return fmt.Errorf("PR #%d is %s: %w", st.Number, state, gitprov.ErrPRNotOpen)
	}
	if err := p.checkHeadLocked(ctx, st, expectedHeadSHA); err != nil {
		return err
	}
	p.setDraftLocked(st, false)
	return nil
}

// Merge implements gitprov.Provider: the head is checked the same way and the
// PR becomes merged only when it is the one asked for. A draft is refused, for
// ApplyReady's reason — a host will not merge one, so a test must notice a
// merge that skipped readying — and there is no bypass to model: the fake
// merges what a real host would merge at the head it was given. There is no
// credential either: the gate is the caller's, and the mint that would refuse
// an unbound merge is the real adapter's (E16, D2049).
func (p *Provider) Merge(ctx context.Context, number int, expectedHeadSHA string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return err
	}
	p.logLocked(Call{Op: "Merge", PR: number, Detail: "sha=" + expectedHeadSHA})
	defer func() { _ = p.save() }()
	st, err := p.findLocked(number)
	if err != nil {
		return err
	}
	if state := stateOf(st); state != gitprov.PROpen {
		return fmt.Errorf("PR #%d is %s: %w", st.Number, state, gitprov.ErrPRNotOpen)
	}
	if err := p.checkHeadLocked(ctx, st, expectedHeadSHA); err != nil {
		return err
	}
	if st.Draft {
		// GitHub refuses to merge a draft, and the ladder's order is
		// MarkReady and then Merge: a test that merges without readying would
		// be passing a sequence the real host rejects.
		return fmt.Errorf("fake provider: Merge on PR #%d, which is still a draft", number)
	}
	st.State = gitprov.PRMerged
	return nil
}

// Checks implements gitprov.Provider: what State.Checks reports at sha, and
// nothing for a commit it does not name. The slice is a copy, so a caller
// that appends to it does not change the state.
func (p *Provider) Checks(_ context.Context, number int, sha string) ([]gitprov.CheckRun, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.load(); err != nil {
		return nil, err
	}
	if _, err := p.findLocked(number); err != nil {
		return nil, err
	}
	return slices.Clone(p.State.Checks[sha]), nil
}

// checkHeadLocked refuses a head-bound call that the state cannot bind: no
// head at all, or one that is not the commit the caller named.
//
// The head it binds to is the one PullRequest reports (Head's own doc): the
// state's recorded Head when one is set, and otherwise the tip of the PR's
// branch in Remote. Reading it any other way would have the fake refuse the
// very head its own read had just handed the caller — a ladder test could not
// then ready or merge the pull request it had just read — and a double that
// refuses a call production accepts is worse than none.
//
// git runs while the lock is held, unlike PullRequest's read: what this binds
// to must be the head the state is at when the write below happens, which is
// the atomicity the real host has and this mutex stands in for.
func (p *Provider) checkHeadLocked(ctx context.Context, st *PRState, expected string) error {
	head := st.Head
	if head == "" {
		tip, err := p.branchTip(ctx, st.Spec.Branch)
		if err != nil {
			return fmt.Errorf("fake provider: PR #%d's branch %s is not in %s: %w", st.Number, st.Spec.Branch, p.Remote, err)
		}
		head = tip
	}
	if head == "" {
		return fmt.Errorf("fake provider: PR #%d records no head, so a head-bound call cannot be checked", st.Number)
	}
	if err := gitprov.CheckExpectedHead(head, expected); err != nil {
		return fmt.Errorf("fake provider: PR #%d: %w", st.Number, err)
	}
	return nil
}

// branchTip is the commit refs/heads/branch names in this provider's Remote,
// "" when there is no remote or the branch is gone (a merge closed it), and
// the git failure otherwise. It does not take the lock: PullRequest reads
// outside it, checkHeadLocked inside.
func (p *Provider) branchTip(ctx context.Context, branch string) (string, error) {
	if p.Remote == "" || branch == "" {
		return "", nil
	}
	cmd := exec.CommandContext(ctx, "git", "--git-dir", p.Remote, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
	cmd.WaitDelay = gitWaitDelay
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(bytes.TrimSpace(out)) == 0 {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
