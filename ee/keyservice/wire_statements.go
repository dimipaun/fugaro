// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Monthly statements (portal plan P6.3; D2189, D2190).
//
// A statement is what a reseller owes Fugaro for one month of accrual: the sum
// of days times seats times the wholesale rate over the month, one line per
// customer, issued once and then read. It is the reseller's invoice and not a
// commission — the direction is D2190's — and what is paid against it is
// recorded here by operations and by nobody else, because only a person can see
// the money arrive.
//
// The run that issues a month is idempotent (P6.3): an accrual that is added up
// twice is a statement that bills a reseller twice, so a second run of the same
// month returns the statement that exists.

import "time"

// WireStatement is one reseller's month.
type WireStatement struct {
	ID                     string `json:"id"`
	ResellerOrganisationID string `json:"reseller_organisation_id"`
	// Month is `YYYY-MM` in UTC: a month boundary is a time zone's opinion
	// (P5.2's TestReportMonthsAreUTC), and a boundary that moved with the
	// reader's clock would put one day's accrual in two statements.
	Month string `json:"month"`
	// AccruedMicros is what the month's accrual came to and PaidMicros is what
	// has been recorded against it. They are separate because a statement is
	// issued before it is paid, and a partly paid one has to keep saying what
	// it was for.
	AccruedMicros int64 `json:"accrued_micros"`
	PaidMicros    int64 `json:"paid_micros"`
	// State is open or paid, and it follows the two amounts: a statement whose
	// payments cover its accrual is paid, and no separate act closes one.
	State string `json:"state"`
	// Lines is one entry per customer the month accrued for, in a stable order,
	// so a reseller reading two months can see which customer moved.
	Lines    []WireStatementLine `json:"lines"`
	IssuedAt time.Time           `json:"issued_at"`
	// PaidAt is when it was settled, and null while it is open.
	PaidAt *time.Time `json:"paid_at"`
}

// WireStatementLine is one customer's part of a statement.
type WireStatementLine struct {
	OrganisationID string `json:"organisation_id"`
	// AmountMicros is that customer's accrual for the month. It is positive:
	// a revoked entitlement stops accruing rather than crediting, and a
	// correction is a payment recorded against the statement.
	AmountMicros int64 `json:"amount_micros"`
}

// WireStatementPayment is operations' record that money arrived against one
// statement.
type WireStatementPayment struct {
	// AmountMicros is what was received, and it may be less than the statement:
	// a partial payment leaves the statement open and shows both figures.
	AmountMicros int64     `json:"amount_micros"`
	PaidAt       time.Time `json:"paid_at"`
	// Reference is the bank or transfer reference operations was given, kept so
	// that a payment can be matched to a statement months later.
	Reference string `json:"reference"`
}

func init() {
	registerWire("statements.statement", func() any { return new(WireStatement) })
	registerWire("statements.payment", func() any { return new(WireStatementPayment) })
}
