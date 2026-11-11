package budget_test

// W7: the reconciliation alarm (docs/workspaces/spec.md §5, D1301's amendment).
// The meter is the basis, so the comparison of the service's own count against
// a member's ledger is a check: a disagreement beyond a stated bound is
// reported, never silently averaged and never silently trusted in either
// direction, and a member the comparison cannot be made for is reported rather
// than counted as zero.
//
// The reading is the caller's — this file holds the comparison — so every test
// here builds the two sides as the caller would have read them and holds what
// the alarm says about them.

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
)

// wsAlarmSpend is the service's own count: one member's settled figure and, when
// it is asked for, the reservations in flight.
func wsAlarmSpend(t *testing.T, settled, reserved map[string]float64) budget.WorkspaceSpend {
	t.Helper()
	s := budget.WorkspaceSpend{
		Month:        budget.MonthKey(wsMeterNow),
		Members:      map[string]budget.Micros{},
		Reservations: map[string]budget.WorkspaceReservation{},
	}
	for member, usd := range settled {
		s.Members[member] = wsMeterUSD(t, usd)
	}
	i := 0
	for member, usd := range reserved {
		i++
		s.Reservations[string(rune('a'+i))] = budget.WorkspaceReservation{
			Member: member, Amount: wsMeterUSD(t, usd), At: wsMeterNow.UnixMilli(),
		}
	}
	return s
}

// wsAlarmReported is the alarm's report for one member, or a fatal failure when
// the member is not in it.
func wsAlarmReported(t *testing.T, a budget.WorkspaceAlarm, member string) budget.WorkspaceAlarmMember {
	t.Helper()
	for _, m := range a.Reported {
		if m.Member == member {
			return m
		}
	}
	t.Fatalf("the alarm says nothing about member %s: %+v", member, a.Reported)
	return budget.WorkspaceAlarmMember{}
}

// A member whose ledger and the service's own count disagree beyond the bound is
// reported, with both figures, and the member that agrees is not.
func TestReconciliationAlarmReportsAMemberWhoseLedgerDisagrees(t *testing.T) {
	in := budget.WorkspaceReconcileInput{
		Name:  wsMeterName,
		Month: budget.MonthKey(wsMeterNow),
		Spend: wsAlarmSpend(t, map[string]float64{wsMeterMember: 5, wsMeterOther: 2}, nil),
		Ledger: []budget.WorkspaceLedger{
			{Member: wsMeterMember, Spent: wsMeterUSD(t, 1)},
			{Member: wsMeterOther, Spent: wsMeterUSD(t, 2)},
		},
	}
	a := budget.ReconcileWorkspace(in)
	if a.Clear() {
		t.Fatal("the alarm is clear although a member's ledger counts $1 of the $5 the workspace paid for")
	}
	if len(a.Reported) != 1 {
		t.Fatalf("the alarm reports %d members, want the one that disagrees: %+v", len(a.Reported), a.Reported)
	}
	got := wsAlarmReported(t, a, wsMeterMember)
	if got.Why != budget.AlarmDisagrees {
		t.Fatalf("the member is reported as %q, want %q", got.Why, budget.AlarmDisagrees)
	}
	if got.Service != wsMeterUSD(t, 5) || got.Ledger != wsMeterUSD(t, 1) {
		t.Fatalf("the report holds %v and %v, want the service's $5 and the ledger's $1", got.Service.USD(), got.Ledger.USD())
	}
	if got.Delta != -wsMeterUSD(t, 4) {
		t.Fatalf("the delta is %v, want -$4: a member that counted less is the direction the alarm exists for", got.Delta.USD())
	}
	if !strings.Contains(got.Sentence, "$4.00") || !strings.Contains(got.Sentence, "bound of $0.00") {
		t.Fatalf("the report reads %q, and it names neither the difference nor the bound it passed", got.Sentence)
	}

	// The other direction is reported too: never silently trusted in either
	// direction is both of them.
	in.Ledger[0] = budget.WorkspaceLedger{Member: wsMeterMember, Spent: wsMeterUSD(t, 9)}
	a = budget.ReconcileWorkspace(in)
	if got := wsAlarmReported(t, a, wsMeterMember); got.Delta != wsMeterUSD(t, 4) {
		t.Fatalf("a member whose ledger counts MORE is reported as %v, want +$4", got.Delta.USD())
	}

	// And a difference inside the bound is not a report at all.
	in.Slack = wsMeterUSD(t, 4)
	if a := budget.ReconcileWorkspace(in); !a.Clear() {
		t.Fatalf("a $4 difference inside a stated $4 of slack is reported: %+v", a.Reported)
	}
}

