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
// left at once (Task 6). Pushes are fast-forward only, by sha, with the
// runner's credentials and the same guards as finalize's push, plus a scan
// for the values the run redacts. A checkpoint never fails the run, never
// forces, never commits and never touches the working tree or the index.

// checkpointTicks makes the poll's ticker; tests replace it with a channel
// they drive (DriveCheckpoints).
var checkpointTicks = func(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// checkpointTicked, when set, is called at the end of every poll (tests).
var checkpointTicked func()

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
	openTries int             // checkpoint pushes that tried to open the draft (Task 6)
	pushes    int             // checkpoint pushes made
}

// checkpointsOn reports whether this run checkpoints: a first run, with a
// resolved base commit (bootstrap may have failed to get one), whose
// fugaro.yaml leaves git.pr.checkpoints on.
func (r *run) checkpointsOn() bool {
	return r.follow == nil && r.baseSHA != "" && r.cfg != nil && r.cfg.Git.PR.CheckpointsOn()
}

// startCheckpoints starts the stage's checkpoint goroutine on stageCtx and
// returns its stop, which cancels it and waits for it: nothing of it runs
// once stop returns. stop is safe to call more than once.
func (r *run) startCheckpoints(stageCtx context.Context, stage string) (stop func()) {
	if !r.checkpointsOn() || r.ckpt.stopped {
		return func() {}
	}
	if r.ckpt.sched == nil {
		r.ckpt.sched = newCheckpointSchedule(r.baseSHA)
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
			r.d.Log.Error("a checkpoint panicked; carrying on", "stage", stage, "panic", fmt.Sprint(p))
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
// commit bootstrap resolved when it fetched the base — never a ref name
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
// poll's push, mid-stage, rather than a boundary's (Task 6).
func (r *run) checkpointPush(ctx context.Context, stage, sha string, during bool) {
	now := r.d.Now()
	// Of the tip read, not HEAD: a tip read just before the agent's first
	// commit is the base, which is never pushed, and neither is one
	// already pushed. Both sides are the frozen values (baseSHA, the last
	// pushed_head), never gitops.CountAhead's "origin/"+branch-name: the
	// agent's own git could repoint that local ref (a fetch that moves it
	// forward, or an adversarial update-ref) to make an ahead-count read 0
	// forever, which would silently stop checkpoints for the rest of the
	// run with no warning logged — exactly the data loss this feature
	// exists to prevent. due() already guarantees sha differs from both
	// before ever calling this; the check is repeated here, cheaply and
	// without a git call, as a safety net for any future caller that
	// pushes without going through the schedule first (a stage boundary,
	// Task 6).
	if sha == r.baseSHA || sha == r.rec.PushedHead {
		return // no commit of the run's own yet
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
		r.stopCheckpoints("the host would refuse the push", "reason", refusalText(r.asRefusal(err)))
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
	if rej := r.workflowGuardAt(ctx, r.checkpointSince(), sha); rej != nil {
		return rej // no network call for a push GitHub is sure to refuse
	}
	pctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	if err := r.repo.PushFastForward(pctx, r.rec.Branch, sha); err != nil {
		return err
	}
	r.rec.PushedHead = sha
	r.save(ctx)
	return nil
}
