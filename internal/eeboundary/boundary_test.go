package eeboundary

// The licence boundary around ee/ (licensing plan task L1.1; reconciliation R3
// and R6, decisions D303 as amended and D309). Its parent is
// internal/creds/boundary_test.go, which walks the module the same way.
//
// ee/ is where net-new organisation features will be born under their own
// licence (D302), and the sentence the whole open-core split rests on is "the
// core never imports ee/": a core package that imported ee/ would put code
// under a different licence into every build of the MIT binary, and the
// archive's LICENSE, the plugin manifest and the Homebrew cask would all stop
// being true. Nothing enforces that but this file. It is not a permission
// check and not a build tag: it is a scan of the source, and its value is that
// it refuses the cases a build would not notice.
//
// What it reads, and why each reading is the awkward one:
//
//   - every .go file outside ee/, at any depth and with every build tag, and
//     every directory is walked: testdata/, node_modules/, `.hidden/` and
//     `_build/` included, unlike the creds walker. `go list -deps` sees only
//     what the current tags build, and a `//go:build ee` file in core is
//     exactly the case it misses; a file no build reads today still cannot
//     carry the import.
//   - the one exception, `cmd/fugaro/ee_on.go` (D303 as amended): that path
//     exactly, that constraint exactly, package main, and exactly one import
//     of the ee path, the blank import of the registration package. A legacy
//     `// +build ee` line alone is not the constraint; a second assembly file
//     is not the exception; a file of the same name in another directory is
//     not either.
//   - every other route into ee/ that is not an import: a symlink resolving
//     into it, a //go:embed pattern, a //go:generate line, a cgo #include or
//     #cgo flag, a `replace` (or `use`) directive in any go.mod or go.work,
//     and a nested go.mod or go.work under ee/ itself.
//   - every tracked file under each licence directory -- the console, the
//     mobile app and ee/, one business licence in three files of identical wording
//     (D311) -- carries the two-line notice R3 asks for, in the comment syntax
//     of its type; a type with no comment syntax needs a sibling
//     `<name>.license`; and ee/LICENSE exists as soon as one source file does.
//     At 0.11.0 ee/ is reserved and holds only ee/README.md, so its half
//     passes vacuously -- which is the point: the rule landed before the first
//     ee/ file did. The console and the mobile app hold code today, so their halves are
//     the rule doing work, and the pointer each file carries names its own
//     directory's licence file.
//
// Its limits are worth stating, because a reader will over-trust it otherwise.
// It is a text scan of this repository at one commit: it says nothing about a
// fork, a second module, a file copied in by a build step, or a link the
// operating system follows outside the tree it was given. And a contributor
// who deletes this file is caught by review, not by the scan -- the path-scoped
// rule for ee/ (plan task L1.6) names these tests for exactly that reason.
//
// The scan takes the tracked ee/ file list as an argument rather than reading
// git itself, which is what lets the last test hand it a tree it built under
// t.TempDir() and plant one violation per rule.

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	eeDirName      = "ee"
	consoleDirName = "console"
	mobileDirName  = "mobile"

	eeAssemblyFile = "cmd/fugaro/ee_on.go"
	eeReadmeFile   = "ee/README.md"
	eeLicenceFile  = "ee/LICENSE"

	// The other two licence directories (D311) name their licence file
	// LICENSE.md, and their READMEs are exempt exactly as ee/'s is. The paths
	// are built from the directory names so a directory is spelled once.
	consoleReadmeFile  = consoleDirName + "/README.md"
	consoleLicenceFile = consoleDirName + "/LICENSE.md"
	mobileReadmeFile   = mobileDirName + "/README.md"
	mobileLicenceFile  = mobileDirName + "/LICENSE.md"

	// symlinkMode is the mode git records for a symlink: `git ls-files -s`
	// answers 120000 for one. A tracked symlink under a licence directory is
	// refused rather than followed, so a link cannot stand in for a file that
	// carries the notice (R3, amended).
	symlinkMode = "120000"

	// eeSiblingSuffix is the file a type with no comment syntax carries its
	// notice in, beside it: `<name>.license`, as REUSE names it.
	eeSiblingSuffix = ".license"

	// The SPDX line R3 asks every file in a licence directory to carry. The
	// pointer below it names that directory's own licence file.
	noticeSPDX = "SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise"

	// The rules, named as the tests below name them.
	ruleImports = "core never imports ee/"
	ruleEmbeds  = "core never embeds from ee/"
	ruleRoutes  = "no module route into ee/"
	ruleLicence = "ee/LICENSE exists whenever an ee/ source file does"
)

// noticeTarget is one directory whose tracked files carry the two-line notice,
// and the licence file the pointer below the SPDX line names.
type noticeTarget struct {
	// dir is the module-relative directory name: ee, console or mobile.
	dir string
	// licence is the module-relative licence file the pointer names.
	licence string
	// readme is the one file besides the licence itself that is exempt.
	readme string
}

// rule is the finding's rule name for this directory. It names the directory
// so a failure says which of the three the file is in.
func (t noticeTarget) rule() string { return "every " + t.dir + "/ file carries the notice" }

// pointer is the second line of the notice: a reader is told which licence file
// governs the file they are reading.
func (t noticeTarget) pointer() string { return "See " + t.licence + " for the terms." }

// noticeTargets are the three directories one business licence covers (D311),
// in the order a reader meets them.
var noticeTargets = []noticeTarget{
	{dir: eeDirName, licence: eeLicenceFile, readme: eeReadmeFile},
	{dir: consoleDirName, licence: consoleLicenceFile, readme: consoleReadmeFile},
	{dir: mobileDirName, licence: mobileLicenceFile, readme: mobileReadmeFile},
}

// targetFor answers the licence directory a module-relative path sits in, and
// whether it is in one at all.
func targetFor(path string) (noticeTarget, bool) {
	for _, t := range noticeTargets {
		if path == t.dir || strings.HasPrefix(path, t.dir+"/") {
			return t, true
		}
	}
	return noticeTarget{}, false
}

// thirdPartyDir is a directory of vendored third-party files under a licence
// directory. Those files are not Fugaro's to license (console/LICENSE.md §7.1),
// so they carry their own licence instead of the notice: the licence text sits
// in the directory as `licence`, and every other file has a sibling
// `<name>.license` whose first line names that licence's SPDX identifier, as
// REUSE spells a third-party file's notice.
type thirdPartyDir struct {
	spdx    string
	licence string
}

// thirdPartyDirs is the closed list. The console redesign vendored its two web
// fonts (owner's ruling of 2026-10-28); a new entry is a licence decision, not
// a convenience.
var thirdPartyDirs = map[string]thirdPartyDir{
	consoleDirName + "/src/renderer/fonts/instrument-sans/": {spdx: "OFL-1.1", licence: "OFL.txt"},
	consoleDirName + "/src/renderer/fonts/ibm-plex-mono/":   {spdx: "OFL-1.1", licence: "LICENSE.txt"},
}

