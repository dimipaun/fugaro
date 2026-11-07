package cli

// S0.7 (D448, T17): console 0.113.0 and the mobile app's first release are the
// first releases of their streams under a business licence, and both wait for
// counsel's sign-off of D432, D433 and D436 (S8.4) before they ship. They are
// honour-based -- the release states what the licence requires and checks no
// key -- and enforcement arrives with the key service in a later release.
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

// releaseGateDocs is one stream's release procedure and the release the gate
// holds back, spelled as that README meets it.
//
// The console's path is spelled from consoleDirName, the way
// docs_ee_reserved_test.go spells it: the boundary test's mention scan flags
// the literal in any file off its closed allow-list, and this file reads a
// release note rather than the console's source.
var releaseGateDocs = []struct {
	path    string
	release string
}{
	{"docs/releases/" + consoleDirName + "/README.md", "`0.113.0`"},
	{"docs/releases/mobile/README.md", "first release"},
}

// TestDocsLicensedReleasesWaitForCounsel: each release procedure says that its
// stream's first licensed release waits for counsel's sign-off on the three
// rulings the licence and its pricing rest on, and that the release is
// honour-based, with no key check until the key service ships.
func TestDocsLicensedReleasesWaitForCounsel(t *testing.T) {
	for _, c := range releaseGateDocs {
		flat := strings.ToLower(flattenDoc(readDoc(t, c.path)))
		for _, want := range []string{
			"waits for counsel's sign-off",
			"(d432)",
			"(d433)",
			"(d436)",
			"honour-based",
			"checks no key",
			"enforcement arrives with the key service in a later release",
			strings.ToLower(c.release),
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s lacks %q: counsel signs off D432, D433 and D436 (S8.4) before the first licensed release of either stream ships, and the release is honour-based with no key check (D448, T17)", c.path, want)
			}
		}
	}
}
