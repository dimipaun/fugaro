// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// P4.2's own cases: the merchant's notification as it arrives, the adapter that
// maps it to this service's canonical event, the ledger the events leave, and
// the events that are parked rather than applied (F15, F16).
//
// Three notification bodies are written out here literally, as Paddle's own
// JSON — spelled the way the merchant's documentation spells it, field by field,
// and carrying a field this service does not read — because the adapter's whole
// reason for existing is that the body is the merchant's and not this service's.
// A case that built its body out of this package's own shapes would prove
// nothing about that. The ids in them are obviously not real ones, so no fixture
// here is a value a deploy holds.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// recordedNotification is one `transaction.completed` exactly as Paddle Billing
// posts it: the event's own envelope, and the transaction under `data`. It
// carries a field this service does not read (`data.checkout`) and a field a
// future Paddle may stop sending, which is the shape the adapter is lenient
// about by design.
const recordedNotification = `{
  "event_id": "evt_01j8xkq2v3b4c5d6e7f8g9h0jk",
  "notification_id": "ntf_01j8xkq2v3b4c5d6e7f8g9h0jl",
  "event_type": "transaction.completed",
  "occurred_at": "2026-11-10T12:00:00Z",
  "data": {
    "id": "txn_01j8xkq2v3b4c5d6e7f8g9h0jm",
    "status": "completed",
    "origin": "web",
    "customer_id": "ctm_01j8xkq2v3b4c5d6e7f8g9h0jn",
    "subscription_id": "sub_01j8xkq2v3b4c5d6e7f8g9h0jp",
    "currency_code": "USD",
    "custom_data": {"org_id": "org-dana", "workspace_id": "ws-dana"},
    "items": [
      {"price_id": "pri_01j8xkq2v3b4c5d6e7f8g9h0jq", "quantity": 3}
    ],
    "billing_period": {
      "starts_at": "2026-11-10T12:00:00Z",
      "ends_at": "2027-11-10T12:00:00Z"
    },
    "checkout": {"url": "https://dana.example/checkout"},
    "details": {"totals": {"grand_total": "34800", "total": "29000", "tax": "5800"}}
  }
}`

// recordedRenewal is Paddle's own word for a transaction it created to renew a
// subscription it already bills, which is the whole of what tells a renewal from
// a purchase.
const recordedRenewal = `{
  "event_id": "evt_01j8xkq2v3b4c5d6e7f8g9h0jr",
  "event_type": "transaction.completed",
  "occurred_at": "2027-11-10T12:00:00Z",
  "data": {
    "id": "txn_01j8xkq2v3b4c5d6e7f8g9h0js",
    "origin": "subscription_recurring",
    "subscription_id": "sub_01j8xkq2v3b4c5d6e7f8g9h0jp",
    "currency_code": "USD",
    "items": [{"quantity": 3}],
    "details": {"totals": {"grand_total": "34800"}}
  }
}`

// recordedRefund is one adjustment: Paddle's own object for money going back,
// which names the transaction and the subscription and never a key.
const recordedRefund = `{
  "event_id": "evt_01j8xkq2v3b4c5d6e7f8g9h0jt",
  "event_type": "adjustment.created",
  "occurred_at": "2026-12-01T09:00:00Z",
  "data": {
    "id": "adj_01j8xkq2v3b4c5d6e7f8g9h0jv",
    "action": "refund",
    "status": "approved",
    "transaction_id": "txn_01j8xkq2v3b4c5d6e7f8g9h0jm",
    "subscription_id": "sub_01j8xkq2v3b4c5d6e7f8g9h0jp",
    "currency_code": "USD",
    "totals": {"grand_total": "34800"}
  }
}`

// paddleBody is one canonical event spelled the way the merchant posts it: the
// envelope a Paddle notification has, and the payload its object of that kind
// has. Every case in this package writes the event it means and drives the
// adapter through this, so a mapping that stopped reading a field fails here
// rather than in a deploy.
func paddleBody(t *testing.T, e Event) []byte {
	t.Helper()
	return notificationBody(t, e, "")
}

// renewalBody is the same, with Paddle's own word for a transaction it made to
// renew a subscription it already bills: the field that tells a renewal from a
// purchase, and the only one that does.
func renewalBody(t *testing.T, e Event) []byte {
	t.Helper()
	return notificationBody(t, e, paddleOriginRecurring)
}

func notificationBody(t *testing.T, e Event, origin string) []byte {
	t.Helper()
	id := e.Transaction
	if id == "" {
		id = e.Subscription
	}
	custom := map[string]any{}
	if e.Organisation != "" {
		custom["org_id"] = e.Organisation
	}
	if e.WorkspaceID != "" {
		custom["workspace_id"] = e.WorkspaceID
	}
	data := map[string]any{
		"id":            id,
		"currency_code": "USD",
		"custom_data":   custom,
		"items":         []any{map[string]any{"price_id": "pri_fixture", "quantity": e.Seats}},
		"details":       map[string]any{"totals": map[string]any{"grand_total": "12000"}},
	}
	if e.Subscription != "" {
		data["subscription_id"] = e.Subscription
	}
	if e.Transaction != "" {
		data["transaction_id"] = e.Transaction
	}
	if e.CustomerID != "" {
		data["customer_id"] = e.CustomerID
	}
	if origin != "" {
		data["origin"] = origin
	}
	if e.Type == EventRefund || e.Type == EventChargeback {
		data["action"] = e.Type
		// Paddle states an adjustment's status beside its action, and only the
		// one whose money has gone back is acted on, so the body this fixture
		// posts for a withdrawal carries it: a case about another status writes
		// its own (`unapprovedAdjustmentBody`).
		data["status"] = paddleStatusApproved
	}
	if e.TermDays > 0 {
		data["billing_period"] = map[string]any{
			"starts_at": e.OccurredAt.UTC().Format(time.RFC3339),
			"ends_at":   e.OccurredAt.Add(time.Duration(e.TermDays) * 24 * time.Hour).UTC().Format(time.RFC3339),
		}
	}
	body := map[string]any{
		"event_id":    e.ID,
		"event_type":  paddleTypeOf(e),
		"occurred_at": e.OccurredAt.UTC().Format(time.RFC3339),
		"data":        data,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding the notification %s: %v", e.ID, err)
	}
	return encoded
}

// unapprovedAdjustmentBody is one adjustment with a status of its own: the same
// body Paddle posts for a refund, as it posts it before the money has moved.
func unapprovedAdjustmentBody(t *testing.T, e Event, status string) []byte {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(notificationBody(t, e, ""), &body); err != nil {
		t.Fatalf("decoding the notification %s: %v", e.ID, err)
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("the notification %s carries no payload", e.ID)
	}
	data["status"] = status
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding the notification %s: %v", e.ID, err)
	}
	return encoded
}

// paddleTypeOf is the merchant's own name for the event one canonical event is
// about.
func paddleTypeOf(e Event) string {
	switch e.Type {
	case EventPurchase:
		return paddleTransactionCompleted
	case EventSeatChange:
		return paddleSubscriptionUpdated
	case EventCancellation:
		return paddleSubscriptionCanceled
	case EventRefund, EventChargeback:
		return paddleAdjustmentCreated
	default:
		return "customer.updated"
	}
}

