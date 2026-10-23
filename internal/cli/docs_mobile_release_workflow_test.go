package cli

// The staged release workflow for the mobile app (docs/pending-workflows/mobile-release.yml).
//
// The mobile app is its own artifact outside the Go module (mobile plan D125)
// with its own release path, and this is that path: on every release tag, and on
// a manual dispatch, it builds the app on EAS and submits the finished build to
// TestFlight's internal testers and to Google Play's internal-testing track. The
// owner's rulings of 2026-10-19/20 fix the shape: the build runs from GitHub
// Actions on each tag and by hand; it waits at a `mobile-release` GitHub
// environment with the owner as its required reviewer (the same click
// `console-release` and `website-live` wait for); both platforms release, each
// in its own job, with one repository variable that can turn either off.
//
// No agent may write or edit `.github/workflows/**` -- GitHub refuses Fugaro's
// push of a workflow change -- so the file is staged under
// docs/pending-workflows/ for the owner to copy in by hand, the way
// images.yml, console-linux.yml and shared-tests.yml are staged there.
//
// "Staged for a human to copy" is only safe if the staged bytes are pinned by
// something a human cannot forget to update, and the properties that matter
// here are the ones that make the file safe to run on a public repository with
// a store credential in reach:
//
//   - TestDocsMobileReleaseWorkflowIsStagedAndPinned: the triggers are a
//     release-tag push and a manual dispatch and nothing else, on the tag
//     pattern the sibling release workflows use; **every path that can reach a
//     store passes a gate first** -- a tag push runs `scripts/release-gate.sh`
//     in each job, as every other tag-triggered publishing workflow here does,
//     and a manual dispatch that would submit runs a second gate of its own,
//     which holds a dispatched tag to that same script and a dispatched branch
//     to being on `main` (a dispatch with `dry_run` set passes neither, because
//     it submits nothing); the
//     permissions are `contents: read` plus the `checks: read` that gate needs;
//     both jobs wait at the `mobile-release` environment; the file names exactly
//     one secret (`EXPO_TOKEN`), carries no
//     literal that could be one, and binds the token in the `env:` of the steps
//     that talk to EAS rather than at job level; every `uses:` is a full 40-hex
//     commit with its `# vX.Y.Z` beside it; `eas-cli` is installed at one exact
//     version; the `concurrency` group makes two releases queue instead of
//     submitting at once; the `dry_run` input stops the run before the
//     submission; both platforms' `eas` commands run under
//     `eas env:exec production` with `--non-interactive`; the build's id and
//     **not** its artifact download URL reaches the job summary, and the
//     submission submits the build the run made (`--id`, never `--latest`); the
//     store identity is masked before any `eas` command runs, and its two
//     variable names appear nowhere but in that mask step; the two job gates
//     are one switch, compared whole; and **no reverse-DNS-shaped value appears
//     anywhere in the file**.
//   - TestDocsMobileReleaseStagingTellsTheOwnerWhatToCopyIn: the
//     section beside the staged file has to carry what a human cannot guess --
//     the environment's name and its required reviewer, the secret's name, the
//     order those two have to be created in, the copy-in command, the switch
//     that turns a platform off, the Android first-release checklist, and the
//     follow-up edits a copy-in needs.
//
// What this test cannot do is run the job. Nothing here executes GitHub
// Actions, and no `eas` command has ever been run from this repository: the
// store path itself is Check 89's (`docs/gcp-live-checklist.md`), which is
// owner-run and NOT RUN. What is checked is the file's text.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	// stagedMobileReleaseWorkflowPath is the file this change writes: the whole
	// workflow, in the directory an agent is allowed to write.
	stagedMobileReleaseWorkflowPath = "docs/pending-workflows/mobile-release.yml"
	// appliedMobileReleaseWorkflowPath is where the owner copies it to. The read
	// below looks at which of the two exists rather than assuming either: while
	// the file is pending the staged copy is what is judged, and once it has
	// been copied in and the staged copy removed the real file has to stand on
	// its own.
	appliedMobileReleaseWorkflowPath = ".github/workflows/mobile-release.yml"
	// mobileReleaseWorkflowMarker is what the applied file must contain once the
	// staged copy is gone: the one wrapper that keeps the store identity out of
	// this repository. A copy that lost the job, or a file renamed over it, is
	// refused here rather than passing vacuously.
	mobileReleaseWorkflowMarker = mobileReleaseEnvExecCommand
	// mobileReleaseReadmeHeading opens the staging document's section on this
	// workflow.
	mobileReleaseReadmeHeading = "## `mobile-release.yml`"
	// mobileReleaseEnvironment is the protected environment both jobs wait at.
	// It is the gate: a staging file cannot create it, which is why the README
	// beside the file carries it as a step for the owner.
	mobileReleaseEnvironment = "mobile-release"
	// mobileReleaseSecretName is the one secret the workflow reads -- an Expo
	// access token the owner creates in their own account. Only the name is
	// written down anywhere in this repository.
	mobileReleaseSecretName = "EXPO_TOKEN"
	// mobileReleaseDisabledPlatforms is the one repository variable that can turn
	// either platform off: its value names the platforms to skip (`ios`,
	// `android`, or both), and an unset variable names neither, so both jobs run.
	// It is a variable and not a workflow edit on purpose -- an agent cannot edit
	// a workflow, so an owner-facing switch has to be a setting -- and it is one
	// variable rather than two so that the owner reads one switch instead of a
	// pair that can disagree (the owner's ruling of 2026-10-20).
	mobileReleaseDisabledPlatforms = "MOBILE_RELEASE_DISABLED_PLATFORMS"
	// mobileReleaseIOSGate and mobileReleaseAndroidGate are the whole conditions
	// the two jobs run on -- one switch, read for one platform each -- and
	// mobileReleaseSubmitGate and mobileReleaseNoticeGate are the whole
	// conditions on the dry run, the submission's and the notice's. They are
	// compared by equality and not by substring because each one is a gate: a
	// condition that also fired on something else (a tag push, say) would run
	// what the gate exists to hold back, and `strings.Contains(got, "'true'")`
	// is green on `== 'true' }} || github.ref_type == 'tag'` exactly as it is on
	// the gate alone.
	mobileReleaseIOSGate     = "${{ !contains(vars." + mobileReleaseDisabledPlatforms + ", 'ios') }}"
	mobileReleaseAndroidGate = "${{ !contains(vars." + mobileReleaseDisabledPlatforms + ", 'android') }}"
	mobileReleaseSubmitGate  = "${{ github.event.inputs.dry_run != 'true' }}"
	mobileReleaseNoticeGate  = "${{ github.event.inputs.dry_run == 'true' }}"
	// mobileReleaseTagPattern is the tag filter: the app's own release stream,
	// spelled the way the sibling release workflows spell theirs
	// (`.github/workflows/release.yml`, `images.yml`, `console-release.yml`)
	// under the app's own prefix (`mobile-`, docs/release.md's "Mobile
	// releases"). A bare `v*` also starts a release on `vtest` and on a
	// `v1.2.3-rc1` tag; the CLI's own `v[0-9]+.[0-9]+.[0-9]+` filter -- which
	// this file carried until the app got its own stream -- starts one on every
	// CLI release as well, which is the coupling the owner split apart on
	// 2026-10-22.
	mobileReleaseTagPattern = "mobile-v[0-9]+.[0-9]+.[0-9]+"
	// mobileReleaseVersionStepName is the step that holds the tag's version to
	// the app config's: the app has its own tag stream (`mobile-vX.Y.Z`) and its
	// own version line (`expo.version` in `mobile/app.json`), EAS reports the
	// config's version rather than the tag, and a tag that disagrees with it
	// would be submitted as a version no tag names. The whole rule is the four
	// constants below, and each is asserted rather than a pattern that could
	// appear in a comment.
	mobileReleaseVersionStepName = "The tag's version is the app's version"
	// mobileReleaseVersionCondition is the whole `if:` on that step. It is a
	// fact about the **ref** rather than the event: a manual dispatch can name a
	// tag (`gh workflow run --ref mobile-v0.1.0`), and a dispatched tag reaches
	// a store exactly as a pushed one does, so the version rule has to cover
	// both. `github.event_name == 'push'` would leave the dispatched tag the
	// only way into a store behind a tag nothing compared with the config.
	mobileReleaseVersionCondition = "github.ref_type == 'tag'"
	// mobileReleaseVersionShapeCheck is the block that refuses a tag that is not
	// the app's own strict shape, and mobileReleaseVersionMismatchCheck the one
	// that refuses a tag whose version is not the config's. Both are read as
	// blocks (each has to stop the job), because a guard that only prints is a
	// build that still goes to a store (mobileReleaseGuardStops).
	mobileReleaseVersionShapeCheck    = `if [[ ! "${TAG}" =~ ^mobile-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then`
	mobileReleaseVersionMismatchCheck = `if [[ "${app_version}" != "${version}" ]]; then`
	// mobileReleaseVersionStrip is how the version is taken from the tag: the
	// one prefix `scripts/release-gate.sh` strips as well, so the number this
	// step compares is the number the gate is given.
	mobileReleaseVersionStrip = `version="${TAG#mobile-v}"`
	// mobileReleaseVersionRead is the whole read of the app's version line, and
	// its shape is the point: `jq -er` prints a value only when the field is
	// there, is not null and is a string, and exits non-zero otherwise, and the
	// steps carry `set -euo pipefail` -- so a config this step cannot read stops
	// the job rather than comparing the tag with an empty string (which a
	// missing `expo.version` would otherwise look like). It reads the committed
	// `mobile/app.json`, never a resolved `app.config.js`.
	mobileReleaseVersionRead = `jq -er '.expo.version | select(type == "string")' mobile/app.json`
	// mobileReleaseVersionMismatchError is the word both refusals use, so the
	// step's failure says what it refused and why rather than exiting silently.
	mobileReleaseVersionMismatchError = "refusing to build"
	// mobileReleaseEasCLIVersion is the exact `eas-cli` the job installs. The
	// pin matters because the job holds a store credential: a global install of
	// whatever `latest` was at the moment of the run is a dependency nobody
	// reviewed. **25.3.0** was the version the npm registry's `latest` tag
	// pointed at on 2026-10-21.
	mobileReleaseEasCLIVersion = "25.3.0"
	// mobileReleaseBuildJSONPath is the file the build step captures `eas build
	// --json` into. The summary and the submission read the build's id out of it
	// rather than asking EAS for the latest build of the profile, which is a
	// different build the moment anything else has run.
	mobileReleaseBuildJSONPath = "mobile-build.json"
	// mobileReleaseMaskCommand is how the mask step reads one identity value out
	// of the EAS project's environment, so that `::add-mask::` can hide it:
	// `mask <NAME>` is the only shape the two names below may take in the file.
	mobileReleaseMaskCommand = "mask "
	// mobileReleaseGateCommand is the release gate every tag-triggered
	// publishing workflow in this repository runs first
	// (`.github/workflows/release.yml`, `images.yml`, `console-release.yml`):
	// the tag is a strict `vX.Y.Z`, its commit is reachable from `origin/main`,
	// and the required checks succeeded on it. Without it a tag pushed on an
	// off-main or red commit would still build on EAS and submit to two stores.
	mobileReleaseGateCommand = "scripts/release-gate.sh"
	// mobileReleaseFollowUpEdits is how many follow-up edits the copy-in commit
	// needs, as the word both sides of it write: the two sentences that count and
	// name the files still pending, and the README section's own Status line,
	// which its test reads against the tree in both directions. Both sides are
	// read (mobileReleaseFollowUpEditsRE, below) and both have to say this: an
	// owner told "two" leaves the third edit out, and the suite goes red in their
	// commit rather than here.
	mobileReleaseFollowUpEdits = "Three"
	// mobileReleaseGateCondition is the whole `if:` on that step: the gate is a
	// claim about a tag. `images.yml` and `console-release.yml` carry the same
	// condition for the same reason. Compared whole rather than by substring,
	// like every other gate here: a condition that also fired on a dispatch
	// would run a gate that has no tag to check.
	mobileReleaseGateCondition = "github.event_name == 'push'"
	// mobileReleaseDispatchGateCondition is the whole `if:` on the **second**
	// gate step, the one a manual dispatch runs: a dispatch has no `push` event,
	// and the environment's approval is a person deciding to run the workflow
	// rather than a review of the commit it would submit. It fires only when the
	// dispatch would actually submit, because `dry_run` is what holds the other
	// path back -- a dry run reaches no store. Compared whole for the same reason
	// as every other gate here: `github.event.inputs.dry_run != 'true'` alone
	// would also fire on a tag push, and a disjunct on the event name would run
	// it on the path the first gate already covers.
	mobileReleaseDispatchGateCondition = "github.event_name == 'workflow_dispatch' && github.event.inputs.dry_run != 'true'"
	// mobileReleaseDispatchGateTagRef is how that step tells a dispatched tag
	// from a dispatched branch, and mobileReleaseDispatchGateMainCheck is the
	// half of the gate a branch is held to: the whole gate is a claim about a
	// tag, and a branch has no tag to shape-check, but "this commit is on main"
	// is the claim a submission needs either way.
	mobileReleaseDispatchGateTagRef    = "refs/tags/*"
	mobileReleaseDispatchGateMainCheck = "git merge-base --is-ancestor"
	// mobileReleaseEnvExecCommand is how every `eas` command in the file runs:
	// under the EAS project's `production` environment, which is what loads the
	// store identity, and **non-interactively**. The flag is not cosmetic:
	// interactive mode prefixes each line of the wrapped command's output, and
	// both halves of this workflow read that output as a value -- the mask step
	// masks what it reads, so a prefix is a string `::add-mask::` was never
	// given, and the build step parses its output as JSON, which a prefix
	// breaks.
	mobileReleaseEnvExecCommand = "eas env:exec --non-interactive production"
	// mobileReleaseMaskEmptyCheck is the branch the mask step takes when a value
	// comes back empty, and mobileReleaseMaskExit is what both that branch and a
	// failed read do: stop the job before the build step. Without them a read
	// that returned nothing masks nothing and the build below resolves
	// `mobile/app.config.js` -- which requires the identity only on EAS's own
	// build machines -- and prints it into a public log.
	mobileReleaseMaskEmptyCheck = `[ -z "${value}" ]`
	mobileReleaseMaskReadGuard  = "if ! value="
	mobileReleaseMaskExit       = "exit 1"
	// mobileReleaseBuildIDShapeCheck is the UUID test the submission applies to
	// the id it read out of the build's JSON before pasting it into the submit
	// command. `jq -e` fails on a document with no id; a shape check is what
	// stops an id that is not a UUID -- a URL, or a string carrying a `;` --
	// from becoming part of the command this job runs.
	mobileReleaseBuildIDShapeCheck = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
	// mobileReleaseAndroidSwitchStep is the owner step the README has to carry
	// while the Play side is not ready: the Android job is enabled by default,
	// so the variable has to be set **before** the copy-in, or the first run of
	// the copied-in file can submit to Play. The step is read against the
	// copy-in command's position, not merely as a sentence that exists.
	mobileReleaseAndroidSwitchStep = "`" + mobileReleaseDisabledPlatforms + "=android`, before the copy-in"
	// mobileReleasePinTestName and mobileReleaseReadmeTestName are the two
	// checks this file adds. The document beside the staged file has to name
	// both, so the paragraph that tells a reader what is verified cannot outlive
	// the names it prints.
	mobileReleasePinTestName    = "TestDocsMobileReleaseWorkflowIsStagedAndPinned"
	mobileReleaseReadmeTestName = "TestDocsMobileReleaseStagingTellsTheOwnerWhatToCopyIn"
	// mobileReleaseOwnerStepsHeading is the heading over the owner's list of the
	// things that have to be in place before the workflow can run at all. Its
	// count is a claim about the list under it, and the two were read against
	// each other because they had drifted apart: the heading said three over four
	// numbered steps. A step that is not done *before* the first run -- the
	// dry-run read, which is done after the copy-in, and the version bump, which
	// is done before every release -- belongs to its own subsection, which is
	// what the paragraph under the list forwards to.
	mobileReleaseOwnerStepsHeading = "### Before it can run: the three things the owner does first"
)

