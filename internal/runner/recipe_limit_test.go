package runner_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The run-digest recipe bounds its review's output per call (0.24.1): without
// the bound the gateway reserves the reviewer model's own maximum output,
// which on claude-opus-5-5 is $2.56 plus the input, and a run whose per-run cap
// is smaller than that halts with run_cap having spent nothing.

// TestDigestReviewStagePinnedToTheOutputBound: a report run's one model call is
// its review, and the pin that reaches Claude Code — and so the max_tokens of
// the call it makes, which is what the gateway reserves — carries the recipe's
// bound rather than the model's own maximum. A repository that reserves less
// for its reviewer keeps its own figure and one that reserves more is capped:
// the recipe only ever tightens.
//
// The call at the bound is made through the gateway, so the bound is not below
// what a review of this run sends. That the gateway refuses one token above the
// stage's limit is its own rule, pinned in internal/gateway.
func TestDigestReviewStagePinnedToTheOutputBound(t *testing.T) {
	const own = "20261020-101500-aaaa"
	for _, tc := range []struct {
		name    string
		written int64
		want    int64
	}{
		{"no bound of its own", 0, 32000},
		{"a smaller one", 16000, 16000},
		{"a larger one", 64000, 32000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pin := strconv.FormatInt(tc.want, 10)
			g := newGW(t, reviewerBound(t, tc.written), "observe", "", anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 10}))
			g.setSpec(t, digestSpec(own))
			clk := digestClock(g.harness)
			digestRecord(t, g.bucket, repoSlug, own, clk.Now().AddDate(0, 0, -1), 1.5, nil)

			var st []int
			rec, err := g.run(t, calling(&st, review("ship", 0), call{sonnet, tc.want}))
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != runstore.StatusSucceeded || rec.Outcome != runstore.OutcomeReady {
				t.Fatalf("record = status %s, outcome %s, reason %q", rec.Status, rec.Outcome, rec.Reason)
			}
			if len(st) != 1 || st[0] != 200 {
				t.Fatalf("statuses = %v, want the review's call at the bound admitted", st)
			}
			if len(g.agent.calls) != 1 {
				t.Fatalf("the run made %d model calls, want its one review", len(g.agent.calls))
			}
			if got := envValue(g.agent.calls[0].Env, "CLAUDE_CODE_MAX_OUTPUT_TOKENS"); got != pin {
				t.Errorf("the review's output pin = %q, want %s", got, pin)
			}
		})
	}
}

// reviewerBound is gwConfig with the repository's own reviewer output bound
// written as written, or with the whole max_output_tokens line removed when it
// is 0: the shape of the repository whose report run halted with run_cap, which
// set no per-call output bound of its own.
func reviewerBound(t *testing.T, written int64) string {
	t.Helper()
	const line = "  max_output_tokens: { coder: 4096, reviewer: 4096 }\n"
	cfg := gwConfig(t, "")
	from, to := line, ""
	if written > 0 {
		from, to = "reviewer: 4096", "reviewer: "+strconv.FormatInt(written, 10)
	}
	if out := strings.Replace(cfg, from, to, 1); out != cfg {
		return out
	}
	t.Fatal("the fixture has no reviewer output bound to write")
	return ""
}
