package orchestrator_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/orchestrator"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The named orchestrator at this package's own doors (CG1a of
// docs/console-fixes): the id rule C-ORCH fixes (D3207, naming.go) is the one
// every path, listing and state document is built through, so `<project>~<name>`
// is a node this package addresses, lists and decodes — and an id the rule
// refuses is refused at all three, rather than escaped into a node nobody
// grants. A work item's id is deliberately not this rule (F36).
//
// The driver node beside the claim is the second half: the lease holder writes
// who it is to `orchestrator/<id>/driver` (D3218, F18), and neither the state
// nor the lease document gains a field to carry it (F35) — `project` and `name`
// are derived from the id, and the driver is a node of its own.
//
// Everything runs on internal/gcpfake's Realtime Database fake: no live cloud,
// no real clock, no key and no secret.

// The fixture's named orchestrator, and the two principals that drive it. An
// address is what a driver node names — a person's account or a service
// account's — and the two are distinct here so a test can tell a node a
// takeover rewrote from one it did not.
const (
	namedID     = "fugaro~night"
	namedDriver = "night-shift@fugaro.iam.gserviceaccount.com"
	nextDriver  = "day-shift@fugaro.iam.gserviceaccount.com"
)

// TestNamedIDAccepted: a named orchestrator's id is an id this package builds
// its paths from and writes a state document under. The red step is the rule
// itself: with the id held to the work item's own idRE, `fugaro~night` carries a
// `~` it does not admit and every path below is refused.
func TestNamedIDAccepted(t *testing.T) {
	// The default orchestrator, a named one, one whose slug carries a hyphen and
	// a digit, and the longest id the rule admits.
	for _, id := range []string{"fugaro", namedID, "my-project~night-shift-2", longProject + "~" + longName} {
		t.Run(id, func(t *testing.T) {
			node, err := orchestrator.PathNode(id)
			if err != nil {
				t.Fatalf("the store refuses %q as an orchestrator id: %v", id, err)
			}
			// An id is a key as written: none of its characters is one the
			// database escapes (budget.Key), so the node a writer names and the
			// node the rules grant are the same string (C-ORCH).
			if want := orchestrator.SubtreeRoot + "/" + id; node != want {
				t.Errorf("the node of %q is %q, want %q", id, node, want)
			}
			for _, sibling := range []struct {
				what string
				path func(string) (string, error)
			}{
				{"state", orchestrator.PathState},
				{"lease", orchestrator.PathLease},
				{"audit", orchestrator.PathAudit},
				{"driver", orchestrator.PathDriver},
			} {
				path, err := sibling.path(id)
				if err != nil {
					t.Errorf("the %s node of %q: %v", sibling.what, id, err)
					continue
				}
				if !strings.HasPrefix(path, node+"/") {
					t.Errorf("the %s node of %q is %q, and it is no child of %q", sibling.what, id, path, node)
				}
			}

			// The state document is the third door: DecodeState validates the id
			// it carries, so a named orchestrator's own node is refused until
			// this check moves with the paths (validate.go).
			s := stateWithAQueuedItem(t, leaseAt)
			s.AgentID = id
			raw, err := orchestrator.EncodeState(s)
			if err != nil {
				t.Fatalf("a state of %q is refused: %v", id, err)
			}
			back, err := orchestrator.DecodeState(raw)
			if err != nil {
				t.Fatalf("the state of %q does not read back: %v", id, err)
			}
			if back.AgentID != id {
				t.Errorf("the state read back names %q, not %q", back.AgentID, id)
			}
		})
	}
}

// TestStoreRefusesBadName: the id rule refuses what is not an id, at the paths
// and in the state document alike, so a name no project could carry is refused
// where it is written rather than escaped into a node nothing grants. The claim
// key is refused here too: no orchestrator can be named it (D3209), and the
// listing's own skip (TestNodeIDsSkipsClaims) is what keeps it out of a list.
func TestStoreRefusesBadName(t *testing.T) {
	for _, id := range []string{
		"",
		"Fugaro",
		"-fugaro",
		"fugaro-",
		"fugaro_night",
		"fugaro~Night",
		"fugaro~",
		"~night",
		"fugaro~night~x",
		"fugaro~night_shift",
		"fugaro night",
		"fugaro/night",
		strings.Repeat("n", 41),
		longProject + "~" + longName + "n",
		orchestrator.ClaimsKey,
	} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			if _, err := orchestrator.PathState(id); !errors.Is(err, orchestrator.ErrInvalid) {
				t.Errorf("the store builds the state path of %q, and the id rule refuses it: %v", id, err)
			}
			s := stateWithAQueuedItem(t, leaseAt)
			s.AgentID = id
			if _, err := orchestrator.EncodeState(s); !errors.Is(err, orchestrator.ErrInvalid) {
				t.Errorf("a state naming %q is written, and the id rule refuses it: %v", id, err)
			}
		})
	}
}

