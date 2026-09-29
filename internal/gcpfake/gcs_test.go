package gcpfake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

func TestGCSFakeWithRealClient(t *testing.T) {
	ctx := context.Background()
	g := NewGCS(t)
	c := g.Client(t)
	obj := c.Bucket("runs").Object("runs/acme-app/x/task.json")
	w := obj.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	_, _ = w.Write([]byte("{}"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	gen := w.Attrs().Generation
	w = obj.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	_, _ = w.Write([]byte("{}"))
	if err := w.Close(); err == nil {
		t.Fatal("DoesNotExist overwrite succeeded")
	}
	r, err := obj.NewReader(ctx)
	if err != nil || r.Attrs.Generation != gen {
		t.Fatalf("reader gen = %v, %v", r, err)
	}
	data, _ := io.ReadAll(r)
	r.Close()
	if string(data) != "{}" {
		t.Fatalf("data = %q", data)
	}
	it := c.Bucket("runs").Objects(ctx, &storage.Query{Prefix: "runs/", Delimiter: "/"})
	a, err := it.Next()
	if err != nil || a.Prefix != "runs/acme-app/" {
		t.Fatalf("prefix listing = %+v, %v", a, err)
	}
	if _, err := it.Next(); !errors.Is(err, iterator.Done) {
		t.Fatalf("second item = %v", err)
	}
	if err := obj.If(storage.Conditions{GenerationMatch: gen + 1}).Delete(ctx); err == nil {
		t.Fatal("mismatched delete succeeded")
	}
	if err := obj.If(storage.Conditions{GenerationMatch: gen}).Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := obj.Attrs(ctx); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Fatalf("after delete: %v", err)
	}
}

// TestGCSFakeResumableUpload covers the path the client takes above its
// chunk size: an initiating POST, then chunks sent to the session URL.
func TestGCSFakeResumableUpload(t *testing.T) {
	ctx := context.Background()
	g := NewGCS(t)
	obj := g.Client(t).Bucket("runs").Object("cache/big")
	want := bytes.Repeat([]byte("0123456789abcdef"), 40_000) // 640 KB: three 256 KiB chunks
	w := obj.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	w.ChunkSize = 256 * 1024
	if _, err := w.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := obj.NewReader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if !bytes.Equal(got, want) || r.Attrs.Generation != w.Attrs().Generation {
		t.Fatalf("read %d bytes at generation %d, wrote %d at %d", len(got), r.Attrs.Generation, len(want), w.Attrs().Generation)
	}
	var chunks int
	for _, req := range g.Requests() {
		if strings.Contains(req.Query, "upload_id=") {
			chunks++
		}
	}
	if chunks < 3 {
		t.Fatalf("%d resumable chunks, want at least 3", chunks)
	}
}

// TestGCSFakeRefusesUnimplementedPreconditions: a precondition the fake
// does not evaluate must fail the test rather than pass unchecked.
func TestGCSFakeRefusesUnimplementedPreconditions(t *testing.T) {
	ctx := context.Background()
	g := NewGCS(t)
	var mu sync.Mutex
	var unhandled []string
	g.failf = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		unhandled = append(unhandled, fmt.Sprintf(format, args...))
	}
	reported := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := len(unhandled)
		unhandled = nil
		return n
	}
	obj := g.Client(t).Bucket("runs").Object("k")
	w := obj.NewWriter(ctx)
	_, _ = w.Write([]byte("v1"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	mg := w.Attrs().Metageneration
	calls := map[string]func() error{
		"upload": func() error {
			w := obj.If(storage.Conditions{MetagenerationMatch: mg}).NewWriter(ctx)
			_, _ = w.Write([]byte("v2"))
			return w.Close()
		},
		"delete": func() error { return obj.If(storage.Conditions{GenerationNotMatch: 1}).Delete(ctx) },
		"patch": func() error {
			_, err := obj.If(storage.Conditions{MetagenerationNotMatch: mg + 1}).Update(ctx, storage.ObjectAttrsToUpdate{CustomTime: time.Now()})
			return err
		},
	}
	for name, call := range calls {
		reported()
		if err := call(); err == nil {
			t.Errorf("%s with an unimplemented precondition succeeded", name)
		}
		if reported() == 0 {
			t.Errorf("%s with an unimplemented precondition was not reported", name)
		}
	}
}

// A bucket insert gives the bucket the project's convenience bindings, and
// a policy set must carry the etag of the policy it replaces.
func TestGCSBucketInsertAndSetPolicy(t *testing.T) {
	g := NewGCS(t)
	g.AddProject("proj-1234", 42)
	c := &http.Client{}
	do := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequest(method, g.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if code, _ := do("POST", "/storage/v1/b?project=other-project", `{"name":"b1"}`); code != 403 {
		t.Fatalf("insert into an unknown project: %d", code)
	}
	code, out := do("POST", "/storage/v1/b?project=proj-1234", `{"name":"b1","labels":{"fugaro":"tfstate"}}`)
	if code != 200 || out["projectNumber"] != "42" {
		t.Fatalf("insert: %d %v", code, out)
	}
	if code, _ := do("POST", "/storage/v1/b?project=proj-1234", `{"name":"b1"}`); code != 409 {
		t.Fatalf("second insert: %d", code)
	}
	if got := g.Inserted("b1")["labels"]; !reflect.DeepEqual(got, map[string]any{"fugaro": "tfstate"}) {
		t.Fatalf("inserted labels = %v", got)
	}
	if !reflect.DeepEqual(g.BucketPolicy("b1"), ConvenienceBindings("proj-1234")) {
		t.Fatalf("policy = %v", g.BucketPolicy("b1"))
	}
	etag := g.BucketPolicyEtag("b1")
	if code, _ := do("PUT", "/storage/v1/b/b1/iam", `{"etag":"stale","bindings":[]}`); code != 412 {
		t.Fatalf("stale set: %d", code)
	}
	if code, _ := do("PUT", "/storage/v1/b/b1/iam", `{"etag":"`+etag+`","bindings":[{"role":"r","members":["user:a@example.com"]}]}`); code != 200 {
		t.Fatalf("set: %d", code)
	}
	if got := g.BucketPolicy("b1"); len(got) != 1 || got[0].Role != "r" || g.BucketPolicyEtag("b1") == etag {
		t.Fatalf("after set: %v, etag %s", got, g.BucketPolicyEtag("b1"))
	}
	if code, _ := do("PUT", "/storage/v1/b/b1/iam", `{"etag":"`+etag+`","bindings":[]}`); code != 412 {
		t.Fatalf("set with the old etag: %d", code)
	}
}
