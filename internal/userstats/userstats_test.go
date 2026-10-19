package userstats

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestUnknownUserIsReportsOwnSpelling pins this package's "nobody recorded"
// key to `fugaro report --by person`'s: two commands that group the same fact
// must group it under one name, or a reader comparing them sees two groups.
func TestUnknownUserIsReportsOwnSpelling(t *testing.T) {
	if UnknownUser != budget.UnknownPerson {
		t.Errorf("UnknownUser = %q, report's is %q", UnknownUser, budget.UnknownPerson)
	}
}

// TestAggregateIsPerUser is the attribution rule: the launcher the run itself
// recorded is the group key, automated identities are their own group, and a
// run that recorded nobody is UnknownUser.
func TestAggregateIsPerUser(t *testing.T) {
	res := Aggregate([]Run{
		{User: "dana@example.com", Status: string(runstore.StatusSucceeded), CountedUSD: 1.50, CountedCost: true},
		{User: "dana@example.com", Status: string(runstore.StatusFailed), CountedUSD: 0.50, CountedCost: true},
		// An automated launch runs as its own identity: a service account is
		// a group like a person, never folded into one.
		{User: "fugaro-ci@app.iam.gserviceaccount.com", Status: string(runstore.StatusSucceeded), CountedUSD: 9, CountedCost: true},
		{User: UnknownUser, Status: string(runstore.StatusSucceeded), CountedUSD: 3, CountedCost: true},
	})
	if len(res.Rows) != 3 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if got := res.Rows[0].User; got != "dana@example.com" {
		t.Errorf("first row = %q, want dana (unknown sorts last)", got)
	}
	if got := res.Rows[2].User; got != UnknownUser {
		t.Errorf("last row = %q, want %q", got, UnknownUser)
	}
	dana := res.Rows[0]
	if dana.Runs != 2 || dana.Succeeded != 1 || dana.Failed != 1 || dana.CountedUSD != 2 {
		t.Errorf("dana = %+v", dana)
	}
	if dana.AvgCountedUSD != 1 {
		t.Errorf("dana's average = %v, want 1.00", dana.AvgCountedUSD)
	}
	// An empty User is the launcher nobody recorded, never a group of its own.
	empty := Aggregate([]Run{{User: "", Status: string(runstore.StatusSucceeded)}})
	if len(empty.Rows) != 1 || empty.Rows[0].User != UnknownUser {
		t.Errorf("an empty launcher = %+v, want one %s row", empty.Rows, UnknownUser)
	}
}

// TestUnknownUserSortsUnderEveryName is the ordering rule on its own: the
// unattributed row is last whatever the names around it are, including one
// that sorts after the word "unknown" on its own (the package's own key is a
// word, so a launcher called zed would otherwise sort under it).
func TestUnknownUserSortsUnderEveryName(t *testing.T) {
	res := Aggregate([]Run{
		{User: "zed@example.com", Status: string(runstore.StatusSucceeded)},
		{User: UnknownUser, Status: string(runstore.StatusSucceeded)},
		{User: "dana@example.com", Status: string(runstore.StatusSucceeded)},
	})
	var order []string
	for _, row := range res.Rows {
		order = append(order, row.User)
	}
	want := []string{"dana@example.com", "zed@example.com", UnknownUser}
	if len(order) != len(want) {
		t.Fatalf("rows = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("rows = %v, want %v: %q is a name like any other and %q is not a name at all",
				order, want, want[1], UnknownUser)
		}
	}
	// The same rule under a currency that is not USD, since the ordering is
	// per currency and the currencies are ordered first.
	eur := Aggregate([]Run{
		{User: "zed@example.com", Currency: "EUR", Status: string(runstore.StatusSucceeded)},
		{User: UnknownUser, Currency: "EUR", Status: string(runstore.StatusSucceeded)},
	})
	if len(eur.Rows) != 2 || eur.Rows[1].User != UnknownUser {
		t.Errorf("rows = %+v, want %s last within EUR too", eur.Rows, UnknownUser)
	}
}

