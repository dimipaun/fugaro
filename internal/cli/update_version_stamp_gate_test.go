package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/initflow"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/versionstamp"
)

// update_version_stamp_gate_test.go is the managed gate's half of the
// required-CLI stage (T2b, D3267): the stage is one a managed project's coding
// agent session reaches, the document the gate judges is the exact bytes the
// one conditional PUT writes, the record of an admitted raise names that
// document's digest and the binary's own release, a lost race records nothing
// and retries nothing, and a release lookup that fails leaves the node exactly
// where it was.
//
// The release bound is answered over a local endpoint and never by stubbing the
// seam's answer (update_version_stamp_test.go's publishedSeam): what these
// tests are about is what the stage does with updatecheck.Published's real
// answer, so the seam keeps its production wiring and only the endpoint moves
// (D3269).

// stampReleaseAnswers points the stage's release bound at a server of this
// test's own, which answers GitHub's own release object for the tag it is asked
// about — the tag that was asked for, neither a draft nor a pre-release — with
// the status the test chose. The production endpoint is GitHub's and no test
// reaches it; a loopback server is reachable through the run's own client
// because Go's proxy handling never proxies 127.0.0.1, so what the test
// replaces is where the question is asked and never whether it is.
func stampReleaseAnswers(t *testing.T, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, rq *http.Request) {
		tag, ok := strings.CutPrefix(rq.URL.Path, "/tags/")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
		}
	}))
	t.Cleanup(srv.Close)
	old := stampReleaseEndpoint
	stampReleaseEndpoint = srv.URL
	t.Cleanup(func() { stampReleaseEndpoint = old })
}

// stampPutBody is the document the run's one conditional PUT left at the node,
// as the database fake received it: the bytes the gate judged are those bytes
// (D1022), so their digest is what the admission's record has to carry.
func stampPutBody(t *testing.T, db *gcpfake.RTDB) []byte {
	t.Helper()
	for _, rq := range db.Requests() {
		if rq.Method != "GET" && strings.TrimSuffix(rq.Path, ".json") == "/"+versionstamp.Path {
			return rq.Body
		}
	}
	t.Fatalf("nothing was written to %s, so there is no document the gate judged", versionstamp.Path)
	return nil
}

// stampRecordRule is the head of the second of the stage's admitted rules: the
// half that makes the record this stage's own rather than another step's (the
// first is the ownership rule every writer into the database answers), up to
// the release it names. A record that carries it is the required-CLI step's,
// and a test reads it as an owner would — off the step's own rule rather than
// off a stage name the record does not carry.
func stampRecordRule() string {
	return strings.SplitN(versionStampAdmittedRules(releaseVersion())[1], ":", 2)[0]
}

// TestUpdateManagedGatesTheVersionStampStage is this task's red step: a managed
// project, a coding agent's session, and a required CLI below this binary's.
// The node the stage writes is the release every member of the project has to
// run, so before the gate the stage was one a session stopped at — it is
// registered with the stages that write what a deployed reader reads. With its
// entry in gatedStages and its own gateStep it is judged like the rules
// generation and the policy node, and an admitted step raises the node without
// the confirmation the verdict replaces.
func TestUpdateManagedGatesTheVersionStampStage(t *testing.T) {
	r, db := updateBudgetRig(t)
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	isolateGateCredentials(t)
	// The rig's world plans an additive change, which is what lets the steps in
	// front of the stamp run to their applies (see the rules stage's test): a
	// step that plans nothing stops the run before this one is reached.
	updating(t, r)
	r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))
	stampReleaseAnswers(t, http.StatusOK)

	out, errOut, err := executeStdin(t, "", "update")
	got := out + errOut
	if err != nil {
		t.Fatalf("a managed project's agent session stopped before the required CLI was raised (%v):\n%s", err, got)
	}
	if !strings.Contains(got, initflow.AgentProceeded("CLAUDECODE")) {
		t.Errorf("the admitted required-CLI step did not say it proceeded:\n%s", got)
	}
	e := stampEngine(t, r, &initOptions{door: updateDoor.name})
	stamp, present := stampOn(t, e)
	if !present {
		t.Fatalf("the update wrote no required CLI into %s:\n%s", versionstamp.Path, got)
	}
	if want := releaseVersion(); stamp.Min != want {
		t.Errorf("the node names %s, want this binary's own release %s:\n%s", stamp.Min, want, got)
	}
	if n := stampNodeWrites(db); n != 1 {
		t.Errorf("the node was written %d times, want the one conditional PUT a raise is:\n%s", n, got)
	}
	// The step is the run's own and not one left for the owner: the verdict
	// stood in for the confirmation, and the account says the step applied.
	line := stepLineOf(reportLines(got+newestLog(t, "update")), initflow.VersionStamp)
	if line == "" {
		t.Fatalf("the account holds no required-CLI step:\n%s", got)
	}
	if !strings.HasPrefix(line, markDone) {
		t.Errorf("the required-CLI step reads %q, want a step this run applied: the gate admitted the document it judged\n%s", line, got)
	}
	if !strings.Contains(line, "required CLI: none → "+releaseVersion()) {
		t.Errorf("the step does not say which fence it moved: %q", line)
	}
}

