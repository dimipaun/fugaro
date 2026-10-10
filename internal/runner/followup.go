package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/followup"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/verify"
)

// A follow-up run continues a pull request an earlier Fugaro run opened
// (design §4.4). It checks out the pull request's branch, but takes its
// configuration from the base branch; it acts on the pull request's
// comments by trusted authors, as data; and it updates the same pull
// request, never opening another. Every check at bootstrap happens before
// anything on the provider changes, so a refused follow-up leaves the pull
// request as it was.

// followState is what a follow-up carries from bootstrap to finalize.
type followState struct {
	pr       gitprov.PRInfo     // the pull request as bootstrap read it
	startSHA string             // the branch's head when the run started
	wasReady bool               // the pull request was not a draft: at bootstrap, then right before finalize's push
	sel      followup.Selection // the comments the agent gets
	nonce    string             // the comments block's delimiter nonce
	restored restored           // the session to resume, or why not
	// prevAnswer is the previous follow-up's followup.md, as stored.
	prevAnswer string
	// rootTask and diffStat are a fresh session's context.
	rootTask, diffStat string
}

// followupFile is where a follow-up's agent writes its account.
const followupFile = "followup.md"

// maxAnswerRead bounds how much of the agent's followup.md is read; the
// report quotes at most a few KiB of it anyway.
const maxAnswerRead = 1 << 20

// redactor is a redactor for every secret the run knows now. It is built
// on each use: the list grows when git credentials are refreshed.
func (r *run) redactor() func(string) string { return agent.RedactFunc(r.secretList()) }

// checkoutFollowUp checks out the pull request's branch as it is on
// origin, refusing when origin no longer has it.
func (r *run) checkoutFollowUp(ctx context.Context, repo *gitops.Repo) error {
	branch := r.spec.Branch
	tip, err := repo.RemoteTip(ctx, branch)
	if err != nil {
		return fmt.Errorf("fetching %s from origin: %w", branch, err)
	}
	if tip == "" {
		return fmt.Errorf("branch %s no longer exists on origin; the PR was merged or its branch deleted", branch)
	}
	// Fetched by its full name: a tag named like the branch would win a
	// bare fetch.
	if err := repo.CheckoutNewBranch(ctx, "refs/heads/"+branch, branch); err != nil {
		return fmt.Errorf("checking out %s: %w", branch, err)
	}
	head, err := repo.HeadSHA(ctx)
	if err != nil {
		return fmt.Errorf("reading the head of %s: %w", branch, err)
	}
	r.follow.startSHA = head
	return nil
}

// readBaseConfig reads fugaro.yaml, and the files it names, from the base
// branch (origin/<ref>) rather than the checked-out pull request branch,
// which an earlier agent or a comment could have changed. The base named
// in it must be the task's ref.
func (r *run) readBaseConfig(ctx context.Context) (*config.Config, error) {
	ref := r.spec.Ref
	if err := r.repo.FetchBase(ctx, ref); err != nil {
		return nil, fmt.Errorf("fetching base %s: %w", ref, err)
	}
	rev := "origin/" + ref
	data, err := r.repo.ShowFile(ctx, rev, "fugaro.yaml")
	if err != nil {
		return nil, fmt.Errorf("reading fugaro.yaml at %s: %w", rev, err)
	}
	if r.d.Project != "" {
		// Before the strict parse, so a base that names no project is
		// told so rather than reported as an invalid file.
		if err := r.projectOfFile(ref, data); err != nil {
			return nil, err
		}
	}
	cfg, err := r.resolveConfig(data)
	if err != nil {
		return nil, err
	}
	if b := cfg.Git.BaseBranch; b != ref {
		return nil, fmt.Errorf("fugaro.yaml on %s names base %s; a follow-up's base must be the branch its configuration comes from", ref, b)
	}
	read := func(rel string) (string, error) { return r.readBaseFile(ctx, rev, rel) }
	if r.instructions, err = read(cfg.Agent.Instructions); err != nil {
		return nil, fmt.Errorf("agent.instructions: %w", err)
	}
	if cfg.Agent.Review != "" && !strings.HasPrefix(cfg.Agent.Review, "/") {
		if r.reviewFile, err = read(cfg.Agent.Review); err != nil {
			return nil, fmt.Errorf("agent.review: %w", err)
		}
	}
	return cfg, nil
}

