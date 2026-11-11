package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// extendState puts the registry, its build latch and the launch declarer back
// to their released values, before and after a test that moves them.
func extendState(t *testing.T) {
	t.Helper()
	resetExtend()
	t.Cleanup(resetExtend)
}

// commandPaths is every command path in a tree, sorted.
func commandPaths(root *cobra.Command) []string {
	var paths []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		paths = append(paths, c.CommandPath())
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	sort.Strings(paths)
	return paths
}

// builtTreePaths is the tree the CLI builds with nothing registered: the
// released tree plus a stub for every paid name the core no longer holds the
// code for. `coordinator` is that name today -- its library and command moved
// to ee/ in E2.1b -- and a registration is what replaces the stub, so a test
// that compares a built tree against a baseline asks this rather than spelling
// the stub out, which is how the baseline went stale when the move landed.
func builtTreePaths() []string {
	paths := coreRootPaths()
	for _, name := range paidCommandNames {
		if !coreSurface().rootCommands[name] {
			paths = append(paths, "fugaro "+name)
		}
	}
	sort.Strings(paths)
	return paths
}

func findCmd(t *testing.T, root *cobra.Command, name string) *cobra.Command {
	t.Helper()
	c, _, err := root.Find([]string{name})
	if err != nil || c == nil {
		t.Fatalf("no %s command in the tree: %v", name, err)
	}
	return c
}

// registeredCmd is the command a test registers: it prints a word no core
// command prints, so a test can tell it apart from one it might have replaced.
func registeredCmd(name, word string) func() *cobra.Command {
	return func() *cobra.Command {
		return &cobra.Command{
			Use:   name,
			Short: "a command this build registered",
			RunE: func(cmd *cobra.Command, _ []string) error {
				fmt.Fprintln(cmd.OutOrStdout(), word)
				return nil
			},
		}
	}
}

func TestRegisteredCommandJoinsTheRoot(t *testing.T) {
	extendState(t)
	if err := RegisterCommand("fugaro-test-paid", registeredCmd("fugaro-test-paid", "registered")); err != nil {
		t.Fatalf("registering a command no core command holds: %v", err)
	}
	root := NewRootCmd()
	if !slices.Contains(commandPaths(root), "fugaro fugaro-test-paid") {
		t.Fatalf("the registered command is not in the tree: %v", commandPaths(root))
	}
	out, _, err := execute(t, "fugaro-test-paid")
	if err != nil {
		t.Fatalf("running the registered command: %v", err)
	}
	if out != "registered\n" {
		t.Errorf("the registered command printed %q, want %q", out, "registered\n")
	}
}

// TestRegisteredCommandCannotShadowACoreCommand is the registry's whole point:
// a registration adds a command and never takes one over, so a name the
// released tree holds is refused — and the tree the CLI builds still answers to
// the released command, not to the registered one — whichever of the two names
// the registration writes it under.
func TestRegisteredCommandCannotShadowACoreCommand(t *testing.T) {
	extendState(t)
	want := findCmd(t, newCoreRoot(), "run").Short
	// The tree the CLI builds with nothing registered: what a refused
	// registration must leave exactly as it found it. Building the tree here
	// would seal the registry and the refusals below would be refusals for the
	// wrong reason.
	before := builtTreePaths()

	core := coreSurface()
	for _, name := range []string{"run", "account", "help", "completion", "__complete", "__completeNoDesc"} {
		if !core.rootCommands[name] {
			t.Errorf("the released tree does not answer to %s at the root: a registration under that name would take it over", name)
		}
	}

	refused := []struct{ what, key, use string }{
		{"a key the released tree holds", "run", "run"},
		{"a command named after a core command", "fugaro-test-paid", "run"},
		{"an alias the released tree holds", "account", "account"},
		{"cobra's own help command", "help", "help"},
		{"cobra's own completion command", "completion", "completion"},
		{"the shell's completion entry point", "__complete", "__complete"},
		{"the shell's completion entry point without descriptions", "__completeNoDesc", "__completeNoDesc"},
	}
	for _, tc := range refused {
		err := RegisterCommand(tc.key, func() *cobra.Command {
			return &cobra.Command{Use: tc.use, Short: "the registered impostor"}
		})
		if err == nil {
			t.Fatalf("%s (%s) was accepted: a paid command could take the released run over", tc.what, tc.key)
		}
		if !strings.Contains(err.Error(), tc.use) {
			t.Errorf("%s: the refusal %q does not name the command it refused to replace", tc.what, err)
		}
	}

	got := findCmd(t, NewRootCmd(), "run").Short
	if got != want {
		t.Errorf("the tree's run command is %q, want the released one (%q): a registration replaced a core command", got, want)
	}
	if after := commandPaths(NewRootCmd()); !slices.Equal(after, before) {
		t.Errorf("a refused registration reached the tree:\n%v\n%v", after, before)
	}
	if len(registrations) != 0 {
		t.Errorf("a refused registration was kept: %v", registrations)
	}
}