// thirdPartyFor answers the third-party directory a path sits directly in.
func thirdPartyFor(path string) (thirdPartyDir, bool) {
	dir := path[:strings.LastIndex(path, "/")+1]
	tp, ok := thirdPartyDirs[dir]
	return tp, ok
}

// noticeRuleFor answers the notice rule's name for one directory, for a caller
// that holds the name rather than the target.
func noticeRuleFor(dir string) string { return noticeTarget{dir: dir}.rule() }

// trackedFile is one file under a licence directory as the tracked list
// describes it: the module-relative slash path, and the mode `git ls-files -s`
// recorded.
type trackedFile struct {
	path string
	mode string
}

// finding is one violation: which rule, which file, and why in words a reader
// can act on.
type finding struct {
	rule   string
	path   string
	detail string
}

func (f finding) String() string { return f.rule + ": " + f.path + ": " + f.detail }

// report is what one scan of one tree answered.
type report struct {
	// goFiles counts the .go files outside ee/ the walk parsed: a test
	// asserts it is the module and not an empty directory, so no check below
	// can pass by looking at nothing.
	goFiles int
	// trackedFiles counts the tracked files the notice rule was handed.
	trackedFiles int
	findings     []finding
}

func (r *report) byRule(rule string) []finding {
	var out []finding
	for _, f := range r.findings {
		if f.rule == rule {
			out = append(out, f)
		}
	}
	return out
}

// scanner is one walk in progress: the tree, the ee import path read from its
// go.mod (so a rename is followed rather than assumed), and the tracked ee/
// list the notice rule reads.
type scanner struct {
	root     string
	eePath   string
	tracked  map[string]bool
	findings []finding
}

func (s *scanner) add(rule, path, detail string) {
	s.findings = append(s.findings, finding{rule: rule, path: path, detail: detail})
}

