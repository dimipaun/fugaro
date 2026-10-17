package cli

// The site's install facts (website plan, Task W0.1; reconciliation findings
// 19-22, D94).
//
// website/data/install.yaml is the one install source the site renders: the
// five paths this repository really ships, each naming the file its text came
// from. Nothing under website/ is importable by this module (the boundary test
// W0.2 adds), so the file is read as bytes -- the way every other
// docs_*_test.go here reads its document -- and the three tests below hold it
// to the repository rather than to its own prose:
//
//   - TestWebsiteInstallFactsMatchTheRepository: every command appears
//     verbatim in the file its `source:` names (README.md's "## Getting
//     started" or docs/release.md); `build` equals .goreleaser.yaml's
//     builds[0]; the archive's name template and extension equal archives[0];
//     the Homebrew line equals homebrew_casks[0]'s owner, tap and cask; and
//     the go install path carries README.md's "development only" marker.
//   - TestWebsiteInstallFactsNameNoUnbuiltPlatform: the reader-facing platform
//     list is README.md's "## Requirements" and nothing more, a platform no
//     build produces is named only where it is marked untested, no support is
//     claimed for one, and every path's own platform list equals the list the
//     source offering that path implies -- so `homebrew: [linux]` fails rather
//     than rendering `brew install` on the Linux page (finding 20; F20's gate
//     and D94's wording, and a Windows build is the only thing that changes
//     this test's answer).
//   - TestWebsiteInstallFactsCarryNoVersion: no version number appears
//     anywhere -- the site renders `latest`, or the release's own tag.
//
// Two consequences are deliberate, and a later page renders around them rather
// than fixing them here:
//
//   - The Windows wording is this file's, not the page's: D94 makes the
//     platform list part of this same source, so install.yaml carries the
//     "untested" note -- and the test above is what refuses a *support* claim
//     for a platform no build produces, which is the narrower refusal the plan
//     states (a Windows build in .goreleaser.yaml is what re-opens it).
//   - The archive is a link to the Releases page, never a releases/download/
//     URL: that URL carries a version the site does not know, and it is the
//     string W0.2's console-link test refuses site-wide (finding 25).

