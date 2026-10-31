package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// upgradeEnv is a hermetic machine for fugaro upgrade at version v: no coding
// agent's session, nothing but the system tools on PATH, and empty Claude Code
// and Fugaro user configs.
func upgradeEnv(t *testing.T, v string) {
	t.Helper()
	withVersion(t, v)
	useSelf(t)
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	t.Setenv("PATH", strings.Join([]string{t.TempDir(), "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// No test finds the developer's own claude: the plugin step looks it up
	// through claudeLookPath, which finds nothing unless a test hands it the
	// fake (useFakeClaude).
	old := claudeLookPath
	claudeLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() { claudeLookPath = old })
}

func upgradeRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestUpgradeLocalPinsThenPinCurrent(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r := pluginwire.Status(settings, "0.5.2", ""); r.Pin != pluginwire.OK {
		t.Fatalf("pin %+v\n%s", r, out)
	}
	for _, want := range []string{"fugaro upgrade with fugaro 0.5.2", "brew upgrade dimipaun/tap/fugaro && fugaro upgrade", "== " + root,
		"pin: done: pinned to v0.5.2 in " + settings, "git diff", "summary:", "  " + root + ": pin done"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	before := upgradeRead(t, settings)
	out, _, err = executeStdin(t, "", "upgrade", "--local", root)
	if err != nil || upgradeRead(t, settings) != before || !strings.Contains(out, "pin: current: pinned to v0.5.2") || !strings.Contains(out, "nothing to do: "+root) {
		t.Fatalf("rerun: %v, changed %v\n%s", err, upgradeRead(t, settings) != before, out)
	}
}

// TestUpgradeEveryStepIsImplemented: pin, plugin and cloud run in that order
// and none is a placeholder, so each mode reports every step's own state:
// here pin current, plugin skipped (no claude on PATH, or --check with no
// install recorded), cloud skipped (no fugaro.yaml; under --local and in an
// agent session before anything is read, U7, so with their reason). With no
// step left unchecked the checkout is "nothing to do" and the exit is 0.
func TestUpgradeEveryStepIsImplemented(t *testing.T) {
	var names []string
	for _, s := range upgradeSteps {
		names = append(names, s.name)
	}
	if got := strings.Join(names, ","); got != "pin,plugin,cloud,rules,policy" {
		t.Fatalf("upgradeSteps = %s", got)
	}
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.2"))
	for _, tc := range []struct {
		name       string
		agent      bool
		args       []string
		pluginSkip string
	}{
		{"check", false, []string{"--check"}, "plugin: skipped: not installed on this machine"},
		{"local", false, []string{"--local"}, "plugin: skipped: claude is not on PATH"},
		{"yes", false, []string{"--yes"}, "plugin: skipped: claude is not on PATH"},
		{"agent", true, nil, "plugin: skipped: claude is not on PATH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.agent {
				t.Setenv(agentMarkers[0], "1")
			}
			out, _, err := executeStdin(t, "", append(append([]string{"upgrade"}, tc.args...), root)...)
			if err != nil || upgradeRead(t, settings) != wiredAt("v0.5.2") {
				t.Fatalf("%v\n%s", err, out)
			}
			cloud := "cloud: skipped: no fugaro.yaml yet, so no job image to refresh"
			rules := "rules: skipped: no fugaro.yaml yet, so no job image to refresh"
			switch tc.name {
			case "local":
				cloud, rules = "cloud: skipped: --local", "rules: skipped: --local"
			case "agent":
				cloud, rules = "cloud: skipped: a coding agent's session", "rules: skipped: a coding agent's session"
			case "check":
				// --check reaches its caveat before anything is read, the way
				// the policy step does, so it says it cannot tell whatever the
				// checkout holds.
				rules = "rules: skipped: --check makes no cloud call, so the deployed security rules are not read"
			}
			for _, want := range []string{"pin: current", tc.pluginSkip, cloud, rules,
				"  " + root + ": pin current, plugin skipped, cloud skipped, rules skipped, policy skipped",
				"nothing to do: " + root + " is current (apart from what the skipped steps name)"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			for _, bad := range []string{"not implemented", "not checked:", "plugin: current", "cloud: current", "rules: current"} {
				if strings.Contains(out, bad) {
					t.Errorf("output claims %q of a step that is skipped here:\n%s", bad, out)
				}
			}
		})
	}
}

// upgradeBudgetRig is a checkout of the project whose local config records the
// shared budget backend the fake database backs — the state fugaro upgrade has
// to reach the rules deploy from. updateBudgetRig is update's own half of it,
// and the checkout is what resolveCloudRepo reads the door through.
func upgradeBudgetRig(t *testing.T) (*initRig, *gcpfake.RTDB, string) {
	t.Helper()
	r, db := updateBudgetRig(t)
	return r, db, upgradeRigCheckout(t, r)
}

// upgradeRigCheckout is the git checkout a rig's door is read through
// (resolveCloudRepo): the repository directory the rig configured, with the
// origin remote the repository check accepts.
func upgradeRigCheckout(t *testing.T, r *initRig) string {
	t.Helper()
	root := gitCheckout(t, filepath.Join(r.dir, "app"), checkoutYAML("bitbucket", "api-key", initProjectName, ""))
	testutil.Git(t, root, "remote", "add", "origin", "https://bitbucket.org/acme/sandbox.git")
	return root
}

// runUpgradeRulesStep runs the rules step alone, against one checkout and with
// its own confirmation taken (--yes), and returns the verdict and what the step
// printed. It is the step, not the command: these cases are about the states a
// checkout holds when the step is reached, and the steps before it are the
// fixtures' own (TestUpgradeEveryStepIsImplemented drives the modes).
func runUpgradeRulesStep(t *testing.T, root string) (stepResult, string) {
	t.Helper()
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	u := &upgradeCtx{cmd: cmd, w: out, loc: pluginwire.Location{Root: root}}
	u.o.yes = true
	return rulesStep(context.Background(), u), out.String()
}

// TestUpgradeRunsTheRulesStep: fugaro upgrade runs the rules deploy the update
// door's rules stage runs, in the one slot it can run it — before the policy
// step, whose writer refuses a migration the deployed rules would not admit
// (requirePolicyRules) — so an installation converges from either front door
// and one upgrade is the whole move, the rule of 2026-10-30. The document
// written and the generation recorded are that stage's own, not a second
// implementation of them.
func TestUpgradeRunsTheRulesStep(t *testing.T) {
	var names []string
	for _, s := range upgradeSteps {
		names = append(names, s.name)
	}
	if want := []string{"pin", "plugin", "cloud", "rules", "policy"}; !slices.Equal(names, want) {
		t.Fatalf("the steps are %v, want %v", names, want)
	}
	if got := reflect.ValueOf(upgradeSteps[3].run).Pointer(); got != reflect.ValueOf(rulesStep).Pointer() {
		t.Error("the rules step is registered under some other function")
	}
	_, db, root := upgradeBudgetRig(t)
	// The state the step exists for: the project's database holds rules that
	// carry no generation marker, so a project policy in it would be read
	// under rules that predate the policy model.
	db.SetRules([]byte(`{"rules":{".read":false}}`))
	got, out := runUpgradeRulesStep(t, root)
	if got.state != stepDone {
		t.Fatalf("the rules step is %s: %s\n%s", got.state, got.reason, out)
	}
	gen, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !infra.SameJSON(db.Rules(), gen) {
		t.Errorf("the deployed rules are not this Fugaro's generation:\n%s", db.Rules())
	}
	if db.RulePuts() == 0 {
		t.Error("the step reported a deploy that wrote no rules")
	}
	if want := strconv.Itoa(projectpolicy.RulesVersion); rulesMarkerOn(t, db) != want {
		t.Errorf("the rules generation marker is %q, want %q", rulesMarkerOn(t, db), want)
	}
	if !strings.Contains(out, "The security rules of the Firebase project "+initProject+":") {
		t.Errorf("the plan was not shown before the confirmation:\n%s", out)
	}
}

// TestUpgradeRulesStepVerdicts: the states a checkout can reach the rules step
// in that are NOT the deploy, and what each one must leave behind. The step's
// own early exits (--check, --local and a coding agent's session, and the
// cloudNotHere checkout) are TestUpgradeEveryStepIsImplemented's, at the
// command, where the modes live.
//
// The one state left out is Plan finding nothing to do: every state the
// classification calls a deploy (rulesSkewTodo) carries a write of its own —
// the deploy where the document differs, the generation marker where it is
// absent or older — so the branch is a guard for a classification that changed,
// not a state a fixture can hold. A test that forced it would be asserting the
// step's sentence about a database it cannot build.
func TestUpgradeRulesStepVerdicts(t *testing.T) {
	t.Run("current", func(t *testing.T) {
		_, db, root := upgradeBudgetRig(t)
		gen, err := rules.Generate()
		if err != nil {
			t.Fatal(err)
		}
		db.SetRules(gen)
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
		got, out := runUpgradeRulesStep(t, root)
		if got.state != stepCurrent {
			t.Fatalf("the rules step is %s: %s\n%s", got.state, got.reason, out)
		}
		if db.RulePuts() != 0 {
			t.Error("the step rewrote rules that are already this generation")
		}
		if !strings.Contains(got.reason, "this Fugaro's own") {
			t.Errorf("the settled state's sentence is %q", got.reason)
		}
		if strings.Contains(out, "deploys the security rules listed") {
			t.Errorf("the step asked to deploy rules that are already current:\n%s", out)
		}
	})
	t.Run("unadopted", func(t *testing.T) {
		// The database is in a Firebase project the config names and this
		// installation never adopted (separateFirebaseRig: the pair agrees with
		// itself and the database is empty, which infra.DB.Check passes): the
		// ownership predicate is what refuses it, and the skip has to name the
		// one move that adopts it — this binary's own project.
		r, db := separateFirebaseRig(t)
		got, out := runUpgradeRulesStep(t, upgradeRigCheckout(t, r))
		if got.state != stepSkipped {
			t.Fatalf("the rules step is %s: %s\n%s", got.state, got.reason, out)
		}
		for _, want := range []string{"budget.firebase_project", "other-project", selfCommand() + " update --firebase-project " + initProject} {
			if !strings.Contains(got.reason, want) {
				t.Errorf("the skip does not name %q: %q", want, got.reason)
			}
		}
		if db.RulePuts() != 0 || db.Value(projectpolicy.PathRulesMarker()) != nil {
			t.Error("the step deployed rules into a database in a project the installation never adopted")
		}
		if strings.Contains(out, "deploys the security rules listed") {
			t.Errorf("the step offered to deploy into a database it may not write:\n%s", out)
		}
	})
	t.Run("newer", func(t *testing.T) {
		_, db, root := upgradeBudgetRig(t)
		newer := projectpolicy.RulesVersion + 1
		theirs := []byte(`{"rules":{".read":"auth != null"}}`)
		db.Set(projectpolicy.PathRulesMarker(), newer)
		db.SetRules(theirs)
		got, out := runUpgradeRulesStep(t, root)
		if got.state != stepSkipped {
			t.Fatalf("the rules step is %s: %s\n%s", got.state, got.reason, out)
		}
		if !strings.Contains(got.reason, "a newer Fugaro deployed them") {
			t.Errorf("the skip does not report why it deployed nothing: %q", got.reason)
		}
		if db.RulePuts() != 0 || !bytes.Equal(db.Rules(), theirs) {
			t.Error("the step deployed this binary's document over a newer generation")
		}
		if want := strconv.Itoa(newer); rulesMarkerOn(t, db) != want {
			t.Errorf("the marker is %q, want %q: it must never be rewritten down", rulesMarkerOn(t, db), want)
		}
	})
	t.Run("refused write", func(t *testing.T) {
		_, db, root := upgradeBudgetRig(t)
		db.SetRules([]byte(`{"rules":{".read":false}}`))
		db.DenyRules(1)
		got, out := runUpgradeRulesStep(t, root)
		if got.state != stepFailed || got.code != ExitUserError {
			t.Fatalf("a refused rules deploy is %s (exit %d): %s\n%s", got.state, got.code, got.reason, out)
		}
		if db.RulePuts() != 0 {
			t.Error("the fake recorded a rules deploy it refused")
		}
		if got := rulesMarkerOn(t, db); got != "" {
			t.Errorf("the marker is %q after a refused deploy, want it absent: the step would claim rules this database does not run under", got)
		}
	})
}

func TestUpgradeCheckWritesNothing(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	before := upgradeRead(t, settings)
	out, _, err := executeStdin(t, "", "upgrade", "--check", root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "something is stale") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if upgradeRead(t, settings) != before || !strings.Contains(out, "pin: stale: the pin moves to v0.5.2") {
		t.Fatalf("changed %v\n%s", upgradeRead(t, settings) != before, out)
	}
}

// TestRefAboveBinaryComparesPatchNumerically: v0.5.10 is above 0.5.2 (patch
// 10 > 2), which a string comparison would get backwards ("10" < "2").
func TestRefAboveBinaryComparesPatchNumerically(t *testing.T) {
	withVersion(t, "0.5.2")
	if !refAboveBinary("v0.5.10") {
		t.Error("v0.5.10 should read as above 0.5.2: the patch is compared numerically, not as a string")
	}
	if refAboveBinary("v0.5.1") {
		t.Error("v0.5.1 should not read as above 0.5.2")
	}
}

// TestUpgradeNeverMovesAPinDown: U10 for every marketplace and enablement
// state, not only the one pluginwire.Status calls newer.
func TestUpgradeNeverMovesAPinDown(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		yaml           bool
		flags          []string
	}{
		{"newer", wiredAt("v0.6.0"), false, []string{"--local"}},
		{"fork", `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"someone/fugaro","ref":"v0.6.0"}}},"enabledPlugins":{"fugaro@fugaro":true}}`, false, []string{"--local", "--allow-fork"}},
		{"disabled", `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.6.0"}}},"enabledPlugins":{"fugaro@fugaro":false}}`, true, []string{"--local"}},
		{"check", wiredAt("v0.6.0"), false, []string{"--check"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upgradeEnv(t, "0.5.2")
			root, settings := skillCheckout(t, tc.settings)
			if tc.yaml {
				if err := os.WriteFile(filepath.Join(root, "fugaro.yaml"), []byte("version: 1\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out, _, err := executeStdin(t, "", append(append([]string{"upgrade"}, tc.flags...), root)...)
			if ExitCode(err) != ExitUserError || upgradeRead(t, settings) != tc.settings {
				t.Fatalf("exit %d, changed %v\n%s", ExitCode(err), upgradeRead(t, settings) != tc.settings, out)
			}
			for _, want := range []string{"pin: failed: the plugin is pinned to 0.6.0, newer than this fugaro 0.5.2", "never moves a pin down", "brew upgrade dimipaun/tap/fugaro",
				"  " + root + ": pin failed, plugin not run, cloud not run, rules not run, policy not run", "once that is fixed, rerun: fugaro upgrade " + strings.Join(tc.flags, " ") + " " + root} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "pin: done") || strings.Contains(out, "v0.5.2") {
				t.Errorf("the pin moved down:\n%s", out)
			}
		})
	}
}

// TestUpgradeDevBuildNeedsNoTerminal is U11: a development build has no
// release to pin, install or refresh to, so every step is skipped and the
// run exits 0, even with no flags and no terminal on stdin (the refusal that
// otherwise requires one is for the cloud step a release build would run).
func TestUpgradeDevBuildNeedsNoTerminal(t *testing.T) {
	upgradeEnv(t, "dev")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	before := upgradeRead(t, settings)
	out, _, err := executeStdin(t, "", "upgrade", root)
	if err != nil || upgradeRead(t, settings) != before || !strings.Contains(out, "a development build (dev)") || !strings.Contains(out, "pin: skipped: a development build") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUpgradeDevBuildChangesNothing(t *testing.T) {
	upgradeEnv(t, "dev")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	before := upgradeRead(t, settings)
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil || upgradeRead(t, settings) != before || !strings.Contains(out, "a development build (dev)") || !strings.Contains(out, "pin: skipped: a development build") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestUpgradeDevBuildSkipsEveryPath is U11 for paths that are not Fugaro
// checkouts, or not checkouts at all: every step skipped, exit 0.
func TestUpgradeDevBuildSkipsEveryPath(t *testing.T) {
	upgradeEnv(t, "dev")
	notCheckout := t.TempDir()
	plain, _ := skillCheckout(t, "")
	out, _, err := executeStdin(t, "", "upgrade", "--local", notCheckout, plain)
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	for _, want := range []string{"  " + notCheckout + ": pin skipped, plugin skipped, cloud skipped, rules skipped, policy skipped", "  " + plain + ": pin skipped, plugin skipped, cloud skipped, rules skipped, policy skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "failed") || strings.Contains(out, "is current") {
		t.Errorf("a development build judged a path:\n%s", out)
	}
}

func TestUpgradeEveryPathIsTried(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	notCheckout := t.TempDir()
	plain, _ := skillCheckout(t, "") // a checkout, but not a Fugaro one
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", notCheckout, plain, root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "stopped in 2 of 3 checkout(s)") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if r := pluginwire.Status(settings, "0.5.2", ""); r.Pin != pluginwire.OK {
		t.Fatalf("the failures hid the last checkout: %+v\n%s", r, out)
	}
	for _, want := range []string{"  " + notCheckout + ": checkout failed", "  " + plain + ": checkout failed", "not a Fugaro checkout", "  " + root + ": pin done"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(plain, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Error("a settings file was written in a checkout that is not a Fugaro one")
	}
}

func TestUpgradeSameCheckoutOnce(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, _ := skillCheckout(t, wiredAt("v0.5.1"))
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(filepath.Join(sub, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".claude", "settings.json"), []byte(wiredAt("v0.5.1")), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "upgrade", "--local", root, filepath.Join(root, ".claude"), link, sub)
	if err != nil || strings.Count(out, "pin: done") != 1 || strings.Count(out, "checkout: skipped: the same checkout as "+root) != 3 {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestUpgradeCountsDistinctCheckouts: "stopped in N of M" counts a checkout
// given twice once.
func TestUpgradeCountsDistinctCheckouts(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	notCheckout := t.TempDir()
	root, _ := skillCheckout(t, wiredAt("v0.5.1"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", notCheckout, root, root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "stopped in 1 of 2 checkout(s)") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
}

func TestUpgradeForkNeedsAllowFork(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	fork := `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"someone/fugaro","ref":"v0.5.1"}}},"enabledPlugins":{"fugaro@fugaro":true}}`
	root, settings := skillCheckout(t, fork)
	out, errOut, err := executeStdin(t, "", "upgrade", "--local", root)
	if ExitCode(err) != ExitUserError || upgradeRead(t, settings) != fork || !strings.Contains(errOut, "WARNING") || !strings.Contains(out, "pass --allow-fork if it is yours") {
		t.Fatalf("exit %d\n%s\n%s", ExitCode(err), out, errOut)
	}
	out, errOut, err = executeStdin(t, "", "upgrade", "--local", "--allow-fork", root)
	if data := upgradeRead(t, settings); err != nil || !strings.Contains(data, "v0.5.2") || !strings.Contains(data, "someone/fugaro") {
		t.Fatalf("%v\n%s\n%s", err, data, out)
	}
	if !strings.Contains(errOut, "WARNING: the \"fugaro\" marketplace in "+settings+" is someone/fugaro, not dimipaun/fugaro") || !strings.Contains(out, "pin: done") {
		t.Errorf("the fork's ref moved without the warning:\n%s\n%s", out, errOut)
	}
}

// TestUpgradeCheckReportsAFork: --check reads a fork's pin as stale (exit 1),
// with the warning, and writes nothing.
func TestUpgradeCheckReportsAFork(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	fork := `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"someone/fugaro","ref":"v0.5.1"}}},"enabledPlugins":{"fugaro@fugaro":true}}`
	root, settings := skillCheckout(t, fork)
	out, errOut, err := executeStdin(t, "", "upgrade", "--check", root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "something is stale") || upgradeRead(t, settings) != fork {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	for _, want := range []string{"pin: stale: the marketplace is the fork someone/fugaro: pass --allow-fork if it is yours (only its ref moves)",
		"plugin: skipped: cannot tell from Claude Code's record of installed plugins",
		"  " + root + ": pin stale, plugin skipped, cloud skipped, rules skipped, policy skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "WARNING") {
		t.Errorf("no warning:\n%s", errOut)
	}
}

func TestUpgradeInvalidSettingsNeverRewritten(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, "{not json")
	if err := os.WriteFile(filepath.Join(root, "fugaro.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if ExitCode(err) != ExitUserError || upgradeRead(t, settings) != "{not json" || !strings.Contains(out, "merge this in by hand") {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
}

func TestUpgradeFlagRules(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--check", "--yes"}, "--check writes nothing"},
		{[]string{"--local", "--yes"}, "--yes confirms the cloud, rules and policy steps"},
		{[]string{"--local", "--repo", "acme/app"}, "which --local skips"},
		{[]string{"--yes", "--workflow", "app", root, root}, "give them with one PATH"},
	} {
		args := append([]string{"upgrade"}, tc.args...)
		if !strings.HasSuffix(strings.Join(args, " "), root) {
			args = append(args, root)
		}
		out, _, err := executeStdin(t, "", args...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.want) || strings.Contains(out, "== ") {
			t.Errorf("%q: exit %d, err %v\n%s", tc.args, ExitCode(err), err, out)
		}
	}
	if upgradeRead(t, settings) != wiredAt("v0.5.1") {
		t.Error("a refused command wrote the settings")
	}
}

func TestUpgradeNeedsATerminal(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	out, _, err := executeStdin(t, "", "upgrade", root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), initflow.NoTerminalAdvice) || !strings.Contains(err.Error(), "--local") ||
		strings.Contains(out, "== ") || upgradeRead(t, settings) != wiredAt("v0.5.1") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
}
