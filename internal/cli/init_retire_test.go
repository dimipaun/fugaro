package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// The retirement's own shape: a repository whose fugaro.yaml deploys the
// workflow's default variant only, while the state and the cloud still hold
// the named account's variant and an OAuth container the configuration no
// longer mounts.
const (
	retireVariant  = "app_bmb"
	retireJobName  = "fugaro-acme-sandbox-app-bmb"
	retireKeptName = "claude-oauth-token-bmb"

	retireJobAddr  = `module.repo.module.workflow["app_bmb"].google_cloud_run_v2_job.this[0]`
	retireSvcAddr  = `module.repo.module.workflow["app_bmb"].google_service_account.this`
	retireKeptAddr = `module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`
)

// retireOpts are the rig's knobs.
type retireOpts struct {
	// protection is what the state says the orphaned job's
	// deletion_protection is: the one attribute the refusal gate reads.
	protection bool
	// noJobInState is a resume: an earlier run's state removal already took
	// the job's address out, so only the labels remember it.
	noJobInState bool
	// stateEmpty is a project with nothing left in the state at all: every
	// retirement this rig knows is out of it, so only the labels can speak.
	stateEmpty bool
	// noOrphanJob leaves the cloud without the orphaned job, so the label
	// list has nothing to add either and the retirement set is empty.
	noOrphanJob bool
	// outside, when set, is an extra address the plan deletes that the
	// retirement set does not name.
	outside string
	// stateUnreadable makes the repository root's state read fail, the way
	// terraform fails when the backend holds no state it can read.
	stateUnreadable bool
	// planAbort, when set, is the stderr of a plan that fails with exit 1:
	// prevent_destroy_wrapped.txt for the §3.3 early abort, anything else
	// for a plan that simply failed after the state removal.
	planAbort string
	// deleteFails, when non-zero, is the HTTP status the Cloud Run API
	// answers a job delete with.
	deleteFails int
	// keptAsNote is the resume-after-a-failed-delete case: the kept
	// container is no longer in the state, and the label list is the only
	// thing that still knows about it.
	keptAsNote bool
	// jobNoName is a job the state holds whose own `name` attribute could
	// not be read: Protected keeps it in the removal set (the conservative
	// direction) and the API delete has no name to call.
	jobNoName bool
}

// retireRig is an onboarded repository ready to plan, with the retirement's
// state and cloud in place. The state read (`show -json` with no plan file)
// is scripted apart from the plan's, so a test can tell what the run did
// with each.
type retireRig struct {
	*initRig
	label string // the repository's label, as the spec and the cloud carry it
}

// newRetireRig builds the rig, in the checkout of acme/sandbox.
func newRetireRig(t *testing.T, o retireOpts) *retireRig {
	t.Helper()
	fakeTerminal(t)
	for _, k := range agentMarkers {
		t.Setenv(k, "") // this session may be a coding agent's
	}
	r := newInitRig(t)
	r.repoReady(t)

	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	// Both of the repository root's reads are scripted under "repo" -- the
	// root terraform runs in -- so the plain "show" key keeps answering the
	// installation root the way repoReady left it: a state, not a plan.
	r.script["show -json@repo"] = map[string]any{"stdout": retireStateDoc(t, o)}
	if o.stateUnreadable {
		r.script["show -json@repo"] = map[string]any{"exit": 1, "stderr": "Failed to load state: no such file or directory"}
	}
	plan := []planChange{
		retireChange(retireSvcAddr, "google_service_account", "delete"),
		retireChange(`module.repo.module.workflow["app"].google_cloud_run_v2_job.this[0]`, "google_cloud_run_v2_job", "update"),
		retireChange(`module.repo.google_artifact_registry_repository.images`, "google_artifact_registry_repository", "create"),
	}
	if o.outside != "" {
		plan = append(plan, retireChange(o.outside, "google_storage_bucket_iam_member", "delete"))
	}
	b, err := json.Marshal(map[string]any{"format_version": "1.2", "resource_changes": plan})
	if err != nil {
		t.Fatal(err)
	}
	r.script["show@repo"] = map[string]any{"stdout": string(b)}
	r.script["plan"] = map[string]any{"exit": 2} // the plan changes something
	if o.planAbort != "" {
		r.script["plan"] = map[string]any{"exit": 1, "stderr": o.planAbort}
	}
	r.save(t)
	if o.deleteFails != 0 {
		r.run.FailDeleteWith = o.deleteFails
	}
	if o.keptAsNote {
		// The container an earlier run released: out of the state, still in
		// Secret Manager with its labels, which is the only record left.
		r.sm.Seed(retireKeptName, map[string]string{
			gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: retireKeptName,
		}, []byte("v"))
	}

	// The cloud: the orphaned job carrying this variant's labels. The kept
	// container is deliberately NOT in it -- discovery would adopt a live
	// labelled container back into the spec (the project's own layer), so
	// the case the design names is one the cloud no longer holds: the state
	// still manages it and fugaro.yaml no longer mounts it.
	if !o.noOrphanJob {
		r.run.SetJob(retireJobName, map[string]string{
			gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: retireVariant,
		}, "us-east5-docker.pkg.dev/proj-1234/fugaro/app:1")
	}

	t.Chdir(repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "oauth", "aurora", "")))
	return &retireRig{initRig: r, label: label}
}

// retireChange is one plan entry, with the type the address really has (the
// rig's own change() guesses it from a prefix, which these addresses do not
// carry).
func retireChange(addr, typ string, actions ...string) planChange {
	return planChange{"address": addr, "type": typ,
		"change": map[string]any{"actions": actions, "before": map[string]any{}, "after": map[string]any{}}}
}