// TestRegistrationTakesNoCoreNameAliasOrRootFlag is the registry's add-never-
// replace rule over the whole surface a name can reach (design
// ee-boundary-moves.md §5.1, docs/subscriptions/implementation-plan.md S1.2):
// a registration may bring its own subcommands and its own local flags, but it
// may not answer to a name or alias the released tree holds at the root, and it
// may not re-declare a persistent flag the root carries — each would take a
// name or a flag away from the released tree rather than add one. A subcommand
// under the registered command's own path, and a flag the root does not carry,
// are its business.
func TestRegistrationTakesNoCoreNameAliasOrRootFlag(t *testing.T) {
	extendState(t)
	core := coreSurface()
	want := findCmd(t, newCoreRoot(), "run").Short

	// A subcommand of a registered command sits under the registered command's
	// own path, where the same word is a different command: the command the
	// owner moves carries a report subcommand beside the released report, and
	// neither it nor the released run is replaced.
	nested := func() *cobra.Command {
		c := &cobra.Command{Use: "fugaro-test-paid"}
		c.AddCommand(registeredCmd("run", "nested")())
		return c
	}
	if err := RegisterCommand("fugaro-test-paid", nested); err != nil {
		t.Fatalf("a registered command carrying a run subcommand was refused: %v", err)
	}

	// A flag a core command happens to use is not the root's: a registered
	// command may declare one of its own, and takes nothing away from anyone.
	if core.rootFlags["config"] {
		t.Fatal("the root now carries --config itself: this test's core-command flag name is no longer the narrow case it pins")
	}
	ownConfig := func() *cobra.Command {
		c := &cobra.Command{Use: "fugaro-test-own"}
		c.PersistentFlags().String("config", "", "the registered command's own flag")
		return c
	}
	if err := RegisterCommand("fugaro-test-own", ownConfig); err != nil {
		t.Errorf("a registered command's own --config was refused: %v", err)
	}

	// A persistent flag the ROOT carries is inherited by everything under the
	// command it sits on, so re-declaring one takes it away from that subtree.
	if !core.rootFlags["help"] {
		t.Fatal("the root carries no --help flag: this test's root flag name has moved")
	}
	withHelp := func() *cobra.Command {
		c := &cobra.Command{Use: "fugaro-test-flag"}
		c.PersistentFlags().Bool("help", false, "takes the root's help flag over")
		return c
	}
	err := RegisterCommand("fugaro-test-flag", withHelp)
	if err == nil {
		t.Fatal("a registered command declaring --help as a persistent flag was accepted: it takes the root's flag away from everything under it")
	} else if !strings.Contains(err.Error(), "help") {
		t.Errorf("the refusal %q does not name the flag it refused", err)
	}

	// An alias is a name the tree answers to as surely as a command name is,
	// and cobra takes the first child that matches it.
	withAlias := func() *cobra.Command {
		return &cobra.Command{Use: "fugaro-test-alias", Aliases: []string{"run"}, Short: "the registered impostor"}
	}
	err = RegisterCommand("fugaro-test-alias", withAlias)
	if err == nil {
		t.Fatal("a registered command answering to run as an alias was accepted: cobra would find it before the released run")
	} else if !strings.Contains(err.Error(), "run") {
		t.Errorf("the refusal %q does not name the alias it refused", err)
	}

	root := NewRootCmd()
	if got := findCmd(t, root, "run").Short; got != want {
		t.Errorf("the tree's run command is %q, want the released one (%q): the registered subcommand replaced a core command", got, want)
	}
	out, _, err := execute(t, "fugaro-test-paid", "run")
	if err != nil || out != "nested\n" {
		t.Errorf("the registered nested command = %q, %v; want its own path to answer to it", out, err)
	}
}

func TestLateOrDuplicateRegistrationIsRefused(t *testing.T) {
	extendState(t)
	if err := RegisterCommand("fugaro-test-paid", registeredCmd("fugaro-test-paid", "registered")); err != nil {
		t.Fatal(err)
	}
	err := RegisterCommand("fugaro-test-paid", registeredCmd("fugaro-test-paid", "registered"))
	if err == nil {
		t.Fatal("a second registration of the same name was accepted")
	} else if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("the duplicate's refusal = %q, want it named as a duplicate", err)
	}

	NewRootCmd()
	err = RegisterCommand("fugaro-test-paid-late", registeredCmd("fugaro-test-paid-late", "registered"))
	if err == nil {
		t.Fatal("a registration after the tree was built was accepted: it would never reach a tree")
	} else if !strings.Contains(err.Error(), "already built") {
		t.Errorf("the late registration's refusal = %q, want it named as late", err)
	}
}

