package ciselect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The runtime trace (CS2b): the repository files a full run's test processes and
// their children opened, which the scan of CS2a can only read out of the source.
// A shard wraps its `go test` in `strace -f -y -e trace=openat,open,execve`,
// reduces the log to repository-relative paths with ReduceOpens, and uploads the
// small artifact; the merge back into the committed map is a pull request made
// from the nightly's artifacts, never a push from CI (R10).
//
// The build is not the test. The go command a shard starts, and every compile,
// vet and link it runs, are the toolchain's own processes: a compiler opens the
// source of every package it builds and the directory of each of them, so a log
// taken around the whole `go test` names every package of the leg as a read of
// every package it ran — over-selection, and a scan gap for each. ReduceOpens
// drops them, which is what the log's execve lines are for.
//
// The trace only ever adds a read. It does not clear an `any` mark and does not
// drop a read the scan found: a trace covers the tests a shard ran, which for a
// `./internal/cli` leg is one family of them, so the trace is a subset of what
// the package opens and the scan's conservative marks must survive it.

// TraceArtifact is one shard's share of a full run's trace: the packages the
// shard ran and the repository paths their test processes and their children
// opened, reduced to repository-relative paths. It is the artifact ci.yml
// uploads and the format ci-readmap --from-artifact merges.
type TraceArtifact struct {
	// Shard is the matrix leg the log came from ("cli-3", "rest-1"). It names
	// the artifact and tells a reader which leg opened what.
	Shard string `json:"shard"`
	// Packages are the packages the shard ran, in the map's own form
	// ("./internal/cli"). A leg that runs one test family of a package names
	// that package, and its reads are merged into that package's entry.
	Packages []string `json:"packages"`
	// Reads are the repository paths the traced processes opened, as ReduceOpens
	// reduces them: a file the checkout holds, or "dir/**" for a directory.
	Reads []string `json:"reads"`
}

