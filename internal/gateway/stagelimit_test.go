package gateway

// The gateway's own half of the stage-limit hook (plan T7.2): what it does with
// the answer a limit gives it. The limits themselves — the stage budget, the
// token ceiling, the review ratio, the fan-out stop — are governance's and live
// in ee/governance, tested there against this same gateway; what is pinned here
// is the gateway's handling, in the one mode that refuses nothing and after the
// run's halt has already been delivered once.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// stageStopLimit is a stage limit that stops one named stage and leaves every
// other stage alone, with a sentence of its own: a harness for the tests below,
// so what they read is the gateway's handling of a stop and not a rule's
// arithmetic.
type stageStopLimit struct{ stage, reason, detail string }

func (l stageStopLimit) Begin(StageLimitView) {}

func (l stageStopLimit) Stop(v StageLimitView) StageStop {
	if v.Stage.Name != l.stage {
		return StageStop{}
	}
	return StageStop{Reason: l.reason, Detail: l.detail}
}

// TestObserveNeverRefusesAStageStop: observe is the mode that never refuses, and
// a stage limit is no exception to it. A stop the limit returns under observe is
// counted in the stage's report and logged — the same answer the cap's own
// would-halt gets — and the call goes through, with no halt delivered and the
// stage left running, so the call after it is admitted too. The enforce case
// beside it is the control: the same limit refuses the call and delivers the
// stop, so what the two read apart is the mode and not the limit.
func TestObserveNeverRefusesAStageStop(t *testing.T) {
	u := pricing.Usage{Input: 10, Output: 10}
	stop := stageStopLimit{stage: defaultStage.Name, reason: "stage_budget", detail: "the implement stage spent its share"}

	t.Run("observe", func(t *testing.T) {
		h := newHarnessWith(t, func(o *Options) { o.StageLimit = stop },
			anthropicfake.MessageOK(sonnet, u), anthropicfake.MessageOK(sonnet, u))
		for range 2 {
			if resp, b := h.post(msg(sonnet, 100)); resp.StatusCode != 200 {
				t.Fatalf("observe refused a call the limit stopped: %d %s", resp.StatusCode, b)
			}
		}
		select {
		case halt := <-h.gw.Halted():
			t.Errorf("observe published a halt: %+v", halt)
		default:
		}
		if r := h.gw.EndStage(); r.WouldHalt != 2 || r.Calls != 2 {
			t.Errorf("report %+v, want the 2 stopped calls served and counted as 2 would-halts", r)
		}
		n := 0
		for _, l := range h.logs.lines(t) {
			if l["msg"] == "budget: stage would stop (observe)" {
				n++
			}
		}
		if n != 2 {
			t.Errorf("%d stage would-stop log lines, want 2", n)
		}
	})

	t.Run("enforce", func(t *testing.T) {
		// A cap with room in it: the stop under test is the limit's, and the
		// control is what the same limit does where enforcing is the rule.
		h := newHarnessWith(t, func(o *Options) {
			o.Mode, o.Cap, o.StageLimit = Enforce, pricing.Micros(1_000_000), stop
		}, anthropicfake.MessageOK(sonnet, u))
		resp, b := h.post(msg(sonnet, 100))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("enforce answered %d, want 403: %s", resp.StatusCode, b)
		}
		if _, m := apiError(t, b); !strings.Contains(m, stop.detail) {
			t.Errorf("the refusal is %q, want the limit's own sentence in it", m)
		}
		select {
		case halt := <-h.gw.Halted():
			if halt.Reason != stop.reason || halt.Detail != stop.detail {
				t.Errorf("halt %+v, want the stage's stop", halt)
			}
		case <-time.After(time.Second):
			t.Fatal("enforce published no halt")
		}
	})
}

// TestHaltAfterAStageStopIsNotDelivered: Halted() carries one halt for the life
// of the server and is then closed, and the halt the runner hears is the first —
// a stage's stop. A run-wide halt after it is still a halt: it is what later
// calls are refused with. What it must not do is send on the closed channel,
// which is what deliverLocked's guard is for.
func TestHaltAfterAStageStopIsNotDelivered(t *testing.T) {
	stop := stageStopLimit{stage: defaultStage.Name, reason: "stage_budget", detail: "the implement stage spent its share"}
	// A cap no call fits under, so the call the next stage makes is the one the
	// cap refuses: the stage stop comes first, and the cap's halt second.
	h := newHarnessWith(t, func(o *Options) {
		o.Mode, o.Cap, o.StageLimit = Enforce, pricing.Micros(1), stop
	}, okReplies(1)...)
	body := msg(sonnet, 100)
	resp, b := h.post(body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the stopped stage's call: %d %s, want the limit's 403", resp.StatusCode, b)
	}
	ch := h.gw.Halted()
	first, ok := <-ch
	if !ok {
		t.Fatal("no halt delivered")
	}
	if first.Reason != stop.reason {
		t.Errorf("the halt the runner hears is %+v, want the stage's stop", first)
	}
	// The stage after it: the limit leaves it alone (a stop is the stopped
	// stage's own) and the cap has no room for the call, so the run halts a
	// second time, on an already-closed channel.
	h.gw.BeginStage(Stage{Name: "checkpoint", Model: sonnet})
	resp, b = h.post(body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the call after the stop: %d %s, want the cap's 403", resp.StatusCode, b)
	}
	if _, m := apiError(t, b); !strings.Contains(m, "run cap") {
		t.Errorf("the second refusal is %q, want the cap's own sentence: the later halt is recorded even though it is not delivered", m)
	}
	if halt, ok := <-ch; ok {
		t.Errorf("a second halt was sent on the closed channel: %+v", halt)
	}
}
