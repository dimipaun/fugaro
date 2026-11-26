package cli

// The project copies D3325 retires (IS13), end to end through the one command
// the user runs: `fugaro update` labels a copied copy retired, deletes it on a
// run whose own read found no deployed job naming it (G4), leaves a name the
// grammar does not hold entirely alone, and takes the instances out of
// Terraform's state before the plan that would otherwise be refused by the
// module's prevent_destroy (U5).
//
// The states below are the acceptance's own: a copied copy nothing names
// (marked, deleted, out of the state), a copied copy a deployed job of the
// release behind still names (marked and kept alive, and its state row removed
// all the same — the plan produces no container for it any more, and the
// container's life is the job's), a copied copy the installation's OWN
// configuration still plans (untouched: the release did not drop it here —
// team-a-api-key is a reserved project secret, so the installation derives its
// container whatever the workflow-secret class does), a project copy of a name
// nobody copied (untouched: a project_secret whose owner emptied its
// allow-list has exactly this shape from the configuration's side), and the
// record itself, which is what drives the whole step and is not this step's to
// change.
//
// Both tests pin the release-level switch the whole step hangs on
// (infra.ProjectCopiesDropped): with it off — every release before IS6 — none
// of that happens on any installation, and with it on — this release, which
// removed the workflow_secrets loop — it does. The pair of tests is what says
// which side of it an installation is on, and neither reads the production
// value.

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// retireCopiesYAML is acme/app at the release that no longer derives a
// project-wide container per `workflow_secrets` entry (IS6), with neither
// workflow naming a secret: what these tests are about is the copies the
// installation already holds and the record it already has, not the mounts a
// repository's jobs carry.
const retireCopiesYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { commands: { build: sh build.sh, test: sh test.sh } }\n" +
	"  api: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// retiredCopiesRig is the converge rig of that installation: the record IS5
