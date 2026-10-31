package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	// fpID is the installation's own GCP project: Fugaro only starts fresh,
	// and the budget backend lives in the project setup created (D7), never in
	// a Firebase project of another id.
	fpID             = initProject
	fpSigner         = "fugaro-token-signer@" + initProject + ".iam.gserviceaccount.com"
	fpAPIKey         = "AIzaSyA0123456789abcdefghijklmnopqrstu"
	historyAccount   = "fugaro-history@proj-1234.iam.gserviceaccount.com"
	historyImagePath = "us-east5-docker.pkg.dev/proj-1234/" + infra.BaseRegistry + "/" + infra.HistoryImagePackage + ":latest"
)

// fbRig is the init rig plus the fakes of update --firebase-project: Cloud Billing, the
// project's Realtime Database, and a Firebase project with billing.
type fbRig struct {
	*initRig
	billing *gcpfake.Billing
	db      *gcpfake.RTDB
	fbdb    *gcpfake.FirebaseDB
	idt     *gcpfake.IdentityToolkit
	fs      *gcpfake.Firestore
	rules   *gcpfake.FirebaseRules
	keys    *gcpfake.APIKeys
	fp      string
}

func newFBRig(t *testing.T) *fbRig { return newFBRigFor(t, fpID) }

// newFBRigFor is the rig with the Firebase project fp: another project than
// the installation's (the two-project layout), or initProject itself.
func newFBRigFor(t *testing.T, fp string) *fbRig {
	t.Helper()
	typedThroughYes(t)
	r := &fbRig{initRig: newInitRig(t), billing: gcpfake.NewBilling(t), db: gcpfake.NewRTDB(t), fbdb: gcpfake.NewFirebaseDB(t), fp: fp}
	r.idt = gcpfake.NewIdentityToolkit(t, nil, "key", fp)
	r.fs, r.rules = gcpfake.NewFirestore(t), gcpfake.NewFirebaseRules(t, fp)
	r.fs.RemoveDatabase() // a fresh Firebase project has none
	r.keys = gcpfake.NewAPIKeys(t)
	r.fbdb.AddInstanceFull(fp, "us-central1", fp+"-default-rtdb", "DEFAULT_DATABASE", "ACTIVE", r.db.URL)
	r.stateBucket()
	if fp != initProject {
		r.crm.AddProject(fp, 987654321098)
	}
	r.billing.SetBilling(fp, true)
	r.crm.SetPolicy(initProject,
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com", "group:owners@example.com", "domain:example.com"}},
		gcpfake.Binding{Role: "roles/editor", Members: []string{"user:editor@example.com", "serviceAccount:123456789012-compute@developer.gserviceaccount.com",
			"serviceAccount:123456789012@cloudservices.gserviceaccount.com", "deleted:user:gone@example.com?uid=1"}},
		gcpfake.Binding{Role: "roles/viewer", Members: []string{"user:viewer@example.com"}},
	)
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg := strings.Replace(string(b), "no_auth: true }", "cloud_billing: "+r.billing.URL+"/, firebase_database: "+r.fbdb.URL+"/, api_keys: "+r.keys.URL+"/, identity_toolkit: "+r.idt.URL+"/, firestore: "+r.fs.URL+", firebase_rules: "+r.rules.URL+", no_auth: true }", 1)
	if err := os.WriteFile(r.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"history_service_account": historyAccount, "history_job": nil})}
	r.script["output@firebase"] = map[string]any{"stdout": r.firebaseOutputs(t)}
	r.save(t)
	return r
}

