// Package ciselect decides which tests a change can reach: the reverse import
// graph, the recorded read-map of what the tests open by path, the packer that
// fits the chosen packages into the shards' legs, and the selector that
// composes them.
package ciselect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// rootMark stands for the repository root inside a path expression the scan
// evaluates. "../../docs/x.md" and testutil.ModuleRoot() both lead there, and
// carrying the marker lets filepath.Join of a root with literals resolve the way
// the test resolves it.
const rootMark = "\x00root\x00"

// ReadMap is the recorded map of what each test package reads: the repository
// files its tests name by literal, the packages they build by import path, and
// the packages that walk the tree and so have no finite list at all (D3043). It
// is committed as docs/ci-affected-tests/read-map.json, refreshed by the
// nightly full run, and read by the selector, which unions it with the import
// graph of the changed paths (CS1, CS4, CS2b).
type ReadMap struct {
	Packages []PackageReads `json:"packages"`
}

// PackageReads is one test package's entry in the read map.
type PackageReads struct {
	// Package is the package in the form the shard matrix names it
	// ("./internal/cli").
	Package string `json:"package"`
	// Reads is every repository path a test of this package reaches: an exact
	// file, a glob for a directory or for a literal pattern ("docs/**",
	// "internal/cli/docs_*_test.go"), or every file of a package the tests
	// build or run by import path. Paths are repository-relative and
	// slash-separated.
	Reads []string `json:"reads,omitempty"`
	// Ignores is what a walker of this package states it steps out of, as it
	// states it: a bare name is a directory name at any depth, a path with a
	// slash is repository-relative.
	Ignores []string `json:"ignores,omitempty"`
	// Any says the package reads a path it builds at run time or walks a tree,
	// so no list of files is complete and a changed path it does not name still
	// reaches it.
	Any bool `json:"any,omitempty"`
}

// LoadReadMap decodes a committed read map, refusing a field this build does not
// know: a map written by a newer selector must fail loudly rather than be read
// as if the unknown entries were absent.
func LoadReadMap(file string) (ReadMap, error) {
	f, err := os.Open(file)
	if err != nil {
		return ReadMap{}, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var m ReadMap
	if err := dec.Decode(&m); err != nil {
		return ReadMap{}, fmt.Errorf("%s: %w", file, err)
	}
	return m, nil
}

// MarshalReadMap returns the read map as it is committed: indented, and with a
// trailing newline so the file is a text file a diff reads well.
func MarshalReadMap(m ReadMap) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Scan reads the checkout at root and reports what the tests in it read beyond
// the import graph: every _test.go file's path literals, the same for a non-test
// file that reads a repository path (its reads reach every test package that
// imports it), the shell a test runs (a sourced library is read with the script
// that sources it), the closure of a package a test builds by import path, and
// `any` for the packages whose paths it builds at run time or that walk the
// tree (F2, F3, R4).
//
// The map is a static reading of the tree: it is what a scan can see, not what
// a run opens. CS2b's trace refines it with what the tests really opened.
func Scan(root string) (ReadMap, error) {
	s := &scanner{
		root:      root,
		files:     map[string]bool{},
		dirs:      map[string]bool{".": true},
		pkgs:      map[string]*pkgReads{},
		globCache: map[string]bool{},
	}
	if err := s.index(); err != nil {
		return ReadMap{}, err
	}
	if err := s.scanGo(); err != nil {
		return ReadMap{}, err
	}
	for _, p := range s.pkgs {
		s.followShell(&p.self)
		s.followShell(&p.helper)
	}
	return s.mapOf(), nil
}

// scanner is one pass over a checkout.
type scanner struct {
	root  string
	goMod string // the module's path, for resolving an import path to a directory
	files map[string]bool
	dirs  map[string]bool
	pkgs  map[string]*pkgReads
	// byDepth holds the same paths split into segments, by how many they have:
	// a pattern with no "**" in it can only match a path of its own depth, and
	// looking at those alone is what keeps a scan of a tree this size quick.
	byDepth map[int][][]string
	// globCache remembers a pattern's answer: the same pattern is written in
	// many test files, and what it matches cannot change under them.
	globCache map[string]bool
}

// pkgReads is what one directory contributes. A read of a _test.go file belongs
// to that directory; one of a non-test file belongs to the directory and to
// every test package that imports it, which is why the two are kept apart
// (fixture.go's walk of testdata/fixture-repo reaches internal/runner).
type pkgReads struct {
	dir     string
	tests   bool
	imports map[string]bool
	self    reads
	helper  reads
}

// reads is one set of paths, with the two escape hatches of R4.
type reads struct {
	files   map[string]bool
	ignores map[string]bool
	any     bool
}

func newReads() reads {
	return reads{files: map[string]bool{}, ignores: map[string]bool{}}
}

func (r *reads) add(p string) {
	if p != "" {
		r.files[p] = true
	}
}

func (r *reads) merge(o reads) {
	for p := range o.files {
		r.files[p] = true
	}
	for p := range o.ignores {
		r.ignores[p] = true
	}
	r.any = r.any || o.any
}

// index records every file and directory of the checkout. Everything the commit
// holds is recorded, node_modules and the dot directories included: the tests of
// this repository read a file wherever it is, and a path that is not in the
// index cannot be resolved to the repository at all.
//
// The commit, and not the disk, is the point: a path this checkout ignores — the
// JUnit reports `fugaro verify test` leaves in reports/, a built binary in the
// tree root — is in no commit and in no diff, and recording a read of one would
// make the map differ between two checkouts of the same commit. A tree that is
// not a checkout, which is what a test's fixture is, is walked as it stands
// instead, .git left out because a walk of it costs more than the map is worth.
func (s *scanner) index() error {
	if files, ok := checkoutFiles(s.root); ok {
		for _, rel := range files {
			s.record(rel)
		}
		s.bucket()
		return nil
	}
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			s.dirs[rel] = true
			return nil
		}
		s.record(rel)
		return nil
	})
	if err != nil {
		return err
	}
	s.bucket()
	return nil
}

