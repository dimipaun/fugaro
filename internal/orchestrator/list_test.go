package orchestrator_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// The listing end of the subtree and of the daemon's cache (CG4 of
// docs/cli-output-console-graph): which orchestrators a database holds, what
// one id's lease node says, and which cached briefs this machine has. It is
// what `fugaro orchestrator ls` reads (D1358: the node and the cache file are
// the registry, and there is no registry of PIDs and no port).

// listNow is the server time the fixtures stamp their reads with.
var listNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// TestNodeIDsListTheSubtree: the ids under `orchestrator/` are the
// orchestrators whose node exists, read with one shallow request so that
// listing them does not download every state and audit document the subtree
// holds. A key this release cannot address is not an orchestrator it can read
// and is left out rather than listed as one.
func TestNodeIDsListTheSubtree(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	ctx := context.Background()

	empty, err := orchestrator.NodeIDs(ctx, rtdbClient(t, f))
	if err != nil {
		t.Fatalf("listing an empty subtree: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("an empty database listed %v", empty)
	}

	for _, id := range []string{"aurora", "belong", "zulu"} {
		path, err := orchestrator.PathState(id)
		if err != nil {
			t.Fatal(err)
		}
		f.Set(path, json.RawMessage(`{"schema_version":1}`))
	}
	// A key no path can be built from: `PathState` refuses it, so this release
	// could not read the node even if it listed the name.
	f.Set(orchestrator.SubtreeRoot+"/"+".not-an-id", json.RawMessage(`{}`))
	// A sibling of the orchestrator subtree is not one of its ids.
	f.Set("coordinator/aurora", json.RawMessage(`{}`))

	ids, err := orchestrator.NodeIDs(ctx, rtdbClient(t, f))
	if err != nil {
		t.Fatalf("listing the subtree: %v", err)
	}
	if want := []string{"aurora", "belong", "zulu"}; !slices.Equal(ids, want) {
		t.Errorf("the subtree listed %v, and its ids in order are %v", ids, want)
	}
}

// TestReadLeaseNodeNamesTheHolderAndItsBeat: one id's lease node is read as its
// holder and the server time of its last beat, and an id nobody holds carries
// no claim rather than an error.
func TestReadLeaseNodeNamesTheHolderAndItsBeat(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	ctx := context.Background()
	db := rtdbClient(t, f)

	node, err := orchestrator.ReadLeaseNode(ctx, db, "aurora")
	if err != nil {
		t.Fatalf("reading an id nobody holds: %v", err)
	}
	if node.Holder != "" || !node.At.IsZero() {
		t.Errorf("an unheld id read as %+v", node)
	}

	path, err := orchestrator.PathLease("aurora")
	if err != nil {
		t.Fatal(err)
	}
	f.Set(path, map[string]any{"holder": "macbook", "at": listNow.Add(-30 * time.Second).Format(time.RFC3339)})

	node, err = orchestrator.ReadLeaseNode(ctx, db, "aurora")
	if err != nil {
		t.Fatalf("reading a held id: %v", err)
	}
	if node.Holder != "macbook" || !node.At.Equal(listNow.Add(-30*time.Second)) {
		t.Errorf("the lease node read as %+v, and it names macbook at %s", node, listNow.Add(-30*time.Second))
	}

	// A lease node this release cannot read is a refusal: it is the same node
	// the daemon's own fence reads, and reading it as "nobody holds this id"
	// is the reading that lets two daemons take one id.
	f.Set(path, map[string]any{"holder": "macbook", "at": listNow.Format(time.RFC3339), "beat": 3})
	if _, err := orchestrator.ReadLeaseNode(ctx, db, "aurora"); err == nil {
		t.Error("a lease node with an unknown field was read, and this package's decode is strict")
	}
}

// TestCachedBriefsListTheLocalFiles: the daemon's cache directory lists the
// briefs this machine holds, by the file's own name, with the stamp the daemon
// wrote each one with. A file that is not one of the daemon's caches — another
// program's, a temp file a write is in the middle of — is not an orchestrator
// and is left out; a cache this release cannot read is a refusal rather than a
// silent omission.
func TestCachedBriefsListTheLocalFiles(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "fugaro", "orchestrator")
	t.Setenv("XDG_CACHE_HOME", filepath.Dir(filepath.Dir(cache)))

	got, err := orchestrator.CachedBriefs(os.Getenv, "linux")
	if err != nil {
		t.Fatalf("listing with no cache directory: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a machine with no cache listed %v", got)
	}

	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, body any) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cache, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("aurora.json", map[string]any{
		"schema_version": orchestrator.BriefCacheVersion,
		"brief":          "the daemon's brief",
		"written_at":     listNow.Add(-2 * time.Minute).Format(time.RFC3339),
	})
	write("belong.json", map[string]any{
		"schema_version": orchestrator.BriefCacheVersion,
		"brief":          "another brief",
		"written_at":     listNow.Format(time.RFC3339),
	})
	write(".brief-1234.tmp", map[string]any{"schema_version": 1})
	write("notes.txt", "not a cache")

	got, err = orchestrator.CachedBriefs(os.Getenv, "linux")
	if err != nil {
		t.Fatalf("listing the cache directory: %v", err)
	}
	want := []orchestrator.CachedBrief{{
		ID: "aurora", At: listNow.Add(-2 * time.Minute),
	}, {
		ID: "belong", At: listNow,
	}}
	if !slices.Equal(got, want) {
		t.Errorf("the cache directory listed %+v, and the two briefs in it are %+v", got, want)
	}

	// A file named like a cache and written by another release is refused: the
	// listing may not quietly drop an orchestrator this machine runs.
	write("belong.json", map[string]any{
		"schema_version": orchestrator.BriefCacheVersion + 1,
		"brief":          "from a later release",
		"written_at":     listNow.Format(time.RFC3339),
	})
	if _, err := orchestrator.CachedBriefs(os.Getenv, "linux"); err == nil {
		t.Error("a cached brief from another release was listed, and the reader refuses one it cannot read")
	}
}

// TestCacheStalenessIsTheLeaseTTL: a daemon's cycle is bounded by the lease it
// holds (its own --interval may not exceed LeaseTTL), so a cache nothing has
// rewritten inside one lease TTL is a cache whose daemon is not running.
func TestCacheStalenessIsTheLeaseTTL(t *testing.T) {
	if orchestrator.CacheStaleAfter != orchestrator.LeaseTTL {
		t.Errorf("CacheStaleAfter is %s, and a daemon's cycle may not outlast the lease it holds (%s)",
			orchestrator.CacheStaleAfter, orchestrator.LeaseTTL)
	}
}
