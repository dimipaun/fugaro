package cli

// ee/ carries its licence from 2026-10-26 (licensing plan L4.1, brought
// forward by the owner's ruling D311): the folder holds a README that says what
// it is for, `ee/LICENSE` (counsel's text), and — from E1.5 — the assembly
// point `ee/register`.
//
// D532 (2026-10-29) ends the rule that only net-new features are born here: the
// folder holds the business-licensed features, new ones and those the owner
// moved out of the core (D434, D444), while the versions already released under
// MIT stay MIT (R1). Nothing has moved yet, and the first file that landed is
// not a moved feature but E1.5's assembly point, `ee/register`. The listing half
// below has become R3's notice -- a file lands here with the two-line notice
// rather than only as documentation.
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
const eeLicenceTitle = "Fugaro Subscription Edition Licence"

// eeNotice is the two-line notice R3 puts at the top of every file that lands
// under ee/. The identifier is the one `LICENSES/LicenseRef-Fugaro-Subscription.txt`
// resolves, and the pointer names the terms the file is under.
var eeNotice = [2]string{
	"SPDX-License-Identifier: LicenseRef-Fugaro-Subscription",
	"See ee/LICENSE for the terms.",
}

// eeNoticeExempt are the two paths R3 exempts, matched on the whole path
// relative to ee/: `ee/README.md` and `ee/LICENSE`. A README.md that lands
// inside a feature's own directory is a file like any other and carries the
// notice.
var eeNoticeExempt = []string{"LICENSE", "README.md"}

// eeLicenceSiblingSuffix is the sibling R3 asks for beside a file whose type
// carries no comment syntax to hold the notice (REUSE's `<name>.license`).
const eeLicenceSiblingSuffix = ".license"

// eeNoticeStyle is how one file type carries the two-line notice, and the two
// tables below are R3's, by file type and for a file whose type is its whole
// name. They mirror `internal/eeboundary/boundary_test.go`'s, which runs the
// same rule over the tracked tree -- `TestEEFilesCarryTheNotice` there is the
// authoritative reading, and it also refuses a symlink and a shebang that is
// not on the first line. A shape R3 admits and this reading refused, or the
// reverse, would be a second rule rather than a second reading of one.
type eeNoticeStyle struct {
	open  string
	close string
}

func (st eeNoticeStyle) render(line string) string {
	if st.close == "" {
		return st.open + line
	}
	return st.open + line + st.close
}

var (
	eeSlashNotice = eeNoticeStyle{open: "// "}
	eeHashNotice  = eeNoticeStyle{open: "# "}
	eeHTMLNotice  = eeNoticeStyle{open: "<!-- ", close: " -->"}
	eeCSSNotice   = eeNoticeStyle{open: "/* ", close: " */"}
)

var eeNoticeStyles = map[string]eeNoticeStyle{
	".go": eeSlashNotice, ".ts": eeSlashNotice, ".tsx": eeSlashNotice,
	".js": eeSlashNotice, ".mjs": eeSlashNotice, ".cjs": eeSlashNotice,
	".sh": eeHashNotice, ".py": eeHashNotice, ".tf": eeHashNotice,
	".yaml": eeHashNotice, ".yml": eeHashNotice, ".toml": eeHashNotice,
	".md": eeHTMLNotice, ".html": eeHTMLNotice, ".plist": eeHTMLNotice,
	".css":     eeCSSNotice,
	".license": {},
}

