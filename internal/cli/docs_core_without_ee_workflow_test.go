package cli

// The staged "core without ee/" workflow (licensing plan task L1.7).
//
// The open-core skeleton's boundary is worth nothing unless the core really
// does build and pass its tests with ee/ gone. `internal/eeboundary` proves the
// core never imports, embeds or routes into ee/; no test in this package can
// delete the directory and run the suite without it. That is a workflow's job,
// and a workflow is not a run's to write -- GitHub refuses a Fugaro push that
// touches .github/workflows/** -- so the file is staged under
// docs/pending-workflows/ for the owner to copy in (L1.8), as images.yml and
// the console's two are.
//
// TestDocsCoreWithoutEEWorkflow is the staged file's static shape until then,
// and it holds the copied file afterwards too: consoleWorkflowText reads the
// staged copy while it exists and the applied one once the copy-in has removed
// it, so the same assertions are green before and after a step only a human
// performs. What is asserted is the fields GitHub executes and the claims the
// file makes about this repository:
//
//   - the trigger surface: a push to main and a manual dispatch, nothing else;
//   - least privilege: contents: read at the top, and no secret anywhere in the
//     file;
//   - every `uses:` pinned by a full commit SHA with its `# vX.Y.Z` beside it,
//     Go taken from go.mod, and a job-level timeout-minutes, so a hung suite
//     cannot hold a runner for GitHub's six-hour default;
//   - ee/ is deleted before anything is built, and with the default build tags:
//     no step hands the go tool a tag list holding `ee`, so
//     cmd/fugaro/ee_on.go is not compiled and what is checked is the core
//     alone -- the artifact the 0.11.0 release ships (D303 as amended);
//   - the tests run in two steps, and the split is a claim about this
//     repository rather than a style: the first skips exactly the tests that
//     read the real ee/ and excludes internal/runner, and the second runs
//     internal/runner at the 40-minute bound CONTRIBUTING.md gives it with no
//     skip at all. Nothing under internal/eeboundary/ is ever skipped: those
//     tests plant their own trees under t.TempDir() and read nothing from the
//     repository's ee/.
//
// The skip list is the coupling that matters. eeReadingTests, in
// docs_ee_repo_test.go, is the exact list of this package's tests that read the
// real ee/ through readRealEE; the workflow skips that list and nothing else,
// so a new reader joins both in the same change -- a reader left out of the
// workflow takes it down on the next push to main, and a name left in it skips
// a test that no longer needs skipping.
//
// What this test cannot do is run the job. Nothing here executes GitHub
// Actions, so the file has never run: the first run is the owner's, after the
// copy-in, and the staging directory's README says so in the file's section.

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	// coreWithoutEEStagedPath is the file this task stages: the whole workflow,
	// in the directory an agent run is allowed to write.
	coreWithoutEEStagedPath = "docs/pending-workflows/core-without-ee.yml"
	// coreWithoutEEAppliedPath is where the owner copies it to. This test reads
	// whichever of the two exists rather than assuming either.
	coreWithoutEEAppliedPath = ".github/workflows/core-without-ee.yml"
	// coreWithoutEEName is the workflow's `name:`, which is also what
	// `gh workflow run core-without-ee` looks up.
	coreWithoutEEName = "core-without-ee"
	// coreWithoutEEReadmeHeading opens the staging directory's section on this
	// file: the copy-in the owner performs is written there.
	coreWithoutEEReadmeHeading = "## `core-without-ee.yml`"
	// coreWithoutEEStagedState and coreWithoutEEAppliedState are the two halves
	// of that section's status line. The line is a claim about the tree -- does
	// the staged copy exist? -- so it is held to the tree rather than to prose,
	// the way shared-tests.yml's is: the copy-in changes this line and the
	// document's opening line, and nothing else.
	coreWithoutEEStagedState  = "staged and pending"
	coreWithoutEEAppliedState = "applied, not pending"
	// coreWithoutEEBuildTag is the tag that would compile cmd/fugaro/ee_on.go,
	// the one core file allowed to import ee/. No step of this job may hand the
	// go tool a tag list holding it: the job's subject is the untagged core.
	coreWithoutEEBuildTag = "ee"
)