// TestNodeIDsSkipsClaims: the ids under `orchestrator/` are the orchestrators,
// and the claims node is the one child of the subtree that is not one and is
// meant to be there (D3209). It is skipped by name, and a key the id rule
// refuses is skipped by the rule — a node no reader of this release could have
// written and none can open.
func TestNodeIDsSkipsClaims(t *testing.T) {
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	for _, id := range []string{"fugaro", namedID, "my-project"} {
		path, err := orchestrator.PathState(id)
		if err != nil {
			t.Fatal(err)
		}
		f.Set(path, json.RawMessage(`{"schema_version":1}`))
	}
	// A real claim, at the path ClaimPath builds: the fixture cannot drift from
	// the one writer of that subtree.
	claim, err := orchestrator.ClaimPath(orchestrator.ItemRef{Orchestrator: namedID, Item: "docs.plan/T-1"})
	if err != nil {
		t.Fatalf("building a claim's path: %v", err)
	}
	f.Set(claim, map[string]any{"holder": winnerHolder})
	f.Set(orchestrator.SubtreeRoot+"/fugaro_night", json.RawMessage(`{"schema_version":1}`))

	ids, err := orchestrator.NodeIDs(ctx, rtdbClient(t, f))
	if err != nil {
		t.Fatalf("listing the ids: %v", err)
	}
	want := []string{"fugaro", namedID, "my-project"}
	if !slices.Equal(ids, want) {
		t.Errorf("NodeIDs listed %q, want %q: the claims node is no orchestrator and neither is a key the rule refuses", ids, want)
	}
}

// TestLeaseWritesDriverSibling: the daemon whose claim lands writes who it is
// to the sibling node `orchestrator/<id>/driver` (D3218, F18), so a reader can
// say which credential drives an id without reading the lease — whose holder is
// a daemon's run id and not a person. A takeover rewrites the one the daemon
// before left, and a takeover by a daemon that cannot say who it is removes it:
// the node beside a claim says who drives the id now, so the previous holder's
// address must not stand there once the lease is somebody else's. A daemon with
// no principal and no node before it writes nothing at all.
func TestLeaseWritesDriverSibling(t *testing.T) {
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	client := rtdbClient(t, f)

	lease := mustNamedLease(t, client, namedID, winnerHolder, namedDriver, clockAt(leaseAt))
	state, err := lease.Acquire(ctx)
	if err != nil {
		t.Fatalf("taking the lease of %q: %v", namedID, err)
	}
	if !state.Held {
		t.Fatalf("the daemon does not hold the lease it just took: %+v", state)
	}
	driverPath, err := orchestrator.PathDriver(namedID)
	if err != nil {
		t.Fatal(err)
	}
	d, err := orchestrator.DecodeDriver(readNode(t, ctx, client, driverPath))
	if err != nil {
		t.Fatalf("the driver node of %q does not read back: %v", namedID, err)
	}
	if d.Principal != namedDriver {
		t.Errorf("the driver node names %q, want %q", d.Principal, namedDriver)
	}
	if !d.At.Equal(leaseAt) {
		t.Errorf("the driver node is stamped %s, want the server time of the claim (%s)", d.At, leaseAt)
	}

	// A takeover: the next daemon to hold the id writes its own principal, so
	// the node says who drives the id now and not who drove it first.
	taken, err := mustNamedLease(t, client, namedID, loserHolder, nextDriver, clockAt(leaseAt.Add(time.Hour))).Acquire(ctx)
	if err != nil {
		t.Fatalf("taking the lease over: %v", err)
	}
	if !taken.Held || taken.Holder != loserHolder {
		t.Fatalf("the second daemon holds %+v, not the lease it took over", taken)
	}
	d, err = orchestrator.DecodeDriver(readNode(t, ctx, client, driverPath))
	if err != nil {
		t.Fatalf("the driver node after the takeover does not read back: %v", err)
	}
	if d.Principal != nextDriver || !d.At.Equal(leaseAt.Add(time.Hour)) {
		t.Errorf("after the takeover the driver node is %+v, want %q at %s", d, nextDriver, leaseAt.Add(time.Hour))
	}

	// The takeover by a daemon that cannot say who it is: the node the daemon
	// before left is removed, because it names a credential that no longer
	// holds this lease — a reader that kept it would draw a driver of an
	// orchestrator somebody else is driving (D3218, C-ORCH's readings).
	if _, err := mustNamedLease(t, client, namedID, winnerHolder, "", clockAt(leaseAt.Add(2*time.Hour))).Acquire(ctx); err != nil {
		t.Fatalf("taking the lease over with no driver to name: %v", err)
	}
	if _, found, err := client.GetETag(ctx, driverPath, nil); err != nil {
		t.Fatalf("reading the driver node after the principal-less takeover: %v", err)
	} else if found {
		t.Error("the driver node the previous holder wrote is still there, and the daemon that took the lease over named no principal")
	}

	// A daemon that cannot say who it is writes no driver node rather than one
	// naming nobody: an absent driver is read as unknown, a node naming nobody
	// would be read as a driver of nobody (C-ORCH's readings).
	plain := gcpfake.NewRTDB(t)
	plainClient := rtdbClient(t, plain)
	if _, err := mustNamedLease(t, plainClient, namedID, winnerHolder, "", clockAt(leaseAt)).Acquire(ctx); err != nil {
		t.Fatalf("taking the lease with no driver to name: %v", err)
	}
	path, err := orchestrator.PathDriver(namedID)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := plainClient.GetETag(ctx, path, nil); err != nil {
		t.Fatalf("reading the driver node: %v", err)
	} else if found {
		t.Error("a daemon with no principal to name wrote a driver node")
	}
}

