package projectpolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// Binaries is the binaries block of a policy (D981–D983): the paths a run may
// add a binary at, and how large one may be. It is the owner's grant on this
// node and nowhere else — `fugaro.yaml` has no such key (D986), because that
// file is in the checkout the run's own agent writes, and a key there would
// let the coder admit the very payload the rule refuses.
//
// The block is read as *data* and not as a decision: what a run does with it
// (an added binary under an admitted pattern, within the cap, is committed
// rather than held back) belongs to the runner's finalize, and the merge that
// lets a lower layer only narrow it belongs to internal/policy. This type is
// the shape those two read, and the one place the shape is validated.
//
// Both members carry omitempty so that a project with no block writes no
// member at all — the byte-for-byte promise every optional member of this node
// keeps — and so that a written block never carries a `"allow": null` a person
// reading the node would have to interpret. A written *empty* block is still a
// written block: `{"binaries": {}}` is how an operator takes an admission away
// on purpose (D987), and the pointer on Policy is what tells it from silence.
type Binaries struct {
	Allow    []string `json:"allow,omitempty"`
	MaxBytes int64    `json:"max_bytes,omitempty"`
}

// DefaultBinariesMaxBytes is the cap an admitted binary is held to when the
// node's block writes no max_bytes: 2 MiB (D983).
//
// It is a bound and not a policy, and it is deliberately above the largest
// asset this repository tracks (brand/fugaro-logo.png, 855 285 bytes) with
// room for a retina-sized replacement: a cap that refused the images a site
// needs would be a rule nobody could satisfy without reading the manual first.
// A project may only lower it, and ValidBinariesMaxBytes is what says so.
const DefaultBinariesMaxBytes int64 = 2 * 1024 * 1024

// binariesMembers is the block's own member spelling, read from the struct's
// json tags (carrierMembers) so a field added here is known without a second
// list to keep in step.
var binariesMembers = carrierMembers(reflect.TypeOf(Binaries{}))

// ValidBinariesAllow holds an allow list to the pattern rule of spec.md §2.1,
// and it is the one place that rule is applied: the node's decode calls it,
// and so does the owner's own config loader (AR1), so a pattern the operator
// writes and a pattern the node carries cannot be judged differently.
//
// The rule is internal/config's rebuild.paths rule — not empty, not absolute,
// no `..` component, a pattern doublestar's validator accepts — plus the
// length bound internal/rigor's Area.Validate adds on top of it, in the same
// order and with the same words, because this tree has one matcher and one
// pattern rule (F8, F9) and a second spelling of either is how a lock comes to
// mean two things.
//
// The length bound is not decoration: an entry is copied into the run record
// and into the pull request's status line, so an unbounded entry is unbounded
// text on a wire. The count bound is the same argument one level up, and it is
// rigor.MaxAreas' — the list a layer may write, bounded where it is written.
//
// **What it does not refuse is worth stating, because the design's own example
// assumes otherwise.** `website/public/**.png` passes: `**` means "any depth"
// only as a whole path segment, and inside one it matches like `*`, so that
// entry is a lock over exactly one level. It is a narrower grant than its
// author probably intended, and it is *not* a pattern the matcher cannot read,
// so refusing it would be a fifth pattern check — which F8 counts and F9
// forbids. The semantics are doublestar's, documented and already in force for
// rigor areas and rebuild paths; the alternative is a second glob language in
// this tree.
func ValidBinariesAllow(allow []string) error {
	if len(allow) > rigor.MaxAreas {
		return fmt.Errorf("projectpolicy: binaries.allow holds %d entries: an allow list must hold at most %d, because the entries that match reach the run record and the pull request", len(allow), rigor.MaxAreas)
	}
	for i, entry := range allow {
		at := fmt.Sprintf("binaries.allow[%d]", i)
		switch {
		case strings.TrimSpace(entry) == "":
			return fmt.Errorf("projectpolicy: %s: must not be empty: an entry is a doublestar path glob relative to the repository root, and an empty one admits nothing while looking like a grant", at)
		case len(entry) > rigor.MaxAreaPattern:
			return fmt.Errorf("projectpolicy: %s: must be at most %d characters, because the entry reaches the run record and the pull request", at, rigor.MaxAreaPattern)
		case strings.HasPrefix(entry, "/"):
			return fmt.Errorf("projectpolicy: %s: must be relative to the repository root, not start with /", at)
		case slices.Contains(strings.Split(entry, "/"), ".."):
			return fmt.Errorf("projectpolicy: %s: must not contain a .. component", at)
		case !doublestar.ValidatePattern(entry):
			return fmt.Errorf("projectpolicy: %s: is not a valid glob pattern, and a pattern the matcher cannot read is a lock that is not there", at)
		}
	}
	return nil
}

