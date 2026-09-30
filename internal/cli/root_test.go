package cli

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// execute runs the fugaro command tree with args and captures its output.
func execute(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestVersion(t *testing.T) {
	out, _, err := execute(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	if out != "dev\n" {
		t.Fatalf("version output = %q, want %q", out, "dev\n")
	}
}

// The M4 bootstrap's hidden `gcp job-spec` is gone: fugaro init computes
// every name now.
func TestNoGCPCommand(t *testing.T) {
	if _, _, err := execute(t, "gcp", "--help"); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("fugaro gcp: err = %v, want unknown command", err)
	}
}

func TestExitErrorNilErr(t *testing.T) {
	e := &ExitError{Code: ExitRemoteError}
	if got, want := e.Error(), "exit code 2"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestExitCode(t *testing.T) {
	remote := &ExitError{Code: ExitRemoteError, Err: errors.New("boom")}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"plain error", errors.New("x"), ExitUserError},
		{"exit error", remote, ExitRemoteError},
		{"wrapped exit error", fmt.Errorf("wrap: %w", remote), ExitRemoteError},
	}
	for _, tc := range cases {
		if got := ExitCode(tc.err); got != tc.want {
			t.Errorf("%s: ExitCode = %d, want %d", tc.name, got, tc.want)
		}
	}
}
