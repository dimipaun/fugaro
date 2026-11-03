package managed

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The whole point of Converge's shape (D1021): a caller that read nothing
// refuses, whatever the step's class is. Both halves of the rule fall the same
// way here — the type was never built, and the class the zero value carries is
// initflow.Typed, which no plan gate admits (D1026).
func TestConvergeZeroValueRefuses(t *testing.T) {
	for _, class := range []initflow.ConfirmClass{initflow.Typed, initflow.YesOrTyped} {
		c := Converge{class: class}
		err := c.Refuse()
		if err == nil {
			t.Fatalf("a Converge nobody built admits a %v step", class)
		}
		if !errors.Is(err, ErrNotConverged) {
			t.Errorf("the refusal of a %v step does not carry ErrNotConverged: %v", class, err)
		}
		if !strings.Contains(err.Error(), NotConverged) {
			t.Errorf("the refusal of a %v step does not say %q: %v", class, NotConverged, err)
		}
		if c.Digest() != "" {
			t.Errorf("a Converge nobody built carries the digest %q, want none", c.Digest())
		}
		if got := c.Rules(); len(got) != 0 {
			t.Errorf("a Converge nobody built carries the rules %v, want none", got)
		}
	}
}

// D1026's half that lives inside the type: the gate can only narrow, so a clean
// verdict over a class the stage table gives no --yes for is still refused —
// and no caller can forget the check, because it is not the caller's.
func TestConvergeRefusesATypedClass(t *testing.T) {
	doc := []byte(`{"the":"document the rules read"}`)
	rules := []string{"a rule that fired"}
	c := ConvergeAdmitted(doc, initflow.Typed, rules)
	if err := c.Refuse(); err == nil {
		t.Fatal("a clean verdict admits a Typed step: the gate widened instead of narrowing (D1026)")
	}
	if c.Digest() == "" {
		t.Error("the refusal of a Typed step carries no digest of the document that was judged")
	}
	// The same document and the same rules over the class the gate judges: the
	// class is the only thing that differs, so the class is what refused.
	if err := ConvergeAdmitted(doc, initflow.YesOrTyped, rules).Refuse(); err != nil {
		t.Errorf("an admitted YesOrTyped step is refused: %v", err)
	}
	if err := ConvergeRefused(doc, initflow.YesOrTyped, rules).Refuse(); err == nil {
		t.Error("a refused verdict admits the step")
	}
}

// D1022: the gate hashes the exact bytes it read, so the document that was
// judged and the document that was applied are provably one artifact. The
// digest is of those bytes and of nothing re-rendered — two documents one byte
// apart never share one — and a refusal carries it as well as an admission, or
// a reader of the record could not find the document a refusal was about.
func TestConvergeDigestIsOfTheBytesRead(t *testing.T) {
	a := []byte(`{"repos":["one"]}`)
	b := []byte(`{"repos":["two"]}`)
	admitted := ConvergeAdmitted(a, initflow.YesOrTyped, nil)
	if admitted.Digest() == ConvergeAdmitted(b, initflow.YesOrTyped, nil).Digest() {
		t.Fatal("two documents that differ have the same digest")
	}
	sum := sha256.Sum256(a)
	if want := hex.EncodeToString(sum[:]); admitted.Digest() != want {
		t.Errorf("the digest is %q, want the sha256 of the bytes the rules read (%q)", admitted.Digest(), want)
	}
	refused := ConvergeRefused(a, initflow.YesOrTyped, []string{"a rule that fired"})
	if refused.Digest() != admitted.Digest() {
		t.Errorf("the refusal's digest is %q, want the judged document's %q", refused.Digest(), admitted.Digest())
	}
	if got := refused.Rules(); len(got) != 1 || got[0] != "a rule that fired" {
		t.Errorf("the refusal carries the rules %v, want the one that fired", got)
	}
	// What a caller reads is its own: nothing a caller does to the rules it
	// handed in, or to the copy it read back, changes the verdict it holds.
	handed := []string{"a rule that fired"}
	held := ConvergeRefused(a, initflow.YesOrTyped, handed)
	handed[0] = "another rule"
	if got := held.Rules(); len(got) != 1 || got[0] != "a rule that fired" {
		t.Errorf("the rules the refusal holds moved with the caller's slice: %v", got)
	}
	read := held.Rules()
	read[0] = "another rule"
	if got := held.Rules(); len(got) != 1 || got[0] != "a rule that fired" {
		t.Errorf("the rules the refusal holds moved with the slice it handed out: %v", got)
	}
}

// The refusal prints the rules that fired, and a rule's own words can quote a
// plan — an address, an attribute, a cloud API's error. It is therefore one of
// the sites this package's own enumeration is about
// (TestEveryManagedRefusalSiteBoundsUntrustedText), and it is bounded by the
// same helper, which is also where the count is bounded: a listing that grew
// with its input would push this program's own sentence off the screen, so the
// entries it does not print are counted instead.
func TestConvergeBoundsTheRulesItPrints(t *testing.T) {
	doc := []byte(`{"the":"document the rules read"}`)
	for name, v := range hostileValues() {
		err := ConvergeRefused(doc, initflow.YesOrTyped, []string{v, v}).Refuse()
		if err == nil {
			t.Fatalf("%s: a refused verdict admitted the step", name)
		}
		bounded(t, name+" in the refusal", err.Error(), 1)

		many := make([]string, 5000)
		for i := range many {
			many[i] = v
		}
		long := ConvergeRefused(doc, initflow.YesOrTyped, many).Refuse().Error()
		if note := safetext.FieldListNote(len(many) - safetext.FieldListMax); !strings.Contains(long, note) {
			t.Errorf("%s: a listing past the cap does not say %q:\n%q", name, note, long)
		}
		bounded(t, name+" in the capped refusal", long, 1)
	}
}
