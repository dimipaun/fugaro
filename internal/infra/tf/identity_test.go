package tf

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/safetext"
)

// The identity of the object a removal takes away (design §6 item 5, plan Task
// 6.3): the before view's type, id, name and project, plus the per-instance
// value the type carries where it has one. These tests are the rule's own
// review — the encoding and its injectivity, what a before view that
// identifies nothing answers, the one case the rule exists for (an approval
// given for one object is not an approval of another one a later plan carries
// at the same address), and the residual it does not close, pinned per type
// and in both directions.

// removal is a change that removes the object a before view describes. The
// view is the caller's own and is written out whole: a fixture that left a
// type's per-instance attribute out would be a state terraform does not write,
// and one that means "this object was removed" means the state terraform
// writes (TestRemovedIdentityBindsThePerInstanceAttribute is the rule about
// what it carries).
func removal(typ string, before map[string]any) ResourceChange {
	return ResourceChange{Address: "module.m." + typ + ".this", Mode: "managed", Type: typ,
		ProviderName: "registry.terraform.io/hashicorp/google",
		Change:       Change{Actions: []string{"delete"}, Before: before, After: nil}}
}

// bucketBefore is a bucket's before view as the provider writes one: the
// name-derived id and name, the project, and the per-instance time_created
// every state of a bucket carries.
func bucketBefore(name, created string) map[string]any {
	return map[string]any{"id": name, "name": name, "project": "proj-1", "time_created": created}
}

// TestRemovalIdentityString pins the encoding the owner's object holds and the
// gate compares: a fixed order, a part the view does not carry left out, a
// value written as it is, and the two characters that would make two
// identities write one string escaped.
func TestRemovalIdentityString(t *testing.T) {
	for _, tc := range []struct {
		id   RemovalIdentity
		want string
	}{
		{RemovalIdentity{Type: "google_storage_bucket", Project: "p", Name: "n", ID: "i", Instance: "2026-01-02T03:04:05Z"},
			"type=google_storage_bucket project=p name=n id=i instance=2026-01-02T03:04:05Z"},
		// A type with no per-instance attribute writes none, and one that is
		// there is written last, after the id.
		{RemovalIdentity{Type: "google_cloud_scheduler_job", ID: "i"}, "type=google_cloud_scheduler_job id=i"},
		{RemovalIdentity{Type: "google_cloud_scheduler_job"}, "type=google_cloud_scheduler_job"},
		{RemovalIdentity{}, ""},
		// The order is the struct's and not the order a before view happens to
		// hold its keys in: two plans of one object write one entry.
		{RemovalIdentity{ID: "i", Name: "n", Project: "p", Type: "t"}, "type=t project=p name=n id=i"},
		// A value is written as it is, except for the two characters the
		// encoding uses: a space separates the parts and a backslash escapes,
		// so a value holding either is escaped (identityPart) rather than
		// written in a way another value could imitate. A value's space is
		// escaped to \x20 and not to a backslash-and-space pair: the printers
		// rebuild a line with strings.Fields, which would read that pair plus
		// the separator beside it as one space (identityValue).
		{RemovalIdentity{Type: "t", Name: "a bucket"}, `type=t name=a\x20bucket`},
		{RemovalIdentity{Type: "t", Name: `a\b`}, `type=t name=a\\b`},
		{RemovalIdentity{Type: "t", Name: `a \ b`}, `type=t name=a\x20\\\x20b`},
		// A parenthesis is escaped although it has a printable form: every
		// refusal prints an identity inside a sentence's own parenthetical, so
		// a value's ")" written as itself would sit beside the sentence's and
		// the owner copying the text out could not tell the two apart
		// (identityValue).
		{RemovalIdentity{Type: "t", Name: "a(b)"}, `type=t name=a\x28b\x29`},
		{RemovalIdentity{Type: "t", Name: "b)"}, `type=t name=b\x29`},
	} {
		if got := tc.id.String(); got != tc.want {
			t.Errorf("RemovalIdentity%v.String() = %q, want %q", tc.id, got, tc.want)
		}
	}
}

// oldIdentityString is the encoding this rule wrote before it was made
// injective: every part as "key=value", with a value written as it was and the
// parts joined by a space. It is here for the regression below alone, which
// fails on it: the collision it has is the reason the encoding changed, and a
// test that only asserted the new form would keep passing if the escaping were
// removed again for a value that happens to read the same way.
func oldIdentityString(i RemovalIdentity) string {
	parts := make([]string, 0, 5)
	for _, p := range []struct{ k, v string }{
		{"type", i.Type}, {"project", i.Project}, {"name", i.Name}, {"id", i.ID}, {"instance", i.Instance},
	} {
		if p.v != "" {
			parts = append(parts, p.k+"="+p.v)
		}
	}
	return strings.Join(parts, " ")
}