// retireStateDoc is the repository's state: the retired variant's job (with
// the deletion_protection the state carries) and service account, and the
// OAuth container no workflow mounts any more.
func retireStateDoc(t *testing.T, o retireOpts) string {
	t.Helper()
	workflow := []any{}
	if !o.stateEmpty {
		workflow = append(workflow, map[string]any{"address": retireSvcAddr, "mode": "managed", "type": "google_service_account", "name": "this",
			"values": map[string]any{"account_id": retireJobName}})
	}
	if !o.noJobInState && !o.stateEmpty {
		values := map[string]any{"name": retireJobName, "location": "us-east5", "deletion_protection": o.protection}
		if o.jobNoName {
			delete(values, "name")
		}
		workflow = append(workflow, map[string]any{"address": retireJobAddr, "mode": "managed", "type": "google_cloud_run_v2_job", "name": "this",
			"values": values})
	}
	root := []any{}
	if !o.keptAsNote {
		root = append(root, map[string]any{"address": retireKeptAddr, "mode": "managed", "type": "google_secret_manager_secret", "name": "this",
			"values": map[string]any{"secret_id": retireKeptName}})
	}
	doc := map[string]any{
		"format_version": "1.0",
		"values": map[string]any{"root_module": map[string]any{
			"resources": root,
			"child_modules": []any{map[string]any{
				"address":       "module.repo",
				"child_modules": []any{map[string]any{"address": `module.repo.module.workflow["app_bmb"]`, "resources": workflow}},
			}},
		}},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// callIndex is where the first invocation of sub appears in the recorded
// terraform calls, or -1.
func (r *retireRig) callIndex(t *testing.T, sub string) int {
	t.Helper()
	for i, c := range r.calls(t) {
		name := c[0]
		if name == "state" && len(c) > 1 {
			name += " " + c[1]
		}
		if name == sub {
			return i
		}
	}
	return -1
}

// retireClients is the run's own clients, built the way the run builds them.
func retireClients(t *testing.T, r *initRig) *infra.Clients {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := newInitClients(t.Context(), lc)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A retirement without --retire is refused, and the gate is the two cases
// the design names and no more: the job's own state says
// deletion_protection = true and its variant has left fugaro.yaml, so no
// apply can finish the delete. The summary is printed, the one command that
// can finish it is named, and nothing was planned, removed, applied or
// deleted.
func TestInitRepoRetireRefusesWithoutTheFlag(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true})
	out, errOut, err := executeStdin(t, "", "init", "--repo", "--no-build")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s%s; want the refusal", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	for _, want := range []string{
		"⚠ This change retires", retireJobAddr, retireKeptAddr, retireNextCommand,
		"no apply can delete", "1 the plan will delete, 1 deleted through the Cloud Run API after it",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not say %q (err %v):\n%s", want, err, text)
		}
	}
	if calls := r.ran(t, "state rm"); len(calls) != 0 {
		t.Errorf("state rm ran: %v", calls)
	}
	if applies := r.applies(t); len(applies) != 0 {
		t.Errorf("applied: %v", applies)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("deletes attempted: %v", deletes)
	}
	if r.callIndex(t, "plan") != -1 {
		t.Errorf("the plan ran: the gate is before it\n%s", text)
	}
}

// The gate reads values.deletion_protection, not "the set holds a job": the
// user who followed gcp-setup.md -- protection already lowered by hand, then
// --allow-delete for the service account -- reaches the ordinary path and
// applies, and the job is left to them rather than refused.
func TestInitRepoRetireGateReadsTheStateNotTheAddressList(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: false})
	out, errOut, err := executeStdin(t, "", "init", "--repo", "--no-build", "--yes", "--allow-delete", retireSvcAddr)
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	// The refusal's own words, not the summary's ("the addresses no apply
	// can delete" is in both the order of operations and the refusal).
	if strings.Contains(text, "that no apply can delete") {
		t.Errorf("a job whose protection is already false was refused:\n%s", text)
	}
	// The summary is still printed: the state-derived rows are a retirement
	// whether or not a job of it blocks the apply.
	if !strings.Contains(text, "⚠ This change retires") {
		t.Errorf("the summary is not printed for a lowered job:\n%s", text)
	}
	if got := r.applies(t); len(got) != 1 {
		t.Errorf("applies = %v, want the ordinary apply", got)
	}
	if calls := r.ran(t, "state rm"); len(calls) != 0 {
		t.Errorf("the ordinary path removed state: %v", calls)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("the ordinary path deleted a job: %v", deletes)
	}
}

// A state the ordinary run cannot read narrows what it can see instead of
// stopping it, which is the rule the label calls already follow (design
// §3.1a): the run warns, says the retirement could not be predicted, and
// reaches the ordinary plan with the exit status it always had -- the
// summary is the run's explanation, not a precondition for its work.
// --retire is the exception, and the second half pins it: it removes
// addresses from that state, so a state it cannot read stops it before
// anything is removed.
func TestInitRepoUnreadableStateWarnsAndCarriesOn(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, stateUnreadable: true})
	out, errOut, err := executeStdin(t, "", "init", "--repo", "--no-build", "--yes", "--allow-delete", retireSvcAddr)
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	if !strings.Contains(text, "could not read") || !strings.Contains(text, "a retirement cannot be predicted") {
		t.Errorf("the run did not warn that the state could not be read:\n%s", text)
	}
	if got := r.applies(t); len(got) != 1 {
		t.Errorf("applies = %v, want the ordinary apply", got)
	}
	if calls := r.ran(t, "state rm"); len(calls) != 0 {
		t.Errorf("the ordinary path removed state: %v", calls)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("the ordinary path deleted a job: %v", deletes)
	}
	// The labelled orphan is still named: the labels are the second source
	// of truth, and they need no state.
	if !strings.Contains(text, retireJobAddr) {
		t.Errorf("the labelled orphan is not named when the state cannot be read:\n%s", text)
	}

	r2 := newRetireRig(t, retireOpts{protection: true, stateUnreadable: true})
	out, errOut, err = executeStdin(t, "", "init", "--repo", "--retire", "--no-build")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v\n%s%s; want the unreadable state to stop --retire", ExitCode(err), err, out, errOut)
	}
	if calls := r2.ran(t, "state rm"); len(calls) != 0 {
		t.Errorf("--retire removed state it could not read: %v", calls)
	}
}

