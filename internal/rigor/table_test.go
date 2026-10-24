package rigor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The threshold table and the label (docs/rigor-modes/implementation-plan.md
// T2.3). Every case below cites the sentence it holds: spec.md's findings
// policy per mode and its severity override, D1's 1–5 scale, D2's answer that
// the table reads the three axes and the class while the five words stay
// labels, and D4's fail-closed rule. The names carry Table, Label or Threshold
// so the task's own command — `-run 'Table|Label|Threshold'` — is the whole
// file and not the part of it a name happened to match.

// everyScore is every finding the table can be asked about, plus the shapes
// that are not a score at all: a sweep of the axes with 0 and 6 included, so
// an out-of-range value is exercised beside an absent one, and the zero class
// beside the six the vocabulary names. 2401 cells are small enough to sweep
// rather than sample, which is what makes the properties below statements
// about the whole table rather than about chosen cases.
func everyScore() []Score {
	classes := append(slices.Clone(Classes), "")
	var out []Score
	for l := 0; l <= 6; l++ {
		for i := 0; i <= 6; i++ {
			for c := 0; c <= 6; c++ {
				for _, class := range classes {
					out = append(out, Score{Likelihood: l, Impact: i, FixCost: c, Class: class})
				}
			}
		}
	}
	return out
}

// TestThresholdsAreTheSpecsTwoExamples: D1's reason for a 1–5 scale is that
// three values cannot separate the spec's own pair, so that pair is the first
// thing the table is asked, in the mode the spec names for it.
func TestThresholdsAreTheSpecsTwoExamples(t *testing.T) {
	// "rare but catastrophic findings (data loss, security) block even in
	// Startup mode" (spec.md, Finding scoring). Rare, catastrophic and
	// expensive all at once, so nothing but the override can block it.
	rareButCatastrophic := Score{Likelihood: 1, Impact: 5, FixCost: 5, Class: DataLoss}
	if !Blocks(Startup, rareButCatastrophic) {
		t.Errorf("Startup tolerates a rare but catastrophic finding: %+v", rareButCatastrophic)
	}
	// The other half of D1's pair. Startup "fixes common" findings, so this
	// case is about the axes and not about the likelihood alone: what the mode
	// spends its rounds on is what is both likely and material, and this one is
	// the likelihood without the materiality.
	commonButCosmetic := Score{Likelihood: 5, Impact: 1, FixCost: 1, Class: Cosmetic}
	if Blocks(Startup, commonButCosmetic) {
		t.Errorf("Startup blocks a common but cosmetic finding: %+v", commonButCosmetic)
	}
	// The same likelihood with something behind it is what the mode does
	// block, so the case above is the impact and not "Startup blocks nothing".
	if !Blocks(Startup, Score{Likelihood: 5, Impact: 3, FixCost: 1, Class: Correctness}) {
		t.Error("Startup tolerates a likely correctness finding")
	}
}

// TestTableBlocksTheSecurityOverrideInEveryMode: the spec's severity override,
// asked of every mode rather than of one. The finding is the lowest risk the
// scale can express with the most expensive fix, so no mode's own threshold
// would block it and only the override can — which is what makes each case
// fail if the override is dropped rather than passing on a floor.
func TestTableBlocksTheSecurityOverrideInEveryMode(t *testing.T) {
	for _, m := range Modes {
		for _, class := range []Class{Security, DataLoss} {
			s := Score{Likelihood: 1, Impact: 1, FixCost: 5, Class: class}
			if !Blocks(m, s) {
				t.Errorf("%s tolerates a %s finding at the lowest risk: the override is not applied in every mode", m, class)
			}
		}
	}
	// And it is the class that carries it rather than the axes: the same score
	// in a class the spec does not name as catastrophic is what POC ignores.
	if Blocks(POC, Score{Likelihood: 1, Impact: 1, FixCost: 5, Class: Cosmetic}) {
		t.Error("POC blocks a cosmetic finding at the lowest risk")
	}
}

// TestTableBlocksAnUnscoredFindingInEveryMode is D4's rule, asserted mode by
// mode and shape by shape: a reviewer that stops emitting scores must not be
// able to turn a mode down, so the table's answer for a finding it cannot read
// is the strictest one everywhere.
func TestTableBlocksAnUnscoredFindingInEveryMode(t *testing.T) {
	cases := []struct {
		what string
		s    Score
	}{
		{"no score at all, the shape a verdict had before the score existed", Score{}},
		{"no fix cost", Score{Likelihood: 1, Impact: 1, Class: Cosmetic}},
		{"no class", Score{Likelihood: 1, Impact: 1, FixCost: 1}},
		{"an axis below the scale", Score{Likelihood: 0, Impact: 1, FixCost: 1, Class: Cosmetic}},
		{"an axis above the scale", Score{Likelihood: 6, Impact: 1, FixCost: 1, Class: Cosmetic}},
		{"a class outside the vocabulary", Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: "style"}},
	}
	for _, m := range Modes {
		for _, c := range cases {
			if !Blocks(m, c.s) {
				t.Errorf("%s tolerates a finding with %s: an unscored finding blocks in every mode (D4)", m, c.what)
			}
		}
	}
}

