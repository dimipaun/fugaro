package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// ourMarket is Claude Code's record of the fugaro marketplace as fugaro added
// it before this change: declared with no ref, so it follows
// dimipaun/fugaro's default branch.
const ourMarket = `[{"name":"fugaro","source":"github","repo":"dimipaun/fugaro"}]`

// ourMarketAt is that record for a marketplace declared at ref: what the
// plugin step itself writes (claudeplugin's pinned add).
func ourMarketAt(ref string) string {
	return `[{"name":"fugaro","source":"github","repo":"dimipaun/fugaro","ref":"` + ref + `"}]`
}

func userInstall(v string) string {
	return `[{"id":"fugaro@fugaro","version":"` + v + `","scope":"user","enabled":true}]`
}

var (
	argsListMarkets = []string{"plugin", "marketplace", "list", "--json"}
	argsListPlugins = []string{"plugin", "list", "--json"}
	argsAdd         = addUser("0.5.2")
	argsRemove      = []string{"plugin", "marketplace", "remove", "fugaro", "--scope", "user"}
	// argsBack is the add that puts back what argsRemove cleared: the plain
	// source releases before this one declared, which is what ourMarket holds.
	argsBack      = []string{"plugin", "marketplace", "add", "--scope", "user", "dimipaun/fugaro"}
	argsUpdMarket = []string{"plugin", "marketplace", "update", "fugaro"}
	argsInstall   = []string{"plugin", "install", "fugaro@fugaro", "--scope", "user"}
)

// refusedAdd is what the real claude answers to an add of a name it has
// already declared from a different source (2.1.283, 2026-11-09): an exit 1
// that names the only way through, which is the clear this step runs first.
const refusedAdd = "Cannot add marketplace \"fugaro\": its network source differs from the one declared for it in settings (kind, target, or a fetch-shaping field such as headers / ref / path / sparsePaths); the source must match the one declared for this name in settings (or change the declaration)."

// useFakeClaude makes f the claude the plugin step finds (convergeEnv restores
// the lookup).
func useFakeClaude(t *testing.T, f *testutil.ClaudePluginFake) {
	t.Helper()
	claudeLookPath = func(name string) (string, error) {
		if name != "claude" {
			return "", exec.ErrNotFound
		}
		return f.Bin(), nil
	}
}

// addUser is the marketplace add as the step runs it for a release: the fixed
// argv with Fugaro's own repository at that release's tag (argsAdd is the one
// the 0.5.2 fixtures use).
func addUser(v string) []string {
	return []string{"plugin", "marketplace", "add", "--scope", "user", "dimipaun/fugaro#v" + v}
}

// runPluginStep is the plugin step itself, which is the whole of what `fugaro
// upgrade --local` was about: U4 of docs/plugin-user-scope/implementation-plan.md
// retires that command, the step answers to the plugin stage now
// (init_plugin_stage.go), and the tests below drive it the way the stage does —
// the same call, the same line (printStep, which is what the checkout stage and
// `fugaro update --check` print a step with), and the same exit a failed step
// gives the run. It returns the two streams executeStdin returns so a case still
// reads as the run it replaced did.
func runPluginStep(t *testing.T, root string) (string, string, error) {
	t.Helper()
	out, errOut := &syncBuf{}, &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	loc, ok := pluginwire.Locate(root)
	if !ok {
		t.Fatalf("%s is not in a checkout, so there is no checkout for the step to run in", root)
	}
	r := pluginStep(context.Background(), &stepCtx{cmd: cmd, w: out, loc: loc})
	r.name = "plugin"
	printStep(out, r)
	if r.state != stepFailed {
		return out.String(), errOut.String(), nil
	}
	code := r.code
	if code == 0 {
		code = ExitUserError
	}
	return out.String(), errOut.String(), &ExitError{Code: code, Err: errors.New(r.reason)}
}

func callArgs(calls []testutil.ClaudeCall) [][]string {
	var out [][]string
	for _, c := range calls {
		out = append(out, c.Args)
	}
	return out
}

