// Package imagesurvey is the image survey's signal rules: the fixed set of
// paths one repository is read at, the shape a value taken out of one must
// have, and the signals a repository's answers amount to.
//
// The list is fixed and never discovered, and that is the whole of this
// package's design. gitprov.ContentsReader reads the file a caller names and
// lists nothing, so a survey that walked a repository's tree is one the
// interface cannot support; every path here is therefore a literal, and the
// rules are what a repository's fixed answers mean (the reconciliation's F22).
//
// Every value a rule answers with is either a constant of this package or the
// capture of that rule's own regexp, bounded by it before it is stored, and
// printed through pluginwire.Printable before it leaves Findings. Nothing here
// trusts the repository: a lockfile's name is the survey's, a version is a
// version-shaped token or nothing, and a FROM line is a reference-shaped one,
// so the bytes of a repository the project does not own cannot reach an agent's
// context as anything but a value this package has already bounded.
package imagesurvey

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The kinds a signal is reported as. They are a closed vocabulary — the
// document `fugaro image survey --json` prints its kinds from these constants
// and schemas/image-survey.schema.json lists them — so a consumer branches on
// a kind only where a release has said what that kind means.
const (
	KindLockfile       = "lockfile"
	KindVersion        = "version"
	KindDockerfile     = "dockerfile"
	KindCI             = "ci"
	KindCompose        = "compose"
	KindTestcontainers = "testcontainers"
)

// Signal is one fixed path the survey reads, and the kind its answer is
// reported as. The paths are exported (Signals) so a caller can say what the
// survey reads without reading this package's rules, and so a test can pin
// that the read is exactly this list.
type Signal struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// Finding is one signal a repository answered with: the kind, the fixed path
// the answer came from, and the value the rule bounded — a manager's name, a
// version, a base image reference, a set of service hints, or "" for a rule
// whose answer is the file's presence alone.
type Finding struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Value string `json:"value,omitempty"`
}

// File is one file the survey read for a repository: a fixed path and what was
// there. It is what Findings judges, so the rules can be tested without a
// provider.
type File struct {
	Path string
	Data []byte
}

// rule is one fixed path and how its answer is read.
//
// read answers (value, ok): ok false is "this file names nothing of this
// rule's kind", which is what a lockfile-shaped path with something else in it,
// a Dockerfile with no FROM line and a compose file naming no known service all
// answer, and it contributes no finding at all.
type rule struct {
	kind string
	path string
	read func([]byte) (string, bool)
}

// rules is the whole fixed list, in one settled order: the paths a reader of
// the report sees grouped by kind, and the order the reads are made in.
var rules = []rule{
	// A lockfile's own name is the signal, and the manager it implies is this
	// package's constant: nothing in the file is read.
	{KindLockfile, "package-lock.json", presence("npm")},
	{KindLockfile, "pnpm-lock.yaml", presence("pnpm")},
	{KindLockfile, "yarn.lock", presence("yarn")},
	{KindLockfile, "bun.lock", presence("bun")},
	{KindLockfile, "go.sum", presence("go")},
	{KindLockfile, "Cargo.lock", presence("cargo")},
	{KindLockfile, "poetry.lock", presence("poetry")},
	{KindLockfile, "requirements.txt", presence("pip")},
	{KindLockfile, "Gemfile.lock", presence("bundler")},
	{KindLockfile, "composer.lock", presence("composer")},

	// A version file names the version the image has to carry. Each regexp
	// captures a version-shaped token and nothing else: a file whose text is
	// not one contributes no finding rather than a token nobody bounded.
	{KindVersion, ".nvmrc", capture(nodeVersion)},
	{KindVersion, ".python-version", capture(pythonVersion)},
	{KindVersion, ".tool-versions", capture(asdfTool)},
	{KindVersion, "go.mod", capture(goDirective)},
	{KindVersion, "rust-toolchain.toml", capture(rustChannel)},
	{KindVersion, "package.json", capture(nodeEngines)},

	// The Dockerfile's own presence is the signal, and its first FROM line is
	// what the skill would build the project's image on.
	{KindDockerfile, "Dockerfile", capture(dockerfileFrom)},

	// CI is the signal — the runner is this package's constant — because a
	// repository with a pipeline is one whose image has to carry what the
	// pipeline runs.
	{KindCI, ".github/workflows/ci.yml", presence("github-actions")},
	{KindCI, ".gitlab-ci.yml", presence("gitlab")},
	{KindCI, "bitbucket-pipelines.yml", presence("bitbucket")},
	{KindCI, "Jenkinsfile", presence("jenkins")},
	{KindCI, ".circleci/config.yml", presence("circleci")},
	{KindCI, "azure-pipelines.yml", presence("azure")},

	// A compose file's service names are what the catalog entry's own services:
	// list is written from (D1904), and only the names images/common's
	// fugaro-services knows are answered with: the words of the file itself
	// never reach the report.
	{KindCompose, "docker-compose.yml", serviceHints},
	{KindCompose, "docker-compose.yaml", serviceHints},
	{KindCompose, "compose.yaml", serviceHints},
	{KindCompose, "compose.yml", serviceHints},

	// Testcontainers is the same hint from a build file: the marker alone is
	// the signal, and the services named beside it are the value.
	{KindTestcontainers, "pom.xml", testcontainersHints},
	{KindTestcontainers, "build.gradle", testcontainersHints},
	{KindTestcontainers, "build.gradle.kts", testcontainersHints},
	{KindTestcontainers, "requirements.txt", testcontainersHints},
	{KindTestcontainers, "package.json", testcontainersHints},
}

