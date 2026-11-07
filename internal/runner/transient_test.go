package runner_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// Issue #1827: a model stream closed before its response finished ends a
// stage whose work was fine, and the run loses everything the stage had
// committed. These tests pin the runner's answer: one retry of that stage,
// in the session it already had, bounded on both sides, for that failure and
// for nothing else.

// streamClosedText is what Claude Code prints when the stream it was reading
// ended before the response finished: an error result whose subtype is
// still "success".
const streamClosedText = "API Error: stream closed before completion"

// cutStream is a streaming reply whose connection is dropped after n events:
// the model's answer never reaches its terminal event, and the agent reading
// it sees a stream that closed early.
func cutStream(n int) anthropicfake.Reply {
	rep := anthropicfake.StreamOK(sonnet, pricing.Usage{Input: 100, Output: 4000})
	rep.CutAfter = n
	return rep
}

// postStream sends one streaming Messages request as Claude Code would and
// returns its status. The body is a stream, and a cut one ends mid-read --
// which is the point.
func postStream(t *testing.T, req agent.Request, model string, maxTokens int64) int {
	t.Helper()
	base, key := gwEnv(req)
	body := fmt.Sprintf(`{"model":%q,"max_tokens":%d,"stream":true,"messages":[{"role":"user","content":"hello"}]}`, model, maxTokens)
	status, _ := postBody(t, base+"/v1/messages", key, body)
	return status
}

// closedStream is a stage attempt that loses its stream, then reports the
// error Claude Code prints for it. tail is appended to the text, so a test
// can watch for a run's own words reaching a line or a record.
func closedStream(tail string) step { return closedStreamStatus(nil, tail) }

// closedStreamStatus is closedStream, recording the call's status when
// statuses is not nil (the same way calling does).
func closedStreamStatus(statuses *[]int, tail string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		got := postStream(t, req, sonnet, 4000)
		if statuses != nil {
			*statuses = append(*statuses, got)
		}
		if got != http.StatusOK {
			t.Errorf("the cut call answered %d, want 200: the status arrived before the stream was cut", got)
		}
		return agent.Result{Text: streamClosedText + tail, IsError: true, Subtype: "success"}, nil
	}
}

// retryLine is the last line the runner logged about retrying a stage, and
// the raw line itself (for the no-content checks).
func retryLine(t *testing.T, logs *lockedBuf) string {
	t.Helper()
	var raw string
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if strings.Contains(line, "running the stage once more") {
			raw = line
		}
	}
	if raw == "" {
		t.Fatalf("no retry line logged; log:\n%s", logs.String())
	}
	return raw
}

// A closed stream is retried once, in the session the ended attempt had, and
// the second attempt's result is the one the run goes on with: the stage's
// own work — the commit it made before its stream was cut — is kept.
func TestClosedStreamRetriesTheStageOnce(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "", cutStream(3), out4k())
	var st []int
	rec, err := g.run(t,
		closedStream(""),
		calling(&st, implement("feature"), call{sonnet, 4000}),
		review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Retries != 1 {
		t.Errorf("retries = %d, want 1: the stage was run a second time", rec.Retries)
	}
	if len(g.agent.calls) != 3 {
		t.Fatalf("the agent was called %d times, want implement twice and review once", len(g.agent.calls))
	}
	first, retry := g.agent.calls[0], g.agent.calls[1]
	if !retry.Resume || retry.SessionID == "" || retry.SessionID != first.SessionID {
		t.Errorf("the retry ran in session %q (resume %v), want the ended attempt's %q resumed",
			retry.SessionID, retry.Resume, first.SessionID)
	}
	if line := retryLine(t, g.logs); !strings.Contains(line, "stage=implement") || !strings.Contains(line, "retries=1") {
		t.Errorf("the retry line names neither the stage nor the count: %s", line)
	}
	// The record with its new count is one the published schema accepts.
	assertStoredRecordValidates(t, g.store)
	// The stage's own work is on the branch the run pushed: the retry is the
	// same stage in the same checkout, and implement committed feature.txt.
	if got := testutil.Git(t, g.remote, "log", "-1", "--format=%s", "refs/heads/fugaro/"+runID); got != "Add feature" {
		t.Errorf("the branch's last commit is %q, want the retried stage's own", got)
	}
}

