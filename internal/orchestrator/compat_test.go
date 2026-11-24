package orchestrator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
)

// compat_test.go is CG2 of docs/console-fixes: `fugaro update` needs no
// orchestrator migration, and this is what proves it (D3218 as amended, F35,
// F36).
//
// The state and the lease are decoded strictly — Node.Load refuses an unknown
// field and the lease reads through decodeStrict — so a field this release adds
// to either is a document the release behind refuses. That release is not
// hypothetical: a resident daemon whose image the update has not rebuilt yet
// runs it, and so does a member of the project who has not upgraded, and either
// would then refuse its own node (F35). D3218 as first written added `project`,
// `name` and `driver` to the state; as amended it adds none of them — the first
// two are derived from the id, and the third is a node of its own beside the
// state and the lease — so `update` has nothing to migrate, and the named
// orchestrators this plan adds are invisible to a member still on the release
// behind rather than unreadable to it (F36).
//
// What this test holds every node this release writes to is the release
// behind's own decoders, frozen in testdata/prev-release/ the way this
// repository models such a reader elsewhere
// (internal/projectpolicy/stage_accounts_test.go: the same rule, met with that
// release's vocabulary). The state's is the closed shape that release's decoder
// applies — schemas/orchestrator-state.schema.json as it stood, whose
// `additionalProperties` is false everywhere — and the lease's is the two
// fields its leaseDoc carries, read with DisallowUnknownFields.

// The ids, the holder and the server time every node below is written with.
// compatProject is a project's name, which is also its default orchestrator's
// id; compatNamedID is one of its named ones, which is the spelling this
// release adds and the id the release behind does not know.
const (
	compatProject   = "aurora"
	compatNamedID   = compatProject + orchestrator.NameSeparator + "night"
	compatHolder    = "20261122-080654-abcd"
	compatPrincipal = "owner@example.invalid"
)

var compatNow = time.Date(2026, 11, 22, 8, 6, 54, 0, time.UTC)

// The release behind's decoders, frozen.
const (
	prevStateSchema = "testdata/prev-release/state.schema.json"
	prevLeasePath   = "testdata/prev-release/lease.json"
	prevAddedField  = "testdata/prev-release/state-added-field.json"
)

