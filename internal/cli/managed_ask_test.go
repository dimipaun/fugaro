package cli

import (
	"context"
	"os"
	"strings"
	"testing"

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
// (managed, not managed, a mark that could not be read) × three flag states
// (neither, --yes, --ask) × every session a run can be in (a real terminal, a
// coding agent's session with and without one, a pipe, --json,
// --non-interactive), with both confirmation classes walked beside them. Every
// expectation is written by hand, and the two mutations the flag's safety rests
// on are run against it: a read predicate that answered "managed" for
// everything must fail the not-managed rows, and a decision that dropped the
// refusal must fail the sessions that cannot answer one.
//
// The runs below are the product's own paths and not the rule: `fugaro update`
// over the real gate read in faked installations, and the two step-level
// surfaces the flag gives a prompt back at — the plugin wiring's y/N, and a
// billable build's typed name. A secret stays a hidden prompt in every case and
// is no part of this flag: nothing here reads one.

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
		{"a real terminal with --yes", initflow.Conditions{Terminal: true, Yes: true}},
		{"a coding agent's session at a terminal", initflow.Conditions{Terminal: true, Agent: "CLAUDECODE"}},
		{"a coding agent's session with no terminal", initflow.Conditions{Agent: "CLAUDECODE"}},
		{"a pipe", initflow.Conditions{}},
		{"--json at a terminal", initflow.Conditions{Terminal: true, JSON: true}},
		{"--non-interactive at a terminal", initflow.Conditions{Terminal: true, NonInteractive: true}},
	}
}

// askFlagStates is the three flag states, in the spelling the rule's own
// comments use.
var askFlagStates = []string{"neither", "--yes", "--ask"}

