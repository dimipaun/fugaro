package cli

// PR7 of docs/model-credentials-routing: `runs ls` shows a paused run as paused,
// with the instant its resume is due at and the run that resumed it (R15,
// D3195), in text and in --json.
//
// A paused run is a finished row — its execution is terminal, so its registry
// entry is gone and the listing reads it from the runs bucket like any other
// run that is over — and the run's own record carries the word and nothing
// else. The two facts a reader wants beside it are elsewhere, and the test
// seeds both the way the installation writes them: the instant is the
// credentials service's node under paused/ (runstore.PauseNode), and the
// resumer is another run's task naming the paused run under `resumes`
// (task.Spec.Resumes). A reader that had only the record would show the word
// and neither fact, which is the degradation the last sub-test pins.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

// seedResumer seeds the run that picked a pause up: a follow-up in the same
// chain whose own task names the paused run under `resumes` (R17), which is the
// only place a reader finds it — a chain is walked backwards, so the paused
// run's own chain holds its ancestors and never the run launched to continue
// it. Its record is an ordinary finished one: what makes it a resumer is its
// task and nothing else.
func seedResumer(t *testing.T, f *cloudFixture, id, paused string, at time.Time) {
	t.Helper()
	spec := followUpSpec(id, paused, paused, 7)
	spec.Resumes = paused
	spec.Title = "Resume of the fixture's chain"
	seedSpec(t, f, spec, false)
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web",
		Branch: "fugaro/" + paused, Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady,
		StartedAt: at, FollowUp: &runstore.FollowUp{PR: 7, PreviousRun: paused}}
	writeRecord(t, f, id, rec)
}

