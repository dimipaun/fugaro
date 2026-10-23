package cli

// Dependabot's watch over the console's npm dependencies, staged as
// docs/pending-workflows/dependabot-console.yml (desktop-console plan, open
// item 5; the owner's ruling of 2026-10-22).
//
// The file is the **whole of `.github/dependabot.yml`** as it should read once
// the console is watched, so its copy-in is a `cp` over `.github/dependabot.yml`
// and nothing is written to a path named after it. It is staged rather than
// applied because the plan's open item 5 reserved that decision for the owner --
// `.github/dependabot.yml` is not under `.github/workflows/`, so an agent run
// may write it, and the ruling is "stage it, with the copy-in step, and let them
// apply it".
//
// What this test pins, in both states the tree can be in:
//
//   - the staged file's content: two update entries -- the `github-actions` one
//     that runs today, unchanged, and the `npm` one for `/console`, weekly and
//     grouped so that a week's minor and patch bumps arrive as one pull request
//     (a major stays outside the group and arrives on its own);
//   - the staging state: while the staged copy is here, `.github/dependabot.yml`
//     must NOT already carry the console entry, and once the copy-in has removed
//     it the file that runs must;
//   - the section of docs/pending-workflows/README.md that describes it,
//     including the two commands of its copy-in, and the `**Status: ...**` line
//     that has to move with the copy-in;
//   - the site's README, which names the staged files and is read in both
//     directions by `website/test/readme.mjs`: while this file is staged the
//     site has to name it, and after the copy-in the name has to be gone.
//
// What it cannot pin is what Dependabot does with the file: the service is
// GitHub's, it is not run from CI, and nothing in this repository observes the
// pull requests it opens.

import (
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	stagedDependabotConsolePath = "docs/pending-workflows/dependabot-console.yml"
	appliedDependabotPath       = ".github/dependabot.yml"
	pendingWorkflowsReadme      = "docs/pending-workflows/README.md"
	websiteReadme               = "website/README.md"
)

// dependabotFile is the part of the file this test reads: the schema version and
// the update entries. Everything else Dependabot accepts is out of scope here.
type dependabotFile struct {
	Version int `yaml:"version"`
	Updates []struct {
		PackageEcosystem string `yaml:"package-ecosystem"`
		Directory        string `yaml:"directory"`
		Schedule         struct {
			Interval string `yaml:"interval"`
		} `yaml:"schedule"`
		Groups map[string]struct {
			Patterns    []string `yaml:"patterns"`
			UpdateTypes []string `yaml:"update-types"`
		} `yaml:"groups"`
	} `yaml:"updates"`
}

// dependabotEntry marshals one update entry back to YAML, so two entries can be
// compared without spelling every field twice.
func dependabotEntry(t *testing.T, update any) string {
	t.Helper()
	out, err := yaml.Marshal(update)
	if err != nil {
		t.Fatalf("an update entry of %s does not marshal back to YAML: %v", appliedDependabotPath, err)
	}
	return string(out)
}

func readDependabotYAML(t *testing.T, path string) (string, dependabotFile, []any) {
	t.Helper()
	text := readDoc(t, path)
	var parsed struct {
		Version int   `yaml:"version"`
		Updates []any `yaml:"updates"`
	}
	if err := yaml.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("%s is not YAML this test can read: %v", path, err)
	}
	file := dependabotFile{Version: parsed.Version}
	if err := yaml.Unmarshal([]byte(text), &file); err != nil {
		t.Fatalf("%s's updates are not the shape this test reads: %v", path, err)
	}
	return text, file, parsed.Updates
}

