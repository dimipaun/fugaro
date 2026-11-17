// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The billing mirror, the seat purchase and Paddle's customer portal (portal
// plan P4.3; D2186, D2192, D2197, F20).
//
// **The portal mirrors and never decides.** Paddle is where the subscription
// lives — invoices, payment methods and cancellation are Paddle's own customer
// portal — and what this file answers is what this portal last read from
// Paddle's events (P4.2). It is read out of the records on the way out and never
// kept a second time: every field of the mirror is a field some record this
// service already holds states, and a copy written here would be a second truth
// that only a seat purchase would move — a cancellation, a renewal or a
// proration arrives as a merchant event (P4.2) and would leave the copy saying
// what the subscription was before it.
//
// **The one thing this file moves is the quantity.** A seat purchase asks Paddle
// for the whole quantity the customer named, and the event that carries it back
// is what reissues the key (P3.6, D2200) — which is why the store's own record
// is not written here. `seatChanged` reissues when the event's seats differ from
// the store's, so a store moved ahead of the event would make that reissue a
// no-op and leave a customer who has paid for seven seats holding a key that
// says three.
//
// **The customer is the organisation's and never a request's.** Paddle's
// customer is read off the organisation's own checkout records (P4.1), so a
// path, a body or a header cannot name somebody else's customer — which is the
// whole of what a customer-portal session would be: an open door on another
// organisation's invoices, payment methods and cancellation — and every
// workspace of one organisation answers the same one.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// The states the mirror can report. They are Paddle's own words (Paddle Billing
// spells a cancelled subscription `canceled`), which is what the shape's enum is
// and why the screens show the state as it arrives rather than translating it.
//
// **They are the two this portal's records can tell apart.** What P4.2 keeps of
// a subscription is whether a cancellation was recorded and when its paid term
// ends: `past_due`, `paused` and `trialing` are states of the merchant's
// subscription object that no event this path stores carries, so a mirror that
// answered one of them would be this service guessing at Paddle's word from a
// date. A subscription that reads `active` here and has stopped renewing is one
// whose term has run out, and what says so is `renews_at` being null — the term
// running out is a date and not a cancellation, so `canceled` is answered only
// where a cancellation was recorded.
const (
	subscriptionStateActive   = "active"
	subscriptionStateCanceled = "canceled"
)

// The refusals this area owns, each a sentinel so that the routes answer it with
// the status and the sentence its reader branches on.
var (
	// errNoSuchSubscription is a workspace with nothing bought for it. The
	// mirror has no shape for "no subscription" — a shape with no id and no
	// seats would be one a screen draws as a subscription — so the state is a
	// refusal, and the screen is a person who has not bought yet.
	errNoSuchSubscription = errors.New("keyservice: that workspace holds no subscription")
	// errBillingNotPermitted is a member who is not an owner or an admin (D2186).
	// Changing a subscription's quantity commits the organisation to a charge
	// and Paddle's customer portal is where a payment method is changed and a
	// subscription cancelled, so both are the line between deciding for the
	// organisation and working in it; reading the mirror is not.
	errBillingNotPermitted = errors.New("keyservice: managing a subscription is an owner's or an admin's act")
	// errNoPaddleCustomer is an organisation with no purchase behind it: there is
	// no customer at the merchant of record to mint a session for.
	errNoPaddleCustomer = errors.New("keyservice: that organisation has no customer at the merchant of record")
	// errSeatsBelowOne is a purchase of no seat. The shape's minimum is the
	// handler's check and not `decodeWire`'s (P1.1): the body decodes, and a
	// quantity below one is a request that has nothing to buy.
	errSeatsBelowOne = errors.New("keyservice: a subscription is for one seat or more")
	// errSubscriptionNotRunning is a seat change on a subscription that is not
	// running: cancelled, or past the end of the term it paid for. Nothing would
	// licence the seats it bought — a seat change reissues the key, and a
	// cancelled subscription gets no reissue (merchant.go's `seatChanged`, S2.1)
	// — so the merchant would charge for seats this service never hands over.
	errSubscriptionNotRunning = errors.New("keyservice: that subscription is not running, so its seats cannot be changed")
)