// TestTableCriticalBlocksEveryFinding: "Critical — Every finding blocks"
// (spec.md), with no exception for a finding the other modes tolerate.
func TestTableCriticalBlocksEveryFinding(t *testing.T) {
	for _, s := range []Score{
		{Likelihood: 1, Impact: 1, FixCost: 5, Class: Cosmetic},
		{Likelihood: 1, Impact: 1, FixCost: 1, Class: Maintainability},
		{},
	} {
		if !Blocks(Critical, s) {
			t.Errorf("Critical tolerates %+v", s)
		}
	}
}

// TestTablePOCIgnoresEverythingButTheCatastrophic: "POC — Ignore (except
// catastrophic)". The finding below is the most severe score the scale can
// express that the override does not already cover, and POC still ignores it,
// which is what "damn the torpedoes" costs.
func TestTablePOCIgnoresEverythingButTheCatastrophic(t *testing.T) {
	severe := Score{Likelihood: 5, Impact: 4, FixCost: 5, Class: Correctness}
	if Blocks(POC, severe) {
		t.Errorf("POC blocks %+v; POC ignores everything but the catastrophic", severe)
	}
}

// TestTableStartupLogsTheRareCornerCases: "Startup — Fix common; log
// rare/expensive to backlog" and "Rare, expensive corner cases go to the
// backlog". Both halves are one floor: what is rare does not reach it, and what
// is common and material does.
func TestTableStartupLogsTheRareCornerCases(t *testing.T) {
	for _, s := range []Score{
		{Likelihood: 1, Impact: 4, FixCost: 5, Class: Correctness},
		{Likelihood: 2, Impact: 2, FixCost: 1, Class: Performance},
	} {
		if Blocks(Startup, s) {
			t.Errorf("Startup blocks %+v; a rare corner case is what it logs", s)
		}
	}
	for _, s := range []Score{
		{Likelihood: 5, Impact: 3, FixCost: 1, Class: Correctness},
		{Likelihood: 4, Impact: 2, FixCost: 1, Class: Performance},
	} {
		if !Blocks(Startup, s) {
			t.Errorf("Startup tolerates %+v; it fixes what is common and material", s)
		}
	}
}

// TestTableProWeighsTheFixCost: "Pro — Weigh fix cost vs. likelihood × severity
// … extremely rare, high-effort issues may stay unfixed". The one finding Pro
// tolerates that its own floor would block is that sentence's own case, and the
// same finding with a cheap fix is what it does block.
func TestTableProWeighsTheFixCost(t *testing.T) {
	rareExpensive := Score{Likelihood: 1, Impact: 4, FixCost: 5, Class: Correctness}
	if Blocks(Pro, rareExpensive) {
		t.Errorf("Pro blocks %+v; an extremely rare, high-effort issue may stay unfixed", rareExpensive)
	}
	if !Blocks(Pro, Score{Likelihood: 1, Impact: 4, FixCost: 1, Class: Correctness}) {
		t.Error("Pro tolerates a rare finding whose fix is cheap; the cost is what the escape weighs")
	}
	// Not extremely rare, so the cost does not excuse it.
	if !Blocks(Pro, Score{Likelihood: 2, Impact: 2, FixCost: 5, Class: Correctness}) {
		t.Error("Pro tolerates a finding that is not extremely rare because its fix is expensive")
	}
	// Startup, the weaker mode, tolerates what Pro's escape tolerates, so the
	// escape never makes Pro stricter than the mode below it.
	if Blocks(Startup, rareExpensive) {
		t.Error("Startup blocks what Pro tolerates")
	}
}

