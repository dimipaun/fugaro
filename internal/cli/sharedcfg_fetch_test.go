package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"gocloud.dev/blob/gcsblob"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

var fetchT0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

const belongBucketURL = "gs://fugaro-runs-fugaro-belong"

// fetchRig is a runs bucket on disk behind the sharedBucketOpener seam, and
// config and cache directories of the test's own.
type fetchRig struct {
	runs  string
	opens []string
	// open, when set, replaces the file:// bucket.
	open func(ctx context.Context) (*blobx.Bucket, error)
}

func newFetchRig(t *testing.T) *fetchRig {
	t.Helper()
	dir := t.TempDir()
	isolateProjects(t, dir)
	r := &fetchRig{runs: filepath.Join(dir, "runs")}
	if err := os.MkdirAll(r.runs, 0o755); err != nil {
		t.Fatal(err)
	}
	old := sharedBucketOpener
	sharedBucketOpener = func(ctx context.Context, u string) (*blobx.Bucket, error) {
		r.opens = append(r.opens, u)
		if r.open != nil {
			return r.open(ctx)
		}
		return blobx.Open(ctx, "file://"+r.runs)
	}
	t.Cleanup(func() { sharedBucketOpener = old })
	return r
}

func (r *fetchRig) put(t *testing.T, key, content string) {
	t.Helper()
	p := filepath.Join(r.runs, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *fetchRig) remove(t *testing.T, key string) {
	t.Helper()
	if err := os.Remove(filepath.Join(r.runs, filepath.FromSlash(key))); err != nil {
		t.Fatal(err)
	}
}

func (r *fetchRig) marker(t *testing.T, name, gcp string) {
	t.Helper()
	data, _ := json.Marshal(infra.ProjectMarker{Version: 1, Name: name, GCPProject: gcp})
	r.put(t, infra.ProjectMarkerObject, string(data))
}

// installation is a belong installation that published its shared config.
func (r *fetchRig) installation(t *testing.T) {
	t.Helper()
	r.marker(t, "belong", "fugaro-belong")
	r.put(t, infra.SharedConfigObject, validShared())
}

func (r *fetchRig) fetch(at time.Time) (*localcfg.Config, string, error) {
	return fetchSharedConfig(context.Background(), os.Getenv, at, "belong", "fugaro-belong")
}

func sharedCachePath() string {
	return filepath.Join(os.Getenv("XDG_CACHE_HOME"), "fugaro", "shared-config", "belong.json")
}

func cacheExists(t *testing.T) bool {
	t.Helper()
	_, err := os.Stat(sharedCachePath())
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// statusGCS is a GCS endpoint that answers every request with status, read
// through the real client and gocloud driver, so the error is what a real
// refusal looks like.
func statusGCS(t *testing.T, status int) func(context.Context) (*blobx.Bucket, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":{"code":%d,"message":"%s"}}`, status, http.StatusText(status))
	}))
	t.Cleanup(srv.Close)
	return gcsAt(t, srv.URL, srv.Client())
}

// unreachableGCS is a GCS endpoint nothing listens on any more.
func unreachableGCS(t *testing.T) func(context.Context) (*blobx.Bucket, error) {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return gcsAt(t, u, http.DefaultClient)
}

func gcsAt(t *testing.T, endpoint string, hc *http.Client) func(context.Context) (*blobx.Bucket, error) {
	return func(ctx context.Context) (*blobx.Bucket, error) {
		client, err := storage.NewClient(ctx, option.WithEndpoint(endpoint+"/storage/v1/"),
			option.WithoutAuthentication(), option.WithHTTPClient(hc), storage.WithJSONReads())
		if err != nil {
			return nil, err
		}
		client.SetRetry(storage.WithPolicy(storage.RetryNever))
		b, err := gcsblob.OpenBucket(ctx, nil, "fugaro-runs-fugaro-belong", &gcsblob.Options{Client: client})
		if err != nil {
			return nil, err
		}
		return &blobx.Bucket{Bucket: b, GCSName: "fugaro-runs-fugaro-belong"}, nil
	}
}

func saveCache(t *testing.T, e localcfg.SharedCacheEntry) {
	t.Helper()
	if err := localcfg.SaveSharedCache(os.Getenv, "belong", e); err != nil {
		t.Fatal(err)
	}
}

