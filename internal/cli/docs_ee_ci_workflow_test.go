package cli

// The staged "ee edition" workflow (ee-reorg plan task E1.6, design section 12 G1).
//
// G1 has two halves and the repository can prove only one of them offline:
// `internal/eeboundary` shows the core never imports, embeds or routes into
// `ee/`, and `core-without-ee.yml` (staged beside this file) deletes the
// directory and runs the suite. Neither shows that the *tagged* edition --
// the one `//go:build ee` compiles, and the one a Subscription Edition installation
// ships -- still builds and vets. That is a runner's job, and a runner is not a
// Fugaro run's to write: GitHub refuses a push that touches
// `.github/workflows/**`, so the file is staged under docs/pending-workflows/
// for the owner to move in (the plan's OWNER-RUN line, which moves this file
// and core-without-ee.yml in one commit).
//
// TestStagedCIBuildsBothEditions is the staged file's static shape until then,
// and it holds the moved file afterwards too: consoleWorkflowText reads the
// staged copy while it exists and the applied one once the move has taken it,
// so the same assertions are green before and after a step only a human
// performs. What is asserted is the fields GitHub executes and the claims the
// file makes about this repository:
//
//   - the trigger surface: a push to main and a manual dispatch, nothing else,
//     the same pair ci.yml and core-without-ee.yml carry;
//   - least privilege: contents: read at the top, and no secret anywhere in the
//     file;
//   - every `uses:` pinned by a full commit SHA with its `# vX.Y.Z` beside it,
//     Go taken from go.mod, and a job-level timeout-minutes;
//   - the tagged steps the plan names: `go vet -tags ee ./...` and
//     `go build -tags ee ./cmd/fugaro` both hand the go tool the `ee` tag, so
//     cmd/fugaro/ee_on.go is compiled and what is checked is the tagged
//     edition -- the other half of what core-without-ee.yml checks;
//   - `go test ./ee/... -count=1` carries no tag: the packages under ee/ build
//     without it (only cmd/fugaro/ee_on.go carries `//go:build ee`), so they are
//     tested in the default build too, which is where the core's own suite sees
//     them once E2.x has moved anything there;
//   - the ee test step is guarded on `go list ./ee/...`: ee/ holds no Go package
//     yet (E1.5 and the E2.x moves add the first ones) and `go test ./ee/...`
//     exits non-zero on a directory with no packages, so the step lists what is
//     there and says so rather than failing -- and never reports a pass it did
//     not run, because the real command runs the moment a package exists.
//
// What this test cannot do is run the job. Nothing here executes GitHub
// Actions, so the file has never run: the first run is the owner's, after the
// move, and the staging directory's README says so in the file's section.

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	// eeEditionStagedPath is the file this task stages: the whole workflow, in
	// the directory an agent run is allowed to write.
	eeEditionStagedPath = "docs/pending-workflows/ee-edition.yml"
	// eeEditionAppliedPath is where the owner moves it to. This test reads
	// whichever of the two exists rather than assuming either.
	eeEditionAppliedPath = ".github/workflows/ee-edition.yml"
	// eeEditionName is the workflow's `name:`, which is also what
	// `gh workflow run ee-edition` looks up.
	eeEditionName = "ee-edition"
	// eeEditionReadmeHeading opens the staging directory's section on this file:
	// the move the owner performs is written there.
	eeEditionReadmeHeading = "## `ee-edition.yml`"
	// eeEditionBuildTag is the tag that compiles cmd/fugaro/ee_on.go and the
	// tagged `ee/` packages. The vet and build steps must hand it to the go
	// tool; the test step must not, because the ee packages build untagged.
	eeEditionBuildTag = "ee"
	// eeEditionTimeoutMinutes is the job's own bound. The heaviest step is
	// `go vet -tags ee ./...`, the same compile ci.yml's test job does inside its
	// own 30-minute bound, so the job fits there with room for a slow runner.
	eeEditionTimeoutMinutes = 30
)