func (r *fbRig) firebaseOutputs(t *testing.T) string {
	t.Helper()
	out := map[string]any{}
	for k, v := range map[string]string{"rtdb_url": r.db.URL, "firebase_api_key": fpAPIKey, "token_signer": "fugaro-token-signer@" + r.fp + ".iam.gserviceaccount.com", "firebase_project": r.fp} {
		out[k] = map[string]any{"value": v, "type": "string", "sensitive": false}
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// historyImage makes the history image's latest tag exist in the base
// registry.
func (r *fbRig) historyImage() {
	r.ar.AddRepository(initProject, "us-east5", infra.BaseRegistry, map[string]string{"fugaro": "managed"})
	r.ar.SetTag(initProject, "us-east5", infra.BaseRegistry, infra.HistoryImagePackage, "latest")
}

// names is the confirmation typed n times.
func names(n int) string { return strings.Repeat(initProjectName+"\n", n) }

type tfCall struct {
	Args []string `json:"args"`
	Dir  string   `json:"dir"`
}

// applies are the roots terraform applied in, in order.
func (r *initRig) applies(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(r.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var roots []string
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		var c tfCall
		if err := json.Unmarshal(line, &c); err != nil {
			t.Fatal(err)
		}
		if len(c.Args) > 0 && c.Args[0] == "apply" {
			roots = append(roots, filepath.Base(c.Dir))
		}
	}
	return roots
}

func (r *fbRig) fbRoot() string {
	return filepath.Join(os.Getenv("XDG_STATE_HOME"), "fugaro", "terraform", initProject, "firebase", "gcp", "roots", "firebase")
}

func (r *fbRig) fbVars(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.fbRoot(), infra.VarsFile))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (r *fbRig) localConfig(t *testing.T) *localcfg.Config {
	t.Helper()
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

func strs(v any) []string {
	var out []string
	for _, e := range v.([]any) {
		out = append(out, e.(string))
	}
	return out
}

// Each of the three applies, and the database step, has its own plan and its
// own confirmation: typing the name n times gets exactly n steps.
func TestInitFirebaseThreeAppliesConfirmedSeparately(t *testing.T) {
	r := newFBRig(t)
	r.historyImage()
	fakeTerminal(t)
	out, _, err := executeStdin(t, names(3)+"us-east5\n"+names(1), "setup", "--firebase-project", fpID, "--budget-mode", "observe")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, want := r.applies(t), []string{"installation", "firebase", "installation"}; !slices.Equal(got, want) {
		t.Fatalf("applies in %v, want %v", got, want)
	}
	if n := strings.Count(out, "⚠ CONFIRM"); n != 5 {
		t.Errorf("%d confirmations, want 5 (three applies, the database and its location):\n%s", n, out)
	}
	for _, want := range []string{"to the Firebase project proj-1234", "(the history job and its sweep schedule)", "writes the database as listed"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
	if r.db.RulePuts() != 1 {
		t.Errorf("rules deployed %d times", r.db.RulePuts())
	}

	// Each confirmation is its own: with only two names the second apply is
	// the last thing that happens, and the database is never touched.
	r2 := newFBRig(t)
	r2.historyImage()
	fakeTerminal(t)
	_, _, err = executeStdin(t, names(2), "setup", "--firebase-project", fpID)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if got := r2.applies(t); !slices.Equal(got, []string{"installation", "firebase"}) {
		t.Errorf("applies = %v", got)
	}
	if r2.db.RulePuts() != 0 || r2.db.Value("fugaro") != nil {
		t.Errorf("the database was written without its confirmation: rules %d, fugaro %v", r2.db.RulePuts(), r2.db.Value("fugaro"))
	}
	if _, err := os.Stat(r2.fbRoot()); err != nil {
		t.Errorf("no firebase workdir: %v", err)
	}
}

// The Firebase project is only read: never created, no billing linked.
func TestInitFirebaseNeverCreatesProject(t *testing.T) {
	r := newFBRig(t)
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	for name, reqs := range map[string][]gcpfake.Request{"crm": r.crm.Requests(), "billing": r.billing.Requests()} {
		for _, q := range reqs {
			read := q.Method == "GET" || strings.HasSuffix(q.Path, ":getIamPolicy")
			if !read || strings.Contains(q.Path, ":create") || strings.Contains(q.Path, "updateBillingInfo") {
				t.Errorf("%s: %s %s", name, q.Method, q.Path)
			}
		}
	}

	// Another project, there or not, stops before any plan: Fugaro only
	// starts fresh, and the budget backend goes in the installation's own
	// project and nowhere else.
	r2 := newFBRig(t)
	_, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", "aurora-missing", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "is not the installation's GCP project") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r2.ran(t, "plan")) != 0 || len(r2.ran(t, "apply")) != 0 {
		t.Errorf("calls = %q", r2.calls(t))
	}
	for _, q := range r2.crm.Requests() {
		if q.Method != "GET" && !strings.HasSuffix(q.Path, ":getIamPolicy") {
			t.Errorf("crm: %s %s", q.Method, q.Path)
		}
	}
}

// The billing read failing because the Cloud Billing API is disabled on the
// credentials' quota project must say so, not blame permissions (found live).
func TestInitFirebaseBillingAPIDisabled(t *testing.T) {
	r := newFBRig(t)
	r.su.Disable("cloudbilling.googleapis.com", r.billing.Server)
	_, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if ExitCode(err) != ExitUserError || err == nil {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	for _, want := range []string{"Cloud Billing API", "cloudbilling.googleapis.com", "quota project", "gcloud services enable cloudbilling.googleapis.com --project 123456789012"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "resourceAssociations") {
		t.Errorf("error blames permissions: %v", err)
	}
}

func TestInitFirebaseNoBilling(t *testing.T) {
	r := newFBRig(t)
	r.billing.SetBilling(fpID, false)
	_, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no billing") || !strings.Contains(err.Error(), "--link-billing") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Errorf("calls = %q", r.calls(t))
	}
}

// The Firebase project may be the installation's own GCP project (design D3,
// revised 2026-10-04): the three applies run as ever, the Firebase root's
// tfvars name the one project and leave iam and cloudresourcemanager to the
// installation root, and the database, mark and config are written.
func TestInitFirebaseSameProject(t *testing.T) {
	r := newFBRigFor(t, initProject)
	r.historyImage()
	fakeTerminal(t)
	out, _, err := executeStdin(t, names(3)+"us-east5\n"+names(1), "setup", "--firebase-project", initProject, "--budget-mode", "observe")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, want := r.applies(t), []string{"installation", "firebase", "installation"}; !slices.Equal(got, want) {
		t.Fatalf("applies in %v, want %v", got, want)
	}
	v := r.fbVars(t)
	if v["project"] != initProject || v["history_account"] != historyAccount {
		t.Errorf("tfvars project %v, history account %v", v["project"], v["history_account"])
	}
	if got := strs(v["skip_apis"]); !slices.Equal(got, infra.SharedAPIs) {
		t.Errorf("skip_apis = %v, want %v", got, infra.SharedAPIs)
	}
	if !strings.Contains(out, "warning: the Firebase project is the installation's own project") || !strings.Contains(out, "123456789012-compute@developer.gserviceaccount.com (roles/editor)") ||
		!strings.Contains(out, "iam.automaticIamGrantsForDefaultServiceAccounts") {
		t.Errorf("no warning about the default Compute account's roles/editor in\n%s", out)
	}
	lc := r.localConfig(t)
	if lc.Budget == nil || lc.Budget.FirebaseProject != initProject || lc.GCPProject != initProject || lc.Budget.RTDBURL != r.db.URL {
		t.Errorf("budget config = %+v (gcp_project %s)", lc.Budget, lc.GCPProject)
	}
	if r.db.RulePuts() != 1 || r.db.Value("fugaro") == nil {
		t.Errorf("the database was not written: rules %d, fugaro %v", r.db.RulePuts(), r.db.Value("fugaro"))
	}
	for _, want := range []string{"to the Firebase project " + initProject, "(the history job and its sweep schedule)"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}

// --plan-only with the Firebase project the installation's own makes the same
// checks and applies nothing.
func TestInitFirebaseSameProjectPlanOnly(t *testing.T) {
	r := newFBRigFor(t, initProject)
	out, _, err := executeStdin(t, "", "setup", "--firebase-project", initProject, "--plan-only")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := r.applies(t); len(got) != 0 {
		t.Errorf("applies = %v", got)
	}
}

// TestInitFirebaseRefusesAnotherProject: the two-project layout is gone
// (Fugaro only starts fresh): --firebase-project naming another project than
// the installation's own is refused in both doors before anything is read,
// planned or applied.
func TestInitFirebaseRefusesAnotherProject(t *testing.T) {
	for _, door := range []string{"setup", "update"} {
		t.Run(door, func(t *testing.T) {
			r := newFBRig(t)
			fakeTerminal(t)
			_, _, err := executeStdin(t, names(3), door, "--firebase-project", "aurora-fp", "--budget-mode", "observe")
			if ExitCode(err) != ExitUserError || !strings.Contains(errString(err), "--firebase-project aurora-fp is not the installation's GCP project "+initProject) {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
			if len(r.ran(t, "plan")) != 0 || len(r.ran(t, "apply")) != 0 {
				t.Errorf("calls = %q", r.calls(t))
			}
		})
	}
}

// A database holding data and no mark is not ours: refused before the
// Firebase root is applied, and again before anything is written.
func TestInitFirebaseRefusesUnmarkedData(t *testing.T) {
	r := newFBRig(t)
	r.db.Set("somebody/else", "data")
	_, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no Fugaro mark") || !strings.Contains(err.Error(), "somebody") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if got := r.applies(t); len(got) != 0 {
		t.Errorf("applies = %v: nothing may be applied (not even the first, and no grant on the Firebase project) over unmarked data", got)
	}
	if r.db.RulePuts() != 0 || r.db.Value("fugaro") != nil || r.db.Value("config") != nil {
		t.Errorf("the database was written: %v", r.db.Value(""))
	}

	// Another Fugaro project's mark.
	r2 := newFBRig(t)
	r2.db.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "other", "gcp_project": "proj-1234", "version": 1})
	_, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), `mark of project "other"`) {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}

	// The same Fugaro name in another GCP project is another installation.
	r4 := newFBRig(t)
	r4.db.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": "other-gcp-project", "version": 1})
	_, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "other-gcp-project") || len(r4.applies(t)) != 0 {
		t.Fatalf("exit %d, err %v, applies %v", ExitCode(err), err, r4.applies(t))
	}

	// A database whose project name is another's, though marked as ours.
	r3 := newFBRig(t)
	r3.db.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	r3.db.Set("fugaro/project", "other")
	_, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "belongs to another Fugaro project") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if r3.db.RulePuts() != 0 {
		t.Error("rules deployed over another project's database")
	}
}

// The Firebase root's admins are the GCP project's owners and editors who
// are users or groups; Google's default service accounts, domain: and
// deleted members are left out, and nothing Fugaro manages is accepted.
func TestInitFirebaseOwnersToTfvars(t *testing.T) {
	r := newFBRig(t)
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes", "--budget-admin", "user:boss@example.com",
		"--launcher", "user:l@example.com", "--operator", "user:op@example.com")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	v := r.fbVars(t)
	if got, want := strs(v["admins"]), []string{"group:owners@example.com", "user:editor@example.com", "user:owner@example.com"}; !slices.Equal(got, want) {
		t.Errorf("admins = %v, want %v", got, want)
	}
	if got := strs(v["budget_admins"]); !slices.Equal(got, []string{"user:boss@example.com"}) {
		t.Errorf("budget_admins = %v", got)
	}
	if got := strs(v["launchers"]); !slices.Equal(got, []string{"user:l@example.com", "user:op@example.com"}) {
		t.Errorf("launchers = %v", got)
	}
	if v["project"] != fpID || v["fugaro_project"] != initProjectName || v["history_account"] != historyAccount {
		t.Errorf("vars = %v", v)
	}
	names, _ := v["names"].(map[string]any)
	if names["signer_account_id"] != "fugaro-token-signer" || names["minter_role_id"] != "fugaroTokenMinter" {
		t.Errorf("names = %v", names)
	}
	for _, m := range append(strs(v["admins"]), strs(v["budget_admins"])...) {
		if strings.Contains(m, "gserviceaccount") || strings.HasPrefix(m, "domain:") || strings.HasPrefix(m, "deleted:") {
			t.Errorf("admin %s", m)
		}
	}
	if !strings.Contains(out, "not a budget admin") || !strings.Contains(out, "123456789012-compute@developer.gserviceaccount.com") {
		t.Errorf("the skipped members are not said:\n%s", out)
	}
	if got := r.localConfig(t).Terraform.BudgetAdmins; !slices.Equal(got, []string{"user:boss@example.com"}) {
		t.Errorf("terraform.budget_admins = %v", got)
	}
}

func TestInitFirebaseRefusesBadMembers(t *testing.T) {
	for name, args := range map[string][]string{
		"job account":       {"--budget-admin", "serviceAccount:fugaro-aurora-web-1a2b3c@proj-1234.iam.gserviceaccount.com"},
		"build account":     {"--budget-admin", "serviceAccount:fugaro-b-aurora-9f8e7d@proj-1234.iam.gserviceaccount.com"},
		"scheduler":         {"--launcher", "serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com"},
		"history":           {"--operator", "serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com"},
		"signer":            {"--budget-admin", "serviceAccount:fugaro-token-signer@proj-1234.iam.gserviceaccount.com"},
		"domain launcher":   {"--launcher", "domain:example.com"},
		"wildcard operator": {"--operator", "user:*@example.com"},
		"bare address":      {"--budget-admin", "boss@example.com"},
		"all users":         {"--budget-admin", "allUsers"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newFBRig(t)
			_, _, err := executeStdin(t, "us-east5\n", append([]string{"setup", "--firebase-project", fpID, "--yes"}, args...)...)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("exit %d, err %v", ExitCode(err), err)
			}
			if len(r.ran(t, "apply")) != 0 {
				t.Errorf("applied before refusing: %q", r.calls(t))
			}
		})
	}
}

