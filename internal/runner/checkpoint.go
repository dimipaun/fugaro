package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitops"
)

// Checkpoint pushes (design checkpoint-pushes.md). While a first run's
// stage runs, the runner reads the run branch's tip every checkpointPoll,
// locally, and pushes a new tip once it has been still for checkpointQuiet,
// at most once per checkpointMinGap; each stage boundary pushes what is
// left at once. Pushes are fast-forward only, by sha, with the runner's
// credentials and the same guards as finalize's push, plus a scan for the
// values the run redacts. A checkpoint never fails the run, never forces,
// never commits and never touches the working tree or the index. The draft
// pull request opens at the first checkpoint push, marked not verified.

// checkpointTicks makes the poll's ticker; tests replace it with a channel
// they drive (DriveCheckpoints).
var checkpointTicks = func(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// checkpointTicked, when set, is called at the end of every poll (tests).
var checkpointTicked func()

// checkpointPushedSeam, when set by a test, runs in pushCheckpoint between
// a successful push and saving pushed_head.
var checkpointPushedSeam func()

// fetchedBaseSeam, when set by a test, runs in bootstrap right after the
// base branch is fetched.
var fetchedBaseSeam func()

// checkpointersRunning counts the checkpoint goroutines alive, so a test
// can see that a stage's end stopped its own.
var checkpointersRunning atomic.Int32

// checkpointState is the run's checkpoint bookkeeping, touched only by the
// running stage's checkpoint goroutine (and, for sched, lazily created by
// it the first time); the run goroutine reads it only after startCheckpoints'
// stop has returned.
type checkpointState struct {
	// sched persists across stage boundaries, not just within one stage: a
	// boundary push (Task 6) still counts as the last push for the next
	// stage's minute, and the schedule is what remembers that.
	sched     *checkpointSchedule
	stopped   bool            // a permanent reason: no more checkpoints this run
	warned    map[string]bool // warnings already logged, by kind
	openTries int             // checkpoint pushes that tried to open the draft
	pushes    int             // checkpoint pushes made
}

// checkpointsOn reports whether this run checkpoints: a first run, with a
// resolved base and start commits (bootstrap may have failed to read them),
// whose fugaro.yaml leaves git.pr.checkpoints on.
func (r *run) checkpointsOn() bool {
	return r.follow == nil && r.baseSHA != "" && r.startSHA != "" && r.cfg != nil && r.cfg.Git.PR.CheckpointsOn()
}

// startCheckpoints starts the stage's checkpoint goroutine on stageCtx and
// returns its stop, which cancels it and waits for it: nothing of it runs
// once stop returns. stop is safe to call more than once.
func (r *run) startCheckpoints(stageCtx context.Context, stage string) (stop func()) {
	if !r.checkpointsOn() || r.ckpt.stopped {
		return func() {}
	}
	if r.ckpt.sched == nil {
		// The start commit, not the base's: a run started from another ref
		// than its base branch starts ahead of the base with nothing of
		// the agent's on it.
		r.ckpt.sched = newCheckpointSchedule(r.startSHA)
	}
	ctx, cancel := context.WithCancel(stageCtx)
	ticks, stopTicks := checkpointTicks(checkpointPoll)
	done := make(chan struct{})
	checkpointersRunning.Add(1)
	go func() {
		defer close(done)
		defer checkpointersRunning.Add(-1)
		defer stopTicks()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticks:
				r.checkpointTick(ctx, stage)
			}
		}
	}()
	return sync.OnceFunc(func() { cancel(); <-done })
}

// warnCheckpoint logs msg once per run for key.
func (r *run) warnCheckpoint(key, msg string, args ...any) {
	if r.ckpt.warned == nil {
		r.ckpt.warned = map[string]bool{}
	}
	if r.ckpt.warned[key] {
		return
	}
	r.ckpt.warned[key] = true
	r.d.Log.Warn(msg, args...)
}

// stopCheckpoints ends checkpoints for the run, for a reason no later poll
// can change; finalize then pushes, or refuses, as it always did. Called at
// most once: every later poll sees ckpt.stopped in checkpointBlocked and
// returns before anything could call this again.
func (r *run) stopCheckpoints(reason string, args ...any) {
	r.ckpt.stopped = true
	r.d.Log.Warn("no more checkpoint pushes this run: "+reason+"; finalize pushes as usual", args...)
}

