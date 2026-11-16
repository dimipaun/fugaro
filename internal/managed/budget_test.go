package managed

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// The apply-time budget read's tests (Task 4.4).
//
// What is asserted here is the decision and not the read: whether an amount
// came back is internal/infra/managed's question, answered by its own fixtures
// (read_test.go), and the cases below are its answers. The CLI's end-to-end
// cases — the same nine ways of failing, through the real ReadBudget, the real
// marker object and the command's own fakes — are in internal/cli's
// managed_apply_test.go, so a change to the read that stopped producing one of
// these answers is caught there rather than assumed here.

// closedGate is the gate a read that answered with no amount leaves behind:
// closed, with ErrNotEnabled for a refusal. Every case below starts here, so
// "the gate stays closed" is asserted the same way for all of them.
func closedGate(t *testing.T, r BudgetRead) Enable {
	t.Helper()
	gate, err := ApplyGate(r)
	if err == nil {
		t.Fatalf("a read that did not answer with an amount opened the gate: %+v", r)
	}
	if gate.read {
		t.Fatalf("the gate is open after a refusal: %+v", gate)
	}
	if !errors.Is(err, ErrNotEnabled) {
		t.Errorf("refusal %v is not ErrNotEnabled", err)
	}
	return gate
}

// Every way the apply-time read can fail to answer with an amount, each
// leaving the gate closed with its own sentence. The failures are the task's
// own list — an absent marker, an amount of 0 or below, a currency that is not
// there, a marker naming another project, a read that could not be made at all,
// and an object that is malformed, oversized or has trailing data (the last
// three the same answer, because ReadBudget refuses a shape it cannot parse
// before it looks at anything else) — plus the two answers the second read
// adds. The one case that is NOT here is the marker's version 1: a managed
// creation fact that records no amount is the case D1545 takes, and it opens
// the gate WITH A WARNING rather than a refusal
// (TestApplyGateOpensWithAWarningOnAVersionOneMarker).
func TestApplyGateRefusesEveryReadThatAnsweredNoAmount(t *testing.T) {
	for _, tc := range []struct {
		what string
		read BudgetRead
		want []string
	}{
		{
			what: "the marker is absent",
			read: BudgetRead{Project: project, ManagedReason: "gs://fugaro-runs-proj-1234/fugaro/managed.json is not there (or cannot be read), so aurora was not created by a managed run"},
			want: []string{"is not managed", "not there (or cannot be read)"},
		},
		{
			what: "the marker is malformed",
			read: BudgetRead{Project: project, ManagedReason: "gs://fugaro-runs-proj-1234/fugaro/managed.json is not a marker for aurora: invalid character 'x'"},
			want: []string{"is not managed", "is not a marker"},
		},
		{
			what: "the marker is oversized",
			read: BudgetRead{Project: project, ManagedReason: "gs://fugaro-runs-proj-1234/fugaro/managed.json is 4097 bytes, bigger than any marker (4096), so it is not aurora's creation fact"},
			want: []string{"is not managed", "bigger than any marker"},
		},
		{
			what: "the marker has trailing data",
			read: BudgetRead{Project: project, ManagedReason: "gs://fugaro-runs-proj-1234/fugaro/managed.json is not a marker for aurora: it holds more than one JSON value"},
			want: []string{"is not managed", "more than one JSON value"},
		},
		{
			what: "the marker names another project",
			read: BudgetRead{Project: project, ManagedReason: "gs://fugaro-runs-proj-1234/fugaro/managed.json names project proj-9999, not proj-1234: the fact is about another project, so aurora is not managed"},
			want: []string{"is not managed", "names project proj-9999"},
		},
		{
			what: "the read could not be made at all",
			read: BudgetRead{Project: project, Err: errors.New("reading project proj-1234 for aurora: 502")},
			want: []string{"could not be made", "502"},
		},
		{
			what: "the amount is zero",
			read: BudgetRead{Project: project, OK: true, Amount: 0, Currency: "USD", Managed: true},
			want: []string{"the budget recorded in the marker of aurora is invalid", "amount 0 is outside 1 to 10000000"},
		},
		{
			what: "the amount is negative",
			read: BudgetRead{Project: project, OK: true, Amount: -5, Currency: "USD", Managed: true},
			want: []string{"is invalid", "amount -5 is outside 1 to 10000000"},
		},
		{
			what: "the currency is not there",
			read: BudgetRead{Project: project, OK: true, Amount: 5000, Managed: true},
			want: []string{"is invalid", "currency \"\" is not three ASCII uppercase letters"},
		},
		// H1: the gate's rule is the marker's own Check, exactly — each of
		// these passed an "amount above 0 and a currency" rule and is refused.
		{
			what: "the currency is lowercase",
			read: BudgetRead{Project: project, OK: true, Amount: 5000, Currency: "usd", Managed: true},
			want: []string{"is invalid", "not three ASCII uppercase letters"},
		},
		{
			what: "the currency carries a newline",
			read: BudgetRead{Project: project, OK: true, Amount: 5000, Currency: "US\n", Managed: true},
			want: []string{"is invalid", "not three ASCII uppercase letters"},
		},
		{
			what: "the currency is four letters",
			read: BudgetRead{Project: project, OK: true, Amount: 5000, Currency: "EURO", Managed: true},
			want: []string{"is invalid", "not three ASCII uppercase letters"},
		},
		{
			what: "the amount is above ten million",
			read: BudgetRead{Project: project, OK: true, Amount: 10_000_001, Currency: "USD", Managed: true},
			want: []string{"is invalid", "amount 10000001 is outside 1 to 10000000"},
		},
		{
			what: "the amount is 9e18",
			read: BudgetRead{Project: project, OK: true, Amount: 9_000_000_000_000_000_000, Currency: "USD", Managed: true},
			want: []string{"is invalid", "is outside 1 to 10000000"},
		},
		// L3: a version-2 marker whose figure fails Check comes back from
		// ReadBudget with OK false and the refused figure: it is reported as
		// an invalid recorded budget, never as "is not managed".
		{
			what: "a version 2 marker whose budget is empty",
			read: BudgetRead{Project: project, Invalid: true, Reason: "records no budget amount a managed creation could have chosen"},
			want: []string{"the budget recorded in the marker of aurora is invalid", "amount 0 is outside"},
		},
		{
			what: "a version 2 marker whose recorded figure fails Check",
			read: BudgetRead{Project: project, Amount: 5000, Currency: "usd", Reason: "records no budget amount a managed creation could have chosen"},
			want: []string{"the budget recorded in the marker of aurora is invalid", "not three ASCII uppercase letters"},
		},
		{
			what: "a read that answered nothing at all",
			read: BudgetRead{Project: project},
			want: []string{"is not managed", "its creation fact was not believed"},
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			gate := closedGate(t, tc.read)
			_, err := ApplyGate(tc.read)
			if err == nil {
				t.Fatal("ApplyGate refused nothing")
			}
			if !strings.Contains(err.Error(), project) {
				t.Errorf("the refusal does not name the project: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %v does not carry %q", err, want)
				}
			}
			// The gate the refusal leaves behind refuses on its own too: the
			// value is what Apply reads first, and it must not be openable by
			// being handed over instead of rebuilt.
			if err := gate.Refuse(); err == nil {
				t.Error("the gate left by a refusal does not refuse")
			}
		})
	}
}

