// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The deploy's own Docs (api.go), over the portal's document store
// (ee/keyservice/docstore). The seam was declared by P1.1 and P1.2 and, until
// this file, the only implementation of it in the repository was the test double
// in workspaces_test.go: P1.2's routes are held by cases over that double, and a
// deployment that never built the adapter would start and answer nothing.
//
// It is the same shape as that double on purpose — one backing store, the same
// two answers for a document that is not there — because the cases over it are
// what hold the seam's contract, and an adapter that answered differently would
// be a second contract with no test on it.
//
// **The two seams are two faces of one database.** NewDocStore takes the store
// the licence records live in and this takes the same one, which is what makes
// a binding's withdrawal land with the binding (api.go's Tx.Revoke): a
// deployment that handed the two seams two stores would have the withdrawal
// commit onto data no reader sees, which is why cmd/portal-api builds both over
// one client.
//
// **A removal is the transaction's, as a write is.** The portal's Tx has a
// Delete and the store's transaction has one of its own (docstore's docs.go),
// so a transaction that removes a group of documents removes them in its commit
// or leaves every one of them: an organisation deletion takes the organisation
// with its memberships, its invitations and its workspaces, and a group swept
// one document at a time after the commit leaves the state no retry can leave —
// the memberships gone with the organisation still there, so the person asking
// again is not a member of anything and the route answers 404 for ever
// (ee/keyservice/deletion.go). What this seam answers for is therefore nothing
// the store lacks: it is the store's own commit, handed through.

import (
	"context"

	"github.com/dimipaun/fugaro/ee/keyservice/docstore"
)

// DocStoreDocs is api.go's Docs over the portal's own document store.
type DocStoreDocs struct {
	db docstore.Store
}

// NewDocStoreDocs is the adapter a deployment wires behind Deps.Docs, over the
// same store the licence records are kept in.
func NewDocStoreDocs(db docstore.Store) *DocStoreDocs { return &DocStoreDocs{db: db} }

func (d *DocStoreDocs) Get(ctx context.Context, collection, id string) (Doc, error) {
	var held map[string]any
	found, err := d.db.Get(ctx, collection, id, &held)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNoDocument
	}
	return Doc(held), nil
}

func (d *DocStoreDocs) Put(ctx context.Context, collection, id string, doc Doc) error {
	return d.db.Put(ctx, collection, id, doc)
}

func (d *DocStoreDocs) Query(ctx context.Context, collection string, where Where) ([]Doc, error) {
	return queryDocuments(ctx, d.db.Query, collection, where)
}

// Update runs one transaction, whose writes and removals are the store's own:
// the function's grouped writes land together when it commits, and a group it
// asked to be rid of is not removed at all when it does not (this file's head).
func (d *DocStoreDocs) Update(ctx context.Context, fn func(context.Context, Tx) error) error {
	return d.db.RunTransaction(ctx, func(ctx context.Context, inner docstore.Transaction) error {
		return fn(ctx, &docsTx{Transaction: inner})
	})
}

// docsTx is one transaction of that store, as the portal's seam sees it.
type docsTx struct {
	docstore.Transaction
}

func (t *docsTx) Get(ctx context.Context, collection, id string) (Doc, error) {
	var held map[string]any
	found, err := t.Transaction.Get(ctx, collection, id, &held)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNoDocument
	}
	return Doc(held), nil
}

func (t *docsTx) Query(ctx context.Context, collection string, where Where) ([]Doc, error) {
	return queryDocuments(ctx, t.Transaction.Query, collection, where)
}

func (t *docsTx) Put(ctx context.Context, collection, id string, doc Doc) error {
	return t.Transaction.Put(ctx, collection, id, doc)
}

// Delete removes one document with this transaction's writes, so an error the
// store answers is the caller's transaction failing rather than a record that
// went. It does not check that the document is there, as the store's own Delete
// does not: the caller is asking for a state, and a record already gone is that
// state.
func (t *docsTx) Delete(ctx context.Context, collection, id string) error {
	return t.Transaction.Delete(ctx, collection, id)
}

// Revoke withdraws keys inside this transaction, the way store.go's own Revoke
// does: the records go to the revocations collection of the store this
// transaction runs in, and a key that already has an entry keeps the reason and
// the instant of the entry that withdrew it.
//
// Every read comes before every write because the document store refuses one
// after the other (docstore's ErrReadAfterWrite), which is why the whole list is
// one call: a caller makes it before the transaction writes anything else, and a
// per-key call would put a write between two reads.
func (t *docsTx) Revoke(ctx context.Context, revocations []Revocation) error {
	withdrawn := make([]bool, len(revocations))
	for i, r := range revocations {
		if r.KeyID == "" {
			return ErrRevocationUnnamed
		}
		held, err := t.Transaction.Get(ctx, revocationCollection, r.KeyID, nil)
		if err != nil {
			return err
		}
		withdrawn[i] = held
	}
	for i, r := range revocations {
		if withdrawn[i] {
			continue
		}
		if err := t.Transaction.Put(ctx, revocationCollection, r.KeyID, r); err != nil {
			return err
		}
	}
	return nil
}

// readDocs is what both sides of this file read a collection with: the
// document store's own two readers answer the same way, and the one place the
// match is built is what keeps a Query's meaning from being stated twice.
type readDocs func(ctx context.Context, collection string, q docstore.Query) ([]docstore.Document, error)

func queryDocuments(ctx context.Context, read readDocs, collection string, where Where) ([]Doc, error) {
	// An empty field is the collection's documents and not a match on nothing,
	// which is what Where states and what the store's own query means.
	q := docstore.Query{}
	if where.Field != "" {
		q.Where = []docstore.Where{{Field: where.Field, Equals: where.Value}}
	}
	found, err := read(ctx, collection, q)
	if err != nil {
		return nil, err
	}
	docs := make([]Doc, 0, len(found))
	for _, doc := range found {
		docs = append(docs, Doc(doc.Fields))
	}
	return docs, nil
}
