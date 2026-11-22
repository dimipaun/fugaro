package cli

// The sender (mobile plan Task P5.2.6, SECURITY-SENSITIVE): `internal/cli/
// push.go`, driven through the real command tree (`budget history --sweep`),
// against the task's own verification list — one push per category per run per
// hour and no more, with the cap's planted control; a category the account's
// prefs turn off suppressed **before** the provider is called; quiet hours
// honoured against the stored IANA zone, including across a DST change; no
// producer writing anything, with the control a run that leaves no pending-push
// row anywhere; the ledger pruned to its window on the sweep; and a token the
// provider reports unregistered deleted, and an orphaned push node deleted
// rather than marked.
//
// The design of record is the push design P5.2 wrote, which push.go's own header
// names: §3.6 (B) (the sweep derives its own events), §3.7 (the dedupe key and
// the cap), §3.8 (the switches and quiet hours) and §6 (the failure modes). The
// provider is a fake, because what a test can pin is what the sender asks it to
// send — the FCM call itself is P5.2.7's grant and Check 88's owner run.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/mailbox"
)

// pushTestUID is one paired account: the same name is the mailbox, the child of
// the device node and the child of the push node (P5.2.5's pairing), because
// that identity is what makes the sibling node addressable at all.
const pushTestUID = "m~BBBBBBBBBBBBBBBBBBBBBB"

// pushTestNow is the instant every test's clock starts at unless it says
// otherwise: a winter afternoon in UTC, chosen so that the DST cases below can
// be read against it.
var pushTestNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

type pushFixture struct {
	*historyFixture
	sent    []pushRequest
	answer  pushOutcome
	sendErr error
	now     time.Time
}

// newPushFixture points the history command at a fake FCM provider and a clock
// the test controls, on top of the history fixture the sweep already has.
func newPushFixture(t *testing.T, now time.Time) *pushFixture {
	t.Helper()
	f := &pushFixture{historyFixture: newHistoryFixture(t), now: now.UTC(), answer: pushSent}
	historyTest.now = func() time.Time { return f.now }
	historyTest.pushSend = func(_ context.Context, req pushRequest) (pushOutcome, error) {
		f.sent = append(f.sent, req)
		return f.answer, f.sendErr
	}
	return f
}

// pair leaves the installation as P5.2.4 and P5.2.5 leave it: the device node
// that is the admission, and the sibling push node the registration principal
// wrote with the token, the platform and the prefs.
func (f *pushFixture) pair(t *testing.T, prefs map[string]any) {
	t.Helper()
	f.db.Set(rules.PathDevices+"/"+pushTestUID, map[string]any{
		"name": "seed", "email": "owner@example.com", "role": "owner", "at": "2026-11-17T00:00:00Z",
	})
	f.db.Set(pairPushPath(pushTestUID), map[string]any{
		"token": mailboxRegistrationToken, "platform": "ios", "updated_at": f.now.UnixMilli(), "prefs": prefs,
	})
}

// entry seeds one live registry row — fresh enough that the registry sweep keeps
// it, so the sender's own reading of it is what the test is about.
func (f *pushFixture) entry(run, halted string, spent int64) {
	f.db.Set("agents/"+appSlug+"/"+run, map[string]any{
		"repo": "acme/app", "workflow": "web", "requestedBy": "dimi@example.invalid",
		"startedAt": f.now.Add(-time.Minute).UnixMilli(), "updatedAt": f.now.Add(-30 * time.Second).UnixMilli(),
		"halted": halted, "spent": spent,
	})
}

// stoppedByCap leaves the installation as a run the budget stopped leaves it:
// the repository's per-run cap, and the run's share of the day at or past it.
// Nothing is written to the registry, which is the production shape rather than
// an omission — the run deletes its own entry as it ends, and the sweep's
// registry phase reaps a hard-killed run's entry before the sender's phase runs
// — so a test that only plants an entry is testing a window, not the state.
func (f *pushFixture) stoppedByCap(run string, perRun, spent int64) {
	f.db.Set(budget.PathCapsRepo(appSlug), map[string]any{"perRunMicros": perRun})
	f.db.Set(budget.PathSpendRun(budget.Day(f.now), appSlug, run), map[string]any{"spent": spent})
}

// kill turns the installation's kill switch on, which is the event every test
// that only needs *an* event uses: it needs no run and no account state.
func (f *pushFixture) kill() {
	f.db.Set(budget.PathKillGlobal, map[string]any{"on": true, "by": "owner@example.invalid", "at": f.now.UnixMilli()})
}

func (f *pushFixture) sweep(t *testing.T) (string, string) {
	t.Helper()
	out, errOut, err := execute(t, "budget", "history", "--sweep")
	if err != nil {
		t.Fatalf("the sweep failed: %v", err)
	}
	return out, errOut
}

// pushRows reads the account's ledger as the fake database holds it.
func (f *pushFixture) pushRows(t *testing.T) map[string]any {
	t.Helper()
	rows, _ := f.db.Value(pushTestPath + "/ledger").(map[string]any)
	return rows
}

// pushTestPath is one account's push node: the path the sender writes its own
// state under, and the path every "nothing was written" assertion names.
var pushTestPath = pairPushPath(pushTestUID)

// pushCategoriesSent is the categories a sweep asked the provider to send, in
// order.
func (f *pushFixture) categories() []string {
	out := make([]string, 0, len(f.sent))
	for _, req := range f.sent {
		out = append(out, req.Category)
	}
	return out
}

