package cli

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// providersConfig is a providers and model_prices block the job-config-copy
// tests append to the rig's local config (the shape docs/multi-model.md
// §2 shows): one provider, openrouter, allowed to see acme/app's code, and
// one price override.
const providersConfig = "providers:\n  openrouter: { kind: anthropic-compat, base_url: https://openrouter.ai/api, auth: bearer, secret: openrouter-api-key, models: [\"deepseek/*\"], allow_data_to: [acme/app] }\n" +
	"model_prices:\n  deepseek/deepseek-v4-flash: { input_per_m: 0.14, output_per_m: 0.28 }\n"

// stubCurrentRefresh appends base_images and build records that make
// acme/app's refresh plan current (TestUpgradeRerunWhenCurrentIsNothingToDo's
// setup), so a real --yes run reaches jobConfigCopyNote without asking for
// any confirmation.
func stubCurrentRefresh(t *testing.T, r *anchorModeRig) {
	t.Helper()
	r.appendConfig(t, "base_images: {base: "+managedRef("base", "0.5.1")+"}\n")
	putWorkflowRecordFrom("app", "0.5.1", managedRef("base", "0.5.1"))
	putWorkflowRecordFrom("api", "0.5.1", managedRef("base", "0.5.1"))
}

// upgradeRigWithGitHubAppID is upgradeRig with acme/app's github_app_id set
// and the runs bucket switched to a file:// directory of the test's own,
// carrying the installation's project marker. Every other upgrade test
// stubs the refresh's own reload step (fakeSteps), which never actually
// computes a repository spec or opens the runs bucket, so it never needs
// either; jobConfigCopyNote calls infra.Workflow and openCloudFor directly,
// unstubbed, so it does (the rig's runs_bucket name has no real gs:// bucket
// behind it).
func upgradeRigWithGitHubAppID(t *testing.T) *anchorModeRig {
	t.Helper()
	fakeTerraform(t)
	upgradeEnv(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, false)
	r.appendConfig(t, "  acme/app: { provider: github, workflows: [app], github_app_id: \"12345\" }\n")
	runs := t.TempDir()
	r.appendConfig(t, "bucket_url: file://"+runs+"\n")
	marker, err := json.Marshal(infra.ProjectMarker{Version: 1, Name: "aurora", GCPProject: "proj-1234"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runs, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "fugaro", "project.json"), marker, 0o644); err != nil {
		t.Fatal(err)
	}
	return r
}

// jobConfigCopyJobName is the job jobConfigCopyNote reads for acme/app's
// workflow (refreshYAML has two, "api" and "app", which sort in that order),
// at the default Claude account (gcp.JobName's variant collapses to the bare
// workflow name for it).
func jobConfigCopyJobName(t *testing.T, workflow string) string {
	t.Helper()
	slug, err := task.Slug("github", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	return gcp.JobName(slug, workflow)
}

// jobConfigCopyBaseline is a deployed job's env when it was last deployed
// from exactly today's local config and fugaro.yaml: only the GitHub App ID
// (upgradeRigWithGitHubAppID's rig sets no budget, providers or model_prices
// by default). The GitHub App ID is compared like every other local-config
// field now (review follow-up, PR #324), so every job-config-copy test below
// starts here and adds only the one field it means to test, rather than
// risk an incidental "added FUGARO_GITHUB_APP_ID" diff it never meant to
// exercise.
func jobConfigCopyBaseline() map[string]string {
	return map[string]string{"FUGARO_GITHUB_APP_ID": "12345"}
}

// setJobConfigCopyEnv creates (or replaces) the fake job jobConfigCopyNote
// reads for workflow, with jobConfigCopyBaseline merged with extra (extra
// wins on a shared key).
func setJobConfigCopyEnv(t *testing.T, r *anchorModeRig, workflow string, extra map[string]string) {
	t.Helper()
	job := jobConfigCopyJobName(t, workflow)
	r.run.SetJob(job, nil, "img")
	env := jobConfigCopyBaseline()
	maps.Copy(env, extra)
	r.run.SetJobEnv(job, env)
}

// TestUpgradeCloudReportsStaleJobConfigCopy: the deployed job carries no
// providers or model_prices (an older fugaro update --repo, or none at all),
// while the local config now has both; the real cloud step's note names
// both fields, by name, and the one command that fixes it.
//
// Mutation (verified, run and restored): add `return ""` as
// jobConfigCopyNote's first line: this test fails, expecting the STALE note
// and finding none.
func TestUpgradeCloudReportsStaleJobConfigCopy(t *testing.T) {
	r := upgradeRigWithGitHubAppID(t)
	r.appendConfig(t, providersConfig)
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil)
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"note: the deployed job's copy of the local project config is STALE",
		"providers: added openrouter",
		"model_prices: added deepseek/deepseek-v4-flash",
		"fugaro update --repo .",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestUpgradeNoteSaysTheEnvCopyIsInert: once a policy is published, the
// drift note stops sending the operator to update --repo for the keys the
// policy governs — runs read those from the node, not from the job's
// environment — and names what a job's environment still has to be
// refreshed for (plan task 1.6 step 5).
//
// Mutation (verified, run and restored): drop the
// `if p := jobConfigCopyPolicyNote(cctx, lc); p != ""` clause from
// jobConfigCopyNote: this test fails, finding "use the old copy until you
// run fugaro update --repo" where the policy clause belongs.
func TestUpgradeNoteSaysTheEnvCopyIsInert(t *testing.T) {
	r := upgradeRigWithGitHubAppID(t)
	r.appendConfig(t, providersConfig)
	db := gcpfake.NewRTDB(t)
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5, rtdb_url: "+db.URL+" }\n")
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil)
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"the deployed job's copy of the local project config is STALE",
		"the policy published at " + projectpolicy.PathShared() + " now governs",
		"not a reason to re-run fugaro update --repo",
		// The providers block is the part of the node a run does not read:
		// routing by the job's own copy is what the gateway's allow_data_to
		// gate and the routing switch resolve against, so a changed provider
		// still needs update --repo, and the note has to say so rather than call
		// the whole copy inert.
		"providers block is the part the node carries that a run does not read from it",
		"still a reason to re-run it",
		infra.RTDBURLEnv,
		infra.FirebaseAPIKeyEnv,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the note lacks %q:\n%s", want, out)
		}
	}
	// The whole-copy sentence is what the policy clause replaces: the drift the
	// note just reported is exactly what the policy now governs.
	if strings.Contains(out, "use the old copy until you run") {
		t.Errorf("the note still sends the operator to update --repo for the whole copy:\n%s", out)
	}
}

