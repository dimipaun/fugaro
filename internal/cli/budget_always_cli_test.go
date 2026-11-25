package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// budget_always_cli_test.go is AB3's own tests (docs/always-a-budget): the
// CLI's readers of the budget mode, which is a word this release has two of —
// observe and enforce — and reads as observe for a mode left unset (C1). Every
// door that used to special-case the removed word refuses it with the
// vocabulary's own sentence, and the two readers of what a person has to do —
// the update's listing and doctor — say the same lines (R8).

// TestValidatePricesEveryMode: a model the price table cannot charge is refused
// with the fix whatever the mode says (D3020), and a model with a price of its
// own passes in the same four states. The mode the rule used to skip was the
// removed word, and a mode left unset was read as observe by the readers this
// command speaks for (C1) — so there is no state left in which nothing prices
// the pins, which is the red step AB3 starts from: a validate that skips them
// for an unset mode.
func TestValidatePricesEveryMode(t *testing.T) {
	const pins = "  auth: api-key\n  model: claude-acme-1\n  models: { background: claude-haiku-4-5 }"
	modes := map[string]string{
		"unset":   "",
		"observe": "budget: { mode: observe }\n",
		"enforce": "budget: { mode: enforce, per_run_usd: 5 }\n",
		"off":     "budget: { mode: off }\n",
	}
	for name, budget := range modes {
		out, err := validateProblems(t, budgetProject(t, budget, pins))
		if ExitCode(err) != ExitUserError || !strings.Contains(out, "claude-acme-1 has no price: add it under model_prices in the project config") {
			t.Errorf("%s: exit %d, out %q", name, ExitCode(err), out)
		}
	}
	// The price comes from the project config's own table, which is where the
	// refusal above sends the reader (P8).
	const priced = "model_prices:\n  claude-acme-1: { input_per_m: 1, output_per_m: 5 }\n"
	for name, budget := range modes {
		if _, err := validateProblems(t, budgetProject(t, budget+priced, pins)); err != nil {
			t.Errorf("%s with a price: %v", name, err)
		}
	}
}

// TestBudgetCommandRefusesTheOldWord: `fugaro budget set --mode off` is refused
// with C1's own text and the fix it names, the way every other door of the
// release refuses the word — a person who typed it is told why it is gone and
// what to write instead. A word that was never a mode is a different refusal:
// the sentence here is the vocabulary's, not an unknown string's.
func TestBudgetCommandRefusesTheOldWord(t *testing.T) {
	isolateProjects(t, t.TempDir())
	for _, args := range [][]string{
		{"budget", "set", "--global", "--mode", "off"},
		{"budget", "set", "--global", "--mode", "off", "--daily", "5"},
	} {
		_, _, err := execute(t, args...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), policy.ModeOffRefusal) {
			t.Errorf("%v: exit %d, err %v", args, ExitCode(err), err)
		}
	}
	_, _, err := execute(t, "budget", "set", "--global", "--mode", "strict")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), `--mode "strict": use observe or enforce`) {
		t.Errorf("strict: exit %d, err %v", ExitCode(err), err)
	}
}

// TestBudgetModeOffFlagIsRefused: the same word on the init command's
// --budget-mode, refused before the run reads anything — a run refused at its
// first flag plans and applies nothing.
func TestBudgetModeOffFlagIsRefused(t *testing.T) {
	r := newFBRig(t)
	_, _, err := executeStdin(t, "", "setup", "--firebase-project", fpID, "--budget-mode", "off")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), policy.ModeOffRefusal) {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("a refused flag reached the cloud: %q", calls)
	}
	// A word that is neither is a different refusal, over the same two words:
	// the sentence above is the vocabulary's and not an unknown string's.
	_, _, err = executeStdin(t, "", "setup", "--firebase-project", fpID, "--budget-mode", "strict")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), `--budget-mode "strict" must be observe or enforce`) {
		t.Errorf("strict: exit %d, err %v", ExitCode(err), err)
	}
}

// TestDoctorNamesAPublishedPolicyThatSaysOff: a node an older CLI published
// still carries the word (D3025), and doctor names it and the one command that
// rewrites it in place, keeping every other member of the node (R4, AB8a) — and
// says nothing where the node says a mode this release has.
func TestDoctorNamesAPublishedPolicyThatSaysOff(t *testing.T) {
	doctorPolicyRig(t, doctorPolicy(func(p *projectpolicy.Policy) { p.Budget.Mode = "off" }), "")
	c, ok := doctorChecksByID(t)["policy-mode-off"]
	if !ok {
		t.Fatalf("doctor says nothing about a node that says off: %v", doctorChecksByID(t))
	}
	if c.Severity != "warning" || !strings.Contains(c.Problem, "budget.mode: off") ||
		!strings.Contains(c.Problem, "no run of this project starts") || !strings.Contains(c.Fix, "fugaro update") {
		t.Errorf("policy-mode-off = %+v", c)
	}

	doctorPolicyRig(t, doctorPolicy(nil), "")
	if c, ok := doctorChecksByID(t)["policy-mode-off"]; ok {
		t.Errorf("a node that says enforce is named: %+v", c)
	}
}

// TestDoctorNamesWhatAlwaysABudgetStops: doctor reports the lines the update
// prints before it changes anything, from the one helper both readers use (R8,
// budgetStops) and in the same words — here a checkout whose own file says the
// removed word, which is that listing's line about this repository.
func TestDoctorNamesWhatAlwaysABudgetStops(t *testing.T) {
	r := newDoctorRig(t)
	noProjectLayerBucket(t)
	const file = "version: 1\nproject: aurora\ngcp_project: proj-1234\n" +
		"agent: { auth: api-key, model: claude-sonnet-5-5, models: { background: claude-haiku-4-5 } }\n" +
		"budget: { mode: off }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	t.Chdir(gitCheckout(t, filepath.Join(r.dir, "app"), file))
	c, ok := doctorChecksByID(t)["budget-stops"]
	if !ok {
		t.Fatalf("doctor does not report what this release stops: %v", doctorChecksByID(t))
	}
	if c.Severity != "warning" || !strings.Contains(c.Problem, "budget.mode: off") ||
		!strings.Contains(c.Problem, "fugaro.yaml") || !strings.Contains(c.Fix, "fugaro update") {
		t.Errorf("budget-stops = %+v", c)
	}

	// A repository that says no such word stops nothing here, and the check is
	// absent rather than empty: a release with nothing to stop prints nothing.
	t.Chdir(gitCheckout(t, filepath.Join(t.TempDir(), "app"), strings.Replace(file, "budget: { mode: off }\n", "", 1)))
	if c, ok := doctorChecksByID(t)["budget-stops"]; ok {
		t.Errorf("a repository that says nothing is reported: %+v", c)
	}
}
