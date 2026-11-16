// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The adapter from Paddle's notification body to this service's canonical event
// (portal plan P4.2; F16, F15).
//
// Paddle Billing's webhook body is not this service's shape and cannot be: it is
// the merchant's, it changes when the merchant changes it, and a strict decode
// of it here is a decode that fails the day a field is added. So the body is
// read as what it is — Paddle's own JSON, decoded leniently, every field
// optional — and mapped to the `Event` the paid path has always been driven by.
// That is F16's whole finding: the intake decoded the canonical shape, so a real
// notification was answered 200 and dropped.
//
// **A refund resolves to the entitlement, never to a key.** Paddle sends no key
// id and never has (F16): what its adjustment names is the transaction and the
// subscription, and what this service withdraws is the licence those belong to
// — `merchant.go`'s `withdrawn` walks from the entitlement, which is the shape
// D2196 gives a licence.
//
// **The window.** The signature's timestamp is checked against a five-minute
// window, and that reverses design §5.2's deliberate choice to let the event id
// handle replay (the reconciliation's F16, round 3). It is safe only because
// Paddle signs every delivery with a timestamp of its own, including every
// retry, so a fresh `ts` is the ordinary case and a captured body replayed later
// is not; the window is what keeps a captured body from being replayed at all,
// and the event id's idempotency is still there underneath it.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DeliveryWindow is how long a delivery's own timestamp may be from this
// service's clock, either way: five minutes, as the reconciliation's F16 fixes
// it.
//
// Both directions are refused and not only the stale one. A delivery stamped in
// the future is a clock that disagrees with this service's — the merchant's, or
// an attacker's — and admitting it would let a body captured now be replayed for
// as long as the stamp was ahead. Five minutes is wide enough for the two clocks
// and for an ordinary retry, and narrow enough that a captured body is worth
// nothing.
const DeliveryWindow = 5 * time.Minute

// ErrStale is a delivery whose own timestamp is outside the window: a body
// replayed later, or one signed by a clock this service cannot agree with. It is
// answered like a bad signature — 200 and no body — because the merchant must
// not be told which of its events a stranger could replay.
var ErrStale = errors.New("keyservice: the merchant's signature is outside the delivery window")

// The Paddle notification types this adapter reads, and what each becomes. They
// are the merchant's own names and they are matched literally: a type this list
// does not hold is an event this service does not act on, which is stored and
// answered 200 rather than refused — an event the merchant adds later is not a
// reason to make it retry forever.
const (
	paddleTransactionCompleted = "transaction.completed"
	paddleSubscriptionUpdated  = "subscription.updated"
	paddleSubscriptionCanceled = "subscription.canceled"
	paddleAdjustmentCreated    = "adjustment.created"
	paddleAdjustmentUpdated    = "adjustment.updated"
)

// Paddle's own words for what an adjustment is, read only to tell a withdrawal
// from everything else. A reversal — the money coming back to Fugaro after a
// chargeback or a refund was undone — is deliberately not one of them: it is not
// this service's to act on until the licensor rules what it should do, and
// acting on it as a withdrawal would revoke a licence the customer has paid for.
const (
	paddleActionRefund     = "refund"
	paddleActionChargeback = "chargeback"
)

// paddleNotification is one Paddle Billing notification, decoded leniently:
// every field is optional, an unknown one is ignored, and the fields this
// service reads are read by their Paddle names.
//
// `event_id` and `notification_id` are two generations of Paddle's own name for
// the event, and `id` is the third spelling a subscription or a transaction
// event carries; all three are read, in that order, because the id is what the
// body is stored under and an event stored under no id is one a redelivery
// cannot find (F16).
type paddleNotification struct {
	EventID        string     `json:"event_id"`
	NotificationID string     `json:"notification_id"`
	ID             string     `json:"id"`
	EventType      string     `json:"event_type"`
	OccurredAt     time.Time  `json:"occurred_at"`
	Data           paddleData `json:"data"`
}

// paddleData is the notification's payload. One shape covers a transaction, a
// subscription and an adjustment, because Paddle's three overlap: which of the
// three a body is about is told by `event_type`, and a field that is not there
// is empty.
type paddleData struct {
	ID             string            `json:"id"`
	Status         string            `json:"status"`
	Origin         string            `json:"origin"`
	Action         string            `json:"action"`
	CustomerID     string            `json:"customer_id"`
	SubscriptionID string            `json:"subscription_id"`
	TransactionID  string            `json:"transaction_id"`
	CurrencyCode   string            `json:"currency_code"`
	CustomData     map[string]any    `json:"custom_data"`
	Items          []paddleItem      `json:"items"`
	BillingPeriod  *paddlePeriod     `json:"billing_period"`
	CurrentPeriod  *paddlePeriod     `json:"current_billing_period"`
	Details        paddleTransaction `json:"details"`
	Totals         paddleTotals      `json:"totals"`
}

// paddleTransaction is a transaction's own half of the payload, which is where
// the money is.
type paddleTransaction struct {
	Totals paddleTotals `json:"totals"`
}