// boughtEvent delivers one purchase and answers the bytes it posted, so that a
// case about a redelivery can post the merchant's own body again rather than a
// second one this service wrote.
func (f *fixture) boughtEvent(t *testing.T, e Event) []byte {
	t.Helper()
	if e.OccurredAt.IsZero() {
		e.OccurredAt = f.now
	}
	e = f.claimsOf(e)
	body := paddleBody(t, e)
	if rec := f.deliverRaw(body, signEvent(f.secret, f.now, body)); rec.Code != http.StatusOK {
		t.Fatalf("the purchase %s was answered %d, want 200: %s", e.ID, rec.Code, rec.Body)
	}
	return body
}

// purchaseEvent is one purchase's own event, with the fields a case states left
// to it.
func purchaseEvent(id, organisation string, seats, termDays int) Event {
	return Event{
		ID: id, Type: EventPurchase,
		Subscription: "sub-" + organisation,
		Organisation: organisation,
		WorkspaceID:  workspaceID,
		Email:        buyerAddress(organisation),
		Seats:        seats, TermDays: termDays,
	}
}

// stored is one event as the store holds it.
func (f *fixture) stored(t *testing.T, id string) WireLedgerEvent {
	t.Helper()
	held, present, err := f.svc.storedEvent(context.Background(), id)
	if err != nil {
		t.Fatalf("reading the stored event %s: %v", id, err)
	}
	if !present {
		t.Fatalf("the store holds no event %s, want the verified body kept as it arrived", id)
	}
	return held
}

// events is every stored event, oldest first.
func (f *fixture) events(t *testing.T) []WireLedgerEvent {
	t.Helper()
	held, err := f.svc.eventsReceived(context.Background())
	if err != nil {
		t.Fatalf("reading the stored events: %v", err)
	}
	return held
}

// ledger is the readable ledger the stored events make.
func (f *fixture) ledger(t *testing.T) []WireLedgerEntry {
	t.Helper()
	entries, err := f.svc.ledgerEntries(context.Background())
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	return entries
}

// entitlements is every licence the store holds.
func (f *fixture) entitlements(t *testing.T) []WireEntitlement {
	t.Helper()
	held, err := recordsWhere[WireEntitlement](context.Background(), f.docs, entitlementsCollection, Where{})
	if err != nil {
		t.Fatalf("reading the licences: %v", err)
	}
	return held
}

// onlyEntitlement is the one licence the store holds, which is what a purchase
// leaves behind.
func (f *fixture) onlyEntitlement(t *testing.T) WireEntitlement {
	t.Helper()
	held := f.entitlements(t)
	if len(held) != 1 {
		t.Fatalf("the store holds %d licence(s), and this case reads the one", len(held))
	}
	return held[0]
}

// TestEventIsStoredAsReceivedBeforeProcessing: F16 — the verified body is
// written under Paddle's own event id **before** anything is made of it, with
// the signature header that proved it. What proves the order is an event whose
// processing fails: the body is in the store, as it arrived, and the state says
// nothing was made of it.
func TestEventIsStoredAsReceivedBeforeProcessing(t *testing.T) {
	f := newFixture(t)
	// A purchase for a term the checkout does not sell: the body verifies and
	// the paid path refuses it, which is a 500 and the merchant's retry.
	refused := f.claimsOf(purchaseEvent("evt-refused", "acme.example", 5, 7))
	refused.OccurredAt = f.now
	body := paddleBody(t, refused)
	header := signEvent(f.secret, f.now, body)
	if rec := f.deliverRaw(body, header); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a purchase for a term the checkout does not sell was answered %d, want 500", rec.Code)
	}
	held := f.stored(t, "evt-refused")
	if held.Body != string(body) {
		t.Errorf("the stored body is %q, want the merchant's own bytes (%q): the event is kept as received and never re-encoded", held.Body, body)
	}
	if held.Signature != header {
		t.Errorf("the stored event carries the signature %q, want the header that proved it (%q)", held.Signature, header)
	}
	if held.Notification != paddleTransactionCompleted {
		t.Errorf("the stored event is the merchant's %q, want %q", held.Notification, paddleTransactionCompleted)
	}
	if held.State != ledgerStored {
		t.Errorf("the event of a failed delivery is stored as %q, want %q: the state is what says nothing was made of it", held.State, ledgerStored)
	}
	if !held.ReceivedAt.Equal(f.now) {
		t.Errorf("the event was received at %s, want %s", held.ReceivedAt, f.now)
	}
	if held.EntitlementID != nil {
		t.Errorf("the refused event names the licence %q, want none: nothing was granted", *held.EntitlementID)
	}
	if got := f.entitlements(t); len(got) != 0 {
		t.Errorf("the refused purchase granted %d licence(s), want none", len(got))
	}

	// And the same kind of body, once the term is one the checkout sells: the
	// event is stored first and then marked with what it resolved to. The clock
	// moves between the two, so the arrival instant the record keeps is readable
	// as its own.
	f.advance(time.Minute)
	f.boughtEvent(t, purchaseEvent("evt-1", "acme.example", 5, 365))
	arrived := f.now
	f.advance(time.Second)
	applied := f.stored(t, "evt-1")
	if applied.State != ledgerProcessed {
		t.Errorf("the applied event is stored as %q, want %q", applied.State, ledgerProcessed)
	}
	licence := f.onlyEntitlement(t)
	if applied.EntitlementID == nil || *applied.EntitlementID != licence.ID {
		t.Errorf("the applied event names the licence %v, want the one it granted (%s)", applied.EntitlementID, licence.ID)
	}
	if !applied.ReceivedAt.Equal(arrived) {
		t.Errorf("the applied event was received at %s, want the instant it arrived (%s): the record keeps the delivery's own instant and not the event's", applied.ReceivedAt, arrived)
	}
}

// TestStaleTimestampIsRefused: F16's window — a delivery whose own timestamp is
// more than five minutes from this service's clock is refused, and refused the
// way a bad signature is: 200 and nothing stored, so a stranger posting bodies
// learns nothing about which of the merchant's events it could replay. A
// delivery inside the window is ordinary traffic, a retry included.
func TestStaleTimestampIsRefused(t *testing.T) {
	f := newFixture(t)
	e := f.claimsOf(purchaseEvent("evt-1", "acme.example", 5, 365))
	e.OccurredAt = f.now
	body := paddleBody(t, e)
	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{name: "six minutes old", at: f.now.Add(-6 * time.Minute)},
		{name: "six minutes ahead", at: f.now.Add(6 * time.Minute)},
		{name: "a day old", at: f.now.Add(-24 * time.Hour)},
	} {
		rec := f.deliverRaw(body, signEvent(f.secret, tc.at, body))
		if rec.Code != http.StatusOK {
			t.Errorf("a delivery stamped %s was answered %d, want 200: the merchant is not told which of its events a stranger could replay", tc.name, rec.Code)
		}
	}
	if got := f.events(t); len(got) != 0 {
		t.Errorf("a delivery outside the window stored %d event(s), want none: an unverified body never reaches the store", len(got))
	}
	if got := f.entitlements(t); len(got) != 0 {
		t.Errorf("a delivery outside the window granted %d licence(s), want none", len(got))
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("a delivery outside the window mailed %d message(s), want none", got)
	}

	// Four minutes old, which is a merchant retry and not a replay.
	f.deliverRaw(body, signEvent(f.secret, f.now.Add(-4*time.Minute), body))
	if got := f.events(t); len(got) != 1 {
		t.Errorf("a delivery four minutes old stored %d event(s), want one: the window is wide enough for an ordinary retry", len(got))
	}
}

