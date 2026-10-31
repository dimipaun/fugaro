package infra

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"

	"github.com/dimipaun/fugaro/deploy/terraform"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// checkGolden compares got with testdata/name, or rewrites it with -update.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Errorf("%s differs (rerun with -update to rewrite):\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// The tfvars fugaro update --firebase-project writes, byte for byte: a change
// here is a change to what Terraform is given.
func TestFirebaseTfvarsGolden(t *testing.T) {
	inst := installationSpec(t)
	inst.Launchers = []string{"user:launcher@example.com"}
	inst.Operators = []string{"user:operator@example.com", "user:launcher@example.com"}
	fs, err := Firebase(inst, FirebaseInputs{FP: "aurora-fp",
		Admins:         []string{"user:owner@example.com", "group:editors@example.com", "user:owner@example.com"},
		BudgetAdmins:   []string{"user:extra@example.com"},
		HistoryAccount: "fugaro-history@proj-1234.iam.gserviceaccount.com"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := FirebaseVars(fs)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "firebase.tfvars.json", data)

	// The installation's, with the history account only, and with the job.
	// It carries the local config's profiles, so the fixture and the golden
	// plan its script makes hold project jobs (plan task 3.2 step 5): one
	// job and one job service account per (profile, provider, account), and
	// no repository ever reaches this root.
	//
	// The third is the account axis's (plan task 2): one profile serving a
	// named Claude account beside the default one, so the fixture and its
	// golden plan hold an account job whose token is a secret mount. It is a
	// fixture of its own, and not a case of the second, so that the two plans
	// every existing installation is deploying stay byte for byte what they
	// were: this change adds jobs and replaces none (D9).
	for _, c := range []struct {
		name   string
		deploy bool
		spec   func(*testing.T) InstallationSpec
	}{
		{"installation-budget.tfvars.json", false, projectJobSpec},
		{"installation-budget-job.tfvars.json", true, projectJobSpec},
		{"installation-accounts.tfvars.json", true, projectAccountJobSpec},
	} {
		spec := c.spec(t)
		h, err := History(parseLC(t, m4LocalConfig+m5Additions))
		if err != nil {
			t.Fatal(err)
		}
		if c.deploy {
			h.DeployJob, h.FirebaseProject, h.RTDBURL = true, "aurora-fp", "https://aurora-fp-default-rtdb.firebaseio.com"
		}
		spec.EnableBudget, spec.History = true, &h
		data, err := InstallationVars(spec)
		if err != nil {
			t.Fatal(err)
		}
		checkGolden(t, c.name, data)
	}
}

// The coordinator ON (coordinator plan P5): the owner's opt-in, and the two
// fixtures the golden plans of that state are made from — the installation
// root with the coordinator's account, job and schedule, and the Firebase root
// with the account's one database grant. They are fixtures of their own, and
// not cases of the ones above, because the opt-in is off by default: every
// installation that has not asked plans exactly the fixtures above, byte for
// byte what it planned before the job existed.
func TestCoordinatorTfvarsGolden(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock)
	spec, err := Installation(lc, InstallOptions{BudgetBackend: true, Coordinator: true})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Coordinator == nil {
		t.Fatal("the opt-in planned no coordinator")
	}
	spec.History.DeployJob, spec.History.FirebaseProject, spec.History.RTDBURL = true, "aurora-fp", "https://aurora-fp-default-rtdb.firebaseio.com"
	spec.Coordinator.DeployJob, spec.Coordinator.FirebaseProject, spec.Coordinator.RTDBURL = true, "aurora-fp", "https://aurora-fp-default-rtdb.firebaseio.com"
	data, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "installation-coordinator.tfvars.json", data)

	inst := installationSpec(t)
	inst.Launchers = []string{"user:launcher@example.com"}
	inst.Operators = []string{"user:operator@example.com", "user:launcher@example.com"}
	cs, err := Coordinator(lc)
	if err != nil {
		t.Fatal(err)
	}
	inst.Coordinator = &cs
	fs, err := Firebase(inst, FirebaseInputs{FP: "aurora-fp",
		Admins:             []string{"user:owner@example.com", "group:editors@example.com", "user:owner@example.com"},
		BudgetAdmins:       []string{"user:extra@example.com"},
		HistoryAccount:     "fugaro-history@proj-1234.iam.gserviceaccount.com",
		CoordinatorAccount: "fugaro-coordinator@proj-1234.iam.gserviceaccount.com"})
	if err != nil {
		t.Fatal(err)
	}
	data, err = FirebaseVars(fs)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "firebase-coordinator.tfvars.json", data)
}

