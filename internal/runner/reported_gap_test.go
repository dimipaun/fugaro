package runner

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pricing"
)

// TestWarnReportedGapAgainstBand (SA2.3, D2640, F16): the run's end-of-run
// warning makes the comparison `fugaro diagnose` makes on the same record --
// the report against the price table's band -- and not the older one against
// the charge. D2640 is why: a complete call on a provider route settles on
// the report, so a report read against the charge agrees with it by
// construction, while the band (what the table would have charged those same
// calls at the selected tier's two ends) is what a gap is real against. The
// distinguishing case is the low one: a report below the band's cheapest end
// warns here where the charge comparison stayed silent, and a report inside
// the band warns in neither direction. A run whose calls carried no band at
// all (none reported a cost, or no row priced them) keeps the charge
// comparison, and only above it: with no band a low report may be partial
// reporting.
func TestWarnReportedGapAgainstBand(t *testing.T) {
	// The band: $1.80-$2.20 would be, at the 10% margin, below $1.62 and
	// above $2.42; the charges beside it are what the run spent on its route.
	const (
		lo      = 1_800_000
		hi      = 2_200_000
		charged = 2_000_000
	)
	for _, c := range []struct {
		name     string
		reported int64
		band     bool
		want     string // the words the one warning carries; "" for no warning
	}{
		{"inside the band", 2_000_000, true, ""},
		{"just above the band", 2_400_000, true, ""},
		{"above the band", 2_500_000, true, "dearest end"},
		{"just below the band", 1_700_000, true, ""},
		{"below the band", 1_500_000, true, "cheapest end"},
		{"no band, above the charge", 2_500_000, false, "over the"},
		{"no band, below the charge", 1_500_000, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			var logs bytes.Buffer
			r := &run{
				d:        Deps{Log: slog.New(slog.NewTextHandler(&logs, nil))},
				reported: pricing.Micros(c.reported),
				routeBy:  map[string]pricing.Micros{"openrouter": charged},
			}
			if c.band {
				r.tableLoBy = map[string]pricing.Micros{"deepseek/deepseek-v4.1-flash": lo}
				r.tableHiBy = map[string]pricing.Micros{"deepseek/deepseek-v4.1-flash": hi}
			}
			r.warnReportedGap()
			got := logs.String()
			if n := strings.Count(got, "code=reported_gap"); n > 1 {
				t.Fatalf("%d warnings, want at most one:\n%s", n, got)
			}
			switch {
			case c.want == "" && strings.Contains(got, "code=reported_gap"):
				t.Fatalf("warned, want no warning:\n%s", got)
			case c.want != "" && !strings.Contains(got, "code=reported_gap"):
				t.Fatalf("no warning, want one naming the %s:\n%s", c.want, got)
			case c.want != "" && !strings.Contains(got, c.want):
				t.Fatalf("the warning does not name the %s:\n%s", c.want, got)
			}
		})
	}
}
