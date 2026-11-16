package cli

// report_failure_line_test.go is T-DG9: a command that failed under a log
// prints the one line naming the command that explains it. The run ID is the
// stem of that log, so what the failure prints is what `fugaro troubleshoot`
// takes with nobody spelling it (D1609). A command that kept no log prints
// none, which is a failed read command too (T6, G7).

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/cmdlog"
)

// topLevelLine is what main writes for err, captured in a buffer instead of on
// os.Stderr so a test can read it — `execute` and `executeStdin` never run main.
func topLevelLine(err error) string {
	var buf bytes.Buffer
	PrintFailure(&buf, err)
	return buf.String()
}

// failureLine is the sentence a failed logged command prints, up to the run ID
// the line ends with. It is spelled here rather than read out of the reporter:
// a test that took the words from the package would pass with any of them
// wrong, and this is the sentence the spec and the plan pin.
const failureLine = "Something went wrong. Run: fugaro troubleshoot "

// TestFailureLineNamesTheTroubleshootCommand: the failure block names the
// command that explains the run, and it names it by the ID of the log under
// it — the stem of the file `fugaro troubleshoot` then opens — so a person
// copies one word rather than a path, and the ID the line prints is one the
// command takes (D1609).
func TestFailureLineNamesTheTroubleshootCommand(t *testing.T) {
	r := applyingRig(t)
	r.script["apply"] = map[string]any{"exit": 1, "stderr": "Error: quota exceeded\n"}
	r.save(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("the apply failed, yet the run succeeded:\n%s%s", out, errOut)
	}
	path, _ := onlyLog(t, "update")
	id := strings.TrimSuffix(filepath.Base(path), ".log")
	all := out + errOut

	if n := strings.Count(all, failureLine+id); n != 1 {
		t.Fatalf("the failure block prints %q %d time(s), want once:\n%s", failureLine+id, n, all)
	}
	i := strings.Index(all, failureLine)
	printed, _, _ := strings.Cut(all[i+len(failureLine):], "\n")
	printed = strings.TrimSpace(printed)
	if printed != id {
		t.Errorf("the failure prints the run ID %q, want the stem of the log it points at (%q):\n%s", printed, id, all)
	}
	if _, err := cmdlog.ParseID(printed); err != nil {
		t.Errorf("the run ID the failure prints is not one `fugaro troubleshoot` takes: %v", err)
	}
	if !strings.Contains(all, "full output: "+path) {
		t.Errorf("the failure block lost the log's path:\n%s", all)
	}
}

// TestFailureLineIsPrintedOnlyUnderALog: the line names a run ID, and a run ID
// is a log's name, so a failure with no log to point at prints the error and
// its context alone — a converge whose log could not be opened (G5), and a
// read command, which keeps no log by rule (T6, G7). It is the same rule as
// the log path's: a run that has none promises neither.
func TestFailureLineIsPrintedOnlyUnderALog(t *testing.T) {
	t.Run("a failed converge that opened no log", func(t *testing.T) {
		r := applyingRig(t)
		r.script["apply"] = map[string]any{"exit": 1, "stderr": "Error: quota exceeded\n"}
		r.save(t)
		noLogStateDir(t)
		out, errOut, err := executeStdin(t, "", "update")
		if err == nil {
			t.Fatalf("the apply failed, yet the run succeeded:\n%s%s", out, errOut)
		}
		all := out + errOut
		if strings.Contains(all, "Something went wrong") {
			t.Errorf("a run that opened no log named a run ID it has none of:\n%s", all)
		}
		if !strings.Contains(all, "Error: quota exceeded") {
			t.Errorf("the failure block lost the context it has instead of a log:\n%s", all)
		}
	})

	t.Run("a failed command prints its error once", func(t *testing.T) {
		r := applyingRig(t)
		r.script["apply"] = map[string]any{"exit": 1, "stderr": "Error: quota exceeded\n"}
		r.save(t)
		out, errOut, err := executeStdin(t, "", "update")
		if err == nil {
			t.Fatalf("the apply failed, yet the run succeeded:\n%s%s", out, errOut)
		}
		all := out + errOut
		// The step that failed is the failure's own line, with the log's path,
		// the command that explains the run and the fix under it: the error
		// itself is printed there and once. main's top-level line would be the
		// same error a second time, which is what the owner asked to stop
		// seeing (2026-11-13); the exit code and the error the caller gets are
		// unchanged.
		if n := strings.Count(all, "quota exceeded"); n != 1 {
			t.Errorf("the screen carries the failure %d time(s), want once:\n%s", n, all)
		}
		if line := topLevelLine(err); line != "" {
			t.Errorf("main's line would print the error the account already printed: %q", line)
		}
		if !strings.Contains(newestLog(t, "update"), "quota exceeded") {
			t.Errorf("the run's log lost the error the screen shortened:\n%s", newestLog(t, "update"))
		}
	})

	t.Run("a failed plan-only run", func(t *testing.T) {
		r := applyingRig(t)
		r.script["plan"] = map[string]any{"exit": 1, "stderr": "Error: quota exceeded\n"}
		r.save(t)
		out, errOut, err := executeStdin(t, "", "update", "--plan-only")
		if err == nil {
			t.Fatalf("the plan failed, yet the run succeeded:\n%s%s", out, errOut)
		}
		// A run that only planned prints its stages as rows and opens no step, so
		// the screen has no step line, no reason, no log's path and no command
		// that resumes the run for this failure. main's line is the only place it
		// is said (2026-11-14 review).
		if line := topLevelLine(err); !strings.Contains(line, "quota exceeded") {
			t.Errorf("the failed plan-only run printed no top-level line: %q", line)
		}
		if all := out + errOut; strings.Contains(all, "Something went wrong") {
			t.Errorf("a plan-only run printed a failure block for a step it never opened:\n%s", all)
		}
		if !strings.Contains(newestLog(t, "update"), "quota exceeded") {
			t.Errorf("the run's log lost the failure:\n%s", newestLog(t, "update"))
		}
	})

	t.Run("a failure nothing printed keeps the top-level line", func(t *testing.T) {
		if line := topLevelLine(errors.New("boom")); line != "fugaro: boom\n" {
			t.Errorf("a plain error's top-level line is %q, want %q", line, "fugaro: boom\n")
		}
		if line := topLevelLine(nil); line != "" {
			t.Errorf("a successful command printed %q", line)
		}
	})

	t.Run("a failed read command", func(t *testing.T) {
		f := newCloudFixture(t)
		f.writeMarker(t, "borealis", "proj-1234")
		out, errOut, err := execute(t, "runs", "ls")
		if err == nil {
			t.Fatalf("the installation's project does not match the config, yet runs ls succeeded:\n%s%s", out, errOut)
		}
		if all := out + errOut + err.Error(); strings.Contains(all, "Something went wrong") {
			t.Errorf("a read command keeps no log and its failure is printed as it always was:\n%s", all)
		}
	})
}