// bucket splits the index into segments by depth, once the index is complete.
func (s *scanner) bucket() {
	s.byDepth = map[int][][]string{}
	for _, set := range []map[string]bool{s.files, s.dirs} {
		for name := range set {
			if name == "." {
				continue
			}
			segs := strings.Split(name, "/")
			s.byDepth[len(segs)] = append(s.byDepth[len(segs)], segs)
		}
	}
}

// checkoutFiles lists the paths the checkout at root holds: the tracked files
// and the untracked ones the ignore files do not cover. ok is false when git
// cannot answer — no git, or a directory that is not a repository — which is the
// caller's cue to read the disk instead. The ignore files are the checkout's own
// and git reads them, so a rule this repository adds later — a nested
// .gitignore, a negation, an exclude file — needs no second implementation here
// and cannot change what the map means.
//
// The scan and CS2b's trace both read the checkout with this: a path a commit
// does not hold is in no diff, and recording a read of one would make the map
// differ between two checkouts of the same commit.
func checkoutFiles(root string) ([]string, bool) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	// No home directory and no system config: what the checkout holds must not
	// depend on the machine the scan runs on.
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	var files []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	return files, true
}

// record adds one repository file and the directories that hold it to the index.
func (s *scanner) record(rel string) {
	rel = path.Clean(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return
	}
	s.files[rel] = true
	for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
		s.dirs[dir] = true
	}
}

// scanGo parses every Go file the go tool would build and aggregates what each
// directory reads.
func (s *scanner) scanGo() error {
	if err := s.module(); err != nil {
		return err
	}
	type file struct {
		dir  string
		test bool
		got  fileScan
	}
	var files []file
	for _, rel := range s.goFiles() {
		got, err := s.scanGoFile(rel)
		if err != nil {
			return err
		}
		files = append(files, file{dir: path.Dir(rel), test: strings.HasSuffix(rel, "_test.go"), got: got})
	}
	// Two passes: every import edge is known before any closure is taken, so a
	// package that follows the file naming it in the tree counts like any other.
	for _, f := range files {
		for _, imp := range f.got.imports {
			s.pkg(f.dir).imports[imp] = true
		}
	}
	for _, f := range files {
		p := s.pkg(f.dir)
		if f.test {
			p.tests = true
			p.self.merge(f.got.reads)
			p.self.merge(s.builds(f.got.builds))
		} else if f.got.spread {
			// A helper that opens, walks or builds something hands its reads to
			// every test package that imports it. A file that merely mentions a
			// path-shaped string — a classifier's table of patterns, a file name
			// in a constant — hands over nothing: it read no file, and spreading
			// its strings would put a change to scripts/release.sh in the reach
			// of every package that imports the table.
			p.helper.merge(f.got.reads)
			p.helper.merge(s.builds(f.got.builds))
		}
		for _, e := range f.got.embeds {
			// An embedded file is the package's own: it is built into that
			// package's binary, and an importer reads it only through that
			// package's code.
			p.self.add(e)
		}
	}
	return nil
}

// builds turns the packages a file builds into the reads that building them
// implies: their directories and the directories of everything they import.
func (s *scanner) builds(dirs []string) reads {
	out := newReads()
	for _, dir := range dirs {
		for _, d := range s.closure(dir) {
			out.add(d + "/**")
		}
	}
	return out
}

// goFiles lists the Go files the go tool would build, in the tree's own sorted
// order. A testdata directory, a vendor directory and a directory whose name
// begins with "." or "_" hold no package: that is the go command's own rule, and
// a fixture in testdata is a file the tests read, not a package that reads.
func (s *scanner) goFiles() []string {
	var out []string
	for rel := range s.files {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		if skipGoDir(path.Dir(rel)) {
			continue
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

func skipGoDir(dir string) bool {
	for _, part := range strings.Split(dir, "/") {
		if part == "testdata" || part == "vendor" {
			return true
		}
		if strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_") {
			return true
		}
	}
	return false
}

func (s *scanner) pkg(dir string) *pkgReads {
	p, ok := s.pkgs[dir]
	if !ok {
		p = &pkgReads{dir: dir, imports: map[string]bool{}, self: newReads(), helper: newReads()}
		s.pkgs[dir] = p
	}
	return p
}

// module reads the module path out of go.mod, so an import path can be told
// apart from a directory that happens to share its name.
func (s *scanner) module() error {
	data, err := os.ReadFile(filepath.Join(s.root, "go.mod"))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			s.goMod = strings.TrimSpace(rest)
			return nil
		}
	}
	return fmt.Errorf("%s/go.mod names no module", s.root)
}

