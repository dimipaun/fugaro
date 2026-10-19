package tf

import (
	"slices"
	"strings"
	"testing"
)

// The identity of the object a removal takes away (design §6 item 5, plan Task
// 6.3): the before view's type, id, name and project. These tests are the
// rule's own review — the encoding, what a before view that identifies nothing
// answers, and the one case the rule exists for: an approval given for one
// object is not an approval of another one a later plan carries at the same
// address.

// removal is a change that removes the object a before view describes.
func removal(typ string, before map[string]any) ResourceChange {
	return ResourceChange{Address: "module.m." + typ + ".this", Mode: "managed", Type: typ,
		ProviderName: "registry.terraform.io/hashicorp/google",
		Change:       Change{Actions: []string{"delete"}, Before: before, After: nil}}
}

// TestRemovalIdentityString pins the encoding the owner's object holds and the
// gate compares: a fixed order, a part the view does not carry left out, and a
// value written as it is.
func TestRemovalIdentityString(t *testing.T) {
	for _, tc := range []struct {
		id   RemovalIdentity
		want string
	}{
		{RemovalIdentity{Type: "google_storage_bucket", Project: "p", Name: "n", ID: "i"}, "type=google_storage_bucket project=p name=n id=i"},
		{RemovalIdentity{Type: "google_storage_bucket", ID: "i"}, "type=google_storage_bucket id=i"},
		{RemovalIdentity{Type: "google_storage_bucket"}, "type=google_storage_bucket"},
		{RemovalIdentity{}, ""},
		// The order is the struct's and not the order a before view happens to
		// hold its keys in: two plans of one object write one entry.
		{RemovalIdentity{ID: "i", Name: "n", Project: "p", Type: "t"}, "type=t project=p name=n id=i"},
		// A value is written as it is, spaces included: the comparison is on
		// the whole string.
		{RemovalIdentity{Type: "t", Name: "a bucket"}, "type=t name=a bucket"},
	} {
		if got := tc.id.String(); got != tc.want {
			t.Errorf("RemovalIdentity%v.String() = %q, want %q", tc.id, got, tc.want)
		}
	}
}

// TestRemovedIdentityReadsTheBeforeView: the identity is the change's type and
// the before view's three identifying attributes, and a value of another type
// is not read as one (a plan is JSON, and a number is not a name).
func TestRemovedIdentityReadsTheBeforeView(t *testing.T) {
	rc := removal("google_storage_bucket", map[string]any{"id": "fmgd-b", "name": "fmgd-b", "project": "proj-1", "location": "us"})
	got := RemovedIdentity(rc)
	want := RemovalIdentity{Type: "google_storage_bucket", Project: "proj-1", Name: "fmgd-b", ID: "fmgd-b"}
	if got != want {
		t.Errorf("RemovedIdentity = %v, want %v", got, want)
	}
	if got.String() != "type=google_storage_bucket project=proj-1 name=fmgd-b id=fmgd-b" {
		t.Errorf("RemovedIdentity.String() = %q", got.String())
	}
	// Another type in the view is not a value of one of the three.
	if other := RemovedIdentity(removal("google_storage_bucket", map[string]any{"id": 7, "name": true})); other.Identifies() {
		t.Errorf("a before view whose id and name are not strings identifies %q, want nothing", other)
	}
	// A create carries no before, and a nil view identifies nothing either.
	if id := RemovedIdentity(ResourceChange{Address: "a.b", Type: "google_storage_bucket", Change: Change{Actions: []string{"create"}}}); id.Identifies() {
		t.Errorf("a create's removed identity is %q, want nothing identifying", id)
	}
}

// TestApprovesBindsAnEntryToTheObject: the entry's own address and identity are
// what approve a removal, and the address alone is not enough any more — the
// case the whole rule exists for. The second removal is the same address
// carrying another object, which is what a re-created resource looks like to a
// plan: the approval of the first must not admit it.
func TestApprovesBindsAnEntryToTheObject(t *testing.T) {
	first := removal("google_storage_bucket", map[string]any{"id": "fmgd-b", "name": "fmgd-b", "project": "proj-1"})
	second := first
	second.Change.Before = map[string]any{"id": "fmgd-b2", "name": "fmgd-b2", "project": "proj-1"}
	entries := []DeleteEntry{ApprovalFor(first)}
	if ok, why := Approves(entries, first); !ok {
		t.Fatalf("the entry of a removal does not approve it: %q", why)
	}
	ok, why := Approves(entries, second)
	if ok {
		t.Fatal("an entry bound to one object approved the removal of another one at the same address")
	}
	if !strings.Contains(why, "another object") || !strings.Contains(why, "fmgd-b2") {
		t.Errorf("the refusal is %q, want it to name the object the plan removes", why)
	}
	// And the same plan with an entry bound to its own object is admitted:
	// the rule refuses the reuse, not the delete.
	if ok, why := Approves([]DeleteEntry{ApprovalFor(second)}, second); !ok {
		t.Errorf("an entry bound to the object the plan removes did not approve it: %q", why)
	}
	// An address the list does not name is refused with no sentence of this
	// function's: the caller has its own words for that.
	if ok, why := Approves(entries, removal("google_apikeys_key", map[string]any{"id": "x"})); ok || why != "" {
		t.Errorf("an unlisted address gives (%v, %q), want (false, \"\")", ok, why)
	}
	if ok, why := Approves(nil, first); ok || why != "" {
		t.Errorf("an empty list gives (%v, %q), want (false, \"\")", ok, why)
	}
}

