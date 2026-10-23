package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/dimipaun/fugaro/internal/billing"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/safetext"
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
//
// W4.4 is the other half of that line: what the run does when the API's own
// answer is that cloudbilling.googleapis.com is off on the credentials' quota
// project (D3's answer of 2026-10-20, and the one place in the whole wizard
// where an API's state may change). The API names that project, and the answer
// splits on whether it is the project this run is setting up:
//
//   - it is, so the run may offer to turn the API on there — behind a typed
//     confirmation that --yes never covers, naming the project and the API —
//     and then ask the list once more;
//   - it is not, so the run refuses: it names the exact line to run on that
//     project and who to ask, and enables nothing anywhere. An API turned on
//     on a project none of the run's answers named is precisely the surprise
//     F8's refusals exist to prevent.
//
// Either way the fallback (W4.2) and the project stage's guided line are still
// what the operator has: this is an addition to that path, never a
// replacement (F6). The offer is made only where the list could not be read at
// all — the API's disabled answer is read from the error the fallback already
// carried up (ErrNoSource.Unwrap), so the API is asked once, and a fallback
// that answered has left nothing for the offer to fix.

// billingNoAccount is the answer that links nothing now. A list can hold an
// account the operator does not want to use yet, and a question with no way
// out of it is a question that gets a wrong answer.
const billingNoAccount = "none"

// billingAPIService is the API the account list needs, as Google spells it: the
// one thing W4.4 may turn on, and the name both the offer and the refusal print.
// internal/billing names the same service in the enable line it carries
// (ErrDisabled.EnableCommand), and the tests compare the two spellings.
const billingAPIService = "cloudbilling.googleapis.com"

// billingEnableArgs is that line in argv form: gcloud runs without a shell, so
// the project is passed as it is — and a project ID never needs quoting
// (infra.ValidProjectID). It is the command the line names, and only the
// operator's typed answer runs it.
func billingEnableArgs(project string) []string {
	return []string{"services", "enable", billingAPIService, "--project", project}
}

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

// billingProjectNumber is the number of the project this run is setting up, the
// way Google's own answers name a project (projects/<number>) and the way the
// quota project in a SERVICE_DISABLED answer is named. D3's first case turns on
// whether that quota project is this one, and an ID and a number are not the
// same string, so the two cannot be compared without it.
//
// It is read here, and not through the run's own clients, because the wizard's
// questions all run before it builds one (gatherInputs): the read is the same
// read-only lookup the installation-name question makes (installationName,
// init_inputs.go), through the same helpers. It reports false where the number
// cannot be read — a project this run is *creating* does not exist yet, and
// friction log #4 is exactly that: a project a run created could not be its own
// quota project — and the caller then refuses rather than offering to turn an
// API on in a project it cannot name. Tests replace it.
var billingProjectNumber = func(ctx context.Context, gcpProject, region string) (uint64, bool) {
	if gcpProject == "" || region == "" {
		return 0, false
	}
	lc, err := newProjectConfig("pending", gcpProject, region)
	if err != nil {
		return 0, false
	}
	c, err := newInitClients(ctx, lc)
	if err != nil {
		return 0, false
	}
	num, err := infra.ProjectNumber(ctx, c, gcpProject)
	if err != nil {
		return 0, false
	}
	return num, true
}

