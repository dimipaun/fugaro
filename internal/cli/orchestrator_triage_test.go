package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// The daemon's triage over a host, a run's own storage and the audit log
// (O14b).
//
// What is pinned here is what one cycle does when the rule has an outcome:
// which host calls it makes, at which head, which object it writes to stop a
// run, what it files for a person to approve, what it refuses to do when an
// input cannot be read, and the one line of audit that fences the run.

const (
	triageRunID    = "20261108-090000-abcd"
	triageHeadSHA  = "0b0c1d2e3f405162738495a6b7c8d9e0f1234567"
	triageMovedSHA = "1a2b3c4d5e6f708192a3b4c5d6e7f80912345678"
	triagePRNumber = 7
	// triageAge is how long the attempt has been going: past the ladder's
	// ceiling for every mode (12h for pro, the default), which is what makes
	// the run one the rule is read about at all.
	triageAge = 13 * time.Hour
)

// triageFixture is a daemon whose triage acts through a fake host and the
// fixture's own runs bucket, with one run recorded at the head the host shows.
type triageFixture struct {
	*daemonFixture
	d      *orchestratorDaemon
	out    *strings.Builder
	prov   *fake.Provider
	triage *orchestratorTriage
}

// newTriageFixture builds that daemon with the checks the host reports at the
// run's head: green for the run the rule may act on, and another answer for
// the tests about a host that says something else.
func newTriageFixture(t *testing.T, checks []gitprov.CheckRun) *triageFixture {
	t.Helper()
	f := newDaemonFixture(t)
	// The server's clock is the fixture's, so a triage line is stamped with the
	// same instant the seeded node is and the age the rule judges is exact.
	f.db.SetClock(func() time.Time { return orchestratorTestNow })
	d, out := f.running(t, "")
	prov := &fake.Provider{
		Repo: "acme/app", SelfID: "fugaro",
		State: fake.State{
			PRs: []fake.PRState{{
				PR:    gitprov.PR{Number: triagePRNumber, URL: "https://example.invalid/acme/app/pull/7"},
				Spec:  gitprov.PRSpec{Branch: "fugaro/" + triageRunID, Base: "main"},
				State: gitprov.PROpen,
				Head:  triageHeadSHA,
			}},
			Checks: map[string][]gitprov.CheckRun{triageHeadSHA: checks},
		},
	}
	tf := &triageFixture{daemonFixture: f, d: d, out: out, prov: prov}
	tf.triage = &orchestratorTriage{
		bucket: d.env.bucket.Bucket, db: d.db, id: d.id, env: d.env,
		open: gitprov.Static(prov), warn: out,
		opened: map[string]gitprov.Provider{}, warned: map[string]bool{},
	}
	d.tick.Triage = tf.triage.seams()
	// The run the node records is one the platform reports as going: the fold
	// and the plan have nothing to do with it, so what the cycle does here is
	// the triage's alone.
	d.tick.Runs = startedRuns{}
	tf.seed(t, triageAge, nil)
	tf.writeTask(t)
	tf.writeRecord(t, triageRecord())
	return tf
}

// green is the one check a host reports at a commit it calls passed.
func green() []gitprov.CheckRun {
	return []gitprov.CheckRun{{Status: "completed", Conclusion: "success"}}
}

// startedRuns is the run view of runs that are going: every id it is asked
// about is reported started and unfinished, so nothing in the fold or the plan
// acts on it.
type startedRuns struct{}

func (startedRuns) Runs(_ context.Context, runIDs []string) ([]orchestrator.RunReport, error) {
	out := make([]orchestrator.RunReport, 0, len(runIDs))
	for _, id := range runIDs {
		out = append(out, orchestrator.RunReport{RunID: id, Started: true})
	}
	return out, nil
}

