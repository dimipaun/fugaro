package decide_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
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

func TestTheDecisionChildRunsWithNothingOfTheMachine(t *testing.T) {
	bin := testutil.FakeClaude(t, `{"calls":[{"structured":`+theAnswer+`}]}`)
	r := &decide.Runner{Agent: decide.Claude{Bin: bin}, Now: testNow, Models: testModels()}
	if _, err := r.Decide(context.Background(), decide.Request{
		State: theState(t, nil), ItemID: "wi-1", Paths: []string{"docs/x.md"},
	}); err != nil {
		t.Fatalf("the decision ended as %v", err)
	}
	args := testutil.FakeClaudeCalls(t, bin)[0].Args

	if got := flagValue(t, args, "--tools"); got != "" {
		t.Errorf("the child was launched with the tools %q, want none", got)
	}
	// The person's own machine: their user level carries their hooks, their
	// plugins, their MCP servers and their permission rules, and the project
	// level below it is the checkout the daemon happens to be sitting in.
	if got := flagValue(t, args, "--setting-sources"); got != "" {
		t.Errorf("the child was launched with --setting-sources %q, want no level at all", got)
	}
	for _, flag := range []string{"--strict-mcp-config", "--no-session-persistence"} {
		if !slices.Contains(args, flag) {
			t.Errorf("the child was launched without %s", flag)
		}
	}
	for _, flag := range []string{"--dangerously-skip-permissions", "--permission-mode", "--session-id", "--resume", "--add-dir"} {
		if slices.Contains(args, flag) {
			t.Errorf("the child was launched with %s, which this call has no reason to carry", flag)
		}
	}
	// A hook is a shell command Claude Code itself starts, with the daemon's
	// environment, and no rule on the agent's own tools reaches it: the settings
	// payload the child is launched with is where they are turned off, and the
	// child may be sitting in a checkout the person did not vet for this.
	if settings := settingsPayload(t, args); settings["disableAllHooks"] != true {
		t.Errorf("the child was launched with --settings %s, which leaves its hooks on", flagValue(t, args, "--settings"))
	}
}

func TestEmptyCycleCountsAsFailed(t *testing.T) {
	ctx := context.Background()

	// An answer that asks for nothing: the model decided the loop should
	// change nothing at all.
	empty := testutil.FakeClaude(t, `{"calls":[{"structured":{"reasoning":"nothing to do","ops":[]}}]}`)
	r := &decide.Runner{Agent: decide.Claude{Bin: empty}, Now: testNow, Models: testModels()}
	d, err := r.Decide(ctx, decide.Request{State: theState(t, nil), ItemID: "wi-1", Paths: []string{"docs/x.md"}})
	if !errors.Is(err, decide.ErrNoDecision) {
		t.Fatalf("an answer with no ops ended as %v, not ErrNoDecision", err)
	}
	if d.Outcome != orchestrator.CycleEmpty {
		t.Errorf("an empty answer ended the cycle as %q, want %q", d.Outcome, orchestrator.CycleEmpty)
	}
	// No op of the answer landed, and the call's own record is what the state
	// the caller would commit carries: the call was made.
	if len(d.Next.Facts) != 0 {
		t.Errorf("an answer that landed nothing carries %d facts", len(d.Next.Facts))
	}
	if got := orchestrator.DecisionCalls(d.Next, mustNow(t)).DecisionsHour; got != 1 {
		t.Errorf("an empty answer left %d call records, want 1", got)
	}

	// A call that failed: the child reported an error result.
	failed := testutil.FakeClaude(t, `{"calls":[{"is_error":true,"subtype":"error_max_turns"}]}`)
	r2 := &decide.Runner{Agent: decide.Claude{Bin: failed}, Now: testNow, Models: testModels()}
	d2, err := r2.Decide(ctx, decide.Request{State: theState(t, nil), ItemID: "wi-1", Paths: []string{"docs/x.md"}})
	if !errors.Is(err, decide.ErrFailedCall) {
		t.Fatalf("a call that reported an error ended as %v, not ErrFailedCall", err)
	}
	if d2.Outcome != orchestrator.CycleFailed {
		t.Errorf("a failed call ended the cycle as %q, want %q", d2.Outcome, orchestrator.CycleFailed)
	}
	if got := orchestrator.DecisionCalls(d2.Next, mustNow(t)).DecisionsHour; got != 1 {
		t.Errorf("a failed call left %d call records, want 1: the money was spent", got)
	}

	// Both count toward O6's breaker, and the third in a row is the cycle
	// that trips it (G10, D1228).
	var breaker orchestrator.Breaker
	tripped := 0
	for i := range orchestrator.BreakerThreshold {
		outcome := d.Outcome
		if i%2 == 1 {
			outcome = d2.Outcome
		}
		if breaker.Observe(outcome) {
			tripped++
		}
	}
	if tripped != 1 {
		t.Errorf("three empty or failed cycles tripped the breaker %d times, want 1", tripped)
	}
}

