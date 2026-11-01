package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// settingsEditFixture is the shared helper's own fixture: a file to edit and
// the project the confirmation names.
func settingsEditFixture(t *testing.T) (path string, lc *localcfg.Config) {
	t.Helper()
	settingsWriteHome(t)
	path = filepath.Join(t.TempDir(), "doc.yaml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, cfg, err := settingsSelection()
	if err != nil {
		t.Fatal(err)
	}
	return path, cfg
}

// TestSettingsWriteRefusesWithNoTerminalAndNoYes: the ordinary confirmation is
// a terminal's, so a session with neither a terminal nor --yes refuses and
// leaves the file alone.
func TestSettingsWriteRefusesWithNoTerminalAndNoYes(t *testing.T) {
	path, lc := settingsEditFixture(t)
	noTerminal(t)
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	err := applySettingsEdit(cmd, lc, settingsEdit{
		Path: path, Old: []byte("a: 1\n"), New: []byte("a: 2\n"),
		What: "write a", Command: "fugaro settings set a 2 --layer local",
	})
	if err == nil {
		t.Fatal("the write ran with no terminal and no --yes")
	}
	if got := readFileString(t, path); got != "a: 1\n" {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestSettingsWriteYesWritesTheDiffAndABackup: --yes is the whole confirmation
// of an ordinary write, and the write prints the diff, keeps the old bytes in a
// backup beside the file and ends with the one hand-off line's caller.
func TestSettingsWriteYesWritesTheDiffAndABackup(t *testing.T) {
	path, lc := settingsEditFixture(t)
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path: path, Old: []byte("a: 1\n"), New: []byte("a: 2\n"),
		What: "write a", Command: "fugaro settings set a 2 --layer local", Yes: true,
	}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := readFileString(t, path); got != "a: 2\n" {
		t.Errorf("the file holds %q, want the new bytes", got)
	}
	if !strings.Contains(out.String(), "+ a: 2") {
		t.Errorf("the write prints no diff:\n%s", out.String())
	}
	backups, err := filepath.Glob(path + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("%d backups, want one", len(backups))
	}
	if got := readFileString(t, backups[0]); got != "a: 1\n" {
		t.Errorf("the backup holds %q, want the old bytes", got)
	}
}

// TestSettingsWriteWithNothingToChangeWritesNothing: an edit that produces the
// bytes already there is not a write, so no backup is left and nothing moves.
func TestSettingsWriteWithNothingToChangeWritesNothing(t *testing.T) {
	path, lc := settingsEditFixture(t)
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path: path, Old: []byte("a: 1\n"), New: []byte("a: 1\n"),
		What: "write a", Yes: true,
	}); err != nil {
		t.Fatal(err)
	}
	if backups, _ := filepath.Glob(path + ".bak-*"); len(backups) != 0 {
		t.Errorf("%d backups for a no-op edit, want none", len(backups))
	}
	if !strings.Contains(out.String(), "no change") {
		t.Errorf("the no-op says nothing:\n%s", out.String())
	}
}

// TestSettingsWriteTypedRefusesInAnAgentSession: the helper's own half of the
// carve-out — an edit marked Typed is refused in a coding agent's session
// wherever it lands, and the refusal prints the diff and the command.
func TestSettingsWriteTypedRefusesInAnAgentSession(t *testing.T) {
	path, lc := settingsEditFixture(t)
	t.Setenv("CLAUDECODE", "1")
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	err := applySettingsEdit(cmd, lc, settingsEdit{
		Path: path, Old: []byte("a: 1\n"), New: []byte("a: 2\n"),
		What: "loosen a", Typed: true, TypedWhy: "it loosens a ceiling",
		InsideRepo: true, Command: "fugaro settings set a 2 --layer repo", Yes: true,
	})
	if err == nil {
		t.Fatal("a Typed edit ran in a coding agent's session")
	}
	if !strings.Contains(err.Error(), "CLAUDECODE") || !strings.Contains(err.Error(), "fugaro settings set a 2 --layer repo") {
		t.Errorf("the refusal does not name the marker and the command: %v", err)
	}
	if !strings.Contains(out.String(), "+ a: 2") {
		t.Errorf("the refusal prints no diff:\n%s", out.String())
	}
	if got := readFileString(t, path); got != "a: 1\n" {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
}

// TestSettingsWriteInsideRepoRunsInAnAgentSession: the same session writes a
// file inside the repository when the edit is not the typed one.
func TestSettingsWriteInsideRepoRunsInAnAgentSession(t *testing.T) {
	path, lc := settingsEditFixture(t)
	t.Setenv("CLAUDECODE", "1")
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path: path, Old: []byte("a: 1\n"), New: []byte("a: 2\n"),
		What: "write a", InsideRepo: true, Command: "fugaro settings set a 2 --layer repo", Yes: true,
	}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := readFileString(t, path); got != "a: 2\n" {
		t.Errorf("the file holds %q, want the new bytes", got)
	}
}

