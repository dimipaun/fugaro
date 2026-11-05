package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// update_managed_test.go: `fugaro update`'s steps under the managed gate (M1,
// D1016). What each test is for is said where it stands; the shape they share
// is updateRig (a release, a state bucket, a checkout-less working directory)
// in a managed project — both marks — with a coding agent's session present,
// which is the only session in which the gate is read at all.
//
// Every one of them is hermetic: gcpfake's GCS, Resource Manager and Secret
// Manager, the fake terraform on PATH, t.TempDir(), and credentials read off a
// path that does not exist so a developer's own Application Default
// Credentials can never decide what a record names.

// updateManagedRig is updateRig in a managed project with a coding agent's
// session: the two marks (the runs bucket's object and the project's label),
// the credentials isolated, and the marker set.
func updateManagedRig(t *testing.T) *initRig {
	t.Helper()
	r := updateRig(t)
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	isolateGateCredentials(t)
	return r
}

// isolateGateCredentials is what a record is written with: no credentials file
// to read an address out of, and the person the credentials in force belong to
// answered by this test rather than by a network call. The day is fixed too, so
// the records a test reads back are the day's own.
func isolateGateCredentials(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(dir, "no-application-default-credentials.json"))
	t.Setenv("CLOUDSDK_CONFIG", filepath.Join(dir, "gcloud"))
	oldUser := authenticatedUser
	authenticatedUser = func(context.Context, *localcfg.Config) (string, error) { return managedGateUser, nil }
	t.Cleanup(func() { authenticatedUser = oldUser })
	oldNow := managedGateNow
	managedGateNow = func() time.Time { return managedGateTestNow }
	t.Cleanup(func() { managedGateNow = oldNow })
	// And the program's name, which a record's command field starts with: the
	// test binary's own path is not what any of these assertions are about.
	oldSelf := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = oldSelf })
}

// updating makes the run behave as an update of an installation that has work
// to do: the plan reports changes, which is what puts a step in front of the
// gate at all.
func updating(t *testing.T, r *initRig) {
	t.Helper()
	r.script["plan"] = map[string]any{"exit": 2}
	r.save(t)
}

// updateRun is one `fugaro update` in a rig's world, as one string: the
// account it printed, whatever went to stderr, and the error's own text.
func updateRun(t *testing.T, r *initRig, args ...string) (string, error) {
	t.Helper()
	out, errOut, err := executeStdin(t, "", args...)
	if err != nil {
		out += ErrorText(err)
	}
	return out + errOut, err
}

// audits reads the day's records back through the reader a surface uses, so a
// test asserts about what doctor and report would print.
func audits(t *testing.T, r *initRig) []managedmode.AuditRecord {
	t.Helper()
	got, err := managedmode.ListAuditRecords(context.Background(), managedStorage(t, r.gcs), initRunsBucket, managedmode.AuditDate(managedGateTestNow))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// M1's red step. A managed project, a coding agent's session, and a plan that
// removes an address the owner's list does not name: today the run is refused
// before the stage is reached — the session reads the coding agent's own
// refusal, and nothing about the plan — and after this task the refusal is the
// GATE's, with the gate's own sentence, because the plan is what refused it.
//
// The three assertions are the three halves of that: the gate's sentence is
// what the session reads, the guard's own managed advice is not (a session
// that read that one would be told to hand the step over rather than what
// about the plan was refused), and nothing was applied.
func TestUpdateManagedStageRefusesARemoval(t *testing.T) {
	const a = "user:a@example.com"
	address := `module.installation.google_storage_bucket_iam_member.state["` + a + `"]`
	r := updateManagedRig(t)
	updating(t, r)
	r.setPlan(t, grantChange("state", a, "roles/storage.objectAdmin", "delete"))

	got, err := updateRun(t, r, "update", "--yes")
	if err == nil {
		t.Fatalf("the run applied a plan a managed project refuses:\n%s", got)
	}
	if !strings.Contains(got, managedmode.NotConverged) {
		t.Errorf("the session does not read the gate's own sentence:\n%s", got)
	}
	if !strings.Contains(got, address) {
		t.Errorf("the gate's refusal does not name the address it refused:\n%s", got)
	}
	if strings.Contains(got, initManagedDeleteAdvice) {
		t.Errorf("the session reads the guard's managed advice, which is not this refusal:\n%s", got)
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("%d terraform apply(s) ran: the gate refused the plan and nothing may be applied", n)
	}
	if code := ExitCode(err); code != ExitUserError {
		t.Errorf("exit %d, want %d: a refusal is the user's and not a failure", code, ExitUserError)
	}
	// And the refusal is on the record (D1014, M6): the gate judged a document
	// and refused it, and a refusal nobody can read afterwards is half of the
	// audit this design exists for.
	recs := audits(t, r)
	if len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("%d record(s) after a refused step, want one refused: %+v", len(recs), recs)
	}
	if !strings.Contains(strings.Join(recs[0].Rules, "\n"), address) {
		t.Errorf("the record does not name the rule that refused, so an owner cannot audit it: %+v", recs[0])
	}
}

