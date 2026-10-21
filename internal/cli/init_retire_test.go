package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
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
	"github.com/dimipaun/fugaro/internal/initflow"
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
	retireSvcAddr  = `module.repo.module.workflow["app_bmb"].google_service_account.job`
	retireKeptAddr = `module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`
	// The repository's registry: the configuration still produces it, so it
	// is never a retirement, and its module declaration carries the second
	// lifecycle.prevent_destroy (modules/repo/registry.tf:59).
	retireRegistryAddr = `module.repo.google_artifact_registry_repository.images`
)

// retireKeptSecretID is the container's Secret Manager ID as a real state's
// own values carry it: the derived name (gcp.SecretID), not the logical name
// repeated. With the two equal the confirmation's "logical (id)" line and the
// golden's KEPT row read "claude-oauth-token-bmb (claude-oauth-token-bmb)",
// which no real repository's state holds (review round 2, nit).
func retireKeptSecretID(t *testing.T) string {
	t.Helper()
	slug, err := task.Slug("bitbucket", "acme/sandbox")
	if err != nil {
		t.Fatal(err)
	}
	return gcp.SecretID(slug, retireKeptName)
}

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
	// keptLive is the union's own case (N4): the kept container is live in
	// Secret Manager carrying this repository's labels AND still in the
	// state, so Readiness unions it back into RepoSpec.Secrets, the plan
	// re-produces its address and adopts it. It is not a retirement.
	keptLive bool
	// jobNoName is a job the state holds whose own `name` attribute could
	// not be read: Protected keeps it in the removal set (the conservative
	// direction) and the API delete has no name to call.
	jobNoName bool
	// jobNoProtection is a job whose state carries no deletion_protection
	// attribute at all: Protected is still true (the conservative default
	// the removal set needs), but nothing was read, so the refusal gate may
	// not fire on it (design §3.1).
	jobNoProtection bool
	// stateRmFails makes the state removal succeed for its first address and
	// fail for its second: the midway failure M-D's naming rule is about.
	stateRmFails bool
	// planClean is the resume's own shape: an earlier run already removed the
	// addresses and applied, so terraform's plan changes nothing (exit 0,
	// -detailed-exitcode's own "no changes") and the API delete is the only
	// thing still owed (see noJobInState).
	planClean bool
	// deleteOpError is the delete's operation ending in error: the API
	// accepted the delete, the operation finished badly, and the job is
	// still there -- the run must not report it deleted.
	deleteOpError string
	// deletePolls holds the delete's operation open for that many reads of
	// it, so the run's third outcome ("the job delete is still running") is
	// reached end to end rather than only in deleteOrphanedJob's own test.
	deletePolls int
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
		retireChange(retireRegistryAddr, "google_artifact_registry_repository", "create"),
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
	if o.planClean {
		// -detailed-exitcode: 0 is "no changes", and a resume whose apply
		// already happened is exactly that.
		r.script["plan"] = map[string]any{"exit": 0}
	}
	if o.planAbort != "" {
		r.script["plan"] = map[string]any{"exit": 1, "stderr": o.planAbort}
	}
	if o.stateRmFails {
		r.script["state rm"] = []map[string]any{
			{"exit": 0},
			{"exit": 1, "stderr": "Error: Cannot remove object from state: the backend refused the write"},
		}
	}
	r.save(t)
	if o.deleteFails != 0 {
		r.run.FailDeleteWith = o.deleteFails
	}
	if o.deleteOpError != "" || o.deletePolls != 0 {
		r.run.SetJobFaults(gcpfake.JobFaults{DeletePolls: o.deletePolls, DeleteOpError: o.deleteOpError})
	}
	if o.keptAsNote || o.keptLive {
		// The container an earlier run released: out of the state (keptAsNote)
		// or still in it (keptLive), in both cases live in Secret Manager with
		// its labels, which is the only record left.
		r.sm.Seed(retireKeptSecretID(t), map[string]string{
			gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: retireKeptName,
		}, []byte("v"))
	}

	// The cloud: the orphaned job carrying this variant's labels. The kept
	// container is deliberately NOT in it unless keptLive asks for the
	// union's case -- discovery adopts a live labelled container back into
	// the spec (the project's own layer), so the plain retirement is one the
	// cloud no longer holds: the state still manages it and fugaro.yaml no
	// longer mounts it.
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
		workflow = append(workflow, map[string]any{"address": retireSvcAddr, "mode": "managed", "type": "google_service_account", "name": "job",
			"values": map[string]any{"account_id": retireJobName}})
	}
	if !o.noJobInState && !o.stateEmpty {
		values := map[string]any{"name": retireJobName, "location": "us-east5", "deletion_protection": o.protection}
		if o.jobNoName {
			delete(values, "name")
		}
		if o.jobNoProtection {
			delete(values, "deletion_protection")
		}
		workflow = append(workflow, map[string]any{"address": retireJobAddr, "mode": "managed", "type": "google_cloud_run_v2_job", "name": "this",
			"values": values})
	}
	root := []any{}
	if !o.keptAsNote {
		root = append(root, map[string]any{"address": retireKeptAddr, "mode": "managed", "type": "google_secret_manager_secret", "name": "this",
			"values": map[string]any{"secret_id": retireKeptSecretID(t)}})
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
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--no-build")
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
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--no-build", "--yes", "--allow-delete", retireSvcAddr)
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
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--no-build", "--yes", "--allow-delete", retireSvcAddr)
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
	out, errOut, err = executeStdin(t, "", "setup", "--repo", "--retire", "--no-build")
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
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
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
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	for _, want := range []string{
		retireKeptName + " (" + retireKeptSecretID(t) + ")",
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

// --yes, --non-interactive, --json, a coding agent's session and a stdin
// that is a pipe rather than a terminal cover neither confirmation: each
// refuses, and nothing leaves the state and nothing is deleted.
//
// The refusal's words are pinned per case (review round 4). The retirement's
// confirmation is not the ordinary one and cannot be answered the ordinary
// way: --yes covers ask() wherever it appears, but askTyped never shows a
// prompt to anyone but a real terminal, so the generic notConfirmed would
// tell a --yes run that "the project's name was not typed" -- which did not
// happen -- and would send a piped run to --yes, the one flag design §4.2
// forbids for a retirement. So the message must name the real terminal that
// is missing, and never offer --yes.
func TestInitRepoRetireTypedConfirmationsAreNotCoveredByAnythingElse(t *testing.T) {
	for name, tc := range map[string]struct {
		args    []string
		agent   string
		notTTY  bool
		want    []string
		notWant []string
	}{
		"--yes": {
			args: []string{"--yes"},
			want: []string{
				"the typed confirmation of a retirement needs a real terminal",
				"(--yes never confirms a retirement)",
				"nothing was removed from Terraform's state and nothing was deleted",
			},
			notWant: []string{"was not typed", "pass --yes once you have read what it does"},
		},
		"--non-interactive": {
			args:    []string{"--yes", "--non-interactive"},
			want:    []string{"needs a real terminal", "(--yes and --non-interactive never confirm a retirement)"},
			notWant: []string{"was not typed", "pass --yes once you have read what it does"},
		},
		"--json": {
			args:    []string{"--yes", "--json"},
			want:    []string{"needs a real terminal", "(--yes and --json never confirm a retirement)"},
			notWant: []string{"was not typed", "pass --yes once you have read what it does"},
		},
		"a coding agent": {
			args:  []string{"--yes"},
			agent: "CLAUDECODE",
			// The agent's own refusal, not a terminal one: there is a
			// terminal here, and a person at it.
			want:    []string{initflow.AgentRefusal("CLAUDECODE")},
			notWant: []string{"was not typed", "pass --yes once you have read what it does"},
		},
		// A pipe: the answers are there to be read, but nobody is at a
		// terminal to read them, so the confirmation is unreachable -- and
		// since no flag of this run made it so, the refusal is the bare
		// "needs a real terminal" and must not name --yes at all.
		"a pipe, not a terminal": {
			notTTY: true,
			want: []string{
				"the typed confirmation of a retirement needs a real terminal",
				"nothing was removed from Terraform's state and nothing was deleted",
			},
			notWant: []string{"was not typed", "--yes"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRetireRig(t, retireOpts{protection: true})
			if tc.agent != "" {
				t.Setenv(tc.agent, "1")
			}
			if tc.notTTY {
				old := stdinIsTerminal
				stdinIsTerminal = func(io.Reader) bool { return false }
				t.Cleanup(func() { stdinIsTerminal = old })
			}
			args := append([]string{"setup", "--repo", "--retire", "--no-build"}, tc.args...)
			out, errOut, err := executeStdin(t, "aurora\naurora\n", args...)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("exit %d, err %v\n%s%s; want a refusal", ExitCode(err), err, out, errOut)
			}
			if s := out + errOut; !strings.Contains(s, "⚠ CONFIRM") {
				t.Errorf("%s: the confirmation was not even shown:\n%s", name, s)
			}
			refusal := err.Error()
			for _, want := range tc.want {
				if !strings.Contains(refusal, want) {
					t.Errorf("%s: the refusal does not say %q: %q", name, want, refusal)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(refusal, notWant) {
					t.Errorf("%s: the refusal says %q, which is not what happened here and is not this flow's route: %q",
						name, notWant, refusal)
				}
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
//
// M-5 pins the list itself: it is the plan's deletes, not the summary's
// prediction, and the fixture tells the two apart. The retirement set holds
// three addresses -- the variant's service account the plan really deletes,
// the orphaned job the removal takes out of the state before the plan, and
// the container the plan still produces (adopted, kept) -- so a list built
// from the prediction would name the job and the container, and no apply may
// destroy either. Only `delete <the service account>` belongs in the block,
// and it has to sit between the header and the prompt.
func TestInitRepoRetireShowsThePlanBeforeTheSecondConfirmation(t *testing.T) {
	newRetireRig(t, retireOpts{protection: true})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	second := strings.LastIndex(text, "⚠ CONFIRM")
	counts := strings.Index(text, "0 imports, 1 creates, 1 updates, 1 deletes")
	header := strings.Index(text, "the plan above is the retirement's: 0 imports, 1 creates, 1 updates, 1 deletes, 0 replaces. Deleting:")
	list := strings.Index(text, "\n  delete "+retireSvcAddr)
	if counts == -1 || header == -1 || list == -1 || second == -1 {
		t.Fatalf("counts at %d, the header at %d, the delete list at %d, the second prompt at %d:\n%s",
			counts, header, list, second, text)
	}
	// Fatal, not Error: the block below is the slice between two of these
	// offsets, so an ordering that puts the prompt first must fail here
	// rather than panic in the slice.
	if counts > second || header > second || list > second {
		t.Fatalf("the plan is shown after the second confirmation (counts %d, header %d, list %d, prompt %d)",
			counts, header, list, second)
	}
	if list < header {
		t.Errorf("the delete list is printed before the line that introduces it (header %d, list %d)", header, list)
	}
	// The block is the plan's own list: exactly the deletes of this plan, one
	// address a line, and nothing the prediction holds.
	block := text[header:second]
	var deletes []string
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "  delete ") {
			deletes = append(deletes, strings.TrimPrefix(line, "  delete "))
		}
	}
	if len(deletes) != 1 || deletes[0] != retireSvcAddr {
		t.Errorf("the printed delete list is %q, want only the plan's own delete of %s:\n%s", deletes, retireSvcAddr, block)
	}
	for _, predicted := range []string{retireJobAddr, retireKeptAddr} {
		if strings.Contains(block, predicted) {
			t.Errorf("the printed list names %s, which is a retirement no plan of this run deletes:\n%s", predicted, block)
		}
	}
	// And both of them are in the retirement set this run printed: the block
	// above is what differs, not the summary.
	if !strings.Contains(text, retireJobAddr) {
		t.Errorf("fixture bug: the retirement set does not hold the job:\n%s", text)
	}
}

// I1: a declined second confirmation says what has already happened, not what
// was predicted. The state removal runs before the prompt, so the message must
// name the addresses that left the state, by address, and the command that
// finishes the work -- "nothing was removed" would be false by then.
func TestInitRepoRetireDeclinedSecondConfirmationNamesWhatLeftTheState(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true})
	out, errOut, err := executeStdin(t, "aurora\nno\n", "setup", "--repo", "--retire", "--no-build")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s%s; want the decline to stop the run", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	if !strings.Contains(text, "removed 2 addresses from Terraform's state; nothing was destroyed") {
		t.Errorf("the state removal is not reported before the prompt:\n%s", text)
	}
	if n := strings.Count(text, "⚠ CONFIRM"); n != 2 {
		t.Errorf("%d confirmations, want the first and the second:\n%s", n, text)
	}
	msg := err.Error()
	for _, want := range []string{
		"not confirmed", "the plan was not applied and no job was deleted",
		"2 addresses had already left Terraform's state", retireKeptAddr, retireJobAddr, retireNextCommand,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the decline does not say %q in %q", want, msg)
		}
	}
	if applies := r.applies(t); len(applies) != 0 {
		t.Errorf("a declined confirmation applied: %v", applies)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("a declined confirmation deleted %v", deletes)
	}
	if !r.run.HasJob(retireJobName) {
		t.Error("the job is gone although the delete was declined")
	}
}

// The no-change (resume) path owes the same second typed confirmation: the
// plan changes nothing because an earlier run already removed the addresses
// and applied, but the API job delete is still in front of the user, and no
// plan shows it (design §4.2). Declining stops the run there, with nothing
// deleted I-C.
func TestInitRepoRetireResumeNoChangeAsksBeforeTheDeleteAndDecliningStopsIt(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, noJobInState: true, planClean: true})
	out, errOut, err := executeStdin(t, "aurora\nno\n", "setup", "--repo", "--retire", "--no-build")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s%s; want the declined confirmation to stop the run", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	if !strings.Contains(text, "No changes: ") {
		t.Errorf("the clean plan is not reported:\n%s", text)
	}
	if n := strings.Count(text, "⚠ CONFIRM"); n != 2 {
		t.Errorf("%d confirmations, want two: the resume owes the delete's own:\n%s", n, text)
	}
	// Both prompts are the typed route. ask is the one --yes covers, and
	// retireConfirm uses askTyped precisely because this step is not one --yes
	// may cover: the prompt text is what tells the two apart, and the
	// --yes-coverable one here would let a --yes run delete a job.
	if n := strings.Count(text, "Type "+initProjectName+" to confirm: "); n != 2 {
		t.Errorf("%d typed prompts, want two: the delete's confirmation is not the --yes-coverable one:\n%s", n, text)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("a declined confirmation still deleted %v", deletes)
	}
	if !r.run.HasJob(retireJobName) {
		t.Error("the job is gone although the delete was declined")
	}
	msg := err.Error()
	for _, want := range []string{"not confirmed", "nothing was applied and no job was deleted", "had already left Terraform's state", retireNextCommand} {
		if !strings.Contains(msg, want) {
			t.Errorf("the decline does not say %q in %q", want, msg)
		}
	}
}