// coreWithoutEEWorkflow is the file's shape: the keys GitHub executes, decoded
// with unknown fields refused, so a key nobody pins is a decode error here
// rather than a line this test silently ignores. `on` is the one raw node --
// see coreWithoutEETriggers for why.
type coreWithoutEEWorkflow struct {
	Name        string                      `yaml:"name"`
	On          yaml.Node                   `yaml:"on"`
	Permissions map[string]string           `yaml:"permissions"`
	Jobs        map[string]coreWithoutEEJob `yaml:"jobs"`
}

type coreWithoutEEJob struct {
	RunsOn         string              `yaml:"runs-on"`
	TimeoutMinutes int                 `yaml:"timeout-minutes"`
	Steps          []coreWithoutEEStep `yaml:"steps"`
}

type coreWithoutEEStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]any    `yaml:"with"`
	Env  map[string]string `yaml:"env"`
}

// coreWithoutEETrigger is one trigger the `on:` block declares: its name, and
// its body as a raw node.
type coreWithoutEETrigger struct {
	name string
	body yaml.Node
}

// coreWithoutEETriggers reads the `on:` block: the triggers it declares, in the
// order the file writes them, each with its own body.
//
// It is a raw YAML node rather than a typed struct because `workflow_dispatch:`
// has no body: decoding it into a pointer answers nil whether the key is absent
// or present-and-empty, and which triggers the file declares is the whole
// question here. A block that is not a mapping is a failure rather than an empty
// set, so a file this test cannot read does not pass as one with no triggers.
func coreWithoutEETriggers(t *testing.T, path string, on yaml.Node) []coreWithoutEETrigger {
	t.Helper()
	if on.Kind != yaml.MappingNode {
		t.Fatalf("%s: `on:` is not a mapping of triggers (kind %v): this test reads the triggers the file declares", path, on.Kind)
	}
	var out []coreWithoutEETrigger
	for i := 0; i+1 < len(on.Content); i += 2 {
		out = append(out, coreWithoutEETrigger{name: on.Content[i].Value, body: *on.Content[i+1]})
	}
	return out
}

// coreWithoutEESkipRE pulls the value out of a `-skip` flag, in either quoting
// the shell line may use. The value is the alternation this test compares with
// eeReadingTests.
var coreWithoutEESkipRE = regexp.MustCompile(`-skip[= ]\s*(?:'([^']*)'|"([^"]*)"|(\S+))`)

// coreWithoutEETagFlagRE finds a `-tags` flag and the list it hands the go
// tool, in both spellings (`-tags ee`, `-tags=ee`), quoted or not, and anywhere
// a step may write it -- a command line, or a GOFLAGS value in a step's env.
// The list is what is judged: one that holds `ee` compiles the tagged assembly
// file, which is the one thing this job must not do.
var coreWithoutEETagFlagRE = regexp.MustCompile(`-tags[= ]\s*['"]?([^'"\s]*)`)

// coreWithoutEEStatusRE reads the staging directory's machine-readable line for
// this file, the way sharedTestsStagingStatusRE does for shared-tests.yml.
var coreWithoutEEStatusRE = regexp.MustCompile("\\*\\*Status: `core-without-ee.yml` is (staged and pending|applied, not pending)\\.?\\*\\*")

// readCoreWithoutEEWorkflow returns the workflow's text, the path it came from,
// and its decoded shape. The bytes come from consoleWorkflowText, the reader
// every staged workflow in this package uses: the staged copy while it exists,
// the applied one once the owner's copy-in has removed it, and a byte-identity
// clause while both are there.
func readCoreWithoutEEWorkflow(t *testing.T) (string, string, coreWithoutEEWorkflow) {
	t.Helper()
	text, path := consoleWorkflowText(t, coreWithoutEEStagedPath, coreWithoutEEAppliedPath)
	var wf coreWithoutEEWorkflow
	dec := yaml.NewDecoder(bytes.NewReader([]byte(text)))
	dec.KnownFields(true)
	if err := dec.Decode(&wf); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return text, path, wf
}

