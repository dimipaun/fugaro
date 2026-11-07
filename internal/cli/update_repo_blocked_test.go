package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// convergeLegacyYAML is acme/app with a key 0.7.0 removed (workflows.*.base):
// the file /fugaro:setup wrote before the base-image migration and the owner
// never cleaned up. It is *invalid* — the strict reader refuses the line — so
// the repository stage stops needs-you and the checkout it belongs to is not
// onboarded again by this run.
const convergeLegacyYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { base: server, commands: { build: sh build.sh, test: sh test.sh } }\n"

// TestUpdateConvergesPastAnInvalidCheckoutFugaroYAML (#1836): a checkout whose
// default-branch fugaro.yaml still carries a key 0.7.0 removed stops the
// repository stage — the user's to fix, `fugaro validate` — and the checkout's
// own local steps (the pin in .claude/settings.json and the plugin in Claude
// Code) read that file nowhere, so the run must still reach them. The plugin
// install is the one the HARD RULE is about here: it is the step that clears
// the line every command prints ("the installed Fugaro plugin is X but
// .claude/settings.json pins Y: run fugaro update"), so a run that never
// reaches it leaves the reader of that line in a loop with no way out.
//
// The stages that publish the installation (rules, policy, layer) stay behind
// the repository stage, and this test pins that too: what they publish is what
// admits every cloud run of the project, and a run with a stage left for the
// user does not write it (accounts_add_converge_test.go's F1).
//
// Mutation (run, restore): drop Checkout's pastStops row (initflow/stage.go)
// and this test fails at the first assertion on the checkout stage — it is
// blocked, the pin and the plugin never move, and the local plugin stays on
// the previous release.
func TestUpdateConvergesPastAnInvalidCheckoutFugaroYAML(t *testing.T) {
	r := newConvergeRigYAML(t, v4Node(), convergeLegacyYAML)
	currentRelease(t)
	// The line is once per process and this test is about the process's only
	// one: an earlier test's run may have spent it.
	skillWarned.Store(false)
	t.Cleanup(func() { skillWarned.Store(false) })

	// The fixture is the issue's state: Claude Code holds the previous
	// release's plugin while the checkout pins this one's, which is the line
	// doctor and every command warn about.
	if got := claudeListVersion(t, r); got != convergePrev {
		t.Fatalf("the fixture's installed plugin is %q, want %q", got, convergePrev)
	}

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err == nil {
		t.Fatalf("the run reported success while the checkout's own fugaro.yaml is invalid:\n%s", out)
	}
	var res convergeJSON
	if uerr := json.Unmarshal([]byte(runJSONObject(t, out)), &res); uerr != nil {
		t.Fatalf("%v:\n%s\n%s", uerr, out, errOut)
	}

	// The checkout's file is still the user's to fix, and the run leaves them
	// the command that says how.
	if got := res.state(initflow.Repository); got != "needs-you" {
		t.Errorf("the repository stage is %q, want needs-you: the invalid file is the user's to fix\n%s", got, out)
	}
	leftRepo := false
	for _, l := range res.Left {
		if l["stage"] == initflow.Repository && strings.Contains(l["text"], "fugaro validate") {
			leftRepo = true
		}
	}
	if !leftRepo {
		t.Errorf("the run does not leave the invalid fugaro.yaml for the user:\n%s", out)
	}

	// The checkout's own steps ran: they read no repository fugaro.yaml, and
	// they are the ones the user's stuck line is about.
	if got := res.state(initflow.Checkout); got == "blocked" {
		t.Errorf("the checkout stage is blocked by the repository stage, whose file its pin and plugin steps never read:\n%s", out)
	}
	// The stages that publish the installation are not reached, and that is
	// the deliberate other half: a run with a stage left for the user does not
	// write what admits its runs.
	for _, name := range []string{initflow.Rules, initflow.Policy, initflow.Layer} {
		if got := res.state(name); got != "blocked" {
			t.Errorf("the %s stage is %q, want blocked behind the repository stage: it publishes installation state, and a run that is not converged does not publish what admits its runs\n%s", name, got, out)
		}
	}

	// The plugin install ran — the step the HARD RULE is about — and Claude
	// Code's own list is this release's afterwards.
	if calls := r.claude.Changes(t); len(calls) == 0 {
		t.Errorf("the run never ran claude to install the plugin:\n%s", out)
	}
	if got := claudeListVersion(t, r); got != convergeRelease {
		t.Errorf("Claude Code lists the plugin as %q after fugaro update, want %q: the checkout stage never installed it\n%s", got, convergeRelease, out)
	}

	// And the line the user is stuck on is said after the run, never before it.
	staleLineWaitsForTheRun(t, errOut)
}

