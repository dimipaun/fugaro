package cli

// Task P4.4 of docs/mobile/implementation-plan.md -- "The EAS project, the
// signing keys and the store entries -- owner-run" -- is the one task in the
// mobile plan that **no agent performs a step of**, and its verification is
// Check 89 in the live register (docs/gcp-live-checklist.md, written by P4.2a).
// docs/mobile/eas-signing-runbook.md is the runbook the owner follows at their
// own terminal: the Expo/EAS project, the Apple and Google prerequisites, where
// each signing credential lives, the protected environment a future build
// workflow would read, and the rollback (D120, Global Constraint 9).
//
// There is nothing here to run against a fake. No agent runs `eas build` or
// `eas submit`, no signing key or store credential ever enters an agent's
// environment, and no CLI command implements a step of the runbook -- so the
// runbook's truth is a document's truth, and these are the presence tests every
// docs_*_test.go in this package writes. They hold five claims:
//
//   - TestDocsMobileEasRunbookNamesTheTaskAndTheCheck: the runbook is the one
//     the plan's P4.4 asks for, and it points at the register's Check 89
//     instead of restating it (P4.2a writes that text; PR #580 carries it, and
//     the runbook must not contradict it).
//   - TestDocsMobileEasRunbookStatesEveryOwnerOnlyWarning: each thing the
//     owner must not delegate is written down where a reader meets the step,
//     because "no agent performs any step" is the whole of Global Constraint 9.
//   - TestDocsMobileEasRunbookNamesEveryCredentialAndWhereItLives: every
//     credential the path needs is named, and each is placed (EAS's servers,
//     the owner's machine, or nowhere).
//   - TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape:
//     the repository-side settings, described rather than written -- no
//     workflow file is created here (Global Constraint 8).
//   - TestDocsMobileEasRunbookHoldsNoSecretShapedLiteral: the document holds
//     names and never a value, with a positive control so a mistyped pattern
//     cannot pass for a clean page.
//
// Two more tests hold the edges this runbook must not get wrong: the bundle
// identity tension it records rather than resolves (P3.1's config test and
// Check 89's guard want opposite things of mobile/app.json, and both are on the
// tree), and the directory's own index, which is where a reader finds it.
//
// Four tests hold what the page says about **Check 89** to the check. Three of
// them read what the page claims and refuse the shapes the check does not have:
// the guard's own stop messages are quoted, not "there is no build profile to
// read" and not a KeyError or a traceback
// (TestDocsMobileEasRunbookQuotesCheck89sGuardStops,
// TestDocsMobileEasRunbookDoesNotGiveTheGuardAKeyError), and the profile reads
// are step 1's hand-run `jq` with the owner's own stop on them, not the guard's
// (TestDocsMobileEasRunbookDoesNotGiveTheGuardTheProfileRead); the fourth says
// where those quotations come from and that the guard stops on the store
// identity too (TestDocsMobileEasRunbookPointsAtTheCheckItQuotes). The
// quotations are not held in this file: they are read out of the register's
// Check 89 section and compared with the page, so a stop reworded in
// docs/gcp-live-checklist.md fails the page here. (P4.2a's text lands on main
// with PR #580, and this branch merged main before it: where the register in the
// tree has no Check 89 section there is nothing to compare against, and that one
// test says so in a skip rather than passing quietly. The other three read only
// the page, and run here.)
//
// Three tests hold what the page says about the *repository* to the repository:
// TestDocsMobileEasRunbookNamesEveryProtectedEnvironment derives the protected
// environments -- and the job each one is declared in -- from the workflow files
// themselves, so the page's citations are by content and not by line number;
// TestDocsMobileEasRunbookPlacesExpoTokenInTheEnvironmentSecret holds §7's
// credential table, §9's environment settings and §10's workflow shape to one
// placement for the one secret CI may hold, because a repository secret and an
// environment secret are different fences; and
// TestDocsMobileEasRunbookSaysP4Dot3IsStagedNotRunning reads ci.yml, so the day
// the owner copies P4.3's job in, the page has to stop calling it staged.
//
// Every check above is mutation-proven -- the bug was planted, the run went
// red, the bug was removed. The mutations, in the order they were run, each
// applied to the whole page (a phrase that wraps in the file is the same phrase
// in the assertion, and a mutation that misses the wrapped copy is a mutation
// that proves nothing):
//
//	| Mutation | Went red |
//	| the plan task's name, and every "P4.4", replaced | TestDocsMobileEasRunbookNamesTheTaskAndTheCheck |
//	| every "Check 89" in the page replaced (every occurrence: the count is not written down here, because a count is one more thing to go stale) | TestDocsMobileEasRunbookNamesTheTaskAndTheCheck, TestDocsMobileEasRunbookDoesNotRestateCheck89 |
//	| the "Never paste a signing key" warning dropped | TestDocsMobileEasRunbookStatesEveryOwnerOnlyWarning |
//	| "credentials source" reworded | TestDocsMobileEasRunbookDoesNotRestateCheck89 |
//	| EXPO_TOKEN renamed | TestDocsMobileEasRunbookNamesEveryCredentialAndWhereItLives, TestDocsMobileEasRunbookHoldsNoSecretShapedLiteral |
//	| a PEM block planted in the page | TestDocsMobileEasRunbookHoldsNoSecretShapedLiteral |
//	| a value assigned to a credential's name planted | TestDocsMobileEasRunbookHoldsNoSecretShapedLiteral |
//	| docs.expo.dev replaced by a look-alike host | TestDocsMobileEasRunbookCitesOnlyTheAccountsOwnDocumentation |
//	| "This runbook does not restate Check 89." reworded | TestDocsMobileEasRunbookDoesNotRestateCheck89 |
//	| mobile-release renamed | TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape |
//	| app-config.test.js's name dropped from the tension | TestDocsMobileEasRunbookRecordsTheBundleIdentityTension |
//	| the README's row renamed | TestDocsMobileReadmeListsTheRunbook |
//	| a pull-request trigger planted as a permitted one | TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape |
//	| the long-hex shape dropped from the secret scan | TestMobileEasRunbookSecretScanFindsEveryShape |
//	| a 64-character lower-case hex secret planted in the page | TestDocsMobileEasRunbookHoldsNoSecretShapedLiteral |
//	| the precedent claim restored ("the signing workflow it does have takes its secrets through a protected environment") | TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape |
//	| the deployment-branch/trigger tie removed ("the policy must name `main` as well") | TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape |
//	| the keyless claim dropped from the release.yml sentence | TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape |
//	| "not staged and not created" softened to "it is planned" | TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape |
//	| "Exactly one protected environment exists in this repository today" broadened to a plural | TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape |
//	| `console-release` dropped from the environment table | TestDocsMobileEasRunbookNamesEveryProtectedEnvironment |
//	| the derived count sentence changed from "two" to "one" | TestDocsMobileEasRunbookNamesEveryProtectedEnvironment |
//	| the derived ordinal changed from "third" to "second" | TestDocsMobileEasRunbookNamesEveryProtectedEnvironment |
//	| a third workflow given `environment: mobile-release` | TestDocsMobileEasRunbookNamesEveryProtectedEnvironment |
//	| the environment reader changed from a YAML parse to a text scan | TestRepositoryEnvironmentsComeFromJobsNotComments |
//	| the shared-package reader made to return nothing | TestCISharedPackageJobsFindsAPlantedJob |
//	| "approved on 2026-10-18" dropped from the Phase 6 sentence | TestDocsMobileEasRunbookKeepsPhase6Approved |
//	| "proposed and NOT APPROVED" planted again | TestDocsMobileEasRunbookKeepsPhase6Approved |
//	| P4.3's job put back in the present tense | TestDocsMobileEasRunbookSaysP4Dot3IsStagedNotRunning |
//	| §7's table placed EXPO_TOKEN as the repository secret again | TestDocsMobileEasRunbookPlacesExpoTokenInTheEnvironmentSecret |
//	| §9's environment-secret wording dropped | TestDocsMobileEasRunbookPlacesExpoTokenInTheEnvironmentSecret |
//	| §10's environment-secret wording dropped | TestDocsMobileEasRunbookPlacesExpoTokenInTheEnvironmentSecret |
//	| §9's website-live row named the wrong job (`deploy`) | TestDocsMobileEasRunbookNamesEveryProtectedEnvironment |
//	| §9's website-live row dropped the `environment:` content citation | TestDocsMobileEasRunbookNamesEveryProtectedEnvironment |
//	| a line number ("(line 70)") put back into §2's citation | TestDocsMobileEasRunbookNamesEveryProtectedEnvironment |
//	| a probe workflow declaring website-live in another job (tmp-probe.yml, removed after) | TestDocsMobileEasRunbookNamesEveryProtectedEnvironment |
//	| Check 89's store-identity stop reworded in the page (a paraphrase, not the guard's words), with the register's Check 89 in the tree | TestDocsMobileEasRunbookQuotesCheck89sGuardStops |
//	| Check 89's mobile/eas.json stop in the page reduced to "there is no build profile to read", with the register's Check 89 in the tree | TestDocsMobileEasRunbookQuotesCheck89sGuardStops |
//	| that same stop reworded in the *register* instead (main's Check 89 appended to docs/gcp-live-checklist.md for the run, then removed) | TestDocsMobileEasRunbookQuotesCheck89sGuardStops |
//	| ciSharedPackageJobs' parse error swallowed again (`return nil, nil`) | TestCISharedPackageJobsFindsAPlantedJob |
//	| the guard-stop marker moved to a phrase the register does not print (no stop found in its Check 89) | TestDocsMobileEasRunbookQuotesCheck89sGuardStops |
//	| the file and section the quotation comes from dropped from Tension 1 | TestDocsMobileEasRunbookPointsAtTheCheckItQuotes |
//	| the store identity dropped from §3's list of the guard's stops | TestDocsMobileEasRunbookPointsAtTheCheckItQuotes |
//	| "KeyError" planted in the store-identity paragraph | TestDocsMobileEasRunbookDoesNotGiveTheGuardAKeyError |
//	| "traceback" planted in the store-identity paragraph | TestDocsMobileEasRunbookDoesNotGiveTheGuardAKeyError |
//	| `d["ios"]["bundleIdentifier"]` planted again | TestDocsMobileEasRunbookDoesNotGiveTheGuardAKeyError |
//	| §8's "the first place Check 89's guard turns a wrong value into a stop" restored | TestDocsMobileEasRunbookDoesNotGiveTheGuardTheProfileRead |
//	| §8's register quotation dropped | TestDocsMobileEasRunbookDoesNotGiveTheGuardTheProfileRead |
//	| §8's "the guard does not read this value" dropped | TestDocsMobileEasRunbookDoesNotGiveTheGuardTheProfileRead |

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const mobileEasRunbook = "docs/mobile/eas-signing-runbook.md"

