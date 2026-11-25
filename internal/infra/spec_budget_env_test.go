package infra

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/runner"
)

// Every job names its mode, whatever the project's local config holds (AB5,
// F13, R3): the entry used to be written only when the config named a mode
// other than off, and a runner in an older image reads a missing entry as off
// — a mode this release removed. An unset mode is observe, which is what the
// migration lands on (D3010) and the reason no image gate is needed (R6). The
// grace goes with it whenever the config sets one, whatever the mode: a
// session is the only thing that reads it.
func TestTheJobAlwaysNamesItsMode(t *testing.T) {
	for name, c := range map[string]struct {
		block, want, grace string
	}{
		"no budget block": {"", "observe", ""},
		"unset mode":      {"budget:\n  per_run_usd: 5\n", "observe", ""},
		"observe":         {"budget:\n  mode: observe\n", "observe", ""},
		"enforce":         {"budget:\n  mode: enforce\n  per_run_usd: 5\n", "enforce", ""},
		"grace alone":     {"budget:\n  unreachable_grace: 90s\n", "observe", "1m30s"},
	} {
		t.Run(name, func(t *testing.T) {
			rs, err := Repo(budgetInputs(t, c.block))
			if err != nil {
				t.Fatal(err)
			}
			if len(rs.Workflows) == 0 {
				t.Fatal("no workflows")
			}
			for _, w := range rs.Workflows {
				if got := w.Env[BudgetModeEnv]; got != c.want {
					t.Errorf("%s names %s=%q, want %q", w.Name, BudgetModeEnv, got, c.want)
				}
				if got := w.Env[BudgetGraceEnv]; got != c.grace {
					t.Errorf("%s names %s=%q, want %q", w.Name, BudgetGraceEnv, got, c.grace)
				}
			}
			// The check job calls no model and reads no policy: it names no
			// mode either.
			if rs.Check != nil {
				if got, ok := rs.Check.Env[BudgetModeEnv]; ok {
					t.Errorf("the check job names %s=%q, want none", BudgetModeEnv, got)
				}
			}
		})
	}
}

// A shared project job names its mode too (AB5, F20): no launcher puts one on
// the execution, so such a job used to take its mode from the published policy
// alone, and one of a project that has published none ran on whatever its image
// assumed. The mode it names is the installation's — the project job is shared
// by every repository admitted to its profile, so no repository's own mode can
// go on it.
func TestASharedJobNamesItsMode(t *testing.T) {
	for name, c := range map[string]struct {
		block, want string
	}{
		"no budget block": {"", "observe"},
		"unset mode":      {"budget:\n  per_run_usd: 5\n", "observe"},
		"enforce":         {"budget:\n  mode: enforce\n  per_run_usd: 5\n", "enforce"},
	} {
		t.Run(name, func(t *testing.T) {
			lc := budgetInputs(t, c.block).LC
			env := projectJobEnv(lc, "fugaro-runs-proj-1234", false)
			if got := env[BudgetModeEnv]; got != c.want {
				t.Errorf("a shared job names %s=%q, want %q: env = %v", BudgetModeEnv, got, c.want, env)
			}
		})
	}
}

// The runner of an image built before this release reads the job's environment
// itself, and it is the reason the entry is written for every job (F13, R6): a
// missing entry is off to that reader, so a job that named none would run a run
// of this release under a mode this release does not have. What it reads from a
// job this release deploys is observe — the mode an unset one means — and the
// mode is on, so the run keeps the gateway and the ledger.
func TestAnOlderImageSeesObserve(t *testing.T) {
	rs, err := Repo(budgetInputs(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs.Workflows) == 0 {
		t.Fatal("no workflows")
	}
	for _, w := range rs.Workflows {
		lookup := func(k string) (string, bool) { v, ok := w.Env[k]; return v, ok }
		s, err := runner.SpendFromEnv(lookup)
		if err != nil {
			t.Fatalf("%s: SpendFromEnv = %v", w.Name, err)
		}
		if s.Mode != "observe" || !s.On() {
			t.Errorf("%s: SpendFromEnv = %+v, want observe and a budget that accounts for spend", w.Name, s)
		}
		// And what the entry says is what the config means: the reader above
		// is the one that used to answer off for this very job.
		if _, ok := w.Env[BudgetModeEnv]; !ok {
			t.Errorf("%s carries no %s: an older image reads that as off", w.Name, BudgetModeEnv)
		}
	}
}