// TestUpdateDoesNotBuildFromACheckoutFileTheRunDidNotOnboard (#1836, review):
// the checkout runs past the repository stage's needs-you for the pin and the
// plugin, which read this machine's own files, and its cloud step is the third
// step of the same stage — the one that builds the repository's job images from
// that very fugaro.yaml, billable Cloud Builds included. A working tree whose
// file is valid but not the default branch's (an edit not merged yet) is
// exactly what the repository stage refuses to onboard, so the run must not
// build from it: the tolerance is for the local half, and an image from a
// config no cloud run of the project reads is the other half.
//
// The fixture is the drift the file's own parse alone cannot see: the default
// branch holds refreshYAML and the working tree holds the same file with one
// workflow's build command changed, which resolves and names this project.
//
// Mutation (run, restore): drop the cloud skip from planFor
// (init_checkout_upgrade.go) and this test fails — the checkout stage's own
// line reads "ran fugaro upgrade's pin, plugin, cloud for this checkout" and
// the change that edit asked for reaches Cloud Build.
func TestUpdateDoesNotBuildFromACheckoutFileTheRunDidNotOnboard(t *testing.T) {
	r := newConvergeRigYAML(t, v4Node(), refreshYAML)
	currentRelease(t)
	skillWarned.Store(false)
	t.Cleanup(func() { skillWarned.Store(false) })
	// One unmerged edit to the checkout's file: valid, this project's, and not
	// the default branch's, so the repository stage stops over it.
	checkoutFile(t, filepath.Join(r.root, "fugaro.yaml"), strings.Replace(refreshYAML, "build: sh build.sh", "build: sh build2.sh", 1))

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err == nil {
		t.Fatalf("the run reported success while the checkout's fugaro.yaml is not the default branch's:\n%s", out)
	}
	var res convergeJSON
	if uerr := json.Unmarshal([]byte(runJSONObject(t, out)), &res); uerr != nil {
		t.Fatalf("%v:\n%s\n%s", uerr, out, errOut)
	}
	if got := res.state(initflow.Repository); got != "needs-you" {
		t.Fatalf("the repository stage is %q, want needs-you: the working tree's fugaro.yaml is not the default branch's\n%s", got, out)
	}

	// The run's own account of what it ran for the checkout: its two local
	// steps, and never the cloud one.
	if d := res.stageDetail(t, initflow.Checkout); strings.Contains(d, "cloud") {
		t.Errorf("the checkout stage ran its cloud step over a fugaro.yaml the repository stage did not onboard: %q\n%s", d, out)
	}
	if n := len(r.builder.specs); n != 0 {
		t.Errorf("%d job image(s) reached Cloud Build from a fugaro.yaml the repository stage did not onboard: %+v\n%s", n, r.builder.specs, out)
	}
	// And the parity with the other fixtures still holds: the tolerated half is
	// the one the user's stuck line is about, and it moved.
	if got := claudeListVersion(t, r); got != convergeRelease {
		t.Errorf("Claude Code lists the plugin as %q after the run, want %q: the pin and the plugin are what this stage's tolerance is for\n%s", got, convergeRelease, out)
	}
}

