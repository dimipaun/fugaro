package cli

import (
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
