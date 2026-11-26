package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// setup_events_test.go pins task ST2 of docs/console-improvements/implementation-plan.md:
// `fugaro setup --events`, the converge's own account -- the stages, the steps
// left for the user, the stage a failure stopped at and the exit code -- as
// newline-delimited lines, so a surface that drives the CLI draws each stage as
// it settles (D2904; the console's first-run wizard spawns
// `setup --plan-only --answers-file - --events --json`).
//
// The feed is an output and never a way in: nothing here confirms a step, and a
// step that needs a confirmation arrives as `left_for_you` exactly as --json
// leaves it (unconfirmedWhy, init.go).

// updateSetupEventsGolden rewrites testdata/setup_events.golden.ndjson. It is a
// flag of its own: the package's other goldens already own -update,
// -update-review, -update-retire and -update-cloudrun, and a second flag.Bool
// under one of those names panics.
var updateSetupEventsGolden = flag.Bool("update-setup-events", false, "rewrite testdata/setup_events.golden.ndjson")

// stableSelfCommand pins the command lines a run prints to "fugaro", which is
// what the golden and the feed's own assertions hold: a test binary's own path
// is the temp directory the go tool built it in, and a golden that carried one
// would change on every machine (the shape pair_test.go's
// pairDBPlaceholder normalises for the same reason).
func stableSelfCommand(t *testing.T) {
	t.Helper()
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
}

// setupEventsRun is the hermetic converge these tests read: the package's own
// rig (internal/gcpfake behind every client, a terraform wrapper that plans
// against a script) with a state bucket, so the installation root has somewhere
// to write and the run reaches every stage, and a terminal, so the run is one a
// person is at.
func setupEventsRun(t *testing.T, args ...string) (*initRig, string, string, error) {
	t.Helper()
	r := newInitRig(t)
	r.stateBucket()
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", args...)
	return r, out, errOut, err
}

// setupEventsSessionRun is the same run in a coding agent's session, which is
// the hermetic way a step is left for the user: such a session applies nothing
// in the cloud, so the installation — the first stage with work — comes back
// needs-you with its own line to run, and the run goes on to the stages after
// it rather than waiting for anybody. The marker is set last, because the rig's
// own helpers clear the ones a session is read from.
func setupEventsSessionRun(t *testing.T, args ...string) (out, errOut string, err error) {
	t.Helper()
	r := newInitRig(t)
	r.stateBucket()
	fakeTerminal(t)
	agentSession(t)
	return executeStdin(t, "", args...)
}

// setupFeedLine is one line of a feed as a test reads it: its `type` — the
// whole of what a consumer may assume of a line — the object itself, so a
// member is read by its wire name, and the line as it was written, which is
// what a schema is handed.
type setupFeedLine struct {
	Type string
	Text string
	Raw  map[string]any
}

// setupFeed reads a feed off a run's stdout. Every line is one JSON object and
// every one carries a `type`: that is what makes the stream a feed rather than
// a document, and a line that is not throws the run away rather than being
// skipped (a consumer reads a line it cannot parse as an interrupted feed).
func setupFeed(t *testing.T, out string) []setupFeedLine {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return nil
	}
	var feed []setupFeedLine
	for i, ln := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		var raw map[string]any
		if err := json.Unmarshal([]byte(ln), &raw); err != nil {
			t.Fatalf("line %d of the feed is not one JSON object (%v): %q", i+1, err, ln)
		}
		typ, _ := raw["type"].(string)
		if typ == "" {
			t.Fatalf("line %d of the feed carries no type: %q", i+1, ln)
		}
		feed = append(feed, setupFeedLine{Type: typ, Text: ln, Raw: raw})
	}
	return feed
}

// str is a string member of a line, "" when the line does not carry it.
func (l setupFeedLine) str(member string) string {
	s, _ := l.Raw[member].(string)
	return s
}

// kinds counts the feed's lines by kind.
func feedKinds(feed []setupFeedLine) map[string]int {
	kinds := map[string]int{}
	for _, l := range feed {
		kinds[l.Type]++
	}
	return kinds
}

