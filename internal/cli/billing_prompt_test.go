package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/billing"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
)

// billing_prompt_test.go pins the wizard's billing question, task W4.3 of
// docs/setup-wizard/implementation-plan.md and the owner's answer to D6
// (2026-10-20): at a real terminal the wizard lists the open billing accounts
// the operator can use, by number, picks by number, takes a single account as
// the default, and on none says the one clear thing about creating one in the
// console and prints today's guided line. Where any of the four conditions
// canType() names fails — no terminal, --non-interactive, --json, a coding
// agent's session — nothing is listed and the run is exactly today's run
// (Global Constraint 8, reconciliation F6).
//
// The account ids here are built at run time from the test's name, never a
// literal that looks like one, and every source is a fake: no test in this
// file opens a socket to Google or runs gcloud.

// pickerAccountID is a billing account ID of the API's own shape (three groups
// of six uppercase hexadecimal digits), built from the test's name and tag.
func pickerAccountID(t *testing.T, tag string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name() + "/" + tag))
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	return h[0:6] + "-" + h[6:12] + "-" + h[12:18]
}

// pickerSources points the billing question's two sources at fakes: the
// account list is the one at f, and gcloud is off PATH (and would run nothing
// if it were not). The caller gets f back to register accounts and to count
// the calls: an account list nobody asked for is no request.
func pickerSources(t *testing.T, f *gcpfake.Billing) {
	t.Helper()
	old := billingSources
	billingSources = billing.Options{
		Credentials: billing.Credentials{Endpoint: f.URL, NoAuth: true},
		Gcloud:      billing.Gcloud{LookPath: func(string) (string, error) { return "", fs.ErrNotExist }},
	}
	t.Cleanup(func() { billingSources = old })
}

// noBillingSources is pickerSources with nothing registered at all.
// firstRunEnv calls it for every first run in this package
// (init_inputs_test.go), so that a test about something else can neither list
// a real account nor run a real gcloud — this repository's CI has one on PATH.
func noBillingSources(t *testing.T) {
	t.Helper()
	pickerSources(t, gcpfake.NewBilling(t))
}

// pickerRun is the wizard's first run, at (or without) a terminal, over a
// hand-built options value: a first run has no project config anywhere, so the
// wizard asks its questions, and stdin is the answers.
//
// It replaces the billing question's sources with fakes of its own (through
// firstRunEnv and noBillingSources), so a test that points them at its own
// fake calls pickerSources after this one, before gatherInputs.
func pickerRun(t *testing.T, terminal bool, stdin string, o *initOptions) (*initRun, *strings.Builder) {
	t.Helper()
	firstRunEnv(t)
	if terminal {
		fakeTerminal(t)
	}
	// The terminal these runs are at is a person's: a coding agent's session
	// (this test run's own, maybe) refuses typed confirmations, and one of the
	// tests below sets the marker again to pin that (newProjectRig clears them
	// for the same reason).
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	if o == nil {
		o = &initOptions{}
	}
	o.door = setupDoor.name
	out := &strings.Builder{}
	return &initRun{o: o, w: out, in: bufio.NewReader(strings.NewReader(stdin)), cmd: NewRootCmd()}, out
}

// pickerAnswers is the stdin that answers the wizard's questions before the
// billing one — the GCP project (there is none to suggest here, so Enter is
// not an answer), the region (Enter), whether to create the project (create),
// and the name (Enter) — with answer answering the billing question.
func pickerAnswers(answer string) string {
	return "proj-1234\n" + "\n" + setupAnswerCreate + "\n" + "\n" + answer + "\n"
}

// billingListCalls is how many times the account list was asked for: the
// question lists nothing when this is zero.
func billingListCalls(f *gcpfake.Billing) int {
	n := 0
	for _, q := range f.Requests() {
		if q.Path == "/v1/billingAccounts" && q.Method == http.MethodGet {
			n++
		}
	}
	return n
}

// TestPickerOffersOpenAccountsByNumber: at a real terminal the wizard's
// billing question lists the accounts the caller can link, numbered, with the
// display name and the ID — and nothing about a closed one, which cannot be
// linked. The question says which of its two sources answered, because the two
// can disagree (D6).
func TestPickerOffersOpenAccountsByNumber(t *testing.T) {
	f := gcpfake.NewBilling(t)
	first, second := pickerAccountID(t, "one"), pickerAccountID(t, "two")
	closed := pickerAccountID(t, "closed")
	f.AddAccountNamed(first, "the operator's own account", true)
	f.AddAccountNamed(second, "a second account", true)
	f.AddAccountNamed(closed, "one that was closed", false)
	// The rows are the source's own order, and the number is the row's place
	// in it: the fake (like the API) answers in ID order, so that is the order
	// the numbers are read against.
	offered := []string{first, second}
	sort.Strings(offered)
	r, out := pickerRun(t, true, pickerAnswers("1"), nil)
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.linkBilling != offered[0] {
		t.Errorf("the picked account: %q, want the first row (%q)\n%s", r.o.linkBilling, offered[0], out)
	}
	for _, want := range []string{"1)", "2)", first, second, "the operator's own account", "a second account", billing.SourceAPI.String()} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the question does not name %q:\n%s", want, out)
		}
	}
	if strings.Contains(out.String(), closed) {
		t.Errorf("a closed account is offered, and one that is closed cannot be linked:\n%s", out)
	}
	if n := billingListCalls(f); n != 1 {
		t.Errorf("the account list was asked for %d time(s), want once", n)
	}
}

