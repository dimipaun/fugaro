package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/settings"
)

// TestSettingsSetWideningWithYesWritesAndShowsTheDiff: outside a coding agent's
// session --yes answers the widening's typed form, which is the owner's ruling
// D1107 of 2026-11-01 amending D846 for `settings set|unset` alone. The command
// still shows what it is about to do: the diff, and the sentence saying the
// edit raises a limit.
func TestSettingsSetWideningWithYesWritesAndShowsTheDiff(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))

	out, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo", "--yes")
	if err != nil {
		t.Fatalf("--yes did not confirm a widening outside a coding agent's session: %v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "per_run_usd: 50") {
		t.Errorf("fugaro.yaml does not carry the raised cap:\n%s", got)
	}
	if !strings.Contains(out, "+   per_run_usd: 50") {
		t.Errorf("the write does not show the diff it made:\n%s", out)
	}
	if !strings.Contains(out, "raises a limit") {
		t.Errorf("the write does not say the edit raises a limit:\n%s", out)
	}
	// The sentence printed beside "confirmed by --yes" is the one the person
	// reads, so it may not go on denying the flag that answered it.
	if strings.Contains(out, "does not stand in") {
		t.Errorf("the widening's own sentence says --yes does not stand in, right beside the line saying it did:\n%s", out)
	}
}

// TestSettingsSetWideningWithoutYesAsksTheTypedName: --yes is one way to
// confirm a widening, not the only one. Without it the typed project name is
// still what confirms the edit, and a session with no terminal is refused
// (D1107 keeps S5's behaviour for this path).
func TestSettingsSetWideningWithoutYesAsksTheTypedName(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
	before := readFileString(t, path)

	noTerminal(t)
	_, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo")
	if err == nil {
		t.Fatal("a widening with no terminal and no --yes was written")
	}
	if !strings.Contains(err.Error(), "typed confirmation") {
		t.Errorf("the refusal does not name the typed form: %v", err)
	}
	if readFileString(t, path) != before {
		t.Error("the refused write changed fugaro.yaml")
	}

	fakeTerminal(t)
	if out, _, err := executeStdin(t, "aurora\n", "settings", "set", "budget.per_run_usd", "50", "--layer", "repo"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "per_run_usd: 50") {
		t.Errorf("the typed project name did not confirm the widening:\n%s", got)
	}
}

// TestSettingsSetAgentSessionWithYesIsRefused: the fence comes first and --yes
// never lifts it (D1107). The loop is over agentenv.Markers itself rather than
// a copy, so a marker the tree adds is one this rule is held to, and nothing is
// written under any of them.
func TestSettingsSetAgentSessionWithYesIsRefused(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			noTerminal(t) // every one of the six is an agent's session with no terminal
			settingsWriteHome(t)
			path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
			before := readFileString(t, path)
			t.Setenv(marker, "1")

			out, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo", "--yes")
			if err == nil {
				t.Fatal("--yes lifted the agent-session fence for a widening")
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
			}
			if !strings.Contains(err.Error(), "fugaro settings set budget.per_run_usd 50 --layer repo") {
				t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
			}
			if !strings.Contains(out, "+   per_run_usd: 50") {
				t.Errorf("the refusal does not print the diff:\n%s", out)
			}
			if readFileString(t, path) != before {
				t.Error("the file changed in a coding agent's session")
			}
		})
	}
}

// TestSettingsUnsetCeilingWithYesWrites: `unset` is the second door D1107 opens
// --yes at, and removing a ceiling is a widening exactly as raising one is.
func TestSettingsUnsetCeilingWithYesWrites(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))

	out, _, err := execute(t, "settings", "unset", "budget.per_run_usd", "--layer", "repo", "--yes")
	if err != nil {
		t.Fatalf("--yes did not confirm the removal of a ceiling: %v\n%s", err, out)
	}
	if got := readFileString(t, path); strings.Contains(got, "per_run_usd") {
		t.Errorf("unset left the ceiling behind:\n%s", got)
	}
	if !strings.Contains(out, "-   per_run_usd: 10") {
		t.Errorf("the write does not show the diff it made:\n%s", out)
	}
}

// TestSettingsSetAddOnCeilingWithYesWrites: a member added to a ceiling's list
// widens it, and --yes is the console's and a terminal user's word for that
// outside a coding agent's session (D1107).
func TestSettingsSetAddOnCeilingWithYesWrites(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  allowed_models:\n    - claude-sonnet-5\n"))

	out, _, err := execute(t, "settings", "set", "budget.allowed_models", "--add", "claude-opus-5-5", "--layer", "repo", "--yes")
	if err != nil {
		t.Fatalf("--yes did not confirm a member added to a ceiling: %v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "claude-opus-5-5") {
		t.Errorf("fugaro.yaml does not carry the added model:\n%s", got)
	}
	if !strings.Contains(out, "+     - claude-opus-5-5") {
		t.Errorf("the write does not show the added model as an added diff line:\n%s", out)
	}
}

// TestSettingsSetARowTypedKeyWithYesWrites: the typed direction does not only
// come from the code's own sentences. A row the registry marks TypedWhy carries
// its own, and D1107 reaches it the same way: --yes is the second answer to that
// form outside a coding agent's session.
func TestSettingsSetARowTypedKeyWithYesWrites(t *testing.T) {
	_, localPath := settingsWriteHome(t)
	row, ok := settings.Lookup("claude_account_auto")
	if !ok || row.TypedWhy == "" {
		t.Fatal("the registry no longer marks claude_account_auto as a typed key, so this test covers nothing")
	}

	out, _, err := execute(t, "settings", "set", "claude_account_auto", "true", "--layer", "local", "--yes")
	if err != nil {
		t.Fatalf("--yes did not confirm a row-level typed key: %v\n%s", err, out)
	}
	if got := readFileString(t, localPath); !strings.Contains(got, "claude_account_auto: true") {
		t.Errorf("the local config does not carry the key:\n%s", got)
	}
	if !strings.Contains(out, row.TypedWhy) {
		t.Errorf("the write does not print the row's own sentence saying why the form is asked for:\n%s", out)
	}
	if strings.Contains(row.TypedWhy, "does not stand in") {
		t.Errorf("claude_account_auto's own sentence denies the --yes that just answered it: %s", row.TypedWhy)
	}
}

// TestSettingsUnsetAgentSessionWithYesIsRefused: the fence is the write path's
// and not `set`'s — `unset` goes through the same refusal, so --yes lifts it
// there neither. The loop is over agentenv.Markers itself, and nothing is
// written under any of them.
func TestSettingsUnsetAgentSessionWithYesIsRefused(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			noTerminal(t) // every one of the six is an agent's session with no terminal
			settingsWriteHome(t)
			path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))
			before := readFileString(t, path)
			t.Setenv(marker, "1")

			out, _, err := execute(t, "settings", "unset", "budget.per_run_usd", "--layer", "repo", "--yes")
			if err == nil {
				t.Fatal("--yes lifted the agent-session fence for a widening unset")
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
			}
			if !strings.Contains(err.Error(), "fugaro settings unset budget.per_run_usd --layer repo") {
				t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
			}
			if !strings.Contains(out, "-   per_run_usd: 10") {
				t.Errorf("the refusal does not print the diff:\n%s", out)
			}
			if readFileString(t, path) != before {
				t.Error("the file changed in a coding agent's session")
			}
		})
	}
}
