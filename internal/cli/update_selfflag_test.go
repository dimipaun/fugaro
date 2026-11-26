package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agentenv"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra/managed"
)

// update_selfflag_test.go holds the tests of update_selfflag.go: TN1's stop and
// D2215's rewrite, the two things the head of every run that converges an
// installation does about the project's managed marks.
//
// The stop is a negative with a consequence, and the tests are shaped that way:
// what is asserted about a refused installation is not only the sentence but
// everything the run did NOT do — the terraform it never started, the object it
// never wrote, the local config it left byte for byte as it was (F16) — because
// a stop that had already edited this machine's own file on its way to the
// refusal would pass a test that read only the error.
//
// The rewrite is the other way round: it is a write, so each of its tests reads
// the creation fact back through the fixture's own fakes, and the two fences
// have a test each because a coding agent's session and a Fugaro run's own
// process are different markers set by different hands (F20).
//
// The two rigs are the update door's: managedUpdateRig is the installation
// these steps act on — the default newInitRig marks managed — and
// unmanagedUpdateRig is the same world with the project's own half of the
// managed fact taken back out.

// managedUpdateRig is the update door's rig on a managed installation, with the
// credentials the gate's own record is written with. The two marks are the
// fixture's (newInitRig's managedInstallation); what is added here is
// isolateGateCredentials, which a test that reaches the gate needs and which
// no shared fixture may stub for the whole package.
func managedUpdateRig(t *testing.T) *initRig {
	t.Helper()
	r := updateRig(t)
	isolateGateCredentials(t)
	return r
}

// unmanagedUpdateRig is the same rig on an installation the managed read
// answers is not managed: the project's label is gone (the read answers from
// the label alone, read.go), and the runs bucket still holds the creation fact
// of managedUpdateRig. That is the state a project whose label write failed
// is really in, and the one D1541's refusal is written about.
func unmanagedUpdateRig(t *testing.T) *initRig {
	t.Helper()
	noAgentSession(t)
	noRunSession(t)
	r := managedUpdateRig(t)
	r.removeManagedInstallation(t)
	return r
}

// updateBudgetArgs is the owner's own command line: `fugaro update` given the
// budget — the trio the doors take together at the owner's own terminal, with
// the account D2216 defaults to — where the person being there is what confirms
// update's ordinary steps.
func updateBudgetArgs(extra ...string) []string {
	return append([]string{"update", "--budget", "50", "--budget-currency", "USD", "--billing-account", billingAcc}, extra...)
}

// callSnapshot is the rig's call log at one moment in a test: the fixture's own
// calls are in it (the creation fact newInitRig writes and the policy it sets),
// so a test that asserts what a RUN did takes one before the run and asks what
// the run added.
type callSnapshot struct {
	gcs, crm []gcpfake.Request
	tf       [][]string
}

func callsNow(t *testing.T, r *initRig) callSnapshot {
	t.Helper()
	return callSnapshot{gcs: r.gcs.Requests(), crm: r.crm.Requests(), tf: r.calls(t)}
}

// writes are the calls the rig's fakes recorded since p that are not reads: the
// managed read is a read and reaches both fakes, so what a stopped run may not
// have left behind is a write.
func (p callSnapshot) writes(t *testing.T, r *initRig) []gcpfake.Request {
	t.Helper()
	all := append(append([]gcpfake.Request{}, r.gcs.Requests()[len(p.gcs):]...), r.crm.Requests()[len(p.crm):]...)
	var out []gcpfake.Request
	for _, req := range all {
		if req.Method != http.MethodGet {
			out = append(out, req)
		}
	}
	return out
}

// terraform is the fake terraform's subcommands since p.
func (p callSnapshot) terraform(t *testing.T, r *initRig) [][]string {
	t.Helper()
	return r.calls(t)[len(p.tf):]
}

