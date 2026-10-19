package cli

// The staged CI job for the shared package's tests (mobile plan, Task P4.3).
//
// shared/ (P0.1-P1.5) is a second artifact outside the Go module -- `go test
// ./...`, `go vet` and `gofmt -l .` take no notice of it -- so nothing on
// `main` runs its tests. P4.3 is the job that would, and it is **owner-made**:
// no agent may write or edit `.github/workflows/**` (the mobile plan's Global
// Constraint 8; GitHub refuses Fugaro's push of a workflow change), so the
// file is staged as docs/pending-workflows/shared-tests.yml for the owner to
// copy in by hand, the way images.yml is staged in this directory.
//
// "Staged for a human to copy" is only safe if the staged bytes are pinned by
// something a human cannot forget to update, so this file holds the staged
// YAML to the properties that make it harmless to run on a public repository:
//
//   - TestSharedTestsWorkflowIsStagedAndPinned: the triggers are a `main` push
//     and a `main` pull request filtered to `shared/**` and the workflow's own
//     path, and nothing else; the permissions are `contents: read` and no
//     more; every `uses:` is a full 40-hex commit with its `# vX.Y.Z` beside
//     it; the test script runs on both Node lines the job declares; the
//     package's mutation proofs are run; the file references no secret and
//     carries no literal that could be one; and `pull_request_target` appears
//     nowhere in it.
//   - TestSharedTestsWorkflowPinsAgreeWithTheOtherWorkflows: once the copy has
//     been made and the staged file removed, the pins cannot drift from the
//     other workflows' pins of the same actions (while it is pending they can:
//     Dependabot does not watch docs/pending-workflows/, so the person copying
//     it compares, as the README beside it says).
//   - TestSharedTestsWorkflowStagingStateIsWhatTheReadmeSays: the README beside
//     the file says the file is still pending, and that sentence is false the
//     moment the owner copies it in -- so the copy-in steps update the README,
//     and this is what makes that step load-bearing rather than tidy.
//   - TestSharedTestsWorkflowReadmeNamesEveryProofScript: the README lists the
//     package's mutation-proof scripts by name while the job globs for them, so
//     the list is held to the tree instead of to whoever last edited it.
//
// What this test cannot do is run the job. Nothing here executes GitHub
// Actions, so the job's own behaviour in CI -- the two Node legs, the install,
// the proofs -- is unverified until the owner copies the file in and pushes.
// The one thing that *can* be checked about the versions is checked: the
// runner commands in the file are the ones this repository uses everywhere
// else (`npm ci --ignore-scripts --no-audit --no-fund`), and the version
// matrix names the two lines the plan asks for.

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
	// stagedSharedTestsWorkflowPath is the file this task writes: the whole
	// workflow, in the directory an agent is allowed to write.
	stagedSharedTestsWorkflowPath = "docs/pending-workflows/shared-tests.yml"
	// realSharedTestsWorkflowPath is where the owner copies it to. This test
	// reads which of the two exists rather than assuming either: while the
	// file is pending, the staged copy is what is checked; once it is copied
	// in and the staged copy removed, the real file must stand on its own.
	realSharedTestsWorkflowPath = ".github/workflows/shared-tests.yml"
	// sharedTestsWorkflowMarker is what the real file must contain once the
	// staged copy is gone: the job's own name, so a copy that landed empty, or
	// a file renamed over it, is refused rather than passing vacuously.
	sharedTestsWorkflowMarker = "shared/*/mutation-proof.sh"
	// pendingWorkflowsReadmePath is the document beside the staged file: where
	// a human reads what is still waiting to be copied in, and therefore where
	// the copy-in has to be recorded once it has happened.
	pendingWorkflowsReadmePath = "docs/pending-workflows/README.md"
	// sharedTestsReadmeHeading opens that document's section on this workflow.
	sharedTestsReadmeHeading = "## `shared-tests.yml`"
	// sharedTestsStagedState and sharedTestsAppliedState are the two halves of
	// the README's status line: the words the copy-in changes, and the whole of
	// what the check on that line reads.
	sharedTestsStagedState  = "staged and pending"
	sharedTestsAppliedState = "applied, not pending"
	// sharedTestsPendingClaim is the phrase in the README's opening line that
	// says which file is waiting to be copied in. The line itself is prose and
	// is not parsed; this one phrase is what the copy-in has to remove.
	sharedTestsPendingClaim = "pending: `shared-tests.yml`"
	// sharedTestsStagingEnforcer is the test that holds that document to the
	// tree. The document has to name it, and this is the name.
	sharedTestsStagingEnforcer = "TestSharedTestsWorkflowStagingStateIsWhatTheReadmeSays"
)

// sharedTestsStagingStatusRE reads the one machine-readable line in the README
// that says whether this file is still pending. It is machine-readable on
// purpose: the state it describes is a fact about the tree -- does the staged
// copy exist? -- and a document that restates a fact in prose is exactly what
// goes stale, which is what happened here. The copy-in steps change this line
// and the document's opening line, and nothing else in the prose.
var sharedTestsStagingStatusRE = regexp.MustCompile("\\*\\*Status: `shared-tests.yml` is (staged and pending|applied, not pending)\\.?\\*\\*")