// mapOf is the finished map: a directory with tests is an entry, and a helper's
// reads are copied into every test package that imports it, directly or
// transitively. A directory that holds tests but reaches nothing beyond its own
// package is left out.
func (s *scanner) mapOf() ReadMap {
	dirs := make([]string, 0, len(s.pkgs))
	for dir, p := range s.pkgs {
		if p.tests {
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)

	out := ReadMap{Packages: []PackageReads{}}
	for _, dir := range dirs {
		p := s.pkgs[dir]
		got := newReads()
		got.merge(p.self)
		if p.helper.files != nil || p.helper.any {
			got.merge(p.helper)
		}
		for imported := range s.reaches(dir) {
			if h, ok := s.pkgs[imported]; ok {
				got.merge(h.helper)
			}
		}
		if len(got.files) == 0 && !got.any {
			continue
		}
		out.Packages = append(out.Packages, PackageReads{
			Package: "./" + dir,
			Reads:   sorted(got.files),
			Ignores: sorted(got.ignores),
			Any:     got.any,
		})
	}
	return out
}

// reaches is every directory the package at dir imports, directly or
// transitively, its tests' imports included: the scan reads import statements
// rather than the go tool's graph so that a helper's reads reach the test
// packages that use it under every build tag.
func (s *scanner) reaches(dir string) map[string]bool {
	seen := map[string]bool{}
	queue := []string{dir}
	for len(queue) > 0 {
		next := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		p, ok := s.pkgs[next]
		if !ok {
			continue
		}
		for imp := range p.imports {
			if seen[imp] {
				continue
			}
			seen[imp] = true
			queue = append(queue, imp)
		}
	}
	delete(seen, dir)
	return seen
}

// fileScan is what one Go file's source says.
type fileScan struct {
	reads   reads
	imports []string
	embeds  []string // the repository files the file's //go:embed directives name
	builds  []string // the package directories the file builds with the go command
	spread  bool     // the file opens, walks or builds something of the repository
}

// scanGoFile parses rel and reports what it reads and which repository
// directories it imports. A non-test file that reads nothing of the repository
// is not a helper: internal/cache walks a directory of its own, and marking its
// importers `any` would say a cache directory is a repository path.
func (s *scanner) scanGoFile(rel string) (fileScan, error) {
	src, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return fileScan{}, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return fileScan{}, err
	}
	fs := &fileScan{reads: newReads()}
	fsm := &fileScanner{scan: s, dir: path.Dir(rel), test: strings.HasSuffix(rel, "_test.go"), out: fs, values: map[string]value{}}
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if dir, ok := s.dirOf(p); ok {
			fs.imports = append(fs.imports, dir)
		}
	}
	fs.embeds = s.embeds(rel, f)
	fsm.file(f)
	return *fs, nil
}

// embeds is the repository files a file's //go:embed directives name. The build
// reads them into the binary, so a change to one is a change to the package it
// is built into — the same reach a test literal has, and no import edge of the
// package says it (the diagnose agent's guide, the recipe catalog's YAML).
func (s *scanner) embeds(rel string, f *ast.File) []string {
	dir := path.Dir(rel)
	seen := map[string]bool{}
	for _, group := range f.Comments {
		for _, c := range group.List {
			rest, ok := strings.CutPrefix(c.Text, "//go:embed")
			if !ok {
				continue
			}
			for _, pattern := range strings.Fields(rest) {
				for _, p := range s.resolve(dir, strings.TrimPrefix(pattern, "all:")) {
					seen[p] = true
				}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	return sorted(seen)
}

// dirOf resolves an import path to a repository directory, and reports whether
// it is one of this repository's.
func (s *scanner) dirOf(importPath string) (string, bool) {
	if s.goMod == "" || !strings.HasPrefix(importPath, s.goMod) {
		return "", false
	}
	rest := strings.TrimPrefix(importPath, s.goMod)
	if rest == "" {
		return ".", true
	}
	dir := path.Clean(strings.TrimPrefix(rest, "/"))
	if !s.dirs[dir] {
		return "", false
	}
	return dir, true
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// value is what one path expression evaluates to: the text with rootMark
// standing for the repository root, whether the scan could read the text at all,
// whether it may denote a path inside the repository, and the literal prefix of a
// text the scan cannot read in full.
type value struct {
	text    string
	known   bool
	rooted  bool
	notRepo bool // the scan can see the path is not the repository's
	// prefix is the leading literal text of a text that is not known in full:
	// "docs/" in `"docs/" + name`. It is what the read names even when the name
	// is built at run time, and it is empty when the text does not begin with a
	// literal at all ("a path built from a variable is nothing the scan can
	// place"). A prefix is only ever a directory, a file or a pattern of this
	// repository, never a guess about where an unknown part leads.
	prefix string
	// alts are the texts a name bound by a range over a literal container may
	// take: `for name, pkg := range map[string]string{"a": "x/y"}` leaves pkg one
	// of the table's values, which is every one of them and no other. Text is
	// empty for such a value — the name is not any single one of them — and alts
	// is empty for every value that is not a table's.
	alts []string
}

// rootedText reports whether a path text can denote a path inside the
// repository: it climbs out of the file's own directory, or it is anchored at
// the repository root by a helper call.
func rootedText(text string) bool {
	return strings.Contains(text, rootMark) || text == ".." || strings.HasPrefix(text, "../")
}

// rootHelperNames are the functions that answer the repository root, so a path
// built from one is a repository path. The set is the names this repository
// uses; a name is matched whole so that a test fixture root — a *cobra.Command
// in the ee trees — is not mistaken for it.
var rootHelperNames = map[string]bool{
	"ModuleRoot": true, "moduleRoot": true, "RepoRoot": true, "repoRoot": true,
	"TestRoot": true, "testRoot": true, "RepositoryRoot": true, "repositoryRoot": true,
	"repoDir": true, "RepoDir": true, "rootDir": true, "RootDir": true,
}

// fileScanner reads one Go file: it records what the file's literals name, marks
// a package `any` where a path is built at run time or walked, and keeps the
// values of the identifiers a path is built from, so that
// filepath.Join("..", "..", "docs", x) is seen as a repository path even when x
// is not one.
type fileScanner struct {
	scan *scanner
	dir  string
	test bool
	out  *fileScan
	// values are the file's own bindings: what its top-level declarations hold,
	// which every function of the file can see.
	values map[string]value
	// locals are the scopes of the functions being read, innermost last. A
	// declared function starts with an empty one — another function's `root` is
	// not this function's `root` — and a function literal inherits the bindings
	// its enclosing function holds, which is what a closure closes over.
	locals []*scope
}

// scope is what one function knows: the values it bound, and the names it
// declares itself — its receiver, its parameters, its results — which hold
// nothing the scan can read and shadow anything of the same name outside the
// function, a caller's argument not being the file's variable.
type scope struct {
	values  map[string]value
	unknown map[string]bool
}

func newScope() *scope {
	return &scope{values: map[string]value{}, unknown: map[string]bool{}}
}

// file walks one parsed file: its declarations first, then every function body.
func (f *fileScanner) file(file *ast.File) {
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.GenDecl:
			f.genDecl(d)
		case *ast.FuncDecl:
			f.decl(d)
		}
	}
}

// decl reads one declared function: a scope of its own, then the names the
// function declares, then its body. The scope is what keeps a name from meaning
// whatever the last function to touch it meant: a `root := t.TempDir()` in one
// test function must not turn a `walkAll(t, root string)` beside it into a walk
// of a scratch directory, which is a walker recorded as reading nothing at all.
func (f *fileScanner) decl(d *ast.FuncDecl) {
	f.push()
	defer f.pop()
	f.declares(d.Recv)
	f.declares(d.Type.Params)
	f.declares(d.Type.Results)
	f.body(d.Body)
}

// funcLit reads one function literal. It inherits the bindings around it, since
// that is what a closure closes over, and the names it declares itself are its
// own.
func (f *fileScanner) funcLit(lit *ast.FuncLit) {
	f.push()
	defer f.pop()
	f.declares(lit.Type.Params)
	f.declares(lit.Type.Results)
	f.body(lit.Body)
}

// declares marks the names a signature declares as holding nothing the scan can
// read. Marked, and not bound to a value: the scan cannot read what a caller
// passes — a walk of a parameter is read by `walk` as possibly the repository,
// which is `any`, and reading it as scratch because another function once bound
// the same name to a `t.TempDir()` left the walker neither `any` nor listed
// (R4) — but an assignment to one is the value the function gives it, and
// merging that with the unknown a caller passed would lose the read:
// `func wsAccess(t *testing.T) (path, src string) { path =
// filepath.Join(dir, "workspace_access.tf") }` names that file. A name the
// function declares itself is not rooted either: a rooted value is evidence of
// a repository path for every read, so rooting parameters marks every package
// of the map — 147 of them, against 135 without — `any`, which saves nothing.
func (f *fileScanner) declares(fields *ast.FieldList) {
	sc := f.scope()
	if sc == nil || fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			if name.Name == "_" {
				continue
			}
			sc.unknown[name.Name] = true
		}
	}
}