// TestSetupEventsStages: a plan-only run's stages, as lines. The feed is the
// --json document split into lines and nothing else — the same stages, in the
// run's own order, with the same states and the same details — and the run's
// own progress is where it was: on stderr, where --json already puts it.
func TestSetupEventsStages(t *testing.T) {
	r, out, errOut, err := setupEventsRun(t, "setup", "--plan-only", "--json", "--events")
	if err != nil {
		t.Fatalf("the plan-only run failed: %v\n%s", err, out)
	}
	feed := setupFeed(t, out)
	if len(feed) == 0 || feed[len(feed)-1].Type != setupEventDone {
		t.Fatalf("the feed does not end with a done line: %v", feed)
	}
	// The last line carries the code the process exits with, so a consumer that
	// reads only the feed has it: this run converged, so both are 0.
	if got, want := feed[len(feed)-1].Raw["exit_code"], float64(ExitCode(err)); got != want {
		t.Errorf("the done line says exit_code %v, the process exits with %v", got, want)
	}
	// A plan-only run works on no stage, so no `step` line goes out, and a run
	// that leaves nothing for the user has no `left_for_you` line either: what
	// is left is the stages and the end.
	staged := feed[:len(feed)-1]
	for _, l := range staged {
		if l.Type != setupEventStage {
			t.Errorf("a plan-only run wrote a %q line: %+v", l.Type, l.Raw)
		}
	}
	if len(staged) < 10 {
		t.Fatalf("the feed carries %d stage lines, want the run's every stage:\n%s", len(staged), out)
	}

	// The same rig, without --events: the document names the same stages with
	// the same words. The two are compared member for member, because "the
	// stages --json prints today, as separate lines" is the whole of what the
	// flag adds. Each run's own config path is the one thing the two cannot
	// share — a `stage` line's detail names the file the run read, and a second
	// rig writes its own — so it is normalised away before the comparison.
	docRig, doc, _, derr := setupEventsRun(t, "setup", "--plan-only", "--json")
	if derr != nil {
		t.Fatalf("the --json run failed: %v", derr)
	}
	doc = strings.ReplaceAll(doc, docRig.cfg, "<config>")
	for i := range staged {
		staged[i].Raw["detail"] = strings.ReplaceAll(staged[i].str("detail"), r.cfg, "<config>")
	}
	var res initResult
	if err := json.Unmarshal([]byte(doc), &res); err != nil {
		t.Fatalf("--json printed no document (%v):\n%s", err, doc)
	}
	if len(res.Stages) != len(staged) {
		t.Fatalf("the document names %d stages, the feed %d:\n%s", len(res.Stages), len(staged), out)
	}
	for i, s := range res.Stages {
		l := staged[i]
		if l.str("name") != s.Name || l.str("state") != string(s.State) || l.str("detail") != s.Detail || l.str("after") != s.After {
			t.Errorf("stage %d is %+v in the document and %+v in the feed", i, s, l.Raw)
		}
		if undone, _ := l.Raw["undone"].(bool); undone != s.Undone {
			t.Errorf("stage %s: undone is %v in the document and %v in the feed", s.Name, s.Undone, l.Raw["undone"])
		}
	}

	// The run's own account is still the one on stderr, and neither the rows
	// nor the plan's line reaches stdout.
	if !strings.Contains(errOut, "preflight") || !strings.Contains(errOut, "Plan:") {
		t.Errorf("the run printed no progress on stderr:\n%s", errOut)
	}
	if strings.Contains(out, "✔") || strings.Contains(out, "Plan:") {
		t.Errorf("the run printed its own account on stdout:\n%s", out)
	}
}

