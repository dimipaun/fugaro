package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	apistorage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/initflow"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// The gate at the call site, which internal/infra/managed's own test cannot
// pin: managed.WriteMarker trusts its created argument, and this is the one
// place that argument is read off a run — init's r.res.Created. A run that
// created no project must write no marker, whatever else it did; the mutation
// this exists to catch is dropping that check and marking the project the run
// installs into (r.res.Created = r.gcpProject in markCreated), which in
// production would mark every installation an ordinary `fugaro init` touches
// — fugaro's own, belong, the owner's — as managed, and turn the
// confirmations off for them (design D73: adding Firebase to a project is
// never what makes it managed).
//
// The bucket is seeded in all three cases, labelled and numbered as the
// installation's own, so a mutation that writes really does write: what is
// asserted is the gate, not the bucket check.
func TestMarkCreatedWritesOnlyForAProjectThisRunCreated(t *testing.T) {
	// (a) The ordinary run of an existing installation: no project created,
	// and none of the buckets gets the object.
	t.Run("an existing installation", func(t *testing.T) {
		r := newInitRig(t)
		r.markedRuns(t, initProjectName, initProject)
		fakeTerminal(t)
		if _, _, err := executeStdin(t, initProjectName+"\n", "init"); err != nil {
			t.Fatal(err)
		}
		noMarker(t, r.gcs, initRunsBucket)
	})

	// (b) init --firebase: Firebase is added to a project that already
	// exists, which this run did not create.
	t.Run("init --firebase on an existing project", func(t *testing.T) {
		r := newFBRig(t)
		r.markedRuns(t, initProjectName, initProject)
		if _, _, err := executeStdin(t, "us-east5\n", "init", "--firebase", fpID, "--yes"); err != nil {
			t.Fatal(err)
		}
		noMarker(t, r.gcs, initRunsBucket)
		if n := len(r.crm.Creates()); n != 0 {
			t.Errorf("%d project(s) created: this run must not have created one", n)
		}
	})

	// (c) --create-project: exactly one, about the project this run
	// created, in that project's own runs bucket. The positive case, so the
	// two above cannot pass by the object being unwritable.
	t.Run("--create-project", func(t *testing.T) {
		r := newProjectRig(t)
		bucket := "fugaro-runs-" + newProj
		// The created project, as the fakes know one the run creates through
		// projects.create: its number is the fake's first, and the bucket the
		// fact goes into has to be numbered with it (WriteMarker proves the
		// bucket against it).
		r.gcs.AddProject(newProj, gcpfake.FirstCreatedProjectNumber)
		r.gcs.AddBucket(bucket, gcpfake.FirstCreatedProjectNumber, managedBucketLabels)
		// The created project's own runs bucket, as the installation would
		// name it: init's owner() and Task 0.2's read of the fact both look
		// for the default name (init_review.go:183), and markCreated writes
		// nowhere else.
		roles := map[string]string{}
		for _, r := range []string{"launcher", "job_runner", "build_submitter", "tag_mover"} {
			roles[r] = "projects/" + newProj + "/roles/fugaro" + strings.ToUpper(r[:1]) + r[1:]
		}
		r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
			"runs_bucket":               bucket,
			"registry_host":             "us-east5-docker.pkg.dev/" + newProj,
			"scheduler_service_account": "fugaro-scheduler@" + newProj + ".iam.gserviceaccount.com",
			"role_ids":                  roles,
		})}
		r.save(t)
		r.bill.AddAccount(billingAcc, true)
		out, _, err := r.run(t, true, newProj+"\n"+billingAcc+"\n"+initProjectName+"\n", "--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if n := len(r.crm.Creates()); n != 1 {
			t.Fatalf("%d project(s) created:\n%s", n, out)
		}
		b, ok := markerAt(t, r.gcs, bucket)
		if !ok {
			t.Fatalf("no %s in gs://%s after the run that created the project:\n%s", managed.MarkerObject, bucket, out)
		}
		if want := `{"version":1,"gcp_project":"` + newProj + `"}`; b != want {
			t.Errorf("gs://%s/%s = %s, want %s", bucket, managed.MarkerObject, b, want)
		}
		// And the other half of the same fact, written by the same step
		// (Task 3.0): the label on the project itself, which the read
		// requires. Both, or the project is not managed at all.
		if got := r.crm.Labels(newProj)[managed.ProjectLabelKey]; got != managed.ProjectLabelValue {
			t.Errorf("%s on the created project %s = %q, want %q: the run that creates a project writes both halves of the fact", managed.ProjectLabelKey, newProj, got, managed.ProjectLabelValue)
		}
	})

	// (c') The same creation in the release as it ships, with managed
	// projects off (managedDeployerShipped false): the creation fact is
	// written as it always was, and the label is not, so the project does
	// not read as managed — which would change init's own guard refusal for
	// it to the managed text, with no --allow-delete — in a release whose
	// every managed entry point refuses. And the run does not say it is
	// Fugaro-managed.
	t.Run("--create-project with managed projects off", func(t *testing.T) {
		closeShippedGates(t)
		r := newProjectRig(t)
		bucket := "fugaro-runs-" + newProj
		r.gcs.AddProject(newProj, gcpfake.FirstCreatedProjectNumber)
		r.gcs.AddBucket(bucket, gcpfake.FirstCreatedProjectNumber, managedBucketLabels)
		roles := map[string]string{}
		for _, r := range []string{"launcher", "job_runner", "build_submitter", "tag_mover"} {
			roles[r] = "projects/" + newProj + "/roles/fugaro" + strings.ToUpper(r[:1]) + r[1:]
		}
		r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
			"runs_bucket":               bucket,
			"registry_host":             "us-east5-docker.pkg.dev/" + newProj,
			"scheduler_service_account": "fugaro-scheduler@" + newProj + ".iam.gserviceaccount.com",
			"role_ids":                  roles,
		})}
		r.save(t)
		r.bill.AddAccount(billingAcc, true)
		out, _, err := r.run(t, true, newProj+"\n"+billingAcc+"\n"+initProjectName+"\n", "--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		b, ok := markerAt(t, r.gcs, bucket)
		if want := `{"version":1,"gcp_project":"` + newProj + `"}`; !ok || b != want {
			t.Errorf("gs://%s/%s = %q (present %v), want %s", bucket, managed.MarkerObject, b, ok, want)
		}
		if got, set := r.crm.Labels(newProj)[managed.ProjectLabelKey]; set {
			t.Errorf("%s=%q written on %s with managed projects off in this release", managed.ProjectLabelKey, got, newProj)
		}
		if strings.Contains(out, "it is Fugaro-managed") || !strings.Contains(out, "managed projects are off in this release") {
			t.Errorf("the run's line about the creation fact is not the one for managed projects off:\n%s", out)
		}
	})

	// (c'') The label write refused: the project can be read and not written
	// (no resourcemanager.projects.update — the shape every credential a run
	// holds is in), so the object lands and the label does not. It is the one
	// state where the two halves disagree, and what it must not do is report
	// the project as marked: the read answers "not managed" for a project
	// with the object and no label, so a run that said it marked one would be
	// telling the operator that confirmations are skipped for a project they
	// are not.
	t.Run("--create-project with the label refused", func(t *testing.T) {
		r := newProjectRig(t)
		bucket := "fugaro-runs-" + newProj
		r.gcs.AddProject(newProj, gcpfake.FirstCreatedProjectNumber)
		r.gcs.AddBucket(bucket, gcpfake.FirstCreatedProjectNumber, managedBucketLabels)
		roles := map[string]string{}
		for _, r := range []string{"launcher", "job_runner", "build_submitter", "tag_mover"} {
			roles[r] = "projects/" + newProj + "/roles/fugaro" + strings.ToUpper(r[:1]) + r[1:]
		}
		r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
			"runs_bucket":               bucket,
			"registry_host":             "us-east5-docker.pkg.dev/" + newProj,
			"scheduler_service_account": "fugaro-scheduler@" + newProj + ".iam.gserviceaccount.com",
			"role_ids":                  roles,
		})}
		r.save(t)
		r.bill.AddAccount(billingAcc, true)
		r.crm.DenyUpdate(newProj)
		out, _, err := r.run(t, true, newProj+"\n"+billingAcc+"\n"+initProjectName+"\n", "--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if _, ok := markerAt(t, r.gcs, bucket); !ok {
			t.Fatalf("no %s in gs://%s: the object is written first and this run wrote it\n%s", managed.MarkerObject, bucket, out)
		}
		if got := r.crm.Labels(newProj)[managed.ProjectLabelKey]; got != "" {
			t.Errorf("%s on %s = %q, want it unwritten: the API refused the update", managed.ProjectLabelKey, newProj, got)
		}
		if !strings.Contains(out, "not marked as managed") {
			t.Errorf("the run did not say the project is unmarked, and the read answers \"not managed\" for it:\n%s", out)
		}
		if strings.Contains(out, "recorded that this run created project "+newProj) {
			t.Errorf("the run reported a project marked when only half the fact was written:\n%s", out)
		}
		// And the state bucket is set up anyway: it is D77's, what an apply
		// under the deployer needs, and markCreated is its only writer in the
		// tree — a later run creates no project and returns before it. So a
		// run that stopped at the refused label takes with it the one step
		// that no command of ours can take later, and the paths a person is
		// sent to for the half-written mark end in a managed apply.
		state := managedmode.ManagedStateBucketName(newProj)
		if r.gcs.Inserted(state) == nil {
			t.Fatalf("the run that could not write the label created no gs://%s: a project no later run can make applyable\n%s", state, out)
		}
		member := "serviceAccount:" + managedmode.DeployerAccount(newProj)
		var bound bool
		for _, b := range r.gcs.BucketPolicy(state) {
			if b.Role == managedmode.DeployerStateRole && slices.Contains(b.Members, member) {
				bound = true
			}
		}
		if !bound {
			t.Errorf("%s is not bound to gs://%s: the state bucket step is the deployer's binding and nothing else does it (D75, Task 1.5): %v", member, state, r.gcs.BucketPolicy(state))
		}
		// The half-written mark's own account of itself: which owner-run
		// commands complete it, and the one route that works for the fact a
		// --managed creation writes. The warning this replaced gave both
		// marks to the creating run alone ("written once, by the run that
		// creates the project") and offered a command that refuses for a
		// fact that already records an amount.
		s := out
		if !strings.Contains(s, "records no amount") || !strings.Contains(s, "already records one") {
			t.Errorf("the unmarked warning does not split the two states the owner's commands answer to (a fact that records no amount, and one that does):\n%s", s)
		}
		if strings.Contains(s, "written once, by the run that creates the project") {
			t.Errorf("the unmarked warning still gives both marks to the creating run alone, and the owner's own flag writes both:\n%s", s)
		}
	})

	// (c') --create-project on a project that already exists and has no
	// Firebase: the stage adds Firebase where the project is, and creates
	// nothing. This is where D73's "adding Firebase is never what makes a
	// project managed" is decided — the AddFirebase branch of the same stage
	// that creates — so it is pinned here and not only through init
	// --firebase (b), a different stage. The bucket is the project's own,
	// seeded and numbered as (c) seeds it, so a mutation that marks the
	// project on adding Firebase really writes.
	t.Run("--create-project adding Firebase to an existing project", func(t *testing.T) {
		r := newProjectRig(t)
		const number = 424242
		bucket := "fugaro-runs-" + newProj
		r.crm.AddProject(newProj, number)
		r.gcs.AddProject(newProj, number)
		r.bill.SetBilling(newProj, true)
		r.gcs.AddBucket(bucket, number, managedBucketLabels)
		roles := map[string]string{}
		for _, r := range []string{"launcher", "job_runner", "build_submitter", "tag_mover"} {
			roles[r] = "projects/" + newProj + "/roles/fugaro" + strings.ToUpper(r[:1]) + r[1:]
		}
		r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
			"runs_bucket":               bucket,
			"registry_host":             "us-east5-docker.pkg.dev/" + newProj,
			"scheduler_service_account": "fugaro-scheduler@" + newProj + ".iam.gserviceaccount.com",
			"role_ids":                  roles,
		})}
		r.save(t)
		out, _, err := r.run(t, true, newProj+"\n"+initProjectName+"\n", "--create-project")
		if n := len(r.crm.Creates()); n != 0 {
			t.Fatalf("%d project(s) created (err %v):\n%s", n, err, out)
		}
		if n := len(r.fb.Adds()); n != 1 {
			t.Fatalf("Firebase added %d time(s), want once (err %v):\n%s", n, err, out)
		}
		if !strings.Contains(out, "added Firebase to project "+newProj) {
			t.Fatalf("the run did not reach the add (err %v):\n%s", err, out)
		}
		noMarker(t, r.gcs, bucket)
	})

	// (c'') --create-project with --firebase set to the same project: the
	// one-project layout the docs tell the owner to run ("One project or
	// two: either is supported", docs/gcp-setup.md, and the live checklist's
	// init step), where the Firebase backend lives in the project the run
	// creates. It is a different path to the same fact: with --firebase the
	// installation is the Firebase engine's first apply and the installation
	// stage is skipped (init_stages.go, newInstallationStage), so the
	// outputs markCreated reads — the runs bucket among them — are set by
	// that engine's writeConfig rather than by the installation stage, and
	// the fact still lands, once.
	//
	// Against a *different* Firebase project the flag is refused before
	// anything is called (TestCreateWithOtherFirebaseRefused); this is the
	// accepted combination, and until this case existed no test ran it.
	t.Run("--create-project with Firebase on the created project", func(t *testing.T) {
		r := newCreatedFBRig(t)
		bucket := "fugaro-runs-" + newProj
		out, _, err := r.run(t, true, newProj+"\n"+billingAcc+"\n"+names(1)+"us-east5\n"+names(1),
			"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New",
			"--firebase", newProj, "--budget-mode", "observe")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if n := len(r.crm.Creates()); n != 1 {
			t.Fatalf("%d project(s) created:\n%s", n, out)
		}
		b, ok := markerAt(t, r.gcs, bucket)
		if !ok {
			t.Fatalf("no %s in gs://%s after the run that created the project and put Firebase on it:\n%s", managed.MarkerObject, bucket, out)
		}
		if want := `{"version":1,"gcp_project":"` + newProj + `"}`; b != want {
			t.Errorf("gs://%s/%s = %s, want %s", bucket, managed.MarkerObject, b, want)
		}
		// Once, and on its own first try: a second write would have to get
		// past WriteMarker's IfGenerationMatch(0), and the run says so
		// exactly once. The warning below is what the run prints instead
		// when the fact cannot be written.
		if n := strings.Count(out, "recorded that this run created project "+newProj); n != 1 {
			t.Errorf("the run recorded the fact %d time(s), want once:\n%s", n, out)
		}
		if strings.Contains(out, "not marked as managed") {
			t.Errorf("the run warned that the created project is unmarked:\n%s", out)
		}
	})

	// (d) The project created, but with a runs bucket that is not the
	// project's own under the default name: a marker there would be a fact
	// nothing reads — the read of Task 0.2 locates the bucket the way init's
	// owner() does (init_review.go:183), by the default name — so the write
	// is refused with a warning rather than made where it cannot be found.
	t.Run("--create-project with a custom runs bucket", func(t *testing.T) {
		r := newProjectRig(t)
		r.gcs.AddProject(newProj, gcpfake.FirstCreatedProjectNumber)
		r.gcs.AddBucket("fugaro-runs-shared", gcpfake.FirstCreatedProjectNumber, managedBucketLabels)
		// The default-named bucket exists too, labelled and the project's
		// own, so a run that fell through to the name the read knows would
		// succeed in writing there, and the assertion below would see it.
		r.gcs.AddBucket("fugaro-runs-"+newProj, gcpfake.FirstCreatedProjectNumber, managedBucketLabels)
		r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"runs_bucket": "fugaro-runs-shared"})}
		r.save(t)
		r.bill.AddAccount(billingAcc, true)
		out, _, err := r.run(t, true, newProj+"\n"+billingAcc+"\n"+initProjectName+"\n", "--runs-bucket", "fugaro-runs-shared",
			"--create-project", "--link-billing", billingAcc, "--display-name", "Fugaro New")
		if err != nil && ExitCode(err) != ExitUserError {
			t.Fatalf("%v\n%s", err, out)
		}
		if n := len(r.crm.Creates()); n != 1 {
			t.Fatalf("%d project(s) created (err %v):\n%s", n, err, out)
		}
		noMarker(t, r.gcs, "fugaro-runs-shared")
		noMarker(t, r.gcs, "fugaro-runs-"+newProj)
		if !strings.Contains(out, "not fugaro-runs-"+newProj) {
			t.Errorf("the run did not say which bucket name it needs:\n%s", out)
		}
	})

	// (e) The project created, and the run stopped before any Google client
	// was built (no --link-billing: the run leaves billing for the owner and
	// never reaches the installation). There is no fact to write and no
	// client to write it with; what must not happen either way is silence:
	// the project is not managed, and the run says which project that is, so
	// the owner can mark it (D58's owner-run command, Task 6.1).
	t.Run("--create-project with no client built", func(t *testing.T) {
		r := newProjectRig(t)
		// The bucket a marker for the created project would go in, so the
		// assertion of absence reads a bucket that exists.
		r.gcs.AddBucket("fugaro-runs-"+newProj, gcpfake.FirstCreatedProjectNumber, managedBucketLabels)
		out, _, err := r.run(t, true, newProj+"\n", "--create-project", "--display-name", "Fugaro New")
		if err == nil {
			t.Fatalf("the run finished without a client:\n%s", out)
		}
		if n := len(r.crm.Creates()); n != 1 {
			t.Fatalf("%d project(s) created (err %v):\n%s", n, err, out)
		}
		if !strings.Contains(out, "no Google client was built for the run") || !strings.Contains(out, newProj) {
			t.Errorf("the run did not warn that the project it created is unmarked:\n%s", out)
		}
		// The bucket a marker for newProj would go in: the runs bucket of the
		// project this run created, not the rig's own installation bucket,
		// which no marker of this run could ever be written into.
		noMarker(t, r.gcs, "fugaro-runs-"+newProj)
	})
}