// left holds three names whose value is in the fugaro_p_<name> grammar, the
// module's project-wide container exists for four and the state manages all
// four, a deployed job still mounts one of the three, and the installation's
// own configuration plans one of them again (team-a-api-key, the reserved
// project secret whose container projectSecretContainers still makes
// eligible).
func retiredCopiesRig(t *testing.T) (r *convergeRig, db creds.DB, goneID, keptID, plannedID string) {
	t.Helper()
	r = newConvergeRigYAML(t, v7Node(), retireCopiesYAML)
	// The installation's own local config, which is what makes team-a-api-key
	// the name a run leaves alone: a model provider's key is one of the
	// project-wide containers projectSecretContainers derives whatever the
	// workflow-secret class does, so the release dropped nothing for this
	// operator (IS6 takes that class off the function and leaves providers
	// alone). registerInstallation copies the file the rig appends to into the
	// project file the update door reads.
	r.appendConfig(t, "providers:\n  team-a:\n    kind: anthropic-compat\n    base_url: https://openrouter.invalid/api\n    auth: bearer\n    secret: team-a-api-key\n    models: [\"deepseek/*\"]\n")
	registerInstallation(t, r.initRig)

	// The grammar: the names a copy may be retired for, with the marker the
	// write path puts on a workflow's own value (G2) and one enabled version.
	copied := []string{"gone-token", "kept-token", "team-a-api-key"}
	for _, name := range copied {
		id, err := secretname.Project(name)
		if err != nil {
			t.Fatal(err)
		}
		r.sm.Seed(id, map[string]string{intake.LabelWorkflowSecret: intake.WorkflowSecretMarker}, []byte("grammar-"+name))
	}
	// The record the release behind left: the three names whose value is in the
	// grammar, and only those. uncopied-token is the copy of a name nobody
	// copied, which this step must not touch.
	db, err := newBudgetClient(context.Background(), r.db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.WriteProjectSecrets(context.Background(), db, func(creds.ProjectSecrets, bool) (creds.ProjectSecrets, error) {
		rec := creds.ProjectSecrets{Version: creds.ProjectSecretsVersion}
		for _, name := range copied {
			rec = rec.WithRule(name, creds.ProjectSecretRule{Repos: []string{"acme/*"}})
		}
		return rec, nil
	}); err != nil {
		t.Fatal(err)
	}

	// The module's project-wide containers, and the state that manages them
	// beside one resource of the root that has nothing to do with any of it.
	goneID = r.seedProjectKey(t, "gone-token")
	keptID = r.seedProjectKey(t, "kept-token")
	plannedID = r.seedProjectKey(t, "team-a-api-key")
	r.seedProjectKey(t, "uncopied-token")
	goneAddr := `module.installation.google_secret_manager_secret.project["gone-token"]`
	keptAddr := `module.installation.google_secret_manager_secret.project["kept-token"]`
	plannedAddr := `module.installation.google_secret_manager_secret.project["team-a-api-key"]`
	uncopiedAddr := `module.installation.google_secret_manager_secret.project["uncopied-token"]`
	coordinator := "module.installation.google_cloud_run_v2_job.coordinator[0]"
	stateDoc := func(addrs ...string) string {
		var res []map[string]any
		for _, a := range addrs {
			res = append(res, map[string]any{"address": a, "type": "x", "values": map[string]any{}})
		}
		b, err := json.Marshal(map[string]any{"format_version": "1.0", "values": map[string]any{"root_module": map[string]any{"resources": res}}})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// Three steps, because this command reads the installation root's state
	// more than once: the first is the read that precedes the converge's own
	// passes, the second is the state the removal is made from, and the third
	// is what that removal left — a run converges by naming nothing on the
	// passes after it, and a fixture that answered the same state every time
	// would have each pass remove the same addresses again.
	full := stateDoc(goneAddr, keptAddr, plannedAddr, uncopiedAddr, coordinator)
	after := stateDoc(plannedAddr, uncopiedAddr, coordinator)
	r.script["show -json@installation"] = []map[string]any{
		{"stdout": full}, {"stdout": full}, {"stdout": after},
	}

	// A deployed job of the release behind mounts kept-token's copy: its
	// template names the container, so the copy stays alive for that run (G4).
	r.run.AddJob("fugaro-acme-app-app", "2", "4Gi")
	r.run.SetJobSecretRefs("fugaro-acme-app-app", map[string]string{"DEPLOY_TOKEN": keptID})
	r.save(t)
	return r, db, goneID, keptID, plannedID
}

// dropProjectCopies puts this process on the release that no longer derives the
// containers (ProjectCopiesDropped): the state IS6 ships in, forced so that
// these tests say what each side of the switch does whatever the production
// default is.
func dropProjectCopies(t *testing.T, dropped bool) {
	t.Helper()
	prev := infra.ProjectCopiesDropped
	infra.ProjectCopiesDropped = dropped
	t.Cleanup(func() { infra.ProjectCopiesDropped = prev })
}

// TestUpdateRetiresTheProjectCopiesItCopied is the acceptance end to end: one
// `fugaro update`, at the release that no longer derives the project-wide
// containers, leaves the copies D3325 retires gone, the copy a deployed job
// still names marked retired and alive, the copy its own configuration still
// plans — team-a-api-key, eligible as the reserved project secret it is —
// exactly as it was, and Terraform's state free of the instances the plan no
// longer produces.
func TestUpdateRetiresTheProjectCopiesItCopied(t *testing.T) {
	r, db, goneID, keptID, plannedID := retiredCopiesRig(t)
	dropProjectCopies(t, true)
	uncopiedID := gcp.ProjectSecretID(initProjectName, "uncopied-token")
	keptGrammar, err := secretname.Project("kept-token")
	if err != nil {
		t.Fatal(err)
	}

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if d := r.sm.Deleted(); !slices.Contains(d, goneID) || slices.Contains(d, keptID) || slices.Contains(d, uncopiedID) || slices.Contains(d, plannedID) {
		t.Fatalf("deleted = %v; want %s alone: a copy a deployed job still names, one of a name the grammar does not hold, and one the configuration still plans are not this run's to delete", d, goneID)
	}
	// The kept copy is marked retired: the logical name is on it in place of
	// the label discovery adopts a project-wide container by, which is what
	// lets the plan drop the key at all.
	wantKept := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: initProjectName, gcp.LabelRetired: "kept-token"}
	if got := r.sm.Labels(keptID); !maps.Equal(got, wantKept) {
		t.Errorf("the kept copy carries %v; want %v", got, wantKept)
	}
	if p := r.sm.LabelPatches(); !slices.Contains(p, keptID) || slices.Contains(p, plannedID) {
		t.Errorf("the marked containers are %v; want %s relabelled rather than rewritten, and the planned copy's labels untouched", p, keptID)
	}
	// The name nobody copied keeps its container, its labels and its value; so
	// does the one the installation's own configuration still plans.
	for id, name := range map[string]string{uncopiedID: "uncopied-token", plannedID: "team-a-api-key"} {
		want := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: initProjectName, gcp.LabelSecret: name}
		if got := r.sm.Labels(id); !maps.Equal(got, want) {
			t.Errorf("the %s copy carries %v; want %v", name, got, want)
		}
	}
	// The grammar is not this step's: the value a run reads is where it was.
	if v := r.sm.Latest(keptGrammar); string(v) != "grammar-kept-token" {
		t.Errorf("the %s container reads %q; want the grammar left alone", keptGrammar, v)
	}

	// One line for the person, and the state removal's own.
	if !strings.Contains(out, "retired workflow-secret copies: 2 marked, 1 deleted (1 left alive)") {
		t.Errorf("the run did not report the retirement:\n%s", out)
	}
	if !strings.Contains(out, "removed 2 retired workflow-secret copies from Terraform's state; nothing was destroyed") {
		t.Errorf("the run did not report the state removal:\n%s", out)
	}
	// The removal is one call per address, and it names exactly the two the
	// plan no longer produces — once, on the pass that read the state still
	// holding them.
	wantRemoved := []string{
		`module.installation.google_secret_manager_secret.project["gone-token"]`,
		`module.installation.google_secret_manager_secret.project["kept-token"]`,
	}
	var removed [][]string
	for _, c := range r.calls(t) {
		if len(c) > 1 && c[0] == "state" && c[1] == "rm" {
			removed = append(removed, c)
		}
	}
	if len(removed) != len(wantRemoved) {
		t.Fatalf("terraform state rm was called %d times (%v); want once per retired copy", len(removed), removed)
	}
	for _, addr := range wantRemoved {
		if !slices.ContainsFunc(removed, func(c []string) bool { return slices.Contains(c, addr) }) {
			t.Errorf("the state removal never named %s: %v", addr, removed)
		}
	}
	for _, c := range removed {
		for _, a := range c {
			if strings.Contains(a, "uncopied-token") || strings.Contains(a, "team-a-api-key") || strings.Contains(a, "coordinator") {
				t.Errorf("the state removal named %s, which this run did not retire: %v", a, c)
			}
		}
	}

	// The record is what drives this step and it is not this step's to change:
	// an update that dropped the entry would retire nothing on the next one.
	rec, found, err := creds.ReadProjectSecrets(context.Background(), db)
	if err != nil || !found {
		t.Fatalf("reading %s after the run: %+v, %v", creds.PathProjectSecrets(), rec, err)
	}
	if _, held := rec.Rule("kept-token"); !held {
		t.Errorf("the record after the run holds %v; want the entry the retirement is driven by", rec.Secrets)
	}
}