var eeNoticeStylesByName = map[string]eeNoticeStyle{
	"Dockerfile": eeHashNotice,
	".gitignore": eeHashNotice,
	".npmrc":     eeHashNotice,
}

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
		"nothing outside `ee/` imports anything under `ee/`",
		"spdx-license-identifier: licenseref-fugaro-subscription",
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
		"Fugaro Subscription Edition",
		"LicenseRef-Fugaro-Subscription",
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
	files := eeTreeFiles(func(dir string) []string {
		return strings.Fields(readRealEE(t, dir))
	})
	for _, f := range files {
		if !eeNoticeAdmits(f.rel, readRealEE(t, f.rel), f.siblings) {
			t.Errorf(`%s does not start with the two-line notice: every file under %s/ opens with %q then %q (R3), a file type with no comment syntax needs a %q sibling carrying them, and only %s are exempt. The folder holds the business-licensed features now (D532), so the notice is what admits the first moved file here.`,
				path.Join(eeDirName, f.rel), eeDirName, eeNotice[0], eeNotice[1], path.Base(f.rel)+eeLicenceSiblingSuffix, strings.Join(eeNoticeExempt, " and "))
		}
	}

	// The README's sentence about code is a fact about this tree, so the two
	// move together: while every file under ee/ is the README or the licence,
	// the folder holds no code and the README has to say so; E1.5 landed the
	// assembly point, ee/register, so the README stopped saying it. Without
	// this the sentence could stand while the folder filled up, or the folder
	// could empty out while the README claimed code that is not there.
	features := 0
	for _, f := range files {
		if !eeNoticeExemptPath(f.rel) {
			features++
		}
	}
	saysNoCode := strings.Contains(flat, "no code")
	if features > 0 && saysNoCode {
		t.Errorf("ee/README.md still says the folder holds no code, and %d file(s) besides the README and the licence have landed under ee/: the public sentence and the folder have to move together, or the first feature leaves the statement false with the suite green", features)
	}
	if features == 0 && !saysNoCode {
		t.Errorf("ee/README.md does not say the folder holds no code, and ee/ holds nothing besides the README and the licence: the folder is still empty of features, and the sentence D532 keeps is the one that says so")
	}
}

// TestDocsEEAdmitsTheNotice is the red half of the rule TestDocsEELicensed
// applies to the real tree: it pins both answers of eeNoticeAdmits against
// content it writes itself, so a rule that admitted everything -- or refused
// everything -- fails here rather than passing on a folder whose only entries
// are the README and the licence.
func TestDocsEEAdmitsTheNotice(t *testing.T) {
	noticeGo := "// " + eeNotice[0] + "\n// " + eeNotice[1] + "\n\npackage governance\n"
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
			content:  noticeGo,
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
			name:     "an XML file carrying it below the declaration",
			file:     "data/plan.plist",
			content:  "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!-- " + eeNotice[0] + " -->\n<!-- " + eeNotice[1] + " -->\n<plist/>\n",
			siblings: []string{"plan.plist"},
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
			name:     "the two exempt paths, notice or none",
			file:     "README.md",
			content:  "# ee/ is the Fugaro Subscription Edition\n",
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
			content:  "package governance\n\n" + noticeGo,
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
		{
			name:     "the notice in a syntax the file type does not carry",
			file:     "data/plan.json",
			content:  "// " + eeNotice[0] + "\n// " + eeNotice[1] + "\n",
			siblings: []string{"plan.json"},
			admitted: false,
		},
		{
			name:     "a README inside a feature's directory, with no notice",
			file:     "governance/README.md",
			content:  "# governance\n",
			siblings: []string{"README.md"},
			admitted: false,
		},
		{
			name:     "a README inside a feature's directory, with the notice",
			file:     "governance/README.md",
			content:  "<!-- " + eeNotice[0] + " -->\n<!-- " + eeNotice[1] + " -->\n\n# governance\n",
			siblings: []string{"README.md"},
			admitted: true,
		},
	}
	for _, c := range cases {
		if got := eeNoticeAdmits(c.file, c.content, c.siblings); got != c.admitted {
			t.Errorf("%s: eeNoticeAdmits(%q) = %v, want %v", c.name, c.file, got, c.admitted)
		}
	}
}