// TestUpdateDoesNotBuildPastARepositoryStopThatIsNotAboutTheFile (#1836,
// review): the checkout's row tolerates a repository stop (pastStops,
// RunsPastStop) for its two local steps, and the repository stage has needs-you
// stops that are about no file at all — the GitHub App's ID, an unenrolled or
// declined repository, an agent's refusal. Those leave the stages that publish
// the installation (rules, policy, layer) behind them, and the checkout's cloud
// step builds the job images from what those stages publish: run past a stop
// like that it would build out of order, from a repository this run never
// onboarded, and the build it submits fails at the job's own third step —
// leaving the user a line pointing at `fugaro upgrade`, the second command the
// HARD RULE forbids.
//
// The fixture is the converge rig with the repository's recorded GitHub App ID
// removed: the repository stage's own Check is Todo (nothing about the file is
// wrong) and the stop is the ID, asked for at the apply.
//
// Mutation (run, restore): take the run's own stop back out of the cloud skip
// — a fresh resolveRepoTarget in planFor's repositoryStopped
// (init_checkout_upgrade.go), or no PastStops hand-off from the loop
// (initflow/loop.go) — and this test fails: the checkout runs its cloud step,
// submits the job image builds and fails on them.
func TestUpdateDoesNotBuildPastARepositoryStopThatIsNotAboutTheFile(t *testing.T) {
	r := newConvergeRig(t, v4Node())
	currentRelease(t)
	skillWarned.Store(false)
	t.Cleanup(func() { skillWarned.Store(false) })
	dropAppID(t, r)

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err == nil {
		t.Fatalf("the run reported success while the repository stage needs the GitHub App's ID:\n%s", out)
	}
	var res convergeJSON
	if uerr := json.Unmarshal([]byte(runJSONObject(t, out)), &res); uerr != nil {
		t.Fatalf("%v:\n%s\n%s", uerr, out, errOut)
	}
	if got := res.state(initflow.Repository); got != "needs-you" {
		t.Fatalf("the repository stage is %q, want needs-you: the run has no terminal to ask for the App ID at\n%s", got, out)
	}

	// The stop is behind the checkout, and the cloud step is the one step of
	// its stage that may not be run past it.
	if got := res.state(initflow.Checkout); got == "failed" {
		t.Errorf("the checkout stage failed over the repository stage's stop: its pin and its plugin read no repository state, and its cloud step belongs after the repository is onboarded\n%s", out)
	}
	if d := res.stageDetail(t, initflow.Checkout); strings.Contains(d, "cloud") {
		t.Errorf("the checkout stage ran its cloud step past a repository stop that is not about the file: %q\n%s", d, out)
	}
	if n := len(r.builder.specs); n != 0 {
		t.Errorf("%d job image(s) reached Cloud Build past a repository stop this run did not clear: %+v\n%s", n, r.builder.specs, out)
	}
	// The half the tolerance is for still moved: the two local steps are what
	// the user's stuck plugin line is about, whatever the repository stage
	// stopped over.
	if got := claudeListVersion(t, r); got != convergeRelease {
		t.Errorf("Claude Code lists the plugin as %q after the run, want %q: the pin and the plugin are what the tolerance is for\n%s", got, convergeRelease, out)
	}
	// And nothing this stage did sends the user to a second command: the fix a
	// failed cloud step carries names `fugaro upgrade`, because the step is
	// upgrade's own, and that is the second command the HARD RULE forbids. The
	// stage's "ran fugaro upgrade's plugin" is its account of which steps ran,
	// never a command to run.
	if i := strings.Index(out, "rerun "+selfCommand()+" upgrade"); i >= 0 {
		t.Errorf("the run sends the user to a second command for the checkout:\n%s", out[i:])
	}
}

