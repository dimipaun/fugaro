package cli

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/settings"
)

// This file is the after-write hook's implementation (C1's afterWriteFunc,
// docs/contracts/config-ownership.md §3, D2602): the stages a semantic command's
// write owes, run in-process through the registered list, so that a command that
// changes what the published node or the project layer holds leaves the change
// live rather than leaving the owner a second command to run.
//
// The hook is not a second converge. It runs the stages the write needs and no
// others (D2615), through stagesNamed — the same registered stages `fugaro
// update` runs (composite AP2) — over the config the command just wrote, with
// the command's own door so every line it prints and every record it files
// names the command the person ran. `accounts add` is the shape it follows
// (accounts_add_converge.go, spec §2.5): the stages are invoked here rather than
// forked as a child, because a child would re-run that command's whole list
// behind its own confirmation.
//
// The hook is a no-op until the write has landed, and it may not be read as
// part of the write: a stage that fails leaves the command's own change in
// place — the change is written and it is saved — and the command ends on one
// sentence saying so (afterWriteNotLive, D2614). Nothing here rolls a write
// back, and nothing here asks a question: the run this file opens carries its
// own answer and reads no input, because a command that already wrote has
// nothing left to ask.

// init is what wires this file into C1's seam. It is an init function and not
// an assignment at the var itself: after_write_stub.go's init assigns the no-op
// until I1 deletes that file, and a package's files run their inits in file-name
// order, so an init here sorts after the stub's and wins. A var initialiser runs
// before every init in the package and would be silently reset by the stub.
func init() { runAfterWrite = publishAfterWrite }

// publishAfterWrite is the hook: the stages the change owes, in process, and
// the one sentence the command ends on when one of them does not leave the
// change live.
func publishAfterWrite(ctx context.Context, cmd *cobra.Command, change afterWriteChange) error {
	names := afterWriteStages(change)
	if len(names) == 0 {
		return nil
	}
	sel, lc, err := afterWriteSelection(cmd)
	if err != nil {
		return afterWriteNotLive(change)
	}
	// The run's own door is the command that called: it names the run on every
	// line it prints and it is the command the audit record files (the door's
	// own reason is accountsAddRunStages'), and it is deliberately not `fugaro
	// update` — the gate's D2097 exemption for one command's own hand
	// (accounts_alias.go) is that command's and is not extended to every
	// command by this one.
	o := &initOptions{door: managedApplySubcommand(cmd), yes: true, nonInteractive: true, asJSON: true}
	// The run reads nothing and asks nothing, from here on: the hook runs after
	// the command's write, where every question is answered already, and a
	// confirmation that waited on this command's input would be a command that
	// hangs on a prompt the person has no reason to expect (docs/no-prompt-policy,
	// D1540). The stdin is put back before this returns, so the command that
	// called keeps whatever it held.
	stdin := cmd.InOrStdin()
	cmd.SetIn(strings.NewReader(""))
	defer cmd.SetIn(stdin)
	// The command's own stdout is the answer of the command that called, and a
	// stage prints its diff and its banner there (accountsAddRunStages' own
	// swap): for the length of the run they are the run's lines and they go to
	// stderr, under which newInitRun routes the run's own writer too.
	answer := cmd.OutOrStdout()
	cmd.SetOut(cmd.ErrOrStderr())
	defer cmd.SetOut(answer)
	r := newInitRun(cmd, o)
	r.setProject(lc)
	// The stages of a subset are a slice of an update (stagesNamed), and the
	// slice `fugaro update` runs them in takes the workdir lock: the layer stage
	// writes the machine's own layer file.
	unlock, err := r.lockWorkdirs(lc.GCPProject)
	if err != nil {
		return afterWriteNotLive(change)
	}
	defer unlock()
	spec, err := installOptions(o, lc)
	if err != nil {
		return afterWriteNotLive(change)
	}
	e := &initEngine{r: r, lc: lc, spec: spec, path: sel.Path}
	stages, _ := stagesNamed(e, names...)
	// A coding agent's session is the one session the gate is read for
	// (D1016): it is what the loop's refusal is replaced by, per stage, and a
	// person at their own terminal is asked nothing the gate stood in for. The
	// read is made here rather than by e.managedStage because that helper asks
	// `fugaro update`'s door, and this run's door is the command that called.
	if agentMarker(os.Getenv, r.stdinTerminal()) != "" {
		r.gate = afterWriteGate(ctx, lc, o.door, r.stdinTerminal())
	}
	opts := e.options()
	// The gate stands in for the stages it judges and for those only: the same
	// two conditions initEngine.managedStage asks, with the door and the
	// plan-only flag already settled by this run's own construction.
	opts.ManagedGate = func(name string) bool { return gatedStages[name] && r.gateUnderway() }
	res, err := initflow.Run(ctx, stages, opts)
	if err != nil || res.Failed != nil || len(res.Left) > 0 {
		// Every way a stage can stop short is one answer for the person: the
		// change is saved and it is not live. What stopped it is the run's own
		// line, printed above by the stage that stopped, and the sentence below
		// is the one thing the command itself says about it (D2614).
		return afterWriteNotLive(change)
	}
	return nil
}

