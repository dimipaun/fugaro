// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package governance

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/tier"
)

// The limits of plan T7.2, against a gateway that enforces them: what a stage
// may spend in money and tokens, what review may spend on the implementation it
// read, how far a stage may fan out, and the checkpoint stage's one call. Each
// test drives the real gateway — its real reservation, settlement, halt channel
// and stage reports — with a fake upstream, because the limits read the figures
// the gateway keeps and a limit tested against a hand-built view would pin
// arithmetic and not the run's behaviour.
//
// The runner's own tests cannot express this: internal/runner's harness has no
// gateway (runnertest), and internal/gateway's is an in-package one. So the
// frame below is built from the gateway's exported API, which is also the API
// ee/register's builder is written against.

// sonnet is the model these tests pin their stages to: the embedded table
// prices it at $2 in and $10 out per million tokens, which is the figure the
// money assertions below are computed from.
const sonnet = "claude-sonnet-5-5"

// gatewayKey stands in for a run's model credential. It is a fixture: the fake
// upstream is the only thing it reaches, and the gateway logs no header.
const gatewayKey = "sk-ant-api03-TESTKEY-0123456789abcdefghijklmnopqrstuvwxyz"

// dollar is one dollar in the gateway's micro-dollars, so a cap and a usage
// below read as the amounts a repository writes.
const dollar = pricing.Micros(1_000_000)

// mainSession is the session ID the main agent's calls carry. Check 139 found
// that a sub-agent's calls carry their stage's own session ID, so nothing here
// distinguishes the two by session — which is the point of the fan-out test.
const mainSession = "sess-main"

// charged is what one call that reports a million input tokens and one output
// token settles: $2.00 plus a hundredth of a cent, which is what spends an
// implement budget of 0.1 of a $20 cap to the last micro-dollar.
const charged = pricing.Micros(2_000_010)

// limitRun is one gateway under test: the fake upstream it forwards to, the
// gateway with this file's limit installed as the runner installs it, and the
// halt it published.
type limitRun struct {
	t    *testing.T
	gw   *gateway.Server
	fake *anthropicfake.Fake
	// published is the halt the gateway handed over, and haltRead is whether
	// this frame has read it: the channel carries one value and is then closed.
	published gateway.Halt
	haltRead  bool
}

// newLimitRun starts a gateway enforcing g at cap for a run dispatched at tr,
// with the embedded price table and a log that is discarded, which is the run's
// own gateway in everything but the upstream it forwards to. Its upstream
// answers every call cheaply until the test says otherwise.
func newLimitRun(t *testing.T, g policy.Governance, tr tier.Tier, cap pricing.Micros) *limitRun {
	t.Helper()
	fake, up := anthropicfake.New(t)
	gw, err := gateway.Start(context.Background(), gateway.Options{
		Upstream:   gateway.Upstream{Kind: "anthropic", BaseURL: up.URL, APIKey: gatewayKey},
		Prices:     pricing.Embedded(),
		Mode:       gateway.Enforce,
		Cap:        cap,
		Log:        slog.New(slog.DiscardHandler),
		StageLimit: StageLimit(g, tr, cap),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = gw.Close(ctx)
	})
	r := &limitRun{t: t, gw: gw, fake: fake}
	r.answer(pricing.Usage{Input: 1, Output: 1})
	return r
}

// answer makes the upstream report these usages, in order, the last of them for
// every call past the list: what a stage spent is a figure the test chooses, so
// the assertions below are arithmetic rather than a fixture.
func (r *limitRun) answer(us ...pricing.Usage) {
	i := 0
	r.fake.Func = func(*http.Request, []byte) anthropicfake.Reply {
		u := us[min(i, len(us)-1)]
		i++
		return anthropicfake.MessageOK(sonnet, u)
	}
}

// stage begins a stage of the run: the gateway allows the model a stage pins
// and holds this stage's report, which is what the limits read.
func (r *limitRun) stage(name string) {
	r.gw.BeginStage(gateway.Stage{Name: name, Model: sonnet})
}

// end ends the running stage and answers its report.
func (r *limitRun) end() gateway.StageReport { return r.gw.EndStage() }