import (
	"bytes"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The two files the product's own install text lives in: a command in
// install.yaml appears verbatim in one of them, or the test fails. A source
// outside this pair (an internal design document, say) is refused rather than
// widening the list.
const (
	installFactsPath  = "website/data/install.yaml"
	releaseGuidePath  = "docs/release.md"
	installReadmePath = "README.md"
)

// installFactsFile is website/data/install.yaml, decoded with unknown fields
// refused: a key nobody reads is a fact nobody pins.
type installFactsFile struct {
	Version   int                    `yaml:"version"`
	Build     installFactsBuild      `yaml:"build"`
	Platforms []installFactsPlatform `yaml:"platforms"`
	Paths     []installFactsEntry    `yaml:"paths"`
}

// installFactsBuild is what the repository actually builds, pinned to
// .goreleaser.yaml's builds[0].
type installFactsBuild struct {
	OS   []string `yaml:"os"`
	Arch []string `yaml:"arch"`
}

// installFactsPlatform is one entry of the reader-facing platform list: the
// name a reader is shown, the goos it means, whether the repository supports
// it or only notes it as untested, and the note the untested entry carries.
type installFactsPlatform struct {
	ID     string `yaml:"id"`
	Name   string `yaml:"name"`
	Goos   string `yaml:"goos"`
	Status string `yaml:"status"`
	Note   string `yaml:"note"`
}

// installFactsEntry is one install path: what it is, the file its text came
// from, the platforms it serves, the commands as the source writes them, and
// the archive facts that are not commands.
type installFactsEntry struct {
	ID            string   `yaml:"id"`
	Name          string   `yaml:"name"`
	Source        string   `yaml:"source"`
	Platforms     []string `yaml:"platforms"`
	Note          string   `yaml:"note"`
	Commands      []string `yaml:"commands"`
	TapRepository string   `yaml:"tap_repository"`
	ReleasePage   string   `yaml:"release_page"`
	NameTemplate  string   `yaml:"name_template"`
	Extension     string   `yaml:"extension"`
	DevOnly       bool     `yaml:"dev_only"`
}

// installPath returns the path with the given id, failing the test when the
// file does not carry it.
func (f installFactsFile) installPath(t *testing.T, id string) installFactsEntry {
	t.Helper()
	for _, p := range f.Paths {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("%s carries no %q path", installFactsPath, id)
	return installFactsEntry{}
}

// readInstallFacts reads website/data/install.yaml and decodes it strictly,
// returning the file with its raw text: the negative tests scan the bytes,
// because a value in a key nobody reads is still on the page's source.
//
// The read failure is reported repo-relative and without the os.PathError's
// own prefix, so the red step this task was written against reads exactly
// "open website/data/install.yaml: no such file or directory".
func readInstallFacts(t *testing.T) (installFactsFile, string) {
	t.Helper()
	data, err := os.ReadFile("../../" + installFactsPath)
	if err != nil {
		var perr *os.PathError
		if errors.As(err, &perr) {
			t.Fatalf("open %s: %v", installFactsPath, perr.Err)
		}
		t.Fatal(err)
	}
	var f installFactsFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("%s: %v", installFactsPath, err)
	}
	return f, string(data)
}

// goreleaserBuild, goreleaserArchive and goreleaserCask are the three
// .goreleaser.yaml blocks the install facts are pinned to.
type goreleaserBuild struct {
	ID     string   `yaml:"id"`
	Goos   []string `yaml:"goos"`
	Goarch []string `yaml:"goarch"`
}

type goreleaserArchive struct {
	ID           string   `yaml:"id"`
	Formats      []string `yaml:"formats"`
	NameTemplate string   `yaml:"name_template"`
}

type goreleaserCask struct {
	Name       string `yaml:"name"`
	Repository struct {
		Owner string `yaml:"owner"`
		Name  string `yaml:"name"`
	} `yaml:"repository"`
}

type goreleaserFile struct {
	Builds        []goreleaserBuild   `yaml:"builds"`
	Archives      []goreleaserArchive `yaml:"archives"`
	HomebrewCasks []goreleaserCask    `yaml:"homebrew_casks"`
}

// readGoreleaser reads .goreleaser.yaml -- the release's own build, archive
// and cask definitions, and the file the OS/arch matrix, the archive name
// template and the Homebrew line all come from.
func readGoreleaser(t *testing.T) goreleaserFile {
	t.Helper()
	var f goreleaserFile
	if err := yaml.Unmarshal([]byte(readDoc(t, ".goreleaser.yaml")), &f); err != nil {
		t.Fatalf(".goreleaser.yaml: %v", err)
	}
	if len(f.Builds) == 0 || len(f.Archives) == 0 || len(f.HomebrewCasks) == 0 {
		t.Fatal(".goreleaser.yaml carries no builds[0], archives[0] or homebrew_casks[0]; this test's pins are stale")
	}
	return f
}

// readmeSection returns one of README.md's "## " sections, heading included,
// ending at the next "## " heading.
func readmeSection(t *testing.T, heading string) string {
	t.Helper()
	doc := readDoc(t, installReadmePath)
	start := strings.Index(doc, "\n"+heading+"\n")
	if start < 0 {
		t.Fatalf("%s has no %q section; this test's source list is stale", installReadmePath, heading)
	}
	section := doc[start+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// readmeGettingStarted returns README.md's "## Getting started" section, one
// of the two sources an install command may come from.
func readmeGettingStarted(t *testing.T) string {
	t.Helper()
	return readmeSection(t, "## Getting started")
}

// readmePlatformStatement reads the bullet in README.md's "## Requirements"
// that names the operating systems Fugaro runs on -- "macOS or Linux (Windows:
// untested)" -- and returns the platforms it lists as supported and the ones it
// marks untested. It is the reader-facing statement the site's own platform
// list has to equal: the site says what the repository says, and nothing more
// (D94, F20).
func readmePlatformStatement(t *testing.T) (supported, untested []string) {
	t.Helper()
	section := readmeSection(t, "## Requirements")
	var bullet string
	for _, line := range strings.Split(section, "\n") {
		if strings.Contains(strings.ToLower(line), "untested") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatalf("%s's \"## Requirements\" carries no bullet marking a platform untested; this test's source is stale", installReadmePath)
	}
	open := strings.Index(bullet, "(")
	closing := strings.Index(bullet, ")")
	if open < 0 || closing < open {
		t.Fatalf("%s's platform bullet %q carries no parenthetical marking the untested platform; this test's source is stale", installReadmePath, bullet)
	}
	inside := bullet[open+1 : closing]
	if !strings.Contains(strings.ToLower(inside), "untested") {
		t.Fatalf("%s's platform bullet %q does not mark %q untested; this test's source is stale", installReadmePath, bullet, inside)
	}
	supported, untested = platformsIn(bullet[:open]), platformsIn(inside)
	if len(supported) == 0 || len(untested) == 0 {
		t.Fatalf("%s's platform bullet %q names no supported or no untested platform; this test's source is stale", installReadmePath, bullet)
	}
	return supported, untested
}

// readmeMarkerPlatforms returns the platforms named in the parenthetical
// README.md prints on the same line as the given command -- the "(tested on
// macOS)" beside the Homebrew line -- which is what narrows that path to one
// platform. A command the README prints without a marker is refused rather
// than assumed to serve everything.
func readmeMarkerPlatforms(t *testing.T, command string) []string {
	t.Helper()
	for _, line := range strings.Split(readmeGettingStarted(t), "\n") {
		if !strings.Contains(line, command) {
			continue
		}
		open := strings.Index(line, "(")
		closing := strings.Index(line, ")")
		if open < 0 || closing < open {
			t.Fatalf("%s prints %q with no parenthetical marker; this test's source is stale", installReadmePath, command)
		}
		return platformsIn(line[open+1 : closing])
	}
	t.Fatalf("%s's \"## Getting started\" does not print %q", installReadmePath, command)
	return nil
}

// installCommandLines splits one stored command into the lines it is written
// as. A multi-line command (cosign verify-blob, whose continuation lines carry
// their own leading spaces) is matched line by line, as the release guide
// writes it, rather than joined into a one-liner nothing prints.
func installCommandLines(cmd string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(cmd, "\n"), "\n") {
		if line = strings.TrimRight(line, " \t"); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// TestWebsiteInstallFactsMatchTheRepository holds website/data/install.yaml to
// the repository: the five paths the site ships, every command printed by the
// file its `source:` names, and the build matrix, the archive's name template
// and extension, and the Homebrew line taken from .goreleaser.yaml rather than
// typed here.
func TestWebsiteInstallFactsMatchTheRepository(t *testing.T) {
	facts, raw := readInstallFacts(t)
	gl := readGoreleaser(t)
	gettingStarted := readmeGettingStarted(t)
	releaseGuide := readDoc(t, releaseGuidePath)

	if facts.Version != 1 {
		t.Errorf("%s: version = %d, want 1", installFactsPath, facts.Version)
	}

	// The five paths the repository really ships, in the order the plan lists
	// them. A dropped path, an added one and a reordering all fail here: the
	// list is curated, and adding to it is a deliberate edit of this test.
	var ids []string
	for _, p := range facts.Paths {
		ids = append(ids, p.ID)
	}
	want := []string{"homebrew", "archive", "cosign", "go-install", "plugin"}
	if !slices.Equal(ids, want) {
		t.Errorf("%s: paths = %v, want %v", installFactsPath, ids, want)
	}

	// Every path but the archive carries commands, and each command appears
	// verbatim in the file its own `source:` names. This is the rule that
	// keeps an invented instruction -- a curl | tar one-liner nobody ships, a
	// marketplace command a reader cannot run -- off the site.
	sources := map[string]string{
		installReadmePath: gettingStarted,
		releaseGuidePath:  releaseGuide,
	}
	for _, p := range facts.Paths {
		if p.Name == "" {
			t.Errorf("%s: path %q carries no name", installFactsPath, p.ID)
		}
		text, known := sources[p.Source]
		if !known {
			t.Errorf("%s: path %q names source %q, want %s or %s", installFactsPath, p.ID, p.Source, installReadmePath, releaseGuidePath)
			continue
		}
		if p.ID == "archive" {
			if len(p.Commands) != 0 {
				t.Errorf("%s: the archive path carries commands %q; it is a link to the Releases page, pinned by release_page", installFactsPath, p.Commands)
			}
			continue
		}
		if len(p.Commands) == 0 {
			t.Errorf("%s: path %q carries no command, and %s prints one for it", installFactsPath, p.ID, p.Source)
		}
		for _, cmd := range p.Commands {
			for _, line := range installCommandLines(cmd) {
				if !strings.Contains(text, line) {
					t.Errorf("%s: path %q carries the command line %q, which %s does not print", installFactsPath, p.ID, line, p.Source)
				}
			}
		}
	}

	// What the repository builds, and nothing else: builds[0]'s goos x goarch.
	if !slices.Equal(facts.Build.OS, gl.Builds[0].Goos) {
		t.Errorf("%s: build.os = %v, .goreleaser.yaml builds[0].goos = %v", installFactsPath, facts.Build.OS, gl.Builds[0].Goos)
	}
	if !slices.Equal(facts.Build.Arch, gl.Builds[0].Goarch) {
		t.Errorf("%s: build.arch = %v, .goreleaser.yaml builds[0].goarch = %v", installFactsPath, facts.Build.Arch, gl.Builds[0].Goarch)
	}

	// The release archive: the name template is archives[0]'s own (which
	// carries no extension -- the .tar.gz a downloaded file ends in comes from
	// that archive's formats), and the site links the Releases page rather
	// than building a URL that would name a version.
	archive := facts.installPath(t, "archive")
	if archive.NameTemplate != gl.Archives[0].NameTemplate {
		t.Errorf("%s: archive name_template = %q, .goreleaser.yaml archives[0].name_template = %q", installFactsPath, archive.NameTemplate, gl.Archives[0].NameTemplate)
	}
	if !slices.Contains(gl.Archives[0].Formats, archive.Extension) {
		t.Errorf("%s: archive extension = %q, .goreleaser.yaml archives[0].formats = %v", installFactsPath, archive.Extension, gl.Archives[0].Formats)
	}
	if archive.ReleasePage == "" || !strings.Contains(gettingStarted, archive.ReleasePage) {
		t.Errorf("%s: archive release_page = %q, which %s's \"## Getting started\" does not print", installFactsPath, archive.ReleasePage, installReadmePath)
	}

	// Homebrew: the line is the cask's own owner, tap and name, and the note
	// is the marker README.md prints beside it.
	cask := gl.HomebrewCasks[0]
	tap := strings.TrimPrefix(cask.Repository.Name, "homebrew-")
	wantBrew := "brew install " + cask.Repository.Owner + "/" + tap + "/" + cask.Name
	homebrew := facts.installPath(t, "homebrew")
	if len(homebrew.Commands) != 1 || strings.TrimSpace(homebrew.Commands[0]) != wantBrew {
		t.Errorf("%s: the Homebrew commands are %q, want [%q] (from .goreleaser.yaml homebrew_casks[0])", installFactsPath, homebrew.Commands, wantBrew)
	}
	if wantTap := cask.Repository.Owner + "/" + cask.Repository.Name; homebrew.TapRepository != wantTap {
		t.Errorf("%s: the Homebrew tap_repository = %q, .goreleaser.yaml homebrew_casks[0].repository = %q", installFactsPath, homebrew.TapRepository, wantTap)
	}
	if !strings.Contains(homebrew.Note, "tested on macOS") || !strings.Contains(gettingStarted, "(tested on macOS)") {
		t.Errorf("%s: the Homebrew note %q does not carry the \"(tested on macOS)\" marker %s prints", installFactsPath, homebrew.Note, installReadmePath)
	}

	// go install: the development path, marked as the README marks it.
	goInstall := facts.installPath(t, "go-install")
	if !goInstall.DevOnly {
		t.Errorf("%s: the go install path is not marked dev_only, and %s says it is for development only", installFactsPath, installReadmePath)
	}
	for _, want := range []string{"development only", "no release images", "plugin pin"} {
		if !strings.Contains(goInstall.Note, want) || !strings.Contains(gettingStarted, want) {
			t.Errorf("%s: the go install note %q and %s's \"## Getting started\" must both carry %q", installFactsPath, goInstall.Note, installReadmePath, want)
		}
	}

	// What the file must never carry: a download URL (the site links the
	// Releases page) and the marketplace commands, which live in
	// docs/design/upgrade.md and docs/design/m11-setup-and-skills.md rather
	// than in README.md, so the plugin path leaves them out instead of
	// widening this test's source list to internal design documents.
	for _, banned := range []string{
		"releases/download/",
		"/plugin marketplace add",
		"/plugin install fugaro@fugaro",
		"claude plugin install fugaro@fugaro",
	} {
		if strings.Contains(raw, banned) {
			t.Errorf("%s carries %q, which no install path of this repository prints", installFactsPath, banned)
		}
	}
}

// platformName maps every platform word the install facts could name to the
// goos or goarch name it means, so that a file saying "macOS" or "Windows" is
// checked against .goreleaser.yaml rather than against a note beside it.
var platformName = map[string]string{
	// goos
	"aix": "aix", "android": "android", "darwin": "darwin", "dragonfly": "dragonfly",
	"freebsd": "freebsd", "illumos": "illumos", "ios": "ios", "js": "js",
	"linux": "linux", "netbsd": "netbsd", "openbsd": "openbsd", "plan9": "plan9",
	"solaris": "solaris", "wasip1": "wasip1", "windows": "windows",
	"macos": "darwin", "osx": "darwin", "win32": "windows", "win64": "windows",
	// goarch
	"386": "386", "amd64": "amd64", "arm": "arm", "arm64": "arm64",
	"loong64": "loong64", "mips": "mips", "mips64": "mips64", "mips64le": "mips64le",
	"mipsle": "mipsle", "ppc64": "ppc64", "ppc64le": "ppc64le", "riscv64": "riscv64",
	"s390x": "s390x", "wasm": "wasm",
	"aarch64": "arm64", "i386": "386", "x86": "386", "x86_64": "amd64",
}

// platformNameRE matches any platform word in the map, longest first so that
// "mips64le" is read as mips64le rather than as mips or mips64 (the trailing
// \b already refuses the shorter reads; the order makes the match independent
// of the map's iteration order).
func platformNameRE() *regexp.Regexp {
	names := make([]string, 0, len(platformName))
	for n := range platformName {
		names = append(names, n)
	}
	slices.SortFunc(names, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return regexp.MustCompile(`(?i)\b(` + strings.Join(names, "|") + `)\b`)
}

// platformsIn returns the canonical platforms a text names, in the order it
// first names them and without repeats, so a list written in the repository can
// be compared with one written in the install facts.
func platformsIn(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range platformNameRE().FindAllString(text, -1) {
		canonical := platformName[strings.ToLower(m)]
		if !seen[canonical] {
			seen[canonical] = true
			out = append(out, canonical)
		}
	}
	return out
}

// platformAliases returns every word that means the given goos or goarch name,
// longest first, for building a pattern that matches the platform however a
// document spells it.
func platformAliases(canonical string) []string {
	var out []string
	for name, c := range platformName {
		if c == canonical {
			out = append(out, name)
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return out
}

// supportClaimRE matches a support promise made about one platform -- "Windows
// is supported", "Windows support", "support for Windows", "runs on Windows",
// "Windows works". It is the shape D94 refuses and the plan's item 3 refuses
// with it: the install facts may name a platform no build produces only in
// order to say it is untested, never as somewhere a reader can install.
func supportClaimRE(canonical string) *regexp.Regexp {
	word := `(?:` + strings.Join(platformAliases(canonical), "|") + `)`
	return regexp.MustCompile(`(?i)(` + word + `\s+(is\s+|are\s+)?(support(ed|s)?|available|native|works?|runs?)\b` +
		`|support(ed|s)?\s+(for|on)\s+` + word + `\b` +
		`|(works?|runs?)\s+on\s+` + word + `\b)`)
}

// installProseBlocks returns the file's prose as whole blocks -- each run of
// consecutive comment lines and each note value -- so that a claim is judged as
// the text a reader would read rather than one line at a time.
func installProseBlocks(facts installFactsFile, raw string) []string {
	var blocks, comment []string
	flush := func() {
		if len(comment) > 0 {
			blocks = append(blocks, strings.Join(comment, "\n"))
			comment = nil
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "#") {
			comment = append(comment, trimmed)
			continue
		}
		flush()
	}
	flush()
	for _, p := range facts.Platforms {
		if p.Note != "" {
			blocks = append(blocks, p.Note)
		}
	}
	for _, p := range facts.Paths {
		if p.Note != "" {
			blocks = append(blocks, p.Note)
		}
	}
	return blocks
}

// TestWebsiteInstallFactsNameNoUnbuiltPlatform is F20's gate, narrowed to the
// refusal D94 and the plan's item 3 actually state: the install facts may not
// present a platform .goreleaser.yaml builds[0] does not produce as one a
// reader can install on. The reader-facing platform list equals README.md's
// "## Requirements" -- macOS and Linux supported, Windows untested -- and the
// Windows note is the one D94 settles, so it is carried here rather than left
// for a page to write. A future Windows build is the only thing that changes
// this test's answer, and it changes it through .goreleaser.yaml.
func TestWebsiteInstallFactsNameNoUnbuiltPlatform(t *testing.T) {
	facts, raw := readInstallFacts(t)
	gl := readGoreleaser(t)
	supported, untested := readmePlatformStatement(t)

	built := map[string]bool{}
	for _, os := range gl.Builds[0].Goos {
		built[os] = true
	}
	for _, arch := range gl.Builds[0].Goarch {
		built[arch] = true
	}

	// The structured halves are the matrix itself.
	if !slices.Equal(facts.Build.OS, gl.Builds[0].Goos) {
		t.Errorf("%s: build.os = %v, .goreleaser.yaml builds[0].goos = %v", installFactsPath, facts.Build.OS, gl.Builds[0].Goos)
	}
	if !slices.Equal(facts.Build.Arch, gl.Builds[0].Goarch) {
		t.Errorf("%s: build.arch = %v, .goreleaser.yaml builds[0].goarch = %v", installFactsPath, facts.Build.Arch, gl.Builds[0].Goarch)
	}

	// The reader-facing list: every supported entry is a platform the build
	// produces, every untested entry is one it does not, the untested entry
	// carries the note, and the two lists together are README.md's own
	// statement -- so the site cannot promise Windows, and cannot quietly drop
	// the note either.
	var declared, noted []string
	for _, p := range facts.Platforms {
		if p.Name == "" || p.Goos == "" {
			t.Errorf("%s: platform %q carries no name or no goos", installFactsPath, p.ID)
		}
		switch p.Status {
		case "supported":
			declared = append(declared, p.Goos)
			if !built[p.Goos] {
				t.Errorf("%s: platform %q is supported and .goreleaser.yaml builds[0] does not build %q (goos %v)", installFactsPath, p.ID, p.Goos, gl.Builds[0].Goos)
			}
		case "untested":
			noted = append(noted, p.Goos)
			if built[p.Goos] {
				t.Errorf("%s: platform %q is marked untested and .goreleaser.yaml builds[0] builds it: the note is stale and has to be re-opened", installFactsPath, p.ID)
			}
			if !strings.Contains(strings.ToLower(p.Note), "untested") {
				t.Errorf("%s: platform %q is marked untested and its note %q does not say so", installFactsPath, p.ID, p.Note)
			}
		default:
			t.Errorf("%s: platform %q carries status %q, want supported or untested", installFactsPath, p.ID, p.Status)
		}
	}
	if !slices.Equal(declared, supported) {
		t.Errorf("%s: the supported platforms = %v, %s's \"## Requirements\" names %v", installFactsPath, declared, installReadmePath, supported)
	}
	if !slices.Equal(noted, untested) {
		t.Errorf("%s: the untested platforms = %v, %s's \"## Requirements\" names %v", installFactsPath, noted, installReadmePath, untested)
	}

	// The prose: an unbuilt platform may be named only in a block that marks it
	// untested, and never in one that claims it works. This is what lets the
	// Windows note live in the file while refusing a Windows support claim.
	for _, block := range installProseBlocks(facts, raw) {
		for _, name := range platformsIn(block) {
			if built[name] {
				continue
			}
			if !strings.Contains(strings.ToLower(block), "untested") {
				t.Errorf("%s names the platform %s, which .goreleaser.yaml builds[0] does not build, without marking it untested:\n%s", installFactsPath, name, block)
			}
			if claim := supportClaimRE(name).FindString(block); claim != "" {
				t.Errorf("%s claims %q for %s, which .goreleaser.yaml builds[0] does not build:\n%s", installFactsPath, claim, name, block)
			}
		}
	}

	// Every path's own list is pinned exactly, not merely bounded by the build
	// matrix: a subset check lets `homebrew: [linux]` through, which would
	// render `brew install` on the Linux page while README.md marks the cask
	// "(tested on macOS)" and .goreleaser.yaml's own cask comment says Linux is
	// untested for it.
	for _, p := range facts.Paths {
		if len(p.Platforms) == 0 {
			t.Errorf("%s: path %q names no platform", installFactsPath, p.ID)
		}
		for _, plat := range p.Platforms {
			if !built[plat] {
				t.Errorf("%s: path %q names platform %q, which .goreleaser.yaml builds[0] does not build", installFactsPath, p.ID, plat)
			}
		}
	}
	homebrew := facts.installPath(t, "homebrew")
	if want := readmeMarkerPlatforms(t, strings.TrimSpace(homebrew.Commands[0])); !slices.Equal(homebrew.Platforms, want) {
		t.Errorf("%s: the Homebrew platforms = %v, the marker %s prints beside the brew line names %v", installFactsPath, homebrew.Platforms, installReadmePath, want)
	}
	archive := facts.installPath(t, "archive")
	if !slices.Equal(archive.Platforms, gl.Builds[0].Goos) {
		t.Errorf("%s: the archive's platforms = %v, .goreleaser.yaml builds[0].goos = %v", installFactsPath, archive.Platforms, gl.Builds[0].Goos)
	}
	cosign := facts.installPath(t, "cosign")
	if want := platformsIn(strings.Join(cosign.Commands, "\n")); !slices.Equal(cosign.Platforms, want) {
		t.Errorf("%s: the cosign platforms = %v, the verification block it prints names %v (one checksum line per platform)", installFactsPath, cosign.Platforms, want)
	}
	for _, id := range []string{"go-install", "plugin"} {
		if p := facts.installPath(t, id); !slices.Equal(p.Platforms, supported) {
			t.Errorf("%s: the %s platforms = %v, %s's \"## Requirements\" names %v for the CLI it installs", installFactsPath, id, p.Platforms, installReadmePath, supported)
		}
	}
}

// TestWebsiteInstallFactsCarryNoVersion: no version number appears anywhere in
// the install facts. The site renders `latest` for the go install path, or the
// release's own tag, and a version typed into this file would be a version the
// site states forever.
func TestWebsiteInstallFactsCarryNoVersion(t *testing.T) {
	facts, raw := readInstallFacts(t)

	versionRE := regexp.MustCompile(`\bv?\d+\.\d+\.\d+\b`)
	for _, m := range versionRE.FindAllString(raw, -1) {
		t.Errorf("%s carries the version %q; the site renders `latest`, or the release's own tag", installFactsPath, m)
	}

	// The placeholders the release guide's own verification block uses are the
	// ones this file may carry: a version is a value the reader supplies, not
	// one the site states.
	for _, want := range []string{"X.Y.Z", "${V}"} {
		if !strings.Contains(raw, want) {
			t.Errorf("%s lacks the placeholder %q the release guide's verification block uses", installFactsPath, want)
		}
	}

	goInstall := facts.installPath(t, "go-install")
	cmd := strings.TrimSpace(strings.Join(goInstall.Commands, "\n"))
	if !strings.HasSuffix(cmd, "@latest") {
		t.Errorf("%s: the go install command is %q, want it to end in @latest", installFactsPath, cmd)
	}
}