// TestSetupEventsLeftForYou: a step only the user can do is a line of its own,
// with the one line to run, and the run never waits for it.
//
// The fixture is the case that leaves one without a terminal's being involved
// in the leaving: a coding agent's session, where the installation is the
// user's own step (nothing is applied by such a session). The step is left,
// named with its command, and the run goes on to its own end — a feed that
// carries a `left_for_you` line is a run that did NOT stop there, and that is
// the difference the line makes.
func TestSetupEventsLeftForYou(t *testing.T) {
	stableSelfCommand(t)
	out, errOut, err := setupEventsSessionRun(t, "setup", "--json", "--events")
	if err == nil {
		t.Fatal("the run reported success over a step it left for the user")
	}
	feed := setupFeed(t, out)
	lefts := make([]setupFeedLine, 0, 1)
	for _, l := range feed {
		if l.Type == setupEventLeftForYou {
			lefts = append(lefts, l)
		}
	}
	if len(lefts) != 1 {
		t.Fatalf("the feed carries %d left_for_you lines, want the installation's own:\n%s", len(lefts), out)
	}
	l := lefts[0]
	if l.str("stage") != "installation" || l.str("kind") != "prompt" {
		t.Errorf("the left_for_you line is %+v, want the installation's own prompt", l.Raw)
	}
	if !strings.Contains(l.str("text"), "your own terminal window") {
		t.Errorf("the line does not say where the step can be run: %q", l.str("text"))
	}
	commands, _ := l.Raw["commands"].([]any)
	if len(commands) != 1 || commands[0] != "fugaro setup" {
		t.Errorf("the line's commands are %v, want the one line to run ('fugaro setup')", l.Raw["commands"])
	}
	// The stage's own line behind it says what became of the stage, and its
	// detail is the reason the step was left — the document's own words.
	var stageLine *setupFeedLine
	for i := range feed {
		if feed[i].Type == setupEventStage && feed[i].str("name") == "installation" {
			stageLine = &feed[i]
		}
	}
	if stageLine == nil || stageLine.str("state") != "needs-you" || !strings.Contains(stageLine.str("detail"), "coding agent") {
		t.Errorf("the installation's stage line is %+v, want needs-you with the reason", stageLine)
	}
	// And the feed is the document's own left_for_you entry: the same stage,
	// kind, text and commands. Nothing here is a second account of the step.
	doc, _, derr := setupEventsSessionRun(t, "setup", "--json")
	if derr == nil {
		t.Fatal("the --json run reported success over the same step")
	}
	var res initResult
	if err := json.Unmarshal([]byte(doc), &res); err != nil {
		t.Fatalf("--json printed no document (%v):\n%s", err, doc)
	}
	if len(res.LeftForYou) != 1 {
		t.Fatalf("the document carries %d steps left for the user:\n%s", len(res.LeftForYou), doc)
	}
	lf := res.LeftForYou[0]
	if l.str("stage") != lf.Stage || l.str("kind") != string(lf.Kind) || l.str("text") != lf.Text {
		t.Errorf("the line is %+v, the document's entry %+v", l.Raw, lf)
	}
	if len(commands) != len(lf.Commands) || commands[0] != lf.Commands[0] {
		t.Errorf("the line's commands are %v, the document's are %v", l.Raw["commands"], lf.Commands)
	}

	// A run that left a step for the user is not finished, and it is not a
	// failure either: the feed ends `done` with the code the process exits with
	// — 1, the "step(s) left for you" code — and the lines above it are the
	// steps. An `error` line here would be a surface reading a hand-off as a
	// crash, which is what the console's wizard draws off this line.
	last := feed[len(feed)-1]
	if last.Type != setupEventDone || last.Raw["exit_code"] != float64(ExitUserError) {
		t.Errorf("the feed ends with %+v, want done with exit %d behind the run's own account (%v)", last.Raw, ExitUserError, err)
	}
	for _, l := range feed {
		if l.Type == setupEventError {
			t.Errorf("a run that only left steps for the user wrote an error line: %+v", l.Raw)
		}
	}
	if err.Error() != "fugaro setup is not finished: 1 step(s) left for you, listed above" {
		t.Errorf("the run's own account changed: %v", err)
	}
	// The step was left and not asked for: what the person reads on stderr is
	// the same step, and nothing in the run touched stdin.
	if !strings.Contains(errOut, "your own terminal window") {
		t.Errorf("the step is not on stderr either:\n%s", errOut)
	}
}

// TestSetupEventsNeedsJSON: --events takes the document's place on stdout, so a
// run that asks for lines without --json is refused with the fix rather than
// answered with a mixture of the feed and the account --json keeps on stderr.
func TestSetupEventsNeedsJSON(t *testing.T) {
	_, out, _, err := setupEventsRun(t, "setup", "--plan-only", "--events")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("--events without --json answered %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), "--json --events") {
		t.Errorf("the refusal does not name the fix: %v", err)
	}
	if strings.Contains(out, `"type":`) {
		t.Errorf("the refused run wrote a feed on stdout:\n%s", out)
	}
}

