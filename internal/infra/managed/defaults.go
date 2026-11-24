package managed

// The figures a managed project records when its owner chooses none: the
// monthly amount and the currency `fugaro setup`, `fugaro update` and
// `fugaro managed flag` take when --budget and --budget-currency are not
// given (D2212 and D2211, docs/managed-flag-ergonomics).
//
// They live beside the marker that records them rather than at each door,
// because they are one answer to one question — what the project's budget
// alert is created with — and several callers take them: setup's flags and
// its wizard's question, update, `managed flag`, and the carry that puts them
// back into the installation's variables. A copy at any of those doors would
// be a second default, and the two would drift apart with nothing to say so:
// what the marker records, what the alert is created with and what a person
// reads at the prompt are one figure (reconciliation A2).
//
// What neither of them is: a cap. The figure is a budget ALERT's
// threshold, the point at which Cloud Billing warns the owner of a project
// (internal/managed/budget.go's rule, and why a marker recording one is
// evidence the owner chose a budget rather than a spend limit). What stops a
// run is the gateway's cost cap, a different figure in a different place.
// And a default never overwrites a recorded amount: it applies where the
// marker records nothing or the owner passed no flag, while a project that
// records an amount keeps it until the owner states another with an explicit
// --budget (D2215, and RewriteBudget in marker.go).
const (
	// DefaultBudgetAmount is the monthly amount a managed project records
	// when its owner chooses none: 100 whole units of
	// DefaultBudgetCurrency — the unit the Terraform budget takes
	// (specified_amount.units) and the one --budget is given in, never minor
	// units, and comfortably inside [MinBudgetAmount, MaxBudgetAmount].
	DefaultBudgetAmount int64 = 100
	// DefaultBudgetCurrency is the currency DefaultBudgetAmount is in.
	// USD rather than the billing account's own currency, which only the
	// Billing API knows and which nothing here may spend a call on: a
	// non-USD account passes --budget-currency, and the marker records the
	// code it was created with either way.
	DefaultBudgetCurrency = "USD"
)