// runbook reads the page with its whitespace normalised to single spaces, so a
// sentence that wraps in the file still matches -- the convention the
// neighbouring docs tests use (docs_project_wide_git_test.go). The two tests
// that scan the page's *bytes* for a URL or a credential-shaped literal read it
// raw, because normalising is exactly what a scanner must not do to a value.
func runbook(t *testing.T) string {
	t.Helper()
	return strings.Join(strings.Fields(readDoc(t, mobileEasRunbook)), " ")
}

// TestDocsMobileEasRunbookNamesTheTaskAndTheCheck asserts the runbook is the
// one Task P4.4 asks for and that it hands the running of the check to the
// register rather than paraphrasing it: Check 89's text is P4.2a's (PR #580),
// and a runbook that restated the steps would be a second copy to drift.
func TestDocsMobileEasRunbookNamesTheTaskAndTheCheck(t *testing.T) {
	doc := runbook(t)
	for _, want := range []string{
		"Task P4.4",
		"EAS project, the signing keys and the store entries",
		"implementation-plan.md",
		"Check 89",
		"../gcp-live-checklist.md",
		"P4.2a",
		"#580",
		"NOT RUN",
		// The constraints and decisions the task is built from.
		"Global Constraint 9",
		"D111",
		"D120",
		"D125",
		"C2",
		"C6",
		"C8",
		// The register's own names for the things this runbook configures.
		"mobile/app.json",
		"mobile/eas.json",
		"distribution: store",
		"submit.internal.android.track",
		"eas whoami",
		"eas project:info",
		// The app is versioned from the tag the check builds (C6).
		"git describe --tags --exact-match",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not name %q", mobileEasRunbook, want)
		}
	}
}

// TestDocsMobileEasRunbookStatesEveryOwnerOnlyWarning asserts each owner-only
// warning is present. The list is the brief's own ("no agent performs any step,
// no signing key or store credential ever enters an agent's environment"), and
// each line is one an agent reading this page could otherwise read as an
// invitation: to run a build, to hold a token, to paste a value, or to write
// the workflow the build would use.
func TestDocsMobileEasRunbookStatesEveryOwnerOnlyWarning(t *testing.T) {
	doc := runbook(t)
	for _, want := range []struct{ what, line string }{
		{"that no agent performs a step", "No agent performs any step of this runbook"},
		{"that no agent builds or submits", "No agent runs `eas build` or `eas submit`"},
		{"that no agent touches a key", "no agent touches a signing key or a store credential"},
		{"that no credential enters an agent's environment", "no signing key or store credential ever enters an agent's environment"},
		{"that it is the owner's terminal", "at your own terminal"},
		{"that it has never been run", "Nothing here has been run."},
		{"that it is instructions rather than a record", "This page is the instructions, not a record."},
		{"never to paste a key", "Never paste a signing key"},
		{"that a name is not a value", "the names only, never a value"},
		{"that a credential is not in this repository", "never in this repository"},
		{"that a store credential is not in CI", "never in CI"},
		{"what a store credential in CI would mean", "A store credential in CI is the finding Check 89's step 5 exists to produce."},
		{"that no agent copies a workflow", "No agent copies a workflow file into"},
	} {
		if !strings.Contains(doc, want.line) {
			t.Errorf("%s does not say (%s) %q", mobileEasRunbook, want.what, want.line)
		}
	}
}

// TestDocsMobileEasRunbookNamesEveryCredentialAndWhereItLives asserts the
// runbook's credential table is complete: each thing the path needs is named,
// and each is placed. A runbook that named a credential without saying where it
// lives is one an owner completes by putting it somewhere -- which is how a key
// reaches CI.
func TestDocsMobileEasRunbookNamesEveryCredentialAndWhereItLives(t *testing.T) {
	doc := runbook(t)
	for _, want := range []string{
		// The two answers EAS offers, and the difference between them.
		"EAS-managed",
		"owner-held",
		// iOS.
		"distribution certificate",
		"provisioning profile",
		"App Store Connect API key",
		// Android: the upload key is the owner's; the app signing key is Google's.
		"upload key",
		"Play app signing",
		// The file names Check 89's step 5 looks for.
		"google-play-service-account.json",
		"serviceAccountKeyPath",
		"eas credentials",
		// The one name CI may hold, and the names it may not.
		"EXPO_TOKEN",
		"ASC_API_KEY_ID",
		"PLAY_SERVICE_ACCOUNT_JSON",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not name %q", mobileEasRunbook, want)
		}
	}
}

// TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape
// asserts the repository-side half is described: a protected environment whose
// only required reviewer is the owner, the trigger the build would run on,
// least privilege, pinned actions -- and that the runbook writes no workflow
// file, only the shape a later staged file under docs/pending-workflows/ would
// have (Global Constraint 8). It also pins the precedent claim to the tree as
// the tree is now: the repository has two protected environments --
// website-promote.yml's `website-live` and the console's `console-release`,
// both installed and both declared on a job -- release.yml signs keyless with
// no environment, and a v*-only deployment-branch policy cannot be reached by a
// dispatch from main. The environment set itself is *derived* from the workflow
// files by TestDocsMobileEasRunbookNamesEveryProtectedEnvironment; this test
// refuses the stale sentences an earlier revision of the page carried.
func TestDocsMobileEasRunbookNamesTheProtectedEnvironmentAndTheTriggerShape(t *testing.T) {
	doc := runbook(t)
	for _, want := range []string{
		"mobile-release",
		"required reviewer",
		"environment:",
		"workflow_dispatch",
		"pull_request_target",
		"workflow_run",
		"full 40-character commit SHA",
		"permissions:",
		"contents: read",
		"docs/pending-workflows/",
		"describes a workflow and writes none",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not name %q", mobileEasRunbook, want)
		}
	}
	// The trigger shape is a stop, not a menu: the runbook must say which
	// events are refused rather than leaving the reader to infer it.
	for _, banned := range []string{"`push` of any branch", "`on: pull_request`"} {
		if strings.Contains(doc, banned) {
			t.Errorf("%s says %q: the build must not run on a pull request", mobileEasRunbook, banned)
		}
	}
	// The precedent claim, read off the tree rather than written from memory.
	// An earlier revision of this page said exactly one protected environment
	// existed, that `release.yml` was the repository's signing workflow reading
	// its secrets through one, and that the console's `console-release.yml` was
	// "not staged and not created" -- all three false once the console's
	// signing pipeline landed. The environment set is now *derived* from the
	// workflow files by TestDocsMobileEasRunbookNamesEveryProtectedEnvironment,
	// so this test only has to refuse the stale sentences.
	for _, banned := range []string{
		"the signing workflow it does have takes its secrets through a protected environment",
		"the same mechanism the other two owner-only paths",
		"Exactly one protected environment exists in this repository today",
		"is not staged and not created",
	} {
		if strings.Contains(doc, banned) {
			t.Errorf("%s says %q: the precedent has to be the tree's, and the environment names are derived by TestDocsMobileEasRunbookNamesEveryProtectedEnvironment", mobileEasRunbook, banned)
		}
	}
	// §9's deployment-branch policy and §10's trigger are one decision: an
	// environment restricted to tags is unreachable from a dispatch on main.
	if !strings.Contains(doc, "the policy must name `main` as well") {
		t.Errorf("%s does not tie the deployment-branch policy to the trigger: a `v*`-only environment cannot be reached by a workflow dispatched from main", mobileEasRunbook)
	}
}

// workflowEnvironmentDirs are the directories a protected environment can be
// declared in: the workflows GitHub runs, and docs/pending-workflows/, where a
// staged file is the real file's own twin (the console's two keep theirs after
// the copy-in). Both are read, so a staged copy cannot hide an environment.
var workflowEnvironmentDirs = []string{".github/workflows", "docs/pending-workflows"}

// workflowLineCitationRE finds a citation of a file by line number -- "(line
// 70)", "(lines 60-75)", "line 203 of console-release.yml" -- the shape §2, §9
// and §15 used to carry for the two environment declarations. The page cites
// those files by content instead, so a line number reappearing is the
// regression, whatever file it points at: no parse here can hold a page to a
// line, and a line moves the moment anything above it grows.
var workflowLineCitationRE = regexp.MustCompile(`\(lines? \d+[^)]*\)|lines? \d+ of`)

// workflowEnvironment is one declaration of a protected environment: the file
// that declares it and the **job** that carries the `environment:` key. The job
// is the half a citation by content needs -- the page names the file *and* the
// job, never a line number, so the citation cannot rot when a file above it
// grows.
type workflowEnvironment struct {
	File string
	Job  string
}

// String renders a declaration the way the page cites it, for error messages.
func (w workflowEnvironment) String() string { return w.Job + " in " + filepath.Base(w.File) }

// environmentsInDirs reads every `*.yml` under dirs and returns, per
// environment name, the declarations whose **jobs** carry `environment: name`
// -- or the long form `environment: {name: ...}`. It reads the parsed YAML, not
// the text, so a mention inside a comment -- and both workflows here document
// their environments in their headers -- is not an environment. The parse is
// what makes the assertion below evidence about the repository rather than a
// second copy of a sentence.
func environmentsInDirs(t *testing.T, dirs []string) map[string][]workflowEnvironment {
	t.Helper()
	found := map[string][]workflowEnvironment{}
	files := 0
	for _, dir := range dirs {
		base := dir
		if !filepath.IsAbs(dir) {
			base = filepath.Join("..", "..", dir)
		}
		entries, err := os.ReadDir(base)
		if err != nil {
			t.Fatalf("%s cannot be read: %v -- this test reads the workflow files, not a comment about them", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
				continue
			}
			rel := filepath.Join(dir, entry.Name())
			text, err := os.ReadFile(filepath.Join(base, entry.Name()))
			if err != nil {
				t.Fatalf("%s cannot be read: %v", rel, err)
			}
			files++
			var wf struct {
				Jobs map[string]struct {
					Environment any `yaml:"environment"`
				} `yaml:"jobs"`
			}
			if err := yaml.Unmarshal([]byte(text), &wf); err != nil {
				t.Fatalf("%s is not valid YAML: %v", rel, err)
			}
			for jobName, job := range wf.Jobs {
				var name string
				switch v := job.Environment.(type) {
				case string:
					name = v
				case map[string]any:
					name, _ = v["name"].(string)
				}
				if name != "" {
					found[name] = append(found[name], workflowEnvironment{File: rel, Job: jobName})
				}
			}
		}
	}
	if files == 0 {
		t.Fatalf("no workflow file was read under %v: a green run would prove nothing", dirs)
	}
	return found
}

func repositoryEnvironments(t *testing.T) map[string][]workflowEnvironment {
	t.Helper()
	return environmentsInDirs(t, workflowEnvironmentDirs)
}

// tableRow returns the line of doc that begins with prefix, or "" if there is
// none. §9's environment table is one row per environment, so the row's own
// text is the unit a claim about an environment is held to.
func tableRow(doc, prefix string) string {
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

// blockAt returns the markdown block that begins with prefix -- that line and
// the lines that continue it, since prose here wraps -- with runs of whitespace
// collapsed to single spaces, so a phrase that wraps in the file still matches.
// The block ends at a blank line, at the next list item, at a table row or at a
// heading.
func blockAt(doc, prefix string) string {
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		block := []string{line}
		for _, next := range lines[i+1:] {
			if trim := strings.TrimSpace(next); trim == "" ||
				strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "|") ||
				strings.HasPrefix(trim, "#") {
				break
			}
			block = append(block, next)
		}
		return strings.Join(strings.Fields(strings.Join(block, "\n")), " ")
	}
	return ""
}