// TestTheTableTightensWithTheAxis: the modes are one axis (mode.go's Rank), and
// the table has to move the same way — a stricter mode may tolerate nothing a
// weaker one blocks. This is §7's fourth review question in the direction that
// matters: a mode may require more, and never permit more.
func TestTheTableTightensWithTheAxis(t *testing.T) {
	axis := Modes[:len(Modes)-1] // the four on-axis modes, weakest first
	for i := 1; i < len(axis); i++ {
		weaker, stronger := axis[i-1], axis[i]
		for _, s := range everyScore() {
			if Blocks(weaker, s) && !Blocks(stronger, s) {
				t.Fatalf("%s blocks %+v and the stricter %s tolerates it", weaker, s, stronger)
			}
		}
	}
}

// TestTablePOCAndHotfixTolerateTheSameFindings states as intent what two
// identical rows would otherwise make an accident. The spec gives POC "Ignore
// (except catastrophic)" and Hotfix "Only findings that make the fix wrong or
// unsafe block"; read with the spec's own parenthetical — data loss and
// security — and an impact of 5 for the crash the severity axis names, the two
// sentences name one set. What separates the modes is everything else the spec
// says about them: the loop, the diff gate, what they file and what they may
// spend, and mode.go's Rank is about which mode wins a resolution rather than
// about the size of this set.
func TestTablePOCAndHotfixTolerateTheSameFindings(t *testing.T) {
	for _, s := range everyScore() {
		if Blocks(POC, s) != Blocks(Hotfix, s) {
			t.Fatalf("POC and Hotfix disagree about %+v", s)
		}
	}
}

// TestTableIsTotalOverTheVocabularyAndBlocksWithoutARow: the table is total
// over the vocabulary — every mode's policy is data here or it is nowhere —
// and a mode it does not hold blocks on every finding. The zero Mode is what a
// layer that sets nothing carries and what a record written before the mode
// existed has, and a name outside the vocabulary is what config refuses at
// load; D4's rule read at the table is that a value it cannot place is never
// the lenient answer.
func TestTableIsTotalOverTheVocabularyAndBlocksWithoutARow(t *testing.T) {
	for _, m := range Modes {
		if _, ok := thresholds[m]; !ok {
			t.Errorf("%q has no row in the table", m)
		}
	}
	tolerable := Score{Likelihood: 1, Impact: 1, FixCost: 5, Class: Cosmetic}
	for _, m := range []Mode{"", "bogus"} {
		if !Blocks(m, tolerable) {
			t.Errorf("Blocks(%q, …) is false; a mode the table does not hold blocks", m)
		}
	}
}

