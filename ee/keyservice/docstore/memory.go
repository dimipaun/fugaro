// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package docstore

import (
	"context"
	"sync"
)

// Memory is the store every test of a caller drives: the same methods and the
// same rules as the backend the deploy names, in one process.
//
// It is safe for concurrent use because a service's handlers are, and its
// transaction is the whole of what makes it more than a map: a read records the
// version of what it read, a query records the collection as it stood, and a
// commit that finds either of them moved applies nothing and runs the function
// again. That is the same optimistic shape Firestore's transactions have, and
// the point of having it here is that a caller's check-then-write is exercised
// against a store that really can refuse it: a memory store that held a global
// lock across the function would let every such caller pass a test it would
// fail in the deploy.
type Memory struct {
	mu      sync.Mutex
	colls   map[string]*memCollection
	version uint64
}

// memCollection is one collection: its documents and the instant it last
// changed. The epoch is what a query inside a transaction is validated against
// — a write to the collection moves it, whether or not it touched a document the
// query matched — because a document that arrives matching a filter is a change
// to the query's answer, and the memory store cannot tell the two apart without
// keeping the filters it was asked. Firestore, which can, is the backend whose
// transactions see the finer half of this; a coarser read set costs a retry and
// never a wrong answer.
type memCollection struct {
	epoch uint64
	docs  map[string]memDocument
}

// memDocument is one document and the version it was written at. The fields are
// the map a put encoded once: every read marshals them again rather than handing
// them out, so no caller can reach the store's own value.
type memDocument struct {
	fields  map[string]any
	version uint64
}

// NewMemory builds an empty store.
func NewMemory() *Memory {
	return &Memory{colls: map[string]*memCollection{}}
}

// txInProgressKey marks a context that is already inside a transaction, which is
// how the nesting is refused: the function's own context is the only thing a
// call it makes can carry in, and a store that used a field on itself could not
// tell two callers apart.
type txInProgressKey struct{}

func (m *Memory) Get(_ context.Context, collection, id string, out any) (bool, error) {
	if err := named(collection, id); err != nil {
		return false, err
	}
	m.mu.Lock()
	fields, held := m.fields(collection, id)
	m.mu.Unlock()
	if !held {
		return false, nil
	}
	return true, decode(fields, out)
}

func (m *Memory) Put(_ context.Context, collection, id string, doc any) error {
	if err := named(collection, id); err != nil {
		return err
	}
	fields, err := encode(doc)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.write(collection, id, fields)
	return nil
}

