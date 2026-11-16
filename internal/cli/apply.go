package cli

// apply.go is `fugaro apply` (docs/composite-commands, AP3): the one command
// that runs the follow-up steps the management commands saved for later.
//
// A management command writes its change and then runs the steps that make the
// change live — publishing the project policy, redeploying the checkout's
// repository, building a catalog image — and `--no-apply` is the one word that
// saves the change and stops (D1565). What the flag leaves behind is a local
// record of the steps owed (internal/apply, AP1) and this command is what reads
// it and runs it (D1560, D1562).
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
image. With --no-apply it writes the change and stops, and this command runs
what is owed later.

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
	pending, recorded, err := loadPendingSteps(lc)
	if err != nil {
		return err
	}
	current := applyCurrent(pending, recorded, sel.Path)
	if o.status {
		return printApplyStatus(cmd, lc, pending, recorded, current, o.asJSON)
	}
	return applyRunSteps(cmd, lc, sel.Path, pending, recorded, o)
}

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

// applyRunSteps is the command's run: the owed steps, each once, in the order
// the record gives them.
func applyRunSteps(cmd *cobra.Command, lc *localcfg.Config, path string, pending *apply.Pending, recorded bool, o applyOptions) error {
	if len(pending.Names()) == 0 {
		return applyNothing(cmd, lc, path, pending, recorded, o)
	}
	// The run's account is the reporter's, filed under this command's name
	// (D1351, D1354): the steps it runs are the loop's, and a command that ran
	// a converge without a reporter of its own would print a second account of
	// the same run beside the one the census expects.
	run := stepRunFor(cmd, "apply", cmd.ErrOrStderr())
	defer run.close()
	io := &initOptions{door: applyName, yes: true, nonInteractive: true, asJSON: o.asJSON}
	r := newInitRun(cmd, io)
	// The reporter is this command's: the steps print one line each through it,
	// and the closing line is printed where this command's own run ends
	// (ReportBorrowed below). A converge invoked here with a reporter of its
	// own would file a log this command never names.
	r.rep, r.repLog = run.rep, run.log
	r.setProject(lc)
	// The workdirs are the converge's, whichever door converged them, and the
	// redeploy is one of the stages that takes them: this door takes the lock
	// too, where the project is known and before the loop prepares anything.
	unlock, err := r.lockWorkdirs(lc.GCPProject)
	if err != nil {
		return err
	}
	defer unlock()
	spec, err := installOptions(io, lc)
	if err != nil {
		return err
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
		return err
	}

	names := pending.Names()
	var ran, kept []string
	// The catalog image is the one step that is not a stage, and apply.Order
	// puts it first (D1577): the image is what the project's jobs run, so a run
	// that cannot build it must not go on to publish the policy that names it.
	// A build that fails leaves every step owed, this one included, so running
	// this command again finishes the whole job.
	if slices.Contains(names, apply.CatalogBuild) {
		if err := applyCatalogBuild(cmd, lc); err != nil {
			// --json answers one document on every outcome (G2), and this arm
			// returns ahead of the answer below: nothing ran, so the document
			// is the record as it was read, with every step still owed.
			if o.asJSON {
				fmt.Fprintln(cmd.OutOrStdout(), string(applyDocJSON(lc, pending, recorded, applyCurrent(pending, recorded, path), nil)))
			} else {
				applyKeptLine(cmd.OutOrStdout(), []string{apply.CatalogBuild})
			}
			return err
		}
		ran = append(ran, apply.CatalogBuild)
	}

	stageNames := slices.DeleteFunc(slices.Clone(names), func(n string) bool { return n == apply.CatalogBuild })
	stages, unanswered := stagesNamed(e, stageNames...)
	// A stage that prints to cmd.OutOrStdout() directly — the policy stage's
	// publish diff is the loudest — would land on the stdout this command's
	// answer is on, so the command is pointed at stderr for the length of the
	// run and its own writer goes on the answer.
	answer := cmd.OutOrStdout()
	cmd.SetOut(cmd.ErrOrStderr())
	opts := e.options()
	opts.ReportBorrowed = true
	res, runErr := initflow.Run(cmd.Context(), stages, opts)
	cmd.SetOut(answer)

	states := map[string]initflow.State{}
	if res != nil {
		for _, st := range res.Stages {
			states[st.Name] = st.State
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
		if n == initflow.Repository && states[n] == initflow.Skipped {
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

	// The record is written before the answer, and it is written whole: the
	// steps this run answered are cleared, the rest stay owed (D1563), and a
	// run that answered everything stamps the config as it stands now — a stage
	// may have written it — so the next read can tell a config that changed
	// since from one that did not (D1575). A machine that had no record and
	// answered nothing is not given one: nothing was ever owed here, and a
	// write would be this command inventing state.
	pending.Clear(ran...)
	if len(kept) == 0 && runErr == nil {
		pending.LastApply = applyConfigStamp(path)
	}
	wrote := recorded || len(ran) > 0
	if wrote {
		if err := apply.SavePending(os.Getenv, lc.Name, pending); err != nil {
			return userErr("%v", err)
		}
	}
	current := applyCurrent(pending, wrote, path)

	switch {
	case o.asJSON:
		fmt.Fprintln(cmd.OutOrStdout(), string(applyDocJSON(lc, pending, recorded, current, ran)))
	case len(kept) > 0:
		applyKeptLine(cmd.OutOrStdout(), kept)
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "applied %s\n", strings.Join(ran, ", "))
	}

	switch {
	case runErr != nil:
		return userErr("%v", runErr)
	case res.Failed != nil:
		// The stage's own error, which is a user error or a remote one already:
		// the fix line names the step, and this command adds nothing to it.
		return res.Cause
	case len(res.Left) > 0:
		return userErr("%v", res.Left[0].Text)
	case len(kept) > 0:
		return userErr("%s still owed; run `fugaro apply` again to finish the job", strings.Join(kept, ", "))
	}
	return nil
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
// tell apart, and names the stamp `doctor` compares for the half it cannot.
func applyRecordNote(w io.Writer, recorded, current bool) {
	switch {
	case !recorded:
		fmt.Fprintln(w, "No record of what this machine owes is on disk: nothing is recorded as owed. A record that was deleted leaves the same answer, and what the project config still needs is not recomputed here — the last_apply stamp `fugaro doctor` compares is what says so.")
	case !current:
		fmt.Fprintln(w, "The project config changed since the last apply, and nothing owed is recorded against it: `fugaro doctor` compares the last_apply stamp.")
	}
}

// applyKeptLine is D1563's sentence: the change is saved, it is not live yet,
// and `fugaro apply` is the command that finishes it. The steps it names are
// the ones this run could not answer, and the run's own account above says why.
//
// A kept redeploy says where to run it: the repository stage's Terraform root
// is per repository (D1574), so a machine with no checkout of the repository
// the change was made for cannot run that step, and the answer has to say so
// rather than send the person to the same machine again.
func applyKeptLine(w io.Writer, kept []string) {
	fmt.Fprintf(w, "saved, not live yet: %s still owed\n", strings.Join(kept, ", "))
	if slices.Contains(kept, initflow.Repository) {
		fmt.Fprintln(w, "Run `fugaro apply` in a checkout of the repository the change was made for: the redeploy is that repository's own Terraform root, and this machine has none of it.")
		return
	}
	fmt.Fprintln(w, "Run `fugaro apply` again to finish the job: every step checks the state first, so the ones that already ran cost nothing.")
}

// applyAnswered reports whether a stage's own answer means the step it was run
// for is done: Changed and Done are the stage having done the work, and Skipped
// is it having found nothing to do — the read-only check first, which is the
// rule every step obeys (D1575).
//
// Everything else leaves the step owed: Blocked and WaitingForBilling are a
// stage that never got to its work, NeedsYou is one only the person can finish,
// Failed is the failure D1563 keeps, and Todo is a stage the loop did not reach
// — none of them is an installation that has the change live.
func applyAnswered(st initflow.State) bool {
	switch st {
	case initflow.Changed, initflow.Done, initflow.Skipped:
		return true
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
func applyCatalogBuild(cmd *cobra.Command, lc *localcfg.Config) error {
	if !infra.CatalogBuilds(lc) {
		return nil
	}
	if m := agentMarker(os.Getenv, stdinIsTerminal(cmd.InOrStdin())); m != "" {
		return userErr("%s", initflow.AgentRefusal(m))
	}
	return runImageBuildCatalogOrDerived(cmd, "", imageBuildOptions{})
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
// person and the redeploy of a repository with no checkout here all keep their
// steps owed, and a catalog build is never touched: `update` does not run it
// (D1577).
//
// The stamp beside the entries is not this command's to write: it dates the
// last `fugaro apply` that finished (D1575), and an update that stamped it
// would say a config was applied when what was owed for it may not have been.
func (e *initEngine) clearAnsweredSteps(res *initflow.Result) {
	if e.r.o.door != updateDoor.name || res == nil || e.lc == nil || e.lc.Name == "" {
		return
	}
	pending, _, err := loadPendingSteps(e.lc)
	if err != nil {
		fmt.Fprintf(e.r.notes(), "fugaro: warning: the steps saved for later were not read, so none were cleared: %v\n", err)
		return
	}
	if len(pending.Steps) == 0 {
		return
	}
	var cleared []string
	for _, st := range res.Stages {
		if st.Name == initflow.Repository && st.State == initflow.Skipped {
			continue
		}
		if applyAnswered(st.State) {
			cleared = append(cleared, st.Name)
		}
	}
	if len(cleared) == 0 {
		return
	}
	pending.Clear(cleared...)
	if err := apply.SavePending(os.Getenv, e.lc.Name, pending); err != nil {
		fmt.Fprintf(e.r.notes(), "fugaro: warning: %s still recorded as owed: %v\n", strings.Join(cleared, ", "), err)
	}
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
