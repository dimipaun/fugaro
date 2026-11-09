package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// The retry around a step's plan, gate and apply (task: "fugaro update
// absorbs a Google API that has not caught up with a resource it just
// created"). What it is FOR is the failure of 2026-11-09 on a managed
// project: `terraform apply` created google_cloud_run_v2_service.credentials[0]
// ("Creation complete after 11s") and the two grants on it failed with
// `googleapi: Error 404: Resource 'fugaro-credentials' of kind 'SERVICE' ...
// does not exist` against an IAM front end that had not seen the service yet.
// A person re-runs the command and gets the rest of the apply; CLAUDE.md's
// HARD RULE says that re-run is not theirs to make.
//
// The tests here are the cli's half: the cycle is planned, gated and applied
// again rather than the old saved plan being applied a second time. The closed
// list of errors that may be retried is internal/infra/tf's own
// (TestPropagationFailureIsTheClosedList).

const (
	// The addresses of the reported failure. The service is what the apply
	// creates; the two grants are what Google refused.
	credentialsServiceAddr = "module.installation.google_cloud_run_v2_service.credentials[0]"
	invokerGrantAddr       = "module.installation.google_cloud_run_v2_service_iam_member.credentials_invoker"
	verifierGrantAddr      = "module.installation.google_cloud_run_v2_service_iam_member.credentials_verifier"

	// invoker404Text and verifier404Text are what Google answered, in the
	// shape terraform prints: the name and the kind it could not see.
	invoker404Text  = `Error applying IAM policy for cloudrun.googleapis.com/Service "fugaro-credentials": googleapi: Error 404: Resource 'fugaro-credentials' of kind 'SERVICE' does not exist, notFound`
	verifier404Text = `Error applying IAM policy for cloudrun.googleapis.com/Service "fugaro-credentials": googleapi: Error 404: Resource 'fugaro-credentials' of kind 'SERVICE' does not exist, notFound`
)

// credentialsServiceChanges is the installation root's plan in that failure:
// the service this apply creates, and the two grants on it.
func credentialsServiceChanges() []planChange {
	return []planChange{
		created(credentialsServiceAddr, "google_cloud_run_v2_service",
			map[string]any{"name": "fugaro-credentials", "location": "us-east5", "project": initProject}),
		created(invokerGrantAddr, "google_cloud_run_v2_service_iam_member",
			map[string]any{"name": "fugaro-credentials", "role": "roles/run.invoker", "member": "user:operator@example.invalid"}),
		created(verifierGrantAddr, "google_cloud_run_v2_service_iam_member",
			map[string]any{"name": "fugaro-credentials", "role": "roles/run.invoker", "member": "serviceAccount:fugaro-verifier@" + initProject + ".iam.gserviceaccount.com"}),
	}
}

// created is one planned create with the attributes terraform writes for it:
// the values the classifier reads to say whether the error names a resource
// THIS apply creates.
func created(addr, typ string, after map[string]any) planChange {
	return planChange{"address": addr, "type": typ,
		"change": map[string]any{"actions": []string{"create"}, "before": map[string]any{}, "after": after}}
}

// applyDiagnostic is one terraform apply diagnostic as stderr carries it:
// boxed, with the resource it was applying above the source line.
func applyDiagnostic(address, message string) string {
	return "╷\n│ Error: " + message + "\n│ \n│   with " + address + ",\n│   on iam.tf line 163, in resource \"x\" \"y\":\n│  163: resource \"x\" \"y\" {\n│ \n╵\n"
}

// theReported404s is the stderr of the failure this whole retry exists for.
var theReported404s = applyDiagnostic(invokerGrantAddr, invoker404Text) + applyDiagnostic(verifierGrantAddr, verifier404Text)

// credentialsAccount is the account the two grants on the credentials service
// are made to, and the accountGap400 is Google's answer while IAM has not seen
// it: the other entry of the closed list (it is the class the compute root's
// own gap was verified with).
const (
	credentialsAccountAddr = "module.installation.google_service_account.credentials[0]"
	credentialsAccount     = "fugaro-credentials"

	accountGap400Text = `Error applying IAM policy for cloudrun.googleapis.com/Service "fugaro-credentials": googleapi: Error 400: Service account fugaro-credentials@proj-1234.iam.gserviceaccount.com does not exist., badRequest`
)