// The rules are deployed after the second apply, behind their own
// confirmation, and are the generated ones.
func TestRulesDeployedAfterSecondApply(t *testing.T) {
	r := newFBRig(t)
	r.historyImage()
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	want, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	var live, gen any
	if err := json.Unmarshal(r.db.Rules(), &live); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(want, &gen)
	a, _ := json.Marshal(live)
	b, _ := json.Marshal(gen)
	if r.db.RulePuts() != 1 || !bytes.Equal(a, b) {
		t.Errorf("rules puts %d; deployed rules differ from the generated ones", r.db.RulePuts())
	}
	// The rules go in last, over a database that is already marked and
	// configured: the mark is written before them.
	var puts []string
	for _, q := range r.db.Requests() {
		if q.Method == "PUT" {
			puts = append(puts, strings.TrimSuffix(q.Path, ".json"))
		}
	}
	// The rules go in last of the database's own writes, over a database that
	// is already marked and configured: the mark is written before them. The
	// two writes after them are the generation markers, which exist to say the
	// deploy above succeeded and must not be there otherwise
	// (TestRulesMarkerOnlyAfterTheRulesDeploy): the document's generation and,
	// beside it, the credentials children's own (credentials-service plan, task
	// C1.3), which the same deploy carries because they ride on the same
	// document.
	if len(puts) < 3 || puts[0] != "/fugaro/mark" || puts[len(puts)-3] != "/.settings/rules" ||
		puts[len(puts)-2] != "/"+projectpolicy.PathRulesMarker() ||
		puts[len(puts)-1] != "/"+rules.PathCredsRulesMarker() {
		t.Errorf("PUT order = %v", puts)
	}
}

// TestRulesMarkerOnlyAfterTheRulesDeploy: config/limits/policyRules records
// which generation of the rules is deployed, and the runner (phase 1 task 1.3)
// reads it so it never decodes a policy the deployed rules do not protect. A
// marker written without a successful deploy — or before one — would halt
// every budget-on run of a database whose rules were never deployed, so the
// marker is written only after the deploy, and a deploy that fails leaves no
// marker at all.
//
// The credentials children's marker (`config/limits/credsRules`, plan task
// C1.3) is written by the same deploy and for the same reason, so it is pinned
// here too: absent after a refused deploy, at this binary's generation after
// one that succeeded, and beside — never on top of — the largest lease.
func TestRulesMarkerOnlyAfterTheRulesDeploy(t *testing.T) {
	marker := func(r *fbRig) any { return r.db.Value(projectpolicy.PathRulesMarker()) }
	credsMarker := func(r *fbRig) any { return r.db.Value(rules.PathCredsRulesMarker()) }

	// A refused rules deploy: init fails and leaves no marker.
	refused := newFBRig(t)
	refused.historyImage()
	refused.db.DenyRules(1)
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err == nil {
		t.Fatal("init reported success although the rules deploy was refused")
	}
	if got := marker(refused); got != nil {
		t.Errorf("%s = %#v after a refused rules deploy, want it absent: the runner would read a policy these rules do not protect", projectpolicy.PathRulesMarker(), got)
	}
	// The credentials children's marker is the same claim about a different
	// generation (credentials-service plan, task C1.3): a cascade launch reads it
	// to know the deployed document carries `/creds` and the entry's new
	// children, so it must not survive a deploy that did not happen.
	if got := credsMarker(refused); got != nil {
		t.Errorf("%s = %#v after a refused rules deploy, want it absent: a cascade launch would pass the marker gate on rules that do not carry the children", rules.PathCredsRulesMarker(), got)
	}
	if n := refused.db.RulePuts(); n != 0 {
		t.Errorf("rules deployed %d times, want 0", n)
	}

	// A deploy that succeeds records this generation.
	r := newFBRig(t)
	r.historyImage()
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	if r.db.RulePuts() != 1 {
		t.Errorf("rules deployed %d times, want 1", r.db.RulePuts())
	}
	num, ok := marker(r).(json.Number)
	if !ok || num.String() != strconv.Itoa(projectpolicy.RulesVersion) {
		t.Errorf("%s = %#v, want %d (RulesVersion): the runner compares them", projectpolicy.PathRulesMarker(), marker(r), projectpolicy.RulesVersion)
	}
	credsNum, ok := credsMarker(r).(json.Number)
	if !ok || credsNum.String() != strconv.Itoa(rules.CredsRulesVersion) {
		t.Errorf("%s = %#v, want %d (CredsRulesVersion)", rules.PathCredsRulesMarker(), credsMarker(r), rules.CredsRulesVersion)
	}
	// Both sit beside the largest lease, not on top of it.
	lim, _ := r.db.Value("config/limits").(map[string]any)
	if len(lim) != 3 || lim["maxReserveMicros"] == nil {
		t.Errorf("config/limits = %v, want the two markers beside maxReserveMicros", lim)
	}
}

// TestInitRefusesADatabaseFromANewerFugaro: the marker records which
// generation of the security rules is deployed, and a marker above this
// binary's RulesVersion says the rules came from a newer Fugaro. fugaro
// update --firebase-project deploys the rules *this* binary carries, so it
// must refuse rather than overwrite the newer installation's rules with older
// ones and record its own generation over the newer number — the state doctor
// warns about and tells the owner not to cause ("do not re-run 'fugaro update
// --firebase-project' with this binary", policy-rules-skew).
//
// Hand mutation this fails on: dropping the refusal from infra.DB.Plan, which
// rewrites the marker down to this binary's generation (and, on a real
// downgrade, redeploys the older rules document).
func TestInitRefusesADatabaseFromANewerFugaro(t *testing.T) {
	r := newFBRig(t)
	r.historyImage()
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	if n := r.db.RulePuts(); n != 1 {
		t.Fatalf("rules deployed %d times, want 1 (the fixture)", n)
	}
	newer := projectpolicy.RulesVersion + 1
	r.db.Set(projectpolicy.PathRulesMarker(), newer)
	puts := len(r.db.Requests())
	_, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: an older binary must not deploy over a newer installation's rules", err)
	}
	for _, want := range []string{projectpolicy.PathRulesMarker(), "generation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
	if got := r.db.Value(projectpolicy.PathRulesMarker()); got != json.Number(strconv.Itoa(newer)) {
		t.Errorf("%s = %#v, want %d: the refusal rewrote the newer installation's marker", projectpolicy.PathRulesMarker(), got, newer)
	}
	if n := r.db.RulePuts(); n != 1 {
		t.Errorf("rules deployed %d times, want 1: the older document was put over the newer one", n)
	}
	for _, q := range r.db.Requests()[puts:] {
		if q.Method == "PUT" || q.Method == "PATCH" {
			t.Errorf("the refused init wrote %s /%s", q.Method, q.Path)
		}
	}
}

// TestInitLeavesAnUpToDateMarkerAlone: a database whose rules are already the
// generated ones is not re-deployed, and a marker that already records this
// generation is not rewritten: init writes nothing at all.
func TestInitLeavesAnUpToDateMarkerAlone(t *testing.T) {
	r := newFBRig(t)
	r.historyImage()
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	puts := len(r.db.Requests())
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	if n := r.db.RulePuts(); n != 1 {
		t.Errorf("the rules were deployed %d times, want 1: they are already the generated ones", n)
	}
	for _, q := range r.db.Requests()[puts:] {
		if q.Method == "PUT" || q.Method == "PATCH" {
			t.Errorf("the second init wrote %s /%s", q.Method, q.Path)
		}
	}
}

// The mark, the project's name (a plain string), the mode and the largest
// lease are written.
func TestMarkAndProjectWritten(t *testing.T) {
	r := newFBRig(t)
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	mark, _ := r.db.Value("fugaro/mark").(map[string]any)
	if mark["managed_by"] != "fugaro" || mark["project"] != initProjectName || mark["gcp_project"] != initProject || len(mark) != 4 {
		t.Errorf("mark = %v", mark)
	}
	if got, ok := r.db.Value("fugaro/project").(string); !ok || got != initProjectName {
		t.Errorf("project = %#v", r.db.Value("fugaro/project"))
	}
	lim, _ := r.db.Value("config/limits").(map[string]any)
	if lim["maxReserveMicros"].(json.Number).String() != "5000000" {
		t.Errorf("limits = %v: without a max reserve the rules deny every lease", lim)
	}
	for _, c := range r.db.Credentials() {
		if c != "" {
			t.Errorf("credential %q sent to the fake, which takes none (no_auth)", c)
		}
	}
}

