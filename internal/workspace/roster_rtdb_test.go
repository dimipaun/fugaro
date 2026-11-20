package workspace_test

// WU1: the roster node and the workspace's audit writer.
//
// The roster lives at `workspaceRosters/<name>` in the workspace's OWN database,
// a sibling of the published record and never under it (R5): `fugaro update`'s
// publish stage rewrites `workspaces/<name>` whole under one ETag and refuses a
// node carrying keys it does not know, so a roster inside the record would stop
// an older CLI's update. Every write of the node is conditional on the ETag the
// caller read, so two admins editing the roster cannot lose each other's work,
// and an absent node is a CREATE no `fugaro update` stage seeds (R7).
//
// The database is the fake RTDB with a real client of it, so the ETag
// compare-and-set the roster's write rests on is the client's own: a test that
// stubbed the write would hold nothing about the race (the reading
// internal/budget/workspace_test.go takes of the workspace's own meter). The
// audit half drives the fake Firestore the same way.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"golang.org/x/oauth2"

	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

const (
	wu1Name       = "workspace-alpha"
	wu1RecordPath = "workspaces/" + wu1Name
	wu1AuditID    = "evt-1"
)

// wu1Store is a roster store over a fake database, beside the client it reads
// the record's own ETag through.
func wu1Store(t *testing.T) (*gcpfake.RTDB, *rtdb.Client, *workspace.RosterStore) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("rtdb.New: %v", err)
	}
	s, err := workspace.NewRosterStore(client, wu1Name)
	if err != nil {
		t.Fatalf("NewRosterStore: %v", err)
	}
	return db, client, s
}

// wu1Roster is a roster document. What these tests hold is the store's
// behaviour and never C1's shape, so it is the smallest document the node can
// carry.
func wu1Roster(admins ...string) []byte {
	return []byte(`{"version":1,"admins":` + wu1List(admins) + `}`)
}

func wu1List(xs []string) string {
	b, err := json.Marshal(xs)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// wu1Canon is a JSON document in one spelling (json.Marshal's own key order),
// so two spellings of one document compare equal.
func wu1Canon(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-encoding %s: %v", raw, err)
	}
	return string(b)
}

// wu1Value is the node at path in the spelling wu1Canon uses; "" when it is
// absent (an empty node is pruned, as in the real database).
func wu1Value(t *testing.T, db *gcpfake.RTDB, path string) string {
	t.Helper()
	v := db.Value(path)
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling %s: %v", path, err)
	}
	return string(b)
}

// wu1Write is one conditional write of the roster, failed loudly.
func wu1Write(t *testing.T, s *workspace.RosterStore, node workspace.RosterNode, raw []byte) {
	t.Helper()
	if err := s.Write(context.Background(), node, raw); err != nil {
		t.Fatalf("writing %s: %v", s.Path(), err)
	}
}

// wu1Read is one read of the roster, failed loudly.
func wu1Read(t *testing.T, s *workspace.RosterStore) (workspace.RosterNode, bool) {
	t.Helper()
	node, found, err := s.Read(context.Background())
	if err != nil {
		t.Fatalf("reading %s: %v", s.Path(), err)
	}
	return node, found
}