// TestPushSendsOnePerCategoryPerRunPerHour is the plan's first row and §3.7's
// two rules at once: the state-change key makes a run's cap ONE event however
// many sweeps see it, and the cap lets six of an hour through and suppresses
// the seventh. The planted control is the seventh run — the event past the
// sixth in the hour — and what it proves is that the cap is a cap and not a
// second dedupe.
//
// The second half is the hour itself, and it is two rules at once. A state
// change that is still in force an hour later is NOT a second notification: the
// row that records it is kept for as long as the event it names is derived
// (pushPrune), which is what makes a cap reached at 01:00 one buzz rather than
// one an hour until midnight. And the cap is a RATE and not a lifetime: once the
// states end, the rows go with them and the next hour's seven events are six
// sends and a control again.
func TestPushSendsOnePerCategoryPerRunPerHour(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	first := func(i int) string { return fmt.Sprintf("2026100%d-090000-aaaa", i) }
	second := func(i int) string { return fmt.Sprintf("2026101%d-090000-aaaa", i) }
	plant := func(name func(int) string) {
		for i := 1; i <= 7; i++ {
			f.stoppedByCap(name(i), 1_000_000, 1_000_000)
		}
	}
	end := func(name func(int) string, i int) {
		f.db.Set(budget.PathSpendRun(budget.Day(f.now), appSlug, name(i)), nil)
	}
	plant(first)

	out, _ := f.sweep(t)
	if got := len(f.sent); got != 6 {
		t.Fatalf("the first sweep sent %d notifications (%v), want 6: the cap lets six of an hour through and the seventh event is the planted control", got, f.categories())
	}
	if !strings.Contains(out, "push: 6 sent") {
		t.Errorf("output = %q, want the six it sent", out)
	}
	for _, req := range f.sent {
		if req.Category != pushCapReached {
			t.Errorf("sent %q, want every event of this sweep to be %q", req.Category, pushCapReached)
		}
		if req.Token != mailboxRegistrationToken || req.Platform != "ios" {
			t.Errorf("sent to %q on %q, want the registered token and platform", req.Token, req.Platform)
		}
	}
	if rows := f.pushRows(t); len(rows) != 6 {
		t.Errorf("the ledger holds %d rows (%v), want one per notification sent", len(rows), rows)
	}

	// The same hour again: the runs are still halted on their caps, and §3.7's
	// key is the transition, so nothing is a second event.
	before := len(f.sent)
	if _, _ = f.sweep(t); len(f.sent) != before {
		t.Fatalf("a second sweep of the same hour sent %d more (%v), want none: the state-change key is the transition, not the sweep", len(f.sent)-before, f.categories()[before:])
	}

	// An hour later the window has moved, and the six states are STILL in force:
	// the cap is still reached for those runs, and §3.7's key is the transition,
	// so the account is not told again. The seventh run has ended, so its state
	// is not derived at all — it was the control, and it was never announced.
	f.now = f.now.Add(time.Hour)
	end(first, 7)
	if _, _ = f.sweep(t); len(f.sent) != before {
		t.Fatalf("the sweep an hour later sent %d (%v), want none: a state change still in force is the one notification it already was", len(f.sent)-before, f.categories()[before:])
	}
	if rows := f.pushRows(t); len(rows) != 6 {
		t.Errorf("the ledger holds %d rows (%v) an hour later, want the six state changes that are still in force: the row is kept while its event is derived", len(rows), rows)
	}

	// The states end and a new hour's seven appear: the rows go with the states
	// they recorded, and the cap starts a fresh hour — six through, one
	// suppressed.
	for i := 1; i <= 6; i++ {
		end(first, i)
	}
	plant(second)
	before = len(f.sent)
	out, _ = f.sweep(t)
	if got := len(f.sent) - before; got != 6 {
		t.Fatalf("the next hour's sweep sent %d (%v), want 6: the cap is a rate and not a lifetime", got, f.categories()[before:])
	}
	if !strings.Contains(out, "6 ledger rows pruned") {
		t.Errorf("output = %q, want the ended states' rows counted as pruned", out)
	}
}

// TestPushDerivesCapReachedWithoutTheRunsRegistryEntry is the PRODUCTION shape
// of §3.6 (B)'s derivation, and the reason this category is not read from the
// run registry: a halted run deletes its own entry as it ends, and this job's
// registry phase reaps a hard-killed run's entry before the sender's phase runs,
// so at the 15-minute cadence the sweep almost never meets the entry of a run it
// stopped. What it meets is the run's share of the day against the cap that
// binds it — the two families §1 lets the phone read, which is what makes the
// buzz resolvable when it is tapped (§3.2) — so this test leaves the registry
// empty entirely and the event is still derived.
//
// The last two halves are the key and the comparison: the key names the run, so
// a condition still true on the next sweep is one event and not one per sweep
// (§3.7), and a run under its cap is not an event at all.
func TestPushDerivesCapReachedWithoutTheRunsRegistryEntry(t *testing.T) {
	const run = "20261001-090000-aaaa"
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	f.stoppedByCap(run, 1_000_000, 1_000_000)
	if v := f.db.Value("agents"); v != nil {
		t.Fatalf("the fixture left registry entries (%v): the shape under test is a halted run whose entry is already gone", v)
	}

	out, _ := f.sweep(t)
	if got := f.categories(); !slices.Equal(got, []string{pushCapReached}) {
		t.Fatalf("sent %v, want cap_reached with no registry entry in the tree", got)
	}
	if !strings.Contains(out, "1 sent") {
		t.Errorf("output = %q, want the one notification counted", out)
	}
	rows := f.pushRows(t)
	row, _ := rows[budget.Key(pushCapReached+":cap:"+appSlug+"/"+run)].(map[string]any)
	if row == nil {
		t.Fatalf("the ledger holds %v, want the row keyed by the run: the key is what makes the event one per run per hour", rows)
	}
	if got := mailboxNodeString(t, row, "key"); got != "cap:"+appSlug+"/"+run {
		t.Errorf("the row's key = %q, want the run named in it", got)
	}

	// The condition outlives the sweeps that see it: the same state-change key
	// on the next sweep is not a second buzz.
	before := len(f.sent)
	f.sweep(t)
	if len(f.sent) != before {
		t.Errorf("a second sweep sent %v (%v), want none: the key is the transition, not the sweep", len(f.sent)-before, f.categories()[before:])
	}

	// A run under its cap is not an event: "reached" is the cap, and the run's
	// share of the day is what is compared against it.
	f2 := newPushFixture(t, pushTestNow)
	f2.pair(t, mailboxSwitchState(nil))
	f2.stoppedByCap(run, 1_000_000, 500_000)
	f2.sweep(t)
	if len(f2.sent) != 0 {
		t.Errorf("sent %v for a run under its cap", f2.categories())
	}
}