// TestPreviousReleaseReadsNewNodes decodes, with the release behind's strict
// decoders, every node this release writes: the state, the lease, the driver
// node and the standing backlog's, for the default orchestrator and for a named
// one.
//
// Mutation: add a field to the state or to the lease and the release behind
// refuses the document — the state's because the frozen shape closes every
// object it knows, the lease's because the frozen decoder carries the two fields
// its own node holds and DisallowUnknownFields. `project`, `name` and `driver`
// on the state are the three D3218 as first written would have added, and the
// last subtest tries all three; a driver moved into the state or the lease
// instead of the sibling node lands in the same two refusals.
func TestPreviousReleaseReadsNewNodes(t *testing.T) {
	prevState := compileSchema(t, prevStateSchema)
	prevReleaseLease(t)

	db := gcpfake.NewRTDB(t)

	// The default orchestrator, every node by this release's own writer: the
	// state's encoder, the daemon's claim, the driver node the holder writes
	// beside them, and the standing backlog's node — a State through and
	// through (D2735), so the same encoder's document at the sibling path. The
	// backlog node is the one of the four `fugaro update` itself writes,
	// folding a checkout's docs/backlog/ folder into it (D2716): an update over
	// a checkout holding an open item writes it, which is what makes it the
	// node a release behind most plausibly meets.
	source := stateOf(t, readFixture(t))
	defaultState := writeStateNode(t, db, source, compatProject, orchestrator.StateNode)
	defaultBacklog := writeStateNode(t, db, source, compatProject, orchestrator.BacklogNode)
	writeLeaseNode(t, db, compatProject)
	writeDriverNode(t, db, compatProject)

	// The encoder's own bytes are the validator's, for an id the id rule
	// admits: what EncodeState returns is json.Marshal(s.Clone()) with the
	// validator in front of it, so the named node below — which skips the
	// validator, whose id rule this release widens in CG1a and not here — is
	// the document the writer emits.
	source.AgentID = compatProject
	if enc, err := orchestrator.EncodeState(source); err != nil {
		t.Fatalf("this release refuses the state it writes for its own project's orchestrator: %v", err)
	} else if !bytes.Equal(enc, defaultState) {
		t.Errorf("the validator's bytes are not the encoder's for %q, so the named node below is not the document this release writes", compatProject)
	}

	// A named one: the same four documents at its own paths. This release
	// refuses a named id until CG1a widens the id rule (F36) — PathState,
	// ValidateState and NewLease each check the old one — so the paths are
	// built from the subtree's own names and the lease and the driver are the
	// default's own documents. The id is in the path and in no document (F35),
	// and the state and the backlog node are the two documents that differ, in
	// `agent_id` alone.
	namedState := writeStateNode(t, db, source, compatNamedID, orchestrator.StateNode)
	namedBacklog := writeStateNode(t, db, source, compatNamedID, orchestrator.BacklogNode)
	db.Set(nodePath(t, compatNamedID, orchestrator.LeaseNode), json.RawMessage(storedNode(t, db, nodePath(t, compatProject, orchestrator.LeaseNode))))
	writeDriverNode(t, db, compatNamedID)

	// The paths the test builds are the paths this release builds. The default
	// id is the one both releases' rules admit, so the two are compared there;
	// a named id's builders refuse it until CG1a lands.
	for _, tc := range []struct {
		node string
		path func(string) (string, error)
	}{
		{orchestrator.StateNode, orchestrator.PathState},
		{orchestrator.LeaseNode, orchestrator.PathLease},
		{orchestrator.DriverNode, orchestrator.PathDriver},
		{orchestrator.BacklogNode, orchestrator.PathBacklog},
	} {
		want, err := tc.path(compatProject)
		if err != nil {
			t.Fatal(err)
		}
		if got := nodePath(t, compatProject, tc.node); got != want {
			t.Errorf("this test builds %q for %s's node and this release builds %q", got, tc.node, want)
		}
	}

	t.Run("the default orchestrator", func(t *testing.T) {
		// The release behind's strict decoder for the state reads the document
		// this release writes.
		if err := prevState.Validate(jsonValue(t, defaultState)); err != nil {
			t.Errorf("the release behind refuses the state this release writes for %q: %v", compatProject, err)
		}
		// And the same decoder reads the backlog node, which is the same
		// document at the sibling path and the one an ordinary `fugaro update`
		// writes.
		if err := prevState.Validate(jsonValue(t, defaultBacklog)); err != nil {
			t.Errorf("the release behind refuses the backlog node this release writes for %q: %v", compatProject, err)
		}
		// And the whole reader, this release's own: the id is one its rule
		// admits, so nothing in this reading is the id's doing.
		s, err := orchestrator.ReadState(context.Background(), rtdbClient(t, db), compatProject, orchestrator.RoleOrchestrator)
		if err != nil {
			t.Fatalf("reading the state node of %q: %v", compatProject, err)
		}
		if s.AgentID != compatProject || s.Rev != source.Rev {
			t.Errorf("the state node reads as agent_id=%q rev=%d, want the document this release wrote", s.AgentID, s.Rev)
		}
		readLeaseAndDriver(t, db, compatProject)
	})

	t.Run("a named orchestrator", func(t *testing.T) {
		// The release behind refuses the id and nothing else. Its rule has no
		// separator, so a member still on it never lists a named orchestrator
		// at all (NodeIDs skips the key, F36) and never reaches this document;
		// what it may not refuse is the shape, which is why the assertion is
		// made with the id taken out. The backlog node is read the same way and
		// for the same reason: the id is F36's skew, and a field carrying a
		// name is the one thing D3218 as amended forbids.
		if err := prevState.Validate(jsonValue(t, namedState)); err == nil {
			t.Errorf("the release behind read a state document naming %q: its id rule has no separator, and the id is the one thing this release must not have widened", compatNamedID)
		}
		shaped := bytes.Replace(namedState, []byte(compatNamedID), []byte(compatProject), 1)
		if err := prevState.Validate(jsonValue(t, shaped)); err != nil {
			t.Errorf("the release behind refuses the shape of a named orchestrator's state: the id is F36's skew, and a field carrying a name is the one thing D3218 as amended forbids: %v", err)
		}
		if err := prevState.Validate(jsonValue(t, namedBacklog)); err == nil {
			t.Errorf("the release behind read a backlog document naming %q", compatNamedID)
		}
		shapedBacklog := bytes.Replace(namedBacklog, []byte(compatNamedID), []byte(compatProject), 1)
		if err := prevState.Validate(jsonValue(t, shapedBacklog)); err != nil {
			t.Errorf("the release behind refuses the shape of a named orchestrator's backlog node, which `fugaro update` writes for the default id and `fugaro backlog add --name` will for a named one: %v", err)
		}
		// F35: the two documents are one document, differing in the id alone.
		// `project` and `name` are derived from the id and are in neither.
		sameShapeButTheID(t, defaultState, namedState)
		sameShapeButTheID(t, defaultBacklog, namedBacklog)
		readLeaseAndDriver(t, db, compatNamedID)
	})

	// The red step, and what makes the assertions above able to fail: one field
	// added to a node the release behind reads is a document it refuses. The
	// fixture is the state shape with `project` in it, so the document without
	// any added field is that fixture with the key taken back out, and each of
	// the three names is tried on top of it — a refusal that came from the
	// document rather than from the field would show up as the clean one failing
	// first. A decoder that read anything would pass every case above.
	t.Run("the added fields are refused", func(t *testing.T) {
		raw, err := os.ReadFile(prevAddedField)
		if err != nil {
			t.Fatal(err)
		}
		base := objectOf(t, raw)
		delete(base, "project")
		if err := prevState.Validate(jsonValue(t, marshalDoc(t, base))); err != nil {
			t.Errorf("the release behind refuses %s with its one added field taken back out, so the refusals below are the field's doing and not the fixture's: %v", prevAddedField, err)
		}
		for _, field := range []string{"project", "name", "driver"} {
			doc := map[string]any{}
			for k, v := range base {
				doc[k] = v
			}
			doc[field] = compatProject
			if err := prevState.Validate(jsonValue(t, marshalDoc(t, doc))); err == nil {
				t.Errorf("the release behind read a state carrying a `%s` field, which no release writes: D3218 as amended derives it from the id, or moves it to a node of its own, rather than storing it", field)
			}
		}

		// And the lease, on the release behind's own node: a third field beside
		// the holder and the time is one its frozen decoder refuses.
		leaseRaw, err := os.ReadFile(prevLeasePath)
		if err != nil {
			t.Fatal(err)
		}
		lease := objectOf(t, leaseRaw)
		lease["driver"] = compatPrincipal
		if _, err := decodePrevLease(marshalDoc(t, lease)); err == nil {
			t.Errorf("the release behind read the lease at %s with a `driver` field added, which is the field D3218 as first written would have put beside the holder", prevLeasePath)
		}
	})
}