// TestRegistrationTakesNoEarlierRegistrationsName: the second half of the
// add-never-replace rule. cobra answers to a child by name or alias in the
// order commands were added, so a registration that reuses a name an earlier
// registration brought would be found after that command rather than instead
// of it — an addition under a name the tree already answers to (design §5.1).
// The collision is the same one whichever side of the second registration the
// reused name sits on, and it is the accepted registration's aliases that make
// the name half reachable at all: a repeated KEY is the pre-existing duplicate
// check (TestLateOrDuplicateRegistrationIsRefused), and it is a key that
// matches an earlier registration's alias, rather than its own name, that this
// rule alone refuses.
//
// A refused registration reaches neither the registry nor the tree: the tree
// holds the one accepted command under its own name, and nothing answers to
// the names the refusals tried to reuse.
func TestRegistrationTakesNoEarlierRegistrationsName(t *testing.T) {
	extendState(t)
	if err := RegisterCommand("fugaro-test-paid", func() *cobra.Command {
		return &cobra.Command{Use: "fugaro-test-paid", Aliases: []string{"fugaro-test-alias"}}
	}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		what, key, use, want string
		alias                []string
	}{
		{what: "a name an earlier registration holds as an alias", key: "fugaro-test-alias",
			use: "fugaro-test-alias", want: "an earlier registration already answers to that name"},
		{what: "an alias an earlier registration holds as a name", key: "fugaro-test-other",
			use: "fugaro-test-other", alias: []string{"fugaro-test-paid"}, want: "an earlier registration already holds"},
		{what: "an alias an earlier registration holds as an alias", key: "fugaro-test-third",
			use: "fugaro-test-third", alias: []string{"fugaro-test-alias"}, want: "an earlier registration already holds"},
	} {
		err := RegisterCommand(tc.key, func() *cobra.Command {
			return &cobra.Command{Use: tc.use, Aliases: tc.alias}
		})
		if err == nil {
			t.Fatalf("%s (%s) was accepted: cobra would find the earlier command first", tc.what, tc.key)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: the refusal %q does not name the collision (%q)", tc.what, err, tc.want)
		}
	}
	if len(registrations) != 1 {
		t.Errorf("a refused registration was kept: %v", registrations)
	}
	if !registeredNames["fugaro-test-alias"] {
		t.Error("the first registration's alias was not recorded as a name the tree answers to")
	}

	root := NewRootCmd()
	want := append(builtTreePaths(), "fugaro fugaro-test-paid")
	sort.Strings(want)
	if got := commandPaths(root); !slices.Equal(got, want) {
		t.Errorf("a refused registration reached the tree:\n%v\nwant\n%v", got, want)
	}
	for _, name := range []string{"fugaro-test-alias", "fugaro-test-other", "fugaro-test-third"} {
		if c, _, _ := root.Find([]string{name}); c != nil && c.Name() == name {
			t.Errorf("the refused name %s answers in the tree", name)
		}
	}
}

// TestNoRegistrationLeavesTheRootUnchanged: a build that registers nothing
// builds the released tree plus a stub for every paid name the core no longer
// holds the code for — `coordinator`, whose code moved to ee/ — and nothing
// else. A registration is what replaces a stub; without one the name is still
// an answer the CLI gives (D522), not an absence.
func TestNoRegistrationLeavesTheRootUnchanged(t *testing.T) {
	extendState(t)
	got, want := commandPaths(NewRootCmd()), builtTreePaths()
	if len(got) != len(want) {
		t.Fatalf("the built tree holds %d commands, the released one %d:\n%v\n%v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("the built tree's commands differ from the released ones at %d: %q, want %q", i, got[i], want[i])
		}
	}
}

// TestLaunchWithoutADeclarerDeclaresNothing: an untagged build registers no
// declarer, and the launch path answers nil without opening a database at all
// (D535). The nil environment is what holds that: a declarer that ran would
// dereference it.
func TestLaunchWithoutADeclarerDeclaresNothing(t *testing.T) {
	extendState(t)
	if err := declareLaunch(context.Background(), nil, appSlug, &task.Spec{}); err != nil {
		t.Fatalf("a launch with no declarer failed: %v", err)
	}
}

func TestStubSaysWhyThroughASeam(t *testing.T) {
	extendState(t)
	t.Cleanup(setPaidNames([]string{"fugaro-test-paid", "digest"}))
	if !slices.Contains(coreRootPaths(), "fugaro runs digest") {
		t.Fatal("the released tree no longer carries runs digest: this test's nested-only name has moved")
	}

	assertStubSaysWhy(t, "fugaro-test-paid", "fugaro-test-paid")
	// The flags and subcommands a build without the code does not know are
	// arguments to this message, never cobra's own "unknown flag" or "unknown
	// command": the app asks with flags, and a refusal about flags would be a
	// lie about the command's name.
	assertStubSaysWhy(t, "fugaro-test-paid", "fugaro-test-paid", "--json")
	assertStubSaysWhy(t, "fugaro-test-paid", "fugaro-test-paid", "status", "--from-store", "--all", "--json")
	// A name the released tree holds only as another command's subcommand is a
	// name this build does not hold at the root, so it gets a stub of its own.
	assertStubSaysWhy(t, "digest", "digest")
}