// mobileReleaseNumberWords reads the count the heading states, in the words the
// document writes it in. It is the reader `consolePendingSentence` uses for the
// pending-file counts, narrowed to the heading that counts owner steps.
var mobileReleaseNumberWords = map[string]int{"one": 1, "two": 2, "three": 3, "four": 4, "five": 5}

// mobileReleaseIdentityNames are the two EAS environment variables that hold the
// store identity. They are the exception that proves the rule below: the
// identity's *value* is nowhere in this repository, but the mask step cannot
// hide a value it cannot name, so each of these appears exactly once per job --
// as the argument of a `mask` call -- and nowhere else.
var mobileReleaseIdentityNames = []string{"FUGARO_IOS_BUNDLE_ID", "FUGARO_ANDROID_PACKAGE"}

// mobileReleaseWorkflow is docs/pending-workflows/mobile-release.yml, decoded
// with unknown fields refused: a key nobody reads is a key nobody pins. `on` is
// spelled out as two named triggers rather than a map, so a third trigger --
// `pull_request`, `workflow_run`, `pull_request_target` -- is a decode error
// here rather than a line the assertions below have to remember to refuse.
type mobileReleaseWorkflow struct {
	Name        string                  `yaml:"name"`
	On          mobileReleaseWorkflowOn `yaml:"on"`
	Permissions map[string]string       `yaml:"permissions"`
	Concurrency struct {
		Group            string `yaml:"group"`
		CancelInProgress bool   `yaml:"cancel-in-progress"`
	} `yaml:"concurrency"`
	Jobs map[string]mobileReleaseJob `yaml:"jobs"`
}

type mobileReleaseWorkflowOn struct {
	Push             *mobileReleasePushTrigger `yaml:"push"`
	WorkflowDispatch *mobileReleaseDispatch    `yaml:"workflow_dispatch"`
}

type mobileReleasePushTrigger struct {
	Tags []string `yaml:"tags"`
}

type mobileReleaseDispatch struct {
	Inputs map[string]mobileReleaseInput `yaml:"inputs"`
}

type mobileReleaseInput struct {
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
	Default     any    `yaml:"default"`
}

type mobileReleaseJob struct {
	Name           string              `yaml:"name"`
	If             string              `yaml:"if"`
	RunsOn         string              `yaml:"runs-on"`
	TimeoutMinutes int                 `yaml:"timeout-minutes"`
	Environment    string              `yaml:"environment"`
	Permissions    map[string]string   `yaml:"permissions"`
	Env            map[string]string   `yaml:"env"`
	Steps          []mobileReleaseStep `yaml:"steps"`
}

type mobileReleaseStep struct {
	Name             string            `yaml:"name"`
	If               string            `yaml:"if"`
	Uses             string            `yaml:"uses"`
	Run              string            `yaml:"run"`
	WorkingDirectory string            `yaml:"working-directory"`
	With             map[string]any    `yaml:"with"`
	Env              map[string]string `yaml:"env"`
}

// readMobileReleaseWorkflow returns the workflow's bytes and the path they came
// from, reading the staged copy while it exists and the applied file after the
// copy-in.
func readMobileReleaseWorkflow(t *testing.T) (mobileReleaseWorkflow, []byte, string) {
	t.Helper()
	raw, source, problems, err := readMobileReleaseWorkflowAt(
		"../../"+stagedMobileReleaseWorkflowPath, "../../"+appliedMobileReleaseWorkflowPath)
	for _, p := range problems {
		t.Error(p)
	}
	if err != nil {
		t.Fatal(err)
	}
	var wf mobileReleaseWorkflow
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&wf); err != nil {
		t.Fatalf("decode %s: %v", source, err)
	}
	return wf, raw, source
}

// readMobileReleaseWorkflowAt is the read above with its two paths as arguments
// and its verdicts returned rather than reported, so that
// TestReadDocsMobileReleaseWorkflowFallsBackOnlyWhilePending can pin every mode
// against temp files independently of what this repository currently holds --
// the same shape images/base_workflow_test.go's readWorkflow and
// docs_shared_tests_workflow_test.go's readSharedTestsWorkflowAt use.
//
// The rules, in the order they are decided:
//
//   - The staged copy wins outright while it exists. It is the file a human
//     reviews, so it is the file the assertions judge.
//   - While both exist they must be byte-identical: the file under
//     `.github/workflows/` then is the reviewed staged one rather than
//     something hand-written, which is what makes reviewing the staged copy
//     worth anything.
//   - Once the staged copy is gone the applied file must carry the marker, so a
//     copy that lost the job is refused here rather than passing vacuously.
//     This is also why the README's copy-in procedure removes the staged copy:
//     left in place, it would freeze these assertions on its own bytes and hide
//     every later edit to the file that runs.
func readMobileReleaseWorkflowAt(stagedPath, appliedPath string) (raw []byte, source string, problems []string, err error) {
	staged, stagedErr := os.ReadFile(stagedPath)
	applied, appliedErr := os.ReadFile(appliedPath)
	switch {
	case stagedErr == nil && appliedErr == nil:
		raw, source = staged, stagedPath
		if !bytes.Equal(staged, applied) {
			problems = append(problems, fmt.Sprintf(
				"%s and %s both exist and differ: the copied-in file is not the reviewed staged one (copy the file in and `git rm` the staged copy, as docs/pending-workflows/README.md says -- until it is removed, nothing here can see an edit to the applied file)",
				appliedPath, stagedPath))
		}
	case stagedErr == nil:
		raw, source = staged, stagedPath
	case os.IsNotExist(stagedErr) && appliedErr == nil:
		raw, source = applied, appliedPath
		if !strings.Contains(string(applied), mobileReleaseWorkflowMarker) {
			return nil, "", problems, fmt.Errorf("%s does not contain %q and %s no longer exists: was the staged file removed without its content reaching the applied one?", appliedPath, mobileReleaseWorkflowMarker, stagedPath)
		}
	default:
		return nil, "", problems, fmt.Errorf("neither %s nor %s is readable: %v", stagedPath, appliedPath, stagedErr)
	}
	return raw, source, problems, nil
}