// TestJobConfigCopyMountNoteIsSilentWithoutAProjectScopeName: the clause is
// about the names this repository may mount from the project's container, so
// with none of them there is nothing it could tell a reader and it returns
// "" — a note that stayed silent when every name resolves to the
// repository's own container, which is what makes the mount-only case above
// worth printing at all.
func TestJobConfigCopyMountNoteIsSilentWithoutAProjectScopeName(t *testing.T) {
	if got := jobConfigCopyMountNote(nil); got != "" {
		t.Errorf("the mount clause is not silent with no project-scope name: %q", got)
	}
	if got := jobConfigCopyMountNote([]string{}); got != "" {
		t.Errorf("the mount clause is not silent with an empty list: %q", got)
	}
	if got := jobConfigCopyMountNote([]string{"github-app-key"}); !strings.Contains(got, "github-app-key") {
		t.Errorf("the mount clause does not name the project-scope name: %q", got)
	}
}

// TestUpgradeNoteNamesTheMountCase (plan Task 2.3 step 2): after D2 a name
// stored at project scope changes what a job mounts when the project's copy
// is created or deleted, so the drift note must say that the mounts are
// still a reason to re-run update --repo — the one thing the policy does not
// carry — and that phase 3 moves the mounts to a shared profile.
//
// Mutation (verified, run and restored): make jobConfigCopyMountNote return
// "": this test fails, finding no mount clause in the note.
func TestUpgradeNoteNamesTheMountCase(t *testing.T) {
	r := upgradeRigWithGitHubAppID(t)
	r.appendConfig(t, providersConfig)
	db := gcpfake.NewRTDB(t)
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	db.Set(projectpolicy.PathShared(), map[string]any{
		"version":     projectpolicy.Version,
		"budget":      map[string]any{"mode": "enforce", "per_run_usd": 5},
		"allow_repos": []string{"github/acme/app"},
	})
	r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5, rtdb_url: "+db.URL+" }\n")
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil)
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"what a job mounts is still decided here",
		"fugaro secrets set NAME",
		"phase 3 gives the mounts to a shared profile",
		"fugaro update --repo .",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the note lacks %q:\n%s", want, out)
		}
	}
}

// TestUpgradeNoteSaysWhatNeedsNoRerun: the mount clause has two halves, and
// the second is the one that saves an operator a needless apply. Only
// creating or deleting a project-scope copy changes what a job mounts; a new
// version of that container, or a rotated value, changes nothing at all,
// because the job mounts the container rather than a copy of the value. A
// note that named only the first half would read as "any secret change means
// another fugaro update --repo".
//
// Written first, against the note as it was (the mount clause named only the
// reason to re-run): it fails, finding no "needs no re-run".
func TestUpgradeNoteSaysWhatNeedsNoRerun(t *testing.T) {
	r := upgradeRigWithGitHubAppID(t)
	r.appendConfig(t, providersConfig)
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil)
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"what a job mounts is still decided here",
		"created or deleted",
		"a new version",
		"rotated value",
		"needs no re-run",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the mount clause lacks %q:\n%s", want, out)
		}
	}
}

// TestUpgradeNoteNamesTheMountCaseWithNothingElseStale: the mount-only case.
// Every env key the deployed job carries matches this checkout's — the STALE
// half has nothing to say — but whether a name resolves to the project's
// container or the repository's is not in that env copy at all (the variable
// name is the same either way; the mount is the IAM member and
// repo.project_secrets), so an env diff can never show that a project-scope
// copy was created or deleted after the apply. The note has to say so on its
// own, naming the names this repository may mount project-wide.
//
// Written first, against the note as it was (the mount clause was appended
// only after the STALE branch, which returns early when nothing differs): it
// fails, finding no mount clause at all.
func TestUpgradeNoteNamesTheMountCaseWithNothingElseStale(t *testing.T) {
	r := upgradeRigWithGitHubAppID(t)
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil) // exactly what this local config writes now
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"what a job mounts is still decided here",
		"github-app-key",
		"created or deleted",
		"fugaro update --repo .",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the mount-only note lacks %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"STALE", "use the old copy until"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a matching env copy was reported as %q:\n%s", unwanted, out)
		}
	}
}

// TestUpgradeCloudStaleJobConfigNoteNeverLeaksAValue: the note names fields
// (provider and model IDs), never a value the config carries alongside them
// -- the secret's name, the base URL or a price.
//
// Mutation (verified, run and restored): in mapDiff's "added" case, append
// `fmt.Sprintf("%s=%+v", k, now[k])` instead of just `k`: this test fails,
// the note carrying the provider's base URL and secret name.
func TestUpgradeCloudStaleJobConfigNoteNeverLeaksAValue(t *testing.T) {
	r := upgradeRigWithGitHubAppID(t)
	r.appendConfig(t, providersConfig)
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil)
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "STALE") {
		t.Fatalf("expected the stale note:\n%s", out)
	}
	for _, leak := range []string{"openrouter-api-key", "openrouter.ai", "0.14", "0.28", "bearer"} {
		if strings.Contains(out, leak) {
			t.Fatalf("the note leaked %q:\n%s", leak, out)
		}
	}
}

