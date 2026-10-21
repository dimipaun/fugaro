package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// TestPickerLeavesTheRunAsTodayWhenNoSourceAnswers: the API refused and gcloud
// is not on PATH (W4.2's fallback), so there is nothing to offer and nothing
// to say — the question prints nothing of its own and records nothing. The run
// falls back to today's text, which the project stage prints with the guided
// line (F6).
func TestPickerLeavesTheRunAsTodayWhenNoSourceAnswers(t *testing.T) {
	f := gcpfake.NewBilling(t)
	f.AddAccountNamed(pickerAccountID(t, "one"), "the operator's own account", true)
	f.RefusePath("/v1/billingAccounts", http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "The caller does not have permission")
	r, out := pickerRun(t, true, pickerAnswers(""), nil)
	pickerSources(t, f) // a refusing API and no gcloud on PATH
	if err := r.gatherInputs(t.Context()); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.o.linkBilling != "" {
		t.Errorf("an account was recorded with no source answering: %q", r.o.linkBilling)
	}
	if got := strings.ToLower(out.String()); strings.Contains(got, "billing account") {
		t.Errorf("the question printed something of its own when nothing could be listed:\n%s", out)
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
	asked := func(seam func(*testing.T)) *initOptions {
		firstRunEnv(t)
		fakeTerminal(t)
		seam(t)
		home := os.Getenv("HOME")
		before := filesUnder(t, home)
		o := &initOptions{door: setupDoor.name}
		r := &initRun{o: o, w: &strings.Builder{}, in: bufio.NewReader(strings.NewReader(pickerAnswers("1"))), cmd: NewRootCmd()}
		if err := r.gatherInputs(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := filesUnder(t, home); !reflect.DeepEqual(before, got) {
			t.Errorf("the question wrote files under HOME: %v, want %v", got, before)
		}
		return o
	}
	answered := asked(func(t *testing.T) { pickerSources(t, f) })
	control := asked(noBillingSources)
	if answered.linkBilling != picked {
		t.Fatalf("the picker recorded %q, want %q", answered.linkBilling, picked)
	}
	got, want := *answered, *control
	got.linkBilling, want.linkBilling = "", ""
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the question changed more than --link-billing:\n%+v\nwant\n%+v", got, want)
	}

	// A second question lists again: nothing is kept between runs.
	if second := asked(func(t *testing.T) { pickerSources(t, f) }); second.linkBilling != picked {
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