// TestApprovesRefusesAnEntryThatIdentifiesNothing: an entry with no identity,
// and a plan whose before view shows none of the three attributes, are both
// refused — for the type alone is exactly what an address is, and an address
// is what this rule stops being an approval.
func TestApprovesRefusesAnEntryThatIdentifiesNothing(t *testing.T) {
	rc := removal("google_storage_bucket", map[string]any{"id": "fmgd-b", "name": "fmgd-b"})
	if ok, _ := Approves([]DeleteEntry{{Address: rc.Address}}, rc); ok {
		t.Error("an entry with no identity approved a removal")
	}
	weak := removal("google_storage_bucket", map[string]any{"location": "us"})
	if ok, why := Approves([]DeleteEntry{ApprovalFor(weak)}, weak); ok {
		t.Error("a plan whose before view identifies nothing was approved")
	} else if !strings.Contains(why, "does not identify") {
		t.Errorf("the refusal is %q, want it to say the plan identifies nothing", why)
	}
}

// TestGuardEntriesAdmitsAListedRemovalOfItsOwnObject and the two beside it are
// the guard's own rule, at the item a person reads: a matching entry is
// admitted, an entry bound to another object is refused at the address with
// the reason, and the address alone is no longer an approval.
func TestGuardEntriesAdmitsAListedRemovalOfItsOwnObject(t *testing.T) {
	rc := removal("google_storage_bucket", map[string]any{"id": "fmgd-b", "name": "fmgd-b"})
	p := &Plan{ResourceChanges: []ResourceChange{rc}}
	if err := GuardEntries(p, []DeleteEntry{ApprovalFor(rc)}, ""); err != nil {
		t.Fatalf("a removal its own entry approves is refused: %v", err)
	}
	// The same list on another object at the same address: refused, at the
	// address, with the two objects named.
	other := rc
	other.Change.Before = map[string]any{"id": "fmgd-other", "name": "fmgd-other"}
	err := GuardEntries(&Plan{ResourceChanges: []ResourceChange{other}}, []DeleteEntry{ApprovalFor(rc)}, "")
	if err == nil {
		t.Fatal("the guard admitted another object at a listed address")
	}
	msg := err.Error()
	if !strings.Contains(msg, other.Address) || !strings.Contains(msg, "another object") {
		t.Errorf("the refusal is %q, want it to refuse the address for another object", msg)
	}
	if !strings.Contains(msg, "fmgd-other") || !strings.Contains(msg, "fmgd-b") {
		t.Errorf("the refusal is %q, want both objects in it", msg)
	}
	// No entry at all: Guard's own item, with no reason beside the actions.
	if err := GuardEntries(p, nil, ""); err == nil {
		t.Fatal("the guard admitted a removal no entry names")
	} else if !strings.Contains(err.Error(), rc.Address+" (delete)") {
		t.Errorf("the refusal of an unlisted removal is %q, want the address and its actions", err)
	}
}

// TestListableEntriesIsWhatANewEntryWouldAdmit: the advice a refusal ends with
// may name the owner's list only when the list could clear the refusal, and
// under the entries rule that is a removal no entry approves whose object the
// plan does identify — a mismatched entry included; a moved object, an unknown
// action, and a before view that identifies nothing all not, because no entry
// can be matched against the last one (an entry always carries its type).
func TestListableEntriesIsWhatANewEntryWouldAdmit(t *testing.T) {
	own := removal("google_storage_bucket", map[string]any{"id": "fmgd-b"})
	other := own
	other.Change.Before = map[string]any{"id": "fmgd-other"}
	moved := own
	moved.PreviousAddress = "google_storage_bucket.old"
	weak := removal("google_storage_bucket", map[string]any{"location": "us"})
	unknown := own
	unknown.Change.Actions = []string{"delete", "obliterate"}
	listed := own
	listed.Address = "module.m.google_storage_bucket.listed"

	entries := []DeleteEntry{ApprovalFor(own), ApprovalFor(listed)}
	got := ListableEntries(&Plan{ResourceChanges: []ResourceChange{own, listed, other, moved, weak, unknown}}, entries)
	// weak is not in the answer: its before view identifies nothing, so no
	// entry could clear it, and the advice must not offer its address.
	if want := []string{other.Address}; !slices.Equal(got, want) {
		t.Errorf("ListableEntries = %v, want %v", got, want)
	}
	if ListableEntries(nil, entries) != nil {
		t.Error("ListableEntries of no plan is not empty")
	}
}

