package cli

// U4 of docs/plugin-user-scope/implementation-plan.md (D948, D950): one
// command. `fugaro upgrade` stops existing — cobra's unknown command, no
// tombstone — and the read-only mode it had is `fugaro update --check`'s.
//
// This file is the retirement's own, in the shape left_commands_test.go gives
// the W1.2 one (TestLsIsGone is the other, the tombstone #350 deleted): the
// command is asked of the build's own root rather than of a list kept here, and
// the unknown-command answer is the same one a typo gets.

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestUpgradeIsGone: the name resolves to nothing.
func TestUpgradeIsGone(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "upgrade" {
			t.Fatal("a top-level upgrade command is still registered: U4 folds it into fugaro update, with no tombstone (D950)")
		}
	}
	_, _, err := execute(t, "upgrade", "--check")
	if err == nil || !strings.Contains(err.Error(), `unknown command "upgrade"`) {
		t.Fatalf("fugaro upgrade = %v, want an unknown-command error (D950: not even a tombstone)", err)
	}
	// The exit code is part of the contract, not just the message: with the
	// command gone, `fugaro upgrade` is a typo to cobra, and a typo is a user
	// error (design v1 §9.1). Nothing here touched the cloud.
	if got := ExitCode(err); got != ExitUserError {
		t.Fatalf("fugaro upgrade exit code = %d, want %d (an unknown command is a user error)", got, ExitUserError)
	}
}

// TestUpdateTakesCheckAndNotLocal: what survived of the command's flags.
// `--check` is added to `fugaro update` (read-only, no claude call, no cloud
// call, exit 1 when stale), and `--local` is not: the LocalUnderAgent marker
// absorbs it and `fugaro update-skills` is the plugin-only command.
func TestUpdateTakesCheckAndNotLocal(t *testing.T) {
	update := updateCmdOf(t)
	if update.Flags().Lookup("check") == nil {
		t.Error("fugaro update has no --check: the read-only mode `fugaro upgrade --check` had is this flag's now (D948)")
	}
	if update.Flags().Lookup("local") != nil {
		t.Error("fugaro update takes --local, which D948 dropped: a marker is set by the session and not by a person's roles, so the local half is what plain fugaro update does under one")
	}
}

// TestSetupTakesNoCheck: the flag is the update door's alone. `fugaro setup
// --check` would be a fourth thing to keep in step, and the read-only question
// about an installation that does not exist yet has no answer.
func TestSetupTakesNoCheck(t *testing.T) {
	for _, c := range NewRootCmd().Commands() {
		if c.Name() != "setup" {
			continue
		}
		if c.Flags().Lookup("check") != nil {
			t.Error("fugaro setup takes --check: it is the update door's (D948), where the checkout and the installation it re-converges already exist")
		}
	}
}

func updateCmdOf(t *testing.T) *cobra.Command {
	t.Helper()
	for _, c := range NewRootCmd().Commands() {
		if c.Name() == "update" {
			return c
		}
	}
	t.Fatal("no `fugaro update` in this build")
	return nil
}