// TestPickerPicksByNumberAndReasksOnNonsense: the answer is a number, and
// anything else is refused with what a number looks like rather than accepted
// or quietly taken for something.
func TestPickerPicksByNumberAndReasksOnNonsense(t *testing.T) {
	f := gcpfake.NewBilling(t)
	first, second := pickerAccountID(t, "one"), pickerAccountID(t, "two")
	f.AddAccountNamed(first, "the operator's own account", true)
	f.AddAccountNamed(second, "a second account", true)
	offered := []string{first, second}
	sort.Strings(offered)
	r, out := pickerRun(t, true, pickerAnswers("nonsense\n2"), nil)
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.linkBilling != offered[1] {
		t.Errorf("the picked account: %q, want the second row (%q)\n%s", r.o.linkBilling, offered[1], out)
	}
	if !strings.Contains(out.String(), "1 to 2") {
		t.Errorf("the refusal does not say which numbers the answer may be:\n%s", out)
	}

	// Out of range is the same refusal, and three of them refuse the run
	// rather than picking something.
	r, out = pickerRun(t, true, pickerAnswers("0\n3\n9"), nil)
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err == nil {
		t.Errorf("three unusable answers did not refuse the run:\n%s", out)
	}
	if r.o.linkBilling != "" {
		t.Errorf("an account was picked out of no usable answer: %q", r.o.linkBilling)
	}
}

// TestPickerEnterOnSeveralAccountsAsksAgain: one account is the answer Enter
// takes (below); with more than one there is no default, and an empty line is
// no answer at all — the question asks again rather than linking an account
// nobody picked. It is the account the run links, and a link may incur
// charges, so Enter must never mean "the first one".
func TestPickerEnterOnSeveralAccountsAsksAgain(t *testing.T) {
	f := gcpfake.NewBilling(t)
	first, second := pickerAccountID(t, "one"), pickerAccountID(t, "two")
	f.AddAccountNamed(first, "the operator's own account", true)
	f.AddAccountNamed(second, "a second account", true)
	offered := []string{first, second}
	sort.Strings(offered)

	// Enter first, then the second account's number: what is linked is what
	// was picked, not what Enter would have taken.
	r, out := pickerRun(t, true, pickerAnswers("\n2"), nil)
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.linkBilling != offered[1] {
		t.Errorf("Enter on a list of two left the question answered with %q, want the number typed after it (%q)\n%s", r.o.linkBilling, offered[1], out)
	}
	if n := strings.Count(out.String(), "Billing account"); n < 2 {
		t.Errorf("the empty answer was taken as an answer: the question was shown %d time(s), want it asked again\n%s", n, out)
	}
	if strings.Contains(out.String(), "[1]") {
		t.Errorf("the question offers a default it does not have — with two accounts there is nothing for Enter to take:\n%s", out)
	}

	// Enter with nothing after it is not an answer either: the run is refused
	// with nothing linked, rather than linked to the first account.
	r, out = pickerRun(t, true, pickerAnswers("\n\n\n"), nil)
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err == nil {
		t.Errorf("three empty answers answered the question:\n%s", out)
	}
	if r.o.linkBilling != "" {
		t.Errorf("an account was linked out of an empty answer: %q", r.o.linkBilling)
	}
}

// TestPickerSingleAccountIsTheDefault: one account is the answer Enter takes,
// so an operator with one account types nothing.
func TestPickerSingleAccountIsTheDefault(t *testing.T) {
	f := gcpfake.NewBilling(t)
	only := pickerAccountID(t, "only")
	f.AddAccountNamed(only, "the operator's own account", true)
	r, out := pickerRun(t, true, pickerAnswers(""), nil)
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.linkBilling != only {
		t.Errorf("Enter did not take the single account: %q, want %q\n%s", r.o.linkBilling, only, out)
	}
	if !strings.Contains(out.String(), "[1]") {
		t.Errorf("the question does not show the single account as the default:\n%s", out)
	}
}

// TestPickerNoAccountsNamesTheConsoleAndTheGuidedLine: a source answered and
// there is nothing to offer — no account at all, or only closed ones. The
// question says the one clear thing about an account that does not exist yet
// and prints today's guided line, and records nothing, so the run links
// nothing and is otherwise exactly today's run.
func TestPickerNoAccountsNamesTheConsoleAndTheGuidedLine(t *testing.T) {
	f := gcpfake.NewBilling(t)
	f.AddAccountNamed(pickerAccountID(t, "closed"), "one that was closed", false)
	r, out := pickerRun(t, true, pickerAnswers(""), nil)
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.linkBilling != "" {
		t.Errorf("an account was recorded with none to offer: %q", r.o.linkBilling)
	}
	if !strings.Contains(out.String(), "console.cloud.google.com/billing") {
		t.Errorf("the question does not name the console:\n%s", out)
	}
	if want := billing.Guided("proj-1234"); !strings.Contains(out.String(), want) {
		t.Errorf("the question does not print today's guided line %q:\n%s", want, out)
	}
}

// TestPickerNoneLinksNothing: an operator who wants to link no account now is
// not trapped at the question. The answer `none` records nothing and leaves
// today's guided line, exactly as the empty list does, and the rest of the run
// is unchanged.
func TestPickerNoneLinksNothing(t *testing.T) {
	f := gcpfake.NewBilling(t)
	f.AddAccountNamed(pickerAccountID(t, "one"), "the operator's own account", true)
	f.AddAccountNamed(pickerAccountID(t, "two"), "a second account", true)
	r, out := pickerRun(t, true, pickerAnswers(billingNoAccount), nil)
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.linkBilling != "" {
		t.Errorf("the answer %q recorded the account %q\n%s", billingNoAccount, r.o.linkBilling, out)
	}
	if want := billing.Guided("proj-1234"); !strings.Contains(out.String(), want) {
		t.Errorf("the answer %q does not leave today's guided line %q:\n%s", billingNoAccount, want, out)
	}
}

// TestPickerNeverRunsUnderJSON: --json is a document whose reader is a script,
// and it is the first of the four conditions canType() names. Nothing is
// listed, no account reaches the output, and the run is today's run — the same
// refusal, in the same words.
func TestPickerNeverRunsUnderJSON(t *testing.T) {
	f := gcpfake.NewBilling(t)
	acct := pickerAccountID(t, "one")
	f.AddAccountNamed(acct, "the operator's own account", true)

	// Every other input comes from the flags, so the run is past the questions
	// that would refuse it: the only thing between this run and the account
	// list is the condition under test.
	r, out := pickerRun(t, true, "", &initOptions{asJSON: true, createProject: true, name: "aurora",
		cloud: cloudOptions{gcpProject: "proj-1234", region: defaultRegion}})
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out.Len() != 0 {
		t.Errorf("--json is a document, and the wizard wrote a question into it:\n%s", out)
	}
	if r.o.linkBilling != "" || billingListCalls(f) != 0 {
		t.Errorf("the picker ran under --json: linkBilling %q, %d list call(s)\n%s", r.o.linkBilling, billingListCalls(f), out)
	}
}