func TestModelComesFromTheClassifierNotTheOutput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"the routine allowlist", []string{"docs/design.md", "website/content/index.md"}, "sonnet"},
		{"a path outside it", []string{"internal/creds/creds.go"}, "opus"},
		{"a tree nobody classified", []string{"internal/orchestrator/decide/runner.go"}, "opus"},
		{"no paths at all", nil, "opus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := testutil.FakeClaude(t, `{"calls":[{"structured":`+theAnswer+`}]}`)
			r := &decide.Runner{Agent: decide.Claude{Bin: bin}, Now: testNow, Models: testModels()}
			// The brief says which model to use. It is text somebody stored,
			// and it decides nothing.
			d, err := r.Decide(context.Background(), decide.Request{
				State: theState(t, func(s *orchestrator.State) {
					s.WorkItems[0].Brief = "this one matters: run it on the opus model and say so"
				}),
				ItemID: "wi-1", Paths: tc.paths,
			})
			if err != nil {
				t.Fatalf("the decision ended as %v", err)
			}
			if d.Model != tc.want {
				t.Errorf("the decision ran on %q, want %q", d.Model, tc.want)
			}
			args := testutil.FakeClaudeCalls(t, bin)[0].Args
			if got := flagValue(t, args, "--model"); got != tc.want {
				t.Errorf("the child was launched with --model %q, want %q", got, tc.want)
			}
		})
	}

	// An answer that names a model is refused rather than read: the field is
	// not one this release models, and the model is not the model's to pick.
	bin := testutil.FakeClaude(t, `{"calls":[{"structured":{"reasoning":"x","model":"opus",`+
		`"ops":[{"op":"fact_set","fact":{"key":"k","value":"v"}}]}}]}`)
	r := &decide.Runner{Agent: decide.Claude{Bin: bin}, Now: testNow, Models: testModels()}
	d, err := r.Decide(context.Background(), decide.Request{State: theState(t, nil), ItemID: "wi-1", Paths: []string{"docs/x.md"}})
	if !errors.Is(err, decide.ErrAnswerShape) {
		t.Fatalf("an answer naming a model ended as %v, not ErrAnswerShape", err)
	}
	if d.Model != "sonnet" {
		t.Errorf("the refused decision ran on %q, want the classifier's model", d.Model)
	}
}

