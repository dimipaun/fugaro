// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

// This file is the reach the front door serves the state group over (O16b): the
// account it reads as, the read it makes, and the document it refuses. It runs
// against internal/gcpfake's Realtime Database rather than an in-memory double,
// so what is tested is the read a real database answers — the paths under
// `orchestrator/<id>`, the credential every request carries, and that no request
// is a write — rather than a struct that agrees with the code beside it.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// frontDoorToken is the credential the reach's reads carry: the front door's own
// account's, which the process holds because it runs as that account. It is a
// value of the test's own and not a secret — what the test reads back is that
// every request carried it, never what it is.
const frontDoorToken = "front-door-account-token"

// stateReachFixture is the front door's database as the reach reads it: one
// orchestrator's node and its log, seeded the way this release writes them, and
// the reach built over a client that carries the account's own credential.
type stateReachFixture struct {
	db    *gcpfake.RTDB
	reach *ProjectState
	id    string
	state orchestrator.State
}

func newStateReachFixture(t *testing.T) *stateReachFixture {
	t.Helper()
	f := &stateReachFixture{db: gcpfake.NewRTDB(t), id: testStateID}
	f.state = orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           3,
		AgentID:       testStateID,
		Role:          orchestrator.RoleOrchestrator,
		Goal:          "ship the state group's read half",
	}
	raw, err := orchestrator.EncodeState(f.state)
	if err != nil {
		t.Fatal(err)
	}
	path, err := orchestrator.PathState(f.id)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(path, json.RawMessage(raw))
	// The log is seeded as the map the node holds, keyed by each entry's derived
	// id, so the reach has to order it rather than read a sequence out of it.
	entries := map[string]orchestrator.Entry{}
	for _, e := range []orchestrator.Entry{
		{Subject: "web-fix", Transition: "approved", Text: "the person said yes"},
		{Subject: "web-fix", Transition: "launched", Text: "run 20261108-120000-aaaa"},
	} {
		e.ID, e.At = orchestrator.EntryID(e.Subject, e.Transition), time.Now().UTC().Add(-2*time.Minute)
		entries[e.ID] = e
	}
	auditPath, err := orchestrator.PathAudit(f.id)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(auditPath, entries)

	db, err := rtdb.New(f.db.URL, rtdb.Auth{IDToken: func() string { return frontDoorToken }})
	if err != nil {
		t.Fatal(err)
	}
	reach, err := NewProjectState(db)
	if err != nil {
		t.Fatalf("NewProjectState: %v", err)
	}
	f.reach = reach
	return f
}

// node reads a path back as the JSON value the database holds it as.
func (f *stateReachFixture) node(t *testing.T, path string) string {
	t.Helper()
	v := f.db.Value(path)
	if v == nil {
		return ""
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestStateReachReadsAsTheFrontDoorsOwnAccount: the reach is the front door's
// own account reading the project's node, and nothing else happens.
//
// Three facts, and each is one the served group depends on. The reads answer
// what `fugaro orchestrator state` and `fugaro orchestrator events` answer for
// the same node — the state the document holds, and the log in the order the
// server stamped it with `since` cut on the server's clock. Every request the
// reach made carried the account's credential, which is what "as the front
// door's own account" means: there is no second identity here and no credential
// of the reach's own. And no request was a write: both nodes hold byte for byte
// what they held before, which is the whole of D2020's read-only answer — the
// account holds `instances.get` and no database write.
func TestStateReachReadsAsTheFrontDoorsOwnAccount(t *testing.T) {
	f := newStateReachFixture(t)
	ctx := context.Background()
	statePath, err := orchestrator.PathState(f.id)
	if err != nil {
		t.Fatal(err)
	}
	auditPath, err := orchestrator.PathAudit(f.id)
	if err != nil {
		t.Fatal(err)
	}
	before := [2]string{f.node(t, statePath), f.node(t, auditPath)}
	if before[0] == "" || before[1] == "" {
		t.Fatal("the fixture wrote neither node, so a read of them proves nothing")
	}

	got, err := f.reach.Load(ctx, f.id)
	if err != nil {
		t.Fatalf("Load(%s): %v", f.id, err)
	}
	if got.Rev != f.state.Rev || got.Goal != f.state.Goal || got.SchemaVersion != f.state.SchemaVersion {
		t.Errorf("Load answered %+v, and the node holds %+v", got, f.state)
	}

	events, err := f.reach.Events(ctx, f.id, 0)
	if err != nil {
		t.Fatalf("Events(%s, 0): %v", f.id, err)
	}
	if len(events) != 2 || events[0].Transition != "approved" || events[1].Transition != "launched" {
		t.Fatalf("Events answered %v, and the log holds the two entries oldest first", events)
	}

	// Two lines sit two minutes back, so a minute and a half keeps neither and a
	// cut wider than them keeps both: the boundary is the server's clock's, which
	// is the clock the fixture stamped the entries with.
	recent, err := f.reach.Events(ctx, f.id, 90*time.Second)
	if err != nil {
		t.Fatalf("Events(%s, 90s): %v", f.id, err)
	}
	if len(recent) != 0 {
		t.Errorf("Events with a 90s cut answered %v, and the log's two lines are two minutes old", recent)
	}
	whole, err := f.reach.Events(ctx, f.id, 5*time.Minute)
	if err != nil {
		t.Fatalf("Events(%s, 5m): %v", f.id, err)
	}
	if len(whole) != 2 {
		t.Errorf("Events with a 5m cut answered %d entries, and both lines are inside it", len(whole))
	}

	creds := f.db.Credentials()
	if len(creds) == 0 {
		t.Fatal("the reach made no request at all, so nothing above read the database")
	}
	for i, c := range creds {
		if c != "auth:"+frontDoorToken {
			t.Errorf("request %d carried %q, and every read this reach makes is the front door's own account's", i+1, c)
		}
	}
	if after := [2]string{f.node(t, statePath), f.node(t, auditPath)}; after != before {
		t.Errorf("a read changed a node: the state is %s and the log is %s, and they were %s and %s",
			after[0], after[1], before[0], before[1])
	}
}

// TestStateReachRefusesAnotherLoopsDocument: the role a reach reads with is the
// orchestrator's and not the document's, so a node another loop keeps is refused
// rather than answered as this project's state. The front door reads one id's
// state and has no way to ask for another loop's, which is what keeps a
// coordinator's document from being served as an orchestrator's.
func TestStateReachRefusesAnotherLoopsDocument(t *testing.T) {
	f := newStateReachFixture(t)
	raw, err := orchestrator.EncodeState(orchestrator.State{
		SchemaVersion: orchestrator.SchemaVersion,
		Rev:           1,
		AgentID:       f.id,
		Role:          orchestrator.RoleCoordinator,
	})
	if err != nil {
		t.Fatal(err)
	}
	path, err := orchestrator.PathState(f.id)
	if err != nil {
		t.Fatal(err)
	}
	f.db.Set(path, json.RawMessage(raw))

	if _, err := f.reach.Load(context.Background(), f.id); !errors.Is(err, orchestrator.ErrNotThisNode) {
		t.Fatalf("Load answered %v for a document the coordinator keeps; want ErrNotThisNode", err)
	}
}

// TestTheStateReachNeedsItsDatabase: a reach built without a reader is refused
// where it is built, rather than answering a nil dereference on the first call a
// client makes. NewProjectState is the only way a front door gets one, so the
// refusal lands on whoever wired the door up.
func TestTheStateReachNeedsItsDatabase(t *testing.T) {
	if _, err := NewProjectState(nil); err == nil {
		t.Fatal("NewProjectState(nil) built a reach")
	}
}
