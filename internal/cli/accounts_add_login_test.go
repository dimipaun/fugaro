//go:build darwin || linux

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// loginFake is the login's stand-in: the script, the marker it touches when it
// runs, and the two files it writes about the child the runner handed it.
type loginFake struct {
	bin    string // on PATH as claude
	marker string // touched when it runs
	pgid   string // the process group it ran in, as ps reported it
	env    string // the environment it was handed
}

// fakeClaude writes a `claude` into dir. The script is the login's stand-in
// and never the real binary: it reports what the runner gave it — whether all
// three of its descriptors are terminals, its process group, its environment —
// then prints one run on the descriptors it was handed, reads one line from
// the one it was handed as stdin, and prints what it read back. Neither run
// ends with a newline, so a copier that reprints them a line at a time shows
// itself, and the first carries an escape sequence, so a sanitizer does too.
func fakeClaude(t *testing.T, dir string) loginFake {
	t.Helper()
	f := loginFake{
		bin:    filepath.Join(dir, "claude"),
		marker: filepath.Join(dir, "the-login-ran"),
		pgid:   filepath.Join(dir, "the-login-pgid"),
		env:    filepath.Join(dir, "the-login-env"),
	}
	body := "#!/bin/sh\n" +
		"touch '" + f.marker + "'\n" +
		"ps -o pgid= -p $$ | tr -d ' ' > '" + f.pgid + "'\n" +
		"env > '" + f.env + "'\n" +
		"[ -t 0 ] && [ -t 1 ] && [ -t 2 ] && printf 'fd-tty'\n" +
		"printf '\\033[1mfirst-run\\033[0m'\n" +
		"IFS= read -r typed\n" +
		"printf 'second-run:%s' \"$typed\"\n"
	if err := os.WriteFile(f.bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

// pgidSeen is the process group the login ran in.
func (f loginFake) pgidSeen(t *testing.T) int {
	t.Helper()
	raw, err := os.ReadFile(f.pgid)
	if err != nil {
		t.Fatalf("the login did not report its process group: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("the login's process group %q is not a number: %v", raw, err)
	}
	return n
}

// envSeen is the environment the login was handed, one NAME=value per line.
func (f loginFake) envSeen(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(f.env)
	if err != nil {
		t.Fatalf("the login did not report its environment: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// shellEnvironment is what a POSIX shell sets for itself in an environment it
// was handed: dash sets PWD. It is the shell's own doing and not the runner's,
// so the comparison below allows these names and nothing else.
var shellEnvironment = []string{"PWD"}

// assertChildEnv holds the environment the login was handed to the owner's own
// (spec §6(b)): every variable must be there with the value it has here — a
// runner that filtered or curated the child's environment fails on this line —
// and nothing may have been added to it except what a shell adds for itself.
func assertChildEnv(t *testing.T, got, want []string) {
	t.Helper()
	seen, owner := envMap(got), envMap(want)
	for name, value := range owner {
		if slices.Contains(shellEnvironment, name) || strings.Contains(value, "\n") {
			continue // the login's own file is one variable per line
		}
		switch got, ok := seen[name]; {
		case !ok:
			t.Errorf("the login was not handed %s", name)
		case got != value:
			t.Errorf("the login was handed %s=%q, want %q", name, got, value)
		}
	}
	for name, value := range seen {
		if _, ok := owner[name]; !ok && !slices.Contains(shellEnvironment, name) {
			t.Errorf("the login was handed %s=%q, which fugaro does not set", name, value)
		}
	}
}

// envMap is a list of NAME=value lines as a map; a name without "=" is dropped,
// since a shell's own file cannot have one. A value carrying a newline reads as
// two entries here, which is why assertChildEnv skips those.
func envMap(lines []string) map[string]string {
	m := make(map[string]string, len(lines))
	for _, line := range lines {
		if name, value, ok := strings.Cut(line, "="); ok {
			m[name] = value
		}
	}
	return m
}

// waitLogin waits for a runClaudeLogin call to return. Nothing here may hang:
// a refusal that never returns, or a login that blocks on a terminal the test
// never types at, is a failure of the test and not a wait for the package's
// own ten-minute timeout.
func waitLogin(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("runClaudeLogin did not return")
		return nil
	}
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
// the owner's own terminal and nothing else. All three of the child's
// descriptors are that terminal, so what it prints reaches it while it is
// still running and what the owner types reaches it directly — no tee, no
// pipe, no copier, no parse (spec §3.1 step 4). The child blocks on the line
// the test types below, so a runner that captured its output into a pipe
// never shows this and the wait runs out.
func TestAccountsAddClaudeGivesTheLoginTheOwnerTerminal(t *testing.T) {
	tty := newTTY(t)
	unsetClaudeLoginCredentials(t)
	fake := fakeClaude(t, t.TempDir())
	done := make(chan error, 1)
	go func() { done <- runClaudeLogin(tty.tty, tty.tty, tty.tty) }()

	// The owner sees which binary is about to run, at the path it resolved
	// to, before anything runs (§3.1 step 1, the §6(c) defence).
	waitTerminal(t, tty, fake.bin)
	waitTerminal(t, tty, "fd-tty")
	waitTerminal(t, tty, "\x1b[1mfirst-run\x1b[0m")
	if _, err := tty.master.WriteString("typed-by-the-owner\n"); err != nil {
		t.Fatal(err)
	}
	if err := waitLogin(t, done); err != nil {
		t.Fatalf("runClaudeLogin: %v", err)
	}
	// The line reached the login itself: only the child can have printed it
	// back with its own prefix.
	waitTerminal(t, tty, "second-run:typed-by-the-owner")
	if _, err := os.Stat(fake.marker); err != nil {
		t.Fatalf("the login never ran: %v", err)
	}
	// Verbatim and unfiltered: the escape sequence is the child's own bytes,
	// and the run that had no newline still has none.
	if got := tty.displayed(); strings.Contains(got, "first-run\r\n") || strings.Contains(got, "\\u001b") {
		t.Errorf("the terminal shows bytes the login did not write: %q", got)
	}
	// The login runs in fugaro's own process group — runClaudeLogin sets no
	// Setpgid — so it is the terminal's foreground job and the owner's Ctrl-C
	// reaches it directly (§3.1 step 4). The whole group is signalled, which
	// is what makes the hold below the only thing fugaro has to do; the hold
	// itself stays code-reviewed, since this process cannot send itself a
	// signal without the test binary taking it too.
	if got, want := fake.pgidSeen(t), syscall.Getpgrp(); got != want {
		t.Errorf("the login ran in process group %d, want fugaro's own %d: a Setpgid child is not the terminal's foreground job", got, want)
	}
	assertChildEnv(t, fake.envSeen(t), os.Environ())
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
			fake := fakeClaude(t, t.TempDir())
			t.Setenv(name, credential)

			done := make(chan error, 1)
			go func() { done <- runClaudeLogin(tty.tty, tty.tty, tty.tty) }()
			err := waitLogin(t, done)
			if err == nil {
				t.Fatal("the login ran with a credential exported")
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("the refusal does not name %s: %v", name, err)
			}
			if _, statErr := os.Stat(fake.marker); statErr == nil {
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
	fake := fakeClaude(t, t.TempDir())
	var out bytes.Buffer

	done := make(chan error, 1)
	go func() { done <- runClaudeLogin(strings.NewReader(""), &out, &out) }()
	err := waitLogin(t, done)
	if err == nil {
		t.Fatal("the login ran without a terminal")
	}
	if !strings.Contains(err.Error(), "--token-stdin") {
		t.Errorf("the refusal does not name --token-stdin: %v", err)
	}
	if _, statErr := os.Stat(fake.marker); statErr == nil {
		t.Error("a child ran")
	}
}
