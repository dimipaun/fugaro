package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// computeBackendPlan is the plan this file pins: docs/plans/2026-10-19-compute-backend.md,
// the implementation plan for the Cloud Batch compute backend (design
// docs/design/compute-backend.md), written against the tree branch
// fugaro/20261019-140014-b55a stands on (= the branch `integration-0.9.5`, cut
// from `integration-0.9.0` at `af953e5e`; the branch's own commits are this plan
// and this test, and no code).
const computeBackendPlan = "docs/plans/2026-10-19-compute-backend.md"

// TestDocsComputeBackendCitationsExist: every path the plan cites is a path a
// later implementer has to find — the seam, the Batch package, the Terraform
// root the provisioning stage applies, the register the check number is read
// from, the pins the tasks are told to update. A substring assertion cannot see
// one of them move, so each is stat'ed here as well as required in the text.
//
// Two kinds of citation are deliberately NOT in the list below.
//   - The files the plan's "File Structure" section says it CREATES
//     (internal/cli/backends.go, internal/cli/reaper.go,
//     internal/cli/docs_compute_backend_test.go, deploy/terraform/gcp/modules/compute/reaper.tf):
//     they do not exist yet, and this plan is docs-only, so stat'ing them would
//     fail on the tree the plan is written for.
//   - The placeholders and globs (docs/releases/<the 0.9.5 note>,
//     internal/runner/**, .github/workflows/ci.yml's `for r in …` loop): a
//     placeholder is not a path and a glob is not a file.
func TestDocsComputeBackendCitationsExist(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	for _, cited := range []string{
		// The design and the documents it is read against.
		"docs/design/compute-backend.md",
		"docs/design/budget-token-channel.md",
		// The boundary that keeps an artifact-producing run on Cloud Run:
		// the plan's Global Constraint 13 and Task 8's fourth refusal are
		// written from it, and it is the one design this plan must not
		// contradict.
		"docs/design/artifact-runs.md",
		"docs/gcp-live-checklist.md",
		"docs/plans/2026-10-16-managed-projects.md",
		"docs/plans/2026-10-14-project-centric.md",
		"docs/releases/v0.8.6.md",
		"docs/release.md",
		"docs/gcp-setup.md",
		"docs/pending-workflows/README.md",
		"SECURITY.md",
		"CONTRIBUTING.md",
		"plugin/skills/setup/SKILL.md",
		// The seam, which is what Task 8 wires.
		"internal/backend/backend.go",
		"internal/backend/registry.go",
		"internal/backend/codec.go",
		"internal/backend/platform.go",
		"internal/backend/launchenv.go",
		"internal/backend/gcp/limits.go",
		// The Batch package, built and unreachable.
		"internal/backend/batch/batch.go",
		"internal/backend/batch/run.go",
		"internal/backend/batch/reap.go",
		"internal/backend/batch/logs.go",
		"internal/backend/batch/names.go",
		"internal/backend/batch/prices.go",
		"internal/backend/batch/batch_test.go",
		"internal/backend/batch/reap_test.go",
		"internal/gcpfake/batch.go",
		"internal/gcpfake/compute.go",
		// The CLI sites the three TODO(compute-backend Task 8) markers sit in,
		// and the stage that refuses --provision-batch.
		"internal/cli/cloud.go",
		"internal/cli/run.go",
		"internal/cli/validate.go",
		"internal/cli/doctor.go",
		"internal/cli/root.go",
		"internal/cli/recipes_skew.go",
		"internal/cli/init_compute.go",
		"internal/cli/init_stages.go",
		"internal/cli/launch_budget.go",
		"internal/cli/docs_artifact_runs_test.go",
		"internal/localcfg/localcfg.go",
		"internal/localcfg/localcfg_test.go",
		"internal/infra/compute.go",
		// The Terraform the provisioning stage applies, and the model Task 11 follows.
		"deploy/terraform/gcp/roots/compute",
		"deploy/terraform/gcp/modules/compute/apis.tf",
		"deploy/terraform/gcp/modules/compute/iam.tf",
		"deploy/terraform/gcp/modules/compute/network.tf",
		"deploy/terraform/gcp/modules/compute/outputs.tf",
		"deploy/terraform/gcp/modules/compute/preload.sh",
		"deploy/terraform/gcp/modules/compute/preload.tf",
		"deploy/terraform/gcp/modules/compute/variables.tf",
		"deploy/terraform/gcp/modules/installation/history.tf",
		"deploy/terraform/gcp/modules/installation/scheduler.tf",
		"deploy/terraform/terraform_test.go",
	} {
		// The plan links its neighbours relatively (../design/…, ../../SECURITY.md)
		// in some places and writes the repository-root path in others (the
		// tables and the File Structure blocks), so a citation counts in either
		// form.
		rel, err := filepath.Rel("docs/plans", cited)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc, cited) && !strings.Contains(doc, rel) {
			t.Errorf("%s no longer cites %s: this list is the plan's own citations, so one that has left the text must leave this test", computeBackendPlan, cited)
			continue
		}
		if _, err := os.Stat("../../" + cited); err != nil {
			t.Errorf("%s cites %s, which is not in the tree: %v", computeBackendPlan, cited, err)
		}
	}
}

