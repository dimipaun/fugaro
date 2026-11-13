// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package docstore_test

// The suite every backend of this package runs against, and the two things a
// memo store and a database must agree on beyond it: that a transaction that
// meets a conflict runs again, and that a group of writes that one of them
// refuses lands nowhere.
//
// The suite is the seam's own rules and not a backend's: it is written against
// docstore.Store and constructed once per backend, so the memory store a test
// drives and the Firestore store the deploy names answer the same questions the
// same way (firestore_test.go runs the same Run under the `firestore` tag,
// against the emulator). What it cannot hold — how each backend finds a
// document, what a query costs, whether a write is a merge — is that backend's
// own business and is tested beside it, not here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/keyservice/docstore"
)

// Factory builds one empty backend per test: state from one case must never
// reach the next, and a backend that carried a document between two of them
// would make the suite pass for the wrong reason.
type Factory func(t *testing.T) docstore.Store

// The suite's own collection and the two documents most cases build on.
const (
	collection = "widgets"
)

// widget is the shape the round-trip cases write: one field of each JSON kind,
// including a nested object and an array, because a backend that flattened,
// re-typed or dropped one of them would still pass a suite that wrote strings.
type widget struct {
	Name   string            `json:"name"`
	Seats  int               `json:"seats"`
	Ratio  float64           `json:"ratio"`
	Open   bool              `json:"open"`
	Since  time.Time         `json:"since"`
	Tags   []string          `json:"tags"`
	Labels map[string]string `json:"labels"`
	Note   *string           `json:"note"`
}

// at is every instant the suite uses. A fixed UTC instant keeps a case's
// expected value a constant rather than a reading of the clock, which is what
// makes the round-trip comparison below a comparison of the store and not of
// two calls to time.Now.
var at = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)

func sample() widget {
	note := "held"
	return widget{
		Name:   "acme",
		Seats:  25,
		Ratio:  0.5,
		Open:   true,
		Since:  at,
		Tags:   []string{"a", "b"},
		Labels: map[string]string{"tier": "team"},
		Note:   &note,
	}
}

// Run exercises the seam against one backend. It is exported to the rest of the
// package's tests only: the seam's callers are the portal's own packages, and a
// suite is not part of what this package offers them.
func Run(t *testing.T, open Factory) {
	t.Helper()
	t.Run("AMissingDocumentIsNotAnError", func(t *testing.T) { testMissing(t, open) })
	t.Run("ADocumentComesBackAsItWentIn", func(t *testing.T) { testRoundTrip(t, open) })
	t.Run("APutReplacesTheWholeDocument", func(t *testing.T) { testPutReplaces(t, open) })
	t.Run("AQueryTakesACollection", func(t *testing.T) { testQuery(t, open) })
	t.Run("AFilterComparesTheValueItWrites", func(t *testing.T) { testFilterTypes(t, open) })
	t.Run("ATransactionCommitsTogether", func(t *testing.T) { testCommitTogether(t, open) })
	t.Run("ATransactionThatFailsWritesNothing", func(t *testing.T) { testFailedTransaction(t, open) })
	t.Run("ATransactionReadsBeforeItWrites", func(t *testing.T) { testReadAfterWrite(t, open) })
	t.Run("ATransactionIsNotNested", func(t *testing.T) { testNestedTransaction(t, open) })
	t.Run("ARefusedCallIsRefusedByName", func(t *testing.T) { testUnnamed(t, open) })
}

func ctx() context.Context { return context.Background() }

// doc writes one document, failing the test if the store refuses it.
func doc(t *testing.T, s docstore.Store, id string, v any) {
	t.Helper()
	if err := s.Put(ctx(), collection, id, v); err != nil {
		t.Fatalf("Put(%s) = %v, want it kept: the suite cannot continue from a document the store would not write", id, err)
	}
}

// read gets one document into a widget, failing the test if it is missing.
func read(t *testing.T, s docstore.Store, id string) widget {
	t.Helper()
	var got widget
	held, err := s.Get(ctx(), collection, id, &got)
	if err != nil {
		t.Fatalf("Get(%s) failed: %v", id, err)
	}
	if !held {
		t.Fatalf("Get(%s) says the store holds no such document, and this case wrote it", id)
	}
	return got
}

// same compares two values by the JSON they write. It is the comparison a JSON
// store admits: Go's own equality on a time.Time compares a location pointer, a
// decoded map has its own identity, and neither is a fact the store has.
func same(t *testing.T, what string, got, want any) {
	t.Helper()
	g, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshalling what %s answered: %v", what, err)
	}
	w, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshalling what %s should have answered: %v", what, err)
	}
	if string(g) != string(w) {
		t.Errorf("%s = %s, want %s", what, g, w)
	}
}