func TestBudgetModeSeedsRTDB(t *testing.T) {
	mode := func(r *fbRig) any { return r.db.Value("config/mode") }

	// Without the flag an absent mode is seeded observe, and the jobs'
	// mode (the local config) is left alone.
	r := newFBRig(t)
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if mode(r) != "observe" || r.localConfig(t).BudgetMode() != localcfg.BudgetOff {
		t.Errorf("mode %v, local %s", mode(r), r.localConfig(t).BudgetMode())
	}
	if !strings.Contains(out, "budget.mode is off") {
		t.Errorf("no word that jobs don't use the backend yet:\n%s", out)
	}
	// ... and a mode set since is kept by a run without the flag.
	r.db.Set("config/mode", "enforce")
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	if mode(r) != "enforce" {
		t.Errorf("mode = %v: a run without --budget-mode changed it", mode(r))
	}

	// observe on a fresh project sets both.
	r = newFBRig(t)
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes", "--budget-mode", "observe"); err != nil {
		t.Fatal(err)
	}
	if mode(r) != "observe" || r.localConfig(t).BudgetMode() != localcfg.BudgetObserve {
		t.Errorf("mode %v, local %s", mode(r), r.localConfig(t).BudgetMode())
	}

	// enforce needs the per-run cap the jobs enforce; refused before any apply.
	r = newFBRig(t)
	_, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes", "--budget-mode", "enforce")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "per_run_usd") || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("exit %d, err %v, calls %q", ExitCode(err), err, r.calls(t))
	}
	// With it, enforce is still REFUSED while the database has no global
	// caps (an absent cap is a refusal in the rules: every run would halt),
	// before anything is applied or written, and says what to do instead.
	r.appendConfig(t, "budget: { per_run_usd: 5 }\n")
	_, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes", "--budget-mode", "enforce")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro budget set --global") || !strings.Contains(err.Error(), "--budget-mode observe") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.applies(t)) != 0 || r.db.Value("config/mode") != nil || r.db.RulePuts() != 0 {
		t.Errorf("enforce without caps changed something: applies %v, mode %v", r.applies(t), r.db.Value("config/mode"))
	}
	// Half the caps is no caps (on a database that is ours: marked).
	r.db.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	r.db.Set("config/caps/global", map[string]any{"dailyMicros": 150_000_000})
	if _, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes", "--budget-mode", "enforce"); ExitCode(err) != ExitUserError {
		t.Fatalf("daily cap alone: exit %d, err %v", ExitCode(err), err)
	}
	// Both caps: enforce is allowed and written.
	r.db.Set("config/caps/global", map[string]any{"dailyMicros": 150_000_000, "perRunMicros": 20_000_000})
	out, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes", "--budget-mode", "enforce")
	if err != nil {
		t.Fatal(err)
	}
	if mode(r) != "enforce" || r.localConfig(t).BudgetMode() != localcfg.BudgetEnforce {
		t.Errorf("mode %v, local %s", mode(r), r.localConfig(t).BudgetMode())
	}
	if strings.Contains(out, "every run halts") {
		t.Errorf("a warning about missing caps with caps set:\n%s", out)
	}

	// off turns the jobs' backend off in the config and leaves the database's
	// mode as it is; it still deploys the database, and says so.
	out, _, err = executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes", "--budget-mode", "off")
	if err != nil {
		t.Fatal(err)
	}
	if r.localConfig(t).BudgetMode() != localcfg.BudgetOff || mode(r) != "enforce" {
		t.Errorf("mode %v, local %s", mode(r), r.localConfig(t).BudgetMode())
	}
	if !strings.Contains(out, "still deployed the database") || !strings.Contains(out, "unchanged by off") {
		t.Errorf("off doesn't say the database is still written:\n%s", out)
	}
}

// A first run with no database yet refuses enforce before any apply.
func TestBudgetModeEnforceRefusedOnFirstRun(t *testing.T) {
	r := newFBRig(t)
	r.appendConfig(t, "budget: { per_run_usd: 5 }\n")
	// Point the probe at a fake that lists no database.
	empty := gcpfake.NewFirebaseDB(t)
	b, _ := os.ReadFile(r.cfg)
	if err := os.WriteFile(r.cfg, []byte(strings.Replace(string(b), "firebase_database: "+r.fbdb.URL, "firebase_database: "+empty.URL, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes", "--budget-mode", "enforce")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--budget-mode observe") || len(r.applies(t)) != 0 {
		t.Fatalf("exit %d, err %v, applies %v", ExitCode(err), err, r.applies(t))
	}
	// Observe is fine, and the post-apply path would check the new database.
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes", "--budget-mode", "observe"); err != nil {
		t.Fatal(err)
	}
}

// A second run changes nothing: no apply, no database write, no config
// rewrite, and no confirmation.
func TestInitFirebaseIdempotent(t *testing.T) {
	r := newFBRig(t)
	r.historyImage()
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes", "--budget-mode", "observe"); err != nil {
		t.Fatal(err)
	}
	cfgBefore, _ := os.ReadFile(r.cfg)
	valueBefore, _ := json.Marshal(r.db.Value(""))
	puts := r.db.RulePuts()
	reqs := len(r.db.Requests())
	r.script["plan"] = map[string]any{"exit": 0}
	r.save(t)
	applies := len(r.applies(t))

	fakeTerminal(t) // a confirmation would fail: nothing is typed
	out, _, err := executeStdin(t, "", "setup", "--firebase-project", fpID, "--budget-mode", "observe")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := len(r.applies(t)); got != applies {
		t.Errorf("a second run applied %d more", got-applies)
	}
	cfgAfter, _ := os.ReadFile(r.cfg)
	valueAfter, _ := json.Marshal(r.db.Value(""))
	if !bytes.Equal(cfgBefore, cfgAfter) || !bytes.Equal(valueBefore, valueAfter) || r.db.RulePuts() != puts {
		t.Errorf("a second run changed something:\n%s\n%s", cfgBefore, cfgAfter)
	}
	for _, q := range r.db.Requests()[reqs:] {
		if q.Method != "GET" {
			t.Errorf("the database got %s %s", q.Method, q.Path)
		}
	}
	if strings.Contains(out, "⚠ CONFIRM") {
		t.Errorf("a confirmation on an unchanged installation:\n%s", out)
	}
	if !strings.Contains(out, "is up to date") {
		t.Errorf("no word that the config is up to date:\n%s", out)
	}
}

// The local config records the Firebase outputs, and jobs get them only when
// asked; the history job waits for its image.
func TestInitFirebaseWritesConfigAndHistoryJob(t *testing.T) {
	r := newFBRig(t)
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes", "--budget-mode", "observe"); err != nil {
		t.Fatal(err)
	}
	b := r.localConfig(t).Budget
	if b.FirebaseProject != fpID || b.RTDBURL != r.db.URL || b.FirebaseAPIKey != fpAPIKey || b.TokenSigner != fpSigner || b.Mode != "observe" {
		t.Errorf("budget = %+v", b)
	}
	v := r.tfvars(t)
	h, _ := v["history"].(map[string]any)
	if v["enable_budget"] != true || h["account_id"] != "fugaro-history" || h["job"] != "fugarohist" || h["image"] != historyImagePath || h["deploy_job"] != false {
		t.Errorf("without its image: enable_budget %v, history %v", v["enable_budget"], h)
	}
	if strings.HasPrefix(h["job"].(string), "fugaro-") {
		t.Errorf("the history job %v would be counted as a workflow job", h["job"])
	}

	r = newFBRig(t)
	r.historyImage()
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	h, _ = r.tfvars(t)["history"].(map[string]any)
	if h["deploy_job"] != true || h["firebase_project"] != fpID || h["rtdb_url"] != r.db.URL {
		t.Errorf("with its image: history %v", h)
	}
}

