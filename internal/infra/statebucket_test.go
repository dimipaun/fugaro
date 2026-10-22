package infra

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const testStateBucket = "fugaro-tfstate-proj-1234"

var tfstate = map[string]string{"fugaro": StateLabelValue}

func TestStateBucketCheck(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	exists, err := CheckStateBucket(ctx, f.c, "proj-1234", testStateBucket)
	if err != nil || exists {
		t.Fatalf("missing bucket: exists %v, err %v", exists, err)
	}
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	if exists, err := CheckStateBucket(ctx, f.c, "proj-1234", testStateBucket); err != nil || !exists {
		t.Fatalf("our bucket: exists %v, err %v", exists, err)
	}
}

// A state bucket of another project, or one without our mark, would hand
// the state (every name and account) to someone else: refused.
func TestStateBucketRefusesForeign(t *testing.T) {
	ctx := context.Background()
	for name, seed := range map[string]func(f *cloud){
		"other project": func(f *cloud) { f.gcs.AddBucket(testStateBucket, 999, tfstate) },
		"no mark":       func(f *cloud) { f.gcs.AddBucket(testStateBucket, testProjectNumber, managed) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newCloud(t)
			seed(f)
			_, err := CheckStateBucket(ctx, f.c, "proj-1234", testStateBucket)
			var ue *UserError
			if !errors.As(err, &ue) || !strings.Contains(err.Error(), testStateBucket) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// The new state bucket is versioned, private, marked and in the region,
// and project Viewers lose the read access GCS gives them by default.
func TestStateBucketCreate(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddProject("proj-1234", testProjectNumber)
	if err := CreateStateBucket(ctx, f.c, "proj-1234", "us-east5", testStateBucket); err != nil {
		t.Fatal(err)
	}
	in := f.gcs.Inserted(testStateBucket)
	if in["location"] != "us-east5" || !reflect.DeepEqual(in["labels"], map[string]any{"fugaro": "tfstate"}) ||
		!reflect.DeepEqual(in["versioning"], map[string]any{"enabled": true}) {
		t.Fatalf("inserted = %v", in)
	}
	iam, _ := in["iamConfiguration"].(map[string]any)
	ubla, _ := iam["uniformBucketLevelAccess"].(map[string]any)
	if ubla["enabled"] != true || iam["publicAccessPrevention"] != "enforced" {
		t.Fatalf("iamConfiguration = %v", iam)
	}
	lc, _ := in["lifecycle"].(map[string]any)
	rules, _ := lc["rule"].([]any)
	if len(rules) != 1 {
		t.Fatalf("lifecycle = %v", lc)
	}
	rule, _ := rules[0].(map[string]any)
	cond, _ := rule["condition"].(map[string]any)
	if cond["numNewerVersions"] != float64(21) || cond["isLive"] != false {
		t.Fatalf("lifecycle rule = %v", rule)
	}
	for _, b := range f.gcs.BucketPolicy(testStateBucket) {
		for _, m := range b.Members {
			if strings.HasPrefix(m, "projectViewer:") {
				t.Errorf("the state bucket still grants %s to %s", b.Role, m)
			}
		}
	}
	if exists, err := CheckStateBucket(ctx, f.c, "proj-1234", testStateBucket); err != nil || !exists {
		t.Fatalf("after create: exists %v, err %v", exists, err)
	}
}

// Only the projectViewer members of the two convenience reader bindings go;
// every other grant, conditional ones included, stays, and the set is
// matched on the etag of the policy that was read.
func TestRemoveProjectViewers(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	bucket := "fugaro-runs-proj-1234"
	f.gcs.AddBucket(bucket, testProjectNumber, managed)
	job := gcpfake.Binding{Role: roleObjectUser, Members: []string{"serviceAccount:a@proj-1234.iam.gserviceaccount.com"},
		Condition: &gcpfake.IAMCondition{Title: "t", Expression: "e"}}
	shared := gcpfake.Binding{Role: "roles/storage.legacyObjectReader", Members: []string{"projectViewer:proj-1234", "user:reader@example.com"}}
	policy := append(gcpfake.ConvenienceBindings("proj-1234")[:3:3], shared, job)
	f.gcs.SetBucketPolicy(bucket, policy)

	p, err := BucketPolicy(ctx, f.c, bucket)
	if err != nil {
		t.Fatal(err)
	}
	grants := ProjectViewerGrants(p)
	want := []string{"roles/storage.legacyBucketReader projectViewer:proj-1234", "roles/storage.legacyObjectReader projectViewer:proj-1234"}
	if !slices.Equal(grants, want) {
		t.Fatalf("grants = %q, want %q", grants, want)
	}
	if err := RemoveProjectViewers(ctx, f.c, bucket, p); err != nil {
		t.Fatal(err)
	}
	got := f.gcs.BucketPolicy(bucket)
	wantPolicy := []gcpfake.Binding{policy[0], policy[2],
		{Role: "roles/storage.legacyObjectReader", Members: []string{"user:reader@example.com"}}, job}
	if !reflect.DeepEqual(got, wantPolicy) {
		t.Fatalf("policy = %+v\nwant %+v", got, wantPolicy)
	}
	// The policy read before has a stale etag now: a second set is refused.
	if err := RemoveProjectViewers(ctx, f.c, bucket, p); err == nil {
		t.Fatal("a set with a stale etag succeeded")
	}
	sets := 0
	for _, r := range f.gcs.Requests() {
		if r.Method == "PUT" && strings.HasSuffix(r.Path, "/iam") {
			sets++
		}
	}
	if sets != 2 {
		t.Fatalf("%d policy sets, want 2", sets)
	}
}

func TestRepoStates(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	b := f.gcs.Bucket(t, testStateBucket)
	// A stale lock is not state: fugaro update --repo --forget leaves it, so it
	// must not block the installation's rollback.
	for _, k := range []string{"fugaro/installation/default.tfstate", "fugaro/repos/bitbucket-acme-sandbox/default.tfstate",
		"fugaro/repos/bitbucket-acme-other/default.tflock"} {
		if err := b.WriteAll(ctx, k, []byte("{}"), nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := RepoStates(ctx, f.c, testStateBucket)
	if err != nil || !slices.Equal(got, []string{"fugaro/repos/bitbucket-acme-sandbox/default.tfstate"}) {
		t.Fatalf("repo states = %q, %v", got, err)
	}
}

// The whole-project inventory reads every root's state, so the listing it
// takes is the whole bucket's *.tfstate objects — the installation's, the
// Firebase project's and every repository's — and nothing else: a lock is not
// state, and neither is anything that is not a state object.
func TestStateObjectsListsEveryRootsState(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	b := f.gcs.Bucket(t, testStateBucket)
	for _, k := range []string{
		"fugaro/installation/default.tfstate",
		"fugaro/firebase/default.tfstate",
		"fugaro/repos/bitbucket-acme-sandbox/default.tfstate",
		"fugaro/repos/bitbucket-acme-sandbox/default.tflock",
		"fugaro/repos/bitbucket-acme-sandbox/notes.txt",
		"fugaro/allow-delete.json",
	} {
		if err := b.WriteAll(ctx, k, []byte("{}"), nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := StateObjects(ctx, f.c, testStateBucket, "")
	want := []string{
		"fugaro/firebase/default.tfstate",
		"fugaro/installation/default.tfstate",
		"fugaro/repos/bitbucket-acme-sandbox/default.tfstate",
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("state objects = %q, %v, want %q", got, err, want)
	}
	// A prefix is the same read narrowed to one root, and the empty prefix of
	// the state bucket is not the whole installation: the managed root's state
	// lives in D77's own bucket (StatePrefixManaged).
	if got, err := StateObjects(ctx, f.c, testStateBucket, StatePrefixInstallation); err != nil ||
		!slices.Equal(got, []string{"fugaro/installation/default.tfstate"}) {
		t.Fatalf("installation states = %q, %v", got, err)
	}
	if got, err := StateObjects(ctx, f.c, testStateBucket, StatePrefixManaged); err != nil || len(got) != 0 {
		t.Fatalf("managed states in the installation's bucket = %q, %v, want none", got, err)
	}
}

// A listing GCS answers in pages is read to its last page: a state left on a
// page the read did not follow would account for nothing, and a project
// whose state was read short is one whose own resources read as foreign. The
// page size is the fake's to honour, so the test asks for one object a page.
func TestStateObjectsFollowsEveryPage(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	b := f.gcs.Bucket(t, testStateBucket)
	want := []string{
		"fugaro/firebase/default.tfstate",
		"fugaro/installation/default.tfstate",
		"fugaro/repos/bitbucket-acme-sandbox/default.tfstate",
	}
	for _, k := range want {
		if err := b.WriteAll(ctx, k, []byte("{}"), nil); err != nil {
			t.Fatal(err)
		}
	}
	defer func(n int64) { stateListPageSize = n }(stateListPageSize)
	stateListPageSize = 1
	before := f.gcs.ListCalls()
	got, err := StateObjects(ctx, f.c, testStateBucket, "")
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("state objects = %q, %v, want every page's %q", got, err, want)
	}
	if calls := f.gcs.ListCalls() - before; calls < len(want) {
		t.Errorf("the listing took %d page(s), want one per object", calls)
	}
}

// A listing that fails is an error, never an empty list: an empty list is
// "this bucket holds no state", and the inventory would read the project's own
// resources as foreign and blame the project for a read that was not made.
func TestStateObjectsListingThatFailsIsAnError(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	f.gcs.FailListings(testStateBucket)
	if got, err := StateObjects(ctx, f.c, testStateBucket, ""); err == nil {
		t.Fatalf("a refused listing answered %q and no error", got)
	}
}

// A bucket with no state at all — an installation that has applied nothing —
// is an empty list and not an error, which is the honest answer the accounting
// reads as "the state accounts for nothing". The listing's own failures are
// the caller's: a caller that could not list the states must fall back
// (Global Constraint 3), which is why the error is returned and not swallowed.
func TestStateObjectsOfABucketWithNothing(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	got, err := StateObjects(ctx, f.c, testStateBucket, "")
	if err != nil || len(got) != 0 {
		t.Fatalf("an empty state bucket listed %q, %v, want nothing", got, err)
	}
}

// Reading one state object is the other half of the accounting, and a read
// that fails is an error for the same reason a listing is.
func TestReadStateObject(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	b := f.gcs.Bucket(t, testStateBucket)
	state := []byte(`{"version":4,"resources":[]}`)
	if err := b.WriteAll(ctx, "fugaro/installation/default.tfstate", state, nil); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStateObject(ctx, f.c, testStateBucket, "fugaro/installation/default.tfstate")
	if err != nil || !slices.Equal(got, state) {
		t.Fatalf("state = %q, %v, want %q", got, err, state)
	}
	if _, err := ReadStateObject(ctx, f.c, testStateBucket, "fugaro/installation/gone.tfstate"); err == nil {
		t.Error("reading an object that is not there answered no error")
	}
}

// An object too large to be a state this program writes is refused rather
// than read into memory: what a bucket holds must not decide this program's
// memory. The limit is a variable here only so the test can hold a small
// object against it.
func TestReadStateObjectRefusesSomethingTooLarge(t *testing.T) {
	ctx := context.Background()
	f := newCloud(t)
	f.gcs.AddBucket(testStateBucket, testProjectNumber, tfstate)
	b := f.gcs.Bucket(t, testStateBucket)
	if err := b.WriteAll(ctx, "fugaro/installation/default.tfstate", []byte(strings.Repeat("x", 2048)), nil); err != nil {
		t.Fatal(err)
	}
	defer func(n int) { maxStateObject = n }(maxStateObject)
	maxStateObject = 1024
	got, err := ReadStateObject(ctx, f.c, testStateBucket, "fugaro/installation/default.tfstate")
	if err == nil {
		t.Fatalf("an object larger than the limit answered %d bytes and no error", len(got))
	}
	if !strings.Contains(err.Error(), "larger than") {
		t.Errorf("the error does not say the object is too large: %v", err)
	}
	// Exactly at the limit is a state this build reads: the check is on what
	// is over it, not on what reaches it.
	maxStateObject = 2048
	if got, err := ReadStateObject(ctx, f.c, testStateBucket, "fugaro/installation/default.tfstate"); err != nil || len(got) != 2048 {
		t.Fatalf("an object of exactly the limit read %d bytes, %v", len(got), err)
	}
}
