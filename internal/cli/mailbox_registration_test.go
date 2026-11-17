package cli

// fugaro mailbox (mobile P5.2.4): the registration message applied to the
// sibling node `config/admin/push/$uid`, tested against the task's own
// verification list — the node written once, a re-registration overwriting the
// token and the instant and leaving the ledger alone, a message whose MAC does
// not verify refused with nothing written, a registration whose account has no
// device node writing nothing (revocation stays final), and **no write of this
// path touching `config/admin/devices/**`**, which only `fugaro pair` and
// `fugaro pair revoke` write.
//
// The design's §3.4 is the shape and §3.5 (b) is the path: the token and the
// platform live in a sibling of D166's device node, never a child of it, and
// the message that carries them arrives over the same MAC-covered mailbox every
// other phone-to-host kind does. The prefs are §3.8's, and the names the node
// holds are the schema's own (`shared/mailbox-schema`) rather than a second
// vocabulary — TestMailboxRegistrationPrefsAreTheSchemas is what holds the two
// together.
//
// Every case drives the real command tree (`mailbox --once`), so what is
// asserted is the pass's own document and the store the pass left behind: the
// fake database is the operator's credential's, and the push node is written
// with the operator's own ADC (C0).

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/mailbox"
)

// mailboxRegistrationToken is the token a test's phone carries. It is a phrase
// and not a token-shaped literal, for the same reason mailboxKeyMaterial is:
// nothing here is a secret and nothing here should look like one.
const mailboxRegistrationToken = "a-registration-token-for-tests-not-a-secret"

// mailboxRegistration is the `extra` a registration record is signed with: the
// kind, the two fields the kind requires (P5.2.1) and §3.8's switches when the
// test is about them.
func mailboxRegistration(token, platform string, prefs map[string]any) map[string]any {
	extra := map[string]any{"kind": mailboxKindRegistration, "token": token, "platform": platform}
	if prefs != nil {
		extra["prefs"] = prefs
	}
	return extra
}

// mailboxSwitchState is §3.8's whole switch state as a message carries it: the
// five categories the schema requires (the message is the whole state, so a
// partial one is refused) and a quiet-hours window when the test wants one. The
// defaults are D160's — the interrupting categories on, cap-near off — so a
// test that changes one is changing a value the app could have sent.
func mailboxSwitchState(quietHours map[string]any) map[string]any {
	prefs := map[string]any{
		"kill_switch":      true,
		"cap_reached":      true,
		"consumer_offline": true,
		"cap_near":         false,
		"prompt":           true,
	}
	if quietHours != nil {
		prefs["quiet_hours"] = quietHours
	}
	return prefs
}

// mailboxSeedDeviceNode writes the device node as `fugaro pair` writes it
// (`pairDeviceNode`): the allowlist's whole content is that the node exists
// (D166), and the four fields are the ones M13 lets the node hold.
func mailboxSeedDeviceNode(f *mailboxFixture, uid string) {
	f.db.Set(rules.PathDevices+"/"+uid, map[string]any{
		"name": "seed", "email": "owner@example.com", "role": "owner",
		"at": "2026-11-17T00:00:00Z",
	})
}

// mailboxPushNode is the push node this fixture's database holds, as an object.
func mailboxPushNode(t *testing.T, f *mailboxFixture, uid string) map[string]any {
	t.Helper()
	node, ok := f.db.Value(pairPushPath(uid)).(map[string]any)
	if !ok {
		t.Fatalf("%s holds %v, want the push node a registration writes", pairPushPath(uid), f.db.Value(pairPushPath(uid)))
	}
	return node
}

// mailboxNodeString and mailboxNodeMillis read one field out of a node the fake
// database holds: a string, and an epoch-millisecond instant (the fake keeps
// numbers as json.Number, which is what a reader of the real database gets).
func mailboxNodeString(t *testing.T, node map[string]any, field string) string {
	t.Helper()
	s, ok := node[field].(string)
	if !ok {
		t.Fatalf("%s = %v, want a string", field, node[field])
	}
	return s
}

