package runner_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// resumeID is the run that picks up the pause the first run (runID) left, and
// pauseSession is the transcript that pause names.
const (
	resumeID     = "20260927-110000-a001"
	pauseSession = "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"Add a feature\"}}\n"
)

// paused leaves the first run (runID) suspended in stage, and returns the
// session its pause names: the session it saved under its own prefix, and the
// pause.json naming that session, the commit it pushed and the stage it was in.
// It is the pair the suspended run and the credentials service leave behind
// between them (R19, D3185), and the whole of what the run that resumes it
// reads.
func paused(t *testing.T, h *fuHarness, stage string) string {
	t.Helper()
	session, head := agent.NewSessionID(), h.first.PushedHead
	prev := runstore.Open(h.bucket, repoSlug, runID)
	m := runstore.SessionMeta{Version: 1, ID: session, HeadSHA: head, Pushed: true,
		WorkDir: h.deps.WorkDir, Bytes: int64(len(pauseSession))}
	if err := prev.PutSession(context.Background(), m, []byte(pauseSession)); err != nil {
		t.Fatal(err)
	}
	if err := prev.WritePause(context.Background(), runstore.PauseRecord{Version: runstore.PauseVersion,
		Stage: stage, Session: session, Checkpoint: head}); err != nil {
		t.Fatal(err)
	}
	return session
}

// resumeTask is the task a resumed run is launched with: the paused run's pull
// request and chain, and the member that names the pause it picks up. It is
// what the resident orchestrator writes for a node the credentials service
// recorded (PR4, D3185).
func resumeTask(h *fuHarness, text string) *task.Spec {
	return &task.Spec{Version: 1, RunID: resumeID, Repo: "acme/app", Ref: "main", Workflow: h.first.Workflow,
		Task: text, Branch: "fugaro/" + runID, PR: 1, PreviousRun: runID, Resumes: runID}
}

// resumedSessionFile is where the restored session's file has to be for the
// agent to resume it: the project directory Claude Code names after the
// checkout's real path.
func resumedSessionFile(t *testing.T, h *harness, session string) string {
	t.Helper()
	return filepath.Join(agent.SessionDir(envValue(h.deps.Env, "HOME"), realPath(t, h.deps.WorkDir)), session+".jsonl")
}

// TestAResumedRunRestoresTheSession is PR5: a run whose task names resumes
// reads the pause the run it continues left, finds its own checkout at the
// commit that pause names, puts the saved session in place, continues that
// session, and enters the stage the paused run was in rather than the first
// one.
func TestAResumedRunRestoresTheSession(t *testing.T) {
	cases := []struct {
		stage string
		// steps are what the resumed run's stages do. A pause taken in the
		// review loop enters the loop itself — no implement stage runs, and the
		// round is scripted to ask for a fix, which is the stage that proves
		// the work chain is the restored session — where a pause taken in
		// implement carries the work on from that stage.
		steps []step
		// wantReview is what the resumed run's first stage is.
		wantReview bool
	}{
		{stage: "implement", steps: []step{implement("resumed"), review("ship", 0)}},
		{stage: "review", wantReview: true, steps: []step{
			reviewScored("changes", map[string]any{"summary": "fix it", "likelihood": 2, "impact": 2, "fix_cost": 1, "class": "correctness"}),
			implement("fixed"), review("ship", 0)}},
	}
	for _, c := range cases {
		t.Run("a pause in "+c.stage, func(t *testing.T) {
			h := followUpHarness(t, "", nil)
			session := paused(t, h, c.stage)
			spec := resumeTask(h, "Carry the change on")
			// The task the launcher writes is one this release parses, and one
			// the member survives: a runner that did not know resumes refuses
			// the whole task at bootstrap (task.Parse decodes with
			// DisallowUnknownFields).
			data, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := task.Parse(data)
			if err != nil {
				t.Fatalf("task.Parse refused a resumed run's task: %v", err)
			}
			if parsed.Resumes != runID || parsed.PreviousRun != runID {
				t.Fatalf("parsed = resumes %q, previous_run %q; want %s", parsed.Resumes, parsed.PreviousRun, runID)
			}
			h.followUpSpec(t, spec)
			rec, err := h.run(t, c.steps...)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if len(h.agent.calls) < 2 {
				t.Fatalf("the resumed run made %d agent calls; want at least the stage it entered and the one after it", len(h.agent.calls))
			}
			first := h.agent.calls[0]
			if got := strings.Contains(first.Prompt, "Review the changes on this branch against"); got != c.wantReview {
				t.Fatalf("first call: review prompt = %v; want %v\n%s", got, c.wantReview, first.Prompt)
			}
			if !c.wantReview && !strings.Contains(first.Prompt, "You are resuming the session in which this branch was last worked on") {
				t.Fatalf("the work stage was not told it resumes the session:\n%s", first.Prompt)
			}
			if !c.wantReview && (!first.Resume || first.SessionID != session) {
				t.Fatalf("first call: session %q resume %v; want %s resumed", first.SessionID, first.Resume, session)
			}
			// A review round runs in a session of its own (C1.17, D138), so a
			// resume that entered the loop made its first call in a fresh one
			// — and the fix that round asks for continues the conversation the
			// resume restored, which is what says the restored session is the
			// run's work chain and not only a file in place.
			if c.wantReview {
				if first.Resume {
					t.Fatalf("the review stage resumed the work session %q", first.SessionID)
				}
				var continued bool
				for _, call := range h.agent.calls[1:] {
					continued = continued || (call.Resume && call.SessionID == session)
				}
				if !continued {
					t.Fatalf("no stage continued the restored session %s after the review stage: %d stages ran",
						session, len(h.agent.calls))
				}
			}
			if _, err := os.Stat(resumedSessionFile(t, h.harness, session)); err != nil {
				t.Fatalf("the saved session is not in place: %v", err)
			}
			// The chain: the resumed run continues the paused one, on the
			// branch the pause's checkpoint was pushed to.
			if rec.FollowUp == nil || rec.FollowUp.PreviousRun != runID || rec.FollowUp.Session != "resumed" {
				t.Fatalf("follow_up = %+v; want previous_run %s, session resumed", rec.FollowUp, runID)
			}
			if rec.Branch != "fugaro/"+runID {
				t.Fatalf("branch = %q; want %q", rec.Branch, "fugaro/"+runID)
			}
		})
	}
}

