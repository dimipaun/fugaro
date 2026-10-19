package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// computeBackendPlan is the plan this file pins: docs/plans/2026-10-19-compute-backend.md,
// the implementation plan for the Cloud Batch compute backend (design
// docs/design/compute-backend.md), written against the tree at the head of
// branch fugaro/20261019-140014-b55a (= origin/integration-0.9.5, af953e5e).
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

// computeBackendCommentSymbols is the other half of the table: names the plan
// cites that live in comments rather than in code, so they are checked against
// the file as written. The three TODO markers are the whole of it — a task that
// changes a call site removes its marker in the same commit, and the plan says
// so at each of the three.
var computeBackendCommentSymbols = map[string][]string{
	"internal/cli/cloud.go": {"TODO(compute-backend Task 8)"},
	"internal/cli/run.go":   {"TODO(compute-backend Task 8)"},
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
	for path, symbols := range computeBackendCommentSymbols {
		text, ok := read(path)
		if !ok {
			continue
		}
		for _, sym := range symbols {
			if !strings.Contains(text, sym) {
				t.Errorf("%s no longer carries the comment marker %q that %s's Task 8 names as one of the three call sites it owns", path, sym, computeBackendPlan)
			}
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

// TestDocsComputeBackendSaysWhatTheTreeDisagreesWith: the two places the plan
// was written against a brief that the tree does not carry — the release, and
// the Spot result — are stated in the plan's own text rather than smoothed
// over. The brief's Spot sentence has no `50001` anywhere under docs/, and the
// release is 0.9.0 in the constant and the 0.8.6 note; a plan that quietly
// agreed with either would be a plan a reader could not check.
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
	// And it is genuinely absent from the docs it would have been recorded in.
	for _, path := range []string{"docs/design/compute-backend.md", "docs/gcp-live-checklist.md"} {
		if strings.Contains(readDoc(t, path), "50001") {
			t.Errorf("%s now carries `50001`: the Spot result has been measured and recorded, so %s's second opening fact and this test both have to change", path, computeBackendPlan)
		}
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
