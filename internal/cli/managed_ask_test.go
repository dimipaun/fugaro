package cli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// managed_ask_test.go: `--ask`, the symmetric half of `--yes` (the owner's rule
// of 2026-11-04). What the flag IS lives in one place — managed_ask.go for the
// wiring, initflow.AskDecision for the rule — and what this file adds is the
// table that rule is read against and the runs that show it in the product.
//
// The table is the one the rule has to answer for: three states of the marks
// (managed, not managed, a mark that could not be read) × two flag states
// (neither, --ask) × every session a run can be in (a real terminal, a coding
// agent's session with and without one, a pipe, --json). Every expectation is
// written by hand, and the two mutations the flag's safety rests on are run
// against it: a read predicate that answered "managed" for everything must fail
// the not-managed rows, and a decision that dropped the refusal must fail the
// sessions that cannot answer one.
//
// The runs below are the product's own paths and not the rule: `fugaro update`
// over the real gate read in faked installations, and the two step-level
// surfaces the flag gives a question back at — the plugin wiring's y/N, and a
// billable build's own. Nothing typed comes back with them: the flag restores
// the question a step used to ask (T7 took the answers away, not the questions),
// and what it is answered with is askConfirm's y or n. A secret stays a hidden
// prompt in every case and is no part of this flag: nothing here reads one.

// askSession is one session a run can be in, in initflow's own terms. The list
// is written out rather than derived from confirmTable because the flag's
// question is not that table's: the confirm table asks "may this step be
// confirmed", and this one asks "is there anyone to ask".
type askSession struct {
	name string
	c    initflow.Conditions
}

// askSessions is every session the flag's rule distinguishes, each hand-written.
func askSessions() []askSession {
	return []askSession{
		{"a real terminal", initflow.Conditions{Terminal: true}},
		{"a coding agent's session at a terminal", initflow.Conditions{Terminal: true, Agent: "CLAUDECODE"}},
		{"a coding agent's session with no terminal", initflow.Conditions{Agent: "CLAUDECODE"}},
		{"a pipe", initflow.Conditions{}},
		{"--json at a terminal", initflow.Conditions{Terminal: true, JSON: true}},
	}
}

// askFlagStates is the two flag states, in the spelling the rule's own comments
// use: --yes was the other one, and T11 removed it (docs/no-prompt-policy).
var askFlagStates = []string{"neither", "--ask"}

// askOracle is the flag's whole rule, written by hand and never derived from
// initflow.AskDecision:
//
//   - a run that did not pass the flag prints nothing and refuses nothing: the
//     flag decides nothing about a confirmation it was not asked for;
//   - --ask in a managed project prints the restored line at a real terminal
//     and REFUSES in every session that cannot answer a prompt. It never
//     proceeds on the gate's verdict instead, which is the one answer that
//     would make the flag a way to write without the confirmation it asked for;
//   - --ask in a project the marks did NOT answer "managed" for is the no-op
//     line — there is nothing to restore, and a refusal would fail a command
//     that behaves exactly as it always has — UNLESS the door's flag also
//     turns value entry on (`values`, the update door alone, D3347/R18): then
//     it says so and asks the repositories' values at a real terminal
//     (AskValues), and where no terminal can ask a value it prints and refuses
//     nothing and the stage leaves its own `fugaro secrets set` lines (D1540).
func askOracle(values, isManaged bool, flag string, c initflow.Conditions) (line, refusal string) {
	if flag != "--ask" {
		return "", ""
	}
	canPrompt := c.Agent == "" && c.Terminal && !c.JSON
	switch {
	case isManaged && canPrompt:
		return initflow.AskRestored(), ""
	case isManaged:
		return "", initflow.AskRefusal(c)
	case !values:
		return initflow.AskUnmanaged(), ""
	case canPrompt:
		return initflow.AskValues(), ""
	default:
		return "", ""
	}
}

// askDecider is one candidate for the flag's decision — the flags as they
// reached the gate, the read's answer and the session — so a mutation can be run
// through the same table the product's own decision is.
type askDecider func(askFlags, bool, initflow.Conditions) (string, string)

// askProductDecide is the decision the product makes, through the product's own
// function: askDecide with a buffer for its output. Writing it this way and not
// as a copy of initflow.AskDecision is the point of the table — what is measured
// is the printing the commands call, so a line printed twice, a refusal that
// printed something, or a run that did not pass the flag reaching the rule at
// all is a row that fails.
func askProductDecide(f askFlags, managed bool, c initflow.Conditions) (line, refusal string) {
	out := &syncBuf{}
	if err := askDecide(out, f, managed, c); err != nil {
		return "", ErrorText(err)
	}
	return strings.TrimSuffix(out.String(), "\n"), ""
}

