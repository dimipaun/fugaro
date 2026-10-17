package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"cloud.google.com/go/storage"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
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
	body := fmt.Sprintf(`{"version":1,"gcp_project":%q}`, gcp)
	if err := r.gcs.Bucket(t, initRunsBucket).Bucket.WriteAll(context.Background(), managed.MarkerObject, []byte(body), nil); err != nil {
		t.Fatal(err)
	}
}

// Task 3.2 at the call site: in a managed project `fugaro init`'s guard
// refusal is the managed one — it names the owner's own list and the owner-run
// command that writes it, and no flag — and infra.DeleteHints, whose whole
// purpose is to say which flags to re-pass, is not called at all there. In any
// project that is not managed the refusal is today's, byte for byte, hints and
// all: the removal D55 answered for is a managed project's and nothing else.
func TestManagedInitRefusalNamesTheOwnersListAndNoFlag(t *testing.T) {
	const a = "user:a@example.com"
	// (a) A managed project: the refusal names the owner's object, the
	// owner-run command that writes it and the address it refused, carries no
	// flag and no DeleteHints line, and applies nothing.
	r := newInitRig(t)
	r.managedRuns(t, initProjectName, initProject)
	r.setPlan(t, grantChange("state", a, "roles/storage.objectAdmin", "delete"))
	out, _, err := executeStdin(t, "", "init", "--yes")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	msg := err.Error()
	for _, want := range []string{
		"gs://" + initRunsBucket + "/fugaro/allow-delete.json",
		"fugaro allow-delete",
		`state["` + a + `"]`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the managed refusal does not name %q:\n%s", want, msg)
		}
	}
	noFlagName(t, msg)
	for _, marker := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_REMOTE", "CURSOR_AGENT", "AI_AGENT"} {
		if strings.Contains(msg, marker) {
			t.Errorf("the managed refusal names the marker %s:\n%s", marker, msg)
		}
	}
	if strings.Contains(msg, "hint: ") {
		t.Errorf("a managed refusal carried a hint, and DeleteHints tells a reader which flags to re-pass:\n%s", msg)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Fatal("a plan the guard refused was applied")
	}

	// (b) The same plan in a project that is not managed — no creation fact —
	// is today's refusal: the flag, and the hints beside it.
	r2 := newInitRig(t)
	r2.stateBucket()
	r2.setPlan(t, grantChange("state", a, "roles/storage.objectAdmin", "delete"))
	out2, _, err := executeStdin(t, "", "init", "--yes")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out2)
	}
	if !strings.Contains(err.Error(), "--allow-delete") || !strings.Contains(err.Error(), "--launcher and --operator") {
		t.Errorf("the unmanaged refusal is not today's (the flag and the hints):\n%s", err)
	}
	if len(r2.ran(t, "apply")) != 0 {
		t.Fatal("a plan the guard refused was applied")
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
