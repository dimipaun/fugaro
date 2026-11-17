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
// **What the store cannot do, this seam answers for.** The document seam's
// transaction has no delete (docstore's own docs.go says why: the one record
// that has to go is a handover, and the caller that wrote it sweeps it outside a
// transaction). The portal's Tx has one, and the one caller that reaches it
// deletes a single document in a transaction of its own — the operations seed's
// invitation when its mail could not be sent (invites.go). So the delete is
// staged by the transaction and dropped from the store once it has committed,
// which is the same thing for a transaction that does nothing else and is
// written here rather than left to fail: a record swept after a commit is gone,
// and what is lost against a real in-transaction delete is only the case this
// seam has no caller for — a transaction that writes a document and deletes
// another, where a crash between the commit and the sweep leaves the deleted one
// behind.

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

// Update runs one transaction. Its writes are the store's, and the deletes it
// staged are dropped once it has committed: see this file's head.
func (d *DocStoreDocs) Update(ctx context.Context, fn func(context.Context, Tx) error) error {
	var swept []sweep
	tx := &docsTx{}
	err := d.db.RunTransaction(ctx, func(ctx context.Context, inner docstore.Transaction) error {
		tx.Transaction = inner
		tx.staged = nil
		if err := fn(ctx, tx); err != nil {
			return err
		}
		swept = append([]sweep(nil), tx.staged...)
		return nil
	})
	if err != nil {
		return err
	}
	for _, one := range swept {
		if err := d.db.Delete(ctx, one.collection, one.id); err != nil {
			return err
		}
	}
	return nil
}

// sweep is one document a committed transaction asked to be rid of.
type sweep struct{ collection, id string }

// docsTx is one transaction of that store, as the portal's seam sees it.
type docsTx struct {
	docstore.Transaction
	staged []sweep
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

// Delete stages one document for the sweep this file's head describes. It does
// not check that the document is there, as the store's own Delete does not: the
// caller is asking for a state, and a record already gone is that state.
func (t *docsTx) Delete(_ context.Context, collection, id string) error {
	t.staged = append(t.staged, sweep{collection: collection, id: id})
	return nil
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
