package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/yamledit"
)

// update_budget_off.go is the two writes AB8b owes the word that is not a
// budget mode any more: this machine's local config, which the update rewrites
// in place, and the checkout's fugaro.yaml, whose line it offers to remove as a
// diff (F12, D3022).
//
// The two are different doors and the difference is the whole of why one is
// rewritten and the other offered. The local config is this machine's own file,
// written by `fugaro update` on every installation and never committed: the
// migration is that write, and the run says what it did (the catalog guard's
// precedent, update_catalog_guard.go). A checkout's fugaro.yaml is a committed
// file somebody reviews, so the update offers the removal the way it offers the
// anchor's line — a diff, written with --yes or by a person at the terminal and
// printed otherwise — and the repository's runs are refused until the person
// merges it (D3022's cost, stated once in the reconciliation).
//
// Neither write may fail the update (CLAUDE.md's HARD RULE): every state that
// leaves the file as it is prints what it is and carries on.

// rewriteLocalBudgetMode rewrites the word out of this machine's local config,
// in place, and says so.
//
// It runs before the loop's first stage, beside the image catalog's own guard,
// for the reason that guard does: the config every stage of this run reads is
// the one this run writes. The loader reads the word as the file writes it and
// lets the config load (localcfg's acceptedBudgetMode, F22), so this step is
// where the word leaves — and it is applied to the config the run holds as
// well as to the file, the model-prices step's own pattern, because the
// converge writes the whole config again at its end: a run that spliced the
// file alone would put the word back out of the copy it read at boot.
//
// It is the update door's alone: `fugaro setup` creates an installation rather
// than moving one, and the word is one only a release behind this one wrote.
func (e *initEngine) rewriteLocalBudgetMode() {
	r, o := e.r, e.r.o
	if o.doorName() != updateDoor.name || e.lc == nil || e.path == "" {
		return
	}
	if _, off := localcfg.RemovedBudgetMode(e.old); !off {
		return
	}
	if o.planOnly {
		r.warn(fmt.Sprintf("budget.mode: %s says off, which is not a budget mode any more; this run would rewrite it to observe (--plan-only writes nothing)", e.path))
		return
	}
	// The edit is a splice and not a re-marshal, for the reason the anchor's is:
	// the file keeps every byte it had around the one value that moves, so a
	// config carrying comments or a spelling this binary would rewrite whole is
	// not rewritten whole.
	out, err := yamledit.SetScalar(e.old, []string{"budget", "mode"}, policy.ModeObserve)
	if err != nil || !yamledit.Changed(e.old, out) {
		why := "the edit changed nothing"
		if err != nil {
			why = oneLineCLI(err.Error())
		}
		r.warn(fmt.Sprintf("budget.mode: %s says off, which is not a budget mode any more, and this run could not rewrite the line (%s): change it to observe by hand", e.path, why))
		return
	}
	backup, err := backupFile(e.path, e.old, time.Now())
	if err != nil {
		r.warn(fmt.Sprintf("budget.mode: %s says off, which is not a budget mode any more, and the file could not be backed up (%s): change the line to observe by hand", e.path, oneLineCLI(err.Error())))
		return
	}
	if err := writeFileAtomic(e.path, out); err != nil {
		r.warn(fmt.Sprintf("budget.mode: %s says off, which is not a budget mode any more, and the file could not be written (%s): change the line to observe by hand", e.path, oneLineCLI(err.Error())))
		return
	}
	if e.lc.Budget != nil {
		e.lc.Budget.Mode = policy.ModeObserve
	}
	fmt.Fprintf(r.w, "budget.mode: %s said off, which is not a budget mode any more: it now says %s (the file this run replaced is at %s). Review it with git diff and commit it like any change.\n",
		e.path, policy.ModeObserve, backup)
}

// offerBudgetModeRemoval offers the removal of the budget.mode line from the
// checkout's fugaro.yaml, as a diff, exactly as the anchor's line is offered
// (F12's precedent).
//
// It runs at the end of the converge, beside the budget mode's raise, and not
// before the loop where the listing and the local config's rewrite run: the
// repository stage reads the checkout's file and refuses to act unless the
// working tree holds the default branch's bytes (resolveRepoTarget), so a write
// before that stage would turn this run's own repository step into "this
// checkout's fugaro.yaml is not the default branch's". After the loop the file
// is nobody's input any more, and the person reads the diff beside everything
// else the update did.
//
// A checkout this run is not in, a file that is not there and a file that does
// not say the word are all silence: there is nothing to offer, and none of them
// is this step's to report.
func (e *initEngine) offerBudgetModeRemoval(ctx context.Context) {
	r, o := e.r, e.r.o
	if o.doorName() != updateDoor.name {
		return
	}
	root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel")
	if err != nil {
		return
	}
	edit, err := prepareBudgetModeRemoval(root)
	var yerr *yamledit.Error
	switch {
	case errors.As(err, &yerr):
		// A splice the editor refuses leaves the file alone, and the word is
		// still in it: the person gets the edit named rather than a write that
		// did not happen.
		r.warn(fmt.Sprintf("budget.mode: %s says off, which is not a budget mode any more, and the line could not be removed (%s): remove it by hand, or this repository's runs are refused", filepath.Join(root, "fugaro.yaml"), oneLineCLI(err.Error())))
		return
	case err != nil, edit == nil:
		return
	}
	env := initflow.Env{Yes: o.yes, Interactive: stdinIsTerminal(r.cmd.InOrStdin()) && !o.nonInteractive && !o.asJSON}
	if _, err := r.writeBudgetModeRemoval(edit, env, o.planOnly); err != nil {
		r.warn(fmt.Sprintf("budget.mode: %s says off, which is not a budget mode any more, and the line was not removed (%s): remove it by hand, or this repository's runs are refused", edit.path, oneLineCLI(err.Error())))
	}
}