// The half of the pair the removal above does not reach: a plan the GUARD
// admits and the COVER refuses is refused all the same, and refused by the
// gate. The guard is only one of the two gates a managed plan is read by
// (managed.Spec hands the same document to both), and a step wired to the
// guard alone would apply a plan the review's own cover would have stopped —
// a grant to a member the review never listed, which is what this plan is.
//
// M1's SEC shape is the same one from the other side: the gate's refusal is
// what the session reads (never the confirmation the step would have asked),
// nothing is applied, and the rule that fired is on the record, so an owner
// can read which rule stopped an unattended run.
func TestUpdateManagedStageRefusesAPlanTheCoverDoesNotCover(t *testing.T) {
	const outsider = "user:not-listed@example.com"
	r := updateManagedRig(t)
	updating(t, r)
	r.setPlan(t, grantChange("state", outsider, "roles/storage.objectViewer", "create"))

	got, err := updateRun(t, r, "update", "--yes")
	if err == nil {
		t.Fatalf("the run applied a plan the cover of its own review refuses:\n%s", got)
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("%d terraform apply(s) ran: the gate refused the plan and nothing may be applied", n)
	}
	if !strings.Contains(got, managedmode.NotConverged) {
		t.Errorf("the session does not read the gate's own sentence:\n%s", got)
	}
	recs := audits(t, r)
	if len(recs) != 1 || recs[0].Verdict != managedmode.AuditRefused {
		t.Fatalf("%d record(s) after a refused plan, want one refused: %+v", len(recs), recs)
	}
	rules := strings.Join(recs[0].Rules, "\n")
	// "cover" is the word internal/managed's own verdict uses for this half of
	// the gate (its coverGate); it is a literal here because the constant is
	// that package's.
	if !strings.Contains(rules, "cover: ") || !strings.Contains(rules, outsider) {
		t.Errorf("the record does not name the cover as the rule that refused, so an owner cannot audit it: %+v", recs[0])
	}
}

// The other side of the same gate, and the one the HARD RULE needs: an
// additive plan in the same world is admitted, the step proceeds in the
// session that may confirm nothing, the run's reader sees the sentence that
// says so (initflow.AgentProceeded), and the admission is recorded with the
// digest of exactly the document the rules judged (D1022).
func TestUpdateManagedStageAdmitsAnAdditivePlan(t *testing.T) {
	const additive = "module.installation.google_service_account.scheduler"
	r := updateManagedRig(t)
	updating(t, r)
	r.setPlan(t, change(additive, "create"))

	got, err := updateRun(t, r, "update", "--yes")
	if !strings.Contains(got, initflow.AgentProceeded("CLAUDECODE")) {
		t.Fatalf("the admitted step does not say it proceeded:\n%s", got)
	}
	if n := len(r.ran(t, "apply")); n == 0 {
		t.Fatalf("the gate admitted the plan and the step applied nothing (err %v):\n%s", err, got)
	}
	if strings.Contains(got, managedmode.NotConverged) {
		t.Errorf("an additive plan was refused by the gate:\n%s", got)
	}
	recs := audits(t, r)
	if len(recs) != 1 || recs[0].Verdict != managedmode.AuditAdmitted {
		t.Fatalf("%d record(s) after an admitted step, want one admitted: %+v", len(recs), recs)
	}
	// The digest is of the bytes the rules read, which for this rig is the
	// plan terraform printed — and the record is written only after the apply,
	// which is the ordering D1014 asks for.
	if doc, _ := r.script["show"].(map[string]any)["stdout"].(string); doc != "" {
		if want := gateDigest([]byte(doc)); recs[0].PlanDigest != want {
			t.Errorf("the record's digest is %s, want the digest of the plan the rules read (%s)", recs[0].PlanDigest, want)
		}
	}
	if recs[0].Command != "fugaro update" {
		t.Errorf("the record names the command %q, want %q", recs[0].Command, "fugaro update")
	}
	if _, held := os.LookupEnv("CLAUDECODE"); !held {
		t.Fatal("this test is not a coding agent's session")
	}
}

// markReads is how many calls READ the runs bucket's managed marker object,
// however the client spelled the path: the gate's own read is one, and a run
// that never reads the marks makes none.
//
// Reading, not naming: the rig's own setup uploads the object, and an upload is
// not a read — counting it would make every world here look like a world in
// which the gate was consulted.
func markReads(r *initRig) int {
	n := 0
	for _, rq := range r.gcs.Requests() {
		if rq.Method != http.MethodGet {
			continue
		}
		if strings.Contains(rq.Path, "managed.json") || strings.Contains(rq.Query, "managed.json") {
			n++
		}
	}
	return n
}

// theMarkerObject writes the runs bucket's half of the fact and leaves the
// project's half (the label) alone: half of the fact is not the fact, which is
// the world TestUpdateReadsTheGateBeforeAnyWrite's third arm runs in.
func theMarkerObject(t *testing.T, r *initRig, gcp string) {
	t.Helper()
	body := `{"version":1,"gcp_project":"` + gcp + `"}`
	if err := r.gcs.Bucket(t, initRunsBucket).Bucket.WriteAll(context.Background(), managed.MarkerObject, []byte(body), nil); err != nil {
		t.Fatal(err)
	}
}

