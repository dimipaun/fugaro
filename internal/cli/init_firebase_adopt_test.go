package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
)

// The four resources of the Firebase root's singletons, as the module
// creates them, and the address and ID each is imported at.
var fbAdoptImports = []infra.Import{
	{To: "module.firebase.google_apikeys_key.web", ID: "projects/aurora-fp/locations/global/keys/fugaro-web"},
	{To: "module.firebase.google_firebase_database_instance.this", ID: "projects/aurora-fp/locations/us-central1/instances/aurora-fp-default-rtdb"},
	{To: "module.firebase.google_project_iam_custom_role.token_minter", ID: "projects/aurora-fp/roles/fugaroTokenMinter"},
	{To: "module.firebase.google_service_account.signer", ID: "projects/aurora-fp/serviceAccounts/" + fpSigner},
}

// adoptExisting makes the Firebase project hold the four resources, as an
// earlier apply left them (the database is the rig's own).
func (r *fbRig) adoptExisting() {
	role := "projects/" + r.fp + "/roles/fugaroTokenMinter"
	r.keys.AddKey(r.fp, "fugaro-web", "Fugaro run sign-in", []string{"identitytoolkit.googleapis.com", "securetoken.googleapis.com"}, false, false)
	signer := "fugaro-token-signer@" + r.fp + ".iam.gserviceaccount.com"
	r.iam.AddServiceAccountFull(r.fp, signer, "Fugaro token signer", "Signs the custom tokens of the Fugaro project aurora's runs. Holds no roles.", false)
	r.iam.AddRole(r.fp, "fugaroTokenMinter", "Fugaro token minter", false)
	r.iam.SetRolePermissions(role, "iam.serviceAccounts.signJwt")
}

// fbPlan scripts show@firebase: a plan whose resource changes are the
// imports (importing, no-op) and creates, and a state that lists managed.
func (r *fbRig) fbPlan(t *testing.T, imports []infra.Import, creates, managed []string) {
	t.Helper()
	var changes []map[string]any
	for _, i := range imports {
		changes = append(changes, map[string]any{"address": i.To, "type": strings.Split(strings.TrimPrefix(i.To, "module.firebase."), ".")[0],
			"change": map[string]any{"actions": []string{"no-op"}, "before": map[string]any{}, "after": map[string]any{}, "importing": map[string]any{"id": i.ID}}})
	}
	for _, a := range creates {
		changes = append(changes, map[string]any{"address": a, "type": strings.Split(strings.TrimPrefix(a, "module.firebase."), ".")[0],
			"change": map[string]any{"actions": []string{"create"}, "before": nil, "after": map[string]any{}}})
	}
	var resources []map[string]any
	for _, a := range managed {
		resources = append(resources, map[string]any{"address": a})
	}
	doc := map[string]any{"format_version": "1.2", "resource_changes": changes,
		"values": map[string]any{"root_module": map[string]any{"child_modules": []any{map[string]any{"address": "module.firebase", "resources": resources}}}}}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	r.script["show@firebase"] = map[string]any{"stdout": string(b)}
	r.save(t)
}

var fbCreates = []string{"module.firebase.google_project_service.this[\"iam.googleapis.com\"]", "module.firebase.google_firebase_project.this"}

