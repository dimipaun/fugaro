package decide_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/orchestrator/decide"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// theAnswer is one well-formed decision: a reasoning and a single fact, which
// is an op a decision agent may write.
const theAnswer = `{"reasoning":"the review round is stuck on a flake",` +
	`"ops":[{"op":"fact_set","fact":{"key":"ci-flake","value":"the review round retried twice"}}]}`

// testNow is the server time every test decides at.
func testNow() (time.Time, bool) { return time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC), true }

// testModels is O4's key as the merged policy hands it over: the routine
// class on sonnet, which is not its floor, so a test that reads the map and a
// test that reads a constant cannot both pass.
func testModels() map[policy.DecisionClass]string {
	return map[policy.DecisionClass]string{policy.DecisionRoutine: "sonnet", policy.DecisionSensitive: "opus"}
}

// theState is one valid state carrying one item, with mutate applied.
func theState(t *testing.T, mutate func(*orchestrator.State)) orchestrator.State {
	t.Helper()
	s := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           3,
		AgentID:       "orchestrator-a",
		Role:          orchestrator.RoleOrchestrator,
		Goal:          "keep owner/repo's pull requests moving without a person watching",
		WorkItems: []orchestrator.WorkItem{{
			ID: "wi-1", Title: "land the state migration", Repo: "owner/repo",
			Recipe: "standard", RigorTier: "medium",
			Brief: "Move the sweep onto the daemon.", DependsOn: []string{}, Phase: orchestrator.PhaseQueued,
		}},
		Decisions: []orchestrator.Decision{},
		Facts:     map[string]orchestrator.Fact{},
		Archived:  []orchestrator.Archived{},
		Intents:   []orchestrator.Intent{},
	}
	if mutate != nil {
		mutate(&s)
	}
	if err := orchestrator.ValidateState(s); err != nil {
		t.Fatalf("the fixture state is not one this release reads: %v", err)
	}
	return s
}

func TestDecisionAgentRunsWithTheAgentDenyRules(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"structured":`+theAnswer+`}]}`)
	r := &decide.Runner{Agent: decide.Claude{Bin: bin}, Now: testNow, Models: testModels()}

	if _, err := r.Decide(context.Background(), decide.Request{
		State: theState(t, nil), ItemID: "wi-1", Paths: []string{"docs/design.md"},
	}); err != nil {
		t.Fatalf("the decision ended as %v", err)
	}
	calls := testutil.FakeClaudeCalls(t, bin)
	if len(calls) != 1 {
		t.Fatalf("the runner made %d calls, want 1", len(calls))
	}
	deny := denyRules(t, calls[0].Args)
	want := agent.DenyRules(true, false)
	if len(want) == 0 {
		t.Fatal("the agent package denies nothing, so this test proves nothing")
	}
	for _, rule := range want {
		if !slices.Contains(deny, rule) {
			t.Errorf("the decision agent runs with %q and not with %q", deny, rule)
		}
	}
}

// denyRules is the permissions deny list of the --settings payload the child
// was launched with.
func denyRules(t *testing.T, args []string) []string {
	t.Helper()
	payload := flagValue(t, args, "--settings")
	var settings struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(payload), &settings); err != nil {
		t.Fatalf("--settings %q is not the payload this test reads: %v", payload, err)
	}
	return settings.Permissions.Deny
}

func flagValue(t *testing.T, args []string, name string) string {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	t.Fatalf("%q is not in %q", name, args)
	return ""
}
