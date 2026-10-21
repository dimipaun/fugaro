package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// computeBackendPlan is the plan this file pins: docs/plans/2026-10-19-compute-backend.md,
// the implementation plan for the Cloud Batch compute backend (design
// docs/design/compute-backend.md), written against the tree the branch
// fugaro/20261019-204914-8a1b stands on (= the branch `integration-0.9.5`, cut
// from `integration-0.9.0` at `af953e5e`; when this file was added the branch's
// own commits were this plan and this test, the documents and tests a review of
// them corrected, the credentials-service documents and their own pin test (a
// later, separate piece of work whose owner ruling is recorded in
// docs/credentials-service/reconciliation.md §4), and no workflow — and no task
// of the plan was built). The one behavioural line among those corrections is
// internal/managed/stateaccounts.go's table, which learns the compute module's
// three types — the NAT as the table's empty answer, corrected on a later
// review; everything else those earlier commits add is a comment, a test or a
// document. **The owner approved the plan on 2026-10-19 ("Approve and start",
// every recommendation P1–P8 accepted as written), and three of its tasks are
// built**: Task 14 (the seal's carrier on Batch, its refusal kept and
// documented — see internal/backend/batch/run.go and
// docs/design/budget-token-channel.md) and Task 8's first half, both merged
// into the base, and Task 9, landed on the branch that carries it — which is
// why this file's release check is read against the tree rather than against
// the branch as it was when it was added. The plan's Status line and its OWNER
// APPROVAL GATE say so, and TestDocsComputeBackendStatusMatchesTheBranch and
// TestDocsComputeBackendPlanStatusMatchesTheTree hold both paragraphs to the
// tree, so a branch that carries more (or less) than they claim fails.
const computeBackendPlan = "docs/plans/2026-10-19-compute-backend.md"

// TestDocsComputeBackendCitationsExist: every path the plan cites is a path a
// later implementer has to find — the seam, the Batch package, the Terraform
// root the provisioning stage applies, the register the check number is read
// from, the pins the tasks are told to update. A substring assertion cannot see
// one of them move, so each is stat'ed here as well as required in the text.
//
// Two kinds of citation are deliberately NOT in the list below.
//   - The files the plan's "File Structure" section says it CREATES and that no
//     task has created yet (internal/cli/reaper.go, internal/cli/reaper_test.go,
//     deploy/terraform/gcp/modules/compute/reaper.tf): stat'ing them would fail
//     until Task 11 lands. The block's other two -- internal/cli/backends.go and
//     internal/cli/backends_test.go, which the pin test was written before -- are
//     listed below like any other citation, because Task 8 created them.
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
		// and the stage `fugaro setup --provision-batch` runs (Task 9 shipped it).
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
		// Task 8's own new files, created by the task the pin test was written
		// for: the command and the tests that hold its list to the seam's.
		"internal/cli/backends.go",
		"internal/cli/backends_test.go",
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
		"ActionDeleteJob", "ActionDeleteVM", "Report",
		"ReapMargin", "MaxDeadline", "QueueDeadline",
	},
	"internal/backend/batch/batch_test.go": {
		// Task 9 and Task 14 both name this test as the pin they follow, and
		// the plan's "no literal 412 anywhere" claim rests on this constant.
		"TestLaunchRefusesASecretEnv", "testSocketGID",
	},
	// Task 8 is what these two entries changed: the opener map the seam is
	// opened from is one function now (cloudOpeners, with batchOptions beside
	// it), and the launch record's backend is read from the codec of the
	// backend the launch ran on rather than written as Cloud Run's constant.
	"internal/cli/cloud.go": {"openCloudFor", "checkExecution", "cloudOpeners", "batchOptions", "launchBackend"},
	"internal/cli/run.go":   {"Backend: launchBackend(env.be)"},
	// Task 8's command: the two names a project config may select and the
	// project's own answer, read from localcfg with no cloud call.
	"internal/cli/backends.go": {"newBackendsCmd", "selectableBackends", "backendsLs", "backendsListDoc"},
	"internal/cli/backends_test.go": {
		// The plan's File Structure line for this file: the two names, the
		// project's own answer, and the refusal for an unknown one.
		"TestBackendsLsListsTheOpenerMapsNames", "TestBackendsLsMarksTheProjectsOwnBackend",
	},
	"internal/cli/recipes_skew.go": {
		// The gate the plan is about. Task 9 flips it and removes the refusal
		// beside it; the three names that go are in computeBackendAbsent below,
		// which is what makes a re-added cancel action or a re-added refusal
		// fail rather than read as present.
		"batchProvisionShipped",
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
		// The resource that names the permissions. The one Task 9 removes
		// (batch.jobs.cancel, which no GCP role can carry) is in
		// computeBackendAbsent below.
		"google_project_iam_custom_role", "batch.jobs.delete",
	},
	"deploy/terraform/gcp/modules/compute/preload.tf": {
		"google_cloud_scheduler_job", "preload_rebuild",
	},
	"deploy/terraform/gcp/modules/installation/history.tf": {
		// The precedent Task 11 follows, line for line.
		"google_cloud_run_v2_job", "google_cloud_scheduler_job", "history_sweep",
	},
	"deploy/terraform/terraform_test.go": {
		// The two pins Task 9 updates in the same commit as the permission
		// list. The first is renamed with its assertion: a pin called "exactly
		// six" that asserts five is a name that has stopped being true.
		"TestComputeReaperRoleIsExactlyFive", "TestCustomRolePermissionsAreAllowedByGoogle", "knownInvalid",
	},
	"internal/cli/docs_artifact_runs_test.go": {
		// The shape Task 12's TestChecklistHasCheck57 is written in.
		"TestChecklistHasCheck58", "check58Guard",
	},
}

// computeBackendAbsent are the names Task 9 removed, each with the files it was
// removed from. They are asserted **gone** rather than merely dropped from
// computeBackendSymbols above, and that is the point of the table: a name that
// goes and is not held against the tree is a name the next reader re-adds, which
// is what removed it in the first place. `ActionCancelJob` is the sharpest of
// them -- a cancel action nothing emits is a permission someone re-adds to the
// reaper's role, and that permission (batch.jobs.cancel) is not one GCP has, so
// re-adding it breaks --provision-batch again.
//
// The files are where each name lived, and the walk in
// TestDocsComputeBackendRemovedNamesAreGone is over the whole tree for the same
// names: a name that reappears anywhere in code, Terraform or a script fails
// there, whether or not it is the file it was removed from.
var computeBackendAbsent = map[string][]string{
	"internal/backend/batch/reap.go": {
		// The queue deadline deletes now (Task 9). The action a cancel would
		// be recorded as is gone from the package, and cancelling a queued job
		// is not a verb the reaper has.
		"ActionCancelJob",
	},
	"internal/cli/recipes_skew.go": {
		// The pair around the gate and the refusal it fed: an open gate
		// nothing closes and a refusal nothing can reach are names a later
		// reader re-opens.
		"batchProvisionOpen", "batchProvisionDisabled", "refuseBatchProvisionNotShipped",
	},
	"deploy/terraform/gcp/modules/compute/iam.tf": {
		// The permission Google does not define, which made the reaper's role
		// uncreatable and refused --provision-batch.
		"batch.jobs.cancel",
	},
}

// The two names the walk below leaves out of its repo-wide search: this file,
// which quotes every one of them in the table above, and the plan, which cites
// them. Both are prose about the tree rather than the tree.
var computeBackendAbsenceSkips = []string{"internal/cli/docs_compute_backend_test.go", computeBackendPlan}

