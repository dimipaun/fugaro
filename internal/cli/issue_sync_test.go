package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/issues"
)

// The tests in this file drive the shared half of the `issue` family through
// the tree: `sync` uploads, `list` merges the project's issues with this
// machine's own, `show` prints the project's copy, `dismiss` sets one aside
// there too. The world they run in is issue_mine_test.go's rig — a state
// directory of its own, a shared store over the GCS fake and the seams the
// commands resolve them through — because the four commands read exactly the
// world `mine` reads.

// sync runs `fugaro issue sync` and answers with what it printed and the error
// it answered with, which is the whole of what a failing sync says.
func (r *mineRig) sync() (string, string, error) {
	r.t.Helper()
	return r.run("issue", "sync")
}

// listing runs `fugaro issue list` and answers with its table and the lines it
// put on stderr.
func (r *mineRig) listing() (string, string) {
	r.t.Helper()
	out, errOut, err := r.run("issue", "list")
	if err != nil {
		r.t.Fatalf("issue list: %v\n%s%s", err, out, errOut)
	}
	return out, errOut
}

// syncRow is the listing's row for one issue reference, or nil when the
// listing has no row for it. The cells are read as fields rather than by
// column, so the tabwriter's padding is not what a test depends on.
func syncRow(out, ref string) []string {
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == ref {
			return fields
		}
	}
	return nil
}

// TestSyncUploadsPendingAndRenames: `issue sync` uploads the issues this
// machine saved and has not uploaded, and the record's file takes the name the
// number gives it (D1723). A second run has nothing left to do — the point of
// the uploaded flag and the rename together.
func TestSyncUploadsPendingAndRenames(t *testing.T) {
	rig := newMineRig(t)
	rec := rig.save(mineRec("1", "sig-a"))

	out, errOut, err := rig.sync()
	if err != nil {
		t.Fatalf("issue sync: %v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(out, "Uploaded 1 issue(s).") {
		t.Errorf("sync does not say what it uploaded:\n%s", out)
	}

	got, err := rig.store.Get("1")
	if err != nil {
		t.Fatalf("reading the uploaded issue back: %v", err)
	}
	if got.Number == 0 || !got.Uploaded {
		t.Errorf("the uploaded issue is number %d, uploaded %v, want a number and uploaded", got.Number, got.Uploaded)
	}
	if got.Fingerprint != rec.Fingerprint {
		t.Errorf("the uploaded issue carries fingerprint %q, want %q", got.Fingerprint, rec.Fingerprint)
	}
	// The number is the file's name now, and the local name is gone with it.
	name, err := got.FileName()
	if err != nil {
		t.Fatalf("the uploaded issue has no file name: %v", err)
	}
	numbered := filepath.Join(rig.store.Dir(), name)
	if _, err := os.Stat(numbered); err != nil {
		t.Errorf("the uploaded issue is not at %s: %v", numbered, err)
	}
	if _, err := os.Stat(filepath.Join(rig.store.Dir(), rec.LocalID+".md")); !os.IsNotExist(err) {
		t.Errorf("the local name of an uploaded issue is still in the folder (err = %v)", err)
	}

	// The project's store is where the number came from: the record is there,
	// under the reporter the credential names and not the record's own header.
	shared, err := issues.SharedIssue(context.Background(), rig.bucket, got.Number)
	if err != nil {
		t.Fatalf("reading the shared issue: %v", err)
	}
	if shared.Reporter != rig.caller {
		t.Errorf("the shared issue is filed by %q, want the credential's %q", shared.Reporter, rig.caller)
	}

	out, errOut, err = rig.sync()
	if err != nil {
		t.Fatalf("the second issue sync: %v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(out, "No pending issues.") {
		t.Errorf("a second sync does not say there is nothing to upload:\n%s", out)
	}
	if strings.Contains(out, "Uploaded") {
		t.Errorf("a second sync uploads the same issue again:\n%s", out)
	}
}

// TestSyncOfflineKeepsLocal: a run that cannot reach the project uploads
// nothing and says so, and the issue stays where it is for the next run — the
// store is best effort and an issue is saved locally first (spec 2). Both ways
// a machine has no store are covered: no project selected, and a project whose
// store cannot be opened.
func TestSyncOfflineKeepsLocal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*mineRig)
		want  string
	}{
		{"no project", func(r *mineRig) { r.noProject = true }, "runs bucket could not be found"},
		{"unreachable store", func(r *mineRig) {
			r.openErr = errors.New("dial tcp: connect: network is unreachable")
		}, "shared store could not be opened"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newMineRig(t)
			rec := rig.save(mineRec("1", "sig-a"))
			tc.setup(rig)

			out, errOut, err := rig.sync()
			if err == nil {
				t.Fatalf("a sync with no store succeeded:\n%s%s", out, errOut)
			}
			if !strings.Contains(err.Error(), "nothing was uploaded") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("sync = %q, want a refusal naming the store (%q)", err, tc.want)
			}

			got, err := rig.store.Get(rec.LocalID)
			if err != nil {
				t.Fatalf("the issue is not where it was: %v", err)
			}
			if got.Uploaded || got.Number != 0 {
				t.Errorf("an issue with no store was uploaded as %d", got.Number)
			}
			if _, err := os.Stat(filepath.Join(rig.store.Dir(), rec.LocalID+".md")); err != nil {
				t.Errorf("the issue lost its local name: %v", err)
			}
		})
	}
}

