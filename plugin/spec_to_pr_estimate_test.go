package plugin_test

// S6 of docs/orchestrator-graph-estimates: the estimate reaches the plan and
// the pull-request body through the spec-to-pr skill (D2714's four surfaces,
// its second one), and there is no planner code behind the skill — the planner
// is the user's own agent — so this text is the whole mechanism. A planner not
// told to write a `tier` on each node writes none, and every node of the plan
// is then estimated at the assumed medium; a plan and a pull request that never
// quote the line `fugaro plan graph` prints carry no estimate at all.
//
// Two things are pinned here. The plan step tags each node with the expectation
// tier a run of it is dispatched at (D2711), the same four values the launcher
// spells `--tier`, told apart from the numeric `size` the decoupling gain is
// weighed in. And the plan and the pull-request body quote the read's estimate
// line, in the words the spec's own goal sentence uses (spec.md §1):
// informational, and reserves no budget. A reader who takes the line for a
// reservation, or for a figure the plan has promised to hold to, stops
// believing it the first time a run overshoots it.
//
// The command is resolved against the CLI's own tree, as the skill's other
// commands are, and the example line the reference shows is pinned to what that
// tree really prints: `plan graph` answers a repository with no cached history
// from the seeds, where a task has minutes and no cost at all, so an example
// showing a dollar range would be a line no run of this build can produce and
// an agent would read the real one as wrong.

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/tier"
)

const (
	specToPRSkillPath = "plugin/skills/spec-to-pr/SKILL.md"
	specToPRPlanPath  = "plugin/skills/spec-to-pr/reference/plan.md"
)

// specToPRExampleGraph is the plan the reference's example line is the output
// of: three tasks of the medium tier, no edges, no tier left out and no history
// cached, so every figure comes from the seed and the cost has none — the state
// every repository starts in.
const specToPRExampleGraph = `{
  "schema_version": 3,
  "plan": "docs/plans/example.md",
  "nodes": [
    {"id": "T1", "title": "the first task", "tier": "medium", "size": 1, "run": null, "version": "0.1",
     "footprint": {"prediction": true, "paths": ["internal/a/**"]}},
    {"id": "T2", "title": "the second task", "tier": "medium", "size": 1, "run": null, "version": "0.1",
     "footprint": {"prediction": true, "paths": ["internal/b/**"]}},
    {"id": "T3", "title": "the third task", "tier": "medium", "size": 1, "run": null, "version": "0.1",
     "footprint": {"prediction": true, "paths": ["internal/c/**"]}}
  ],
  "edges": []
}`

// specToPRExampleLine runs `fugaro plan graph` over specToPRExampleGraph on a
// machine with no cache, no project and no home of its own, and returns the
// estimate line it printed.
func specToPRExampleLine(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// The read is offline and answers from the seeds only where the cache is
	// not there, so every directory this CLI keeps is this test's own: a
	// developer's cached history would make the line a different one.
	for _, env := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "HOME"} {
		t.Setenv(env, filepath.Join(dir, env))
	}
	planPath := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(planPath, []byte("# a plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan.ArtifactPath(planPath), []byte(specToPRExampleGraph), 0o600); err != nil {
		t.Fatal(err)
	}
	root := cli.NewRootCmd()
	root.SetArgs([]string{"plan", "graph", "--plan", planPath})
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatalf("fugaro plan graph over the example graph: %v", err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "ready in ") {
			return line
		}
	}
	t.Fatalf("fugaro plan graph printed no estimate line for the example graph:\n%s", out.String())
	return ""
}

// estimateExampleRE is the estimate line the reference shows, as it shows it:
// the one backticked span of that file that starts with the line's first words.
var estimateExampleRE = regexp.MustCompile("`(ready in [^`\n]+)`")

