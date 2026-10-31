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
