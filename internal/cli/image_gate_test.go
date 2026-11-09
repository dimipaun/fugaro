package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// executeBuild is executeStdin with an answer on stdin that nothing reads: a
// cloud image build asks no question, in any session (T4), and the tests of
// everything else about the build go through this so that a command which
// started asking again would find the answer it used to want.
func executeBuild(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	fakeTerminal(t)
	return executeStdin(t, "aurora\n", args...)
}

// A Cloud Build is billable and asks nothing: no session is refused for being
// unable to answer, so a person at their own terminal, a pipe and a coding
// agent's session all submit the build — and what bounds the billable build is
// the lease each submission takes first (image_build_lease.go), not a
// confirmation. The lease's own refusals are TestImageBuildOverCapFailsWithThe-
// SpecError's and image_cost_test.go's.
//
// The mutation this catches is the prompt coming back for a session the manager
// gate does not cover: the sentence the typed confirmation printed is nowhere
// for any of these rows, and every one of them submits its build.
func TestImageBuildAsksNothing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
		marker   string
	}{
		{"a person at their own terminal", true, ""},
		{"a pipe", false, ""},
		{"a coding agent at a terminal", true, "CLAUDECODE"},
		// The Claude Code IDE extension sets its marker in a person's own
		// terminal too, so that marker alone, at a terminal, is a person's
		// (the ruling of 2026-11-05); with no terminal it is an agent's.
		{"an IDE terminal", true, "CLAUDE_CODE_SSE_PORT"},
		{"an IDE terminal, a pipe", false, "CLAUDE_CODE_SSE_PORT"},
		{"a coding agent, a pipe", false, "CURSOR_AGENT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb, _ := cloudBuildCheckout(t, false)
			if tc.terminal {
				fakeTerminal(t)
			}
			if tc.marker != "" {
				t.Setenv(tc.marker, "1")
			}
			out, errOut, err := executeStdin(t, "aurora\n", "image", "build", "--base", "b:1")
			if err != nil || buildPosts(fb) != 1 {
				t.Fatalf("err %v, %d builds\n%s%s", err, buildPosts(fb), out, errOut)
			}
			for _, asked := range []string{"Type ", "⚠ CONFIRM", "[y/N]", "not confirmed", "real terminal", ideNotice} {
				if strings.Contains(out, asked) || strings.Contains(errOut, asked) {
					t.Errorf("the build asked %q, which no session is refused for answering:\n%s%s", asked, out, errOut)
				}
			}
			// And it said what it does before it did it: the line that now
			// stands where the prompt stood.
			if !strings.Contains(errOut, "submits a Cloud Build for") {
				t.Errorf("the build printed no line of its own:\n%s", errOut)
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

// --json is the local build's report, and a Cloud Build reports itself as
// text: the two cannot be one output, so the pairing is refused — before the
// local config or anything in the cloud is read. It is the one refusal left in
// this command, and it is about the shape of the output rather than about a
// question nobody can answer.
func TestImageBuildJSONRefusalPrecedesTheCloud(t *testing.T) {
	t.Setenv("FUGARO_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	_, _, err := execute(t, "image", "build", "--json")
	if err == nil || !strings.Contains(err.Error(), "--json") || !strings.Contains(err.Error(), "--local") {
		t.Fatalf("err = %v", err)
	}
}

// A build the caps have no room for fails with the budget's own error before
// Cloud Build is handed anything, and it is the ONLY thing that stops a
// submission now that the prompt is gone (T4, D1543). The refusal is the
// estimator's — what the build reserves at most, the cap that refused it, what
// the day already counts and the one command that raises it — and it exits 1,
// because an operator can lift it. Nothing is submitted and nothing is counted
// for the build that never ran.
//
// Mutation (run, restore): take the lease after the submission instead of
// before it — or drop the lease and submit — and this test fails: the fake's
// request list holds the build the cap refused.
func TestImageBuildOverCapFailsWithTheSpecError(t *testing.T) {
	fb, f := cloudBuildCheckout(t, false)
	// Enforce, and a repository cap far below what one build reserves: the
	// world buildBudgetWorld makes is observe (the database's own absent node,
	// D1548), where the same build would be recorded and submitted.
	f.db.Set(budget.PathMode, budget.ModeEnforce)
	slug := mustSlug("bitbucket", "acme/app")
	f.db.Set(budget.PathCapsRepo(slug), map[string]any{"dailyMicros": usd1 / 2})
	out, errOut, err := executeBuild(t, "image", "build", "--base", "b:1")
	if err == nil {
		t.Fatalf("a build over the repository's daily cap was submitted\n%s%s", out, errOut)
	}
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v: the operator can raise the cap", ExitCode(err), err)
	}
	for _, want := range []string{
		"$0.94",                       // what the build reserves at the most
		"$0.50",                       // the cap that refused it
		"fugaro budget set --repo",    // the one command that raises it
		"jobs and its settled builds", // what the day already counts
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q:\n%v", want, err)
		}
	}
	if buildPosts(fb) != 0 {
		t.Errorf("submitted %d builds under a cap the first one met", buildPosts(fb))
	}
}
