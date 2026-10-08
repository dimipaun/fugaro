package runner_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// ckptRig is a harness whose checkpointer the test drives: tick sends one
// poll and returns when it is done, and the run's clock moves only when
// the test moves it. Nothing in it waits for wall-clock time.
type ckptRig struct {
	*harness
	clock *testClock
	tick  func()
	logs  *bytes.Buffer // read only after the run
}

func newCkptRig(t *testing.T, cfg string) *ckptRig {
	t.Helper()
	tick := runner.DriveCheckpoints(t)
	// The fixture's 5-minute budget is too short for a clock moved by
	// minutes; the stages' own timeouts still count real time.
	h := prHarness(t, strings.Replace(cfg, "total: 5m", "total: 1h", 1))
	c := newClock()
	h.deps.Now = c.Now
	logs := &bytes.Buffer{}
	h.deps.Log = slog.New(slog.NewTextHandler(logs, nil))
	return &ckptRig{harness: h, clock: c, tick: tick, logs: logs}
}

// settle polls, lets a quiet period and a rate window pass, and polls
// again: a new tip is pushed by the second poll unless something holds
// it back.
func (g *ckptRig) settle() {
	g.tick()
	g.clock.advance(runner.CheckpointMinGap)
	g.tick()
}

// remoteTip is the run branch's tip on the remote, "" when it is absent.
func remoteTip(t *testing.T, h *harness) string {
	t.Helper()
	return strings.TrimSpace(testutil.Git(t, h.remote, "for-each-ref", "--format=%(objectname)", "refs/heads/fugaro/"+runID))
}

// pushedHead is the pushed_head saved in the run record.
func pushedHead(t *testing.T, h *harness) string {
	t.Helper()
	rec, err := h.store.ReadRecord(context.Background())
	if err != nil {
		return ""
	}
	return rec.PushedHead
}

// countPushes makes the remote count the pushes it accepts (post-receive).
func countPushes(t *testing.T, h *harness) func() int {
	t.Helper()
	log := filepath.Join(t.TempDir(), "pushes")
	script := "#!/bin/sh\ncat >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(h.remote, "hooks", "post-receive"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return func() int {
		data, _ := os.ReadFile(log)
		return strings.Count(string(data), "\n")
	}
}

// commitWIP commits a file without verifying it and returns HEAD.
func commitWIP(t *testing.T, req agent.Request, name string) string {
	t.Helper()
	shell(t, req, "echo "+name+" > "+name+".txt && git add -A && git commit -qm 'wip "+name+"'")
	return strings.TrimSpace(testutil.Git(t, req.Dir, "rev-parse", "HEAD"))
}

