package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// `fugaro orchestrator ls` (CG4 of docs/cli-output-console-graph, D1358,
// D1361): the orchestrators an installation has. The registry is what the
// daemon already writes — the RTDB node under `orchestrator/` and the cached
// brief in this machine's cache directory — so there is no registry of PIDs
// and no port, and a local orchestrator is one this machine's daemon keeps.

// seedOrchestratorNode writes an id's state node the way this release would,
// so the listing finds a node rather than a name somebody typed.
func seedOrchestratorNode(t *testing.T, f *budgetFixture, id string) {
	t.Helper()
	path, err := orchestrator.PathState(id)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := orchestrator.EncodeState(orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		AgentID:       id,
		Role:          orchestrator.RoleOrchestrator,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(path, json.RawMessage(raw))
}

// seedOrchestratorLease writes an id's lease node: the holder and its last
// beat, which is what a listing reports about a node it found.
func seedOrchestratorLease(t *testing.T, f *budgetFixture, id, holder string, at time.Time) {
	t.Helper()
	path, err := orchestrator.PathLease(id)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(path, map[string]any{"holder": holder, "at": at.UTC().Format(time.RFC3339)})
}

// seedBriefCache writes one cached brief the way the daemon writes it, into
// the cache directory this machine's commands read.
func seedBriefCache(t *testing.T, id string, at time.Time) string {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "fugaro", "orchestrator")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(map[string]any{
		"schema_version": orchestrator.BriefCacheVersion,
		"brief":          "orchestrator " + id + ": the brief the daemon cached",
		"written_at":     at.UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// lsFixture is the world the listing runs in: the orchestrator fixture's
// project (aurora) and a cache directory of this machine's own, so no test
// touches a developer's cache.
func lsFixture(t *testing.T) *budgetFixture {
	t.Helper()
	f := newOrchestratorFixture(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return f
}

// TestOrchestratorLsListsNodesAndCaches: the listing is the ids whose node
// exists, with the lease holder and its last beat, and the local cache files
// beside them. An id this machine runs — one with a cached brief — is `local`;
// one only the database holds is `central`.
func TestOrchestratorLsListsNodesAndCaches(t *testing.T) {
	f := lsFixture(t)
	seedOrchestratorNode(t, f, "aurora")
	seedOrchestratorNode(t, f, "belong")
	seedOrchestratorLease(t, f, "aurora", "macbook", orchestratorTestNow.Add(-30*time.Second))
	seedOrchestratorLease(t, f, "belong", "cloud-run-7", orchestratorTestNow.Add(-2*time.Minute))
	seedBriefCache(t, "aurora", orchestratorTestNow.Add(-20*time.Second))

	out, _, err := runOrchestrator(t, "", "orchestrator", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	orchestratorDocMatchesSchema(t, out)
	var doc struct {
		Version       int    `json:"version"`
		Kind          string `json:"kind"`
		Orchestrators []struct {
			Kind        string `json:"kind"`
			Project     string `json:"project"`
			LeaseHolder string `json:"lease_holder"`
			UpdatedAt   string `json:"updated_at"`
			CachedAt    string `json:"cached_at"`
			CacheStale  bool   `json:"cache_stale"`
		} `json:"orchestrators"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != 1 || doc.Kind != "ls" {
		t.Errorf("the document is version %d kind %q, and this family's listing is version 1 kind ls", doc.Version, doc.Kind)
	}
	if len(doc.Orchestrators) != 2 {
		t.Fatalf("the listing carries %d orchestrators, and the subtree holds 2:\n%s", len(doc.Orchestrators), out)
	}
	local, central := doc.Orchestrators[0], doc.Orchestrators[1]
	if local.Project != "aurora" || local.Kind != "local" {
		t.Errorf("the first entry is %+v, and aurora is the orchestrator this machine caches", local)
	}
	if local.LeaseHolder != "macbook" || local.UpdatedAt != orchestratorTestNow.Add(-30*time.Second).Format(time.RFC3339) {
		t.Errorf("aurora's entry is %+v, and its lease names macbook at %s", local, orchestratorTestNow.Add(-30*time.Second).Format(time.RFC3339))
	}
	if local.CachedAt != orchestratorTestNow.Add(-20*time.Second).Format(time.RFC3339) || local.CacheStale {
		t.Errorf("aurora's cache is reported as %+v, and the daemon wrote it 20s before the server's now", local)
	}
	if central.Project != "belong" || central.Kind != "central" || central.CachedAt != "" {
		t.Errorf("belong's entry is %+v, and no daemon of this machine caches it", central)
	}
	if central.LeaseHolder != "cloud-run-7" {
		t.Errorf("belong's entry is %+v, and its lease names cloud-run-7", central)
	}

	// Without --json the listing is a table a person reads, and it names the
	// same orchestrators.
	out, _, err = runOrchestrator(t, "", "orchestrator", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"aurora", "belong", "macbook", "local", "central"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table does not name %q:\n%s", want, out)
		}
	}
}

// TestOrchestratorLsStaleCacheIsMarked: a cached brief is marked when nothing
// is beating the id it belongs to — no lease at all, a last beat past the lease
// TTL, or a claim stamped further ahead than this release honours (lease.go's
// own rule) — and the mark is the lease's answer rather than the brief's age: a
// daemon whose --interval is the whole TTL writes its cache a lease TTL apart,
// and a cycle that failed writes none while its beat goes on, so an old cache is
// not by itself a stopped daemon. A local orchestrator whose node the database
// no longer holds is still listed, because the cache is what a machine that ran
// it has, and nothing beats its id.
func TestOrchestratorLsStaleCacheIsMarked(t *testing.T) {
	f := lsFixture(t)
	// aurora: a claim nobody has beaten inside the TTL.
	seedOrchestratorNode(t, f, "aurora")
	seedOrchestratorLease(t, f, "aurora", "macbook", orchestratorTestNow.Add(-orchestrator.LeaseTTL-time.Second))
	seedBriefCache(t, "aurora", orchestratorTestNow.Add(-10*time.Second))
	// beating: the daemon's cache is older than a whole lease TTL — its cycles
	// are as far apart as --interval allows, or its last ones failed — and its
	// lease is beaten now, which is the daemon that is driving the id.
	seedOrchestratorNode(t, f, "beating")
	seedOrchestratorLease(t, f, "beating", "macbook", orchestratorTestNow)
	seedBriefCache(t, "beating", orchestratorTestNow.Add(-orchestrator.LeaseTTL-30*time.Second))
	// gone: a machine that ran this one, whose node the database no longer has.
	seedBriefCache(t, "gone", orchestratorTestNow.Add(-10*time.Second))
	// ahead: a claim stamped past the skew, which this release treats as expired.
	seedOrchestratorNode(t, f, "ahead")
	seedOrchestratorLease(t, f, "ahead", "macbook", orchestratorTestNow.Add(orchestrator.LeaseSkew+time.Second))
	seedBriefCache(t, "ahead", orchestratorTestNow)

	out, _, err := runOrchestrator(t, "", "orchestrator", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	orchestratorDocMatchesSchema(t, out)
	var doc struct {
		Orchestrators []struct {
			Project    string `json:"project"`
			Kind       string `json:"kind"`
			CachedAt   string `json:"cached_at"`
			CacheStale bool   `json:"cache_stale"`
		} `json:"orchestrators"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Orchestrators) != 4 {
		t.Fatalf("the listing carries %d orchestrators, and this machine caches four:\n%s", len(doc.Orchestrators), out)
	}
	for _, e := range doc.Orchestrators {
		switch e.Project {
		case "aurora":
			if !e.CacheStale {
				t.Errorf("aurora's lease has not been beaten for %s and its cache is not marked stale:\n%s", orchestrator.LeaseTTL+time.Second, out)
			}
		case "beating":
			if e.CacheStale {
				t.Errorf("beating's cache is %s old and its lease was beaten at the server's now, so the listing reports a daemon that is driving the id as stopped:\n%s", orchestrator.LeaseTTL+30*time.Second, out)
			}
			if e.CachedAt == "" {
				t.Errorf("beating is %+v, and its entry carries no stamp of when the cache was written", e)
			}
		case "gone":
			if e.Kind != "local" || !e.CacheStale {
				t.Errorf("gone is %+v, and a cache-only orchestrator is a local one nothing is beating", e)
			}
		case "ahead":
			if !e.CacheStale {
				t.Errorf("ahead's claim is stamped %s into the future and its cache is not marked stale:\n%s", orchestrator.LeaseSkew+time.Second, out)
			}
		default:
			t.Errorf("the listing carries %q, which no fixture seeded", e.Project)
		}
	}
}

// TestOrchestratorLsStaleCacheInTheTable: the plain listing says which cache is
// stale, so a person reading it is not told a daemon is driving an id when
// nothing is beating it — and it does not mark the one that is being driven.
func TestOrchestratorLsStaleCacheInTheTable(t *testing.T) {
	f := lsFixture(t)
	// stale: a cache whose id no node and no lease backs any more.
	seedBriefCache(t, "gone", orchestratorTestNow.Add(-10*time.Second))
	seedOrchestratorNode(t, f, "aurora")
	seedOrchestratorLease(t, f, "aurora", "macbook", orchestratorTestNow)
	seedBriefCache(t, "aurora", orchestratorTestNow)
	out, _, err := runOrchestrator(t, "", "orchestrator", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := strings.Count(out, "(stale)"); n != 1 {
		t.Errorf("the table marks %d caches stale, and one of the two is beaten by a live daemon:\n%s", n, out)
	}
}

// TestOrchestratorLsJSONVersioned: the listing is one versioned document of
// the shape schemas/orchestrator-output.schema.json pins, printed on every
// run — an installation with no orchestrator at all is an empty list rather
// than a refusal.
func TestOrchestratorLsJSONVersioned(t *testing.T) {
	lsFixture(t)
	out, _, err := runOrchestrator(t, "", "orchestrator", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	orchestratorDocMatchesSchema(t, out)
	var doc struct {
		Version       int               `json:"version"`
		Kind          string            `json:"kind"`
		Orchestrators []json.RawMessage `json:"orchestrators"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != 1 || doc.Kind != "ls" || doc.Orchestrators == nil {
		t.Errorf("an installation with no orchestrator printed %s", out)
	}
	if len(doc.Orchestrators) != 0 {
		t.Errorf("an installation with no orchestrator listed %v", doc.Orchestrators)
	}
}

// TestOrchestratorLsIsNotASecondCommandForTheNoun: D1361 keeps one spelling
// per noun, and the spec's plural top-level `fugaro orchestrators` is the one
// it refuses (F25). TestCommandIsRegisteredOnce holds the group's subcommands,
// `ls` among them.
func TestOrchestratorLsIsNotASecondCommandForTheNoun(t *testing.T) {
	for _, c := range NewRootCmd().Commands() {
		if c.Name() == "orchestrators" {
			t.Error("the tree answers to `fugaro orchestrators`, and D1361 keeps one spelling per noun: `fugaro orchestrator ls`")
		}
	}
}
