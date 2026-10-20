package cli

// The staged release workflow for the mobile app (docs/pending-workflows/mobile-release.yml).
//
// The mobile app is its own artifact outside the Go module (mobile plan D125)
// with its own release path, and this is that path: on every `v*` release tag,
// and on a manual dispatch, it builds the iOS app on EAS and submits it to
// TestFlight's internal testers. The owner's rulings of 2026-10-19/20 fix the
// shape: the build runs from GitHub Actions on each tag and by hand; it waits at
// a `mobile-release` GitHub environment with the owner as its required reviewer
// (the same click `console-release` and `website-live` wait for); iOS only for
// now, with the Android job written and disabled until the owner turns it on.
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
//     `v*` tag push and a manual dispatch and nothing else; the permissions are
//     `contents: read` and no more; both jobs wait at the `mobile-release`
//     environment; the file names exactly one secret (`EXPO_TOKEN`) and carries
//     no literal that could be one; every `uses:` is a full 40-hex commit with
//     its `# vX.Y.Z` beside it; the `concurrency` group makes two releases queue
//     instead of submitting at once; the `dry_run` input stops the run before
//     the submission; both `eas` commands run under `eas env:exec production`
//     with `--non-interactive`; the build's URL reaches the job summary; the
//     Android job is disabled behind the repository variable; and **neither the
//     store identity's variable names nor a reverse-DNS-shaped value appears
//     anywhere in the file**.
//   - TestDocsMobileReleaseStagingTellsTheOwnerWhatToCopyIn: the
//     section beside the staged file has to carry what a human cannot guess --
//     the environment's name and its required reviewer, the secret's name, the
//     copy-in command, and the two follow-up edits a copy-in needs.
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
	// mobileReleaseAndroidSwitch is the repository variable that turns the
	// Android job on. It is a variable and not a workflow edit on purpose: an
	// agent cannot edit a workflow, so an owner-facing switch has to be a
	// setting.
	mobileReleaseAndroidSwitch = "MOBILE_ANDROID_ENABLED"
	// mobileReleasePinTestName and mobileReleaseReadmeTestName are the two
	// checks this file adds. The document beside the staged file has to name
	// both, so the paragraph that tells a reader what is verified cannot outlive
	// the names it prints.
	mobileReleasePinTestName    = "TestDocsMobileReleaseWorkflowIsStagedAndPinned"
	mobileReleaseReadmeTestName = "TestDocsMobileReleaseStagingTellsTheOwnerWhatToCopyIn"
)

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
	Name             string         `yaml:"name"`
	If               string         `yaml:"if"`
	Uses             string         `yaml:"uses"`
	Run              string         `yaml:"run"`
	WorkingDirectory string         `yaml:"working-directory"`
	With             map[string]any `yaml:"with"`
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
	// A `v*` tag push and a manual dispatch, and nothing else. The decode above
	// refuses a third trigger; these assertions refuse a weakened tag filter,
	// which is the half that decides whether the release runs at all.
	if wf.On.Push == nil || wf.On.WorkflowDispatch == nil {
		t.Fatalf("%s must trigger on both a `v*` tag push and a manual dispatch (push %v, workflow_dispatch %v)", path, wf.On.Push != nil, wf.On.WorkflowDispatch != nil)
	}
	if !slices.Equal(wf.On.Push.Tags, []string{"v*"}) {
		t.Errorf("the push trigger is for tags %q, not [v*]: this is the mobile app's release, and the CLI's tag is what a release is", wf.On.Push.Tags)
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
	// second secret here.
	refs := mobileReleaseSecretRE.FindAllStringSubmatch(text, -1)
	for _, ref := range refs {
		if ref[1] != mobileReleaseSecretName {
			t.Errorf("%s reads `secrets.%s`: the Expo access token is the only credential this job needs, and every extra reference is a credential reachable from a public repository", path, ref[1])
		}
	}
	if len(refs) != len(wf.Jobs) {
		t.Errorf("%s reads `secrets.%s` %d times for %d jobs (%v): each job binds the one secret once at job level, and a reference in a step is a second place it can be reached from", path, mobileReleaseSecretName, len(refs), len(wf.Jobs), keys(wf.Jobs))
	}
	for _, lit := range []string{"-----BEGIN", "PRIVATE KEY", "private_key", "password:", "ascAppId"} {
		if strings.Contains(text, lit) {
			t.Errorf("%s carries %q, which is the shape of a literal secret or a value bound by hand", path, lit)
		}
	}

	// --- The identity is not here -------------------------------------------
	// The iOS bundle identifier and the Android package name live in the EAS
	// project's own environment, and the two commands that need them load that
	// environment with `eas env:exec production`. Neither the variables' names
	// nor a reverse-DNS-shaped value may appear: the identity is the owner's and
	// this repository carries it nowhere (mobile plan D125, mobile/README.md).
	for _, name := range []string{"FUGARO_IOS_BUNDLE_ID", "FUGARO_ANDROID_PACKAGE", "bundleIdentifier", "android.package"} {
		if strings.Contains(text, name) {
			t.Errorf("%s names %q: the store identity is read from the EAS project's environment and appears nowhere in this repository, so the file cannot name it either", path, name)
		}
	}
	for _, token := range mobileReleaseIdentityShapedTokens(text) {
		t.Errorf("%s carries the reverse-DNS-shaped token %q, which is the shape of the store identity: the identity is set in EAS by the owner and read through `eas env:exec production`, and a value here is a value in a public repository", path, token)
	}

	// --- The two jobs -------------------------------------------------------
	if got := keys(wf.Jobs); !slices.Equal(got, []string{"android", "ios"}) {
		t.Fatalf("%s carries the jobs %v, not [android ios]: one platform's release is one job", path, got)
	}

	ios, ok := wf.Jobs["ios"]
	if !ok {
		t.Fatalf("%s carries no `ios` job: %v", path, keys(wf.Jobs))
	}
	// The environment is the gate: GitHub holds the job until its only required
	// reviewer approves it, however the run was started.
	if ios.Environment != mobileReleaseEnvironment {
		t.Errorf("the ios job's environment is %q, not %q: the environment's required reviewer is the whole of what stops a store submission, and a job without it starts as soon as its trigger fires", ios.Environment, mobileReleaseEnvironment)
	}
	if ios.TimeoutMinutes <= 0 {
		t.Errorf("the ios job has timeout-minutes %d: a run that waits on a remote build without a bound is a job nobody can tell from a hung one", ios.TimeoutMinutes)
	}
	if !strings.HasPrefix(ios.RunsOn, "ubuntu-") {
		t.Errorf("the ios job runs on %q: the build itself runs on EAS's workers, so the runner is only the client that uploads it", ios.RunsOn)
	}
	// The one secret is bound once, at job level, and read from the
	// environment's own secret store rather than the repository's.
	if got := ios.Env[mobileReleaseSecretName]; !strings.Contains(got, "secrets."+mobileReleaseSecretName) {
		t.Errorf("the ios job's %s is %q, not `${{ secrets.%s }}`: the token is bound once at job level and never written down", mobileReleaseSecretName, got, mobileReleaseSecretName)
	}
	for _, name := range []string{"FUGARO_IOS_BUNDLE_ID", "FUGARO_ANDROID_PACKAGE"} {
		if _, ok := ios.Env[name]; ok {
			t.Errorf("the ios job binds %s in its env: the identity comes from the EAS project's environment through `eas env:exec production`, not from a GitHub secret or variable", name)
		}
	}

	// The iOS steps, in order: checkout, setup-node, install eas-cli, build,
	// summary, submit, and the dry-run notice. The order matters -- the summary
	// has to describe the build the step before it waited for, and the
	// submission has to follow both. Every predicate below reads the step's
	// commands rather than its whole `run:` block: the file's comments name
	// `eas env:exec production`, `--wait` and the rest of what is checked here,
	// and a comment must not be able to satisfy a check on a command.
	var installs, builds, summaries, submits, dryRuns int
	for i, step := range ios.Steps {
		cmd := workflowRunCommands(step.Run)
		switch {
		case strings.HasPrefix(step.Uses, "actions/checkout@"):
			if !strings.Contains(fmt.Sprintf("%v", step.With["persist-credentials"]), "false") {
				t.Error("the checkout keeps the persisted credentials: this job only reads the repository")
			}
		case strings.HasPrefix(step.Uses, "actions/setup-node@"):
			// The Node line comes from the app's own manifest, the way
			// console-linux.yml takes the console's: a second copy written here
			// is a pin that can disagree with the app's.
			if got := fmt.Sprintf("%v", step.With["node-version-file"]); got != "mobile/package.json" {
				t.Errorf("setup-node's node-version-file is %q, not mobile/package.json: the app's own engines.node is the pin", got)
			}
		case strings.Contains(cmd, "npm install --global eas-cli"):
			installs++
		case strings.Contains(cmd, "eas build --platform ios"):
			builds++
			if !strings.Contains(cmd, "eas env:exec production") {
				t.Errorf("ios step %d builds without `eas env:exec production`: `eas-cli` resolves mobile/app.config.js locally before it uploads anything, and the bundle identifier comes from the EAS project's environment", i)
			}
			if !strings.Contains(cmd, "--non-interactive") {
				t.Errorf("ios step %d builds without `--non-interactive`: a build that can wait for a prompt in CI is a build that hangs", i)
			}
			if !strings.Contains(cmd, "--wait") {
				t.Errorf("ios step %d builds without `--wait`: the CLI would queue the build and return, and the `--latest` submission below would submit whatever was finished before it", i)
			}
		case mobileReleaseSummaryRE.MatchString(cmd) && strings.Contains(cmd, "jq"):
			summaries++
			if !strings.Contains(cmd, "artifacts.buildUrl") {
				t.Errorf("ios step %d writes a summary without the build's URL: the summary is the one place a run can carry the link a reviewer clicks", i)
			}
			if !strings.Contains(cmd, "eas build:list") {
				t.Errorf("ios step %d writes a summary without reading the build back: `eas build:list --json` is the shape docs/gcp-live-checklist.md's Check 89 reads a build with", i)
			}
		case strings.Contains(cmd, "eas submit ") && strings.Contains(cmd, "--platform ios"):
			submits++
			if !strings.Contains(cmd, "eas env:exec production") {
				t.Errorf("ios step %d submits without `eas env:exec production`: the local `eas-cli` process needs the bundle identifier the environment carries, or it stops before it uploads anything", i)
			}
			if !strings.Contains(cmd, "--non-interactive") {
				t.Errorf("ios step %d submits without `--non-interactive`", i)
			}
			if got := step.If; !strings.Contains(got, "github.event.inputs.dry_run") || !strings.Contains(got, "!=") {
				t.Errorf("ios step %d submits with `if: %s`, which is not the dry-run gate: the submission is what `dry_run` stops before", i, got)
			}
		case mobileReleaseSummaryRE.MatchString(cmd) && strings.Contains(cmd, "Dry run"):
			dryRuns++
			if got := step.If; !strings.Contains(got, "github.event.inputs.dry_run") || !strings.Contains(got, "==") {
				t.Errorf("ios step %d is the dry-run notice with `if: %s`, which does not read the input", i, got)
			}
		}
	}
	for _, c := range []struct {
		what string
		got  int
	}{
		{"a step installing eas-cli", installs},
		{"a step building on EAS", builds},
		{"a step writing the summary", summaries},
		{"a step submitting", submits},
		{"a step saying it was dry", dryRuns},
	} {
		if c.got != 1 {
			t.Errorf("the ios job carries %d of %s, not one", c.got, c.what)
		}
	}
	if uses := workflowJobUses(ios); len(uses) != 2 {
		t.Errorf("the ios job uses %d actions (%q); it needs a checkout and a node", len(uses), uses)
	}

	// --- The Android job, written and disabled ------------------------------
	android, ok := wf.Jobs["android"]
	if !ok {
		t.Fatalf("%s carries no `android` job: it is written and disabled, so that turning it on is one repository setting rather than a workflow edit", path)
	}
	if got := android.If; !strings.Contains(got, "vars."+mobileReleaseAndroidSwitch) || !strings.Contains(got, "'true'") {
		t.Errorf("the android job's `if` is %q, not a gate on the repository variable %s being exactly true: an unset variable has to skip the job rather than run it, and a job that can run by default is not disabled", got, mobileReleaseAndroidSwitch)
	}
	if android.Environment != mobileReleaseEnvironment {
		t.Errorf("the android job's environment is %q, not %q: when the owner turns it on, the store submission it makes needs the same approval the iOS one waits for", android.Environment, mobileReleaseEnvironment)
	}
	if got := android.Env[mobileReleaseSecretName]; !strings.Contains(got, "secrets."+mobileReleaseSecretName) {
		t.Errorf("the android job's %s is %q, not `${{ secrets.%s }}`", mobileReleaseSecretName, got, mobileReleaseSecretName)
	}
	var androidBuilds, androidSubmits int
	for _, step := range android.Steps {
		cmd := workflowRunCommands(step.Run)
		switch {
		case strings.Contains(cmd, "eas build --platform android"):
			androidBuilds++
			if !strings.Contains(cmd, "eas env:exec production") || !strings.Contains(cmd, "--non-interactive") {
				t.Error("the android job's build does not run under `eas env:exec production` with `--non-interactive`")
			}
		case strings.Contains(cmd, "eas submit ") && strings.Contains(cmd, "--platform android"):
			androidSubmits++
			if got := step.If; !strings.Contains(got, "github.event.inputs.dry_run") {
				t.Errorf("the android job submits with `if: %s`: the dry-run gate has to cover it too, or turning it on makes `dry_run` mean one platform", got)
			}
		}
	}
	if androidBuilds != 1 || androidSubmits != 1 {
		t.Errorf("the android job carries %d builds and %d submissions, not one of each", androidBuilds, androidSubmits)
	}

	// --- The pins -----------------------------------------------------------
	// Every action by full commit SHA with its version beside it: a floating ref
	// (`@v7`) is a mutable tag on someone else's repository, and this job holds
	// a store credential.
	allUses := append(workflowJobUses(ios), workflowJobUses(android)...)
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
// required reviewer, the secret's name, the copy-in command, the switch that
// turns the Android job on, the two follow-up edits a copy-in needs, and the
// name of the check that pins the file -- so a rename cannot leave the document
// pointing at a test that is gone.
func TestDocsMobileReleaseStagingTellsTheOwnerWhatToCopyIn(t *testing.T) {
	section := mobileReleaseReadmeSection(t)

	for _, want := range []struct{ needle, why string }{
		{"`" + mobileReleaseEnvironment + "`", "the protected environment both jobs wait at, by name"},
		{"required reviewer", "the reviewer whose click is the gate, and that there is one"},
		{"Prevent self-review", "the setting that has to stay off, or the owner's own run can never be approved"},
		{"`" + mobileReleaseSecretName + "`", "the one secret the workflow reads, by name and never by value"},
		{"gh secret set " + mobileReleaseSecretName, "the command that creates the secret, with the environment it belongs on"},
		{"gh api --method PUT \"repos/$REPO/environments/" + mobileReleaseEnvironment + "\"", "the command that creates the environment"},
		{"cp docs/pending-workflows/mobile-release.yml .github/workflows/mobile-release.yml", "the copy-in command"},
		{"git rm docs/pending-workflows/mobile-release.yml", "the removal of the staged copy, which is load-bearing rather than tidy"},
		{"website/README.md", "the first of the two follow-up edits a copy-in needs"},
		{"opening line", "the second: this document's own opening line counts and names the pending files"},
		{"`" + mobileReleaseAndroidSwitch + "`", "the repository variable that turns the Android job on, by name"},
		{"[per repo]", "a label saying where a step is done"},
		{"[per Expo account]", "the label for the step done in the owner's own Expo account"},
		{mobileReleasePinTestName, "the check that pins the staged file"},
		{mobileReleaseReadmeTestName, "the check that reads this section"},
		{"eas-cli", "the one unpinned thing in the file, named rather than left to be discovered"},
		{"NOT RUN", "the store path's own state: Check 89 has never been run"},
	} {
		if !strings.Contains(section, want.needle) {
			t.Errorf("%s's `%s` section lacks %q (%s): the owner is the one who copies this file in, and this section is where they read it", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading, want.needle, want.why)
		}
	}

	// The section has to name the identity's home rather than leave a reader
	// looking for a secret to create: the identity is in EAS, and a section that
	// is silent about it reads as an omission.
	if !strings.Contains(section, "eas env:exec production") {
		t.Errorf("%s's `%s` section does not say where the store identity comes from (`eas env:exec production`): a reader who cannot find it will look for a secret to create, and there is none", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading)
	}
	// The status line is the machine-readable half of "this file is still
	// pending": the copy-in changes it, and a reader who has just copied the
	// file in has to be able to see whether they finished.
	if !strings.Contains(section, "**Status: `mobile-release.yml` is staged and pending.**") {
		t.Errorf("%s's `%s` section does not open its status as `**Status: `mobile-release.yml` is staged and pending.**`: that line is what the copy-in edits, and the tests above read the staged copy while it exists", pendingWorkflowsReadmePath, mobileReleaseReadmeHeading)
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