func testMissing(t *testing.T, open Factory) {
	s := open(t)
	var got widget
	held, err := s.Get(ctx(), collection, "nobody", &got)
	if err != nil || held {
		t.Fatalf("Get of a document nobody wrote = (%v, %v), want (false, nil): a document that is not there is an answer and not a failure", held, err)
	}
	if !reflect.DeepEqual(got, widget{}) {
		t.Errorf("Get of a missing document filled the caller's value with %+v: nothing was read, so nothing may be written", got)
	}
}

func testRoundTrip(t *testing.T, open Factory) {
	s := open(t)
	want := sample()
	doc(t, s, "one", want)
	same(t, "the document read back", read(t, s, "one"), want)

	// The same value put twice is the same document: a store that appended
	// would answer with two, and every lookup by id below would be a guess.
	doc(t, s, "one", want)
	docs, err := s.Query(ctx(), collection, docstore.Query{})
	if err != nil {
		t.Fatalf("Query after writing one document twice: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("the collection holds %d documents after two puts of one id, want 1: a put is a write of that document and never a second", len(docs))
	}
	if docs[0].ID != "one" {
		t.Errorf("the document's id came back as %q, want %q", docs[0].ID, "one")
	}
}

func testPutReplaces(t *testing.T, open Factory) {
	s := open(t)
	doc(t, s, "one", sample())
	doc(t, s, "one", map[string]any{"name": "acme"})

	got := read(t, s, "one")
	if got.Name != "acme" {
		t.Errorf("the document's name is %q, want %q", got.Name, "acme")
	}
	if got.Seats != 0 || got.Open || got.Since != (time.Time{}) || got.Note != nil {
		t.Errorf("the document still holds fields from the write it replaced: %+v. A put is the whole document, so a field the second write left out is gone rather than kept", got)
	}
}

func testQuery(t *testing.T, open Factory) {
	s := open(t)
	for i, id := range []string{"c", "a", "b"} {
		doc(t, s, id, map[string]any{"id": id, "open": id != "b", "size": i + 1})
	}

	all, err := s.Query(ctx(), collection, docstore.Query{})
	if err != nil {
		t.Fatalf("Query with no filter: %v", err)
	}
	if ids := idsOf(all); strings.Join(ids, ",") != "a,b,c" {
		t.Errorf("Query with no filter answered %v, want every document in id order: a list that came back in the order a backend happened to reach the documents would be a different list on every backend", ids)
	}

	opened, err := s.Query(ctx(), collection, docstore.Query{Where: []docstore.Where{{Field: "open", Equals: true}}})
	if err != nil {
		t.Fatalf("Query by one field: %v", err)
	}
	if ids := idsOf(opened); strings.Join(ids, ",") != "a,c" {
		t.Errorf("Query on open=true answered %v, want [a c]", ids)
	}

	two, err := s.Query(ctx(), collection, docstore.Query{
		Where: []docstore.Where{{Field: "open", Equals: true}, {Field: "size", Equals: 2}},
	})
	if err != nil {
		t.Fatalf("Query by two fields: %v", err)
	}
	if ids := idsOf(two); strings.Join(ids, ",") != "a" {
		t.Errorf("Query on open=true and size=2 answered %v, want [a]: every filter has to match, and a document has to match all of them", ids)
	}

	none, err := s.Query(ctx(), collection, docstore.Query{Where: []docstore.Where{{Field: "absent", Equals: "x"}}})
	if err != nil {
		t.Fatalf("Query on a field no document has: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("Query on a field no document carries answered %v, want nothing: a document without the field is not a document whose field is empty", idsOf(none))
	}

	limited, err := s.Query(ctx(), collection, docstore.Query{Limit: 2})
	if err != nil {
		t.Fatalf("Query with a limit: %v", err)
	}
	if ids := idsOf(limited); strings.Join(ids, ",") != "a,b" {
		t.Errorf("Query limited to two answered %v, want the first two in id order [a b]: a limit that cut the documents a backend reached first would answer differently on every backend", ids)
	}

	noneAtAll, err := s.Query(ctx(), "nosuchcollection", docstore.Query{})
	if err != nil {
		t.Fatalf("Query on a collection nothing was written to: %v", err)
	}
	if len(noneAtAll) != 0 {
		t.Errorf("Query on an empty collection answered %v, want nothing", idsOf(noneAtAll))
	}
}

func testFilterTypes(t *testing.T, open Factory) {
	s := open(t)
	doc(t, s, "one", map[string]any{"seats": 3, "labels": map[string]any{"tier": "team"}, "tags": []any{"a", "b"}})

	for _, tc := range []struct {
		name   string
		filter any
		want   bool
	}{
		{"the integer the field was written as", 3, true},
		{"the same number as a JSON decoder reads it", float64(3), true},
		{"the number as text", "3", false},
		{"a whole object", map[string]any{"tier": "team"}, true},
		{"an object with one key of two", map[string]any{"tier": "tea"}, false},
		{"a whole array", []string{"a", "b"}, true},
		{"an array in another order", []string{"b", "a"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			field := "seats"
			switch tc.filter.(type) {
			case map[string]any:
				field = "labels"
			case []string:
				field = "tags"
			}
			held, err := s.Query(ctx(), collection, docstore.Query{Where: []docstore.Where{{Field: field, Equals: tc.filter}}})
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if got := len(held) == 1; got != tc.want {
				t.Errorf("Query on %s = %#v matched %d documents, want %v: a filter compares the JSON value it was given, so a number matches the number whoever carried it and never the text of one", field, tc.filter, len(held), tc.want)
			}
		})
	}
}

func testCommitTogether(t *testing.T, open Factory) {
	s := open(t)
	err := s.RunTransaction(ctx(), func(ctx context.Context, tx docstore.Transaction) error {
		held, err := tx.Get(ctx, collection, "one", nil)
		if err != nil {
			return err
		}
		if held {
			return fmt.Errorf("the transaction read a document nobody wrote")
		}
		if err := tx.Put(ctx, collection, "one", map[string]any{"name": "acme"}); err != nil {
			return err
		}
		return tx.Put(ctx, collection, "two", map[string]any{"name": "beta"})
	})
	if err != nil {
		t.Fatalf("RunTransaction = %v, want nil", err)
	}
	for _, id := range []string{"one", "two"} {
		var got map[string]any
		held, err := s.Get(ctx(), collection, id, &got)
		if err != nil || !held {
			t.Fatalf("after the transaction committed, Get(%s) = (%v, %v), want the document it wrote", id, held, err)
		}
	}

	// The transaction holds what its own function read: a document another
	// writer put is the state the next attempt sees, and a function that
	// depends on a value it read has to depend on the one it was given.
	if err := s.RunTransaction(ctx(), func(ctx context.Context, tx docstore.Transaction) error {
		var held map[string]any
		got, err := tx.Get(ctx, collection, "one", &held)
		if err != nil || !got {
			return fmt.Errorf("Get inside a transaction = (%v, %v), want the document the last write left", got, err)
		}
		if held["name"] != "acme" {
			return fmt.Errorf("the transaction read %v, want the name the committed write left", held)
		}
		return nil
	}); err != nil {
		t.Fatalf("RunTransaction = %v, want nil", err)
	}
}

func testFailedTransaction(t *testing.T, open Factory) {
	s := open(t)
	refused := errors.New("the caller gave up")
	err := s.RunTransaction(ctx(), func(ctx context.Context, tx docstore.Transaction) error {
		if err := tx.Put(ctx, collection, "one", map[string]any{"name": "acme"}); err != nil {
			return err
		}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatalf("RunTransaction = %v, want the error its function returned: a caller's own failure is answered as itself and not as a conflict to retry", err)
	}
	held, err := s.Get(ctx(), collection, "one", nil)
	if err != nil || held {
		t.Errorf("after a transaction whose function failed, Get = (%v, %v), want the document the transaction staged to be gone: a transaction that does not commit writes nothing", held, err)
	}
}

func testReadAfterWrite(t *testing.T, open Factory) {
	s := open(t)
	err := s.RunTransaction(ctx(), func(ctx context.Context, tx docstore.Transaction) error {
		if err := tx.Put(ctx, collection, "one", map[string]any{"name": "acme"}); err != nil {
			return err
		}
		if _, err := tx.Get(ctx, collection, "one", nil); err != nil {
			return err
		}
		return errors.New("the transaction read a document it had already written and the store said nothing")
	})
	if !errors.Is(err, docstore.ErrReadAfterWrite) {
		t.Errorf("Get after Put inside a transaction = %v, want docstore.ErrReadAfterWrite: Firestore's transactions are read-before-write, and a seam whose memory store allowed the read would be a seam that only appears to hold on the backend the deploy names", err)
	}
	if _, err := s.Query(ctx(), collection, docstore.Query{Where: []docstore.Where{{Field: "name", Equals: "acme"}}}); err != nil {
		t.Errorf("the collection after the refused transaction: %v", err)
	}
}

func testNestedTransaction(t *testing.T, open Factory) {
	s := open(t)
	err := s.RunTransaction(ctx(), func(ctx context.Context, tx docstore.Transaction) error {
		return s.RunTransaction(ctx, func(context.Context, docstore.Transaction) error { return nil })
	})
	if err == nil {
		t.Errorf("a transaction opened inside another committed, want a refusal: one backend refuses the nesting and a seam that allowed it would be a seam a caller could build a deadlock on")
	}
}

func testUnnamed(t *testing.T, open Factory) {
	s := open(t)
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"a put with no collection", func() error { return s.Put(ctx(), "", "one", map[string]any{"a": 1}) }},
		{"a put with no id", func() error { return s.Put(ctx(), collection, "", map[string]any{"a": 1}) }},
		{"a get with no collection", func() error { _, err := s.Get(ctx(), "", "one", nil); return err }},
		{"a get with no id", func() error { _, err := s.Get(ctx(), collection, "", nil); return err }},
		{"a query with no collection", func() error { _, err := s.Query(ctx(), "", docstore.Query{}); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, docstore.ErrUnnamed) {
				t.Errorf("%s = %v, want docstore.ErrUnnamed: the empty name is the one id every unnamed caller would share, so a document written under it would answer for all of them", tc.name, err)
			}
		})
	}

	if err := s.Put(ctx(), collection, "one", 42); !errors.Is(err, docstore.ErrNotADocument) {
		t.Errorf("Put of a number = %v, want docstore.ErrNotADocument: a store of documents holds objects, and a value that is not one has no fields to read back", err)
	}
	if err := s.Put(ctx(), collection, "one", map[string]any(nil)); !errors.Is(err, docstore.ErrNotADocument) {
		t.Errorf("Put of a nil map = %v, want docstore.ErrNotADocument", err)
	}
}

