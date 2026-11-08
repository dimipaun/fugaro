package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The state store's tests (O3, F5, G9).
//
// The state node is where every decision a daemon makes lives, so these are
// the tests of what may write it: one conditional write under the ETag the
// caller read, the lease re-checked before that write, a refusal rather than a
// forced write when either the ETag or the lease has moved, and a load that
// fails closed on a document this release cannot read. Everything runs on
// internal/gcpfake's Realtime Database fake: no live cloud, no clock, no key
// and no secret.
//
// The other half of G9 is TestHeartbeatDoesNotMoveTheStateETag below: `state`,
// `lease` and `audit` are siblings under `orchestrator/<id>`, and a heartbeat
// must not move the state's ETag, because a parent write would wipe the
// children and one shared ETag would make every beat a state write.

// TestStaleSaveIsRefusedNotForced is the rule G9 states in one line: a stale
// writer's write is refused, never forced. Nothing here is a message about a
// stale write — each case reads the node the store left behind, because a
// refusal that wrote anyway would be a refusal nobody could tell from a
// success.
//
// Mutation: drop the If-Match (write with PutIfMatch's etag replaced by an
// unconditional PUT) and the stale write lands; re-read the ETag after a 412
// and write the caller's body under it and the third case's one write becomes
// several, each of them over a document the caller never read.
func TestStaleSaveIsRefusedNotForced(t *testing.T) {
	ctx := context.Background()

	t.Run("a writer whose ETag moved is refused and the node keeps the other writer's state", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		counting := &countingCAS{inner: rtdbClient(t, f)}
		node := mustNode(t, counting, nil)

		fresh, etag, err := node.Load(ctx)
		if err != nil {
			t.Fatalf("the first load: %v", err)
		}
		if err := node.Save(ctx, fresh, etag); err != nil {
			t.Fatalf("creating the node: %v", err)
		}

		// Another writer moves the node: what a daemon's tick would have done
		// between this writer's read and its write.
		moved, movedEtag, err := node.Load(ctx)
		if err != nil {
			t.Fatalf("the second load: %v", err)
		}
		moved.Rev = 7
		if err := node.Save(ctx, moved, movedEtag); err != nil {
			t.Fatalf("the other writer's save: %v", err)
		}

		saveErr := node.Save(ctx, fresh, etag)
		if !errors.Is(saveErr, orchestrator.ErrStateMoved) {
			t.Fatalf("a save under a moved ETag ended as %v, not ErrStateMoved", saveErr)
		}
		if counting.puts != 3 {
			t.Errorf("the writer made %d writes for three saves (create, the other writer's, the refused one): one conditional write each", counting.puts)
		}
		if got := storedRev(t, f, node); got != 7 {
			t.Errorf("the node says rev %v after the refused write: a stale writer's state landed", got)
		}
	})

	t.Run("a writer with no ETag never writes", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		counting := &countingCAS{inner: rtdbClient(t, f)}
		node := mustNode(t, counting, nil)

		fresh, _, err := node.Load(ctx)
		if err != nil {
			t.Fatalf("the load: %v", err)
		}
		saveErr := node.Save(ctx, fresh, "")
		if !errors.Is(saveErr, orchestrator.ErrStateMoved) {
			t.Fatalf("a write with no ETag ended as %v, not ErrStateMoved", saveErr)
		}
		if counting.puts != 0 {
			t.Errorf("a write with no ETag reached the store %d times: an empty ETag is a forced write", counting.puts)
		}
		if f.Value(mustPath(t, orchestrator.PathState)) != nil {
			t.Error("a write with no ETag created the node")
		}
	})

	t.Run("a node that moved is never written over, however many times it is asked", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		refusing := &refusingCAS{inner: rtdbClient(t, f)}
		node := mustNode(t, refusing, &stubLease{held: true})

		fresh, etag, err := node.Load(ctx)
		if err != nil {
			t.Fatalf("the load: %v", err)
		}
		for i := 0; i < 3; i++ {
			if saveErr := node.Save(ctx, fresh, etag); !errors.Is(saveErr, orchestrator.ErrStateMoved) {
				t.Fatalf("save %d ended as %v, not ErrStateMoved", i+1, saveErr)
			}
		}
		if refusing.puts != 3 {
			t.Errorf("three refused saves reached the node %d times: a body derived from the document the caller read is written once, never pushed again under a fresher ETag",
				refusing.puts)
		}
		if f.Value(mustPath(t, orchestrator.PathState)) != nil {
			t.Error("a save that lost every race still created the node")
		}
	})
}

