package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
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

	// (d) The project created, but with a runs bucket that is not the
	// project's own under the default name: a marker there would be a fact
	// nothing reads — the read of Task 0.2 locates the bucket the way init's
	// owner() does (init_review.go:183), by the default name — so the write
	// is refused with a warning rather than made where it cannot be found.
	t.Run("--create-project with a custom runs bucket", func(t *testing.T) {
		r := newProjectRig(t)
		r.gcs.AddProject(newProj, gcpfake.FirstCreatedProjectNumber)
		r.gcs.AddBucket("fugaro-runs-shared", gcpfake.FirstCreatedProjectNumber, managedBucketLabels)
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
		noMarker(t, r.gcs, initRunsBucket)
	})
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
func markerAt(t *testing.T, g *gcpfake.GCS, bucket string) (string, bool) {
	t.Helper()
	b, err := g.Bucket(t, bucket).Bucket.ReadAll(context.Background(), managed.MarkerObject)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// noMarker asserts the run wrote no creation fact: nothing is skipped for a
// project nothing recorded.
func noMarker(t *testing.T, g *gcpfake.GCS, bucket string) {
	t.Helper()
	if b, ok := markerAt(t, g, bucket); ok {
		t.Fatalf("gs://%s/%s is there (%s): this run must not have written one", bucket, managed.MarkerObject, b)
	}
}