// TestDocsCoreWithoutEEWorkflow: the staged job is safe to copy in, and it
// checks what the licensing plan says it checks.
func TestDocsCoreWithoutEEWorkflow(t *testing.T) {
	text, path, wf := readCoreWithoutEEWorkflow(t)

	if wf.Name != coreWithoutEEName {
		t.Errorf("%s is named %q, want %q: the name is what `gh workflow run` looks up", path, wf.Name, coreWithoutEEName)
	}

	// --- The trigger surface ------------------------------------------------
	// A push to main and a manual dispatch, in that order and nothing else: a
	// pull request would run the job on every branch of the repository, and a
	// second branch would run it on a tree no release is cut from. The decode
	// above refuses an unknown key anywhere in the file, so a third trigger
	// cannot slip in beside these two.
	triggers := coreWithoutEETriggers(t, path, wf.On)
	var names []string
	for _, tr := range triggers {
		names = append(names, tr.name)
	}
	if want := []string{"push", "workflow_dispatch"}; !slices.Equal(names, want) {
		t.Errorf("%s triggers on %q, want %q: a push to main is what proves the core still builds without ee/, and the dispatch is how the owner starts it by hand", path, names, want)
	}
	if len(triggers) > 0 && triggers[0].name == "push" {
		push := triggers[0].body
		if push.Kind != yaml.MappingNode || len(push.Content) != 2 {
			t.Fatalf("%s: the push trigger carries more than a branch filter (kind %v, %d keys): a paths filter would leave the job unrun on the change that breaks it", path, push.Kind, len(push.Content)/2)
		}
		if key := push.Content[0].Value; key != "branches" {
			t.Errorf("%s: the push trigger's only key is %q, want branches", path, key)
		}
		var branches []string
		if err := push.Content[1].Decode(&branches); err != nil {
			t.Fatalf("%s: the push trigger's branches do not decode: %v", path, err)
		}
		if want := []string{"main"}; !slices.Equal(branches, want) {
			t.Errorf("%s pushes on %q, want %q: main is where every release is cut from", path, branches, want)
		}
	}

	// --- Permissions, secrets, pins, timeout --------------------------------
	// Least privilege at the top, so no job can widen it by omission: the job
	// reads the repository and writes nothing, and it holds no secret at all.
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("%s grants %v at the workflow level, want only contents: read", path, wf.Permissions)
	}
	if strings.Contains(text, "secrets.") {
		t.Errorf("%s names a secret: the job builds and tests the public repository and needs none, so a secret here is a grant nobody asked for (this test scans the bytes, so the file's own comments must not spell the word out either)", path)
	}

	// Every `uses:` by full commit SHA with the `# vX.Y.Z` comment that makes
	// the pin reviewable, the way the console's staged files are held: a tag is
	// a mutable name on a repository this one does not control, and Dependabot
	// does not watch docs/pending-workflows/.
	uses := 0
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "uses:") {
			continue
		}
		uses++
		if !consoleUsesLine.MatchString(line) {
			t.Errorf("%s: %q is not a full 40-hex pin with a `# vX.Y.Z` comment", path, strings.TrimSpace(line))
		}
	}
	if uses == 0 {
		t.Fatalf("%s pins no action at all: the job checks the repository out and installs Go", path)
	}

	// One job, with a bound of its own. A hung suite under GitHub's six-hour
	// default holds a runner all afternoon; the bound is what makes a hang a
	// failed run instead.
	if len(wf.Jobs) != 1 {
		t.Fatalf("%s declares %d jobs, want exactly 1: the file is one job's shape, and a second one would be a different check", path, len(wf.Jobs))
	}
	var jobName string
	var job coreWithoutEEJob
	for name, j := range wf.Jobs {
		jobName, job = name, j
	}
	if jobName != coreWithoutEEName {
		t.Errorf("%s's job is named %q, want %q: the check's name is what a reader sees in the Actions list", path, jobName, coreWithoutEEName)
	}
	if job.RunsOn != "ubuntu-latest" {
		t.Errorf("%s runs on %q, want ubuntu-latest: the job builds and tests the module, which needs no other platform", path, job.RunsOn)
	}
	if job.TimeoutMinutes != 90 {
		t.Errorf("%s's job timeout-minutes is %d, want 90: the suite takes about as long as ci.yml's test job, and a job without a bound runs to GitHub's six-hour default", path, job.TimeoutMinutes)
	}

	// The toolchain comes from go.mod, the way every other job in this
	// repository takes it, so the job cannot run a Go the module does not pin;
	// and the checkout drops the token from .git/config, which no step here
	// needs (images/cloudbuild_test.go requires it of every workflow).
	var setupGo, checkout bool
	for _, s := range job.Steps {
		switch {
		case strings.HasPrefix(s.Uses, "actions/setup-go@"):
			setupGo = true
			if s.With["go-version-file"] != "go.mod" {
				t.Errorf("%s's setup-go takes %v, want go-version-file: go.mod: the Go version is the one go.mod pins", path, s.With)
			}
		case strings.HasPrefix(s.Uses, "actions/checkout@"):
			checkout = true
			if s.With["persist-credentials"] != false {
				t.Errorf("%s's checkout does not set persist-credentials: false: no step here needs the token in .git/config", path)
			}
		}
	}
	if !setupGo || !checkout {
		t.Errorf("%s checks out %v and sets up Go %v: the job needs both", path, checkout, setupGo)
	}

	// --- ee/ is gone before anything is built --------------------------------
	deleteAt := -1
	for i, s := range job.Steps {
		if strings.Contains(s.Run, "rm -rf ee") {
			deleteAt = i
			break
		}
	}
	if deleteAt < 0 {
		t.Fatalf("%s never deletes ee/: the job's whole subject is the core without it", path)
	}
	for i, s := range job.Steps {
		if i >= deleteAt {
			continue
		}
		for _, cmd := range []string{"go build", "go vet", "go test"} {
			if strings.Contains(s.Run, cmd) {
				t.Errorf("%s runs %q in step %d, before ee/ is deleted in step %d: the deletion has to come first, or what is built is the tree with ee/ still in it", path, cmd, i, deleteAt)
			}
		}
	}
	// The deletion is not committed: it is the checkout's own working tree, so
	// the job needs no write scope and no branch. A `git rm`/`git commit` pair
	// here would be a push from CI, which is a different grant.
	for _, s := range job.Steps {
		if strings.Contains(s.Run, "git rm") || strings.Contains(s.Run, "git commit") || strings.Contains(s.Run, "git push") {
			t.Errorf("%s commits or pushes in a step (%q): ee/ is deleted from the working tree only, which is why the job needs no write scope", path, strings.TrimSpace(s.Run))
		}
	}
	for _, cmd := range []string{"go build ./...", "go vet ./..."} {
		found := false
		for _, s := range job.Steps {
			if strings.Contains(s.Run, cmd) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s never runs %q: the core has to build and vet without ee/, not only pass its tests", path, cmd)
		}
	}

	// --- The two test steps -------------------------------------------------
	// Step A: everything but internal/runner, skipping exactly the tests that
	// read the real ee/. Step B: internal/runner alone, at the bound
	// CONTRIBUTING.md gives it, with no skip -- so the package is not run twice
	// and no test of it is silently dropped.
	var coreTest, runnerTest *coreWithoutEEStep
	for i := range job.Steps {
		s := &job.Steps[i]
		switch {
		case strings.Contains(s.Run, "./internal/runner/..."):
			if runnerTest != nil {
				t.Errorf("%s runs ./internal/runner/... in two steps: the package is the long one, and running it twice doubles the job", path)
			}
			runnerTest = s
		case strings.Contains(s.Run, "-skip"):
			if coreTest != nil {
				t.Errorf("%s carries a -skip in two steps: one step skips the tests that read ee/, and nothing else does", path)
			}
			coreTest = s
		}
	}
	if coreTest == nil {
		t.Fatalf("%s has no test step carrying -skip: the tests that read the real ee/ have to be skipped by name, because the directory is gone", path)
	}
	if runnerTest == nil {
		t.Fatalf("%s never runs ./internal/runner/...: the package takes about 25 minutes and needs the 40-minute bound CONTRIBUTING.md gives it, so it is a step of its own", path)
	}

	// Step A's skip list is eeReadingTests, character for character: the list
	// docs_ee_repo_test.go holds is the exact set of tests that read the real
	// ee/, so the two are one claim and a change to either is a change to both.
	wantSkip := "^(" + strings.Join(eeReadingTests, "|") + ")$"
	m := coreWithoutEESkipRE.FindStringSubmatch(coreTest.Run)
	if m == nil {
		t.Fatalf("%s's test step carries a -skip flag this test cannot read: %q", path, coreTest.Run)
	}
	gotSkip := ""
	for _, g := range m[1:] {
		if g != "" {
			gotSkip = g
		}
	}
	if gotSkip != wantSkip {
		t.Errorf(`%s skips %q and the tests that read the real ee/ are %q:
  want -skip '%s'

eeReadingTests, in docs_ee_repo_test.go, is the exact list of this package's tests that read the real ee/ through readRealEE. The workflow skips that list and nothing else: a reader left out of it takes this job down on the next push to main, and a name left in it skips a test that no longer reads anything the job deleted.`, path, gotSkip, eeReadingTests, wantSkip)
	}
	if strings.Contains(gotSkip, "eeboundary") {
		t.Errorf("%s skips an internal/eeboundary test (%q): those tests plant their own ee/ trees under t.TempDir() and read nothing from the repository's, so the job must run every one of them", path, gotSkip)
	}
	if !strings.Contains(coreTest.Run, "$(go list ./... | grep -v internal/runner)") {
		t.Errorf("%s's skipping step runs %q: it has to take every package but internal/runner -- that package is step B's, and running it here too would double the job and put it under the wrong bound", path, coreTest.Run)
	}
	if !strings.Contains(coreTest.Run, "go test") || !strings.Contains(coreTest.Run, "-timeout 30m") {
		t.Errorf("%s's skipping step runs %q: it is the core's test step, at the 30-minute bound the other packages fit in", path, coreTest.Run)
	}

	if !strings.Contains(runnerTest.Run, "go test") || !strings.Contains(runnerTest.Run, "-race") || !strings.Contains(runnerTest.Run, "-timeout 40m") {
		t.Errorf("%s's runner step runs %q: the package needs -race and the 40-minute bound CONTRIBUTING.md gives it", path, runnerTest.Run)
	}
	if strings.Contains(runnerTest.Run, "-skip") {
		t.Errorf("%s's runner step runs %q: no test of internal/runner reads ee/, so the step skips nothing", path, runnerTest.Run)
	}

	// --- No build tag reaches the tagged assembly file ----------------------
	// cmd/fugaro/ee_on.go is the one core file allowed to import ee/, it carries
	// //go:build ee, and the release sets no tag at 0.11.0 (D303 as amended):
	// this job must build the untagged core, which is the binary that ships.
	for _, s := range job.Steps {
		values := make([]string, 0, len(s.Env)+1)
		values = append(values, s.Run)
		for _, v := range s.Env {
			values = append(values, v)
		}
		for _, v := range values {
			for _, tag := range coreWithoutEETagFlagRE.FindAllStringSubmatch(v, -1) {
				for _, name := range strings.FieldsFunc(tag[1], func(r rune) bool { return r == ',' || r == ' ' }) {
					if name == coreWithoutEEBuildTag {
						t.Errorf("%s hands the go tool the tag %q (%q): the job's subject is the untagged core, and an ee-tagged build is the release's own build (D303 as amended)", path, coreWithoutEEBuildTag, strings.TrimSpace(v))
					}
				}
			}
		}
	}

	// --- The staging directory says how to copy it in -----------------------
	// The file is a proposal a person applies by hand, so the place they read
	// carries the commands and says what happens to the staged copy afterwards.
	// The status line is a claim about the tree, so it is held to the tree: the
	// copy-in that removes the staged copy has to change it.
	const readme = "docs/pending-workflows/README.md"
	doc := readDoc(t, readme)
	section := consoleReadmeSection(t, doc, coreWithoutEEReadmeHeading)
	for _, want := range []string{
		"cp docs/pending-workflows/core-without-ee.yml .github/workflows/core-without-ee.yml",
		"git rm docs/pending-workflows/core-without-ee.yml",
		"gh workflow run core-without-ee",
		// The two checks that hold the sentences the copy-in has to update.
		"TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere",
		"website/test/readme.mjs",
		// This test, so a reader knows what is pinned and what is not.
		"TestDocsCoreWithoutEEWorkflow",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's `core-without-ee.yml` section lacks %q: the owner is the one who copies the file in, and this is where they read what it is, why it is staged and what to do after the copy", readme, want)
		}
	}
	status := coreWithoutEEStatusRE.FindStringSubmatch(doc)
	if status == nil {
		t.Fatalf("%s carries no `**Status: `core-without-ee.yml` is …**` line: the staged/applied state is a fact about the tree, and the document that describes it has to state it where a copy-in can change it", readme)
	}
	staged := docExists(t, coreWithoutEEStagedPath)
	wantState := coreWithoutEEAppliedState
	if staged {
		wantState = coreWithoutEEStagedState
	}
	if status[1] != wantState {
		t.Errorf("%s says `core-without-ee.yml` is %q, and the staged copy %s: the line and the directory have to agree, in both directions -- the copy-in removes the staged copy and changes this line in the same commit", readme, status[1], map[bool]string{true: "exists", false: "is gone"}[staged])
	}
}
