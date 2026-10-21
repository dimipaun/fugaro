package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// retired_test.go pins the `init` tombstone (task W1.2 of
// docs/setup-wizard/implementation-plan.md; D2 of the reconciliation §5,
// answered by the owner on 2026-10-20 as recommended: no alias, a tombstone).
//
// The command is gone and the two that replace it are `fugaro setup` (the
// interactive first-time wizard) and `fugaro update` (the re-converge after a
// CLI upgrade). A user who types the old name is exactly the user who cannot
// tell the two apart, so the tombstone's whole job is to say both names and
// exit non-zero — it applies nothing, and it must not become an alias that
// picks one meaning for them.

// TestInitIsRetiredAndSaysBothNames: every shape of the old command line ends
// in the one sentence, whatever flags or paths came with it. That is why the
// tombstone parses no flags (DisableFlagParsing): a run that carried `--yes`
// or a checkout path must read the reason, not cobra's "unknown flag".
func TestInitIsRetiredAndSaysBothNames(t *testing.T) {
	firstRunEnv(t)
	for _, args := range [][]string{
		{"init"},
		{"init", "--yes"},
		{"init", "--repo", "."},
		{"init", "--create-project", "--gcp-project", "acme-prod"},
		{"init", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, errOut, err := executeStdin(t, "", args...)
			if err == nil {
				t.Fatalf("%v exited 0", args)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("%v exits %d, want %d (a user error)", args, got, ExitUserError)
			}
			got := out + errOut + ErrorText(err)
			if !strings.Contains(got, retiredInitMessage) {
				t.Fatalf("%v does not say %q:\n%s", args, retiredInitMessage, got)
			}
			for _, name := range []string{"fugaro setup", "fugaro update"} {
				if !strings.Contains(got, name) {
					t.Errorf("%v does not name %q", args, name)
				}
			}
		})
	}
}

// The tombstone is hidden: it is not a command this CLI offers any more, and
// `fugaro --help` must not list it beside the two that replaced it.
func TestInitIsNotOfferedByHelp(t *testing.T) {
	out, errOut, err := execute(t, "--help")
	if err != nil {
		t.Fatalf("fugaro --help: %v", err)
	}
	help := out + errOut
	for _, line := range strings.Split(help, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "init ") {
			t.Errorf("fugaro --help still offers the retired init:\n%s", help)
		}
	}
	if !strings.Contains(help, "setup") || !strings.Contains(help, "update") {
		t.Errorf("fugaro --help does not offer both successors:\n%s", help)
	}
}

// TestRetiredInitAppliesNothing: the tombstone reaches no cloud client at all
// — not a Terraform run, not an HTTP call to the rig's fake services, not even
// a read of the local config. The rig's endpoints are all fakes, so a run that
// constructed a client would leave a trace; this asserts on the trace and on a
// config path that does not exist, which a run that loaded it would fail on
// with a different message.
func TestRetiredInitAppliesNothing(t *testing.T) {
	r := newInitRig(t)
	before, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}

	out, errOut, err := executeStdin(t, "aurora\naurora\n", "init", "--yes", "--repo", ".")
	if err == nil {
		t.Fatal("the tombstone exited 0")
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d", got, ExitUserError)
	}
	if !strings.Contains(out+errOut+ErrorText(err), retiredInitMessage) {
		t.Errorf("the tombstone said something else:\n%s%s%s", out, errOut, ErrorText(err))
	}
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("terraform ran %v, want nothing", calls)
	}
	// The local config is untouched: the run wrote nothing into it.
	after, rerr := os.ReadFile(r.cfg)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(after) != string(before) {
		t.Errorf("the tombstone rewrote the local config:\n%s", after)
	}

	// And with no config to read at all: a run that loaded it would refuse
	// with the load error instead of the tombstone's sentence.
	t.Setenv("FUGARO_CONFIG", filepath.Join(t.TempDir(), "absent.yaml"))
	_, _, err = executeStdin(t, "", "init", "--yes")
	if err == nil || !strings.Contains(ErrorText(err), retiredInitMessage) {
		t.Errorf("with no config: %v", err)
	}
}

// TestConfigInitIsUntouched: `fugaro config init` is a different command with
// the same word (F21, Global Constraint 6), and the tombstone must not shadow
// it: it still resolves under `fugaro config`, it is not the retired verb, and
// it still writes the minimal config.
func TestConfigInitIsUntouched(t *testing.T) {
	root := NewRootCmd()
	sub, _, err := root.Find([]string{"config", "init"})
	if err != nil || sub.Name() != "init" {
		t.Fatalf("fugaro config init does not resolve: %v", err)
	}
	if sub == root {
		t.Fatal("fugaro config init resolved to the root command")
	}
	top, _, err := root.Find([]string{"init"})
	if err != nil || top == sub {
		t.Fatalf("fugaro init resolved to %v, want the retired top-level command", top.Name())
	}

	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "config", "init", "--project", "aurora", "--yes")
	if err != nil || !strings.Contains(out, "wrote") {
		t.Fatalf("fugaro config init: out %q, err %v", out, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "fugaro.yaml"))
	if err != nil || string(data) != minimalAnchored {
		t.Fatalf("fugaro.yaml = %q, want %q (%v)", data, minimalAnchored, err)
	}
}