// askViolations runs one decision over the whole table for one read predicate
// and returns every row that did not answer what the oracle says, with the
// number of rows run.
//
// The expectation comes from the read's HAND-WRITTEN verdict and never from the
// predicate under test: a mutation that answers "managed" for everything would
// otherwise move the oracle with it and pass itself — which is exactly the
// mutation the first check exists to catch.
//
// The gate's own predicate is walked beside the sessions: what the rows assert
// is that the flag takes the gate's verdict out of every one of them, because a
// step left to the gate under the flag that asked for the confirmations is the
// direction a mistake here would fall in.
func askViolations(decide askDecider, mode func(bool, string, error) bool, reads []managedRead) (bad []string, n int) {
	for _, rd := range reads {
		isManaged := mode(rd.ok, rd.reason, rd.err)
		// The gate's own verdict for this read with the plan admitted: the most
		// the gate ever gives, so a session it is not taken out of here is one
		// --ask cannot take out at all.
		m := initflow.Managed{Managed: rd.managed, Admitted: rd.managed}
		for _, flag := range askFlagStates {
			for _, s := range askSessions() {
				for _, values := range []bool{false, true} {
					n++
					c := s.c
					c.Ask = flag == "--ask"
					f := askFlags{ask: flag == "--ask", values: values}
					wantLine, wantRefusal := askOracle(values, rd.managed, flag, c)
					line, refusal := decide(f, isManaged, c)
					where := rd.state + ", " + flag + ", values=" + boolWord(values) + ", " + s.name
					if line != wantLine {
						bad = append(bad, where+": line "+askQuote(line)+", want "+askQuote(wantLine))
					}
					if refusal != wantRefusal {
						bad = append(bad, where+": refusal "+askQuote(refusal)+", want "+askQuote(wantRefusal))
					}
					if c.Ask && initflow.GateStandsIn(c, m) {
						bad = append(bad, where+": the gate still stands in for this step")
					}
				}
			}
		}
	}
	return bad, n
}