// Two closed streams in a row are the failure the stage always had: the
// second attempt records it, with the agent's own words, and no third
// attempt is made.
func TestTwoClosedStreamsFailTheStage(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "", cutStream(3), cutStream(3))
	rec, _ := g.run(t, closedStream(""), closedStream(""))
	if rec.Status != runstore.StatusFailed {
		t.Fatalf("rec = %+v, want the stage failed after its one retry", rec)
	}
	if len(g.agent.calls) != 2 {
		t.Fatalf("the stage ran %d times, want twice and no more", len(g.agent.calls))
	}
	if rec.Retries != 1 {
		t.Errorf("retries = %d, want the one retry the stage spent", rec.Retries)
	}
	if !strings.Contains(rec.Reason, streamClosedText) {
		t.Errorf("reason = %q, want the agent's own words for the failure", rec.Reason)
	}
}

// The second bound is the run's, not the stage's: a later stage that loses a
// stream of its own is not retried, because the run's one retry is already
// spent. It fails on its own error, with the agent's own words for it, and
// the run ends there -- retrying it would make the bound a per-stage one.
func TestTheRunsOneRetryIsSpentOnTheFirstStage(t *testing.T) {
	// The cut implement attempt, the implement retry's call, then the review
	// stage's own cut stream.
	g := newGW(t, gwConfig(t, ""), "observe", "", cutStream(3), out4k(), cutStream(3))
	var st []int
	rec, err := g.run(t,
		closedStream(""),
		calling(&st, implement("feature"), call{sonnet, 4000}),
		closedStream(""))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed {
		t.Fatalf("rec = %+v, want the review stage failed on the stream it lost", rec)
	}
	if rec.Retries != 1 {
		t.Errorf("retries = %d, want 1: the run retries one stage, and the implement stage already spent it", rec.Retries)
	}
	if len(g.agent.calls) != 3 {
		t.Errorf("the agent was called %d times, want implement twice and review once: the review stage's cut stream is not the retry's to take",
			len(g.agent.calls))
	}
	if !strings.Contains(rec.Reason, streamClosedText) {
		t.Errorf("reason = %q, want the review stage's own failure, which is what the run failed for", rec.Reason)
	}
	if len(st) != 1 || st[0] != http.StatusOK {
		t.Errorf("statuses = %v, want the implement retry's one served call", st)
	}
	// The review stage's cut call is the upstream's last: no attempt followed it.
	if g.fake.Count() != 3 {
		t.Errorf("the upstream saw %d calls, want the three scripted ones", g.fake.Count())
	}
}