// A plain fugaro setup keeps the history account and job while the config
// records the Firebase project (else it would plan their deletion).
func TestInitKeepsBudgetBackend(t *testing.T) {
	r := newFBRig(t)
	r.historyImage()
	r.appendConfig(t, "budget:\n  mode: observe\n  firebase_project: "+fpID+"\n  rtdb_url: "+r.db.URL+"\n")
	if _, _, err := executeStdin(t, "", "setup", "--yes"); err != nil {
		t.Fatal(err)
	}
	v := r.tfvars(t)
	h, _ := v["history"].(map[string]any)
	if v["enable_budget"] != true || h["deploy_job"] != true || h["rtdb_url"] != r.db.URL {
		t.Errorf("enable_budget %v, history %v", v["enable_budget"], h)
	}
	// Without a Firebase project, no budget backend in the installation.
	r = newFBRig(t)
	if _, _, err := executeStdin(t, "", "setup", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.tfvars(t)["enable_budget"]; ok || r.tfvars(t)["history"] != nil {
		t.Errorf("tfvars = %v", r.tfvars(t))
	}
}

// A removed admin grant is listed first and refused, with the reason it may
// have happened; --allow-delete names it when intended.
func TestGuardListsRemovedAdminGrant(t *testing.T) {
	r := newFBRig(t)
	addr := `module.firebase.google_project_iam_member.admin["user:old@example.com"]`
	r.setPlan(t, planChange{"address": addr, "type": "google_project_iam_member",
		"change": map[string]any{"actions": []string{"delete"}, "before": map[string]any{}, "after": nil}})
	r.script["plan"] = map[string]any{"exit": 2}
	// The installation's plan is empty; only the Firebase root changes.
	r.script["plan@installation"] = map[string]any{"exit": 0}
	r.script["show@installation"] = map[string]any{"stdout": `{"format_version":"1.2","resource_changes":[]}`}
	r.save(t)
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), addr) {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if !strings.Contains(out, "⚠ Review these first:\n  ⚠ delete "+addr) {
		t.Errorf("the removed grant is not listed under Review:\n%s", out)
	}
	if !strings.Contains(err.Error(), "owners and editors") || !strings.Contains(err.Error(), "--budget-admin") {
		t.Errorf("no hint on why a grant is removed: %v", err)
	}
	if len(r.applies(t)) != 0 {
		t.Errorf("applied: %v", r.applies(t))
	}
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes", "--allow-delete", addr); err != nil {
		t.Fatal(err)
	}
	if got := r.applies(t); !slices.Equal(got, []string{"firebase"}) {
		t.Errorf("applies = %v", got)
	}
}

func TestInitFirebaseFlagsRefused(t *testing.T) {
	r := newFBRig(t)
	for name, args := range map[string][]string{
		"budget-mode alone":  {"setup", "--budget-mode", "observe"},
		"budget-admin alone": {"setup", "--budget-admin", "user:a@example.com"},
		"bad mode":           {"setup", "--firebase-project", fpID, "--budget-mode", "strict"},
		"bad id":             {"setup", "--firebase-project", "Not A Project"},
		"with forget":        {"update", "--firebase-project", fpID, "--forget"},
		"with config-only":   {"setup", "--firebase-project", fpID, "--config-only"},
		"with repo":          {"setup", "--repo", "--firebase-project", fpID},
	} {
		_, _, err := executeStdin(t, "", args...)
		if ExitCode(err) != ExitUserError {
			t.Errorf("%s: exit %d, err %v", name, ExitCode(err), err)
		}
	}
	// Another Firebase project than the one the config records.
	r.appendConfig(t, "budget: { firebase_project: other-fp }\n")
	_, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "other-fp") {
		t.Errorf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "plan")) != 0 || len(r.ran(t, "apply")) != 0 {
		t.Errorf("terraform planned or applied: %q", r.calls(t))
	}
}

func TestInitFirebasePrintVarsNoCalls(t *testing.T) {
	r := newFBRig(t)
	out, errOut, err := executeStdin(t, "", "setup", "--firebase-project", fpID, "--print-vars", "--budget-admin", "user:boss@example.com")
	if err != nil {
		t.Fatal(err)
	}
	var both struct {
		Installation map[string]any `json:"installation"`
		Firebase     map[string]any `json:"firebase"`
	}
	if err := json.Unmarshal([]byte(out), &both); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if both.Installation["enable_budget"] != true || both.Firebase["project"] != fpID || !slices.Equal(strs(both.Firebase["budget_admins"]), []string{"user:boss@example.com"}) {
		t.Errorf("vars = %s", out)
	}
	if !strings.Contains(errOut, "ungated") {
		t.Errorf("no ungated note:\n%s", errOut)
	}
	if len(r.calls(t)) != 0 || len(r.crm.Requests()) != 0 || len(r.billing.Requests()) != 0 || len(r.db.Requests()) != 0 {
		t.Errorf("--print-vars made calls: terraform %q", r.calls(t))
	}
}

// update --repo writes the backend's address and web key into the workflow jobs
// whenever the installation has them, whatever its budget mode (D11, plan
// task 1.4 step 1). The mode is the project's policy now, and a run reads that
// policy with these two (the token exchange needs the key, not only the URL);
// a job deployed with the mode off and nothing to read the policy with would
// run on the environment ceiling with the policy unenforced. What the mode
// still decides is FUGARO_BUDGET_MODE and the grace, which are the fallback
// for an installation that has published no policy at all.
func TestInitRepoPassesRTDBEnv(t *testing.T) {
	for mode, want := range map[string]map[string]bool{
		"observe": {"FUGARO_RTDB_URL": true, "FUGARO_FIREBASE_API_KEY": true, "FUGARO_BUDGET_GRACE": true, "FUGARO_BUDGET_MODE": true},
		"off":     {"FUGARO_RTDB_URL": true, "FUGARO_FIREBASE_API_KEY": true, "FUGARO_BUDGET_GRACE": false, "FUGARO_BUDGET_MODE": false},
	} {
		isolateProjects(t, t.TempDir())
		path := writeProject(t, "aurora", "proj-1234")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		block := "budget:\n  mode: " + mode + "\n  firebase_project: aurora-fp\n  rtdb_url: https://aurora-fp-default-rtdb.firebaseio.com\n  firebase_api_key: " + fpAPIKey +
			"\n  token_signer: " + fpSigner + "\n  unreachable_grace: 2m\nbase_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc}\n"
		if err := os.WriteFile(path, append(data, block...), 0o600); err != nil {
			t.Fatal(err)
		}
		root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: api-key }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n")
		testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
		t.Chdir(t.TempDir())
		out, _, err := execute(t, "setup", "--repo", "--print-vars", "--project", "aurora", "--github-app-id", "42", root)
		if err != nil {
			t.Fatal(err)
		}
		for key, wantKey := range want {
			if got := strings.Contains(out, key); got != wantKey {
				t.Errorf("mode %s: %s in the job's env = %v, want %v", mode, key, got, wantKey)
			}
		}
		for _, secretish := range []string{fpSigner, "aurora-fp\"", "token_signer"} {
			if strings.Contains(out, secretish) {
				t.Errorf("mode %s: the jobs' env carries %q", mode, secretish)
			}
		}
	}
}

// With no history image the operator is told the exact commands that build
// and push it: nothing else does.
func TestInitFirebasePrintsHistoryImageCommands(t *testing.T) {
	r := newFBRig(t)
	_ = r
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"sh images/build-base.sh history " + historyImagePath, "docker push " + historyImagePath, "gcloud auth configure-docker us-east5-docker.pkg.dev"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}

func TestIdentityPlatformInitializedWhenMissing(t *testing.T) {
	r := newFBRig(t)
	r.idt.UninitializeIdentityPlatform()
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := r.idt.InitializeAuthCalls(); n != 1 {
		t.Errorf("initializeAuth calls = %d, want 1", n)
	}
	if !strings.Contains(out, "ensure Identity Platform is initialized (no sign-in providers)") {
		t.Errorf("the confirmation does not list the step:\n%s", out)
	}
	if r.db.Value("fugaro/mark") == nil {
		t.Error("the database was not written")
	}
}

func TestIdentityPlatformStepNotRunWithoutConfirmation(t *testing.T) {
	r := newFBRig(t)
	r.idt.UninitializeIdentityPlatform()
	fakeTerminal(t) // nothing is typed
	// The installation and Firebase applies are confirmed; the database step is not.
	out, _, err := executeStdin(t, initProjectName+"\n"+initProjectName+"\n", "setup", "--firebase-project", fpID)
	if err == nil {
		t.Fatalf("not confirmed, yet init succeeded:\n%s", out)
	}
	if n := r.idt.InitializeAuthCalls(); n != 0 {
		t.Errorf("initializeAuth calls = %d before the confirmation", n)
	}
	if !strings.Contains(out, "ensure Identity Platform is initialized (no sign-in providers)") {
		t.Errorf("the confirmation does not list the step:\n%s", out)
	}
}

func TestIdentityPlatformAlreadyInitializedNoPost(t *testing.T) {
	r := newFBRig(t)
	if _, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	if n := r.idt.InitializeAuthCalls(); n != 0 {
		t.Errorf("initializeAuth calls = %d on an initialized project", n)
	}
}

