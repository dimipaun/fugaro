package cli

// ee/ carries its licence from 2026-10-26 (licensing plan L4.1, brought
// forward by the owner's ruling D311): the folder holds a README that says what
// it is for, `ee/LICENSE` (counsel's text), and no code.
//
// D532 (2026-10-29) ends the rule that only net-new features are born here: the
// folder holds the business-licensed features, new ones and those the owner
// moved out of the core (D434, D444), while the versions already released under
// MIT stay MIT (R1). Nothing has moved yet, so the folder still holds no code,
// and the listing half below has become R3's notice -- a file lands here with
// the two-line notice rather than only as documentation.
//
// The read goes through readRealEE (docs_ee_repo_test.go), so this test's name
// is in eeReadingTests and the "core without ee/" workflow skips it there.

import (
	"path"
	"slices"
	"strings"
	"testing"
)

// eeLicenceTitle is the first line of `ee/LICENSE`: counsel's text carries no
// draft banner, so a banner that comes back means an older draft was restored.
const eeLicenceTitle = "Fugaro Enterprise Edition Licence"

// eeNotice is the two-line notice R3 puts at the top of every file that lands
// under ee/. The identifier is the one `LICENSES/LicenseRef-Fugaro-Enterprise.txt`
// resolves, and the pointer names the terms the file is under.
var eeNotice = [2]string{
	"SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise",
	"See ee/LICENSE for the terms.",
}

// eeNoticeExempt are the two names under ee/ that carry no notice (R3): the
// README, which is what this file's first half reads, and the terms themselves.
var eeNoticeExempt = []string{"LICENSE", "README.md"}

// eeLicenceSiblingSuffix is the sibling R3 asks for beside a file whose type
// carries no comment syntax to hold the notice (REUSE's `<name>.license`).
const eeLicenceSiblingSuffix = ".license"

// TestDocsEELicensed: the README says what the folder is -- the home of the
// business-licensed features, new and moved, with the released MIT versions
// staying MIT -- the licence names the Software, the seats and the SPDX
// identifier every ee/ file's notice carries, and every other file under ee/ is
// admitted by that notice.
func TestDocsEELicensed(t *testing.T) {
	flat := strings.ToLower(flattenDoc(readRealEE(t, "README.md")))
	for _, want := range []string{
		"business-licensed features",
		"moved out of the core",
		"released mit versions stay mit",
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

	// The listing half is R3's notice: a fixed pair of names held only while the
	// folder held documentation, and D532 ends that. What admits a file here is
	// its notice, so the folder takes the first moved feature with the same
	// check it took the README with.
	eeCheckTheNotice(t, "")
}

// TestDocsEEAdmitsTheNotice is the red half of the rule TestDocsEELicensed
// applies to the real tree: it pins both answers of eeFileAdmitted against
// content it writes itself, so a rule that admitted everything -- or refused
// everything -- fails here rather than passing on a folder whose only entries
// are the README and the licence.
func TestDocsEEAdmitsTheNotice(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		content  string
		siblings []string
		admitted bool
	}{
		{
			name:     "a Go file opening with the notice",
			file:     "governance/policy.go",
			content:  "// " + eeNotice[0] + "\n// " + eeNotice[1] + "\n\npackage governance\n",
			siblings: []string{"policy.go"},
			admitted: true,
		},
		{
			name:     "a shell script carrying it on lines 2 and 3",
			file:     "bin/sweep",
			content:  "#!/bin/sh\n# " + eeNotice[0] + "\n# " + eeNotice[1] + "\n",
			siblings: []string{"sweep"},
			admitted: true,
		},
		{
			name:     "a type with no comment syntax, beside its sibling",
			file:     "data/plan.json",
			content:  "{\"steps\":0}\n",
			siblings: []string{"plan.json", "plan.json" + eeLicenceSiblingSuffix},
			admitted: true,
		},
		{
			name:     "the README, which is exempt",
			file:     "README.md",
			content:  "# ee/ is the Fugaro Enterprise Edition\n",
			siblings: []string{"LICENSE", "README.md"},
			admitted: true,
		},
		{
			name:     "a Go file opening with its package clause",
			file:     "governance/policy.go",
			content:  "package governance\n",
			siblings: []string{"policy.go"},
			admitted: false,
		},
		{
			name:     "the notice below the first two lines",
			file:     "governance/policy.go",
			content:  "package governance\n\n// " + eeNotice[0] + "\n// " + eeNotice[1] + "\n",
			siblings: []string{"policy.go"},
			admitted: false,
		},
		{
			name:     "only the first of the two lines",
			file:     "governance/policy.go",
			content:  "// " + eeNotice[0] + "\n\npackage governance\n",
			siblings: []string{"policy.go"},
			admitted: false,
		},
		{
			name:     "a type with no comment syntax and no sibling",
			file:     "data/plan.json",
			content:  "{\"steps\":0}\n",
			siblings: []string{"plan.json"},
			admitted: false,
		},
	}
	for _, c := range cases {
		if got := eeFileAdmitted(path.Base(c.file), c.content, c.siblings); got != c.admitted {
			t.Errorf("%s: eeFileAdmitted(%q) = %v, want %v", c.name, c.file, got, c.admitted)
		}
	}
}