// scanTree runs every rule over one tree -- this module, or the one a plant
// built -- and answers what it found. files is the tracked list of the three
// licence directories the notice rule reads; everything else is read from the
// tree.
//
// The walk steps out of no directory at all, and that is deliberate: a
// testdata/, node_modules/, `.hidden/` or `_build/` directory is read like any
// other, because a file the go tool would not build today still cannot carry
// the import. It is also why the walk cannot be `go list -deps`: that sees one
// build's files, and the files this rule exists for are the ones no build
// reads.
func scanTree(root string, files []trackedFile) (*report, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	modulePath, err := modulePathOf(abs)
	if err != nil {
		return nil, err
	}
	s := &scanner{
		root:    abs,
		eePath:  modulePath + "/" + eeDirName,
		tracked: make(map[string]bool, len(files)),
	}
	for _, f := range files {
		s.tracked[f.path] = true
	}
	rep := &report{trackedFiles: len(files)}
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(abs, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || d.IsDir() {
			return nil
		}
		if inEEPath(rel) {
			// ee/'s own files are the notice rule's, and it reads the tracked
			// list rather than this walk; a module file under ee/ is a route
			// into it either way.
			if d.Name() == "go.mod" || d.Name() == "go.work" {
				s.add(ruleRoutes, rel, "a nested module under ee/")
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return s.checkSymlink(p, rel)
		}
		switch {
		case d.Name() == "go.mod" || d.Name() == "go.work":
			return s.checkModuleFile(p, rel)
		case strings.HasSuffix(d.Name(), ".go"):
			rep.goFiles++
			return s.checkGoFile(p, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.checkEELicence(files)
	for _, f := range files {
		if err := s.checkNoticeFile(f); err != nil {
			return nil, err
		}
	}
	rep.findings = s.findings
	return rep, nil
}

// modulePathOf answers go.mod's module path, which is what makes the ee path
// checkable: "under ee/" means "<module>/ee" or below. It is read rather than
// written down, so a rename cannot leave the scan looking for a path nothing
// uses any more -- and a scan that looked for the wrong path would report
// nothing and pass.
func modulePathOf(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("the ee boundary needs the module path from go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module ")
		if !ok {
			continue
		}
		if p := strings.TrimSpace(rest); p != "" {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s/go.mod names no module", root)
}

// inEEPath reports whether a module-relative slash path is ee/ or below it.
func inEEPath(rel string) bool {
	return rel == eeDirName || strings.HasPrefix(rel, eeDirName+"/")
}

// insideEE reports whether an absolute path lands in this tree's ee/.
func (s *scanner) insideEE(p string) bool {
	rel, err := filepath.Rel(s.root, p)
	if err != nil {
		return false
	}
	return inEEPath(filepath.ToSlash(rel))
}

// resolvesIntoEE reports whether a link at dir pointing at target lands in
// ee/, following a chain when the filesystem can resolve it. An unresolved
// path is answered lexically, so a dangling link into ee/ is refused too.
func (s *scanner) resolvesIntoEE(dir, target string) bool {
	p := target
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	p = filepath.Clean(p)
	if s.insideEE(p) {
		return true
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	root := s.root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return false
	}
	return inEEPath(filepath.ToSlash(rel))
}

// importsEE reports whether an import path is the ee package or under it,
// matched by path segment so that a module named ".../eevee" is not a match.
func (s *scanner) importsEE(importPath string) bool {
	return importPath == s.eePath || strings.HasPrefix(importPath, s.eePath+"/")
}

// namesEEPath reports whether one token of a directive names ee/: the module's
// own ee path, or a filesystem path with an `ee` segment. The segment match is
// what reaches `../../ee/x.h` and `web/node_modules/ee/x`; a path that merely
// contains the letters, like `spree/`, is not a match.
func (s *scanner) namesEEPath(token string) bool {
	token = strings.Trim(strings.TrimSpace(token), `"'`)
	if token == "" {
		return false
	}
	if s.importsEE(token) {
		return true
	}
	return slices.Contains(strings.Split(strings.TrimPrefix(token, "./"), "/"), eeDirName)
}

// checkSymlink refuses a link outside ee/ that lands in it: the walk does not
// follow symlinks, and the tree already has some under .claude/skills/, so a
// link is a route into ee/ that an import scan alone would miss.
func (s *scanner) checkSymlink(path, rel string) error {
	target, err := os.Readlink(path)
	if err != nil {
		return err
	}
	if s.resolvesIntoEE(filepath.Dir(path), target) {
		s.add(ruleImports, rel, "a symlink resolving into "+eeDirName+"/")
	}
	return nil
}

// checkGoFile is the import rule and the two directives that reach a package
// without being one, over one .go file outside ee/.
func (s *scanner) checkGoFile(path, rel string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s.checkGoSource(path, rel, src)
	return nil
}

func (s *scanner) checkGoSource(path, rel string, src []byte) {
	fset := token.NewFileSet()
	f, perr := parser.ParseFile(fset, path, src, parser.ParseComments)
	if perr != nil || f == nil {
		// A file the parser cannot read is a file whose imports cannot be
		// checked, and "cannot be checked" is not "is fine". Every .go file
		// in this module parses (`gofmt -l .` is a CI gate), so this fires
		// only on a file added with a syntax error.
		s.add(ruleImports, rel, "it cannot be parsed, so its imports cannot be checked: "+errText(perr))
		return
	}
	pkgLine := 0
	if f.Package.IsValid() {
		pkgLine = fset.Position(f.Package).Line
	}
	expr, fromGoBuild := buildConstraint(src, pkgLine)

	var eeImports []eeImport
	for _, imp := range f.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		if !s.importsEE(importPath) {
			continue
		}
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		}
		eeImports = append(eeImports, eeImport{name: name, path: importPath})
	}

	if rel == eeAssemblyFile {
		s.checkAssembly(rel, f, expr, fromGoBuild, eeImports)
		return
	}
	for _, imp := range eeImports {
		s.add(ruleImports, rel, "it imports "+imp.path)
	}
	if expr != nil && constraintNamesEE(expr) {
		s.add(ruleImports, rel, "its build constraint names the ee tag, which only "+eeAssemblyFile+" may do")
	}
	s.checkEmbedPatterns(path, rel, f)
	s.checkDirectives(rel, f)
}

// eeImport is one import of the ee path.
type eeImport struct {
	name string
	path string
}

// checkAssembly holds cmd/fugaro/ee_on.go to the one shape R6 allows, and
// refuses every deviation here rather than leaving it to whichever general
// check happens to catch it: the file's whole purpose is the blank import, so
// a file at that path without it (or without the tag, or not in package main)
// is a file to delete, not to exempt.
func (s *scanner) checkAssembly(rel string, f *ast.File, expr constraint.Expr, fromGoBuild bool, eeImports []eeImport) {
	want := s.eePath + "/register"
	if f.Name == nil || f.Name.Name != "main" {
		s.add(ruleImports, rel, "the one assembly file must be in package main")
	}
	if !fromGoBuild || expr == nil || expr.String() != "ee" {
		s.add(ruleImports, rel, "the one assembly file's build constraint must be exactly //go:build ee, above the package clause")
	}
	if len(eeImports) != 1 || eeImports[0].name != "_" || eeImports[0].path != want {
		s.add(ruleImports, rel, "the one assembly file's only import of the ee path must be the blank import _ \""+want+"\"")
	}
}

// checkEmbedPatterns refuses a //go:embed over an ee/ path. The directive is a
// var's doc comment by definition, so a mention of it in prose is not a
// pattern and is not read as one.
func (s *scanner) checkEmbedPatterns(path, rel string, f *ast.File) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR || gd.Doc == nil {
			continue
		}
		for _, c := range gd.Doc.List {
			rest, ok := strings.CutPrefix(strings.TrimPrefix(c.Text, "//"), "go:embed ")
			if !ok {
				continue
			}
			for _, pattern := range strings.Fields(rest) {
				p := strings.TrimPrefix(pattern, "all:")
				if strings.HasPrefix(p, "/") {
					continue
				}
				if s.insideEE(filepath.Join(filepath.Dir(path), filepath.FromSlash(p))) {
					s.add(ruleEmbeds, rel, "a //go:embed pattern into ee/: "+pattern)
				}
			}
		}
	}
}

// checkDirectives refuses the three comment directives that reach a path or a
// symbol without being an import: //go:generate, //go:linkname and a cgo
// #include or #cgo flag.
func (s *scanner) checkDirectives(rel string, f *ast.File) {
	for _, group := range f.Comments {
		for _, c := range group.List {
			for _, line := range commentLines(c.Text) {
				switch {
				case strings.HasPrefix(line, "go:generate "):
					for _, token := range strings.Fields(strings.TrimPrefix(line, "go:generate ")) {
						if s.namesEEPath(token) {
							s.add(ruleRoutes, rel, "a //go:generate line naming ee/: "+line)
						}
					}
				case strings.HasPrefix(line, "go:linkname "):
					fields := strings.Fields(line)
					if len(fields) >= 3 && s.importsEE(strings.Trim(fields[2], `"`)) {
						s.add(ruleImports, rel, "a //go:linkname onto the ee path: "+fields[2])
					}
				case strings.HasPrefix(line, "#include"), strings.HasPrefix(line, "#cinclude"), strings.HasPrefix(line, "#cgo"):
					for _, token := range strings.Fields(line) {
						if s.namesEEPath(token) {
							s.add(ruleRoutes, rel, "a cgo directive reaching into ee/: "+line)
						}
					}
				}
			}
		}
	}
}

// buildConstraint answers the build constraint written above a file's package
// clause. The boolean reports whether it came from a `//go:build` line: R6
// asks the assembly file for that form exactly, because a legacy `// +build`
// line is the one the go tool is retiring, and a file whose constraint the
// tool reads differently from this scan is a file whose exemption is an
// accident.
func buildConstraint(src []byte, pkgLine int) (constraint.Expr, bool) {
	lines := strings.Split(string(src), "\n")
	if pkgLine > 0 && pkgLine-1 < len(lines) {
		lines = lines[:pkgLine-1]
	}
	var goBuild, legacy []constraint.Expr
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "//go:build"):
			if e, err := constraint.Parse(trimmed); err == nil {
				goBuild = append(goBuild, e)
			}
		case strings.HasPrefix(trimmed, "// +build"):
			if e, err := constraint.Parse(trimmed); err == nil {
				legacy = append(legacy, e)
			}
		}
	}
	if len(goBuild) > 0 {
		return goBuild[0], true
	}
	switch len(legacy) {
	case 0:
		return nil, false
	case 1:
		return legacy[0], false
	}
	e := legacy[0]
	for _, more := range legacy[1:] {
		e = &constraint.AndExpr{X: e, Y: more}
	}
	return e, false
}

// constraintNamesEE reports whether a build constraint mentions the ee tag in
// any form -- `ee`, `!ee`, `ee || linux` alike. An expression built from other
// tags only is not this scan's business.
func constraintNamesEE(e constraint.Expr) bool {
	switch v := e.(type) {
	case *constraint.TagExpr:
		return v.Tag == "ee"
	case *constraint.NotExpr:
		return constraintNamesEE(v.X)
	case *constraint.AndExpr:
		return constraintNamesEE(v.X) || constraintNamesEE(v.Y)
	case *constraint.OrExpr:
		return constraintNamesEE(v.X) || constraintNamesEE(v.Y)
	}
	return false
}

