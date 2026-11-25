package cli

// chain.go is the command side of the composite chain (docs/composite-commands,
// AP6): what a management command does with the steps the change it just wrote
// owes.
//
// The steps are one table and it is not here (internal/apply, AP1): a change —
// a setting key or a resource type — declares what makes it live, in initflow's
// own stage names plus the one step that is not a stage, a catalog image's
// build. This file is the other half. A command names the change it made and
// gets one of two answers: the steps run, in process, through the same loop
// `fugaro update` uses (stagesNamed) and the same runner `fugaro apply` uses
// (applyRunRecord) — so the order, the check-first rule and the sentence a step
// that did not finish leaves behind are one code path and not one per family —
// or they are saved in this machine's own record for `fugaro apply` to run
// (D1560, D1563, D1565).
//
// `--no-apply` is the one word that asks for the second answer (D1565), and it
// is the same word, the same help line and the same behaviour everywhere
// (addNoApplyFlag). A step that does not finish — a stage that failed, a
// machine with no checkout of the repository the redeploy is that repository's
// own Terraform root for (D1574), a session the gate refuses — leaves its
// change saved and the step owed, said in one sentence that names `fugaro
// apply`: nothing is rolled back, and nothing is forgotten.
//
// A chain asks nothing. The run it opens carries its own answer and reads no
// input (initOptions.nonInteractive below), which is what the no-prompt policy
// requires of every step a change runs on its own (D1540): a command that
// already wrote has nothing left to ask, and a confirmation that waited here
// would hang on a prompt the person has no reason to expect.

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/apply"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// chainSteps answers the steps a command's change owes, in the order they run,
// and refuses a change the table does not declare.
//
// A change nobody declared is a gap in the table and never "nothing owed"
// (apply.Steps' own rule): the table is the one place a change's steps are
// declared, so a key or a resource type with no row is a command whose change
// would be saved with nothing to make it live — the one outcome the composite
// spec exists to prevent (spec rules 1 and 2). The refusal is the caller's
// programming error and it is said rather than swallowed.
//
// Two changes that owe the same step leave it once (apply.Order): the steps
// dedupe and run once, however many changes asked for them (spec rule 3).
func chainSteps(changes []apply.Change) ([]string, error) {
	var names []string
	for _, c := range changes {
		steps, ok := apply.Steps(c)
		if !ok {
			return nil, userErr("this command changed %s %q and the step table declares nothing for it: the change would be saved with nothing to make it live, which is a bug in fugaro rather than something to repair here", c.Kind, c.Key)
		}
		names = append(names, steps...)
	}
	return apply.Order(names), nil
}

// chainSave records that the steps are owed, for the change that asked for
// them, in this machine's own record (internal/apply, D1562).
//
// Every step is saved with the change it answers, and the change is the
// subject and never a value (D1570): the record says "a model change", never
// what was added or removed, so it can never become a second copy of the
// config.
//
// The write is the caller's to fail on: a record that cannot be written is a
// step nobody would run, and the command has already written its change — so
// the failure is said with the sentence that names `fugaro apply`, which is
// the same sentence the run below ends on when a step does not finish.
//
// It answers the record as this command left it, its own entries included:
// that is what the settle below matches the entries of the run against, so a
// step somebody queued while the run was going — the same step, for a change
// made after this command's — stays owed rather than reading as the entry this
// run answered (apply.Pending.ClearUnchanged, D1563). The queue sequence Add
// stamps is what tells the two apart: every other member of an entry for the
// same step and the same change is the same, so without it the entry this
// command queued and the one that replaced it would be one entry.
func chainSave(lc *localcfg.Config, changes []apply.Change, names []string) (*apply.Pending, error) {
	p, _, err := loadPendingSteps(lc)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		p.Add(apply.PendingStep{
			Step:       name,
			Project:    lc.Name,
			GCPProject: lc.GCPProject,
			Change:     chainChangeFor(name, changes),
		})
	}
	if err := apply.SavePending(os.Getenv, lc.Name, p); err != nil {
		return nil, userErr("%v", err)
	}
	return p.Clone(), nil
}

