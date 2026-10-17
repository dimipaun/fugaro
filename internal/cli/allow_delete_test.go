package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/localcfg"
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

// TestAllowDeleteCommandAsksBeforeItDropsAnApproval: the write replaces the
// list, so an invocation that leaves an address out removes an approval the
// owner gave — and a silently dropped approval is the fail-closed direction
// only by accident: the owner believes it is still there. The command names
// the entries it is about to remove and asks for the project's name before it
// writes a list that drops any of them.
func TestAllowDeleteCommandAsksBeforeItDropsAnApproval(t *testing.T) {
	r := allowDeleteRig(t)
	if _, _, err := execute(t, "allow-delete", "google_storage_bucket.data", "google_cloud_run_v2_job.web"); err != nil {
		t.Fatal(err)
	}
	before, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("the list was not written")
	}
	// No terminal: the removal is refused, and nothing is written.
	out, _, err := execute(t, "allow-delete", "google_cloud_run_v2_job.web")
	if err == nil {
		t.Fatalf("the removal ran without a confirmation:\n%s", out)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d, want %d", ExitCode(err), ExitUserError)
	}
	if !strings.Contains(out, "google_storage_bucket.data") {
		t.Errorf("the command does not say which approval it would drop:\n%s", out)
	}
	// And the refusal names no flag this command lacks. It used to end "or
	// pass --yes once you have read what it does", which fugaro allow-delete
	// does not define (it registers the cloud flags and nothing else), so the
	// owner was told to pass a flag that does not exist. Every --token the
	// refusal prints is now checked against the command's own flags.
	assertNamesNoFlagItLacks(t, newAllowDeleteCmd(), err)
	if after, _ := allowDeleteAt(t, r.gcs, initRunsBucket); after != before {
		t.Errorf("a refused removal changed the list: %s → %s", before, after)
	}
	// A terminal, but the wrong name typed: still nothing written.
	terminal(t, true)
	out, _, err = executeStdin(t, "not-the-name\n", "allow-delete", "google_cloud_run_v2_job.web")
	if err == nil {
		t.Fatalf("a mistyped confirmation wrote the list:\n%s", out)
	}
	if after, _ := allowDeleteAt(t, r.gcs, initRunsBucket); after != before {
		t.Errorf("a mistyped confirmation changed the list: %s → %s", before, after)
	}
	// The project's name typed: the shorter list is written.
	out, _, err = executeStdin(t, initProjectName+"\n", "allow-delete", "google_cloud_run_v2_job.web")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("the confirmed removal wrote nothing")
	}
	want := `{"version":1,"gcp_project":"proj-1234","addresses":["google_cloud_run_v2_job.web"]}`
	if got != want {
		t.Errorf("gs://%s/%s = %s, want %s", initRunsBucket, managed.AllowDeleteObject, got, want)
	}
	// And a write that drops nothing asks nothing: the same list again is not
	// a removal.
	if _, _, err := execute(t, "allow-delete", "google_cloud_run_v2_job.web"); err != nil {
		t.Errorf("re-approving what is already approved asked for a confirmation: %v", err)
	}
}

// assertNamesNoFlagItLacks: a refusal that tells the owner to pass a flag the
// command does not define is advice that cannot be taken — fugaro allow-delete
// registers the cloud flags and nothing else, so "or pass --yes" named a flag
// that does not exist. Every --token the message prints is looked up in the
// command's own flags, the way the docs tests look a --flag up in a command
// before believing it.
func assertNamesNoFlagItLacks(t *testing.T, cmd *cobra.Command, err error) {
	t.Helper()
	if cmd.Flags().Lookup("yes") != nil {
		t.Fatalf("fugaro allow-delete defines --yes now: this assertion is about a command that has none")
	}
	for _, f := range strings.Fields(err.Error()) {
		name := docFlagToken(f)
		if name == "" {
			continue
		}
		if cmd.Flags().Lookup(name) == nil && cmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("the refusal names --%s, which this command does not define: %v", name, err)
		}
	}
}

// terminal patches the terminal test budget's way: the command asks for a
// typed confirmation only when stdin is one.
func terminal(t *testing.T, is bool) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return is }
	t.Cleanup(func() { stdinIsTerminal = old })
}

