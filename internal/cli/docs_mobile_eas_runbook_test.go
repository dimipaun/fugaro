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
// identity tension it records -- P3.1's config test keeps the release identity
// out of mobile/app.json and Check 89's guard reads the identity out of the
// config a build resolves, which P4.4's own files settled (PR #675) by moving
// the read to mobile/app.config.js rather than by amending either fence -- and
// the directory's own index, which is where a reader finds it.
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
// Five tests hold what the page says about the *repository* to the repository:
// TestDocsMobileEasRunbookNamesEveryProtectedEnvironment derives the protected
// environments -- and the job each one is declared in -- from the workflow files
// themselves, so the page's citations are by content and not by line number;
// TestDocsMobileEasRunbookMatchesTheStagedReleaseWorkflow holds §10, the page's
// whole account of the mobile release workflow, to
// docs/pending-workflows/mobile-release.yml -- its trigger, both jobs, the
// environment the jobs declare and the one secret they reach through it (PR
// #777, the staged file the page's §10 is a description of);
// TestDocsMobileEasRunbookPlacesExpoTokenInTheEnvironmentSecret holds §7's
// credential table, §9's environment settings and §10's workflow shape to one
// placement for the one secret CI may hold, because a repository secret and an
// environment secret are different fences;
// TestDocsMobileEasRunbookSaysP4Dot3IsStagedNotRunning reads ci.yml, so the day
// the owner copies P4.3's job in, the page has to stop calling it staged; and
// TestDocsMobileEasRunbookMatchesTheEasFilesItDescribes reads P4.4's own two
// files -- mobile/eas.json and mobile/app.config.js, PR #675 -- so §8's profile
// table, its submit track, the App Store Connect app id PR #759 committed there
// and its two variable names are held to the files rather than to themselves.
// (That one skips where the files are not yet in the tree, for the same reason
// the register comparison does.)
//
// On mutation proof. The revision of this file that arrived with PR #609
// carried a table of sixty-odd mutations, each of which had been planted, run
// red and removed. Those mutations belong to that revision and to the tree it
// was written against, and this refresh did not re-run them: the tests they
// proved are the tests above, unchanged, and re-running sixty mutations would
// say nothing new about them. What this refresh *did* mutation-prove is its one
// new claim -- the headline test,
// TestDocsMobileEasRunbookMatchesTheStagedReleaseWorkflow, which is the test
// that holds §10 to the staged workflow file. The proof is one mutation, run
// once: the page's trigger row was reworded from the staged file's tag filter
// (`v[0-9]+.[0-9]+.[0-9]+`) to a bare `v*`, the run went red on that test
// alone, and the wording was put back. One mutation is what the brief asked for
// and what a headline test needs: the point is that the test reads the file and
// not the page's memory of it, and one planted divergence shows that.
//
// The other tests in this file are the ones it carried before, with the
// assertions the refresh had to correct; where a reader wants to know whether
// one of *those* still bites, the honest answer is that this refresh left them
// as they were and did not re-derive their proof.
//
import (
	"encoding/json"
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
		{"that no agent has run any of it", "No agent has run any of it."},
		{"that it is instructions rather than a record", "is the instructions, not a record"},
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
// the tree is now: release.yml signs keyless with no environment, and a
// v*-only deployment-branch policy cannot be reached by a dispatch from main,
// which is the tie §9 and §10 have to make between the environment's branch
// policy and the trigger. The environment set itself is *derived* from the
// workflow files by TestDocsMobileEasRunbookNamesEveryProtectedEnvironment --
// `website-live`, `console-release`, and `mobile-release` from the staged file
// -- so this test only refuses the stale sentences an earlier revision of the
// page carried, including the one that read as a description of a workflow
// nobody had written yet.
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
// protected environments this tree declares, written from the count the files
// give. The page must carry this exact sentence, so an environment added or
// removed later breaks the test until the page is corrected -- which is the
// whole point of deriving it rather than asserting a number. "Declared on this
// tree" rather than "in this repository" because one of them is declared only by
// a file staged under docs/pending-workflows/: the count is the declarations'
// and the page says which of them is installed.
func protectedEnvironmentSentence(n int) string {
	word := numberWord(n)
	if n == 1 {
		return "**One protected environment is declared on this tree today:**"
	}
	return fmt.Sprintf("**%s%s protected environments are declared on this tree today:**", strings.ToUpper(word[:1]), word[1:])
}

var numberWords = []string{"no", "one", "two", "three", "four", "five", "six", "seven", "eight"}

func numberWord(n int) string {
	if n >= 0 && n < len(numberWords) {
		return numberWords[n]
	}
	return strconv.Itoa(n)
}

// ordinalWord is the word the page uses for the mobile release environment --
// the one whose declaration is the staged file's, counted with the installed
// ones because §9's table is one list of declarations.
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
// of them is `mobile-release` and that its declaration is the *staged* file's
// rather than an installed workflow's. The claim cannot rot: a workflow added
// with an `environment:` fails this test until the page names it, so does the
// deletion of one, and so does copying the staged file into `.github/workflows/`
// without rewriting §9 and §10.
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
	// The environment the page's §10 is about is `mobile-release`, and since PR
	// #777 it *is* declared -- by a file staged under docs/pending-workflows/,
	// which is the directory this reader also reads, precisely so a staged copy
	// cannot hide an environment. What the page has to get right is which of the
	// two it is: an installed workflow would mean §10 describes something already
	// running, and the page would have to stop calling it staged. The row above
	// already holds the file and the job; this holds the tense.
	decls, exists := envs["mobile-release"]
	if !exists {
		t.Errorf("no workflow on this tree declares `environment: mobile-release`: §9's table and §10 are about it, and this reader reads docs/pending-workflows/ too, so the staged file of PR #777 should be here")
	}
	installed := false
	for _, decl := range decls {
		if filepath.Dir(decl.File) == ".github/workflows" {
			installed = true
		}
	}
	row := tableRow(raw, "| **`mobile-release`** |")
	if installed {
		// Copied in on 2026-10-23: the row has to say so, and must no longer
		// read "staged, not applied" about a file that is running.
		if !strings.Contains(row, "applied 2026-10-23") || strings.Contains(row, "staged, not applied") {
			t.Errorf("%s's `mobile-release` row does not say \"applied 2026-10-23\" (or still says \"staged, not applied\"): .github/workflows/mobile-release.yml is installed, so the row has to describe a running pipeline", mobileEasRunbook)
		}
	} else if !strings.Contains(row, "staged, not applied") {
		t.Errorf("%s's `mobile-release` row does not say \"staged, not applied\": the file is under docs/pending-workflows/, so a row that reads as an installed workflow claims a running pipeline this repository does not have", mobileEasRunbook)
	}
	// And which environment the page says it is, derived from how many the files
	// declare: with the staged file counted, `mobile-release` is the third, and a
	// deletion or an addition above it moves that word.
	want := fmt.Sprintf("**`mobile-release` is the %s**", ordinalWord(len(envs)))
	if !strings.Contains(doc, want) {
		t.Errorf("%s does not say %q (%d environments declared by %v): the word is derived from the files, not written twice", mobileEasRunbook, want, len(envs), names)
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

// stagedMobileReleaseWorkflow is the file §10 is an account of: the app's
// release workflow, written and **staged** under docs/pending-workflows/ (PR
// #777) for the owner to copy in. It is the one workflow file here that is not
// installed, which is what makes the page's tense about it checkable at all --
// and it is the file that superseded §10's earlier ceiling ("CI builds a test
// artifact and never runs `eas submit`"), so the page's account of it is the
// part of this runbook most likely to drift from the tree.
const stagedMobileReleaseWorkflow = "docs/pending-workflows/mobile-release.yml"

// appliedMobileReleaseWorkflow is where the owner copied that file on
// 2026-10-23. Once it exists and the staged copy is gone, §10 is an account of
// the running file instead, and the page says so in the present tense.
const appliedMobileReleaseWorkflow = ".github/workflows/mobile-release.yml"

// mobileReleaseWorkflowForRunbook is the file §10 is an account of on this
// tree: the staged copy while it exists, the applied file once the copy-in has
// removed it. Both existing at once is the half-done copy-in, and the staged
// copy (the reviewed text) is read then; neither existing is "".
func mobileReleaseWorkflowForRunbook() string {
	if _, err := os.Stat(filepath.Join("..", "..", stagedMobileReleaseWorkflow)); err == nil {
		return stagedMobileReleaseWorkflow
	}
	if _, err := os.Stat(filepath.Join("..", "..", appliedMobileReleaseWorkflow)); err == nil {
		return appliedMobileReleaseWorkflow
	}
	return ""
}

// workflowSecretRE finds a `secrets.NAME` reference. The reader below scans the
// file's text rather than one parsed field because a secret can be reached from
// a job's `env:`, a step's `env:` or a step's `with:`, and the page's claim
// ("one, and no other") is about the file and not about one of those three.
var workflowSecretRE = regexp.MustCompile(`secrets\.([A-Za-z_][A-Za-z0-9_]*)`)

// workflowSecrets returns every secret name a workflow text references, sorted
// and deduplicated.
func workflowSecrets(text string) []string {
	seen := map[string]bool{}
	for _, m := range workflowSecretRE.FindAllStringSubmatch(text, -1) {
		seen[m[1]] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestDocsMobileEasRunbookMatchesTheStagedReleaseWorkflow is this page's
// headline claim about the repository after PR #777: §10 says it is an account
// of docs/pending-workflows/mobile-release.yml read off the file, "and where the
// file and this page disagree, the file wins". This test is what makes that
// sentence true rather than decorative. It reads the file's own YAML -- the tag
// filter, the dispatch input and its default, the refusals, the two jobs and the
// environment each declares, each job's permissions and its release-gate step,
// the pins, and every `secrets.NAME` the file reaches -- and requires §10 to
// name each of them, so an edit to the workflow that the page does not follow
// fails here rather than on the day of a release.
//
// It also holds the two edges the page cannot get wrong about the file's
// *state*: the file has to still be under docs/pending-workflows/, because a
// copy into .github/workflows/ makes the page's tense wrong everywhere it
// appears; and the page's Secrets row may name no secret the file does not read,
// which is the assertion that catches a page that has rediscovered a store
// credential in CI.
func TestDocsMobileEasRunbookMatchesTheStagedReleaseWorkflow(t *testing.T) {
	wfPath := mobileReleaseWorkflowForRunbook()
	if wfPath == "" {
		t.Skipf("neither %s nor %s is in this tree: §10 is this page's account of the app's release workflow, so a tree without it has nothing for §10 to be an account of", stagedMobileReleaseWorkflow, appliedMobileReleaseWorkflow)
	}
	text := readDoc(t, wfPath)
	var wf struct {
		On struct {
			Push struct {
				Tags []string `yaml:"tags"`
			} `yaml:"push"`
			WorkflowDispatch struct {
				Inputs map[string]struct {
					Type    string `yaml:"type"`
					Default bool   `yaml:"default"`
				} `yaml:"inputs"`
			} `yaml:"workflow_dispatch"`
			PullRequest       any `yaml:"pull_request"`
			PullRequestTarget any `yaml:"pull_request_target"`
			WorkflowRun       any `yaml:"workflow_run"`
			Schedule          any `yaml:"schedule"`
		} `yaml:"on"`
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Environment string            `yaml:"environment"`
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Uses string `yaml:"uses"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(text), &wf); err != nil {
		t.Fatalf("%s is not valid YAML: %v -- §10 claims the file as its authority, so a file this test cannot read is not one the page agrees with", wfPath, err)
	}
	raw := readDoc(t, mobileEasRunbook)
	section := sectionText(raw, "## 10. ", "## 11. ")
	if section == "" {
		t.Fatal("§10 was renamed: this test reads the section between \"## 10. \" and \"## 11. \", and it has to be repointed at it")
	}
	if !strings.Contains(section, wfPath) {
		t.Errorf("§10 does not name %s: the file is what the section is an account of, and a reader has to be able to open it", wfPath)
	}

	// The trigger, and the refusals: the tag filter is derived from the file, so
	// the page has to print the file's own filter rather than "a release tag".
	if len(wf.On.Push.Tags) != 1 {
		t.Errorf("%s declares %d tag filters (%v): §10 describes one, the filter the sibling release workflows use", wfPath, len(wf.On.Push.Tags), wf.On.Push.Tags)
	}
	for _, filter := range wf.On.Push.Tags {
		if want := "`" + filter + "`"; !strings.Contains(section, want) {
			t.Errorf("§10 does not name the file's tag filter %s: a bare `v*` is not this file's trigger, and the page's account of the trigger is the whole reason §9's branch policy has to admit a tag", want)
		}
	}
	if wf.On.WorkflowDispatch.Inputs == nil {
		t.Errorf("%s declares no workflow_dispatch input: §10 describes the dispatch and its one dry-run input", wfPath)
	}
	for name, input := range wf.On.WorkflowDispatch.Inputs {
		if want := fmt.Sprintf("input `%s` **defaults to `%t`", name, input.Default); !strings.Contains(section, want) {
			t.Errorf("§10 does not say %q: %s's dispatch input is %q with default %t, and the default is what decides whether a person trying the workflow reaches the store", want, wfPath, name, input.Default)
		}
	}
	for _, refused := range []struct {
		event string
		what  any
	}{
		{"pull_request", wf.On.PullRequest},
		{"pull_request_target", wf.On.PullRequestTarget},
		{"workflow_run", wf.On.WorkflowRun},
		{"schedule", wf.On.Schedule},
	} {
		if refused.what != nil {
			t.Errorf("%s declares a `%s` trigger: §10 says the file refuses it, because a job that builds on EAS and holds an Expo token must never run on a pull request", wfPath, refused.event)
		}
	}

	// The two jobs: one per platform, each declaring the environment and the
	// least-privilege pair, each running the release gate as its first step --
	// which is what §10's "Locks" row claims.
	if len(wf.Jobs) != 2 {
		t.Errorf("%s declares %d jobs (%v): §10 says the file is two jobs, one per platform, independent of each other", wfPath, len(wf.Jobs), noteKeys(wf.Jobs))
	}
	for _, name := range noteKeys(wf.Jobs) {
		job := wf.Jobs[name]
		if want := "`" + name + "`"; !strings.Contains(section, want) {
			t.Errorf("§10 does not name the %s job: the page's account of the workflow is an account of both jobs", want)
		}
		if want := "`environment: " + job.Environment + "`"; !strings.Contains(section, want) {
			t.Errorf("§10 does not name %s, the environment the file's %s job declares: the lock the page describes is the declaration in the file", want, name)
		}
		for key, value := range job.Permissions {
			if want := fmt.Sprintf("`%s: %s`", key, value); !strings.Contains(section, want) {
				t.Errorf("§10 does not name %s, a scope the file's %s job declares: the page's claim is that the file holds no write scope of any kind", want, name)
			}
			if value != "read" {
				t.Errorf("%s's %s job declares `%s: %s`: §10 says the file holds no write scope, and a write scope here is the page being wrong about the tree", wfPath, name, key, value)
			}
		}
		gate := false
		for _, step := range job.Steps {
			if strings.Contains(step.Run, "scripts/release-gate.sh") {
				gate = true
			}
		}
		if !gate {
			t.Errorf("%s's %s job runs no step naming scripts/release-gate.sh: §10 says each job runs that script as its own first step, and it is the second of the two locks the page describes", wfPath, name)
		}
	}
	if !strings.Contains(section, "scripts/release-gate.sh") {
		t.Errorf("§10 does not name scripts/release-gate.sh: it is the release gate the file's jobs run, and the page says the app is outside it (D125) while still running it")
	}

	// The workflow-level permissions, and the pins: the page claims both, and the
	// file is the authority for both.
	for key, value := range wf.Permissions {
		if want := fmt.Sprintf("`%s: %s`", key, value); !strings.Contains(section, want) {
			t.Errorf("§10 does not name %s, the workflow-level permission %s declares: least privilege is a claim about this block", want, wfPath)
		}
	}
	pinRE := regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}$`)
	for _, m := range regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*(\S+)`).FindAllStringSubmatch(text, -1) {
		if !pinRE.MatchString(m[1]) {
			t.Errorf("%s uses %s: §10 says every action is pinned to a full 40-character commit SHA, and a tag or a short SHA here is the page being wrong about the tree", wfPath, m[1])
		}
	}

	// The secrets: the file reads one, and the page's Secrets row may name no
	// other. §10 is where a reader learns what a store credential in this
	// repository's CI would mean, so a row that named a second secret would be
	// the finding Check 89's step 5 exists to produce, written down as the plan.
	read := workflowSecrets(text)
	if len(read) != 1 {
		t.Errorf("%s references the secrets %v: §10 says the file reads exactly one, the Expo token, because the store credentials stay on the EAS project", wfPath, read)
	}
	row := tableRow(raw, "| **Secrets** |")
	if row == "" {
		t.Fatal("§10's Secrets row was renamed: this test reads the line beginning \"| **Secrets** |\", and it has to be repointed at it")
	}
	for _, name := range workflowSecrets(row) {
		if !slices.Contains(read, name) {
			t.Errorf("§10's Secrets row names `secrets.%s`, which %s does not reference: the page's account of the file's secrets is derived from the file, and a secret the file does not read is a credential the page has put in CI", name, wfPath)
		}
	}
	for _, name := range read {
		if want := "`secrets." + name + "`"; !strings.Contains(row, want) {
			t.Errorf("§10's Secrets row does not name %s, the one secret %s reads: the row's whole claim is that the token is the only credential CI holds", want, wfPath)
		}
	}
}

// TestWorkflowSecretsFindsEveryPlaceASecretCanBeReached pins the reader above
// against a temp file rather than today's workflow: a secret reached from a
// step's `env:`, from a step's `with:` and from a job's `env:` are all secrets
// the file reads, a name that only *looks* like one (`secrets.` inside a
// comment, or `secrets: read` as a permission) is not, and the names come back
// sorted and deduplicated.
func TestWorkflowSecretsFindsEveryPlaceASecretCanBeReached(t *testing.T) {
	const body = `# this comment names secrets.EXPO_TOKEN twice: secrets.EXPO_TOKEN
name: probe
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  build:
    environment: probe
    env:
      FROM_JOB: ${{ secrets.JOB_LEVEL }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
      - run: echo build
        env:
          FROM_STEP: ${{ secrets.STEP_LEVEL }}
      - uses: actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1
        with:
          node-version: ${{ secrets.WITH_LEVEL }}
`
	got := workflowSecrets(body)
	want := []string{"EXPO_TOKEN", "JOB_LEVEL", "STEP_LEVEL", "WITH_LEVEL"}
	if !slices.Equal(got, want) {
		t.Errorf("the reader found %v, want %v: a secret is read wherever `secrets.NAME` appears, including inside a comment, and the reader's job is to over-report rather than to miss one", got, want)
	}
	if empty := workflowSecrets("name: none\non:\n  workflow_dispatch:\n"); len(empty) != 0 {
		t.Errorf("the reader found %v in a workflow with no secrets: a reader that invents a name would make §10's row check pass on anything", empty)
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

// check89RegisterSection reads Check 89's own section out of
// docs/gcp-live-checklist.md -- the register the runbook quotes and defers to --
// with its whitespace normalised and its `#` comment markers dropped.
//
// The markers have to go for the *prose* to be readable as sentences: the
// block's comments wrap every ~78 columns, so a sentence about a missing
// interpreter is written
//
//	# missing interpreter is a Python FileNotFoundError -- a traceback, not the
//	# sentence this guard promises.
//
// and only after `# ` is removed does that read as the one sentence it is. No
// stop message contains the marker, so the comparison of stop messages below is
// unaffected by removing it.
//
// The pin used to hold the register's sentences as literals in this file, copied
// from main by hand, because P4.2a's text (PR #580) is not in this branch's tree.
// A literal cannot notice the register changing its mind, so everything the page
// quotes is read from the register itself now: a sentence reworded there fails
// the page here.
//
// A tree whose register has no Check 89 has nothing to compare against, so this
// skips with that reason rather than passing quietly: a skip is visible, which
// is the difference between this and swallowing the file.
func check89RegisterSection(t *testing.T) string {
	t.Helper()
	const (
		section = "## Check 89"
		path    = "docs/gcp-live-checklist.md"
	)
	doc := readDoc(t, path)
	start := strings.Index(doc, section)
	if start < 0 {
		t.Skipf("%s has no %q section in this tree: P4.2a's text lands with PR #580, so a tree that merged main before it has no register here to hold the page's quotations to -- this test compares them as soon as the section is in the tree", path, section)
	}
	rest := doc[start+len(section):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return strings.ReplaceAll(strings.Join(strings.Fields(rest), " "), "# ", "")
}

// check89RegisterStops reads Check 89's own stop messages out of the section
// above: the page quotes them, and it may not quote something the register does
// not say.
func check89RegisterStops(t *testing.T) []string {
	t.Helper()
	section := check89RegisterSection(t)
	// The guard's file tests and its python line are the stops a reader meets
	// before any step runs; a section with fewer than the two the page quotes is
	// the section moved, not a check that stops less often.
	stops := check89Stops(section)
	if len(stops) < 2 {
		t.Fatalf("%s's %q section yields %d stop messages, want at least the guard's file tests and its python line: the section or the marker has moved, and this test now reads a different part of the register", "docs/gcp-live-checklist.md", "## Check 89", len(stops))
	}
	return stops
}

// check89RegisterInterpreterNote is the register's own sentence about why the
// guard tests for `node` at all: the block's comment says a missing interpreter
// would be a *Python* FileNotFoundError -- a traceback, not the sentence the
// guard promises -- which is what the guard's `node` test exists to turn into
// its own stop.
//
// The sentence is read here rather than written down, so the page's quotation of
// it is held to the register like every other quotation on the page. The elision
// this catches is the one a paraphrase leaves behind: a quotation that begins
// inside the sentence ("a FileNotFoundError", dropping the "Python") reads as
// verbatim and is not.
func check89RegisterInterpreterNote(t *testing.T) string {
	t.Helper()
	section := check89RegisterSection(t)
	const start, end = "missing interpreter is ", "this guard promises"
	i := strings.Index(section, start)
	if i < 0 {
		t.Fatalf("the register's Check 89 section no longer says %q: the guard's reason for testing for `node` is a sentence of the register's, and the page quotes it", start)
	}
	j := strings.Index(section[i:], end)
	if j < 0 {
		t.Fatalf("the register's Check 89 section names a missing interpreter but never reaches %q: the sentence the page quotes has been reworded or dropped", end)
	}
	return section[i+len(start) : i+j+len(end)]
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
	// And the two stops the page's Tension 1 and Tension 2 turn on are among the
	// register's own sentences, found in the register by what they are about
	// rather than repeated here: the store identity the guard reads out of the
	// config `mobile/app.config.js` resolves, and the `mobile/eas.json` file
	// P4.4 adds. The marker for the first is the phrase that is unique to that
	// stop -- the guard's earlier python line mentions `android.package` too, in
	// the message for a config that is not a config, and that is a different
	// stop from the one Tension 1 quotes.
	for _, marker := range []string{"the config resolved through app.config.js", "mobile/eas.json"} {
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
//
// The word "traceback" is refused wherever it is the page's own prose. The one
// place the page may use it is inside a quotation, because the register uses it
// itself -- in its note about the *missing interpreter*, which is why the guard
// tests for `node` at all -- so quoted spans are subtracted from the page and
// the word refused in what is left. That the quotation is the register's, whole,
// is held by TestDocsMobileEasRunbookQuotesTheRegistersInterpreterNote, which
// reads the sentence out of the register; a page that dropped the quotation and
// used the word in prose fails here, and a page that elided it inside the
// quotation marks fails there.
func TestDocsMobileEasRunbookDoesNotGiveTheGuardAKeyError(t *testing.T) {
	const quoted = `"[^"]*"`
	doc := runbook(t)
	prose := regexp.MustCompile(quoted).ReplaceAllString(doc, "")
	for _, banned := range []string{
		"KeyError",
		"traceback",
		`d["ios"]["bundleIdentifier"]`,
		`d["android"]["package"]`,
		"dies with a traceback",
	} {
		if strings.Contains(prose, banned) {
			t.Errorf("%s says %q outside any quotation: Check 89's guard reads the two identity fields with .get() and stops with its own message, so there is no crash to describe", mobileEasRunbook, banned)
		}
	}
}

// TestDocsMobileEasRunbookQuotesTheRegistersInterpreterNote holds §3's one
// quotation of the register's *prose* -- as opposed to its stop messages, which
// TestDocsMobileEasRunbookQuotesCheck89sGuardStops compares -- to the register,
// word for word. The sentence is the block's reason for testing for `node`: a
// missing interpreter would be a Python FileNotFoundError, a traceback rather
// than a stop, so the guard refuses to run without one.
//
// It is read out of docs/gcp-live-checklist.md rather than written down here,
// because a copy written down here is compared with the page and not with the
// register: the page quoted it as "a FileNotFoundError -- a traceback, ...",
// eliding the "Python " inside the quotation marks, and a test that held the
// page to a copy of the page's own wording could not see it.
func TestDocsMobileEasRunbookQuotesTheRegistersInterpreterNote(t *testing.T) {
	note := check89RegisterInterpreterNote(t)
	doc := runbook(t)
	if !strings.Contains(doc, note) {
		t.Errorf("%s does not quote the register's sentence about the missing interpreter, %q, word for word: §3 says why the guard tests for `node`, and a quotation that begins inside the sentence is a paraphrase wearing quotation marks", mobileEasRunbook, note)
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

// TestDocsMobileEasRunbookMatchesTheEasFilesItDescribes reads P4.4's own two
// files -- mobile/eas.json and mobile/app.config.js, merged as PR #675 -- and
// holds §8 to them. §8 is the page's account of the profiles, the environments
// and the identities, and it claims the files as its authority: the three
// profile names, which of them is the store profile, its submit track, the two
// `cli` settings, which profile may increment its build number, the EAS
// environment each profile reads, and the two variable names the dynamic config
// resolves. Every one of those is read out of the file here, not repeated from
// the page, so an edit to either file that the page does not follow fails here
// rather than at the store.
//
// The files are in the tree now -- #675 landed them, #759 added the App Store
// Connect app id the submit profile carries -- so this test compares rather than
// skipping. It keeps the skip for a tree that does not have them, which is the
// same shape as check89RegisterStops, one file over.
func TestDocsMobileEasRunbookMatchesTheEasFilesItDescribes(t *testing.T) {
	const (
		easJSON    = "mobile/eas.json"
		appConfig  = "mobile/app.config.js"
		skipReason = "%s is not in this tree: §8 is this page's account of the two files P4.4 lands (#675), so a tree without them has nothing to hold §8 to -- this test compares them as soon as they are here"
	)
	for _, path := range []string{easJSON, appConfig} {
		if _, err := os.Stat(filepath.Join("..", "..", path)); err != nil {
			t.Skipf(skipReason, path)
		}
	}
	var eas struct {
		Cli struct {
			AppVersionSource string `json:"appVersionSource"`
			RequireCommit    bool   `json:"requireCommit"`
		} `json:"cli"`
		Build map[string]struct {
			Distribution  string `json:"distribution"`
			AutoIncrement bool   `json:"autoIncrement"`
			Environment   string `json:"environment"`
		} `json:"build"`
		Submit map[string]struct {
			IOS     *map[string]any `json:"ios"`
			Android struct {
				Track string `json:"track"`
			} `json:"android"`
		} `json:"submit"`
	}
	if err := json.Unmarshal([]byte(readDoc(t, easJSON)), &eas); err != nil {
		t.Fatalf("%s is not valid JSON: %v -- §8 describes this file, so a file this test cannot read is not one the page agrees with", easJSON, err)
	}
	raw := readDoc(t, mobileEasRunbook)
	section := sectionText(raw, "## 8. ", "## 9. ")
	if section == "" {
		t.Fatal("§8 was renamed: this test reads the section between \"## 8. \" and \"## 9. \", and it has to be repointed at it")
	}

	// The three profile names, and only them: §8's table is one row each, and a
	// profile added to the file is a row the page is missing.
	if len(eas.Build) != 3 {
		t.Errorf("%s has %d build profiles (%v), want the three §8 describes", easJSON, len(eas.Build), noteKeys(eas.Build))
	}
	for _, name := range []string{"development", "preview", "internal"} {
		profile, ok := eas.Build[name]
		if !ok {
			t.Errorf("%s has no %q build profile: §8's table names it as one of the three", easJSON, name)
			continue
		}
		for _, want := range []string{fmt.Sprintf("| **`%s`** |", name), fmt.Sprintf("environment: %q", profile.Environment)} {
			if !strings.Contains(section, want) {
				t.Errorf("§8 does not say %q: the profile table is the page's account of %s, read off the file", want, easJSON)
			}
		}
	}

	// The store profile, which is the one Check 89 builds and submits with: the
	// distribution, the build number and the submit track are the three values
	// that decide whether a mistake is a failed build or a public release.
	internal, ok := eas.Build["internal"]
	if !ok {
		t.Fatalf("%s has no `internal` build profile: Check 89's step 1 reads it by that name, so the file and the check have to agree", easJSON)
	}
	if internal.Distribution != "store" {
		t.Errorf("%s's `internal` profile has distribution %q: §8 says the store profile's distribution is \"store\", and an `internal`-distribution profile reaches no store at all", easJSON, internal.Distribution)
	}
	if !internal.AutoIncrement {
		t.Errorf("%s's `internal` profile does not set autoIncrement: §8 says the store profile is the one that raises EAS's build number, because a second internal build and submit would otherwise be refused by both stores", easJSON)
	}
	for name, profile := range eas.Build {
		if name != "internal" && profile.AutoIncrement {
			t.Errorf("%s's %q profile sets autoIncrement: §8 says it is on `internal` alone, because the other two spend this release's build numbers on artifacts nobody submits", easJSON, name)
		}
		if name != "internal" && profile.Distribution == "store" {
			t.Errorf("%s's %q profile has distribution \"store\": §8 says only `internal` is the store profile", easJSON, name)
		}
	}
	if eas.Cli.AppVersionSource != "remote" {
		t.Errorf("%s sets cli.appVersionSource to %q: §8's account of autoIncrement rests on it being \"remote\"", easJSON, eas.Cli.AppVersionSource)
	}
	if !eas.Cli.RequireCommit {
		t.Errorf("%s does not set cli.requireCommit: §8 says it is what makes a store build record the commit step 2 compares with $SHA", easJSON)
	}
	for _, want := range []string{
		`distribution: "store"`,
		"`cli.requireCommit: true`",
		"`cli.appVersionSource` is `\"remote\"`",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("§8 does not say %q: that is what %s has, and the page claims the file as its authority", want, easJSON)
		}
	}

	// The submit side: one submit profile, on the internal track, with the iOS
	// entry naming the App Store Connect app id PR #759 committed there -- the id
	// the owner's first submission of 2026-10-20 produced. The page used to say
	// the entry named nothing and that EAS asked for the app at submit time; that
	// was true of the file PR #675 landed and is false of the file in the tree,
	// so the assertion reads the id out of the file and requires §8 to name it.
	if len(eas.Submit) != 1 {
		t.Errorf("%s has %d submit profiles (%v): §8 says `internal` is the one, and `preview` has no submit section at all", easJSON, len(eas.Submit), noteKeys(eas.Submit))
	}
	submit, ok := eas.Submit["internal"]
	if !ok {
		t.Fatalf("%s has no `internal` submit profile: Check 89's step 1 reads submit.internal.android.track", easJSON)
	}
	if submit.Android.Track != "internal" {
		t.Errorf("%s submits `internal` builds to track %q: C8's whole content is that this value is `internal`", easJSON, submit.Android.Track)
	}
	if submit.IOS == nil {
		t.Fatalf("%s's `internal` submit profile has no `ios` entry: §8 names the app id it carries", easJSON)
	}
	ascAppID, _ := (*submit.IOS)["ascAppId"].(string)
	if ascAppID == "" {
		t.Fatalf("%s's `internal` submit profile's `ios` entry names %v: §8 says it carries the App Store Connect app id the owner's first submission produced, so the page and the file disagree about what is in the file", easJSON, noteKeys(*submit.IOS))
	}
	if len(*submit.IOS) != 1 {
		t.Errorf("%s's `internal` submit profile's `ios` entry names %d key(s) (%v): §8 says `ascAppId` is the one, and every other key there is a submission setting the page would have to account for", easJSON, len(*submit.IOS), noteKeys(*submit.IOS))
	}
	for _, want := range []string{
		"`submit.internal.android.track`",
		"`submit.internal.ios.ascAppId`",
		fmt.Sprintf("**`%s`**", ascAppID),
	} {
		if !strings.Contains(section, want) {
			t.Errorf("§8 does not say %q: that is what %s has", want, easJSON)
		}
	}

	// And the two names the dynamic config resolves, so the page's two exports
	// and the file's two variables are the same two names.
	config := readDoc(t, appConfig)
	for _, variable := range []string{"FUGARO_IOS_BUNDLE_ID", "FUGARO_ANDROID_PACKAGE"} {
		if !strings.Contains(config, variable) {
			t.Errorf("%s does not name %q: §8 says the store identity reaches a build through this file's two variables", appConfig, variable)
		}
		if !strings.Contains(section, variable) {
			t.Errorf("§8 does not name %q, the variable %s resolves", variable, appConfig)
		}
	}
}

// noteKeys renders a profile or submit map's keys sorted, for an error message
// about a file this test read: the reader of a failure should not have to open
// the file to learn what was in it.
func noteKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

// TestDocsMobileEasRunbookRecordsTheBundleIdentityTension asserts the mismatch
// an owner would otherwise meet as a surprise is recorded rather than papered
// over: mobile/src/app-config.test.js (P3.1, on main) asserts mobile/app.json
// carries no bundle identifier and no Android package, while the guard block of
// Check 89 (P4.2a, PR #580) reads the identity out of the config a build
// resolves. The page has to name both sides and say whose the ruling is. It
// records the settlement too -- P4.4's own files (PR #675) keep P3.1's fence and
// move the read to mobile/app.config.js -- so this test reads §14's heading,
// which says the section is about how each tension was settled, and not the
// earlier "not resolved here" one.
func TestDocsMobileEasRunbookRecordsTheBundleIdentityTension(t *testing.T) {
	doc := runbook(t)
	for _, want := range []string{
		"app-config.test.js",
		"bundleIdentifier",
		"android.package",
		"the owner rules",
		"#675",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not record %q", mobileEasRunbook, want)
		}
	}
	if !strings.Contains(doc, "mobile/app.json") {
		t.Errorf("%s records the identity tension without naming the file both sides read", mobileEasRunbook)
	}
	// The heading itself, and the contents link that points at it, read as lines
	// rather than as prose: the sentence appears twice on the page, so a check
	// that only looked for the words would stay green with the heading put back
	// to the earlier "not resolved here" and the link left corrected.
	raw := readDoc(t, mobileEasRunbook)
	for _, line := range []string{
		"\n## 14. Recorded tensions, and how each was settled\n",
		"(#14-recorded-tensions-and-how-each-was-settled)",
	} {
		if !strings.Contains(raw, line) {
			t.Errorf("%s has no %q: §14 records the two tensions that *are* settled now -- P4.4's own files closed the identity one (PR #675) -- and both the heading and the contents link have to say so", mobileEasRunbook, strings.TrimSpace(line))
		}
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