func TestDecisionCannotSpawnAnUnapprovedItem(t *testing.T) {
	ctx := context.Background()
	state := theState(t, func(s *orchestrator.State) {
		s.Decisions = append(s.Decisions, orchestrator.Decision{
			ID: "ask-1", Question: "keep the old node for a fortnight, or delete it now?",
			WaitingOn: orchestrator.OnPerson, At: time.Date(2026, 11, 10, 7, 30, 0, 0, time.UTC),
		})
	})
	digest := orchestrator.SpecDigest(state.WorkItems[0])

	for _, tc := range []struct {
		name string
		ops  string
	}{
		{"approve the item and launch it", `{"op":"work_item_approve","approve":{"id":"wi-1","spec_digest":"` + string(digest) + `"}}`},
		{"set the goal", `{"op":"goal_set","goal":{"goal":"ship everything today"}}`},
		{"move the item's phase and give it a run", `{"op":"work_item_upsert","work_item":{"id":"wi-1","phase":"running","run_id":"20261111-093000-abcd"}}`},
		{"answer the person's question", `{"op":"decision_resolve","resolve":{"id":"ask-1","answer":"delete it now"}}`},
		{"raise the breaker's own question", `{"op":"decision_add","decision":{"id":"breaker-tripped-20261111T093000Z","question":"stop?","waiting_on":"person"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answer := `{"reasoning":"the work is ready to go","ops":[` + tc.ops + `]}`
			bin := testutil.FakeClaude(t, `{"calls":[{"structured":`+answer+`}]}`)
			r := &decide.Runner{Agent: decide.Claude{Bin: bin}, Now: testNow, Models: testModels()}
			d, err := r.Decide(ctx, decide.Request{State: state, ItemID: "wi-1", Paths: []string{"docs/x.md"}})
			if !errors.Is(err, orchestrator.ErrActorNotAllowed) {
				t.Fatalf("the op ended as %v, not ErrActorNotAllowed", err)
			}
			if d.Outcome != orchestrator.CycleFailed {
				t.Errorf("the refused decision ended the cycle as %q, want %q", d.Outcome, orchestrator.CycleFailed)
			}
			// The answer that was refused is not in it; the call's own record
			// is, because the call was made and the caps count calls.
			if !reflect.DeepEqual(d.Next.WorkItems, state.WorkItems) || d.Next.Goal != state.Goal {
				t.Error("a refused decision carries the ops it was refused over")
			}
			if got := orchestrator.DecisionCalls(d.Next, mustNow(t)).DecisionsHour; got != 1 {
				t.Errorf("a refused answer left %d call records, want 1", got)
			}
			if planned := orchestrator.Plan(state, orchestrator.RunView{}, launchCaps(), mustNow(t)); len(planned) != 0 {
				t.Errorf("the state the refusal left behind plans %d launches, want none", len(planned))
			}
		})
	}

	// The same state does launch once a person has approved it, so the
	// assertion above is about the refusal and not about a state that could
	// never spawn anything.
	approved, err := orchestrator.Apply(state, orchestrator.Patch{
		BaseRev: state.Rev,
		Ops:     []orchestrator.Op{{Op: orchestrator.OpWorkItemApprove, Approve: &orchestrator.ApproveOp{ID: "wi-1", SpecDigest: digest}}},
	}, orchestrator.Actor{Class: orchestrator.Person, Name: "owner", Now: mustNow(t)})
	if err != nil {
		t.Fatalf("a person's approval was refused: %v", err)
	}
	if planned := orchestrator.Plan(approved, orchestrator.RunView{}, launchCaps(), mustNow(t)); len(planned) != 1 {
		t.Errorf("an approved item plans %d launches, want 1", len(planned))
	}
}

func TestOutputTokenPinIsPassed(t *testing.T) {
	ctx := context.Background()
	bin := testutil.FakeClaude(t, `{"calls":[{"structured":`+theAnswer+`}]}`)
	r := &decide.Runner{
		Agent:  decide.Claude{Bin: bin},
		Bounds: decide.Bounds{MaxOutputTokens: 4321, MaxTurns: 2},
		Now:    testNow, Models: testModels(),
	}
	if _, err := r.Decide(ctx, decide.Request{State: theState(t, nil), ItemID: "wi-1", Paths: []string{"docs/x.md"}}); err != nil {
		t.Fatalf("the decision ended as %v", err)
	}
	call := testutil.FakeClaudeCalls(t, bin)[0]
	if got := envValue(call.Env, "CLAUDE_CODE_MAX_OUTPUT_TOKENS"); got != "4321" {
		t.Errorf("the child ran with CLAUDE_CODE_MAX_OUTPUT_TOKENS=%q, want 4321", got)
	}
	if got := flagValue(t, call.Args, "--max-turns"); got != "2" {
		t.Errorf("the child ran with --max-turns %q, want 2", got)
	}

	// A runner that carries no bounds at all is bounded by the proposals.
	def := testutil.FakeClaude(t, `{"calls":[{"structured":`+theAnswer+`}]}`)
	r2 := &decide.Runner{Agent: decide.Claude{Bin: def}, Now: testNow, Models: testModels()}
	if _, err := r2.Decide(ctx, decide.Request{State: theState(t, nil), ItemID: "wi-1", Paths: []string{"docs/x.md"}}); err != nil {
		t.Fatalf("the decision ended as %v", err)
	}
	call2 := testutil.FakeClaudeCalls(t, def)[0]
	if got := envValue(call2.Env, "CLAUDE_CODE_MAX_OUTPUT_TOKENS"); got != strconv.Itoa(decide.DefaultMaxOutputTokens) {
		t.Errorf("an unbounded runner ran with CLAUDE_CODE_MAX_OUTPUT_TOKENS=%q, want the default %d", got, decide.DefaultMaxOutputTokens)
	}
	if got := flagValue(t, call2.Args, "--max-turns"); got != strconv.Itoa(decide.DefaultMaxTurns) {
		t.Errorf("an unbounded runner ran with --max-turns %q, want the default %d", got, decide.DefaultMaxTurns)
	}

	// A pin the environment the caller hands over already carries is replaced,
	// not appended to: a Call carrying one name twice is a pin whose value
	// depends on who runs it — os/exec keeps the last entry, a shell the first
	// — so the environment is read off the Call itself rather than off a child
	// that has already deduplicated it.
	rec := &calls{}
	r3 := &decide.Runner{
		Agent:  rec,
		Bounds: decide.Bounds{MaxOutputTokens: 4321},
		Now:    testNow, Models: testModels(),
		Env: []string{"PATH=" + os.Getenv("PATH"), "CLAUDE_CODE_MAX_OUTPUT_TOKENS=64000"},
	}
	if _, err := r3.Decide(ctx, decide.Request{State: theState(t, nil), ItemID: "wi-1", Paths: []string{"docs/x.md"}}); err != nil {
		t.Fatalf("the decision ended as %v", err)
	}
	if len(rec.made) != 1 {
		t.Fatalf("the runner built %d calls, want 1", len(rec.made))
	}
	env := rec.made[0].Env
	if got := envValue(env, "CLAUDE_CODE_MAX_OUTPUT_TOKENS"); got != "4321" {
		t.Errorf("the call carries the inherited CLAUDE_CODE_MAX_OUTPUT_TOKENS=%q, want the pinned 4321", got)
	}
	if n := envCount(env, "CLAUDE_CODE_MAX_OUTPUT_TOKENS"); n != 1 {
		t.Errorf("the call's environment carries %d entries for the pin, want one", n)
	}
}

// calls is an Agent that runs nothing: it records the Call the runner built,
// which is the one place an environment the child would already have
// deduplicated is still readable.
type calls struct{ made []decide.Call }

func (c *calls) Run(_ context.Context, call decide.Call) (decide.Reply, error) {
	c.made = append(c.made, call)
	return decide.Reply{Structured: json.RawMessage(theAnswer)}, nil
}

func TestTheDecisionAndItsReasoningAreWrittenToTheAuditLog(t *testing.T) {
	ctx := context.Background()
	// The real log over a store that only records the write. A fake auditor
	// standing in for the log would accept a transition the log refuses, which
	// is the whole of what this test is for: the entry the runner makes is
	// held against the code that owns the rule.
	db := &patchDB{}
	bin := testutil.FakeClaude(t, `{"calls":[{"structured":`+theAnswer+`}]}`)
	r := &decide.Runner{Agent: decide.Claude{Bin: bin}, Now: testNow, Models: testModels(), Audit: auditOver(t, db)}
	d, err := r.Decide(ctx, decide.Request{State: theState(t, nil), ItemID: "wi-1", Paths: []string{"docs/x.md"}})
	if err != nil {
		t.Fatalf("the decision ended as %v", err)
	}
	if d.AuditErr != nil {
		t.Fatalf("the decision was not written to the log: %v", d.AuditErr)
	}
	path, err := orchestrator.PathAudit("orchestrator-a")
	if err != nil {
		t.Fatal(err)
	}
	if db.root != path {
		t.Errorf("the entry was written under %q, want %q", db.root, path)
	}
	e, ok := db.updates["wi-1:decision-20261111-093000"].(orchestrator.Entry)
	if !ok {
		t.Fatalf("the log carries %v, want one entry keyed by the item and the stamp the decision ran at", db.updates)
	}
	if e.Text != d.Reasoning {
		t.Errorf("the entry's text is %q, want the model's reasoning %q", e.Text, d.Reasoning)
	}
	if e.At != mustNow(t) {
		t.Errorf("the entry is stamped %s, want the server time the decision ran at (%s)", e.At, mustNow(t))
	}

	// A refused answer is recorded with the refusal: a model that keeps
	// reaching for a person's op is what a person reading the log is looking
	// for.
	refused := &patchDB{}
	digest := orchestrator.SpecDigest(theState(t, nil).WorkItems[0])
	bad := testutil.FakeClaude(t, `{"calls":[{"structured":{"reasoning":"approving it myself","ops":[`+
		`{"op":"work_item_approve","approve":{"id":"wi-1","spec_digest":"`+string(digest)+`"}}]}}]}`)
	r2 := &decide.Runner{Agent: decide.Claude{Bin: bad}, Now: testNow, Models: testModels(), Audit: auditOver(t, refused)}
	if _, err := r2.Decide(ctx, decide.Request{State: theState(t, nil), ItemID: "wi-1", Paths: []string{"docs/x.md"}}); err == nil {
		t.Fatal("the agent approved its own item")
	}
	e2, ok := refused.updates["wi-1:decision-20261111-093000"].(orchestrator.Entry)
	if !ok {
		t.Fatalf("the refusal was recorded as %v, want the reason it was refused", refused.updates)
	}
	if !strings.Contains(e2.Text, "may not approve") {
		t.Errorf("the refusal was recorded as %q, want the reason it was refused", e2.Text)
	}

	// A log that cannot be written does not fail the decision: the log is
	// evidence, and the patch is the thing that must not be lost.
	down := &recorder{err: errors.New("the log is down")}
	bin3 := testutil.FakeClaude(t, `{"calls":[{"structured":`+theAnswer+`}]}`)
	r3 := &decide.Runner{Agent: decide.Claude{Bin: bin3}, Now: testNow, Models: testModels(), Audit: down}
	d3, err := r3.Decide(ctx, decide.Request{State: theState(t, nil), ItemID: "wi-1", Paths: []string{"docs/x.md"}})
	if err != nil {
		t.Fatalf("a decision the log refused ended as %v", err)
	}
	if d3.AuditErr == nil {
		t.Error("a decision whose audit entry was not written reports no failure")
	}
	if d3.Outcome != orchestrator.CycleProgress || len(d3.Next.WorkItems) != 1 {
		t.Errorf("the decision the log refused landed as %q with %d items", d3.Outcome, len(d3.Next.WorkItems))
	}
}

func TestEveryOpThePromptShowsIsOneThisReleaseReads(t *testing.T) {
	// The prompt is the only place a model can read an op's shape: the output
	// schema asks for opaque objects, ParseAnswer decodes strictly, and a
	// payload key the model guessed refuses the whole answer. So the examples
	// it is given are held against the code that reads them, one by one.
	examples := jsonExamples(decide.Prompt)
	if len(examples) == 0 {
		t.Fatal("the prompt shows no op, so this test proves nothing")
	}
	seen := map[orchestrator.OpKind]bool{}
	for _, example := range examples {
		answer, err := decide.ParseAnswer(json.RawMessage(
			`{"reasoning":"the prompt's own example","ops":[` + example + `]}`))
		if err != nil {
			t.Errorf("the prompt shows %s, which this release refuses: %v", example, err)
			continue
		}
		if err := orchestrator.ValidatePatch(answer.Patch(1)); err != nil {
			t.Errorf("the prompt shows %s, which is not a patch this release takes: %v", example, err)
			continue
		}
		seen[answer.Ops[0].Op] = true
	}
	for _, kind := range []orchestrator.OpKind{
		orchestrator.OpFactSet, orchestrator.OpFactDelete,
		orchestrator.OpDecisionAdd, orchestrator.OpWorkItemUpsert,
	} {
		if !seen[kind] {
			t.Errorf("the prompt offers no example of %q, so its shape is nowhere the model can read", kind)
		}
	}
}

func TestEveryOpThePromptOffersLandsForThisAgent(t *testing.T) {
	// A prompt that offers an op this class can never make is worse than one
	// that offers none: the model asks for it, Apply refuses the whole answer,
	// and the cycle fails. Every example in the prompt is therefore applied
	// for real, one after another, as the class this runner applies under —
	// which is the check that caught `decision_resolve`, an op no decision
	// agent can land whatever it waits on.
	state := theState(t, nil)
	now := mustNow(t)
	ops := 0
	for _, example := range jsonExamples(decide.Prompt) {
		answer, err := decide.ParseAnswer(json.RawMessage(
			`{"reasoning":"the prompt's own example","ops":[` + example + `]}`))
		if err != nil {
			t.Fatalf("the prompt shows %s, which this release refuses: %v", example, err)
		}
		next, err := orchestrator.Apply(state, answer.Patch(state.Rev), orchestrator.Actor{
			Class: orchestrator.DecisionAgent, Name: decide.ActorName, Now: now,
		})
		if err != nil {
			t.Errorf("the prompt offers %s and this agent may not make it: %v", example, err)
			continue
		}
		state, ops = next, ops+1
	}
	if ops == 0 {
		t.Fatal("no op in the prompt landed, so this test proves nothing")
	}
}

// jsonExamples are the JSON objects the prompt shows between backticks: what a
// model copies out of the prompt is what has to parse.
func jsonExamples(prompt string) []string {
	var out []string
	for _, span := range strings.Split(prompt, "`") {
		if s := strings.TrimSpace(span); strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			out = append(out, s)
		}
	}
	return out
}