// protectedEnvironmentSentence is the page's own sentence about how many
// protected environments this repository has, written from the count the files
// give. The page must carry this exact sentence, so an environment added or
// removed later breaks the test until the page is corrected -- which is the
// whole point of deriving it rather than asserting a number.
func protectedEnvironmentSentence(n int) string {
	if n == 1 {
		return "**There is one protected environment in this repository today:**"
	}
	return fmt.Sprintf("**There are %s protected environments in this repository today:**", numberWord(n))
}

var numberWords = []string{"no", "one", "two", "three", "four", "five", "six", "seven", "eight"}

func numberWord(n int) string {
	if n >= 0 && n < len(numberWords) {
		return numberWords[n]
	}
	return strconv.Itoa(n)
}

// ordinalWord is the word the page uses for the environment it proposes: the
// one after the environments that exist.
var ordinalWords = []string{"", "first", "second", "third", "fourth", "fifth", "sixth", "seventh", "eighth"}

func ordinalWord(n int) string {
	if n >= 0 && n < len(ordinalWords) {
		return ordinalWords[n]
	}
	return strconv.Itoa(n) + "th"
}

// TestDocsMobileEasRunbookNamesEveryProtectedEnvironment derives the protected
// environments from the repository's own workflow files and requires the
// runbook to name each one, to state the count the files give, and to say which
// environment it is *adding* -- the one after them. The claim cannot rot: a
// workflow added with an `environment:` fails this test until the page names it,
// and so does the deletion of one.
func TestDocsMobileEasRunbookNamesEveryProtectedEnvironment(t *testing.T) {
	doc := runbook(t)
	// The table's rows are read raw: the normalised text above is one line, and a
	// claim about "the row that names this environment" needs the rows.
	raw := readDoc(t, mobileEasRunbook)
	envs := repositoryEnvironments(t)
	if len(envs) == 0 {
		t.Fatal("no workflow on this tree declares an environment: the runbook's precedent claim is about these files, so this test has nothing to hold it to")
	}
	names := make([]string, 0, len(envs))
	for name := range envs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		// The name has to appear as the row this page's table gives it, and the
		// declaration has to be cited **in that row by content** -- the file, the
		// `environment:` key and its value, and the job that carries it: naming
		// the string anywhere would be satisfied by a sentence that mentions it
		// in passing, which is how the earlier revision's claim stayed wrong
		// while looking checked, and a line number would be satisfied by nothing
		// at all, since no parse can hold a file to a line.
		row := tableRow(raw, fmt.Sprintf("| **`%s`** |", name))
		if row == "" {
			t.Errorf("%s has no %q row in §9's table: every environment this repository declares gets one", mobileEasRunbook, name)
			continue
		}
		citation := "`environment: " + name + "`"
		if !strings.Contains(row, citation) {
			t.Errorf("%s's %q row does not cite %s, the declaration itself: the page cites the workflow files by content, not by line number", mobileEasRunbook, name, citation)
		}
		for _, decl := range envs[name] {
			if !strings.Contains(row, filepath.Base(decl.File)) {
				t.Errorf("%s's %q row does not name %s, the file that declares it: the page's precedent is about these workflows", mobileEasRunbook, name, filepath.Base(decl.File))
			}
			if !strings.Contains(row, "`"+decl.Job+"` job") {
				t.Errorf("%s's %q row does not name the job that declares it, %q in %s: the citation is by content -- the environment key and the job -- so it cannot rot when the file grows above them", mobileEasRunbook, name, decl.Job, filepath.Base(decl.File))
			}
		}
	}
	// And no citation by line number anywhere on the page: a line number is the
	// one form of citation no parse here can check, and §2 claims the precedent
	// was read off the files.
	if m := workflowLineCitationRE.FindString(doc); m != "" {
		t.Errorf("%s cites a file as %q: the workflow citations are by content (the `environment:` key and the job that declares it), which is what this test can hold them to", mobileEasRunbook, m)
	}
	if want := protectedEnvironmentSentence(len(envs)); !strings.Contains(doc, want) {
		t.Errorf("%s does not say %q (%d environments declared by %v): the count is derived from the files, not written twice",
			mobileEasRunbook, want, len(envs), names)
	}
	// The environment this runbook proposes is the one after them all -- and it
	// must not exist yet: §10 says no mobile workflow is written, so a
	// `mobile-release` on the tree would mean this page is describing something
	// already built.
	if decls, exists := envs["mobile-release"]; exists {
		t.Errorf("mobile-release is declared by %v: the mobile build workflow §10 describes as not written does exist, and this page has to be rewritten as a description of it", decls)
	}
	want := fmt.Sprintf("this repository's %s protected environment", ordinalWord(len(envs)+1))
	if !strings.Contains(doc, want) {
		t.Errorf("%s does not call mobile-release %q: the page has to say which environment it is adding, and that is derived from how many exist", mobileEasRunbook, want)
	}
	// What each of them gates, so naming them is not decoration.
	for _, want := range []string{"website-promote.yml", "console-release.yml", "release.yml"} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not name %s: the precedent is a statement about this repository's workflows, and each one it rests on has to be named", mobileEasRunbook, want)
		}
	}
	if !strings.Contains(doc, "keyless") {
		t.Errorf("%s does not say release.yml signs keyless: it is the workflow an owner would otherwise assume reads a signing secret through an environment, and it does not", mobileEasRunbook)
	}
}

// check89Stops returns every stop message in text: the sentence after a
// "Check 89 stopped: " marker, read to the closing quote of the `echo "…"` or
// `sys.exit("…")` that carries it. The marker is Check 89's own -- the guard
// prints its reason as `Check 89 stopped: …` and the section's prose says
// "Check 89 stopped" of nothing else -- and no message contains a double quote,
// so the marker and the closing quote delimit the sentence exactly. The same
// function reads the register and the page, which is what lets the two be
// compared byte for byte: nothing here decides what a stop *says*.
func check89Stops(text string) []string {
	const marker = "Check 89 stopped: "
	var stops []string
	for rest := text; ; {
		i := strings.Index(rest, marker)
		if i < 0 {
			return stops
		}
		rest = rest[i:]
		if j := strings.Index(rest, `"`); j >= 0 {
			stops = append(stops, strings.TrimSpace(rest[:j]))
		}
		rest = rest[len(marker):]
	}
}