// The whole flow, in order: the first typed confirmation, the state removal,
// the plan, the second typed confirmation with the plan in front of the
// user, the apply, and the API delete -- with the kept container never
// deleted and the unprotected service account left to the apply.
func TestInitRepoRetireRunsTheWholeFlowInOrder(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "init", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	rm, plan, apply := r.callIndex(t, "state rm"), r.callIndex(t, "plan"), r.callIndex(t, "apply")
	if rm == -1 || plan == -1 || apply == -1 || !(rm < plan && plan < apply) {
		t.Fatalf("state rm/plan/apply at %d/%d/%d, want the state removal first\n%s", rm, plan, apply, text)
	}
	// Only the protected addresses leave the state: the kept container and
	// the job -- and the service account, which no apply may be stopped from
	// deleting, does not.
	var rmCalls []string
	for _, c := range r.ran(t, "state rm") {
		rmCalls = append(rmCalls, strings.Join(c, " "))
	}
	rmCall := strings.Join(rmCalls, "\n")
	for _, want := range []string{retireKeptAddr, retireJobAddr} {
		if !strings.Contains(rmCall, want) {
			t.Errorf("state rm %s does not name %s", rmCall, want)
		}
	}
	if strings.Contains(rmCall, retireSvcAddr) {
		t.Errorf("state rm named the unprotected service account, which the apply must delete: %s", rmCall)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 1 || deletes[0] != retireJobName {
		t.Errorf("JobDeletes = %v, want exactly %q", deletes, retireJobName)
	}
	if r.run.HasJob(retireJobName) {
		t.Error("the orphaned job is still there")
	}
	if d, x := r.sm.Deleted(), r.sm.Destroyed(); len(d) != 0 || len(x) != 0 {
		t.Errorf("the kept secret was deleted (%v) or destroyed (%v)", d, x)
	}
	if n := strings.Count(text, "⚠ CONFIRM"); n != 2 {
		t.Errorf("%d confirmations, want two typed ones:\n%s", n, text)
	}
	if !strings.Contains(text, "removed 2 addresses from Terraform's state") {
		t.Errorf("the state removal is not reported:\n%s", text)
	}
	if !strings.Contains(text, "deleted the orphaned Cloud Run job "+retireJobName) {
		t.Errorf("the API delete is not reported:\n%s", text)
	}
}

// The confirmation names every secret left in Secret Manager, by logical
// name and Secret Manager ID -- and the plan is not the only place it is
// said: both confirmations carry it.
func TestInitRepoRetireConfirmationNamesEveryKeptSecret(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "init", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	for _, want := range []string{
		retireKeptName + " (" + retireKeptName + ")",
		"Fugaro deletes no secret, so these leave Terraform's state and stay in Secret Manager if they are still there",
		"unmanaged",
	} {
		if n := strings.Count(text, want); n < 2 {
			t.Errorf("the confirmations say %q %d times, want it in both:\n%s", want, n, text)
		}
	}
	if d, x := r.sm.Deleted(), r.sm.Destroyed(); len(d) != 0 || len(x) != 0 {
		t.Errorf("the kept secret was deleted (%v) or destroyed (%v)", d, x)
	}
}

// --yes, --non-interactive, --json and a coding agent's session cover
// neither confirmation: each refuses, and nothing leaves the state and
// nothing is deleted.
func TestInitRepoRetireTypedConfirmationsAreNotCoveredByAnythingElse(t *testing.T) {
	for name, tc := range map[string]struct {
		args  []string
		agent string
	}{
		"--yes":             {args: []string{"--yes"}},
		"--non-interactive": {args: []string{"--yes", "--non-interactive"}},
		"--json":            {args: []string{"--yes", "--json"}},
		"a coding agent":    {args: []string{"--yes"}, agent: "CLAUDECODE"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRetireRig(t, retireOpts{protection: true})
			if tc.agent != "" {
				t.Setenv(tc.agent, "1")
			}
			args := append([]string{"init", "--repo", "--retire", "--no-build"}, tc.args...)
			out, errOut, err := executeStdin(t, "aurora\naurora\n", args...)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("exit %d, err %v\n%s%s; want a refusal", ExitCode(err), err, out, errOut)
			}
			if s := out + errOut; !strings.Contains(s, "⚠ CONFIRM") {
				t.Errorf("%s: the confirmation was not even shown:\n%s", name, s)
			}
			if calls := r.ran(t, "state rm"); len(calls) != 0 {
				t.Errorf("%s removed state: %v", name, calls)
			}
			if applies := r.applies(t); len(applies) != 0 {
				t.Errorf("%s applied: %v", name, applies)
			}
			if deletes := r.run.JobDeletes(); len(deletes) != 0 {
				t.Errorf("%s deleted a job: %v", name, deletes)
			}
			if !r.run.HasJob(retireJobName) {
				t.Errorf("%s left the job gone", name)
			}
		})
	}
}

// A retirement is not a way to apply an unread plan: the plan's own counts
// and its full delete list are printed before the second confirmation.
func TestInitRepoRetireShowsThePlanBeforeTheSecondConfirmation(t *testing.T) {
	newRetireRig(t, retireOpts{protection: true})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "init", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	second := strings.LastIndex(text, "⚠ CONFIRM")
	counts := strings.Index(text, "0 imports, 1 creates, 1 updates, 1 deletes")
	list := strings.Index(text, "delete "+retireSvcAddr)
	if counts == -1 || list == -1 || second == -1 {
		t.Fatalf("counts at %d, the delete list at %d, the second prompt at %d:\n%s", counts, list, second, text)
	}
	if counts > second || list > second {
		t.Errorf("the plan is shown after the second confirmation (counts %d, list %d, prompt %d)", counts, list, second)
	}
}

// --plan-only stays read-only: the summary prints before the ordinary plan,
// and nothing is removed, deleted or applied.
func TestInitRepoPlanOnlyRemovesNothing(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true})
	// --allow-delete names the service account: --plan-only runs the
	// ordinary guard, which refuses any delete the user did not name.
	out, errOut, err := executeStdin(t, "", "init", "--repo", "--no-build", "--plan-only", "--allow-delete", retireSvcAddr)
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	summary := strings.Index(text, "⚠ This change retires")
	plan := strings.Index(text, "--plan-only: nothing applied")
	if summary == -1 || plan == -1 || summary > plan {
		t.Errorf("the summary (%d) is not printed before the ordinary plan (%d):\n%s", summary, plan, text)
	}
	if calls := r.ran(t, "state rm"); len(calls) != 0 {
		t.Errorf("--plan-only removed state: %v", calls)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("--plan-only deleted a job: %v", deletes)
	}
	if applies := r.applies(t); len(applies) != 0 {
		t.Errorf("--plan-only applied: %v", applies)
	}
}

// --retire and --plan-only exclude one another: state rm changes the state,
// and --plan-only's contract is that it changes nothing.
func TestInitRepoRetireAndPlanOnlyExcludeOneAnother(t *testing.T) {
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	_, _, err := executeStdin(t, "", "init", "--repo", "--retire", "--plan-only")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "exclude one another") {
		t.Fatalf("exit %d, err %v; want the flags refused", ExitCode(err), err)
	}
}

// The guard still runs inside the retirement: a plan that deletes an address
// the retirement set does not name is refused, even with the set's own
// deletes pre-allowed.
func TestInitRepoRetireRefusesADeleteOutsideTheSet(t *testing.T) {
	outside := "module.repo.google_storage_bucket_iam_member.job"
	r := newRetireRig(t, retireOpts{protection: true, outside: outside})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "init", "--repo", "--retire", "--no-build")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), outside) {
		t.Fatalf("exit %d, err %v\n%s%s; want the guard to refuse %s", ExitCode(err), err, out, errOut, outside)
	}
	if applies := r.applies(t); len(applies) != 0 {
		t.Errorf("the refused plan was applied: %v", applies)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("a job was deleted under a refused plan: %v", deletes)
	}
	if !r.run.HasJob(retireJobName) {
		t.Error("the job was deleted under a refused plan")
	}
}

