package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestFinalizeClassFollowsTheReasonItWrites: design §4.2's three-way choice,
// read off the record rather than off the code — the class a run carries is
// the one belonging to the sentence finalize wrote, never the one Decide read
// for a sentence the record no longer holds.
//
// Every arm is a run whose Decide refused in its own way while the runner's
// state said something else, so the class can only be right if the override
// that rewrote the reason moved the class with it: a run that made no commits
// (`no_commits` over the decision's `unverified_final_commit`), one that
// halted mid-implement with nothing committed (the halt's family over both),
// and — the arm that shows the other two are not simply what always wins — a
// run whose only refusal is the decision's own (`review_not_converged`). The
// arm is walked too, in the one shape that tells its own finalize's choice
// apart from the decision's.
//
// The class is durable (the report groups runs by it and nothing rewrites it),
// so a run filed under a refusal it no longer holds is a figure its owner acts
// on with the wrong remedy: `unverified_final_commit`'s is "run it again",
// while a halt's is the cap.
func TestFinalizeClassFollowsTheReasonItWrites(t *testing.T) {
	noCommits := func(t *testing.T) *runstore.Record {
		h := newHarness(t, "", nil)
		nothing := func(*testing.T, context.Context, agent.Request) (agent.Result, error) { return agent.Result{}, nil }
		rec, err := h.run(t, nothing, review("ship", 0))
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	for _, tc := range []struct {
		name   string
		run    func(t *testing.T) *runstore.Record
		status runstore.Status
		reason string
		class  failure.Class
	}{
		{
			name: "the run's own first failure outranks the decision",
			run:  noCommits,
			// The empty commit finalize records carries no test record, so
			// Decide refuses `unverified_final_commit` here; the reason the
			// record holds is the runner's own.
			status: runstore.StatusFailed,
			reason: "the agent made no commits",
			class:  failure.NoCommits,
		},
		{
			name: "a halt outranks both",
			run: func(t *testing.T) *runstore.Record {
				b := newHandleBox(t)
				h := newHarness(t, "", nil)
				rec, err := h.run(t, haltAfter(b, leaveFile("partial.txt")))
				if err != nil {
					t.Fatal(err)
				}
				return rec
			},
			// The halt leaves the tree with nothing committed and no test
			// record, which is both other ways at once: the record is the
			// halt's, and so is the class.
			status: runstore.StatusHalted,
			reason: "halted: run_cap: ",
			class:  failure.HaltedBudget,
		},
		{
			name: "the decision's own refusal, where nothing else happened",
			run: func(t *testing.T) *runstore.Record {
				h := newHarness(t, "", nil)
				rec, err := h.run(t, implement("feature"), review("changes", 1), idle, review("changes", 1))
				if err != nil {
					t.Fatal(err)
				}
				return rec
			},
			status: runstore.StatusFailed,
			reason: "review round 2 still has 1 finding",
			class:  failure.ReviewNotConverged,
		},
		{
			name: "an arm's own finalize chooses the same way",
			run: func(t *testing.T) *runstore.Record {
				g, _ := armGW(t, armCfg(t), "50", challengerModel, armLayer)
				nothing := func(*testing.T, context.Context, agent.Request) (agent.Result, error) { return agent.Result{}, nil }
				rec, err := g.run(t, nothing, review("ship", 0))
				if err != nil {
					t.Fatal(err)
				}
				return rec
			},
			status: runstore.StatusFailed,
			reason: "the agent made no commits",
			class:  failure.NoCommits,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.run(t)
			if rec.Status != tc.status {
				t.Fatalf("status = %s, want %s (record %+v)", rec.Status, tc.status, rec)
			}
			if tc.status == runstore.StatusHalted {
				if !strings.HasPrefix(rec.Reason, tc.reason) {
					t.Fatalf("reason = %q, want it to start %q", rec.Reason, tc.reason)
				}
			} else if rec.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", rec.Reason, tc.reason)
			}
			if rec.FailureClass != tc.class {
				t.Errorf("failure_class = %q, want %q: the class describes the reason the record carries, and a class left over from what Decide read names a sentence this record no longer holds", rec.FailureClass, tc.class)
			}
		})
	}
}