func mailboxNodeMillis(t *testing.T, node map[string]any, field string) int64 {
	t.Helper()
	number, ok := node[field].(json.Number)
	if !ok {
		t.Fatalf("%s = %v (%T), want a number", field, node[field], node[field])
	}
	ms, err := number.Int64()
	if err != nil {
		t.Fatalf("%s = %v, want an integer epoch-millisecond instant", field, number)
	}
	return ms
}

// mailboxNodeChild is one child of a node the fake database holds, as an
// object, and a failure naming the parent when it is not one.
func mailboxNodeChild(t *testing.T, node map[string]any, field string) map[string]any {
	t.Helper()
	child, ok := node[field].(map[string]any)
	if !ok {
		t.Fatalf("%s = %v, want an object", field, node[field])
	}
	return child
}

// TestMailboxRegistrationWritesTheSiblingPushNode is P5.2.4's first row: a
// registration message the phone signed writes `config/admin/push/$uid` — the
// node pairing does not write and no client token may read — with the token,
// the platform and the instant the consumer wrote them. The node did not exist
// before the pass, so this is the create §3.4's writers table names, and the
// instant is the **database's** clock rather than the message's, which is what
// `updated_at` means.
func TestMailboxRegistrationWritesTheSiblingPushNode(t *testing.T) {
	f := newMailboxFixture(t, "")
	mailboxSeedDeviceNode(f, mailboxUID)
	at := mailboxNowMs() - 90_000
	f.writeRecord(t, "reg-1", mailboxRegistration(mailboxRegistrationToken, "ios", nil), at, mailboxKeyMaterial)
	if v := f.db.Value(pairPushPath(mailboxUID)); v != nil {
		t.Fatalf("%s holds %v before any pass: pairing writes no push node, so the phone's registration is the node's first write", pairPushPath(mailboxUID), v)
	}

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a registration is an outcome of a completed pass", err)
	}
	node := mailboxPushNode(t, f, mailboxUID)
	if got := mailboxNodeString(t, node, "token"); got != mailboxRegistrationToken {
		t.Errorf("token = %q, want the token the message carried", got)
	}
	if got := mailboxNodeString(t, node, "platform"); got != "ios" {
		t.Errorf("platform = %q, want ios", got)
	}
	if updated := mailboxNodeMillis(t, node, "updated_at"); updated <= at {
		t.Errorf("updated_at = %d, want an instant after the message's own %d: the field is the consumer's write time on the database's clock, not the phone's", updated, at)
	} else if lag := mailboxNowMs() - updated; lag > 60_000 || lag < -60_000 {
		t.Errorf("updated_at = %d, want this pass's own instant (%d ± a minute): the consumer reads the clock from the database (D117)", updated, mailboxNowMs())
	}
	doc := mailboxJSON(t, out)
	if got := mailboxActedOutcome(t, doc, 0); got != mailboxOutcomeRegistered {
		t.Errorf("acted[0].outcome = %q, want %s", got, mailboxOutcomeRegistered)
	}
	if acted, ok := doc["acted"].([]any); !ok || len(acted) != 1 {
		t.Errorf("acted = %v, want the one registration this pass applied", doc["acted"])
	}
	if got := mailboxCount(t, doc, "refused"); got != 0 {
		t.Errorf("refused = %d, want 0: a registration the store took is not a refusal", got)
	}
	if f.db.Value(rules.PathDevices+"/"+mailboxUID) == nil {
		t.Error("the device node is gone: this path writes the sibling and never `config/admin/devices/**`")
	}
}

// TestMailboxRegistrationIsNotHandedToTheOrchestrator: a registration is a
// machine-to-machine message, and an orchestrator handed one would take it for
// a question it must answer — the reason the kind used to be refused outright.
// What it reaches is the push node and nothing else.
func TestMailboxRegistrationIsNotHandedToTheOrchestrator(t *testing.T) {
	f := newMailboxFixture(t, "")
	mailboxSeedDeviceNode(f, mailboxUID)
	offers := f.offers(t)
	f.writeRecord(t, "reg-1", mailboxRegistration(mailboxRegistrationToken, "android", nil), mailboxNowMs(), mailboxKeyMaterial)

	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	if len(*offers) != 0 {
		t.Fatalf("a registration was handed to the orchestrator: %v", *offers)
	}
}