// paddleTotals is what Paddle says a transaction or an adjustment came to, in
// the currency's **minor unit** and as a string: `grand_total` is the figure
// including tax, and `total` is the fallback for a payload that carries only it.
type paddleTotals struct {
	GrandTotal string `json:"grand_total"`
	Total      string `json:"total"`
}

// paddleItem is one line of what was sold. The quantity is the seats: the
// checkout sells a seat of one price, and P4.3 grows the subscription by
// changing exactly this.
type paddleItem struct {
	PriceID  string `json:"price_id"`
	Quantity int    `json:"quantity"`
}

// paddlePeriod is a billing period's own bounds, which is where the term in days
// comes from: the merchant bills a calendar month or a year, and the days
// between the two instants are the term the customer bought.
type paddlePeriod struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

// decodeNotification reads one Paddle notification body, or answers that it is
// not one.
//
// It is lenient about fields and strict about the two things that make a body an
// event at all: it has to be a JSON object, and it has to name its own type.
// Everything else — a field Paddle adds, a field it stops sending, a `null`
// where an object was — is absorbed, because the alternative is an intake that
// fails on the day the merchant ships a change. What a body that is not an
// object, or that names no type, gets is this error, and it is the case F16
// makes visible: the body is stored and answered 400, so the merchant retries it
// and operations can look at it.
func decodeNotification(body []byte) (paddleNotification, error) {
	var n paddleNotification
	if err := json.Unmarshal(body, &n); err != nil {
		return paddleNotification{}, err
	}
	if strings.TrimSpace(n.EventType) == "" {
		return paddleNotification{}, errors.New("keyservice: the body names no Paddle event type")
	}
	if n.key() == "" {
		return paddleNotification{}, errors.New("keyservice: the body names no event of Paddle's")
	}
	return n, nil
}