// TestUpgradeCloudCopyNoteNeverReadsTheCloudUnderCheckOrAgent: --check makes
// no cloud call (U6) and a coding agent's session never reaches the cloud
// step at all (U7); the job-config-copy note must not change that, even when
// providers and model_prices are configured, by reading the fake Cloud Run
// server directly.
//
// Mutation (run, restore): in cloudStep's --check branch, call
// jobConfigCopyNote(ctx, ...) directly instead of only printing
// checkJobCopyCaveat: this test fails, finding a request on r.run.
// Both rigs below use upgradeRigWithGitHubAppID, not the plain upgradeRig:
// its working file:// bucket and github_app_id let jobConfigCopyNote run to
// completion (reaching r.run) if it were wrongly called here, so
// r.run.Requests() actually catches a violation instead of the call merely
// failing earlier at an unrelated, un-faked gs:// bucket.
//
// Mutation (verified, run and restored): in cloudStep's --check branch, also
// call `jobConfigCopyNote(ctx, u.loc.Root, u.o.refresh.cloud)` and print it:
// the "check" subtest fails, finding two GET requests on r.run (the job's
// FUGARO_MODEL_PROVIDERS and FUGARO_MODEL_PRICES).
func TestUpgradeCloudCopyNoteNeverReadsTheCloudUnderCheckOrAgent(t *testing.T) {
	t.Run("check", func(t *testing.T) {
		r := upgradeRigWithGitHubAppID(t)
		r.appendConfig(t, providersConfig)
		stubCurrentRefresh(t, r)
		setJobConfigCopyEnv(t, r, "api", nil)
		out, _, err := executeStdin(t, "", "upgrade", "--check")
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit %d\n%s", ExitCode(err), out)
		}
		if !strings.Contains(out, "note: "+checkJobCopyCaveat) {
			t.Fatalf("missing the --check caveat:\n%s", out)
		}
		if len(r.run.Requests()) != 0 {
			t.Fatalf("--check read the cloud: %+v", r.run.Requests())
		}
	})
	t.Run("agent", func(t *testing.T) {
		r := upgradeRigWithGitHubAppID(t)
		r.appendConfig(t, providersConfig)
		stubCurrentRefresh(t, r)
		setJobConfigCopyEnv(t, r, "api", nil)
		t.Setenv("CLAUDECODE", "1")
		out, _, err := executeStdin(t, "", "upgrade")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if strings.Contains(out, "job's copy") {
			t.Fatalf("the agent-session skip reported on the job's copy:\n%s", out)
		}
		if len(r.run.Requests()) != 0 {
			t.Fatalf("the agent session read the cloud: %+v", r.run.Requests())
		}
	})
}

// TestUpgradeCloudReportsStaleBudgetConfigCopy is the review's own bug
// reproduction (PR #324 review, finding 1): providers and model_prices
// match (so the old, narrower comparison would have found nothing to say),
// but the local config now also sets a budget the deployed job's copy
// lacks; the note must still name it, by field.
//
// Mutation (verified, run and restored): in scalarConfigEnv, also delete
// infra.BudgetModeEnv and infra.MaxRunUSDEnv (narrowing the comparison back
// to roughly its old, pre-fix scope): this test fails, finding no note.
func TestUpgradeCloudReportsStaleBudgetConfigCopy(t *testing.T) {
	r := upgradeRigWithGitHubAppID(t)
	r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5 }\n")
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil) // no budget on the job's copy
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"note: the deployed job's copy of the local project config is STALE",
		"FUGARO_BUDGET_MODE", "FUGARO_MAX_RUN_USD",
		"fugaro update --repo .",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	// Never a value: the cap itself (5, or its formatted "5") is not a
	// secret, but the note names fields, never values, of anything.
	if strings.Contains(out, "providers:") || strings.Contains(out, "model_prices:") {
		t.Fatalf("falsely named providers/model_prices (nothing configures them here):\n%s", out)
	}
}

// TestUpgradeCloudCheckCaveatFiresEvenWithAnEmptyLocalConfig: the caveat
// used to key off whether the local config currently sets providers or
// model_prices, so an owner who had just removed every provider locally
// (the job may still carry the old ones) saw nothing under --check; it now
// fires whenever a real cloud step would run, whatever the local config
// currently holds.
//
// Mutation (verified, run and restored): put back
// `&& (len(lc.Providers) > 0 || len(lc.ModelPrices) > 0)` on the printed
// branch in cloudStep: this test fails, finding no caveat.
func TestUpgradeCloudCheckCaveatFiresEvenWithAnEmptyLocalConfig(t *testing.T) {
	r, _ := upgradeRig(t) // refreshYAML's local config sets no providers, model_prices or budget at all
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--check")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
	if !strings.Contains(out, "note: "+checkJobCopyCaveat) {
		t.Fatalf("missing the --check caveat with an empty local config:\n%s", out)
	}
	if len(r.run.Requests()) != 0 {
		t.Fatalf("--check read the cloud: %+v", r.run.Requests())
	}
}

// jobReadErrorMarker is the text the fake's failed jobs.get carries, and what
// this test asks the output never to hold. A marker word, not a status
// number: the first version asserted the output never holds "500" (the status
// the fake answers), but every rig makes its temp directories under a name
// derived from the test's own -- `TestUpgradeCloud...NNN` plus random digits
// -- so a random suffix carrying "500" put "500" in the output on its own and
// failed the test for no defect at all. That is the flake that blocked the
// 0.9.6 and 0.9.7 release gates. A word no path and no number can carry
// cannot arrive that way, so a failure here now means the note really did
// repeat the error it was handed.
const jobReadErrorMarker = "kestrel-injected-job-read-failure"