// TestMailboxRegistrationAgainOverwritesTokenAndLeavesTheLedger is P5.2.4's
// second row, and it is what makes a re-registration a re-registration rather
// than a second registration: the token, the platform and the instant are
// overwritten, §3.8's switch state is replaced with the state the message
// carries — a dropped `quiet_hours` turns quiet hours off instead of leaving
// the last window behind — and **the sender's `ledger` is left exactly as it
// is**, because it is P5.2.6's state in this node and not this consumer's to
// clear.
func TestMailboxRegistrationAgainOverwritesTokenAndLeavesTheLedger(t *testing.T) {
	f := newMailboxFixture(t, "")
	mailboxSeedDeviceNode(f, mailboxUID)
	f.db.Set(pairPushPath(mailboxUID), map[string]any{
		"token": "the-token-this-phone-had-before", "platform": "android", "updated_at": 1,
		"prefs": mailboxSwitchState(map[string]any{"zone": "Europe/Berlin", "start": 1320, "end": 420}),
		"ledger": map[string]any{
			"sent": map[string]any{"cap-near": 2},
		},
	})
	at := mailboxNowMs()
	f.writeRecord(t, "reg-2", mailboxRegistration("the-token-the-phone-has-now", "ios", mailboxSwitchState(nil)), at, mailboxKeyMaterial)

	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("err = %v, want exit 0", err)
	}
	node := mailboxPushNode(t, f, mailboxUID)
	if got := mailboxNodeString(t, node, "token"); got != "the-token-the-phone-has-now" {
		t.Errorf("token = %q, want the message's own: a re-registration overwrites it", got)
	}
	if got := mailboxNodeString(t, node, "platform"); got != "ios" {
		t.Errorf("platform = %q, want the message's own", got)
	}
	if updated := mailboxNodeMillis(t, node, "updated_at"); updated <= 1 {
		t.Errorf("updated_at = %d, want the instant this pass wrote, not the one already there", updated)
	}
	prefs := mailboxNodeChild(t, node, "prefs")
	if _, present := prefs["quiet_hours"]; present {
		t.Errorf("prefs.quiet_hours = %v, want it gone: the message carried the whole state and no window, which is §3.8's default of off", prefs["quiet_hours"])
	}
	if prompt, ok := prefs["prompt"].(bool); !ok || !prompt {
		t.Errorf("prefs.prompt = %v, want the switch the message carried", prefs["prompt"])
	}
	ledger := mailboxNodeChild(t, node, "ledger")
	sent := mailboxNodeChild(t, ledger, "sent")
	if got, ok := sent["cap-near"].(json.Number); !ok || got.String() != "2" {
		t.Errorf("ledger.sent.cap-near = %v, want the sender's own state untouched: the write is a merge, and this consumer clears nothing it cannot re-derive", sent["cap-near"])
	}
}

// TestMailboxRegistrationWithoutTheDeviceNodeWritesNothing is P5.2.4's fourth
// row: the device node's existence IS the allowlist (§3.4, D166), so a
// registration whose account has none — the everyday case being a message the
// phone sent before `fugaro pair revoke` and this pass processes after it —
// writes nothing at all. Revocation stays final, and the phone's token does not
// outlive the pairing it was minted for.
func TestMailboxRegistrationWithoutTheDeviceNodeWritesNothing(t *testing.T) {
	f := newMailboxFixture(t, "")
	f.writeRecord(t, "reg-1", mailboxRegistration(mailboxRegistrationToken, "ios", mailboxSwitchState(nil)), mailboxNowMs(), mailboxKeyMaterial)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: a registration this consumer may not apply is a per-message refusal, not an action refused", err)
	}
	if v := f.db.Value(pairPushPath(mailboxUID)); v != nil {
		t.Fatalf("%s holds %v: a registration whose device node is gone must write nothing, which is what makes a revocation final", pairPushPath(mailboxUID), v)
	}
	doc := mailboxJSON(t, out)
	if code := mailboxRefusedCode(t, doc, 0); code != string(mailboxCodeRegistrationUnpaired) {
		t.Errorf("refused[0].code = %q, want %s", code, mailboxCodeRegistrationUnpaired)
	}
	warnings := strings.Join(mailboxWarnings(t, doc), "\n")
	if !strings.Contains(warnings, rules.PathDevices+"/"+mailboxUID) {
		t.Errorf("warnings = %q, want the missing device node named: the operator's answer is a pair or a re-pair", warnings)
	}
	if acted, ok := doc["acted"].([]any); !ok || len(acted) != 0 {
		t.Errorf("acted = %v, want nothing: no token was stored", doc["acted"])
	}
}

