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
// leaving the gate closed with its own sentence. The nine failures are the
// task's own list — the marker's version 1, an absent marker, an amount of 0
// or below, a currency that is not there, a marker naming another project, a
// read that could not be made at all, and an object that is malformed,
// oversized or has trailing data (the last three the same answer, because
// ReadBudget refuses a shape it cannot parse before it looks at anything else)
// — plus the two answers the second read adds.
func TestApplyGateRefusesEveryReadThatAnsweredNoAmount(t *testing.T) {
	const reason = "gs://fugaro-runs-proj-1234/fugaro/managed.json records no budget amount for aurora"
	for _, tc := range []struct {
		what string
		read BudgetRead
		want []string
	}{
		{
			what: "the marker is version 1: managed, and no amount recorded",
			read: BudgetRead{Project: project, Managed: true, Reason: reason},
			want: []string{"has no budget recorded", "fugaro managed flag <gcp-project-id> --budget N --budget-currency CCC", "fugaro setup --create-project --managed --budget N --budget-currency CCC --billing-account ID", reason},
		},
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

// The refusal a reader acts on is one line, whatever the read put in it: a
// reason can come from an object someone else wrote, and a newline in it must
// not become a line of this program's output.
func TestApplyGateRefusalIsOneLine(t *testing.T) {
	_, err := ApplyGate(BudgetRead{Project: project, Managed: true, Reason: "no amount\r\nsecond line\x00"})
	if err == nil {
		t.Fatal("ApplyGate refused nothing")
	}
	if strings.ContainsAny(err.Error(), "\r\x00") || strings.Count(err.Error(), "\n") != 0 {
		t.Errorf("the refusal carries the reason's own control characters: %q", err.Error())
	}
}

// The only thing that opens the gate is a read that answered with an amount
// above 0 and a currency — and then the pair is carried, whole, so what the
// apply runs under is the amount the owner chose and not a default.
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
	// The gate a read of a managed project with no recorded amount leaves.
	gate := closedGate(t, BudgetRead{Project: project, Managed: true, Reason: "records no budget amount"})
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