// key is Paddle's own id for this event, in the three spellings its payloads
// have carried, or the empty string for a body that names none.
func (n paddleNotification) key() string {
	for _, candidate := range []string{n.EventID, n.NotificationID, n.ID} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// custom is one value of the transaction's own data, which is where the
// organisation and the workspace a purchase was for are carried (F15, P4.1).
// Only a string is read: the API writes strings, and anything else is a body
// this service did not build.
func (n paddleNotification) custom(field string) string {
	value, found := n.Data.CustomData[field]
	if !found {
		return ""
	}
	text, isText := value.(string)
	if !isText {
		return ""
	}
	return strings.TrimSpace(text)
}

// transactionID is the merchant's transaction this notification is about: an
// adjustment names the transaction it is against, and a transaction event is
// about itself.
func (n paddleNotification) transactionID() string {
	if n.Data.TransactionID != "" {
		return n.Data.TransactionID
	}
	switch n.EventType {
	case paddleTransactionCompleted:
		return n.Data.ID
	}
	return ""
}

// subscriptionID is the merchant's subscription this notification is about, and
// it is what a renewal, a proration, a seat change and a cancellation are all
// matched to a licence by.
func (n paddleNotification) subscriptionID() string {
	if n.Data.SubscriptionID != "" {
		return n.Data.SubscriptionID
	}
	switch n.EventType {
	case paddleSubscriptionUpdated, paddleSubscriptionCanceled:
		return n.Data.ID
	}
	return ""
}

// seats is how many seats the notification is about, which is what was sold on
// the transaction's own lines.
func (n paddleNotification) seats() int {
	total := 0
	for _, item := range n.Data.Items {
		total += item.Quantity
	}
	return total
}

// termDays is the term the notification covers, in whole days, from the billing
// period it states. A body that states no period answers zero, and zero is a
// term no row is sold for — so it is refused where a term is needed rather than
// guessed at from the row the customer bought (D442).
func (n paddleNotification) termDays() int {
	period := n.Data.BillingPeriod
	if period == nil {
		period = n.Data.CurrentPeriod
	}
	if period == nil {
		return 0
	}
	return daysBetween(period.StartsAt, period.EndsAt)
}

// totals is the money the notification is about: a transaction's own details
// where it has them, and the payload's top-level totals where it is an
// adjustment.
func (n paddleNotification) totals() paddleTotals {
	if n.Data.Details.Totals.GrandTotal != "" || n.Data.Details.Totals.Total != "" {
		return n.Data.Details.Totals
	}
	return n.Data.Totals
}

// grandTotalMicros is the money this notification moves, in micro-dollars and
// signed the way a ledger adds it up: a refund and a chargeback are negative,
// because the money went the other way.
//
// Paddle states an amount in the currency's minor unit and as a string, so the
// multiplication into micro-dollars is whole-number arithmetic and never a
// float (wire_ledger.go's unit). A figure that is not a number, or one absurdly
// large for a sale, is answered zero rather than wrapped: a ledger line with a
// wrong sign or a wrapped total is worse than one with no figure, and the body
// it came from is stored either way.
func (n paddleNotification) grandTotalMicros() int64 {
	stated := n.totals().GrandTotal
	if stated == "" {
		stated = n.totals().Total
	}
	minor, err := strconv.ParseInt(strings.TrimSpace(stated), 10, 64)
	if err != nil || minor > 1<<40 || minor < -(1<<40) {
		return 0
	}
	micros := minor * 10000
	if kind, _ := ledgerKind(n); kind == "refund" || kind == "chargeback" {
		return -micros
	}
	return micros
}

// canonicalEvent maps one notification to the event the paid path is driven by,
// and answers false for a notification this service does not act on.
//
// The mapping is one-way and total: every field the canonical event carries is
// filled from the merchant's own body, and a field the body does not carry is
// left empty so that the paid path's own refusals — a purchase with no term, an
// event with no instant — are what answer it, rather than this function guessing
// (ErrNoClaims, ErrNoInstant).
//
// The mode is the deployment's and never the body's: a notification carries no
// such thing, and which of the two pairs an event is acted on by is what the
// environment the service runs in decides (§5.4).
func canonicalEvent(n paddleNotification, mode Mode) (Event, bool) {
	e := Event{
		ID:           n.key(),
		Mode:         mode,
		OccurredAt:   n.OccurredAt,
		Transaction:  n.transactionID(),
		Subscription: n.subscriptionID(),
		Organisation: n.custom("org_id"),
		WorkspaceID:  n.custom("workspace_id"),
		Seats:        n.seats(),
		TermDays:     n.termDays(),
	}
	switch n.EventType {
	case paddleTransactionCompleted:
		e.Type = EventPurchase
	case paddleSubscriptionUpdated:
		e.Type = EventSeatChange
	case paddleSubscriptionCanceled:
		e.Type = EventCancellation
	case paddleAdjustmentCreated, paddleAdjustmentUpdated:
		switch n.Data.Action {
		case paddleActionRefund:
			e.Type = EventRefund
		case paddleActionChargeback:
			e.Type = EventChargeback
		default:
			return Event{}, false
		}
	default:
		return Event{}, false
	}
	return e, true
}

// ledgerKind is what one notification did to the money, in the ledger's own
// vocabulary (wire_ledger.go's Kind), and false for an event that moved none.
//
// A transaction that renews a subscription is a **renewal** and not a purchase:
// Paddle states the difference in `origin`, and the difference matters to a
// report, which is asked what was sold and what was renewed.
func ledgerKind(n paddleNotification) (string, bool) {
	switch n.EventType {
	case paddleTransactionCompleted:
		if n.Data.Origin == paddleOriginRecurring {
			return "renewal", true
		}
		return "purchase", true
	case paddleAdjustmentCreated, paddleAdjustmentUpdated:
		switch n.Data.Action {
		case paddleActionRefund:
			return "refund", true
		case paddleActionChargeback:
			return "chargeback", true
		}
		return "adjustment", true
	}
	return "", false
}

// paddleOriginRecurring is Paddle's own word for a transaction it created to
// renew a subscription it already bills: what tells a renewal from a purchase.
const paddleOriginRecurring = "subscription_recurring"

// VerifyDelivery checks one delivery before anything is done with it: the
// signature over the raw body, and the timestamp's own age.
//
// The signature is checked first, and the order is the point: a stranger posting
// bodies at this route learns nothing from an answer, and a body this service
// did not verify never reaches the store (F16's round 2 — the body is stored
// *after* verification, so no forged body can be pre-stored under a real event
// id).
func VerifyDelivery(secret []byte, header string, body []byte, now time.Time) error {
	at, err := verifySignature(secret, header, body)
	if err != nil {
		return err
	}
	if age := now.Sub(at); age > DeliveryWindow || age < -DeliveryWindow {
		return fmt.Errorf("%w: the delivery is stamped %s and this service's clock reads %s", ErrStale, at.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	return nil
}

// verifySignature is the merchant's own scheme over the raw body, and it answers
// the instant the header's timestamp names as well as the error: Paddle signs
// `ts=<unix>;h1=<hex>`, where the digest is HMAC-SHA256 of `ts:body` under the
// deployment's signing secret.
func verifySignature(secret []byte, header string, body []byte) (time.Time, error) {
	if len(secret) == 0 {
		return time.Time{}, errors.New("the merchant's signing secret is not configured, so no body can be verified and none is read")
	}
	stamp, signature := "", ""
	for _, part := range strings.Split(header, ";") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch name {
		case "ts":
			if stamp == "" {
				stamp = value
			}
		case "h1":
			if signature == "" {
				signature = value
			}
		}
	}
	if stamp == "" || signature == "" {
		return time.Time{}, fmt.Errorf("%w: the header carries no ts and h1", ErrSignature)
	}
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: the timestamp is not a unix second", ErrSignature)
	}
	signed, err := hex.DecodeString(signature)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: the h1 is not hexadecimal", ErrSignature)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(stamp + ":"))
	mac.Write(body)
	if !hmac.Equal(signed, mac.Sum(nil)) {
		return time.Time{}, ErrSignature
	}
	return time.Unix(seconds, 0).UTC(), nil
}