// mobileReleaseUsesRE and mobileReleaseShaRE are the pin check's vocabulary: a
// `uses:` line -- the action and its ref, then the `# vX.Y.Z` comment that makes
// the SHA readable, both required by the `$` anchor so a trailing word cannot
// slip past -- and the 40-hex commit a ref must be.
var (
	mobileReleaseUsesRE = regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*(\S+)(?:\s+#\s*v(\S+))?\s*$`)
	mobileReleaseShaRE  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// mobileReleaseSecretRE finds every `secrets.NAME` the file reads. The
	// assertion below is that there is exactly one and that it is the Expo
	// token: a second one would be a credential nobody wrote down.
	mobileReleaseSecretRE = regexp.MustCompile(`secrets\.([A-Za-z0-9_]+)`)
	// mobileReleaseDottedRE finds a dotted token of two or more labels: the
	// shape the store identity has (mobile/README.md: "letters, digits and
	// hyphens in two or more dot-separated segments"). Version comments
	// (`# v7.0.1`) and runner images (`ubuntu-24.04`) are excluded by requiring
	// the LAST label to start with a letter, and the file names the workflow is
	// allowed to mention are excluded by suffix below.
	//
	// **Case-insensitive, deliberately**: the identity's labels may be either
	// case (`mobile/README.md` says "letters, either case"), so a pasted
	// `com.Fugaro.App` has to be caught exactly as `com.fugaro.app` is. A
	// lowercase-only scan would pass the one accident it exists to catch, and
	// the sentence in docs/pending-workflows/README.md that describes it says so.
	mobileReleaseDottedRE = regexp.MustCompile(`(?i)\b[a-z][a-z0-9-]*(?:\.[a-z0-9-]+)+\b`)
	// mobileReleaseFollowUpEditsRE finds the sentence in which each side of the
	// copy-in states **how many** follow-up edits the copy-in commit needs: the
	// workflow file's own header, and the README section that writes them out.
	// Both are read, and both have to say mobileReleaseFollowUpEdits.
	mobileReleaseFollowUpEditsRE = regexp.MustCompile(`\b(\w+) follow-up edits\b`)
	// mobileReleaseEasCLIRE finds the CLI install and captures whatever version,
	// if any, is pinned to it. Go's regexp has no lookahead, so a bare
	// `eas-cli` is caught by requiring the capture to be exactly `@<version>`
	// rather than by refusing an install without one.
	mobileReleaseEasCLIRE = regexp.MustCompile(`npm install --global eas-cli([^\s]*)`)
	// mobileReleaseNumberedItemsRE finds the owner list's numbered steps: each
	// one is written `**1. [per repo] …**`, so an item is a bolded number at the
	// start of a line and nothing else in the section is.
	mobileReleaseNumberedItemsRE = regexp.MustCompile(`(?m)^\*\*([0-9]+)\. `)
	// mobileReleaseCountWordRE reads the count the heading states ("the three
	// things the owner does first"), which the list under it has to match.
	mobileReleaseCountWordRE = regexp.MustCompile(`the (\w+) things? the owner`)
)

// mobileReleaseKnownSuffixes are the file extensions a dotted token may end in
// without being mistaken for an identity: `app.json`, `mobile/eas.json`,
// `mobile/package.json`, `mobile/app.config.js`, `images.yml` and the like are
// references to files in this repository, not a bundle identifier.
var mobileReleaseKnownSuffixes = []string{"yml", "yaml", "json", "js", "md", "sh"}

// mobileReleaseAllowedDotted is every other dotted token the file may carry,
// each one a field name rather than a value. It is short on purpose: adding an
// entry is the deliberate act of saying "this is not the store identity", which
// is what makes the check below worth having. The `github.` context namespace
// (`github.event.inputs.dry_run`, `github.event_name`, `github.ref_name`,
// `github.token`) is allowed as a namespace rather than entry by entry, below:
// a path into the event payload is not a value, and no bundle identifier is
// spelled `github.…`.
var mobileReleaseAllowedDotted = []string{
	"engines.node",                  // mobile/package.json's field, named in a comment
	"submit.internal.android.track", // the EAS submit field C8 pins, named in a comment
	"android.buildType",             // the eas.json field that makes the Android build an AAB
	"artifacts.buildUrl",            // the EAS field the summary deliberately does not read
	"Info.plist",                    // the iOS binary's metadata file, named in a comment
	// The app's own version line: the field the version rule compares with the
	// tag (mobile/app.json). A field name and never a value -- the version it
	// carries is a number the tag also carries, which is the point of the step.
	"expo.version",
}

// mobileReleaseSummaryRE finds the steps that write the run summary. The build's
// URL has to reach a reader of the run, and `$GITHUB_STEP_SUMMARY` is the only
// place a job can put it.
var mobileReleaseSummaryRE = regexp.MustCompile(`GITHUB_STEP_SUMMARY`)

// mobileReleasePerLabelRE finds the `[per …]` labels the README puts on the
// steps only the owner can do, each one naming where that step is done. The
// section's set of them is checked rather than only the ones the needles name:
// a label is a claim about a place, and a place no step is done in is a step a
// reader goes looking for.
var mobileReleasePerLabelRE = regexp.MustCompile(`\[per [^\]]+\]`)

// TestDocsMobileReleaseWorkflowIsStagedAndPinned: the staged release
// workflow is safe to copy in. Every assertion below is a property a later edit
// could silently drop, and each was planted and seen red while this test was
// written.
func TestDocsMobileReleaseWorkflowIsStagedAndPinned(t *testing.T) {
	wf, raw, path := readMobileReleaseWorkflow(t)
	text := string(raw)

	if wf.Name != mobileReleaseEnvironment {
		t.Errorf("%s is named %q; the workflow's name is what a badge and a run's check names are built from", path, wf.Name)
	}

	// --- The trigger surface ------------------------------------------------
	// A release tag push and a manual dispatch, and nothing else. The decode above
	// refuses a third trigger; these assertions refuse a weakened tag filter,
	// which is the half that decides whether the release runs at all.
	if wf.On.Push == nil || wf.On.WorkflowDispatch == nil {
		t.Fatalf("%s must trigger on both a release tag push and a manual dispatch (push %v, workflow_dispatch %v)", path, wf.On.Push != nil, wf.On.WorkflowDispatch != nil)
	}
	if !slices.Equal(wf.On.Push.Tags, []string{mobileReleaseTagPattern}) {
		t.Errorf("the push trigger is for tags %q, not [%s]: this is the mobile app's release, and `v*` -- the pattern this file used to carry -- also starts one on `vtest` and on a `v1.2.3-rc1` tag", wf.On.Push.Tags, mobileReleaseTagPattern)
	}
	// The header's copy-in instructions are a procedure, and the number of edits
	// in it is a claim about another file: the README section lists them, and the
	// third is that section's own Status line. This side is checked here, the
	// README's side in the test below, and both against the same count.
	mobileReleaseFollowUpEditsIn(t, path+"'s header", text)
	// The brief's own named refusals, scanned in the bytes as well as decoded:
	// `pull_request_target` and `workflow_run` run with a read/write token and
	// the repository's secrets, and this job holds an Expo token.
	for _, banned := range []string{"pull_request_target", "workflow_run", "write-all", "continue-on-error", "set -x"} {
		if strings.Contains(text, banned) {
			t.Errorf("%s carries %q; this job holds a store credential, publishes to a store, and must not be reachable with a write token or print its environment", path, banned)
		}
	}
	if !strings.Contains(text, "set -euo pipefail") {
		t.Errorf("%s has no `set -euo pipefail`: a `run:` step without it reports success for a failure in the middle of the step", path)
	}

	// --- The dry-run input --------------------------------------------------
	// One boolean input, defaulting to a dry run: a person trying the workflow
	// gets a build and no store upload.
	run, ok := wf.On.WorkflowDispatch.Inputs["dry_run"]
	if !ok {
		t.Fatalf("%s's manual dispatch carries no `dry_run` input (%v): the workflow has to be runnable without submitting anything", path, keys(wf.On.WorkflowDispatch.Inputs))
	}
	if run.Type != "boolean" {
		t.Errorf("the dry_run input is of type %q, not boolean: the conditions below compare it to `true`, and a free-text input is a way to submit by typo", run.Type)
	}
	if got := fmt.Sprintf("%v", run.Default); got != "true" {
		t.Errorf("the dry_run input defaults to %v, not true: a manual dispatch that means to submit says so, and one that means to look does not", run.Default)
	}

	// --- Permissions --------------------------------------------------------
	// `contents: read` at the top, so no job can widen it by omission, and no
	// scope beside it.
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("%s's workflow-level permissions are %v, not exactly {contents: read}: this job reads the repository and talks to EAS, and creates no GitHub release, tag or artifact", path, wf.Permissions)
	}
	// A job adds one scope to the floor above: `checks: read`, which is what the
	// release gate's `gh api …/check-runs` call needs. Nothing else -- a job
	// block is where a widening hides, and neither job writes anything.
	for name, job := range wf.Jobs {
		want := map[string]string{"contents": "read", "checks": "read"}
		if len(job.Permissions) != len(want) || job.Permissions["contents"] != want["contents"] || job.Permissions["checks"] != want["checks"] {
			t.Errorf("job %q declares permissions %v, not exactly {contents: read, checks: read}: the release gate reads the tagged commit's check-runs and needs the second, and a job that writes nothing needs nothing else", name, job.Permissions)
		}
	}

	// --- Every `eas env:exec` is non-interactive ----------------------------
	// Interactive mode prefixes each line of the wrapped command's output, and
	// both halves of this workflow read that output as a value: the mask step
	// masks what it reads, so a prefix is a string `::add-mask::` was never
	// given, and the build step parses its output as JSON, which a prefix
	// breaks. Read over the steps' commands rather than the text, because the
	// file's comments name `eas env:exec production` too.
	for name, job := range wf.Jobs {
		for i, step := range job.Steps {
			for _, line := range strings.Split(workflowRunCommands(step.Run), "\n") {
				if !strings.Contains(line, "eas env:exec") {
					continue
				}
				if !strings.Contains(line, mobileReleaseEnvExecCommand) {
					t.Errorf("%s's %s job runs `%s` in step %d without `--non-interactive`: interactive mode prefixes each output line, and this workflow masks that output and parses it as JSON", path, name, strings.TrimSpace(line), i)
				}
			}
		}
	}

	// --- Concurrency --------------------------------------------------------
	// One release at a time, keyed on the workflow rather than the ref: two
	// releases must never submit at once, and a submission in flight is never
	// cancelled by the next tag.
	if !strings.Contains(wf.Concurrency.Group, mobileReleaseEnvironment) || strings.Contains(wf.Concurrency.Group, "github.ref") {
		t.Errorf("the concurrency group is %q: it has to be the workflow's own name and not the ref, or two releases submit at once", wf.Concurrency.Group)
	}
	if wf.Concurrency.CancelInProgress {
		t.Error("concurrency.cancel-in-progress is true: a release that is mid-submission would be cancelled by the next tag")
	}

	// --- The secrets --------------------------------------------------------
	// Exactly one, named, and no literal that could be one. The token is the
	// only credential this workflow holds: the Apple signing material and the
	// App Store Connect key are EAS's, which is why the submission needs no
	// second secret here. Where it is bound is a claim about steps, so it is
	// checked with the jobs below.
	refs := mobileReleaseSecretRE.FindAllStringSubmatch(text, -1)
	for _, ref := range refs {
		if ref[1] != mobileReleaseSecretName {
			t.Errorf("%s reads `secrets.%s`: the Expo access token is the only credential this job needs, and every extra reference is a credential reachable from a public repository", path, ref[1])
		}
	}
	for _, lit := range []string{"-----BEGIN", "PRIVATE KEY", "private_key", "password:", "ascAppId"} {
		if strings.Contains(text, lit) {
			t.Errorf("%s carries %q, which is the shape of a literal secret or a value bound by hand", path, lit)
		}
	}

	// --- The identity's names, and the one place they may appear ------------
	// The iOS bundle identifier and the Android package name live in the EAS
	// project's own environment, and neither value is in this repository (mobile
	// plan D125, mobile/README.md). Their **names** are the one exception, and
	// the mask step is why: a value can only be hidden from a log by
	// `::add-mask::`, which has to be given the value, which means naming the
	// variable it is read from. So each name appears exactly once per job -- as
	// the argument of a `mask` call, which the job walk below checks -- and
	// nowhere else in the file.
	for _, name := range mobileReleaseIdentityNames {
		if got := strings.Count(text, name); got != len(wf.Jobs) {
			t.Errorf("%s names %s %d times, and there are %d jobs: the name is the argument of the mask step's `mask` call, once per job, and the identity itself is read from the EAS project's environment and appears nowhere in this repository", path, name, got, len(wf.Jobs))
		}
	}
	for _, name := range []string{"bundleIdentifier", "android.package"} {
		if strings.Contains(text, name) {
			t.Errorf("%s names %q: the store identity is read from the EAS project's environment, and the file has no reason to name the config key it is set through either", path, name)
		}
	}
	for _, token := range mobileReleaseIdentityShapedTokens(text) {
		t.Errorf("%s carries the reverse-DNS-shaped token %q, which is the shape of the store identity: the identity is set in EAS by the owner and read through `eas env:exec production`, and a value here is a value in a public repository", path, token)
	}

	// --- The two jobs -------------------------------------------------------
	if got := keys(wf.Jobs); !slices.Equal(got, []string{"android", "ios"}) {
		t.Fatalf("%s carries the jobs %v, not [android ios]: one platform's release is one job", path, got)
	}
	// Both jobs, by the same rules: the two platforms release from here (the
	// owner's ruling of 2026-10-20), so Android is not a copy that may lag.
	ios := mobileReleaseCheckJob(t, path, "ios", wf.Jobs["ios"], mobileReleaseIOSGate)
	android := mobileReleaseCheckJob(t, path, "android", wf.Jobs["android"], mobileReleaseAndroidGate)
	if got := ios.bound + android.bound; got != len(refs) {
		t.Errorf("%s reads `secrets.%s` %d times and %d steps bind it: every reference belongs in the `env:` of a step that talks to EAS, because a job-level binding hands the token to every step in the job -- the checkout, the node, the CLI install and the summary included", path, mobileReleaseSecretName, len(refs), got)
	}

	// --- What the file must never run ---------------------------------------
	// Checked over the steps' commands rather than the whole text: the header and
	// the steps' own comments name all three of these to say why they are not
	// used, and a comment is not a command.
	//
	//   - `artifacts.buildUrl` is a download link for a signed binary whose
	//     Info.plist carries the bundle identifier, and a run summary in a public
	//     repository is public. The summary carries the build's page id instead.
	//   - `eas build:list` asks EAS for a build instead of reading back the one
	//     this run made, which is the same mistake `--latest` makes.
	//   - `--latest` submits the latest build of the profile, whoever made it.
	for name, job := range wf.Jobs {
		for i, step := range job.Steps {
			for _, banned := range []string{"artifacts.buildUrl", "eas build:list", "--latest"} {
				if strings.Contains(workflowRunCommands(step.Run), banned) {
					t.Errorf("%s's %s job runs %q in step %d: the submission names the build this run made (`--id`), and the summary carries its page id rather than a link that downloads the binary", path, name, banned, i)
				}
			}
		}
	}

	// --- The pins -----------------------------------------------------------
	// Every action by full commit SHA with its version beside it: a floating ref
	// (`@v7`) is a mutable tag on someone else's repository, and this job holds
	// a store credential.
	allUses := append(workflowJobUses(wf.Jobs["ios"]), workflowJobUses(wf.Jobs["android"])...)
	pins := mobileReleaseUsesRE.FindAllStringSubmatch(text, -1)
	if len(pins) != len(allUses) {
		t.Errorf("%s has %d `uses:` steps (%q) but %d match the pinned form `uses: <action>@<sha> # v<version>`: an unpinned or oddly written one is listed here rather than judged", path, len(allUses), allUses, len(pins))
	}
	for _, m := range pins {
		at := strings.LastIndex(m[1], "@")
		if at < 0 {
			t.Errorf("uses %q has no version at all: the action is not pinned to anything", m[1])
			continue
		}
		action, ref, version := m[1][:at], m[1][at+1:], m[2]
		if !mobileReleaseShaRE.MatchString(ref) {
			t.Errorf("uses %s@%s is not pinned by a full commit SHA: a tag is a mutable name on a repository this one does not control", action, ref)
		}
		if version == "" {
			t.Errorf("uses %s@%s carries no `# vX.Y.Z` beside it: a bare SHA is a pin nobody can read or compare", action, ref)
		}
	}
	for _, want := range []string{"actions/checkout@", "actions/setup-node@"} {
		if !slices.ContainsFunc(allUses, func(u string) bool { return strings.HasPrefix(u, want) }) {
			t.Errorf("%s never uses %s: the job cannot check the app out or select a node line without it", path, want)
		}
	}

	// The one install in the file, and it is pinned to a version rather than
	// floating: `eas-cli` is not an action, so there is no SHA to pin it by, and
	// a global install of whatever `latest` was at the moment of the run is a
	// dependency nobody reviewed in a job that holds a store credential.
	installs := mobileReleaseEasCLIRE.FindAllStringSubmatch(text, -1)
	if len(installs) != len(wf.Jobs) {
		t.Fatalf("%s installs eas-cli %d times for %d jobs: each job runs on its own runner, so each one installs its own CLI", path, len(installs), len(wf.Jobs))
	}
	for _, m := range installs {
		if m[1] != "@"+mobileReleaseEasCLIVersion {
			t.Errorf("%s installs `eas-cli%s`, not `eas-cli@%s`: the job holds a store credential, and an unpinned global install is a dependency nobody reviewed", path, m[1], mobileReleaseEasCLIVersion)
		}
	}
}