func TestFetchSharedHappyPathCachesAndSecondCallDoesNotTouchTheBucket(t *testing.T) {
	r := newFetchRig(t)
	r.installation(t)
	c, note, err := r.fetch(fetchT0)
	if err != nil || c == nil || c.Name != "belong" || note != "" {
		t.Fatalf("fetch = %+v, %q, %v", c, note, err)
	}
	if len(r.opens) != 1 || r.opens[0] != belongBucketURL {
		t.Fatalf("opened %q, want the one bucket %s", r.opens, belongBucketURL)
	}
	e, ok := localcfg.LoadSharedCache(os.Getenv, "belong")
	if !ok || e.GCPProject != "fugaro-belong" || e.Bucket != "fugaro-runs-fugaro-belong" || !e.CheckedAt.Equal(fetchT0) || e.YAML != validShared() {
		t.Fatalf("cache = %+v, %v", e, ok)
	}
	r.remove(t, infra.SharedConfigObject)
	r.remove(t, infra.ProjectMarkerObject)
	c, note, err = r.fetch(fetchT0.Add(23 * time.Hour))
	if err != nil || c.Name != "belong" || note != "" {
		t.Fatalf("second fetch = %+v, %q, %v", c, note, err)
	}
	if len(r.opens) != 1 {
		t.Errorf("the second fetch within the day opened the bucket: %q", r.opens)
	}
}

func TestFetchSharedRefusesAMarkerThatNamesAnotherProject(t *testing.T) {
	cases := map[string]struct {
		marker string
		want   string
	}{
		"other name":             {`{"version":1,"name":"other","gcp_project":"fugaro-belong"}`, "other"},
		"same name, other gcp":   {`{"version":1,"name":"belong","gcp_project":"fugaro-other"}`, "fugaro-other"},
		"version 2":              {`{"version":2,"name":"belong","gcp_project":"fugaro-belong"}`, "version 1"},
		"not json":               {`not json`, "version 1"},
		"bad name":               {`{"version":1,"name":"../x","gcp_project":"fugaro-belong"}`, "version 1"},
		"over the 4 KiB cap":     {`{"version":1,"name":"belong","gcp_project":"fugaro-belong","pad":"` + strings.Repeat("x", markerMaxBytes) + `"}`, "version 1"},
		"name differs in case":   {`{"version":1,"name":"Belong","gcp_project":"fugaro-belong"}`, "version 1"},
		"gcp with trailing junk": {`{"version":1,"name":"belong","gcp_project":"fugaro-belong "}`, "gcp_project"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newFetchRig(t)
			r.put(t, infra.ProjectMarkerObject, c.marker)
			r.put(t, infra.SharedConfigObject, validShared())
			_, _, err := r.fetch(fetchT0)
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), belongBucketURL) {
				t.Fatalf("err = %v, want one naming %q and the bucket", err, c.want)
			}
			if cacheExists(t) {
				t.Error("a refused marker left a cache")
			}
		})
	}
}

func TestFetchSharedMissingMarker(t *testing.T) {
	r := newFetchRig(t)
	r.put(t, infra.SharedConfigObject, validShared())
	_, _, err := r.fetch(fetchT0)
	if err == nil || !strings.Contains(err.Error(), "no Fugaro installation at "+belongBucketURL) {
		t.Fatalf("err = %v", err)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("exit %d", ExitCode(err))
	}
}

func TestFetchSharedMissingConfigObject(t *testing.T) {
	r := newFetchRig(t)
	r.marker(t, "belong", "fugaro-belong")
	_, _, err := r.fetch(fetchT0)
	if err == nil || !strings.Contains(err.Error(), "has not published a shared config; ask an operator to run fugaro init") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "belong") || ExitCode(err) != ExitUserError {
		t.Errorf("err = %v, exit %d", err, ExitCode(err))
	}
}

func TestFetchSharedOversizeObjectIsRefused(t *testing.T) {
	r := newFetchRig(t)
	r.marker(t, "belong", "fugaro-belong")
	r.put(t, infra.SharedConfigObject, validShared()+"#"+strings.Repeat("x", localcfg.SharedMaxBytes)+"\n")
	if _, _, err := r.fetch(fetchT0); err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("err = %v", err)
	}
	if cacheExists(t) {
		t.Error("an oversize object was cached")
	}
}

