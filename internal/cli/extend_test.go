package cli

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

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

	refused := []struct{ what, key, use string }{
		{"a key the released tree holds", "run", "run"},
		{"a command named after a core command", "fugaro-test-paid", "run"},
	}
	for _, tc := range refused {
		err := RegisterCommand(tc.key, func() *cobra.Command {
			return &cobra.Command{Use: tc.use, Short: "the registered impostor"}
		})
		if err == nil {
			t.Fatalf("%s (%s) was accepted: a paid command could take the released run over", tc.what, tc.key)
		}
		if !strings.Contains(err.Error(), "run") {
			t.Errorf("%s: the refusal %q does not name the command it refused to replace", tc.what, err)
		}
	}

	got := findCmd(t, NewRootCmd(), "run").Short
	if got != want {
		t.Errorf("the tree's run command is %q, want the released one (%q): a registration replaced a core command", got, want)
	}
	if len(registrations) != 0 {
		t.Errorf("a refused registration was kept: %v", registrations)
	}
}

func TestRegisteredCommandCannotReplaceCoreSubcommandsOrPersistentFlags(t *testing.T) {
	extendState(t)
	core := coreSurface()

	withRun := func() *cobra.Command {
		c := &cobra.Command{Use: "fugaro-test-paid"}
		c.AddCommand(&cobra.Command{Use: "run", Short: "the registered impostor"})
		return c
	}
	err := RegisterCommand("fugaro-test-paid", withRun)
	if err == nil {
		t.Fatal("a registered command carrying a run subcommand was accepted: the tree would answer to a core name twice")
	} else if !strings.Contains(err.Error(), "run") {
		t.Errorf("the refusal %q does not name the subcommand it refused", err)
	}

	if !core.flags["config"] {
		t.Fatal("the released tree defines no --config flag: this test's core flag name has moved")
	}
	withFlag := func() *cobra.Command {
		c := &cobra.Command{Use: "fugaro-test-paid"}
		c.PersistentFlags().String("config", "", "a flag that takes a core name over")
		return c
	}
	err = RegisterCommand("fugaro-test-paid", withFlag)
	if err == nil {
		t.Fatal("a registered command declaring --config as a persistent flag was accepted: it takes the core flag away from everything under it")
	} else if !strings.Contains(err.Error(), "config") {
		t.Errorf("the refusal %q does not name the flag it refused", err)
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

// TestNoRegistrationLeavesTheRootUnchanged: the registry is inert. A build that
// registers nothing, and whose paid names are still core commands, builds
// exactly the released tree — no stub, no extra command. This is the test that
// has to change in the commit that makes report and coordinator paid commands:
// from there the tree gains their stubs and, in a tagged build, the commands
// themselves.
func TestNoRegistrationLeavesTheRootUnchanged(t *testing.T) {
	extendState(t)
	got, want := commandPaths(NewRootCmd()), coreRootPaths()
	if len(got) != len(want) {
		t.Fatalf("the built tree holds %d commands, the released one %d:\n%v\n%v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("the built tree's commands differ from the released ones at %d: %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLaunchWithoutADeclarerDeclaresNothing(t *testing.T) {
	extendState(t)
	SetNoLaunchDeclarer()
	var declared bool
	prev := reportLaunchIntent
	reportLaunchIntent = func(context.Context, *cloudEnv, string, *task.Spec) error {
		declared = true
		return nil
	}
	t.Cleanup(func() { reportLaunchIntent = prev })

	if err := declareLaunch(context.Background(), nil, appSlug, &task.Spec{}); err != nil {
		t.Fatalf("a launch with no declarer failed: %v", err)
	}
	if declared {
		t.Error("a launch with no declarer declared something")
	}
}

func TestStubSaysWhyThroughASeam(t *testing.T) {
	extendState(t)
	t.Cleanup(setPaidNames([]string{"fugaro-test-paid"}))

	if _, _, err := execute(t, "fugaro-test-paid"); err == nil {
		t.Fatal("the stub ran successfully, want a non-zero exit")
	} else {
		if ExitCode(err) != ExitUserError {
			t.Errorf("the stub exited %d, want %d: %v", ExitCode(err), ExitUserError, err)
		}
		for _, want := range []string{"fugaro-test-paid", "part of Fugaro Enterprise", "does not include it"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the stub's message %q does not say %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "unknown command") {
			t.Errorf("the stub fell through to cobra's unknown command: %q", err)
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
	if err == nil || !strings.Contains(err.Error(), "part of Fugaro Enterprise") {
		t.Errorf("a stub left alone by two registrations = %v, want the Enterprise message", err)
	}
}

// TestLaunchStillDeclaresBeforeTheMove: the hook's core default is today's
// declarer (F16a), so a hook that lands before the declarer moves cannot stop
// every launch declaring itself — and the launch path reaches through the hook,
// so a build whose declarer moved is the one that runs.
func TestLaunchStillDeclaresBeforeTheMove(t *testing.T) {
	extendState(t)
	f := newLaunchBudget(t, "observe")
	env := memEnv(t, f.cloudFixture)
	spec := raceSpec(t, env)
	spec.RequestedBy = "owner@example.com"

	var preMove []string
	prev := reportLaunchIntent
	reportLaunchIntent = func(_ context.Context, got *cloudEnv, slug string, _ *task.Spec) error {
		if got != env {
			t.Error("the core declarer was handed another environment")
		}
		preMove = append(preMove, slug)
		return nil
	}
	t.Cleanup(func() { reportLaunchIntent = prev })

	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); err != nil {
		t.Fatalf("the launch failed: %v", err)
	}
	if len(preMove) != 1 || preMove[0] != appSlug {
		t.Fatalf("the launch declared %v, want one declaration of %s", preMove, appSlug)
	}

	var hooked []string
	wantSpec := spec
	err := SetLaunchDeclarer(func(_ context.Context, c *Cloud, slug string, got *task.Spec) error {
		if c == nil || c.env != env {
			t.Errorf("the hook was handed %v, want the launch's own environment", c)
		}
		if got == nil || got.RunID != wantSpec.RunID || got.Task != wantSpec.Task {
			t.Errorf("the hook was handed the run %v in %q, want the launch's own (%s)", got, got.Task, wantSpec.RunID)
		}
		hooked = append(hooked, slug)
		return nil
	})
	if err != nil {
		t.Fatalf("setting the launch declarer: %v", err)
	}
	if err := SetLaunchDeclarer(func(context.Context, *Cloud, string, *task.Spec) error { return nil }); err == nil {
		t.Error("a second declarer was accepted: nothing may replace what a build installed")
	}

	next := raceSpecAt(t, env, "20260927-100001-wxyz")
	wantSpec = next
	if _, err := launchRun(context.Background(), env, appSlug, next, time.Now()); err != nil {
		t.Fatalf("the launch failed: %v", err)
	}
	if len(hooked) != 1 || hooked[0] != appSlug {
		t.Errorf("the installed declarer declared %v, want one declaration of %s: the launch does not go through the hook", hooked, appSlug)
	}
	if len(preMove) != 1 {
		t.Errorf("the pre-move declarer ran %d times, want 1 (the first launch alone): the launch does not go through the hook", len(preMove))
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