// mobileReleaseJobShape is one platform's job, counted: what the walk below
// found in it and what each step did. The two jobs are the same shape with one
// platform's name in it -- both platforms release from here -- so every rule is
// written once and applied to both, rather than an iOS rule that Android
// quietly lacks.
type mobileReleaseJobShape struct {
	platform string
	gates    int
	// dispatchGates counts the second gate step, the one a manual dispatch that
	// would submit runs. It is counted apart from `gates` because the two steps
	// carry different conditions and hold different paths: one gate that fired
	// on both events would be a gate with no tag to check, and a job with only
	// the tag gate is the hole this count exists to close.
	dispatchGates int
	// versionSteps counts the step that holds the tag's version to the app
	// config's. It is one per job and it is asserted apart from the gates
	// because it is a *refusal* rather than a lock: it decides nothing about
	// who may run the release, and it is the only thing that stops a tag whose
	// version is not the version EAS would report.
	versionSteps int
	installs     int
	masks        int
	builds       int
	summaries    int
	submits      int
	dryRuns      int
	// bound is how many of the job's steps bind the Expo token, which the caller
	// holds against the number of `secrets.EXPO_TOKEN` references in the file.
	bound int
	uses  []string
}

// mobileReleaseCheckJob checks one platform's job against the rules that hold
// for both, and returns what it found for the counts the caller asserts. `gate`
// is the whole `if:` the job has to carry: the caller passes that platform's,
// and equality is the whole check, because a condition with a second disjunct
// would run what the switch exists to hold back.
//
// Every predicate reads the step's *commands* rather than its whole `run:`
// block (workflowRunCommands): the file's comments name `eas env:exec
// production`, `--wait`, `artifacts.buildUrl` and the rest of what is checked
// here, and a comment must not be able to satisfy a check on a command.
func mobileReleaseCheckJob(t *testing.T, path, platform string, job mobileReleaseJob, gate string) mobileReleaseJobShape {
	t.Helper()
	shape := mobileReleaseJobShape{platform: platform, uses: workflowJobUses(job)}

	// The switch: one repository variable naming the platforms to skip, read for
	// this platform. An unset variable names neither, which is what "both
	// platforms release by design" means, and nothing else may appear in the
	// condition -- a second disjunct (a tag push, say) would start a store
	// submission the owner never turned on.
	if got := job.If; got != gate {
		t.Errorf("the %s job's `if` is %q, not exactly %q: `%s` names the platforms to skip, so this job runs unless the variable names %s -- and an unset variable names neither, so both platforms release", platform, got, gate, mobileReleaseDisabledPlatforms, platform)
	}
	// The environment is the gate: GitHub holds the job until its only required
	// reviewer approves it, however the run was started.
	if job.Environment != mobileReleaseEnvironment {
		t.Errorf("the %s job's environment is %q, not %q: the environment's required reviewer is the whole of what stops a store submission, and a job without it starts as soon as its trigger fires", platform, job.Environment, mobileReleaseEnvironment)
	}
	if job.TimeoutMinutes <= 0 {
		t.Errorf("the %s job has timeout-minutes %d: a run that waits on a remote build without a bound is a job nobody can tell from a hung one", platform, job.TimeoutMinutes)
	}
	if !strings.HasPrefix(job.RunsOn, "ubuntu-") {
		t.Errorf("the %s job runs on %q: the build itself runs on EAS's workers, so the runner is only the client that uploads it", platform, job.RunsOn)
	}
	// No job-level `env:` at all. The token belongs on the steps that talk to
	// EAS, so that the checkout, the node line and the CLI install never hold it.
	if len(job.Env) != 0 {
		t.Errorf("the %s job binds %v at job level: the Expo token belongs in the `env:` of the steps that talk to EAS, and a job-level binding hands it to every step in the job", platform, job.Env)
	}

	var gateAt, dispatchGateAt, versionAt, maskAt, buildAt, summaryAt, submitAt = -1, -1, -1, -1, -1, -1, -1
	for i, step := range job.Steps {
		cmd := workflowRunCommands(step.Run)
		switch {
		case strings.HasPrefix(step.Uses, "actions/checkout@"):
			if !strings.Contains(fmt.Sprintf("%v", step.With["persist-credentials"]), "false") {
				t.Errorf("the %s job's checkout keeps the persisted credentials: this job only reads the repository", platform)
			}
			// Full depth: `scripts/release-gate.sh` asks whether the tagged
			// commit is reachable from `origin/main`, which a shallow clone
			// cannot answer.
			if got := fmt.Sprintf("%v", step.With["fetch-depth"]); got != "0" {
				t.Errorf("the %s job's checkout takes fetch-depth %q, not 0: the release gate below asks whether the tagged commit is reachable from origin/main, and a shallow clone cannot answer that", platform, got)
			}
		case step.Name == mobileReleaseVersionStepName:
			// The version rule: the app's own strict tag shape, the version
			// taken from the tag, and the comparison with the app config's
			// `expo.version` -- each half a block that stops the job rather
			// than printing and carrying on.
			shape.versionSteps++
			if versionAt < 0 {
				versionAt = i
			}
			if got := step.If; got != mobileReleaseVersionCondition {
				t.Errorf("the %s job's version step carries `if: %s`, not exactly `%s`: the rule is a fact about the ref, so it holds a dispatched tag (`gh workflow run --ref mobile-v0.1.0`) to the same comparison a pushed one is held to, and an event-based condition would leave the dispatched tag the one way into a store that nothing compared with the app's config", platform, got, mobileReleaseVersionCondition)
			}
			if env := step.Env["TAG"]; env != "${{ github.ref_name }}" {
				t.Errorf("the %s job's version step sets TAG to %q, not %q: the version comes from the tag, and the tag is the ref this run was started from", platform, env, "${{ github.ref_name }}")
			}
			if !strings.Contains(cmd, mobileReleaseVersionStrip) {
				t.Errorf("the %s job's version step does not take the version out of the tag (`%s`): the prefix is the app's stream's (`%s`), and the version under it is what the gate below is given too", platform, mobileReleaseVersionStrip, mobileReleaseTagPattern)
			}
			if !strings.Contains(cmd, mobileReleaseVersionRead) {
				t.Errorf("the %s job's version step does not read the app's version as %s: the version compared with the tag is `expo.version` in the committed `mobile/app.json`, read so that a missing or non-string field stops the job rather than comparing the tag with an empty string", platform, mobileReleaseVersionRead)
			}
			if !strings.Contains(cmd, mobileReleaseVersionMismatchError) {
				t.Errorf("the %s job's version step does not say %q when it refuses: a step that exits silently leaves a release that did not happen with nothing in the log to say why", platform, mobileReleaseVersionMismatchError)
			}
			for _, block := range []struct{ what, open string }{
				{"the tag's shape", mobileReleaseVersionShapeCheck},
				{"the two versions", mobileReleaseVersionMismatchCheck},
			} {
				if !mobileReleaseGuardStops(cmd, block.open) {
					t.Errorf("the %s job's version step has no %s block that stops the job (opening on %q and reaching an `exit 1` before its `fi`): a refusal that only prints is a build that still goes to EAS and to a store under a version no tag names", platform, block.what, block.open)
				}
			}
		case strings.Contains(cmd, mobileReleaseGateCommand):
			// Two gate steps, one per event, each with its own whole condition:
			// the tag push runs the gate itself, and a manual dispatch that
			// would submit runs the second one -- a dispatched tag through the
			// whole gate, a dispatched branch through its on-main half. A
			// dispatch with `dry_run` set submits nothing and is held to
			// neither.
			shape.gates++
			if gateAt < 0 {
				gateAt = i
			}
			switch got := step.If; got {
			case mobileReleaseGateCondition:
				if !strings.Contains(cmd, `"$TAG"`) {
					t.Errorf("the %s job's release gate does not pass the tag to %s: the script takes the tag as its argument and refuses anything that is not a strict vX.Y.Z", platform, mobileReleaseGateCommand)
				}
				for name, want := range map[string]string{
					"GH_TOKEN": "${{ github.token }}", // the checks API read the gate makes
					"TAG":      "${{ github.ref_name }}",
				} {
					if env := step.Env[name]; env != want {
						t.Errorf("the %s job's release gate sets %s to %q, not %q: the gate cannot read the commit's check-runs without the token, and it needs the pushed tag rather than any other ref", platform, name, env, want)
					}
				}
			case mobileReleaseDispatchGateCondition:
				shape.dispatchGates++
				dispatchGateAt = i
				// A dispatched tag is held to the whole gate, so the command
				// has to reach it: a step that only checked the branch case
				// would let a dispatch on a `vtest` tag submit.
				if !strings.Contains(cmd, mobileReleaseGateCommand) || !strings.Contains(cmd, mobileReleaseDispatchGateTagRef) {
					t.Errorf("the %s job's manual-dispatch gate never sends a dispatched tag through %s (it has to tell %s from a branch and run the whole gate for it): a dispatch can name a tag as well as a branch, and a tag that is not a strict vX.Y.Z is not a release", platform, mobileReleaseGateCommand, mobileReleaseDispatchGateTagRef)
				}
				// And a dispatched branch through the gate's second half: the
				// commit has to be reachable from `origin/main`, which is what
				// makes the submission a release rather than a build of
				// whatever branch the dispatcher named.
				if !strings.Contains(cmd, mobileReleaseDispatchGateMainCheck) || !strings.Contains(cmd, "origin/main") {
					t.Errorf("the %s job's manual-dispatch gate does not check that the dispatched commit is on main (`%s ... origin/main`): the environment's approval is a person deciding to run this workflow, not a review of the commit it would submit, so an off-main dispatch would submit behind nothing but that click", platform, mobileReleaseDispatchGateMainCheck)
				}
				if !strings.Contains(cmd, mobileReleaseMaskExit) {
					t.Errorf("the %s job's manual-dispatch gate does not stop the job on a commit that is not on main: a check whose failure only prints is a submission that still happens", platform)
				}
				for name, want := range map[string]string{
					"GH_TOKEN": "${{ github.token }}", // the checks API read the tag path makes
					"REF":      "${{ github.ref }}",
					"REF_NAME": "${{ github.ref_name }}",
				} {
					if env := step.Env[name]; env != want {
						t.Errorf("the %s job's manual-dispatch gate sets %s to %q, not %q: the step has to tell a dispatched tag from a dispatched branch (%s), and the tag path needs the token the gate reads check-runs with", platform, name, env, want, mobileReleaseDispatchGateTagRef)
					}
				}
			default:
				t.Errorf("the %s job's release gate step %d carries `if: %s`, which is neither `%s` (the tag push) nor `%s` (a manual dispatch that would submit): the two events are the two paths into a store, and each one needs the gate its own path can be held to", platform, i, got, mobileReleaseGateCondition, mobileReleaseDispatchGateCondition)
			}
		case strings.HasPrefix(step.Uses, "actions/setup-node@"):
			// The Node line comes from the app's own manifest, the way
			// console-linux.yml takes the console's: a second copy written here
			// is a pin that can disagree with the app's.
			if got := fmt.Sprintf("%v", step.With["node-version-file"]); got != "mobile/package.json" {
				t.Errorf("the %s job's setup-node takes node-version-file %q, not mobile/package.json: the app's own engines.node is the pin", platform, got)
			}
		case strings.Contains(cmd, "npm install --global eas-cli"):
			shape.installs++
		case strings.Contains(cmd, "::add-mask::"):
			shape.masks++
			maskAt = i
			for _, name := range mobileReleaseIdentityNames {
				if !strings.Contains(cmd, mobileReleaseMaskCommand+name) {
					t.Errorf("the %s job's mask step does not mask %s: `eas build` and `eas submit` resolve mobile/app.config.js locally, and this repository is public, so an identity value that reaches the log is public with it", platform, name)
				}
			}
			// The read that feeds the mask. `2>/dev/null || true` is the shape
			// this step must not have: it turns a read that failed into a step
			// that succeeded having masked nothing, and the build below then
			// resolves `mobile/app.config.js` -- which requires the identity
			// only on EAS's own build machines, so the build itself does not
			// fail -- and prints the identity into a public log. A value that
			// came back empty is the same accident without the error.
			if strings.Contains(cmd, "|| true") {
				t.Errorf("the %s job's mask step swallows a read that failed (`|| true`): the step then masks nothing and succeeds, and the build below resolves mobile/app.config.js -- which requires the identity only on EAS's own build machines -- and prints the identity into this public log. A read that fails has to stop the job before the build", platform)
			}
			if !strings.Contains(cmd, mobileReleaseEnvExecCommand) {
				t.Errorf("the %s job's mask step does not read the values with `%s`: interactive mode prefixes each line `eas env:exec` prints, and a prefixed line is not the value `::add-mask::` is asked to hide", platform, mobileReleaseEnvExecCommand)
			}
			if !strings.Contains(cmd, mobileReleaseMaskEmptyCheck) {
				t.Errorf("the %s job's mask step does not test the value it read (`%s`): a variable that is set and empty masks nothing, and the build below then prints whatever the identity resolves to", platform, mobileReleaseMaskEmptyCheck)
			}
			// And both guards have to stop the job, not just one: a branch that
			// reports a value it could not read and carries on is a build
			// started without the mask this step exists to put in place, which
			// is the same accident the swallowed read was. Counted rather than
			// merely looked for, so that one `exit` cannot stand in for two.
			guards := 0
			for _, guard := range []string{mobileReleaseMaskReadGuard, mobileReleaseMaskEmptyCheck} {
				if strings.Contains(cmd, guard) {
					guards++
				}
			}
			if got := strings.Count(cmd, mobileReleaseMaskExit); got != guards {
				t.Errorf("the %s job's mask step stops the job %d time(s) for %d guard(s) (the read that failed, and the value that came back empty): each guard has to stop the job before the build, because the build below resolves mobile/app.config.js and prints whatever the identity resolves to", platform, got, guards)
			}
		case strings.Contains(cmd, "eas build --platform "+platform):
			shape.builds++
			buildAt = i
			if !strings.Contains(cmd, mobileReleaseEnvExecCommand) {
				t.Errorf("the %s job's build step does not run under `eas env:exec production`: `eas-cli` resolves mobile/app.config.js locally before it uploads anything, and the identity comes from the EAS project's environment", platform)
			}
			for _, flag := range []string{"--non-interactive", "--wait", "--json"} {
				if !strings.Contains(cmd, flag) {
					t.Errorf("the %s job's build step is missing %s: `--non-interactive` keeps a prompt from hanging the job, `--wait` makes the build it reports the one it made, and `--json` is what carries that build's id to the submission", platform, flag)
				}
			}
			if !strings.Contains(cmd, mobileReleaseBuildJSONPath) {
				t.Errorf("the %s job's build step does not capture its output into %s: the steps after it read the build's id from there, and without it the submission has nothing to name", platform, mobileReleaseBuildJSONPath)
			}
		case mobileReleaseSummaryRE.MatchString(cmd) && strings.Contains(cmd, mobileReleaseBuildJSONPath):
			shape.summaries++
			summaryAt = i
			if !strings.Contains(cmd, "build id") {
				t.Errorf("the %s job's summary step does not print the build's id: the id is what names the build's page, and the artifact download link is deliberately not printed", platform)
			}
		case strings.Contains(cmd, "eas submit ") && strings.Contains(cmd, "--platform "+platform):
			shape.submits++
			submitAt = i
			if !strings.Contains(cmd, mobileReleaseEnvExecCommand) {
				t.Errorf("the %s job's submission does not run under `eas env:exec production`: the local `eas-cli` process needs the identity the environment carries, or it stops before it uploads anything", platform)
			}
			if !strings.Contains(cmd, "--non-interactive") {
				t.Errorf("the %s job's submission is missing --non-interactive", platform)
			}
			if !strings.Contains(cmd, "--id") || !strings.Contains(cmd, mobileReleaseBuildJSONPath) {
				t.Errorf("the %s job's submission does not name the build this run made: it has to read the id out of %s and pass it to `--id`, because `--latest` submits the latest build of the profile whoever made it", platform, mobileReleaseBuildJSONPath)
			}
			if got := step.If; got != mobileReleaseSubmitGate {
				t.Errorf("the %s job submits with `if: %s`, not `if: %s`: the submission is what `dry_run` stops before, and a condition with anything else in it (a tag push, say) submits on a dispatch that asked for a dry run", platform, got, mobileReleaseSubmitGate)
			}
			// The id is pasted into the shell string this step runs, so its
			// shape is checked before it gets there: `jq -e` refuses a document
			// with no id, and this is what refuses an id that is not a UUID.
			// The whole guard is read rather than the pattern alone --
			// mobileReleaseBuildIDGuard says why: a `[[ … =~ … ]]` with no `!`,
			// or one whose body reports the wrong shape and carries on, still
			// carries the pattern this file looks for.
			guard, why, guardEnd := mobileReleaseBuildIDGuard(cmd)
			submitAt := strings.Index(cmd, "eas submit --profile")
			switch {
			case why != "":
				t.Errorf("the %s job's submission does not guard the build's id before pasting it into the submit command: %s. The id comes out of a JSON document this run parsed, and anything else in that field -- a URL, or a string carrying a `;` -- would become part of the command", platform, why)
			case submitAt >= 0 && submitAt < guardEnd:
				t.Errorf("the %s job checks the build's id's shape only at byte %d of the step and submits at byte %d (the guard is %q): the check has to run before the command it protects, or the id reaches `--id` unchecked", platform, guardEnd, submitAt, guard)
			}
		case mobileReleaseSummaryRE.MatchString(cmd) && strings.Contains(cmd, "Dry run"):
			shape.dryRuns++
			if got := step.If; got != mobileReleaseNoticeGate {
				t.Errorf("the %s job's dry-run notice carries `if: %s`, not `if: %s`: the notice says a run submitted nothing, so it has to be the exact complement of the submission's gate", platform, got, mobileReleaseNoticeGate)
			}
		}
	}

	// The token is bound on the steps that talk to EAS and on no other: the CLI
	// install needs no credential, and a step that runs no `eas` command has no
	// reason to hold one.
	for i, step := range job.Steps {
		got, binds := step.Env[mobileReleaseSecretName]
		runsEas := strings.Contains(workflowRunCommands(step.Run), mobileReleaseEnvExecCommand)
		switch {
		case binds && !runsEas:
			t.Errorf("the %s job's step %d binds %s and runs no `eas` command: a step that does not use the token has no reason to hold it", platform, i, mobileReleaseSecretName)
		case runsEas && !binds:
			t.Errorf("the %s job's step %d runs `eas env:exec production` without binding %s: the token is what lets it load the project's environment at all", platform, i, mobileReleaseSecretName)
		}
		if !binds {
			continue
		}
		shape.bound++
		if want := "${{ secrets." + mobileReleaseSecretName + " }}"; got != want {
			t.Errorf("the %s job's step %d binds %s to %q, not %q: the token is read from the environment's secret store and never written down", platform, i, mobileReleaseSecretName, got, want)
		}
	}

	// The counts and the order. One of each step, and the order is load-bearing:
	// the gate has to run before anything is built or submitted, the mask has to
	// be in place before the first command that resolves the app's config, the
	// summary describes the build the step before it made, and the submission
	// submits that same build.
	if shape.gates != 2 || shape.dispatchGates != 1 || shape.versionSteps != 1 || shape.installs != 1 || shape.masks != 1 || shape.builds != 1 || shape.summaries != 1 || shape.submits != 1 || shape.dryRuns != 1 {
		t.Errorf("the %s job carries %d release gate step(s) (%d of them the manual-dispatch one), %d version step(s), %d install(s), %d mask step(s), %d build(s), %d summary step(s), %d submission(s) and %d dry-run notice(s), not two gates, one version step and one of everything else: the job is this platform's whole release, and a store submission can start from a tag push or from a dispatch that would submit", platform, shape.gates, shape.dispatchGates, shape.versionSteps, shape.installs, shape.masks, shape.builds, shape.summaries, shape.submits, shape.dryRuns)
	} else if !(versionAt < gateAt && gateAt < dispatchGateAt && dispatchGateAt < maskAt && maskAt < buildAt && buildAt < summaryAt && summaryAt < submitAt) {
		t.Errorf("the %s job's steps are out of order (version %d, tag gate %d, dispatch gate %d, mask %d, build %d, summary %d, submission %d): the version rule has to refuse a tag the app's config disagrees with before the gate reads anything, both gates have to refuse a commit that is off-main or red before anything is built, the mask has to run before the first `eas` command that resolves mobile/app.config.js, the summary has to describe the build that was made, and the submission has to follow both", platform, versionAt, gateAt, dispatchGateAt, maskAt, buildAt, summaryAt, submitAt)
	}
	if len(shape.uses) != 2 {
		t.Errorf("the %s job uses %d actions (%q); it needs a checkout and a node", platform, len(shape.uses), shape.uses)
	}
	return shape
}

