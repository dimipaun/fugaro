package yamledit

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestVerifiedRefusesAResultThatDoesNotCarryTheChange holds the last guard to
// what it promises. No public operation can be made to reach it on purpose —
// that is the point of the guard — and it is the only thing standing between a
// splice that drifted and a settings file written wrong, so it is pinned here:
// bytes that do not parse are refused, a result that does not carry the change
// is refused, and a result that does carry it is not.
func TestVerifiedRefusesAResultThatDoesNotCarryTheChange(t *testing.T) {
	path := []string{"a"}

	if err := verified("SetScalar", path, []byte("a: 1\n"), func(*yaml.Node) bool { return false }); ReasonOf(err) != ReasonVerification {
		t.Fatalf("a result that does not carry the change: reason = %q, want %q (%v)", ReasonOf(err), ReasonVerification, err)
	}
	if err := verified("SetScalar", path, []byte("a: [1\n"), func(*yaml.Node) bool { return true }); ReasonOf(err) != ReasonVerification {
		t.Fatalf("a result that does not parse: reason = %q, want %q (%v)", ReasonOf(err), ReasonVerification, err)
	}
	if err := verified("SetScalar", path, []byte("a: 1\n"), func(root *yaml.Node) bool {
		return scalarIs(resolve(root, path), "1")
	}); err != nil {
		t.Fatalf("a result that carries the change was refused: %v", err)
	}
}

// TestVerifiedNamesThePathAndNoBytes pins that the guard's refusal is shaped
// like every other one: the path it was asked about, and nothing the document
// holds.
func TestVerifiedNamesThePathAndNoBytes(t *testing.T) {
	err := verified("SetScalar", []string{"kept", "deeper"}, []byte("kept:\n  other: value-a\n"), func(*yaml.Node) bool { return false })
	if err == nil {
		t.Fatal("no refusal")
	}
	if got := err.Error(); got != "yamledit: SetScalar: "+string(ReasonVerification)+" (at kept.deeper)" {
		t.Fatalf("the refusal reads %q", got)
	}
}

// TestVerifiedRefusesAResultWhoseFlowNeighbourChanged hands the guard the shape
// the sibling check is there for: the value at the path is the one that was
// asked for, and the member beside it is not what it was — `{0: , b: 1}` comes
// back as `{0:, b: 2}`, where the comma against the colon turns the key `0`
// into the key `0:`. The closure is the one SetScalar passes, and no operation
// can be asked to reach it on purpose: the splices that produced such a result
// are refused or written differently, which is what makes this check the last
// thing between a splice that landed against its neighbour and a settings file
// that reads back as settings nobody wrote. Bytes that carry the value and
// leave the neighbour alone are not refused, so the check is not simply saying
// no.
func TestVerifiedRefusesAResultWhoseFlowNeighbourChanged(t *testing.T) {
	path := []string{"b"}
	d, err := open("SetScalar", []byte("{0: , b: 1}\n"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	carries := func(root *yaml.Node) bool {
		return scalarIs(resolve(root, path), "2") && d.sameFlowSiblings(path, root)
	}

	if err := verified("SetScalar", path, []byte("{0:, b: 2}\n"), carries); ReasonOf(err) != ReasonVerification {
		t.Fatalf("a member beside the path that reads differently: reason = %q, want %q (%v)", ReasonOf(err), ReasonVerification, err)
	}
	if err := verified("SetScalar", path, []byte("{0: , b: 2}\n"), carries); err != nil {
		t.Fatalf("a result that carries the value and leaves the neighbour as it was: %v", err)
	}
}