// The text a reader acts on is one line, whatever the read put in it: a reason
// can come from an object someone else wrote, and a newline in it must not
// become a line of this program's output. Both halves are checked — the refusal
// of a project the marker did not place, and the version-1 warning D1545
// attaches to a managed creation fact that records no amount.
func TestApplyGateRefusalIsOneLine(t *testing.T) {
	bad := "no amount\r\nsecond line\x00"
	_, err := ApplyGate(BudgetRead{Project: project, ManagedReason: bad})
	if err == nil {
		t.Fatal("ApplyGate refused nothing")
	}
	oneLine(t, "the refusal", err.Error())
	gate, err := ApplyGate(BudgetRead{Project: project, Managed: true, Reason: bad})
	if err != nil {
		t.Fatalf("a version-1 marker was refused, and D1545 takes it with a warning: %v", err)
	}
	if w := gate.Warning(); w == "" {
		t.Fatal("the version-1 warning is empty, so the session is told nothing")
	} else {
		oneLine(t, "the warning", w)
	}
}

// oneLine fails the test when text carries a carriage return, a NUL or a
// newline: this program's output a reader acts on is one line.
func oneLine(t *testing.T, what, text string) {
	t.Helper()
	if strings.ContainsAny(text, "\r\x00") || strings.Count(text, "\n") != 0 {
		t.Errorf("%s carries the reason's own control characters: %q", what, text)
	}
}