// seed is the node the daemon reads: one item running under the run this test
// recorded, with the attempt and the approval that launched it.
func (f *triageFixture) seed(t *testing.T, age time.Duration, mutate func(*orchestrator.State)) {
	t.Helper()
	at := orchestratorTestNow.Add(-age)
	w := orchestrator.WorkItem{
		ID: "wi-1", Title: "land the sweep", Repo: "acme/app", Recipe: "standard",
		RigorTier: "medium", Brief: "Move the sweep to the daemon.",
		Phase: orchestrator.PhaseRunning, RunID: triageRunID, Attempts: 1,
	}
	digest := orchestrator.SpecDigest(w)
	w.Approved = &orchestrator.Approval{By: "owner", At: at, SpecDigest: digest, Attempts: orchestrator.ApprovalAttempts}
	s := orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       orchestratorTestID, Role: orchestrator.RoleOrchestrator,
		Goal:      "keep the repository's pull requests moving without a person watching",
		WorkItems: []orchestrator.WorkItem{w},
		Intents: []orchestrator.Intent{{
			ID: orchestrator.IntentID(w.ID, 1), RunID: triageRunID,
			SpecDigest: digest, ApprovalAt: at, At: at,
		}},
	}
	if mutate != nil {
		mutate(&s)
	}
	f.putState(t, s)
}

// triageRecord is the run as the runner finalized it: over, with the pull
// request it opened at the head it pushed and a diff wholly inside the routine
// allowlist.
func triageRecord() *runstore.Record {
	return &runstore.Record{
		Version: 1, RunID: triageRunID, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Stage: "finalize",
		HeadSHA: triageHeadSHA, PushedHead: triageHeadSHA,
		PR:           &runstore.PRRef{Number: triagePRNumber, URL: "https://example.invalid/acme/app/pull/7"},
		ChangedFiles: &runstore.ChangedFiles{Paths: []string{"docs/design/sweep.md"}, Total: 1},
	}
}

// writeTask puts the run's task in the run's own store. The daemon finds a run
// by the task its launcher wrote, so a fixture with a record and no task is a
// run no seam here can reach at all.
func (f *triageFixture) writeTask(t *testing.T) {
	t.Helper()
	doc := `{"version":1,"run_id":"` + triageRunID + `","repo":"acme/app"}`
	if err := f.store(t).PutFile(context.Background(), "task.json", []byte(doc), "application/json"); err != nil {
		t.Fatalf("writing the run's task: %v", err)
	}
}

// writeRecord puts one record in the run's own store.
func (f *triageFixture) writeRecord(t *testing.T, rec *runstore.Record) {
	t.Helper()
	if err := f.store(t).WriteRecord(context.Background(), rec); err != nil {
		t.Fatalf("writing the run's record: %v", err)
	}
}

// putRecord replaces the record at the run's own store.
func (f *triageFixture) putRecord(t *testing.T, rec *runstore.Record) {
	t.Helper()
	f.writeRecord(t, rec)
}

// store is the run's own storage, which is where its record, its findings and
// its cancel marker live.
func (f *triageFixture) store(t *testing.T) *runstore.Store {
	t.Helper()
	return runstore.Open(f.d.env.bucket.Bucket, appSlug, triageRunID)
}

// findings writes the runner's findings artifact into the run's own prefix.
func (f *triageFixture) findings(t *testing.T, findings ...string) {
	t.Helper()
	doc := `{"version":1,"run":"` + triageRunID + `","findings":[` + strings.Join(findings, ",") + `]}`
	if err := f.store(t).PutFile(context.Background(), triageFindingsObject, []byte(doc), "application/json"); err != nil {
		t.Fatalf("writing the run's findings artifact: %v", err)
	}
}

// step runs one real cycle of the fixture's daemon.
func (f *triageFixture) step(t *testing.T) orchestrator.Report {
	t.Helper()
	rep, err := f.d.tick.Step(context.Background())
	if err != nil {
		t.Fatalf("the cycle ended as %v", err)
	}
	return rep
}

// audit is the project's audit log as it stands.
func (f *triageFixture) audit(t *testing.T) map[string]orchestrator.Entry {
	t.Helper()
	path, err := orchestrator.PathAudit(orchestratorTestID)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]orchestrator.Entry
	if _, err := f.d.db.Get(context.Background(), path, &raw); err != nil {
		t.Fatalf("reading the audit log: %v", err)
	}
	return raw
}

