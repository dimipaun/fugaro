package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

// The hand-back half of readiness (docs/rigor-modes/levels-implementation-plan.md
// LV5.1): a level's promise is what its agent hands back, and a run that did
// not hand it back is not ready however clean its rounds were.
//
// These cases drive Decide itself, because the rule is one function of the
// level, the file's presence and the sections the file carried: a run would
// only put the stage machinery between the two. The two facts about a run that
// the rule needs from one — that a follow-up's file is followup.md, and that a
// review or artifact run never reaches readiness at all — are the harness's,
// and handback_readiness_test.go and the existing review and artifact tests
// hold them.

// decide is Decide for the tests whose subject is another rule: the hand-back
// a level's promise is met by is handed in whole, so what they assert on is the
// reason their own case names and never this one. A test of the hand-back rule
// calls Decide itself.
func decide(records []verify.Record, finalSHA string, last *runstore.ReviewSummary, pol FindingsPolicy, owner *runstore.OwnerDecisions) Decision {
	hb, fromAgent := fulfilled(pol.Mode)
	return Decide(records, finalSHA, last, pol, owner, hb, fromAgent)
}

// fulfilled is the hand-back a level's promise is met by: every section the
// level's row carries, each with one entry, which is what an agent that wrote
// the file would have left. It reads rigor.HandBackOf for the reason the rule
// does — a section list here would be a second answer to one question — and a
// policy naming no level gets no file, since such a run owes nothing either
// way.
func fulfilled(level rigor.Rigor) (HandBack, bool) {
	if !rigor.Known(level) {
		return HandBack{}, false
	}
	row := rigor.HandBackOf(level)
	one := HandBackSection{Entries: []string{"something the agent left"}}
	var hb HandBack
	if row.Deferred {
		hb.Deferred = one
	}
	if row.OpenRisks {
		hb.OpenRisks = one
	}
	if row.Verification {
		hb.Verification = one
	}
	if row.RevertPlan {
		hb.RevertPlan = one
	}
	if row.MorningNote {
		hb.MorningNote = one
	}
	return hb, true
}

// shipped is the run Decide reads one that did everything else right: a clean
// verified test on the commit that ships, and a senior round that shipped.
func shipped() ([]verify.Record, *runstore.ReviewSummary) {
	pass := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true, Passed: true}
	return []verify.Record{pass}, &runstore.ReviewSummary{Round: 2, Tier: runstore.TierSenior, Verdict: "ship"}
}

// TestDecideHandBackFileIsRequired is the file itself, for every level whose
// row carries a section the agent writes: a run that wrote nothing hands back
// nothing, whatever else it did right.
//
// Mutation: drop the !fromAgent refusal in handBackRefusal, and every level
// below is ready with no file at all.
func TestDecideHandBackFileIsRequired(t *testing.T) {
	for _, level := range []rigor.Rigor{rigor.Startup, rigor.Pro, rigor.Critical, rigor.Hotfix} {
		t.Run(string(level), func(t *testing.T) {
			records, last := shipped()
			dec := Decide(records, "abc", last, FindingsPolicy{Mode: level, Round: 2}, nil, HandBack{}, false)
			if dec.Ready {
				t.Fatalf("%s is ready with no hand-back file: %q", level, dec.Reason)
			}
			if dec.Reason != ReasonNoHandBack || dec.Kind != KindBlocked || dec.Class != failure.Unclassified {
				t.Errorf("%s: %+v, want the no-file refusal", level, dec)
			}
		})
	}
}

// TestDecideHandBackSectionsThePromiseStandsOn is the entries under the
// sections the level's promise is: a file that names the heading and leaves it
// empty is a promise the agent did not keep, while the lists beside them may be
// empty — nothing deferred and nothing left open are honest answers.
//
// Mutation: drop the required-section loop, and the three Critical and Hotfix
// cases below are ready on a heading with nothing under it.
func TestDecideHandBackSectionsThePromiseStandsOn(t *testing.T) {
	one := HandBackSection{Entries: []string{"an entry the agent left"}}
	// A heading the agent wrote and left empty parses as a section with no
	// entries, which is the shape this rule refuses.
	empty := HandBackSection{}
	for _, tc := range []struct {
		name  string
		level rigor.Rigor
		hb    HandBack
		want  string
	}{
		{"a startup file with no deferred list", rigor.Startup, HandBack{}, ""},
		{"a pro file with no deferred list", rigor.Pro, HandBack{}, ""},
		{"critical without its proof", rigor.Critical, HandBack{OpenRisks: one}, "the hand-back's Verification section is empty"},
		{"critical with an empty proof", rigor.Critical, HandBack{Verification: empty, OpenRisks: one}, "the hand-back's Verification section is empty"},
		{"critical with its proof and no open risks", rigor.Critical, HandBack{Verification: one}, ""},
		{"hotfix without its revert plan", rigor.Hotfix, HandBack{MorningNote: one}, "the hand-back's Revert plan section is empty"},
		{"hotfix without its morning note", rigor.Hotfix, HandBack{RevertPlan: one}, "the hand-back's Morning note section is empty"},
		{"hotfix with both", rigor.Hotfix, HandBack{RevertPlan: one, MorningNote: one}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records, last := shipped()
			dec := Decide(records, "abc", last, FindingsPolicy{Mode: tc.level, Round: 2}, nil, tc.hb, true)
			if dec.Ready != (tc.want == "") || dec.Reason != tc.want {
				t.Errorf("Decide = %v %q, want ready %v %q", dec.Ready, dec.Reason, tc.want == "", tc.want)
			}
		})
	}
}