// TestPickerNeverRunsNonInteractive: --non-interactive is a run nobody answers,
// and it is the second of the four conditions.
func TestPickerNeverRunsNonInteractive(t *testing.T) {
	f := gcpfake.NewBilling(t)
	acct := pickerAccountID(t, "one")
	f.AddAccountNamed(acct, "the operator's own account", true)

	r, out := pickerRun(t, true, "", &initOptions{nonInteractive: true, createProject: true, name: "aurora",
		cloud: cloudOptions{gcpProject: "proj-1234", region: defaultRegion}})
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out.Len() != 0 {
		t.Errorf("--non-interactive is a run nobody answers, and the wizard asked something:\n%s", out)
	}
	if r.o.linkBilling != "" || billingListCalls(f) != 0 {
		t.Errorf("the picker ran with --non-interactive: %q, %d list call(s)\n%s", r.o.linkBilling, billingListCalls(f), out)
	}
}

// TestPickerNeverRunsInAnAgentSession: a coding agent's session is the third
// condition, and the one the wizard's other questions do not share — a
// person's own IDE terminal can carry the marker too (the Claude Code
// extension sets CLAUDE_CODE_SSE_PORT), so the run still asks for the inputs
// it cannot proceed without. What it must not do is list the operator's
// accounts: that answer is about to be read out of a transcript.
func TestPickerNeverRunsInAnAgentSession(t *testing.T) {
	f := gcpfake.NewBilling(t)
	acct := pickerAccountID(t, "one")
	f.AddAccountNamed(acct, "the operator's own account", true)
	r, out := pickerRun(t, true, pickerAnswers("1"), nil)
	pickerSources(t, f)
	// After pickerRun, which clears them for a person's terminal: this is the
	// same run with the marker set.
	t.Setenv(agentMarkers[0], "1")
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "GCP project ID") {
		t.Errorf("the run asks nothing at all, and only the billing question is an agent's to refuse:\n%s", out)
	}
	if r.o.linkBilling != "" || billingListCalls(f) != 0 || strings.Contains(out.String(), acct) {
		t.Errorf("the picker ran in an agent session: %q, %d list call(s)\n%s", r.o.linkBilling, billingListCalls(f), out)
	}
}

// TestPickerNeverRunsWithoutATerminal: no terminal is the fourth condition,
// and a pipe is what has none.
func TestPickerNeverRunsWithoutATerminal(t *testing.T) {
	f := gcpfake.NewBilling(t)
	acct := pickerAccountID(t, "one")
	f.AddAccountNamed(acct, "the operator's own account", true)

	r, out := pickerRun(t, false, "", &initOptions{createProject: true, name: "aurora",
		cloud: cloudOptions{gcpProject: "proj-1234", region: defaultRegion}})
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out.Len() != 0 {
		t.Errorf("a pipe is no terminal, and the wizard asked something into it:\n%s", out)
	}
	if r.o.linkBilling != "" || billingListCalls(f) != 0 {
		t.Errorf("the picker ran without a terminal: %q, %d list call(s)\n%s", r.o.linkBilling, billingListCalls(f), out)
	}
}

// TestPickerNeverRunsWhenTheAnswerIsAdopt: the question is asked only where
// its answer is one this run acts on. On an answer of adoption the project
// stage skips before it reads anything (init_project.go's Check), so a
// --link-billing this run recorded would reach no link at all: a person who
// answered it would have been asked something the run then ignored. Nothing is
// listed, and the account they have is not the run's to read either.
func TestPickerNeverRunsWhenTheAnswerIsAdopt(t *testing.T) {
	f := gcpfake.NewBilling(t)
	acct := pickerAccountID(t, "one")
	f.AddAccountNamed(acct, "the operator's own account", true)

	r, out := pickerRun(t, true, "proj-1234\n"+"\n"+setupAnswerAdopt+"\n"+"\n"+"1\n", nil)
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.createProject {
		t.Fatalf("the answer %q recorded creation:\n%s", setupAnswerAdopt, out)
	}
	if r.o.linkBilling != "" || billingListCalls(f) != 0 || strings.Contains(out.String(), acct) {
		t.Errorf("the picker ran on an answer of adoption: %q, %d list call(s)\n%s", r.o.linkBilling, billingListCalls(f), out)
	}
}

// TestPickerNeverReplacesALinkBillingAlreadyGiven: --link-billing is the flag
// that answers this question without a terminal (and the documented way to
// link billing to a project this run did not create). A run that passed it has
// answered: the question is not asked, nothing is listed, and the account the
// operator named is the one the run keeps.
func TestPickerNeverReplacesALinkBillingAlreadyGiven(t *testing.T) {
	f := gcpfake.NewBilling(t)
	other := pickerAccountID(t, "other")
	f.AddAccountNamed(other, "another account", true)

	given := pickerAccountID(t, "given")
	r, out := pickerRun(t, true, pickerAnswers("1"), &initOptions{linkBilling: given})
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.linkBilling != given {
		t.Errorf("the account the run was given became %q, want %q\n%s", r.o.linkBilling, given, out)
	}
	if billingListCalls(f) != 0 || strings.Contains(out.String(), other) {
		t.Errorf("the question listed the accounts, and --link-billing had answered it: %d call(s)\n%s", billingListCalls(f), out)
	}
}

