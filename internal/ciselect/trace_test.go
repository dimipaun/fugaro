package ciselect

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// openLine is one line of the log `strace -f -y -e trace=openat,open,execve`
// writes for a successful open: the call with the path the process passed, and
// the decoration -y adds, which is the path it reached.
func openLine(pid int, passed, reached string) string {
	return strconv.Itoa(pid) + "  openat(AT_FDCWD, " + strconv.Quote(passed) + ", O_RDONLY|O_CLOEXEC) = 3<" + reached + ">"
}

// execLine is one line of the log for a process that becomes a program: strace
// prints the path it execs and the arguments it takes, and the pid the line
// carries is the process the opens after it belong to.
func execLine(pid int, prog string, argv ...string) string {
	quoted := make([]string, 0, len(argv))
	for _, a := range argv {
		quoted = append(quoted, strconv.Quote(a))
	}
	return strconv.Itoa(pid) + "  execve(" + strconv.Quote(prog) + ", [" + strings.Join(quoted, ", ") + "], 0x7ffd) = 0"
}

// reduce is ReduceOpens with no go root named: the logs below carry no process
// of a toolchain's own. The one that does is
// TestTraceIgnoresTheBuildTheGoToolRuns.
func reduce(raw, root string, drop ...string) []string {
	return ReduceOpens(raw, root, "", drop...)
}

// artifactDir writes the artifacts of a run the way `gh run download` lays them
// out — one directory per artifact — and returns the directory a merge is given.
func artifactDir(t *testing.T, arts ...TraceArtifact) string {
	t.Helper()
	dir := t.TempDir()
	for _, a := range arts {
		data, err := MarshalTraceArtifact(a)
		if err != nil {
			t.Fatalf("MarshalTraceArtifact: %v", err)
		}
		sub := filepath.Join(dir, "reads-"+a.Shard)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "reads-"+a.Shard+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestTraceReducesOpensToRepoPaths(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod":        goMod,
		"docs/x.md":     "x\n",
		"docs/sub/y.md": "y\n",
		".git/HEAD":     "ref: refs/heads/main\n",
	})
	abs := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	raw := strings.Join([]string{
		// A test binary and a child it spawned: the same file opened twice, which
		// the map names once.
		openLine(1234, abs("docs/x.md"), abs("docs/x.md")),
		"[pid  1235]  openat(AT_FDCWD, " + strconv.Quote(abs("docs/x.md")) + ", O_RDONLY) = 4<" + abs("docs/x.md") + ">",
		// A relative argument, which only -y's decoration places: the test read
		// "../../docs/x.md" from its own package's directory.
		openLine(1235, "../../docs/x.md", abs("docs/x.md")),
		// A directory: the map writes a read of one as "dir/**".
		openLine(1235, abs("docs/sub"), abs("docs/sub")),
		// Outside the repository: the loader, the toolchain's caches, the
		// runner's own files.
		openLine(1234, "/etc/ld.so.cache", "/etc/ld.so.cache"),
		openLine(1234, "/home/runner/.cache/go-build/ab/cdef-d", "/home/runner/.cache/go-build/ab/cdef-d"),
		// Inside the repository and held by no commit: an open that failed, and
		// the .git files a test's own git command reads.
		"1234  openat(AT_FDCWD, " + strconv.Quote(abs("docs/missing.md")) + ", O_RDONLY) = -1 ENOENT (No such file or directory)",
		openLine(1234, abs(".git/HEAD"), abs(".git/HEAD")),
		// A relative argument with no decoration: the log does not say which
		// directory the process was in, so nothing places the path.
		"1234  openat(AT_FDCWD, \"docs/x.md\", O_RDONLY) = -1 ENOENT (No such file or directory)",
		// Lines that are not opens at all, which a -f log is full of.
		"1234  read(3, \"x\", 1) = 1",
		"1234  execve(\"/tmp/go-build123/b001/cli.test\", [\"cli.test\"], 0x7ffd) = 0",
	}, "\n")

	got := reduce(raw, root)
	want := []string{"docs/sub/**", "docs/x.md"}
	if !slices.Equal(got, want) {
		t.Errorf("ReduceOpens = %v, want %v", got, want)
	}
}