// assertStubSaysWhy pins the stub's whole answer: the exact refusal, a
// non-zero exit, nothing on stdout, and no word about a licence anywhere. The
// message is a capability statement -- this build does not include the command
// -- and never a licence notice (D523, design ee-boundary-moves.md §6): it may
// not promise a licence, a key or a payment, which is what "licen" would do.
func assertStubSaysWhy(t *testing.T, name string, args ...string) {
	t.Helper()
	out, errOut, err := execute(t, args...)
	if err == nil {
		t.Fatalf("%v ran successfully, want a non-zero exit", args)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("%v exited %d, want %d: %v", args, ExitCode(err), ExitUserError, err)
	}
	want := name + " is part of Fugaro Subscription Edition; this build does not include it"
	if err.Error() != want {
		t.Errorf("the stub's message for %v = %q, want exactly %q", args, err.Error(), want)
	}
	if out != "" {
		t.Errorf("the stub wrote %q to stdout, want it empty", out)
	}
	for _, stream := range []struct{ name, text string }{{"stdout", out}, {"stderr", errOut}} {
		if strings.Contains(strings.ToLower(stream.text), "licen") {
			t.Errorf("the stub's %s %q speaks of a licence, which D523 defers", stream.name, stream.text)
		}
	}
}

func TestStubIsReplacedOnlyByItsRegistration(t *testing.T) {
	extendState(t)
	t.Cleanup(setPaidNames([]string{"fugaro-test-paid", "fugaro-test-other", "fugaro-test-third"}))
	if err := RegisterCommand("fugaro-test-paid", registeredCmd("fugaro-test-paid", "registered")); err != nil {
		t.Fatal(err)
	}
	if err := RegisterCommand("fugaro-test-third", registeredCmd("fugaro-test-third", "third")); err != nil {
		t.Fatal(err)
	}

	out, _, err := execute(t, "fugaro-test-paid")
	if err != nil || out != "registered\n" {
		t.Errorf("the name its registration holds = %q, %v; want the registered command", out, err)
	}
	out, _, err = execute(t, "fugaro-test-third")
	if err != nil || out != "third\n" {
		t.Errorf("a second registration's own name = %q, %v; want its command", out, err)
	}
	_, _, err = execute(t, "fugaro-test-other")
	if err == nil || !strings.Contains(err.Error(), "part of Fugaro Subscription Edition") {
		t.Errorf("a stub left alone by two registrations = %v, want the Subscription Edition message", err)
	}
}

// TestUntaggedBuildStillLaunches: an untagged build registers no declarer, so
// a launch succeeds and declares nothing (D535) — the coordinator's record is
// the coordinator's own, and the code that writes it moved to ee/ with the
// command. That a tagged build's launch reaches ee/'s Declare instead is
// ee/register/coordinator_test.go's identity check.
func TestUntaggedBuildStillLaunches(t *testing.T) {
	extendState(t)
	f := newLaunchBudget(t, "observe")
	env := memEnv(t, f.cloudFixture)
	spec := raceSpec(t, env)
	spec.RequestedBy = "owner@example.com"

	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); err != nil {
		t.Fatalf("an untagged launch failed: %v", err)
	}
	if v := f.db.Value(coordpath.PathRoot); v != nil {
		t.Errorf("an untagged launch wrote %v into the coordinator's subtree, want nothing (D535)", v)
	}
}