// TestRedeliveryDoesNotDoubleCount: §5.2's idempotency, at the store — the same
// event delivered twice is one event, one licence, one notice and one line of
// the ledger, and the second delivery is answered 200 as the first was.
func TestRedeliveryDoesNotDoubleCount(t *testing.T) {
	f := newFixture(t)
	body := f.boughtEvent(t, purchaseEvent("evt-1", "acme.example", 5, 365))

	f.advance(2 * time.Hour)
	if rec := f.deliverRaw(body, signEvent(f.secret, f.now, body)); rec.Code != http.StatusOK {
		t.Fatalf("the redelivered purchase was answered %d, want 200: it is an event the store already holds", rec.Code)
	}
	if got := f.events(t); len(got) != 1 {
		t.Errorf("the same event delivered twice is stored %d time(s), want once", len(got))
	}
	if got := f.entitlements(t); len(got) != 1 {
		t.Errorf("the same event delivered twice granted %d licence(s), want one", len(got))
	}
	if got := len(f.mail.sent); got != 1 {
		t.Errorf("the same event delivered twice mailed %d message(s), want one", got)
	}
	if got := f.ledger(t); len(got) != 1 {
		t.Errorf("the same event delivered twice made %d ledger line(s), want one: a redelivery is not a second sale", len(got))
	}
	if got := f.mintedKeys(); len(got) != 0 {
		t.Errorf("a purchase minted %d key(s), want none (D2196)", len(got))
	}
}

// TestPaddleBodyMapsToTheEvent: F16's adapter — the merchant's own body, field
// by field, becomes this service's canonical event, and a body the service does
// not act on is answered as no event at all rather than as one with empty
// fields.
func TestPaddleBodyMapsToTheEvent(t *testing.T) {
	n, err := decodeNotification([]byte(recordedNotification))
	if err != nil {
		t.Fatalf("decoding the recorded notification: %v", err)
	}
	e, known := canonicalEvent(n, Live)
	if !known {
		t.Fatalf("the recorded %s mapped to no event", n.EventType)
	}
	for _, want := range []struct {
		field string
		got   any
		want  any
	}{
		{"id", e.ID, "evt_01j8xkq2v3b4c5d6e7f8g9h0jk"},
		{"type", e.Type, EventPurchase},
		{"mode", e.Mode, Live},
		{"occurred_at", e.OccurredAt.UTC().Format(time.RFC3339), "2026-11-10T12:00:00Z"},
		{"transaction", e.Transaction, "txn_01j8xkq2v3b4c5d6e7f8g9h0jm"},
		{"subscription", e.Subscription, "sub_01j8xkq2v3b4c5d6e7f8g9h0jp"},
		{"organisation", e.Organisation, "org-dana"},
		{"workspace_id", e.WorkspaceID, "ws-dana"},
		{"seats", e.Seats, 3},
		{"term_days", e.TermDays, 365},
	} {
		if want.got != want.want {
			t.Errorf("the notification's %s mapped to %v, want %v", want.field, want.got, want.want)
		}
	}

	// The money and the kind, which are the ledger's own reading of the same
	// body: the total is Paddle's string in the currency's minor unit, and it is
	// a purchase because the transaction is not one Paddle made to renew.
	if kind, sale := ledgerKind(n); !sale || kind != "purchase" {
		t.Errorf("the recorded transaction is ledgered as %q (sale=%v), want a purchase", kind, sale)
	}
	if got := n.grandTotalMicros(); got != 348000000 {
		t.Errorf("the recorded total is %d micro-dollars, want 348000000: 34800 cents, whole-number arithmetic and no float", got)
	}
	// A body whose money is only in the payload's top-level totals is read from
	// there, which is where an adjustment states it, and a body whose figure is
	// not a number makes a line of no money rather than a wrong one.
	flat := n
	flat.Data.Details = paddleTransaction{}
	flat.Data.Totals = paddleTotals{Total: "29000"}
	if got := flat.grandTotalMicros(); got != 290000000 {
		t.Errorf("the recorded total read from the payload's own `total` is %d micro-dollars, want 290000000", got)
	}
	flat.Data.Totals = paddleTotals{GrandTotal: "three hundred"}
	if got := flat.grandTotalMicros(); got != 0 {
		t.Errorf("a total that is not a number is read as %d micro-dollars, want 0: a line with a wrong figure is worse than one with none", got)
	}

	// A renewal is the same notification type and a different `origin`, which is
	// the only thing that tells the two apart (P5.2's report asks what was sold
	// and what was renewed).
	renewal, err := decodeNotification([]byte(recordedRenewal))
	if err != nil {
		t.Fatalf("decoding the recorded renewal: %v", err)
	}
	if kind, sale := ledgerKind(renewal); !sale || kind != "renewal" {
		t.Errorf("the recorded renewal is ledgered as %q (sale=%v), want a renewal", kind, sale)
	}

	// An adjustment is the money going back: a refund of the transaction, which
	// is a withdrawal of the licence and a negative line.
	refund, err := decodeNotification([]byte(recordedRefund))
	if err != nil {
		t.Fatalf("decoding the recorded refund: %v", err)
	}
	withdrawal, known := canonicalEvent(refund, Live)
	if !known || withdrawal.Type != EventRefund {
		t.Fatalf("the recorded adjustment mapped to %+v (known=%v), want a refund", withdrawal, known)
	}
	if withdrawal.Transaction != "txn_01j8xkq2v3b4c5d6e7f8g9h0jm" || withdrawal.Subscription != "sub_01j8xkq2v3b4c5d6e7f8g9h0jp" {
		t.Errorf("the refund names the transaction %q and the subscription %q, want both of the merchant's own", withdrawal.Transaction, withdrawal.Subscription)
	}
	if got := refund.grandTotalMicros(); got != -348000000 {
		t.Errorf("the recorded refund is %d micro-dollars, want -348000000: the money went the other way", got)
	}

	// A `subscription.canceled` is a cancellation and not a seat change, and the
	// two arrive under different types over one payload shape.
	cancelled, err := decodeNotification([]byte(`{"event_id":"evt_2","event_type":"subscription.canceled","occurred_at":"2026-11-11T12:00:00Z","data":{"id":"sub_1","subscription_id":"sub_1"}}`))
	if err != nil {
		t.Fatalf("decoding a cancellation: %v", err)
	}
	off, known := canonicalEvent(cancelled, Live)
	if !known || off.Type != EventCancellation || off.Subscription != "sub_1" {
		t.Errorf("the cancellation mapped to %+v (known=%v), want a cancellation of sub_1", off, known)
	}
	if kind, sale := ledgerKind(cancelled); sale {
		t.Errorf("a cancellation is ledgered as %q, want no line: it moves no money", kind)
	}

	// A notification this service does not act on is not an event: it is stored
	// and answered, and it is not read as one with empty fields.
	other, err := decodeNotification([]byte(`{"event_id":"evt_1","event_type":"customer.updated","data":{"id":"ctm_1"}}`))
	if err != nil {
		t.Fatalf("decoding a notification of another kind: %v", err)
	}
	if _, known := canonicalEvent(other, Live); known {
		t.Error("a notification this service does not act on mapped to an event, want none")
	}

	// And an adjustment whose action is neither a refund nor a chargeback is not
	// this service's to act on: a reversal puts money back to Fugaro, and acting
	// on it as a withdrawal would revoke a licence the customer has paid for.
	reversal, err := decodeNotification([]byte(`{"event_id":"evt_3","event_type":"adjustment.created","data":{"id":"adj_1","action":"chargeback_reverse","transaction_id":"txn_1"}}`))
	if err != nil {
		t.Fatalf("decoding a reversal: %v", err)
	}
	if _, known := canonicalEvent(reversal, Live); known {
		t.Error("a reversal mapped to a withdrawal, want no event: it is not this service's to act on until the licensor rules")
	}
}