// sharedTestsProofScriptRE finds a concrete proof-script path in the README --
// `shared/<module>/mutation-proof.sh`, with a real module name, so the glob
// form the job uses (`shared/*/mutation-proof.sh`) is not a match and the list
// the test compares is one of actual scripts.
var sharedTestsProofScriptRE = regexp.MustCompile(`shared/[a-z0-9-]+/mutation-proof\.sh`)

// sharedTestsWorkflow is docs/pending-workflows/shared-tests.yml, decoded with
// unknown fields refused: a key nobody reads is a key nobody pins. `on` is
// spelled out as two named triggers rather than a map, so a third trigger --
// `workflow_dispatch`, `workflow_run`, `pull_request_target` -- is a decode
// error here rather than a line the assertions below have to remember to
// refuse.
type sharedTestsWorkflow struct {
	Name        string                      `yaml:"name"`
	On          sharedTestsWorkflowTriggers `yaml:"on"`
	Permissions map[string]string           `yaml:"permissions"`
	Concurrency struct {
		Group            string `yaml:"group"`
		CancelInProgress bool   `yaml:"cancel-in-progress"`
	} `yaml:"concurrency"`
	Jobs map[string]sharedTestsJob `yaml:"jobs"`
}

type sharedTestsWorkflowTriggers struct {
	Push        *sharedTestsWorkflowTrigger `yaml:"push"`
	PullRequest *sharedTestsWorkflowTrigger `yaml:"pull_request"`
}

type sharedTestsWorkflowTrigger struct {
	Branches []string `yaml:"branches"`
	Paths    []string `yaml:"paths"`
}

