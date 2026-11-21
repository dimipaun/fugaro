package ciselect

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The committed read map is a fact about the tests of this repository, and it is
// the one input of the selector that nothing else can check: the map is
// generated, committed and then trusted, so a test that stops reading a file, a
// file that is renamed, a pin that is added — none of it fails anything unless
// something reads the tree again and compares. That is what this file does
// (CS5, spec item 5): it re-reads the checkout and fails when the map has
// stopped describing it, so a change that adds a path literal or a //go:embed
// carries the refreshed map in the same pull request.
//
// The pin is not theoretical: the map was already stale when it landed. The
// scan named 213 things the committed file was missing — 211 reads, one package
// it had no entry for at all, and one package whose `any` was gone — most of
// them around a read of internal/rigor/personas that reaches most of the tree,
// while no entry named a file the checkout had lost.
const (
	readMapFile = "../../docs/ci-affected-tests/read-map.json"
	readMapRoot = "../.."

	// readMapAdvice is the repair every failure here names: the map is
	// generated, so it is generated again, in the pull request that changed the
	// tree it describes.
	readMapAdvice = "run `go run ./scripts/ci-readmap` and commit the refreshed map"
)

func committedReadMap(t *testing.T) ReadMap {
	t.Helper()
	m, err := LoadReadMap(filepath.FromSlash(readMapFile))
	if err != nil {
		t.Fatalf("the committed read map does not load: %v", err)
	}
	return m
}

// repoIndex indexes the checkout the way the scan does — git ls-files, and the
// scanner's own walk where git cannot answer. The map is a fact about the commit
// and not about a working tree, so a check of it asks the same question the scan
// asks rather than reading files no clone of this commit holds.
func repoIndex(t *testing.T) *scanner {
	t.Helper()
	s := &scanner{
		root:      readMapRoot,
		files:     map[string]bool{},
		dirs:      map[string]bool{".": true},
		pkgs:      map[string]*pkgReads{},
		globCache: map[string]bool{},
	}
	if err := s.index(); err != nil {
		t.Fatalf("index %s: %v", readMapRoot, err)
	}
	return s
}

// entryOf is the map's entry for one package. A package the map does not name at
// all is a gap its caller reports, not a fatal: a map that lost a whole entry is
// the rot this file is about.
func entryOf(m ReadMap, pkg string) (PackageReads, bool) {
	for _, p := range m.Packages {
		if p.Package == pkg {
			return p, true
		}
	}
	return PackageReads{}, false
}

// reportReadMapGaps fails the test with what a check found, and names the one
// repair for all of them.
func reportReadMapGaps(t *testing.T, gaps []string, summary string) {
	t.Helper()
	if len(gaps) == 0 {
		return
	}
	t.Errorf("the committed read map no longer describes the tests it names: %s (%d of them); %s:\n\t%s",
		summary, len(gaps), readMapAdvice, strings.Join(gaps, "\n\t"))
}

// cloneReadMap is a copy a check can be exercised against without changing the
// map the next assertion reads.
func cloneReadMap(m ReadMap) ReadMap {
	out := ReadMap{Packages: make([]PackageReads, 0, len(m.Packages))}
	for _, p := range m.Packages {
		p.Reads = slices.Clone(p.Reads)
		p.Ignores = slices.Clone(p.Ignores)
		out.Packages = append(out.Packages, p)
	}
	return out
}

// dropRead removes one read from a package of the map, and reports whether it
// found one to drop.
func dropRead(m *ReadMap, pkg, read string) bool {
	for i, p := range m.Packages {
		if p.Package != pkg {
			continue
		}
		if j := slices.Index(p.Reads, read); j >= 0 {
			m.Packages[i].Reads = slices.Delete(p.Reads, j, j+1)
			return true
		}
	}
	return false
}

// scanGaps is what the committed map is missing of the scan: every read the scan
// names and the map does not, and every package the scan cannot name a finite
// list for and the map does not mark `any`. One direction only, and it is the
// safe one: a map that names more than the scan does selects a test it did not
// need to, while a map that names less misses a reach and lets a break through
// (R4, R8).
func scanGaps(scanned, committed ReadMap) []string {
	var gaps []string
	for _, p := range scanned.Packages {
		entry, ok := entryOf(committed, p.Package)
		if !ok {
			gaps = append(gaps, fmt.Sprintf("%s: the map has no entry for it, and the scan reads %d path(s) of the repository", p.Package, len(p.Reads)))
			continue
		}
		for _, read := range p.Reads {
			if !slices.Contains(entry.Reads, read) {
				gaps = append(gaps, fmt.Sprintf("%s: the scan reads %s and the map does not name it", p.Package, read))
			}
		}
		if p.Any && !entry.Any {
			gaps = append(gaps, fmt.Sprintf("%s: the scan reads a path it cannot name, and the map does not mark it any", p.Package))
		}
	}
	return gaps
}