// TestTheCoordinatorIsOffByDefault is the security review's first finding
// (MAJOR-1): a budget installation that has not opted in gets no coordinator
// account, so no grant, no job and no schedule — and the Firebase root is
// handed no account to grant the database's admin to, even when the caller
// passes one. Only InstallOptions.Coordinator turns it on.
//
// Fails when Installation sets the coordinator for every budget backend, or
// Firebase derives the account without one.
func TestTheCoordinatorIsOffByDefault(t *testing.T) {
	lc := parseLC(t, m4LocalConfig+projectJobRepoLC+m5Additions+projectProfilesLCBlock)
	spec, err := Installation(lc, InstallOptions{BudgetBackend: true})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Coordinator != nil {
		t.Fatalf("a budget installation that did not opt in plans the coordinator: %+v", spec.Coordinator)
	}
	data, err := InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "coordinator") {
		t.Errorf("the installation's tfvars name the coordinator without the opt-in:\n%s", data)
	}
	for _, in := range []FirebaseInputs{
		{FP: "aurora-fp"},
		{FP: "aurora-fp", CoordinatorAccount: "fugaro-coordinator@proj-1234.iam.gserviceaccount.com"},
	} {
		fs, err := Firebase(spec, in)
		if err != nil {
			t.Fatal(err)
		}
		if fs.CoordinatorAccount != "" {
			t.Errorf("the Firebase root grants the database's admin to %q without the opt-in", fs.CoordinatorAccount)
		}
	}
	on, err := Installation(lc, InstallOptions{BudgetBackend: true, Coordinator: true})
	if err != nil {
		t.Fatal(err)
	}
	if on.Coordinator == nil {
		t.Fatal("the opt-in planned no coordinator")
	}
	if fs, err := Firebase(on, FirebaseInputs{FP: "aurora-fp"}); err != nil || fs.CoordinatorAccount != "fugaro-coordinator@proj-1234.iam.gserviceaccount.com" {
		t.Errorf("with the opt-in the Firebase root's account = %q (err %v), want the coordinator's", fs.CoordinatorAccount, err)
	}
	// And the opt-in needs the budget backend: it is the account the
	// Firebase root grants, and an installation without one has no root.
	if off, err := Installation(lc, InstallOptions{Coordinator: true}); err != nil || off.Coordinator != nil {
		t.Errorf("an installation without a budget backend planned the coordinator: %+v (err %v)", off.Coordinator, err)
	}
}

// An installation without the budget backend writes the tfvars it always did:
// no enable_budget, no history.
func TestInstallationVarsWithoutBudgetBackend(t *testing.T) {
	data, err := InstallationVars(installationSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "enable_budget") || strings.Contains(string(data), "history") {
		t.Errorf("tfvars = %s", data)
	}
}

func TestHistoryJobIsNotAWorkflowJob(t *testing.T) {
	h, err := History(parseLC(t, m4LocalConfig+m5Additions))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(h.Job, "fugaro-") {
		t.Errorf("the history job %s would be counted by ls and max_parallel", h.Job)
	}
	if h.AccountID != "fugaro-history" || h.SchedulerRegion != "us-east4" || h.Image != "us-east5-docker.pkg.dev/proj-1234/fugaro-base/history:latest" || h.DeployJob {
		t.Errorf("history = %+v", h)
	}
}

func TestAdminsFromPolicy(t *testing.T) {
	p := &crm.Policy{Bindings: []*crm.Binding{
		{Role: "roles/owner", Members: []string{"user:b@example.com", "user:a@example.com", "group:g@example.com", "domain:example.com", "allUsers", "allAuthenticatedUsers", "serviceAccount:ci@proj-1234.iam.gserviceaccount.com"}},
		{Role: "roles/editor", Members: []string{"user:a@example.com", "user:c@example.com", "serviceAccount:123-compute@developer.gserviceaccount.com", "serviceAccount:123@cloudservices.gserviceaccount.com",
			"serviceAccount:proj-1234@appspot.gserviceaccount.com", "deleted:user:gone@example.com?uid=1", "projectOwner:proj-1234", "user:*@example.com"}},
		{Role: "roles/viewer", Members: []string{"user:viewer@example.com"}},
		{Role: "roles/owner", Members: []string{"user:timed@example.com"}, Condition: &crm.Expr{Title: "temp", Expression: "request.time < timestamp('2030-01-01T00:00:00Z')"}},
	}}
	admins, skipped := AdminsFromPolicy(p)
	if want := []string{"group:g@example.com", "user:a@example.com", "user:b@example.com", "user:c@example.com"}; !slices.Equal(admins, want) {
		t.Errorf("admins = %v, want %v", admins, want)
	}
	for _, m := range admins {
		if strings.Contains(m, "gserviceaccount") || strings.HasPrefix(m, "domain:") || strings.Contains(m, "*") || m == "allUsers" {
			t.Errorf("admin %s", m)
		}
	}
	for _, want := range []string{"domain:example.com", "allUsers", "gserviceaccount.com", "user:timed@example.com (roles/owner, conditional)", "deleted:user"} {
		if !slices.ContainsFunc(skipped, func(s string) bool { return strings.Contains(s, want) }) {
			t.Errorf("%q not said among %v", want, skipped)
		}
	}
	if a, _ := AdminsFromPolicy(&crm.Policy{}); len(a) != 0 {
		t.Errorf("an empty policy: %v", a)
	}
}

