package ciselect

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// goMod is the module line every fixture needs: the scan resolves an import
// path to a directory through it.
const goMod = "module example.com/m\n\ngo 1.24\n"

// tree writes a fixture checkout — the file names with their bodies, the
// directories made as it goes — and returns its root. Every test scans a tree of
// its own, so none of them depends on this repository's files or on the working
// directory a test happens to run in.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func scanTree(t *testing.T, files map[string]string) ReadMap {
	t.Helper()
	m, err := Scan(tree(t, files))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return m
}

// entry is one package's line in the map, and fails the test when the map has
// none: a package that reads nothing is not in the map at all, so a missing
// entry is a finding about the scan.
func entry(t *testing.T, m ReadMap, pkg string) PackageReads {
	t.Helper()
	for _, p := range m.Packages {
		if p.Package == pkg {
			return p
		}
	}
	t.Fatalf("the read map has no entry for %s: %+v", pkg, m.Packages)
	return PackageReads{}
}

func wantReads(t *testing.T, p PackageReads, want ...string) {
	t.Helper()
	for _, w := range want {
		if !slices.Contains(p.Reads, w) {
			t.Errorf("%s reads %v, want %s among them", p.Package, p.Reads, w)
		}
	}
}

func wantNoReads(t *testing.T, p PackageReads, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if slices.Contains(p.Reads, u) {
			t.Errorf("%s reads %s, want it not to: %v", p.Package, u, p.Reads)
		}
	}
}

func TestScanFindsLiteralRepoPaths(t *testing.T) {
	m := scanTree(t, map[string]string{
		"go.mod":                goMod,
		"docs/x.md":             "x\n",
		"docs/how-to/a.md":      "a\n",
		"internal/a/b.go":       "package a\n",
		"internal/a/testdata/y": "y\n",
		"internal/a/a_test.go": `package a

import (
	"os"
	"path/filepath"
)

func TestReads(t *testing.T) {
	_ = readRepoFile("../../docs/x.md")
	_, _ = os.ReadFile(filepath.Join(repoRoot(), "internal", "a", "b.go"))
	_, _ = filepath.Glob("../../docs/how-to/*.md")
	_, _ = os.ReadFile("testdata/" + os.Getenv("FUGARO_FIXTURE"))
}

func readRepoFile(name string) []byte { return nil }

func repoRoot() string { return "../.." }
`,
	})

	p := entry(t, m, "./internal/a")
	wantReads(t, p,
		"docs/x.md",              // a literal climbing out of the package
		"internal/a/b.go",        // filepath.Join of the repository root
		"docs/how-to/*.md",       // a literal pattern, kept as written
		"internal/a/testdata/**", // the literal prefix of a name built at run time
	)
	if p.Any {
		t.Errorf("./internal/a is any, want a finite list: %v", p.Reads)
	}
}

func TestScanMarksADynamicPathAny(t *testing.T) {
	m := scanTree(t, map[string]string{
		"go.mod":    goMod,
		"docs/x.md": "x\n",
		"internal/dynamic/dynamic_test.go": `package dynamic

import (
	"os"
	"path/filepath"
)

func TestDynamic(t *testing.T) {
	name := os.Getenv("FUGARO_FILE")
	_, _ = os.ReadFile(filepath.Join(repoRoot(), name))
}

func repoRoot() string { return "../.." }
`,
		"internal/finite/finite_test.go": `package finite

import "os"

func TestFinite(t *testing.T) {
	_, _ = os.ReadFile("../../docs/x.md")
}
`,
	})

	d := entry(t, m, "./internal/dynamic")
	if !d.Any {
		t.Errorf("./internal/dynamic is not any, want any: %v", d.Reads)
	}
	// `any` is per package: the one that names its file is still a list, or the
	// map would save nothing (F1).
	f := entry(t, m, "./internal/finite")
	if f.Any {
		t.Errorf("./internal/finite is any, want a finite list: %v", f.Reads)
	}
	wantReads(t, f, "docs/x.md")
}