// The wiring property (spec §8.5): the gate is read BEFORE the step writes, and
// the one shape that can witness it is the same run in two worlds. The plan,
// the session and the command line are the same; the only thing that moves is
// what the two marks say — and the write's very existence moves with them. A
// command that wrote first and asked afterwards, or that asked only where a
// clean answer was already in hand, cannot pass both halves.
//
// The three worlds that write nothing are the three ways a mark is not the
// fact: the object gone, the label gone, and the object present but unreadable.
// The last is the read blip M6's review binds this to (a transient GCS or CRM
// failure must fail closed and never read as "not managed"), and all three are
// the zero verdict's own answer: today's refusal, in the session's own words.
func TestUpdateReadsTheGateBeforeAnyWrite(t *testing.T) {
	const additive = "module.installation.google_service_account.scheduler"

	t.Run("both marks, and the step writes", func(t *testing.T) {
		r := updateManagedRig(t)
		updating(t, r)
		r.setPlan(t, change(additive, "create"))

		got, err := updateRun(t, r, "update", "--yes")
		if !strings.Contains(got, initflow.AgentProceeded("CLAUDECODE")) {
			t.Fatalf("the step the gate admitted did not say so (err %v):\n%s", err, got)
		}
		if n := len(r.ran(t, "apply")); n == 0 {
			t.Fatalf("the gate admitted the plan and the step applied nothing:\n%s", got)
		}
		// The read is what the write hangs on, and it is this run's own.
		if n := markReads(r); n == 0 {
			t.Error("the run applied a plan without ever reading the runs bucket's managed marker: the write is not gated on the read")
		}
		if recs := audits(t, r); len(recs) != 1 || recs[0].Verdict != managedmode.AuditAdmitted {
			t.Errorf("the admitted step left %+v, want one admitted record", recs)
		}
	})

	// The two worlds in which the marks are not the fact, one half each: the
	// step writes nothing, and the session reads the refusal it always read.
	for _, tc := range []struct {
		name  string
		world func(t *testing.T, r *initRig)
	}{
		{"no object", func(t *testing.T, r *initRig) {
			r.markedRuns(t, initProjectName, initProject)
			labelled(r.crm, initProject)
		}},
		{"no label", func(t *testing.T, r *initRig) {
			r.markedRuns(t, initProjectName, initProject)
			theMarkerObject(t, r, initProject)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := updateRig(t)
			tc.world(t, r)
			t.Setenv("CLAUDECODE", "1")
			isolateGateCredentials(t)
			updating(t, r)
			r.setPlan(t, change(additive, "create"))

			got, err := updateRun(t, r, "update", "--yes")
			if n := len(r.ran(t, "apply")); n != 0 {
				t.Errorf("%d terraform apply(s) ran in a session whose gate was never opened:\n%s", n, got)
			}
			if !strings.Contains(got, initflow.AgentRefusal("CLAUDECODE")) {
				t.Errorf("the session did not read the refusal it has always read:\n%s", got)
			}
			if strings.Contains(got, initflow.AgentProceeded("CLAUDECODE")) {
				t.Errorf("the step proceeded on a mark that is not the fact:\n%s", got)
			}
			if recs := audits(t, r); len(recs) != 0 {
				t.Errorf("a step nothing judged left %+v: the record is the gate's", recs)
			}
			if err == nil {
				t.Error("the run exited 0 with the step left for the user")
			}
		})
	}

	// And the third way a mark is not the fact, which is M6's own bind (a
	// transient read that fails must fail closed): the object is there and the
	// read of it answers 5xx. Nothing is applied — the run STOPS on the read
	// rather than reading the failure as "not managed" and going on.
	t.Run("the object cannot be read", func(t *testing.T) {
		r := updateRig(t)
		r.managedRuns(t, initProjectName, initProject)
		r.gcs.RefusePath("managed.json", http.StatusInternalServerError, "INTERNAL", "", "backend error")
		t.Setenv("CLAUDECODE", "1")
		isolateGateCredentials(t)
		updating(t, r)
		r.setPlan(t, change(additive, "create"))

		got, err := updateRun(t, r, "update", "--yes")
		if err == nil {
			t.Fatalf("a mark that could not be read was read as an answer:\n%s", got)
		}
		if n := len(r.ran(t, "apply")); n != 0 {
			t.Errorf("%d terraform apply(s) ran while the marks could not be read:\n%s", n, got)
		}
		if strings.Contains(got, initflow.AgentProceeded("CLAUDECODE")) {
			t.Errorf("the step proceeded on a read that failed:\n%s", got)
		}
		if !strings.Contains(got, "could not be read") {
			t.Errorf("the run does not say that the read is what stopped it:\n%s", got)
		}
		if recs := audits(t, r); len(recs) != 0 {
			t.Errorf("a step nothing judged left %+v: the record is the gate's", recs)
		}
	})
}

// A project the owner unflags while the run is in flight is a project that is
// not managed from that moment on, and the confirmations come back with it —
// which is a thing a terminal can witness and a coding agent's session cannot.
// The run's own read (gateRun, before the first stage) finds both marks; the
// helper's second read, immediately before the first step's write (D1024),
// finds the label gone.
//
// The state is arranged rather than raced, because the two reads are one
// function apart: the gate is built by the read the run itself makes, the label
// is then removed underneath it, and the gate that read left is handed to the
// run. That is exactly the pair of facts the bug's window holds — a run whose
// read said "managed" in a project that is not managed any more — and what the
// run does next is the whole assertion: the step's own confirmation is asked
// (the project's name at the review) with nothing on stdin, so nothing is
// applied. A run that kept the first read's answer never asks, prints the
// gate's own sentence, and applies the plan with nothing typed.
//
// The mutation this is for: dropping the second half of initGate.underway, so a
// revoked run goes on standing in for the confirmations of a project that is
// not managed.
func TestUpdateRevokedMidRunKeepsTheConfirmations(t *testing.T) {
	const additive = "module.installation.google_service_account.scheduler"
	releaseBuild(t, "0.2.0")
	r := updateRig(t)
	r.managedRuns(t, initProjectName, initProject)
	// The runs bucket's project Viewers have no grant to lose here, so the one
	// step of this run that is not a Terraform plan — the removal of that
	// access, which is asked with the run's own confirmation before the first
	// plan (init.go's runsBucketViewers) — is not a step at all. What is left
	// is the shape this test is about: the run's read, then the first gated
	// step, and nothing between them that could have taken the confirmation
	// already.
	r.gcs.SetBucketPolicy(initRunsBucket, nil)
	isolateGateCredentials(t)
	noAgentSession(t)
	fakeTerminal(t)
	updating(t, r)
	r.setPlan(t, change(additive, "create"))

	// The read the run makes before its first stage, in a project that is
	// managed at that moment: the gate a run in this window holds.
	gate, err := readManagedGate(context.Background(), managedGateEnv{
		lc:  &localcfg.Config{Name: initProjectName, GCPProject: initProject, RunsBucket: initRunsBucket},
		s:   managedStorage(t, r.gcs),
		crm: managedCRM(t, r.crm),
	}, managedGateFacts{Subcommand: "update", Declined: "nothing was applied"})
	if err != nil {
		t.Fatal(err)
	}
	if !gate.managed {
		t.Fatal("the rig's project did not read as managed, so there is no revocation for this test to make")
	}
	// The owner unflags it between that read and the write the first step is
	// admitted for.
	r.crm.SetLabels(initProject, map[string]string{managed.ProjectLabelKey: "no"})

	e := rigEngine(t, r, &initOptions{door: updateDoor.name})
	out := &syncBuf{}
	e.r.w = out
	e.r.gate = &initGate{g: gate, managed: true}
	_ = e.converge(t.Context())

	got := out.String()
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("%d terraform apply(s) ran in a project the second read found unmanaged:\n%s", n, got)
	}
	if strings.Contains(got, initflow.GateOrdinary()) || strings.Contains(got, initflow.GateProceeded()) {
		t.Errorf("the revoked run applied a step under the gate's own sentence:\n%s", got)
	}
	if !strings.Contains(got, "Type "+initProjectName) {
		t.Errorf("the revoked run asked nothing at the owner's own terminal:\n%s", got)
	}
	if e.r.gateUnderway() {
		t.Error("the gate is still underway after the read that found the project unmanaged")
	}
}