// TestRosterCreateWhenAbsent: the first write of a workspace's roster creates
// the node, under the absent node's own ETag, so a second first writer cannot
// land its document over the first. It is why no `fugaro update` stage seeds the
// node (R7).
func TestRosterCreateWhenAbsent(t *testing.T) {
	db, _, s := wu1Store(t)

	if got, want := s.Path(), "workspaceRosters/"+wu1Name; got != want {
		t.Fatalf("the roster node is %q, want %q: a sibling of the record at %q and never under it (R5)", got, want, wu1RecordPath)
	}

	absent, found := wu1Read(t, s)
	if found {
		t.Fatalf("a workspace with no roster reads as found")
	}
	if absent.Raw != nil {
		t.Errorf("the absent roster read %s, want no document at all", absent.Raw)
	}
	if absent.ETag == "" {
		t.Fatalf("the absent roster read with no ETag; the create would be no precondition at all")
	}

	first := wu1Roster("admin-1@example.com")
	wu1Write(t, s, absent, first)
	if got, want := wu1Value(t, db, s.Path()), wu1Canon(t, first); got != want {
		t.Fatalf("the node after the create:\n got %s\nwant %s", got, want)
	}

	if err := s.Write(context.Background(), absent, wu1Roster("admin-2@example.com")); !errors.Is(err, rtdb.ErrPrecondition) {
		t.Fatalf("a second create under the stale absent ETag = %v, want rtdb.ErrPrecondition", err)
	}
	if got, want := wu1Value(t, db, s.Path()), wu1Canon(t, first); got != want {
		t.Fatalf("the second create overwrote the first: the node is %s, want %s", got, want)
	}

	node, found := wu1Read(t, s)
	if !found {
		t.Fatalf("the created roster reads as absent")
	}
	if got, want := wu1Canon(t, node.Raw), wu1Canon(t, first); got != want {
		t.Errorf("the re-read roster = %s, want %s", got, want)
	}
}

// TestRosterWriteRefusesStaleETag: two writers that read the same roster can
// both write only if neither has moved it. The second one is refused — the node
// its change would have erased is still there — and the answer the refusal asks
// for is a re-read, which is what makes the next write land.
func TestRosterWriteRefusesStaleETag(t *testing.T) {
	db, _, s := wu1Store(t)
	ctx := context.Background()

	absent, _ := wu1Read(t, s)
	wu1Write(t, s, absent, wu1Roster("admin-1@example.com"))

	a, _ := wu1Read(t, s)
	b, _ := wu1Read(t, s)
	if a.ETag != b.ETag {
		t.Fatalf("two reads of an unmoved node carry different ETags (%q, %q)", a.ETag, b.ETag)
	}

	kept := wu1Roster("admin-1@example.com", "admin-2@example.com")
	wu1Write(t, s, a, kept)

	if err := s.Write(ctx, b, wu1Roster("admin-3@example.com")); !errors.Is(err, rtdb.ErrPrecondition) {
		t.Fatalf("the stale write = %v, want rtdb.ErrPrecondition", err)
	}
	if got, want := wu1Value(t, db, s.Path()), wu1Canon(t, kept); got != want {
		t.Fatalf("the refused write changed the node:\n got %s\nwant %s", got, want)
	}

	fresh, found := wu1Read(t, s)
	if !found || fresh.ETag == b.ETag {
		t.Fatalf("the re-read after the refusal is (found=%v, etag=%q), want the moved node's own ETag", found, fresh.ETag)
	}
	if got, want := wu1Canon(t, fresh.Raw), wu1Canon(t, kept); got != want {
		t.Fatalf("the re-read roster = %s, want the writer that won: %s", got, want)
	}

	next := wu1Roster("admin-1@example.com", "admin-3@example.com")
	wu1Write(t, s, fresh, next)
	if got, want := wu1Value(t, db, s.Path()), wu1Canon(t, next); got != want {
		t.Errorf("the write under the re-read ETag:\n got %s\nwant %s", got, want)
	}
}

