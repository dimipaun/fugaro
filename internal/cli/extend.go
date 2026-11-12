package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The seams one binary ships with (design ee-boundary-moves.md §5). One
// binary carries the released CLI and the commands the owner moves out of the
// core, and the moved code reaches this tree through this file alone: a
// registry that adds commands, a hook for the one call the launch makes into
// the coordinator's record, and the launch planner model A/B testing asks
// before a first run's task is written. Everything here is inert — with
// nothing registered and nothing set, the tree and every launch are exactly
// what the core builds. `ee/register` is what fills them (§5.5), and no core
// caller sets any of them.

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
	if core.rootCommands[name] {
		return fmt.Errorf("registering %s: the released tree holds a command of that name, and a registration adds a command rather than replacing one", name)
	}
	// An earlier registration's names are the second half of the same rule:
	// cobra answers to them in the order commands were added, so a second
	// registration under one of them would be found after that command rather
	// than instead of it — an addition under a name the tree already answers
	// to. The name and every alias a registration brought count alike.
	if registeredNames[name] {
		return fmt.Errorf("registering %s: an earlier registration already answers to that name", name)
	}
	cmd := newCmd()
	if cmd == nil {
		return fmt.Errorf("registering %s: its constructor built no command", name)
	}
	// A registration is added to the root, and cobra resolves a child by the
	// name the command itself carries: a constructor that builds a differently
	// named command would be an addition under one name and a takeover under
	// the other. The command's OWN subcommands are not judged: they live under
	// its own path, where a name the released tree uses at the root means
	// nothing different — which is what a moved command needs, since the
	// commands beside it keep their own names.
	if cmd.Name() != name {
		return fmt.Errorf("registering %s: the command it built is named %q, so it would be added under one name and answer to another", name, cmd.Name())
	}
	// cobra resolves a child by its aliases as well as its name, and it takes
	// the first match in the order the children were added: a registered
	// command that answers to a released name would be found after the
	// released command rather than instead of it, and one that answers to an
	// earlier registration's name or alias would collide with that command the
	// same way. Both are the takeover this refuses.
	for _, alias := range cmd.Aliases {
		if core.rootCommands[alias] {
			return fmt.Errorf("registering %s: it answers to %s, a name the released tree holds, and cobra would find the released command first", name, alias)
		}
		if registeredNames[alias] {
			return fmt.Errorf("registering %s: it answers to %s, which an earlier registration already holds", name, alias)
		}
	}
	// A persistent flag is inherited by everything under the command it sits
	// on, so re-declaring one of the root's own takes that flag away from the
	// subtree rather than adding a flag beside it. A command's own local flags
	// — and its persistent flags the root does not use — are its business.
	var clash string
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if clash == "" && core.rootFlags[f.Name] {
			clash = f.Name
		}
	})
	if clash != "" {
		return fmt.Errorf("registering %s: it declares --%s as a persistent flag, and the released tree already defines that name", name, clash)
	}
	registrations[name] = newCmd
	registeredNames[name] = true
	for _, alias := range cmd.Aliases {
		registeredNames[alias] = true
	}
	return nil
}

// LaunchDeclarer declares one launch to the coordinator's record. An untagged
// build registers none, so its launches declare nothing (D535): the record is
// the coordinator's, and the code that writes it moved out of the core.
// `ee/register/coordinator.go` is what installs the tagged build's declarer.
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
// moved can register its own. With none registered it opens nothing and
// answers nil: an untagged build declares nothing at all (D535).
func declareLaunch(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec) error {
	d := launchDeclarer
	if d == nil {
		return nil
	}
	return d(ctx, &Cloud{env: env}, slug, spec)
}