// TestAgentSessionStampRaiseWritesTheAuditRecord: the admission's record is
// written after the step's own write and never before it (D1014), and what it
// carries is the document that landed and the binary that composed it — the
// digest of the exact bytes the conditional PUT put at the node, and the rule
// that names this release, which is the whole of what the step requires of the
// project.
func TestAgentSessionStampRaiseWritesTheAuditRecord(t *testing.T) {
	r, db := updateBudgetRig(t)
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	isolateGateCredentials(t)
	updating(t, r)
	r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))
	stampReleaseAnswers(t, http.StatusOK)

	out, errOut, err := executeStdin(t, "", "update")
	got := out + errOut
	if err != nil {
		t.Fatalf("a managed project's agent session did not raise the required CLI (%v):\n%s", err, got)
	}

	want := gateDigest(stampPutBody(t, db))
	rule := stampRecordRule()
	var found []managedmode.AuditRecord
	for _, rec := range audits(t, r) {
		for _, got := range rec.Rules {
			if strings.Contains(got, rule) {
				found = append(found, rec)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d record(s) carry the required-CLI step's own rule, want one: %+v\n%s", len(found), audits(t, r), got)
	}
	rec := found[0]
	if rec.Verdict != managedmode.AuditAdmitted {
		t.Errorf("the required-CLI step's record reads %q, want an admission", rec.Verdict)
	}
	if rec.Command != "fugaro update" {
		t.Errorf("the record names the command %q", rec.Command)
	}
	if rec.PlanDigest != want {
		t.Errorf("the record's digest is %q, want the digest of the document this run wrote (%s): the record has to name the bytes the gate judged (D1022)", rec.PlanDigest, want)
	}
	if !strings.Contains(strings.Join(rec.Rules, "\n"), releaseVersion()) {
		t.Errorf("no rule of the record names the release this binary carries (%s), and the step is the one that requires it of every member: %+v", releaseVersion(), rec.Rules)
	}
	// The digest is of this step's document and not of another step's: the rules
	// generation is the one other document of this run whose digest the test can
	// compute itself.
	gen, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if rec.PlanDigest == gateDigest(gen) {
		t.Errorf("the record carries the rules generation's digest under the required-CLI step's rules")
	}
}

// TestGatedLostRaceRecordsNothingAndDoesNotRetry: the gate admits the document
// the stage is about to write, and another writer lands between the read that
// took the ETag and the PUT conditional on it. The write is refused, and nothing
// is recorded for it (D1014): the record is the gate's account of a write that
// happened, and one saying "admitted" about a write that never happened is worse
// than no record at all. Nothing is retried either — the read is not made again,
// so a change nobody read is not suppressed.
func TestGatedLostRaceRecordsNothingAndDoesNotRetry(t *testing.T) {
	r, db := updateBudgetRig(t)
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	isolateGateCredentials(t)
	stampReleaseAnswers(t, http.StatusOK)

	e := stampEngine(t, r, &initOptions{door: updateDoor.name, yes: true})
	// The run reads its gate once, before the loop's first stage (gateRun);
	// the stage asks it itself (gateUnderway), so the judgement is made exactly
	// where the run makes it.
	e.r.gate = readUpdateGate(e, e.r)
	if !e.r.gateUnderway() {
		t.Fatal("the fixture's project is not managed, so nothing here is judged by the gate")
	}
	s := newVersionStampStage(e)

	// Apply makes two requests to the node: the read that takes the ETag and the
	// PUT conditional on it. Holding both puts another writer exactly between
	// them.
	stampedRead, releaseRead := db.HoldNext(versionstamp.Path)
	stampedWrite, releaseWrite := db.HoldNext(versionstamp.Path)
	var outcome initflow.Outcome
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		outcome, err = s.Apply(context.Background(), initflow.Env{Yes: true})
	}()
	<-stampedRead
	releaseRead()
	<-stampedWrite
	seedUpdateStamp(t, db, "9.9.9")
	releaseWrite()
	<-done

	if err != nil {
		t.Fatalf("a lost race failed the run: %v", err)
	}
	for _, want := range []string{"changed while this update was running", "nothing was written"} {
		if !strings.Contains(outcome.Detail, want) {
			t.Errorf("the line does not say %q: %q", want, outcome.Detail)
		}
	}
	stamp, present := stampOn(t, e)
	if !present || stamp.Min != "9.9.9" {
		t.Errorf("the node holds %+v, want the other writer's 9.9.9: the lost race wrote something", stamp)
	}
	if s.p != nil && s.p.wroteIt {
		t.Error("the stage recorded a write that did not land")
	}
	if got := stampNodeWrites(db); got != 1 {
		t.Errorf("the node was written %d times, want the one refused PUT: a lost race is not retried", got)
	}
	rule := stampRecordRule()
	for _, rec := range audits(t, r) {
		for _, got := range rec.Rules {
			if strings.Contains(got, rule) {
				t.Errorf("a write that never landed left a record: %+v", rec)
			}
		}
	}
}

