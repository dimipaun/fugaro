package cli

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/managed"
)

// The other half of what the managed creation leaves behind: beside Task 0.1's
// creation fact, the managed run sets up D77's dedicated state bucket and binds
// the deployer to it on that bucket alone (Task 1.5 (3) of
// docs/plans/2026-10-16-managed-projects.md).
//
// Both steps live in markCreated and nowhere else, and both are gated on the
// run having created the project: they are setup on the operator's own
// credentials, in the one moment the runs bucket exists and the operator is
// still there. This test drives markCreated directly rather than through a full
// converge — what it pins is the call and its gate, and the object-level
// behaviour of the step itself is internal/managed's own tests
// (statebucket_test.go).
func TestMarkCreatedSetsUpTheManagedStateBucket(t *testing.T) {
	const bucket = "fugaro-runs-" + newProj

	// managedCreation is a run that created newProj, with its clients pointed
	// at this rig's fakes.
	managedCreation := func(t *testing.T) (*initRig, *initEngine, *bytes.Buffer) {
		t.Helper()
		r := newInitRig(t)
		lc, err := localcfg.Load(r.cfg)
		if err != nil {
			t.Fatal(err)
		}
		c, err := newInitClients(t.Context(), lc)
		if err != nil {
			t.Fatal(err)
		}
		// The created project, as the fakes know one a run creates: its own
		// number, and its own runs bucket, labelled as the installation makes
		// it (WriteMarker proves both before it writes).
		const number = gcpfake.FirstCreatedProjectNumber
		r.crm.AddProject(newProj, number)
		r.gcs.AddProject(newProj, number)
		r.gcs.AddBucket(bucket, number, managedBucketLabels)

		var out bytes.Buffer
		// --managed with its required budget trio (Task 4.2): markCreated
		// reads the options for the amount the creation fact records.
		o := &initOptions{managed: true, budget: 100, budgetCurrency: "USD", billingAccount: "000000-000000-000000"}
		run := &initRun{o: o, cmd: NewRootCmd(), w: &out, gcpProject: newProj}
		run.res.Created, run.res.Outputs = newProj, &infra.InstallationOutputs{RunsBucket: bucket}
		e := &initEngine{r: run, c: c, lc: lc, spec: infra.InstallationSpec{Project: newProj, Region: "us-east5"}}
		return r, e, &out
	}

	t.Run("a managed creation", func(t *testing.T) {
		r, e, out := managedCreation(t)
		e.markCreated(t.Context())

		want := "fugaro-managed-tfstate-" + newProj
		if e.r.res.Warnings != nil {
			t.Fatalf("the managed creation warned (%v):\n%s", e.r.res.Warnings, out.String())
		}
		inserted := r.gcs.Inserted(want)
		if inserted == nil {
			t.Fatalf("gs://%s was not created by the run that created the project:\n%s", want, out.String())
		}
		if got := inserted["location"]; got != "us-east5" {
			t.Errorf("gs://%s was created in %v, want the installation's region us-east5", want, got)
		}
		if got := inserted["versioning"]; got == nil {
			t.Errorf("gs://%s was created without versioning, which the installation's own state bucket has: %v", want, inserted)
		}
		// The deployer, on that bucket, and nobody else by this step: the
		// binding is the role the deployer holds, unconditional (objectAdmin
		// has no condition in D75's three bindings), and its member is the
		// account the installation module creates.
		member := "serviceAccount:fugaro-deployer@" + newProj + ".iam.gserviceaccount.com"
		var found bool
		for _, b := range r.gcs.BucketPolicy(want) {
			if b.Role == managed.DeployerStateRole && b.Condition != nil {
				t.Errorf("the deployer's binding carries condition %v", b.Condition)
			}
			if b.Role == managed.DeployerStateRole && slices.Contains(b.Members, member) {
				found = true
			}
			if b.Role == managed.DeployerStateRole && !slices.Contains(b.Members, member) {
				t.Errorf("roles/storage.objectAdmin carries %v, which this step did not add", b.Members)
			}
		}
		if !found {
			t.Fatalf("%s is not bound to %s on gs://%s: %v", member, managed.DeployerStateRole, want, r.gcs.BucketPolicy(want))
		}
		// The state of the agent-authored root is a bucket of its own: this
		// step creates the dedicated one and no other (the installation's own
		// state bucket is never made by this run), and the runs bucket, which
		// is there, gains no deployer.
		if r.gcs.Inserted(initStateBucket) != nil {
			t.Errorf("the run created gs://%s, the installation's own state bucket", initStateBucket)
		}
		for _, b := range r.gcs.BucketPolicy(bucket) {
			if slices.Contains(b.Members, member) {
				t.Errorf("%s is bound to gs://%s, which is not the managed state bucket", member, bucket)
			}
		}
		if !strings.Contains(out.String(), "gs://"+want) {
			t.Errorf("the run's account does not name the managed state bucket:\n%s", out.String())
		}
	})

	// The gate, the same one the marker has: a run that created no project sets
	// up nothing. This is every ordinary run of an existing installation —
	// fugaro's own, belong, the owner's.
	t.Run("a run that created no project", func(t *testing.T) {
		r, e, out := managedCreation(t)
		e.r.res.Created, e.r.gcpProject = "", initProject
		e.markCreated(t.Context())
		for _, name := range []string{"fugaro-managed-tfstate-" + newProj, "fugaro-managed-tfstate-" + initProject} {
			if r.gcs.Inserted(name) != nil {
				t.Errorf("a run that created no project created gs://%s:\n%s", name, out.String())
			}
		}
	})

	// And the fail-closed direction the marker also takes: when the step cannot
	// be done, the run says so and still ends as the run it was. Here the
	// installation's region is not known — the spec is the one thing this step
	// takes from outside, so an empty one is what a run reaches this with when
	// it never got as far as building an installation spec.
	t.Run("no region", func(t *testing.T) {
		r, e, out := managedCreation(t)
		e.spec.Region = ""
		e.markCreated(t.Context())
		if r.gcs.Inserted("fugaro-managed-tfstate-"+newProj) != nil {
			t.Errorf("the managed state bucket was created without a region:\n%s", out.String())
		}
		var found bool
		for _, w := range e.r.res.Warnings {
			if strings.Contains(w, "managed state bucket") && strings.Contains(w, newProj) {
				found = true
			}
		}
		if !found {
			t.Errorf("the failed setup is not a warning naming the project (%v):\n%s", e.r.res.Warnings, out.String())
		}
	})
}