// PlannedRun is a first run a planner is asked about, before its task is
// written: the launch's own resolved facts, so the decision to sample a pair
// is made on the same commit and the same task the run would start with
// (D558, D571). Everything a planner needs that is not here is in the task it
// is handed — the project layer the run reads among it, whose own ab block is
// D556's challenger list — or behind Cloud.
type PlannedRun struct {
	// Cloud is the launch's own environment, for the reads only the planner
	// knows how to make (today's challenger records, the published policy).
	Cloud *Cloud
	// Slug is the repository's storage slug, Repo the repository itself.
	Slug string
	Repo string
	// Commit is the commit the run is cut from: the ref resolved in this
	// machine's checkout of the repository, the ref itself when it is already
	// a full commit SHA, and empty where this machine holds no checkout that
	// has it. A launch from outside one names a branch the launcher cannot
	// resolve, and a planner that needs the commit then resolves it itself.
	Commit string
	// Spec is the task as it would be launched, a copy: a planner that keeps
	// or edits it changes nothing about the run.
	Spec *task.Spec
	// Chosen is whether the caller named the run id (--run-id). D558 samples
	// the first runs of code tasks and never a launch whose id a person chose.
	Chosen bool
	// Config is the configuration the run will read (launchLevelsConfig): the
	// repository's file at the ref when this machine holds a checkout of it,
	// else the project layer the task carries resolved over the anchoring that
	// layer applies to, and the empty configuration where there is neither.
	Config *config.Config
	// Policy is the policy this launch resolves — the launcher's own merge of
	// the deployed ceiling, the default branch's fugaro.yaml, this
	// repository's file at the ref and this launch's chain limits
	// (chainLimitPolicyOf) — which is the merge a challenger's admission is
	// judged by (D556). It is not the runner's whole bootstrap merge: a
	// project that publishes a policy after this job's environment was
	// deployed has the node's own keys applied there and not here, and the
	// runner re-checks every challenger at bootstrap (A2.1). Providers and
	// Prices are the routing table and the price table this launch resolves,
	// both nil when there is none.
	Policy    policy.Effective
	Providers map[string]config.ModelProvider
	Prices    *pricing.Table
	// ServiceHeldKey is whether the credentials service holds this
	// installation's provider key, so that the job it starts opens the
	// provider keyless. It is the same switch that decides whether the job is
	// handed the service's address (credentialsServiceLaunchEnv).
	ServiceHeldKey bool
}

// Pair is what a planner answers about a launch: the pair it means to sample
// — the champion's ab block and the challenger's task — or, with no
// Challenger, the reason it sampled none (Skip). The champion's block is
// written onto the task the launch was already building; the challenger's
// task is read for the arm's own two facts — its run id and its ab block —
// and the launcher starts it from the champion's launched task, so the arms
// cannot drift apart (D555, D571).
type Pair struct {
	Champion   task.AB
	Challenger *task.Spec
	// Skip is why the planner sampled nothing, on a Pair it returns with no
	// Challenger: one of its closed reasons (D556), which the launcher prints
	// as its one skip line and carries in fugaro run --json. Empty is the
	// answer with nothing to say — not eligible, or the rate's own roll said
	// no — and a nil Pair says the same thing.
	Skip string
}

// LaunchPlanner is the build's A/B planner: the one call a launch makes
// before it samples a pair. An untagged build registers none, so its launches
// sample nothing (D550): the model A/B feature is the Subscription Edition's,
// and this hook is the MIT core's whole half of it. `ee/register/ab_launch.go`
// is what installs the tagged build's planner.
type LaunchPlanner func(ctx context.Context, run PlannedRun) (*Pair, error)

// RegisterLaunchPlanner installs the build's launch planner. It refuses a
// second call, so nothing can quietly replace what a tagged build installed
// (§5.5), and it refuses nil: "sample nothing" is the absence of a
// registration, not a planner that plans nothing.
func RegisterLaunchPlanner(p LaunchPlanner) error {
	if launchPlannerSet {
		return errors.New("the launch planner is already set")
	}
	if p == nil {
		return errors.New("the launch planner is nil")
	}
	launchPlanner, launchPlannerSet = p, true
	return nil
}

// planning reports whether this build has a planner at all. The launch path
// asks it first, so an untagged build's launch does none of the work a pair
// would need — no price table, no credential read, no budget read — and
// prints nothing of its own (D550).
func planning() bool { return launchPlanner != nil }

// planLaunch asks the build's planner whether this launch samples a pair,
// handing it the launch's own resolved facts and the environment behind
// Cloud. A build with no planner never reaches it.
func planLaunch(ctx context.Context, env *cloudEnv, run PlannedRun) (*Pair, error) {
	p := launchPlanner
	if p == nil {
		return nil, nil
	}
	run.Cloud = &Cloud{env: env}
	return p(ctx, run)
}

// RecipeManifest is the build's manifest builder for one registered catalog
// recipe: the objects a mode: evaluate launch leaves under its run's own prefix
// for the recipe's registered step to read back. The pair a judge step judges
// is decided here, on the launching machine, rather than by the step, which the
// job's own grant keeps to a single prefix — the same division the report
// manifest makes (digestManifest), and D550's "the plumbing is the core's, what
// it holds is the build's".
//
// warn is the launch's own stderr, for the lines a builder has to say.
type RecipeManifest func(ctx context.Context, run PlannedRun, warn io.Writer) ([]byte, error)