// commentLines answers the directive-carrying lines of one comment, without
// its markers, so a `/* ... */` block is read like a run of `//` lines.
func commentLines(text string) []string {
	text = strings.TrimPrefix(text, "//")
	text = strings.TrimPrefix(text, "/*")
	text = strings.TrimSuffix(text, "*/")
	var out []string
	for _, line := range strings.Split(text, "\n") {
		out = append(out, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*")))
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return "no error reported"
	}
	return err.Error()
}

// checkModuleFile refuses a go.mod or go.work directive that makes ee/ part of
// a build again: a `replace` target, or a `use` in a workspace, that lands in
// ee/. Module paths listed under `require` are not read: a path is not a route
// into a directory, and a `require` of some module named .../ee/x would be
// refused by the import scan if anything imported it.
func (s *scanner) checkModuleFile(path, rel string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	inBlock := ""
	for _, line := range strings.Split(string(data), "\n") {
		line, _, _ = strings.Cut(line, "//")
		line = strings.TrimSpace(line)
		if inBlock != "" {
			if line == ")" {
				inBlock = ""
				continue
			}
			s.checkModuleDirective(rel, filepath.Dir(path), inBlock, line)
			continue
		}
		directive, rest, ok := strings.Cut(line, "(")
		if !ok {
			directive, rest, _ = strings.Cut(line, " ")
		}
		if directive != "replace" && directive != "use" {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" {
			inBlock = directive
			continue
		}
		s.checkModuleDirective(rel, filepath.Dir(path), directive, rest)
	}
	return nil
}

func (s *scanner) checkModuleDirective(rel, dir, directive, rest string) {
	switch directive {
	case "replace":
		_, target, ok := strings.Cut(rest, "=>")
		if !ok {
			return
		}
		fields := strings.Fields(strings.TrimSpace(target))
		if len(fields) == 0 {
			return
		}
		if s.targetsEE(dir, fields[0]) {
			s.add(ruleRoutes, rel, "a replace directive whose target is in ee/: "+rest)
		}
	case "use":
		for _, field := range strings.Fields(rest) {
			if s.targetsEE(dir, field) {
				s.add(ruleRoutes, rel, "a use directive naming ee/: "+rest)
			}
		}
	}
}

// targetsEE reports whether a directive's target is ee/ or under it, either as
// a path relative to the module file or as the module path itself.
func (s *scanner) targetsEE(dir, target string) bool {
	target = strings.Trim(target, `"'`)
	if s.importsEE(target) {
		return true
	}
	if !strings.HasPrefix(target, ".") && !filepath.IsAbs(target) {
		return false
	}
	return s.resolvesIntoEE(dir, target)
}

// noticeStyle is how one file type carries the two-line notice.
type noticeStyle struct {
	open  string
	close string
}

func (st noticeStyle) render(line string) string {
	if st.close == "" {
		return st.open + line
	}
	return st.open + line + st.close
}

var (
	slashStyle = noticeStyle{open: "// "}
	hashStyle  = noticeStyle{open: "# "}
	htmlStyle  = noticeStyle{open: "<!-- ", close: " -->"}
	cssStyle   = noticeStyle{open: "/* ", close: " */"}
	// bareStyle is the sibling `<name>.license` file, which is the notice and
	// nothing else: it has no syntax of its own to carry a comment in.
	bareStyle = noticeStyle{}
)

// noticeStyles is R3's comment syntax by file type. A type that is not here
// has no comment syntax and needs a sibling `.license` file instead.
var noticeStyles = map[string]noticeStyle{
	".go": slashStyle, ".ts": slashStyle, ".tsx": slashStyle,
	".js": slashStyle, ".mjs": slashStyle, ".cjs": slashStyle,
	".sh": hashStyle, ".py": hashStyle, ".tf": hashStyle,
	".yaml": hashStyle, ".yml": hashStyle, ".toml": hashStyle,
	".md": htmlStyle, ".html": htmlStyle, ".plist": htmlStyle,
	".css":     cssStyle,
	".license": bareStyle,
}

// noticeStylesByName is the same table for a file whose type is its whole name:
// a Dockerfile, and the two dot-files the console and the mobile app carry,
// which take `#` comments like the INI and ignore-file formats they are.
var noticeStylesByName = map[string]noticeStyle{
	"Dockerfile": hashStyle,
	".gitignore": hashStyle,
	".npmrc":     hashStyle,
}

// noticeStyleFor answers how one file carries the notice: `#` for a file that
// opens with a shebang, whatever its type, and otherwise the comment syntax
// its type asks for. The second answer is false for a type with no comment
// syntax at all.
func noticeStyleFor(base string, shebang bool) (noticeStyle, bool) {
	if shebang {
		return hashStyle, true
	}
	if st, ok := noticeStyles[strings.ToLower(filepath.Ext(base))]; ok {
		return st, true
	}
	st, ok := noticeStylesByName[base]
	return st, ok
}

// checkNoticeFile holds one tracked file under a licence directory to R3. A
// tracked file missing from the working tree is skipped rather than refused:
// the core-without-ee workflow deletes ee/ without committing, and a checkout
// in that state is a state this test has to pass in.
func (s *scanner) checkNoticeFile(f trackedFile) error {
	target, ok := targetFor(f.path)
	if !ok {
		return nil
	}
	if f.mode == symlinkMode {
		s.add(target.rule(), f.path, "a symlink under a licence directory, which the notice rule refuses")
		return nil
	}
	if f.path == target.readme || f.path == target.licence {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(f.path)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	base := filepath.Base(f.path)
	if tp, ok := thirdPartyFor(f.path); ok {
		s.checkThirdPartyFile(target, tp, f.path, base, data)
		return nil
	}
	lines := fileLines(data)
	shebang := len(lines) > 0 && strings.HasPrefix(lines[0], "#!")
	// An XML declaration is the one other line that has to stay first, so the
	// notice sits below it: the doctype and the root element then follow, and
	// the file is still well-formed XML.
	xmlDecl := !shebang && len(lines) > 0 && strings.HasPrefix(lines[0], "<?xml")
	style, ok := noticeStyleFor(base, shebang)
	if !ok {
		if !s.tracked[f.path+eeSiblingSuffix] {
			s.add(target.rule(), f.path, "this file type carries no comment syntax, so it needs a sibling "+f.path+eeSiblingSuffix+" carrying the notice")
		}
		return nil
	}
	if !shebang && scriptByType(base) {
		if n := misplacedShebang(lines); n > 0 {
			s.add(target.rule(), f.path, fmt.Sprintf("a shebang on line %d rather than the first line, so the notice sits where a script expects its interpreter", n))
			return nil
		}
	}
	offset := 0
	if shebang || xmlDecl {
		offset = 1
	}
	if len(lines) < offset+2 {
		s.add(target.rule(), f.path, "it is shorter than the two-line notice")
		return nil
	}
	pointer := style.render(target.pointer())
	if strings.TrimSpace(lines[offset]) != style.render(noticeSPDX) || strings.TrimSpace(lines[offset+1]) != pointer {
		s.add(target.rule(), f.path, "it does not start with the two-line notice, which for this file type reads `"+style.render(noticeSPDX)+"` then `"+pointer+"`")
	}
	return nil
}

// checkThirdPartyFile holds a vendored third-party file to its own licence: the
// licence text itself is exempt, a `.license` sibling names the licence's SPDX
// identifier on its first line, and every other file has such a sibling.
func (s *scanner) checkThirdPartyFile(target noticeTarget, tp thirdPartyDir, path, base string, data []byte) {
	if base == tp.licence {
		return
	}
	want := "SPDX-License-Identifier: " + tp.spdx
	if strings.HasSuffix(base, eeSiblingSuffix) {
		if lines := fileLines(data); len(lines) == 0 || strings.TrimSpace(lines[0]) != want {
			s.add(target.rule(), path, "a third-party file's sibling notice must start with `"+want+"`")
		}
		return
	}
	if !s.tracked[path+eeSiblingSuffix] {
		s.add(target.rule(), path, "a vendored third-party file needs a sibling "+path+eeSiblingSuffix+" naming its licence")
	}
}

// checkEELicence holds R3's last clause: the licence text exists as soon as
// one file needs it, so a reserved ee/ holding only its README.md passes. It
// reads the ee/ files of the tracked list and no others.
func (s *scanner) checkEELicence(files []trackedFile) {
	source, licence := false, false
	for _, f := range files {
		if target, ok := targetFor(f.path); !ok || target.dir != eeDirName {
			continue
		}
		switch f.path {
		case eeLicenceFile:
			licence = true
		case eeReadmeFile:
		default:
			source = true
		}
	}
	if source && !licence {
		s.add(ruleLicence, eeDirName, "ee/ holds a source file while ee/LICENSE does not exist")
	}
}

func fileLines(data []byte) []string {
	return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
}

// misplacedShebang answers the line number of a `#!` line that is not the
// file's first, or 0. It is asked only about a script by extension, so a
// `#!` inside a document's example code is not a shebang this test reads.
func misplacedShebang(lines []string) int {
	for i := 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "#!") {
			return i + 1
		}
	}
	return 0
}

