package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/infra"
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

// A retry's failure is judged against what the STEP has created, and not only
// against the plan of the attempt that just failed. The two are not the same
// plan, and they differ exactly where this retry lives: the first apply
// creates the service and the grants on it are refused, so by the time the
// retry plans, the service is in the state and the re-plan holds the leftover
// grants alone. Judged against that plan alone the second failure names a
// resource no plan of the attempt creates -- a real 404 as far as the
// classifier could tell -- and the step stopped at "attempt 1 of 3": one retry
// after 20s instead of the three waits it was given, and the compute step's
// four applies (~4 min) gone with it. The two failures here are the same gap
// on the same service, which is the whole case.
func TestPropagationRetryKeepsRetryingAfterItsOwnApplyCreated(t *testing.T) {
	noPropagationWaits(t)
	r := updateRig(t)
	// The first attempt's plan is the reported failure's own: the service this
	// apply creates and the two grants on it. The re-plan holds the grants
	// alone, which is what terraform finds left to do once its own apply
	// created the service; the fake repeats the last answer, so the third
	// attempt plans the same leftover.
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.script["show@installation"] = []any{
		map[string]any{"stdout": planJSON(t, credentialsServiceChanges()...)},
		map[string]any{"stdout": planJSON(t, credentialsServiceChanges()[1:]...)},
	}
	r.script["apply@installation"] = []any{
		map[string]any{"exit": 1, "stderr": theReported404s},
		map[string]any{"exit": 1, "stderr": theReported404s},
		map[string]any{"exit": 0},
	}
	r.save(t)

	out, err := updateRun(t, r, "update", "--yes")
	if err != nil {
		t.Fatalf("the retried installation step failed: %v\n%s", err, out)
	}
	for i, wait := range propagationWaits[:2] {
		want := "retrying the installation step in " + strconv.Itoa(int(wait.Seconds())) +
			"s: Google had not yet caught up with a resource it just created (attempt " + strconv.Itoa(i+1) + " of " + strconv.Itoa(len(propagationWaits)) + ")"
		if n := strings.Count(out, want); n != 1 {
			t.Errorf("the retry line for wait %d appears %d times, want one: the second failure is the same gap on the resource the first apply created, and the re-plan no longer creates it\n%q\n%s", i+1, n, want, out)
		}
	}
	if got := rootSubs(t, r, "installation", "apply"); len(got) != 3 {
		t.Errorf("the installation root applied %d times, want 3: the two failures and the apply that closed the gap\n%s", len(got), out)
	}
}

// A retried plan is judged afresh, and in a project that is not managed with
// --yes the guard is the whole of that judgement: the retry re-plans, so a
// plan the guard refuses is refused before it is applied, and never applied
// under the confirmation given to a plan that did not hold what it holds. The
// refusal here is the one only the guard sees -- a delete nobody allowed --
// because the failure the retry absorbs is an apply that creates: without the
// guard on the retry's own plan the delete would go through unnamed.
func TestPropagationRetryHoldsTheNewPlanToTheGuard(t *testing.T) {
	noPropagationWaits(t)
	r := updateRig(t)
	// The first attempt's plan is the reported failure's own, which the guard
	// admits; the re-plan holds those changes AND a delete nothing allowed.
	const runsBucket = "module.installation.google_storage_bucket.runs"
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.script["show@installation"] = []any{
		map[string]any{"stdout": planJSON(t, credentialsServiceChanges()...)},
		map[string]any{"stdout": planJSON(t, append(slices.Clone(credentialsServiceChanges()), change(runsBucket, "delete"))...)},
	}
	r.script["apply@installation"] = []any{
		map[string]any{"exit": 1, "stderr": theReported404s},
		map[string]any{"exit": 0},
	}
	r.save(t)

	out, err := updateRun(t, r, "update", "--yes")
	if err == nil {
		t.Fatalf("a retried plan holding an unallowed delete was applied:\n%s", out)
	}
	if said := ErrorText(err); !strings.Contains(said, "the plan would delete or replace 1 resource(s)") || !strings.Contains(said, runsBucket) {
		t.Errorf("the retry did not stop in the guard's own words about the re-plan: %v\n%s", err, out)
	}
	if got := rootSubs(t, r, "installation", "apply"); len(got) != 1 {
		t.Errorf("the installation root applied %d times, want 1: the plan the retry was handed was refused before it reached the apply\n%s", len(got), out)
	}
}

