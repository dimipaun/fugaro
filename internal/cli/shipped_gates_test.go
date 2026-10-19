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
// design is, and that the ordinary paths are unaffected. None of them names a
// release: 0.9.0 is the release these refuse in, and the wording they shipped
// with said the feature "returns in 0.9.0", which told a 0.9.0 user to wait for
// the release they were running. What a refusal may say is what is still to be
// done, not when.
func TestShippedGateMessages(t *testing.T) {
	for _, want := range []string{"managed projects are disabled in this release", "scoped role and its binding are not created yet", "docs/design/managed-projects.md", "without --managed is unaffected"} {
		if !strings.Contains(managedDisabled, want) {
			t.Errorf("managedDisabled does not say %q", want)
		}
	}
	for _, want := range []string{"--provision-batch is disabled in this release", "batch.jobs.cancel", "the reaper is still to be redesigned"} {
		if !strings.Contains(batchProvisionDisabled, want) {
			t.Errorf("batchProvisionDisabled does not say %q", want)
		}
	}
	for _, s := range []string{managedDisabled, batchProvisionDisabled, doctorManagedDeployerProblem(t)} {
		if strings.Contains(s, "0.9.0") {
			t.Errorf("a shipped-gate message names a release (%q): the release that opens a gate is the one that ships the fix, and a message naming it tells the user running that release to wait for it — say what is still to be done instead", s)
		}
	}
	for _, f := range []string{"../../docs/design/managed-projects.md", "../../docs/design/compute-backend.md"} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("a refusal names %s: %v", f, err)
		}
	}
}

// doctorManagedDeployerProblem is the doctor line's text with the gate closed,
// the third user-facing message that states why a managed entry point refuses.
func doctorManagedDeployerProblem(t *testing.T) string {
	t.Helper()
	closeShippedGates(t)
	c := doctorManagedDeployer()
	if c == nil {
		t.Fatal("doctor has no managed-deployer line while managedDeployerShipped is false")
	}
	return c.Problem
}

// No user-facing text promises a release for a feature whose gate is shut, for
// the reason TestShippedGateMessages gives: the release that opens a gate is the
// one that ships the fix, so naming it in the text a user of that release reads
// points at a version that has already passed. The refusals are held to it
// there; these are the operator-facing documents and the installation module's
// own variable description, which promised the two gated features back three
// more ways — the guide said managed projects "are planned to return in 0.9.0",
// the design's status banner set the fence's redesign "for 0.9.0", and the
// `managed_deployer_scoped` description said managed projects stay disabled
// "(0.9.0)". What a tree that is about to release 0.9.0 may say is where the
// gate stands, which each of the three now does; the owner's ruling of
// 2026-10-18 is still *quoted* as it was given ("redesign for 0.9.0"), and that
// is history, not a promise made by this release.
func TestGatedFeaturesPromiseNoRelease(t *testing.T) {
	const variables = "../../deploy/terraform/gcp/modules/installation/variables.tf"
	for _, f := range []string{"../../docs/gcp-setup.md", "../../docs/design/managed-projects.md", "../../docs/design/compute-backend.md", "../../docs/release.md", variables} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, stale := range []string{
			"Managed projects are planned to return in 0.9.0",
			"redesigned and measured live (Check 68) for 0.9.0",
			"Managed projects stay disabled until the fence is redesigned and measured live (0.9.0)",
			"the Batch backend returns in 0.9.0",
		} {
			if strings.Contains(string(data), stale) {
				t.Errorf("%s still says %q: a user reading it is running the release it names, so it promises what this release does not ship — state where the gate stands instead", f, stale)
			}
		}
	}
	for f, want := range map[string]string{
		"../../docs/gcp-setup.md":               "still disabled in 0.9.0",
		"../../docs/design/managed-projects.md": "0.9.0 ships with it still not done",
		variables:                               "0.9.0 ships with them still disabled",
		"../../docs/design/compute-backend.md":  "batchProvisionShipped",
	} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not state the gate's current state (%q)", f, want)
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

// doctor says plainly, as info, that the scoped deployer role is absent on
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
	if !strings.Contains(c.Problem, "disabled in this release") || !strings.Contains(c.Problem, "refused in this release") {
		t.Errorf("the line does not say the scoped role is disabled in this release and that the managed commands are refused in it: %q", c.Problem)
	}
	// TestShippedGateMessages bans a version in this text; the ban lives there
	// so the two refusals and this line are held to it together.
	if checksFail([]doctorCheck{*c}, true) {
		t.Error("the managed-deployer line fails doctor --strict")
	}
}