// line is the one audit line the run's triage left, and a failure when there
// is not exactly one.
func (f *triageFixture) line(t *testing.T) orchestrator.Entry {
	t.Helper()
	e, ok := f.audit(t)[orchestrator.TriageAuditID(triageRunID)]
	if !ok {
		t.Fatalf("the run has no triage line: %+v", f.audit(t))
	}
	return e
}

// merges is every merge the host was asked for.
func (f *triageFixture) merges(t *testing.T) []fake.Call {
	t.Helper()
	var out []fake.Call
	for _, c := range f.prov.Snapshot().Calls {
		if c.Op == "Merge" {
			out = append(out, c)
		}
	}
	return out
}

// cancelRequested reports whether the runner would stop: the marker in the
// run's own store, which is the object `fugaro cancel` writes.
func (f *triageFixture) cancelRequested(t *testing.T) bool {
	t.Helper()
	ok, err := f.store(t).CancelRequested(context.Background())
	if err != nil {
		t.Fatalf("reading the run's cancel marker: %v", err)
	}
	return ok
}

// items is the node's work list, keyed by id.
func (f *triageFixture) items(t *testing.T) map[string]orchestrator.WorkItem {
	t.Helper()
	out := map[string]orchestrator.WorkItem{}
	for _, w := range f.state(t).WorkItems {
		out[w.ID] = w
	}
	return out
}

// filed is every item the node carries that the seed did not: what an outcome
// filed for a person to approve.
func (f *triageFixture) filed(t *testing.T) []orchestrator.WorkItem {
	t.Helper()
	var out []orchestrator.WorkItem
	for id, w := range f.items(t) {
		if id != "wi-1" {
			out = append(out, w)
		}
	}
	return out
}

// input is the triage read for the fixture's run, the way the daemon makes it
// and the way `fugaro runs watch` will (O14c).
func (f *triageFixture) input(t *testing.T) orchestrator.TriageInput {
	t.Helper()
	ctx := context.Background()
	rec, err := f.store(t).ReadRecord(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The host is the one this daemon would open for the repository, which is
	// what makes the reading below the daemon's own and not a second one.
	prov, err := f.triage.provider(ctx, "acme/app")
	if err != nil {
		prov = nil
	}
	in, err := GatherTriageInput(ctx, f.store(t), prov, rec)
	if err != nil {
		t.Fatalf("gathering the triage input: %v", err)
	}
	return in
}

// TestTriageReadsCIThroughTheProvider: the CI the rule judges is the host's
// answer about the commit the pull request is at, read through the provider,
// and the rule never invents one. A commit the host says passed is green, a
// commit it says failed is not, and a commit it reports nothing about is not
// green either — an empty answer is an answer the rule cannot read.
func TestTriageReadsCIThroughTheProvider(t *testing.T) {
	checks := green()
	f := newTriageFixture(t, checks)
	f.findings(t)

	in := f.input(t)
	if in.CI != gitprov.CIGreen {
		t.Fatalf("the read reports CI %s for a commit the host reports green", in.CI)
	}
	if in.PR == nil || in.PR.HeadSHA != triageHeadSHA || in.PR.Number != triagePRNumber {
		t.Fatalf("the read reports the pull request as %+v, want #%d at %s", in.PR, triagePRNumber, triageHeadSHA)
	}
	if in.Findings.Read != orchestrator.FindingsReadable {
		t.Fatalf("the read reports the findings as %q, want readable", in.Findings.Read)
	}

	f.step(t)
	if got := f.merges(t); len(got) != 1 {
		t.Fatalf("a green commit the host reported was merged %d time(s), want once: %+v", len(got), got)
	}
	if got := f.merges(t)[0]; got.PR != triagePRNumber || got.Detail != "sha="+triageHeadSHA {
		t.Errorf("the merge is %+v, want pull request %d at the head the rule read", got, triagePRNumber)
	}
	if got := f.prov.Snapshot().PRs[0].State; got != gitprov.PRMerged {
		t.Errorf("the host carries the pull request as %q after a green merge, want merged", got)
	}

	// The same run, judged where the host reports a check that failed and where
	// it reports none at all: neither is green, so neither merges.
	for _, tc := range []struct {
		name   string
		checks []gitprov.CheckRun
	}{
		{name: "a check the host says failed", checks: []gitprov.CheckRun{{Status: "completed", Conclusion: "failure"}}},
		{name: "a check that has not finished", checks: []gitprov.CheckRun{{Status: "in_progress", Conclusion: ""}}},
		{name: "no check at all", checks: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newTriageFixture(t, tc.checks)
			g.findings(t)
			if in := g.input(t); in.CI == gitprov.CIGreen {
				t.Fatalf("the read reports CI green for %s", tc.name)
			}
			g.step(t)
			if got := g.merges(t); len(got) != 0 {
				t.Errorf("%s was merged: %+v", tc.name, got)
			}
			if g.cancelRequested(t) {
				t.Errorf("%s stopped the run, and a CI read is not a reason to stop one", tc.name)
			}
			if e := g.line(t); e.ID != orchestrator.TriageAuditID(triageRunID) {
				t.Errorf("%s left the line %+v", tc.name, e)
			}
		})
	}
}