func TestScanMarksEveryWalkerAny(t *testing.T) {
	m := scanTree(t, map[string]string{
		"go.mod": goMod,
		"internal/w1/w1_test.go": `package w1

import (
	"io/fs"
	"path/filepath"
)

func TestWalk(t *testing.T) {
	_ = filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error { return nil })
}
`,
		"internal/w2/w2_test.go": `package w2

import "os"

func TestReadDir(t *testing.T) {
	root := os.Getenv("FUGARO_ROOT")
	_, _ = os.ReadDir(root)
}
`,
		"internal/w3/w3_test.go": `package w3

import (
	"io/fs"
	"os"
)

func TestWalkFS(t *testing.T) {
	_ = fs.WalkDir(os.DirFS("../.."), ".", func(p string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata":
				return fs.SkipDir
			}
		}
		return nil
	})
}
`,
		"internal/w4/testdata/one.txt": "one\n",
		"internal/w4/w4_test.go": `package w4

import (
	"io/fs"
	"path/filepath"
)

func TestWalkFixture(t *testing.T) {
	_ = filepath.WalkDir("testdata", func(p string, d fs.DirEntry, err error) error { return nil })
}
`,
	})

	// Every walker, not the three F3 happens to name: one that walks the
	// repository, one that walks a root it cannot read, one that walks through
	// an fs.FS of the repository.
	for _, pkg := range []string{"./internal/w1", "./internal/w2", "./internal/w3"} {
		if p := entry(t, m, pkg); !p.Any {
			t.Errorf("%s is not any, want any: %v", pkg, p.Reads)
		}
	}
	// The ignores the walk states, and only those: they narrow what the any
	// covers (R4).
	w3 := entry(t, m, "./internal/w3")
	for _, want := range []string{".git", "node_modules", "testdata"} {
		if !slices.Contains(w3.Ignores, want) {
			t.Errorf("./internal/w3 ignores %v, want %s among them", w3.Ignores, want)
		}
	}
	// A walk of one directory the scan can name is a list, not `any`.
	w4 := entry(t, m, "./internal/w4")
	if w4.Any {
		t.Errorf("./internal/w4 is any, want a finite list: %v", w4.Reads)
	}
	wantReads(t, w4, "internal/w4/testdata/**")
}

func TestScanFollowsABuildByImportPath(t *testing.T) {
	m := scanTree(t, map[string]string{
		"go.mod": goMod,
		"internal/build/gen/main.go": `package main

import "example.com/m/internal/build/gen/dep"

func main() { dep.Do() }
`,
		"internal/build/gen/dep/dep.go": "package dep\n\nfunc Do() {}\n",
		"internal/consumer/x_test.go": `package consumer

import (
	"os/exec"
	"path/filepath"
)

func TestBuild(t *testing.T) {
	cmd := exec.Command("go", "run", "example.com/m/internal/build/gen")
	cmd.Dir = filepath.Join(repoRoot(), "internal", "build", "gen")
	_ = cmd.Run()
}

func repoRoot() string { return "../.." }
`,
	})

	// A test that builds a package by import path reads that package and
	// everything it imports, which no import edge of the test's own package
	// leads to (F2). The scan of path literals alone cannot see it: the import
	// path names no file.
	p := entry(t, m, "./internal/consumer")
	wantReads(t, p, "internal/build/gen/**", "internal/build/gen/dep/**")
	if p.Any {
		t.Errorf("./internal/consumer is any, want the closure of the built package: %v", p.Reads)
	}
}

func TestScanAttributesAHelpersReadsToItsImporters(t *testing.T) {
	const (
		fixtureRead = "internal/testutil/testdata/fixture-repo/a.txt"
		testutil    = `package testutil

import (
	"os"
	"path/filepath"
)

func Fixture(name string) []byte {
	data, err := os.ReadFile(filepath.Join("testdata", "fixture-repo", "a.txt"))
	if err != nil {
		panic(err)
	}
	return data
}
`
	)
	m := scanTree(t, map[string]string{
		"go.mod":                       goMod,
		"internal/testutil/fixture.go": testutil,
		"internal/testutil/testdata/fixture-repo/a.txt": "a\n",
		"internal/one/one_test.go": `package one

import (
	"testing"

	"example.com/m/internal/testutil"
)

func TestOne(t *testing.T) { _ = testutil.Fixture("a") }
`,
		"internal/two/two_test.go": `package two

import (
	"testing"

	"example.com/m/internal/testutil"
)

func TestTwo(t *testing.T) { _ = testutil.Fixture("a") }
`,
		"docs/z.md": "z\n",
		"internal/three/three_test.go": `package three

import (
	"os"
	"testing"
)

func TestThree(t *testing.T) {
	_, _ = os.ReadFile("../../docs/z.md")
}
`,
	})

	// A helper's reads belong to every test package that imports it, directly or
	// through another package (fixture.go reaches internal/runner that way).
	for _, pkg := range []string{"./internal/one", "./internal/two"} {
		wantReads(t, entry(t, m, pkg), fixtureRead)
	}
	// And to no other: a package that does not import the helper pays nothing
	// for it, and a directory with no test file of its own is not a package the
	// selector runs at all.
	three := entry(t, m, "./internal/three")
	wantReads(t, three, "docs/z.md")
	wantNoReads(t, three, fixtureRead)
	for _, p := range m.Packages {
		if p.Package == "./internal/testutil" {
			t.Errorf("the map has an entry for internal/testutil, a directory with no test file: %+v", p)
		}
	}
}

