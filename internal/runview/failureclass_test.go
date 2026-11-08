package runview

// A record written before failure_class shipped carries no class, and the
// report the field exists for is about the last seven days — so the reader
// classes an old record from its reason, with one fixed table, and never
// writes the answer back (docs/design/failure-classes.md §5).
//
// What these tests hold is the two halves of that sentence: the table reads
// the reasons the runner already writes the way the design's §5 lists them,
// and the reading is a reading — the record's bytes are the same before and
// after, and the answer is published on the row.

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// legacyHalts is the halt the halted case carries, and the one reason of the
// closed set it names: the walk over all of them is
// internal/runner's TestHaltClassesAgreeWithTheReadersTable, which can read
// both the writer's switch and this reader.
var legacyHalts = &runstore.Halt{Reason: runstore.HaltTokenCap, Scope: "run", At: now, Detail: "run used 110 tokens of 100"}

// TestLegacyClassFromReasonTable: an old record's reason maps as the design's
// §5 table says, cause by cause. Every reason below is one the runner writes
// today — the sentences are the runner's own, quoted from the sites that
// write them — and the want is the class the writer that writes that sentence
// chooses, so the reader and the writer cannot call one cause by two names
// (F34's rule, which the plan makes this task's).
//
// Three of the cases are reasons the design's §5 list does not carry and the
// plan adds: endUnchanged's own three shapes (followup.go, runner.go), which
// the writer classes branch_moved — a record written before the field shipped
// and one written after must read the same class for the same fact.
func TestLegacyClassFromReasonTable(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		halt   *runstore.Halt
		want   failure.Class
	}{
		{"no test ran on the final commit", "no verified test run on the final commit", nil, failure.UnverifiedFinalCommit},
		{"work was committed after the last verified test run", "uncommitted changes were committed at finalize, after the last verified test run", nil, failure.UnverifiedFinalCommit},
		{"the final commit's tests failed", "tests failing on the final commit", nil, failure.TestsFailed},
		{"the last review round still had findings", "review round 2 still has 3 findings", nil, failure.ReviewNotConverged},
		{"the mode was raised at finalize", "the mode was raised to pro at finalize, after the last review round", nil, failure.ReviewNotConverged},
		{"an outer level held the run back", "review deep last shipped nothing: round 2 left changes", nil, failure.ReviewNotConverged},
		{"no review was reached at all", "no review verdict", nil, failure.ReviewUnread},
		{"a review round answered nothing", "review round 3 returned no parseable verdict after a retry", nil, failure.ReviewUnread},
		{"the agent made no commits", "the agent made no commits", nil, failure.NoCommits},
		{"the provider refused the credential", "stage implement: the agent reported an error (error_during_execution): the provider answered with status 403", nil, failure.ProviderAuthRefused},
		{"the provider's own authentication error", `stage implement: the agent reported an error: {"type":"error","error":{"type":"authentication_error","message":"invalid api key"}}`, nil, failure.ProviderAuthRefused},
		{"a stream closed before its response finished", "stage review: the agent reported an error: stream closed before completion", nil, failure.TransportFailed},
		{"an error with no phrase the table knows", "stage fix: the agent reported an error (error_max_turns): I could not finish", nil, failure.StageErrorUnexplained},
		{"the OAuth account's allowance was spent", "stage implement: the OAuth account's subscription usage limit was reached, so the run stops here: the account's allowance is shared with the owner's own Claude Code use", nil, failure.StageErrorUnexplained},
		{"a maximum above the model's own", "fugaro.yaml is invalid: Configured maximum above the model's own: max_tokens 64000 for model claude-sonnet-5 is above the model's maximum 32000", nil, failure.ConfigInvalid},
		{"the pull request closed with nothing pushed", "PR #12 was closed during the run; nothing was pushed", nil, failure.BranchMoved},
		{"the branch was deleted under the run", "acme/app#feat no longer exists on origin; nothing was pushed, and the branch was not recreated", nil, failure.BranchMoved},
		{"someone pushed to the branch first", "someone pushed to acme/app#feat during the run; nothing was overwritten", nil, failure.BranchMoved},
		{"the pull request closed during finalize", "PR #12 was closed during finalize; the branch was pushed", nil, failure.BranchMoved},
		{"the pull request closed after the push", "PR #12 was merged during the run; the branch was pushed", nil, failure.BranchMoved},
		{"the host refused the push", "GitHub refused the push: a repository rule rejected the push (see the repository's rulesets) (the work is saved on the branch)", nil, failure.BranchMoved},
		{"a halt's own block", "halted: token_cap: run used 110 tokens of 100", legacyHalts, failure.HaltedBudget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status := runstore.StatusFailed
			if tc.halt != nil {
				status = runstore.StatusHalted
			}
			rec := &runstore.Record{Status: status, Reason: tc.reason, Halt: tc.halt}
			if got := ClassOf(rec); got != tc.want {
				t.Errorf("ClassOf(%q) is %q, want %q: the class is what the report counts the run by, and a legacy record reading a class the writer of the same sentence does not choose splits one cause across two rows", tc.reason, got, tc.want)
			}
		})
	}
}