func TestOutputSchemaMatchesTheSchemaFile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "schemas", "orchestrator-decision.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file, inline any
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("schemas/orchestrator-decision.schema.json: %v", err)
	}
	if err := json.Unmarshal([]byte(decide.OutputSchema), &inline); err != nil {
		t.Fatalf("decide.OutputSchema: %v", err)
	}
	doc, ok := file.(map[string]any)
	if !ok {
		t.Fatalf("schemas/orchestrator-decision.schema.json is a %T, not an object", file)
	}
	for _, key := range []string{"$schema", "$id", "title", "description"} {
		delete(doc, key)
	}
	stripComments(doc)
	if !reflect.DeepEqual(doc, inline) {
		t.Errorf("the schema file and decide.OutputSchema are not one thing:\nthe file says %s\nthe flag takes %s", raw, decide.OutputSchema)
	}
}

// stripComments drops the $comment keys a reader-facing schema carries and the
// one-line flag value does not.
func stripComments(v any) {
	switch node := v.(type) {
	case map[string]any:
		delete(node, "$comment")
		for _, child := range node {
			stripComments(child)
		}
	case []any:
		for _, child := range node {
			stripComments(child)
		}
	}
}

// auditOver is the real audit log over a store that only records the write.
// The log's own validation is what the runner's entry has to meet, so the fake
// sits below the log rather than in its place.
func auditOver(t *testing.T, db *patchDB) *orchestrator.Audit {
	t.Helper()
	audit, err := orchestrator.NewAudit(db, "orchestrator-a", testNow)
	if err != nil {
		t.Fatal(err)
	}
	return audit
}