// wroteNothing is the whole of what a run that stops at the head may have done:
// no cloud write, no project created, and no terraform started (the toolchain's own version read starts nothing). It is the F16
// assertion every refusal below makes.
func wroteNothing(t *testing.T, r *initRig, p callSnapshot, what string) {
	t.Helper()
	var started [][]string
	for _, c := range p.terraform(t, r) {
		if len(c) > 0 && c[0] == "version" {
			continue
		}
		started = append(started, c)
	}
	if len(started) != 0 {
		t.Errorf("%s: terraform ran: %q", what, started)
	}
	for _, req := range p.writes(t, r) {
		t.Errorf("%s: the run wrote to the project: %s %s", what, req.Method, req.Path)
	}
	if n := len(r.crm.Creates()); n != 0 {
		t.Errorf("%s: the run created %d project(s)", what, n)
	}
}

// TestUpdateRefusesUnmanaged is TN1's stop at the head of `fugaro update`. An
// installation the managed read answers is not managed is one this program may
// not converge — the marks are what say a project's confirmations may be
// skipped, so converging one that never carried them is a run writing into a
// project nobody declared (D1541) — and the refusal is the sentence every
// command that changes a managed project uses, naming the one command that
// flags a project: `fugaro managed flag`, run by its owner at their own
// terminal.
//
// The stop is at the HEAD (F16), and the rest of the test is about that: it is
// before the first cloud write, so terraform never starts and nothing lands in
// either fake but the read's own GETs, and it is before the local config is
// rewritten, so this machine's own file comes back byte for byte what it was.
// A stop placed after the catalog guard or the budget-mode rewrite would have
// edited the owner's own file on its way to a refusal.
//
// --plan-only is not stopped, which is the other half of the same rule: it
// applies nothing by its own definition, and refusing it would take the
// preview of an installation the owner is deciding about away from them.
func TestUpdateRefusesUnmanaged(t *testing.T) {
	r := unmanagedUpdateRig(t)
	fakeTerminal(t)
	before := callsNow(t, r)
	cfg := readFileString(t, r.cfg)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("fugaro update converged an installation that is not managed:\n%s\n%s", out, errOut)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d — a refusal, not a failure", got, ExitUserError)
	}
	said := out + errOut + ErrorText(err)
	for _, want := range []string{
		initProject + " is not managed",
		// The read's own reason, so the refusal says which half is missing
		// rather than only that something is.
		managed.ProjectLabelKey,
		"the command that flags an existing project managed is `fugaro managed flag`",
		"run by its owner at their own terminal",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, said)
		}
	}
	wroteNothing(t, r, before, "fugaro update on an unmanaged installation")
	if after := readFileString(t, r.cfg); after != cfg {
		t.Errorf("the stop rewrote this machine's own config, and it is before that write for that reason:\n%s", after)
	}
	// The same command line with --plan-only reads: the marks are read, the
	// plan is made, and nothing is refused.
	out, errOut, err = executeStdin(t, "", "update", "--plan-only")
	if err != nil {
		t.Fatalf("fugaro update --plan-only on an unmanaged installation: %v\n%s\n%s", err, out, errOut)
	}
	if said := out + errOut; strings.Contains(said, "is not managed") {
		t.Errorf("--plan-only refused an installation it may only read:\n%s", said)
	}
}

// TestSetupExistingUnmanagedRefuses: R8. `fugaro setup` on an installation that
// is already there is update's stage set and update's own stop — the door only
// drops the project stage, which a run whose project config exists does not
// register — so the same installation is refused in the same sentence, and the
// run writes nothing on its way there.
func TestSetupExistingUnmanagedRefuses(t *testing.T) {
	r := unmanagedUpdateRig(t)
	fakeTerminal(t)
	before := callsNow(t, r)
	cfg := readFileString(t, r.cfg)
	out, errOut, err := executeStdin(t, "", "setup")
	if err == nil {
		t.Fatalf("fugaro setup converged an installation that is not managed:\n%s\n%s", out, errOut)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d", got, ExitUserError)
	}
	said := out + errOut + ErrorText(err)
	if !strings.Contains(said, initProject+" is not managed") || !strings.Contains(said, "fugaro managed flag") {
		t.Errorf("the refusal is not the one update gives on the same installation:\n%s", said)
	}
	wroteNothing(t, r, before, "fugaro setup on an unmanaged installation")
	if after := readFileString(t, r.cfg); after != cfg {
		t.Errorf("the stop rewrote this machine's own config:\n%s", after)
	}
}