// TestTriageFailsClosedWithoutACIAnswer: a checks read the host refuses is
// unknown, never green, so the run is recommended on and nothing is merged or
// stopped — the same answer a run whose merge a person has to judge gets.
func TestTriageFailsClosedWithoutACIAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "the host holds no checks permission", err: gitprov.ErrChecksPermission},
		{name: "the host cannot report checks at all", err: gitprov.ErrChecksUnsupported},
		{name: "the read failed", err: errors.New("the host answered 500")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTriageFixture(t, green())
			f.findings(t)
			f.triage.open = gitprov.Static(refusingChecks{Provider: f.prov, err: tc.err})

			if in := f.input(t); in.CI != gitprov.CIUnknown {
				t.Fatalf("a refused checks read reports CI %s, want unknown", in.CI)
			}
			rep := f.step(t)
			if got := f.merges(t); len(got) != 0 {
				t.Errorf("a refused checks read merged: %+v", got)
			}
			if f.cancelRequested(t) {
				t.Errorf("a refused checks read stopped the run, and a read that failed is not evidence the run is bad")
			}
			if rep.Triaged != 1 {
				t.Errorf("the cycle reports %d triaged run(s), want the one it read", rep.Triaged)
			}
			if got := f.line(t).Text; !strings.Contains(got, string(orchestrator.ReasonCIUnknown)) {
				t.Errorf("the line is %q, and the reason it was recommended on is %s", got, orchestrator.ReasonCIUnknown)
			}
		})
	}
}

// refusingChecks is the host as a test makes it refuse the checks read: the
// fake's answers for everything else, and the error a provider answers with
// when it cannot say what a commit's checks were.
type refusingChecks struct {
	gitprov.Provider
	err error
}

func (r refusingChecks) Checks(context.Context, int, string) ([]gitprov.CheckRun, error) {
	return nil, r.err
}

// TestTriageCancelsThroughTheCancelPath: an escalated run is stopped through
// the run's own store — the marker `fugaro cancel` writes and the runner's own
// watch reads before it finalizes a draft pull request — and never by reaching
// past the runner for the execution.
func TestTriageCancelsThroughTheCancelPath(t *testing.T) {
	f := newTriageFixture(t, green())
	f.findings(t, `{"severity":"critical","path":"internal/agent/stage.go","key":"k1"}`)

	if f.cancelRequested(t) {
		t.Fatal("the run carries a cancel marker before the cycle ran")
	}
	f.step(t)
	if !f.cancelRequested(t) {
		t.Fatal("an escalated run was not stopped: the run's own store carries no cancel marker")
	}
	if !strings.Contains(f.line(t).Text, string(orchestrator.Escalate)) {
		t.Errorf("the audit line is %q, want the outcome the rule took", f.line(t).Text)
	}
	if got := f.merges(t); len(got) != 0 {
		t.Errorf("an escalated run was merged: %+v", got)
	}
}