// A person's own run on a project that is NOT managed keeps every behaviour it
// had: --yes covers what it always covered, nothing asks a new question, and no
// record is written about a decision this design never made.
//
// The count of reads is where "keeps every behaviour" is measured. Two calls
// name the marker object in a person's run, and the second is the one this
// design adds: the gate reads the marks per command (D1015) in the owner's
// session too, because whether to show a prompt at all is the question those
// marks answer (the ruling of 2026-11-01). It is a read and nothing else — no
// verdict is reached for this run, nothing is applied on it, and the count is
// the whole of what it costs. A change that makes an owner's run read the mark
// a third time, or that makes this read reach a write, fails here.
func TestUpdateManagedLeavesAnOwnersRunAlone(t *testing.T) {
	const additive = "module.installation.google_service_account.scheduler"
	r := updateRig(t)
	r.markedRuns(t, "", "") // the runs bucket, and no mark of any kind in it
	updating(t, r)
	r.setPlan(t, change(additive, "create"))

	got, err := updateRun(t, r, "update", "--yes")
	if err != nil {
		t.Fatalf("an owner's run on an unmanaged project failed: %v\n%s", err, got)
	}
	if n := len(r.ran(t, "apply")); n == 0 {
		t.Errorf("--yes stopped covering the installation step:\n%s", got)
	}
	if n := markReads(r); n != 2 {
		t.Errorf("an owner's run read the managed marker %d time(s), want two: the installation's own fact read, and the one the gate makes to know whether to ask at all", n)
	}
	if strings.Contains(got, initflow.AgentProceeded("CLAUDECODE")) {
		t.Errorf("a person's run printed the coding agent's own sentence:\n%s", got)
	}
	if recs := audits(t, r); len(recs) != 0 {
		t.Errorf("an unmanaged project left %+v: a record is about a managed project's step (D1025)", recs)
	}
}

// The owner's own terminal on a managed project takes no confirmation either,
// and this is the ruling of 2026-11-01 in one test: "the whole point of a
// managed project is that YOU manage it on my behalf without all these
// prompts". The marks are read because this is exactly the session the run's
// one confirmation would have stopped, the gate judges each document as the
// stage reaches it, and the verdict is what applies it — the plan the stages
// print is still printed, and the admission is in the owner's own bucket.
//
// Nothing is typed: the terminal's stdin is empty, so a run that asked at all
// would stop where it asked (the typed name never arrives) and no apply would
// run. That is what makes "the apply ran" the assertion about the prompt.
func TestUpdateManagedTerminalAsksNothing(t *testing.T) {
	const additive = "module.installation.google_service_account.scheduler"
	r := updateRig(t)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	fakeTerminal(t)
	updating(t, r)
	r.setPlan(t, change(additive, "create"))

	got, err := updateRun(t, r, "update")
	if err != nil {
		t.Fatalf("a managed project's own terminal was refused: %v\n%s", err, got)
	}
	if n := len(r.ran(t, "apply")); n == 0 {
		t.Errorf("no apply ran, so the gate's verdict did not stand in for the confirmation:\n%s", got)
	}
	for _, no := range []string{"Type " + initProjectName + " to apply all of the above", "confirmed by --yes", "Type " + initProjectName + " to confirm"} {
		if strings.Contains(got, no) {
			t.Errorf("a managed project's own terminal printed the question %q:\n%s", no, got)
		}
	}
	if !strings.Contains(got, initflow.GateProceeded()) {
		t.Errorf("the run does not say what decided the step:\n%s", got)
	}
	if !strings.Contains(got, "Review: project ") {
		t.Errorf("the run took no confirmation and printed no review of what it would do:\n%s", got)
	}
	recs := audits(t, r)
	if len(recs) == 0 {
		t.Fatal("a managed project's own terminal applied a step and filed nothing: the record is the half of the safety the prompt used to carry")
	}
	admitted := 0
	for _, rec := range recs {
		if rec.Verdict == managedmode.AuditAdmitted {
			admitted++
		}
		if rec.AgentMarker != "" {
			t.Errorf("a record of a person's own run names the marker %q", rec.AgentMarker)
		}
	}
	if admitted == 0 {
		t.Errorf("the day holds no admission for a step that applied: %+v", recs)
	}
}