func TestFetchSharedUnreadableBucketNamesAccess(t *testing.T) {
	stale := localcfg.SharedCacheEntry{GCPProject: "fugaro-belong", Bucket: "fugaro-runs-fugaro-belong", CheckedAt: fetchT0.Add(-8 * 24 * time.Hour), YAML: validShared()}
	recent := stale
	recent.CheckedAt = fetchT0.Add(-3 * 24 * time.Hour)
	otherBucket := stale
	otherBucket.Bucket, otherBucket.CheckedAt = "fugaro-runs-elsewhere", fetchT0.Add(-time.Hour)
	for name, tc := range map[string]struct {
		open  func(t *testing.T) func(context.Context) (*blobx.Bucket, error)
		cache *localcfg.SharedCacheEntry
	}{
		"403, cache older than 7 days": {func(t *testing.T) func(context.Context) (*blobx.Bucket, error) {
			return statusGCS(t, http.StatusForbidden)
		}, &stale},
		"403, cache 3 days old": {func(t *testing.T) func(context.Context) (*blobx.Bucket, error) {
			return statusGCS(t, http.StatusForbidden)
		}, &recent},
		"403, fresh cache of another bucket": {func(t *testing.T) func(context.Context) (*blobx.Bucket, error) {
			return statusGCS(t, http.StatusForbidden)
		}, &otherBucket},
		"401, cache 3 days old": {func(t *testing.T) func(context.Context) (*blobx.Bucket, error) {
			return statusGCS(t, http.StatusUnauthorized)
		}, &recent},
		"opener refused": {func(t *testing.T) func(context.Context) (*blobx.Bucket, error) {
			return func(context.Context) (*blobx.Bucket, error) {
				return nil, fmt.Errorf("opening: %w", &googleapi.Error{Code: http.StatusForbidden, Message: "forbidden"})
			}
		}, &recent},
		"no cache": {func(t *testing.T) func(context.Context) (*blobx.Bucket, error) {
			return statusGCS(t, http.StatusForbidden)
		}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			r := newFetchRig(t)
			r.open = tc.open(t)
			if tc.cache != nil {
				saveCache(t, *tc.cache)
			}
			c, note, err := r.fetch(fetchT0)
			if err == nil || c != nil || note != "" {
				t.Fatalf("fetch = %+v, %q, %v; want the access error", c, note, err)
			}
			if !strings.Contains(err.Error(), "access") || !strings.Contains(err.Error(), belongBucketURL) {
				t.Errorf("err = %v, want one naming access and the bucket", err)
			}
			if len(r.opens) != 1 {
				t.Errorf("opened %q", r.opens)
			}
		})
	}
}

func TestFetchSharedOfflineUsesCacheUpToSevenDays(t *testing.T) {
	r := newFetchRig(t)
	r.installation(t)
	if _, _, err := r.fetch(fetchT0); err != nil {
		t.Fatal(err)
	}
	r.open = unreachableGCS(t)
	c, note, err := r.fetch(fetchT0.Add(3*24*time.Hour + time.Hour))
	if err != nil || c == nil || c.Name != "belong" {
		t.Fatalf("day 3 = %+v, %v", c, err)
	}
	if !strings.Contains(note, "cached") || !strings.Contains(note, "3 days old") || !strings.Contains(note, "unreachable") {
		t.Errorf("note = %q", note)
	}
	// The offline use does not renew the stamp: the allowance runs from
	// the last real read.
	if e, ok := localcfg.LoadSharedCache(os.Getenv, "belong"); !ok || !e.CheckedAt.Equal(fetchT0) {
		t.Errorf("cache after an offline use = %+v, %v", e, ok)
	}
	if _, _, err := r.fetch(fetchT0.Add(7 * 24 * time.Hour)); err != nil {
		t.Errorf("day 7: %v", err)
	}
	c, note, err = r.fetch(fetchT0.Add(8 * 24 * time.Hour))
	if err == nil || c != nil || note != "" {
		t.Fatalf("day 8 = %+v, %q, %v; want an error", c, note, err)
	}
	if !strings.Contains(err.Error(), belongBucketURL) {
		t.Errorf("err = %v", err)
	}
	// Past the allowance the entry is not used, but it is kept: the next
	// successful read replaces it, and nothing about it was found wrong.
	if !cacheExists(t) {
		t.Error("an unreachable bucket past the allowance dropped the cache")
	}
}