// TestMailboxRegistrationWithABadMACWritesNothing is P5.2.4's third row, and it
// is M4a's order: the MAC is verified **before** the delivery reaches the
// ledger, so a registration signed with a key this pairing does not hold is
// refused and never applied — the write is the last thing in the chain and the
// first thing the tag protects.
func TestMailboxRegistrationWithABadMACWritesNothing(t *testing.T) {
	f := newMailboxFixture(t, "")
	mailboxSeedDeviceNode(f, mailboxUID)
	f.writeRecord(t, "reg-1", mailboxRegistration(mailboxRegistrationToken, "ios", nil), mailboxNowMs(), mailboxOtherKey)

	out, _, err := execute(t, "mailbox", "--once", "--json")
	if err != nil {
		t.Fatalf("err = %v, want exit 0: an unverifiable message is a recorded outcome (M4a)", err)
	}
	if v := f.db.Value(pairPushPath(mailboxUID)); v != nil {
		t.Fatalf("%s holds %v: a message that does not verify is never acted on", pairPushPath(mailboxUID), v)
	}
	if code := mailboxRefusedCode(t, mailboxJSON(t, out), 0); code != string(mailbox.CodeBadMAC) {
		t.Errorf("refused[0].code = %q, want %s", code, mailbox.CodeBadMAC)
	}
}

// TestMailboxRegistrationNeverWritesTheDeviceNode is P5.2.4's last row, and the
// design's rule 1 (§3.4): `config/admin/devices/**` is written by `fugaro pair`
// and `fugaro pair revoke` alone, because the node's existence is the allowlist
// and a write that recreates it re-admits the device. The fake database refuses
// every write under that path here, so the pass below succeeds **only** because
// this path does not touch it: a registration that wrote a field under the
// device node would be refused by the store and this test would see the
// failure rather than the reader having to trust the code.
func TestMailboxRegistrationNeverWritesTheDeviceNode(t *testing.T) {
	f := newMailboxFixture(t, "")
	mailboxSeedDeviceNode(f, mailboxUID)
	f.db.Deny(rules.PathDevices)
	before := f.db.Value(rules.PathDevices)
	f.writeRecord(t, "reg-1", mailboxRegistration(mailboxRegistrationToken, "ios", mailboxSwitchState(nil)), mailboxNowMs(), mailboxKeyMaterial)

	if _, _, err := execute(t, "mailbox", "--once"); err != nil {
		t.Fatalf("err = %v, want exit 0: applying a registration writes the sibling node and nothing under the device node", err)
	}
	if got := mailboxNodeString(t, mailboxPushNode(t, f, mailboxUID), "token"); got != mailboxRegistrationToken {
		t.Errorf("token = %q, want the registration applied", got)
	}
	if after := f.db.Value(rules.PathDevices); !reflect.DeepEqual(before, after) {
		t.Errorf("%s changed from %v to %v: only `fugaro pair` and `fugaro pair revoke` write there", rules.PathDevices, before, after)
	}
}

