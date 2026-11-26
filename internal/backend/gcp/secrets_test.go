package gcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/task"
)

func newTestSecrets(t *testing.T) (*Secrets, *gcpfake.Secrets) {
	t.Helper()
	sm := gcpfake.NewSecrets(t)
	s, err := NewSecrets(context.Background(), Options{GCPProject: "proj-1234", Region: "europe-west1", Endpoints: Endpoints{SecretManager: sm.URL + "/", NoAuth: true}})
	if err != nil {
		t.Fatal(err)
	}
	return s, sm
}

func TestSecretsSetSequence(t *testing.T) {
	ctx := context.Background()
	s, sm := newTestSecrets(t)
	slug, err := task.Slug("bitbucket", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	id := SecretID(slug, "bitbucket-token")
	labels := map[string]string{"fugaro": "managed", "fugaro_repo": slug}
	v1, err := s.Set(ctx, id, []byte("tok-1"), labels)
	if err != nil || v1 != "1" {
		t.Fatalf("first Set = %q, %v", v1, err)
	}
	if v2, err := s.Set(ctx, id, []byte("tok-2"), labels); err != nil || v2 != "2" {
		t.Fatalf("second Set = %q, %v", v2, err)
	}
	var calls []string
	for _, r := range sm.Requests() {
		calls = append(calls, r.Method+" "+r.Path[strings.LastIndex(r.Path, "/")+1:])
	}
	want := []string{
		"GET " + id, "POST secrets", "POST " + id + ":addVersion", // new secret
		"GET " + id, "POST " + id + ":addVersion", // existing one
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v", calls)
	}
	create := sm.Requests()[1]
	var sec struct {
		Labels      map[string]string `json:"labels"`
		Replication struct {
			Automatic   *struct{} `json:"automatic"`
			UserManaged *struct {
				Replicas []struct{ Location string } `json:"replicas"`
			} `json:"userManaged"`
		} `json:"replication"`
	}
	if err := json.Unmarshal(create.Body, &sec); err != nil || sec.Labels["fugaro_repo"] != slug || !strings.Contains(create.Query, "secretId="+id) {
		t.Fatalf("create = %s?%s", create.Body, create.Query)
	}
	// The replication is user-managed in the installation's region: automatic
	// is global, which an EU installation may not have (D3082, D3092).
	if sec.Replication.Automatic != nil || sec.Replication.UserManaged == nil ||
		len(sec.Replication.UserManaged.Replicas) != 1 || sec.Replication.UserManaged.Replicas[0].Location != "europe-west1" {
		t.Fatalf("create replication = %s", create.Body)
	}
	var add struct {
		Payload struct{ Data string } `json:"payload"`
	}
	_ = json.Unmarshal(sm.Requests()[4].Body, &add)
	if got, _ := base64.StdEncoding.DecodeString(add.Payload.Data); string(got) != "tok-2" {
		t.Fatalf("payload = %q", got)
	}
	if got := string(sm.Latest(id)); got != "tok-2" {
		t.Fatalf("Latest = %q", got)
	}
	list, err := s.List(ctx, map[string]string{"fugaro_repo": slug})
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].Labels["fugaro"] != "managed" ||
		list[0].Versions != 2 || list[0].Latest != "2" || list[0].Created.IsZero() {
		t.Fatalf("List = %+v, %v", list, err)
	}
	for _, r := range sm.Requests() {
		if strings.Contains(r.Path, ":access") {
			t.Fatalf("List read a value: %s", r.Path)
		}
	}
	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if list, err := s.List(ctx, map[string]string{"fugaro_repo": slug}); err != nil || len(list) != 0 {
		t.Fatalf("List after Delete = %+v, %v", list, err)
	}
}