func TestScanFollowsASourcedShellLibrary(t *testing.T) {
	m := scanTree(t, map[string]string{
		"go.mod":                    goMod,
		"scripts/retry.sh":          "#!/usr/bin/env bash\nretry() { :; }\n",
		"scripts/release-gate.sh":   "#!/usr/bin/env bash\nset -eu\n. \"$(dirname \"$0\")/retry.sh\"\n",
		"scripts/versions.sh":       "#!/usr/bin/env bash\nversion() { :; }\n",
		"scripts/hotfix-commits.sh": "#!/usr/bin/env bash\ncommits() { :; }\n",
		"scripts/release.sh":        "#!/usr/bin/env bash\nsource \"$(dirname \"$0\")/versions.sh\"\nsource \"$(dirname \"$0\")/hotfix-commits.sh\"\n",
		"internal/gate/gate_test.go": `package gate

import (
	"os/exec"
	"path/filepath"
	"testing"
)

const gate = "release-gate.sh"

const release = "release.sh"

func TestRunsTheScripts(t *testing.T) {
	_ = exec.Command("bash", filepath.Join(repoRoot(), "scripts", gate)).Run()
	_ = exec.Command("bash", filepath.Join(repoRoot(), "scripts", release)).Run()
}

func repoRoot() string { return "../.." }
`,
	})

	// A test that runs a script reads the script and what the script sources:
	// the change that matters may be one directory away from the one the test
	// names (release-gate.sh sources retry.sh).
	p := entry(t, m, "./internal/gate")
	wantReads(t, p,
		"scripts/release-gate.sh", "scripts/retry.sh",
		"scripts/release.sh", "scripts/versions.sh", "scripts/hotfix-commits.sh",
	)
}

func TestScanIsDeterministic(t *testing.T) {
	files := map[string]string{
		"go.mod":                goMod,
		"docs/x.md":             "x\n",
		"internal/z/z_test.go":  "package z\n\nimport \"os\"\n\nfunc TestZ(t *testing.T) { _, _ = os.ReadFile(\"../../docs/x.md\") }\n",
		"internal/a/a_test.go":  "package a\n\nimport \"os\"\n\nfunc TestA(t *testing.T) { _, _ = os.ReadFile(\"../../docs/x.md\") }\n",
		"internal/m/testdata/y": "y\n",
		"internal/m/m_test.go":  "package m\n\nimport \"os\"\n\nfunc TestM(t *testing.T) { _, _ = os.ReadDir(\"../..\") }\n",
	}
	root := tree(t, files)
	first, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	second, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	a, err := MarshalReadMap(first)
	if err != nil {
		t.Fatalf("MarshalReadMap: %v", err)
	}
	b, err := MarshalReadMap(second)
	if err != nil {
		t.Fatalf("MarshalReadMap: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("two scans of one tree differ:\n%s\n%s", a, b)
	}

	var names []string
	for _, p := range first.Packages {
		names = append(names, p.Package)
		if !slices.IsSorted(p.Reads) {
			t.Errorf("%s reads are not sorted: %v", p.Package, p.Reads)
		}
		if !slices.IsSorted(p.Ignores) {
			t.Errorf("%s ignores are not sorted: %v", p.Package, p.Ignores)
		}
	}
	if !slices.IsSorted(names) {
		t.Errorf("the packages are not sorted: %v", names)
	}

	// The committed file is read back by the selector, so what it writes must
	// load as it stands.
	file := filepath.Join(t.TempDir(), "read-map.json")
	if err := os.WriteFile(file, a, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadReadMap(file)
	if err != nil {
		t.Fatalf("LoadReadMap: %v", err)
	}
	again, err := MarshalReadMap(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, again) {
		t.Errorf("the map does not survive a round trip:\n%s\n%s", a, again)
	}
}

func TestLoadReadMapRefusesAnUnknownField(t *testing.T) {
	file := filepath.Join(t.TempDir(), "read-map.json")
	body := `{"packages":[{"package":"./internal/cli","walks":true}]}`
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReadMap(file); err == nil {
		t.Fatal("a map with a field this build does not know loaded, want a refusal")
	} else if !strings.Contains(err.Error(), "walks") {
		t.Errorf("the refusal does not name the field: %v", err)
	}
}
