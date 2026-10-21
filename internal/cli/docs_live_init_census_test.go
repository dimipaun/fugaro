package cli

// The census of every file that still names `fugaro init`, and the line the
// rename must not cross (setup-wizard plan, Task W0.2; reconciliation §4, F20
// and D9; Global Constraints 3 and 7; Convention 11).
//
// `fugaro init` is retired in favour of `fugaro setup` and `fugaro update`
// (D1, D2, D11), and the rename touches some 790 lines across more than three
// hundred files -- far more than one review can hold in its head. The owner's
// line (D9) is what makes it reviewable: rename live text, never rewrite a
// dated record. A line like that is only a line if both of its halves are
// written down, so this file is both of them. `pendingRename` is today's
// census, and `datedRecords` is the six dated roots, stated once, here, and
// referenced by every task that renames rather than restated (Global
// Constraint 7: two copies of the list are two lists that drift).
//
// Two tests read the two halves:
//
//   - TestLiveInitCensusMatchesTheFrozenList walks the live roots, collects
//     every file naming `fugaro init` or `init --repo`, and asserts that set
//     equals `pendingRename`. It fails in both directions, which is what makes
//     it a pin rather than a snapshot: a file that starts naming the command
//     and is not listed fails it (the set grew -- a string nobody has on their
//     map), and a listed file that has stopped naming it fails it (the set
//     shrank -- W2.x renamed it and left it on the map the remaining tasks
//     work from).
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
// file that IS on the list -- the site's build directory holds the rendered
// cli-reference page, whose source markdown is on `pendingRename` -- and a
// rendered copy is regenerated, not renamed.
//
// Mentions are matched over flattened text -- line-comment markers dropped,
// whitespace collapsed, the normalisation docs_base_test.go's flattenDoc
// already uses -- so a mention wrapped across a line break or across a `//`
// counts, and the two files on the list that name the command nowhere else --
// internal/cli/managed_apply.go and internal/cli/project_job.go -- are there
// for that reason. W2.6's claim that the live tree names the retired command
// nowhere is only worth making if the scan can see the wrapped spellings it is
// claiming about, and W2.2's sweep is where that was cashed: the plan gives
// that task's file list from a line-based `grep`, so two of the files it
// renamed -- internal/gcpfake/firebasemgmt.go and
// internal/infra/managed/untrusted_test.go -- are on the list without being on
// that command's output, exactly as the two internal/cli files above are on it
// without being on the 48-file list the plan gives W2.1.
//
// W2.6 -- the task that empties the census -- flips this file's terminal
// assertion from "the walk found something" to "`pendingRename` is empty", and
// that is where the pin finally bites. Nothing before it may relax a row: the
// two exemption classes below exist because the rename cannot be finished
// without them, not to make a red test green.

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

// datedRecords is D9's line: the six dated roots the rename never enters,
// stated once, here, and referenced by every task that renames (Global
// Constraint 7). Each entry is a path prefix relative to the repository root.
// `docs/releases/v` is a file prefix, so the releases *index* beside the
// records (`docs/releases/README.md`) stays live while `docs/releases/v0.9.0.md`
// does not. The line is the one `plugin/readme_test.go`'s
// TestReadmeNamesNoRetiredSkill draws in spirit: a record of what was decided
// or of what shipped is not rewritten to say what the tree says now.
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
	// This folder: spec.md is the owner's brief verbatim, and the plan and the
	// reconciliation cannot describe a retirement without naming what is
	// retired.
	"docs/setup-wizard/",
}