// createdFBRig is the rig of the one-project layout: the create rig (the
// project, its billing and Firebase Management) with the Firebase engine's
// own fakes on top, for `fugaro init --create-project --gcp-project P
// --firebase P`. The Firebase project is the one this run creates, so its
// database, its admins and its billing exist as the run itself leaves them,
// not as a rig seeds them.
type createdFBRig struct {
	*projectRig
	db    *gcpfake.RTDB
	fbdb  *gcpfake.FirebaseDB
	idt   *gcpfake.IdentityToolkit
	fs    *gcpfake.Firestore
	rules *gcpfake.FirebaseRules
	keys  *gcpfake.APIKeys
}

func newCreatedFBRig(t *testing.T) *createdFBRig {
	t.Helper()
	p := newProjectRig(t)
	r := &createdFBRig{projectRig: p, db: gcpfake.NewRTDB(t), fbdb: gcpfake.NewFirebaseDB(t)}
	r.idt = gcpfake.NewIdentityToolkit(t, nil, "key", newProj)
	r.fs, r.rules = gcpfake.NewFirestore(t), gcpfake.NewFirebaseRules(t, newProj)
	r.fs.RemoveDatabase() // a fresh Firebase project has none
	r.keys = gcpfake.NewAPIKeys(t)
	r.fbdb.AddInstanceFull(newProj, "us-central1", newProj+"-default-rtdb", "DEFAULT_DATABASE", "ACTIVE", r.db.URL)

	// The rest of the Firebase engine's endpoints, beside the ones the
	// create rig already wrote (cloud_billing, firebase_management).
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(string(b), "no_auth: true }", "firebase_database: "+r.fbdb.URL+"/, api_keys: "+r.keys.URL+
		"/, identity_toolkit: "+r.idt.URL+"/, firestore: "+r.fs.URL+", firebase_rules: "+r.rules.URL+", no_auth: true }", 1)
	if err := os.WriteFile(r.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	// Who may change a cap in the created project: the Firebase engine reads
	// the installation project's owners and editors before its first apply
	// and refuses a project with none.
	r.crm.SetPolicy(newProj, gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}})
	r.bill.AddAccount(billingAcc, true)

	// The created project's own runs bucket, as the installation would make
	// it, and the number both the bucket check and the fact's write are made
	// against (the number the fake gives the first project created through
	// projects.create).
	bucket := "fugaro-runs-" + newProj
	r.gcs.AddProject(newProj, gcpfake.FirstCreatedProjectNumber)
	r.gcs.AddBucket(bucket, gcpfake.FirstCreatedProjectNumber, managedBucketLabels)
	roles := map[string]string{}
	for _, role := range []string{"launcher", "job_runner", "build_submitter", "tag_mover"} {
		roles[role] = "projects/" + newProj + "/roles/fugaro" + strings.ToUpper(role[:1]) + role[1:]
	}
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"runs_bucket":               bucket,
		"registry_host":             "us-east5-docker.pkg.dev/" + newProj,
		"scheduler_service_account": "fugaro-scheduler@" + newProj + ".iam.gserviceaccount.com",
		"role_ids":                  roles,
		"history_service_account":   "fugaro-history@" + newProj + ".iam.gserviceaccount.com",
		"history_job":               nil,
	})}
	r.script["output@firebase"] = map[string]any{"stdout": r.firebaseOutputs(t)}
	r.save(t)

	// The history image the images stage mirrors into the created project's
	// registry, so the installation-2 apply (the history job) has one.
	r.ar.AddRepository(newProj, "us-east5", infra.BaseRegistry, managedBucketLabels)
	r.ar.SetTag(newProj, "us-east5", infra.BaseRegistry, infra.HistoryImagePackage, "latest")
	return r
}

