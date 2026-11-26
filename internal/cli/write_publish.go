package cli

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
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

// init is what wires this file into C1's seam, and it is an init function
// rather than an initialiser at the var itself: a var initialiser is read
// before every init of the package, so a file whose init assigns the seam
// silently overwrites it, and the seam's value would be decided by the order
// the files are compiled in. That is exactly what C1's stub was, and the plan
// itself says which way it fell: this file sorts after `after_write_stub.go`, so
// the real hook won from the moment N3 landed and the stub's no-op was the
// dead half. It is gone now (I1 deleted it) and one assignment is left, in this
// file's init, which is what TestNoStubSurvives reads: the seam is one variable
// every semantic command reaches through, so a second assignment anywhere in
// the package takes the hook away from all of them at once.
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
		// No installation is known here, so no record of the steps could be
		// filed under one (afterWriteOwed): the sentence is the one every arm
		// ends on and the command it names reads the record of the shell this
		// write was made from.
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
	// D1540).
	//
	// What is emptied is the reader the run itself reads from (initRun.in, which
	// every confirmation and every value entry goes through) and not the
	// command's own stdin: whether a person is there is read off that input all
	// over the loop, and an emptied one answers "nobody" — which agentenv.Session
	// turns into a coding agent's session for the Claude Code IDE extension's own
	// marker alone (CLAUDE_CODE_SSE_PORT is set in a person's integrated
	// terminal, where the person typing `fugaro settings set` is not an agent).
	// A hook that took the command's input away would refuse that person as an
	// agent and end their write not live, which is the one outcome this hook
	// exists to prevent; the run gets an empty reader instead, and the one fact
	// about who is there is read off the input as it arrived, before it is
	// handed over.
	terminal := stdinIsTerminal(cmd.InOrStdin())
	// The command's own stdout is the answer of the command that called, and a
	// stage prints its diff and its banner there (accountsAddRunStages' own
	// swap): for the length of the run they are the run's lines and they go to
	// stderr, under which newInitRun routes the run's own writer too.
	answer := cmd.OutOrStdout()
	cmd.SetOut(cmd.ErrOrStderr())
	defer cmd.SetOut(answer)
	r := newInitRun(cmd, o)
	r.in = strings.NewReader("")
	r.setProject(lc)
	// The stages of a subset are a slice of an update (stagesNamed), and the
	// slice `fugaro update` runs them in takes the workdir lock: the layer stage
	// writes the machine's own layer file.
	unlock, err := r.lockWorkdirs(lc.GCPProject)
	if err != nil {
		return afterWriteOwed(lc, change, names)
	}
	defer unlock()
	spec, err := installOptions(o, lc)
	if err != nil {
		return afterWriteOwed(lc, change, names)
	}
	e := &initEngine{r: r, lc: lc, spec: spec, path: sel.Path}
	stages, unanswered := stagesNamed(e, names...)
	// A coding agent's session is the one session the gate is read for
	// (D1016): it is what the loop's refusal is replaced by, per stage, and a
	// person at their own terminal is asked nothing the gate stood in for. The
	// read is made here rather than by e.managedStage because that helper asks
	// `fugaro update`'s door, and this run's door is the command that called.
	if agentMarker(os.Getenv, terminal) != "" {
		r.gate = afterWriteGate(ctx, lc, o.door, terminal)
	}
	opts := e.options()
	// The loop is told the session read above and not a second reading of it:
	// e.options derives the pair from the run's command, which this hook leaves
	// where it found it, and the gate's own read was given the same value — so
	// the session the gate judges and the session the loop applies for cannot
	// come to be two different ones.
	opts.Terminal, opts.Agent = terminal, agentMarker(os.Getenv, terminal)
	// The gate stands in for the stages it judges and for those only: the same
	// two conditions initEngine.managedStage asks, with the door and the
	// plan-only flag already settled by this run's own construction.
	opts.ManagedGate = func(name string) bool { return gatedStages[name] && r.gateUnderway() }
	res, err := r.runStages(ctx, stages, opts)
	if err != nil || res.Failed != nil || len(res.Left) > 0 {
		// Every way a stage can stop short is one answer for the person: the
		// change is saved and it is not live. What stopped it is the run's own
		// line, printed above by the stage that stopped, and the sentence below
		// is the one thing the command itself says about it (D2614).
		return afterWriteOwed(lc, change, afterWriteKept(names, unanswered, res))
	}
	return nil
}

