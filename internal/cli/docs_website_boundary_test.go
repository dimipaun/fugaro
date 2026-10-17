package cli

// The Go/site boundary, the site's own facts, and the console-link rule
// (website plan, Task W0.2; reconciliation findings 1, 2, 25 and 63, D90, D91,
// D92, D95, D97 and D109; Global Constraints 2, 6 and 9).
//
// The site is a second, non-Go artifact that lives in this repository under
// website/ (D90), exactly as the desktop console lives under its own
// directory, and exactly as that one it is a directory the Go module must not
// touch: go build ./..., go vet ./... and gofmt -l . take no notice of it,
// go.mod and go.sum gain nothing from it, and no Go file here builds against a
// path under website/. Three named tests hold that claim, so it stays true a
// year after the task that made it:
//
//   - TestWebsiteIsOutsideTheGoModule: no Go file in the module has an import
//     under website/, and the site's own facts are where the tests read them,
//     so no check below can pass by looking at an empty directory.
//   - TestGoModHasNoWebsiteDependency: go.mod's require block is unchanged
//     from the baseline the module's own boundary test records (see below),
//     go.mod requires no module under website/, and go.sum names none either.
//   - TestNoGoFileEmbedsFromWebsite: no file, test or otherwise, has a
//     go:embed directive over a website/ path.
//
// A fourth test is not about the Go module at all but is this task's other
// half, and it is the mechanical form of Global Constraint 9 -- the site never
// links an unsigned or unreleased artifact: while website/data/site.yaml's
// features.console_downloads is false, no file under website/ may carry this
// repository's release-asset URL. W3.1 flips that flag when the console ships,
// and extends this test in the same commit (§7's L9 file row names this file).
//
// And a fifth pins the facts file itself to the repository it describes,
// because a site that copies a fact is a site that drifts from it (Global
// Constraint 6): the repository URL is go.mod's own module path, min_runs is
// internal/stats.MinRuns, the canonical URL's host is on the site's own host
// list, and every decision-backed value is in the state its decision left it
// in -- analytics' flag and measurement id agreeing with each other (D93; W3.2 sets both together), the console's downloads off
// (D95), the video off (D107), and no second domain (D92).
//
// THE BOUNDARY IS ABOUT CODE, NOT ABOUT BYTES, and that distinction is the
// console boundary's own (docs_console_boundary_test.go), carried here so the
// two cannot disagree: the import check matches on IMPORT LINES only, because
// an import path is a module path, which is the real boundary, while a
// comment or a string that merely names website/data/site.yaml is a read of a
// document. The docs tests of Part 0 -- this task's siblings and their
// successors -- read the site's files as bytes, in the convention every
// docs_*_test.go here already uses.
//
// The walk, the parser and the import extractor are the console boundary
// test's helpers, used rather than copied, so that "a Go file of this module"
// means one thing in both files. What is this file's own is the resolution:
// a path reaches THE SITE when it lands in the module root's website/
// directory, spelled as an import, as a relative import or as a go:embed
// pattern.

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/stats"
	"gopkg.in/yaml.v3"
)

const (
	// websiteDirName is the directory at the module root this boundary is
	// about, and the one token the checks below resolve against.
	websiteDirName = "website"
	// websiteFactsPath is the site's own facts, relative to the module root:
	// the values its pages read and nobody hard-codes (D90, D92, D95, D97).
	websiteFactsPath = "website/data/site.yaml"
	// websiteInstallFactsPath is W0.1's install source. It is named here only
	// to assert it is where the site's pages expect it, which is what keeps
	// the file walk from passing on an empty directory.
	websiteInstallFactsPath = "website/data/install.yaml"
	// websiteDownloadSuffix is the shape the site may not carry while the
	// console's downloads are off: the release-asset path of THIS repository,
	// appended to site.yaml's own repository with its scheme stripped. The
	// needle therefore carries no scheme and no leading "//", so every way a
	// page can write the link holds it -- "https://host/path",
	// "//host/path", a bare "host/path", which is how this repository's own
	// documents spell a GitHub path (website/data/install.yaml's go install
	// line), or a path relative to a host named elsewhere. A needle carrying
	// the scheme would catch only the first of those, and the others render as
	// a live link to an asset that does not exist. Some third party's download
	// path in a lockfile or a document is not this rule's business, which is
	// why the repository is named in full. The archive a reader downloads is
	// reached from the Releases page, which is a page and not a versioned asset
	// URL (finding 25, Global Constraint 9).
	websiteDownloadSuffix = "/releases/download"
)