// TestPushSuppressesACategoryTheAccountTurnedOff is §3.8's server-side switch:
// a category the account turned off is decided **before the provider is called**
// at all, so no token is spent, nothing reaches the OS, and no ledger row is
// written (a suppressed event is not an event the sender has sent).
func TestPushSuppressesACategoryTheAccountTurnedOff(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	prefs := mailboxSwitchState(nil)
	prefs["kill_switch"] = false
	f.pair(t, prefs)
	f.kill()
	f.stoppedByCap("20261001-090000-aaaa", 1_000_000, 1_000_000)

	out, _ := f.sweep(t)
	if got := f.categories(); !slices.Equal(got, []string{pushCapReached}) {
		t.Fatalf("sent %v, want only cap_reached: kill_switch is off for this account, so its event must not reach the provider", got)
	}
	if rows := f.pushRows(t); len(rows) != 1 {
		t.Fatalf("the ledger holds %d rows (%v), want only the one notification sent", len(rows), rows)
	}
	if !strings.Contains(out, "1 suppressed") {
		t.Errorf("output = %q, want the suppressed event counted", out)
	}

	// The whole set off: the provider is never called at all.
	f2 := newPushFixture(t, pushTestNow)
	off := mailboxSwitchState(nil)
	for _, c := range pushCategories {
		off[c] = false
	}
	f2.pair(t, off)
	f2.kill()
	f2.stoppedByCap("20261001-090000-aaaa", 1_000_000, 1_000_000)
	f2.sweep(t)
	if len(f2.sent) != 0 {
		t.Fatalf("an account with every category off was sent %v", f2.categories())
	}
	if v := f2.db.Value(pushTestPath + "/ledger"); v != nil {
		t.Fatalf("%s/ledger holds %v, want nothing: a suppressed event is not an event the sender has sent", pushTestPath, v)
	}
}

// pushQuietSweep is one sweep of a quiet-hours window at one instant: how many
// notifications the account was sent, given the zone and the clock.
func pushQuietSweep(t *testing.T, at time.Time, zone string) int {
	t.Helper()
	f := newPushFixture(t, at)
	f.pair(t, mailboxSwitchState(map[string]any{"zone": zone, "start": 22 * 60, "end": 23 * 60}))
	f.kill()
	f.sweep(t)
	return len(f.sent)
}

// TestPushHonoursQuietHoursInTheStoredZone is §3.8's datum: the window is a
// zone NAME resolved at the instant of the check, so the same UTC hour is
// quiet in one zone and not in another. The window is 22:00–23:00 local, and
// the pair below is the whole property — 21:30 UTC is 22:30 in Berlin and 21:30
// in UTC, so the account that stored Berlin is asleep and the one that stored
// UTC is not.
func TestPushHonoursQuietHoursInTheStoredZone(t *testing.T) {
	at := time.Date(2026, 1, 15, 21, 30, 0, 0, time.UTC)
	if got := pushQuietSweep(t, at, "Europe/Berlin"); got != 0 {
		t.Errorf("Berlin at %s: %d sent, want 0: 21:30 UTC is 22:30 CET, inside the stored window", at, got)
	}
	if got := pushQuietSweep(t, at, "UTC"); got != 1 {
		t.Errorf("UTC at %s: %d sent, want 1: 21:30 is outside the stored window, so the zone name is what decides", at, got)
	}
}

// TestPushQuietHoursFollowTheZoneAcrossTheDSTChange: a stored OFFSET would
// suppress the wrong hour twice a year and nothing would say so. The window is
// 22:00–23:00 local in Berlin, and each pair below straddles a European
// transition — the same UTC hour is quiet on the winter side and outside the
// window on the summer side, which is only true if the name is resolved at the
// instant of the check.
func TestPushQuietHoursFollowTheZoneAcrossTheDSTChange(t *testing.T) {
	const zone = "Europe/Berlin"
	for _, c := range []struct {
		what string
		at   time.Time
		want int
	}{
		{"the day before the spring change (CET, UTC+1)", time.Date(2026, 3, 28, 21, 30, 0, 0, time.UTC), 0},
		{"the day of the spring change (CEST, UTC+2)", time.Date(2026, 3, 29, 21, 30, 0, 0, time.UTC), 1},
		{"the day before the autumn change (CEST, UTC+2)", time.Date(2026, 10, 24, 21, 30, 0, 0, time.UTC), 1},
		{"the day after the autumn change (CET, UTC+1)", time.Date(2026, 10, 26, 21, 30, 0, 0, time.UTC), 0},
	} {
		if got := pushQuietSweep(t, c.at, zone); got != c.want {
			t.Errorf("%s at %s: %d sent, want %d", c.what, c.at, got, c.want)
		}
	}
}

// TestPushQuietHoursThatCannotBeHonouredSuppressNothing: quiet hours is a
// nuisance control, and silently dropping every notification for an account
// whose zone name the sender cannot resolve is a worse failure than a buzz at
// the wrong hour. The mailbox carries everything either way.
func TestPushQuietHoursThatCannotBeHonouredSuppressNothing(t *testing.T) {
	f := newPushFixture(t, time.Date(2026, 1, 15, 23, 0, 0, 0, time.UTC))
	f.pair(t, mailboxSwitchState(map[string]any{"zone": "Mars/Olympus", "start": 0, "end": 1439}))
	f.kill()
	_, errOut := f.sweep(t)
	if len(f.sent) != 1 {
		t.Fatalf("sent %v, want the notification: a zone this sender cannot resolve must not silence the account", f.categories())
	}
	if !strings.Contains(errOut, "Mars/Olympus") {
		t.Errorf("stderr = %q, want the unresolvable zone named", errOut)
	}
}