// TestSettingsWriteAnsweringNoWritesNothing: the y/N confirmation is a
// confirmation, so every answer but y/yes is a refusal with nothing written —
// no backup, no bytes moved. `y` is the other half: the same prompt, answered,
// writes.
func TestSettingsWriteAnsweringNoWritesNothing(t *testing.T) {
	path, lc := settingsEditFixture(t)
	fakeTerminal(t)
	for _, answer := range []string{"n", "no", "", "yep", "yes please"} {
		t.Run(answer, func(t *testing.T) {
			cmd := NewRootCmd()
			var out strings.Builder
			cmd.SetOut(&out)
			cmd.SetIn(strings.NewReader(answer + "\n"))
			err := applySettingsEdit(cmd, lc, settingsEdit{
				Path: path, Old: []byte("a: 1\n"), New: []byte("a: 2\n"),
				What: "write a", Command: "fugaro settings set a 2 --layer local",
			})
			if err == nil {
				t.Fatalf("the write ran on the answer %q", answer)
			}
			if !strings.Contains(err.Error(), "not confirmed") {
				t.Errorf("the refusal does not say nothing was written: %v", err)
			}
			if got := readFileString(t, path); got != "a: 1\n" {
				t.Errorf("the refused write changed the file:\n%s", got)
			}
			if backups, _ := filepath.Glob(path + ".bak-*"); len(backups) != 0 {
				t.Errorf("%d backups for a refused write, want none", len(backups))
			}
		})
	}
	t.Run("y", func(t *testing.T) {
		cmd := NewRootCmd()
		var out strings.Builder
		cmd.SetOut(&out)
		cmd.SetIn(strings.NewReader("y\n"))
		if err := applySettingsEdit(cmd, lc, settingsEdit{
			Path: path, Old: []byte("a: 1\n"), New: []byte("a: 2\n"),
			What: "write a", Command: "fugaro settings set a 2 --layer local",
		}); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		if got := readFileString(t, path); got != "a: 2\n" {
			t.Errorf("the confirmed write did not land:\n%s", got)
		}
	})
}

// TestSettingsWriteRefusesAFileThatChangedSinceItWasRead: the bytes an edit was
// built from may have moved under it — another writer, or this same person in
// another terminal — and writing them would silently discard that edit. The
// re-read is the compare-and-swap: the refusal comes before the backup, so
// nothing is written and nothing is left behind.
func TestSettingsWriteRefusesAFileThatChangedSinceItWasRead(t *testing.T) {
	path, lc := settingsEditFixture(t)
	// The caller read `a: 1`; somebody else has since written `a: 9`.
	if err := os.WriteFile(path, []byte("a: 9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	err := applySettingsEdit(cmd, lc, settingsEdit{
		Path: path, Old: []byte("a: 1\n"), New: []byte("a: 2\n"),
		What: "write a", Command: "fugaro settings set a 2 --layer local", Yes: true,
	})
	if err == nil {
		t.Fatal("the write landed on bytes that had moved since they were read")
	}
	if !strings.Contains(err.Error(), "changed since it was read") {
		t.Errorf("the refusal does not say the file moved: %v", err)
	}
	if got := readFileString(t, path); got != "a: 9\n" {
		t.Errorf("the refused write changed the file:\n%s", got)
	}
	if backups, _ := filepath.Glob(path + ".bak-*"); len(backups) != 0 {
		t.Errorf("%d backups for a refused write, want none", len(backups))
	}
}

// TestSettingsWriteRefusesASymlinkSwapInsideACheckout: the same comparison in a
// checkout reads the strict way, so a file swapped for a symlink between the
// caller's read and the write is refused rather than written through to
// whatever the link names.
func TestSettingsWriteRefusesASymlinkSwapInsideACheckout(t *testing.T) {
	path, lc := settingsEditFixture(t)
	target := filepath.Join(t.TempDir(), "elsewhere.yaml")
	if err := os.WriteFile(target, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	err := applySettingsEdit(cmd, lc, settingsEdit{
		Path: path, Old: []byte("a: 1\n"), New: []byte("a: 2\n"),
		What: "write a", InsideRepo: true, Command: "fugaro settings set a 2 --layer repo", Yes: true,
	})
	if err == nil {
		t.Fatal("the write followed a symlink a checkout's file was swapped for")
	}
	if got := readFileString(t, target); got != "a: 1\n" {
		t.Errorf("the write reached through the symlink:\n%s", got)
	}
}

// TestSettingsWriteNamesTheWideningBeforeTheTypedPrompt: the typed confirmation
// is one block, and the sentence saying what makes this edit different from the
// ordinary y/N is part of it — printed before the prompt, not only in the
// refusal a session with no terminal gets (D846).
func TestSettingsWriteNamesTheWideningBeforeTheTypedPrompt(t *testing.T) {
	path, lc := settingsEditFixture(t)
	fakeTerminal(t)
	const why = "this loosens a ceiling, so the project's own name is what confirms it"
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(lc.Name + "\n"))
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path: path, Old: []byte("a: 1\n"), New: []byte("a: 2\n"),
		What: "loosen a", Typed: true, TypedWhy: why, InsideRepo: true,
		Command: "fugaro settings set a 2 --layer repo",
	}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	got := out.String()
	named, prompted := strings.Index(got, why), strings.Index(got, "Type "+lc.Name+" to confirm: ")
	if named < 0 {
		t.Fatalf("the confirmation does not name the widening:\n%s", got)
	}
	if prompted < 0 {
		t.Fatalf("the confirmation prints no typed prompt:\n%s", got)
	}
	if named > prompted {
		t.Errorf("the widening is named after the prompt it explains:\n%s", got)
	}
	if got := readFileString(t, path); got != "a: 2\n" {
		t.Errorf("the confirmed write did not land:\n%s", got)
	}
}