// The resume: an earlier run's state removal already took the job's address
// out, so only the labels remember it. The summary names it from them (the
// state cannot), --retire still deletes it through the API after the apply,
// and the state removal passes nothing for it.
func TestInitRepoRetireResumeFindsTheOrphanByLabel(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, noJobInState: true})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "init", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	if !strings.Contains(text, retireJobAddr) {
		t.Errorf("the label-read orphan is not in the summary:\n%s", text)
	}
	if rm := r.ran(t, "state rm"); len(rm) == 0 || strings.Contains(strings.Join(rm[0], " "), retireJobAddr) {
		t.Errorf("state rm = %v, want the job's address left out of it (it is not in the state)", rm)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 1 || deletes[0] != retireJobName {
		t.Errorf("JobDeletes = %v, want the label-read orphan deleted", deletes)
	}
	if r.run.HasJob(retireJobName) {
		t.Error("the orphan the labels named is still there")
	}
}

// The gate's own rule, on its own: a job is blocking only when the state
// both holds it and says its protection is on.
func TestBlockingJobsReadsTheStatesOwnProtection(t *testing.T) {
	rets := []infra.Retirement{
		{Address: retireJobAddr, Kind: "workflow job", Protected: true},
		{Address: retireSvcAddr, Kind: "service account", Protected: true},
	}
	if got := blockingJobs([]string{retireJobAddr, retireSvcAddr}, rets); len(got) != 1 || got[0].Address != retireJobAddr {
		t.Errorf("blockingJobs = %v, want only the protected job", got)
	}
	if got := blockingJobs([]string{retireSvcAddr}, rets); len(got) != 0 {
		t.Errorf("blockingJobs = %v, want nothing: the state does not hold the job", got)
	}
	open := []infra.Retirement{{Address: retireJobAddr, Kind: "workflow job", Protected: false}}
	if got := blockingJobs([]string{retireJobAddr}, open); len(got) != 0 {
		t.Errorf("blockingJobs = %v, want nothing for a job the state says is not protected", got)
	}
}

// The set splits by what actually happens to each row, and nothing deletes
// a kept row (I3).
func TestRetireSplitIsWhatHappensToEachRow(t *testing.T) {
	const goneByLabel = `module.repo.module.workflow["gone"].google_cloud_run_v2_job.this[0]`
	const goneByLabel2 = `module.repo.module.workflow["gone2"].google_cloud_run_v2_job.this[0]`
	spec := infra.RepoSpec{Name: "acme-sandbox", GCPProject: initProject, Region: "us-east5"}
	inState := []string{retireJobAddr, retireSvcAddr, retireKeptAddr, goneByLabel}
	rets := []infra.Retirement{
		// The state holds it, its protection is off: the plan's own delete
		// finishes it, and calling the API on it would delete twice.
		{Address: retireJobAddr, Kind: "workflow job", Job: retireJobName},
		{Address: retireSvcAddr, Kind: "service account"},
		{Address: retireKeptAddr, Kind: "secret container", Logical: retireKeptName, Kept: infra.KeptSecret},
		// The state still holds it and says its protection is on: no apply
		// can finish it.
		{Address: goneByLabel, Kind: "workflow job", Job: "fugaro-acme-sandbox-gone", Protected: true},
		// The labels found it and the state never had it.
		{Address: goneByLabel2, Kind: "workflow job", Job: "fugaro-acme-sandbox-gone2"},
	}
	api, apply, kept := infra.RetireSplit(inState, spec, rets)
	got := func(rs []infra.Retirement) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Address)
		}
		return out
	}
	if want := []string{goneByLabel, goneByLabel2}; !slices.Equal(got(api), want) {
		t.Errorf("api = %v, want %v", got(api), want)
	}
	if want := []string{retireJobAddr, retireSvcAddr}; !slices.Equal(got(apply), want) {
		t.Errorf("apply = %v, want %v", got(apply), want)
	}
	if want := []string{retireKeptAddr}; !slices.Equal(got(kept), want) {
		t.Errorf("kept = %v, want %v", got(kept), want)
	}
}

// The classification reads the configuration, not the shape of an address:
// while the variant is still in fugaro.yaml no row of it is called a
// workflow job, because that kind is what the API delete keys on and the
// job of a deployed workflow must never be a candidate for it (M3).
func TestRetirementNeedsTheVariantToHaveLeftTheConfig(t *testing.T) {
	spec := infra.RepoSpec{Workflows: map[string]infra.WorkflowSpec{retireVariant: {}}}
	live := infra.Retirements([]string{retireJobAddr}, spec)
	if len(live) != 1 {
		t.Fatalf("rets = %+v, want the one address", live)
	}
	if live[0].Kind == "workflow job" {
		t.Errorf("%s was called a workflow job while %s is still in the configuration", live[0].Address, retireVariant)
	}
	if _, ok := infra.JobDeleteLabels(spec, live[0]); ok {
		t.Error("JobDeleteLabels accepted a job of a variant that is still deployed")
	}
	if !strings.Contains(live[0].Cause, retireVariant) {
		t.Errorf("cause = %q, want the variant it belongs to named", live[0].Cause)
	}
	// With the variant gone it is one, and the cause says which config edit
	// took it away.
	gone := infra.Retirements([]string{retireJobAddr}, infra.RepoSpec{})
	if len(gone) != 1 || gone[0].Kind != "workflow job" {
		t.Fatalf("rets = %+v, want one workflow job", gone)
	}
	if _, ok := infra.JobDeleteLabels(infra.RepoSpec{}, gone[0]); !ok {
		t.Error("JobDeleteLabels refused the job of a variant that is gone")
	}
	if !strings.Contains(gone[0].Cause, "fugaro.yaml") {
		t.Errorf("cause = %q, want the configuration change named", gone[0].Cause)
	}
}

// A plan's own deletes are the retirement's allow-list, and --allow-delete
// can only narrow it: an address the user did not name is dropped, and one
// they named that the plan does not delete is never added.
func TestRetireAllowDeleteNarrowsToTheFlag(t *testing.T) {
	const a, b, c = "module.repo.a", "module.repo.b", "module.repo.c"
	plan := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		{Address: a, Change: tf.Change{Actions: []string{"delete"}}},
		{Address: b, Change: tf.Change{Actions: []string{"update"}}},
		{Address: c, Change: tf.Change{Actions: []string{"delete"}}},
	}}
	rets := []infra.Retirement{{Address: a}, {Address: b}}
	for name, tc := range map[string]struct {
		flag []string
		want []string
	}{
		"no flag":       {nil, []string{a}},
		"the same one":  {[]string{a}, []string{a}},
		"a wider one":   {[]string{a, c}, []string{a}},
		"another one":   {[]string{c}, nil},
		"an update":     {[]string{b}, nil},
		"nothing named": {[]string{}, []string{a}},
	} {
		got := retireAllowDelete(plan, rets, tc.flag)
		if len(got) != len(tc.want) || (len(got) == 1 && got[0] != tc.want[0]) {
			t.Errorf("%s: retireAllowDelete = %v, want %v", name, got, tc.want)
		}
	}
}