// mobileReleaseGuardStops reports whether the `if` block that opens on `open`
// in the step's command stops the job: the block's own closing `fi` has a line
// `exit 1` before it. It is what mobileReleaseBuildIDGuard does for the
// submission's UUID check, in the small: a refusal this step writes as an
// `echo` with no `exit` reads as a green assertion against
// `strings.Contains(cmd, …)`, and it is a job that carries on to build and
// submit the very tag it just said it refused.
//
// The block is found by its opening line and closed at the first `fi` on a line
// of its own, which is the shape both blocks in that step have (the `if [[ … ]];
// then` form, one level deep, closed with a bare `fi`).
func mobileReleaseGuardStops(cmd, open string) bool {
	at := strings.Index(cmd, open)
	if at < 0 {
		return false
	}
	rest := cmd[at+len(open):]
	end := regexp.MustCompile(`(?m)^\s*fi\s*$`).FindStringIndex(rest)
	if end == nil {
		return false
	}
	return regexp.MustCompile(`(?m)^\s*exit\s+[1-9][0-9]*\s*$`).MatchString(rest[:end[0]])
}

// mobileReleaseBuildIDGuard reads a submission step's shape check **as a guard**
// rather than as a pattern that appears somewhere in the step, and it is the
// reason the submission assertion uses it: `mobileReleaseBuildIDShapeCheck` is a
// literal, and every way of breaking this check that matters leaves the literal
// where it is. Remove the `!` and an id that *is* a UUID is the only one the job
// refuses, while a URL or a string carrying a `;` goes into the submit command;
// drop the `exit 1` and a guard that fires only prints; test some other variable
// and the id is never looked at. All three read as a green test against a
// `strings.Contains(cmd, mobileReleaseBuildIDShapeCheck)`.
//
// It returns the guard's text, the byte the step reaches once the guard has
// closed (so the caller can hold the submit command to coming after it), and a
// reason the guard is not one -- the pattern missing, no `!`, no `=~`, a
// variable other than `${build_id}`, a first line that opens no block, a block
// never closed with `fi`, a body that does not stop the job, or a body that runs
// the submission itself. A reason means the step has no guard of this shape and
// the caller has nothing to read; an empty reason means `guard` is the whole
// `if … fi` and the byte is where it ends.
//
// The check is deliberately matched against the file's own pattern constant and
// not a looser "looks like a UUID" shape: this test pins what the workflow
// documents, so a pattern that was quietly widened (`^.*$`, or a `grep -q` that
// accepts anything) has to be an edit to that constant and to this test too.
func mobileReleaseBuildIDGuard(cmd string) (guard, why string, endsAt int) {
	lines := strings.Split(cmd, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, mobileReleaseBuildIDShapeCheck) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", "the UUID pattern the check applies is nowhere in the step", -1
	}
	head := strings.TrimSpace(lines[start])
	switch {
	case !strings.HasPrefix(head, "if [["):
		return "", fmt.Sprintf("the pattern is on %q, which is not an `if [[ … ]]` guard: a pattern in a command of its own checks nothing", head), -1
	case !strings.Contains(head, "=~"):
		return "", fmt.Sprintf("the guard %q applies no `=~` match, so the pattern beside it is text in a condition that never uses it", head), -1
	case !strings.Contains(head, "${build_id}"):
		return "", fmt.Sprintf("the guard %q does not test ${build_id}: whatever it does test, the id read out of the build's JSON still reaches the submit command", head), -1
	case !strings.Contains(head, "!"):
		return "", fmt.Sprintf("the guard %q has no `!`: it refuses a build id that *is* a UUID and lets every other value -- a URL, a string carrying a `;` -- through to the submit command", head), -1
	case !strings.HasSuffix(head, "then"):
		return "", fmt.Sprintf("the guard opens with %q, which is not a conditional block (`then`)", head), -1
	}
	stop := -1
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "fi" {
			stop = i
			break
		}
	}
	if stop < 0 {
		return "", fmt.Sprintf("the guard on line %d of the step is never closed with `fi`", start+1), -1
	}
	body := strings.Join(lines[start:stop+1], "\n")
	if !strings.Contains(body, "exit 1") {
		return "", fmt.Sprintf("the guard %q does not stop the job (`exit 1`): a shape check whose failure only prints is a submission that still happens", body), -1
	}
	if strings.Contains(body, "eas submit") {
		return "", fmt.Sprintf("the guard %q runs the submission itself: the check has to come before the command it protects", body), -1
	}
	for _, line := range lines[:stop+1] {
		endsAt += len(line) + 1
	}
	return body, "", endsAt
}