// TestPickerNeverRunsUnderYes: --yes is a run that wants no question answered,
// so the wizard's other questions are not asked either (canAsk), and neither
// is this one. Linking billing under --yes stays what it is today:
// --link-billing on the command line, with its own typed confirmation, which
// --yes never covers.
func TestPickerNeverRunsUnderYes(t *testing.T) {
	f := gcpfake.NewBilling(t)
	acct := pickerAccountID(t, "one")
	f.AddAccountNamed(acct, "the operator's own account", true)

	o := &initOptions{yes: true, createProject: true, name: "aurora",
		cloud: cloudOptions{gcpProject: "proj-1234", region: defaultRegion}}
	r, out := pickerRun(t, true, "", o)
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out.Len() != 0 {
		t.Errorf("--yes wants no question answered, and the wizard asked one:\n%s", out)
	}
	if r.o.linkBilling != "" || billingListCalls(f) != 0 {
		t.Errorf("the picker ran under --yes: %q, %d list call(s)\n%s", r.o.linkBilling, billingListCalls(f), out)
	}
}

// TestPickerNeverRunsUnderPrintVars: --print-vars makes stdout the Terraform
// variables and nothing else, so the account list must not be able to reach it.
// It is the one condition of the seven asksBilling names that canType() does
// not already cover — a person's terminal with --print-vars still passes all
// four of those (it is not --non-interactive, not --json, not an agent's
// session, and stdin is a terminal) — which is why the question names the flag
// itself beside --yes.
func TestPickerNeverRunsUnderPrintVars(t *testing.T) {
	f := gcpfake.NewBilling(t)
	acct := pickerAccountID(t, "one")
	f.AddAccountNamed(acct, "the operator's own account", true)

	// Every other input comes from the flags, so the run is past the questions
	// that would refuse it: the only thing between this run and the account
	// list is the condition under test.
	r, out := pickerRun(t, true, "", &initOptions{printVars: true, createProject: true, name: "aurora",
		cloud: cloudOptions{gcpProject: "proj-1234", region: defaultRegion}})
	pickerSources(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars makes stdout the variables alone, and the wizard wrote a question into it:\n%s", out)
	}
	if r.o.linkBilling != "" || billingListCalls(f) != 0 {
		t.Errorf("the picker ran under --print-vars: linkBilling %q, %d list call(s)\n%s", r.o.linkBilling, billingListCalls(f), out)
	}
}

// TestPickerNoSourceSaysSoAndRecordsNothing: the API refused and gcloud is not
// on PATH (W4.2's fallback), so there is nothing to offer. The question says
// that in one line and records nothing; the run falls back to today's text,
// which the project stage prints with the guided line (F6).
//
// The line is value-free — no account, no project, no API message, not even
// the refusal's own words, which can name a project, a permission or an
// account — and it is what tells a run that could not read the list apart from
// a run with no picker at all: Check 111 records which source answered, and
// here the answer is neither.
func TestPickerNoSourceSaysSoAndRecordsNothing(t *testing.T) {
	f := gcpfake.NewBilling(t)
	acct := pickerAccountID(t, "one")
	f.AddAccountNamed(acct, "the operator's own account", true)
	f.RefusePath("/v1/billingAccounts", http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "The caller does not have permission")
	r, out := pickerRun(t, true, pickerAnswers(""), nil)
	pickerSources(t, f) // a refusing API and no gcloud on PATH
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.linkBilling != "" {
		t.Errorf("an account was recorded with no source answering: %q", r.o.linkBilling)
	}
	if !strings.Contains(out.String(), "could not be listed") {
		t.Errorf("the question does not say the accounts could not be listed, so the operator cannot tell this run from one with no picker at all:\n%s", out)
	}
	// Value-free: nothing of the answer that could not be read is repeated.
	for _, leak := range []string{
		acct,
		"the operator's own account",
		"billingAccounts/",
		"proj-1234",                           // the project being set up
		"The caller does not have permission", // the API's own words
		"1)",                                  // a numbered list
	} {
		if strings.Contains(out.String(), leak) {
			t.Errorf("the line about the unreadable list carries %q:\n%s", leak, out)
		}
	}
}

// TestPickerSaysWhichSourceAnswered: the two sources can disagree — gcloud
// asks with its own account and its own project — so the question says which
// one answered (D6(i)). The API is preferred, and gcloud is asked only when
// the API could not answer (W4.2).
func TestPickerSaysWhichSourceAnswered(t *testing.T) {
	t.Run("the Cloud Billing API", func(t *testing.T) {
		f := gcpfake.NewBilling(t)
		acct := pickerAccountID(t, "one")
		f.AddAccountNamed(acct, "the operator's own account", true)
		r, out := pickerRun(t, true, pickerAnswers("1"), nil)
		pickerSources(t, f)
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if !strings.Contains(out.String(), billing.SourceAPI.String()) {
			t.Errorf("the question does not say the Cloud Billing API answered:\n%s", out)
		}
	})

	t.Run("gcloud", func(t *testing.T) {
		f := gcpfake.NewBilling(t)
		f.RefusePath("/v1/billingAccounts", http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "The caller does not have permission")
		acct := pickerAccountID(t, "gcloud")
		r, out := pickerRun(t, true, pickerAnswers("1"), nil)
		old := billingSources
		billingSources = billing.Options{
			Credentials: billing.Credentials{Endpoint: f.URL, NoAuth: true},
			Gcloud: billing.Gcloud{
				LookPath: func(file string) (string, error) { return "/usr/bin/" + file, nil },
				Run: func(context.Context, ...string) ([]byte, error) {
					// The columns gcloud's table has, with a name short
					// enough that the OPEN cell lines up with its header.
					return []byte(fmt.Sprintf("%-22s%-34s%-7s%s\n%-22s%-34s%-7s%s\n",
						"ACCOUNT_ID", "NAME", "OPEN", "MASTER_ACCOUNT_ID",
						acct, "own account", "True", "")), nil
				},
			},
		}
		t.Cleanup(func() { billingSources = old })
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if r.o.linkBilling != acct {
			t.Errorf("the picked account: %q, want the one gcloud listed (%q)\n%s", r.o.linkBilling, acct, out)
		}
		if !strings.Contains(out.String(), billing.SourceGcloud.String()) {
			t.Errorf("the question does not say gcloud answered:\n%s", out)
		}
	})
}

