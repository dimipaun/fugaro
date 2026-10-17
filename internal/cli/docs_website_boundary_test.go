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
// (D95), the video off (D107), and the second domain, when there is one,
// distinct from the canonical host and off the host list (D92).
//
// W2.2 adds three checks to this file, all three of them about the domain
// being real: the canonical URL is a real host and appears in site.yaml and
// nowhere else under website/ (TestWebsiteCanonicalURLAppearsOnlyInSiteYaml),
// no firebase.json redirect rule sends one of the site's own paths to the
// site's own host (TestWebsiteFirebaseRedirectsDoNotLoop -- the rule a
// domain-level redirect replaces), and the register carries the two owner-run
// live checks the plan reserves for this task
// (TestWebsiteChecklistHasCheck69aAnd69b).
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
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
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
	// (W1.3). It was a reserved placeholder until W2.2 replaced it with the
	// domain the owner registered and connected Firebase Hosting to; the
	// registration, the Firebase custom domain and the DNS records are the
	// owner's steps rather than an agent's (D92).
	CanonicalURL string `yaml:"canonical_url"`
	// SecondaryDomain is the second domain the site redirects to the canonical
	// one. D92's answer is one canonical domain and a second one whose only job
	// is to send the reader back, so the value is a bare host or empty, and the
	// redirect that uses it is a property of the attached domain rather than a
	// firebase.json rule (the owner's, and check 69a's first step).
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
//   - canonical_url: an absolute https origin -- the origin alone, with no
//     path, no query and no trailing slash -- whose host is a real host (not a
//     reserved placeholder name) and is on the site's own allowed_hosts list.
//     W2.2 replaced the placeholder W0.2 wrote with the domain the owner
//     registered and connected Firebase Hosting to, and relaxed this check in
//     the same commit;
//   - secondary_domain: empty, or a bare lowercase host that is neither the
//     canonical host nor a host the site links (D92: one canonical domain, and
//     a second one whose only job is to send the reader back to it). W2.2 set
//     it to the domain the owner acquired and redirects to the canonical one;
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
	// trailing slash, under a real host -- the domain the owner registered and
	// connected Firebase Hosting to -- which the site's own list admits. The
	// pin was the reverse until W2.2 (a reserved placeholder host, because no
	// domain existed); W2.2 writes the real value and relaxes this check in the
	// same commit, which is what the task's "the pin is relaxed in the same
	// commit" means.
	const scheme = "https://"
	host, found := strings.CutPrefix(facts.CanonicalURL, scheme)
	switch {
	case !found:
		t.Errorf("%s: canonical_url = %q, want an absolute %s URL (D92, plan Task W0.2 step 1)", websiteFactsPath, facts.CanonicalURL, scheme)
	case host == "" || strings.ContainsAny(host, "/?#"):
		t.Errorf("%s: canonical_url = %q, want the origin alone -- the canonical <link>, the sitemap and the OpenGraph tag are built from it, so a path or a query here would be copied into all of them", websiteFactsPath, facts.CanonicalURL)
	default:
		if websitePlaceholderHost(host) {
			t.Errorf("%s: canonical_url names the reserved placeholder host %q: the domain is registered and Firebase Hosting serves the site on it, so the canonical <link>, the sitemap, the OpenGraph tag and every absolute link the site builds would point at a name that can never resolve. W2.2 sets the real canonical URL (D92, the owner's ruling of 2026-10-17, plan Task W2.2 step 1)", websiteFactsPath, host)
		}
		if !slices.Contains(facts.AllowedHosts, host) {
			t.Errorf("%s: canonical_url names the host %q, which allowed_hosts %v does not: the list admits exactly the hosts the site may send a reader to, and the site's own is one of them. W2.2 changes both lines together, in the same commit (W1.11, plan Task W0.2 step 1)", websiteFactsPath, host, facts.AllowedHosts)
		}
	}

	// The secondary domain: empty, or the bare host the site redirects TO the
	// canonical one (D92: one canonical domain, and a second whose only job is
	// to send the reader back). It is a host and not a URL -- the direction is
	// the whole point of it -- it is not the canonical host, and it is not on
	// allowed_hosts: that list admits the hosts the site may LINK to, and the
	// site links nothing on the domain it redirects away from. The redirect
	// itself is a domain-level setting and not a firebase.json rule, which is
	// why nothing in this file pins a rule in that file (see
	// TestWebsiteFirebaseRedirectsDoNotLoop).
	if facts.SecondaryDomain != "" {
		secondary := facts.SecondaryDomain
		switch {
		case !strings.Contains(secondary, ".") || strings.ContainsAny(secondary, "/:@?#") || secondary != strings.ToLower(secondary):
			t.Errorf("%s: secondary_domain = %q, want a bare lowercase host: it names the domain the site redirects FROM, so a scheme, a path, a port or a userinfo part here would describe something that is not the domain the owner attached (D92, plan Task W2.2 step 1)", websiteFactsPath, secondary)
		default:
			if secondary == host {
				t.Errorf("%s: secondary_domain = %q and canonical_url's host are the same: a domain that redirects to itself is a loop, not a redirect (D92, plan Task W2.2 step 1)", websiteFactsPath, secondary)
			}
			if slices.Contains(facts.AllowedHosts, secondary) {
				t.Errorf("%s: secondary_domain = %q is on allowed_hosts %v: that list admits the hosts the site links to, and the site links nothing on the domain whose only job is to send the reader back to the canonical one (W1.11, plan Task W2.2 step 1)", websiteFactsPath, secondary, facts.AllowedHosts)
			}
		}
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

// websiteBuildDir is the site's build output, relative to the module root. The
// canonical-host scan below steps over it: a page under it carries the canonical
// origin because the build RENDERED it from website/data/site.yaml, which is
// what rendering means, and nothing there is committed or edited by hand
// (website/.gitignore). A built copy is a copy the build makes, not a second
// source of the fact.
const websiteBuildDir = "website/dist"

// websiteHostHit is one line of one file under website/ that names a host.
type websiteHostHit struct {
	file string
	line int
	text string
}

// websiteCanonicalHostHits is the canonical-host scan: every line under root's
// website/ that names host, with the build output stepped over (websiteBuildDir
// explains why). It is the whole mechanism, so the test that states the rule and
// the positive control that proves the mechanism sees a planted copy read the
// same code rather than two copies of it.
//
// The files come from websiteBoundaryFiles, so the scan cannot pass by walking
// an empty directory, and every file is read: a host in a lockfile, a document
// or a built page is a line of a file under website/ like any other.
func websiteCanonicalHostHits(t *testing.T, root, host string) []websiteHostHit {
	t.Helper()
	var hits []websiteHostHit
	for _, rel := range websiteBoundaryFiles(t, root) {
		if rel == websiteBuildDir || strings.HasPrefix(rel, websiteBuildDir+"/") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, host) {
				hits = append(hits, websiteHostHit{file: rel, line: i + 1, text: strings.TrimSpace(line)})
			}
		}
	}
	return hits
}