// TestRefundFindsItsEntitlement: F16 — Paddle sends no key id and never has, so
// what a refund names is the transaction and the subscription, and the licence
// is what either of them resolves to. The key the customer's installation runs
// is the one the binding minted for that licence, and it is what is withdrawn.
func TestRefundFindsItsEntitlement(t *testing.T) {
	f := newFixture(t)
	bought := f.purchase("evt-1", "acme.example", 5, 365)
	licence := f.onlyEntitlement(t)
	if entry, held, _ := f.store.Revocation(context.Background(), bought.KeyID); held {
		t.Fatalf("the key %s is already withdrawn (%+v) before the refund", bought.KeyID, entry)
	}

	f.advance(24 * time.Hour)
	// The refund names the subscription alone, which is the shape an adjustment
	// against a subscription has: no transaction of its own and no workspace.
	f.deliver(Event{
		ID: "evt-2", Type: EventRefund, OccurredAt: f.now,
		Subscription: "sub-acme.example",
	})
	entry, held, err := f.store.Revocation(context.Background(), bought.KeyID)
	if err != nil {
		t.Fatalf("reading the revocation back: %v", err)
	}
	if !held {
		t.Fatalf("the refund of %s left no entry for the key %s, want the licence's own key withdrawn", licence.ID, bought.KeyID)
	}
	if entry.Reason != ReasonRefund {
		t.Errorf("the withdrawal is filed as %q, want %q", entry.Reason, ReasonRefund)
	}
	if !entry.At.Equal(f.now) {
		t.Errorf("the withdrawal is stamped %s, want the event's own instant %s", entry.At, f.now)
	}
	recorded := f.stored(t, "evt-2")
	if recorded.State != ledgerProcessed {
		t.Errorf("the refund is stored as %q, want %q", recorded.State, ledgerProcessed)
	}
	if recorded.EntitlementID == nil || *recorded.EntitlementID != licence.ID {
		t.Errorf("the refund resolved to %v, want the licence %s: the event resolves to the entitlement and never to a key", recorded.EntitlementID, licence.ID)
	}

	// And the other half of the resolution, which is the shape an adjustment
	// against a one-off transaction has: the transaction alone, with the
	// checkout's own record as the step to the licence.
	f.advance(time.Hour)
	f.deliver(Event{
		ID: "evt-3", Type: EventChargeback, OccurredAt: f.now,
		Transaction: "txn-sub-acme.example",
	})
	byTransaction := f.stored(t, "evt-3")
	if byTransaction.State != ledgerProcessed {
		t.Errorf("the chargeback is stored as %q, want %q", byTransaction.State, ledgerProcessed)
	}
	if byTransaction.EntitlementID == nil || *byTransaction.EntitlementID != licence.ID {
		t.Errorf("the chargeback resolved to %v, want the licence %s: the transaction is the other half of the resolution", byTransaction.EntitlementID, licence.ID)
	}
	// §8's list still holds the first delivery's entry and is left exactly as it
	// is: the list only ever grows, so a second event about the same licence is
	// one entry from the first instant whatever reason it carries (K2.2).
	after, held, _ := f.store.Revocation(context.Background(), bought.KeyID)
	if !held || after != entry {
		t.Errorf("the licence's entry is %+v (held=%v), want the first delivery's %+v", after, held, entry)
	}
}

// TestARefundedLicenceCannotBeBoundAgain: F29 — revocation is by entitlement, so
// the money going back withdraws the licence itself and not only the keys that
// existed when it arrived. A record left active is one the customer binds again
// (the first rebind after a binding is not held by D2198's clock), and the
// binding mints a fresh key the walk never reached, running to the end of the
// term that was refunded: the withdrawal would be undone by the customer's own
// next request, with no merchant event to notice it.
func TestARefundedLicenceCannotBeBoundAgain(t *testing.T) {
	f := newFixture(t)
	bought := f.purchase("evt-1", "acme.example", 3, 30)
	licence := f.onlyEntitlement(t)
	signed, recorded := f.signer.calls, len(f.records())

	f.advance(time.Hour)
	f.deliver(Event{ID: "evt-2", Type: EventRefund, OccurredAt: f.now, Subscription: "sub-acme.example"})
	if _, held, _ := f.store.Revocation(context.Background(), bought.KeyID); !held {
		t.Fatalf("the refund left the key %s live, want the licence's key withdrawn", bought.KeyID)
	}
	after, held, err := getRecord[WireEntitlement](context.Background(), f.docs, entitlementsCollection, licence.ID)
	if err != nil || !held {
		t.Fatalf("the refund left no licence %s (held=%v, err=%v), want it withdrawn where it stands", licence.ID, held, err)
	}
	if after.Status != entitlementRevoked {
		t.Errorf("the refunded licence is %q, want %q: a licence left active is one the customer binds again, and the binding mints the key the withdrawal did not reach (F29)", after.Status, entitlementRevoked)
	}

	if err := f.bindingAttempt("acme.example", "sub-acme.example"); !errors.Is(err, errEntitlementNotActive) {
		t.Errorf("binding the refunded licence answered %v, want %v: the licence is withdrawn, so there is no key to mint for it", err, errEntitlementNotActive)
	}
	if got := f.signer.calls; got != signed {
		t.Errorf("the refused binding signed %d key(s), want none", got-signed)
	}
	if got := len(f.records()); got != recorded {
		t.Errorf("the refused binding recorded %d key(s), want none: the key it would have minted is one §8's list does not hold", got-recorded)
	}
	if got := f.revocations(); len(got) != 1 {
		t.Errorf("§8's list holds %d entry(ies), want the one the refund wrote: nothing the customer does re-issues the withdrawn licence", len(got))
	}
}