// The repository step runs the same cycle through the same retry: its own
// plan, gate and apply, and no saved plan applied twice. The failure here is
// the repository root's own version of the reported one -- an account this
// apply creates, and the binding on it that IAM refuses until it has seen the
// account (the repo module's build.tf) -- because the repository root is where
// the module's own bindings are made.
func TestPropagationRetryRerunsTheRepositoryStep(t *testing.T) {
	noPropagationWaits(t)
	p := newRepoPlanRig(t, names(4), sandboxBaseRelease, sandboxProjectSecretLC, repoAccountGapChanges)
	p.ri.script["apply"] = []any{
		map[string]any{"exit": 1, "stderr": repoAccountGap400},
		map[string]any{"exit": 0},
	}
	p.ri.save(t)

	if _, _, err := p.plan(t); err != nil {
		t.Fatalf("the retried repository step failed: %v\n%s", err, p.out.String())
	}
	out := p.out.String()
	const line = "retrying the repository step in 20s: Google had not yet caught up with a resource it just created (attempt 1 of 3)"
	if n := strings.Count(out, line); n != 1 {
		t.Errorf("the retry was reported %d times, want one line:\n%s", n, out)
	}
	// The leading show is the retirement's read of the state, which planRepo
	// makes once before the cycle; the cycle itself is plan, show, apply --
	// twice.
	if got, want := rootSubs(t, p.ri, filepath.Base(p.wd.Root), "plan", "show", "apply"),
		[]string{"show", "plan", "show", "apply", "plan", "show", "apply"}; !slices.Equal(got, want) {
		t.Errorf("the repository root's plan/show/apply = %q, want the whole cycle again\n%s", got, out)
	}
}

// repoAccountGapChanges is the repository root's plan in that failure: the
// build account the apply creates, and the project binding on it (build.tf's
// google_project_iam_member.build).
func repoAccountGapChanges(infra.RepoSpec) []planChange {
	return []planChange{
		created(repoBuildAccountAddr, "google_service_account",
			map[string]any{"account_id": repoBuildAccount, "email": repoBuildAccount + "@" + initProject + ".iam.gserviceaccount.com"}),
		created(repoBuildBindingAddr, "google_project_iam_member",
			map[string]any{"member": "serviceAccount:" + repoBuildAccount + "@" + initProject + ".iam.gserviceaccount.com", "role": "projects/" + initProject + "/roles/fugaroBuildSubmitter"}),
	}
}

// repoAccountGap400 is Google's answer to the binding while the account it
// names is not visible to IAM yet: the 400 entry of the closed list, on the
// repository root's own addresses.
var repoAccountGap400 = applyDiagnostic(repoBuildBindingAddr,
	"Error applying IAM policy for project \"projects/"+initProject+"\": googleapi: Error 400: Service account "+repoBuildAccount+"@"+initProject+".iam.gserviceaccount.com does not exist., badRequest")

const (
	repoBuildAccountAddr = "module.repo.google_service_account.build"
	repoBuildBindingAddr = "module.repo.google_project_iam_member.build"
	repoBuildAccount     = "fugaro-build"
)

