package cli

// The census of every file that still names `fugaro init`, and the line the
// rename must not cross (setup-wizard plan, Task W0.2, emptied by W2.6;
// reconciliation §4, F20 and D9; Global Constraints 3 and 7; Convention 11).
//
// `fugaro init` is retired in favour of `fugaro setup` and `fugaro update`
// (D1, D2, D11), and the rename touched some 790 lines across more than three
// hundred files -- far more than one review can hold in its head. The owner's
// line (D9) is what makes it reviewable: rename live text, never rewrite a
// dated record. A line like that is only a line if both of its halves are
// written down, so this file is both of them. `pendingRename` was the census
// while the rename was in flight and is empty now that it is done, and
// `datedRecords` is the dated roots, stated once, here, and referenced by
// every task that renames rather than restated (Global Constraint 7: two
// copies of the list are two lists that drift).
//
// Two tests read the two halves:
//
//   - TestLiveInitCensusMatchesTheFrozenList walks the live roots, collects
//     every file naming `fugaro init` or the bare `init --<flag>`, and asserts
//     the set is empty and that `pendingRename` is. While the rename was in
//     flight it
//     asserted the two were *equal*, which fails in both directions and is
//     what made it a pin rather than a snapshot: a file that started naming
//     the command and was not listed failed it (the set grew -- a string
//     nobody had on their map), and a listed file that had stopped naming it
//     failed it too (the set shrank -- W2.x renamed it and left it on the map
//     the remaining tasks worked from). W2.6 flipped the terminal assertion,
//     as its own entry asks, and the reviewer's to-do list is now the walk's
//     own output.
//   - TestLiveInitCensusRootsAreTheOnlyOnesThatNameIt asserts the same of the
//     root list itself: every top-level entry that names the command is a live
//     root, so the walk and the rename's task list cover the same set. A
//     census that quietly stopped looking at a directory would otherwise pass
//     by finding less.
//
// The census is a *file* census. A listed file that gains a second mention is
// not a failure, because the rename has to touch that file either way; what
// the list freezes is which files the rename reaches.
//
// A third thing is not an exemption but a boundary: `censusOutputDirs`,
// `censusOutputNames` and `censusOutputMarkers` are the ignored outputs and
// dependency trees both walks step over, because a built page renders a source
// file that the rename reaches -- the site's build directory holds the
// rendered cli-reference page, whose source markdown is live text -- and a
// rendered copy is regenerated, not renamed.
//
// Mentions are matched over flattened text -- line-comment markers dropped,
// whitespace collapsed, the normalisation docs_base_test.go's flattenDoc
// already uses -- so a mention wrapped across a line break or across a `//`
// counts. W2.6's claim that the live tree names the retired command nowhere is
// only worth making if the scan can see the wrapped spellings it is claiming
// about, which is why the flip also holds the scan to the mentions the
// exemption classes keep: an empty answer from a pattern that stopped matching
// would otherwise read as a clean tree.

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// datedRecords is D9's line: the dated roots the rename never enters, stated
// once, here, and referenced by every task that renames (Global Constraint 7).
// Each entry is a path prefix relative to the repository root.
// `docs/releases/v` is a file prefix, so the releases *index* beside the
// records (`docs/releases/README.md`) stays live while `docs/releases/v0.9.0.md`
// does not. The line is the one `plugin/readme_test.go`'s
// TestReadmeNamesNoRetiredSkill draws in spirit: a record of what was decided
// or of what shipped is not rewritten to say what the tree says now.
//
// **W2.6 grew this list to the ten the plan names, and then by three more of
// the same class, and both moves are rulings it had to make.** The plan's ten
// (`docs/plans/`, `docs/releases/v`, `docs/design/`,
// `docs/credentials-service/`, `docs/website/`, `docs/coordinator/`,
// `docs/in-job-review/`, `docs/mobile/`, `docs/windows-cli/` and
// `docs/setup-wizard/`) is what D9 answered on 2026-10-20 and what W2.3's own
// entry states; W0.2 had written six of them, and the four the plan names
// past those six sat on `pendingRename` -- which the walk reads as files still
// owing a rename --
// only because the constant and the answer disagreed. The plan is explicit
// about which half is wrong when that happens: "if the flip prints a file
// inside one of them, the constant is wrong -- not the file". W2.3 could not
// settle it (this file is not in its list) and left the residue visible; this
// file is W2.6's, so W2.6 is where the two halves were made to agree.
//
// The three beyond the ten are `docs/central-orchestrator/`,
// `docs/mobile-auth/` and `docs/rigor-modes/`, and they are the same class as
// `docs/windows-cli/`, which the plan added past its own census on exactly
// this reasoning: each is another artifact's own record -- README, spec,
// implementation-plan and reconciliation for work planned on its own date --
// so renaming inside one edits a record rather than fixing a document, which
// is what D9 forbids. They postdate `82d366af`, the commit this plan was
// checked against, so no census read there could see them, and the plan's
// "no more and no fewer" is a claim about the roots it knew rather than a
// licence to rewrite another artifact's plan. All three name the command, so
// this is not the direction Global Constraint 7 forbids: an exemption for a
// root with no occurrences at all (which is why `docs/website/redesign/` is
// deliberately absent) would be.
var datedRecords = []string{
	// The plans: what was decided, in the words of the day.
	"docs/plans/",
	// The release notes: what shipped, including the releases that shipped
	// `fugaro init` to their readers.
	"docs/releases/v",
	// The designs describing shipped state.
	"docs/design/",
	// Another artifact's plan record, with its own decisions and its own
	// numbers.
	"docs/credentials-service/",
	// The website's plan record -- three mentions between its
	// implementation-plan.md and its reconciliation.md. Its `redesign/` folder
	// names the command zero times, which is why the folder holding the plan is
	// the thing to exempt and not the folder that happens to be newest.
	"docs/website/",
	// The three D9 added on 2026-10-20, each another artifact's
	// reconciliation.md (the mobile one its implementation-plan.md too): the
	// coordinator's, the in-job review's and the phone's. W2.3's entry names
	// them as dated records the rename does not enter.
	"docs/coordinator/",
	"docs/in-job-review/",
	"docs/mobile/",
	// The Windows CLI's record, added past the plan's census because the
	// review found the folder; it postdates `82d366af` like the three below.
	"docs/windows-cli/",
	// This folder: spec.md is the owner's brief verbatim, and the plan and the
	// reconciliation cannot describe a retirement without naming what is
	// retired.
	"docs/setup-wizard/",
	// The three records that arrived after the plan's census was taken, and the
	// ruling W2.6 made on them -- see the paragraph above.
	"docs/central-orchestrator/",
	"docs/mobile-auth/",
	"docs/rigor-modes/",
}