// The same resume, confirmed: the delete runs and the apply does not -- a
// clean plan is not applied again, and the confirmation's own banner says so
// rather than naming an apply that is not coming.
func TestInitRepoRetireResumeNoChangeDeletesAfterTheConfirmation(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, noJobInState: true, planClean: true})
	// The third answer is the local config's own confirmation: this run never
	// applied, so that write is not covered by an apply's.
	out, errOut, err := executeStdin(t, "aurora\naurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	if !strings.Contains(text, "the plan changes nothing and 1 address is already out of Terraform's state (destroying nothing), so what remains is the delete of 1 Cloud Run job through the API") {
		t.Errorf("the confirmation does not name what actually remains:\n%s", text)
	}
	if applies := r.applies(t); len(applies) != 0 {
		t.Errorf("a clean plan was applied: %v", applies)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 1 || deletes[0] != retireJobName {
		t.Errorf("JobDeletes = %v, want the orphan the labels named", deletes)
	}
	if !strings.Contains(text, "deleted the orphaned Cloud Run job "+retireJobName) {
		t.Errorf("the API delete is not reported:\n%s", text)
	}
}

// The union's own case, end to end (N4, review round 7): the OAuth container
// is live in Secret Manager carrying this repository's labels AND still in
// Terraform's state, while no job of the configuration mounts it any more.
// Readiness unions it back into RepoSpec.Secrets, so the same run's plan
// re-produces its address and adopts it: it is not a retirement, it is not
// kept, it is not handed to `terraform state rm`, and the summary says
// nothing about it.
//
// The two halves of this were pinned apart and never composed: the state-side
// test asserts a unioned container is adopted rather than released, the label
// side asserts Orphans returns the container, and the rig's plain retirement
// deliberately leaves the cloud without it -- so the composition, which is
// what a real run does, was the untested path.
func TestInitRepoRetireAdoptsALiveLabelledContainerInTheState(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, keptLive: true})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut

	// The union is what makes it adopted: the run reports the import the
	// plan needs to take the live container back, which is the mechanism the
	// retirement must not fight.
	if !strings.Contains(text, retireKeptName) {
		t.Errorf("the run does not mention the adopted container at all:\n%s", text)
	}
	// The retirement set is the two addresses the configuration really no
	// longer produces: the variant's service account and its job.
	if !strings.Contains(text, "⚠ This change retires 2 addresses") {
		t.Errorf("the retirement set is not the two state-derived addresses:\n%s", text)
	}
	for _, notWant := range []string{
		// The KEPT block and the removal list are the two false claims: the
		// first says the container leaves the state, the second does it.
		"KEPT — Fugaro never deletes a secret or a secret version",
		"Removed from Terraform's state, and never deleted",
	} {
		if strings.Contains(text, notWant) {
			t.Errorf("the summary says %q about a container the same run adopts:\n%s", notWant, text)
		}
	}
	// Nothing may hand its address to the state removal: the containers are
	// not removed from anything (design §1, §3.2, §4.2a).
	for _, c := range r.ran(t, "state rm") {
		if call := strings.Join(c, " "); strings.Contains(call, retireKeptAddr) {
			t.Errorf("the state removal names the adopted container: %s", call)
		}
	}
	if d, x := r.sm.Deleted(), r.sm.Destroyed(); len(d) != 0 || len(x) != 0 {
		t.Errorf("the adopted container was deleted (%v) or destroyed (%v)", d, x)
	}
	// The retirement itself still runs: the plan deletes the service account
	// and the API deletes the orphaned job.
	if deletes := r.run.JobDeletes(); len(deletes) != 1 || deletes[0] != retireJobName {
		t.Errorf("JobDeletes = %v, want the orphaned job", deletes)
	}
	if !strings.Contains(text, "deleted the orphaned Cloud Run job "+retireJobName) {
		t.Errorf("the API delete is not reported:\n%s", text)
	}
}

