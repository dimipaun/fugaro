package cli

// The shared project jobs issue #3686 retires, end to end through the one
// command the user runs: `fugaro update` deletes, through the Cloud Run API,
// the job of a (profile, provider, account) the configuration no longer
// declares, takes its instance out of Terraform's state before the plan that
// would otherwise be refused (the module hard-codes the job's
// deletion_protection, so no apply can delete it), and leaves the rest of the
// instance — its service account and its grants — to the apply that follows.
//
// The installation here is one at the release behind: its profile served
// team-a and now serves team-b, so the state still manages the team-a
// instance and the cloud still runs its job.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
)

// retireJobsAccountYAML is the installation's own local config for these
// tests: two accounts that cover the one repository, and a profile that lists
// team-b alone. team-a is declared, keeps its own claude_accounts entry and its
// own token — what moved is which account the profile names, which is the whole
// of what retires a job.
const retireJobsAccountYAML = `claude_accounts:
  team-a: { allow_data_to: [acme/app] }
  team-b: { allow_data_to: [acme/app] }
profiles:
  standard:
    provider: github
    cpu: 2
    memory: 8Gi
    timeout_class: 6h
    allow_repos: [github/acme/app]
    claude_accounts: [team-b]
`

// projectJobStateDoc is the installation root's state with one modules/project
// instance per name: the job (with the deletion_protection the module
// hard-codes), the service account it runs as, and one grant. Nothing states
// which triple an instance is for — the for_each key IS the job's Cloud Run
// name, which is what the retirement reads it from.
func projectJobStateDoc(t *testing.T, names ...string) string {
	t.Helper()
	var mods []map[string]any
	for _, name := range names {
		mod := `module.project["` + name + `"]`
		var res []map[string]any
		for _, r := range []struct {
			typ, nm string
			vals    map[string]any
		}{
			{"google_cloud_run_v2_job", "this", map[string]any{"name": name, "location": "us-east5", "deletion_protection": true}},
			{"google_service_account", "job", map[string]any{"account_id": name}},
			{"google_project_iam_member", "vertex", map[string]any{"role": "roles/aiplatform.user"}},
		} {
			res = append(res, map[string]any{
				"address": mod + "." + r.typ + "." + r.nm, "mode": "managed",
				"type": r.typ, "name": r.nm, "values": r.vals,
			})
		}
		mods = append(mods, map[string]any{"address": mod, "resources": res})
	}
	b, err := json.Marshal(map[string]any{
		"format_version": "1.0",
		"values":         map[string]any{"root_module": map[string]any{"child_modules": mods}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stateRemovals is every address this run handed `terraform state rm`, in
// order and once each: the calls carry terraform's own flags beside the
// addresses, and what these tests are about is the addresses.
func stateRemovals(t *testing.T, r *convergeRig) []string {
	t.Helper()
	var out []string
	for _, c := range r.calls(t) {
		if len(c) < 2 || c[0] != "state" || c[1] != "rm" {
			continue
		}
		for _, a := range c[2:] {
			if strings.HasPrefix(a, "-") || slices.Contains(out, a) {
				continue
			}
			out = append(out, a)
		}
	}
	return out
}

// seedAccountTokens stores a value for each named account's token container, in
// the project's own copy: the update's secrets stage asks for one value per name
// a job mounts, and a rig that left them out would stop on `fugaro accounts add`
// rather than on the step these tests are about. It says nothing about the jobs
// — a container is what an account needs, the job is what the profile declares.
func seedAccountTokens(t *testing.T, r *convergeRig, names ...string) {
	t.Helper()
	for _, name := range names {
		secret := config.ClaudeAccountSecret(name)
		r.sm.Seed(gcp.ProjectSecretID(initProjectName, secret), map[string]string{
			gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: initProjectName, gcp.LabelSecret: secret,
		}, []byte("token-"+name))
	}
}

// retireJobsRig is the installation of that shape: the state holds the instance
// of the job team-a's triple names and the one team-b's does, and the cloud runs
// both jobs, each wearing the labels modules/project/job.tf writes.
func retireJobsRig(t *testing.T) (r *convergeRig, gone, kept string) {
	t.Helper()
	r = newConvergeRigYAML(t, v7Node(), retireCopiesYAML)
	r.appendConfig(t, retireJobsAccountYAML)
	registerInstallation(t, r.initRig)
	seedAccountTokens(t, r, "team-a", "team-b")

	gone = gcp.ProjectJobName("standard", "github", "team-a")
	kept = gcp.ProjectJobName("standard", "github", "team-b")
	// One answer for every state read: the retirement removes the addresses
	// once, and a fixture whose second answer had them gone would say nothing
	// about the removal either way.
	r.script["show -json@installation"] = map[string]any{"stdout": projectJobStateDoc(t, gone, kept)}
	for name, account := range map[string]string{gone: "team-a", kept: "team-b"} {
		r.run.AddProjectJob(name, "standard", "github", account, "2", "8Gi")
		r.run.SetJob(name, map[string]string{
			gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProfile: "standard",
			"fugaro_provider": "github", "fugaro_account": account,
		}, "image")
	}
	r.save(t)
	return r, gone, kept
}

// TestUpdateRetiresAProjectJobTheConfigStoppedDeclaring is the acceptance end
// to end: one `fugaro update` on an installation whose profile moved from
// team-a to team-b deletes the team-a job through the Cloud Run API, takes its
// three addresses out of Terraform's state (the job alone is protected, so it
// is the job alone that leaves — the account and the grant are the apply's),
// leaves the team-b job and its own state rows exactly as they were, and says
// both halves in one line each.
func TestUpdateRetiresAProjectJobTheConfigStoppedDeclaring(t *testing.T) {
	r, gone, kept := retireJobsRig(t)

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	deletes := r.run.JobDeletes()
	if !slices.Contains(deletes, gone) {
		t.Errorf("deleted jobs = %v; want %s, the job of the triple the profile no longer names", deletes, gone)
	}
	if slices.Contains(deletes, kept) {
		t.Errorf("deleted jobs = %v; want %s kept: the configuration still declares its triple", deletes, kept)
	}
	if !r.run.HasJob(kept) {
		t.Errorf("the %s job is gone: a job the configuration still declares is not this step's to delete", kept)
	}

	// The state removal names the job's address alone: the service account and
	// the grant are not protected, so an ordinary apply deletes them and an
	// address taken out of the state is an address nothing destroys.
	wantGone := `module.project["` + gone + `"].google_cloud_run_v2_job.this`
	if removed := stateRemovals(t, r); !slices.Equal(removed, []string{wantGone}) {
		t.Errorf("terraform state rm was handed %v; want %s alone", removed, wantGone)
	}

	if !strings.Contains(out, "removed 1 retired project job from Terraform's state; nothing was destroyed") {
		t.Errorf("the run did not report the state removal:\n%s", out)
	}
	if !strings.Contains(out, "retired project jobs: 1 deleted, 0 left for a later update") {
		t.Errorf("the run did not report the retirement:\n%s", out)
	}
}

// TestUpdateKeepsAProjectJobARunIsInFlightOn is the rule the whole step rests
// on: deleting a job cancels the executions on it, and the run behind one is a
// person's work going away with no report anywhere. So a job with an execution
// that has not finished is left exactly as it is — its labels, its state rows
// and the job itself — and because its name is out of the document this run
// writes, the next update finds it in the cloud by those labels and retires it
// then.
//
// The state removal is not gated on that: the plan produces no address of a
// job the document no longer declares, so leaving the rows in would refuse the
// whole update on a job nobody may delete — the failure the step exists to
// close, one update older.
func TestUpdateKeepsAProjectJobARunIsInFlightOn(t *testing.T) {
	r, gone, kept := retireJobsRig(t)
	r.run.Start(gone)

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if d := r.run.JobDeletes(); len(d) != 0 {
		t.Errorf("deleted jobs = %v; want none: a run in flight is on %s", d, gone)
	}
	for _, name := range []string{gone, kept} {
		if !r.run.HasJob(name) {
			t.Errorf("the %s job is gone; want a job a run is on left exactly as it is", name)
		}
	}
	if !strings.Contains(out, "a run in flight is on it, so it is not destroyed; a later update retires it") {
		t.Errorf("the run did not say why the job stayed:\n%s", out)
	}
	if !strings.Contains(out, "retired project jobs: 0 deleted, 1 left for a later update") {
		t.Errorf("the run did not report the job it left:\n%s", out)
	}
	// The rows still leave: the plan no longer produces them, and a row left
	// in the state is a plan that deletes a protected job — the whole update
	// refused, on a job this run may not delete either.
	wantGone := `module.project["` + gone + `"].google_cloud_run_v2_job.this`
	if removed := stateRemovals(t, r); !slices.Equal(removed, []string{wantGone}) {
		t.Errorf("terraform state rm was handed %v; want %s alone", removed, wantGone)
	}
}

// TestUpdateKeepsEveryProjectJobWhenTheirListingFails: "could not look" and
// "nothing is there" must never be the same answer for a call that deletes a
// job. A listing this run could not make deletes nothing, one update older than
// the run that may be on one, and the names stay where they are for the next
// update to find again.
func TestUpdateKeepsEveryProjectJobWhenTheirListingFails(t *testing.T) {
	r, gone, kept := retireJobsRig(t)
	r.run.FailListWith = 403

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if d := r.run.JobDeletes(); len(d) != 0 {
		t.Errorf("deleted jobs = %v; want nothing from a run that could not read the installation's jobs", d)
	}
	for _, name := range []string{gone, kept} {
		if !r.run.HasJob(name) {
			t.Errorf("the %s job is gone; want it left alone", name)
		}
	}
	if !strings.Contains(out, "the installation's shared Cloud Run jobs could not be listed") {
		t.Errorf("the run did not say the jobs could not be read:\n%s", out)
	}
	if !strings.Contains(out, "gcloud run jobs list") {
		t.Errorf("the run did not name the call that answers the read by hand:\n%s", out)
	}
}

// TestUpdateLeavesAProjectJobItsConfigStillDeclares: the other direction, and
// the one a step that read the cloud alone would get wrong. Both instances are
// in the state, both jobs are running, and the document declares team-b's
// triple — so nothing is deleted and nothing leaves the state. Without this a
// retirement that fired on every live job would read as the feature.
func TestUpdateLeavesAProjectJobItsConfigStillDeclares(t *testing.T) {
	r := newConvergeRigYAML(t, v7Node(), retireCopiesYAML)
	// The profile still serves team-a: the whole axis is declared, so no job of
	// it is an orphan.
	r.appendConfig(t, strings.Replace(retireJobsAccountYAML, "claude_accounts: [team-b]", "claude_accounts: [team-a, team-b]", 1))
	registerInstallation(t, r.initRig)
	seedAccountTokens(t, r, "team-a", "team-b")

	live := gcp.ProjectJobName("standard", "github", "team-a")
	r.script["show -json@installation"] = map[string]any{"stdout": projectJobStateDoc(t, live)}
	r.run.AddProjectJob(live, "standard", "github", "team-a", "2", "8Gi")
	r.run.SetJob(live, map[string]string{
		gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProfile: "standard",
		"fugaro_provider": "github", "fugaro_account": "team-a",
	}, "image")
	r.save(t)

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if d := r.run.JobDeletes(); len(d) != 0 {
		t.Errorf("deleted jobs = %v; want none: the profile names that triple", d)
	}
	if !r.run.HasJob(live) {
		t.Errorf("the %s job is gone: the configuration still declares its triple", live)
	}
	for _, c := range r.calls(t) {
		if len(c) > 1 && c[0] == "state" && c[1] == "rm" {
			t.Errorf("terraform was handed %v; want no state removal for a job the config declares", c)
		}
	}
	if strings.Contains(out, "retired project jobs:") {
		t.Errorf("the run reported a retirement with nothing to retire:\n%s", out)
	}
}

// TestUpdateDoesNotRetireAProjectJobInASession is the half of the rule D2097
// puts on every cloud write: a coding agent's session runs no update at all, so
// the job stays, the state rows stay, and the refusal comes before either is
// read. The delete is the one call in this file nothing can take back, which is
// why it is asserted on its own here.
func TestUpdateDoesNotRetireAProjectJobInASession(t *testing.T) {
	r, gone, _ := retireJobsRig(t)
	t.Setenv(agentMarkers[0], "1")

	out, errOut, err := execute(t, "update")
	if err == nil {
		t.Fatalf("an update ran in a coding agent's session:\n%s\n%s", out, errOut)
	}
	if d := r.run.JobDeletes(); len(d) != 0 {
		t.Errorf("deleted jobs = %v; want none from a session: %s is still there", d, gone)
	}
	if !r.run.HasJob(gone) {
		t.Errorf("the %s job is gone under a refused run", gone)
	}
	for _, c := range r.calls(t) {
		if len(c) > 1 && c[0] == "state" && c[1] == "rm" {
			t.Errorf("terraform was handed %v under a refused run", c)
		}
	}
}
