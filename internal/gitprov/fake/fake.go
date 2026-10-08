// Package fake is an in-memory gitprov.Provider, optionally persisted to a
// JSON file so separate processes (tests, `fugaro exec --provider fake`) share it.
package fake

import (
	"bytes"
	"cmp"
	"context"
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
// (EnsurePR, UpdatePR, ApplyReady, Comment), PR the PR number involved
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

// Snapshot is a copy of the state taken under the provider's lock, for a
// test that reads it while the runner may be calling the provider from
// another goroutine (a checkpoint). The PRs and calls are copied; the
// slices inside each PR are not, and must only be read.
func (p *Provider) Snapshot() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.State // with Path set, every call has already saved and loaded it
	st.PRs = slices.Clone(p.State.PRs)
	st.Calls = slices.Clone(p.State.Calls)
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

// PullRequest implements gitprov.Provider. The head is PRState.Head when
// set, else the tip of the PR's branch in Remote, else empty.
func (p *Provider) PullRequest(ctx context.Context, number int) (gitprov.PRInfo, error) {
	info, head, err := p.pullRequest(number)
	if err != nil || info.HeadSHA != "" || p.Remote == "" {
		return info, err
	}
	// git runs outside the lock: the branch is read from the PR's own spec.
	cmd := exec.CommandContext(ctx, "git", "--git-dir", p.Remote, "rev-parse", "--verify", "--quiet", "refs/heads/"+head+"^{commit}")
	cmd.WaitDelay = gitWaitDelay
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(bytes.TrimSpace(out)) == 0 {
		// --verify --quiet: the branch is gone (a merge closed it). A real
		// host still shows the PR, so the fake does too, without a head.
		return info, nil
	}
	if err != nil {
		return gitprov.PRInfo{}, fmt.Errorf("fake provider: PR #%d's branch %s is not in %s: %w", number, head, p.Remote, err)
	}
	info.HeadSHA = strings.TrimSpace(string(out))
	return info, nil
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
		SourceBranch: st.Spec.Branch,
		SourceRepo:   cmp.Or(st.Source, p.Repo),
		HeadSHA:      st.Head,
	}, st.Spec.Branch, nil
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