// The other shape of the same resume: the state removal had nothing left to
// take (every address the run knows is one the labels found), so the
// confirmation names the delete alone -- no count of addresses that never left
// the state.
func TestInitRepoRetireResumeNoChangeNamesOnlyTheDelete(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, noJobInState: true, keptAsNote: true, planClean: true})
	out, errOut, err := executeStdin(t, "aurora\naurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	// The confirmation's own banner, not the "removed 0" report below it.
	clean := text[strings.Index(text, "the plan changes nothing"):]
	if !strings.Contains(clean, "the plan changes nothing, so what remains is the delete of 1 Cloud Run job through the API") {
		t.Errorf("the confirmation does not name the delete as what remains:\n%s", text)
	}
	if banner := clean[:strings.Index(clean, ". Delete ")]; strings.Contains(banner, "0 addresses") {
		t.Errorf("the confirmation counts addresses that never left the state:\n%s", text)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 1 || deletes[0] != retireJobName {
		t.Errorf("JobDeletes = %v, want the orphan the labels named", deletes)
	}
}

// --plan-only stays read-only: the summary prints before the ordinary plan,
// and nothing is removed, deleted or applied.
func TestInitRepoPlanOnlyRemovesNothing(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true})
	// --allow-delete names the service account: --plan-only runs the
	// ordinary guard, which refuses any delete the user did not name.
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--no-build", "--plan-only", "--allow-delete", retireSvcAddr)
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
	_, _, err := executeStdin(t, "", "setup", "--repo", "--retire", "--plan-only")
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
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
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
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
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
// both holds it and says its protection is on -- a value, not the
// conservative default (ProtectedRead). Protected alone cannot tell the two
// apart, and the refusal may not put words in the state's mouth (design
// §3.1, review round 3).
func TestBlockingJobsReadsTheStatesOwnProtection(t *testing.T) {
	rets := []infra.Retirement{
		{Address: retireJobAddr, Kind: "workflow job", Protected: true, ProtectedRead: true},
		{Address: retireSvcAddr, Kind: "service account", Protected: true},
	}
	if got := blockingJobs([]string{retireJobAddr, retireSvcAddr}, rets); len(got) != 1 || got[0].Address != retireJobAddr {
		t.Errorf("blockingJobs = %v, want only the protected job", got)
	}
	if got := blockingJobs([]string{retireSvcAddr}, rets); len(got) != 0 {
		t.Errorf("blockingJobs = %v, want nothing: the state does not hold the job", got)
	}
	open := []infra.Retirement{{Address: retireJobAddr, Kind: "workflow job", Protected: false, ProtectedRead: true}}
	if got := blockingJobs([]string{retireJobAddr}, open); len(got) != 0 {
		t.Errorf("blockingJobs = %v, want nothing for a job the state says is not protected", got)
	}
	// The conservative default: a job whose protection could not be read is
	// protected for the removal set, and not blocking for the gate.
	unread := []infra.Retirement{{Address: retireJobAddr, Kind: "workflow job", Protected: true}}
	if got := blockingJobs([]string{retireJobAddr}, unread); len(got) != 0 {
		t.Errorf("blockingJobs = %v, want nothing for a job whose deletion_protection the state does not carry", got)
	}
	if !unread[0].Protected {
		t.Error("an unread attribute must still leave the job protected: the removal set may not guess it unprotected")
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

// A protected job of a workflow variant that is still deployed is the row
// nothing deletes: the state removal takes it out (its protection is on),
// the plan then cannot see it at all, and the API delete refuses that kind
// by rule -- the job of a deployed variant must never be a candidate for it
// (M3). The summary used to list it under "Going away ... the plan will
// delete", which is false the moment it leaves the state; it belongs to the
// removal list alone (review round 2, M-D).
func TestPrintRetirementsCountsALiveVariantsJobAsRemoved(t *testing.T) {
	const liveJob = `module.repo.module.workflow["app"].google_cloud_run_v2_job.this[0]`
	spec := infra.RepoSpec{
		Name: "acme-sandbox", GCPProject: initProject, Region: "us-east5",
		Workflows: map[string]infra.WorkflowSpec{"app": {}},
	}
	rets := []infra.Retirement{
		{Address: liveJob, Kind: "job", Job: "fugaro-acme-sandbox-app", Protected: true,
			Cause: "the configuration no longer produces this job, though the workflow variant app is still deployed"},
		{Address: retireJobAddr, Kind: "workflow job", Job: retireJobName, Protected: true},
	}
	var out strings.Builder
	r := &initRun{w: &out, projectName: "aurora", gcpProject: initProject,
		retireState: []string{liveJob, retireJobAddr}, retireSet: rets}
	if err := r.printRetirements(spec, rets); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	// The variant's job is the API's; the live variant's is nobody's, so the
	// "going away" count is one and the plan deletes none of them.
	if !strings.Contains(text, "1 go away.") {
		t.Errorf("the summary does not count the live variant's job as going away by a delete:\n%s", text)
	}
	if !strings.Contains(text, "Going away (1, predicted from the state and the labels) — 0 the plan will delete, 1 deleted through the Cloud Run API after it:") {
		t.Errorf("the summary still promises the plan will delete it:\n%s", text)
	}
	if !strings.Contains(text, "Removed from Terraform's state, and never deleted (1):\n  "+liveJob+"\n") {
		t.Errorf("the summary does not list it under the removal:\n%s", text)
	}
	// The API job is not in the removal list: it goes away by its own delete.
	if strings.Contains(text, "and never deleted (1):\n  "+retireJobAddr) {
		t.Errorf("the API-deleted job is listed as never deleted:\n%s", text)
	}
}

// A run that applied its retirement and then ended before the API delete --
// a failure in the secret versions, the build resolution, the builds or the
// re-plan -- owes the user the addresses the state removal took out: they
// are alive in GCP with nothing managing them, and the delete is the step
// that never ran. Saying nothing there is what design §6 forbids (review
// round 2, M-B). A run that never applied prints nothing at all.
func TestRetireLeftoverNamesWhatAStopBeforeTheAPIDeleteOwes(t *testing.T) {
	spec := infra.RepoSpec{Name: "acme-sandbox", GCPProject: initProject, Region: "us-east5"}
	boom := errors.New("the secret versions could not be read")
	var out strings.Builder
	r := &initRun{w: &out, retireApplied: true, retireRemoved: []string{retireJobAddr},
		retireSet: []infra.Retirement{{Address: retireJobAddr, Kind: "workflow job", Job: retireJobName, Region: "us-east5"}}}
	if err := r.retireLeftover(spec, boom); err != boom {
		t.Fatalf("retireLeftover = %v, want the failure it was given", err)
	}
	text := out.String()
	for _, want := range []string{
		retireJobAddr, "this address is alive in GCP with nothing managing it",
		"gcloud run jobs delete " + retireJobName, retireNextCommand,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the stop before the API delete does not say %q:\n%s", want, text)
		}
	}
	// Nothing applied: nothing is alive and unmanaged, so nothing is said.
	var quiet strings.Builder
	if err := (&initRun{w: &quiet}).retireLeftover(spec, boom); err != boom {
		t.Errorf("retireLeftover = %v, want the failure it was given", err)
	}
	if quiet.Len() != 0 {
		t.Errorf("a run that never applied named a residue: %s", quiet.String())
	}
}

// The state removal is one terraform call per address, so it can fail
// midway. The addresses removed before the failure are out of the state --
// alive in GCP with nothing managing them -- and design §6's rule is that
// they are named, not swallowed with the failure (review round 2, M-D). The
// rig's state holds two protected addresses (the container and the job), and
// the script fails the second call.
func TestInitRepoRetireNamesWhatAPartialStateRemovalTook(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, stateRmFails: true})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if err == nil {
		t.Fatalf("the partial state removal did not fail the run\n%s%s", out, errOut)
	}
	if ExitCode(err) != ExitRemoteError {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	// Removals sorts, so the container's address is the call that succeeded
	// and the job's is the one that failed.
	for _, want := range []string{
		retireKeptAddr, "this address is alive in GCP with nothing managing it",
		"Rerun " + retireNextCommand,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the midway failure does not say %q:\n%s", want, text)
		}
	}
	if applies := r.applies(t); len(applies) != 0 {
		t.Errorf("applies = %v, want none: the removal did not finish", applies)
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
	// The address used here is the container's, so this is also I-B's unit
	// form: no message of fugaro's tells the reader to drop a
	// prevent_destroy block from a resource it never destroys.
	notRead := retireAborted(nil, retireKeptAddr, "/wd")
	if strings.Contains(notRead.Error(), retireNextCommand) {
		t.Errorf("the abort points at --retire for an address it would not remove: %v", notRead)
	}
	if !strings.Contains(notRead.Error(), retireKeptAddr) || !strings.Contains(notRead.Error(), "prevent_destroy") {
		t.Errorf("the abort does not name the address and the block: %v", notRead)
	}
	for _, want := range []string{"terraform state rm '" + retireKeptAddr + "'", "/wd", "keeps the secret and its value"} {
		if !strings.Contains(notRead.Error(), want) {
			t.Errorf("the abort for a resource fugaro never destroys does not say %q: %v", want, notRead)
		}
	}
	// An ordinary address is not given the module advice, and is given the
	// state removal instead (M-1): the repo root's only two
	// lifecycle.prevent_destroy declarations are the container and the
	// registry, and the embedded workdir is rewritten every run, so "take the
	// block out of the module" could not be followed even if it were safe.
	// A service account, an IAM member and a bucket grant are all things an
	// apply may destroy once the configuration stops producing them.
	const plain = `module.repo.module.workflow["app"].google_service_account.job`
	ordinary := retireAborted(nil, plain, "/wd")
	if strings.Contains(ordinary.Error(), "take the lifecycle.prevent_destroy block out of the module") {
		t.Errorf("an ordinary address is still told to edit the module: %v", ordinary)
	}
	for _, want := range []string{"terraform state rm '" + plain + "'", "/wd", "revert the configuration change"} {
		if !strings.Contains(ordinary.Error(), want) {
			t.Errorf("the abort for an ordinary address does not say %q: %v", want, ordinary)
		}
	}
	// And when Terraform's text does not yield the address, the removal is
	// asked for by the address Terraform named, not by an empty pair of
	// quotes -- which is the same wording the reader needs either way.
	unreadable := retireAborted(nil, "", "/wd")
	for _, want := range []string{"the address named in Terraform's error", "/wd", "revert the configuration change"} {
		if !strings.Contains(unreadable.Error(), want) {
			t.Errorf("the abort with no address does not say %q: %v", want, unreadable)
		}
	}
	if strings.Contains(unreadable.Error(), "state rm ''") {
		t.Errorf("the abort with no address asks to remove the empty address: %v", unreadable)
	}
	if !strings.Contains(unreadable.Error(), "a resource carries") {
		t.Errorf("the abort with no address does not read as a sentence: %v", unreadable)
	}
	read := retireAborted([]infra.Retirement{{Address: retireKeptAddr, Kept: infra.KeptSecret}}, retireKeptAddr, "/wd")
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

// The operation's own failure (op.Error != nil) is the fourth outcome of the
// wait: the API accepted the delete, the operation ended in error, and the
// job is still there. Reporting it deleted would be §6's forbidden silent
// disagreement -- a live job with nothing managing it and nothing said
// (review round 4).
func TestDeleteOrphanedJobReportsAnOperationThatFailed(t *testing.T) {
	r := newInitRig(t)
	c := retireClients(t, r)
	spec := infra.RepoSpec{Label: "acme-sandbox", GCPProject: initProject, Region: "us-east5"}
	want, _ := infra.JobDeleteLabels(spec, infra.Retirement{Address: retireJobAddr, Kind: "workflow job"})
	r.run.SetJob(retireJobName, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: spec.Label, gcp.LabelWorkflow: retireVariant,
	}, "")
	r.run.SetJobFaults(gcpfake.JobFaults{DeleteOpError: "the job is in use"})

	err := deleteOrphanedJob(t.Context(), c, initProject, "us-east5", retireJobName, want)
	if err == nil {
		t.Fatal("a delete whose operation failed was reported as done")
	}
	var still *stillDeletingError
	if errors.As(err, &still) {
		t.Errorf("a failed operation was reported as still running: %v", err)
	}
	for _, w := range []string{"its operation", "failed", "the job is in use", gcloudJobDelete(initProject, "us-east5", retireJobName)} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the failure does not say %q: %v", w, err)
		}
	}
	if !r.run.HasJob(retireJobName) {
		t.Error("a job whose operation failed is gone")
	}
}

