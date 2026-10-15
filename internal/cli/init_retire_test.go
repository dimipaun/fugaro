package cli

import (
	"encoding/json"
	"errors"
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
	// outside, when set, is an extra address the plan deletes that the
	// retirement set does not name.
	outside string
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
	r.save(t)

	// The cloud: the orphaned job carrying this variant's labels. The kept
	// container is deliberately NOT in it -- discovery would adopt a live
	// labelled container back into the spec (the project's own layer), so
	// the case the design names is one the cloud no longer holds: the state
	// still manages it and fugaro.yaml no longer mounts it.
	r.run.SetJob(retireJobName, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelWorkflow: retireVariant,
	}, "us-east5-docker.pkg.dev/proj-1234/fugaro/app:1")

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
	workflow := []any{
		map[string]any{"address": retireSvcAddr, "mode": "managed", "type": "google_service_account", "name": "this",
			"values": map[string]any{"account_id": retireJobName}},
	}
	if !o.noJobInState {
		workflow = append(workflow, map[string]any{"address": retireJobAddr, "mode": "managed", "type": "google_cloud_run_v2_job", "name": "this",
			"values": map[string]any{"name": retireJobName, "location": "us-east5", "deletion_protection": o.protection}})
	}
	doc := map[string]any{
		"format_version": "1.0",
		"values": map[string]any{"root_module": map[string]any{
			"resources": []any{
				map[string]any{"address": retireKeptAddr, "mode": "managed", "type": "google_secret_manager_secret", "name": "this",
					"values": map[string]any{"secret_id": retireKeptName}},
			},
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
		"These secrets stay in Secret Manager with their values",
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

// The set splits by how each row goes, and nothing deletes a kept row.
func TestRetireGoingAwaySplitsTheSet(t *testing.T) {
	rets := []infra.Retirement{
		{Address: retireJobAddr, Kind: "workflow job", Job: retireJobName},
		{Address: retireSvcAddr, Kind: "service account"},
		{Address: retireKeptAddr, Kind: "secret container", Logical: retireKeptName, Kept: infra.KeptSecret},
	}
	api, apply, kept := retireGoingAway(rets)
	if len(api) != 1 || api[0].Address != retireJobAddr {
		t.Errorf("api = %v", api)
	}
	if len(apply) != 1 || apply[0].Address != retireSvcAddr {
		t.Errorf("apply = %v", apply)
	}
	if len(kept) != 1 || kept[0].Address != retireKeptAddr {
		t.Errorf("kept = %v", kept)
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
	if !strings.Contains(retireAborted().Error(), retireNextCommand) {
		t.Error("the abort does not name the next command")
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
