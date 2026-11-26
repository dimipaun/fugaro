package initflow

import (
	"strings"
	"testing"
)

// This file is the managed case's own rule, tested where it is written rather
// than only where it is called (internal/cli's tests drive the callers).
//
// The two facts the whole managed case rests on are stated here as a table and
// not as a re-run of the code: Managed has three fields and Conditions five, so
// the product is small enough to enumerate, and every row below says what a
// person reading the design would say the answer is.
//
// Two properties are the reason the file exists at all, and both are the
// direction a mistake falls:
//
//   - The gate never widens a step no plan for it exists. A step the Converge
//     refuses (managed.ConvergeUnjudged, D1026) reaches GateStandsIn with
//     Admitted false, so the answer is the one it had before the managed case
//     existed whatever the project's marks say. The row that would break is the
//     one where a managed project's verdict is read as permission for a billable
//     build or a loosened ceiling.
//   - The gate never answers where the run's own reader said what the run does
//     with a question: --json (which confirms no step, the console's whole
//     contract), --non-interactive, and --ask, which is the owner asking for
//     the question back. A managed project's verdict stands in for a step's
//     confirmation wherever nobody can be asked — the owner's own terminal, a
//     coding agent's session, and a session nobody is at, which is the pipe
//     `fugaro update` is driven in by a script, a cron or check 292 (the
//     owner's, 2026-11-26) — and never in a run whose reader asked for
//     something else.

// allConditions is the whole product of the six fields a run says about who
// is there: 64 rows, each reachable from the CLI. --ask is one of them because
// it is a fact about the run and not about a plan: the owner asking for the
// confirmations back is a condition every predicate below is decided under,
// and a table without it would test a rule the CLI can pass a different value
// to. So are the two facts a run carries rather than a command: a run that
// carries its own answer and one that never prompts are rows every predicate
// here reads, and a table without them would test those rules on one value.
func allConditions() []Conditions {
	markers := []string{"", "CLAUDECODE"}
	var out []Conditions
	for _, yes := range []bool{false, true} {
		for _, ni := range []bool{false, true} {
			for _, js := range []bool{false, true} {
				for _, term := range []bool{false, true} {
					for _, agent := range markers {
						for _, ask := range []bool{false, true} {
							out = append(out, Conditions{Yes: yes, NonInteractive: ni, JSON: js, Terminal: term, Agent: agent, Ask: ask})
						}
					}
				}
			}
		}
	}
	if len(out) != 64 {
		panic("the conditions product is not 64 rows")
	}
	return out
}

// allManaged is the whole product of the three facts a step is decided under:
// eight rows, one of which is the zero value every caller that read nothing
// passes.
func allManaged() []Managed {
	var out []Managed
	for _, managed := range []bool{false, true} {
		for _, admitted := range []bool{false, true} {
			for _, fallback := range []bool{false, true} {
				out = append(out, Managed{Managed: managed, Admitted: admitted, Fallback: fallback})
			}
		}
	}
	if len(out) != 8 {
		panic("the managed product is not 8 rows")
	}
	return out
}

