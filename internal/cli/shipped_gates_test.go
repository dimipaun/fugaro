package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// closeShippedGates puts managedDeployerOpen back to its constant for one
// test: TestMain opens it so the managed paths' own tests can run, and these
// tests are about the release as it ships. Never call it from a parallel test:
// it is a package variable.
//
// --provision-batch has no gate any more (0.9.5 closed it: the reaper's role
// is five permissions and batchProvisionShipped is true), so there is nothing
// of the Batch side left to put back.
func closeShippedGates(t *testing.T) {
	t.Helper()
	m := managedDeployerOpen
	managedDeployerOpen = managedDeployerShipped
	t.Cleanup(func() { managedDeployerOpen = m })
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

// managedGateCases is every managed entry point and the arguments that reach
// it: TestManagedEntryPointsRefuseWhileNotShipped runs each one with the gate
// closed, and TestManagedEntryPointsAreAllGated derives the list a release must
// cover from the command tree, so a managed command added later without an
// entry here fails rather than shipping ungated.
var managedGateCases = map[string][]string{
	"setup --create-project --managed": {"setup", "--create-project", "--gcp-project", "fugaro-newproj-1234", "--managed", "--budget", "50", "--budget-currency", "USD", "--billing-account", "000000-000000-000000"},
	"setup --managed":                  {"setup", "--managed"},
	"managed apply":                    {"managed", "apply", "infra"},
	"managed flag":                     {"managed", "flag", "fugaro-newproj-1234", "--budget", "50", "--budget-currency", "USD"},
	"restore":                          {"restore", "fugaro-newproj-1234", "infra"},
}

// While managedDeployerShipped is false (0.8.6 onward: 0.9.0 still ships with
// the scoped role gated off, and the redesign is planned for 0.10.0), every
// command that would create a managed project, mark one managed or act as its
// deployer refuses with the one message, as a user error, before anything
// else: the message is the first refusal of each, so no other check (and no
// cloud call, which every one of them needs a project config, credentials or a
// terminal for) ran before it.
func TestManagedEntryPointsRefuseWhileNotShipped(t *testing.T) {
	closeShippedGates(t)
	for name, args := range managedGateCases {
		t.Run(name, func(t *testing.T) {
			out, err := runGated(t, args...)
			if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), managedDisabled) {
				t.Fatalf("fugaro %s: exit %d (%v), want the user error %q\n%s", strings.Join(args, " "), ExitCode(err), err, managedDisabled, out)
			}
		})
	}
}

// The managed entry points, read from the command tree rather than listed by
// hand: every runnable command under `fugaro managed`, every command that
// defines a --managed flag (with --create-project as well, where the command
// has it), and restore, which creates a managed project. Each must have a case
// in managedGateCases, and a case that names no such entry point is stale.
func TestManagedEntryPointsAreAllGated(t *testing.T) {
	want := map[string]bool{"restore": true}
	var walk func(c *cobra.Command, path []string)
	walk = func(c *cobra.Command, path []string) {
		for _, sub := range c.Commands() {
			p := append(append([]string{}, path...), sub.Name())
			if len(p) >= 2 && p[0] == "managed" && sub.Runnable() {
				want[strings.Join(p, " ")] = true
			}
			// A flag the command HIDES is not an entry point: it is not
			// offered, and the door that hides it refuses it by name before
			// any stage runs, so nothing can reach the gate through it. The
			// update door declares the creating flags for exactly that
			// refusal (W1.2), which is why the walk has to read Hidden.
			if f := sub.Flags().Lookup("managed"); f != nil && !f.Hidden {
				name := strings.Join(p, " ")
				want[name+" --managed"] = true
				if c := sub.Flags().Lookup("create-project"); c != nil && !c.Hidden {
					want[name+" --create-project --managed"] = true
				}
			}
			walk(sub, p)
		}
	}
	root := NewRootCmd()
	walk(root, nil)
	if !want["managed apply"] || !want["setup --managed"] {
		t.Fatalf("the walk did not find the known entry points: %v", want)
	}
	if c, _, err := root.Find([]string{"restore"}); err != nil || c.Name() != "restore" {
		t.Fatalf("no restore command: %v", err)
	}
	for name := range want {
		if _, ok := managedGateCases[name]; !ok {
			t.Errorf("managed entry point %q has no case in managedGateCases: add one, and make it call refuseManagedNotShipped first", name)
		}
	}
	for name := range managedGateCases {
		if !want[name] {
			t.Errorf("managedGateCases names %q, which the command tree does not have as a managed entry point", name)
		}
	}
}

// The refusals name what the operator needs: which feature, why, where the
// design is, and that the ordinary paths are unaffected.
func TestShippedGateMessages(t *testing.T) {
	for _, want := range []string{"managed projects are disabled in this release", "scoped roles and their bindings are not created yet", "docs/design/managed-projects.md", "without --managed is unaffected"} {
		if !strings.Contains(managedDisabled, want) {
			t.Errorf("managedDisabled does not say %q", want)
		}
	}
	for _, f := range []string{"../../docs/design/managed-projects.md", "../../docs/design/compute-backend.md"} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("a refusal names %s: %v", f, err)
		}
	}
}

// The gate's variable is the constant in production: no non-test file of this
// package assigns it after its declaration, so only a test can open it.
func TestShippedGateVariablesAreOnlyTheConstants(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	assign := regexp.MustCompile(`(?m)^\s*managedDeployerOpen\b[^=\n]*=[^=]|managedDeployerOpen\s*,[^\n]*=[^=]`)
	decl := map[string]bool{
		"var managedDeployerOpen = managedDeployerShipped": false,
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
			t.Errorf("%s assigns the shipped gate: %q", f, m)
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
	if !strings.Contains(c.Problem, "disabled in this release") || !strings.Contains(c.Problem, "0.10.0") {
		t.Errorf("the line does not say the scoped role is disabled in this release and returns in 0.10.0: %q", c.Problem)
	}
	if checksFail([]doctorCheck{*c}, true) {
		t.Error("the managed-deployer line fails doctor --strict")
	}
}