// TestPushSendsNothingForAnOrdinaryRun is the plan's control for §3.6 (B): the
// sweep DERIVES the events, so a run that crosses no threshold and asks nothing
// leaves **no pending-push row anywhere**. The last loop is the stronger half —
// no write of any kind reaches the push root on such a sweep, which is the
// property that makes "one sender, and it is the sweep" checkable rather than a
// claim about the tree's intent.
func TestPushSendsNothingForAnOrdinaryRun(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	f.entry("20261001-090000-aaaa", "", 0)
	f.db.Set("mailbox/"+pushTestUID+"/heartbeat", map[string]any{"at": f.now.Add(-time.Minute).UnixMilli(), "boot": "boot-a"})

	out, _ := f.sweep(t)
	if len(f.sent) != 0 {
		t.Fatalf("sent %v for a run that crossed no threshold", f.categories())
	}
	if !strings.Contains(out, "push: 0 sent") {
		t.Errorf("output = %q, want the sweep's own line", out)
	}
	if v := f.db.Value(pushTestPath + "/ledger"); v != nil {
		t.Fatalf("%s/ledger holds %v, want nothing: a run's own path writes no pending-push row", pushTestPath, v)
	}
	for _, r := range f.db.Requests() {
		if (r.Method == "PATCH" || r.Method == "PUT") && strings.Contains(r.Path, "config/admin/push") {
			t.Fatalf("the sweep wrote %s %s: an ordinary run leaves the push node alone", r.Method, r.Path)
		}
	}
}

// TestPushPrunesTheLedgerToItsWindow is Q10 (ii): the ledger's lifetime is the
// pairing's, and its WINDOW is the sweep's — a row past the dedupe window is
// deleted on the sweep, so the node holds the last hour's state changes and not
// a growing history of everything the account was ever told. Both rows here are
// cap_reached rows whose runs are gone from the day's spend, so both are state
// changes that are over; a row whose event is still derived is the other test's
// (TestPushSendsOnePerCategoryPerRunPerHour).
func TestPushPrunesTheLedgerToItsWindow(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	old := budget.Key(pushCapReached + ":cap:acme/app/20261001-090000-aaaa")
	fresh := budget.Key(pushCapReached + ":cap:acme/app/20261002-090000-bbbb")
	f.db.Set(pushTestPath+"/ledger/"+old, map[string]any{"category": pushCapReached, "key": "cap:acme/app/20261001-090000-aaaa",
		"at": f.now.Add(-2 * time.Hour).UnixMilli(), "state": pushStateSent})
	f.db.Set(pushTestPath+"/ledger/"+fresh, map[string]any{"category": pushCapReached, "key": "cap:acme/app/20261002-090000-bbbb",
		"at": f.now.Add(-time.Minute).UnixMilli(), "state": pushStateSent})

	out, _ := f.sweep(t)
	if v := f.db.Value(pushTestPath + "/ledger/" + old); v != nil {
		t.Errorf("the row past the window survived: %v", v)
	}
	if v := f.db.Value(pushTestPath + "/ledger/" + fresh); v == nil {
		t.Error("the row inside the window was pruned, so the next sweep would send its event again")
	}
	if !strings.Contains(out, "1 ledger rows pruned") {
		t.Errorf("output = %q, want the pruned row counted", out)
	}
}

// TestPushRetriesAPendingIntentInsideTheWindowAndDropsItPastIt is §6's retry: a
// send that failed is retried by the sweeps inside the retry window — and
// dropped when it ages out, never escalated into a late buzz. The row inside the
// window is retried and becomes a sent row; the row past it is pruned and never
// reaches the provider.
func TestPushRetriesAPendingIntentInsideTheWindowAndDropsItPastIt(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	inside := budget.Key(pushPrompt + ":p-inside")
	past := budget.Key(pushPrompt + ":p-past")
	f.db.Set(pushTestPath+"/ledger/"+inside, map[string]any{"category": pushPrompt, "key": "p-inside",
		"at": f.now.Add(-10 * time.Minute).UnixMilli(), "state": pushStatePending, "attempts": 1})
	f.db.Set(pushTestPath+"/ledger/"+past, map[string]any{"category": pushPrompt, "key": "p-past",
		"at": f.now.Add(-45 * time.Minute).UnixMilli(), "state": pushStatePending, "attempts": 1})

	out, _ := f.sweep(t)
	if got := f.categories(); !slices.Equal(got, []string{pushPrompt}) {
		t.Fatalf("sent %v, want the one intent inside the retry window", got)
	}
	if !strings.Contains(out, "1 retried") || !strings.Contains(out, "1 ledger rows pruned") {
		t.Errorf("output = %q, want the retry and the drop both counted", out)
	}
	if v := f.db.Value(pushTestPath + "/ledger/" + past); v != nil {
		t.Errorf("the intent past the retry window survived: %v", v)
	}
	row, _ := f.db.Value(pushTestPath + "/ledger/" + inside).(map[string]any)
	if got := mailboxNodeString(t, row, "state"); got != pushStateSent {
		t.Errorf("the retried row's state = %q, want %q", got, pushStateSent)
	}
	// The retry keeps the intent's own instant: the retry filter and the prune
	// both measure age from it, so a row restamped on every attempt would never
	// age out and would be retried for ever.
	if want := f.now.Add(-10 * time.Minute).UnixMilli(); !equalJSON(row["at"], want) {
		t.Errorf("the retried row's at = %v, want the original %v", row["at"], want)
	}

	// An intent whose category the account turned off between the sweeps is
	// dropped rather than sent: the switch is honoured at the send.
	f2 := newPushFixture(t, pushTestNow)
	prefs := mailboxSwitchState(nil)
	prefs["prompt"] = false
	f2.pair(t, prefs)
	f2.db.Set(pushTestPath+"/ledger/"+inside, map[string]any{"category": pushPrompt, "key": "p-inside",
		"at": f2.now.Add(-10 * time.Minute).UnixMilli(), "state": pushStatePending})
	f2.sweep(t)
	if len(f2.sent) != 0 {
		t.Fatalf("an intent whose category was turned off was sent anyway: %v", f2.categories())
	}
	if v := f2.db.Value(pushTestPath + "/ledger/" + inside); v != nil {
		t.Errorf("the dropped intent survived: %v", v)
	}
}