// Both of the delete's reads can fail, and each says which read it was and
// how to finish by hand: run.jobs.get before anything is deleted, and the
// operation's own run.operations.get while waiting. Neither is a delete
// (review round 4).
func TestDeleteOrphanedJobReportsItsFailingReads(t *testing.T) {
	oldPolls, oldPoll := retireDeletePolls, retireDeletePoll
	retireDeletePolls, retireDeletePoll = 3, time.Millisecond
	t.Cleanup(func() { retireDeletePolls, retireDeletePoll = oldPolls, oldPoll })

	r := newInitRig(t)
	c := retireClients(t, r)
	spec := infra.RepoSpec{Label: "acme-sandbox", GCPProject: initProject, Region: "us-east5"}
	want, _ := infra.JobDeleteLabels(spec, infra.Retirement{Address: retireJobAddr, Kind: "workflow job"})
	labels := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: spec.Label, gcp.LabelWorkflow: retireVariant}

	// run.jobs.get, the read that says what the job is before it is deleted.
	r.run.SetJob(retireJobName, labels, "")
	r.run.SetJobFaults(gcpfake.JobFaults{GetStatus: 500})
	err := deleteOrphanedJob(t.Context(), c, initProject, "us-east5", retireJobName, want)
	if err == nil {
		t.Fatal("a job that could not be read was deleted")
	}
	for _, w := range []string{"reading it (run.jobs.get)", gcloudJobDelete(initProject, "us-east5", retireJobName)} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the read failure does not say %q: %v", w, err)
		}
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("a job whose labels could not be read was deleted: %v", deletes)
	}

	// run.operations.get, the read that says whether the delete finished.
	r.run.SetJob(retireJobName, labels, "")
	r.run.SetJobFaults(gcpfake.JobFaults{DeletePolls: 1000, OpStatus: 500})
	err = deleteOrphanedJob(t.Context(), c, initProject, "us-east5", retireJobName, want)
	if err == nil {
		t.Fatal("a delete whose operation could not be read was reported as done")
	}
	for _, w := range []string{"run.operations.get", gcloudJobDelete(initProject, "us-east5", retireJobName)} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the operation read failure does not say %q: %v", w, err)
		}
	}
	if !r.run.HasJob(retireJobName) {
		t.Error("a job whose operation could not be read was reported gone")
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
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--no-build", "--yes")
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

// The reframing never tells the reader to drop a lifecycle.prevent_destroy
// block from a resource fugaro itself never destroys (review round 2, I-B):
// the modules are embedded in the binary and PrepareWorkdir rewrites the
// workdir, so the advice cannot be followed; and taking it would delete a
// secret container with every version in it, or a registry with every image
// -- the one thing fugaro promises never to do. The registry is the case
// here: the configuration still produces it, so it is not a retirement at
// all, which is what puts it on this branch. The fixture's own wrapped text
// carries the address.
func TestInitRepoPreventDestroyOnAResourceFugaroNeverDestroys(t *testing.T) {
	aborted := strings.Replace(fixture(t, "prevent_destroy_wrapped.txt"), retireKeptAddr, retireRegistryAddr, 1)
	r := newRetireRig(t, retireOpts{planAbort: aborted})
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--no-build", "--yes")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	msg := err.Error()
	if !strings.Contains(msg, "the plan was refused: "+retireRegistryAddr) {
		t.Errorf("the refusal does not name the address Terraform named: %v", err)
	}
	if strings.Contains(msg, "take the lifecycle.prevent_destroy block out of the module") {
		t.Errorf("fugaro tells the reader to delete the block on a resource it never destroys: %v", err)
	}
	for _, want := range []string{
		"terraform state rm '" + retireRegistryAddr + "'",
		"keeps the registry and the images in it",
		"revert the configuration change",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	// The workdir the state removal would run in is named, and it is the real
	// one -- under the state directory this run uses -- rather than a
	// placeholder: the reader has to run the command there.
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" || !strings.Contains(msg, "`terraform state rm '"+retireRegistryAddr+"'` in "+filepath.Join(state, "fugaro", "terraform", initProject)) {
		t.Errorf("the refusal does not name the workdir under %q: %v", state, err)
	}
	// Advice only: nothing left the state and nothing was applied.
	if calls := r.ran(t, "state rm"); len(calls) != 0 {
		t.Errorf("the refused run removed state: %v", calls)
	}
	if applies := r.applies(t); len(applies) != 0 {
		t.Errorf("the refused run applied: %v", applies)
	}
}

// The owner's own flow, end to end, across two runs of the CLI: the plan
// aborts on prevent_destroy for the container the configuration stopped
// producing, the refusal names --retire, and the rerun -- which removes that
// address from the state before it plans -- gets past the same plan. The
// two runs share one rig and one script, and the fake terraform's sequencing
// is what tells them apart: `plan@repo`'s first step is the abort, its
// second is the plan the rerun sees.
func TestInitRepoPreventDestroyThenRetireFinishesIt(t *testing.T) {
	aborted := fixture(t, "prevent_destroy_wrapped.txt") // before the rig chdirs
	// The job's own deletion_protection is off, so the first run is not
	// stopped by the refusal gate: it gets as far as the plan, which is
	// where the container's prevent_destroy refuses it.
	r := newRetireRig(t, retireOpts{})
	r.script["plan@repo"] = []map[string]any{
		{"exit": 1, "stderr": aborted},
		{"exit": 2}, // the rerun's plan: the same changes, no destroy to refuse
	}
	r.save(t)

	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--no-build", "--yes")
	if ExitCode(err) != ExitUserError || err == nil ||
		!strings.Contains(err.Error(), "the plan was refused: "+retireKeptAddr) {
		t.Fatalf("the first run was not refused on the container: exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	if err := err; !strings.Contains(err.Error(), retireNextCommand) {
		t.Fatalf("the refusal does not name the rerun: %v", err)
	}
	if calls := r.ran(t, "state rm"); len(calls) != 0 {
		t.Fatalf("the refused run removed state: %v", calls)
	}

	out, errOut, err = executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("the rerun failed: exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	var removed []string
	for _, c := range r.ran(t, "state rm") {
		for _, arg := range c {
			if strings.HasPrefix(arg, "module.") {
				removed = append(removed, arg)
			}
		}
	}
	if !slices.Contains(removed, retireKeptAddr) {
		t.Errorf("the rerun did not remove the container the plan refused to destroy: %v", removed)
	}
	if applies := r.applies(t); len(applies) != 1 {
		t.Errorf("applies = %v, want the rerun's one apply", applies)
	}
	// The job's deletion_protection is off here, so the plan deletes it and
	// no API delete is needed; what this test is about is the plan the
	// first run could not get past and the rerun could.
	if deletes := r.run.JobDeletes(); len(deletes) != 0 {
		t.Errorf("JobDeletes = %v, want none for an unprotected job", deletes)
	}
}

// A retirement that stops after the state removal may not stop quietly: the
// addresses that left the state are alive in GCP with nothing managing them,
// and the run says which ones, with the job delete named as the step still to
// come (Review Focus 6, design §6).
func TestInitRepoRetireHalfFailureNamesWhatWasRemoved(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, planAbort: "Error: Invalid provider configuration\n"})
	out, errOut, err := executeStdin(t, "aurora\n", "setup", "--repo", "--retire", "--no-build")
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
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
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

// M4: a failed API delete does not cost the run its local config. The plan
// did apply and the state did change, so the config file is the record of what
// the deployment now is -- written before the delete's error is returned, and
// the run still exits non-zero with the delete named.
func TestInitRepoRetireFailedDeleteStillWritesTheLocalConfig(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, deleteFails: 500})
	before, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, runErr := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if ExitCode(runErr) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s%s; want the failed delete to exit non-zero", ExitCode(runErr), runErr, out, errOut)
	}
	text := out + errOut
	after, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) {
		t.Errorf("the local config was not written after the failed delete:\n%s", text)
	}
	if !strings.Contains(text, "wrote "+r.cfg) {
		t.Errorf("the config write is not reported:\n%s", text)
	}
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatalf("the written config does not load: %v", err)
	}
	if repo, ok := lc.Repos["acme/sandbox"]; !ok || repo.Provider != "bitbucket" || repo.BaseBranch != "master" ||
		!slices.Equal(repo.Workflows, []string{"app"}) {
		t.Errorf("the written config's repository entry = %+v, want the run's own", repo)
	}
	// The error is still the delete's: writing the config does not swallow it.
	if !strings.Contains(runErr.Error(), "job delete failed") {
		t.Errorf("the returned error is not the failed delete: %v", runErr)
	}
}