func TestDefaultAccountRisks(t *testing.T) {
	p := &crm.Policy{Bindings: []*crm.Binding{
		{Role: "roles/editor", Members: []string{"serviceAccount:123-compute@developer.gserviceaccount.com", "serviceAccount:123@cloudbuild.gserviceaccount.com",
			"serviceAccount:proj-1234@appspot.gserviceaccount.com", "serviceAccount:123@cloudservices.gserviceaccount.com", "serviceAccount:ci@proj-1234.iam.gserviceaccount.com", "user:a@example.com"}},
		{Role: "roles/owner", Members: []string{"serviceAccount:123-compute@developer.gserviceaccount.com"}},
		{Role: "roles/cloudbuild.builds.builder", Members: []string{"serviceAccount:456@cloudbuild.gserviceaccount.com"}},
	}}
	want := []string{"123-compute@developer.gserviceaccount.com (roles/editor)", "123-compute@developer.gserviceaccount.com (roles/owner)",
		"123@cloudbuild.gserviceaccount.com (roles/editor)", "proj-1234@appspot.gserviceaccount.com (roles/editor)"}
	if got := DefaultAccountRisks(p); !slices.Equal(got, want) {
		t.Errorf("risks = %v, want %v", got, want)
	}
	if got := DefaultAccountRisks(&crm.Policy{}); len(got) != 0 {
		t.Errorf("an empty policy: %v", got)
	}
}

func TestCheckFirebaseMembers(t *testing.T) {
	for member, ok := range map[string]bool{
		"user:a@example.com":  true,
		"group:g@example.com": true,
		"serviceAccount:ci@other-project.iam.gserviceaccount.com":                   true,
		"serviceAccount:fugaro-aurora-web-1a2b3c@proj-1234.iam.gserviceaccount.com": false,
		"serviceAccount:fugaro-b-aurora-9f8e7d@proj-1234.iam.gserviceaccount.com":   false,
		"serviceAccount:fugaro-scheduler@proj-1234.iam.gserviceaccount.com":         false,
		"serviceAccount:fugaro-history@proj-1234.iam.gserviceaccount.com":           false,
		"serviceAccount:fugaro-token-signer@aurora-fp.iam.gserviceaccount.com":      false,
		"domain:example.com":     false,
		"user:*@example.com":     false,
		"allUsers":               false,
		"allAuthenticatedUsers":  false,
		"a@example.com":          false,
		"user:":                  false,
		"user:a b@example.com":   false,
		"projectOwner:proj-1234": false,
		"deleted:user:a@e.com":   false,
	} {
		err := CheckFirebaseMembers("proj-1234", "aurora-fp", map[string][]string{"launchers": {member}})
		if (err == nil) != ok {
			t.Errorf("%s: err = %v, want ok=%v", member, err, ok)
		}
	}
	var ue *UserError
	if err := CheckFirebaseMembers("proj-1234", "aurora-fp", map[string][]string{"a": {"domain:x"}}); !errors.As(err, &ue) {
		t.Errorf("a refusal is a user error: %v", err)
	}
}