// TestUpgradeCloudCouldNotCheckJobConfigCopyOnAJobReadError: a real error
// reading the deployed job (here, the fake failing jobs.get with
// jobReadErrorMarker as its message) must not look like "the copies match":
// it is reported, and the report never repeats the underlying error (which
// could carry a URL or, from a real client library, a token).
//
// Mutation (verified, run and restored): in jobConfigCopyNote, change
// `if err != nil { return couldNotCheckJobConfigCopy(...) }` after JobFullEnv
// to `if err != nil && false`: this test fails, finding no note (silence
// that looks like a match). Mutation (verified, run and restored), for the
// leak assertion itself: append the error's own text to the note there
// (`couldNotCheckJobConfigCopy("reading the deployed job failed") + " " + err.Error()`),
// which keeps the note's fixed reason and adds jobReadErrorMarker, and fails
// the test on the marker -- so the marker is text the error really carries,
// not a string no run can produce.
func TestUpgradeCloudCouldNotCheckJobConfigCopyOnAJobReadError(t *testing.T) {
	r := upgradeRigWithGitHubAppID(t)
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil)
	r.run.SetJobFaults(gcpfake.JobFaults{GetStatus: 500, GetError: jobReadErrorMarker})
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "note: could not check the job's config copy (reading the deployed job failed)") {
		t.Fatalf("missing the could-not-check note:\n%s", out)
	}
	for _, leak := range []string{jobReadErrorMarker, "googleapi", "http://", "https://"} {
		if strings.Contains(out, leak) {
			t.Fatalf("the note leaked the underlying error %q:\n%s", leak, out)
		}
	}
}

// TestJobConfigCopyNoteReportsCloudFailedInsteadOfGoingSilent: review
// follow-up (PR #324, NIT): jobConfigCopyNote returned "" for any non-nil
// verdict from resolveCloudRepo, treating a real cloudFailed error (a bad
// fugaro.yaml or origin, an unreadable local config) the same as a U9 skip
// (no fugaro.yaml, a teammate checkout): silence that looks like "the
// copies match". Calls jobConfigCopyNote directly (not through the full
// upgrade command, whose own earlier cloudCheck call would normally catch
// the same cloudFailed condition first and stop the checkout before
// jobConfigCopyNote ever runs): this isolates the one path where
// resolveCloudRepo's own verdict reaches jobConfigCopyNote's v != nil
// branch, whatever lets it get there in a real run (a file changing between
// the cloud step's own check and this one, for instance).
//
// Mutation (verified, run and restored): change `if v.state == cloudFailed`
// to `if false`: this test fails, finding "".
func TestJobConfigCopyNoteReportsCloudFailedInsteadOfGoingSilent(t *testing.T) {
	upgradeRigWithGitHubAppID(t)
	loc, ok := pluginwire.Locate(".")
	if !ok {
		t.Fatal("not a checkout")
	}
	testutil.Git(t, loc.Root, "remote", "remove", "origin")
	note := jobConfigCopyNote(t.Context(), loc.Root, cloudOptions{})
	if !strings.Contains(note, "could not check the job's config copy (") {
		t.Fatalf("jobConfigCopyNote = %q, want a could-not-check note for resolveCloudRepo's cloudFailed", note)
	}
}

// TestJobConfigCopyTimeoutBoundsASlowCloudCall: discovered running the full
// suite for a previous round of this review (not one of its findings):
// jobConfigCopyNote used the caller's own, often-undeadlined context, so a
// real upgrade whose Cloud Run read hit a slow or unreachable backend (an
// offline network, a black-holed connection) hung in the client's own
// retry/backoff loop for as long as the process let it — minutes, in one
// full-suite run — instead of giving up and reporting "could not check".
// jobConfigCopyTimeout now bounds that work (the same risk
// layerBucketTimeout, layer_resolve.go, already guards against for the
// project layer's bucket read).
//
// Review follow-up (PR #324, MINOR): an earlier version of this test drove
// openCloudFor and JobFullEnv directly with its own cctx, so it could not
// catch jobConfigCopyNote itself forgetting to use the bounded one, and it
// needed a real unresponsive httptest server plus a 200ms wall-clock wait.
// This version instead runs the real fugaro upgrade command, through
// jobConfigCopyNote, against upgradeRigWithGitHubAppID's ordinary, healthy
// fake Cloud Run and file:// bucket, with jobConfigCopyTimeout set to a
// negative duration: context.WithTimeout's deadline is then already in the
// past the instant jobConfigCopyNote creates the context, so its first
// cloud call fails immediately — no waiting, no unresponsive server, fully
// deterministic. If jobConfigCopyNote instead used its caller's own (live,
// undeadlined) context for those calls, they would succeed against this
// test's healthy fakes, and the note below would never print.
//
// Mutation (verified, run and restored): in jobConfigCopyNote, pass ctx
// instead of cctx to both openCloudFor and JobFullEnv: this test fails,
// finding no "could not check" note (the calls succeed instead, against the
// live fakes, and the upgrade reports the job current).
func TestJobConfigCopyTimeoutBoundsASlowCloudCall(t *testing.T) {
	r := upgradeRigWithGitHubAppID(t)
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil)

	// A negative duration makes context.WithTimeout's deadline a moment
	// already in the past, so the context it returns is Done() the instant
	// jobConfigCopyNote creates it: no real waiting, no unresponsive server
	// needed, and the failure is deterministic. If jobConfigCopyNote used
	// its caller's own (live, undeadlined) context for its cloud calls
	// instead of this bounded one, those calls would succeed against this
	// test's perfectly healthy fake Cloud Run and file:// bucket, and the
	// note below would never print.
	old := jobConfigCopyTimeout
	jobConfigCopyTimeout = -1 * time.Second
	t.Cleanup(func() { jobConfigCopyTimeout = old })

	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "note: could not check the job's config copy (") {
		t.Fatalf("missing the could-not-check note for an already-expired deadline:\n%s", out)
	}
}