// The other half of the half-failure: the apply succeeded, the API accepted
// the delete, and the operation ended in error -- so the job is still alive
// in GCP with nothing managing it. The run must say so, name the operation
// and the gcloud command, and exit non-zero rather than report the job
// deleted (design §6, review round 4).
func TestInitRepoRetireReportsADeleteWhoseOperationFailed(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true, deleteOpError: "the job is in use"})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s%s; want a failed operation to exit non-zero", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	for _, want := range []string{
		"apply ok, job delete failed",
		"its operation",
		"the job is in use",
		"/locations/us-east5/operations/", // the operation is named
		gcloudJobDelete(initProject, "us-east5", retireJobName),
		"apply ok, and the job is out of Terraform's state and fugaro.yaml; " + retireNextCommand,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the failed delete does not say %q:\n%s", want, text)
		}
	}
	if err.Error() == "" || !strings.Contains(err.Error(), "job delete failed for Cloud Run job "+retireJobName) {
		t.Errorf("the returned error does not name the failed job: %v", err)
	}
	if !r.run.HasJob(retireJobName) {
		t.Error("a job whose operation failed was reported gone")
	}
	if !strings.Contains(text, "wrote "+r.cfg) {
		t.Errorf("the failed delete cost the run its local config (M4):\n%s", text)
	}
}

