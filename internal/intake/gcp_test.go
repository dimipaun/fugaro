// gcp_test.go holds the two production adapters to their contract over
// objects the repository already trusts: internal/gcpfake speaks Secret
// Manager's own API and records what it was asked, and the runs bucket is a
// directory under t.TempDir(). Nothing here reaches a network beyond the
// loopback the fake listens on.
//
// What this file is for is the half the routes' tests cannot see: that the
// five calls the account holds are the five calls made, over the filters and
// the labels this package writes, with no read of a version's payload
// anywhere.

package intake

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/secretname"
	"google.golang.org/api/option"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

// testProject is the project the adapters are pointed at. It is a name and not
// an installation: the fake is the only thing that reads it.
const testProject = "aurora-dev"

// testRegion is the installation's own region, as the deployment names it: the
// one a container the intake creates is replicated in (D3082).
const testRegion = "europe-west1"

// fakeSecrets starts the Secret Manager fake and returns the adapter over it,
// for a deployment that names region. An empty region is the deployment of a
// service from before LP5a's variable existed (TestIntakeWithNoRegionIsAutomatic).
func fakeSecrets(t *testing.T, region string) (*gcpfake.Secrets, *SecretManager) {
	t.Helper()
	f := gcpfake.NewSecrets(t)
	store, err := NewSecretManager(context.Background(), testProject, region,
		option.WithoutAuthentication(), option.WithEndpoint(f.URL+"/"))
	if err != nil {
		t.Fatalf("connecting the intake's store to the fake: %v", err)
	}
	return f, store
}

// ownerLabels is the label set the intake writes for a per-user container,
// spelled here as the adapter must spell it on the wire.
func ownerLabels(sub string) map[string]string {
	return map[string]string{LabelScope: string(ScopeUser), LabelOwnerSub: sub}
}

// TestSecretManagerReadsTheWorkflowSecretMarker (F18, G2): the marker is a
// label, and secretFromAPI is the only place it reaches the Secret the write
// path compares — the list call answers labels and nothing else does. No other
// test in this tree can catch a production read that drops them: the routes'
// own tests seed fakeStore, which hands the whole Secret back (server_test.go),
// so a marker that never left Secret Manager reads as "unmarked" there and in
// every test, while in production every rotation of a workflow's secret would
// be refused as another writer's container and `fugaro secrets shared set NAME`
// would write a project-shared credential into the workflow's own.
func TestSecretManagerReadsTheWorkflowSecretMarker(t *testing.T) {
	f, store := fakeSecrets(t, testRegion)
	marked := secretname.ProjectPrefix + "npm-token"
	plain := secretname.ProjectPrefix + "model-key"
	f.Seed(marked, map[string]string{LabelScope: string(ScopeShared), LabelWorkflowSecret: WorkflowSecretMarker}, []byte(testValue))
	f.Seed(plain, map[string]string{LabelScope: string(ScopeShared)}, []byte(testValue))

	// The write path's read: Describe, and the class it decides from.
	got, found, err := store.Describe(context.Background(), marked)
	if err != nil || !found {
		t.Fatalf("describing the workflow's container: %v, found %v", err, found)
	}
	if got.Labels[LabelWorkflowSecret] != WorkflowSecretMarker {
		t.Fatalf("the described container's labels are %v, want %s=%s: the write path reads the marker from here alone (G2)",
			got.Labels, LabelWorkflowSecret, WorkflowSecretMarker)
	}
	// The other class's container is read the same way, so a label set that
	// arrived short — the scope without the marker, or nothing at all — reads
	// as this one, and the check above is not passing by accident.
	got, found, err = store.Describe(context.Background(), plain)
	if err != nil || !found {
		t.Fatalf("describing the credential's container: %v, found %v", err, found)
	}
	if _, ok := got.Labels[LabelWorkflowSecret]; ok {
		t.Fatalf("a project-shared credential's container answers the workflow-secret marker: %v", got.Labels)
	}
	if got.Labels[LabelScope] != string(ScopeShared) {
		t.Fatalf("the described container's scope label is %q, want %q: labels have to arrive whole", got.Labels[LabelScope], ScopeShared)
	}

	// And the service's read is the same label through the list call (IS1,
	// G2): the marker has to survive that one too.
	listed, err := store.List(context.Background(), ScopeShared)
	if err != nil {
		t.Fatalf("listing the shared scope: %v", err)
	}
	markers := map[string]string{}
	for _, s := range listed {
		markers[s.ID] = s.Labels[LabelWorkflowSecret]
	}
	if len(markers) != 2 {
		t.Fatalf("the shared scope listed %d containers, want the two seeded ones: %v", len(markers), markers)
	}
	if markers[marked] != WorkflowSecretMarker || markers[plain] != "" {
		t.Fatalf("the shared listing answered markers %v, want %s for %s and none for %s",
			markers, WorkflowSecretMarker, marked, plain)
	}
}

