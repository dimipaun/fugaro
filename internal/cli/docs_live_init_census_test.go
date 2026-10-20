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
// W2.6 -- the task that empties the census -- flips this file's terminal
// assertion from "the walk found something" to "`pendingRename` is empty", and
// that is where the pin finally bites. Nothing before it may relax a row: the
// two exemption classes below exist because the rename cannot be finished
// without them, not to make a red test green.

import (
	"bytes"
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
// MAY name the command, so a path that does not exist yet contributes nothing
// -- W1.2 adds the first two, and the third is this file, whose scan pattern
// is the string. W2.6 asserts that the files naming the command are exactly
// `datedRecords` plus these three.
//
// internal/cli/testdata/init_retire.golden is deliberately NOT here, although
// it names the command four times today: it is a transcript, not a retirement
// statement, and all four are strings W2.1 renames, so W1.2/W2.1 regenerate it
// with TestInitRepoRetireTranscriptGolden -update-retire exactly like the other
// two goldens. An exemption for a file the rename empties is the "wrong in one
// direction" case Global Constraint 7 forbids.
var retirementText = []string{
	// W1.2's tombstone: one message, two names, non-zero exit.
	"internal/cli/retired.go",
	// W1.2's test, which asserts that message.
	"internal/cli/retired_test.go",
	// This file: `pendingRename` is a list of paths, but the pattern below is
	// the string itself.
	"internal/cli/docs_live_init_census_test.go",
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
var initNameRE = regexp.MustCompile(`fugaro init|init --repo`)

// censusRoot is the repository root as this package's tests reach it.
const censusRoot = "../.."

// pendingRename is today's census: every file under liveRoots that names the
// command, sorted, taken on main at 73a0c109. It is 308 files -- 115 non-test
// Go files, 116 test files, 34 markdown files, 29 Terraform files, the four
// `.tftest.hcl` fixtures beside them, the two goldens and the eight files the
// website, the schema and the images scripts keep. Each task that renames a
// string takes the file off this list in the same commit, and W2.6 empties it.
var pendingRename = []string{
	".claude/skills/new-release/SKILL.md",
	"CONTRIBUTING.md",
	"README.md",
	"SECURITY.md",
	"deploy/terraform/gcp/modules/compute/outputs.tf",
	"deploy/terraform/gcp/modules/compute/variables.tf",
	"deploy/terraform/gcp/modules/firebase/apis.tf",
	"deploy/terraform/gcp/modules/firebase/firebase.tf",
	"deploy/terraform/gcp/modules/firebase/outputs.tf",
	"deploy/terraform/gcp/modules/firebase/variables.tf",
	"deploy/terraform/gcp/modules/installation/bucket.tf",
	"deploy/terraform/gcp/modules/installation/deployer.tf",
	"deploy/terraform/gcp/modules/installation/iam.tf",
	"deploy/terraform/gcp/modules/installation/outputs.tf",
	"deploy/terraform/gcp/modules/installation/registry.tf",
	"deploy/terraform/gcp/modules/installation/secrets.tf",
	"deploy/terraform/gcp/modules/installation/variables.tf",
	"deploy/terraform/gcp/modules/project/job.tf",
	"deploy/terraform/gcp/modules/repo/check.tf",
	"deploy/terraform/gcp/modules/repo/variables.tf",
	"deploy/terraform/gcp/modules/workflow/job.tf",
	"deploy/terraform/gcp/modules/workflow/variables.tf",
	"deploy/terraform/gcp/roots/compute/main.tf",
	"deploy/terraform/gcp/roots/compute/outputs.tf",
	"deploy/terraform/gcp/roots/compute/tests/compute.tftest.hcl",
	"deploy/terraform/gcp/roots/firebase/main.tf",
	"deploy/terraform/gcp/roots/firebase/outputs.tf",
	"deploy/terraform/gcp/roots/firebase/tests/firebase.tftest.hcl",
	"deploy/terraform/gcp/roots/firebase/variables.tf",
	"deploy/terraform/gcp/roots/installation/main.tf",
	"deploy/terraform/gcp/roots/installation/outputs.tf",
	"deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl",
	"deploy/terraform/gcp/roots/installation/variables.tf",
	"deploy/terraform/gcp/roots/repo/main.tf",
	"deploy/terraform/gcp/roots/repo/outputs.tf",
	"deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl",
	"deploy/terraform/gcp/roots/repo/tests/testdata/README.md",
	"deploy/terraform/gcp/roots/repo/variables.tf",
	"deploy/terraform/terraform.go",
	"deploy/terraform/terraform_test.go",
	"docs/backends.md",
	"docs/base-image-migration.md",
	"docs/base-image.md",
	"docs/checkout.md",
	"docs/coordinator/README.md",
	"docs/coordinator/reconciliation.md",
	"docs/dogfooding.md",
	"docs/gcp-live-checklist.md",
	"docs/gcp-setup.md",
	"docs/git-providers.md",
	"docs/in-job-review/reconciliation.md",
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
	"docs/windows-cli/reconciliation.md",
	"images/history/Dockerfile",
	"images/verify-public.sh",
	"internal/agent/agent_test.go",
	"internal/backend/backend.go",
	"internal/backend/backendtest/backendtest.go",
	"internal/backend/gcp/live_test.go",
	"internal/backend/gcp/names.go",
	"internal/backend/gcp/run.go",
	"internal/backend/gcp/run_test.go",
	"internal/basepin/basepin.go",
	"internal/budget/fixture_test.go",
	"internal/budget/registry.go",
	"internal/budget/registry_test.go",
	"internal/budget/rollover.go",
	"internal/budget/rules/creds_emulator_test.go",
	"internal/budget/rules/harness_test.go",
	"internal/budget/rules/marker.go",
	"internal/budget/session.go",
	"internal/budget/session_test.go",
	"internal/cli/allow_delete.go",
	"internal/cli/budget.go",
	"internal/cli/budget_preview.go",
	"internal/cli/cloud.go",
	"internal/cli/config_list.go",
	"internal/cli/config_list_test.go",
	"internal/cli/config_publish.go",
	"internal/cli/docs_base_test.go",
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
	"internal/cli/doctor.go",
	"internal/cli/doctor_bucket.go",
	"internal/cli/doctor_bucket_test.go",
	"internal/cli/doctor_policy_test.go",
	"internal/cli/doctor_shared_test.go",
	"internal/cli/doctor_test.go",
	"internal/cli/exec.go",
	"internal/cli/exec_test.go",
	"internal/cli/gitsecret.go",
	"internal/cli/history_rollover.go",
	"internal/cli/image.go",
	"internal/cli/image_cloud_test.go",
	"internal/cli/image_layer.go",
	"internal/cli/image_layer_test.go",
	"internal/cli/image_refresh.go",
	"internal/cli/image_refresh_rules.go",
	"internal/cli/image_refresh_steps.go",
	"internal/cli/image_refresh_steps_test.go",
	"internal/cli/image_refresh_test.go",
	"internal/cli/image_refresh_upgrade_test.go",
	"internal/cli/image_test.go",
	"internal/cli/imagecheck.go",
	"internal/cli/imagecheck_test.go",
	"internal/cli/init.go",
	"internal/cli/init_adopt.go",
	"internal/cli/init_adopt_test.go",
	"internal/cli/init_anchor.go",
	"internal/cli/init_anchor_test.go",
	"internal/cli/init_apis_test.go",
	"internal/cli/init_budget_test.go",
	"internal/cli/init_compute_test.go",
	"internal/cli/init_firebase.go",
	"internal/cli/init_firebase_test.go",
	"internal/cli/init_fugaroyaml.go",
	"internal/cli/init_fugaroyaml_test.go",
	"internal/cli/init_gate_test.go",
	"internal/cli/init_images.go",
	"internal/cli/init_images_test.go",
	"internal/cli/init_inputs.go",
	"internal/cli/init_managed_test.go",
	"internal/cli/init_plugin_stage.go",
	"internal/cli/init_project.go",
	"internal/cli/init_project_test.go",
	"internal/cli/init_repo_gate.go",
	"internal/cli/init_repo_gate_test.go",
	"internal/cli/init_repo_stage.go",
	"internal/cli/init_repo_stage_test.go",
	"internal/cli/init_repo_target.go",
	"internal/cli/init_retire.go",
	"internal/cli/init_retire_test.go",
	"internal/cli/init_review_test.go",
	"internal/cli/init_stages.go",
	"internal/cli/init_test.go",
	"internal/cli/initsecrets.go",
	"internal/cli/initsecrets_pty_test.go",
	"internal/cli/launch_budget.go",
	"internal/cli/launch_budget_test.go",
	"internal/cli/layer_routes_overlap_test.go",
	"internal/cli/layer_sites_test.go",
	"internal/cli/lswarn.go",
	"internal/cli/managed_apply_test.go",
	"internal/cli/managed_flag.go",
	"internal/cli/managed_test.go",
	"internal/cli/operator_write.go",
	"internal/cli/operator_write_test.go",
	"internal/cli/project.go",
	"internal/cli/project_check_test.go",
	"internal/cli/project_cmd.go",
	"internal/cli/project_enroll.go",
	"internal/cli/project_job_test.go",
	"internal/cli/project_policy_test.go",
	"internal/cli/project_shared_test.go",
	"internal/cli/project_test.go",
	"internal/cli/recipes_skew.go",
	"internal/cli/recipes_skew_test.go",
	"internal/cli/report.go",
	"internal/cli/restore.go",
	"internal/cli/restore_test.go",
	"internal/cli/roles.go",
	"internal/cli/root_test.go",
	"internal/cli/run_allowlist_test.go",
	"internal/cli/run_oauth_test.go",
	"internal/cli/run_recipe_test.go",
	"internal/cli/runs_test.go",
	"internal/cli/secrets.go",
	"internal/cli/secrets_test.go",
	"internal/cli/sharedcfg.go",
	"internal/cli/sharedcfg_fetch_test.go",
	"internal/cli/sharedcfg_test.go",
	"internal/cli/sharedcfg_validate_test.go",
	"internal/cli/testdata/init_commands.golden",
	"internal/cli/testdata/init_retire.golden",
	"internal/cli/upgrade.go",
	"internal/cli/upgrade_cloud.go",
	"internal/cli/upgrade_cloud_step_test.go",
	"internal/cli/upgrade_cloud_test.go",
	"internal/cli/validate.go",
	"internal/cli/validate_test.go",
	"internal/config/config.go",
	"internal/config/example.go",
	"internal/config/example.yaml",
	"internal/config/project_secrets.go",
	"internal/config/providers.go",
	"internal/config/routing.go",
	"internal/e2e/init_test.go",
	"internal/e2e/live_budget_test.go",
	"internal/gateway/route.go",
	"internal/gcpfake/logging.go",
	"internal/gcpfake/rtdb.go",
	"internal/gcpfake/serviceusage.go",
	"internal/imagecheck/check.go",
	"internal/infra/apis.go",
	"internal/infra/checkjob.go",
	"internal/infra/checkjob_test.go",
	"internal/infra/discover.go",
	"internal/infra/discover_firebase.go",
	"internal/infra/discover_firebase_test.go",
	"internal/infra/discover_test.go",
	"internal/infra/firebase.go",
	"internal/infra/firebase_test.go",
	"internal/infra/firebasedb.go",
	"internal/infra/hardening.go",
	"internal/infra/hardening_test.go",
	"internal/infra/installation.go",
	"internal/infra/installation_terraform_test.go",
	"internal/infra/installation_test.go",
	"internal/infra/managed/deletelist.go",
	"internal/infra/managed/deletelist_test.go",
	"internal/infra/managed/marker.go",
	"internal/infra/project.go",
	"internal/infra/projectcreate.go",
	"internal/infra/projectjobs.go",
	"internal/infra/projectjobs_test.go",
	"internal/infra/readiness.go",
	"internal/infra/readiness_optional_test.go",
	"internal/infra/readiness_policy_test.go",
	"internal/infra/repo.go",
	"internal/infra/retire.go",
	"internal/infra/spec.go",
	"internal/infra/spec_test.go",
	"internal/infra/statebucket.go",
	"internal/infra/statebucket_test.go",
	"internal/infra/tf/env.go",
	"internal/infra/tf/managed_cover.go",
	"internal/infra/tf/project_golden_test.go",
	"internal/infra/tf/tf.go",
	"internal/infra/tfvars.go",
	"internal/infra/workdir.go",
	"internal/infra/workdir_test.go",
	"internal/initflow/loop.go",
	"internal/initflow/loop_test.go",
	"internal/initflow/stage.go",
	"internal/localcfg/localcfg.go",
	"internal/localcfg/localcfg_test.go",
	"internal/localcfg/select.go",
	"internal/localcfg/select_shared_test.go",
	"internal/localcfg/select_test.go",
	"internal/managed/apply.go",
	"internal/managed/budget.go",
	"internal/managed/budget_test.go",
	"internal/managed/inventory.go",
	"internal/managed/inventory_test.go",
	"internal/mirror/ref.go",
	"internal/preflight/google.go",
	"internal/preflight/preflight.go",
	"internal/projectpolicy/accounts.go",
	"internal/projectpolicy/policy.go",
	"internal/projectpolicy/profiles.go",
	"internal/projectpolicy/profiles_test.go",
	"internal/projectpolicy/read.go",
	"internal/projectpolicy/read_test.go",
	"internal/projectpolicy/version.go",
	"internal/projectpolicy/version_test.go",
	"internal/runner/backend_run_test.go",
	"internal/runner/backend_test.go",
	"internal/runner/budget_session.go",
	"internal/runner/gateway.go",
	"internal/runner/halt.go",
	"internal/runner/halt_test.go",
	"internal/runner/layer_claim_test.go",
	"internal/runner/policy.go",
	"internal/runner/policy_test.go",
	"internal/runner/project.go",
	"internal/runner/project_test.go",
	"internal/runner/projectjob.go",
	"internal/runner/projectjob_internal_test.go",
	"internal/runner/projectpolicy.go",
	"internal/runner/projectpolicy_test.go",
	"internal/runner/provider_policy_test.go",
	"internal/runner/providers.go",
	"internal/runner/providers_test.go",
	"internal/runner/spend.go",
	"plugin/readme_test.go",
	"plugin/setup_skill_test.go",
	"plugin/skills/setup/SKILL.md",
	"plugin/skills/setup/reference/decisions.md",
	"plugin/skills/setup/reference/transitions.md",
	"plugin/skills/working/reference/status.md",
	"plugin/skills_lint_test.go",
	"schemas/fugaro.schema.json",
	"website/README.md",
	"website/content/cli-reference.md",
	"website/content/security.md",
	"website/data/install.yaml",
	"website/src/components/Changelog.astro",
	"website/src/pages/getting-started.astro",
	"website/test/caps-and-costs.mjs",
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

// censusNamesInit reports whether the file at path names the command. A file
// whose first 8 KiB hold a NUL byte is binary and is skipped: the tree carries
// two Windows binaries at its root, and a byte pattern inside a compiled
// artifact is not a string anybody renames.
func censusNamesInit(t *testing.T, path string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	return initNameRE.Match(data)
}

// liveInitCensus walks liveRoots and returns, sorted, every non-exempt file
// that names the command. A root that is not there is fatal rather than
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
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if censusExempt(rel) {
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
// not listed -- internal/cli/docs_checkout_test.go is one -- and it fails on
// the set having grown; rename every mention out of a listed file
// (internal/cli/config_list.go has one) and it fails on the set having shrunk.
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
	// where that is visible: they are on the list today, and they leave it when
	// the generator runs, not when a hand edit does.
	for _, generated := range []string{"website/content/cli-reference.md", "website/content/security.md"} {
		if !slices.Contains(pendingRename, generated) {
			t.Errorf("pendingRename does not list the generated %s: it names the command today, and W2.4 takes it off the list by regenerating it, not by editing it", generated)
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
		if name == ".git" || slices.Contains(liveRoots, name) {
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
			if d.IsDir() {
				return nil
			}
			read++
			if !censusNamesInit(t, path) {
				return nil
			}
			rel, err := filepath.Rel(censusRoot, path)
			if err != nil {
				return err
			}
			t.Errorf("%s names `fugaro init`, but %s/ is not a live root: add it to liveRoots, or rename the mention -- the walk and the rename's task list have to cover the same set", filepath.ToSlash(rel), name)
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
