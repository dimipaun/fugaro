package projectpolicy

import "testing"

// TestSkewMessagesShareTheSentence: the runner's refusal (task 1.3) and the
// CLI's (task 1.6) print the same sentence and only differ in the remedy, so
// the constants must stay in step.
func TestSkewMessagesShareTheSentence(t *testing.T) {
	for name, msg := range map[string]string{"RunnerSkew": RunnerSkew, "CLISkew": CLISkew} {
		if len(msg) <= len(Skew) || msg[:len(Skew)] != Skew {
			t.Errorf("%s does not start with Skew: %q", name, msg)
		}
	}
	if Version != 1 {
		t.Errorf("Version = %d: a version bump needs the plan's skew handling, this test and the CLI's", Version)
	}
}