// TestSaveRechecksTheLeaseBeforeARetry is G9's second half, and the reason the
// lease is not the fence: the lease and the state ETag are separate nodes, so a
// daemon that lost its lease can still hold a fresh state ETag. The name is the
// plan's, and the answer here is stronger than a re-check between attempts:
// there is one attempt and the lease is checked before it, so a daemon whose
// lease went to another machine cannot record the intent it would then act on —
// and a lost race is refused rather than pushed again under a fresher ETag
// (TestStaleSaveIsRefusedNotForced says why). The cases below cover both: the
// write that would have been a second, retried write, and the write that must
// not happen at all.
//
// Mutation: drop Node.checkLease and both of the last two cases write while the
// lease is not this daemon's.
func TestSaveRechecksTheLeaseBeforeARetry(t *testing.T) {
	ctx := context.Background()

	t.Run("a save whose write lost the race is refused, not written twice", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		flaky := &flakyCAS{inner: rtdbClient(t, f), refuse: 1}
		lease := &stubLease{held: true}
		node := mustNode(t, flaky, lease)

		fresh, etag, err := node.Load(ctx)
		if err != nil {
			t.Fatalf("the load: %v", err)
		}
		saveErr := node.Save(ctx, fresh, etag)
		if !errors.Is(saveErr, orchestrator.ErrStateMoved) {
			t.Fatalf("a save whose write lost the race ended as %v, not ErrStateMoved", saveErr)
		}
		if flaky.puts != 1 {
			t.Errorf("the save wrote %d times under one held lease: a second attempt would push a body derived from the document the caller read",
				flaky.puts)
		}
		if lease.checks != 1 {
			t.Errorf("the lease was checked %d times for one write, not once", lease.checks)
		}
		if f.Value(mustPath(t, orchestrator.PathState)) != nil {
			t.Error("a save that lost the race created the node anyway")
		}
	})

	t.Run("a save with the lease gone writes nothing", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		refusing := &refusingCAS{inner: rtdbClient(t, f)}
		lease := &stubLease{held: false}
		node := mustNode(t, refusing, lease)

		fresh, etag, err := node.Load(ctx)
		if err != nil {
			t.Fatalf("the load: %v", err)
		}
		saveErr := node.Save(ctx, fresh, etag)
		if !errors.Is(saveErr, orchestrator.ErrLeaseLost) {
			t.Fatalf("a save that lost its lease ended as %v, not ErrLeaseLost", saveErr)
		}
		if refusing.puts != 0 || lease.checks != 1 {
			t.Errorf("the save wrote %d times after %d lease checks: a lease that is not this daemon's is checked before the write and then no write",
				refusing.puts, lease.checks)
		}
		if f.Value(mustPath(t, orchestrator.PathState)) != nil {
			t.Error("a daemon that lost its lease wrote the state")
		}
	})

	t.Run("a lease that cannot be read is a refusal, not a write", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		refusing := &refusingCAS{inner: rtdbClient(t, f)}
		lease := &stubLease{err: errors.New("the lease node is not readable")}
		node := mustNode(t, refusing, lease)

		fresh, etag, err := node.Load(ctx)
		if err != nil {
			t.Fatalf("the load: %v", err)
		}
		saveErr := node.Save(ctx, fresh, etag)
		if saveErr == nil || errors.Is(saveErr, orchestrator.ErrLeaseLost) {
			t.Fatalf("an unreadable lease ended as %v: a daemon that cannot tell whether it holds the id must fail closed", saveErr)
		}
		if refusing.puts != 0 {
			t.Errorf("the save wrote %d times over an unreadable lease", refusing.puts)
		}
		if !strings.Contains(saveErr.Error(), "lease") {
			t.Errorf("the refusal does not say what could not be read: %v", saveErr)
		}
	})
}

