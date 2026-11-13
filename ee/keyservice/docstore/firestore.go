// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package docstore

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Firestore is the store the deploy names: the same seam as Memory, on the
// portal's own database in the project the service runs in (P1.5 creates it and
// names it `portal`). It is the only file in this package that names a
// database, which is what the seam is for — an owner who answers the storage
// question with something else writes one file beside this one and its test.
//
// **What it keeps is what Memory keeps.** A document is the JSON object encode
// produced, written whole; a read decodes it back; a query reads the collection
// and selects with the same code the memory store uses. Nothing here translates
// between Firestore's types and a caller's, and that is deliberate: the SDK's
// types (a Timestamp, an int64 beside a float64, microsecond precision on a
// time) would otherwise be a second set of rules a caller had to know, and the
// one place they could differ from the memory store is the one place no test
// runs by default.
//
// **The client's transactions are the ones that retry.** They are optimistic
// and the server refuses a commit whose read set moved, so the seam's promise —
// a function that runs again on the state that conflicted with it — is the
// SDK's own behaviour and not something reimplemented here; the attempt bound is
// this package's, so both backends give up at the same point. The suite that
// proves the rest of it (conformance_test.go) runs against both, the memory
// store by default and this one under the `firestore` tag against the emulator
// (firestore_test.go; the workflow that runs it is staged by P9.2).
type Firestore struct {
	client *firestore.Client
}

// NewFirestore wraps one client. The client is the caller's to build and to
// close: it carries the project, the credentials and the emulator host, none of
// which is this seam's business.
func NewFirestore(client *firestore.Client) *Firestore { return &Firestore{client: client} }

func (f *Firestore) Get(ctx context.Context, collection, id string, out any) (bool, error) {
	if err := named(collection, id); err != nil {
		return false, err
	}
	snap, err := f.client.Collection(collection).Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("docstore: reading %s/%s: %w", collection, id, err)
	}
	if err := decode(snap.Data(), out); err != nil {
		return false, err
	}
	return true, nil
}

func (f *Firestore) Put(ctx context.Context, collection, id string, doc any) error {
	if err := named(collection, id); err != nil {
		return err
	}
	fields, err := encode(doc)
	if err != nil {
		return err
	}
	// Set and never a merge: a put is the whole document, and a merge would
	// leave the fields of the write it replaced behind (conformance_test.go
	// holds both backends to it).
	if _, err := f.client.Collection(collection).Doc(id).Set(ctx, fields); err != nil {
		return fmt.Errorf("docstore: writing %s/%s: %w", collection, id, err)
	}
	return nil
}

// Delete drops one document, as the seam says. A document the database does not
// hold is the state the caller asked for and not a failure, which is what the
// SDK's own NotFound means here.
func (f *Firestore) Delete(ctx context.Context, collection, id string) error {
	if err := named(collection, id); err != nil {
		return err
	}
	_, err := f.client.Collection(collection).Doc(id).Delete(ctx)
	if err != nil && status.Code(err) != codes.NotFound {
		return fmt.Errorf("docstore: dropping %s/%s: %w", collection, id, err)
	}
	return nil
}

func (f *Firestore) Query(ctx context.Context, collection string, q Query) ([]Document, error) {
	if collection == "" {
		return nil, ErrUnnamed
	}
	docs, err := readAll(f.client.Collection(collection).Documents(ctx))
	if err != nil {
		return nil, err
	}
	return selectDocs(docs, q), nil
}

func (f *Firestore) RunTransaction(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error {
	err := f.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		return fn(ctx, &firestoreTx{client: f.client, tx: tx})
	}, firestore.MaxAttempts(maxAttempts))
	// Out of attempts with the commit still losing the race is the seam's own
	// answer and not the database's: the caller's move is to let the next call
	// read the state that keeps winning, which is what ErrConflict asks for.
	if status.Code(err) == codes.Aborted {
		return ErrConflict
	}
	return err
}

// firestoreTx is one attempt's transaction. It carries the client because the
// SDK addresses a document through the client's own reference and not through
// the transaction, and it carries whether the attempt has written, because the
// SDK refuses a read after a write with an error of its own and the seam has to
// refuse it as the sentinel both backends answer with (docstore.ErrReadAfterWrite,
// which the suite holds this backend to as hard as the memory store).
type firestoreTx struct {
	client *firestore.Client
	tx     *firestore.Transaction
	wrote  bool
}

func (t *firestoreTx) ref(collection, id string) *firestore.DocumentRef {
	return t.client.Collection(collection).Doc(id)
}

func (t *firestoreTx) Get(ctx context.Context, collection, id string, out any) (bool, error) {
	if err := named(collection, id); err != nil {
		return false, err
	}
	if t.wrote {
		return false, ErrReadAfterWrite
	}
	snap, err := t.tx.Get(t.ref(collection, id))
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := decode(snap.Data(), out); err != nil {
		return false, err
	}
	return true, nil
}

func (t *firestoreTx) Put(_ context.Context, collection, id string, doc any) error {
	if err := named(collection, id); err != nil {
		return err
	}
	fields, err := encode(doc)
	if err != nil {
		return err
	}
	if err := t.tx.Set(t.ref(collection, id), fields); err != nil {
		return err
	}
	t.wrote = true
	return nil
}

func (t *firestoreTx) Query(_ context.Context, collection string, q Query) ([]Document, error) {
	if collection == "" {
		return nil, ErrUnnamed
	}
	if t.wrote {
		return nil, ErrReadAfterWrite
	}
	docs, err := readAll(t.tx.Documents(t.client.Collection(collection)))
	if err != nil {
		return nil, err
	}
	return selectDocs(docs, q), nil
}

// readAll is every document an iterator answers, each with the fields the
// database holds. A collection with no documents in it is an empty list and not
// an error, on this backend as on the other.
func readAll(it *firestore.DocumentIterator) ([]Document, error) {
	var out []Document
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("docstore: reading a collection: %w", err)
		}
		out = append(out, Document{ID: snap.Ref.ID, Fields: snap.Data()})
	}
}