// ValidBinariesMaxBytes holds the block's cap to D983: greater than zero, and
// at most the shipped bound, because a project may only lower it.
//
// Zero and a negative value are refused rather than read as "the key is
// absent". This is the one member of the block whose zero is the *wider*
// answer: absent means the 2 MiB default, so a written 0 — which is what
// encoding/json leaves for a written null, and what a hand-edited node or a
// `max_bytes:` with nothing under it produces — would silently raise the cap
// to its ceiling. A member whose null/0 decodes into a fail-open zero is read
// off the raw document for exactly this reason (route_fee_pct is the
// precedent), and the refusal is what keeps the two apart.
func ValidBinariesMaxBytes(n int64) error {
	switch {
	case n <= 0:
		return fmt.Errorf("projectpolicy: binaries.max_bytes is %d: the cap must be greater than 0, and a written 0 or null is not the absent key — leave the member out to take the %d-byte default", n, DefaultBinariesMaxBytes)
	case n > DefaultBinariesMaxBytes:
		return fmt.Errorf("projectpolicy: binaries.max_bytes is %d: the cap must be at most %d bytes (2 MiB), which is a ceiling a project may only lower", n, DefaultBinariesMaxBytes)
	}
	return nil
}

// decodeBinaries reads the node's binaries block, and it is strict the way
// every block of this node is: a member it does not model, a member spelled in
// another case and a member written twice are refused rather than dropped,
// because neither mistake is visible in a decoded policy — a misspelt `allow`
// is a list that admits nothing and a misspelt `max_bytes` is a cap that reads
// as the default, and the operator would have no way to tell either from the
// silence of a project that set nothing.
//
// Two checks do that and neither is enough alone: the decoder refuses a member
// name it does not model, and checkMemberNames refuses one that is a known
// member spelled in another case (which the decoder folds onto it) or written
// twice. max_bytes is then read off the raw document as well, because a
// written null decodes into the zero value and this block's own rule has to
// tell that from the key being absent.
//
// A block that writes no member at all is kept as a written block and not
// folded onto the absent member. That is deliberate and it is D987's whole
// mechanism: `binaries: {allow: []}` is how an operator takes an admission
// away on purpose, while a file that says nothing leaves the published list
// alone, and a reader that normalized the two to nil would leave the publisher
// unable to tell "clear it" from "say nothing". At admission time the two are
// one answer — nothing is admitted — and that is BinariesAllow's.
//
// The empty block is reached here only from a document, never from the database
// as an empty member: it stores no member for an empty list, so the two
// spellings are one write where the node is concerned and this decode's job is
// to read back whatever a document does hold (Policy's Binaries field comment).
func decodeBinaries(raw json.RawMessage) (*Binaries, error) {
	names, err := objectMemberNames(raw)
	if err != nil {
		return nil, fmt.Errorf("projectpolicy: binaries: %w", err)
	}
	if err := checkMemberNames("binaries", names, binariesMembers); err != nil {
		return nil, fmt.Errorf("projectpolicy: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var out Binaries
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("projectpolicy: binaries: %w", err)
	}
	var written map[string]json.RawMessage
	if err := json.Unmarshal(raw, &written); err != nil {
		return nil, fmt.Errorf("projectpolicy: binaries: %w", err)
	}
	// The cap is judged only when the node writes it: an absent member is the
	// shipped bound and not a cap of zero, and BinariesMaxBytes is where the
	// two are told apart.
	if mraw, ok := written["max_bytes"]; ok {
		if IsNull(mraw) {
			return nil, fmt.Errorf("projectpolicy: binaries.max_bytes is null: the cap must be greater than 0, and a written null is not the absent key — leave the member out to take the %d-byte default", DefaultBinariesMaxBytes)
		}
		if err := ValidBinariesMaxBytes(out.MaxBytes); err != nil {
			return nil, err
		}
	}
	if err := ValidBinariesAllow(out.Allow); err != nil {
		return nil, err
	}
	return &out, nil
}

// BinariesAllow is the paths a run of this project may add a binary at, and
// nothing when the node admits none — which is what an absent member, a
// written empty block and a project that never published one all answer, and
// what every node written before this member existed answers byte for byte.
//
// It is a copy, so a caller cannot write through it to the policy it read, and
// a nil policy answers nothing rather than panicking: the runner holds a
// policy that may be absent (a project with no published node), and the answer
// there is the same one a project that admits nothing gets.
func (p *Policy) BinariesAllow() []string {
	if p == nil || p.Binaries == nil || len(p.Binaries.Allow) == 0 {
		return nil
	}
	return slices.Clone(p.Binaries.Allow)
}

// BinariesMaxBytes is the cap an admitted binary is held to: what the node
// writes, or DefaultBinariesMaxBytes when the block writes none.
//
// It is a method and not the field for the reason BakesRepos is: absent is not
// zero here, it is the shipped bound, and a reader that took the field would
// hold a project that wrote no cap to a cap of nothing. The zero policy and a
// nil one answer the default too, so a caller with no policy needs no branch.
func (p *Policy) BinariesMaxBytes() int64 {
	if p == nil || p.Binaries == nil || p.Binaries.MaxBytes == 0 {
		return DefaultBinariesMaxBytes
	}
	return p.Binaries.MaxBytes
}