func TestFirebaseSpecRefusals(t *testing.T) {
	inst := installationSpec(t)
	for name, in := range map[string]FirebaseInputs{
		"not an id":         {FP: "Not A Project"},
		"job account admin": {FP: "aurora-fp", BudgetAdmins: []string{"serviceAccount:fugaro-x@proj-1234.iam.gserviceaccount.com"}},
	} {
		if _, err := Firebase(inst, in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	inst.Launchers = []string{"domain:example.com"}
	if _, err := Firebase(inst, FirebaseInputs{FP: "aurora-fp"}); err == nil {
		t.Error("a domain: launcher reached the Firebase project")
	}
}

// The Firebase project may be the installation's own GCP project (user
// ruling 2026-10-04, design D3 revised): the same checks apply, the Firebase
// root leaves the two APIs the installation root enables to it, and the
// two-project layout's tfvars carry no such field.
func TestFirebaseSameProject(t *testing.T) {
	inst := installationSpec(t)
	in := FirebaseInputs{FP: inst.Project, Admins: []string{"user:owner@example.com"}, HistoryAccount: "fugaro-history@proj-1234.iam.gserviceaccount.com"}
	spec, err := Firebase(inst, in)
	if err != nil {
		t.Fatalf("the installation's own project refused: %v", err)
	}
	if spec.Project != inst.Project || !slices.Equal(spec.SkipAPIs, SharedAPIs) {
		t.Errorf("spec = %+v", spec)
	}
	data, err := FirebaseVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "firebase-same-project.tfvars.json", data)

	// A Fugaro-managed account is still refused as a budget admin: in one
	// project the job, build, scheduler, history and signer accounts all
	// live in the FP.
	for _, m := range []string{"fugaro-x@proj-1234.iam.gserviceaccount.com", "fugaro-token-signer@proj-1234.iam.gserviceaccount.com"} {
		in.BudgetAdmins = []string{"serviceAccount:" + m}
		if _, err := Firebase(inst, in); err == nil {
			t.Errorf("%s accepted as a budget admin of the same project", m)
		}
	}

	// Two projects: no skip_apis in the tfvars (belong's stay as they were).
	two, err := Firebase(inst, FirebaseInputs{FP: "aurora-fp"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := FirebaseVars(two)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "skip_apis") {
		t.Errorf("two-project tfvars name skip_apis: %s", b)
	}
}

var googleAPIRE = regexp.MustCompile(`"([a-z0-9]+\.googleapis\.com)"`)

// SharedAPIs is exactly the overlap of the APIs the two modules enable, read
// from the embedded Terraform: a new overlap would make both roots own one
// google_project_service in a same-project layout.
func TestSharedAPIsAreTheOverlap(t *testing.T) {
	apis := func(path string) map[string]bool {
		b, err := fs.ReadFile(terraform.FS, path)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, m := range googleAPIRE.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = true
		}
		return out
	}
	inst, fb := apis("gcp/modules/installation/apis.tf"), apis("gcp/modules/firebase/apis.tf")
	var overlap []string
	for a := range fb {
		if inst[a] {
			overlap = append(overlap, a)
		}
	}
	slices.Sort(overlap)
	if !slices.Equal(overlap, SharedAPIs) {
		t.Errorf("SharedAPIs = %v, but the two modules both enable %v", SharedAPIs, overlap)
	}
}

func TestDecodeFirebaseOutputs(t *testing.T) {
	good := map[string]json.RawMessage{"rtdb_url": json.RawMessage(`"https://x.firebaseio.com"`), "firebase_api_key": json.RawMessage(`"AIzaKey"`),
		"token_signer": json.RawMessage(`"s@x.iam.gserviceaccount.com"`), "firebase_project": json.RawMessage(`"aurora-fp"`)}
	o, err := DecodeFirebaseOutputs(good)
	if err != nil || o.RTDBURL != "https://x.firebaseio.com" || o.FirebaseProject != "aurora-fp" {
		t.Fatalf("%+v, %v", o, err)
	}
	if _, err := DecodeFirebaseOutputs(nil); !errors.Is(err, ErrNoFirebaseOutputs) {
		t.Errorf("empty: %v", err)
	}
	bad := map[string]json.RawMessage{}
	for k, v := range good {
		bad[k] = v
	}
	bad["rtdb_url"] = json.RawMessage(`null`)
	if _, err := DecodeFirebaseOutputs(bad); err == nil {
		t.Error("a null rtdb_url decoded")
	}
	delete(bad, "rtdb_url")
	if _, err := DecodeFirebaseOutputs(bad); err == nil {
		t.Error("a missing rtdb_url decoded")
	}
}

func planDeleting(addr string) *tf.Plan {
	return &tf.Plan{ResourceChanges: []tf.ResourceChange{{Address: addr, Change: tf.Change{Actions: []string{"delete"}}}}}
}

func TestDeleteHintsForFirebaseGrants(t *testing.T) {
	hints := DeleteHints(planDeleting(`module.firebase.google_project_iam_member.admin["user:a@example.com"]`), nil)
	if len(hints) != 1 || !strings.Contains(hints[0], "owners and editors") || !strings.Contains(hints[0], "--budget-admin") {
		t.Errorf("hints = %v", hints)
	}
	hints = DeleteHints(planDeleting("module.installation.google_cloud_run_v2_job.history[0]"), nil)
	if len(hints) != 1 || !strings.Contains(hints[0], "--firebase-project") {
		t.Errorf("hints = %v", hints)
	}
}

// --- the database -----------------------------------------------------------

func newDB(t *testing.T, mode string) (*DB, *gcpfake.RTDB) {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	return NewDB(c, "aurora", "proj-1234", mode), f
}

func deploy(t *testing.T, d *DB) []DBAction {
	t.Helper()
	acts, _, err := d.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	return acts
}

func TestDBDeploysAndThenChangesNothing(t *testing.T) {
	d, f := newDB(t, "")
	acts := deploy(t, d)
	var paths []string
	for _, a := range acts {
		paths = append(paths, a.Path)
	}
	// The rules are last of the database's own writes; the one action after
	// them is the rules-generation marker, which must not be written unless
	// the deploy succeeded (TestRulesMarkerOnlyAfterTheRulesDeploy in
	// internal/cli drives the failing case through init).
	wantPaths := []string{"fugaro/mark", "fugaro/project", "config/mode", "config/limits", "rules",
		projectpolicy.PathRulesMarker(), rules.PathCredsRulesMarker()}
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("actions = %v, want %v (the rules last, their marker after them)", paths, wantPaths)
	}
	if f.Value("fugaro/project") != "aurora" || f.Value("config/mode") != "observe" || f.RulePuts() != 1 {
		t.Errorf("database = %v, rules %d", f.Value(""), f.RulePuts())
	}
	if got := f.Value(projectpolicy.PathRulesMarker()); got != json.Number(strconv.Itoa(projectpolicy.RulesVersion)) {
		t.Errorf("%s = %#v, want %d (RulesVersion)", projectpolicy.PathRulesMarker(), got, projectpolicy.RulesVersion)
	}
	if got := f.Value(rules.PathCredsRulesMarker()); got != json.Number(strconv.Itoa(rules.CredsRulesVersion)) {
		t.Errorf("%s = %#v, want %d (CredsRulesVersion): the credentials children's generation", rules.PathCredsRulesMarker(), got, rules.CredsRulesVersion)
	}
	lim, _ := f.Value(budget.PathLimits).(map[string]any)
	if len(lim) != 3 || lim["maxReserveMicros"] == nil {
		t.Errorf("config/limits = %v, want the two markers beside maxReserveMicros", lim)
	}
	want, _ := rules.Generate()
	if !SameJSON(f.Rules(), want) {
		t.Error("the deployed rules are not the generated ones")
	}
	d2 := NewDB(d.c, "aurora", "proj-1234", "")
	if acts, _, err := d2.Plan(context.Background()); err != nil || len(acts) != 0 {
		t.Fatalf("a second plan: %v, %v", acts, err)
	}
}