// check89RegisterStops reads Check 89's own stop messages out of
// docs/gcp-live-checklist.md -- the register the runbook quotes and defers to.
//
// The pin used to hold the two sentences as literals in this file, copied from
// main by hand, because P4.2a's text (PR #580) is not in this branch's tree.
// A literal cannot notice the register changing its mind, so the sentences are
// read from the register itself now: a stop reworded there fails the page here.
//
// This tree's register does not carry Check 89 -- the branch merged main before
// #580 landed -- and there is no register here to compare against, so the test
// skips with that reason rather than passing quietly. It says so in its output:
// a skip is visible, which is the difference between this and swallowing the
// file. The moment the branch carries the section, the same test compares.
func check89RegisterStops(t *testing.T) []string {
	t.Helper()
	const (
		section = "## Check 89"
		path    = "docs/gcp-live-checklist.md"
	)
	doc := readDoc(t, path)
	start := strings.Index(doc, section)
	if start < 0 {
		t.Skipf("%s has no %q section in this tree: P4.2a's text lands with PR #580, which main carries and this branch merged before, so there is no register here to hold the page's quotations to -- this test compares them as soon as the section is in the tree", path, section)
	}
	rest := doc[start+len(section):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	// The guard's file tests and its python line are the stops a reader meets
	// before any step runs; a section with fewer than the two the page quotes is
	// the section moved, not a check that stops less often.
	stops := check89Stops(rest)
	if len(stops) < 2 {
		t.Fatalf("%s's %q section yields %d stop messages, want at least the guard's file tests and its python line: the section or the marker has moved, and this test now reads a different part of the register", path, section, len(stops))
	}
	return stops
}

// firstCheck89Stop is the register's own sentence among stops that names marker
// -- how the page's quotations are identified without this file repeating any of
// them. It fails loudly when the register has no such stop: the section has
// moved, and a test that went on to compare nothing would report a green page.
func firstCheck89Stop(t *testing.T, stops []string, marker string) string {
	t.Helper()
	for _, stop := range stops {
		if strings.Contains(stop, marker) {
			return stop
		}
	}
	t.Fatalf("the register's Check 89 section has no stop message naming %q: the guard's stops are what this page quotes, and one of the two has moved", marker)
	return ""
}

// TestDocsMobileEasRunbookQuotesCheck89sGuardStops: the page describes what the
// guard does when the store identity is absent, and it has to describe the stop
// the guard really makes. The guard reads `ios.bundleIdentifier` and
// `android.package` with `.get()` and tests `if not bid or not pkg`, so their
// absence is the stop it was written for -- printed on stderr from `sys.exit`
// and acted on by `|| exit 1`, which the block's own comment says is there
// precisely so the reason is not printed and read past.
//
// Both sentences are the register's, read from it here: the page may quote the
// register, and it may not quote something the register does not say.
func TestDocsMobileEasRunbookQuotesCheck89sGuardStops(t *testing.T) {
	register := check89RegisterStops(t)
	doc := runbook(t)
	// Every "Check 89 stopped: …" the page writes is a sentence of the
	// register's. A quotation the register does not carry is a paraphrase with
	// quotation marks around it, which is the shape this test exists to refuse.
	quoted := check89Stops(doc)
	if len(quoted) == 0 {
		t.Errorf("%s quotes none of Check 89's stop messages: the page describes the stops, and the reader has to be able to find the sentence the check actually prints", mobileEasRunbook)
	}
	for _, q := range quoted {
		if !slices.Contains(register, q) {
			t.Errorf("%s quotes %q, which is not a stop message in the register's Check 89 section: a quotation the register does not carry is this page's own words", mobileEasRunbook, q)
		}
	}
	// And the two stops the page's Tension 1 turns on are among the register's
	// own sentences, found in the register by what they are about rather than
	// repeated here: the store identity the guard reads out of app.json, and the
	// mobile/eas.json file P4.4 adds.
	for _, marker := range []string{"android.package", "mobile/eas.json"} {
		want := firstCheck89Stop(t, register, marker)
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not quote the register's own stop %q: the page describes the stop as the check makes it, in the check's own words", mobileEasRunbook, want)
		}
	}
}

// TestDocsMobileEasRunbookPointsAtTheCheckItQuotes: the page names the file and
// the section its verbatim quotations come from, so an owner can go and read the
// stop for themselves -- a quotation nobody can check is a paraphrase with
// quotation marks -- and §3's account of the guard block names the store identity
// among the stops, because the guard's file tests are not the whole of it.
//
// This test reads only the page, so unlike the comparison above it needs no
// register in the tree: it runs here, today, and it is where a page that dropped
// its citation or its store-identity stop is caught.
func TestDocsMobileEasRunbookPointsAtTheCheckItQuotes(t *testing.T) {
	raw := readDoc(t, mobileEasRunbook)
	block := sectionText(raw, "**Tension 1 —", "**Tension 2 —")
	if block == "" {
		t.Fatal("§14's Tension 1 was renamed: this test reads the text between \"**Tension 1 —\" and \"**Tension 2 —\", and it has to be repointed at it")
	}
	for _, want := range []string{"docs/gcp-live-checklist.md", `"Check 89"`} {
		if !strings.Contains(block, want) {
			t.Errorf("§14's Tension 1 does not name %s, the file and the section its verbatim quotation comes from", want)
		}
	}
	guardList := blockAt(raw, "**Check 89 owns this step")
	if guardList == "" {
		t.Fatal("§3's account of the guard block was renamed: this test reads the bullet beginning \"**Check 89 owns this step\", and it has to be repointed at it")
	}
	for _, want := range []string{"store identity", "`android.package`"} {
		if !strings.Contains(guardList, want) {
			t.Errorf("§3's account of the guard block does not name %q: the guard stops on the store identity too, and a reader who took the file tests for the whole of it would meet that stop as a surprise", want)
		}
	}
}

// TestDocsMobileEasRunbookDoesNotGiveTheGuardAKeyError: the page said the
// guard's python line "reads `d[\"ios\"][\"bundleIdentifier\"]`" and that the
// check "dies with a traceback rather than one of its own stop messages". Both
// are wrong about the same line: it indexes with `.get()` and stops with its own
// message. A page that promised a traceback would send an owner looking for a
// bug in P4.2a's guard where there is a stop, and the stop's message is the
// thing to record.
func TestDocsMobileEasRunbookDoesNotGiveTheGuardAKeyError(t *testing.T) {
	doc := runbook(t)
	for _, banned := range []string{
		"KeyError",
		"traceback",
		`d["ios"]["bundleIdentifier"]`,
		`d["android"]["package"]`,
		"dies with a traceback",
	} {
		if strings.Contains(doc, banned) {
			t.Errorf("%s says %q: Check 89's guard reads the two identity fields with .get() and stops with its own message, so there is no crash to describe", mobileEasRunbook, banned)
		}
	}
}

// TestDocsMobileEasRunbookDoesNotGiveTheGuardTheProfileRead: §8 said the
// `distribution: store` check was "the first place Check 89's guard turns a
// wrong value into a stop". The guard reads no value out of `mobile/eas.json` at
// all -- it tests that the file exists -- and the register says why the stop is
// the owner's: "`jq` is a read, so the guard cannot stop on a value only the
// run's owner can judge". The read is step 1's, run by hand, and the stop is
// taken there ("edit it and re-run").
func TestDocsMobileEasRunbookDoesNotGiveTheGuardTheProfileRead(t *testing.T) {
	raw := readDoc(t, mobileEasRunbook)
	section := sectionText(raw, "## 8. ", "## 9. ")
	if section == "" {
		t.Fatal("§8 was renamed: this test reads the section between \"## 8. \" and \"## 9. \", and it has to be repointed at it")
	}
	for _, banned := range []string{
		"the first place Check 89's guard turns a wrong value into a stop",
		"Check 89's guard turns a wrong value into a stop",
	} {
		if strings.Contains(raw, banned) {
			t.Errorf("%s says %q: no line of the guard opens mobile/eas.json, so a wrong profile value is step 1's hand-run read and the owner's own stop, not the guard's", mobileEasRunbook, banned)
		}
	}
	// §8 has to say it in the register's words, so the reader meets the rule and
	// not this page's summary of it.
	for _, want := range []string{
		"`jq` is a read, so the guard cannot stop on a value only the run's owner can judge",
		"guard does not read this value",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("§8 does not say %q: the profile reads are step 1's and the stop on them is the owner's, and §8 is where a reader deciding what to write in eas.json meets that", want)
		}
	}
}