// asksBilling is the whole of the question's condition: the run can ask and be
// answered (a terminal, and none of the flags a script uses), the operator's
// billing accounts may be listed (canType: the four conditions of Global
// Constraint 8), and the answer is one this run acts on.
//
// The last part is the question's own: the project stage links billing only
// for a project this run was asked to create (its Check skips the stage
// outright on a run that creates no project, so a billing answer there would be read
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
		// Neither source answered. Where the API's own answer is that it is off
		// on the credentials' quota project, the run says the one thing D3 lets
		// it do about that (billingAPIOff: an offer, or a refusal) — and it may
		// come back with a list, the second time the API is asked. Everything
		// else is the line below and nothing more.
		accounts, source, err = r.billingAPIOff(ctx, opts, err)
	}
	if err != nil {
		// Neither source answered. Nothing is recorded, and the one line below is
		// all this says about it: the refusal itself is not printed — it is the
		// API's own words about a list this run did not need, and it can name a
		// project, a permission or an account — and the project stage prints the
		// guided line for an account it does not know. Without the line, a run
		// that could not read the list is indistinguishable from a run with no
		// picker at all, and Check 111 records which source answered: neither.
		//
		// What this does not say is that nothing was enabled: the offer above runs
		// one, and where the list could not be read even after it, the enable's
		// own line ("enabled ...," or billingEnableLeft's) has said what happened
		// to it. The line below is about the list alone.
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

// billingAPIOff is W4.4 (D3): the one place in the wizard where the state of an
// API may change, and it exists only where the API's own answer is that it is
// off on the credentials' quota project. err is what ListWithFallback carried
// up, and that answer is inside it — *ErrNoSource hands the API's own
// *ErrDisabled back (Unwrap) — so the API is asked once and nothing here can
// fire on a list the fallback answered. Every other error (a missing
// permission, a refusal, no gcloud on PATH) leaves as it came: the caller's one
// line is all the run says, and the project stage's guided line is what the
// operator has (F6).
//
// It returns the list of a second read, which is only ever made after an enable
// this run ran. It is reached once per run: the offer is made on the error of
// the run's own list, and the second list is the last the question asks.
func (r *initRun) billingAPIOff(ctx context.Context, opts billing.Options, err error) ([]billing.Account, billing.Source, error) {
	var off *billing.ErrDisabled
	if !errors.As(err, &off) {
		return nil, billing.SourceNone, err
	}
	quota, project := pluginwire.Printable(off.QuotaProject), pluginwire.Printable(opts.Project)
	// Google names the project it is refusing in a SERVICE_DISABLED answer by
	// *number* (internal/billing reads it out of the consumer as it is, and
	// gcpfake's own default is projects/123456789012), and the project this run
	// is setting up is an *ID*: comparing the two strings directly answers "not
	// this project" every time, which is what makes the offer below unreachable
	// and the refusal's claim false. The project's number is read and the two are
	// compared as numbers, the way internal/infra compares the consumer of the
	// same answer with the project it asked about (target.consumes) — the ID
	// spelling passes too, as it does there. num is 0 and known is false where
	// the number cannot be read, and that is a refusal: a project this run is
	// creating does not exist yet, and an API turned on in a project the run
	// cannot name is the surprise F8's refusals exist to prevent.
	num, known := billingProjectNumber(ctx, opts.Project, r.o.cloud.region)
	if off.QuotaProject != opts.Project && !(known && off.QuotaProject == strconv.FormatUint(num, 10)) {
		r.refuseBillingAPI(off, quota, project, known)
		return nil, billing.SourceNone, err
	}
	// D3's first case: the API is off on the project this run is setting up, so
	// this run may offer to turn it on there — behind a typed confirmation, the
	// one the wizard's other state-changing steps ask for, naming the project
	// and the API. --yes never covers it: a --yes run is a run that asked for no
	// question, so asksBilling is false there and this is not reached at all.
	//
	// Nothing about the offer is taken for an answer: only the project's own ID
	// typed on the line enables anything, and every other answer — Enter, a
	// stray word, the end of input — leaves the project exactly as it was and
	// hands the operator the line to run by hand.
	fmt.Fprintf(r.w, "⚠ CONFIRM: ENABLE the Cloud Billing API (%s) in GCP project %s, the project this run is setting up: it is disabled there, and it is what the billing accounts this run can link are listed with (free, and nothing fugaro does disables it again)\n",
		billingAPIService, project)
	fmt.Fprintf(r.w, "Type %s to enable it, or press Enter to link an account by hand instead: ", project)
	line, rerr := r.in.ReadString('\n')
	if rerr != nil && !errors.Is(rerr, io.EOF) {
		return nil, billing.SourceNone, userErr("reading the confirmation: %v", rerr)
	}
	fmt.Fprintln(r.w)
	if strings.TrimSpace(line) != opts.Project {
		r.billingEnableLeft(off)
		return nil, billing.SourceNone, err
	}
	if !r.enableBillingAPI(ctx, opts, off) {
		return nil, billing.SourceNone, err
	}
	// The API's own answer is the only thing that said it was off, so it is
	// asked once more: what the enable turned on is what makes the list
	// readable, and the second answer is the run's own (an account list that
	// arrives here is a list the question was answered with).
	return billing.ListWithFallback(ctx, opts)
}

// refuseBillingAPI is D3's second case: the API is off on the credentials' quota
// project, and this run enables nothing anywhere. It names what it read — the
// quota project, the exact line to run there and who to ask — and turning an API
// on in a project none of the run's answers named is precisely the surprise F8's
// refusals exist to prevent: this run has no idea what else that project holds.
//
// known says whether the project this run is setting up could be read at all.
// Where it could not (a project the run is creating does not exist yet, and the
// number of a project that does not exist cannot be read), the run prints the
// second wording rather than the first: it has not established that the two
// projects differ, and a refusal that says so would be the same false statement
// the number comparison above exists to stop making. The line to run and who to
// ask are in both.
func (r *initRun) refuseBillingAPI(off *billing.ErrDisabled, quota, project string, known bool) {
	if known {
		fmt.Fprintf(r.w, "The Cloud Billing API (%s) is disabled on %s, the quota project of your credentials, which is not the project this run is setting up (%s). This run enables nothing there: ask an owner of %s to run:\n  %s\n",
			billingAPIService, quota, project, quota, off.EnableCommand)
		return
	}
	fmt.Fprintf(r.w, "The Cloud Billing API (%s) is disabled on %s, the quota project of your credentials. This run could not read the number of the project it is setting up (%s), so it cannot tell whether %s is that project: it enables nothing there. Ask an owner of %s to run:\n  %s\n",
		billingAPIService, quota, project, quota, quota, off.EnableCommand)
}

// billingEnableLeft is the line the offer leaves behind when it was not taken,
// or was taken and could not be run: the exact command internal/billing carries
// (ErrDisabled.EnableCommand), which needs no API of this run's and is the same
// one the refusal prints for another project. The guided `gcloud billing
// projects link` line is the project stage's to print, and it prints it.
//
// "Nothing was enabled" and not "this run enables nothing": it is reached after
// a gcloud that failed as well as after a decline, and the one true thing about
// both is that no API's state changed.
func (r *initRun) billingEnableLeft(off *billing.ErrDisabled) {
	fmt.Fprintf(r.w, "Nothing was enabled. Run:\n  %s\nthen run this again, or link an account you already have.\n", off.EnableCommand)
}

// enableBillingAPI runs the enable line through gcloud, the same injected seam
// the fallback uses (billing.Options.Gcloud), and reports whether it ran. It is
// reached only after the operator typed the project's ID, and it is the only
// command this question ever runs that changes anything: what it does not do is
// touch Service Usage directly, so what the operator would have run by hand and
// what the wizard runs are the same line, in the same project.
//
// A gcloud that is not on PATH, or an enable that fails, is not the run's
// failure: it says what happened, prints the line, and the operator keeps
// everything they had (the fallback, and the project stage's guided line).
func (r *initRun) enableBillingAPI(ctx context.Context, opts billing.Options, off *billing.ErrDisabled) bool {
	look := opts.Gcloud.LookPath
	if look == nil {
		look = exec.LookPath
	}
	if _, err := look(billing.GcloudBin); err != nil {
		fmt.Fprintf(r.w, "%s is not on PATH, so this run cannot enable %s in %s itself.\n", billing.GcloudBin, billingAPIService, pluginwire.Printable(opts.Project))
		r.billingEnableLeft(off)
		return false
	}
	run := opts.Gcloud.Run
	if run == nil {
		run = billing.ExecRunner
	}
	if _, err := run(ctx, billingEnableArgs(opts.Project)...); err != nil {
		// The command's own words: internal/billing's runner carries gcloud's
		// stderr, and this is the one line of it the operator needs to see. It
		// is not repeated anywhere else (nothing is written or logged).
		fmt.Fprintf(r.w, "Enabling %s in %s failed: %s\n", billingAPIService, pluginwire.Printable(opts.Project), safetext.OneLine(err.Error()))
		r.billingEnableLeft(off)
		return false
	}
	r.res.Enabled = append(r.res.Enabled, billingAPIService)
	fmt.Fprintf(r.w, "enabled %s in %s\n", billingAPIService, pluginwire.Printable(opts.Project))
	return true
}
