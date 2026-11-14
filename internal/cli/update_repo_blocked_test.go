package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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
	r := newConvergeRigYAML(t, v6Node(), convergeLegacyYAML)
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

	// The owner's own terminal, which is what confirms the steps the run asks
	// about now the flag that stood in for it is gone (T11).
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("the run reported success while the checkout's own fugaro.yaml is invalid:\n%s", out)
	}
	account := reportLines(out + newestLog(t, "update"))

	// The checkout's file is still the user's to fix, and the run leaves them
	// the command that says how.
	if l := stepLineOf(account, initflow.Repository); !strings.HasPrefix(l, markLeft) {
		t.Errorf("the repository stage's line is %q, want a step left for the user: the invalid file is the user's to fix\n%s", l, out)
	}
	if !strings.Contains(out, "fugaro validate") {
		t.Errorf("the run does not leave the invalid fugaro.yaml for the user:\n%s", out)
	}

	// The checkout's own step ran: what reads the repository of this stage is
	// its cloud step, and the stage says in its own line that it dropped it
	// rather than waiting behind the stop (pastStops). The plugin install is
	// the plugin stage's, ahead of the repository stage, and it is what the
	// user's stuck line is about.
	if l := stepLineOf(account, initflow.Checkout); l == "" {
		t.Errorf("the checkout stage has no line of its own: it is waiting behind the repository stage, whose stop its own step runs past\n%s", out)
	}
	// The stages that publish the installation are not reached, and that is
	// the deliberate other half: a run with a stage left for the user does not
	// write what admits its runs.
	for _, name := range []string{initflow.Rules, initflow.Policy, initflow.Layer} {
		if l := stepLineOf(account, name); l != "" {
			t.Errorf("the %s stage acted behind a stage left for the user: it publishes installation state, and a run that is not converged does not publish what admits its runs\n%q\n%s", name, l, out)
		}
	}
	if got := out + newestLog(t, "update"); !strings.Contains(got, "wait for repository") {
		t.Errorf("the run does not say the stages behind the repository stage wait for it:\n%s", out)
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
// (init_checkout_upgrade.go) and this test fails — the checkout stage's own row
// no longer says the cloud step was skipped, and the change that edit asked for
// reaches Cloud Build.
func TestUpdateDoesNotBuildFromACheckoutFileTheRunDidNotOnboard(t *testing.T) {
	r := newConvergeRigYAML(t, v6Node(), refreshYAML)
	currentRelease(t)
	skillWarned.Store(false)
	t.Cleanup(func() { skillWarned.Store(false) })
	// One unmerged edit to the checkout's file: valid, this project's, and not
	// the default branch's, so the repository stage stops over it.
	checkoutFile(t, filepath.Join(r.root, "fugaro.yaml"), strings.Replace(refreshYAML, "build: sh build.sh", "build: sh build2.sh", 1))

	fakeTerminal(t)
	out, _, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("the run reported success while the checkout's fugaro.yaml is not the default branch's:\n%s", out)
	}
	rows := stageRows(out + newestLog(t, "update"))
	if got := rows[initflow.Repository].word; got != "left for you" {
		t.Fatalf("the repository stage is %q, want left for you: the working tree's fugaro.yaml is not the default branch's\n%s", got, out)
	}

	// The run's own account of what it ran for the checkout: its one step, the
	// cloud build, dropped over a fugaro.yaml the repository stage did not
	// onboard — its job images are that repository's, and a build from a config
	// no cloud run of the project reads is the half the tolerance is not for.
	if row := rows[initflow.Checkout].line; !strings.Contains(row, "skipped here: cloud") {
		t.Errorf("the checkout stage's line is %q, want the cloud step dropped over a fugaro.yaml the repository stage did not onboard\n%s", row, out)
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
// RunsPastStop), and the repository stage has stops that are about no file at
// all — the GitHub App's ID, an unenrolled or declined repository, an agent's
// refusal. Those stops leave the stages that publish the installation (rules,
// policy, layer) behind them, and the checkout's cloud step builds the job
// images from the repository that stage onboards: run past a stop like that it
// would build out of order, from a repository this run never onboarded, and the
// build it submits fails at the job's own third step — leaving the user a line
// the HARD RULE forbids: a second command beside the one command the whole
// upgrade is. So a stop that is not the user's is not one the checkout runs its
// one step past, however the stop is about nothing on disk.
//
// The fixture is the converge rig with the repository's recorded GitHub App ID
// removed: nothing about the file is wrong, so the repository stage's own Check
// is Todo, and the stop is the ID, asked for at the apply.
//
// The run has the person's own terminal, because a step nothing else confirms
// now the flag that stood in for the person is gone (T11) — so the repository
// stage asks for the ID and the empty input ends the ask: the stage fails over
// it, and a failure is a stop nothing after it runs past. What the test pins is
// the same fact the needs-you variant would: the checkout's cloud step never
// runs over a repository this run did not onboard, so no job image reaches
// Cloud Build.
//
// Mutation (run, restore): drop the yours check in the loop's blocker
// (initflow/loop.go) or answer RunsPastStop yes for a stop that is not the
// user's, and this test fails: the checkout runs its cloud step behind the
// failure, submits the job image builds and fails on them.
func TestUpdateDoesNotBuildPastARepositoryStopThatIsNotAboutTheFile(t *testing.T) {
	r := newConvergeRig(t, v6Node())
	currentRelease(t)
	skillWarned.Store(false)
	t.Cleanup(func() { skillWarned.Store(false) })
	dropAppID(t, r)

	fakeTerminal(t)
	out, _, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("the run reported success while the repository stage needs the GitHub App's ID:\n%s", out)
	}
	account := reportLines(out + newestLog(t, "update"))

	// The stop: the repository stage could not clear the App ID at this terminal
	// and failed over it, and a failure is not the user's to clear later.
	if l := stepLineOf(account, initflow.Repository); !strings.HasPrefix(l, markFailed) || !strings.Contains(l, "GitHub App ID") {
		t.Fatalf("the repository stage's line is %q, want a failure over the missing GitHub App ID\n%s", l, out)
	}

	// The checkout has no line of its own: it waits behind the stop, and the
	// cloud step it would run is the one the stop is about.
	if l := stepLineOf(account, initflow.Checkout); l != "" {
		t.Errorf("the checkout stage acted behind a stop this run did not clear: %q\n%s", l, out)
	}
	if got := out + newestLog(t, "update"); !strings.Contains(got, "wait for repository") {
		t.Errorf("the run does not say the stages behind the repository stage wait for it:\n%s", out)
	}
	if n := len(r.builder.specs); n != 0 {
		t.Errorf("%d job image(s) reached Cloud Build past a repository stop this run did not clear: %+v\n%s", n, r.builder.specs, out)
	}
	// The plugin stage is ahead of the repository stage and is this machine's
	// own, so the user's stuck plugin line is cleared whatever the repository
	// stage stopped over.
	if got := claudeListVersion(t, r); got != convergeRelease {
		t.Errorf("Claude Code lists the plugin as %q after the run, want %q: the plugin stage's own install is ahead of the repository stage\n%s", got, convergeRelease, out)
	}
	// And nothing this stage did sends the user to a second command: the fix a
	// failed cloud step carries names the one door, never a command beside it,
	// which is what the HARD RULE forbids.
	if i := strings.Index(out, "fugaro upgrade"); i >= 0 {
		t.Errorf("the run sends the user to a second command for the checkout:\n%s", out[i:])
	}
}

// dropAppID removes the checkout's repository's recorded GitHub App ID from the
// local config this run reads, both copies of it (the rig's own and the project
// file the CLI reads), so the repository stage has to ask for it at the apply
// rather than read it: a stop over no file at all.
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
// was the release behind. The pin's half is not the whole truth, the line
// carries the half that is behind, and since U1 of
// docs/plugin-user-scope/implementation-plan.md the stage that says it is the
// one that installs it — the step the line used to point at was the checkout
// stage's and is this stage's own.
func TestUpdatePluginLineDoesNotReadCurrentWhileTheInstallIsBehind(t *testing.T) {
	r := newConvergeRigYAML(t, v6Node(), convergeLegacyYAML)
	currentRelease(t)
	skillWarned.Store(false)
	t.Cleanup(func() { skillWarned.Store(false) })
	// A previous run moved the pin; the install it asked for did not happen
	// (the state pluginFirstRun warns about), which the fake's claude keeps in
	// the list the plugin stage's step reads.
	checkoutFile(t, r.settings, wiredAt("v"+convergeRelease))

	// The stage's own check is the line #1836c is about — the run's plan line
	// and `fugaro update --plan-only` print it — and it may not read as current
	// while Claude Code's own copy is the release behind: it has both versions
	// in it, the copy Claude Code holds and the one this stage installs. Read
	// before the run, which is the state the line is about (the install this
	// stage makes is what the run then does).
	e := rigEngine(t, r.initRig, &initOptions{door: updateDoor.name})
	st, ok := stageNamed(initStages(e), initflow.Plugin)
	if !ok {
		t.Fatalf("fugaro update registers no %s stage: %v", initflow.Plugin, namesOf(initStages(e)))
	}
	status, cerr := st.Check(t.Context())
	if cerr != nil {
		t.Fatal(cerr)
	}
	if status.State != initflow.Todo || !strings.Contains(status.Detail, convergePrev) || !strings.Contains(status.Detail, convergeRelease) {
		t.Errorf("the plugin stage's check reads %s %q, want the copy Claude Code holds (%s) and the one this stage installs (%s) in it", status.State, status.Detail, convergePrev, convergeRelease)
	}

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("the run reported success while the checkout's own fugaro.yaml is invalid:\n%s", out)
	}
	account := reportLines(out + newestLog(t, "update"))
	if l := stepLineOf(account, initflow.Plugin); !strings.HasPrefix(l, markDone) {
		t.Fatalf("the plugin stage's step is %q, want the stage that acts: the pin is already this release's and Claude Code's own copy is the release behind\n%s", l, out)
	}
	// The line that names what it installed is the stage's own step, which the
	// run reports beside its rows: --json carried it as the stage's detail, and
	// a run that reaches the stages is the person's own now (T11), so it is
	// read off the account the run prints and logs.
	if detail := out + errOut + newestLog(t, "update"); !strings.Contains(detail, "installed "+convergeRelease) {
		t.Errorf("the run's account does not name the install of %s that #1836c's line is about:\n%s", convergeRelease, out)
	}

	// The other half of #1836c: the line and the run agree — the run is the step
	// the line names, and it did the install.
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

// stageRow is one stage's row in the run's plan view: the word its state
// prints and the row whole, stage name and reason with it.
type stageRow struct{ word, line string }

// stageRowRE matches one plan-view row: the state's mark, the word the converge
// gives that state, the stage's name column and the reason after it. It is
// anchored on the word and reads the name column, not a plain substring: the
// name is padded by %-16s and a stage's reason names other stages ("project
// aurora" is one), so a test asking which stages a run reached must not read a
// reason as a stage. The words are initflow's stateLabel, longest first.
var stageRowRE = regexp.MustCompile(`^  \S (done|changed|will do|waiting for billing|billing pending|waiting|left for you|skipped|failed) +(\S+)(?: +(.*))?$`)

// stageRows is the run's own account of its stages, keyed by the stage's name:
// the plan-view rows, which a run that is not --plan-only keeps in its log and
// a --plan-only run shows on the screen (loop.view). It is where a test reads a
// stage's state now that no flag confirms a step: --json confirms none (T11,
// docs/no-prompt-policy), so a run that reaches the stages at all is the
// person's own, and the account to read beside it is this one.
func stageRows(all string) map[string]stageRow {
	rows := map[string]stageRow{}
	for _, l := range reportLines(all) {
		m := stageRowRE.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		rows[m[2]] = stageRow{word: m[1], line: strings.TrimSpace(l)}
	}
	return rows
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
