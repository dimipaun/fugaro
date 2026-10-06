//go:build terraform

package infra

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
)

// writeTree writes the embedded Terraform tree into dir.
func writeTree(t *testing.T, dir string) {
	t.Helper()
	err := fs.WalkDir(terraform.FS, "gcp", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := fs.ReadFile(terraform.FS, p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// validate runs init and validate in a root that holds imports, and
// returns validate's output and error.
func validate(t *testing.T, root string, im Imports) (string, error) {
	t.Helper()
	if err := WriteImports(root, im); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("terraform", "-chdir="+root, "init", "-backend=false", "-lockfile=readonly")
	data := filepath.Join(t.TempDir(), "tfdata")
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1", "TF_DATA_DIR="+data)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terraform init: %v\n%s", err, out)
	}
	cmd = exec.Command("terraform", "-chdir="+root, "validate", "-no-color")
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1", "TF_DATA_DIR="+data)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestGeneratedRootValidates writes the three roots with every kind of import
// discovery generates, and has terraform validate them: validate fails on
// an import whose target block doesn't exist.
func TestGeneratedRootValidates(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Fatalf("this test needs terraform on PATH: %v", err)
	}
	ctx := context.Background()
	f := newCloud(t)
	spec := sandboxSpec(t)
	f.bootstrap(t, spec, legacyDisplay)
	f.m5(t, spec)
	repoIm, _, err := DiscoverRepo(ctx, f.c, spec)
	if err != nil {
		t.Fatal(err)
	}
	inst := installationSpec(t)
	f.ar.AddRepository("proj-1234", "us-east5", LegacyRegistry, managed)
	f.m5Installation(inst)
	instIm, err := DiscoverInstallation(ctx, f.c, inst)
	if err != nil {
		t.Fatal(err)
	}
	fc := newFBCloud(t)
	fc.addAll("https://fp-1234-default-rtdb.firebaseio.com")
	fbIm, err := DiscoverFirebase(ctx, fc.c, fbSpec(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fbIm.List) != 4 {
		t.Fatalf("Firebase imports %v, want the four singletons", fbIm.List)
	}
	// Every kind of import is exercised.
	seen := map[string]bool{}
	for _, i := range append(append(append([]Import{}, repoIm.List...), instIm.List...), fbIm.List...) {
		seen[i.To] = true
	}
	for kind := range importTable {
		probe := newImport(kind, "proj-1234", "us-east5", "web", "x").To
		prefix, _, _ := strings.Cut(probe, `["`)
		var found bool
		for to := range seen {
			if strings.HasPrefix(to, prefix) {
				found = true
			}
		}
		if !found {
			t.Errorf("no generated import of kind %s", probe)
		}
	}

	dir := t.TempDir()
	writeTree(t, dir)
	for _, r := range []struct {
		root string
		im   Imports
	}{
		{filepath.Join(dir, "gcp/roots/repo"), repoIm},
		{filepath.Join(dir, "gcp/roots/installation"), instIm},
		// A fresh project imports nothing: the file is then empty.
		{filepath.Join(dir, "gcp/roots/installation"), Imports{}},
		{filepath.Join(dir, "gcp/roots/firebase"), fbIm},
	} {
		if out, err := validate(t, r.root, r.im); err != nil {
			t.Errorf("%s: terraform validate: %v\n%s", r.root, err, out)
		}
	}

	// The control: an import of an address with no block fails validate.
	bad := Imports{List: append(append([]Import{}, repoIm.List...),
		Import{To: "module.repo.google_service_account.nothing", ID: "projects/proj-1234/serviceAccounts/x@proj-1234.iam.gserviceaccount.com"})}
	if out, err := validate(t, filepath.Join(dir, "gcp/roots/repo"), bad); err == nil {
		t.Errorf("validate accepted an import with no target block:\n%s", out)
	}
}
