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

	"github.com/dimipaun/fugaro/internal/coordpath"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
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

// TestUntaggedPaidCommandSaysWhy: with the coordinator's code moved to ee/,
// an untagged build holds the name as a stub and nothing more (D522, G3): it
// exits non-zero with the one Enterprise message, writes nothing to stdout,
// and is never cobra's "unknown command" — which would tell a source builder
// and the console that the name was never part of the CLI. `report` is the
// same shape and E2.2 re-points this test at it; `coordinator` is the one the
// move flipped.
func TestUntaggedPaidCommandSaysWhy(t *testing.T) {
	extendState(t)
	if slices.Contains(coreRootPaths(), "fugaro coordinator") {
		t.Fatal("the released tree still holds the coordinator command: the stub is not installed for a name a core command owns")
	}
	assertStubSaysWhy(t, "coordinator", "coordinator")
	// A subcommand and a flag reach the same message: the stub parses neither,
	// so the console's own argv gets the refusal rather than "unknown flag".
	assertStubSaysWhy(t, "coordinator", "coordinator", "status", "--json")
}