// TestTriageMergesThroughTheProvider: a merge is the host's own call, made
// once, at the head the rule judged — and a head that moved under it is a
// refusal a person answers, with nothing recorded as landed.
func TestTriageMergesThroughTheProvider(t *testing.T) {
	t.Run("the head the rule judged lands", func(t *testing.T) {
		f := newTriageFixture(t, green())
		f.findings(t)
		f.step(t)
		if got := f.merges(t); len(got) != 1 || got[0].Detail != "sha="+triageHeadSHA {
			t.Fatalf("the host was asked to merge %+v, want one merge at %s", got, triageHeadSHA)
		}
		if got := f.prov.Snapshot().PRs[0].State; got != gitprov.PRMerged {
			t.Errorf("the pull request is %q, want merged", got)
		}
		if f.prov.Snapshot().PRs[0].Draft {
			t.Error("the pull request was marked ready, and the rule only ever merges a ready one")
		}
	})

	t.Run("a head that moved is refused", func(t *testing.T) {
		f := newTriageFixture(t, green())
		f.findings(t)
		f.triage.open = gitprov.Static(headMoves{Provider: f.prov, prov: f.prov, to: triageMovedSHA})

		f.step(t)
		if got := f.prov.Snapshot().PRs[0].State; got != gitprov.PROpen {
			t.Errorf("a moved head left the pull request %q, want it still open", got)
		}
		if len(f.filed(t)) != 0 {
			t.Errorf("a refused merge filed work: %+v", f.filed(t))
		}
		if got := f.line(t).Text; !strings.Contains(got, string(orchestrator.ReasonMergeRefused)) {
			t.Errorf("the audit line is %q, and a refused merge is a person's to read", got)
		}
		d, ok := f.state(t).Decision(triageRunID + "-" + "triage")
		if !ok || d.WaitingOn != orchestrator.OnPerson || d.Resolved() {
			t.Fatalf("a refused merge raised %+v, want an open question waiting on a person", d)
		}
	})
}

// headMoves is the host as a test moves it: the pull request is read at the
// head the run pushed, and the branch moves before the merge lands — the race
// the provider's own second read is there for.
type headMoves struct {
	gitprov.Provider
	prov *fake.Provider
	to   string
}

func (h headMoves) Merge(ctx context.Context, number int, expectedHeadSHA string) error {
	h.prov.State.PRs[0].Head = h.to
	return h.Provider.Merge(ctx, number, expectedHeadSHA)
}

// TestTriageRestartFilesSmallerItemsWaitingForApproval: a restarted run stops,
// and what it should have done is filed as work waiting for a person — one
// item per reason the rule gave, none of them approved by the daemon (D1226).
func TestTriageRestartFilesSmallerItemsWaitingForApproval(t *testing.T) {
	f := newTriageFixture(t, green())
	f.findings(t)
	// A run that pushed nothing: what the rule restarts, and never merges.
	rec := triageRecord()
	rec.Status = runstore.StatusFailed
	rec.Stage = "code"
	rec.PushedHead = ""
	rec.ChangedFiles = &runstore.ChangedFiles{Paths: []string{}, Total: 0}
	f.putRecord(t, rec)

	f.step(t)
	if !f.cancelRequested(t) {
		t.Error("a restarted run was not stopped")
	}
	filed := f.filed(t)
	if len(filed) != 1 {
		t.Fatalf("the restart filed %d item(s), want one per reason: %+v", len(filed), filed)
	}
	for _, w := range filed {
		if w.Approved != nil {
			t.Errorf("the daemon approved the item it filed: %+v", w)
		}
		if strings.Contains(w.ID, ":") || !strings.Contains(w.ID, triageRunID) {
			t.Errorf("the filed item is named %q, want a name derived from the run", w.ID)
		}
		if w.Phase != orchestrator.PhaseQueued && w.Phase != "" {
			t.Errorf("the filed item is in phase %q, want work waiting to be approved", w.Phase)
		}
		if w.Repo != "acme/app" || w.Brief == "" || w.Title == "" {
			t.Errorf("the filed item is not a scope a person could approve: %+v", w)
		}
	}
	if got := f.line(t).Text; !strings.Contains(got, string(orchestrator.ReasonNoCommits)) {
		t.Errorf("the line is %q, and the evidence was %s", got, orchestrator.ReasonNoCommits)
	}
	if got := f.merges(t); len(got) != 0 {
		t.Errorf("a run that pushed nothing was merged: %+v", got)
	}
}

