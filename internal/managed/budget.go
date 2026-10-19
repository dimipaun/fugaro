package managed

import (
	"fmt"

	inframanaged "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The apply-time budget read (Task 4.4, and D62's second blocking input).
//
// What it is, and what the owner's ruling of 2026-10-18 settled. D62 is
// "does the cost check block a plan, or only flag it?", and its answer is
// "flag by default, block on a named, owner-written attribute allowlist, and
// block on the budget": the allowlist's block is Task 1.3's (a plan that is
// above a listed attribute is refused), and this is the budget's. The plan's
// text for this task was written to compare the amount the marker records
// against the spend Fugaro's own history holds (`internal/budget`'s RTDB
// ledgers), and the owner's ruling of 2026-10-18 removed that comparison,
// because the two figures are not in the same unit: Fugaro's recorded spend is
// **micro-USD model spend** — what the agent sessions cost — while the marker
// records the **GCP budget ALERT amount**, in whatever currency the owner
// chose (D64, and Task 4.3's note that a budget alert reaches the billing
// account's admins, not the project's users). Comparing them would need a
// currency, a GCP bill and a conversion this program does not have, and the
// amount caps no bill: it is the figure a budget alert fires at. So the
// comparison is gone, and the amount keeps the one job it can do honestly:
// **it is the evidence that the owner chose a budget at all**.
//
// That is why this read REFUSES rather than compares. The apply gate Task 3.1
// ships closed opens — and only opens — when the project's creation fact was
// read and it records an amount and a currency. A managed project whose
// creation recorded no amount (the marker's version 1: a creation before Task
// 4.2, or one that was not managed), a marker that cannot be read, one this
// build cannot parse, one of a version it does not know and one naming another
// project all leave the gate closed, each with its own sentence. Every failure
// is a refusal and never a fall-through: nothing here reads a missing amount
// as an unlimited one, and nothing here falls back to the old behaviour, which
// was to apply with no budget control at all.
//
// The read itself is not made here. It is internal/infra/managed.ReadBudget —
// the same read as IsManaged, over the same object and the same project-number
// proof, so the amount can never come from an object IsManaged would refuse —
// and the caller that holds the cloud clients makes it (internal/cli's
// `fugaro managed apply`, and `fugaro restore` through it). What THIS file
// owns is the decision the read's answer is put to, in one place, so there is
// exactly one thing that opens the gate.

// Budget is the amount and the currency the apply-time read answered with: the
// GCP budget alert amount the owner chose at creation (Task 4.2, D64) and the
// currency it is in. It is never a credential, never an estimate and never
// Fugaro's own recorded spend — see this file's comment for the ruling that
// says why.
type Budget struct {
	Amount   int64
	Currency string
}

// Check refuses a pair an apply may not be opened on, and it is the marker's
// own rule and not a copy of it: internal/infra/managed's Budget.Check, which
// the marker's writer (Marker.Bytes, and init before anything is created) and
// its reader (readFact) both apply — an amount from 1 to 10,000,000 whole
// units of its currency, and a currency of exactly three ASCII uppercase
// letters. One rule, called rather than restated, so the gate can never accept
// a figure the marker would refuse ("usd", "US\n", "EURO", 9e18).
func (b Budget) Check() error {
	return inframanaged.Budget{Amount: b.Amount, Currency: b.Currency}.Check()
}

// Valid reports whether the pair is one an apply may be opened on: exactly
// when Check refuses nothing. It is a method rather than a check inside
// ApplyGate so that Enable's own Refuse — the second check of the same value,
// the one that runs however Apply is called — cannot drift from the first.
func (b Budget) Valid() bool { return b.Check() == nil }

// BudgetRead is the apply-time read's answer, carried whole, in the shape the
// caller that holds the cloud clients can make it. It is deliberately the
// caller's own struct rather than ReadBudget's four return values, because the
// decision needs one thing ReadBudget cannot answer on its own.
type BudgetRead struct {
	// Project is the Fugaro project's name: every refusal below names it.
	Project string
	// Amount, Currency, OK, Reason and Err are internal/infra/managed's
	// ReadBudget, field for field. OK is true only when the creation fact was
	// read AND it records a valid amount; Reason is its one-line reason, empty
	// when OK; Err is set only when the read could not be made at all, and it
	// is never a reason to proceed. With OK false, Amount and Currency are set
	// only when the marker is a version 2 whose recorded figure fails
	// Budget.Check — the one case ReadBudget hands the refused figure back, so
	// the refusal can say the recorded budget is invalid rather than that the
	// project is not managed.
	Amount   int64
	Currency string
	OK       bool
	Reason   string
	Err      error
	// Invalid is internal/infra/managed's BudgetAnswer.Invalid: the marker is
	// a version 2 about this project whose recorded figure fails Check —
	// including an empty "budget":{}, whose zero figure the Amount and
	// Currency above cannot tell from "no figure".
	Invalid bool
	// Managed and ManagedReason are internal/infra/managed's IsManaged, and
	// they are filled in for one case only: when ReadBudget answered with no
	// amount and no error, which is the case ReadBudget's own reason cannot
	// tell apart — a managed creation that recorded no amount (version 1) and
	// a project that is not managed at all both come back that way. The caller
	// makes that second read then and only then, so the refusal can name the
	// right one of the two; the zero value is "not managed", which is the
	// fail-closed direction. Nothing is applied in either case.
	Managed       bool
	ManagedReason string
}

// ApplyGate is Task 4.4's whole decision, in one place: given the apply-time
// read's answer, it returns the gate the apply runs behind, or the refusal
// that says why it did not open.
//
// The order of the cases is the order of the reads: an error first (a read
// that did not answer is not an apply), then the amount itself — which is what
// opens the gate and the only thing that does — then the two ways of having no
// amount. Every branch returns a closed Enable, so there is no path through
// this function that opens the gate on anything but a read that answered with
// a usable pair.
func ApplyGate(r BudgetRead) (Enable, error) {
	b := Budget{Amount: r.Amount, Currency: r.Currency}
	switch {
	case r.Err != nil:
		return Enable{}, fmt.Errorf("%w: the apply-time budget read of %s could not be made, and a read that did not answer is an apply with no budget control at all: %s",
			ErrNotEnabled, safetext.Field(r.Project), safetext.Field(r.Err.Error()))
	case r.OK && b.Valid():
		return Enable{budget: b, read: true}, nil
	case r.OK || r.Invalid || r.Amount != 0 || r.Currency != "":
		// A recorded figure that fails the marker's own Check. Through
		// ReadBudget this is a version-2 marker whose amount or currency is
		// outside the shape the write enforces (ReadBudget answers OK only for
		// a valid one, and hands the refused figure back for exactly this
		// sentence); a second caller passing a pair built by hand lands here
		// too. Either way it is refused, and it is reported as what it is —
		// an invalid recorded budget — and not as a project that is not
		// managed, which would send the owner to the wrong cure.
		why := "the recorded figure was refused"
		if err := b.Check(); err != nil {
			why = err.Error()
		}
		return Enable{}, fmt.Errorf("%w: the budget recorded in the marker of %s is invalid: %s",
			ErrNotEnabled, safetext.Field(r.Project), safetext.Field(why))
	case !r.Managed:
		// Not managed at all: the project keeps the coding agent's refusal, and
		// the sentence is the one the command already prints for that.
		reason := safetext.Field(r.ManagedReason)
		if reason == "" {
			reason = "its creation fact was not believed"
		}
		return Enable{}, fmt.Errorf("%w: %s is not managed (%s), so its plans keep the coding agent's refusal and are applied by fugaro init's own path with its confirmation",
			ErrNotEnabled, safetext.Field(r.Project), reason)
	default:
		// Managed, and the creation fact records no amount: the marker's
		// version 1, which is a creation before Task 4.2 or a creation that
		// was not managed (an ordinary `fugaro init --create-project`, whose
		// WriteMarker call passes no budget). The sentence says what is true
		// in this release, and since Task 6.1 there IS a way for such a
		// project to get an amount: the owner's own command, at their own
		// terminal, which replaces this marker's version-1 fact with the one
		// that records the amount the owner states (managed.WriteFlag).
		reason := safetext.Field(r.Reason)
		if reason == "" {
			reason = "the creation fact records no amount"
		}
		return Enable{}, fmt.Errorf("%w: %s has no budget recorded (%s), so the project's own owner can add one by flagging it managed — `fugaro managed flag <gcp-project-id> --budget N --budget-currency CCC`, typed at their own terminal, which replaces this creation fact with the version that records the amount — or a managed project is created with one: `fugaro init --create-project --managed --budget N --budget-currency CCC --billing-account ID`",
			ErrNotEnabled, safetext.Field(r.Project), reason)
	}
}
