package issues

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// Outcome is what a diagnosis concluded about a finding: the labels
// internal/diagnoseagent's prompt asks the agent to state. Two of them have
// something to save and two do not (F1, F6).
type Outcome string

const (
	// OutcomeConfiguration is a wrong setting on this machine: the fix is
	// printed and nothing is saved.
	OutcomeConfiguration Outcome = "configuration"
	// OutcomeBug is a bug in Fugaro.
	OutcomeBug Outcome = "bug"
	// OutcomeFixedInNewerRelease is a bug a later release already fixes: the
	// fix is `fugaro update` and nothing is saved.
	OutcomeFixedInNewerRelease Outcome = "fixed_in_newer_release"
	// OutcomeUnclear is a finding the diagnosis could not attribute.
	OutcomeUnclear Outcome = "unclear"
)

// Valid reports whether o is one of the four labels the prompt states. The
// labels are exact: a case spelling, or the `local` of the draft, is not one.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeConfiguration, OutcomeBug, OutcomeFixedInNewerRelease, OutcomeUnclear:
		return true
	}
	return false
}

// Verdict is one diagnosis: what the agent concluded and what it has to say
// about it. The agent writes no file, so every field here is text a person
// reads and a value this package checks before anything is written from it
// (F6).
type Verdict struct {
	// Outcome is the label the answer stated.
	Outcome Outcome
	// Summary is the one line a listing shows.
	Summary string
	// Diagnosis is the cause, in the agent's own words.
	Diagnosis string
	// Steps is how to reproduce the finding, empty when the diagnosis does
	// not know one.
	Steps string
	// FixSteps is what fixes it: what the two outcomes with something to do
	// on this machine have to print (F1).
	FixSteps string
	// Signature is the failing command and the error class, the agent's
	// normalized answer that duplicates are matched by (D1724).
	Signature string
}

// Validate refuses a verdict this package would not act on: an outcome outside
// the four labels, and — for the two outcomes that are saved — a signature with
// nothing in it. The record is written by the CLI from the verdict and never by
// the agent (F6), so a value nobody here knows stops at this gate rather than
// becoming a class.
func (v Verdict) Validate() error {
	if !v.Outcome.Valid() {
		return fmt.Errorf("issues: %q is not an outcome, want %q, %q, %q or %q",
			v.Outcome, OutcomeConfiguration, OutcomeBug, OutcomeFixedInNewerRelease, OutcomeUnclear)
	}
	// A verdict with no signature hashes to the one fingerprint every other
	// unsigned verdict hashes to, and an exact match would then attach an
	// unrelated finding to the issue that got there first — the silent merge
	// F21 forbids. The two outcomes that are not saved need no signature:
	// nothing of theirs reaches the store.
	if _, saved := v.Saved(); saved && normaliseSignature(v.Signature) == "" {
		return fmt.Errorf("issues: a %q verdict carries the signature it is matched by, and this one is blank", v.Outcome)
	}
	return nil
}

// Saved is the class an outcome is saved under, and whether there is one. A
// finding that is only this machine's — a wrong setting, or a bug a newer
// release already fixes — has no class, so nothing can persist one
// (acceptance 1, F6).
func (v Verdict) Saved() (Class, bool) {
	switch v.Outcome {
	case OutcomeBug:
		return ClassFugaro, true
	case OutcomeUnclear:
		return ClassUnclear, true
	}
	return "", false
}

// fingerprintRE is the shape a fingerprint has: the SHA-256 of the normal form,
// in lowercase hex. Fingerprint writes it and the record holds both its header
// and its schema to it, so a value that is neither empty nor this shape is one
// no writer here produces and one a validating reader would refuse.
var fingerprintRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Fingerprint is the key two reports of one bug share: the hash of the
// signature's normal form (D1724). An exact match attaches an occurrence to
// the issue that already has the fingerprint, and two signatures that differ
// by a word stay two issues — nothing here merges them (F21).
func Fingerprint(signature string) string {
	sum := sha256.Sum256([]byte(normaliseSignature(signature)))
	return hex.EncodeToString(sum[:])
}

// normaliseSignature is the form the hash is taken over: the case folded and
// every run of whitespace folded to one space. The signature is free text the
// agent writes, so the same failing command comes back spelled with another
// case or another line break, and folding those two is what makes an exact
// match find the report that is already there. Nothing else is folded: two
// signatures that differ by a word are two reports.
func normaliseSignature(signature string) string {
	return strings.ToLower(strings.Join(strings.Fields(signature), " "))
}