// checkpointBlocked reports whether no checkpoint may run now.
func (r *run) checkpointBlocked(ctx context.Context) bool {
	return ctx.Err() != nil || r.ckpt.stopped || r.pr.gone || r.haltValue() != nil || r.isCancelled() || r.budget.Exhausted()
}

// checkpointTip reads the run branch's tip, locally; false when the
// checkout is busy (the next poll looks again) or the read failed.
func (r *run) checkpointTip(ctx context.Context) (string, bool) {
	sha, err := r.repo.CheckpointTip(ctx, r.rec.Branch)
	switch {
	case errors.Is(err, gitops.ErrGitBusy):
		return "", false
	case err != nil:
		r.warnCheckpoint("tip", "reading the run branch for a checkpoint failed", "err", r.redact(err.Error()))
		return "", false
	}
	return sha, true
}

// checkpointTick is one poll: read the tip locally and push it when the
// schedule says so. A poll whose tip is unchanged makes no network call.
// Every failure is a warning; a panic is recovered here, as a goroutine's
// panic would end the process.
func (r *run) checkpointTick(ctx context.Context, stage string) {
	if checkpointTicked != nil {
		defer checkpointTicked()
	}
	defer func() {
		if p := recover(); p != nil {
			r.d.Log.Error("a checkpoint panicked; carrying on", "stage", stage, "panic", r.redact(fmt.Sprint(p)))
		}
	}()
	if r.checkpointBlocked(ctx) {
		return
	}
	sha, ok := r.checkpointTip(ctx)
	if !ok || !r.ckpt.sched.due(r.d.Now(), sha, r.rec.PushedHead) {
		return
	}
	r.checkpointPush(ctx, stage, sha, true)
}

// checkpointSince is where a checkpoint's secret scan and workflow guard
// start: the last pushed commit, or, before the first push, the base
// commit bootstrap read right after it fetched the base (as finalize's
// guards start at the base) — never a ref name
// such as origin/<base> that the agent's own git could repoint once it
// controls the checkout (C6a).
func (r *run) checkpointSince() string {
	if r.rec.PushedHead != "" {
		return r.rec.PushedHead
	}
	return r.baseSHA
}

// checkpointPush pushes sha, the tip just read: every check is on sha,
// never on HEAD, which the agent may have moved since. during says it is a
// poll's push, mid-stage, rather than a boundary's.
func (r *run) checkpointPush(ctx context.Context, stage, sha string, during bool) {
	now := r.d.Now()
	if !r.checkpointAhead(ctx, sha) {
		return // no commit of the run's own on it
	}
	if !r.checkpointClean(ctx, sha) {
		if !r.ckpt.stopped {
			r.ckpt.sched.failed(now)
		}
		return
	}
	err := r.pushCheckpoint(ctx, sha)
	switch {
	case err == nil:
	case errors.Is(err, gitops.ErrNotFastForward):
		r.ckpt.sched.failed(now)
		r.warnCheckpoint("rewritten", "the run branch's pushed commits were rewritten; checkpoints wait until a push fast-forwards again (a verified stage end or finalize pushes the branch as it is)", "sha", shortSHA(sha))
		return
	case r.asRefusal(err) != nil:
		// The text names files the agent chose: redacted like every
		// other text this file logs.
		r.stopCheckpoints("the host would refuse the push", "reason", r.redact(refusalText(r.asRefusal(err))))
		return
	case ctx.Err() != nil:
		return // the stage ended mid-push; the boundary or finalize pushes
	default:
		r.ckpt.sched.failed(now)
		r.warnCheckpoint("push", "a checkpoint push failed; it is retried in a few minutes", "err", r.redact(err.Error()))
		return
	}
	r.ckpt.sched.pushed(now)
	r.ckpt.pushes++
	r.d.Log.Info("checkpoint pushed", "stage", stage, "sha", shortSHA(sha), "n", r.ckpt.pushes, "boundary", !during)
	r.afterCheckpoint(ctx, stage, during)
}