// A plan Terraform refused on lifecycle.prevent_destroy is the §3.3
// signature, matched however Terraform wrapped the line.
func TestPreventDestroyAbortMatchesWrappedTerraformText(t *testing.T) {
	wrapped := "Error: Instance cannot be destroyed\n\n  on secrets.tf line 1:\n\nResource " +
		retireKeptAddr + " has\nlifecycle.prevent_destroy set, but the plan calls for this resource to be\ndestroyed. To avoid this error and continue with the plan, either disable\nlifecycle.prevent_destroy or reduce the scope of the plan using the -target\nflag.\n"
	if !preventDestroyAbort(&tf.ExitError{Command: "plan", Stderr: wrapped}) {
		t.Error("the wrapped prevent_destroy message is not recognized")
	}
	if preventDestroyAbort(&tf.ExitError{Command: "plan", Stderr: "Error: something else"}) {
		t.Error("another failure was read as prevent_destroy")
	}
	if preventDestroyAbort(errors.New("plain")) {
		t.Error("a non-terraform error was read as prevent_destroy")
	}
	// M5: --retire is named only for an address it would actually remove.
	// For anything else the same plan would abort in the same place, so the
	// refusal names the configuration and the block instead of advice that
	// cannot work.
	notRead := retireAborted(nil, retireKeptAddr)
	if strings.Contains(notRead.Error(), retireNextCommand) {
		t.Errorf("the abort points at --retire for an address it would not remove: %v", notRead)
	}
	if !strings.Contains(notRead.Error(), retireKeptAddr) || !strings.Contains(notRead.Error(), "prevent_destroy") {
		t.Errorf("the abort does not name the address and the block: %v", notRead)
	}
	read := retireAborted([]infra.Retirement{{Address: retireKeptAddr, Kept: infra.KeptSecret}}, retireKeptAddr)
	if !strings.Contains(read.Error(), retireNextCommand) {
		t.Errorf("the abort does not name the next command for an address --retire removes: %v", read)
	}
	// The address Terraform named is pulled out of its own wrapped text.
	if got := preventDestroyAddress(&tf.ExitError{Command: "plan", Stderr: wrapped}); got != retireKeptAddr {
		t.Errorf("preventDestroyAddress = %q, want %q", got, retireKeptAddr)
	}
}

// deleteOrphanedJob's own contract, case by case (Review Focus 7): the
// labels first, 404 as done, the operation waited for, and the outcomes told
// apart.
func TestDeleteOrphanedJobRefusesAnotherRepositoriesOrVariantsJob(t *testing.T) {
	r := newInitRig(t)
	c := retireClients(t, r)
	spec := infra.RepoSpec{Label: "acme-sandbox", GCPProject: initProject, Region: "us-east5"}
	want, ok := infra.JobDeleteLabels(spec, infra.Retirement{Address: retireJobAddr, Kind: "workflow job"})
	if !ok {
		t.Fatal("no label set for a workflow job")
	}

	r.run.SetJob(retireJobName, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: "another-repo", gcp.LabelWorkflow: retireVariant,
	}, "")
	err := deleteOrphanedJob(t.Context(), c, initProject, "us-east5", retireJobName, want)
	if err == nil {
		t.Fatal("another repository's job was deleted")
	}
	if !strings.Contains(err.Error(), gcp.LabelRepo) {
		t.Errorf("the refusal does not name the label that did not match: %v", err)
	}

	// The repository's label, another variant: the case that would delete a
	// surviving workflow's job.
	r.run.SetJob(retireJobName, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: spec.Label, gcp.LabelWorkflow: "app",
	}, "")
	err = deleteOrphanedJob(t.Context(), c, initProject, "us-east5", retireJobName, want)
	if err == nil {
		t.Fatal("another variant's job was deleted")
	}
	if !strings.Contains(err.Error(), gcp.LabelWorkflow) {
		t.Errorf("the refusal does not name %s: %v", gcp.LabelWorkflow, err)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("a refused job was deleted: %v", deletes)
	}
	if !r.run.HasJob(retireJobName) {
		t.Error("a refused job is gone")
	}
}

// The check job is deleted under its own label (fugaro_role=check, no
// fugaro_workflow): demanding a workflow label there would leave a retired
// check job alive forever.
func TestDeleteOrphanedJobDeletesTheCheckJobUnderTheRoleLabel(t *testing.T) {
	r := newInitRig(t)
	c := retireClients(t, r)
	spec := infra.RepoSpec{Label: "acme-sandbox", GCPProject: initProject, Region: "us-east5"}
	want, ok := infra.JobDeleteLabels(spec, infra.Retirement{Address: `module.repo.google_cloud_run_v2_job.check[0]`, Kind: "check job"})
	if !ok || want[gcp.LabelRole] != gcp.RoleCheck {
		t.Fatalf("want = %v, ok %v; want the check job's own labels", want, ok)
	}
	if _, has := want[gcp.LabelWorkflow]; has {
		t.Errorf("the check job's label set demands %s: %v", gcp.LabelWorkflow, want)
	}
	check := "fugaro-acme-sandbox-check"
	r.run.SetJob(check, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: spec.Label, gcp.LabelRole: gcp.RoleCheck,
	}, "")
	if err := deleteOrphanedJob(t.Context(), c, initProject, "us-east5", check, want); err != nil {
		t.Fatalf("the check job was not deleted: %v", err)
	}
	if r.run.HasJob(check) {
		t.Error("the check job is still there")
	}
}

// A job that is already gone is done, not a failure: the resumed run and the
// hand-run gcloud delete both land here.
func TestDeleteOrphanedJobTreatsNotFoundAsDone(t *testing.T) {
	r := newInitRig(t)
	c := retireClients(t, r)
	spec := infra.RepoSpec{Label: "acme-sandbox", GCPProject: initProject, Region: "us-east5"}
	want, _ := infra.JobDeleteLabels(spec, infra.Retirement{Address: retireJobAddr, Kind: "workflow job"})
	if err := deleteOrphanedJob(t.Context(), c, initProject, "us-east5", retireJobName, want); err != nil {
		t.Fatalf("a job that is not there: %v, want done", err)
	}
}

