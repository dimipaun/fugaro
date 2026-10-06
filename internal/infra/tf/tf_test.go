package tf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPlanExitCodeTwoIsChanges(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		exit    int
		changed bool
		fails   bool
	}{
		{exit: 0, changed: false},
		{exit: 2, changed: true},
		{exit: 1, fails: true},
		{exit: 3, fails: true},
	} {
		r := newRig(t)
		r.script["plan"] = map[string]any{"exit": tc.exit, "stderr": "boom"}
		tf := r.tf(t, nil)
		out := filepath.Join(r.dir, "tfplan")
		changed, err := tf.Plan(ctx, out)
		if tc.fails {
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code != tc.exit || !strings.Contains(err.Error(), "boom") {
				t.Errorf("exit %d: err = %v, want an ExitError with the code and stderr", tc.exit, err)
			}
			continue
		}
		if err != nil || changed != tc.changed {
			t.Errorf("exit %d: changed=%v err=%v, want changed=%v", tc.exit, changed, err, tc.changed)
		}
		want := []string{"plan", "-input=false", "-no-color", "-lock-timeout=60s", "-detailed-exitcode", "-out=" + out, "-var-file=terraform.tfvars.json"}
		if got := r.call(t, "plan").Args; !slices.Equal(got, want) {
			t.Errorf("plan argv = %q, want %q", got, want)
		}
	}
}

func TestApplyUsesSavedPlanOnly(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	tf := r.tf(t, nil)
	out := filepath.Join(r.dir, "tfplan")
	if _, err := tf.Plan(ctx, out); err != nil {
		t.Fatal(err)
	}
	// The fake writes its marker to the -out file, so this is Plan's output.
	if data, err := os.ReadFile(out); err != nil || string(data) != "fake-terraform-plan\n" {
		t.Fatalf("the plan file holds %q (%v), want the fake's marker", data, err)
	}
	if err := tf.Apply(ctx, out); err != nil {
		t.Fatal(err)
	}
	args := r.call(t, "apply").Args
	if args[len(args)-1] != out {
		t.Errorf("apply argv %q doesn't end in the plan %s", args, out)
	}
	for _, a := range args {
		for _, bad := range []string{"-auto-approve", "-target", "-var", "-replace", "-destroy", "-lock=false"} {
			if a == bad || strings.HasPrefix(a, bad+"=") {
				t.Errorf("apply argv %q holds %s", args, bad)
			}
		}
	}
	// The apply takes the state lock again, so it waits for a lock held
	// briefly by something else, as the plan does.
	want := []string{"apply", "-input=false", "-no-color", "-lock-timeout=60s", out}
	if !slices.Equal(args, want) {
		t.Errorf("apply argv = %q, want %q", args, want)
	}

	// Without a plan file, nothing runs at all.
	n := len(r.calls(t))
	for _, bad := range []string{"", filepath.Join(r.dir, "missing"), r.dir, "-auto-approve"} {
		if err := tf.Apply(ctx, bad); err == nil {
			t.Errorf("Apply(%q) succeeded, want a refusal", bad)
		}
	}
	if got := len(r.calls(t)); got != n {
		t.Errorf("a refused apply still ran terraform (%d calls, want %d)", got, n)
	}
}

// TestRunWritesProgressWithoutRacing covers a command whose Out is a
// bytes.Buffer (as in the real-terraform test, and as the cli wires it up in
// tests): os/exec copies stdout and stderr on two separate goroutines unless
// cmd.Stdout and cmd.Stderr are the same value, so run must make any writes
// they both make into t.Out safe to interleave.
func TestRunWritesProgressWithoutRacing(t *testing.T) {
	r := newRig(t)
	r.script["plan"] = map[string]any{"exit": 0, "stdout": strings.Repeat("o", 1<<12), "stderr": strings.Repeat("e", 1<<12)}
	tf := r.tf(t, nil)
	var out bytes.Buffer
	tf.Out = &out
	if _, err := tf.Plan(context.Background(), filepath.Join(r.dir, "tfplan")); err != nil {
		t.Fatal(err)
	}
}

func TestInitArgs(t *testing.T) {
	r := newRig(t)
	tf := r.tf(t, nil)
	if err := tf.Init(context.Background(), map[string]string{"prefix": "fugaro/installation", "bucket": "fugaro-tfstate-p"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"init", "-input=false", "-no-color", "-lockfile=readonly", "-backend-config=bucket=fugaro-tfstate-p", "-backend-config=prefix=fugaro/installation"}
	if got := r.call(t, "init").Args; !slices.Equal(got, want) {
		t.Errorf("init argv = %q, want %q", got, want)
	}
	dir, _ := filepath.EvalSymlinks(r.dir) // macOS's temp dir is behind a symlink
	if got := r.call(t, "init").Dir; got != dir {
		t.Errorf("init ran in %s, want %s", got, dir)
	}
}

func TestShowParsesPlan(t *testing.T) {
	data, err := os.ReadFile("testdata/plan_import.json")
	if err != nil {
		t.Fatal(err)
	}
	r := newRig(t)
	r.script["show"] = map[string]any{"stdout": string(data)}
	tf := r.tf(t, nil)
	p, err := tf.Show(context.Background(), "tfplan")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"show", "-json", "tfplan"}
	if got := r.call(t, "show").Args; !slices.Equal(got, want) {
		t.Errorf("show argv = %q, want %q", got, want)
	}
	var found bool
	for _, rc := range p.ResourceChanges {
		if rc.Change.Importing != nil && rc.Change.Importing.ID != "" {
			found = true
		}
	}
	if !found {
		t.Errorf("no import in the parsed plan %+v", p)
	}
}