// maxLinkHops bounds how many symbolic links readBaseFile follows.
const maxLinkHops = 8

// readBaseFile reads rel, a path fugaro.yaml names, from revision rev's
// tree as a first run would read it from its checkout: the path is
// cleaned first (./a and a//b are fine), and a symbolic link is followed
// within the tree, so CLAUDE.md -> AGENTS.md reads AGENTS.md. A path, or
// a link target, that leads outside the repository is refused. "" reads
// as "".
func (r *run) readBaseFile(ctx context.Context, rev, rel string) (string, error) {
	if rel == "" {
		return "", nil
	}
	p := path.Clean(filepath.ToSlash(rel))
	for hop := 0; ; hop++ {
		if !filepath.IsLocal(p) {
			return "", fmt.Errorf("%s leads outside the repository", rel)
		}
		mode, err := r.repo.TreeEntryMode(ctx, rev, p)
		if err != nil {
			return "", err
		}
		data, err := r.repo.ShowFile(ctx, rev, p)
		if err != nil {
			return "", err
		}
		if mode != "120000" {
			return string(data), nil
		}
		if hop == maxLinkHops {
			return "", fmt.Errorf("%s: too many symbolic links", rel)
		}
		target := string(data)
		if path.IsAbs(target) {
			return "", fmt.Errorf("%s is a symbolic link outside the repository", rel)
		}
		p = path.Clean(path.Join(path.Dir(p), target))
	}
}

// checkPullRequest makes sure the follow-up may touch the pull request: the
// repository is private (or its base config allows public ones), and the
// pull request is open, from this branch of this repository, at the
// commit checked out. Only then are the pull request and the follow-up
// recorded, in one save.
func (r *run) checkPullRequest(ctx context.Context) error {
	n := r.spec.PR
	repoInfo, err := r.provider.Repository(ctx)
	if err != nil {
		return fmt.Errorf("reading the repository: %w", err)
	}
	if !repoInfo.Private && !r.cfg.Followup.AllowPublic {
		return fmt.Errorf("follow-ups on a public repository need followup.allow_public in fugaro.yaml on %s", r.spec.Ref)
	}
	pr, err := r.provider.PullRequest(ctx, n)
	if err != nil {
		return fmt.Errorf("reading PR #%d: %w", n, err)
	}
	switch {
	case pr.State != gitprov.PROpen:
		return fmt.Errorf("PR #%d is %s", n, pr.State)
	case pr.SourceBranch != r.spec.Branch:
		return fmt.Errorf("PR #%d's source branch is %s, not %s", n, pr.SourceBranch, r.spec.Branch)
	case !strings.EqualFold(pr.SourceRepo, r.spec.Repo):
		return fmt.Errorf("PR #%d comes from repository %s, not %s", n, pr.SourceRepo, r.spec.Repo)
	case !gitprov.SameCommit(pr.HeadSHA, r.follow.startSHA):
		return fmt.Errorf("PR #%d's head moved during bootstrap; launch again", n)
	}
	r.follow.pr, r.follow.wasReady = pr, !pr.Draft
	r.rec.PR = &runstore.PRRef{Number: n, URL: pr.URL}
	r.rec.FollowUp = &runstore.FollowUp{PR: n, PreviousRun: r.spec.PreviousRun, StartSHA: r.follow.startSHA}
	r.save(ctx)
	r.noteRegistryPRURL(pr.URL)
	return nil
}

