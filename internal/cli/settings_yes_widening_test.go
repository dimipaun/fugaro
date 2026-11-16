package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/initflow"
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

	out, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo")
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

// TestSettingsSetWideningWithoutYesWritesAndAsksNothing: --yes is not what
// confirms a widening — nothing is asked for it in either session (D1540,
// D1552) — so the same write lands with the flag, without it, and at a session
// with no terminal at all.
func TestSettingsSetWideningWithoutYesWritesAndAsksNothing(t *testing.T) {
	settingsWriteHome(t)
	path := settingsWriteCheckout(t, settingsRepoYAML("budget:\n  per_run_usd: 10\n"))

	noTerminal(t)
	if out, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo"); err != nil {
		t.Fatalf("a widening with no terminal asked for an answer: %v\n%s", err, out)
	}
	if got := readFileString(t, path); !strings.Contains(got, "per_run_usd: 50") {
		t.Errorf("fugaro.yaml does not carry the widened ceiling:\n%s", got)
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

			out, _, err := execute(t, "settings", "set", "budget.per_run_usd", "50", "--layer", "repo")
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

	out, _, err := execute(t, "settings", "unset", "budget.per_run_usd", "--layer", "repo")
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

	out, _, err := execute(t, "settings", "set", "budget.allowed_models", "--add", "claude-opus-5-5", "--layer", "repo")
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

	out, _, err := execute(t, "settings", "set", "claude_account_auto", "true", "--layer", "local")
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

// TestSettingsSetAgentSessionRefusesARowTypedKey: a list the registry marks
// widening (verifier_accounts, D2603) takes --add and nothing else, and the
// other two tests here write scalars — so this is the surface a person's
// `settings set` reaches it through, and what the test pins is the refusal a
// coding agent's session reads on it: the marker, the diff, the sentence
// telling the person the command to run themselves, and a file byte-unchanged.
//
// What it does NOT pin is the classification, and it says so here rather than
// leaving a reader to infer it from the refusal: in an unmanaged project the
// fence trips on the session and the file's kind (D1012) whatever the typed
// flag holds, and the flag is what the row's own TypedWhy moves. That half is
// TestSettingsWriteARowTypedAddIsRefusedWhereTheGateStands, which hands the
// fence a standing gate so that the classification is the only thing that can
// refuse the edit.
func TestSettingsSetAgentSessionRefusesARowTypedKey(t *testing.T) {
	for _, marker := range agentenv.Markers {
		t.Run(marker, func(t *testing.T) {
			noTerminal(t)
			_, localPath := settingsWriteHome(t)
			settingsWriteCheckout(t, settingsRepoYAML(""))
			before := readFileString(t, localPath)
			t.Setenv(marker, "1")

			out, _, err := execute(t, "settings", "set", "verifier_accounts", "--add", settingsProbeItems["verifier_accounts"], "--layer", "local")
			if err == nil {
				t.Fatal("a coding agent's session added a verifier account")
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
			}
			if !strings.Contains(err.Error(), "fugaro settings set verifier_accounts --add "+settingsProbeItems["verifier_accounts"]) {
				t.Errorf("the refusal does not print the command to run at a terminal: %v", err)
			}
			if !strings.Contains(out, "+   - "+settingsProbeItems["verifier_accounts"]) {
				t.Errorf("the refusal does not print the diff:\n%s", out)
			}
			if readFileString(t, localPath) != before {
				t.Error("the file changed in a coding agent's session")
			}
		})
	}
}

// TestSettingsWriteARowTypedAddIsRefusedWhereTheGateStands: the classification
// a row's own TypedWhy decides is what the fence refuses the edit FOR, and this
// is the test that pins that. The edit is the one `settings set
// verifier_accounts --add X --layer local` builds — the row, its sentence, and
// the typed flag settingsTypedWrite answers — and the gate's answer is handed in
// as a standing one, which is a fixture no command line can build: a typed edit
// is never admitted by a real gate (D1026, settingsOwnerOnlyRefusal), so no
// session reaches the fence with Typed true on a project whose gate stands.
//
// The pair is what makes the flag load-bearing: the same edit with the
// classification cleared writes the file, because the gate's standing answer is
// the one fact that lifts the session's refusal. Drop the arm that types a
// row's --add and the first half stops being refused — the mutation the CLI
// test above cannot catch, since an unmanaged project refuses both spellings
// for the session's own reason.
func TestSettingsWriteARowTypedAddIsRefusedWhereTheGateStands(t *testing.T) {
	row, ok := settings.Lookup("verifier_accounts")
	if !ok || row.TypedWhy == "" {
		t.Fatal("verifier_accounts carries no TypedWhy, so this test covers nothing")
	}
	item := settingsProbeItems["verifier_accounts"]
	typed, why := settingsTypedWrite(row, "", item, "add")
	if !typed || why != row.TypedWhy {
		t.Fatalf("settingsTypedWrite(%s, add) = %v %q, want true and the row's own sentence", row.Key, typed, why)
	}
	command := "fugaro settings set verifier_accounts --add " + item + " --layer local"
	// The gate's verdict for an edit it admitted: a managed project whose gate
	// stands (initflow.Managed.Removes).
	standing := initflow.Managed{Managed: true, Admitted: true}
	path, lc := settingsEditFixture(t)
	t.Setenv("CLAUDECODE", "1")
	edit := func(typed bool) settingsEdit {
		return settingsEdit{
			Path: path, Old: []byte("a: 1\n"), New: []byte("a: 2\n"),
			What:  "add " + item + " to verifier_accounts",
			Typed: typed, TypedWhy: why, Managed: standing, Command: command,
		}
	}
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	err := applySettingsEdit(cmd, lc, edit(typed))
	if err == nil {
		t.Fatal("a row-typed --add ran in a coding agent's session with the gate standing")
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "CLAUDECODE") || !strings.Contains(err.Error(), command) {
		t.Errorf("the refusal does not name the marker and the command to run at a terminal: %v", err)
	}
	if !strings.Contains(out.String(), "+ a: 2") {
		t.Errorf("the refusal does not print the diff:\n%s", out.String())
	}
	if got := readFileString(t, path); got != "a: 1\n" {
		t.Errorf("the refused write changed the file:\n%s", got)
	}

	// The same edit with the classification cleared: the gate stands, so the
	// fence stands down and the write lands. It is this half that says the
	// refusal above came from the flag and not from the session's own rule.
	cmd = NewRootCmd()
	out.Reset()
	cmd.SetOut(&out)
	if err := applySettingsEdit(cmd, lc, edit(false)); err != nil {
		t.Fatalf("an untyped edit under a standing gate was refused: %v\n%s", err, out.String())
	}
	if got := readFileString(t, path); got != "a: 2\n" {
		t.Errorf("the untyped edit did not land (%q), and a standing gate is what lets one through:\n%s", got, out.String())
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

			out, _, err := execute(t, "settings", "unset", "budget.per_run_usd", "--layer", "repo")
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