// TestStoreFailsClosedOnAnUnreadableState: a state node this release cannot
// read is refused, never repaired, never half-read, and nothing is written
// over it. This is the client half of D1241 — the rules refuse a token writer,
// so a document that exists was written by the person's own credential, and
// what stops a malformed one from being acted on is here.
//
// Mutation: skip DecodeState in Load (unmarshal straight into a State) and the
// unknown-field and future-version cases both load; drop the owner check and a
// node of another orchestrator id is read as this daemon's own.
func TestStoreFailsClosedOnAnUnreadableState(t *testing.T) {
	ctx := context.Background()
	statePath := mustPath(t, orchestrator.PathState)

	for _, tc := range []struct {
		name string
		doc  any
		want error
	}{
		{"a field this release does not model", map[string]any{
			"schema_version": 1, "rev": 0, "agent_id": daemonID, "role": orchestrator.RoleOrchestrator, "status": "running",
		}, orchestrator.ErrInvalid},
		{"a schema version this release cannot know", map[string]any{
			"schema_version": orchestrator.SchemaVersion + 1, "rev": 0, "agent_id": daemonID, "role": orchestrator.RoleOrchestrator,
		}, orchestrator.ErrSchemaVersion},
		{"a value that is not a document", "not a state", orchestrator.ErrInvalid},
		{"another orchestrator's state", map[string]any{
			"schema_version": 1, "rev": 3, "agent_id": "orchestrator-b", "role": orchestrator.RoleOrchestrator,
		}, orchestrator.ErrNotThisNode},
		{"another loop's state", map[string]any{
			"schema_version": 1, "rev": 3, "agent_id": daemonID, "role": orchestrator.RoleCoordinator,
		}, orchestrator.ErrNotThisNode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := gcpfake.NewRTDB(t)
			counting := &countingCAS{inner: rtdbClient(t, f)}
			node := mustNode(t, counting, nil)
			f.Set(statePath, tc.doc)
			before := f.Value(statePath)

			s, etag, err := node.Load(ctx)
			if !errors.Is(err, tc.want) {
				t.Fatalf("loading %s ended as %v, not %v", tc.name, err, tc.want)
			}
			if etag != "" || !reflect.DeepEqual(s, orchestrator.State{}) {
				t.Errorf("a refused load returned a state (%+v) and an ETag (%q): fail closed means neither", s, etag)
			}
			if !reflect.DeepEqual(f.Value(statePath), before) {
				t.Error("a refused load changed the node")
			}
		})
	}

	t.Run("a state this release would not write never reaches the node", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		counting := &countingCAS{inner: rtdbClient(t, f)}
		node := mustNode(t, counting, nil)

		fresh, etag, err := node.Load(ctx)
		if err != nil {
			t.Fatalf("the load: %v", err)
		}
		if err := node.Save(ctx, fresh, etag); err != nil {
			t.Fatalf("creating the node: %v", err)
		}
		before := f.Value(statePath)

		for _, bad := range []orchestrator.State{
			func() orchestrator.State { s := fresh; s.SchemaVersion = orchestrator.SchemaVersion + 1; return s }(),
			func() orchestrator.State {
				s := fresh
				s.WorkItems = []orchestrator.WorkItem{{ID: "wi-1", Phase: "halfway"}}
				return s
			}(),
		} {
			if err := node.Save(ctx, bad, etag); !errors.Is(err, orchestrator.ErrInvalid) && !errors.Is(err, orchestrator.ErrSchemaVersion) {
				t.Fatalf("saving a state this release would not read back ended as %v", err)
			}
		}
		if counting.puts != 1 {
			t.Errorf("the store saw %d writes, of which one created the node: a document that fails its own validator was published", counting.puts)
		}
		if !reflect.DeepEqual(f.Value(statePath), before) {
			t.Error("a refused save changed the node")
		}
	})

	t.Run("a node that does not exist yet loads as this orchestrator's empty state", func(t *testing.T) {
		f := gcpfake.NewRTDB(t)
		node := mustNode(t, &countingCAS{inner: rtdbClient(t, f)}, nil)

		s, etag, err := node.Load(ctx)
		if err != nil {
			t.Fatalf("the first load of an absent node: %v", err)
		}
		if s.SchemaVersion != orchestrator.SchemaVersion || s.AgentID != daemonID || s.Role != orchestrator.RoleOrchestrator || s.Rev != 0 {
			t.Fatalf("an absent node loaded as %+v, not this orchestrator's empty state", s)
		}
		if etag == "" {
			t.Fatal("an absent node loaded with no ETag: the first save would have no precondition")
		}
		if err := node.Save(ctx, s, etag); err != nil {
			t.Fatalf("creating the node from the empty state: %v", err)
		}
		if got := storedRev(t, f, node); got != 0 {
			t.Errorf("the created node reads back as rev %v", got)
		}
	})
}