// billingHeld is what every route of this area reads before it answers: the
// workspace the path names, the caller's membership of the organisation that
// owns it, and the subscription that workspace holds.
type billingHeld struct {
	workspace    WireWorkspace
	organisation WireOrganisation
	membership   WireMembership
	subscription Subscription
}

// heldFor is that read, and the one place the two refusals a request earns
// before anything else are decided: a workspace that is not there — or is not
// the caller's, which is the same answer for the reason api_orgs.go's reads are
// (which workspaces exist is a list of the portal's customers) — and a workspace
// whose owner has bought nothing.
//
// The organisation is the workspace's own and never one a request names, and the
// membership is the caller's of that organisation: the same two reads the
// checkout makes (P4.1), because they are the whole of what authorises anything
// here.
func (s *Service) heldFor(ctx context.Context, actor Caller, workspaceID string) (billingHeld, error) {
	workspace, present, err := getRecord[WireWorkspace](ctx, s.docs, workspacesCollection, strings.TrimSpace(workspaceID))
	if err != nil {
		return billingHeld{}, err
	}
	if !present {
		return billingHeld{}, errNoSuchWorkspace
	}
	org, membership, member, err := s.ownMembership(ctx, actor, workspace.OrganisationID)
	if err != nil {
		return billingHeld{}, err
	}
	if !member {
		return billingHeld{}, errNoSuchWorkspace
	}
	sub, err := s.subscriptionOf(ctx, workspace, s.now().UTC())
	if err != nil {
		return billingHeld{}, err
	}
	return billingHeld{workspace: workspace, organisation: org, membership: membership, subscription: sub}, nil
}

// subscriptionOf is the subscription one workspace holds: the running one if it
// holds one, and otherwise the one that ended last.
//
// One workspace has one subscription (D2192), so this is the newest record where
// there is more than one: a workspace whose subscription was cancelled and then
// bought again holds both, and the one a customer's screen is about is the one
// that is running — or, where none is, the one that ended most recently, since
// that is the subscription whose term and cancellation the customer last saw.
//
// The record names the workspace in one of two spellings and **both are read**:
// `Subscription.WorkspaceID` is written from the purchase event's custom_data —
// the portal workspace id the checkout set (P4.1) — and a workspace bound after
// the purchase is named there by the project id instead. The two are names of
// one workspace (checkout.go's `refuseSubscribedWorkspace` reads both for the
// same reason), so a guard reading one spelling alone would answer "no
// subscription" for a workspace that is paying.
//
// **A name is not a tenant, so the organisation is read too.** A project is
// freed by a rebind (workspaces.go) and given to another organisation's
// workspace, while the subscription that still names it is moved by the
// merchant's own events alone (merchant.go): one organisation's subscription can
// therefore name a project another organisation's workspace holds now. Matching
// on the name alone would answer that subscription — and the seat change would
// call the merchant on it, charging the organisation that bought it for seats
// bought by somebody else. The subscription's own organisation is what makes it
// this workspace's.
func (s *Service) subscriptionOf(ctx context.Context, workspace WireWorkspace, now time.Time) (Subscription, error) {
	held, err := s.store.Subscriptions(ctx)
	if err != nil {
		return Subscription{}, err
	}
	project := ""
	if workspace.ProjectID != nil {
		project = strings.TrimSpace(*workspace.ProjectID)
	}
	matching := make([]Subscription, 0, 1)
	for _, sub := range held {
		if sub.Organisation != workspace.OrganisationID {
			continue
		}
		if sub.WorkspaceID == workspace.ID || (project != "" && sub.WorkspaceID == project) {
			matching = append(matching, sub)
		}
	}
	if len(matching) == 0 {
		return Subscription{}, errNoSuchSubscription
	}
	// The store answers its list in no order of its own, so the pick is made
	// here rather than taken from the iteration: the running one first, then the
	// later term, then the id, which is the only tie-break two records of one
	// instant have.
	slices.SortFunc(matching, func(a, b Subscription) int {
		if running := runningSubscription(a, now); running != runningSubscription(b, now) {
			if running {
				return -1
			}
			return 1
		}
		if !a.Expires.Equal(b.Expires) {
			if a.Expires.After(b.Expires) {
				return -1
			}
			return 1
		}
		return strings.Compare(b.ID, a.ID)
	})
	return matching[0], nil
}