// A PERMISSION_DENIED is a named outcome with a way out: the installation's
// launcher role holds no run.jobs.delete, so this call runs as the operator
// and the gcloud command that finishes the work is printed.
func TestDeleteOrphanedJobReportsAPermissionDenied(t *testing.T) {
	r := newInitRig(t)
	c := retireClients(t, r)
	spec := infra.RepoSpec{Label: "acme-sandbox", GCPProject: initProject, Region: "us-east5"}
	want, _ := infra.JobDeleteLabels(spec, infra.Retirement{Address: retireJobAddr, Kind: "workflow job"})
	r.run.SetJob(retireJobName, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: spec.Label, gcp.LabelWorkflow: retireVariant,
	}, "")
	r.run.FailDeleteWith = 403
	err := deleteOrphanedJob(t.Context(), c, initProject, "us-east5", retireJobName, want)
	if err == nil {
		t.Fatal("a refused delete was reported as done")
	}
	for _, w := range []string{"permission denied", "run.jobs.delete", gcloudJobDelete(initProject, "us-east5", retireJobName)} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the refusal does not say %q: %v", w, err)
		}
	}
}

// The delete waits for its long-running operation rather than reporting
// success when it is created, and a wait that runs out is a third outcome:
// neither success nor failure, with the job and the operation named.
func TestDeleteOrphanedJobWaitsForItsOperationAndReportsATimeout(t *testing.T) {
	oldPolls, oldPoll := retireDeletePolls, retireDeletePoll
	retireDeletePolls, retireDeletePoll = 5, time.Millisecond
	t.Cleanup(func() { retireDeletePolls, retireDeletePoll = oldPolls, oldPoll })

	r := newInitRig(t)
	c := retireClients(t, r)
	spec := infra.RepoSpec{Label: "acme-sandbox", GCPProject: initProject, Region: "us-east5"}
	want, _ := infra.JobDeleteLabels(spec, infra.Retirement{Address: retireJobAddr, Kind: "workflow job"})
	labels := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: spec.Label, gcp.LabelWorkflow: retireVariant}

	r.run.SetJob(retireJobName, labels, "")
	r.run.SetJobFaults(gcpfake.JobFaults{DeletePolls: 1})
	if err := deleteOrphanedJob(t.Context(), c, initProject, "us-east5", retireJobName, want); err != nil {
		t.Fatalf("the delete did not wait for its operation: %v", err)
	}
	if r.run.HasJob(retireJobName) {
		t.Error("the job is still there after its operation finished")
	}

	r.run.SetJob(retireJobName, labels, "")
	r.run.SetJobFaults(gcpfake.JobFaults{DeletePolls: 1000})
	err := deleteOrphanedJob(t.Context(), c, initProject, "us-east5", retireJobName, want)
	var still *stillDeletingError
	if !errors.As(err, &still) {
		t.Fatalf("err = %v, want the still-deleting outcome", err)
	}
	if !strings.Contains(err.Error(), retireJobName) || !strings.Contains(err.Error(), "still deleting") {
		t.Errorf("the still-deleting outcome does not name the job and the wait: %v", err)
	}
	if still.Operation == "" {
		t.Error("the still-deleting outcome does not name the operation")
	}
	if !r.run.HasJob(retireJobName) {
		t.Error("a job whose operation is not done was reported gone")
	}
}

// --- Task 6: the early abort, the transcript, and the half-failure ---

// fixture is one of the run's testdata files: the text Terraform really
// printed, kept as a fixture rather than a string a test invented, so the
// matcher is pinned against the wrap Terraform actually produces.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The §3.3 signature is matched whitespace-normalized, and the real text
// proves it must be: Terraform wraps after "has", so a literal substring
// match on the signature never fires. Both halves are asserted here, so the
// normalization cannot be dropped without a test going red.
func TestPreventDestroySignatureIsWhitespaceNormalized(t *testing.T) {
	wrapped := fixture(t, "prevent_destroy_wrapped.txt")
	const needle = "has lifecycle.prevent_destroy set, but the plan calls for this resource to be destroyed"
	if strings.Contains(wrapped, needle) {
		t.Fatalf("the fixture is not wrapped, so it pins nothing:\n%s", wrapped)
	}
	if !strings.Contains(collapseSpace(wrapped), needle) {
		t.Errorf("the normalized match does not fire on the real text:\n%s", collapseSpace(wrapped))
	}
	if !preventDestroyAbort(&tf.ExitError{Command: "plan", Code: 1, Stderr: wrapped}) {
		t.Error("preventDestroyAbort does not fire on the wrapped text")
	}
	// And it stays narrow: another plan failure is not a prevent_destroy abort.
	for _, err := range []error{
		&tf.ExitError{Command: "plan", Code: 1, Stderr: "Error: Invalid provider configuration\n"},
		&tf.ExitError{Command: "state rm", Code: 1, Stderr: wrapped},
		errors.New("terraform plan: " + wrapped),
	} {
		if preventDestroyAbort(err) {
			t.Errorf("preventDestroyAbort fired on %v", err)
		}
	}
}

// A plan that dies on prevent_destroy is re-framed: the summary was printed
// first, and the run's own words are the next command, not Terraform's text
// (Review Focus 5). The fixture is the owner's own case -- a wrapped refusal
// naming a repo secret container the configuration stopped producing -- so
// the address Terraform names is one this change really retires, which is
// what makes --retire the right advice. Terraform's stderr is streamed by the
// terraform plumbing either way -- that is what "nothing is hidden, only
// re-framed" means -- so what is asserted about the raw text is the run's own
// message, and the stdout the summary owns.
func TestInitRepoPreventDestroyReframed(t *testing.T) {
	r := newRetireRig(t, retireOpts{planAbort: fixture(t, "prevent_destroy_wrapped.txt")})
	out, errOut, err := executeStdin(t, "", "init", "--repo", "--no-build", "--yes")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	if !strings.Contains(out, "⚠ This change retires") || !strings.Contains(out, retireJobAddr) {
		t.Errorf("the summary was not printed:\n%s", out)
	}
	if strings.Contains(out, "lifecycle.prevent_destroy") {
		t.Errorf("Terraform's text reached stdout:\n%s", out)
	}
	if err == nil || !strings.Contains(err.Error(), "the plan was refused: "+retireKeptAddr) || !strings.Contains(err.Error(), "this change retires it") {
		t.Errorf("the failure was not re-framed as a retirement naming the address Terraform named: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), retireNextCommand) {
		t.Errorf("the re-framing does not name the next command: %v", err)
	}
	if calls := r.ran(t, "state rm"); len(calls) != 0 {
		t.Errorf("an ordinary run removed state: %v", calls)
	}
}

// A retirement that stops after the state removal may not stop quietly: the
// addresses that left the state are alive in GCP with nothing managing them,
// and the run says which ones, with the job delete named as the step still to
// come (Review Focus 6, design §6).
func TestInitRepoRetireHalfFailureNamesWhatWasRemoved(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, planAbort: "Error: Invalid provider configuration\n"})
	out, errOut, err := executeStdin(t, "aurora\n", "init", "--repo", "--retire", "--no-build")
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	for _, want := range []string{
		"removed 2 addresses from Terraform's state",
		retireKeptAddr, retireJobAddr,
		"the run stopped after the state removal",
		"nothing managing them",
		"the Cloud Run job " + retireJobName,
		retireNextCommand,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the half-failure does not say %q:\n%s", want, text)
		}
	}
	if applies := r.applies(t); len(applies) != 0 {
		t.Errorf("applied after a failed plan: %v", applies)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("deleted a job after a failed plan: %v", deletes)
	}
}

