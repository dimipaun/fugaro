//go:build darwin || linux

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
)

// accounts_add_claude_test.go is `accounts add --claude` end to end (spec §3):
// act one runs Anthropic's own login on the owner's own terminal, and act two
// is the hidden paste the command uses without the flag. The value the login
// prints is never a source — D862's answer (a) — so what these tests assert is
// the paste and only the paste.
//
// The command runs on a pseudo-terminal, because both acts need one: the child
// inherits the terminal and the paste is read from it without echo.

// loginStub is the login's stand-in: a script under t.TempDir(), never a real
// claude, that prints what the login prints, touches a marker when it runs and
// exits with the status the test asked for.
type loginStub struct {
	bin    string // on PATH as claude
	marker string // touched when it runs
}

// loginPrint is what the stub prints: a value-shaped run of characters and
// deliberately not a token-shaped one, because no literal in this tree may read
// as a secret. What the tests need is only that a capture would have something
// to capture.
func loginPrint() string { return strings.Repeat("q", 48) }

// loginStubOnPath writes the stub into a directory of its own and puts that
// directory at the front of PATH, so the lookup resolves to it and to nothing
// else.
func loginStubOnPath(t *testing.T, stdout string, status int) loginStub {
	t.Helper()
	dir := t.TempDir()
	s := loginStub{bin: filepath.Join(dir, "claude"), marker: filepath.Join(dir, "the-login-ran")}
	body := "#!/bin/sh\n" +
		"touch '" + s.marker + "'\n" +
		"printf '%s' '" + stdout + "'\n" +
		"exit " + strconv.Itoa(status) + "\n"
	if err := os.WriteFile(s.bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return s
}

// loginFindable restores the real lookup for a test whose subject is the
// command rather than the rig: the converge rig replaces claudeLookPath with
// ErrNotFound so that no test runs a developer's own claude, and these tests
// hand the command a stub of their own instead.
func loginFindable(t *testing.T) {
	t.Helper()
	old := claudeLookPath
	claudeLookPath = exec.LookPath
	t.Cleanup(func() { claudeLookPath = old })
}

// hostileLookup answers every claude lookup with the case's own path, so a
// refusal about what the lookup returned is decided by this answer and never by
// whatever the machine running the tests happens to have on PATH.
func hostileLookup(t *testing.T, answer func(string) (string, error)) {
	t.Helper()
	old := claudeLookPath
	claudeLookPath = answer
	t.Cleanup(func() { claudeLookPath = old })
}

// accountSecretID is the secret an account's value lands under, through the one
// function that names it (gcp.ProjectSecretID).
func accountSecretID(name string) string {
	return gcp.ProjectSecretID(initProjectName, config.ClaudeAccountSecret(name))
}

// runOnTerminal runs the command the way the owner's shell does — the terminal
// is its stdin, stdout and stderr — so the login inherits it and the hidden
// paste is read from it.
func runOnTerminal(t *testing.T, tty *ttyFixture, args ...string) <-chan error {
	t.Helper()
	cmd := NewRootCmd()
	cmd.SetIn(tty.tty)
	cmd.SetOut(tty.tty)
	cmd.SetErr(tty.tty)
	cmd.SetArgs(NormalizeArgs(args))
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	return done
}

// waitCommand waits for a command started by runOnTerminal. Nothing here may
// hang: a command that never returns is a failure of the test and not a wait
// for the package's own timeout.
func waitCommand(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Minute):
		t.Fatal("the command did not return")
		return nil
	}
}

// pasteAtThePrompt waits for the hidden prompt for name and types value at the
// terminal: the one token path, with or without --claude (§3.2).
func pasteAtThePrompt(t *testing.T, tty *ttyFixture, name, value string) {
	t.Helper()
	waitTerminal(t, tty, "Paste the value for "+name)
	if _, err := tty.master.WriteString(value + "\n"); err != nil {
		t.Fatal(err)
	}
}