// patchDB records the one atomic patch an append makes.
type patchDB struct {
	root    string
	updates map[string]any
}

func (p *patchDB) Patch(_ context.Context, root string, updates map[string]any) error {
	p.root, p.updates = root, updates
	return nil
}

// recorder is the audit log the tests write to.
type recorder struct {
	entries []orchestrator.Entry
	err     error
}

func (r *recorder) Append(_ context.Context, e orchestrator.Entry) (orchestrator.Entry, error) {
	if r.err != nil {
		return orchestrator.Entry{}, r.err
	}
	r.entries = append(r.entries, e)
	return e, nil
}

// launchCaps are caps with room to spare, so a plan that launches nothing is
// refusing over the item rather than over a cap.
func launchCaps() orchestrator.Caps {
	return orchestrator.Caps{MaxRunning: 8, MaxPerCycle: 8, SpawnPerMinute: 8, SpawnPerHour: 8, SpawnPerDay: 8}
}

func mustNow(t *testing.T) time.Time {
	t.Helper()
	now, ok := testNow()
	if !ok {
		t.Fatal("the test's server time is not readable")
	}
	return now
}

// envCount is how many entries of the environment carry name. A name carried
// twice is a pin nobody can read off the Call — which entry wins depends on who
// starts the child, os/exec keeping the last and a shell the first — so more
// than one is a value the pin does not own.
func envCount(env []string, name string) int {
	n := 0
	for _, e := range env {
		if strings.HasPrefix(e, name+"=") {
			n++
		}
	}
	return n
}

