package cli

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
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

// markReads is how many calls asked the runs bucket for the managed marker
// object, however the client spelled the path: the gate's own read is one, and
// a run that never reads the marks makes none.
func markReads(r *initRig) int {
	n := 0
	for _, rq := range r.gcs.Requests() {
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

// A person's own run pays for the gate in nothing at all: the gate is
// consulted only in a coding agent's session (M6's bind), so an owner's
// terminal on an unmanaged project keeps every behaviour it had — --yes covers
// what it always covered — and no record is written about a decision this
// design never made.
//
// The count of reads is where "nothing at all" is measured. One call names the
// marker object in a person's run, and it is the installation's own managed-fact
// read, which every run has always made while planning (init_managed.go); the
// gate's own read would be a second. A change that makes an owner's run read
// the mark twice — or that makes the gate read it in the session it must not be
// read in — fails here.
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
	if n := markReads(r); n != 1 {
		t.Errorf("an owner's run read the managed marker %d time(s), want the one read the installation has always made: the gate costs an owner's session a read it never had", n)
	}
	if strings.Contains(got, initflow.AgentProceeded("CLAUDECODE")) {
		t.Errorf("a person's run printed the coding agent's own sentence:\n%s", got)
	}
	if recs := audits(t, r); len(recs) != 0 {
		t.Errorf("an unmanaged project left %+v: a record is about a managed project's step (D1025)", recs)
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
	r.e.r.gate = &initGate{read: true, managed: true}
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
