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

// TestSettingsWriteAsksNothing: no settings write asks anything any more
// (D1540). What a person reads is the sentence naming the change — the whole of
// what the y/N confirmation showed, minus the prompt itself — and the write
// lands, with a terminal on stdin and with none: there is no line to answer,
// so neither session has to be refused for want of one. The one question left
// in this path belongs to --ask (D1544) and to askConfirm, which asks it only
// where the flag is registered; nothing here reads stdin on its own, which the
// unread lines below would show.
func TestSettingsWriteAsksNothing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
	}{
		{"no terminal", false},
		{"a terminal", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, lc := settingsEditFixture(t)
			terminal(t, tc.terminal)
			cmd := NewRootCmd()
			var out strings.Builder
			cmd.SetOut(&out)
			cmd.SetIn(strings.NewReader("n\nno\ny\n"))
			if err := applySettingsEdit(cmd, lc, settingsEdit{
				Path: path, Old: []byte("a: 1\n"), New: []byte("a: 2\n"),
				What: "write a", Command: "fugaro settings set a 2 --layer local",
			}); err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}
			got := out.String()
			if !strings.Contains(got, "write a") {
				t.Errorf("the write does not print the sentence naming it:\n%s", got)
			}
			assertNoPrompt(t, got)
			if after := readFileString(t, path); after != "a: 2\n" {
				t.Errorf("the write did not land:\n%s", after)
			}
		})
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
		Kind: settingsDocRepo, Command: "fugaro settings set a 2 --layer repo", Yes: true,
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
		What: "write a", Kind: settingsDocRepo, Command: "fugaro settings set a 2 --layer repo", Yes: true,
	}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := readFileString(t, path); got != "a: 2\n" {
		t.Errorf("the file holds %q, want the new bytes", got)
	}
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
		What: "write a", Kind: settingsDocRepo, Command: "fugaro settings set a 2 --layer repo", Yes: true,
	})
	if err == nil {
		t.Fatal("the write followed a symlink a checkout's file was swapped for")
	}
	if got := readFileString(t, target); got != "a: 1\n" {
		t.Errorf("the write reached through the symlink:\n%s", got)
	}
}

// TestSettingsWriteRefusesASymlinkSwapInTheLayerFile: the project layer file is
// a checkout's kind of document — settingsReadDocument reads it through
// config.ReadRegular, refusing a symlink, a FIFO or an oversized file — so the
// compare-and-swap reads it that way too, and a file swapped for a symlink
// between the caller's read and the write is refused rather than written
// through to whatever the link names (F4).
func TestSettingsWriteRefusesASymlinkSwapInTheLayerFile(t *testing.T) {
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
		What: "write a", Kind: settingsDocLayer, Command: "fugaro settings set a 2 --layer project", Yes: true,
	})
	if err == nil {
		t.Fatal("the write followed a symlink the layer file was swapped for")
	}
	if got := readFileString(t, target); got != "a: 1\n" {
		t.Errorf("the write reached through the symlink:\n%s", got)
	}
}

// TestSettingsWriteNamesTheWideningAndAsksNothing: a widening is one block —
// the diff, and the sentence saying what it widens — and it is the whole of
// what the owner reads: the typed prompt that used to follow it is gone
// (D1540), and the write reads no line at all, with a terminal or without one.
func TestSettingsWriteNamesTheWideningAndAsksNothing(t *testing.T) {
	path, lc := settingsEditFixture(t)
	fakeTerminal(t)
	const why = "this loosens a ceiling, so it is the owner's own step"
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(lc.Name + "\n"))
	if err := applySettingsEdit(cmd, lc, settingsEdit{
		Path: path, Old: []byte("a: 1\n"), New: []byte("a: 2\n"),
		What: "loosen a", Typed: true, TypedWhy: why, Kind: settingsDocRepo,
		Command: "fugaro settings set a 2 --layer repo",
	}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, why) {
		t.Fatalf("the confirmation does not name the widening:\n%s", got)
	}
	if strings.Contains(got, "to confirm: ") {
		t.Errorf("the widening still asks a question:\n%s", got)
	}
	if got := readFileString(t, path); got != "a: 2\n" {
		t.Errorf("the confirmed write did not land:\n%s", got)
	}
}