// runningSubscription is whether a subscription is one the customer is still
// paying for: not cancelled, and not past the end of the term it has paid for.
// It is the rule the renewal reads (renew.go) and the rule a second checkout is
// refused by (checkout.go), read here for the third time because what it decides
// is the third thing — whether the mirror says the subscription renews.
func runningSubscription(sub Subscription, now time.Time) bool {
	return !sub.Cancelled && now.Before(sub.Expires)
}

// readBilling answers one workspace's subscription as the portal last read it
// from Paddle's events.
func (s *Service) readBilling(ctx context.Context, actor Caller, workspaceID string) (WireSubscription, error) {
	held, err := s.heldFor(ctx, actor, workspaceID)
	if err != nil {
		return WireSubscription{}, err
	}
	return s.mirror(ctx, held.workspace, held.subscription, s.now().UTC())
}

// mirror is one subscription as a billing screen reads it, assembled from the
// records this service holds: the subscription and the origin record the
// merchant's events wrote (P4.2, P3.6), the licence the purchase granted and the
// transaction the checkout opened (P4.1).
//
// Nothing here is arithmetic on what Paddle said. `renews_at` is the end of the
// paid term and never a next date worked out from it — a date this service
// computed would be wrong on the first edge case, and the term's end is the fact
// the customer actually holds — and `starts_at` is the licence's own instant,
// which is the payment's (D2197).
func (s *Service) mirror(ctx context.Context, workspace WireWorkspace, sub Subscription, now time.Time) (WireSubscription, error) {
	answer := WireSubscription{
		ID:             sub.ID,
		OrganisationID: sub.Organisation,
		WorkspaceID:    workspace.ID,
		State:          subscriptionStateActive,
		Seats:          sub.Seats,
		// The price the sale was made at is read from the transaction below.
		// Until it is, this is the price this deployment sells: a checkout sells
		// the one price the deploy is configured with (`checkout.go`), so it is
		// the only other true statement about a subscription whose own
		// transaction record is not there, and it is what keeps the shape's
		// required field answered rather than empty. A deployment that names no
		// price cannot sell at all, and answers the empty string the shape's own
		// minimum refuses.
		PriceID:  s.cfg.PaddlePriceID,
		StartsAt: sub.At,
	}
	// The origin record is what ties a subscription to the two records behind the
	// mirror's other fields: the licence the purchase granted, whose start is the
	// payment's instant, and the transaction the checkout opened, which is where
	// the price was sold. Both are read for **either** state, because a running
	// subscription and an ended one state them the same way — and neither is a
	// field of the store's own record: `sub.At` is the instant of the last event
	// that moved the subscription (a seat change, a renewal, a cancellation,
	// P4.2) and not the instant it began, and the store holds no price at all. A
	// subscription this service has a record of and no purchase behind it is one
	// the mirror still has to draw, so a missing record leaves the field at what
	// the subscription itself states.
	origin, held, err := s.origin(ctx, sub.ID)
	if err != nil {
		return WireSubscription{}, err
	}
	if held {
		grant, found, err := getRecord[WireEntitlement](ctx, s.docs, entitlementsCollection, origin.EntitlementID)
		if err != nil {
			return WireSubscription{}, err
		}
		if found && !grant.StartsAt.IsZero() {
			answer.StartsAt = grant.StartsAt
		}
		bought, found, err := getRecord[checkoutTransaction](ctx, s.docs, checkoutTransactionsCollection, origin.TransactionID)
		if err != nil {
			return WireSubscription{}, err
		}
		if found && strings.TrimSpace(bought.PriceID) != "" {
			answer.PriceID = bought.PriceID
		}
	}
	if runningSubscription(sub, now) {
		renews := sub.Expires
		answer.RenewsAt = &renews
		return answer, nil
	}
	// Nothing renews it, and the records give two different reasons for that —
	// which are two different facts and only one of them is a word Paddle said.
	// A cancellation was recorded (P4.2's `cancellation`): `canceled` is the
	// merchant's own word for that subscription and the store's last event is the
	// instant it happened. Or the paid term ran out and nothing followed it, which
	// is **not** a cancellation — nothing said `canceled`, so the state stays what
	// Paddle last said and the dates are what state the term is over: `renews_at`
	// null, `ends_at` at the end of the term it paid for. Either way the customer
	// keeps that term until it ends.
	ends := sub.Expires
	answer.EndsAt = &ends
	if sub.Cancelled {
		answer.State = subscriptionStateCanceled
		cancelled := sub.At
		answer.CancelledAt = &cancelled
	}
	return answer, nil
}