// TestMobileReleaseBuildIDGuardIsAGuardAndNotAPattern pins the reader above
// against temp text rather than against the file, so each way the check can be
// present and still do nothing is checkable without editing the staged workflow:
// the six breakages its own comment names -- the pattern missing, no `!`, the
// match not applied, another variable tested, a block never closed, and a body
// that only prints -- the guard that is moved after the command it protects,
// and the one good guard those fixtures are variations of. It is what makes the
// submission assertion's use of it safe -- that assertion reads the staged file,
// and this one is the statement of what "a guard" means in it.
//
// The mutations that prove it are the fixtures themselves: every `bad` case here
// is a guard a `strings.Contains(cmd, mobileReleaseBuildIDShapeCheck)` would
// accept.
func TestMobileReleaseBuildIDGuardIsAGuardAndNotAPattern(t *testing.T) {
	const pat = mobileReleaseBuildIDShapeCheck
	good := strings.Join([]string{
		`build_id=$(jq -er '.id' "$RUNNER_TEMP/mobile-build.json")`,
		`if [[ ! "${build_id}" =~ ` + pat + ` ]]; then`,
		`  echo "::error::the build's id is not a UUID" >&2`,
		`  exit 1`,
		`fi`,
		`eas env:exec --non-interactive production "eas submit --profile internal --id ${build_id}"`,
	}, "\n")
	guard, why, endsAt := mobileReleaseBuildIDGuard(good)
	if why != "" {
		t.Fatalf("the guard these fixtures are variations of is rejected: %s", why)
	}
	if !strings.Contains(guard, pat) || endsAt <= 0 {
		t.Fatalf("the guard came back as %q ending at %d, and it is the `if … fi` carrying the pattern", guard, endsAt)
	}

	for _, bad := range []struct{ what, cmd, want string }{
		{"the pattern is not there at all", strings.Replace(good, pat, `^[0-9a-f-]+$`, 1), "nowhere in the step"},
		{"the `!` is gone", strings.Replace(good, "if [[ ! ", "if [[ ", 1), "no `!`"},
		{"the body only prints", strings.Replace(good, "\n  exit 1", "", 1), "does not stop the job"},
		{"another variable is tested", strings.Replace(good, `"${build_id}"`, `"${other_id}"`, 1), "does not test ${build_id}"},
		{"the block is never closed", strings.Replace(good, "\nfi\n", "\n", 1), "never closed with `fi`"},
		{"the match is not applied", strings.Replace(good, " =~ ", " == ", 1), "applies no `=~`"},
	} {
		_, why, _ := mobileReleaseBuildIDGuard(bad.cmd)
		if why == "" {
			t.Errorf("%s was accepted as a shape check, so this test would pass on a workflow whose id guard does nothing: the check has to be the `if [[ ! … =~ pattern ]] … exit 1 … fi` the file's comment describes", bad.what)
			continue
		}
		if !strings.Contains(why, bad.want) {
			t.Errorf("%s was rejected for the wrong reason: %q, and the reason looked for is %q", bad.what, why, bad.want)
		}
		if !strings.Contains(bad.cmd, mobileReleaseBuildIDShapeCheck) && bad.what != "the pattern is not there at all" {
			t.Errorf("the fixture for %s no longer carries the pattern literal, so it no longer stands for a check that is present and does nothing", bad.what)
		}
	}

	// And the order the assertion reads: a guard that is present, well shaped
	// and placed *after* the command it protects still leaves the id pasted
	// unchecked, so the byte the guard ends at has to be before the submit.
	moved := strings.Join([]string{
		`build_id=$(jq -er '.id' "$RUNNER_TEMP/mobile-build.json")`,
		`eas env:exec --non-interactive production "eas submit --profile internal --id ${build_id}"`,
		`if [[ ! "${build_id}" =~ ` + pat + ` ]]; then`,
		`  exit 1`,
		`fi`,
	}, "\n")
	_, why, endsAt = mobileReleaseBuildIDGuard(moved)
	if why != "" {
		t.Fatalf("the moved guard is a guard: %s", why)
	}
	if submit := strings.Index(moved, "eas submit --profile"); submit >= endsAt {
		t.Fatalf("the moved fixture puts the submission at %d and the guard ends at %d: this fixture exists to be the order the submission assertion refuses, which is the submission *before* the byte the guard ends at", submit, endsAt)
	}
}

// mobileReleaseFollowUpEditsIn checks the one claim in text about how many
// follow-up edits the copy-in commit needs, and reports every way it can be
// wrong: stated nowhere, stated twice, or stating a count other than the three
// this copy-in actually needs. It is shared by the two tests because the two
// sides have to agree -- the workflow file's header tells the owner the number,
// and the README section is where the edits are written out.
func mobileReleaseFollowUpEditsIn(t *testing.T, what, text string) {
	t.Helper()
	got := mobileReleaseFollowUpEditsRE.FindAllStringSubmatch(text, -1)
	if len(got) != 1 {
		t.Errorf("%s states how many follow-up edits the copy-in needs %d times (%q); it has to state it exactly once, because the count and the list it introduces are one claim", what, len(got), got)
		return
	}
	if got[0][1] != mobileReleaseFollowUpEdits {
		t.Errorf("%s says %q follow-up edits, and the copy-in needs %s: the two sentences that count and name the pending files, and the Status line the copy-in changes -- which is read against the tree, so leaving it out of that commit turns the suite red there", what, got[0][1], mobileReleaseFollowUpEdits)
	}
}