// sectionText returns the page's text from the line beginning with from up to
// the line beginning with to, with its whitespace collapsed -- the unit a claim
// about a whole section is held to.
func sectionText(doc, from, to string) string {
	start := strings.Index(doc, from)
	if start < 0 {
		return ""
	}
	rest := doc[start:]
	if end := strings.Index(rest, to); end >= 0 {
		rest = rest[:end]
	}
	return strings.Join(strings.Fields(rest), " ")
}

// TestDocsMobileEasRunbookPlacesExpoTokenInTheEnvironmentSecret holds the three
// places the page places `EXPO_TOKEN` to one answer. §7's credential table, §9's
// environment settings and §10's workflow shape each describe where it may live,
// and they are different fences: a **repository** secret is readable by every
// workflow in the repository, whatever job it runs in, while an **environment**
// secret is readable only by a job that names `environment: mobile-release`, and
// only after its required reviewer has approved. A page whose table said
// "repository secret" while its settings said "the only secret that belongs
// here" would be describing the weaker fence as the stronger one, which is the
// arrangement §7's own "a key in CI is a key any later workflow change can read"
// exists to refuse.
func TestDocsMobileEasRunbookPlacesExpoTokenInTheEnvironmentSecret(t *testing.T) {
	raw := readDoc(t, mobileEasRunbook)
	// §7 and §10 state the placement in a table row (one line each); §9 states
	// it in a bullet that wraps, so its whole block is the unit.
	places := []struct {
		section string
		text    string
	}{
		{"§7's credential table", tableRow(raw, "| **Expo access token**")},
		{"§9's environment settings", blockAt(raw, "- **Environment secrets:**")},
		{"§10's workflow shape", tableRow(raw, "| **Secrets** |")},
	}
	for _, p := range places {
		if p.text == "" {
			t.Fatalf("%s has no line this test can read for %s: the section it is about was renamed, and this check has to be repointed at it", mobileEasRunbook, p.section)
		}
		for _, want := range []string{"environment secret", "never a repository secret"} {
			if !strings.Contains(p.text, want) {
				t.Errorf("%s does not say %q: §7, §9 and §10 have to place EXPO_TOKEN the same way -- the environment secret on mobile-release, reached only through the job's environment:, and never a repository secret", p.section, want)
			}
		}
	}
	// The stale placement, refused on §7's own row: this is the sentence the
	// page used to carry, and it is the one a reader would act on.
	for _, banned := range []string{"stored as the repository secret", "repository secret **`EXPO_TOKEN`**"} {
		if strings.Contains(places[0].text, banned) {
			t.Errorf("%s says %q: EXPO_TOKEN is the mobile-release environment secret and not a repository secret", mobileEasRunbook, banned)
		}
	}
	// And the environment itself, named where the token's fence is: §7's row and
	// §10's shape both have to say which environment, since "an environment
	// secret" without a name is a setting the owner cannot make.
	for _, i := range []int{0, 2} {
		if !strings.Contains(places[i].text, "`mobile-release`") {
			t.Errorf("%s does not name `mobile-release`, the environment EXPO_TOKEN's fence is on", places[i].section)
		}
	}
}

// TestRepositoryEnvironmentsComeFromJobsNotComments pins the reader above
// against temp files, so its evidence is its own parse rather than today's
// repository: a workflow that only *mentions* an environment in its header (as
// both of this repository's do) declares none, the long form counts, and a job
// with no `environment:` adds nothing.
func TestRepositoryEnvironmentsComeFromJobsNotComments(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("documented.yml", `# This job reads its secrets from environment: commented-out
name: documented
on:
  workflow_dispatch:
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: 'echo "# environment: also-only-a-comment"'
`)
	write("declaring.yml", `name: declaring
on:
  workflow_dispatch:
jobs:
  sign:
    environment: alpha
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
  deploy:
    environment:
      name: beta
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
  plain:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`)
	found := environmentsInDirs(t, []string{dir})
	want := map[string]workflowEnvironment{
		"alpha": {File: filepath.Join(dir, "declaring.yml"), Job: "sign"},
		"beta":  {File: filepath.Join(dir, "declaring.yml"), Job: "deploy"},
	}
	if len(found) != len(want) {
		t.Fatalf("the reader found %v, want %v: a comment is not an environment, and neither is a job without one", found, want)
	}
	for name, decl := range want {
		got := found[name]
		if len(got) != 1 || filepath.Base(got[0].File) != filepath.Base(decl.File) || got[0].Job != decl.Job {
			t.Errorf("the reader found %v for %q, want %v: the job is half of the citation the page makes", got, name, decl)
		}
	}
	for _, name := range []string{"commented-out", "also-only-a-comment"} {
		if _, exists := found[name]; exists {
			t.Errorf("the reader counted %q, which is only ever mentioned in a comment: a claim about this repository built that way would be a claim about its prose", name)
		}
	}
}