// retirementText is the second exemption class, and it is not a judgement
// call: each of these paths names `fugaro init` *as the retired verb*, which
// is its whole job and which no rename can remove. It is a list of paths that
// MAY name the command, so a path that does not exist yet contributes nothing.
// W2.6 asserts that the files naming the command are exactly `datedRecords`
// plus these four.
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
// assertion. The plan counts this class as three because it was written before
// W1.2 existed and before that case was added; the class's rule, not its old
// count, is what decides, and this file fits the rule.
//
// internal/cli/testdata/init_retire.golden is deliberately NOT here, although
// it named the command four times before W2.2: it is a transcript, not a
// retirement statement, and the three it names today are internal/cli's own
// strings -- W2.1's -- so W1.2/W2.1 regenerate it with
// TestInitRepoRetireTranscriptGolden -update-retire exactly like the other two
// goldens. The fourth was internal/localcfg's build.service_account warning,
// which W2.2 renamed in the commit that renamed the string it quotes. An
// exemption for a file the rename empties is the "wrong in one direction" case
// Global Constraint 7 forbids.
var retirementText = []string{
	// W1.2's tombstone: one message, two names, non-zero exit.
	"internal/cli/retired.go",
	// W1.2's test, which asserts that message.
	"internal/cli/retired_test.go",
	// This file: `pendingRename` is a list of paths, but the pattern below is
	// the string itself.
	"internal/cli/docs_live_init_census_test.go",
	// W1.2's relay case, whose input is the retired command line because the
	// assertion is that the word is retired.
	"internal/agent/relay_test.go",
}