// unreadableRead makes the managed read FAIL rather than answer: the Resource
// Manager read the read proves the project with answers a 500, which is neither
// a 403 nor a 404 and so is an error and not a verdict. It is the whole of what
// a read that did not answer is here — the marks are all there.
//
// It is one call in the rig's world and not one call in the run: the project's
// own read is made by the run's stages as well, so an installation whose
// metadata read is down is one nothing can converge. What the two tests below
// are about is therefore the HEAD's answer to it — warns, refuses nothing, and
// admits nothing — and they read what follows as the outage's own failure.
func unreadableRead(r *initRig) {
	r.crm.FailGet(initProject, http.StatusInternalServerError, "INTERNAL", "the API did not answer")
}

// TestUpdateManagedReadFailsWarns is D2218's amendment, and the direction it
// fails in: a read that did not ANSWER is not the answer "not managed". A
// program that stopped here would end an owner's update over an API outage, and
// one that read the failure as a verdict would carry on with no evidence at
// all. What the head does instead is the path a project that is not managed
// took before this plan: a warning naming the read, and the run goes on into
// its own work.
//
// What the run then meets is the same outage in its first stage's own read, and
// that failure is the API's — not a refusal, which is the distinction this test
// is really about (exit 2, not exit 1): a read that failed never becomes the
// managed sentence.
func TestUpdateManagedReadFailsWarns(t *testing.T) {
	r := managedUpdateRig(t)
	noAgentSession(t)
	noRunSession(t)
	unreadableRead(r)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	said := out + errOut + ErrorText(err)
	if !strings.Contains(said, "could not read whether "+initProject+" is managed") {
		t.Fatalf("the run does not warn about the read that failed:\n%s", said)
	}
	if strings.Contains(said, "is not managed (") {
		t.Errorf("the run treated a read that failed as the answer that the project is not managed:\n%s", said)
	}
	if err != nil && ExitCode(err) == ExitUserError {
		t.Errorf("the run refused where it warned it would go on:\n%s", said)
	}
	if calls := r.calls(t); len(calls) == 0 {
		t.Errorf("the run stopped at the head instead of going on as a project that is not managed:\n%s", said)
	}
}

// TestUpdateReadFailureNeverAdmitsTheGate is the other half of D2218, and the
// half that fails closed. What the gate buys is the confirmations a managed
// project's marks take away, and a read that did not answer has bought nothing.
//
// The two subtests are the same session and the same plan, and the only
// difference is the read: with the marks readable a coding agent's session is
// admitted and the step applies, and with the read failing the same session
// applies nothing at all. A build that admitted whatever it could not read
// would show the second as the first.
func TestUpdateReadFailureNeverAdmitsTheGate(t *testing.T) {
	const additive = "module.installation.google_service_account.scheduler"

	t.Run("the read answers", func(t *testing.T) {
		r := updateManagedRig(t)
		updating(t, r)
		r.setPlan(t, change(additive, "create"))
		got, err := updateRun(t, r, "update")
		if err != nil {
			t.Fatalf("the gate admitted nothing on a readable installation: %v\n%s", err, got)
		}
		if n := len(r.ran(t, "apply")); n == 0 {
			t.Fatalf("the marks were readable and the session applied nothing:\n%s", got)
		}
		if recs := audits(t, r); len(recs) == 0 {
			t.Errorf("the admitted step left no record:\n%s", got)
		}
	})

	t.Run("the read fails", func(t *testing.T) {
		r := updateManagedRig(t)
		updating(t, r)
		r.setPlan(t, change(additive, "create"))
		unreadableRead(r)
		got, err := updateRun(t, r, "update")
		if !strings.Contains(got, "could not read whether "+initProject+" is managed") {
			t.Fatalf("the run does not warn about the read that failed:\n%s", got)
		}
		if strings.Contains(got, "is not managed (") {
			t.Errorf("a read that failed was answered as the verdict that the project is not managed:\n%s", got)
		}
		if n := len(r.ran(t, "apply")); n != 0 {
			t.Errorf("a session was admitted by a read that failed: %d apply(ies)\n%s", n, got)
		}
		if recs := audits(t, r); len(recs) != 0 {
			t.Errorf("a read that failed recorded an admission: %+v\n%s", recs, got)
		}
		if err != nil && ExitCode(err) == ExitUserError {
			t.Errorf("the run refused where the gate had bought it nothing:\n%s", got)
		}
	})
}

