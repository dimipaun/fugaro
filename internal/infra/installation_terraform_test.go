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
	for _, adopt := range []bool{false, true} {
		name := "fresh"
		if adopt {
			name = "adopt_legacy_registry"
		}
		t.Run(name, func(t *testing.T) {
			spec := installationSpec(t)
			spec.AdoptLegacyRegistry = adopt
			spec.Launchers = []string{"user:launcher@example.com"}
			spec.Operators = []string{"user:operator@example.com"}
			email := "ops@example.com"
			spec.AlertEmail = &email
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
			if err := os.WriteFile(filepath.Join(root, "tests", "vars.tftest.hcl"), []byte(varsTest(t, data, adopt)), 0o644); err != nil {
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
func varsTest(t *testing.T, data []byte, adopt bool) string {
	t.Helper()
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(data, &vars); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("mock_provider \"google\" {}\n\nvariables {\n")
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
	b.WriteString("    error_message = \"adopt_legacy_registry did not reach the module\"\n  }\n}\n")
	return b.String()
}