// TestUpgradeCloudChecksEveryWorkflowsJobNotJustTheFirst: review follow-up
// (PR #324): refreshYAML has two workflows, api (sorted first) and app.
// api's job does not exist at all yet (as if it were just added to
// fugaro.yaml and never deployed), while app's job does exist and is
// genuinely stale. Checking only the alphabetically-first workflow's job
// (api, which does not exist) used to make the whole note go silent,
// hiding app's real staleness; every workflow with a job must be checked.
//
// Mutation (verified, run and restored): in jobConfigCopyNote, replace the
// loop over names with a single check of names[0] (api) alone: this test
// fails, finding no note (api's job does not exist, and the old code never
// looked at app's).
func TestUpgradeCloudChecksEveryWorkflowsJobNotJustTheFirst(t *testing.T) {
	r := upgradeRigWithGitHubAppID(t)
	r.appendConfig(t, providersConfig)
	stubCurrentRefresh(t, r)
	// api: no job at all (never deployed). app: a job deployed, stale (the
	// baseline only: no providers, no model_prices).
	setJobConfigCopyEnv(t, r, "app", nil)
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"note: the deployed job's copy of the local project config is STALE",
		"providers: added openrouter",
		"model_prices: added deepseek/deepseek-v4-flash",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestUpgradeCloudUnionsDiffsAcrossWorkflowsDeduplicated: both api and app
// have a deployed job; api's lacks the configured providers and app's
// matches (deployed more recently), or vice versa — either way, a field
// that is stale on at least one job is named once, not once per workflow.
//
// Mutation (verified, run and restored): in jobConfigCopyNote, change
// `if !seen[d]` to `if true` (still setting seen[d], but never checking it):
// this test fails, finding the diff text twice.
func TestUpgradeCloudUnionsDiffsAcrossWorkflowsDeduplicated(t *testing.T) {
	r := upgradeRigWithGitHubAppID(t)
	r.appendConfig(t, providersConfig)
	stubCurrentRefresh(t, r)
	setJobConfigCopyEnv(t, r, "api", nil) // stale: no providers/model_prices
	setJobConfigCopyEnv(t, r, "app", nil) // also stale, the same way
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := strings.Count(out, "providers: added openrouter"); n != 1 {
		t.Fatalf("the diff appeared %d times, want exactly 1 (deduplicated):\n%s", n, out)
	}
}

// jobConfigCopySecretNames are the env vars deploy/terraform/gcp/modules/workflow/job.tf's
// secret_env dynamic block mounts for refreshYAML's fixture (git: github,
// agent.auth: oauth): the git credential and the Claude account token. A
// real deployed job never carries a value for these, only a valueSource;
// jobBodyWithSecretValueSources models exactly that.
var jobConfigCopySecretNames = []string{"FUGARO_GITHUB_APP_PRIVATE_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}

// jobBodyWithSecretValueSources is a Cloud Run job's raw jobs.get body (for
// gcpfake.Run.SetJobJSON): env's entries as plain values, plus
// jobConfigCopySecretNames mounted the way job.tf deploys every secret_env
// entry, a valueSource and no value at all. Modeling this in the fake is the
// point of the test that uses it: JobFullEnv must not read a secret's
// valueSource entry as config that was "removed".
func jobBodyWithSecretValueSources(t *testing.T, job string, env map[string]string) string {
	t.Helper()
	var entries []map[string]any
	for _, k := range slices.Sorted(maps.Keys(env)) {
		entries = append(entries, map[string]any{"name": k, "value": env[k]})
	}
	for _, s := range jobConfigCopySecretNames {
		entries = append(entries, map[string]any{"name": s, "valueSource": map[string]any{
			"secretKeyRef": map[string]any{"secret": "projects/proj-1234/secrets/x", "version": "latest"},
		}})
	}
	body := map[string]any{
		"name": "projects/proj-1234/locations/us-east5/jobs/" + job,
		"template": map[string]any{"taskCount": 1, "template": map[string]any{
			"maxRetries": 0,
			"containers": []any{map[string]any{
				"image":     "img",
				"resources": map[string]any{"limits": map[string]any{"cpu": "1", "memory": "512Mi"}},
				"env":       entries,
			}},
		}},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// jobConfigCopyRealSpec is acme/app's workflow spec, computed exactly as
// jobConfigCopyNote computes it (resolveCloudRepo, checkoutURL,
// infra.Workflow): the review's own reproduction needs "a job built from
// exactly today's infra.Workflow spec", so this builds that spec the same
// way the production code does, not a hand-picked guess at its keys. It
// must run after a rig has chdir'd into the checkout (upgradeRigWithGitHubAppID).
func jobConfigCopyRealSpec(t *testing.T, workflow string) infra.WorkflowSpec {
	t.Helper()
	loc, ok := pluginwire.Locate(".")
	if !ok {
		t.Fatal("not a checkout")
	}
	rf, lc, _, repo, _, v := resolveCloudRepo(t.Context(), loc.Root, cloudOptions{})
	if v != nil {
		t.Fatalf("resolveCloudRepo: %+v", v)
	}
	cfg := rf.Cfg
	repoURL, err := checkoutURL(t.Context(), loc.Root)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := infra.Workflow(infra.Inputs{LC: lc, Repo: repo, Cfg: cfg, FileSetsBaseBranch: fileSetsBaseBranch(rf), RepoURL: repoURL}, workflow)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// TestUpgradeCloudSecretValueSourceEntriesAreNeverConfigCopy: review
// follow-up (PR #324, BLOCKER): a real deployed job mounts its secrets
// (CLAUDE_CODE_OAUTH_TOKEN, FUGARO_GITHUB_APP_PRIVATE_KEY, a provider's key,
// ...) as a valueSource, with no value at all (job.tf's secret_env dynamic
// block); spec.Env never has those keys, so comparing the job's whole env
// against spec.Env reported every one of them "removed", on every correctly
// deployed job, on every upgrade, forever. JobFullEnv now drops any entry
// whose ValueSource is set.
//
// Mutation (verified, run and restored): in JobFullEnv (internal/backend/gcp/run.go),
// drop the `if e.ValueSource != nil { continue }` check: "matches" finds a
// false STALE note naming the two secret variables.
func TestUpgradeCloudSecretValueSourceEntriesAreNeverConfigCopy(t *testing.T) {
	t.Run("matches", func(t *testing.T) {
		r := upgradeRigWithGitHubAppID(t)
		r.appendConfig(t, providersConfig)
		stubCurrentRefresh(t, r)
		spec := jobConfigCopyRealSpec(t, "api")
		r.run.SetJobJSON(spec.Job, jobBodyWithSecretValueSources(t, spec.Job, spec.Env))
		fakeSteps(t, "", nil)
		out, _, err := executeStdin(t, "", "upgrade", "--yes")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if strings.Contains(out, "job's copy") {
			t.Fatalf("a false STALE note on a correctly deployed job:\n%s", out)
		}
	})
	t.Run("one field really differs", func(t *testing.T) {
		r := upgradeRigWithGitHubAppID(t)
		r.appendConfig(t, providersConfig)
		stubCurrentRefresh(t, r)
		spec := jobConfigCopyRealSpec(t, "api")
		stale := maps.Clone(spec.Env)
		stale["FUGARO_GITHUB_APP_ID"] = "99999" // the local config's is "12345"
		r.run.SetJobJSON(spec.Job, jobBodyWithSecretValueSources(t, spec.Job, stale))
		fakeSteps(t, "", nil)
		out, _, err := executeStdin(t, "", "upgrade", "--yes")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if !strings.Contains(out, "FUGARO_GITHUB_APP_ID") {
			t.Fatalf("missing the changed field:\n%s", out)
		}
		for _, secret := range jobConfigCopySecretNames {
			if strings.Contains(out, secret) {
				t.Fatalf("the note named a secret variable %q:\n%s", secret, out)
			}
		}
	})
}

// upgradeRig is an onboarded checkout of acme/app (two workflows, both on
// the one base kind) whose local config records no base image yet, at
// version 0.5.1, with no claude; it returns the rig and the checkout's top
// as fugaro upgrade sees it.
func upgradeRig(t *testing.T) (*anchorModeRig, string) {
	t.Helper()
	fakeTerraform(t) // builds with go, which upgradeEnv takes off PATH
	upgradeEnv(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	loc, ok := pluginwire.Locate(".")
	if !ok {
		t.Fatal("the rig is not a checkout")
	}
	return r, loc.Root
}

func TestUpgradeInAnAgentSessionNeverTouchesTheCloud(t *testing.T) {
	r, root := upgradeRig(t)
	t.Setenv("CLAUDECODE", "1")
	noRecordReads(t)
	ran := fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"pin: done", "cloud: skipped: a coding agent's session (CLAUDECODE is set) never applies cloud changes",
		"in your own terminal window, not through the agent, run fugaro upgrade --yes " + root, "or fugaro upgrade " + root + " to be asked at each step"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(*ran) != 0 || len(r.calls(t)) != 0 || len(r.ar.Requests()) != 0 || strings.Contains(out, "fugaro image refresh of") {
		t.Fatalf("the cloud was touched: steps %q\n%s", *ran, out)
	}
}

func TestUpgradeCheckSeesTheCloudWithoutTouchingIt(t *testing.T) {
	r, root := upgradeRig(t)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "none.json"))
	noRecordReads(t)
	ran := fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--check")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "cloud: stale: base images to move: base: none recorded; this release pins it by digest through proj-1234/fugaro-ghcr") {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
	if len(*ran) != 0 || len(r.calls(t)) != 0 {
		t.Fatalf("the cloud was touched: %q", *ran)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("--check wrote the settings")
	}
}

func TestUpgradeLocalNamesTheCloudCommand(t *testing.T) {
	_, root := upgradeRig(t)
	noRecordReads(t)
	ran := fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--local")
	if err != nil || len(*ran) != 0 || !strings.Contains(out, "cloud: skipped: --local; for the cloud step, run in your own terminal window: fugaro upgrade "+root) {
		t.Fatalf("%v, steps %q\n%s", err, *ran, out)
	}
}

// TestUpgradePassesYesToTheRefresh: the cloud step runs after pin and
// plugin (skipped here: no claude on PATH), and --yes reaches the refresh
// unchanged.
func TestUpgradePassesYesToTheRefresh(t *testing.T) {
	for _, yes := range []bool{true, false} {
		t.Run(map[bool]string{true: "yes", false: "interactive"}[yes], func(t *testing.T) {
			_, root := upgradeRig(t)
			var seen *initOptions
			old := newRefreshSteps
			newRefreshSteps = func(r *initRun, e *initEngine, p *refreshPlan) refreshSteps {
				seen = r.o
				return refreshSteps{
					base:     func(context.Context) error { return nil },
					reload:   func(context.Context) (*refreshTarget, error) { return &refreshTarget{lc: p.lc, cfg: p.cfg}, nil },
					checkJob: func(context.Context, *refreshTarget) error { return nil },
					builds:   func(context.Context, *refreshTarget) error { return nil },
				}
			}
			t.Cleanup(func() { newRefreshSteps = old })
			args := []string{"upgrade"}
			if yes {
				args = append(args, "--yes")
			} else {
				fakeTerminal(t)
			}
			out, _, err := executeStdin(t, "", args...)
			if err != nil || seen == nil || seen.yes != yes {
				t.Fatalf("%v, options %+v\n%s", err, seen, out)
			}
			pin, plugin, base, cloud := strings.Index(out, "pin: done"), strings.Index(out, "plugin: skipped"), strings.Index(out, "step 2, base:"), strings.Index(out, "cloud: done")
			if pin < 0 || plugin < pin || base < plugin || cloud < base || !strings.Contains(out, "  "+root+": pin done, plugin skipped, cloud done, rules skipped, policy skipped") {
				t.Fatalf("order or summary wrong:\n%s", out)
			}
		})
	}
}

func TestUpgradeCloudFailureNamesTheUpgradeRerun(t *testing.T) {
	_, root := upgradeRig(t)
	fakeSteps(t, "check job", &ExitError{Code: ExitRemoteError, Err: errors.New("Cloud Run refused the update")})
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
	for _, want := range []string{
		"cloud: failed: Cloud Run refused the update; fugaro image refresh stopped at step 3 (check job), steps finished: preflight, base; once that is fixed, rerun fugaro upgrade --yes " + root + ": finished steps say No changes",
		"once that is fixed, rerun: fugaro upgrade --yes " + root,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "in this checkout") {
		t.Errorf("the stop names refresh's own rerun:\n%s", out)
	}
}

func TestUpgradeOneCheckoutsCloudFailureDoesNotHideAnother(t *testing.T) {
	_, root := upgradeRig(t)
	other, otherSettings := skillCheckout(t, wiredAt("v0.5.0"))
	fakeSteps(t, "base", userErr("the base copy was declined"))
	out, _, err := executeStdin(t, "", "upgrade", "--yes", root, other)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "stopped in 1 of 2 checkout(s)") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if r := pluginwire.Status(otherSettings, "0.5.1", ""); r.Pin != pluginwire.OK || !strings.Contains(out, "  "+other+": pin done, plugin skipped, cloud skipped, rules skipped, policy skipped") {
		t.Fatalf("the other checkout: %+v\n%s", r, out)
	}
}

func TestUpgradeTeammateSkipsTheCloud(t *testing.T) {
	_, _ = upgradeRig(t)
	ran := fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes", "--config", filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil || len(*ran) != 0 || !strings.Contains(out, "cloud: skipped: no local project config selects this checkout") {
		t.Fatalf("%v, steps %q\n%s", err, *ran, out)
	}
}

// TestUpgradeRerunWhenCurrentIsNothingToDo: pin and cloud both current and
// plugin skipped (no claude on PATH), so the checkout is "nothing to do": no
// settings write, and a cloud plan with no base to copy and no build.
func TestUpgradeRerunWhenCurrentIsNothingToDo(t *testing.T) {
	r, root := upgradeRig(t)
	// This release's own pin, recorded with the owner it is checked
	// against: the entry is current, so nothing is copied and nothing is
	// rebuilt (design §3.6).
	r.appendConfig(t, "image_source: ghcr.io/"+refreshOwner+"\nbase_images: {base: "+pinnedRef("base", "0.5.1")+"}\n")
	putWorkflowRecordFrom("app", "0.5.1", pinnedRef("base", "0.5.1"))
	putWorkflowRecordFrom("api", "0.5.1", pinnedRef("base", "0.5.1"))
	settings := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(wiredAt("v0.5.1")), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes")
	if err != nil || upgradeRead(t, settings) != wiredAt("v0.5.1") ||
		!strings.Contains(out, "pin: current: pinned to v0.5.1") ||
		!strings.Contains(out, "cloud: current: no base image to pin and no image to rebuild") ||
		!strings.Contains(out, "nothing to do: "+root) {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(r.calls(t)) != 0 || len(r.ar.Requests()) != 0 {
		t.Fatalf("the cloud touched terraform or the registry:\n%s", out)
	}
}

// TestUpgradeCloudCheckFailureIsAFailedStep: a real local-file error
// (cloudFailed) stops the checkout in every mode, --check included, never a
// skip (binding note from past reviews of this feature); the refresh itself
// is never attempted when cloudCheck already found the checkout broken.
func TestUpgradeCloudCheckFailureIsAFailedStep(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		exit int
	}{
		{"check", []string{"--check"}, ExitUserError},
		{"yes", []string{"--yes"}, ExitUserError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, root := upgradeRig(t)
			testutil.Git(t, root, "remote", "remove", "origin")
			ran := fakeSteps(t, "", nil)
			out, _, err := executeStdin(t, "", append([]string{"upgrade"}, tc.args...)...)
			if ExitCode(err) != tc.exit || !strings.Contains(out, "cloud: failed:") || !strings.Contains(out, "origin") ||
				strings.Contains(out, "stopped at step 1") {
				t.Fatalf("exit %d, want %d\n%s", ExitCode(err), tc.exit, out)
			}
			if len(*ran) != 0 {
				t.Fatalf("the refresh ran after cloudCheck already found a real error: %q", *ran)
			}
		})
	}
}

// TestUpgradeCloudFailedIsSkippedBeforeReadingUnderLocalAndAgent: per U7 the
// cloud step is skipped before anything is read under --local and in an
// agent's session, so a broken checkout (no origin) does not fail it: exit 0,
// skipped, the command to run named. --check and a real run still fail it
// (TestUpgradeCloudCheckFailureIsAFailedStep).
func TestUpgradeCloudFailedIsSkippedBeforeReadingUnderLocalAndAgent(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"local", "cloud: skipped: --local; for the cloud step, run in your own terminal window: fugaro upgrade "},
		{"agent", "cloud: skipped: a coding agent's session (CLAUDECODE is set) never applies cloud changes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, root := upgradeRig(t)
			testutil.Git(t, root, "remote", "remove", "origin")
			args := []string{"upgrade"}
			if tc.name == "local" {
				args = append(args, "--local")
			} else {
				t.Setenv("CLAUDECODE", "1")
			}
			ran := fakeSteps(t, "", nil)
			out, _, err := executeStdin(t, "", args...)
			if err != nil || len(*ran) != 0 || !strings.Contains(out, tc.want) || strings.Contains(out, "cloud: failed") || !strings.Contains(out, "fugaro upgrade") {
				t.Fatalf("%v, steps %q\n%s", err, *ran, out)
			}
		})
	}
}