// TestAccountsAddClaudeRunsTheLoginThenAsksForOnePaste is A3's red step: the
// login runs to completion on the owner's own terminal and the token is then
// read at the hidden prompt — and only there. The value the account ends up
// with is the one pasted, never anything the login printed, which is what an
// implementation that read the token out of the login's output fails on (D862's
// answer (a), spec §3.2).
func TestAccountsAddClaudeRunsTheLoginThenAsksForOnePaste(t *testing.T) {
	r := newAddRig(t)
	unsetClaudeLoginCredentials(t)
	loginFindable(t)
	login := loginStubOnPath(t, loginPrint(), 0)
	tty := newTTY(t)

	done := runOnTerminal(t, tty, "accounts", "add", "team-a", "--repos", "owner/repo-a", "--claude", "--yes")
	// The owner sees which binary is about to run, at the path it resolved to,
	// before anything runs, and then that it ran (§3.1 step 1).
	waitTerminal(t, tty, login.bin)
	waitTerminal(t, tty, loginPrint())
	pasteAtThePrompt(t, tty, config.ClaudeAccountSecret("team-a"), addToken())
	if err := waitCommand(t, done); err != nil {
		t.Fatalf("accounts add --claude: %v\nterminal:\n%s", err, tty.displayed())
	}

	if _, err := os.Stat(login.marker); err != nil {
		t.Fatalf("the login never ran: %v", err)
	}
	if got := string(r.sm.Latest(accountSecretID("team-a"))); got != addToken() {
		t.Errorf("the account's secret holds %q, want the token pasted at the hidden prompt: the login's output is never a source", got)
	}
}

// TestAccountsAddClaudeWritesTheValueNowhere: the login prints a value-shaped
// run of characters on its own stdout, and nothing fugaro writes holds it — the
// value comes only from the paste. The terminal shows the login's own bytes
// once, which is the child's print and not a copy fugaro made (spec §3.2).
func TestAccountsAddClaudeWritesTheValueNowhere(t *testing.T) {
	r := newAddRig(t)
	unsetClaudeLoginCredentials(t)
	loginFindable(t)
	printed := loginPrint()
	loginStubOnPath(t, printed, 0)
	tty := newTTY(t)

	done := runOnTerminal(t, tty, "accounts", "add", "team-a", "--repos", "owner/repo-a", "--claude", "--yes")
	waitTerminal(t, tty, printed)
	pasteAtThePrompt(t, tty, config.ClaudeAccountSecret("team-a"), addToken())
	if err := waitCommand(t, done); err != nil {
		t.Fatalf("accounts add --claude: %v\nterminal:\n%s", err, tty.displayed())
	}

	if got := string(r.sm.Latest(accountSecretID("team-a"))); got != addToken() {
		t.Errorf("the account's secret holds %q, want the token pasted at the hidden prompt", got)
	}
	if n := strings.Count(tty.displayed(), printed); n != 1 {
		t.Errorf("the terminal shows what the login printed %d times, want once: the child's own bytes and no copy fugaro made", n)
	}
	if strings.Contains(tty.displayed(), addToken()) {
		t.Errorf("the terminal shows the pasted value, so the hidden prompt echoed it")
	}
	if cfg := readFileString(t, r.cfg); strings.Contains(cfg, printed) || strings.Contains(cfg, addToken()) {
		t.Errorf("the local config carries a value:\n%s", cfg)
	}
}

// TestAccountsAddClaudeRunsTheLoginWithoutAsking is the owner's ruling of
// 2026-11-02 as D1540 leaves it: the whole block is shown ONCE, before the
// login runs, so a person reads what is about to happen — including the
// resolved claude path it names — but nothing waits on an answer to it any
// more, and the run goes on to the login and the paste. The terminal here is a
// real one and neither --ask nor --yes is passed: a confirmation that was
// still asked for would leave this run sitting at it, and the deadline in
// waitTerminal is what would fail first.
func TestAccountsAddClaudeRunsTheLoginWithoutAsking(t *testing.T) {
	r := newAddRig(t)
	unsetClaudeLoginCredentials(t)
	loginFindable(t)
	login := loginStubOnPath(t, loginPrint(), 0)
	tty := newTTY(t)

	done := runOnTerminal(t, tty, "accounts", "add", "team-a", "--repos", "owner/repo-a", "--claude")
	waitTerminal(t, tty, loginPrint())
	pasteAtThePrompt(t, tty, config.ClaudeAccountSecret("team-a"), addToken())
	if err := waitCommand(t, done); err != nil {
		t.Fatalf("accounts add --claude: %v\nterminal:\n%s", err, tty.displayed())
	}
	if _, statErr := os.Stat(login.marker); statErr != nil {
		t.Fatalf("the login did not run: %v", statErr)
	}
	if got := string(r.sm.Latest(accountSecretID("team-a"))); got != addToken() {
		t.Errorf("the account's secret holds %q, want the token pasted at the hidden prompt", got)
	}
	shown := tty.displayed()
	// The block names the program it is about to run, at the path it resolved
	// to (spec §3.1 step 1), and it is printed before the child starts: a
	// person reads which program runs even though nobody is asked about it.
	naming, running := strings.Index(shown, login.bin), strings.Index(shown, "running: "+login.bin)
	if naming < 0 {
		t.Errorf("the run never names the resolved claude path %q, so a person reads about a program it does not identify:\n%s", login.bin, shown)
	} else if running < 0 || naming > running {
		t.Errorf("the resolved claude path is not named before the login starts (%d, %d):\n%s", naming, running, shown)
	}
	// The command's own half of the transcript is everything above the child it
	// starts, and that half asks nothing: the converge's stages below it are
	// their own readers (their blocks are not this command's, and T7 is where
	// they lose theirs).
	if running < 0 {
		t.Fatalf("the login never started:\n%s", shown)
	}
	assertNoPrompt(t, shown[:running])
}

