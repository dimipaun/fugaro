package tf

import (
	"slices"
	"strconv"
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
// gate admits a removal only when both match: an approval given for the object
// at an address is not an approval of another object a later plan carries
// there, which is what the address alone could not tell apart.
//
// An identity is two kinds of attribute, and the second is what tells an
// INSTANCE of an object from the next object of the same name:
//
//   - the name-derived ones: the resource type, the project the object is in,
//     its configured name, and the provider's own id. The id is NOT
//     per-instance — the Google provider derives it from the configured name
//     for every type on the managed allowlist, google_apikeys_key included
//     (its id is projects/P/locations/global/keys/NAME) — so it says which
//     object the entry is about and not which instance of that object;
//   - and, for a type whose before view carries one, the value the provider
//     mints when the object is created and never reuses: a bucket's
//     time_created, a secret's and an artifact repository's create_time, a
//     job's and an API key's uid, a service account's unique_id and a bucket
//     object's generation (removalInstanceAttributes, one entry per type, read
//     from the before view exactly as the name-derived parts are). A bucket
//     deleted and re-created under the same name carries a different
//     time_created, so the entry that approved the first removal does not
//     approve the second.
//
// What this does NOT close, recorded here rather than implied by the rule:
// seven of the managed allowlist's fourteen types have no such attribute in
// their before view at all (removalNoInstanceAttribute, one reason per type) —
// a scheduler job, a project service, and the five *_iam_member types, whose
// whole state is the grant they carry. For those the identity is the named
// object: an object deleted and re-created under the same name at the same
// address carries the same identity, and the entry still approves its removal.
// For those types the rule tells one NAMED object at an address from another
// one, and for the other seven it tells one instance of a name from the next.
// A before view of a type that has a per-instance attribute and does not carry
// it — a state without it, or an attribute the provider left out — identifies
// nothing and is refused rather than approved for its name alone (Identifies,
// Approves): a type whose name is known not to be enough has no weaker answer
// the rule may fall back to.

// removalInstanceAttributes is the attribute of each resource type whose
// before view carries a value the provider mints per INSTANCE of the object
// rather than per name: the value that differs when an object of the same name
// is created again, and the one thing a name-derived id cannot tell apart
// (plan Task 6.3).
//
// Every entry is a computed, output-only attribute of the Google provider
// (8.4.0, the version every root pins) whose value changes across
// re-creation:
//
//   - google_storage_bucket: time_created, the bucket's creation time;
//   - google_secret_manager_secret and google_artifact_registry_repository:
//     create_time, the same fact;
//   - google_cloud_run_v2_job and google_apikeys_key: uid, the server's unique
//     id for the object;
//   - google_service_account: unique_id, the account's own numeric id (the
//     provider's id is its email, derived from account_id);
//   - google_storage_bucket_object: generation, the number GCS gives each
//     write of an object, which is a NUMBER in terraform's JSON rather than a
//     string (instanceValue).
//
// The other seven types of the managed allowlist are in
// removalNoInstanceAttribute with the reason they have none. A type on neither
// list is one no managed plan may remove at all (ManagedCover refuses it for
// its type before any delete is judged), and
// TestRemovalInstanceAttributesDecideEveryManagedType holds both maps against
// managedTypes, so a type added to that list has to be decided here too.
var removalInstanceAttributes = map[string]string{
	"google_apikeys_key":                  "uid",
	"google_artifact_registry_repository": "create_time",
	"google_cloud_run_v2_job":             "uid",
	"google_secret_manager_secret":        "create_time",
	"google_service_account":              "unique_id",
	"google_storage_bucket":               "time_created",
	"google_storage_bucket_object":        "generation",
}

// removalNoInstanceAttribute is why each managed type the map above does not
// carry has no per-instance attribute to bind an entry to: the residual the
// package comment states, one reason per type, required for every type of the
// managed allowlist the map does not carry
// (TestRemovalInstanceAttributesDecideEveryManagedType), so leaving a type out
// is a decision rather than an omission.
var removalNoInstanceAttribute = map[string]string{
	"google_artifact_registry_repository_iam_member": "an IAM grant's whole state is the grant: the provider's id is the repository, the role and the member, it adds no output-only value of its own, and a grant re-created exactly as it was is the same grant",
	"google_cloud_run_v2_job_iam_member":             "an IAM grant's whole state is the grant (see google_artifact_registry_repository_iam_member)",
	"google_cloud_scheduler_job":                     "the provider adds no output-only value to a scheduler job: its id is projects/P/locations/R/jobs/NAME, derived from name and region, and the one computed attribute it carries beside it (state, ENABLED or PAUSED) is configuration a re-creation need not change",
	"google_project_service":                         "a service enablement has no instance: its id is PROJECT/SERVICE, the pair it enables, and enabling the same service in the same project again is that same fact rather than a new object",
	"google_secret_manager_secret_iam_member":        "an IAM grant's whole state is the grant (see google_artifact_registry_repository_iam_member)",
	"google_service_account_iam_member":              "an IAM grant's whole state is the grant (see google_artifact_registry_repository_iam_member)",
	"google_storage_bucket_iam_member":               "an IAM grant's whole state is the grant (see google_artifact_registry_repository_iam_member)",
}

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
// plan's before view shows it: the resource type, the name-derived attributes
// that view carries — its id, its name and the project it is in — and, for a
// type that has one, the per-instance value that tells a re-creation from the
// object the owner approved (removalInstanceAttributes).
//
// The name-derived three are the ones terraform's Google provider writes for
// every one of its resources and the ones that name the object: `name` is the
// configured name, `project` the project it lives in, and `id` the provider's
// own identifier for it — which for every managed type is DERIVED from that
// name (a bucket's id is its bucket name, a secret's is
// projects/P/secrets/NAME, a Cloud Run job's and an API key's are the same
// shape), so `id` names the object and does not distinguish two instances of
// it.
//
// A view that carries none of these three identifies nothing (Identifies), and
// neither does a view of a type whose per-instance attribute it does not
// carry: two objects of one type at one address are exactly what an
// address-only approval cannot tell apart, and a type the rule knows needs one
// value more is refused rather than approved on a weaker identity than the
// rule means.
type RemovalIdentity struct {
	Type    string
	Project string
	Name    string
	ID      string
	// Instance is the type's per-instance value, read from the before view's
	// own attribute (removalInstanceAttributes); empty for the types that have
	// none.
	Instance string
}

// String is the identity as one string: every part this view carries, in a
// fixed order, as "type=… project=… name=… id=… instance=…". It is both what
// the owner's object holds and what the gate compares, and the comparison is
// exact string equality — so a part is written as the plan wrote it, and a
// part the view does not carry is left out rather than written empty. An
// absent attribute and an empty one therefore encode identically; both
// identify nothing (Identifies), and no before view of a type the managed
// allowlist admits carries an empty one, so nothing has to be told apart
// there.
//
// The encoding is injective, and two identities that differ in any part write
// different strings: a space separates the parts and a backslash escapes, so a
// value that itself holds either is written escaped (identityPart) and no
// other value can stand in its place. Without that, the name "a id=b" and the
// pair of a name "a" with an id "b" would write one string, and an entry
// approved for one object would match the other; the encoding that wrote a
// value as it was collided exactly there, and
// TestRemovalIdentityStringIsInjective fails on it.
//
// The string is for comparing and for one person to read, and a refusal prints
// it for the owner to paste into fugaro allow-delete. Nothing parses it back
// into parts: the identity is a string the command compares whole, and the
// escaping is what makes that safe rather than ambiguous.
func (i RemovalIdentity) String() string {
	parts := make([]string, 0, 5)
	for _, p := range []struct{ k, v string }{
		{"type", i.Type}, {"project", i.Project}, {"name", i.Name}, {"id", i.ID}, {"instance", i.Instance},
	} {
		if p.v != "" {
			parts = append(parts, identityPart(p.k, p.v))
		}
	}
	return strings.Join(parts, " ")
}

// identityPart is one part of an identity as "key=value", with the two
// characters that could make two different identities write one string
// escaped: a backslash (the escape itself, escaped first) and a space (which
// separates the parts). Every other character is written as it is, so the
// ordinary identity — a type, a project, a DNS-shaped name, a provider id —
// reads exactly as it always did, and only a value that actually holds one of
// the two reads differently.
//
// Distinct values escape to distinct text (unescaping is its inverse), and an
// escaped space cannot end a part, so the whole encoding is injective, which
// is what String's own doc records. Quoting is deliberately not used: the
// identity is printed for the owner to paste into fugaro allow-delete as
// ADDRESS=IDENTITY, and the command compares it byte for byte.
func identityPart(k, v string) string {
	if strings.ContainsAny(v, " \\") {
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, " ", `\ `)
	}
	return k + "=" + v
}