// TestUpdateKeepsTheCopiesWhenTheJobsReadFails is G4's fail-closed half, end to
// end through the door: a run that could not read the installation's jobs
// cannot see what still mounts a copy, so it deletes none of them. Every copy
// is marked retired — which is what lets the plan drop the key, and what the
// next update reads back — and every container stays alive, one update older
// than the in-flight run that mounts it (G4). "Could not look" and "nothing
// names it" must never be the same answer for a call that deletes a container.
//
// The state half is not gated on that read: the plan produces no container for
// these names whatever the jobs say, so prevent_destroy would refuse the apply
// if their instances stayed in it.
func TestUpdateKeepsTheCopiesWhenTheJobsReadFails(t *testing.T) {
	r, _, goneID, keptID, _ := retiredCopiesRig(t)
	dropProjectCopies(t, true)
	// The whole installation's jobs, not one job: this is the read G4 rests on.
	r.run.FailListWith = http.StatusForbidden

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if d := r.sm.Deleted(); len(d) != 0 {
		t.Errorf("deleted = %v; want nothing from a run that could not read the jobs", d)
	}
	for id, name := range map[string]string{goneID: "gone-token", keptID: "kept-token"} {
		want := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: initProjectName, gcp.LabelRetired: name}
		if got := r.sm.Labels(id); !maps.Equal(got, want) {
			t.Errorf("the %s copy carries %v; want %v: a retired copy stays alive until an update can see that nothing names it", name, got, want)
		}
	}
	if !strings.Contains(out, "the Cloud Run jobs could not be read") || !strings.Contains(out, "gcloud run jobs list") {
		t.Errorf("the run did not say the jobs could not be read, nor the call that answers it:\n%s", out)
	}
	if !strings.Contains(out, "retired workflow-secret copies: 2 marked, 0 deleted (2 left alive)") {
		t.Errorf("the run did not report the retirement:\n%s", out)
	}
	wantRemoved := []string{
		`module.installation.google_secret_manager_secret.project["gone-token"]`,
		`module.installation.google_secret_manager_secret.project["kept-token"]`,
	}
	var removed [][]string
	for _, c := range r.calls(t) {
		if len(c) > 1 && c[0] == "state" && c[1] == "rm" {
			removed = append(removed, c)
		}
	}
	if len(removed) != len(wantRemoved) {
		t.Fatalf("terraform state rm was called %d times (%v); want once per retired copy", len(removed), removed)
	}
	for _, addr := range wantRemoved {
		if !slices.ContainsFunc(removed, func(c []string) bool { return slices.Contains(c, addr) }) {
			t.Errorf("the state removal never named %s: %v", addr, removed)
		}
	}
}