// retirementText is the second exemption class, and it is not a judgement
// call: each of these paths names `fugaro init` *as the retired verb* -- or
// because it pins a record that keeps the name -- which is its whole job and
// which no rename can remove. It is a list of paths that MAY name the command,
// so a path that does not exist yet contributes nothing. W2.6 asserts that the
// files naming the command are exactly `datedRecords` plus these.
//
// The entries are named here rather than counted, because the list is appended
// to and an ordinal goes stale the moment it is: a sentence like "the third is
// this file" is true only for as long as nothing is inserted above it. In the
// order the slice holds them: internal/cli/retired.go and
// internal/cli/retired_test.go, W1.2's tombstone and the test that asserts its
// message; internal/cli/docs_live_init_census_test.go, this file, whose scan
// pattern is the string; and internal/agent/relay_test.go, which is here for
// the tombstone's reason -- it carries one case whose whole subject is that the
// retired word is no longer a subcommand the run summary reports
// (`TestRelayOnToolNeverRepeatsTheRawCommand`, "the retired init word is not a
// subcommand any more"), and its input has to be the retired command line, so
// no rename can take the mention out and renaming it would delete the
// assertion. The plan counted this class as three because it was written before
// W1.2 existed and before that case was added; the class's rule, not its old
// count, is what decides, and this file fits the rule.
//
// W2.6 added the last four, and each is here by that same rule: the mention is
// what the file is *for*, so renaming it out would delete an assertion rather
// than finish a rename. Two are must-not-contain guards whose whole subject is
// the retired word -- internal/cli/docs_setup_wizard_checks_test.go holds
// Check 27's rewritten Restore to not naming it, and internal/cli/restore_test.go
// holds `fugaro restore`'s printed child command line to not naming it; the
// pattern each is built from IS the string, exactly as this file's own scan
// pattern is. Two more pin dated records that D9 keeps: internal/cli/docs_release_notes_test.go
// is built around the 0.9.0 notes' own spelling (its comment: "the notes' own
// spelling, which is frozen with them"), and internal/cli/docs_compute_backend_test.go
// quotes the 0.8.6 notes and design/compute-backend.md in the same way. A
// rename cannot reach their mentions -- the record keeps the name, so the pin
// that reads it must -- and a test that spelled the command around the census
// (`"fugaro " + "init"`) would be hiding the mention rather than retiring the
// word, which is the "wrong in one direction" half of Global Constraint 7.
//
// internal/cli/testdata/init_retire.golden is deliberately NOT here, and it
// names the command zero times today, which is the point: it named it four
// times before the rename reached it. It is a transcript, not a retirement
// statement, and three of the four were internal/cli's own strings -- W2.1's --
// so W2.1 regenerated it with TestInitRepoRetireTranscriptGolden
// -update-retire exactly like the other two goldens; the fourth was
// internal/localcfg's build.service_account warning, which W2.2 renamed in the
// commit that renamed the string it quotes. An exemption for a file the rename
// empties is the "wrong in one direction" case Global Constraint 7 forbids.
var retirementText = []string{
	// W1.2's tombstone: one message, two names, non-zero exit.
	"internal/cli/retired.go",
	// W1.2's test, which asserts that message.
	"internal/cli/retired_test.go",
	// This file: the census is a list of paths, but the pattern below is the
	// string itself.
	"internal/cli/docs_live_init_census_test.go",
	// W1.2's relay case, whose input is the retired command line because the
	// assertion is that the word is retired.
	"internal/agent/relay_test.go",
	// W2.6's four, added by the paragraph above: two must-not-contain guards
	// and the two pins of dated records.
	"internal/cli/docs_setup_wizard_checks_test.go",
	"internal/cli/restore_test.go",
	"internal/cli/docs_release_notes_test.go",
	"internal/cli/docs_compute_backend_test.go",
}

// liveRoots is what the census walks. Every top-level entry that names the
// command is here, and every entry that names it zero times is not -- the
// second test below asserts that rather than trusting it. `scripts/` is the
// one entry that is walked and names the command zero times today; it stays
// because the plan walks it, and a root that finds nothing costs one stat.
var liveRoots = []string{
	"README.md",
	"CONTRIBUTING.md",
	// A root file rather than a directory. It was on the walk because it named
	// the command, which the owner's feedback of 2026-10-22 renamed in place;
	// it stays for `scripts/`'s reason -- the plan walks it, and a root that
	// finds nothing costs one stat.
	"SECURITY.md",
	"docs",
	"plugin",
	".claude",
	"website",
	"schemas",
	"scripts",
	"internal",
	"images",
	"deploy",
}

// initNameRE is what counts as naming the command: two spellings. The command
// as prose and as a user-facing string writes it -- `fugaro init` -- and the
// bare `init --<flag>` form, which a comment-only mention used and which
// `fugaro init` does not match: the rerun line the retired command prints is
// `fugaro init --repo . --retire`, the deprecation warnings name `init --repo`
// on its own, and the comments that named the flag that did the thing --
// `init --firebase-project` across internal/infra, internal/localcfg and
// internal/runner, `init --create-project` in internal/localcfg,
// `init --expect-digest` in internal/mirror -- were dozens until this
// branch's rename pass (022f8ad8) gave each one the door it belongs to, which
// is why the walk below finds none of them today. The alternative stays
// because that is the spelling a *new* comment reaches for, and a census that
// read one as clean would enforce nothing exactly where the rename did most of
// its work.
//
// **W2.6 wrote the second alternative as `init --repo`, and that was too
// narrow**: it caught the one flag a live document happened to spell and left
// every other bare mention reading as a clean file. The flag is the variable,
// so the pattern takes the flag as one (`--` and a letter) rather than
// enumerating the retired command's own flags -- which could not be done
// anyway, because the live `fugaro config init` shares `--yes` with it.
//
// It is matched against flattenDoc's output and never against the raw bytes,
// so a wrapped mention is a mention: a help string's line break inside the
// two words, and a comment's `//` marker between them, are both whitespace by
// the time this pattern sees them. The single space in the pattern is the
// flattened separator, not a promise about the source.
var initNameRE = regexp.MustCompile(`fugaro init|init --[a-z]`)

// otherInitRE is the other command in this tree that writes `init --<flag>`,
// and the reason initNameRE can be as wide as it is. It is `fugaro config
// init --yes`, the live subcommand that writes the minimal `fugaro.yaml`
// (internal/cli/config_cmd.go), whose flag is the retired verb's own -- the
// overlap that rules out the flag list above, since a list of the retired
// command's flags would have to carry `--yes` and so would swallow this one.
//
// A file that mentions it *beside* a real mention must not be read as clean,
// so the exclusion is not a second pattern that exempts the file: the match is
// removed from the flattened text and initNameRE reads what is left. `fugaro
// config init --yes` therefore leaves `fugaro`, and a bare `init --repo` in
// the same file is still found.
//
// The list holds what the tree has, not what it might: `git init --bare` is
// the obvious next entry and is deliberately absent, because no live file
// names it (both mentions are dated records), and an exemption for a spelling
// that occurs nowhere is the direction Global Constraint 7 forbids. The day
// one does, the census fails on that file and the answer is one of the two
// this file hands every unexpected mention -- rename it, or add the command
// here with its reason.
var otherInitRE = regexp.MustCompile(`config init --[a-z]`)

// censusRoot is the repository root as this package's tests reach it.
const censusRoot = "../.."

