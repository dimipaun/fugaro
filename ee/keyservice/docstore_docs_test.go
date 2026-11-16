// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The deployment's own Docs (docstore_docs.go). The seam's rules are held by
// the cases over the test double in workspaces_test.go; what these hold is what
// that double cannot: the adapter over the portal's real document store, and the
// one thing it does that the store's transaction cannot — a delete, staged and
// swept after the commit, which is the shape the operations seed's undo needs
// (invites.go).
//
// The assertions are the seam's, not the store's: a case here that reads a
// document back through the same adapter could pass while the adapter wrote
// something the deploy's own readers cannot see, so the last case reads through
// the licence store's DocStore over the same database — the two seams are two
// faces of one store, and a withdrawal a binding makes has to land with the
// binding (api.go's Tx.Revoke).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/keyservice/docstore"
)

func TestTheDocsAdapterIsTheDocumentStore(t *testing.T) {
	ctx := context.Background()
	docs := NewDocStoreDocs(docstore.NewMemory())

	if _, err := docs.Get(ctx, "orgs", "org_1"); !errors.Is(err, ErrNoDocument) {
		t.Fatalf("a document the store does not hold answered %v, want ErrNoDocument", err)
	}
	held := Doc{"id": "org_1", "name": "Acme", "owner": "owner@acme.example"}
	if err := docs.Put(ctx, "orgs", "org_1", held); err != nil {
		t.Fatalf("Put = %v", err)
	}
	read, err := docs.Get(ctx, "orgs", "org_1")
	if err != nil {
		t.Fatalf("Get = %v", err)
	}
	if read["name"] != "Acme" || read["owner"] != "owner@acme.example" {
		t.Fatalf("the document read back as %v", read)
	}
	if err := docs.Put(ctx, "orgs", "org_2", Doc{"id": "org_2", "owner": "other@acme.example"}); err != nil {
		t.Fatalf("Put = %v", err)
	}
	// A Where matches one field; a Where with no field is the collection.
	matched, err := docs.Query(ctx, "orgs", Where{Field: "owner", Value: "owner@acme.example"})
	if err != nil {
		t.Fatalf("Query = %v", err)
	}
	if len(matched) != 1 || matched[0]["id"] != "org_1" {
		t.Fatalf("the query answered %v", matched)
	}
	all, err := docs.Query(ctx, "orgs", Where{})
	if err != nil {
		t.Fatalf("Query = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("an empty Where answered %d documents, want the collection's two", len(all))
	}
}

// TestTheDocsAdapterSweepsWhatATransactionDeletes is the one difference between
// this adapter and the document store's own transaction, pinned in both
// directions: a committed transaction's delete lands, and a transaction that
// failed deletes nothing.
func TestTheDocsAdapterSweepsWhatATransactionDeletes(t *testing.T) {
	ctx := context.Background()
	docs := NewDocStoreDocs(docstore.NewMemory())
	if err := docs.Put(ctx, "invites", "inv_1", Doc{"id": "inv_1", "state": "pending"}); err != nil {
		t.Fatalf("Put = %v", err)
	}
	// The undo the seed makes: one delete, in a transaction of its own.
	if err := docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		return tx.Delete(ctx, "invites", "inv_1")
	}); err != nil {
		t.Fatalf("Update = %v", err)
	}
	if _, err := docs.Get(ctx, "invites", "inv_1"); !errors.Is(err, ErrNoDocument) {
		t.Fatalf("the deleted invitation answered %v, want ErrNoDocument", err)
	}
	// A transaction that returns an error commits nothing, and the delete it
	// staged is not swept: what the caller asked for did not happen.
	if err := docs.Put(ctx, "invites", "inv_2", Doc{"id": "inv_2", "state": "pending"}); err != nil {
		t.Fatalf("Put = %v", err)
	}
	if err := docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		if err := tx.Delete(ctx, "invites", "inv_2"); err != nil {
			return err
		}
		return errors.New("the mail could not be sent")
	}); err == nil {
		t.Fatal("a transaction that returned an error was reported as committed")
	}
	if _, err := docs.Get(ctx, "invites", "inv_2"); err != nil {
		t.Fatalf("a refused transaction's delete was swept anyway: %v", err)
	}
}

// TestTheDocsAdapterWithdrawsIntoTheLicenceStore holds the call api.go's Tx
// describes: a withdrawal made inside the portal's transaction lands in the
// store the licence records are kept in, under the store's own rule for a key
// that already has an entry.
func TestTheDocsAdapterWithdrawsIntoTheLicenceStore(t *testing.T) {
	ctx := context.Background()
	db := docstore.NewMemory()
	docs := NewDocStoreDocs(db)
	store := NewDocStore(db)
	withdrawnAt := issuedAt
	if err := docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		if err := tx.Revoke(ctx, []Revocation{{KeyID: "fgk_1", Reason: "refunded", At: withdrawnAt}}); err != nil {
			return err
		}
		return tx.Put(ctx, "workspaces", "ws_1", Doc{"id": "ws_1", "key": "fgk_1"})
	}); err != nil {
		t.Fatalf("Update = %v", err)
	}
	held, ok, err := store.Revocation(ctx, "fgk_1")
	if err != nil || !ok {
		t.Fatalf("the revocation was not in the licence store: %v (held %v)", err, ok)
	}
	if held.Reason != "refunded" || !held.At.Equal(withdrawnAt) {
		t.Fatalf("the revocation was read back as %+v", held)
	}
	// First writer wins: a later withdrawal of the same key leaves the entry.
	if err := docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		return tx.Revoke(ctx, []Revocation{{KeyID: "fgk_1", Reason: "rotated", At: withdrawnAt.Add(48 * time.Hour)}})
	}); err != nil {
		t.Fatalf("Update = %v", err)
	}
	held, _, err = store.Revocation(ctx, "fgk_1")
	if err != nil {
		t.Fatalf("Revocation = %v", err)
	}
	if held.Reason != "refunded" {
		t.Fatalf("the earlier entry was overwritten: %+v", held)
	}
	// And a revocation that names no key is refused, as the store's own is.
	if err := docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		return tx.Revoke(ctx, []Revocation{{Reason: "refunded"}})
	}); !errors.Is(err, ErrRevocationUnnamed) {
		t.Fatalf("Revoke of an unnamed key answered %v, want ErrRevocationUnnamed", err)
	}
}