func TestDBModeIsChangedOnlyOnRequest(t *testing.T) {
	// Enforce on a database without both global caps is refused, writing nothing.
	d, f := newDB(t, "enforce")
	f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "aurora", "gcp_project": "proj-1234", "version": 1})
	for _, seed := range []any{nil, map[string]any{"dailyMicros": 150_000_000}, map[string]any{"perRunMicros": 20_000_000}} {
		f.Set("config/caps/global", seed)
		_, _, err := d.Plan(context.Background())
		var ue *UserError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "fugaro budget set --global") || f.Value("fugaro/project") != nil || f.RulePuts() != 0 {
			t.Fatalf("caps %v: err %v; the refusal must come before any write", seed, err)
		}
	}
	f.Set("config/caps/global", map[string]any{"dailyMicros": 150_000_000, "perRunMicros": 20_000_000})
	acts, _, err := d.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(acts, func(a DBAction) bool { return a.Path == "config/mode" && strings.Contains(a.Text, "enforce") }) {
		t.Errorf("actions = %v", acts)
	}
	if err := d.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A run without a mode keeps enforce; asking for observe changes it, and
	// says old -> new.
	if _, _, err := NewDB(d.c, "aurora", "proj-1234", "").Plan(context.Background()); err != nil {
		t.Errorf("keeps: %v", err)
	}
	if f.Value("config/mode") != "enforce" {
		t.Errorf("mode = %v", f.Value("config/mode"))
	}
	acts, _, err = NewDB(d.c, "aurora", "proj-1234", "observe").Plan(context.Background())
	if err != nil || len(acts) != 1 || !strings.Contains(acts[0].Text, "enforce -> observe") {
		t.Errorf("acts %v, err %v", acts, err)
	}
}

// An existing limits node keeps its other fields; an owner-set max reserve is
// not touched.
func TestDBLimitsKeepOwnerValues(t *testing.T) {
	d, f := newDB(t, "")
	f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "aurora", "gcp_project": "proj-1234", "version": 1})
	f.Set("config/limits", map[string]any{"other": 7})
	deploy(t, d)
	lim, _ := f.Value("config/limits").(map[string]any)
	if lim["other"] == nil || lim["maxReserveMicros"] == nil {
		t.Errorf("limits = %v", lim)
	}
	f.Set("config/limits", map[string]any{"maxReserveMicros": 1_000_000})
	acts, _, err := NewDB(d.c, "aurora", "proj-1234", "").Plan(context.Background())
	if err != nil || slices.ContainsFunc(acts, func(a DBAction) bool { return a.Path == "config/limits" }) {
		t.Errorf("the owner's max reserve would be rewritten: %v, %v", acts, err)
	}
}