type sharedTestsJob struct {
	RunsOn         string            `yaml:"runs-on"`
	TimeoutMinutes int               `yaml:"timeout-minutes"`
	Permissions    map[string]string `yaml:"permissions"`
	Strategy       struct {
		FailFast bool `yaml:"fail-fast"`
		Matrix   struct {
			Node []string `yaml:"node"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []sharedTestsStep `yaml:"steps"`
}

type sharedTestsStep struct {
	Name             string            `yaml:"name"`
	Uses             string            `yaml:"uses"`
	Run              string            `yaml:"run"`
	WorkingDirectory string            `yaml:"working-directory"`
	With             map[string]any    `yaml:"with"`
	Env              map[string]string `yaml:"env"`
}

// readSharedTestsWorkflow returns the workflow's bytes and the path they came
// from, reading the staged copy while it exists and the real file after the
// copy-in.
func readSharedTestsWorkflow(t *testing.T) (sharedTestsWorkflow, []byte, string) {
	t.Helper()
	raw, source, problems, err := readSharedTestsWorkflowAt(
		"../../"+stagedSharedTestsWorkflowPath, "../../"+realSharedTestsWorkflowPath)
	for _, p := range problems {
		t.Error(p)
	}
	if err != nil {
		t.Fatal(err)
	}
	var wf sharedTestsWorkflow
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&wf); err != nil {
		t.Fatalf("decode %s: %v", source, err)
	}
	return wf, raw, source
}

// readSharedTestsWorkflowAt is the read the test above uses, with its two
// paths as arguments and its verdicts returned rather than reported: that is
// what lets TestReadSharedTestsWorkflowFallsBackOnlyWhilePending pin both
// modes against temp files, independently of what this repository currently
// holds -- the same shape images/base_workflow_test.go's readWorkflow uses.
//
// The rules, in the order they are decided:
//
//   - The staged copy wins outright while it exists. It is the file a human
//     reviews, so it is the file the assertions judge.
//   - While both exist they must be byte-identical, and that is the weaker
//     half of the pair on purpose: it says the file under `.github/workflows/`
//     is the reviewed staged one rather than something hand-written, which is
//     what makes reviewing the staged copy worth anything. It is deliberately
//     not an assertion that the real file is *absent*: Task P4.3's whole point
//     is that the owner copies it in, so absence would turn this test red the
//     moment they did.
//   - Once the staged copy is gone the real file must carry the marker, so a
//     copy that lost the job -- or a file renamed over it -- is refused here
//     rather than passing vacuously. This is also why the README's copy-in
//     procedure removes the staged copy: left in place, it would freeze these
//     assertions on its own bytes and hide every later edit to the real file.
//
// source is which of the two copies was read, so that a red run says which file
// it judged. It is the path the bytes actually came from and never a property
// of their content: the two copies are byte-identical by requirement, so no
// banner or other marker inside the file could tell them apart.
//
// problems are reported and the read continues; err is fatal, because there
// is then no document to judge.
func readSharedTestsWorkflowAt(stagedPath, realPath string) (raw []byte, source string, problems []string, err error) {
	staged, stagedErr := os.ReadFile(stagedPath)
	real, realErr := os.ReadFile(realPath)
	switch {
	case stagedErr == nil && realErr == nil:
		raw, source = staged, stagedPath
		if !bytes.Equal(staged, real) {
			problems = append(problems, fmt.Sprintf(
				"%s and %s both exist and differ: the copied-in file is not the reviewed staged one (copy the file in and `git rm` the staged copy, as docs/pending-workflows/README.md says -- until it is removed, nothing here can see an edit to the real file)",
				realPath, stagedPath))
		}
	case stagedErr == nil:
		raw, source = staged, stagedPath
	case os.IsNotExist(stagedErr) && realErr == nil:
		raw, source = real, realPath
		if !strings.Contains(string(real), sharedTestsWorkflowMarker) {
			return nil, "", problems, fmt.Errorf("%s does not contain %q and %s no longer exists: was the staged file removed without its content reaching the real file?", realPath, sharedTestsWorkflowMarker, stagedPath)
		}
	default:
		return nil, "", problems, fmt.Errorf("neither %s nor %s is readable: %v", stagedPath, realPath, stagedErr)
	}
	return raw, source, problems, nil
}

// usesRE and shaRE are the pin check's whole vocabulary: a `uses:` line -- the
// action and its ref, then the `# vX.Y.Z` comment that makes the SHA readable,
// both required by the `$` anchor so a trailing word cannot slip past -- and
// the 40-hex commit a ref must be.
var (
	sharedTestsUsesRE = regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*(\S+)(?:\s+#\s*v(\S+))?\s*$`)
	sharedTestsShaRE  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// TestSharedTestsWorkflowIsStagedAndPinned: the staged job is safe to copy in.
// Every assertion below is a property a later edit could silently drop, and
// each was planted and seen red while this test was written.
func TestSharedTestsWorkflowIsStagedAndPinned(t *testing.T) {
	wf, raw, path := readSharedTestsWorkflow(t)
	text := string(raw)

	if wf.Name != "shared-tests" {
		t.Errorf("%s is named %q; the job's name is what a branch protection rule and a badge would name", path, wf.Name)
	}

	// --- The trigger surface ------------------------------------------------
	// A `main` push and a `main` pull request, filtered to the package and to
	// this file. The decode above refuses a third trigger; these assertions
	// refuse a weakened or missing filter, which is the half that decides
	// whether the job runs on a change that cannot affect it.
	if wf.On.Push == nil || wf.On.PullRequest == nil {
		t.Fatalf("%s must trigger on both a push and a pull request to main (push %v, pull_request %v)", path, wf.On.Push != nil, wf.On.PullRequest != nil)
	}
	wantPaths := []string{"shared/**", ".github/workflows/shared-tests.yml"}
	for _, tr := range []struct {
		what string
		tr   *sharedTestsWorkflowTrigger
	}{{"push", wf.On.Push}, {"pull_request", wf.On.PullRequest}} {
		if !slices.Equal(tr.tr.Branches, []string{"main"}) {
			t.Errorf("the %s trigger is for branches %q, not [main]: this job is about main's shared package", tr.what, tr.tr.Branches)
		}
		if !slices.Equal(sortedCopy(tr.tr.Paths), sortedCopy(wantPaths)) {
			t.Errorf("the %s trigger's path filter is %q, not %q: without the workflow's own path a change to the job is the one change it does not run on, and without any filter it runs on every unrelated push", tr.what, tr.tr.Paths, wantPaths)
		}
	}
	// The brief's own named refusal, scanned in the bytes as well as decoded:
	// `pull_request_target` runs with a read/write token and the repository's
	// secrets even for a fork's pull request, which is exactly what this job
	// must never be. It is asserted over the text so it cannot be smuggled in
	// as a third trigger key, a job-level `if:` or a comment's instruction.
	for _, banned := range []string{"pull_request_target", "workflow_run", "write-all"} {
		if strings.Contains(text, banned) {
			t.Errorf("%s carries %q; this job reads the repository, writes nothing and must not be reachable with a write token or a secret", path, banned)
		}
	}

	// --- Permissions --------------------------------------------------------
	// `contents: read` at the top, so no job can widen it by omission, and no
	// scope beside it.
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("%s's workflow-level permissions are %v, not exactly {contents: read}: the package's tests read the repository and write nothing", path, wf.Permissions)
	}
	for name, job := range wf.Jobs {
		if len(job.Permissions) != 0 {
			t.Errorf("job %q declares permissions %v; the workflow-level `contents: read` is the whole grant this job needs, and a job-level block is where a widening hides", name, job.Permissions)
		}
	}

	// --- No secrets ---------------------------------------------------------
	// Nothing here needs one: the package has no dependencies, no network use
	// and no credential, and a secret referenced by a job that does not need
	// it is a secret in a public repository's log.
	if strings.Contains(text, "secrets.") {
		t.Errorf("%s references a secret; this job needs none, and the reference itself is what would make one reachable here", path)
	}
	for _, lit := range []string{"-----BEGIN", "private_key", "password:"} {
		if strings.Contains(text, lit) {
			t.Errorf("%s carries %q, which is the shape of a literal secret", path, lit)
		}
	}

	// --- Concurrency --------------------------------------------------------
	// One run per ref, the newer superseding the older: two pushes to one
	// branch must not both finish.
	if !strings.Contains(wf.Concurrency.Group, "github.ref") {
		t.Errorf("the concurrency group is %q, which is not keyed on the ref: two branches would cancel each other's runs", wf.Concurrency.Group)
	}
	if !wf.Concurrency.CancelInProgress {
		t.Error("concurrency.cancel-in-progress is not true: a superseded run would keep running beside its replacement")
	}

	// --- The job, the two Node lines, and the steps -------------------------
	if len(wf.Jobs) != 1 {
		t.Fatalf("%s has %d jobs; the shared package's tests are one job with a version matrix", path, len(wf.Jobs))
	}
	job, ok := wf.Jobs["shared"]
	if !ok {
		t.Fatalf("%s carries no `shared` job: %v", path, keys(wf.Jobs))
	}
	if got := sortedCopy(job.Strategy.Matrix.Node); !slices.Equal(got, []string{"22", "26"}) {
		t.Errorf("the job's node matrix is %q, not [22 26]: 22 is the line shared/package.json declares and 26 is the current line, and dropping either makes the other's result the only one this job ever reports", got)
	}
	if job.Strategy.FailFast {
		t.Error("strategy.fail-fast is true: the first Node leg to fail would cancel the other, so the job would report one runner's answer instead of two")
	}

	// The steps, in order: checkout, setup-node, install, test, proofs. The
	// order matters -- the proofs mutate the package's own source with `git
	// checkout` to restore it, so they run after the suite, not beside it.
	var uses []string
	var installs, tests, proofs int
	for i, step := range job.Steps {
		if step.Uses != "" {
			uses = append(uses, step.Uses)
		}
		switch step.Run {
		case "npm ci --ignore-scripts --no-audit --no-fund":
			installs++
			if step.WorkingDirectory != "shared" {
				t.Errorf("step %d installs with working-directory %q, not shared: `npm ci` outside the package installs nothing it needs", i, step.WorkingDirectory)
			}
		case "npm test":
			tests++
			if step.WorkingDirectory != "shared" {
				t.Errorf("step %d runs the suite with working-directory %q, not shared: `npm test` runs the package's script, and that is the directory it is relative to", i, step.WorkingDirectory)
			}
			if i == 0 || job.Steps[i-1].Run != "npm ci --ignore-scripts --no-audit --no-fund" {
				t.Error("the suite does not follow the install: the package's lockfile is committed and `npm ci` is what proves it is installable")
			}
		default:
			if strings.Contains(step.Run, sharedTestsWorkflowMarker) {
				proofs++
				if !strings.Contains(step.Run, `bash "$p"`) {
					t.Error("the mutation-proof step names the scripts but does not run each one: a listed script that is never executed is a proof that never happened")
				}
				if !strings.Contains(step.Run, "exit 1") {
					t.Error("the mutation-proof step does not fail when the glob matches nothing: `nullglob` would leave an empty list and the step would pass having run nothing")
				}
				if i != len(job.Steps)-1 {
					t.Errorf("the mutation-proof step is step %d of %d; it restores the package's source with `git checkout` after each mutation, so it goes last, where nothing runs beside it", i, len(job.Steps))
				}
			}
		}
	}
	if installs != 1 {
		t.Errorf("%s carries %d `npm ci ...` install steps, not one", path, installs)
	}
	if tests != 1 {
		t.Errorf("%s carries %d `npm test` steps, not one", path, tests)
	}
	if proofs != 1 {
		t.Errorf("%s carries %d steps running the package's mutation proofs, not one", path, proofs)
	}
	for _, banned := range []string{"npm install", "npx "} {
		if strings.Contains(text, banned) {
			t.Errorf("%s carries %q: the package has no dependencies, so an install that resolves them at run time is an unpinned one", path, banned)
		}
	}

	// --- The pins -----------------------------------------------------------
	// Every action by full commit SHA with its version beside it, and the two
	// steps that need one are exactly the two that are there: a floating ref
	// (`@v7`) is a mutable tag on someone else's repository, and this job's
	// whole surface is what runs on a public repository's main branch.
	if len(uses) != 2 {
		t.Errorf("%s uses %d actions (%q); the job needs a checkout and a node", path, len(uses), uses)
	}
	pins := sharedTestsUsesRE.FindAllStringSubmatch(text, -1)
	if len(pins) != len(uses) {
		t.Errorf("%s has %d `uses:` steps but %d match the pinned form `uses: <action>@<sha> # v<version>`: an unpinned or oddly written one is listed here rather than judged", path, len(uses), len(pins))
	}
	for _, m := range pins {
		action, ref, version := m[1], "", m[2]
		if i := strings.LastIndex(m[1], "@"); i < 0 {
			t.Errorf("uses %q has no version at all: the action is not pinned to anything", m[1])
			continue
		} else {
			action, ref = m[1][:i], m[1][i+1:]
		}
		if !sharedTestsShaRE.MatchString(ref) {
			t.Errorf("uses %s@%s is not pinned by a full commit SHA: a tag is a mutable name on a repository this one does not control", action, ref)
		}
		if version == "" {
			t.Errorf("uses %s@%s carries no `# vX.Y.Z` beside it: a bare SHA is a pin nobody can read or compare", action, ref)
		}
	}
	for _, want := range []string{"actions/checkout@", "actions/setup-node@"} {
		found := false
		for _, u := range uses {
			if strings.HasPrefix(u, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s never uses %s: the job cannot check out the package or select a node line without it", path, want)
		}
	}
	// ... and setup-node must take its version from the matrix, not a literal:
	// a literal beside a matrix means one of the two legs is not the line it
	// claims to be.
	var sawMatrixVersion bool
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, "actions/setup-node@") {
			if got := fmt.Sprintf("%v", step.With["node-version"]); got == "${{ matrix.node }}" {
				sawMatrixVersion = true
			} else {
				t.Errorf("setup-node's node-version is %q, not ${{ matrix.node }}: the matrix's two lines are the whole reason there are two legs", got)
			}
		}
		if strings.HasPrefix(step.Uses, "actions/checkout@") {
			if !strings.Contains(fmt.Sprintf("%v", step.With["persist-credentials"]), "false") {
				t.Error("the checkout keeps the persisted credentials: this job only reads the repository")
			}
		}
	}
	if !sawMatrixVersion {
		t.Error("no setup-node step takes node-version from the matrix")
	}
}

// TestSharedTestsWorkflowStagingStateIsWhatTheReadmeSays: the document beside
// the staged file is itself a claim about the tree, and the claim it makes --
// "this file is still waiting to be copied in" -- is false the moment the
// owner copies it in and `git rm`s the staged copy. Nothing about the workflow
// changes then, so nothing in the tests above notices; this is the test that
// does, and it is why the README's copy-in steps end with an edit to the README
// itself rather than with the copy.
//
// The claim is read from two places, both of which the copy-in has to update:
// the machine-readable `**Status: `shared-tests.yml` is ...**` line in the
// file's own section, and the document's opening line, which is the sentence a
// reader sees first and the one that names the pending file. A third assertion
// keeps the document naming this test, so the paragraph that tells a reader
// what enforces the state cannot outlive the name it prints.
func TestSharedTestsWorkflowStagingStateIsWhatTheReadmeSays(t *testing.T) {
	// The document's prose, without its fenced blocks. The copy-in steps quote
	// both forms -- the command that copies the file in, and the line the
	// README's own status should become -- so a check over the raw text would
	// find the applied form quoted in the instructions and call the document
	// self-contradictory. What is claimed is the prose; what is quoted in a
	// fence is a command or a shape, not a claim about today.
	readme := sharedTestsReadmeProse(t)
	_, stagedErr := os.Stat("../../" + stagedSharedTestsWorkflowPath)
	_, realErr := os.Stat("../../" + realSharedTestsWorkflowPath)

	for _, p := range sharedTestsStagingStateProblems(readme, stagedErr == nil, realErr == nil) {
		t.Error(p)
	}
}

// sharedTestsStagingStateProblems returns every disagreement between the
// README's prose and which of the two files exist, as messages rather than as
// test failures: taking the state as arguments is what lets
// TestSharedTestsStagingStateRulesSeeEveryState pin all four combinations
// against fixtures. That matters here more than it usually would, because one
// of the four -- copied in and the staged copy removed -- is a state this
// repository is never in while an agent is working: writing
// .github/workflows/shared-tests.yml is the owner's step, so without the
// fixture test the applied half of this check would never be exercised at all.
//
// The state is keyed on the staged copy, which is what readSharedTestsWorkflowAt
// above keys on: while it exists the staged file is the reviewed one, and the
// README is still telling a reader to copy it.
func sharedTestsStagingStateProblems(readme string, staged, real bool) []string {
	if !staged && !real {
		return []string{fmt.Sprintf("neither %s nor %s exists: the job is in neither place, so there is no staging state for %s to be right about",
			stagedSharedTestsWorkflowPath, realSharedTestsWorkflowPath, pendingWorkflowsReadmePath)}
	}
	var problems []string

	// The machine-readable half: one line, and it has to say the state the tree
	// is in.
	statuses := sharedTestsStagingStatusRE.FindAllStringSubmatch(readme, -1)
	want := sharedTestsAppliedState
	if staged {
		want = sharedTestsStagedState
	}
	switch {
	case len(statuses) == 0:
		problems = append(problems, fmt.Sprintf("%s carries no `**Status: `shared-tests.yml` is ...**` line: that line is the machine-readable half of this claim, and the copy-in steps change it", pendingWorkflowsReadmePath))
	case len(statuses) > 1:
		problems = append(problems, fmt.Sprintf("%s carries %d `**Status: `shared-tests.yml` is ...**` lines, want exactly one: two of them cannot both be the state of this directory", pendingWorkflowsReadmePath, len(statuses)))
	case statuses[0][1] != want:
		problems = append(problems, fmt.Sprintf("%s says shared-tests.yml is %q while %s %s: the document and the directory have to agree in both directions, or a reader is told to copy a file that is already in place, or left believing a copy nobody made",
			pendingWorkflowsReadmePath, statuses[0][1],
			map[bool]string{true: stagedSharedTestsWorkflowPath, false: realSharedTestsWorkflowPath}[staged],
			map[bool]string{true: "is still here", false: "is gone"}[staged]))
	}

	// The opening line, which is prose and is checked as the one phrase the
	// copy-in has to remove rather than as a whole sentence: it is the line
	// that says which file is pending.
	if got := strings.Contains(readme, sharedTestsPendingClaim); got != staged {
		problems = append(problems, fmt.Sprintf("%s %s %q, and the staged copy %s: the opening line and the directory must agree (see the copy-in steps in that document -- this is the step that used to be missing)",
			pendingWorkflowsReadmePath,
			map[bool]string{true: "still carries", false: "does not carry"}[got], sharedTestsPendingClaim,
			map[bool]string{true: "exists", false: "is gone"}[staged]))
	}

	// ... and the document has to name the check that holds it to the tree. A
	// reader who has just copied the file in has no other way to find out which
	// test tells them whether they finished.
	if !strings.Contains(readme, sharedTestsStagingEnforcer) {
		problems = append(problems, fmt.Sprintf("%s never names %s: the paragraph that describes the staged/applied state has to name the check that enforces it, or the name drifts and the reader is left with prose", pendingWorkflowsReadmePath, sharedTestsStagingEnforcer))
	}
	return problems
}

// TestSharedTestsStagingStateRulesSeeEveryState pins the rules above against
// every combination of the two files and both README forms, including the two
// this repository cannot be in: the copy-in (the real file exists, the staged
// one is gone -- an agent may not create it) and the half-done copy-in (both
// exist, which the read above treats as still pending, because the staged file
// is still the reviewed one). Each case asserts the problems *and* that a
// correct document produces none, so a rule that refused everything would fail
// here rather than looking strict.
func TestSharedTestsStagingStateRulesSeeEveryState(t *testing.T) {
	const (
		stagedLine  = "**Status: `shared-tests.yml` is staged and pending.**"
		appliedLine = "**Status: `shared-tests.yml` is applied, not pending.**"
		stagedOpen  = "**One file is pending: `shared-tests.yml`**"
		appliedOpen = "**Nothing is pending.**"
		enforcer    = sharedTestsStagingEnforcer
	)
	doc := func(open, status, tail string) string {
		return open + "\n\n" + status + "\n\n" + tail + "\n"
	}
	for _, tc := range []struct {
		name         string
		staged, real bool
		readme       string
		want         []string // one substring per expected problem, in any order
	}{
		{"staged and pending, the state this branch is in", true, false,
			doc(stagedOpen, stagedLine, enforcer), nil},
		{"copied in and the staged copy removed", false, true,
			doc(appliedOpen, appliedLine, enforcer), nil},
		{"both copies present, which is still pending", true, true,
			doc(stagedOpen, stagedLine, enforcer), nil},
		{"staged, but the README already says applied", true, false,
			doc(appliedOpen, appliedLine, enforcer),
			[]string{"says shared-tests.yml is", "does not carry"}},
		{"copied in, but the README still says staged", false, true,
			doc(stagedOpen, stagedLine, enforcer),
			[]string{"says shared-tests.yml is", "still carries"}},
		// (the two above are the pair the review found missing: the same
		// disagreement, in the two directions)
		{"the status line gone", true, false,
			doc(stagedOpen, "", enforcer),
			[]string{"carries no `**Status:"}},
		{"two status lines", true, false,
			doc(stagedOpen, stagedLine+"\n"+appliedLine, enforcer),
			[]string{"carries 2 `**Status:"}},
		{"the enforcer no longer named", true, false,
			doc(stagedOpen, stagedLine, "some other test"),
			[]string{"never names " + enforcer}},
		{"neither file", false, false,
			doc(appliedOpen, appliedLine, enforcer),
			[]string{"is in neither place"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := sharedTestsStagingStateProblems(tc.readme, tc.staged, tc.real)
			if len(problems) != len(tc.want) {
				t.Fatalf("problems = %q, want %d of them (%q)", problems, len(tc.want), tc.want)
			}
			for _, want := range tc.want {
				if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, want) }) {
					t.Errorf("no problem names %q; got %q", want, problems)
				}
			}
		})
	}
}