// TestSecretManagerDescribesOnlyItsOwnContainers: a container is described
// through the list call — there is no secrets.get in the grant — and a
// container another writer made without the intake's labels is not found,
// which is what makes the write path refuse it instead of writing into it
// (R12).
func TestSecretManagerDescribesOnlyItsOwnContainers(t *testing.T) {
	f, store := fakeSecrets(t, testRegion)
	id := secretname.UserPrefix + testOwnerSub + "_api-key"
	other := secretname.UserPrefix + testOwnerSub + "_unlabelled"
	f.Seed(id, ownerLabels(testOwnerSub), []byte(testValue))
	f.Seed(other, nil, []byte(testValue))

	got, found, err := store.Describe(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("describing a container of the intake's own: %v, found %v", err, found)
	}
	if got.ID != id {
		t.Fatalf("described %q, want %q", got.ID, id)
	}
	if _, found, err := store.Describe(context.Background(), other); err != nil || found {
		t.Fatalf("describing a container with no labels of the intake's: %v, found %v — want not found", err, found)
	}
	if _, _, err := store.Describe(context.Background(), "not-an-ID"); err == nil {
		t.Fatal("describing a name the grammar does not build was not refused")
	}

	// Every call is the list call. A secrets.get would be answered by the
	// fake and by nothing else: the deployment grants the intake no get
	// (DS2.2), so a read that asked for one would fail in production and only
	// there.
	for _, r := range f.Requests() {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.Path, "/secrets") {
			t.Fatalf("the intake called %s %s: the list call is the only read it may make", r.Method, r.Path)
		}
		query, err := url.ParseQuery(r.Query)
		if err != nil {
			t.Fatalf("the list call's query %q does not parse: %v", r.Query, err)
		}
		filter := query.Get("filter")
		for _, term := range []string{"labels." + LabelScope + "=" + string(ScopeUser), "labels." + LabelOwnerSub + "=" + testOwnerSub} {
			if !strings.Contains(filter, term) {
				t.Fatalf("the list call's filter %q does not carry %q, the label the intake writes", filter, term)
			}
		}
	}
}

// TestSecretManagerListsOneScope: the list narrows to one scope by the label,
// and a container whose name says another scope is dropped rather than
// answered — the name is the truth about a container and the label is only
// how it was found.
func TestSecretManagerListsOneScope(t *testing.T) {
	f, store := fakeSecrets(t, testRegion)
	mine := secretname.UserPrefix + testOwnerSub + "_api-key"
	yours := secretname.UserPrefix + testOtherSub + "_api-key"
	shared := secretname.ProjectPrefix + "model-key"
	f.Seed(mine, ownerLabels(testOwnerSub), []byte(testValue))
	f.Seed(yours, ownerLabels(testOtherSub), []byte(testValue))
	f.Seed(shared, map[string]string{LabelScope: string(ScopeShared)}, []byte(testValue))
	// A container carrying a per-user label and a shared name: another writer
	// can label a container anything, and this intake does not answer for it.
	f.Seed(shared+"-mislabelled", ownerLabels(testOwnerSub), []byte(testValue))

	users, err := store.List(context.Background(), ScopeUser)
	if err != nil {
		t.Fatalf("listing the per-user scope: %v", err)
	}
	var ids []string
	for _, s := range users {
		ids = append(ids, s.ID)
	}
	if len(ids) != 2 || ids[0] != mine || ids[1] != yours {
		t.Fatalf("the per-user scope listed %v, want %v", ids, []string{mine, yours})
	}
	shareds, err := store.List(context.Background(), ScopeShared)
	if err != nil {
		t.Fatalf("listing the shared scope: %v", err)
	}
	if len(shareds) != 1 || shareds[0].ID != shared {
		t.Fatalf("the shared scope listed %v, want %v", shareds, shared)
	}
}

