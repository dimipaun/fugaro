package budget

import "fmt"

// WarnShare is the share of a daily cap at which Fugaro warns and refuses
// nothing: the launch output, `fugaro budget show` and `fugaro watch` all
// warn from this share on, from this one threshold (reconciliation R5). It
// adds no cap of its own; the cap itself is what refuses, at 100%.
const WarnShare = 0.80

// DailyWarnNote words the one warning line for a daily cap counted at or past
// WarnShare of it, and is "" when the counter is below the share. An unset cap
// never reaches here — RepoDaily and GlobalDaily answer ok=false for it — and
// a cap of zero is a cap, not an unset one: the rules refuse every write
// against it, so it is 100% counted, the same 100% watch's bar draws for it
// (build.go's bar). scope names whose cap it is: "the project's", "the
// repository's", "acme/app's".
func DailyWarnNote(scope string, counted, cap Micros) string {
	if float64(counted) < WarnShare*float64(cap) {
		return ""
	}
	pct := 100.0
	if cap > 0 {
		pct = float64(counted) / float64(cap) * 100
	}
	return fmt.Sprintf("%s daily cap: %s of %s counted (%.1f%%); past %d%% warns, the cap keeps applying",
		scope, dollars(counted), dollars(cap), pct, int(WarnShare*100))
}