// TestAResumeOfAnUnreadableRecordIsNamed is R18's other half: a pause this
// image cannot act on — a format version it does not read, a session that is
// not there, a checkpoint its checkout is not at — ends the run with a named
// outcome, and starts nothing: no stage runs, and the work the paused run left
// is not started afresh.
func TestAResumeOfAnUnreadableRecordIsNamed(t *testing.T) {
	const want = "pause.json names no usable pause"
	cases := []struct {
		name string
		// break_ makes the pause unreadable once it is written, which is what a
		// release ahead of this image leaves, or a prefix whose session object
		// went missing, or a branch something else pushed to while the run was
		// suspended.
		breakIt func(t *testing.T, h *fuHarness, session string)
	}{{
		name: "a version this image does not read",
		breakIt: func(t *testing.T, h *fuHarness, session string) {
			raw := `{"version": 2, "stage": "implement", "session": "` + session + `", "checkpoint": "` + h.first.PushedHead + `"}`
			if err := runstore.Open(h.bucket, repoSlug, runID).PutFile(context.Background(),
				runstore.PauseFile, []byte(raw), "application/json"); err != nil {
				t.Fatal(err)
			}
		},
	}, {
		name: "a session that is not there",
		breakIt: func(t *testing.T, h *fuHarness, _ string) {
			// The record's own shape is fine and it names a session; nothing
			// was saved under that one.
			if err := runstore.Open(h.bucket, repoSlug, runID).PutFile(context.Background(),
				runstore.PauseFile, []byte(`{"version": 1, "stage": "implement", "session": "`+agent.NewSessionID()+`", "checkpoint": "`+h.first.PushedHead+`"}`),
				"application/json"); err != nil {
				t.Fatal(err)
			}
		},
	}, {
		name: "a head that is not the checkpoint",
		breakIt: func(t *testing.T, h *fuHarness, session string) {
			// The branch moved on since the run was suspended — a person pushed
			// to the pull request, or another run did — so this checkout is not
			// the work in progress the pause names.
			if err := runstore.Open(h.bucket, repoSlug, runID).PutFile(context.Background(),
				runstore.PauseFile, []byte(`{"version": 1, "stage": "implement", "session": "`+session+`", "checkpoint": "`+strings.Repeat("a", 40)+`"}`),
				"application/json"); err != nil {
				t.Fatal(err)
			}
		},
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := followUpHarness(t, "", nil)
			session := paused(t, h, "implement")
			c.breakIt(t, h, session)
			h.followUpSpec(t, resumeTask(h, "Carry the change on"))
			rec, err := h.run(t, implement("resumed"), review("ship", 0))
			if err == nil {
				t.Fatalf("the run ended %s with no error", rec.Status)
			}
			if rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, want) {
				t.Fatalf("record = status %q, reason %q; want %q naming %q", rec.Status, rec.Reason, runstore.StatusInfraError, want)
			}
			if n := len(h.agent.calls); n != 0 {
				t.Fatalf("%d agent calls for a pause this image cannot act on: the stage was started afresh", n)
			}
		})
	}
}