// raceSpecAt is raceSpec with a run id of its own, so a test can launch twice:
// one run id is one launch, and a second launch of the same id finds the first
// one's claim and never reaches the declaration.
func raceSpecAt(t *testing.T, env *cloudEnv, runID string) *task.Spec {
	t.Helper()
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	if err := runstore.Open(env.bucket.Bucket, appSlug, runID).CreateTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

// installDeclarer installs d as the build's launch declarer for one test, and
// sends the launch's warnings to the builder it answers.
func installDeclarer(t *testing.T, d LaunchDeclarer) *strings.Builder {
	t.Helper()
	extendState(t)
	if err := SetLaunchDeclarer(d); err != nil {
		t.Fatalf("setting the launch declarer: %v", err)
	}
	var warned strings.Builder
	prevWarn := launchWarn
	launchWarn = &warned
	t.Cleanup(func() { launchWarn = prevWarn })
	return &warned
}

// TestALaunchHandsTheDeclarerItsOwnRun: the launch path reaches an installed
// declarer through the hook, once, with the launch's own environment, project
// and spec -- the declarer moved out of the core, and this is the core's half
// of the seam it moved through.
func TestALaunchHandsTheDeclarerItsOwnRun(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpecAt(t, env, "20260927-100001-wxyz")
	var declared []string
	installDeclarer(t, func(_ context.Context, c *Cloud, slug string, got *task.Spec) error {
		if c == nil || c.env != env {
			t.Errorf("the declarer was handed %v, want the launch's own environment", c)
		}
		if got == nil || got.RunID != spec.RunID || got.Task != spec.Task {
			t.Errorf("the declarer was handed the run %v, want the launch's own (%s)", got, spec.RunID)
		}
		declared = append(declared, slug)
		return nil
	})

	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); err != nil {
		t.Fatalf("the launch failed: %v", err)
	}
	if len(declared) != 1 || declared[0] != appSlug {
		t.Errorf("the installed declarer declared %v, want one declaration of %s", declared, appSlug)
	}
}

