package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	fpID             = "aurora-fp"
	fpSigner         = "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"
	fpAPIKey         = "AIzaSyA0123456789abcdefghijklmnopqrstu"
	historyAccount   = "fugaro-history@proj-1234.iam.gserviceaccount.com"
	historyImagePath = "us-east5-docker.pkg.dev/proj-1234/" + infra.BaseRegistry + "/" + infra.HistoryImagePackage + ":latest"
)

// fbRig is the init rig plus the fakes of init --firebase: Cloud Billing, the
// project's Realtime Database, and a Firebase project with billing.
type fbRig struct {
	*initRig
	billing *gcpfake.Billing
	db      *gcpfake.RTDB
	fbdb    *gcpfake.FirebaseDB
	idt     *gcpfake.IdentityToolkit
	fs      *gcpfake.Firestore
	rules   *gcpfake.FirebaseRules
}

func newFBRig(t *testing.T) *fbRig {
	t.Helper()
	r := &fbRig{initRig: newInitRig(t), billing: gcpfake.NewBilling(t), db: gcpfake.NewRTDB(t), fbdb: gcpfake.NewFirebaseDB(t)}
	r.idt = gcpfake.NewIdentityToolkit(t, nil, "key", fpID)
	r.fs, r.rules = gcpfake.NewFirestore(t), gcpfake.NewFirebaseRules(t, fpID)
	r.fs.RemoveDatabase() // a fresh Firebase project has none
	r.fbdb.AddInstance(fpID, r.db.URL)
	r.stateBucket()
	r.crm.AddProject(fpID, 987654321098)
	r.billing.SetBilling(fpID, true)
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
	cfg := strings.Replace(string(b), "no_auth: true }", "cloud_billing: "+r.billing.URL+"/, firebase_database: "+r.fbdb.URL+"/, identity_toolkit: "+r.idt.URL+"/, firestore: "+r.fs.URL+", firebase_rules: "+r.rules.URL+", no_auth: true }", 1)
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
	for k, v := range map[string]string{"rtdb_url": r.db.URL, "firebase_api_key": fpAPIKey, "token_signer": fpSigner, "firebase_project": fpID} {
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
	out, _, err := executeStdin(t, names(3)+"us-east5\n"+names(1), "init", "--firebase", fpID, "--budget-mode", "observe")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, want := r.applies(t), []string{"installation", "firebase", "installation"}; !slices.Equal(got, want) {
		t.Fatalf("applies in %v, want %v", got, want)
	}
	if n := strings.Count(out, "⚠ CONFIRM"); n != 5 {
		t.Errorf("%d confirmations, want 5 (three applies, the database and its location):\n%s", n, out)
	}
	for _, want := range []string{"to the Firebase project aurora-fp", "(the history job and its sweep schedule)", "writes the database as listed"} {
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
	_, _, err = executeStdin(t, names(2), "init", "--firebase", fpID)
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
	if _, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil {
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

	// A project that isn't there stops before any plan.
	r2 := newFBRig(t)
	_, _, err := executeStdin(t, "", "init", "--firebase", "aurora-missing", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), "never creates") {
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
	_, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
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
	_, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "no billing") || !strings.Contains(err.Error(), "never enables billing") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Errorf("calls = %q", r.calls(t))
	}
	// And a project of its own: not the installation's.
	_, _, err = executeStdin(t, "", "init", "--firebase", initProject, "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "project of its own") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
}

// A database holding data and no mark is not ours: refused before the
// Firebase root is applied, and again before anything is written.
func TestInitFirebaseRefusesUnmarkedData(t *testing.T) {
	r := newFBRig(t)
	r.db.Set("somebody/else", "data")
	_, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
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
	_, _, err = executeStdin(t, "", "init", "--firebase", fpID, "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), `mark of project "other"`) {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}

	// The same Fugaro name in another GCP project is another installation.
	r4 := newFBRig(t)
	r4.db.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": "other-gcp-project", "version": 1})
	_, _, err = executeStdin(t, "", "init", "--firebase", fpID, "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "other-gcp-project") || len(r4.applies(t)) != 0 {
		t.Fatalf("exit %d, err %v, applies %v", ExitCode(err), err, r4.applies(t))
	}

	// A database whose project name is another's, though marked as ours.
	r3 := newFBRig(t)
	r3.db.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	r3.db.Set("fugaro/project", "other")
	_, _, err = executeStdin(t, "", "init", "--firebase", fpID, "--yes")
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
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--budget-admin", "user:boss@example.com",
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
		"signer":            {"--budget-admin", "serviceAccount:fugaro-token-signer@aurora-fp.iam.gserviceaccount.com"},
		"domain launcher":   {"--launcher", "domain:example.com"},
		"wildcard operator": {"--operator", "user:*@example.com"},
		"bare address":      {"--budget-admin", "boss@example.com"},
		"all users":         {"--budget-admin", "allUsers"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newFBRig(t)
			_, _, err := executeStdin(t, "", append([]string{"init", "--firebase", fpID, "--yes"}, args...)...)
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
	if _, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil {
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
	if len(puts) == 0 || puts[len(puts)-1] != "/.settings/rules" || puts[0] != "/fugaro/mark" {
		t.Errorf("PUT order = %v", puts)
	}
}

// The mark, the project's name (a plain string), the mode and the largest
// lease are written.
func TestMarkAndProjectWritten(t *testing.T) {
	r := newFBRig(t)
	if _, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil {
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
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
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
	if _, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	if mode(r) != "enforce" {
		t.Errorf("mode = %v: a run without --budget-mode changed it", mode(r))
	}

	// observe on a fresh project sets both.
	r = newFBRig(t)
	if _, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--budget-mode", "observe"); err != nil {
		t.Fatal(err)
	}
	if mode(r) != "observe" || r.localConfig(t).BudgetMode() != localcfg.BudgetObserve {
		t.Errorf("mode %v, local %s", mode(r), r.localConfig(t).BudgetMode())
	}

	// enforce needs the per-run cap the jobs enforce; refused before any apply.
	r = newFBRig(t)
	_, _, err = executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--budget-mode", "enforce")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "per_run_usd") || len(r.ran(t, "apply")) != 0 {
		t.Fatalf("exit %d, err %v, calls %q", ExitCode(err), err, r.calls(t))
	}
	// With it, enforce is still REFUSED while the database has no global
	// caps (an absent cap is a refusal in the rules: every run would halt),
	// before anything is applied or written, and says what to do instead.
	r.appendConfig(t, "budget: { per_run_usd: 5 }\n")
	_, _, err = executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--budget-mode", "enforce")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro budget set --global") || !strings.Contains(err.Error(), "--budget-mode observe") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.applies(t)) != 0 || r.db.Value("config/mode") != nil || r.db.RulePuts() != 0 {
		t.Errorf("enforce without caps changed something: applies %v, mode %v", r.applies(t), r.db.Value("config/mode"))
	}
	// Half the caps is no caps (on a database that is ours: marked).
	r.db.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
	r.db.Set("config/caps/global", map[string]any{"dailyMicros": 150_000_000})
	if _, _, err = executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--budget-mode", "enforce"); ExitCode(err) != ExitUserError {
		t.Fatalf("daily cap alone: exit %d, err %v", ExitCode(err), err)
	}
	// Both caps: enforce is allowed and written.
	r.db.Set("config/caps/global", map[string]any{"dailyMicros": 150_000_000, "perRunMicros": 20_000_000})
	out, _, err = executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--budget-mode", "enforce")
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
	out, _, err = executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--budget-mode", "off")
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
	_, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--budget-mode", "enforce")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--budget-mode observe") || len(r.applies(t)) != 0 {
		t.Fatalf("exit %d, err %v, applies %v", ExitCode(err), err, r.applies(t))
	}
	// Observe is fine, and the post-apply path would check the new database.
	if _, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--budget-mode", "observe"); err != nil {
		t.Fatal(err)
	}
}