// TestDocsEEWalksSubdirectories is the red case for the walk's recursive half:
// the first subdirectory that lands under ee/ is read here first, so a branch
// that descends into it -- or fails to -- is exercised before a feature's files
// depend on it. It also pins that a nested file is judged against its own
// directory's listing, which is where its `.license` sibling would be.
func TestDocsEEWalksSubdirectories(t *testing.T) {
	tree := map[string][]string{
		"":           {"LICENSE", "README.md", "governance/"},
		"governance": {"notes.txt", "policy.go", "README.md"},
	}
	files := eeTreeFiles(func(dir string) []string { return tree[dir] })
	got := make([]string, 0, len(files))
	for _, f := range files {
		got = append(got, f.rel)
	}
	slices.Sort(got)
	want := []string{"LICENSE", "README.md", path.Join("governance", "README.md"), path.Join("governance", "notes.txt"), path.Join("governance", "policy.go")}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("the walk found %v, want %v: readRealEE names a subdirectory with a trailing slash, and the walk has to descend into it, or every file of the first feature that lands is never read", got, want)
	}
	for _, f := range files {
		if f.rel == path.Join("governance", "notes.txt") && !slices.Equal(f.siblings, tree["governance"]) {
			t.Errorf("the walk judged %s against %v, want its own directory's listing %v: the sibling half of R3 reads the directory the file sits in", f.rel, f.siblings, tree["governance"])
		}
	}
}

// eeNoticeExemptPath reports whether R3 exempts one path under ee/ from the
// notice: the folder's own README and the terms it points at, and nothing else.
func eeNoticeExemptPath(rel string) bool { return slices.Contains(eeNoticeExempt, rel) }

// eeNoticeStyleFor answers how one file carries the notice: `#` for a file that
// opens with a shebang, whatever its type, and otherwise the syntax its type
// asks for; the second answer is false for a type with no comment syntax at
// all, which is the type that needs the sibling carrying the notice.
func eeNoticeStyleFor(base string, shebang bool) (eeNoticeStyle, bool) {
	if shebang {
		return eeHashNotice, true
	}
	if st, ok := eeNoticeStyles[strings.ToLower(path.Ext(base))]; ok {
		return st, true
	}
	st, ok := eeNoticeStylesByName[base]
	return st, ok
}

// eeNoticeAdmits reports whether R3 admits one file under ee/: an exempt path,
// a file that opens with the two-line notice in its type's syntax, or a file
// whose type has no comment syntax and which has a `<name>.license` sibling.
// siblings is the listing of the file's own directory, so the rule reads no
// filesystem of its own and the cases above exercise it with none.
func eeNoticeAdmits(rel, content string, siblings []string) bool {
	if eeNoticeExemptPath(rel) {
		return true
	}
	base := path.Base(rel)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	shebang := len(lines) > 0 && strings.HasPrefix(lines[0], "#!")
	// An XML declaration is the one other line that has to stay first, so the
	// notice sits below it: the doctype and the root element then follow, and
	// the file is still well-formed XML.
	xmlDecl := !shebang && len(lines) > 0 && strings.HasPrefix(lines[0], "<?xml")
	style, ok := eeNoticeStyleFor(base, shebang)
	if !ok {
		return slices.Contains(siblings, base+eeLicenceSiblingSuffix)
	}
	offset := 0
	if shebang || xmlDecl {
		offset = 1
	}
	if len(lines) < offset+2 {
		return false
	}
	return strings.TrimSpace(lines[offset]) == style.render(eeNotice[0]) &&
		strings.TrimSpace(lines[offset+1]) == style.render(eeNotice[1])
}

// eeTreeFile is one file the walk found: its path relative to ee/ and the
// listing of the directory it sits in, which is where its `.license` sibling
// would be.
type eeTreeFile struct {
	rel      string
	siblings []string
}

// eeTreeFiles answers every file under the tree list describes. A listing names
// a subdirectory with a trailing slash -- which is how readRealEE lists one --
// so the walk descends into it instead of reading it as a file.
func eeTreeFiles(list func(dir string) []string) []eeTreeFile {
	var files []eeTreeFile
	var walk func(dir string)
	walk = func(dir string) {
		entries := list(dir)
		for _, name := range entries {
			if sub, isDir := strings.CutSuffix(name, "/"); isDir {
				walk(path.Join(dir, sub))
				continue
			}
			files = append(files, eeTreeFile{rel: path.Join(dir, name), siblings: entries})
		}
	}
	walk("")
	return files
}