// TestUpdateExplicitBudgetRewrites is D2215's write, and R7 with it: an explicit
// --budget on an installation whose creation fact records an amount rewrites
// that amount through the writer that owns the object, before the run's own
// apply — the installation's alert follows in that same apply, because an
// explicit --budget is a figure this run's own document carries — and the run
// prints both figures, the one the fact held and the one it holds afterwards,
// because the object is what every later apply reads.
func TestUpdateExplicitBudgetRewrites(t *testing.T) {
	r := managedUpdateRig(t)
	noAgentSession(t)
	noRunSession(t)
	before, ok := managedFlagAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("the fixture wrote no gs://%s/%s", initRunsBucket, managed.MarkerObject)
	}
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", updateBudgetArgs()...)
	if err != nil {
		t.Fatalf("fugaro update with the budget: %v\n%s\n%s", err, out, errOut)
	}
	got, ok := managedFlagAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("the creation fact is gone from gs://%s/%s:\n%s\n%s", initRunsBucket, managed.MarkerObject, out, errOut)
	}
	if !strings.Contains(got, `"amount":50`) || !strings.Contains(got, `"currency":"USD"`) {
		t.Errorf("gs://%s/%s = %s, want the amount this run was given", initRunsBucket, managed.MarkerObject, got)
	}
	if got == before {
		t.Errorf("the run says it rewrote the fact and left it as it was: %s", got)
	}
	said := out + errOut
	if want := "recorded the budget 100 USD and now records 50 USD"; !strings.Contains(said, want) {
		t.Errorf("the run does not print the old and the new amount (%q):\n%s", want, said)
	}
	// The rewrite is before the apply and not instead of it: the run's own
	// document carries the amount it was given, so the alert it sets follows.
	if calls := r.calls(t); len(calls) == 0 {
		t.Errorf("the run rewrote the fact and applied nothing:\n%s", said)
	}
}

// TestUpdateRewriteKeepsRecordedCurrency: R6, and the half of the rewrite a
// caller can leave out. A run given --budget and no --budget-currency takes the
// currency the fact records and not the default: the amount is a figure about a
// unit, and rewriting 75 EUR as 50 USD would rewrite two things where the owner
// asked for one. The update door may take the currency from the fact
// (recordedCurrency, init.go) because it reads the fact at the head of the
// engine, whatever its own flags.
func TestUpdateRewriteKeepsRecordedCurrency(t *testing.T) {
	r := managedUpdateRig(t)
	noAgentSession(t)
	noRunSession(t)
	seedFlagFact(t, r, managed.Marker{
		Version:    managed.MarkerVersionNoBuilds,
		GCPProject: initProject,
		Budget:     &managed.Budget{Amount: 75, Currency: "EUR"},
	})
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update", "--budget", "50", "--billing-account", billingAcc)
	if err != nil {
		t.Fatalf("fugaro update --budget with no --budget-currency: %v\n%s\n%s", err, out, errOut)
	}
	got, ok := managedFlagAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("the creation fact is gone from gs://%s/%s", initRunsBucket, managed.MarkerObject)
	}
	if !strings.Contains(got, `"amount":50`) || !strings.Contains(got, `"currency":"EUR"`) {
		t.Errorf("gs://%s/%s = %s, want the amount this run was given in the currency the fact recorded", initRunsBucket, managed.MarkerObject, got)
	}
	if said := out + errOut; !strings.Contains(said, "recorded the budget 75 EUR and now records 50 EUR") {
		t.Errorf("the run does not print the currency it kept:\n%s", said)
	}
}

