// Package ciselect chooses the CI legs a change can reach.
package ciselect

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// BuildTags is the union of the build tags CI compiles under, and the tag set
// the graph has to be listed with. The terraform and rules jobs reach packages
// the untagged build never compiles, the live shards and the docker image tests
// their own; a graph missing one of them misses a package a change reaches,
// which is the one direction this selector may not fail in.
const BuildTags = "terraform,firebase,docker,live"

// Graph is the reverse import graph of one module: the packages that import
// each package, and the package that owns each file.
type Graph struct {
	importers map[string]map[string]bool
	owner     map[string]string
}

// ParseGraph reads the stream of JSON objects `go list -deps -test -json -tags
// BuildTags ./...` prints and returns the graph over it.
//
// Both halves of the listing matter. -deps so that every package a package
// imports is an edge, and -test so that a test file's imports are too: a package
// may reach the changed one from its test code alone. An object that is not a
// package of the main module (the standard library, a dependency, the test
// binary go list synthesises) is skipped, because no file of the repository
// belongs to it.
func ParseGraph(r io.Reader) (*Graph, error) {
	g := &Graph{
		importers: map[string]map[string]bool{},
		owner:     map[string]string{},
	}
	dec := json.NewDecoder(r)
	root := ""
	for {
		var lp listedPkg
		err := dec.Decode(&lp)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read go list output: %w", err)
		}
		if lp.Module == nil || !lp.Module.Main {
			continue
		}
		if root == "" {
			root = lp.Module.Dir
		}
		if lp.Name == "main" && strings.HasSuffix(lp.ImportPath, ".test") {
			continue
		}
		// A test variant (`pkg [pkg.test]`, `pkg_test [pkg.test]`) names no
		// directory of its own: its imports and files belong to the package
		// under test, which is the package ForTest names.
		pkg := lp.ForTest
		if pkg == "" {
			pkg = lp.ImportPath
		}
		for _, imp := range slices.Concat(lp.Imports, lp.TestImports, lp.XTestImports) {
			if imp = canonical(imp); imp == pkg {
				continue
			}
			if g.importers[imp] == nil {
				g.importers[imp] = map[string]bool{}
			}
			g.importers[imp][pkg] = true
		}
		dir, err := filepath.Rel(root, lp.Dir)
		if err != nil || dir == ".." || strings.HasPrefix(dir, ".."+string(filepath.Separator)) {
			continue
		}
		dir = filepath.ToSlash(dir)
		// IgnoredGoFiles are the package's files the tag set does not compile:
		// the one behind -tags ee or firestore, and the other platform's
		// openfile_other.go. They are still files of this package, and a change
		// to one has to reach it.
		for _, name := range slices.Concat(lp.GoFiles, lp.CgoFiles, lp.IgnoredGoFiles,
			lp.TestGoFiles, lp.XTestGoFiles, lp.EmbedFiles, lp.TestEmbedFiles) {
			if filepath.IsAbs(name) {
				continue
			}
			g.owner[path.Join(dir, filepath.ToSlash(name))] = pkg
		}
	}
	if root == "" {
		return nil, errors.New("go list reported no package of this module")
	}
	return g, nil
}

// Affected returns the import paths of the packages a change to one of
// changedFiles reaches: the package owning a changed file, and every package
// that imports one of those, directly or transitively, test imports included.
// The result is sorted and holds packages of the module only.
//
// A file no package owns — a document, a testdata fixture, go.mod — reaches
// nothing, which the caller reads as a doubt and answers with a full run rather
// than with an empty selection.
func (g *Graph) Affected(changedFiles []string) []string {
	seen := map[string]bool{}
	var queue []string
	for _, f := range changedFiles {
		pkg, ok := g.owner[path.Clean(f)]
		if !ok || seen[pkg] {
			continue
		}
		seen[pkg] = true
		queue = append(queue, pkg)
	}
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		for importer := range g.importers[pkg] {
			if seen[importer] {
				continue
			}
			seen[importer] = true
			queue = append(queue, importer)
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// listedPkg is the part of one `go list -json` object the graph reads.
type listedPkg struct {
	ImportPath     string
	ForTest        string
	Name           string
	Dir            string
	Module         *listedModule
	Imports        []string
	TestImports    []string
	XTestImports   []string
	GoFiles        []string
	CgoFiles       []string
	IgnoredGoFiles []string
	TestGoFiles    []string
	XTestGoFiles   []string
	EmbedFiles     []string
	TestEmbedFiles []string
}

type listedModule struct {
	Dir  string
	Main bool
}

// canonical drops the ` [pkg.test]` suffix go list puts on a package copied into
// a test binary, which is the same package as far as the graph is concerned.
func canonical(importPath string) string {
	if i := strings.Index(importPath, " ["); i >= 0 {
		return importPath[:i]
	}
	return importPath
}
