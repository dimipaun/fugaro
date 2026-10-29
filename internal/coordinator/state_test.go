package coordinator

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The coordinator's state, as P3 builds it: the subtree D195 names, the
// document ID derived from the thing a document describes, and the ETag CAS
// every write goes through (reconciliation Q6, answered (A) on 2026-10-19, and
// finding #16/#17's house idiom). Nothing here contacts Firebase: the database
// is internal/gcpfake's.

func testStore(t *testing.T) *Store {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(c)
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// TestDocumentIDIsDerivedFromWhatItDescribes: the ID is a function of the
// parts and of nothing else — no clock, no counter, no random source — so the
// same thing reported twice is one document (Q6 A's idempotency, finding #17's
// house idiom). The parts are length-prefixed, so two different splits of the
// same characters are two IDs and not one.
//
// Hand mutation this fails on: mixing a timestamp or a counter into the ID,
// which is the mutation that turns P4's idempotency test red.
func TestDocumentIDIsDerivedFromWhatItDescribes(t *testing.T) {
	if a, b := DocumentID("who", "what"), DocumentID("who", "what"); a != b {
		t.Errorf("DocumentID is not a function of its parts: %q and %q", a, b)
	}
	if a, b := DocumentID("who", "what"), DocumentID("who", "other"); a == b {
		t.Errorf("DocumentID ignores its last part: %q", a)
	}
	if a, b := DocumentID("who", "what"), DocumentID("what", "who"); a == b {
		t.Errorf("DocumentID ignores the order of its parts: %q", a)
	}
	if a, b := DocumentID("a", "bc"), DocumentID("ab", "c"); a == b {
		t.Errorf("DocumentID joins its parts without a separator: %q", a)
	}
	if a := DocumentID(""); a == "" {
		t.Error("DocumentID of one empty part is empty: an empty key is not a document")
	}
}

// TestDocumentIDIsADatabaseKey: a document ID is a node name, and the database
// refuses a key holding a dot, a dollar, a hash, a bracket or a slash. The ID
// is lower-case hex, so every one of those is unrepresentable in it rather
// than escaped out of it.
func TestDocumentIDIsADatabaseKey(t *testing.T) {
	id := DocumentID("owner/repo", "a.b$c#d[e]")
	if strings.ContainsAny(id, "./$#[]") {
		t.Errorf("DocumentID = %q holds a character the database refuses in a key", id)
	}
	if strings.ToLower(id) != id {
		t.Errorf("DocumentID = %q is not lower-case hex", id)
	}
}

// TestPathsAreOneSubtree: D195's layout, and the reason it was chosen — the
// read grant of Q15(A) is one prefix, so every document the coordinator writes
// has to sit under it, and a repository's documents have to sit under the
// repository rather than beside it.
func TestPathsAreOneSubtree(t *testing.T) {
	for _, p := range []string{
		PathProject,
		PathRepo("acme-webapp"),
		PathProjectDocument(DocumentID("x")),
		PathRepoDocument("acme-webapp", DocumentID("x")),
	} {
		if !strings.HasPrefix(p, PathRoot+"/") {
			t.Errorf("%q is not under /%s: the read grant is one prefix and this is outside it", p, PathRoot)
		}
	}
	if got, want := PathRepo("a.b"), PathRepos+"/a%2Eb"; got != want {
		t.Errorf("PathRepo(%q) = %q, want %q: a slug is a key, not a path", "a.b", got, want)
	}
}

// TestStaleETagIsRefusedAndTheDocumentDoesNotMove is Q6(A)'s CAS, and it is
// the test the part's red step names: two writers hold the same ETag, the
// first wins, and the second is refused rather than overwriting the winner.
//
// Hand mutation this fails on: Put dropping the If-Match precondition (the
// second write lands and the document moves).
func TestStaleETagIsRefusedAndTheDocumentDoesNotMove(t *testing.T) {
	s := testStore(t)
	ctx := testCtx(t)
	path := PathProjectDocument(DocumentID("project", "release"))

	// Both writers read the same absent node and get the same ETag.
	first, _, err := s.Read(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.Read(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("two reads of one node disagree about its ETag: %q and %q", first, second)
	}

	if err := s.Put(ctx, path, first, map[string]any{"release": "0.11.0"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, path, second, map[string]any{"release": "0.12.0"}); !errors.Is(err, rtdb.ErrPrecondition) {
		t.Fatalf("a put carrying the stale ETag = %v, want ErrPrecondition", err)
	}

	var got map[string]any
	_, found, err := s.Read(ctx, path, &got)
	if err != nil || !found {
		t.Fatalf("reading the document back: found=%v err=%v", found, err)
	}
	if got["release"] != "0.11.0" {
		t.Errorf("the refused write moved the document: %v", got)
	}
}

// TestWriteCreatesThenReplaces: the read-then-CAS-write the sweep and the
// report path both need, on an absent node and on one that is already there.
func TestWriteCreatesThenReplaces(t *testing.T) {
	s := testStore(t)
	ctx := testCtx(t)
	path := PathRepoDocument("acme-webapp", DocumentID("intent", "who", "what"))

	if err := s.Write(ctx, path, map[string]any{"state": "open"}); err != nil {
		t.Fatalf("writing an absent document: %v", err)
	}
	if err := s.Write(ctx, path, map[string]any{"state": "closed"}); err != nil {
		t.Fatalf("replacing a document: %v", err)
	}
	var got map[string]any
	_, found, err := s.Read(ctx, path, &got)
	if err != nil || !found || got["state"] != "closed" {
		t.Fatalf("read back: %v found=%v err=%v", got, found, err)
	}
}

// TestAnAbsentReadLeavesTheTargetAlone: an absent node is the answer "no
// document", never a zero one — a caller that got a zero value back would have
// to guess whether the coordinator wrote nothing or wrote zeroes.
func TestAnAbsentReadLeavesTheTargetAlone(t *testing.T) {
	s := testStore(t)
	ctx := testCtx(t)
	got := map[string]any{"kept": true}
	_, found, err := s.Read(ctx, PathProjectDocument(DocumentID("nope")), &got)
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if kept, _ := got["kept"].(bool); !kept {
		t.Errorf("an absent read wrote into the target: %v", got)
	}
}

// TestStoreRefusesEveryPathOutsideTheSubtree: the writer holds the FP's
// database admin and bypasses the rules (D194), so Store's own check is the
// only fence between it and config/, the ledgers and the mailbox. A refused
// path never reaches the database: the node it names is untouched.
//
// Hand mutation this fails on: dropping the check from any one of Read, Put
// or Write.
func TestStoreRefusesEveryPathOutsideTheSubtree(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(c)
	ctx := testCtx(t)
	f.Set("config/limits/policyRules", 4)

	for _, path := range []string{"", "/", "config/limits/policyRules", "coordinatorx", "coordinatorx/y", "mailbox/a/inbox/m", "x/coordinator"} {
		if _, _, err := s.Read(ctx, path, nil); !errors.Is(err, ErrOutsideSubtree) {
			t.Errorf("Read(%q) = %v, want ErrOutsideSubtree", path, err)
		}
		if err := s.Put(ctx, path, "etag", map[string]any{"x": 1}); !errors.Is(err, ErrOutsideSubtree) {
			t.Errorf("Put(%q) = %v, want ErrOutsideSubtree", path, err)
		}
		if err := s.Write(ctx, path, map[string]any{"x": 1}); !errors.Is(err, ErrOutsideSubtree) {
			t.Errorf("Write(%q) = %v, want ErrOutsideSubtree", path, err)
		}
	}
	var marker int
	if found, err := c.Get(ctx, "config/limits/policyRules", &marker); err != nil || !found || marker != 4 {
		t.Errorf("the refused writes moved config/limits/policyRules: %d found=%v err=%v", marker, found, err)
	}

	for _, path := range []string{PathRoot, "/" + PathProjectDocument("a"), PathRepoDocument("acme-webapp", "b") + "/"} {
		if _, _, err := s.Read(ctx, path, nil); err != nil {
			t.Errorf("Read(%q) inside the subtree = %v", path, err)
		}
	}
}

// TestWriteRetriesOnlyALostRace: a refusal or an outage is not a race, and a
// second attempt meets the same answer, so Write returns it after one put.
//
// Hand mutation this fails on: retrying on every Put error.
func TestWriteRetriesOnlyALostRace(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, rtdb.ErrPermission},
		{http.StatusBadRequest, nil},
	} {
		var puts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				puts.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":"refused"}`))
				return
			}
			w.Header().Set("ETag", "e1")
			_, _ = w.Write([]byte("null"))
		}))
		c, err := rtdb.New(srv.URL, rtdb.Auth{IDToken: func() string { return "idtok" }})
		if err != nil {
			t.Fatal(err)
		}
		err = NewStore(c).Write(testCtx(t), PathProjectDocument("a"), map[string]any{"x": 1})
		srv.Close()
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("status %d: Write = %v, want %v", tc.status, err, tc.want)
		}
		if n := puts.Load(); n != 1 {
			t.Errorf("status %d: Write put %d times, want 1: only rtdb.ErrPrecondition is a lost race", tc.status, n)
		}
	}
}

