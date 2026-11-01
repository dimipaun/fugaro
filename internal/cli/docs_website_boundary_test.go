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
// links an unsigned or unreleased artifact: no file under website/ may carry
// this repository's release-asset URL, and the reason the rule holds changed
// with the console's release rather than the rule. While
// website/data/site.yaml's features.console_downloads is false, the console is
// unreleased and a versioned asset URL would 404 or point at an unsigned
// artifact; W3.1 flipped that flag when `console-v0.9.0` was published, and
// every URL the site offers is now composed at build time from
// website/content/downloads.json's release facts, so a URL written in a source
// file is a second source that drifts (the owner's changes spec of 2026-10-18,
// item 6; §7's L9 file row names this file). The test reads the flag and says
// which of the two it is refusing in.
//
// W3.1's second half is the release facts themselves, and it is the test after
// that one: while the console's downloads are on, the data file's release
// block has to be complete and offline-checkable -- the tag prefix the console
// workflow composes the release tag from, a strict version, one published name
// per asset (the name carrying the version), the SHA-256 the release recorded
// for each of them, and the CLI release note the console is published from.
//
// And a fifth pins the facts file itself to the repository it describes,
// because a site that copies a fact is a site that drifts from it (Global
// Constraint 6): the repository URL is go.mod's own module path, min_runs is
// internal/stats.MinRuns, the canonical URL's host is on the site's own host
// list, and every decision-backed value is in the state its decision left it
// in -- analytics' flag and measurement id agreeing with each other (D93; W3.2 sets both together), the console's downloads off
// (D95), the video off (D107), and NO second domain anywhere under website/:
// W2.2 wrote one for a domain the owner then ruled does not exist (the ruling
// of 2026-10-17, the amendment under plan Task W2.2), and the checks below hold
// the file and the site to the absence.
//
// W2.2 adds four checks to this file, all of them about the domain the site is
// served on: the canonical URL is a real host and is the ONE host of the site,
// named in site.yaml and nowhere else under website/
// (TestWebsiteCanonicalHostAppearsOnlyInSiteYaml), no file under website/ names
// the second domain the owner ruled out (TestWebsiteNamesNoSecondDomain --
// W2.2's value is gone and its absence is a pin), no firebase.json redirect
// rule sends one of the site's own paths to the site's own host
// (TestWebsiteFirebaseRedirectsDoNotLoop -- the rule a domain-level setting
// replaces, and a way to take the live site down), and the register carries the
// two owner-run live checks the plan reserves for this task
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
	"regexp"
	"slices"
	"strconv"
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
	// websiteDownloadsPath is the console's own facts (W1.6): what the console
	// is, the sentence the unreleased state renders, the properties its
	// decisions fix, and -- since W3.1 -- the release every download URL is
	// derived from and the SHA-256 the release recorded for each asset. The
	// site's build reads it through src/lib/console-downloads.mjs, which
	// refuses a shape this test would not expect; what is checked here is the
	// release the file describes.
	websiteDownloadsPath = "website/content/downloads.json"
	// websiteConsoleSumsPath is the release's own checksum list, copied
	// verbatim from the `console-sha256sums.txt` asset of the `console-vX.Y.Z`
	// release when the site's console downloads are turned on. It is the one
	// thing that makes a checksum checkable without the network: a well-formed
	// value can still be the wrong one, and the release's own file is what says
	// which value is right.
	websiteConsoleSumsPath = "website/data/console-sha256sums.txt"
	// websiteConsoleReleaseWorkflowPath is the workflow that builds, signs, notarises
	// and publishes the console as its own `console-vX.Y.Z` release (D11). It
	// is read, rather than quoted, because the tag prefix the site's download
	// URLs are built from is composed there (`CONSOLE_TAG=console-v$VERSION`
	// from the CLI's own vX.Y.Z tag), and a prefix written down in two places
	// is the copy that drifts.
	websiteConsoleReleaseWorkflowPath = ".github/workflows/console-release.yml"
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
// read it is a page whose value nothing checks. That refusal is also this
// file's first pin on the shape W2.2 wrote and the owner struck: a
// `secondary_domain` key in site.yaml is a field this struct does not carry, so
// it is a decode failure here as well as a named failure in
// TestWebsiteFactsMatchTheRepository, which says what the ruling was.
type websiteFactsFile struct {
	Version int `yaml:"version"`
	// CanonicalURL is the one absolute URL every other absolute link, the
	// sitemap, the canonical <link> and the OpenGraph tag are built from
	// (W1.3). It was a reserved placeholder until W2.2 replaced it with the
	// domain the owner registered and connected Firebase Hosting to; the
	// registration, the Firebase custom domain and the DNS records are the
	// owner's steps rather than an agent's (D92).
	CanonicalURL string `yaml:"canonical_url"`
	// Repository is the repository every GitHub link on the site is built
	// from (W1.7), pinned below to go.mod's own module path.
	Repository string                `yaml:"repository"`
	Analytics  websiteFactsAnalytics `yaml:"analytics"`
	// Marketing is the campaign measurement's configuration (plan Task G2.3,
	// D401): the pixel's id, the insight tag's partner id and the origin each
	// of the three loaders fetches its script from, read on the four campaign
	// pages by the same consent banner under a second question.
	Marketing websiteFactsMarketing `yaml:"marketing"`
	Features  websiteFactsFeatures  `yaml:"features"`
	// MinRuns is D97's N: no figure the site publishes is drawn from fewer
	// runs than this, and it is the aggregation's own minimum rather than a
	// second number written down here (W0.3, internal/stats.MinRuns).
	MinRuns int `yaml:"min_runs"`
	// AllowedHosts is the host list the site's link and origin checks enforce
	// (W1.11): a link to a host that is not on it fails the build. It is the
	// hosts the site actually links -- github.com, raw.githubusercontent.com,
	// and firebase.google.com for the Firebase privacy policy the privacy
	// statement links rather than restating -- plus the site's own canonical
	// origin, and nothing else, because every entry is a promise that a
	// reader's browser may talk to that host.
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

// websiteFactsMarketing is the campaign measurement's configuration (plan Task
// G2.3, D401): the ids and the origins the pixel, the insight tag and the
// booking widget load from, on the four campaign pages only, behind the
// consent banner's second question.
type websiteFactsMarketing struct {
	// MetaPixelID and LinkedinPartnerID are the pixel's and the insight tag's
	// identifiers. Both are the owner's, supplied on 2026-10-31, and the pin
	// below refuses either of them coming back empty: an id invented here would
	// send a campaign page's visitors into a stranger's ad account, and an empty
	// id is a loader switched off rather than a loader that loads something
	// nameless (R3).
	MetaPixelID       string `yaml:"meta_pixel_id"`
	LinkedinPartnerID string `yaml:"linkedin_partner_id"`
	// The three origins, each the exact host the vendor documents rather than
	// a scheme-wide or wildcard entry: the plan pins the narrowest origin each
	// one needs (O3), and the deployed policy in firebase.json is held to this
	// exact list.
	MetaOrigin     string `yaml:"meta_origin"`
	LinkedinOrigin string `yaml:"linkedin_origin"`
	CalendlyOrigin string `yaml:"calendly_origin"`
}

// websiteFactsFeatures are the features the site renders only when they exist
// (findings 23-25 and 57, D95 and D107).
type websiteFactsFeatures struct {
	// ConsoleDownloads is true since the console's first release shipped
	// (`console-v0.9.0`, 2026-10-19): its section renders the release's own
	// downloads, and the pair of facts -- this flag and the data file's
	// `available` -- is what the console-link rule and the release-facts check
	// read (Global Constraint 9, D95, W3.1).
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

// websiteDownloadsFile is website/content/downloads.json, decoded the way the
// site's own reader decodes it: the fields a check below reads, and nothing
// else. The site's reader is stricter than this struct (it refuses an unknown
// key, a missing one and a malformed value, and it is what the build runs), so
// what is here is the release the URLs are derived from rather than a second
// validation of the file's shape.
type websiteDownloadsFile struct {
	// Available is the console's own flag: the section renders the downloads
	// from it, and it has to agree with site.yaml's features.console_downloads
	// (W1.6 step 2).
	Available bool `json:"available"`
	Release   struct {
		// TagPrefix is what the release's tag is composed from: `console-v`,
		// the prefix the console's own workflow publishes under (D11).
		TagPrefix string `json:"tag_prefix"`
		// Version is the CLI version the console was built from, and the
		// version in every published asset name.
		Version string `json:"version"`
	} `json:"release"`
	Downloads []struct {
		// ID names the entry within the file (`macos-dmg`).
		ID string `json:"id"`
		// Platform is the label's first half (`macOS`), and the reason the
		// site claims no platform the release does not carry.
		Platform string `json:"platform"`
		// File is the release's PUBLISHED name, with `{version}` where the
		// version goes -- the URL's last segment, not a URL.
		File string `json:"file"`
		// SHA256 is the checksum the release recorded for those bytes.
		SHA256 string `json:"sha256"`
	} `json:"downloads"`
}

// readWebsiteDownloads reads website/content/downloads.json, the console's own
// facts, and fails with the path named rather than with a decoder's offset.
func readWebsiteDownloads(t *testing.T) websiteDownloadsFile {
	t.Helper()
	data, err := os.ReadFile("../../" + websiteDownloadsPath)
	if err != nil {
		t.Fatalf("open %s: %v", websiteDownloadsPath, err)
	}
	var f websiteDownloadsFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("%s: %v", websiteDownloadsPath, err)
	}
	return f
}

// consoleReleaseTagPrefix is the tag prefix the console's release is published
// under, read out of the workflow that publishes it: the line that composes
// the console's own tag from the CLI's (`CONSOLE_TAG="console-v$VERSION"`),
// with the version's own `$VERSION` reference cut off. It fails rather than
// returning a default, because a prefix this check cannot find is a prefix the
// site's URLs are not held to at all.
func consoleReleaseTagPrefix(t *testing.T) string {
	t.Helper()
	const marker = `CONSOLE_TAG="`
	for _, line := range strings.Split(readDoc(t, websiteConsoleReleaseWorkflowPath), "\n") {
		i := strings.Index(line, marker)
		if i < 0 {
			continue
		}
		rest := line[i+len(marker):]
		j := strings.Index(rest, "$VERSION")
		if j <= 0 {
			continue
		}
		return rest[:j]
	}
	t.Fatalf("%s carries no %s...$VERSION line, so the prefix the console's release tag is composed from cannot be read: the site's download URLs are built from that tag, and this check would otherwise hold them to nothing", websiteConsoleReleaseWorkflowPath, marker)
	return ""
}

// consoleReleaseSums reads the release's own checksum list -- the
// `console-sha256sums.txt` asset of the console's release, copied verbatim into
// the tree -- and returns it as published name -> checksum.
//
// The file's shape is checked rather than tolerated: one `<64 hex>  <name>` line
// per asset, which is what `sha256sum` writes and `shasum -a 256 -c` reads. A
// line this cannot read is a failure and not a skip, because a skipped line is
// an asset the cross-check in the caller silently stops covering, and a file
// with no line at all would make every check against it vacuous.
func consoleReleaseSums(t *testing.T) map[string]string {
	t.Helper()
	body := readDoc(t, websiteConsoleSumsPath)
	sums := map[string]string{}
	for i, line := range strings.Split(body, "\n") {
		if line == "" {
			continue
		}
		m := consoleSumsLine.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("%s line %d is %q, and the file the release publishes is one \"<64 hex>  <name>\" line per asset -- the shape sha256sum writes and shasum -a 256 -c reads. A line this cannot read is an asset the checks below would not cover", websiteConsoleSumsPath, i+1, line)
		}
		if _, dup := sums[m[2]]; dup {
			t.Fatalf("%s names %q twice (line %d), and one published name has one checksum", websiteConsoleSumsPath, m[2], i+1)
		}
		sums[m[2]] = m[1]
	}
	if len(sums) == 0 {
		t.Fatalf("%s carries no checksum line, so every value in %s would be checked against nothing", websiteConsoleSumsPath, websiteDownloadsPath)
	}
	return sums
}

