package gcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	s, err := NewSecrets(context.Background(), Options{Project: "proj-1234", Endpoints: Endpoints{SecretManager: sm.URL + "/", NoAuth: true}})
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
			Automatic *struct{} `json:"automatic"`
		} `json:"replication"`
	}
	if err := json.Unmarshal(create.Body, &sec); err != nil || sec.Labels["fugaro_repo"] != slug || sec.Replication.Automatic == nil || !strings.Contains(create.Query, "secretId="+id) {
		t.Fatalf("create = %s?%s", create.Body, create.Query)
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