// TestCountsAddUp holds the invariant the table's numbers are read with: the
// four named outcomes, Other and NoRecord are a partition of Runs.
func TestCountsAddUp(t *testing.T) {
	var runs []Run
	for _, s := range []string{"succeeded", "failed", "halted", "infra_error", "running", "cancelled"} {
		runs = append(runs, Run{User: "dana@example.com", Status: s})
	}
	runs = append(runs, Run{User: "dana@example.com", NoRecord: true})
	res := Aggregate(runs)
	var got Counts
	for _, row := range res.Rows {
		got = Counts{
			Runs:       got.Runs + row.Runs,
			Succeeded:  got.Succeeded + row.Succeeded,
			Failed:     got.Failed + row.Failed,
			Halted:     got.Halted + row.Halted,
			InfraError: got.InfraError + row.InfraError,
			Other:      got.Other + row.Other,
			NoRecord:   got.NoRecord + row.NoRecord,
		}
	}
	want := Counts{Runs: 7, Succeeded: 1, Failed: 1, Halted: 1, InfraError: 1, Other: 2, NoRecord: 1}
	if got != want {
		t.Fatalf("counts = %+v, want %+v", got, want)
	}
	if sum := want.Succeeded + want.Failed + want.Halted + want.InfraError + want.Other + want.NoRecord; sum != want.Runs {
		t.Fatalf("the counts do not add up: %d != %d", sum, want.Runs)
	}
	// A record that has not settled is not a failure: cancelled and running
	// are Other, and only a missing record is NoRecord.
	if res.Rows[0].Failed != 1 || res.Rows[0].NoRecord != 1 {
		t.Errorf("row = %+v", res.Rows[0])
	}
}

// TestCurrenciesAreNeverSummed is the currency rule: two currencies are two
// rows and two totals, and neither total carries a cent of the other.
func TestCurrenciesAreNeverSummed(t *testing.T) {
	res := Aggregate([]Run{
		{User: "dana@example.com", Currency: CurrencyUSD, Status: string(runstore.StatusSucceeded), CountedUSD: 10, CountedCost: true},
		{User: "dana@example.com", Currency: "EUR", Status: string(runstore.StatusSucceeded), CountedUSD: 90, CountedCost: true},
	})
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %+v, want one per currency", res.Rows)
	}
	if res.Rows[0].Currency != "EUR" || res.Rows[1].Currency != CurrencyUSD {
		t.Fatalf("rows = %+v, want EUR before USD", res.Rows)
	}
	for _, row := range res.Rows {
		var want float64
		switch row.Currency {
		case CurrencyUSD:
			want = 10
		case "EUR":
			want = 90
		}
		if row.CountedUSD != want || row.Runs != 1 {
			t.Errorf("%s row = %+v, want %v of one run", row.Currency, row, want)
		}
	}
	if len(res.Totals) != 2 {
		t.Fatalf("totals = %+v, want one per currency", res.Totals)
	}
	if res.Totals[0].CountedUSD != 90 || res.Totals[0].Currency != "EUR" || res.Totals[1].CountedUSD != 10 {
		t.Errorf("totals = %+v", res.Totals)
	}
	// The empty currency is USD, the unit every recorded cost is in.
	if got := Aggregate([]Run{{User: "dana@example.com"}}).Rows[0].Currency; got != CurrencyUSD {
		t.Errorf("an unset currency = %q, want %q", got, CurrencyUSD)
	}
}

// TestNotionalIsNotCounted is the spend rule: a subscription's model dollars
// are a column of their own, they are in no counted total, and a notional run
// is not averaged in as a run that cost nothing.
func TestNotionalIsNotCounted(t *testing.T) {
	res := Aggregate([]Run{
		{User: "dana@example.com", Status: string(runstore.StatusSucceeded), CountedUSD: 4, CountedCost: true},
		// A run with no money is not a run that cost $0.00.
		{User: "dana@example.com", Status: string(runstore.StatusSucceeded), CountedUSD: 6, CountedCost: true},
		{User: "dana@example.com", Status: string(runstore.StatusSucceeded), NotionalUSD: 100},
		{User: "dana@example.com", NoRecord: true},
	})
	row := res.Rows[0]
	if row.CountedUSD != 10 || row.NotionalUSD != 100 {
		t.Errorf("row = %+v, want 10 counted and 100 notional kept apart", row)
	}
	if row.CountedRuns != 2 || row.AvgCountedUSD != 5 {
		t.Errorf("average = %v over %d runs, want 5.00 over 2: the notional run and the "+
			"unreadable one are not runs that cost nothing", row.AvgCountedUSD, row.CountedRuns)
	}
	if row.Runs != 4 {
		t.Errorf("runs = %d, want 4: every run started, averaged or not", row.Runs)
	}
	// No run to average is not $0.00 a run.
	only := Aggregate([]Run{{User: "dana@example.com", NotionalUSD: 5}}).Rows[0]
	if only.CountedRuns != 0 || only.AvgCountedUSD != 0 {
		t.Errorf("a notional-only group = %+v, want no counted run to average", only)
	}
}