// RegisterRecipeManifest installs the build's manifest builder for a catalog
// recipe, by the recipe's own name. It refuses a name no registered catalog
// recipe holds — a builder under a name nothing can launch is a manifest that
// would never be written, and a name misspelled in ee/register would be silent
// otherwise — and it refuses a second builder for one recipe, so nothing can
// quietly replace what a tagged build installed (§5.5).
func RegisterRecipeManifest(name string, b RecipeManifest) error {
	if _, ok := recipe.CatalogText(name); !ok {
		return fmt.Errorf("recipe %s is not a catalog recipe this build holds, and a manifest builder is registered under the recipe whose launches write it", name)
	}
	if b == nil {
		return fmt.Errorf("registering the manifest builder for %s: it is nil", name)
	}
	if _, dup := recipeManifests[name]; dup {
		return fmt.Errorf("registering the manifest builder for %s: it is already registered", name)
	}
	recipeManifests[name] = b
	return nil
}

// writeRecipeManifest leaves the manifest a mode: evaluate launch's recipe
// builder answers, under the run's own prefix, before its task exists. Nothing
// is written for a launch of any other recipe, so the object stays absent the
// way an older release left it, and a launch whose builder refuses starts
// nothing and leaves nothing (§4 step 1 of the run design, as --attach does).
//
// The task is handed to the builder as a copy, through plannedRun: a builder
// judges the launch on exactly the facts the run will start with, and it cannot
// change the run by editing what it was handed. The size cap is the reader's
// own (runstore.MaxRecordBytes): a manifest past it could be written here and
// never read back by the step it was written for.
//
// Only a launch whose task carries a catalog recipe can write one: every
// registered builder is a catalog recipe's (RegisterRecipeManifest), and a
// launch whose recipe the runner chooses from the repository's own
// fugaro.yaml carries none in its task — the launcher would be guessing at a
// name, and a manifest under the wrong recipe's name is read by nothing.
func writeRecipeManifest(ctx context.Context, env *cloudEnv, o *runOptions, slug string, spec *task.Spec, s *runstore.Store, warn io.Writer) error {
	if spec.Recipe == nil || spec.Recipe.Source != string(recipe.SourceCatalog) {
		return nil
	}
	b, ok := recipeManifests[spec.Recipe.Name]
	if !ok {
		return nil
	}
	run, why := plannedRun(ctx, env, o, slug, spec)
	if why != "" {
		return userErr("recipe %s needs the manifest its launch writes, but %s", spec.Recipe.Name, why)
	}
	run.Cloud = &Cloud{env: env}
	body, err := b(ctx, run, warn)
	if err != nil {
		return err
	}
	if len(body) > runstore.MaxRecordBytes {
		return userErr("recipe %s's manifest is %d bytes, over the %d the reader takes, so it would be written and never read", spec.Recipe.Name, len(body), runstore.MaxRecordBytes)
	}
	return s.PutFile(ctx, runstore.ManifestName, body, "application/json")
}

// paidCommandNames are the command names whose code this build does not carry:
// the commands the owner moves out of the core, and the ones that never lived
// there — `graph` (D1362) and `ab` (D570) are ee/'s from the start. A core
// build shows a stub
// for each name it does not hold rather than leaving the name unknown to cobra,
// whose "unknown command" would mislead a source builder and the app into
// thinking the name was never part of the CLI (D522). A build whose edition is
// ee gets no stub: applyExtensions says why.
var paidCommandNames = []string{"report", "ab", "coordinator", "graph"}