// A deployed marker *above* this binary's RulesVersion is a newer Fugaro's
// rules. fugaro update --firebase-project deploys the rules the binary carries,
// so an older binary would put its own, older document over the newer
// installation's and record generation RulesVersion in the marker over the newer
// number — the downgrade `fugaro doctor` warns about and tells the owner not to
// cause ("do not re-run 'fugaro update --firebase-project' with this binary",
// doctorRulesSkew).
// The deploy path refuses instead: nothing is planned, nothing is written, and
// the rules and the marker are left exactly as the newer installation left
// them.
//
// A marker above this binary's RulesVersion needs a future release's write;
// the boundary is what this test pins before that release exists.
//
// Hand mutation this fails on: dropping the refusal (or widening it to `>=`),
// which plans the rules deploy over the newer document and the marker write
// down to this generation.
func TestDBRefusesToDeployOverNewerRules(t *testing.T) {
	d, f := newDB(t, "")
	deploy(t, d)
	if n := f.RulePuts(); n != 1 {
		t.Fatalf("rules deployed %d times, want 1 (the fixture)", n)
	}
	// A newer Fugaro has been here: its marker, and (in a real installation) its
	// rules document.
	newer := projectpolicy.RulesVersion + 1
	f.Set(projectpolicy.PathRulesMarker(), newer)
	before, _ := json.Marshal(f.Value(""))
	puts := len(f.Requests())

	acts, _, err := d.Plan(context.Background())
	var ue *UserError
	if !errors.As(err, &ue) {
		t.Fatalf("Plan = (%v, %v), want a refusal: an older binary must not deploy its rules over a newer installation's", acts, err)
	}
	for _, want := range []string{projectpolicy.PathRulesMarker(), strconv.Itoa(newer), strconv.Itoa(projectpolicy.RulesVersion)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if len(acts) != 0 {
		t.Errorf("acts = %v, want none: a refused plan proposes nothing to apply", acts)
	}
	after, _ := json.Marshal(f.Value(""))
	if string(before) != string(after) || f.RulePuts() != 1 {
		t.Errorf("the refusal wrote: database %s -> %s, rules put %d times", before, after, f.RulePuts())
	}
	for _, q := range f.Requests()[puts:] {
		if q.Method == "PUT" || q.Method == "PATCH" {
			t.Errorf("the refused plan wrote %s /%s", q.Method, q.Path)
		}
	}
	// The state is still the newer installation's, so the database is not left
	// in a half-read state either: a marker at this generation plans normally.
	f.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	if _, _, err := NewDB(d.c, "aurora", "proj-1234", "").Plan(context.Background()); err != nil {
		t.Errorf("a database at this generation is refused: %v", err)
	}
}

// The credentials marker (credentials-service plan, task C1.3) is written the
// way the rules-generation marker is: after the rules PUT succeeds, and on the
// no-deploy path too, when it is absent. It records the generation of the
// credentials children (/creds denied, the registry entry's `credential` and
// `waiting_until`, the by-tier counters), which a cascade launch, the init
// stage and doctor read before a run may start -- a marker of its own rather
// than a `RulesVersion` bump, which would drop every published policy on an
// installation whose owner has not redeployed yet.
//
// Hand mutation this fails on: writing the marker before the rules (a failed
// deploy then claims rules that are not there), or dropping the write
// altogether (a cascade launch reads nothing and is refused on an installation
// that has deployed).
func TestFirebaseDeployWritesCredsMarker(t *testing.T) {
	d, f := newDB(t, "")
	acts := deploy(t, d)
	rulesAt, credsAt := -1, -1
	for i, a := range acts {
		switch a.Path {
		case "rules":
			rulesAt = i
		case rules.PathCredsRulesMarker():
			credsAt = i
		}
	}
	if rulesAt < 0 || credsAt < 0 {
		t.Fatalf("actions = %v, want the rules deploy and the credentials marker", acts)
	}
	if credsAt < rulesAt {
		t.Errorf("the credentials marker (%d) is planned before the rules deploy (%d): it must not claim rules that are not there", credsAt, rulesAt)
	}
	if !strings.Contains(acts[credsAt].Text, rules.PathCredsRulesMarker()) {
		t.Errorf("the marker's action text does not name it: %q", acts[credsAt].Text)
	}
	if got := f.Value(rules.PathCredsRulesMarker()); got != json.Number(strconv.Itoa(rules.CredsRulesVersion)) {
		t.Errorf("%s = %#v, want %d", rules.PathCredsRulesMarker(), got, rules.CredsRulesVersion)
	}
}

// A database whose deployed rules already equal the generated document needs no
// deploy -- and a credentials marker that is absent is still written, because
// the document that carries the children is the one running. That is the path
// an installation takes when it upgrades the CLI and its rules were already at
// this generation.
//
// Hand mutation this fails on: writing the marker only when a rules deploy was
// planned.
func TestFirebaseNoDeployWritesAbsentCredsMarker(t *testing.T) {
	d, f := newDB(t, "")
	deploy(t, d)
	f.Set(rules.PathCredsRulesMarker(), nil) // the marker an earlier init never wrote
	d2 := NewDB(d.c, "aurora", "proj-1234", "")
	acts, _, err := d2.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 || acts[0].Path != rules.PathCredsRulesMarker() {
		t.Fatalf("actions = %v, want the credentials marker alone (no rules deploy: the deployed document is the generated one)", acts)
	}
	if err := d2.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.Value(rules.PathCredsRulesMarker()); got != json.Number(strconv.Itoa(rules.CredsRulesVersion)) {
		t.Errorf("%s = %#v, want %d", rules.PathCredsRulesMarker(), got, rules.CredsRulesVersion)
	}
	if n := f.RulePuts(); n != 1 {
		t.Errorf("the rules were deployed %d times, want 1 (the fixture's): the marker write must not redeploy", n)
	}
}

// A credentials marker above this binary's CredsRulesVersion is a newer
// Fugaro's, and the same refusal as the rules marker's applies: this binary's
// document does not carry the children that marker describes, so deploying it
// would leave a `cascade` launch passing a gate on rules that are not there.
// Nothing is planned and nothing is written.
//
// Hand mutation this fails on: dropping the refusal (the plan then writes this
// binary's older document and the marker down to this version), or widening it
// to `>=` (which refuses a database at this generation).
func TestFirebasePlanRefusesNewerCredsMarker(t *testing.T) {
	d, f := newDB(t, "")
	deploy(t, d)
	newer := rules.CredsRulesVersion + 1
	f.Set(rules.PathCredsRulesMarker(), newer)
	before, _ := json.Marshal(f.Value(""))
	puts := len(f.Requests())

	acts, _, err := d.Plan(context.Background())
	var ue *UserError
	if !errors.As(err, &ue) {
		t.Fatalf("Plan = (%v, %v), want a refusal: this binary must not deploy over a newer credentials generation", acts, err)
	}
	for _, want := range []string{rules.PathCredsRulesMarker(), strconv.Itoa(newer), strconv.Itoa(rules.CredsRulesVersion)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if len(acts) != 0 {
		t.Errorf("acts = %v, want none", acts)
	}
	after, _ := json.Marshal(f.Value(""))
	if string(before) != string(after) || f.RulePuts() != 1 {
		t.Errorf("the refusal wrote: database %s -> %s, rules put %d times", before, after, f.RulePuts())
	}
	for _, q := range f.Requests()[puts:] {
		if q.Method == "PUT" || q.Method == "PATCH" {
			t.Errorf("the refused plan wrote %s /%s", q.Method, q.Path)
		}
	}
	// A marker at this generation plans normally, and one below it is a database
	// whose rules predate the children: that is the deploy this binary is for.
	for _, seed := range []int{rules.CredsRulesVersion, rules.CredsRulesVersion - 1} {
		f.Set(rules.PathCredsRulesMarker(), seed)
		if _, _, err := NewDB(d.c, "aurora", "proj-1234", "").Plan(context.Background()); err != nil {
			t.Errorf("a credentials marker at %d is refused: %v", seed, err)
		}
	}
}

// The refusals below each prescribe a remedy, and a remedy has to be a command
// that runs: the retired verb is a hidden tombstone that prints one sentence and
// exits 1 (internal/cli/retired.go, W1.2), so a refusal that says "re-run init"
// sends the operator to a command that cannot carry the remedy out. That bare
// spelling is also the one the rename census cannot see: its pattern wants the
// binary in front of the verb or a flag after it, and it enforces both of those
// tree-wide already, so nothing else in the tree fails when one of these
// messages grows the retired verb back. This check is case-insensitive and
// covers the imperative ("Run init with --budget-mode observe") as well as the
// compound ("re-run init").
//
// Hand mutation this fails on: putting "and re-run init with the newer binary"
// back in either marker refusal, "then rerun init" back in the Firestore one, or
// "Run init with --budget-mode observe" back in the no-caps one: the message
// stops naming a door that runs, and the retired-verb check fires.
func TestRefusalsPrescribeACommandThatRuns(t *testing.T) {
	var messages []string
	collect := func(what string, err error) {
		if err == nil {
			t.Fatalf("%s is not refused", what)
		}
		messages = append(messages, err.Error())
	}

	// The rules marker above this binary's generation.
	d, f := newDB(t, "")
	deploy(t, d)
	f.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion+1)
	_, _, err := d.Plan(context.Background())
	collect("a rules marker above this binary's generation", err)

	// The credentials marker above this binary's generation.
	d2, f2 := newDB(t, "")
	deploy(t, d2)
	f2.Set(rules.PathCredsRulesMarker(), rules.CredsRulesVersion+1)
	_, _, err = d2.Plan(context.Background())
	collect("a credentials marker above this binary's generation", err)

	// A Firestore release that is not deny-all.
	fr := gcpfake.NewFirebaseRules(t, "aurora")
	fr.SeedRelease("rules_version = '2';\nservice cloud.firestore { match /databases/{d}/documents { match /{x=**} { allow read, write: if true; } } }")
	rc := &RulesClient{Endpoint: fr.URL, Project: "aurora", HTTP: fr.Client()}
	_, err = rc.inspect(context.Background(), FirestoreRules)
	collect("a Firestore release that is not deny-all", err)

	// A door's own plan under --budget-mode enforce, with no global caps to
	// compare a run against.
	collect("--budget-mode enforce without the database's global caps", NoCapsForEnforce())

	if len(messages) != 4 {
		t.Fatalf("collected %d refusal(s), want 4", len(messages))
	}
	for _, msg := range messages {
		if !strings.Contains(msg, "fugaro update --firebase-project") {
			t.Errorf("the refusal does not name 'fugaro update --firebase-project', the door that deploys the rules it refuses: %s", msg)
		}
		// And it names the project that door takes: --firebase-project is a
		// string flag with no default, so a message that stops at the flag
		// prints a command that cannot run — the same failure as the retired
		// verb, one token further along.
		for i := 0; ; {
			j := strings.Index(msg[i:], firebaseFlag)
			if j < 0 {
				break
			}
			rest := strings.TrimLeft(msg[i+j+len(firebaseFlag):], " ")
			if rest == "" || !firebaseFlagValue(rest) {
				t.Errorf("the refusal stops at '--firebase-project', which takes a project id: the operator gets a command that fails on a missing argument: %s", msg)
			}
			i += j + len(firebaseFlag)
		}
		for _, bad := range []string{"re-run init", "rerun init", "run init"} {
			if strings.Contains(strings.ToLower(msg), bad) {
				t.Errorf("the refusal sends the operator to %q, and the retired verb is a hidden tombstone that exits 1: name the door that deploys the rules instead: %s", bad, msg)
			}
		}
	}
}

// firebaseFlag is the door these refusals prescribe, and firebaseFlagValue
// judges what follows it: a project id (a real one, or the placeholder a
// message with no config to read one from prints) and never a quote, a
// backtick or an English word carrying the sentence on.
const firebaseFlag = "fugaro update --firebase-project"

func firebaseFlagValue(rest string) bool {
	word := rest
	if k := strings.IndexAny(word, " \t\n"); k >= 0 {
		word = word[:k]
	}
	word = strings.Trim(word, ".,;:")
	if word == "" || !(word[0] == '<' || word[0] == '-' || word[0] >= '0' && word[0] <= '9' || word[0] >= 'a' && word[0] <= 'z' || word[0] >= 'A' && word[0] <= 'Z') {
		return false
	}
	for _, prose := range []string{"again", "with", "and", "would", "is", "to", "refuses", "refused", "never", "first"} {
		if word == prose {
			return false
		}
	}
	return true
}

func TestDBRefusals(t *testing.T) {
	for name, seed := range map[string]func(f *gcpfake.RTDB){
		"unmarked data":    func(f *gcpfake.RTDB) { f.Set("data", "x") },
		"unmarked project": func(f *gcpfake.RTDB) { f.Set("fugaro/project", "aurora") },
		"another's mark": func(f *gcpfake.RTDB) {
			f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "other", "gcp_project": "proj-1234", "version": 1})
		},
		"someone else's": func(f *gcpfake.RTDB) {
			f.Set("fugaro/mark", map[string]any{"managed_by": "someone", "project": "aurora", "gcp_project": "proj-1234", "version": 1})
		},
		"a mark of a future": func(f *gcpfake.RTDB) {
			f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "aurora", "gcp_project": "proj-1234", "version": 2})
		},
		"same name, other GCP project": func(f *gcpfake.RTDB) {
			f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "aurora", "gcp_project": "elsewhere", "version": 1})
		},
		"a mark without a GCP project": func(f *gcpfake.RTDB) {
			f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "aurora", "version": 1})
		},
		"a string mark": func(f *gcpfake.RTDB) { f.Set("fugaro/mark", "yes") },
		"foreign name": func(f *gcpfake.RTDB) {
			f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "aurora", "gcp_project": "proj-1234", "version": 1})
			f.Set("fugaro/project", "other")
		},
	} {
		t.Run(name, func(t *testing.T) {
			d, f := newDB(t, "")
			seed(f)
			before, _ := json.Marshal(f.Value(""))
			_, _, err := d.Plan(context.Background())
			var ue *UserError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v", err)
			}
			after, _ := json.Marshal(f.Value(""))
			if string(before) != string(after) || f.RulePuts() != 0 {
				t.Error("a refusal wrote")
			}
		})
	}
}