// TestAccountsAddClaudeYesRunsTheLogin: --yes is the same single answer
// wherever it is given, and the login that follows it is the command's own act
// one — the child runs, and the value it prints is still not the value stored.
func TestAccountsAddClaudeYesRunsTheLogin(t *testing.T) {
	r := newAddRig(t)
	unsetClaudeLoginCredentials(t)
	loginFindable(t)
	login := loginStubOnPath(t, loginPrint(), 0)
	tty := newTTY(t)

	done := runOnTerminal(t, tty, "accounts", "add", "team-a", "--repos", "owner/repo-a", "--claude", "--yes")
	waitTerminal(t, tty, loginPrint())
	pasteAtThePrompt(t, tty, config.ClaudeAccountSecret("team-a"), addToken())
	if err := waitCommand(t, done); err != nil {
		t.Fatalf("accounts add --claude --yes: %v\nterminal:\n%s", err, tty.displayed())
	}
	if _, err := os.Stat(login.marker); err != nil {
		t.Fatalf("--yes did not run the login: %v", err)
	}
	if got := string(r.sm.Latest(accountSecretID("team-a"))); got != addToken() {
		t.Errorf("the account's secret holds %q, want the token pasted at the hidden prompt", got)
	}
}

// TestAccountsAddClaudeAsksOnlyForThePaste: the command's own question is gone
// (D1540), and the one thing left that stops a run at a terminal is the value
// entry — the hidden paste that is the only token path (§3.2). The run below
// answers nothing by hand and goes all the way to the stored secret, so a
// confirmation anywhere on the path would show up here as a prompt nobody
// answered — and the paste is asserted to be asked for exactly once.
func TestAccountsAddClaudeAsksOnlyForThePaste(t *testing.T) {
	r := newAddRig(t)
	unsetClaudeLoginCredentials(t)
	loginFindable(t)
	loginStubOnPath(t, loginPrint(), 0)
	tty := newTTY(t)

	done := runOnTerminal(t, tty, "accounts", "add", "team-a", "--repos", "owner/repo-a", "--claude")
	waitTerminal(t, tty, loginPrint())
	pasteAtThePrompt(t, tty, config.ClaudeAccountSecret("team-a"), addToken())
	if err := waitCommand(t, done); err != nil {
		t.Fatalf("accounts add --claude: %v\nterminal:\n%s", err, tty.displayed())
	}
	if got := string(r.sm.Latest(accountSecretID("team-a"))); got != addToken() {
		t.Errorf("the account's secret holds %q, want the token pasted at the hidden prompt", got)
	}
	// What the command itself asks, above the stages it converges, is nothing;
	// the one thing left at a terminal that stops a run is the value entry
	// below it, and it is asked for exactly once.
	shown := tty.displayed()
	running := strings.Index(shown, "running: ")
	if running < 0 {
		t.Fatalf("the login never started:\n%s", shown)
	}
	assertNoPrompt(t, shown[:running])
	if n := strings.Count(shown, "Paste the value for "+config.ClaudeAccountSecret("team-a")); n != 1 {
		t.Errorf("the terminal asks for the token %d times, want once:\n%s", n, shown)
	}
}

// TestAccountsAddClaudeStillAsksForThePasteAfterAFailedLogin is spec §3.3: the
// login is never a gate. A child that exits non-zero is not a failure of the
// command — the command reports the exit status, says why the login stored
// nothing, and still reads the token at the hidden prompt.
func TestAccountsAddClaudeStillAsksForThePasteAfterAFailedLogin(t *testing.T) {
	r := newAddRig(t)
	unsetClaudeLoginCredentials(t)
	loginFindable(t)
	loginStubOnPath(t, "", 3)
	tty := newTTY(t)

	done := runOnTerminal(t, tty, "accounts", "add", "team-a", "--repos", "owner/repo-a", "--claude", "--yes")
	waitTerminal(t, tty, "exit status 3")
	waitTerminal(t, tty, "the hidden prompt below is the only token path")
	pasteAtThePrompt(t, tty, config.ClaudeAccountSecret("team-a"), addToken())
	if err := waitCommand(t, done); err != nil {
		t.Fatalf("a login that exited non-zero failed the command: %v\nterminal:\n%s", err, tty.displayed())
	}
	if got := string(r.sm.Latest(accountSecretID("team-a"))); got != addToken() {
		t.Errorf("the account's secret holds %q, want the token pasted at the hidden prompt", got)
	}
}

