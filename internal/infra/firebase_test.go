package infra

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	crm "google.golang.org/api/cloudresourcemanager/v1"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/tf"
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

// The tfvars init --firebase writes, byte for byte: a change here is a change
// to what Terraform is given.
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
	for name, deploy := range map[string]bool{"installation-budget.tfvars.json": false, "installation-budget-job.tfvars.json": true} {
		spec := installationSpec(t)
		h, err := History(parseLC(t, m4LocalConfig+m5Additions))
		if err != nil {
			t.Fatal(err)
		}
		if deploy {
			h.DeployJob, h.FirebaseProject, h.RTDBURL = true, "aurora-fp", "https://aurora-fp-default-rtdb.firebaseio.com"
		}
		spec.EnableBudget, spec.History = true, &h
		data, err := InstallationVars(spec)
		if err != nil {
			t.Fatal(err)
		}
		checkGolden(t, name, data)
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
		"the installation's own project": {FP: "proj-1234"},
		"not an id":                      {FP: "Not A Project"},
		"job account admin":              {FP: "aurora-fp", BudgetAdmins: []string{"serviceAccount:fugaro-x@proj-1234.iam.gserviceaccount.com"}},
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
	if len(hints) != 1 || !strings.Contains(hints[0], "--firebase") {
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
	return NewDB(c, "aurora", mode), f
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
	if want := []string{"fugaro/mark", "fugaro/project", "config/mode", "config/limits", "rules"}; !slices.Equal(paths, want) {
		t.Fatalf("actions = %v, want %v (the rules last)", paths, want)
	}
	if f.Value("fugaro/project") != "aurora" || f.Value("config/mode") != "observe" || f.RulePuts() != 1 {
		t.Errorf("database = %v, rules %d", f.Value(""), f.RulePuts())
	}
	want, _ := rules.Generate()
	if !sameJSON(f.Rules(), want) {
		t.Error("the deployed rules are not the generated ones")
	}
	d2 := NewDB(d.c, "aurora", "")
	if acts, _, err := d2.Plan(context.Background()); err != nil || len(acts) != 0 {
		t.Fatalf("a second plan: %v, %v", acts, err)
	}
}

func TestDBModeIsChangedOnlyOnRequest(t *testing.T) {
	d, f := newDB(t, "enforce")
	acts, warnings, err := d.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "global caps") {
		t.Errorf("warnings = %v: enforce without caps must say every run halts", warnings)
	}
	if !slices.ContainsFunc(acts, func(a DBAction) bool { return a.Path == "config/mode" && strings.Contains(a.Text, "enforce") }) {
		t.Errorf("actions = %v", acts)
	}
	if err := d.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Caps set: no warning. A run without a mode keeps enforce; asking for
	// observe changes it, and says old -> new.
	f.Set("config/caps/global", map[string]any{"dailyMicros": 150_000_000, "perRunMicros": 20_000_000})
	if _, w, err := NewDB(d.c, "aurora", "").Plan(context.Background()); err != nil || len(w) != 0 {
		t.Errorf("keeps: %v, %v", w, err)
	}
	if f.Value("config/mode") != "enforce" {
		t.Errorf("mode = %v", f.Value("config/mode"))
	}
	acts, _, err = NewDB(d.c, "aurora", "observe").Plan(context.Background())
	if err != nil || len(acts) != 1 || !strings.Contains(acts[0].Text, "enforce -> observe") {
		t.Errorf("acts %v, err %v", acts, err)
	}
}

// An existing limits node keeps its other fields; an owner-set max reserve is
// not touched.
func TestDBLimitsKeepOwnerValues(t *testing.T) {
	d, f := newDB(t, "")
	f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "aurora", "version": 1})
	f.Set("config/limits", map[string]any{"other": 7})
	deploy(t, d)
	lim, _ := f.Value("config/limits").(map[string]any)
	if lim["other"] == nil || lim["maxReserveMicros"] == nil {
		t.Errorf("limits = %v", lim)
	}
	f.Set("config/limits", map[string]any{"maxReserveMicros": 1_000_000})
	acts, _, err := NewDB(d.c, "aurora", "").Plan(context.Background())
	if err != nil || slices.ContainsFunc(acts, func(a DBAction) bool { return a.Path == "config/limits" }) {
		t.Errorf("the owner's max reserve would be rewritten: %v, %v", acts, err)
	}
}

func TestDBRefusals(t *testing.T) {
	for name, seed := range map[string]func(f *gcpfake.RTDB){
		"unmarked data":    func(f *gcpfake.RTDB) { f.Set("data", "x") },
		"unmarked project": func(f *gcpfake.RTDB) { f.Set("fugaro/project", "aurora") },
		"another's mark": func(f *gcpfake.RTDB) {
			f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "other", "version": 1})
		},
		"someone else's": func(f *gcpfake.RTDB) {
			f.Set("fugaro/mark", map[string]any{"managed_by": "someone", "project": "aurora", "version": 1})
		},
		"a mark of a future": func(f *gcpfake.RTDB) {
			f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "aurora", "version": 2})
		},
		"a string mark": func(f *gcpfake.RTDB) { f.Set("fugaro/mark", "yes") },
		"foreign name": func(f *gcpfake.RTDB) {
			f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "aurora", "version": 1})
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
	f.Set("fugaro/mark", map[string]any{"managed_by": "fugaro", "project": "aurora", "version": 1, "extra": true})
	err := d.Apply(context.Background())
	var ue *UserError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "changed while init was running") {
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