func scriptByType(base string) bool {
	switch strings.ToLower(filepath.Ext(base)) {
	case ".sh", ".py":
		return true
	}
	return false
}

// moduleRoot answers this module's root from this package's own directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s: these paths assume the test runs from internal/eeboundary: %v", root, err)
	}
	return root
}

// trackedNoticeFiles is `git ls-files -s` over the three licence directories,
// which is R3's scope: an untracked .DS_Store or build output under them does
// not fail the notice rule, and the mode is what tells a symlink from a file.
func trackedNoticeFiles(t *testing.T, root string) []trackedFile {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "ls-files", "-s", "-z", "--", eeDirName+"/", consoleDirName+"/", mobileDirName+"/").Output()
	if err != nil {
		t.Skipf("git ls-files is unavailable (%v): the ee/ notice rule reads the tracked files", err)
	}
	var files []trackedFile
	for _, entry := range strings.Split(string(out), "\x00") {
		meta, path, ok := strings.Cut(entry, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) == 0 {
			continue
		}
		files = append(files, trackedFile{path: path, mode: fields[0]})
	}
	return files
}

// realReport scans the repository this test lives in. Each test scans for
// itself rather than sharing one report: the walk is a second of work, and a
// shared result would hide which rule read what.
func realReport(t *testing.T) *report {
	t.Helper()
	root := moduleRoot(t)
	rep, err := scanTree(root, trackedNoticeFiles(t, root))
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestCoreNeverImportsEE(t *testing.T) {
	rep := realReport(t)
	if rep.goFiles < 100 {
		t.Fatalf("the scan parsed %d Go files outside ee/: it did not see the module, so an empty result would prove nothing", rep.goFiles)
	}
	assertNoFindings(t, rep, ruleImports,
		"Only "+eeAssemblyFile+", with `//go:build ee` and nothing else, may import ee/, and only as the blank import of the registration package (R6, D303 as amended). A core file that links ee/ puts code under another licence into every build of this MIT binary; move the call behind an interface core defines, or drop the import.")
}

func TestCoreNeverEmbedsEE(t *testing.T) {
	assertNoFindings(t, realReport(t), ruleEmbeds,
		"A //go:embed over an ee/ path copies ee/ files into the core binary at build time, which is the import boundary's rule reached by another road. Embed the core asset, or move the feature into ee/.")
}

func TestNoModuleRouteIntoEE(t *testing.T) {
	assertNoFindings(t, realReport(t), ruleRoutes,
		"A nested go.mod or go.work under ee/, a replace or use directive pointing at it, a //go:generate line or a cgo include reaching into it, all make ee/ part of a build again through a road the import scan does not read. Keep ee/ in the one module, reached only through "+eeAssemblyFile+".")
}

func TestEEFilesCarryTheNotice(t *testing.T) {
	rule := noticeRuleFor(eeDirName)
	assertNoFindings(t, realReport(t), rule, noticeWhy(eeDirName))
}

func TestConsoleFilesCarryTheLicenceNotice(t *testing.T) {
	rule := noticeRuleFor(consoleDirName)
	assertNoFindings(t, realReport(t), rule, noticeWhy(consoleDirName))
}

func TestMobileFilesCarryTheLicenceNotice(t *testing.T) {
	rule := noticeRuleFor(mobileDirName)
	assertNoFindings(t, realReport(t), rule, noticeWhy(mobileDirName))
}

// noticeWhy is the failure message one directory's notice rule prints. It names
// the directory's own licence file, because the notice rule is what tells a
// reader of any single file which of the three copies of the same terms covers
// it.
func noticeWhy(dir string) string {
	var target noticeTarget
	for _, candidate := range noticeTargets {
		if candidate.dir == dir {
			target = candidate
		}
	}
	return "Every tracked file under " + dir + "/ starts with the two-line notice in its type's comment syntax: `" +
		noticeSPDX + "`, then `" + target.pointer() + "`. A type with no comment syntax needs a sibling `<name>" +
		eeSiblingSuffix + "` carrying it. Only " + target.readme + " and " + target.licence + " are exempt."
}

func TestEESourceNeedsTheLicence(t *testing.T) {
	assertNoFindings(t, realReport(t), ruleLicence,
		"ee/LICENSE arrives with the first file under ee/ that needs it, so the terms are beside the code they cover. While ee/ is reserved (T1) it holds only ee/README.md and this rule passes vacuously.")
}

func assertNoFindings(t *testing.T, rep *report, rule, why string) {
	t.Helper()
	found := rep.byRule(rule)
	if len(found) == 0 {
		return
	}
	t.Errorf("%s: %d violation(s):\n  %s\n\n%s", rule, len(found), joinFindings(found), why)
}

func joinFindings(fs []finding) string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.String())
	}
	return strings.Join(out, "\n  ")
}