// TestTheRefusalsIdentityIsWhatTheOwnerPastes: the sentence an owner acts on
// carries the identity of the object the plan removes, and it carries it as
// the entry's own text — no quotation marks and no other mark of the
// sentence's — because the owner copies it into `fugaro allow-delete`, whose
// argument is ADDRESS=IDENTITY and whose identity half must begin with
// "type=". A quoted identity is rejected there, so this pins the two ends
// against each other: the text printed here is the text the command accepts.
func TestTheRefusalsIdentityIsWhatTheOwnerPastes(t *testing.T) {
	rc := removal("google_storage_bucket", map[string]any{"id": "fmgd-b", "name": "fmgd-b", "project": "proj-1"})
	entries := []DeleteEntry{ApprovalFor(rc)}
	// The mismatched-entry sentence, from Approves itself.
	other := rc
	other.Change.Before = map[string]any{"id": "fmgd-other", "name": "fmgd-other", "project": "proj-1"}
	_, why := Approves(entries, other)
	// And the sentence for an address the list does not name, which is the
	// cover's and the one the owner copies in the ordinary loop.
	unlisted := RemoveIdentityText(other)
	for name, text := range map[string]string{"the mismatched entry": why, "the unlisted address": unlisted} {
		// A double quote is what breaks the paste: the command cuts an
		// argument at its first "=", so a leading quote makes the identity
		// half start with the quote rather than with "type=".
		if strings.Contains(text, `"`) {
			t.Errorf("%s's sentence quotes the identity: %q", name, text)
		}
		// The identity is in it whole, and the identity itself is an argument.
		id := RemovedIdentity(other).String()
		if !strings.Contains(text, id) {
			t.Errorf("%s's sentence does not carry the identity %q:\n%q", name, id, text)
		}
		if !strings.HasPrefix(id, "type=") {
			t.Errorf("the identity %q does not begin with the type the command requires", id)
		}
	}
	// And the entry's own identity is the same text: what one refusal printed
	// is what the next list holds, and what the next plan is compared against.
	if got := ApprovalFor(other).Before; got != RemovedIdentity(other).String() {
		t.Errorf("ApprovalFor = %q, want the identity the refusal printed", got)
	}
}

// TestAnEntryApprovesASameNamedObjectReCreatedAtTheAddress — the residual this
// rule does not close, pinned as behaviour rather than left to be inferred.
// For every managed type bar google_apikeys_key the provider derives the id
// from the object's configured name, so a plan's before view cannot tell an
// object from a re-creation of it under the same name: the entry still
// approves the removal. What the rule closes is the address carrying a
// DIFFERENT object, which the test above proves.
func TestAnEntryApprovesASameNamedObjectReCreatedAtTheAddress(t *testing.T) {
	// A bucket as the provider writes one: id IS the bucket name, so the two
	// views are indistinguishable to this rule.
	original := removal("google_storage_bucket", map[string]any{"id": "fmgd-data", "name": "fmgd-data", "project": "proj-1"})
	recreated := removal("google_storage_bucket", map[string]any{"id": "fmgd-data", "name": "fmgd-data", "project": "proj-1"})
	if ok, why := Approves([]DeleteEntry{ApprovalFor(original)}, recreated); !ok {
		t.Fatalf("a same-named object at the address is refused, so the residual has changed shape: %q", why)
	}
	// A type whose id is a UID is the exception: the id differs across
	// re-creation even when the name does not, and the entry does not survive.
	key := removal("google_apikeys_key", map[string]any{"id": "uid-1", "name": "projects/p/keys/k"})
	rekeyed := removal("google_apikeys_key", map[string]any{"id": "uid-2", "name": "projects/p/keys/k"})
	if ok, _ := Approves([]DeleteEntry{ApprovalFor(key)}, rekeyed); ok {
		t.Error("an entry approved a re-created object whose id differs, which the rule should refuse")
	}
}
