package managed

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The gate's shape: what one step's document was judged to be, and the class
// the step's confirmation is, in one value every command in the managed set
// reads BEFORE it writes (D1012, D1021).
//
// Why it is a type in this package and not a predicate in internal/cli: the
// question — "was this step's document admitted, and may an agent's session
// proceed on that answer" — is the same question in `fugaro update`, `config
// publish`, `project policy publish` and `image refresh`, and four predicates
// written four times are four chances for one of them to answer with the
// admitting direction by accident. The per-command RULES stay where each
// command's rules already live — internal/infra/tf's ManagedCover, ManagedCosts
// and Guard for a Terraform plan (D1021), and a command's own registry,
// generation and bounds elsewhere; what is shared here is only the shape of a
// verdict and the one class rule a per-command rule must not be able to
// redefine.
//
// Why a value and not a flag or a variable (D1021): the session this gate
// admits is a coding agent's, and a gate that session could open on its own
// command line or in its own environment would have handed back the refusal it
// exists to answer. The fields are unexported, the zero value refuses, and the
// only way to hold an admitted Converge is one of the two constructors below.
// They are not proof that a read happened — `check.go` says the same of
// CheckedInventory, and it is true here for the same reason: a caller that
// constructs a clean verdict by hand is lying about a read, no type in this
// package can prevent it, and the tests that pin the WIRING, that a command
// reads the gate before it writes, are what catch it.
//
// Why the class rule lives inside Refuse and nowhere else (D1026): a Typed step
// is money or permanent, and a document cannot carry a plan for it, so no clean
// verdict may admit one. A caller that reads its class off its own stage table
// and remembers to check it is a caller that can forget; the type therefore
// refuses a Typed class whatever the verdict was, and the gate can only ever
// narrow a step's confirmation, never widen it.
//
// Why the digest (D1022): the record of a gate's answer names the document that
// was judged, and the digest is what makes the document the gate read and the
// document the command applied provably one artifact rather than two that
// happened to agree. It is computed here, from the bytes the caller read, so
// nothing can be handed in beside it that disagrees with what the rules saw.
//
// This file lands wired to nothing (the plan's §2): a command that lifted a
// refusal before the gate existed would be the one shape this design must not
// have, so the gate is built and tested first and its callers arrive after it.

// Converge is a step's judged document and the class its confirmation is: the
// two facts a managed project's removal of the coding agent's refusal is
// conditional on, held together so they cannot be established separately and
// recombined wrongly by a caller.
type Converge struct {
	// digest is the sha256, hex-encoded, of the exact bytes every rule of the
	// step read. It is "" on a Converge nobody built, because no document was
	// read to hash.
	digest string
	// class is the step's own confirmation class, read off the stage table or
	// the command's own equivalent of it. The zero value is initflow.Typed, the
	// strictest class, so a caller that names no class gets the refusing answer
	// — the same direction ConfirmClass' own zero value already takes.
	class initflow.ConfirmClass
	// rules are the rules that fired, each in its own words: on a refusal, the
	// ones that refused; on an admission, the ones that were applied and passed.
	// They are carried because the sentence a refusal reads names the rule that
	// refused (D1023) and because the audit record holds them (spec §6), and
	// they are bounded where they are rendered, since a rule's words can quote
	// a plan.
	rules []string
	// admitted is the verdict itself, and false is its zero value: every
	// Converge that ConvergeAdmitted did not build refuses.
	admitted bool
}

// ConvergeAdmitted is the way to say the rules ran over the document and none
// of them refused it: the caller passes the bytes IT read — the same bytes the
// rules were given, so the digest is of the document the rules judged — and the
// rules that were applied.
func ConvergeAdmitted(doc []byte, class initflow.ConfirmClass, rules []string) Converge {
	return Converge{digest: convergeDigest(doc), class: class, rules: slices.Clone(rules), admitted: true}
}