// The bound is a number the alarm states, and the calls in flight are part of
// it: a call the service has taken room for and settled may not be in the
// member's ledger yet, and the two sides can legitimately differ by that much
// and no more.
func TestReconciliationAlarmBoundIsTheCallsInFlightPlusTheStatedSlack(t *testing.T) {
	in := budget.WorkspaceReconcileInput{
		Name:  wsMeterName,
		Month: budget.MonthKey(wsMeterNow),
		Spend: wsAlarmSpend(t, map[string]float64{wsMeterMember: 5}, map[string]float64{wsMeterMember: 1}),
		Ledger: []budget.WorkspaceLedger{
			{Member: wsMeterMember, Spent: wsMeterUSD(t, 4.5)},
		},
	}
	a := budget.ReconcileWorkspace(in)
	if a.Bound != wsMeterUSD(t, 1) {
		t.Fatalf("the bound is %v with $1 in flight and no slack, want $1", a.Bound.USD())
	}
	if !a.Clear() {
		t.Fatalf("a $0.50 difference with $1 in flight is reported: %+v", a.Reported)
	}

	// One cent beyond the bound is reported, and the bound it passed is in the
	// report.
	in.Ledger[0].Spent = wsMeterUSD(t, 3.99)
	a = budget.ReconcileWorkspace(in)
	if a.Clear() {
		t.Fatal("a $1.01 difference with $1 in flight is not reported")
	}
	if got := wsAlarmReported(t, a, wsMeterMember); !strings.Contains(got.Sentence, "$1.00") {
		t.Fatalf("the report reads %q and does not state the bound it passed", got.Sentence)
	}
}

// The bound a member is compared against is the member's OWN calls in flight,
// and never the workspace's: what another member has in flight is not a
// difference this member's ledger can account for, and a workspace-wide bound
// would let one member's under-reporting hide under another member's calls —
// the direction the alarm exists for.
func TestReconciliationAlarmBoundsAMemberByItsOwnCallsInFlight(t *testing.T) {
	in := budget.WorkspaceReconcileInput{
		Name:  wsMeterName,
		Month: budget.MonthKey(wsMeterNow),
		Now:   wsMeterNow,
		Spend: wsAlarmSpend(t, map[string]float64{wsMeterMember: 1, wsMeterOther: 3}, map[string]float64{wsMeterOther: 2}),
		Ledger: []budget.WorkspaceLedger{
			// $1 short, with nothing of its own in flight.
			{Member: wsMeterMember, Spent: 0},
			// $2 short of the service's $3, which its own $2 in flight accounts
			// for exactly.
			{Member: wsMeterOther, Spent: wsMeterUSD(t, 1)},
		},
	}
	a := budget.ReconcileWorkspace(in)
	got := wsAlarmReported(t, a, wsMeterMember)
	if got.Why != budget.AlarmDisagrees || got.Delta != -wsMeterUSD(t, 1) {
		t.Fatalf("member %s is reported as %+v, want a $1 disagreement", wsMeterMember, got)
	}
	if got.Bound != 0 {
		t.Fatalf("member %s was compared against %v, want its own calls in flight, of which it has none", wsMeterMember, got.Bound.USD())
	}
	if !strings.Contains(got.Sentence, "bound of $0.00") {
		t.Fatalf("the report reads %q and does not state the member's own bound", got.Sentence)
	}
	for _, m := range a.Reported {
		if m.Member == wsMeterOther {
			t.Fatalf("member %s is reported although its own calls in flight account for its whole difference: %+v", wsMeterOther, m)
		}
	}
	if a.Bound != wsMeterUSD(t, 2) {
		t.Fatalf("the alarm's own bound is %v, want the widest a member was compared against ($2)", a.Bound.USD())
	}

	// And a hold whose call can no longer be in flight is not a call in flight:
	// it widens no bound either.
	in.Spend.Reservations["stale"] = budget.WorkspaceReservation{
		Member: wsMeterMember, Amount: wsMeterUSD(t, 5),
		At: wsMeterNow.Add(-budget.WorkspaceReservationTTL - time.Minute).UnixMilli(),
	}
	got = wsAlarmReported(t, budget.ReconcileWorkspace(in), wsMeterMember)
	if got.Why != budget.AlarmDisagrees || got.Bound != 0 {
		t.Fatalf("a hold past its age is still a bound: %+v", got)
	}
}

