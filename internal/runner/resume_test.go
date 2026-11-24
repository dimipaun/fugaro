package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// atStage is s, after checking that the run is in stage when it is called: the
// record's own stage, written before the agent of the stage is handed its
// request. It is what says which stage a resume entered — a resumed run that
// ran implement where the pause named fix would have committed the same file.
func atStage(t *testing.T, h *harness, stage string, s step) step {
	t.Helper()
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		rec, err := h.store.ReadRecord(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Stage != stage {
			t.Fatalf("the run is in stage %q where the pause entered %q", rec.Stage, stage)
		}
		return s(t, ctx, req)
	}
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
		// steps builds what the resumed run's stages do, and it is built from
		// the harness because the case that proves WHICH stage the run entered
		// reads the run's record on the way in (atStage). A pause taken in the
		// review loop enters the loop itself — no implement stage runs, and the
		// round is scripted to ask for a fix, which is the stage that proves
		// the work chain is the restored session — and one taken in a fix round
		// enters that stage, where a pause taken in implement carries the work
		// on from that stage.
		steps func(*fuHarness) []step
		// wantReview is what the resumed run's first stage is.
		wantReview bool
	}{
		{stage: "implement", steps: func(*fuHarness) []step {
			return []step{implement("resumed"), review("ship", 0)}
		}},
		{stage: "fix", steps: func(h *fuHarness) []step {
			// The stage the run is in when the step below is called is the fix
			// stage and not implement: a pause taken in a fix round re-enters
			// THAT stage, in the session the round was in (resumeFix), which is
			// the whole of what separates this case from the one above it.
			return []step{atStage(t, h.harness, "fix", implement("fixed")), review("ship", 0)}
		}},
		{stage: "review", wantReview: true, steps: func(*fuHarness) []step {
			return []step{
				reviewScored("changes", map[string]any{"summary": "fix it", "likelihood": 2, "impact": 2, "fix_cost": 1, "class": "correctness"}),
				implement("fixed"), review("ship", 0)}
		}},
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
			rec, err := h.run(t, c.steps(h)...)
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

// TestAResumedRunKeepsTheChainsRuling: suspending a run does not end the
// answers the owner has given it. The paused run here worked from a ruling and
// the decisions it answered — its record carries the pair, as a run launched
// with --owner-ruled leaves them — and the resumed run, whose own launch
// carries no ruling, continues that chain: its reviewers are handed the same
// ruling and the same list of decisions, so a finding the owner already settled
// is not marked needs_owner again and does not stop the run on a question that
// was answered before it paused; and its own record carries the pair, so the
// follow-up after it inherits it too rather than losing it for good.
func TestAResumedRunKeepsTheChainsRuling(t *testing.T) {
	h := followUpHarness(t, criticalLevelsCfg(t), nil,
		implement("feature"), review("ship", 0), review("ship", 0), review("ship", 0))
	const ruling = "Counsel cleared the MIT text as it stands. Keep the approval flow; product will revisit it next quarter."
	const decision = "`LICENSE.md`: the licence text needs counsel's clearance"
	seedRuled(t, h, ruling, runstore.OwnerDecision{File: "LICENSE.md", Summary: "the licence text needs counsel's clearance"})
	paused(t, h, "implement")
	h.followUpSpec(t, resumeTask(h, "Carry the change on"))
	rec, err := h.run(t, implement("resumed"), review("ship", 0), review("ship", 0), review("ship", 0))
	mustReady(t, rec, err)
	if rec.OwnerRuled != ruling || rec.OwnerRuledDecisions == nil {
		t.Fatalf("the resumed run recorded ruling %q, decisions %+v; want the pair the paused run worked from",
			rec.OwnerRuled, rec.OwnerRuledDecisions)
	}
	// The brief the resumed run works from carries it, and so does every review
	// level: a reviewer told nothing about the ruling is the reviewer that puts
	// the settled question back to the owner.
	if !strings.Contains(h.agent.calls[0].Prompt, ruling) {
		t.Errorf("the resumed run's brief does not carry the paused run's ruling:\n%s", h.agent.calls[0].Prompt)
	}
	for i, level := range []string{"the first line", "the senior review", "the adversarial review"} {
		prompt := h.agent.calls[i+1].Prompt
		if !strings.Contains(prompt, ruling) {
			t.Errorf("%s's prompt does not carry the paused run's ruling verbatim:\n%s", level, prompt)
		}
		if !strings.Contains(prompt, decision) {
			t.Errorf("%s's prompt does not list the decision that ruling answers:\n%s", level, prompt)
		}
	}
}

// TestAResumedRunKeepsTheChainsRulings: a pause can be taken in a run that was
// itself already carrying a chain — a ruled follow-up that inherited the pair
// behind it (OwnerRuledEarlier), which is the shape every later run in a chain
// has (TestASecondRulingKeepsTheAnswerBeforeIt) — and the run that resumes it is
// the run after it. Carrying the newest pair alone is the loop OD1 exists to
// close, one run further out: the reviewers, shown an answer with no sign of the
// question behind it, mark that one needs_owner again, the resume stops on a
// question the owner settled two rulings ago, and the next ruling drops the
// older answer in turn. So the whole chain the paused run worked from reaches the
// resumed run's reviewers and lands on its own record, where the follow-up after
// it reads it back.
//
// The implementer works from the newest ruling alone, as it does for a follow-up
// (TestASecondRulingKeepsTheAnswerBeforeIt): the rulings behind it are what tells
// a reviewer which findings are already settled, not a second set of
// instructions for the work.
func TestAResumedRunKeepsTheChainsRulings(t *testing.T) {
	const (
		ruling  = "The approval flow stays as the product team built it."
		earlier = "Counsel cleared the MIT text as it stands."
		dec     = "`internal/approval/flow.go`: the approval flow is a product decision"
		decE    = "`LICENSE.md`: the licence text needs counsel's clearance"
	)
	h := followUpHarness(t, criticalLevelsCfg(t), nil,
		implement("feature"), review("ship", 0), review("ship", 0), review("ship", 0))
	seedRuled(t, h, ruling, runstore.OwnerDecision{File: "internal/approval/flow.go", Summary: "the approval flow is a product decision"})
	seedChain(t, h, runstore.OwnerRuling{Ruling: earlier,
		Decisions: runstore.NewOwnerDecisions([]runstore.OwnerDecision{{File: "LICENSE.md", Summary: "the licence text needs counsel's clearance"}})})
	paused(t, h, "implement")
	h.followUpSpec(t, resumeTask(h, "Carry the change on"))
	rec, err := h.run(t, implement("resumed"), review("ship", 0), review("ship", 0), review("ship", 0))
	mustReady(t, rec, err)
	// The resumed run's record holds the whole chain the paused run worked from:
	// the newest pair in the fields they have always had, the pair behind it in
	// the list a later follow-up inherits.
	if rec.OwnerRuled != ruling || rec.OwnerRuledDecisions == nil {
		t.Fatalf("the resumed run recorded ruling %q, decisions %+v; want the newest pair the paused run worked from",
			rec.OwnerRuled, rec.OwnerRuledDecisions)
	}
	if len(rec.OwnerRuledEarlier) != 1 {
		t.Fatalf("the resumed run holds %d rulings behind the newest one, want the one the paused run inherited: %+v",
			len(rec.OwnerRuledEarlier), rec.OwnerRuledEarlier)
	}
	if e := rec.OwnerRuledEarlier[0]; e.Ruling != earlier || e.Decisions == nil ||
		len(e.Decisions.Decisions) != 1 || !strings.Contains(e.Decisions.Decisions[0].Summary, "counsel") {
		t.Errorf("the resumed run's earlier ruling is %+v, want the pair behind the newest one", e)
	}
	// Every review level reads the whole chain, each ruling with the decision it
	// answered under it.
	for i, level := range []string{"the first line", "the senior review", "the adversarial review"} {
		prompt := h.agent.calls[i+1].Prompt
		for _, want := range []string{ruling, earlier, dec, decE} {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s's prompt is not told %q:\n%s", level, want, prompt)
			}
		}
		if n := strings.Count(prompt, "<<<fugaro-owner-ruling-"); n != 2 {
			t.Errorf("%s reads %d rulings, want both the chain holds:\n%s", level, n, prompt)
		}
	}
	// And the implementer works from the newest ruling only, as it does for a
	// follow-up: the answers behind it are the reviewers'.
	if got := h.agent.calls[0].Prompt; !strings.Contains(got, ruling) || strings.Contains(got, earlier) {
		t.Errorf("the resumed run's brief does not work from the newest ruling alone:\n%s", got)
	}
}