// websiteFactsFile is website/data/site.yaml, decoded with unknown fields
// refused: a key nobody reads is a fact nobody pins, and the page that would
// read it is a page whose value nothing checks.
type websiteFactsFile struct {
	Version int `yaml:"version"`
	// CanonicalURL is the one absolute URL every other absolute link, the
	// sitemap, the canonical <link> and the OpenGraph tag are built from
	// (W1.3). It is a reserved placeholder until W2.2 replaces it: no domain is
	// registered and nothing is deployed, and the registration, the Firebase
	// custom domain and the DNS records are the owner's steps rather than an
	// agent's (D92).
	CanonicalURL string `yaml:"canonical_url"`
	// SecondaryDomain is the second domain the site would redirect to the
	// canonical one, and there is none: D92's answer is one domain and no
	// redirect domain, so the value is empty and W2.2 writes the redirect block
	// in firebase.json only for a domain that exists.
	SecondaryDomain string `yaml:"secondary_domain"`
	// Repository is the repository every GitHub link on the site is built
	// from (W1.7), pinned below to go.mod's own module path.
	Repository string                `yaml:"repository"`
	Analytics  websiteFactsAnalytics `yaml:"analytics"`
	Features   websiteFactsFeatures  `yaml:"features"`
	// MinRuns is D97's N: no figure the site publishes is drawn from fewer
	// runs than this, and it is the aggregation's own minimum rather than a
	// second number written down here (W0.3, internal/stats.MinRuns).
	MinRuns int `yaml:"min_runs"`
	// AllowedHosts is the host list the site's link and origin checks enforce
	// (W1.11): a link to a host that is not on it fails the build. It is the
	// hosts the site actually links -- github.com and raw.githubusercontent.com
	// -- plus the site's own canonical origin, and nothing else, because every
	// entry is a promise that a reader's browser may talk to that host.
	AllowedHosts []string `yaml:"allowed_hosts"`
}

// websiteFactsAnalytics is the analytics block: D93's answer is Firebase
// Analytics behind a consent banner, and the flag stays false until W3.2
// turns it on -- the load itself goes through W1.12's consent gate either
// way, so a false flag is not the thing that keeps the script off a page.
type websiteFactsAnalytics struct {
	Enabled bool `yaml:"enabled"`
	// MeasurementID is the analytics property the site would report to. It is
	// empty while analytics is off, and W3.2 supplies it.
	MeasurementID string `yaml:"measurement_id"`
}

// websiteFactsFeatures are the features the site renders only when they exist
// (findings 23-25 and 57, D95 and D107).
type websiteFactsFeatures struct {
	// ConsoleDownloads is false until the console ships: its section renders
	// as a placeholder with no link and no button, and a test refuses to emit
	// a download URL while the flag is false (Global Constraint 9).
	ConsoleDownloads bool `yaml:"console_downloads"`
	// Video is false until the owner produces the explainer video: the hero
	// carries no player, no iframe, no poster and no play button.
	Video bool `yaml:"video"`
}

// readWebsiteFacts reads website/data/site.yaml and decodes it strictly,
// returning the file with its raw text: the negative checks scan the bytes,
// because a value in a key nobody reads is still on the page's source.
//
// The read failure is reported repo-relative and without the os.PathError's
// own prefix, so the red step this task was written against reads exactly
// "open website/data/site.yaml: no such file or directory".
func readWebsiteFacts(t *testing.T) (websiteFactsFile, string) {
	t.Helper()
	data, err := os.ReadFile("../../" + websiteFactsPath)
	if err != nil {
		var perr *os.PathError
		if errors.As(err, &perr) {
			t.Fatalf("open %s: %v", websiteFactsPath, perr.Err)
		}
		t.Fatal(err)
	}
	var f websiteFactsFile
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("%s: %v", websiteFactsPath, err)
	}
	return f, string(data)
}

// websiteWalk returns every file under the module root's website/ directory,
// relative to the root, slash-separated and sorted, and it is the one walk the
// three site-wide scans below share.
//
// Two directories are stepped out of, and the rule is deliberately narrower
// than "anything that looks like a build": a name beginning with "." -- a
// toolchain's cache directory (.astro/, .cache/) rather than a source tree --
// is skipped, exactly as the Go tool skips one, and nothing else is. The
// website directory itself is not skipped however it is named, and neither is
// node_modules: it is a directory full of files under website/, and a rule
// that quietly looked past it would be the same hole the console boundary
// records having had. A read error is returned rather than skipped, so a file
// that disappears mid-scan fails the test instead of shortening the scan.
//
// The walk does not follow symlinks (filepath.WalkDir does not), and it reads
// the file NAMES only: the bytes are read by the caller that wants them, so a
// directory of thousands of files costs one stat each and not one read each
// until a scan actually looks at them.
func websiteWalk(root string) ([]string, error) {
	dir := filepath.Join(root, websiteDirName)
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	return files, nil
}