// TestAccountsAddClaudeRefusesAHostileClaude is spec §3.1 step 1 through the
// command: a claude the lookup resolves to but fugaro will not run — a relative
// PATH entry (exec.ErrDot), a path that is not absolute, a directory — is a
// refusal that writes nothing, and no child starts under it.
func TestAccountsAddClaudeRefusesAHostileClaude(t *testing.T) {
	cases := []struct {
		name    string
		hostile func(t *testing.T) string // arms the lookup, and answers the sentence the refusal owes
	}{
		{"a relative PATH entry", func(t *testing.T) string {
			loginFindable(t)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			t.Setenv("PATH", "."+string(os.PathListSeparator)+os.Getenv("PATH"))
			return "relative PATH entry"
		}},
		{"a path that is not absolute", func(t *testing.T) string {
			hostileLookup(t, func(string) (string, error) { return "claude", nil })
			return "not an absolute path"
		}},
		{"a directory", func(t *testing.T) string {
			dir := t.TempDir()
			hostileLookup(t, func(string) (string, error) { return dir, nil })
			return dir
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newAddRigWith(t, accountsAddLocal)
			unsetClaudeLoginCredentials(t)
			before := readFileString(t, r.cfg)
			want := tc.hostile(t)
			tty := newTTY(t)

			done := runOnTerminal(t, tty, "accounts", "add", "team-a", "--repos", "acme/app", "--claude", "--yes")
			err := waitCommand(t, done)
			if err == nil {
				t.Fatalf("a claude fugaro will not run was run:\n%s", tty.displayed())
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q: %v", want, err)
			}
			if after := readFileString(t, r.cfg); after != before {
				t.Errorf("a refused --claude wrote the local config:\n%s", after)
			}
		})
	}
}

// TestAccountsAddClaudeRefusedInEveryAgentSession is the owner's ruling of
// 2026-11-01 (the A0 review's finding 1, option (b)): a coding agent's session
// never runs Anthropic's login, --yes included (D868). Four things are
// asserted for each marker: the refusal is the login's own (a refusal from the
// settings write, which also stops the session, would not name the login, so a
// fence placed only on the write fails here), it names the marker, nothing is
// written, and the fake login's marker file is absent, so the login never ran.
//
// The markers are the five a coding agent's own processes set: the sixth,
// CLAUDE_CODE_SSE_PORT, is the IDE extension's, and alone, at the real
// terminal this test runs the command on, it is a person's terminal rather
// than an agent's session (the ruling of 2026-11-05, and
// internal/agentenv's own test of the classification).
func TestAccountsAddClaudeRefusedInEveryAgentSession(t *testing.T) {
	for _, marker := range agentenv.AgentMarkers {
		t.Run(marker, func(t *testing.T) {
			path := accountsAddFixture(t, accountsAddLocal)
			unsetClaudeLoginCredentials(t)
			before := readFileString(t, path)
			loginFindable(t)
			login := loginStubOnPath(t, loginPrint(), 0)
			t.Setenv(marker, "1")
			tty := newTTY(t)

			done := runOnTerminal(t, tty, "accounts", "add", "team-a", "--repos", "acme/app", "--claude", "--yes")
			err := waitCommand(t, done)
			if err == nil {
				t.Fatalf("--claude ran in a coding agent's session:\n%s", tty.displayed())
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			if !strings.Contains(err.Error(), "refusing to run Anthropic's login") {
				t.Errorf("the refusal is not the login's own, so the dedicated fence is unpinned: %v", err)
			}
			if !strings.Contains(err.Error(), marker) {
				t.Errorf("the refusal does not name %s, so nobody knows which variable to unset: %v", marker, err)
			}
			if _, statErr := os.Stat(login.marker); statErr == nil {
				t.Error("the login ran: a coding agent's session is refused before anything starts")
			}
			if after := readFileString(t, path); after != before {
				t.Errorf("the file changed in a coding agent's session:\n%s", after)
			}
		})
	}
}