// TestAnUnapprovedAdjustmentWithdrawsNothing: Paddle reports an adjustment
// before it decides it — one arrives `pending`, and a `rejected` one never moves
// anything — and only the status that says the money went back is a withdrawal.
// Withdrawing on any other status kills a customer's key for a refund that did
// not happen, and nothing undoes it, because the revocation list refuses to
// change an entry it holds (D2131). The body is stored as F16 requires either
// way, so operations can see it.
func TestAnUnapprovedAdjustmentWithdrawsNothing(t *testing.T) {
	f := newFixture(t)
	bought := f.purchase("evt-1", "acme.example", 3, 30)

	for _, status := range []string{"pending", "rejected"} {
		e := Event{
			ID: "evt-" + status, Type: EventRefund, OccurredAt: f.now,
			Subscription: "sub-acme.example",
		}
		body := unapprovedAdjustmentBody(t, e, status)
		if rec := f.deliverRaw(body, signEvent(f.secret, f.now, body)); rec.Code != http.StatusOK {
			t.Fatalf("the %s adjustment was answered %d, want 200: it is stored and left, not refused", status, rec.Code)
		}
		held := f.stored(t, e.ID)
		if held.State != ledgerStored {
			t.Errorf("the %s adjustment is stored as %q, want %q: no money has gone back, so nothing is applied", status, held.State, ledgerStored)
		}
		if held.Body != string(body) {
			t.Errorf("the %s adjustment is stored as %q, want the body as it arrived", status, held.Body)
		}
		if held.EntitlementID != nil {
			t.Errorf("the %s adjustment resolved to the licence %q, want none", status, *held.EntitlementID)
		}
		if _, held, _ := f.store.Revocation(context.Background(), bought.KeyID); held {
			t.Errorf("the %s adjustment withdrew the key %s, want the licence left alone: no money went back", status, bought.KeyID)
		}
	}
	if got := f.revocations(); len(got) != 0 {
		t.Errorf("the unapproved adjustments wrote %d revocation(s), want none", len(got))
	}
	if got := f.ledger(t); len(got) != 1 {
		t.Errorf("the ledger holds %d line(s), want the purchase's one: an adjustment whose money never moved is not a line", len(got))
	}
}

// TestARefundRevokesTheLicenceItsTransactionBought: F16 — a refund resolves to
// the licence **the money belongs to**, which is the licence that transaction
// bought and not the workspace's current one. A workspace whose licence has been
// replaced holds more than one licence over time, and withdrawing the newest
// would take a licence this money never paid for while the refunded one stayed
// live — a withdrawal nothing undoes, because the revocation list only grows
// (D2131, D2204).
func TestARefundRevokesTheLicenceItsTransactionBought(t *testing.T) {
	f := newFixture(t)
	refunded := f.purchase("evt-1", "acme.example", 3, 30)
	// A second sale for the same workspace replaces the first licence (D2204),
	// and the licence it replaces keeps the key its own binding minted.
	f.event(Event{
		ID: "evt-2", Type: EventPurchase, Subscription: "sub-second.example",
		Organisation: "acme.example", Seats: 3, TermDays: 30,
	})
	current := f.bind("acme.example", "sub-second.example")
	if current.KeyID == refunded.KeyID {
		t.Fatalf("the second binding issued the key %s, want a key of its own", refunded.KeyID)
	}

	// The refund names the first transaction alone: an adjustment against a
	// one-off transaction names no subscription, and the checkout's own record
	// is the step from it to the licence it bought.
	f.advance(time.Hour)
	f.deliver(Event{
		ID: "evt-3", Type: EventRefund, OccurredAt: f.now,
		Transaction: "txn-sub-acme.example",
	})
	if _, held, _ := f.store.Revocation(context.Background(), refunded.KeyID); !held {
		t.Errorf("the refund of the first transaction left the key %s live, want the licence that transaction bought withdrawn", refunded.KeyID)
	}
	if _, held, _ := f.store.Revocation(context.Background(), current.KeyID); held {
		t.Errorf("the refund of the first transaction withdrew the key %s, want the workspace's current licence left alone: it is not the licence this money bought", current.KeyID)
	}
	if got := f.revocations(); len(got) != 1 {
		t.Errorf("the refund wrote %d revocation(s), want the one the refunded licence's key", len(got))
	}
}

// TestARefundBeforeItsPurchaseIsRetried: the merchant's deliveries are not
// ordered, and a refund can arrive before the purchase it belongs to has been
// applied — that delivery may be the very one the merchant is still retrying.
// The checkout's own record is what admits the refund, and it is also what
// resolves it to a licence, which is not written yet. Nothing is withdrawn at
// that moment and the licence is still coming, so the event is refused (a `500`)
// rather than marked processed: a state saying it was applied short-circuits
// every redelivery of it, and the purchase's own retry would then go on to grant
// an active licence for money that has gone back (F16, F29).
func TestARefundBeforeItsPurchaseIsRetried(t *testing.T) {
	f := newFixture(t)
	purchase := f.claimsOf(purchaseEvent("evt-1", "acme.example", 3, 30))
	purchase.OccurredAt = f.now
	refund := Event{
		ID: "evt-2", Type: EventRefund, OccurredAt: f.now,
		Transaction: purchase.Transaction,
	}

	if rec := f.deliver(refund); rec.Code != http.StatusInternalServerError {
		t.Errorf("a refund that arrived before its purchase was answered %d, want 500: the merchant's retry is what carries it to the licence it withdraws", rec.Code)
	}
	if got := f.stored(t, "evt-2").State; got != ledgerStored {
		t.Errorf("the early refund reads %q, want %q: nothing was applied, so nothing is processed", got, ledgerStored)
	}
	if got := f.entitlements(t); len(got) != 0 {
		t.Fatalf("the early refund left %d licence(s), want none: the purchase it is about has not been applied", len(got))
	}

	if rec := f.event(purchase); rec.Code != http.StatusOK {
		t.Fatalf("the purchase was answered %d, want 200", rec.Code)
	}
	licence := f.onlyEntitlement(t)
	if licence.Status != entitlementActive {
		t.Fatalf("the purchase left the licence %q, want %q", licence.Status, entitlementActive)
	}
	if rec := f.deliver(refund); rec.Code != http.StatusOK {
		t.Fatalf("the redelivered refund was answered %d, want 200: the licence it resolves to is in the store now", rec.Code)
	}
	withdrawn, held, err := getRecord[WireEntitlement](context.Background(), f.docs, entitlementsCollection, licence.ID)
	if err != nil || !held {
		t.Fatalf("reading the licence back (held=%v, err=%v)", held, err)
	}
	if withdrawn.Status != entitlementRevoked {
		t.Errorf("the licence reads %q after the refund's redelivery, want %q: the money went back", withdrawn.Status, entitlementRevoked)
	}
	if got := f.stored(t, "evt-2").State; got != ledgerProcessed {
		t.Errorf("the redelivered refund reads %q, want %q", got, ledgerProcessed)
	}
}