// prepareFollowUp reads the pull request's comments, refuses a stale
// follow-up, selects what the agent gets, stores comments.json, and puts
// the previous run's session in place. It runs after agent.BuildEnv, so
// every secret is registered before any comment is redacted.
func (r *run) prepareFollowUp(ctx context.Context) error {
	n, prevID := r.spec.PR, r.spec.PreviousRun
	prev := r.d.Store.Sibling(prevID)
	prevRec, err := prev.ReadRecord(ctx)
	if err != nil {
		r.d.Log.Warn("reading the previous run's record failed", "previous_run", prevID, "err", r.redact(err.Error()))
		return fmt.Errorf("previous run %s has no readable record", prevID)
	}
	prevTask, err := prev.ReadTask(ctx)
	if err != nil {
		prevTask = nil
	}
	fetched := r.d.Now().UTC()
	all, err := r.provider.Comments(ctx, n)
	if err != nil {
		return fmt.Errorf("reading PR #%d's comments: %w", n, err)
	}
	if err := r.checkStale(ctx, all, prevRec); err != nil {
		return err
	}
	since := r.since(ctx, prev, prevTask, prevRec)
	if r.follow.pr.AuthorID == "" {
		r.d.Log.Warn("the provider did not name the pull request's author, so no comment can be told apart from Fugaro's own; trusting none")
	}
	redact := r.redactor()
	sel := followup.Select(all, since, followup.NewTrust(r.cfg.Followup, r.follow.pr), followup.DefaultLimits, redact)
	if sel.MarkersFromAnyone {
		r.d.Log.Warn("the provider could not tell Fugaro's own comments apart; honouring Fugaro markers from every author")
	}
	r.follow.sel, r.follow.nonce = sel, followup.NewNonce()
	r.d.Log.Info("pull request comments selected", "comments", len(sel.Comments), "omitted", sel.Omitted,
		"authors", len(sel.Authors), "untrusted_authors", sel.UntrustedAuthorCount, "since", since)
	if data, err := json.MarshalIndent(followup.NewSnapshot(n, sel, fetched), "", "  "); err != nil {
		r.d.Log.Warn("encoding comments.json failed", "err", err)
	} else if err := r.d.Store.PutFile(ctx, "comments.json", data, "application/json"); err != nil {
		r.d.Log.Warn("storing comments.json failed", "err", r.redact(err.Error()))
	}
	switch data, err := prev.ReadFile(ctx, followupFile); {
	case errors.Is(err, runstore.ErrNotFound):
	case err != nil:
		r.d.Log.Warn("reading the previous run's followup.md failed", "err", r.redact(err.Error()))
	default:
		r.follow.prevAnswer = string(data)
	}
	fu := r.rec.FollowUp
	fu.Comments, fu.Authors, fu.UntrustedAuthors, fu.Omitted = len(sel.Comments), sel.Authors, sel.UntrustedAuthors, sel.Omitted
	fu.UntrustedAuthorCount = sel.UntrustedAuthorCount

	r.follow.restored = r.restoreSession(ctx, prev, r.follow.startSHA)
	if r.follow.restored.Resumed {
		fu.Session = "resumed"
	} else {
		fu.Session = "fresh"
		r.freshContext(ctx)
	}
	fu.SessionNote = r.follow.restored.Note
	r.save(ctx)
	return nil
}

// checkStale refuses a follow-up that another run overtook: Fugaro's own
// comments name the runs that posted them, and none may name a run, other
// than the previous one and this one, that pushed to the pull request and
// started after the previous one. A run that pushed nothing (its push was
// refused) never updated the pull request, so its note can't make a
// follow-up stale; nor can a run whose record can't be read.
func (r *run) checkStale(ctx context.Context, all []gitprov.Comment, prevRec *runstore.Record) error {
	for _, id := range followup.FugaroRuns(all) {
		if id == r.spec.PreviousRun || id == r.spec.RunID {
			continue
		}
		other := r.d.Store.Sibling(id)
		rec, err := other.ReadRecord(ctx)
		if err != nil {
			r.d.Log.Warn("a run named on the pull request has no readable record; not counting it", "run", id, "err", r.redact(err.Error()))
			continue
		}
		t, err := other.ReadTask(ctx)
		if err != nil {
			t = nil
		}
		if _, pushed := runview.Pushed(t, rec); pushed && rec.StartedAt.After(prevRec.StartedAt) {
			return fmt.Errorf("run %s updated PR #%d after this follow-up was launched; start a new follow-up", id, r.spec.PR)
		}
	}
	return nil
}

