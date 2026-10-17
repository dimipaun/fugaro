package managed

import (
	"errors"
	"strings"
	"testing"
)

// The config gate's own tests: what this release ships is closed, the closed
// value is the zero value, and the refusal is the plan's sentence word for
// word. Task 4.4 is the task that changes Shipped, and this file is what says
// so out loud rather than in a comment.

// The shipped gate is closed, and it is the zero value: a caller that forgets
// to thread it through is refused rather than admitted.
func TestShippedGateIsClosedAndIsTheZeroValue(t *testing.T) {
	if Shipped().Open {
		t.Fatal("the shipped gate is open, and phase 4's three safeguards are not in this release")
	}
	var zero Enable
	if zero.Open {
		t.Fatal("the zero Enable is open")
	}
	if err := zero.Refuse(); err == nil {
		t.Fatal("the zero Enable refuses nothing")
	}
}

// The refusal is the plan's own sentence, byte for byte, and it names the three
// things that are not in this release — a reader told only "not enabled" would
// look for a flag to pass, and there is none.
func TestClosedGateRefusalIsThePlansOwnSentence(t *testing.T) {
	const sentence = "the managed apply is not enabled in this release: the whole-project inventory (Task 4.1), the required budget (Task 4.2) and the apply-time budget read (Task 4.4) are not in it"
	if NotEnabled != sentence {
		t.Errorf("NotEnabled is\n%s\nwant\n%s", NotEnabled, sentence)
	}
	err := Shipped().Refuse()
	if err == nil {
		t.Fatal("the shipped gate refuses nothing")
	}
	if !errors.Is(err, ErrNotEnabled) {
		t.Errorf("refusal %v is not ErrNotEnabled", err)
	}
	if !strings.Contains(err.Error(), sentence) {
		t.Errorf("refusal %v does not carry the plan's sentence", err)
	}
	// It names no flag, variable or command a reader could pass: the session it
	// refuses is the one that would read it as an instruction (IDEHint's rule).
	for _, banned := range []string{"--", "export ", "unset ", "set ", "FUGARO_"} {
		if strings.Contains(err.Error(), banned) {
			t.Errorf("the refusal %v names %q, which its reader would take as a way through", err, banned)
		}
	}
}

// And the open value is the one Task 4.4 passes: it refuses nothing, so the
// gate is the only thing the release is waiting on.
func TestOpenGateRefusesNothing(t *testing.T) {
	if err := (Enable{Open: true}).Refuse(); err != nil {
		t.Errorf("the open gate refused: %v", err)
	}
}