// TestPurchaseCreatesAnEntitlement: D2196 — a purchase grants a licence and
// mints nothing. The grant carries the row the term is sold under, the seats,
// the workspace the money was for and the term the customer bought, running from
// the payment's own instant (D2197); the store records the subscription the
// merchant's later events are about and what it began with; and the buyer is
// mailed the portal's address.
func TestPurchaseCreatesAnEntitlement(t *testing.T) {
	f := newFixture(t)
	f.boughtEvent(t, purchaseEvent("evt-1", "acme.example", 5, 365))

	if got := f.mintedKeys(); len(got) != 0 {
		t.Errorf("the purchase minted %d key(s), want none: the key is minted at the binding (D2196)", len(got))
	}
	if got := f.records(); len(got) != 0 {
		t.Errorf("the purchase recorded %d key(s), want none", len(got))
	}
	licence := f.onlyEntitlement(t)
	if licence.OrganisationID != "acme.example" {
		t.Errorf("the licence is for the organisation %q, want the checkout's own", licence.OrganisationID)
	}
	if licence.WorkspaceID == nil || *licence.WorkspaceID != workspaceID {
		t.Errorf("the licence is for the workspace %v, want the one the money was for (%s)", licence.WorkspaceID, workspaceID)
	}
	if licence.Source != entitlementSourcePurchase || licence.Row != PolicyTeamAnnual || licence.Seats != 5 {
		t.Errorf("the licence is %+v, want a purchase of 5 seats on the %s row", licence, PolicyTeamAnnual)
	}
	if !licence.StartsAt.Equal(f.now) || !licence.EndsAt.Equal(f.now.Add(365*24*time.Hour)) {
		t.Errorf("the licence runs %s to %s, want the year the customer bought from the payment's own instant", licence.StartsAt, licence.EndsAt)
	}
	if licence.BoundAt != nil {
		t.Errorf("the purchase bound the licence at %s, want it unbound: binding is the customer's own act and the key is minted there", *licence.BoundAt)
	}
	if licence.Status != entitlementActive {
		t.Errorf("the licence is %q, want %q", licence.Status, entitlementActive)
	}

	sub, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("the store holds no subscription for the purchase (held=%v, err=%v)", held, err)
	}
	if sub.Seats != 5 || sub.TermDays != 365 || sub.Email != buyerAddress("acme.example") || sub.WorkspaceID != workspaceID {
		t.Errorf("the subscription is %+v, want the seats, term, address and workspace the event states", sub)
	}
	if !sub.Expires.Equal(licence.EndsAt) {
		t.Errorf("the subscription ends at %s and the licence at %s, want the one term the money bought", sub.Expires, licence.EndsAt)
	}
	origin, held, err := f.svc.origin(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("the store holds no origin for the subscription (held=%v, err=%v)", held, err)
	}
	if origin.EntitlementID != licence.ID || origin.TransactionID != "txn-sub-acme.example" {
		t.Errorf("the origin is %+v, want the transaction the API created and the licence it bought", origin)
	}

	if got := len(f.mail.sent); got != 1 {
		t.Fatalf("the purchase mailed %d message(s), want one: the buyer is told where the licence is", got)
	}
	if want := intakeOrigin + portalWorkspacesPath; !strings.Contains(f.mail.sent[0].Body, want) {
		t.Errorf("the purchase's mail reads %q, want it to name the portal at %s", f.mail.sent[0].Body, want)
	}
}

// TestUndecodableEventIsKeptAndRetried: F16 — a body that verified and does not
// decode is stored under the digest of itself and answered 400, so the merchant
// sends it again and a person can see what arrived. A redelivery of the same
// bytes is one record; different bytes are a record of their own; and none of
// them makes a line of the ledger.
func TestUndecodableEventIsKeptAndRetried(t *testing.T) {
	f := newFixture(t)
	bodies := []string{
		"not a notification at all",
		`[{"event_type":"transaction.completed"}]`,
		`{"event_id":"evt_1","data":{}}`,
		`{"event_type":"transaction.completed","data":{}}`,
	}
	for _, text := range bodies {
		body := []byte(text)
		header := signEvent(f.secret, f.now, body)
		if rec := f.deliverRaw(body, header); rec.Code != http.StatusBadRequest {
			t.Fatalf("the body %q was answered %d, want 400: the merchant's retry is what re-reads it", text, rec.Code)
		}
		held := f.stored(t, eventDigest(body))
		if held.State != ledgerUndecodable {
			t.Errorf("the body %q is stored as %q, want %q", text, held.State, ledgerUndecodable)
		}
		if held.Body != text {
			t.Errorf("the stored body is %q, want the bytes that arrived (%q)", held.Body, text)
		}
		if held.Notification != "" {
			t.Errorf("the body %q is filed as the notification %q, want none: it named none", text, held.Notification)
		}
		// The same bytes again: one record, and 400 again.
		if rec := f.deliverRaw(body, header); rec.Code != http.StatusBadRequest {
			t.Errorf("the redelivered body %q was answered %d, want 400", text, rec.Code)
		}
	}
	if got := len(f.events(t)); got != len(bodies) {
		t.Errorf("the store holds %d event(s), want one per distinct body: an undecodable body is keyed by its own digest", got)
	}
	if got := f.ledger(t); len(got) != 0 {
		t.Errorf("an undecodable body made %d ledger line(s), want none: no figure is read out of a body that does not decode", len(got))
	}
	if got := f.entitlements(t); len(got) != 0 {
		t.Errorf("an undecodable body granted %d licence(s), want none", len(got))
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("an undecodable body mailed %d message(s), want none", got)
	}
}

// TestForeignTransactionIsParked: F15 — an event whose transaction, and whose
// subscription, did not begin with a transaction this API created is parked for
// operations and not applied. Nothing is granted, nothing is mailed, and the
// body is in the list for a person to attach or dismiss (P5.1).
func TestForeignTransactionIsParked(t *testing.T) {
	f := newFixture(t)
	parked := []Event{
		{
			ID: "evt-1", Type: EventPurchase, OccurredAt: f.now,
			Transaction: "txn-somebody", Subscription: "sub-somebody.example",
			Organisation: "somebody.example", WorkspaceID: workspaceID,
			Email: buyerAddress("somebody.example"), Seats: 5, TermDays: 365,
		},
		{
			ID: "evt-2", Type: EventSeatChange, OccurredAt: f.now,
			Subscription: "sub-somebody.example", Organisation: "somebody.example",
			WorkspaceID: workspaceID, Seats: 9, TermDays: 365,
		},
		{
			ID: "evt-3", Type: EventRefund, OccurredAt: f.now,
			Transaction: "txn-somebody", Subscription: "sub-somebody.example",
			Organisation: "somebody.example",
		},
	}
	for _, one := range parked {
		body := paddleBody(t, one)
		if rec := f.deliverRaw(body, signEvent(f.secret, f.now, body)); rec.Code != http.StatusOK {
			t.Fatalf("the %s was answered %d, want 200: a parked event is stored and left, not refused", one.ID, rec.Code)
		}
		held := f.stored(t, one.ID)
		if held.State != ledgerParked {
			t.Errorf("the %s is stored as %q, want %q: neither its transaction nor its subscription began with one this API created", one.ID, held.State, ledgerParked)
		}
		if held.EntitlementID != nil {
			t.Errorf("the %s resolved to the licence %q, want none: nothing was applied", one.ID, *held.EntitlementID)
		}
	}
	if got := f.entitlements(t); len(got) != 0 {
		t.Errorf("a parked event granted %d licence(s), want none", len(got))
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("a parked event mailed %d message(s), want none", got)
	}
	if _, held, _ := f.store.Subscription(context.Background(), "sub-somebody.example"); held {
		t.Error("a parked event wrote a subscription, want nothing applied")
	}
	if got := f.ledger(t); len(got) != 0 {
		t.Errorf("a parked event made %d ledger line(s), want none: the money is the merchant's and not this portal's, so it is not a figure a report of this portal's sales adds up", len(got))
	}
}