// eeEditionWorkflow is the file's shape: the keys GitHub executes, decoded with
// unknown fields refused, so a key nobody pins is a decode error here rather
// than a line this test silently ignores. `on` is the one raw node -- see
// eeEditionTriggers for why.
type eeEditionWorkflow struct {
	Name        string                  `yaml:"name"`
	On          yaml.Node               `yaml:"on"`
	Permissions map[string]string       `yaml:"permissions"`
	Jobs        map[string]eeEditionJob `yaml:"jobs"`
}

type eeEditionJob struct {
	RunsOn         string          `yaml:"runs-on"`
	TimeoutMinutes int             `yaml:"timeout-minutes"`
	Steps          []eeEditionStep `yaml:"steps"`
}

type eeEditionStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]any    `yaml:"with"`
	Env  map[string]string `yaml:"env"`
}

// eeEditionTrigger is one trigger the `on:` block declares: its name, and its
// body as a raw node.
type eeEditionTrigger struct {
	name string
	body yaml.Node
}

// eeEditionTriggers reads the `on:` block: the triggers it declares, in the
// order the file writes them, each with its own body.
//
// It is a raw YAML node rather than a typed struct because `workflow_dispatch:`
// has no body: decoding it into a pointer answers nil whether the key is absent
// or present-and-empty, and which triggers the file declares is the whole
// question here. A block that is not a mapping is a failure rather than an empty
// set, so a file this test cannot read does not pass as one with no triggers.
func eeEditionTriggers(t *testing.T, path string, on yaml.Node) []eeEditionTrigger {
	t.Helper()
	if on.Kind != yaml.MappingNode {
		t.Fatalf("%s: `on:` is not a mapping of triggers (kind %v): this test reads the triggers the file declares", path, on.Kind)
	}
	var out []eeEditionTrigger
	for i := 0; i+1 < len(on.Content); i += 2 {
		out = append(out, eeEditionTrigger{name: on.Content[i].Value, body: *on.Content[i+1]})
	}
	return out
}

// eeEditionTagFlagRE finds a `-tags` flag and the list it hands the go tool, in
// both spellings (`-tags ee`, `-tags=ee`), quoted or not, and anywhere a step
// may write it -- a command line, or a GOFLAGS value in a step's env.
var eeEditionTagFlagRE = regexp.MustCompile(`-tags[= ]\s*['"]?([^'"\s]*)`)

// eeEditionEmptyBranchRE matches the empty-directory branch of the ee test
// step: the guard condition, the message it prints, and the `exit 0` that ends
// the branch before the real command. The exit is what makes the step succeed
// on today's tree rather than merely say so: with the echo alone the branch
// falls through to `go test ./ee/...`, which exits non-zero on a directory with
// no packages, and the job goes red on main on the very tree this file lands
// on. Pinning the message without the exit is the one text-only pin that would
// leave that mutation green, so the branch is read as a sequence.
var eeEditionEmptyBranchRE = regexp.MustCompile(`(?m)^[ \t]*if \[ -z "\$\(go list \./ee/\.\.\. 2>/dev/null\)" \]; then[ \t]*\n[ \t]*echo [^\n]*\n[ \t]*exit 0[ \t]*\n[ \t]*fi[ \t]*$`)

// eeEditionTags is the tag list a step hands the go tool, split on the
// separators the flag accepts.
func eeEditionTags(s eeEditionStep) []string {
	var out []string
	values := make([]string, 0, len(s.Env)+1)
	values = append(values, s.Run)
	for _, v := range s.Env {
		values = append(values, v)
	}
	for _, v := range values {
		for _, m := range eeEditionTagFlagRE.FindAllStringSubmatch(v, -1) {
			out = append(out, strings.FieldsFunc(m[1], func(r rune) bool { return r == ',' || r == ' ' })...)
		}
	}
	return out
}