func (f *fileScanner) push() { f.locals = append(f.locals, newScope()) }

func (f *fileScanner) pop() {
	if len(f.locals) > 0 {
		f.locals = f.locals[:len(f.locals)-1]
	}
}

// scope is the innermost function's scope, which is where a name a function
// binds lands, or nil between functions, where only the file's own bindings
// are written.
func (f *fileScanner) scope() *scope {
	if len(f.locals) == 0 {
		return nil
	}
	return f.locals[len(f.locals)-1]
}

// lookup is what a name holds: the innermost function's binding or declared
// name, then an enclosing function's, then the file's own.
func (f *fileScanner) lookup(name string) value {
	for i := len(f.locals) - 1; i >= 0; i-- {
		if v, ok := f.locals[i].values[name]; ok {
			return v
		}
		if f.locals[i].unknown[name] {
			return value{}
		}
	}
	return f.values[name]
}

func (f *fileScanner) genDecl(d *ast.GenDecl) {
	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		if len(vs.Names) == len(vs.Values) {
			for i, name := range vs.Names {
				f.bind(name.Name, f.eval(vs.Values[i]))
			}
		}
		for _, v := range vs.Values {
			f.inspect(v)
		}
	}
}

func (f *fileScanner) body(b *ast.BlockStmt) {
	if b == nil {
		return
	}
	for _, st := range b.List {
		f.stmt(st)
	}
}

func (f *fileScanner) stmt(st ast.Stmt) {
	switch s := st.(type) {
	case nil:
	case *ast.AssignStmt:
		if len(s.Lhs) == len(s.Rhs) {
			for i, lhs := range s.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					f.bind(id.Name, f.eval(s.Rhs[i]))
				}
			}
		}
		for _, e := range s.Lhs {
			f.inspect(e)
		}
		for _, e := range s.Rhs {
			f.inspect(e)
		}
	case *ast.DeclStmt:
		if d, ok := s.Decl.(*ast.GenDecl); ok {
			f.genDecl(d)
		}
	case *ast.ExprStmt:
		f.inspect(s.X)
	case *ast.ReturnStmt:
		for _, e := range s.Results {
			f.inspect(e)
		}
	case *ast.IfStmt:
		f.stmt(s.Init)
		f.inspect(s.Cond)
		f.body(s.Body)
		f.stmt(s.Else)
	case *ast.ForStmt:
		f.stmt(s.Init)
		f.inspect(s.Cond)
		f.stmt(s.Post)
		f.body(s.Body)
	case *ast.RangeStmt:
		if lit, ok := s.X.(*ast.CompositeLit); ok {
			f.bindRange(s, lit)
		}
		f.inspect(s.X)
		f.body(s.Body)
	case *ast.BlockStmt:
		f.body(s)
	case *ast.SwitchStmt:
		f.stmt(s.Init)
		f.inspect(s.Tag)
		f.body(s.Body)
	case *ast.TypeSwitchStmt:
		f.stmt(s.Init)
		f.stmt(s.Assign)
		f.body(s.Body)
	case *ast.SelectStmt:
		f.body(s.Body)
	case *ast.CaseClause:
		for _, e := range s.List {
			f.inspect(e)
		}
		for _, st := range s.Body {
			f.stmt(st)
		}
	case *ast.CommClause:
		f.stmt(s.Comm)
		for _, st := range s.Body {
			f.stmt(st)
		}
	case *ast.LabeledStmt:
		f.stmt(s.Stmt)
	case *ast.GoStmt:
		f.inspect(s.Call)
	case *ast.DeferStmt:
		f.inspect(s.Call)
	case *ast.SendStmt:
		f.inspect(s.Chan)
		f.inspect(s.Value)
	case *ast.IncDecStmt:
		f.inspect(s.X)
	}
}