// TestUpgradeYesRefreshesTheNamedCheckoutNotTheWorkingDirectory: run from
// another directory, fugaro upgrade --yes <root> refreshes root.
// Mutation (run, restore): in cloudStep, ro.dir = "" instead of u.loc.Root.
func TestUpgradeYesRefreshesTheNamedCheckoutNotTheWorkingDirectory(t *testing.T) {
	_, root := upgradeRig(t)
	t.Chdir(t.TempDir())
	ran := fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--yes", root)
	if err != nil || len(*ran) == 0 || !strings.Contains(out, "cloud: done") || !strings.Contains(out, "fugaro image refresh of acme/app is done") {
		t.Fatalf("%v, steps %q\n%s", err, *ran, out)
	}
}

// TestUpgradePassesTheCloudFlagsToTheRefresh: --config, --project, --region,
// --workflow, --image-source and --expect-digest reach the refresh.
// Mutation (run, restore): in cloudStep, ro := refreshOptions{} instead of
// u.o.refresh.
func TestUpgradePassesTheCloudFlagsToTheRefresh(t *testing.T) {
	r, _ := upgradeRig(t)
	var seen *initOptions
	var plan *refreshPlan
	old := newRefreshSteps
	newRefreshSteps = func(run *initRun, e *initEngine, p *refreshPlan) refreshSteps {
		seen, plan = run.o, p
		return refreshSteps{
			base:     func(context.Context) error { return nil },
			reload:   func(context.Context) (*refreshTarget, error) { return &refreshTarget{lc: p.lc, cfg: p.cfg}, nil },
			checkJob: func(context.Context, *refreshTarget) error { return nil },
			builds:   func(context.Context, *refreshTarget) error { return nil },
		}
	}
	t.Cleanup(func() { newRefreshSteps = old })
	digest := "go=sha256:" + strings.Repeat("a", 64)
	out, _, err := executeStdin(t, "", "upgrade", "--yes", "--config", r.cfg, "--project", "aurora", "--region", "us-east5",
		"--workflow", "app", "--image-source", "ghcr.io/acme", "--expect-digest", digest)
	if err != nil || seen == nil {
		t.Fatalf("%v\n%s", err, out)
	}
	c := seen.cloud
	if c.config != r.cfg || c.project != "aurora" || c.region != "us-east5" || seen.imageSource != "ghcr.io/acme" ||
		len(seen.expectDigests) != 1 || seen.expectDigests[0] != digest || len(plan.workflows) != 1 || plan.workflows[0] != "app" {
		t.Fatalf("flags not passed: cloud %+v, image source %q, digests %q, workflows %q", c, seen.imageSource, seen.expectDigests, plan.workflows)
	}
}