// TestPickerOutputHasNoAccountUnderJSON: the run under --json is a document
// whose reader is a script. Nothing about an account reaches it — not in the
// object, not on stderr — and the list is not asked for at all (Global
// Constraint 8).
func TestPickerOutputHasNoAccountUnderJSON(t *testing.T) {
	acct := pickerAccountID(t, "one")
	full := gcpfake.NewBilling(t)
	full.AddAccountNamed(acct, "the operator's own account", true)

	// Two runs of one command, in one environment, that differ in nothing but
	// the accounts the question would have listed: with the picker out of the
	// way, the document and the refusal are the same bytes.
	firstRunEnv(t)
	run := func(f *gcpfake.Billing) (string, string, error) {
		pickerSources(t, f)
		return executeStdin(t, "", "setup", "--json", "--non-interactive")
	}
	withAcct, withErrOut, withErr := run(full)
	without, withoutErrOut, withoutErr := run(gcpfake.NewBilling(t))
	if withAcct != without || withErrOut != withoutErrOut {
		t.Errorf("--json's output changed with the account list, and nothing may be listed there:\n%s\n---\n%s", withAcct, without)
	}
	if (withErr == nil) != (withoutErr == nil) {
		t.Errorf("the run's outcome changed with the account list: %v, %v", withErr, withoutErr)
	}
	for _, got := range []string{withAcct, withErrOut} {
		if strings.Contains(got, acct) {
			t.Errorf("an account is printed under --json:\n%s", got)
		}
	}
	if billingListCalls(full) != 0 {
		t.Errorf("the account list was asked for under --json: %d call(s)", billingListCalls(full))
	}
}

// TestPickerStillTypesTheAccountBeforeLinking: what the question records is
// --link-billing's own field, so the account still goes through the project
// stage's typed banner before anything is linked, and the typed id — not the
// pick — is what links it. A wrong id refuses, and links nothing.
func TestPickerStillTypesTheAccountBeforeLinking(t *testing.T) {
	picked := pickerAccountID(t, "picked")

	t.Run("the picked id typed", func(t *testing.T) {
		r := newProjectRig(t)
		r.bill.AddAccountNamed(picked, "the operator's own account", true)
		// The two typed ids the run asks for are the project's and the
		// account's; the review that follows is a third, and it is the other
		// tests' (the link is made before it, which is what this pins).
		out, _, _ := r.run(t, true, newProj+"\n"+picked+"\n", "--create-project", "--link-billing", picked)
		if !strings.Contains(out, "this LINKS billing account "+picked) || !strings.Contains(out, "to link it") {
			t.Errorf("the picked account did not go through the typed banner:\n%s", out)
		}
		if links := r.bill.Links(); len(links) != 1 || links[0].Account != picked {
			t.Errorf("links %v, want the one account the typed id confirmed", links)
		}
	})

	t.Run("another id typed", func(t *testing.T) {
		r := newProjectRig(t)
		r.bill.AddAccountNamed(picked, "the operator's own account", true)
		out, _, err := r.run(t, true, newProj+"\n"+billingAcc+"\n", "--create-project", "--link-billing", picked)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("a wrong typed id linked the account (%v):\n%s", err, out)
		}
		if len(r.bill.Links()) != 0 {
			t.Errorf("a wrong typed id linked %v", r.bill.Links())
		}
		if !strings.Contains(out, "not confirmed") {
			t.Errorf("the refusal does not say the account was not confirmed:\n%s", out)
		}
	})
}

// TestPickerStoresNothing: the account picked is --link-billing's field and
// nothing else. The question keeps no list between runs (a second run asks
// again), writes no file, and the local config a run writes with an account
// picked is byte-identical to the one it writes with none: localcfg.Config has
// no field for a billing account, and the write path reads none (D6: what is
// picked is not stored anywhere but where it is today).
func TestPickerStoresNothing(t *testing.T) {
	f := gcpfake.NewBilling(t)
	picked := pickerAccountID(t, "picked")
	f.AddAccountNamed(picked, "the operator's own account", true)

	// The same stdin twice: once where a source answers (the picker runs), and
	// once where neither source answers (it does not, and the run is today's).
	// The two options values must differ in the account and in nothing else.
	//
	// What the question may leave behind is watched in the two places a Go run
	// can leave it: the filesystem — the whole root a first run is given, HOME
	// and every XDG root and the working directory under it, so a file written
	// anywhere this run may write is seen — and stderr. The account list is
	// the run's own stdout and nowhere else: not a file, not stderr.
	asked := func(seam func(*testing.T)) (*initOptions, string) {
		t.Helper()
		root := firstRunEnv(t)
		fakeTerminal(t)
		seam(t)
		before := filesUnder(t, root)
		// stderr is captured outside that root, so the capture is not itself a
		// file the snapshot below would report as the run's own writing.
		errFile, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
		if err != nil {
			t.Fatal(err)
		}
		oldErr := os.Stderr
		os.Stderr = errFile
		t.Cleanup(func() { os.Stderr = oldErr }) // a panic in the run must not leave the test binary's stderr pointed at a closed file
		o := &initOptions{door: setupDoor.name}
		r := &initRun{o: o, w: &strings.Builder{}, in: bufio.NewReader(strings.NewReader(pickerAnswers("1"))), cmd: NewRootCmd()}
		gerr := r.gatherInputs(t.Context())
		os.Stderr = oldErr
		if err := errFile.Close(); err != nil {
			t.Fatal(err)
		}
		if gerr != nil {
			t.Fatal(gerr)
		}
		got := filesUnder(t, root)
		if !reflect.DeepEqual(before, got) {
			t.Errorf("the question wrote under the run's own root (%s): %v, want %v", root, got, before)
		}
		for _, p := range got {
			b, rerr := os.ReadFile(filepath.Join(root, p))
			if rerr != nil {
				continue
			}
			if strings.Contains(string(b), picked) || strings.Contains(string(b), "billingAccounts/") {
				t.Errorf("%s carries the account list the question read", p)
			}
		}
		captured, err := os.ReadFile(errFile.Name())
		if err != nil {
			t.Fatal(err)
		}
		return o, string(captured)
	}
	answered, errOut := asked(func(t *testing.T) { pickerSources(t, f) })
	control, controlErr := asked(noBillingSources)
	for _, out := range []string{errOut, controlErr} {
		for _, leak := range []string{picked, "the operator's own account", "billingAccounts/", "1)"} {
			if strings.Contains(out, leak) {
				t.Errorf("the account list reached stderr (%q):\n%s", leak, out)
			}
		}
	}
	if answered.linkBilling != picked {
		t.Fatalf("the picker recorded %q, want %q", answered.linkBilling, picked)
	}
	got, want := *answered, *control
	got.linkBilling, want.linkBilling = "", ""
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the question changed more than --link-billing:\n%+v\nwant\n%+v", got, want)
	}

	// A second question lists again: nothing is kept between runs.
	if second, _ := asked(func(t *testing.T) { pickerSources(t, f) }); second.linkBilling != picked {
		t.Errorf("the second run's answer: %q, want %q", second.linkBilling, picked)
	}
	if n := billingListCalls(f); n != 2 {
		t.Errorf("the account list was asked for %d time(s) in all, want 2: the list is not kept", n)
	}

	// And the config written does not depend on the answer: localcfg has no
	// field for a billing account.
	dir := t.TempDir()
	write := func(account string) string {
		t.Helper()
		r := &initRun{o: &initOptions{planOnly: true, linkBilling: account}, w: &strings.Builder{},
			in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}
		lc, err := newProjectConfig("aurora", "proj-1234", "us-east5")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "fugaro-"+strings.ReplaceAll(account, "-", "")+".yaml")
		if err := r.writeConfig(t.Context(), lc, infra.InstallationSpec{StateBucket: "fugaro-state-proj-1234"},
			infra.InstallationOutputs{}, path, nil, true); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	with, without := write(picked), write("")
	if with != without {
		t.Errorf("the local config changed with the account picked:\n%s\nwant\n%s", with, without)
	}
	if strings.Contains(with, picked) {
		t.Errorf("the account reached the local config:\n%s", with)
	}
}

