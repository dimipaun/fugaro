package cli

// ee/ is reserved at 0.11.0 (licensing plan L1.2, D306's fallback as ruled by
// T1 choice (iii)): the folder holds a README that says so and no code, no
// licence file and nothing else, until counsel's enterprise text is approved
// and `ee/LICENSE` lands in its own task.
//
// The read goes through readRealEE (docs_ee_repo_test.go), so this test's name
// is in eeReadingTests and the "core without ee/" workflow skips it there.

import (
	"slices"
	"strings"
	"testing"
)

// TestDocsEEReserved is the reservation itself: the README exists and says what
// the folder is, ee/ holds nothing else, and there is no licence file to read.
func TestDocsEEReserved(t *testing.T) {
	// Read flattened and folded, so a re-wrapped sentence or a capitalised
	// heading is not what this test is about: the folder's reservation is.
	flat := strings.ToLower(flattenDoc(readRealEE(t, "README.md")))
	for _, want := range []string{
		"reserved",
		"net-new organisation features",
		"their own licence",
		"nothing here is licensed yet",
		"no code",
		"no licence file",
		"ee/license",
		"counsel",
		"nothing outside `ee/` imports anything under `ee/`",
		"spdx-license-identifier: licenseref-fugaro-enterprise",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("ee/README.md lacks %q", want)
		}
	}

	// The listing is the "and nothing else" half, and it is also how the test
	// asserts that ee/LICENSE is absent: a reserved ee/ holds the one README, so
	// a second tracked file (a licence file included) fails here rather than
	// being read as if the folder were licensed.
	entries := strings.Fields(readRealEE(t, ""))
	if want := []string{"README.md"}; !slices.Equal(entries, want) {
		t.Errorf(`ee/ holds %v; a reserved ee/ holds exactly %v.

ee/ is reserved with no licence file until counsel's text is approved (licensing plan L1.2, T1 choice (iii)), so a file landing here means either the licence arrived and this test and ee/README.md move with it, or code arrived before there is anything licensing it.`, entries, want)
	}
}