// pendingRename was the census: every file under liveRoots that named the
// command, sorted. **It is empty, and W2.6 emptied it**, which is the state
// the list existed to reach: the two tests above assert both halves -- the
// walk finds nothing, and this list holds nothing -- and `TestCensusOutputAnd
// ExemptArePinned` holds the two exemption classes, which are what is left of
// the line D9 drew.
//
// It is kept, rather than deleted with the last entry, because the empty claim
// is the one the plan names ("`pendingRename` is empty") and because it is the
// anchor a future rename grows: the walk's own assertion already fails on the
// first mention a live file gains, and this list is where the file goes if the
// tree ever has to be renamed again rather than read.
//
// Read on main at `1d325928`, the branch W2.1-W2.5 were built on, it was
// **304** files: 118 non-test Go files, 117 test files, 26 markdown files, 29
// Terraform files, the four `.tftest.hcl` fixtures beside them, the two
// goldens, and eight in none of those groups -- the website's four, the two
// under images/, the published schema and internal/config/example.yaml. Each
// rename task took its own files off in the commit that renamed them, which is
// the rule this list asked of every one of them, and the per-merge history of
// those moves is in this file's git log rather than restated here: W2.1 moved
// it 107 files down (internal/cli's own strings, plus the generated page),
// W2.2 108 (everything outside internal/cli), W2.4 seven (the website's), W2.5
// forty-four (Terraform, the schema, the example config, images/), W2.3 thirty
// (the live prose and the pins that read it). Those five numbers count a file
// once per task that touched it, which is why they sum to more than the census:
// the file lanes above name the files with more than one writer. What that
// leaves is the residue
// the plan's own text calls "the leftover set", and it is the residue W2.6
// ruled on.
//
// **W2.6's own move, and the last one.** Five files were renamed, and every
// one of them is a mention in live text or a pin of live text:
// docs/gcp-live-checklist.md, the live register W0.3's rewrite left with the
// retired spelling in its prose (its Checks 27 and 111 already named the new
// doors); internal/cli/docs_clone_workflows_test.go,
// internal/cli/docs_mobile_checks_test.go and
// internal/cli/docs_project_centric_test.go, whose assertions quote that
// page's sections and so move with it (Convention 1); and
// internal/cli/doctor_bucket_test.go, whose one mention is a comment about a
// repository re-applied with the repository door.
//
// Fifteen files were ruled dated records rather than renamed: nine under the
// four folders the plan exempts past W0.2's six -- docs/coordinator/,
// docs/in-job-review/ and docs/mobile/, which D9's answer of 2026-10-20 added,
// and docs/windows-cli/, which postdates the plan's census -- and six under the
// three folders of that same class which also postdate it
// (docs/central-orchestrator/, docs/mobile-auth/ and docs/rigor-modes/, each
// another artifact's own plan record). That is why `datedRecords` above carries
// thirteen roots and says so.
//
// Eight name the command by design, and they are `retirementText` above --
// W0.2's three, W1.2's relay case and the four this task added: the tombstone
// and its test, this file, the relay case whose input IS the retired command
// line, the two must-not-contain guards whose pattern IS the retired word
// (internal/cli/docs_setup_wizard_checks_test.go and
// internal/cli/restore_test.go), and the two pins of dated records
// (internal/cli/docs_release_notes_test.go and
// internal/cli/docs_compute_backend_test.go) whose quoted spelling is frozen
// with the record they read.
//
// The plan's "anything else the red step prints is a file a rename task missed
// and belongs to that task, not to this one" was written for a residue of one,
// docs/gcp-live-checklist.md; the twenty-three it did not foresee are the
// rulings above, each of which the census file is the only place that can
// make, and none of which is a rename W2.1-W2.5 owed.
var pendingRename = []string{}