// MarshalTraceArtifact returns one shard's trace as it is uploaded: indented and
// newline-terminated, like the committed map, so a diff of one reads well.
func MarshalTraceArtifact(a TraceArtifact) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(a); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// LoadTraceArtifact decodes one shard's trace. It refuses a field this build
// does not know, and it refuses an entry the committed map could not hold — a
// package that is not "./dir", or a read that is not a canonical
// repository-relative path. What an artifact names is merged into a committed
// file, so a value that is not a path of this repository is a finding to report
// rather than something to write down.
func LoadTraceArtifact(file string) (TraceArtifact, error) {
	f, err := os.Open(file)
	if err != nil {
		return TraceArtifact{}, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var a TraceArtifact
	if err := dec.Decode(&a); err != nil {
		return TraceArtifact{}, fmt.Errorf("%s: %w", file, err)
	}
	if a.Shard == "" {
		return TraceArtifact{}, fmt.Errorf("%s: the artifact names no shard", file)
	}
	for _, pkg := range a.Packages {
		if !strings.HasPrefix(pkg, "./") || !repoRelative(strings.TrimPrefix(pkg, "./")) {
			return TraceArtifact{}, fmt.Errorf("%s: %q is not a package of this repository in the map's \"./dir\" form", file, pkg)
		}
	}
	for _, r := range a.Reads {
		if !repoRelative(r) {
			return TraceArtifact{}, fmt.Errorf("%s: %q is not a repository-relative path", file, r)
		}
	}
	return a, nil
}

// repoRelative reports whether a path is one the map can name as it stands: a
// slash-separated path that a clean of it leaves unchanged, so no "" segment, no
// trailing slash, no "../" and nothing absolute.
func repoRelative(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\n\t") {
		return false
	}
	clean := path.Clean(p)
	return clean == p && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

// ScanGap is a repository path the trace found that the scan did not list for
// the package that opened it: the scan named no way for a change to reach that
// package, and the merge records the path as a read of it.
type ScanGap struct {
	Package string
	Path    string
}

func (g ScanGap) String() string { return g.Package + ": " + g.Path }

// FromArtifact merges the traces a full run left under dir — one artifact per
// shard, at any depth, because `gh run download` writes one directory per
// artifact — into m as the `reads` of the packages each shard ran, and reports
// the scan gaps, sorted.
//
// A package the scan marked `any` has no gaps: an `any` entry already reaches
// every path it does not exclude (R4), so the trace found nothing the scan had
// missed. A package no entry names at all — the scan leaves out a directory
// whose tests read nothing beyond their own package — gains an entry here.
//
// A directory that holds no artifact is refused rather than merged: the flag's
// safe default is the scan's map, and a merge that quietly wrote an unchanged
// file would look like a trace that found nothing.
func FromArtifact(m ReadMap, dir string) (ReadMap, []ScanGap, error) {
	files, err := traceArtifacts(dir)
	if err != nil {
		return ReadMap{}, nil, err
	}
	if len(files) == 0 {
		return ReadMap{}, nil, fmt.Errorf("%s holds no trace artifact: nothing to merge", dir)
	}
	out := ReadMap{Packages: make([]PackageReads, len(m.Packages))}
	index := make(map[string]int, len(m.Packages))
	for i, p := range m.Packages {
		p.Reads = slices.Clone(p.Reads)
		p.Ignores = slices.Clone(p.Ignores)
		out.Packages[i] = p
		index[p.Package] = i
	}
	var gaps []ScanGap
	for _, file := range files {
		a, err := LoadTraceArtifact(file)
		if err != nil {
			return ReadMap{}, nil, err
		}
		for _, pkg := range a.Packages {
			i, ok := index[pkg]
			if !ok {
				out.Packages = append(out.Packages, PackageReads{Package: pkg})
				i = len(out.Packages) - 1
				index[pkg] = i
			}
			reads := slices.Clone(out.Packages[i].Reads)
			for _, r := range a.Reads {
				if coveredBy(reads, r) {
					// The map already names it, exactly or through a pattern of
					// the shape it writes for a directory: a trace of 17 packages
					// must not put a file an entry's "dir/**" already reaches
					// into the committed file a second time.
					continue
				}
				if !out.Packages[i].Any {
					if gap := (ScanGap{Package: pkg, Path: r}); !slices.Contains(gaps, gap) {
						gaps = append(gaps, gap)
					}
				}
				reads = append(reads, r)
			}
			slices.Sort(reads)
			out.Packages[i].Reads = reads
		}
	}
	sort.Slice(out.Packages, func(i, j int) bool { return out.Packages[i].Package < out.Packages[j].Package })
	slices.SortFunc(gaps, func(a, b ScanGap) int {
		if a.Package != b.Package {
			return strings.Compare(a.Package, b.Package)
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out, gaps, nil
}

// coveredBy reports whether a package's scanned reads already name a path: the
// path itself, or a pattern of the shape the map writes for a directory or for a
// literal file name ("docs/**", "internal/cli/docs_*_test.go").
func coveredBy(reads []string, p string) bool {
	name := strings.Split(p, "/")
	for _, pat := range reads {
		if matchSegments(strings.Split(pat, "/"), name) {
			return true
		}
	}
	return false
}

// traceArtifacts lists the trace artifacts under dir in the tree's own order: a
// full run's artifacts arrive as one directory each, and the merge must not
// depend on the order a filesystem hands them back.
func traceArtifacts(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// pidPrefix is what strace -f puts in front of a line: the pid of a process it
// followed into ("[pid  1235] ") or of the one it started ("1234  "), which is
// the process the call behind it belongs to.
var pidPrefix = regexp.MustCompile(`^\s*(?:\[pid\s+(\d+)\]\s+|(\d+)\s+)?`)

// call splits one log line into the pid it belongs to and the call it records.
// A log taken without -f carries no pid, and then a line's own text is its call.
func call(line string) (pid, rest string) {
	m := pidPrefix.FindStringSubmatchIndex(line)
	switch {
	case m[2] >= 0:
		pid = line[m[2]:m[3]]
	case m[4] >= 0:
		pid = line[m[4]:m[5]]
	}
	return pid, line[m[1]:]
}

// openCall matches an open in the call that follows a line's pid prefix.
// Anchoring at its head is what keeps a path a line merely mentions — the binary
// an execve named, a string a read returned — from being read as an open of its
// own.
var openCall = regexp.MustCompile(`^(?:openat2|openat64|openat|open64|open)\(`)

// execCall matches the exec of a log taken with execve among its traced calls:
// the call that says which program a pid is running.
var execCall = regexp.MustCompile(`^execve(?:at)?\(`)

// fdPath matches the path strace's -y decorates a returned descriptor with
// ("= 3</work/fugaro/docs/x.md>"): the path the process really reached, however
// it spelled it.
var fdPath = regexp.MustCompile(`= *\d+<([^>]*)>`)

// ReduceOpens turns one shard's `strace -f -y -e trace=openat,open,execve` log
// into the repository files its test processes and their children opened: the
// paths under root, minus those under a drop root the caller names — the go
// build and module caches, which the shard reads out of `go env` — minus .git,
// minus the paths the checkout does not hold, and minus everything a process of
// the go toolchain's own opened. A path that is a directory becomes the "dir/**"
// a read of a directory means in the map everywhere else, and the result is
// sorted and deduplicated, so the artifact does not depend on the order the
// shard's processes ran or on how often they opened one file.
//
// goroot is where that toolchain's own programs live, as `go env GOROOT` answers
// it, and "" treats no process as the toolchain's. The build a shard runs is the
// shard's rather than its tests': the go command, and the compile, vet and link
// it starts, open the source of every package they build and the directory of
// each of them, and taking those for reads would name every package of the leg
// as a read of every package it ran.
//
// A relative argument is placed by -y's decoration alone: it means the
// process's working directory, which the log does not carry.
func ReduceOpens(raw, root, goroot string, drop ...string) []string {
	// The log names what a process opened: an absolute path. The root a caller
	// hands over is the checkout it scans, which may be named "." or relatively,
	// so it is made absolute before anything is compared with it.
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	root = filepath.Clean(root)
	// An answer that is not a directory — no go, or a tool that named one
	// relatively — leaves no process read as the toolchain's, which costs only
	// the over-selection this drops.
	if goroot = filepath.Clean(goroot); !filepath.IsAbs(goroot) {
		goroot = ""
	}
	drops := make([]string, 0, len(drop))
	for _, d := range drop {
		if d != "" {
			drops = append(drops, filepath.Clean(d))
		}
	}
	// The checkout, and not the disk, is the point, exactly as in the scan: a
	// path a commit does not hold is in no diff, and a read of one — the name of
	// an open that failed, a report a run wrote into the tree, a file the
	// checkout ignores — would make the committed map differ between two
	// checkouts of the same commit.
	files, dirs := checkoutIndex(root)
	// progs is the program each traced process is running, from the log's own
	// execve lines: a process of the go toolchain's is the build, and the source
	// it opens is one it compiles rather than one a test read.
	progs := map[string]string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		pid, rest := call(line)
		if prog, ok := execProgram(rest); ok {
			progs[pid] = prog
			continue
		}
		if isBuild(progs[pid], goroot) {
			continue
		}
		p, ok := openPath(rest)
		if !ok {
			continue
		}
		rel, ok := repoFile(p, root, drops)
		if !ok {
			continue
		}
		switch {
		case files != nil:
			switch {
			case files[rel]:
				seen[rel] = true
			case dirs[rel]:
				seen[rel+"/**"] = true
			}
		default:
			// Not a checkout: with no commit to read, the disk is all there is.
			switch info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); {
			case err != nil:
			case info.IsDir():
				seen[rel+"/**"] = true
			default:
				seen[rel] = true
			}
		}
	}
	return sorted(seen)
}

// checkoutIndex is the paths the checkout holds and the directories that hold
// them, for a membership test. Both are nil when git cannot answer — no git, or
// a tree that is not a repository — which is when a caller reads the disk.
func checkoutIndex(root string) (files, dirs map[string]bool) {
	names, ok := checkoutFiles(root)
	if !ok {
		return nil, nil
	}
	files, dirs = make(map[string]bool, len(names)), map[string]bool{}
	for _, name := range names {
		if !repoRelative(name) {
			continue
		}
		files[name] = true
		for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	return files, dirs
}

// openPath is the path one open names: the descriptor's -y decoration when there
// is one, and otherwise the argument the process passed, which is usable only
// when it is absolute.
func openPath(rest string) (string, bool) {
	loc := openCall.FindStringIndex(rest)
	if loc == nil {
		return "", false
	}
	if m := fdPath.FindStringSubmatch(rest[loc[1]:]); m != nil && m[1] != "" {
		return m[1], true
	}
	return quotedArg(rest[loc[1]:])
}

// execProgram is the program a call makes its process: the path an exec named,
// when the exec succeeded. A failed one leaves the process running what it was
// already running, so it says nothing about the program.
func execProgram(rest string) (string, bool) {
	loc := execCall.FindStringIndex(rest)
	if loc == nil || !strings.HasSuffix(strings.TrimSpace(rest), "= 0") {
		return "", false
	}
	return quotedArg(rest[loc[1]:])
}

// isBuild reports whether a traced process is one of the go toolchain's own: the
// go command a shard started, and the compile, vet, link and asm tools it runs,
// all of which live under the go root. What one of them opens is a package's
// source or its directory — the build reading the tree, which the map already
// reaches through the import graph — and not a file a test read.
func isBuild(prog, goroot string) bool {
	if prog == "" || goroot == "" {
		return false
	}
	return prog == goroot || strings.HasPrefix(prog, goroot+string(filepath.Separator))
}

// quotedArg is the first C-quoted argument of a call: the path the process
// passed, which strace escapes the way C writes a string literal.
func quotedArg(s string) (string, bool) {
	i := strings.IndexByte(s, '"')
	if i < 0 {
		return "", false
	}
	var b strings.Builder
	for k := i + 1; k < len(s); k++ {
		switch c := s[k]; {
		case c == '\\':
			if k+1 >= len(s) {
				return "", false
			}
			b.WriteByte(c)
			k++
			b.WriteByte(s[k])
		case c == '"':
			out, err := strconv.Unquote(`"` + b.String() + `"`)
			if err != nil {
				return "", false
			}
			return out, true
		default:
			b.WriteByte(c)
		}
	}
	return "", false
}

// repoFile is one opened path as the map names files: repository-relative, under
// root, outside every drop root and outside the .git directory itself.
func repoFile(p, root string, drops []string) (string, bool) {
	if !filepath.IsAbs(p) {
		return "", false
	}
	abs := filepath.Clean(p)
	sep := string(filepath.Separator)
	for _, d := range drops {
		if abs == d || strings.HasPrefix(abs, d+sep) {
			return "", false
		}
	}
	if !strings.HasPrefix(abs, root+sep) {
		return "", false
	}
	rel := filepath.ToSlash(strings.TrimPrefix(abs, root+sep))
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return "", false
	}
	return rel, true
}