// D1545's one exception (budget.go): a managed creation fact that records no
// amount — the marker's version 1 — opens the gate WITH A WARNING rather than a
// refusal, because the read answered that the project is managed and what is
// missing is only the evidence that the owner chose a budget. The gate is open
// (Refuse nil) and the warning names the reason and the owner's own commands,
// and it names no environment variable.
func TestApplyGateOpensWithAWarningOnAVersionOneMarker(t *testing.T) {
	const reason = "gs://fugaro-runs-proj-1234/fugaro/managed.json records no budget amount for aurora"
	gate, err := ApplyGate(BudgetRead{Project: project, Managed: true, Reason: reason})
	if err != nil {
		t.Fatalf("a version-1 marker was refused, and D1545 takes it with a warning: %v", err)
	}
	if err := gate.Refuse(); err != nil {
		t.Fatalf("the gate a version-1 read opened refuses: %v", err)
	}
	w := gate.Warning()
	if w == "" {
		t.Fatal("the gate opened with no warning, so the session is told nothing about the missing budget")
	}
	for _, want := range []string{project, "has no budget recorded", reason,
		"fugaro managed flag <gcp-project-id> --budget N --budget-currency CCC",
		"fugaro setup --create-project --managed --budget N --budget-currency CCC --billing-account ID"} {
		if !strings.Contains(w, want) {
			t.Errorf("the warning %q does not carry %q", w, want)
		}
	}
	if strings.Contains(w, "FUGARO_") || strings.Contains(w, "export ") {
		t.Errorf("the warning %q names an environment variable, which its reader would take as a way around the owner", w)
	}
	// The gate carries no amount, because there was none to carry, and it is
	// open on the warning alone.
	if gate.Budget() != (Budget{}) {
		t.Errorf("the warned gate carries %+v, want the zero Budget", gate.Budget())
	}
	// And a warning is not a way through the other failures: a read that also
	// errored, one whose recorded figure fails Check, and a project the marker
	// did not place all still refuse.
	for _, r := range []BudgetRead{
		{Project: project, Managed: true, Reason: reason, Err: errors.New("5xx")},
		{Project: project, Managed: true, Reason: reason, OK: true, Amount: 0, Currency: "USD"},
		{Project: project, Reason: reason},
	} {
		if _, err := ApplyGate(r); err == nil {
			t.Errorf("a read %+v opened the gate", r)
		}
	}
}

// M4 and the marker's own rule: a marker this build cannot place leaves the
// gate CLOSED and an error, whatever its reason — an unreadable object, a
// version this build does not know, and one naming another project are three
// faces of the same fail-closed answer, and none of them opens the gate. This
// is the direction every mistake here has to fall in (Global Constraint 3).
func TestMarkerChecksFailClosed(t *testing.T) {
	for _, tc := range []struct {
		what string
		read BudgetRead
	}{
		{
			what: "the marker cannot be read",
			read: BudgetRead{Project: project, ManagedReason: "gs://fugaro-runs-proj-1234/fugaro/managed.json is not there (or cannot be read)"},
		},
		{
			what: "the marker is a version this build does not know",
			read: BudgetRead{Project: project, ManagedReason: "gs://fugaro-runs-proj-1234/fugaro/managed.json is not a marker for aurora: version 9, and this build reads up to 2"},
		},
		{
			what: "the marker names another project",
			read: BudgetRead{Project: project, ManagedReason: "gs://fugaro-runs-proj-1234/fugaro/managed.json names project proj-9999, not proj-1234"},
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			gate := closedGate(t, tc.read)
			_, err := ApplyGate(tc.read)
			if err == nil {
				t.Fatal("the marker check opened the gate")
			}
			if !strings.Contains(err.Error(), "is not managed") || !strings.Contains(err.Error(), tc.read.ManagedReason) {
				t.Errorf("the refusal %v does not carry the marker's own reason %q", err, tc.read.ManagedReason)
			}
			if gate.Warning() != "" {
				t.Errorf("a refused read left a warning %q, want none", gate.Warning())
			}
		})
	}
}

// One thing that opens the gate is a read that answered with an amount above 0
// and a currency — and then the pair is carried, whole, so what the apply runs
// under is the amount the owner chose and not a default. (The other thing is
// the version-1 warning, TestApplyGateOpensWithAWarningOnAVersionOneMarker.)
func TestApplyGateOpensOnlyOnARecordedAmount(t *testing.T) {
	gate, err := ApplyGate(BudgetRead{Project: project, Amount: 12000, Currency: "EUR", OK: true, Managed: true})
	if err != nil {
		t.Fatalf("a read that answered with an amount was refused: %v", err)
	}
	if !gate.read || gate.budget != (Budget{Amount: 12000, Currency: "EUR"}) {
		t.Fatalf("the gate carries %+v (read %v), want the read's own pair", gate.budget, gate.read)
	}
	if err := gate.Refuse(); err != nil {
		t.Errorf("the gate a successful read opened refused: %v", err)
	}
}