// noticeText renders the two-line notice in one file type's comment syntax, so
// a planted tree reads as the files it stands for.
const (
	eePointer      = "See " + eeLicenceFile + " for the terms."
	consolePointer = "See " + consoleLicenceFile + " for the terms."
	mobilePointer  = "See " + mobileLicenceFile + " for the terms."
)

func noticeText(open, close, pointer string) string {
	st := noticeStyle{open: open, close: close}
	return st.render(noticeSPDX) + "\n" + st.render(pointer) + "\n"
}

const (
	goModFixture     = "module github.com/dimipaun/fugaro\n\ngo 1.27\n"
	registerImport   = `_ "github.com/dimipaun/fugaro/ee/register"`
	assemblyFixture  = "//go:build ee\n\npackage main\n\nimport " + registerImport + "\n"
	licenceFixture   = "MIT License\n\nCopyright (c) 2026\n"
	eeRegisterDetail = "it imports github.com/dimipaun/fugaro/ee/register"
	notTheNotice     = "it does not start with the two-line notice"
)

// plant is one tree built under t.TempDir() with one rule's violation in it,
// and the finding it must produce. wantNone is the other half: a tree that is
// allowed and must produce nothing at all, so the scan cannot pass by
// refusing everything.
type plant struct {
	name  string
	files map[string]string
	links map[string]string
	// trackedFiles overrides the tracked ee/ list; the default is the ee/ tree the
	// plant wrote, read the way a committed tree's `git ls-files -s ee/` would.
	trackedFiles []trackedFile
	want         []string
	none         bool
}