// websiteBoundaryFiles is websiteWalk with the "did it find the site" guard
// the console boundary's own walk carries, so no scan below can pass by
// looking at nothing: the site's directory must exist and hold the two facts
// files the pages read.
func websiteBoundaryFiles(t *testing.T, root string) []string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, websiteDirName)); err != nil {
		t.Fatalf("no %s/ at %s: the site lives there (D90) and Task W0.2 writes its facts: %v", websiteDirName, root, err)
	}
	files, err := websiteWalk(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{websiteFactsPath, websiteInstallFactsPath} {
		if !slices.Contains(files, want) {
			t.Fatalf("the walk over %s/ did not find %s: every check below reads the site through it (%v)", websiteDirName, want, files)
		}
	}
	return files
}

// websiteBoundaryModulePath is go.mod's module path, read the way the console
// boundary test reads it, so an import path can be checked: "under website/"
// means "<module>/website" or below.
func websiteBoundaryModulePath(t *testing.T, root string) string {
	t.Helper()
	for _, line := range strings.Split(readDoc(t, "go.mod"), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module ")
		if ok {
			if mod := strings.TrimSpace(rest); mod != "" {
				return mod
			}
		}
	}
	t.Fatalf("go.mod at %s declares no module path", root)
	return ""
}

// websiteBoundaryResolvedInto reports whether a slash-separated path written
// inside the file at fileRel (both relative to the module root) lands in the
// module root's website/ directory. This is the one place the "which website/
// is this?" question is answered, so an import and an embed pattern cannot
// disagree about it -- and it is the resolution that makes a nested directory
// of the same name somebody else's: a package's own testdata/website/x.md does
// not reach this repository's site.
func websiteBoundaryResolvedInto(fileRel, p string) bool {
	resolved := path.Join(path.Dir(fileRel), path.Clean(p))
	return resolved == websiteDirName || strings.HasPrefix(resolved, websiteDirName+"/")
}

// websiteBoundaryImportReachesWebsite reports whether an import path reaches
// the module root's website directory. An import path is a module path, so the
// first case is the boundary directly; the second covers a relative import,
// which cannot compile in a module but is the same reference spelled
// differently -- the boundary is about what a file says.
func websiteBoundaryImportReachesWebsite(fileRel, imp, websitePkg string) bool {
	switch {
	case imp == websitePkg, strings.HasPrefix(imp, websitePkg+"/"):
		return true
	case strings.HasPrefix(imp, "./"), strings.HasPrefix(imp, "../"):
		return websiteBoundaryResolvedInto(fileRel, imp)
	}
	return false
}

// websiteBoundaryEmbedPatternReachesWebsite reports whether a go:embed pattern
// names a file under the module root's website directory. A pattern is a
// slash-separated path relative to the PACKAGE DIRECTORY, so the resolution is
// what decides, exactly as it does for a relative import, and the all: prefix
// is Go's "walk this directory too" marker rather than part of the path.
func websiteBoundaryEmbedPatternReachesWebsite(fileRel, pattern string) bool {
	return websiteBoundaryResolvedInto(fileRel, strings.TrimPrefix(pattern, "all:"))
}

// websiteDownloadNeedle returns the string the console-link scan refuses,
// built from the site's own repository: "host/path/releases/download", with no
// scheme and no leading "//", so a page that writes the link any way at all --
// absolute, protocol-relative, bare or relative to a base URL -- carries the
// needle. It follows site.yaml's `repository` rather than a hand-typed string,
// so the rule moves with the repository the site links to, and it refuses a
// value that is not a host and a path rather than scanning for a string no
// page could hold.
func websiteDownloadNeedle(t *testing.T, repository string) string {
	t.Helper()
	hostPath := repository
	if i := strings.Index(hostPath, "://"); i >= 0 {
		hostPath = hostPath[i+3:]
	}
	hostPath = strings.TrimSuffix(strings.TrimPrefix(hostPath, "//"), "/")
	host, rest, ok := strings.Cut(hostPath, "/")
	if !ok || rest == "" || !strings.Contains(host, ".") {
		t.Fatalf("%s: repository = %q does not name a host and a path, so the console-link check cannot say what to look for", websiteFactsPath, repository)
	}
	return hostPath + websiteDownloadSuffix
}