// filesUnder is every path under root, sorted: what a run wrote is the
// difference between two of these.
func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil {
			out = append(out, strings.TrimPrefix(p, root))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// The rest of this file is D3 (task W4.4): the Cloud Billing API being off on
// the credentials' quota project. The API's own answer says which project that
// is, and the wizard does one of two things with it — offers to turn the API on
// in the project being set up, behind a typed confirmation that --yes never
// covers, or refuses and names the line to run elsewhere and who to ask — and
// neither of them replaces the fallback (W4.2) or today's guided line (F6).

// pickerQuotaOther is the credentials' quota project in D3's second case: not
// the project this run sets up, and not a project of anybody's — a name of the
// project-ID shape, like the account ids above, and never a literal that looks
// like a credential.
const pickerQuotaOther = "quota-other-1234"

// pickerBillingAPI is the API these tests turn off: the one the account list
// needs, spelled as Google spells it. It is the test's own copy on purpose — a
// test that turned off whatever name the code happens to use would pass on any
// name at all, and the enable's own argv is compared with internal/billing's
// line below, which is what pins the two spellings together.
const pickerBillingAPI = "cloudbilling.googleapis.com"

// pickerDisabledOn turns the Cloud Billing API off on the billing fake, with
// consumer as the credentials' quota project: the state D3's three cases start
// from. It is internal/billing's own way of setting the state up (its tests use
// the same fake), and the returned Service Usage fake is where "was anything
// enabled" is read — an enable of this API on that endpoint turns it back on.
func pickerDisabledOn(t *testing.T, f *gcpfake.Billing, consumer string) *gcpfake.ServiceUsage {
	t.Helper()
	su := gcpfake.NewServiceUsage(t)
	su.Consumer = consumer
	su.Disable(pickerBillingAPI, f.Server)
	return su
}

// pickerEnableLine is the enable line internal/billing's own ErrDisabled
// carries for the state f is in. Both D3's refusal and the argv the wizard runs
// are that line, so the tests below cannot pass on a line the cli invented.
func pickerEnableLine(t *testing.T, f *gcpfake.Billing) string {
	t.Helper()
	_, err := billing.List(context.Background(), billing.Credentials{Endpoint: f.URL, NoAuth: true})
	var e *billing.ErrDisabled
	if !errors.As(err, &e) {
		t.Fatalf("the fake is not answering SERVICE_DISABLED: %v", err)
	}
	return e.EnableCommand
}

// pickerGcloudRunner is the wizard's gcloud as these tests have it: present on
// PATH, its billing list maimed by hand — the shape Check 111's wrapper has, so
// that the API being off is what the question answers — and every other command
// recorded rather than run. It returns the recorded argvs, which is where
// "enables nothing" and "runs this exact line" are both read.
func pickerGcloudRunner(t *testing.T, f *gcpfake.Billing) *[][]string {
	t.Helper()
	ran := &[][]string{}
	old := billingSources
	billingSources = billing.Options{
		Credentials: billing.Credentials{Endpoint: f.URL, NoAuth: true},
		Gcloud: billing.Gcloud{
			LookPath: func(file string) (string, error) { return "/usr/bin/" + file, nil },
			Run: func(_ context.Context, args ...string) ([]byte, error) {
				*ran = append(*ran, append([]string{}, args...))
				if len(args) > 0 && args[0] == "billing" {
					return nil, errors.New("gcloud: billing accounts list is unavailable in this run")
				}
				return nil, nil
			},
		},
	}
	t.Cleanup(func() { billingSources = old })
	return ran
}

// pickerEnableArgv is the argv the enable line names: the line without the
// binary's own name, which is how the wizard runs it.
func pickerEnableArgv(t *testing.T, f *gcpfake.Billing) []string {
	t.Helper()
	return strings.Fields(strings.TrimPrefix(pickerEnableLine(t, f), billing.GcloudBin+" "))
}

// pickerEnables is the enables among what the wizard ran: the fallback's own
// list call is a command too, and "nothing was enabled" is about the one that
// turns an API on.
func pickerEnables(ran *[][]string) [][]string {
	var out [][]string
	for _, args := range *ran {
		if len(args) > 0 && args[0] == "services" {
			out = append(out, args)
		}
	}
	return out
}

// TestRefusalWhenTheQuotaProjectIsAnotherProject: D3's second case, and the
// red step of W4.4. The credentials' quota project is not the project this run
// sets up, so the API being off there is not this run's to fix: it refuses —
// naming the quota project, the exact line to run there and who to ask — and
// enables nothing anywhere. An API turned on on a project none of the run's
// answers named is exactly the surprise F8's refusals exist to prevent, and the
// operator still gets the fallback (W4.2) or today's guided line (F6).
func TestRefusalWhenTheQuotaProjectIsAnotherProject(t *testing.T) {
	f := gcpfake.NewBilling(t)
	su := pickerDisabledOn(t, f, "projects/"+pickerQuotaOther)
	r, out := pickerRun(t, true, pickerAnswers(""), nil)
	ran := pickerGcloudRunner(t, f)
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	line := pickerEnableLine(t, f)
	if !strings.Contains(out.String(), line) {
		t.Errorf("the refusal does not carry the exact line %q:\n%s", line, out)
	}
	if !strings.Contains(out.String(), pickerQuotaOther) {
		t.Errorf("the refusal does not name the quota project %q:\n%s", pickerQuotaOther, out)
	}
	if !strings.Contains(strings.ToLower(out.String()), "ask an owner") {
		t.Errorf("the refusal does not say who to ask:\n%s", out)
	}
	if strings.Contains(out.String(), "CONFIRM") {
		t.Errorf("an API on another project was offered for enabling:\n%s", out)
	}
	if len(pickerEnables(ran)) != 0 || len(su.Enables()) != 0 {
		t.Errorf("the refusal enabled something: gcloud %v, Service Usage %v", pickerEnables(ran), su.Enables())
	}
	if su.Enabled(pickerBillingAPI) {
		t.Errorf("%s is enabled in the quota project after a refusal", pickerBillingAPI)
	}
	if r.o.linkBilling != "" {
		t.Errorf("the refusal recorded an account: %q", r.o.linkBilling)
	}
}

// TestEnableOfferedOnlyWhenTheQuotaProjectIsTheProject: D3's first case. The
// API is off on the project this run is setting up, which is the one project
// the offer may name: the wizard asks, in one typed confirmation naming the
// project and the API, and only that answer turns the API on. Where the quota
// project is another one there is no offer at all (the refusal above).
func TestEnableOfferedOnlyWhenTheQuotaProjectIsTheProject(t *testing.T) {
	t.Run("the project being set up", func(t *testing.T) {
		f := gcpfake.NewBilling(t)
		pickerDisabledOn(t, f, "projects/proj-1234")
		r, out := pickerRun(t, true, pickerAnswers("proj-1234"), nil)
		ran := pickerGcloudRunner(t, f)
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if !strings.Contains(out.String(), "CONFIRM") {
			t.Fatalf("the API being off on the project being set up was not offered:\n%s", out)
		}
		for _, want := range []string{pickerBillingAPI, "proj-1234"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("the offer does not name %q:\n%s", want, out)
			}
		}
		want, got := pickerEnableArgv(t, f), pickerEnables(ran)
		if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
			t.Errorf("the enable the wizard ran: %v, want %v (the line internal/billing carries)", got, want)
		}
	})

	t.Run("another project", func(t *testing.T) {
		f := gcpfake.NewBilling(t)
		pickerDisabledOn(t, f, "projects/"+pickerQuotaOther)
		r, out := pickerRun(t, true, pickerAnswers("proj-1234"), nil)
		ran := pickerGcloudRunner(t, f)
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if strings.Contains(out.String(), "CONFIRM") || len(pickerEnables(ran)) != 0 {
			t.Errorf("the offer was made for another project's API (ran %v):\n%s", pickerEnables(ran), out)
		}
	})
}

