package runstore

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMergeJudgeBlockIsBoundedAndNeverNull: what a judge run's block may hold
// once it is stored, and the two promises the published shapes make about it —
// schemas/result.schema.json's "a reason of at most 200 runes, a blocker of at
// most 500 and at most 32 follow-ups" and its follow_ups "Never null, so a
// reader can range over it" (E6).
//
// The rules below are the ones the runner's own reader cannot reach:
// DecodeMergeJudgeVerdict refuses a third word, an empty reason, a block with
// nothing to name and a follow-up outside the shapes before NewMergeJudge ever
// sees them, so nothing but a test holds these guards in place. They are what
// the record's own reader (internal/runner's MergeJudgeOf) leans on when it
// reads a block some other writer left.
func TestMergeJudgeBlockIsBoundedAndNeverNull(t *testing.T) {
	t.Run("the text is clipped to the caps", func(t *testing.T) {
		got := NewMergeJudge(MergeJudge{
			Verdict: MergeJudgeBlocked,
			Reason:  strings.Repeat("r", MaxTriageLine+50),
			Blocker: strings.Repeat("b", MaxMergeJudgeBlocker+50),
		})
		if got == nil {
			t.Fatal("a blocked verdict with a blocker was refused")
		}
		if n := len([]rune(got.Reason)); n != MaxTriageLine {
			t.Errorf("the reason is %d runes, want %d (MaxTriageLine)", n, MaxTriageLine)
		}
		if n := len([]rune(got.Blocker)); n != MaxMergeJudgeBlocker {
			t.Errorf("the blocker is %d runes, want %d (MaxMergeJudgeBlocker)", n, MaxMergeJudgeBlocker)
		}
	})

	t.Run("the follow-ups are capped and filtered", func(t *testing.T) {
		const key = "0f1e2d3c4b5a6978877665544332211a"
		followUps := make([]MergeJudgeFollowUp, 0, MaxMergeJudgeFollowUps+8)
		for i := 0; i < MaxMergeJudgeFollowUps+8; i++ {
			followUps = append(followUps, MergeJudgeFollowUp{Key: key, Importance: MergeJudgeMinor})
		}
		// Two the record may not carry: a key that is not one this tree writes
		// and a word outside D2040's two. They are dropped rather than stored —
		// a reader cannot act on either — and they are dropped from anywhere in
		// the list, not only from its tail.
		followUps = append([]MergeJudgeFollowUp{
			{Key: "not-a-key", Importance: MergeJudgeMinor},
			{Key: key, Importance: "urgent"},
		}, followUps...)
		got := NewMergeJudge(MergeJudge{Verdict: MergeJudgeMergeWithFollowUps, Reason: "fine", FollowUps: followUps})
		if got == nil {
			t.Fatal("a merge verdict was refused")
		}
		if len(got.FollowUps) != MaxMergeJudgeFollowUps {
			t.Fatalf("the block holds %d follow-ups, want the cap of %d", len(got.FollowUps), MaxMergeJudgeFollowUps)
		}
		for _, f := range got.FollowUps {
			if f.Key != key || f.Importance != MergeJudgeMinor {
				t.Fatalf("a follow-up the filter should have dropped was stored: %+v", f)
			}
		}
	})

	t.Run("follow_ups is an empty array and never null", func(t *testing.T) {
		got := NewMergeJudge(MergeJudge{Verdict: MergeJudgeMergeWithFollowUps, Reason: "fine"})
		if got == nil {
			t.Fatal("a merge verdict was refused")
		}
		if got.FollowUps == nil {
			t.Fatal("the block's follow_ups is nil, which a reader ranging over it and a schema reading it both refuse")
		}
		data, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"follow_ups":[]`) {
			t.Errorf("the stored block carries %s, want follow_ups as an empty array", data)
		}
	})

	t.Run("a word outside the two is no block at all", func(t *testing.T) {
		if got := NewMergeJudge(MergeJudge{Verdict: "merge", Reason: "fine"}); got != nil {
			t.Errorf("a third word was stored as %+v, want no block: a reader would read a word the judge never said", got)
		}
	})

	t.Run("a block with nothing to name is unreadable", func(t *testing.T) {
		got := NewMergeJudge(MergeJudge{Verdict: MergeJudgeBlocked, Reason: "no"})
		if got == nil {
			t.Fatal("a blocked verdict was refused")
		}
		if !got.Unreadable {
			t.Errorf("the stored block is %+v, want it marked unreadable: rung 4's split has nothing to be scoped to (D2031)", got)
		}
	})
}

// TestRecordOmitsAnAbsentMergeJudge: the block is additive and optional, so
// every record written before this field existed, and every run that judged
// nothing, encodes without it. An empty block in its place would be a record no
// reader can tell from a judged one, which is why the shapes say "Absent —
// never an empty block" and why the ladder reads the absence as the veto it is
// (D2044).
func TestRecordOmitsAnAbsentMergeJudge(t *testing.T) {
	data, err := json.Marshal(Record{RunID: "20261112-000000-abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "merge_judge") {
		t.Errorf("a record with no verdict carries the field: %s", data)
	}
}
