package runner

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
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
		"at most one push a minute", "60 pushes an hour", "every stage end", "post-commit hook",
		"fast-forward", "git.pr.checkpoints: false", "not verified", "resources.memory",
		"fugaro image refresh", "early_draft: false", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS", "for example signal 7",
	} {
		if !strings.Contains(gp, want) {
			t.Errorf("docs/git-providers.md never says %q", want)
		}
	}
	for _, f := range []string{
		"../../docs/git-providers.md", "../../docs/gcp-setup.md", "../../docs/design/v1.md", "../../README.md",
		"../../plugin/skills/working/SKILL.md", "../../plugin/skills/working/reference/launch.md",
		"../../plugin/skills/working/reference/diagnose.md", "../config/config.go",
		"../config/example.yaml", "prflow.go", "../../docs/gcp-live-checklist.md", "../../schemas/fugaro.schema.json",
	} {
		if strings.Contains(read(f), "first verified push") {
			t.Errorf("%s still says the draft opens at the first verified push", f)
		}
	}
	if !strings.Contains(read("../../docs/design/v1.md"), "checkpoint-pushes.md") {
		t.Error("design v1 §4.2a does not link the checkpoint design")
	}
}

// TestCheckpointKeyIsPinned: the documented key spelling is the one the
// config parses, and the schema declares it.
func TestCheckpointKeyIsPinned(t *testing.T) {
	f, ok := reflect.TypeOf(config.PRSettings{}).FieldByName("Checkpoints")
	if !ok {
		t.Fatal("config.PRSettings has no Checkpoints field")
	}
	if tag := f.Tag.Get("yaml"); tag != "checkpoints" {
		t.Fatalf("the yaml tag is %q, but the docs, example.yaml and the schema say git.pr.checkpoints", tag)
	}
	for _, p := range []string{"../../docs/git-providers.md", "../config/example.yaml", "../../schemas/fugaro.schema.json"} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "checkpoints") {
			t.Errorf("%s never names the checkpoints key", p)
		}
	}
	// The real template with the key set, through the real parser.
	tmpl := string(config.Example)
	if !strings.Contains(tmpl, "    # checkpoints: true") {
		t.Fatal("example.yaml no longer shows the commented git.pr.checkpoints line")
	}
	off, probs := config.Parse([]byte(strings.Replace(tmpl, "    # checkpoints: true", "    checkpoints: false #", 1)))
	if len(probs) > 0 || off == nil {
		t.Fatalf("the parser rejects git.pr.checkpoints: %v", probs)
	}
	if off.Git.PR.CheckpointsOn() {
		t.Error("git.pr.checkpoints: false did not turn checkpoints off")
	}
	on, probs := config.Parse([]byte(tmpl))
	if len(probs) > 0 || on == nil || !on.Git.PR.CheckpointsOn() {
		t.Errorf("checkpoints are not on by default: %v", probs)
	}
}