// A second run changes nothing: no apply, no database write, no config
// rewrite, and no confirmation.
func TestInitFirebaseIdempotent(t *testing.T) {
	r := newFBRig(t)
	r.historyImage()
	if _, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--budget-mode", "observe"); err != nil {
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
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--budget-mode", "observe")
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
	if _, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--budget-mode", "observe"); err != nil {
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
	if _, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil {
		t.Fatal(err)
	}
	h, _ = r.tfvars(t)["history"].(map[string]any)
	if h["deploy_job"] != true || h["firebase_project"] != fpID || h["rtdb_url"] != r.db.URL {
		t.Errorf("with its image: history %v", h)
	}
}

// A plain fugaro init keeps the history account and job while the config
// records the Firebase project (else it would plan their deletion).
func TestInitKeepsBudgetBackend(t *testing.T) {
	r := newFBRig(t)
	r.historyImage()
	r.appendConfig(t, "budget:\n  mode: observe\n  firebase_project: "+fpID+"\n  rtdb_url: "+r.db.URL+"\n")
	if _, _, err := executeStdin(t, "", "init", "--yes"); err != nil {
		t.Fatal(err)
	}
	v := r.tfvars(t)
	h, _ := v["history"].(map[string]any)
	if v["enable_budget"] != true || h["deploy_job"] != true || h["rtdb_url"] != r.db.URL {
		t.Errorf("enable_budget %v, history %v", v["enable_budget"], h)
	}
	// Without a Firebase project, no budget backend in the installation.
	r = newFBRig(t)
	if _, _, err := executeStdin(t, "", "init", "--yes"); err != nil {
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
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
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
	if _, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes", "--allow-delete", addr); err != nil {
		t.Fatal(err)
	}
	if got := r.applies(t); !slices.Equal(got, []string{"firebase"}) {
		t.Errorf("applies = %v", got)
	}
}

