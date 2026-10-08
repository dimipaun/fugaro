package runner

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestCheckpointDocsMatchTheCode: the docs state the timings the code uses
// and the cost model, name the opt-out, and no longer say the draft waits
// for a verified push.
func TestCheckpointDocsMatchTheCode(t *testing.T) {
	read := func(p string) string {
		t.Helper()
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if checkpointMinGap != time.Minute {
		t.Fatal("the docs say at most one push a minute (60 an hour); update them with checkpointMinGap")
	}
	gp := read("../../docs/git-providers.md")
	for _, want := range []string{
		"## Checkpoint pushes (`git.pr.checkpoints`)",
		fmt.Sprintf("every %d seconds", int(checkpointPoll/time.Second)),
		fmt.Sprintf("unchanged for %d seconds", int(checkpointQuiet/time.Second)),
		fmt.Sprintf("%d minutes", int(checkpointFallback/time.Minute)),
		"at most one push a minute", "60 pushes an hour", "every stage boundary", "post-commit hook",
		"fast-forward", "git.pr.checkpoints: false", "not verified", "resources.memory",
	} {
		if !strings.Contains(gp, want) {
			t.Errorf("docs/git-providers.md never says %q", want)
		}
	}
	for _, f := range []string{
		"../../docs/git-providers.md", "../../docs/gcp-setup.md", "../../docs/design/v1.md", "../../README.md",
		"../../plugin/skills/working/SKILL.md", "../../plugin/skills/working/reference/launch.md",
		"../../plugin/skills/working/reference/diagnose.md",
	} {
		if strings.Contains(read(f), "first verified push") {
			t.Errorf("%s still says the draft opens at the first verified push", f)
		}
	}
	if !strings.Contains(read("../../docs/design/v1.md"), "checkpoint-pushes.md") {
		t.Error("design v1 §4.2a does not link the checkpoint design")
	}
}
