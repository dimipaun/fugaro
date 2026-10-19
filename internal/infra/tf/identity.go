package tf

import (
	"slices"
	"strings"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// An owner's delete list names addresses, and an address is not an object
// (design §6 item 5, plan Task 6.3): the same address can carry one object
// today and another one later, so an approval of "the delete at B" would stay
// a standing permission to delete whatever a later managed plan puts at B.
// What closes it is the before view: terraform writes a destroy or a replace
// with the object it removes in before and null (a destroy) or the replacement
// (a replace) in after, so before is the object's own description at the
// moment of the removal — its type and the identifying attributes it carries.
// An entry of the list therefore holds the address AND that identity, and the
// gate admits a removal only when both match, which is what makes one approval
// one delete (the entry no longer matches once the object it named is gone).

// DeleteEntry is one approval of the owner's delete list: the Terraform
// address the owner approved a delete or a replace at, and the identity of the
// object that was there when they approved it, encoded as RemovalIdentity.String
// writes it.
//
// Both halves are compared, and both by exact string equality: an entry
// approves a removal only when its Address is the change's address and its
// Before is the identity of the object the change removes. Empty is never an
// approval — an entry with no Before approves nothing, whatever its address
// says — so a list that lost its identities refuses every delete rather than
// admitting every object at the addresses it names.
// The two field names are also the owner's object's own (infra/managed's
// DeleteList marshals a slice of these): an entry is one shape wherever it is
// written, so the file the owner approves and the rule the gate applies cannot
// disagree about what an entry is.
type DeleteEntry struct {
	Address string `json:"address"`
	Before  string `json:"before"`
}

// RemovalIdentity is the identity of the object a removal takes away, as the
// plan's before view shows it: the resource type, and whichever of the
// object's own identifying attributes that view carries — its id, its name and
// the project it is in.
//
// The three attributes are the ones terraform's Google provider writes for
// every one of its resources and the ones that make the object the object:
// id is unique per object for the life of that object, so an object re-created
// at the same address after a delete carries another one. A view that carries
// none of them identifies nothing (Identifies), which is the fail-closed
// answer rather than an approval of the type alone.
type RemovalIdentity struct {
	Type    string
	Project string
	Name    string
	ID      string
}

// String is the identity as one string: every part this view carries, in a
// fixed order, as "type=… project=… name=… id=…". It is both what the owner's
// object holds and what the gate compares, and the comparison is exact string
// equality — so a part is written as the plan wrote it, and a part the view
// does not carry is left out rather than written empty (an absent attribute
// and an empty one are different objects).
//
// The string is for comparing and for one person to read; it is not a
// canonical form of anything else, and nothing parses it back into parts. A
// value that itself holds a space is written as it is: the comparison is on
// the whole string, so an ambiguous-looking value is still an exact one.
func (i RemovalIdentity) String() string {
	parts := make([]string, 0, 4)
	for _, p := range []struct{ k, v string }{
		{"type", i.Type}, {"project", i.Project}, {"name", i.Name}, {"id", i.ID},
	} {
		if p.v != "" {
			parts = append(parts, p.k+"="+p.v)
		}
	}
	return strings.Join(parts, " ")
}

// Identifies reports whether the identity names an object rather than only its
// type: one of id, name and project must be there. A type alone does not
// identify anything — two objects of one type at one address are exactly what
// an address-only approval cannot tell apart — so a removal whose before view
// shows none of the three is never approved (Approves).
func (i RemovalIdentity) Identifies() bool {
	return i.ID != "" || i.Name != "" || i.Project != ""
}

// RemovedIdentity is the identity of the object rc removes: the change's
// resource type and its before view's id, name and project. A change that
// removes nothing still answers — a create's before is null — and the answer
// then identifies nothing, which is what Approves refuses.
func RemovedIdentity(rc ResourceChange) RemovalIdentity {
	b := rc.Change.Before
	return RemovalIdentity{
		Type:    rc.Type,
		Project: str(b["project"]),
		Name:    str(b["name"]),
		ID:      str(b["id"]),
	}
}

// ApprovalFor is the entry that approves rc's own removal: its address and the
// identity of the object the plan shows there. It is what the owner's object
// holds for a removal the owner approved, and what a test writes when it means
// "this removal is approved" — computing it rather than spelling it out, so a
// fixture cannot approve an object the plan does not show.
func ApprovalFor(rc ResourceChange) DeleteEntry {
	return DeleteEntry{Address: rc.Address, Before: RemovedIdentity(rc).String()}
}

// Approves reports whether entries approve the removal rc makes, and, when
// they do not, the sentence that says why (empty when approved). It is the one
// decision both halves of the managed gate take — tf.GuardEntries for the
// address half and the managed cover for the resource half — so the two cannot
// be told different rules about what an entry admits.
//
// The removal is approved only when an entry names its address and that
// entry's Before is the identity of the object the plan removes. Anything
// else is a refusal, and the sentence is which of the two the list itself
// makes: an entry at the address bound to another object (the case this rule
// exists for — an approval that survived the delete it was given for, or a
// plan whose object was replaced under the address), or a plan whose before
// view shows no identifying attribute, which no entry can be matched against
// and which is therefore refused rather than approved for the type alone.
//
// No entry at the address is the third answer and it is a refusal too, but it
// is not this function's sentence: an empty why with false is "the list does
// not name this address", which is the refusal Guard has always written for
// it and the cover writes about the resource. The caller's own words say it.
//
// The sentence names the identity the plan shows, because the person who can
// change the answer is the owner, and what they need is the object the plan
// removes: it is what fugaro allow-delete records, and printing it is what
// makes that loop possible at all. Every value in it that the plan or the
// object wrote is one bounded field (safetext.Field): the sentence is printed,
// and a plan is a document someone else's session may have written.
func Approves(entries []DeleteEntry, rc ResourceChange) (bool, string) {
	id := RemovedIdentity(rc)
	listed := slices.ContainsFunc(entries, func(e DeleteEntry) bool { return e.Address == rc.Address })
	if !listed {
		return false, ""
	}
	if !id.Identifies() {
		return false, "the plan does not identify the object this change removes (no id, name or project in its before view), so the owner's delete list cannot be matched against it"
	}
	// Every entry at the address is tried, in order: a list holding one
	// address twice with two identities approves the removal whose object is
	// one of them, and needs no other rule to say what that means.
	for _, e := range entries {
		if e.Address == rc.Address && e.Before == id.String() {
			return true, ""
		}
	}
	// The identity of the entry back, so the two objects the refusal is about
	// are both in it: what the owner approved, and what the plan removes.
	var bound []string
	for _, e := range entries {
		if e.Address == rc.Address {
			bound = append(bound, safetext.Field(e.Before))
		}
	}
	held := strings.Join(bound, "\", \"")
	if held == "" {
		held = "no identity at all"
	}
	return false, "the owner's delete list approves this address for another object (the list holds \"" + held + "\", and the plan removes " + identityText(id) + ")"
}

// identityText is a RemovalIdentity as a sentence names it: the identity
// itself, or, for a view that identifies nothing, the reason it does not.
func identityText(id RemovalIdentity) string {
	if s := safetext.Field(id.String()); s != "" && id.Identifies() {
		return "\"" + s + "\""
	}
	return "an object the plan does not identify (no id, name or project in its before view)"
}

// RemoveIdentityText is the identity of the object a removal takes away, as
// the refusal a person reads names it, for the sentences about a removal whose
// list approval is not the rule that refused it — the cover's own item for an
// address the owner's list does not name. It is what the owner copies into
// fugaro allow-delete, so it is the identity itself and not a description of
// it, and the reason when the plan identifies nothing.
func RemoveIdentityText(rc ResourceChange) string { return identityText(RemovedIdentity(rc)) }
