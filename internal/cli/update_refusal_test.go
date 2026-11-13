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

// TestARefusalAfterAFailureStillSaysHowToUnblockIt: the apply fails, the next
// stage wants a real terminal, and the run returns the refusal — the failure is
// what the account printed, the refusal is not, so the refusal has to reach the
// screen under main's own line. Marked as read, it would reach the screen, the
// log and the error text nowhere, and the person would be left with a command
// and no way to unblock it.
func TestARefusalAfterAFailureStillSaysHowToUnblockIt(t *testing.T) {
	r := applyingRig(t)
	r.script["apply"] = map[string]any{"exit": 1, "stderr": "Error: quota exceeded\n"}
	r.save(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("the apply failed, yet the run succeeded:\n%s%s", out, errOut)
	}
	if !strings.Contains(err.Error(), "needs a real terminal") {
		t.Fatalf("the run did not stop at a stage no terminal can confirm: %v", err)
	}
	if ShownOnScreen(err) {
		t.Errorf("the refusal is marked as printed, so main's line drops it:\n%v", err)
	}
	line := topLevelLine(err)
	if !strings.Contains(line, "needs a real terminal") || !strings.Contains(line, "--yes") {
		t.Errorf("the top-level line does not carry the refusal's advice on how to unblock the run: %q", line)
	}
	if !strings.Contains(newestLog(t, "update"), "needs a real terminal") {
		t.Errorf("the run's log lost the refusal:\n%s", newestLog(t, "update"))
	}
}
