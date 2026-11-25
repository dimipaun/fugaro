package cli

// The B18 ceiling at the writer (spec §5.6, reconciliation A3): a repository's
// own node is not written above its ceiling — for each dimension the project's
// global cap when it is set, else the default repository cap. The refusal names
// the ceiling and the command that raises it, and it asks nothing.
//
// It is a refusal of the figure and not of who is writing it, so it comes
// before the raise fence: the owner's own command that fence hands back would
// meet this same ceiling.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// refusedAboveCeiling asserts err is the ceiling refusal, naming the ceiling
// and the command that raises it, and that the run put no question to anyone.
func refusedAboveCeiling(t *testing.T, err error, out, ceiling, command string) {
	t.Helper()
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	for _, want := range []string{"caps of acme/app", ceiling, command} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if strings.Contains(out, "CONFIRM") || strings.Contains(out, "Type ") {
		t.Errorf("the refusal asked a question:\n%s", out)
	}
}

// repoDailyOf reads a repository node's daily cap out of the fixture's
// database, as a dollar amount.
func repoDailyOf(t *testing.T, f *budgetFixture, slug string) string {
	t.Helper()
	got := repoCapsOf(t, f, slug)
	if got == nil {
		return ""
	}
	return got["dailyMicros"].(json.Number).String()
}

func TestBudgetSetRepoRefusedAboveCeiling(t *testing.T) {
	t.Run("the default repository cap is the ceiling", func(t *testing.T) {
		f := newBudgetFixture(t, "")
		f.db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 10 * usd1, "repoPerRunMicros": 4 * usd1})
		// A terminal with nothing typed on it: a question would be answered here,
		// and the refusal below is what shows none was put — --ask included.
		typed(t)
		for _, c := range []struct {
			args    []string
			ceiling string
			command string
		}{
			{[]string{"--daily", "50"}, "the ceiling $10", "fugaro budget set --global --daily <usd>"},
			{[]string{"--per-run", "5"}, "the ceiling $4", "fugaro budget set --global --per-run <usd>"},
			{[]string{"--daily", "50", "--ask"}, "the ceiling $10", "fugaro budget set --global --daily <usd>"},
		} {
			out, _, err := executeStdin(t, "", append([]string{"budget", "set", "--repo", "acme/app"}, c.args...)...)
			refusedAboveCeiling(t, err, out, c.ceiling, c.command)
		}
		if got := repoCapsOf(t, f, appSlug); got != nil {
			t.Fatalf("a refused figure was written: %v", got)
		}
	})

	t.Run("a set global cap is the ceiling", func(t *testing.T) {
		f := newBudgetFixture(t, "")
		f.SetCaps(100, 20, 10, 4) // the project's caps, with defaults under them
		// Under the global and over the default: the global is what bounds a
		// repository's own node, so this one lands.
		if _, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--daily", "50"); err != nil {
			t.Fatalf("a figure under the global cap was refused: %v", err)
		}
		for _, c := range []struct {
			args    []string
			ceiling string
		}{
			{[]string{"--daily", "150"}, "the ceiling $100"},
			{[]string{"--per-run", "25"}, "the ceiling $20"},
		} {
			out, _, err := execute(t, append([]string{"budget", "set", "--repo", "acme/app"}, c.args...)...)
			refusedAboveCeiling(t, err, out, c.ceiling, "fugaro budget set --global")
		}
		if got := repoDailyOf(t, f, appSlug); got != "50000000" {
			t.Fatalf("a refused figure moved the node: daily = %s", got)
		}
	})

	t.Run("a figure at the ceiling lands", func(t *testing.T) {
		f := newBudgetFixture(t, "")
		f.SetCaps(100, 20, 10, 4)
		if _, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--daily", "100", "--per-run", "20"); err != nil {
			t.Fatalf("a figure at the ceiling was refused: %v", err)
		}
		if got := repoDailyOf(t, f, appSlug); got != "100000000" {
			t.Fatalf("daily = %s, want the ceiling itself", got)
		}
	})

	t.Run("no ceiling anywhere leaves the node standing", func(t *testing.T) {
		f := newBudgetFixture(t, "")
		if _, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--daily", "50"); err != nil {
			t.Fatalf("a figure with no ceiling anywhere was refused: %v", err)
		}
		if got := repoDailyOf(t, f, appSlug); got != "50000000" {
			t.Fatalf("daily = %s, want the write", got)
		}
	})

	t.Run("a dimension with neither cap has no ceiling", func(t *testing.T) {
		f := newBudgetFixture(t, "")
		f.db.Set(budget.PathCapsDefaults, map[string]any{"repoDailyMicros": 10 * usd1})
		if _, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--per-run", "50"); err != nil {
			t.Fatalf("the per-run dimension has no cap anywhere and was refused: %v", err)
		}
		if _, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--daily", "50"); err == nil {
			t.Fatal("the daily ceiling was not applied")
		}
	})

	t.Run("a coding agent's session reads the ceiling", func(t *testing.T) {
		f := newBudgetFixture(t, "")
		f.SetCaps(100, 20, 10, 4)
		agentSession(t)
		out, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--daily", "150")
		refusedAboveCeiling(t, err, out, "the ceiling $100", "fugaro budget set --global --daily <usd>")
		if strings.Contains(err.Error(), "owner's own step") {
			t.Errorf("the fence's own command meets this ceiling too, so the refusal must name the ceiling: %v", err)
		}
		// Under the ceiling the fence still holds: a session may not raise what
		// bounds it (D1542).
		if _, _, err := execute(t, "budget", "set", "--repo", "acme/app", "--daily", "50"); err == nil || !strings.Contains(err.Error(), "owner's own step") {
			t.Errorf("a raise under the ceiling is no longer the fence's: %v", err)
		}
	})
}