// firebaseOutputs is the Firebase root's outputs, about the project this rig
// puts Firebase on.
func (r *createdFBRig) firebaseOutputs(t *testing.T) string {
	t.Helper()
	out := map[string]any{}
	for k, v := range map[string]string{"rtdb_url": r.db.URL, "firebase_api_key": fpAPIKey,
		"token_signer": "fugaro-token-signer@" + newProj + ".iam.gserviceaccount.com", "firebase_project": newProj} {
		out[k] = map[string]any{"value": v, "type": "string", "sensitive": false}
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestMarkCreatedWarningsAreInThePrintedResult pins the ordering rather than
// an end-to-end run, because under --json there is no end-to-end run that
// reaches it: the project stage is one --yes never covers (initflow's noYes),
// so a --json run leaves it for the user and creates no project — with
// --json, r.res.Created is always empty and markCreated has nothing to record
// or warn about. What must hold is the ordering itself: outcome records the
// creation fact before it prints the result, so a warning markCreated adds is
// in the object --json prints and not on stderr alone. Before the fix the
// marker warnings were the one thing appended to r.res after printResult had
// encoded it, so the day any path lets a --json run create a project (a
// programmatic project creation, or Task 6.1's marker command) it would have
// shipped a run whose account said "warnings": [] while the run said the
// project it had just created was not marked.
func TestMarkCreatedWarningsAreInThePrintedResult(t *testing.T) {
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	r := &initRun{cmd: cmd, w: &errOut, o: &initOptions{asJSON: true}, gcpProject: newProj, projectName: initProjectName}
	r.res.GCPProject, r.res.Created = newProj, newProj
	// No client: markCreated's first warning branch, and the one a run that
	// stopped before the installation stage takes.
	e := &initEngine{r: r}
	if err := e.outcome(context.Background(), &initflow.Result{}, nil); err != nil {
		t.Fatalf("%v", err)
	}
	var doc struct {
		Created  string   `json:"created_project"`
		Warnings []string `json:"warnings"`
	}
	if jerr := json.Unmarshal(out.Bytes(), &doc); jerr != nil {
		t.Fatalf("stdout is not the result object (%v):\n%s", jerr, out.String())
	}
	if doc.Created != newProj {
		t.Errorf("created_project = %q, want %q:\n%s", doc.Created, newProj, out.String())
	}
	var found bool
	for _, w := range doc.Warnings {
		if strings.Contains(w, "no Google client was built") && strings.Contains(w, newProj) {
			found = true
		}
	}
	if !found {
		t.Errorf("the marker warning is not in the printed result's warnings (%v):\n%s", doc.Warnings, out.String())
	}
	if strings.Contains(errOut.String(), "no Google client was built") && !found {
		t.Errorf("the warning went to stderr alone:\n%s", errOut.String())
	}
}

// managedBucketLabels is the label the installation puts on the runs bucket,
// and which WriteMarker proves before writing: a bucket without it is
// refused, so a test seeding one has to seed this too.
var managedBucketLabels = map[string]string{"fugaro": "managed"}

// markerAt is the creation fact's bytes in bucket, and whether it is there.
// Only an absent object is "not there": a bucket that does not exist, or a
// read that fails any other way, fails the test, since an assertion of
// absence against a bucket nothing could write into passes whatever the run
// did.
func markerAt(t *testing.T, g *gcpfake.GCS, bucket string) (string, bool) {
	t.Helper()
	ctx := context.Background()
	h := g.Client(t).Bucket(bucket)
	if _, err := h.Attrs(ctx); err != nil {
		t.Fatalf("gs://%s is unreadable (%v): an assertion about its marker would prove nothing", bucket, err)
	}
	rd, err := h.Object(managed.MarkerObject).NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return "", false
	}
	if err != nil {
		t.Fatalf("gs://%s/%s: %v", bucket, managed.MarkerObject, err)
	}
	defer rd.Close()
	b, err := io.ReadAll(rd)
	if err != nil {
		t.Fatalf("gs://%s/%s: %v", bucket, managed.MarkerObject, err)
	}
	return string(b), true
}

// noMarker asserts the run wrote no creation fact into bucket, a bucket that
// exists (markerAt fails on one that does not): nothing is skipped for a
// project nothing recorded.
func noMarker(t *testing.T, g *gcpfake.GCS, bucket string) {
	t.Helper()
	if b, ok := markerAt(t, g, bucket); ok {
		t.Fatalf("gs://%s/%s is there (%s): this run must not have written one", bucket, managed.MarkerObject, b)
	}
}

// managedRuns is markedRuns plus the creation fact: the project's runs bucket
// is the installation's own and holds fugaro/managed.json naming this GCP
// project, which is what Task 0.2's read (IsManaged) answers managed with. A
// marker for another project, or none, is the not-managed direction, and the
// tests below use both.
func (r *initRig) managedRuns(t *testing.T, name, gcp string) {
	t.Helper()
	r.markedRuns(t, name, gcp)
	// The project's own half of the fact (Task 3.0): a world with the object
	// and no label is not managed, so a test that means "managed" needs both.
	labelled(r.crm, gcp)
	body := fmt.Sprintf(`{"version":1,"gcp_project":%q}`, gcp)
	if err := r.gcs.Bucket(t, initRunsBucket).Bucket.WriteAll(context.Background(), managed.MarkerObject, []byte(body), nil); err != nil {
		t.Fatal(err)
	}
}

// stubManagedFact makes init's creation-fact read answer ok and err for this
// test, and counts the reads: the fake APIs answer a 403 or a 404 as "not
// managed" (by design) and the client retries a 5xx, so an error of the read
// itself is reached only through isManagedFact.
func stubManagedFact(t *testing.T, ok bool, err error) *int {
	t.Helper()
	reads := new(int)
	old := isManagedFact
	isManagedFact = func(context.Context, *apistorage.Service, *crm.Service, string, string) (bool, string, error) {
		*reads++
		return ok, "stubbed", err
	}
	t.Cleanup(func() { isManagedFact = old })
	return reads
}

// managedRefusal checks the text of a managed project's init refusal: it names
// the refused address, carries no flag, no agent marker and no DeleteHints
// line, and does not point at the owner's delete list (which approves nothing
// in fugaro's own roots). advice says whether it must end with
// initManagedDeleteAdvice or carry none at all.
func managedRefusal(t *testing.T, err error, address string, advice bool) {
	t.Helper()
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v; want the guard's refusal", ExitCode(err), err)
	}
	msg := err.Error()
	if !strings.Contains(msg, address) {
		t.Errorf("the managed refusal does not name %q:\n%s", address, msg)
	}
	if got := strings.Contains(msg, initManagedDeleteAdvice); got != advice {
		t.Errorf("the managed refusal carries the advice: %v, want %v:\n%s", got, advice, msg)
	}
	noFlagName(t, msg)
	for _, never := range []string{"hint: ", "allow-delete.json", "fugaro allow-delete", "Name an address"} {
		if strings.Contains(msg, never) {
			t.Errorf("the managed refusal carries %q:\n%s", never, msg)
		}
	}
	for _, marker := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_REMOTE", "CURSOR_AGENT", "AI_AGENT"} {
		if strings.Contains(msg, marker) {
			t.Errorf("the managed refusal names the marker %s:\n%s", marker, msg)
		}
	}
}