// TestHeartbeatDoesNotMoveTheStateETag is the sibling rule of G9. A heartbeat
// that moved the state's ETag would make every beat a lost race for the
// daemon's own save, and a write to the parent would wipe all three children
// at once — so the three are separate nodes, and this test reads the state's
// ETag back after three beats and an append rather than trusting that they
// are.
//
// Mutation: build the paths so the lease or the audit writes the parent
// (PathNode) and the state node disappears; share one path between the state
// and the lease and the ETag moves on every beat.
func TestHeartbeatDoesNotMoveTheStateETag(t *testing.T) {
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	db := rtdbClient(t, f)

	clock := leaseAt
	now := func() (time.Time, bool) { return clock, true }
	lease := mustLease(t, db, winnerHolder, now)
	node := mustNode(t, db, lease)
	audit := mustAudit(t, db, now)

	// The lease first, as a daemon does: the state is written by whoever holds
	// the id, so this test cannot even create it before the claim.
	if _, err := lease.Acquire(ctx); err != nil {
		t.Fatalf("taking the lease: %v", err)
	}
	fresh, etag, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("the load: %v", err)
	}
	if err := node.Save(ctx, fresh, etag); err != nil {
		t.Fatalf("creating the state: %v", err)
	}
	_, before, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("reading the state's ETag: %v", err)
	}

	for i := 0; i < 3; i++ {
		clock = clock.Add(orchestrator.LeaseBeat)
		if err := lease.Beat(ctx); err != nil {
			t.Fatalf("beat %d: %v", i+1, err)
		}
	}
	if _, err := audit.Append(ctx, orchestrator.Entry{Subject: winnerHolder, Transition: "started"}); err != nil {
		t.Fatalf("appending to the audit log: %v", err)
	}

	_, after, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("re-reading the state's ETag: %v", err)
	}
	if after != before {
		t.Errorf("the state node's ETag moved from %q to %q across three heartbeats and an append: the siblings are not separate nodes",
			before, after)
	}

	parent := nodeObject(t, f.Value(mustPath(t, orchestrator.PathNode)), "the orchestrator node")
	keys := make([]string, 0, len(parent))
	for k := range parent {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{orchestrator.AuditNode, orchestrator.LeaseNode, orchestrator.StateNode}; !reflect.DeepEqual(keys, want) {
		t.Errorf("the orchestrator node carries %v, not exactly %v: a fourth sibling, or a value written to the parent", keys, want)
	}
}

