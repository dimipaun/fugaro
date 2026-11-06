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
//   - The gate never widens the class rule. A Typed step — money or
//     permanence, and no plan for any of it — takes the same answer whatever
//     Managed holds (D1026). The row that would break is the one where a
//     managed project's verdict is read as permission for a billable build or a
//     loosened ceiling.
//   - The gate never answers for a session nobody is at. Removes may be true in
//     a run with no terminal, which is why the terminal branch asks for one
//     beside it; a step that reached past it would be a managed project's
//     plan applied by a pipe or a --json run.

// allConditions is the whole product of the six fields a run says about who
// is there: 64 rows, each reachable from the CLI. --ask is one of them because
// it is a fact about the run and not about a plan: the owner asking for the
// confirmations back is a condition every predicate below is decided under,
// and a table without it would test a rule the CLI can pass a different value
// to.
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

func allClasses() []ConfirmClass { return []ConfirmClass{Typed, YesOrTyped} }

func className(c ConfirmClass) string {
	if c == Typed {
		return "Typed"
	}
	return "YesOrTyped"
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

// TestManagedADecisionNeverDependsOnTheFlag is the property the owner's ruling
// rests on: --yes is not asked for and does not decide. A managed project that
// needed a flag remembered at the terminal would be a project whose owner is
// still asked for it, which is the whole of what the ruling removes.
//
// The mutation caught: putting c.Yes into GateConfirms or ConfirmsByGate —
// dropping `!c.Yes` from the terminal branch, or writing the branch as
// CanConfirm is written one line below it.
func TestManagedADecisionNeverDependsOnTheFlag(t *testing.T) {
	for _, class := range allClasses() {
		for _, c := range allConditions() {
			if c.Yes {
				continue
			}
			flipped := c
			flipped.Yes = true
			for _, m := range allManaged() {
				if got, want := GateConfirms(class, flipped, m), GateConfirms(class, c, m); got != want {
					t.Errorf("GateConfirms(%s, %s, %s) = %v with --yes and %v without: the gate's answer is about the document and who is there, not about a flag",
						className(class), conditionsName(flipped), managedName(m), got, want)
				}
			}
		}
	}
}

// TestManagedNeverWidensATypedStep is D1026, and it is the one row of the
// table that must hold for EVERY Managed value: a Typed step is money or
// permanence — a billable image build, a loosened ceiling, a secret, a
// Firestore location — and no plan gate says anything about any of them. So
// the answer for a Typed step is the answer it had before the managed case
// existed, whatever the project's marks say.
//
// The mutation caught: Managed.Covers returning true for every class, which is
// the smallest edit that would let a managed project's verdict stand where the
// owner's own word is the only thing that may.
func TestManagedNeverWidensATypedStep(t *testing.T) {
	for _, c := range allConditions() {
		// The Typed rule as the design states it: a person at a real terminal,
		// with no flag and no pipe, and no coding agent's marker.
		want := c.Agent == "" && c.Terminal && !c.Yes && !c.NonInteractive && !c.JSON
		for _, m := range allManaged() {
			if got := CanConfirmManaged(Typed, c, m); got != want {
				t.Errorf("CanConfirmManaged(Typed, %s, %s) = %v, want %v: a Typed step is the owner's own word whatever a plan looked like",
					conditionsName(c), managedName(m), got, want)
			}
			if m.Removes(Typed) {
				t.Errorf("Removes(Typed) is true for %s: no plan is money or permanence", managedName(m))
			}
		}
	}
}

// TestManagedRemovesHoldsEveryFactAtOnce pins the four facts and what each of
// them alone does: dropping any one leaves today's confirmation in place.
//
// The mutant predicates below are the four ways this rule is misread, plus the
// shape every mistake ends in — a predicate that answers true whatever it is
// handed. Each is asserted to differ from Managed.Removes on at least one row
// of the table, so the table is the thing that catches it: this is the mutation
// check, run rather than described.
func TestManagedRemovesHoldsEveryFactAtOnce(t *testing.T) {
	mutants := []struct {
		name string
		fn   func(Managed, ConfirmClass) bool
	}{
		// Every one of these is an edit somebody would make to widen the exit
		// from a prompt, and each is caught by a row below.
		{"the project's marks alone, with no verdict", func(m Managed, class ConfirmClass) bool {
			return m.Managed && m.Covers(class) && !m.Fallback
		}},
		{"the verdict alone, with no marks", func(m Managed, class ConfirmClass) bool {
			return m.Admitted && m.Covers(class) && !m.Fallback
		}},
		{"the class rule dropped", func(m Managed, class ConfirmClass) bool {
			return m.Managed && m.Admitted && !m.Fallback
		}},
		{"the inventory's fallback ignored", func(m Managed, class ConfirmClass) bool {
			return m.Managed && m.Admitted && m.Covers(class)
		}},
		{"every step of every project", func(Managed, ConfirmClass) bool { return true }},
	}
	for _, mut := range mutants {
		t.Run(mut.name, func(t *testing.T) {
			caught := ""
			for _, class := range allClasses() {
				for _, m := range allManaged() {
					if mut.fn(m, class) != m.Removes(class) {
						caught = className(class) + ", " + managedName(m)
						break
					}
				}
				if caught != "" {
					break
				}
			}
			if caught == "" {
				t.Fatalf("the table accepts %q: no row separates it from Removes, so the rule above is not what is being tested", mut.name)
			}
			t.Logf("%s is caught by %s", mut.name, caught)
		})
	}
	// And the rule itself, in the four-fact form the design states, for every
	// row: managed, admitted, of a class the gate covers and no fallback.
	for _, class := range allClasses() {
		for _, m := range allManaged() {
			want := m.Managed && m.Admitted && m.Covers(class) && !m.Fallback
			if got := m.Removes(class); got != want {
				t.Errorf("Removes(%s) for %s = %v, want %v", className(class), managedName(m), got, want)
			}
		}
	}
}

// TestManagedGateConfirmsInBothSessions pins what a caller about to show a
// prompt asks: under a coding agent's marker the answer is Removes, and in a
// session with none it is ConfirmsByGate. The two are told apart by the marker
// because they are different sentences to two different readers
// (AgentProceeded's and GateProceeded's), and a caller that conflated them
// would print the wrong one.
//
// The mutation caught: ConfirmsByGate dropping its `c.Agent == ""` — the
// terminal sentence printed to a coding agent's session — or the two
// predicates being swapped at a call site, which this equality makes a
// compile-time-looking mistake rather than a silent one.
//
// --ask is the one condition that answers false here whatever else is true of
// the row, and it is written into the oracle rather than taken from the code:
// the owner asking for the prompts back is exactly "the gate does not confirm
// this step", in both sessions.
func TestManagedGateConfirmsInBothSessions(t *testing.T) {
	for _, class := range allClasses() {
		for _, c := range allConditions() {
			for _, m := range allManaged() {
				var want bool
				switch {
				case c.Ask:
					want = false
				case c.Agent != "":
					want = m.Removes(class)
				default:
					want = ConfirmsByGate(class, c, m)
				}
				if got := GateConfirms(class, c, m); got != want {
					t.Errorf("GateConfirms(%s, %s, %s) = %v, want %v", className(class), conditionsName(c), managedName(m), got, want)
				}
				// A step the gate confirms is a step the whole rule lets
				// through: the gate never hands a caller a step its own class
				// rule then refuses, which is what a caller would print a
				// "nothing is asked" sentence in front of.
				if gate := GateConfirms(class, c, m); gate && !CanConfirmManaged(class, c, m) {
					t.Errorf("GateConfirms(%s, %s, %s) is true and CanConfirmManaged is false", className(class), conditionsName(c), managedName(m))
				}
			}
		}
	}
}

// TestManagedConfirmsByGateIsTheOwnerAtARealTerminal: the terminal case is the
// same four facts plus the one fact about who is there, and the case it must
// never be true of is the run nobody is at — a pipe, --json,
// --non-interactive, a session with a coding agent's marker in it, or a run
// whose owner asked for the prompts back (--ask: the gate is then nobody's
// answer, which is the whole of what the flag is for).
func TestManagedConfirmsByGateIsTheOwnerAtARealTerminal(t *testing.T) {
	for _, class := range allClasses() {
		for _, c := range allConditions() {
			for _, m := range allManaged() {
				got := ConfirmsByGate(class, c, m)
				switch {
				case c.Ask, c.Agent != "", !c.Terminal, c.NonInteractive, c.JSON:
					if got {
						t.Errorf("ConfirmsByGate(%s, %s, %s) is true with nobody at the terminal", className(class), conditionsName(c), managedName(m))
					}
				default:
					if want := m.Removes(class); got != want {
						t.Errorf("ConfirmsByGate(%s, %s, %s) = %v, want Removes = %v", className(class), conditionsName(c), managedName(m), got, want)
					}
				}
			}
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
	if !strings.Contains(GateProceeded(), "record") {
		t.Errorf("GateProceeded does not say where the verdict can be read afterwards: %q", GateProceeded())
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
// The mutation caught: GateConfirms (or ConfirmsByGate) dropping its `c.Ask`,
// which is a managed project answering a step with its verdict on a run whose
// owner asked to be asked.
func TestManagedAskTakesTheGateOutOfTheDecision(t *testing.T) {
	for _, class := range allClasses() {
		for _, c := range allConditions() {
			if !c.Ask {
				continue
			}
			plain := c
			plain.Ask = false
			for _, m := range allManaged() {
				if GateConfirms(class, c, m) {
					t.Errorf("GateConfirms(%s, %s, %s) is true with the confirmations asked for: the gate is nobody's answer in that run",
						className(class), conditionsName(c), managedName(m))
				}
				got := CanConfirmManaged(class, c, m)
				if got != CanConfirmManaged(class, c, Managed{}) {
					t.Errorf("CanConfirmManaged(%s, %s, %s) = %v and %v with nothing read: asking for the confirmations back takes the project's marks out of the decision",
						className(class), conditionsName(c), managedName(m), got, CanConfirmManaged(class, c, Managed{}))
				}
				if c.Agent != "" {
					if got {
						t.Errorf("CanConfirmManaged(%s, %s, %s) is true: a coding agent's session answers no confirmation (--ask included)",
							className(class), conditionsName(c), managedName(m))
					}
					continue
				}
				if want := CanConfirm(class, plain); got != want {
					t.Errorf("CanConfirmManaged(%s, %s, %s) = %v, want the class rule's own answer %v: the flag restores a confirmation, it never removes one",
						className(class), conditionsName(c), managedName(m), got, want)
				}
			}
		}
	}
}

// TestAskDecisionIsTheFlagsWholeRule enumerates the decision, by hand: the
// no-op line wherever the marks did not answer "managed" (and a refusal
// nowhere), the refusal wherever a managed project's run cannot prompt, and
// the restored line for the owner who can. The rows are written out rather
// than derived, so a rule that answered a line and a refusal at once, or
// answered a managed project's terminal with a refusal, is a failure here.
func TestAskDecisionIsTheFlagsWholeRule(t *testing.T) {
	for _, c := range allConditions() {
		canPrompt := c.Agent == "" && c.Terminal && !c.NonInteractive && !c.JSON
		for _, managed := range []bool{false, true} {
			line, refusal := AskDecision(c, managed)
			switch {
			case !managed:
				if refusal != "" || line != AskUnmanaged() {
					t.Errorf("AskDecision(%s, managed=false) = %q, %q, want the no-op line and no refusal", conditionsName(c), line, refusal)
				}
			case canPrompt:
				if refusal != "" || line != AskRestored() {
					t.Errorf("AskDecision(%s, managed=true) = %q, %q, want the restored line and no refusal", conditionsName(c), line, refusal)
				}
			default:
				if line != "" || refusal != AskRefusal(c) {
					t.Errorf("AskDecision(%s, managed=true) = %q, %q, want the refusal and no line", conditionsName(c), line, refusal)
				}
				if !strings.Contains(refusal, askNobody(c)) {
					t.Errorf("AskDecision(%s, managed=true) refuses with %q, which does not say why nobody can answer it (%q)", conditionsName(c), refusal, askNobody(c))
				}
			}
		}
	}
	// A run with no terminal at all is the case the refusal exists for, and it
	// must never read as a proceed: the sentence says nothing is applied, and
	// it is the session's own advice (the person's terminal), the way every
	// refusal for a session nobody can ask is.
	for _, c := range []Conditions{{Agent: "CLAUDECODE"}, {NonInteractive: true}, {JSON: true}, {Terminal: false}, {Terminal: true, NonInteractive: true}} {
		_, refusal := AskDecision(c, true)
		if !strings.Contains(refusal, "nothing is applied") {
			t.Errorf("the refusal for %s does not say that nothing is applied: %q", conditionsName(c), refusal)
		}
		if !strings.Contains(refusal, AgentAdvice) {
			t.Errorf("the refusal for %s does not carry the way out that every refusal for a session nobody can ask carries: %q", conditionsName(c), refusal)
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
	for _, c := range []Conditions{{Agent: "CLAUDECODE"}, {Agent: IDEMarker}, {NonInteractive: true}, {JSON: true}, {}} {
		refusal := AskRefusal(c)
		for _, no := range []string{"--ask", "--yes", "unset ", "export ", "="} {
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
	if AskAndYes() == "" || !strings.Contains(AskAndYes(), "--ask") || !strings.Contains(AskAndYes(), "--yes") {
		t.Errorf("AskAndYes does not name the two flags it refuses together: %q", AskAndYes())
	}
}
