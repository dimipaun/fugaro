package cli

import (
	"strings"
	"testing"
)

// executeBuild is executeStdin with the project's name on stdin and a terminal
// under it. The name is left over from the typed confirmation this command used
// to require; the tests below keep it so that a confirmation coming back is a
// failure rather than a silently satisfied read.
func executeBuild(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	fakeTerminal(t)
	return executeStdin(t, "aurora\n", args...)
}

// T4's own claim, over every session at once: a Cloud Build is billable, and
// this command asks nothing about it — no name typed, no y/N — in a person's
// terminal, a pipe, a coding agent's session and a --json run alike, and every
// one of them reaches Cloud Build and submits its build.
//
// "Nothing was asked" is a claim about an ABSENCE, so it is said three ways: the
// reader each session's own stdin is handed to is never reached (unreadStdin
// fails the test when anything reads it), the sentence the removed prompt
// printed is nowhere in what the session read, and the build was submitted
// anyway — a command that asked and a command that did not both submit one
// build in a test that only watched the fake's request count.
func TestImageBuildAsksNothing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		terminal bool
		marker   string
	}{
		{"a terminal", nil, true, ""},
		{"a pipe", nil, false, ""},
		{"--json at a terminal", []string{"--json"}, true, ""},
		{"a coding agent at a terminal", nil, true, "CLAUDECODE"},
		// The Claude Code IDE extension sets its marker in a person's own
		// terminal too, so that marker alone, at a terminal, is a person's
		// (the ruling of 2026-11-05); with no terminal it is an agent's.
		{"an IDE terminal", nil, true, "CLAUDE_CODE_SSE_PORT"},
		{"an IDE terminal, a pipe", nil, false, "CLAUDE_CODE_SSE_PORT"},
		{"a coding agent, a pipe", nil, false, "CURSOR_AGENT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb, _ := cloudBuildCheckout(t, false)
			if tc.terminal {
				fakeTerminal(t)
			}
			if tc.marker != "" {
				t.Setenv(tc.marker, "1")
			}
			stdin := &unreadStdin{}
			args := append([]string{"image", "build", "--base", "b:1"}, tc.args...)
			out, errOut, err := executeStdinReader(t, stdin, args...)
			if err != nil {
				t.Fatalf("err %v\n%s%s", err, out, errOut)
			}
			if stdin.read {
				t.Errorf("the session was read, so a question was asked:\n%s", errOut)
			}
			if strings.Contains(errOut, "Type "+initProjectName+" to submit the build: ") || strings.Contains(errOut, "[y/N]: ") {
				t.Errorf("the removed confirmation was printed:\n%s", errOut)
			}
			if buildPosts(fb) != 1 {
				t.Fatalf("submitted %d builds, want one\n%s%s", buildPosts(fb), out, errOut)
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

// The check job runs in Cloud Run, where CLOUD_RUN_JOB is set and no coding
// agent's marker is: anywhere else it submits nothing. --dry-run submits
// nothing either, so it is always allowed.
func TestImageCheckJobRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, job, agent string
		args             []string
		refused          string
	}{
		{"an agent session, in a job", "j", "CLAUDECODE", []string{"--force"}, "CLAUDECODE is set"},
		{"no job marker", "", "", []string{"--force"}, "Cloud Run job"},
		{"no job marker, an agent", "", "CLAUDECODE", nil, "CLAUDECODE is set"},
		{"--dry-run, an agent, no marker", "", "CLAUDECODE", []string{"--dry-run"}, ""},
		{"--dry-run, no marker", "", "", []string{"--dry-run"}, ""},
		{"the marker set", "j", "", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range append([]string{"CLOUD_RUN_JOB", "CLOUD_RUN_EXECUTION"}, agentMarkers...) {
				t.Setenv(k, "")
			}
			if tc.job != "" {
				t.Setenv("CLOUD_RUN_JOB", tc.job)
			}
			if tc.agent != "" {
				t.Setenv(tc.agent, "1")
			}
			_, _, err := execute(t, append([]string{"image", "check", "--job"}, tc.args...)...)
			if tc.refused != "" {
				if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.refused) {
					t.Fatalf("exit %d, err %v", ExitCode(err), err)
				}
				return
			}
			// Past the gate: the job's own spec is what is missing here.
			if err == nil || !strings.Contains(err.Error(), "is not set") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