// TestLoadMigratesAnOlderSchemaVersion: a node written by an older release is
// raised on the way in and written back raised, so every reader of the node
// sees this release's version from the next save on. The document is O0's own
// fixture with its version removed — a v0 node — so this test reads what the
// previous generation really wrote rather than a shape invented here.
//
// The fixture is written to the fake and saved back through the node, so this
// is also where a name the transport refuses would show: a fact key is a
// property name in the state document, and Save would fail with the database's
// 400 rather than with a refusal this package made (D1231: the id would be
// wedged, since every tick re-derives the same state and the intent that stops
// a second spawn never commits). TestANameTheDatabaseRefusesIsRefusedHere is
// the other half of that guard.
//
// Mutation: drop Migrate from Load (the version stays 0 and the validator
// refuses the node) or write the state the caller held instead of the one
// Load returned (the node stays at v0 and the next daemon refuses it).
func TestLoadMigratesAnOlderSchemaVersion(t *testing.T) {
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	statePath := mustPath(t, orchestrator.PathState)

	v0 := document(t, readFixture(t))
	if _, present := v0["schema_version"]; !present {
		t.Fatal("the fixture no longer carries a schema version: this test would migrate nothing")
	}
	delete(v0, "schema_version")
	f.Set(statePath, v0)

	node := mustNode(t, &countingCAS{inner: rtdbClient(t, f)}, nil)
	s, etag, err := node.Load(ctx)
	if err != nil {
		t.Fatalf("loading the previous generation's node: %v", err)
	}
	if s.SchemaVersion != orchestrator.SchemaVersion {
		t.Fatalf("the v0 node loaded as schema version %d, not %d", s.SchemaVersion, orchestrator.SchemaVersion)
	}
	if s.Rev != 12 || len(s.WorkItems) != 2 {
		t.Errorf("the migration lost the node's own data: rev %d, %d work items", s.Rev, len(s.WorkItems))
	}
	if got := s.Facts["repo-primary"].Value; got != "owner/repo" {
		t.Errorf("the fixture's facts loaded as %q, so the document this test saves is not the one it read", got)
	}

	if err := node.Save(ctx, s, etag); err != nil {
		t.Fatalf("saving the raised state: %v", err)
	}
	stored := nodeObject(t, f.Value(statePath), "the state")
	if got := nodeInt(t, stored, "schema_version"); got != orchestrator.SchemaVersion {
		t.Errorf("the node was written back as schema version %d, not %d: the next daemon refuses it",
			got, orchestrator.SchemaVersion)
	}
	if got := nodeInt(t, stored, "rev"); got != 12 {
		t.Errorf("the migration moved the revision: %d", got)
	}
}

// TestANameTheDatabaseRefusesIsRefusedHere is the defect this task found and
// fixed: a fact key is a property name in the state document, and the Realtime
// Database refuses a dot in a key anywhere in the tree, so a name the model
// admitted, validated and encoded was then refused by the node — the
// orchestrator id wedged, because every tick derives the same state again, and
// the intent that would stop a second spawn never committed (D1231). The name
// rules now refuse it here (validate.go's idRE, the schema's patterns, the
// audit's subject), which is the only place the refusal can be a Go error a
// caller can act on rather than a 400 in the middle of a tick.
//
// The document is the real fixture, so the names it carries are the ones the
// migration test writes through the node: the two together say that a state
// this package accepts is one the database carries.
//
// Mutation: put the dot back in idRE (or drop the fact key's check in
// ValidateState) and the first case fails with the transport's own refusal —
// the state that validates and then cannot be written. The other three are
// refused by the same rule although their dot sits in a value rather than in a
// key, which is what keeps one rule for every name.
func TestANameTheDatabaseRefusesIsRefusedHere(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		corrupt func(t *testing.T, doc map[string]any)
	}{
		{"a fact key", func(_ *testing.T, doc map[string]any) {
			doc["facts"].(map[string]any)["repo.primary"] = map[string]any{"value": "owner/repo", "by": "person"}
		}},
		{"a work item's id", func(t *testing.T, doc map[string]any) {
			listAt(t, doc, "work_items")[0].(map[string]any)["id"] = "wi.1"
		}},
		{"a decision's id", func(t *testing.T, doc map[string]any) {
			listAt(t, doc, "decisions")[0].(map[string]any)["id"] = "ask.1"
		}},
		{"the orchestrator's id", func(_ *testing.T, doc map[string]any) {
			doc["agent_id"] = "orchestrator.a"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := gcpfake.NewRTDB(t)
			doc := document(t, readFixture(t))
			tc.corrupt(t, doc)
			f.Set(mustPath(t, orchestrator.PathState), doc)

			cas := &countingCAS{inner: rtdbClient(t, f)}
			node := mustNode(t, cas, nil)
			s, etag, err := node.Load(ctx)
			if errors.Is(err, orchestrator.ErrInvalid) {
				if cas.puts != 0 {
					t.Errorf("a document with a %s carrying a dot was written %d times after all", tc.name, cas.puts)
				}
				return
			}
			if err != nil {
				t.Fatalf("loading a %s carrying a dot: %v", tc.name, err)
			}
			// Not a name this package refuses, so the node has to carry it —
			// the defect's own shape: a state that loads, validates and is then
			// refused by the database at the write, on every tick, for ever.
			if err := node.Save(ctx, s, etag); err != nil {
				t.Fatalf("a %s carrying a dot passed the validator and the database refused the write: %v", tc.name, err)
			}
		})
	}
}