// TestFetchSharedKeepsTheCacheOnTransientFailures: only a refusal of the
// content or a vanished object drops the cache; a cancel, a server error
// or a refused access leaves a valid entry in place (and unused).
func TestFetchSharedKeepsTheCacheOnTransientFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		open   func(t *testing.T) func(context.Context) (*blobx.Bucket, error)
		cancel bool
	}{
		"cancel": {func(t *testing.T) func(context.Context) (*blobx.Bucket, error) {
			return func(ctx context.Context) (*blobx.Bucket, error) { return nil, ctx.Err() }
		}, true},
		"503": {func(t *testing.T) func(context.Context) (*blobx.Bucket, error) {
			return statusGCS(t, http.StatusServiceUnavailable)
		}, false},
		"500": {func(t *testing.T) func(context.Context) (*blobx.Bucket, error) {
			return statusGCS(t, http.StatusInternalServerError)
		}, false},
		"403": {func(t *testing.T) func(context.Context) (*blobx.Bucket, error) {
			return statusGCS(t, http.StatusForbidden)
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := newFetchRig(t)
			r.installation(t)
			if _, _, err := r.fetch(fetchT0); err != nil {
				t.Fatal(err)
			}
			r.open = tc.open(t)
			ctx := context.Background()
			if tc.cancel {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			c, note, err := fetchSharedConfig(ctx, os.Getenv, fetchT0.Add(2*24*time.Hour), "belong", "fugaro-belong")
			if err == nil || c != nil || note != "" {
				t.Fatalf("fetch = %+v, %q, %v; want an error and no fallback", c, note, err)
			}
			e, ok := localcfg.LoadSharedCache(os.Getenv, "belong")
			if !ok || !e.CheckedAt.Equal(fetchT0) {
				t.Errorf("the cache was dropped or changed: %+v, %v", e, ok)
			}
		})
	}
}

// TestFetchSharedOfflineNeverUsesAForeignOrFutureCache: an unreachable
// bucket falls back only to a cache of this very bucket, stamped in the
// past.
func TestFetchSharedOfflineNeverUsesAForeignOrFutureCache(t *testing.T) {
	base := localcfg.SharedCacheEntry{GCPProject: "fugaro-belong", Bucket: "fugaro-runs-fugaro-belong", CheckedAt: fetchT0.Add(-2 * 24 * time.Hour), YAML: validShared()}
	otherGCP, otherBucket, future, tampered := base, base, base, base
	otherGCP.GCPProject = "fugaro-other"
	otherBucket.Bucket = "fugaro-runs-elsewhere"
	future.CheckedAt = fetchT0.Add(time.Hour)
	tampered.YAML = strings.Replace(validShared(), "token-signer@fugaro-belong", "token-signer@evil-proj", 1)
	for name, e := range map[string]localcfg.SharedCacheEntry{"other gcp project": otherGCP, "other bucket": otherBucket, "from the future": future, "tampered": tampered} {
		t.Run(name, func(t *testing.T) {
			r := newFetchRig(t)
			r.open = unreachableGCS(t)
			saveCache(t, e)
			if c, note, err := r.fetch(fetchT0); err == nil {
				t.Fatalf("fetch = %+v, %q; want an error", c, note)
			}
		})
	}
}