// A stage error that is not a closed stream is not retried, however
// transient it looks: the list is closed, and a run's money, its credential
// and its clock are never spent on a second attempt.
func TestOtherStageErrorsAreNotRetried(t *testing.T) {
	errs := []string{
		"API Error: 400 invalid_request_error: max_tokens: field required",
		"API Error: 401 authentication_error: invalid x-api-key",
		"fugaro: budget halted: run cap $5.00 reached ($5.00 spent)",
		"Claude AI usage limit reached",
		"the stage ran out of time",
	}
	// Twice: on the words alone, and after a streamed call of the same
	// attempt was cut. The gateway's own count is the second half of the
	// trigger, and it says a stream of this stage ended early -- not that the
	// stream is what ended the attempt. A stage that lost one call and then
	// failed for a reason of its own fails for that reason, and the count
	// does not turn an auth error, a work failure or a cap into a retry.
	for _, cut := range []bool{false, true} {
		for _, text := range errs {
			g := newGW(t, gwConfig(t, ""), "observe", "", cutStream(3), out4k())
			step := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
				if cut {
					if got := postStream(t, req, sonnet, 4000); got != http.StatusOK {
						t.Errorf("the cut call answered %d, want 200", got)
					}
				}
				return agent.Result{Text: text, IsError: true, Subtype: "success"}, nil
			}
			rec, _ := g.run(t, step)
			if rec.Status != runstore.StatusFailed {
				t.Fatalf("%q (cut %v): rec = %+v", text, cut, rec)
			}
			if rec.Retries != 0 || len(g.agent.calls) != 1 {
				t.Fatalf("%q (cut %v): retried %d times over %d calls, want none", text, cut, rec.Retries, len(g.agent.calls))
			}
			if cut && !strings.Contains(rec.Reason, text) {
				t.Errorf("%q: reason = %q, want the stage's own failure", text, rec.Reason)
			}
		}
	}
}

// A stage the run's own timeouts.stage ended is not retried, even when a
// streamed call was cut while it ran: the cut stream is a fact about the
// attempt's connection, and the clock is a fact about the run. Retrying a
// stage the clock stopped would spend a second model call and a fresh stage
// window on a run that is out of time -- which is what the bound is there to
// prevent -- and would spend the run's one retry, so that a later, genuine
// cut stream is not retried at all.
func TestStageTimeoutIsNotRetried(t *testing.T) {
	cfg := gwConfig(t, "")
	cut := strings.Replace(cfg, "timeouts: { total: 5m, stage: 2m,", "timeouts: { total: 5m, stage: 1s,", 1)
	if cut == cfg {
		t.Fatal("the fixture's workflow has no `stage: 2m` timeout to shorten to 1s")
	}
	g := newGW(t, cut, "observe", "", cutStream(3), cutStream(3))
	// The attempt makes one streamed call that is cut, then blocks until its
	// stage context ends: what the agent's own process does when the stage's
	// bound fires.
	timedOut := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if got := postStream(t, req, sonnet, 4000); got != http.StatusOK {
			t.Errorf("the cut call answered %d, want 200", got)
		}
		<-ctx.Done()
		return agent.Result{}, ctx.Err()
	}
	rec, err := g.run(t, timedOut, timedOut)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed {
		t.Fatalf("rec = %+v, want the stage failed on its timeout", rec)
	}
	if rec.Retries != 0 {
		t.Errorf("retries = %d, want 0: a stage the clock ended is not the retry's to take", rec.Retries)
	}
	if len(g.agent.calls) != 1 {
		t.Errorf("the agent was called %d times, want once: the cut stream during a stage the clock ended is not retried", len(g.agent.calls))
	}
	if !strings.Contains(rec.Reason, "timed out after 1s") {
		t.Errorf("reason = %q, want the stage's own timeout, which is what the run failed for", rec.Reason)
	}
}

// The gateway's own count is the other half of the trigger: an attempt that
// lost a streamed call and then failed without any words for it -- a killed
// process, an error exit -- is the same failure, and gets the same retry.
func TestLostStreamWithoutWordsIsRetried(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "observe", "", cutStream(3), out4k())
	lost := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if got := postStream(t, req, sonnet, 4000); got != http.StatusOK {
			t.Errorf("the cut call answered %d, want 200", got)
		}
		return agent.Result{}, errors.New("claude exited with code 1 without a result event")
	}
	var st []int
	rec, err := g.run(t,
		lost,
		calling(&st, implement("feature"), call{sonnet, 4000}),
		review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.Retries != 1 || len(g.agent.calls) != 3 {
		t.Fatalf("retries = %d over %d calls, want one retry", rec.Retries, len(g.agent.calls))
	}
}