// Task 3.2 at the call site: in a managed project `fugaro init`'s guard
// refusal ends with the managed sentence — no flag, no DeleteHints line, the
// owner as the one who decides — and in any project that is not managed it is
// today's, byte for byte, hints and all. Only the text differs: what is
// refused is the same in both (the next test).
func TestManagedInitRefusalNamesNoFlag(t *testing.T) {
	const a = "user:a@example.com"
	address := `module.installation.google_storage_bucket_iam_member.state["` + a + `"]`

	// (a) A managed project, read through the real creation fact.
	r := newInitRig(t)
	r.managedRuns(t, initProjectName, initProject)
	r.setPlan(t, grantChange("state", a, "roles/storage.objectAdmin", "delete"))
	out, _, err := executeStdin(t, "", "init", "--yes")
	managedRefusal(t, err, address, true)
	if len(r.ran(t, "apply")) != 0 {
		t.Fatalf("a plan the guard refused was applied\n%s", out)
	}

	// (b) The same plan where the project is not managed is today's refusal.
	r2 := newInitRig(t)
	r2.stateBucket()
	r2.setPlan(t, grantChange("state", a, "roles/storage.objectAdmin", "delete"))
	out2, _, err := executeStdin(t, "", "init", "--yes")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out2)
	}
	want := "the plan would delete or replace 1 resource(s), or do something the guard doesn't know; nothing was applied. " +
		tf.AllowDeleteAdvice + ":\n  " + address
	if !strings.HasPrefix(err.Error(), want) || !strings.Contains(err.Error(), "--launcher and --operator") {
		t.Errorf("the unmanaged refusal is not today's (the flag and the hints):\n%s", err)
	}
	if len(r2.ran(t, "apply")) != 0 {
		t.Fatal("a plan the guard refused was applied")
	}
}