// driverRefusingCAS refuses the conditional write to one path the way a moved
// ETag does and lets every other read and write through, so a claim that landed
// with a driver node that did not is the case under test rather than a claim
// that never landed at all.
type driverRefusingCAS struct {
	inner orchestrator.CAS
	path  string
}

func (c *driverRefusingCAS) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	return c.inner.GetETag(ctx, path, out)
}

func (c *driverRefusingCAS) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	if path == c.path {
		return fmt.Errorf("the driver node's ETag moved: %w", rtdb.ErrPrecondition)
	}
	return c.inner.PutIfMatch(ctx, path, etag, v)
}

// TestAFailedDriverWriteTakesTheClaimBack: a claim that lands and a driver node
// that does not is a daemon told its Acquire failed, so the claim must not stand
// — the lease node is freed and the id is the next daemon's at once, instead of
// being held until the TTL runs out by a daemon that stopped on the error and
// never beats (D3218, O3).
//
// The failure the caller sees is the driver write's own: the lease write landed,
// and reporting a claim the daemon does not hold would be the reading
// LeaseState's Held exists to refuse.
func TestAFailedDriverWriteTakesTheClaimBack(t *testing.T) {
	ctx := context.Background()
	f := gcpfake.NewRTDB(t)
	client := rtdbClient(t, f)
	driverPath, err := orchestrator.PathDriver(namedID)
	if err != nil {
		t.Fatal(err)
	}
	leasePath, err := orchestrator.PathLease(namedID)
	if err != nil {
		t.Fatal(err)
	}

	db := &driverRefusingCAS{inner: client, path: driverPath}
	lease := mustNamedLease(t, db, namedID, winnerHolder, namedDriver, clockAt(leaseAt))
	if state, err := lease.Acquire(ctx); err == nil {
		t.Fatalf("the claim whose driver write failed is reported as %+v, want the failure", state)
	} else if !strings.Contains(err.Error(), "recording who drives") {
		t.Errorf("the failure is %q, want the driver write's own", err)
	}

	var raw json.RawMessage
	found, err := client.Get(ctx, leasePath, &raw)
	if err != nil {
		t.Fatalf("reading the lease node: %v", err)
	}
	if found {
		t.Errorf("the lease node is %s, want no node at all: a failed Acquire leaves the id free", raw)
	}

	// And the id is free rather than merely unheld: the next daemon takes it on
	// the first attempt, with nothing to wait out.
	taken, err := mustNamedLease(t, client, namedID, loserHolder, nextDriver, clockAt(leaseAt)).Acquire(ctx)
	if err != nil {
		t.Fatalf("taking the lease the failed claim left behind: %v", err)
	}
	if !taken.Held || taken.Holder != loserHolder {
		t.Errorf("the next daemon holds %+v, want the lease the failed claim freed", taken)
	}
}