// TestFetchSharedCachesTheGeneration: against GCS (the fake), the cache
// records the object's generation, and a refresh after a rewrite records
// the new one (doctor shows it).
func TestFetchSharedCachesTheGeneration(t *testing.T) {
	r := newFetchRig(t)
	g := gcpfake.NewGCS(t)
	r.open = func(context.Context) (*blobx.Bucket, error) { return g.Bucket(t, "fugaro-runs-fugaro-belong"), nil }
	ctx := context.Background()
	b := g.Bucket(t, "fugaro-runs-fugaro-belong")
	marker, _ := json.Marshal(infra.ProjectMarker{Version: 1, Name: "belong", GCPProject: "fugaro-belong"})
	if err := b.WriteAll(ctx, infra.ProjectMarkerObject, marker, nil); err != nil {
		t.Fatal(err)
	}
	if err := b.WriteAll(ctx, infra.SharedConfigObject, []byte(validShared()), nil); err != nil {
		t.Fatal(err)
	}
	_, gen1, err := b.Read(ctx, infra.SharedConfigObject)
	if err != nil || gen1 == 0 {
		t.Fatalf("gen %d, %v", gen1, err)
	}
	if _, _, err := r.fetch(fetchT0); err != nil {
		t.Fatal(err)
	}
	if e, ok := localcfg.LoadSharedCache(os.Getenv, "belong"); !ok || e.Generation != gen1 {
		t.Fatalf("cache = %+v, %v; want generation %d", e, ok, gen1)
	}
	if err := b.WriteAll(ctx, infra.SharedConfigObject, []byte(strings.Replace(validShared(), "max_parallel: 20", "max_parallel: 9", 1)), nil); err != nil {
		t.Fatal(err)
	}
	_, gen2, _ := b.Read(ctx, infra.SharedConfigObject)
	c, _, err := r.fetch(fetchT0.Add(25 * time.Hour))
	if err != nil || c.MaxParallel != 9 {
		t.Fatalf("refresh = %+v, %v", c, err)
	}
	if e, ok := localcfg.LoadSharedCache(os.Getenv, "belong"); !ok || e.Generation != gen2 || gen2 == gen1 {
		t.Errorf("cache = %+v, %v; want generation %d (was %d)", e, ok, gen2, gen1)
	}
}

func TestFetchSharedStaleCacheRefreshesWhenContentChanged(t *testing.T) {
	r := newFetchRig(t)
	r.installation(t)
	if _, _, err := r.fetch(fetchT0); err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(validShared(), "max_parallel: 20", "max_parallel: 9", 1)
	r.put(t, infra.SharedConfigObject, updated)
	if c, _, err := r.fetch(fetchT0.Add(time.Hour)); err != nil || c.MaxParallel != 20 {
		t.Fatalf("within the day = %+v, %v; want the cached 20", c, err)
	}
	at := fetchT0.Add(25 * time.Hour)
	c, note, err := r.fetch(at)
	if err != nil || c.MaxParallel != 9 || note != "" {
		t.Fatalf("after a day = %+v, %q, %v; want the new 9", c, note, err)
	}
	e, ok := localcfg.LoadSharedCache(os.Getenv, "belong")
	if !ok || e.YAML != updated || !e.CheckedAt.Equal(at) {
		t.Errorf("cache = %+v, %v", e, ok)
	}
	if len(r.opens) != 2 {
		t.Errorf("opens = %q", r.opens)
	}
}

func TestFetchSharedInvalidNewContentDropsTheCache(t *testing.T) {
	for name, spoil := range map[string]func(r *fetchRig, t *testing.T){
		"tampered signer": func(r *fetchRig, t *testing.T) {
			r.put(t, infra.SharedConfigObject, strings.Replace(validShared(), "token-signer@fugaro-belong", "token-signer@evil-proj", 1))
		},
		"tampered database": func(r *fetchRig, t *testing.T) {
			r.put(t, infra.SharedConfigObject, strings.Replace(validShared(), "https://fugaro-belong-default-rtdb.firebaseio.com", "https://evil.example.com", 1))
		},
		"config object vanished": func(r *fetchRig, t *testing.T) { r.remove(t, infra.SharedConfigObject) },
		"marker vanished":        func(r *fetchRig, t *testing.T) { r.remove(t, infra.ProjectMarkerObject) },
		"marker now foreign":     func(r *fetchRig, t *testing.T) { r.marker(t, "other", "fugaro-belong") },
	} {
		t.Run(name, func(t *testing.T) {
			r := newFetchRig(t)
			r.installation(t)
			if _, _, err := r.fetch(fetchT0); err != nil {
				t.Fatal(err)
			}
			if !cacheExists(t) {
				t.Fatal("not cached")
			}
			spoil(r, t)
			if c, note, err := r.fetch(fetchT0.Add(25 * time.Hour)); err == nil {
				t.Fatalf("fetch = %+v, %q; want an error", c, note)
			}
			if cacheExists(t) {
				t.Error("the cache survived")
			}
			// And an unreachable bucket afterwards has nothing to fall
			// back to.
			r.open = unreachableGCS(t)
			if _, _, err := r.fetch(fetchT0.Add(26 * time.Hour)); err == nil {
				t.Error("a dropped cache was used offline")
			}
		})
	}
}