// TestTotalsAreOnePerCurrencyAndAddUp holds the total row to the rows above
// it: a reader adds the column and gets the last line.
func TestTotalsAreOnePerCurrencyAndAddUp(t *testing.T) {
	res := Aggregate([]Run{
		{User: "dana@example.com", Status: string(runstore.StatusSucceeded), CountedUSD: 1.40, CountedCost: true},
		{User: "sam@example.com", Status: string(runstore.StatusSucceeded), CountedUSD: 2.60, CountedCost: true},
		{User: UnknownUser, Status: string(runstore.StatusFailed), CountedUSD: 4.00, CountedCost: true},
	})
	if len(res.Totals) != 1 {
		t.Fatalf("totals = %+v, want one", res.Totals)
	}
	tot := res.Totals[0]
	var sum float64
	var runs, counted int
	for _, row := range res.Rows {
		sum += row.CountedUSD
		runs += row.Runs
		counted += row.CountedRuns
	}
	if tot.CountedUSD != sum || tot.Runs != runs || tot.CountedRuns != counted {
		t.Errorf("total = %+v, rows sum to %v over %d runs (%d counted)", tot, sum, runs, counted)
	}
	if tot.User != "" {
		t.Errorf("a total names a user: %q", tot.User)
	}
	if tot.CountedUSD != 8.00 || tot.Currency != CurrencyUSD || tot.Succeeded != 2 || tot.Failed != 1 {
		t.Errorf("total = %+v", tot)
	}
	if tot.AvgCountedUSD != 2.67 {
		t.Errorf("total average = %v, want 2.67 (8.00 over 3 counted runs)", tot.AvgCountedUSD)
	}
}

// TestIdentityIsSanitisedAndCapped is the untrusted-text rule: the key is
// display text a launcher wrote, so it is made one printable line and cut to
// a bounded size before it can reach a row.
func TestIdentityIsSanitisedAndCapped(t *testing.T) {
	if got := IdentityOf("dana@example.com"); got != "dana@example.com" {
		t.Errorf("an ordinary address = %q", got)
	}
	hostile := "dana\x1b[2J\x07@example.com\r\nsecond line‮"
	got := IdentityOf(hostile)
	for _, bad := range []string{"\x1b", "\x07", "\r", "\n", "‮"} {
		if strings.Contains(got, bad) {
			t.Errorf("IdentityOf(%q) = %q, still carries %q", hostile, got, bad)
		}
	}
	if !strings.Contains(got, "dana@example.com") || !utf8.ValidString(got) {
		t.Errorf("IdentityOf(%q) = %q, want the printable part kept", hostile, got)
	}
	long := strings.Repeat("x", 10_000)
	if n := len([]rune(IdentityOf(long))); n > MaxIdentityRunes {
		t.Errorf("a 10k identity is %d runes, want at most %d", n, MaxIdentityRunes)
	}
	for _, empty := range []string{"", "   ", "\t\n", "\x1b\x07", "​"} {
		if got := IdentityOf(empty); got != UnknownUser {
			t.Errorf("IdentityOf(%q) = %q, want %q", empty, got, UnknownUser)
		}
	}
}

// TestCallerKeyIsTheKeyOrNothing is the difference between the caller's own
// key and a launcher's: an identity with nothing printable in it has no key of
// its own, and is not the key the unattributed runs are grouped under.
func TestCallerKeyIsTheKeyOrNothing(t *testing.T) {
	if got := CallerKey("dana@example.com"); got != "dana@example.com" {
		t.Errorf("CallerKey = %q", got)
	}
	// Sanitised and capped exactly as a launcher's identity is, so `you` can be
	// matched against a row key.
	raw := "dana@example.com​" + strings.Repeat("z", 500)
	if got, want := CallerKey(raw), IdentityOf(raw); got != want {
		t.Errorf("CallerKey(%q) = %q, IdentityOf = %q: the two are one text for an "+
			"identity that has a key", raw, got, want)
	}
	// Nothing printable is no key at all -- and specifically not UnknownUser,
	// which is a group of runs rather than a person.
	for _, me := range []string{"", "   ", "\t\n", "\x1b\x07", "​", "​​\n"} {
		if got := CallerKey(me); got != "" {
			t.Errorf("CallerKey(%q) = %q, want the empty key: an identity with no printable "+
				"text in it is not the %q group", me, got, UnknownUser)
		}
	}
	// The word itself is a key: it is a caller's own text, whatever it says.
	if got := CallerKey(UnknownUser); got != UnknownUser {
		t.Errorf("CallerKey(%q) = %q, want the identity it is", UnknownUser, got)
	}
}

// TestAggregateOfNothing is a window with no runs: no rows, no total, and no
// nil the caller has to guard.
func TestAggregateOfNothing(t *testing.T) {
	res := Aggregate(nil)
	if len(res.Rows) != 0 || len(res.Totals) != 0 {
		t.Errorf("Aggregate(nil) = %+v", res)
	}
}