// TestAResumeNeverStartsTheStageAfresh is R18 where the AGENT is what refuses
// the session: Claude Code reports it cannot find the one it was handed, which
// a follow-up answers by starting a fresh session and carrying on (a follow-up
// that finds nothing is a follow-up — TestFollowUpResumeFailureFallsBackFresh)
// and a resumed run may not. The session it was given is the work the pause
// names, and the stage it entered is that work's own stage: a run that started
// a fresh session here would run the whole stage over a tree it did not
// understand, which is exactly the fresh start R18 refuses. So the run ends
// failed, once, and its record still says it resumed.
func TestAResumeNeverStartsTheStageAfresh(t *testing.T) {
	h := followUpHarness(t, "", nil)
	session := paused(t, h, "implement")
	h.followUpSpec(t, resumeTask(h, "Carry the change on"))
	noSession := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !req.Resume || req.SessionID != session {
			t.Errorf("the resumed stage did not ask for the restored session %s: session %q resume %v",
				session, req.SessionID, req.Resume)
		}
		return agent.Result{CostUSD: 0.01}, fmt.Errorf("%w: claude exited with code 1", agent.ErrNoSession)
	}
	rec, err := h.run(t, noSession)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rec.Status != runstore.StatusFailed || !strings.Contains(rec.Reason, "stage implement failed") {
		t.Fatalf("record = status %q, reason %q; want %q with the stage that failed named", rec.Status, rec.Reason, runstore.StatusFailed)
	}
	if n := len(h.agent.calls); n != 1 {
		t.Fatalf("the stage ran %d times: a second call is the fresh session R18 refuses", n)
	}
	if rec.FollowUp == nil || rec.FollowUp.Session != "resumed" {
		t.Fatalf("follow_up = %+v; want the session the pause named and not a fresh one", rec.FollowUp)
	}
}