// Delete drops one document, as the seam says. The collection's epoch moves
// with it: a document that goes is a change to what a query of that collection
// answers, and a transaction sitting on a query of it must meet the conflict
// rather than commit onto the list it read.
func (m *Memory) Delete(_ context.Context, collection, id string) error {
	if err := named(collection, id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	coll := m.colls[collection]
	if coll == nil {
		return nil
	}
	if _, held := coll.docs[id]; !held {
		return nil
	}
	delete(coll.docs, id)
	m.version++
	coll.epoch = m.version
	return nil
}

func (m *Memory) Query(_ context.Context, collection string, q Query) ([]Document, error) {
	if collection == "" {
		return nil, ErrUnnamed
	}
	m.mu.Lock()
	docs, err := m.snapshot(collection)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return selectDocs(docs, q), nil
}

// fields is one document's fields as the store holds them. The caller holds the
// lock; the map it answers with belongs to the store, so a caller that decodes
// it must not keep it.
func (m *Memory) fields(collection, id string) (map[string]any, bool) {
	coll := m.colls[collection]
	if coll == nil {
		return nil, false
	}
	held, known := coll.docs[id]
	return held.fields, known
}

// snapshot is every document of one collection, each with fields of its own: a
// query answers the documents it read, and a caller that edited the map one of
// them carried would otherwise be editing the store. The database backend hands
// out copies (it decodes what it read), so this one has to as well or the two
// would differ in a way no caller could see coming.
func (m *Memory) snapshot(collection string) ([]Document, error) {
	coll := m.colls[collection]
	if coll == nil {
		return nil, nil
	}
	out := make([]Document, 0, len(coll.docs))
	for id, held := range coll.docs {
		fields, err := encode(held.fields)
		if err != nil {
			return nil, err
		}
		out = append(out, Document{ID: id, Fields: fields})
	}
	return out, nil
}

// write is one document's write: the fields are stored and the collection's
// epoch moves. The caller holds the lock.
func (m *Memory) write(collection, id string, fields map[string]any) {
	coll := m.colls[collection]
	if coll == nil {
		coll = &memCollection{docs: map[string]memDocument{}}
		m.colls[collection] = coll
	}
	m.version++
	coll.epoch = m.version
	coll.docs[id] = memDocument{fields: fields, version: m.version}
}

func (m *Memory) RunTransaction(ctx context.Context, fn func(ctx context.Context, tx Transaction) error) error {
	if ctx.Value(txInProgressKey{}) != nil {
		return errNestedTransaction
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		tx := &memTx{store: m, writes: map[string]map[string]map[string]any{}}
		if err := fn(context.WithValue(ctx, txInProgressKey{}, true), tx); err != nil {
			return err
		}
		committed, err := tx.commit()
		if err != nil {
			return err
		}
		if committed {
			return nil
		}
	}
	return ErrConflict
}

// memRead is one thing a transaction read: a document's version, or — with no
// id — a collection's epoch. It is what the commit is validated against.
type memRead struct {
	collection string
	id         string
	version    uint64
}

// memTx is one attempt's reads and staged writes. It disappears at the end of
// the attempt: what a conflicted attempt wrote is dropped with it, which is what
// makes the retry a rerun of the function rather than a second half of one.
type memTx struct {
	store   *Memory
	read    []memRead
	writes  map[string]map[string]map[string]any
	written bool
}

func (tx *memTx) Get(_ context.Context, collection, id string, out any) (bool, error) {
	if err := named(collection, id); err != nil {
		return false, err
	}
	if tx.written {
		return false, ErrReadAfterWrite
	}
	tx.store.mu.Lock()
	fields, held := tx.store.fields(collection, id)
	var version uint64
	if coll := tx.store.colls[collection]; coll != nil {
		version = coll.docs[id].version
	}
	tx.store.mu.Unlock()
	tx.read = append(tx.read, memRead{collection: collection, id: id, version: version})
	if !held {
		return false, nil
	}
	return true, decode(fields, out)
}

func (tx *memTx) Put(_ context.Context, collection, id string, doc any) error {
	if err := named(collection, id); err != nil {
		return err
	}
	fields, err := encode(doc)
	if err != nil {
		return err
	}
	if tx.writes[collection] == nil {
		tx.writes[collection] = map[string]map[string]any{}
	}
	tx.writes[collection][id] = fields
	tx.written = true
	return nil
}

func (tx *memTx) Query(_ context.Context, collection string, q Query) ([]Document, error) {
	if collection == "" {
		return nil, ErrUnnamed
	}
	if tx.written {
		return nil, ErrReadAfterWrite
	}
	tx.store.mu.Lock()
	var epoch uint64
	if coll := tx.store.colls[collection]; coll != nil {
		epoch = coll.epoch
	}
	docs, err := tx.store.snapshot(collection)
	tx.store.mu.Unlock()
	if err != nil {
		return nil, err
	}
	tx.read = append(tx.read, memRead{collection: collection, version: epoch})
	return selectDocs(docs, q), nil
}

// commit applies the attempt's writes when nothing it read moved. The second
// answer is false for a conflict, which is the caller's signal to run the
// function again rather than an error.
func (tx *memTx) commit() (bool, error) {
	m := tx.store
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range tx.read {
		var held uint64
		coll := m.colls[r.collection]
		if coll != nil {
			if r.id == "" {
				held = coll.epoch
			} else {
				held = coll.docs[r.id].version
			}
		}
		if held != r.version {
			return false, nil
		}
	}
	for collection, docs := range tx.writes {
		for id, fields := range docs {
			m.write(collection, id, fields)
		}
	}
	return true, nil
}

// named refuses a document nothing named.
func named(collection, id string) error {
	if collection == "" || id == "" {
		return ErrUnnamed
	}
	return nil
}