// rowJSON is the first row of a `runs ls --json` document whose run_id is id,
// as its own key set: the shape check the two new members are made on, because
// the members are new and no member beside them may be retyped.
func rowJSON(t *testing.T, id string, args ...string) map[string]json.RawMessage {
	t.Helper()
	out, errOut, err := execute(t, append([]string{"runs", "ls", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("ls: %v (%s)", err, errOut)
	}
	var doc struct {
		Runs []map[string]json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, r := range doc.Runs {
		var got string
		if err := json.Unmarshal(r["run_id"], &got); err == nil && got == id {
			return r
		}
	}
	t.Fatalf("runs ls listed %d rows and none of them is %s: %s", len(doc.Runs), id, out)
	return nil
}

// TestRunsLsShowsThePauseAndItsResume is the task's own test: a paused run is
// listed as paused with its resume instant and its resumer, in both documents,
// and a run that is not paused carries neither fact.
func TestRunsLsShowsThePauseAndItsResume(t *testing.T) {
	f := newCloudFixture(t)
	at := lsNow().UTC().Truncate(time.Second)
	resume := at.Add(90 * time.Minute).Truncate(time.Second)
	suspended := wallRunIDAt(0, "160000", "7d3e")
	resumer := wallRunIDAt(0, "161000", "9f10")
	pausedRun(t, f, suspended, "", 7, at)
	pausedNode(t, f, suspended, suspended, resume, false)
	seedResumer(t, f, resumer, suspended, at.Add(time.Minute))

	t.Run("--json", func(t *testing.T) {
		got, _ := lsJSON(t, "--since", "0")
		row, ok := findRow(got.Runs, suspended)
		if !ok {
			t.Fatalf("runs ls = %+v, want the run that suspended itself listed", runIDs(got.Runs))
		}
		if row.Status != string(runstore.StatusPaused) {
			t.Errorf("status = %q, want %q: a pause is the run's own record's word", row.Status, runstore.StatusPaused)
		}
		if row.ResumeAt == nil || !row.ResumeAt.Equal(resume) {
			t.Errorf("resume_at = %v, want %s", row.ResumeAt, resume.Format(time.RFC3339))
		}
		if row.ResumedBy != resumer {
			t.Errorf("resumed_by = %q, want %q: the resumer is the run whose task names this one under `resumes`", row.ResumedBy, resumer)
		}
		// The resumer's own row is an ordinary finished run's: it was not
		// paused, so neither member has anything to say about it. That is what
		// makes the two members answers about a pause and not a second pair of
		// stamps on every row.
		other, ok := findRow(got.Runs, resumer)
		if !ok {
			t.Fatalf("runs ls did not list the resumer %s", resumer)
		}
		if other.ResumeAt != nil || other.ResumedBy != "" {
			t.Errorf("the resumer's own row carries resume_at %v and resumed_by %q, want neither", other.ResumeAt, other.ResumedBy)
		}
	})

	t.Run("the wire members are new, and none retyped", func(t *testing.T) {
		row := rowJSON(t, suspended, "--since", "0")
		if got := string(row["resume_at"]); got != `"`+resume.Format(time.RFC3339)+`"` {
			t.Errorf("resume_at on the wire = %s, want the instant as RFC 3339 UTC", got)
		}
		if got := string(row["resumed_by"]); got != `"`+resumer+`"` {
			t.Errorf("resumed_by on the wire = %s, want the resumer's run id, %q", got, resumer)
		}
		// `status` keeps its own spelling and its own meaning: a paused run's
		// row says `paused` where it always said the record's status, and the
		// two new members are beside it rather than instead of it (CLAUDE.md,
		// "A field never changes meaning").
		if got := string(row["status"]); got != `"`+string(runstore.StatusPaused)+`"` {
			t.Errorf("status on the wire = %s, want %q", got, runstore.StatusPaused)
		}
		// A run that is not paused carries neither member at all: both are
		// `omitempty`, so their absence is what says "no pause", exactly as the
		// contract's other optional members read.
		other := rowJSON(t, resumer, "--since", "0")
		for _, key := range []string{"resume_at", "resumed_by"} {
			if _, ok := other[key]; ok {
				t.Errorf("the resumer's row on the wire carries %s, want the key absent", key)
			}
		}
	})

	t.Run("the text table", func(t *testing.T) {
		out, _, err := execute(t, "runs", "ls", "--since", "0")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "RESUME") {
			t.Errorf("the listing has no RESUME column:\n%s", out)
		}
		// The same two facts, in the same row: the instant the chain comes
		// back, and the run that resumed it — the whole of what tells a reader
		// whether the wait is minutes or hours (R15).
		if want := resume.Format(time.RFC3339) + " by " + task.ShortRun(resumer); !strings.Contains(out, want) {
			t.Errorf("the listing does not carry %q for the paused run:\n%s", want, out)
		}
	})
}

// TestRunsLsListsAPausedRunWithOnlyTheWordItHas: the two facts degrade to
// absence, and neither degradation is a claim. A pause whose node this reader
// cannot see still reads `paused` — the run's own record says it suspended
// itself — and a listing whose window holds no resumer shows the instant alone;
// nothing is invented to fill either cell, and no member is dropped from the
// document. The column itself is the page's, so a listing with no paused run
// has no column to leave `-` down.
func TestRunsLsListsAPausedRunWithOnlyTheWordItHas(t *testing.T) {
	f := newCloudFixture(t)
	at := lsNow().UTC().Truncate(time.Second)
	resume := at.Add(30 * time.Minute).Truncate(time.Second)
	suspended := wallRunIDAt(0, "162000", "cafe")

	pausedRun(t, f, suspended, "", 0, at)
	// No node and no resumer: the record alone.
	got, _ := lsJSON(t, "--since", "0")
	row, ok := findRow(got.Runs, suspended)
	if !ok {
		t.Fatalf("runs ls did not list the paused run %s", suspended)
	}
	if row.Status != string(runstore.StatusPaused) || row.ResumeAt != nil || row.ResumedBy != "" {
		t.Errorf("a pause with no node = status %q resume_at %v resumed_by %q, want the word alone", row.Status, row.ResumeAt, row.ResumedBy)
	}
	out, _, err := execute(t, "runs", "ls", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "paused") {
		t.Errorf("the listing does not show the paused run as paused:\n%s", out)
	}

	// The node, and no resumer: the instant is shown and the resumer is not
	// named, because nothing in the listing read one.
	pausedNode(t, f, suspended, suspended, resume, false)
	raw := rowJSON(t, suspended, "--since", "0")
	if got := string(raw["resume_at"]); got != `"`+resume.Format(time.RFC3339)+`"` {
		t.Errorf("resume_at = %s, want %s", got, resume.Format(time.RFC3339))
	}
	if _, ok := raw["resumed_by"]; ok {
		t.Error("a pause nothing has resumed carries resumed_by: absence is the answer, not a name")
	}
	out, _, err = execute(t, "runs", "ls", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	line := rowLine(t, out, suspended)
	if want := resume.Format(time.RFC3339); !strings.Contains(line, want) {
		t.Errorf("the paused run's line does not carry the resume instant %q:\n%s", want, line)
	}
	if strings.Contains(line, "by ") {
		t.Errorf("the paused run's line names a resumer for a pause none has resumed:\n%s", line)
	}
}

// TestRunsLsHasNoResumeColumnWithoutAPausedRun: the column is the page's, and a
// listing whose window holds no paused run is the listing it always was — no
// widened row, and no cell left `-` down the side of it.
func TestRunsLsHasNoResumeColumnWithoutAPausedRun(t *testing.T) {
	f := newCloudFixture(t)
	plain := wallRunIDAt(0, "163000", "beef")
	seedRun(t, f, plain, "", "someone@example.com", false)
	writeRecord(t, f, plain, &runstore.Record{Version: 1, RunID: plain, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeReady})

	out, _, err := execute(t, "runs", "ls", "--since", "0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "RESUME") {
		t.Errorf("a listing with no paused run widened every row for a RESUME column:\n%s", out)
	}
}

// rowLine is the listing's line for run id: the table is tab-aligned, so a row
// is read whole and a cell's own text is asserted inside it. The RUN cell names
// the run by its short form (task.ShortRun, SR1), which is what the line is
// found by.
func rowLine(t *testing.T, out, id string) string {
	t.Helper()
	short := task.ShortRun(id)
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, short) {
			return line
		}
	}
	t.Fatalf("the listing has no line for %s (%s):\n%s", id, short, out)
	return ""
}

// findRow is the row of run id in a listing, ok false when the listing does not
// hold it.
func findRow(rows []runview.Row, id string) (runview.Row, bool) {
	for _, r := range rows {
		if r.RunID == id {
			return r, true
		}
	}
	return runview.Row{}, false
}