func TestEEBoundaryScanCatchesPlantedViolations(t *testing.T) {
	for _, p := range []plant{
		{
			name: "a core file with the ee tag importing the registration package",
			files: map[string]string{
				"internal/web/x.go": "//go:build ee\n\npackage web\n\nimport " + registerImport + "\n",
			},
			want: []string{"internal/web/x.go: " + eeRegisterDetail},
		},
		{
			name: "an import of the ee path itself",
			files: map[string]string{
				"internal/web/x.go": "package web\n\nimport \"github.com/dimipaun/fugaro/ee\"\n",
			},
			want: []string{"internal/web/x.go: it imports github.com/dimipaun/fugaro/ee"},
		},
		{
			name:  "a symlink outside ee/ that resolves into it",
			files: map[string]string{"internal/web/x.go": "package web\n"},
			links: map[string]string{"internal/web/lib": "../../ee"},
			want:  []string{"internal/web/lib: a symlink resolving into ee/"},
		},
		{
			name: "a core .go file under a nested node_modules directory",
			files: map[string]string{
				"web/node_modules/x/y.go": "package x\n\nimport " + registerImport + "\n",
			},
			want: []string{"web/node_modules/x/y.go: " + eeRegisterDetail},
		},
		{
			name: "a go:embed pattern into ee/",
			files: map[string]string{
				"internal/web/x.go": "package web\n\nimport _ \"embed\"\n\n//go:embed all:../../ee/README.md\nvar zzz string\n",
			},
			want: []string{"internal/web/x.go: a //go:embed pattern into ee/: all:../../ee/README.md"},
		},
		{
			name:  "a nested module under ee/",
			files: map[string]string{"ee/go.mod": "module github.com/dimipaun/fugaro/ee\n"},
			want:  []string{"ee/go.mod: a nested module under ee/"},
		},
		{
			name:  "a root go.mod replacing a module into ee/",
			files: map[string]string{"go.mod": goModFixture + "\nreplace github.com/example/thing => ./ee/thing\n"},
			want:  []string{"go.mod: a replace directive whose target is in ee/"},
		},
		{
			name:  "a go.work using ee/ as a workspace module",
			files: map[string]string{"go.work": "go 1.27\n\nuse ./ee\n"},
			want:  []string{"go.work: a use directive naming ee/"},
		},
		{
			name: "a go:generate line naming ee/",
			files: map[string]string{
				"internal/web/x.go": "package web\n\n//go:generate go run ../../ee/gen\n",
			},
			want: []string{"internal/web/x.go: a //go:generate line naming ee/: go:generate go run ../../ee/gen"},
		},
		{
			name: "a cgo include reaching into ee/",
			files: map[string]string{
				"internal/web/x.go": "package web\n\n// #include \"../../ee/x.h\"\nimport \"C\"\n",
			},
			want: []string{"internal/web/x.go: a cgo directive reaching into ee/: #include \"../../ee/x.h\""},
		},
		{
			name:  "an ee/ source file with no notice",
			files: map[string]string{"ee/x.go": "package ee\n"},
			want:  []string{"ee/x.go: " + notTheNotice},
		},
		{
			name:  "a README.md that is not ee/README.md, with no notice",
			files: map[string]string{"ee/sub/README.md": "# sub\n"},
			want:  []string{"ee/sub/README.md: " + notTheNotice},
		},
		{
			name:  "a file under a dot directory of ee/",
			files: map[string]string{"ee/.hidden/x.go": "package hidden\n"},
			want:  []string{"ee/.hidden/x.go: " + notTheNotice},
		},
		{
			name:  "a file under ee/testdata/",
			files: map[string]string{"ee/testdata/x.go": "package x\n"},
			want:  []string{"ee/testdata/x.go: " + notTheNotice},
		},
		{
			name:  "a script whose shebang is not its first line",
			files: map[string]string{"ee/run.sh": noticeText("# ", "", eePointer) + "#!/bin/sh\n"},
			want:  []string{"ee/run.sh: a shebang on line 3 rather than the first line"},
		},
		{
			name:  "a file type with no comment syntax and no sibling",
			files: map[string]string{"ee/x.json": "{}\n"},
			want:  []string{"ee/x.json: this file type carries no comment syntax, so it needs a sibling ee/x.json.license carrying the notice"},
		},
		{
			name:  "a tracked symlink under ee/",
			files: map[string]string{"ee/x.go": noticeText("// ", "", eePointer) + "package x\n"},
			links: map[string]string{"ee/alias.go": "x.go"},
			want:  []string{"ee/alias.go: a symlink under a licence directory"},
		},
		{
			name:  "a noticed source file while ee/LICENSE does not exist",
			files: map[string]string{"ee/x.go": noticeText("// ", "", eePointer) + "package x\n"},
			want:  []string{"ee: ee/ holds a source file while ee/LICENSE does not exist"},
		},
		{
			name:  "a second assembly file",
			files: map[string]string{"cmd/fugaro/ee_more.go": assemblyFixture},
			want:  []string{"cmd/fugaro/ee_more.go: " + eeRegisterDetail},
		},
		{
			name:  "an assembly file without the build tag",
			files: map[string]string{eeAssemblyFile: "package main\n\nimport " + registerImport + "\n"},
			want:  []string{eeAssemblyFile + ": the one assembly file's build constraint must be exactly //go:build ee, above the package clause"},
		},
		{
			name:  "an assembly file importing another ee package",
			files: map[string]string{eeAssemblyFile: "//go:build ee\n\npackage main\n\nimport _ \"github.com/dimipaun/fugaro/ee/other\"\n"},
			want:  []string{eeAssemblyFile + ": the one assembly file's only import of the ee path must be the blank import"},
		},
		{
			name:  "an assembly file importing the registration package by a name",
			files: map[string]string{eeAssemblyFile: "//go:build ee\n\npackage main\n\nimport register \"github.com/dimipaun/fugaro/ee/register\"\n"},
			want:  []string{eeAssemblyFile + ": the one assembly file's only import of the ee path must be the blank import"},
		},
		{
			name:  "an assembly file importing nothing from ee/",
			files: map[string]string{eeAssemblyFile: "//go:build ee\n\npackage main\n"},
			want:  []string{eeAssemblyFile + ": the one assembly file's only import of the ee path must be the blank import"},
		},
		{
			name:  "an assembly file that is not in package main",
			files: map[string]string{eeAssemblyFile: "//go:build ee\n\npackage fugaro\n\nimport " + registerImport + "\n"},
			want:  []string{eeAssemblyFile + ": the one assembly file must be in package main"},
		},
		{
			name:  "a file named ee_on.go in another directory",
			files: map[string]string{"internal/web/ee_on.go": assemblyFixture},
			want:  []string{"internal/web/ee_on.go: " + eeRegisterDetail},
		},
		{
			name:  "an import of the registration package from internal/cli under the tag",
			files: map[string]string{"internal/cli/x.go": "//go:build ee\n\npackage cli\n\nimport " + registerImport + "\n"},
			want:  []string{"internal/cli/x.go: " + eeRegisterDetail, "internal/cli/x.go: its build constraint names the ee tag"},
		},
		{
			name:  "an assembly file built with ee or linux",
			files: map[string]string{eeAssemblyFile: "//go:build ee || linux\n\npackage main\n\nimport " + registerImport + "\n"},
			want:  []string{eeAssemblyFile + ": the one assembly file's build constraint must be exactly //go:build ee"},
		},
		{
			name:  "an assembly file built when ee is off",
			files: map[string]string{eeAssemblyFile: "//go:build !ee\n\npackage main\n\nimport " + registerImport + "\n"},
			want:  []string{eeAssemblyFile + ": the one assembly file's build constraint must be exactly //go:build ee"},
		},
		{
			name:  "an assembly file whose constraint sits below the package clause",
			files: map[string]string{eeAssemblyFile: "package main\n\n//go:build ee\n\nimport " + registerImport + "\n"},
			want:  []string{eeAssemblyFile + ": the one assembly file's build constraint must be exactly //go:build ee"},
		},
		{
			name:  "an assembly file with only a legacy build line",
			files: map[string]string{eeAssemblyFile: "// +build ee\n\npackage main\n\nimport " + registerImport + "\n"},
			want:  []string{eeAssemblyFile + ": the one assembly file's build constraint must be exactly //go:build ee"},
		},
		{
			name:  "another core file whose constraint names the ee tag",
			files: map[string]string{"internal/web/x.go": "//go:build ee\n\npackage web\n"},
			want:  []string{"internal/web/x.go: its build constraint names the ee tag"},
		},
		{
			name: "a go:linkname onto an ee symbol",
			files: map[string]string{
				"internal/web/x.go": "package web\n\nimport _ \"unsafe\"\n\n//go:linkname hidden github.com/dimipaun/fugaro/ee/register.x\nfunc hidden()\n",
			},
			want: []string{"internal/web/x.go: a //go:linkname onto the ee path: github.com/dimipaun/fugaro/ee/register.x"},
		},
		{
			name:  "an import of a path that merely ends in eevee",
			files: map[string]string{"internal/web/x.go": "package web\n\nimport _ \"github.com/example/eevee\"\n"},
			none:  true,
		},
		{
			name:  "a reserved ee/ holding only its README.md",
			files: map[string]string{eeReadmeFile: "# ee/ is reserved\n"},
			none:  true,
		},
		{
			name: "a shebang script whose notice follows the shebang",
			files: map[string]string{
				eeLicenceFile: licenceFixture,
				"ee/run.sh":   "#!/bin/sh\n" + noticeText("# ", "", eePointer),
			},
			none: true,
		},
		{
			name:  "the assembly file in the one shape R6 allows",
			files: map[string]string{eeAssemblyFile: assemblyFixture},
			none:  true,
		},
		{
			name: "each type's notice passes",
			files: map[string]string{
				eeLicenceFile:                 licenceFixture,
				"ee/x.go":                     noticeText("// ", "", eePointer) + "package x\n",
				"ee/notes.md":                 noticeText("<!-- ", " -->", eePointer) + "# notes\n",
				"ee/style.css":                noticeText("/* ", " */", eePointer) + "a {}\n",
				"ee/build.yaml":               noticeText("# ", "", eePointer) + "a: b\n",
				"ee/Dockerfile":               noticeText("# ", "", eePointer) + "FROM scratch\n",
				"ee/x.json":                   "{}\n",
				"ee/x.json" + eeSiblingSuffix: noticeText("", "", eePointer),
			},
			none: true,
		},
		{
			name: "each type's notice passes in the console and the mobile app too",
			files: map[string]string{
				consoleLicenceFile: licenceFixture,
				// The pointer names each directory's own licence file, so the two
				// lines differ between the two app directories and neither may carry
				// the other's.
				consoleDirName + "/src/main.ts":                    noticeText("// ", "", consolePointer) + "export const main = 1;\n",
				consoleDirName + "/src/app.css":                    noticeText("/* ", " */", consolePointer) + "a {}\n",
				consoleDirName + "/src/index.html":                 "<?xml version=\"1.0\"?>\n" + noticeText("<!-- ", " -->", consolePointer) + "<html></html>\n",
				consoleDirName + "/package.json":                   "{}\n",
				consoleDirName + "/package.json" + eeSiblingSuffix: noticeText("", "", consolePointer),
				consoleDirName + "/build/entitlements.plist":       "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" + noticeText("<!-- ", " -->", consolePointer) + "<plist></plist>\n",
				consoleDirName + "/.gitignore":                     noticeText("# ", "", consolePointer) + "dist/\n",
				consoleDirName + "/scripts/pack.sh":                "#!/bin/sh\n" + noticeText("# ", "", consolePointer),
				mobileLicenceFile:                                  licenceFixture,
				mobileDirName + "/src/a.js":                        noticeText("// ", "", mobilePointer) + "export const a = 1;\n",
				mobileDirName + "/app.json":                        "{}\n",
				mobileDirName + "/app.json" + eeSiblingSuffix:      noticeText("", "", mobilePointer),
				mobileDirName + "/.gitignore":                      noticeText("# ", "", mobilePointer) + "node_modules/\n",
			},
			none: true,
		},
		{
			name:  "a console file with no notice at all",
			files: map[string]string{consoleDirName + "/src/main.ts": "export const main = 1;\n"},
			want:  []string{consoleDirName + "/src/main.ts: " + notTheNotice},
		},
		{
			name: "a mobile file carrying the console's pointer instead of its own",
			files: map[string]string{
				mobileDirName + "/src/a.js": noticeText("// ", "", consolePointer) + "export const a = 1;\n",
			},
			want: []string{mobileDirName + "/src/a.js: " + notTheNotice},
		},
		{
			name:  "a console JSON file whose sibling is named but empty",
			files: map[string]string{consoleDirName + "/package.json": "{}\n", consoleDirName + "/other.json" + eeSiblingSuffix: noticeText("", "", consolePointer)},
			want:  []string{consoleDirName + "/package.json: this file type carries no comment syntax"},
		},
		{
			name:  "a mobile asset with no sibling",
			files: map[string]string{mobileDirName + "/assets/icon.png": "\x89PNG\r\n"},
			want:  []string{mobileDirName + "/assets/icon.png: this file type carries no comment syntax"},
		},
		{
			name: "a mobile file whose notice sits below its shebang",
			files: map[string]string{
				mobileDirName + "/scripts/run.sh": noticeText("# ", "", mobilePointer) + "#!/bin/sh\n",
			},
			want: []string{mobileDirName + "/scripts/run.sh: a shebang on line 3 rather than the first line"},
		},
		{
			name: "a vendored font with its licence and its sibling",
			files: map[string]string{
				consoleDirName + "/src/renderer/fonts/ibm-plex-mono/LICENSE.txt":                       "SIL Open Font License\n",
				consoleDirName + "/src/renderer/fonts/ibm-plex-mono/IBMPlexMono-Regular.woff2":         "wOF2",
				consoleDirName + "/src/renderer/fonts/ibm-plex-mono/IBMPlexMono-Regular.woff2.license": "SPDX-License-Identifier: OFL-1.1\n",
			},
			none: true,
		},
		{
			name: "a vendored font with no sibling, and a sibling naming the wrong licence",
			files: map[string]string{
				consoleDirName + "/src/renderer/fonts/ibm-plex-mono/IBMPlexMono-Medium.woff2":          "wOF2",
				consoleDirName + "/src/renderer/fonts/ibm-plex-mono/IBMPlexMono-Regular.woff2":         "wOF2",
				consoleDirName + "/src/renderer/fonts/ibm-plex-mono/IBMPlexMono-Regular.woff2.license": noticeText("", "", consolePointer),
			},
			want: []string{
				consoleDirName + "/src/renderer/fonts/ibm-plex-mono/IBMPlexMono-Medium.woff2: a vendored third-party file needs a sibling",
				consoleDirName + "/src/renderer/fonts/ibm-plex-mono/IBMPlexMono-Regular.woff2.license: a third-party file's sibling notice must start with",
			},
		},
		{
			name:  "a symlink under the console",
			links: map[string]string{consoleDirName + "/alias.ts": "src/main.ts"},
			want:  []string{consoleDirName + "/alias.ts: a symlink under a licence directory"},
		},
		{
			name:         "a tracked console file deleted from the working tree",
			trackedFiles: []trackedFile{{path: consoleDirName + "/src/main.ts", mode: "100644"}},
			none:         true,
		},
		{
			name:         "a tracked ee/ file deleted from the working tree",
			trackedFiles: []trackedFile{{path: "ee/x.go", mode: "100644"}, {path: eeLicenceFile, mode: "100644"}},
			none:         true,
		},
	} {
		t.Run(p.name, func(t *testing.T) {
			root, files := plantTree(t, p)
			rep, err := scanTree(root, files)
			if err != nil {
				t.Fatal(err)
			}
			got := rep.findings
			if p.none {
				if len(got) > 0 {
					t.Errorf("a tree this rule allows was refused:\n  %s", joinFindings(got))
				}
				return
			}
			var missing []string
			for _, want := range p.want {
				if !slices.ContainsFunc(got, func(f finding) bool { return strings.Contains(f.String(), want) }) {
					missing = append(missing, want)
				}
			}
			if len(missing) > 0 {
				t.Errorf("the planted violation(s)\n  %s\nwere not reported; the scan answered %d finding(s):\n  %s", strings.Join(missing, "\n  "), len(got), joinFindings(got))
			}
		})
	}
}