// TestRenewalOfAnAPISubscriptionIsAdmitted: F15's other half — a renewal and a
// proration are Paddle's own acts on a subscription this API began, and what
// admits them is that subscription's origin and not a checkout record. The term
// the money bought is added to the licence that is already there, the customer's
// key stays the key of that licence, and no second notice is mailed.
func TestRenewalOfAnAPISubscriptionIsAdmitted(t *testing.T) {
	f := newFixture(t)
	f.boughtEvent(t, purchaseEvent("evt-1", "acme.example", 5, 365))
	licence := f.onlyEntitlement(t)
	before, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("the purchase wrote no subscription (held=%v, err=%v)", held, err)
	}

	f.advance(365 * 24 * time.Hour)
	// The renewal is a second transaction on the same subscription, and Paddle
	// created it: there is no checkout record for it, and the only thing behind
	// it is the subscription this API began.
	renewal := Event{
		ID: "evt-2", Type: EventPurchase, OccurredAt: f.now,
		Transaction: "txn-renewal", Subscription: "sub-acme.example",
		Organisation: "acme.example", WorkspaceID: workspaceID,
		Email: buyerAddress("acme.example"), Seats: 5, TermDays: 365,
	}
	body := renewalBody(t, renewal)
	if rec := f.deliverRaw(body, signEvent(f.secret, f.now, body)); rec.Code != http.StatusOK {
		t.Fatalf("the renewal of an API-created subscription was answered %d, want 200: %s", rec.Code, rec.Body)
	}
	recorded := f.stored(t, "evt-2")
	if recorded.State != ledgerProcessed {
		t.Errorf("the renewal is stored as %q, want %q: it is admitted by the subscription the API began", recorded.State, ledgerProcessed)
	}

	after, _, _ := f.store.Subscription(context.Background(), "sub-acme.example")
	if !after.Expires.After(before.Expires) {
		t.Errorf("the renewal left the subscription ending at %s, want it past %s", after.Expires, before.Expires)
	}
	if after.Cancelled {
		t.Error("the renewal cancelled the subscription, want it left as it was")
	}
	// The licence is the same one, with the term the money bought added to it.
	// A renewal that granted a second licence would supersede the first (D2204)
	// and leave the key the customer holds attached to a grant the portal no
	// longer shows as theirs.
	if got := f.entitlements(t); len(got) != 1 {
		t.Fatalf("the renewal left %d licence(s), want the one the customer already holds", len(got))
	}
	moved := f.onlyEntitlement(t)
	if moved.ID != licence.ID {
		t.Errorf("the renewal is on the licence %s, want the one the purchase granted (%s)", moved.ID, licence.ID)
	}
	if !moved.EndsAt.Equal(f.now.Add(365 * 24 * time.Hour)) {
		t.Errorf("the licence now ends at %s, want the term the renewal paid for (%s)", moved.EndsAt, f.now.Add(365*24*time.Hour))
	}
	if got := len(f.mail.sent); got != 1 {
		t.Errorf("the renewal mailed %d message(s) in all, want the purchase's one: the customer has been told about this licence already", got)
	}
	entries := f.ledger(t)
	if len(entries) != 2 {
		t.Fatalf("the purchase and the renewal made %d ledger line(s), want two", len(entries))
	}
	if entries[1].Kind != "renewal" {
		t.Errorf("the renewal's line is a %q, want a renewal: a report is asked what was sold and what was renewed", entries[1].Kind)
	}
	if entries[1].EntitlementID == nil || *entries[1].EntitlementID != licence.ID {
		t.Errorf("the renewal's line names the licence %v, want the one the money extended (%s)", entries[1].EntitlementID, licence.ID)
	}
}

// TestAnUnsoldStatedPeriodKeepsTheSubscriptionsOwnTerm: a transaction Paddle
// creates on a subscription it already bills states a period of its own, and a
// proration's period is one the checkout sells no row for. What the customer
// bought is the row the subscription stands on, so the record keeps that row's
// term and not the period the one event stated — a record left holding a term
// nobody sells is one every later event is refused for good from, and the
// customer's licence is never served again (D442).
func TestAnUnsoldStatedPeriodKeepsTheSubscriptionsOwnTerm(t *testing.T) {
	f := newFixture(t)
	f.boughtEvent(t, purchaseEvent("evt-1", "acme.example", 5, 365))

	proration := Event{
		ID: "evt-2", Type: EventPurchase, OccurredAt: f.now,
		Transaction: "txn-proration", Subscription: "sub-acme.example",
		Organisation: "acme.example", WorkspaceID: workspaceID,
		Email: buyerAddress("acme.example"), Seats: 5, TermDays: 7,
	}
	body := renewalBody(t, proration)
	if rec := f.deliverRaw(body, signEvent(f.secret, f.now, body)); rec.Code != http.StatusOK {
		t.Fatalf("the proration stating an unsold period was answered %d, want 200: %s", rec.Code, rec.Body)
	}
	sub, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("reading the subscription back (held=%v, err=%v)", held, err)
	}
	if sub.TermDays != 365 {
		t.Errorf("the subscription stands for %d day(s), want the 365 its purchase bought: a period no row sells is read against the row it was bought on (D442)", sub.TermDays)
	}

	// And the licence the customer holds is one the portal can still serve: an
	// ordinary seat change is minted under the subscription's own term.
	f.advance(24 * time.Hour)
	seatChange := f.event(Event{ID: "evt-3", Type: EventSeatChange, Organisation: "acme.example", Seats: 9})
	if seatChange.Code != http.StatusOK {
		t.Fatalf("a seat change after an unsold stated period was answered %d, want 200: the subscription's own term is what its events are served under", seatChange.Code)
	}
	if got := len(f.records()); got != 1 {
		t.Errorf("the seat change recorded %d key(s), want the one it reissued", got)
	}
}

// TestUnverifiedBodyIsNeverStored: F16's round 2 — the store is written *after*
// verification, so a stranger who can post bodies cannot pre-store a forged one
// under a real event id and have the real delivery read as a redelivery of it.
// Nothing of an unverified body reaches the store, not even as an undecodable
// record.
func TestUnverifiedBodyIsNeverStored(t *testing.T) {
	f := newFixture(t)
	// A real event id, which is what the forgery is aimed at.
	e := f.claimsOf(purchaseEvent("evt-1", "acme.example", 5, 365))
	e.OccurredAt = f.now
	body := paddleBody(t, e)
	for _, tc := range []struct {
		name   string
		body   []byte
		header string
	}{
		{name: "no signature at all", body: body, header: ""},
		{name: "another deployment's secret", body: body, header: signEvent([]byte(otherDeploySecret), f.now, body)},
		{name: "a signature over another body", body: body, header: signEvent(f.secret, f.now, append(append([]byte(nil), body...), ' '))},
		{name: "a header that is not the merchant's scheme", body: body, header: "h1=deadbeef"},
		{name: "an undecodable body with no signature", body: []byte("{}"), header: ""},
	} {
		rec := f.deliverRaw(tc.body, tc.header)
		if rec.Code != http.StatusOK {
			t.Errorf("%s was answered %d, want 200: a forger learns nothing from the answer", tc.name, rec.Code)
		}
	}
	if got := f.events(t); len(got) != 0 {
		t.Errorf("an unverified delivery stored %d event(s), want none: no forged body can be pre-stored under a real event id", len(got))
	}
	if got := f.entitlements(t); len(got) != 0 {
		t.Errorf("an unverified delivery granted %d licence(s), want none", len(got))
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("an unverified delivery mailed %d message(s), want none", got)
	}

	// The merchant's own signature over the same bytes, and the event lands.
	f.deliverRaw(body, signEvent(f.secret, f.now, body))
	if got := f.events(t); len(got) != 1 {
		t.Errorf("the signed event stored %d event(s), want one", len(got))
	}
	if got := f.entitlements(t); len(got) != 1 {
		t.Errorf("the signed event granted %d licence(s), want one: the refusals above were the signature and not the event", len(got))
	}
}