// Identifies reports whether the identity names an object rather than only its
// type: one of id, name and project must be there, and, for a type whose
// before view carries a per-instance value, that value as well. A type alone
// does not identify anything — two objects of one type at one address are
// exactly what an address-only approval cannot tell apart — and neither does
// the name of a type the provider gives a per-instance value to, which is a
// name two instances can share: a removal whose before view shows no such
// value is never approved (Approves).
func (i RemovalIdentity) Identifies() bool {
	if attr := removalInstanceAttributes[i.Type]; attr != "" && i.Instance == "" {
		return false
	}
	return i.ID != "" || i.Name != "" || i.Project != ""
}

// RemovedIdentity is the identity of the object rc removes: the change's
// resource type, its before view's id, name and project, and, for a type whose
// view carries one, the per-instance value that tells this instance from the
// next object of the same name. A change that removes nothing still answers —
// a create's before is null — and the answer then identifies nothing, which is
// what Approves refuses.
func RemovedIdentity(rc ResourceChange) RemovalIdentity {
	b := rc.Change.Before
	id := RemovalIdentity{
		Type:    rc.Type,
		Project: str(b["project"]),
		Name:    str(b["name"]),
		ID:      str(b["id"]),
	}
	if attr := removalInstanceAttributes[rc.Type]; attr != "" {
		id.Instance = instanceValue(b[attr])
	}
	return id
}