// since is when the comments this follow-up hasn't seen start: when the
// previous run was a follow-up, the moment it fetched its comments (so
// comments posted while it ran aren't lost); otherwise, or when that
// can't be read, the previous run's start (its pull request didn't exist
// before its finalize).
func (r *run) since(ctx context.Context, prev *runstore.Store, prevTask *task.Spec, prevRec *runstore.Record) time.Time {
	if prevRec.FollowUp == nil && (prevTask == nil || !prevTask.IsFollowUp()) {
		return prevRec.StartedAt
	}
	data, err := prev.ReadFile(ctx, "comments.json")
	var snap followup.Snapshot
	if err == nil {
		err = json.Unmarshal(data, &snap)
	}
	if err == nil && snap.Fetched.IsZero() {
		err = errors.New("it has no fetched_at")
	}
	if err != nil {
		r.d.Log.Warn("the previous follow-up's comments.json is unusable; reading comments since it started", "err", r.redact(err.Error()))
		return prevRec.StartedAt
	}
	// The fetch time is the runner's clock, comment times the provider's:
	// a margin keeps a comment posted around the fetch from being lost to
	// skew. Seeing one again is harmless; the previous answer says what
	// was already done.
	return snap.Fetched.Add(-sinceSkewMargin)
}

// sinceSkewMargin is how far before the previous follow-up's fetch the
// comments a follow-up reads start.
const sinceSkewMargin = 2 * time.Minute

// freshContext gathers what a fresh session's prompt carries: the root
// run's task, when it is still stored, and the branch's diff stat.
func (r *run) freshContext(ctx context.Context) {
	r.follow.rootTask = ""
	if id, ok := task.BranchRunID(r.spec.Branch); ok {
		switch t, err := r.d.Store.Sibling(id).ReadTask(ctx); {
		case errors.Is(err, runstore.ErrNotFound):
		case err != nil:
			r.d.Log.Warn("reading the root run's task failed", "run", id, "err", r.redact(err.Error()))
		default:
			r.follow.rootTask = t.Task
		}
	}
	base := r.cfg.Git.BaseBranch
	stat, err := sessionGit(ctx, r.repo, "diff", "--stat", "origin/"+base+"...HEAD")
	if err != nil {
		r.d.Log.Warn("reading the branch's diff stat failed", "err", r.redact(err.Error()))
	}
	r.follow.diffStat = stat
}

// promptData fills the follow-up's prompts.
func (r *run) promptData() followup.PromptData {
	f := r.follow
	return followup.PromptData{
		PR: r.spec.PR, PRURL: f.pr.URL, Branch: r.rec.Branch, Base: r.cfg.Git.BaseBranch, StateDir: r.d.StateDir,
		Instructions: r.spec.Task, Resumed: f.restored.Resumed, MovedCommits: f.restored.Moved,
		RootTask: f.rootTask, DiffStat: f.diffStat, PreviousAnswer: f.prevAnswer, Nonce: f.nonce, Redact: r.redactor(),
	}
}

// fellBackFresh records that the saved session couldn't be resumed after
// all, and gathers a fresh session's context for the retry.
func (r *run) fellBackFresh(ctx context.Context) {
	r.d.Log.Warn("the saved session could not be resumed; starting a fresh session")
	r.follow.restored = restored{Note: "the saved session could not be resumed"}
	r.freshContext(ctx)
	r.rec.FollowUp.Session, r.rec.FollowUp.SessionNote = "fresh", r.follow.restored.Note
	r.sessionID = ""
	r.save(ctx)
}

// answer is the agent's followup.md as the report quotes it, or "" when
// it wrote none. It is read as a regular file only, never through a
// symlink the agent could point at the runner's credentials.
func (r *run) answer() string {
	f, err := os.OpenFile(filepath.Join(r.d.StateDir, followupFile), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			r.d.Log.Warn("reading followup.md failed", "err", r.redact(err.Error()))
		}
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		r.d.Log.Warn("followup.md is not a regular file; not quoting it")
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAnswerRead))
	if err != nil {
		r.d.Log.Warn("reading followup.md failed", "err", r.redact(err.Error()))
		return ""
	}
	return followup.QuoteAnswer(string(data), r.redactor())
}