// TestSetupEventsRefusalEndsError is the other end of the one the test above
// pins: a run the door refuses before any stage ends `error`, with no stage of
// its own — it stopped at none — and the refusal's own code. A run with steps
// left for the user is `done` (there is no failure and the steps are the lines
// above it); everything that stops the run without one is this line.
func TestSetupEventsRefusalEndsError(t *testing.T) {
	_, out, _, err := setupEventsRun(t, "setup", "--json", "--events", "--check")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("the refused run answered %v, want a refusal", err)
	}
	feed := setupFeed(t, out)
	last := feed[len(feed)-1]
	if last.Type != setupEventError || last.Raw["exit_code"] != float64(ExitUserError) {
		t.Errorf("the feed ends with %+v, want error with exit %d", last.Raw, ExitUserError)
	}
	if last.str("stage") != "" {
		t.Errorf("the refusal names the stage %q, and it stopped at none", last.str("stage"))
	}
	if !strings.Contains(last.str("error"), "--check") {
		t.Errorf("the line does not carry the refusal's own sentence: %q", last.str("error"))
	}
}

// setupEventsSchema compiles the published contract, and hands back the object
// it was read from as well: a test validating a line uses the first, and one
// reading the kinds it declares uses the second.
func setupEventsSchema(t *testing.T) (*jsonschema.Schema, map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "setup-events.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := doc.(map[string]any)
	if body == nil {
		t.Fatal("schemas/setup-events.schema.json is not an object")
	}
	id, _ := body["$id"].(string)
	if id == "" {
		t.Fatal("schemas/setup-events.schema.json has no $id")
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return schema, body
}

// TestSetupEventsNotice: the lines a run reports outside its stages are lines
// of the feed, under a step's own name — the document's `notices` entry, and
// the same line a surface reading the feed draws — and they come before the
// line that ends the feed (R12, R19).
//
// The producer is the step, not the loop: a step after the stage loop has no
// stage to report under, so its lines reach the feed through the result the
// command ended with (its `notices` member), which is why the writing is
// finished in finish.
func TestSetupEventsNotice(t *testing.T) {
	schema, _ := setupEventsSchema(t)
	var buf bytes.Buffer
	events := newSetupEvents(&buf)
	events.notice(initflow.Notice{
		Step: "notices", Repo: "github/acme/*", State: initflow.RepoNeedsYou,
		Detail: "admitted by pattern, not listed: add the repository by name with `fugaro project repos add`",
	})
	events.finish(&initResult{Notices: []initflow.Notice{{
		Step: "pin", Detail: "the pin in this checkout's fugaro.yaml is not there any more",
	}}}, nil)

	feed := setupFeed(t, buf.String())
	if len(feed) != 3 {
		t.Fatalf("the feed carries %d lines, want the notice, the result's notice and the done line:\n%s", len(feed), buf.String())
	}
	n := feed[0]
	if n.Type != setupEventNotice {
		t.Fatalf("the first line is a %q line, want notice", n.Type)
	}
	if n.str("step") != "notices" || n.str("repo") != "github/acme/*" || n.str("state") != "needs you" {
		t.Errorf("the notice line is %+v, want the step's own name, the pattern and the state it was reported with", n.Raw)
	}
	if !strings.Contains(n.str("detail"), "fugaro project repos add") {
		t.Errorf("the notice line's detail is %q, want the line the step reported", n.str("detail"))
	}
	// A notice about no repository carries both members as the empty string and
	// not a guess at one.
	second := feed[1]
	if second.Type != setupEventNotice || second.str("repo") != "" || second.str("state") != "" || second.str("step") != "pin" {
		t.Errorf("the second line is %+v, want a notice under the pin step with no repository", second.Raw)
	}
	if last := feed[2]; last.Type != setupEventDone {
		t.Errorf("the feed ends with %+v, want the done line behind the notices", last.Raw)
	}
	for i, l := range feed {
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(l.Text))
		if err != nil {
			t.Fatalf("line %d is not JSON (%v): %s", i+1, err, l.Text)
		}
		if err := schema.Validate(inst); err != nil {
			t.Errorf("line %d does not validate against the published schema (%v):\n%s", i+1, err, l.Text)
		}
	}
}

