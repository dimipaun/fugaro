package runner

// The reader of a legacy record keeps its own copy of two tables the writer
// owns — the halt reason's family (haltClass, halt.go) and the phrases inside
// the agent's own error message (authRefusedPhrases, transportFailedPhrases,
// FC4) — because internal/runview may not import this package: the import
// direction is one way (F21), so the two sides cannot share a function.
//
// D1455 is what holds them together: the copies are two thin switches over one
// table of names, and a test that walks the whole closed set through both. It
// has to sit on this side of the import, which is the side that can read the
// writer's table *and* call the reader.
//
// The class is durable: a report counts runs by it and nothing rewrites it, so
// a reason moved to another family, or a phrase added to one list and not the
// other, is one cause counted under two names — a figure a reader acts on with
// the wrong remedy.

import (
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

// TestHaltClassesAgreeWithTheReadersTable: every halt reason runstore declares
// is read twice — through this package's haltClass, which is what a new record
// is written with, and through runview.ClassOf, which is what a record written
// before the field shipped is read as — and the two answers are the same.
//
// The walk is over the closed set and not over the sixteen reasons that happen
// to be there today, so a reason added to runstore is red here until both
// sides place it. TestHaltReasonsMapToTheirFamily is the same set read against
// the design; this is the same set read against the reader.
func TestHaltClassesAgreeWithTheReadersTable(t *testing.T) {
	reasons := haltReasonConstants(t)
	if len(reasons) == 0 {
		t.Fatal("the walk found no halt reasons in runstore.go: a walk over an empty set agrees with anything")
	}
	for _, reason := range runstore.GovernanceHaltReasons() {
		if !slices.Contains(reasons, reason) {
			t.Errorf("the walk misses runstore's governance reason %q, which is the half of the closed set the reader's copy is most likely to have left behind: a reason the walk does not reach is a family nobody compares", reason)
		}
	}
	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			want := haltClass(reason)
			// The record a legacy halt left: the block the runner attaches at
			// every halt ending and the halt's own sentence as the reason,
			// with no class on it at all.
			h := &runstore.Halt{Reason: reason, Scope: "run", At: time.Time{}}
			rec := &runstore.Record{Status: runstore.StatusHalted, Reason: (&HaltError{*h}).Error(), Halt: h}
			if got := runview.ClassOf(rec); got != want {
				t.Errorf("haltClass(%q) is %q and a legacy record halted for it reads %q: the writer and the reader disagreeing about one reason counts one cause under two names, and the reader's copy of this table is the side that has to move", reason, want, got)
			}
		})
	}
}

// TestStageErrorClassesAgreeWithTheReadersTable is the same walk for FC4's
// classifier: for every phrase in the two lists, the reason a stage writes is
// built by the writer's own function and read by the reader's table, and the
// class the classifier answers with on the same message is the class the
// reader answers with.
//
// It is the sentences that are compared and not the lists, which is the whole
// point of a legacy reading: a phrase dropped from the reader's copy leaves
// the runs that ended this way classed two ways depending on the day they ran,
// and the reason below is the one a record actually carries.
func TestStageErrorClassesAgreeWithTheReadersTable(t *testing.T) {
	r := &run{cfg: &config.Config{}}
	lists := []struct {
		name    string
		phrases []string
	}{
		{"authRefusedPhrases", authRefusedPhrases},
		{"transportFailedPhrases", transportFailedPhrases},
	}
	for _, list := range lists {
		if len(list.phrases) == 0 {
			t.Errorf("%s holds no phrase: a list that matches nothing classifies nothing, and this walk would pass on an empty one", list.name)
			continue
		}
		for _, phrase := range list.phrases {
			t.Run(list.name+": "+phrase, func(t *testing.T) {
				// The result a stage reports when the call failed this way.
				// The subtype is the one Claude Code reports for a refused
				// call, so the reason is the sentence a record carries.
				res := agent.Result{Subtype: "success", Text: "the call failed: " + phrase}
				rec := &runstore.Record{Status: runstore.StatusFailed, Reason: r.stageErrorReason("review", res)}
				want := r.stageFailureClass("", res)
				if got := runview.ClassOf(rec); got != want {
					t.Errorf("a legacy record whose reason is %q reads %q, and the classifier the same message gives is %q: one phrase classed two ways is one cause counted under two names", rec.Reason, got, want)
				}
			})
		}
	}
	// The one stop the reader may not reach through the table at all: the
	// OAuth account's spent allowance. stageErrorReason writes that stop as a
	// sentence of its own with the model's own text after it and no error
	// envelope in front of it, so a reader that scans the whole reason can be
	// steered by that text either way. The two messages below are the two
	// halves of it: the first says "is invalid: ", which a reader scanning the
	// table would count as a configuration refusal — and the second says the
	// envelope's own words and then a refusal phrase, which a reader looking
	// for that envelope anywhere in the reason would count as a refused
	// credential. The runner answers the allowance before its own tables and
	// reads the envelope only as its own opening sentence (stageFailureClass,
	// stageErrorReason), so the reader has to answer it in the same place.
	oauth := &run{cfg: &config.Config{Agent: config.Agent{Auth: "oauth"}}}
	for _, text := range []string{
		`API Error: 400 {"type":"error","error":{"type":"rate_limit_error","message":"your request is invalid: messages.0.content"}}`,
		"Claude AI usage limit reached; the agent reported an error: the provider answered with status 403",
	} {
		res := agent.Result{Subtype: "success", Text: text}
		rec := &runstore.Record{Status: runstore.StatusFailed, Reason: oauth.stageErrorReason("implement", res)}
		want := oauth.stageFailureClass("oauth", res)
		if got := runview.ClassOf(rec); got != want {
			t.Errorf("a legacy record whose reason is %q reads %q, and the classifier the same message gives is %q: the reason carries the model's own text after the runner's sentence, and a class read out of that text is the model choosing what the run is counted under", rec.Reason, got, want)
		}
		if want != failure.StageErrorUnexplained {
			t.Errorf("the classifier answers %q for a spent allowance, want %q: this case reads the writer's answer, so the writer moving would make it agree with a reader that is wrong", want, failure.StageErrorUnexplained)
		}
	}

	// The other direction: a message no phrase names is the class the
	// classifier answers with when nothing matches, so a phrase dropped from
	// both lists does not become a class of its own.
	res := agent.Result{Subtype: "success", Text: "the stage gave up for a reason of its own"}
	rec := &runstore.Record{Status: runstore.StatusFailed, Reason: r.stageErrorReason("review", res)}
	want := r.stageFailureClass("", res)
	if got := runview.ClassOf(rec); got != want {
		t.Errorf("an unmatched message reads %q, want %q: a cause nothing names is counted rather than guessed at", got, want)
	}
	if want != failure.StageErrorUnexplained {
		t.Errorf("the classifier answers %q for a message with no phrase, want %q: this walk's second half reads the writer's answer, so the writer moving would make it agree with a reader that is wrong", want, failure.StageErrorUnexplained)
	}
}
