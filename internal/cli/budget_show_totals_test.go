package cli

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// The project total is the sum of every repository's counters. Without --all
// only the config's repositories are listed, so the rows can sum to less than
// the total: show says by how much, and when the stored counters really
// disagree it says that instead.
func TestShowExplainsProjectTotalVersusRows(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	d := today()
	f.db.Set(budget.PathSpendGlobal(d), map[string]any{"counted": 30 * usd1, "spent": 10 * usd1, "notional": 3*usd1 + 560_000})
	f.db.Set(budget.PathSpendRepo(d, appSlug), map[string]any{"counted": 25 * usd1, "spent": 10 * usd1, "notional": 3 * usd1})
	f.db.Set(budget.PathSpendRepo(d, "github/acme/other"), map[string]any{"counted": 5 * usd1, "notional": 560_000})

	out, _, err := execute(t, "budget", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not listed above hold") || !strings.Contains(out, "notional $0.56") {
		t.Fatalf("the unlisted repository's share is not stated:\n%s", out)
	}
	if strings.Contains(out, "do not add up") {
		t.Fatalf("counters add up, but show says they do not:\n%s", out)
	}
	out, _, err = execute(t, "budget", "show", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "not listed above hold") || strings.Contains(out, "do not add up") {
		t.Fatalf("--all lists every repository; nothing to explain:\n%s", out)
	}

	// A real mismatch: the project counter holds notional no repository has.
	f.db.Set(budget.PathSpendRepo(d, "github/acme/other"), map[string]any{"counted": 5 * usd1})
	out, _, err = execute(t, "budget", "show", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "do not add up") || !strings.Contains(out, "notional $0.56") {
		t.Fatalf("a real mismatch is not reported:\n%s", out)
	}
}

// Money that can be negative reads "-$0.01", never "$-0.01" or a bare number.
func TestMoneyNegative(t *testing.T) {
	for m, want := range map[budget.Micros]string{-10_000: "-$0.01", -1_500_000: "-$1.50", -500_000: "-$0.50", 0: "$0", 10_000: "$0.01",
		// Corrupt counters can hold the extremes; -MinInt64 overflows.
		math.MinInt64: "-$9223372036854.775808", math.MinInt64 + 1: "-$9223372036854.775807", math.MaxInt64: "$9223372036854.775807"} {
		if got := money(m); got != want {
			t.Errorf("money(%d) = %q, want %q", m, got, want)
		}
	}
}

// A project counter under the repositories' sum is reported with signed money.
func TestShowNegativeMismatchHasSign(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	d := today()
	f.db.Set(budget.PathSpendGlobal(d), map[string]any{"counted": 5*usd1 - 10_000})
	f.db.Set(budget.PathSpendRepo(d, appSlug), map[string]any{"counted": 5 * usd1})
	out, _, err := execute(t, "budget", "show", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "do not add up") || !strings.Contains(out, "counted -$0.01") {
		t.Fatalf("the negative difference is not signed:\n%s", out)
	}
}

// show --json carries leased_usd on `global` and on every repository row: the
// money open leases hold and have not yet spent, from the two counters the
// document already carries -- counted minus spent, floored at zero, always
// present (0 when nothing is leased, never a negative).
func TestShowJSONCarriesLeasedUSD(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedCaps(f, 100, 20)
	d := today()
	f.db.Set(budget.PathSpendGlobal(d), map[string]any{"counted": 47*usd1 + 310_000, "spent": 47*usd1 + 110_000})
	f.db.Set(budget.PathSpendRepo(d, appSlug), map[string]any{"counted": 47*usd1 + 310_000, "spent": 47*usd1 + 110_000})
	f.db.Set(budget.PathSpendRepo(d, "github/acme/under"), map[string]any{"counted": usd1, "spent": 2 * usd1})

	out, _, err := execute(t, "budget", "show", "--all", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Global struct {
			LeasedUSD float64 `json:"leased_usd"`
		} `json:"global"`
		Repos []struct {
			Slug      string  `json:"slug"`
			LeasedUSD float64 `json:"leased_usd"`
		} `json:"repos"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.Global.LeasedUSD != 0.2 {
		t.Errorf("global.leased_usd = %v, want 0.20 (counted 47.31 - spent 47.11)", doc.Global.LeasedUSD)
	}
	got := map[string]float64{}
	for _, r := range doc.Repos {
		got[r.Slug] = r.LeasedUSD
	}
	if got[appSlug] != 0.2 {
		t.Errorf("repos[%s].leased_usd = %v, want 0.20", appSlug, got[appSlug])
	}
	// counted below spent is a state no normal write makes: the figure is
	// floored, never negative.
	if v, ok := got["github/acme/under"]; !ok || v != 0 {
		t.Errorf("repos[github/acme/under].leased_usd = %v (present %v), want 0", v, ok)
	}

	// Nothing leased: a project with no counters at all still carries the key,
	// as 0.
	empty := newBudgetFixture(t, "")
	seedCaps(empty, 100, 20)
	out, _, err = execute(t, "budget", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"leased_usd": 0`) {
		t.Errorf("a document with nothing leased does not carry leased_usd: 0:\n%s", out)
	}

	// The text view is unchanged: it does not print the figure.
	out, _, err = execute(t, "budget", "show")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "leased") {
		t.Errorf("the text view gained a leased figure:\n%s", out)
	}
}