// fbImportsFile is the Firebase workdir's imports.tf.json, nil if absent.
func (r *fbRig) fbImportsFile(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.fbRoot(), infra.ImportsFile))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (r *fbRig) importRows(t *testing.T) []infra.Import {
	t.Helper()
	b := r.fbImportsFile(t)
	if b == nil {
		t.Fatal("no imports.tf.json in the Firebase workdir")
	}
	var doc struct {
		Import []infra.Import `json:"import"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return doc.Import
}

// fbRootCalls are the terraform subcommands run in the Firebase root.
func (r *fbRig) fbRootCalls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(r.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var subs []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var c tfCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		// version is the check every terraform handle makes when built.
		if len(c.Args) > 0 && c.Args[0] != "version" && filepath.Base(c.Dir) == "firebase" {
			subs = append(subs, c.Args[0])
		}
	}
	return subs
}

func (r *fbRig) runAdopt(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return r.runAdoptIn(t, names(3)+"us-east5\n"+names(1), args...)
}

func (r *fbRig) runAdoptIn(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	r.historyImage()
	fakeTerminal(t)
	out, _, err := executeStdin(t, stdin, append([]string{"init", "--firebase", r.fp}, args...)...)
	return out, err
}

// The four resources exist and are ours, and are not managed: the plan
// imports them, the output lists them, the confirmation names and counts
// them, and imports.tf.json holds exactly those rows.
func TestInitFirebaseAdoptFour(t *testing.T) {
	r := newFBRig(t)
	r.adoptExisting()
	r.fbPlan(t, fbAdoptImports, fbCreates, nil)
	out, err := r.runAdopt(t, "--budget-mode", "observe")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, i := range fbAdoptImports {
		if want := "import " + i.To + " (id " + i.ID + ")"; !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	for _, want := range []string{"applies 4 imports, 2 creates, 0 updates to the Firebase project aurora-fp (adopting the existing Realtime Database aurora-fp-default-rtdb, web API key fugaro-web, token signer and fugaroTokenMinter role;"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if got := r.importRows(t); !slices.Equal(got, fbAdoptImports) {
		t.Errorf("imports.tf.json rows\n got %v\nwant %v", got, fbAdoptImports)
	}
	if got := r.applies(t); !slices.Equal(got, []string{"installation", "firebase", "installation"}) {
		t.Errorf("applies %v", got)
	}
}

// A clean project writes an empty configuration and behaves as before.
func TestInitFirebaseAdoptCleanWritesEmpty(t *testing.T) {
	r := newFBRig(t)
	r.fbdb.RemoveInstances(r.fp)
	out, err := r.runAdopt(t, "--budget-mode", "observe")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(r.fbImportsFile(t))); got != "{}" {
		t.Errorf("imports.tf.json = %q, want {}", got)
	}
	if strings.Contains(out, "adopting the existing") {
		t.Errorf("the confirmation names imports on a clean project:\n%s", out)
	}
}

// A look-alike is refused with what was found and expected, before
// anything is written to the Firebase workdir or terraform runs there.
func TestInitFirebaseAdoptRefusal(t *testing.T) {
	r := newFBRig(t)
	r.adoptExisting()
	r.iam.SetRolePermissions("projects/"+r.fp+"/roles/fugaroTokenMinter", "iam.serviceAccounts.signJwt", "iam.serviceAccounts.getAccessToken")
	r.fbPlan(t, fbAdoptImports, fbCreates, nil)
	out, err := r.runAdopt(t, "--budget-mode", "observe")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	for _, want := range []string{"fugaroTokenMinter", "iam.serviceAccounts.getAccessToken", "iam.serviceAccounts.signJwt"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("no %q in %v", want, err)
		}
	}
	if b := r.fbImportsFile(t); b != nil {
		t.Errorf("imports.tf.json written: %s", b)
	}
	if _, err := os.Stat(filepath.Join(r.fbRoot(), infra.VarsFile)); !os.IsNotExist(err) {
		t.Errorf("tfvars written (stat err %v)", err)
	}
	if got := r.applies(t); !slices.Equal(got, []string{"installation"}) {
		t.Errorf("applies %v, want only the installation's", got)
	}
	if got := r.fbRootCalls(t); len(got) != 0 {
		t.Errorf("terraform ran in the Firebase root: %v", got)
	}
}

// A stale imports.tf.json (another project's, an earlier run's) is
// replaced by the empty configuration on a clean project. --plan-only
// ends at the installation's plan, before the Firebase root is touched
// (nothing is applied, so the stale file is never applied either); the
// next run replaces it.
func TestInitFirebaseAdoptStaleFileReplaced(t *testing.T) {
	r := newFBRig(t)
	r.fbdb.RemoveInstances(r.fp)
	if err := os.MkdirAll(r.fbRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := `{"import":[{"to":"module.firebase.google_apikeys_key.web","id":"projects/other-fp/locations/global/keys/fugaro-web"}]}`
	path := filepath.Join(r.fbRoot(), infra.ImportsFile)
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := r.runAdopt(t, "--plan-only"); err != nil || len(r.applies(t)) != 0 {
		t.Fatalf("--plan-only: %v, applies %v\n%s", err, r.applies(t), out)
	}
	out, err := r.runAdopt(t, "--budget-mode", "observe")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !slices.Contains(r.fbRootCalls(t), "init") {
		t.Fatalf("the Firebase root was never reached: %v\n%s", r.fbRootCalls(t), out)
	}
	if got := strings.TrimSpace(string(r.fbImportsFile(t))); got != "{}" {
		t.Errorf("imports.tf.json = %q, want {}", got)
	}
}

// Addresses the state already holds are not imported; a missing state is
// no error.
func TestInitFirebaseAdoptSkipsManaged(t *testing.T) {
	for name, tc := range map[string]struct {
		managed []string
		want    []infra.Import
	}{
		"all managed": {[]string{fbAdoptImports[0].To, fbAdoptImports[1].To, fbAdoptImports[2].To, fbAdoptImports[3].To, "module.firebase.google_firebase_project.this"}, nil},
		"one managed": {[]string{fbAdoptImports[0].To}, fbAdoptImports[1:]},
		"no state":    {nil, fbAdoptImports},
	} {
		r := newFBRig(t)
		r.adoptExisting()
		r.fbPlan(t, tc.want, fbCreates, tc.managed)
		out, err := r.runAdopt(t, "--budget-mode", "observe")
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		got := r.importRows(t)
		if len(tc.want) == 0 {
			if s := strings.TrimSpace(string(r.fbImportsFile(t))); s != "{}" {
				t.Errorf("%s: imports.tf.json = %q, want {}", name, s)
			}
		} else if !slices.Equal(got, tc.want) {
			t.Errorf("%s: rows\n got %v\nwant %v", name, got, tc.want)
		}
	}
}

// A rerun after an adopting apply imports nothing: the state lists the four.
func TestInitFirebaseAdoptRerunImportsNothing(t *testing.T) {
	r := newFBRig(t)
	r.adoptExisting()
	var managed []string
	for _, i := range fbAdoptImports {
		managed = append(managed, i.To)
	}
	r.fbPlan(t, nil, nil, managed)
	r.script["plan@firebase"] = map[string]any{"exit": 0}
	r.save(t)
	out, err := r.runAdoptIn(t, names(2)+"us-east5\n"+names(1), "--budget-mode", "observe")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(r.fbImportsFile(t))); got != "{}" {
		t.Errorf("imports.tf.json = %q, want {}", got)
	}
	if !strings.Contains(out, "No changes: the firebase root matches the plan.") {
		t.Errorf("no 'No changes' in\n%s", out)
	}
}

// The Firebase project's discovery reads are billed to it, not to the
// installation's project, when the two differ.
func TestQuotaOptions(t *testing.T) {
	r := newFBRig(t)
	lc := r.localConfig(t)
	if got := gcpOptions(lc).GCPProject; got != initProject {
		t.Fatalf("installation options quota %q", got)
	}
	if got := quotaOptions(lc, fpID); got.GCPProject != fpID || got.Region != lc.Region || got.Endpoints != gcpOptions(lc).Endpoints {
		t.Errorf("options %+v: want the Firebase project as quota and the rest unchanged", got)
	}
}