// chainSettle is the other end of chainSave for a command whose own converge
// runs the steps — accounts rm, whose block of step lines is read off the
// stages' results and not off print this runner would make (accountsRmReport).
// It clears the steps the stages answered from the record chainSave wrote,
// keeps every other one owed, and answers D1563's sentence and a non-zero exit
// for them: the change stays, the step is remembered, and the command names
// what finishes it rather than pretending a run that did not finish did.
//
// states is what the caller's run answered for each step, keyed by stage name.
// A step the run reported no result for is owed and not dropped — a run that
// quietly did fewer steps than it was asked for is the one thing neither the
// record nor the command may do (applyRunRecord's own rule) — and the rule that
// reads a result is applyStepAnswered's, the one applyRunRecord uses, so the
// two doors cannot come to disagree about whether a step is done.
//
// queued is the record chainSave answered: the document as this command left
// it, its own entries included. What the settle clears is the entries still
// equal to those, and nothing else (ClearUnchanged's own rule).
func chainSettle(cmd *cobra.Command, lc *localcfg.Config, path string, queued *apply.Pending, names []string, states map[string]initflow.StageResult) error {
	after, afterRecorded, err := applyRecordRead(lc)
	if err != nil {
		return userErr("%v", err)
	}
	var ran, kept []string
	for _, n := range names {
		if applyStepAnswered(n, states[n], applyOptions{chain: true}) {
			ran = append(ran, n)
			continue
		}
		kept = append(kept, n)
	}
	after.ClearUnchanged(queued, ran...)
	// The stamp is written on the same rule applyRunRecord writes it (D1575): a
	// run that answered everything and left nothing owed stamps the config as it
	// stands now, so a later read can tell a config that changed since from one
	// that did not. A machine that had no record and answered nothing is not
	// given one: nothing was ever owed here, and a write would be this command
	// inventing state.
	if len(kept) == 0 && len(after.Steps) == 0 {
		after.LastApply = applyConfigStamp(path)
	}
	if afterRecorded || len(ran) > 0 {
		if err := apply.SavePending(os.Getenv, lc.Name, after); err != nil {
			return userErr("%v", err)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	applyKeptLine(cmd.OutOrStdout(), kept)
	return userErr("%s still owed; run `fugaro apply` to finish the steps it can answer", strings.Join(kept, ", "))
}

// chainChangeFor is the change one owed step answers: the first of the
// command's changes whose own row names that step.
//
// A step two changes asked for is recorded once, under the first of them
// (chainSave's Add replaces the entry), and the sentence `fugaro apply
// --status` prints for it names that one. Which of the two is named does not
// change what runs: the step is the answer to both.
func chainChangeFor(step string, changes []apply.Change) apply.Change {
	for _, c := range changes {
		if steps, ok := apply.Steps(c); ok && slices.Contains(steps, step) {
			return c
		}
	}
	return changes[0]
}

// chainApply is what a management command does once its change has landed: the
// steps that make it live, or — under --no-apply, or when a step does not
// finish — the record of what is still owed.
//
// door is the command's own name, and it is the door the run is filed and
// printed under rather than `fugaro update`'s: the lines a person reads have to
// name the command they ran (accountsAddRunStages' own rule), and the log a
// failure sends them to is that command's.
//
// A change the table declares with no steps owes none and is live once the
// config is written: the command prints nothing about it and this returns
// without opening a run — there is nothing a step could do (D1564's shape for
// the repo layer, and the member half of a workspace membership).
func chainApply(ctx context.Context, cmd *cobra.Command, lc *localcfg.Config, sel localcfg.Selection, door string, changes []apply.Change, noApply bool) error {
	names, err := chainSteps(changes)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	// The steps run over the config as the write left it, never over the copy
	// the command read before it. The command's own `lc` is that earlier read,
	// and a step reads its subject out of the config it is handed — the
	// workspace stages decide whether this installation is a workspace's own
	// from `workspace_members` and `workspace_cap`, which is exactly what
	// `workspace cap set` and `workspace project add` have just written — so a
	// chain handed the stale copy would ask a stage about a config that no
	// longer exists and skip it as "nothing to do here".
	//
	// The write has already been held to the strict loader by the command
	// (applySettingsEdit's read-back), so a file that does not read back here is
	// the one thing this may not carry on past: the steps would run over a
	// config nobody can name.
	if sel.Path != "" {
		fresh, err := localcfg.Load(sel.Path)
		if err != nil {
			return userErr("%s would not read back after the write, so the steps this change owes were not run: %v", sel.Path, err)
		}
		lc = fresh
	}
	// The record is written BEFORE the run, on both arms: a run is minutes of
	// Terraform, and one that died in the middle would leave a change nothing
	// remembers is not live. The steps the run answers are cleared from it as it
	// ends (applyRunRecord, ClearUnchanged), so a run that finished leaves an
	// empty record rather than an owed step.
	if _, err := chainSave(lc, changes, names); err != nil {
		return err
	}
	if noApply {
		chainSavedLine(cmd.OutOrStdout(), names)
		return nil
	}
	io := &initOptions{door: door, yes: true, nonInteractive: true}
	r := newInitRun(cmd, io)
	r.setProject(lc)
	// The workdirs are the converge's, whichever door converged them, and the
	// steps of a chain are stages of that converge: this run takes the lock
	// where the project is known and before the loop prepares anything, as
	// `fugaro apply` does.
	unlock, err := r.lockWorkdirs(lc.GCPProject)
	if err != nil {
		return err
	}
	defer unlock()
	pending, recorded, err := loadPendingSteps(lc)
	if err != nil {
		return err
	}
	// The run's account is filed under the command the person ran (D1351): the
	// steps' lines are this command's answer, and a run filed under `fugaro
	// apply`'s name while the person ran `fugaro models add` would be a log
	// they never think to look in. door is this command's own name — the one
	// its own lines are printed under (`fugaro workspace cap set`) — and the
	// log's name is that name with dashes for the spaces, the shape
	// report.OpenLog accepts and the shape the census holds for the doors that
	// hand their run to a converge of their own.
	run := stepRunFor(cmd, doorLogName(door), cmd.ErrOrStderr())
	defer run.close()
	return applyRunRecord(cmd, r, run, lc, sel.Path, pending, recorded, names, applyOptions{chain: true})
}

// chainSavedLine is what a command says when --no-apply left the steps owed
// (D1563's sentence, in the words the flag's own help uses): the change is
// saved, it is not live yet, and `fugaro apply` is the command that finishes
// the job.
//
// The step names are the table's and never a person's, and they are escaped
// where they are joined for the reason applyKeptLine escapes the record's: a
// name that carried an escape sequence would redraw the very line that says the
// change is not live yet.
func chainSavedLine(w io.Writer, names []string) {
	escaped := make([]string, 0, len(names))
	for _, n := range names {
		escaped = append(escaped, safetext.Field(n))
	}
	fmt.Fprintf(w, "saved, not live yet: %s owed\n", strings.Join(escaped, ", "))
	fmt.Fprintln(w, "Run `fugaro apply` to run it: every step checks the state first, so nothing is done twice.")
}