// The other half: the apply succeeded and the API delete did not. The run
// says what is left, names the command that finishes it, and exits non-zero,
// with the state already consistent.
func TestInitRepoRetireReportsAnApplyThatSucceededAndADeleteThatFailed(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, deleteFails: 500})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "init", "--repo", "--retire", "--no-build")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	if got := r.applies(t); len(got) != 1 {
		t.Errorf("applies = %v, want the apply before the delete", got)
	}
	for _, want := range []string{
		"apply ok, job delete failed", retireJobName,
		"gcloud run jobs delete " + retireJobName, retireNextCommand,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the failed delete does not say %q:\n%s", want, text)
		}
	}
}

// A protected, state-held job whose values carry no readable `name` is the
// one retirement the run cannot finish: the state removal takes the address
// out, no apply can delete the resource, and the API call needs a name it
// does not have -- guessing one is forbidden (design §3.1). The run refuses
// before anything leaves the state, names the address and the gcloud command
// that answers the question by hand, and exits non-zero: a state removal that
// left a job alive in GCP with nothing managing it, and said nothing, is the
// outcome this refuses (I3, Review Focus 6).
func TestInitRepoRetireRefusesAJobTheStateCannotName(t *testing.T) {
	// noOrphanJob too: when the label list answers, it names the job and the
	// delete goes ahead -- the empty-name path is what is left when the state
	// cannot name it and the labels cannot either (a refused or degraded
	// label call), and it is the one that must not pass silently.
	r := newRetireRig(t, retireOpts{protection: true, jobNoName: true, noOrphanJob: true})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "init", "--repo", "--retire", "--no-build")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	// The refusal is a returned error (the root command silences cobra's own
	// printing and main prints it), so its words are asserted on the error.
	msg := err.Error()
	for _, want := range []string{
		retireJobAddr, "nothing was removed from Terraform's state and nothing was applied",
		"the state's own values carry no name for it", "gcloud run jobs list",
		retireNextCommand,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q in %q\n%s", want, msg, text)
		}
	}
	if strings.Contains(text, "removed ") && strings.Contains(text, " from Terraform's state") {
		t.Errorf("a state removal was reported:\n%s", text)
	}
	// Nothing left the state: that is the whole point of refusing here.
	if got := r.ran(t, "state rm"); len(got) != 0 {
		t.Errorf("state rm = %v, want none: the job cannot be finished once it leaves the state", got)
	}
	if got := r.applies(t); len(got) != 0 {
		t.Errorf("applies = %v, want none", got)
	}
	if got := r.run.JobDeletes(); len(got) != 0 {
		t.Errorf("JobDeletes = %v, want none: no name to call the API with", got)
	}
}

// repeat.
func TestInitRepoRetireResumeDoesNotReremove(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, noJobInState: true})
	_, errOut, err := executeStdin(t, "aurora\naurora\n", "init", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, errOut)
	}
	var calls []string
	for _, c := range r.ran(t, "state rm") {
		calls = append(calls, strings.Join(c, " "))
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, retireKeptAddr) {
		t.Errorf("the container the state still holds was not removed: %s", joined)
	}
	if strings.Contains(joined, retireJobAddr) {
		t.Errorf("an address the state no longer holds was removed again: %s", joined)
	}
	// The job is still deleted: the labels are the record, not the state.
	if deletes := r.run.JobDeletes(); len(deletes) != 1 || deletes[0] != retireJobName {
		t.Errorf("JobDeletes = %v, want the orphan found by its labels", deletes)
	}
}

// The same rerun as the previous test, one step further on: the container an
// earlier retirement released is no longer in the state at all, and it is
// live with this repository's labels. With #329 on main that is the case the
// union takes: Readiness puts the container back into the spec, so the plan
// adopts it (import, kept in the module's for_each, mounted or not) and the
// run never reports it as a leftover -- no note, no KEPT row, no state
// removal, nothing deleted (I4).
func TestInitRepoRetireResumeAdoptsTheLiveLabelledContainer(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, keptAsNote: true})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "init", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	for _, bad := range []string{"no longer mounted by fugaro.yaml", "KEPT", retireKeptAddr} {
		if strings.Contains(text, bad) {
			t.Errorf("the live labelled container is reported as a leftover (%q):\n%s", bad, text)
		}
	}
	for _, c := range r.ran(t, "state rm") {
		if strings.Contains(strings.Join(c, " "), retireKeptAddr) {
			t.Errorf("the adopted container was removed from the state: %v", c)
		}
	}
	if d, x := r.sm.Deleted(), r.sm.Destroyed(); len(d) != 0 || len(x) != 0 {
		t.Errorf("the adopted secret was deleted (%v) or destroyed (%v)", d, x)
	}
}