// The run-level third outcome: the apply succeeded and the delete's operation
// had not reached a terminal state when the wait ran out. Neither success nor
// failure, said as such, with the job named and a rerun that re-attempts
// (design §4.2c, review round 4).
func TestInitRepoRetireReportsADeleteStillRunning(t *testing.T) {
	oldPolls, oldPoll := retireDeletePolls, retireDeletePoll
	retireDeletePolls, retireDeletePoll = 3, time.Millisecond
	t.Cleanup(func() { retireDeletePolls, retireDeletePoll = oldPolls, oldPoll })

	r := newRetireRig(t, retireOpts{protection: true, deletePolls: 1000})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s%s; want a delete that did not finish to exit non-zero", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	for _, want := range []string{
		"apply ok, job delete still running",
		"Cloud Run job " + retireJobName + " is still deleting: its operation",
		"is not done after",
		"/locations/us-east5/operations/",
		"apply ok, and the job is out of Terraform's state and fugaro.yaml; " + retireNextCommand,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the still-running delete does not say %q:\n%s", want, text)
		}
	}
	if !strings.Contains(err.Error(), "job delete still running for Cloud Run job "+retireJobName) {
		t.Errorf("the returned error does not name the still-deleting job: %v", err)
	}
	if !r.run.HasJob(retireJobName) {
		t.Error("a job whose operation is not done was reported gone")
	}
	if !strings.Contains(text, "wrote "+r.cfg) {
		t.Errorf("the unfinished delete cost the run its local config (M4):\n%s", text)
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
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
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
		// The hand delete alone is not enough: the state still holds the
		// address, so the rerun refuses the same address again (review round
		// 2, M-C). The step after it is named.
		"`terraform state rm '" + retireJobAddr + "'` in " + filepath.Join(os.Getenv("XDG_STATE_HOME"), "fugaro", "terraform", initProject),
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
	_, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
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
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	// The released container is a note, and only a note (design §3.1a's
	// convergence rule, review round 3): the labels are the only thing left
	// that remembers it, so the run tells the user the secret is safe -- and
	// because a note is not a retirement, there is no row for it, no state
	// removal and no delete.
	if !strings.Contains(text, "note: "+retireKeptName) {
		t.Errorf("the live labelled container got no note:\n%s", text)
	}
	for _, bad := range []string{"KEPT", retireKeptAddr} {
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
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--no-build", "--yes", "--json")
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
	if kept.Logical != retireKeptName || kept.SecretID != retireKeptSecretID(t) || kept.Kept == "" {
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
	r.sm.Seed(retireKeptSecretID(t), nil, []byte("v"))
	if _, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build"); err != nil {
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
	if got := r.sm.Latest(retireKeptSecretID(t)); len(got) == 0 {
		t.Errorf("the retired container's value is gone from Secret Manager")
	}
}

// --- The transcript, as a golden ---

// updateRetireGolden rewrites testdata/init_retire.golden. It is a flag of
// its own: the package's other goldens already own -update and
// -update-review, and a second flag.Bool of the same name panics.
var updateRetireGolden = flag.Bool("update-retire", false, "rewrite testdata/init_retire.golden")

// retireTranscriptRE are the run's few non-deterministic pieces: the test
// binary's own path, the checkout's temp dir, the fake servers' ports, the
// backup's timestamp, and the one line that quotes a tool's own failure
// text. Everything else -- the addresses, the counts, the confirmations,
// the commands -- must be exactly what the golden holds.
var retireTranscriptRE = []struct {
	re *regexp.Regexp
	to string
}{
	// "warning: couldn't check origin/main's fugaro.yaml ...": the rig's
	// origin is a local URL the fetch cannot reach, and the sentence is
	// git's, so its tail is the curl on the machine -- "Could not connect
	// to server" on the builder's Linux, "Couldn't connect" in CI's
	// macOS -- and the golden may not pin that. The whole line is folded:
	// what it says is that the check could not be made, which is the same
	// on every machine.
	{regexp.MustCompile(`warning: couldn't check origin/main's fugaro\.yaml[^\n]*`),
		"warning: couldn't check origin/main's fugaro.yaml for project: aurora: <the fetch did not reach it>; runs read it from there"},
	// The test binary's path and the temp dir differ per machine: Linux's is
	// /tmp, macOS's is /var/folders/.../T/ (a trailing slash that must not
	// double up), so neither is spelled out.
	{regexp.MustCompile(`[^ \n"']*/go-build[0-9]+/b[0-9]+/cli\.test`), "fugaro"},
	{regexp.MustCompile(regexp.QuoteMeta(strings.TrimSuffix(os.TempDir(), "/")) + `/[^ "'\n)\]]*`), "<tmp>"},
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
	out, errOut, runErr := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
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
	for _, bad := range []string{"lifecycle.prevent_destroy", "/go-build", strings.TrimSuffix(os.TempDir(), "/") + "/"} {
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
		out, errOut, err := executeStdin(t, "", "setup", "--repo", "--no-build", "--yes", "--allow-delete", retireSvcAddr)
		if err != nil {
			t.Fatalf("run %d: exit %d, err %v\n%s%s", i, ExitCode(err), err, out, errOut)
		}
		text := out + errOut
		// The adopted container is a note and nothing more: the run says its
		// value is safe, and the convergence rule -- no summary, no gate, no
		// next command -- keeps that from becoming a retirement the user is
		// asked about on every later run (design §3.1a, §6).
		if !strings.Contains(text, "note: "+retireKeptName) {
			t.Errorf("run %d: the adopted container got no note:\n%s", i, text)
		}
		for _, bad := range []string{"⚠ This change retires", retireNextCommand} {
			if strings.Contains(text, bad) {
				t.Errorf("run %d: the adopted container is reported as a retirement (%q):\n%s", i, bad, text)
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

// An ordinary --retire run with nothing to retire says so (review round 2,
// nit): the flag was typed, and one that quietly does nothing reads as one
// that was ignored. The line is the whole of what it had to say -- no
// summary, no next command, no state removal -- and the ordinary plan below
// it is what runs.
func TestInitRepoRetireSaysWhenThereIsNothingToRetire(t *testing.T) {
	// The converge fixture: an empty state, no orphan job, and the container
	// live and labelled in Secret Manager, so Readiness adopts it back into
	// the spec and nothing at all is left to retire.
	r := newRetireRig(t, retireOpts{protection: false, stateEmpty: true, noOrphanJob: true, keptAsNote: true})
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--retire", "--no-build", "--yes", "--allow-delete", retireSvcAddr)
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	if !strings.Contains(text, "Nothing to retire:") {
		t.Errorf("--retire with nothing to retire said nothing:\n%s", text)
	}
	for _, bad := range []string{"⚠ This change retires", retireNextCommand} {
		if strings.Contains(text, bad) {
			t.Errorf("the empty run printed %q:\n%s", bad, text)
		}
	}
	if calls := r.ran(t, "state rm"); len(calls) != 0 {
		t.Errorf("state rm ran: %v", calls)
	}
	if got := r.applies(t); len(got) != 1 {
		t.Errorf("applies = %v, want the ordinary apply", got)
	}
}

// M-4: a label call that did not answer stops --retire, and the refusal names
// the call. §3.1a's degradation is for the ordinary run; here the set drives
// state removals, and without the labels a live container of the repository
// that no job mounts is called KEPT -- and Kept is what Removals takes out of
// the state, so the container would silently lose Terraform's management while
// keeping its value and its labels. That is the false positive §3.1a forbids,
// so the run refuses before it computes anything, removes nothing, deletes
// nothing and confirms nothing -- and does not reach a prompt.
func TestInitRepoRetireRefusesAFailedLabelCall(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		fail       func(r *retireRig)
	}{
		{"the job list", "could not list the repository's Cloud Run jobs (run.jobs.list)", func(r *retireRig) { r.run.FailListWith = 403 }},
		{"the secrets list", "could not list the repository's secrets (secretmanager.secrets.list)", func(r *retireRig) { r.sm.FailListWith = 403 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRetireRig(t, retireOpts{protection: false, keptAsNote: true})
			tc.fail(r)
			// No typed confirmation is fed in: a refusal that reached the
			// first prompt would fail on EOF instead of on the message.
			out, errOut, err := executeStdin(t, "", "setup", "--repo", "--retire", "--no-build", "--yes", "--allow-delete", retireSvcAddr)
			if ExitCode(err) != ExitRemoteError {
				t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
			}
			msg := err.Error()
			for _, want := range []string{
				tc.want,
				"it will not run on a retirement set read without the labels",
				"the removal would release the live one from Terraform's management",
				"Nothing was removed from the state, nothing was deleted and nothing was confirmed.",
				"rerun without --retire",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal does not say %q:\n%v", want, err)
				}
			}
			if calls := r.ran(t, "state rm"); len(calls) != 0 {
				t.Errorf("state rm ran: %v", calls)
			}
			if got := r.ran(t, "plan"); len(got) != 0 {
				t.Errorf("the run planned anyway: %v", got)
			}
			if got := r.applies(t); len(got) != 0 {
				t.Errorf("the run applied anyway: %v", got)
			}
		})
	}
}

// The same two calls degrade loudly on the ordinary run, and the run goes on
// (design §3.1a, review round 3): discovery cannot union a container the
// configuration no longer computes back into the plan, and it says so rather
// than aborting the ordinary `fugaro init --repo` -- the same permission
// answers the retirement's own label call, which the run reports the same
// way. What is lost is the union, and the plan's own prevent_destroy refusal
// is what catches the consequence.
func TestInitRepoRefusedSecretsListDegradesInsteadOfAborting(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: false})
	r.sm.FailListWith = 403
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--no-build", "--yes", "--allow-delete", retireSvcAddr)
	if err != nil {
		t.Fatalf("a refused secrets.list stopped the run: exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	for _, want := range []string{
		"could not list the repository's secrets (secretmanager.secrets.list)",
		"cannot be adopted back into the plan",
		"gcloud secrets list --project " + initProject,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the degradation does not say %q:\n%s", want, text)
		}
	}
	if got := r.applies(t); len(got) == 0 {
		t.Errorf("the run stopped before the plan: applies = %v\n%s", got, text)
	}
}

// The state removal is between the two confirmations, so only the first of
// them asks for it (review round 3): by the second the run has already
// reported the removal, and re-asking for a step it has taken reads as a
// consent prompt for something that has happened. The second names what
// remains -- the apply and the API deletes -- and the count of what left the
// state as the fact it is.
func TestInitRepoRetireSecondConfirmationIsPastTense(t *testing.T) {
	newRetireRig(t, retireOpts{protection: true})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire", "--no-build")
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	var confirm []string
	for _, line := range strings.Split(out+errOut, "\n") {
		if strings.HasPrefix(line, "⚠ CONFIRM") {
			confirm = append(confirm, line)
		}
	}
	if len(confirm) != 2 {
		t.Fatalf("%d confirmations, want two:\n%s%s", len(confirm), out, errOut)
	}
	if !strings.Contains(confirm[0], "remove 2 from Terraform's state (destroying nothing), then apply") {
		t.Errorf("the pre-plan confirmation does not ask for the removal:\n%s", confirm[0])
	}
	if !strings.Contains(confirm[1], "already out of Terraform's state") {
		t.Errorf("the post-plan confirmation does not say the removal happened:\n%s", confirm[1])
	}
	if !strings.Contains(confirm[1], "what remains is the apply and the delete of 1 Cloud Run job") {
		t.Errorf("the post-plan confirmation does not name what remains:\n%s", confirm[1])
	}
	if strings.Contains(confirm[1], "remove 2") {
		t.Errorf("the post-plan confirmation re-asks for the removal:\n%s", confirm[1])
	}
}

// The gate refuses only on a deletion_protection the state actually carries
// (design §3.1, review round 3). Such a job is Protected all the same -- the
// removal set may not guess it unprotected -- so the run walks the ordinary
// path, and if the job really is protected the plan refuses it and §3.3
// explains that instead of a refusal that would have said "the state says
// deletion_protection = true" about an attribute the state does not carry.
func TestInitRepoRetireRefusalNeedsAValueTheStateCarried(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: false, jobNoProtection: true})
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--no-build", "--yes", "--allow-delete", retireSvcAddr)
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s%s", ExitCode(err), err, out, errOut)
	}
	text := out + errOut
	if strings.Contains(text, "that no apply can delete") {
		t.Errorf("a job whose state carries no deletion_protection was refused:\n%s", text)
	}
	if got := r.applies(t); len(got) != 1 {
		t.Errorf("applies = %v, want the ordinary apply", got)
	}
	if calls := r.ran(t, "state rm"); len(calls) != 0 {
		t.Errorf("the ordinary path removed state: %v", calls)
	}
}