// The other half, and the control the whole rule is read against: the same run
// with the same empty terminal on a project that is NOT managed keeps every
// prompt exactly as it was — the run's one confirmation is asked, nothing is
// typed, and no stage applies. The mutations caught: initRun.gateUnderway
// answering true for every project, and Managed.Removes with it.
func TestUpdateManagedTerminalControlKeepsEveryPrompt(t *testing.T) {
	const additive = "module.installation.google_service_account.scheduler"
	r := updateRig(t)
	r.markedRuns(t, "", "")
	isolateGateCredentials(t)
	fakeTerminal(t)
	updating(t, r)
	r.setPlan(t, change(additive, "create"))

	got, err := updateRun(t, r, "update")
	if err == nil {
		t.Fatalf("a project that is not managed ran to completion with nothing typed at its terminal:\n%s", got)
	}
	// The stage's own question, which is the one this path asks: this rig has no
	// checkout, so the run's review is not the owner's to confirm and each stage
	// asks separately — exactly the sentence the docs carry ("a terminal run
	// still asks separately, never covered by this confirmation").
	if !strings.Contains(got, "Type "+initProjectName+" to apply to GCP project") {
		t.Errorf("the step's own confirmation was not asked:\n%s", got)
	}
	if strings.Contains(got, initflow.GateProceeded()) || strings.Contains(got, initflow.GateOrdinary()) {
		t.Errorf("a project that is not managed printed the managed gate's sentence:\n%s", got)
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("a project that is not managed applied %d steps with nothing confirmed", n)
	}
	if recs := audits(t, r); len(recs) != 0 {
		t.Errorf("a project that is not managed filed %+v", recs)
	}
}

// The plugin pin's write is one of the questions `fugaro update` asks on a
// managed project today — "Write this to .claude/settings.json? [y/N]" — and it
// goes the same way as the rest: the gate being underway is the whole of what
// admits the step, the diff is still printed, and nothing is typed. The stage
// is called directly, which is the call the loop makes for a step that is not
// SelfConfirming, and the stdin is EMPTY: a stage that asked would get no
// answer and write nothing, so the file being wired at all is the assertion
// about the question.
//
// The control is the same call with no gate: a terminal, the same empty stdin,
// the same diff, and the y/N — which is what keeps this from being a stage
// that simply stopped asking. The mutation caught: the wiring's managed branch
// taken on anything but the project's own marks (gateUnderway answering true
// for every project).
func TestUpdateManagedPluginWiringAsksNothing(t *testing.T) {
	releaseBuild(t, "0.2.0")
	for _, tc := range []struct {
		name    string
		managed bool
	}{
		{"managed", true},
		{"not managed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := wiringCheckout(t, settingsWithOthers)
			withTerminal(t, true)
			e, out := stageEngine(t, "", &initOptions{})
			if tc.managed {
				e.r.gate = &initGate{managed: true}
			}
			s := newPluginStage(e)
			_, err := s.Apply(t.Context(), initflow.Env{Interactive: true})
			got, _ := os.ReadFile(settings)
			written := string(got) != settingsWithOthers
			if !strings.Contains(out.String(), "extraKnownMarketplaces") {
				t.Fatalf("the step printed no diff of what it would write:\n%s", out.String())
			}
			if tc.managed {
				if err != nil {
					t.Fatalf("a managed project at its own terminal was stopped: %v\n%s", err, out.String())
				}
				if !written {
					t.Errorf("the settings were not wired, so the gate's verdict did not stand in for the y/N:\n%s", out.String())
				}
				if strings.Contains(out.String(), "[y/N]") || strings.Contains(out.String(), "confirmed by --yes") {
					t.Errorf("a managed project's own terminal was asked:\n%s", out.String())
				}
				if !strings.Contains(out.String(), initflow.GateOrdinary()) {
					t.Errorf("the write does not say what decided it:\n%s", out.String())
				}
				return
			}
			if err == nil || written {
				t.Fatalf("a project that is not managed wired the settings with nothing typed (err %v):\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), "Write this to "+settings+"? [y/N]") {
				t.Errorf("a project that is not managed was not asked:\n%s", out.String())
			}
		})
	}
}

// The secrets stage is never admitted (§3.2), whatever the project is: a step
// whose whole subject is a secret the user types has nothing a gate could read
// — no plan, no digest, no rule — so the gate does not name it, and a coding
// agent's session reads the route it always read. This is the gate's own
// narrowing (D1011's never-list) and the reason the map has no entry for it.
func TestUpdateManagedNeverAdmitsTheSecretsStage(t *testing.T) {
	if gatedStages[initflow.Secrets] {
		t.Fatal("the gate names the secrets stage: a step whose document is a secret has nothing the gate could judge")
	}
	spy := &readerSpy{Reader: strings.NewReader("")}
	r := newSecretsRig(t, bitbucketOrigin, spy)
	t.Setenv("CLAUDECODE", "1")
	// The gate open and the project managed: the state a run is in when it
	// reaches this stage, so what the stage does here is the stage's own doing
	// and not the gate's absence.
	r.e.r.gate = &initGate{managed: true}
	r.e.r.o.door = updateDoor.name

	res, err := r.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if spy.read || r.store.setCalls() != 0 {
		t.Fatalf("the secrets stage read stdin (%v) or stored a secret (%d sets) with the gate open", spy.read, r.store.setCalls())
	}
	if stateOf(res, initflow.Secrets) != initflow.NeedsYou || len(res.Left) != 1 {
		t.Fatalf("the secrets stage is %s with %d step(s) left, want the owner's own step", stateOf(res, initflow.Secrets), len(res.Left))
	}
	if !strings.Contains(resultJSON(t, res), "your own terminal") {
		t.Errorf("the step left for the owner does not name the route a coding agent can take:\n%s", resultJSON(t, res))
	}
	if len(r.store.secrets) != 0 {
		t.Errorf("a secret was stored: %v", r.store.secrets)
	}
}