// buySeats moves one subscription to the quantity a customer named. The quantity
// is the whole and not an increment (the shape's own decision, `WireSeatChange`),
// so a request whose reply was lost is safe to send again — and so is one Paddle
// itself applied twice, which is what makes this route idempotent without a
// record of its own.
//
// It is a change to a subscription that is still running, by the rule the
// renewal reads (renew.go) and a second checkout is refused by (checkout.go):
// what a quantity buys is a licence, and a subscription nothing will reissue is
// one whose seats nobody would hold.
//
// The answer is the mirror with the quantity Paddle has just taken. The event
// that carries it (P3.6) has not arrived when this answers, so the mirror would
// otherwise state the number the customer just replaced.
func (s *Service) buySeats(ctx context.Context, actor Caller, workspaceID string, asked WireSeatChange) (WireSubscription, error) {
	if asked.Seats < 1 {
		return WireSubscription{}, errSeatsBelowOne
	}
	held, err := s.heldFor(ctx, actor, workspaceID)
	if err != nil {
		return WireSubscription{}, err
	}
	if held.membership.Role != roleOwner && held.membership.Role != roleAdmin {
		return WireSubscription{}, errBillingNotPermitted
	}
	// A subscription that is not running is one this service will not licence a
	// change on, and the merchant charges at the moment of the change (Paddle's
	// own proration), so asking it to is a charge for seats that never arrive:
	// the event that carries the new quantity reissues the key, and a cancelled
	// subscription is one `seatChanged` drops (S2.1). The screen offers the
	// quantity form to anyone holding a subscription
	// (`ee/portal/src/screens/billing/billing.tsx` gates it on the subscription
	// existing), so the rule belongs here rather than to the form.
	if !runningSubscription(held.subscription, s.now().UTC()) {
		return WireSubscription{}, errSubscriptionNotRunning
	}
	if err := s.paddle.SetSeats(ctx, held.subscription.ID, asked.Seats); err != nil {
		return WireSubscription{}, fmt.Errorf("the merchant of record did not take the new quantity: %w", err)
	}
	answer, err := s.mirror(ctx, held.workspace, held.subscription, s.now().UTC())
	if err != nil {
		return WireSubscription{}, err
	}
	answer.Seats = asked.Seats
	return answer, nil
}

