package managed

import (
	"errors"
	"strings"
	"testing"
)

// The whole point of InventoryCheck's shape: the zero value is the fallback,
// not the clean project. A Decision's zero value is the other way round, and a
// caller that forgot the read would otherwise hand the apply a project nobody
// looked at and get D55's removal for it.
func TestInventoryCheckZeroValueFallsBack(t *testing.T) {
	var zero InventoryCheck
	if !zero.Fallback() {
		t.Fatal("the zero InventoryCheck does not fall back: a caller that made no read would proceed")
	}
	if zero.Cause() != CauseNoReader {
		t.Errorf("Cause = %q, want %q", zero.Cause(), CauseNoReader)
	}
	if zero.Why() != NotCheckedWhy {
		t.Errorf("Why = %q, want the program's own sentence", zero.Why())
	}
	// And the same for a value that only a read can build, over every Decision
	// Inventory.Check can produce: the fallback is the decision's own, and a
	// clean decision is the only one that does not fall back.
	a := &fakeAssets{assets: []Asset{listed("//storage.googleapis.com/fugaro-runs-"+gcpProject, "storage.googleapis.com/Bucket")}}
	for _, d := range []Decision{
		{},
		inventory(a, StateResource{Type: "storage.googleapis.com/Bucket", Name: "fugaro-runs-" + gcpProject}).Check(t.Context()),
		inventory(&fakeAssets{err: errors.New("connection reset")}).Check(t.Context()),
		Unread(project, gcpProject, CauseStateUnread, "the state bucket could not be listed"),
	} {
		c := CheckedInventory(d)
		if c.Fallback() != d.Fallback {
			t.Errorf("CheckedInventory(%+v).Fallback() = %v, want the decision's own %v", d, c.Fallback(), d.Fallback)
		}
		if c.Cause() != d.Cause {
			t.Errorf("CheckedInventory(%+v).Cause() = %q, want %q", d, c.Cause(), d.Cause)
		}
		if c.Why() != d.Why {
			t.Errorf("CheckedInventory(%+v).Why() = %q, want %q", d, c.Why(), d.Why)
		}
		if d.Fallback && c.Why() == "" {
			t.Errorf("CheckedInventory(%+v) falls back with nothing to say", d)
		}
	}
	// A check that falls back always has a sentence, and one that does not has
	// none: the apply prints the sentence and asks or refuses on the boolean.
	if got := CheckedInventory(Decision{}); got.Fallback() || got.Cause() != "" || got.Why() != "" {
		t.Errorf("a clean check = %+v (%s), want no fallback and nothing to say", got, got)
	}
}

// Unread is for the failures outside Inventory.Check — no client to read with,
// a state that could not be read — and it is the same fallback, in the same
// words, with the caller's cause and detail. It never reads as clean, which is
// the direction the caller that cannot read must take (D59, Global Constraint
// 3), and the project's names are in the sentence so a reader knows which
// project was not checked.
func TestUnreadIsAFallbackWithTheCallersCause(t *testing.T) {
	for _, cause := range []Cause{CauseStateUnread, CauseUnreadable, CauseAPIDisabled} {
		d := Unread(project, gcpProject, cause, "gs://fugaro-tfstate-"+gcpProject+" could not be listed")
		if !d.Fallback {
			t.Fatalf("Unread(%q) does not fall back: %+v", cause, d)
		}
		if d.Cause != cause {
			t.Errorf("Cause = %q, want %q", d.Cause, cause)
		}
		if len(d.Foreign) != 0 {
			t.Errorf("Foreign = %+v, want none: nothing was read", d.Foreign)
		}
		for _, want := range []string{string(cause), project, gcpProject, "confirmation"} {
			if !strings.Contains(d.Why, want) {
				t.Errorf("the sentence does not name %q: %s", want, d.Why)
			}
		}
	}
}