// reportPosted reports whether Fugaro's identity already posted a comment
// carrying this run's marker on pr, as when an earlier attempt of this
// execution posted its report. A listing that fails says no, with a
// warning: a second report is better than none. Only Self comments
// count, since anyone can type a marker: when the provider can't tell
// Fugaro's own comments apart, nothing matches and the report is posted,
// possibly twice.
func (r *run) reportPosted(ctx context.Context, pr gitprov.PR) bool {
	if r.follow.sel.MarkersFromAnyone {
		r.d.Log.Warn("the provider can't tell Fugaro's own comments apart; posting the report without checking for an earlier copy")
		return false
	}
	all, err := r.provider.Comments(ctx, pr.Number)
	if err != nil {
		r.d.Log.Warn("listing the pull request's comments failed; posting the report anyway", "err", r.redact(err.Error()))
		return false
	}
	for _, c := range all {
		if id, ok := gitprov.FugaroRun(c.Body); c.Self && ok && id == r.rec.RunID {
			return true
		}
	}
	return false
}

// pushFollowUp pushes a follow-up's branch, only while its pull request is
// open and only onto the branch as this run left it. done is true when the
// run ended here without pushing (the pull request was merged or closed,
// its branch deleted, or someone else pushed to it): the record says why,
// and nothing but a short note for the last case goes on the pull request.
func (r *run) pushFollowUp(ctx context.Context, records []verify.Record) (done bool, err error) {
	n, branch := r.spec.PR, r.rec.Branch
	pr, err := r.readPRRetrying(ctx)
	if err != nil {
		// Never push to a pull request whose state is unknown.
		err = fmt.Errorf("reading PR #%d before the push: %w", n, err)
		r.storeUnposted(ctx, err, records)
		return false, err
	}
	if pr.State != gitprov.PROpen {
		r.endUnchanged(ctx, fmt.Sprintf("PR #%d was %s during the run; nothing was pushed", n, pr.State), records)
		return true, nil
	}
	// What finalize changes is the state people see now, which they may
	// have changed during the run: the report's "was ready" is about it.
	r.follow.wasReady = !pr.Draft
	if rej := r.workflowGuard(ctx); rej != nil {
		return true, r.endRefused(ctx, rej, n, records)
	}
	err = r.repo.PushExisting(ctx, branch, r.follow.startSHA)
	switch rejected := r.asRefusal(err); {
	case rejected != nil:
		return true, r.endRefused(ctx, rejected, n, records)
	case err == nil:
		return false, nil
	case errors.Is(err, gitops.ErrBranchGone):
		r.endUnchanged(ctx, fmt.Sprintf("%s no longer exists on origin; nothing was pushed, and the branch was not recreated", branch), records)
		return true, nil
	case errors.Is(err, gitops.ErrForeignTip):
		reason := fmt.Sprintf("someone pushed to %s during the run; nothing was overwritten", branch)
		note := "**Fugaro:** " + reason + ". This run's changes were not pushed; start a new follow-up to build on the branch as it is now.\n" +
			gitprov.ReportMarker(r.rec.RunID) + "\n"
		if cerr := r.provider.Comment(ctx, gitprov.PR{Number: n, URL: pr.URL}, note); cerr != nil {
			r.d.Log.Warn("posting the note about the refused push failed", "err", r.redact(cerr.Error()))
		}
		r.endUnchanged(ctx, reason, records)
		return true, nil
	}
	err = fmt.Errorf("pushing %s: %w", branch, err)
	r.storeUnposted(ctx, err, records)
	return false, err
}

// readPRRetrying reads the follow-up's pull request, retrying a failure
// as ensurePR does: a passing provider error must not throw the run's
// work away.
func (r *run) readPRRetrying(ctx context.Context) (gitprov.PRInfo, error) {
	delay := r.d.RetryDelay
	if delay == 0 {
		delay = 3 * time.Second
	}
	for attempt := 1; ; attempt++ {
		pr, err := r.provider.PullRequest(ctx, r.spec.PR)
		if err == nil || attempt == prAttempts {
			return pr, err
		}
		r.d.Log.Warn("reading the pull request failed; retrying", "attempt", attempt, "err", r.redact(err.Error()))
		select {
		case <-ctx.Done():
			return pr, err
		case <-time.After(delay):
		}
	}
}