// computeBackendSymbols maps each file the plan cites to the symbols it says
// are in it. The plan's tasks are written in terms of these names — "the opener
// map", "the three TODO(compute-backend Task 8) sites", "batchProvisionShipped
// flips" — so a rename on either side has to fail here rather than in a
// reviewer's head. Each entry is a symbol the plan asserts EXISTS today, not
// one a task will add.
//
// Every entry carries at least one symbol, and the test fails on one that does
// not: an entry with an empty list runs zero assertions and turns this map into
// a file-existence check, which is the drift it exists to catch. A file the plan
// cites but names no symbol in — the logs, names and prices files, the two
// gcpfake packages, reap_test.go and the two init stages, each cited as
// "already built" rather than as a rename target — is therefore **not** an entry
// here; TestDocsComputeBackendCitationsExist is what holds those.
var computeBackendSymbols = map[string][]string{
	"internal/backend/backend.go":   {"CloudRun", "CloudBatch"},
	"internal/backend/registry.go":  {"Opener", "func Open"},
	"internal/backend/codec.go":     {"Codec", "CloudRunCodec", "LocalCodec", "BatchCodec", "ParseExecution", "SameExecution", "ValidateRun", "ParseInRegion", "FromEnv"},
	"internal/backend/platform.go":  {"Platform", "LocalPlatform", "DetectPlatform", "InCloud", "ExecutionFromEnv", "OnPlatform"},
	"internal/backend/launchenv.go": {"EnvVar", "LaunchEnv", "PlatformEnvNames", "ReservedEnvNames"},
	"internal/backend/gcp/limits.go": {
		// Task 8 says the VM backend ships its own resource check rather than
		// Cloud Run's; this is the Cloud Run one it is contrasted with.
		"cpuChoices",
	},
	"internal/backend/batch/batch.go": {
		"func New", "Options", "Docker", "SocketGID", "HostSocketPath",
		"checkDocker", "checkSecrets", "checkVM", "checkNetwork",
	},
	"internal/backend/batch/run.go": {
		"jobDefinition", "func (b *Backend) Launch", "SecretVariables",
	},
	"internal/backend/batch/reap.go": {
		"Reaper", "NewReaper", "func (r *Reaper) Reap", "ReaperOptions",
		"ActionDeleteJob", "ActionCancelJob", "ActionDeleteVM", "Report",
		"ReapMargin", "MaxDeadline", "QueueDeadline",
	},
	"internal/backend/batch/batch_test.go": {
		// Task 9 and Task 14 both name this test as the pin they follow, and
		// the plan's "no literal 412 anywhere" claim rests on this constant.
		"TestLaunchRefusesASecretEnv", "testSocketGID",
	},
	"internal/cli/cloud.go": {"openCloudFor", "checkExecution"},
	"internal/cli/run.go":   {"Backend: backend.CloudRun"},
	"internal/cli/recipes_skew.go": {
		"batchProvisionShipped", "batchProvisionDisabled", "refuseBatchProvisionNotShipped",
	},
	"internal/cli/launch_budget.go": {"budgetMintOn"},
	"internal/cli/root.go":          {"AddCommand"},
	"internal/localcfg/localcfg.go": {"BackendName", "func (c *Config) Backend"},
	"internal/localcfg/localcfg_test.go": {
		// Task 10 turns this test into the acceptance it replaced, so the plan
		// naming it is the plan naming what it must not leave stale.
		"TestCloudBatchIsRefused",
	},
	"internal/infra/compute.go": {
		"ComputeNames", "ComputeSpec", "ComputeVars", "ComputeServiceAgents",
		"ComputeCover", "ComputeWorkdir", "ComputeOutputs", "JobRunAccounts",
		"RunSchedulerJob", "PreloadedImage", "propagating",
	},
	"deploy/terraform/gcp/modules/compute/iam.tf": {
		// The permission Task 9 removes, and the resource that names it.
		"google_project_iam_custom_role", "batch.jobs.cancel", "batch.jobs.delete",
	},
	"deploy/terraform/gcp/modules/compute/preload.tf": {
		"google_cloud_scheduler_job", "preload_rebuild",
	},
	"deploy/terraform/gcp/modules/installation/history.tf": {
		// The precedent Task 11 follows, line for line.
		"google_cloud_run_v2_job", "google_cloud_scheduler_job", "history_sweep",
	},
	"deploy/terraform/terraform_test.go": {
		// The two pins Task 9 updates in the same commit as the permission list.
		"TestComputeReaperRoleIsExactlySix", "TestCustomRolePermissionsAreAllowedByGoogle", "knownInvalid",
	},
	"internal/cli/docs_artifact_runs_test.go": {
		// The shape Task 12's TestChecklistHasCheck57 is written in.
		"TestChecklistHasCheck58", "check58Guard",
	},
}

// computeBackendTodoMarker is the marker the design's Task 8 left at each call
// site the seam has not reached, and which the plan's Task 8 removes with the
// call site it marks.
const computeBackendTodoMarker = "TODO(compute-backend Task 8)"