// Signals is the fixed list of what the survey reads, as a copy of the rules'
// own paths: a caller cannot change the read by writing to the answer.
func Signals() []Signal {
	out := make([]Signal, 0, len(rules))
	for _, r := range rules {
		out = append(out, Signal{Kind: r.kind, Path: r.path})
	}
	return out
}

// Findings judges the files a repository answered with against the fixed rules
// and answers the signals, in the rules' own order. A path the caller did not
// read — because the file is not there — contributes nothing: the survey's
// answer is what a repository HAS, and a missing file is not a mistake of the
// reader.
func Findings(files []File) []Finding {
	byPath := make(map[string][]byte, len(files))
	for _, f := range files {
		byPath[f.Path] = f.Data
	}
	out := make([]Finding, 0, len(rules))
	for _, r := range rules {
		data, ok := byPath[r.path]
		if !ok {
			continue
		}
		value, ok := r.read(data)
		if !ok {
			continue
		}
		// Every value leaves this package through Printable, whatever the rule
		// that produced it: a rule bounds its own value by shape, and this is
		// the one place that bounds the text that reaches a report, a terminal
		// or an agent, so a rule added later cannot forget it.
		out = append(out, Finding{Kind: r.kind, Path: r.path, Value: pluginwire.Printable(value)})
	}
	return out
}

// Read reads every signal of the fixed list out of r, at ref ("" for the
// repository's default branch), and answers the findings.
//
// A path whose read fails contributes nothing: ContentsReader answers an error
// both for a file that is not there and for a read that could not be made —
// the two are one error to this interface, and a survey is not the place to
// decide a repository cannot be read from a file it is allowed not to have. A
// caller that needs to tell those apart reads the repository's own state
// elsewhere; what this returns is the signals it has. A cancelled context is
// the one failure that is the caller's own, and it stops the read.
func Read(ctx context.Context, r gitprov.ContentsReader, ref string) ([]Finding, error) {
	files := make([]File, 0, len(rules))
	for _, rule := range rules {
		data, err := r.Contents(ctx, rule.path, ref)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			continue
		}
		files = append(files, File{Path: rule.path, Data: data})
	}
	return Findings(files), nil
}

// presence is a rule whose answer is the file's own existence, with value as
// the kind's constant.
func presence(value string) func([]byte) (string, bool) {
	return func([]byte) (string, bool) { return value, true }
}

// capture is a rule whose answer is the first submatch of re, and nothing when
// the text does not match: the token a report carries is one this package
// bounded by shape, never a line of someone else's file.
func capture(re *regexp.Regexp) func([]byte) (string, bool) {
	return func(data []byte) (string, bool) {
		m := re.FindSubmatch(data)
		if m == nil {
			return "", false
		}
		return string(m[1]), true
	}
}

