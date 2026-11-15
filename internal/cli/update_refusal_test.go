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
// The failure is a refusal of the same kind, and that is what a pipe gets now
// (T11, docs/no-prompt-policy): a step's confirmation needs a real terminal, so
// the step that would have applied — and could have failed the way the apply of
// a plan with a change fails — comes back unconfirmed first. A terminal that is
// there takes the confirmation and the apply's own failure is the run's
// (report_failure_line_test.go).
func TestARefusalAfterAFailureStillSaysHowToUnblockIt(t *testing.T) {
	r := updateRig(t)
	r.setPlan(t, change("module.installation.google_storage_bucket.runs", "create"))
	r.script["plan"] = map[string]any{"exit": 2, "stdout": convergeNoise + "\n"}
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
	// account's own row for the stage that failed, "the confirmation of this step
	// needs a real terminal", while the refusal the run returns names the stage
	// the loop reached after it ("typing the confirmation for plugin"): the
	// failure is in the log and the refusal is not, which is why main's own line
	// is the only place the refusal is said.
	if !strings.Contains(newestLog(t, "update"), "needs a real terminal") {
		t.Errorf("the run's log lost the failure it printed:\n%s", newestLog(t, "update"))
	}
}
