package managed

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// The whole point of Converge's shape (D1021): a caller that read nothing
// refuses. The type was never built, so there is no verdict to read and no
// document to hash.
func TestConvergeZeroValueRefuses(t *testing.T) {
	c := Converge{}
	err := c.Refuse()
	if err == nil {
		t.Fatal("a Converge nobody built admits the step")
	}
	if !errors.Is(err, ErrNotConverged) {
		t.Errorf("the refusal of an unbuilt Converge does not carry ErrNotConverged: %v", err)
	}
	if !strings.Contains(err.Error(), NotConverged) {
		t.Errorf("the refusal of an unbuilt Converge does not say %q: %v", NotConverged, err)
	}
	if c.Digest() != "" {
		t.Errorf("a Converge nobody built carries the digest %q, want none", c.Digest())
	}
	if got := c.Rules(); len(got) != 0 {
		t.Errorf("a Converge nobody built carries the rules %v, want none", got)
	}
	if c.Unjudged() {
		t.Error("a Converge nobody built says the step is one no plan gate may judge")
	}
}

// D1026's half that lives inside the type: the gate can only narrow, so a clean
// verdict over a step no plan gate may judge is still refused — and no caller
// can forget the check, because it is not the caller's. The same document and
// the same rules over a step the gate does judge are admitted, so it is the
// unjudged fact alone that refused, and an unjudged step is refused whether a
// verdict was built beside it or not.
func TestConvergeRefusesAnUnjudgedStep(t *testing.T) {
	doc := []byte(`{"the":"document the rules read"}`)
	rules := []string{"a rule that fired"}
	c := ConvergeUnjudged(doc, rules)
	if err := c.Refuse(); err == nil {
		t.Fatal("a step no plan gate may judge is admitted: the gate widened instead of narrowing (D1026)")
	}
	if !c.Unjudged() {
		t.Error("the Converge built for a step no plan gate may judge does not report it")
	}
	if c.Digest() == "" {
		t.Error("the refusal of an unjudged step carries no digest of the document that was read")
	}
	if err := c.Refuse(); strings.HasSuffix(err.Error(), "— ") {
		t.Errorf("the refusal of an unjudged step ends in a bare dash: %v", err)
	}
	if err := ConvergeAdmitted(doc, rules).Refuse(); err != nil {
		t.Errorf("an admitted step the gate may judge is refused: %v", err)
	}
	if err := ConvergeRefused(doc, rules).Refuse(); err == nil {
		t.Error("a refused verdict admits the step")
	}
	if err := ConvergeWarned(doc, rules, []string{"a finding"}).Refuse(); err != nil {
		t.Errorf("a warned step the gate may judge is refused: %v", err)
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
	admitted := ConvergeAdmitted(a, nil)
	if admitted.Digest() == ConvergeAdmitted(b, nil).Digest() {
		t.Fatal("two documents that differ have the same digest")
	}
	sum := sha256.Sum256(a)
	if want := hex.EncodeToString(sum[:]); admitted.Digest() != want {
		t.Errorf("the digest is %q, want the sha256 of the bytes the rules read (%q)", admitted.Digest(), want)
	}
	refused := ConvergeRefused(a, []string{"a rule that fired"})
	if refused.Digest() != admitted.Digest() {
		t.Errorf("the refusal's digest is %q, want the judged document's %q", refused.Digest(), admitted.Digest())
	}
	if got := refused.Rules(); len(got) != 1 || got[0] != "a rule that fired" {
		t.Errorf("the refusal carries the rules %v, want the one that fired", got)
	}
	// What a caller reads is its own: nothing a caller does to the rules it
	// handed in, or to the copy it read back, changes the verdict it holds.
	handed := []string{"a rule that fired"}
	held := ConvergeRefused(a, handed)
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

// The ruling of 2026-11-11: a document the integrity rules refused can carry
// the content findings the gate raised beside them, and the record of the
// refusal holds both. Carrying them is NOT being applied with them — that is
// the whole reason refused is a field of its own — so Warned stays false and
// Refuse still refuses, whatever the findings say. The other half is what the
// session reads: the refusal's own sentence names the rules that stopped it and
// not the findings, because a session that was refused acts on what refused and
// the findings are the record's business.
func TestConvergeRefusedWithWarningsStillRefuses(t *testing.T) {
	doc := []byte(`{"the":"document the rules read"}`)
	const rule = "the plan removes a bucket that holds state"
	const finding = "the plan sets a cost attribute the tables do not carry"
	c := ConvergeRefusedWithWarnings(doc, []string{rule}, []string{finding})

	err := c.Refuse()
	if err == nil {
		t.Fatal("a refusal that carries findings admits the step: the findings passed for an admission")
	}
	if !errors.Is(err, ErrNotConverged) {
		t.Errorf("the refusal does not carry ErrNotConverged: %v", err)
	}
	if c.Warned() {
		t.Error("a refused document reports Warned, so a record of it would say it was applied with the findings")
	}
	if got := c.Warnings(); len(got) != 1 || got[0] != finding {
		t.Errorf("the refusal carries the findings %q, want the one the gate raised", got)
	}
	if got := c.Rules(); len(got) != 1 || got[0] != rule {
		t.Errorf("the refusal carries the rules %q, want the one that fired", got)
	}
	sum := sha256.Sum256(doc)
	if want := hex.EncodeToString(sum[:]); c.Digest() != want {
		t.Errorf("the refusal's digest is %q, want the judged document's %q", c.Digest(), want)
	}
	if !strings.Contains(err.Error(), rule) {
		t.Errorf("the refusal does not name the rule that fired:\n%q", err.Error())
	}
	if strings.Contains(err.Error(), finding) {
		t.Errorf("the refusal prints the findings beside the rules, which the record holds and this sentence does not:\n%q", err.Error())
	}

	// The unjudged rule is not weakened by the findings either: a step no plan
	// gate may judge is refused whether or not a verdict was built beside it.
	if err := ConvergeUnjudged(doc, []string{rule}).Refuse(); err == nil {
		t.Error("a step no plan gate may judge is admitted by a Converge built for it")
	}
	plain := ConvergeRefused(doc, []string{rule})
	if plain.Warned() || len(plain.Warnings()) != 0 {
		t.Errorf("a plainly refused Converge reports Warned %v with findings %q, want neither", plain.Warned(), plain.Warnings())
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
		err := ConvergeRefused(doc, []string{v, v}).Refuse()
		if err == nil {
			t.Fatalf("%s: a refused verdict admitted the step", name)
		}
		bounded(t, name+" in the refusal", err.Error(), 1)

		many := make([]string, 5000)
		for i := range many {
			many[i] = v
		}
		long := ConvergeRefused(doc, many).Refuse().Error()
		if note := safetext.FieldListNote(len(many) - safetext.FieldListMax); !strings.Contains(long, note) {
			t.Errorf("%s: a listing past the cap does not say %q:\n%q", name, note, long)
		}
		bounded(t, name+" in the capped refusal", long, 1)
	}
}