// TestTriageIsWrittenToTheAuditLog: every outcome is one line under the run's
// own derived id, with what the rule decided and why — the record a level
// store cannot reconstruct.
func TestTriageIsWrittenToTheAuditLog(t *testing.T) {
	f := newTriageFixture(t, green())
	f.findings(t, `{"severity":"critical","path":"internal/creds/store.go","key":"k1"}`)
	f.step(t)

	e := f.line(t)
	if e.Subject != triageRunID || e.Transition != "triage" || e.ID != triageRunID+":triage" {
		t.Errorf("the entry is %+v, want the run's own triage id", e)
	}
	if !e.At.Equal(orchestratorTestNow) {
		t.Errorf("the entry is stamped %s, want the server's clock (%s)", e.At, orchestratorTestNow)
	}
	text := e.Text
	for _, want := range []string{string(orchestrator.Escalate), string(orchestrator.ReasonCriticalFinding)} {
		if !strings.Contains(text, want) {
			t.Errorf("the line %q does not carry %s", text, want)
		}
	}
	if len(f.audit(t)) != 1 {
		t.Errorf("the log carries %d entries for one triaged run: %+v", len(f.audit(t)), f.audit(t))
	}
}

// TestTriageAppendsOnceAcrossTicks: a repeated cycle over the same run appends
// nothing and acts on nothing — the line is the fence, so a run is triaged
// once however many cycles go by.
func TestTriageAppendsOnceAcrossTicks(t *testing.T) {
	f := newTriageFixture(t, green())
	f.findings(t, `{"severity":"critical","path":"internal/agent/stage.go","key":"k1"}`)

	first := f.step(t)
	if first.Triaged != 1 {
		t.Fatalf("the first cycle reports %d triaged run(s), want one", first.Triaged)
	}
	before := len(f.audit(t))
	for i := 0; i < 3; i++ {
		rep := f.step(t)
		if rep.Triaged != 0 {
			t.Fatalf("cycle %d triaged the run again", i+2)
		}
	}
	if got := len(f.audit(t)); got != before {
		t.Errorf("the log carries %d entries after four cycles, want %d", got, before)
	}
	if got := len(f.filed(t)); got != 0 {
		t.Errorf("a repeated cycle filed work: %+v", got)
	}
	if got := f.state(t).Decisions; len(got) != 1 {
		t.Errorf("a repeated cycle raised %d questions, want the one: %+v", len(got), got)
	}
	if got := f.merges(t); len(got) != 0 {
		t.Errorf("a repeated cycle merged: %+v", got)
	}
}

// TestTriageReadsTheFindingsFileFromTheRunsArtifactNotTheCheckout: the
// findings are the runner's finalize artifact in the run's own storage, and a
// file of that name anywhere near the daemon's working directory is not
// evidence — an unreadable findings input is recommend-only, which is what
// keeps a run the triage cannot see alive.
func TestTriageReadsTheFindingsFileFromTheRunsArtifactNotTheCheckout(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// The file that would escalate this run, where a checkout would put it.
	checkout := `{"version":1,"run":"` + triageRunID + `","findings":[{"severity":"critical","path":"internal/creds/store.go","key":"k1"}]}`
	if err := os.WriteFile(filepath.Join(dir, triageFindingsObject), []byte(checkout), 0o600); err != nil {
		t.Fatal(err)
	}

	f := newTriageFixture(t, green())
	// A run the rule would restart if it could read the findings: it pushed
	// nothing, and BF1's artifact is absent.
	rec := triageRecord()
	rec.Status = runstore.StatusFailed
	rec.Stage = "code"
	rec.PushedHead = ""
	rec.ChangedFiles = &runstore.ChangedFiles{Paths: []string{}, Total: 0}
	f.putRecord(t, rec)

	in := f.input(t)
	if in.Findings.Read != orchestrator.FindingsAbsent {
		t.Fatalf("the read reports the findings as %q, and the run wrote none in its own storage", in.Findings.Read)
	}
	f.step(t)
	if f.cancelRequested(t) {
		t.Error("a run whose findings nobody could read was stopped, and a file in the working directory is not its findings")
	}
	if len(f.filed(t)) != 0 {
		t.Errorf("a run whose findings nobody could read filed work: %+v", f.filed(t))
	}
	if got := f.line(t).Text; !strings.Contains(got, string(orchestrator.ReasonFindingsAbsent)) {
		t.Errorf("the line is %q, and the reason it was recommended on is %s", got, orchestrator.ReasonFindingsAbsent)
	}
}

