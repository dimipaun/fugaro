package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/issues"
)

// TestAgentMayReadAndStop is D3425's read-and-stop lift: a coding agent's
// session reads the project's issues exactly as a person's does — `issue list`,
// `show` and `mine` answer from the project's store and not from this machine's
// files alone — and it may run `runs digest` and stop a workspace with
// `budget kill --workspace`. The one raise beside the stop, `budget resume`,
// stays the owner's (D1542).
//
// `followups` is the other read D3425 lifts, and it already runs in a session
// through the installation's credentials service: its own
// TestFollowUpsInACodingAgentsSessionReadsThroughTheCredentialsService pins it.
func TestAgentMayReadAndStop(t *testing.T) {
	t.Run("issue list, show and mine read the project's store", func(t *testing.T) {
		rig := newMineRig(t)
		// The issue is this user's, so `mine` has it too: `mine` shows the
		// project's issues this user reported, and `list` shows the whole team's.
		mate := mineRec("1", "sig-a")
		up := rig.upload(mate, rig.caller)
		local := rig.save(mineRec("2", "sig-b"))
		t.Setenv("CLAUDECODE", "1")

		out, _, err := rig.run("issue", "list")
		if err != nil {
			t.Fatalf("issue list in a coding agent's session: %v", err)
		}
		if syncRow(out, mineRefOf(up.Number)) == nil {
			t.Errorf("a session's listing does not read the project's store:\n%s", out)
		}
		if syncRow(out, local.LocalID) == nil {
			t.Errorf("a session's listing drops this machine's own issue:\n%s", out)
		}

		shown, _, err := rig.run("issue", "show", mineRefOf(up.Number))
		if err != nil {
			t.Fatalf("issue show of a numbered issue in a coding agent's session: %v", err)
		}
		if !strings.Contains(shown, mate.Summary) {
			t.Errorf("a session's show does not print the project's copy:\n%s", shown)
		}

		if syncRow(rig.mine(), mineRefOf(up.Number)) == nil {
			t.Errorf("a session's mine does not read the project's store:\n%s", rig.mine())
		}

		// The read lifts and the upload does not: this is AG5a's write, and a
		// listing that uploaded on the way would be it by the back door.
		if rows, err := issues.ListShared(context.Background(), rig.bucket); err != nil {
			t.Fatalf("reading the shared store: %v", err)
		} else if len(rows) != 1 {
			t.Errorf("a coding agent's session uploaded: the store holds %d issue(s), want 1", len(rows))
		}
		if got, err := rig.store.Get(local.LocalID); err != nil {
			t.Fatalf("the issue is not where it was: %v", err)
		} else if got.Uploaded {
			t.Error("a coding agent's session uploaded this machine's own issue")
		}
	})

	t.Run("runs digest runs in a session", func(t *testing.T) {
		t.Setenv("CLAUDECODE", "1")
		if _, _, err := execute(t, "runs", "digest", "--due", "--dir", t.TempDir()); err != nil {
			t.Fatalf("runs digest --due in a coding agent's session: %v", err)
		}
	})

	t.Run("budget kill --workspace is a session's and resume is not", func(t *testing.T) {
		h := newWSUpdateHome(t, wsOwnerTable)
		db := h.project(t, "workspace-alpha").db
		db.Set(budget.PathProject, "workspace-alpha")
		t.Setenv("CLAUDECODE", "1")

		if _, _, err := execute(t, "budget", "kill", "--workspace", "workspace-alpha"); err != nil {
			t.Fatalf("budget kill --workspace in a coding agent's session: %v", err)
		}
		if on, _ := wsKillStop(t, db, "workspace-alpha"); !on {
			t.Fatal("the kill in a coding agent's session did not stop the workspace")
		}
		if _, _, err := execute(t, "budget", "resume", "--workspace", "workspace-alpha"); err == nil {
			t.Fatal("budget resume --workspace in a coding agent's session: want the owner-only refusal")
		}
	})
}