// TestWebsiteCanonicalURLAppearsOnlyInSiteYaml holds the site to D92's single
// source for its own name: the canonical origin is written in
// website/data/site.yaml and nowhere else under website/, so pointing the site
// at a domain is that one line and a rebuild, and so no page can go on serving a
// domain the site stopped being.
//
// The needle is the canonical HOST and not the whole URL: a page that wrote
// fugaro.dev with no scheme, or a template that supplies the scheme itself, is a
// second copy of the same fact, and that is the spelling a hard-coded link in a
// component would have.
//
// The scan reads every file under website/ that websiteWalk returns except the
// build output (websiteBuildDir). Documents under docs/ are out of scope on
// purpose: they are not the site, and this plan's own documents name the domain
// where they record the task that set it.
func TestWebsiteCanonicalURLAppearsOnlyInSiteYaml(t *testing.T) {
	root := consoleBoundaryRoot(t)
	facts, _ := readWebsiteFacts(t)
	host, found := strings.CutPrefix(facts.CanonicalURL, "https://")
	if !found || host == "" || strings.ContainsAny(host, "/?#") {
		t.Fatalf("%s: canonical_url = %q, want an absolute https origin: this scan reads the host out of it (TestWebsiteFactsMatchTheRepository holds the value's shape)", websiteFactsPath, facts.CanonicalURL)
	}

	hits := websiteCanonicalHostHits(t, root, host)

	// The hit that has to be there. Without it the scan is reading nothing, and
	// a scan that finds nothing passes for the wrong reason.
	if !slices.ContainsFunc(hits, func(h websiteHostHit) bool { return h.file == websiteFactsPath }) {
		t.Fatalf("no line of %s names the canonical host %q, so this scan is not reading the site's own facts: either canonical_url is not what this test read (%v), or the walk lost the file", websiteFactsPath, host, hits)
	}
	for _, h := range hits {
		if h.file == websiteFactsPath {
			continue
		}
		t.Errorf("%s:%d names the canonical host %q: the canonical URL has one source, %s, and every absolute link, the canonical <link>, the sitemap and the OpenGraph tag are built from it -- a copy here is a second source that keeps pointing at the old domain after the one line changes (D92, plan Task W2.2 step 1)\n%s", h.file, h.line, host, websiteFactsPath, h.text)
	}
}