// afterWriteSelection is the installation the write landed in, which is the one
// the stages must run over: the config the command was aimed at, taken from the
// command's own --config and --project, so a write aimed at another
// installation converges that installation's node rather than this machine's
// default one. A command whose family takes neither flag has neither to read
// and resolves as settingsSelection does (the environment, the checkout, the
// single config on the machine).
func afterWriteSelection(cmd *cobra.Command) (localcfg.Selection, *localcfg.Config, error) {
	var o settingsWriteOptions
	if f := cmd.Flags().Lookup("config"); f != nil {
		o.config, _ = cmd.Flags().GetString("config")
	}
	if f := cmd.Flags().Lookup("project"); f != nil {
		o.project, _ = cmd.Flags().GetString("project")
	}
	return settingsAimedSelection(o)
}

// afterWriteStages is the stages a write owes, from the registry row of the key
// that was written and the store the command wrote it in (D2615).
//
// The row decides what the key IS and the store decides where this write
// landed, and both are needed: a pool's store is `local` — the command writes
// the machine's own config — while the published node is what serves it, and one
// {key, store} pair cannot say "both layers" on its own. Neither can the store
// alone say it: a command may write the project layer file for a key whose row
// also states the local one.
//
// What each stage is owed for:
//
//   - the policy stage owns the published node. A write is the node's business
//     when the row states the cloud layer (`policy`, which CR1 adds to the rows
//     the node serves), when the row is one the machine's own config carries
//     (`local`, `shared`) — the node is derived from that config, so a value the
//     node serves lives there for a key the node serves, which is what makes a
//     pool's write a publish — or when the command wrote the node itself;
//   - the layer stage owns the project layer file, so a row that states the
//     project layer, or a write that landed in it.
//
// A key with no row at all runs nothing. The registry is the one place a key is
// known, and a key nothing there answers for is one no stage can be said to
// serve: running the update's stages for it would be this hook deciding about a
// key the program has never heard of, which is the fail-closed direction read
// the wrong way round — the hook is not a gate and owes nothing it cannot name.
//
// A row whose key varies (`pools.*.members` is written at `pools.dev.members`)
// is matched by its pattern, the way the read resolves one, and the key the
// write landed on is the concrete spelling: a hook that knew only the exact
// spellings would run no stage for a pool, which is the row D2615 is explicit
// about. Two patterns that match one key are settled by the more specific of
// them — the row that varies in fewer segments — and a function of the registry
// rather than of the order the map happens to hand its rows out in.
//
// The names come back for stagesNamed, which orders them: both stages are
// named here in no order of their own, because initflow's order is the run's.
func afterWriteStages(change afterWriteChange) []string {
	if row, ok := settings.Lookup(change.Key); ok {
		return afterWriteStagesFor(row, change.Store)
	}
	best, found, varied := settings.Row{}, false, 0
	for _, row := range settings.Rows() {
		if !settingsPatternMatch(row.Key, change.Key) {
			continue
		}
		if n := strings.Count(row.Key, "*"); !found || n < varied || (n == varied && row.Key < best.Key) {
			best, found, varied = row, true, n
		}
	}
	if !found {
		return nil
	}
	return afterWriteStagesFor(best, change.Store)
}

// afterWriteStagesFor is afterWriteStages' rule over one row and one store, and
// it is a function of its own so the rule can be read (and held to a case) for a
// row no command writes today: no row carries both the project layer and the
// local one, so the arm that owes both stages is reachable only from here.
func afterWriteStagesFor(row settings.Row, store settings.Store) []string {
	policy := store == settings.StoreNode ||
		slices.Contains(row.Layers, settings.LayerPolicy) ||
		slices.Contains(row.Layers, settings.LayerLocal) ||
		slices.Contains(row.Layers, settings.LayerShared)
	layer := store == settings.StoreProject || slices.Contains(row.Layers, settings.LayerProject)
	var names []string
	if policy {
		names = append(names, initflow.Policy)
	}
	if layer {
		names = append(names, initflow.Layer)
	}
	return names
}

// afterWriteGate reads the managed project's marks for this run, the way the
// door whose stage is being run reads them (readUpdateGate): the two marks, once,
// with the command that called as the record's own command.
//
// Every way of not being able to ask leaves the zero gate behind, which is
// initGate.underway's "not managed": a session that cannot be told the project
// is managed keeps every confirmation it had, so a mark that cannot be read is
// never a reason to proceed (settingsWriteGate's answer, and readUpdateGate's).
func afterWriteGate(ctx context.Context, lc *localcfg.Config, subcommand string, terminal bool) *initGate {
	g := &initGate{}
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return g
	}
	// The budget database is left out, as readUpdateGate leaves it: the ownership
	// question (D1017) is the stage's own, asked where the database is in hand.
	gate, err := readManagedGate(ctx, managedGateEnv{lc: lc, s: c.Storage, crm: c.CRM}, managedGateFacts{
		Subcommand:  subcommand,
		Argv:        os.Args[1:],
		Declined:    "nothing was published",
		AgentMarker: agentMarker(os.Getenv, terminal),
	})
	if err != nil {
		return g
	}
	g.g, g.managed = gate, gate.managed
	return g
}

// afterWriteNotLive is the one sentence a command ends on when the change it
// wrote is not live (D2614): the write stays, the command exits non-zero, and
// this is the whole of what it says about it. It is the one helper AP3 repoints
// when the command that applies a pending change exists (composite D1563's own
// sentence names `fugaro apply`, which this release does not have), so the
// wording lives here once and is never spelled a second time at a call site.
func afterWriteNotLive(change afterWriteChange) error {
	return userErr("%s is saved and is not live yet: run `%s update`, which publishes it",
		safetext.Field(change.Key), selfCommand())
}