func TestReadMapNamesEveryLiteralPath(t *testing.T) {
	scanned, err := Scan(readMapRoot)
	if err != nil {
		t.Fatalf("Scan(%s): %v", readMapRoot, err)
	}
	committed := committedReadMap(t)
	reportReadMapGaps(t, scanGaps(scanned, committed), "it is missing paths the scan reads")

	// The check above is worth nothing unless it can fail, so it is asked about
	// the red step of CS5: the committed map with one read the scan names
	// deleted.
	stale, ok := readDroppedFromScan(scanned, committed)
	if !ok {
		t.Fatalf("the check cannot be exercised: no read the scan names is in the committed map")
	}
	if len(scanGaps(scanned, stale)) == 0 {
		t.Errorf("a map missing a path the scan reads was not reported: the check cannot fail, and the map is free to rot")
	}
}

// readDroppedFromScan is the committed map with one read the scan names deleted,
// and reports whether it found one to drop.
func readDroppedFromScan(scanned, committed ReadMap) (ReadMap, bool) {
	for _, p := range scanned.Packages {
		for _, read := range p.Reads {
			m := cloneReadMap(committed)
			if dropRead(&m, p.Package, read) {
				return m, true
			}
		}
	}
	return ReadMap{}, false
}

// oneEmbed is one file a //go:embed directive reads: the Go file that holds the
// directive, the repository path the build reads into that file's package, and
// the package's map entry, which is where the read belongs. Read is empty for a
// pattern the checkout holds nothing for.
type oneEmbed struct {
	file string
	read string
	pkg  string
}

// embeddedReads is every file the checkout's //go:embed directives read. The
// directives are read out of the tree here rather than asked of the scan: a
// check that rests on the code it checks would pass while the scan stopped
// recording embeds, which is the rot F12 is about — an embedded file has no
// import edge to the package that carries it.
func embeddedReads(t *testing.T, s *scanner) []oneEmbed {
	t.Helper()
	var out []oneEmbed
	for _, rel := range s.goFiles() {
		dir := path.Dir(rel)
		for _, pattern := range embedPatterns(t, s.root, rel) {
			reads := s.resolve(dir, pattern)
			if len(reads) == 0 {
				out = append(out, oneEmbed{file: rel, pkg: "./" + dir})
				continue
			}
			for _, read := range reads {
				out = append(out, oneEmbed{file: rel, read: read, pkg: "./" + dir})
			}
		}
	}
	return out
}

// embedPatterns is the patterns one Go file's //go:embed directives name, with
// the all: prefix the build strips. A file that does not mention the directive
// is not parsed: this walks every Go file of the repository.
func embedPatterns(t *testing.T, root, rel string) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	if !bytes.Contains(src, []byte("//go:embed")) {
		return nil
	}
	f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	var out []string
	for _, group := range f.Comments {
		for _, c := range group.List {
			rest, ok := strings.CutPrefix(c.Text, "//go:embed")
			if !ok {
				continue
			}
			for _, pattern := range strings.Fields(rest) {
				out = append(out, strings.TrimPrefix(pattern, "all:"))
			}
		}
	}
	return out
}

func embedGaps(t *testing.T, committed ReadMap, embeds []oneEmbed) []string {
	t.Helper()
	var gaps []string
	for _, e := range embeds {
		if e.read == "" {
			gaps = append(gaps, fmt.Sprintf("%s: //go:embed names nothing the checkout holds", e.file))
			continue
		}
		entry, ok := entryOf(committed, e.pkg)
		if !ok || !slices.Contains(entry.Reads, e.read) {
			gaps = append(gaps, fmt.Sprintf("%s: //go:embed %s is not named by the map's %s entry", e.file, e.read, e.pkg))
		}
	}
	return gaps
}