func TestIdentityPlatformAlreadyEnabledRaceIsOK(t *testing.T) {
	r := newFBRig(t)
	r.idt.UninitializeIdentityPlatform()
	r.idt.RaceInitialize()
	out, _, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := r.idt.InitializeAuthCalls(); n != 1 {
		t.Errorf("initializeAuth calls = %d, want 1", n)
	}
}

func TestIdentityPlatformRefusesPublicSignUp(t *testing.T) {
	for name, cfg := range map[string]map[string]any{
		"email":     {"signIn": map[string]any{"email": map[string]any{"enabled": true}}},
		"anonymous": {"signIn": map[string]any{"anonymous": map[string]any{"enabled": true}}},
		"phone":     {"signIn": map[string]any{"phoneNumber": map[string]any{"enabled": true}}},
		"idp":       {"defaultSupportedIdpConfigs": []any{map[string]any{"name": "google.com", "enabled": true}}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newFBRig(t)
			r.idt.SetIdentityPlatformConfig(cfg)
			_, stderr, err := executeStdin(t, "us-east5\n", "setup", "--firebase-project", fpID, "--yes")
			if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error()+stderr, "public sign-up") {
				t.Fatalf("err = %v, stderr = %s", err, stderr)
			}
			if r.idt.InitializeAuthCalls() != 0 || r.db.Value("fugaro/mark") != nil {
				t.Error("a refused configuration was followed by writes")
			}
		})
	}
}

// dropEndpoint removes one endpoint key from the local config.
func (r *fbRig) dropEndpoint(t *testing.T, key string) {
	t.Helper()
	b, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if strings.Contains(l, "endpoints:") {
			parts := strings.Split(l, ", ")
			var keep []string
			for _, p := range parts {
				if !strings.Contains(p, key+": ") {
					keep = append(keep, p)
				}
			}
			lines[i] = strings.Join(keep, ", ")
		}
	}
	if err := os.WriteFile(r.cfg, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestFirebaseProjectDefaultsToTheGCPProject: one project id serves Google
// Cloud and Firebase unless the advanced flag says otherwise (spec.md, "One
// project, one id"; reconciliation C2, F4, D7). The default is the *id* the
// run works in, and it is the run's own --gcp-project: the pairing rule reads
// it, the Firebase engine resolves it, and the flag's own presence is still
// what puts the backend in the run at all (init_stages.go's newFirebaseStage),
// so a run that passes nothing applies no budget backend — the default names
// which project a Firebase step would use, it does not ask for one.
func TestFirebaseProjectDefaultsToTheGCPProject(t *testing.T) {
	o := &initOptions{cloud: cloudOptions{gcpProject: initProject}}
	if got := o.firebaseProject(); got != initProject {
		t.Errorf("firebaseProject() with no flag = %q, want the run's GCP project %q", got, initProject)
	}
	o.firebase = fpID
	if got := o.firebaseProject(); got != fpID {
		t.Errorf("firebaseProject() with --firebase-project %s = %q", fpID, got)
	}
	// The one-id case is one run spelled two ways: a creation whose Firebase
	// project is the project it creates is the default path (F4's "the
	// pairing rule becomes the default path rather than a special case"),
	// whether the id is left to the default or named.
	for _, tc := range []struct {
		name, flag string
	}{{"left to the default", ""}, {"named as the created project", newProj}} {
		t.Run(tc.name, func(t *testing.T) {
			o := &initOptions{door: setupDoor.name, createProject: true, cloud: cloudOptions{gcpProject: newProj}, firebase: tc.flag}
			if err := o.check(); err != nil {
				t.Errorf("--create-project --gcp-project %s with the Firebase project %s: %v", newProj, tc.name, err)
			}
			if got := o.firebaseProject(); got != newProj {
				t.Errorf("firebaseProject() = %q, want the created project %q", got, newProj)
			}
		})
	}
}

// bareFirebase is the retired flag as a whole word: `--firebase` and not the
// prefix of `--firebase-project`, which a name check that matched the prefix
// would pass on.
var bareFirebase = regexp.MustCompile(`--` + retiredFirebaseFlag + `([^-a-z0-9]|$)`)

// TestOldFirebaseFlagIsGone: --firebase is renamed, not aliased (D2's
// reasoning applies to a flag too, D7). Both doors take --firebase-project,
// neither one offers or accepts --firebase, and a scripted run that still
// passes it is refused with the new spelling named rather than with pflag's
// bare "unknown flag" -- a flag that moved has to say where it moved to.
func TestOldFirebaseFlagIsGone(t *testing.T) {
	for door, build := range doorCommands {
		cmd := build()
		if f := cmd.Flags().Lookup(retiredFirebaseFlag); f != nil {
			t.Errorf("fugaro %s still declares --%s: it is renamed, not aliased", door, retiredFirebaseFlag)
		}
		if f := cmd.Flags().Lookup("firebase-project"); f == nil {
			t.Errorf("fugaro %s has no --firebase-project", door)
		}
		// The offer, in the two places a door makes one: the flag list it
		// prints, and its Long. A *refusal* may name the retired flag (that is
		// the sentence a scripted run gets, below), which is why this reads the
		// two surfaces that offer things.
		for _, text := range []string{cmd.Flags().FlagUsages(), cmd.Long} {
			if m := bareFirebase.FindAllString(text, -1); len(m) > 0 {
				t.Errorf("fugaro %s's help offers --%s %d time(s); the flag is --firebase-project", door, retiredFirebaseFlag, len(m))
			}
		}
	}
	firstRunEnv(t)
	out, errOut, err := executeStdin(t, "", "setup", "--"+retiredFirebaseFlag, fpID)
	if err == nil {
		t.Fatal("fugaro setup --" + retiredFirebaseFlag + " exited 0")
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d", got, ExitUserError)
	}
	got := out + errOut + ErrorText(err)
	if !strings.Contains(got, "--firebase-project") {
		t.Errorf("the refusal does not name the flag that replaced it: %q", got)
	}
	// Only that one spelling is answered, and the comparison is pflag's whole
	// message. A typo that merely begins with it is a different question with
	// a different answer: `--firebase-db` does not exist either, and telling
	// its author that --firebase moved would name a rename that has nothing to
	// do with what they typed.
	for _, spelling := range []string{"--firebase-db", "--firebase-proj", "--firebase-project-x"} {
		t.Run(spelling, func(t *testing.T) {
			firstRunEnv(t)
			out, errOut, err := executeStdin(t, "", "setup", spelling, fpID)
			if err == nil {
				t.Fatalf("fugaro setup %s exited 0", spelling)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			msg := out + errOut + ErrorText(err)
			if !strings.Contains(msg, "unknown flag: "+spelling) {
				t.Errorf("the error is not pflag's own for %s: %q", spelling, msg)
			}
			if strings.Contains(msg, "is --firebase-project now") {
				t.Errorf("%s was answered with the retired flag's sentence:\n%s", spelling, msg)
			}
		})
	}
	// The retired spelling is an unknown flag, and unknown flags are pflag's:
	// the refusal is raised while the flags are parsed, before there is a run
	// whose --json object could carry it, so neither it nor cobra's own error
	// for a flag that never existed prints one. The two have one shape, which
	// is the point — the exit code (ExitUserError) is the half a script acts
	// on, and the sentence the half a person reads.
	for _, args := range [][]string{
		{"setup", "--json", "--" + retiredFirebaseFlag, fpID},
		{"setup", "--json", "--no-such-flag"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			firstRunEnv(t)
			out, _, err := executeStdin(t, "", args...)
			if err == nil {
				t.Fatal("exit 0")
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			if strings.Contains(out, "{") {
				t.Errorf("%v printed a document on stdout, and a flag-parse refusal has no run to print one from: %q", args, out)
			}
		})
	}
}

// TestTheFirebaseProjectDefaultNamesTheIdAndNotTheStage: D7's default has two
// halves, and this pins them apart, because reading either as the other is a
// wrong run. The *id* the run works in defaults to the run's own --gcp-project
// (one id for both), so the pairing rule, the validation and every message
// have one answer for "which project". The *backend* is still the flag's own
// presence (init_stages.go's newFirebaseStage and newInstallation2Stage), so a
// run that names no project applies none — and `--firebase-project=` is
// pflag's empty value, which reads as no flag at all: a script whose variable
// is unset must not buy three applies against the installation's own project,
// one of them a Firestore database in a location that can never change.
func TestTheFirebaseProjectDefaultNamesTheIdAndNotTheStage(t *testing.T) {
	// The value is the id, whatever it names: the installation's own project
	// spelled out, or a separate one (the advanced case).
	for _, fp := range []string{initProject, fpID} {
		o := &initOptions{cloud: cloudOptions{gcpProject: initProject}, firebase: fp}
		if got := o.firebaseProject(); got != fp {
			t.Errorf("--firebase-project %s: the id is %q", fp, got)
		}
	}
	// The same id, and no backend: the two spellings of "no project named"
	// converge nothing of the Firebase stage's.
	for name, extra := range map[string][]string{
		"no flag at all": {"--yes"},
		"an empty value": {"--yes", "--firebase-project="},
	} {
		t.Run(name, func(t *testing.T) {
			r := newInitRig(t)
			out, _, err := executeStdin(t, "", append([]string{"setup", "--json"}, extra...)...)
			if err != nil {
				t.Fatalf("%v:\n%s", err, out)
			}
			var res convergeJSON
			if err := json.Unmarshal([]byte(out), &res); err != nil {
				t.Fatalf("%v:\n%s", err, out)
			}
			if got := res.state("firebase"); got != "skipped" {
				t.Errorf("with %s the Firebase stage is %q, want skipped: the default names which project a backend would use, it does not ask for one", name, got)
			}
			for _, c := range r.calls(t) {
				if strings.Contains(strings.Join(c, " "), "firebase") {
					t.Errorf("with %s the run called the Firebase engine: %q", name, c)
				}
			}
		})
	}
}

// stageDetail is the detail line the result carries for one stage. It is the
// stage's own account of itself — what a skipped state says about why, and what
// a done one says about what it found — which is the half of a stage result the
// state alone does not carry.
func (c convergeJSON) stageDetail(t *testing.T, name string) string {
	t.Helper()
	for _, s := range c.Stages {
		if s.Name == name {
			return s.Detail
		}
	}
	t.Errorf("the result has no %s stage: %+v", name, c.Stages)
	return ""
}

// updateBudgetRig is updateRig with the shared budget backend an installation
// that ran `fugaro update --firebase-project` has: budget.firebase_project is
// set in the local config (nothing else writes it), and the project's Realtime
// Database is the fake, marked as this installation's. It is the state the rules
// stage acts on, and the fixture every test below starts from.
func updateBudgetRig(t *testing.T) (*initRig, *gcpfake.RTDB) {
	t.Helper()
	r := updateRig(t)
	db := gcpfake.NewRTDB(t)
	db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	db.Set(budget.PathProject, initProjectName)
	// The mode and the largest lease are the database's own, seeded when the
	// backend was set up: the rules stage is about the rules document and its
	// two markers, and the plan it shows must carry nothing else.
	db.Set(budget.PathMode, budget.ModeEnforce)
	db.Set(budget.PathLimits, map[string]any{"maxReserveMicros": 5_000_000})
	r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5, rtdb_url: "+db.URL+", firebase_project: \""+initProject+"\" }\n")
	r.save(t)
	return r, db
}

// rulesMarkerOn is the deployed generation marker as the fake holds it, "" when
// the node is absent.
func rulesMarkerOn(t *testing.T, db *gcpfake.RTDB) string {
	t.Helper()
	v := db.Value(projectpolicy.PathRulesMarker())
	if v == nil {
		return ""
	}
	if n, ok := v.(json.Number); ok {
		return n.String()
	}
	return fmt.Sprint(v)
}

// TestUpdateConvergesV3NodeWithoutRulesMarkerInOneRun: the owner's rule of
// 2026-10-30 — `fugaro update` is the whole upgrade, and it works perfectly — at
// the state this stage exists for: the installation's published policy node is a
// version behind this Fugaro, and the deployed rules carry no policy marker at
// all (a deploy that never ran, or one an older CLI's redeploy rolled back).
// One `fugaro update --yes` has to converge it, the rules generation deployed
// and then the node migrated, because a state that needs a second run of the
// same command — or a 'fugaro update --firebase-project' beside it — is the
// operator step the rule forbids.
//
// Red before the rules stage existed: the policy stage's writer gate
// (requirePolicyRules) refuses to migrate a node under rules that predate the
// policy model, which is exactly this state, and no stage before it deployed the
// rules that gate is about.
func TestUpdateConvergesV3NodeWithoutRulesMarkerInOneRun(t *testing.T) {
	_, db := updateBudgetRig(t)
	db.Set(projectpolicy.PathShared(), v3Node(nil))
	out, _, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("one fugaro update --yes did not converge a version-3 node under rules that carry no policy marker: %v\n%s", err, out)
	}
	if got := nodeVersionOn(t, db); got != projectpolicy.Version {
		t.Errorf("the published policy is version %d, want %d:\n%s", got, projectpolicy.Version, out)
	}
	gen, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !infra.SameJSON(db.Rules(), gen) {
		t.Errorf("the rules deployed are not this Fugaro's generation, so the migrated node sits under rules no run reads it with:\n%s", out)
	}
	if got, want := rulesMarkerOn(t, db), strconv.Itoa(projectpolicy.RulesVersion); got != want {
		t.Errorf("the rules generation marker is %q, want %q:\n%s", got, want, out)
	}
	if !strings.Contains(out, initflow.Rules) {
		t.Errorf("the account does not name the rules stage:\n%s", out)
	}
}