// dropAppID removes the checkout's repository's recorded GitHub App ID from the
// local config this run reads, both copies of it (the rig's own and the project
// file the CLI reads), so the repository stage asks for it: needs-you, over no
// file at all.
func dropAppID(t *testing.T, r *convergeRig) {
	t.Helper()
	const recorded = `, github_app_id: "12345"`
	for _, path := range []string{r.cfg, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initProjectName+".yaml")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out := strings.ReplaceAll(string(data), recorded, "")
		if out == string(data) {
			t.Fatalf("the fixture's local config records no GitHub App ID to remove: %s", path)
		}
		if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestUpdatePluginLineDoesNotReadCurrentWhileTheInstallIsBehind (#1836c): the
// staleness line and the plugin stage's line are about the two halves of the
// plugin — the copy Claude Code holds and the pin in .claude/settings.json —
// and on a checkout whose pin is already this release's they used to read one
// current and the other stale on the same run: the stage said "No changes: ...
// pins the plugin to vX" while every command warned that the installed copy
// was the release behind. The pin's half is not the whole truth, and the line
// now carries the half that is behind and the step that installs it.
func TestUpdatePluginLineDoesNotReadCurrentWhileTheInstallIsBehind(t *testing.T) {
	r := newConvergeRigYAML(t, v4Node(), convergeLegacyYAML)
	currentRelease(t)
	skillWarned.Store(false)
	t.Cleanup(func() { skillWarned.Store(false) })
	// A previous run moved the pin; the install it asked for did not happen
	// (the state pluginFirstRun warns about), which the fake's claude keeps in
	// the list the checkout's plugin step reads.
	checkoutFile(t, r.settings, wiredAt("v"+convergeRelease))

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err == nil {
		t.Fatalf("the run reported success while the checkout's own fugaro.yaml is invalid:\n%s", out)
	}
	var res convergeJSON
	if uerr := json.Unmarshal([]byte(runJSONObject(t, out)), &res); uerr != nil {
		t.Fatalf("%v:\n%s\n%s", uerr, out, errOut)
	}
	if got := res.state(initflow.Plugin); got != "done" {
		t.Fatalf("the plugin stage is %q, want done: the pin is already this release's\n%s", got, out)
	}

	d := res.stageDetail(t, initflow.Plugin)
	if !strings.Contains(d, convergePrev) {
		t.Errorf("the plugin stage's line reads as current while Claude Code still has %s installed: %q\n%s", convergePrev, d, out)
	}
	if !strings.Contains(d, convergeRelease) {
		t.Errorf("the plugin stage's line does not name the version the checkout stage installs: %q\n%s", d, out)
	}

	// The other half of #1836c: the two lines agree now, and the run that the
	// line points at still did the install.
	if got := claudeListVersion(t, r); got != convergeRelease {
		t.Errorf("Claude Code lists the plugin as %q after the run, want %q\n%s", got, convergeRelease, out)
	}
	staleLineWaitsForTheRun(t, errOut)
}

// staleLineWaitsForTheRun is #1836b: the staleness line, when the run prints
// one at all, comes after the run's own account of the plugin and never before
// it. Printed first it is the door that clears the staleness sending the
// reader to itself over a step of its own that has not run yet, and it is the
// one line a run could not clear however often it was run. The fixture's fake
// claude leaves Claude Code's own record alone, so what is left to say at the
// end is still said: the point of the assertion is when.
func staleLineWaitsForTheRun(t *testing.T, errOut string) {
	t.Helper()
	line := staleWarnings(errOut)
	if line == "" {
		return
	}
	install := strings.Index(errOut, "installed "+convergeRelease)
	if install < 0 {
		t.Fatalf("the run never said it installed the plugin:\n%s", errOut)
	}
	if i := strings.Index(errOut, line); i < install {
		t.Errorf("fugaro update printed the staleness line before the run that clears it: %q\n%s", line, errOut)
	}
}

// runJSONObject is the run's own --json object out of a command's stdout. It is
// not the whole of it: the policy stage's confirm and the line recording the
// generation it wrote go to the command's stdout rather than to the run's
// writer (budget.go's confirmTyped, init_policy_migrate.go's record), so a run
// that publishes a node prints them ahead of the object. The object is the
// last document on the stream, and it is the one this asks for.
func runJSONObject(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "{\n")
	if i < 0 {
		t.Fatalf("no JSON object on the run's stdout:\n%s", out)
	}
	return out[i:]
}

// staleWarnings is every staleness line in a run's output, "" when there is
// none: the sentence a stale line carries, so a test reads the warning and not
// some other mention of the plugin.
func staleWarnings(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "warning: the installed Fugaro plugin") || strings.HasPrefix(line, "warning: the Fugaro plugin") {
			return line
		}
	}
	return ""
}

// claudeListVersion is what the fake claude answers `plugin list --json` with:
// Claude Code's own record of the installed Fugaro plugin, which is what the
// checkout stage's plugin step reads to decide whether the install is behind
// and rewrites when it installs one.
func claudeListVersion(t *testing.T, r *convergeRig) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.claude.Dir, "plugins.json"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	var list []struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	for _, p := range list {
		if p.ID == pluginwire.PluginID {
			return p.Version
		}
	}
	return ""
}