// TestSyncRefusesInAgentSession: uploading is a write to the project's store,
// and a coding agent's session makes none (F10, D1734). The refusal names the
// command a person runs, and it comes before anything is read: no credential
// is asked and no bucket is opened.
func TestSyncRefusesInAgentSession(t *testing.T) {
	rig := newMineRig(t)
	rec := rig.save(mineRec("1", "sig-a"))
	t.Setenv("CLAUDECODE", "1")

	out, errOut, err := rig.sync()
	if err == nil {
		t.Fatalf("a coding agent's session uploaded:\n%s%s", out, errOut)
	}
	if !strings.Contains(err.Error(), "coding agent's session") || !strings.Contains(err.Error(), "fugaro issue sync") {
		t.Errorf("sync = %q, want a refusal naming the session and the command a person runs", err)
	}
	if rig.lookups != 0 {
		t.Errorf("a coding agent's session asked the credential %d time(s)", rig.lookups)
	}
	if rig.opens != 0 {
		t.Errorf("a coding agent's session opened the shared store %d time(s)", rig.opens)
	}
	if rows, err := issues.ListShared(context.Background(), rig.bucket); err != nil {
		t.Fatalf("reading the shared store: %v", err)
	} else if len(rows) != 0 {
		t.Errorf("a coding agent's session left %d issue(s) in the shared store", len(rows))
	}
	if got, err := rig.store.Get(rec.LocalID); err != nil {
		t.Fatalf("the issue is not where it was: %v", err)
	} else if got.Uploaded {
		t.Error("a coding agent's session marked the issue uploaded")
	}
}