// storeUnposted stores the report of a follow-up that stops with err
// before updating its pull request, as the infra_error Run then records,
// so diagnose still shows what the agent did. Nothing is posted.
func (r *run) storeUnposted(ctx context.Context, err error, records []verify.Record) {
	rec := *r.rec
	rec.Status, rec.Outcome, rec.Reason = runstore.StatusInfraError, runstore.OutcomeNone, r.redact(err.Error())
	fu := r.followUpSection()
	report := agent.Redact(FollowUpReport(&rec, r.d.Store.Prefix(), r.logTail(false, records), fu), r.secretList())
	r.storeReport(ctx, report, fu)
}

// giveUpNoteAllowed reports whether finalize may post its not-ready note
// after EnsurePR gave up. A first run's pull request is its own; a
// follow-up's is read again first, and the note goes only on an open one:
// a transport error can hide that it was merged or closed meanwhile.
func (r *run) giveUpNoteAllowed(ctx context.Context) bool {
	n := r.spec.PR
	if r.follow == nil {
		if r.prNumber() == 0 {
			return true
		}
		n = r.prNumber() // opened early: a person may have closed it since
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), giveUpCommentTimeout)
	defer cancel()
	pr, err := r.provider.PullRequest(cctx, n)
	switch {
	case err != nil:
		r.d.Log.Warn("reading the pull request before the not-ready note failed; not posting it", "err", r.redact(err.Error()))
		return false
	case pr.State != gitprov.PROpen:
		r.d.Log.Warn("the pull request is no longer open; not posting the not-ready note", "state", string(pr.State))
		return false
	}
	return true
}

// endUnchanged ends a follow-up whose pull request this run no longer
// updates: failed, with outcome none. Its report is stored, not posted,
// so diagnose still shows what the run did.
func (r *run) endUnchanged(ctx context.Context, reason string, records []verify.Record) {
	r.rec.Status, r.rec.Outcome, r.rec.Reason = runstore.StatusFailed, runstore.OutcomeNone, reason
	if r.follow == nil && r.isCancelled() {
		// A cancelled run stays cancelled (design 4.2a, E10); the PR note
		// is its reason.
		r.rec.Status, r.rec.Reason = runstore.StatusCancelled, "cancelled; "+reason
	}
	if h := r.haltValue(); h != nil {
		// A halt stays a halt: the run stopped for its limit, and the
		// pull request was left as it was for the reason given too.
		r.rec.Status, r.rec.Halt = runstore.StatusHalted, h
		r.rec.Reason = (&HaltError{*h}).Error() + "; " + reason
	}
	r.d.Log.Warn("the pull request was not updated", "reason", reason)
	r.storeEnded(ctx, records)
}

// storeEnded stores the report of a run that ends without posting it.
func (r *run) storeEnded(ctx context.Context, records []verify.Record) {
	r.updateCost()
	var fu *FollowUpSection
	if r.follow != nil {
		fu = r.followUpSection()
	}
	report := agent.Redact(FollowUpReport(r.rec, r.d.Store.Prefix(), r.logTail(false, records), fu), r.secretList())
	r.storeReport(ctx, report, fu)
}

// followUpSection is the report's follow-up section, as far as the run
// got.
func (r *run) followUpSection() *FollowUpSection {
	f, sel := r.follow, r.follow.sel
	fu := &FollowUpSection{
		PreviousRun: r.spec.PreviousRun, Authors: sel.Authors, UntrustedAuthors: sel.UntrustedAuthors,
		UntrustedAuthorCount: sel.UntrustedAuthorCount, UntrustedComments: sel.Omitted[followup.OmitUntrusted],
		MarkersFromAnyone: sel.MarkersFromAnyone, AuthorUnknown: f.pr.AuthorID == "", NoNewCommits: r.rec.HeadSHA == f.startSHA, Answer: r.answer(),
	}
	if rf := r.rec.FollowUp; rf != nil {
		fu.Session, fu.SessionNote = rf.Session, rf.SessionNote
	}
	return fu
}
