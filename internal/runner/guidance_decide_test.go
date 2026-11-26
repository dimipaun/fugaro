package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/rigor"
)

// The open-questions half of readiness (docs/rigor-modes/levels-implementation-plan.md
// LV5.2, levels-reconciliation.md LD8, LF11): a run whose own hand-back file
// asks a question it may not settle stops a draft that says so, whatever its
// rounds found.
//
// These cases drive Decide itself for the reason handback_decide_test.go's do:
// the rule is one function of the file, and a run would only put the stage
// machinery between the two. That a run reaching here with a question ends as a
// record carrying needs_guidance is the harness's, and guidance_test.go holds
// it.

// TestDecideOpenQuestionsOutrankTheVerdicts is the agent's own stop above every
// verdict: a file that asked a question ends the run a draft with
// NeedsGuidance, even where the clean test and the shipping senior round below
// would be a yes — a reviewer's `ship` approves the diff, never the call the
// agent declined to make.
//
// Mutation: drop the askedGuidance case in Decide, and every level below is
// ready on a clean round.
func TestDecideOpenQuestionsOutrankTheVerdicts(t *testing.T) {
	question := HandBackSection{Entries: []string{"should the flag default to off?"}}
	for _, level := range []rigor.Rigor{rigor.POC, rigor.Startup, rigor.Pro, rigor.Critical, rigor.Hotfix} {
		t.Run(string(level), func(t *testing.T) {
			records, last := shipped()
			hb, fromAgent := fulfilled(level)
			hb.OpenQuestions = question
			dec := Decide(records, "abc", last, FindingsPolicy{Mode: level, Round: 2}, nil, hb, fromAgent, true)
			if dec.Ready || dec.Kind != KindBlocked || dec.Class != failure.Unclassified {
				t.Fatalf("%s: %+v, want the guidance refusal", level, dec)
			}
			if dec.Reason != ReasonOpenQuestions || !dec.NeedsGuidance {
				t.Errorf("%s: reason %q, needs_guidance %v; want the questions' own reason and the flag set",
					level, dec.Reason, dec.NeedsGuidance)
			}
		})
	}
}

// TestDecideNeedsGuidanceIsTheQuestionsOwnAnswer: the flag is set by the one
// answer that reads the questions and by no other, so the record's
// needs_guidance cannot disagree with the reason beside it. A file whose open
// questions section is empty — the heading the agent wrote and left empty, or
// a level whose row carries none and whose agent asked nothing — is the
// ordinary answer.
//
// Mutation: read len(hb.OpenQuestions.Entries) >= 0, and every case below is a
// draft that needs guidance.
func TestDecideNeedsGuidanceIsTheQuestionsOwnAnswer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		level     rigor.Rigor
		hb        HandBack
		fromAgent bool
		want      bool
	}{
		{"a pro file that asked", rigor.Pro, HandBack{Deferred: HandBackSection{Entries: []string{"the retry loop backs off blindly"}}, OpenQuestions: HandBackSection{Entries: []string{"should the flag default to off?"}}}, true, true},
		{"a pro file that asked nothing", rigor.Pro, HandBack{Deferred: HandBackSection{Entries: []string{"the retry loop backs off blindly"}}}, true, false},
		{"a pro file with an empty questions heading", rigor.Pro, HandBack{Deferred: HandBackSection{Entries: []string{"the retry loop backs off blindly"}}, OpenQuestions: HandBackSection{}}, true, false},
		{"the runner's fallback body", rigor.Pro, HandBack{OpenQuestions: HandBackSection{Entries: []string{"should the flag default to off?"}}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records, last := shipped()
			dec := Decide(records, "abc", last, FindingsPolicy{Mode: tc.level, Round: 2}, nil, tc.hb, tc.fromAgent, true)
			if dec.NeedsGuidance != tc.want {
				t.Errorf("needs_guidance = %v, want %v: %+v", dec.NeedsGuidance, tc.want, dec)
			}
			// A run that asked nothing is the run it was: the questions are the
			// only reading this rule adds, so a file that met its level's
			// promise is still ready. The fallback body is a draft for the
			// file's own absence, which is LV5.1's answer and not this one.
			if !tc.want && tc.fromAgent && (!dec.Ready || dec.Reason != "") {
				t.Errorf("Decide = %v %q, want ready", dec.Ready, dec.Reason)
			}
		})
	}
}