// TestMailboxRegistrationRefusedByTheDatabaseNamesTheRole: `config/admin/**` is
// client-denied and every write under it is the operator's own credential's, so
// applying a registration needs the `roles/firebasedatabase.admin` a pairing
// needs (P5.2.2, §3.5 (b), Q3). Where the credential does not hold it the
// database refuses the write, and what the operator gets is this command's own
// sentence naming the role rather than the shared budget-admin one, which would
// send them to the wrong place. It is a run-level refusal (exit 1): the write
// is this command's, so a pass that cannot make it stops and reports the reason
// rather than going on to the next delivery.
//
// **And the refusal is not the end of the delivery**, which is what
// TestMailboxRegistrationRefusedByTheDatabaseIsOwedByTheNextPass is about: the
// role this sentence names is one an operator grants while the phone is away,
// so the record of a registration the store would not take is taken back out of
// the ledger and the first pass that finds the store willing applies it.
func TestMailboxRegistrationRefusedByTheDatabaseNamesTheRole(t *testing.T) {
	f := newMailboxFixture(t, "")
	mailboxSeedDeviceNode(f, mailboxUID)
	f.db.Deny(pairPushPath(mailboxUID))
	f.writeRecord(t, "reg-1", mailboxRegistration(mailboxRegistrationToken, "ios", nil), mailboxNowMs(), mailboxKeyMaterial)

	_, _, err := execute(t, "mailbox", "--once")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, code %d, want exit 1: the database refused this command's own write", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "roles/firebasedatabase.admin") {
		t.Errorf("err = %v, want the role the write needs named", err)
	}
	if !strings.Contains(err.Error(), pairPushPath(mailboxUID)) {
		t.Errorf("err = %v, want the node that could not be written named", err)
	}
	if v := f.db.Value(pairPushPath(mailboxUID)); v != nil {
		t.Fatalf("%s holds %v: a refused write writes nothing", pairPushPath(mailboxUID), v)
	}
}

// TestMailboxRegistrationRefusedByTheDatabaseIsOwedByTheNextPass is the other
// half of the paragraph above, and the case the operator actually lives: the
// role is granted, or the outage ends, while the phone is away — so the
// delivery has to be there for the pass that finds the store willing rather
// than have been consumed by the pass that did not. The whole case is deny →
// pass → lift → pass, and what the last pass leaves in the push node is the
// token the first one could not write.
//
// Nothing happens between the passes but the lifting of the refusal: the same
// consumer object makes both, so the second is the renewal of the lease the
// first took (a holder's own re-read honours a fresh claim only), and the inbox
// record is the one the first pass read — the phone never speaks again. What
// the refused pass leaves behind is a push node that was never written **and a
// ledger with no record of the delivery** (`mailbox.Ledger.Retract`): a record
// still there would meet the second pass as `OutcomeDuplicate` and the token
// would never be written at all.
func TestMailboxRegistrationRefusedByTheDatabaseIsOwedByTheNextPass(t *testing.T) {
	f := newMailboxFixture(t, "")
	mailboxSeedDeviceNode(f, mailboxUID)
	f.db.Deny(pairPushPath(mailboxUID))
	f.writeRecord(t, "reg-1", mailboxRegistration(mailboxRegistrationToken, "ios", nil), mailboxNowMs(), mailboxKeyMaterial)
	ctx := context.Background()
	c := f.consumer(t, "boot-registration-owed")

	if _, err := c.pass(ctx, c.pairings[0]); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, code %d, want exit 1: the database refused this command's own write", err, ExitCode(err))
	}
	if v := f.db.Value(pairPushPath(mailboxUID)); v != nil {
		t.Fatalf("%s holds %v: a refused write writes nothing", pairPushPath(mailboxUID), v)
	}
	if entries := mailboxLedgerEntries(t); len(entries) != 0 {
		t.Fatalf("the ledger holds %+v after the refused pass: a registration the store would not take is owed by the next pass, so its record is taken back out rather than left to answer the delivery as a duplicate", entries)
	}

	// The operator grants the role — or the network comes back — while the phone
	// is away. Nothing else about the fixture changes.
	f.db.Deny("")

	second, err := c.pass(ctx, c.pairings[0])
	if err != nil {
		t.Fatalf("the pass after the write was allowed: %v", err)
	}
	if got := mailboxNodeString(t, mailboxPushNode(t, f, mailboxUID), "token"); got != mailboxRegistrationToken {
		t.Errorf("token = %q, want the token the refused pass could not write: the second pass read the same inbox node and applied it", got)
	}
	if len(second.acted) != 1 || second.acted[0].ID != "reg-1" || second.acted[0].Outcome != mailboxOutcomeRegistered {
		t.Errorf("acted = %+v, want the one registration this pass applied", second.acted)
	}
	if second.verdict.duplicates != 0 {
		t.Errorf("duplicates = %d, want 0: a delivery no pass has applied is not a duplicate of anything", second.verdict.duplicates)
	}
	if entries := mailboxLedgerEntries(t); len(entries) != 1 || entries[0].Sender != mailboxUID ||
		entries[0].ID != "reg-1" || entries[0].Status != mailbox.StatusActed {
		t.Errorf("the ledger holds %+v after the applied pass, want the one record of the delivery that was performed", entries)
	}
}