func TestFetchSharedRejectsMalformedGCPProjectBeforeBuildingABucketName(t *testing.T) {
	r := newFetchRig(t)
	r.installation(t)
	for _, gcp := range []string{"../x", "A-B", "", "fugaro-belong/../evil", "fugaro-belong?x=1", "fugaro-belong\n", "x", "fugaro-belong.evil"} {
		_, _, err := fetchSharedConfig(context.Background(), os.Getenv, fetchT0, "belong", gcp)
		if err == nil || !strings.Contains(err.Error(), "GCP project ID") || ExitCode(err) != ExitUserError {
			t.Errorf("%q: err = %v", gcp, err)
		}
	}
	for _, name := range []string{"../x", "", "Belong", "a/b"} {
		if _, _, err := fetchSharedConfig(context.Background(), os.Getenv, fetchT0, name, "fugaro-belong"); err == nil || ExitCode(err) != ExitUserError {
			t.Errorf("name %q: err = %v", name, err)
		}
	}
	if len(r.opens) != 0 {
		t.Errorf("the opener was called with %q", r.opens)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CACHE_HOME"), "fugaro")); !os.IsNotExist(err) {
		t.Errorf("the cache directory was touched: %v", err)
	}
}

func TestFetchSharedIgnoresACacheOfAnotherProjectOrBucket(t *testing.T) {
	// A fresh cache entry whose YAML would be accepted for its own
	// anchor, but which is for another GCP project or bucket: the bucket
	// is read instead, and its answer wins.
	foreign := strings.ReplaceAll(validShared(), "max_parallel: 20", "max_parallel: 3")
	for name, e := range map[string]localcfg.SharedCacheEntry{
		"other gcp project": {GCPProject: "fugaro-other", Bucket: "fugaro-runs-fugaro-belong", CheckedAt: fetchT0, YAML: foreign},
		"other bucket":      {GCPProject: "fugaro-belong", Bucket: "fugaro-runs-elsewhere", CheckedAt: fetchT0, YAML: foreign},
		"from the future":   {GCPProject: "fugaro-belong", Bucket: "fugaro-runs-fugaro-belong", CheckedAt: fetchT0.Add(time.Hour), YAML: foreign},
		"tampered":          {GCPProject: "fugaro-belong", Bucket: "fugaro-runs-fugaro-belong", CheckedAt: fetchT0, YAML: strings.Replace(foreign, "projects/fugaro-belong/", "projects/evil-proj/", 1)},
	} {
		t.Run(name, func(t *testing.T) {
			r := newFetchRig(t)
			r.installation(t)
			saveCache(t, e)
			c, _, err := r.fetch(fetchT0.Add(time.Minute))
			if err != nil || c.MaxParallel != 20 {
				t.Fatalf("fetch = %+v, %v; want the bucket's 20", c, err)
			}
			if len(r.opens) != 1 {
				t.Errorf("opens = %q", r.opens)
			}
		})
	}
}

func TestIsUnreachable(t *testing.T) {
	op := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"dial refused":         {op, true},
		"wrapped dial":         {fmt.Errorf("reading: %w", &url.Error{Op: "Get", URL: "https://storage.googleapis.com", Err: op}), true},
		"dns":                  {&url.Error{Op: "Get", URL: "u", Err: &net.DNSError{Err: "no such host", Name: "storage.googleapis.com"}}, true},
		"deadline":             {fmt.Errorf("x: %w", context.DeadlineExceeded), true},
		"nil":                  {nil, false},
		"canceled":             {context.Canceled, false},
		"403":                  {fmt.Errorf("x: %w", &googleapi.Error{Code: http.StatusForbidden}), false},
		"401":                  {&googleapi.Error{Code: http.StatusUnauthorized}, false},
		"503":                  {&googleapi.Error{Code: http.StatusServiceUnavailable}, false},
		"not found":            {blobx.ErrNotExist, false},
		"too large":            {blobx.ErrTooLarge, false},
		"file permission":      {&fs.PathError{Op: "open", Path: "/x", Err: fs.ErrPermission}, false},
		"validation":           {errors.New("the shared config's token_signer is wrong"), false},
		"url error, not a net": {&url.Error{Op: "Get", URL: "u", Err: errors.New("oauth2: token expired and refresh failed")}, false},
		"403 inside a dial":    {fmt.Errorf("%w: %w", &googleapi.Error{Code: http.StatusForbidden}, op), false},
	} {
		if got := isUnreachable(c.err); got != c.want {
			t.Errorf("%s: isUnreachable(%v) = %v, want %v", name, c.err, got, c.want)
		}
	}
}

