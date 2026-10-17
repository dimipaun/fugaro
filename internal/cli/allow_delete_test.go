package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/managed"
)

// allowDeleteRig is the installation's own runs bucket, seeded as the read
// proves it: the fakes' project number, and the label the installation puts
// on it. The writer proves both before it writes, so a rig that wants its
// list recorded has to seed both.
func allowDeleteRig(t *testing.T) *initRig {
	t.Helper()
	noAgentSession(t)
	r := newInitRig(t)
	r.gcs.AddBucket(initRunsBucket, initProjectNumber, managedBucketLabels)
	return r
}

// allowDeleteAt is the delete list's bytes in bucket, and whether it is there.
func allowDeleteAt(t *testing.T, g *gcpfake.GCS, bucket string) (string, bool) {
	t.Helper()
	b, err := g.Bucket(t, bucket).Bucket.ReadAll(context.Background(), managed.AllowDeleteObject)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// TestAllowDeleteCommandWritesTheWholeList is the writer's contract: the
// owner's approval, recorded at the runs bucket's root, whole on every
// invocation — a shorter list refuses more, and nothing an owner took off the
// list stays deletable.
func TestAllowDeleteCommandWritesTheWholeList(t *testing.T) {
	r := allowDeleteRig(t)
	out, _, err := execute(t, "allow-delete", "google_storage_bucket.data", "google_cloud_run_v2_job.web")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// Sorted and deduplicated, so one list has one encoding: the object is
	// compared by eye and by generation.
	want := `{"version":1,"gcp_project":"proj-1234","addresses":["google_cloud_run_v2_job.web","google_storage_bucket.data"]}`
	got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("no %s in gs://%s after the command:\n%s", managed.AllowDeleteObject, initRunsBucket, out)
	}
	if got != want {
		t.Errorf("gs://%s/%s = %s, want %s", initRunsBucket, managed.AllowDeleteObject, got, want)
	}
	// A second invocation with one address replaces the list rather than
	// merging into it.
	out, _, err = execute(t, "allow-delete", "google_cloud_run_v2_job.web")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, ok = allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("the second command removed the list:\n%s", out)
	}
	if want := `{"version":1,"gcp_project":"proj-1234","addresses":["google_cloud_run_v2_job.web"]}`; got != want {
		t.Errorf("after the second command gs://%s/%s = %s, want %s (the whole list, not an append)", initRunsBucket, managed.AllowDeleteObject, got, want)
	}
}

// TestAllowDeleteCommandPrintsTheListAndWritesNothing: with no addresses the
// command prints what is approved. It never empties the list — a write that
// emptied it would refuse every delete under the appearance of a change, and
// the owner's way to refuse one is to write the list without it.
func TestAllowDeleteCommandPrintsTheListAndWritesNothing(t *testing.T) {
	r := allowDeleteRig(t)
	// No list yet: the print says so rather than writing an empty one.
	out, _, err := execute(t, "allow-delete")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "allows no address") {
		t.Errorf("with no list the command printed:\n%s", out)
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("printing the list wrote one: gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
	}
	if _, _, err := execute(t, "allow-delete", "google_storage_bucket.data", "google_cloud_run_v2_job.web"); err != nil {
		t.Fatal(err)
	}
	before, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("the list was not written")
	}
	out, _, err = execute(t, "allow-delete")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, address := range []string{"google_storage_bucket.data", "google_cloud_run_v2_job.web"} {
		if !strings.Contains(out, address) {
			t.Errorf("the print does not name %s:\n%s", address, out)
		}
	}
	if after, _ := allowDeleteAt(t, r.gcs, initRunsBucket); after != before {
		t.Errorf("printing the list changed it: %s → %s", before, after)
	}
}

// TestAllowDeleteCommandRefusesUnderACodingAgentMarker: the command records
// the owner's approval of deletes in the cloud with the owner's own
// credentials, and a coding agent's session holds those credentials and could
// pass the flag itself — so nothing is written from one, whatever the
// arguments.
func TestAllowDeleteCommandRefusesUnderACodingAgentMarker(t *testing.T) {
	r := allowDeleteRig(t)
	t.Setenv("CLAUDECODE", "1")
	out, _, err := execute(t, "allow-delete", "google_storage_bucket.data")
	if err == nil {
		t.Fatalf("the command ran in a coding agent's session:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d (a user error: nothing was attempted)", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(err.Error(), "CLAUDECODE") {
		t.Errorf("the refusal does not name the marker: %v", err)
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
	}
	// Printing the list is a read, but it is reached by the same command, so
	// it is refused from an agent's session too: the command's whole surface
	// is the owner's, and a semi-idempotent half of it is a half that can be
	// mistaken for the approval path.
	if _, _, err := execute(t, "allow-delete"); err == nil {
		t.Error("printing the list is allowed in a coding agent's session")
	}
}

// TestAllowDeleteCommandRefusesABucketThatIsNotTheProjectsOwn: a bucket name
// is global, so a list written into a stranger's bucket of the same name
// would be one nothing reads and the owner's approval would be lost. The
// command says which bucket it needs rather than writing into it.
func TestAllowDeleteCommandRefusesABucketThatIsNotTheProjectsOwn(t *testing.T) {
	// The bucket the rig seeds is the project's own; each row replaces it with
	// one that is not, so the assertion is about the proof rather than about a
	// bucket that is not there at all (internal/infra/managed's own test
	// covers the absent bucket, which no CLI rig can seed away).
	for name, seed := range map[string]func(*testing.T, *initRig){
		"no label": func(t *testing.T, r *initRig) { r.gcs.AddBucket(initRunsBucket, initProjectNumber, nil) },
		"another project number": func(t *testing.T, r *initRig) {
			r.gcs.AddBucket(initRunsBucket, initProjectNumber+1, managedBucketLabels)
		},
		"a mark of someone else": func(t *testing.T, r *initRig) {
			r.gcs.AddBucket(initRunsBucket, initProjectNumber, map[string]string{"fugaro": "managed-by-someone"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := allowDeleteRig(t)
			seed(t, r)
			out, _, err := execute(t, "allow-delete", "google_storage_bucket.data")
			if err == nil {
				t.Fatalf("the command wrote gs://%s/%s:\n%s", initRunsBucket, managed.AllowDeleteObject, out)
			}
			if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
				t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
			}
			if !strings.Contains(err.Error(), initRunsBucket) || !strings.Contains(err.Error(), initProjectName) {
				t.Errorf("the refusal names neither the bucket nor the project: %v", err)
			}
		})
	}
}
