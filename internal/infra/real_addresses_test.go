//go:build terraform

package infra

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
)

// This is the one step of docs/plans/2026-10-14-config-transitions.md that
// reaches outside the repository, and it is gated so that CI never does:
// .github/workflows/ci.yml runs `go test -tags terraform ./internal/infra/...`,
// which compiles and vets this file but must not download a provider. The
// generator therefore returns before it does anything unless -update is
// set, and every network step (terraform init, the provider registry
// download) sits behind that skip.
//
// When it does run, on the owner's machine, its only network use is the
// provider registry download of the google provider binary named by the
// root's lock file. It carries no project credentials, calls no Google API
// and applies nothing: it plans a temp copy of the committed root with the
// provider's credential checks skipped and a local backend, and reads the
// addresses out of the plan's resource_changes. The credential-skipping
// provider override was enough for both fixtures (no data source is read
// by this root, so the plan needs no API call at all); if a later module
// change makes one necessary, plan with read-only credentials for a
// sandbox project instead and record that here.
//
// The two goldens are internal/infra/testdata/repo_root_addresses.golden
// and repo_root_vertex_addresses.golden, and the untagged
// TestRepoAddressesMatchTheRealPlanGolden / ...MatchTheVertexGolden are
// what CI runs against them.

// providerOverride skips everything the google provider would otherwise
// ask Google about before it can plan: the credential's validity, the
// caller's account ID and the provider registry. The root's own provider
// block sets project, region, billing_project and user_project_override;
// an override file merges into it, so those survive.
//
// access_token is what makes the generator runnable with no credentials at
// all: the provider insists on finding one before it will even load, and
// any non-empty string satisfies it. Nothing sends it anywhere: the plan
// reads no data source, so the provider never calls the API, and the token
// is never a real one.
const providerOverride = `
provider "google" {
  access_token                = "no-credentials-the-plan-makes-no-api-call"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_provider_registry      = true
}
`

// backendOverride replaces the root's `backend "gcs" {}` with a local one:
// the fixture plans a copy in a temp directory, with no state bucket and no
// state to read.
const backendOverride = `
terraform {
  backend "local" {}
}
`

// realRepoRootAddresses plans the committed repository root from one
// fixture spec's tfvars and returns the sorted addresses its plan's
// resource_changes name. resource_changes excludes data sources and any
// zero-count instance, which is exactly what Addresses mirrors.
func realRepoRootAddresses(t *testing.T, name string) []string {
	t.Helper()
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Fatalf("this test needs terraform on PATH: %v", err)
	}
	spec := repoRootFixtureSpec(t, name)
	vars, err := RepoRootVars(spec, RepoRootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// The embedded tree, so the plan is of exactly what the repository
	// ships. The imports file is deliberately absent: Terraform reads
	// `import` blocks at plan time and an importer may need credentials for
	// the resource it adopts, which this generator carries none of.
	if err := fs.WalkDir(terraform.FS, "gcp", func(p string, d fs.DirEntry, err error) error {
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
	}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "gcp", "roots", "repo")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("terraform.tfvars.json", string(vars))
	write("provider_override.tf", providerOverride)
	write("backend_override.tf", backendOverride)

	env := append(os.Environ(),
		"TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1",
		// The provider binary stays out of the tree.
		"TF_DATA_DIR="+filepath.Join(dir, "tfdata"),
		// A fresh HOME, so nothing of the developer's terraform setup
		// (a provider mirror, a CLI config) is read.
		"HOME="+filepath.Join(dir, "home"),
	)
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("terraform %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	run("init", "-input=false", "-no-color", "-lockfile=readonly")
	run("plan", "-input=false", "-no-color", "-out=plan.bin", "-var-file=terraform.tfvars.json")
	show := run("show", "-json", "plan.bin")
	var plan struct {
		ResourceChanges []struct {
			Address string `json:"address"`
			Change  struct {
				Actions []string `json:"actions"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(show, &plan); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, rc := range plan.ResourceChanges {
		// A data source reads, and is not an address spec produces.
		if slices.Contains(rc.Change.Actions, "read") {
			continue
		}
		out = append(out, rc.Address)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func TestRealRepoRootAddresses(t *testing.T) {
	// -update is spec_test.go's own flag, declared in the untagged file so
	// that a second declaration here would panic: the tagged run compiles
	// both files into one test binary.
	if !*update {
		t.Skip("the golden generator reaches the provider registry; run it with -update on a machine with terraform")
	}
	for name := range repoRootFixtures {
		addrs := realRepoRootAddresses(t, name)
		if len(addrs) == 0 {
			t.Fatalf("%s: the plan named no address", name)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# The addresses a real terraform plan of deploy/terraform/gcp/roots/repo produces\n")
		fmt.Fprintf(&b, "# for the fixture spec in testdata/%s.tfvars.json, sorted. Generated by\n", name)
		fmt.Fprintf(&b, "# TestRealRepoRootAddresses (-tags terraform -update); CI reads it in\n")
		fmt.Fprintf(&b, "# TestRepoAddressesMatchTheRealPlanGolden, untagged. Do not edit by hand.\n")
		for _, a := range addrs {
			b.WriteString(a)
			b.WriteString("\n")
		}
		path := filepath.Join(repoRootGoldenDir, name+"_addresses.golden")
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d addresses)", path, len(addrs))
	}
}