// consoleSumsLine is one line of a sha256sum file: the checksum, the two spaces
// sha256sum writes between the fields, and the file name.
var consoleSumsLine = regexp.MustCompile(`^([0-9a-f]{64})  (.+)$`)

// strictVersion reports whether a version is a strict X.Y.Z, the only shape the
// console's workflow publishes from and the shape the published asset names
// carry.
func strictVersion(v string) bool {
	return strictVersionPattern.MatchString(v)
}

// strictVersionPattern is a version as the console's workflow writes one: three
// dot-separated numbers and nothing else (`.github/workflows/console-release.yml`
// refuses any tag that is not a strict vX.Y.Z). It is also what a published
// asset name must not carry beside its `{version}` placeholder.
var strictVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// versionToken is any X.Y.Z-shaped run of characters, which is what a published
// asset name carries where the release's version goes.
var versionToken = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)

// sha256Hex reports whether a value is a SHA-256 as the release's own sums file
// writes it: 64 lowercase hexadecimal characters. A well-formed value is not
// evidence the checksum is the published bytes' -- only the release's
// console-sha256sums.txt is -- but a value that is not this shape is a
// checksum no reader can check anything against.
func isSHA256Hex(v string) bool {
	return sha256HexPattern.MatchString(v)
}

// sha256HexPattern is a SHA-256 as the release's own sums file writes one.
var sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

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
// under one of them can never resolve, so it is the shape a placeholder
// canonical_url had before the site had a domain -- W2.2 replaced that
// placeholder with the registered domain, and this function is what refuses the
// placeholder coming back: a canonical_url on a reserved name is a claim that
// the site is served somewhere it never will be.
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
		"website/data/console-sha256sums.txt",
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
// mechanical: the site never links an unsigned or unreleased artifact, and it
// never keeps a second copy of a link it derives. The console is designed,
// shipped (`console-v0.9.0`) and released through a flag, so the scan below
// holds in BOTH states of that flag and the failure it prints says which state
// it is refusing in:
//
//   - while website/data/site.yaml's features.console_downloads is false, this
//     repository's release-asset URL anywhere under website/ is a link to an
//     asset that does not exist -- the console is not released, and a link
//     here would either 404 or point at an unsigned artifact (finding 25, D95,
//     Global Constraint 9);
//   - while the flag is true, the site does offer those URLs, and every one of
//     them is composed at build time by `src/lib/console-downloads.mjs` from
//     website/content/downloads.json's release facts and site.yaml's
//     repository, so a URL WRITTEN in a file under website/ is a second source
//     of a fact the build already derives -- the copy that drifts first, and
//     the one a version bump would leave behind (the owner's changes spec of
//     2026-10-18, item 6; Global Constraint 6).
//
// The flag is the site's own, so this is a scan of the site rather than of the
// Go tree, and it is deliberately blunt: every file under website/, whatever
// its name, is read.
//
// The URL to look for is built from the facts file's own `repository`, so the
// rule follows the repository the site links to, and the field's pin (in
// TestWebsiteFactsMatchTheRepository) keeps that value honest.
func TestWebsiteLinksNoConsoleWhileTheFlagIsOff(t *testing.T) {
	root := consoleBoundaryRoot(t)
	facts, _ := readWebsiteFacts(t)

	// The URL the site may not carry, built from the facts file's own
	// repository rather than typed here, so the rule follows the repository the
	// site links to (and the pin in TestWebsiteFactsMatchTheRepository keeps
	// that value honest).
	download := websiteDownloadNeedle(t, facts.Repository)

	for _, rel := range websiteBoundaryFiles(t, root) {
		// The build output is stepped over, for the reason the canonical-host
		// scan steps over it: a page under website/dist/ carries these URLs
		// because the build RENDERED them from website/content/downloads.json
		// and site.yaml, which is what deriving a URL means -- and nothing
		// there is committed or edited by hand (website/.gitignore). The rule
		// above is about the files a person writes, and every one of those is
		// read.
		if rel == websiteBuildDir || strings.HasPrefix(rel, websiteBuildDir+"/") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, download) {
				continue
			}
			if facts.Features.ConsoleDownloads {
				t.Errorf("%s:%d carries the console download URL %q while features.console_downloads is true: the console's downloads are on, and every URL the site offers for them is composed at build time from website/content/downloads.json's release facts and %s's repository by src/lib/console-downloads.mjs. A URL written here is a second source of that fact -- the copy a version bump leaves behind, pointing at the previous release's asset (the owner's changes spec of 2026-10-18, item 6; Global Constraint 6)\n%s", rel, i+1, download, websiteFactsPath, line)
				continue
			}
			t.Errorf("%s:%d carries the console download URL %q while features.console_downloads is false: the console is not released, the site links the Releases page and never a versioned asset URL, and a link here would either 404 or point at an unsigned artifact (finding 25, D95, Global Constraint 9)\n%s", rel, i+1, download, line)
		}
	}
}