// TestRemovalIdentityStringIsInjective: two identities that differ in any part
// write different strings, so an entry approved for one object matches one
// object and no other. The first case is the collision the old encoding has —
// the name "a id=b" against the pair of a name "a" with an id "b", one string
// for two objects — and every part is covered, the per-instance value among
// them.
func TestRemovalIdentityStringIsInjective(t *testing.T) {
	collision := []RemovalIdentity{
		{Type: "t", Name: "a id=b"},
		{Type: "t", Name: "a", ID: "b"},
	}
	if oldIdentityString(collision[0]) != oldIdentityString(collision[1]) {
		t.Fatalf("the two fixtures no longer collide under the old encoding (%q, %q), so this test proves nothing",
			oldIdentityString(collision[0]), oldIdentityString(collision[1]))
	}
	for _, tc := range []struct {
		a, b RemovalIdentity
		what string
	}{
		{collision[0], collision[1], "a name holding a space against a name and an id"},
		{RemovalIdentity{Type: "t", Project: "p q", Name: "n"}, RemovalIdentity{Type: "t", Project: "p", Name: "q n"}, "a project holding a space against the part after it"},
		{RemovalIdentity{Type: "t", Name: "n", Instance: "a id=b"}, RemovalIdentity{Type: "t", Name: "n", ID: "a id=b"}, "a per-instance value against an id of the same text"},
		{RemovalIdentity{Type: "t", Name: `a\ b`}, RemovalIdentity{Type: "t", Name: `a\`, ID: "b"}, "a backslash against the escape that spells it"},
		{RemovalIdentity{Type: "t", ID: "i"}, RemovalIdentity{Type: "t", Instance: "i"}, "an id against a per-instance value"},
	} {
		if tc.a.String() == tc.b.String() {
			t.Errorf("%s write one string (%q) for two identities: an entry approved for one would match the other", tc.what, tc.a.String())
		}
	}
	// Every part of the identity is escaped, not only some: a sweep of values
	// that hold the encoding's own characters writes as many strings as it has
	// values.
	seen := map[string]string{}
	for _, name := range []string{"a", "a b", `a\b`, `a\ b`, `a\\b`, `a b c`, ` `, `\`, `\ `} {
		id := RemovalIdentity{Type: "t", Name: name}
		if prev, dup := seen[id.String()]; dup {
			t.Errorf("the names %q and %q write one string (%q)", prev, name, id.String())
		}
		seen[id.String()] = name
	}
}

// TestRemovalInstanceAttributesDecideEveryManagedType: every type of the
// managed allowlist is decided by one of the two maps and by only one — it
// either carries a per-instance value the identity binds an entry to, or it
// has a recorded reason it has none. A type added to managedTypes fails this
// test until it is decided, so leaving one out is a decision and not an
// omission, and neither map may carry a type the allowlist does not admit.
func TestRemovalInstanceAttributesDecideEveryManagedType(t *testing.T) {
	for _, typ := range ManagedAllowedTypes() {
		attr, bound := removalInstanceAttributes[typ]
		why, none := removalNoInstanceAttribute[typ]
		switch {
		case bound && none:
			t.Errorf("%s is in both maps: it either has a per-instance attribute or a reason it has none, not both", typ)
		case bound && attr == "":
			t.Errorf("%s is in removalInstanceAttributes with no attribute named", typ)
		case none && strings.TrimSpace(why) == "":
			t.Errorf("%s is in removalNoInstanceAttribute with no reason it has none", typ)
		case !bound && !none:
			t.Errorf("%s is on the managed allowlist and neither map decides it: name the attribute that tells one instance of it from the next, or say why none does", typ)
		}
	}
	for typ := range removalInstanceAttributes {
		if !managedTypes[typ] {
			t.Errorf("removalInstanceAttributes binds %s, which the managed allowlist does not admit: a type no managed plan may contain has no removal to bind", typ)
		}
	}
	for typ := range removalNoInstanceAttribute {
		if !managedTypes[typ] {
			t.Errorf("removalNoInstanceAttribute decides %s, which the managed allowlist does not admit", typ)
		}
	}
}

// TestRemovedIdentityBindsThePerInstanceAttribute: for every type whose before
// view carries a per-instance value, the identity reads it out of the view's
// own attribute — the entry the refusal prints and the gate compares — and
// that value is what tells one instance of a name from the next. A view of
// such a type that does not carry it identifies nothing, which is the
// fail-closed answer.
func TestRemovedIdentityBindsThePerInstanceAttribute(t *testing.T) {
	for _, tc := range []struct {
		typ, attr string
		one, next any
		want      string
	}{
		{"google_storage_bucket", "time_created", "2026-01-02T03:04:05.678Z", "2026-06-07T08:09:10.111Z", "instance=2026-01-02T03:04:05.678Z"},
		// A bucket object's generation is a NUMBER in terraform's JSON, and it
		// is read as the decimal the plan wrote.
		{"google_storage_bucket_object", "generation", float64(1758000000000000), float64(1758000000000001), "instance=1758000000000000"},
		{"google_service_account", "unique_id", "104329501182774832335", "107711663860607879222", "instance=104329501182774832335"},
		{"google_cloud_run_v2_job", "uid", "2f9a0d1e-1a1b-4c1d-9e0f-1a2b3c4d5e6f", "8c7b6a5d-4e3f-2a1b-0c9d-8e7f6a5b4c3d", "instance=2f9a0d1e-1a1b-4c1d-9e0f-1a2b3c4d5e6f"},
		// google_apikeys_key is one of these types and not an exception: its
		// provider id is projects/P/locations/global/keys/NAME, derived from
		// the key's name (the fixture below is that shape), and the value
		// that differs across a re-creation is uid.
		{"google_apikeys_key", "uid", "6b1c5e2a-9d3f-4a7b-8c1d-2e3f4a5b6c7d", "0a9b8c7d-6e5f-4a3b-2c1d-0e9f8a7b6c5d", "instance=6b1c5e2a-9d3f-4a7b-8c1d-2e3f4a5b6c7d"},
		{"google_secret_manager_secret", "create_time", "2026-01-02T03:04:05.678Z", "2026-06-07T08:09:10.111Z", "instance=2026-01-02T03:04:05.678Z"},
		{"google_artifact_registry_repository", "create_time", "2026-01-02T03:04:05.678Z", "2026-06-07T08:09:10.111Z", "instance=2026-01-02T03:04:05.678Z"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			one := removal(tc.typ, map[string]any{"id": "fmgd-x", "name": "fmgd-x", "project": "proj-1", tc.attr: tc.one})
			id := RemovedIdentity(one)
			if !id.Identifies() {
				t.Fatalf("the before view of a %s carrying its %s identifies nothing: %v", tc.typ, tc.attr, id)
			}
			if !strings.Contains(id.String(), tc.want) {
				t.Errorf("the identity of a %s is %q, want it to carry %q", tc.typ, id.String(), tc.want)
			}
			// The same name, the same name-derived id, the next instance:
			// refused, with the two identities in the sentence.
			next := removal(tc.typ, map[string]any{"id": "fmgd-x", "name": "fmgd-x", "project": "proj-1", tc.attr: tc.next})
			ok, why := Approves([]DeleteEntry{ApprovalFor(one)}, next)
			if ok {
				t.Errorf("an entry approved a re-created %s at the same address: its %s differs, so the two are different objects", tc.typ, tc.attr)
			}
			if !strings.Contains(why, "another object") {
				t.Errorf("the refusal of a re-created %s is %q, want it to say the entry is about another object", tc.typ, why)
			}
			// A before view of that type without the value: nothing identified,
			// and the refusal says which value is missing rather than claiming
			// the view carries no name.
			bare := removal(tc.typ, map[string]any{"id": "fmgd-x", "name": "fmgd-x", "project": "proj-1"})
			if RemovedIdentity(bare).Identifies() {
				t.Errorf("a before view of a %s that carries no %s identified an object", tc.typ, tc.attr)
			}
			ok, why = Approves([]DeleteEntry{ApprovalFor(bare)}, bare)
			if ok {
				t.Fatalf("a removal whose before view carries no %s was approved", tc.attr)
			}
			if !strings.Contains(why, tc.attr) {
				t.Errorf("the refusal is %q, want it to name the missing %s", why, tc.attr)
			}
			// And the value in another JSON shape is not read as one.
			wrong := removal(tc.typ, map[string]any{"id": "fmgd-x", "name": "fmgd-x", "project": "proj-1", tc.attr: map[string]any{"not": "a value"}})
			if RemovedIdentity(wrong).Identifies() {
				t.Errorf("a %s whose %s is an object identified an object", tc.typ, tc.attr)
			}
		})
	}
}

// TestRemovedIdentityReadsTheBeforeView: the identity is the change's type and
// the before view's three name-derived attributes, and a value of another type
// is not read as one (a plan is JSON, and a number is not a name).
func TestRemovedIdentityReadsTheBeforeView(t *testing.T) {
	rc := removal("google_storage_bucket",
		map[string]any{"id": "fmgd-b", "name": "fmgd-b", "project": "proj-1", "location": "us", "time_created": "2026-01-02T03:04:05Z"})
	got := RemovedIdentity(rc)
	want := RemovalIdentity{Type: "google_storage_bucket", Project: "proj-1", Name: "fmgd-b", ID: "fmgd-b", Instance: "2026-01-02T03:04:05Z"}
	if got != want {
		t.Errorf("RemovedIdentity = %v, want %v", got, want)
	}
	if got.String() != "type=google_storage_bucket project=proj-1 name=fmgd-b id=fmgd-b instance=2026-01-02T03:04:05Z" {
		t.Errorf("RemovedIdentity.String() = %q", got.String())
	}
	// A type that carries no per-instance value reads none, whatever the view
	// holds beside the three.
	job := removal("google_cloud_scheduler_job", map[string]any{"id": "projects/p/locations/us-east5/jobs/fmgd-sweep", "name": "fmgd-sweep", "project": "p"})
	if want := (RemovalIdentity{Type: "google_cloud_scheduler_job", Project: "p", Name: "fmgd-sweep", ID: "projects/p/locations/us-east5/jobs/fmgd-sweep"}); RemovedIdentity(job) != want {
		t.Errorf("RemovedIdentity of a scheduler job = %v, want %v", RemovedIdentity(job), want)
	}
	// Another type in the view is not a value of one of the three.
	if other := RemovedIdentity(removal("google_cloud_scheduler_job", map[string]any{"id": 7, "name": true})); other.Identifies() {
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
	first := removal("google_storage_bucket", bucketBefore("fmgd-b", "2026-01-02T03:04:05Z"))
	second := removal("google_storage_bucket", bucketBefore("fmgd-b2", "2026-06-07T08:09:10Z"))
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
	if ok, why := Approves(entries, removal("google_apikeys_key", map[string]any{"id": "x", "uid": "u"})); ok || why != "" {
		t.Errorf("an unlisted address gives (%v, %q), want (false, \"\")", ok, why)
	}
	if ok, why := Approves(nil, first); ok || why != "" {
		t.Errorf("an empty list gives (%v, %q), want (false, \"\")", ok, why)
	}
}

// TestApprovesRefusesAnEntryThatIdentifiesNothing: an entry with no identity,
// and a plan whose before view identifies nothing, are both refused — for the
// type alone is exactly what an address is, and an address is what this rule
// stops being an approval.
func TestApprovesRefusesAnEntryThatIdentifiesNothing(t *testing.T) {
	rc := removal("google_storage_bucket", bucketBefore("fmgd-b", "2026-01-02T03:04:05Z"))
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
	rc := removal("google_storage_bucket", bucketBefore("fmgd-b", "2026-01-02T03:04:05Z"))
	p := &Plan{ResourceChanges: []ResourceChange{rc}}
	if err := GuardEntries(p, []DeleteEntry{ApprovalFor(rc)}, ""); err != nil {
		t.Fatalf("a removal its own entry approves is refused: %v", err)
	}
	// The same list on another object at the same address: refused, at the
	// address, with the two objects named.
	other := removal("google_storage_bucket", bucketBefore("fmgd-other", "2026-06-07T08:09:10Z"))
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
// can be matched against the last one (an entry always carries its type, or
// the per-instance value its type needs).
func TestListableEntriesIsWhatANewEntryWouldAdmit(t *testing.T) {
	own := removal("google_storage_bucket", bucketBefore("fmgd-b", "2026-01-02T03:04:05Z"))
	other := removal("google_storage_bucket", bucketBefore("fmgd-other", "2026-06-07T08:09:10Z"))
	moved := own
	moved.PreviousAddress = "google_storage_bucket.old"
	// A view with the name but not the per-instance value: nothing for an
	// entry to be matched against.
	weak := removal("google_storage_bucket", map[string]any{"name": "fmgd-b", "id": "fmgd-b"})
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
	rc := removal("google_storage_bucket", bucketBefore("fmgd-b", "2026-01-02T03:04:05Z"))
	entries := []DeleteEntry{ApprovalFor(rc)}
	// The mismatched-entry sentence, from Approves itself.
	other := removal("google_storage_bucket", bucketBefore("fmgd-other", "2026-06-07T08:09:10Z"))
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
// rule does not close, pinned as behaviour rather than left to be inferred,
// and pinned per type: it holds for the managed types whose before view
// carries no per-instance value (removalNoInstanceAttribute), and NOT for the
// seven that carry one. Both directions are here, because what the residual
// claims is exactly "these types and no others": a test of the approved
// direction alone would keep passing if the binding were dropped for a type
// that has one.
func TestAnEntryApprovesASameNamedObjectReCreatedAtTheAddress(t *testing.T) {
	// A scheduler job is one of the types with none: its id is derived from
	// the name and the region, it carries no output-only value of its own, so
	// two views of the same job are the same identity and the entry survives
	// its re-creation.
	original := removal("google_cloud_scheduler_job",
		map[string]any{"id": "projects/proj-1/locations/us-east5/jobs/fmgd-sweep", "name": "fmgd-sweep", "project": "proj-1"})
	recreated := removal("google_cloud_scheduler_job",
		map[string]any{"id": "projects/proj-1/locations/us-east5/jobs/fmgd-sweep", "name": "fmgd-sweep", "project": "proj-1"})
	if ok, why := Approves([]DeleteEntry{ApprovalFor(original)}, recreated); !ok {
		t.Fatalf("a same-named scheduler job at the address is refused, so the residual has changed shape: %q", why)
	}
	// A bucket is NOT one of them: the same name and the same name-derived id,
	// created again, carries a different time_created and the entry does not
	// approve the removal.
	first := removal("google_storage_bucket", bucketBefore("fmgd-data", "2026-01-02T03:04:05Z"))
	again := removal("google_storage_bucket", bucketBefore("fmgd-data", "2026-06-07T08:09:10Z"))
	if ok, _ := Approves([]DeleteEntry{ApprovalFor(first)}, again); ok {
		t.Error("an entry approved a re-created bucket whose time_created differs, which the rule should refuse")
	}
}

// TestTheAPIKeyIdentityBindsItsUIDAndNotItsNameDerivedID: google_apikeys_key
// was recorded as the exception to the id rule — the one managed type whose
// provider id is not the name — and that was wrong. The provider writes
// projects/P/locations/global/keys/NAME as the id (the shape the fixture
// below has, and the shape the plan's before view carries), so the id names
// the key and the value that differs across a re-creation is uid. This test
// pins both halves, so neither the old claim nor a fixture that spelled the id
// as a uid can come back.
func TestTheAPIKeyIdentityBindsItsUIDAndNotItsNameDerivedID(t *testing.T) {
	key := func(uid string) ResourceChange {
		return removal("google_apikeys_key", map[string]any{
			"id": "projects/proj-1/locations/global/keys/fmgd-web", "name": "fmgd-web", "project": "proj-1", "uid": uid})
	}
	id := RemovedIdentity(key("6b1c5e2a-9d3f-4a7b-8c1d-2e3f4a5b6c7d"))
	if id.ID != "projects/proj-1/locations/global/keys/fmgd-web" {
		t.Errorf("the fixture's id is %q, want the provider's name-derived shape (projects/P/locations/global/keys/NAME)", id.ID)
	}
	if id.Instance != "6b1c5e2a-9d3f-4a7b-8c1d-2e3f4a5b6c7d" {
		t.Errorf("the api key's per-instance value is %q, want its uid", id.Instance)
	}
	if ok, _ := Approves([]DeleteEntry{ApprovalFor(key("6b1c5e2a-9d3f-4a7b-8c1d-2e3f4a5b6c7d"))}, key("0a9b8c7d-6e5f-4a3b-2c1d-0e9f8a7b6c5d")); ok {
		t.Error("an entry approved a re-created API key whose uid differs")
	}
}

// TestThePrintedIdentityIsTheIdentityAnEntryHolds: the loop a refusal starts —
// refusal → the owner copies the identity → fugaro allow-delete → the next
// plan passes — closes only when the text a sentence prints is BYTE FOR BYTE
// the text the gate compares, and the sentences this program writes do not
// print a value raw: safetext.Field bounds and rewrites one, safetext.MultiLine
// (what cli.ErrorText prints a refusal through) rewrites one, and either would
// turn an identity holding a zero-width character, a bidi override, a
// non-breaking space, a tab or an invalid byte into a different text. So the
// identity is escaped by its own encoder until every one of those transforms is
// a no-op on it, and every one of them is checked here: what a refusal prints
// for such an object is what an entry holds, and an entry holding the printed
// text approves the removal.
func TestThePrintedIdentityIsTheIdentityAnEntryHolds(t *testing.T) {
	zwsp, rlo, nbsp, line := string(rune(0x200b)), string(rune(0x202e)), string(rune(0x00a0)), string(rune(0x2028))
	for _, tc := range []struct{ what, name string }{
		{"a zero-width space", "fmgd-" + zwsp + "data"},
		{"a bidi override", "fmgd-" + rlo + "data"},
		{"a non-breaking space", "fmgd" + nbsp + "data"},
		{"a tab", "fmgd\tdata"},
		{"a line separator", "fmgd" + line + "data"},
		{"a carriage return", "fmgd\rdata"},
		{"an invalid byte", "fmgd\xffdata"},
		{"an escape sequence", "fmgd\x1b[2Jdata"},
		{"a space", "fmgd data"},
		{"a name that ends in a space", "fmgd-data "},
		{"a name that starts with a space", " fmgd-data"},
		{"a name of spaces", "   "},
		{"a long name", "fmgd-" + strings.Repeat("x", 300)},
	} {
		t.Run(tc.what, func(t *testing.T) {
			rc := removal("google_storage_bucket", bucketBefore(tc.name, "2026-01-02T03:04:05Z"))
			id := RemovedIdentity(rc)
			if !id.Identifies() {
				t.Fatalf("a bucket named %q identifies nothing: %s", tc.name, id.notIdentified())
			}
			text := id.String()
			// The transforms this program prints a value through, and the
			// identity survives each one unchanged. MultiLine is what the
			// command's own top-level error line applies to a refusal, and it
			// neither rewrites nor cuts; Field is the bound every refusal item
			// and every listing line goes through, and it leaves the identity
			// alone entirely — rewrites as well as length — for every identity
			// up to its own bound; past that its CUT is the one thing it must
			// not be allowed to do, and the identity is printed through
			// FieldUncut instead (identityText), which rewrites nothing either.
			if got := safetext.MultiLine(text); got != text {
				t.Errorf("safetext.MultiLine rewrites the printed identity:\n got %q\nwant %q", got, text)
			}
			printer := safetext.Field
			if len([]rune(text)) > safetext.FieldMax {
				printer = safetext.FieldUncut
			}
			if got := printer(text); got != text {
				t.Errorf("the printer of a refusal rewrites the printed identity:\n got %q\nwant %q", got, text)
			}
			for _, printed := range []string{RemoveIdentityText(rc), identityText(id)} {
				if printed != text {
					t.Errorf("the sentence prints %q, want the identity itself (%q)", printed, text)
				}
			}
			// And the paste the loop describes: the entry an owner records from
			// what the refusal printed approves the removal the refusal was
			// about.
			pasted := DeleteEntry{Address: rc.Address, Before: RemoveIdentityText(rc)}
			if ok, why := Approves([]DeleteEntry{pasted}, rc); !ok {
				t.Errorf("an entry holding the identity the refusal printed does not approve the removal: %s", why)
			}
		})
	}
}

// TestAnIdentityIsOnePrintableLineWithSingleSpaces is the invariant the two
// ends of the loop rest on, asserted over every position a space can sit in a
// part: an identity this encoder writes holds printable ASCII, has no space
// but the ones between its parts, two of them never in a row, and none at
// either end, and it holds NO parenthesis.
//
// Each clause is a printer or a reader of this program. safetext rebuilds a
// line with strings.Fields (safetext.fieldRewrite), which collapses a run of
// whitespace into one space and drops it at either end — so a value whose own
// space met the space between two parts would come out of a refusal one space
// shorter than the entry the gate compares, and a value ending in a space
// would lose it entirely (the escape written as "\ " was exactly that bug).
// fugaro allow-delete trims the argument it is handed (approveArgument), and
// it may: what the trim removes is the whitespace a paste added, never a byte
// of an identity, because no identity begins or ends with one. A TERMINAL
// takes what is printable ASCII as it is. Every value here is a value the
// representation would break on if the escaping did not cover it.
//
// No parenthesis, because every refusal prints an identity inside a sentence of
// its own — "…(the plan removes type=… id=…)" — and the owner copies that text
// out of the refusal: the last character of the copy is the sentence's own ")"
// and not the object's, which is only true if no identity can hold one
// (identityValue, and internal/cli's approveArgument, which takes that one
// bracket off a paste and refuses an argument that still holds one).
func TestAnIdentityIsOnePrintableLineWithSingleSpaces(t *testing.T) {
	values := []string{
		"", "a", " a", "a ", " ", "   ", "a b", "a  b", "\ta", "a\t", "a\n", "\r", "\\", "a\\ b", "\\ ", "a\\b",
		"(", ")", "(a)", "a)", "b(a", "((", "))",
	}
	for _, v := range values {
		ids := []RemovalIdentity{
			{Type: "t", Project: v, Name: v, ID: v, Instance: v},
			{Type: v, Project: v, Name: v, ID: v},
			{Type: "t", Project: v, Name: v, ID: v}, // a type with no per-instance part: the id is written last
			{Type: "t", Name: v},
			{Type: v},
		}
		for _, id := range ids {
			text := id.String()
			if got := safetext.FieldUncut(text); got != text {
				t.Errorf("FieldUncut(%q) = %q, want the identity unchanged", text, got)
			}
			if got := safetext.MultiLine(text); got != text {
				t.Errorf("MultiLine(%q) = %q, want the identity unchanged", text, got)
			}
			if trimmed := strings.TrimSpace(text); trimmed != text {
				t.Errorf("the command's trim would cut %q to %q", text, trimmed)
			}
			if strings.Contains(text, "  ") {
				t.Errorf("the identity %q holds two spaces in a row, which a printer rebuilds as one", text)
			}
			if strings.ContainsAny(text, "()") {
				t.Errorf("the identity %q holds a parenthesis, which is the punctuation of the sentence a refusal prints it in", text)
			}
			for _, r := range text {
				if r != ' ' && (r < 0x21 || r > 0x7e) {
					t.Errorf("the identity %q holds %U, which no printer of this program leaves alone", text, r)
				}
			}
		}
	}
}

// printedTail is what a mismatch refusal ends by naming, copied out of the
// sentence the way its reader copies it: everything after the sentence's own
// words, the closing bracket of its parenthetical included. It is the literal
// copy and not the identity reconstructed from the fixture, because the loop
// the delete list works by is textual — what the reader ends up with is what
// this returns, and the sentence's bracket is part of it.
//
// It is a TAIL and not an identity for that reason: the bracket at its end
// belongs to the sentence, which is what makes it safe for the command that
// records a paste to take that one bracket off (internal/cli's
// approveArgument), and what an identity must hold no parenthesis for.
func printedTail(t *testing.T, why string) string {
	t.Helper()
	const mark = "and the plan removes "
	i := strings.LastIndex(why, mark)
	if i < 0 {
		t.Fatalf("the refusal does not name the object the plan removes: %q", why)
	}
	return why[i+len(mark):]
}

// TestTheMismatchRefusalNamesAnIdentityAnEntryCanHold: the same contract for
// the other sentence that prints an identity — the one an entry at the address
// bound to another object draws (Approves) — and the copy loop run on it: what
// its reader takes off the end of that sentence is an identity fugaro
// allow-delete can record and the gate then approves.
func TestTheMismatchRefusalNamesAnIdentityAnEntryCanHold(t *testing.T) {
	plan := removal("google_storage_bucket", bucketBefore("fmgd-data", "2026-01-02T03:04:05Z"))
	other := removal("google_storage_bucket", bucketBefore("fgmd"+string(rune(0x200b))+"data", "2026-06-07T08:09:10Z"))
	ok, why := Approves([]DeleteEntry{ApprovalFor(other)}, plan)
	if ok {
		t.Fatal("an entry for another object approved the removal")
	}
	printed := printedTail(t, why)
	want := RemovedIdentity(plan).String()
	// The copy is the identity and then the sentence's own closing bracket, and
	// nothing else: the owner who copies the printed text arrives at the
	// command with exactly this, and one bracket is what that command takes off
	// (TestAllowDeleteCommandRecordsTheIdentityARefusalPrints, which runs the
	// paste this fixture describes). A tail holding anything more — the
	// fixture's own text, a second bracket — would be a text the command had to
	// read by a rule of its own rather than by the identity's.
	if printed != want+")" {
		t.Errorf("the sentence ends %q, want the identity %q followed by the sentence's own \")\"", printed, want)
	}
	if ok, why := Approves([]DeleteEntry{{Address: plan.Address, Before: strings.TrimSuffix(printed, ")")}}, plan); !ok {
		t.Errorf("the identity copied out of the refusal does not approve the removal: %s", why)
	}
}

// TestAnIdentityTooLongToPrintIsRefused: the escaping makes the identity safe
// to print, and it cannot make it short. A refusal's own lines are bounded, so
// an identity longer than the bound cannot be printed whole — and a printed
// identity that is a PREFIX of the real one is worse than none at all: the
// owner would record it, the entry would match nothing, and every later apply
// would refuse the same removal with the same sentence. So the rule refuses
// such a removal rather than printing part of its identity, which is the same
// fail-closed direction as the rest of this gate, and the two sides of the
// bound are pinned here.
func TestAnIdentityTooLongToPrintIsRefused(t *testing.T) {
	// "type=t name=" is the whole identity of the empty name, so the name that
	// fills the bound exactly is this long.
	const prefix = "type=t name="
	at := RemovalIdentity{Type: "t", Name: strings.Repeat("x", RemovalIdentityPrintMax-len(prefix))}
	if n := len(at.String()); n != RemovalIdentityPrintMax {
		t.Fatalf("the fixture identity is %d characters, want the bound (%d)", n, RemovalIdentityPrintMax)
	}
	if !at.Identifies() {
		t.Error("an identity of exactly the bound does not identify an object")
	}
	straddling := RemovalIdentity{Type: "t", Name: strings.Repeat("x", RemovalIdentityPrintMax-len(prefix)+1)}
	if straddling.Identifies() {
		t.Errorf("an identity of %d characters identifies an object, though the program prints at most %d of them",
			len(straddling.String()), RemovalIdentityPrintMax)
	}
	rc := removal("google_storage_bucket_object", map[string]any{
		"id": "fmgd-bucket/" + strings.Repeat("x", RemovalIdentityPrintMax), "name": strings.Repeat("x", RemovalIdentityPrintMax),
		"project": "proj-1", "generation": float64(1758000000000000)})
	id := RemovedIdentity(rc)
	if id.Identifies() {
		t.Fatalf("a %d-character identity identifies an object, want it refused", len(id.String()))
	}
	// The refusal says the length, and it prints no part of the identity: a
	// cut identity is one nothing can ever be matched against.
	ok, why := Approves([]DeleteEntry{{Address: rc.Address, Before: id.String()}}, rc)
	if ok {
		t.Error("an entry holding an identity over the bound approved the removal")
	}
	if !strings.Contains(why, strconv.Itoa(len(id.String()))) || !strings.Contains(why, strconv.Itoa(RemovalIdentityPrintMax)) {
		t.Errorf("the refusal does not say how long the identity is and what the bound is: %q", why)
	}
	if strings.Contains(why, strings.Repeat("x", 64)) {
		t.Errorf("the refusal prints the identity it cannot print whole: %q", why)
	}
	if printed := RemoveIdentityText(rc); strings.Contains(printed, strings.Repeat("x", 64)) {
		t.Errorf("the sentence for an address the list does not name prints the identity it cannot print whole: %q", printed)
	}
	// And the address is not offered as one to approve: no entry can clear it.
	if got := ListableEntries(&Plan{ResourceChanges: []ResourceChange{rc}}, nil); len(got) != 0 {
		t.Errorf("ListableEntries offers %v, an address no entry can clear", got)
	}
}

// TestTheMismatchRefusalNamesABoundedNumberOfEntries: a list is as long as
// whoever wrote it made it, and the object it is read from is bounded at 64
// KiB — so the sentence that names the entries at one address has to bound
// them itself, the way every other listing in this program does
// (safetext.FieldList): a bounded number of them, and an honest count of the
// rest. Without the count bound one refusal line grows with the list.
func TestTheMismatchRefusalNamesABoundedNumberOfEntries(t *testing.T) {
	plan := removal("google_storage_bucket", bucketBefore("fmgd-data", "2026-01-02T03:04:05Z"))
	entries := make([]DeleteEntry, 0, safetext.FieldListMax+10)
	for i := 0; i < safetext.FieldListMax+10; i++ {
		entries = append(entries, DeleteEntry{Address: plan.Address,
			Before: "type=google_storage_bucket id=other-" + strconv.Itoa(i)})
	}
	ok, why := Approves(entries, plan)
	if ok {
		t.Fatal("a list of entries for other objects approved the removal")
	}
	if note := safetext.FieldListNote(10); !strings.Contains(why, note) {
		t.Errorf("the refusal does not count the entries it left out (%q): %q", note, why)
	}
	last := entries[len(entries)-1].Before
	if strings.Contains(why, last) {
		t.Errorf("the refusal names the last entry (%q) although the listing is bounded: %q", last, why)
	}
	if n := len(why); n > 4096 {
		t.Errorf("the refusal is %d characters long for %d entries, want it bounded", n, len(entries))
	}
}
