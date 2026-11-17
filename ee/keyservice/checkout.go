// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal's checkout (portal plan P4.1; F15, D2192, D2197).
//
// **The transaction is created here and never by the browser.** `custom_data`
// is what a webhook maps an event back through — the organisation and the
// workspace it bought for — and `custom_data` a page could set is the buyer's to
// forge: a page that named a victim's workspace could buy against it and then
// refund, which is F15's whole finding. So the SPA names a workspace and the
// licence terms it accepted, this service reads the organisation off the
// caller's own membership, and Paddle is told what to sell by this service and
// nobody else (`paddleclient.go`). What the page is handed back is a transaction
// id and nothing else.
//
// **What a purchase is admissible by is a record written here.** A merchant
// event is admitted when its transaction, or the subscription it belongs to,
// began with a transaction this API created (F15, P4.2), and the record below is
// what "this API created" is: Paddle's id for the transaction, the
// organisation and the workspace it carries, and the buyer it was opened for.
// P4.2 reads it by the id the event names, which is why the id is the record's
// own key and not a field of it.
//
// **The licence terms are accepted here and not at sign-up.** The terms of
// service are the sign-up's (P2.1) and these are the licence's (spec §Legal):
// they are the terms a paid term runs under, so the checkout that buys the term
// is where a person agrees to them, and the version is recorded rather than
// flagged — a boolean would say that somebody accepted something without saying
// which wording, and the record has to say which. Nothing here serves the
// wording: the page does, and a version it does not name is no acceptance.
//
// **A term runs from payment and not from binding** (D2197). This service does
// not write the term: the entitlement is P4.2's, made from the purchase event's
// own instant, which is what a customer who pays and sets their project up a
// month later holds the month they paid for by.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// checkoutTransactionsCollection holds one document per transaction this
	// API created, keyed by Paddle's own transaction id: it is what P4.2 reads
	// an event's transaction back through, and it is keyed by that id because
	// the id is the only thing the event and this record share.
	checkoutTransactionsCollection = "checkout_transactions"

	// checkoutSeats is how many seats the checkout sells. It is one, and the
	// seat a customer buys first is the licence's own seat: a subscription is
	// grown from the billing page (P4.3), which is the act that changes Paddle's
	// quantity, and a checkout that let a page name a quantity would be a page
	// setting a price.
	checkoutSeats = 1
)

// The refusals the checkout owns, each a sentinel so that the route can answer
// it with the status and the sentence its reader branches on.
var (
	// errLicenceTermsNotAccepted is a checkout with no licence terms named. The
	// version is the page's — it is the wording the person was shown — and a
	// request without one has recorded no acceptance, so there is nothing to
	// buy under.
	errLicenceTermsNotAccepted = errors.New("keyservice: a checkout records the licence terms the buyer accepted and this one names none")
	// errCheckoutNotPermitted is a member who is not an owner or an admin.
	// Buying commits the organisation to a paid term, which is D2186's line
	// between deciding for the organisation and working in it.
	errCheckoutNotPermitted = errors.New("keyservice: buying a licence is an owner's or an admin's act")
	// errWorkspaceSubscribed is a workspace whose subscription is still running.
	// One active subscription per workspace is D2192's rule, and a second one is
	// two payments for one licence: the seat count is what a customer changes
	// (P4.3).
	errWorkspaceSubscribed = errors.New("keyservice: that workspace already has a running subscription")
)

// The refusals a checkout shares with the workspace it is for.
var (
	// errNoSuchWorkspace is a workspace the store does not hold, or one of an
	// organisation the caller does not belong to. The two are one answer for the
	// reason the organisation reads are (api_orgs.go): which workspaces exist is
	// a list of the portal's customers.
	errNoSuchWorkspace = errors.New("keyservice: that workspace is not one this caller may buy for")
)