// bind records what an identifier holds, in the scope the scan is reading. Two
// assignments that disagree leave the value unknown, but keep the rootedness of
// either: a name that holds a repository path at one assignment and a scratch
// directory at another is a name that may be the repository, and reading the
// last assignment alone would leave the walker neither `any` nor listed — the
// false negative R4 forbids.
func (f *fileScanner) bind(name string, v value) {
	bindings := f.values
	if sc := f.scope(); sc != nil {
		bindings = sc.values
	}
	if old, ok := bindings[name]; ok {
		if !old.known || !v.known || old.text != v.text {
			v.known = false
		}
		v.rooted = v.rooted || old.rooted
		if old.rooted || v.rooted {
			v.notRepo = false
		}
		v.alts = mergeAlts(old.alts, v.alts)
	}
	bindings[name] = v
}

// mergeAlts is the alternatives of a name assigned twice: a name that is one of
// a table's texts at one assignment and one of another table's at the next is one
// of either set, and a name assigned anything else is not a table's text at all.
func mergeAlts(old, new []string) []string {
	if len(old) == 0 || len(new) == 0 {
		return nil
	}
	return append(old[:len(old):len(old)], new...)
}

// bindRange binds the names a range statement takes its values from when the
// container is written out as a literal: `for name, pkg := range
// map[string]string{...}` leaves pkg one of the table's values, and a `go build
// ... pkg` in the body names the packages the table holds even though no single
// literal is the argument (internal/testutil/build.go builds cmd/fugaro that
// way). A map literal binds the key and the value; a slice or array literal
// binds the value alone. A container holding anything the scan cannot read binds
// nothing — half a table is not the table, and the argument is then unknown,
// which `any` covers.
func (f *fileScanner) bindRange(s *ast.RangeStmt, lit *ast.CompositeLit) {
	var keys, vals []string
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			v := f.eval(el)
			if !v.known {
				return
			}
			vals = append(vals, v.text)
			continue
		}
		k, v := f.eval(kv.Key), f.eval(kv.Value)
		if !v.known {
			return
		}
		vals = append(vals, v.text)
		if k.known {
			keys = append(keys, k.text)
		}
	}
	if id, ok := s.Key.(*ast.Ident); ok && len(keys) > 0 && id.Name != "_" {
		f.bind(id.Name, value{alts: keys})
	}
	if id, ok := s.Value.(*ast.Ident); ok && len(vals) > 0 && id.Name != "_" {
		f.bind(id.Name, value{alts: vals})
	}
}

// inspect walks one expression for everything this scan can see in it: the
// literals that name repository files, the calls that build a path, walk a tree
// or run a package, and the function literals, whose bodies hold assignments of
// their own.
func (f *fileScanner) inspect(e ast.Expr) {
	if e == nil {
		return
	}
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			f.literal(n)
		case *ast.CallExpr:
			f.call(n)
		case *ast.FuncLit:
			f.funcLit(n)
			return false
		}
		return true
	})
}

func (f *fileScanner) eval(e ast.Expr) value {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return value{}
		}
		text, err := strconv.Unquote(e.Value)
		if err != nil {
			text = e.Value
		}
		return value{text: text, known: true, rooted: rootedText(text), prefix: text}
	case *ast.ParenExpr:
		return f.eval(e.X)
	case *ast.Ident:
		return f.lookup(e.Name)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return value{}
		}
		l, r := f.eval(e.X), f.eval(e.Y)
		v := value{text: l.text + r.text, known: l.known && r.known, rooted: l.rooted || r.rooted, prefix: l.prefix}
		if l.known {
			v.prefix = l.text + r.prefix
		}
		return v
	case *ast.CallExpr:
		return f.evalCall(e)
	}
	return value{}
}

func (f *fileScanner) evalCall(c *ast.CallExpr) value {
	switch calleeBase(c.Fun) {
	case "Join":
		parts := make([]string, 0, len(c.Args))
		known, rooted := true, false
		prefix := ""
		leading := true
		for _, a := range c.Args {
			v := f.eval(a)
			parts = append(parts, v.text)
			known = known && v.known
			rooted = rooted || v.rooted
			if leading && v.prefix != "" {
				prefix = path.Join(prefix, v.prefix)
				leading = v.known
				continue
			}
			leading = false
		}
		text := path.Join(parts...)
		return value{text: text, known: known, rooted: rooted || strings.Contains(text, rootMark), prefix: prefix}
	case "Dir", "Base", "Clean", "ToSlash", "FromSlash":
		if len(c.Args) != 1 {
			return value{}
		}
		v := f.eval(c.Args[0])
		switch calleeBase(c.Fun) {
		case "Dir":
			v.text = path.Dir(v.text)
		case "Base":
			v.text = path.Base(v.text)
		case "Clean":
			v.text = path.Clean(v.text)
		}
		return v
	}
	if isRootHelper(calleeBase(c.Fun)) {
		return value{text: rootMark, known: true, rooted: true}
	}
	v := value{}
	if tempDirCall(calleeBase(c.Fun)) {
		v.notRepo = true
		return v
	}
	for _, a := range c.Args {
		inner := f.eval(a)
		v.rooted = v.rooted || inner.rooted
		v.notRepo = v.notRepo || inner.notRepo
	}
	return v
}

// tempDirCall reports whether a call answers a scratch directory of the test's
// own: a path under one is not a repository path, and a walk of one is not a
// walk of the tree.
func tempDirCall(name string) bool {
	switch name {
	case "TempDir", "MkdirTemp", "CreateTemp", "UserHomeDir":
		return true
	}
	return false
}

func isRootHelper(name string) bool {
	return rootHelperNames[name] || strings.HasSuffix(name, "RepoRoot") || strings.HasSuffix(name, "ModuleRoot")
}

// calleeBase is a call's name with any package qualifier dropped: "filepath.Join"
// and "join" are both "Join".
func calleeBase(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.IndexExpr:
		return calleeBase(e.X)
	case *ast.ParenExpr:
		return calleeBase(e.X)
	}
	return ""
}

