package cli

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runner"
)

// runIDRuleClause is the clause the run-ID rule carries once the read commands
// take an ending: the one SR7's interface pins, and the form shortIDCommands
// reads. SR14 is what lands it, with the CLI switch it promises.
const runIDRuleClause = "Pass the short ID to `logs`, `diagnose` and `artifacts` (the commands that accept it; `cancel`, `run --retry` take full IDs)"

// backtickedName is one `name` a piece of rule text names.
var backtickedName = regexp.MustCompile("`([^`]+)`")

// runIDRuleLine is the standing instructions' run-ID rule, read out of the
// prompt the implement and fix stages are given rather than out of the constant
// it is written from: the prompt is what an agent reads as fact.
func runIDRuleLine(t *testing.T) string {
	t.Helper()
	prompt := runner.SystemPrompt(runner.PromptData{Branch: "fugaro/20261108-115248-3bb2", Base: "main", StateDir: "/state"}, "")
	for _, line := range strings.Split(prompt, "\n") {
		if strings.Contains(line, "Refer to runs only by their short ID") {
			return line
		}
	}
	t.Fatalf("no line of the standing instructions states the run-ID rule:\n%s", prompt)
	return ""
}

// shortIDCommands are the commands a rule sends a short ID to: the `names`
// between "Pass the short ID to" and the parenthesis that closes the clause. A
// rule without the clause names none.
func shortIDCommands(rule string) []string {
	const clause, close = "Pass the short ID to ", " (the commands that accept it"
	i := strings.Index(rule, clause)
	if i < 0 {
		return nil
	}
	rest := rule[i+len(clause):]
	if j := strings.Index(rest, close); j >= 0 {
		rest = rest[:j]
	}
	var names []string
	for _, m := range backtickedName.FindAllStringSubmatch(rest, -1) {
		names = append(names, m[1])
	}
	return names
}

// TestShortIDCommandsReadsTheClause is the guard on the guard: the check below
// asserts nothing about a rule that names no command, so the reading of a rule
// that does name them is pinned here rather than left to pass as a parse that
// quietly found nothing.
func TestShortIDCommandsReadsTheClause(t *testing.T) {
	rule := "- Refer to runs only by their short ID; " + runIDRuleClause + ", and never print the full run ID."
	if got := shortIDCommands(rule); !slices.Equal(got, []string{"logs", "diagnose", "artifacts"}) {
		t.Errorf("a rule carrying the clause reads as %v, want the three commands it names", got)
	}
	if got := shortIDCommands("- Refer to runs only by their short ID; `cancel` takes a full ID."); len(got) != 0 {
		t.Errorf("a rule without the clause reads as %v, want none", got)
	}
}

// TestRunIDRuleShortIDCommandsResolveAnEnding: the rule tells an agent which
// commands take a short ID, the agent passes what the rule names, and a command
// that refuses that form has cost the run its answer. So every command the rule
// sends a short ID to must resolve one on this tree.
//
// This is what caught SR7's rule naming logs, diagnose and artifacts while all
// three still resolved a run with locateRun — "3bb2" is neither a run ID nor
// <repo-slug>/<run-id> (TestLocateRunStillRefusesAnEnding) — so the clause
// waits for SR14, which switches them to resolveRun (D1701). It fires the day
// the clause is written back without them, or a command is named that this
// check cannot probe.
func TestRunIDRuleShortIDCommandsResolveAnEnding(t *testing.T) {
	names := shortIDCommands(runIDRuleLine(t))
	if len(names) == 0 {
		return
	}
	f := newEndingFixture(t)
	seedTitledRun(t, f.cloudFixture, recentRunID(t, time.Minute, "3bb2"), "fix the parser")
	for _, c := range []struct {
		name string
		args []string
	}{
		{"logs", []string{"logs", "3bb2"}},
		{"diagnose", []string{"diagnose", "3bb2"}},
		{"artifacts", []string{"artifacts", "ls", "3bb2"}},
	} {
		if !slices.Contains(names, c.name) {
			continue
		}
		_, _, err := execute(t, c.args...)
		if err != nil && strings.Contains(err.Error(), "nor a run ID") {
			t.Errorf("the run-ID rule sends %s a short ID, but it refuses one: %v", c.name, err)
		}
	}
	for _, name := range names {
		if !slices.Contains([]string{"logs", "diagnose", "artifacts"}, name) {
			t.Errorf("the run-ID rule sends a short ID to %s, which this check cannot probe: add its invocation above", name)
		}
	}
}