// TestPushDoesNotRetryInsideQuietHours is §6's quiet-hours row applied to the
// retry path: the window is honoured at the sender before the provider is
// called, and a retry is a send. An intent that comes due inside the window is
// left pending rather than sent — and left pending rather than dropped, because
// the retry window and not the quiet one is what ends an intent.
func TestPushDoesNotRetryInsideQuietHours(t *testing.T) {
	// 22:05, inside the stored 22:00–07:00 window, with an intent queued before
	// the window opened.
	f := newPushFixture(t, time.Date(2026, 1, 15, 22, 5, 0, 0, time.UTC))
	f.pair(t, mailboxSwitchState(map[string]any{"zone": "UTC", "start": 22 * 60, "end": 7 * 60}))
	inside := budget.Key(pushPrompt + ":p-inside")
	f.db.Set(pushTestPath+"/ledger/"+inside, map[string]any{"category": pushPrompt, "key": "p-inside",
		"at": f.now.Add(-15 * time.Minute).UnixMilli(), "state": pushStatePending, "attempts": 1})

	out, _ := f.sweep(t)
	if len(f.sent) != 0 {
		t.Fatalf("sent %v inside the quiet-hours window: a retry is a send, and the window is honoured at the sender before the provider is called", f.categories())
	}
	if !strings.Contains(out, "1 suppressed") {
		t.Errorf("output = %q, want the held-back retry counted as suppressed", out)
	}
	row, _ := f.db.Value(pushTestPath + "/ledger/" + inside).(map[string]any)
	if row == nil {
		t.Fatal("the intent was dropped: the quiet window is not the retry window, and the intent is left pending")
	}
	if got := mailboxNodeString(t, row, "state"); got != pushStatePending {
		t.Errorf("the row's state = %q, want %q", got, pushStatePending)
	}

	// Half an hour later the intent ages out of its own window and is pruned —
	// dropped, never sent late and never escalated (§6). That the row is GONE is
	// the assertion that carries the quiet check: the window is still open at
	// this hour, so a sender that had simply not retried would still hold it.
	f.now = f.now.Add(35 * time.Minute)
	f.sweep(t)
	if len(f.sent) != 0 {
		t.Fatalf("sent %v when the held-back intent aged out, want nothing: the retry window is what ends an intent", f.categories())
	}
	if v := f.db.Value(pushTestPath + "/ledger/" + inside); v != nil {
		t.Errorf("the aged-out intent survived: %v, want the retry window to have dropped it", v)
	}
}

// TestPushStopsTheSweepWhenARetryReapsTheToken is the retry path's own stop. A
// pending intent whose retry the provider reports unregistered ends the
// account's turn exactly as a fresh send does, and it has to: the registration
// is gone, so the events the same sweep derived must not be offered to a
// provider that has just said the token is dead. The sweep below derives the
// kill switch, and the provider is called ONCE — for the retry — because the
// reap is what the retry answered with.
func TestPushStopsTheSweepWhenARetryReapsTheToken(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	f.kill()
	inside := budget.Key(pushPrompt + ":p-inside")
	f.db.Set(pushTestPath+"/ledger/"+inside, map[string]any{"category": pushPrompt, "key": "p-inside",
		"at": f.now.Add(-10 * time.Minute).UnixMilli(), "state": pushStatePending, "attempts": 1})
	f.answer = pushUnregistered

	out, _ := f.sweep(t)
	if got := f.categories(); !slices.Equal(got, []string{pushPrompt}) {
		t.Fatalf("the provider was called for %v, want the retry alone: the token it reaped is not one this sweep's own events may be sent to", got)
	}
	if !strings.Contains(out, "1 tokens reaped") {
		t.Errorf("output = %q, want the reaped token counted once", out)
	}
}

// TestPushReapsATokenTheProviderReportsUnregistered is §6's first row: FCM says
// the token is dead, so the entry is DELETED — never marked, never retried — and
// the account's prefs are left alone so the app's next registration is a fresh
// token on the settings the user chose. Nothing here may touch
// `config/admin/devices/**`: that node is the admission, and only `fugaro pair`
// and `fugaro pair revoke` write it.
func TestPushReapsATokenTheProviderReportsUnregistered(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	f.kill()
	f.answer = pushUnregistered

	devices := f.db.Value(rules.PathDevices)
	out, errOut := f.sweep(t)
	for _, child := range []string{"token", "platform", "updated_at"} {
		if v := f.db.Value(pushTestPath + "/" + child); v != nil {
			t.Errorf("%s/%s holds %v, want the stale registration deleted", pushTestPath, child, v)
		}
	}
	if v := f.db.Value(pushTestPath + "/prefs"); v == nil {
		t.Error("the prefs were deleted with the token: what the user chose outlives the token that died")
	}
	if v := f.db.Value(pushTestPath + "/ledger"); v != nil {
		t.Errorf("%s/ledger holds %v, want nothing: there is no event left to dedupe", pushTestPath, v)
	}
	if !strings.Contains(out, "1 tokens reaped") {
		t.Errorf("output = %q, want the reaped token counted", out)
	}
	if !strings.Contains(errOut, "unregistered") {
		t.Errorf("stderr = %q, want the reaping said out loud", errOut)
	}
	if got := f.db.Value(rules.PathDevices); !equalJSON(devices, got) {
		t.Errorf("%s changed: %v -> %v, want the admission untouched", rules.PathDevices, devices, got)
	}

	// The node with nothing but the registration in it goes whole — and the
	// device node that admitted it is left exactly as it was: the whole-node
	// delete must not reach across into `devices/**` either.
	f2 := newPushFixture(t, pushTestNow)
	f2.db.Set(rules.PathDevices+"/"+pushTestUID, map[string]any{"name": "seed"})
	f2.db.Set(pairPushPath(pushTestUID), map[string]any{"token": mailboxRegistrationToken, "platform": "ios", "updated_at": f2.now.UnixMilli()})
	devices2 := f2.db.Value(rules.PathDevices)
	f2.kill()
	f2.answer = pushUnregistered
	f2.sweep(t)
	if v := f2.db.Value(pairPushPath(pushTestUID)); v != nil {
		t.Errorf("%s holds %v, want the node deleted: an emptied push node is one the next registration creates", pairPushPath(pushTestUID), v)
	}
	if got := f2.db.Value(rules.PathDevices); !equalJSON(devices2, got) {
		t.Errorf("%s changed: %v -> %v, want the admission untouched", rules.PathDevices, devices2, got)
	}
}