// storedRev is the revision the node carries, read back from the fake rather
// than from the value the test already holds.
func storedRev(t *testing.T, f *gcpfake.RTDB, node *orchestrator.Node) int64 {
	t.Helper()
	return nodeInt(t, nodeObject(t, f.Value(node.Path()), "the state"), "rev")
}

// stubLease is the fence as a test sets it: whether this writer holds the
// lease, and how many times that was asked.
type stubLease struct {
	held   bool
	err    error
	checks int
}

func (l *stubLease) Held(context.Context) (bool, error) {
	l.checks++
	return l.held, l.err
}

// flakyCAS refuses the first n conditional writes the way a moved ETag does
// and is the real store after that, so a save whose write lost a race can be
// watched as the refusal it is.
type flakyCAS struct {
	inner  orchestrator.CAS
	refuse int
	gets   int
	puts   int
}

func (c *flakyCAS) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	c.gets++
	return c.inner.GetETag(ctx, path, out)
}

func (c *flakyCAS) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	c.puts++
	if c.refuse > 0 {
		c.refuse--
		return fmt.Errorf("the ETag moved: %w", rtdb.ErrPrecondition)
	}
	return c.inner.PutIfMatch(ctx, path, etag, v)
}

func rtdbClient(t *testing.T, f *gcpfake.RTDB) *rtdb.Client {
	t.Helper()
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	return c
}

func mustNode(t *testing.T, db orchestrator.CAS, lease orchestrator.LeaseGuard) *orchestrator.Node {
	t.Helper()
	node, err := orchestrator.NewNode(orchestrator.NodeOptions{DB: db, AgentID: daemonID, Role: orchestrator.RoleOrchestrator, Lease: lease})
	if err != nil {
		t.Fatalf("building the state node: %v", err)
	}
	return node
}

func mustAudit(t *testing.T, db orchestrator.PatchDB, now orchestrator.ServerNow) *orchestrator.Audit {
	t.Helper()
	audit, err := orchestrator.NewAudit(db, daemonID, now)
	if err != nil {
		t.Fatalf("building the audit log: %v", err)
	}
	return audit
}

// nodeObject is a node read back from the fake as the map the database really
// carries, so a test asserts on what was written rather than on what it meant
// to write.
func nodeObject(t *testing.T, v any, what string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s is %#v, not an object", what, v)
	}
	return m
}

// nodeInt is a number read back from the fake, which carries JSON numbers as
// json.Number: a plain float64 assertion here would pass on a value the
// database never held and fail on the one it did.
func nodeInt(t *testing.T, m map[string]any, key string) int64 {
	t.Helper()
	switch v := m[key].(type) {
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			t.Fatalf("the node carries %q at %q, not an integer", v, key)
		}
		return n
	case float64:
		return int64(v)
	default:
		t.Fatalf("the node carries %#v at %q, not a number", m[key], key)
		return 0
	}
}

func nodeText(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	s, ok := m[key].(string)
	if !ok {
		t.Fatalf("the node carries %#v at %q, not a string", m[key], key)
	}
	return s
}
