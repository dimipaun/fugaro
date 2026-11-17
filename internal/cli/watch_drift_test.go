package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// TestWatchReadsYesterdayForTheDriftLine: the drift line's window is today and
// yesterday (R6, F30), and `fugaro watch` is the reader that has both: the
// command asks for the day before's repository counters beside today's, so the
// line a plain frame and a --json document carry is summed over the two.
func TestWatchReadsYesterdayForTheDriftLine(t *testing.T) {
	const model = "openai/gpt-5.1"
	f := newBudgetFixture(t, "")
	seedWatch(f, "fix the bug")
	day := leaves(model, 500_000, 700_000, 250_000, 300_000)
	f.db.Set(budget.PathSpendRepo(today(), appSlug), map[string]any{
		"counted": 25 * usd1, "spent": 10 * usd1, "notional": 3 * usd1, "byModel": day})
	f.db.Set(budget.PathSpendRepo(today()-1, appSlug),
		map[string]any{"byModel": leaves(model, 600_000, 400_000, 500_000, 600_000)})

	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Drift []struct {
			Model     string  `json:"model"`
			Reported  float64 `json:"reported_usd"`
			TableLo   float64 `json:"table_lo_usd"`
			TableHi   float64 `json:"table_hi_usd"`
			Factor    float64 `json:"factor"`
			Direction string  `json:"direction"`
		} `json:"drift"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Drift) != 1 {
		t.Fatalf("drift = %+v in\n%s", doc.Drift, out)
	}
	d := doc.Drift[0]
	if d.Model != model || d.Direction != "above" || d.Reported != 1.1 || d.TableLo != 0.75 || d.TableHi != 0.9 {
		t.Fatalf("drift = %+v, want today's and yesterday's leaves summed", d)
	}

	// The plain frame draws the same line, off the same two days.
	out, _, err = execute(t, "watch", "--once", "--no-pr-state")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "drift: "+model) || !strings.Contains(out, "1.22x") {
		t.Fatalf("plain frame does not draw the drift line:\n%s", out)
	}
}

// leaves is one model's day leaves as the counters carry them.
func leaves(model string, micros, rep, lo, hi int64) map[string]any {
	return map[string]any{budget.ModelKey(model): map[string]any{
		"micros": micros, "rep": rep, "tlo": lo, "thi": hi}}
}