// TestPushReapsAnOrphanedPushNode is §3.4's other half, and the reason a revoke
// that removed only the device node is harmless: a push node whose device node
// is gone is DELETED, not marked, so nothing the rules consult is recreated and
// no marker is left behind for a reader to misread.
func TestPushReapsAnOrphanedPushNode(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	// An orphaned push node is one whose device node is gone: there is nothing
	// under `devices/**` for the reaping to touch, and it must stay that way —
	// the delete is the push node's own.
	devices := f.db.Value(rules.PathDevices)
	f.db.Set(pairPushPath(pushTestUID), map[string]any{"token": mailboxRegistrationToken, "platform": "ios",
		"updated_at": f.now.UnixMilli(), "prefs": mailboxSwitchState(nil)})
	f.kill()

	out, _ := f.sweep(t)
	if got := f.db.Value(rules.PathDevices); !equalJSON(devices, got) {
		t.Errorf("%s changed: %v -> %v, want the admission untouched", rules.PathDevices, devices, got)
	}
	if v := f.db.Value(pushTestPath); v != nil {
		t.Fatalf("%s holds %v, want it deleted: its device node is gone", pushTestPath, v)
	}
	if !strings.Contains(out, "1 nodes reaped") {
		t.Errorf("output = %q, want the orphan counted", out)
	}
	if len(f.sent) != 0 {
		t.Errorf("sent %v to an orphaned node", f.categories())
	}
	// The node above it is not left holding a marker either: the delete is the
	// child's, and an empty parent is not a node.
	if v, ok := f.db.Value(pairPushRoot).(map[string]any); ok && len(v) != 0 {
		t.Errorf("%s holds %v, want nothing: reaping deletes and never marks", pairPushRoot, v)
	}
}

// TestPushDerivesAPromptAndAStaleConsumer is the two per-account events, derived
// from what the account's own mailbox already holds: a stored prompt of
// `kind: prompt` (D168), and a consumer whose heartbeat is past the app's own
// presence rule (twelve minutes, shared/presence's DEFAULT_OFFLINE_INTERVALS of
// DEFAULT_INTERVAL_MS) — the same rule the phone applies when it says the
// orchestrator is offline, so the two cannot disagree.
//
// The state-change keys are the message's own id and the consumer's boot id, so
// a second sweep of the same unchanged mailbox is not a second buzz.
func TestPushDerivesAPromptAndAStaleConsumer(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	f.db.Set("mailbox/"+pushTestUID+"/outbox/p-1", pushPromptRecord(t, "p-1", f.now.Add(-2*time.Minute).UnixMilli()))
	f.db.Set("mailbox/"+pushTestUID+"/outbox/m-1", map[string]any{"id": "m-1", "sender": pushTestUID, "text": "a message",
		"at": f.now.Add(-2 * time.Minute).UnixMilli(), "mac": strings.Repeat("a", 64)})
	f.db.Set("mailbox/"+pushTestUID+"/heartbeat", map[string]any{"at": f.now.Add(-20 * time.Minute).UnixMilli(), "boot": "boot-a"})

	out, _ := f.sweep(t)
	want := []string{pushConsumerOffline, pushPrompt}
	if got := f.categories(); !slices.Equal(got, want) {
		t.Fatalf("sent %v, want %v: a stored prompt and a consumer silent past the presence rule", got, want)
	}
	if !strings.Contains(out, "2 sent") {
		t.Errorf("output = %q", out)
	}
	before := len(f.sent)
	f.sweep(t)
	if len(f.sent) != before {
		t.Fatalf("the unchanged mailbox was announced twice: %v", f.categories()[before:])
	}

	// An hour later both states are still in force — the prompt is unanswered
	// and the consumer is still silent — and §3.7's key is the transition, so
	// neither is announced again. The dedupe window has moved by now, which is
	// what makes this the rows' own doing: a prompt stays in the outbox until
	// its TTL, and a quiet consumer stays quiet, so pruning their rows on age
	// alone would make both a buzz an hour, for ever.
	f.now = f.now.Add(time.Hour)
	if _, _ = f.sweep(t); len(f.sent) != before {
		t.Fatalf("the states still in force an hour later were announced again: %v", f.categories()[before:])
	}

	// A consumer inside the presence rule, and a mailbox with no stored prompt:
	// neither is an event.
	f2 := newPushFixture(t, pushTestNow)
	f2.pair(t, mailboxSwitchState(nil))
	f2.db.Set("mailbox/"+pushTestUID+"/heartbeat", map[string]any{"at": f2.now.Add(-time.Minute).UnixMilli(), "boot": "boot-a"})
	f2.sweep(t)
	if len(f2.sent) != 0 {
		t.Fatalf("sent %v for a consumer a minute old and no prompt", f2.categories())
	}
}

// TestPushAnnouncesAKillSwitchLeftOnOnce is §3.7 for the state change that
// outlives every window: a switch nobody turns off. Its key names the instant the
// switch was written, so the event is one transition — and the row that records
// it is kept for as long as the switch is on, which is what makes an hour of
// sweeps one buzz rather than one an hour.
func TestPushAnnouncesAKillSwitchLeftOnOnce(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	f.kill()

	f.sweep(t)
	if got := f.categories(); !slices.Equal(got, []string{pushKillSwitch}) {
		t.Fatalf("sent %v, want the one switch", got)
	}
	f.now = f.now.Add(2 * time.Hour)
	f.sweep(t)
	if got := f.categories(); len(got) != 1 {
		t.Fatalf("a switch left on was announced again two hours later: %v", got[1:])
	}
	if rows := f.pushRows(t); len(rows) != 1 {
		t.Errorf("the ledger holds %d rows (%v), want the one the switch's transition wrote", len(rows), rows)
	}
}