func managedName(m Managed) string {
	return "Managed{Managed:" + boolWord(m.Managed) + ", Admitted:" + boolWord(m.Admitted) + ", Fallback:" + boolWord(m.Fallback) + "}"
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func conditionsName(c Conditions) string {
	return "Conditions{Yes:" + boolWord(c.Yes) + ", NonInteractive:" + boolWord(c.NonInteractive) +
		", JSON:" + boolWord(c.JSON) + ", Terminal:" + boolWord(c.Terminal) + ", Agent:" + c.Agent +
		", Ask:" + boolWord(c.Ask) + "}"
}

// TestManagedTheGateIgnoresTheRunsOwnAnswer is the property the owner's ruling
// rests on: whether the run carried an answer of its own is not asked for and
// does not decide. A managed project whose gate stood in only for a run that
// had answered beforehand would be a project whose owner is still asked for it,
// which is the whole of what the ruling removes.
//
// The mutation caught: putting c.Yes into GateStandsIn or PersonAtTheTerminal —
// reading the run's own answer as though it were who is there.
func TestManagedTheGateIgnoresTheRunsOwnAnswer(t *testing.T) {
	for _, c := range allConditions() {
		if c.Yes {
			continue
		}
		flipped := c
		flipped.Yes = true
		for _, m := range allManaged() {
			if got, want := GateStandsIn(flipped, m), GateStandsIn(c, m); got != want {
				t.Errorf("GateStandsIn(%s, %s) = %v with the run's own answer and %v without: the gate's answer is about the document and who is there, not about what the run carried",
					conditionsName(flipped), managedName(m), got, want)
			}
			if got, want := PersonAtTheTerminal(flipped), PersonAtTheTerminal(c); got != want {
				t.Errorf("PersonAtTheTerminal(%s) = %v with the run's own answer and %v without: who is there does not depend on what the run carried",
					conditionsName(flipped), got, want)
			}
		}
	}
}

// TestManagedRemovesHoldsEveryFactAtOnce pins the three facts and what each of
// them alone does: dropping any one leaves today's confirmation in place.
//
// The mutant predicates below are the ways this rule is misread, plus the
// shape every mistake ends in — a predicate that answers true whatever it is
// handed. Each is asserted to differ from Managed.Removes on at least one row
// of the table, so the table is the thing that catches it: this is the mutation
// check, run rather than described.
func TestManagedRemovesHoldsEveryFactAtOnce(t *testing.T) {
	mutants := []struct {
		name string
		fn   func(Managed) bool
	}{
		// Every one of these is an edit somebody would make to widen the exit
		// from a prompt, and each is caught by a row below.
		{"the project's marks alone, with no verdict", func(m Managed) bool {
			return m.Managed && !m.Fallback
		}},
		{"the verdict alone, with no marks", func(m Managed) bool {
			return m.Admitted && !m.Fallback
		}},
		{"the inventory's fallback ignored", func(m Managed) bool {
			return m.Managed && m.Admitted
		}},
		{"every step of every project", func(Managed) bool { return true }},
	}
	for _, mut := range mutants {
		t.Run(mut.name, func(t *testing.T) {
			caught := ""
			for _, m := range allManaged() {
				if mut.fn(m) != m.Removes() {
					caught = managedName(m)
					break
				}
			}
			if caught == "" {
				t.Fatalf("the table accepts %q: no row separates it from Removes, so the rule above is not what is being tested", mut.name)
			}
			t.Logf("%s is caught by %s", mut.name, caught)
		})
	}
	// And the rule itself, in the three-fact form the design states, for every
	// row: managed, admitted by the gate, and no fallback.
	for _, m := range allManaged() {
		want := m.Managed && m.Admitted && !m.Fallback
		if got := m.Removes(); got != want {
			t.Errorf("Removes() for %s = %v, want %v", managedName(m), got, want)
		}
	}
}

// TestManagedGateStandsInInEverySession pins what a caller about to show a
// prompt asks, in the three sessions the answer is read in: under a coding
// agent's marker and in a session nobody is at the answer is Removes, and at
// the owner's own terminal it is PersonAtTheTerminal beside Removes. The
// marker's session and the owner's are told apart by the marker because they
// are different sentences to two different readers (AgentProceeded's and
// GateProceeded's), and a caller that conflated them would print the wrong one;
// the session nobody is at takes the owner's sentence, because the verdict
// written and the plan shown are what it has to read instead of an answer.
//
// The mutation caught: PersonAtTheTerminal dropping its `c.Agent == ""` — the
// terminal sentence printed to a coding agent's session — or the two halves
// being swapped at a call site, which this equality makes a
// compile-time-looking mistake rather than a silent one. And the one this
// predicate got wrong until check 292: answering a pipe with a step's own
// confirmation, which is a demand for a person in a run that has none.
//
// --ask, --json and --non-interactive answer false here whatever else is true
// of the row, except under a marker: a coding agent's session is the agent's
// case whatever else is true of it (the marker decides, --json included), and
// the two flags are written into the oracle rather than taken from the code
// because they say what the run does with a question (one asked for a document,
// the other for a run that puts nothing to anybody) rather than where the run
// is. The owner asking for the prompts back is exactly "the gate does not
// confirm this step".
func TestManagedGateStandsInInEverySession(t *testing.T) {
	for _, c := range allConditions() {
		for _, m := range allManaged() {
			var want bool
			switch {
			case c.Ask:
				want = false
			case c.Agent != "":
				want = m.Removes()
			case c.JSON, c.NonInteractive:
				want = false
			default:
				want = m.Removes()
			}
			if got := GateStandsIn(c, m); got != want {
				t.Errorf("GateStandsIn(%s, %s) = %v, want %v", conditionsName(c), managedName(m), got, want)
			}
			// The gate's answer is never a licence to apply a step no verdict
			// admitted, whatever the session: Removes is asked in every arm.
			if GateStandsIn(c, m) && !m.Removes() {
				t.Errorf("GateStandsIn(%s, %s) is true with no verdict behind it", conditionsName(c), managedName(m))
			}
			// A coding agent's session is the one the marker decides: the
			// gate's sentence names a verdict there and never a person, so a
			// row that reached the terminal half would print the wrong one.
			if c.Agent != "" && GateStandsIn(c, m) && PersonAtTheTerminal(c) {
				t.Errorf("GateStandsIn(%s, %s) is true through the terminal half in a coding agent's session", conditionsName(c), managedName(m))
			}
		}
	}
}

// TestNobodyIsAtTheSessionIsThePipeAndNothingElse: the third session is the run
// no person is at and no flag speaks for — no coding agent's marker, no
// terminal, neither --json nor --non-interactive, and no --ask. It is the pipe
// with its input closed a script, a cron or a live check drives `fugaro update`
// in, and the case check 292 (the owner's, 2026-11-26) found answering with a
// confirmation there was nobody to give.
//
// The two flags and the flag of the owner are the mutations caught: read as
// this session, each of them would have the gate answer a run whose own reader
// asked for something else — a document, a run that puts nothing to anybody, or
// the question back.
func TestNobodyIsAtTheSessionIsThePipeAndNothingElse(t *testing.T) {
	for _, c := range allConditions() {
		want := c.Agent == "" && !c.Terminal && !c.NonInteractive && !c.JSON && !c.Ask
		if got := NobodyIsAtTheSession(c); got != want {
			t.Errorf("NobodyIsAtTheSession(%s) = %v, want %v", conditionsName(c), got, want)
		}
		// The session nobody is at and the owner's own are the two halves of
		// the same question, so no row is both and no row is neither of the
		// two ways the gate stands in.
		if got := PersonAtTheTerminal(c); got && NobodyIsAtTheSession(c) {
			t.Errorf("%s is both the owner's session and a session nobody is at", conditionsName(c))
		}
	}
}

// TestManagedGateStandsInIsTheOwnerAtARealTerminal: the owner's own session is
// the case where a person is there to be asked and the gate answers instead,
// and the two sentences it can print are told apart by the marker
// (TestManagedGateStandsInInEverySession's oracle). What this pins is the
// half that is about who is there: where a person is at the terminal and the
// verdict is Removes, the gate stands in for the step's confirmation, and in
// every other row of the table it is the session's own answer that decides.
func TestManagedGateStandsInIsTheOwnerAtARealTerminal(t *testing.T) {
	for _, c := range allConditions() {
		for _, m := range allManaged() {
			got := PersonAtTheTerminal(c) && m.Removes()
			switch {
			case c.Ask, c.Agent != "", !c.Terminal, c.NonInteractive, c.JSON:
				if got {
					t.Errorf("the owner half of GateStandsIn(%s, %s) is true with nobody at the terminal", conditionsName(c), managedName(m))
				}
			default:
				if want := m.Removes(); got != want {
					t.Errorf("the owner half of GateStandsIn(%s, %s) = %v, want Removes = %v", conditionsName(c), managedName(m), got, want)
				}
			}
		}
	}
}

// TestPersonAtTheTerminalIsTheOwnerAndNothingElse is the predicate the ruling of
// 2026-11-06 is decided under, and it is the half of GateStandsIn that is
// about WHO is there rather than about what a document said: the owner's own
// session, at a real terminal, with no coding agent's marker in the
// environment, nothing asked in JSON, and no --ask putting the prompts back.
//
// Two properties are what it exists for, and each is a direction a mistake
// falls in. A marker in the environment is never the owner, whatever else the
// row says (that is the mutation a "person at a terminal" read as "a terminal"
// would make: a coding agent's session is a terminal too). And a run nobody is
// at — a pipe, --json, a run that never prompts, --ask — is never the owner
// either: the owner's sentence is for a person who is there, and what a run
// nobody is at reads is the other half of the same question
// (NobodyIsAtTheSession, whose own test pins the truth table).
func TestPersonAtTheTerminalIsTheOwnerAndNothingElse(t *testing.T) {
	for _, c := range allConditions() {
		want := c.Agent == "" && c.Terminal && !c.NonInteractive && !c.JSON && !c.Ask
		if got := PersonAtTheTerminal(c); got != want {
			t.Errorf("PersonAtTheTerminal(%s) = %v, want %v", conditionsName(c), got, want)
		}
	}
}

// TestGateSentencesNameNoWayPast: the two sentences a run prints when the gate
// stood in for a confirmation are read by the person the prompt would have
// asked, and by nobody else — but a coding agent's session can read them too
// (it is the session that can change the line above), so they follow
// AgentRefusal's own rule and name no flag, no variable and no marker a reader
// could set to reach past the gate. What GateProceeded names instead is where
// the verdict was written, because wanting to check afterwards is not trying to
// get past anything.
func TestGateSentencesNameNoWayPast(t *testing.T) {
	for _, s := range []struct{ name, sentence string }{
		{"GateProceeded", GateProceeded()},
		{"GateOrdinary", GateOrdinary()},
		{"OwnerProceeded", OwnerProceeded()},
	} {
		for _, no := range []string{"--", "=", "unset ", "CLAUDECODE", "CLAUDE_CODE", "AI_AGENT", "FUGARO_", "export ", "Type "} {
			if strings.Contains(s.sentence, no) {
				t.Errorf("%s contains %q: a sentence a session can read names no flag, variable or marker to reach the step with: %q", s.name, no, s.sentence)
			}
		}
		if !strings.Contains(s.sentence, "managed") {
			t.Errorf("%s does not say why nothing is asked: %q", s.name, s.sentence)
		}
		if strings.Contains(s.sentence, AgentAdvice) {
			t.Errorf("%s carries the coding agent's own advice: the reader here is the person the prompt would have asked: %q", s.name, s.sentence)
		}
	}
	if GateProceeded() == GateOrdinary() {
		t.Error("the two sentences are one: they answer about two different things — a document the gate judged, and a step the run announced")
	}
	if GateOrdinary() == OwnerProceeded() || GateProceeded() == OwnerProceeded() {
		t.Error("the sentences are one: OwnerProceeded speaks for a step no rule judged, and claiming a verdict there is the direction this file refuses to take")
	}
	if !strings.Contains(GateProceeded(), "record") {
		t.Errorf("GateProceeded does not say where the verdict can be read afterwards: %q", GateProceeded())
	}
	if strings.Contains(OwnerProceeded(), "verdict") || strings.Contains(OwnerProceeded(), "gate") {
		t.Errorf("OwnerProceeded claims a verdict for a step no rule judged: %q", OwnerProceeded())
	}
}

// TestManagedAskTakesTheGateOutOfTheDecision is --ask's own rule, and it is
// the property the flag's safety rests on: asking for the confirmations back
// puts the gate's verdict out of the decision, so the answer is the class
// rule's — the answer the run would have had if the project were not managed —
// under every Managed value. Two directions are checked, and they are the two
// a mistake falls in: the project's marks stop deciding anything (a row where
// some Managed changes the answer with the flag set is a managed project still
// answering on its verdict), and no row is admitted that the same run without
// the flag, and with nothing read, does not admit (the flag restores prompts,
// it never removes one).
//
// A coding agent's session is the one place the flag's answer is not the plain
// class rule's: it is refused there in both the rule (CanConfirmManaged's own
// marker branch) and the caller (AskRefusal), because a prompt nobody can
// answer is a refusal and never a silent proceed — including the silently
// managed proceed the mark would otherwise give.
//
// The mutation caught: GateStandsIn (or PersonAtTheTerminal) dropping its
// `c.Ask`, which is a managed project answering a step with its verdict on a
// run whose owner asked to be asked.
func TestManagedAskTakesTheGateOutOfTheDecision(t *testing.T) {
	for _, c := range allConditions() {
		if !c.Ask {
			continue
		}
		for _, m := range allManaged() {
			if GateStandsIn(c, m) {
				t.Errorf("GateStandsIn(%s, %s) is true with the confirmations asked for: the gate is nobody's answer in that run",
					conditionsName(c), managedName(m))
			}
			if PersonAtTheTerminal(c) {
				t.Errorf("PersonAtTheTerminal(%s) is true with the confirmations asked for: the flag puts the question back, and the owner reads it as one",
					conditionsName(c))
			}
			// A coding agent's session is refused under the flag exactly as it
			// is without it: a prompt nobody can answer is a refusal and never
			// a silent proceed, the silently managed one included.
			if c.Agent != "" && CanConfirmRun(c) {
				t.Errorf("CanConfirmRun(%s) is true: a coding agent's session answers no confirmation (--ask included)", conditionsName(c))
			}
		}
	}
}

// TestAskDecisionIsTheFlagsWholeRule enumerates the decision, by hand: the
// no-op line wherever the marks did not answer "managed" and the flag turns no
// value entry on (and a refusal nowhere there), the restored line for the
// owner who can and the refusal wherever a managed project's run cannot
// prompt, and, for the update door whose --ask also asks for values, the
// AskValues line at a terminal and its refusal where nobody can answer one.
// The rows are written out rather than derived, so a rule that answered a line
// and a refusal at once, or answered a managed project's terminal with a
// refusal, is a failure here.
func TestAskDecisionIsTheFlagsWholeRule(t *testing.T) {
	for _, c := range allConditions() {
		canPrompt := c.Agent == "" && c.Terminal && !c.NonInteractive && !c.JSON
		for _, managed := range []bool{false, true} {
			for _, values := range []bool{false, true} {
				line, refusal := AskDecision(c, managed, values)
				switch {
				case managed && canPrompt:
					if refusal != "" || line != AskRestored() {
						t.Errorf("AskDecision(%s, managed=true, values=%v) = %q, %q, want the restored line and no refusal", conditionsName(c), values, line, refusal)
					}
				case managed:
					if line != "" || refusal != AskRefusal(c) {
						t.Errorf("AskDecision(%s, managed=true, values=%v) = %q, %q, want the refusal and no line", conditionsName(c), values, line, refusal)
					}
					if !strings.Contains(refusal, askNobody(c)) {
						t.Errorf("AskDecision(%s, managed=true) refuses with %q, which does not say why nobody can answer it (%q)", conditionsName(c), refusal, askNobody(c))
					}
				case !values:
					if refusal != "" || line != AskUnmanaged() {
						t.Errorf("AskDecision(%s, managed=false, values=false) = %q, %q, want the no-op line and no refusal", conditionsName(c), line, refusal)
					}
				case canPrompt:
					if refusal != "" || line != AskValues() {
						t.Errorf("AskDecision(%s, managed=false, values=true) = %q, %q, want the values line and no refusal", conditionsName(c), line, refusal)
					}
				default:
					if line != "" || refusal != "" {
						t.Errorf("AskDecision(%s, managed=false, values=true) = %q, %q, want nothing from the flag where no terminal can ask a value", conditionsName(c), line, refusal)
					}
				}
			}
		}
	}
	// A run with no terminal at all is the case the refusal exists for, and it
	// must never read as a proceed: the sentence says nothing is applied, and
	// it is the session's own advice (the person's terminal), the way every
	// refusal for a session nobody can ask is.
	for _, c := range []Conditions{{Agent: "CLAUDECODE"}, {JSON: true}, {}, {Terminal: true, JSON: true}} {
		_, refusal := AskDecision(c, true, false)
		if !strings.Contains(refusal, "nothing is applied") {
			t.Errorf("the refusal for %s does not say that nothing is applied: %q", conditionsName(c), refusal)
		}
		if !strings.Contains(refusal, AgentAdvice) {
			t.Errorf("the refusal for %s does not carry the way out that every refusal for a session nobody can ask carries: %q", conditionsName(c), refusal)
		}
		// The update door's value entry is not this refusal: where no terminal
		// can ask a value the flag prints and refuses nothing, and the stage
		// leaves its own `fugaro secrets set` lines instead (D1540).
		if line, vr := AskDecision(c, false, true); line != "" || vr != "" {
			t.Errorf("the update door's value entry answered %q, %q where no terminal can ask a value, want nothing from the flag", line, vr)
		}
	}
}

// TestAskRefusalNamesNoWayPast: the refusal of --ask is read by the session it
// refuses, and the one name it must not carry is the flag itself. Dropping
// --ask hands the step back to the gate's verdict, so a sentence that named it
// would be an instruction on how to have the write made without the owner —
// the rule AgentRefusal and AgentProceeded already follow, and the reason
// AgentAdvice names a terminal rather than a variable to unset.
func TestAskRefusalNamesNoWayPast(t *testing.T) {
	for _, c := range []Conditions{{Agent: "CLAUDECODE"}, {Agent: IDEMarker}, {JSON: true}, {}} {
		refusal := AskRefusal(c)
		for _, no := range []string{"--ask", "unset ", "export ", "="} {
			if strings.Contains(refusal, no) {
				t.Errorf("AskRefusal(%s) contains %q: the session it refuses can read it, and dropping the flag it would name is the way round this refusal: %q", conditionsName(c), no, refusal)
			}
		}
	}
}

// TestAskLinesAreOneLineEachAndNotEachOther: the three lines a command may
// print for the flag say three different things — the prompts are back, there
// was nothing to restore, the run never had a step for it — and a reader who
// saw the wrong one would be told something false about their project.
func TestAskLinesAreOneLineEachAndNotEachOther(t *testing.T) {
	lines := map[string]string{"AskRestored": AskRestored(), "AskUnmanaged": AskUnmanaged(), "AskNoGate": AskNoGate()}
	for name, line := range lines {
		if strings.Contains(line, "\n") {
			t.Errorf("%s is not one line: %q", name, line)
		}
		if !strings.HasPrefix(line, "--ask:") {
			t.Errorf("%s does not start by naming the flag it is about: %q", name, line)
		}
		if strings.Contains(line, AgentAdvice) {
			t.Errorf("%s carries the coding agent's own advice: the reader here is a person the flag belongs to: %q", name, line)
		}
	}
	for a, la := range lines {
		for b, lb := range lines {
			if a != b && la == lb {
				t.Errorf("%s and %s are one sentence: they answer about three different runs", a, b)
			}
		}
	}
	// The restored line is the one a reader could misread as "the gate is off".
	// It says the opposite, in the words the design uses: judged, refused,
	// unchanged.
	for _, want := range []string{"still judged", "refused", "unchanged"} {
		if !strings.Contains(AskRestored(), want) {
			t.Errorf("AskRestored does not say %q: the flag restores prompts and weakens nothing: %q", want, AskRestored())
		}
	}
	if !strings.Contains(AskUnmanaged(), "not an error") {
		t.Errorf("AskUnmanaged does not say the flag is not an error: a reader would take a no-op line for a refusal: %q", AskUnmanaged())
	}
}