// TestReadRepoView: the stored view and the marker beside it, read as the
// typed documents a sweep writes rather than as the raw messages the sweep's
// own reader uses. Three things are the read's and not a decoder's:
//
//   - An absent node is "no view" (Stored false) and not a view with nothing
//     in it — a console that could not tell the two apart would claim the
//     coordinator had reconciled a repository it never read.
//   - The repository is echoed, because the node's key is a slug (a hash of
//     the provider and the name) and an absent node carries no view to read
//     the name out of.
//   - A node that is not a view is refused rather than shown as one with
//     nothing in it: strict decoding is the rule the tree's other wire
//     formats keep.
func TestReadRepoView(t *testing.T) {
	s := testStore(t)
	ctx := testCtx(t)
	const slug = "github-acme-webapp"

	got, err := s.ReadRepoView(ctx, slug, "acme/webapp")
	if err != nil {
		t.Fatal(err)
	}
	if got.Stored || got.Stale != nil || len(got.View.Records) != 0 || got.View.Repo != "" {
		t.Errorf("an absent node read as %+v, want no view", got)
	}
	if got.Repo != "acme/webapp" {
		t.Errorf("Repo = %q, want the repository the caller asked about", got.Repo)
	}

	view := View{Repo: "acme/webapp", Records: []Record{{
		PR:       &PullRequest{Number: 7, State: StateOpen},
		Runs:     []Run{{ID: "20261024-120000-aaaa", PR: 7, Files: []string{}}},
		InFlight: true, Files: []string{"internal/cli/run.go"},
	}}, Collisions: []Collision{}}
	since := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	mark := Stale{Since: since, Reason: StaleUnsupported}
	if err := s.Write(ctx, PathView(slug), view); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, PathStale(slug), mark); err != nil {
		t.Fatal(err)
	}

	got, err = s.ReadRepoView(ctx, slug, "acme/webapp")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Stored || !reflect.DeepEqual(got.View, view) {
		t.Errorf("the stored view read back as %+v (stored %v), want %+v", got.View, got.Stored, view)
	}
	if got.Stale == nil || !got.Stale.Since.Equal(since) || got.Stale.Reason != StaleUnsupported {
		t.Errorf("the marker read back as %+v, want %+v as stored", got.Stale, mark)
	}

	// One repository's node is its own: a neighbour has no view, and this read
	// does not fall back to the project's.
	other, err := s.ReadRepoView(ctx, "github-acme-other", "acme/other")
	if err != nil || other.Stored || other.Stale != nil {
		t.Errorf("a neighbour's node read as %+v (err %v), want no view", other, err)
	}

	if err := s.Write(ctx, PathView("github-acme-broken"), "not a view"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadRepoView(ctx, "github-acme-broken", "acme/broken"); err == nil {
		t.Error("a node that does not decode read as a view")
	}
}