// afterWriteKept is the steps a run that stopped short did not answer, in the
// command's own order: every step whose stage left the change live is answered
// and every other one is owed, which is the rule applyRunRecord settles a chain
// and `fugaro apply` settles a record by (applyStepAnswered, D1563).
//
// A step this door registers no stage under — a catalog image's build, which is
// not a stage at all, and the services slot the hook's own list does not carry —
// is owed and not dropped: a run that quietly did fewer steps than it was asked
// for is the one thing neither the record nor the command may do
// (applyRunRecord's own rule).
func afterWriteKept(names, unanswered []string, res *initflow.Result) []string {
	states := map[string]initflow.StageResult{}
	if res != nil {
		for _, st := range res.Stages {
			states[st.Name] = st
		}
	}
	var kept []string
	for _, n := range names {
		if slices.Contains(unanswered, n) || !applyStepAnswered(n, states[n], applyOptions{chain: true}) {
			kept = append(kept, n)
		}
	}
	return kept
}

// afterWriteOwed records the steps this run did not answer and ends the command
// on the one sentence (D2614).
//
// The record is what makes that sentence true: `fugaro apply` reads this
// machine's own record and runs the steps in it, and the wording names that
// command rather than `fugaro update` since AP3 landed it — the repoint
// docs/contracts/config-ownership.md §7 asks for, in the one place that file
// keeps the wording (afterWriteNotLive). The change is the subject and never a
// value (D1570), and the record is written under the same project name and GCP
// project ID the chain writes its own (chainSave).
//
// A record that cannot be written is not swallowed: the command says so, as
// chainApply says it, because a step nobody would run is worse than a sentence
// that reads wrong.
func afterWriteOwed(lc *localcfg.Config, change afterWriteChange, kept []string) error {
	if len(kept) > 0 {
		if _, err := chainSave(lc, []apply.Change{{Kind: apply.Setting, Key: change.Key}}, kept); err != nil {
			return err
		}
	}
	return afterWriteNotLive(change)
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

// afterWriteStages is the stages a write owes, from the change that was written
// and the store the command wrote it in (D2615, and apply.WriteSteps is the
// rule itself).
//
// It was the hook's own rule until composite AP5 gave the settings commands
// their own chain: the two now ask one function, so what `settings set` queues
// and says it owes and what this hook runs cannot come to be two answers
// (D1561). The row decides what the key IS and the store decides where this
// write landed, and both are needed: a pool's store is `local` — the command
// writes the machine's own config — while the published node is what serves it,
// and one {key, store} pair cannot say "both layers" on its own. Neither can
// the store alone say it: a command may write the project layer file for a key
// whose row also states the local one.
//
// A write into a checkout's `fugaro.yaml` owes nothing at all: the branch's
// merge is what makes it live (D1564). A key with no row in the registry and no
// row in the table runs nothing — the registry is the one place a key is known,
// and running the update's stages for a key the program has never heard of
// would be this hook deciding about it, which is the fail-closed direction read
// the wrong way round.
func afterWriteStages(change afterWriteChange) []string {
	return apply.WriteSteps(apply.Change{Kind: apply.Setting, Key: change.Key}, change.Store)
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
// this is the whole of what it says about it. The wording lives here once and
// is never spelled a second time at a call site, which is what
// TestAfterWriteSentenceIsOneHelper holds.
//
// It names `fugaro apply`, the command that runs what a change still owes, and
// not `fugaro update`: AP3 landed `apply`, and docs/contracts/config-ownership.md
// §7 is where this one helper was left to be repointed the day it did. The
// sentence is true because the arm that reaches it recorded the steps first
// (afterWriteOwed), and `fugaro apply` reads exactly that record.
func afterWriteNotLive(change afterWriteChange) error {
	return userErr("%s is saved and is not live yet: run `%s apply` to finish the steps it can answer",
		safetext.Field(change.Key), selfCommand())
}
