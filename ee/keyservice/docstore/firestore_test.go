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
	"errors"
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

// TestPrefixedKeepsTheEmptyNameEmpty is the wrapper's own rule, and it is the
// one fact about it a test can state without a database. The suite's cases for a
// call that names nothing pass the empty collection, and the store owes them a
// refusal: a wrapper that answered "-" for it — a prefix in front of nothing,
// which is a name as valid as any other — would hand the backend a collection to
// file a document under and let a put through where the memory store refuses it,
// so the emulator run would fail on the first of those cases instead of proving
// that one suite holds both stores.
func TestPrefixedKeepsTheEmptyNameEmpty(t *testing.T) {
	p := prefixed{prefix: "docstore-x-1"}
	if got := p.collection(""); got != "" {
		t.Errorf("the wrapper turned an empty collection name into %q, want the empty name it was given: the suite's unnamed cases have to reach the backend unnamed", got)
	}
	tx := prefixedTx{prefix: "docstore-x-1"}
	if got := tx.collection(""); got != "" {
		t.Errorf("the transaction's wrapper turned an empty collection name into %q, want the empty name it was given", got)
	}
	if got := p.collection("widgets"); got != "docstore-x-1-widgets" {
		t.Errorf("the wrapper named a collection %q, want the case's prefix in front of the name it was given", got)
	}
}

// TestPrefixedLeavesARefusedNameAlone is the rest of that rule, and it is what
// the suite's name cases need of the wrapper under -tags firestore: a call under
// a name the store refuses has to reach the store as the name the case asked
// for, or the emulator run fails on a collection the wrapper renamed. The
// backend here is the memory store, which is the same seam and needs no
// database — what is under test is the name the wrapper passes on, not the
// answer a database gives it.
func TestPrefixedLeavesARefusedNameAlone(t *testing.T) {
	ctx := context.Background()
	p := prefixed{db: docstore.NewMemory(), prefix: "docstore-x-1"}
	tx := prefixedTx{prefix: "docstore-x-1"}
	for _, tc := range []struct {
		why        string
		collection string
		want       error
	}{
		{"a collection in the database's reserved form", "__reserved__", docstore.ErrUnstorableName},
		{"a collection carrying the store's path separator", "one/two", docstore.ErrUnstorableName},
		{"no collection at all", "", docstore.ErrUnnamed},
	} {
		if got := p.collection(tc.collection); got != tc.collection {
			t.Errorf("the wrapper named %s %q, want the name it was given: a prefix in front of it asks the backend about a collection the case never named, and the emulator run then fails the backend for the wrapper's own rename", tc.why, got)
		}
		if got := tx.collection(tc.collection); got != tc.collection {
			t.Errorf("the transaction's wrapper named %s %q, want the name it was given", tc.why, got)
		}
		if err := p.Put(ctx, tc.collection, "one", map[string]any{"a": 1}); !errors.Is(err, tc.want) {
			t.Errorf("a put under %s through the wrapper = %v, want %v: the suite's case for it has to be refused by the store and not answered by a collection of the wrapper's making", tc.why, err, tc.want)
		}
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

// prefixedName is the wrapper's whole rule, in one place because the store and
// its transaction have to name a collection the same way: a case's prefix goes
// in front of a name the store would file, and a name it would not is answered
// with the name it was given.
//
// A name like that never reaches the database — the store refuses it before any
// client is built — so there is nothing to keep apart, and prefixing it would be
// the wrapper's own mistake with the backend's name on it. A prefix in front of
// the empty name is a collection the case never named, and one in front of the
// database's reserved form __.*__ stops it matching that form: either way the
// suite's case for a name the store must refuse would instead ask the backend
// something it can file and fail the emulator run for the answer to a question
// the wrapper invented (TestPrefixedLeavesARefusedNameAlone).
func prefixedName(prefix, name string) string {
	if !docstore.Storable(name) {
		return name
	}
	return prefix + "-" + name
}

func (p prefixed) collection(name string) string { return prefixedName(p.prefix, name) }

func (p prefixed) Get(ctx context.Context, collection, id string, out any) (bool, error) {
	return p.db.Get(ctx, p.collection(collection), id, out)
}

func (p prefixed) Put(ctx context.Context, collection, id string, doc any) error {
	return p.db.Put(ctx, p.collection(collection), id, doc)
}

func (p prefixed) Delete(ctx context.Context, collection, id string) error {
	return p.db.Delete(ctx, p.collection(collection), id)
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

func (t prefixedTx) collection(name string) string { return prefixedName(t.prefix, name) }

func (t prefixedTx) Get(ctx context.Context, collection, id string, out any) (bool, error) {
	return t.tx.Get(ctx, t.collection(collection), id, out)
}

func (t prefixedTx) Put(ctx context.Context, collection, id string, doc any) error {
	return t.tx.Put(ctx, t.collection(collection), id, doc)
}

func (t prefixedTx) Query(ctx context.Context, collection string, q docstore.Query) ([]docstore.Document, error) {
	return t.tx.Query(ctx, t.collection(collection), q)
}