// computeBackendCommentMarkers is the other half of the table, as counts: names
// the plan cites that live in comments rather than in code, so they are checked
// against the file as written. **The count is the assertion.** One Contains per
// file passes with one of run.go's two markers deleted — the file still carries
// the marker, so it reads as present while a call site has lost the note that
// says Task 8 owns it — and the plan's own sentence is a distribution, not a
// total: one in `internal/cli/cloud.go`, two in `internal/cli/run.go`. The three
// are the whole of it, and TestDocsComputeBackendTodoMarkersAreCounted walks the
// repository to hold both halves of that sentence.
var computeBackendCommentMarkers = map[string]int{
	"internal/cli/cloud.go": 1,
	"internal/cli/run.go":   2,
}

// stripLineComments removes a file's line comments (`//` in Go, `#` in HCL),
// so a symbol that has been renamed but whose own doc comment still names it
// does not read as present. That is the realistic drift this test is for: a
// rename that updates the identifier and leaves the comment behind would
// otherwise pass, and the plan's tasks are written as instructions to rename
// exactly these names.
func stripLineComments(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		for _, mark := range []string{"//", "#"} {
			if i := strings.Index(line, mark); i >= 0 {
				line = line[:i]
			}
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestDocsComputeBackendSymbolsExist: the plan's tasks are written in terms of
// names in other packages — the opener map, the constant that refuses
// --provision-batch, the six-permission role, the test that pins the seal's
// refusal. Each is asserted present in the file the plan says it is in, and
// asserted in that file's **code** rather than anywhere in its text, so a
// rename fails here rather than leaving a task that tells an implementer to
// change something that no longer exists.
func TestDocsComputeBackendSymbolsExist(t *testing.T) {
	read := func(path string) (string, bool) {
		data, err := os.ReadFile("../../" + path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			return "", false
		}
		return string(data), true
	}
	for path, symbols := range computeBackendSymbols {
		if len(symbols) == 0 {
			t.Errorf("%s is an entry in computeBackendSymbols with no symbols, so it asserts nothing but the file's existence: either name the symbol %s's tasks use in it, or leave the file to TestDocsComputeBackendCitationsExist", path, computeBackendPlan)
			continue
		}
		text, ok := read(path)
		if !ok {
			continue
		}
		code := stripLineComments(text)
		for _, sym := range symbols {
			if !strings.Contains(code, sym) {
				t.Errorf("%s no longer contains %q in its code, which %s's tasks are written in terms of: a rename on either side must fail here", path, sym, computeBackendPlan)
			}
		}
	}
}

// TestDocsComputeBackendTodoMarkersAreCounted: the plan says the three
// `TODO(compute-backend Task 8)` markers are the whole of what the seam has not
// reached — one in internal/cli/cloud.go, two in internal/cli/run.go — and that
// Task 8 removes each one with the call site it marks. A per-file Contains
// cannot hold that: deleting one of run.go's two markers leaves the other one
// answering for both, so the file still reads as "the marker is there" while a
// call site has lost the note that says Task 8 owns it.
//
// So the markers are counted, per file and in total, and the count is held
// against a walk of the repository in both directions: a marker the table does
// not name is a call site the plan has not accounted for, and a count that has
// dropped is a marker removed without its call site moving. This file is the one
// path the walk skips, because it quotes the marker in its own text.
func TestDocsComputeBackendTodoMarkersAreCounted(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	// The distribution is the plan's sentence, not this test's invention, so the
	// counts below and the plan fail together.
	if !strings.Contains(doc, "one in `internal/cli/cloud.go`, two in `internal/cli/run.go`") {
		t.Errorf("%s no longer says where the three markers are (one in `internal/cli/cloud.go`, two in `internal/cli/run.go`): the counts in this test are that sentence", computeBackendPlan)
	}
	if !strings.Contains(doc, "the three `TODO(compute-backend Task 8)` sites") {
		t.Errorf("%s no longer says the three `TODO(compute-backend Task 8)` sites are the whole of what the seam has not reached", computeBackendPlan)
	}
	seen := map[string]int{}
	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel := strings.TrimPrefix(filepath.ToSlash(p), "../../")
		// This file is where the marker is quoted as a name (the constant, the
		// comments and the table), so it is the one path skipped by name.
		if rel == "internal/cli/docs_compute_backend_test.go" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if n := strings.Count(string(data), computeBackendTodoMarker); n > 0 {
			seen[rel] = n
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for path, want := range computeBackendCommentMarkers {
		total += want
		got, ok := seen[path]
		if !ok {
			t.Errorf("%s carries no %q marker, and %s's Task 8 names the call site in it as one of the three: either the marker went before the call site did, or the call site moved and this table has to move with it", path, computeBackendTodoMarker, computeBackendPlan)
			continue
		}
		if got != want {
			t.Errorf("%s carries %d %q markers, want %d: %s counts the three markers rather than checking one substring per file, so a file that still carries one is not a file whose other marker may go unnoticed", path, got, computeBackendTodoMarker, want, computeBackendPlan)
		}
	}
	if total != 3 {
		t.Errorf("computeBackendCommentMarkers accounts for %d markers, and %s says the three `TODO(compute-backend Task 8)` sites are the whole of it", total, computeBackendPlan)
	}
	for path, n := range seen {
		if _, ok := computeBackendCommentMarkers[path]; !ok {
			t.Errorf("%s carries %d %q markers and is not in computeBackendCommentMarkers: the three the plan names are the whole of it, so a fourth site is either a call site the plan has not accounted for or a marker a task left behind", path, n, computeBackendTodoMarker)
		}
	}
}

// TestDocsComputeBackendNamesItsBase: every citation in this plan is written
// against one tree, and the plan names that tree as a branch plus the commit the
// branch was cut from — not as a remote-tracking ref. The distinction is not
// pedantry: a clone whose fetch refspec maps `main` alone does not carry
// `origin/integration-0.9.5`, so a base named as that ref is a base a reviewer
// goes looking for and does not find, which is what happened. A test in this
// package cannot ask the remote; the fetch command in the same sentence is how a
// reader checks the name, and this test is what holds the plan to it.
func TestDocsComputeBackendNamesItsBase(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	// The base as one sentence, so a wrong branch, a wrong parent line, a wrong
	// commit or a wrong date each fails by name rather than passing on the two
	// halves that happen to be right.
	const base = "**`integration-0.9.5`, cut from `integration-0.9.0` at `af953e5e` on 2026-10-19**"
	if !strings.Contains(doc, base) {
		t.Errorf("%s no longer names its base as %q: the plan's whole citation set is written against that tree, so a base whose branch, parent line, commit or date has moved is a claim a reviewer cannot check", computeBackendPlan, base)
	}
	for _, want := range []string{
		"**The base is named as that branch and the commit it was cut from rather than as a remote-tracking ref, because a default clone does not carry the ref**",
		"git fetch origin integration-0.9.5",
		"the owner moved this work, and the managed redesign with it, out of 0.9.0",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer says %q: the first is the shape the base is named in, the second is how a clone that does not carry the ref resolves it, and the third is why the base is the 0.9.5 line and not 0.9.0", computeBackendPlan, want)
		}
	}
}

// TestDocsComputeBackendTasksThatFalsifyThePinNameIt: Global Constraint 11 says
// this plan's own pin test is a pin like the others, names the four tasks that
// falsify it — 8, 9, 10 and 12 — and says each of them names the file in its own
// Files. A task that removes a symbol the pin asserts without touching the pin
// is a commit that does not build; a task that edits the pin without saying so
// in its Files is a change the reviewer of that one task does not see coming.
//
// So each of the four Files lines is read out of the plan and asserted to name
// the file, and every other task's is asserted not to: the constraint's list is
// the whole of it, in the way Global Constraint 8's is of the flagged headings.
func TestDocsComputeBackendTasksThatFalsifyThePinNameIt(t *testing.T) {
	const pinTest = "internal/cli/docs_compute_backend_test.go"
	doc := readDoc(t, computeBackendPlan)
	if !strings.Contains(doc, "Tasks 8, 9, 10 and 12 each name that file in their own Files and say what flips") {
		t.Errorf("%s no longer says which tasks name the pin test in their Files: Global Constraint 11 is where a reviewer of a single task learns that this file is one of the pins that task has to carry", computeBackendPlan)
	}
	// Each task's own Files line, taken from its heading to the next one.
	heading := regexp.MustCompile(`(?m)^### Task (\d+):`)
	locs := heading.FindAllStringSubmatchIndex(doc, -1)
	files := map[string]string{}
	for i, m := range locs {
		end := len(doc)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		for _, line := range strings.Split(doc[m[0]:end], "\n") {
			if strings.HasPrefix(line, "**Files:**") {
				files[doc[m[2]:m[3]]] = line
				break
			}
		}
	}
	if len(files) != 7 {
		t.Errorf("%s carries %d task Files lines, want 7 (one per task): the four that name the pin test are read out of them", computeBackendPlan, len(files))
	}
	named := []string{"8", "9", "10", "12"}
	for _, n := range named {
		line, ok := files[n]
		if !ok {
			t.Errorf("%s has no Files line for Task %s, which Global Constraint 11 says names the pin test", computeBackendPlan, n)
			continue
		}
		if !strings.Contains(line, pinTest) {
			t.Errorf("Task %s's Files line no longer names %s: Global Constraint 11 says the tasks that falsify the pin test name it in their own Files, and Task %s is one of the four", n, pinTest, n)
		}
	}
	for n, line := range files {
		if !strings.Contains(line, pinTest) {
			continue
		}
		isNamed := false
		for _, want := range named {
			if want == n {
				isNamed = true
			}
		}
		if !isNamed {
			t.Errorf("Task %s's Files line names %s and Global Constraint 11 lists only Tasks 8, 9, 10 and 12: a fifth task that flips the pin test is a task the constraint has to name, and one that merely reads it is a task that has misread what the file is for", n, pinTest)
		}
	}
}

// TestDocsComputeBackendPlanReservesNoCheckNumber: the plan must not claim a
// check number of its own. 57 is the compute backend's already — its summary
// row is in the register and its section lives in the design's §7.2 — and the
// register's ruling is that a number, once reserved, is never reused or
// renumbered. The plan's new live steps are 57b sub-letters, which is the
// convention design Task 6 already used when it added 57b steps 1 and 7.
func TestDocsComputeBackendPlanReservesNoCheckNumber(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	if !strings.Contains(doc, "**This plan reserves nothing.**") {
		t.Errorf("%s no longer says it reserves nothing; a plan that takes a number says which one and why", computeBackendPlan)
	}
	// The three steps this plan does add, each as a sub-letter of 57b.
	for _, want := range []string{"57b-10", "57b-11", "57a-7"} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer names %s among the steps it adds or re-runs", computeBackendPlan, want)
		}
	}
	if !strings.Contains(doc, "## Check numbers this plan takes (57, and no new number)") {
		t.Errorf("%s's check-number heading no longer reads as the 57-only one it is", computeBackendPlan)
	}
	// A fresh top-level reservation would have to be one of these forms, and
	// none may appear: the plan takes 57 and nothing above it.
	for _, banned := range []string{"reserves **58**", "reserves **59**", "takes **58**", "takes **59**", "takes two numbers"} {
		if strings.Contains(doc, banned) {
			t.Errorf("%s now looks like it reserves a number of its own (%q): 57 is the compute backend's and 58 onward are taken", computeBackendPlan, banned)
		}
	}
}

