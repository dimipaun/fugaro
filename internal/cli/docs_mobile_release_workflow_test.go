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
//     pattern the sibling release workflows use; the permissions are
//     `contents: read` and no more; both jobs wait at the `mobile-release`
//     environment; the file names exactly one secret (`EXPO_TOKEN`), carries no
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
	mobileReleaseWorkflowMarker = "eas env:exec production"
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
	// mobileReleaseTagPattern is the tag filter, spelled the way the sibling
	// release workflows spell it (`.github/workflows/release.yml`, `images.yml`,
	// `console-release.yml`). A bare `v*` also starts a release on `vtest` and
	// on a `v1.2.3-rc1` tag, and neither of those is a release of anything.
	mobileReleaseTagPattern = "v[0-9]+.[0-9]+.[0-9]+"
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
	// mobileReleasePinTestName and mobileReleaseReadmeTestName are the two
	// checks this file adds. The document beside the staged file has to name
	// both, so the paragraph that tells a reader what is verified cannot outlive
	// the names it prints.
	mobileReleasePinTestName    = "TestDocsMobileReleaseWorkflowIsStagedAndPinned"
	mobileReleaseReadmeTestName = "TestDocsMobileReleaseStagingTellsTheOwnerWhatToCopyIn"
)

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
	// mobileReleaseDottedRE finds a dotted token of two or more lowercase
	// labels: the shape the store identity has (mobile/README.md: "letters,
	// digits and hyphens in two or more dot-separated segments"). Version
	// comments (`# v7.0.1`) and runner images (`ubuntu-24.04`) are excluded by
	// requiring the LAST label to start with a letter, and the file names the
	// workflow is allowed to mention are excluded by suffix below.
	mobileReleaseDottedRE = regexp.MustCompile(`\b[a-z][a-z0-9-]*(?:\.[a-z0-9-]+)+\b`)
	// mobileReleaseEasCLIRE finds the CLI install and captures whatever version,
	// if any, is pinned to it. Go's regexp has no lookahead, so a bare
	// `eas-cli` is caught by requiring the capture to be exactly `@<version>`
	// rather than by refusing an install without one.
	mobileReleaseEasCLIRE = regexp.MustCompile(`npm install --global eas-cli([^\s]*)`)
)

// mobileReleaseKnownSuffixes are the file extensions a dotted token may end in
// without being mistaken for an identity: `app.json`, `mobile/eas.json`,
// `mobile/package.json`, `mobile/app.config.js`, `images.yml` and the like are
// references to files in this repository, not a bundle identifier.
var mobileReleaseKnownSuffixes = []string{"yml", "yaml", "json", "js", "md", "sh"}

// mobileReleaseAllowedDotted is every other dotted token the file may carry,
// each one a field name or an event-payload path rather than a value. It is
// short on purpose: adding an entry is the deliberate act of saying "this is
// not the store identity", which is what makes the check below worth having.
var mobileReleaseAllowedDotted = []string{
	"engines.node",                  // mobile/package.json's field, named in a comment
	"github.event.inputs",           // the payload path the dry_run conditions read
	"submit.internal.android.track", // the EAS submit field C8 pins, named in a comment
}

// mobileReleaseSummaryRE finds the steps that write the run summary. The build's
// URL has to reach a reader of the run, and `$GITHUB_STEP_SUMMARY` is the only
// place a job can put it.
var mobileReleaseSummaryRE = regexp.MustCompile(`GITHUB_STEP_SUMMARY`)

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
	for name, job := range wf.Jobs {
		if len(job.Permissions) != 1 || job.Permissions["contents"] != "read" {
			t.Errorf("job %q declares permissions %v, not exactly {contents: read}: a job-level block is where a widening hides", name, job.Permissions)
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
	platform  string
	installs  int
	masks     int
	builds    int
	summaries int
	submits   int
	dryRuns   int
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

	var maskAt, buildAt, summaryAt, submitAt = -1, -1, -1, -1
	for i, step := range job.Steps {
		cmd := workflowRunCommands(step.Run)
		switch {
		case strings.HasPrefix(step.Uses, "actions/checkout@"):
			if !strings.Contains(fmt.Sprintf("%v", step.With["persist-credentials"]), "false") {
				t.Errorf("the %s job's checkout keeps the persisted credentials: this job only reads the repository", platform)
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
		case strings.Contains(cmd, "eas build --platform "+platform):
			shape.builds++
			buildAt = i
			if !strings.Contains(cmd, "eas env:exec production") {
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
			if !strings.Contains(cmd, "eas env:exec production") {
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
		runsEas := strings.Contains(workflowRunCommands(step.Run), "eas env:exec production")
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
	// the mask has to be in place before the first command that resolves the
	// app's config, the summary describes the build the step before it made, and
	// the submission submits that same build.
	if shape.installs != 1 || shape.masks != 1 || shape.builds != 1 || shape.summaries != 1 || shape.submits != 1 || shape.dryRuns != 1 {
		t.Errorf("the %s job carries %d install(s), %d mask step(s), %d build(s), %d summary step(s), %d submission(s) and %d dry-run notice(s), not one of each: the job is this platform's whole release", platform, shape.installs, shape.masks, shape.builds, shape.summaries, shape.submits, shape.dryRuns)
	} else if !(maskAt < buildAt && buildAt < summaryAt && summaryAt < submitAt) {
		t.Errorf("the %s job's steps are out of order (mask %d, build %d, summary %d, submission %d): the mask has to run before the first `eas` command that resolves mobile/app.config.js, the summary has to describe the build that was made, and the submission has to follow both", platform, maskAt, buildAt, summaryAt, submitAt)
	}
	if len(shape.uses) != 2 {
		t.Errorf("the %s job uses %d actions (%q); it needs a checkout and a node", platform, len(shape.uses), shape.uses)
	}
	return shape
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
		// end in a digit: neither is a name.
		if len(last) < 2 || last[0] < 'a' || last[0] > 'z' {
			continue
		}
		if slices.Contains(mobileReleaseKnownSuffixes, last) {
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
		{"a clean file", "uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1\nruns-on: ubuntu-24.04\nnode-version-file: mobile/package.json\nsee mobile/app.config.js and docs/pending-workflows/README.md\n", nil},
		{"the three allowed field names", "engines.node github.event.inputs submit.internal.android.track\n", nil},
		{"a version with no comment", "  uses: actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0\n", nil},
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

	for _, want := range []struct{ needle, why string }{
		{"`" + mobileReleaseEnvironment + "`", "the protected environment both jobs wait at, by name"},
		{"required reviewer", "the reviewer whose click is the gate, and that there is one"},
		{"Prevent self-review", "the setting that has to stay off, or the owner's own run can never be approved"},
		{"the file is copied in, not after it", "the order: the environment and the secret exist before the copy-in"},
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