// TestTableSplitIsTheVerdictsFindingsInTwo: the runner prints the findings a
// mode left open in the order the reviewer wrote them, so the split keeps each
// group's order as well as its membership, and a verdict with nothing in it
// answers with nothing rather than with two empty slices a caller has to check
// for length.
func TestTableSplitIsTheVerdictsFindingsInTwo(t *testing.T) {
	findings := []Score{
		{Likelihood: 1, Impact: 1, FixCost: 5, Class: Cosmetic},    // tolerated at Pro
		{Likelihood: 4, Impact: 3, FixCost: 1, Class: Security},    // the override
		{Likelihood: 1, Impact: 4, FixCost: 5, Class: Correctness}, // Pro's escape
		{Likelihood: 5, Impact: 3, FixCost: 1, Class: Correctness}, // above Pro's floor
	}
	blocking, tolerated := Split(Pro, findings)
	if want := []Score{findings[1], findings[3]}; !slices.Equal(blocking, want) {
		t.Errorf("blocking = %+v; want %+v", blocking, want)
	}
	if want := []Score{findings[0], findings[2]}; !slices.Equal(tolerated, want) {
		t.Errorf("tolerated = %+v; want %+v", tolerated, want)
	}
	if b, tol := Split(Pro, nil); b != nil || tol != nil {
		t.Errorf("Split of no findings = %+v, %+v; want nothing in each", b, tol)
	}
	// And the split is Blocks asked once per finding rather than a second
	// rule: a finding the split tolerates is one Blocks says the mode does not
	// block, and no finding is lost on the way.
	for _, s := range everyScore() {
		b, tol := Split(Startup, []Score{s})
		if got, want := len(b) == 1, Blocks(Startup, s); got != want {
			t.Fatalf("Split(Startup, %+v) blocked=%v and Blocks=%v", s, got, want)
		}
		if len(b)+len(tol) != 1 {
			t.Fatalf("Split(Startup, %+v) lost the finding", s)
		}
	}
}

// TestThresholdClassesAreTheSchemasEnum: the vocabulary in this package is a
// second copy of runner.VerdictSchema's enum, which is the schema that
// constrains the model (D3). A class the schema gains and this list does not is
// a finding the table cannot read — unscored, so it blocks in every mode — and
// a class this list gains and the schema does not is a word no reviewer will
// ever emit. The guard reads the schema's own text rather than a third copy of
// the six words, because two copies that agree today are exactly the pair that
// drifts.
func TestThresholdClassesAreTheSchemasEnum(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "runner", "verdict.go"))
	if err != nil {
		t.Fatalf("reading runner.VerdictSchema's source: %v", err)
	}
	const prefix = `"class":{"type":"string","enum":[`
	i := strings.Index(string(src), prefix)
	if i < 0 {
		t.Fatalf("runner.VerdictSchema states no %s", prefix)
	}
	rest := string(src)[i+len(prefix):]
	j := strings.Index(rest, "]")
	if j < 0 {
		t.Fatal("runner.VerdictSchema's class enum is not closed")
	}
	var got []Class
	for _, w := range strings.Split(rest[:j], ",") {
		got = append(got, Class(strings.Trim(w, `"`)))
	}
	if !slices.Equal(got, Classes) {
		t.Errorf("runner.VerdictSchema's classes are %v and rigor's are %v; the two lists are one vocabulary", got, Classes)
	}
}

// TestLabelIsDerivedFromTheAxes: D2's answer is that the five words are a label
// derived from the score rather than an input to it, so every case here is a
// score and the word a person should read for it. The cases walk the bands from
// both ends and include the most severe score a class the spec does not call
// catastrophic can carry, which is what pins that the top of the scale is a
// band of its own rather than a second way to say blocker.
func TestLabelIsDerivedFromTheAxes(t *testing.T) {
	cases := []struct {
		what string
		s    Score
		want Label
	}{
		{"a rare but catastrophic finding", Score{Likelihood: 1, Impact: 5, FixCost: 5, Class: DataLoss}, Blocker},
		{"a security finding at the lowest risk", Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: Security}, Blocker},
		{"the worst score the scale can express", Score{Likelihood: 5, Impact: 5, FixCost: 5, Class: Correctness}, Blocker},
		{"a finding that is likely and material", Score{Likelihood: 4, Impact: 3, FixCost: 1, Class: Correctness}, High},
		{"a finding at the top of the high band", Score{Likelihood: 3, Impact: 4, FixCost: 1, Class: Correctness}, High},
		{"the most severe finding a class the spec does not call catastrophic can carry", Score{Likelihood: 5, Impact: 4, FixCost: 1, Class: Cosmetic}, High},
		{"a finding in the middle", Score{Likelihood: 3, Impact: 3, FixCost: 1, Class: Performance}, Medium},
		{"a finding at the top of the medium band", Score{Likelihood: 2, Impact: 4, FixCost: 1, Class: Performance}, Medium},
		{"a finding whose fix is cheap", Score{Likelihood: 2, Impact: 2, FixCost: 1, Class: Correctness}, Low},
		{"a finding at the top of the low band", Score{Likelihood: 2, Impact: 3, FixCost: 1, Class: Correctness}, Low},
		{"a finding nobody would notice", Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: Cosmetic}, Nit},
		{"the most a nit can be", Score{Likelihood: 3, Impact: 1, FixCost: 1, Class: Maintainability}, Nit},
		// Fail closed, like the table: a finding the score cannot be read for
		// is the word no mode tolerates, not the mildest one.
		{"a finding with no score at all", Score{}, Blocker},
		{"a finding with a class outside the vocabulary", Score{Likelihood: 1, Impact: 1, FixCost: 1, Class: "style"}, Blocker},
	}
	for _, c := range cases {
		if got := c.s.Label(); got != c.want {
			t.Errorf("%s is labelled %q; want %q (%+v)", c.what, got, c.want, c.s)
		}
	}
}