// readEEEditionWorkflow returns the workflow's text, the path it came from, and
// its decoded shape. The bytes come from consoleWorkflowText, the reader every
// staged workflow in this package uses: the staged copy while it exists, the
// applied one once the owner's move has taken it, and a byte-identity clause
// while both are there.
func readEEEditionWorkflow(t *testing.T) (string, string, eeEditionWorkflow) {
	t.Helper()
	text, path := consoleWorkflowText(t, eeEditionStagedPath, eeEditionAppliedPath)
	var wf eeEditionWorkflow
	dec := yaml.NewDecoder(bytes.NewReader([]byte(text)))
	dec.KnownFields(true)
	if err := dec.Decode(&wf); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return text, path, wf
}

// TestStagedCIBuildsBothEditions: the staged job builds and vets the tagged
// edition, tests the untagged ee packages, and is safe to move in.
func TestStagedCIBuildsBothEditions(t *testing.T) {
	text, path, wf := readEEEditionWorkflow(t)

	if wf.Name != eeEditionName {
		t.Errorf("%s is named %q, want %q: the name is what `gh workflow run` looks up", path, wf.Name, eeEditionName)
	}

	// --- The trigger surface ------------------------------------------------
	// A push to main and a manual dispatch, in that order and nothing else, the
	// same pair ci.yml and core-without-ee.yml carry: the tagged edition is what
	// ships, so it is checked where a release is cut from. The decode above
	// refuses an unknown key anywhere in the file, so a third trigger cannot
	// slip in beside these two.
	triggers := eeEditionTriggers(t, path, wf.On)
	var names []string
	for _, tr := range triggers {
		names = append(names, tr.name)
	}
	if want := []string{"push", "workflow_dispatch"}; !slices.Equal(names, want) {
		t.Errorf("%s triggers on %q, want %q: a push to main is where a release is cut from, and the dispatch is how the owner starts it by hand", path, names, want)
	}
	if len(triggers) > 0 && triggers[0].name == "push" {
		push := triggers[0].body
		if push.Kind != yaml.MappingNode || len(push.Content) != 2 {
			t.Fatalf("%s: the push trigger carries more than a branch filter (kind %v, %d keys): a paths filter would leave the job unrun on the change that breaks the tagged build", path, push.Kind, len(push.Content)/2)
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
	// the pin reviewable, the way the console's staged files and
	// core-without-ee.yml are held: a tag is a mutable name on a repository this
	// one does not control, and Dependabot does not watch
	// docs/pending-workflows/.
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

	// One job, with a bound of its own. A hung compile under GitHub's six-hour
	// default holds a runner all afternoon; the bound is what makes a hang a
	// failed run instead.
	if len(wf.Jobs) != 1 {
		t.Fatalf("%s declares %d jobs, want exactly 1: the file is one job's shape, and a second one would be a different check", path, len(wf.Jobs))
	}
	var jobName string
	var job eeEditionJob
	for name, j := range wf.Jobs {
		jobName, job = name, j
	}
	if jobName != eeEditionName {
		t.Errorf("%s's job is named %q, want %q: the check's name is what a reader sees in the Actions list", path, jobName, eeEditionName)
	}
	if job.RunsOn != "ubuntu-latest" {
		t.Errorf("%s runs on %q, want ubuntu-latest: the job builds and tests the module, which needs no other platform", path, job.RunsOn)
	}
	if job.TimeoutMinutes != eeEditionTimeoutMinutes {
		t.Errorf("%s's job timeout-minutes is %d, want %d: the job compiles the whole module for the tagged vet, and a job without a bound runs to GitHub's six-hour default", path, job.TimeoutMinutes, eeEditionTimeoutMinutes)
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

	// --- The three steps the plan names -------------------------------------
	// The tagged vet and the tagged build hand the go tool `ee`, so
	// cmd/fugaro/ee_on.go is compiled and what is checked is the edition a
	// Subscription Edition installation ships -- the half core-without-ee.yml does not
	// check. The ee test step carries no tag: the packages under ee/ build
	// without it, so they are covered in the default build too.
	find := func(cmd string) (int, *eeEditionStep) {
		for i := range job.Steps {
			if strings.Contains(job.Steps[i].Run, cmd) {
				return i, &job.Steps[i]
			}
		}
		return -1, nil
	}
	for _, c := range []struct {
		cmd, why string
		tagged   bool
	}{
		{"go vet -tags ee ./...", "the tagged tree has to vet, not only build", true},
		{"go build -tags ee ./cmd/fugaro", "the tagged binary has to build, which is what compiles cmd/fugaro/ee_on.go", true},
		{"go test ./ee/... -count=1", "the ee packages' tests have to run", false},
	} {
		i, s := find(c.cmd)
		if s == nil {
			t.Errorf("%s never runs %q: %s", path, c.cmd, c.why)
			continue
		}
		got := slices.Contains(eeEditionTags(*s), eeEditionBuildTag)
		if got != c.tagged {
			t.Errorf("%s's step %d runs %q and hands the go tool the tags %q: want the `ee` tag %v -- %s", path, i, strings.TrimSpace(s.Run), eeEditionTags(*s), c.tagged, c.why)
		}
	}

	// The ee test step is guarded on a package listing, because ee/ holds no Go
	// package yet (E1.5 and the E2.x moves add the first ones) and
	// `go test ./ee/...` exits non-zero on a directory with no packages. The
	// guard says so and **exits zero**: the exit is what keeps the step green
	// on today's tree, so the branch is read as the whole sequence -- listing,
	// message, exit -- rather than as the text of the message alone.
	_, testStep := find("go test ./ee/... -count=1")
	if testStep != nil && !eeEditionEmptyBranchRE.MatchString(testStep.Run) {
		t.Errorf("%s's ee test step runs %q: the empty-directory branch has to list the packages under ee/ (`go list ./ee/...`), say in the log that there is nothing to test, and `exit 0` before the real command -- with the echo alone the branch falls through to `go test ./ee/...`, which exits non-zero on a directory with no packages (E1.6 lands before E1.5 and the E2.x moves), so the job would go red on main on the tree this file lands on", path, strings.TrimSpace(testStep.Run))
	}

	// The job builds the tree, so it must not delete ee/ the way
	// core-without-ee.yml does: the two files are the two halves of G1, and a
	// deletion here would check the untagged core twice.
	for _, s := range job.Steps {
		if strings.Contains(s.Run, "rm -rf ee") {
			t.Errorf("%s deletes ee/ (%q): that is core-without-ee.yml's job, and this one is the tagged edition's -- ee/ has to be there for the tagged build", path, strings.TrimSpace(s.Run))
		}
	}

	// --- The staging directory says how to move it in ------------------------
	// The file is a proposal a person applies by hand, so the place they read
	// carries the command and says what happens to the staged copy afterwards,
	// and that core-without-ee.yml is due with it: the two halves of G1 go in
	// together.
	const readme = "docs/pending-workflows/README.md"
	doc := readDoc(t, readme)
	section := consoleReadmeSection(t, doc, eeEditionReadmeHeading)
	for _, want := range []string{
		"git mv docs/pending-workflows/ee-edition.yml .github/workflows/",
		"git mv docs/pending-workflows/core-without-ee.yml .github/workflows/",
		// The other half of G1 is due with this file: the section has to say so,
		// or the owner moves one of the two in and leaves the claim half-proved.
		"`core-without-ee.yml` is due",
		// The test that pins the file, so a reader knows what is verified and
		// what is not.
		"TestStagedCIBuildsBothEditions",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's `ee-edition.yml` section lacks %q: the owner is the one who moves the file in, and this is where they read what it is, why it is staged and what to do after the move", readme, want)
		}
	}
}
