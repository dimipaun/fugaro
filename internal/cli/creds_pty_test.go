//go:build darwin || linux

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// The App key is refused at a terminal BEFORE anything is read: the hidden
// prompt takes one line, and a prompt that took the -----BEGIN line and then
// failed would have had the operator paste a private key into a terminal for
// nothing. The refusal is the same sentence `fugaro secrets set` makes for its
// one multi-line secret (readSecret, secrets.go), with this command's name,
// and it names the way in: stdin, from the key file.
//
// The pty is real, and nothing is ever written to it — the point is that the
// command answers without waiting for a line, which is why this test has no
// timeout of its own: a prompt that read would hang here.
func TestCredsSetRefusesTheAppKeyAtATerminal(t *testing.T) {
	f := credsFixtureFor(t)
	f.seed(t, nil)
	tty := newTTY(t)
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(tty.tty)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"creds", "set", config.GitHubAppKeySecret})
	err := cmd.Execute()
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("creds set %s at a terminal = %v, want a user error", config.GitHubAppKeySecret, err)
	}
	for _, want := range []string{"hidden prompt can't take", "< PATH-TO-THE-KEY-FILE", config.GitHubAppKeySecret} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal is %q, want it to say %q", err, want)
		}
	}
	if n := f.writes(); n != 0 {
		t.Fatalf("%d version(s) written at a terminal", n)
	}
	// The value was never read, so nothing of a key can be in the answer: what
	// the terminal displayed is the prompt and not the input, and no key was
	// ever typed for it to echo.
	if shown := tty.displayed(); strings.Contains(shown, "PRIVATE KEY") {
		t.Errorf("the terminal displayed a key: %q", shown)
	}
}

// The same terminal, for a model credential, still gets the hidden prompt: the
// refusal above is about the App key's shape and not about `creds set` at a
// terminal, and the one-line path is unchanged. A value pasted at the prompt
// is stored and echoed by nothing.
func TestCredsSetPromptsForAModelCredentialAtATerminal(t *testing.T) {
	f := credsFixtureFor(t)
	f.seed(t, nil)
	value := credsValue(t, "prompted")
	tty := newTTY(t)
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetIn(tty.tty)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"creds", "set", config.ClaudeOAuthToken})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	// The hidden prompt has no echo, so the value goes in and the terminal
	// shows nothing of it. waitEcho is what says the prompt is up (readHidden
	// turns echo off before it reads).
	tty.waitEcho(t, false)
	if _, err := tty.master.WriteString(value + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("creds set at a terminal = %v (stderr %q)", err, errOut.String())
	}
	if n := f.writes(); n != 1 {
		t.Fatalf("%d version(s) written, want 1", n)
	}
	if shown := tty.displayed(); strings.Contains(shown, value) {
		t.Fatalf("the terminal echoed the value: %q", shown)
	}
	if strings.Contains(out.String()+errOut.String(), value) {
		t.Fatalf("a value leaked: %q", out.String()+errOut.String())
	}
}