// specToPRStep returns one numbered step of SKILL.md, up to the next heading.
// The step is where a rule has to sit: the plan step is read while the plan is
// written, and a rule filed under another step is one that step's reader never
// arrives at.
func specToPRStep(t *testing.T, body, heading string) string {
	t.Helper()
	head := "### " + heading + "\n"
	i := strings.Index(body, head)
	if i < 0 {
		t.Fatalf("%s has no %q step", specToPRSkillPath, head)
	}
	rest := body[i+len(head):]
	if j := strings.Index(rest, "\n### "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// TestSpecToPRAsksForTierAndEstimate: the plan step asks for a tier on every
// node, and both the plan and the pull-request body quote the estimate line
// `fugaro plan graph` prints, marked informational and not a reservation.
func TestSpecToPRAsksForTierAndEstimate(t *testing.T) {
	skill := readRepoFile(t, specToPRSkillPath)
	plan := readRepoFile(t, specToPRPlanPath)
	planStep := specToPRStep(t, skill, "6. Write the plan")
	bodyStep := specToPRStep(t, skill, "8. Check and open the pull request")

	for _, want := range []struct{ what, text string }{
		{"that every node is tagged with its `tier`", "Tag every node with a `tier`"},
		{"that the tier is not the numeric `size`", "not the numeric `size`"},
		{"that the estimate line the read prints goes in the plan", "the estimate line it prints"},
		{"that the estimate is informational and reserves no budget", "informational and reserves no budget"},
	} {
		if !strings.Contains(planStep, want.text) {
			t.Errorf("%s's plan step never says %s (%q): the graph is what the orchestrator launches from and what the estimate reads each node's figure off, and a planner not told this writes no tier and quotes no estimate", specToPRSkillPath, want.what, want.text)
		}
	}

	// The reference is where the graph's own section lives, so the four values
	// have to be there and not only in SKILL.md's step: the step sends the
	// planner to the reference for the format. The names are read off the
	// vocabulary rather than typed here, so a tier renamed in internal/tier is
	// a skill that no longer spells what `--tier` takes.
	for _, tr := range tier.Tiers {
		if !strings.Contains(plan, "`"+string(tr)+"`") {
			t.Errorf("%s never names the tier %q: the estimate has no figure for a node whose tier the planner was never told, and the launcher has no value to dispatch it at", specToPRPlanPath, tr)
		}
	}
	if !strings.Contains(plan, "`tier`") {
		t.Errorf("%s never names the node's `tier`: the field is additive to schema 3 (D2711), so a planner who is not told it exists writes a graph with none and every node is estimated at the assumed medium", specToPRPlanPath)
	}
	if !strings.Contains(plan, "informational and reserves no budget") {
		t.Errorf("%s never says the estimate is informational and reserves no budget: the spec's own goal sentence says so (spec.md §1), and a figure read as a reservation is one the first overshoot discredits", specToPRPlanPath)
	}
	if !strings.Contains(plan, "`no history yet for N tasks`") {
		t.Errorf("%s never says the cost reads `no history yet for N tasks` where the history does not cover the plan: there is no cost seed, so a planner told only that the line carries a cost expects a dollar figure the read cannot print", specToPRPlanPath)
	}

	for _, want := range []struct{ what, text string }{
		{"quotes the line the graph read printed", "estimate line"},
		{"marks the estimate informational and not a reservation", "not a reservation"},
	} {
		if !strings.Contains(bodyStep, want.text) {
			t.Errorf("%s's pull-request step never says the body %s (%q): the estimate is one of the four surfaces it appears on (D2714), and the body is the one a reviewer reads before anything is launched", specToPRSkillPath, want.what, want.text)
		}
	}

	// The line the skill sends a planner to read is one this build prints:
	// `--plan` is the flag the read takes, and the whole command resolves in
	// the CLI's own tree.
	if c := resolveSkillCommand(cli.NewRootCmd(), "plan", "graph", "--plan", "PLAN"); !c.known() {
		t.Fatalf("`fugaro plan graph --plan` does not resolve in the CLI's own tree (%+v): the skill quotes a line the released binary does not print", c)
	}

	// And the example is that read's own output, run here rather than trusted:
	// a seeds-only repository has no cost figure to print, so an example
	// showing a dollar range — or leaving out the clause that says where the
	// table came from — is a line no run of this build produces, and an agent
	// shown it would take the real line for a broken one.
	example := estimateExampleRE.FindStringSubmatch(plan)
	if example == nil {
		t.Fatalf("%s shows no `ready in ...` example of the estimate line: a planner has nothing to compare the read's output against", specToPRPlanPath)
	}
	if got := specToPRExampleLine(t); example[1] != got {
		t.Errorf("%s shows the estimate line as %q, but `fugaro plan graph` prints %q for the plan that example describes: the skill's example has to be a line the read really prints, or a planner reads the real one as wrong",
			specToPRPlanPath, example[1], got)
	}
}