// TestDocsComputeBackendCheck57IsStillOnlyARow: the plan's Task 12 is the task
// that writes Check 57 into the register, so until it lands the register must
// still carry 57's summary row and no `## Check 57` section — and the register
// says so in its own words. If the section appears, Task 12 has landed and this
// test (and the plan's claim that the row is re-pointed by it) is what has to
// be updated, in the same commit.
func TestDocsComputeBackendCheck57IsStillOnlyARow(t *testing.T) {
	checklist := readDoc(t, "docs/gcp-live-checklist.md")
	if !strings.Contains(checklist, "| 57 | The compute-instance Docker backend") {
		t.Fatalf("docs/gcp-live-checklist.md no longer carries 57's summary row: %s's whole check-number section is written against it", computeBackendPlan)
	}
	// A heading, not the sentence that explains its absence: the register says
	// "There is no `## Check 57` section in this file yet" in prose, and that
	// sentence is asserted below.
	if strings.Contains(checklist, "\n## Check 57") {
		t.Fatalf("docs/gcp-live-checklist.md now carries a `## Check 57` section: Task 12 of %s has landed, so that task's heading and this test both have to change in the same commit", computeBackendPlan)
	}
	if !strings.Contains(checklist, "There is no `## Check 57` section in this file yet") {
		t.Errorf("docs/gcp-live-checklist.md no longer says why it has no `## Check 57` section; %s's Task 12 step 1 quotes that sentence", computeBackendPlan)
	}
	// The design is where the body lives until then, and the plan's Task 12 is
	// written from it.
	design := readDoc(t, "docs/design/compute-backend.md")
	if !strings.Contains(design, "## 7. The measurement: Check 57") {
		t.Errorf("docs/design/compute-backend.md no longer carries §7, which %s's Task 12 writes the register's section from", computeBackendPlan)
	}
}