// websitePlaceholderHost reports whether a host is a name reserved for
// documentation: RFC 2606's `.test`, `.example`, `.invalid` and `.localhost`
// TLDs, and the reserved example.com/example.net/example.org names. A name
// under one of them can never resolve, which is the only kind of host
// canonical_url may carry while the site has no domain -- a host that resolves
// is a claim about a domain this repository has not registered.
func websitePlaceholderHost(host string) bool {
	host = strings.ToLower(host)
	switch host {
	case "example.com", "example.net", "example.org", "localhost":
		return true
	}
	i := strings.LastIndex(host, ".")
	if i < 0 {
		return false
	}
	switch host[i+1:] {
	case "example", "test", "invalid", "localhost":
		return true
	}
	return false
}

// TestWebsiteIsOutsideTheGoModule: no Go file in this module builds against a
// path under website/, and the site's own facts are where these tests read
// them. The site is a second artifact with its own language and its own build
// (D90), and the module's only coupling to it is a test -- which is exactly
// the claim the console's boundary test makes for the console, and the reason
// both are tests rather than conventions.
func TestWebsiteIsOutsideTheGoModule(t *testing.T) {
	root := consoleBoundaryRoot(t)
	websitePkg := websiteBoundaryModulePath(t, root) + "/" + websiteDirName

	t.Run("no_go_file_imports_a_website_path", func(t *testing.T) {
		for _, rel := range consoleBoundaryGoFiles(t, root) {
			src := readDoc(t, rel)
			imports, parsed := consoleBoundaryImports(t, rel, src)
			if !parsed {
				// A file the parser refuses is not compiled either, so it
				// cannot build against the site; it can still say so, and the
				// text is what catches it -- both spellings, since a relative
				// path reaching the site has no "website/" substring in an
				// import for a literal scan to see.
				if strings.Contains(src, websitePkg) {
					t.Errorf("%s names the import path %s and does not parse: the Go module and %s/ never touch (plan Task W0.2 step 2)", rel, websitePkg, websiteDirName)
				}
				for _, m := range consoleRelativeImportRE.FindAllStringSubmatch(src, -1) {
					if websiteBoundaryImportReachesWebsite(rel, m[1], websitePkg) {
						t.Errorf("%s imports %q and does not parse: a relative import of %s/ is the same boundary (plan Task W0.2 step 2)", rel, m[1], websiteDirName)
					}
				}
				continue
			}
			for _, imp := range imports {
				if websiteBoundaryImportReachesWebsite(rel, imp, websitePkg) {
					t.Errorf("%s imports %q: nothing under %s/ is importable by the Go module, and no Go file builds against it (D90, plan Task W0.2 step 2)", rel, imp, websiteDirName)
				}
			}
		}
	})

	t.Run("the_site_holds_the_facts_its_pages_render", func(t *testing.T) {
		// The walk asserts both facts files are there, so a scan that lost the
		// site fails here rather than passing quietly -- and the facts file is
		// decoded strictly, so a key nobody reads is a failure rather than a
		// value no page and no test would ever see.
		if !slices.Contains(websiteBoundaryFiles(t, root), websiteFactsPath) {
			t.Fatalf("%s is missing", websiteFactsPath)
		}
		facts, _ := readWebsiteFacts(t)
		if facts.Version != 1 {
			t.Errorf("%s: version = %d, want 1", websiteFactsPath, facts.Version)
		}
	})
}

