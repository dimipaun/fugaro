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

	"github.com/dimipaun/fugaro/internal/budget"
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

// TestUpgradeLocalWritesNothingInTheCheckout is U2 on the door the pin left
// behind: --local runs the plugin step, which installs in Claude Code and
// writes nothing in the checkout, and the pin an older fugaro left in
// .claude/settings.json comes out of the run byte for byte as it was. Red
// against the tree the pin step was in, which rewrote the file's ref.
func TestUpgradeLocalWritesNothingInTheCheckout(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	before := upgradeRead(t, settings)
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if upgradeRead(t, settings) != before {
		t.Fatalf("the checkout's settings were rewritten:\n%s", upgradeRead(t, settings))
	}
	for _, want := range []string{"fugaro upgrade with fugaro 0.5.2; it never upgrades fugaro itself: brew upgrade dimipaun/tap/fugaro && fugaro upgrade is the whole sequence", "== " + root,
		"plugin: skipped: claude is not on PATH", "summary:", "  " + root + ": plugin skipped, cloud skipped, rules skipped, policy skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The rerun is the same run: nothing on this machine changed, and the
	// step has no state of its own to report but the machine's.
	before = upgradeRead(t, settings)
	if out, _, err = executeStdin(t, "", "upgrade", "--local", root); err != nil || upgradeRead(t, settings) != before {
		t.Fatalf("rerun: %v, changed %v\n%s", err, upgradeRead(t, settings) != before, out)
	}
}

// TestUpgradeEveryStepIsImplemented: plugin and cloud run in that order and
// none is a placeholder, so each mode reports every step's own state: here
// plugin skipped (no claude on PATH, or --check with no install recorded),
// cloud skipped because no local project config selects this checkout — under
// --local and in an agent session the step is skipped before anything is read
// at all, U7, so with their reason. With no step left unchecked the checkout is
// "nothing to do" and the exit is 0. The steps are four: the pin went with the
// merge it wrote (U2).
func TestUpgradeEveryStepIsImplemented(t *testing.T) {
	var names []string
	for _, s := range upgradeSteps {
		names = append(names, s.name)
	}
	if got := strings.Join(names, ","); got != "plugin,cloud,rules,policy" {
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
			cloud := "cloud: skipped: no local project config selects this checkout"
			rules := "rules: skipped: no local project config selects this checkout"
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
			for _, want := range []string{tc.pluginSkip, cloud, rules,
				"  " + root + ": plugin skipped, cloud skipped, rules skipped, policy skipped",
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

// runUpgradeStep runs one step function alone, against one checkout and with
// its own confirmation taken (--yes), and returns the verdict and what the step
// printed. It is the step, not the command: these cases are about the states a
// checkout holds when the step is reached, and the steps before it are the
// fixtures' own (TestUpgradeEveryStepIsImplemented drives the modes, and
// TestUpgradeRunsTheRulesStepBeforeThePolicyItGates drives the loop).
func runUpgradeStep(t *testing.T, root string, run func(context.Context, *upgradeCtx) stepResult) (stepResult, string) {
	t.Helper()
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	u := &upgradeCtx{cmd: cmd, w: out, loc: pluginwire.Location{Root: root}}
	u.o.yes = true
	return run(context.Background(), u), out.String()
}

func runUpgradeRulesStep(t *testing.T, root string) (stepResult, string) {
	t.Helper()
	return runUpgradeStep(t, root, rulesStep)
}

func runUpgradePolicyStep(t *testing.T, root string) (stepResult, string) {
	t.Helper()
	return runUpgradeStep(t, root, policyStep)
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
	if want := []string{"plugin", "cloud", "rules", "policy"}; !slices.Equal(names, want) {
		t.Fatalf("the steps are %v, want %v", names, want)
	}
	if got := reflect.ValueOf(upgradeSteps[2].run).Pointer(); got != reflect.ValueOf(rulesStep).Pointer() {
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
		t.Errorf("the plan was not shown before the deploy:\n%s", out)
	}
}

// TestUpgradeRulesStepAsksNothing is D1540 on the one step of this command
// that carried a typed confirmation: the deploy is named — the plan above it
// and the sentence under it — and it goes ahead with no --yes and no terminal,
// which is the shape a script drives this command in. A step that still asked
// would refuse this run instead of deploying.
func TestUpgradeRulesStepAsksNothing(t *testing.T) {
	_, db, root := upgradeBudgetRig(t)
	db.SetRules([]byte(`{"rules":{".read":false}}`))
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	u := &upgradeCtx{cmd: cmd, w: out, loc: pluginwire.Location{Root: root}}

	got := rulesStep(context.Background(), u)
	if got.state != stepDone {
		t.Fatalf("the rules step is %s: %s\n%s", got.state, got.reason, out)
	}
	assertNoPrompt(t, out.String())
	if !strings.Contains(out.String(), "deploys the security rules listed") {
		t.Errorf("the sentence naming the deploy is not printed:\n%s", out)
	}
	if want := strconv.Itoa(projectpolicy.RulesVersion); rulesMarkerOn(t, db) != want {
		t.Errorf("the rules generation marker is %q, want %q", rulesMarkerOn(t, db), want)
	}
}

// TestUpgradeRunsTheRulesStepBeforeThePolicyItGates is the two steps the
// owner's rule of 2026-10-30 is about, through the shared loop and in the order
// the loop runs them: the project's database carries rules that predate the
// policy model (no generation marker) and a published node an earlier Fugaro
// wrote at version 3, so the policy migration's own gate — requirePolicyRules,
// the publisher's — refuses it until this binary's rules are deployed. One run
// has to do both: the rules step deploys the generation and records it, and the
// migration passes the gate the step before it satisfied.
//
// The second arm is what makes the first one mean something: the SAME migration
// on the same fixture, with the rules step left out, is refused by that gate and
// leaves the node where it was. Without it the loop's second `done` would be
// satisfied by a gate that never refuses.
func TestUpgradeRunsTheRulesStepBeforeThePolicyItGates(t *testing.T) {
	// The cloud rig, because the run is the whole loop and the cloud step has to
	// settle before the two steps this case is about: the checkout, the local
	// project config listing it, the fake terraform and the stub that answers
	// the build records the refresh reads are upgradeRigWithGitHubAppID's, and
	// on top of them the installation's shared budget backend — the fake
	// database, marked as this installation's, with rules that carry no
	// generation marker and a published node at version 3.
	rig := func(t *testing.T) (*anchorModeRig, *gcpfake.RTDB) {
		t.Helper()
		r := upgradeRigWithGitHubAppID(t)
		db := gcpfake.NewRTDB(t)
		db.Set(budget.PathMark, map[string]any{"managed_by": "fugaro", "project": initProjectName, "gcp_project": initProject, "version": 1})
		db.Set(budget.PathProject, initProjectName)
		db.Set(budget.PathMode, budget.ModeEnforce)
		db.Set(budget.PathLimits, map[string]any{"maxReserveMicros": 5_000_000})
		r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 5, rtdb_url: "+db.URL+", firebase_project: \""+initProject+"\" }\n")
		db.SetRules([]byte(`{"rules":{".read":false}}`))
		db.Set(projectpolicy.PathShared(), v3Node(nil))
		stubCurrentRefresh(t, r)
		fakeSteps(t, "", nil)
		return r, db
	}

	t.Run("one run deploys the rules and then migrates the node", func(t *testing.T) {
		_, db := rig(t)
		out, errOut, err := executeStdin(t, "", "upgrade", "--yes")
		if err != nil {
			t.Fatalf("%v\n%s%s", err, out, errOut)
		}
		for _, want := range []string{
			"rules: done: deployed the security rules (generation " + strconv.Itoa(projectpolicy.RulesVersion) + ") and recorded it",
			"policy: done: migrated the published policy from version 3 to " + strconv.Itoa(projectpolicy.Version) + " in place",
			"The security rules of the Firebase project " + initProject + ":",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
		if got, want := rulesMarkerOn(t, db), strconv.Itoa(projectpolicy.RulesVersion); got != want {
			t.Errorf("the rules generation marker is %q, want %q", got, want)
		}
		if got := nodeVersionOn(t, db); got != projectpolicy.Version {
			t.Errorf("the published node is version %d, want %d: the migration never ran", got, projectpolicy.Version)
		}
	})

	t.Run("a run that publishes and migrates nothing says so", func(t *testing.T) {
		r, db := rig(t)
		// The node is at this version — nothing to migrate — and carries a
		// ceiling the checkout's own config does not state, under a rules
		// generation that is already deployed: the whole of this run's write is
		// the publish.
		db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
		db.Set(projectpolicy.PathShared(), v3Node(map[string]any{
			"version": projectpolicy.Version,
			"budget":  map[string]any{"mode": "enforce", "per_run_usd": 9},
		}))
		got, out := runUpgradePolicyStep(t, r.root)
		if got.state != stepDone {
			t.Fatalf("the policy step is %s: %s\n%s", got.state, got.reason, out)
		}
		// The outcome is the write's own line (doneDetail): a step that reported a
		// migration here would report one from this version to this version.
		if want := "published the node the local config states"; got.reason != want {
			t.Errorf("the step reports %q, want %q", got.reason, want)
		}
		if got := nodeVersionOn(t, db); got != projectpolicy.Version {
			t.Errorf("the published node is version %d, want %d", got, projectpolicy.Version)
		}
	})

	t.Run("without the rules step the same migration is refused", func(t *testing.T) {
		r, db := rig(t)
		got, out := runUpgradePolicyStep(t, r.root)
		if got.state != stepFailed || got.code != ExitUserError {
			t.Fatalf("the policy step alone is %s (exit %d): %s\n%s", got.state, got.code, got.reason, out)
		}
		if !strings.Contains(got.reason, "predate the policy model") {
			t.Errorf("the refusal is %q, want the gate's own sentence about the deployed rules", got.reason)
		}
		if got := nodeVersionOn(t, db); got != 3 {
			t.Errorf("the refused migration left the node at version %d, want 3", got)
		}
		if got := rulesMarkerOn(t, db); got != "" {
			t.Errorf("the marker is %q after a run that deployed nothing", got)
		}
	})
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

// TestUpgradeCheckWritesNothing: --check reads Claude Code's record of
// installed plugins and writes nothing — the checkout's settings included,
// where a pin an older fugaro left stands whatever the machine's install says.
func TestUpgradeCheckWritesNothing(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	record := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json")
	checkoutFile(t, record, `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.1"}]}}`)
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	before := upgradeRead(t, settings)
	out, _, err := executeStdin(t, "", "upgrade", "--check", root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "something is stale") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if upgradeRead(t, settings) != before || !strings.Contains(out, "plugin: stale: installed 0.5.1, this fugaro is 0.5.2") {
		t.Fatalf("changed %v\n%s", upgradeRead(t, settings) != before, out)
	}
}

// TestUpgradeDevBuildNeedsNoTerminal is U11: a development build has no
// release to install or refresh to, so every step is skipped and the run exits
// 0, even with no flags and no terminal on stdin (the refusal that otherwise
// requires one is for the cloud step a release build would run).
func TestUpgradeDevBuildNeedsNoTerminal(t *testing.T) {
	upgradeEnv(t, "dev")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	before := upgradeRead(t, settings)
	out, _, err := executeStdin(t, "", "upgrade", root)
	if err != nil || upgradeRead(t, settings) != before || !strings.Contains(out, "a development build (dev)") || !strings.Contains(out, "plugin: skipped: a development build") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUpgradeDevBuildChangesNothing(t *testing.T) {
	upgradeEnv(t, "dev")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	before := upgradeRead(t, settings)
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil || upgradeRead(t, settings) != before || !strings.Contains(out, "a development build (dev)") || !strings.Contains(out, "plugin: skipped: a development build") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestUpgradeDevBuildSkipsEveryPath is U11 for paths that are not Fugaro
// checkouts, or not checkouts at all: every step skipped, exit 0.
func TestUpgradeDevBuildSkipsEveryPath(t *testing.T) {
	upgradeEnv(t, "dev")
	notCheckout := t.TempDir()
	plain := plainCheckout(t)
	out, _, err := executeStdin(t, "", "upgrade", "--local", notCheckout, plain)
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	for _, want := range []string{"  " + notCheckout + ": plugin skipped, cloud skipped, rules skipped, policy skipped", "  " + plain + ": plugin skipped, cloud skipped, rules skipped, policy skipped"} {
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
	plain := plainCheckout(t) // a checkout, but not a Fugaro one
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", notCheckout, plain, root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "stopped in 2 of 3 checkout(s)") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if upgradeRead(t, settings) != wiredAt("v0.5.1") {
		t.Errorf("nothing writes the checkout's settings any more (U2), and this run changed them:\n%s", upgradeRead(t, settings))
	}
	for _, want := range []string{"  " + notCheckout + ": checkout failed", "  " + plain + ": checkout failed", "not a Fugaro checkout", "  " + root + ": plugin skipped, cloud skipped, rules skipped, policy skipped"} {
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
	if err != nil || strings.Count(out, "== "+root) != 1 || strings.Count(out, "checkout: skipped: the same checkout as "+root) != 3 {
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

// TestUpgradeHasNoAllowFork: the flag belonged to the write — a checkout's
// settings pointing at someone else's marketplace, which the pin step then
// moved — and U2 removes it with the write. What a leftover fork says is
// update-skills --check's and fugaro doctor's to report, never this step's,
// which declares Fugaro's own marketplace on the machine.
func TestUpgradeHasNoAllowFork(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	fork := forkAt("someone/fugaro", "v0.5.1")
	root, settings := skillCheckout(t, fork)
	out, errOut, err := executeStdin(t, "", "upgrade", "--local", "--allow-fork", root)
	if !strings.Contains(err.Error(), "unknown flag: --allow-fork") {
		t.Fatalf("--allow-fork is still a flag: exit %d, err %v\n%s\n%s", ExitCode(err), err, out, errOut)
	}
	if upgradeRead(t, settings) != fork {
		t.Fatalf("a refused command rewrote the settings:\n%s", upgradeRead(t, settings))
	}
	// A leftover fork is not this step's business either way: it installs from
	// dimipaun/fugaro, and the file it came out of is untouched.
	out, errOut, err = executeStdin(t, "", "upgrade", "--local", root)
	if err != nil || upgradeRead(t, settings) != fork || strings.Contains(out, "someone/fugaro") || strings.Contains(errOut, "WARNING") {
		t.Fatalf("%v\n%s\n%s", err, out, errOut)
	}
}

// TestUpgradeInvalidSettingsNeverRewritten: the plugin step reads no repository
// out of the checkout any more, so settings it could not parse are not a
// reason to stop — and nothing rewrites them either.
func TestUpgradeInvalidSettingsNeverRewritten(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, "{not json")
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil || upgradeRead(t, settings) != "{not json" {
		t.Fatalf("exit %d, settings %q\n%s", ExitCode(err), upgradeRead(t, settings), out)
	}
	for _, want := range []string{"plugin: skipped: claude is not on PATH", "  " + root + ": plugin skipped, cloud skipped, rules skipped, policy skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
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