// Task 3.2's review, finding 2 (the owner's ruling): the refusal TEXT is all
// Task 3.2 changes. In fugaro's own roots a managed project's init approves
// and refuses exactly what any other project's does: the owner's delete list
// approves nothing here (D72/D74 scope it to a managed apply of the
// agent-authored root), the command line's --allow-delete keeps its meaning,
// and a plan with no delete is never refused because of the list or the flag.
func TestManagedInitApprovesExactlyWhatAnyInitApproves(t *testing.T) {
	const a = "user:a@example.com"
	address := `module.installation.google_storage_bucket_iam_member.state["` + a + `"]`

	// (c) An address on the owner's list does not permit an init --yes
	// delete, and the entry here is the strongest one there is: it names this
	// plan's own address and the identity this plan's own before view carries
	// (identityOf reads it back through the plan, so the two cannot drift). A
	// list that matched is the only way this row says anything — an object
	// whose before view identified nothing, or a version-1 object a reader
	// takes for no entries at all, would be refused whatever init did with
	// the list — and what it says is the SCOPING: fugaro init's guard is the
	// address half over fugaro's own roots (tf.Guard with the command line's
	// own --allow-delete, D72/D74), and the owner's entries are read by a
	// managed apply of the project's agent-authored root alone.
	//
	// The before view therefore carries the whole identity a bucket's removal
	// is identified by, time_created included (tf.removalInstanceAttributes):
	// without it tf.RemovedIdentity identifies nothing and tf.Approves refuses
	// "the plan does not identify the object this change removes" whatever
	// init did with the list — so the row would pass for the reason that makes
	// it say nothing.
	const bucket = "module.installation.google_storage_bucket.runs"
	r := newInitRig(t)
	r.managedRuns(t, initProjectName, initProject)
	ch := planChange{"address": bucket, "type": "google_storage_bucket",
		"change": map[string]any{"actions": []string{"delete"},
			"before": map[string]any{"id": "fugaro-runs-" + initProject, "name": "fugaro-runs-" + initProject, "project": initProject,
				"time_created": "2026-01-02T03:04:05.678Z"},
			"after": nil}}
	r.allowDeleteList(t, initProject, tf.DeleteEntry{Address: bucket, Before: identityOf(t, ch)})
	r.setPlan(t, ch)
	_, _, err := executeStdin(t, "", "init", "--yes")
	managedRefusal(t, err, bucket, true)
	if len(r.ran(t, "apply")) != 0 {
		t.Fatal("the owner's delete list approved a delete in fugaro's own root")
	}

	// (d) The command line's --allow-delete decides as it does in any
	// project: managed or not, the same plan with the same flag is applied.
	for name, managedProject := range map[string]bool{"managed": true, "not managed": false} {
		r := newInitRig(t)
		if managedProject {
			r.managedRuns(t, initProjectName, initProject)
		} else {
			r.stateBucket()
		}
		r.setPlan(t, grantChange("state", a, "roles/storage.objectAdmin", "delete"))
		out, _, err := executeStdin(t, "", "init", "--yes", "--allow-delete", address)
		if err != nil || len(r.ran(t, "apply")) != 1 {
			t.Errorf("%s: --allow-delete for the plan's delete: err %v, %d applies\n%s", name, err, len(r.ran(t, "apply")), out)
		}
	}

	// (e) A plan with no delete is never refused because of the list or the
	// flag: not with an unreadable list, not with an --allow-delete naming
	// nothing in the plan, and the creation fact is not even read for it.
	r = newInitRig(t)
	r.managedRuns(t, initProjectName, initProject)
	if err := r.gcs.Bucket(t, initRunsBucket).Bucket.WriteAll(context.Background(), managed.AllowDeleteObject, []byte("not a list"), nil); err != nil {
		t.Fatal(err)
	}
	reads := stubManagedFact(t, false, errors.New("must not be read"))
	out, _, err := executeStdin(t, "", "init", "--yes", "--allow-delete", address)
	if err != nil || len(r.ran(t, "apply")) != 1 {
		t.Fatalf("a plan with no delete: err %v, %d applies\n%s", err, len(r.ran(t, "apply")), out)
	}
	if *reads != 0 {
		t.Errorf("the creation fact was read %d time(s) for a plan the guard admits", *reads)
	}
}