// TestUpdateRulesStageRunsBeforePolicy: the ORDER, which is the whole point of
// the stage. The policy stage's write holds the publisher's gate on the deployed
// rules marker (requirePolicyRules), so a rules stage after it would deploy the
// rules too late for the migration that same run had already refused — the state
// UC3c-1 exists for. The canonical order and the order initStages registers are
// both checked, because the loop sorts by the first and a reader of the second
// sees who runs when.
func TestUpdateRulesStageRunsBeforePolicy(t *testing.T) {
	pos := func(names []string, name string) int {
		for i, n := range names {
			if n == name {
				return i
			}
		}
		return -1
	}
	canonical := initflow.Names()
	rules, policy := pos(canonical, initflow.Rules), pos(canonical, initflow.Policy)
	switch {
	case rules < 0:
		t.Fatalf("%s is not a canonical stage: %v", initflow.Rules, canonical)
	case policy < 0:
		t.Fatalf("%s is not a canonical stage: %v", initflow.Policy, canonical)
	case rules > policy:
		t.Errorf("the canonical order runs %s at %d and %s at %d: the rules generation is what the policy node is read under, so it is deployed first", initflow.Rules, rules, initflow.Policy, policy)
	}
	if !slices.Contains(initflow.Needs(initflow.Policy), initflow.Rules) {
		t.Errorf("the policy stage does not need %s: %v", initflow.Rules, initflow.Needs(initflow.Policy))
	}
	registered := namesOf(initStages(prodEngine(initOptions{door: updateDoor.name})))
	if r, p := pos(registered, initflow.Rules), pos(registered, initflow.Policy); r < 0 || p < 0 || r > p {
		t.Errorf("update registers the rules stage at %d and the policy stage at %d in %v: the rules stage is registered before the policy stage", r, p, registered)
	}
}

// TestUpdateDeploysStaleRulesWithoutFirebaseProjectFlag: the state the stage
// exists for, one generation short of this binary's (a marker below
// RulesVersion but above the policy generation, so the deployed document is not
// what this release carries while the policy nodes are already readable). One
// plain `fugaro update` deploys this binary's generation and records it, with
// no --firebase-project: that flag is the door that ADOPTS a Firebase project,
// and an installation that has one does not need to adopt it again.
func TestUpdateDeploysStaleRulesWithoutFirebaseProjectFlag(t *testing.T) {
	_, db := updateBudgetRig(t)
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.PolicyRulesVersion)
	db.SetRules([]byte(`{"rules":{".read":false}}`))

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err != nil {
		t.Fatalf("fugaro update did not deploy stale rules: %v\n%s%s", err, out, errOut)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got := res.state(initflow.Firebase); got != "skipped" {
		t.Errorf("the firebase stage is %q: no --firebase-project was given, so the rules stage is what deploys the rules", got)
	}
	if got := res.state(initflow.Rules); got != "changed" {
		t.Errorf("the rules stage is %q, want changed:\n%s", got, out)
	}
	gen, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !infra.SameJSON(db.Rules(), gen) {
		t.Errorf("the deployed rules are not this Fugaro's generation:\n%s", out)
	}
	if db.RulePuts() == 0 {
		t.Error("the run reports the rules deployed and wrote no document")
	}
	if got, want := rulesMarkerOn(t, db), strconv.Itoa(projectpolicy.RulesVersion); got != want {
		t.Errorf("the rules generation marker is %q, want %q:\n%s", got, want, out)
	}
}