// TestWebsiteConsoleReleaseFactsAreComplete is W3.1's other half: the console's
// downloads are on, and the facts they are rendered and derived from are
// complete, offline-checkable, and about the release that exists.
//
// The pair is one fact. site.yaml's features.console_downloads is what the
// repository-wide rule above reads; website/content/downloads.json's
// `available` is what the console page's section renders from. A build that
// rendered one state while the other said the opposite is the drift the
// placeholder existed to prevent, and the site's own build refuses it
// (`src/lib/console-downloads.mjs`) -- so the two are checked to agree here as
// well, where a Go run that never builds the site can see it.
//
// Everything else below is the release the URLs are derived from, and every
// check is one an offline run can make:
//
//   - the tag prefix is the console workflow's own, read out of
//     .github/workflows/console-release.yml rather than typed here, because
//     that file is what composes the release tag (`CONSOLE_TAG=console-v$VERSION`
//     from the CLI's vX.Y.Z tag) and a second spelling of it here would be the
//     copy that drifts (D11, Global Constraint 6);
//   - the version is a strict X.Y.Z, which is what the workflow refuses to
//     publish anything but;
//   - the CLI's own release note for that version exists, docs/releases/vX.Y.Z.md
//     -- the console is published from the CLI's vX.Y.Z tag (D11), so a
//     version with no release note is a console the site would offer a download
//     for and no release behind it. That file is also the offline record of the
//     tag, which is what a check can hold without a network or a git fetch;
//   - every asset's published name carries the version, because a name for
//     another version is a URL that 404s (the name is what the URL's last
//     segment is built from), and no two assets carry the same SHA-256, because
//     two different bytes with one checksum is a checksum that says nothing
//     about the file a reader has.
//
// What it cannot check offline, and says so rather than implying otherwise: that
// a well-formed SHA-256 is the checksum of the PUBLISHED bytes. That is a fact
// about bytes the release holds, and the release's own console-sha256sums.txt is
// its source; the owner's check is against it, and the data file's values were
// read from it when this release was turned on.
func TestWebsiteConsoleReleaseFactsAreComplete(t *testing.T) {
	facts, _ := readWebsiteFacts(t)
	downloads := readWebsiteDownloads(t)

	if downloads.Available != facts.Features.ConsoleDownloads {
		t.Errorf("%s: available = %t and %s's features.console_downloads = %t: the two are one fact -- the flag is what the repository-wide console-link rule reads and `available` is what the console page renders from -- and the site's own build refuses them disagreeing (W1.6 step 2, W3.1, D95)", websiteDownloadsPath, downloads.Available, websiteFactsPath, facts.Features.ConsoleDownloads)
	}

	wantPrefix := consoleReleaseTagPrefix(t)
	if downloads.Release.TagPrefix != wantPrefix {
		t.Errorf("%s: release.tag_prefix = %q, want %q, the prefix .github/workflows/console-release.yml composes the console's release tag from (CONSOLE_TAG=console-v$VERSION, from the CLI's own vX.Y.Z tag): every download URL the site builds is `<repository>/releases/download/<prefix><version>/<name>`, so a prefix that is not the workflow's names a tag no run creates (D11)", websiteDownloadsPath, downloads.Release.TagPrefix, wantPrefix)
	}
	if !strictVersion(downloads.Release.Version) {
		t.Errorf("%s: release.version = %q, want a strict X.Y.Z: the workflow refuses to publish a console from any other tag, and the version is what the published asset names carry", websiteDownloadsPath, downloads.Release.Version)
	}

	if len(downloads.Downloads) == 0 {
		t.Fatalf("%s: the downloads list is empty: the console's downloads are %t, and a site that offers a release with no asset in it offers nothing (W1.6, W3.1)", websiteDownloadsPath, downloads.Available)
	}

	// The release's own checksums, which is what makes each value below checkable: a
	// well-formed SHA-256 can still be the wrong one, and no build can compute the
	// right one from anything in this tree. The file is the release's, copied
	// verbatim, so a value that disagrees with it is a checksum that fails for every
	// reader who checks their download (W3.1).
	sums := consoleReleaseSums(t)

	seen := map[string]string{}
	for i, d := range downloads.Downloads {
		if !strings.Contains(d.File, "{version}") {
			t.Errorf("%s: downloads[%d].file = %q, want the release's published name with {version} where the version goes: the name is the URL's last segment, and one for another version is a download that 404s", websiteDownloadsPath, i, d.File)
		}
		// The name is a template, so the version reaches it by substitution
		// rather than literally -- and the check that matters is that the name
		// carries NO OTHER version: a name with a version written into it
		// beside the placeholder is a name for the release that version was,
		// which is the URL a version bump leaves behind (D11).
		if other := versionToken.FindString(strings.ReplaceAll(d.File, "{version}", "")); other != "" {
			t.Errorf("%s: downloads[%d].file = %q carries the version %q as well as {version}: the placeholder is where the release's own version goes, so a version written into the name is a name for another release -- the download a version bump leaves pointing at the previous asset (D11)", websiteDownloadsPath, i, d.File, other)
		}
		if !isSHA256Hex(d.SHA256) {
			t.Errorf("%s: downloads[%d].sha256 = %q, want 64 lowercase hex characters: the value is printed beside the link for a reader to check their download against, and anything else is not a SHA-256 (D183, W3.1)", websiteDownloadsPath, i, d.SHA256)
		}
		if first, dup := seen[d.SHA256]; dup {
			t.Errorf("%s: downloads[%d] and downloads[%s] carry the same sha256 %q: two different bytes cannot share one checksum, so at least one of the two is not the file the release published (W3.1)", websiteDownloadsPath, i, first, d.SHA256)
		}
		seen[d.SHA256] = strconv.Itoa(i)

		// And the entry against the release's own record of it: the published name
		// the entry resolves to, and the checksum the release published for it.
		published := strings.ReplaceAll(d.File, "{version}", downloads.Release.Version)
		recorded, ok := sums[published]
		if !ok {
			t.Errorf("%s: downloads[%d] names %q, which %s does not: the file is the published name of an asset of the %s%s release, and a name the release does not carry is a download that 404s (D11, W3.1)", websiteDownloadsPath, i, published, websiteConsoleSumsPath, downloads.Release.TagPrefix, downloads.Release.Version)
			continue
		}
		if recorded != d.SHA256 {
			t.Errorf("%s: downloads[%d].sha256 = %q and %s records %q for %q: the release's own file is what the checksum is checked against -- a well-formed value that is not the published one is a checksum that fails for every reader who checks their download (W3.1)", websiteDownloadsPath, i, d.SHA256, websiteConsoleSumsPath, recorded, published)
		}
	}

	// And the release's sums file covers nothing else: an asset the release carries
	// and the site does not offer is a page a reader cannot get a download from,
	// which is the other half of the same fact. The one exception is the
	// console's update feed: the release job sums latest-mac.yml into this file
	// so the one cosign signature covers it, but electron-updater fetches it and
	// no reader downloads it. Leaving its line in keeps the file byte for byte
	// the signed one.
	for name := range sums {
		if name == consoleFeedFileName {
			continue
		}
		found := false
		for _, d := range downloads.Downloads {
			if strings.ReplaceAll(d.File, "{version}", downloads.Release.Version) == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s names %q, which %s offers no download for: the site offers the release's assets, and a reader who wants the one it leaves out has nowhere to get it (W1.6, W3.1)", websiteConsoleSumsPath, name, websiteDownloadsPath)
		}
	}

	// The console release whose assets these are: its own notes file is in the
	// tree, and it names the console's version. This is the offline half of "the
	// tag exists" -- a git tag cannot be checked without a fetch, and a shallow
	// CI checkout has none (D11, W3.1). Since the console's own release stream
	// (the stream's README under docs/releases/, the owner's addendum of
	// 2026-10-22) the console has its own tag, version line and notes, and its
	// workflow reads its notes file from the tagged commit and refuses to build
	// without it -- so that file is where this repository records that the
	// version the site offers a download for was released.
	//
	// The directory is composed rather than written whole because this file may
	// carry no path into the console directory: the console boundary walk refuses
	// one outside its allow list, and a literal notes path is the spelling that
	// walk reads (TestConsoleIsOutsideTheGoModule, plan Task 0.3 a3).
	note := "docs/releases/" + consoleDirName + "/v" + downloads.Release.Version + ".md"
	body := readDoc(t, note)
	if !strings.Contains(body, "Fugaro Console "+downloads.Release.Version) {
		t.Errorf("%s does not name %q: the console's own release is cut with its notes file, which the workflow reads from the tagged commit and passes as the release body, so the notes for that version are where the console's release is recorded -- and the site offers a download for it (docs/releases/, the console stream's README, W3.1)", note, "Fugaro Console "+downloads.Release.Version)
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
//   - no second domain: W2.2 set a `secondary_domain` for a domain the owner
//     had acquired alongside the canonical one, and the owner ruled on
//     2026-10-17 that no such domain is theirs, so the key is gone from the
//     file, the struct above refuses it coming back, and the failure below
//     names the ruling. TestWebsiteNamesNoSecondDomain holds the rest of the
//     site to the same absence;
//   - analytics: the flag and the measurement id agree with each other -- on
//     with a property, or off without one -- so W3.2 sets both when it turns
//     the measurement on (D93) and does not have to edit this file at all;
//   - features: the console's downloads agree with the data file that renders
//     them, and the video is off. W3.1 flipped the first when the console's
//     first release was published (2026-10-19), and relaxed its own pin in the
//     same commit; W2.4 flips the second when the video exists, in the commit
//     that relaxes its check;
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

	// There is no second domain, and this is the pin that says so even though
	// the struct above already refuses the key: a decode failure names the
	// field, not the ruling, and a later task that reintroduces the key (or
	// reaches for it in a page) has to meet the ruling rather than a parse
	// error. The value W2.2 wrote is not printed here either -- the scan in
	// TestWebsiteNamesNoSecondDomain reads this file too.
	if strings.Contains(raw, "secondary_domain") {
		t.Errorf("%s carries a secondary_domain key: the owner ruled on 2026-10-17 that no second domain is theirs, so the site has ONE domain, the canonical_url above, and nothing redirects to or from another. W2.2 wrote the key and this pin, and the amendment under plan Task W2.2 records the ruling (D92 as amended)", websiteFactsPath)
	}

	// Analytics: the two values have to agree, and that is the invariant worth
	// pinning -- W3.2 turns the measurement on by setting the property id and
	// the flag together, so it meets no red test here, while a flag turned on
	// with nowhere to report to is refused wherever it is done.
	if facts.Analytics.Enabled != (facts.Analytics.MeasurementID != "") {
		t.Errorf("%s: analytics is %+v, and its two values must agree: analytics that report nowhere and a property id nothing reports to are both broken states, and W3.2 sets both together when it turns the measurement on behind W1.12's consent gate (D93, plan Task W3.2)", websiteFactsPath, facts.Analytics)
	}

	// The console's downloads were pinned OFF here until W3.1, which flipped
	// the flag with the console's first release (`console-v0.9.0`, published
	// 2026-10-19). The pin that replaces it is not "the flag is on": it is
	// that the flag and the data file agree, which is the invariant worth
	// holding and the one the site's own build refuses to break (W1.6 step 2).
	// What the flag being ON then means -- a complete release block, the
	// workflow's own tag prefix, a version with a release note, one published
	// name per asset and a SHA-256 for each -- is
	// TestWebsiteConsoleReleaseFactsAreComplete, which runs in both states of
	// the flag because the data file carries the release's names from the day
	// they are known.
	if facts.Features.ConsoleDownloads != readWebsiteDownloads(t).Available {
		available := readWebsiteDownloads(t).Available
		t.Errorf("%s: features.console_downloads = %t and %s's available = %t, and the two are one fact: the flag is what TestWebsiteLinksNoConsoleWhileTheFlagIsOff reads over the whole of %s/, and `available` is what the console page's section renders from, so the site's build refuses them disagreeing (W1.6 step 2, W3.1, D95)", websiteFactsPath, facts.Features.ConsoleDownloads, websiteDownloadsPath, available, websiteDirName)
	}
	if facts.Features.Video {
		t.Errorf("%s: features.video is true: the explainer video does not exist, so the hero may not render a player, an iframe, a poster or a play button. W2.4 sets this flag when the video exists, in the same commit that relaxes this check (D107, finding 57, plan Task W2.4 step 1)", websiteFactsPath)
	}

	// The campaign measurement (plan Task G2.3, D401; the owner's rulings of
	// 2026-10-31). G2.3 pinned BOTH ids empty and said the pin is relaxed in
	// the commit that fills one in, because a task that fills one in is a task
	// the owner supplied the id for (R3) -- exactly as the video pin above is
	// relaxed by W2.4. The owner supplied the insight tag's partner id and then
	// the pixel's id, so both halves are now the other way round: an empty value
	// is refused, because the four campaign pages load a tag only while one is
	// there and website/test/consent.mjs reads that off the built artifact. What
	// the pin still refuses is an empty file, not a value it happens to hold.
	// Neither value is printed here: the id lives in site.yaml and nowhere
	// else, and a copy in this file would be the second one to keep in step.
	if facts.Marketing.MetaPixelID == "" {
		t.Errorf("%s: marketing carries an empty meta_pixel_id: the OWNER supplied the pixel id on 2026-10-31, the four campaign pages hand it to the pixel after the campaign consent, and an empty value is the loader switched off (R3, plan Task G2.3)", websiteFactsPath)
	}
	if facts.Marketing.LinkedinPartnerID == "" {
		t.Errorf("%s: marketing carries an empty linkedin_partner_id: the OWNER supplied the partner id on 2026-10-31, the four campaign pages hand it to the insight tag after the campaign consent, and an empty value is the loader switched off (R3, plan Task G2.3)", websiteFactsPath)
	}
	// The three origins are exact origins: scheme, host, no path, no wildcard.
	// A wildcard is the one shape the plan refuses outright in `script-src` and
	// requires the owner's word for anywhere else (O3), and the deployed policy
	// in firebase.json is held to these exact three values by
	// website/test/origins.mjs.
	for name, origin := range map[string]string{
		"meta_origin":     facts.Marketing.MetaOrigin,
		"linkedin_origin": facts.Marketing.LinkedinOrigin,
		"calendly_origin": facts.Marketing.CalendlyOrigin,
	} {
		switch {
		case !strings.HasPrefix(origin, "https://"):
			t.Errorf("%s: marketing.%s = %q, want an absolute https origin: the three loaders fetch their scripts over TLS from the vendor's own host, and the deployed policy admits that origin and nothing wider (O3, plan Task G2.3)", websiteFactsPath, name, origin)
		case strings.ContainsAny(strings.TrimPrefix(origin, "https://"), "/?#*"):
			t.Errorf("%s: marketing.%s = %q, want a bare origin -- no path, no query and no wildcard: the campaign's origins are admitted by the consent check rather than by allowed_hosts, and a wildcard here would be a promise about every subdomain of a vendor's domain (O3, D401, plan Task G2.3)", websiteFactsPath, name, origin)
		}
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

// websiteHostHits is the host scan: every line under root's website/ that names
// host. It is the whole mechanism, so the tests that state a rule and the
// positive control that proves the mechanism sees a planted copy read the same
// code rather than two copies of it. The files come from
// websiteBoundaryFiles, so the scan cannot pass by walking an empty directory,
// and every file is read: a host in a lockfile, a document or a built page is a
// line of a file under website/ like any other.
//
// skipBuild steps over the build output (websiteBuildDir), and the two rules
// below disagree about it for a reason that is each rule's own. The canonical
// host's: a built page carries the origin because the build RENDERED it from
// website/data/site.yaml, which is what rendering means, and nothing there is
// committed or edited by hand. The second domain's: nothing under website/
// names it and no page renders it, so a built page carrying it is either a
// build of a tree that named it in a page -- which is exactly what that rule
// exists for -- or of a revision that did, and both are worth failing on.
func websiteHostHits(t *testing.T, root, host string, skipBuild bool) []websiteHostHit {
	t.Helper()
	var hits []websiteHostHit
	for _, rel := range websiteBoundaryFiles(t, root) {
		if skipBuild && (rel == websiteBuildDir || strings.HasPrefix(rel, websiteBuildDir+"/")) {
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

// TestWebsiteCanonicalHostAppearsOnlyInSiteYaml holds the site to D92's single
// source for its own name: the canonical origin is written in
// website/data/site.yaml and nowhere else under website/, so pointing the site
// at a domain is that one line and a rebuild, and so no page can go on serving
// a domain the site stopped being.
//
// The needle is the host and not the whole URL: a page that wrote the domain
// with no scheme, or a template that supplies the scheme itself, is a second
// copy of the same fact, and that is the spelling a hard-coded link in a
// component would have.
//
// The scan reads every file under website/ that websiteWalk returns except the
// build output (websiteBuildDir, and the reason is the skip's own comment).
// Documents under docs/ are out of scope on purpose: they are not the site, and
// this plan's own documents name the domain where they record the task that set
// it.
func TestWebsiteCanonicalHostAppearsOnlyInSiteYaml(t *testing.T) {
	root := consoleBoundaryRoot(t)
	facts, _ := readWebsiteFacts(t)
	host, found := strings.CutPrefix(facts.CanonicalURL, "https://")
	if !found || host == "" || strings.ContainsAny(host, "/?#") {
		t.Fatalf("%s: canonical_url = %q, want an absolute https origin: this scan reads the host out of it (TestWebsiteFactsMatchTheRepository holds the value's shape)", websiteFactsPath, facts.CanonicalURL)
	}

	hits := websiteHostHits(t, root, host, true)

	// The hit that has to be there. Without it the scan is reading nothing, and
	// a scan that finds nothing passes for the wrong reason.
	if !slices.ContainsFunc(hits, func(x websiteHostHit) bool { return x.file == websiteFactsPath }) {
		t.Fatalf("no line of %s names the host %q, so this scan is not reading the site's own facts: either the value is not what this test read (%v), or the walk lost the file", websiteFactsPath, host, hits)
	}
	for _, hit := range hits {
		if hit.file == websiteFactsPath {
			continue
		}
		// The Subscription Terms are generated from a document under docs/, and
		// three clauses name the Licensor's own address: 29.1 where they are
		// published, the privacy notice's URL and the notice email. That is the
		// agreement's wording, which the page may not change.
		if hit.file == "website/content/subscription-terms.md" &&
			(strings.Contains(hit.text, "29.1 ") || strings.Contains(hit.text, "privacy notice at") || strings.Contains(hit.text, "Form (for the Licensor")) {
			continue
		}
		t.Errorf("%s:%d names the site's host %q: the canonical URL has one source, %s, and every absolute link, the canonical <link>, the sitemap and the OpenGraph tag are built from it -- a copy here is a second source that keeps pointing at the old domain after the one line changes (D92, plan Task W2.2 step 1)\n%s", hit.file, hit.line, host, websiteFactsPath, hit.text)
	}
}

// TestWebsiteHostScanSeesAPlantedCopy is that scan's positive control: it plants
// a host in a page, in the facts file and in a built page of a temp tree, and
// proves the scan reports the first two and steps over the third, so the rule
// above cannot pass by looking at nothing and cannot be tightened into a rule
// about the build by a later change to the walk.
func TestWebsiteHostScanSeesAPlantedCopy(t *testing.T) {
	const canonical = "fugaro.dev"
	root := t.TempDir()
	planted := map[string]string{
		websiteFactsPath:                      "canonical_url: https://" + canonical + "\n",
		websiteInstallFactsPath:               "version: 1\n",
		"website/src/pages/index.astro":       "<a href=\"https://" + canonical + "/docs\">Docs</a>\n",
		"website/src/components/Footer.astro": "<span>fugaro</span>\n",
		websiteBuildDir + "/index.html":       "<link rel=\"canonical\" href=\"https://" + canonical + "/\">\n",
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
	for _, hit := range websiteHostHits(t, root, canonical, true) {
		seen[hit.file] = true
	}
	for _, want := range []string{websiteFactsPath, "website/src/pages/index.astro"} {
		if !seen[want] {
			t.Errorf("the scan for %q did not see the planted host in %s: a rule that finds nothing passes for the wrong reason", canonical, want)
		}
	}
	for _, dontWant := range []string{"website/src/components/Footer.astro", websiteBuildDir + "/index.html"} {
		if seen[dontWant] {
			t.Errorf("the scan for %q reported %s: a file that does not name the host, or a built page the build rendered the origin into, is not a second source of the fact", canonical, dontWant)
		}
	}
}

// websiteSecondDomain is the host W2.2 wrote into website/data/site.yaml as the
// site's second domain, and the one the owner struck. The owner does not own it
// (the ruling of 2026-10-17, recorded in the amendment under plan Task W2.2), so
// there is no second domain: the site is served on one host, nothing redirects,
// and no file under website/ may name this one. It is written here as the
// absence scan's needle, because it is the spelling a page would carry if a
// later task copied an older revision of one -- a scheme-less host in a
// template, or a whole URL in a link -- and a scan for the canonical host alone
// would not see it.
const websiteSecondDomain = "fugaro.ai"

// TestWebsiteNamesNoSecondDomain holds the site to the owner's ruling: there is
// ONE domain, and the second one W2.2 wrote does not exist. Every file under
// website/ is read -- the facts file, every page and component, the lockfile, a
// build output if one is there -- and a line naming the struck host is a failure
// wherever it is, because the site's own facts, its pages and its documents must
// not describe a domain the owner never owned.
//
// It is not the reverse of the scan above and it is not redundant with it: that
// one pins where the canonical host IS written, this one pins that the struck
// host is written NOWHERE, and the two are different rules about different
// hosts. `secondary_domain`'s key is refused separately, by the decode in
// readWebsiteFacts (the struct carries no field for it) and by name in
// TestWebsiteFactsMatchTheRepository, which prints the ruling.
func TestWebsiteNamesNoSecondDomain(t *testing.T) {
	root := consoleBoundaryRoot(t)

	// The scan's own proof that it is reading the site: the file the walk
	// asserts and this rule reads like any other. A scan that reported nothing
	// because it walked nothing would pass for the wrong reason, which is the
	// failure mode every scan in this file carries a guard against.
	if !slices.Contains(websiteBoundaryFiles(t, root), websiteFactsPath) {
		t.Fatalf("%s is not in the walk, so this scan is not looking at the site", websiteFactsPath)
	}

	for _, hit := range websiteHostHits(t, root, websiteSecondDomain, false) {
		t.Errorf("%s:%d names %s: the owner ruled on 2026-10-17 that no second domain is theirs, so the site has ONE domain and nothing under website/ may name the other. W2.2 wrote it into %s and this follow-up removed it -- the value, the page or the document that carries it now is a copy of a revision that predates the ruling, and it would put a domain nobody owns in front of a reader (the amendment under plan Task W2.2)\n%s", hit.file, hit.line, websiteSecondDomain, websiteFactsPath, hit.text)
	}
}

// TestWebsiteSecondDomainScanSeesAPlantedCopy is that scan's positive control:
// it plants the struck host in a page, in the facts file and in a built page of
// a temp tree, and proves the scan reports all three -- the build output
// included, because nothing renders this host, so a built page carrying it is
// the rule's own case -- so the rule above cannot pass by looking at nothing.
func TestWebsiteSecondDomainScanSeesAPlantedCopy(t *testing.T) {
	root := t.TempDir()
	planted := map[string]string{
		websiteFactsPath:                      "canonical_url: https://fugaro.dev\nsecondary_domain: " + websiteSecondDomain + "\n",
		websiteInstallFactsPath:               "version: 1\n",
		"website/src/pages/index.astro":       "<a href=\"https://" + websiteSecondDomain + "/\">elsewhere</a>\n",
		"website/src/components/Footer.astro": "<span>fugaro</span>\n",
		websiteBuildDir + "/index.html":       "<a href=\"https://" + websiteSecondDomain + "/\">elsewhere</a>\n",
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
	for _, hit := range websiteHostHits(t, root, websiteSecondDomain, false) {
		seen[hit.file] = true
	}
	for _, want := range []string{websiteFactsPath, "website/src/pages/index.astro", websiteBuildDir + "/index.html"} {
		if !seen[want] {
			t.Errorf("the scan did not see the planted host in %s: a rule that finds nothing passes for the wrong reason -- and the build output is one of the places it must find one, because nothing renders this host into a page", want)
		}
	}
	if seen["website/src/components/Footer.astro"] {
		t.Error("the scan reported website/src/components/Footer.astro, which does not name the host: the needle is too loose to be read")
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

// firebasePlaceholderEnd returns the index just past the placeholder name that
// starts at i in a pattern ("/:splat" at the ":" ends at the end of the string).
func firebasePlaceholderEnd(p string, i int) int {
	j := i + 1
	for j < len(p) && (p[j] == '_' || p[j] == '-' || p[j] >= '0' && p[j] <= '9' || p[j] >= 'a' && p[j] <= 'z' || p[j] >= 'A' && p[j] <= 'Z') {
		j++
	}
	return j
}

// firebaseRedirectPath normalizes a redirect pattern or destination path for the
// comparison below: a splat or a capture placeholder is the wildcard it stands
// for ("/:splat", "/:1" and "/**" all become "/*"), a pattern with no leading
// slash is the path it would be with one (Firebase writes its catch-all both
// ways, "**" and "/**"), and the root is that same wildcard, because Firebase's
// "/**" matches "/" as well. Two paths that normalize to the same string are the
// same path to every request that reaches either of them.
func firebaseRedirectPath(p string) string {
	for {
		i := strings.IndexByte(p, ':')
		if i < 0 {
			break
		}
		p = p[:i] + "*" + p[firebasePlaceholderEnd(p, i):]
	}
	p = strings.ReplaceAll(p, "**", "*")
	if p == "" || p == "/" {
		return "/*"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

// firebasePatternMatches reports whether a Firebase Hosting glob pattern matches
// a concrete request path -- the question the guard below has to ask, because a
// rule whose own destination is matched by its own source answers the request
// that rule just made: the redirect is followed, the rule fires again, and the
// browser reports a loop. It is what makes a catch-all source visible: "/**" and
// "**" (the spelling Firebase's own examples use) match every path there is, so
// a catch-all pointed at any path on the site's own host loops, whatever that
// path is -- "/index.html" and the bare origin included.
//
// Firebase's glob has two wildcards, and this is them: "**" as a whole path
// segment stands for any run of segments, including none, and inside a segment
// "*" stands for any run of characters while ":name" stands for a whole captured
// segment. Nothing else in a pattern is special.
func firebasePatternMatches(pattern, path string) bool {
	return firebaseSegmentsMatch(firebasePatternSegments(pattern), firebasePatternSegments(path))
}

// firebasePatternSegments splits a pattern or a path into its segments, with the
// root ("/", "//", "") as no segments at all and a leading slash carrying no
// meaning of its own.
func firebasePatternSegments(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func firebaseSegmentsMatch(pattern, path []string) bool {
	if len(pattern) == 0 {
		return len(path) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(path); i++ {
			if firebaseSegmentsMatch(pattern[1:], path[i:]) {
				return true
			}
		}
		return false
	}
	if len(path) == 0 || !firebaseSegmentMatches(pattern[0], path[0]) {
		return false
	}
	return firebaseSegmentsMatch(pattern[1:], path[1:])
}

func firebaseSegmentMatches(pattern, segment string) bool {
	for {
		if pattern == "" {
			return segment == ""
		}
		switch pattern[0] {
		case ':':
			// A capture placeholder is a whole segment, so it has to have
			// something to capture; a suffix after the name is matched against
			// what is left of the segment.
			if segment == "" {
				return false
			}
			rest := pattern[firebasePlaceholderEnd(pattern, 0):]
			if rest == "" {
				return true
			}
			for i := 1; i <= len(segment); i++ {
				if firebaseSegmentMatches(rest, segment[i:]) {
					return true
				}
			}
			return false
		case '*':
			if len(pattern) == 1 {
				return true
			}
			for i := 0; i <= len(segment); i++ {
				if firebaseSegmentMatches(pattern[1:], segment[i:]) {
					return true
				}
			}
			return false
		}
		if segment == "" || segment[0] != pattern[0] {
			return false
		}
		pattern, segment = pattern[1:], segment[1:]
	}
}

// firebaseSelfRedirects returns the redirect rules of a firebase.json that
// answer a request for one of the site's own paths with a redirect to the site's
// own host -- the loop. own is the host the site itself serves: the canonical
// host, and it is the only one, because the owner ruled on 2026-10-17 that there
// is no second domain (the amendment under plan Task W2.2).
//
// Why this is a rule and not a preference: Firebase Hosting matches a redirect
// rule against the request URL PATH, and firebase-tools' schema for one carries
// no field that could condition it on the host (see firebaseRedirect), so a rule
// that sends every path to the canonical origin fires for requests that already
// arrive on the canonical origin: "https://fugaro.dev/faq" is answered with a
// 301 to "https://fugaro.dev/faq" and the browser reports a redirect loop. That
// is why firebase.json carries no redirects block, why the site's one domain is
// attached rather than configured here, and why this test is the mechanical half
// of the plan's amendment: a later task that wants to move a request onto the
// canonical origin -- or that copies the rule W2.2's struck step 2 asked for --
// meets a red test instead of a loop on the live site. A rule whose destination
// is another origin, or one of the site's own paths that its source does not
// match, is legitimate and is not reported.
//
// What counts as the loop is firebasePatternMatches' question and not a string
// comparison: a rule loops when its source matches its own destination, so a
// catch-all pointed at "/index.html" on the site's own host is reported like the
// same-path rule is. A guard that only compared the two strings would leave the
// catch-all -- the shape a later task is most likely to reach for -- invisible.
//
// A rule that uses `regex` cannot be compared with a destination path here, so a
// regex rule pointing at the site's own host is reported rather than judged:
// whoever writes one extends this test with the rule they mean.
func firebaseSelfRedirects(t *testing.T, path string, config []byte, own string) []string {
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
		// An empty host is a destination on this site ("/new"), which is the
		// site's own host like any other; a destination on another host leaves
		// the site and cannot loop.
		if u.Host != "" && u.Host != own {
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
		// Two ways a same-host destination loops, and both are needed: the
		// source and the destination are the same path, or the source is a
		// pattern that matches the destination path -- which is how a catch-all
		// ("/**", "**") pointed at any path on this site loops, and how a
		// wildcard segment pointed at a path inside its own match set does. The
		// first is what catches a destination that is itself a pattern
		// ("https://fugaro.dev/:splat"), which the second cannot match against.
		if firebaseRedirectPath(source) == firebaseRedirectPath(u.Path) || firebasePatternMatches(source, u.Path) {
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
// This is the mechanical half of the plan's amendment, and it survives the
// owner's ruling of 2026-10-17 intact: the site has ONE domain, nothing
// redirects, and the shape a later task is most likely to reach for when it
// wants to move a request onto the canonical origin -- a catch-all pointed at
// that origin -- is the rule that would take the live site down. The positive
// control beside it plants the loop, so the test cannot pass by looking at an
// empty list.
func TestWebsiteFirebaseRedirectsDoNotLoop(t *testing.T) {
	root := consoleBoundaryRoot(t)
	facts, _ := readWebsiteFacts(t)
	own, ok := strings.CutPrefix(facts.CanonicalURL, "https://")
	if !ok || own == "" || strings.ContainsAny(own, "/?#") {
		t.Fatalf("%s: canonical_url = %q names no host, so this test could not tell a loop from a redirect to somewhere else (TestWebsiteFactsMatchTheRepository holds the value's shape)", websiteFactsPath, facts.CanonicalURL)
	}

	const path = "firebase.json"
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	for _, loop := range firebaseSelfRedirects(t, path, data, own) {
		t.Errorf("%s carries a redirect that answers a request on the site's own host with a redirect to the site's own host: %s. Hosting matches a rule against the path only and has no host condition, so this rule fires on the canonical host too and loops -- on the live site, from the next deploy. firebase.json carries no redirects block, and the amendment under plan Task W2.2 records why one cannot be written for this site", path, loop)
	}
}

// TestWebsiteFirebaseRedirectGuardSeesAPlantedLoop is that guard's positive
// control: every shape of the loop it exists for, and the shapes a legitimate
// rules list can have beside them -- a redirect to another origin, a redirect to
// another path on this site, a relative destination, a wildcard whose
// destination is outside its own match set -- so the guard cannot pass by
// looking at nothing and cannot become a rule against redirects.
//
// The looping shapes are the two spellings of Firebase's catch-all ("/**" and
// "**", the one the docs' own examples use) pointed at a path on the site's own
// host, and the same path twice. The catch-all is the one a string comparison
// misses and this control exists for: "/**" does not equal "/index.html", so a
// guard that only compared source to destination would report nothing and pass
// while the live site looped. A regex rule on the site's own host is here too,
// because the guard reports that shape without comparing its match set and
// nothing else would prove the branch runs.
func TestWebsiteFirebaseRedirectGuardSeesAPlantedLoop(t *testing.T) {
	const path = "firebase.json"
	const own = "fugaro.dev"
	planted := `{"hosting":{"public":"website/dist","redirects":[
		{"source":"/**","destination":"https://fugaro.dev/:splat","type":301},
		{"source":"/**","destination":"https://fugaro.dev/index.html","type":301},
		{"source":"**","destination":"https://fugaro.dev","type":301},
		{"source":"/old","destination":"https://fugaro.dev/old","type":301},
		{"source":"/legacy/*","destination":"/legacy/index.html","type":301},
		{"source":"/old","destination":"https://fugaro.dev/new","type":301},
		{"source":"/moved","destination":"/moved-here","type":302},
		{"source":"/blog/:slug","destination":"/posts/:slug","type":301},
		{"source":"/**","destination":"https://elsewhere.example/:splat","type":301},
		{"source":"/releases","destination":"https://github.com/dimipaun/fugaro/releases","type":302},
		{"regex":"^/old/(.*)$","destination":"https://fugaro.dev/new/$1","type":301}
	]}}`
	want := []int{0, 1, 2, 3, 4, 10}

	var got []int
	for _, loop := range firebaseSelfRedirects(t, path, []byte(planted), own) {
		var i int
		if n, err := fmt.Sscanf(loop, "redirects[%d]:", &i); n != 1 || err != nil {
			t.Fatalf("the guard reported %q, which this control cannot read an index out of: the message has to name the rule, or a report cannot be acted on", loop)
		}
		got = append(got, i)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the guard reported rules %v, want %v: a guard that misses a planted loop passes for the wrong reason (rule 1 and rule 2 are the catch-all spellings, which no string comparison sees; rule 3 is a path redirected to itself; rule 4 is a wildcard pointing inside its own match set; and rule 10 is a regex rule on the site's own host, which the guard refuses without comparing its match set), and one that also reports a redirect to another origin, another path or a relative destination is a rule against redirects", got, want)
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
//
// 69a is also held to the owner's ruling of 2026-10-17 by absence: the check
// verifies the canonical origin, the deployed artifact and the page a stranger
// lands on, and it may not carry a second domain or a redirect to observe, so a
// later edit cannot put the owner back in front of a domain that is not theirs.
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
		// refuses are strings the section may NOT carry. 69a's are the
		// owner's ruling of 2026-10-17 made mechanical: a check that still
		// asks for a second domain, or for a redirect to observe, is a check
		// that sends the owner looking for a domain they do not own.
		refuses []string
	}{
		{
			heading: "## Check 69a:",
			wants: []string{
				"NOT RUN",
				"run by you",
				"No agent runs any step",
				"website/data/site.yaml",
				"canonical_url",
				"HTTPS",
				"features.console_downloads",
				"features.video",
				"runs hermetically",
				"FACT:",
				"Passes when:",
				"Fails when:",
				"Paste back:",
			},
			refuses: []string{"secondary_domain", "301", websiteSecondDomain},
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
				t.Errorf("%s does not carry %q: a live check says who runs it, what to run and look at, when it passes and what failing looks like (plan Task W2.2 step 4)", c.heading, want)
			}
		}
		for _, refuse := range c.refuses {
			if strings.Contains(section, refuse) {
				t.Errorf("%s carries %q, which the owner's ruling of 2026-10-17 took out of this check: the site has one domain, nothing redirects, and a step that observes another domain is a step the owner cannot run (the amendment under plan Task W2.2)", c.heading, refuse)
			}
		}
	}
}