func TestInitFirebaseFlagsRefused(t *testing.T) {
	r := newFBRig(t)
	for name, args := range map[string][]string{
		"budget-mode alone":  {"init", "--budget-mode", "observe"},
		"budget-admin alone": {"init", "--budget-admin", "user:a@example.com"},
		"bad mode":           {"init", "--firebase", fpID, "--budget-mode", "strict"},
		"bad id":             {"init", "--firebase", "Not A Project"},
		"with forget":        {"init", "--firebase", fpID, "--forget"},
		"with config-only":   {"init", "--firebase", fpID, "--config-only"},
		"with repo":          {"init", "--repo", "--firebase", fpID},
	} {
		_, _, err := executeStdin(t, "", args...)
		if ExitCode(err) != ExitUserError {
			t.Errorf("%s: exit %d, err %v", name, ExitCode(err), err)
		}
	}
	// Another Firebase project than the one the config records.
	r.appendConfig(t, "budget: { firebase_project: other-fp }\n")
	_, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "other-fp") {
		t.Errorf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "plan")) != 0 || len(r.ran(t, "apply")) != 0 {
		t.Errorf("terraform planned or applied: %q", r.calls(t))
	}
}

func TestInitFirebasePrintVarsNoCalls(t *testing.T) {
	r := newFBRig(t)
	out, errOut, err := executeStdin(t, "", "init", "--firebase", fpID, "--print-vars", "--budget-admin", "user:boss@example.com")
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

// init --repo writes the backend's address and web key into the workflow
// jobs of a repository whose budget is on, and nothing of it otherwise.
func TestInitRepoPassesRTDBEnv(t *testing.T) {
	for mode, want := range map[string]bool{"observe": true, "off": false} {
		isolateProjects(t, t.TempDir())
		path := writeProject(t, "aurora", "proj-1234")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		block := "budget:\n  mode: " + mode + "\n  firebase_project: aurora-fp\n  rtdb_url: https://aurora-fp-default-rtdb.firebaseio.com\n  firebase_api_key: " + fpAPIKey +
			"\n  token_signer: " + fpSigner + "\n  unreachable_grace: 2m\nbase_image: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-abc\n"
		if err := os.WriteFile(path, append(data, block...), 0o600); err != nil {
			t.Fatal(err)
		}
		root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: api-key }\nworkflows:\n  app: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n")
		testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
		t.Chdir(t.TempDir())
		out, _, err := execute(t, "init", "--repo", "--print-vars", "--project", "aurora", "--github-app-id", "42", root)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"FUGARO_RTDB_URL", "FUGARO_FIREBASE_API_KEY", "FUGARO_BUDGET_GRACE"} {
			if got := strings.Contains(out, key); got != want {
				t.Errorf("mode %s: %s in the job's env = %v, want %v", mode, key, got, want)
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
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
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
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
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
	out, _, err := executeStdin(t, initProjectName+"\n"+initProjectName+"\n", "init", "--firebase", fpID)
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
	if _, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes"); err != nil {
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
	out, _, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
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
			_, stderr, err := executeStdin(t, "", "init", "--firebase", fpID, "--yes")
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