// The retry is a stage like any other: its calls are reserved and charged by
// the gateway, so a run whose retry would take it past its cap is refused
// and halted exactly as any other call past the cap is. The retry is not a
// way around the budget.
func TestRetryRespectsTheRunCap(t *testing.T) {
	g := newGW(t, gwConfig(t, ""), "enforce", "0.05", cutStream(3), out4k())
	var st []int
	// The first attempt's call settles at its reservation's worth (~$0.04),
	// so the retry's own call cannot fit under the $0.05 cap: the gateway
	// refuses it with a 403 and halts the run.
	retry := calling(&st, func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		return agent.Result{Text: streamClosedText, IsError: true, Subtype: "success"}, nil
	}, call{sonnet, 4000})
	rec, err := g.run(t, closedStreamStatus(&st, ""), retry)
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || st[0] != http.StatusOK || st[1] != http.StatusForbidden {
		t.Fatalf("statuses = %v, want the first call served and the retry's refused", st)
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltRunCap {
		t.Fatalf("rec = %+v, want the run halted on its cap", rec)
	}
	if rec.Retries != 1 {
		t.Errorf("retries = %d, want the one the run spent before the cap refused it", rec.Retries)
	}
	// The refusal is the cap's: the retry never reached the upstream.
	if g.fake.Count() != 1 {
		t.Errorf("the upstream saw %d calls, want only the first attempt's", g.fake.Count())
	}
}

// The retry is a stage the run pays for twice, and the run's token ceiling
// has to see both attempts: the caller's own countTokens only ever sees the
// last attempt of a stage (countTokens, halt.go), so the ended attempt's
// tokens are added where it ends, in stage's transient branch, before the
// retry.
//
// The figures are chosen so the under-count changes the outcome: the ended
// attempt and its retry each report 40 tokens and the review reports 40, so
// the run's total is 120 and the 100-token ceiling halts it. A total that lost
// the ended attempt's 40 stops at 80, and the run succeeds.
func TestTheRetryCountsTheEndedAttemptsTokens(t *testing.T) {
	h := newHarness(t, tokenCfg(t, "100"), nil)
	cut := withUsage(func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		return agent.Result{Text: streamClosedText, IsError: true, Subtype: "success"}, nil
	}, agent.Usage{Input: 40}, nil)
	rec, err := h.run(t,
		cut,
		withUsage(implement("feature"), agent.Usage{Input: 40}, nil),
		withUsage(review("ship", 0), agent.Usage{Input: 40}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Retries != 1 || len(h.agent.calls) != 3 {
		t.Fatalf("retries = %d over %d calls, want the implement stage's one retry and the review", rec.Retries, len(h.agent.calls))
	}
	if rec.Status != runstore.StatusHalted || rec.Halt == nil || rec.Halt.Reason != runstore.HaltTokenCap || rec.Halt.Scope != "run" {
		t.Fatalf("rec = %+v, halt = %+v; want the run halted by its token ceiling", rec, rec.Halt)
	}
	if want := "run used 120 tokens of 100"; rec.Halt.Detail != want {
		t.Fatalf("detail = %q, want %q: the ended attempt's tokens are not in the run's total", rec.Halt.Detail, want)
	}
}

// The retry line names the stage and the run's count and carries nothing
// else: no text the agent wrote, no prompt, no credential.
func TestRetryLineCarriesNoContent(t *testing.T) {
	const nonce = "NONCE-STAGETEXT-f3a1c9"
	g := newGW(t, gwConfig(t, ""), "observe", "", cutStream(3), out4k())
	var st []int
	rec, err := g.run(t,
		closedStream(" "+nonce),
		calling(&st, implement("feature"), call{sonnet, 4000}),
		review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	line := retryLine(t, g.logs)
	for _, bad := range []string{nonce, streamClosedText, plantedKey, "hello", "x-api-key"} {
		if strings.Contains(line, bad) {
			t.Errorf("the retry line carries %q: %s", bad, line)
		}
	}
}