// plantTree writes one plant as a tree under t.TempDir() and answers it with
// the tracked ee/ list its scan is handed. Every plant carries a go.mod,
// because the module path the scan checks imports against is read from it.
func plantTree(t *testing.T, p plant) (string, []trackedFile) {
	t.Helper()
	root := t.TempDir()
	if _, ok := p.files["go.mod"]; !ok {
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goModFixture), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range p.files {
		writeFixture(t, root, name, []byte(body))
	}
	for name, target := range p.links {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	if p.trackedFiles != nil {
		return root, p.trackedFiles
	}
	return root, treeNoticeFiles(t, root)
}

func writeFixture(t *testing.T, root, name string, body []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// treeNoticeFiles lists everything under a planted tree's licence directories,
// the way a committed tree's `git ls-files -s` would: no directory is skipped,
// and a symlink is marked with the mode git records for one.
func treeNoticeFiles(t *testing.T, root string) []trackedFile {
	t.Helper()
	var out []trackedFile
	for _, dir := range []string{eeDirName, consoleDirName, mobileDirName} {
		out = append(out, treeNoticeFilesIn(t, root, dir)...)
	}
	return out
}

func treeNoticeFilesIn(t *testing.T, root, dir string) []trackedFile {
	t.Helper()
	var out []trackedFile
	err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // a plant with no such directory at all: it contributes nothing
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		mode := "100644"
		if d.Type()&fs.ModeSymlink != 0 {
			mode = symlinkMode
		}
		out = append(out, trackedFile{path: filepath.ToSlash(rel), mode: mode})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