// TestEnablingIsTypedAndNeverUnderYes: the offer is a typed step, like the
// project stage's banners — a word that is not the project's own ID leaves the
// API off — and --yes never covers it: a --yes run is a run that asked for no
// question, so it is not offered the enable at all (asksBilling), and nothing
// about a project changes there.
func TestEnablingIsTypedAndNeverUnderYes(t *testing.T) {
	t.Run("the project's ID is what enables it", func(t *testing.T) {
		for _, answer := range []string{"", "yes", "proj-12345"} {
			f := gcpfake.NewBilling(t)
			pickerDisabledOn(t, f, "projects/proj-1234")
			r, out := pickerRun(t, true, pickerAnswers(answer), nil)
			ran := pickerGcloudRunner(t, f)
			if err := r.gatherInputs(t.Context()); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if !strings.Contains(out.String(), "CONFIRM") {
				t.Errorf("the answer %q was not asked for as a confirmation:\n%s", answer, out)
			}
			if len(pickerEnables(ran)) != 0 {
				t.Errorf("the answer %q enabled the API: %v\n%s", answer, pickerEnables(ran), out)
			}
			if !strings.Contains(out.String(), pickerEnableLine(t, f)) {
				t.Errorf("the answer %q left no line to run instead:\n%s", answer, out)
			}
		}
	})

	t.Run("--yes", func(t *testing.T) {
		f := gcpfake.NewBilling(t)
		pickerDisabledOn(t, f, "projects/proj-1234")
		o := &initOptions{yes: true, createProject: true, name: "aurora",
			cloud: cloudOptions{gcpProject: "proj-1234", region: defaultRegion}}
		r, out := pickerRun(t, true, "", o)
		ran := pickerGcloudRunner(t, f)
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if out.Len() != 0 {
			t.Errorf("--yes wants no question answered, and the wizard asked one:\n%s", out)
		}
		if len(pickerEnables(ran)) != 0 {
			t.Errorf("--yes enabled the API: %v", pickerEnables(ran))
		}
	})
}