// askQuote is a row's answer in a failure message: an empty one is a fact
// (nothing was printed, nothing was refused) and is never printed as nothing.
func askQuote(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// The flag's rule, over every state of the marks the installation's own read
// can answer, every flag state and every session. The reads are the product's:
// managedReads drives managed.IsManaged over internal/gcpfake's storage and
// Resource Manager, and managedMode is the predicate every caller decides a
// step under (a read that answered true and did not fail).
func TestManagedAskIsTheFlagsWholeRule(t *testing.T) {
	reads := managedReads(t)
	bad, n := askViolations(askProductDecide, managedMode, reads)
	if n == 0 {
		t.Fatal("the table ran no row")
	}
	if len(bad) != 0 {
		t.Errorf("--ask answered %d of %d row(s) differently from the rule:\n%s", len(bad), n, firstFew(bad))
	}
	// The three states the table is about are really in it: a table whose
	// reads were all one answer would pass by accident.
	var managedN, notManagedN, unreadableN int
	for _, rd := range reads {
		switch {
		case managedMode(rd.ok, rd.reason, rd.err):
			managedN++
		case rd.err != nil:
			unreadableN++
		default:
			notManagedN++
		}
	}
	if managedN == 0 || notManagedN == 0 || unreadableN == 0 {
		t.Fatalf("the reads hold %d managed, %d not managed and %d that did not answer: the table has no state to tell apart",
			managedN, notManagedN, unreadableN)
	}
}

// The first mutation: a read predicate that answered "managed" for everything.
// It is the mistake the flag's safety rests on — the flag speaks about the
// marks — so a project nothing recorded as managed must never read as one whose
// confirmations the flag may replace. Every not-managed row must be reported.
func TestManagedAskPropertyCatchesAManagedEverythingRead(t *testing.T) {
	reads := managedReads(t)
	for _, tc := range []struct {
		name string
		mode func(bool, string, error) bool
	}{
		// The predicate that ignores the read entirely.
		{"every read is managed", func(bool, string, error) bool { return true }},
		// The read's first result alone: the shape of a caller that dropped
		// the error, and it is caught by the one row the read never gives
		// ("a read that answered managed and then failed").
		{"the read's first result alone is managed", func(ok bool, reason string, err error) bool { return ok }},
		// The classic fail-open: nothing failed, so it must be managed.
		{"a read with no error is managed", func(ok bool, reason string, err error) bool { return err == nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad, _ := askViolations(askProductDecide, tc.mode, reads)
			if len(bad) == 0 {
				t.Fatal("the table accepted a predicate that answered managed for a project nothing recorded as managed")
			}
			if !askMentions(bad, initflow.AskUnmanaged()) {
				t.Errorf("the mutation is not caught by the no-op line's own rows:\n%s", firstFew(bad))
			}
		})
	}
}

// The second mutation: the refusal dropped. A decision that answers the
// restored line wherever the marks say managed is the flag read as "write
// without the prompts" in exactly the sessions that cannot answer them, and
// every one of those sessions must be reported. The two further decisions are
// the other shapes of the same mistake: one that decides nothing, and one that
// refuses in a project which is not managed — failing a command that behaves
// exactly as it always has.
func TestManagedAskPropertyCatchesADecisionWithNoRefusal(t *testing.T) {
	reads := managedReads(t)
	bad, _ := askViolations(func(f askFlags, isManaged bool, c initflow.Conditions) (string, string) {
		if !f.ask {
			return "", ""
		}
		if !isManaged {
			return initflow.AskUnmanaged(), ""
		}
		return initflow.AskRestored(), ""
	}, managedMode, reads)
	if len(bad) == 0 {
		t.Fatal("the table accepted a decision that answers the restored line where nobody can answer a prompt")
	}
	for _, s := range []string{
		"a coding agent's session at a terminal", "a coding agent's session with no terminal",
		"a pipe", "--json at a terminal",
	} {
		if !askMentionsSession(bad, s) {
			t.Errorf("dropping the refusal is not caught in %s:\n%s", s, firstFew(bad))
		}
	}
	for _, tc := range []struct {
		name   string
		decide askDecider
	}{
		{"nothing at all", func(askFlags, bool, initflow.Conditions) (string, string) { return "", "" }},
		{"a refusal wherever the marks did not answer, the no-op line dropped", func(f askFlags, isManaged bool, c initflow.Conditions) (string, string) {
			if !f.ask {
				return "", ""
			}
			if !isManaged || c.Agent != "" || c.JSON || !c.Terminal {
				return "", initflow.AskRefusal(c)
			}
			return initflow.AskRestored(), ""
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if bad, _ := askViolations(tc.decide, managedMode, reads); len(bad) == 0 {
				t.Fatal("the table accepted a decision that does not answer the flag's rule")
			}
		})
	}
}

func askMentions(bad []string, line string) bool {
	for _, b := range bad {
		if strings.Contains(b, line) {
			return true
		}
	}
	return false
}

func askMentionsSession(bad []string, name string) bool {
	for _, b := range bad {
		if strings.Contains(b, ", "+name+":") {
			return true
		}
	}
	return false
}

// askMarkWorld is one state of the marks as a rig can be put in, for the runs
// below: what puts the installation there.
type askMarkWorld struct {
	name string
	put  func(t *testing.T, r *initRig)
}

// askMarkWorlds are the three states the flag's rule turns on. "not managed"
// and "a mark that could not be read" are two rows and not one: the flag
// answers the same sentence to both (AskDecision's own comment says why), and a
// test with only one of them could not tell a read that fails closed from a
// project that genuinely is not managed.
func askMarkWorlds() []askMarkWorld {
	return []askMarkWorld{
		{"managed", func(t *testing.T, r *initRig) { r.managedRuns(t, initProjectName, initProject) }},
		// markedRuns takes the project's own half of the fact back out as a
		// world without the fixture's label was written; the fixture carries
		// it now, so the label goes too, and the creation fact with it.
		{"not managed", func(t *testing.T, r *initRig) { r.markedRuns(t, "", ""); r.unmarkedInstallation(t) }},
		{"a mark that could not be read", func(t *testing.T, r *initRig) {
			r.managedRuns(t, initProjectName, initProject)
			writeUnreadableMarker(t, r)
		}},
	}
}

// writeUnreadableMarker puts the runs bucket's marker object there with bytes
// the reader refuses: the installation's label says it is managed and the fact
// cannot be read. It is the state every read in internal/infra/managed answers
// "not managed" for, and the object is overwritten rather than deleted so that
// the label is not the half that refuses.
func writeUnreadableMarker(t *testing.T, r *initRig) {
	t.Helper()
	r.gcs.Put(initRunsBucket, managed.MarkerObject, []byte("{this is not a creation fact"))
}

// askUpdateRig is one `fugaro update` world for the flag: the marks in the
// state the world names, the credentials isolated, and a release with an
// additive plan, so a step really reaches the gate and its own confirmation.
func askUpdateRig(t *testing.T, w askMarkWorld) *initRig {
	t.Helper()
	r := updateRig(t)
	w.put(t, r)
	isolateGateCredentials(t)
	updating(t, r)
	r.setPlan(t, change(askAdditiveAddress, "create"))
	return r
}

const askAdditiveAddress = "module.installation.google_service_account.scheduler"

// freeLogName leaves the next run in this rig a log name of its own: a log is
// named after its command, the second and the pid (report.logName) and created
// O_EXCL, so two runs of one command inside one second collide on the name and
// the second keeps no log at all (CL3's OpenLog). That is not a difference a
// test may leave in place — a converge without a log cuts nothing on its screen
// and prints the plan's own summary in full, because the brief account is the
// log's (D1352) — so a test that compares two runs of one command in one rig
// hands the second one the name back.
func freeLogName(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(logDir(t)); err != nil {
		t.Fatal(err)
	}
}

// askUpdateRun is updateRun with something typed: the flag gives prompts back,
// so a test that means to answer one needs to be able to.
func askUpdateRun(t *testing.T, r *initRig, stdin string, args ...string) (string, error) {
	t.Helper()
	out, errOut, err := executeStdin(t, stdin, args...)
	if err != nil {
		out += ErrorText(err)
	}
	return out + errOut, err
}

// The flag's promise at the owner's own terminal in a managed project: the
// confirmation the gate would have stood in for is asked again, and the gate's
// own sentence — what a run without the flag prints instead — is nowhere.
//
// And it is the RUN'S one confirmation that comes back, which is the whole of
// what "the prompts the project would have had if it were not managed" means
// here: this rig's runs bucket is the installation's own, so the review is the
// owner's to confirm, one question for all of its steps, exactly as it is
// without the flag (TestMarkedProjectTakesOnePrompt) — and since T7 the
// question is the [y/N] askConfirm asks (nothing is typed under it), not a name.
// Nothing is applied because the rig's stdin holds no answer, which is the
// assertion that the question is a real one and not a line about a question.
func TestUpdateManagedAskAsksTheRunsConfirmationAgain(t *testing.T) {
	r := askUpdateRig(t, askMarkWorlds()[0])
	fakeTerminal(t)
	noAgentSession(t)

	got, err := updateRun(t, r, "update", "--ask")
	if err == nil {
		t.Fatalf("the run applied with nothing typed at the confirmation --ask gave back:\n%s", got)
	}
	if !strings.Contains(got, initflow.AskRestored()) {
		t.Errorf("the run does not say what the flag did:\n%s", got)
	}
	if !strings.Contains(got, "apply all of the steps above to GCP project "+initProject+"? [y/N]: ") {
		t.Errorf("--ask did not ask the run's own confirmation:\n%s", got)
	}
	if !strings.Contains(got, "Review: project ") || !strings.Contains(got, "is Fugaro's own") {
		t.Errorf("--ask did not put the review back in its ordinary form:\n%s", got)
	}
	for _, no := range []string{initflow.GateProceeded(), initflow.GateOrdinary(), "covered by the run confirmation", "is a managed project, so its steps take no confirmation"} {
		if strings.Contains(got, no) {
			t.Errorf("--ask left the gate's verdict in the confirmation's place (%q):\n%s", no, got)
		}
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("%d terraform apply(s) ran with nothing confirmed", n)
	}
}

// And with the question answered yes, the run goes: the flag gives the
// confirmation back and the confirmation is answered the way askConfirm's own
// question is answered — a y, one answer for the run's ordinary steps.
func TestUpdateManagedAskAppliesOnTheRunsOwnYes(t *testing.T) {
	r := askUpdateRig(t, askMarkWorlds()[0])
	fakeTerminal(t)
	noAgentSession(t)

	got, err := askUpdateRun(t, r, "y\n", "update", "--ask")
	if !strings.Contains(got, initflow.AskRestored()) {
		t.Errorf("the run does not say what the flag did:\n%s", got)
	}
	if n := len(r.ran(t, "apply")); n == 0 {
		t.Errorf("nothing was applied after the question --ask gave back was answered yes (err %v):\n%s", err, got)
	}
}

// The refusal, in the sessions the flag cannot be answered in: a managed
// project with --ask and nobody at the terminal. Nothing is applied, no prompt
// is printed, and the run does NOT fall back to the gate's verdict — the answer
// that would make the flag a way to write without the confirmation it asked
// for.
func TestUpdateManagedAskRefusesWhereNobodyCanAnswer(t *testing.T) {
	for _, s := range []struct {
		name   string
		marker bool
	}{
		{"a coding agent's session", true},
		{"a session with no terminal", false},
	} {
		t.Run(s.name, func(t *testing.T) {
			r := askUpdateRig(t, askMarkWorlds()[0])
			noAgentSession(t)
			if s.marker {
				t.Setenv("CLAUDECODE", "1")
			} else {
				noTerminal(t)
			}

			got, err := updateRun(t, r, "update", "--ask")
			if err == nil {
				t.Fatalf("the run proceeded where the confirmations --ask asked for cannot be answered:\n%s", got)
			}
			if !strings.Contains(got, "the confirmations of this managed project were asked for and") ||
				!strings.Contains(got, "never a silent proceed") {
				t.Errorf("the refusal is not the flag's own sentence:\n%s", got)
			}
			if strings.Contains(got, "Type "+initProjectName) {
				t.Errorf("a session that cannot answer was shown a prompt:\n%s", got)
			}
			if n := len(r.ran(t, "apply")); n != 0 {
				t.Errorf("%d terraform apply(s) ran in a session that cannot answer", n)
			}
			if code := ExitCode(err); code != ExitUserError {
				t.Errorf("exit %d, want %d: the refusal is the user's and not a failure", code, ExitUserError)
			}
			// Nothing was admitted for a step that never ran: the record is
			// written AFTER the write it was admitted for (D1014).
			for _, rec := range audits(t, r) {
				if rec.Verdict == managedmode.AuditAdmitted {
					t.Errorf("a step refused before it ran filed an admission: %+v", rec)
				}
			}
		})
	}
}

// The gate comes first. A plan a managed project refuses is refused in the
// gate's own sentence, before any prompt --ask gives back: the flag asks for
// the confirmations the gate's verdict would have replaced, and it does not ask
// the gate to judge less. The question the flag put back earlier in the run is
// answered yes here, so what the run stops on is the gate's refusal and not the
// flag's own unanswered question.
//
// The fixture is D1549's kind and not the grant this test used to carry: since
// T9 a grant the review did not list is a content finding the gate records and
// warns about rather than refuses, so a plan of those would be applied and
// could not witness a refusal at all. An authoritative IAM resource still
// refuses, which is what leaves the gate's sentence — and not the confirmation
// --ask put back — the thing this run stops on.
func TestUpdateManagedAskKeepsTheGatesOwnRefusalFirst(t *testing.T) {
	const address = "module.installation.google_storage_bucket_iam_binding.runs"
	r := updateRig(t)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	fakeTerminal(t)
	noAgentSession(t)
	updating(t, r)
	r.setPlan(t, change(address, "create"))

	got, err := askUpdateRun(t, r, "y\n", "update", "--ask")
	if err == nil {
		t.Fatalf("--ask applied a plan the managed gate refuses:\n%s", got)
	}
	// The fact is read where the terminal says it is: the brief's three lines
	// carry the rule's clause and the step's own line in the log carries the
	// sentence that names the resource (the owner's feedback of 2026-11-09).
	whole := got + namedLog(t, got)
	if !strings.Contains(whole, managedmode.NotConverged) || !strings.Contains(whole, address) {
		t.Errorf("the refusal is not the gate's own, naming what it refused:\n%s", whole)
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("%d terraform apply(s) ran for a plan the gate refused", n)
	}
}

// A project the marks did not answer "managed" for: the update door refuses the
// run at its head (TN1's stop, requireManagedInstallation), before any step and
// before the flag is read at all — so in both sessions whose mistake would be
// expensive, --ask changes nothing, and neither the flag's own line for an
// unmanaged project nor the one for a managed one reaches a run that never
// started. A refusal in a coding agent's session would otherwise stop a command
// that behaves as it always has, and the restored line at a person's terminal
// would claim confirmations were replaced that never were.
//
// The stop is what this asserts, and the flag's rule over a read answering "not
// managed" is not lost with it: the rule and its line belong to the gate, and
// the door that still reaches it — settings set, which converges no installation
// — is where the line is read back
// (TestSettingsSetAskInAProjectThatIsNotManagedIsANoOp).
func TestUpdateAskInAProjectThatIsNotManagedRefusesTheRun(t *testing.T) {
	for _, w := range askMarkWorlds()[1:] {
		for _, s := range []struct {
			name  string
			agent bool
		}{
			{"a real terminal", false},
			{"a coding agent's session", true},
		} {
			t.Run(w.name+", "+s.name, func(t *testing.T) {
				r := askUpdateRig(t, w)
				noAgentSession(t)
				if s.agent {
					t.Setenv("CLAUDECODE", "1")
				} else {
					fakeTerminal(t)
				}

				plain, plainErr := updateRun(t, r, "update")
				flagged, flaggedErr := updateRun(t, r, "update", "--ask")
				for _, run := range []struct {
					name string
					out  string
					err  error
				}{
					{"the run without the flag", plain, plainErr},
					{"the run with it", flagged, flaggedErr},
				} {
					if ExitCode(run.err) != ExitUserError || !strings.Contains(run.err.Error(), "is not managed") ||
						!strings.Contains(run.err.Error(), "managed flag") {
						t.Errorf("%s: exit %d, err %v\n%s; want the stop's refusal", run.name, ExitCode(run.err), run.err, run.out)
					}
					for _, line := range []string{initflow.AskUnmanaged(), initflow.AskRestored(), initflow.AskValues()} {
						if strings.Contains(run.out, line) {
							t.Errorf("%s: the flag's line reached a run the stop refused (%q):\n%s", run.name, line, run.out)
						}
					}
				}
				if n := len(r.ran(t, "apply")); n != 0 {
					t.Errorf("%d terraform apply(s) ran in a project that is not managed", n)
				}
			})
		}
	}
}

// The path the stop does not take: a read that did not ANSWER at all (D2218)
// warns and goes on as a project that is not managed, and on that path the
// update door's --ask is still U3b's row (D3347, R18) — the flag turns value
// entry on there, so the line it prints is AskValues, and never AskUnmanaged,
// whose promise ("this run asks the confirmations it always asks") is not what a
// run that turns the flag's value entry on does.
//
// Nothing here claims a value was typed. The head's warning is answered by the
// same outage in the run's first stage's own read, which is what ends the run
// (TestUpdateManagedReadFailsWarns) — so this pins which line the flag gets on
// the one path a not-managed answer does not stop, and that it applies nothing.
func TestUpdateAskOnAFailedReadKeepsTheValuesLine(t *testing.T) {
	r := updateManagedRig(t)
	noAgentSession(t)
	noRunSession(t)
	unreadableRead(r)
	fakeTerminal(t)

	out, errOut, err := executeStdin(t, "", "update", "--ask")
	said := out + errOut + ErrorText(err)
	if !strings.Contains(said, "could not read whether "+initProject+" is managed") {
		t.Fatalf("the run does not warn about the read that failed:\n%s", said)
	}
	if !strings.Contains(said, initflow.AskValues()) {
		t.Errorf("--ask did not answer with the update door's own line where the read did not answer:\n%s", said)
	}
	for _, line := range []string{initflow.AskUnmanaged(), initflow.AskRestored()} {
		if strings.Contains(said, line) {
			t.Errorf("--ask answered another door's line on a read that did not answer (%q):\n%s", line, said)
		}
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("%d terraform apply(s) ran on a read that did not answer", n)
	}
	if err != nil && ExitCode(err) == ExitUserError {
		t.Errorf("the run refused where a read that failed only warns:\n%s", said)
	}
}

// The same read in a session that cannot answer a prompt: there is no terminal
// to ask a value at, so the update door's value entry answers NOTHING — no
// values line, and no refusal either. The empty answer is AskDecision's own row
// (D3347/R18; the stage leaves its `fugaro secrets set` lines, D1540) and not a
// gap: the flag refuses where a confirmation it restored cannot be answered, and
// here it restored none. A refusal would be the claim the rule does not make —
// that asking for a value nobody can type fails the run — and the run meets the
// read outage instead, in the stage's own words.
func TestUpdateAskOnAFailedReadAsksForNoValueInAnAgentSession(t *testing.T) {
	r := updateManagedRig(t)
	noAgentSession(t)
	noRunSession(t)
	unreadableRead(r)
	t.Setenv("CLAUDECODE", "1")

	out, errOut, err := executeStdin(t, "", "update", "--ask")
	said := out + errOut + ErrorText(err)
	if !strings.Contains(said, "could not read whether "+initProject+" is managed") {
		t.Fatalf("the run does not warn about the read that failed:\n%s", said)
	}
	for _, line := range []string{
		initflow.AskValues(), initflow.AskUnmanaged(), initflow.AskRestored(),
		initflow.AskRefusal(initflow.Conditions{Agent: "CLAUDECODE"}),
	} {
		if strings.Contains(said, line) {
			t.Errorf("--ask answered a session that cannot ask a value (%q):\n%s", line, said)
		}
	}
	if err != nil && ExitCode(err) == ExitUserError {
		t.Errorf("the flag refused where the rule answers nothing:\n%s", said)
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("%d terraform apply(s) ran on a read that did not answer", n)
	}
}

// A run the gate never covers: --plan-only applies nothing by its own
// definition, so there is no step whose confirmation the gate stands in for.
// The flag says that, and the run is otherwise the run it always was.
func TestUpdateAskOnAPlanOnlyRunSaysThereWasNothingToRestore(t *testing.T) {
	r := askUpdateRig(t, askMarkWorlds()[0])
	fakeTerminal(t)
	noAgentSession(t)
	// The unit step is put on disk as this release renders it before either
	// run: its own line is the one thing --ask moves by itself, because the
	// step reads a person at the terminal through initflow.PersonAtTheTerminal,
	// whose Ask half counts --ask as nobody being there — so it announces the
	// write without the flag and skips it with it. That is the step's reading
	// of the flag and not the flag's rule, and the comparison below is the
	// flag's: with the unit already current, both runs read it as done.
	u := currentUnit(t)
	unitFileAt(t, u.Text, time.Now())

	plain, plainErr := updateRun(t, r, "update", "--plan-only")
	freeLogName(t)
	flagged, flaggedErr := updateRun(t, r, "update", "--plan-only", "--ask")
	if ExitCode(plainErr) != ExitCode(flaggedErr) {
		t.Errorf("--ask changed the exit code of a plan-only run: %d without it, %d with it", ExitCode(plainErr), ExitCode(flaggedErr))
	}
	if !strings.Contains(flagged, initflow.AskNoGate()) {
		t.Errorf("--ask did not say that this run has nothing for it to restore:\n%s", flagged)
	}
	if stripped := strings.Replace(flagged, initflow.AskNoGate()+"\n", "", 1); stripped != plain {
		t.Errorf("--ask changed a plan-only run (the flag's line stripped from the middle).\nwithout: %s\nwith:    %s", plain, flagged)
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("--plan-only applied %d step(s)", n)
	}
}

// The plugin stage's y/N went with the wiring (U2, D949): the settings write it
// confirmed is gone, and the step left is the install in Claude Code, which no
// gate stands in for and --ask is not the flag to put a question back on. The
// two runs — the flag, and the control without it — are driven the same way
// (the gate open, a terminal, the stage's Apply called directly), and what is
// read off them is that they are the same: no prompt, no diff of a file, and
// the checkout's settings byte for byte.
func TestManagedAskGivesThePluginStageNoQuestion(t *testing.T) {
	releaseBuild(t, "0.2.0")
	for _, ask := range []bool{false, true} {
		name := "the flag"
		if !ask {
			name = "no flag (the control)"
		}
		t.Run(name, func(t *testing.T) {
			settings := wiringCheckout(t, settingsWithOthers)
			withTerminal(t, true)
			e, out := stageEngine(t, "", &initOptions{ask: ask})
			// --ask reaches a step's confirmation through the command's own
			// flags — askConfirm reads it there, and boolFlag answers false for
			// a flag its command does not carry — so a rig that left it off the
			// command would be a --ask run in which nothing is ever asked. And
			// askConfirm prints its question on the command, not through r.w.
			e.r.cmd.Flags().Bool("ask", ask, "")
			e.r.cmd.SetOut(out)
			e.r.gate = &initGate{managed: true}

			if _, err := newPluginStage(e).Apply(t.Context(), initflow.Env{Interactive: true}); err != nil {
				t.Fatalf("the plugin stage was stopped (err %v): it has nothing to ask:\n%s", err, out.String())
			}
			if got, _ := os.ReadFile(settings); string(got) != settingsWithOthers {
				t.Errorf("the stage wrote the checkout's settings:\n%s", got)
			}
			for _, bad := range []string{"[y/N]", "extraKnownMarketplaces", initflow.GateOrdinary(), initflow.GateProceeded()} {
				if strings.Contains(out.String(), bad) {
					t.Errorf("the stage says %q, and it neither asks nor writes:\n%s", bad, out.String())
				}
			}
		})
	}
}

// A billable build is the one surface --ask does not put back: the project's
// name typed at each build went with the prompts (T4), and a build asks nothing
// in any session — a managed project's own terminal included. What --ask
// restores are the steps that still ask (the base pin, the check-job update);
// the build keeps its cost line either way, with the gate's own sentence under
// it where the gate's verdict is what the owner reads, and without it where the
// flag asked for the questions back (there is no question here left to ask).
//
// The mutation caught: announceBuild reading the confirmation conditions and
// asking for the build's own name again — the flag case then prints a prompt
// and fails the first assertion, and the loudest shape of it fails the control.
func TestManagedAskDoesNotPutTheBuildsNameBack(t *testing.T) {
	g := initflow.Managed{Managed: true, Admitted: true}
	for _, ask := range []bool{false, true} {
		name := "the flag"
		if !ask {
			name = "no flag (the control)"
		}
		t.Run(name, func(t *testing.T) {
			r, _ := confirmRig(t, confirmRow{terminal: true, ask: ask}, "y")
			out := &syncBuf{}
			r.w = out
			r.cmd.SetOut(out) // askConfirm prints its question on the command, not through r.w
			fakeTerminal(t)
			noAgentSession(t)
			// This command's own read of the marks, which is the one --ask
			// answers for here: `fugaro image refresh` reads them into
			// r.refresh, and non-nil is exactly a project they answered
			// "managed" for (openRefreshManaged, and r.gateRestores).
			r.refresh = &refreshManaged{}

			if err := r.announceBuild("builds the image for acme/app", g); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "builds the image for acme/app") {
				t.Errorf("the build's own line was not printed:\n%s", out.String())
			}
			if strings.Contains(out.String(), "to confirm") || strings.Contains(out.String(), "⚠ CONFIRM") {
				t.Errorf("the removed prompt is still printed:\n%s", out.String())
			}
			if got := strings.Contains(out.String(), initflow.GateProceeded()); got != !ask {
				t.Errorf("the gate's sentence printed=%v, want %v:\n%s", got, !ask, out.String())
			}
		})
	}
}