func TestDependabotConsoleStagingIsPinned(t *testing.T) {
	// The file this test reads its claims from: the staged one while the change
	// is pending, the applied one once the owner has copied it in. The staging
	// clause below is what keeps the two from being confused.
	staged := docExists(t, stagedDependabotConsolePath)
	source := stagedDependabotConsolePath
	if !staged {
		source = appliedDependabotPath
	}
	text, file, rawUpdates := readDependabotYAML(t, source)

	// The schema version, and exactly two updates: the actions entry that runs
	// today and the console's npm entry.
	if file.Version != 2 {
		t.Errorf("%s says version %d, want 2: Dependabot's configuration schema", source, file.Version)
	}
	if len(file.Updates) != 2 {
		t.Fatalf("%s carries %d update entries, want exactly 2 (the github-actions entry and the console's npm entry): %s",
			source, len(file.Updates), text)
	}

	// The first is the one that runs today, untouched: a weekly `github-actions`
	// entry at the repository root. `images/cloudbuild_test.go` asserts the file
	// that runs has one, and it has to keep having one after the copy-in.
	actions := file.Updates[0]
	if actions.PackageEcosystem != "github-actions" || actions.Directory != "/" || actions.Schedule.Interval != "weekly" {
		t.Errorf("%s's first update is %+v, want the github-actions entry at / on a weekly schedule, unchanged",
			source, actions)
	}
	if len(actions.Groups) != 0 {
		t.Errorf("%s's github-actions entry gained groups: %v -- only the console's entry is grouped, and the actions entry is left as it runs today", source, actions.Groups)
	}

	// The second is the console's: npm, `/console`, weekly, and grouped with
	// minor and patch bumps in one group.
	console := file.Updates[1]
	if console.PackageEcosystem != "npm" {
		t.Errorf("%s's second update is %q, want npm: the console's dependencies are the one npm package in this repository", source, console.PackageEcosystem)
	}
	if console.Directory != "/console" {
		t.Errorf("%s's npm entry watches %q, want /console: the console's package directory, not the repository root", source, console.Directory)
	}
	if console.Schedule.Interval != "weekly" {
		t.Errorf("%s's npm entry runs %q, want weekly: the plan's and the ruling's cadence", source, console.Schedule.Interval)
	}
	group, ok := console.Groups["console"]
	if !ok {
		t.Fatalf("%s's npm entry is not grouped (groups = %v): a week's bumps are meant to arrive as one pull request", source, console.Groups)
	}
	if len(group.Patterns) != 1 || group.Patterns[0] != "*" {
		t.Errorf("%s's console group has patterns %v, want exactly [\"*\"]: every console dependency is in it", source, group.Patterns)
	}
	if len(group.UpdateTypes) != 2 || group.UpdateTypes[0] != "minor" || group.UpdateTypes[1] != "patch" {
		t.Errorf("%s's console group has update-types %v, want exactly [minor patch]: a major (Electron's above all) stays outside the group and arrives on its own", source, group.UpdateTypes)
	}

	// No credential of any kind, and no shape that could be one: this file holds
	// no token, and a `dependabot.yml` that gained one would be a secret in the
	// tree rather than a permission.
	for _, forbidden := range []string{"secrets.", "-----BEGIN", "token:", "password"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("%s contains %q: this file configures the service and holds no credential", source, forbidden)
		}
	}
	for _, blob := range consoleBlob.FindAllString(text, -1) {
		if consoleWord.MatchString(blob) {
			continue
		}
		t.Errorf("%s carries %q, which is long enough and shaped like a pasted credential: this file holds no secret", source, blob)
	}

	// The staging state, in both directions: while the staged copy is here the
	// file that runs must not already carry the console entry (a copy-in that had
	// happened would leave a staged file claiming a change that is in), and once
	// it is gone the file that runs must carry it.
	appliedText, applied, appliedUpdates := readDependabotYAML(t, appliedDependabotPath)
	appliedConsole := appliedUpdates[len(appliedUpdates)-1]
	if staged {
		if len(applied.Updates) != 1 {
			t.Errorf("%s carries %d update entries while %s is staged, want 1: the console's entry is the pending change, and a file that already has it makes the staged copy a lie (%s)",
				appliedDependabotPath, len(applied.Updates), stagedDependabotConsolePath, appliedText)
		}
		// And the entry that runs is the staged file's own first entry, byte for
		// byte as YAML: the staged file is that file plus the console's entry, so
		// the copy-in changes Dependabot's scope and nothing else.
		if got, want := dependabotEntry(t, appliedConsole), dependabotEntry(t, rawUpdates[0]); got != want {
			t.Errorf("%s's entry is not the staged file's first entry: the staged copy is the running file plus the console's npm entry, and nothing else changes\n got %s\nwant %s",
				appliedDependabotPath, got, want)
		}
		// The staged file has to carry its own copy-in: it is the file the owner
		// has open when they apply it.
		for _, line := range []string{
			"cp docs/pending-workflows/dependabot-console.yml .github/dependabot.yml",
			"git rm docs/pending-workflows/dependabot-console.yml",
		} {
			if !strings.Contains(text, line) {
				t.Errorf("%s's header lacks the copy-in line %q: the file is what the owner has open, and its target is .github/dependabot.yml rather than a path named after it", stagedDependabotConsolePath, line)
			}
		}
	} else if !strings.Contains(appliedText, "directory: /console") {
		t.Errorf("%s does not watch /console, and %s is gone: the copy-in has been applied, so the file that runs has to carry the entry the staged file described",
			appliedDependabotPath, stagedDependabotConsolePath)
	}

	// The section of docs/pending-workflows/README.md: what the file is -- a
	// settings file whose copy-in lands on `.github/dependabot.yml`, not a
	// workflow -- the two commands that apply it, and the test that pins it.
	doc := readDoc(t, pendingWorkflowsReadme)
	section := consoleReadmeSection(t, doc, "## `dependabot-console.yml`")
	for _, want := range []string{
		"cp docs/pending-workflows/dependabot-console.yml .github/dependabot.yml",
		"git rm docs/pending-workflows/dependabot-console.yml",
		"dependabot.yml",
		"/console",
		"weekly",
		"groups",
		"update-types",
		"TestDependabotConsoleStagingIsPinned",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's `dependabot-console.yml` section lacks %q: the owner is the one who copies it in, and this is where they read what it is, where it lands and what pins it", pendingWorkflowsReadme, want)
		}
	}

	// The `**Status: ...**` sentence is the machine-readable half of the claim,
	// and it has to move with the copy-in -- the same rule `shared-tests.yml` and
	// `mobile-release.yml` carry, in the same two words. Read from the whole
	// document and held to this file's own section, so a status sentence about
	// another file cannot satisfy it.
	statuses := dependabotConsoleStatusRE.FindAllString(doc, -1)
	if len(statuses) != 1 {
		t.Fatalf("%s carries %d `**Status: `dependabot-console.yml` is ...**` sentences, want exactly one: two of them cannot both be the state of this file", pendingWorkflowsReadme, len(statuses))
	}
	if !strings.Contains(section, statuses[0]) {
		t.Errorf("%s's status sentence for this file (%q) is not in its own section: the section is where a reader looks", pendingWorkflowsReadme, statuses[0])
	}
	wantStatus := "**Status: `dependabot-console.yml` is applied, not pending.**"
	if staged {
		wantStatus = "**Status: `dependabot-console.yml` is staged and pending.**"
	}
	if statuses[0] != wantStatus {
		t.Errorf("%s's status sentence is %q and the staged file is %v: it has to say %q (copying the file in without updating that sentence leaves the document describing a staged copy that is gone)",
			pendingWorkflowsReadme, statuses[0], staged, wantStatus)
	}

	// The site's README, in both directions: `website/test/readme.mjs` reads the
	// count and the set of names against the directory, so while this file is
	// staged the site has to name it, and after the copy-in the name has to be
	// gone (the site's own test is the other half of this check; this one says so
	// from the Go side, where the staging state is what decides it).
	site := readDoc(t, websiteReadme)
	named := strings.Contains(site, "`docs/pending-workflows/dependabot-console.yml`")
	if staged && !named {
		t.Errorf("%s does not name docs/pending-workflows/dependabot-console.yml while the file is staged: a staged change nobody was told about is what website/test/readme.mjs refuses", websiteReadme)
	}
	if !staged && named {
		t.Errorf("%s still names docs/pending-workflows/dependabot-console.yml after the copy-in removed it: the site's paragraph has to move with the copy-in", websiteReadme)
	}
}

// dependabotConsoleStatusRE is the machine-readable status sentence, in the two
// wordings the other staged files use: the copy-in changes it, and nothing else
// about it is free.
var dependabotConsoleStatusRE = regexp.MustCompile("\\*\\*Status: `dependabot-console\\.yml` is (staged and pending|applied, not pending)\\.\\*\\*")
