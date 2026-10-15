package projectpolicy

import (
	"strings"
	"testing"
)

// TestSkewMessagesShareTheSentence: the runner's refusal (task 1.3) and the
// CLI's two (task 1.6, one per direction) print the same sentence and only
// differ in the remedy, so the constants must stay in step.
func TestSkewMessagesShareTheSentence(t *testing.T) {
	for name, msg := range map[string]string{
		"RunnerSkew": RunnerSkew, "CLISkew": CLISkew, "CLISkewNewer": CLISkewNewer,
	} {
		if len(msg) <= len(Skew) || msg[:len(Skew)] != Skew {
			t.Errorf("%s does not start with Skew: %q", name, msg)
		}
	}
	if Version != 1 {
		t.Errorf("Version = %d: a version bump needs the plan's skew handling, this test and the CLI's", Version)
	}
}

// TestTheCLISkewRemediesStayApart: the two CLI directions carry different
// advice because they have different fixes, and each one is wrong for the other
// direction — `fugaro upgrade` when this binary is already the newer side does
// nothing, and `fugaro init --firebase` at an older binary redeploys this
// release's rules template over rules a newer Fugaro wrote. internal/cli picks
// between them by Found > Understood (cliSkewSentence), which is why the pair
// is pinned here beside the sentence they share.
func TestTheCLISkewRemediesStayApart(t *testing.T) {
	if !strings.Contains(CLISkew, "upgrade Fugaro") {
		t.Errorf("CLISkew = %q, want the upgrade remedy for the older binary", CLISkew)
	}
	if !strings.Contains(CLISkewNewer, "re-run") || !strings.Contains(CLISkewNewer, "init --firebase") {
		t.Errorf("CLISkewNewer = %q, want the re-run init remedy for the older node", CLISkewNewer)
	}
	for name, msg := range map[string]string{"CLISkew": CLISkew, "CLISkewNewer": CLISkewNewer} {
		if strings.Contains(msg, "%!") || strings.Count(msg, "%") != 2 {
			t.Errorf("%s = %q, want exactly the two version placeholders Skew has", name, msg)
		}
	}
	if strings.Contains(CLISkewNewer, "upgrade Fugaro") {
		t.Errorf("CLISkewNewer = %q: the node is the older side here, so an upgrade is not the remedy", CLISkewNewer)
	}
	if strings.Contains(CLISkew, "init") {
		t.Errorf("CLISkew = %q: this binary is the older side here, and this release's rules template must not be redeployed over a newer Fugaro's", CLISkew)
	}
}
