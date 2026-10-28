package cli

// ee/ carries its licence from 2026-10-26 (licensing plan L4.1, brought
// forward by the owner's ruling D311): the folder holds a README that says what
// it is for, `ee/LICENSE` (counsel's text), and no code.
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

// eeLicenceTitle is the first line of `ee/LICENSE`: counsel's text carries no
// draft banner, so a banner that comes back means an older draft was restored.
const eeLicenceTitle = "Fugaro Enterprise Edition Licence"

// TestDocsEELicensed: the README says what the folder is and that the licence
// says the licence's Subscription Terms are not published yet, the licence names the Software, the seats and the SPDX
// identifier every ee/ file's notice carries, and ee/ holds nothing else.
func TestDocsEELicensed(t *testing.T) {
	flat := strings.ToLower(flattenDoc(readRealEE(t, "README.md")))
	for _, want := range []string{
		"reserved",
		"net-new organisation features",
		"their own licence",
		"](license)",
		"not published yet",
		"no code",
		"nothing outside `ee/` imports anything under `ee/`",
		"spdx-license-identifier: licenseref-fugaro-enterprise",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("ee/README.md lacks %q", want)
		}
	}

	licence := strings.ReplaceAll(readRealEE(t, "LICENSE"), "\r\n", "\n")
	if first, _, _ := strings.Cut(licence, "\n"); first != eeLicenceTitle {
		t.Errorf("ee/LICENSE's first line is %q, want %q: counsel's text of 2026-10-27 starts with its title and carries no draft banner (D311)", first, eeLicenceTitle)
	}
	flatLicence := flattenDoc(licence)
	for _, want := range []string{
		"Fugaro Enterprise Edition",
		"LicenseRef-Fugaro-Enterprise",
		// Without the scheme: flattenDoc turns "//" into a space.
		"fugaro.dev/legal/subscription-terms",
		// The CLA's published URL (plan task L0.2's last step, 2026-10-29),
		// again without the scheme: flattenDoc turns "//" into a space. It is
		// derived from docs_licensing_test.go's claURL so the package holds one
		// spelling of the revision, not one here and another there.
		strings.TrimPrefix(claURL, "https://"),
		// Counsel's coverage sentence names the three directories, and the
		// console's half is spelled from the boundary test's own constant: the
		// floor of docs_console_boundary_test.go's mention scan is the string a
		// console path starts with, so writing it here would put this file
		// outside the one-entry allow-list over wording that is the licence's
		// own and not a reference to the directory.
		"every file under the ee/, " + consoleDirName + "/ and mobile/ directories",
		"correct number of user seats",
		"development and testing purposes without a subscription",

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