// TestDocsComputeBackendDecisionsAreRecommendations: the plan's own decisions
// are P1 to P8, every one of them a recommendation and none an answer, and the
// gate at the foot is UNAPPROVED. A row that acquires an owner's answer must
// acquire it here and in the gate's own text, so this test is what makes
// "answered silently" impossible.
func TestDocsComputeBackendDecisionsAreRecommendations(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	for i := 1; i <= 8; i++ {
		row := fmt.Sprintf("| **P%d** |", i)
		if !strings.Contains(doc, row) {
			t.Errorf("%s no longer carries the %s decision row", computeBackendPlan, row)
		}
	}
	decisions := section(t, doc, "## Decisions this plan defers to the owner", "## Open items")
	if !strings.Contains(decisions, "**None is answered**") {
		t.Errorf("%s: the decisions section no longer says none of its rows is answered", computeBackendPlan)
	}
	// The three the gate says change what gets built, named there as such.
	// The gate is the file's last section, so it runs to the end of the file.
	i := strings.Index(doc, "## OWNER APPROVAL GATE")
	if i < 0 {
		t.Fatalf("%s has no OWNER APPROVAL GATE: the plan's whole approval story is that section", computeBackendPlan)
	}
	gate := doc[i:]
	for _, want := range []string{
		"**UNAPPROVED. Nothing in this plan is approved",
		"**P1 — the seal's carrier.**",
		"**P2 — the reaper's impossible permission.**",
		"**P5 — the release.**",
		// Task 12 is the owner's alone, and the gate has to say so.
		"**Task 12 is OWNER-RUN**",
	} {
		if !strings.Contains(gate, want) {
			t.Errorf("%s's OWNER APPROVAL GATE no longer says %q", computeBackendPlan, want)
		}
	}
}