// TestUpdateKeepsARecordedAmountWithoutBudget: a default never overwrites a
// recorded amount (D2215). A plain `fugaro update` — no --budget at all — takes
// the figure the fact holds and writes nothing: the amount is the owner's own
// decision, made once, and an upgrade that replaced it with the default would
// change what a project's runs are judged by on a command that says nothing
// about budgets.
func TestUpdateKeepsARecordedAmountWithoutBudget(t *testing.T) {
	r := managedUpdateRig(t)
	noAgentSession(t)
	noRunSession(t)
	before, ok := managedFlagAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("the fixture wrote no gs://%s/%s", initRunsBucket, managed.MarkerObject)
	}
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("a plain fugaro update: %v\n%s\n%s", err, out, errOut)
	}
	after, ok := managedFlagAt(t, r.gcs, initRunsBucket)
	if !ok {
		t.Fatalf("the creation fact is gone from gs://%s/%s", initRunsBucket, managed.MarkerObject)
	}
	if after != before {
		t.Errorf("gs://%s/%s = %s, want the fact the fixture wrote, unchanged (%s)", initRunsBucket, managed.MarkerObject, after, before)
	}
	if said := out + errOut; strings.Contains(said, "now records") {
		t.Errorf("a run given no --budget says it rewrote the recorded amount:\n%s", said)
	}
}

// TestUpdateRewriteRefusalEndsTheRun: the fence is the run's outcome and not a
// warning. A rewrite the session may not make ends the run — it does not go on
// to apply the explicit amount anyway, which is what would leave the record
// saying one thing and the installation's alert another — and nothing is
// written on the way: no rewrite, and no terraform.
func TestUpdateRewriteRefusalEndsTheRun(t *testing.T) {
	r := managedUpdateRig(t)
	noAgentSession(t)
	noRunSession(t)
	t.Setenv(agentenv.Markers[0], "1")
	before, _ := managedFlagAt(t, r.gcs, initRunsBucket)
	calls := callsNow(t, r)
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", updateBudgetArgs()...)
	if err == nil {
		t.Fatalf("a coding agent's session rewrote the recorded amount:\n%s\n%s", out, errOut)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d", got, ExitUserError)
	}
	if after, _ := managedFlagAt(t, r.gcs, initRunsBucket); after != before {
		t.Errorf("the refused run rewrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, after)
	}
	wroteNothing(t, r, calls, "the refused rewrite")
}

// TestUpdateRewriteRefusedInAgentSession is F20's fence on the update door: what
// a coding agent's session may not do is decide the amount the project it runs
// in is judged by. The refusal names the marker, the amount the fact records,
// the one the run was given, and the command the owner runs at their own
// terminal instead.
func TestUpdateRewriteRefusedInAgentSession(t *testing.T) {
	r := managedUpdateRig(t)
	noAgentSession(t)
	noRunSession(t)
	t.Setenv(agentenv.Markers[0], "1")
	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", updateBudgetArgs()...)
	if err == nil {
		t.Fatalf("a coding agent's session rewrote the recorded amount:\n%s\n%s", out, errOut)
	}
	if after, _ := managedFlagAt(t, r.gcs, initRunsBucket); !strings.Contains(after, `"amount":100`) {
		t.Errorf("the refused run rewrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, after)
	}
	said := out + errOut + ErrorText(err)
	for _, want := range []string{
		"nothing was applied",
		agentenv.Markers[0],
		"records the budget 100 USD",
		"--budget 50 USD",
		"fugaro update --budget 50 --budget-currency USD",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, said)
		}
	}
}