// TestUpgradeCheckBlockedIsAFailureWithTheRerunLine: a custom base blocks the
// refresh, so --check reports it stale (exit 1, never skipped), and the
// reason carries the command the user ran, --check included.
// Mutations (run, restore): M15 return stepSkipped for cloudBlocked in the
// --check switch; M17 pass "" as the rerun in cloudStep's cloudCheck call.
func TestUpgradeCheckBlockedIsAFailureWithTheRerunLine(t *testing.T) {
	r, root := upgradeRig(t)
	r.appendConfig(t, "base_images: {base: "+refreshHost+"/fugaro-base/fugaro-base:dev-abc}\n")
	ran := fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--check")
	if ExitCode(err) != ExitUserError || len(*ran) != 0 || !strings.Contains(out, "cloud: stale:") || strings.Contains(out, "cloud: skipped") {
		t.Fatalf("exit %d, steps %q\n%s", ExitCode(err), *ran, out)
	}
	if again := "fugaro upgrade --check " + root; !strings.Contains(out, again) {
		t.Fatalf("the reason lacks the rerun line %q:\n%s", again, out)
	}
}

// TestUpgradeCheckCurrentCloudIsCurrent: every kind on this release's base
// is "current" under --check, not stale or skipped. The entry has to be
// this release's pin — the recorded owner and the digest through the remote
// repository — and not the copy an earlier CLI made at the same version,
// which is stale (B3, TestUpgradeSaysTheBaseMustMoveForACopiedEntryAtTheSameVersion).
// Mutation (run, restore): return stepStale for cloudCurrent in the --check
// switch.
func TestUpgradeCheckCurrentCloudIsCurrent(t *testing.T) {
	r, _ := upgradeRig(t)
	r.appendConfig(t, "image_source: ghcr.io/dimipaun\nbase_images: {base: "+pinnedRef("base", "0.5.1")+"}\n")
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--check")
	if !strings.Contains(out, "cloud: current:") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestUpgradeSaysTheBaseMustMoveForACopiedEntryAtTheSameVersion is B3 on
// fugaro upgrade's offline path: an installation whose entry is the copy an
// earlier CLI made, at this very version, still needs the migration, so
// --check must not answer "no base image to pin and no image to rebuild"
// and leave it unmigrated. The reason names the shape rather than a value
// it does not have.
func TestUpgradeSaysTheBaseMustMoveForACopiedEntryAtTheSameVersion(t *testing.T) {
	r, _ := upgradeRig(t)
	r.appendConfig(t, "base_images: {base: "+managedRef("base", "0.5.1")+"}\n")
	fakeSteps(t, "", nil)
	out, _, err := executeStdin(t, "", "upgrade", "--check")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "cloud: stale:") ||
		!strings.Contains(out, "base: "+managedRef("base", "0.5.1")+"; this release pins it by digest through proj-1234/fugaro-ghcr") {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
}
