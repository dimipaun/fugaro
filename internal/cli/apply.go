package cli

// apply.go is `fugaro apply` (docs/composite-commands, AP3) and the run every
// management command's chain is made of (AP4): the follow-up steps a change
// owes, run in process by the command that wrote the change, or later by the
// one command that reads what was saved.
//
// A management command writes its change and then runs the steps that make the
// change live — publishing the project policy, redeploying the checkout's
// repository, building a catalog image — and `--no-apply` is the one word that
// saves the change and stops (D1565). What the flag leaves behind is a local
// record of the steps owed (internal/apply, AP1): this command is what reads it
// and runs it later (D1560, D1562), and runChangeChain is what a management
// command runs in process, so a person who typed none of this has nothing left
// to run.
//
// The steps are the initflow stages, run as a named subset through the same
// loop every door uses (stagesNamed, AP2), plus the one step that is not a
// stage: the billable catalog image build (D1577). The record is a hint and
// never authority (D1575) — every step checks the state first and does nothing
// when there is nothing to do — so a record that is stale, queued twice or
// carried here from another installation can waste a step's time but never its
// outcome.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// applyName is the word this command is, in both places it is named: the log a
// run is filed under (D1351) and the door the loop prints its lines for. The
// two are the same string because the name a person types is the name every
// line has to use (D1560).
const applyName = "apply"

// applyLong is the command's help: what it runs, in what order, and what a
// step that fails leaves behind.
const applyLong = `Run the follow-up steps a management change saved for later.

A management command (settings set, image add, models rm, workspace add, ...)
writes its change and then runs the steps that make it live: publishing the
project policy, redeploying the checkout's repository, building a catalog
image. A step its own run did not finish stays owed, and this command runs
what is owed.

Every owed step runs once, in the installation's own order, whatever queued it
and however many changes asked for the same one. Each step checks the state
first and does nothing when there is nothing to do, so running this command
again costs nothing. A step that fails leaves its change saved and the step
owed: this command says so and exits non-zero, and running it again finishes
the job.

With --status it prints what is owed and changes nothing.
`

// applyOptions are the command's flags.
type applyOptions struct {
	// status is --status: print what is owed and run nothing. It writes
	// nothing either, which is what makes it the one read the console is
	// admitted to (D1569).
	status  bool
	asJSON  bool
	config  string
	project string
}

