package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