// --json carries the same retirement the run printed: the summary's own rows,
// with the state's and the labels' fields filled in (design §3.2).
func TestInitRepoJSONCarriesRetirements(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true})
	out, errOut, err := executeStdin(t, "", "init", "--repo", "--no-build", "--yes", "--json")
	if ExitCode(err) != ExitUserError || err == nil {
		// --yes cannot run a retirement, so the documented outcome is the
		// refusal -- and the result document is printed either way.
		t.Logf("exit %d, err %v\n%s", ExitCode(err), err, errOut)
	}
	var res struct {
		Retire []infra.Retirement `json:"retire"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	byAddr := map[string]infra.Retirement{}
	for _, ret := range res.Retire {
		byAddr[ret.Address] = ret
	}
	job, ok := byAddr[retireJobAddr]
	if !ok {
		t.Fatalf("the JSON has no retirement for the job: %+v", res.Retire)
	}
	if job.Job != retireJobName || job.Region == "" || !job.Protected || job.Kind != "workflow job" {
		t.Errorf("the job's row = %+v, want its name, region, kind and protection", job)
	}
	kept, ok := byAddr[retireKeptAddr]
	if !ok {
		t.Fatalf("the JSON has no retirement for the kept container: %+v", res.Retire)
	}
	if kept.Logical != retireKeptName || kept.SecretID != retireKeptName || kept.Kept == "" {
		t.Errorf("the container's row = %+v, want its logical name, Secret Manager ID and Kept text", kept)
	}
	if n := len(r.ran(t, "state rm")); n != 0 {
		t.Errorf("--json removed state: %d call(s)", n)
	}
}

// The secret-safety assertion (Review Focus 2): across a whole retirement,
// nothing ever deletes or destroys a secret or a secret version, and no
// terraform invocation is a destroy. The state removal is what keeps the
// container's value: it leaves Secret Manager untouched.
func TestInitRepoRetireNeverTouchesSecretManager(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true})
	// The container the state holds is live in Secret Manager with a value:
	// the retirement takes its address out of the state, and that value has
	// to survive.
	r.sm.Seed(retireKeptName, nil, []byte("v"))
	if _, errOut, err := executeStdin(t, "aurora\naurora\n", "init", "--repo", "--retire", "--no-build"); err != nil {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, errOut)
	}
	if got := r.sm.Deleted(); len(got) != 0 {
		t.Errorf("a secret was deleted: %v", got)
	}
	if got := r.sm.Destroyed(); len(got) != 0 {
		t.Errorf("a secret version was destroyed: %v", got)
	}
	for _, c := range r.calls(t) {
		for _, arg := range c {
			if arg == "destroy" || strings.Contains(arg, "-destroy") {
				t.Errorf("terraform was invoked with %q: %v", arg, c)
			}
		}
	}
	if got := r.sm.Latest(retireKeptName); len(got) == 0 {
		t.Errorf("the retired container's value is gone from Secret Manager")
	}
}

// --- The transcript, as a golden ---

// updateRetireGolden rewrites testdata/init_retire.golden. It is a flag of
// its own: the package's other goldens already own -update and
// -update-review, and a second flag.Bool of the same name panics.
var updateRetireGolden = flag.Bool("update-retire", false, "rewrite testdata/init_retire.golden")

// retireTranscriptRE are the run's few non-deterministic pieces: the test
// binary's own path, the checkout's temp dir, the fake servers' ports and
// the backup's timestamp. Everything else -- the addresses, the counts, the
// confirmations, the commands -- must be exactly what the golden holds.
var retireTranscriptRE = []struct {
	re *regexp.Regexp
	to string
}{
	{regexp.MustCompile(`/tmp/go-build[0-9]+/b[0-9]+/cli\.test`), "fugaro"},
	{regexp.MustCompile(regexp.QuoteMeta(os.TempDir()) + `/[^ "'\n)\]]*`), "<tmp>"},
	{regexp.MustCompile(`127\.0\.0\.1:[0-9]+`), "127.0.0.1:<port>"},
	{regexp.MustCompile(`[0-9]{8}T[0-9]{6}Z`), "<timestamp>"},
}

// normaliseRetireTranscript folds those pieces to placeholders, so the
// golden is the run's own words and nothing else.
func normaliseRetireTranscript(s string) string {
	for _, r := range retireTranscriptRE {
		s = r.re.ReplaceAllString(s, r.to)
	}
	return s
}

// The whole retirement as one transcript: the summary, both typed
// confirmations, the state removal, the plan's own counts, the apply and the
// API delete, with no Terraform text anywhere in it (Review Focus 5, design
// §3.2). The fixture is the origin's own shape -- acme/sandbox losing the
// named account bmb -- and every identifier in it is the fake's.
func TestInitRepoRetireTranscriptGolden(t *testing.T) {
	// The rig chdirs into the checkout, so the golden's path is taken first.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	newRetireRig(t, retireOpts{protection: true})
	out, errOut, runErr := executeStdin(t, "aurora\naurora\n", "init", "--repo", "--retire", "--no-build")
	if runErr != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(runErr), runErr, out, errOut)
	}
	got := normaliseRetireTranscript(out + errOut)
	path := filepath.Join(wd, "testdata", "init_retire.golden")
	if *updateRetireGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Fatal("no golden file: go test ./internal/cli -run TestInitRepoRetireTranscriptGolden -update-retire")
		}
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("the retirement transcript changed (go test -update-retire to accept):\n%s\nwant:\n%s", got, want)
	}
	for _, bad := range []string{"lifecycle.prevent_destroy", "/tmp/go-build", os.TempDir()} {
		if strings.Contains(got, bad) {
			t.Errorf("the golden carries %q", bad)
		}
	}
}

// The convergence rule as #329 leaves it (design §3.1a, review D8, I4): a
// container an earlier retirement released, still live and still carrying
// this repository's labels, and a configuration that re-mounts nothing of it.
// Readiness unions it back into the spec, so the run reports no retirement,
// removes nothing from the state, deletes no secret and asks for no --retire
// advice; the run says the same thing twice, so a project with nothing left
// to retire converges instead of re-reporting forever.
//
// What the plan does with the adopted container (the import that keeps it in
// the module's for_each) is the plan's own business, printed with the plan's
// counts; the union itself is pinned by
// infra's TestRetirementsProviderDroppedIsTheUnionCase and
// TestOrphansSkipsWhatTheUnionAdopted.
func TestInitRepoRetireConvergesOnAnAdoptedContainer(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: false, stateEmpty: true, noOrphanJob: true, keptAsNote: true})
	for i := 1; i <= 2; i++ {
		// --allow-delete names the one address this fixture's plan deletes (the
		// retired service account); the container is nobody's to delete, which
		// is what the assertions below are about.
		out, errOut, err := executeStdin(t, "", "init", "--repo", "--no-build", "--yes", "--allow-delete", retireSvcAddr)
		if err != nil {
			t.Fatalf("run %d: exit %d, err %v\n%s%s", i, ExitCode(err), err, out, errOut)
		}
		text := out + errOut
		for _, bad := range []string{"no longer mounted by fugaro.yaml", "⚠ This change retires", retireNextCommand} {
			if strings.Contains(text, bad) {
				t.Errorf("run %d: the adopted container is reported as a leftover (%q):\n%s", i, bad, text)
			}
		}
		if calls := r.ran(t, "state rm"); len(calls) != 0 {
			t.Errorf("run %d: state rm ran: %v", i, calls)
		}
	}
	if got := r.sm.Deleted(); len(got) != 0 {
		t.Errorf("a secret was deleted: %v", got)
	}
}
