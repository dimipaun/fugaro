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
//     including the two commands of its copy-in, the sentence the copy-in has to
//     write in its place and the `**Status: ...**` opening line itself;
//   - the site's README, which names the staged files and is read in both
//     directions by `website/test/readme.mjs`: while this file is staged the
//     site has to name it, and after the copy-in the name has to be gone.
//
// What it cannot pin is what Dependabot does with the file: the service is
// GitHub's, it is not run from CI, and nothing in this repository observes the
// pull requests it opens.

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	stagedDependabotConsolePath = "docs/pending-workflows/dependabot-console.yml"
	appliedDependabotPath       = ".github/dependabot.yml"
	pendingWorkflowsReadme      = "docs/pending-workflows/README.md"
	websiteReadme               = "website/README.md"

	// The two wordings of the machine-readable status line, as
	// `shared-tests.yml` and `mobile-release.yml` write them: the copy-in
	// changes the first into the second and nothing else about it is free.
	dependabotConsoleStagedStatus  = "**Status: `dependabot-console.yml` is staged and pending.**"
	dependabotConsoleAppliedStatus = "**Status: `dependabot-console.yml` is applied, not pending.**"

	dependabotConsoleReadmeHeading = "## `dependabot-console.yml`"
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

// TestDependabotConsoleStagingIsPinned reads the three documents the change is
// spread over -- the file, the README section that carries its copy-in, and the
// site's paragraph -- and holds them to the tree. The rules live in
// dependabotConsoleStagingProblems so that both states, including the one this
// tree is never in while an agent is working, can be pinned against fixtures.
func TestDependabotConsoleStagingIsPinned(t *testing.T) {
	staged := docExists(t, stagedDependabotConsolePath)
	stagedText := ""
	if staged {
		stagedText = readDoc(t, stagedDependabotConsolePath)
	}
	for _, problem := range dependabotConsoleStagingProblems(
		staged, stagedText, readDoc(t, appliedDependabotPath),
		readDoc(t, pendingWorkflowsReadme), readDoc(t, websiteReadme)) {
		t.Error(problem)
	}
}

// TestDependabotConsoleStagingStateIsReadableInEveryState pins the rules above
// against both states the two files can be in, and against documents that break
// one rule each. It matters here more than it usually would: the state the
// copy-in leaves -- staged copy gone, the console entry in the file that runs --
// is one this repository is never in while an agent is working, because
// `git rm`-ing the staged file and copying it over `.github/dependabot.yml` is
// the owner's step. Without this test that half of the check would be written
// and never run, and the copy-in it describes would be the first thing to find
// out whether it was right -- which is how a documented copy-in ends up leaving
// a red test behind it.
func TestDependabotConsoleStagingStateIsReadableInEveryState(t *testing.T) {
	if !docExists(t, stagedDependabotConsolePath) {
		t.Skip("the staged copy is gone, so the applied half of these rules IS this tree's state and TestDependabotConsoleStagingIsPinned reads it directly; what this test is for is the state a tree with the file staged is never in")
	}
	stagedYAML := readDoc(t, stagedDependabotConsolePath)
	appliedYAML := readDoc(t, appliedDependabotPath)
	readme := readDoc(t, pendingWorkflowsReadme)
	site := readDoc(t, websiteReadme)

	// The state the copy-in leaves, built from the real documents: the staged
	// file's own content is what the `cp` puts in `.github/dependabot.yml` (its
	// header comments included -- they are comments, and the file is the whole of
	// the target), the staged copy is gone, the section's opening line has been
	// rewritten, and the site names the file no longer.
	appliedAfterCopyIn := stagedYAML
	readmeAfterCopyIn := strings.Replace(readme, dependabotConsoleStagedStatus, dependabotConsoleAppliedStatus, 1)
	siteAfterCopyIn := "the site's paragraph counts the staged files, and names none of them here"

	// Both real states, as they are described: the tree one with the documents
	// that are in it, the applied one with the documents the copy-in leaves.
	if problems := dependabotConsoleStagingProblems(true, stagedYAML, appliedYAML, readme, site); len(problems) != 0 {
		t.Errorf("this tree's own state is reported as inconsistent: %v", problems)
	}
	if problems := dependabotConsoleStagingProblems(false, "", appliedAfterCopyIn, readmeAfterCopyIn, siteAfterCopyIn); len(problems) != 0 {
		t.Errorf("the state the documented copy-in leaves is reported as inconsistent, so the instructions and the test disagree with each other: %v", problems)
	}

	// ... and each rule broken on purpose, one at a time, so a rule that refused
	// nothing would fail here rather than look strict. Each case starts from the
	// consistent documents for its state (the fields that are set are the
	// overrides), and `want` is a phrase from the problem's own message: what is
	// pinned is which disagreement was found, not the wording that reports it.
	for _, tc := range []struct {
		name         string
		staged       bool
		stagedText   string
		appliedText  string
		readme, site string
		want         string
	}{
		{
			name:   "the copy-in is applied but the section still opens with the staged line",
			readme: readme,
			want:   "does not open with",
		},
		{
			name:        "the copy-in is applied but the file that runs has no console entry",
			appliedText: appliedYAML,
			want:        "want exactly 2",
		},
		{
			name:       "the staged file's npm entry is not npm",
			staged:     true,
			stagedText: strings.Replace(stagedYAML, "  - package-ecosystem: npm", "  - package-ecosystem: pip", 1),
			want:       "want npm",
		},
		{
			name:       "the staged file's group lost its update types",
			staged:     true,
			stagedText: strings.Replace(stagedYAML, "        update-types:\n          - minor\n          - patch\n", "", 1),
			want:       "update-types",
		},
		{
			name: "the site still names the file the copy-in removed",
			site: "a site README that names `docs/pending-workflows/dependabot-console.yml` still",
			want: "still names",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stagedText, appliedText := stagedYAML, appliedYAML
			readmeText, siteText := readme, site
			if !tc.staged {
				stagedText, appliedText = "", appliedAfterCopyIn
				readmeText, siteText = readmeAfterCopyIn, siteAfterCopyIn
			}
			for _, override := range []struct {
				given string
				dst   *string
			}{
				{tc.stagedText, &stagedText},
				{tc.appliedText, &appliedText},
				{tc.readme, &readmeText},
				{tc.site, &siteText},
			} {
				if override.given != "" {
					*override.dst = override.given
				}
			}
			problems := dependabotConsoleStagingProblems(tc.staged, stagedText, appliedText, readmeText, siteText)
			for _, problem := range problems {
				if strings.Contains(problem, tc.want) {
					return
				}
			}
			t.Errorf("expected a problem mentioning %q, got %v", tc.want, problems)
		})
	}
}