// workflowRunCommands returns a step's `run:` script with its comment lines
// removed. The file's comments name the very commands and flags the assertions
// above check for -- `eas env:exec production`, `--wait`, `--non-interactive`
// -- so a check against the whole `run:` block can be satisfied by a comment
// while the command it describes is gone. Every predicate about what a step
// runs goes through here.
func workflowRunCommands(run string) string {
	var out []string
	for _, line := range strings.Split(run, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// workflowJobUses returns the actions a job's steps use, in the order they
// appear: the pin check counts them, and a count that has to agree with the
// `uses:` lines in the file is a count taken from the decoded steps rather than
// from the text.
func workflowJobUses(job mobileReleaseJob) []string {
	var out []string
	for _, step := range job.Steps {
		if step.Uses != "" {
			out = append(out, step.Uses)
		}
	}
	return out
}

// mobileReleaseIdentityShapedTokens returns every dotted token in text that has
// the shape of the store identity and is not one of the few the file may carry:
// two or more lowercase labels whose last one is not a file extension this
// repository uses. It is a function rather than an inline loop so that
// TestDocsMobileReleaseIdentityShapedTokensSeeAnIdentity can pin it against
// fixtures: the check above passes on a clean file, and a check that passes is
// not a proof that it could fail.
func mobileReleaseIdentityShapedTokens(text string) []string {
	var out []string
	for _, token := range mobileReleaseDottedRE.FindAllString(text, -1) {
		labels := strings.Split(token, ".")
		last := labels[len(labels)-1]
		// A version comment (`# v7.0.1`) and a runner image (`ubuntu-24.04`)
		// end in a digit: neither is a name. Either case counts as a letter, for
		// the reason the scan above is case-insensitive.
		if len(last) < 2 || !isASCIILetter(last[0]) {
			continue
		}
		if slices.Contains(mobileReleaseKnownSuffixes, strings.ToLower(last)) {
			continue
		}
		// A path into the GitHub event payload is a field the workflow reads,
		// not a value: `github.event_name` is the release gate's condition, and
		// `github.ref_name` and `github.token` are what that step is given.
		if strings.HasPrefix(strings.ToLower(token), "github.") {
			continue
		}
		if slices.Contains(mobileReleaseAllowedDotted, token) {
			continue
		}
		if !slices.Contains(out, token) {
			out = append(out, token)
		}
	}
	slices.Sort(out)
	return out
}

// isASCIILetter reports whether b is an ASCII letter of either case. The scan
// above is case-insensitive, so the label tests that decide whether a token is
// a name have to be too: `com.Fugaro.App`'s last label starts with an uppercase
// letter and is a name exactly as `com.fugaro.app`'s is.
func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// TestDocsMobileReleaseIdentityShapedTokensSeeAnIdentity pins the check above
// against fixtures: an identity is reported, a version comment, a runner image
// and the file names this repository uses are not, and the short allow-list is
// what lets the three field names through.
func TestDocsMobileReleaseIdentityShapedTokensSeeAnIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       []string
	}{
		{"an identity with three labels", "bundle id: com.fugaro.app\n", []string{"com.fugaro.app"}},
		{"an identity with two labels", "the app is fugaro.app\n", []string{"fugaro.app"}},
		{"an identity pasted into a command", "eas build --platform ios # com.example.phone\n", []string{"com.example.phone"}},
		// The identity's labels may be either case (mobile/README.md), so a scan
		// that only saw lowercase would pass the one paste it exists to catch.
		{"an identity in mixed case", "the bundle id is com.Fugaro.App\n", []string{"com.Fugaro.App"}},
		{"an identity in upper case", "bundle id: COM.FUGARO.APP\n", []string{"COM.FUGARO.APP"}},
		{"a clean file", "uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1\nruns-on: ubuntu-24.04\nnode-version-file: mobile/package.json\nsee mobile/app.config.js and docs/pending-workflows/README.md\n", nil},
		{"the two allowed field names", "engines.node submit.internal.android.track\n", nil},
		// The event-payload namespace the release gate and the dry-run conditions
		// read, allowed as a namespace rather than entry by entry.
		{"the github context namespace", "if: github.event_name == 'push'\nenv:\n  TAG: ${{ github.ref_name }}\n  GH_TOKEN: ${{ github.token }}\nif: ${{ github.event.inputs.dry_run != 'true' }}\n", nil},
		{"a version with no comment", "  uses: actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0\n", nil},
		// A file name this repository uses, in the case its own file names use.
		{"a file name in mixed case", "see docs/pending-workflows/README.md and mobile/package.json\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mobileReleaseIdentityShapedTokens(tc.text)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("tokens = %q, want %q", got, tc.want)
			}
		})
	}
}

// mobileReleaseReadmeSection returns the body of the staging document's
// section on mobile-release.yml, from its heading to the next `## ` heading,
// fences and all: what this test reads includes the commands the owner pastes,
// which live in fences. Scoped rather than whole-document because every claim
// here is about this one workflow: a sentence in another workflow's section
// must not be able to satisfy it.
func mobileReleaseReadmeSection(t *testing.T) string {
	t.Helper()
	doc := readDoc(t, pendingWorkflowsReadmePath)
	start := strings.Index(doc, "\n"+mobileReleaseReadmeHeading)
	if start < 0 {
		t.Fatalf("%s has no %q heading: this test reads that section and nothing else", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading)
	}
	rest := doc[start+1:]
	if end := strings.Index(rest[len(mobileReleaseReadmeHeading):], "\n## "); end >= 0 {
		return rest[:len(mobileReleaseReadmeHeading)+end]
	}
	return rest
}

