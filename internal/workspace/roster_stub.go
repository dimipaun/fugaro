package workspace

// This file is C1's scaffolding, and it is the one part of the roster contract
// that goes away: I1 adapts the stores WU1 merged (RosterStore, AuditWriter)
// onto the Store and AuditSink interfaces and deletes this file, which is why
// every stub lives here and nothing the sides keep does.
//
// Each stub is the SHAPE of the thing it is named for and decides nothing: it
// reads no node, writes no node and creates no document. That is the whole of
// what a stub may do while the level-2 tasks are written in parallel against
// these interfaces (A1): each of them injects its own double in its own test, so
// none of them can pass on a decision no side has made, because nothing here
// makes one.
//
// Read answers the fail-closed zero value the contract fixes: no bytes, Found
// false, which is what a workspace whose roster has never been written reads as
// and what a caller then decides about (with its own double, or with the real
// store). The two WRITES answer an error instead, and that asymmetry is
// deliberate: nil is the one answer a stub must not give, because a caller that
// read nil would believe a roster node was written or an audit entry was
// created that is not there -- a roster that never landed and an audit entry
// nothing holds, which is the trail and the fence both failing in silence.

import (
	"context"
	"errors"
)

// errStubWrite is what every stub write answers: nothing was written, and the
// caller has no store behind it yet.
var errStubWrite = errors.New("workspace: this is the contract's stub and not a store: nothing was written (I1 adapts the real store onto the interface)")

// StubStore is the shape of a Store and nothing else: it names no node, reads
// no node and writes none.
func StubStore() Store { return stubStore{} }

type stubStore struct{}

// Path is empty: the stub names no workspace's node, and a path is the one
// thing it cannot invent -- a caller that wrote to it would be writing
// somewhere no workspace owns.
func (stubStore) Path() string { return "" }

func (stubStore) Read(context.Context) (Revision, []byte, error) { return Revision{}, nil, nil }

func (stubStore) Write(context.Context, Revision, []byte) error { return errStubWrite }

// StubAuditSink is the shape of an AuditSink and nothing else: it creates no
// document, and it does not remember an id either, so a caller that minted one
// learns nothing about whether it is taken.
func StubAuditSink() AuditSink { return stubAuditSink{} }

type stubAuditSink struct{}

func (stubAuditSink) Create(context.Context, string, []byte) error { return errStubWrite }