// TestSharedTestsWorkflowReadmeNamesEveryProofScript: the proof step globs for
// `shared/*/mutation-proof.sh`, so the job never needs an edit when a module
// adds or loses one -- but the README lists them by name, and a list written by
// hand is a list that goes stale. This is the test that stops it: the scripts
// are counted from the tree, and the README's list has to be exactly that set,
// in both directions.
//
// It is also what keeps the section honest about *how many* proofs there are:
// the count that used to be written into that prose ("the package's four
// mutation-proof scripts") was wrong within weeks of being written, and the
// list it is replaced by cannot be wrong without failing here.
func TestSharedTestsWorkflowReadmeNamesEveryProofScript(t *testing.T) {
	section := sharedTestsReadmeSectionText(t)
	found, err := filepath.Glob("../../shared/*/mutation-proof.sh")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatalf("shared/ carries no */mutation-proof.sh: the job's proof step refuses an empty glob, and so does this test -- a README list checked against nothing would pass vacuously")
	}
	want := make([]string, 0, len(found))
	for _, path := range found {
		want = append(want, strings.TrimPrefix(path, "../../"))
	}
	slices.Sort(want)

	named := sharedTestsProofScriptRE.FindAllString(section, -1)
	slices.Sort(named)
	named = slices.Compact(named)

	if !slices.Equal(named, want) {
		t.Errorf("%s's `%s` section names the proof scripts %q; shared/ carries %q. The list is the README's and the glob is the job's, and they have to be the same set: a module that adds a proof script is covered by the job with no edit, so the README is the only place that can go on saying otherwise, and a name here whose script is gone sends a reader looking for a proof that does not exist",
			pendingWorkflowsReadmePath, sharedTestsReadmeHeading, named, want)
	}
}

