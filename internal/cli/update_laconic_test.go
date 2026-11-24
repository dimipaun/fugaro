package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
)

// maxMessageChars is CLAUDE.md's bound on what a command tells a person: one
// line, what happened and what to do, no decision number and no internal name.
const maxMessageChars = 140

// The owner's report of 2026-11-24 was a `fugaro update` whose notes ran to
// hundreds of characters: the managed project's "this project is managed" lines
// on every step, the gate's paragraph and its IAM call, the roles step's
// sentences, and the run index's own. Each of them is one line now, and this
// pins both halves of that: the length, and that nobody puts a newline back in.
//
// The messages that only a referee reads — a refusal of a plan, the sentence a
// launch gets — are not here: several are pinned by docs tests, and the bound
// is on what an update says while it converges.
func TestUpdateMessagesAreLaconic(t *testing.T) {
	r := &initRun{o: &initOptions{door: "update"}}
	msgs := map[string]string{
		"gate content":            gateContentWarning,
		"gate IAM call":           r.gateNamesAnIAMCall("gs://fugaro-runs-aurora"),
		"agent proceeded":         initflow.AgentProceeded("CLAUDECODE"),
		"gate proceeded":          initflow.GateProceeded(),
		"gate ordinary":           initflow.GateOrdinary(),
		"owner proceeded":         initflow.OwnerProceeded(),
		"ask restored":            initflow.AskRestored(),
		"ask unmanaged":           initflow.AskUnmanaged(),
		"ask no gate":             initflow.AskNoGate(),
		"access needs admin":      accessNeedsAdmin,
		"access flags admin":      accessFlagsNeedAdmin,
		"access no outputs":       accessNoOutputs,
		"access stale":            accessStale,
		"access dropped":          accessDropped,
		"access added":            accessAdded,
		"access adds unknown":     accessAddsUnknown,
		"access migrated":         accessMigratedNoGrants,
		"access mark needs admin": accessMarkNeedsAdmin,
		"reindex not marked":      fmt.Sprintf(reindexNotMarked, "the budget database could not be opened: firestore 404"),
	}
	for name, msg := range msgs {
		if msg == "" {
			t.Errorf("%s: the message is empty", name)
			continue
		}
		if strings.ContainsAny(msg, "\r\n") {
			t.Errorf("%s is not one line: %q", name, msg)
		}
		if n := len([]rune(msg)); n > maxMessageChars {
			t.Errorf("%s is %d characters, over the %d a message gets: %q", name, n, maxMessageChars, msg)
		}
	}
}
