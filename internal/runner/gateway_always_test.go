package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/runner"
)

// allModes is every string a job's FUGARO_BUDGET_MODE can carry into a run:
// the two the vocabulary keeps, unset, and the word that used to be a mode and
// is read as observe until C1's constant goes (AB2). A test that varies the
// mode sweeps this.
var allModes = []string{"", string(policy.ModeOff), policy.ModeObserve, policy.ModeEnforce}

// TestTheGatewayNoLongerReadsTheMode (AB1b, D3009, F15): there is always a
// budget, so whether a run has a gateway is a fact about its credential and
// nothing else. GatewayOn keeps the mode argument its CLI callers still pass
// and ignores it, and a run whose job names no mode at all — the state the
// suite's own harness is in — starts its gateway like any other.
//
// Red step: with an unset mode (and with the word off) GatewayOn answered
// false, so the run had no gateway and agent.BuildEnv's api-key arm mounted the
// job's real key in the agent's environment. Both halves below are about that:
// the predicate answers true for every mode, and the stage's environment
// carries the gateway's per-run token where the key used to be.
//
// Hand mutation this fails on: restoring the mode as a condition
// (`auth == "api-key" && mode != "" && mode != "off"`), which leaves the
// harness's own runs — and every job of an installation whose mode is unset —
// keyless and gateway-less again.
func TestTheGatewayNoLongerReadsTheMode(t *testing.T) {
	for _, auth := range []string{"api-key", "vertex", config.AuthCascade} {
		for _, mode := range allModes {
			if !runner.GatewayOn(auth, mode) {
				t.Errorf("GatewayOn(%q, %q) = false: the mode is not a fact of the gateway any more (D3009)", auth, mode)
			}
		}
	}
	// The credential's own arm is untouched: a credential the gateway cannot
	// carry has no gateway in any mode, which is what keeps an oauth run the
	// one that reaches Anthropic with a token of its own (D3026, R12).
	for _, mode := range allModes {
		if runner.GatewayOn("oauth", mode) {
			t.Errorf("GatewayOn(%q, %q) = true: oauth is not a credential the gateway carries", "oauth", mode)
		}
	}
	for _, mode := range allModes {
		// An enforcing run without a cap halts at bootstrap (HaltNoCap), which
		// is a different rule than this one: the cap is what keeps that from
		// standing in for the gateway's own question.
		capUSD := ""
		if mode == policy.ModeEnforce {
			capUSD = "5"
		}
		g := newGW(t, gwConfig(t, ""), mode, capUSD, anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 1}))
		probed := false
		inspect := func(inner step) step {
			return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
				probed = true
				base, key := gwEnv(req)
				if base == "" {
					t.Fatalf("mode %q: the stage's calls go nowhere: no gateway was started", mode)
				}
				if key == "" || key == plantedKey {
					t.Fatalf("mode %q: the stage presents %q, want the gateway's per-run token (ANTHROPIC_BASE_URL=%s)", mode, key, base)
				}
				return inner(t, ctx, req)
			}
		}
		if _, err := g.run(t, inspect(implement("feature")), review("ship", 0)); err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if !probed {
			t.Fatalf("mode %q: no stage ran", mode)
		}
	}
}

// TestTheVertexRefusalNamesObserveOnly (AB1b, F11): the mode off is gone from
// the vocabulary (D3009), so the remedy a Vertex run is given names the one
// mode such a run can hold. The text is pinned here as well as in the CLI,
// whose validate and init print it: a reader who sees it is told the same
// thing wherever they see it.
func TestTheVertexRefusalNamesObserveOnly(t *testing.T) {
	if !strings.Contains(runner.VertexBudgetRefusal, "budget.mode observe") {
		t.Errorf("VertexBudgetRefusal does not name observe: %q", runner.VertexBudgetRefusal)
	}
	// The old word of the mode is not a remedy any more: a reader sent to it
	// would be sent to a value the configuration refuses.
	if strings.Contains(runner.VertexBudgetRefusal, "off") {
		t.Errorf("VertexBudgetRefusal still names the removed mode: %q", runner.VertexBudgetRefusal)
	}
}