// sharedTestsReadmeProse returns docs/pending-workflows/README.md with its
// fenced code blocks removed: three-backtick fences and the lines between them,
// including the ```sh blocks that hold the copy-in commands. A command quoted
// in a fence is an instruction, and the shapes it names are the ones the reader
// is being asked to produce; only the prose outside the fences is a claim about
// the tree, and only the prose is what the checks below read.
func sharedTestsReadmeProse(t *testing.T) string {
	t.Helper()
	var out strings.Builder
	inFence := false
	for _, line := range strings.Split(readDoc(t, pendingWorkflowsReadmePath), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			out.WriteString(line)
			out.WriteString("\n")
		}
	}
	if inFence {
		t.Fatalf("%s has an unclosed ``` fence: this test cannot tell prose from a quoted command in it", pendingWorkflowsReadmePath)
	}
	return out.String()
}

// sharedTestsReadmeSectionText returns the body of the README's
// `## `shared-tests.yml“ section, from its heading to the next `## ` heading.
// Scoped rather than whole-document because every claim here is about this one
// workflow: a sentence in another workflow's section must not be able to
// satisfy it.
func sharedTestsReadmeSectionText(t *testing.T) string {
	t.Helper()
	doc := sharedTestsReadmeProse(t)
	start := strings.Index(doc, "\n"+sharedTestsReadmeHeading)
	if start < 0 {
		t.Fatalf("%s has no %q heading: this test reads that section and nothing else", pendingWorkflowsReadmePath, sharedTestsReadmeHeading)
	}
	rest := doc[start+1:]
	if end := strings.Index(rest[len(sharedTestsReadmeHeading):], "\n## "); end >= 0 {
		return rest[:len(sharedTestsReadmeHeading)+end]
	}
	return rest
}