// askOracle is the flag's whole rule, written by hand and never derived from
// initflow.AskDecision:
//
//   - a run that did not pass the flag prints nothing and refuses nothing: the
//     flag decides nothing about a confirmation it was not asked for;
//   - --ask in a project the marks did NOT answer "managed" for prints the
//     no-op line, in every session: there is nothing to restore, and a refusal
//     would fail a command that behaves exactly as it always has;
//   - --ask in a managed project prints the restored line at a real terminal
//     and REFUSES in every session that cannot answer a prompt. It never
//     proceeds on the gate's verdict instead, which is the one answer that
//     would make the flag a way to write without the confirmation it asked for.
func askOracle(isManaged bool, flag string, c initflow.Conditions) (line, refusal string) {
	if flag != "--ask" {
		return "", ""
	}
	if !isManaged {
		return initflow.AskUnmanaged(), ""
	}
	if c.Agent != "" || c.NonInteractive || c.JSON || !c.Terminal {
		return "", initflow.AskRefusal(c)
	}
	return initflow.AskRestored(), ""
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
// The two classes are walked beside the sessions. --ask is the same decision for
// both (AskDecision takes no class), and what the rows assert about them is that
// the flag takes the gate's verdict out of BOTH: a step of the stricter class is
// never left to the gate by the flag that asked for the confirmations, which is
// the direction a mistake here would fall in.
func askViolations(decide askDecider, mode func(bool, string, error) bool, reads []managedRead) (bad []string, n int) {
	for _, rd := range reads {
		isManaged := mode(rd.ok, rd.reason, rd.err)
		// The gate's own verdict for this read with the plan admitted: the most
		// the gate ever gives, so a class it is not taken out of here is one
		// --ask cannot take out at all.
		m := initflow.Managed{Managed: rd.managed, Admitted: rd.managed}
		for _, flag := range askFlagStates {
			for _, s := range askSessions() {
				n++
				c := s.c
				c.Ask = flag == "--ask"
				c.Yes = c.Yes || flag == "--yes"
				f := askFlags{ask: flag == "--ask", yes: flag == "--yes"}
				wantLine, wantRefusal := askOracle(rd.managed, flag, c)
				line, refusal := decide(f, isManaged, c)
				where := rd.state + ", " + flag + ", " + s.name
				if line != wantLine {
					bad = append(bad, where+": line "+askQuote(line)+", want "+askQuote(wantLine))
				}
				if refusal != wantRefusal {
					bad = append(bad, where+": refusal "+askQuote(refusal)+", want "+askQuote(wantRefusal))
				}
				if c.Ask {
					for _, class := range []struct {
						class initflow.ConfirmClass
						name  string
					}{{initflow.Typed, "Typed"}, {initflow.YesOrTyped, "YesOrTyped"}} {
						if initflow.GateConfirms(class.class, c, m) {
							bad = append(bad, where+": the gate still stands in for a "+class.name+" step")
						}
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
		"a pipe", "--json at a terminal", "--non-interactive at a terminal",
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
			if !isManaged || c.Agent != "" || c.NonInteractive || c.JSON || !c.Terminal {
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
		{"not managed", func(t *testing.T, r *initRig) { r.markedRuns(t, "", "") }},
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
// owner's to confirm, one typed name for all of its steps, exactly as it is
// without the flag (TestMarkedProjectTakesOnePrompt). Nothing is applied
// because the rig's stdin holds nothing to type, which is the assertion that
// the question is a real one and not a line about a question.
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
	if !strings.Contains(got, "Type "+initProjectName+" to apply all of the above") {
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

// And with the project's name typed, the run goes: the flag gives the
// confirmation back and the confirmation is answered the way this command has
// always taken it — the one review typed once for the run's ordinary steps.
func TestUpdateManagedAskAppliesOnTheTypedName(t *testing.T) {
	r := askUpdateRig(t, askMarkWorlds()[0])
	fakeTerminal(t)
	noAgentSession(t)

	got, err := askUpdateRun(t, r, initProjectName+"\n", "update", "--ask")
	if !strings.Contains(got, initflow.AskRestored()) {
		t.Errorf("the run does not say what the flag did:\n%s", got)
	}
	if n := len(r.ran(t, "apply")); n == 0 {
		t.Errorf("nothing was applied after the name was typed at the confirmation --ask gave back (err %v):\n%s", err, got)
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
// the gate to judge less.
func TestUpdateManagedAskKeepsTheGatesOwnRefusalFirst(t *testing.T) {
	const a = "user:a@example.com"
	address := `module.installation.google_storage_bucket_iam_member.state["` + a + `"]`
	r := updateRig(t)
	r.managedRuns(t, initProjectName, initProject)
	isolateGateCredentials(t)
	fakeTerminal(t)
	noAgentSession(t)
	updating(t, r)
	r.setPlan(t, grantChange("state", a, "roles/storage.objectAdmin", "delete"))

	got, err := askUpdateRun(t, r, initProjectName+"\n", "update", "--ask")
	if err == nil {
		t.Fatalf("--ask applied a plan the managed gate refuses:\n%s", got)
	}
	if !strings.Contains(got, managedmode.NotConverged) || !strings.Contains(got, address) {
		t.Errorf("the refusal is not the gate's own, naming what it refused:\n%s", got)
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("%d terraform apply(s) ran for a plan the gate refused", n)
	}
}

// A project the marks did not answer "managed" for: --ask says so, and it is
// not an error. This is rule 3 of the flag — the no-op — in both sessions whose
// mistake would be expensive: a refusal in a coding agent's session would stop
// a command that behaves exactly as it always has, and the restored line at a
// person's terminal would claim confirmations were replaced that never were.
// The comparison is the assertion: the flagged run IS the plain run with the
// flag's one line taken out of the middle, and nothing else moved.
func TestUpdateAskInAProjectThatIsNotManagedIsANoOp(t *testing.T) {
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

				plain, _ := updateRun(t, r, "update")
				flagged, _ := updateRun(t, r, "update", "--ask")

				if !strings.Contains(flagged, initflow.AskUnmanaged()) {
					t.Errorf("--ask did not say what the marks answered:\n%s", flagged)
				}
				if strings.Contains(flagged, initflow.AskRestored()) || strings.Contains(flagged, "never a silent proceed") {
					t.Errorf("--ask answered as if the project were managed:\n%s", flagged)
				}
				if stripped := withoutRunOwn(strings.Replace(flagged, initflow.AskUnmanaged()+"\n", "", 1)); stripped != withoutRunOwn(plain) {
					t.Errorf("--ask changed the run in a project that is not managed (the flag's line stripped from the middle).\nwithout: %s\nwith:    %s", plain, flagged)
				}
				// The flag is a sentence and not a decision: a project that
				// is not managed applies nothing here with or without it.
				if n := len(r.ran(t, "apply")); n != 0 {
					t.Errorf("--ask applied %d step(s) in a project that is not managed", n)
				}
			})
		}
	}
}

// A run the gate never covers: --plan-only applies nothing by its own
// definition, so there is no step whose confirmation the gate stands in for.
// The flag says that, and the run is otherwise the run it always was.
func TestUpdateAskOnAPlanOnlyRunSaysThereWasNothingToRestore(t *testing.T) {
	r := askUpdateRig(t, askMarkWorlds()[0])
	fakeTerminal(t)
	noAgentSession(t)

	plain, plainErr := updateRun(t, r, "update", "--plan-only")
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

// The two flags contradict each other, and the refusal is made before anything
// is read: no mark, no gate, no prompt, no write.
func TestUpdateAskAndYesAreRefused(t *testing.T) {
	r := askUpdateRig(t, askMarkWorlds()[0])
	fakeTerminal(t)
	noAgentSession(t)

	got, err := updateRun(t, r, "update", "--ask", "--yes")
	if err == nil {
		t.Fatalf("the run honoured two contradictory flags:\n%s", got)
	}
	if !strings.Contains(got, initflow.AskAndYes()) {
		t.Errorf("the refusal is not the pair's own sentence:\n%s", got)
	}
	if n := markReads(r); n != 0 {
		t.Errorf("the pair was refused after %d mark read(s)", n)
	}
	if n := len(r.ran(t, "apply")); n != 0 {
		t.Errorf("%d apply(s) ran", n)
	}
}

// The plugin wiring's y/N is one of the questions a managed project takes no
// confirmation for today (the stage's own Apply, driven here under the gate),
// and it is the flag's most ordinary surface: with --ask the y/N is asked, and
// with an empty stdin the answer is no and the file is not written. The control
// is the same call without the flag, which writes and prints the gate's
// sentence instead.
func TestManagedAskGivesThePluginWiringItsYOrNBack(t *testing.T) {
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
			e.r.gate = &initGate{managed: true}

			_, err := newPluginStage(e).Apply(t.Context(), initflow.Env{Interactive: true})
			got, _ := os.ReadFile(settings)
			written := string(got) != settingsWithOthers
			if !strings.Contains(out.String(), "extraKnownMarketplaces") {
				t.Fatalf("the step printed no diff of what it would write:\n%s", out.String())
			}
			if !ask {
				if err != nil || !written || !strings.Contains(out.String(), initflow.GateOrdinary()) {
					t.Fatalf("the control did not take the gate's verdict (err %v, written %v):\n%s", err, written, out.String())
				}
				return
			}
			if err == nil || written {
				t.Fatalf("--ask wrote the settings with nothing typed (err %v, written %v):\n%s", err, written, out.String())
			}
			if !strings.Contains(out.String(), "Write this to "+settings+"? [y/N]") {
				t.Errorf("--ask did not ask the y/N the gate stood in for:\n%s", out.String())
			}
			if strings.Contains(out.String(), initflow.GateOrdinary()) || strings.Contains(out.String(), initflow.GateProceeded()) {
				t.Errorf("--ask printed the gate's sentence instead of asking:\n%s", out.String())
			}
		})
	}
}

// A billable build: askBuild is step 4's per-build confirmation, the project's
// name typed, and in a managed project's own terminal the gate's verdict stands
// in for it today (GateProceeded). --ask is exactly the flag for that surface —
// the typed name comes back — and the control is the same call without it,
// which confirms on the gate's verdict and prints its sentence instead. Nothing
// else moves: the class is the same YesOrTyped build either way.
func TestManagedAskGivesTheBuildsTypedNameBack(t *testing.T) {
	g := initflow.Managed{Managed: true, Admitted: true}
	for _, ask := range []bool{false, true} {
		name := "the flag"
		if !ask {
			name = "no flag (the control)"
		}
		t.Run(name, func(t *testing.T) {
			r, _ := confirmRig(t, confirmRow{terminal: true, ask: ask}, initProjectName)
			out := &syncBuf{}
			r.w = out
			fakeTerminal(t)
			noAgentSession(t)

			confirmed, reachable, err := r.askBuild("builds the image for acme/app", g)
			if err != nil {
				t.Fatal(err)
			}
			if !reachable || !confirmed {
				t.Fatalf("reachable=%v confirmed=%v (the project's name is typed at the prompt in both worlds)", reachable, confirmed)
			}
			if ask {
				if !strings.Contains(out.String(), "Type "+initProjectName+" to confirm: ") {
					t.Errorf("--ask did not ask for the project's name:\n%s", out.String())
				}
				if strings.Contains(out.String(), initflow.GateProceeded()) {
					t.Errorf("--ask printed the gate's verdict instead of asking:\n%s", out.String())
				}
				return
			}
			if !strings.Contains(out.String(), initflow.GateProceeded()) {
				t.Errorf("the control did not take the gate's verdict:\n%s", out.String())
			}
			if strings.Contains(out.String(), "to confirm: ") {
				t.Errorf("the control asked a question the gate stands in for:\n%s", out.String())
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