// Where the flag lives, and where it deliberately does not. It is on the
// commands whose steps the managed gate covers. `fugaro setup` declares it and
// HIDES it from its help (the foreign-flag mechanism, so a reader is never
// offered a flag the door refuses) and refuses it by name at runtime, which
// TestSetupRefusesAskByName reads. The surfaces the ruling does not reach — a
// billable image build, the config publish and the project policy publish — do
// not carry it at all, so no help text can promise a prompt there.
func TestManagedAskIsOnTheCommandsItBelongsTo(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"update", "--help"}, true},
		{[]string{"settings", "set", "--help"}, true},
		{[]string{"settings", "unset", "--help"}, true},
		{[]string{"accounts", "add", "--help"}, true},
		{[]string{"image", "refresh", "--help"}, true},
		{[]string{"setup", "--help"}, false}, // declared, hidden and refused by name
		{[]string{"image", "build", "--help"}, false},
		{[]string{"config", "publish", "--help"}, false},
		{[]string{"project", "policy", "publish", "--help"}, false},
	} {
		out, _, err := execute(t, c.args...)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if got := strings.Contains(out, "--ask"); got != c.want {
			t.Errorf("%v names --ask = %v, want %v", c.args, got, c.want)
		}
	}
}

// `fugaro setup` refuses --ask by name, with the door that owns it, and it does
// so before anything is read: the wizard asks every one of its own
// confirmations already, whatever the project's marks say. The refusal is the
// foreign-flag sentence, which the root prints on stderr, and the flag is not
// in this door's help (TestManagedAskIsOnTheCommandsItBelongsTo).
func TestSetupRefusesAskByName(t *testing.T) {
	r := newInitRig(t)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	fakeTerminal(t)
	noAgentSession(t)

	out, errOut, err := executeStdin(t, initProjectName+"\n", "setup", "--ask")
	if err == nil {
		t.Fatalf("fugaro setup accepted --ask:\n%s", out)
	}
	said := out + errOut + ErrorText(err)
	if !strings.Contains(said, "--ask") || !strings.Contains(said, "fugaro update") {
		t.Errorf("the refusal does not name the flag and the door that owns it:\n%s", said)
	}
	if code := ExitCode(err); code != ExitUserError {
		t.Errorf("exit %d, want %d: a flag a door does not take is the user's, not a failure", code, ExitUserError)
	}
	if n := markReads(r); n != 0 {
		t.Errorf("setup read the marks %d time(s) for a flag it refuses", n)
	}
}

// The third state of the marks is really one — and it is one of the states the
// table above turns on — so the bytes written there are asserted to answer "not
// managed" rather than assumed to.
func TestManagedAskUnreadableMarkWorldIsReallyUnreadable(t *testing.T) {
	r := updateRig(t)
	askMarkWorlds()[2].put(t, r)
	ok, reason, err := managed.IsManaged(context.Background(), managedStorage(t, r.gcs), managedCRM(t, r.crm), initProject, initProjectName)
	if ok {
		t.Fatalf("the unreadable marker was read as a fact: %s", reason)
	}
	if err != nil {
		t.Fatalf("the read failed rather than answering, so this is not the world the table's third row is: %v", err)
	}
	if strings.Contains(reason, "carries no "+managed.ProjectLabelKey) {
		t.Errorf("the project's label is what refused, not the object: %s", reason)
	}
}
