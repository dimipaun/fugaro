package cli

import (
	"context"
	"reflect"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// update_review_job_test.go is RJ4 of docs/review-job/implementation-plan.md:
// the migration that gives an installation at the previous version the
// implicit review-only job converges with a run in flight.
//
// The claim is the owner's requirement of 2026-11-13 (the review job, option C)
// read against CLAUDE.md's HARD RULE: the job piece 1 deploys (#2523) reaches
// every installation through the one command the user runs, and a run that
// bootstrapped under the release behind is left running by the update that
// deploys it — its records, its ledger and its counters untouched.
//
// The fixture is the update door's own (update_converge_test.go's convergeRig)
// with the credentials service an installation at the previous version holds:
// the block in the installation root's tfvars, on that release's image, and no
// implicit review job beside it, because the release behind has none to write.
// A run of the installation's own repository is in flight across the update,
// and every write it makes afterwards is checked by the same inFlightRun the
// in-flight audit's own test uses.

// anchoredYAML is the fixture's checkout with the gcp_project: line already on
// its default branch.
//
// The line is the one a repository's onboarding writes into the checkout
// (init_anchor.go), and the rig's own file has none: the run under test writes
// it, which leaves the working tree different from the default branch's and
// makes the NEXT run stop for the user over that file (init_repo_target.go,
// resolveRepoTarget). A person meets the same line and merges it — the update
// prints the diff and says so — and what the second update of THIS test is
// about is the review job, not the checkout's onboarding. So the fixture starts
// from the file that person's merge leaves: the installation is the previous
// version's exactly as before, and the checkout is one that has been onboarded.
const anchoredYAML = "version: 1\nproject: aurora\ngcp_project: " + initProject + "\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { commands: { build: sh build.sh, test: sh test.sh } }\n" +
	"  api: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// TestUpdateAddsTheImplicitReviewJobAndLeavesARunInFlightRunning: an
// installation at the previous version — the credentials service deployed by
// the release behind, no implicit review job in its tfvars — gets the job from
// ONE `fugaro update`, with a run in flight left running; and a second update
// hands terraform the same job, so it proposes no change to it.
//
// The job is named through the deployment's own helper and never as a literal:
// the name is derived from the (profile, provider, account) triple
// (infra.ImplicitReviewJobName), which is what the credentials service admits a
// review caller by, so a task that generalises the deployer over providers
// keeps this test reading the same fact.
func TestUpdateAddsTheImplicitReviewJobAndLeavesARunInFlightRunning(t *testing.T) {
	r := newConvergeRigYAML(t, v3Node(nil), anchoredYAML)
	// The credentials service an installation at the previous version holds:
	// the block in the installation root's tfvars, on that release's image,
	// with the outputs the module publishes beside it (the installation stage
	// refuses the half-carried state, a tfvars block whose outputs name no
	// service) and the installation root's plan holding the revision this run
	// moves. What the fixture does NOT hold is the review job: the release
	// behind deploys none, which is the fact this test is about.
	publishCredentialsImage(t, r.imagesRig, convergeRelease)
	seedCredentialsService(t, r.imagesRig, oldCredentialsRef(convergePrev))
	r.script["show -json"] = r.script["show"]
	r.script["plan@installation"] = map[string]any{"exit": 2}
	r.setPlanAt(t, "installation", change("module.installation.google_cloud_run_v2_service.credentials", "update"))
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"credentials_service": credentialsServiceOutput(),
		"intake":              intakeServiceOutput(),
	})}
	r.save(t)

	name := infra.ImplicitReviewJobName()
	if _, ok := reviewJobsHanded(t, r)[name]; ok {
		t.Fatalf("the fixture already hands terraform the review job %s: the installation is not one at the previous version, and the run below would prove nothing about the job this release adds", name)
	}

	run := startRunInFlight(t, r)

	convergeOn(t, r, []string{"policy-rules-skew", "policy-older-than-binary", "plugin-pin"})

	// One `fugaro update` and the job is in the document every stage of the run
	// plans the installation root from, which is what terraform is handed: the
	// job and its own service account are created by the apply that follows, and
	// the credentials service's review table gains the row it admits a review
	// caller by (infra.CredentialsReviewJobAccounts). No second command names
	// either — that is CLAUDE.md's HARD RULE, and the whole of what RJ4 pins.
	job, ok := reviewJobsHanded(t, r)[name]
	if !ok {
		t.Fatalf("after one fugaro update the installation hands terraform no review job %s: project_review_jobs = %v. The installation is at the previous version, whose release deployed no implicit review job, so this run is the one that has to deploy it", name, reviewJobsHanded(t, r))
	}
	// The entry is that job and not a name with something else under it: the
	// deployer derives the name from the triple, and a plan that creates a job
	// under a name no launcher resolves is the review working nowhere.
	if got := job["job"]; got != name {
		t.Errorf("the review job the installation hands terraform is named %v under the key %s: the name and the entry are the deployment's own and are derived, never spelled twice", got, name)
	}

	// What the run in flight holds, right after the update: it is still the run
	// the database counts as running, on the registry entry it bootstrapped
	// with, and its record is the one it wrote for itself. No step of the update
	// writes either, and the job the update adds is a create beside them.
	if got := r.db.Value(budget.PathAgent(run.slug, inFlightRunID)); got == nil {
		t.Errorf("the run's registry entry is gone after the update: %s reads nothing, so the update took the run in flight out of what the database counts as running", budget.PathAgent(run.slug, inFlightRunID))
	}
	ctx := context.Background()
	rec, err := run.store.ReadRecord(ctx)
	if err != nil {
		t.Fatalf("the run's record could not be read after the update: %v", err)
	}
	if rec.Status != runstore.StatusRunning || rec.Stage != "implement" {
		t.Errorf("the run's record reads status %q stage %q after the update, want the running/implement it bootstrapped with: the update rewrote a record a run holds", rec.Status, rec.Stage)
	}

	// And every write the run makes afterwards still lands: its lease, the
	// ledger and the counters that lease settles, the outcome it ends with, its
	// record and its review session. This is the half of the claim that is about
	// the run and not about the job.
	run.laterWrites(t, r)

	// The second update: the installation already has the job, and the run hands
	// terraform the same job — the same name under the same key, the same
	// service account and the same class — so nothing about it is deleted,
	// renamed or replaced, and RefuseProjectJobDeletes has nothing to refuse. A
	// job whose name moved would be a plan that destroys the one the first run
	// created and takes its executions with it.
	//
	// What the second run does move is the entry's own image and its
	// environment, and that is the update's ordinary catch-up rather than a
	// second job: the installation stage composes the root before the images
	// stage has pinned this release's base image, and before this run's
	// writeConfig records the credentials service the first run deployed, so the
	// job the first run created carries the release behind's base image and no
	// service address. The run that follows is the one that catches it up
	// (CarriedProjectReviewJobs' comment states the same rule for the carried
	// job), and the update AFTER that hands terraform the entry byte for byte:
	// the installation has reached its fixed point.
	if so, se, err := executeStdin(t, "", "update"); err != nil {
		t.Fatalf("the second fugaro update was refused: %v\n%s\n%s", err, so, se)
	}
	again, ok := reviewJobsHanded(t, r)[name]
	if !ok {
		t.Fatalf("the second fugaro update hands terraform no review job %s: project_review_jobs = %v, so the installation stopped converging on the job the first run added", name, reviewJobsHanded(t, r))
	}
	for _, k := range []string{"job", "service_account", "profile", "provider", "account"} {
		if !reflect.DeepEqual(again[k], job[k]) {
			t.Errorf("the second fugaro update hands terraform %s = %v, want the first run's %v: the job's identity moved, and a plan that moves it destroys the job the first run created", k, again[k], job[k])
		}
	}
	if so, se, err := executeStdin(t, "", "update"); err != nil {
		t.Fatalf("the third fugaro update was refused: %v\n%s\n%s", err, so, se)
	}
	third, ok := reviewJobsHanded(t, r)[name]
	if !ok {
		t.Fatalf("the third fugaro update hands terraform no review job %s: project_review_jobs = %v", name, reviewJobsHanded(t, r))
	}
	if !reflect.DeepEqual(third, again) {
		t.Errorf("the third fugaro update hands terraform %v, want the entry the second one handed it, %v: the installation has not reached a fixed point, so every update proposes a change to the job", third, again)
	}
	if _, err := run.store.ReadRecord(ctx); err != nil {
		t.Fatalf("the run's record could not be read back after the second update: %v", err)
	}
}

// reviewJobsHanded is the review-only job set the installation root's tfvars
// carry after a run: the document every stage of the run plans the root from, so
// it is what terraform is handed, and the one a test reads to say what the
// update deployed.
func reviewJobsHanded(t *testing.T, r *convergeRig) map[string]map[string]any {
	t.Helper()
	raw, _ := r.tfvars(t)["project_review_jobs"].(map[string]any)
	out := make(map[string]map[string]any, len(raw))
	for name, entry := range raw {
		if job, ok := entry.(map[string]any); ok {
			out[name] = job
		}
	}
	return out
}