// censusExempt reports whether a repository-relative path is one of the two
// exemption classes: under a dated root, or a retirement statement.
func censusExempt(rel string) bool {
	for _, prefix := range datedRecords {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return slices.Contains(retirementText, rel)
}

// censusOutputDirs, censusOutputNames and censusOutputMarkers are the third
// thing the census steps over, and they are not an exemption: these are the
// build outputs and dependency trees this repository's own .gitignore files
// declare, so they are not source text and no rename reaches them. They matter
// because they are *rendered* copies: `npm run build` writes the site's build
// directory from website/src/ and website/content/, so a built cli-reference
// page carries the `fugaro init` its source markdown carries -- and both of
// those sources are on `pendingRename`. A census that read the build output
// would fail a developer who ran the build the README documents, with a remedy
// nobody can carry out: a rendered page is regenerated, not renamed, and an
// installed dependency tree is not this repository's text at all.
//
// The three lists are the three shapes those .gitignore files write, and
// between them they cover every line of every .gitignore either walk can
// arrive at -- TestCensusOutputCoversEveryIgnoredPathTheWalksReach reads those
// files and holds them to it, so a new ignored output cannot appear without
// being classified here:
//
//   - censusOutputDirs: a tree a .gitignore names in full, matched as a path
//     prefix. websiteBuildDir ("website/dist", docs_website_boundary_test.go)
//     is the constant this package already keeps for the first of them, and the
//     reason is that file's own: a built copy is a copy the build makes, not a
//     second source of the fact.
//   - censusOutputNames: a name that is ignored wherever it appears, because
//     six .gitignore files here declare a node_modules/ -- the root's
//     dependency trees are not the only ones a checkout can grow -- and
//     terraform's own provider tree is terraform's wherever a local init wrote
//     it.
//   - censusOutputMarkers: a file whose *name* carries a marker, which is how
//     the .gitignore files write their file patterns: a swap file, a terraform
//     state and the `.tfstate.*` backup beside it, a log, a screenshot. Each is
//     a copy of something else's text -- the file an editor had open, the
//     resource values a local apply recorded, the run that wrote the log -- so
//     it can carry a mention the census must not read as repository text.
//
// One class of ignored path is deliberately not here. The binaries a build
// leaves in the tree -- /fugaro, /clidoc, and the `*.exe` a
// `GOOS=windows go build` writes into the directory it ran in -- are files, and
// censusNamesInit's binary sniff is what steps over them, which keeps the rule
// in one place: these lists are about what a build or a local run *writes*,
// that sniff is about what a compiler *links*.
var censusOutputDirs = []string{
	// website/.gitignore
	websiteBuildDir,
	"website/.astro",
	// The console's own .gitignore, spelled through consoleDirName rather than
	// as one literal: the boundary's mention rule (docs_console_boundary_test.go)
	// counts that directory's name followed by a slash anywhere in a Go file,
	// comments included, and this file names the directory to step over it
	// rather than to reach into it. consoleDirName is the package's one
	// spelling of it, which is the rule that file states for itself.
	consoleDirName + "/dist",
	consoleDirName + "/release",
	consoleDirName + "/test-results",
	consoleDirName + "/playwright-report",
	consoleDirName + "/.record-fixture",
	consoleDirName + "/.bundle-renderer",
	// The screenshot tool's two trees, which the console's .gitignore declares
	// beside the ones above: `.screenshots/` is the compiled tool `npm run
	// screenshots` runs (`tsconfig.screenshots.json`), and `/screenshots/` is
	// what that tool leaves -- the PNGs the website's Console page is
	// illustrated from, and the MANIFEST.json beside them. Both are a machine's
	// and a day's output; the fixtures the tool *reads* are test/fixtures/
	// screenshots/, which the pattern is anchored away from and which stays
	// repository text.
	consoleDirName + "/.screenshots",
	consoleDirName + "/screenshots",
	// mobile/.gitignore
	"mobile/dist",
	"mobile/web-build",
	"mobile/.expo",
	// testdata/fixture-repo/.gitignore: a fixture repository's own build
	// directory, which the second walk reaches because testdata/ is not a live
	// root.
	"testdata/fixture-repo/build",
	// .gitignore: the root's own build output, and the two trees it keeps for
	// the owner's local state -- the Firebase CLI's cache, and the folder
	// signing material lives in.
	"dist",
	".firebase",
	"secrets",
}

// censusOutputNames are ignored by name at any depth.
var censusOutputNames = []string{
	// website/.gitignore, the console's, mobile/.gitignore,
	// deploy/sandbox/.gitignore, internal/budget/rules/emulator/.gitignore and
	// testdata/fixture-repo/.gitignore all declare it, and the reason is the
	// same under each: `npm ci` writes a tree nobody reviews from a lockfile
	// that already pins it.
	"node_modules",
	// .gitignore's /deploy/terraform/**/.terraform/: terraform's provider tree,
	// ignored at whatever depth the module that ran init sits. The match is the
	// whole segment, so the committed .terraform.lock.hcl beside it stays
	// repository text.
	".terraform",
	// deploy/sandbox/.gitignore's junit.xml: a test runner's report, which
	// records the names of the tests it ran -- the console's test-results/
	// writes one too.
	"junit.xml",
	// .gitignore's /THIRD_PARTY_NOTICES.md: the licence and notice texts of the
	// Go modules the released binary links, which scripts/third-party-notices.sh
	// generates from the module cache when GoReleaser's before hooks run. That
	// pattern is anchored at the root, the one place the script writes; the name
	// is the shape these lists match, and the text is those modules' rather than
	// this repository's, which is what they step over.
	"THIRD_PARTY_NOTICES.md",
}

// censusOutputMarkers are matched against a file's base name, because the
// .gitignore patterns behind them are an ending, a beginning, or both sides of
// a stem, and a marker in the name covers all three without a fourth shape.
var censusOutputMarkers = []string{
	// .gitignore's /deploy/terraform/**/*.tfstate and *.tfstate.*: a local
	// terraform run's state, which records the resource values that were
	// applied, at a path the `**/` puts wherever the module that wrote it sits.
	".tfstate",
	// .gitignore's *.swp: the editor's copy of the file being edited, which
	// carries that file's text -- a live file's mentions included.
	".swp",
	// .gitignore's /docs/website/redesign/{current,after}/*.jpg: the redesign's
	// screenshots, which docs/website/redesign/capture-screenshots.mjs
	// regenerates.
	".jpg",
	// mobile/.gitignore's *.log and .metro-health-check*: the app's own log, and
	// the health-check file a Metro run leaves at the app's root.
	".log",
	".metro-health-check",
}

// censusOutput reports whether a repository-relative path is one of the ignored
// outputs above: a tree on censusOutputDirs (the directory itself, or anything
// under it), a path with a censusOutputNames segment, or a file whose name
// carries a censusOutputMarkers marker.
func censusOutput(rel string) bool {
	if rel == "" {
		return false
	}
	for _, dir := range censusOutputDirs {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	segs := strings.Split(rel, "/")
	for _, seg := range segs {
		if slices.Contains(censusOutputNames, seg) {
			return true
		}
	}
	base := segs[len(segs)-1]
	for _, marker := range censusOutputMarkers {
		if strings.Contains(base, marker) {
			return true
		}
	}
	return false
}

// censusVendoredRoots are the root-level trees of vendored third-party agent
// skills, which the census walks do not open: samber/cc-skills-golang, added
// 2026-10-23 (skills-lock.json pins each skill's source and hash), in the
// canonical install location the skills tooling writes, `.agents/`; the skills
// Claude Code loads are symlinks into it under `.claude/skills/`. A second copy
// the tooling wrote for another agent layout (`agent/`) was dropped the same
// day. They are not Fugaro's text: their prose names other tools' `init --<flag>` (swag's,
// in golang-swagger), and one ships a sample .gitignore for a generic Go
// project. Neither is a rename Fugaro owes nor an output a Fugaro build writes;
// the remedy for a mention there is re-vendoring, not editing.
//
// The match is ROOT-LEVEL and exact: internal/agent is a Fugaro package and
// stays walked, and so does any sibling such as `agents/` or `agentx/`.
// TestCensusVendoredIsRootLevelAndExact pins that.
var censusVendoredRoots = []string{".agents"}

// censusVendored reports whether a repository-relative path is one of
// censusVendoredRoots or under one of them.
func censusVendored(rel string) bool {
	for _, root := range censusVendoredRoots {
		if rel == root || strings.HasPrefix(rel, root+"/") {
			return true
		}
	}
	return false
}

// TestCensusVendoredIsRootLevelAndExact holds the vendored skip to the one
// root-level tree it names, so it cannot widen into Fugaro's own code.
//
// Mutation (run, restore): match with strings.Contains -- the internal/agent
// cases fail; drop the "/" boundary -- the agentx case fails.
func TestCensusVendoredIsRootLevelAndExact(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{".agents", true},
		{".agents/x", true},
		{".agents/skills/golang-swagger/references/swag-cli.md", true},
		// The second install location was dropped: a root `agent/` is walked.
		{"agent", false},
		{"agent/skills/golang-project-layout/assets/.gitignore", false},
		// Fugaro's own package of that name, and a file in it the census
		// exemptions name, stay walked.
		{"internal/agent", false},
		{"internal/agent/relay.go", false},
		{"internal/agent/relay_test.go", false},
		{"agents/x", false},
		{"agentx/x", false},
		{".agentsx/x", false},
		{"docs/agent/x.md", false},
		{"", false},
	} {
		if got := censusVendored(tc.path); got != tc.want {
			t.Errorf("censusVendored(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
	// The package the skip must not reach is really there and really walked:
	// internal/ is a live root, so liveInitCensus opens internal/agent.
	for _, r := range liveRoots {
		if censusVendored(r) {
			t.Errorf("live root %s is also a vendored tree: the skip would hide a root the census claims to walk", r)
		}
	}
	if !slices.Contains(liveRoots, "internal") {
		t.Fatal("internal is not a live root: internal/agent would not be walked")
	}
	if info, err := os.Stat(filepath.Join(censusRoot, "internal", "agent")); err != nil || !info.IsDir() {
		t.Fatalf("internal/agent is not a directory (%v): this test's negative cases name a package that is gone", err)
	}
}

// censusBinarySniff is how much of a file censusNamesInit reads before it
// decides whether the file is text. Eight KiB is enough for the two Windows
// binaries at this repository's root, whose NUL bytes start at byte 4.
const censusBinarySniff = 8192

// censusNamesInit reports whether the file at path names the command. The
// bytes are flattened before they are matched -- line-comment markers dropped,
// whitespace collapsed -- because prose and Go comments wrap, and a census
// that only saw the unwrapped spelling would miss the mention the moment
// somebody re-wrapped the sentence. Four files were the census's own examples
// of that hazard, each naming the command in one of those spellings and nothing
// else:
// the `fugaro managed` Long help string wrapped it across a line break
// (`internal/cli/managed_apply.go`), and three comments wrapped it across a
// `//` marker (`internal/cli/project_job.go`,
// `internal/gcpfake/firebasemgmt.go`,
// `internal/infra/managed/untrusted_test.go`). The rename has since reached all
// four, so they stand here as the hazard's shape and not as files that still
// carry a mention -- which is what a later re-wrap of any sentence would make
// again. flattenDoc is the same
// normalisation docs_base_test.go uses for this exact hazard, reused rather
// than reimplemented so the two cannot disagree.
//
// A file whose first 8 KiB hold a NUL byte is binary and is skipped: the tree
// carries two Windows binaries at its root, and a byte pattern inside a
// compiled artifact is not a string anybody renames. The head is read first
// and the rest only when the head says the file is text, because the walk also
// reaches the binaries a build leaves in the root -- .gitignore's own note puts
// `clidoc` at 74 MB -- and reading one of those whole to learn what its first
// eight kilobytes already say is the memory the sniff exists to save. A NUL
// past the sniff is not consulted, so a file is text or binary by its head
// alone, exactly as it was before.
func censusNamesInit(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	defer f.Close()
	head := make([]byte, censusBinarySniff)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		t.Fatalf("reading %s: %v", path, err)
	}
	head = head[:n]
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	rest, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	flat := flattenDoc(string(append(head, rest...)))
	return initNameRE.MatchString(otherInitRE.ReplaceAllString(flat, " "))
}

// liveInitCensus walks liveRoots and returns, sorted, every file that names
// the command, minus the two exemption classes (censusExempt) and the ignored
// build outputs (censusOutput). A root that is not there is fatal rather than
// skipped, and so is a read error: a census that shortened itself would pass
// the assertions below by finding less.
func liveInitCensus(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	for _, r := range liveRoots {
		p := filepath.Join(root, filepath.FromSlash(r))
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("live root %s: %v -- the census is only as wide as its roots, so a missing one silently shortens it", r, err)
		}
		if !info.IsDir() {
			if censusNamesInit(t, p) {
				found = append(found, r)
			}
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// A symlink is not text of its own: `.claude/skills/<name>` points into the
			// vendored `.agents/skills/<name>`, which is where its target is judged.
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			// A build output is not descended into at all: the point of the
			// rule is that nothing under it is repository text, and stepping
			// over the tree is also what keeps the walk cheap when one is there.
			if censusOutput(rel) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() || censusExempt(rel) {
				return nil
			}
			if censusNamesInit(t, path) {
				found = append(found, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", r, err)
		}
	}
	slices.Sort(found)
	return found
}

// TestCensusNamesInitReadsPastTheHead pins both halves of censusNamesInit's
// read, on files it writes itself, so the sniff's two rules are held where they
// are written rather than only through the tree:
//
//   - a file whose head is text is read to its end, so a mention past the sniff
//     is still a mention (the walk must not stop looking at 8 KiB), and
//   - a file whose head holds a NUL is binary and the rest is not read at all
//     (the sniff's whole purpose: the walk reaches the binaries a build leaves
//     in the root, and `clidoc` is 74 MB).
//
// The third case is the boundary itself: a NUL past the sniff does not make a
// file binary, because a file is text or binary by its head alone.
//
// Mutation (run, restore): drop the `io.ReadAll` and match the head alone --
// the past-the-head case fails; move the NUL check after a whole-file read --
// the third case fails.
func TestCensusNamesInitReadsPastTheHead(t *testing.T) {
	pad := strings.Repeat("a ", censusBinarySniff/2)
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"mention in the head", "rerun: fugaro init\n", true},
		{"mention past the head", pad + "\nrerun: fugaro init\n", true},
		{"wrapped mention past the head", pad + "\n// rerun: fugaro\n// init\n", true},
		{"mention past the head, init --repo", pad + "\ninit --repo .\n", true},
		{"nothing to find, past the head", pad + "\nnothing here\n", false},
		{"binary head", "MZ\x00\x00fugaro init\n", false},
		{"NUL past the head", pad + "\x00fugaro init\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "candidate.txt")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := censusNamesInit(t, path); got != tc.want {
				t.Errorf("censusNamesInit(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestCensusNamesInitMatchesBothSpellingsAndTheOtherCommand pins the pattern
// itself, on files written out here for the reason
// TestCensusNamesInitReadsPastTheHead exists: the tree cannot hold it. A live
// tree that names the retired command nowhere -- which is the state this file
// asserts -- says nothing about whether the pattern would have seen it, so
// every case below is a spelling the walk has to be able to answer for, and a
// pattern that stopped matching one of them would leave the census reading a
// clean tree it never looked at.
//
// Three halves, and they fail apart:
//
//   - the two spellings are caught, and the bare one is caught with *any*
//     flag, not the one flag a document happens to spell today. W2.6 wrote the
//     second alternative as `init --repo`, which passed a tree that named
//     `init --firebase-project`, `init --create-project`, `init --print-vars`
//     and sixteen more files' worth of the same thing;
//   - the bare spelling is caught *wrapped*, because a comment wraps it across
//     its `//` marker (init_firebase_test.go, init_images_test.go,
//     init_review.go and mirror/ref.go all do), and
//   - `fugaro config init --yes` is not the retired command, while a file that
//     names it beside a real mention still is: the exclusion is a redaction of
//     the flattened text, not an exemption for the file.
//
// Mutation (run, restore): narrow initNameRE's second alternative back to
// `init --repo` -- the bare-flag cases fail; drop the `--[a-z]` and match a
// bare `init ` -- the "other command alone" case matches and fails; drop
// otherInitRE's redaction and exempt the whole file instead -- the "beside a
// real mention" case fails.
func TestCensusNamesInitMatchesBothSpellingsAndTheOtherCommand(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"the command as prose", "rerun fugaro init in the checkout\n", true},
		{"the command with a flag", "fugaro init --repo . --retire\n", true},
		{"a bare flag, the one W2.6 spelled", "rerun `init --repo` in the checkout\n", true},
		{"a bare flag, firebase-project", "init --firebase-project checks them first\n", true},
		{"a bare flag, create-project", "the project was created by this run (init --create-project)\n", true},
		{"a bare flag, print-vars", "printVarsUngatedInstallation is init --print-vars's warning\n", true},
		{"a bare flag, wrapped across a // marker", "// and a marker above this\n// binary says so. init\n// --firebase-project deploys the rules\n", true},
		{"a bare flag, expect-digest", "Plan's expect digest (init --expect-digest) pins one\n", true},
		{"the other command alone", "fugaro config init --yes writes the minimal fugaro.yaml\n", false},
		{"the other command, with the retired one beside it", "fugaro config init --yes writes it; the old text said init --repo\n", true},
		{"neither spelling", "fugaro setup --repo and fugaro update --firebase-project\n", false},
		{"a flag with no init in front", "fugaro setup --create-project\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "candidate.txt")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := censusNamesInit(t, path); got != tc.want {
				t.Errorf("censusNamesInit(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestCensusOutputAndExemptArePinned holds the two rules both walks ask before
// they object, on paths written out here rather than through the tree -- for the
// reason TestCensusNamesInitReadsPastTheHead exists. Nothing in a clean checkout
// *is* an ignored output or a dated record, so the tree cannot hold either rule:
// every test above passes with censusOutput stubbed to `return false` and with
// datedRecords emptied, and that is the state the census exists to make
// impossible. The first walk would then report a built page as a file to rename,
// and the second would hand a dated record the two remedies it hands a file it
// cannot place -- rename it, or add its directory to liveRoots, which would make
// the first walk demand the rename.
//
// The cases hold the three shapes apart on purpose. A prefix is a path boundary,
// not a string: website/dist-notes.md and distx/ are repository text. A name is a
// whole segment: the committed .terraform.lock.hcl is not the ignored provider
// tree beside it. And the two rules answer different questions, so neither may
// absorb the other: a dated record is not an output, and an output is not exempt.
//
// The last check is the one a widened rule can break: no entry of `pendingRename`
// may be an ignored output. A rule broad enough to swallow a frozen file would
// leave the census reading less than it lists, which is the one failure this
// file's two tests cannot see for themselves.
//
// Mutation (run, restore): stub censusOutput to `return false` -- every case that
// wants true fails; drop websiteBuildDir from censusOutputDirs -- its two cases
// fail; match the directory prefix with strings.Contains instead of a boundary --
// the sibling cases fail; drop "docs/plans/" from datedRecords -- the dated-root
// cases fail; make censusExempt a prefix match over retirementText -- the
// retired.go.md case fails.
func TestCensusOutputAndExemptArePinned(t *testing.T) {
	output := []struct {
		path string
		want bool
	}{
		// Every entry of censusOutputDirs: the tree itself, and a file under it.
		{"website/dist", true},
		{"website/dist/index.html", true},
		{"website/.astro/types.d.ts", true},
		{consoleDirName + "/dist/renderer/index.html", true},
		{consoleDirName + "/release/fugaro-console.AppImage", true},
		{consoleDirName + "/test-results/results.xml", true},
		{consoleDirName + "/playwright-report/index.html", true},
		{consoleDirName + "/.record-fixture/fixture.json", true},
		{consoleDirName + "/.bundle-renderer/bundle.js", true},
		{"mobile/dist/index.html", true},
		{"mobile/web-build/index.html", true},
		{"mobile/.expo/cache/state.json", true},
		{"testdata/fixture-repo/build/out.txt", true},
		{"dist/fugaro", true},
		{".firebase/hosting.json", true},
		{"secrets/signing.json", true},
		// Every name on censusOutputNames, wherever it appears.
		{"website/node_modules/react/index.js", true},
		{consoleDirName + "/node_modules/electron/index.js", true},
		{"deploy/sandbox/node_modules/tfjs/index.js", true},
		{"internal/budget/rules/emulator/node_modules/x/index.js", true},
		{"testdata/fixture-repo/node_modules/x/index.js", true},
		{"deploy/terraform/prod/.terraform/providers/x/terraform-provider-google", true},
		{"deploy/sandbox/junit.xml", true},
		// Every marker on censusOutputMarkers.
		{"deploy/terraform/prod/terraform.tfstate", true},
		{"deploy/terraform/prod/terraform.tfstate.backup", true},
		{"internal/cli/.init.go.swp", true},
		{"mobile/app.log", true},
		{"mobile/.metro-health-check.json", true},
		{"docs/website/redesign/after/home.jpg", true},
		{"docs/website/redesign/current/home.jpg", true},
		// A prefix is a path boundary, not a string; a name is a whole segment.
		{"website/dist-notes.md", false},
		{"website/distx/index.html", false},
		{"distx/fugaro", false},
		{"secrets.md", false},
		{"deploy/terraform/prod/.terraform.lock.hcl", false},
		// Repository text, and the trees the walks do descend.
		{"website/src/pages/getting-started.astro", false},
		{"website/content/cli-reference.md", false},
		{"internal/cli/init.go", false},
		{"mobile/README.md", false},
		{"testdata/fixture-repo/README.md", false},
		{"images/history/Dockerfile", false},
		// An exemption is not an output, and an output is not an exemption.
		{"docs/plans/setup-wizard.md", false},
		{"", false},
	}
	for _, tc := range output {
		if got := censusOutput(tc.path); got != tc.want {
			t.Errorf("censusOutput(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}

	exempt := []struct {
		path string
		want bool
	}{
		// Every dated root, and every path in retirementText -- named rather
		// than counted, for the reason that list's own comment gives: it is
		// appended to. The plan's ten, the three W2.6 ruled on beside them, the
		// tombstone and its test, this file, the relay case whose input is the
		// retired command line, and W2.6's two guards and two dated-record pins.
		{"docs/plans/setup-wizard/implementation-plan.md", true},
		{"docs/releases/v0.9.0.md", true},
		{"docs/design/x.md", true},
		{"docs/credentials-service/x.md", true},
		{"docs/website/x.md", true},
		{"docs/coordinator/reconciliation.md", true},
		{"docs/in-job-review/reconciliation.md", true},
		{"docs/mobile/reconciliation.md", true},
		{"docs/windows-cli/reconciliation.md", true},
		{"docs/setup-wizard/implementation-plan.md", true},
		{"docs/central-orchestrator/reconciliation.md", true},
		{"docs/mobile-auth/reconciliation.md", true},
		{"docs/rigor-modes/reconciliation.md", true},
		{"internal/cli/retired.go", true},
		{"internal/cli/retired_test.go", true},
		{"internal/cli/docs_live_init_census_test.go", true},
		{"internal/agent/relay_test.go", true},
		{"internal/cli/docs_setup_wizard_checks_test.go", true},
		{"internal/cli/restore_test.go", true},
		{"internal/cli/docs_release_notes_test.go", true},
		{"internal/cli/docs_compute_backend_test.go", true},
		// The releases index beside the records stays live, and a prefix that
		// stops at a slash is not a prefix that stops mid-word.
		{"docs/releases/README.md", false},
		{"docs/plansx/x.md", false},
		{"docs/checkout.md", false},
		{"internal/cli/docs_checkout_test.go", false},
		// A dated root is a prefix and the retirement class is a list of paths,
		// so neither absorbs the sibling of a folder or of a file it names.
		{"docs/mobilex/reconciliation.md", false},
		{"docs/central-orchestratorx/README.md", false},
		{"internal/cli/retired.go.md", false},
		{"", false},
	}
	for _, tc := range exempt {
		if got := censusExempt(tc.path); got != tc.want {
			t.Errorf("censusExempt(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}

	// The lists themselves: a clean repository-relative path each, so the prefix
	// match reads what its .gitignore reads, and no entry twice.
	var all []string
	all = append(all, censusOutputDirs...)
	all = append(all, censusOutputNames...)
	all = append(all, censusOutputMarkers...)
	for _, e := range all {
		if e == "" || strings.HasPrefix(e, "/") || strings.HasSuffix(e, "/") || strings.HasPrefix(e, "./") || strings.Contains(e, "//") {
			t.Errorf("the census output lists carry %q, which is not a clean repository-relative path: an absolute, empty or trailing-slash entry matches nothing the walk hands it", e)
		}
	}
	if dup := duplicatePaths(all); len(dup) > 0 {
		t.Errorf("the census output lists name %s twice: one entry per ignored thing, so taking one off is a one-line diff to review", strings.Join(dup, ", "))
	}

	// The two halves of the line must not overlap.
	for _, p := range pendingRename {
		if censusOutput(p) {
			t.Errorf("pendingRename lists %s, which censusOutput steps over: a rule wide enough to swallow a frozen file would leave the census reading less than it lists, and nothing else here would say so", p)
		}
	}
}

// censusIgnored is every line of every .gitignore a walk can arrive at, each with
// a repository-relative path that line ignores. It is the provenance of the three
// lists above, written down where a reader can check it: those lists are not a
// summary of what a build happens to write, they are these lines.
//
// bySniff marks the two root binaries, which are the one class of ignored path
// the three lists do not cover: censusNamesInit's binary sniff steps over them
// instead, and TestCensusNamesInitReadsPastTheHead's "binary head" case is that
// rule.
var censusIgnored = []struct {
	ignore  string // the .gitignore file, repository-relative
	pattern string // the line in it
	sample  string // a path that line ignores
	bySniff bool   // covered by the binary sniff rather than by censusOutput
}{
	{".gitignore", "/dist/", "dist/fugaro", false},
	{".gitignore", "/THIRD_PARTY_NOTICES.md", "THIRD_PARTY_NOTICES.md", false},
	{".gitignore", "/fugaro", "fugaro", true},
	{".gitignore", "/clidoc", "clidoc", true},
	{".gitignore", "*.exe", "fugaro.exe", true},
	{".gitignore", "/deploy/terraform/**/.terraform/", "deploy/terraform/prod/.terraform/providers/registry.terraform.io/hashicorp/google/6.0.0/linux_amd64/terraform-provider-google", false},
	{".gitignore", "/deploy/terraform/**/*.tfstate", "deploy/terraform/prod/terraform.tfstate", false},
	{".gitignore", "/deploy/terraform/**/*.tfstate.*", "deploy/terraform/prod/terraform.tfstate.backup", false},
	{".gitignore", "*.swp", "internal/cli/.init.go.swp", false},
	{".gitignore", "/docs/website/redesign/current/*.jpg", "docs/website/redesign/current/home.jpg", false},
	{".gitignore", "/docs/website/redesign/after/*.jpg", "docs/website/redesign/after/home.jpg", false},
	{".gitignore", "/.firebase/", ".firebase/hosting.json", false},
	{".gitignore", "/secrets/", "secrets/owner.json", false},
	{"website/.gitignore", "node_modules/", "website/node_modules/react/index.js", false},
	{"website/.gitignore", "dist/", "website/dist/index.html", false},
	{"website/.gitignore", ".astro/", "website/.astro/types.d.ts", false},
	{consoleDirName + "/.gitignore", "node_modules/", consoleDirName + "/node_modules/electron/index.js", false},
	{consoleDirName + "/.gitignore", "dist/", consoleDirName + "/dist/renderer/index.html", false},
	{consoleDirName + "/.gitignore", "release/", consoleDirName + "/release/fugaro-console.AppImage", false},
	{consoleDirName + "/.gitignore", "test-results/", consoleDirName + "/test-results/results.xml", false},
	{consoleDirName + "/.gitignore", "playwright-report/", consoleDirName + "/playwright-report/index.html", false},
	{consoleDirName + "/.gitignore", ".record-fixture/", consoleDirName + "/.record-fixture/fixture.json", false},
	{consoleDirName + "/.gitignore", ".bundle-renderer/", consoleDirName + "/.bundle-renderer/bundle.js", false},
	{consoleDirName + "/.gitignore", ".screenshots/", consoleDirName + "/.screenshots/screenshots.mjs", false},
	{consoleDirName + "/.gitignore", "/screenshots/", consoleDirName + "/screenshots/MANIFEST.json", false},
	{"mobile/.gitignore", "node_modules/", "mobile/node_modules/expo/index.js", false},
	{"mobile/.gitignore", ".expo/", "mobile/.expo/cache/state.json", false},
	{"mobile/.gitignore", ".metro-health-check*", "mobile/.metro-health-check.json", false},
	{"mobile/.gitignore", "dist/", "mobile/dist/index.html", false},
	{"mobile/.gitignore", "web-build/", "mobile/web-build/index.html", false},
	{"mobile/.gitignore", "*.log", "mobile/app.log", false},
	{"deploy/sandbox/.gitignore", "node_modules/", "deploy/sandbox/node_modules/tfjs/index.js", false},
	{"deploy/sandbox/.gitignore", "junit.xml", "deploy/sandbox/junit.xml", false},
	{"internal/budget/rules/emulator/.gitignore", "node_modules/", "internal/budget/rules/emulator/node_modules/x/index.js", false},
	{"testdata/fixture-repo/.gitignore", "build/", "testdata/fixture-repo/build/out.txt", false},
	{"testdata/fixture-repo/.gitignore", "node_modules/", "testdata/fixture-repo/node_modules/x/index.js", false},
}

// TestCensusOutputCoversEveryIgnoredPathTheWalksReach holds the claim the three
// lists make about themselves -- that between them they cover every ignored
// output the two walks can reach -- to the files that justify it. It reads every
// .gitignore in the tree (a dependency tree's own .gitignore is not one of them:
// it does not govern this repository), requires every line of each to be
// classified in censusIgnored, and then asks censusOutput about the path that
// line ignores.
//
// The claim is prose until this test runs, and it was prose that was false. The
// root .gitignore alone declares eleven things and the list that cited it carried
// five: a local terraform run's state file under deploy/terraform/ -- which
// records the resource values that were applied, a copy of what the .tf files
// beside it say -- or an editor's swap file for a file that names the command
// would have failed the census with a remedy nobody can carry out, which is the
// failure the build outputs were put on the list to prevent. A new ignored output
// is now a failing test rather than a quiet hole.
//
// Mutation (run, restore): drop a line from censusIgnored -- the walk finds that
// line unclassified and fails; drop ".tfstate" from censusOutputMarkers -- the
// state cases fail; drop a .gitignore file from censusIgnored -- the file count
// disagrees with the walk.
func TestCensusOutputCoversEveryIgnoredPathTheWalksReach(t *testing.T) {
	classified := map[string]bool{}
	files := map[string]bool{}
	for _, c := range censusIgnored {
		classified[c.ignore+"\t"+c.pattern] = true
		files[c.ignore] = true
	}
	if len(files) == 0 {
		t.Fatal("censusIgnored classifies no .gitignore at all")
	}

	found := 0
	err := filepath.WalkDir(censusRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// A symlink is not text of its own: `.claude/skills/<name>` points into the
		// vendored `.agents/skills/<name>`, which is where its target is judged.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(censusRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			// A vendored skill tree's .gitignore is a sample for some other
			// project and governs nothing here (censusVendoredRoots).
			if rel == ".git" || censusOutput(rel) || censusVendored(rel) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != ".gitignore" {
			return nil
		}
		found++
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if !classified[rel+"\t"+line] {
				t.Errorf("%s:%d declares %q, and no censusOutput entry classifies it: an ignored output either walk can reach has to be on one of the three lists, or a local run of the tool that writes it fails the census with a remedy nobody can carry out", rel, i+1, line)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found != len(files) {
		t.Errorf("the walk found %d .gitignore files and censusIgnored classifies %d: every one the walks can arrive at has to be classified, or its lines are unchecked", found, len(files))
	}

	for _, c := range censusIgnored {
		if got := censusOutput(c.sample); got != !c.bySniff {
			if c.bySniff {
				t.Errorf("%s ignores %s, and censusOutput claims %s: the root binaries are the binary sniff's business, not these lists'", c.ignore, c.pattern, c.sample)
				continue
			}
			t.Errorf("%s ignores %s (e.g. %s), but censusOutput reads it as repository text: a local run that writes it fails the census with a remedy nobody can carry out", c.ignore, c.pattern, c.sample)
		}
	}
}

// duplicatePaths returns the entries that appear in paths more than once.
func duplicatePaths(paths []string) []string {
	seen := map[string]bool{}
	var dup []string
	for _, p := range paths {
		if seen[p] {
			dup = append(dup, p)
		}
		seen[p] = true
	}
	return dup
}

// TestLiveInitCensusMatchesTheFrozenList is D9's line, both halves of it, as a
// test: the walk over the live roots, and the list of files still owing a
// rename. W2.6 emptied the list, so the assertion today is the retirement
// itself -- the live tree names the command nowhere.
//
// Mutation (run, restore): add a mention of the command to a live file that is
// not exempt -- internal/cli/watch.go is one -- and it fails with that file
// named; put a dated record back on `pendingRename` and it fails on the list
// not being empty; narrow initNameRE so the walk matches nothing and the
// retirementText anchor fails, which is what keeps "found nothing" from being
// indistinguishable from "stopped looking".
func TestLiveInitCensusMatchesTheFrozenList(t *testing.T) {
	found := liveInitCensus(t, censusRoot)

	// The terminal assertion, and the one W2.6 flipped. While the rename was in
	// flight an empty census meant the walk stopped looking -- a root moved, the
	// pattern changed -- and "found nothing" is not the same claim as "there is
	// nothing to find". W2.6, the task that empties `pendingRename`, turned it
	// into the opposite claim: the list is empty because the rename is done, and
	// the walk finds nothing because the live tree names the retired command
	// nowhere. The two are asserted separately, because they fail apart: a
	// rename task that finished a file and left it on the list leaves the walk
	// empty and the list not, and a file renamed out of a *dated record* is the
	// mistake that shows up in the other direction.
	if len(pendingRename) != 0 {
		t.Fatalf("pendingRename still lists %d file(s): %s -- each is a rename the plan owes, and this list is what is left of the rename", len(pendingRename), strings.Join(pendingRename, ", "))
	}
	if len(found) != 0 {
		t.Errorf("the live tree names the retired command in %d file(s): %s -- outside the dated records and the retirement text it must name it nowhere", len(found), strings.Join(found, ", "))
	}

	// An empty census is the one answer this test cannot check for itself: a
	// broken root list and a pattern that matches nothing both report a clean
	// tree. So the scan is held to the mentions its own exemption classes keep:
	// a path in `retirementText` exists to name the command, and the day one of
	// them stops, either the pattern stopped reading (the flattening, the two
	// spellings) or a sentence whose whole subject is the retirement was
	// renamed away. A path that does not exist yet contributes nothing, as that
	// list's own comment says.
	//
	// Mutation (run, restore): narrow initNameRE to `fugaro initty` -- every
	// case below fails, and so does the walk's own silence above.
	for _, p := range retirementText {
		if _, err := os.Stat(filepath.Join(censusRoot, p)); err != nil {
			continue
		}
		if !censusNamesInit(t, filepath.Join(censusRoot, p)) {
			t.Errorf("%s is in retirementText and does not name the command: either the scan stopped seeing it, or a mention whose whole subject is the retirement was renamed away", p)
		}
	}

	if !slices.IsSorted(pendingRename) {
		t.Error("pendingRename is not sorted: keep it sorted, so a task that takes a file off it is a one-line diff to review")
	}
	if dup := duplicatePaths(pendingRename); len(dup) > 0 {
		t.Errorf("pendingRename lists %s more than once: the list is a set, and a duplicate hides a removal", strings.Join(dup, ", "))
	}
	for _, p := range pendingRename {
		if censusExempt(p) {
			t.Errorf("pendingRename lists %s, which is exempt (a dated root or retirement text): an exempt file is renamed by nobody, so listing it promises a rename that cannot happen", p)
		}
	}

	// Both generated pages are off the list, and neither left it by hand:
	// a page leaves the list when its generator runs, and the census is where
	// that is visible. cli-reference.md is rendered from the command tree
	// (today by `go run ./ee/clidoc`; at W2.1's rename it was
	// `./internal/clidoc`), so W2.1's rename reached it and it left;
	// security.md is SECURITY.md byte for byte, so W2.3's rename of the
	// document reached it and the same command regenerated it in that commit.
	// The rows stay as the invariant they assert -- no generated page is ever
	// on this list, because a page is regenerated, not renamed -- which is
	// what W2.4, the task that owned the rest of the site, worked from.
	for _, tc := range []struct {
		page   string
		listed bool
	}{
		{"website/content/cli-reference.md", false},
		{"website/content/security.md", false},
	} {
		if got := slices.Contains(pendingRename, tc.page); got != tc.listed {
			t.Errorf("pendingRename lists the generated %s = %v, want %v: it is written by its generator, so it leaves the list by being regenerated, not edited", tc.page, got, tc.listed)
		}
	}

	// The two sets the list used to be walked against -- what the walk found and
	// did not list, what the list held and the walk did not find -- are both
	// asserted empty above, so the diff between them is asserted empty too and
	// is not recomputed here.
	t.Logf("the live tree names the retired command in %d files; pendingRename lists %d", len(found), len(pendingRename))
}

// TestLiveInitCensusRootsAreTheOnlyOnesThatNameIt: liveRoots is a list, and a
// list can be wrong in the direction that costs the most. A top-level
// directory that names the command and is not walked is a file no rename task
// and no assertion ever sees -- the census would report a clean tree while the
// string sat in a directory it never opened. So the roots are held to the
// claim the plan makes for them: every top-level entry that names the command
// is walked, and every entry that is not walked names it zero times.
//
// This walk asks the same two questions the first one does before it objects,
// and for the same reason: a file that is exempt or under a build output is
// not this test's business either. Without the exemption check the two
// remedies it offers would both be impossible for such a file -- adding it to
// liveRoots would make the first test demand it in `pendingRename`, which is
// to demand renaming a dated record. No dated root or retirement path sits
// outside docs/ or internal/cli/ today, so the hole is latent; it is closed
// because it is the one place where the file's own rule was applied to one
// half of the line and not the other.
//
// Mutation (run, restore): add a line naming the command to a file under a
// directory that is not a root -- mobile/README.md is one -- and this fails.
func TestLiveInitCensusRootsAreTheOnlyOnesThatNameIt(t *testing.T) {
	entries, err := os.ReadDir(censusRoot)
	if err != nil {
		t.Fatal(err)
	}
	read := 0
	for _, e := range entries {
		name := e.Name()
		// censusVendored steps over the vendored third-party skill trees: their
		// text is another project's, so neither remedy below applies to it.
		if name == ".git" || slices.Contains(liveRoots, name) || censusOutput(name) || censusVendored(name) {
			continue
		}
		p := filepath.Join(censusRoot, name)
		if !e.IsDir() {
			read++
			if censusNamesInit(t, p) {
				t.Errorf("%s names `fugaro init` and is not a live root: add it to liveRoots, or rename the mention -- a census that does not look is not a census", name)
			}
			continue
		}
		err := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// A symlink is not text of its own: `.claude/skills/<name>` points into the
			// vendored `.agents/skills/<name>`, which is where its target is judged.
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			rel, err := filepath.Rel(censusRoot, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if censusOutput(rel) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() || censusExempt(rel) {
				return nil
			}
			read++
			if censusNamesInit(t, path) {
				t.Errorf("%s names `fugaro init`, but %s/ is not a live root: add it to liveRoots, or rename the mention -- the walk and the rename's task list have to cover the same set", rel, name)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", name, err)
		}
	}
	if read == 0 {
		t.Fatal("no file outside liveRoots was read: the walk did not run")
	}
	t.Logf("read %d files outside liveRoots; none names the retired command", read)
}