// TestUpdateRetiresNothingBeforeTheReleaseDropsTheKeys is the other side of the
// switch, and the reading of the old rule it replaced: the release that still
// derives the containers decides nothing from an operator's own configuration,
// so an installation whose operator happens to plan none of the copied names
// retires none of them either. Nothing is marked, nothing is deleted and no row
// leaves Terraform's state until the release that drops the keys is the one
// running.
func TestUpdateRetiresNothingBeforeTheReleaseDropsTheKeys(t *testing.T) {
	r, db, _, _, _ := retiredCopiesRig(t)
	dropProjectCopies(t, false)

	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	if d := r.sm.Deleted(); len(d) != 0 {
		t.Errorf("deleted = %v; want nothing at the release that still derives the containers", d)
	}
	if p := r.sm.LabelPatches(); len(p) != 0 {
		t.Errorf("marked containers = %v; want nothing at the release that still derives the containers", p)
	}
	for id, name := range map[string]string{
		gcp.ProjectSecretID(initProjectName, "gone-token"):     "gone-token",
		gcp.ProjectSecretID(initProjectName, "kept-token"):     "kept-token",
		gcp.ProjectSecretID(initProjectName, "team-a-api-key"): "team-a-api-key",
		gcp.ProjectSecretID(initProjectName, "uncopied-token"): "uncopied-token",
	} {
		want := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: initProjectName, gcp.LabelSecret: name}
		if got := r.sm.Labels(id); !maps.Equal(got, want) {
			t.Errorf("the %s copy carries %v; want %v", name, got, want)
		}
	}
	for _, c := range r.calls(t) {
		if len(c) > 1 && c[0] == "state" && c[1] == "rm" {
			t.Errorf("terraform was handed %v; want no state removal before the release drops the keys", c)
		}
	}
	for _, line := range []string{"retired workflow-secret copies:", "removed 2 retired workflow-secret"} {
		if strings.Contains(out, line) {
			t.Errorf("the run reported %q at the release that still derives the containers:\n%s", line, out)
		}
	}
	// The record is left as it was here too: this step is not what changes it.
	rec, found, err := creds.ReadProjectSecrets(context.Background(), db)
	if err != nil || !found || len(rec.Secrets) != 3 {
		t.Fatalf("the record after the run: %+v (found %v, err %v)", rec, found, err)
	}
}