// TestDocsComputeBackendTasksCarryTheirAnatomy: every task the plan's phases
// describe has the anatomy the repository's other plans use — a Files line, a
// Depends on line, a Commit line and a red step — and the security-sensitive
// ones carry the flag in their own heading. A task that loses one of these is a
// task a reviewer cannot schedule or review, which is what the format exists to
// prevent.
func TestDocsComputeBackendTasksCarryTheirAnatomy(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	for _, want := range []string{
		"### Task 8: The backend the installation selects — the opener, its configuration, and the seam's call sites — SECURITY-SENSITIVE, independent opus review",
		"### Task 9: The provisioning that completes — the reaper's role, the recorded gid, and `--provision-batch` — SECURITY-SENSITIVE, independent opus review",
		"### Task 11: The reaper's invocation — `fugaro reaper`, its Cloud Run job and its Scheduler job — SECURITY-SENSITIVE, independent opus review",
		"### Task 10: The enable — `backend: cloud-batch` is accepted, and `doctor` gates it — SECURITY-SENSITIVE, independent opus review",
		"### Task 12: Check 57b, the register's section, the docs and the release bullet — OWNER-RUN",
		"### Task 14: The budget seal's carrier on Batch — the answer, and the refusal it keeps — SECURITY-SENSITIVE, independent opus review",
		"### Task 13: The runner-side Secret Manager fetch — only if Task 14's answer needs it",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer carries the task heading\n  %q", computeBackendPlan, want)
		}
	}
	// Seven tasks, seven of each anatomy line. Counting rather than spot
	// checking is the point: a task added without a red step is the failure
	// this catches.
	for _, want := range []struct {
		line string
		n    int
	}{
		{"**Red step — `Expected: FAIL`**", 7},
		{"**Depends on:**", 7},
		{"**Commit:**", 7},
		{"**Files:**", 7},
	} {
		if got := strings.Count(doc, want.line); got != want.n {
			t.Errorf("%s carries %d %q lines, want %d (one per task)", computeBackendPlan, got, want.line, want.n)
		}
	}
	// The three tasks the design's §8.1 outline has no home for, named as such
	// where a reader of the mapping table finds them.
	for _, want := range []string{
		"| — (no home in the outline) | **Task 9**",
		"| — (no home in the outline) | **Task 11**",
		"| — (no home in the outline) | **Task 14**",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s's §8.1 mapping table no longer records %q", computeBackendPlan, want)
		}
	}
}

// TestDocsComputeBackendKeepsTheArtifactBoundary: the Batch backend's account
// is not per-repository, so docs/design/artifact-runs.md §8.5 and its D11 keep
// an artifact-producing run on the plain per-repository Cloud Run backend until
// a per-run prefix condition or a per-run account exists — and that design says
// in its own words that no code enforces it today and that the plan which
// enables artifact runs on another backend must bring the per-run boundary with
// it. This plan is that plan's neighbour, so the boundary is asserted twice:
// that the design really says it (the citation is not invented), and that the
// plan refuses the combination in both the task that adds the checks and the
// task that lifts the refusal.
func TestDocsComputeBackendKeepsTheArtifactBoundary(t *testing.T) {
	const design = "docs/design/artifact-runs.md"
	artifacts := readDoc(t, design)
	for _, want := range []string{
		"an artifact-producing run stays on the plain per-repository Cloud Run backend until a per-run prefix condition or a per-run account exists",
		"**No code in this slice enforces that, and this document does not claim it does**",
		"must bring the per-run boundary with it",
	} {
		if !strings.Contains(artifacts, want) {
			t.Errorf("%s no longer says %q: %s's Global Constraint 13 quotes that sentence as the boundary its enable must not cross", design, want, computeBackendPlan)
		}
	}
	doc := readDoc(t, computeBackendPlan)
	for _, want := range []string{
		"docs/design/artifact-runs.md",
		"13. **An artifact-producing run is never launched on the Batch backend by this plan",
		"`artifacts:` with `backend: cloud-batch` is an error",
		"**the artifact boundary asserted to survive the enable**",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer carries %q, which is the artifact boundary it must leave standing", computeBackendPlan, want)
		}
	}
}