// newPaidStub is the command a build shows for a paid name it carries no code
// for. It refuses, non-zero, with the one message D522 fixes, and it carries
// no licence notice of any kind (D523): nothing can run, so this is not the
// warn-and-continue the config fields get. It parses no flags of its own —
// there are none to parse, and a flag this command would have had is a flag
// this build does not know: cobra's own "unknown flag" would answer before
// this message, which is exactly the confusion the stub exists to avoid. So
// every spelling reaches the message, whatever arguments or flags it carries.
func newPaidStub(name string) *cobra.Command {
	return &cobra.Command{
		Use:                name,
		DisableFlagParsing: true,
		RunE: func(*cobra.Command, []string) error {
			return userErr("%s is part of Fugaro Subscription Edition; this build does not include it", name)
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
		if core.rootCommands[name] {
			// The core command is the one that ships: its stub would take the
			// name over rather than stand in for absent code. What counts is
			// the name at the root, where a command is spelled: a name another
			// command answers to below the root (runs' digest) is not this
			// name, and a build without this command's code must still say so.
			continue
		}
		if _, registered := registrations[name]; registered {
			continue
		}
		// The stub's message is a statement about a build that is not the
		// Subscription Edition — "graph is part of Fugaro Subscription Edition;
		// this build does not include it" is true of the core build it is
		// written for, and a falsehood to the person who has that edition. A
		// tagged build carries the paid code, so a paid name it does not answer
		// to is a registration that has not landed yet (`graph`, until CG3
		// registers it): the honest answer there is cobra's own "unknown
		// command", which is what the design says the message is for anyway —
		// it appears only in untagged/core builds (§6 of
		// docs/design/ee-boundary-moves.md).
		if Edition() == EditionEE {
			continue
		}
		root.AddCommand(newPaidStub(name))
	}
	registrationsSealed = true
}

// coreNames are the names at the root a registration may not take over: every
// command the root answers to there, by its name and by its aliases, and every
// flag the root itself carries. A registration is judged against them, so the
// rule reads as one sentence: an addition may bring new names and may never
// take over a name the root already has. Names deeper in the tree are not here
// on purpose: a registered command's own subcommand sits under its own path,
// where the same word is a different command.
type coreNames struct {
	rootCommands map[string]bool
	rootFlags    map[string]bool
}

// surface is built by building the bare tree once. Only the names are kept: a
// tree is built fresh for every command the binary runs, so no tree may be
// held here.
var surface *coreNames

func coreSurface() *coreNames {
	if surface != nil {
		return surface
	}
	names := &coreNames{rootCommands: map[string]bool{}, rootFlags: map[string]bool{}}
	root := newCoreRoot()
	// The tree a build hands to main is the one cobra executes, and cobra adds
	// its help and completion commands and the root's version flag as it
	// executes. A registration is judged against that tree rather than against
	// the one this constructor returns, so they are asked for here: an alias,
	// `help` and `completion` are names the CLI answers to like any other.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()
	// Cobra adds its two hidden shell-completion commands as it executes a
	// tree rather than when one is built, so they are not among the commands
	// above: a registration under either name would join the tree beside them
	// and be found in the order added, which a name the shell completion
	// script calls directly must never mean. They are names the CLI answers to
	// like `help` and `completion`, so they are written down here.
	names.rootCommands["__complete"] = true
	names.rootCommands["__completeNoDesc"] = true
	for _, child := range root.Commands() {
		names.rootCommands[child.Name()] = true
		for _, alias := range child.Aliases {
			names.rootCommands[alias] = true
		}
	}
	root.LocalFlags().VisitAll(func(f *pflag.Flag) { names.rootFlags[f.Name] = true })
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) { names.rootFlags[f.Name] = true })
	surface = names
	return names
}

var (
	// registrations are the commands a build registered, by name. They are
	// constructors rather than commands because a tree is built fresh for
	// every command the binary runs, and a cobra command belongs to one tree.
	registrations = map[string]func() *cobra.Command{}
	// registeredNames is every name and alias the registrations above brought
	// to the root. An earlier registration's names are a name the tree answers
	// to as surely as a core one is, so a later registration may add neither:
	// cobra finds a child by name or alias in the order commands were added,
	// and the later one would be reached after the earlier rather than instead
	// of it.
	registeredNames = map[string]bool{}
	// registrationsSealed is set when a tree is built: a registration that
	// arrives after one would never reach a tree, so it is refused loudly
	// rather than accepted and ignored.
	registrationsSealed bool
	// launchDeclarer is the build's declarer; launchDeclarerSet is what makes
	// SetLaunchDeclarer refuse a second call. Nil is the untagged build's
	// state, and it means "declare nothing" rather than "declare an empty
	// record" (D535).
	launchDeclarer    LaunchDeclarer
	launchDeclarerSet bool
	// launchPlanner is the build's A/B planner, and launchPlannerSet what
	// makes RegisterLaunchPlanner refuse a second call. Nil is the untagged
	// build's state, and it means "sample nothing" (D550).
	launchPlanner    LaunchPlanner
	launchPlannerSet bool
	// recipeManifests are the build's manifest builders, by catalog recipe
	// name. Empty is the untagged build's state, and it means "no launch writes
	// a manifest" (D550).
	recipeManifests = map[string]RecipeManifest{}
)
