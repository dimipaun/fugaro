// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Resellers (portal plan P6.1; D2189, D2190, D2191).
//
// A reseller is an organisation that sells to its own customers. It is never a
// member of theirs (F40): it creates a customer's organisation, invites that
// customer's first owner, and from then on sees what it sold — their keys'
// metadata, never a key (F5) — and what it owes Fugaro, never what Fugaro is
// owed (D2190).
//
// The money is operations' to set and the reseller's to accrue against: a credit
// limit, and a wholesale daily rate per row. Accrual is days times seats times
// the rate (D2189), and the limit is what stops a reseller minting past what it
// has been allowed to sell.

import "time"

// WireReseller is one reseller organisation's commercial terms, as the reseller
// reads them and as operations sets them.
type WireReseller struct {
	OrganisationID string `json:"organisation_id"`
	// CreditLimitMicros is the ceiling on accrual that has not been paid for
	// (D2189). It is a limit and not a balance: no prepaid credit exists, and
	// the accrual's own sum is what a statement collects (P6.3).
	CreditLimitMicros int64 `json:"credit_limit_micros"`
	// Rates is one wholesale price per policy row, and a row with no rate is a
	// row this reseller cannot sell: a missing price is not a price of zero.
	Rates     []WireResellerRate `json:"rates"`
	CreatedAt time.Time          `json:"created_at"`
}

// WireResellerRate is one row's wholesale price.
type WireResellerRate struct {
	// Row names a policy row in policy.go, as `entitlements.mint` does.
	Row string `json:"row"`
	// DailyRateMicros is what one seat of this row costs a day, prorated at
	// statement time (D2189) rather than multiplied out against a term: a
	// mid-month revocation has to stop the accrual, and only a daily rate can.
	DailyRateMicros int64 `json:"daily_rate_micros"`
}

// WireResellerCustomer is one customer organisation a reseller sold to.
type WireResellerCustomer struct {
	OrganisationID string `json:"organisation_id"`
	Name           string `json:"name"`
	// ResellerOrganisationID is the link, and it is a field here rather than on
	// the customer alone because operations can relink one (P6.1): a customer
	// whose reseller changed is a customer whose accrual moves with it.
	ResellerOrganisationID string    `json:"reseller_organisation_id"`
	LinkedAt               time.Time `json:"linked_at"`
}

// WireResellerRelink is operations' request to move one customer organisation
// from one reseller to another.
type WireResellerRelink struct {
	CustomerOrganisationID string `json:"customer_organisation_id"`
	ResellerOrganisationID string `json:"reseller_organisation_id"`
}

func init() {
	registerWire("resellers.reseller", func() any { return new(WireReseller) })
	registerWire("resellers.customer", func() any { return new(WireResellerCustomer) })
	registerWire("resellers.relink", func() any { return new(WireResellerRelink) })
}