// TestSetupEventsSchema holds the published contract and the feed to each
// other: the schema is what the console reads against — its copy of this
// contract is compared with the golden by I-OPS — and every line a run writes
// validates against it.
func TestSetupEventsSchema(t *testing.T) {
	schema, body := setupEventsSchema(t)

	// Every line of both feeds the CLI writes in these tests validates, member
	// for member.
	_, planOut, _, _ := setupEventsRun(t, "setup", "--plan-only", "--json", "--events")
	_, leftOut, _, _ := setupEventsRun(t, "setup", "--json", "--events")
	_, refusedOut, _, _ := setupEventsRun(t, "setup", "--json", "--events", "--check")
	for _, tc := range []struct {
		name string
		out  string
	}{
		{"the plan-only run", planOut},
		{"the run that leaves a step", leftOut},
		{"the refused run", refusedOut},
	} {
		t.Run(tc.name, func(t *testing.T) {
			feed := setupFeed(t, tc.out)
			if len(feed) == 0 {
				t.Fatalf("the run wrote no feed:\n%s", tc.out)
			}
			for i, l := range feed {
				inst, err := jsonschema.UnmarshalJSON(strings.NewReader(l.Text))
				if err != nil {
					t.Fatalf("line %d is not JSON (%v): %s", i+1, err, l.Text)
				}
				if err := schema.Validate(inst); err != nil {
					t.Errorf("line %d of the feed does not validate (%v):\n%s", i+1, err, l.Text)
				}
			}
		})
	}

	// The schema refuses a line the feed cannot write, so a reader comparing
	// the console's decoder with it is comparing it with something that holds.
	t.Run("a line the feed does not have", func(t *testing.T) {
		for name, line := range map[string]string{
			"an unknown kind":             `{"type":"progress","stage":"installation"}`,
			"a stage with none":           `{"type":"stage"}`,
			"a state no stage has":        `{"type":"stage","name":"installation","state":"maybe","detail":"","after":"","undone":false,"repos":[]}`,
			"a stage with no repos":       `{"type":"stage","name":"installation","state":"done","detail":"","after":"","undone":false}`,
			"a left with no command list": `{"type":"left_for_you","stage":"secrets","kind":"command","text":"run this"}`,
			"a notice with no step":       `{"type":"notice","repo":"","state":"","detail":"something happened"}`,
			"a done with no code":         `{"type":"done"}`,
			"an error with no sentence":   `{"type":"error","stage":"installation","fix":"","exit_code":2}`,
		} {
			t.Run(name, func(t *testing.T) {
				inst, err := jsonschema.UnmarshalJSON(strings.NewReader(line))
				if err != nil {
					t.Fatal(err)
				}
				if err := schema.Validate(inst); err == nil {
					t.Errorf("the schema accepted %s", line)
				}
			})
		}
	})

	// A stage that does per-repository work carries its lines (U13): the lines
	// themselves and the summary they make as the stage's detail, which is what
	// a surface draws per stage. The member is in the schema — a consumer
	// reading the contract has it — and the golden's own lines carry it, which
	// the golden test compares.
	t.Run("a stage with repositories", func(t *testing.T) {
		defs, _ := body["$defs"].(map[string]any)
		stageDef, _ := defs["stage"].(map[string]any)
		props, _ := stageDef["properties"].(map[string]any)
		if _, ok := props["repos"]; !ok {
			t.Errorf("the schema's stage kind declares no repos member: a stage's own lines are part of the contract (U13)")
		}
		required, _ := stageDef["required"].([]any)
		if !slices.Contains(required, "repos") {
			t.Errorf("the schema's stage kind does not require repos: a reader takes the member as always there, an empty array for a stage that reports none (required: %v)", required)
		}
		line := `{"type":"stage","name":"secrets","state":"changed","detail":"3 repositories: 1 done, 1 needs you, 1 unread","after":"","undone":false,` +
			`"repos":[{"repo":"acme/app","state":"done","detail":"stored 1 (git credential)"},{"repo":"acme/web","state":"needs you"},{"repo":"acme/old","state":"unread","detail":"the repository is gone (404)"}]}`
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(line))
		if err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(inst); err != nil {
			t.Errorf("the schema refused a stage line that carries its repositories (%v):\n%s", err, line)
		}
		for _, bad := range []string{
			`{"type":"stage","name":"secrets","state":"changed","detail":"","after":"","undone":false,"repos":[{"repo":"acme/app","state":"half-way"}]}`,
			`{"type":"stage","name":"secrets","state":"changed","detail":"","after":"","undone":false,"repos":[{"state":"done"}]}`,
		} {
			inst, err := jsonschema.UnmarshalJSON(strings.NewReader(bad))
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(inst); err == nil {
				t.Errorf("the schema accepted a repository line the contract has no shape for: %s", bad)
			}
		}
	})

	// The kinds are the console's closed set (SETUP_EVENT_TYPES, new-ops.ts)
	// and the writer's own: a kind added here without one there is a line the
	// console drops.
	t.Run("the kinds are the six", func(t *testing.T) {
		one, _ := body["oneOf"].([]any)
		if len(one) != 6 {
			t.Errorf("the schema has %d line kinds, want the six the feed writes", len(one))
		}
	})
}