// theAccountGap400s is the same two grants, refused because the account this
// apply created is not visible to IAM yet.
var theAccountGap400s = applyDiagnostic(invokerGrantAddr, accountGap400Text) + applyDiagnostic(verifierGrantAddr, accountGap400Text)

// credentialsAccountChanges is that failure's plan: the account this apply
// creates, and the two grants on it.
func credentialsAccountChanges() []planChange {
	return []planChange{
		created(credentialsAccountAddr, "google_service_account",
			map[string]any{"account_id": credentialsAccount, "email": credentialsAccount + "@" + initProject + ".iam.gserviceaccount.com"}),
		created(invokerGrantAddr, "google_cloud_run_v2_service_iam_member",
			map[string]any{"name": credentialsAccount, "role": "roles/run.invoker", "member": "serviceAccount:" + credentialsAccount + "@" + initProject + ".iam.gserviceaccount.com"}),
		created(verifierGrantAddr, "google_cloud_run_v2_service_iam_member",
			map[string]any{"name": credentialsAccount, "role": "roles/run.invoker", "member": "serviceAccount:fugaro-verifier@" + initProject + ".iam.gserviceaccount.com"}),
	}
}

// noPropagationWaits makes the retry's waits instant without changing what
// they print: the durations are what a person reads, the sleep is what a test
// cannot afford.
func noPropagationWaits(t *testing.T) {
	t.Helper()
	old := propagationSleep
	propagationSleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { propagationSleep = old })
}

// rootSubs are the subcommands of want run in one root's own workdir, in
// order. The rig's world runs several roots, and a retry is one root's own
// (the fake records each call's directory).
func rootSubs(t *testing.T, r *initRig, root string, want ...string) []string {
	t.Helper()
	b, err := os.ReadFile(r.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var c tfCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		if len(c.Args) == 0 || filepath.Base(c.Dir) != root {
			continue
		}
		name := c.Args[0]
		if name == "state" && len(c.Args) > 1 {
			name += " " + c.Args[1]
		}
		if slices.Contains(want, name) {
			out = append(out, name)
		}
	}
	return out
}

// happyInstallation is the installation step's world with one plan that
// creates the credentials service and two grants: the retry's own cycle, and
// nothing else, is what these tests are about.
func happyInstallation(t *testing.T, r *initRig, changes ...planChange) {
	t.Helper()
	r.script["plan@installation"] = map[string]any{"exit": 2}
	if len(changes) == 0 {
		changes = credentialsServiceChanges()
	}
	r.setPlanAt(t, "installation", changes...)
	r.save(t)
}

// An apply that failed only on resources Google had not caught up with is run
// again -- and the whole cycle with it: the new plan is shown, judged and
// applied, rather than the old saved plan being applied a second time.
func TestPropagationRetryRerunsTheInstallationStep(t *testing.T) {
	noPropagationWaits(t)
	r := updateRig(t)
	happyInstallation(t, r)
	r.script["apply@installation"] = []any{
		map[string]any{"exit": 1, "stderr": theReported404s},
		map[string]any{"exit": 0},
	}
	r.save(t)

	out, err := updateRun(t, r, "update", "--yes")
	if err != nil {
		t.Fatalf("the retried installation step failed: %v\n%s", err, out)
	}
	const line = "retrying the installation step in 20s: Google had not yet caught up with a resource it just created (attempt 1 of 3)"
	if n := strings.Count(out, line); n != 1 {
		t.Errorf("the retry was reported %d times, want one line:\n%s", n, out)
	}
	if got, want := rootSubs(t, r, "installation", "plan", "show", "apply"), []string{"plan", "show", "apply", "plan", "show", "apply"}; !slices.Equal(got, want) {
		t.Errorf("the installation root's plan/show/apply = %q, want the whole cycle again\n%s", got, out)
	}
}