// The two stages that create an identity and provision money-bearing
// infrastructure stay the owner's: both are `noYes` in the stage table, neither
// is in the gate's own set, and on this door the compute stage is not even
// reachable — `fugaro update` does not take --provision-batch at all. A clean
// plan verdict cannot reach either of them (D1026: the gate narrows, and a
// stage --yes never covered has nothing to narrow).
func TestUpdateManagedLeavesCredentialsAndComputeOwnerRun(t *testing.T) {
	for _, name := range []string{initflow.Credentials, initflow.Compute} {
		if initflow.YesCovers(name) || initflow.RunCovers(name) {
			t.Errorf("--yes covers the %s stage: it is money or permanent", name)
		}
		if gatedStages[name] {
			t.Errorf("the gate names the %s stage: a step with no plan and no confirmation class the gate covers is never an agent-runnable one", name)
		}
	}

	r := updateManagedRig(t)
	updating(t, r)
	got, err := updateRun(t, r, "update", "--yes", "--provision-credentials")
	if err == nil {
		t.Fatalf("a coding agent's session provisioned the credentials service:\n%s", got)
	}
	if !strings.Contains(got, initflow.AgentRefusal("CLAUDECODE")) {
		t.Errorf("the credentials stage was not refused in the agent's own words:\n%s", got)
	}
	if !strings.Contains(got, "[needs you]  "+initflow.Credentials) {
		t.Errorf("the run did not reach the credentials stage, so this is another stage's refusal:\n%s", got)
	}
	if !strings.Contains(got, "--provision-credentials") {
		t.Errorf("the step left for the owner does not name the flag that runs it:\n%s", got)
	}

	// The compute stage is not this door's to reach at all: the flag is
	// setup's, and the refusal says so before anything runs.
	r2 := updateManagedRig(t)
	updating(t, r2)
	got, err = updateRun(t, r2, "update", "--yes", "--provision-batch")
	if err == nil || !strings.Contains(got, "fugaro update does not take --provision-batch") {
		t.Errorf("this door took --provision-batch (err %v):\n%s", err, got)
	}
	if n := len(r2.ran(t, "apply")); n != 0 {
		t.Errorf("the run applied %d plan(s) on its way to refusing the compute stage", n)
	}
}

// A run whose own arguments shape the installation is refused the gate, and it
// is refused it for the reason §3.2 gives those arguments: the cover's Listed
// members are read from THIS run's options (accessLists reads spec.Launchers,
// spec.Operators and the budget admins into tf.Cover.Listed), so a session that
// passed --launcher, --operator or --budget-admin would have the gate judge a
// plan against the list it just wrote — §4.1's rules 4 and 5 refuse exactly the
// grant such a plan makes, and the flag would walk around both by making it
// covered. --budget with --billing-account sets the cover's Billing the same
// way, and the rest of initOptions.installationFlags changes what the
// installation is. The state bucket and the owner's delete list are the same
// answer (`shapesTheInstallation`).
//
// Every row is therefore today's refusal, whole: the same plan that the
// widening flag would have had admitted is refused in the session's own words,
// nothing is applied, and — the property a flag asked beside the door has — the
// gate read nothing and no record exists, because no verdict was ever made. The
// plan grants a member the run's own --launcher would have listed, which is the
// exact shape the hole was found with.
func TestUpdateManagedRefusesASessionWideningFlag(t *testing.T) {
	const outsider = "user:not-listed@example.com"
	address := `module.installation.google_storage_bucket_iam_member.state["` + outsider + `"]`

	for _, tc := range []struct {
		name string
		args []string
		// rig is the world the row runs in, when it is not the managed rig the
		// rows share: --budget-admin is the one flag here that only exists
		// beside another installation flag (--firebase-project, init.go's
		// check) and only reaches a stage at all in an installation whose local
		// config names its shared budget backend.
		rig func(t *testing.T) *initRig
	}{
		{"launcher", []string{"--launcher", outsider}, nil},
		{"operator", []string{"--operator", outsider}, nil},
		{"budget-admin", []string{"--firebase-project", fpID, "--budget-admin", outsider}, func(t *testing.T) *initRig {
			r := newFBRig(t)
			r.managedRuns(t, initProjectName, initProject)
			t.Setenv("CLAUDECODE", "1")
			isolateGateCredentials(t)
			return r.initRig
		}},
		{"budget and billing account", []string{"--budget", "1", "--budget-currency", "USD", "--billing-account", billingAcc}, nil},
		{"state bucket", []string{"--state-bucket", initStateBucket}, nil},
		// --region rewrites the persisted region (and the cover's region and
		// registry), --github-app-id the App ID the repository jobs carry: both
		// are written to the local config by an apply this gate would admit.
		{"region", []string{"--region", "us-west1"}, nil},
		{"github app id", []string{"--github-app-id", "123456"}, nil},
		{"the owner's delete list", []string{"--allow-delete", address}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := updateManagedRig(t)
			if tc.rig != nil {
				r = tc.rig(t)
			}
			updating(t, r)
			r.setPlan(t, grantChange("state", outsider, "roles/storage.objectViewer", "create"))

			got, err := updateRun(t, r, append([]string{"update", "--yes"}, tc.args...)...)
			if err == nil {
				t.Fatalf("a session that shaped the installation ran to the end:\n%s", got)
			}
			if n := len(r.ran(t, "apply")); n != 0 {
				t.Errorf("%d terraform apply(s) ran under a flag no gate may open a stage for:\n%s", n, got)
			}
			if !strings.Contains(got, initflow.AgentRefusal("CLAUDECODE")) {
				t.Errorf("the session did not read the refusal it has always read:\n%s", got)
			}
			if strings.Contains(got, initflow.AgentProceeded("CLAUDECODE")) ||
				strings.Contains(got, managedmode.NotConverged) {
				t.Errorf("a step was judged under a flag that shapes the installation:\n%s", got)
			}
			if recs := audits(t, r); len(recs) != 0 {
				t.Errorf("a run no gate judged left %+v", recs)
			}
		})
	}
}