// liveRoots is what the census walks. Every top-level entry that names the
// command is here, and every entry that names it zero times is not -- the
// second test below asserts that rather than trusting it. `scripts/` is the
// one entry that is walked and names the command zero times today; it stays
// because the plan walks it, and a root that finds nothing costs one stat.
var liveRoots = []string{
	"README.md",
	"CONTRIBUTING.md",
	// A root file rather than a directory, and on the walk because it names
	// the command: W2.3 renames it in place.
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
// as prose and as a user-facing string writes it, and the `init --repo` form,
// which a dozen comment-only mentions use and which `fugaro init` does not
// match -- the rerun line the retired command prints is
// `fugaro init --repo . --retire`, and the deprecation warnings name
// `init --repo` on its own.
//
// It is matched against flattenDoc's output and never against the raw bytes,
// so a wrapped mention is a mention: a help string's line break inside the
// two words, and a comment's `//` marker between them, are both whitespace by
// the time this pattern sees them. The single space in the pattern is the
// flattened separator, not a promise about the source.
var initNameRE = regexp.MustCompile(`fugaro init|init --repo`)

// censusRoot is the repository root as this package's tests reach it.
const censusRoot = "../.."

// pendingRename is today's census: every file under liveRoots that names the
// command, sorted. It is 52 files -- 18 test files and 34 markdown files, and
// nothing else: the five that fell in no group were the website's, and W2.4
// renamed them (below). No non-test Go file and no golden is on it any more:
// W2.1's rename emptied internal/cli and the retirement transcript, and the
// generated page left it when its generator ran.
// Read on main at `1d325928`, this
// branch's base, it was **312** files -- 118 non-test Go files, 117 test files,
// 34 markdown files, 29 Terraform files, the four `.tftest.hcl` fixtures beside
// them, the two goldens, and eight in none of those groups: the website's four,
// the two under images/ (a Dockerfile and a shell script), the published
// schema, and internal/config/example.yaml, the sample config
// internal/config/example.go embeds.
//
// **W2.1 moved the list 107 files down, and it is the last L2 sweep.** 104
// were internal/cli's own -- the engine's lines, the refusals, the left-for-you
// commands and the tests that assert them, renamed together in one task -- and
// one was the generated website/content/cli-reference.md, which
// `go run ./internal/clidoc` renders from the command tree and so left the
// list by being regenerated, not edited. Two more emptied as that commit's
// own consequences: internal/cli/testdata/init_retire.golden, rewritten by
// TestInitRepoRetireTranscriptGolden -update-retire (the golden the two L2
// tasks were split by), and internal/e2e/init_test.go, whose assertions read
// internal/cli's own output -- the file W2.2 deliberately left on this list
// because the strings it quotes did not exist yet, and which W5.2 renames to
// update_test.go with the assertions it now holds.
//
// **The list moves in both directions, and each move is a merge a reader can
// find.** Forty-four files left it because the rename reached them: #766 (W2.5)
// renamed the 29 Terraform files, the four `.tftest.hcl` fixtures beside them,
// the published schema, internal/config/example.{go,yaml}, the two files under
// images/ and deploy/terraform/terraform.go with its test -- and
// deploy/terraform/gcp/roots/repo/tests/testdata/README.md, whose mention the
// same commit (03c70731) took out of its prose while leaving the file itself
// where it is; #765 (W1.1) renamed internal/initflow's loop.go, loop_test.go and
// stage.go. Leaving the list in the commit that renamed them is what the
// paragraph below asks of every rename task, and a file that stayed on it after
// its rename would fail the walk in the direction that costs the most: it would
// read as a promise still owed.
//
// Eleven files joined it, every one a mention the rename owes or has since
// paid, and eight merges put them there. #775 (the central-orchestrator folder's
// spec,
// reconciliation and plan) added docs/central-orchestrator/{README,
// implementation-plan}.md and #793 (the coordinator's plan) added
// docs/coordinator/implementation-plan.md -- both another artifact's dated
// record, which is the class docs/coordinator/**, docs/mobile/** and
// docs/windows-cli/** are already in; #798 (the mobile sign-in folder) added
// docs/mobile-auth/{implementation-plan,reconciliation}.md and #773 (the
// rigor-modes folder) docs/rigor-modes/{implementation-plan,reconciliation}.md.
// Three are code: #771 (W0.3, the register's Check 27 rewrite) added
// internal/cli/docs_setup_wizard_checks_test.go, whose mention is a scan
// pattern rather than text a user reads -- the guard that Check 27's Restore
// does not name the retired command, the same job this file's own pattern does
// -- so W2.1 has to re-point it rather than rename it; #765 (W1.1) added
// internal/cli/loop_program_test.go, whose loopProgramWant was the front door
// the stage engine is driven with and which #789 renamed to `fugaro setup`,
// taking that file off this list; and #770 (W0.1) added
// internal/cli/setup_refusals_test.go, a comment naming the bare `init --repo`
// form. The eleventh, #778's
// website/design-ref/GettingStartedV4.dc.html, is the one whose remedy is not
// the rename's to make alone: it is the owner-approved board for the
// getting-started page printing the command in its own copy, and the folder's
// README says the boards are not built, not shipped and not served -- so W2.4
// either renames the board or the owner rules the folder a dated record, and
// leaving it on the list is what keeps that choice visible instead of settled
// by a walk that stopped looking. **W2.4 renamed it**: the folder is a live
// root and not one of the ten dated ones, the board is the reference the page
// it draws was built from, and "rename the mention, move nothing" is the rule
// every other rename task follows. The folder keeps no old name for the reason
// a dated record does -- it records a look, not a date's decision.
//
// **#789 (W1.2), the merge that retired the word, moved the list six files down
// and one up, and this file is where that is paid for.** It emptied six listed
// files by renaming their mentions -- internal/cli/loop_program_test.go, the
// setup skill's plugin/skills/setup/SKILL.md and its reference/decisions.md and
// reference/transitions.md, plugin/skills/working/reference/status.md and
// plugin/skills_lint_test.go -- and left them on the list, where the walk then
// read them as six promises still owed; the paragraph below asks the opposite
// of every rename task, so they come off here. The one it added is
// internal/cli/docs_checkout_test.go, a comment explaining that the doc it
// checks still spells the command the retired way, which is W2.1's mention to
// rename along with the doc itself; it goes on the list for the same reason.
//
// **W2.2 moved the list 108 files down, and that is the widest move it takes.**
// Every file on it outside internal/cli that named the command is renamed
// there: 64 non-test Go files (the 60 the census command names, the bare
// `init --repo` of internal/backend/backend.go,
// internal/config/project_secrets.go and internal/runner/policy.go, and the one
// wrapped-only file above, internal/gcpfake/firebasemgmt.go) and 43 test files
// (the 37 the command names, the 5
// the `init --repo` pattern adds, and
// internal/infra/managed/untrusted_test.go, which only the walk sees). A
// forty-fourth is internal/cli's own -- managed_apply_test.go asserts a
// sentence internal/managed writes, so it leaves the list in this commit
// although the file is W2.1's -- and the rename reaches seven more files in
// that package that stay: a test in internal/cli that reads a string W2.2
// renamed has to move with it, wherever it lives, which is the rule the two
// L2 tasks are split by.
//
// One file it touches stays, and it is the exception to that rule:
// internal/e2e/init_test.go, whose nine assertions read internal/cli's own
// output -- W2.1's strings, which do not exist yet -- so they stay as they are
// until W2.1 lands, and the file stays here with them. A test cannot be moved
// to a string no task has written; that file is the one place the order of the
// two L2 tasks is visible.
//
// **W2.4 moved the list seven files down, and it is the website's whole
// share.** Six are the site's own: website/data/install.yaml (whose plugin
// path now offers `fugaro setup`), website/src/pages/getting-started.astro
// (whose `#setup` band is three steps and renders that command from the file),
// website/src/components/Changelog.astro (a comment quoting release-note
// prose), website/test/caps-and-costs.mjs (the comment resolving the page's
// anchors), website/README.md (two sentences of a round's own log), and
// website/design-ref/GettingStartedV4.dc.html, the board above. The seventh is
// plugin/readme_test.go, and it left because the sentence it pins left
// README.md's Getting started in the same commit: the file asserts that
// section's two steps, so the two moves are one move.
//
// One website file stays, and it is the generated one: website/content/
// security.md is SECURITY.md rendered byte for byte, so a document that still
// names the command still generates a page that does -- and SECURITY.md is
// W2.3's, which is why this task depends on it. website/content/
// cli-reference.md left the list earlier, when W2.1 regenerated it.
//
// Each task that renames a string takes the file off this list in the same
// commit, and W2.6 empties it.
var pendingRename = []string{
	".claude/skills/new-release/SKILL.md",
	"CONTRIBUTING.md",
	"README.md",
	"SECURITY.md",
	"docs/backends.md",
	"docs/base-image-migration.md",
	"docs/base-image.md",
	"docs/central-orchestrator/README.md",
	"docs/central-orchestrator/implementation-plan.md",
	"docs/checkout.md",
	"docs/coordinator/README.md",
	"docs/coordinator/implementation-plan.md",
	"docs/coordinator/reconciliation.md",
	"docs/dogfooding.md",
	"docs/gcp-live-checklist.md",
	"docs/gcp-setup.md",
	"docs/git-providers.md",
	"docs/in-job-review/reconciliation.md",
	"docs/mobile-auth/implementation-plan.md",
	"docs/mobile-auth/reconciliation.md",
	"docs/mobile/design/push-notifications.md",
	"docs/mobile/functional-spec-reconciliation.md",
	"docs/mobile/implementation-plan.md",
	"docs/mobile/reconciliation.md",
	"docs/multi-model.md",
	"docs/project-claude-accounts.md",
	"docs/project-layer.md",
	"docs/recipes.md",
	"docs/release.md",
	"docs/releases/README.md",
	"docs/rigor-modes/implementation-plan.md",
	"docs/rigor-modes/reconciliation.md",
	"docs/windows-cli/reconciliation.md",
	"internal/cli/docs_base_test.go",
	"internal/cli/docs_checkout_test.go",
	"internal/cli/docs_clone_workflows_test.go",
	"internal/cli/docs_compute_backend_test.go",
	"internal/cli/docs_managed_label_test.go",
	"internal/cli/docs_mobile_checks_review_test.go",
	"internal/cli/docs_mobile_checks_test.go",
	"internal/cli/docs_project_centric_test.go",
	"internal/cli/docs_project_layer_test.go",
	"internal/cli/docs_project_named_oauth_tokens_test.go",
	"internal/cli/docs_project_wide_git_test.go",
	"internal/cli/docs_providers_test.go",
	"internal/cli/docs_recipes_test.go",
	"internal/cli/docs_release_notes_test.go",
	"internal/cli/docs_setup_wizard_checks_test.go",
	"internal/cli/doctor_bucket_test.go",
	"internal/cli/restore_test.go",
	"plugin/setup_skill_test.go",
	"website/content/security.md",
}

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

// censusBinarySniff is how much of a file censusNamesInit reads before it
// decides whether the file is text. Eight KiB is enough for the two Windows
// binaries at this repository's root, whose NUL bytes start at byte 4.
const censusBinarySniff = 8192

// censusNamesInit reports whether the file at path names the command. The
// bytes are flattened before they are matched -- line-comment markers dropped,
// whitespace collapsed -- because prose and Go comments wrap, and a census
// that only saw the unwrapped spelling would miss the mention the moment
// somebody re-wrapped the sentence. Four files in this tree name the command
// in one of those spellings and nothing else: the `fugaro managed` Long help
// string wraps it across a line break (`internal/cli/managed_apply.go`), and
// three comments wrap it across a `//` marker
// (`internal/cli/project_job.go`, `internal/gcpfake/firebasemgmt.go`,
// `internal/infra/managed/untrusted_test.go`). flattenDoc is the same
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
	return initNameRE.MatchString(flattenDoc(string(append(head, rest...))))
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
		// appended to. The tombstone and its test, this file, and the relay
		// case whose input is the retired command line.
		{"docs/plans/setup-wizard/implementation-plan.md", true},
		{"docs/releases/v0.9.0.md", true},
		{"docs/design/x.md", true},
		{"docs/credentials-service/x.md", true},
		{"docs/website/x.md", true},
		{"docs/setup-wizard/implementation-plan.md", true},
		{"internal/cli/retired.go", true},
		{"internal/cli/retired_test.go", true},
		{"internal/cli/docs_live_init_census_test.go", true},
		{"internal/agent/relay_test.go", true},
		// The releases index beside the records stays live, and a prefix that
		// stops at a slash is not a prefix that stops mid-word.
		{"docs/releases/README.md", false},
		{"docs/plansx/x.md", false},
		{"docs/checkout.md", false},
		{"internal/cli/docs_checkout_test.go", false},
		// The retirement class is a list of paths, not a prefix.
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
		rel, err := filepath.Rel(censusRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git" || censusOutput(rel) {
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
// test.
//
// Mutation (run, restore): add a mention of the command to a live file that is
// not listed -- internal/cli/watch.go is one -- and it fails on the set having
// grown; rename every mention out of a listed file (internal/cli/config_list.go
// has one) and it fails on the set having shrunk.
func TestLiveInitCensusMatchesTheFrozenList(t *testing.T) {
	found := liveInitCensus(t, censusRoot)

	// The terminal assertion, and the one W2.6 flips. While the rename is in
	// flight an empty census means the walk stopped looking -- a root moved, the
	// pattern changed -- and "found nothing" is not the same claim as "there is
	// nothing to find"; W2.6, the task that empties `pendingRename`, turns it
	// into the opposite: the live tree must then name the retired command
	// nowhere.
	if len(found) == 0 {
		t.Fatal("the walk found no live file naming `fugaro init`: the roots or the pattern are wrong, and an empty census is not a clean tree")
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

	// W2.4 regenerates these two rather than editing them, and the census is
	// where that is visible: a page leaves the list when its generator runs,
	// not when a hand edit removes a mention. cli-reference.md is rendered
	// from the command tree (`go run ./internal/clidoc`), so W2.1's rename
	// reached it and it has left; security.md is prose a person writes, so it
	// stays until W2.4 regenerates it.
	for _, tc := range []struct {
		page   string
		listed bool
	}{
		{"website/content/cli-reference.md", false},
		{"website/content/security.md", true},
	} {
		if got := slices.Contains(pendingRename, tc.page); got != tc.listed {
			t.Errorf("pendingRename lists the generated %s = %v, want %v: it is written by its generator, so it leaves the list by being regenerated, not edited", tc.page, got, tc.listed)
		}
	}

	var grown, shrunk []string
	for _, p := range found {
		if !slices.Contains(pendingRename, p) {
			grown = append(grown, p)
		}
	}
	for _, p := range pendingRename {
		if !slices.Contains(found, p) {
			shrunk = append(shrunk, p)
		}
	}
	for _, p := range grown {
		t.Errorf("%s names `fugaro init` and is not in pendingRename: either rename it, or -- if it is a dated record -- say why it is exempt", p)
	}
	for _, p := range shrunk {
		t.Errorf("pendingRename lists %s, which no longer names the command: take it off the list in the commit that renamed it", p)
	}
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
		if name == ".git" || slices.Contains(liveRoots, name) || censusOutput(name) {
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
