package firestore_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/firestore"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// runIndexAt is a run's updated_at.
func runIndexAt(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// seedRunIndex seeds a runIndex collection: two plans, two pairs sharing
// updated_at, one document without the sort key and one in another collection.
func seedRunIndex(t *testing.T, f *gcpfake.Firestore) {
	t.Helper()
	for _, d := range []struct{ id, plan, status, at string }{
		{"run-a1", "alpha", "ready", "2026-11-20T10:00:00Z"},
		{"run-a2", "alpha", "live", "2026-11-21T10:00:00Z"},
		{"run-a3", "alpha", "stalled", "2026-11-21T10:00:00Z"},
		{"run-a4", "alpha", "ready", "2026-11-22T09:00:00Z"},
		{"run-a5", "alpha", "draft", "2026-11-19T10:00:00Z"},
		{"run-a6", "alpha", "orphan", "2026-11-22T09:00:00Z"},
		{"run-b1", "beta", "ready", "2026-11-25T10:00:00Z"},
		{"run-b2", "beta", "live", "2026-11-26T10:00:00Z"},
	} {
		f.Set("runIndex", d.id, map[string]any{"plan": d.plan, "status": d.status, "updated_at": runIndexAt(t, d.at)})
	}
	f.Set("runIndex", "run-nosort", map[string]any{"plan": "alpha", "status": "ready"})
	f.Set("other", "run-a4", map[string]any{"plan": "alpha", "updated_at": runIndexAt(t, "2026-11-22T09:00:00Z")})
}

func queryCount(f *gcpfake.Firestore) int {
	n := 0
	for _, r := range f.Requests() {
		if strings.HasSuffix(r.Path, ":runQuery") {
			n++
		}
	}
	return n
}

func lastQueryBody(f *gcpfake.Firestore) string {
	body := ""
	for _, r := range f.Requests() {
		if strings.HasSuffix(r.Path, ":runQuery") {
			body = string(r.Body)
		}
	}
	return body
}

func docIDs(docs []firestore.Doc) []string {
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	return ids
}

// TestRunQueryOrdersAndPages pages a filtered, ordered collection the way the
// run index is read: updated_at descending, the run id as the tie-break, one
// request per page and a cursor carrying the last row read.
func TestRunQueryOrdersAndPages(t *testing.T) {
	f := gcpfake.NewFirestore(t)
	seedRunIndex(t, f)
	c := newClient(t, f)
	ctx := context.Background()
	sortKey := []firestore.Order{{Field: "updated_at", Direction: "DESCENDING"}}

	// A page costs exactly one request, whatever its size.
	page := func(after *firestore.Doc, limit int) []firestore.Doc {
		t.Helper()
		before := queryCount(f)
		docs, err := c.RunQuery(ctx, firestore.Query{
			Collection: "runIndex", Where: map[string]any{"plan": "alpha"},
			OrderBy: sortKey, Limit: limit, StartAfter: after,
		})
		if err != nil {
			t.Fatalf("RunQuery: %v", err)
		}
		if n := queryCount(f) - before; n != 1 {
			t.Fatalf("a page of %d cost %d requests, want 1", limit, n)
		}
		return docs
	}

	var last *firestore.Doc
	for i, want := range [][]string{
		{"run-a6", "run-a4"}, // 2026-11-22 09:00, ties broken by run id descending
		{"run-a3", "run-a2"}, // 2026-11-21 10:00
		{"run-a1", "run-a5"}, // 2026-11-20, then 2026-11-19
		{},                   // the end
	} {
		docs := page(last, 2)
		if got := docIDs(docs); !reflect.DeepEqual(got, want) {
			t.Fatalf("page %d = %v, want %v", i+1, got, want)
		}
		if i == 0 {
			want := map[string]any{"plan": "alpha", "status": "orphan", "updated_at": runIndexAt(t, "2026-11-22T09:00:00Z")}
			if !reflect.DeepEqual(docs[0].Fields, want) {
				t.Fatalf("fields = %#v, want %#v", docs[0].Fields, want)
			}
		}
		if len(docs) > 0 {
			last = &docs[len(docs)-1]
		}
	}

	// The page after the first carries an exclusive cursor over both sort
	// fields, the direction the sort runs in, and the limit the caller asked
	// for.
	body := lastQueryBody(f)
	for _, want := range []string{`"startAt"`, `"before":false`, `"limit":2`, `"fieldPath":"updated_at"`, `"fieldPath":"__name__"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the query lacks %s: %s", want, body)
		}
	}
	if n := strings.Count(body, `"direction":"DESCENDING"`); n != 2 {
		t.Fatalf("the query carries %d descending clauses, want 2 (the sort and the tie-break): %s", n, body)
	}
	if strings.Contains(body, `"startAfter"`) {
		t.Fatalf("the cursor is a startAfter, which the service does not take: %s", body)
	}

	// One orderBy field: __name__ is appended in that field's own direction
	// (descending here), so the ties break with the sort, and a document without
	// the sort key is not in the result.
	docs, err := c.RunQuery(ctx, firestore.Query{
		Collection: "runIndex", Where: map[string]any{"plan": "alpha"}, OrderBy: sortKey, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run-a6", "run-a4", "run-a3", "run-a2", "run-a1", "run-a5"}
	if got := docIDs(docs); !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered by one field = %v, want %v", got, want)
	}

	// No limit is the client's page size; another plan filters the rest out.
	docs, err = c.RunQuery(ctx, firestore.Query{Collection: "runIndex", Where: map[string]any{"plan": "beta"}, OrderBy: sortKey})
	if err != nil {
		t.Fatal(err)
	}
	if got := docIDs(docs); !reflect.DeepEqual(got, []string{"run-b2", "run-b1"}) {
		t.Fatalf("beta = %v", got)
	}

	// Two equality filters, both honoured.
	docs, err = c.RunQuery(ctx, firestore.Query{
		Collection: "runIndex", Where: map[string]any{"plan": "alpha", "status": "ready"},
		OrderBy: sortKey, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := docIDs(docs); !reflect.DeepEqual(got, []string{"run-a4", "run-a1"}) {
		t.Fatalf("plan and status = %v", got)
	}

	// Everything refused locally: no request leaves the client.
	before := queryCount(f)
	for name, q := range map[string]firestore.Query{
		"no collection":      {OrderBy: sortKey},
		"three order fields": {Collection: "runIndex", OrderBy: []firestore.Order{{Field: "a"}, {Field: "b"}, {Field: "c"}}},
		"bad direction":      {Collection: "runIndex", OrderBy: []firestore.Order{{Field: "updated_at", Direction: "SIDEWAYS"}}},
		"empty order field":  {Collection: "runIndex", OrderBy: []firestore.Order{{Field: ""}}},
		"negative limit":     {Collection: "runIndex", OrderBy: sortKey, Limit: -1},
		"unencodable filter": {Collection: "runIndex", Where: map[string]any{"plan": struct{}{}}, OrderBy: sortKey},
		"cursor without the sort key": {Collection: "runIndex", OrderBy: sortKey,
			StartAfter: &firestore.Doc{Collection: "runIndex", ID: "run-nosort", Fields: map[string]any{"plan": "alpha"}}},
		// A cursor is a document of the queried collection: one of another
		// collection carries the wrong fields and the wrong name, and the
		// service reads a cursor positionally.
		"cursor outside the collection": {Collection: "runIndex", OrderBy: sortKey,
			StartAfter: &firestore.Doc{Collection: "other", ID: "run-a4", Fields: map[string]any{"updated_at": runIndexAt(t, "2026-11-22T09:00:00Z")}}},
		// And a document path the service would not take at all, which is a
		// refusal of its own: the collection matches, the name does not.
		"cursor with a name that is not a document path": {Collection: "runIndex", OrderBy: sortKey,
			StartAfter: &firestore.Doc{Collection: "runIndex", ID: "..", Fields: map[string]any{"updated_at": runIndexAt(t, "2026-11-22T09:00:00Z")}}},
	} {
		if _, err := c.RunQuery(ctx, q); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if n := queryCount(f) - before; n != 0 {
		t.Fatalf("a refused query made %d requests", n)
	}
}
