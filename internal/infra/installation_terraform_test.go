//go:build terraform

package infra

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestInstallationVarsPlan plans the installation root with the tfvars
// fugaro init writes, against a mock provider, so a type or validation
// mismatch between Go and the HCL fails here rather than in a live plan.
func TestInstallationVarsPlan(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Fatalf("this test needs terraform on PATH: %v", err)
	}
	// forget is the rollback's tfvars (log_isolation=false); the others
	// leave log isolation to the root's default (on).
	for _, c := range []struct {
		name                     string
		adopt, forget            bool
		budget, deployHistoryJob bool
	}{{"fresh", false, false, false, false}, {"adopt_legacy_registry", true, false, false, false}, {"forget", false, true, false, false},
		{"budget_backend", false, false, true, false}, {"budget_backend_with_job", false, false, true, true}} {
		t.Run(c.name, func(t *testing.T) {
			spec := installationSpec(t)
			spec.AdoptLegacyRegistry = c.adopt
			spec.Launchers = []string{"user:launcher@example.com"}
			spec.Operators = []string{"user:operator@example.com"}
			email := "ops@example.com"
			spec.AlertEmail = &email
			if c.budget {
				h := History0(t, c.deployHistoryJob)
				spec.EnableBudget, spec.History = true, &h
			}
			if c.forget {
				spec = ForgetSpec(spec)
			}
			data, err := InstallationVars(spec)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			writeTree(t, dir)
			root := filepath.Join(dir, "gcp/roots/installation")
			// Only the generated test runs.
			if err := os.RemoveAll(filepath.Join(root, "tests")); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "tests", "vars.tftest.hcl"), []byte(varsTest(t, data, c.adopt, !c.forget, c.budget)), 0o644); err != nil {
				t.Fatal(err)
			}
			tfdata := filepath.Join(t.TempDir(), "tfdata")
			for _, args := range [][]string{{"init", "-backend=false", "-lockfile=readonly", "-input=false"}, {"test", "-no-color"}} {
				cmd := exec.Command("terraform", append([]string{"-chdir=" + root}, args...)...)
				cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1", "TF_DATA_DIR="+tfdata)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("terraform %s: %v\n%s", args[0], err, out)
				}
			}
		})
	}
}

// varsTest is a test file that sets every variable from the tfvars (a JSON
// value is an HCL expression) and plans the root with a mock provider.
func varsTest(t *testing.T, data []byte, adopt, logIsolation, budget bool) string {
	t.Helper()
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(data, &vars); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("mock_provider \"google\" {}\n\n")
	if budget {
		// The computed names the plan needs (the mock provider leaves them unknown).
		for _, acct := range []struct{ res, id string }{{"google_service_account.history[0]", HistoryAccountID}, {"google_service_account.scheduler", SchedulerServiceAccountID}} {
			b.WriteString("override_resource {\n  target          = module.installation." + acct.res + "\n  override_during = plan\n  values = {\n")
			b.WriteString("    name   = \"projects/proj-1234/serviceAccounts/" + acct.id + "@proj-1234.iam.gserviceaccount.com\"\n")
			b.WriteString("    email  = \"" + acct.id + "@proj-1234.iam.gserviceaccount.com\"\n")
			b.WriteString("    member = \"serviceAccount:" + acct.id + "@proj-1234.iam.gserviceaccount.com\"\n  }\n}\n\n")
		}
	}
	b.WriteString("variables {\n")
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		b.WriteString("  " + k + " = " + string(vars[k]) + "\n")
	}
	b.WriteString("}\n\nrun \"plan\" {\n  command = plan\n\n  assert {\n")
	b.WriteString("    condition     = output.runs_bucket == \"fugaro-runs-proj-1234\"\n")
	b.WriteString("    error_message = \"the runs bucket is not the tfvars'\"\n  }\n")
	legacy := "null"
	if adopt {
		legacy = "\"" + LegacyRegistry + "\""
	}
	b.WriteString("  assert {\n    condition     = output.legacy_registry == " + legacy + "\n")
	b.WriteString("    error_message = \"adopt_legacy_registry did not reach the module\"\n  }\n")
	view := "output.log_view == null"
	if logIsolation {
		view = "output.log_view != null"
	}
	b.WriteString("  assert {\n    condition     = " + view + "\n")
	b.WriteString("    error_message = \"log_isolation did not reach the module\"\n  }\n}\n")
	return b.String()
}