// TestDocsComputeBackendRemovedNamesAreGone: every name Task 9 removed is
// asserted absent from the tree's **code**, in the file it was removed from and
// anywhere else, so that re-adding one fails here rather than leaving a plan
// whose task has been undone silently. Comments are stripped before the search
// for the same reason computeBackendSymbols strips them: the files that explain
// these removals say the names (iam.tf says why batch.jobs.cancel is gone, and
// says it is not to come back), and a doc comment naming a symbol is not the
// symbol.
func TestDocsComputeBackendRemovedNamesAreGone(t *testing.T) {
	for file, names := range computeBackendAbsent {
		text, err := os.ReadFile("../../" + file)
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		code := stripLineComments(string(text))
		for _, name := range names {
			if strings.Contains(code, name) {
				t.Errorf("%s still has %s in its code: Task 9 removed it, and this table is what makes a re-add fail (the plan's Tasks are written in terms of these having gone)", file, name)
			}
		}
	}
	// The same names, over the whole tree. A name re-added in another package
	// (a caller that still expects a cancel, a test that pins one) is the same
	// regression as one re-added where it lived.
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
		rel := strings.TrimPrefix(filepath.ToSlash(p), "../../")
		if slices.Contains(computeBackendAbsenceSkips, rel) {
			return nil
		}
		switch filepath.Ext(rel) {
		case ".go", ".tf", ".sh":
		default:
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		code := stripLineComments(string(data))
		for _, names := range computeBackendAbsent {
			for _, name := range names {
				if !strings.Contains(code, name) {
					continue
				}
				for i, line := range strings.Split(code, "\n") {
					if strings.Contains(line, name) {
						t.Errorf("%s:%d names %s in code: Task 9 removed it from %s (or from the package it lived in), and this walk is what holds the removal over the whole tree rather than over one file", rel, i+1, name, "the tree")
						break
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// computeBackendTodoMarker is the marker the design's Task 8 left at each call
// site the seam has not reached, and which the plan's Task 8 removes with the
// call site it marks.
const computeBackendTodoMarker = "TODO(compute-backend Task 8)"

// computeBackendCommentMarkers is the other half of the table, as counts: names
// the plan cites that live in comments rather than in code, so they are checked
// against the file as written. **The count is the assertion**, and Task 8 is
// what took the counts to zero: step 6 removes each marker with the call site it
// marks, so what the counts assert now is the markers' absence — a zero here
// plus the walk in TestDocsComputeBackendTodoMarkersAreCounted, which fails on a
// marker in any file. The two paths stay listed, because they are the two sites
// the plan's sentence names (one in `internal/cli/cloud.go`, two in
// `internal/cli/run.go`) and a table that dropped them would be a test that
// stopped looking where the markers were.
var computeBackendCommentMarkers = map[string]int{
	"internal/cli/cloud.go": 0,
	"internal/cli/run.go":   0,
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
// names in other packages — the opener map, the gate the --provision-batch
// stage ships behind, the five-permission reaper role, the test that pins the
// seal's refusal. Each is asserted present in the file the plan says it is in, and
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
// `TODO(compute-backend Task 8)` markers were the whole of what the seam had not
// reached — one in internal/cli/cloud.go, two in internal/cli/run.go — and that
// Task 8 removes each one with the call site it marks. Both halves are still
// asserted, and the second one is inverted by Task 8's own step 6 ("the counts
// go to zero and the assertion becomes the markers' absence"): a per-file
// Contains could never hold the removal, because deleting one of run.go's two
// markers left the other answering for both — the file still read as "the marker
// is there" while a call site had lost the note that said Task 8 owned it.
//
// So the markers are counted, per file and in total, and the count is held
// against a walk of the repository in both directions: with the counts at zero,
// a marker anywhere is a call site the plan has not accounted for — the marker
// went with the call site, so one that is still there is a call site the seam
// has not reached — and the two paths the plan's sentence names must stay in the
// table, so the absence is asserted where the markers were rather than nowhere.
// This file is the one path the walk skips, because it quotes the marker in its
// own text.
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
	// The two paths the plan's sentence names are the table's whole key set:
	// dropping one would be a test that stopped looking exactly where the
	// markers were.
	for _, path := range []string{"internal/cli/cloud.go", "internal/cli/run.go"} {
		if _, ok := computeBackendCommentMarkers[path]; !ok {
			t.Errorf("computeBackendCommentMarkers no longer lists %s, one of the two files %s names as a marker's home: the zero has to be asserted where the marker was", path, computeBackendPlan)
		}
	}
	total := 0
	for path, want := range computeBackendCommentMarkers {
		total += want
		if got := seen[path]; got != want {
			t.Errorf("%s carries %d %q markers, want %d: Task 8 removes each marker with the call site it marks, so a marker still here is a call site the seam has not reached (or one whose removal left the note behind)", path, got, computeBackendTodoMarker, want)
		}
	}
	if total != 0 {
		t.Errorf("computeBackendCommentMarkers accounts for %d markers: Task 8's step 6 removes the three, so every count is zero", total)
	}
	for path, n := range seen {
		if _, ok := computeBackendCommentMarkers[path]; !ok {
			t.Errorf("%s carries %d %q markers and is not in computeBackendCommentMarkers: the three the plan names were the whole of it, so a fourth site is either a call site the plan has not accounted for or a marker a task left behind", path, n, computeBackendTodoMarker)
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
// are P1 to P8, and every one of them is still written as a recommendation the
// plan does not answer itself — what changed is that the owner answered all
// eight, and both the decisions section and the gate at the foot have to say
// so. An answer recorded in one place and not the other is the failure this
// test is for: a gate that says APPROVED over rows a reader still reads as
// open questions, or rows marked answered by a gate nobody pointed them at.
//
// The wrong pair is the one that matters: the gate is APPROVED and the section
// says the plan answers none of them, or the reversal — a section that looks
// answered while the gate still refuses to let a task start. Both fail here.
//
// Both paragraphs also name the tasks that have landed — Task 14, Task 8's
// first half and Task 9 — so a plan whose tasks are built while its prose still reads as
// a proposal fails here too.
func TestDocsComputeBackendDecisionsAreRecommendations(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	for i := 1; i <= 8; i++ {
		row := fmt.Sprintf("| **P%d** |", i)
		if !strings.Contains(doc, row) {
			t.Errorf("%s no longer carries the %s decision row", computeBackendPlan, row)
		}
	}
	decisions := section(t, doc, "## Decisions this plan deferred to the owner", "## Open items")
	for _, want := range []string{
		// Still a recommendation, and still not the plan's own answer.
		"this plan answers none of them itself",
		// And the owner's answer, with the date it was given.
		"the owner accepted all eight, as written, on 2026-10-19",
		"**The owner approved every row below with its recommendation as written, on 2026-10-19**",
		"**ANSWERED 2026-10-19, and landed (Task 14).**",
	} {
		if !strings.Contains(decisions, want) {
			t.Errorf("%s: the decisions section no longer says %q", computeBackendPlan, want)
		}
	}
	if strings.Contains(doc, "**None is answered**") {
		t.Errorf("%s still says its own decisions are unanswered; the owner approved them on 2026-10-19, and Task 14 — P1's answer — is built", computeBackendPlan)
	}
	// The three the gate says change what gets built, named there as such.
	// The gate is the file's last section, so it runs to the end of the file.
	i := strings.Index(doc, "## OWNER APPROVAL GATE")
	if i < 0 {
		t.Fatalf("%s has no OWNER APPROVAL GATE: the plan's whole approval story is that section", computeBackendPlan)
	}
	gate := doc[i:]
	for _, want := range []string{
		"**APPROVED 2026-10-19 (\"Approve and start\")",
		"accepted every recommendation P1–P8 as written",
		"**P1 — the seal's carrier. ANSWERED, and landed (Task 14).**",
		"**P2 — the reaper's impossible permission.**",
		"**P5 — the release.**",
		// Task 12 is the owner's alone, and the gate has to say so.
		"**Task 12 is OWNER-RUN**",
		// The gate and the Status line agree on what the approval has built:
		// Task 14 and Task 8's first half, and nothing else.
		"Three tasks have been built since: Task 14",
		"**Task 14 is built, and so are Task 8's first half and Task 9, and nothing else here is**",
	} {
		if !strings.Contains(gate, want) {
			t.Errorf("%s's OWNER APPROVAL GATE no longer says %q", computeBackendPlan, want)
		}
	}
	if strings.Contains(gate, "**UNAPPROVED") {
		t.Errorf("%s's OWNER APPROVAL GATE still reads UNAPPROVED, while Task 14's commit is on the branch it governs", computeBackendPlan)
	}
	// The Status line is where a reader meets the approval first, so it says
	// the same thing the gate does.
	status := section(t, doc, "**Status:", "**Spec:**")
	for _, want := range []string{
		"**Status: the owner APPROVED this plan on 2026-10-19 (\"Approve and start\")",
		"Three tasks have been built since: **Task 14**",
		"**No task's commit exists except Task 14's, Task 8's and Task 9's**",
	} {
		if !strings.Contains(status, want) {
			t.Errorf("%s's Status line no longer says %q", computeBackendPlan, want)
		}
	}
	// The mapping table's last row is the same fact for a reader who checks
	// the design's outline against this plan rather than reading the prose.
	if !strings.Contains(doc, "| **Task 14**: the budget seal's carrier on Batch | **built** — this branch, P1's recommended answer |") {
		t.Errorf("%s's §8.1 mapping table no longer marks Task 14's row built", computeBackendPlan)
	}
	if !strings.Contains(doc, "| **dropped** (Task 14's answer needs no fetch, so there is nothing to build) |") {
		t.Errorf("%s's §8.1 mapping table no longer says Task 13 is dropped rather than deferred", computeBackendPlan)
	}
}

// TestDocsComputeBackendStatusMatchesTheBranch holds the two paragraphs that
// describe what this branch carries, against the tree they describe: the Status
// line's account and the gate's. A plan is a document about code, so a sentence
// in it that says "the branch carries X and no task is built" goes stale the
// moment a task's commit lands — and a stale one is worse than none, because
// its whole job is to tell a reviewer what to expect before they read the diff.
// The plan's own gate makes that a finding rather than a shortcut ("a task that
// lifts it earlier is a finding"), so the paragraph claiming no task has started
// must not survive the first task that starts.
//
// Two directions, both checked here:
//
//   - the sentences are present, so the account cannot be deleted to make the
//     second half pass; and
//   - the work the account names really is in the tree, so the account cannot
//     claim a task whose code is not there — the names are the symbols the task
//     added, and each is read out of its file.
func TestDocsComputeBackendStatusMatchesTheBranch(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	status := section(t, doc, "**Status:", "**Spec:**")
	for _, want := range []string{
		// The approval, in the Status line as well as in the gate.
		"the owner APPROVED this plan on 2026-10-19",
		// What the branch carries beyond the plan and its pin test.
		"Task 8 is the one begun",
		// And what that half does not include, which is the honest half of
		// the account: a reader who reads only the Status knows the task is
		// incomplete before opening its pull request.
		"steps 4 (`fugaro validate`'s offline checks) and 5 (`fugaro doctor`'s live ones) not written",
	} {
		if !strings.Contains(status, want) {
			t.Errorf("%s: the Status line no longer says %q", computeBackendPlan, want)
		}
	}
	// The sentences the first task to start falsified, and which no later
	// reader may find again: each said this branch builds none of the plan's
	// work, and Task 8's commits do.
	for _, gone := range []string{
		"nothing here is approved",
		"no task's commit exists",
		"this pull request builds none of it",
		"no line of the work below has been written or run",
	} {
		if strings.Contains(doc, gone) {
			t.Errorf("%s still says %q, and Task 8's first half is committed on this branch: the account a reader checks the diff against says nothing was built", computeBackendPlan, gone)
		}
	}
	// What the account names, read out of the tree: the opener and the
	// configuration it reads, the command, and the seam's call sites. The
	// symbols are the pin test's own list (computeBackendSymbols above), so
	// this is the same hold the rest of the file makes, applied to the
	// paragraph that says which of them exist.
	for file, symbols := range map[string][]string{
		"internal/cli/cloud.go":         {"cloudOpeners", "batchOptions", "batchTimeouts"},
		"internal/cli/backends.go":      {"newBackendsCmd", "selectableBackends", "backendsLs", "backendsListDoc"},
		"internal/localcfg/localcfg.go": {"validateBatch", "BatchTaskTimeout"},
	} {
		data, err := os.ReadFile("../../" + file)
		if err != nil {
			t.Fatalf("the Status line says this branch carries %s, which could not be read: %v", file, err)
		}
		code := stripLineComments(string(data))
		for _, sym := range symbols {
			if !strings.Contains(code, sym) {
				t.Errorf("%s no longer defines %s, and %s's Status line says the branch carries it: the account and the tree disagree", file, sym, computeBackendPlan)
			}
		}
	}
	// And the gate's own account of the same thing, which is the paragraph a
	// reader arriving at the foot of the file reads.
	i := strings.Index(doc, "## OWNER APPROVAL GATE")
	if i < 0 {
		t.Fatalf("%s has no OWNER APPROVAL GATE: the plan's whole approval story is that section", computeBackendPlan)
	}
	gate := doc[i:]
	for _, want := range []string{
		"Task 8's first half, begun under that approval",
		"**No other task has a commit",
		"`backend: cloud-batch` is still refused by `internal/localcfg`",
	} {
		if !strings.Contains(gate, want) {
			t.Errorf("%s's OWNER APPROVAL GATE no longer says %q: the gate is what tells a reviewer which tasks have started", computeBackendPlan, want)
		}
	}
	// The one refusal the account promises is still there, checked against
	// localcfg's behaviour rather than against its prose: a gate that says the
	// name is still refused over a config this build accepts would be the
	// worst of the two failures.
	if _, err := localcfg.Parse([]byte("version: 1\nname: aurora\ngcp_project: proj-1234\nregion: us-east5\nruns_bucket: my-runs\n" +
		"backend: cloud-batch\nrepos:\n  acme/app: { provider: github, workflows: [web] }\n")); err == nil {
		t.Errorf("%s's gate says `backend: cloud-batch` is still refused, and this build accepts the config that names it", computeBackendPlan)
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
// this plan**, and the release is 0.9.0 in the released notes and in the
// constant's own comment; a plan that quietly agreed with either would be a plan
// a reader could not check.
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
	// The Spot claim is the brief's, and the tree does not carry it. It is
	// asserted as the whole sentence rather than its prefix:
	// "no `50001` appears anywhere under `docs/`" is the first half of a claim
	// whose second half is what makes it true of a repository that carries the
	// number in this very plan, so a plan that dropped "outside this plan" and
	// kept the prefix would otherwise pass an assertion about a sentence it no
	// longer states.
	for _, want := range []string{
		"`50001`", "no `50001` appears anywhere under `docs/` outside this plan", "was not run",
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
	// The release: the tree's own surfaces say 0.9.5 now and the plan follows
	// the brief's 0.9.5, and the only 0.9.0 the release fact still quotes is the
	// released notes' and the design's status line, which Task 9 does not turn
	// back.
	//
	// The constant is asserted by its **new** words. This assertion used to read
	// "is false until 0.9.0" (and, after 0.9.0's release content, "is false
	// until a later release") out of internal/cli/recipes_skew.go, and that was
	// the assertion Task 9 was written to falsify -- a pin that keeps the old
	// sentence is a pin that fails the moment the gate it is about ships, so it
	// is flipped with the thing it asserts rather than deleted. The absence
	// assertions beside it are what make a revert of step 5 -- the refusal back
	// on, under either old wording -- fail here.
	for _, want := range []string{
		"**0.9.5**", "both are planned to return in 0.9.0 after a redesign", "The reaper will be redesigned for 0.9.0",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer quotes %q in its release fact", computeBackendPlan, want)
		}
	}
	skew := readDoc(t, "internal/cli/recipes_skew.go")
	if !strings.Contains(skew, "batchProvisionShipped is true as of 0.9.5") {
		t.Errorf("internal/cli/recipes_skew.go no longer says the Batch provisioning is true as of 0.9.5: Task 9's commit is the one that flips that constant to the release it lands in, so the flag is refused again for a release the plan does not name and %s's release fact has to change with it", computeBackendPlan)
	}
	if strings.Contains(skew, "is false until 0.9.0") {
		t.Errorf("internal/cli/recipes_skew.go says the Batch provisioning is false until 0.9.0 again: Task 9 is the commit that retires that sentence -- the module the stage applies is fixed in 0.9.5, so re-landing the refusal under the old release name is the state this pin exists to catch")
	}
	if strings.Contains(skew, "is false until a later release") {
		t.Errorf("internal/cli/recipes_skew.go says the Batch provisioning is false until a later release again: Task 9 is the commit that retires that sentence, and re-landing it is a revert of step 5")
	}
}

// computeBackendZeroNineZero is every place in the tree's own code, its tests
// and its release documents that names 0.9.0 for the work this plan is about,
// each as the file, the anchor a reader finds the place by and the words the
// tree carries there beside the release name: the constant whose comment names
// the release, the released notes the refusal came from and was recorded in, the
// rerelease checklist's verifying line, the Terraform pin's comment and its
// known-invalid entry, the design's own status line, and the init flag table's
// entry for the stage the refusal covers.
//
// It held nine entries before Task 9 and holds four after it, and the four are
// the whole of what that task leaves: docs/releases/v0.8.6.md's two lines and
// docs/releases/v0.9.0.md's known-limit bullet, which are **released notes** --
// they were true when they were written, and a plan does not edit history --
// and the design's own status line, which says the backend is not in 0.9.0
// and which Task 9 rewrote to say what stays refused. The five that went are
// the surfaces Task 9 changes or removes: the constant's comment,
// docs/release.md's verifying bullet and managed_test.go's init flag entry
// (rewritten to 0.9.5 by that task), and deploy/terraform/terraform_test.go's
// comment and knownInvalid entry (which went with the entry step 4 takes out
// of the map); the refusal text and the assertion that held it went with
// batchProvisionDisabled and refuseBatchProvisionNotShipped, which the absence
// table above holds gone. A reader who finds a fifth entry here has found a
// surface a commit reverted, and the walk below is what says so rather than a
// reader's count.
//
// It is a table of places rather than of lines because the words are the fact
// and the line number is not: the tree's 0.9.0 release content landed on this
// branch, and it rewrote some of these places, retired one of them (the refusal
// text, which no longer names a release at all) and moved the lines of the rest
// — a citation by line is a second fact that an edit anywhere above it
// falsifies, while the anchor a reader finds the place by does not move. The
// anchor is the symbol, heading or bullet the place is found by, and the
// assertion below reads the words back out of the comment or paragraph the
// anchor opens, so a citation that moved, a place rewritten elsewhere and a
// count that drifted are all failures. What a table cannot do is say whether it
// is complete, and a count nobody can check is how this fact said "two places"
// for a tree that has nine: so the two walks below are its bounds.
// TestDocsComputeBackendZeroNineZeroSurfacesAreAllListed searches the tree for a
// 0.9.0 release name beside one of this work's gating identifiers and fails on
// any line that is not one of these; TestDocsComputeBackendReleaseProseDocumentsAreNamed
// does the same, over 0.9.x, for the documents under docs/ that place this work
// in such a release in prose. The difference between the two sets is the plan's
// own and is what Task 9 implements: these are what that task changes, and the
// documents the second walk finds are the record of an earlier ruling, which no
// task here rewrites -- which is why this walk reads 0.9.0 and that one reads
// any 0.9.x. The narrowed pattern is deliberate and is not a weakening: 0.9.5
// beside a gating identifier is what this task's commit **puts** in the tree
// (the constant's comment, the verifying line, the flag table's entry), so a
// search that kept 0.9.x would require the plan to list its own new lines; what
// it must keep finding is every line that still claims this work ships in
// **0.9.0**, and after Task 9 the four places in the table are the whole of it.
var computeBackendZeroNineZero = []struct {
	file   string
	anchor string
	text   string
}{
	{"docs/releases/v0.8.6.md", "Managed projects and `fugaro setup --provision-batch` are turned off in this release", "both are planned to return in 0.9.0 after a redesign"},
	{"docs/releases/v0.8.6.md", "**`fugaro setup --provision-batch` is disabled.**", "The reaper will be redesigned for 0.9.0."},
	{"docs/design/compute-backend.md", "**Not shipped, and not in 0.9.0.**", "`fugaro setup --provision-batch` no longer refuses"},
	{"docs/releases/v0.9.0.md", "**The Batch backend is still disabled.**", "would return in 0.9.0, and neither returns here"},
}

// anchoredLines finds the anchor in doc and returns the 0-based index of the
// line it is on, whether it was found, and the place read as prose: the anchor's
// line and, for a Go comment or a markdown blockquote, the comment or quote it
// opens, each stripped of its marker and joined with single spaces. A sentence a
// comment wraps does not stop at the line it wraps on — the constant's comment
// wraps "excluded from 0.9.0" across two lines — and the words are what is
// pinned, so the wrap is not part of the pin. A line carrying no such marker
// stands alone: a map entry is its own place, not the head of the block it sits
// in.
func anchoredLines(doc, anchor string) (int, string, bool) {
	lines := strings.Split(doc, "\n")
	for i, line := range lines {
		if !strings.Contains(line, anchor) {
			continue
		}
		marker := lineMarker(lines[i])
		parts := []string{strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), marker))}
		for j := i + 1; marker != "" && j < len(lines); j++ {
			next := strings.TrimSpace(lines[j])
			if next == "" || !strings.HasPrefix(next, marker) {
				break
			}
			parts = append(parts, strings.TrimSpace(strings.TrimPrefix(next, marker)))
		}
		return i, strings.Join(parts, " "), true
	}
	return 0, "", false
}

// lineMarker is the comment or quote marker a line opens with: "//" for a Go
// comment, ">" for a markdown blockquote, and "" for everything else.
func lineMarker(line string) string {
	s := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(s, "//"):
		return "//"
	case strings.HasPrefix(s, ">"):
		return ">"
	}
	return ""
}

// computeBackendGateIdentifiers are this work's own gating names: the constant
// the release gate reads, the refusal it feeds, the two names that exist only to
// make the gate re-openable, the permission GCP does not have (which is why the
// provisioning is refused), and the flag. A line in the tree that carries one of
// these beside a 0.9.x release name names this work's release on one of its own
// surfaces, which is what the walk below requires to be listed.
var computeBackendGateIdentifiers = []string{
	"batchProvisionShipped",
	"batchProvisionDisabled",
	"refuseBatchProvisionNotShipped",
	"batch.jobs.cancel",
	"--provision-batch",
}

// The count the plan states is the table's length, in words, so the sentence
// and the list cannot drift apart.
var computeBackendCountWords = []string{"no", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}

// TestDocsComputeBackendNamesWhereTheTreeSaysZeroNineZero: the plan's release
// fact says the tree names 0.9.0 for this work in a stated number of places and
// lists each by file and line, and this holds all three parts of that — the
// count, the citations, and the lines themselves.
func TestDocsComputeBackendNamesWhereTheTreeSaysZeroNineZero(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	n := len(computeBackendZeroNineZero)
	if n >= len(computeBackendCountWords) {
		t.Fatalf("the table holds %d places and this test spells counts up to %d", n, len(computeBackendCountWords)-1)
	}
	sentence := fmt.Sprintf("**The tree names 0.9.0 for this work in %s places on the surfaces this plan is about**", computeBackendCountWords[n])
	if !strings.Contains(doc, sentence) {
		t.Errorf("%s no longer states %q: the count and this table's %d entries are one fact and a reader checks them against each other", computeBackendPlan, sentence, n)
	}
	// The count is scoped to these surfaces on purpose, and the scope has to be
	// in the plan: 0.9.0 is also all over the tree's design and plan prose for
	// this same work, which no task here rewrites, and a sentence that counted
	// those would be the "nowhere else" claim this fact used to carry — the one
	// an implementer of Task 9 read as "the tree agrees with 0.9.5 after my
	// commit".
	if !strings.Contains(doc, "states no count over them") {
		t.Errorf("%s no longer says it states no count over the release prose that places this work in 0.9.0: that scoping is what makes the count checkable, and without it the sentence is a claim about the whole tree again", computeBackendPlan)
	}
	// The three shapes this fact has already had and been corrected for, each
	// of them an overclaim about the whole tree: "two places" was a reading that
	// missed six, "Nothing else in the tree names a release for this work" was
	// false when it was written, and "the two lines of that one released note
	// and nowhere else" is the sentence an implementer of Task 9 reads as the
	// tree agreeing with 0.9.5 after that one commit. A phrase is a weak thing
	// to forbid; these three are the ones that were here.
	for _, claim := range []string{
		"and nowhere else",
		"Nothing else in the tree names",
		"the tree names 0.9.0 for this work in the two lines",
	} {
		if strings.Contains(doc, claim) {
			t.Errorf("%s says %q about the places naming 0.9.0 for this work: the tree's design and plan prose names 0.9.0 in documents %s itself lists, so that claim was false when it was written and is the one an implementer reads as the tree agreeing with 0.9.5", computeBackendPlan, claim, computeBackendPlan)
		}
	}
	for _, want := range computeBackendZeroNineZero {
		if !strings.Contains(doc, "`"+want.file+"`") {
			t.Errorf("%s no longer names %s among the places the tree names 0.9.0 for this work", computeBackendPlan, want.file)
		}
		// The plan quotes the anchor and the words, so a reader can find the
		// place and see what it says without the plan having to be right about
		// which line it is on.
		for _, quoted := range []string{want.anchor, want.text} {
			if !strings.Contains(doc, quoted) {
				t.Errorf("%s no longer quotes %q, which %s carries beside the release name it gives this work", computeBackendPlan, quoted, want.file)
			}
		}
		_, place, ok := anchoredLines(readDoc(t, want.file), want.anchor)
		if !ok {
			t.Errorf("%s no longer carries the anchor %q that %s's place is found by: the release fact cites that place, so the anchor and this table move together — and Task 9 is the commit that rewrites or retires the five of these that are not the released notes or the design's own status line", want.file, want.anchor, computeBackendPlan)
			continue
		}
		if !strings.Contains(place, want.text) {
			t.Errorf("%s's %q no longer says %q (it reads %q): the release fact lists that place, so rewritten words mean the plan and this table move together — and Task 9 is the commit that rewrites or retires the five of these that are not the released notes or the design's own status line", want.file, want.anchor, want.text, place)
		}
	}
}

// TestDocsComputeBackendZeroNineZeroSurfacesAreAllListed: fact 1's count is a
// claim about the tree, and a claim about the tree is only as good as the search
// behind it. This is that search for the surfaces the plan enumerates: every
// line in the tree carrying a **0.9.0** release name beside one of this work's
// gating identifiers must be one of the places the plan lists.
//
// It is the direction the earlier counts never had. "Two places" and then
// "eight" were both readings of the tree by eye, and the ninth —
// internal/cli/managed_test.go's init flag entry — is exactly the kind a reading
// misses: it breaks no compile, fails no other test, and is a line of prose in a
// table. A count is only a fact if something searches for the tenth.
//
// **The pattern is 0.9.0 and not any 0.9.x, and Task 9 is why**: that task's
// commit is the one that *puts* 0.9.5 beside these identifiers (the constant's
// comment, the verifying line, the init flag table's entry, the Terraform
// pin's comment and the module pin's own), so a search over 0.9.x after it
// would be requiring the plan to list the lines the plan's own task just wrote
// -- a bound that grows with the work rather than holding it. What this must
// keep finding is every line that still claims this work ships in 0.9.0, which
// after Task 9 is the four places the table lists and nothing else: a line
// rewritten back to 0.9.0, or a new one, is a fifth surface and fails here.
//
// Two files are skipped by name: this test file, whose table quotes every one of
// the lines, and the plan, which cites them.
func TestDocsComputeBackendZeroNineZeroSurfacesAreAllListed(t *testing.T) {
	// The table is a table of places, and the walk finds lines: each entry is
	// resolved to the line its anchor is on, and that line is what is listed.
	listed := map[string]bool{}
	for _, e := range computeBackendZeroNineZero {
		i, _, ok := anchoredLines(readDoc(t, e.file), e.anchor)
		if !ok {
			t.Errorf("%s no longer carries the anchor %q this table lists a place by, so the walk below cannot tell whether a line of it is listed", e.file, e.anchor)
			continue
		}
		listed[fmt.Sprintf("%s:%d", e.file, i+1)] = true
	}
	release := regexp.MustCompile(`0\.9\.0`)
	found := 0
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
		rel := strings.TrimPrefix(filepath.ToSlash(p), "../../")
		if rel == "internal/cli/docs_compute_backend_test.go" || rel == computeBackendPlan {
			return nil
		}
		// Only the kinds of file a gating surface is written in: a release
		// name beside a gating identifier is a fact about code, Terraform, a
		// script or a document, and reading every other byte of the tree (a
		// fixture, an image, an editor's leftover) would match by accident
		// without ever naming a surface.
		switch filepath.Ext(rel) {
		case ".go", ".tf", ".sh", ".md", ".yml", ".yaml", ".json":
		default:
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !release.MatchString(line) {
				continue
			}
			gated := false
			for _, id := range computeBackendGateIdentifiers {
				if strings.Contains(line, id) {
					gated = true
					break
				}
			}
			if !gated {
				continue
			}
			found++
			if !listed[fmt.Sprintf("%s:%d", rel, i+1)] {
				t.Errorf("%s:%d carries a 0.9.0 release name beside one of this work's gating identifiers (%s) and is not among the places %s's fact 1 lists: the count there is a claim about this tree, so a third surface is either a line the plan has to list or a release name that has to move with the work", rel, i+1, strings.Join(computeBackendGateIdentifiers, ", "), computeBackendPlan)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The floor is the table's own length: this walk reads 0.9.0, and the table
	// is every 0.9.0 line beside an identifier that is left, so a search finding
	// fewer lines than the table lists is not matching what the plan states and
	// is a bound that cannot fail.
	if found < len(computeBackendZeroNineZero) {
		t.Fatalf("the walk found %d lines carrying a 0.9.0 name beside a gating identifier while the table lists %d: the search is matching less than the plan does, so it is a bound that cannot fail", found, len(computeBackendZeroNineZero))
	}
}

// computeBackendReleaseProse is every document under docs/ that names a 0.9.x
// release for this work in prose, listed by document rather than by line and
// with no count stated anywhere near it. These are the records of the owner's
// ruling of 2026-10-15, which placed this backend in 0.9.0 and which the brief's
// placement supersedes: docs/release.md and docs/releases/v0.8.6.md are the two
// the plan treats as release surfaces (they are in the table above, by their
// places), and the rest are released notes and design and plan text no task in
// this plan rewrites — of which docs/design/compute-backend.md is this work's
// own design and docs/releases/v0.9.0.md is the note that recorded the work as a
// known limit of the release it did not return in.
var computeBackendReleaseProse = []string{
	"docs/release.md",
	"docs/releases/v0.8.6.md",
	"docs/releases/v0.9.0.md",
	"docs/design/compute-backend.md",
	"docs/design/generic-tool.md",
	"docs/plans/2026-10-08-generic-tool.md",
	"docs/design/managed-projects.md",
	"docs/plans/2026-10-16-managed-projects.md",
	"docs/plans/2026-10-14-project-centric.md",
	"docs/design/voice.md",
	"docs/plans/2026-10-16-voice.md",
	"docs/design/project-named-oauth-tokens.md",
	"docs/design/named-oauth-tokens.md",
}

// computeBackendReleaseWords are the names this work goes by in prose. A
// document under docs/ that carries one of them on a line naming a 0.9.x release
// is a document placing this work in that release, which is what the walk below
// requires the plan to name. The list is deliberately this work's own names and
// not "compute" or "backend" alone: 0.9.0 is all over this tree for other
// features — the budget token's seal, the managed deployer's separate gate,
// project-centric's phases — and a rule loose enough to catch those would be a
// rule that fails on documents this work has nothing to do with.
var computeBackendReleaseWords = []string{
	"compute backend",
	"compute-backend",
	"compute-instance",
	"cloud batch",
	"batch backend",
	"docker-capable",
	"docker backend",
	"vm/batch",
	"provision-batch",
	"batchprovision",
	"this backend",
	"0.9.0 phases",
	"0.9.0 phase",
}

// TestDocsComputeBackendReleaseProseDocumentsAreNamed: the plan states no count
// over the prose set, so this does not check a number — it checks the set. Every
// document under docs/ that names a 0.9.x release in this work's context must be
// one the plan names, and every document the plan names must still be one the
// walk finds, so a document added to this set and a document that stopped
// placing the work in that release both fail here rather than at a reader's
// memory. It is the second bound the plan's fact 1 needs: the table above says
// what Task 9 changes, this says what Task 9 leaves alone.
func TestDocsComputeBackendReleaseProseDocumentsAreNamed(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	words := regexp.MustCompile("(?i)" + strings.Join(computeBackendReleaseWords, "|"))
	release := regexp.MustCompile(`0\.9\.[0-9]`)
	found := map[string]bool{}
	err := filepath.WalkDir("../../docs", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel := strings.TrimPrefix(filepath.ToSlash(p), "../../")
		if rel == computeBackendPlan {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if release.MatchString(line) && words.MatchString(line) {
				found[rel] = true
				return nil
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{}
	for _, path := range computeBackendReleaseProse {
		named[path] = true
		if !found[path] {
			t.Errorf("%s is listed as a document placing this work in a 0.9.x release and no line of it does: either the document stopped placing the work in that release, or the work's name moved out of its prose, and either way %s's fact 1 lists it", path, computeBackendPlan)
		}
		if !strings.Contains(doc, path) {
			t.Errorf("%s names no path %q: the prose set is the plan's own statement of what Task 9 leaves alone, so a document in this table and not in the plan is a document an implementer will not know to keep", computeBackendPlan, path)
		}
	}
	for path := range found {
		if !named[path] {
			t.Errorf("%s names a 0.9.x release for this work in its prose and is not a document %s lists: an implementer of Task 9 reads the plan's list as what stays, so a document that has joined the set has to join the list", path, computeBackendPlan)
		}
	}
	if len(found) < 8 {
		t.Fatalf("the walk found %d documents naming a 0.9.x release in this work's context while the table lists %d: the search is matching less than the plan does, so it is a bound that cannot fail", len(found), len(computeBackendReleaseProse))
	}
}

// TestDocsReleaseChecklistNamesEveryShippedGate: docs/release.md's "Before you
// tag" list is where a release verifies the shipped-gate constants, and Task 9
// is what showed the list can lose one silently. The commit that flipped
// `batchProvisionShipped` rewrote the bullet that held it and dropped the
// `managedDeployerShipped` half with it — a gate that is still `false`, and so a
// checklist that stopped verifying a refusal that is still live — and nothing in
// the tree said so: the list enumerates constants by hand, and no test read it
// against them. This is that reading, taken from the constants themselves, so a
// later commit that folds one bullet into another fails here rather than in a
// release.
//
// Mutation (verified, run and restored): delete the `managedDeployerShipped`
// bullet this change restores — this fails naming that constant.
func TestDocsReleaseChecklistNamesEveryShippedGate(t *testing.T) {
	skew := readDoc(t, "internal/cli/recipes_skew.go")
	gates := regexp.MustCompile(`(?m)^const (\w+Shipped) = (?:true|false)$`).FindAllStringSubmatch(skew, -1)
	if len(gates) < 4 {
		t.Fatalf("internal/cli/recipes_skew.go declares %d *Shipped constants and this check requires at least four (cloneShipped, catalogShipped, managedDeployerShipped, batchProvisionShipped): the declaration shape moved and the walk below would pass over nothing", len(gates))
	}
	const beforeYouTag = "**Before you tag"
	doc := readDoc(t, "docs/release.md")
	start := strings.Index(doc, beforeYouTag)
	if start < 0 {
		t.Fatalf("docs/release.md has no %q list: the checklist is where a release verifies every gate constant", beforeYouTag)
	}
	section := doc[start:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	for _, g := range gates {
		if !strings.Contains(section, "`"+g[1]+"`") {
			t.Errorf("docs/release.md's Before-you-tag list names no `%s`: every gate constant in internal/cli/recipes_skew.go is verified there, or a release ships one that nothing checks", g[1])
		}
	}
}

// TestDocsComputeBackendReaperRoleCountMatchesTheModule: the reaper's role is
// built in deploy/terraform/gcp/modules/compute/iam.tf, and the prose that
// describes it drifted from it — Task 9 rewrote two of the three places that
// said "six permissions" and the third survived (§7.2's Built note), so the
// same tree stated both counts for the same list. The count is read out of the
// module the role is created from, and the design's own present-tense claims
// are read against it; the historical sentences ("an earlier shape of this note
// named a sixth") are not what this bans, the "six permissions" claim is.
//
// Mutation (verified, run and restored): put "the six permissions §3.7 lists"
// back into §7.2's Built note — this fails naming it.
func TestDocsComputeBackendReaperRoleCountMatchesTheModule(t *testing.T) {
	iam := readDoc(t, "deploy/terraform/gcp/modules/compute/iam.tf")
	role := regexp.MustCompile(`(?s)resource "google_project_iam_custom_role" "reaper" \{.*?\n\}`).FindString(iam)
	if role == "" {
		t.Fatal("deploy/terraform/gcp/modules/compute/iam.tf has no google_project_iam_custom_role \"reaper\": the module is where the reaper's permission list is written, and the prose below is read against it")
	}
	list := regexp.MustCompile(`(?s)permissions = \[(.*?)\]`).FindStringSubmatch(role)
	if list == nil {
		t.Fatal("the reaper's custom role carries no `permissions = [...]` list")
	}
	var perms []string
	for _, p := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(list[1], -1) {
		perms = append(perms, p[1])
	}
	if len(perms) != 5 {
		t.Fatalf("the reaper's role carries %d permissions (%v), and every surface that states the count says five: the module and the prose have to agree on the one number the role is built from", len(perms), perms)
	}
	// Flattened, as everywhere else in this file: the design wraps at about 78
	// columns, and a phrase that only ever appears wrapped is a phrase a
	// substring check written without it will miss.
	design := flattenDoc(readDoc(t, "docs/design/compute-backend.md"))
	if strings.Contains(design, "six permissions") || strings.Contains(design, "six-permission") {
		t.Error("docs/design/compute-backend.md claims the reaper's list is six permissions: the module's role carries five, and the note that named a sixth is history (it says so itself) — a present-tense six in the same document as §3.7's five is the drift Task 9 left behind")
	}
	if !strings.Contains(design, "as **five** permissions") {
		t.Error("docs/design/compute-backend.md's §3.7 Built note no longer states the count as **five** permissions: that sentence is what the tree's module is read against")
	}
	backends := flattenDoc(readDoc(t, "docs/backends.md"))
	if strings.Contains(backends, "below this one") {
		t.Error("docs/backends.md points at \"the note below this one\" for the sixth permission: nothing below it discusses the reaper's permissions, and the note that named the sixth is the design's §3.7 Built note — cite it by document and section")
	}
	if !strings.Contains(backends, "§3.7") {
		t.Error("docs/backends.md's permission sentence cites no section for the sixth permission it records")
	}
}

// TestReleaseChecklistCopiesNoApplyFailure: "fails partway through its apply" is
// true of one bullet only — `managedDeployerShipped`, whose flip re-enables an
// installation apply the deployer's fence refuses — and it was copied into the
// `catalogSince` bullet above it, where the failure is not an init at all:
// `catalogSince` names a release that does not exist yet, so the base-image age
// check passes for every image there is, the deploy goes through, and the RUN
// fails at bootstrap on a `FUGARO_MODEL_PROVIDERS` field its runner refuses to
// decode. The sentence is read against the bullet it sits in, so a later copy
// fails here rather than in a release.
//
// Mutation (verified, run and restored): put the copied sentence back into the
// `catalogSince` bullet — this fails naming it.
func TestReleaseChecklistCopiesNoApplyFailure(t *testing.T) {
	const beforeYouTag = "**Before you tag"
	doc := readDoc(t, "docs/release.md")
	start := strings.Index(doc, beforeYouTag)
	if start < 0 {
		t.Fatalf("docs/release.md has no %q list: the checklist is where a release verifies every gate constant", beforeYouTag)
	}
	section := doc[start:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	bullets := strings.Split(section, "\n- **")
	if len(bullets) < 10 {
		t.Fatalf("the Before-you-tag list parsed as %d bullets: the walk below would pass over nothing", len(bullets))
	}
	const phrase = "fails partway through its apply"
	for _, b := range bullets {
		if strings.Contains(b, phrase) && !strings.Contains(b, "managedDeployerShipped") {
			t.Errorf("a Before-you-tag bullet that is not `managedDeployerShipped` says %q: that sentence is the deployer fence's apply failure, and the catalog gate's flip leaves the deploy whole and fails the run at bootstrap instead", phrase)
		}
	}
	var catalog string
	for _, b := range bullets {
		if strings.Contains(b, "Verify `catalogSince`") {
			catalog = b
			break
		}
	}
	if catalog == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no `catalogSince` bullet")
	}
	if !strings.Contains(catalog, "bootstrap") {
		t.Error("the `catalogSince` bullet does not name the bootstrap failure its flip brings back: those keys are decoded with DisallowUnknownFields, so it is the run that fails at bootstrap, not the init's apply")
	}
}

// TestDocsComputeBackendPlanStatusMatchesTheTree: the plan's Status line and its
// OWNER APPROVAL GATE are claims about the branch the plan is read on, and Task 9
// is what made them false — the line said no task's commit existed, that no
// Terraform on the branch changed and that `batchProvisionShipped` was `false`,
// while the same file's fact 1 already said Task 9 had landed and the constant in
// the tree was `true`. No test read the Status line against the tree, so the
// contradiction survived a review of the plan that wrote it. This one does, from
// the two facts the tree can be asked about: the constant's value and whether a
// task's commit has landed beside it.
//
// Mutation (verified, run and restored): put "no task's commit exists" back into
// the Status line — this fails naming it.
func TestDocsComputeBackendPlanStatusMatchesTheTree(t *testing.T) {
	skew := readDoc(t, "internal/cli/recipes_skew.go")
	plan := readDoc(t, computeBackendPlan)
	if !strings.Contains(skew, "const batchProvisionShipped = true") {
		t.Skip("the tree's batchProvisionShipped is not true, so the plan's Status line is not read: this check is about the branch the task landed on")
	}
	var status string
	for _, line := range strings.Split(plan, "\n") {
		if strings.HasPrefix(line, "**Status:") {
			status = line
			break
		}
	}
	if status == "" {
		t.Fatalf("%s's Status line is not a line starting with **Status:, so the claims a reader takes from it are not held by anything", computeBackendPlan)
	}
	if !strings.Contains(status, "Task 9") {
		t.Errorf("%s's Status line does not name Task 9 while the tree carries its landed work (batchProvisionShipped is true): the line is what tells a reader which of the tasks below exist", computeBackendPlan)
	}
	for _, stale := range []string{
		"no task's commit exists",
		"still refuses before any cloud call",
		"`batchProvisionShipped` is `false`",
	} {
		if strings.Contains(status, stale) {
			t.Errorf("%s's Status line still says %q while Task 9 has landed and the tree's batchProvisionShipped is true: a Status line is a claim about the branch, and this one is the shape the plan's own fact 1 contradicts", computeBackendPlan, stale)
		}
	}
	if strings.Contains(plan, "**UNAPPROVED.") {
		t.Errorf("%s's OWNER APPROVAL GATE still opens with UNAPPROVED while the tree carries Task 9's landed work: a gate that forbids every task while a task has landed is the contradiction this check is for", computeBackendPlan)
	}
}

// TestDocsComputeBackendQueueDeadlineKeepsLaunchJSON: the design justifies
// deleting a job that never left `QUEUED` — rather than cancelling it, which the
// reaper's five-permission role cannot do — on what such a job leaves behind,
// and an earlier draft of that argument said "there is no `launch.json`", which
// is false: `fugaro run` writes `launch.json` to the runs bucket as `Launch`
// returns (`internal/cli/run.go`: `be.Launch`, then `WriteLaunch`), and a job
// that has been created is `QUEUED` before it is anything else, so the run does
// not vanish from `fugaro ls` when the job's record is deleted. The claim is
// read here against the code it rests on and against the reaper's own header,
// so the false shape cannot come back silently in either.
//
// Mutations (verified, run and restored): put "there is no `launch.json`" back
// into §1.4's amended paragraph — this fails naming it; and put "nothing an
// operator reads back is lost with the record" back into the reaper's
// deleteQueued comment — this fails on that phrase, which is the false half of
// the argument in the place a reader of the code meets it.
func TestDocsComputeBackendQueueDeadlineKeepsLaunchJSON(t *testing.T) {
	// The code fact the argument rests on: launch.json is the launcher's write,
	// made after the create returns, so it is not the job's record.
	run := readDoc(t, "internal/cli/run.go")
	launch := strings.Index(run, "env.be.Launch(lctx, lspec)")
	write := strings.Index(run, "s.WriteLaunch(hctx, l)")
	if launch < 0 || write < 0 || write < launch {
		t.Fatalf("internal/cli/run.go no longer launches and then writes launch.json (%d, %d): the design's argument that a QUEUED job's run survives the reaper's delete rests on that order", launch, write)
	}
	for _, path := range []string{"docs/design/compute-backend.md", "internal/backend/batch/reap.go", "docs/backends.md"} {
		text := flattenDoc(readDoc(t, path))
		for _, bad := range []string{"no `launch.json`", "no launch.json", "no launch.json and"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s says %q about a job that never left QUEUED: launch.json is written by the launcher as the create returns, so it survives the delete and the run stays readable by fugaro ls, diagnose and watch", path, bad)
			}
		}
	}
	// And the amended passage has to say what the delete does not touch, not
	// only what it costs.
	design := readDoc(t, "docs/design/compute-backend.md")
	const amended = "**Amended by Task 9: the deadline deletes, not cancels**"
	i := strings.Index(design, amended)
	if i < 0 {
		t.Fatalf("docs/design/compute-backend.md no longer carries §1.4's %q paragraph: that paragraph is where the delete is justified", amended)
	}
	passage := design[i:]
	if end := strings.Index(passage, "\n  **Built**"); end >= 0 {
		passage = passage[:end]
	}
	if !strings.Contains(passage, "`launch.json`") || !strings.Contains(passage, "survives") {
		t.Errorf("docs/design/compute-backend.md's §1.4 amendment names no surviving `launch.json`: a reader is left with \"the delete destroys nothing that matters\" and no statement of what it does destroy, which is the shape a false claim grew in")
	}
	// The other side of the same claim: the delete is affordable because of
	// what it costs as well as what it spares, and the job's own record --
	// status message included -- is what it costs. A comment that says
	// "nothing an operator reads back is lost" is the false half back again.
	reap := flattenDoc(readDoc(t, "internal/backend/batch/reap.go"))
	for _, bad := range []string{
		"nothing an operator reads back is lost",
		"nothing an operator reads back is lost with the record",
	} {
		if strings.Contains(reap, bad) {
			t.Errorf("internal/backend/batch/reap.go's queue-deadline comment says %q: the job's own record and the status message Batch attaches go with the delete (design §1.4), so what is affordable is the delete, not a claim that nothing is lost", bad)
		}
	}
	if !strings.Contains(reap, "status message") {
		t.Errorf("internal/backend/batch/reap.go's queue-deadline comment never names the job's status message as what the delete costs: the design §1.4 amendment says that is the price of jobs.cancel not existing")
	}
}

// TestDocsComputeBackendTask10CarriesTheAccountRequirement: the security review
// of Task 8's first half, and the follow-up to it, left three things to the
// enable rather than settling them in that task's code, and all three are
// requirements on Task 10's text. The first is an escalation the code alone does
// not close — a published `batch.service_account` is held to the anchor's project
// and to nothing more, so the gate must refuse one outside the installation's own
// run accounts, or the account must come from the local config only — the second
// is the inventory of code outside the seam that still assumes Cloud Run, which
// the enable has to reach, and the third is the shared-config reader's own
// residue of the same kind as the account: a published `batch.image` is refused
// only in the `fugaro-ghcr` pull-through repository, so an image in a second
// pull-through repository of the project passes a reader that has no repository
// list to check against, and the enable's gate is where that is settled. None of
// the three is pinned anywhere else, so a trim can drop one without a test
// noticing.
func TestDocsComputeBackendTask10CarriesTheAccountRequirement(t *testing.T) {
	doc := readDoc(t, computeBackendPlan)
	task := section(t, doc, "### Task 10: The enable", "## Phase C")
	for _, want := range []string{
		// The requirement, in both shapes the plan allows, and the third one
		// (the anchor-project check alone) named as the escalation it is.
		"not one of the installation's own run accounts",
		"read from the local config only and never from the shared one",
		"Keeping the anchor-project check alone is the escalation this step exists to forbid",
		// The inventory of what the enable must reach, and the one entry that is
		// the account requirement's neighbour rather than a Cloud Run path.
		"`gcp.JobName`",
		"`gcp.WorkflowVariant`",
		"names Secret Manager secrets of the installation's project without asking whether the installation created them",
		// The third: the reader's own residue, which the gate is where it is
		// closed, and which the design's §5 states in the same terms.
		"refuses it by that one repository ID and nothing else",
		"A later reader who finds the `fugaro-ghcr` check and nothing more has found this step unfinished",
	} {
		if !strings.Contains(task, want) {
			t.Errorf("%s's Task 10 no longer says %q: the security review of Task 8's first half left that to the enable, and the enable's own text is where it has to be", computeBackendPlan, want)
		}
	}
	// The other half of the dependency: the step that writes the artifact
	// refusal points at the task that must leave it standing, and Task 10's own
	// Depends line names that step.
	for _, want := range []string{
		"Task 10's enable depends on this refusal and on nothing else in this step",
		"Task 8's step 4 in particular",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer carries %q: the artifact refusal and the enable's dependency on it are one fact and are stated on both sides", computeBackendPlan, want)
		}
	}
}

// TestDocsSharedConfigStatesTheImageChecksExactly: the design's §5 bullet is
// what a later reader checks the reader's own batch.image checks against, so it
// has to claim exactly what they enforce. Two halves are pinned here, and the
// second is what the follow-up to Task 8's first half corrected: the bullet had
// said the check leaves an image in one of the installation's own repositories,
// which is more than it leaves — it refuses the one repository ID fugaro-ghcr
// and passes every other repository of the project, a second pull-through
// repository among them. A trim that dropped either half would leave the design
// promising a refusal the code does not make (the plan's Task 10 step 5 owns the
// remainder, and its own pin above).
func TestDocsSharedConfigStatesTheImageChecksExactly(t *testing.T) {
	const doc = "docs/design/shared-config.md"
	bullet := ""
	for _, line := range strings.Split(readDoc(t, doc), "\n") {
		if strings.Contains(line, "every value of the published `batch:` block") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatalf("%s no longer carries §5's bullet on the published batch block, which is where the image's checks are read off", doc)
	}
	for _, want := range []string{
		// What the reader enforces of the path, stated beside the reference
		// the refusals name.
		"every path segment a lowercase name component in the Docker reference grammar",
		"`batchImageRE`",
		// The refusal, and its residue, which the reader cannot see past: the
		// anchor carries no repository list.
		"not one served through the `fugaro-ghcr` remote repository",
		"What the check refuses is exactly that one repository ID",
		"an operator created under another ID",
		"the check cannot see which of them they are",
		"Task 10 step 5",
	} {
		if !strings.Contains(bullet, want) {
			t.Errorf("%s §5 no longer says %q of the published batch.image: the bullet is where the reader's checks are read off, so it has to state what they enforce and what they leave", doc, want)
		}
	}
	// The claim the follow-up removed: "one of this installation's own
	// repositories" is a promise the reader does not keep, so a later edit that
	// restores the phrasing restores the overclaim with it.
	if strings.Contains(readDoc(t, doc), "What the check leaves is an image in one of this installation's own repositories") {
		t.Errorf("%s §5 claims the fugaro-ghcr refusal leaves an image in one of this installation's own repositories, which the reader does not enforce: it refuses one repository ID and passes the project's others", doc)
	}
}