// TestPushSkipsAPromptTheOwnerAnsweredAndOnePastItsTtl is §3.7's rule where the
// state change is the OWNER's: a prompt stays in the outbox until D118's 72 hours
// expire it, so the row's own window would re-announce an unanswered question
// every hour and an answered one for the rest of its TTL. What settles a prompt
// is an inbox record naming it in `reply_to` — the inbox is create-only, so the
// answer is still there — and what ends it is the mailbox sweep's own boundary,
// drawn in the same place here so the two cannot disagree.
func TestPushSkipsAPromptTheOwnerAnsweredAndOnePastItsTtl(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	asked := f.now.Add(-2 * time.Minute).UnixMilli()
	f.db.Set("mailbox/"+pushTestUID+"/outbox/p-open", pushPromptRecord(t, "p-open", asked))
	f.db.Set("mailbox/"+pushTestUID+"/outbox/p-answered", pushPromptRecord(t, "p-answered", asked))
	f.db.Set("mailbox/"+pushTestUID+"/outbox/p-stale", pushPromptRecord(t, "p-stale", f.now.Add(-73*time.Hour).UnixMilli()))
	f.db.Set("mailbox/"+pushTestUID+"/inbox/a-1", map[string]any{"id": "a-1", "sender": pushTestUID, "text": "yes",
		"at": asked, "kind": mailboxKindAnswer, "reply_to": "p-answered", "mac": strings.Repeat("a", 64)})

	out, _ := f.sweep(t)
	if got := f.categories(); !slices.Equal(got, []string{pushPrompt}) {
		t.Fatalf("sent %v, want the one prompt nobody has answered: an answered question and one past its TTL are not events", got)
	}
	if !strings.Contains(out, "1 sent") {
		t.Errorf("output = %q, want the one notification counted", out)
	}
	rows := f.pushRows(t)
	if _, ok := rows[budget.Key(pushPrompt+":prompt:p-open")]; !ok {
		t.Errorf("the ledger holds %v, want the open prompt's row", rows)
	}
	if len(rows) != 1 {
		t.Errorf("the ledger holds %d rows (%v), want the one open prompt", len(rows), rows)
	}

	// The answer is what settled it and not the clock: with the inbox empty, the
	// same outbox is two prompts again.
	f2 := newPushFixture(t, pushTestNow)
	f2.pair(t, mailboxSwitchState(nil))
	f2.db.Set("mailbox/"+pushTestUID+"/outbox/p-answered", pushPromptRecord(t, "p-answered", asked))
	f2.sweep(t)
	if got := f2.categories(); !slices.Equal(got, []string{pushPrompt}) {
		t.Fatalf("sent %v for a prompt with no answer in the inbox, want it announced", got)
	}
}

// pushPromptRecord is one stored prompt as P5.2.4's consumer writes it: the five
// fields a message has plus the kind, signed with the pairing's key so the shape
// is the one the app reads (the sender verifies nothing — it has no key — but a
// record the schema refuses is not a stored prompt and must not buzz).
func pushPromptRecord(t *testing.T, id string, at int64) map[string]any {
	t.Helper()
	record := map[string]any{"id": id, "sender": pushTestUID, "text": "a question for the owner", "at": at, "kind": mailboxKindPrompt}
	mac, err := mailbox.MacOf(record, []byte(mailboxKeyMaterial), "outbox")
	if err != nil {
		t.Fatalf("signing %s: %v", id, err)
	}
	record["mac"] = mac
	return record
}

// TestPushCapNearIsOffByDefaultAndOnWhenAsked: D160 answers "available, off by
// default", so the default set sends nothing for a run at four fifths of its own
// cap and the account that turns it on is told. The threshold is this task's own
// number (pushCapNearPercent) and the design names only the category, which is
// why it is stated here rather than pinned to a design sentence.
func TestPushCapNearIsOffByDefaultAndOnWhenAsked(t *testing.T) {
	const run = "20261001-090000-aaaa"
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	f.db.Set(budget.PathCapsRepo(appSlug), map[string]any{"perRunMicros": 1_000_000})
	f.entry(run, "", 900_000)
	f.sweep(t)
	if len(f.sent) != 0 {
		t.Fatalf("sent %v with cap_near off: D160's default set does not interrupt for it", f.categories())
	}

	f2 := newPushFixture(t, pushTestNow)
	prefs := mailboxSwitchState(nil)
	prefs["cap_near"] = true
	f2.pair(t, prefs)
	f2.db.Set(budget.PathCapsRepo(appSlug), map[string]any{"perRunMicros": 1_000_000})
	f2.entry(run, "", 900_000)
	f2.sweep(t)
	if got := f2.categories(); !slices.Equal(got, []string{pushCapNear}) {
		t.Fatalf("sent %v, want cap_near once the account asked for it", got)
	}
}

// TestPushFailuresAreWarningsNotTheJobsExitCode is §6's rule: the mailbox is
// authoritative and the push is the doorbell, so a send that fails must never
// end the history job — a job that exited non-zero on a push would stop the
// sweep, the user deletion and the rollover behind it. The intent is queued and
// the next sweep retries it.
//
// This is also the state of the tree until P5.2.7 grants the FCM permission:
// the send fails, and the sweep must be no worse for it.
func TestPushFailuresAreWarningsNotTheJobsExitCode(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	f.kill()
	f.sendErr = errors.New("the permission firebasecloudmessaging.messages.create is not granted")

	out, errOut := f.sweep(t)
	if !strings.Contains(out, "0 sent, 0 suppressed, 1 queued") {
		t.Errorf("output = %q, want the failed send counted as queued", out)
	}
	if !strings.Contains(errOut, "push:") || !strings.Contains(errOut, "queued") {
		t.Errorf("stderr = %q, want the failure said out loud", errOut)
	}
	rows := f.pushRows(t)
	if len(rows) != 1 {
		t.Fatalf("the ledger holds %d rows (%v), want the queued intent", len(rows), rows)
	}
	for _, row := range rows {
		held, _ := row.(map[string]any)
		if got := mailboxNodeString(t, held, "state"); got != pushStatePending {
			t.Errorf("the queued row's state = %q, want %q", got, pushStatePending)
		}
	}
}

