package runner

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/failure"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestApplyChallengerRefusesASlotOutsideTheVocabulary: the slot says which one
// thing a pair varies, and its vocabulary is closed (task.ABSlots, D728). The
// runner reads its task through task.Parse, which validates the slot, so a
// stored task naming anything else is refused before this is reached — this is
// the arm's own guard behind that one, and it is called directly because the
// decode is what makes it unreachable end to end. Reading an unknown slot as a
// coder pair would run the whole coding loop for a pair that asked for
// something else entirely, so the arm is refused by name, before any stage and
// before any clone, and the record carries the arm's own class rather than an
// infrastructure one.
func TestApplyChallengerRefusesASlotOutsideTheVocabulary(t *testing.T) {
	const challenger = "deepseek/deepseek-v4.1-flash"
	r := &run{
		shadow: true,
		spec: &task.Spec{AB: &task.AB{
			Arm: task.ABArmChallenger, Challenger: challenger, Slot: task.ABSlot("l9"),
		}},
		ab:  &config.AB{Challengers: []string{challenger}},
		rec: &runstore.Record{},
		d:   Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))},
	}
	err := r.applyChallenger(&config.Agent{})
	if err == nil {
		t.Fatal("a slot outside the vocabulary was accepted")
	}
	if r.rec.Status != runstore.StatusFailed || r.rec.Outcome != runstore.OutcomeNone || r.rec.FailureClass != failure.ConfigInvalid {
		t.Fatalf("record = %s/%s/%s, want failed/none/%s", r.rec.Status, r.rec.Outcome, r.rec.FailureClass, failure.ConfigInvalid)
	}
	// The reason names the slot the task carries and the vocabulary it is
	// outside of, so a reader can tell a typo from a slot this runner has no
	// pair for.
	for _, want := range []string{"l9", string(task.ABSlotL1), string(task.ABSlotL2), string(task.ABSlotL3)} {
		if !strings.Contains(r.rec.Reason, want) {
			t.Fatalf("the reason does not name %q: %q", want, r.rec.Reason)
		}
	}
}