// eeCheckTheNotice walks one directory of the real ee/ and fails for every file
// R3's notice does not admit. readRealEE lists a directory's entries with a
// trailing slash on the subdirectories, so the walk descends into them instead
// of reading one as a file.
func eeCheckTheNotice(t *testing.T, rel string) {
	t.Helper()
	entries := strings.Fields(readRealEE(t, rel))
	for _, name := range entries {
		if dir, isDir := strings.CutSuffix(name, "/"); isDir {
			eeCheckTheNotice(t, path.Join(rel, dir))
			continue
		}
		if !eeFileAdmitted(name, readRealEE(t, path.Join(rel, name)), entries) {
			t.Errorf(`%s does not start with the two-line notice: every file under %s/ opens with %q then %q (R3), a file type with no comment syntax needs a %q sibling carrying them, and only %s are exempt. The folder holds the business-licensed features now (D532), so the notice is what admits the first moved file here.`,
				path.Join(eeDirName, rel, name), eeDirName, eeNotice[0], eeNotice[1], name+eeLicenceSiblingSuffix, strings.Join(eeNoticeExempt, " and "))
		}
	}
}

// eeFileAdmitted reports whether R3 admits one file under ee/: one of the two
// exempt names, a file whose own text opens with the two-line notice, or a file
// whose type carries no comment syntax and which has a `<name>.license` sibling.
// siblings is the listing of the file's own directory, so the rule needs no
// filesystem of its own and the red case above exercises it with none.
func eeFileAdmitted(name, content string, siblings []string) bool {
	if slices.Contains(eeNoticeExempt, name) {
		return true
	}
	if eeOpensWithTheNotice(content) {
		return true
	}
	return slices.Contains(siblings, name+eeLicenceSiblingSuffix)
}

// eeOpensWithTheNotice reports whether a file's own text opens with the two-line
// notice. Only the notice's text is read, never the comment syntax around it, so
// `// `, `# `, `<!-- ` and `/* ` all pass, which is how eeboundary's own notice
// rule reads a file type it has no table entry for. A `#!` script carries the
// notice on lines 2 and 3, as `.claude/rules/ee.md` says it does.
func eeOpensWithTheNotice(content string) bool {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	offset := 0
	if len(lines) > 0 && strings.HasPrefix(lines[0], "#!") {
		offset = 1
	}
	return len(lines) >= offset+2 &&
		strings.Contains(lines[offset], eeNotice[0]) &&
		strings.Contains(lines[offset+1], eeNotice[1])
}