// instanceValue is a before view's value for a per-instance attribute as an
// identity writes it: a string as the plan wrote it, and a NUMBER as the
// decimal the plan wrote — google_storage_bucket_object.generation is a number
// in terraform's JSON, and this is where it is read. The values these
// attributes hold are RFC 3339 timestamps, uids and small integers, and the
// integer ones (a generation, a service account's unique_id) are far below
// 2^53, which float64 holds exactly, so the decimal is the plan's own.
//
// Any other value — a null, an object, a list, a bool — is "", the fail-closed
// answer: a view that does not carry the value in the shape the provider
// writes it identifies nothing, rather than identifying an object on a
// rendering of something else.
func instanceValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
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
// view does not identify the object the rule needs (notIdentified), which no
// entry can be matched against and which is therefore refused rather than
// approved for the type alone.
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
		return false, "the plan does not identify the object this change removes (" + id.notIdentified() + "), so the owner's delete list cannot be matched against it"
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
	held := strings.Join(bound, ", ")
	if held == "" {
		held = "no identity at all"
	}
	return false, "the owner's delete list approves this address for another object (the list holds " + held + ", and the plan removes " + identityText(id) + ")"
}

// notIdentified is what a before view that identifies nothing is missing: the
// per-instance attribute, for a type whose identity turns on one, and the
// three name-derived attributes otherwise. It is the parenthetical the two
// sentences about an unidentified removal carry (Approves, identityText), so
// the reason a refusal gives is the reason this rule actually applied.
func (i RemovalIdentity) notIdentified() string {
	if attr := removalInstanceAttributes[i.Type]; attr != "" && i.Instance == "" {
		return "the before view of a " + safetext.Field(i.Type) + " carries no " + attr + ", the value that tells one instance of the name from the next"
	}
	return "no id, name or project in its before view"
}

// identityText is a RemovalIdentity as a sentence names it: the identity
// itself, or, for a view that identifies nothing, the reason it does not.
func identityText(id RemovalIdentity) string {
	if s := safetext.Field(id.String()); s != "" && id.Identifies() {
		return s
	}
	return "an object the plan does not identify (" + id.notIdentified() + ")"
}

// RemoveIdentityText is the identity of the object a removal takes away, as
// the refusal a person reads names it, for the sentences about a removal whose
// list approval is not the rule that refused it — the cover's own item for an
// address the owner's list does not name. It is what the owner copies into
// fugaro allow-delete, so it is the identity itself and not a description of
// it, and the reason when the plan identifies nothing. It is printed unquoted
// and without marks of the sentence's own: the owner pastes it, and a
// surrounding pair of quotes would be characters the identity does not have
// (the command strips one matched pair if a caller pastes them anyway, so the
// two agree about what is printed here). A value the identity holds that
// carries a space is printed escaped, exactly as the entry holds it
// (identityPart), so the text a refusal prints is the text an entry compares.
func RemoveIdentityText(rc ResourceChange) string { return identityText(RemovedIdentity(rc)) }