// TestCheckpointPushesCommittedWorkMidStage is the incident: work
// committed in a long stage is on the remote before the stage ends.
func TestCheckpointPushesCommittedWorkMidStage(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head := commitWIP(t, req, "wip")
		g.settle()
		if pushedHead(t, g.harness) != head || remoteTip(t, g.harness) != head {
			t.Errorf("the committed work is not on the remote mid-stage")
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || remoteTip(t, g.harness) != rec.HeadSHA {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !strings.Contains(g.logs.String(), "checkpoint pushed") {
		t.Fatalf("no checkpoint logged:\n%s", g.logs)
	}
}

// TestBurstOfCommitsIsOnePush: commits closer together than the quiet
// period are one push, of the latest.
func TestBurstOfCommitsIsOnePush(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	pushes := countPushes(t, g.harness)
	burst := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		var last string
		for _, name := range []string{"one", "two", "three"} {
			last = commitWIP(t, req, name)
			g.tick()
			g.clock.advance(2 * time.Second)
		}
		if n := pushes(); n != 0 {
			t.Errorf("%d pushes inside the burst", n)
		}
		g.clock.advance(runner.CheckpointQuiet)
		g.tick()
		if n := pushes(); n != 1 || pushedHead(t, g.harness) != last {
			t.Errorf("after the burst: %d pushes, pushed %s, want 1 of %s", n, pushedHead(t, g.harness), last)
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, burst, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
}

// TestAtMostOnePushPerMinute: commits every 20 s are pushed at most once a
// minute, the tip at the end of the window.
func TestAtMostOnePushPerMinute(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	pushes := countPushes(t, g.harness)
	steady := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "c1")
		g.tick()
		g.clock.advance(runner.CheckpointQuiet)
		g.tick() // the first push, at T
		var last string
		for _, name := range []string{"c2", "c3"} {
			g.clock.advance(20 * time.Second)
			last = commitWIP(t, req, name)
			g.tick()
		}
		g.clock.advance(19 * time.Second) // T+59s
		g.tick()
		if n := pushes(); n != 1 {
			t.Errorf("%d pushes inside the minute, want 1", n)
		}
		g.clock.advance(time.Second) // T+60s: the window ended
		g.tick()
		if n := pushes(); n != 2 || pushedHead(t, g.harness) != last {
			t.Errorf("at the window's end: %d pushes, pushed %s, want 2 with %s", n, pushedHead(t, g.harness), last)
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, steady, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
}

// TestFailedPushRetriedByTheFallback: a refused push warns once and is
// retried checkpointFallback later, not at every poll.
func TestFailedPushRetriedByTheFallback(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	flag, tried := filepath.Join(t.TempDir(), "refuse"), filepath.Join(t.TempDir(), "tried")
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\ncat >/dev/null\nif [ -e " + flag + " ]; then touch " + tried + "; echo 'try later' >&2; exit 1; fi\n"
	if err := os.WriteFile(filepath.Join(g.remote, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head := commitWIP(t, req, "wip")
		g.tick()
		g.clock.advance(runner.CheckpointQuiet)
		g.tick()
		if _, err := os.Stat(tried); err != nil || remoteHasBranch(t, g.harness) {
			t.Fatalf("the first push was not tried, or not refused (%v)", err)
		}
		if err := os.Remove(flag); err != nil {
			t.Fatal(err)
		}
		g.clock.advance(time.Minute)
		g.tick()
		if remoteHasBranch(t, g.harness) {
			t.Error("a failed push was retried before the fallback")
		}
		g.clock.advance(runner.CheckpointFallback)
		g.tick()
		if pushedHead(t, g.harness) != head {
			t.Error("the fallback did not retry the push")
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if n := strings.Count(g.logs.String(), "a checkpoint push failed"); n != 1 {
		t.Fatalf("%d push warnings, want 1:\n%s", n, g.logs)
	}
}

// TestIdlePollsNeverTouchTheRemote: once the tip is pushed, polls read
// only the checkout: with the remote gone, nothing fails and the provider
// is not called.
func TestIdlePollsNeverTouchTheRemote(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	pushes := countPushes(t, g.harness)
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "wip")
		g.settle()
		calls := len(g.provider.Snapshot().Calls)
		away := g.remote + ".away"
		if err := os.Rename(g.remote, away); err != nil {
			t.Fatal(err)
		}
		for range 5 {
			g.clock.advance(30 * time.Second)
			g.tick()
		}
		if err := os.Rename(away, g.remote); err != nil {
			t.Fatal(err)
		}
		if n := len(g.provider.Snapshot().Calls); n != calls || pushes() != 1 {
			t.Errorf("idle polls: provider calls %d -> %d, pushes %d", calls, n, pushes())
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, long, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(g.logs.String(), "level=WARN") {
		t.Fatalf("an idle poll warned:\n%s", g.logs)
	}
}

// TestStageEndStopsTheCheckpointer: each stage has exactly one
// checkpointer, and none outlives the run.
func TestStageEndStopsTheCheckpointer(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	one := func(t *testing.T) {
		if n := runner.CheckpointersRunning(); n != 1 {
			t.Errorf("%d checkpointers running in a stage, want 1", n)
		}
	}
	if _, err := g.run(t, probe(one, implement("feature")), probe(one, review("ship", 0))); err != nil {
		t.Fatal(err)
	}
	if n := runner.CheckpointersRunning(); n != 0 {
		t.Fatalf("%d checkpointers outlived the run", n)
	}
}

// TestCheckpointNeverRewritesThePushedBranch: after the agent amends a
// pushed commit, checkpoints refuse (one warning) and finalize pushes the
// final branch as it always did.
func TestCheckpointNeverRewritesThePushedBranch(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		first := commitWIP(t, req, "wip")
		g.settle()
		shell(t, req, "git commit -q --amend -m 'wip, amended'")
		g.settle()
		g.settle()
		if got := remoteTip(t, g.harness); got != first {
			t.Errorf("a checkpoint replaced the pushed commit: remote at %s, want %s", got, first)
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || remoteTip(t, g.harness) != rec.HeadSHA {
		t.Fatalf("finalize did not push the final branch: rec = %+v, err = %v", rec, err)
	}
	if n := strings.Count(g.logs.String(), "were rewritten"); n != 1 {
		t.Fatalf("%d rewrite warnings, want 1:\n%s", n, g.logs)
	}
}

// TestCheckpointSkipsWorkflowCommits: on GitHub a commit that changes a
// workflow file is never pushed by a checkpoint; finalize refuses as today.
func TestCheckpointSkipsWorkflowCommits(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	ci := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "mkdir -p .github/workflows && echo 'on: push' > .github/workflows/ci.yml && git add -A && git commit -qm 'Add CI'")
		g.settle()
		if remoteHasBranch(t, g.harness) {
			t.Error("a checkpoint pushed a workflow change")
		}
		return implementVerified(t, ctx, req)
	}
	runRefused(t, g.harness, ci, review("ship", 0))
	if n := strings.Count(g.logs.String(), "no more checkpoint pushes"); n != 1 {
		t.Fatalf("%d stop notices, want 1:\n%s", n, g.logs)
	}
}

// TestCheckpointStopsOnAKnownSecret: a commit holding a value the run
// redacts is never pushed mid-run, nor anything after it, even once a
// later commit removes the value.
func TestCheckpointStopsOnAKnownSecret(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	const mounted = "mounted-checkpoint-secret-value"
	g.deps.Env = append(g.deps.Env, "RENAMED_TOKEN="+mounted, runner.SecretEnvsVar+"=RENAMED_TOKEN")
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo "+mounted+" > leak.txt && git add -A && git commit -qm leak")
		g.settle()
		shell(t, req, "git rm -q leak.txt && git commit -qm unleak")
		g.settle()
		if remoteHasBranch(t, g.harness) {
			t.Error("a checkpoint pushed commits holding a secret value")
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, leak, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.logs.String(), "no more checkpoint pushes") || strings.Contains(g.logs.String(), mounted) {
		t.Fatalf("logs:\n%s", g.logs)
	}
}

// TestCheckpointsOffPushesNothingMidStage: git.pr.checkpoints false runs
// no checkpointer at all.
func TestCheckpointsOffPushesNothingMidStage(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", checkpoints: false"))
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if n := runner.CheckpointersRunning(); n != 0 {
			t.Errorf("%d checkpointers with checkpoints false", n)
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestNoCheckpointAfterHalt: once a halt is recorded, nothing is pushed
// mid-stage; finalize pushes as it does for any halt.
func TestNoCheckpointAfterHalt(t *testing.T) {
	b := newHandleBox(t)
	g := newCkptRig(t, prCfg(t, 2, ""))
	halted := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !b.h.HaltNow(runCapHalt) {
			t.Fatal("HaltNow was refused")
		}
		commitWIP(t, req, "wip")
		g.settle()
		if remoteHasBranch(t, g.harness) {
			t.Error("a checkpoint pushed after the halt")
		}
		return agent.Result{CostUSD: 1}, nil
	}
	rec, err := g.run(t, halted)
	if err != nil || rec.Status != runstore.StatusHalted {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestNoCheckpointAfterCancel: once a cancel is recorded, nothing is
// pushed mid-stage; finalize leaves the cancelled draft as today.
func TestNoCheckpointAfterCancel(t *testing.T) {
	b := newHandleBox(t)
	g := newCkptRig(t, prCfg(t, 2, ""))
	cancelled := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !b.h.MarkCancelled() {
			t.Fatal("MarkCancelled was refused")
		}
		commitWIP(t, req, "wip")
		g.settle()
		if remoteHasBranch(t, g.harness) {
			t.Error("a checkpoint pushed after the cancel")
		}
		if err := g.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		return blockUntilDone(t, ctx, req)
	}
	rec, err := g.run(t, cancelled)
	if err != nil || rec.Status != runstore.StatusCancelled {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestFollowUpDoesNotCheckpoint: a follow-up runs no checkpointer and
// pushes only at finalize, as in 0.5.0 (decision C6).
func TestFollowUpDoesNotCheckpoint(t *testing.T) {
	runner.DriveCheckpoints(t) // the first run's polls are never sent
	h := followUpHarness(t, "", nil, implement("feature"), review("ship", 0))
	start := remoteTip(t, h.harness)
	h.followUp(t, followID, runID, "")
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if n := runner.CheckpointersRunning(); n != 0 {
			t.Errorf("%d checkpointers in a follow-up", n)
		}
		commitWIP(t, req, "more")
		if got := remoteTip(t, h.harness); got != start {
			t.Errorf("the follow-up's branch moved mid-stage: %s -> %s", start, got)
		}
		return implement("again")(t, ctx, req)
	}
	rec, err := h.run(t, long, review("ship", 0))
	mustReady(t, rec, err)
}