// ciSharedPackageJobs reports the jobs in a workflow that install or test the
// shared package: a step whose `run` names `shared/`, or whose
// `working-directory` is under it. P4.3 is the owner's task that adds one to
// `.github/workflows/ci.yml`, and the plan's open item 4 says what its absence
// means -- *"Until it exists, the shared package's tests run only where someone
// runs them"*.
//
// It returns the parse error rather than folding it into "no such job": a
// workflow this reader cannot read is not a workflow that runs no shared-package
// tests, and a caller that asserted the second from the first would pass on any
// file at all. environmentsInDirs, above, fails loudly on the same input for the
// same reason.
func ciSharedPackageJobs(text string) ([]string, error) {
	var wf struct {
		Jobs map[string]struct {
			Defaults struct {
				Run struct {
					WorkingDirectory string `yaml:"working-directory"`
				} `yaml:"run"`
			} `yaml:"defaults"`
			Steps []struct {
				Run              string `yaml:"run"`
				WorkingDirectory string `yaml:"working-directory"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(text), &wf); err != nil {
		return nil, fmt.Errorf("the workflow is not valid YAML: %w", err)
	}
	var jobs []string
	namesSharedPackage := func(s string) bool {
		trimmed := strings.TrimSpace(s)
		return strings.Contains(trimmed, "shared/") || trimmed == "shared"
	}
	for name, job := range wf.Jobs {
		if namesSharedPackage(job.Defaults.Run.WorkingDirectory) {
			jobs = append(jobs, name)
			continue
		}
		for _, step := range job.Steps {
			if namesSharedPackage(step.Run) || namesSharedPackage(step.WorkingDirectory) {
				jobs = append(jobs, name)
				break
			}
		}
	}
	sort.Strings(jobs)
	return jobs, nil
}

// TestDocsMobileEasRunbookSaysP4Dot3IsStagedNotRunning holds the page's claim
// about P4.3 to the file P4.3 changes: the shared package's CI job is staged
// for the owner to copy in (PR #582) and is not running, and if
// `.github/workflows/ci.yml` ever gains such a job this test says so rather than
// leaving the page to say "runs only where someone runs them" about a job that
// exists.
func TestDocsMobileEasRunbookSaysP4Dot3IsStagedNotRunning(t *testing.T) {
	doc := runbook(t)
	for _, want := range []string{
		"staged for the owner to copy in",
		"not yet running",
		"P4.3",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not say %q about P4.3's CI job", mobileEasRunbook, want)
		}
	}
	for _, banned := range []string{"P4.3's job (owner-made) is the shared package's tests"} {
		if strings.Contains(doc, banned) {
			t.Errorf("%s says %q in the present tense: that job does not exist yet", mobileEasRunbook, banned)
		}
	}
	jobs, err := ciSharedPackageJobs(readDoc(t, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatalf(".github/workflows/ci.yml cannot be read: %v -- this test reads the file P4.3 changes, and a file it cannot read is not one that runs no shared-package tests", err)
	}
	if len(jobs) != 0 {
		t.Errorf(".github/workflows/ci.yml now runs the shared package's tests in job(s) %v: P4.3 has landed, and this page's 'staged, not yet running' sentence is stale", jobs)
	}
}

// TestDocsMobileEasRunbookKeepsPhase6Approved holds the page's status for the
// block check 102 lives in to the plan's own: the owner approved all of Phase 6
// on 2026-10-18 and checks 100-109 with it, so a page that still calls them
// "proposed and NOT APPROVED" is telling the owner a task is barred that is not.
// The plan is read here rather than trusted, so the two cannot drift apart in
// either direction.
func TestDocsMobileEasRunbookKeepsPhase6Approved(t *testing.T) {
	doc := runbook(t)
	plan := readDoc(t, "docs/mobile/implementation-plan.md")
	if !strings.Contains(plan, "APPROVED by the owner on 2026-10-18") {
		t.Fatalf("docs/mobile/implementation-plan.md no longer says Phase 6 was approved by the owner on 2026-10-18: this page follows the plan's status, and one of the two has moved")
	}
	if !strings.Contains(doc, "approved on 2026-10-18") {
		t.Errorf("%s does not say Phase 6 was approved on 2026-10-18: check 102's block is approved (the plan's Phase 6 heading and its checks 100-109 note)", mobileEasRunbook)
	}
	for _, banned := range []string{"proposed and NOT APPROVED", "NOT APPROVED"} {
		if strings.Contains(doc, banned) {
			t.Errorf("%s says %q: the plan approved Phase 6 and checks 100-109 on 2026-10-18, and TestDocsMobilePlanPhase6IsApproved pins that", mobileEasRunbook, banned)
		}
	}
}

// TestCISharedPackageJobsFindsAPlantedJob is the control for the reader above:// the repository's ci.yml has no such job today, and a reader that never finds
// one would report that whatever a later file said.
func TestCISharedPackageJobsFindsAPlantedJob(t *testing.T) {
	planted := `name: ci
on:
  push:
jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - run: go test ./...
  shared:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: shared
    steps:
      - run: npm test
`
	got, err := ciSharedPackageJobs(planted)
	if err != nil {
		t.Fatalf("the reader refused a valid workflow: %v", err)
	}
	if len(got) != 1 || got[0] != "shared" {
		t.Errorf("the reader found %v in a workflow whose `shared` job runs npm in shared/, want [shared]", got)
	}
	clean, err := ciSharedPackageJobs("name: ci\non:\n  push:\njobs:\n  go:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n")
	if err != nil {
		t.Fatalf("the reader refused a valid workflow: %v", err)
	}
	if len(clean) != 0 {
		t.Errorf("the reader found %v in a Go-only workflow, want none", clean)
	}
	// The third state, and the one this reader used to report as "none": a file
	// it cannot parse. Swallowing that would let a caller assert "ci.yml runs no
	// shared-package tests" about a ci.yml that is not YAML at all, so the error
	// is returned and the caller fails on it.
	pending, err := ciSharedPackageJobs("name: ci\non:\n  push:\njobs:\n  go: [unterminated\n")
	if err == nil {
		t.Errorf("the reader accepted a workflow that is not YAML (it reported %v): a caller asserting 'no shared/ jobs' would then pass on a file it could not read", pending)
	}
}

// TestDocsMobileEasRunbookDoesNotRestateCheck89 asserts the runbook defers to
// the register's text, keeps its own guard as the check's (the block the owner
// re-runs before every step) and repeats none of its stop conditions as its
// own. It also asserts the two values Check 89's guard turns on are named the
// way the check names them, so the two files cannot drift into disagreement
// about which profile is the store one.
func TestDocsMobileEasRunbookDoesNotRestateCheck89(t *testing.T) {
	doc := runbook(t)
	for _, want := range []string{
		"This runbook does not restate Check 89.",
		"The register is its authority.",
		"A profile whose submit section names a public track is a stop",
		"the `internal` profile",
		"never `production`",
		// What Check 89 asks the owner to paste back, by name.
		"gitCommitHash",
		"credentials source",
		"both submission ids",
		"the secret **names**",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not say %q", mobileEasRunbook, want)
		}
	}
}

// TestDocsMobileEasRunbookRecordsTheBundleIdentityTension asserts the one
// mismatch an owner will meet first is recorded rather than papered over.
// mobile/src/app-config.test.js (P3.1, on main) asserts mobile/app.json carries
// no bundle identifier and no Android package; the guard block of Check 89
// (P4.2a, PR #580) reads both fields out of that same file. Both are on the
// tree, neither is wrong on its own, and this runbook does not get to decide
// which is amended -- so it must say so, name both files, and say the owner
// rules.
func TestDocsMobileEasRunbookRecordsTheBundleIdentityTension(t *testing.T) {
	doc := runbook(t)
	for _, want := range []string{
		"Recorded tensions, not resolved here",
		"app-config.test.js",
		"bundleIdentifier",
		"android.package",
		"the owner rules",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not record %q", mobileEasRunbook, want)
		}
	}
	if !strings.Contains(doc, "mobile/app.json") {
		t.Errorf("%s records the identity tension without naming the file both sides read", mobileEasRunbook)
	}
}

// runbookDocHosts is the allow-list of hosts a runbook may cite: the accounts'
// own documentation, the Expo project's, GitHub's, and this repository. A URL
// that is not one of these is a link the owner is asked to trust for a credential
// step, and the fix is the vendor's own documentation page.
var runbookDocHosts = map[string]bool{
	"docs.expo.dev":             true,
	"expo.dev":                  true,
	"developer.apple.com":       true,
	"appstoreconnect.apple.com": true,
	"play.google.com":           true,
	"support.google.com":        true,
	"docs.github.com":           true,
	"github.com":                true,
}

// TestDocsMobileEasRunbookCitesOnlyTheAccountsOwnDocumentation asserts every
// URL in the runbook is https and on a host the owner's accounts own. The
// runbook deliberately does not state an exact console label as certain -- the
// vendors rename them -- so each step names the page to check, and this test
// holds those pages to the accounts' own documentation rather than to a
// look-alike.
func TestDocsMobileEasRunbookCitesOnlyTheAccountsOwnDocumentation(t *testing.T) {
	doc := readDoc(t, mobileEasRunbook)
	links := regexp.MustCompile("https?://[^\\s)\\]<>\"'`]+").FindAllString(doc, -1)
	if len(links) < 8 {
		t.Fatalf("%s cites %d URLs: the vendor steps are supposed to point at the vendors' documentation", mobileEasRunbook, len(links))
	}
	for _, link := range links {
		u, err := url.Parse(strings.TrimRight(link, ".,;:"))
		if err != nil {
			t.Errorf("%s cites %q, which is not a URL: %v", mobileEasRunbook, link, err)
			continue
		}
		if u.Scheme != "https" {
			t.Errorf("%s cites %q over %s: every one of these pages is https", mobileEasRunbook, link, u.Scheme)
		}
		if !runbookDocHosts[u.Host] {
			t.Errorf("%s cites %q: %q is not one of the accounts' own documentation hosts", mobileEasRunbook, link, u.Host)
		}
	}
}

// runbookSecretShapes are the shapes a credential takes when it is written down
// by accident, and the runbook must hold none of them. The list is the one
// mobile/src/accounts/settings.js refuses values with (SECRET_SHAPES: jwt,
// pem-private-key, pem-certificate, google-api-key, model-api-key, long-hex),
// in the shapes this repository actually writes them -- plus the two shapes a
// *page* holds that a settings value cannot: a provider token pasted into prose
// (a GitHub or AWS prefix, which settings.js has no reason to know) and a value
// assigned to a name that says what it is.
//
// **long-hex is here deliberately, and it is the entry a blob-length check would
// miss.** settings.js refuses any run of 32 or more hex digits; runbookBlobish
// below needs 40 characters *and* an upper-case letter and a digit, so a pasted
// 32- or 64-character lower-case hex secret -- a MAC key, a hash, an id-shaped
// token, all of them written down as lower-case hex in practice -- passes the
// blob check and is caught only by this one. The page is a page of prose, not a
// settings value, so this pattern is unanchored where settings.js's is anchored:
// a credential pasted mid-sentence is still a credential.
var runbookSecretShapes = []struct {
	what string
	re   *regexp.Regexp
}{
	{"a PEM block", regexp.MustCompile(`-----BEGIN [A-Z ]*(PRIVATE KEY|CERTIFICATE|RSA)`)},
	{"a model-provider key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}`)},
	{"a GitHub token", regexp.MustCompile(`\b(ghp_|gho_|ghs_|ghu_|github_pat_)[A-Za-z0-9_]{10,}`)},
	{"an AWS access key id", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"a Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"a JSON Web Token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.`)},
	{"a long hex string", regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`)},
	{"a value assigned to a credential's name", regexp.MustCompile(`(?i)\b(secret|token|password|apikey|api_key|private_key|service_account)\s*[:=]\s*["']?[A-Za-z0-9/+_.-]{16,}`)},
}

// runbookBlobish finds an unbroken run of 40 or more alphanumerics that holds an
// upper-case letter, a lower-case letter and a digit -- a key, a token or a
// commit SHA written into the page. The three-letter requirement is what keeps
// this from firing on a long word or a path: English prose is separated by
// spaces and URLs are separated by dots and slashes, so neither produces such a
// run, and a base64 blob of key length essentially always does.
var runbookBlobish = regexp.MustCompile(`\b[A-Za-z0-9]{40,}\b`)

func runbookSecretShaped(page string) []string {
	var found []string
	for _, shape := range runbookSecretShapes {
		for _, hit := range shape.re.FindAllString(page, -1) {
			found = append(found, shape.what+": "+hit)
		}
	}
	for _, hit := range runbookBlobish.FindAllString(page, -1) {
		if strings.ContainsAny(hit, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") &&
			strings.ContainsAny(hit, "abcdefghijklmnopqrstuvwxyz") &&
			strings.ContainsAny(hit, "0123456789") {
			found = append(found, "a long alphanumeric blob: "+hit)
		}
	}
	return found
}

// TestMobileEasRunbookSecretScanFindsEveryShape is the positive control, and it
// comes first for the reason every fence in this repository has one: a scan
// whose patterns match nothing reports a clean page whatever the page says.
// Every example is assembled at run time from harmless pieces, so this test
// file holds no credential-shaped literal either (mobile/src/accounts'
// convention).
func TestMobileEasRunbookSecretScanFindsEveryShape(t *testing.T) {
	planted := []struct {
		what  string
		value string
	}{
		{"a PEM block", "-" + "----BEGIN " + "PRIVATE KEY-----"},
		{"a model-provider key", "sk-" + "abcdef123456"},
		{"a GitHub token", "ghp_" + "abcdefghijklmnopqrst"},
		{"an AWS access key id", "AKIA" + "IOSFODNN7EXAMPLE"},
		{"a Google API key", "AI" + "za" + strings.Repeat("y", 35)},
		{"a JSON Web Token", "eyJ" + "hbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiIxIn0" + "." + "signature"},
		// Lower-case hex only, at a length settings.js refuses and runbookBlobish
		// cannot see: no upper-case letter and no digit, so the blob check stays
		// silent and only the long-hex shape catches it.
		{"a long hex string", "dead" + strings.Repeat("beef", 8)},
		{"a long alphanumeric blob", "Aa1" + strings.Repeat("b2C", 14)},
		{"a value assigned to a credential's name", "password: " + "hunter2hunter2hunter"},
	}
	for _, tc := range planted {
		if got := runbookSecretShaped(tc.value); len(got) == 0 {
			t.Errorf("the scan does not find %s in a planted %q: a green run over the runbook would mean nothing", tc.what, tc.value)
		}
	}
	// The point of the long-hex entry, stated so that an edit which "simplifies"
	// the list back fails here rather than quietly reopening the hole: this value
	// is invisible to the blob check and visible only to the long-hex shape.
	hexOnly := "dead" + strings.Repeat("beef", 8)
	if runbookBlobish.MatchString(hexOnly) {
		t.Errorf("runbookBlobish matches %q: the long-hex shape is no longer the only thing that catches it, so this control no longer proves what it claims", hexOnly)
	}
	if got := runbookSecretShaped(hexOnly); len(got) != 1 || !strings.Contains(got[0], "a long hex string") {
		t.Errorf("the long-hex shape does not catch %q on its own: got %q", hexOnly, got)
	}
	// And the control's other half: the words the runbook is made of are not
	// false positives, so a failure names a real defect.
	clean := []string{
		"The App Store Connect API key is the owner's, and it is never in CI.",
		"`google-play-service-account.json` is named by `serviceAccountKeyPath`, and its contents are never read.",
		"See https://docs.expo.dev/app-signing/managed-credentials/ for where EAS keeps the certificate.",
		"Pin every action to a full 40-character commit SHA.",
		// A short run of hex letters is a word, not a key: the long-hex shape
		// starts at 32 characters, the length settings.js starts refusing at.
		"A hex-looking word such as deadbeefcafe is not a credential here.",
	}
	for _, page := range clean {
		if got := runbookSecretShaped(page); len(got) != 0 {
			t.Errorf("the scan calls clean prose a credential: %q -> %q", page, got)
		}
	}
}

// TestDocsMobileEasRunbookHoldsNoSecretShapedLiteral is the runbook's own half
// of the control above: names, paths and identifiers, and not one value. Check
// 89's step 5 makes the same claim of the repository by hand; this is the
// machine-checkable half of it for the page that tells the owner where the keys
// go.
func TestDocsMobileEasRunbookHoldsNoSecretShapedLiteral(t *testing.T) {
	doc := readDoc(t, mobileEasRunbook)
	if found := runbookSecretShaped(doc); len(found) != 0 {
		t.Errorf("%s holds %d secret-shaped literal(s): %v", mobileEasRunbook, len(found), found)
	}
	// The names it is supposed to hold, so the scan cannot be satisfied by a
	// runbook that names no credential at all.
	for _, want := range []string{"EXPO_TOKEN", "google-play-service-account.json"} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not name %q: the scan above is only meaningful over a page that names them", mobileEasRunbook, want)
		}
	}
}

// TestDocsMobileReadmeListsTheRunbook asserts the directory's own index carries
// the runbook: docs/mobile/README.md is the table a reader meets first, and a
// page nobody is pointed at is a page the next owner rediscovers from the plan.
func TestDocsMobileReadmeListsTheRunbook(t *testing.T) {
	readme := readDoc(t, "docs/mobile/README.md")
	for _, want := range []string{
		"eas-signing-runbook.md",
		"P4.4",
		"Check 89",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("docs/mobile/README.md does not name %q", want)
		}
	}
	if _, err := os.Stat("../../docs/mobile/eas-signing-runbook.md"); err != nil {
		t.Errorf("the README points at a runbook that is not there: %v", err)
	}
}