// TestRosterWriteLeavesRecordETag: the roster write touches the roster node and
// nothing else. The record `fugaro update` publishes is read BEFORE it for its
// own ETag and read again after, because one ETag covers the whole
// `workspaces/<name>` document: a roster written inside it (or a write beside it
// that reached up) would move it and stop every older CLI's update (R5).
func TestRosterWriteLeavesRecordETag(t *testing.T) {
	db, client, s := wu1Store(t)
	ctx := context.Background()

	db.Set(wu1RecordPath, map[string]any{
		"version": 1, "name": wu1Name,
		"members": []any{map[string]any{"project": "project-a", "repos": []any{}}},
	})
	var before json.RawMessage
	etagBefore, found, err := client.GetETag(ctx, wu1RecordPath, &before)
	if err != nil || !found {
		t.Fatalf("reading the record at %s: found=%v err=%v", wu1RecordPath, found, err)
	}

	absent, _ := wu1Read(t, s)
	wu1Write(t, s, absent, wu1Roster("admin-1@example.com"))

	var after json.RawMessage
	etagAfter, found, err := client.GetETag(ctx, wu1RecordPath, &after)
	if err != nil || !found {
		t.Fatalf("re-reading the record at %s: found=%v err=%v", wu1RecordPath, found, err)
	}
	if etagAfter != etagBefore {
		t.Errorf("the record's ETag moved from %q to %q across a roster write: the roster is not under %s (R5)", etagBefore, etagAfter, wu1RecordPath)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the record changed across a roster write:\n before %s\n after %s", before, after)
	}

	if got, want := wu1Value(t, db, s.Path()), wu1Canon(t, wu1Roster("admin-1@example.com")); got != want {
		t.Errorf("the roster did not land at %s: %s", s.Path(), got)
	}
}

// TestRosterWriteRefusesNodeDeletingDocument: a document that prunes away is not
// a roster but a delete of the node, so the write is refused before it is sent.
// The database stores values and prunes a null, an empty object and an empty
// list — it has no list type at all, so `[]` is an empty object — which makes a
// PUT of `{}`, of an all-null object or of a roster whose every member is an
// empty list a removal of the node, with Read afterwards answering found=false:
// the roster gone with no record of it, the workspace looking like one whose
// first write never happened, and the next create landing under the null ETag
// with nothing to judge it against. A shape that is not an object at all goes the
// same way. Each refusal is a guard's and not a stale ETag's (the
// finding's own trap: a guard that a retry loop mistook for rtdb.ErrPrecondition
// would never terminate), and each is asked for under the node's CURRENT ETag,
// so the write it refuses is one that would otherwise have landed.
func TestRosterWriteRefusesNodeDeletingDocument(t *testing.T) {
	db, _, s := wu1Store(t)
	ctx := context.Background()

	roster := wu1Roster("admin-1@example.com")
	absent, _ := wu1Read(t, s)
	wu1Write(t, s, absent, roster)
	kept, found := wu1Read(t, s)
	if !found {
		t.Fatalf("the roster did not land at %s", s.Path())
	}

	for _, bad := range []struct {
		what string
		raw  []byte
	}{
		{"an empty object", []byte(`{}`)},
		{"an all-null object", []byte(`{"version":null,"admins":null}`)},
		{"an object whose every member prunes away", []byte(`{"version":{"members":null},"admins":{}}`)},
		{"an object whose only member is an empty list", []byte(`{"admins":[]}`)},
		{"an object whose only member is a list nothing survives in", []byte(`{"admins":[null,{}]}`)},
		{"a scalar", []byte(`7`)},
		{"an array", []byte(`[{"admins":["admin-1@example.com"]}]`)},
	} {
		node, found := wu1Read(t, s)
		if !found {
			t.Fatalf("%s: the roster was already gone before the write", bad.what)
		}
		err := s.Write(ctx, node, bad.raw)
		if err == nil {
			t.Fatalf("writing %s = nil, want a refusal: the PUT would delete the node", bad.what)
		}
		if errors.Is(err, rtdb.ErrPrecondition) {
			t.Errorf("writing %s = %v, want a refusal of the document and not a stale ETag: a caller re-reads and retries on that one", bad.what, err)
		}
		if got, want := wu1Value(t, db, s.Path()), wu1Canon(t, roster); got != want {
			t.Fatalf("writing %s changed the node:\n got %s\nwant %s", bad.what, got, want)
		}
		after, found := wu1Read(t, s)
		if !found {
			t.Fatalf("writing %s deleted the node: Read answers found=false and the roster is gone", bad.what)
		}
		if got, want := wu1Canon(t, after.Raw), wu1Canon(t, kept.Raw); got != want {
			t.Errorf("writing %s rewrote the roster:\n got %s\nwant %s", bad.what, got, want)
		}
	}

	// The guard is about the containers the database prunes and not about lists:
	// a list is a value the moment something in it is, so a roster whose only
	// member is a list with an admin in it is a roster and lands. Without this
	// side the test would pass on a guard that refused every list, and that
	// over-refusal would stop a real write.
	listOnly := []byte(`{"admins":["admin-1@example.com"]}`)
	node, found := wu1Read(t, s)
	if !found {
		t.Fatalf("the roster is gone before the list-only write")
	}
	wu1Write(t, s, node, listOnly)
	if got, want := wu1Value(t, db, s.Path()), wu1Canon(t, listOnly); got != want {
		t.Errorf("the write of a roster whose only member is a list with an admin in it:\n got %s\nwant %s", got, want)
	}
}