// TestAllowDeleteCommandWritesTheWholeList is the writer's contract: the
// owner's approval, recorded at the runs bucket's root, whole on every
// invocation — a shorter list refuses more, and nothing an owner took off the
// list stays deletable.
func TestAllowDeleteCommandWritesTheWholeList(t *testing.T) {
	r := allowDeleteRig(t)
	out, _, err := execute(t, "allow-delete", "google_storage_bucket.data", "google_cloud_run_v2_job.web", "google_cloud_run_v2_job.web")
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
	// The second invocation drops google_storage_bucket.data, so it is a
	// removal: it is confirmed the way any removal is (TestAllowDeleteCommand-
	// AsksBeforeItDropsAnApproval is where that rule is asserted).
	terminal(t, true)
	out, _, err = executeStdin(t, initProjectName+"\n", "allow-delete", "google_cloud_run_v2_job.web")
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

// TestAllowDeleteCommandRefusesAnAddressThatIsNotOne: an address is compared
// with a plan's by exact equality, so a stray space around one would be an
// approval of nothing at all. The command trims what it is given, and an
// argument that is nothing but space is the empty address — the message for
// it, not the one about the bucket the writer would otherwise fail on.
func TestAllowDeleteCommandRefusesAnAddressThatIsNotOne(t *testing.T) {
	r := allowDeleteRig(t)
	for _, arg := range []string{"", "   ", "\t"} {
		out, _, err := execute(t, "allow-delete", arg)
		if err == nil {
			t.Fatalf("the command accepted %q:\n%s", arg, out)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("%q: exit %d, want %d", arg, ExitCode(err), ExitUserError)
		}
		if !strings.Contains(err.Error(), "an empty address") {
			t.Errorf("%q was refused with the wrong reason: %v", arg, err)
		}
		if strings.Contains(err.Error(), "runs_bucket") {
			t.Errorf("%q was reported as a bucket problem: %v", arg, err)
		}
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
	}
	// And what it does write is trimmed, so the object holds addresses that
	// can match a plan's.
	if _, _, err := execute(t, "allow-delete", "  google_storage_bucket.data  "); err != nil {
		t.Fatal(err)
	}
	got, ok := allowDeleteAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatal("the trimmed address was not written")
	}
	want := `{"version":1,"gcp_project":"proj-1234","addresses":["google_storage_bucket.data"]}`
	if got != want {
		t.Errorf("gs://%s/%s = %s, want %s", initRunsBucket, managed.AllowDeleteObject, got, want)
	}
}

// TestAllowDeleteCommandNeedsTheDefaultRunsBucket: the managed objects are
// only ever read from the project's runs bucket under its default name — the
// rule init's owner() and markCreated already apply — so a list recorded in a
// custom bucket is one nothing looks in. The command refuses rather than
// recording an approval where the read will never find it.
func TestAllowDeleteCommandNeedsTheDefaultRunsBucket(t *testing.T) {
	r := allowDeleteRig(t)
	cfg, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	custom := strings.Replace(string(cfg), "runs_bucket: "+initRunsBucket, "runs_bucket: aurora-own-runs", 1)
	if custom == string(cfg) {
		t.Fatal("the rig's config does not name the default runs bucket")
	}
	if err := os.WriteFile(r.cfg, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"allow-delete", "google_storage_bucket.data"}, {"allow-delete"}} {
		out, _, err := execute(t, args...)
		if err == nil {
			t.Fatalf("%v ran against a custom runs bucket:\n%s", args, out)
		}
		if ExitCode(err) != ExitUserError {
			t.Errorf("%v: exit %d, want %d", args, ExitCode(err), ExitUserError)
		}
		if !strings.Contains(err.Error(), initRunsBucket) {
			t.Errorf("%v: the refusal does not name the name it needs: %v", args, err)
		}
	}
	if got, ok := allowDeleteAt(t, r.gcs, initRunsBucket); ok {
		t.Errorf("a refused run wrote gs://%s/%s = %s", initRunsBucket, managed.AllowDeleteObject, got)
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

// allowDeleteGeneration is the list's generation, 0 when it is absent, read
// from a listing so that a test that fails the object's reads can still see
// whether it was rewritten.
func allowDeleteGeneration(t *testing.T, g *gcpfake.GCS, bucket string) int64 {
	t.Helper()
	it := g.Client(t).Bucket(bucket).Objects(context.Background(), &storage.Query{Prefix: managed.AllowDeleteObject})
	attrs, err := it.Next()
	if err != nil {
		return 0
	}
	return attrs.Generation
}

// TestAllowDeleteCommandWritesNothingWhenItCannotReadTheList: the write
// replaces the list and asks before it drops an approval, so a read that
// failed must not be taken for "nothing approved" — that would drop every
// approval without the question. Nothing is written, and the error says why.
func TestAllowDeleteCommandWritesNothingWhenItCannotReadTheList(t *testing.T) {
	r := allowDeleteRig(t)
	if _, _, err := execute(t, "allow-delete", "google_storage_bucket.data", "google_cloud_run_v2_job.web"); err != nil {
		t.Fatal(err)
	}
	before := allowDeleteGeneration(t, r.gcs, initRunsBucket)
	if before == 0 {
		t.Fatal("the list was not written")
	}
	r.gcs.FailObjectGetsAfter(initRunsBucket, managed.AllowDeleteObject, 0)
	out, _, err := execute(t, "allow-delete", "google_compute_network.vpc")
	if err == nil {
		t.Fatalf("a write after a failed read ran:\n%s", out)
	}
	if !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("the refusal does not say nothing was written: %v", err)
	}
	if after := allowDeleteGeneration(t, r.gcs, initRunsBucket); after != before {
		t.Errorf("a write after a failed read rewrote the list (generation %d → %d)", before, after)
	}
}

// TestAllowDeleteCommandRefusesAListThatChangedUnderIt: the error a
// concurrent write produces is the owner's to act on, and says what to do.
func TestAllowDeleteCommandRefusesAListThatChangedUnderIt(t *testing.T) {
	lc := &localcfg.Config{Name: initProjectName, GCPProject: "proj-1234"}
	err := allowDeleteWriteErr(lc, initRunsBucket, fmt.Errorf("%w: generation 7", managed.ErrChanged))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "run the command again") {
		t.Errorf("allowDeleteWriteErr(ErrChanged) = %v (exit %d), want a user error that says to run it again", err, ExitCode(err))
	}
}