// TestStampStaysWhenTheReleaseLookupFails: the raise is bounded to a release the
// releases API answers for (D3269), and the two answers that are not "yes" leave
// the node exactly where it was — a lookup that cannot be made at all, and a
// version the API does not have. The person reads one line and the reason whole
// is in the run's log: what a failed lookup carries is a URL and a dial error,
// and neither belongs on the screen of an update that did the rest of its work.
func TestStampStaysWhenTheReleaseLookupFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   string
	}{
		{"the API cannot be read", http.StatusInternalServerError, "could not be read"},
		{"the release is not published", http.StatusNotFound, "is not a published release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, db := stampRig(t)
			stampReleaseAnswers(t, tc.status)

			res, out, err := runStampStage(t, r, &initOptions{door: updateDoor.name, yes: true})
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			line := res.Stages[0].Detail
			if !strings.Contains(line, tc.want) {
				t.Errorf("the line is %q, want %q in it", line, tc.want)
			}
			if !strings.Contains(line, "the required CLI is left as it is") {
				t.Errorf("the line does not say the node was left where it was: %q", line)
			}
			if n := len(line); n > withheldLineMax {
				t.Errorf("the line is %d characters, and a line a person reads is about %d: %q", n, withheldLineMax, line)
			}
			if strings.Contains(line, "http") {
				t.Errorf("the line carries the endpoint or the network error: %q", line)
			}
			if got := stampNodeWrites(db); got != 0 {
				t.Errorf("the node was written %d times over a release the bound would not confirm", got)
			}
			e := stampEngine(t, r, &initOptions{door: updateDoor.name})
			if _, present := stampOn(t, e); present {
				t.Error("a stamp was written for a release the bound did not confirm")
			}
		})
	}
}

// TestPolicyDeleteKeepsTheStamp: `fugaro project policy publish --delete` takes
// the published policy away — the repos children it wrote and the shared node —
// and it never takes config/policy whole (projectpolicy.PathRepos). The required
// CLI is a third node under that prefix, and it is not the policy's to delete:
// the deletion is one project's answer about what its runs are admitted by, and
// the release every member has to run is not.
func TestPolicyDeleteKeepsTheStamp(t *testing.T) {
	f := newProjectFixture(t, "")
	f.db.Set(projectpolicy.PathShared(), staleBudgetNode())
	f.db.Set(versionstamp.Path, map[string]any{
		"schema": versionstamp.SchemaVersion, "min": "0.2.0", "set_at": "2026-11-01T00:00:00Z",
	})

	fakeTerminal(t)
	out, _, err := executeStdin(t, "aurora\n", "project", "policy", "publish", "--delete")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if v := f.db.Value(projectpolicy.PathShared()); v != nil {
		t.Errorf("--delete left the policy node: %v", v)
	}
	if v := f.db.Value(versionstamp.Path); v == nil {
		t.Error("--delete took the project's required CLI away with the policy: the fence is not the policy's")
	}
}