// checkoutTransaction is one purchase this API started: Paddle's id for the
// transaction (the document's own key), the custom_data it carries, what it
// sells, and the licence terms the buyer accepted when they opened it.
//
// It holds no key material and no payment detail: the money is Paddle's, and
// what is kept here is what an event is matched by, a price is read back from
// and a support question about a purchase is answered with. The buyer's address
// is kept for the reason the subscription's is (store.go): the purchase event
// mails the key to it, and an event that arrived without one has this record to
// fall back on.
type checkoutTransaction struct {
	ID             string `json:"id"`
	OrganisationID string `json:"organisation_id"`
	WorkspaceID    string `json:"workspace_id"`
	// PriceID and Seats are what was sold, as this service asked for it: the
	// purchase event states its own of both, and a record that disagreed with
	// the event is what a support question about a charge is read from.
	PriceID string `json:"price_id"`
	Seats   int    `json:"seats"`
	// UID and Email are the buyer: the account the sign-in proved and the
	// address the membership carries, and never a body's word for either.
	UID   string `json:"uid"`
	Email string `json:"email"`
	// TermsVersion and TermsAcceptedAt are the licence terms' acceptance
	// (spec §Legal). The instant is this service's clock at the checkout and not
	// a time the page sent, because an acceptance is this service's record of
	// one having happened.
	TermsVersion    string    `json:"terms_version"`
	TermsAcceptedAt time.Time `json:"terms_accepted_at"`
	// CustomerID is the Paddle customer the transaction belongs to. It is
	// Paddle's answer and it is what the billing screen's customer-portal
	// session is minted for (P4.3), so a record written before the customer
	// exists carries none.
	CustomerID string `json:"customer_id"`
	// EntitlementID is the licence this transaction bought, written when the
	// purchase event is processed and empty until then. It is what a refund or a
	// chargeback that names this transaction resolves to (F16, F29): the
	// transaction is the API's own record of the sale, so the licence the money
	// belongs to is read off it and never off the workspace, which holds whatever
	// licence is current rather than the one that was paid for.
	EntitlementID string    `json:"entitlement_id"`
	CreatedAt     time.Time `json:"created_at"`
}

// checkout opens one purchase: it checks what the caller may buy, records the
// licence terms, asks the merchant of record for a transaction, and records what
// it asked for. The answer is the transaction id the overlay is opened with and
// nothing else.
//
// Everything that can refuse is refused before Paddle is reached, and the one
// step that cannot be undone is the last of them: a transaction Paddle has
// created exists whatever happens next, so a record that cannot be written after
// it leaves a transaction whose event P4.2 parks for operations rather than one
// whose payment went nowhere. That is the safe half of the pair, and it is the
// same shape `merchant.go`'s `paid` takes with a mint.
func (s *Service) checkout(ctx context.Context, actor Caller, asked WireCheckoutCreate) (WireCheckoutSession, error) {
	if strings.TrimSpace(asked.TermsVersion) == "" {
		return WireCheckoutSession{}, errLicenceTermsNotAccepted
	}
	workspace, present, err := getRecord[WireWorkspace](ctx, s.docs, workspacesCollection, asked.WorkspaceID)
	if err != nil {
		return WireCheckoutSession{}, err
	}
	if !present {
		return WireCheckoutSession{}, errNoSuchWorkspace
	}
	// The workspace's own organisation is the one whose membership decides,
	// the way a binding's licence decides it: an organisation named by a request
	// is never what authorises a request, and the one a workspace belongs to is
	// read from the workspace.
	org, membership, member, err := s.ownMembership(ctx, actor, workspace.OrganisationID)
	if err != nil {
		return WireCheckoutSession{}, err
	}
	if !member {
		return WireCheckoutSession{}, errNoSuchWorkspace
	}
	if membership.Role != roleOwner && membership.Role != roleAdmin {
		return WireCheckoutSession{}, errCheckoutNotPermitted
	}
	// The price is the deployment's (Config.PaddlePriceID), and the row behind it
	// is the Team month: the checkout's wire shape names a workspace and no row,
	// so this route sells the one row the deploy priced. A deployment running
	// with a merchant client and no price is one whose every checkout would ask
	// Paddle to sell nothing, so it is refused here, naming the field, rather
	// than passed on as the merchant's own complaint about a body this service
	// built.
	price := strings.TrimSpace(s.cfg.PaddlePriceID)
	if price == "" {
		return WireCheckoutSession{}, errors.New("this deployment names no Paddle price (Config.PaddlePriceID), so there is nothing for a checkout to sell")
	}
	now := s.now().UTC()
	if err := s.refuseSubscribedWorkspace(ctx, workspace, now); err != nil {
		return WireCheckoutSession{}, err
	}
	transaction, err := s.paddle.CreateTransaction(ctx, Checkout{
		PriceID:      price,
		Organisation: org.ID,
		Workspace:    workspace.ID,
		Seats:        checkoutSeats,
	})
	if err != nil {
		return WireCheckoutSession{}, fmt.Errorf("the merchant of record did not open a transaction: %w", err)
	}
	if strings.TrimSpace(transaction.ID) == "" {
		return WireCheckoutSession{}, errors.New("the merchant of record opened a transaction and answered no id, so the overlay has nothing to open")
	}
	if err := s.recordCheckout(ctx, checkoutTransaction{
		ID:              transaction.ID,
		OrganisationID:  org.ID,
		WorkspaceID:     workspace.ID,
		PriceID:         price,
		Seats:           checkoutSeats,
		UID:             actor.UID,
		Email:           membership.Email,
		TermsVersion:    asked.TermsVersion,
		TermsAcceptedAt: now,
		CustomerID:      transaction.CustomerID,
		CreatedAt:       now,
	}); err != nil {
		return WireCheckoutSession{}, err
	}
	return WireCheckoutSession{TransactionID: transaction.ID}, nil
}