// boundaryCheckpoint pushes what a stage left committed and unpushed, at
// once, on the run goroutine after the stage's checkpointer stopped: the
// agent is idle, so there is nothing to wait for, and a boundary is exempt
// from the per-minute limit (a run has a handful of them).
func (r *run) boundaryCheckpoint(ctx context.Context, stage string) {
	// On the run goroutine, not its own: a panic here must not reach
	// afterStage's recover, which logs its panic unredacted.
	defer func() {
		if p := recover(); p != nil {
			r.d.Log.Error("a checkpoint panicked; carrying on", "stage", stage, "panic", r.redact(fmt.Sprint(p)))
		}
	}()
	if !r.checkpointsOn() || r.checkpointBlocked(ctx) {
		return
	}
	sha, ok := r.checkpointTip(ctx)
	if !ok || sha == r.rec.PushedHead {
		return
	}
	r.checkpointPush(ctx, stage, sha, false)
}

// afterCheckpoint opens the draft at the first checkpoint push, or brings an
// open draft's status section up to date. With early_draft false the branch
// is all a checkpoint pushes: the PR opens at finalize. Opening the draft is
// tried at most checkpointOpenTries times, once per checkpoint push; after
// that the verified boundary or finalize opens it.
func (r *run) afterCheckpoint(ctx context.Context, stage string, during bool) {
	if !r.cfg.Git.PR.EarlyDraftOn() || r.pr.gone {
		return
	}
	if r.rec.PR == nil {
		if r.ckpt.openTries >= checkpointOpenTries {
			return // left to the verified boundary or finalize
		}
		r.ckpt.openTries++
		r.openDraftPR(ctx, stage, during)
		return
	}
	r.statusUpdate(ctx, stage, during)
}

// checkpointAhead reports whether sha, the tip read, holds a commit of the
// run's own: it is not the start commit, the base commit or the last pushed
// one, nor behind any of them (the agent reset the branch back, say with
// git reset --hard HEAD~1, which would otherwise create the remote branch
// at an old base commit). It judges on the tip read and the frozen SHAs
// only (C6a), never on a ref name such as origin/<base>: the agent's own
// git could repoint that local ref to make every tip look behind, silently
// stopping checkpoints for the rest of the run. A failed check warns once
// and pushes nothing; the next poll checks again.
func (r *run) checkpointAhead(ctx context.Context, sha string) bool {
	if sha == r.startSHA || sha == r.baseSHA || sha == r.rec.PushedHead {
		return false
	}
	for _, c := range []string{r.startSHA, r.baseSHA, r.rec.PushedHead} {
		if c == "" {
			continue
		}
		behind, err := r.repo.IsAncestor(ctx, sha, c)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				r.warnCheckpoint("ahead", "checking a checkpoint against the base failed; not pushing it", "err", r.redact(err.Error()))
			}
			return false
		case behind:
			return false
		}
	}
	return true
}

// checkpointClean scans the commits a checkpoint would add (checkpointSince
// to sha) for a value the run redacts. A hit stops checkpoints for the run:
// every later push would carry that commit. A binary or very large range is
// pushed, as finalize would push it.
func (r *run) checkpointClean(ctx context.Context, sha string) bool {
	redact := agent.RedactFunc(r.secretList())
	scan, err := r.repo.ScanRange(ctx, r.checkpointSince(), sha, func(text string) bool { return redact(text) != text })
	switch {
	case err != nil:
		r.warnCheckpoint("scan", "checking a checkpoint's commits for secrets failed; not pushing it", "err", r.redact(err.Error()))
		return false
	case scan.Hit:
		r.stopCheckpoints("the new commits hold a value the run redacts (a secret)")
		return false
	}
	return true
}

// pushCheckpoint pushes sha fast-forward only, after finalize's workflow
// guard on that exact commit, and saves pushed_head at once, as every push
// does, so a run killed later still says what reached the remote.
func (r *run) pushCheckpoint(ctx context.Context, sha string) error {
	r.refreshForPush(ctx)
	// The range starts at the frozen base commit (or the last pushed one),
	// wider than finalize's, which starts at origin/<base>. After a rebase
	// onto a newer base that brought someone else's .github/workflows
	// change, this guard refuses and checkpoints stop for the run although
	// the host would accept the push: it fails closed, over-conservatively.
	if rej := r.workflowGuardAt(ctx, r.checkpointSince(), sha); rej != nil {
		return rej // no network call for a push GitHub is sure to refuse
	}
	pctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	if err := r.repo.PushFastForward(pctx, r.rec.Branch, sha); err != nil {
		return err
	}
	if checkpointPushedSeam != nil {
		checkpointPushedSeam()
	}
	r.rec.PushedHead = sha
	r.save(ctx)
	return nil
}