// TestIsUnreachableOnRealDriverErrors: the classifier sees what the GCS
// driver really returns.
func TestIsUnreachableOnRealDriverErrors(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		open func(context.Context) (*blobx.Bucket, error)
		want bool
	}{
		"unreachable": {unreachableGCS(t), true},
		"403":         {statusGCS(t, http.StatusForbidden), false},
		"404 bucket":  {statusGCS(t, http.StatusNotFound), false},
	} {
		b, err := c.open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = b.Read(ctx, "x")
		b.Close()
		if err == nil || isUnreachable(err) != c.want {
			t.Errorf("%s: %v: isUnreachable = %v", name, err, !c.want)
		}
	}
}

// markerOnlyGCS is a GCS endpoint, read through the real client and gocloud
// driver, that serves the marker of a belong installation and answers 403 for
// the shared config object (as GCS does for an account without the access).
// configGets counts the requests that named the config object.
func markerOnlyGCS(t *testing.T, configGets *int) func(context.Context) (*blobx.Bucket, error) {
	t.Helper()
	marker, _ := json.Marshal(infra.ProjectMarker{Version: 1, Name: "belong", GCPProject: "fugaro-belong"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.EscapedPath(), "config.yaml") {
			*configGets++
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":{"code":403,"message":"Forbidden"}}`)
			return
		}
		if r.Method != http.MethodGet || !strings.Contains(r.URL.EscapedPath(), "project.json") && !strings.Contains(r.URL.EscapedPath(), strings.ReplaceAll(infra.ProjectMarkerObject, "/", "%2F")) {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		w.Header().Set("X-Goog-Generation", "1")
		w.Header().Set("Content-Length", strconv.Itoa(len(marker)))
		w.Write(marker)
	}))
	t.Cleanup(srv.Close)
	return gcsAt(t, srv.URL, srv.Client())
}

// A marker that reads fine and a config object that answers 403 is "no
// access": not "has not published", and never a cache hit.
func TestFetchSharedConfigObjectForbidden(t *testing.T) {
	r := newFetchRig(t)
	gets := 0
	r.open = markerOnlyGCS(t, &gets)
	saveCache(t, localcfg.SharedCacheEntry{GCPProject: "fugaro-belong", Bucket: "fugaro-runs-fugaro-belong", CheckedAt: fetchT0.Add(-3 * 24 * time.Hour), YAML: validShared()})
	c, note, err := r.fetch(fetchT0)
	if err == nil || c != nil || note != "" {
		t.Fatalf("fetch = %+v, %q, %v; want the access error", c, note, err)
	}
	if gets == 0 || !strings.Contains(err.Error(), "access") || strings.Contains(err.Error(), "has not published") {
		t.Errorf("config gets %d, err = %v", gets, err)
	}
}

// The publisher must not overwrite an object it was refused a read of.
func TestPublishSharedForbiddenExistingObjectIsNotOverwritten(t *testing.T) {
	newFetchRig(t)
	gets := 0
	open := markerOnlyGCS(t, &gets)
	sharedBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) { return open(ctx) }
	lc, err := localcfg.Parse([]byte("version: 1\nname: belong\ngcp_project: fugaro-belong\nregion: us-east5\nruns_bucket: fugaro-runs-fugaro-belong\nregistry_host: us-east5-docker.pkg.dev/fugaro-belong\n"))
	if err != nil {
		t.Fatal(err)
	}
	written, err := publishSharedWarn(context.Background(), lc, func(string) {})
	if written || err == nil || !strings.Contains(err.Error(), "access") {
		t.Fatalf("written %v, err %v; want no write and an access error", written, err)
	}
}
