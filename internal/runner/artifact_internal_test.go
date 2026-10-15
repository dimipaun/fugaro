package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestRenderRecordDropsABlankCommand pins artifact.go's rule that the one
// record runRender can return for a render that never ran is recorded as no
// render at all. Record.Render documents a blank command as none
// (runstore.go) and schemas/result.schema.json says the same, while
// runRender's blank-command arm returns a record carrying exactly that: a
// blank Command, the sha256 of the empty string, exit code -1. Stored, it
// would claim a render that never happened.
//
// The arm itself is unreachable through an ordinary launch: bootstrap's
// checkGateCommands refuses a render step without commands.render first
// (TestBlankRenderCommandRefusesTheRun), so this is the fail-closed path
// behind it and the rule is pinned here rather than through a run.
//
// Mutation (return &rec unconditionally): the blank case is recorded and
// this test fails.
func TestRenderRecordDropsABlankCommand(t *testing.T) {
	// What runRender builds before it looks at the command: the state a
	// blank render leaves the record in.
	blank := runstore.RenderRecord{Command: "", CommandSHA256: sha256Hex(""), Tree: renderTree, ExitCode: -1, StartedAt: "2026-10-15T10:00:00Z"}
	for _, command := range []string{"", " ", "\t\n"} {
		if got := renderRecordFor(command, blank); got != nil {
			t.Fatalf("command %q is recorded as %+v, want no render record", command, got)
		}
	}
	ran := runstore.RenderRecord{Command: "sh render.sh", CommandSHA256: sha256Hex("sh render.sh"), Tree: renderTree, ExitCode: 0}
	got := renderRecordFor("sh render.sh", ran)
	if got == nil || *got != ran {
		t.Fatalf("render record = %+v, want the record runRender returned (%+v)", got, ran)
	}
}