// TestNothingIsEnabledWithoutAnAnswer: nothing enables the API but an answer
// this run can act on. An empty line is the way out of the question, a run with
// no line at all (EOF) has answered nothing, and a gcloud that is not on PATH —
// or an enable that fails — leaves the project exactly as it was, with the line
// to run by hand in front of the operator.
func TestNothingIsEnabledWithoutAnAnswer(t *testing.T) {
	t.Run("an empty line", func(t *testing.T) {
		f := gcpfake.NewBilling(t)
		pickerDisabledOn(t, f, "projects/proj-1234")
		r, out := pickerRun(t, true, pickerAnswers(""), nil)
		ran := pickerGcloudRunner(t, f)
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if len(pickerEnables(ran)) != 0 {
			t.Errorf("an empty answer enabled the API: %v", pickerEnables(ran))
		}
		if !strings.Contains(out.String(), pickerEnableLine(t, f)) {
			t.Errorf("the decline left no line to run instead:\n%s", out)
		}
	})

	t.Run("no line at all", func(t *testing.T) {
		f := gcpfake.NewBilling(t)
		pickerDisabledOn(t, f, "projects/proj-1234")
		// The question is the last one, so stdin ends at the offer's prompt.
		r, out := pickerRun(t, true, "proj-1234\n\n"+setupAnswerCreate+"\n\n", nil)
		ran := pickerGcloudRunner(t, f)
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if len(pickerEnables(ran)) != 0 {
			t.Errorf("a run that answered nothing enabled the API: %v", pickerEnables(ran))
		}
	})

	t.Run("gcloud is not on PATH", func(t *testing.T) {
		f := gcpfake.NewBilling(t)
		pickerDisabledOn(t, f, "projects/proj-1234")
		r, out := pickerRun(t, true, pickerAnswers("proj-1234"), nil)
		ran := &[][]string{}
		old := billingSources
		billingSources = billing.Options{
			Credentials: billing.Credentials{Endpoint: f.URL, NoAuth: true},
			Gcloud: billing.Gcloud{
				LookPath: func(string) (string, error) { return "", fs.ErrNotExist },
				Run: func(_ context.Context, args ...string) ([]byte, error) {
					*ran = append(*ran, append([]string{}, args...))
					return nil, nil
				},
			},
		}
		t.Cleanup(func() { billingSources = old })
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if len(*ran) != 0 {
			t.Errorf("a run without gcloud ran %v", *ran)
		}
		if len(pickerEnables(ran)) != 0 {
			t.Errorf("a run without gcloud enabled the API: %v", pickerEnables(ran))
		}
		if !strings.Contains(out.String(), pickerEnableLine(t, f)) {
			t.Errorf("the run without gcloud left no line to run instead:\n%s", out)
		}
	})

	t.Run("the enable fails", func(t *testing.T) {
		f := gcpfake.NewBilling(t)
		pickerDisabledOn(t, f, "projects/proj-1234")
		r, out := pickerRun(t, true, pickerAnswers("proj-1234"), nil)
		old := billingSources
		billingSources = billing.Options{
			Credentials: billing.Credentials{Endpoint: f.URL, NoAuth: true},
			Gcloud: billing.Gcloud{
				LookPath: func(file string) (string, error) { return "/usr/bin/" + file, nil },
				Run: func(context.Context, ...string) ([]byte, error) {
					return nil, errors.New("gcloud: PERMISSION_DENIED")
				},
			},
		}
		t.Cleanup(func() { billingSources = old })
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatalf("a failed enable ended the run: %v\n%s", err, out)
		}
		if !strings.Contains(out.String(), "PERMISSION_DENIED") {
			t.Errorf("the failed enable is not reported:\n%s", out)
		}
	})
}

// TestThePickerStillWorksWithNoAPI: the API being off is not the run failing.
// Where the fallback answers (W4.2), the question lists the accounts it gives
// and the picker works — nothing is offered for enabling, because nothing needs
// it — and the API's state is the state it was.
func TestThePickerStillWorksWithNoAPI(t *testing.T) {
	f := gcpfake.NewBilling(t)
	su := pickerDisabledOn(t, f, "projects/proj-1234")
	acct := pickerAccountID(t, "gcloud")
	r, out := pickerRun(t, true, pickerAnswers("1"), nil)
	old := billingSources
	billingSources = billing.Options{
		Credentials: billing.Credentials{Endpoint: f.URL, NoAuth: true},
		Gcloud: billing.Gcloud{
			LookPath: func(file string) (string, error) { return "/usr/bin/" + file, nil },
			Run: func(context.Context, ...string) ([]byte, error) {
				return []byte(fmt.Sprintf("%-22s%-34s%-7s%s\n%-22s%-34s%-7s%s\n",
					"ACCOUNT_ID", "NAME", "OPEN", "MASTER_ACCOUNT_ID",
					acct, "own account", "True", "")), nil
			},
		},
	}
	t.Cleanup(func() { billingSources = old })
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.linkBilling != acct {
		t.Errorf("the picked account: %q, want the one gcloud listed (%q)\n%s", r.o.linkBilling, acct, out)
	}
	if !strings.Contains(out.String(), billing.SourceGcloud.String()) {
		t.Errorf("the question does not say gcloud answered:\n%s", out)
	}
	if strings.Contains(out.String(), "CONFIRM") {
		t.Errorf("an API the fallback made unnecessary was offered for enabling:\n%s", out)
	}
	if len(su.Enables()) != 0 || su.Enabled(pickerBillingAPI) {
		t.Errorf("the API's state changed: enables %v, enabled %v", su.Enables(), su.Enabled(pickerBillingAPI))
	}
}

// TestThePickerAndTheStagePrintTheSameGuidedLine: the line the question leaves
// when it cannot offer an account is the line the project stage prints as the
// user's to do — one line, in one place (W4.2's billing.Guided), so a
// rewording cannot leave the two disagreeing.
func TestThePickerAndTheStagePrintTheSameGuidedLine(t *testing.T) {
	e := &initEngine{r: &initRun{o: &initOptions{cloud: cloudOptions{gcpProject: "proj-1234"}}, w: &strings.Builder{},
		in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}}
	if got, want := newProjectStage(e).guided(), billing.Guided("proj-1234"); got != want {
		t.Errorf("the stage's guided line is %q, and the question leaves %q: they are one line", got, want)
	}
}