// TestSetupEventsGolden pins the feeds a hermetic run writes, line for line:
// the shape the console's decoder is compared against (I-OPS).
//
// Three whole feeds are in the file, in the shape pair-events.golden.jsonl
// keeps its two streams: the plan-only run of a first setup, which is what the
// plan names for this golden and what the console's wizard reads first; the
// run of a coding agent's session, which leaves a step for the user and ends
// `done` with exit 1 behind it; and a run the door refuses before any stage,
// which is the only end that is an `error` line. The second and the third are
// here because the first cannot carry more than two of the five kinds — a
// plan-only run works on no stage and leaves nothing — and a golden a decoder
// is compared against is worth as much as the kinds it exercises. The five
// kinds these runs write are in the file, which the test below checks; the
// sixth, a `notice`, is not, because no step reports one yet: until one does it
// is TestSetupEventsNotice that holds that line's shape. A `repos` member is on
// every stage line, empty, for the same reason.
//
// It is regenerated with -update-setup-events, and the one fact it normalises
// is the run's own temp directory (the config a stage's detail names, which is
// the config file the rig wrote): the command a run names for itself is pinned
// to "fugaro" instead (stableSelfCommand), because a test binary's path is the
// temp directory the go tool built it in.
func TestSetupEventsGolden(t *testing.T) {
	stableSelfCommand(t)
	r, plan, _, err := setupEventsRun(t, "setup", "--plan-only", "--json", "--events")
	if err != nil {
		t.Fatalf("the plan-only run failed: %v\n%s", err, plan)
	}
	left, _, lerr := setupEventsSessionRun(t, "setup", "--json", "--events")
	if lerr == nil {
		t.Fatal("the session run reported success over the step it left for the user")
	}
	_, refused, _, rerr := setupEventsRun(t, "setup", "--json", "--events", "--check")
	if rerr == nil {
		t.Fatal("the refused run answered success")
	}
	got := strings.ReplaceAll(plan, r.cfg, "<config>") + left + refused

	feed := setupFeed(t, got)
	kinds := feedKinds(feed)
	for _, kind := range []string{setupEventStep, setupEventStage, setupEventLeftForYou, setupEventDone, setupEventError} {
		if kinds[kind] == 0 {
			t.Errorf("the golden carries no %s line: %v", kind, kinds)
		}
	}

	path := filepath.Join("testdata", "setup_events.golden.ndjson")
	if *updateSetupEventsGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d lines)", path, strings.Count(got, "\n"))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; the feed is:\n%s", err, got)
	}
	if got != string(want) {
		t.Errorf("the feed is not the golden (regenerate with -update-setup-events):\ngot:\n%s\nwant:\n%s", got, want)
	}
}