// idsOf is the ids a query answered, in the order it answered them.
func idsOf(docs []docstore.Document) []string {
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.ID)
	}
	return out
}

// TestTransactionRetriesOnConflict is the one rule the suite above cannot state
// as a rule of the seam: what a transaction does when another writer changes a
// document between its read and its commit. It is written against the memory
// store alone, because the conflict has to be provoked from inside the
// transaction's own function to be a case rather than a race, and the only
// writer a test can reach from there is the same process's. What the emulator
// proves is the same rule end to end, by two clients against one database
// (P9.2's suite), and it is not something this file can make deterministic.
func TestTransactionRetriesOnConflict(t *testing.T) {
	store := docstore.NewMemory()
	if err := store.Put(ctx(), collection, "counter", map[string]any{"n": 0}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	attempts := 0
	err := store.RunTransaction(ctx(), func(ctx context.Context, tx docstore.Transaction) error {
		attempts++
		var held struct {
			N int `json:"n"`
		}
		if _, err := tx.Get(ctx, collection, "counter", &held); err != nil {
			return err
		}
		if attempts == 1 {
			// Another instance writes the document the transaction has just
			// read, which is the whole of a conflict: the read the function
			// decided on is no longer the state it would commit onto.
			if err := store.Put(context.Background(), collection, "counter", map[string]any{"n": 100}); err != nil {
				return err
			}
		}
		return tx.Put(ctx, collection, "counter", map[string]any{"n": held.N + 1})
	})
	if err != nil {
		t.Fatalf("RunTransaction = %v, want nil: a transaction that met a conflict runs again on the state that conflicted with it", err)
	}
	if attempts != 2 {
		t.Fatalf("the transaction's function ran %d time(s), want 2: once onto the state it read and once onto the one that replaced it", attempts)
	}

	var got struct {
		N int `json:"n"`
	}
	if held, err := store.Get(ctx(), collection, "counter", &got); err != nil || !held {
		t.Fatalf("Get after the transaction = (%v, %v)", held, err)
	}
	if got.N != 101 {
		t.Errorf("the counter is %d, want 101: the retry read the write that conflicted with it (100) and added its own increment to that, and the increment the conflicted attempt staged landed nowhere", got.N)
	}
}

// TestDocstoreConformance runs the suite against the store a test drives.
func TestDocstoreConformance(t *testing.T) {
	Run(t, func(t *testing.T) docstore.Store { return docstore.NewMemory() })
}
