// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

//go:build firestore

// The Firestore backend run against the emulator: the same suite as the memory
// store (conformance_test.go's Run), on the backend the deploy names, so the
// two answers are the same answers and not two files that happen to compile.
//
// It is behind a build tag and it skips without an emulator, because it is the
// one test in this package that needs a database process: `go test ./...` on a
// machine with none still passes, and the workflow that provides one is P9.2's
// (staged under docs/pending-workflows/). Nothing here reaches a real project —
// FIRESTORE_EMULATOR_HOST is what the SDK dials, and with it set there are no
// credentials to be had.
//
// **Every case runs under a collection prefix of its own.** The emulator is one
// database shared by everything a test binary starts, and the suite requires a
// backend that is empty at the start of each case (Factory's contract in
// conformance_test.go): a fixed collection name would let one case see the
// documents of the one before it, which is the way this suite would pass while
// proving nothing.

package docstore_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/dimipaun/fugaro/ee/keyservice/docstore"
)

// emulatorProject is the project id the client is built with. The emulator
// ignores it — it names the database process, not a project anything is billed
// for — and the SDK refuses an empty one.
const emulatorProject = "fugaro-portal-test"

func TestFirestoreConformance(t *testing.T) {
	client := emulator(t)
	Run(t, func(t *testing.T) docstore.Store {
		return prefixed{db: docstore.NewFirestore(client), prefix: uniq(t)}
	})
}

// TestFirestoreWritesTheCollectionItWasGiven is the one fact the suite cannot
// state: that the documents really land in the named collection of the database
// and are not held in the process. A backend that answered from a map and never
// wrote would pass every case above and lose the portal's records on the first
// deploy. The collection name is read off the client itself, past the seam,
// because that name is exactly what is under test.
func TestFirestoreWritesTheCollectionItWasGiven(t *testing.T) {
	ctx := context.Background()
	client := emulator(t)
	prefix := uniq(t)
	store := prefixed{db: docstore.NewFirestore(client), prefix: prefix}
	if err := store.Put(ctx, "widgets", "one", map[string]any{"name": "acme"}); err != nil {
		t.Fatalf("Put = %v", err)
	}

	snap, err := client.Collection(prefix + "-widgets").Doc("one").Get(ctx)
	if err != nil {
		t.Fatalf("reading the document the store wrote, straight from the client: %v", err)
	}
	if got := snap.Data()["name"]; got != "acme" {
		t.Errorf("the database holds %v for the document's name, want %q", got, "acme")
	}
}

// emulator builds one client against the emulator, skipping when the process
// names none: a test that dialled a real project would be the one thing this
// package's tests must never do.
func emulator(t *testing.T) *firestore.Client {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("no FIRESTORE_EMULATOR_HOST: the Firestore backend's suite needs an emulator (P9.2 provides one)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := firestore.NewClient(ctx, emulatorProject)
	if err != nil {
		t.Fatalf("building a client for the emulator: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// uniq names one case's collections. The name carries the case and a counter
// rather than the clock alone, so a failure names the collections to look at.
var caseNumber atomic.Int64

func uniq(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("docstore-%s-%d", strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()), caseNumber.Add(1))
}

// prefixed is one case's view of the shared emulator: the backend under its own
// collection names, so a suite that needs an empty store finds one.
type prefixed struct {
	db     docstore.Store
	prefix string
}

func (p prefixed) collection(name string) string { return p.prefix + "-" + name }

func (p prefixed) Get(ctx context.Context, collection, id string, out any) (bool, error) {
	return p.db.Get(ctx, p.collection(collection), id, out)
}

func (p prefixed) Put(ctx context.Context, collection, id string, doc any) error {
	return p.db.Put(ctx, p.collection(collection), id, doc)
}

func (p prefixed) Query(ctx context.Context, collection string, q docstore.Query) ([]docstore.Document, error) {
	return p.db.Query(ctx, p.collection(collection), q)
}

func (p prefixed) RunTransaction(ctx context.Context, fn func(ctx context.Context, tx docstore.Transaction) error) error {
	return p.db.RunTransaction(ctx, func(ctx context.Context, tx docstore.Transaction) error {
		return fn(ctx, prefixedTx{tx: tx, prefix: p.prefix})
	})
}

// prefixedTx is the transaction's half of the same view.
type prefixedTx struct {
	tx     docstore.Transaction
	prefix string
}

func (t prefixedTx) collection(name string) string { return t.prefix + "-" + name }

func (t prefixedTx) Get(ctx context.Context, collection, id string, out any) (bool, error) {
	return t.tx.Get(ctx, t.collection(collection), id, out)
}

func (t prefixedTx) Put(ctx context.Context, collection, id string, doc any) error {
	return t.tx.Put(ctx, t.collection(collection), id, doc)
}

func (t prefixedTx) Query(ctx context.Context, collection string, q docstore.Query) ([]docstore.Document, error) {
	return t.tx.Query(ctx, t.collection(collection), q)
}
