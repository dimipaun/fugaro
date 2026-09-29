package infra

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

func TestInstallationWorkdirPath(t *testing.T) {
	got, err := InstallationWorkdir(env(map[string]string{"XDG_STATE_HOME": "/s", "HOME": "/h"}), "proj-1234")
	if err != nil || got != "/s/fugaro/terraform/proj-1234/installation" {
		t.Fatalf("with XDG_STATE_HOME: %q, %v", got, err)
	}
	// A relative XDG_STATE_HOME is ignored, as the XDG spec says.
	got, err = InstallationWorkdir(env(map[string]string{"XDG_STATE_HOME": "rel", "HOME": "/h"}), "proj-1234")
	if err != nil || got != "/h/.local/state/fugaro/terraform/proj-1234/installation" {
		t.Fatalf("default: %q, %v", got, err)
	}
	if _, err := InstallationWorkdir(env(map[string]string{"HOME": "/h"}), "../x"); err == nil {
		t.Fatal("a project that is not an ID made a path")
	}
	if _, err := InstallationWorkdir(env(nil), "proj-1234"); err == nil {
		t.Fatal("no HOME and no XDG_STATE_HOME made a path")
	}
	cache, err := PluginCache(env(map[string]string{"HOME": "/h"}))
	if err != nil || cache != "/h/.cache/fugaro/terraform-plugins" {
		t.Fatalf("plugin cache: %q, %v", cache, err)
	}
}

// init --repo reads the installation's outputs in a workdir of its own,
// inside the installation's: preparing either leaves the other's tree.
func TestInstallationOutputsWorkdir(t *testing.T) {
	getenv := env(map[string]string{"XDG_STATE_HOME": t.TempDir()})
	inst, err := InstallationWorkdir(getenv, "proj-1234")
	if err != nil {
		t.Fatal(err)
	}
	outs, err := InstallationOutputsWorkdir(getenv, "proj-1234")
	if err != nil || outs != filepath.Join(inst, "outputs") {
		t.Fatalf("outputs workdir = %q, %v", outs, err)
	}
	w, err := PrepareWorkdir(inst, "installation")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteVars([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	o, err := PrepareWorkdir(outs, "installation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.Root, VarsFile)); err != nil {
		t.Errorf("preparing the outputs workdir removed the installation's tfvars: %v", err)
	}
	if err := o.WriteVars([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareWorkdir(inst, "installation"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(o.Root, VarsFile)); err != nil {
		t.Errorf("preparing the installation workdir removed the outputs workdir: %v", err)
	}
	if _, err := InstallationOutputsWorkdir(getenv, "../x"); err == nil {
		t.Fatal("a project that is not an ID made a path")
	}
}

// The workdir holds the embedded tree, fresh each time, and is private.
func TestPrepareWorkdir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "w")
	w, err := PrepareWorkdir(dir, "installation")
	if err != nil {
		t.Fatal(err)
	}
	if w.Root != filepath.Join(dir, "gcp", "roots", "installation") {
		t.Fatalf("root = %s", w.Root)
	}
	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("workdir mode: %v, %v", st, err)
	}
	for _, p := range []string{"main.tf", "variables.tf", "outputs.tf", ".terraform.lock.hcl", "../../modules/installation/variables.tf"} {
		if _, err := os.Stat(filepath.Join(w.Root, p)); err != nil {
			t.Errorf("the workdir lacks %s: %v", p, err)
		}
	}
	stale := filepath.Join(w.Root, "stale.tf")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, ".terraform", "keep")
	if err := os.MkdirAll(filepath.Dir(keep), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareWorkdir(dir, "installation"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a stale file of an earlier run survived: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("terraform's data directory was removed: %v", err)
	}
	if _, err := PrepareWorkdir(dir, "../x"); err == nil {
		t.Error("an unknown root was accepted")
	}

	cfg, err := w.WriteBackend(testStateBucket, StatePrefixInstallation)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["bucket"] != testStateBucket || cfg["prefix"] != "fugaro/installation" || len(cfg) != 2 {
		t.Fatalf("backend config = %v", cfg)
	}
	hcl, err := os.ReadFile(filepath.Join(w.Root, BackendFile))
	if err != nil || !strings.Contains(string(hcl), `bucket = "`+testStateBucket+`"`) || !strings.Contains(string(hcl), `prefix = "fugaro/installation"`) {
		t.Fatalf("backend.hcl = %q, %v", hcl, err)
	}
	if err := w.WriteVars([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(w.Root, VarsFile)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("tfvars: %v, %v", st, err)
	}
}