// Apply stops at a node that changed since the plan, and leaves the rest.
func TestDBApplyStopsOnAChangedNode(t *testing.T) {
	d, f := newDB(t, "")
	if _, _, err := d.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "aurora", "gcp_project": "proj-1234", "version": 1, "extra": true})
	err := d.Apply(context.Background())
	var ue *UserError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "changed while fugaro was running") {
		t.Fatalf("err = %v", err)
	}
	if f.RulePuts() != 0 {
		t.Error("the rules went in over a database that changed")
	}
}

func TestDBPermissionIsExplained(t *testing.T) {
	d, f := newDB(t, "")
	if _, _, err := d.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.DenyNext(1)
	err := d.Apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "firebasedatabase.admin") {
		t.Fatalf("err = %v", err)
	}
}

// Outputs read from a state bucket are checked as a set for the one Firebase
// project: a foreign but well-formed triple (another database, another signer,
// another project) is refused whole, field by field and together.
func TestFirebaseOutputsMustBelongToOneProject(t *testing.T) {
	good := FirebaseOutputs{RTDBURL: "https://aurora-fp-default-rtdb.firebaseio.com", FirebaseAPIKey: "AIzaSyA0123456789abcdefghijklmnopqrstu",
		TokenSigner: "fugaro-token-signer@aurora-fp.iam.gserviceaccount.com", FirebaseProject: "aurora-fp"}
	if err := CheckFirebaseOutputsFor(good, "", false); err != nil {
		t.Fatalf("own project, derived: %v", err)
	}
	regional := good
	regional.RTDBURL = "https://aurora-fp-default-rtdb.europe-west1.firebasedatabase.app"
	if err := CheckFirebaseOutputsFor(regional, "aurora-fp", false); err != nil {
		t.Fatalf("regional form: %v", err)
	}
	foreign := map[string]func(o *FirebaseOutputs){
		"database": func(o *FirebaseOutputs) { o.RTDBURL = "https://attacker-db.firebaseio.com" },
		"regional database of another project": func(o *FirebaseOutputs) {
			o.RTDBURL = "https://other-fp-default-rtdb.europe-west1.firebasedatabase.app"
		},
		"database with a path": func(o *FirebaseOutputs) { o.RTDBURL = "https://aurora-fp-default-rtdb.firebaseio.com/x" },
		"signer":               func(o *FirebaseOutputs) { o.TokenSigner = "attacker@other-fp.iam.gserviceaccount.com" },
		"signer of our project, another account": func(o *FirebaseOutputs) {
			o.TokenSigner = "evil@aurora-fp.iam.gserviceaccount.com"
		},
		"project": func(o *FirebaseOutputs) { o.FirebaseProject = "other-fp" },
		"all three": func(o *FirebaseOutputs) {
			o.RTDBURL, o.TokenSigner, o.FirebaseProject = "https://attacker-db.firebaseio.com", "attacker@other-fp.iam.gserviceaccount.com", "other-fp"
		},
	}
	for name, mut := range foreign {
		o := good
		mut(&o)
		// Passed --firebase-project aurora-fp: nothing else is accepted.
		if err := CheckFirebaseOutputsFor(o, "aurora-fp", false); err == nil {
			t.Errorf("%s accepted with --firebase-project aurora-fp", name)
		}
		// Derived from the state: the three still have to agree.
		if name != "project" {
			if err := CheckFirebaseOutputsFor(o, "", false); err == nil {
				t.Errorf("%s accepted when derived", name)
			}
		}
	}
	// A fake's loopback database is allowed only where fakes are.
	loop := good
	loop.RTDBURL = "http://127.0.0.1:9000"
	if CheckFirebaseOutputsFor(loop, "aurora-fp", false) == nil || CheckFirebaseOutputsFor(loop, "aurora-fp", true) != nil {
		t.Error("loopback rule")
	}
}
