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
	for _, k := range []string{"fugaro/installation/default.tfstate", "fugaro/repos/bitbucket-acme-sandbox/default.tfstate"} {
		if err := b.WriteAll(ctx, k, []byte("{}"), nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := RepoStates(ctx, f.c, testStateBucket)
	if err != nil || !slices.Equal(got, []string{"fugaro/repos/bitbucket-acme-sandbox/default.tfstate"}) {
		t.Fatalf("repo states = %q, %v", got, err)
	}
}