func TestReadMapNamesEveryEmbed(t *testing.T) {
	index := repoIndex(t)
	committed := committedReadMap(t)
	embeds := embeddedReads(t, index)
	reportReadMapGaps(t, embedGaps(t, committed, embeds), "it does not name every embedded file")

	first := slices.IndexFunc(embeds, func(e oneEmbed) bool { return e.read != "" })
	if first < 0 {
		t.Fatalf("the check cannot be exercised: the checkout holds no //go:embed directive naming a file")
	}
	stale := cloneReadMap(committed)
	if !dropRead(&stale, embeds[first].pkg, embeds[first].read) {
		t.Fatalf("the committed map does not name %s (%s): the check cannot be exercised", embeds[first].read, embeds[first].file)
	}
	if len(embedGaps(t, stale, embeds)) == 0 {
		t.Errorf("a map that lost the embedded file %s was not reported: the check cannot fail, and the map is free to rot", embeds[first].read)
	}
}

// danglingReads is what the map names and the checkout does not hold: a file
// that was renamed, a directory that was removed, a pattern that matches
// nothing. It costs a run the selector did not need, and it is the way a map
// goes wrong on its own — a stale name stays in the file while the path it named
// is gone.
func danglingReads(s *scanner, committed ReadMap) []string {
	var gaps []string
	for _, p := range committed.Packages {
		for _, read := range p.Reads {
			if hasGlob(read) {
				if !s.globMatches(read) {
					gaps = append(gaps, fmt.Sprintf("%s: %s matches nothing the checkout holds", p.Package, read))
				}
				continue
			}
			if !s.files[read] && !s.dirs[read] {
				gaps = append(gaps, fmt.Sprintf("%s: %s is not in the checkout", p.Package, read))
			}
		}
	}
	return gaps
}

func TestReadMapNamesNoMissingFile(t *testing.T) {
	index := repoIndex(t)
	committed := committedReadMap(t)
	reportReadMapGaps(t, danglingReads(index, committed), "it names paths the checkout does not hold")

	if len(committed.Packages) == 0 {
		t.Fatalf("the committed map has no packages")
	}
	gone := cloneReadMap(committed)
	gone.Packages[0].Reads = append(gone.Packages[0].Reads, "docs/ci-affected-tests/no-such-file.md")
	if len(danglingReads(index, gone)) == 0 {
		t.Errorf("an entry naming a file that does not exist was not reported: the check cannot fail, and a renamed file stays in the map forever")
	}
}

// docsPinPattern is how the Docs pins reach one another, and so how a change to
// one of them reaches the tests that read the documents: the pins glob each
// other by it (filepath.Glob("../../internal/cli/docs_*_test.go")), and the map
// records that glob as a read of ./internal/cli (spec item 5).
const docsPinPattern = "internal/cli/docs_*_test.go"

// isDocsPin is the wider net the check below walks: every test file of
// internal/cli named after a document, whatever the map's pattern happens to be,
// so that a pin the pattern does not cover is found rather than assumed.
func isDocsPin(rel string) bool {
	return path.Dir(rel) == "internal/cli" &&
		strings.HasPrefix(path.Base(rel), "docs") && strings.HasSuffix(rel, "_test.go")
}

func TestReadMapReachesEveryDocsPin(t *testing.T) {
	index := repoIndex(t)
	committed := committedReadMap(t)

	var pins []string
	for rel := range index.files {
		if isDocsPin(rel) {
			pins = append(pins, rel)
		}
	}
	slices.Sort(pins)
	if len(pins) == 0 {
		t.Fatalf("the checkout holds no Docs pin under internal/cli: the check would pass without reading one")
	}

	pattern := strings.Split(docsPinPattern, "/")
	pinGaps := func(m ReadMap) []string {
		var gaps []string
		entry, ok := entryOf(m, "./internal/cli")
		if !ok {
			return []string{"the map has no entry for ./internal/cli, the package that reads the documents"}
		}
		if !slices.Contains(entry.Reads, docsPinPattern) {
			gaps = append(gaps, fmt.Sprintf("the entry does not name %s, so a change to a Docs pin reaches no test", docsPinPattern))
		}
		for _, pin := range pins {
			if !matchSegments(pattern, strings.Split(pin, "/")) {
				gaps = append(gaps, fmt.Sprintf("%s is not reached by %s: a Docs pin the pattern does not name is read by none of the others", pin, docsPinPattern))
			}
		}
		return gaps
	}

	reportReadMapGaps(t, pinGaps(committed), "it does not reach every Docs pin")

	stale := cloneReadMap(committed)
	if !dropRead(&stale, "./internal/cli", docsPinPattern) {
		t.Fatalf("./internal/cli does not name %s: the check cannot be exercised", docsPinPattern)
	}
	if len(pinGaps(stale)) == 0 {
		t.Errorf("the entry losing %s was not reported: the check cannot fail, and the docs pins are free to fall out of the map", docsPinPattern)
	}
}
