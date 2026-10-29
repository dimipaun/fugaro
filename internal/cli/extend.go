package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dimipaun/fugaro/internal/task"
)

// The seams one binary ships with (design ee-boundary-moves.md §5). One
// binary carries the released CLI and the commands the owner moves out of the
// core, and the moved code reaches this tree through this file alone: a
// registry that adds commands, and a hook for the one call the launch makes
// into the coordinator's record. Everything here is inert — with nothing
// registered and nothing set, the tree and every launch are exactly what the
// core builds. `ee/register` is what fills them (§5.5), and no core caller
// sets either.

// RegisterCommand adds a command to the root tree. One binary ships (T21), so
// a command the owner moves out of the core joins the tree the released CLI
// builds, rather than a second tree beside it.
//
// A registration ADDS and never REPLACES: it is refused when a name the
// released tree already answers to would come to mean the registered command
// instead (see the checks below). What it replaces is the core's own stub for
// that name, which is exactly the one thing meant to be taken over — a name
// whose code this build does not carry.
//
// The name is the registration's identity, so the command the constructor
// builds must carry it: a constructor that builds a differently named command
// would be an addition under one name and a takeover under another.
func RegisterCommand(name string, newCmd func() *cobra.Command) error {
	if name == "" || strings.ContainsAny(name, " \t\n") {
		return fmt.Errorf("registering %q: a command name is one word", name)
	}
	if newCmd == nil {
		return fmt.Errorf("registering %s: no command constructor", name)
	}
	if registrationsSealed {
		return fmt.Errorf("registering %s: the command tree is already built, and a registration that arrives after it would never reach a tree", name)
	}
	if _, dup := registrations[name]; dup {
		return fmt.Errorf("registering %s: it is already registered", name)
	}
	core := coreSurface()
	if core.commands[name] {
		return fmt.Errorf("registering %s: the released tree holds a command of that name, and a registration adds a command rather than replacing one", name)
	}
	cmd := newCmd()
	if cmd == nil {
		return fmt.Errorf("registering %s: its constructor built no command", name)
	}
	if cmd.Name() != name {
		return fmt.Errorf("registering %s: the command it built is named %q, so it would be added under one name and answer to another", name, cmd.Name())
	}
	for _, child := range cmd.Commands() {
		if core.commands[child.Name()] {
			return fmt.Errorf("registering %s: it carries a %s subcommand, and the released tree already answers to that name", name, child.Name())
		}
	}
	// A persistent flag is inherited by everything under the command it sits
	// on, so re-declaring one of the tree's own takes that flag away from the
	// subtree rather than adding a flag beside it. A command's own local flags
	// are its business and are not judged here.
	var clash string
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if clash == "" && core.flags[f.Name] {
			clash = f.Name
		}
	})
	if clash != "" {
		return fmt.Errorf("registering %s: it declares --%s as a persistent flag, and the released tree already defines that name", name, clash)
	}
	registrations[name] = newCmd
	return nil
}

// LaunchDeclarer declares one launch to the coordinator's record. The core
// default is the declarer the launch has always used, so a hook that landed
// before the move cannot stop every launch declaring itself (F16a); E2.1
// flips the default to declaring nothing (D535) and `ee/register` registers
// today's declarer.
type LaunchDeclarer func(ctx context.Context, c *Cloud, slug string, spec *task.Spec) error

// SetLaunchDeclarer installs the build's launch declarer. It refuses a second
// call, so nothing can quietly replace what a tagged build installed (§5.5),
// and it refuses nil: "declare nothing" is the absence of a registration, not
// a declarer that does nothing.
func SetLaunchDeclarer(d LaunchDeclarer) error {
	if launchDeclarerSet {
		return errors.New("the launch declarer is already set")
	}
	if d == nil {
		return errors.New("the launch declarer is nil")
	}
	launchDeclarer, launchDeclarerSet = d, true
	return nil
}

// declareLaunch declares one launch through the build's declarer. The launch
// path calls this rather than the declarer itself, so a build whose declarer
// moved can register its own.
func declareLaunch(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec) error {
	d := launchDeclarer
	if d == nil {
		return nil
	}
	return d(ctx, &Cloud{env: env}, slug, spec)
}

// coreLaunchDeclarer is the core default: today's declarer, read when it is
// called rather than captured at init, so the tests that replace
// reportLaunchIntent keep replacing exactly the one thing that reaches a
// database — and so a build that never sets the hook declares exactly what it
// declares today.
func coreLaunchDeclarer(ctx context.Context, c *Cloud, slug string, spec *task.Spec) error {
	return reportLaunchIntent(ctx, c.env, slug, spec)
}

// paidCommandNames are the command names whose code the owner moves out of the
// core. A build without that code shows a stub for each name it does not hold
// rather than leaving the name unknown to cobra, whose "unknown command" would
// mislead a source builder and the app into thinking the name was never part
// of the CLI (D522).
var paidCommandNames = []string{"report", "coordinator"}

// newPaidStub is the command a build shows for a paid name it carries no code
// for. It refuses, non-zero, with the one message D522 fixes, and it carries
// no licence notice of any kind (D523): nothing can run, so this is not the
// warn-and-continue the config fields get. It takes whatever arguments it is
// given, because every spelling of the name must reach this message.
func newPaidStub(name string) *cobra.Command {
	return &cobra.Command{
		Use: name,
		RunE: func(*cobra.Command, []string) error {
			return userErr("%s is part of Fugaro Enterprise; this build does not include it", name)
		},
	}
}

// applyExtensions is what a built root gets from this file: the commands
// registered for it, and a stub for every paid name no core command holds.
// It is called once per tree, when the tree is built, and it refuses nothing —
// registration is where a name was judged.
func applyExtensions(root *cobra.Command) {
	core := coreSurface()
	names := make([]string, 0, len(registrations))
	for name := range registrations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		root.AddCommand(registrations[name]())
	}
	for _, name := range paidCommandNames {
		if core.commands[name] {
			// The core command is the one that ships: its stub would take the
			// name over rather than stand in for absent code.
			continue
		}
		if _, registered := registrations[name]; registered {
			continue
		}
		root.AddCommand(newPaidStub(name))
	}
	registrationsSealed = true
}

// coreNames are the names the released tree answers to: every command in it,
// and every flag any of its commands defines. A registration is judged against
// them, so the rule reads as one sentence: an addition may bring new names and
// may never take over a name the tree already has.
type coreNames struct {
	commands map[string]bool
	flags    map[string]bool
}

// surface is built by building the bare tree once and walking it. Only the
// names are kept: a tree is built fresh for every command the binary runs, so
// no tree may be held here.
var surface *coreNames

func coreSurface() *coreNames {
	if surface != nil {
		return surface
	}
	names := &coreNames{commands: map[string]bool{}, flags: map[string]bool{}}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		names.commands[c.Name()] = true
		c.LocalFlags().VisitAll(func(f *pflag.Flag) { names.flags[f.Name] = true })
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(newCoreRoot())
	surface = names
	return names
}

var (
	// registrations are the commands a build registered, by name. They are
	// constructors rather than commands because a tree is built fresh for
	// every command the binary runs, and a cobra command belongs to one tree.
	registrations = map[string]func() *cobra.Command{}
	// registrationsSealed is set when a tree is built: a registration that
	// arrives after one would never reach a tree, so it is refused loudly
	// rather than accepted and ignored.
	registrationsSealed bool
	// launchDeclarer is the build's declarer; launchDeclarerSet is what makes
	// SetLaunchDeclarer refuse a second call.
	launchDeclarer    LaunchDeclarer = coreLaunchDeclarer
	launchDeclarerSet bool
)