// The rules stage is the one judged step of this command whose document is not
// a Terraform plan (spec §4.2: "it is a document the binary carries, so the plan
// IS the generation"). In a managed project with a coding agent's session, a
// stale deployed generation is one the gate admits: the session deploys it, and
// the admission is recorded with the digest of the generation this binary
// carries — which is what an owner reads afterwards to check that the rules
// that landed are the release's own.
func TestUpdateManagedRulesStageUnderTheGate(t *testing.T) {
	r, db := updateBudgetRig(t)
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	isolateGateCredentials(t)
	// The rig's world plans an additive change (updating), which is what lets
	// the installation step run to its apply: a step that plans nothing has no
	// paper for the gate either, and the run would stop at the installation's
	// own local-config write — a file write of this machine, not a cloud write,
	// and one this task deliberately leaves asking.
	updating(t, r)
	r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	got := out + errOut
	if err != nil {
		t.Fatalf("a managed project's agent session did not deploy the rules generation (%v):\n%s", err, got)
	}
	if !strings.Contains(got, initflow.AgentProceeded("CLAUDECODE")) {
		t.Errorf("the admitted rules step did not say it proceeded:\n%s", got)
	}
	gen, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !infra.SameJSON(db.Rules(), gen) {
		t.Errorf("the rules deployed are not this binary's generation:\n%s", got)
	}
	if want := strconv.Itoa(projectpolicy.RulesVersion); rulesMarkerOn(t, db) != want {
		t.Errorf("the deployed generation marker is %q, want %q:\n%s", rulesMarkerOn(t, db), want, got)
	}
	// One record per judged step and not one per run: the installation's plan
	// and the rules generation are two documents, and each carries its own
	// digest. The rules' own is the one this test is about.
	recs := audits(t, r)
	want := gateDigest(gen)
	found := false
	for _, rec := range recs {
		if rec.Verdict != managedmode.AuditAdmitted {
			t.Errorf("a step of this run left %+v, want every record admitted", rec)
		}
		if rec.Command != "fugaro update" {
			t.Errorf("the record names the command %q", rec.Command)
		}
		if rec.PlanDigest == want {
			found = true
		}
	}
	if !found {
		t.Errorf("no record carries the digest of the generation this binary carries (%s): %+v\n%s", want, recs, got)
	}
}

// --plan-only applies nothing, and the gate does not change that: the flag is
// asked before any mark is read, so an agent's plan-only run in a managed
// project is the read-only account it has always been — no apply, no record,
// and the step shown as one that would be applied.
func TestUpdateManagedPlanOnlyStillAppliesNothing(t *testing.T) {
	r := updateManagedRig(t)
	updating(t, r)
	r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))

	got, err := updateRun(t, r, "update", "--plan-only")
	if err != nil {
		t.Fatalf("--plan-only failed (%v):\n%s", err, got)
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("--plan-only applied %d plan(s) in a managed project:\n%s", n, got)
	}
	if strings.Contains(got, initflow.AgentProceeded("CLAUDECODE")) {
		t.Errorf("a plan-only run read the coding agent's own admission sentence:\n%s", got)
	}
	if n := markReads(r); n != 1 {
		t.Errorf("--plan-only read the managed marker %d time(s), want the one read the installation has always made: the answer cannot change what a plan-only run does", n)
	}
	if recs := audits(t, r); len(recs) != 0 {
		t.Errorf("a plan-only run left %+v: it applied nothing to record", recs)
	}
	// The step is the read-only account it always was: the plan is shown, the
	// table says it would apply it, and the run ends by naming the plan file
	// instead of a write. A step that never reached its plan reads as [needs
	// you] or as blocked, and both fail here.
	for _, want := range []string{"[will do]", "installation", "1 creates", "--plan-only: nothing applied; the plan is"} {
		if !strings.Contains(got, want) {
			t.Errorf("the step is not reported as one that would be applied, missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[needs you]") {
		t.Errorf("a plan-only run in a managed project left a step for the owner, though it applies nothing:\n%s", got)
	}
}

// The policy stage is the other judged step of this command whose document is
// not a Terraform plan: the node the stage is about to publish, byte for byte
// (policyMigration.payload — the bytes its conditional write puts at the shared
// path). A node a release behind this binary is a node the gate admits in a
// managed project, and the record it leaves names the two questions the step
// asked before writing — the database is this installation's (D1017) and this
// machine's config is not behind the published node (D854) — rather than a
// value from the node.
func TestUpdateManagedPolicyStageUnderTheGate(t *testing.T) {
	r, db := updateBudgetRig(t)
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	isolateGateCredentials(t)
	// The rig's world plans an additive change, which is what lets the
	// installation step run to its apply (see the rules stage's test).
	updating(t, r)
	r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))
	// A node a release before this binary's, with the ceiling the local config
	// states: the migration is the one write, and it loosens nothing.
	db.Set(projectpolicy.PathShared(), v3Node(nil))

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	got := out + errOut
	if err != nil {
		t.Fatalf("a managed project's agent session did not publish the policy node (%v):\n%s", err, got)
	}
	if !strings.Contains(got, initflow.AgentProceeded("CLAUDECODE")) {
		t.Errorf("the admitted policy step did not say it proceeded:\n%s", got)
	}
	if v := nodeVersionOn(t, db); v != projectpolicy.Version {
		t.Errorf("the published policy is version %d, want %d: the admitted step wrote nothing\n%s", v, projectpolicy.Version, got)
	}
	gen, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	rulesDigest := gateDigest(gen)
	var policy []managedmode.AuditRecord
	for _, rec := range audits(t, r) {
		for _, rule := range rec.Rules {
			if strings.Contains(rule, "not behind the published node") {
				policy = append(policy, rec)
			}
		}
	}
	if len(policy) != 1 {
		t.Fatalf("%d record(s) say the policy node was judged, want one: %+v", len(policy), audits(t, r))
	}
	rec := policy[0]
	if rec.Verdict != managedmode.AuditAdmitted {
		t.Errorf("the policy node's record reads %q, want an admission", rec.Verdict)
	}
	if rec.Command != "fugaro update" {
		t.Errorf("the record names the command %q", rec.Command)
	}
	// The digest is of the node this step wrote and not of another step's
	// document: the rules generation is the one other document of this run whose
	// digest the test can compute itself.
	if rec.PlanDigest == "" || rec.PlanDigest == rulesDigest {
		t.Errorf("the record's digest is %q, which is no document of its own (the rules generation's is %s)", rec.PlanDigest, rulesDigest)
	}
}