// TestListShowsWholeTeamsIssues: an issue is the project's, so `list` shows
// every issue of the project's store and not only the ones this machine saved
// (spec 4b). Where both sides have a record of one number the store's is the
// row (F13) — a teammate's dismissal is what the listing says, not the stale
// status this machine's file carries.
func TestListShowsWholeTeamsIssues(t *testing.T) {
	rig := newMineRig(t)
	// A teammate's issue: this machine has no file of it at all.
	mate := mineRec("1", "sig-mate")
	mate.Summary = "the run stops on the second step"
	mateUp := rig.upload(mate, "user:bo@example.test")
	// One this machine saved and a teammate set aside in the store: the file
	// here still says pending.
	stale := mineRec("2", "sig-stale")
	staleUp := rig.upload(stale, rig.caller)
	local := stale
	local.Number, local.Uploaded = staleUp.Number, true
	rig.save(local)
	if err := issues.SetSharedStatus(context.Background(), rig.bucket, staleUp.Number, issues.StatusDismissed, rig.now); err != nil {
		t.Fatalf("setting the shared status: %v", err)
	}

	out, errOut := rig.listing()
	if errOut != "" {
		t.Errorf("a listing that reached the store printed %q on stderr", errOut)
	}
	if row := syncRow(out, mineRefOf(mateUp.Number)); row == nil {
		t.Errorf("the listing drops a teammate's issue:\n%s", out)
	} else if !strings.Contains(strings.Join(row, " "), mate.Summary) {
		t.Errorf("the teammate's row is %q, want its summary %q", row, mate.Summary)
	}
	if row := syncRow(out, mineRefOf(staleUp.Number)); row == nil {
		t.Errorf("the listing drops an issue the store holds:\n%s", out)
	} else if !strings.Contains(strings.Join(row, " "), string(issues.StatusDismissed)) {
		t.Errorf("the row is %q, want the store's %q and not this machine's file", row, issues.StatusDismissed)
	}
	if strings.Contains(out, local.LocalID) {
		t.Errorf("an issue that reached the store still prints its local id:\n%s", out)
	}
}

// TestDismissWritesSharedStatus: an issue is the project's, so setting one
// aside sets it aside in the store as well (F13) — a teammate's listing then
// shows the dismissal, and nobody decides the same issue twice. A store that
// answers and refuses is the other half: the decision is not this machine's to
// make, and the file here is left as it was.
func TestDismissWritesSharedStatus(t *testing.T) {
	t.Run("the shared copy is set aside", func(t *testing.T) {
		rig := newMineRig(t)
		rec := mineRec("1", "sig-a")
		up := rig.upload(rec, rig.caller)
		local := rec
		local.Number, local.Uploaded = up.Number, true
		rig.save(local)

		out, errOut, err := rig.run("issue", "dismiss", mineRefOf(up.Number))
		if err != nil {
			t.Fatalf("issue dismiss: %v\n%s%s", err, out, errOut)
		}
		if !strings.Contains(out, "Dismissed "+mineRefOf(up.Number)) {
			t.Errorf("dismiss does not name the issue it set aside:\n%s", out)
		}
		shared, err := issues.SharedIssue(context.Background(), rig.bucket, up.Number)
		if err != nil {
			t.Fatalf("reading the shared issue: %v", err)
		}
		if shared.Status != issues.StatusDismissed {
			t.Errorf("the project's copy is %q, want %q", shared.Status, issues.StatusDismissed)
		}
		if shared.DismissedAt == nil {
			t.Error("the project's copy carries no dismissal date")
		}
		got, err := rig.store.Get("1")
		if err != nil {
			t.Fatalf("reading the issue back: %v", err)
		}
		if got.Status != issues.StatusDismissed {
			t.Errorf("this machine's file is %q, want %q", got.Status, issues.StatusDismissed)
		}
	})

	t.Run("a store that refuses", func(t *testing.T) {
		rig := newMineRig(t)
		// The store has the issue confirmed fixed by a release: it is decided,
		// and setting it aside is not this machine's to do (F9).
		fixed := mineRec("1", "sig-a")
		fixed.Status = issues.StatusResolved
		up := rig.upload(fixed, rig.caller)
		local := mineRec("1", "sig-a")
		local.Number, local.Uploaded = up.Number, true
		rig.save(local)

		out, errOut, err := rig.run("issue", "dismiss", mineRefOf(up.Number))
		if err == nil {
			t.Fatalf("a decided issue was set aside:\n%s%s", out, errOut)
		}
		if !strings.Contains(err.Error(), "nothing was dismissed") {
			t.Errorf("dismiss = %q, want a refusal", err)
		}
		if got, err := rig.store.Get("1"); err != nil {
			t.Fatalf("reading the issue back: %v", err)
		} else if got.Status != issues.StatusPending {
			t.Errorf("a refused dismiss left the file %q, want it as it was: %q", got.Status, issues.StatusPending)
		}
		shared, err := issues.SharedIssue(context.Background(), rig.bucket, up.Number)
		if err != nil {
			t.Fatalf("reading the shared issue: %v", err)
		}
		if shared.Status != issues.StatusResolved {
			t.Errorf("a refused dismiss moved the project's copy to %q", shared.Status)
		}
	})
}

