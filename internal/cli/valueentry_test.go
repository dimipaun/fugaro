package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// valueentry_test.go: readValue is the one door a value only a person can
// supply comes through (D1540 of docs/no-prompt-policy). What this file pins is
// the half of the policy that turns a prompt into a refusal: with no terminal
// the read is never attempted and the refusal names the flag or the environment
// variable that supplies the value, because a read that IS attempted with no
// terminal is exactly the hang the policy removes.
//
// The terminal path is here too, small: the wizard's names, the billing
// account, the pairing code and the hidden secrets all come through this one
// helper (plan T5, T7), and a read that did not answer what was typed at the
// prompt would otherwise be found late. The prompt is asserted on stderr: a
// value's prompt is not the command's output, and a --json caller reads stdout.

// testStreams is a bare command with the test's own input and one locked buffer
// per output stream, so a row can say which stream a prompt went to and where
// the command's own output ended up. The buffers are locked because the calls
// below are made from a goroutine wherever a read must not happen: what is
// asserted has to be safe to read when the call did not return.
type testStreams struct {
	cmd         *cobra.Command
	out, errOut *syncBuf
}

func newTestStreams(in io.Reader) testStreams {
	cmd := &cobra.Command{Use: "test"}
	s := testStreams{cmd: cmd, out: &syncBuf{}, errOut: &syncBuf{}}
	cmd.SetIn(in)
	cmd.SetOut(s.out)
	cmd.SetErr(s.errOut)
	return s
}

// TestReadValueFailsAtOnceWithNoTerminal is D1540's "never hangs": a value with
// no terminal to type it at is refused before anything is read, and the refusal
// names what supplies it instead — the flag, the variable, or both — so the
// sentence can be acted on. A value with neither source says only that it needs
// a terminal, and a value with one source never names a source it does not
// have, because a refusal naming a flag that does not exist sends the reader
// after something that is not there.
func TestReadValueFailsAtOnceWithNoTerminal(t *testing.T) {
	terminal(t, false)
	specs := []struct {
		name  string
		spec  valueSpec
		wants []string
		never []string
	}{
		{
			"a flag and a variable",
			valueSpec{what: "the billing account ID", flag: "--billing-account", env: "FUGARO_BILLING_ACCOUNT"},
			[]string{"the billing account ID", "needs a real terminal", "--billing-account", "FUGARO_BILLING_ACCOUNT"},
			nil,
		},
		{
			"a flag only",
			valueSpec{what: "the pairing code", flag: "--code"},
			[]string{"the pairing code", "needs a real terminal", "--code"},
			[]string{"FUGARO_"},
		},
		{
			"a variable only",
			valueSpec{what: "your API token", env: "FUGARO_TOKEN"},
			[]string{"your API token", "needs a real terminal", "FUGARO_TOKEN"},
			[]string{"pass --"},
		},
		{
			"neither",
			valueSpec{what: "the project's name"},
			[]string{"the project's name", "needs a real terminal"},
			[]string{"pass --", "set "},
		},
	}
	for _, row := range specs {
		t.Run(row.name, func(t *testing.T) {
			in := &unreadStdin{}
			s := newTestStreams(in)
			got, err := readValue(s.cmd, row.spec)
			if in.read {
				t.Error("the value was read from stdin with no terminal to read it at")
			}
			if err == nil {
				t.Fatalf("readValue read %q with no terminal", got)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("exit %d, want a user error: %v", ExitCode(err), err)
			}
			for _, want := range row.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			for _, never := range row.never {
				if strings.Contains(err.Error(), never) {
					t.Errorf("the refusal names %q, which this value has no source for: %v", never, err)
				}
			}
		})
	}
}

// TestReadValueReadsAtATerminal is the other half: what was typed is what the
// caller gets, one line and no newline, and the prompt names the value it waits
// for on stderr, leaving stdout to the command's own output. A hidden value is
// the same read with the terminal's echo off (readHiddenLine), which the
// callers that hold secrets reach and this test does not.
func TestReadValueReadsAtATerminal(t *testing.T) {
	terminal(t, true)
	s := newTestStreams(strings.NewReader("  acct-1  \n"))
	got, err := readValue(s.cmd, valueSpec{what: "the billing account ID", flag: "--billing-account"})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "acct-1" {
		t.Errorf("readValue = %q, want the line that was typed at the prompt", got)
	}
	if !strings.Contains(s.errOut.String(), "the billing account ID") {
		t.Errorf("the prompt does not name the value it waits for: %q", s.errOut.String())
	}
	if s.out.String() != "" {
		t.Errorf("the prompt was written to the command's own output: %q", s.out.String())
	}
}
