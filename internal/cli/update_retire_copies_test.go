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
// container's life is the job's), a project copy of a name nobody copied
// (untouched: a project_secret whose owner emptied its allow-list has exactly
// this shape from the configuration's side), and the record itself, which is
// what drives the whole step and is not this step's to change.

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/intake"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// retireCopiesYAML is acme/app at the release that no longer derives a
// project-wide container per `workflow_secrets` entry (IS6), with neither
// workflow naming a secret: what this test is about is the copies the
// installation already holds and the record it already has, not the mounts a
// repository's jobs carry.
const retireCopiesYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { commands: { build: sh build.sh, test: sh test.sh } }\n" +
	"  api: { commands: { build: sh build.sh, test: sh test.sh } }\n"

// retiredCopiesRig is the converge rig of that installation: the record IS5
// left holds the two names whose value is in the fugaro_p_<name> grammar, the
// module's project-wide container exists for three names and the state manages
// all three, and a deployed job still mounts one of the two.
func retiredCopiesRig(t *testing.T) (r *convergeRig, db creds.DB, goneID, keptID string) {
	t.Helper()
	r = newConvergeRigYAML(t, v7Node(), retireCopiesYAML)

	// The grammar: the names a copy may be retired for, with the marker the
	// write path puts on a workflow's own value (G2) and one enabled version.
	for _, name := range []string{"gone-token", "kept-token"} {
		id, err := secretname.Project(name)
		if err != nil {
			t.Fatal(err)
		}
		r.sm.Seed(id, map[string]string{intake.LabelWorkflowSecret: intake.WorkflowSecretMarker}, []byte("grammar-"+name))
	}
	// The record the release behind left: the two names whose value is in the
	// grammar, and only those. uncopied-token is the copy of a name nobody
	// copied, which this step must not touch.
	db, err := newBudgetClient(context.Background(), r.db.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.WriteProjectSecrets(context.Background(), db, func(creds.ProjectSecrets, bool) (creds.ProjectSecrets, error) {
		rec := creds.ProjectSecrets{Version: creds.ProjectSecretsVersion}
		rec = rec.WithRule("gone-token", creds.ProjectSecretRule{Repos: []string{"acme/*"}})
		return rec.WithRule("kept-token", creds.ProjectSecretRule{Repos: []string{"acme/*"}}), nil
	}); err != nil {
		t.Fatal(err)
	}

	// The module's project-wide containers, and the state that manages them
	// beside one resource of the root that has nothing to do with any of it.
	goneID = r.seedProjectKey(t, "gone-token")
	keptID = r.seedProjectKey(t, "kept-token")
	r.seedProjectKey(t, "uncopied-token")
	goneAddr := `module.installation.google_secret_manager_secret.project["gone-token"]`
	keptAddr := `module.installation.google_secret_manager_secret.project["kept-token"]`
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
	full := stateDoc(goneAddr, keptAddr, `module.installation.google_secret_manager_secret.project["uncopied-token"]`, "module.installation.google_cloud_run_v2_job.coordinator[0]")
	after := stateDoc(`module.installation.google_secret_manager_secret.project["uncopied-token"]`, "module.installation.google_cloud_run_v2_job.coordinator[0]")
	r.script["show -json@installation"] = []map[string]any{
		{"stdout": full}, {"stdout": full}, {"stdout": after},
	}

	// A deployed job of the release behind mounts kept-token's copy: its
	// template names the container, so the copy stays alive for that run (G4).
	r.run.AddJob("fugaro-acme-app-app", "2", "4Gi")
	r.run.SetJobSecretRefs("fugaro-acme-app-app", map[string]string{"DEPLOY_TOKEN": keptID})
	r.save(t)
	return r, db, goneID, keptID
}

// TestUpdateRetiresTheProjectCopiesItCopied is the acceptance end to end: one
// `fugaro update` of an installation whose configuration has just stopped
// deriving the project-wide containers leaves the copies D3325 retires gone,
// the copy a deployed job still names marked retired and alive, every other
// project container exactly as it was, and Terraform's state free of the
// instances the plan no longer produces.
func TestUpdateRetiresTheProjectCopiesItCopied(t *testing.T) {
	r, db, goneID, keptID := retiredCopiesRig(t)
	uncopiedID := gcp.ProjectSecretID(initProjectName, "uncopied-token")
	keptGrammar, err := secretname.Project("kept-token")
	if err != nil {
		t.Fatal(err)
	}

	out := convergeOn(t, r, []string{"policy-rules-skew", "plugin-pin"})

	if d := r.sm.Deleted(); !slices.Contains(d, goneID) || slices.Contains(d, keptID) || slices.Contains(d, uncopiedID) {
		t.Fatalf("deleted = %v; want %s alone: a copy a deployed job still names, and one of a name the grammar does not hold, are not this run's to delete", d, goneID)
	}
	// The kept copy is marked retired: the logical name is on it in place of
	// the label discovery adopts a project-wide container by, which is what
	// lets the plan drop the key at all.
	wantKept := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: initProjectName, gcp.LabelRetired: "kept-token"}
	if got := r.sm.Labels(keptID); !maps.Equal(got, wantKept) {
		t.Errorf("the kept copy carries %v; want %v", got, wantKept)
	}
	if p := r.sm.LabelPatches(); !slices.Contains(p, keptID) {
		t.Errorf("the marked containers are %v; want %s relabelled rather than rewritten", p, keptID)
	}
	// The name nobody copied keeps its container, its labels and its value.
	wantUncopied := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelProject: initProjectName, gcp.LabelSecret: "uncopied-token"}
	if got := r.sm.Labels(uncopiedID); !maps.Equal(got, wantUncopied) {
		t.Errorf("the uncopied name's copy carries %v; want %v", got, wantUncopied)
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
	// configuration no longer produces — once, on the pass that read the state
	// still holding them.
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
			if strings.Contains(a, "uncopied-token") || strings.Contains(a, "coordinator") {
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