// TestUpdateDeploysWhenRulesMarkerAbsent: no marker node at all, which is the
// state of an installation whose rules were deployed before the marker existed
// (and of one whose redeploy an older CLI rolled back). Neither the marker nor
// the document says the rules are this Fugaro's, so the stage deploys — and this
// is the arm that would fail if "absent" were read as "nothing to do", the
// reading requirePolicyRules cannot make for itself (any marker >= 1 passes its
// gate) and which the doctor's own check leaves to the policy-published line.
func TestUpdateDeploysWhenRulesMarkerAbsent(t *testing.T) {
	_, db := updateBudgetRig(t)
	db.SetRules([]byte(`{"rules":{}}`))
	if got := rulesMarkerOn(t, db); got != "" {
		t.Fatalf("the fixture has a marker: %q", got)
	}

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err != nil {
		t.Fatalf("fugaro update did not deploy rules under an absent marker: %v\n%s%s", err, out, errOut)
	}
	gen, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !infra.SameJSON(db.Rules(), gen) {
		t.Errorf("the deployed rules are not this Fugaro's generation:\n%s", out)
	}
	if got, want := rulesMarkerOn(t, db), strconv.Itoa(projectpolicy.RulesVersion); got != want {
		t.Errorf("the rules generation marker is %q, want %q:\n%s", got, want, out)
	}
}

// TestUpdateNeverDowngradesNewerRules: a marker above this binary's generation
// means a newer Fugaro deployed these rules, and this release's template must
// never be written over them — not the document and not the number. The stage
// reports the state, in the doctor's own sentence, and the run goes on: an owner
// whose installation is ahead of their CLI has other work in the same update
// (the images, the pin, the plugin), and refusing all of it would leave them no
// way through but an upgrade they may not be able to take yet.
func TestUpdateNeverDowngradesNewerRules(t *testing.T) {
	_, db := updateBudgetRig(t)
	newer := projectpolicy.RulesVersion + 1
	theirs := []byte(`{"rules":{".read":"auth != null"}}`)
	db.Set(projectpolicy.PathRulesMarker(), newer)
	db.SetRules(theirs)

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err != nil {
		t.Fatalf("a newer deployed generation stopped the whole update: %v\n%s%s", err, out, errOut)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got := res.state(initflow.Rules); got != "skipped" {
		t.Errorf("the rules stage is %q, want skipped: nothing may be deployed over a newer generation:\n%s", got, out)
	}
	if db.RulePuts() != 0 {
		t.Error("the run deployed this binary's rules over a newer generation")
	}
	if !bytes.Equal(db.Rules(), theirs) {
		t.Errorf("the deployed document changed:\n%s", db.Rules())
	}
	if got, want := rulesMarkerOn(t, db), strconv.Itoa(newer); got != want {
		t.Errorf("the rules generation marker is %q, want %q: the marker must not be rewritten down", got, want)
	}
	if text := out + errOut + res.stageDetail(t, initflow.Rules); !strings.Contains(text, "a newer Fugaro deployed them") {
		t.Errorf("the run does not report why it deployed nothing: %s", text)
	}
}

// TestUpdateLeavesCurrentRulesAlone: the marker at this generation with the
// deployed document equal to the one this Fugaro generates is the state every
// installation converges to, and a second `fugaro update` must do nothing at all
// to it — no plan, no confirmation, no write. A stage that redeployed here would
// make every update a security-rules write, which is exactly what the doctor's
// line exists to keep quiet about.
func TestUpdateLeavesCurrentRulesAlone(t *testing.T) {
	_, db := updateBudgetRig(t)
	gen, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	db.SetRules(gen)
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got := res.state(initflow.Rules); got != "done" {
		t.Errorf("the rules stage is %q, want done:\n%s", got, out)
	}
	if db.RulePuts() != 0 {
		t.Error("the run rewrote rules that are already this generation")
	}
	if got, want := rulesMarkerOn(t, db), strconv.Itoa(projectpolicy.RulesVersion); got != want {
		t.Errorf("the rules generation marker is %q, want %q", got, want)
	}
	if text := out + errOut; strings.Contains(text, "deploys the security rules listed") {
		t.Errorf("the run asked to deploy rules that are already current:\n%s", text)
	}
	if detail := res.stageDetail(t, initflow.Rules); !strings.Contains(detail, "this Fugaro's own") {
		t.Errorf("the account of the settled state is %q", detail)
	}
}

// TestUpdateWithoutSharedBudgetDoesNotTouchFirebase: an installation with no
// shared budget backend (budget.firebase_project is written by `fugaro update
// --firebase-project` and by nothing else) has no deployed generation to read,
// and the stage does nothing: no rule deploy, no marker, and no refusal — a
// project that never adopted a Firebase backend converges as it always did.
func TestUpdateWithoutSharedBudgetDoesNotTouchFirebase(t *testing.T) {
	r := updateRig(t)
	db := gcpfake.NewRTDB(t)
	db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	db.Set(budget.PathProject, initProjectName)
	db.Set(budget.PathMode, budget.ModeEnforce)
	db.Set(budget.PathLimits, map[string]any{"maxReserveMicros": 5_000_000})
	r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5, rtdb_url: "+db.URL+" }\n")
	r.save(t)

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	var res convergeJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if got := res.state(initflow.Rules); got != "skipped" {
		t.Errorf("the rules stage is %q, want skipped:\n%s", got, out)
	}
	if !strings.Contains(res.stageDetail(t, initflow.Rules), "budget.firebase_project") {
		t.Errorf("the skip does not say what is missing: %q", res.stageDetail(t, initflow.Rules))
	}
	if db.RulePuts() != 0 || rulesMarkerOn(t, db) != "" {
		t.Errorf("the run wrote to a database no shared budget names: %d rule deploy(s), marker %q", db.RulePuts(), rulesMarkerOn(t, db))
	}
}

// TestUpdateRulesDeclinedRefusesPolicyWrite: the deploy is behind the
// confirmation the database step of `--firebase-project` takes, and declining it
// stops the run where it stands. It must not go on to migrate the policy node:
// the gate the migration holds (requirePolicyRules) is the marker the refused
// deploy would have written, so a run that carried on would write a policy under
// the very rules it just declined to deploy. A person at a terminal who does not
// type the project's name is what a decline is — no other answer confirms this
// step for them — and the rules and the node are read back exactly as they were.
//
// The first run converges the installation, so the second one's first step with
// something to do is the rules deploy: on a fixture that has never run, the
// installation stage's own confirmation comes first, and the answer typed there
// would be the one this test is about.
func TestUpdateRulesDeclinedRefusesPolicyWrite(t *testing.T) {
	_, db := updateBudgetRig(t)
	if out, _, err := executeStdin(t, "", "update", "--yes"); err != nil {
		t.Fatalf("the converging run: %v\n%s", err, out)
	}
	puts := db.RulePuts()
	if puts == 0 {
		t.Fatal("the converging run deployed no rules: the second run would be declining a deploy that is not there")
	}
	// The state the two stages are ordered for, one run later: rules one
	// generation behind, and a published node one version behind.
	db.Set(projectpolicy.PathShared(), v3Node(nil))
	db.Set(projectpolicy.PathRulesMarker(), projectpolicy.PolicyRulesVersion)
	stale := []byte(`{"rules":{".read":false}}`)
	db.SetRules(stale)

	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { stdinIsTerminal = old })

	out, errOut, err := executeStdin(t, "\n\n\n\n\n\n\n\n", "update")
	if err == nil {
		t.Fatalf("a declined rules deploy exited 0:\n%s%s", out, errOut)
	}
	if text := out + errOut + ErrorText(err); !strings.Contains(text, "not confirmed") {
		t.Errorf("the refusal is not the declined confirmation: %s", text)
	}
	// And it is the rules step's own decline: the run reaches this stage's
	// confirmation and stops there, not at a step of a stage before it.
	if !strings.Contains(out, "stopped at "+initflow.Rules) {
		t.Errorf("the run did not stop at the rules stage's own confirmation:\n%s", out)
	}
	if db.RulePuts() != puts {
		t.Errorf("the rules were deployed anyway: %d deploy(s), want the %d the converging run made", db.RulePuts(), puts)
	}
	if !bytes.Equal(db.Rules(), stale) {
		t.Errorf("the deployed document changed:\n%s", db.Rules())
	}
	if got, want := rulesMarkerOn(t, db), strconv.Itoa(projectpolicy.PolicyRulesVersion); got != want {
		t.Errorf("the rules generation marker is %q, want the stale %q the deploy would have moved", got, want)
	}
	if got := nodeVersionOn(t, db); got != 3 {
		t.Errorf("the published policy moved to version %d after the rules deploy was declined: the migration's own gate is the marker that deploy did not write", got)
	}
}
