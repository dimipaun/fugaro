// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Reports (portal plan P5.2; spec §Operations, D2190).
//
// A report is one month of sales split by channel: what customers bought
// directly and what they bought through a reseller. It is derived from the
// ledger's entries and never from a counter, so a report and the ledger cannot
// disagree, and a comped key is not revenue — the free seat is a grant, and a
// figure that counted it would make it look like a sale (D2199, D2203).
//
// Months are UTC (P5.2), and a reseller's receivable is its statements' total
// rather than a second calculation of the same accrual (D2190): the direction of
// that money is resellers owing Fugaro, so what a report adds up for a reseller
// is what the statements have already billed.

type WireReportMonth struct {
	// Month is `YYYY-MM` in UTC, the same spelling a statement carries.
	Month string `json:"month"`
	// DirectMicros and ResellerMicros are the month's two channels, and they
	// are separate figures rather than one figure and a split: the totals the
	// operations team reads are the two, and neither is derived from the other.
	DirectMicros   int64 `json:"direct_micros"`
	ResellerMicros int64 `json:"reseller_micros"`
	// Customers is every customer the month had a line for, including the ones
	// that came to nothing: a customer with no amount is a fact a report is
	// read for, and leaving them out would hide a month that never happened.
	Customers []WireReportCustomer `json:"customers"`
}

// WireReportCustomer is one customer's line in a month.
type WireReportCustomer struct {
	OrganisationID string `json:"organisation_id"`
	Name           string `json:"name"`
	// Channel is direct or reseller and it is the channel of the month's sales,
	// not of the customer: a customer a reseller sold to in March and who
	// bought directly in April appears once in each month's report under that
	// month's channel (operations can relink a customer, P6.1).
	Channel string `json:"channel"`
	// AmountMicros is what that customer paid in the month, signed as the
	// ledger's lines are: a month of refunds is a negative figure and not an
	// empty one. A comped customer's line is zero.
	AmountMicros int64 `json:"amount_micros"`
}

func init() {
	registerWire("reports.month", func() any { return new(WireReportMonth) })
	registerWire("reports.customer", func() any { return new(WireReportCustomer) })
}
