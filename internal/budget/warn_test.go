package budget

import (
	"strings"
	"testing"
)

// TestDailyWarnNote: the one warning line for a daily cap, and the share that
// draws none. A cap of zero is a cap, not an unset one — "unset" is what
// RepoDaily and GlobalDaily answer ok=false for, and the rules refuse every
// write against a zero cap (evaluate.go's `add(counted, amount) > cap`), which
// is why watch draws it at 100% (build.go's bar). So it warns, at 100%, the
// way the bar draws it.
func TestDailyWarnNote(t *testing.T) {
	for _, tc := range []struct {
		name         string
		counted, cap Micros
		want         string
	}{
		{"below the share", 39_950_000, 50_000_000, ""},
		{"at the share", 40_000_000, 50_000_000, "$40.00 of $50.00 counted (80.0%)"},
		{"a zero cap is a cap at 100%", 0, 0, "$0.00 of $0.00 counted (100.0%)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DailyWarnNote("the repository's", tc.counted, tc.cap)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("note = %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("note = %q, want it to hold %q", got, tc.want)
			}
		})
	}
}