// TestUpdateRewriteRefusedInsideAFugaroRun: the second fence, and a different
// one. A Fugaro run's own variables are set by the platform rather than by the
// agent, so the session marker is not set in one and this fence is not the same
// test. A run credential holds the operator's credentials and may not decide the
// amount the project it runs in is judged by — every later apply of that project
// is compared against it — which is why the refusal names the variable it found.
func TestUpdateRewriteRefusedInsideAFugaroRun(t *testing.T) {
	for _, marker := range managedFlagRunMarkers {
		t.Run(marker, func(t *testing.T) {
			r := managedUpdateRig(t)
			noAgentSession(t)
			noRunSession(t)
			t.Setenv(marker, "x")
			fakeTerminal(t)
			out, errOut, err := executeStdin(t, "", updateBudgetArgs()...)
			if err == nil {
				t.Fatalf("a Fugaro run's process rewrote the recorded amount:\n%s\n%s", out, errOut)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			said := out + errOut + ErrorText(err)
			for _, want := range []string{"nothing was applied", marker, "records the budget 100 USD"} {
				if !strings.Contains(said, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, said)
				}
			}
			if after, _ := managedFlagAt(t, r.gcs, initRunsBucket); !strings.Contains(after, `"amount":100`) {
				t.Errorf("the refused run rewrote gs://%s/%s = %s", initRunsBucket, managed.MarkerObject, after)
			}
		})
	}
}

// TestUpdateConvergesAfterFlag: TN1's stop costs the owner one command once, and
// this is what that command buys. The installation is refused; `fugaro managed
// flag` — the command the refusal names, run by the owner at their own terminal
// — flags it; and the same `fugaro update` then converges, which is the HARD
// RULE's answer to the extra step (F11): the upgrade is two commands once and
// one command from then on.
func TestUpdateConvergesAfterFlag(t *testing.T) {
	r := unmanagedUpdateRig(t)
	fakeTerminal(t)
	if _, _, err := executeStdin(t, "", "update"); err == nil {
		t.Fatal("fugaro update converged an installation that is not managed")
	}
	if _, _, err := executeStdin(t, "", managedFlagArgs()...); err != nil {
		t.Fatalf("fugaro managed flag on the refused installation: %v", err)
	}
	if !managedFlagIsManaged(t, r) {
		t.Fatal("the flag command left the installation reading as not managed")
	}
	before := callsNow(t, r)
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("fugaro update after the flag: %v\n%s\n%s", err, out, errOut)
	}
	if said := out + errOut; strings.Contains(said, "is not managed") {
		t.Errorf("the update after the flag still refused the installation:\n%s", said)
	}
	if calls := before.terraform(t, r); len(calls) == 0 {
		t.Errorf("the update after the flag converged nothing:\n%s\n%s", out, errOut)
	}
}

// seedFlagFact leaves gs://<runs bucket>/fugaro/managed.json holding m and
// nothing else, as an earlier run left it: the marker object without the label
// on the project is exactly the half-written state a flag's own steps can
// produce — its object write succeeds and its label write then fails — and it
// is sealed here rather than injected, because a GCS answer that fails once has
// no hook in gcpfake. The marker is built by the package's own writer, so the
// seed is a marker this build would have written and not a literal that could
// drift from one.
func seedFlagFact(t *testing.T, r *initRig, m managed.Marker) {
	t.Helper()
	data, err := m.Bytes()
	if err != nil {
		t.Fatalf("building the marker to seed: %v", err)
	}
	r.gcs.Put(initRunsBucket, managed.MarkerObject, data)
}
