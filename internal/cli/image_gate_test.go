package cli

import (
	"strings"
	"testing"
)

// executeBuild is executeStdin at a terminal that types the project's name:
// a cloud image build is billable, so the rule is the typed confirmation, and
// the tests of everything else about the build give it.
func executeBuild(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	fakeTerminal(t)
	return executeStdin(t, "aurora\n", args...)
}

// A Cloud Build is billable: only the project's name typed at a real terminal
// submits it. --json, a pipe and a coding agent never do, with or without a
// marker, and none of them reaches Cloud Build at all.
func TestImageBuildCloudMatrix(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		terminal bool
		marker   string
		stdin    string
		want     bool
	}{
		{"a terminal, name typed", nil, true, "", "aurora\n", true},
		{"a terminal, wrong name", nil, true, "", "yes\n", false},
		{"a terminal, nothing typed", nil, true, "", "", false},
		{"a pipe", nil, false, "", "aurora\n", false},
		{"--json at a terminal", []string{"--json"}, true, "", "aurora\n", false},
		{"a coding agent at a terminal", nil, true, "CLAUDECODE", "aurora\n", false},
		{"an IDE terminal", nil, true, "CLAUDE_CODE_SSE_PORT", "aurora\n", false},
		{"a coding agent, a pipe", nil, false, "CURSOR_AGENT", "aurora\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb, _ := cloudBuildCheckout(t, false)
			if tc.terminal {
				fakeTerminal(t)
			}
			if tc.marker != "" {
				t.Setenv(tc.marker, "1")
			}
			args := append([]string{"image", "build", "--base", "b:1"}, tc.args...)
			out, errOut, err := executeStdin(t, tc.stdin, args...)
			if tc.want {
				if err != nil || buildPosts(fb) != 1 {
					t.Fatalf("err %v, %d builds\n%s%s", err, buildPosts(fb), out, errOut)
				}
				return
			}
			if buildPosts(fb) != 0 {
				t.Fatalf("a build was submitted")
			}
			// Refused before it is asked, nothing at all reaches Cloud Build.
			typedAtTerminal := tc.terminal && tc.marker == "" && !strings.Contains(strings.Join(tc.args, " "), "--json")
			if !typedAtTerminal && len(fb.Requests()) != 0 {
				t.Fatalf("Cloud Build was reached: %d requests", len(fb.Requests()))
			}
			if ExitCode(err) != ExitUserError {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
			msg := err.Error()
			if strings.Contains(msg, "--yes") || strings.Contains(msg, "\n") {
				t.Errorf("the refusal is not one line without --yes: %q", msg)
			}
			if tc.marker != "" && !strings.Contains(msg, tc.marker+" is set") {
				t.Errorf("the refusal does not name the marker: %q", msg)
			}
			if typedAtTerminal && !strings.Contains(msg, "not confirmed") {
				t.Errorf("a wrong name must say it was not confirmed: %q", msg)
			}
			if tc.marker == "" && !typedAtTerminal && !strings.Contains(msg, "real terminal") {
				t.Errorf("no terminal must name the route: %q", msg)
			}
		})
	}
}

// image build --local never reaches Cloud Build and stays as it is.
func TestImageBuildLocalNeedsNoConfirmation(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	_, _, err := execute(t, "image", "build", "--local", "--repo", "acme/app")
	if err == nil || strings.Contains(err.Error(), "coding agent") {
		t.Fatalf("err = %v", err)
	}
}

// The check job runs in Cloud Run with no terminal and submits the daily
// rebuild; the same command in a coding agent's session submits nothing.
func TestImageCheckJobRefusesInAnAgentSession(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	_, _, err := execute(t, "image", "check", "--job", "--force")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "CLAUDECODE is set") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}
