//go:build darwin || linux

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude writes a `claude` into dir and returns its path and the marker it
// touches when it runs. It is the login's stand-in and never the real binary:
// it prints one run on the descriptors it was handed, reads one line from the
// one it was handed as stdin, and prints what it read back. Neither run ends
// with a newline, so a copier that reprints them a line at a time shows
// itself, and the first carries an escape sequence, so a sanitizer does too.
func fakeClaude(t *testing.T, dir string) (bin, marker string) {
	t.Helper()
	marker = filepath.Join(dir, "the-login-ran")
	bin = filepath.Join(dir, "claude")
	body := "#!/bin/sh\n" +
		"touch '" + marker + "'\n" +
		"printf '\\033[1mfirst-run\\033[0m'\n" +
		"IFS= read -r typed\n" +
		"printf 'second-run:%s' \"$typed\"\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin, marker
}

// unsetClaudeLoginCredentials clears the two variables the login refuses on,
// so the machine running the tests does not decide what they see: a coding
// agent's own session exports ANTHROPIC_API_KEY.
func unsetClaudeLoginCredentials(t *testing.T) {
	t.Helper()
	for _, name := range claudeLoginCredentialEnv {
		t.Setenv(name, "")
	}
}

// waitTerminal waits until the terminal has displayed want. The pty's reader
// is a goroutine of its own, so bytes reach it as the login writes them —
// which is what makes "the login is still running" assertable.
func waitTerminal(t *testing.T, tty *ttyFixture, want string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if strings.Contains(tty.displayed(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the terminal never showed %q; it showed %q", want, tty.displayed())
		}
	}
}

// TestAccountsAddClaudeGivesTheLoginTheOwnerTerminal: act one hands the login
// the owner's own terminal and nothing else. The child's descriptors are the
// owner's, so what it prints reaches the terminal while it is still running
// and what the owner types reaches it directly — no pipe, no copier, no parse
// (spec §3.1 step 4). The child blocks on the line the test types below, so a
// runner that captured its output into a pipe never shows the first run and
// the wait runs out.
func TestAccountsAddClaudeGivesTheLoginTheOwnerTerminal(t *testing.T) {
	tty := newTTY(t)
	unsetClaudeLoginCredentials(t)
	bin, marker := fakeClaude(t, t.TempDir())
	done := make(chan error, 1)
	go func() { done <- runClaudeLogin(tty.tty, tty.tty, tty.tty) }()

	// The owner sees which binary is about to run, at the path it resolved
	// to, before anything runs (§3.1 step 1, the §6(c) defence).
	waitTerminal(t, tty, bin)
	waitTerminal(t, tty, "\x1b[1mfirst-run\x1b[0m")
	if _, err := tty.master.WriteString("typed-by-the-owner\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runClaudeLogin: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the login did not return")
	}
	// The line reached the login itself: only the child can have printed it
	// back with its own prefix.
	waitTerminal(t, tty, "second-run:typed-by-the-owner")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the login never ran: %v", err)
	}
	// Verbatim and unfiltered: the escape sequence is the child's own bytes,
	// and the run that had no newline still has none.
	if got := tty.displayed(); strings.Contains(got, "first-run\r\n") || strings.Contains(got, "\\u001b") {
		t.Errorf("the terminal shows bytes the login did not write: %q", got)
	}
}

// TestAccountsAddClaudeRefusesAnExportedCredential: an exported
// CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY decides which account the login
// lands on, so the command stops and says so and no child runs at all — a
// refusal, never a deletion of the owner's own variable (spec §3.1 step 3,
// §6(b)).
func TestAccountsAddClaudeRefusesAnExportedCredential(t *testing.T) {
	const credential = "an-account-the-owner-did-not-mean"
	vars := []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}
	for _, name := range vars {
		t.Run(name, func(t *testing.T) {
			tty := newTTY(t)
			unsetClaudeLoginCredentials(t)
			_, marker := fakeClaude(t, t.TempDir())
			t.Setenv(name, credential)

			err := runClaudeLogin(tty.tty, tty.tty, tty.tty)
			if err == nil {
				t.Fatal("the login ran with a credential exported")
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("the refusal does not name %s: %v", name, err)
			}
			if _, statErr := os.Stat(marker); statErr == nil {
				t.Error("a child ran")
			}
		})
	}
}

// TestAccountsAddClaudeRefusesWithoutATerminal: the login is interactive and
// the paste that follows is hidden, so --claude without one is a named
// refusal that names --token-stdin, and no child runs (spec §3.1 step 2).
func TestAccountsAddClaudeRefusesWithoutATerminal(t *testing.T) {
	unsetClaudeLoginCredentials(t)
	_, marker := fakeClaude(t, t.TempDir())
	var out bytes.Buffer

	err := runClaudeLogin(strings.NewReader(""), &out, &out)
	if err == nil {
		t.Fatal("the login ran without a terminal")
	}
	if !strings.Contains(err.Error(), "--token-stdin") {
		t.Errorf("the refusal does not name --token-stdin: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("a child ran")
	}
}