// TestSecretManagerCreateSendsTheIntakeShape: the container is made with the
// annotations and the labels the routes computed, with the replication the
// deployment's region asks for and with nothing else — no TTL, no expiry, no
// topic, no rotation — so a container this store made is one the shape check
// accepts on the next call (R12). A second create of the same name is
// ErrExists and not a failure.
func TestSecretManagerCreateSendsTheIntakeShape(t *testing.T) {
	f, store := fakeSecrets(t, testRegion)
	id := secretname.UserPrefix + testOwnerSub + "_api-key"
	wantAnnotations := map[string]string{
		AnnotationOwnerSub:   testOwnerSub,
		AnnotationOwnerEmail: testOwnerEmail,
		AnnotationCreatedBy:  testOwnerEmail,
	}
	ctx := context.Background()
	if err := store.Create(ctx, id, wantAnnotations, ownerLabels(testOwnerSub)); err != nil {
		t.Fatalf("creating the container: %v", err)
	}
	if err := store.Create(ctx, id, wantAnnotations, ownerLabels(testOwnerSub)); !errors.Is(err, ErrExists) {
		t.Fatalf("creating a container that is there: %v, want ErrExists", err)
	}

	var body map[string]any
	for _, r := range f.Requests() {
		if r.Method == http.MethodPost {
			if err := json.Unmarshal(r.Body, &body); err != nil {
				t.Fatalf("the create body is not JSON: %v", err)
			}
		}
	}
	if body == nil {
		t.Fatal("no create was sent")
	}
	if body["replication"] == nil {
		t.Fatal("the container was created without a replication policy")
	}
	for _, key := range []string{"ttl", "expireTime", "topics", "rotation", "customerManagedEncryption"} {
		if _, ok := body[key]; ok {
			t.Fatalf("the create carried %q: the intake writes none of these, and a container carrying one is refused on the next call", key)
		}
	}
	annotations, _ := body["annotations"].(map[string]any)
	for k, v := range wantAnnotations {
		if annotations[k] != v {
			t.Fatalf("the create carried annotation %s=%v, want %q", k, annotations[k], v)
		}
	}
	labels, _ := body["labels"].(map[string]any)
	if labels[LabelScope] != string(ScopeUser) || labels[LabelOwnerSub] != testOwnerSub {
		t.Fatalf("the create carried the labels %v, which a later call could not find it by", labels)
	}
}

// TestAnIntakeSecretIsInTheRegion: the container this process creates is
// user-managed with one replica in the region its deployment names, and not
// automatic — which Google documents as global, and which an EU installation
// may not have (D3082). The policy is read back off the container the fake
// stored, so it is what the create really carried and not what this file
// believes it sent.
func TestAnIntakeSecretIsInTheRegion(t *testing.T) {
	f, store := fakeSecrets(t, testRegion)
	id := secretname.UserPrefix + testOwnerSub + "_api-key"
	ctx := context.Background()
	if err := store.Create(ctx, id, map[string]string{AnnotationCreatedBy: testOwnerEmail}, ownerLabels(testOwnerSub)); err != nil {
		t.Fatalf("creating the container: %v", err)
	}
	if got := f.Replication(id); !slices.Equal(got, []string{testRegion}) {
		t.Fatalf("the container is replicated in %v, want only the deployment's region %q", got, testRegion)
	}
}