// TestPushSkipsAnAccountWithNoToken: §6's first row. The node exists — the
// registration wrote the prefs and the token child was reaped — so there is
// nothing to send to and nothing to write: the app re-registers when it opens.
func TestPushSkipsAnAccountWithNoToken(t *testing.T) {
	f := newPushFixture(t, pushTestNow)
	f.pair(t, mailboxSwitchState(nil))
	f.db.Set(pushTestPath+"/token", nil)
	f.kill()

	out, _ := f.sweep(t)
	if len(f.sent) != 0 {
		t.Fatalf("sent %v to an account with no token", f.categories())
	}
	if v := f.db.Value(pushTestPath + "/ledger"); v != nil {
		t.Errorf("%s/ledger holds %v, want nothing written for an account that cannot be reached", pushTestPath, v)
	}
	if !strings.Contains(out, "0 sent") {
		t.Errorf("output = %q", out)
	}
}

// TestFCMSenderReadsTheProvidersOwnWordAndSendsTheCategory is the shipped
// provider against a server that answers like FCM: what it posts is the
// category and nothing else (§3.1 — no title, no body, no run, nothing the
// lock screen could leak), and reaping takes the provider's own word.
//
// The middle cases are the ones that matter: a 404 that does not say
// `UNREGISTERED` (a wrong project path, a proxy) and a token minted for another
// project are FAILURES the sender queues and warns about, not registrations it
// deletes on a guess. Deleting a token the app could have made right is the one
// mistake here that the account cannot see.
func TestFCMSenderReadsTheProvidersOwnWordAndSendsTheCategory(t *testing.T) {
	for _, c := range []struct {
		what   string
		status int
		body   string
		want   pushOutcome
	}{
		{"the provider took it", http.StatusOK, `{"name":"projects/aurora-fp/messages/1"}`, pushSent},
		{"a token the provider reports unregistered", http.StatusNotFound,
			`{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`, pushUnregistered},
		{"a 404 that does not say which token", http.StatusNotFound, `{"error":{"status":"NOT_FOUND"}}`, pushFailed},
		{"a token minted for another project", http.StatusBadRequest,
			`{"error":{"status":"INVALID_ARGUMENT","details":[{"errorCode":"SENDER_ID_MISMATCH"}]}}`, pushFailed},
		{"the provider is down", http.StatusServiceUnavailable, `{"error":{"status":"UNAVAILABLE"}}`, pushFailed},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Path; got != "/v1/projects/aurora-fp/messages:send" {
				t.Errorf("%s: posted to %s, want the Firebase project's messages:send path", c.what, got)
			}
			var body struct {
				Message struct {
					Token string            `json:"token"`
					Data  map[string]string `json:"data"`
				} `json:"message"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("%s: the body is not JSON: %v", c.what, err)
			}
			if body.Message.Token != mailboxRegistrationToken {
				t.Errorf("%s: token = %q, want the registered one", c.what, body.Message.Token)
			}
			if len(body.Message.Data) != 1 || body.Message.Data["category"] != pushKillSwitch {
				t.Errorf("%s: the payload carries %v, want the category and nothing else", c.what, body.Message.Data)
			}
			w.WriteHeader(c.status)
			fmt.Fprint(w, c.body)
		}))
		defer srv.Close()
		got, _ := fcmSender{project: "aurora-fp", url: srv.URL, http: srv.Client()}.
			send(context.Background(), pushRequest{Token: mailboxRegistrationToken, Platform: "ios", Category: pushKillSwitch})
		if got != c.want {
			t.Errorf("%s: outcome = %v, want %v", c.what, got, c.want)
		}
	}
}

// TestPushCategoryNamesAreTheSchemas: the payload's category and the
// registration message's `prefs` child are ONE vocabulary (§3.8) — a switch the
// app writes and a category the sender sends that drifted apart would be a
// category no account could ever turn off.
func TestPushCategoryNamesAreTheSchemas(t *testing.T) {
	switches := mailbox.PrefsFields[:len(pushCategories)]
	if !slices.Equal(pushCategories, switches) {
		t.Fatalf("pushCategories = %v, want the registration message's switches %v", pushCategories, switches)
	}
	for _, c := range pushCategories {
		if !slices.Contains(mailbox.PrefsRequired, c) {
			t.Errorf("%q is not one of the switches a registration must carry", c)
		}
	}
}

// TestPushNoProducerWritesAPendingRow is §3.6 (B) as a property of the tree
// rather than of one sweep: no account on a RUN's path names the push node at
// all. (A) put the send permission on the runner and the gateway, which is
// exactly the widening §4 rule 6 forbids, so the check that the sender stayed
// the sweep's is that the run-side packages cannot reach the node to write a
// pending row into it.
//
// The three files that do name it are the writers table's three — pair.go
// (revoke's delete), mailbox.go (the registration principal) and push.go (the
// sender) — plus comments; what this test refuses is a fourth, in a package a
// run runs.
func TestPushNoProducerWritesAPendingRow(t *testing.T) {
	runSide := []string{
		"internal/runner", "internal/agent", "internal/gateway", "internal/gitops",
		"internal/gitprov", "internal/runstore", "internal/backend", "internal/infra",
		// internal/budget runs inside the run container too: the runner takes its
		// leases through it, so it holds the run's own identity and is exactly the
		// kind of account §3.6 (A) refuses to widen with a send.
		"internal/budget",
	}
	var offenders []string
	for _, dir := range runSide {
		// The test's working directory is this package's, so the tree is two up
		// (internal/cli -> the repository root).
		root := filepath.Join("..", "..", dir)
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("%s: %v (the list of run-side packages is stale)", root, err)
		}
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(body), pairPushRoot) {
				offenders = append(offenders, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	if len(offenders) != 0 {
		t.Fatalf("%v name %q: the sender is the sweep and nothing on a run's path writes a pending-push row (§3.6 (B))", offenders, pairPushRoot)
	}
}