// The Firebase root is the fourth, and last, cloud root of the converge, and
// it is wrapped the same way (applyRoot): modules/firebase/iam.tf creates the
// token signer and the google_service_account_iam_member that names it in one
// apply, which is the create-then-bind race itself -- so a fresh project's
// first `fugaro update --firebase-project` is exactly where a Google API that
// has not caught up used to stop the upgrade and ask for the re-run CLAUDE.md
// forbids. The failure here is that gap: the signer this apply creates, and
// the binding on it that IAM refuses until it has seen the account.
func TestPropagationRetryRerunsTheFirebaseStep(t *testing.T) {
	noPropagationWaits(t)
	p := newRepoPlanRig(t, names(4), sandboxBaseRelease, sandboxProjectSecretLC, firebaseSignerChanges)
	p.ri.script["apply"] = []any{
		map[string]any{"exit": 1, "stderr": firebaseSignerGap400},
		map[string]any{"exit": 0},
	}
	p.ri.save(t)

	// The Firebase root plans in a workdir of its own, whose tfvars the step
	// before it writes (initFirebase's fwd.WriteVars): terraform reads
	// -var-file=terraform.tfvars.json out of the directory it runs in, and the
	// fake refuses a plan whose vars are not there. This rig reaches the step
	// directly, so it builds the same pair -- the root's workdir and its vars --
	// rather than borrowing the repository's.
	fwd, ft, err := p.run.terraform("terraform", t.TempDir(), "firebase")
	if err != nil {
		t.Fatal(err)
	}
	if err := fwd.WriteVars([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}

	applied, stop, err := p.run.applyRoot(t.Context(), p.c, ft, fwd, "firebase", "to the Firebase project "+initProject, nil)
	if err != nil || !applied || stop {
		t.Fatalf("applied = %v, stop = %v, err = %v, want the retried Firebase step to have applied:\n%s", applied, stop, err, p.out.String())
	}
	out := p.out.String()
	const line = "retrying the Firebase step in 20s: Google had not yet caught up with a resource it just created (attempt 1 of 3)"
	if n := strings.Count(out, line); n != 1 {
		t.Errorf("the retry was reported %d times, want one line:\n%s", n, out)
	}
	if got, want := rootSubs(t, p.ri, filepath.Base(fwd.Root), "plan", "show", "apply"), []string{"plan", "show", "apply", "plan", "show", "apply"}; !slices.Equal(got, want) {
		t.Errorf("the Firebase root's plan/show/apply = %q, want the whole cycle again\n%s", got, out)
	}
}

// firebaseSignerChanges is the Firebase root's plan in that failure: the token
// signer this apply creates (modules/firebase/iam.tf) and the binding on it.
func firebaseSignerChanges(infra.RepoSpec) []planChange {
	account := "projects/" + initProject + "/serviceAccounts/" + signerAccount + "@" + initProject + ".iam.gserviceaccount.com"
	return []planChange{
		created(signerAccountAddr, "google_service_account",
			map[string]any{"account_id": signerAccount, "email": signerAccount + "@" + initProject + ".iam.gserviceaccount.com", "name": account}),
		created(minterGrantAddr, "google_service_account_iam_member",
			map[string]any{"service_account_id": account, "role": "projects/" + initProject + "/roles/fugaroTokenMinter", "member": "user:owner@example.invalid"}),
	}
}

// firebaseSignerGap400 is Google's answer to that binding while the account it
// names is not visible to IAM yet: the 400 entry of the closed list.
var firebaseSignerGap400 = applyDiagnostic(minterGrantAddr,
	"Error applying IAM policy for service account 'projects/"+initProject+"/serviceAccounts/"+signerAccount+"@"+initProject+".iam.gserviceaccount.com': googleapi: Error 400: Service account "+signerAccount+"@"+initProject+".iam.gserviceaccount.com does not exist., badRequest")

const (
	signerAccountAddr = "module.firebase.google_service_account.signer"
	minterGrantAddr   = "module.firebase.google_service_account_iam_member.minter"
	signerAccount     = "fugaro-signer"
)

// The managed gate judges a plan per attempt, and each admission leaves its
// own record: the plan the failed apply was on is one the gate admitted and
// the apply partly landed, so the retry's admission must not replace it in the
// day's trail (D1014) -- the record of the failed attempt carries the digest of
// the plan it applied, and the record of the retry the digest of the new one.
//
// The two plans are the two attempts'. Both create the account the 400 names,
// which is what the classifier reads to call the failure the closed list's own
// gap, and they are not the same plan: the attempt that failed also creates the
// scheduler's account, the retry only the one it could not grant on. They are
// also accounts the installation's own cover admits -- the cover tables are not
// widened for a test.
//
// One plan is answered for every read of the first attempt and the other for
// every read of the retry, because one attempt reads the saved plan three times
// (the summary, the document the gate digests, and the gate's own read) and the
// fake repeats its last answer.
func TestPropagationRetryJudgesTheNewPlanThroughTheGate(t *testing.T) {
	noPropagationWaits(t)
	r := updateManagedRig(t)
	updating(t, r)
	failed := append(credentialsAccountChanges()[:1],
		created("module.installation.google_service_account.scheduler", "google_service_account",
			map[string]any{"account_id": "fugaro-scheduler", "email": "fugaro-scheduler@" + initProject + ".iam.gserviceaccount.com"}))
	retried := credentialsAccountChanges()[:1]
	failedJSON := map[string]any{"stdout": planJSON(t, failed...)}
	retriedJSON := map[string]any{"stdout": planJSON(t, retried...)}
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.script["show@installation"] = []any{failedJSON, failedJSON, failedJSON, retriedJSON, retriedJSON, retriedJSON}
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
	// them are the gate judging the failed attempt's plan and then the new one.
	if n := strings.Count(got, initflow.AgentProceeded("CLAUDECODE")); n != 2 {
		t.Errorf("the gate admitted %d plan(s), want two: the retry re-plans and the gate judges the new plan\n%s", n, got)
	}
	// Two admissions, two records: the plan the failed apply was on is one the
	// gate admitted and the apply partly landed, so its record is owed before
	// the retry judges another plan — the second admission does not replace the
	// first one's trail (D1014). The order of the two is the listing's own and
	// is not asserted: both records carry the same second, and the id that
	// breaks the tie is random.
	recs := audits(t, r)
	if len(recs) != 2 {
		t.Fatalf("%d record(s) after a retried step, want one per admitted plan: %+v", len(recs), recs)
	}
	digests := map[string]managedmode.AuditRecord{}
	for _, rec := range recs {
		if rec.Verdict != managedmode.AuditAdmitted {
			t.Errorf("a record's verdict is %q, want %q", rec.Verdict, managedmode.AuditAdmitted)
		}
		digests[rec.PlanDigest] = rec
	}
	// The retry's admission is the gate's own verdict on the NEW plan, and it
	// did not replace the failed attempt's.
	if want := gateDigest([]byte(planJSON(t, retried...))); digests[want].PlanDigest != want {
		t.Errorf("no record carries the digest of the plan the retry applied (%s): the gate judged the plan of the failed attempt and not the new one\n%+v", want, recs)
	}
	// And the failed attempt's is still there, about the plan whose apply
	// created the account before the grants on it were refused.
	if was := gateDigest([]byte(planJSON(t, failed...))); digests[was].PlanDigest != was {
		t.Errorf("no record carries the digest of the plan the failed apply was on (%s): the retry's admission replaced it, and what that apply created is in no trail\n%+v", was, recs)
	}
}

// The other half of the same rule, and the half a retry that reused an
// admission would get wrong: a step that failed on the closed list is planned
// and JUDGED again, and a plan the gate refuses stops the step there. The
// admission the failed attempt was under is spent -- it admits that plan and no
// other -- so nothing is applied under it, and the refusal is the record the
// day carries for the retry (D1012, D1014).
func TestPropagationRetryAppliesNothingUnderTheFailedAttemptsAdmission(t *testing.T) {
	noPropagationWaits(t)
	r := updateManagedRig(t)
	updating(t, r)
	// The first attempt's plan is one the installation's own cover admits; the
	// plan the retry is handed grants the credentials service to a principal
	// the installation's modules do not grant, which the cover refuses.
	admitted := credentialsAccountChanges()[:1]
	refused := credentialsAccountChanges()
	admittedJSON := map[string]any{"stdout": planJSON(t, admitted...)}
	refusedJSON := map[string]any{"stdout": planJSON(t, refused...)}
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.script["show@installation"] = []any{admittedJSON, admittedJSON, admittedJSON, refusedJSON, refusedJSON, refusedJSON}
	r.script["apply@installation"] = []any{
		map[string]any{"exit": 1, "stderr": theAccountGap400s},
		map[string]any{"exit": 0},
	}
	r.save(t)

	got, err := updateRun(t, r, "update", "--yes")
	if err == nil {
		t.Fatalf("a plan the gate refused was applied:\n%s", got)
	}
	if !strings.Contains(got, "refused by cover") {
		t.Errorf("the retry did not stop in the gate's own words:\n%s", got)
	}
	if n := len(rootSubs(t, r, "installation", "apply")); n != 1 {
		t.Errorf("the installation root applied %d times, want 1: only the attempt that was admitted", n)
	}
	// Two verdicts, two records: the admission of the plan the failed apply was
	// on, and the refusal of the plan the retry was handed.
	recs := audits(t, r)
	if len(recs) != 2 {
		t.Fatalf("the day holds %d record(s), want the admission and the refusal: %+v", len(recs), recs)
	}
	kinds := map[string]managedmode.AuditVerdict{}
	for _, rec := range recs {
		kinds[rec.PlanDigest] = rec.Verdict
	}
	if want := gateDigest([]byte(planJSON(t, admitted...))); kinds[want] != managedmode.AuditAdmitted {
		t.Errorf("the plan the failed apply was on has no admitted record (digest %s): %+v", want, recs)
	}
	if want := gateDigest([]byte(planJSON(t, refused...))); kinds[want] != managedmode.AuditRefused {
		t.Errorf("the plan the retry judged was not recorded as refused (digest %s): %+v", want, recs)
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

// What the design says about which applies retry (§7.2 finding 5) is held to
// the code, in both directions: the steps this command runs through the retry
// are exactly the four the design names, and the two applies it names as NOT
// wrapped are still outside it. Wrapping a fifth -- the retirement's apply,
// the gap the design leaves open and says why -- fails here, which is what
// makes the sentence and its exception list move with the code that would make
// them false.
func TestPropagationRetrySitesAreWhatTheDesignNames(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sites := regexp.MustCompile(`propagationRetry\(ctx, r\.w, "([^"]+)"`)
	wrapped := map[string]string{} // the step's name in its retry line -> its file
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range sites.FindAllStringSubmatch(string(b), -1) {
			wrapped[m[1]] = f
		}
	}
	design := readDoc(t, "docs/design/compute-backend.md")
	for step, root := range map[string]string{
		"the installation step": "`installRoot`",
		"the repository step":   "`planRepo`",
		"the Firebase step":     "`applyRoot`",
		"the compute step":      "`computeStage.apply`",
	} {
		if _, ok := wrapped[step]; !ok {
			t.Errorf("propagationRetry does not run %s (a call site looks like propagationRetry(ctx, r.w, %q, ...)): the design says every cloud root of the converge runs its own cycle through the retry", step, step)
		}
		if !strings.Contains(design, root) {
			t.Errorf("docs/design/compute-backend.md no longer names %s among the roots that retry", root)
		}
	}
	if len(wrapped) != 4 {
		steps := make([]string, 0, len(wrapped))
		for step, f := range wrapped {
			steps = append(steps, step+" ("+f+")")
		}
		slices.Sort(steps)
		t.Errorf("propagationRetry wraps %d step(s): %s. The design names four, and names the two applies that are NOT wrapped: wrapping a fifth -- the retirement's apply, `retirePlan`, is the gap it leaves open -- moves §7.2 finding 5's list and this test with it",
			len(wrapped), strings.Join(steps, ", "))
	}
	// The names and the claim they answer have to be in the retry's own
	// paragraph -- where the "every cloud root" sentence is -- and not
	// somewhere else in the document: a mention of `retirePlan` in the
	// retirement's own section is not this claim.
	i := strings.Index(design, "are not wrapped")
	if i < 0 {
		t.Fatalf("docs/design/compute-backend.md no longer names the applies that are NOT wrapped: the design claims that every cloud root of the converge retries")
	}
	claim := design[i:min(len(design), i+2000)]
	for _, outside := range []string{"`retirePlan`", "`fillCredentialsAudience`"} {
		if !strings.Contains(claim, outside) {
			t.Errorf("docs/design/compute-backend.md no longer names %s among the applies that do not retry, so the design claims more than the code does", outside)
		}
	}
}