// TestDocsComputeBackendNamesEveryChecklistTest: the plan's own text enumerates
// what `grep -n 'func TestChecklist'` finds in this package, and it does so to
// make a claim about the tree rather than about the plan — the design's §8.1
// names `TestChecklistChecksAreGuarded` as the project-centric plan's backstop,
// and the plan's Task 12 has to write `TestChecklistHasCheck57` in the shape of
// the tests that do exist rather than of a backstop that is not there.
//
// An enumeration written into a document goes stale the moment a check gets a
// test of its own, and this one says "and nothing else". So the sentence is held
// to the tree in both directions: every `func TestChecklist…` in this package is
// named in the plan, the count the plan states is the count in the tree, and the
// backstop the plan says is absent is still absent.
func TestDocsComputeBackendNamesEveryChecklistTest(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^func (TestChecklist\w*)\(`)
	var found []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(data), -1) {
			found = append(found, m[1])
		}
	}
	sort.Strings(found)
	if len(found) == 0 {
		t.Fatalf("no `func TestChecklist…` in internal/cli: the plan's open item 9 enumerates them, so the enumeration has come loose from the tree")
	}
	for _, name := range found {
		if !strings.Contains(doc, name) {
			t.Errorf("internal/cli declares %s, which %s's open item 9 does not name: that item enumerates what `grep -n 'func TestChecklist'` finds and says \"and nothing else\", so a new checklist test has to be added there (or that claim dropped) in the same commit", name, computeBackendPlan)
		}
	}
	// The count is part of the sentence, so it is pinned with the list.
	if want := fmt.Sprintf("finds %d functions in `internal/cli`", len(found)); !strings.Contains(doc, want) {
		t.Errorf("%s no longer says %q: internal/cli declares %d `func TestChecklist…` functions (%s)", computeBackendPlan, want, len(found), strings.Join(found, ", "))
	}
	// And the backstop the design names is still the one thing that is not here:
	// the plan names it as absent, and a test function of that name would make
	// Task 12's whole reason for writing its own test false.
	if !strings.Contains(doc, "TestChecklistChecksAreGuarded") {
		t.Errorf("%s no longer names TestChecklistChecksAreGuarded as the backstop the design assumes and this tree does not have", computeBackendPlan)
	}
	for _, name := range found {
		if name == "TestChecklistChecksAreGuarded" {
			t.Fatalf("internal/cli now declares TestChecklistChecksAreGuarded: %s's open item 9 says that test is not in this tree and Task 12 is written from its absence, so both have to change in the same commit", computeBackendPlan)
		}
	}
	if !strings.Contains(doc, "none of them the backstop the design names") {
		t.Errorf("%s no longer says none of the enumerated tests is the design's backstop", computeBackendPlan)
	}
}

// TestDocsComputeBackendSecuritySensitiveSetIsComplete: Global Constraint 8 is
// normative — it says which tasks a reviewer may not merge on a self-review —
// and it claims to *be* the set of headings that carry the flag ("and nothing
// else is on it"). A list like that goes wrong quietly and in both directions:
// a task whose heading carries the flag but which the list omits is a task
// nobody reviews, and a task the list names but whose heading lost the flag is
// a review the constraint asks for and the work does not show.
//
// So the two are asserted against each other: the flagged headings are exactly
// the tasks the constraint names, each named in the constraint's own bold form.
func TestDocsComputeBackendSecuritySensitiveSetIsComplete(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	constraint := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "8. **Security-sensitive tasks are flagged") {
			constraint = line
			break
		}
	}
	if constraint == "" {
		t.Fatalf("%s has no Global Constraint 8 (the security-sensitive one): the plan's review story is what it fixes", computeBackendPlan)
	}
	// The tasks whose own heading carries the flag.
	heading := regexp.MustCompile(`(?m)^### Task (\d+):(.*)$`)
	flagged := map[string]bool{}
	for _, m := range heading.FindAllStringSubmatch(doc, -1) {
		if strings.Contains(m[2], "SECURITY-SENSITIVE") {
			flagged[m[1]] = true
		}
	}
	want := []string{"8", "9", "10", "11", "14"}
	if len(flagged) != len(want) {
		t.Errorf("%s carries the SECURITY-SENSITIVE flag on %d task headings, and Global Constraint 8 names %d tasks: the constraint says it is the set of headings that carry it, so the two are the same list", computeBackendPlan, len(flagged), len(want))
	}
	for _, n := range want {
		if !flagged[n] {
			t.Errorf("Task %s's heading no longer carries SECURITY-SENSITIVE, which Global Constraint 8 lists it as: either the heading or the constraint has moved", n)
		}
		if !strings.Contains(constraint, "**"+n+"**") {
			t.Errorf("Global Constraint 8 no longer names Task %s, whose heading carries the flag: the constraint is what tells a reviewer which tasks may not merge on a self-review", n)
		}
	}
	// No other task may be flagged without appearing in the constraint, and the
	// check above is what makes the count meaningful: a sixth flagged heading
	// leaves one of the two lists short.
	if !strings.Contains(constraint, "and nothing else is on it") {
		t.Errorf("%s's Global Constraint 8 no longer claims to be the whole set, which is the claim this test holds", computeBackendPlan)
	}
}

// TestDocsComputeBackendCIReachMatchesTheTree: Global Constraint 3 rests on a
// fact about CI — the compute root is already in the terraform job's root loop,
// so the design's staged `docs/pending-workflows/ci.yml` request is satisfied
// and this plan re-stages nothing — and on a disagreement it records rather
// than edits: `CONTRIBUTING.md` still tells a contributor the compute root is
// the one CI does not reach until that staged file is copied in.
//
// All three are checkable, and each is the kind of fact that moves silently:
// the loop can lose the name, the staged file can appear, and the contributor
// rule can be corrected. The plan is held to the tree in whichever direction
// the tree moves, because Global Constraint 3 is what a reviewer of every task
// in this plan reads before deciding that no workflow change is needed.
func TestDocsComputeBackendCIReachMatchesTheTree(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	if !strings.Contains(doc, "for r in installation firebase repo compute; do") {
		t.Errorf("%s no longer quotes the terraform job's root loop as naming compute, which is its whole reason for saying no workflow change is needed", computeBackendPlan)
	}
	for _, want := range []string{"`CONTRIBUTING.md:25`", "`CONTRIBUTING.md:32`"} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer cites %s: the plan quotes each of the two lines by its own words, and a citation that drops a line is a claim about the tree that nothing checks", computeBackendPlan, want)
		}
	}
	ci := readDoc(t, ".github/workflows/ci.yml")
	if !strings.Contains(ci, "for r in installation firebase repo compute; do") {
		t.Errorf(".github/workflows/ci.yml's terraform job no longer names compute in its root loop: %s's Global Constraint 3 says it does, and the plan's answer to the design's staged-workflow request rests on it", computeBackendPlan)
	}
	// The staged file the design asked for is not here because the loop has the
	// name: if it ever appears, one of the two claims in Constraint 3 is wrong.
	if _, err := os.Stat("../../docs/pending-workflows/ci.yml"); err == nil {
		t.Errorf("docs/pending-workflows/ci.yml now exists: %s's Global Constraint 3 says the staged request was applied to the workflow instead, so either the plan or that file has to go", computeBackendPlan)
	}
	// The two lines the plan cites are asserted separately and at their own line
	// numbers, because the plan quotes each line's own sentence: `:25` and `:32`
	// say the same thing in different words, and a single "somewhere in the file"
	// assertion would hold the file to neither of them — which is exactly how the
	// plan came to attribute `:25`'s sentence to `:32` in the first place.
	for _, want := range []struct {
		line int
		text string
	}{
		{25, "is the one CI does not reach until the staged `docs/pending-workflows/ci.yml` is copied in"},
		{32, "`compute` is added by the staged `docs/pending-workflows/ci.yml`, so until that file is copied in the new root is validated locally only"},
	} {
		lines := strings.Split(readDoc(t, "CONTRIBUTING.md"), "\n")
		if want.line > len(lines) {
			t.Errorf("CONTRIBUTING.md has %d lines, so :%d no longer exists: %s's Global Constraint 3 cites it by number", len(lines), want.line, computeBackendPlan)
			continue
		}
		if got := lines[want.line-1]; !strings.Contains(got, want.text) {
			t.Errorf("CONTRIBUTING.md:%d no longer says %q (it reads %q): the contributor rule has been corrected, so %s's Global Constraint 3, which quotes that line by number, has to change with it",
				want.line, want.text, strings.TrimSpace(got), computeBackendPlan)
		}
	}
}

