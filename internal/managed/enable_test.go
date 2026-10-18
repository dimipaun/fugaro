package managed

import (
	"errors"
	"strings"
	"testing"
)

// The gate's own tests: what a value nobody built refuses, what the refusal
// says, and what an open one does. Task 4.4 is the task that opens the gate,
// and the file that says out loud what "open" now means — a budget read that
// answered with an amount (budget_test.go is where every way of not answering
// is put to it).

// The zero value is closed, and it is what a caller that forgets to thread a
// read through passes: refused rather than admitted.
func TestZeroGateIsClosedAndRefuses(t *testing.T) {
	var zero Enable
	if zero.read {
		t.Fatal("the zero Enable is open")
	}
	if err := zero.Refuse(); err == nil {
		t.Fatal("the zero Enable refuses nothing")
	}
	if !errors.Is(zero.Refuse(), ErrNotEnabled) {
		t.Errorf("the zero Enable's refusal %v is not ErrNotEnabled", zero.Refuse())
	}
	// A gate carrying an amount nobody read is closed too: Read is the read's
	// answer, and a pair set beside it is not one.
	if err := (Enable{budget: Budget{Amount: 5000, Currency: "USD"}}).Refuse(); err == nil {
		t.Error("an Enable with a budget and no read refuses nothing")
	}
	// And a gate the read opened with a pair the marker's own reader refuses is
	// not open: the second check of the same value cannot drift from the first.
	for _, b := range []Budget{{}, {Amount: 0, Currency: "USD"}, {Amount: -5, Currency: "USD"}, {Amount: 5000}, {Amount: 5000, Currency: "usd"}, {Amount: 5000, Currency: "US\n"}, {Amount: 5000, Currency: "EURO"}, {Amount: 10_000_001, Currency: "USD"}, {Amount: 9_000_000_000_000_000_000, Currency: "USD"}} {
		if err := (Enable{budget: b, read: true}).Refuse(); err == nil {
			t.Errorf("an Enable opened on %+v refuses nothing", b)
		}
	}
}

// The refusal of a gate nobody opened is one sentence, and it is not the
// release sentence Task 3.1 shipped: those three safeguards are in the build
// now, and what a closed gate is left to mean is that nothing was read at all.
// It names no flag, variable or command a reader could pass, because the
// session it refuses is the one that would read it as an instruction
// (IDEHint's rule).
func TestClosedGateRefusalNamesNoWayThrough(t *testing.T) {
	if !strings.Contains(NotEnabled, "no apply-time budget read opened it") {
		t.Errorf("NotEnabled no longer says what a closed gate means: %s", NotEnabled)
	}
	err := Enable{}.Refuse()
	if err == nil {
		t.Fatal("the zero gate refuses nothing")
	}
	if !errors.Is(err, ErrNotEnabled) {
		t.Errorf("refusal %v is not ErrNotEnabled", err)
	}
	if !strings.Contains(err.Error(), NotEnabled) {
		t.Errorf("refusal %v does not carry the sentence", err)
	}
	for _, banned := range []string{"--", "export ", "unset ", "set ", "FUGARO_"} {
		if strings.Contains(err.Error(), banned) {
			t.Errorf("the refusal %v names %q, which its reader would take as a way through", err, banned)
		}
	}
}

// And the gate a budget read opens is the one that refuses nothing: it is the
// only way the value is ever open, and it is what Apply's own first step reads.
func TestGateABudgetReadOpenedRefusesNothing(t *testing.T) {
	gate, err := ApplyGate(BudgetRead{Project: project, Amount: 5000, Currency: "USD", OK: true})
	if err != nil {
		t.Fatalf("a read that answered with an amount was refused: %v", err)
	}
	if !gate.read {
		t.Fatal("the gate of a read that answered with an amount is closed")
	}
	if gate.budget != (Budget{Amount: 5000, Currency: "USD"}) {
		t.Errorf("the open gate carries %+v, want the read's own pair", gate.budget)
	}
	if err := gate.Refuse(); err != nil {
		t.Errorf("the open gate refused: %v", err)
	}
}
