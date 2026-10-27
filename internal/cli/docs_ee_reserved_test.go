package cli

// ee/ carries its licence from 2026-10-26 (licensing plan L4.1, brought
// forward by the owner's ruling D311): the folder holds a README that says what
// it is for, the draft `ee/LICENSE` that counsel has not read yet, and no code.
// It is still reserved for net-new organisation features, which is the
// sentence the website's licence band and LICENSING.md lean on.
//
// The read goes through readRealEE (docs_ee_repo_test.go), so this test's name
// is in eeReadingTests and the "core without ee/" workflow skips it there.

import (
	"slices"
	"strings"
	"testing"
)

// eeDraftStatusLine is the first line of `ee/LICENSE` until counsel has read
// the text. The owner removes it then, and this constant goes with it in the
// same commit, so a licence that quietly lost its status line while still a
// draft fails here.
const eeDraftStatusLine = "DRAFT FOR COUNSEL REVIEW: not yet reviewed by counsel"

// TestDocsEELicensed: the README says what the folder is and that the licence
// is a draft, the licence names the Software, the seats and the SPDX
// identifier every ee/ file's notice carries, and ee/ holds nothing else.
func TestDocsEELicensed(t *testing.T) {
	flat := strings.ToLower(flattenDoc(readRealEE(t, "README.md")))
	for _, want := range []string{
		"reserved",
		"net-new organisation features",
		"their own licence",
		"](license)",
		"counsel has not read",
		"no code",
		"nothing outside `ee/` imports anything under `ee/`",
		"spdx-license-identifier: licenseref-fugaro-enterprise",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("ee/README.md lacks %q", want)
		}
	}

	licence := strings.ReplaceAll(readRealEE(t, "LICENSE"), "\r\n", "\n")
	if first, _, _ := strings.Cut(licence, "\n"); first != eeDraftStatusLine {
		t.Errorf("ee/LICENSE's first line is %q, want %q: the text is a draft counsel has not read (D311), and the status line comes off only when counsel has", first, eeDraftStatusLine)
	}
	flatLicence := flattenDoc(licence)
	for _, want := range []string{
		"Fugaro Enterprise Edition",
		"LicenseRef-Fugaro-Enterprise",
		"<URL TO BE PUBLISHED>",
		"correct number of user seats",
		"development and testing purposes without a subscription",
		"Nothing under ee/ has ever been released",
		`THE SOFTWARE IS PROVIDED "AS IS"`,
	} {
		if !strings.Contains(flatLicence, want) {
			t.Errorf("ee/LICENSE lacks %q", want)
		}
	}
	if core := licenceCopyrightLine(readDoc(t, "LICENSE")); licenceCopyrightLine(licence) != core {
		t.Errorf("ee/LICENSE's copyright line is %q, want the root LICENSE's %q: the licensor of ee/ is the core's own holder until counsel names another", licenceCopyrightLine(licence), core)
	}

	// The listing is the "and nothing else" half: no code has landed, so a
	// third file here is code arriving without a test that says what it is.
	entries := strings.Fields(readRealEE(t, ""))
	if want := []string{"LICENSE", "README.md"}; !slices.Equal(entries, want) {
		t.Errorf(`ee/ holds %v; it holds exactly %v until the first organisation feature lands, and that feature's task moves this list with it.`, entries, want)
	}
}