// TestAFailedReportDoesNotFailTheLaunch: the coordinator is advisory (spec
// §3), so a declaration that cannot be written is a warning on stderr and
// never a failed launch -- a run must not wait on a service that may not be
// deployed.
func TestAFailedReportDoesNotFailTheLaunch(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	warned := installDeclarer(t, func(context.Context, *Cloud, string, *task.Spec) error {
		return errors.New("the database is unreachable")
	})

	if _, err := launchRun(context.Background(), env, appSlug, raceSpec(t, env), time.Now()); err != nil {
		t.Fatalf("a failed report failed the launch: %v", err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
	if !strings.Contains(warned.String(), "not reported to the coordinator") {
		t.Errorf("the failure was silent: %q", warned.String())
	}
}

// TestARefusedReportIsQuiet: a launcher who is not a budget admin cannot write
// the coordinator's record, and that is the expected case for most people who
// launch rather than a fault to tell them about on every run (owner ruling
// 2026-10-24). The launch still starts and nothing is printed.
func TestARefusedReportIsQuiet(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	warned := installDeclarer(t, func(context.Context, *Cloud, string, *task.Spec) error {
		return fmt.Errorf("writing the intent: %w", rtdb.ErrPermission)
	})

	if _, err := launchRun(context.Background(), env, appSlug, raceSpec(t, env), time.Now()); err != nil {
		t.Fatalf("a refused report failed the launch: %v", err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
	if warned.Len() != 0 {
		t.Errorf("a refused report printed %q, want nothing", warned.String())
	}
}

// TestAFailedReportWarnsOnOneSafeLine: any failure other than a refusal is
// still one warning line, and the error's text, which can quote what a remote
// database answered, reaches the terminal without its control sequences.
func TestAFailedReportWarnsOnOneSafeLine(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	warned := installDeclarer(t, func(context.Context, *Cloud, string, *task.Spec) error {
		return errors.New("the database answered \x1b]52;c;cGF3bmVk\x07 503")
	})

	if _, err := launchRun(context.Background(), env, appSlug, raceSpec(t, env), time.Now()); err != nil {
		t.Fatalf("a failed report failed the launch: %v", err)
	}
	got := warned.String()
	if strings.Count(got, "\n") != 1 || !strings.HasPrefix(got, "warning: the launch was not reported to the coordinator") {
		t.Errorf("the warning = %q, want one line", got)
	}
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x07') {
		t.Errorf("the warning carries the remote text's controls: %q", got)
	}
}

// TestUntaggedPaidCommandSaysWhy: with the coordinator's and the report's code
// moved to ee/, an untagged build holds each name as a stub and nothing more
// (D522, G3): it exits non-zero with the one Enterprise message, writes nothing
// to stdout, and is never cobra's "unknown command" — which would tell a source
// builder and the console that the name was never part of the CLI.
func TestUntaggedPaidCommandSaysWhy(t *testing.T) {
	extendState(t)
	for _, held := range []string{"fugaro coordinator", "fugaro report"} {
		if slices.Contains(coreRootPaths(), held) {
			t.Fatalf("the released tree still holds %s: the stub is not installed for a name a core command owns", held)
		}
	}
	assertStubSaysWhy(t, "coordinator", "coordinator")
	// A subcommand and a flag reach the same message: the stub parses neither,
	// so the console's own argv gets the refusal rather than "unknown flag".
	assertStubSaysWhy(t, "coordinator", "coordinator", "status", "--json")

	assertStubSaysWhy(t, "report", "report")
	// The console spawns `report --json`, so that argv reaches the stub rather
	// than a parse error about a flag the stub does not have.
	assertStubSaysWhy(t, "report", "report", "--json")
	assertStubSaysWhy(t, "report", "report", "--by", "mode", "--json")
}

// TestUntaggedBuildHasTheGraphStub: `fugaro graph` is ee/'s command (D1362), so
// a core build carries no graph command at all -- and the name is still an
// answer the CLI gives, the stub D522 fixes, rather than cobra's "unknown
// command", which would tell a source builder, the console and a skill that the
// name was never part of the CLI (T3). The other half of the rule is
// ee/register's TestTaggedBuildCarriesNoPaidStub: a build whose edition is ee
// shows no stub, because its sentence is written for the builds that are not
// the Subscription Edition.
func TestUntaggedBuildHasTheGraphStub(t *testing.T) {
	extendState(t)
	if slices.Contains(coreRootPaths(), "fugaro graph") {
		t.Fatal("the released tree holds a graph command: the stub is not installed for a name a core command owns")
	}
	if got := commandPaths(NewRootCmd()); !slices.Contains(got, "fugaro graph") {
		t.Fatalf("a build without the ee tag answers no graph command:\n%v", got)
	}
	assertStubSaysWhy(t, "graph", "graph")
}

// TestGraphStubRefusesWhateverTheFlags: the stub parses nothing, so every argv
// the console sends -- the one-shot read, the live feed, the scope as a value
// -- reaches the one message rather than cobra's "unknown flag", which would
// be a lie about a command the released binary has (D522). CG9a reads the
// refusal as "this CLI has no graph" and keeps the plan scope.
func TestGraphStubRefusesWhateverTheFlags(t *testing.T) {
	extendState(t)
	assertStubSaysWhy(t, "graph", "graph", "--json")
	assertStubSaysWhy(t, "graph", "graph", "--source", "coordinator", "--json")
	assertStubSaysWhy(t, "graph", "graph", "--watch", "--json")
	assertStubSaysWhy(t, "graph", "graph", "--help")
}

// ---- the launch planner (A3.0) -------------------------------------------

// abPair is the pair a test planner answers with: the champion's block for
// pair pid, and a challenger task at run id challenger running model.
func abPair(pid, challenger, model string) *Pair {
	return &Pair{
		Champion: task.AB{PairID: pid, Arm: task.ABArmChampion},
		Challenger: &task.Spec{
			Version: 1, RunID: challenger, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
			AB: &task.AB{PairID: pid, Arm: task.ABArmChallenger, Publish: task.ABPublishNone,
				Challenger: model, ChallengerSource: task.ABChallengerOwner},
		},
	}
}

// installPlanner installs p as the build's launch planner for one test.
func installPlanner(t *testing.T, p LaunchPlanner) {
	t.Helper()
	extendState(t)
	if err := RegisterLaunchPlanner(p); err != nil {
		t.Fatalf("registering the launch planner: %v", err)
	}
}

// TestNoPlannerLaunchUnchanged: the core default is no planner, so a launch
// samples nothing, marks nothing and says nothing of its own — the whole of
// D550's "the plumbing is inert in a build that registers none".
func TestNoPlannerLaunchUnchanged(t *testing.T) {
	extendState(t)
	f := newLaunchBudget(t, "observe")
	out, errOut, err := runBudget(t)
	if err != nil {
		t.Fatalf("an untagged launch failed: %v", err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
	spec := readSpec(t, f.cloudFixture, budgetRun)
	if spec.AB != nil {
		t.Errorf("an untagged launch sampled a pair: ab = %+v", spec.AB)
	}
	if spec.Batch != "" {
		t.Errorf("batch = %q, want none", spec.Batch)
	}
	if strings.Contains(out+errOut, "ab:") {
		t.Errorf("an untagged launch printed something of its own: %q / %q", out, errOut)
	}
}

// TestPlannerCalledBeforeTaskWritten: the hook is asked once, before the first
// run's task exists, with the launch's own resolved facts — the commit the run
// is cut from, the repository and its slug, and a copy of the task as it would
// be launched — and a planner that samples nothing leaves the launch as it was.
func TestPlannerCalledBeforeTaskWritten(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	var (
		calls  int
		atPlan PlannedRun
		there  bool
	)
	installPlanner(t, func(ctx context.Context, run PlannedRun) (*Pair, error) {
		calls++
		atPlan = run
		b, err := blob.OpenBucket(ctx, f.bucket)
		if err != nil {
			t.Errorf("opening the runs bucket: %v", err)
			return nil, nil
		}
		defer b.Close()
		if _, err := runstore.Open(b, appSlug, budgetRun).ReadTask(ctx); err == nil {
			there = true
		}
		run.Spec.Task = "the planner's own copy"
		return nil, nil
	})
	if _, _, err := runBudget(t); err != nil {
		t.Fatalf("the launch failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("%d planner calls, want 1", calls)
	}
	if there {
		t.Error("the planner was asked after the task was written")
	}
	if atPlan.Cloud == nil || atPlan.Cloud.env == nil {
		t.Error("the planner was handed no cloud handle")
	}
	if atPlan.Slug != appSlug || atPlan.Repo != "acme/app" {
		t.Errorf("planned %q of %q, want %q of acme/app", atPlan.Slug, atPlan.Repo, appSlug)
	}
	if atPlan.Spec == nil || atPlan.Spec.RunID != budgetRun {
		t.Errorf("planned task = %+v, want the launch's own run", atPlan.Spec)
	}
	if atPlan.Chosen != true {
		t.Error("--run-id was given, and the planner was not told the run id was chosen")
	}
	if atPlan.Config == nil {
		t.Error("the planner was handed no configuration")
	}
	if spec := readSpec(t, f.cloudFixture, budgetRun); spec.Task != "Add a feature" {
		t.Errorf("the stored task = %q, want the launch's own: the planner was handed a copy", spec.Task)
	}
}

// TestLaunchCommitResolvesARef: the commit the planner is handed is the one
// the run is cut from (D571): the ref resolved in this machine's checkout of
// the repository, a full commit SHA as itself, and nothing at all where there
// is no checkout to resolve it in.
func TestLaunchCommitResolvesARef(t *testing.T) {
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "x")
	sha := testutil.Git(t, dir, "rev-parse", "HEAD")
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/main", sha)

	if got := commitAt(context.Background(), dir, "main"); got != sha {
		t.Errorf("commitAt(main) = %q, want %q", got, sha)
	}
	if got := commitAt(context.Background(), dir, sha); got != sha {
		t.Errorf("commitAt(%s) = %q, want itself", sha, got)
	}
	if got := commitAt(context.Background(), dir, "nope"); got != "" {
		t.Errorf("commitAt(nope) = %q, want nothing", got)
	}
	if got := commitAt(context.Background(), "", "main"); got != "" {
		t.Errorf("commitAt with no checkout = %q, want nothing", got)
	}
}

// TestPairLaunchesBothArmsUnderOneBatchLabel: a pair the budget has room for
// is written and launched whole — the champion carries the pair's block and
// the batch label both arms share, the challenger is the champion's own task
// with the arm's run id and block, and the day's pair pre-check stands in for
// the champion's single-run one.
func TestPairLaunchesBothArmsUnderOneBatchLabel(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	installPlanner(t, func(context.Context, PlannedRun) (*Pair, error) {
		return abPair("20261111-abcd1234", "20261111-120000-beef", "gemini-2.5-pro"), nil
	})
	if _, _, err := runBudget(t); err != nil {
		t.Fatalf("the launch failed: %v", err)
	}
	if n := len(f.run.Executions()); n != 2 {
		t.Fatalf("%d executions, want the pair's 2", n)
	}
	champion := readSpec(t, f.cloudFixture, budgetRun)
	if champion.AB == nil || champion.AB.Arm != task.ABArmChampion || champion.AB.PairID != "20261111-abcd1234" {
		t.Fatalf("the champion's block = %+v", champion.AB)
	}
	const label = "ab-20261111-abcd1234"
	if champion.Batch != label {
		t.Errorf("the champion's batch = %q, want %q", champion.Batch, label)
	}
	challenger := readSpec(t, f.cloudFixture, "20261111-120000-beef")
	if challenger.AB == nil || challenger.AB.Arm != task.ABArmChallenger || challenger.AB.Challenger != "gemini-2.5-pro" {
		t.Fatalf("the challenger's block = %+v", challenger.AB)
	}
	if challenger.Batch != label {
		t.Errorf("the challenger's batch = %q, want the pair's one label %q", challenger.Batch, label)
	}
	if challenger.Task != champion.Task || challenger.Ref != champion.Ref || challenger.Workflow != champion.Workflow {
		t.Errorf("the arms differ beyond the block: %+v vs %+v", challenger, champion)
	}
	if challenger.Overrides.Model != "" {
		t.Error("the challenger's model travelled as an override, which D555 forbids")
	}
}

// TestPlannerErrorNeverFailsLaunch: a planner that fails is a warning on one
// line, never a failed launch: the champion starts alone, unmarked (D554).
func TestPlannerErrorNeverFailsLaunch(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	installPlanner(t, func(context.Context, PlannedRun) (*Pair, error) {
		return nil, errors.New("the challenger list could not be read")
	})
	_, errOut, err := runBudget(t)
	if err != nil {
		t.Fatalf("a planner error failed the launch: %v", err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the champion alone", n)
	}
	spec := readSpec(t, f.cloudFixture, budgetRun)
	if spec.AB != nil || spec.Batch != "" {
		t.Errorf("the champion carries %+v / %q, want no pair block and no batch", spec.AB, spec.Batch)
	}
	if !strings.Contains(errOut, "ab:") {
		t.Errorf("the planner's failure was silent: %q", errOut)
	}
}

// TestPairPrecheckRefusedLaunchesChampionAloneUnmarked: the pair's own
// pre-check is one lease of two headrooms, before either arm starts; when the
// budget refuses it, the champion still goes — alone, unmarked, on the single
// pre-check every launch makes (D554, D566).
func TestPairPrecheckRefusedLaunchesChampionAloneUnmarked(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	// The repository's day is nearly spent: room for one run, not for two.
	f.db.Set(budget.PathSpendRepo(budget.Day(time.Now()), appSlug), map[string]any{"spent": 49 * usd1})
	installPlanner(t, func(context.Context, PlannedRun) (*Pair, error) {
		return abPair("20261111-abcd1234", "20261111-120000-beef", "gemini-2.5-pro"), nil
	})
	_, errOut, err := runBudget(t)
	if err != nil {
		t.Fatalf("a refused pair failed the champion's launch: %v", err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the champion alone", n)
	}
	spec := readSpec(t, f.cloudFixture, budgetRun)
	if spec.AB != nil || spec.Batch != "" {
		t.Errorf("the champion carries %+v / %q, want no pair block and no batch", spec.AB, spec.Batch)
	}
	if !strings.Contains(errOut, "ab:") {
		t.Errorf("the refused pair was silent: %q", errOut)
	}
}

// TestMarginNeedsFiveCaps: a pair is sampled only with five per-run caps of
// the day left (D573) — in observe as much as enforce — and the champion goes
// alone, unmarked, when the margin is not there.
func TestMarginNeedsFiveCaps(t *testing.T) {
	// The fixture's per-run cap is 5 USD (the default), so the margin is 25.
	for _, tc := range []struct {
		name    string
		spent   int64
		sampled bool
	}{
		{"the day has five caps left", 25 * usd1, true},
		{"the day has four caps left", 26 * usd1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchBudget(t, "observe")
			f.db.Set(budget.PathSpendRepo(budget.Day(time.Now()), appSlug), map[string]any{"spent": tc.spent})
			installPlanner(t, func(context.Context, PlannedRun) (*Pair, error) {
				return abPair("20261111-abcd1234", "20261111-120000-beef", "gemini-2.5-pro"), nil
			})
			_, errOut, err := runBudget(t)
			if err != nil {
				t.Fatalf("the launch failed: %v", err)
			}
			sampled := readSpec(t, f.cloudFixture, budgetRun).AB != nil
			if sampled != tc.sampled {
				t.Errorf("sampled = %v with %d micros spent, want %v (%s)", sampled, tc.spent, tc.sampled, errOut)
			}
			if n := len(f.run.Executions()); n != map[bool]int{true: 2, false: 1}[tc.sampled] {
				t.Errorf("%d executions, want %d", n, map[bool]int{true: 2, false: 1}[tc.sampled])
			}
			if !tc.sampled && !strings.Contains(errOut, "ab:") {
				t.Errorf("the refused margin was silent: %q", errOut)
			}
		})
	}
}

// TestPlannerNotCalledForFollowUpRetryAttempts: the hook is for a first run's
// task alone: a follow-up, a retry and a best-of-N attempt never reach it
// (D558), whatever the build registered.
func TestPlannerNotCalledForFollowUpRetryAttempts(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	setMaxParallel(t, 4) // the retry below launches after --attempts has two runs in flight
	var calls int
	installPlanner(t, func(context.Context, PlannedRun) (*Pair, error) {
		calls++
		return nil, nil
	})
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--attempts", "2", "Add a feature"); err != nil {
		t.Fatalf("--attempts failed: %v", err)
	}
	if calls != 0 {
		t.Fatalf("--attempts asked the planner %d times, want none", calls)
	}
	// A stored task that never launched: --retry starts it as it stands.
	seedRun(t, f.cloudFixture, "20261111-130000-cafe", "", "someone@example.com", false)
	if _, _, err := execute(t, "run", "--retry", "20261111-130000-cafe"); err != nil {
		t.Fatalf("--retry failed: %v", err)
	}
	if calls != 0 {
		t.Fatalf("--retry asked the planner %d times, want none", calls)
	}
}

// TestRetryRefusesABTask: --retry launches a stored task as it stands, and an
// arm is one half of a pair whose other half is a record of its own: a stored
// task carrying a pair block is refused rather than replayed as a lone run.
func TestRetryRefusesABTask(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	const id = "20261111-130000-cafe"
	spec := &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x",
		AB: &task.AB{PairID: "ab-20261111-abcd1234", Arm: task.ABArmChampion}}
	if err := runstore.Open(b, appSlug, id).CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "run", "--retry", id)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "A/B") {
		t.Fatalf("--retry of an arm: err = %v, want exit 1 naming the pair", err)
	}
}