// TestWebsiteCanonicalHostScanSeesAPlantedCopy is that scan's positive control:
// it plants the canonical host in a page, in the facts file and in a built page
// of a temp tree, and proves the scan reports the first two and steps over the
// third, so the rule above cannot pass by looking at nothing and cannot be
// tightened into a rule about the build by a later change to the walk.
func TestWebsiteCanonicalHostScanSeesAPlantedCopy(t *testing.T) {
	const host = "fugaro.dev"
	root := t.TempDir()
	planted := map[string]string{
		websiteFactsPath:                      "canonical_url: https://" + host + "\n",
		websiteInstallFactsPath:               "version: 1\n",
		"website/src/pages/index.astro":       "<a href=\"https://" + host + "/docs\">Docs</a>\n",
		"website/src/components/Footer.astro": "<span>fugaro</span>\n",
		websiteBuildDir + "/index.html":       "<link rel=\"canonical\" href=\"https://" + host + "/\">\n",
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

	seen := map[string]bool{}
	for _, h := range websiteCanonicalHostHits(t, root, host) {
		seen[h.file] = true
	}
	for _, want := range []string{websiteFactsPath, "website/src/pages/index.astro"} {
		if !seen[want] {
			t.Errorf("the scan did not see the planted canonical host in %s: a rule that finds nothing passes for the wrong reason", want)
		}
	}
	for _, dontWant := range []string{"website/src/components/Footer.astro", websiteBuildDir + "/index.html"} {
		if seen[dontWant] {
			t.Errorf("the scan reported %s: a file that does not name the host, or a built page the build rendered the origin into, is not a second source of the fact", dontWant)
		}
	}
}

// firebaseRedirect is one entry of firebase.json's hosting.redirects list, as
// firebase-tools' config schema defines it (schema/firebase-config.json,
// definitions/HostingRedirects: destination plus exactly one of source, glob and
// regex, and an optional type -- additionalProperties is false there, so a key
// this struct does not carry is a config firebase-tools refuses, not a field
// this test ignores).
type firebaseRedirect struct {
	Source      string `json:"source"`
	Glob        string `json:"glob"`
	Regex       string `json:"regex"`
	Destination string `json:"destination"`
	Type        int    `json:"type"`
}

// firebaseRedirectPath normalizes a redirect pattern or destination path for the
// comparison below: a splat or a capture placeholder is the wildcard it stands
// for ("/:splat", "/:1" and "/**" all become "/*"), and the root is that same
// wildcard, because Firebase's "/**" matches "/" as well. Two paths that
// normalize to the same string are the same path to every request that reaches
// either of them.
func firebaseRedirectPath(p string) string {
	for {
		i := strings.IndexByte(p, ':')
		if i < 0 {
			break
		}
		j := i + 1
		for j < len(p) && (p[j] == '_' || p[j] == '-' || p[j] >= '0' && p[j] <= '9' || p[j] >= 'a' && p[j] <= 'z' || p[j] >= 'A' && p[j] <= 'Z') {
			j++
		}
		p = p[:i] + "*" + p[j:]
	}
	p = strings.ReplaceAll(p, "**", "*")
	if p == "" || p == "/" {
		return "/*"
	}
	return p
}

// firebaseSelfRedirects returns the redirect rules of a firebase.json that
// answer a request for one of the site's own paths with a redirect to the site's
// own host -- the loop. own is the set of hosts the site itself serves (the
// canonical host and, when there is one, the secondary domain).
//
// Why this is a rule and not a preference: Firebase Hosting matches a redirect
// rule against the request URL PATH, and firebase-tools' schema for one carries
// no field that could condition it on the host (see firebaseRedirect), so a rule
// that sends every path to the canonical origin fires for requests that already
// arrive on the canonical origin: "https://fugaro.dev/faq" is answered with a
// 301 to "https://fugaro.dev/faq" and the browser reports a redirect loop. The
// redirect from a secondary domain to the canonical one is therefore a property
// of the attached domain (the Firebase console's own redirect option when the
// domain is added, or the Hosting API's custom-domain redirect), which is the
// owner's step and is recorded in website/README.md and in check 69a -- never a
// firebase.json entry. A rule whose destination is another origin, or one of the
// site's own paths that is a different path, is legitimate and is not reported.
//
// A rule that uses `regex` cannot be compared with a destination path here, so a
// regex rule pointing at one of the site's own hosts is reported rather than
// judged: whoever writes one extends this test with the rule they mean.
func firebaseSelfRedirects(t *testing.T, path string, config []byte, own []string) []string {
	t.Helper()
	var cfg struct {
		Hosting json.RawMessage `json:"hosting"`
	}
	if err := json.Unmarshal(config, &cfg); err != nil {
		t.Fatalf("%s is not JSON: %v", path, err)
	}
	if len(cfg.Hosting) == 0 {
		t.Fatalf("%s has no hosting object: the redirect rules live under it, so a scan that found none is not reading the site's Hosting config", path)
	}
	var hosting struct {
		Redirects []firebaseRedirect `json:"redirects"`
	}
	if err := json.Unmarshal(cfg.Hosting, &hosting); err != nil {
		t.Fatalf("%s: hosting is not an object with a redirects list: %v", path, err)
	}

	var loops []string
	for i, r := range hosting.Redirects {
		u, err := url.Parse(r.Destination)
		if err != nil {
			t.Errorf("%s: redirects[%d] destination %q does not parse: %v", path, i, r.Destination, err)
			continue
		}
		// An empty host is a destination on this site ("/new"), which is a
		// host the site serves like any other; a destination on another host
		// leaves the site and cannot loop.
		if u.Host != "" && !slices.Contains(own, u.Host) {
			continue
		}
		if r.Regex != "" {
			loops = append(loops, fmt.Sprintf("redirects[%d]: regex %q -> %s (a regex rule's match set is not compared here: extend this test if this one is meant)", i, r.Regex, r.Destination))
			continue
		}
		source := r.Source
		if source == "" {
			source = r.Glob
		}
		if firebaseRedirectPath(source) == firebaseRedirectPath(u.Path) {
			loops = append(loops, fmt.Sprintf("redirects[%d]: %q -> %s", i, source, r.Destination))
		}
	}
	return loops
}

// TestWebsiteFirebaseRedirectsDoNotLoop refuses the one redirect rule
// that would take the live site down: a rule that sends one of the site's own
// paths back to the site's own host. firebaseSelfRedirects carries the reasoning
// and the schema citation.
//
// This is the mechanical half of W2.2's answer about the redirect: the
// secondary domain's redirect is a domain-level setting and not a firebase.json
// entry, so the file has no redirects block today, and the rule this test states
// is what keeps a later task from adding the looping one in the belief that the
// domain-level step was already done. The positive control beside it plants the
// loop, so the test cannot pass by looking at an empty list.
func TestWebsiteFirebaseRedirectsDoNotLoop(t *testing.T) {
	root := consoleBoundaryRoot(t)
	facts, _ := readWebsiteFacts(t)
	own := []string{}
	if host, ok := strings.CutPrefix(facts.CanonicalURL, "https://"); ok && host != "" {
		own = append(own, host)
	}
	if facts.SecondaryDomain != "" {
		own = append(own, facts.SecondaryDomain)
	}
	if len(own) == 0 {
		t.Fatalf("%s: canonical_url = %q names no host, so this test would compare every destination against an empty list", websiteFactsPath, facts.CanonicalURL)
	}

	const path = "firebase.json"
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	for _, loop := range firebaseSelfRedirects(t, path, data, own) {
		t.Errorf("%s carries a redirect that answers a request on the site's own host with a redirect to the site's own host: %s. Hosting matches a rule against the path only and has no host condition, so this rule fires on the canonical domain too and loops. The secondary domain's redirect is a property of the attached domain (the console's own option, or the Hosting API's custom-domain redirect) and is recorded in website/README.md and in check 69a -- not here (plan Task W2.2 step 2)", path, loop)
	}
}

// TestWebsiteFirebaseRedirectGuardSeesAPlantedLoop is that guard's positive
// control: the looping rule it exists for, and the three shapes a legitimate
// rules list can have beside it -- a redirect to another origin, a redirect to
// another path on this site, and a relative destination -- so the guard cannot
// pass by looking at nothing and cannot become a rule against redirects.
func TestWebsiteFirebaseRedirectGuardSeesAPlantedLoop(t *testing.T) {
	const path = "firebase.json"
	own := []string{"fugaro.dev", "fugaro.ai"}
	planted := `{"hosting":{"public":"website/dist","redirects":[
		{"source":"/**","destination":"https://fugaro.dev/:splat","type":301},
		{"source":"/old","destination":"https://fugaro.dev/new","type":301},
		{"source":"/moved","destination":"/moved-here","type":302},
		{"source":"/releases","destination":"https://github.com/dimipaun/fugaro/releases","type":302}
	]}}`
	loops := firebaseSelfRedirects(t, path, []byte(planted), own)
	if len(loops) != 1 || !strings.Contains(loops[0], `/**`) || !strings.Contains(loops[0], "https://fugaro.dev/:splat") {
		t.Errorf("the guard reported %v, want exactly the planted `/**` -> https://fugaro.dev/:splat loop: a guard that misses it passes for the wrong reason, and one that also reports a redirect to another origin, another path or a relative destination is a rule against redirects", loops)
	}
}

// TestWebsiteChecklistHasCheck69aAnd69b pins W2.2's half of the website plan's check 69
// in docs/gcp-live-checklist.md: the two rows of the summary table and the two
// sections they point at, each of them the owner's to run against the live site
// and each naming its own pass and failure conditions. W2.3 owns 69c, the third
// live check of the same line of work, and writes it in the level after this
// one, so this test names only the two.
//
// The register's shape is the point of the test rather than decoration: a check
// that exists as a section but not as a row is a check nobody reads first, a row
// with no section is a command nobody can follow, and a check written as
// something an agent runs is a check that will be run by an agent -- the whole
// line of work is owner-run because a live site, a domain and a DNS record are
// not an agent's to touch (D92, D108).
func TestWebsiteChecklistHasCheck69aAnd69b(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)

	rows := map[string]string{}
	for _, line := range strings.Split(doc, "\n") {
		for _, n := range []string{"| 69a |", "| 69b |"} {
			if strings.HasPrefix(line, n) {
				if rows[n] != "" {
					t.Errorf("%s has more than one %q row: one check, one row", path, strings.Trim(n, "| "))
				}
				rows[n] = line
			}
		}
	}
	for _, n := range []string{"| 69a |", "| 69b |"} {
		row := rows[n]
		if row == "" {
			// Not a Fatalf: the sections below are checked either way, so the
			// failure names both halves of what is missing at once -- the row a
			// reader sees first, and the section it points at.
			t.Errorf("the summary table has no %q row: the check is in the register's index or it is not in the register", strings.Trim(n, "| "))
			continue
		}
		for _, want := range []string{"USER-RUN, NOT RUN", "run by you", "No agent runs any step", "See \"Check " + strings.Trim(n, "| ") + "\" below"} {
			if !strings.Contains(row, want) {
				t.Errorf("the %q row does not carry %q: %s", strings.Trim(n, "| "), want, row)
			}
		}
	}

	for _, c := range []struct {
		heading string
		wants   []string
	}{
		{
			heading: "## Check 69a:",
			wants: []string{
				"NOT RUN",
				"run by you",
				"No agent runs any step",
				"website/data/site.yaml",
				"secondary_domain",
				"301",
				"HTTPS",
				"features.console_downloads",
				"features.video",
				"runs hermetically",
				"FACT:",
				"Passes when:",
				"Fails when:",
				"Paste back:",
			},
		},
		{
			heading: "## Check 69b:",
			wants: []string{
				"NOT RUN",
				"run by you",
				"No agent runs any step",
				"allowed_hosts",
				"network panel",
				"font",
				"pixel",
				"before consent",
				"analytics",
				"FACT:",
				"Passes when:",
				"Fails when:",
				"Paste back:",
			},
		},
	} {
		section := docSection(t, path, doc, c.heading)
		for _, want := range c.wants {
			if !strings.Contains(section, want) {
				t.Errorf("%s does not carry %q: a live check says who runs it, what to run or look at, when it passes and what failing looks like (plan Task W2.2 step 4)", c.heading, want)
			}
		}
	}
}
