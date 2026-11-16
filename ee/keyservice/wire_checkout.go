// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Checkout (portal plan P4.1; F15, D2192).
//
// The SPA never tells Paddle what it is buying on the customer's behalf: the API
// creates the transaction server-side, with the organisation and workspace in
// its `custom_data` (F15), and the overlay is handed a transaction id and
// nothing else. That is the whole of why these two shapes are this small — a
// browser that could name the workspace could name somebody else's, buy against
// it and then refund, and the API's own record of the transaction is what makes
// the webhook's event admissible.
//
// The licence terms are accepted here, at checkout, and not at sign-up: the
// terms of service are the sign-up's (P2.1) and these are the licence's
// (spec §Legal), and the version is what gets recorded.

type WireCheckoutCreate struct {
	// WorkspaceID is the workspace the purchase is for. One active subscription
	// per workspace is the rule (D2192), so this is also the key the API checks
	// that rule against.
	WorkspaceID string `json:"workspace_id"`
	// TermsVersion is the licence terms the buyer accepted, and it is named
	// rather than flagged: a boolean would say that somebody accepted something
	// without saying which wording, and the record has to say which.
	TermsVersion string `json:"terms_version"`
}

// WireCheckoutSession is what the SPA opens the overlay with.
type WireCheckoutSession struct {
	// TransactionID is Paddle's id for the transaction the API created, and it
	// is the only value the browser is trusted with: the price, the quantity
	// and the custom data are the API's, not the page's.
	TransactionID string `json:"transaction_id"`
}

func init() {
	registerWire("checkout.create", func() any { return new(WireCheckoutCreate) })
	registerWire("checkout.session", func() any { return new(WireCheckoutSession) })
}