// TestDocsMobileReleaseStagingTellsTheOwnerWhatToCopyIn: the staged
// file cannot create a GitHub environment or a secret, so the section beside it
// is where the owner reads what to make and what to run. Everything a human
// cannot guess from the file is asserted here: the environment's name and its
// required reviewer, the secret's name, **the order the two have to be created
// in** (before the copy-in, or GitHub creates the environment with no reviewer),
// the copy-in command, the one switch that can turn either platform off, the
// Android first-release checklist and the accounts each of its steps belongs to,
// the two follow-up edits a copy-in needs, and the name of the check that pins
// the file -- so a rename cannot leave the document pointing at a test that is
// gone.
func TestDocsMobileReleaseStagingTellsTheOwnerWhatToCopyIn(t *testing.T) {
	section := mobileReleaseReadmeSection(t)

	// The other half of the header's claim, against the same count: this section
	// is where the edits are written out, and the third of them is this
	// section's own Status line, read against the tree just below.
	mobileReleaseFollowUpEditsIn(t, pendingWorkflowsReadmePath+"'s `"+mobileReleaseReadmeHeading+"` section", section)

	for _, want := range []struct{ needle, why string }{
		{"`" + mobileReleaseEnvironment + "`", "the protected environment both jobs wait at, by name"},
		{"required reviewer", "the reviewer whose click is the gate, and that there is one"},
		{"Prevent self-review", "the setting that has to stay off, or the owner's own run can never be approved"},
		{"the file is copied in, not after it", "the order: the environment, the secret and the Android switch exist before the copy-in"},
		{mobileReleaseAndroidSwitchStep, "the Android switch, with the order it has to be set in: the job is enabled by default, so a copy-in made before it is a file whose first run can submit to Play"},
		{mobileReleaseDispatchGateCondition, "the second gate's own condition, spelled as the file spells it: a manual dispatch that would submit is gated, and one that asked for a dry run is not"},
		{"stops the job", "the mask step's fail-closed read: a value it could not read or that came back empty stops the job before the build, rather than masking nothing and carrying on"},
		{"UUID-shaped", "the shape check the submission applies to the build's id before pasting it into the submit command"},
		{"no required reviewer at all", "what an environment GitHub creates on first use has, which is why that order matters"},
		{"`" + mobileReleaseSecretName + "`", "the one secret the workflow reads, by name and never by value"},
		{"gh secret set " + mobileReleaseSecretName, "the command that creates the secret, with the environment it belongs on"},
		{"gh api --method PUT \"repos/$REPO/environments/" + mobileReleaseEnvironment + "\"", "the command that creates the environment"},
		{"cp docs/pending-workflows/mobile-release.yml .github/workflows/mobile-release.yml", "the copy-in command"},
		{"git rm docs/pending-workflows/mobile-release.yml", "the removal of the staged copy, which is load-bearing rather than tidy"},
		{"website/README.md", "the first of the two follow-up edits a copy-in needs"},
		{"opening line", "the second: this document's own opening line counts and names the pending files"},
		{mobileReleaseTagPattern, "the tag filter the workflow triggers on, stated rather than left as the `v*` this file used to carry"},
		{"`" + mobileReleaseDisabledPlatforms + "`", "the repository variable that can turn either platform off, by name"},
		{"gh variable set " + mobileReleaseDisabledPlatforms, "the command that turns a platform off, which is the whole of what \"one repository setting\" means"},
		{mobileReleaseGateCommand, "the gate a tag push runs first, so an off-main or red commit publishes nothing"},
		{mobileReleaseGateCondition, "the condition that keeps that gate off the manual-dispatch path, where there is no tag to check"},
		{"checks: read", "the one scope the release gate adds beside `contents: read`"},
		{"com.Fugaro.App", "the mixed case the identity scan catches as well as the lowercase spelling"},
		{"::add-mask::", "the mechanism that keeps the identity out of the log, by name"},
		{"first dry run", "the run whose log the owner reads for an identifier, since no test can"},
		{"eas-cli@" + mobileReleaseEasCLIVersion, "the CLI's exact pinned version"},
		{"npm view eas-cli version", "where that version was checked, so the next reader can check it again"},
		{"--latest", "the flag the submission deliberately does not use, named rather than left to be discovered"},
		{"Android first release", "the Play checklist's own heading"},
		{"Google Play Console", "the console the Android app record is created in, by hand"},
		{"Data safety", "one of the Play forms the internal track needs, by name"},
		{"Content rating", "another of those forms, by name"},
		{"Release to testing tracks", "the Play permission the service account is given"},
		{"service account", "the Google Cloud account `eas submit` authenticates as"},
		{"given to an agent", "the rule that keeps the service account's key out of every agent's reach"},
		{"eas build --platform android", "the quickest path to an installable APK, stated as the command it is"},
		{"eas env:exec preview", "the environment that command is run with, which is where the package name is"},
		{"[per repo]", "a label saying where a step is done"},
		{"[per Expo account]", "the label for the step done in the owner's own Expo account"},
		{"[per Google account]", "the label for the step done in the owner's Google account"},
		{"[per Play Console]", "the label for the steps done in the Play Console"},
		{"[per Google Cloud, then per Play Console]", "the label for the service account, which is made in one console and linked in the other"},
		{"[per owner's machine]", "the label for the one step that is a command on the owner's own machine"},
		{mobileReleasePinTestName, "the check that pins the staged file"},
		{mobileReleaseReadmeTestName, "the check that reads this section"},
		{"NOT RUN", "the store path's own state: Check 89 has never been run"},
	} {
		if !strings.Contains(section, want.needle) {
			t.Errorf("%s's `%s` section lacks %q (%s): the owner is the one who copies this file in, and this section is where they read it", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading, want.needle, want.why)
		}
	}

	// The `[per …]` labels are the section's claim about where each step is
	// done, so the **set** is checked and not only the three labels the needles
	// above name. A label for an account no step is done under sends a reader
	// looking for a step that is not there -- the Apple side has none of its
	// own here, because the App Store Connect key and the signing certificate
	// are EAS's, server-side, under the owner's own account (the closing
	// paragraph of the owner's section says so). This is the check that would
	// have caught the intro sentence that used to name the owner's Apple
	// account among the places a step is done.
	var labels []string
	for _, label := range mobileReleasePerLabelRE.FindAllString(section, -1) {
		if !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	slices.Sort(labels)
	want := []string{
		"[per Expo account]",
		"[per Google Cloud, then per Play Console]",
		"[per Google account]",
		"[per Play Console]",
		"[per owner's machine]",
		"[per repo, after the first dry run]",
		"[per repo, before every release tag]",
		"[per repo]",
	}
	if !slices.Equal(labels, want) {
		t.Errorf("%s's `%s` section labels its steps %q, and the steps there are done in exactly %q: each label says where a step is done, so a label for an account no step is done under is a step a reader will look for and not find", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading, labels, want)
	}

	// The order the section prescribes is load-bearing, so it is checked as an
	// order and not only as two sentences that exist: the warning about an
	// environment GitHub creates with no reviewer has to come **before** the
	// copy-in command, because the whole point is that the environment exists
	// first. A section that mentioned both, in the other order, would tell the
	// owner to copy the file in and then protect it.
	warn := strings.Index(section, "no required reviewer at all")
	copyIn := strings.Index(section, "cp docs/pending-workflows/mobile-release.yml")
	if warn < 0 || copyIn < 0 || warn > copyIn {
		t.Errorf("%s's `%s` section warns about an environment with no required reviewer at %d and gives the copy-in command at %d: the warning has to come first, because the copy-in is what creates the unprotected environment when it is made before the environment exists", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading, warn, copyIn)
	}

	// And the Android switch before it too, for the reason the environment does:
	// the Android job is enabled by default -- an unset variable names neither
	// platform -- so a copy-in that lands first is a workflow whose first run can
	// build and submit to Play, and the environment's approval click says a
	// person ran the workflow rather than that the Play side is ready. The step
	// is read against the copy-in's position and not merely as a sentence that
	// exists somewhere in the section.
	androidOff := strings.Index(section, mobileReleaseAndroidSwitchStep)
	if androidOff < 0 {
		t.Errorf("%s's `%s` section no longer carries %q: while the Play side is not ready the owner has to set %s before the copy-in, or the first run of the copied-in file can submit to Play", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading, mobileReleaseAndroidSwitchStep, mobileReleaseDisabledPlatforms)
	} else if androidOff > copyIn {
		t.Errorf("%s's `%s` section sets %s at %d and gives the copy-in command at %d: the switch has to come first, because the Android job is enabled by default and a copy-in made before the variable is set is a workflow whose first run can submit to Play", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading, mobileReleaseDisabledPlatforms, androidOff, copyIn)
	}

	// The owner's list counts itself, and the count and the list are one claim:
	// the heading below said "the three things the owner does first" over four
	// numbered steps until it was noticed, which is the drift a count in prose
	// takes on without a test. The steps that are deliberately **not** among
	// them -- the dry-run read and the version bump, each of which has a
	// subsection of its own -- are forwarded to from the paragraph under the
	// list, so the list is what the heading counts and nothing else.
	if at := strings.Index(section, mobileReleaseOwnerStepsHeading); at < 0 {
		t.Errorf("%s's `%s` section no longer carries the heading %q: that heading is where the owner's own list of what has to be in place before the workflow runs is, and this test reads the list under it", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading, mobileReleaseOwnerStepsHeading)
	} else {
		rest := section[at+len(mobileReleaseOwnerStepsHeading):]
		list := rest
		if end := strings.Index(rest, "\n### "); end >= 0 {
			list = rest[:end]
		}
		items := mobileReleaseNumberedItemsRE.FindAllStringSubmatch(list, -1)
		var numbers []string
		for _, item := range items {
			numbers = append(numbers, item[1])
		}
		m := mobileReleaseCountWordRE.FindStringSubmatch(mobileReleaseOwnerStepsHeading)
		if m == nil {
			t.Fatalf("the heading %q no longer states a count this test can read: the heading and the list under it are one claim, and the count is the half that drifts", mobileReleaseOwnerStepsHeading)
		}
		want, ok := mobileReleaseNumberWords[strings.ToLower(m[1])]
		if !ok {
			t.Fatalf("the heading %q counts %q things, which is not a number this test can read", mobileReleaseOwnerStepsHeading, m[1])
		}
		if len(numbers) != want {
			t.Errorf("%s's heading says the owner does %d thing(s) first and the list under it has %d numbered item(s) (%v): the heading and the list are one claim, and a step that is not among them -- the dry-run read, the version bump -- belongs in the paragraph below the list, not under this heading", pendingWorkflowsReadmePath, want, len(numbers), numbers)
		}
		for i, number := range numbers {
			if number != strconv.Itoa(i+1) {
				t.Errorf("%s's owner list numbers its steps %v: the heading counts them and the numbers are read in order, so a list that skips or repeats a number reads as fewer steps than it has", pendingWorkflowsReadmePath, numbers)
				break
			}
		}
	}

	// The section has to name the identity's home rather than leave a reader
	// looking for a secret to create: the identity is in EAS, and a section that
	// is silent about it reads as an omission.
	if !strings.Contains(section, "eas env:exec production") {
		t.Errorf("%s's `%s` section does not say where the store identity comes from (`eas env:exec production`): a reader who cannot find it will look for a secret to create, and there is none", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading)
	}
	// The status line is the machine-readable half of "this file is still
	// pending": it is what the copy-in edits, and a reader who has just copied
	// the file in has to be able to see whether they finished. So it is read
	// against the tree -- the rule TestSharedTestsWorkflowStagingStateIsWhatTheReadmeSays
	// applies to `shared-tests.yml`'s line -- rather than asserted as one fixed
	// sentence: the copy-in removes the staged copy, and a section that kept
	// saying "pending" afterwards would be describing a file that is not there.
	//
	// What is checked is the section's **opening** line, not the section: the
	// copy-in procedure below quotes both spellings of it, so a search over the
	// whole section is satisfied by the quote and would stay green with the live
	// line saying the opposite. While the staged copy exists the file is still
	// pending -- including the half-done copy-in, where both copies are present
	// and the staged one is still the reviewed text, which is the state
	// readMobileReleaseWorkflowAt reads the staged file in.
	body := strings.TrimSpace(section[len(mobileReleaseReadmeHeading):])
	if docExists(t, stagedMobileReleaseWorkflowPath) {
		want := "**Status: `mobile-release.yml` is staged and pending.**"
		if !strings.HasPrefix(body, want) {
			t.Errorf("%s's `%s` section does not open with %s while %s exists: that line is what the copy-in edits, and it has to be the section's opening claim rather than a sentence quoted further down", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading, want, stagedMobileReleaseWorkflowPath)
		}
	} else {
		want := "**Status: `mobile-release.yml` is applied, not pending.**"
		if !strings.HasPrefix(body, want) {
			t.Errorf("%s's `%s` section does not open with %s now that %s is gone and %s exists: the copy-in changes that line in the same commit, and a section still opening with \"staged and pending\" would be describing a file that is not there", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading, want, stagedMobileReleaseWorkflowPath, appliedMobileReleaseWorkflowPath)
		}
	}

	// The pair of sentences the copy-in writes into the opening line and into
	// website/README.md names what the directory is left holding, so it is the
	// pair for making this copy-in first: everything staged beside it except
	// `mobile-release.yml` itself. It is checked in the tree it describes --
	// with the console's two files still staged, which is the tree this file is
	// staged in -- and the section itself says which pair to write in the other
	// order (the one-file pair in `## console-release-linux.yml`, which that
	// section prints under its own label and TestStagedLinuxFilesSurviveTheCopyIn
	// splices in), so a tree where the console's files have already been applied
	// has no pair here to check.
	if docExists(t, consoleLinuxWorkflowPath) && docExists(t, consoleReleaseLinuxWorkflowPath) {
		var want []string
		for _, name := range pendingWorkflows(t) {
			if name != filepath.Base(stagedMobileReleaseWorkflowPath) {
				want = append(want, name)
			}
		}
		for _, pair := range []struct{ what, block, pattern string }{
			{"the opening line", fencedBlockContaining(t, pendingWorkflowsReadmePath, section, "pending: `shared-tests.yml`"), consolePendingBoldPattern},
			{"the site's last paragraph", fencedBlockContaining(t, pendingWorkflowsReadmePath, section, "now holds"), consolePendingPathsPattern},
		} {
			names, count := consolePendingSentence(t, "the pair in `"+mobileReleaseReadmeHeading+"` for "+pair.what, pair.block, pair.pattern)
			slices.Sort(names)
			if !slices.Equal(names, want) {
				t.Errorf("the pair in `%s` for %s names %q, and applying %s leaves the directory holding %q: the sentences are held to the directory, and one that named a file that is not pending would be red in the owner's commit", mobileReleaseReadmeHeading, pair.what, names, stagedMobileReleaseWorkflowPath, want)
			}
			if count != len(want) {
				t.Errorf("the pair in `%s` for %s says %d pending workflow(s), and applying %s leaves %d (%q): the count and the names are one claim", mobileReleaseReadmeHeading, pair.what, count, stagedMobileReleaseWorkflowPath, len(want), want)
			}
		}
	}
}

// TestReadDocsMobileReleaseWorkflowFallsBackOnlyWhilePending pins every mode of the
// read above against temp files, so the rules stay checkable whatever state this
// repository is in -- the staged copy here, the copied-in file there, or both.
// It is what makes the copy-in procedure safe rather than a hope, and the
// applied state is one this repository is never in while an agent is working:
// writing `.github/workflows/mobile-release.yml` is the owner's step.
func TestReadDocsMobileReleaseWorkflowFallsBackOnlyWhilePending(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "pending.yml")
	applied := filepath.Join(dir, "applied.yml")

	// The two fixtures are written so that nothing inside them could say which
	// copy it is: the staged file and the applied one are byte-identical by
	// requirement, so a marker in the text would either be false once it was
	// copied in or make the byte-identity assertion unsatisfiable. Which file
	// was read is reported as a path, and the identical case below pins that.
	pending := "name: mobile-release\non:\n  push:\n"
	copiedIn := "name: mobile-release\non:\n  workflow_dispatch:\n"
	if err := os.WriteFile(staged, []byte(pending), 0o600); err != nil {
		t.Fatal(err)
	}

	// Pending alone: the staged bytes are what is judged, there is nothing to
	// report, and the source names the staged file.
	raw, source, problems, err := readMobileReleaseWorkflowAt(staged, applied)
	if err != nil || len(problems) != 0 {
		t.Fatalf("staged alone: want no problems and no error, got %q and %v", problems, err)
	}
	if string(raw) != pending || source != staged {
		t.Fatalf("staged alone: want the staged bytes from %s, got %q from %s", staged, raw, source)
	}

	// Both, identical: the staged copy still wins, the pair is consistent, and
	// the source is still the staged path -- the case no in-file marker could
	// answer, because the two files are the same bytes.
	if err := os.WriteFile(applied, []byte(pending), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, source, problems, err = readMobileReleaseWorkflowAt(staged, applied)
	if err != nil || len(problems) != 0 || string(raw) != pending || source != staged {
		t.Fatalf("both, identical: want the staged bytes from %s and no problems, got %q from %s, %q, %v", staged, raw, source, problems, err)
	}

	// Both, differing: the copy-in was edited after review, which is the one
	// thing the staged file cannot otherwise notice.
	if err := os.WriteFile(applied, []byte("name: not-the-staged-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, source, problems, err = readMobileReleaseWorkflowAt(staged, applied)
	if err != nil {
		t.Fatalf("both, differing: the staged copy is still readable, so this is a problem and not an error: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "both exist and differ") {
		t.Fatalf("both, differing: want the byte-identity problem, got %q", problems)
	}
	if string(raw) != pending || source != staged {
		t.Fatalf("both, differing: the staged copy must still be what is judged, got %q from %s", raw, source)
	}

	// Copied in and the staged copy removed: the applied file is read alone, its
	// source is the applied path, and it must carry the marker.
	if err := os.WriteFile(applied, []byte(copiedIn), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(staged); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readMobileReleaseWorkflowAt(staged, applied); err == nil {
		t.Fatal("copied in, marker missing: want an error, got none: this is the read that refuses a copy which lost the job")
	}
	if err := os.WriteFile(applied, []byte(copiedIn+mobileReleaseWorkflowMarker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, source, problems, err = readMobileReleaseWorkflowAt(staged, applied)
	if err != nil || len(problems) != 0 {
		t.Fatalf("copied in, marker present: want the applied bytes and no problems, got %q, %q, %v", raw, problems, err)
	}
	if string(raw) != copiedIn+mobileReleaseWorkflowMarker+"\n" || source != applied {
		t.Fatalf("copied in, marker present: want the applied file's bytes from %s, got %q from %s", applied, raw, source)
	}

	// Neither: there is no document to judge, and that is fatal rather than a
	// silently empty run.
	if err := os.Remove(applied); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readMobileReleaseWorkflowAt(staged, applied); err == nil {
		t.Fatal("neither file: want an error, got none")
	}
}
