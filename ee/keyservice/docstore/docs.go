// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// Package docstore is the portal's own storage: collections of JSON documents,
// and the four things its callers need of them — read one, write one, query a
// collection, and run a group of those as one all-or-nothing transaction.
//
// **A document is JSON, and nothing else.** Whatever a caller writes is
// marshalled on the way in and decoded on the way out, so no fact about a
// backend's own types reaches a caller: Firestore's timestamps, its int64-and-
// double split and its microsecond precision are properties of one backend, and
// a record read back through this seam is the record that was written. The
// portal's records are JSON in any case — they are the shapes the API answers
// with (P1.1) — and a store that kept them as anything else would need a second
// mapping kept in step with the first.
//
// **A query reads a collection and matches here.** The scale this store is
// built for is the one ee/keyservice/store.go already states for the licence
// records: an installation's organisations, workspaces, entitlements and
// events, thousands rather than millions — the scale below which reading the
// list is the honest shape rather than a shortcut. Matching in one place,
// rather than once here and once in Firestore's query language, is also what
// makes the two backends one store: an equality that meant one thing in a test
// and another in the deploy would be a rule the seam only appears to hold. A
// collection that outgrows this needs the query pushed down to the backend, and
// that is a change here and in firestore.go together.
//
// **The memory store is not a mock.** It holds the same rules as the backend
// the deploy names — including a transaction's read-before-write, its
// all-or-nothing commit and its retry on a conflict — because it is what every
// test of a caller drives, and a caller tested against a store that was more
// forgiving would be a caller that fails in the deploy (conformance_test.go
// runs one suite against both).
package docstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Store is the portal's storage. Get and Put address one document by its
// collection and its id, Query reads a collection, and RunTransaction runs a
// function whose reads and writes land together or not at all.
//
// Nothing here is a schema: a collection is a name a caller chose, and this
// package never sees the shape of what is written in one beyond the JSON object
// it must be. What a collection means is the caller's — the portal's records
// have one package each — and the store's whole business is to give them back
// the way they went in.
type Store interface {
	// Get reads one document into out, which is a pointer to the shape Put
	// wrote it as, or nil for a caller that only asks whether it is there. The
	// second answer is false for a document the store does not hold, which is
	// an answer rather than an error.
	Get(ctx context.Context, collection, id string, out any) (bool, error)
	// Put writes one document whole. It is an upsert by id and never a merge:
	// a field the caller left out is gone, which is what makes a document a
	// value rather than an accumulation of writes. A caller that must not
	// overwrite reads inside a transaction first.
	Put(ctx context.Context, collection, id string, doc any) error
	// Query answers the documents of one collection that match q, in id order.
	Query(ctx context.Context, collection string, q Query) ([]Document, error)
	// RunTransaction runs fn, and commits what it wrote only if nothing it
	// read changed underneath it; when something did, fn runs again on the new
	// state. It is what a check-then-write has to be built on: two callers
	// asking the same question at once must not both get the answer they asked
	// for (ee/keyservice/store.go's ClaimTrial and SaveIssuance are the two the
	// licence records need).
	RunTransaction(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error
}

// Transaction is one run of a function's reads and writes. Its reads are
// answered from the state the transaction commits onto, its writes land
// together when it commits, and a write it stages is visible to nobody else
// until then.
//
// **Reads come before writes**, which is the rule the backend the deploy names
// keeps: Firestore's transactions refuse a read of a document the same
// transaction has already written, and a memory store that answered it would
// answer differently from the deploy. What a function needs beyond that — a
// value it wants to write twice, a document it just wrote — it holds in a
// variable of its own.
type Transaction interface {
	Get(ctx context.Context, collection, id string, out any) (bool, error)
	Put(ctx context.Context, collection, id string, doc any) error
	// Query reads a collection inside the transaction. The documents it
	// answers are the read set the commit is validated against, so a write to
	// that collection landing in between is a conflict rather than a state the
	// transaction never saw.
	Query(ctx context.Context, collection string, q Query) ([]Document, error)
}

// Document is one document as the store holds it: the id it is filed under and
// the fields it was written with, as a JSON decoder reads them.
type Document struct {
	ID     string
	Fields map[string]any
}

// Decode fills out — a pointer to the shape the document was written as — from
// the document's fields.
//
// A field the shape does not know is left alone rather than refused: the
// records a deployed API reads were written by whichever version was running
// then, so a reader has to survive a field a later version added. What is
// refused is a field of another type, because a record the store cannot read
// whole is one no caller may act on.
func (d Document) Decode(out any) error {
	if out == nil {
		return nil
	}
	return decode(d.Fields, out)
}

// Query selects the documents of one collection. Its zero value is the whole
// collection, and there is no ordering to choose: every query answers in id
// order, because a list that came back in the order a backend happened to reach
// the documents would be a different list on every backend and after every
// compaction, and a caller could not tell the difference from its own.
type Query struct {
	// Where holds the filters, all of which must match. An empty slice is
	// every document.
	Where []Where
	// Limit caps how many documents come back, after the matches are ordered
	// by id; 0 means no cap.
	Limit int
}

// Where is one equality filter on one field.
type Where struct {
	// Field is one key of the document's own object. A dotted name is not a
	// path here: the records the portal keeps are flat where they are looked
	// up, and the one backend that reads a dot as a path into a nested object
	// would otherwise answer a filter this package reads as a literal key.
	Field string
	// Equals is compared with the field's value as the JSON each of them
	// writes, so the number 3 matches the field a Go struct wrote as an int and
	// the field a decoder read back as a float64 alike, while the text "3"
	// matches neither. An object or an array compares whole.
	Equals any
}

// ErrUnnamed is a call with no collection or no id. Both are refused rather
// than written: the empty id is the one name every unnamed caller would share,
// so a document under it would answer every such call at once.
var ErrUnnamed = errors.New("docstore: a document is named by its collection and its id")

// ErrNotADocument is a Put of something that is not a JSON object — a number, a
// string, a nil map. A store of documents holds objects, and a value with no
// fields has nothing to read back.
var ErrNotADocument = errors.New("docstore: a document is a JSON object")

// ErrReadAfterWrite is a transaction that read a document it had already
// written. It is refused because the backend the deploy names refuses it, and a
// run that depended on the read would be a run that worked in a test and failed
// in the deploy.
var ErrReadAfterWrite = errors.New("docstore: a transaction reads before it writes")

// ErrConflict is a transaction that met a conflict on every attempt. It is a
// state and not a failure of the store: what the caller does about it is what
// it does about any other transient refusal — answer "try again" and let the
// next call read the state that keeps winning.
var ErrConflict = errors.New("docstore: the transaction kept conflicting with another writer")

// errNestedTransaction is a transaction opened inside another's function. The
// only backend here refuses it (its own client does), so the memory store
// refuses it too: a seam that allowed the nesting in a test would be a seam
// whose callers built one.
var errNestedTransaction = errors.New("docstore: a transaction is not nested")

// maxAttempts is how many times a transaction's function runs before ErrConflict
// is the answer. It is the five the Firestore client's transactions default to
// (its DefaultTransactionMaxAttempts), so both backends give up at the same
// point rather than one of them retrying a contention the other would have
// answered.
const maxAttempts = 5

// encode is what Put writes: the document's fields as the JSON object they
// marshal to. Going through JSON rather than keeping what the caller passed is
// what makes what is read back equal to what was written and never the caller's
// own value — a slice or a map a caller keeps editing after the write does not
// reach the stored document, and a type the backend has no column for (a
// time.Time, an int) is the shape JSON gives it on every backend alike.
func encode(doc any) (map[string]any, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("docstore: a document has to be JSON: %w", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, ErrNotADocument
	}
	return fields, nil
}

// decode fills out from fields, which is the same round trip in the other
// direction.
func decode(fields map[string]any, out any) error {
	if out == nil {
		return nil
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("docstore: the stored document is not JSON: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("docstore: the stored document does not fit the shape it was asked for: %w", err)
	}
	return nil
}

// selectDocs is what both backends answer a query with, so that a query means
// one thing: the matches, in id order, cut to the limit. It takes the documents
// a backend read and never a query it handed down, which is the limit this
// package states — the backend that could filter and order a collection itself
// is the one a collection that outgrows this would move to.
func selectDocs(docs []Document, q Query) []Document {
	out := make([]Document, 0, len(docs))
	for _, d := range docs {
		if matches(d.Fields, q.Where) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out
}

// matches reports whether a document's fields satisfy every filter.
func matches(fields map[string]any, where []Where) bool {
	for _, f := range where {
		held, known := fields[f.Field]
		if !known || !equalJSON(held, f.Equals) {
			return false
		}
	}
	return true
}

// equalJSON compares two values by the JSON each of them writes, which is the
// comparison a JSON store admits: the same number arrives as an int from a Go
// struct and as a float64 from a document a backend decoded, and a caller that
// filtered on the first must find the second. A value that cannot be written as
// JSON matches nothing.
func equalJSON(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(left, right)
}
