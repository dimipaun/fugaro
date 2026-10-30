package budget_test

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
)

// The pairing sweep (mobile plan task T4, decisions 1, 2 and 6): the sweeper's
// second family of Firebase users and its second family of nodes. Every test
// here is about what is *not* deleted as much as about what is: the device
// allowlist is what the rules read (D166), and a sweeper that deleted a device
// user whose node exists would unpair a working phone.
const (
	devUID   = "m~BBBBBBBBBBBBBBBBBBBBBB"
	devUID2  = "m~CCCCCCCCCCCCCCCCCCCCCC"
	pairUID  = "p~AAAAAAAAAAAAAAAAAAAAAAAAAA"
	pairUID2 = "p~DDDDDDDDDDDDDDDDDDDDDDDD"
)

// deviceNode writes the one field of a device node the sweep must not read for
// its decision: the node's existence is the whole of it (M13's fields are the
// command's, and no rule reads one).
func (e *sweepEnv) deviceNode(uid string) {
	e.t.Helper()
	e.db.Set("config/admin/devices/"+uid, map[string]any{
		"name": "phone-a", "email": "person-1@example.com", "role": "operator", "at": "2026-10-02T11:00:00Z",
	})
}

// pairingNode writes a pairing session's subtree as the CLI leaves it: the
// request the phone wrote and the delivery the CLI wrote, which is the payload
// the node's deletion is for.
func (e *sweepEnv) pairingNode(uid string) {
	e.t.Helper()
	e.db.Set("pairing/"+uid, map[string]any{
		"request":  map[string]any{"key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "at": sweepNow.UnixMilli()},
		"delivery": map[string]any{"v": 1, "sealed": "AAAA"},
	})
}

func (e *sweepEnv) user(uid string, age time.Duration) { e.idt.AddUser(uid, sweepNow.Add(-age)) }

func (e *sweepEnv) users() []string {
	got := e.idt.Users()
	sort.Strings(got)
	return got
}

// TestSweepDeletesOldPairingUserAndNode is decision 1 and decision 2 together:
// a pairing user past the session's own hour plus one sweep interval is
// deleted, and the pairing node it owns goes with it — that node is a payload
// the uid's ID token can still read, and nothing else puts a clock on it.
func TestSweepDeletesOldPairingUserAndNode(t *testing.T) {
	e := newSweepEnv(t)
	e.user(pairUID, 2*time.Hour)
	e.pairingNode(pairUID)
	rep := e.sweep()
	if got := e.users(); len(got) != 0 {
		t.Fatalf("users left = %v, want the pairing user deleted", got)
	}
	if e.has("pairing/" + pairUID) {
		t.Fatal("the pairing node survived the user it belongs to")
	}
	if rep.UsersDeleted != 1 || rep.PairingNodesDeleted != 1 {
		t.Fatalf("report = %+v, want one user and one node", rep)
	}
}

// TestSweepKeepsYoungPairingUser is the grace, and its boundary: the age is
// token.Lifetime plus one sweep interval, so a session still inside the ID
// token's hour is never raced by a sweep, and one exactly at the boundary is
// kept.
func TestSweepKeepsYoungPairingUser(t *testing.T) {
	e := newSweepEnv(t)
	e.user(pairUID, time.Minute)
	e.pairingNode(pairUID)
	e.user(pairUID2, budget.PairingUserMaxAge)
	e.pairingNode(pairUID2)
	rep := e.sweep()
	want := []string{pairUID, pairUID2}
	sort.Strings(want)
	if got := e.users(); !reflect.DeepEqual(got, want) {
		t.Fatalf("users left = %v, want %v", got, want)
	}
	if !e.has("pairing/"+pairUID) || !e.has("pairing/"+pairUID2) {
		t.Fatal("a kept pairing's node was deleted with it")
	}
	if rep.UsersDeleted != 0 || rep.PairingNodesDeleted != 0 {
		t.Fatalf("report = %+v, want nothing deleted", rep)
	}
}

// TestSweepKeepsDeviceUserWithNode is D166: the node is the allowlist the rules
// read, so a device user whose node exists is a paired phone however old its
// account is, and deleting it would be unpairing a phone nobody revoked.
func TestSweepKeepsDeviceUserWithNode(t *testing.T) {
	e := newSweepEnv(t)
	e.deviceNode(devUID)
	e.user(devUID, 30*24*time.Hour)
	rep := e.sweep()
	if got := e.users(); !reflect.DeepEqual(got, []string{devUID}) {
		t.Fatalf("users left = %v, want the paired device kept", got)
	}
	if !e.has("config/admin/devices/" + devUID) {
		t.Fatal("the device node was deleted")
	}
	if rep.UsersDeleted != 0 {
		t.Fatalf("report = %+v, want nothing deleted", rep)
	}
}

// TestSweepDeletesDeviceUserWithoutNode is the other half of that rule: the
// node's absence is what `fugaro pair revoke` leaves behind, and no role a
// Terraform grant gives an operator can delete an Auth user, so this pass is
// what removes the orphan.
func TestSweepDeletesDeviceUserWithoutNode(t *testing.T) {
	e := newSweepEnv(t)
	e.user(devUID, 30*24*time.Hour)
	rep := e.sweep()
	if got := e.users(); len(got) != 0 {
		t.Fatalf("users left = %v, want the orphaned device user deleted", got)
	}
	if rep.UsersDeleted != 1 {
		t.Fatalf("report = %+v, want one user", rep)
	}
}

// TestSweepTouchesNothingElse is the delete set: exactly the two prefixes and
// the pairing nodes of the users it deletes. Everything else in the database —
// a kept pairing, a paired device's node, the mailbox tree, the config — is
// read and never written, and a user of another shape is not this sweeper's.
func TestSweepTouchesNothingElse(t *testing.T) {
	e := newSweepEnv(t)
	e.deviceNode(devUID) // a paired device: its user stays
	e.user(devUID, time.Hour)
	e.user(devUID2, 2*time.Hour) // revoked: no node, so the user goes
	e.pairingNode(pairUID)       // an abandoned session: user and node go
	e.user(pairUID, 2*time.Hour)
	e.pairingNode(pairUID2) // a session still in flight: both stay
	e.user(pairUID2, time.Minute)
	e.user(budgetUID(runA), 72*time.Hour) // a run's, by the old rule
	e.idt.AddUser("someone-else", sweepNow.Add(-72*time.Hour))
	e.idt.AddUser("m", sweepNow.Add(-72*time.Hour)) // the prefix alone is not a device
	e.db.Set("mailbox/mb-1/prompts/p-1", map[string]any{"at": sweepNow.Add(-time.Hour).UnixMilli()})
	e.db.Set("config/caps/global/dailyMicros", 50_000_000)

	before := map[string]any{
		"mailbox": e.db.Value("mailbox"),
		"caps":    e.db.Value("config/caps"),
	}
	rep := e.sweep()

	want := []string{"m", pairUID2, devUID, "someone-else"}
	sort.Strings(want)
	if got := e.users(); !reflect.DeepEqual(got, want) {
		t.Fatalf("users left = %v, want %v", got, want)
	}
	if rep.UsersDeleted != 3 || rep.PairingNodesDeleted != 1 {
		t.Fatalf("report = %+v, want three users and one node", rep)
	}
	if e.has("pairing/" + pairUID) {
		t.Fatal("the abandoned pairing's node survived")
	}
	if !e.has("pairing/"+pairUID2) || !e.has("config/admin/devices/"+devUID) {
		t.Fatal("a kept pairing's node or a paired device's node was deleted")
	}
	if !reflect.DeepEqual(before["mailbox"], e.db.Value("mailbox")) || !reflect.DeepEqual(before["caps"], e.db.Value("config/caps")) {
		t.Fatal("the sweep wrote outside its own families")
	}
	for _, r := range e.db.Requests() {
		if r.Method != "PATCH" && r.Method != "PUT" {
			continue
		}
		if strings.Contains(string(r.Body), "mailbox") || strings.Contains(r.Path, "/config/caps") {
			t.Fatalf("the sweeper wrote %s %s", r.Method, r.Path)
		}
	}
}

// TestSweepPairingUserMaxAgeIsTheSessionPlusASweep pins decision 1's number to
// the two values it is a sum of: the pairing session's own hour, which the
// token package already holds, and one sweep interval, which is the history
// job's schedule.
func TestSweepPairingUserMaxAgeIsTheSessionPlusASweep(t *testing.T) {
	if budget.PairingUserMaxAge != token.Lifetime+budget.SweepEvery {
		t.Fatalf("PairingUserMaxAge = %s, want token.Lifetime (%s) plus one sweep interval (%s)",
			budget.PairingUserMaxAge, token.Lifetime, budget.SweepEvery)
	}
	if token.Lifetime != time.Hour {
		t.Fatalf("token.Lifetime = %s, want the hour a p~ ID token lives", token.Lifetime)
	}
}

// TestSweepPairingNodeFailureStopsTheUser is the ordering inside the pass: a
// pairing node that could not be deleted stops the p~ user of this pass rather
// than deleting a user whose payload node would outlive it. The next interval
// retries, and the node is still there for it to find.
func TestSweepPairingNodeFailureStopsTheUser(t *testing.T) {
	e := newSweepEnv(t)
	e.user(pairUID, 2*time.Hour)
	e.pairingNode(pairUID)
	e.db.Deny("pairing")
	if _, err := e.sw.Sweep(context.Background()); err == nil {
		t.Fatal("a refused node deletion was swallowed")
	}
	if got := e.users(); !reflect.DeepEqual(got, []string{pairUID}) {
		t.Fatalf("users left = %v, want the pairing user kept until its node goes", got)
	}
}