// History0 is the history job's spec the tests use, with or without the job.
func History0(t *testing.T, deploy bool) HistorySpec {
	t.Helper()
	lc := parseLC(t, m4LocalConfig+m5Additions)
	h, err := History(lc)
	if err != nil {
		t.Fatal(err)
	}
	if deploy {
		h.DeployJob, h.FirebaseProject, h.RTDBURL = true, "aurora-fp", "https://aurora-fp-default-rtdb.firebaseio.com"
	}
	return h
}

// TestFirebaseVarsPlan plans the Firebase root with the tfvars fugaro init
// --firebase writes, against mock providers.
func TestFirebaseVarsPlan(t *testing.T) {
	// aurora-fp is the two-project layout; proj-1234 is the installation's
	// own project (the same-project layout).
	for _, fp := range []string{"aurora-fp", "proj-1234"} {
		t.Run(fp, func(t *testing.T) { firebaseVarsPlan(t, fp) })
	}
}

func firebaseVarsPlan(t *testing.T, fp string) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Fatalf("this test needs terraform on PATH: %v", err)
	}
	spec := installationSpec(t)
	spec.Launchers = []string{"user:launcher@example.com"}
	spec.Operators = []string{"user:operator@example.com"}
	fs, err := Firebase(spec, FirebaseInputs{FP: fp, Admins: []string{"user:owner@example.com", "group:editors@example.com"},
		BudgetAdmins: []string{"user:extra@example.com"}, HistoryAccount: "fugaro-history@proj-1234.iam.gserviceaccount.com"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := FirebaseVars(fs)
	if err != nil {
		t.Fatal(err)
	}
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(data, &vars); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("mock_provider \"google\" {}\nmock_provider \"google-beta\" {}\n\n")
	for _, o := range []struct{ target, values string }{
		{"module.firebase.google_service_account.signer", "name = \"projects/" + fp + "/serviceAccounts/fugaro-token-signer@" + fp + ".iam.gserviceaccount.com\"\n      email = \"fugaro-token-signer@" + fp + ".iam.gserviceaccount.com\""},
		{"module.firebase.google_firebase_database_instance.this", "database_url = \"https://" + fp + "-default-rtdb.firebaseio.com\""},
		{"module.firebase.google_apikeys_key.web", "key_string = \"AIzaSyMockKey\""},
	} {
		b.WriteString("override_resource {\n  target          = " + o.target + "\n  override_during = plan\n  values = {\n      " + o.values + "\n  }\n}\n\n")
	}
	b.WriteString("variables {\n")
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		b.WriteString("  " + k + " = " + string(vars[k]) + "\n")
	}
	b.WriteString("}\n\nrun \"plan\" {\n  command = plan\n\n  assert {\n    condition     = output.firebase_project == \"" + fp + "\"\n    error_message = \"the Firebase project is not the tfvars'\"\n  }\n}\n")
	dir := t.TempDir()
	writeTree(t, dir)
	root := filepath.Join(dir, "gcp/roots/firebase")
	if err := os.RemoveAll(filepath.Join(root, "tests")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "vars.tftest.hcl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tfdata := filepath.Join(t.TempDir(), "tfdata")
	for _, args := range [][]string{{"init", "-backend=false", "-lockfile=readonly", "-input=false"}, {"test", "-no-color"}} {
		cmd := exec.Command("terraform", append([]string{"-chdir=" + root}, args...)...)
		cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1", "TF_DATA_DIR="+tfdata)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("terraform %s: %v\n%s", args[0], err, out)
		}
	}
}
