package orchestrator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The session-start cache's tests (O8, D1243).
//
// The cache is the one thing the daemon writes that is not the node: a file
// the print at the start of a session reads, so the shape it has to have, the
// freshness stamp it carries and the fact that a write which fails never stops
// a tick are what is pinned here. The reader's half of the same contract is
// internal/cli's (orchestrator state --brief --offline): these tests decode
// with the reader's own rules, unknown fields refused, and pin the field set,
// so the two ends cannot drift apart without one of them failing.

// cacheEnv is one test's cache root: the getenv a writer is handed, and the
// path the cache of the id the tests use lands on.
func cacheEnv(t *testing.T) (func(string) string, string) {
	t.Helper()
	root := t.TempDir()
	getenv := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return root
		}
		return ""
	}
	return getenv, filepath.Join(root, "fugaro", "orchestrator", "aurora.json")
}

// readCache reads the cache the way the reader does: the fields it knows and
// no others.
func readCache(t *testing.T, path string) (orchestrator.BriefCache, error) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return orchestrator.BriefCache{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c orchestrator.BriefCache
	if err := dec.Decode(&c); err != nil {
		return orchestrator.BriefCache{}, err
	}
	return c, nil
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}

// TestCacheCarriesItsFreshnessStamp: one document, the brief the state
// generates, the server time the daemon read and the cache format's own
// version — and nothing else, because the reader refuses a field it does not
// know.
func TestCacheCarriesItsFreshnessStamp(t *testing.T) {
	getenv, path := cacheEnv(t)
	at := time.Date(2026, 11, 9, 10, 30, 0, 0, time.UTC)
	s := orchestrator.State{SchemaVersion: orchestrator.SchemaVersion, AgentID: "aurora",
		Role: orchestrator.RoleOrchestrator, Goal: "ship the orchestrator"}
	w := &orchestrator.BriefCacheWriter{Getenv: getenv, GOOS: runtime.GOOS, ID: "aurora"}
	w.Write(s, at)

	c, err := readCache(t, path)
	if err != nil {
		t.Fatalf("reading the cache: %v", err)
	}
	if c.Version != 1 {
		t.Errorf("the cache carries schema_version %d, want 1", c.Version)
	}
	if c.Brief != orchestrator.Summarize(s) {
		t.Errorf("the cache carries a brief the state does not generate:\n%q\nwant\n%q", c.Brief, orchestrator.Summarize(s))
	}
	if !c.WrittenAt.Equal(at) {
		t.Errorf("the cache is stamped %s, want the server time it was written at (%s)", c.WrittenAt, at)
	}
	if mode := modeOf(t, path); mode != 0o600 {
		t.Errorf("the cache's mode is %o, want 600", mode)
	}
	if dir := filepath.Dir(path); modeOf(t, dir) != 0o700 {
		t.Errorf("the cache's directory mode is %o, want 700", modeOf(t, dir))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("the cache is not one JSON document: %v", err)
	}
	got := make([]string, 0, len(doc))
	for k := range doc {
		got = append(got, k)
	}
	sort.Strings(got)
	if want := []string{"brief", "schema_version", "written_at"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the cache's field set is %v, want %v", got, want)
	}
}