// The retirement flow is entered once, and only from planRepo's first call
// (design Task 5, review D3): the run calls planRepo twice whenever it built
// an image -- the second time to deploy the freshly built images -- and by
// then the state removal has run and the orphan is still alive in the cloud,
// so a second entry would re-prompt for a confirmed transition and could
// attempt the same delete twice. This run is the one with builds left (no
// --no-build), so it goes past the retirement; the steps the user sees ran
// once each.
// The whole flow runs once in a run that has builds left: without
// --no-build the run goes on past the retirement (plan, apply, deploy), and
// nothing the retirement does may be done twice. The rig's fake cannot carry
// the run far enough to build an image -- the billable build needs a real
// Cloud Build behind it -- so the once-only assertions are the user-visible
// ones: the summary, the two typed confirmations, one state removal per
// address and one API delete.
func TestInitRepoRetireRunsTheFlowOnce(t *testing.T) {
	r := newRetireRig(t, retireOpts{protection: true})
	out, errOut, err := executeStdin(t, "aurora\naurora\n", "setup", "--repo", "--retire")
	text := out + errOut
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, text)
	}
	if n := strings.Count(text, "⚠ This change retires"); n != 1 {
		t.Errorf("the retirement summary was printed %d times, want once:\n%s", n, text)
	}
	if n := strings.Count(text, "⚠ CONFIRM"); n != 2 {
		t.Errorf("%d confirmations, want the two typed ones:\n%s", n, text)
	}
	// state rm takes one address per call, so the assertion is that each
	// address was removed exactly once -- the same address twice is what a
	// second entry into the flow would look like.
	seen := map[string]int{}
	for _, c := range r.ran(t, "state rm") {
		for _, arg := range c {
			if strings.HasPrefix(arg, "module.") {
				seen[arg]++
			}
		}
	}
	if len(seen) != 2 || seen[retireKeptAddr] != 1 || seen[retireJobAddr] != 1 {
		t.Errorf("state removals = %v, want each of the container and the job once", seen)
	}
	if deletes := r.run.JobDeletes(); len(deletes) != 1 || deletes[0] != retireJobName {
		t.Errorf("JobDeletes = %v, want exactly one delete of %s", deletes, retireJobName)
	}
}