func TestTraceIgnoresTheGoBuildCache(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod":             goMod,
		"docs/x.md":          "x\n",
		".gocache/ab/cdef-d": "blob\n",
	})
	// A shard that puts the toolchain's cache inside the workspace: the trace of
	// the build a test runs reads it, and none of it is a file a change reaches.
	cache := filepath.Join(root, ".gocache")
	blob := filepath.Join(cache, "ab", "cdef-d")
	raw := strings.Join([]string{
		openLine(1234, blob, blob),
		openLine(1234, filepath.Join(root, "docs", "x.md"), filepath.Join(root, "docs", "x.md")),
	}, "\n")

	got := reduce(raw, root, cache)
	if !slices.Equal(got, []string{"docs/x.md"}) {
		t.Errorf("ReduceOpens = %v, want only docs/x.md: the build cache is not a file of the checkout", got)
	}
}

// The build is not the test. A shard's `go test` compiles, vets and links every
// package it runs, and each of those opens the package's sources and the
// directory it sits in; a test that runs a build of its own does the same, one
// level down. None of it is a file a test read, and taking it for one would name
// every package of the leg as a read of every package it ran — and report each
// as a scan gap (R8, and the CS2b acceptance: the test process and its children,
// not the toolchain's).
func TestTraceIgnoresTheBuildTheGoToolRuns(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod":          goMod,
		"docs/x.md":       "x\n",
		"docs/y.md":       "y\n",
		"internal/x/x.go": "package x\n",
	})
	abs := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	goroot := filepath.Join(t.TempDir(), "go")
	tool := func(name string) string { return filepath.Join(goroot, "pkg", "tool", "linux_amd64", name) }
	testbin := filepath.Join(t.TempDir(), "x.test")
	raw := strings.Join([]string{
		// The shard's own go test, which reads the module file as it builds.
		execLine(1234, filepath.Join(goroot, "bin", "go"), "go", "test", "./internal/x"),
		openLine(1234, abs("go.mod"), abs("go.mod")),
		// The compiler it runs: one source file, and the package's directory.
		execLine(1235, tool("compile"), "compile", "-p", "internal/x"),
		openLine(1235, abs("internal/x/x.go"), abs("internal/x/x.go")),
		openLine(1235, abs("internal/x"), abs("internal/x")),
		// The linker, on the same package.
		execLine(1236, tool("link"), "link", "-o", testbin),
		openLine(1236, abs("internal/x/x.go"), abs("internal/x/x.go")),
		// An exec that failed leaves the process in the program it was already
		// running: this pid is still the go command, and this open is still the
		// build's.
		"1234  execve(\"/nonexistent/bin/go\", [\"go\"], 0x7ffd) = -1 ENOENT (No such file or directory)",
		openLine(1234, abs("internal/x/x.go"), abs("internal/x/x.go")),
		// The test binary the shard built: a child of the build, and the process
		// whose reads the trace is for.
		execLine(1240, testbin, "x.test", "-test.run=."),
		openLine(1240, abs("docs/x.md"), abs("docs/x.md")),
		// A process the log never shows exec'ing — a fork of a test — is not the
		// toolchain's either.
		openLine(1250, abs("docs/y.md"), abs("docs/y.md")),
	}, "\n")

	got := ReduceOpens(raw, root, goroot)
	want := []string{"docs/x.md", "docs/y.md"}
	if !slices.Equal(got, want) {
		t.Errorf("ReduceOpens = %v, want %v: what the go toolchain opened is the build, not the tests", got, want)
	}
}

func TestTraceReportsAScanGap(t *testing.T) {
	m := ReadMap{Packages: []PackageReads{
		{Package: "./internal/a", Reads: []string{"docs/**"}},
		{Package: "./internal/b", Reads: []string{"docs/pinned.md"}},
		{Package: "./internal/c", Reads: []string{"internal/c/x.txt"}, Any: true},
	}}
	dir := artifactDir(t, TraceArtifact{
		Shard:    "rest-1",
		Packages: []string{"./internal/a", "./internal/b", "./internal/c", "./internal/d"},
		Reads:    []string{"docs/pinned.md", "docs/x.md"},
	})

	merged, gaps, err := FromArtifact(m, dir)
	if err != nil {
		t.Fatalf("FromArtifact: %v", err)
	}
	var got []string
	for _, g := range gaps {
		got = append(got, g.String())
	}
	// ./internal/a's "docs/**" already names both paths, ./internal/c is `any`
	// and so reaches a path it does not name (R4), and ./internal/d is a package
	// the scan left out entirely: the trace is its only record.
	want := []string{
		"./internal/b: docs/x.md",
		"./internal/d: docs/pinned.md",
		"./internal/d: docs/x.md",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the scan gaps are %v, want %v", got, want)
	}
	wantReads(t, entry(t, merged, "./internal/b"), "docs/pinned.md", "docs/x.md")
	if p := entry(t, merged, "./internal/c"); !p.Any || len(p.Reads) != 3 {
		t.Errorf("./internal/c is %+v, want it any with the traced reads merged", p)
	}
}