func TestOutputAndStateRm(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.script["output"] = map[string]any{"stdout": `{"runs_bucket":{"sensitive":false,"type":"string","value":"fugaro-runs-x"}}`}
	tf := r.tf(t, nil)
	out, err := tf.Output(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var v string
	if err := json.Unmarshal(out["runs_bucket"], &v); err != nil || v != "fugaro-runs-x" {
		t.Errorf("runs_bucket = %s (%v), want its value", out["runs_bucket"], err)
	}
	if got := r.call(t, "output").Args; !slices.Equal(got, []string{"output", "-no-color", "-json"}) {
		t.Errorf("output argv = %q", got)
	}

	if err := tf.StateRm(ctx, "module.a.x", `module.b.y["k"]`); err != nil {
		t.Fatal(err)
	}
	want := []string{"state", "rm", "-lock-timeout=60s", "module.a.x", `module.b.y["k"]`}
	if got := r.call(t, "state").Args; !slices.Equal(got, want) {
		t.Errorf("state rm argv = %q, want %q", got, want)
	}
	n := len(r.calls(t))
	if err := tf.StateRm(ctx, "-lock=false"); err == nil {
		t.Error("StateRm accepted a flag as an address")
	}
	if err := tf.StateRm(ctx); err != nil {
		t.Errorf("StateRm with no addresses: %v", err)
	}
	if got := len(r.calls(t)); got != n {
		t.Errorf("StateRm ran terraform for nothing (%d calls, want %d)", got, n)
	}
}

func TestVersionTooOld(t *testing.T) {
	for _, tc := range []struct {
		version string
		ok      bool
	}{
		{"1.6.6", false},
		{"1.7.0-beta1", false},
		{"0.15.5", false},
		{"2.0.0", false},
		{"2.0.0-alpha1", false},
		{"garbage", false},
		{"1.7.0", true},
		{"1.16.4", true},
	} {
		r := newRig(t)
		r.script["version"] = map[string]any{"stdout": `{"terraform_version":"` + tc.version + `"}`}
		_, err := New(fakeBin, r.dir, r.env(t, nil))
		if (err == nil) != tc.ok {
			t.Errorf("version %s: err = %v, want ok=%v", tc.version, err, tc.ok)
		}
	}
}

// ShowState reads the state (show -json with no plan file) and tells an
// emptied state (resources removed, outputs possibly left) from one that
// still manages something.
func TestShowState(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		stdout  string
		managed bool
		fails   bool
	}{
		"no state":         {stdout: `{"format_version":"1.0"}`},
		"outputs only":     {stdout: `{"format_version":"1.0","values":{"outputs":{"registry":{"value":"x"}},"root_module":{}}}`},
		"root resources":   {stdout: `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"a.b"}]}}}`, managed: true},
		"module resources": {stdout: `{"format_version":"1.0","values":{"root_module":{"child_modules":[{"address":"module.repo","resources":[{"address":"module.repo.a.b"}]}]}}}`, managed: true},
		"other major":      {stdout: `{"format_version":"2.0"}`, fails: true},
	} {
		r := newRig(t)
		r.script["show"] = map[string]any{"stdout": tc.stdout}
		tf := r.tf(t, nil)
		st, err := tf.ShowState(ctx)
		if tc.fails {
			if err == nil {
				t.Errorf("%s: no error", name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := st.Managed(); got != tc.managed {
			t.Errorf("%s: managed = %v, want %v", name, got, tc.managed)
		}
		if got := r.call(t, "show").Args; !slices.Equal(got, []string{"show", "-json"}) {
			t.Errorf("%s: show argv = %q", name, got)
		}
	}
}

// Addresses lists every resource instance of a state, in the root module
// and in nested child modules, as terraform show -json writes them.
func TestStateAddresses(t *testing.T) {
	const doc = `{
  "format_version": "1.0",
  "terraform_version": "1.9.0",
  "values": {
    "outputs": {"firebase_project": {"sensitive": false, "value": "fp-1", "type": "string"}},
    "root_module": {
      "resources": [
        {"address": "google_project_service.root", "mode": "managed", "type": "google_project_service", "name": "root", "provider_name": "registry.terraform.io/hashicorp/google", "schema_version": 0, "values": {"id": "x"}}
      ],
      "child_modules": [
        {
          "address": "module.firebase",
          "resources": [
            {"address": "module.firebase.google_apikeys_key.web", "mode": "managed", "type": "google_apikeys_key", "name": "web", "values": {}},
            {"address": "module.firebase.google_project_service.this[\"iam.googleapis.com\"]", "mode": "managed", "type": "google_project_service", "name": "this", "index": "iam.googleapis.com", "values": {}}
          ],
          "child_modules": [
            {"address": "module.firebase.module.inner", "resources": [{"address": "module.firebase.module.inner.google_service_account.a", "mode": "managed"}]}
          ]
        }
      ]
    }
  }
}`
	for name, tc := range map[string]struct {
		stdout string
		want   []string
	}{
		"nested modules": {doc, []string{
			"google_project_service.root",
			"module.firebase.google_apikeys_key.web",
			`module.firebase.google_project_service.this["iam.googleapis.com"]`,
			"module.firebase.module.inner.google_service_account.a",
		}},
		"no state":     {`{"format_version":"1.0"}`, nil},
		"outputs only": {`{"format_version":"1.0","values":{"outputs":{"a":{"value":"x"}},"root_module":{}}}`, nil},
	} {
		r := newRig(t)
		r.script["show"] = map[string]any{"stdout": tc.stdout}
		st, err := r.tf(t, nil).ShowState(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := st.Addresses()
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: addresses\n got %q\nwant %q", name, got, tc.want)
		}
	}
	var nilState *State
	if got := nilState.Addresses(); len(got) != 0 {
		t.Errorf("nil state: %q", got)
	}
}
