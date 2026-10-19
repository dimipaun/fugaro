package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// closeShippedGates puts managedDeployerOpen and batchProvisionOpen back to
// their constants for one test: TestMain opens both so the managed and Batch
// paths' own tests can run, and these tests are about the release as it ships.
// Never call it from a parallel test: the two are package variables.
func closeShippedGates(t *testing.T) {
	t.Helper()
	m, b := managedDeployerOpen, batchProvisionOpen
	managedDeployerOpen, batchProvisionOpen = managedDeployerShipped, batchProvisionShipped
	t.Cleanup(func() { managedDeployerOpen, batchProvisionOpen = m, b })
}

// runGated runs the root command with args in a scratch project directory, as
// a user would, and returns its error and output.
func runGated(t *testing.T, args ...string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	isolateProjects(t, dir)
	t.Chdir(dir)
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// While managedDeployerShipped is false (0.8.6), every command that would
// create a managed project or act as its deployer refuses with the one
// message, as a user error, before anything else: the message is the first
// refusal of each, so no other check (and no cloud call, which every one of
// them needs a project config, credentials or a terminal for) ran before it.
func TestManagedEntryPointsRefuseWhileNotShipped(t *testing.T) {
	closeShippedGates(t)
	for name, args := range map[string][]string{
		"init --create-project --managed": {"init", "--create-project", "--gcp-project", "fugaro-newproj-1234", "--managed", "--budget", "50", "--budget-currency", "USD", "--billing-account", "000000-000000-000000"},
		"init --managed":                  {"init", "--managed"},
		"managed apply":                   {"managed", "apply", "infra"},
		"restore":                         {"restore", "fugaro-newproj-1234", "infra"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runGated(t, args...)
			if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), managedDisabled) {
				t.Fatalf("fugaro %s: exit %d (%v), want the user error %q\n%s", strings.Join(args, " "), ExitCode(err), err, managedDisabled, out)
			}
		})
	}
}

// --provision-batch refuses the same way while batchProvisionShipped is false:
// the compute module's reaper role names batch.jobs.cancel, which Google does
// not have, so its apply cannot complete.
func TestProvisionBatchRefusedWhileNotShipped(t *testing.T) {
	closeShippedGates(t)
	for name, args := range map[string][]string{
		"init --provision-batch":        {"init", "--provision-batch"},
		"init --repo --provision-batch": {"init", "--repo", "--provision-batch"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runGated(t, args...)
			if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), batchProvisionDisabled) {
				t.Fatalf("fugaro %s: exit %d (%v), want the user error %q\n%s", strings.Join(args, " "), ExitCode(err), err, batchProvisionDisabled, out)
			}
		})
	}
}

// The refusals name what the operator needs: which feature, why, where the
// design is, and that the ordinary paths are unaffected.
func TestShippedGateMessages(t *testing.T) {
	for _, want := range []string{"managed projects are disabled in this release", "scoped role and its binding are not created yet", "docs/design/managed-projects.md", "without --managed is unaffected"} {
		if !strings.Contains(managedDisabled, want) {
			t.Errorf("managedDisabled does not say %q", want)
		}
	}
	for _, want := range []string{"--provision-batch is disabled in this release", "batch.jobs.cancel", "0.9.0"} {
		if !strings.Contains(batchProvisionDisabled, want) {
			t.Errorf("batchProvisionDisabled does not say %q", want)
		}
	}
	for _, f := range []string{"../../docs/design/managed-projects.md", "../../docs/design/compute-backend.md"} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("a refusal names %s: %v", f, err)
		}
	}
}

// The gates' variables are the constants in production: no non-test file of
// this package assigns either after its declaration, so only a test can open
// one.
func TestShippedGateVariablesAreOnlyTheConstants(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	assign := regexp.MustCompile(`(?m)^\s*(managedDeployerOpen|batchProvisionOpen)\b[^=\n]*=[^=]|(managedDeployerOpen|batchProvisionOpen)\s*,[^\n]*=[^=]`)
	decl := map[string]bool{
		"var managedDeployerOpen = managedDeployerShipped": false,
		"var batchProvisionOpen = batchProvisionShipped":   false,
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for d := range decl {
			if strings.Contains(string(b), d) {
				decl[d] = true
			}
		}
		if m := assign.FindString(string(b)); m != "" {
			t.Errorf("%s assigns a shipped gate: %q", f, m)
		}
	}
	for d, ok := range decl {
		if !ok {
			t.Errorf("no file declares %q", d)
		}
	}
}

// doctor says plainly, as info, that the scoped deployer roles are absent on
// purpose in this release, and never warns or fails on it, not even with
// --strict.
func TestDoctorSaysManagedDeployerIsDisabled(t *testing.T) {
	closeShippedGates(t)
	c := doctorManagedDeployer()
	if c == nil {
		t.Fatal("doctor has no managed-deployer line while managedDeployerShipped is false")
	}
	if c.Severity != "info" {
		t.Errorf("severity %q, want info", c.Severity)
	}
	if !strings.Contains(c.Problem, "disabled in this release") || !strings.Contains(c.Problem, "0.9.0") {
		t.Errorf("the line does not say the scoped role is disabled in this release and returns in 0.9.0: %q", c.Problem)
	}
	if checksFail([]doctorCheck{*c}, true) {
		t.Error("the managed-deployer line fails doctor --strict")
	}
}