// Budget.Valid is the pair the marker's own reader and writer refuse, and the
// second check of the value (Enable.Refuse) uses the same method: one rule,
// so the two cannot drift.
func TestBudgetValidIsTheMarkersOwnPair(t *testing.T) {
	for _, tc := range []struct {
		b    Budget
		want bool
	}{
		{Budget{Amount: 1, Currency: "USD"}, true},
		{Budget{Amount: 10_000_000, Currency: "EUR"}, true},
		{Budget{Amount: 1, Currency: "usd"}, false},
		{Budget{Amount: 1, Currency: "US\n"}, false},
		{Budget{Amount: 1, Currency: "EURO"}, false},
		{Budget{Amount: 1, Currency: "ÉUR"}, false},
		{Budget{Amount: 10_000_001, Currency: "USD"}, false},
		{Budget{Amount: 9_000_000_000_000_000_000, Currency: "USD"}, false},
		{Budget{Amount: 0, Currency: "USD"}, false},
		{Budget{Amount: -1, Currency: "USD"}, false},
		{Budget{Amount: 100, Currency: ""}, false},
		{Budget{}, false},
	} {
		if got := tc.b.Valid(); got != tc.want {
			t.Errorf("%+v.Valid() = %v, want %v", tc.b, got, tc.want)
		}
	}
}

// The seam: EVERY path into the apply carries the same gate, because the check
// is the first thing managed.Apply reads and not something a caller does. This
// is the assertion that the managed apply and the restore (Task 5.2, which
// applies through `fugaro managed apply` and this package's own Apply) cannot
// come apart: a Request built by either, holding a gate a failed read left
// closed, is refused before anything is assumed, planned or applied — and the
// terraform the Request would have run is never built.
func TestApplyRefusesEveryCallerWhoseBudgetReadDidNotAnswer(t *testing.T) {
	// The gate a read of a project the marker did not place leaves: closed. A
	// managed project with no recorded amount does NOT leave a closed gate any
	// more (D1545 opens it with a warning), so the closed gate this test needs
	// is the not-managed answer.
	gate := closedGate(t, BudgetRead{Project: project, ManagedReason: "no creation fact for aurora"})
	n := 0
	// Over the whole product of the run's conditions and the three states the
	// fact can be in, because a caller that reaches Apply by another route
	// passes different ones.
	for _, c := range allConditions() {
		for _, fact := range []Fact{
			{Managed: true},
			{Managed: false, Reason: "no creation fact"},
			{Err: errors.New("the read failed")},
		} {
			n++
			r := newApplyRig(t, plainRoot())
			r.planWithChanges(t, bucket(planBucket))
			req := r.request(t)
			req.Enable = gate
			req.Fact = fact
			req.Conditions = c
			built := false
			inner := req.Terraform
			req.Terraform = func(cred Credential) (*tf.TF, func(), error) {
				built = true
				return inner(cred)
			}
			res, err := Apply(context.Background(), req)
			if !errors.Is(err, ErrNotEnabled) {
				t.Fatalf("conditions %+v fact %+v: %v, want the gate's refusal", c, fact, err)
			}
			if !strings.Contains(err.Error(), NotEnabled) {
				t.Errorf("the refusal %v is not the gate's own sentence", err)
			}
			if res.Changed {
				t.Error("a closed gate reports a change")
			}
			if built {
				t.Error("the closed gate built a terraform")
			}
			if calls := r.calls(t); len(calls) != 0 {
				t.Errorf("the closed gate ran terraform: %v", calls)
			}
		}
	}
	if want := len(allConditions()) * 3; n != want {
		t.Fatalf("the seam ran %d combinations, want %d", n, want)
	}
}

// M4: the read's own error (BudgetRead.Err) reaches the refusal through
// safetext.Field: its text can carry a 5xx body, so a C1 CSI, a bidi
// override, a newline and a 10,000-character string come out neutralised and
// cut — and the error is carried, not dropped.
func TestApplyGateSanitisesTheReadError(t *testing.T) {
	for _, payload := range []string{"csi\u009b31m", "bidi‮evil", "line\nforged", strings.Repeat("z", 10000)} {
		_, err := ApplyGate(BudgetRead{Project: project, Err: errors.New("marker read: " + payload)})
		if err == nil {
			t.Fatal("a failed read opened the gate")
		}
		got := err.Error()
		if strings.ContainsAny(got, "\u009b‮\n") {
			t.Errorf("the refusal carries a raw control, bidi or newline: %q", got)
		}
		if len([]rune(got)) > 1000 {
			t.Errorf("the refusal is %d runes: the error was not cut", len([]rune(got)))
		}
		if !strings.Contains(got, "marker read:") {
			t.Errorf("the read's own error was dropped: %q", got)
		}
	}
}
