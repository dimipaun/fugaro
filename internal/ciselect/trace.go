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

// The runtime trace (CS2b): what a full run's tests really opened, which the
// scan of CS2a can only read out of the source. A shard wraps its `go test` in
// `strace -f -y -e trace=openat,open`, reduces the log to repository-relative
// paths with ReduceOpens, and uploads the small artifact; the merge back into
// the committed map is a pull request made from the nightly's artifacts, never a
// push from CI (R10).
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
				if !out.Packages[i].Any && !coveredBy(reads, r) {
					if gap := (ScanGap{Package: pkg, Path: r}); !slices.Contains(gaps, gap) {
						gaps = append(gaps, gap)
					}
				}
				if !slices.Contains(reads, r) {
					reads = append(reads, r)
				}
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

// openCall matches the opens of a `strace -f -y -e trace=openat,open` log. The
// pid strace prefixes a line with is not part of the match: only the call and
// its arguments are read.
var openCall = regexp.MustCompile(`\b(?:openat2|openat64|openat|open64|open)\(`)

// fdPath matches the path strace's -y decorates a returned descriptor with
// ("= 3</work/fugaro/docs/x.md>"): the path the process really reached, however
// it spelled it.
var fdPath = regexp.MustCompile(`= *\d+<([^>]*)>`)

// ReduceOpens turns one shard's `strace -f -y -e trace=openat,open` log into the
// repository files its test processes and their children opened: the paths under
// root, minus those under a drop root the caller names — the go build and module
// caches, which the shard reads out of `go env` — minus .git, and minus the
// paths the checkout does not hold, since strace prints the name of an open that
// failed too. A path that is a directory becomes the "dir/**" a read of a
// directory means in the map everywhere else, and the result is sorted and
// deduplicated, so the artifact does not depend on the order the shard's
// processes ran or on how often they opened one file.
//
// A relative argument is placed by -y's decoration alone: it means the
// process's working directory, which the log does not carry.
func ReduceOpens(raw, root string, drop ...string) []string {
	// The log names what a process opened: an absolute path. The root a caller
	// hands over is the checkout it scans, which may be named "." or relatively,
	// so it is made absolute before anything is compared with it.
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	root = filepath.Clean(root)
	drops := make([]string, 0, len(drop))
	for _, d := range drop {
		if d != "" {
			drops = append(drops, filepath.Clean(d))
		}
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		p, ok := openPath(line)
		if !ok {
			continue
		}
		rel, ok := repoFile(p, root, drops)
		if !ok {
			continue
		}
		switch info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); {
		case err != nil:
			// The checkout does not hold it: a failed open, or a name only a
			// build wrote, neither of which a change can reach.
		case info.IsDir():
			seen[rel+"/**"] = true
		default:
			seen[rel] = true
		}
	}
	return sorted(seen)
}

// openPath is the path one open line names: the descriptor's -y decoration when
// there is one, and otherwise the argument the process passed, which is usable
// only when it is absolute.
func openPath(line string) (string, bool) {
	loc := openCall.FindStringIndex(line)
	if loc == nil {
		return "", false
	}
	if m := fdPath.FindStringSubmatch(line[loc[1]:]); m != nil && m[1] != "" {
		return m[1], true
	}
	return quotedArg(line[loc[1]:])
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
