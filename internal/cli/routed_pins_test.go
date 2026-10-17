package cli

import (
	"strings"
	"testing"
)

// routedPinsProject is a project config whose switch routes claude-sonnet-5-5
// through one entry, priced only under the canonical ID: the route's own ID
// (anthropic/claude-sonnet-5.5) has no row, so every pin the switch rewrites is
// a model the run cannot price.
const routedPinsProject = `budget: { mode: enforce, per_run_usd: 5 }
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["anthropic/claude-sonnet-5.5"]
    allow_data_to: ["acme/app"]
model_prices:
  "claude-sonnet-5-5": { input_per_m: 3, output_per_m: 15 }
`

// validateOnlyAgentModel runs validate over a file that names one Claude model
// and nothing else: agent.model answers for the coder and the reviewer both
// (config.Agent.ModelFor), and no background is written, so a run derives the
// background from the coder.
func validateOnlyAgentModel(t *testing.T, via string) (string, string, error) {
	t.Helper()
	isolateProjects(t, t.TempDir())
	projectRouting(t, routedPinsProject+"routing:\n  claude:\n    via: "+via+"\n    models: [claude-sonnet-5-5]\n")
	inCheckoutOf(t, "acme/app")
	const agent = "agent:\n  auth: api-key\n  model: claude-sonnet-5-5\nworkflows:"
	return execute(t, "validate", writeConfig(t, strings.Replace(cliMinimalYAML, "workflows:", agent, 1)))
}

// TestValidatePrintsARoutedPinOnceWhenOnlyAgentModelIsSet is the review finding
// on routedAgent reporting the slots it *assigned* rather than the pins whose
// effective model it moved. With only agent.model written, the coder's rewrite
// writes the route's ID there — and agent.model is the reviewer's model too, so
// the reviewer's effective pin moved with it. routedAgent named the coder
// alone, budgetProblems' skip of the rewritten pins therefore did not cover
// `agent.models.reviewer`, and the run's own routing block and its budget block
// each printed the same missing-price line for it: `agent.models.reviewer`
// twice, once bare and once backticked.
//
// Mutation that makes this fail: return the assigned slots (append the path in
// each slot) instead of comparing the effective pins before and after.
func TestValidatePrintsARoutedPinOnceWhenOnlyAgentModelIsSet(t *testing.T) {
	out, errOut, err := validateOnlyAgentModel(t, "openrouter")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, want %d: every pin names the route's ID, which has no price\n%s%s", ExitCode(err), ExitUserError, out, errOut)
	}
	// The pins the switch moved are the routed run's own block (routingProblems,
	// whose line is config.CheckProviderPolicy's, with the ID backticked): the
	// reviewer's is one line there, not one there and one more from
	// budgetProblems' table.
	seen := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, "is valid") {
			continue
		}
		path, _, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("a problem line without a path: %q", line)
		}
		seen[path]++
		if !strings.Contains(line, "`anthropic/claude-sonnet-5.5` has no price") {
			t.Errorf("%s is not printed in the routed run's own words: %q", path, line)
		}
	}
	for _, path := range []string{"agent.models.coder", "agent.models.reviewer"} {
		if seen[path] != 1 {
			t.Errorf("%s is printed %d times, want once:\n%s", path, seen[path], out)
		}
	}
	// The file writes no background: a run derives one (the routed coder's own
	// model), and it is the route's ID the coder already names. A second line
	// for it is the same refusal for one model.
	if seen["agent.models.background"] > 1 {
		t.Errorf("the derived background repeats the coder's refusal %d times:\n%s", seen["agent.models.background"], out)
	}
}

// TestValidateSplitsAViaAutoSwitchByWhatEachRunReaches is the same finding on
// the reach that depends on the job's stored Anthropic API key. With only
// agent.model written and no background, the two runs of a via: auto switch
// halt on different things, and each pin belongs to one of them:
//
//   - the keyed run keeps the file's pins, so its background is the empty one
//     the file has and the run refuses it ("is required with a budget");
//   - the keyless run takes the route, so all three pins are the route's ID,
//     and it halts first on the coder's price — the background's price is never
//     reached.
//
// Every pin the switch moved belongs to the routed run's own block
// (routingProblems), so the keyed run's background requirement is the only
// finding left for the direct run, and neither is a problem: no run of this
// configuration makes the other's, which is splitByReach's rule for via: auto.
// Reporting the pins by the slots assigned put the reviewer and the derived
// background in both blocks at once and printed the keyless run's missing-price
// sentence for a pin (the background) that run never reaches.
//
// Mutation that makes this fail: the assigned slots in routedAgent, and —
// the other half of the same line — a rewrittenPaths that compares the written
// background (a.Models.Background) rather than the background a run derives for
// those pins.
func TestValidateSplitsAViaAutoSwitchByWhatEachRunReaches(t *testing.T) {
	out, errOut, err := validateOnlyAgentModel(t, "auto")
	if err != nil {
		t.Fatalf("exit %d: the keyed run's background and the keyless run's coder price are each one run's, so neither is this configuration's\n%s%s", ExitCode(err), out, errOut)
	}
	for _, want := range []string{
		// The keyed run, whose pins stay direct.
		"note: agent.models.background: is required with a budget",
		// The keyless run, which takes the route: the coder is the pin it
		// halts on.
		"note: agent.models.coder: `anthropic/claude-sonnet-5.5` has no price",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("a note is missing: %q\n%s", want, errOut)
		}
	}
	// And nothing of those two runs is claimed as every run's: the reviewer's
	// line belongs to the keyless run too, and the keyed run never reaches it.
	if strings.Contains(out, "has no price") && !strings.Contains(errOut, "note:") {
		t.Errorf("the keyless run's price refusal is printed as a problem:\n%s", out)
	}
}