// TestTriageEscalateRaisesAPersonDecision: an escalation stops the run and
// asks a person, through the same decision the rest of the loop raises — and
// the question is on the node, so a daemon that restarted asks nothing twice.
func TestTriageEscalateRaisesAPersonDecision(t *testing.T) {
	f := newTriageFixture(t, green())
	f.findings(t, `{"severity":"high","path":"internal/agent/stage.go","key":"k1"}`)

	f.step(t)
	d, ok := f.state(t).Decision(triageRunID + "-triage")
	if !ok {
		t.Fatalf("an escalated run asked nothing: %+v", f.state(t).Decisions)
	}
	if d.WaitingOn != orchestrator.OnPerson || d.Resolved() {
		t.Errorf("the question is %+v, want one waiting on a person", d)
	}
	if d.Question == "" {
		t.Error("the question carries no text, and a decision nobody can read is not one")
	}
	if !f.cancelRequested(t) {
		t.Error("an escalated run was not stopped")
	}
	if !strings.Contains(f.out.String(), string(orchestrator.NotifyDecisionNeeded)) || !strings.Contains(f.out.String(), d.ID) {
		t.Errorf("the daemon said nothing about the question it raised: %q", f.out.String())
	}
	// The decision is on the node, so the run's line is what a restarted
	// daemon reads: a second cycle over it asks nothing again.
	f.step(t)
	if got := f.state(t).Decisions; len(got) != 1 {
		t.Errorf("a second cycle left %d questions on the node, want one: %+v", len(got), got)
	}
}

// TestTriageRecommendOnlyActsOnNothing: the fail-closed outcome writes the
// audit line and nothing else — no merge, no cancel marker, no state write —
// even for a run the rule would otherwise restart.
func TestTriageRecommendOnlyActsOnNothing(t *testing.T) {
	f := newTriageFixture(t, green())
	rec := triageRecord()
	rec.Status = runstore.StatusFailed
	rec.Stage = "code"
	rec.PushedHead = ""
	rec.ChangedFiles = &runstore.ChangedFiles{Paths: []string{}, Total: 0}
	f.putRecord(t, rec)
	// Every writer the cycle could reach is wired to fail: a merge that lands,
	// a marker that is written, or a state the node takes all end this test.
	f.triage.open = gitprov.Static(anyCallFails{Provider: f.prov, t: t})
	before := f.db.Value(orchestratorPath(t, orchestrator.StateNode))

	f.step(t)
	if f.cancelRequested(t) {
		t.Error("the cancel marker was written by an outcome that acts on nothing")
	}
	if len(f.filed(t)) != 0 {
		t.Errorf("an outcome that acts on nothing filed work: %+v", f.filed(t))
	}
	if got := f.state(t).Decisions; len(got) != 0 {
		t.Errorf("an outcome that acts on nothing raised a question: %+v", got)
	}
	after := f.db.Value(orchestratorPath(t, orchestrator.StateNode))
	if !reflect.DeepEqual(before, after) {
		t.Errorf("the node moved under an outcome that acts on nothing:\n%v\n%v", before, after)
	}
	if got := f.line(t).Text; !strings.Contains(got, string(orchestrator.RecommendOnly)) {
		t.Errorf("the line is %q, want the outcome the rule took", got)
	}
}

// anyCallFails is the host as a test fences it: any write call fails the test
// rather than being made, so a cycle that reaches one is caught at the call.
type anyCallFails struct {
	gitprov.Provider
	t *testing.T
}

func (a anyCallFails) Merge(context.Context, int, string) error {
	a.t.Error("the cycle merged, and this outcome acts on nothing")
	return nil
}

func (a anyCallFails) MarkReady(context.Context, int, string) error {
	a.t.Error("the cycle marked a pull request ready, and this outcome acts on nothing")
	return nil
}