// call makes one Messages call as agentID in sessionID and answers its status
// and body. An empty agent ID is the main session, which is what Claude Code
// sends for it.
func (r *limitRun) call(agentID, sessionID string) (int, string) {
	r.t.Helper()
	body := fmt.Sprintf(`{"model":%q,"max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`, sonnet)
	req, err := http.NewRequest("POST", r.gw.URL()+"/v1/messages", strings.NewReader(body))
	if err != nil {
		r.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", r.gw.Token())
	if agentID != "" {
		req.Header.Set("x-claude-code-agent-id", agentID)
	}
	if sessionID != "" {
		req.Header.Set("x-claude-code-session-id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatalf("the call did not reach the gateway: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// ok makes one call and fails unless the gateway admitted it.
func (r *limitRun) ok(agentID, sessionID string) {
	r.t.Helper()
	if code, body := r.call(agentID, sessionID); code != http.StatusOK {
		r.t.Fatalf("the call was answered %d, want 200: %s", code, body)
	}
}

// refused makes one call and fails unless the gateway refused it with a 403
// carrying want, which is the sentence a run's agent is told why with.
func (r *limitRun) refused(agentID, sessionID, want string) string {
	r.t.Helper()
	code, body := r.call(agentID, sessionID)
	if code != http.StatusForbidden {
		r.t.Fatalf("the call was answered %d, want 403 with %q: %s", code, want, body)
	}
	if !strings.Contains(body, want) {
		r.t.Fatalf("the refusal does not carry %q: %s", want, body)
	}
	return body
}

// halt is the halt the gateway published, read once: the channel carries one
// value and is then closed, exactly as the runner's own watcher reads it.
func (r *limitRun) halt() gateway.Halt {
	r.t.Helper()
	if !r.haltRead {
		select {
		case h := <-r.gw.Halted():
			r.published, r.haltRead = h, true
		case <-time.After(10 * time.Second):
			r.t.Fatal("the gateway published no halt")
		}
	}
	return r.published
}

// noHalt fails when the gateway published a halt: a limit that admits a call
// must leave the run's halt alone.
func (r *limitRun) noHalt() {
	r.t.Helper()
	select {
	case h := <-r.gw.Halted():
		r.published, r.haltRead = h, true
		r.t.Fatalf("the gateway halted with %+v, want no halt", h)
	default:
	}
}

// TestStageBudgetHalts is the dollar guardrail (spec §2, T7.2): a stage that
// has spent its own budget refuses the next call, with the reason the run's
// record carries for it, and every later call of that stage reads the same
// sentence.
func TestStageBudgetHalts(t *testing.T) {
	// 0.1 of a $20 cap is $2.00, and one call reporting a million input tokens
	// spends it: the call about to be made is what the budget is checked
	// against, so the stage stops before it spends a cent past its share.
	g := policy.Governance{StageBudget: policy.StageBudget{Implement: 0.1}}
	r := newLimitRun(t, g, tier.Small, 20*dollar)
	r.answer(pricing.Usage{Input: 1_000_000, Output: 1})
	r.stage("implement")
	r.ok("", mainSession)
	body := r.refused("", mainSession, "implement stage budget $2.00 reached")
	if !strings.Contains(body, "budget halted") {
		t.Errorf("the refusal does not read as the halt it is: %s", body)
	}
	r.refused("", mainSession, "implement stage budget $2.00 reached")

	h := r.halt()
	if h.Reason != string(runstore.HaltStageBudget) {
		t.Errorf("the halt's reason is %q, want %q: a reader of the run has to learn which guardrail stopped it", h.Reason, runstore.HaltStageBudget)
	}
	if !strings.Contains(h.Detail, "$2.00 spent") {
		t.Errorf("the halt's detail is %q, want the stage's own figure in it", h.Detail)
	}
	rep := r.end()
	if rep.Calls != 1 || rep.Used != charged {
		t.Errorf("the stage's report is %d calls and %d micro-dollars, want one call that settled %d", rep.Calls, int64(rep.Used), int64(charged))
	}
}

// TestStageTokensHalt is the token ceiling (T7.2): a ceiling a layer set bounds
// the stage on what its calls reported, which is the only figure a call's token
// count can be.
func TestStageTokensHalt(t *testing.T) {
	// No money budget at all: the ceiling is the whole rule.
	g := policy.Governance{StageTokens: policy.StageTokens{Implement: 1_000}}
	r := newLimitRun(t, g, tier.Small, 20*dollar)
	r.answer(pricing.Usage{Input: 1_000_000, Output: 1})
	r.stage("implement")
	r.ok("", mainSession)
	r.refused("", mainSession, "implement stage token ceiling 1000 passed (1000001 tokens spent)")
	if h := r.halt(); h.Reason != string(runstore.HaltStageBudget) {
		t.Errorf("the halt's reason is %q, want %q", h.Reason, runstore.HaltStageBudget)
	}
}

// TestReviewRatioHaltsAboveFloorOnly is the ratio guardrail (spec §2, T7.2):
// review calls stop at max(floor, N x the implement stage's calls), so the
// floor is what a short implementation is reviewed under and the ratio is what
// a long one is.
func TestReviewRatioHaltsAboveFloorOnly(t *testing.T) {
	for _, tc := range []struct {
		name       string
		implCalls  int
		wantCalls  int
		wantDetail string
	}{
		{"the floor", 1, 3, "review calls (3) reached max(3, 2 x 1 implement calls)"},
		{"the ratio", 4, 8, "review calls (8) reached max(3, 2 x 4 implement calls)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := policy.Governance{ReviewRatio: 2, ReviewFloorCalls: 3}
			r := newLimitRun(t, g, tier.Small, 50*dollar)
			r.stage("implement")
			for i := 0; i < tc.implCalls; i++ {
				r.ok("", mainSession)
			}
			r.end()
			r.stage("review")
			for i := 0; i < tc.wantCalls; i++ {
				r.ok("", mainSession)
			}
			r.refused("", mainSession, tc.wantDetail)
			if h := r.halt(); h.Reason != string(runstore.HaltReviewRatio) {
				t.Errorf("the halt's reason is %q, want %q", h.Reason, runstore.HaltReviewRatio)
			}
		})
	}
}

// TestFanoutHalts is the fan-out guardrail (spec §5, T7.2): a stage that used
// more distinct sub-agents than it may stops. The count is the gateway's — the
// agent IDs its calls carried — and the sub-agent that crosses the maximum is
// admitted its first call, because that call is the only evidence there is that
// the sub-agent exists.
func TestFanoutHalts(t *testing.T) {
	// No tier, so the governance block's own maximum is the rule.
	g := policy.Governance{MaxSubagents: 2}
	r := newLimitRun(t, g, tier.Tier(""), 50*dollar)
	r.stage("implement")
	// The main session is no sub-agent, however many calls it makes.
	r.ok("", mainSession)
	r.ok("", mainSession)
	for _, agent := range []string{"sub-1", "sub-2"} {
		r.ok(agent, mainSession)
	}
	r.ok("sub-3", mainSession)
	r.refused("sub-1", mainSession, "implement stage used 3 distinct sub-agents, its maximum is 2")
	if h := r.halt(); h.Reason != string(runstore.HaltFanout) {
		t.Errorf("the halt's reason is %q, want %q", h.Reason, runstore.HaltFanout)
	}
	if rep := r.end(); rep.Agents != 3 {
		t.Errorf("the stage's report counts %d sub-agents, want the 3 that made a call", rep.Agents)
	}
}

// TestFanoutCountsAgentIDsNotSessions: the fan-out count is of distinct
// non-empty agent IDs and never of sessions (Check 139's answer, U4). The
// owner's live run found that a sub-agent's calls carry their stage's own
// session ID, so a session cannot tell a sub-agent from the main session — two
// sub-agents in one session, and the main session beside them, are three
// different things — while an agent ID can: an empty one is the main session
// and never a sub-agent, and each sub-agent carries one of its own.
func TestFanoutCountsAgentIDsNotSessions(t *testing.T) {
	g := policy.Governance{MaxSubagents: 1}
	r := newLimitRun(t, g, tier.Tier(""), 50*dollar)
	r.stage("implement")
	// The main session's calls, in one session and again under another: none
	// of them is a sub-agent, and a session that differs changes nothing.
	r.ok("", mainSession)
	r.ok("", mainSession)
	r.ok("", "sess-other")
	// One sub-agent, and then more of the main session's calls in that same
	// session: the empty agent ID is what keeps them from counting.
	r.ok("sub-1", mainSession)
	r.ok("", mainSession)
	r.ok("", mainSession)
	// A second sub-agent whose calls carry the SAME session as the first,
	// which is what a live run's sub-agents do. It is a second distinct agent
	// ID, so it crosses the maximum of 1 and the next call is refused.
	r.ok("sub-2", mainSession)
	r.refused("sub-2", mainSession, "implement stage used 2 distinct sub-agents, its maximum is 1")
	if rep := r.end(); rep.Agents != 2 {
		t.Errorf("the stage's report counts %d sub-agents, want 2: a session shared with the main session, or with another sub-agent, is not what makes a sub-agent", rep.Agents)
	}
}

// TestReviewOnlyRunHasNoRatio is reconciliation F6, pinned: a review-only run
// reviews a branch somebody else wrote, so it has no implement stage and no
// calls of its own to measure review against. The ratio does not apply at all,
// rather than applying against a count of zero — which would stop the review
// stage on its floor alone, a bound about a stage that never ran.
func TestReviewOnlyRunHasNoRatio(t *testing.T) {
	g := policy.Governance{ReviewRatio: 1, ReviewFloorCalls: 1}
	r := newLimitRun(t, g, tier.Small, 50*dollar)
	r.stage("review")
	for i := 0; i < 3; i++ {
		r.ok("", mainSession)
	}
	r.noHalt()
	if rep := r.end(); rep.Calls != 3 {
		t.Errorf("the review stage's report is %d calls, want all 3", rep.Calls)
	}
}

// TestCheckpointAllowanceOneCallWithinCap is the checkpoint stage (T6.1, T7.2):
// the one call that fills a halted run's checkpoint report is admitted, a second
// is not, and the run's own cap still decides whether even the first fits.
func TestCheckpointAllowanceOneCallWithinCap(t *testing.T) {
	t.Run("one call", func(t *testing.T) {
		r := newLimitRun(t, policy.Governance{}, tier.Small, 20*dollar)
		r.stage(CheckpointStage)
		r.ok("", mainSession)
		r.refused("", mainSession, "the checkpoint stage admits one call and has already made 1")
	})
	t.Run("the cap still decides", func(t *testing.T) {
		// A cap no call fits under refuses the checkpoint call like any other:
		// the allowance is one call, and never a call the run cannot pay for.
		r := newLimitRun(t, policy.Governance{}, tier.Small, 100)
		r.stage(CheckpointStage)
		r.refused("", mainSession, "run cap")
		if h := r.halt(); h.Reason != "run_cap" {
			t.Errorf("the halt's reason is %q, want run_cap: the cap is what stopped it", h.Reason)
		}
	})
}

// TestNoCheckpointAllowanceAfterCapHalt: a run-wide halt is what every later
// call reads, the checkpoint stage included. The checkpoint's allowance is of
// its own stage's calls and is not a way past a halt the run already carries.
func TestNoCheckpointAllowanceAfterCapHalt(t *testing.T) {
	r := newLimitRun(t, policy.Governance{}, tier.Small, 100)
	r.stage("implement")
	r.refused("", mainSession, "run cap")
	if h := r.halt(); h.Reason != "run_cap" {
		t.Fatalf("the halt's reason is %q, want run_cap", h.Reason)
	}
	r.stage(CheckpointStage)
	body := r.refused("", mainSession, "run cap")
	if strings.Contains(body, "checkpoint stage admits") {
		t.Errorf("the checkpoint stage's own allowance answered a halted run: %s", body)
	}
}

// TestCheckpointStageRefusesStoppedStageCalls: a governance stop is scoped to
// the stage it stopped. The stopped stage's calls keep reading its sentence —
// including a stage of that name the runner opens again — while the checkpoint
// stage is a stage of its own and gets the one call T7.2 leaves the run.
func TestCheckpointStageRefusesStoppedStageCalls(t *testing.T) {
	g := policy.Governance{StageBudget: policy.StageBudget{Implement: 0.1}}
	r := newLimitRun(t, g, tier.Small, 20*dollar)
	r.answer(pricing.Usage{Input: 1_000_000, Output: 1})
	r.stage("implement")
	r.ok("", mainSession)
	r.refused("", mainSession, "implement stage budget $2.00 reached")
	r.stage("implement")
	r.refused("", mainSession, "implement stage budget $2.00 reached")
	r.stage(CheckpointStage)
	r.ok("", mainSession)
	r.refused("", mainSession, "the checkpoint stage admits one call")
}

// TestReviewFirstCountsAsReview: first-line and senior review are one budget
// and one ratio (T7.1). The second-line review of a pass starts from what its
// review_first spent, rather than from a fresh share of the cap, and a call
// review_first made is a review call the ratio counts.
func TestReviewFirstCountsAsReview(t *testing.T) {
	t.Run("the budget", func(t *testing.T) {
		g := policy.Governance{StageBudget: policy.StageBudget{Review: 0.1}}
		r := newLimitRun(t, g, tier.Small, 20*dollar)
		r.answer(pricing.Usage{Input: 1_000_000, Output: 1})
		r.stage("implement")
		r.ok("", mainSession)
		r.end()
		r.stage("review_first")
		r.ok("", mainSession)
		r.end()
		r.stage("review")
		body := r.refused("", mainSession, "review stage budget $2.00 reached")
		if !strings.Contains(body, "$2.00 spent") {
			t.Errorf("the review stage's spend does not include its review_first's: %s", body)
		}
	})
	t.Run("the ratio", func(t *testing.T) {
		// One implement call, a ratio of 1 and a floor of 1 leave review a
		// single call, and review_first's own call is it.
		g := policy.Governance{ReviewRatio: 1, ReviewFloorCalls: 1}
		r := newLimitRun(t, g, tier.Small, 50*dollar)
		r.stage("implement")
		r.ok("", mainSession)
		r.end()
		r.stage("review_first")
		r.ok("", mainSession)
		r.end()
		r.stage("review")
		r.refused("", mainSession, "review calls (1) reached max(1, 1 x 1 implement calls)")
	})
}