// TestIntakeWithNoRegionIsAutomatic: a service deployed before LP5a's variable
// existed names no region, and it creates the containers it always did —
// automatic — rather than refusing to write or guessing a region for itself.
// A container that already exists is untouched either way, which the second
// half pins: the create is the only call that carries a policy.
func TestIntakeWithNoRegionIsAutomatic(t *testing.T) {
	f, store := fakeSecrets(t, "")
	created := secretname.UserPrefix + testOwnerSub + "_api-key"
	older := secretname.UserPrefix + testOwnerSub + "_older"
	ctx := context.Background()
	if err := store.Create(ctx, created, map[string]string{AnnotationCreatedBy: testOwnerEmail}, ownerLabels(testOwnerSub)); err != nil {
		t.Fatalf("creating the container: %v", err)
	}
	if got := f.Replication(created); got != nil {
		t.Fatalf("a deployment that names no region created a container replicated in %v, want automatic", got)
	}
	// The container such a deployment made before, in the shape it has always
	// had: a second call finds it and adds a version without touching its
	// replication.
	f.Seed(older, ownerLabels(testOwnerSub), []byte(testValue))
	if _, err := store.AddVersion(ctx, older, []byte(testValue)); err != nil {
		t.Fatalf("adding a version to a container that is there: %v", err)
	}
	if got := f.Replication(older); got != nil {
		t.Fatalf("an existing container's replication changed: %v", got)
	}
}

// TestSecretManagerWritesAVersionAndDeletesTheContainer: the value goes in as
// a version — the fake holds it, and no call of the intake's reads one back —
// and a delete takes the container and its versions away.
func TestSecretManagerWritesAVersionAndDeletesTheContainer(t *testing.T) {
	f, store := fakeSecrets(t, testRegion)
	id := secretname.ProjectPrefix + "model-key"
	ctx := context.Background()
	if err := store.Create(ctx, id, map[string]string{AnnotationCreatedBy: testAdminEmail}, map[string]string{LabelScope: string(ScopeShared)}); err != nil {
		t.Fatalf("creating the container: %v", err)
	}
	version, err := store.AddVersion(ctx, id, []byte(testValue))
	if err != nil {
		t.Fatalf("adding a version: %v", err)
	}
	if version != "1" {
		t.Fatalf("the version is %q, want \"1\"", version)
	}
	if got := string(f.Latest(id)); got != testValue {
		t.Fatalf("the fake holds %q, want the value it was handed", got)
	}
	if err := store.Delete(ctx, id); err != nil {
		t.Fatalf("deleting the container: %v", err)
	}
	if _, found, err := store.Describe(ctx, id); err != nil || found {
		t.Fatalf("after the delete: %v, found %v — want not found", err, found)
	}
	// The fake fails the test itself on any value read; this is the second
	// half of it, for the reader of this file: the intake read no value.
	if accessed := f.Accessed(); len(accessed) != 0 {
		t.Fatalf("the intake read the values of %v", accessed)
	}
}