// TestCacheIsWrittenAtomically: the document goes down beside the one a reader
// may be holding and is renamed over it, so a reader running beside the writer
// decodes a whole brief every time, and a placement that cannot land leaves the
// previous cache exactly where it was and nothing of its own behind.
func TestCacheIsWrittenAtomically(t *testing.T) {
	getenv, path := cacheEnv(t)
	w := &orchestrator.BriefCacheWriter{Getenv: getenv, GOOS: runtime.GOOS, ID: "aurora"}
	w.Write(orchestrator.State{SchemaVersion: orchestrator.SchemaVersion, AgentID: "aurora",
		Role: orchestrator.RoleOrchestrator}, time.Unix(1000, 0).UTC())
	first, err := readCache(t, path)
	if err != nil {
		t.Fatalf("reading the first cache: %v", err)
	}

	// A reader beside the writer: every read decodes a whole document, and
	// never one that is half written.
	done := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var bad []error
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				_, err := readCache(t, path)
				if err != nil {
					mu.Lock()
					bad = append(bad, err)
					mu.Unlock()
					return
				}
			}
		}()
	}
	for i := range 40 {
		w.Write(orchestrator.State{SchemaVersion: orchestrator.SchemaVersion, AgentID: "aurora",
			Role: orchestrator.RoleOrchestrator, Goal: strings.Repeat("x", 400) + string(rune('a'+i%26))},
			time.Unix(int64(1001+i), 0).UTC())
	}
	close(done)
	wg.Wait()
	if len(bad) > 0 {
		t.Fatalf("a reader beside the writer saw a document that is not one: %v", bad[0])
	}
	last, err := readCache(t, path)
	if err != nil {
		t.Fatalf("reading the last cache: %v", err)
	}
	if last.Brief == first.Brief {
		t.Fatal("the writer never replaced the first cache")
	}

	// A placement that cannot land: the destination is not a file the writer
	// can rename over (a kill between the temp file and the rename leaves a
	// window of the same shape). The failure is logged, nothing of the write's
	// own is left in the directory, and what is at the path is untouched.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(path, "keep")
	if err := os.WriteFile(kept, []byte("the previous cache's directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logged []error
	w.Log = func(err error) { logged = append(logged, err) }
	w.Write(orchestrator.State{SchemaVersion: orchestrator.SchemaVersion, AgentID: "aurora",
		Role: orchestrator.RoleOrchestrator, Goal: "never lands"}, time.Unix(2000, 0).UTC())
	if len(logged) != 1 {
		t.Fatalf("a write that could not land was logged %d times, want once: %v", len(logged), logged)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("the write replaced what was at the cache's path: %v", err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".brief-*.tmp")); len(leftovers) != 0 {
		t.Errorf("a write that could not land left %v behind", leftovers)
	}
}

// TestCacheWriteFailureNeverFailsATick: the cache is what a print reads and
// never what a tick decides on, so a writer that cannot place its file logs the
// failure once and the tick's own outcome stands.
func TestCacheWriteFailureNeverFailsATick(t *testing.T) {
	root := t.TempDir()
	// A regular file where the cache's directory has to be: creating the
	// directory fails, which is the shape of a cache root somebody else owns.
	if err := os.WriteFile(filepath.Join(root, "fugaro"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return root
		}
		return ""
	}
	var logged []error
	w := &orchestrator.BriefCacheWriter{Getenv: getenv, GOOS: runtime.GOOS, ID: "aurora",
		Log: func(err error) { logged = append(logged, err) }}

	state := orchestrator.State{SchemaVersion: orchestrator.SchemaVersion, AgentID: "aurora",
		Role: orchestrator.RoleOrchestrator}
	rep, err := orchestrator.StepWithCache(context.Background(), idleTick(&state), w)
	if err != nil {
		t.Fatalf("a tick whose cache could not be written failed: %v", err)
	}
	if rep.Outcome == "" {
		t.Fatal("the tick reported no outcome at all")
	}
	if len(logged) != 1 {
		t.Fatalf("the failed cache write was logged %d times, want once: %v", len(logged), logged)
	}
	if _, err := os.Stat(filepath.Join(root, "fugaro")); err != nil {
		t.Fatalf("the write reached into the test's own tree: %v", err)
	}
}

// idleTick is a tick with nothing to do: a node it reads, no run behind it and
// a launcher that fails loudly if the cycle ever gets as far as starting a run.
func idleTick(s *orchestrator.State) *orchestrator.Tick {
	return &orchestrator.Tick{
		Store:    &cacheStore{s: s},
		Runs:     cacheReader{},
		Launcher: cacheLauncher{},
		Mint:     func(time.Time) (string, error) { return "20261109-101500-abcd", nil },
		Caps:     orchestrator.Caps{MaxRunning: 1, MaxPerCycle: 1, SpawnPerMinute: 1, SpawnPerHour: 1, SpawnPerDay: 1},
		Now:      func() (time.Time, bool) { return time.Unix(5000, 0), true },
	}
}

// cacheStore is a state node held in memory.
type cacheStore struct {
	s    *orchestrator.State
	etag orchestrator.ETag
}

func (c *cacheStore) Load(context.Context) (orchestrator.State, orchestrator.ETag, error) {
	return *c.s, c.etag, nil
}

func (c *cacheStore) Save(_ context.Context, s orchestrator.State, _ orchestrator.ETag) error {
	*c.s, c.etag = s, "etag-2"
	return nil
}

// cacheReader answers no run at all, which is a cycle with nothing to fold.
type cacheReader struct{}

func (cacheReader) Runs(context.Context, []string) ([]orchestrator.RunReport, error) {
	return nil, nil
}

// cacheLauncher refuses to start anything, so a cycle that reached it fails
// loudly rather than passing.
type cacheLauncher struct{}

func (cacheLauncher) Launch(context.Context, orchestrator.Spec, string) (string, error) {
	return "", errors.New("this cycle must not launch anything")
}