// dependabotConsoleStagingProblems returns every disagreement between the three
// documents and the state the tree is in, as messages rather than as test
// failures: taking the state and the documents as arguments is what lets the
// test above pin the applied half of the rules with fixtures.
//
// `staged` is the state of the staged copy, which is what decides which document
// the content claims are read from: while it exists the staged file is the
// reviewed one, and once it is gone the staged file's content IS the content of
// the file that runs.
func dependabotConsoleStagingProblems(staged bool, stagedText, appliedText, readme, site string) []string {
	var problems []string
	bad := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	// The document the claims are read from, and the name to report it by.
	source, text := appliedDependabotPath, appliedText
	if staged {
		source, text = stagedDependabotConsolePath, stagedText
	}

	var raw struct {
		Version int   `yaml:"version"`
		Updates []any `yaml:"updates"`
	}
	var file dependabotFile
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		return append(problems, fmt.Sprintf("%s is not YAML this test can read: %v", source, err))
	}
	if err := yaml.Unmarshal([]byte(text), &file); err != nil {
		return append(problems, fmt.Sprintf("%s's updates are not the shape this test reads: %v", source, err))
	}

	// The schema version, and exactly two updates: the actions entry that runs
	// today and the console's npm entry.
	if file.Version != 2 {
		bad("%s says version %d, want 2: Dependabot's configuration schema", source, file.Version)
	}
	if len(file.Updates) != 2 {
		return append(problems, fmt.Sprintf("%s carries %d update entries, want exactly 2 (the github-actions entry and the console's npm entry): %s",
			source, len(file.Updates), text))
	}

	// The first is the one that runs today, untouched: a weekly `github-actions`
	// entry at the repository root. `images/cloudbuild_test.go` asserts the file
	// that runs has one, and it has to keep having one after the copy-in.
	actions := file.Updates[0]
	if actions.PackageEcosystem != "github-actions" || actions.Directory != "/" || actions.Schedule.Interval != "weekly" {
		bad("%s's first update is %+v, want the github-actions entry at / on a weekly schedule, unchanged", source, actions)
	}
	if len(actions.Groups) != 0 {
		bad("%s's github-actions entry gained groups: %v -- only the console's entry is grouped, and the actions entry is left as it runs today", source, actions.Groups)
	}

	// The second is the console's: npm, `/console`, weekly, and grouped with
	// minor and patch bumps in one group.
	console := file.Updates[1]
	if console.PackageEcosystem != "npm" {
		bad("%s's second update is %q, want npm: the console's dependencies are what the ruling takes, and the entry names their ecosystem", source, console.PackageEcosystem)
	}
	if console.Directory != "/console" {
		bad("%s's npm entry watches %q, want /console: the console's own package directory, which is the scope the owner's ruling takes (this repository ships several npm packages -- shared/, website/, mobile/, the sandbox, the image harnesses and the budget emulator -- and none of the others is in it)", source, console.Directory)
	}
	if console.Schedule.Interval != "weekly" {
		bad("%s's npm entry runs %q, want weekly: the plan's and the ruling's cadence", source, console.Schedule.Interval)
	}
	group, ok := console.Groups["console"]
	if !ok {
		bad("%s's npm entry is not grouped (groups = %v): a week's bumps are meant to arrive as one pull request", source, console.Groups)
	} else {
		if len(group.Patterns) != 1 || group.Patterns[0] != "*" {
			bad("%s's console group has patterns %v, want exactly [\"*\"]: every console dependency is in it", source, group.Patterns)
		}
		if len(group.UpdateTypes) != 2 || group.UpdateTypes[0] != "minor" || group.UpdateTypes[1] != "patch" {
			bad("%s's console group has update-types %v, want exactly [minor patch]: a major (Electron's above all) stays outside the group and arrives on its own", source, group.UpdateTypes)
		}
	}

	// No credential of any kind, and no shape that could be one: this file holds
	// no token, and a `dependabot.yml` that gained one would be a secret in the
	// tree rather than a permission.
	for _, forbidden := range []string{"secrets.", "-----BEGIN", "token:", "password"} {
		if strings.Contains(text, forbidden) {
			bad("%s contains %q: this file configures the service and holds no credential", source, forbidden)
		}
	}
	for _, blob := range consoleBlob.FindAllString(text, -1) {
		if consoleWord.MatchString(blob) {
			continue
		}
		bad("%s carries %q, which is long enough and shaped like a pasted credential: this file holds no secret", source, blob)
	}

	// The staging state, in both directions: while the staged copy is here the
	// file that runs must not already carry the console entry (a copy-in that had
	// happened would leave a staged file claiming a change that is in), and once
	// it is gone the file that runs must carry it.
	var appliedRaw struct {
		Updates []any `yaml:"updates"`
	}
	var applied dependabotFile
	if err := yaml.Unmarshal([]byte(appliedText), &appliedRaw); err != nil {
		bad("%s is not YAML this test can read: %v", appliedDependabotPath, err)
	} else if err := yaml.Unmarshal([]byte(appliedText), &applied); err != nil {
		bad("%s's updates are not the shape this test reads: %v", appliedDependabotPath, err)
	} else if len(appliedRaw.Updates) == 0 {
		bad("%s carries no update entry at all: the `github-actions` entry is what runs today, and this test reads the file that runs to tell a staged change from an applied one", appliedDependabotPath)
	} else if staged {
		if len(applied.Updates) != 1 {
			bad("%s carries %d update entries while %s is staged, want 1: the console's entry is the pending change, and a file that already has it makes the staged copy a lie (%s)",
				appliedDependabotPath, len(applied.Updates), stagedDependabotConsolePath, appliedText)
		} else if got, want := marshalDependabotEntry(appliedRaw.Updates[0]), marshalDependabotEntry(raw.Updates[0]); got == "" || want == "" || got != want {
			// The entry that runs is the staged file's own first entry, YAML for
			// YAML: the staged file is that file plus the console's entry, so the
			// copy-in changes Dependabot's scope and nothing else. An entry that
			// does not marshal back is reported as a problem rather than compared,
			// so a failure on both sides cannot pass by looking alike.
			bad("%s's entry is not the staged file's first entry: the staged copy is the running file plus the console's npm entry, and nothing else changes\n got %s\nwant %s",
				appliedDependabotPath, got, want)
		}
	}
	if staged {
		// The staged file has to carry its own copy-in: it is the file the owner
		// has open when they apply it.
		for _, line := range []string{
			"cp docs/pending-workflows/dependabot-console.yml .github/dependabot.yml",
			"git rm docs/pending-workflows/dependabot-console.yml",
		} {
			if !strings.Contains(text, line) {
				bad("%s's header lacks the copy-in line %q: the file is what the owner has open, and its target is .github/dependabot.yml rather than a path named after it", stagedDependabotConsolePath, line)
			}
		}
	} else if !strings.Contains(appliedText, "directory: /console") {
		bad("%s does not watch /console, and %s is gone: the copy-in has been applied, so the file that runs has to carry the entry the staged file described",
			appliedDependabotPath, stagedDependabotConsolePath)
	}

	// The section of docs/pending-workflows/README.md: what the file is -- a
	// settings file whose copy-in lands on `.github/dependabot.yml`, not a
	// workflow -- the two commands that apply it, the sentence the copy-in writes
	// in place of the opening one, and the test that pins it.
	section, ok := dependabotConsoleReadmeSection(readme, dependabotConsoleReadmeHeading)
	if !ok {
		bad("%s has no %q section: the owner is the one who copies the file in, and this is where they read how", pendingWorkflowsReadme, dependabotConsoleReadmeHeading)
	} else {
		for _, want := range []string{
			"cp docs/pending-workflows/dependabot-console.yml .github/dependabot.yml",
			"git rm docs/pending-workflows/dependabot-console.yml",
			dependabotConsoleAppliedStatus,
			"dependabot.yml",
			"/console",
			"weekly",
			"groups",
			"update-types",
			"TestDependabotConsoleStagingIsPinned",
		} {
			if !strings.Contains(section, want) {
				bad("%s's `dependabot-console.yml` section lacks %q: the owner is the one who copies it in, and this is where they read what it is, where it lands, what the copy-in has to rewrite and what pins it", pendingWorkflowsReadme, want)
			}
		}

		// The section's **opening** line, not the section: the copy-in
		// instructions above quote the applied wording -- that is what
		// `dependabotConsoleAppliedStatus` in the list is -- so a search over the
		// whole section would be satisfied by the quote and would stay green with
		// the live line saying the opposite. The same rule `mobile-release.yml`'s
		// section is held to.
		want := dependabotConsoleAppliedStatus
		if staged {
			want = dependabotConsoleStagedStatus
		}
		// `section` is already the text after the heading, so the body starts at
		// its own first non-blank character -- the status line, if the document is
		// the one this state calls for.
		body := strings.TrimSpace(section)
		if !strings.HasPrefix(body, want) {
			bad("%s's `%s` section does not open with %s %s: the copy-in rewrites that line in the same commit, and a section opening with the other wording describes a staged copy that is or is not there",
				pendingWorkflowsReadme, dependabotConsoleReadmeHeading, want,
				map[bool]string{true: "while " + stagedDependabotConsolePath + " is still here", false: "now that " + stagedDependabotConsolePath + " is gone"}[staged])
		}
	}

	// The site's README, in both directions: `website/test/readme.mjs` reads the
	// count and the set of names against the directory, so while this file is
	// staged the site has to name it, and after the copy-in the name has to be
	// gone (the site's own test is the other half of this check; this one says so
	// from the Go side, where the staging state is what decides it).
	named := strings.Contains(site, "`docs/pending-workflows/dependabot-console.yml`")
	if staged && !named {
		bad("%s does not name docs/pending-workflows/dependabot-console.yml while the file is staged: a staged change nobody was told about is what website/test/readme.mjs refuses", websiteReadme)
	}
	if !staged && named {
		bad("%s still names docs/pending-workflows/dependabot-console.yml after the copy-in removed it: the site's paragraph has to move with the copy-in", websiteReadme)
	}
	return problems
}

// dependabotConsoleReadmeSection is `consoleReadmeSection` without the failure,
// and with the one difference this document needs: the heading is looked for at
// the START OF A LINE. Searching for the bare string finds the sentence in the
// opening line that says "see `## dependabot-console.yml` below" first, and the
// "section" it returns then runs from line 7 of the document to the next heading
// -- a chunk wide enough to satisfy every `strings.Contains` below while proving
// nothing. The text of the section, and whether the document has it at all, are
// returned together so the checks above can report a missing section as a
// problem rather than stopping the test.
func dependabotConsoleReadmeSection(doc, heading string) (string, bool) {
	start := strings.Index(doc, "\n"+heading)
	if start < 0 {
		return "", false
	}
	rest := doc[start+1+len(heading):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		return rest[:end], true
	}
	return rest, true
}

// marshalDependabotEntry marshals one update entry back to YAML, so two entries
// can be compared without spelling every field twice. It returns "" rather than
// an error for an entry that does not marshal, and the caller treats that as a
// disagreement: two entries that both fail to marshal must not compare equal.
func marshalDependabotEntry(update any) string {
	out, err := yaml.Marshal(update)
	if err != nil {
		return ""
	}
	return string(out)
}
