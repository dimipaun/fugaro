// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"

	"github.com/dimipaun/fugaro/ee/coordinator"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// Declare declares one launch to the coordinator's record. It is the writer the
// launch has always used, now the tagged build's implementation of the core's
// launch-declarer hook (design docs/design/ee-boundary-moves.md §5.1): the
// launch path calls the hook, and an untagged build registers no declarer at
// all, so its launches declare nothing (D535).
//
// Three rules, and each is a decision rather than a detail:
//
//   - The declaration follows the launch's own database switch. The coordinator
//     keeps its state in the same database the budget uses, and a launch that
//     was told not to use it declares nothing: budget.mode: off and a project
//     with no budget.rtdb_url are that switch in the config, and
//     --no-budget-check is the operator saying it for this launch. Nothing is
//     lost by it: the declaration is advisory, and the view the sweep renders
//     reads the runs bucket, where the launch's own record lands whatever
//     happens here.
//   - A task that names no requester declares nothing the coordinator could
//     attribute, and the report path refuses an unnamed caller
//     (coordinator.ErrUnnamedCaller). That refusal is right when a person asks
//     for a report and pointless here, where nobody asked, so the launch is
//     simply not declared.
//   - Nothing else can fail the launch: the core's caller turns a failure into
//     a warning, because the coordinator is advisory and a run must not wait on
//     it, and drops a refusal of the write (rtdb.ErrPermission) without one.
func Declare(ctx context.Context, c *fcli.Cloud, slug string, spec *task.Spec) error {
	lc := c.Config()
	if lc.BudgetMode() == localcfg.BudgetOff || lc.Budget == nil || lc.Budget.RTDBURL == "" ||
		c.NoBudgetCheck() || spec.RequestedBy == "" {
		return nil
	}
	// The launch's own bound, well inside the claim's hold: a database that
	// does not answer must not eat the time the launch itself needs.
	ctx, cancel := context.WithTimeout(ctx, fcli.ReportIntentTimeout)
	defer cancel()
	store, err := openCoordinatorStore(ctx, lc)
	if err != nil {
		return err
	}
	// The database is this installation's before the declaration lands in it:
	// the record is read back by this project's coordinator and by nobody
	// else, so a launch must not write one into a database another
	// installation marked. The launch itself is untouched by the answer — the
	// caller turns it into a warning line, as it does every failure here.
	if err := ownCoordinatorDB(ctx, lc, "nothing was declared"); err != nil {
		return err
	}
	branch := spec.Branch
	if branch == "" {
		// The launcher's own rule for the branch it reports and creates: the
		// run's own name, so the declaration and the run name the same thing.
		branch = "fugaro/" + spec.RunID
	}
	_, _, err = (&coordinator.Reporter{Store: store}).Report(ctx, slug, coordinator.Intent{
		DeclaredBy: spec.RequestedBy,
		Branch:     branch,
	})
	return err
}