// TestReadSharedTestsWorkflowFallsBackOnlyWhilePending pins every mode of the
// read above against temp files, so the rules stay checkable whatever state
// this repository is in -- the staged copy here, the copied-in file there, or
// both. It is what makes the copy-in procedure safe rather than a hope.
func TestReadSharedTestsWorkflowFallsBackOnlyWhilePending(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "pending.yml")
	real := filepath.Join(dir, "real.yml")

	// The two fixtures are deliberately written so that nothing inside them
	// could say which copy it is: the requirement is that the staged file and
	// the copied-in one are byte-identical, so a marker in the text -- a
	// "staged, not installed" banner, say -- would either be false once it was
	// copied in or make the byte-identity assertion unsatisfiable. Which file
	// was read is therefore reported as a path, and the identical case below is
	// what pins that.
	pending := "name: shared-tests\non:\n  push:\n"
	copiedIn := "name: shared-tests\non:\n  pull_request:\n"
	if err := os.WriteFile(staged, []byte(pending), 0o600); err != nil {
		t.Fatal(err)
	}

	// Pending alone: the staged bytes are what is judged, there is nothing to
	// report, and the source names the staged file.
	raw, source, problems, err := readSharedTestsWorkflowAt(staged, real)
	if err != nil || len(problems) != 0 {
		t.Fatalf("staged alone: want no problems and no error, got %q and %v", problems, err)
	}
	if string(raw) != pending || source != staged {
		t.Fatalf("staged alone: want the staged bytes from %s, got %q from %s", staged, raw, source)
	}

	// Both, identical: the staged copy still wins, the pair is consistent, and
	// the source is still the staged path -- which is the case no in-file
	// marker could answer, because the two files are the same bytes.
	if err := os.WriteFile(real, []byte(pending), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, source, problems, err = readSharedTestsWorkflowAt(staged, real)
	if err != nil || len(problems) != 0 || string(raw) != pending || source != staged {
		t.Fatalf("both, identical: want the staged bytes from %s and no problems, got %q from %s, %q, %v", staged, raw, source, problems, err)
	}

	// Both, differing: the copy-in was edited after review, which is the one
	// thing the staged file cannot otherwise notice.
	if err := os.WriteFile(real, []byte("name: not-the-staged-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, source, problems, err = readSharedTestsWorkflowAt(staged, real)
	if err != nil {
		t.Fatalf("both, differing: the staged copy is still readable, so this is a problem and not an error: %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "both exist and differ") {
		t.Fatalf("both, differing: want the byte-identity problem, got %q", problems)
	}
	if string(raw) != pending || source != staged {
		t.Fatalf("both, differing: the staged copy must still be what is judged, got %q from %s", raw, source)
	}

	// Copied in and the staged copy removed: the real file is read alone, its
	// source is the real path, and it must carry the marker.
	if err := os.WriteFile(real, []byte(copiedIn), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(staged); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readSharedTestsWorkflowAt(staged, real); err == nil {
		t.Fatal("copied in, marker missing: want an error, got none: this is the read that refuses a copy which lost the job")
	}
	if err := os.WriteFile(real, []byte(copiedIn+sharedTestsWorkflowMarker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, source, problems, err = readSharedTestsWorkflowAt(staged, real)
	if err != nil || len(problems) != 0 {
		t.Fatalf("copied in, marker present: want the real bytes and no problems, got %q, %q, %v", raw, problems, err)
	}
	if string(raw) != copiedIn+sharedTestsWorkflowMarker+"\n" || source != real {
		t.Fatalf("copied in, marker present: want the real file's bytes from %s, got %q from %s", real, raw, source)
	}

	// Neither: there is no document to judge, and that is fatal rather than a
	// silently empty run.
	if err := os.Remove(real); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readSharedTestsWorkflowAt(staged, real); err == nil {
		t.Fatal("neither file: want an error, got none")
	}
}

// TestSharedTestsWorkflowPinsAgreeWithTheOtherWorkflows: once the staged copy
// has been copied in and removed, this workflow's pins cannot drift from the
// other workflows' pins of the same action. While it is pending the comparison
// is skipped, and says so: Dependabot does not watch docs/pending-workflows/,
// so the pins here can legitimately lag main's bumps, and the README beside the
// file makes the person copying it check the drift (the same rule
// images.yml's own pin test uses).
func TestSharedTestsWorkflowPinsAgreeWithTheOtherWorkflows(t *testing.T) {
	if _, err := os.Stat("../../" + stagedSharedTestsWorkflowPath); err == nil {
		t.Skipf("%s is still pending: Dependabot does not watch it, so a pin that lags main is expected and the copy-in compares the two (docs/pending-workflows/README.md)", stagedSharedTestsWorkflowPath)
	}
	_, raw, path := readSharedTestsWorkflow(t)
	mine := workflowPins(string(raw))
	others, err := os.ReadDir("../../.github/workflows")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range others {
		name := entry.Name()
		if name == "shared-tests.yml" || !strings.HasSuffix(name, ".yml") {
			continue
		}
		data, err := os.ReadFile("../../.github/workflows/" + name)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pinDriftProblems(mine, path, name, workflowPins(string(data))) {
			t.Error(p)
		}
	}
}

// pinDriftProblems names every action in mine that name also uses at another
// ref, and is a function rather than an inline loop so that
// TestPinDriftProblemsFindsDrift can pin it against two maps: the test above
// skips while the staged copy exists, and a skip is not a proof.
func pinDriftProblems(mine map[string]string, path, name string, other map[string]string) []string {
	var out []string
	for action, ref := range other {
		if ours, ok := mine[action]; ok && ours != ref {
			out = append(out, fmt.Sprintf("%s pins %s to %s and %s pins it to %s: the two must be the same commit, or one of them is an older tag of the same action", path, action, ours, name, ref))
		}
	}
	slices.Sort(out)
	return out
}

// TestPinDriftProblemsFindsDrift pins pinDriftProblems itself: a disagreement
// is reported, and an action one workflow uses and the other does not is not.
func TestPinDriftProblemsFindsDrift(t *testing.T) {
	const a, b = "3d3c42e5aac5ba805825da76410c181273ba90b1", "949feb2413d6458794dcd2491c4babbbce0c15c1"
	got := pinDriftProblems(
		map[string]string{"actions/checkout": a, "actions/setup-node": b},
		"shared-tests.yml", "ci.yml",
		map[string]string{"actions/checkout": b, "actions/setup-go": b},
	)
	if len(got) != 1 || !strings.Contains(got[0], "actions/checkout") {
		t.Fatalf("want exactly the checkout drift, got %q", got)
	}
	if p := pinDriftProblems(map[string]string{"actions/checkout": a}, "shared-tests.yml", "ci.yml", map[string]string{"actions/checkout": a}); len(p) != 0 {
		t.Fatalf("matching pins reported: %q", p)
	}
}

// workflowPins returns every `action@ref` a document's `uses:` lines carry,
// last occurrence winning, which is what a drift comparison needs.
func workflowPins(text string) map[string]string {
	out := map[string]string{}
	for _, m := range sharedTestsUsesRE.FindAllStringSubmatch(text, -1) {
		u := m[1]
		if i := strings.LastIndex(u, "@"); i >= 0 {
			out[u[:i]] = u[i+1:]
		}
	}
	return out
}

// sortedCopy returns a copy of in sorted, so two lists are compared as sets:
// the order of a YAML list is not a property this file's assertions are about.
func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

// keys returns a map's keys sorted, for a failure message that names what was
// there instead.
func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
