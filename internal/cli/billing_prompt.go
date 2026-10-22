package cli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/dimipaun/fugaro/internal/billing"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// billing_prompt.go is the wizard's billing question (task W4.3 of
// docs/setup-wizard/implementation-plan.md, and the owner's answer to D6 of
// 2026-10-20): where the project stage prints the guided `gcloud billing
// projects link` line, a run at a real terminal lists the open billing
// accounts the operator can link, by number, and takes the one picked into
// --link-billing's own field — the field the project stage's typed
// confirmation reads, so the account still reaches a link only by being typed
// there.
//
// It is a deliberate, narrow reversal of the project stage's rule that init
// never lists the caller's billing accounts. internal/billing's package doc
// carries the bounds, and this file is the one call site it names: the list is
// asked for only where canType() says yes (a real terminal, not
// --non-interactive, not --json, not a coding agent's session — Global
// Constraint 8), only the open accounts are offered, only an ID and a display
// name are shown, nothing is cached, written or logged, and the account a
// project is already linked to is never read or printed.
//
// Where any of that fails — one of the four conditions — nothing is listed,
// nothing is recorded, and the run is exactly the run it was before this task
// (reconciliation F6): the project stage's own guided line is what the
// operator sees then. Where the question ran and neither source answered, the
// run is that run plus one value-free line saying the list could not be read
// (askBilling): an owner who asked for the list and got silence cannot tell it
// from a run with no picker at all, and Check 111 records which source
// answered — here, neither.

// billingNoAccount is the answer that links nothing now. A list can hold an
// account the operator does not want to use yet, and a question with no way
// out of it is a question that gets a wrong answer.
const billingNoAccount = "none"

// billingConsole is where an account that does not exist yet is created. The
// wizard names it rather than a command: creating a billing account is not
// something a CLI does here, and the console is where the operator's own
// accounts already are.
const billingConsole = "https://console.cloud.google.com/billing"

// billingSources is where the question's two sources point: the zero value is
// the operator's own Application Default Credentials and the real gcloud, and
// a test replaces it (internal/billing's Credentials documents the same seam)
// so that no test lists a real account or runs a binary. Project is filled in
// per run below; the two sources themselves never read it.
var billingSources billing.Options

// asksBilling is the whole of the question's condition: the run can ask and be
// answered (a terminal, and none of the flags a script uses), the operator's
// billing accounts may be listed (canType: the four conditions of Global
// Constraint 8), and the answer is one this run acts on.
//
// The last part is the question's own: the project stage links billing only
// for a project this run was asked to create (its Check skips the stage
// outright on an answer of adoption, so a billing answer there would be read
// by nobody), and a --link-billing on the command line has already answered
// this question — a second answer would replace what the operator typed.
func (r *initRun) asksBilling() bool {
	o := r.o
	if !r.canType() {
		return false
	}
	// The wizard's own question conditions (canAsk), without the terminal
	// canType already reads: --yes takes the answers it covers on the
	// operator's behalf, and --print-vars is stdout as a document.
	if o.yes || o.printVars {
		return false
	}
	return o.createProject && o.linkBilling == ""
}

// askBilling is the wizard's billing question. It is the last question the
// wizard asks, because it is about the GCP project the answers above settled.
//
// The picked account is recorded in --link-billing's field and nowhere else:
// nothing is written to a file, to the local config or to a Terraform
// variable, and the account reaches a link only through the project stage's
// typed confirmation of its ID.
func (r *initRun) askBilling(ctx context.Context) error {
	if !r.asksBilling() {
		return nil
	}
	o, project := r.o, r.o.cloud.gcpProject
	opts := billingSources
	opts.Project = project
	accounts, source, err := billing.ListWithFallback(ctx, opts)
	if err != nil {
		// Neither source answered. Nothing is offered and nothing is recorded,
		// and the one line below is all this says about it: the refusal itself
		// is not printed — it is the API's own words about a list this run did
		// not need, and it can name a project, a permission or an account —
		// and the project stage prints the guided line for an account it does
		// not know. Without the line, a run that could not read the list is
		// indistinguishable from a run with no picker at all, and Check 111
		// records which source answered: neither.
		fmt.Fprintf(r.w, "Billing accounts could not be listed (neither the Cloud Billing API nor %s answered).\n", billing.GcloudBin)
		return nil
	}
	if len(accounts) == 0 {
		r.billingLeft(project)
		return nil
	}
	fmt.Fprintf(r.w, "Billing accounts you can link to %s (from %s):\n", pluginwire.Printable(project), source)
	for i, a := range accounts {
		if a.DisplayName == "" {
			fmt.Fprintf(r.w, "  %d) %s\n", i+1, a.ID)
			continue
		}
		fmt.Fprintf(r.w, "  %d) %s (%s)\n", i+1, a.DisplayName, a.ID)
	}
	def := ""
	if len(accounts) == 1 {
		def = "1" // one account is the answer Enter takes
	}
	v, err := r.prompt(fmt.Sprintf("Billing account (a number, or %s to link none now)", billingNoAccount), def, func(s string) error {
		if s == billingNoAccount {
			return nil
		}
		n, aerr := strconv.Atoi(s)
		if aerr != nil || n < 1 || n > len(accounts) {
			return fmt.Errorf("answer a number from 1 to %d, or %s to link no account now", len(accounts), billingNoAccount)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if v == billingNoAccount {
		r.billingLeft(project)
		return nil
	}
	// The validator above took the answer, so this cannot fail: it is the same
	// parse that let it through, one line later.
	n, err := strconv.Atoi(v)
	if err != nil {
		return userErr("reading the billing account's number %q: %v", v, err)
	}
	o.linkBilling = accounts[n-1].ID
	return nil
}

// billingLeft is what the question says when it has no account to offer, and
// when the operator wants none of the ones it has: the one clear thing about
// an account that does not exist yet, and today's guided line — billing.Guided,
// the same words the project stage prints (its guided() is the same call), so a
// rewording cannot leave the two disagreeing.
func (r *initRun) billingLeft(project string) {
	fmt.Fprintf(r.w, "This run links no billing account. Create one in the Google Cloud console (%s), then run:\n  %s\n",
		billingConsole, billing.Guided(project))
}