// prevLeaseDoc is the lease document as the release behind decodes it — this
// package's own leaseDoc — frozen here with the two fields it carries and
// DisallowUnknownFields, so a lease this release adds a field to is a document
// the release behind refuses. testdata/prev-release/lease.json is that
// release's own node, and it is what keeps this shape honest.
type prevLeaseDoc struct {
	Holder string    `json:"holder"`
	At     time.Time `json:"at"`
}

// prevReleaseLease reads the frozen lease fixture with the frozen decoder,
// which is the assertion that the fixture is the document the release behind
// writes: a third field in it would be refused here.
func prevReleaseLease(t *testing.T) prevLeaseDoc {
	t.Helper()
	raw, err := os.ReadFile(prevLeasePath)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := decodePrevLease(raw)
	if err != nil {
		t.Errorf("the release behind refuses the lease document at %s: %v", prevLeasePath, err)
	}
	if doc.Holder == "" || doc.At.IsZero() {
		t.Errorf("the lease fixture at %s names no holder or no time: %+v", prevLeasePath, doc)
	}
	return doc
}

func decodePrevLease(raw []byte) (prevLeaseDoc, error) {
	var doc prevLeaseDoc
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(&doc)
	return doc, err
}

// writeStateNode writes one state-shaped document of this release's encoder for
// id — the state node's or the standing backlog's, which are the same document
// (D2735) — and hands back the bytes.
//
// EncodeState is the validator and json.Marshal(s.Clone()); the validator adds
// no field and drops none, which the caller pins against EncodeState itself for
// the default id, so the document is the encoder's for a named id too — whose
// id the validator refuses until CG1a widens the rule (F36).
func writeStateNode(t *testing.T, db *gcpfake.RTDB, s orchestrator.State, id, node string) []byte {
	t.Helper()
	s.AgentID = id
	raw, err := json.Marshal(s.Clone())
	if err != nil {
		t.Fatal(err)
	}
	db.Set(nodePath(t, id, node), json.RawMessage(raw))
	return raw
}

// writeLeaseNode takes the lease of one orchestrator id through this release's
// own writer — the daemon's ETag-conditioned claim (Lease.Acquire) — so the
// node is the document `orchestrator serve` writes rather than one the test
// built.
func writeLeaseNode(t *testing.T, db *gcpfake.RTDB, id string) {
	t.Helper()
	lease, err := orchestrator.NewLease(rtdbClient(t, db), id, compatHolder, "", func() (time.Time, bool) { return compatNow, true })
	if err != nil {
		t.Fatalf("building the lease of %q: %v", id, err)
	}
	if _, err := lease.Acquire(context.Background()); err != nil {
		t.Fatalf("taking the lease of %q: %v", id, err)
	}
}

// writeDriverNode writes the driver node beside the state and the lease, as the
// holder of the lease writes it (D3218 as amended, F18): the credential's
// principal and the server time it wrote.
func writeDriverNode(t *testing.T, db *gcpfake.RTDB, id string) {
	t.Helper()
	raw, err := json.Marshal(orchestrator.Driver{Principal: compatPrincipal, At: compatNow})
	if err != nil {
		t.Fatal(err)
	}
	db.Set(nodePath(t, id, orchestrator.DriverNode), json.RawMessage(raw))
}

