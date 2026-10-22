//go:build darwin || linux

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
)

// TestCredsSetReadsHiddenPrompt: at a real terminal, `fugaro creds set` reads
// the value the way `fugaro secrets set` does — a hidden prompt, no echo —
// and never from an argument, a flag or the environment. The terminal is a
// pseudo-terminal (secrets_pty_test.go's newTTY), so this is the one path
// TestCredsSetValueNeverFromArgvOrEnv cannot reach: a value pasted at the
// prompt.
//
// The command runs in its own goroutine because it blocks in the read until
// the line arrives; the pty's ECHO flag is what tells the test the prompt has
// been reached, and the result comes back on a channel (which is also what
// makes the buffers below safe to read).
func TestCredsSetReadsHiddenPrompt(t *testing.T) {
	tty := newTTY(t)
	if !tty.echo(t) {
		t.Fatal("a fresh pty has ECHO off")
	}
	sm := credsFixtureFor(t)
	sm.seed(t, nil)
	value := credsValue(t, "prompt")

	type result struct {
		out, errOut string
		err         error
	}
	done := make(chan result, 1)
	go func() {
		cmd := NewRootCmd()
		var out, errOut bytes.Buffer
		cmd.SetIn(tty.tty)
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs(NormalizeRepoArgs([]string{"creds", "set", config.ClaudeOAuthToken}))
		err := cmd.Execute()
		done <- result{out.String(), errOut.String(), err}
	}()

	tty.waitEcho(t, false) // off while the value is read
	if _, err := tty.master.WriteString(value + "\n"); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("%v (stderr %q)", r.err, r.errOut)
	}
	tty.waitEcho(t, true) // and back on afterwards

	if got := sm.value(t); !strings.Contains(got, config.ClaudeOAuthToken+": "+value) {
		t.Fatalf("the bundle is %q, want the value just typed", got)
	}
	if !strings.Contains(r.errOut, "Paste the value for "+config.ClaudeOAuthToken+" (input hidden)") {
		t.Errorf("the prompt was not printed: %q", r.errOut)
	}
	if strings.Contains(r.errOut, "trailing newline") {
		t.Errorf("the hidden prompt's read keeps its own newline handling: %q", r.errOut)
	}
	if !strings.Contains(r.out, "version 1") {
		t.Errorf("the write was not reported: %q", r.out)
	}
	assertNoValue(t, r.out+r.errOut, value)

	// What the terminal itself displayed: the value was typed into it, so an
	// echo that leaked would be here and nowhere else.
	if got := tty.displayed(); strings.Contains(got, value) {
		t.Errorf("the terminal echoed the value: %q", got)
	}
	// The command reads the bundle document and nothing else, and it is the
	// only secret in this fixture: no other value was read, and none was
	// written anywhere but the one container.
	if got := sm.Accessed(); len(got) != 0 && !slicesAll(got, credsvc.BundleSecretID("aurora")) {
		t.Errorf("values were read from %q", got)
	}
}

// slicesAll reports whether every element of got is want. It is here rather
// than slices.Equal because the number of reads is not what this asserts: it
// is that the only container read is the bundle.
func slicesAll(got []string, want string) bool {
	for _, g := range got {
		if g != want {
			return false
		}
	}
	return true
}