// literal records the repository paths a string literal names, if it names any.
// This is the wide net of the scan: it is what makes readRepoFile("a/b.go"),
// "../../docs/x.md" and a table of paths a reader is handed all land in the map,
// whatever call they sit in.
func (f *fileScanner) literal(lit *ast.BasicLit) {
	if lit.Kind != token.STRING || !f.test {
		return
	}
	text, err := strconv.Unquote(lit.Value)
	if err != nil {
		return
	}
	for _, p := range f.scan.resolve(f.dir, text) {
		f.out.reads.add(p)
		f.out.spread = true
	}
}

// call reads one call: the package it builds, the tree it walks, or the path it
// opens. A path the scan cannot read the text of, but which is built from a
// repository root, makes the package `any`: the test reaches a repository file
// that no literal names (R4).
func (f *fileScanner) call(c *ast.CallExpr) {
	name := callName(c.Fun)
	lower := strings.ToLower(calleeBase(c.Fun))
	switch {
	case isGoTool(c):
		f.goTool(c)
	case walkCalls[name]:
		f.walk(c)
	case globCalls[name], name == "exec.Command" || name == "exec.CommandContext" || name == "Command" || name == "CommandContext":
		f.readCall(c)
	case readNames[lower] || strings.Contains(lower, "read") || strings.Contains(lower, "open"):
		f.readCall(c)
	}
}

// callName is a call's name as the standard library is written in this
// repository: "filepath.WalkDir" qualified, "WalkDir" alone. A local helper that
// shares a name with one of these — deploy/terraform's walk over the embedded
// tree, called fifty times — is not the library's.
func callName(e ast.Expr) string {
	if sel, ok := e.(*ast.SelectorExpr); ok {
		if pkg, ok := sel.X.(*ast.Ident); ok {
			return pkg.Name + "." + sel.Sel.Name
		}
	}
	return calleeBase(e)
}

// walk reads a call that walks a tree. A walk of the repository has no finite
// file list, so the package is `any` with whatever ignores the walk states
// (F3, R4); a walk of a scratch directory of the test's own is nothing, because
// it reads no file of this repository.
func (f *fileScanner) walk(c *ast.CallExpr) {
	var root value
	if len(c.Args) > 0 {
		root = f.eval(c.Args[0])
	}
	reads := false
	switch {
	case root.known && !root.notRepo:
		// A walk of one directory the scan can name is a glob, not `any`: a
		// fixture directory has a finite list, and naming it says more than
		// "everything" does. The repository root itself is the exception.
		got := f.scan.resolve(f.dir, root.text)
		for _, p := range got {
			f.out.reads.add(p)
		}
		reads = len(got) > 0
		if f.scan.treeRoot(root.text, f.dir) {
			// A walk of the root reaches everything, so the files the file names
			// elsewhere are a part of what it reads and not a replacement for
			// it: `any` says the list is incomplete, and dropping what the file
			// does name would lose a read a docs test pins (a walk in one test
			// function must not unname the file another reads).
			reads, f.out.reads.any = true, true
		}
	case !root.known && !root.notRepo && root.rooted:
		reads, f.out.reads.any = true, true
	case !root.known && !root.notRepo && f.test:
		// A test runs in its package's directory, so a walk root the scan
		// cannot read may well be the repository: `any` is the safe reading.
		reads, f.out.reads.any = true, true
	}
	if !reads {
		return
	}
	f.out.spread = true
	for name := range f.walkIgnores(c) {
		f.out.reads.ignores[name] = true
	}
}

// namesChild reports whether a bare name — "testdata", a word with no separator
// in it — is a directory beside the file. A test walks or reads such a name
// often enough that dropping it would lose a fixture directory, and no other
// reading of a word is a repository path.
func (s *scanner) namesChild(dir, text string) bool {
	if strings.ContainsAny(text, "/\\ ") {
		return false
	}
	return s.dirs[path.Clean(path.Join(dir, text))]
}

// treeRoot reports whether a path text names the repository root rather than a
// directory in it: a walk of the root has no finite list at all, while a walk of
// one directory in it is a glob like any other literal directory.
func (s *scanner) treeRoot(text, dir string) bool {
	if text == "" {
		return false
	}
	if strings.Contains(text, rootMark) {
		// A root helper — repoRoot(), testutil.ModuleRoot() — stands for the
		// root itself, and only a path under it is not the root: the same test
		// that walks filepath.Join(repoRoot(), "testdata") walks repoRoot().
		return path.Clean(strings.TrimPrefix(strings.ReplaceAll(text, rootMark, ""), "/")) == "."
	}
	// A test runs in its package's directory, so "." there is the package and
	// only a text that climbs to the root — "../.." — is the whole repository.
	return path.Clean(path.Join(dir, text)) == "."
}

// readCall records the paths a read-shaped call is handed, and marks the package
// `any` when one of them is a repository path the scan cannot read.
func (f *fileScanner) readCall(c *ast.CallExpr) {
	for _, a := range c.Args {
		if _, ok := a.(*ast.FuncLit); ok {
			continue
		}
		v := f.eval(a)
		if !v.known {
			if v.rooted && !v.notRepo {
				f.out.reads.any = true
				f.out.spread = true
			} else if v.prefix != "" && !v.notRepo {
				// The name is built at run time, but it starts inside the
				// repository: the read reaches whatever the literal prefix names,
				// and nothing the scan can do says more than that.
				for _, p := range f.scan.resolve(f.dir, v.prefix) {
					f.out.reads.add(p)
					f.out.spread = true
				}
			}
			continue
		}
		for _, p := range f.scan.resolve(f.dir, v.text) {
			f.out.reads.add(p)
			f.out.spread = true
		}
	}
}

// isGoTool reports whether a call runs the go command, which is how a test
// builds a package that its own import graph has no edge to (F2).
func isGoTool(c *ast.CallExpr) bool {
	base := calleeBase(c.Fun)
	if base != "Command" && base != "CommandContext" {
		return false
	}
	for _, a := range c.Args {
		lit, ok := a.(*ast.BasicLit)
		if !ok {
			continue
		}
		if text, err := strconv.Unquote(lit.Value); err == nil && text == "go" {
			return true
		}
	}
	return false
}