// wu1Audit is the workspace's audit writer over a fake Firestore.
func wu1Audit(t *testing.T) (*gcpfake.Firestore, *firestore.Client, *workspace.AuditWriter) {
	t.Helper()
	fs := gcpfake.NewFirestore(t)
	client, err := firestore.New(fs.URL, "aurora-ws-123", oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "wu1-token"}))
	if err != nil {
		t.Fatalf("firestore.New: %v", err)
	}
	return fs, client, workspace.NewAuditWriter(client)
}

// TestAuditWriterCreateOnly: an audit entry is created once. A document id that
// is already there is refused and rewrites nothing, so the entry a second writer
// meant to write is lost and the first one is not — which is the whole of what
// "create-only" means, and never a claim that the trail is tamper-proof
// (threat-model.md §7 item 5).
func TestAuditWriterCreateOnly(t *testing.T) {
	fs, client, w := wu1Audit(t)
	ctx := context.Background()

	added := []byte(`{"at":"2026-11-20T09:00:00Z","actor":"admin-1@example.com","action":"workspace user add"}`)
	if err := w.Create(ctx, wu1AuditID, added); err != nil {
		t.Fatalf("creating the first audit event: %v", err)
	}
	first, ok := fs.Value(workspace.AuditCollection, wu1AuditID)
	if !ok {
		t.Fatalf("the audit event did not land in %s/%s", workspace.AuditCollection, wu1AuditID)
	}
	if got, want := first["action"], "workspace user add"; got != want {
		t.Errorf("the stored event's action = %v, want %v", got, want)
	}
	doc, err := client.Get(ctx, workspace.AuditCollection, wu1AuditID)
	if err != nil {
		t.Fatalf("reading the created document: %v", err)
	}
	stored := doc.UpdateTime

	if err := w.Create(ctx, wu1AuditID, []byte(`{"at":"2026-11-20T10:00:00Z","actor":"admin-2@example.com","action":"workspace user rm"}`)); !errors.Is(err, firestore.ErrPrecondition) {
		t.Fatalf("the second create under the same id = %v, want firestore.ErrPrecondition", err)
	}
	if ids := fs.IDs(workspace.AuditCollection); !reflect.DeepEqual(ids, []string{wu1AuditID}) {
		t.Errorf("the refused create left the collection as %v, want the one document it did not write", ids)
	}
	after, ok := fs.Value(workspace.AuditCollection, wu1AuditID)
	if !ok || !reflect.DeepEqual(after, first) {
		t.Errorf("the refused create rewrote the event:\n got %v\nwant %v", after, first)
	}
	doc, err = client.Get(ctx, workspace.AuditCollection, wu1AuditID)
	if err != nil {
		t.Fatalf("re-reading the document: %v", err)
	}
	if doc.UpdateTime != stored {
		t.Errorf("the document's updateTime moved from %s to %s across a refused create: an audit entry is never updated", stored, doc.UpdateTime)
	}
}