// serviceHints is a compose rule: the services a compose file names, from the
// closed list below, deduplicated and sorted. A compose file that names none of
// them contributes no finding — the file's own presence is not a signal, since
// what the skill proposes is the service, not the file.
func serviceHints(data []byte) (string, bool) {
	return serviceWordsIn(data, false)
}

// testcontainersHints is a Testcontainers rule: the marker in a build file is
// the signal, and the services named beside it are the value. A file with no
// marker contributes nothing.
func testcontainersHints(data []byte) (string, bool) {
	return serviceWordsIn(data, true)
}

// services are the names images/common/fugaro-services starts (F12), each with
// the words a repository writes it as — compiled once, and matched against the
// file's bytes and nothing else. Only these are ever answered with: a service
// a repository names that Fugaro has no starter for is not one this survey can
// propose, so no word of the file ever reaches the report.
var services = []struct {
	service string
	words   []*regexp.Regexp
}{
	{"postgres", []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bpostgres\b`),
		regexp.MustCompile(`(?i)\bpostgresql\b`),
	}},
	{"redis", []*regexp.Regexp{regexp.MustCompile(`(?i)\bredis\b`)}},
	{"firebase-emulator", []*regexp.Regexp{regexp.MustCompile(`(?i)\bfirebase\b`)}},
}

var testcontainersMarker = regexp.MustCompile(`(?i)\btestcontainers\b`)

// serviceWordsIn answers the closed-list services the text names. marker is
// whether the answer requires testcontainersMarker to be there as well.
func serviceWordsIn(data []byte, marker bool) (string, bool) {
	if marker && !testcontainersMarker.Match(data) {
		return "", false
	}
	found := make([]string, 0, len(services))
	for _, s := range services {
		if slices.ContainsFunc(s.words, func(re *regexp.Regexp) bool { return re.Match(data) }) {
			found = append(found, s.service)
		}
	}
	if len(found) == 0 {
		// The file is there and names no service this survey knows: the kind
		// answers "" rather than nothing, so the report still shows that the
		// repository has a compose file (or a Testcontainers build).
		return "", true
	}
	slices.Sort(found)
	return strings.Join(found, ","), true
}

// The version rules. Each captures one token with a version's or a
// reference's own alphabet, so what a version file contributes is a version
// and never the line it was written on.
var (
	// nodeVersion: .nvmrc, "20", "v20.11.0" or "lts/*"'s numeric half.
	nodeVersion = regexp.MustCompile(`(?m)^\s*v?([0-9]+(?:\.[0-9]+){0,2})\s*$`)
	// pythonVersion: .python-version, "3.12" or "3.12.1".
	pythonVersion = regexp.MustCompile(`(?m)^([0-9]+\.[0-9]+(?:\.[0-9]+)?)\s*$`)
	// asdfTool: .tool-versions, "nodejs 20.11.0" — one tool and its version.
	asdfTool = regexp.MustCompile(`(?m)^([A-Za-z0-9_.+-]{1,32} +[0-9][0-9A-Za-z_.+-]{0,31})\s*$`)
	// goDirective: go.mod's "go 1.23" line.
	goDirective = regexp.MustCompile(`(?m)^go ([0-9]+\.[0-9]+(?:\.[0-9]+)?)$`)
	// rustChannel: rust-toolchain.toml's own channel, a release or one of the
	// three names a toolchain file may carry.
	rustChannel = regexp.MustCompile(`(?m)^\s*channel\s*=\s*"([0-9]+(?:\.[0-9]+){0,2}|stable|beta|nightly)"`)
	// nodeEngines: package.json's engines.node range, which is a range and not
	// a version: its own alphabet, bounded in length.
	nodeEngines = regexp.MustCompile(`"node"\s*:\s*"([A-Za-z0-9.^~<>=*|x -]{1,32})"`)
	// dockerfileFrom: the first FROM line's own reference — a registry host, a
	// path, a tag and a digest, which is everything a base image reference may
	// carry.
	dockerfileFrom = regexp.MustCompile(`(?mi)^FROM\s+([A-Za-z0-9][A-Za-z0-9._/:@-]{0,127})`)
)