// TestSecretFromAPIReadsTheShape: every fact R12 refuses a container for is
// read off the API's own fields, including a CMEK named on any of the three
// places it can be, and the annotations come across whole.
func TestSecretFromAPIReadsTheShape(t *testing.T) {
	created := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	base := &secretmanager.Secret{
		Name:        "projects/" + testProject + "/secrets/" + secretname.ProjectPrefix + "key",
		CreateTime:  created.Format(time.RFC3339Nano),
		Annotations: map[string]string{AnnotationCreatedBy: testAdminEmail},
		Replication: &secretmanager.Replication{Automatic: &secretmanager.Automatic{}},
	}
	got := secretFromAPI(base)
	if got.ID != secretname.ProjectPrefix+"key" || !got.Created.Equal(created) || got.Annotations[AnnotationCreatedBy] != testAdminEmail {
		t.Fatalf("the container came across as %+v", got)
	}
	if !got.Shape.Empty() {
		t.Fatalf("a container with nothing but the intake's shape read as %s", got.Shape)
	}

	for _, tc := range []struct {
		what string
		sec  *secretmanager.Secret
		want string
	}{
		{"a TTL", &secretmanager.Secret{Ttl: "86400s"}, "a TTL"},
		{"an expiry", &secretmanager.Secret{ExpireTime: "2027-01-01T00:00:00Z"}, "an expiry"},
		{"a topic", &secretmanager.Secret{Topics: []*secretmanager.Topic{{Name: "projects/p/topics/t"}}}, "a Pub/Sub topic"},
		{"a rotation schedule", &secretmanager.Secret{Rotation: &secretmanager.Rotation{RotationPeriod: "2592000s"}}, "a rotation schedule"},
		{"a rotation schedule with no period", &secretmanager.Secret{Rotation: &secretmanager.Rotation{}}, "a rotation schedule"},
		{"a key on the secret", &secretmanager.Secret{CustomerManagedEncryption: &secretmanager.CustomerManagedEncryption{KmsKeyName: "projects/p/locations/l/keyRings/k/cryptoKeys/c"}}, "a customer-managed key"},
		{"a key on automatic replication", &secretmanager.Secret{Replication: &secretmanager.Replication{Automatic: &secretmanager.Automatic{CustomerManagedEncryption: &secretmanager.CustomerManagedEncryption{KmsKeyName: "projects/p/locations/l/keyRings/k/cryptoKeys/c"}}}}, "a customer-managed key"},
		{"a key on a replica", &secretmanager.Secret{Replication: &secretmanager.Replication{UserManaged: &secretmanager.UserManaged{Replicas: []*secretmanager.Replica{{Location: "global"}, {CustomerManagedEncryption: &secretmanager.CustomerManagedEncryption{KmsKeyName: "projects/p/locations/l/keyRings/k/cryptoKeys/c"}}}}}}, "a customer-managed key"},
	} {
		shape := secretFromAPI(tc.sec).Shape
		if shape.Empty() || shape.String() != tc.want {
			t.Errorf("a container carrying %s read as the shape %q (empty %v), want %q", tc.what, shape.String(), shape.Empty(), tc.want)
		}
	}
}

// TestBucketAdminsReadsTheDerivedList: the list the admin routes decide from
// is the object `fugaro update` publishes, read whole and parsed strictly. An
// object that is missing or is not the list is an error and never an empty
// list — "no admins" is a document, not a failure to read one.
func TestBucketAdminsReadsTheDerivedList(t *testing.T) {
	ctx := context.Background()
	bucket, err := blobx.Open(ctx, "file://"+t.TempDir())
	if err != nil {
		t.Fatalf("opening the bucket: %v", err)
	}
	defer bucket.Close()
	admins, err := NewBucketAdminList(bucket)
	if err != nil {
		t.Fatalf("building the list reader: %v", err)
	}
	if _, err := NewBucketAdminList(nil); err == nil {
		t.Fatal("an admin list over no bucket was built")
	}

	if _, err := admins.Admins(ctx); err == nil {
		t.Fatal("a list that is not there was read as one")
	} else if !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("reading a list that is not there: %v, want the bucket's own not-found", err)
	}

	want := []access.Admin{{Email: testAdminEmail, Sub: testAdminSub}, {Email: testOtherEmail}}
	raw, err := access.EncodeAdminList(want)
	if err != nil {
		t.Fatalf("encoding the list: %v", err)
	}
	if err := bucket.Put(ctx, access.PathAdminList, raw, "application/json"); err != nil {
		t.Fatalf("writing the list: %v", err)
	}
	got, err := admins.Admins(ctx)
	if err != nil {
		t.Fatalf("reading the list: %v", err)
	}
	if len(got) != len(want) || got[0].Email != want[0].Email || got[0].Sub != testAdminSub {
		t.Fatalf("the list read as %v, want %v", got, want)
	}
	if path.Base(access.PathAdminList) != "intake-admins.json" {
		t.Fatal("the object the list is read from is not the one DS2.0 publishes")
	}

	if err := bucket.Put(ctx, access.PathAdminList, []byte(`{"admins":[{"email":"x@example.com","role":"admin"}]}`), "application/json"); err != nil {
		t.Fatalf("writing a foreign document: %v", err)
	}
	if _, err := admins.Admins(ctx); err == nil {
		t.Fatal("a document with a field the list does not have was read as a list")
	}
}