// goTool records the closure of every package a `go build`, `go run`, `go test`
// or `go vet` names: the test reads that package's source and everything it
// imports, and no import edge of the test's own package leads there. A package
// argument that is not one literal is followed where the scan can — a name bound
// to a literal, or to the table a range walks — and leaves the package `any`
// where it cannot, rather than guessing.
func (f *fileScanner) goTool(c *ast.CallExpr) {
	args := c.Args
	if len(args) > 1 {
		args = args[1:] // the command word: "go"
	}
	skipNext := false
	for _, a := range args {
		lit, isLit := a.(*ast.BasicLit)
		text := ""
		if isLit && lit.Kind == token.STRING {
			text, _ = strconv.Unquote(lit.Value)
		}
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(text, "-") {
			if text == "-o" || text == "-C" {
				skipNext = true
			}
			continue
		}
		switch text {
		case "go", "build", "run", "test", "vet", "list", "generate", "install":
			continue
		}
		if text == "" {
			// A package named by something other than a literal: a name the scan
			// followed to one literal is that package, a name bound by a range
			// over a table of import paths names every one of them, and anything
			// else — a variable, a parameter, a name the scan lost the value of —
			// leaves the package `any`, since the go command builds whatever the
			// file can name (R4). A path the scan can see is a scratch directory
			// of the test's own names no package of this repository.
			v := f.eval(a)
			if v.known {
				f.build(v.text)
				continue
			}
			if len(v.alts) > 0 {
				for _, alt := range v.alts {
					f.build(alt)
				}
				continue
			}
			if !v.notRepo {
				f.out.reads.any = true
				f.out.spread = true
			}
			continue
		}
		f.build(text)
	}
}

// build records the closure of one package argument, or leaves the package `any`
// when the argument names every package under a pattern.
func (f *fileScanner) build(text string) {
	if strings.Contains(text, "...") {
		f.out.reads.any = true
		f.out.spread = true
		return
	}
	dir, ok := f.packageDir(text)
	if !ok {
		return
	}
	f.out.builds = append(f.out.builds, dir)
	f.out.spread = true
}

// packageDir resolves a package argument — an import path, or a path relative to
// the test's own directory — to the repository directory it names.
func (f *fileScanner) packageDir(text string) (string, bool) {
	if dir, ok := f.scan.dirOf(text); ok {
		return dir, true
	}
	if strings.HasPrefix(text, ".") {
		cand := path.Clean(path.Join(f.dir, text))
		if !strings.HasPrefix(cand, "..") && f.scan.dirs[cand] {
			return cand, true
		}
	}
	return "", false
}

// closure is a package's directory and every repository directory it imports,
// directly or transitively: what building it reads.
func (s *scanner) closure(dir string) []string {
	seen := map[string]bool{dir: true}
	queue := []string{dir}
	for len(queue) > 0 {
		next := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		p, ok := s.pkgs[next]
		if !ok {
			continue
		}
		for imp := range p.imports {
			if seen[imp] {
				continue
			}
			seen[imp] = true
			queue = append(queue, imp)
		}
	}
	return sorted(seen)
}

// walkCalls are the calls that walk a tree rather than open one path: a test
// that makes one of them over the repository has no finite list of files and the
// package is `any` (F3, R4). They are named with their package, so that a
// helper of this repository that happens to be called walk is not one of them.
var walkCalls = map[string]bool{
	"filepath.Walk": true, "filepath.WalkDir": true,
	"fs.WalkDir": true, "fs.WalkDirFS": true, "fs.ReadDir": true, "fs.ReadDirFS": true,
	"os.ReadDir": true, "os.ReadDirFS": true,
	"ioutil.ReadDir": true,
	"WalkDir":        true, "WalkDirFS": true, "ReadDir": true, "ReadDirFS": true,
}

// globCalls are the calls that match a pattern instead of naming a file: a
// pattern the scan can read is recorded as the glob it is, and one it cannot
// leaves the package `any`.
var globCalls = map[string]bool{"filepath.Glob": true, "fs.Glob": true, "Glob": true}

// readNames are the calls whose arguments this scan reads as paths, beyond the
// ones whose name alone says so ("read", "open").
var readNames = map[string]bool{
	"stat": true, "lstat": true, "readlink": true, "sub": true, "dirfs": true,
	"openroot": true, "load": true, "loadfile": true, "loaddoc": true, "fixture": true,
	"mustread": true, "readfixture": true, "readrepo": true, "readrepofile": true,
	"readdoc": true, "readtext": true, "readlines": true, "readstrings": true,
}

// walkIgnores is the name a walk states it steps out of: only the shape that
// really skips — a branch that returns fs.SkipDir, and the case of a switch that
// does. A condition that *selects* an entry (HasSuffix(name, ".go"), a name that
// is not in a list) says nothing about what the walk does not read, and reading
// it as an ignore would drop a file the package does read from its reach (R8);
// an ignore this scan cannot see only costs a run it did not need.
func (f *fileScanner) walkIgnores(c *ast.CallExpr) map[string]bool {
	out := map[string]bool{}
	for _, a := range c.Args {
		lit, ok := a.(*ast.FuncLit)
		if !ok {
			continue
		}
		ast.Inspect(lit.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.IfStmt:
				if stepsOut(n.Body.List) {
					f.ignoreLiterals(n.Cond, out)
				}
			case *ast.CaseClause:
				if stepsOut(n.Body) {
					for _, e := range n.List {
						f.ignoreLiterals(e, out)
					}
				}
			}
			return true
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stepsOut reports whether a block leaves the walk where it is. Only the
// explicit skip counts: a bare return is how a callback ends, not a statement
// about the entry it was handed.
func stepsOut(stmts []ast.Stmt) bool {
	found := false
	for _, st := range stmts {
		ast.Inspect(st, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && (id.Name == "SkipDir" || id.Name == "SkipAll") {
				found = true
			}
			return !found
		})
	}
	return found
}

func (f *fileScanner) ignoreLiterals(e ast.Expr, out map[string]bool) {
	ast.Inspect(e, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil || text == "" || text == "." || text == ".." {
			return true
		}
		out[strings.TrimPrefix(path.Clean(text), "./")] = true
		return true
	})
}