func TestFromArtifactMergesIntoTheCommittedMap(t *testing.T) {
	scanned := scanTree(t, map[string]string{
		"go.mod":                               goMod,
		"docs/x.md":                            "x\n",
		"docs/pinned.md":                       "pinned\n",
		"internal/a/testdata/fixture.txt":      "a\n",
		"internal/testdata/fixture-repo/a.txt": "fixture\n",
		"internal/a/a_test.go":                 "package a\n\nimport \"os\"\n\nfunc TestA(t *testing.T) { _, _ = os.ReadFile(\"../../docs/x.md\") }\n",
		"internal/b/b_test.go":                 "package b\n\nimport \"os\"\n\nfunc TestB(t *testing.T) { _, _ = os.ReadFile(\"../../docs/pinned.md\") }\n",
		"internal/c/c_test.go":                 "package c\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) {}\n",
	})
	file := filepath.Join(t.TempDir(), "read-map.json")
	data, err := MarshalReadMap(scanned)
	if err != nil {
		t.Fatalf("MarshalReadMap: %v", err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	committed, err := LoadReadMap(file)
	if err != nil {
		t.Fatalf("LoadReadMap: %v", err)
	}
	dir := artifactDir(t, TraceArtifact{
		Shard:    "cli-3",
		Packages: []string{"./internal/a"},
		Reads:    []string{"docs/x.md", "internal/a/testdata/fixture.txt"},
	}, TraceArtifact{
		Shard:    "rest-2",
		Packages: []string{"./internal/b", "./internal/c"},
		Reads:    []string{"internal/testdata/fixture-repo/a.txt", "docs/x.md"},
	})

	merged, _, err := FromArtifact(committed, dir)
	if err != nil {
		t.Fatalf("FromArtifact: %v", err)
	}
	mergedData, err := MarshalReadMap(merged)
	if err != nil {
		t.Fatalf("MarshalReadMap: %v", err)
	}
	a := entry(t, merged, "./internal/a")
	wantReads(t, a, "docs/x.md", "internal/a/testdata/fixture.txt")
	if !slices.IsSorted(a.Reads) {
		t.Errorf("./internal/a reads are not sorted: %v", a.Reads)
	}
	wantReads(t, entry(t, merged, "./internal/b"), "docs/pinned.md", "internal/testdata/fixture-repo/a.txt", "docs/x.md")
	// ./internal/c reads nothing beyond its own directory and so has no entry in
	// the scan's map: a shard that ran it gives it one.
	wantReads(t, entry(t, merged, "./internal/c"), "internal/testdata/fixture-repo/a.txt", "docs/x.md")

	// The artifacts are a run's, and a person may merge them into a map they
	// already merged them into: a read is a set, so the second merge is the
	// first.
	twice, gaps, err := FromArtifact(merged, dir)
	if err != nil {
		t.Fatalf("FromArtifact of an already merged map: %v", err)
	}
	if len(gaps) != 0 {
		t.Errorf("a second merge of the same artifacts reports %v, want no gap: the reads are already in the map", gaps)
	}
	twiceData, err := MarshalReadMap(twice)
	if err != nil {
		t.Fatalf("MarshalReadMap: %v", err)
	}
	if string(twiceData) != string(mergedData) {
		t.Errorf("merging the same artifacts twice changed the map:\n%s\n%s", mergedData, twiceData)
	}

	var names []string
	for _, p := range merged.Packages {
		names = append(names, p.Package)
	}
	if !slices.IsSorted(names) {
		t.Errorf("the packages are not sorted: %v", names)
	}

	// The merged map is what a pull request commits, so it must load as the
	// committed one does and survive the round trip unchanged.
	out := filepath.Join(t.TempDir(), "merged.json")
	if err := os.WriteFile(out, mergedData, 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := LoadReadMap(out)
	if err != nil {
		t.Fatalf("LoadReadMap of the merged map: %v", err)
	}
	againData, err := MarshalReadMap(again)
	if err != nil {
		t.Fatalf("MarshalReadMap: %v", err)
	}
	if string(againData) != string(mergedData) {
		t.Errorf("the merged map does not survive a round trip:\n%s\n%s", mergedData, againData)
	}
}

// The trace reads the checkout as the scan does: a path the checkout ignores is
// in no commit — the JUnit report a test command leaves in a gitignored reports/,
// the binary a build writes into the tree root — so a read of one would make the
// committed map differ between two checkouts of the same commit.
func TestTraceIgnoresAPathTheCheckoutIgnores(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod":           goMod,
		".gitignore":       "# the reports the test command writes\n/reports/\n",
		"docs/x.md":        "x\n",
		"reports/tree.xml": "<testsuite/>\n",
	})
	git(t, root, "init", "-q")
	git(t, root, "add", "--", "go.mod", ".gitignore", "docs/x.md")
	raw := strings.Join([]string{
		openLine(1234, filepath.Join(root, "reports", "tree.xml"), filepath.Join(root, "reports", "tree.xml")),
		openLine(1234, filepath.Join(root, "docs", "x.md"), filepath.Join(root, "docs", "x.md")),
	}, "\n")

	got := reduce(raw, root)
	if !slices.Equal(got, []string{"docs/x.md"}) {
		t.Errorf("ReduceOpens = %v, want only docs/x.md: reports/ is in no commit", got)
	}
}

// The trace adds reads and nothing else. A shard traces the tests it ran — one
// family of them, for a ./internal/cli leg — so its reads are a subset of what
// the package opens, and a merge that took them for the whole would clear an
// `any` and drop a read the scan found. That is the safe default the flag's off
// position rests on.
func TestTraceKeepsTheScansEscapeHatches(t *testing.T) {
	m := ReadMap{Packages: []PackageReads{{
		Package: "./internal/a",
		Reads:   []string{"docs/pinned.md", "internal/testdata/fixture-repo/**"},
		Ignores: []string{"node_modules"},
		Any:     true,
	}}}
	dir := artifactDir(t, TraceArtifact{
		Shard:    "cli-3",
		Packages: []string{"./internal/a"},
		Reads:    []string{"docs/x.md"},
	})

	merged, _, err := FromArtifact(m, dir)
	if err != nil {
		t.Fatalf("FromArtifact: %v", err)
	}
	p := entry(t, merged, "./internal/a")
	if !p.Any {
		t.Errorf("./internal/a is no longer any: %+v", p)
	}
	if !slices.Equal(p.Ignores, []string{"node_modules"}) {
		t.Errorf("./internal/a ignores are %v, want them untouched", p.Ignores)
	}
	wantReads(t, p, "docs/pinned.md", "internal/testdata/fixture-repo/**", "docs/x.md")
}

func TestFromArtifactRefusesAnEmptyDirectory(t *testing.T) {
	m := ReadMap{Packages: []PackageReads{{Package: "./internal/a", Reads: []string{"docs/x.md"}}}}
	if _, _, err := FromArtifact(m, t.TempDir()); err == nil {
		t.Fatal("an empty artifact directory merged, want a refusal: the safe default is the scan's own map")
	}
}

func TestTraceArtifactRefusesAnEntryTheMapCannotHold(t *testing.T) {
	for _, body := range []string{
		`{"shard":"rest-1","packages":["internal/a"],"reads":[]}`,
		`{"shard":"rest-1","packages":["./internal/a"],"reads":["/etc/passwd"]}`,
		`{"shard":"rest-1","packages":["./internal/a"],"reads":["../docs/x.md"]}`,
		`{"shard":"","packages":["./internal/a"],"reads":[]}`,
		`{"shard":"rest-1","packages":["./internal/a"],"reads":[],"extra":true}`,
	} {
		file := filepath.Join(t.TempDir(), "reads.json")
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadTraceArtifact(file); err == nil {
			t.Errorf("%s loaded, want a refusal", body)
		}
	}
}