// A member the workspace's table does not bind is reported as an alarm rather
// than counted as zero: the service cannot resolve it, so nothing can be
// compared for it, and a zero would read as a member that spent nothing.
func TestReconciliationAlarmReportsAnUnboundMember(t *testing.T) {
	in := budget.WorkspaceReconcileInput{
		Name:  wsMeterName,
		Month: budget.MonthKey(wsMeterNow),
		Spend: wsAlarmSpend(t, map[string]float64{wsMeterMember: 5}, nil),
		Ledger: []budget.WorkspaceLedger{
			{Member: wsMeterMember, Unbound: true},
		},
	}
	a := budget.ReconcileWorkspace(in)
	got := wsAlarmReported(t, a, wsMeterMember)
	if got.Why != budget.AlarmUnbound {
		t.Fatalf("the member is reported as %q, want %q", got.Why, budget.AlarmUnbound)
	}
	if got.Ledger != 0 {
		t.Fatalf("an unbound member's ledger reads %v, want nothing at all", got.Ledger.USD())
	}
	if !strings.Contains(got.Sentence, "not bound") {
		t.Fatalf("the report reads %q and does not say the member is unbound", got.Sentence)
	}

	// A member whose ledger could not be read is reported as itself, and not as
	// a member that spent nothing.
	in.Ledger[0] = budget.WorkspaceLedger{Member: wsMeterMember, Unreadable: errors.New("the member's database refused the read")}
	if got := wsAlarmReported(t, budget.ReconcileWorkspace(in), wsMeterMember); got.Why != budget.AlarmUnreadable {
		t.Fatalf("the member is reported as %q, want %q", got.Why, budget.AlarmUnreadable)
	}

	// A member the service counted whose ledger was not read at all is a
	// comparison that never happened, and it is reported rather than skipped.
	in.Ledger = nil
	got = wsAlarmReported(t, budget.ReconcileWorkspace(in), wsMeterMember)
	if got.Why != budget.AlarmUnreadable || got.Service != wsMeterUSD(t, 5) {
		t.Fatalf("a member whose ledger was not read is reported as %+v, want an unreadable one holding the service's $5", got)
	}
}

// The entry carries the member, its figure and the two reasons there is not one
// — and nothing else. There is no database URL and no bucket in it, so neither
// can be missing from it: the database is derived from the member's verified
// `iss`, and the bucket read the old rollup used is gone.
func TestReconciliationEntryCarriesNoDatabaseAndNoBucket(t *testing.T) {
	typ := reflect.TypeOf(budget.WorkspaceLedger{})
	var names []string
	for i := 0; i < typ.NumField(); i++ {
		names = append(names, typ.Field(i).Name)
	}
	want := []string{"Member", "Spent", "Unbound", "Unreadable"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("the entry's fields are %v, want exactly %v", names, want)
	}
}

// The alarm changes neither figure: the service's count stays the one the cap is
// measured against, and the member's ledger stays authoritative for the
// member's own caps.
func TestReconciliationAlarmChangesNeitherFigure(t *testing.T) {
	spend := wsAlarmSpend(t, map[string]float64{wsMeterMember: 5}, nil)
	ledger := []budget.WorkspaceLedger{{Member: wsMeterMember, Spent: wsMeterUSD(t, 1)}}
	in := budget.WorkspaceReconcileInput{Name: wsMeterName, Month: budget.MonthKey(wsMeterNow), Spend: spend, Ledger: ledger}

	a := budget.ReconcileWorkspace(in)
	if a.Clear() {
		t.Fatal("the fixture is meant to disagree")
	}
	if spend.Members[wsMeterMember] != wsMeterUSD(t, 5) {
		t.Fatalf("the alarm moved the service's own count to %v", spend.Members[wsMeterMember].USD())
	}
	if ledger[0].Spent != wsMeterUSD(t, 1) {
		t.Fatalf("the alarm moved the member's ledger to %v", ledger[0].Spent.USD())
	}
}