// customerPortal mints one Paddle customer-portal session for the organisation
// the workspace belongs to, and answers the URL the screen sends a person to.
//
// **It is an act and not a read**, which is why a member is refused it: Paddle's
// portal is where the payment method is changed and the subscription cancelled
// (spec §Customer experience), so the session is the same line D2186 draws for
// the seats. What the screen does with the URL is its own business
// (`ee/portal/src/screens/billing/billing.tsx` follows it only when it is
// https), and the URL that is answered here is checked to be https before it
// leaves: a session this service hands a browser is one it vouches for.
func (s *Service) customerPortal(ctx context.Context, actor Caller, workspaceID string) (WireCustomerPortal, error) {
	held, err := s.heldFor(ctx, actor, workspaceID)
	if err != nil {
		return WireCustomerPortal{}, err
	}
	if held.membership.Role != roleOwner && held.membership.Role != roleAdmin {
		return WireCustomerPortal{}, errBillingNotPermitted
	}
	customer, err := s.paddleCustomer(ctx, held.organisation.ID)
	if err != nil {
		return WireCustomerPortal{}, err
	}
	session, err := s.paddle.PortalSession(ctx, customer)
	if err != nil {
		return WireCustomerPortal{}, fmt.Errorf("the merchant of record did not open a customer portal: %w", err)
	}
	// A URL is followed by a browser, so what is answered is checked to be https
	// and never left to the screen alone: a session that named another scheme
	// would be script or a file the screen's own guard happens to catch, and a
	// merchant answer this service will not vouch for is a failure rather than an
	// answer.
	parsed, err := url.Parse(strings.TrimSpace(session))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return WireCustomerPortal{}, errors.New("the merchant of record answered a customer-portal session that is not an https address, so there is nothing here to send a person to")
	}
	return WireCustomerPortal{URL: parsed.String()}, nil
}

// paddleCustomer is the Paddle customer one organisation's money is under: the
// customer Paddle answered the checkout with (P4.1's own record), read from the
// organisation's own transactions.
//
// It is never anything a request names — not the subscription's, not a body's,
// not a path's — because a customer id is every invoice, every payment method
// and the cancellation of every subscription that customer holds, and one a
// caller could set would be a session opened on somebody else's organisation.
// The newest purchase wins, and a record written before the customer existed
// carries none, so the newest one that has a customer is the answer.
func (s *Service) paddleCustomer(ctx context.Context, organisationID string) (string, error) {
	held, err := recordsWhere[checkoutTransaction](ctx, s.docs, checkoutTransactionsCollection,
		Where{Field: "organisation_id", Value: organisationID})
	if err != nil {
		return "", err
	}
	slices.SortFunc(held, func(a, b checkoutTransaction) int {
		if !a.CreatedAt.Equal(b.CreatedAt) {
			if a.CreatedAt.After(b.CreatedAt) {
				return -1
			}
			return 1
		}
		return strings.Compare(b.ID, a.ID)
	})
	for _, bought := range held {
		if customer := strings.TrimSpace(bought.CustomerID); customer != "" {
			return customer, nil
		}
	}
	return "", errNoPaddleCustomer
}

// cancelSubscriptionsOf ends every subscription one organisation holds, which is
// the cancellation P2.4's deletion reaches: the plan's own line for this task
// puts it here because this area is where a subscription is acted on, and
// Paddle's `Cancel` is what ends one.
//
// The record is left alone and the merchant's own cancellation event is what
// moves it, the way a seat purchase leaves the seats to the event that carries
// them (P4.2): a store marked cancelled here would be a subscription this
// service had ended without Paddle having said so.
//
// Every subscription is attempted and the failures are joined. A deletion that
// stopped at the first merchant failure would leave the rest of an
// organisation's subscriptions charging a customer who is gone, and what a
// caller can do with the answer is finish by hand the ones that are named in it.
func (s *Service) cancelSubscriptionsOf(ctx context.Context, organisationID string) error {
	held, err := s.store.Subscriptions(ctx)
	if err != nil {
		return err
	}
	var failed []error
	for _, sub := range held {
		if sub.Organisation != organisationID || sub.Cancelled {
			continue
		}
		if err := s.paddle.Cancel(ctx, sub.ID); err != nil {
			failed = append(failed, fmt.Errorf("the merchant of record did not cancel the subscription %s: %w", sub.ID, err))
		}
	}
	return errors.Join(failed...)
}