// identityOf is the identity a plan's own before view carries for one change,
// read back through the plan the fake would report: it is tf.RemovedIdentity's
// answer and not a fixture's guess, so an entry built with it matches exactly
// what the gate compares.
func identityOf(t *testing.T, ch planChange) string {
	t.Helper()
	var p tf.Plan
	if err := json.Unmarshal([]byte(planJSON(t, ch)), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.ResourceChanges) != 1 {
		t.Fatalf("the change is %d resource changes, want 1", len(p.ResourceChanges))
	}
	return tf.RemovedIdentity(p.ResourceChanges[0]).String()
}

// Task 3.2's review, finding 5: a managed refusal made only of what no
// approval clears — an object moved here from another address, an action the
// guard does not know, a change with no actions — ends at what it refused.
func TestManagedInitRefusalOfWhatNoApprovalClearsHasNoAdvice(t *testing.T) {
	const addr = "module.installation.google_storage_bucket.runs"
	for name, ch := range map[string]planChange{
		"moved": {"address": addr, "type": "google_storage_bucket", "previous_address": "module.installation.google_storage_bucket.old",
			"action_reason": "delete_because_no_move_target", "change": map[string]any{"actions": []string{"delete"}, "before": map[string]any{}, "after": nil}},
		"unknown action": change(addr, "delete", "obliterate"),
		"no actions":     {"address": addr, "type": "google_storage_bucket", "change": map[string]any{"actions": []string{}, "before": map[string]any{}, "after": map[string]any{}}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newInitRig(t)
			r.managedRuns(t, initProjectName, initProject)
			r.setPlan(t, ch)
			// Listed on the command line, so the moved object's refusal is
			// the moved one rather than an unlisted delete's.
			_, _, err := executeStdin(t, "", "init", "--yes", "--allow-delete", addr)
			managedRefusal(t, err, addr, false)
			if len(r.ran(t, "apply")) != 0 {
				t.Fatal("a plan the guard refused was applied")
			}
		})
	}
}

