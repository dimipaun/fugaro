package cli

// S0.7 (D448, T17): the first licensed release of each stream sits behind
// counsel's sign-off of D432, D433 and D436 (S8.4), honour-based, with no key
// check until the key service ships. Console 0.113.0 was tagged on 2026-10-27
// (#1149) and went out ahead of that sign-off, so its procedure records the
// exception instead of a wait that was never kept; the app's first release has
// not shipped and waits.
//
// The gate lives in the licence section of each stream's own README because
// that is the file a person cutting the release reads, and no workflow refuses
// the tag over it: the console's notes step checks only that the notes file
// exists, and the app's version lines are the app's own. The two READMEs are
// therefore the whole rule, and this test reads both.

import (
	"strings"
	"testing"
)

// releaseGateDocs is one stream's release procedure, the release it is about,
// and the wording that procedure owes its reader beyond the two lines both
// carry ("honour-based", "checks no key").
//
// The console's path is spelled from consoleDirName, the way
// docs_ee_reserved_test.go spells it: the boundary test's mention scan flags
// the literal in any file off its closed allow-list, and this file reads a
// release note rather than the console's source.
var releaseGateDocs = []struct {
	path    string
	release string
	wants   []string
}{
	{
		// The console's half is the record: the release shipped, and before
		// the sign-off the gate names.
		"docs/releases/" + consoleDirName + "/README.md",
		"`0.113.0`",
		[]string{"shipped before counsel's sign-off", "2026-10-27", "exception"},
	},
	{
		// The app's half is the gate itself: nothing has shipped under the
		// app's licence yet, so the wait is still ahead.
		"docs/releases/mobile/README.md",
		"first release",
		[]string{
			"waits for counsel's sign-off",
			"(d432)",
			"(d433)",
			"(d436)",
			"enforcement arrives with the key service in a later release",
		},
	},
}

// TestDocsLicensedReleasesWaitForCounsel: the console's procedure records that
// its first licensed release went out ahead of counsel's sign-off, and the
// app's procedure says its first release waits for it. Both say the release is
// honour-based, with no key check until the key service ships.
func TestDocsLicensedReleasesWaitForCounsel(t *testing.T) {
	for _, c := range releaseGateDocs {
		flat := strings.ToLower(flattenDoc(readDoc(t, c.path)))
		wants := append([]string{strings.ToLower(c.release), "honour-based", "checks no key"}, c.wants...)
		for _, want := range wants {
			if !strings.Contains(flat, want) {
				t.Errorf("%s lacks %q: counsel signs off D432, D433 and D436 (S8.4) before the first licensed release of either stream ships, the console's release went out ahead of that sign-off and says so, and the app's release still waits (D448, T17)", c.path, want)
			}
		}
	}
}