// TestStateAndLeaseUnchanged: neither the state nor the lease document gains a
// field for the named id or the driver (F35, D3218 as amended). The id's two
// halves are derived from the id itself, and the driver is a sibling node, so
// the documents this release writes are the documents the release before it
// reads — which is what keeps an update safe with runs in flight.
func TestStateAndLeaseUnchanged(t *testing.T) {
	ctx := context.Background()

	s := stateWithAQueuedItem(t, leaseAt)
	s.AgentID = namedID
	raw, err := orchestrator.EncodeState(s)
	if err != nil {
		t.Fatalf("a state of %q is refused: %v", namedID, err)
	}
	assertNoField(t, "the state document", raw, "project", "name", "driver")

	// The lease node the holder writes carries the two fields it always had, and
	// the driver is written beside it and not into it.
	f := gcpfake.NewRTDB(t)
	client := rtdbClient(t, f)
	if _, err := mustNamedLease(t, client, namedID, winnerHolder, namedDriver, clockAt(leaseAt)).Acquire(ctx); err != nil {
		t.Fatalf("taking the lease of %q: %v", namedID, err)
	}
	leasePath, err := orchestrator.PathLease(namedID)
	if err != nil {
		t.Fatal(err)
	}
	assertNoField(t, "the lease document", readNode(t, ctx, client, leasePath), "project", "name", "driver")
	driverPath, err := orchestrator.PathDriver(namedID)
	if err != nil {
		t.Fatal(err)
	}

	// And both documents refuse a field this release does not model: a document
	// an update wrote one into would be refused by the release behind rather
	// than read as one of its own (D3218's whole point).
	if _, err := orchestrator.DecodeState(withField(t, raw, "driver", namedDriver)); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Errorf("a state carrying a driver field decodes, and it must be refused: %v", err)
	}
	if _, err := orchestrator.DecodeState(withField(t, raw, "project", "fugaro")); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Errorf("a state carrying a project field decodes, and it must be refused: %v", err)
	}
	if _, err := orchestrator.DecodeDriver(withField(t, readNode(t, ctx, client, driverPath), "name", "night")); !errors.Is(err, orchestrator.ErrInvalid) {
		t.Errorf("a driver node carrying a name field decodes, and it must be refused: %v", err)
	}
}

// mustNamedLease builds one daemon's claim on one orchestrator id, driver and
// all: the fixture every test here shares, so the id and the principal are
// never a string a test wrote twice.
func mustNamedLease(t *testing.T, db orchestrator.CAS, id, holder, driver string, now orchestrator.ServerNow) *orchestrator.Lease {
	t.Helper()
	lease, err := orchestrator.NewLease(db, id, holder, driver, now)
	if err != nil {
		t.Fatalf("building the lease of %q for %q: %v", id, holder, err)
	}
	return lease
}

// readNode is one node of the fake as its bytes: what a reader outside this
// package would see at the path, rather than the package's own reader.
func readNode(t *testing.T, ctx context.Context, db *rtdb.Client, path string) []byte {
	t.Helper()
	var raw json.RawMessage
	found, err := db.Get(ctx, path, &raw)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !found {
		t.Fatalf("%s is not there", path)
	}
	return raw
}

// assertNoField refuses a document carrying one of the fields the plan's D3218
// says is derived rather than stored.
func assertNoField(t *testing.T, what string, raw []byte, fields ...string) {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s does not parse: %v", what, err)
	}
	for _, field := range fields {
		if _, ok := doc[field]; ok {
			t.Errorf("%s carries a %q field, and the id's halves are derived from the id and the driver is a sibling node (D3218, F35)", what, field)
		}
	}
}

// withField adds one field to a document, the way a release that stored what
// this one derives would have written it.
func withField(t *testing.T, raw []byte, field, value string) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the document does not parse: %v", err)
	}
	doc[field] = value
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("writing the document back: %v", err)
	}
	return out
}