// refuseSubscribedWorkspace refuses a workspace whose subscription is still
// running (D2192). "Running" is the rule the renewal reads (renew.go): not
// cancelled, and not past the end of the term it has paid for. A workspace whose
// subscription was cancelled and has run out is one a customer buys again for.
//
// **The record names the workspace in one of two spellings, and both are read.**
// `Subscription.WorkspaceID` is the Firebase project the subscription's keys
// bind to (store.go), which is what the one landed writer stores — `merchant.go`'s
// `purchased`, from the deploy event's own project id. A portal workspace may
// instead have no project at all: D2192's "a workspace may be unbound" is F25's
// whole point, since a customer pays before `fugaro setup` has created anything,
// and a subscription bought for such a workspace can only be recorded under the
// portal workspace id the checkout's own `custom_data` carries. A guard reading
// one spelling alone would either never fire or miss a second sale, so this one
// reads both: the two are names of one workspace, so either naming it is this
// workspace and not two.
func (s *Service) refuseSubscribedWorkspace(ctx context.Context, workspace WireWorkspace, now time.Time) error {
	held, err := s.store.Subscriptions(ctx)
	if err != nil {
		return err
	}
	project := ""
	if workspace.ProjectID != nil {
		project = strings.TrimSpace(*workspace.ProjectID)
	}
	for _, sub := range held {
		if sub.Cancelled || !now.Before(sub.Expires) {
			continue
		}
		if sub.WorkspaceID != workspace.ID && (project == "" || sub.WorkspaceID != project) {
			continue
		}
		return fmt.Errorf("%w: the subscription %s runs to %s", errWorkspaceSubscribed, sub.ID, sub.Expires.UTC().Format(time.RFC3339))
	}
	return nil
}

// recordCheckout writes the record a merchant event is admitted by (F15). It is
// written under Paddle's transaction id and it is an upsert, so a checkout
// retried after a lost answer — the same transaction, or a second one — leaves
// one record per transaction and never a record that moved.
func (s *Service) recordCheckout(ctx context.Context, record checkoutTransaction) error {
	doc, err := storedDoc(record)
	if err != nil {
		return err
	}
	if err := s.docs.Put(ctx, checkoutTransactionsCollection, record.ID, doc); err != nil {
		return fmt.Errorf("the checkout %s was created at the merchant of record and could not be recorded: %w", record.ID, err)
	}
	return nil
}

// recordBought writes the licence one checkout's transaction bought onto that
// transaction's own record, which is what a refund or a chargeback naming the
// transaction resolves to (F16, F29).
//
// A checkout record the store does not hold is left alone: the licence has
// already been granted by the time this runs, and a `500` would send the
// merchant back through a purchase that is done rather than fix anything.
func (s *Service) recordBought(ctx context.Context, transactionID, entitlementID string) error {
	record, held, err := getRecord[checkoutTransaction](ctx, s.docs, checkoutTransactionsCollection, transactionID)
	if err != nil {
		return err
	}
	if !held {
		return nil
	}
	record.EntitlementID = entitlementID
	doc, err := storedDoc(record)
	if err != nil {
		return err
	}
	if err := s.docs.Put(ctx, checkoutTransactionsCollection, record.ID, doc); err != nil {
		return fmt.Errorf("keyservice: the licence the checkout %s bought could not be recorded on it: %w", transactionID, err)
	}
	return nil
}