// Task 3.2's review, finding 3: the creation-fact read fails closed. An error
// reading it is not "not managed" — the refusal names no flag and no hint and
// says the read failed — nothing is applied, and the error is not remembered:
// the next refusal reads again.
func TestManagedInitFactReadErrorFailsClosed(t *testing.T) {
	const a = "user:a@example.com"
	address := `module.installation.google_storage_bucket_iam_member.state["` + a + `"]`
	r := newInitRig(t)
	r.stateBucket()
	r.setPlan(t, grantChange("state", a, "roles/storage.objectAdmin", "delete"))
	reads := stubManagedFact(t, false, errors.New("the storage API answered 400"))
	_, _, err := executeStdin(t, "", "init", "--yes")
	managedRefusal(t, err, address, false)
	if !strings.Contains(err.Error(), "could not be read") || !strings.Contains(err.Error(), "the storage API answered 400") {
		t.Errorf("the refusal does not say the read failed:\n%s", err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Fatal("a plan the guard refused was applied")
	}
	if *reads != 1 {
		t.Errorf("%d reads, want 1", *reads)
	}

	// Not cached: an initRun whose read failed reads again, and a managed
	// answer after it is believed.
	run := &initRun{o: &initOptions{}, projectName: initProjectName, gcpProject: initProject}
	c := &infra.Clients{}
	if _, err := run.managedProject(context.Background(), c); err == nil {
		t.Fatal("the read's error was swallowed")
	}
	stubManagedFact(t, true, nil)
	if ok, err := run.managedProject(context.Background(), c); err != nil || !ok {
		t.Errorf("after a failed read, a managed answer gives %v, %v", ok, err)
	}
	// And "not managed" is not remembered either.
	run = &initRun{o: &initOptions{}, projectName: initProjectName, gcpProject: initProject}
	stubManagedFact(t, false, nil)
	if ok, _ := run.managedProject(context.Background(), c); ok {
		t.Fatal("not managed read as managed")
	}
	stubManagedFact(t, true, nil)
	if ok, _ := run.managedProject(context.Background(), c); !ok {
		t.Error("a not-managed answer was cached for the rest of the run")
	}
	// No clients is an error, never "not managed".
	if _, err := (&initRun{o: &initOptions{}}).managedProject(context.Background(), nil); err == nil {
		t.Error("a run with no clients read as not managed")
	}
}

// A run that creates a managed project is managed before its marker is
// written at the end of the converge, and once markCreated wrote it.
func TestManagedInitCreationRunIsManagedBeforeTheMarker(t *testing.T) {
	reads := stubManagedFact(t, false, nil)
	run := &initRun{o: &initOptions{}, projectName: initProjectName, gcpProject: initProject}
	run.o.managed = true
	run.res.Created = initProject
	if ok, err := run.managedProject(context.Background(), nil); err != nil || !ok {
		t.Errorf("a managed creation run reads %v, %v before its marker", ok, err)
	}
	// Created a different project, or created none: the fact decides.
	run.res.Created = "other-project"
	if ok, _ := run.managedProject(context.Background(), &infra.Clients{}); ok {
		t.Error("a run that created another project read this one as managed")
	}
	run.res.Created = ""
	if ok, _ := run.managedProject(context.Background(), &infra.Clients{}); ok {
		t.Error("a --managed run that created nothing read as managed")
	}
	if *reads != 2 {
		t.Errorf("%d reads, want 2", *reads)
	}
}

// Task 3.2's review, finding 7: every init call site of the guard gives the
// managed refusal in a managed project and applies nothing — the repository
// root, the Firebase root (applyRoot), the retirement and --forget, beside the
// installation root above.
func TestManagedInitRefusalAtEveryCallSite(t *testing.T) {
	t.Run("repo", func(t *testing.T) {
		stubManagedFact(t, true, nil)
		const addr = "module.repo.google_service_account.build"
		out, err, ri, _ := repoPlan(t, names(1), func(infra.RepoSpec) []planChange {
			return []planChange{{"address": addr, "type": "google_service_account",
				"change": map[string]any{"actions": []string{"delete"}, "before": map[string]any{}, "after": nil}}}
		})
		managedRefusal(t, err, addr, true)
		if len(ri.ran(t, "apply")) != 0 {
			t.Fatalf("applied\n%s", out)
		}
	})
	t.Run("firebase", func(t *testing.T) {
		stubManagedFact(t, true, nil)
		r := newFBRig(t)
		addr := `module.firebase.google_project_iam_member.admin["user:old@example.com"]`
		r.setPlan(t, planChange{"address": addr, "type": "google_project_iam_member",
			"change": map[string]any{"actions": []string{"delete"}, "before": map[string]any{}, "after": nil}})
		r.script["plan"] = map[string]any{"exit": 2}
		r.script["plan@installation"] = map[string]any{"exit": 0}
		r.script["show@installation"] = map[string]any{"stdout": `{"format_version":"1.2","resource_changes":[]}`}
		r.save(t)
		_, _, err := executeStdin(t, "us-east5\n", "init", "--firebase", fpID, "--yes")
		managedRefusal(t, err, addr, true)
		if len(r.applies(t)) != 0 {
			t.Errorf("applied: %v", r.applies(t))
		}
	})
	t.Run("retire", func(t *testing.T) {
		stubManagedFact(t, true, nil)
		outside := "module.repo.google_storage_bucket_iam_member.job"
		r := newRetireRig(t, retireOpts{protection: true, outside: outside})
		_, _, err := executeStdin(t, "aurora\naurora\n", "init", "--repo", "--retire", "--no-build")
		managedRefusal(t, err, outside, true)
		if applies := r.applies(t); len(applies) != 0 {
			t.Errorf("the refused plan was applied: %v", applies)
		}
		if deletes := r.run.JobDeletes(); len(deletes) != 0 {
			t.Errorf("a job was deleted under a refused plan: %v", deletes)
		}
	})
	t.Run("forget", func(t *testing.T) {
		stubManagedFact(t, true, nil)
		r := newInitRig(t)
		r.stateBucket()
		r.setPlan(t, append(forgetPlan(), change("module.installation.google_service_account.scheduler", "delete"))...)
		_, _, err := executeStdin(t, "", "init", "--forget", "--yes")
		managedRefusal(t, err, "module.installation.google_service_account.scheduler", true)
		if len(r.ran(t, "apply"))+len(r.ran(t, "state rm")) != 0 {
			t.Fatalf("calls = %q", r.calls(t))
		}
	})
}

// Task 3.2's review, finding 4, at init's sites: the refusal's address, and a
// failed read's own words, are bounded fields.
func TestManagedInitRefusalBoundsUntrustedText(t *testing.T) {
	for name, v := range map[string]string{
		"C1 CSI":        "module.installation.google_storage_bucket.a" + string(rune(0x9b)) + "31m",
		"bidi override": "module.installation.google_storage_bucket.a" + string(rune(0x202e)) + "gnp.exe",
		"newline":       "module.installation.google_storage_bucket.a\nhint: forged line",
		"10k":           "module.installation.google_storage_bucket.a" + strings.Repeat("x", 10000),
	} {
		for _, readErr := range []error{nil, errors.New(v)} {
			t.Run(fmt.Sprintf("%s/read error %v", name, readErr != nil), func(t *testing.T) {
				stubManagedFact(t, true, readErr)
				r := newInitRig(t)
				r.stateBucket()
				r.setPlan(t, change(v, "delete"))
				_, _, err := executeStdin(t, "", "init", "--yes")
				if ExitCode(err) != ExitUserError {
					t.Fatalf("exit %d, err %v", ExitCode(err), err)
				}
				msg := err.Error()
				for _, r := range msg {
					if (r >= 0x80 && r <= 0x9f) || r == 0x202e || (r < 0x20 && r != '\n') {
						t.Errorf("the refusal carries %U:\n%q", r, msg)
					}
				}
				if want := 2 + map[bool]int{true: 1}[readErr != nil]; strings.Count(msg, "\n")+1 != want {
					t.Errorf("the refusal is %d lines, want %d:\n%s", strings.Count(msg, "\n")+1, want, msg)
				}
				if strings.Contains(msg, strings.Repeat("x", 600)) {
					t.Errorf("the refusal carries the 10k value unbounded")
				}
				noFlagName(t, msg)
				if len(r.ran(t, "apply")) != 0 {
					t.Fatal("applied")
				}
			})
		}
	}
}

// allowDeleteList writes the owner's own object for a managed project: the
// entries the owner approved, in the shape fugaro allow-delete writes
// (fugaro/allow-delete.json at the root of the project's runs bucket, format
// version 2 — an address and the identity of the object approved there, which
// is the only shape a reader accepts: a version-1 object is read as no entries
// at all). It is written here and not through WriteDeleteList because this rig
// has no operator credential to write with, and what these tests are about is
// what a run READS, not what the owner's command writes
// (internal/infra/managed's own tests hold the writer).
func (r *initRig) allowDeleteList(t *testing.T, gcp string, entries ...tf.DeleteEntry) {
	t.Helper()
	body, err := json.Marshal(managed.DeleteList{Version: managed.AllowDeleteVersion, GCPProject: gcp, Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.gcs.Bucket(t, initRunsBucket).Bucket.WriteAll(context.Background(), managed.AllowDeleteObject, body, nil); err != nil {
		t.Fatal(err)
	}
}

// noFlagName fails when s names a command-line flag (Task 3.2's negative test:
// a refusal that names one is read by the session it refuses as a thing to
// pass).
func noFlagName(t *testing.T, s string) {
	t.Helper()
	for _, flag := range []string{
		"--allow-delete", "--allow-job-delete", "--launcher", "--operator", "--alert-email",
		"--budget", "--billing-account", "--yes", "--non-interactive", "--managed",
	} {
		if strings.Contains(s, flag) {
			t.Errorf("the refusal names %s:\n%s", flag, s)
		}
	}
	if i := strings.Index(s, "--"); i >= 0 {
		tail := s[i:]
		if len(tail) > 24 {
			tail = tail[:24]
		}
		t.Errorf("the refusal names a flag (%q):\n%s", tail, s)
	}
}
