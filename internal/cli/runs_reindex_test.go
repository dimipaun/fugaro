package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/runindex"
)

// reindexRig is the cloud fixture with the two things `fugaro runs reindex`
// writes to: the installation's Firestore (the index) and its budget database
// (the lease), plus the history job the command starts.
//
// firebase_project is added to the harness's budget block because the Firestore
// client is built from it (reportcore.go): the config records which Firebase
// project the index lives in, and the fake endpoint stands in for it. The
// block ends on the signer account (testutil.Budget, AB4: every installation
// with a database has one), which is where this rig writes the project in.
func reindexRig(t *testing.T) (*cloudFixture, *gcpfake.Firestore) {
	t.Helper()
	fs := gcpfake.NewFirestore(t)
	f := newCloudFixture(t, "firestore: "+fs.URL+"/")
	f.rewriteConfig(t, "rtdb_url: "+f.budget.DB.URL+",", "rtdb_url: "+f.budget.DB.URL+", firebase_project: proj-1234,")
	f.run.AddJob(infra.HistoryJob, "1", "2Gi")
	return f, fs
}

// TestRunsReindexStartsJob is RI5's command: `fugaro runs reindex` marks the
// index for a rebuild -- the lease loses its cursor and the summary is latched
// incomplete -- and then starts one execution of the history job in --reindex
// mode, with no timeout override, because the job's own timeout is what a pass
// leaves its cursor against.
//
// The mark is the half that always lands (O7): it is what makes every later
// sweep of the history job walk the index on its own, so a caller who may not
// start the job still gets a rebuild. That half is pinned here too -- a walk
// already going is left alone and nothing is started.
func TestRunsReindexStartsJob(t *testing.T) {
	f, fs := reindexRig(t)

	out, stderr, err := execute(t, "runs", "reindex")
	if err != nil {
		t.Fatalf("err = %v, stdout = %q, stderr = %q", err, out, stderr)
	}
	if !strings.Contains(out, "index: marked incomplete") {
		t.Fatalf("the command does not say what it marked: %q", out)
	}
	if !strings.Contains(out, "index: reindexing now as ") {
		t.Fatalf("the command does not say what it started: %q", out)
	}

	// The job was started once, in --reindex mode, with no timeout override
	// and nothing else in the override.
	calls := f.run.RunRequests()
	if len(calls) != 1 {
		t.Fatalf("runs = %+v, want one", calls)
	}
	if calls[0].Job != infra.HistoryJob || strings.Join(calls[0].Args, " ") != "budget history --reindex" {
		t.Fatalf("call = %+v", calls[0])
	}
	if calls[0].Timeout != "" {
		t.Fatalf("the start overrode the job's own timeout: %q", calls[0].Timeout)
	}
	if !strings.Contains(out, calls[0].Execution) {
		t.Fatalf("the command does not name the execution it started: %q", out)
	}

	// The lease carries the person's own identity and holds nothing: the walk
	// the reset asks for begins at the top of history, and the execution that
	// follows takes the node over rather than waiting out a TTL.
	l, ok := f.budget.DB.Value(runindex.PathLease).(map[string]any)
	if !ok {
		t.Fatalf("lease = %#v", f.budget.DB.Value(runindex.PathLease))
	}
	if l["holder"] != "someone@example.com" {
		t.Fatalf("the reset's node names %#v, want the person", l["holder"])
	}
	if c, ok := l["cursor"]; ok && c != nil && c != "" {
		t.Fatalf("the reset left a cursor behind: %#v", l)
	}
	expires, err := time.Parse(time.RFC3339Nano, l["expires_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if expires.After(time.Now()) {
		t.Fatalf("the reset's claim reads as fresh: %v", l)
	}

	// The summary is there and is not complete, which is what the next sweep
	// reads as "a walk is owed".
	sum, ok := fs.Value(runindex.SummaryCollection, runindex.SummaryDocument)
	if !ok {
		t.Fatal("the reset seeded no summary, so no sweep would walk")
	}
	if done, ok := sum["complete"].(bool); !ok || done {
		t.Fatalf("summary = %v", sum)
	}

	// A walk that is already going: the command starts nothing, because the
	// walk the index is owed is the one that is running.
	before := len(f.run.RunRequests())
	at := time.Now().UTC()
	f.budget.DB.Set(runindex.PathLease, map[string]any{
		"holder":     "fugaro-history-abc12",
		"at":         at.Format(time.RFC3339Nano),
		"expires_at": at.Add(runindex.LeaseTTL).Format(time.RFC3339Nano),
	})
	out, stderr, err = execute(t, "runs", "reindex")
	if err != nil {
		t.Fatalf("err = %v, stderr = %q", err, stderr)
	}
	if !strings.Contains(out, "already walking") {
		t.Fatalf("a live walk was not reported: %q", out)
	}
	if got := len(f.run.RunRequests()); got != before {
		t.Fatalf("a live walk was started over: %d calls", got)
	}
	if l, ok := f.budget.DB.Value(runindex.PathLease).(map[string]any); !ok || l["holder"] != "fugaro-history-abc12" {
		t.Fatalf("the running walk's claim was stamped over: %#v", f.budget.DB.Value(runindex.PathLease))
	}
}

// TestRunsReindexWithoutAReportedName is the line the command prints when the
// platform started the execution but its operation reported no readable name
// (gcp.ErrStartedUnnamed): the walk is going, so the line says that rather than
// leaving "reindexing now as" with nothing after it, and it must not read as
// "no execution was started", which would be untrue and would invite a second
// start.
func TestRunsReindexWithoutAReportedName(t *testing.T) {
	f, _ := reindexRig(t)
	f.run.BadRunMetadata = true

	out, stderr, err := execute(t, "runs", "reindex")
	if err != nil {
		t.Fatalf("err = %v, stdout = %q, stderr = %q", err, out, stderr)
	}
	if !strings.Contains(out, "index: reindexing now (the platform started the execution but did not report its name), resuming from its own cursor") {
		t.Fatalf("the line does not name the start without an execution name: %q", out)
	}
	if strings.Contains(out, "no execution was started") {
		t.Fatalf("a started execution was reported as one that never started: %q", out)
	}
	if got := len(f.run.RunRequests()); got != 1 {
		t.Fatalf("the history job was started %d times, want one: %+v", got, f.run.RunRequests())
	}
}

// TestRunsReindexOverAFoldClaimMarks is the lease's other holder (RI5, U3): the
// writer lease is the sweep's own while the sweep folds, and a dead execution's
// claim stands for its whole TTL. A person asking for a rebuild must not be
// told "a reindex is already walking" by one of those -- the mark is what makes
// the next sweep walk, and a rebuild turned away by a fold is a rebuild that
// never happens.
func TestRunsReindexOverAFoldClaimMarks(t *testing.T) {
	f, fs := reindexRig(t)
	at := time.Now().UTC()
	f.budget.DB.Set(runindex.PathLease, map[string]any{
		"holder":     "fugaro-history-fold1",
		"at":         at.Format(time.RFC3339Nano),
		"expires_at": at.Add(runindex.LeaseTTL).Format(time.RFC3339Nano),
	})
	fs.Set(runindex.SummaryCollection, runindex.SummaryDocument, map[string]any{
		"version": int64(1), "complete": true, "folded_through": at, "updated_at": at,
		"runs": int64(2), "states": map[string]any{"succeeded": int64(2)},
		"spent_micros": int64(0), "notional_micros": int64(0),
		"compute_runs": int64(0), "compute_not_estimated": int64(0),
	})

	out, stderr, err := execute(t, "runs", "reindex")
	if err != nil {
		t.Fatalf("err = %v, stderr = %q", err, stderr)
	}
	if strings.Contains(out, "already walking") {
		t.Fatalf("a fold's claim was read as a reindex in flight: %q", out)
	}
	if !strings.Contains(out, "index: marked incomplete") || !strings.Contains(out, "index: reindexing now as ") {
		t.Fatalf("the command did not mark and start: %q", out)
	}
	if sum, ok := fs.Value(runindex.SummaryCollection, runindex.SummaryDocument); !ok {
		t.Fatal("the command left no summary")
	} else if done, ok := sum["complete"].(bool); !ok || done {
		t.Fatalf("the command left the index complete: %v", sum)
	}
	// The folding writer's claim is left exactly as it stands: it is a writer,
	// and stamping over it would tell it its own lease had moved.
	l, ok := f.budget.DB.Value(runindex.PathLease).(map[string]any)
	if !ok || l["holder"] != "fugaro-history-fold1" {
		t.Fatalf("the command stamped over a folding writer's claim: %#v", f.budget.DB.Value(runindex.PathLease))
	}
}

// TestRunsReindexStandsWhenTheJobStartIsRefused is O7: only the installation's
// scheduler account holds run.invoker on the history job, so a caller who may
// not start it -- a member, an operator whose role does not cover the job -- is
// refused by the platform. That refusal must not fail the command, because the
// reset ran first and is the half the command is for: the index is marked
// incomplete, the next sweep of the history job walks it on its own, and the
// command says so in one line rather than erroring.
func TestRunsReindexStandsWhenTheJobStartIsRefused(t *testing.T) {
	f, fs := reindexRig(t)
	f.run.FailRunWith = 403

	out, stderr, err := execute(t, "runs", "reindex")
	if err != nil {
		t.Fatalf("a refused job start failed the command: err = %v, stderr = %q", err, stderr)
	}
	if !strings.Contains(out, "index: marked incomplete") {
		t.Fatalf("the command does not say what it marked: %q", out)
	}
	if !strings.Contains(out, "index: no execution was started") || !strings.Contains(out, "The reindex stands all the same") {
		t.Fatalf("the command does not report the refused start and the mark that stands: %q", out)
	}
	if strings.Contains(out, "index: reindexing now as ") {
		t.Fatalf("the command claims it started an execution it could not: %q", out)
	}

	// The start was attempted and refused, so nothing carries an execution.
	calls := f.run.RunRequests()
	if len(calls) != 1 || calls[0].Job != infra.HistoryJob || calls[0].Execution != "" {
		t.Fatalf("the refused start was recorded as an execution: %+v", calls)
	}

	// The reset landed before the start was tried, which is what makes the
	// refusal harmless: the lease names the person and holds nothing, and the
	// summary is latched incomplete, so every later sweep walks the index.
	l, ok := f.budget.DB.Value(runindex.PathLease).(map[string]any)
	if !ok {
		t.Fatalf("lease = %#v", f.budget.DB.Value(runindex.PathLease))
	}
	if l["holder"] != "someone@example.com" {
		t.Fatalf("the reset's node names %#v, want the person", l["holder"])
	}
	if c, ok := l["cursor"]; ok && c != nil && c != "" {
		t.Fatalf("the reset left a cursor behind: %#v", l)
	}
	sum, ok := fs.Value(runindex.SummaryCollection, runindex.SummaryDocument)
	if !ok {
		t.Fatal("the reset seeded no summary, so no sweep would walk")
	}
	if done, ok := sum["complete"].(bool); !ok || done {
		t.Fatalf("summary = %v", sum)
	}
}