// TestLabelsAreTheFiveWordsD2Names: D2's answer names the vocabulary —
// blocker, high, medium, low, nit — and closes it, the way the modes are
// closed, so a reader of a pull request body meets one set of words.
func TestLabelsAreTheFiveWordsD2Names(t *testing.T) {
	want := []Label{Blocker, High, Medium, Low, Nit}
	if !slices.Equal(Labels, want) {
		t.Fatalf("Labels = %v; want %v", Labels, want)
	}
	for _, l := range want {
		if !KnownLabel(l) {
			t.Errorf("KnownLabel(%q) is false", l)
		}
	}
	for _, l := range []Label{"", "critical", "Blocker", "info"} {
		if KnownLabel(l) {
			t.Errorf("KnownLabel(%q) is true", l)
		}
	}
}

// TestTheLabelAndTheTableAgree: the label is what a person reads in the pull
// request body and in `fugaro diagnose` (D2), so the word and the decision have
// to be the same statement in both directions — a finding a mode blocks on is
// one the label already calls serious, and a finding the label calls serious
// enough for that mode is one it blocks on. A one-way check would let the bands
// drift away from the floors and still read as a green test, which is the drift
// this test exists to catch.
func TestTheLabelAndTheTableAgree(t *testing.T) {
	// Each mode's set is the bands its floor reaches: POC and Hotfix block only
	// what no mode tolerates, Startup starts at the medium band, Pro at the low
	// one, and Critical at every finding.
	allowed := map[Mode][]Label{
		POC:      {Blocker},
		Hotfix:   {Blocker},
		Startup:  {Blocker, High, Medium},
		Pro:      {Blocker, High, Medium, Low},
		Critical: Labels,
	}
	for _, m := range Modes {
		for _, s := range everyScore() {
			if Blocks(m, s) && !slices.Contains(allowed[m], s.Label()) {
				t.Fatalf("%s blocks a finding it labels %q: %+v", m, s.Label(), s)
			}
		}
	}
	for _, m := range []Mode{POC, Hotfix, Startup, Critical} {
		for _, s := range everyScore() {
			if got, want := Blocks(m, s), slices.Contains(allowed[m], s.Label()); got != want {
				t.Fatalf("%s blocks %+v = %v and labels it %q", m, s, got, s.Label())
			}
		}
	}
	// Pro's floor is the label's own low band, and the one finding its band
	// reaches that it leaves open is the escape TestTableProWeighsTheFixCost
	// holds on its own.
	for _, s := range everyScore() {
		// The override is asked before the row, so a catastrophic finding is
		// never the escape's even when its axes and its cost are.
		escaped := s.Scored() && !s.catastrophic() && s.Likelihood == 1 && s.Impact == 4 && s.FixCost >= 4
		if got, want := Blocks(Pro, s), slices.Contains(allowed[Pro], s.Label()) && !escaped; got != want {
			t.Fatalf("Pro blocks %+v = %v and labels it %q", s, got, s.Label())
		}
	}
}