// The second plan is a plan like the first one: the managed gate judges it
// again, and the record is of the plan the retry applied -- the digest of the
// plan the first attempt failed on is nowhere in the day's trail, because a
// step that applied nothing is not recorded (managed_gate's recorded()).
//
// The plan here is one the gate admits (a service account and the two grants
// on it, which is the shape the reported failure left behind) and the error is
// the other entry of the closed list: the 400 Google answers a binding whose
// account it has not seen.
func TestPropagationRetryJudgesTheNewPlanThroughTheGate(t *testing.T) {
	noPropagationWaits(t)
	r := updateManagedRig(t)
	updating(t, r)
	first := credentialsAccountChanges()
	second := credentialsAccountChanges()[:1]
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.script["show@installation"] = []any{
		map[string]any{"stdout": planJSON(t, first...)},
		map[string]any{"stdout": planJSON(t, second...)},
	}
	r.script["apply@installation"] = []any{
		map[string]any{"exit": 1, "stderr": theAccountGap400s},
		map[string]any{"exit": 0},
	}
	r.save(t)

	got, err := updateRun(t, r, "update", "--yes")
	if err != nil {
		t.Fatalf("the retried installation step failed: %v\n%s", err, got)
	}
	// The gate's own sentence is printed once per plan it admits, so two of
	// them are the gate judging the first plan and then the second.
	if n := strings.Count(got, initflow.AgentProceeded("CLAUDECODE")); n != 2 {
		t.Errorf("the gate admitted %d plan(s), want two: the retry re-plans and the gate judges the new plan\n%s", n, got)
	}
	recs := audits(t, r)
	if len(recs) != 1 || recs[0].Verdict != managedmode.AuditAdmitted {
		t.Fatalf("%d record(s) after a retried step, want one admitted: %+v", len(recs), recs)
	}
	if want := gateDigest([]byte(planJSON(t, second...))); recs[0].PlanDigest != want {
		t.Errorf("the record's digest is %s, want the digest of the plan the retry applied (%s): the gate judged the plan of the failed attempt and not the new one", recs[0].PlanDigest, want)
	}
	if stale := gateDigest([]byte(planJSON(t, first...))); recs[0].PlanDigest == stale {
		t.Errorf("the record is of the plan the failed apply was on (%s)", stale)
	}
}

// What the retry does not do: absorb anything that is not the closed list.
// One error off the list is enough to stop, exactly as before this retry
// existed -- the failure is reported and nothing is run again.
func TestPropagationRetryStopsOnAnythingElse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stderr  string
		changes []planChange
	}{
		{
			// The retryable 404 mixed with a failure a second apply would
			// hide: a quota is not a resource Google has not caught up with.
			name: "a 404 mixed with another failure",
			stderr: applyDiagnostic(invokerGrantAddr, invoker404Text) +
				applyDiagnostic("module.installation.google_cloud_run_v2_job.history", "Error creating job: googleapi: Error 403: Quota exceeded, quotaExceeded"),
			changes: credentialsServiceChanges(),
		},
		{
			// A 404 on something that is not an IAM grant: a bucket that is
			// not there will not be there on the next apply either.
			name:    "a 404 on a resource that is not an IAM grant",
			stderr:  applyDiagnostic("module.installation.google_storage_bucket.runs", `Error creating bucket: googleapi: Error 404: Resource 'fugaro-runs-proj-1234' of kind 'STORAGE_BUCKET' does not exist, notFound`),
			changes: credentialsServiceChanges(),
		},
		{
			// The grant and the 404 are real, and the target is not something
			// this apply created: there is nothing for the API to catch up with.
			name:    "a 404 on a grant whose target this apply does not create",
			stderr:  applyDiagnostic(invokerGrantAddr, invoker404Text),
			changes: credentialsServiceChanges()[1:],
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noPropagationWaits(t)
			r := updateRig(t)
			happyInstallation(t, r, tc.changes...)
			r.script["apply@installation"] = map[string]any{"exit": 1, "stderr": tc.stderr}
			r.save(t)

			out, err := updateRun(t, r, "update", "--yes")
			if err == nil {
				t.Fatalf("a failed apply succeeded:\n%s", out)
			}
			if !strings.Contains(ErrorText(err), "Error") {
				t.Errorf("the apply's own error was not reported: %v\n%s", err, out)
			}
			if got := rootSubs(t, r, "installation", "apply"); len(got) != 1 {
				t.Errorf("the installation root applied %d times, want 1: only the closed list is retried\n%s", len(got), out)
			}
			if strings.Contains(out, "retrying the installation step") {
				t.Errorf("an error off the list was retried:\n%s", out)
			}
		})
	}
}