// TestDocsComputeBackendSaysWhatTheTreeDisagreesWith: the two places the plan
// was written against a brief that the tree does not carry — the release, and
// the Spot result — are stated in the plan's own text rather than smoothed
// over. The brief's Spot sentence has no `50001` anywhere under docs/ **outside
// this plan**, and the release is 0.9.0 in the constant and the 0.8.6 note; a
// plan that quietly agreed with either would be a plan a reader could not check.
//
// The `50001` half is a claim about the whole of `docs/`, so it is asserted
// over the whole of `docs/` — every file, recursively, this plan excluded —
// rather than over the two documents the number would most obviously land in.
// The plan itself carries the number (it quotes the brief), which is why the
// claim has to say "outside this plan" and why an assertion that looked only
// where the plan is not would be a fact the repository contradicts.
func TestDocsComputeBackendSaysWhatTheTreeDisagreesWith(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	if !strings.Contains(doc, "**Two facts this plan is written against") {
		t.Fatalf("%s no longer states the two facts it was written against", computeBackendPlan)
	}
	// The Spot claim is the brief's, and the tree does not carry it.
	for _, want := range []string{
		"`50001`", "no `50001` appears anywhere under `docs/`", "was not run",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer says %q about the Spot result", computeBackendPlan, want)
		}
	}
	// The plan carries the number because it quotes the brief, which is what the
	// word "outside this plan" in the sentence is for: if it ever stops carrying
	// it, the exclusion has become a claim about nothing.
	if !strings.Contains(doc, "50001") {
		t.Errorf("%s no longer carries `50001` at all, and its Spot sentence excludes itself from the claim for that reason", computeBackendPlan)
	}
	// And it is genuinely absent from every document under docs/ other than this
	// plan — the claim the plan makes, asserted over the whole tree it names.
	walked := 0
	err := filepath.WalkDir("../../docs", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		walked++
		rel := strings.TrimPrefix(filepath.ToSlash(p), "../../")
		if rel == computeBackendPlan {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "50001") {
			t.Errorf("%s now carries `50001`: the Spot result has been measured and recorded outside %s, so that plan's second opening fact (which says no `50001` appears anywhere under docs/ outside it) and this assertion both have to change", rel, computeBackendPlan)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if walked == 0 {
		t.Fatalf("no files under docs/ were walked: %s's Spot fact is a claim about the whole directory", computeBackendPlan)
	}
	// The release: the tree says 0.9.0 in the constant and the 0.8.6 note, and
	// the plan follows the brief's 0.9.5 and says so.
	for _, want := range []string{
		"**0.9.5**", "is false until 0.9.0", "The reaper will be redesigned for 0.9.0",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer quotes %q in its release fact", computeBackendPlan, want)
		}
	}
	if !strings.Contains(readDoc(t, "internal/cli/recipes_skew.go"), "is false until 0.9.0") {
		t.Errorf("internal/cli/recipes_skew.go no longer says the Batch provisioning is false until 0.9.0: Task 9's commit is the one that changes it, so a change here means that task has landed and %s's release fact has to change with it", computeBackendPlan)
	}
}