// mailboxLedgerEntries is this machine's mailbox ledger for the fixture's
// project as the passes above left it — the file under the state directory
// newMailboxFixture isolates, opened the way the command opens it.
func mailboxLedgerEntries(t *testing.T) []mailbox.LedgerEntry {
	t.Helper()
	ledger, err := mailbox.OpenLedger(mailbox.LedgerOptions{Project: "aurora", PromptTtlMs: mailboxPromptTtlMs})
	if err != nil {
		t.Fatalf("opening the ledger: %v", err)
	}
	return ledger.Entries()
}

// TestMailboxRegistrationPrefsAreTheSchemas holds §3.8's child names to
// `shared/mailbox-schema`'s, for the reason TestMailboxInboxKindsAndVerbsAreThe
// Schemas holds the kinds to it: the node's vocabulary is the message's, and a
// switch the schema renames must be a failing test here rather than a reader
// (P5.2.6's sender, the app) looking for a child nothing writes.
func TestMailboxRegistrationPrefsAreTheSchemas(t *testing.T) {
	node := mailboxPrefsNode(&mailbox.Prefs{
		KillSwitch: true, CapReached: true, ConsumerOffline: true, CapNear: false, Prompt: true,
		QuietHours: &mailbox.QuietHours{Zone: "Europe/Berlin", Start: 22 * 60, End: 7 * 60},
	})
	got := make([]string, 0, len(node))
	for field := range node {
		got = append(got, field)
	}
	slices.Sort(got)
	want := slices.Clone(mailbox.PrefsFields)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the push node's prefs children are %v, want the schema's %v", got, want)
	}
	switches := make([]string, 0, len(node))
	for field := range node {
		if field != "quiet_hours" {
			switches = append(switches, field)
		}
	}
	slices.Sort(switches)
	wantSwitches := slices.Clone(mailbox.PrefsRequired)
	slices.Sort(wantSwitches)
	if !slices.Equal(switches, wantSwitches) {
		t.Errorf("the switches written are %v, want the five the message must carry, %v", switches, wantSwitches)
	}
	window := mailboxNodeChild(t, node, "quiet_hours")
	gotWindow := make([]string, 0, len(window))
	for field := range window {
		gotWindow = append(gotWindow, field)
	}
	slices.Sort(gotWindow)
	wantWindow := slices.Clone(mailbox.QuietHoursFields)
	slices.Sort(wantWindow)
	if !slices.Equal(gotWindow, wantWindow) {
		t.Errorf("the quiet-hours children are %v, want the schema's %v", gotWindow, wantWindow)
	}
	if zone, ok := window["zone"].(string); !ok || zone != "Europe/Berlin" {
		t.Errorf("quiet_hours.zone = %v, want the zone the app sent: §3.8 stores the name and not an offset, which is what survives a DST change", window["zone"])
	}

	// The window is written only when the message carries one: an absent window
	// is §3.8's default of off, and a switch with no window is what the sender
	// reads as "no quiet hours".
	off := mailboxPrefsNode(&mailbox.Prefs{})
	if _, present := off["quiet_hours"]; present {
		t.Errorf("a message with no window wrote prefs.quiet_hours = %v, want it absent", off["quiet_hours"])
	}
}