// A publish that loosens a ceiling is the policy stage's Typed class (D846),
// and D1026 never admits one: the node keeps every confirmation it has, nothing
// is written and nothing is recorded about it. What the session reads is the
// gate's sentence and not its own: this step's document WAS admitted by the
// rules the stage answered before the write, so the class is what refused it,
// and the sentence says which kind of step this is and that no flag reaches
// past the gate (D1023's "names the rule that refused, and the fact that a
// person is not being asked") rather than telling a session it is a session.
func TestUpdateManagedPolicyStageRefusesTheTypedLoosening(t *testing.T) {
	r, db := updateBudgetRig(t)
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	isolateGateCredentials(t)
	updating(t, r)
	r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))
	// The node states 3 and the local config states 5 (updateBudgetRig): the
	// publish would raise what every run of the project is admitted by.
	db.Set(projectpolicy.PathShared(), staleBudgetNodeAt(3))

	got, err := updateRun(t, r, "update", "--yes")
	if err == nil {
		t.Fatalf("a managed project's agent session published a looser node:\n%s", got)
	}
	// The step's own line is what is read here and not the whole account: this
	// run also notes that the shared config was not published, which is another
	// step's refusal in the session's words and has nothing to do with the node.
	head := "[needs you]  " + initflow.Policy
	if !strings.Contains(got, head) {
		t.Fatalf("the run did not reach the policy stage, so nothing here was judged:\n%s", got)
	}
	line := got[strings.Index(got, head):]
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if !strings.Contains(line, managedmode.NotConverged) {
		t.Errorf("the policy step is the user's in a sentence that is not the gate's: %s", line)
	}
	if !strings.Contains(line, "the step's confirmation class is Typed, which no plan gate admits (D1026)") {
		t.Errorf("the refusal does not name the class rule that refused the node: %s", line)
	}
	if strings.Contains(line, initflow.AgentRefusal("CLAUDECODE")) {
		t.Errorf("the policy step reads the session's own refusal rather than what about the node the gate would not admit: %s", line)
	}
	if got := ceilingOn(t, db); got != 3 {
		t.Errorf("the published ceiling is %v, want the 3 it had: the refused step wrote the local config's 5", got)
	}
	for _, rec := range audits(t, r) {
		for _, rule := range rec.Rules {
			if strings.Contains(rule, "not behind the published node") {
				t.Errorf("a step the gate never judged left a record: %+v", rec)
			}
		}
	}
}

// TestUpdateManagedRefusesTheFirstPolicyPublish: the first node of a managed
// project is the owner's to write, whichever door the session came through —
// a first publish has no published list for its allow-list to be unchanged or
// narrower than, so every repository it names would be admitted where none was
// (M3, spec §4.1 rule 5). The policy stage's own publish makes one now (#1656),
// and the gate refuses this one exactly as it refuses the explicit door's: no
// admission, no record, and the node still absent when the run is over.
func TestUpdateManagedRefusesTheFirstPolicyPublish(t *testing.T) {
	r, db := updateBudgetRig(t)
	r.managedRuns(t, initProjectName, initProject)
	t.Setenv("CLAUDECODE", "1")
	isolateGateCredentials(t)
	updating(t, r)
	r.setPlan(t, change("module.installation.google_service_account.scheduler", "create"))
	// This machine's config states a catalog and nothing is published, which is
	// the state the stage now leaves to do. The catalog has a built entry, so
	// the catalog check job needs this installation's base image to run from.
	r.appendConfig(t, catalogConfigYAML+"base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc}\n")

	got, err := updateRun(t, r, "update", "--yes")
	if err == nil {
		t.Fatalf("a managed project's agent session wrote the first policy node:\n%s", got)
	}
	head := "[needs you]  " + initflow.Policy
	if !strings.Contains(got, head) {
		t.Fatalf("the run did not stop at the policy stage, so nothing here was judged:\n%s", got)
	}
	if v := db.Value(projectpolicy.PathShared()); v != nil {
		t.Fatalf("the refused first publish wrote the node: %v", v)
	}
	for _, rec := range audits(t, r) {
		for _, rule := range rec.Rules {
			if strings.Contains(rule, "not behind the published node") {
				t.Errorf("a step the gate never admitted left a record: %+v", rec)
			}
		}
	}
}

// The one failure this step has of its own: a node write with no bytes to write
// is a step whose document does not exist, and a document nobody could read is
// never applied (D1012's direction, the same one managed.Gate takes for a plan
// terraform cannot read). The step stops in the gate's sentence rather than
// being published empty, and it stops before M6's helper is asked anything at
// all: no verdict is left held afterwards and no record is written.
//
// The migration the command itself builds never has an empty payload — it is
// rendered from the node and this machine's config — so the step is driven
// directly with the one field that makes its own contract true, which is what
// makes this a check of the contract rather than of a state a run can reach.
func TestUpdateManagedPolicyStageRefusesAnEmptyNode(t *testing.T) {
	r := updateManagedRig(t)
	run := &initRun{o: &initOptions{door: updateDoor.name}, w: io.Discard, cmd: NewRootCmd()}
	run.gate = &initGate{managed: true}
	s := &policyMigrateStage{e: &initEngine{r: run}}
	admitted, err := s.gateStep(context.Background(), &policyMigration{moved: []byte{}})
	if !admitted {
		t.Errorf("a step with no document was left to its own confirmation: an empty node would be published")
	}
	var ge *initflow.GatedError
	if !errors.As(err, &ge) {
		t.Fatalf("the refusal is %v, want the gate's own sentence", err)
	}
	if ge.Reason != gateUnreadable {
		t.Errorf("the refusal reads %q, want %q", ge.Reason, gateUnreadable)
	}
	if run.gate.g.judged != nil {
		t.Errorf("the gate judged a document that does not exist: %+v", *run.gate.g.judged)
	}
	for _, rec := range audits(t, r) {
		t.Errorf("a step that was never judged left a record: %+v", rec)
	}
}