// TestASecretCreatedByTheCLIIsInTheRegion: the container Set creates is
// user-managed with one replica in the installation's region, and a container
// that already exists keeps the replication it has — Set sends a policy only
// with a create, which is what leaves an installation's older, automatic
// containers alone (D3082, D3092).
func TestASecretCreatedByTheCLIIsInTheRegion(t *testing.T) {
	ctx := context.Background()
	s, sm := newTestSecrets(t)
	creates := func() int {
		n := 0
		for _, r := range sm.Requests() {
			if r.Method == "POST" && strings.HasSuffix(r.Path, "/secrets") {
				n++
			}
		}
		return n
	}
	if _, err := s.Set(ctx, "token", []byte("v1"), map[string]string{"fugaro": "managed"}); err != nil {
		t.Fatal(err)
	}
	if got := sm.Replication("token"); !slices.Equal(got, []string{"europe-west1"}) {
		t.Fatalf("replication = %v, want the installation's region", got)
	}
	// An automatic container another writer made, as every container of an
	// installation from before this work is: the version goes in and the
	// replication does not change.
	sm.Seed("older", map[string]string{"fugaro": "managed"}, []byte("theirs"))
	if v, err := s.Set(ctx, "older", []byte("v2"), map[string]string{"fugaro": "managed"}); err != nil || v != "2" {
		t.Fatalf("Set into an existing container = %q, %v", v, err)
	}
	if got := sm.Replication("older"); got != nil {
		t.Fatalf("an existing container's replication changed: %v", got)
	}
	if n := creates(); n != 1 {
		t.Fatalf("creates = %d, want only the one that made token", n)
	}
}