// TestWebsiteBoundaryWalkReadsEveryDirectoryOfTheSite holds websiteWalk's rule,
// both halves of it, because it is the one new rule in this file and all three
// site-wide scans read whatever it returns: a later tightening that stepped out
// of website/src/, website/dist/ or website/node_modules would silently shrink
// the console-link scan over most of the site, and nothing else here would
// notice.
//
// First: a name beginning with "." is stepped out of -- a toolchain's cache
// directory (.astro/, .cache/) is not a source tree, and a walk that read one
// would make the site's scans depend on whatever a local build happened to
// leave behind. Second: everything else is read, and that includes node_modules
// and a build output -- both are full of files under website/, and a file that
// came back from a build is exactly the kind of file the console-link scan
// exists for.
//
// The tree is built under t.TempDir() and passed to websiteWalk directly, so
// the walk's own rule is the only thing exercised and the repository is never
// written to. The console boundary test carries the same case for the same
// reason: a planted file under a directory the walk quietly skipped failed no
// test at all once, while the directory's own README promised it would.
func TestWebsiteBoundaryWalkReadsEveryDirectoryOfTheSite(t *testing.T) {
	root := t.TempDir()
	plant := func(rel string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("planted\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The site's own layout, plus the two directories a walk could be tempted
	// to skip: a dependency tree and a build output.
	read := []string{
		"website/data/site.yaml",
		"website/data/install.yaml",
		"website/src/pages/index.astro",
		"website/src/lib/changelog.mjs",
		"website/test/consent.mjs",
		"website/node_modules/dep/index.js",
		"website/dist/index.html",
	}
	// A dot-directory is the one thing the walk steps out of: a cache, not a
	// source tree. The file under the dependency tree's own dot-directory is
	// skipped with it, so the rule is about the name and not about the depth.
	skipped := []string{
		"website/.astro/types.d.ts",
		"website/.cache/report.json",
		"website/node_modules/dep/.bin/cli",
	}
	for _, rel := range append(slices.Clone(read), skipped...) {
		plant(rel)
	}

	files, err := websiteWalk(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range read {
		if !slices.Contains(files, rel) {
			t.Errorf("the walk did not read %s: every file under %s/ is read except a dot-directory's, and a rule that stepped out of this directory would leave the site's scans looking at a fraction of the site (%v)", rel, websiteDirName, files)
		}
	}
	for _, rel := range skipped {
		if slices.Contains(files, rel) {
			t.Errorf("the walk read %s: a name beginning with \".\" is a toolchain's cache rather than a source tree, and reading one would make the site's scans depend on whatever a local build left behind (plan Task W0.2 step 2)", rel)
		}
	}

	// A walk that cannot read what it was pointed at returns the error: a
	// silent empty result is how a scan passes by looking at nothing.
	if _, err := websiteWalk(filepath.Join(root, "no-such-directory")); err == nil {
		t.Error("websiteWalk on a path that does not exist returned no error: a read failure must fail loudly, not shorten the scan")
	}
}

// TestGoModHasNoWebsiteDependency: the module gains nothing from the site --
// go.mod's require block is unchanged, no require is a module under website/,
// and go.sum names none.
//
// The baseline is consoleGoModRequireBaseline, the module's own boundary test
// records it, and it is used rather than copied on purpose: it is the single
// record of what this module requires, and its maintenance rule is already
// written down there -- a dependency added on purpose is a deliberate edit to
// that slice, made in the same commit as the require line and argued for in
// that commit's message. A require line that arrives without such an edit is
// the failure this test exists for, and it is the failure that would let the
// site smuggle a Go dependency into a module it is not part of.
func TestGoModHasNoWebsiteDependency(t *testing.T) {
	root := consoleBoundaryRoot(t)
	websitePkg := websiteBoundaryModulePath(t, root) + "/" + websiteDirName
	require := consoleRequireLines(readDoc(t, "go.mod"))
	want := slices.Clone(consoleGoModRequireBaseline)
	slices.Sort(want)
	if len(want) == 0 {
		t.Fatal("consoleGoModRequireBaseline is empty: this test would compare go.mod against nothing")
	}

	if !slices.Equal(require, want) {
		for _, line := range require {
			if !slices.Contains(want, line) {
				t.Errorf("go.mod requires %q, which the module's recorded baseline does not: a dependency arrived without a deliberate edit to that baseline. If it is intended, add it there in the same commit and say why; if it is there for the site's sake, it is the boundary itself -- the module gains nothing from %s/ (D90, plan Task W0.2 step 2)", line, websiteDirName)
			}
		}
		for _, line := range want {
			if !slices.Contains(require, line) {
				t.Errorf("go.mod no longer requires %q, which the module's recorded baseline carries: a removal is a deliberate edit to the baseline too (plan Task W0.2 step 2)", line)
			}
		}
	}

	t.Run("no_require_is_a_path_under_the_site", func(t *testing.T) {
		for _, line := range require {
			mod := strings.Fields(line)[0]
			if mod == websitePkg || strings.HasPrefix(mod, websitePkg+"/") {
				t.Errorf("go.mod requires the module %q: a Go module under %s/ is the boundary itself, and nothing under %s/ is importable by the Go module (D90, plan Task W0.2 step 2)", mod, websiteDirName, websiteDirName)
			}
		}
	})

	t.Run("go_sum_names_no_site_module", func(t *testing.T) {
		lines := strings.Split(readDoc(t, "go.sum"), "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			// A go.sum line is "<module> <version> <hash>" (or ".../go.mod").
			mod, _, _ := strings.Cut(line, " ")
			if mod == websitePkg || strings.HasPrefix(mod, websitePkg+"/") {
				t.Errorf("go.sum:%d names the module %q: go.sum gains nothing from the site, and a module under %s/ is the boundary itself (plan Task W0.2 step 2)", i+1, mod, websiteDirName)
			}
		}
		if len(lines) < 10 {
			t.Errorf("go.sum carries %d lines: the scan is looking at the wrong file", len(lines))
		}
	})
}

// TestNoGoFileEmbedsFromWebsite: no file, test or otherwise, uses go:embed over
// a website/ path. Embedding one would put a site file inside a Go binary,
// which is a reference of exactly the kind the boundary forbids -- and unlike
// an import it needs no module and no package to succeed.
func TestNoGoFileEmbedsFromWebsite(t *testing.T) {
	root := consoleBoundaryRoot(t)

	// The proof that this check can see anything is the extractor's own, and it
	// is shown a directive AND a resolution it must find: a module with no
	// go:embed directive at all is a legitimate state -- the last one can be
	// deleted on purpose -- so a count of what the tree happens to hold would
	// ask the author to break the test to make it pass.
	const probe = "package probe\n\nimport _ \"embed\"\n\n//go:embed ../../website/data/site.yaml\nvar zzz string\n"
	patterns := consoleBoundaryEmbedPatterns("probe.go", probe)
	if !slices.Equal(patterns, []string{"../../website/data/site.yaml"}) {
		t.Fatalf("the go:embed extractor read %q out of a known source, so a scan finding nothing proves nothing", patterns)
	}
	if !websiteBoundaryEmbedPatternReachesWebsite("internal/cli/probe.go", patterns[0]) {
		t.Fatalf("the embed resolution does not see %q from internal/cli as a %s/ path, so this test would find nothing", patterns[0], websiteDirName)
	}

	for _, rel := range consoleBoundaryGoFiles(t, root) {
		for _, pattern := range consoleBoundaryEmbedPatterns(rel, readDoc(t, rel)) {
			if websiteBoundaryEmbedPatternReachesWebsite(rel, pattern) {
				t.Errorf("%s embeds %q with a go:embed directive: no file, test or otherwise, embeds from %s/, which puts a site file inside a Go binary (plan Task W0.2 step 2)", rel, pattern, websiteDirName)
			}
		}
	}
}

// TestWebsiteLinksNoConsoleWhileTheFlagIsOff is Global Constraint 9 made
// mechanical: the site never links an unsigned or unreleased artifact. The
// console is designed and not shipped (finding 23), so its section renders as
// a placeholder -- and while website/data/site.yaml's features.console_downloads
// is false, this repository's release-asset URL anywhere under website/ is a
// link to an asset that does not exist.
//
// The flag is the site's own, so this is a scan of the site rather than of the
// Go tree, and it is deliberately blunt: every file under website/, whatever
// its name, is read. W3.1 flips the flag when the console's first release
// ships, and it extends this test in the same commit -- so a true flag fails
// here, loudly, rather than turning the scan below into a silent pass.
//
// The URL to look for is built from the facts file's own `repository`, so the
// rule follows the repository the site links to, and the field's pin (in
// TestWebsiteFactsMatchTheRepository) keeps that value honest.
func TestWebsiteLinksNoConsoleWhileTheFlagIsOff(t *testing.T) {
	root := consoleBoundaryRoot(t)
	facts, _ := readWebsiteFacts(t)

	if facts.Features.ConsoleDownloads {
		t.Fatalf("%s: features.console_downloads is true and this test has not been extended: the flag is flipped by W3.1 when the console ships, in the same commit that extends this test to check every release-asset URL under %s/ against a release asset of a tag that exists (W1.6 step 2, W3.1, Global Constraint 9)", websiteFactsPath, websiteDirName)
	}

	// The URL the site may not carry, built from the facts file's own
	// repository rather than typed here, so the rule follows the repository the
	// site links to (and the pin in TestWebsiteFactsMatchTheRepository keeps
	// that value honest).
	download := websiteDownloadNeedle(t, facts.Repository)

	for _, rel := range websiteBoundaryFiles(t, root) {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, download) {
				continue
			}
			t.Errorf("%s:%d carries the console download URL %q while features.console_downloads is false: the console is not released, the site links the Releases page and never a versioned asset URL, and a link here would either 404 or point at an unsigned artifact (finding 25, D95, Global Constraint 9)\n%s", rel, i+1, download, line)
		}
	}
}

// TestWebsiteDownloadNeedleSeesEverySpelling is the console-link scan's
// positive control: it plants the release-asset URL in a temp tree in the
// spellings a page could use -- with a scheme, and without one -- and proves
// the needle and the scan find each, and a clean file stays clean, so the
// scan cannot pass by looking at nothing.
func TestWebsiteDownloadNeedleSeesEverySpelling(t *testing.T) {
	needle := websiteDownloadNeedle(t, "https://github.com/example-org/example-repo")
	root := t.TempDir()
	planted := map[string]string{
		"website/src/pages/a.astro":  "<a href=\"https://github.com/example-org/example-repo/releases/download/v1/x\">",
		"website/src/pages/b.astro":  "<a href=\"github.com/example-org/example-repo/releases/download/v1/x\">",
		"website/src/pages/c.astro":  "<a href=\"//github.com/example-org/example-repo/releases/download/v1/x\">",
		"website/src/pages/ok.astro": "<a href=\"https://github.com/example-org/example-repo/releases\">",
	}
	for rel, body := range planted {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := websiteWalk(root)
	if err != nil {
		t.Fatal(err)
	}
	hits := map[string]bool{}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		hits[rel] = strings.Contains(string(data), needle)
	}
	for _, rel := range []string{"website/src/pages/a.astro", "website/src/pages/b.astro", "website/src/pages/c.astro"} {
		if !hits[rel] {
			t.Errorf("the scan did not see the planted download URL in %s (needle %q): a console-link check that misses a spelling passes by looking at nothing", rel, needle)
		}
	}
	if hits["website/src/pages/ok.astro"] {
		t.Errorf("the scan flagged the Releases page link, which the site is allowed to carry (needle %q)", needle)
	}
}

// TestWebsiteFactsMatchTheRepository pins website/data/site.yaml to the
// repository it describes, rather than to its own prose (Global Constraint 6):
// every value a page reads has one source, and this is where the two are held
// together.
//
// What is pinned to what, and which task relaxes each pin. Every message below
// names that task, because a later task that changes a value here meets this
// test, and its failure has to say what to do rather than argue with the task
// that owns the change (§7's file-conflict note records this file's writers):
//
//   - repository: go.mod's own module path, so every GitHub link W1.7 builds
//     points at this repository and at no other. No task changes it;
//   - min_runs: internal/stats.MinRuns, the aggregation's own minimum (D97's
//     N is 20 and it is one number, not two). A change to D97's N moves both
//     values, in the aggregation's task rather than in a page's;
//   - canonical_url: a reserved placeholder origin (websitePlaceholderHost)
//     with no path, no query and no trailing slash, whose host is on the
//     site's own allowed_hosts list. W2.2 replaces the value and relaxes this
//     check in the same commit, when the domain exists and serves the site;
//   - secondary_domain: empty, because D92's answer is one domain and no
//     redirect domain. W2.2 writes a value here, with the redirect rule in
//     firebase.json, only if the owner ever acquires a second domain;
//   - analytics: the flag and the measurement id agree with each other -- on
//     with a property, or off without one -- so W3.2 sets both when it turns
//     the measurement on (D93) and does not have to edit this file at all;
//   - features: the console's downloads and the video are both off. W3.1
//     flips the first with the console's first release, and W2.4 the second
//     when the video exists, each in the commit that relaxes its own check;
//   - allowed_hosts: bare lowercase hosts, no scheme, no port and no path,
//     carrying the two hosts the site's links need and the canonical host.
func TestWebsiteFactsMatchTheRepository(t *testing.T) {
	facts, raw := readWebsiteFacts(t)
	mod := websiteBoundaryModulePath(t, consoleBoundaryRoot(t))

	if want := "https://" + mod; facts.Repository != want {
		t.Errorf("%s: repository = %q, want %q (go.mod's module path): every GitHub link the site builds comes from this value, and a copy of it is a second source that drifts (plan Task W0.2 step 1)", websiteFactsPath, facts.Repository, want)
	}

	if facts.MinRuns != stats.MinRuns {
		t.Errorf("%s: min_runs = %d, internal/stats.MinRuns = %d: the site publishes no figure drawn from fewer runs than the aggregation's own minimum, and one number cannot have two values (D97, plan Task W0.2 step 1)", websiteFactsPath, facts.MinRuns, stats.MinRuns)
	}

	// The canonical URL: an absolute https URL with no path, no query and no
	// trailing slash, under a reserved placeholder host the site's own list
	// admits. W2.2 is the task that writes the real one.
	const scheme = "https://"
	host, found := strings.CutPrefix(facts.CanonicalURL, scheme)
	switch {
	case !found:
		t.Errorf("%s: canonical_url = %q, want an absolute %s URL (D92, plan Task W0.2 step 1)", websiteFactsPath, facts.CanonicalURL, scheme)
	case host == "" || strings.ContainsAny(host, "/?#"):
		t.Errorf("%s: canonical_url = %q, want the origin alone -- the canonical <link>, the sitemap and the OpenGraph tag are built from it, so a path or a query here would be copied into all of them", websiteFactsPath, facts.CanonicalURL)
	default:
		if !websitePlaceholderHost(host) {
			t.Errorf("%s: canonical_url names the host %q, which is not a reserved placeholder: no domain is registered and nothing is deployed, so a real host here would make the canonical <link>, the sitemap, the OpenGraph tag and every absolute link the site builds point at a domain this repository does not serve. W2.2 sets the real canonical URL, in the same commit that relaxes this check (D92, plan Task W2.2 step 1)", websiteFactsPath, host)
		}
		if !slices.Contains(facts.AllowedHosts, host) {
			t.Errorf("%s: canonical_url names the host %q, which allowed_hosts %v does not: the list admits exactly the hosts the site may send a reader to, and the site's own is one of them. W2.2 changes both lines together, in the same commit (W1.11, plan Task W0.2 step 1)", websiteFactsPath, host, facts.AllowedHosts)
		}
	}

	if facts.SecondaryDomain != "" {
		t.Errorf("%s: secondary_domain = %q, want empty: D92's answer is one domain and no redirect domain, so a value here would be a redirect the site cannot make and a second source for a name. W2.2 sets it, with the redirect rule in firebase.json, only for a domain that exists (D92, plan Task W2.2 step 1)", websiteFactsPath, facts.SecondaryDomain)
	}

	// Analytics: the two values have to agree, and that is the invariant worth
	// pinning -- W3.2 turns the measurement on by setting the property id and
	// the flag together, so it meets no red test here, while a flag turned on
	// with nowhere to report to is refused wherever it is done.
	if facts.Analytics.Enabled != (facts.Analytics.MeasurementID != "") {
		t.Errorf("%s: analytics is %+v, and its two values must agree: analytics that report nowhere and a property id nothing reports to are both broken states, and W3.2 sets both together when it turns the measurement on behind W1.12's consent gate (D93, plan Task W3.2)", websiteFactsPath, facts.Analytics)
	}

	if facts.Features.ConsoleDownloads {
		t.Errorf("%s: features.console_downloads is true: the console is designed and not shipped, so its section may not render as something a reader can follow. W3.1 flips this flag with the console's first release, in the same commit that extends TestWebsiteLinksNoConsoleWhileTheFlagIsOff (D95, finding 23, plan Task W3.1)", websiteFactsPath)
	}
	if facts.Features.Video {
		t.Errorf("%s: features.video is true: the explainer video does not exist, so the hero may not render a player, an iframe, a poster or a play button. W2.4 sets this flag when the video exists, in the same commit that relaxes this check (D107, finding 57, plan Task W2.4 step 1)", websiteFactsPath)
	}

	// The host list: bare hosts, and the two the site's links need.
	if len(facts.AllowedHosts) == 0 {
		t.Errorf("%s: allowed_hosts is empty: the check W1.11 runs admits links to the hosts on it and refuses every other, so an empty list is a site with no external link at all", websiteFactsPath)
	}
	for _, h := range facts.AllowedHosts {
		if h == "" || strings.ToLower(h) != h || strings.ContainsAny(h, "/:@?#") {
			t.Errorf("%s: allowed_hosts carries %q, want a bare lowercase host with no scheme, port or path: every entry is a promise that a reader's browser may talk to that host, and it is compared against a URL's host (W1.11, plan Task W0.2 step 1)", websiteFactsPath, h)
		}
	}
	for _, want := range []string{"github.com", "raw.githubusercontent.com"} {
		if !slices.Contains(facts.AllowedHosts, want) {
			t.Errorf("%s: allowed_hosts %v does not carry %q, the host the site's own GitHub links -- the repository, the Releases page and the raw documents the docs hub renders -- are made to (plan Task W0.2 step 1)", websiteFactsPath, facts.AllowedHosts, want)
		}
	}

	// The file's own shape, so nothing above can pass on a file whose keys are
	// spelled the way no page will read them.
	if !strings.Contains(raw, "version: 1") {
		t.Errorf("%s carries no version line: the file is versioned like website/data/install.yaml, so a change to its shape can be told from a change to its values", websiteFactsPath)
	}
	for _, placeholder := range []string{"TODO", "FIXME", "XXX"} {
		if strings.Contains(raw, placeholder) {
			t.Errorf("%s carries the placeholder %q: a value a page reads is either the fact or the empty state, never a note to a future reader", websiteFactsPath, placeholder)
		}
	}
}