// readLeaseAndDriver reads the two nodes beside the state of one orchestrator
// id: the lease with the release behind's frozen decoder, and the driver node
// with this release's own reader.
func readLeaseAndDriver(t *testing.T, db *gcpfake.RTDB, id string) {
	t.Helper()
	raw := storedNode(t, db, nodePath(t, id, orchestrator.LeaseNode))
	got, err := decodePrevLease(raw)
	if err != nil {
		t.Errorf("the release behind refuses the lease this release writes for %q: %v", id, err)
	}
	if got.Holder != compatHolder || !got.At.Equal(compatNow) {
		t.Errorf("the release behind reads the lease of %q as %+v, want the holder and the server time this release wrote", id, got)
	}

	driver := storedNode(t, db, nodePath(t, id, orchestrator.DriverNode))
	d, err := orchestrator.DecodeDriver(driver)
	if err != nil {
		t.Errorf("this release refuses the driver node it writes for %q: %v", id, err)
	} else if d.Principal != compatPrincipal || !d.At.Equal(compatNow) {
		t.Errorf("the driver node of %q reads as %+v, want the principal and the time this release wrote", id, d)
	}

	// The driver is a node of its own and not a field, which is the whole of
	// what keeps it out of every strict decoder of either release: neither the
	// state nor the lease carries its own names.
	for _, doc := range [][]byte{storedNode(t, db, nodePath(t, id, orchestrator.StateNode)), raw} {
		obj := objectOf(t, doc)
		if _, ok := obj["driver"]; ok {
			t.Errorf("a document of %q carries a `driver` field, so a decoder of the release behind meets the node D3218 moved out of it", id)
		}
		if _, ok := obj["principal"]; ok {
			t.Errorf("a document of %q carries a `principal` field, so a decoder of the release behind meets the node D3218 moved out of it", id)
		}
	}
}

// sameShapeButTheID asserts that two documents of one node are one document
// with the id changed: the same keys, and the same value under every one of
// them but `agent_id`. It is F35's whole content — a name is derived from the
// id and is a field of no document — so a writer that carried one would fail
// here.
func sameShapeButTheID(t *testing.T, defaultDoc, namedDoc []byte) {
	t.Helper()
	a, b := objectOf(t, defaultDoc), objectOf(t, namedDoc)
	if a["agent_id"] != compatProject || b["agent_id"] != compatNamedID {
		t.Fatalf("the two documents name %v and %v, want %q and %q", a["agent_id"], b["agent_id"], compatProject, compatNamedID)
	}
	delete(a, "agent_id")
	delete(b, "agent_id")
	if !reflect.DeepEqual(a, b) {
		t.Errorf("the state this release writes for %q is not the one it writes for %q with the id changed: a field that differs between the two is a field a name is carried in", compatNamedID, compatProject)
	}
}

// nodePath is the path of one node of an orchestrator id: the subtree's own
// root, the id escaped with the tree's one escaper, and the node's name — which
// is what PathState, PathLease and PathDriver build. The builders refuse a
// named id until CG1a widens the id rule (F36), and the test pins above that
// the two spellings agree for the default id.
func nodePath(t *testing.T, id, node string) string {
	t.Helper()
	if id == "" || node == "" {
		t.Fatalf("a node's path needs an id and a node name, got %q and %q", id, node)
	}
	return orchestrator.SubtreeRoot + "/" + budget.Key(id) + "/" + node
}

// storedNode is one node as the database holds it: the fake's tree read back
// and marshalled, so the test decodes the bytes a reader would be handed rather
// than the value it meant to write.
func storedNode(t *testing.T, db *gcpfake.RTDB, path string) []byte {
	t.Helper()
	v := db.Value(path)
	if v == nil {
		t.Fatalf("no node at %s", path)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// objectOf is a document as the map the database carries, for the assertions
// about which names a document holds.
func objectOf(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("the document is not a JSON object: %v", err)
	}
	return obj
}

// marshalDoc is one document as bytes, for the mutation cases: the test builds
// them by editing a map, and every reader here is handed bytes rather than the
// value the test meant.
func marshalDoc(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// jsonValue is a document as the schema validator reads it, the way the schema
// tests in this package hand it one.
func jsonValue(t *testing.T, raw []byte) any {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the document is not JSON: %v", err)
	}
	return v
}

// stateOf reads a state document without this release's validator: the fixture
// the reader, the migration and the schemas are all checked against, whose id
// is replaced per node below.
func stateOf(t *testing.T, raw []byte) orchestrator.State {
	t.Helper()
	var s orchestrator.State
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("the state fixture does not parse: %v", err)
	}
	return s
}