// The retry is bounded: three waits, four applies in all, and then the error
// is the one terraform printed -- reported as every failed apply was before
// this retry existed.
func TestPropagationRetryStopsAfterTheLastWait(t *testing.T) {
	noPropagationWaits(t)
	r := updateRig(t)
	happyInstallation(t, r)
	r.script["apply@installation"] = map[string]any{"exit": 1, "stderr": theReported404s}
	r.save(t)

	out, err := updateRun(t, r, "update", "--yes")
	if err == nil {
		t.Fatalf("a gap that never closes succeeded:\n%s", out)
	}
	if !strings.Contains(ErrorText(err), "does not exist") {
		t.Errorf("the last apply's error was not reported: %v\n%s", err, out)
	}
	if got := rootSubs(t, r, "installation", "apply"); len(got) != len(propagationWaits)+1 {
		t.Errorf("the installation root applied %d times, want %d", len(got), len(propagationWaits)+1)
	}
	for i, wait := range propagationWaits {
		want := "retrying the installation step in " + strconv.Itoa(int(wait.Seconds())) + "s: Google had not yet caught up with a resource it just created (attempt " + strconv.Itoa(i+1) + " of " + strconv.Itoa(len(propagationWaits)) + ")"
		if n := strings.Count(out, want); n != 1 {
			t.Errorf("the retry line for wait %d appears %d times, want one: %q\n%s", i+1, n, want, out)
		}
	}
}

// The wait is the command's own: a run whose context is done -- ctrl-c -- stops
// where it is rather than sleeping through the backoff. The real wait is the
// one under test, with the first backoff's own duration.
func TestPropagationRetryWaitStopsWithTheContext(t *testing.T) {
	if !slices.Equal(propagationWaits, []time.Duration{20 * time.Second, 60 * time.Second, 120 * time.Second}) {
		t.Fatalf("propagationWaits = %v, want the three the retry was asked for", propagationWaits)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cycle := func(context.Context) (*tf.Plan, error) {
		return credentialsServicePlan(t), &tf.ExitError{Command: "apply", Code: 1, Stderr: theReported404s}
	}
	var out strings.Builder
	start := time.Now()
	err := propagationRetry(ctx, &out, "the installation step", cycle)
	if err == nil {
		t.Fatal("a cancelled context reported success")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("the wait outlived the cancelled context by %s", elapsed)
	}
	if n := strings.Count(out.String(), "retrying the installation step in 20s"); n != 1 {
		t.Errorf("the retry line appears %d times, want the one before the interrupted wait:\n%s", n, out.String())
	}
}

// propagationSleep itself, against the real implementation: the retry's wait
// is interruptible, and a context already done is not waited on at all.
func TestPropagationSleepIsInterruptible(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := sleepFor(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepFor on a cancelled context = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > time.Minute {
		t.Fatalf("sleepFor waited %s on a cancelled context", elapsed)
	}
	if err := sleepFor(context.Background(), 0); err != nil {
		t.Errorf("sleepFor for no time = %v, want nil", err)
	}
}

// credentialsServicePlan is the plan the failed apply was on, as the wrapper
// hands it back: the classifier reads its changes to say whether the error
// named a resource this apply created.
func credentialsServicePlan(t *testing.T) *tf.Plan {
	t.Helper()
	var p tf.Plan
	if err := json.Unmarshal([]byte(planJSON(t, credentialsServiceChanges()...)), &p); err != nil {
		t.Fatal(err)
	}
	return &p
}