func newApplyCmd() *cobra.Command {
	var o applyOptions
	cmd := &cobra.Command{
		Use:   "apply [--status]",
		Short: "Run the follow-up steps a management command saved for later",
		Long:  applyLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runApply(cmd, o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.status, "status", false, "print the steps that are owed and change nothing")
	f.BoolVar(&o.asJSON, "json", false, "print the answer as a JSON document")
	f.StringVar(&o.config, "config", "", "the project config file to use (default: the project's own under $XDG_CONFIG_HOME/fugaro/projects)")
	f.StringVar(&o.project, "project", "", "the Fugaro project to act on (default: the checkout's fugaro.yaml project:, else $FUGARO_PROJECT, else the only project config)")
	return cmd
}

// addNoApplyFlag registers --no-apply on cmd and answers whether it was passed.
//
// One helper rather than a flag each command spells for itself (D1565): every
// management command writes its change and then runs the steps that make it
// live, and the flag has to be the same word, the same help line and the same
// behaviour everywhere — two spellings of "save this and stop" are two
// behaviours the day one of them is changed. `fugaro apply` runs what it leaves
// behind, and nothing else does.
func addNoApplyFlag(cmd *cobra.Command) *bool {
	return cmd.Flags().Bool("no-apply", false,
		"save the change and run none of its follow-up steps: fugaro apply runs them later")
}

// applyStatusVersion is the version of this command's JSON document. The
// console reads `fugaro apply --status --json` through the fence (D1569), so
// the shape is a contract: a field is added and never repointed, and the
// document carries the version it was written as (F9,
// schemas/apply-status.schema.json).
const applyStatusVersion = 1

// applyDoc is the JSON answer of every arm of this command: what is owed, and —
// for a run — what it ran.
type applyDoc struct {
	Version int `json:"version"`
	// Project and GCPProject are the installation this answer is about, and
	// they are the pair every entry of the record is bound to (D1575).
	Project    string `json:"project"`
	GCPProject string `json:"gcp_project"`
	// Recorded says whether a record of what is owed is on disk at all. A
	// machine that never made a change and one whose record was deleted read
	// the same — nothing is owed — and this is what tells the two apart in the
	// answer (D1575).
	Recorded bool `json:"recorded"`
	// Current says whether the record was stamped against the project config as
	// it stands. False means the config changed since the last apply, so what
	// the record owes may be older than the config, or newer than it.
	Current bool `json:"current"`
	// Pending is what is still owed, in the order it runs, and it is written
	// even when it is empty: `[]` is "nothing is owed", which is a different
	// answer from a document that could not be read.
	Pending []applyRow `json:"pending"`
	// Applied is the steps a run answered, in the order they ran. It is absent
	// for --status, which runs none, and never present-and-empty for a run: a
	// run that answered nothing has nothing to report as applied.
	Applied []string `json:"applied,omitempty"`
}

// applyRow is one owed step as the document holds it: the step, and the change
// that asked for it. It is the change's subject only and never a value (D1570).
type applyRow struct {
	Step string `json:"step"`
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

func runApply(cmd *cobra.Command, o applyOptions) error {
	sel, lc, err := settingsAimedSelection(settingsWriteOptions{config: o.config, project: o.project})
	if err != nil {
		return err
	}
	if o.status {
		pending, recorded, err := loadPendingSteps(lc)
		if err != nil {
			return err
		}
		return printApplyStatus(cmd, lc, pending, recorded, applyCurrent(pending, recorded, sel.Path), o.asJSON)
	}
	// The run reads the record itself, and only once it holds the workdirs
	// lock: reading it here would be reading a copy a run that may take minutes
	// would then write back whole (applyRunSteps).
	return applyRunSteps(cmd, lc, sel.Path, o)
}

// applyRecordRead is where a run reads the record, and it is a variable for one
// reason: a run reads the record twice on purpose (applyRunSteps), and the fact
// the second read exists for — a change that landed while the run was going —
// is one no test can otherwise make happen between the two.
var applyRecordRead = loadPendingSteps

// loadPendingSteps reads what this machine owes for lc's project, and answers
// whether a record is on disk at all.
//
// A machine that never made a change and one whose record was deleted are the
// same read (LoadPending answers an empty document and no error for a missing
// file, D1575), and the two are told apart by what the answer says rather than
// by a second store: the record is a hint, and what says whether anything was
// owed is the stamp beside it.
func loadPendingSteps(lc *localcfg.Config) (*apply.Pending, bool, error) {
	path, err := apply.PendingPath(os.Getenv, lc.Name)
	if err != nil {
		return nil, false, userErr("%v", err)
	}
	_, statErr := os.Stat(path)
	recorded := statErr == nil
	p, err := apply.LoadPending(os.Getenv, lc.Name, lc.GCPProject)
	if err != nil {
		// A record bound to another project or another installation is refused
		// here rather than run: its steps would be run against an installation
		// the change was never made for (D1575).
		return nil, recorded, userErr("%v", err)
	}
	return p, recorded, nil
}

// applyCurrent reports whether the record was stamped against the project
// config as it stands now (D1575).
func applyCurrent(p *apply.Pending, recorded bool, path string) bool {
	return recorded && p.LastApply != "" && p.LastApply == applyConfigStamp(path)
}

// applyConfigStamp is the stamp Pending.LastApply holds: the hash of the local
// project config's bytes at the last apply that finished. A project selected
// without a file of its own has no bytes to stamp and answers "", which is no
// stamp rather than a stamp of nothing.
func applyConfigStamp(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// owedRun is one run of the owed steps as its two doors read it: what the run
// was asked, what it answered, and what it left owed. The doors print their own
// answer from it — `fugaro apply` its document, its kept line or its `applied`
// line, a management command the sentence that says its change is not live yet
// — so the run itself decides nothing about what a person reads (spec rule 4).
type owedRun struct {
	// started is whether the run reached the loop at all: false means it
	// stopped before any step, and the error beside it is the run's own — the
	// workdirs lock, the record, the managed gate's read — rather than a
	// step's. A door prints nothing of its own about the steps then.
	started bool
	// buildFailed is the catalog build's own arm, which returns ahead of the
	// loop: nothing ran, and the record is the one the run read.
	buildFailed bool
	// pending and recorded are the record as the run read it, and whether a
	// document was on disk at all; after is the record as it stands at the end
	// of the run, with wrote saying whether this run wrote it and current
	// whether it is stamped against the project config as it stands now (D1575).
	pending  *apply.Pending
	recorded bool
	after    *apply.Pending
	wrote    bool
	current  bool
	// ran is the steps this run answered and kept the steps it could not, in
	// the order the record gave them.
	ran  []string
	kept []string
}

// nothing is a run that owed nothing at all (D1568): it reached the record, ran
// no step and kept none, so it has no answer about a step to print. A run that
// owed steps answers every one of them one way or the other, which is what
// makes "ran none and kept none" the empty record and nothing else.
func (run owedRun) nothing() bool {
	return run.started && !run.buildFailed && len(run.ran) == 0 && len(run.kept) == 0
}

// runOwedSteps is one run of the steps this machine owes, from the workdirs lock
// to the settled record: the whole of what a management command's chain does,
// and the same run `fugaro apply` makes of the record a person left for later
// (spec rule 1, D1561, D1571).
//
// The record is read once the workdirs lock is held and settled against the one
// on disk when the run ends, never against a copy read before either. A run is
// minutes of Terraform, and a record read early and written back whole would
// drop a step another command queued while it was going — the change would read
// as applied, and never be published (D1563). Two reads are what keep that
// apart: the first says what to run, and the second, after the run, holds
// whatever was queued meanwhile, of which only the entries still identical to
// the ones read are cleared (apply.ClearUnchanged).
//
// The door is `apply` whichever command opened the run, and deliberately so:
// the door is what enrolling, the managed gate and the account are keyed on
// (gateable, initOptions.enrolling), a chain must behave as this command does —
// it converges the installation as it is, it never enrols a repository by
// asking (D1574), and it runs under the gate this command's own steps run under
// (D1571) — and `fugaro apply` is the command D1563 names for whatever is left
// owed. What a command that runs its own chain adds is its own sentences (spec
// rule 4).
//
// What comes back is the run as a door prints it and the error of what failed:
// the run's own error before the first step, a stage's own failure, the line
// the loop left for the person, or the catalog build's. A step left owed is not
// an error here — it is in the run — because the two doors say it differently.
func runOwedSteps(cmd *cobra.Command, lc *localcfg.Config, path string, asJSON bool) (owedRun, error) {
	io := &initOptions{door: applyName, yes: true, nonInteractive: true, asJSON: asJSON}
	r := newInitRun(cmd, io)
	r.setProject(lc)
	// The workdirs are the converge's, whichever door converged them, and the
	// redeploy is one of the stages that takes them: this door takes the lock
	// too, where the project is known and before the loop prepares anything.
	unlock, err := r.lockWorkdirs(lc.GCPProject)
	if err != nil {
		return owedRun{}, err
	}
	defer unlock()
	// Read here and not before the lock: what this run settles is the record as
	// it stands while this command is the machine's writer of it. It is read
	// before the reporter below as well: a run that owes nothing runs no step,
	// and it is a read, not a run — one that files no log (ReportBorrowed).
	pending, recorded, err := applyRecordRead(lc)
	if err != nil {
		return owedRun{}, err
	}
	if len(pending.Names()) == 0 {
		// Nothing owed (D1568): the door that ran answers it, and a chain
		// never reads an empty record here — the command that opened it
		// queued its change's steps before this run started.
		return owedRun{started: true, pending: pending, recorded: recorded}, nil
	}
	// The run's account is the reporter's, filed under the name of the command
	// a person ran (D1351, D1354) — `apply` for this door, `image-add` for a
	// chain — and never under the door's: the steps it runs are the loop's, and
	// a command that ran a converge without a reporter of its own would print a
	// second account of the same run beside the one the census expects.
	run := stepRunFor(cmd, runLogName(cmd), cmd.ErrOrStderr())
	defer run.close()
	// The reporter is this command's: the steps print one line each through it,
	// and the closing line is printed where this command's own run ends
	// (ReportBorrowed below). A converge invoked here with a reporter of its
	// own would file a log this command never names.
	r.rep, r.repLog = run.rep, run.log
	spec, err := installOptions(io, lc)
	if err != nil {
		return owedRun{}, err
	}
	old, _ := os.ReadFile(path)
	e := &initEngine{r: r, lc: lc, spec: spec, path: path, old: old}
	// The run's own cover, for the gate: this door builds no review (a chain
	// asks nothing), so the plan of a step it runs is judged by the cover the
	// review would have printed rather than by a step that had none
	// (init_review.go's coverOf).
	r.cover = e.cover
	// The managed project's read, once for the whole run, and before the first
	// step: a chain's steps are the same stages under the same gate
	// (gateable), so a managed project's owner and a coding agent's session
	// reach them exactly as `fugaro update` does, and each step that writes is
	// recorded (D1014, D1545). A project that is not managed keeps every
	// refusal it has, which is what the steps below answer with.
	if err := e.gateRun(cmd.Context()); err != nil {
		return owedRun{}, err
	}

	names := pending.Names()
	var ran, kept []string
	// A stage that prints to cmd.OutOrStdout() directly — the policy stage's
	// publish diff is the loudest — would land on the stdout this command's
	// answer is on, and --json answers one document and no line of anything else
	// (G2). So the command is pointed at stderr for the length of the run and
	// its own writer goes on the answer. The catalog build below runs inside
	// that same window: whatever it writes where the command's output is — the
	// notice its no-catalog arm answers with, a step line — is not the answer.
	answer := cmd.OutOrStdout()
	cmd.SetOut(cmd.ErrOrStderr())
	// The catalog image is the one step that is not a stage, and apply.Order
	// puts it first (D1577): the image is what the project's jobs run, so a run
	// that cannot build it must not go on to publish the policy that names it.
	// A build that fails leaves every step owed, this one included, so running
	// this command again finishes the whole job.
	if slices.Contains(names, apply.CatalogBuild) {
		if err := applyCatalogBuild(cmd, lc, run); err != nil {
			// This arm returns ahead of the loop, so nothing ran and the
			// answer a door prints is the record as it was read, with every
			// step still owed — the build's own error goes back beside it. The
			// redirect above is undone first: what the door prints is the
			// answer, and the answer is what the command's own output is.
			cmd.SetOut(answer)
			return owedRun{buildFailed: true, started: true, pending: pending, recorded: recorded, kept: names}, err
		}
		ran = append(ran, apply.CatalogBuild)
	}

	stageNames := slices.DeleteFunc(slices.Clone(names), func(n string) bool { return n == apply.CatalogBuild })
	stages, unanswered := stagesNamed(e, stageNames...)
	opts := e.options()
	opts.ReportBorrowed = true
	res, runErr := initflow.Run(cmd.Context(), stages, opts)
	cmd.SetOut(answer)

	states := map[string]initflow.StageResult{}
	if res != nil {
		for _, st := range res.Stages {
			states[st.Name] = st
		}
	}
	for _, n := range stageNames {
		if slices.Contains(unanswered, n) {
			// A name this door registers no stage under — the services slot,
			// the Batch backend's compute, the project stage, or a name that is
			// no stage at all. It stays owed rather than being dropped: a run
			// that quietly did fewer steps than it was asked for is the one
			// thing neither the record nor this command may do.
			kept = append(kept, n)
			continue
		}
		if n == initflow.Repository && states[n].State == initflow.Skipped {
			// The redeploy is per repository (D1574): a checkout that is not
			// this repository's — or a machine that has no checkout of it at
			// all — cannot redeploy it, and the step stays owed with the
			// sentence that says where to run this command.
			kept = append(kept, n)
			continue
		}
		if applyAnswered(states[n]) {
			ran = append(ran, n)
			continue
		}
		kept = append(kept, n)
	}

	// The record is written before the answer, and it is written from the one on
	// disk now rather than the copy read above: the steps this run answered are
	// cleared where they are still the entries that were read, so a step queued
	// while the run was going stays owed (D1563) — and the stamp is written only
	// when nothing at all is left, so a change queued meanwhile never reads as
	// applied to it (D1575). A run that answered everything stamps the config as
	// it stands now — a stage may have written it — so the next read can tell a
	// config that changed since from one that did not. A machine that had no
	// record and answered nothing is not given one: nothing was ever owed here,
	// and a write would be this command inventing state.
	after, afterRecorded, err := applyRecordRead(lc)
	if err != nil {
		return owedRun{started: true, pending: pending, recorded: recorded, ran: ran, kept: kept}, userErr("%v", err)
	}
	after.ClearUnchanged(pending, ran...)
	if len(kept) == 0 && runErr == nil && len(after.Steps) == 0 {
		after.LastApply = applyConfigStamp(path)
	}
	wrote := afterRecorded || len(ran) > 0
	if wrote {
		if err := apply.SavePending(os.Getenv, lc.Name, after); err != nil {
			return owedRun{started: true, pending: pending, recorded: recorded, ran: ran, kept: kept}, userErr("%v", err)
		}
	}
	answered := owedRun{
		started: true, pending: pending, recorded: recorded,
		after: after, wrote: wrote, current: applyCurrent(after, wrote, path),
		ran: ran, kept: kept,
	}

	switch {
	case runErr != nil:
		return answered, userErr("%v", runErr)
	case res.Failed != nil:
		// The stage's own error, which is a user error or a remote one already:
		// the fix line names the step, and this command adds nothing to it.
		return answered, res.Cause
	case len(res.Left) > 0:
		return answered, userErr("%v", res.Left[0].Text)
	}
	return answered, nil
}

// applyRunSteps is `fugaro apply`'s own run: the owed steps through
// runOwedSteps, and then this command's answer — its document under --json, the
// kept line, or the line naming what it applied.
func applyRunSteps(cmd *cobra.Command, lc *localcfg.Config, path string, o applyOptions) error {
	run, err := runOwedSteps(cmd, lc, path, o.asJSON)
	switch {
	case !run.started:
		// The run stopped before a step: the refusal or the failure is the
		// whole of what this command says.
		return err
	case run.nothing():
		return applyNothing(cmd, lc, path, run.pending, run.recorded, o)
	case run.buildFailed:
		// --json answers one document on every outcome (G2), and this arm has
		// nothing applied to report: the document is the record as the run read
		// it, with every step still owed.
		if o.asJSON {
			fmt.Fprintln(cmd.OutOrStdout(), string(applyDocJSON(lc, run.pending, run.recorded, applyCurrent(run.pending, run.recorded, path), nil)))
		} else {
			applyKeptLine(cmd.OutOrStdout(), run.kept)
		}
		return err
	}

	switch {
	case o.asJSON:
		fmt.Fprintln(cmd.OutOrStdout(), string(applyDocJSON(lc, run.after, run.wrote, run.current, run.ran)))
	case len(run.kept) > 0:
		applyKeptLine(cmd.OutOrStdout(), run.kept)
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "applied %s\n", strings.Join(run.ran, ", "))
	}

	switch {
	case err != nil:
		return err
	case len(run.kept) > 0:
		return userErr("%s still owed; run `fugaro apply` again to finish the job", strings.Join(run.kept, ", "))
	}
	return nil
}

// runLogName is the name the run's log is filed under (D1351): the command a
// person ran, spelled the way the other commands' logs are (`accounts add`
// files accounts-add) and never the door this run borrowed — `fugaro apply`
// files `apply`, and a management command's chain files its own path. What the
// log keeps is the record of one command line somebody ran.
//
// A chain's name is read off the path a person typed, which no scan of the
// source can see; this door's is its whole path, so it is written out here as
// the literal report_exempt.go's builtLogNames hands the census.
func runLogName(cmd *cobra.Command) string {
	if cmd.Name() == applyName {
		return "apply"
	}
	return strings.Join(strings.Fields(cmd.CommandPath())[1:], "-")
}

// applyNothing is the answer of a run that owes nothing (D1568): exit 0, the
// sanctioned sentence, and — beside it — what this machine can say about the
// record it read (D1575).
func applyNothing(cmd *cobra.Command, lc *localcfg.Config, path string, pending *apply.Pending, recorded bool, o applyOptions) error {
	current := applyCurrent(pending, recorded, path)
	if o.asJSON {
		fmt.Fprintln(cmd.OutOrStdout(), string(applyDocJSON(lc, pending, recorded, current, nil)))
		return nil
	}
	w := cmd.OutOrStdout()
	fmt.Fprintln(w, "Nothing to apply.")
	applyRecordNote(w, recorded, current)
	return nil
}

// applyRecordNote is what keeps a missing or stale record from being silent
// (D1575). A machine that owes nothing and a machine whose record was deleted
// read the same — nothing is owed — so the answer says which of the two it can
// tell apart, and neither sentence promises a comparison this build does not
// make: the stamp beside the record (Pending.LastApply) is read here and
// written here, and the command that recomputes what a config still needs
// against it is not built yet.
func applyRecordNote(w io.Writer, recorded, current bool) {
	switch {
	case !recorded:
		fmt.Fprintln(w, "No record of what this machine owes is on disk: nothing is recorded as owed. A record that was deleted leaves the same answer, and what the project config still needs is not recomputed here.")
	case !current:
		fmt.Fprintln(w, "The project config changed since the last apply, and nothing owed is recorded against it.")
	}
}

// keptLine is D1563's sentence: the change is saved, it is not live yet, and
// `fugaro apply` is the command that finishes it. The steps it names are the
// ones a run could not answer, and that run's own account above says why.
//
// A kept redeploy says where to run it: the repository stage's Terraform root
// is per repository (D1574), so a machine with no checkout of the repository
// the change was made for cannot run that step, and the answer has to say so
// rather than send the person to the same machine again.
//
// A kept workspace-project says where too, for a reason of this command's own:
// its step is a grant confirmed by a typed name, and no run of `fugaro apply`
// can give that word, so the command it names is `fugaro update`.
//
// The names are the record's and not the command's: it is a file in the
// machine's own config directory, nothing fences it, and the reader checks the
// entry's binding and never its name (D1575) — so a name this build registers
// no stage for is kept, and a name can carry an escape sequence that would
// redraw the very line saying the change is not live yet. Each one is escaped
// where it is joined, the way the status table escapes the same names.
//
// tail is the closing line, and it is the one thing the two doors say
// differently: the command that finishes the job, named by the door that ran
// (a chain runs `fugaro apply` in process and has not left the person a second
// command to run for this change).
func keptLine(w io.Writer, kept []string, tail string) {
	names := make([]string, 0, len(kept))
	for _, n := range kept {
		names = append(names, safetext.Field(n))
	}
	fmt.Fprintf(w, "saved, not live yet: %s still owed\n", strings.Join(names, ", "))
	if slices.Contains(kept, initflow.Repository) {
		fmt.Fprintln(w, "Run `fugaro apply` in a checkout of the repository the change was made for: the redeploy is that repository's own Terraform root, and this machine has none of it.")
		return
	}
	// A kept workspace-project is not a step this command can ever answer: its
	// confirmation is a name typed at a prompt (workspace_create.go) and apply
	// runs with no terminal of its own, so "run `fugaro apply` again" would
	// promise a second run that reaches the same wall and says the same thing.
	// The door that asks the question is `fugaro update`, in the owner's own
	// terminal.
	if slices.Contains(kept, initflow.WorkspaceProject) {
		fmt.Fprintln(w, "Run `fugaro update` in your own terminal window, not through a coding agent or a pipe: the workspace's own project confirms this step by the name typed at its terminal, and no flag and no run of this command can give that word.")
		return
	}
	fmt.Fprintln(w, tail)
}

// applyKeptLine is keptLine for `fugaro apply`'s own run, whose steps the
// person may have run through this command already: the same command is named
// as one to run again.
func applyKeptLine(w io.Writer, kept []string) {
	keptLine(w, kept, "Run `fugaro apply` again to finish the job: every step checks the state first, so the ones that already ran cost nothing.")
}

// chainKeptLine is keptLine for a management command's own chain (D1563): the
// command is still `fugaro apply`, and one it has not run for this change yet,
// so the sentence names it without "again" (spec rule 4 keeps the internal
// words out: what a person reads is the command that finishes the job).
func chainKeptLine(w io.Writer, kept []string) {
	keptLine(w, kept, "Run `fugaro apply` to finish the job: every step checks the state first, so the ones that already ran cost nothing.")
}

// queueChange records the steps a change owes in this machine's record, for the
// project and the GCP project the command acted on, and answers them in the
// order a run takes them (D1562, D1570).
//
// A change the table does not declare is refused rather than queued with none:
// the table is the one place a change's steps are declared (spec rule 2), so a
// resource type with no row is a gap in it, and a command that wrote its change
// and queued nothing would leave it looking live with nothing owed.
func queueChange(cmd *cobra.Command, lc *localcfg.Config, change apply.Change) ([]string, error) {
	steps, declared := apply.Steps(change)
	if !declared {
		return nil, userErr("fugaro has no follow-up steps declared for a %s change (%s), so this command cannot say what makes it live: this is a gap in fugaro's own table and not something to work around",
			change.Kind, pluginwire.Printable(change.Key))
	}
	p, err := apply.LoadPending(os.Getenv, lc.Name, lc.GCPProject)
	if err != nil {
		// What the read refused — a document this build cannot parse, or one
		// bound to another project or installation — is dropped, and said,
		// rather than left where it is: the change is written already, and
		// leaving the file alone would refuse every later write of the record
		// until somebody repaired it by hand (SavePending's own direction,
		// D1575).
		fmt.Fprintf(cmd.ErrOrStderr(), "note: what this machine had recorded as owed was not read, so the record starts fresh here: %v\n", err)
		p = &apply.Pending{Version: apply.PendingVersion}
	}
	for _, s := range steps {
		p.Add(apply.PendingStep{Step: s, Project: lc.Name, GCPProject: lc.GCPProject, Change: change})
	}
	if err := apply.SavePending(os.Getenv, lc.Name, p); err != nil {
		return nil, userErr("%v", err)
	}
	return steps, nil
}

// runChangeChain is what a management command does once its change is written:
// it records the steps the change owes and runs them, in process and under
// `fugaro apply`'s own door (spec rules 1 and 3, D1561, D1565, D1571).
//
// In process rather than as a child `fugaro apply`: a child would re-read the
// record and run it behind its own answer, and this command's sentence would
// then be printed over another command's run — the reason the after-write hook
// runs its stages here too (write_publish.go). The steps are the apply run's in
// every other way: the same order, the same gate, the same closure of the
// record (runOwedSteps).
//
// noApply is D1565: the change is saved, its steps are recorded, and nothing is
// run — so the answer is the same sentence saying what is owed, and the exit is
// the plain one, because the command did exactly what it was asked for.
//
// live is what the caller's own sentence hangs on: it is true only when this
// run answered every step of the change, so a command never tells a person
// that its change is live in the same breath as a line saying what is still
// owed.
func runChangeChain(cmd *cobra.Command, lc *localcfg.Config, path string, change apply.Change, noApply bool) (live bool, err error) {
	steps, err := queueChange(cmd, lc, change)
	if err != nil {
		return false, err
	}
	if noApply {
		chainKeptLine(cmd.OutOrStdout(), steps)
		return false, nil
	}
	// The steps run against the config as this command left it and not the copy
	// it read before writing its change. A step's own check reads what is true
	// now (D1575), and the change is the newest thing in that file: handed the
	// copy taken before the write, the catalog build's check reads a project
	// whose catalog is the one this run was aimed at rather than the entry it
	// just added, answers its own step with nothing built (infra.CatalogBuilds,
	// applyCatalogBuild), and the policy step beside it publishes a node the
	// entry is missing from. So the file is read again here, once the write has
	// happened; a file that will not read back is this command's own error and
	// not a fallback to the copy, because no step of the change can be run
	// against a config this build cannot read.
	data, err := os.ReadFile(path)
	if err != nil {
		return false, userErr("reading %s back for the steps this change owes: %v", pluginwire.Printable(path), err)
	}
	fresh, err := localcfg.Parse(data)
	if err != nil {
		return false, userErr("the project config %s: %v", pluginwire.Printable(path), err)
	}
	lc = fresh
	run, err := runOwedSteps(cmd, lc, path, false)
	kept := run.kept
	if !run.started {
		// The run stopped before it read a step — the record, the workdirs
		// lock, the managed gate's read — so every step this command queued is
		// what is owed.
		kept = steps
	}
	if len(kept) > 0 {
		chainKeptLine(cmd.OutOrStdout(), kept)
	}
	switch {
	case err != nil:
		// The run's own failure, which already says what stopped it: the
		// sentence above is this command's, and this is the run's.
		return false, err
	case len(kept) > 0:
		return false, userErr("%s still owed; run `fugaro apply` to finish the job", strings.Join(kept, ", "))
	}
	return true, nil
}

// applyAnswered reports whether a stage's own answer means the step it was run
// for is done: Changed and Done are the stage having done the work, and Skipped
// is it having found nothing to do — the read-only check first, which is the
// rule every step obeys (D1575).
//
// A skip is the one answer of the three that is not always an answer: the layer
// stage skips when the bucket it would publish into cannot be read, and the
// policy stage when the node it migrates is held or is another installation's,
// and neither published anything. Those carry Undone and leave the step owed
// (D1567: what `update` clears is the steps of the stages it ran successfully;
// D1575: a check that found nothing owed is what answers a step).
//
// Everything else leaves the step owed too: Blocked and WaitingForBilling are a
// stage that never got to its work, NeedsYou is one only the person can finish,
// Failed is the failure D1563 keeps, and Todo is a stage the loop did not reach
// — none of them is an installation that has the change live.
func applyAnswered(st initflow.StageResult) bool {
	switch st.State {
	case initflow.Changed, initflow.Done:
		return true
	case initflow.Skipped:
		return !st.Undone
	}
	return false
}

// applyCatalogBuild runs the one pending step that is not a stage: the billable
// Cloud Build of a catalog image (apply.CatalogBuild, D1577).
//
// Its check comes first, as every step's does (D1575): the entry was written
// into this machine's own config by the command that queued the step, and a
// config that names no catalog image this installation builds owes nothing, so
// this reads nothing and builds nothing.
//
// What is left is the build command's own path, which is where the rest of the
// rules already are: it leases the build from the job budget and refuses
// without a lease (D1543), so a change saved with --no-apply is never a
// billable build nobody asked for (F6); and in a coding agent's session it
// refuses with the session's own refusal, the one `fugaro update`'s steps
// answer with (D1571).
func applyCatalogBuild(cmd *cobra.Command, lc *localcfg.Config, run *stepRun) error {
	if !infra.CatalogBuilds(lc) {
		return nil
	}
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("%s", initflow.AgentRefusal(m))
	}
	// The build acts on the installation this command was aimed at, and it is
	// handed that installation rather than being left to select one: this
	// command's own selection is checkout-blind (settingsAimedSelection), while
	// a build that selected for itself reads the working directory's checkout
	// before anything else (selectProject) — so a build left to itself in a
	// checkout of another project would build THAT project's catalog, lease the
	// Cloud Build against that installation's budget, and answer nothing to
	// this run's record: the aimed project's step would clear as applied with
	// its catalog unbuilt (D1577). Passing the config on is also what keeps the
	// build out of the checkout rule that refuses a command whose project and
	// checkout disagree, which is a fact about this command's aim and not about
	// the repository the Dockerfile is read out of.
	//
	// It runs inside this command's own run as well (D1352, D1354): the build's
	// steps and their output are this run's account and this run's one log, not
	// a second `image-build` log filed beside it.
	return runImageBuildCatalogOrDerived(cmd, "", imageBuildOptions{lc: lc, run: run})
}

// clearAnsweredSteps is D1567: `update` runs every stage, so the stages it ran
// successfully are steps nothing owes any more, and their entries are cleared.
//
// It is the update door's alone. `setup` converges the same stages, but the
// record is settled by the command whose job is to bring an installation up to
// date, and a second door clearing a record it did not write is a record nobody
// can date.
//
// What it may not do is fail the run. The record is a hint (D1575) and `update`
// is the one command that has to work (CLAUDE.md): a record this build cannot
// read — one bound to another installation, one from a newer fugaro, one that
// is not JSON — and a write that does not land are both said and carried past,
// because the step they leave owed is one `fugaro apply` runs again for nothing
// (every step checks the state first). A stage that failed, one that was
// blocked behind one that did not finish, one that left something for the
// person, one that skipped without doing its own work (applyAnswered's Undone)
// and the redeploy of a repository with no checkout here all keep their steps
// owed, and a catalog build is never touched: `update` does not run it
// (D1577).
//
// The stamp beside the entries is not this command's to write: it dates the
// last `fugaro apply` that finished (D1575), and an update that stamped it
// would say a config was applied when what was owed for it may not have been.
func (e *initEngine) clearAnsweredSteps(res *initflow.Result) {
	if e.r.o.door != updateDoor.name || res == nil || e.lc == nil || e.lc.Name == "" {
		return
	}
	// What is dropped is the entry the run READ, and nothing else: an update is
	// minutes of Terraform and the record is a file another terminal writes —
	// a management command run with --no-apply, for a change this run's own
	// stage has already gone past. Clearing by name would drop that change
	// unrun while the record read as settled, which is what ClearUnchanged
	// exists for, and it is the shape the apply side settles with
	// (applyRunSteps). Nothing read before the first stage is nothing cleared:
	// an unread record is not a licence to drop entries.
	if e.owedBefore == nil {
		if e.owedBeforeErr != nil {
			fmt.Fprintf(e.r.notes(), "fugaro: warning: the steps saved for later were not read, so none were cleared: %v\n", e.owedBeforeErr)
		}
		return
	}
	var cleared []string
	for _, st := range res.Stages {
		if st.Name == initflow.Repository && st.State == initflow.Skipped {
			continue
		}
		if applyAnswered(st) {
			cleared = append(cleared, st.Name)
		}
	}
	if len(cleared) == 0 {
		return
	}
	pending, _, err := applyRecordRead(e.lc)
	if err != nil {
		fmt.Fprintf(e.r.notes(), "fugaro: warning: the steps saved for later were not read, so none were cleared: %v\n", err)
		return
	}
	if len(pending.Steps) == 0 {
		return
	}
	pending.ClearUnchanged(e.owedBefore, cleared...)
	if err := apply.SavePending(os.Getenv, e.lc.Name, pending); err != nil {
		fmt.Fprintf(e.r.notes(), "fugaro: warning: %s still recorded as owed: %v\n", strings.Join(cleared, ", "), err)
	}
}

// readOwedBefore reads the record as this door finds it, before the first stage
// plans: clearAnsweredSteps drops at the end only the entries still identical
// to these, so a step another terminal queued — or one a stage re-queued after
// failing — while the run was going stays owed, and a change nothing published
// is never recorded as settled (D1563).
//
// It is the update door's alone, which is the door that clears, and a read that
// fails is no reason to stop: the record is a hint (D1575) and `update` is the
// one command that has to work. The error is kept for clearAnsweredSteps to say,
// because nothing read is nothing cleared — the entries stay owed, which is the
// side the record fails on (`fugaro apply` runs them again for nothing, and
// every step checks the state first).
func (e *initEngine) readOwedBefore() {
	if e.r.o.door != updateDoor.name || e.lc == nil || e.lc.Name == "" {
		return
	}
	pending, _, err := applyRecordRead(e.lc)
	if err != nil {
		e.owedBeforeErr = err
		return
	}
	e.owedBefore = pending
}

// printApplyStatus is `fugaro apply --status`: what is owed, in the order it
// runs, and nothing else. It runs no step and writes no file, which is what
// makes it the one read the console is admitted to (D1569).
func printApplyStatus(cmd *cobra.Command, lc *localcfg.Config, pending *apply.Pending, recorded, current, asJSON bool) error {
	if asJSON {
		fmt.Fprintln(cmd.OutOrStdout(), string(applyDocJSON(lc, pending, recorded, current, nil)))
		return nil
	}
	w := cmd.OutOrStdout()
	if len(pending.Names()) == 0 {
		fmt.Fprintln(w, "Nothing to apply.")
		applyRecordNote(w, recorded, current)
		return nil
	}
	rows := applyRows(pending)
	fmt.Fprintf(w, "%s owed for %s (GCP %s):\n", applyCount(len(rows)), lc.Name, lc.GCPProject)
	for _, row := range rows {
		fmt.Fprintf(w, "  %s\t%s\n", safetext.Field(row.Step), applyChangeText(row))
	}
	fmt.Fprintln(w, "Run `fugaro apply` to run them.")
	return nil
}

// applyCount is the first line of the status answer: how many steps are owed,
// in words, so the sentence reads for one and for many.
func applyCount(n int) string {
	if n == 1 {
		return "1 step is"
	}
	return fmt.Sprintf("%d steps are", n)
}

// applyChangeText is the change that asked for a step, as the person running
// this command reads it. The key is written by a person — a setting key or a
// resource type — so it goes through safetext: it lands on a terminal, and a
// key that carries an escape sequence would draw whatever it liked there
// (D1575).
func applyChangeText(row applyRow) string {
	key := safetext.Field(row.Key)
	switch apply.Kind(row.Kind) {
	case apply.Setting:
		return "a settings change (" + key + ")"
	case apply.Resource:
		return "a " + key + " change"
	}
	return key
}

// applyRows is what the record owes as the document holds it, in the order the
// steps run.
func applyRows(p *apply.Pending) []applyRow {
	byStep := make(map[string]apply.PendingStep, len(p.Steps))
	for _, s := range p.Steps {
		byStep[s.Step] = s
	}
	rows := make([]applyRow, 0, len(p.Steps))
	for _, n := range p.Names() {
		s := byStep[n]
		rows = append(rows, applyRow{Step: n, Kind: string(s.Change.Kind), Key: s.Change.Key})
	}
	return rows
}

// applyDocJSON is the command's answer as its versioned document.
func applyDocJSON(lc *localcfg.Config, p *apply.Pending, recorded, current bool, applied []string) []byte {
	doc := applyDoc{
		Version:    applyStatusVersion,
		Project:    lc.Name,
		GCPProject: lc.GCPProject,
		Recorded:   recorded,
		Current:    current,
		Pending:    applyRows(p),
		Applied:    applied,
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		// The document is a fixed shape of strings, a bool and a number: a
		// failure here is this program's, and an answer that is not JSON is
		// worse than an empty one a reader can refuse.
		return []byte("{}")
	}
	return b
}
