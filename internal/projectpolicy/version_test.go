package projectpolicy

import (
	"errors"
	"testing"
)

// TestSkewMessagesShareTheSentence: the runner's refusal (task 1.3) and the
// CLI's (task 1.6) print the same sentence and only differ in the remedy, so
// the constants must stay in step.
func TestSkewMessagesShareTheSentence(t *testing.T) {
	for name, msg := range map[string]string{"RunnerSkew": RunnerSkew, "CLISkew": CLISkew} {
		if len(msg) <= len(Skew) || msg[:len(Skew)] != Skew {
			t.Errorf("%s does not start with Skew: %q", name, msg)
		}
	}
	if Version != 2 {
		t.Errorf("Version = %d: a version bump needs the plan's skew handling, this test and the CLI's", Version)
	}
}

// TestProfileGateIsVersionTwo: version 2 is the first version whose runner
// enforces a profile's allow_repos (plan task 3.3). A runner of version 1
// decodes the profiles block and ignores it, so a launcher that points a run
// of a profile's job at another repository of the project's allow_repos would
// get the profile's secrets from it. The bump is what makes such a runner
// refuse the node instead: a version-1 reader meets version 2 and halts
// (RunnerSkew), and this code refuses a version-1 node the same way rather
// than reading it as one whose profiles were never enforced.
func TestProfileGateIsVersionTwo(t *testing.T) {
	_, _, err := Decode([]byte(`{"version":1,"allow_repos":["github/acme/*"],"profiles":{"standard":{"allow_repos":["github/acme/app"]}}}`))
	var ve *VersionError
	if !errors.As(err, &ve) || ve.Found != 1 || ve.Understood != 2 {
		t.Fatalf("a version-1 node with profiles: err = %v, want a VersionError found 1, understood 2", err)
	}
}