func TestSecretsListFiltersByEveryLabel(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestSecrets(t)
	for id, repo := range map[string]string{"a-1": "repo-a", "a-2": "repo-a", "b-1": "repo-b"} {
		if _, err := s.Set(ctx, id, []byte("value"), map[string]string{"fugaro": "managed", "fugaro_repo": repo}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.List(ctx, map[string]string{"fugaro": "managed", "fugaro_repo": "repo-a"})
	var ids []string
	for _, x := range list {
		ids = append(ids, x.ID)
	}
	slices.Sort(ids)
	if err != nil || !slices.Equal(ids, []string{"a-1", "a-2"}) {
		t.Fatalf("List = %v, %v", ids, err)
	}
}

// TestSecretsSetRefusesForeignSecret: a secret that exists under the ID
// without our labels belongs to someone else (a chosen-name collision, or
// one made by hand), so Set adds no version to it.
func TestSecretsSetRefusesForeignSecret(t *testing.T) {
	ctx := context.Background()
	s, sm := newTestSecrets(t)
	if _, err := s.Set(ctx, "shared-id", []byte("theirs"), map[string]string{"fugaro": "managed", "fugaro_repo": "other"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Set(ctx, "shared-id", []byte("mine"), map[string]string{"fugaro": "managed", "fugaro_repo": "mine"})
	if !errors.Is(err, ErrForeignSecret) || !strings.Contains(err.Error(), "fugaro_repo") {
		t.Fatalf("err = %v", err)
	}
	if got := string(sm.Latest("shared-id")); got != "theirs" {
		t.Fatalf("foreign secret overwritten: %q", got)
	}
}

// TestSecretsSetCreateRace: a secret created between Set's get and its
// create (409) is re-read and label-checked, not an error.
func TestSecretsSetCreateRace(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, repo string
		want       error
	}{{"a concurrent set of ours", "mine", nil}, {"someone else's", "other", ErrForeignSecret}} {
		t.Run(c.name, func(t *testing.T) {
			s, sm := newTestSecrets(t)
			sm.OnCreate = func(id string) {
				sm.OnCreate = nil
				sm.Seed(id, map[string]string{"fugaro": "managed", "fugaro_repo": c.repo}, []byte("theirs"))
			}
			v, err := s.Set(ctx, "raced", []byte("mine"), map[string]string{"fugaro": "managed", "fugaro_repo": "mine"})
			if !errors.Is(err, c.want) || (c.want == nil) != (v == "2") {
				t.Fatalf("Set = %q, %v", v, err)
			}
			if want := map[bool]string{true: "mine", false: "theirs"}[c.want == nil]; string(sm.Latest("raced")) != want {
				t.Fatalf("Latest = %q", sm.Latest("raced"))
			}
		})
	}
}

func TestSecretsSetAPIError(t *testing.T) {
	s, sm := newTestSecrets(t)
	sm.FailAddVersion = "bad payload"
	if _, err := s.Set(context.Background(), "x", []byte("value"), map[string]string{"fugaro": "managed"}); err == nil || !strings.Contains(err.Error(), "bad payload") {
		t.Fatalf("err = %v", err)
	}
}

// captureStderr points os.Stderr at a file for the rest of the test (the
// SDK's env-driven logger binds os.Stderr when a client is built) and
// returns a function reading what was written.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = old; f.Close() })
	return func() string {
		data, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
}

// TestClientsIgnoreSDKDebugLogging: GOOGLE_SDK_GO_LOGGING_LEVEL=debug makes
// a Google client with no logger of its own log every request and its body
// to stderr, which for addVersion is the secret, base64. Every client
// Fugaro builds has a discarding logger instead.
func TestClientsIgnoreSDKDebugLogging(t *testing.T) {
	t.Setenv("GOOGLE_SDK_GO_LOGGING_LEVEL", "debug")
	stderr := captureStderr(t)
	ctx := context.Background()
	s, _ := newTestSecrets(t)
	const value = "sk-debug-EXAMPLEEXAMPLE"
	if _, err := s.Set(ctx, "x", []byte(value), map[string]string{"fugaro": "managed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(ctx, map[string]string{"fugaro": "managed"}); err != nil {
		t.Fatal(err)
	}
	b, fr, _ := newTestBackend(t)
	fr.AddJob(webJob, "4", "8Gi")
	ref, err := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name}, func(backend.LogEntry) error { return nil }); err != nil {
		t.Fatal(err)
	}
	out := stderr()
	if strings.Contains(out, value) || strings.Contains(out, base64.StdEncoding.EncodeToString([]byte(value))) || strings.Contains(out, "api request") {
		t.Fatalf("the SDK logged to stderr:\n%s", out)
	}
}

// Access is the one value read (the GitHub App pre-check's), and says what
// it could not do without echoing a value.
func TestSecretsAccess(t *testing.T) {
	ctx := context.Background()
	s, sm := newTestSecrets(t)
	sm.AllowAccess()
	if _, err := s.Access(ctx, "missing-1"); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("absent secret: %v", err)
	}
	sm.Seed("empty-1", map[string]string{"fugaro": "managed"}, nil)
	if _, err := s.Access(ctx, "empty-1"); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("no version: %v", err)
	}
	sm.Seed("has-1", map[string]string{"fugaro": "managed"}, []byte("old"))
	if _, err := s.Set(ctx, "has-1", []byte("the-value"), map[string]string{"fugaro": "managed"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Access(ctx, "has-1")
	if err != nil || string(got) != "the-value" {
		t.Fatalf("Access = %q, %v", got, err)
	}
	if a := sm.Accessed(); len(a) != 1 || a[0] != "has-1" {
		t.Errorf("accessed = %v", a)
	}
}

// SetLabels is the retirement's own write (D3325, IS13): a project copy is
// marked retired by taking fugaro_secret OFF it, which is the label
// internal/infra's discovery adopts a project-wide container by, and a label
// the map no longer holds is a label the API's labels mask removes. The
// container's versions are not part of it.
func TestSecretsSetLabelsReplacesTheWholeMap(t *testing.T) {
	ctx := context.Background()
	s, sm := newTestSecrets(t)
	id := ProjectSecretID("acme", "npm-token")
	sm.Seed(id, map[string]string{LabelManaged: ManagedValue, LabelProject: "acme", LabelSecret: "npm-token"}, []byte("the-value"))

	retired := map[string]string{LabelManaged: ManagedValue, LabelProject: "acme", LabelRetired: "npm-token"}
	if err := s.SetLabels(ctx, id, retired); err != nil {
		t.Fatalf("SetLabels: %v", err)
	}
	got := sm.Labels(id)
	if _, ok := got[LabelSecret]; ok {
		t.Fatalf("labels = %v: fugaro_secret is still on the container, so discovery still adopts it", got)
	}
	if !maps.Equal(got, retired) {
		t.Fatalf("labels = %v, want exactly %v", got, retired)
	}
	if v := sm.Latest(id); string(v) != "the-value" {
		t.Fatalf("the container's value is %q: a label write took a version with it", v)
	}
	if p := sm.LabelPatches(); len(p) != 1 || p[0] != id {
		t.Fatalf("label patches = %v", p)
	}
	if d := sm.Deleted(); len(d) != 0 {
		t.Fatalf("SetLabels deleted %v", d)
	}
	var patch *gcpfake.Request
	for _, r := range sm.Requests() {
		if r.Method == http.MethodPatch {
			patch = &r
		}
	}
	if patch == nil {
		t.Fatal("no patch was made")
	}
	if patch.Path != "/v1/projects/proj-1234/secrets/"+id || !strings.Contains(patch.Query, "updateMask=labels") {
		t.Fatalf("the patch was %s?%s, want the labels mask on the one container", patch.Path, patch.Query)
	}

	// A container that is not there is the API's refusal, not a silent success:
	// the caller read the container before it marked it, so a 404 here is a
	// container that went away between the two calls.
	if err := s.SetLabels(ctx, "sev-missing", map[string]string{LabelRetired: "x"}); err == nil {
		t.Fatal("SetLabels on an absent container succeeded")
	}
}