// TestLegacyUnmatchedReasonIsUnclassified: a reason the table does not know is
// counted rather than guessed at, and never read off the status. The three
// shapes below are the sites that write no class of their own — a stage
// failure's own sentence, a review run whose stage failed, and a record with
// no reason at all — plus the one a later build's value is: a class this build
// does not know reads as unclassified, which is what lets the vocabulary grow
// without a record becoming unreadable.
func TestLegacyUnmatchedReasonIsUnclassified(t *testing.T) {
	cases := []struct {
		name string
		rec  *runstore.Record
	}{
		{"a stage failure's own sentence", &runstore.Record{Status: runstore.StatusFailed, Reason: "stage implement: the stage hook left the halt reason \"nope\", which is not a governance stop this runner can record"}},
		{"a review run whose stage failed", &runstore.Record{Status: runstore.StatusFailed, Reason: "the review stage ended without a verdict"}},
		{"a record with no reason at all", &runstore.Record{Status: runstore.StatusFailed}},
		{"a class a later build wrote", &runstore.Record{Status: runstore.StatusFailed, Reason: "no review verdict", FailureClass: failure.Class("a_class_from_later")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassOf(tc.rec); got != failure.Unclassified {
				t.Errorf("ClassOf(%q, holding %q) is %q, want %q: an unmatched reason is never a guess and never a class read off the status, and a value this build does not know is the same answer", tc.rec.Reason, tc.rec.FailureClass, got, failure.Unclassified)
			}
		})
	}
}

// TestLegacyClassIsNeverWrittenBack: reading a record does not change it. The
// bytes the store handed over are the bytes the next reader gets, which is
// what makes the reading a reading — a derived class written back would be a
// second writer of a field whose whole value is that one function wrote it,
// and it would survive only as long as the retention window.
func TestLegacyClassIsNeverWrittenBack(t *testing.T) {
	rec := &runstore.Record{Status: runstore.StatusFailed, Stage: "verify", Reason: "tests failing on the final commit", CostUSD: 2}
	before, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if got := ClassOf(rec); got != failure.TestsFailed {
		t.Fatalf("ClassOf is %q, want %q", got, failure.TestsFailed)
	}
	row := Join(Input{Task: spec, Launch: launch, Record: rec, Exec: exec(backend.StateSucceeded)}, prices, now)
	if row.FailureClass != failure.TestsFailed {
		t.Fatalf("the row's class is %q, want %q: the reading is published on the row, which is the only place it belongs", row.FailureClass, failure.TestsFailed)
	}
	after, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the record changed under a read:\nbefore %s\nafter  %s", before, after)
	}
	rowJSON, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rowJSON, []byte(`"failure_class":"tests_failed"`)) {
		t.Errorf("the row does not carry the class: %s", rowJSON)
	}
}

// TestRowCarriesTheClassTheRecordHolds: a row's class is the record's own
// where the record has one — a new record's field, over whatever its reason
// would have read as — and the row of a run that did not fail carries none at
// all: a succeeded run, a run still going and a run that never started have
// no cause to name, and a class on them would be a classification of a
// success (D1464).
func TestRowCarriesTheClassTheRecordHolds(t *testing.T) {
	published := &runstore.Record{Status: runstore.StatusFailed, Reason: "no review verdict", FailureClass: failure.TestsFailed}
	if row := Join(Input{Task: spec, Launch: launch, Record: published, Exec: exec(backend.StateSucceeded)}, prices, now); row.FailureClass != failure.TestsFailed {
		t.Errorf("a record carrying a class reads %q, want the field: %q", row.FailureClass, failure.TestsFailed)
	}
	// A record whose status is a success carries no class whatever the ground
	// under it is: the field is written with the status, and a reading off a
	// status the run did not fail under would name a cause the run does not
	// have.
	done := &runstore.Record{Status: runstore.StatusSucceeded, Reason: "no review verdict"}
	if row := Join(Input{Task: spec, Launch: launch, Record: done, Exec: exec(backend.StateSucceeded)}, prices, now); row.FailureClass != "" {
		t.Errorf("a succeeded row carries the class %q: there is no failure to name", row.FailureClass)
	}
	running := &runstore.Record{Status: runstore.StatusRunning, Reason: "review round 1 still has 2 findings"}
	if row := Join(Input{Task: spec, Launch: launch, Record: running, Exec: exec(backend.StateRunning)}, prices, now); row.FailureClass != "" {
		t.Errorf("a running row carries the class %q: the run has not ended, and a class on it would describe a stopping that has not happened", row.FailureClass)
	}
	if row := Join(Input{Task: spec}, prices, now); row.FailureClass != "" {
		t.Errorf("an unlaunched row carries the class %q", row.FailureClass)
	}
	// A row the read side ends itself carries none either, and this is the case
	// a reader would otherwise meet as a contradiction: the row says `failed`
	// (or `infra_error`, or `cancelled`) with no class on it. Nothing the run
	// wrote recorded an ending at all — a preemption, a lost execution and a
	// cancel before launch all leave a record that never settled — so no object
	// ever named a cause, and the read side's own account of what happened is
	// the Derived field rather than a class it would have to invent.
	for _, tc := range []struct {
		name string
		in   Input
	}{
		{"a preemption", Input{Task: spec, Launch: launch, Exec: preemptedExec()}},
		{"a lost execution", Input{Task: spec, Launch: &runstore.Launch{Execution: "exec-1", LaunchedAt: now.Add(-runstore.ClaimTTL - time.Hour)}}},
		{"a cancel before launch", Input{Task: spec, CancelMarker: true}},
	} {
		row := Join(tc.in, prices, now)
		switch row.Status {
		case string(runstore.StatusFailed), string(runstore.StatusInfraError), string(runstore.StatusCancelled):
		default:
			t.Fatalf("%s: the row reads %q, which is not one of the failure endings this case is about", tc.name, row.Status)
		}
		if row.FailureClass != "" {
			t.Errorf("%s: the row reads %q and carries the class %q, want none: the record under it never settled, so nothing named a cause", tc.name, row.Status, row.FailureClass)
		}
	}
}