func TestUpgradePluginInstallsWithExactArgv(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.5.2"), argsInstall...)
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := f.Calls(t)
	want := [][]string{argsListMarkets, argsListPlugins, argsAdd, argsInstall, argsListPlugins}
	if !slices.EqualFunc(callArgs(calls), want, slices.Equal[[]string]) {
		t.Fatalf("calls %q, want %q", callArgs(calls), want)
	}
	// User-scope calls never need the checkout (claudeplugin.needsCheckout):
	// they run in a fresh empty directory, so an owner/name on the command
	// line can never collide with a local directory of that name in root.
	for _, c := range calls {
		if testutil.SamePath(c.Dir, root) {
			t.Errorf("a user-scope call ran in the checkout %s; only a project or local scope update should", root)
		}
	}
	for _, w := range []string{"using claude at " + f.Bin(), "running: " + f.Bin() + " plugin install fugaro@fugaro --scope user",
		"plugin: done: installed 0.5.2; restart running Claude Code sessions to apply it"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "claude changed") {
		t.Errorf("the settings note fired though claude changed nothing:\n%s", out)
	}
}

// TestUpgradePluginMovesAnUnpinnedMarketplaceToTheRelease: Claude Code's own
// marketplace on this machine is what the plugin is installed and updated
// from, and the step used to declare it with no ref, so it followed
// dimipaun/fugaro's default branch. On a patch release cut from release/X.Y
// main's plugin.json still carries the version main was last bumped to, so
// `fugaro update` on 0.21.1 (2026-11-09) installed 0.21.0 from it and failed a
// complete update with "the installed plugin is still 0.21.0". The step
// declares the marketplace at this release's tag before anything is installed
// from it, and it clears the old declaration first: claude refuses an add of a
// name it has already declared from a different source, and the fake refuses
// it here too until the clear has run.
func TestUpgradePluginMovesAnUnpinnedMarketplaceToTheRelease(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
	f.On(t, "fail", refusedAdd, argsAdd...)
	f.On(t, "rm", "fail."+testutil.ClaudePluginKey(argsAdd...), argsRemove...)
	// The removal takes every install of the marketplace with it (claude's own
	// "add the marketplace back and reinstall the plugin"), and what Claude Code
	// serves is decided by the marketplace's ref, so the install that follows the
	// pinned add is what leaves 0.5.2 — the way the failed update of 2026-11-09
	// got 0.21.0 from an unpinned marketplace.
	f.On(t, "plugins", "[]", argsRemove...)
	f.On(t, "plugins", userInstall("0.5.2"), argsInstall...)
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2")) // the checkout's own pin is already right
	out, _, err := runPluginStep(t, root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := [][]string{argsRemove, argsAdd, argsUpdMarket, argsInstall, argsUpdUser}
	if !slices.EqualFunc(f.Changes(t), want, slices.Equal[[]string]) {
		t.Fatalf("changes %q, want %q\n%s", f.Changes(t), want, out)
	}
	if !strings.Contains(out, "plugin: done: installed 0.5.2") {
		t.Errorf("the plugin did not converge on this release's version:\n%s", out)
	}
}

// TestUpgradePluginMovesAnInstallTheClearLeftStanding: claude takes every
// install of a marketplace it removes only when the removal leaves the name
// undeclared, and a fugaro checkout's own committed settings declare it too
// (the pin step writes them). So on those machines the clear leaves the
// machine's own install standing at the version it was installed at — an
// install of one that is already there does not move it, claude answers
// "already installed" and names update as the call that re-records the version
// — and the update at that install's own scope is what converges the machine.
// Without it the step reads the install it started with, which is the failure
// of 2026-11-09 again ("the installed plugin is still 0.21.0") on the machine
// this change is for.
func TestUpgradePluginMovesAnInstallTheClearLeftStanding(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
	f.On(t, "fail", refusedAdd, argsAdd...)
	f.On(t, "rm", "fail."+testutil.ClaudePluginKey(argsAdd...), argsRemove...)
	// The install the plan makes does not move the standing one (the fake
	// leaves the record where it is, as claude's "already installed" does), and
	// the user-scope update is what re-records its version.
	f.On(t, "plugins", userInstall("0.5.2"), argsUpdUser...)
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := [][]string{argsRemove, argsAdd, argsUpdMarket, argsInstall, argsUpdUser}
	if !slices.EqualFunc(f.Changes(t), want, slices.Equal[[]string]) {
		t.Fatalf("changes %q, want %q\n%s", f.Changes(t), want, out)
	}
	if !strings.Contains(out, "plugin: done: installed 0.5.2") {
		t.Errorf("the plugin did not converge on this release's version:\n%s", out)
	}
}

// TestUpgradePluginToleratesAnInstallTheClearTook: the other machine the moved
// plan has to cover is one whose settings leave the marketplace name
// undeclared, so the clear takes every install of it with the declaration
// ("Also uninstalled 1 plugin from this marketplace"): the update at that
// install's scope has nothing to update, which is not a failure of the step —
// the install it ran is what the machine has — and every other failure there
// stays reported by the check after the calls, because it leaves the version
// where it was.
func TestUpgradePluginToleratesAnInstallTheClearTook(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
	f.On(t, "fail", refusedAdd, argsAdd...)
	f.On(t, "rm", "fail."+testutil.ClaudePluginKey(argsAdd...), argsRemove...)
	f.On(t, "plugins", "[]", argsRemove...)
	f.On(t, "plugins", userInstall("0.5.2"), argsInstall...)
	f.On(t, "fail", `Plugin "fugaro" not found in marketplace "fugaro".`, argsUpdUser...)
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := [][]string{argsRemove, argsAdd, argsUpdMarket, argsInstall, argsUpdUser}
	if !slices.EqualFunc(f.Changes(t), want, slices.Equal[[]string]) {
		t.Fatalf("changes %q, want %q\n%s", f.Changes(t), want, out)
	}
	for _, w := range []string{"continuing without that call", "plugin: done: installed 0.5.2"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// TestUpgradePluginCarriesOnWhenTheInstallTheClearMayHaveTakenFails: the
// install on the cleared path is a call the clear may have taken the need for,
// not the update's own work. On this machine the install answers with a
// failure and the update at the same scope is what converges the machine, so a
// non-zero answer from the install must not fail an update the check after the
// calls passes. The fake fails it because a test may not lean on claude's real
// answer to an install of one that is already there ("already installed", exit
// 0) staying that answer.
func TestUpgradePluginCarriesOnWhenTheInstallTheClearMayHaveTakenFails(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
	f.On(t, "fail", refusedAdd, argsAdd...)
	f.On(t, "rm", "fail."+testutil.ClaudePluginKey(argsAdd...), argsRemove...)
	f.On(t, "fail", "claude: network down", argsInstall...)
	f.On(t, "plugins", userInstall("0.5.2"), argsUpdUser...)
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := [][]string{argsRemove, argsAdd, argsUpdMarket, argsInstall, argsUpdUser}
	if !slices.EqualFunc(f.Changes(t), want, slices.Equal[[]string]) {
		t.Fatalf("changes %q, want %q\n%s", f.Changes(t), want, out)
	}
	for _, w := range []string{"continuing without that call", "plugin: done: installed 0.5.2"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// TestUpgradePluginPutsBackWhatTheClearRemovedWhenTheCheckAfterTheCallsFails:
// the calls the clear may have taken the need for are carried on from, so the
// stop comes from the check after them instead of from a call — and that stop
// must not leave the machine without what the clear took either. Here the clear
// emptied every install, the install that follows it fails (the network, say)
// and the update at the vanished install's scope finds nothing to update, so
// the machine's own install is not there and the step fails: the declaration
// and the install go back first, best effort, as the add-back of a stopped step
// does.
func TestUpgradePluginPutsBackWhatTheClearRemovedWhenTheCheckAfterTheCallsFails(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
	f.On(t, "fail", refusedAdd, argsAdd...)
	f.On(t, "rm", "fail."+testutil.ClaudePluginKey(argsAdd...), argsRemove...)
	f.On(t, "plugins", "[]", argsRemove...)
	f.On(t, "fail", "claude: network down", argsInstall...)
	f.On(t, "fail", `Plugin "fugaro" not found in marketplace "fugaro".`, argsUpdUser...)
	f.On(t, "fail", refusedAdd, argsBack...) // the tag is declared by then: the add-back is refused
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
	want := [][]string{argsRemove, argsAdd, argsUpdMarket, argsInstall, argsUpdUser, argsBack, argsInstall}
	if !slices.EqualFunc(f.Changes(t), want, slices.Equal[[]string]) {
		t.Fatalf("changes %q, want %q\n%s", f.Changes(t), want, out)
	}
	for _, w := range []string{"plugin: failed: after the update claude lists no install of fugaro@fugaro at user scope",
		"putting back what the clear removed failed", "claude: network down"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// TestUpgradePluginPutsBackWhatTheClearRemovedWhenTheReListFails: the check
// after the calls reads the installs again, and that read can fail on its own
// — claude's record of them unreadable, say — after a clear that took the
// plugin with it. That stop is the step's, and it is a stop after the clear
// like any other: the declaration and the install go back before it is
// reported, so even a failed update leaves the machine with the plugin it had
// rather than with neither.
func TestUpgradePluginPutsBackWhatTheClearRemovedWhenTheReListFails(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
	f.On(t, "rm", "fail."+testutil.ClaudePluginKey(argsAdd...), argsRemove...)
	f.On(t, "plugins", "[]", argsRemove...)
	f.On(t, "plugins", userInstall("0.5.2"), argsInstall...)
	// The install is the last call before the read, and this makes the read
	// that follows it fail, once: the change calls the put-back then runs are
	// not the read, and come back with the plugin.
	f.On(t, "nextlistfail", "could not read the installed plugins\n", argsInstall...)
	f.On(t, "fail", refusedAdd, argsBack...) // the tag is declared by then: the add-back is refused
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
	want := [][]string{argsRemove, argsAdd, argsUpdMarket, argsInstall, argsUpdUser, argsBack, argsInstall}
	if !slices.EqualFunc(f.Changes(t), want, slices.Equal[[]string]) {
		t.Fatalf("changes %q, want %q\n%s", f.Changes(t), want, out)
	}
	for _, w := range []string{"plugin: failed: claude plugin list --json failed: exit status 1",
		"could not read the installed plugins", "putting back what the clear removed failed"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// TestUpgradePluginPutsBackWhatTheClearRemovedWhenAChangeFails: the clear runs
// before the rest of the plan, and claude's removal took the plugin with it, so
// a change call that fails after it — the marketplace update, or the install
// itself — must not leave the machine without the plugin it had, which a rerun
// would then have to find there. The declaration goes back too, best effort:
// once this release's tag is declared for the name, claude refuses it at the
// source it had, and the install from the declaration that stands is what puts
// the plugin back.
func TestUpgradePluginPutsBackWhatTheClearRemovedWhenAChangeFails(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
	f.On(t, "rm", "fail."+testutil.ClaudePluginKey(argsAdd...), argsRemove...)
	f.On(t, "plugins", "[]", argsRemove...)
	f.On(t, "fail", "network down\n", argsUpdMarket...)
	f.On(t, "fail", refusedAdd, argsBack...) // the tag is declared by then: the add-back is refused
	f.On(t, "plugins", userInstall("0.5.2"), argsInstall...)
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
	want := [][]string{argsRemove, argsAdd, argsUpdMarket, argsBack, argsInstall}
	if !slices.EqualFunc(f.Changes(t), want, slices.Equal[[]string]) {
		t.Fatalf("changes %q, want %q\n%s", f.Changes(t), want, out)
	}
	for _, w := range []string{"plugin: failed: claude plugin marketplace update fugaro failed: exit status 1",
		"putting back what the clear removed failed", "claude: network down"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// TestUpgradePluginReportsAMarketplaceItCannotDeclare: claude refuses the add, and
// the clear it refused it by is not fugaro's to make either (a source declared
// for the name in settings fugaro does not own, an organization's, say). The
// plugin cannot be had on this machine, so the step says exactly that and hands
// over the commands, and the update's other steps are not stopped for it.
func TestUpgradePluginReportsAMarketplaceItCannotDeclare(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
	f.On(t, "fail", refusedAdd, argsAdd...)
	f.On(t, "fail", "Marketplace 'fugaro' is not declared in user settings. Omit --scope to remove it from all scopes.", argsRemove...)
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !slices.EqualFunc(f.Changes(t), [][]string{argsRemove, argsAdd}, slices.Equal[[]string]) {
		t.Fatalf("changes %q\n%s", f.Changes(t), out)
	}
	for _, w := range []string{"continuing without clearing", "plugin: skipped: claude would not declare the fugaro marketplace at v0.5.2",
		"/plugin marketplace remove fugaro", "/plugin marketplace add dimipaun/fugaro#v0.5.2"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// TestUpgradePluginPutsBackTheDeclarationItsClearRemoved: the clear runs before
// the add because claude refuses an add of a name already declared from another
// source, so an add that then fails must not leave the machine without the
// marketplace and the plugin it had — and claude's own refusal of the
// declaration is the only add failure the step reports and carries on from:
// anything else (a clone that failed, a network that is down) fails the step.
func TestUpgradePluginPutsBackTheDeclarationItsClearRemoved(t *testing.T) {
	for _, tc := range []struct {
		name, said, want string
		code             int
	}{
		{"a clone that failed", "fatal: unable to access 'https://github.com/dimipaun/fugaro.git/': Could not resolve host",
			"plugin: failed: claude plugin marketplace add --scope user dimipaun/fugaro#v0.5.2 failed: exit status 1", ExitUserError},
		{"claude refusing the declaration", refusedAdd, "plugin: skipped: claude would not declare the fugaro marketplace at v0.5.2", ExitOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			convergeEnv(t, "0.5.2")
			f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
			f.On(t, "fail", tc.said, argsAdd...)
			useFakeClaude(t, f)
			root, _ := skillCheckout(t, wiredAt("v0.5.2"))
			out, _, err := runPluginStep(t, root)
			if ExitCode(err) != tc.code {
				t.Fatalf("exit %d\n%s", ExitCode(err), out)
			}
			// The clear takes the plugin with the declaration, so both go back
			// before the step reads why the add failed.
			want := [][]string{argsRemove, argsAdd, argsBack, argsInstall}
			if !slices.EqualFunc(f.Changes(t), want, slices.Equal[[]string]) {
				t.Fatalf("changes %q, want %q\n%s", f.Changes(t), want, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out)
			}
		})
	}
}

// TestUpgradePluginNeverLowersANewerMarketplaceRef: a newer fugaro pinned
// Claude Code's marketplace to a newer tag (U10: a pin is never moved down),
// so the step refreshes that declaration instead of re-declaring it at this
// release's tag, and the newer plugin that comes back is a note, not a failure.
func TestUpgradePluginNeverLowersANewerMarketplaceRef(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.6.0"), userInstall("0.6.0"))
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if err != nil || !slices.EqualFunc(f.Changes(t), [][]string{argsUpdMarket, argsUpdUser}, slices.Equal[[]string]) {
		t.Fatalf("%v, changes %q\n%s", err, f.Changes(t), out)
	}
	if !strings.Contains(out, "plugin: done: installed 0.6.0, newer than this fugaro 0.5.2") {
		t.Errorf("a newer pin was not reported as one:\n%s", out)
	}
}

func TestUpgradePluginCurrentRunsOnlyTheLists(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), userInstall("0.5.2"))
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	// The install is this binary's release already, so the step plans nothing:
	// the two lists and no call that changes anything. The line the run says
	// about the checkout ("nothing to do: <path>") is the check's own and is
	// pinned there (TestUpdateCheckReportsEveryStep).
	out, _, err := runPluginStep(t, root)
	if err != nil || len(f.Changes(t)) != 0 || !strings.Contains(out, "plugin: current: installed 0.5.2") {
		t.Fatalf("%v, changes %q\n%s", err, f.Changes(t), out)
	}
}

// TestUpgradePluginLeavesThisCheckoutsProjectInstall: a project-scope install
// is the checkout's own settings, whoever made it — a person's, or one Claude
// Code made from the pin a wired checkout carried when the folder was trusted
// — and this step no longer moves one (D949, U2). A behind project install
// beside the machine's own current install is nothing to do, and no call is
// planned in the checkout for it: what applies there is a question this step
// asks nowhere (claudeplugin.Effective, D942, which nothing reads). The
// version the line names is the machine's
// own, so this line and fugaro upgrade --check's say the same thing about the
// same machine.
func TestUpgradePluginLeavesThisCheckoutsProjectInstall(t *testing.T) {
	convergeEnv(t, "0.5.2")
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	plugins := `[{"id":"fugaro@fugaro","version":"0.5.2","scope":"user"},{"id":"fugaro@fugaro","version":"0.5.1","scope":"project","projectPath":"` + root + `"}]`
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), plugins)
	useFakeClaude(t, f)
	out, _, err := runPluginStep(t, root)
	if err != nil || len(f.Changes(t)) != 0 || !strings.Contains(out, "plugin: current: installed 0.5.2") {
		t.Fatalf("%v, changes %q\n%s", err, f.Changes(t), out)
	}
}

// TestUpgradePluginInstallsAtUserScopeBesideThisCheckoutsProjectInstall: the
// machine has no install of the plugin at all — the only one that applies to
// the checkout is the checkout's own, put there by its settings when the folder
// was trusted. That is not an install this step has left alone; it is one it
// has none of, so the user-scope install is planned even though something
// applies, which is what the step's promise and its check after the calls ask
// for (D949, U2).
func TestUpgradePluginInstallsAtUserScopeBesideThisCheckoutsProjectInstall(t *testing.T) {
	convergeEnv(t, "0.5.2")
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	project := `{"id":"fugaro@fugaro","version":"0.5.2","scope":"project","projectPath":"` + root + `"}`
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), "["+project+"]")
	f.On(t, "plugins", "["+project+`,{"id":"fugaro@fugaro","version":"0.5.2","scope":"user","enabled":true}]`, argsInstall...)
	useFakeClaude(t, f)
	out, _, err := runPluginStep(t, root)
	if err != nil || !slices.EqualFunc(f.Changes(t), [][]string{argsUpdMarket, argsInstall}, slices.Equal[[]string]) {
		t.Fatalf("%v, changes %q\n%s", err, f.Changes(t), out)
	}
	if !strings.Contains(out, "plugin: done: installed 0.5.2; restart running Claude Code sessions to apply it") {
		t.Errorf("the machine's own install was not reported as what the step made:\n%s", out)
	}
}

// TestUpgradePluginFailsWhenTheCallsLeaveNoUserScopeInstall: the check after
// the calls asks about the machine's own install (claudeplugin.UserVersion,
// D949, U2), and this is the machine that tells the two questions apart: the
// checkout's own project install is at this release and the install call the
// step planned left it standing alone. Read through Effective (D942) — what
// applies to the checkout — the step would report "done" having installed
// nothing on the machine, which is the install every reader of the report can
// act on.
func TestUpgradePluginFailsWhenTheCallsLeaveNoUserScopeInstall(t *testing.T) {
	convergeEnv(t, "0.5.2")
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	project := `{"id":"fugaro@fugaro","version":"0.5.2","scope":"project","projectPath":"` + root + `"}`
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), "["+project+"]")
	useFakeClaude(t, f) // the install call leaves the checkout's own install standing
	out, _, err := runPluginStep(t, root)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, want the step to fail\n%s", ExitCode(err), out)
	}
	if !strings.Contains(out, "plugin: failed: after the update claude lists no install of fugaro@fugaro at user scope") {
		t.Fatalf("output lacks the failure naming the user-scope install:\n%s", out)
	}
}

// TestUpgradeCheckLeavesThisCheckoutsProjectInstall: the same configuration on
// the check side, which reads Claude Code's own record and runs no claude. The
// version this line answers with is the machine's own install — the one the
// step maintains (pluginwire.Report.UserVersion) — so a behind project install
// beside it is not "stale": the update can never move that install, and a check
// that called it stale would leave every run naming a state it cannot clear
// (D949, U2).
func TestUpgradeCheckLeavesThisCheckoutsProjectInstall(t *testing.T) {
	convergeEnv(t, "0.5.2")
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	record := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	plugins := `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.2"},{"scope":"project","version":"0.5.1","projectPath":"` + root + `"}]}}`
	if err := os.WriteFile(record, []byte(plugins), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, _ := executeStdin(t, "", "update", "--check", root)
	if !strings.Contains(out, "plugin: current: installed 0.5.2") || strings.Contains(out, "plugin: stale") {
		t.Fatalf("%s", out)
	}
}

// TestUpgradeCheckSeesNoUserScopeInstallBesideThisCheckoutsProjectInstall: the
// same record with the machine's own install missing, which is the state the
// two questions differ in. The checkout's project-scope install is at this
// release and the machine has none at user scope: read as current, the check
// would promise a machine the write path still installs the plugin on — the
// step plans that install, and fails after the calls when it did not happen
// (TestUpgradePluginInstallsAtUserScopeBesideThisCheckoutsProjectInstall, from
// the write side) — so the check answers about the machine's install
// (pluginwire's Report.UserInstall, D949, U2) and says the plugin is not there.
func TestUpgradeCheckSeesNoUserScopeInstallBesideThisCheckoutsProjectInstall(t *testing.T) {
	convergeEnv(t, "0.5.2")
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	record := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	plugins := `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"project","version":"0.5.2","projectPath":"` + root + `"}]}}`
	if err := os.WriteFile(record, []byte(plugins), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, _ := executeStdin(t, "", "update", "--check", root)
	if strings.Contains(out, "plugin: current") {
		t.Fatalf("the checkout's own install was read as the machine's:\n%s", out)
	}
	if !strings.Contains(out, "plugin: skipped: not installed on this machine (fugaro update-skills installs it through the claude CLI)") {
		t.Fatalf("the check does not say the machine has no install:\n%s", out)
	}
}

// TestUpgradeCheckReadsTheMachineOnACheckoutWithNoSettings: the same checkout
// with no .claude/settings.json at all, which is what every checkout looks like
// now that nothing writes a pin (D949, U2). The machine's own install is still
// the step's subject, so a plugin a release behind is stale here and the run
// exits 1 — read off the checkout's wiring instead, this checkout would come
// out "cannot tell", the exit would be 0, and the upgrade skill would never run
// the install it exists to run.
func TestUpgradeCheckReadsTheMachineOnACheckoutWithNoSettings(t *testing.T) {
	convergeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, "")
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Fatalf("the fixture is wired: %v", err)
	}
	record := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	plugins := `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.1"}]}}`
	if err := os.WriteFile(record, []byte(plugins), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "update", "--check", root)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, want 1 for a plugin a release behind\n%s", ExitCode(err), out)
	}
	if !strings.Contains(out, "plugin: stale: installed 0.5.1, this fugaro is 0.5.2") {
		t.Fatalf("the check does not read the machine's own install off a checkout with no settings:\n%s", out)
	}
}

func TestUpgradePluginNeverReplacesAnotherMarketplace(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, `[{"name":"fugaro","source":"github","repo":"someone/else"}]`, "[]")
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if ExitCode(err) != ExitUserError || len(f.Changes(t)) != 0 || !strings.Contains(out, "never replaces it") {
		t.Fatalf("exit %d, changes %q\n%s", ExitCode(err), f.Changes(t), out)
	}
}

// TestUpgradePluginFailureFailsTheStep: a call claude refuses fails the step,
// and the failure is one line naming the call and what claude said about it.
// The step is the plugin stage's first write, so a failed one stops the run
// there (initflow's own rule for any stage); what this case is about is the
// line and the exit, and the per-checkout summary it used to read was the
// retired command's (U4).
func TestUpgradePluginFailureFailsTheStep(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), "[]")
	f.On(t, "fail", "network down\n", argsInstall...)
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
	for _, w := range []string{"plugin: failed: claude plugin install fugaro@fugaro --scope user failed: exit status 1", "claude: network down"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func TestUpgradePluginWithoutClaude(t *testing.T) {
	convergeEnv(t, "0.5.2")
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if err != nil || !strings.Contains(out, "plugin: skipped: claude is not on PATH") || !strings.Contains(out, "/plugin install fugaro@fugaro") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUpgradeCheckRunsNoClaude(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), userInstall("0.5.1"))
	useFakeClaude(t, f)
	record := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte(`{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.1"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := executeStdin(t, "", "update", "--check", root)
	if ExitCode(err) != ExitUserError || len(f.Calls(t)) != 0 || !strings.Contains(out, "plugin: stale: installed 0.5.1, this fugaro is 0.5.2") {
		t.Fatalf("exit %d, calls %+v\n%s", ExitCode(err), f.Calls(t), out)
	}
}

func TestUpgradePluginVersionAfterTheCalls(t *testing.T) {
	for _, tc := range []struct {
		after, want string
		code        int
	}{
		{"0.6.0", "installed 0.6.0, newer than this fugaro 0.5.2", ExitOK},
		{"0.5.1", "after the update the installed plugin is still 0.5.1", ExitUserError},
	} {
		t.Run(tc.after, func(t *testing.T) {
			convergeEnv(t, "0.5.2")
			f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), "[]")
			f.On(t, "plugins", userInstall(tc.after), argsInstall...)
			useFakeClaude(t, f)
			root, _ := skillCheckout(t, wiredAt("v0.5.2"))
			out, _, err := runPluginStep(t, root)
			if ExitCode(err) != tc.code || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d\n%s", ExitCode(err), out)
			}
		})
	}
}

func TestUpgradePluginListsThatFailAreFailedSteps(t *testing.T) {
	const garbage = "not json"
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) *testutil.ClaudePluginFake
	}{
		{"markets", func(t *testing.T) *testutil.ClaudePluginFake {
			return testutil.NewClaudePluginFake(t, garbage, "[]")
		}},
		{"installed", func(t *testing.T) *testutil.ClaudePluginFake {
			return testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), garbage)
		}},
		{"relist", func(t *testing.T) *testutil.ClaudePluginFake {
			f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), "[]")
			f.On(t, "plugins", garbage, argsInstall...)
			return f
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			convergeEnv(t, "0.5.2")
			f := tc.setup(t)
			useFakeClaude(t, f)
			root, _ := skillCheckout(t, wiredAt("v0.5.2"))
			out, _, err := runPluginStep(t, root)
			if ExitCode(err) != ExitUserError || !strings.Contains(out, "plugin: failed:") {
				t.Fatalf("exit %d\n%s", ExitCode(err), out)
			}
		})
	}
}

// TestUpgradePluginNothingAppliesAfterTheUpdate: a machine the install left
// without the plugin is a failed step, and the sentence says which install it
// read: the machine's own, at user scope (a checkout's own is not this step's
// to move any more, D949, U2).
func TestUpgradePluginNothingAppliesAfterTheUpdate(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), "[]")
	useFakeClaude(t, f) // the install call leaves the list empty
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := runPluginStep(t, root)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "plugin: failed: after the update claude lists no install of fugaro@fugaro at user scope") {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
}

// TestUpgradeCheckPluginNewerThanFugaro: a plugin newer than the binary is a
// note, not stale (design: U3), so --check exits 0 and names brew upgrade.
func TestUpgradeCheckPluginNewerThanFugaro(t *testing.T) {
	convergeEnv(t, "0.5.2")
	record := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte(`{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.6.0"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := executeStdin(t, "", "update", "--check", root)
	if !strings.Contains(out, "plugin: current: installed 0.6.0, newer than this fugaro 0.5.2 (brew upgrade dimipaun/tap/fugaro)") || strings.Contains(out, "plugin: stale") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestUpgradePluginForkRepoNamesReachNeitherClaudeNorTheOutput: the repo in a
// leftover pin's committed settings.json is not read any more (D949, U2) — the
// marketplace this step declares is Fugaro's own — so an invalid one never
// reaches claude and never a claude call's arguments. --allow-fork, the flag
// that moved a fork's ref to this release's tag, is gone with the write
// (TestUpgradeHasNoAllowFork).
func TestUpgradePluginForkRepoNamesReachNeitherClaudeNorTheOutput(t *testing.T) {
	convergeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.2"))
	useFakeClaude(t, f)
	evil := `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"ev\u001b[31mil/x","ref":"v0.5.2"}}},"enabledPlugins":{"fugaro@fugaro":true}}`
	root, _ := skillCheckout(t, evil)
	out, _, err := runPluginStep(t, root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("a raw escape reached the output: %q", out)
	}
	if strings.Contains(out, "il/x") {
		t.Errorf("the repo in the checkout's settings reached the output:\n%s", out)
	}
	want := [][]string{argsRemove, argsAdd, argsUpdMarket, argsInstall}
	if !slices.EqualFunc(f.Changes(t), want, slices.Equal[[]string]) {
		t.Fatalf("changes %q, want %q\n%s", f.Changes(t), want, out)
	}
}