// TestDecideHandBackFallbackBodyIsNotTheFile: the runner's fallback body is
// built from the task text, so a brief that spells a level's headings is not
// the agent's account of anything (reconciliation §3b item 13). A hand-back
// that would satisfy every level's promise is refused when it did not come from
// a file of the agent's own.
//
// Mutation: read the sections out of the fallback body — or pass fromAgent as
// true for it — and this run is ready on a task that happens to carry the
// headings.
func TestDecideHandBackFallbackBodyIsNotTheFile(t *testing.T) {
	for _, level := range []rigor.Rigor{rigor.Startup, rigor.Pro, rigor.Critical, rigor.Hotfix} {
		t.Run(string(level), func(t *testing.T) {
			hb, ok := fulfilled(level)
			if !ok {
				t.Fatalf("%s has no hand-back to refuse", level)
			}
			records, last := shipped()
			dec := Decide(records, "abc", last, FindingsPolicy{Mode: level, Round: 2}, nil, hb, false)
			if dec.Ready || dec.Reason != ReasonNoHandBack {
				t.Errorf("Decide = %v %q, want %q: no file of the agent's own is behind this run", dec.Ready, dec.Reason, ReasonNoHandBack)
			}
		})
	}
}

// TestDecideHandBackPocOwesNoFile: POC's row carries the one line the runner
// writes itself, so its run owes no file and a POC run that wrote none is ready
// (LD5). The rule reads the row rather than naming the level, which is what the
// mutation proves.
//
// Mutation: make handBackRefusal owe the file for every known level, and this
// run is a draft.
func TestDecideHandBackPocOwesNoFile(t *testing.T) {
	records, last := shipped()
	if dec := Decide(records, "abc", last, FindingsPolicy{Mode: rigor.POC, Round: 2}, nil, HandBack{}, false); !dec.Ready {
		t.Errorf("POC is not ready without a hand-back file: %q", dec.Reason)
	}
}

// TestDecideHandBackLevelThatNeverResolved: a policy naming no level is a run
// built by hand rather than one whose resolver failed — D19 resolves the
// default for a real run — and readiness is not the place to invent one, so the
// rule answers nothing for it. Every test in this package that builds its own
// policy reads readiness through this gap (decide).
func TestDecideHandBackLevelThatNeverResolved(t *testing.T) {
	records, last := shipped()
	if dec := Decide(records, "abc", last, FindingsPolicy{}, nil, HandBack{}, false); !dec.Ready {
		t.Errorf("a run whose level never resolved is not ready: %q", dec.Reason)
	}
}

// TestDecideHandBackGatesOutrankIt: the check is asked at the answers that
// would otherwise be yes, so a run refused for a cause of its own still reports
// that cause rather than this one — the file is what is left when nothing else
// is wrong.
func TestDecideHandBackGatesOutrankIt(t *testing.T) {
	failing := verify.Record{Kind: verify.KindTest, HeadSHA: "abc", CleanTree: true}
	changes := &runstore.ReviewSummary{Round: 2, Tier: runstore.TierSenior, Verdict: "changes", Findings: 1}
	dec := Decide([]verify.Record{failing}, "abc", changes, FindingsPolicy{Mode: rigor.Hotfix, Round: 2}, nil, HandBack{}, false)
	if dec.Ready || dec.Reason != "tests failing on the final commit" {
		t.Errorf("Decide = %v %q, want the failing suite's own reason", dec.Ready, dec.Reason)
	}
	records, _ := shipped()
	dec = Decide(records, "abc", changes, FindingsPolicy{Mode: rigor.Hotfix, Round: 2}, nil, HandBack{}, false)
	if dec.Ready || dec.Reason != "review round 2 still has 1 finding" {
		t.Errorf("Decide = %v %q, want the round's own reason", dec.Ready, dec.Reason)
	}
}