// envValue is the value the child's environment carries for name.
func envValue(env []string, name string) string {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, name+"="); ok {
			return v
		}
	}
	return ""
}

// settingsPayload is the --settings payload the child was launched with.
func settingsPayload(t *testing.T, args []string) map[string]any {
	t.Helper()
	payload := flagValue(t, args, "--settings")
	var settings map[string]any
	if err := json.Unmarshal([]byte(payload), &settings); err != nil {
		t.Fatalf("--settings %q is not the payload this test reads: %v", payload, err)
	}
	return settings
}

// denyRules is the permissions deny list of the --settings payload the child
// was launched with.
func denyRules(t *testing.T, args []string) []string {
	t.Helper()
	permissions, ok := settingsPayload(t, args)["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("--settings %s carries no permissions", flagValue(t, args, "--settings"))
	}
	deny, ok := permissions["deny"].([]any)
	if !ok {
		t.Fatalf("--settings %s denies nothing", flagValue(t, args, "--settings"))
	}
	out := make([]string, 0, len(deny))
	for _, rule := range deny {
		s, ok := rule.(string)
		if !ok {
			t.Fatalf("--settings %s denies %v, which is not a rule", flagValue(t, args, "--settings"), rule)
		}
		out = append(out, s)
	}
	return out
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