// resolve returns the repository paths a path text denotes: the text read from
// the file's own directory — a test runs in its package's directory — and from
// the repository root, which is how this repository's tests name repository
// paths. A path that does not exist, or that leaves the repository, is not one:
// "../../etc/passwd" is a test's input, not a file this repository holds.
func (s *scanner) resolve(dir, text string) []string {
	text = strings.TrimSpace(text)
	if text == "" || (!pathLike(text) && !s.namesChild(dir, text)) {
		return nil
	}
	var candidates []string
	if strings.Contains(text, rootMark) {
		candidates = append(candidates, path.Clean(strings.TrimPrefix(strings.ReplaceAll(text, rootMark, ""), "/")))
	} else {
		if strings.HasPrefix(text, "/") {
			return nil
		}
		candidates = append(candidates, path.Clean(path.Join(dir, text)), path.Clean(text))
	}
	var out []string
	seen := map[string]bool{}
	for _, cand := range candidates {
		if cand == "." || cand == ".." || strings.HasPrefix(cand, "../") || seen[cand] {
			continue
		}
		seen[cand] = true
		switch {
		case s.files[cand]:
			out = append(out, cand)
		case s.dirs[cand]:
			out = append(out, cand+"/**")
		case hasGlob(cand):
			if s.globMatches(cand) {
				out = append(out, cand)
			}
		}
	}
	sort.Strings(out)
	return out
}

// globMatches reports whether the checkout holds a path a pattern matches. The
// pattern is matched against the index and not against the disk, so a file the
// checkout ignores — and so a file no clone of this commit has — cannot make the
// map depend on the machine the scan ran on. "**" spans directories, which is
// what a test writing one means by it.
func (s *scanner) globMatches(pat string) bool {
	if got, ok := s.globCache[pat]; ok {
		return got
	}
	want := strings.Split(pat, "/")
	found := false
	for _, name := range s.byDepth[len(want)] {
		if matchSegments(want, name) {
			found = true
			break
		}
	}
	if !found && slices.Contains(want, "**") {
		// "**" spans directories, so such a pattern can match a path of any
		// depth and the depth bucket says nothing.
		for depth, names := range s.byDepth {
			if depth == len(want) {
				continue
			}
			for _, name := range names {
				if matchSegments(want, name) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	s.globCache[pat] = found
	return found
}

// matchSegments matches one split pattern against one split path, "**" taking
// any number of segments.
func matchSegments(want, name []string) bool {
	for len(want) > 0 {
		if want[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchSegments(want[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(want[0], name[0]); err != nil || !ok {
			return false
		}
		want, name = want[1:], name[1:]
	}
	return len(name) == 0
}

// pathLike reports whether a text is shaped like a path at all: a doc sentence
// that happens to hold a slash is not worth resolving, and a bare word is a
// name.
func pathLike(text string) bool {
	if strings.ContainsAny(text, "\n\t") || strings.Contains(text, "://") {
		return false
	}
	if strings.ContainsAny(text, "*?[") {
		return true
	}
	if strings.Contains(text, "/") {
		return true
	}
	if text == "." || text == ".." {
		return false
	}
	base := text
	if i := strings.LastIndex(base, "."); i > 0 {
		return i < len(base)-1
	}
	return false
}

func hasGlob(text string) bool {
	return strings.ContainsAny(text, "*?[")
}

// followShell adds the shell a package runs: a test that runs a script reads
// that script, and every library the script sources, however deep — F2's
// scripts/retry.sh reaches ./internal/cli through scripts/release-gate.sh,
// which is the only place it is named. A source line whose target the scan
// cannot read (a variable holding the directory) leaves the package `any`.
func (s *scanner) followShell(r *reads) {
	queue := make([]string, 0, len(r.files))
	for p := range r.files {
		if isShell(p) {
			queue = append(queue, p)
		}
	}
	sort.Strings(queue)
	seen := map[string]bool{}
	for len(queue) > 0 {
		script := queue[0]
		queue = queue[1:]
		if seen[script] {
			continue
		}
		seen[script] = true
		targets, unresolved := s.shellSources(script)
		if unresolved {
			r.any = true
		}
		for _, target := range targets {
			if r.files[target] {
				continue
			}
			r.files[target] = true
			queue = append(queue, target)
		}
	}
}

// shellSources reads the files a script sources, as its `source` and `.` lines
// name them: a repository path, or a name beside the script itself, which is how
// every library in scripts/ is reached.
func (s *scanner) shellSources(script string) ([]string, bool) {
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(script)))
	if err != nil {
		return nil, true
	}
	var out []string
	unresolved := false
	dir := path.Dir(script)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		rest := ""
		switch {
		case strings.HasPrefix(line, "source "):
			rest = strings.TrimPrefix(line, "source ")
		case strings.HasPrefix(line, ". "):
			rest = strings.TrimPrefix(line, ". ")
		default:
			continue
		}
		text := strings.ReplaceAll(strings.TrimSpace(rest), `"`, "")
		for _, beside := range []string{"$(dirname $0)", "${0%/*}"} {
			text = strings.ReplaceAll(text, beside, dir)
		}
		if strings.Contains(text, "$") {
			unresolved = true
			continue
		}
		target := ""
		for _, cand := range []string{path.Clean(path.Join(dir, text)), path.Clean(text)} {
			if target == "" && s.files[cand] {
				target = cand
			}
		}
		if target == "" {
			unresolved = true
			continue
		}
		out = append(out, target)
	}
	sort.Strings(out)
	return out, unresolved
}

func isShell(p string) bool {
	return strings.HasSuffix(p, ".sh") || strings.HasSuffix(p, ".bash")
}