// TestSyncRunsOnlyInIssueCommands: the upload lives in the `issue` commands
// and nowhere else (D1725). A command that is not one of them leaves a pending
// issue pending — which is what keeps `doctor`, `runs digest` and the rest of
// the tree from writing to the project's store behind a person's back.
func TestSyncRunsOnlyInIssueCommands(t *testing.T) {
	rig := newMineRig(t)
	rec := rig.save(mineRec("1", "sig-a"))

	for _, args := range [][]string{
		{"version"},
		{"doctor", "--dir", t.TempDir()},
	} {
		// What each prints is its own business here, and doctor's exit code is
		// the installation's: the fact is that the issue is still waiting.
		_, _, _ = rig.run(args...)
		if got, err := rig.store.Get(rec.LocalID); err != nil {
			t.Fatalf("%s: the issue is not where it was: %v", strings.Join(args, " "), err)
		} else if got.Uploaded || got.Number != 0 {
			t.Errorf("%s uploaded the pending issue as %d", strings.Join(args, " "), got.Number)
		}
	}
	if rows, err := issues.ListShared(context.Background(), rig.bucket); err != nil {
		t.Fatalf("reading the shared store: %v", err)
	} else if len(rows) != 0 {
		t.Errorf("a command that is not an issue command left %d issue(s) in the shared store", len(rows))
	}
}

// TestListShowUseLocalInAgentSession: a coding agent's session uploads
// nothing, so it reads nothing from the project either (F10, D1734). `list`
// and `show` answer from the files here and say why, and neither the
// credential nor the store is touched.
func TestListShowUseLocalInAgentSession(t *testing.T) {
	rig := newMineRig(t)
	mate := mineRec("1", "sig-mate")
	mateUp := rig.upload(mate, "user:bo@example.test")
	rec := rig.save(mineRec("2", "sig-a"))
	t.Setenv("CLAUDECODE", "1")

	out, errOut := rig.listing()
	if !strings.Contains(errOut, "coding agent's session") {
		t.Errorf("a session's listing does not say what it read:\n%s", errOut)
	}
	if row := syncRow(out, rec.LocalID); row == nil {
		t.Errorf("a session's listing drops this machine's own issue:\n%s", out)
	}
	if strings.Contains(out, mineRefOf(mateUp.Number)) {
		t.Errorf("a session's listing shows an issue only the project's store has:\n%s", out)
	}

	shown, errOut, err := rig.run("issue", "show", rec.LocalID)
	if err != nil {
		t.Fatalf("issue show: %v\n%s%s", err, shown, errOut)
	}
	if !strings.Contains(shown, rec.LocalID) || !strings.Contains(errOut, "coding agent's session") {
		t.Errorf("a session's show printed %q and said %q", shown, errOut)
	}
	if _, _, err := rig.run("issue", "show", mineRefOf(mateUp.Number)); err == nil {
		t.Error("a session's show read the project's store")
	}
	if rig.lookups != 0 {
		t.Errorf("a coding agent's session asked the credential %d time(s)", rig.lookups)
	}
	if rig.opens != 0 {
		t.Errorf("a coding agent's session opened the shared store %d time(s)", rig.opens)
	}
	if got, err := rig.store.Get(rec.LocalID); err != nil {
		t.Fatalf("the issue is not where it was: %v", err)
	} else if got.Uploaded {
		t.Error("a coding agent's session uploaded the issue")
	}
}