// ConvergeRefused is the other verdict a document can reach: at least one rule
// refused it, and the caller passes the rules that did.
//
// A document that could not be read at all is not one of these. It is an error
// before this type exists (plan.go's own rule for a plan terraform could not
// read, which is what keeps "no verdict" from ever reading as "admitted"), and
// a command in the set fails on it rather than writing.
func ConvergeRefused(doc []byte, class initflow.ConfirmClass, rules []string) Converge {
	return Converge{digest: convergeDigest(doc), class: class, rules: slices.Clone(rules)}
}

// ErrNotConverged is the refusal of a step the gate did not admit. It is a
// value as well as a message, so a caller can match on it without reading
// English and so the class rule and a spent verdict are told apart by the
// sentence alone.
var ErrNotConverged = errors.New("the managed gate did not admit this step")

// NotConverged is the one sentence a refused step reads. It names no flag, no
// variable and no marker a reader could change to get past the gate (the rule
// IDEHint and AgentProceeded in internal/initflow already follow), because a
// recipe for reaching past a control has no business in the text an agent reads
// — and what it does not claim is that the step is safe: the sentence says this
// program wrote nothing, and the rules that refused are named after it.
const NotConverged = "a managed project admits an agent's session to the steps this gate judged and to no others, so nothing is written here: no person is being asked, and no flag, variable or mark reaches past the gate (D1012)"

// Refuse is the gate's whole behaviour: nil only when a verdict admitted the
// document AND the step's class is one a plan gate judges. It is the FIRST
// thing a command in the set reads — before its first write, and before any
// argument of a write is built — so a Converge that was never built refuses
// however it is called.
//
// The two ways to a refusal are one predicate on purpose: a caller cannot ask
// "was the plan admitted" and act on that answer, because the answer the class
// rule gives is the refusal (D1026), and the classes the stage table refuses a
// --yes for never become agent-runnable because a plan looks clean.
func (c Converge) Refuse() error {
	if c.admitted && c.class != initflow.Typed {
		return nil
	}
	return fmt.Errorf("%w: %s — %s", ErrNotConverged, NotConverged, c.refusedBy())
}

// refusedBy is what the refusal names after NotConverged, so a reader acts on
// it without re-deriving it from the verdict: the class rule where the class is
// what refused, and the rules that fired where the verdict is. One of the two
// branches always applies when Refuse has returned an error, so the sentence
// never ends in a bare dash; the rules are rendered through safetext's own
// listing because a rule's words can quote a plan, and an address, a newline or
// a screenful of someone else's text in this sentence would be this program
// printing it to the session it is refusing.
func (c Converge) refusedBy() string {
	why := make([]string, 0, 2)
	if !c.admitted {
		if len(c.rules) == 0 {
			why = append(why, "no rule of this step's document admitted it")
		} else {
			why = append(why, "refused by "+safetext.FieldListLine(c.rules))
		}
	}
	if c.class == initflow.Typed {
		why = append(why, "the step's confirmation class is Typed, which no plan gate admits (D1026)")
	}
	return strings.Join(why, "; ")
}

// Digest is the document's sha256 as the record of the gate's answer names it
// (D1022), and "" for a Converge nobody built. Nothing else may stand in for
// it: the digest is what lets an owner who kept the judged document check that
// the one applied was the one judged.
func (c Converge) Digest() string { return c.digest }

// Rules are the rules that fired, as a copy: a caller cannot change the verdict
// it holds by writing to the slice it handed in or to the one it read back, and
// the refusal that printed them printed what the gate held.
func (c Converge) Rules() []string { return slices.Clone(c.rules) }

// convergeDigest is the document's bytes, hex-encoded sha256: the digest the
// gate carries of the exact bytes it read, and the one D1022's record is
// checked against.
func convergeDigest(doc []byte) string {
	sum := sha256.Sum256(doc)
	return hex.EncodeToString(sum[:])
}