// TestAResumeOfAnUnreadableRecordIsNamed is R18's other half: a pause this
// image cannot act on — a format version it does not read, a session that is
// not there, a checkpoint its checkout is not at, a stage it cannot enter —
// ends the run with a named outcome, and starts nothing: no stage runs, and
// the work the paused run left is not started afresh.
//
// Each case is a record the resume refuses for its OWN reason and for no other:
// the checkpoint case's session is restorable and its head is an ancestor of
// the checkpoint, and the stage case's record is whole and this checkout is the
// checkpoint it names — so a case fails if the rule it is about is relaxed,
// which is what makes them pins rather than four ways to reach the same
// refusal.
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
		breakIt: func(t *testing.T, h *fuHarness, _ string) {
			// The branch moved on since the run was suspended: a person pushed
			// to the pull request, and this run's checkout is their commit
			// rather than the one the pause named as the work in progress. The
			// saved session is the pause's own and restorable — its head is an
			// ancestor of the checkpoint — so nothing but the checkpoint check
			// refuses this run, which is the point: a resume that took the
			// session onto a checkout the paused run never worked at would be
			// carrying on work that is not there.
			h.pushForeign(t)
		},
	}, {
		name: "a stage this image cannot enter",
		breakIt: func(t *testing.T, h *fuHarness, session string) {
			// A record this image reads whole — the version, the session whose
			// file is saved, the checkpoint this checkout is at — naming a
			// stage a resume cannot enter from a standing start: what that
			// stage had half-done is in no record this release reads, and a run
			// that entered another stage instead would be the fresh start R18
			// refuses.
			if err := runstore.Open(h.bucket, repoSlug, runID).PutFile(context.Background(),
				runstore.PauseFile, []byte(`{"version": 1, "stage": "finalize", "session": "`+session+`", "checkpoint": "`+h.first.PushedHead+`"}`),
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