// TestLedgerDerivesFromStoredEvents: the ledger is a reading of the events and
// of nothing else — one line per sale the portal applied, walkable back to the
// body Paddle signed, with the money signed the way a report adds it up and the
// licence it bought named.
func TestLedgerDerivesFromStoredEvents(t *testing.T) {
	f := newFixture(t)
	f.boughtEvent(t, purchaseEvent("evt-1", "acme.example", 5, 365))
	licence := f.onlyEntitlement(t)

	f.advance(365 * 24 * time.Hour)
	renewal := Event{
		ID: "evt-2", Type: EventPurchase, OccurredAt: f.now,
		Transaction: "txn-renewal", Subscription: "sub-acme.example",
		Organisation: "acme.example", WorkspaceID: workspaceID,
		Seats: 5, TermDays: 365,
	}
	body := renewalBody(t, renewal)
	f.deliverRaw(body, signEvent(f.secret, f.now, body))

	f.advance(24 * time.Hour)
	f.event(Event{
		ID: "evt-3", Type: EventRefund, OccurredAt: f.now,
		Organisation: "acme.example", Subscription: "sub-acme.example", Seats: 5,
	})

	entries := f.ledger(t)
	if len(entries) != 3 {
		t.Fatalf("the ledger holds %d line(s), want the purchase, the renewal and the refund", len(entries))
	}
	for i, want := range []struct {
		id     string
		kind   string
		micros int64
	}{
		{id: "evt-1", kind: "purchase", micros: 120000000},
		{id: "evt-2", kind: "renewal", micros: 120000000},
		{id: "evt-3", kind: "refund", micros: -120000000},
	} {
		line := entries[i]
		if line.ID != want.id || line.Kind != want.kind || line.AmountMicros != want.micros {
			t.Errorf("the ledger's line %d is %+v, want the %s event's %s of %d micro-dollars", i, line, want.id, want.kind, want.micros)
		}
		if line.Currency != "USD" || line.Seats != 5 {
			t.Errorf("the ledger's line %d is %+v, want the merchant's own currency and the seats sold", i, line)
		}
		if line.Channel != "direct" {
			t.Errorf("the ledger's line %d is a %q sale, want a direct one: a merchant event is a sale the customer made to Fugaro", i, line.Channel)
		}
		if line.OrganisationID != "acme.example" {
			t.Errorf("the ledger's line %d is for %q, want the organisation the money was for", i, line.OrganisationID)
		}
		if line.WorkspaceID == nil || *line.WorkspaceID != workspaceID {
			t.Errorf("the ledger's line %d names the workspace %v, want %s", i, line.WorkspaceID, workspaceID)
		}
		if line.EntitlementID == nil || *line.EntitlementID != licence.ID {
			t.Errorf("the ledger's line %d names the licence %v, want %s", i, line.EntitlementID, licence.ID)
		}
	}
	var total int64
	for _, line := range entries {
		total += line.AmountMicros
	}
	if total != 120000000 {
		t.Errorf("the ledger adds up to %d micro-dollars, want the one term still standing (120000000)", total)
	}

	// Every line is a stored event, and the figure is that event's own reading:
	// the body a report walks back to is the one the merchant signed, and
	// nothing in the ledger is kept beside the body it came from.
	held := f.stored(t, "evt-1")
	var read map[string]any
	if err := json.Unmarshal([]byte(held.Body), &read); err != nil {
		t.Fatalf("reading the stored body: %v", err)
	}
	if read["event_type"] != paddleTransactionCompleted {
		t.Errorf("the stored body is the merchant's %v, want the notification it arrived as", read["event_type"])
	}

	// An event the portal did not apply makes no line: the ledger is the sales
	// this portal made, and a purchase it parked is one it did not make. The body
	// goes in unclaimed, because a checkout record for its transaction is exactly
	// what would admit it.
	f.advance(time.Hour)
	parked := Event{
		ID: "evt-4", Type: EventPurchase, OccurredAt: f.now,
		Transaction: "txn-somebody", Subscription: "sub-somebody.example",
		Organisation: "somebody.example", WorkspaceID: workspaceID,
		Email: buyerAddress("somebody.example"), Seats: 5, TermDays: 365,
	}
	parkedBody := paddleBody(t, parked)
	f.deliverRaw(parkedBody, signEvent(f.secret, f.now, parkedBody))
	if got := f.ledger(t); len(got) != 3 {
		t.Errorf("the ledger holds %d line(s) after a parked purchase, want the three the portal applied", len(got))
	}
	// And the parked body is in the store beside them, which is what lets a
	// person see what arrived (P5.1).
	if got := f.stored(t, "evt-4"); got.State != ledgerParked {
		t.Errorf("the parked purchase is stored as %q, want %q", got.State, ledgerParked)
	}
}

// TestPurchaseMailCarriesNoKey: F3 and D2182 — the purchase's notice names the
// portal and carries no key, because there is none: the key is minted at the
// binding. A notice that carried one would be a key mailed to an address nobody
// has installed anything at.
func TestPurchaseMailCarriesNoKey(t *testing.T) {
	f := newFixture(t)
	f.boughtEvent(t, purchaseEvent("evt-1", "acme.example", 5, 365))

	if got := f.mintedKeys(); len(got) != 0 {
		t.Fatalf("the purchase minted %d key(s), want none: there is no key for the notice to carry", len(got))
	}
	if got := len(f.mail.sent); got != 1 {
		t.Fatalf("the purchase mailed %d message(s), want one", got)
	}
	msg := f.mail.sent[0]
	if msg.To != buyerAddress("acme.example") {
		t.Errorf("the notice goes to %q, want the buyer's own address", msg.To)
	}
	if msg.Subject != licenceSubject {
		t.Errorf("the notice's subject is %q, want the licence notice's own (%q)", msg.Subject, licenceSubject)
	}
	link := intakeOrigin + portalWorkspacesPath
	if !strings.Contains(msg.Body, link) {
		t.Errorf("the notice reads %q, want it to name the portal at %s: the portal is where the licence is bound and the key is issued", msg.Body, link)
	}
	if !strings.Contains(msg.Body, "No key is sent by mail") {
		t.Errorf("the notice reads %q, want it to say no key is sent by mail", msg.Body)
	}
	if strings.Contains(msg.Body, keyIDPrefix) {
		t.Errorf("the notice reads %q, want no key id in it: the customer's key does not exist yet", msg.Body)
	}

	// And a second purchase for another organisation mails its own buyer, which
	// is what keeps the notice from being a broadcast.
	f.boughtEvent(t, purchaseEvent("evt-2", "widget.example", 3, 30))
	if got := len(f.mail.sent); got != 2 {
		t.Fatalf("the two purchases mailed %d message(s), want two", got)
	}
	if f.mail.sent[1].To != buyerAddress("widget.example") {
		t.Errorf("the second notice goes to %q, want the second buyer's own address", f.mail.sent[1].To)
	}
	if !strings.Contains(f.mail.sent[1].Body, link) {
		t.Errorf("the second notice reads %q, want it to name the portal too", f.mail.sent[1].Body)
	}
}
