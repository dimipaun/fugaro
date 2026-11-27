package cli

// update_refusal_test.go is one thing: a run that failed a stage and then stops
// at a stage only a real terminal can confirm returns the refusal, and the
// refusal is the only place the run says how to get past it. It is not an error
// the account printed, so main's line has to carry it (the owner's feedback of
// 2026-11-13; the review of the run that routed the account off the screen).

import (
	"strings"
	"testing"
)

// TestARefusalAfterAFailureStillSaysHowToUnblockIt: the installation stage
// fails, the run goes on to the stage behind it that no terminal here can
// confirm, and the run returns that refusal — the failure is what the account
// printed, the refusal is not, so the refusal has to reach the screen under
// main's own line. Marked as read, it would reach the screen, the log and the
// error text nowhere, and the person would be left with a command and no way to
// unblock it.
//
// The failure is the apply of a plan with a change, which a managed project's gate
// covers for a session nobody is at (#4170); a project whose steps need a
// confirmation gets the refusal first instead (T11, docs/no-prompt-policy), and a
// terminal that is there takes the confirmation and the apply's own failure is
// the run's (report_failure_line_test.go).
func TestARefusalAfterAFailureStillSaysHowToUnblockIt(t *testing.T) {
	// The refusal this test is about is the plugin stage's own typed
	// confirmation, so the machine's installed plugin has to be this test's and
	// not the host's: a machine whose plugin is newer than this release reads the
	// stage as nothing to do, the confirmation is never asked, and the run stops
	// somewhere else with an error the account did print.
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	r := updateRig(t)
	r.setPlan(t, change("module.installation.google_storage_bucket.runs", "create"))
	r.script["plan"] = map[string]any{"exit": 2, "stdout": convergeNoise + "\n"}
	// A managed project's gate covers the apply for a session nobody is at (#4170), so the failure this test puts first is the apply's own.
	r.script["apply"] = map[string]any{"exit": 1, "stderr": "Error: the apply did not land\n"}
	r.save(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("the step no terminal here can confirm went ahead, and the run succeeded:\n%s%s", out, errOut)
	}
	if !strings.Contains(err.Error(), "needs a real terminal") {
		t.Fatalf("the run did not stop at a stage no terminal can confirm: %v", err)
	}
	if ShownOnScreen(err) {
		t.Errorf("the refusal is marked as printed, so main's line drops it:\n%v", err)
	}
	line := topLevelLine(err)
	if !strings.Contains(line, "needs a real terminal") || !strings.Contains(line, "run it in your own terminal window") {
		t.Errorf("the top-level line does not carry the refusal's advice on how to unblock the run: %q", line)
	}
	// What the log holds — it is what `fugaro troubleshoot` opens — is the
	// account's own row for the stage that failed, the apply's "the apply did not land",
	// while the refusal the run returns names the stage the loop reached after it
	// ("typing the confirmation for plugin"): the failure is in the log and the
	// refusal is not, which is why main's own line is the only place the refusal
	// is said.
	if !strings.Contains(newestLog(t, "update"), "the apply did not land") {
		t.Errorf("the run's log lost the failure it printed:\n%s", newestLog(t, "update"))
	}
}
