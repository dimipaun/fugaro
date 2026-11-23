package workspace_test

// C1: the roster contract, held to the promises it is written for.
//
// The roster is a wire format over IAM -- the node five accounts of the
// workspace's project can rewrite, and the shape every `workspace user`,
// `workspace domains`, `workspace project` and `workspace link` change in the
// plan reads and writes -- so what is pinned here is the pair a drift can hide
// in: the Go types and the schema have to describe ONE document, and the split
// between what the schema refuses and what only the reader can refuse has to be
// the one the contract document states. JSON Schema has no ordering keyword, no
// uniqueness keyword and no cross-object rule, so the sorted-and-unique lists,
// "an admin is a person" and the folder id that goes with the folder mechanism
// are the reader's; everything a closed object can say is the schema's, because
// an editor completing a document from the schema writes a document the reader
// takes.
//
// The tests are named for the plan's filter: `go test ./internal/workspace/
// ./schemas/ -run 'TestRoster|TestSchema' -count=1`. Every helper here carries
// the task id, as the plan's conventions ask, because C2's tests land in this
// same package.

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The two schema files this task adds, by the names under schemas/.
const (
	c1RosterSchemaFile = "workspace-roster.schema.json"
	c1AuditSchemaFile  = "workspace-audit-event.schema.json"
)

// c1RosterFixture is the roster the round trip is made of: every key of the
// document is exercised at least once, so a field the struct forgets to write
// and a field the schema does not declare are both visible. It is a folder
// workspace with one human from an allowed domain, one human from outside it
// (D2812's mark), one machine with the per-project role it is expected to hold
// (D2806), one admin, one allowed domain and one grant the workspace wrote into
// a member (D2818's mark).
func c1RosterFixture() *workspace.Roster {
	return &workspace.Roster{
		Version:   workspace.RosterVersion,
		Mechanism: workspace.MechanismFolder,
		FolderID:  "folders/123456789012",
		People: []workspace.Person{
			{Address: "alice@example.com", Kind: workspace.KindHuman},
			{Address: "bob@outside.test", Kind: workspace.KindHuman, External: true},
			{
				Address: "ci@acme-app.iam.gserviceaccount.com",
				Kind:    workspace.KindSA,
				Roles:   []workspace.ProjectRole{{Project: "acme-app", Role: workspace.RoleOperator}},
			},
		},
		Admins:  []string{"alice@example.com"},
		Domains: []string{"example.com"},
		Grants: []workspace.StoredGrant{
			{Project: "acme-app", User: "bob@outside.test", Role: workspace.RoleViewer},
		},
	}
}

// TestRosterSchemaRoundTrip is the red step of the task: the types and the
// schema agree on one document, in both directions.
func TestRosterSchemaRoundTrip(t *testing.T) {
	sch := c1Schema(t, c1RosterSchemaFile)
	doc := c1SchemaDocument(t, c1RosterSchemaFile)

	r := c1RosterFixture()
	raw, err := workspace.EncodeRoster(r)
	if err != nil {
		t.Fatalf("EncodeRoster refuses the fixture: %v", err)
	}
	if err := c1Validate(t, sch, raw); err != nil {
		t.Fatalf("schemas/%s refuses the document workspace.EncodeRoster writes: %v\n%s", c1RosterSchemaFile, err, raw)
	}
	back, err := workspace.DecodeRoster(raw)
	if err != nil {
		t.Fatalf("this package writes a document it does not read: %v\n%s", err, raw)
	}
	if !reflect.DeepEqual(r, back) {
		t.Errorf("the roster did not round trip:\n got %+v\nwant %+v\nfrom\n%s", back, r, raw)
	}

	// Every key the document writes is declared and every key the schema
	// declares is one it writes, at each of the four objects. The closed
	// objects are the strict reader's own promise -- an unknown field is
	// refused at the decode (TestRosterDecodeRefusesUnknownField) -- so a
	// schema admitting a key the reader refuses would complete a document that
	// cannot be read back.
	var written map[string]any
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	c1Agrees(t, c1RosterSchemaFile, doc, written, "the roster")
	// The machine's entry is the one that writes every key the schema declares
	// for a person -- a human's carries no roles, which is asserted from the
	// other side just below.
	c1Agrees(t, c1RosterSchemaFile, doc, c1Map(t, c1List(t, written["people"])[2]), "$defs.person", "$defs", "person")
	c1Agrees(t, c1RosterSchemaFile, doc, c1Map(t, c1List(t, c1Map(t, c1List(t, written["people"])[2])["roles"])[0]), "$defs.projectRole", "$defs", "projectRole")
	c1Agrees(t, c1RosterSchemaFile, doc, c1Map(t, c1List(t, written["grants"])[0]), "$defs.grant", "$defs", "grant")
	// A human carries no roles, and the machine's are the only reason the key
	// is there at all: the empty list is left out, which is the form the
	// database keeps anyway (it prunes an empty list on the way in).
	if got := slices.Sorted(maps.Keys(c1Map(t, c1List(t, written["people"])[0]))); !slices.Equal(got, []string{"address", "external", "kind"}) {
		t.Errorf("a human is written as %v, and a human's entry is its address, its kind and the external mark: the roles key belongs to a machine (D2806)", got)
	}
	if _, ok := c1Map(t, c1List(t, written["people"])[2])["roles"]; !ok {
		t.Errorf("the machine's entry carries no roles, and a machine is written with the per-project roles it is expected to hold (D2806)")
	}

	// What the schema requires of each object, and that each of them is closed.
	if got := slices.Sorted(slices.Values(c1Strings(t, doc, "required"))); !slices.Equal(got, []string{"admins", "mechanism", "people", "version"}) {
		t.Errorf("the roster schema requires %v, and a roster states its version, its mechanism, its people and its admins; the domains and the grants the workspace wrote are lists that are there only when they hold something", got)
	}
	if got := slices.Sorted(slices.Values(c1Strings(t, doc, "$defs", "person", "required"))); !slices.Equal(got, []string{"address", "external", "kind"}) {
		t.Errorf("the person schema requires %v, and a person states its address, its kind and the external mark -- the mark is written even when it is false, because it is D2812's statement about how the person was admitted and not a default a reader may assume", got)
	}
	if got := slices.Sorted(slices.Values(c1Strings(t, doc, "$defs", "projectRole", "required"))); !slices.Equal(got, []string{"project", "role"}) {
		t.Errorf("the project-role schema requires %v, and a role is its project and the role", got)
	}
	if got := slices.Sorted(slices.Values(c1Strings(t, doc, "$defs", "grant", "required"))); !slices.Equal(got, []string{"project", "role", "user"}) {
		t.Errorf("the grant schema requires %v, and a stored grant is its project, its user and the role the workspace wrote", got)
	}
	for _, at := range [][]string{
		{"additionalProperties"},
		{"$defs", "person", "additionalProperties"},
		{"$defs", "projectRole", "additionalProperties"},
		{"$defs", "grant", "additionalProperties"},
	} {
		if closed, ok := c1Node(t, doc, at...).(bool); !ok || closed {
			t.Errorf("the roster schema's %s is %v, and DecodeRoster decodes strictly: the schema has to refuse the unknown key the reader refuses", strings.Join(at, "."), closed)
		}
	}

	// The enums are the Go lists, so the two sides cannot drift apart one
	// value at a time: a kind, a mechanism, a role or an action added to one
	// and not the other is a document valid in one place and refused in the
	// other.
	if got, want := c1Strings(t, doc, "properties", "mechanism", "enum"), workspace.Mechanisms(); !slices.Equal(got, c1Names(want)) {
		t.Errorf("the mechanism enum is %v and workspace.Mechanisms() is %v", got, want)
	}
	if got, want := c1Strings(t, doc, "$defs", "person", "properties", "kind", "enum"), workspace.Kinds(); !slices.Equal(got, c1Names(want)) {
		t.Errorf("the kind enum is %v and workspace.Kinds() is %v", got, want)
	}
	if got, want := c1Strings(t, doc, "$defs", "role", "enum"), workspace.Roles(); !slices.Equal(got, c1Names(want)) {
		t.Errorf("the role enum is %v and workspace.Roles() is %v", got, want)
	}
	if got := c1Node(t, doc, "properties", "version", "const"); fmt.Sprint(got) != fmt.Sprint(workspace.RosterVersion) {
		t.Errorf("the schema's version const is %v and this build reads version %d", got, workspace.RosterVersion)
	}
	// The folder id is declared and carries the grammar workspace init writes:
	// an id the Resource Manager accepts, which is the same grammar
	// internal/workspace's folder authority holds a parent to.
	if _, ok := c1Node(t, doc, "properties", "folder_id").(map[string]any); !ok {
		t.Error("the roster schema declares no folder_id, and a folder workspace states the folder it owns")
	}

	// The pruned form. The database keeps no empty list -- a list is an object
	// with numeric keys, so an empty one goes on the way in -- and the node
	// therefore carries no `domains` key and no `grants` key for a workspace
	// that has written neither. The absence is the empty list, and it is the
	// same roster: two spellings of "none" would be two documents the drift
	// comparison reads as different.
	pruned := maps.Clone(written)
	delete(pruned, "domains")
	delete(pruned, "grants")
	if err := c1Validate(t, sch, c1Remarshal(t, pruned)); err != nil {
		t.Errorf("the schema refuses the document the database really holds for a workspace that lists no domain and has written no grant: %v", err)
	}
	want := c1RosterFixture()
	want.Domains, want.Grants = nil, nil
	got, err := workspace.DecodeRoster(c1Remarshal(t, pruned))
	if err != nil {
		t.Fatalf("DecodeRoster refuses the pruned document: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the pruned document decoded to %+v and the document with empty lists to %+v: an absent list is the empty list", got, want)
	}

	// The service-account mechanism: no folder at all, and the two list keys
	// left out because nothing is in them.
	bare := &workspace.Roster{
		Version:   workspace.RosterVersion,
		Mechanism: workspace.MechanismSA,
		People:    []workspace.Person{{Address: "alice@example.com", Kind: workspace.KindHuman}},
		Admins:    []string{"alice@example.com"},
	}
	bareRaw, err := workspace.EncodeRoster(bare)
	if err != nil {
		t.Fatalf("EncodeRoster refuses a roster with the service-account mechanism: %v", err)
	}
	if err := c1Validate(t, sch, bareRaw); err != nil {
		t.Fatalf("schemas/%s refuses the roster a service-account workspace holds: %v\n%s", c1RosterSchemaFile, err, bareRaw)
	}
	bareBack, err := workspace.DecodeRoster(bareRaw)
	if err != nil {
		t.Fatalf("DecodeRoster refuses the roster a service-account workspace holds: %v\n%s", err, bareRaw)
	}
	if !reflect.DeepEqual(bare, bareBack) {
		t.Errorf("the service-account roster did not round trip:\n got %+v\nwant %+v", bareBack, bare)
	}
}

// TestRosterDecodeRefusesUnknownField is the strictness half of the contract:
// an unknown field is an error, as every config and wire format here does, at
// each of the places a key can be added. A document that was read with a key
// nobody decided is a document two writers disagree about -- the one that put
// the key there and the reader with no rule for it.
func TestRosterDecodeRefusesUnknownField(t *testing.T) {
	sch := c1Schema(t, c1RosterSchemaFile)

	// A key nobody decided at the roster's own level, at a person's, at one of
	// a machine's roles, and at a grant's.
	for _, tc := range []struct {
		name   string
		mutate func(doc map[string]any)
	}{
		{"the roster", func(doc map[string]any) { doc["owner"] = "alice@example.com" }},
		{"a person", func(doc map[string]any) { c1Person(t, doc, 0)["member"] = true }},
		{"a project role", func(doc map[string]any) {
			c1Role(t, doc, 2, 0)["scope"] = "project"
		}},
		{"a grant", func(doc map[string]any) { c1Grant(t, doc, 0)["reason"] = "first" }},
	} {
		raw := c1Remarshal(t, c1Mutated(t, tc.mutate))
		if err := c1Validate(t, sch, raw); err == nil {
			t.Errorf("schemas/%s accepts an unknown key at %s, and the reader refuses it", c1RosterSchemaFile, tc.name)
		}
		if _, err := workspace.DecodeRoster(raw); err == nil {
			t.Errorf("DecodeRoster accepts an unknown key at %s: %s", tc.name, raw)
		}
	}

	// The audit event is the other document this task fixes, and it is closed
	// the same way at both of its levels.
	eventRaw, err := workspace.EncodeAuditEvent(c1AuditFixture())
	if err != nil {
		t.Fatalf("EncodeAuditEvent refuses the fixture: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal(eventRaw, &event); err != nil {
		t.Fatal(err)
	}
	event["reason"] = "because"
	if _, err := workspace.DecodeAuditEvent(c1Remarshal(t, event)); err == nil {
		t.Errorf("DecodeAuditEvent accepts an unknown key: %s", c1Remarshal(t, event))
	}
}

// TestRosterDecodeRefusesASecondSpellingOfAField is the half of the closed
// objects the reader has to carry on its own: encoding/json matches an object
// key to a struct field case-insensitively and lets the LATER of two spellings
// of one key win, so a document that spells a field in another case, or writes
// it twice, is read as whatever the hidden spelling says while the spelling a
// person reads says something else -- the admin list swapped, a grant raised
// from viewer to admin, a machine's expected roles emptied. DisallowUnknownFields
// says nothing about either: "Admins" IS the admins field as far as it is
// concerned, and a repeated key is one key with two values to it.
//
// The node is written by hand as well as by this program -- five accounts of the
// workspace's project hold write on it (D2817) -- so the reader walks the
// document and refuses a name that is not the one it writes, spelled exactly,
// written once (refuseRosterSpellings, refuseEventSpellings, the refusal
// internal/access/store.go makes for the member roster).
//
// These cases are raw documents rather than maps, and the reason is the exploit
// itself: two spellings of one key collapse in a Go map, and json.Marshal writes
// a map's keys in byte order -- an upper-case variant ahead of the lower-case
// name it hides -- so a map cannot put the hidden spelling where it wins. Here
// it is written last, and what it steals is a value the roster would otherwise
// accept -- a person of the roster, a role of the closed set -- so a reader
// without the walk takes the document and says nothing about it.
func TestRosterDecodeRefusesASecondSpellingOfAField(t *testing.T) {
	sch := c1Schema(t, c1RosterSchemaFile)

	for _, tc := range []struct {
		name      string
		old, new  string
		schemaToo bool
	}{
		{"the admin list swapped under another spelling",
			`"admins":["alice@example.com"]`,
			`"admins":["alice@example.com"],"Admins":["bob@outside.test"]`, true},
		{"the admin list written twice",
			`"admins":["alice@example.com"]`,
			`"admins":["alice@example.com"],"admins":["bob@outside.test"]`, false},
		{"a person's external mark swapped under another spelling",
			`"address":"alice@example.com","kind":"human","external":false`,
			`"address":"alice@example.com","kind":"human","external":false,"External":true`, true},
		{"a person's external mark written twice",
			`"address":"alice@example.com","kind":"human","external":false`,
			`"address":"alice@example.com","kind":"human","external":false,"external":true`, false},
		{"a machine's expected roles emptied under another spelling",
			`"roles":[{"project":"acme-app","role":"operator"}]`,
			`"roles":[{"project":"acme-app","role":"operator"}],"Roles":[]`, true},
		{"a machine's expected role raised under another spelling",
			`"role":"operator"`,
			`"role":"operator","Role":"admin"`, true},
		{"a grant raised under another spelling",
			`"user":"bob@outside.test","role":"viewer"`,
			`"user":"bob@outside.test","role":"viewer","Role":"admin"`, true},
		{"a grant's role written twice",
			`"user":"bob@outside.test","role":"viewer"`,
			`"user":"bob@outside.test","role":"viewer","role":"admin"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := c1Patched(t, c1RosterBytes(t), tc.old, tc.new)
			// A key spelled in another case is a key the schema does not know,
			// and it is closed at every level -- so an editor completing a node
			// from the schema cannot write one. A key written twice is not the
			// schema's: a validator reads one value per name, the last, and the
			// repetition is gone before the rule is asked.
			schemaErr := c1Validate(t, sch, raw)
			switch {
			case tc.schemaToo && schemaErr == nil:
				t.Errorf("schemas/%s accepts %s, and a schema closed at every level refuses a key it does not know", c1RosterSchemaFile, tc.name)
			case !tc.schemaToo && schemaErr != nil:
				t.Errorf("schemas/%s refuses %s, and a key written twice is not something it can see: %v", c1RosterSchemaFile, tc.name, schemaErr)
			}
			if _, err := workspace.DecodeRoster(raw); err == nil {
				t.Errorf("DecodeRoster accepts %s:\n%s", tc.name, raw)
			}
		})
	}

	// The audit event is one object, and the same walk over it. The trail is
	// create-only, so an entry read as something other than what it says is a
	// statement about the past nobody can correct.
	eventSch := c1Schema(t, c1AuditSchemaFile)
	for _, tc := range []struct {
		name      string
		old, new  string
		schemaToo bool
	}{
		{"the action swapped under another spelling",
			`"action":"user.add"`, `"action":"user.add","Action":"user.remove"`, true},
		{"the action written twice",
			`"action":"user.add"`, `"action":"user.add","action":"user.remove"`, false},
		{"the actor swapped under another spelling",
			`"actor":"alice@example.com"`, `"actor":"alice@example.com","Actor":"bob@outside.test"`, true},
	} {
		t.Run("event: "+tc.name, func(t *testing.T) {
			raw := c1Patched(t, c1EventBytes(t), tc.old, tc.new)
			schemaErr := c1Validate(t, eventSch, raw)
			switch {
			case tc.schemaToo && schemaErr == nil:
				t.Errorf("schemas/%s accepts %s, and a schema closed at every level refuses a key it does not know", c1AuditSchemaFile, tc.name)
			case !tc.schemaToo && schemaErr != nil:
				t.Errorf("schemas/%s refuses %s, and a key written twice is not something it can see: %v", c1AuditSchemaFile, tc.name, schemaErr)
			}
			if _, err := workspace.DecodeAuditEvent(raw); err == nil {
				t.Errorf("DecodeAuditEvent accepts %s:\n%s", tc.name, raw)
			}
		})
	}
}

// TestRosterRefuses is the refusal corpus. Each case is a document that differs
// from the fixture in one way, and the two columns say what the contract says
// about it: every one of them is refused by the reader, and the ones the schema
// can also refuse -- a closed object, an enum, a pattern, a const -- are
// refused by it too. A case in the second column is the whole reason the
// contract document has to state the split: JSON Schema cannot say that a list
// is sorted, unique, or that an admin is a person.
func TestRosterRefuses(t *testing.T) {
	sch := c1Schema(t, c1RosterSchemaFile)

	for _, tc := range []struct {
		name string
		// schemaToo is true for a difference the schema itself refuses. False
		// means the rule is the reader's, and the schema accepting the
		// document is asserted rather than assumed: a rule that turned out to
		// be expressible in the schema belongs there, where every editor of a
		// hand-written node sees it.
		schemaToo bool
		mutate    func(doc map[string]any)
	}{
		// The version. A node a newer Fugaro wrote is refused as a newer one
		// and not read as a shape nobody knows.
		{"a version this build does not read", true, func(doc map[string]any) { doc["version"] = 2 }},
		{"no version at all", true, func(doc map[string]any) { delete(doc, "version") }},

		// The mechanism and its folder id. The id is there exactly when the
		// mechanism is the folder: an id on a service-account workspace names
		// a folder that is not that workspace's, and a folder workspace
		// without one names no folder at all.
		{"a mechanism nobody decided", true, func(doc map[string]any) { doc["mechanism"] = "org" }},
		{"a folder id under the service-account mechanism", true, func(doc map[string]any) { doc["mechanism"] = "sa" }},
		{"no folder id under the folder mechanism", true, func(doc map[string]any) { delete(doc, "folder_id") }},
		{"a folder id that is not one", true, func(doc map[string]any) { doc["folder_id"] = "123456789012" }},
		{"a folder id of another resource", true, func(doc map[string]any) { doc["folder_id"] = "organizations/123456789012" }},

		// A person.
		{"a kind nobody decided", true, func(doc map[string]any) { c1Person(t, doc, 0)["kind"] = "machine" }},
		{"an address that is not one", true, func(doc map[string]any) { c1Person(t, doc, 0)["address"] = "alice.example.com" }},
		{"an address whose domain is not folded", true, func(doc map[string]any) { c1Person(t, doc, 0)["address"] = "alice@EXAMPLE.COM" }},
		{"the external mark missing", true, func(doc map[string]any) { delete(c1Person(t, doc, 0), "external") }},
		{"roles on a person who is not a machine", true, func(doc map[string]any) {
			c1Person(t, doc, 0)["roles"] = []any{map[string]any{"project": "acme-app", "role": "viewer"}}
		}},
		{"a machine marked external", true, func(doc map[string]any) { c1Person(t, doc, 2)["external"] = true }},
		{"a role nobody decided", true, func(doc map[string]any) { c1Role(t, doc, 2, 0)["role"] = "owner" }},
		{"a role on a project that is not a project name", true, func(doc map[string]any) { c1Role(t, doc, 2, 0)["project"] = "Acme_App" }},
		{"two people with one address", false, func(doc map[string]any) { c1Person(t, doc, 1)["address"] = "alice@example.com" }},
		{"people out of order", false, func(doc map[string]any) {
			people := c1List(t, doc["people"])
			people[0], people[1] = people[1], people[0]
		}},
		{"a machine with the same project twice", false, func(doc map[string]any) {
			role := maps.Clone(c1Role(t, doc, 2, 0))
			c1Person(t, doc, 2)["roles"] = []any{c1Role(t, doc, 2, 0), role}
		}},
		{"a machine's roles out of order", false, func(doc map[string]any) {
			second := c1Role(t, doc, 2, 0)
			second["project"] = "z-app"
			c1Person(t, doc, 2)["roles"] = []any{second, c1Role(t, doc, 2, 0)}
		}},
		{"no people at all", true, func(doc map[string]any) { doc["people"] = []any{} }},

		// The admins. The list is a set of people: sorted, each once, each of
		// them a person of this roster, and a machine is never one (D2823
		// trusts an admin only when the live binding agrees, which a machine
		// can never give).
		{"no admins at all", true, func(doc map[string]any) { doc["admins"] = []any{} }},
		{"an admin whose domain is not folded", true, func(doc map[string]any) { doc["admins"] = []any{"alice@EXAMPLE.COM"} }},
		{"admins out of order", false, func(doc map[string]any) { doc["admins"] = []any{"bob@outside.test", "alice@example.com"} }},
		{"an admin listed twice", false, func(doc map[string]any) { doc["admins"] = []any{"alice@example.com", "alice@example.com"} }},
		{"an admin who is not a person of the roster", false, func(doc map[string]any) { doc["admins"] = []any{"zoe@example.com"} }},
		{"a machine as an admin", false, func(doc map[string]any) { doc["admins"] = []any{"ci@acme-app.iam.gserviceaccount.com"} }},

		// The allowed domains.
		{"a domain that is empty", true, func(doc map[string]any) { doc["domains"] = []any{""} }},
		{"a domain that is not folded", true, func(doc map[string]any) { doc["domains"] = []any{"Example.com"} }},
		{"a domain with a trailing dot", true, func(doc map[string]any) { doc["domains"] = []any{"example.com."} }},
		{"domains out of order", false, func(doc map[string]any) { doc["domains"] = []any{"outside.test", "example.com"} }},

		// The grants the workspace wrote, which are the mark D2818 keeps here
		// and never in a member's roster (D2818 as corrected: the member roster
		// gains no field).
		{"a grant for somebody who is not a person of the roster", false, func(doc map[string]any) { c1Grant(t, doc, 0)["user"] = "zoe@example.com" }},
		{"a grant for a machine", false, func(doc map[string]any) { c1Grant(t, doc, 0)["user"] = "ci@acme-app.iam.gserviceaccount.com" }},
		{"a grant whose role nobody decided", true, func(doc map[string]any) { c1Grant(t, doc, 0)["role"] = "owner" }},
		{"a grant on a project that is not a project name", true, func(doc map[string]any) { c1Grant(t, doc, 0)["project"] = "Acme_App" }},
		{"grants out of order", false, func(doc map[string]any) {
			first := maps.Clone(c1Grant(t, doc, 0))
			first["user"] = "alice@example.com"
			doc["grants"] = []any{c1Grant(t, doc, 0), first}
		}},
		{"grants out of order by project", false, func(doc map[string]any) {
			first := maps.Clone(c1Grant(t, doc, 0))
			first["project"], first["user"] = "acme-app", "alice@example.com"
			second := maps.Clone(c1Grant(t, doc, 0))
			second["project"], second["user"] = "acme", "alice@example.com"
			doc["grants"] = []any{first, second}
		}},
		{"two grants of one project and user", false, func(doc map[string]any) {
			dupe := maps.Clone(c1Grant(t, doc, 0))
			doc["grants"] = []any{c1Grant(t, doc, 0), dupe}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := c1Remarshal(t, c1Mutated(t, tc.mutate))
			schemaErr := c1Validate(t, sch, raw)
			switch {
			case tc.schemaToo && schemaErr == nil:
				t.Errorf("schemas/%s accepts a document the reader refuses (%s): a rule the schema can state belongs in the schema, where an editor of a hand-written node sees it", c1RosterSchemaFile, tc.name)
			case !tc.schemaToo && schemaErr != nil:
				t.Errorf("schemas/%s refuses %s, and the contract says this rule is the reader's: %v", c1RosterSchemaFile, tc.name, schemaErr)
			}
			if _, err := workspace.DecodeRoster(raw); err == nil {
				t.Errorf("DecodeRoster accepts %s:\n%s", tc.name, raw)
			}
		})
	}
}

// TestRosterGrantsSortByProjectThenByUser pins the grants' order to the pair
// and not to a key the two halves are joined into. A project name may carry a
// hyphen, and `-` sorts before the `/` a joined key would be compared at, so a
// joined comparison holds `acme-app` to sort before `acme` and refuses the
// order the contract states -- which is the order every writer of this plan
// sorts by, so the reader would refuse the rosters it is written for (D2818's
// mark).
func TestRosterGrantsSortByProjectThenByUser(t *testing.T) {
	sch := c1Schema(t, c1RosterSchemaFile)

	for _, tc := range []struct {
		name   string
		grants []workspace.StoredGrant
		sorted bool
	}{
		{"by project first", []workspace.StoredGrant{
			{Project: "acme", User: "alice@example.com", Role: workspace.RoleViewer},
			{Project: "acme-app", User: "bob@outside.test", Role: workspace.RoleOperator},
		}, true},
		{"by user inside one project", []workspace.StoredGrant{
			{Project: "acme-app", User: "alice@example.com", Role: workspace.RoleViewer},
			{Project: "acme-app", User: "bob@outside.test", Role: workspace.RoleOperator},
		}, true},
		{"a project after its longer name", []workspace.StoredGrant{
			{Project: "acme-app", User: "alice@example.com", Role: workspace.RoleViewer},
			{Project: "acme", User: "bob@outside.test", Role: workspace.RoleOperator},
		}, false},
		{"a user out of order inside one project", []workspace.StoredGrant{
			{Project: "acme-app", User: "bob@outside.test", Role: workspace.RoleViewer},
			{Project: "acme-app", User: "alice@example.com", Role: workspace.RoleOperator},
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := c1RosterFixture()
			r.Grants = tc.grants
			raw, err := workspace.EncodeRoster(r)

			if !tc.sorted {
				if err == nil {
					t.Fatalf("EncodeRoster accepts grants that are not sorted by project and then by user: %+v", tc.grants)
				}
				if !strings.Contains(err.Error(), "the list is sorted by project and then by user") {
					t.Errorf("EncodeRoster refuses an unordered grant list with %q, and the refusal names the order the list is in", err)
				}
				if err := r.Validate(); err == nil {
					t.Errorf("Validate accepts grants that are not sorted by project and then by user: %+v", tc.grants)
				}
				return
			}

			if err != nil {
				t.Fatalf("EncodeRoster refuses grants sorted project-first: %+v\n%v", tc.grants, err)
			}
			if err := c1Validate(t, sch, raw); err != nil {
				t.Fatalf("schemas/%s refuses the document: %v\n%s", c1RosterSchemaFile, err, raw)
			}
			back, err := workspace.DecodeRoster(raw)
			if err != nil {
				t.Fatalf("DecodeRoster refuses a document EncodeRoster wrote: %v\n%s", err, raw)
			}
			if !slices.Equal(back.Grants, tc.grants) {
				t.Errorf("the grants did not round trip:\n got %+v\nwant %+v", back.Grants, tc.grants)
			}
		})
	}
}

// TestRosterSchemaAgreesWithTheReaderOnAddresses pins the two address rules to
// one another over a corpus: the schema's pattern is exactly the set of
// spellings CanonicalAddress leaves alone, and the schema refuses a spelling the
// folder would change. That is what makes the schema's pattern the reader's
// answer -- a node an editor wrote from the schema is one this package reads
// back unchanged -- and the reason the member roster's own address grammar
// (internal/access.Member) is the one restated here rather than a second one.
func TestRosterSchemaAgreesWithTheReaderOnAddresses(t *testing.T) {
	sch := c1Schema(t, c1RosterSchemaFile)
	address := func(t *testing.T, value string) []byte {
		t.Helper()
		doc := c1Document(t, c1RosterFixture())
		c1Person(t, doc, 0)["address"] = value
		return c1Remarshal(t, doc)
	}
	for _, value := range []string{
		"alice@example.com",
		"alice+bob@sub.example.co.uk",
		"Alice@example.com",
		"alice@EXAMPLE.COM",
		"ALICE@example.com",
		"a@b",
		"ci@acme-app.iam.gserviceaccount.com",
		"alice",
		"@example.com",
		"alice@",
		"alice@@example.com",
		"alice@example.com ",
		" alice@example.com",
		"ali ce@example.com",
		"álice@example.com",
		"alice@exámple.com",
		"",
	} {
		canonical, err := workspace.CanonicalAddress(value)
		schemaErr := c1Validate(t, sch, address(t, value))
		switch {
		case err != nil && schemaErr == nil:
			t.Errorf("CanonicalAddress refuses %q (%v) and the schema admits the spelling: the schema has to refuse what the reader refuses", value, err)
		case err == nil && canonical == value && schemaErr != nil:
			t.Errorf("CanonicalAddress leaves %q alone and the schema refuses it: %v", value, schemaErr)
		case err == nil && canonical != value && schemaErr == nil:
			t.Errorf("the schema admits %q and CanonicalAddress folds it to %q: a document an editor wrote from the schema would not read back unchanged", value, canonical)
		}
		// And the other direction: what the fold produces is always a spelling
		// the schema admits, so a writer that folds first never writes a
		// document its own editor's schema refuses.
		if err == nil {
			if schemaErr := c1Validate(t, sch, address(t, canonical)); schemaErr != nil {
				t.Errorf("the schema refuses %q, which is what CanonicalAddress folds %q to: %v", canonical, value, schemaErr)
			}
		}
	}
}

// TestRosterAddressFoldAgreesWithAccess pins the roster's address fold to the
// member roster's, letter for letter. The two packages cannot share the
// function -- internal/access imports internal/projectpolicy, which imports
// this package, so this file restates the rule -- and a restated rule is a rule
// that drifts unless something holds the two together. This is that something:
// a workspace's roster address and a member's roster address name one person,
// and a fold that differed would be a person the workspace sees and a project
// does not.
func TestRosterAddressFoldAgreesWithAccess(t *testing.T) {
	for _, value := range []string{
		"alice@example.com",
		"Alice@example.com",
		"alice@EXAMPLE.COM",
		"a@b",
		"alice+bob@sub.example.co.uk",
		"ci@acme-app.iam.gserviceaccount.com",
		"alice",
		"@example.com",
		"alice@",
		"alice@@example.com",
		"alice@example.com ",
		"ali ce@example.com",
		"álice@example.com",
		"",
	} {
		got, gotErr := workspace.CanonicalAddress(value)
		want, wantErr := access.Member(value)
		if (gotErr == nil) != (wantErr == nil) {
			t.Errorf("CanonicalAddress(%q) errs=%v and access.Member errs=%v: the two spellings of one address have to be one rule", value, gotErr, wantErr)
			continue
		}
		if gotErr != nil {
			continue
		}
		if folded := strings.TrimPrefix(want, "user:"); got != folded {
			t.Errorf("CanonicalAddress(%q) is %q and access.Member folds it to %q", value, got, folded)
		}
	}
	if got, want := c1Names(workspace.Roles()), c1Names(access.Roles()); !slices.Equal(got, want) {
		t.Errorf("workspace.Roles() is %v and access.Roles() is %v: the member roster's roles are this roster's roles, or a grant the workspace wrote is a role no member accepts", got, want)
	}
}

// TestRosterAuditEventRoundTripsThroughTheSchema is the audit event's half of
// the contract. The events are created once and never updated (D2811), so the
// document an event is written as is the document every later reader gets: it
// has to be one this build reads back byte for byte, with the optional fields
// of an event that names a project and a subject left out rather than written
// empty.
func TestRosterAuditEventRoundTripsThroughTheSchema(t *testing.T) {
	sch := c1Schema(t, c1AuditSchemaFile)
	doc := c1SchemaDocument(t, c1AuditSchemaFile)

	events := []workspace.AuditEvent{
		c1AuditFixture(),
		{
			Version: workspace.AuditEventVersion,
			ID:      "20261120T175820Z-0011223344556677",
			Time:    time.Date(2026, 11, 20, 17, 58, 20, 0, time.UTC),
			Actor:   "alice@example.com",
			Action:  workspace.ActionInit,
		},
	}
	for _, event := range events {
		raw, err := workspace.EncodeAuditEvent(event)
		if err != nil {
			t.Fatalf("EncodeAuditEvent refuses %+v: %v", event, err)
		}
		if err := c1Validate(t, sch, raw); err != nil {
			t.Fatalf("schemas/%s refuses the event workspace.EncodeAuditEvent writes: %v\n%s", c1AuditSchemaFile, err, raw)
		}
		back, err := workspace.DecodeAuditEvent(raw)
		if err != nil {
			t.Fatalf("this package writes an event it does not read: %v\n%s", err, raw)
		}
		if !reflect.DeepEqual(event, back) {
			t.Errorf("the event did not round trip:\n got %+v\nwant %+v\nfrom\n%s", back, event, raw)
		}
		var written map[string]any
		if err := json.Unmarshal(raw, &written); err != nil {
			t.Fatal(err)
		}
		// Every key an event writes is declared and every key the schema
		// declares is one the full event writes: the optional three are left
		// out by the events that carry none, which the second event asserts.
		if event.Project != "" {
			c1Agrees(t, c1AuditSchemaFile, doc, written, "the audit event")
		}
		carries := map[string]bool{
			"project": event.Project != "",
			"subject": event.Subject != "",
			"role":    event.Role != "",
		}
		for key, want := range carries {
			if _, ok := written[key]; ok != want {
				t.Errorf("the event writes %s=%v, and an event that carries none leaves the key out rather than writing it empty: %s", key, written[key], raw)
			}
		}
	}

	// The action enum is the Go list of actions, and the id's pattern is the
	// grammar workspace.NewAuditEventID writes -- the id is a Firestore
	// document id and a database key at once, so the schema states what both
	// accept.
	if got, want := c1Strings(t, doc, "properties", "action", "enum"), workspace.Actions(); !slices.Equal(got, c1Names(want)) {
		t.Errorf("the action enum is %v and workspace.Actions() is %v: an action one side knows and the other does not is an audit entry that cannot be written or cannot be read", got, want)
	}
	if got := c1Node(t, doc, "properties", "version", "const"); fmt.Sprint(got) != fmt.Sprint(workspace.AuditEventVersion) {
		t.Errorf("the audit event schema's version const is %v and this build writes version %d", got, workspace.AuditEventVersion)
	}
	if got := slices.Sorted(slices.Values(c1Strings(t, doc, "required"))); !slices.Equal(got, []string{"action", "actor", "id", "time", "version"}) {
		t.Errorf("the audit event schema requires %v, and every event states who did it, what they did, when, under which id, in which version of this document", got)
	}
	if closed, ok := c1Node(t, doc, "additionalProperties").(bool); !ok || closed {
		t.Error("the audit event schema is not closed, and DecodeAuditEvent decodes strictly")
	}
}

// TestRosterAuditEventSubjectIsBounded: the subject is what a change was made
// to, and it names things of several grammars -- an address, a domain, a project
// name -- so both halves of the contract bound it rather than pattern it. The
// schema publishes the bound (maxLength 512) and AuditEvent.Validate refuses a
// longer one before the write, which is the only place it can be caught: the
// trail is create-only (D2811), so an entry the schema refuses is one no later
// writer can correct, and the schema is published to readers outside this
// binary.
func TestRosterAuditEventSubjectIsBounded(t *testing.T) {
	sch := c1Schema(t, c1AuditSchemaFile)
	doc := c1SchemaDocument(t, c1AuditSchemaFile)
	if workspace.MaxAuditSubjectBytes != 512 {
		t.Fatalf("MaxAuditSubjectBytes is %d and schemas/%s publishes another bound: the reader and the schema have to refuse one document", workspace.MaxAuditSubjectBytes, c1AuditSchemaFile)
	}
	if got := fmt.Sprint(c1Node(t, doc, "properties", "subject", "minLength")); got != "1" {
		t.Errorf("schemas/%s puts minLength %s on a subject, and a subject is the name of something -- an address, a domain, a project -- so it is never empty", c1AuditSchemaFile, got)
	}
	if got := fmt.Sprint(c1Node(t, doc, "properties", "subject", "maxLength")); got != fmt.Sprint(workspace.MaxAuditSubjectBytes) {
		t.Errorf("schemas/%s puts maxLength %s on a subject and MaxAuditSubjectBytes is %d: the schema is published to readers outside this binary, so the two have to be one bound", c1AuditSchemaFile, got, workspace.MaxAuditSubjectBytes)
	}

	event := c1AuditFixture()
	event.Subject = strings.Repeat("a", workspace.MaxAuditSubjectBytes)
	raw, err := workspace.EncodeAuditEvent(event)
	if err != nil {
		t.Fatalf("EncodeAuditEvent refuses a subject of exactly %d bytes, which is the bound: %v", workspace.MaxAuditSubjectBytes, err)
	}
	if err := c1Validate(t, sch, raw); err != nil {
		t.Errorf("schemas/%s refuses the event at the bound: %v\n%s", c1AuditSchemaFile, err, raw)
	}

	// One byte past it: the writer refuses, so the document the schema has to
	// refuse is the one at the bound with a longer subject written into it.
	event.Subject = strings.Repeat("a", workspace.MaxAuditSubjectBytes+1)
	if _, err := workspace.EncodeAuditEvent(event); err == nil {
		t.Fatal("EncodeAuditEvent writes an event whose subject is past the bound the schema publishes")
	} else if !strings.Contains(err.Error(), "MaxAuditSubjectBytes") {
		t.Errorf("EncodeAuditEvent refuses a subject past the bound with %q, and the refusal names the bound a caller has to meet", err)
	}
	long := c1Patched(t, raw, strings.Repeat("a", workspace.MaxAuditSubjectBytes), strings.Repeat("a", workspace.MaxAuditSubjectBytes+1))
	if err := c1Validate(t, sch, long); err == nil {
		t.Errorf("schemas/%s accepts a subject of %d bytes, and the reader refuses it\n%s", c1AuditSchemaFile, workspace.MaxAuditSubjectBytes+1, long)
	}
	if _, err := workspace.DecodeAuditEvent(long); err == nil {
		t.Errorf("DecodeAuditEvent accepts a subject of %d bytes, and the bound is the reader's half of a schema a reader outside this binary holds:\n%s", workspace.MaxAuditSubjectBytes+1, long)
	}
}

// TestRosterAuditEventIDIsAKeyAndAnInstant: the id is minted here and read
// everywhere -- it is the Firestore document id in the workspace's own
// collection (internal/workspace.AuditCollection) and the child key in a
// member's own audit node -- so it carries nothing either store refuses, and it
// opens with the instant it was minted, which is what makes a listing of the
// collection a listing in time order.
func TestRosterAuditEventIDIsAKeyAndAnInstant(t *testing.T) {
	now := time.Date(2026, 11, 20, 17, 58, 20, 0, time.UTC)
	id, err := workspace.NewAuditEventID(now, rand.Reader)
	if err != nil {
		t.Fatalf("NewAuditEventID: %v", err)
	}
	if !strings.HasPrefix(id, "20261120T175820Z-") {
		t.Errorf("the id is %q, and an event's id opens with the instant it was minted in UTC, so a listing in key order is a listing in time order to the second", id)
	}
	// The id is one both stores take: minting an event with it and validating
	// it against the schema is the assertion, because the schema's id pattern
	// is where the two grammars -- a Firestore document id and a Realtime
	// Database key -- are written down.
	event := c1AuditFixture()
	event.ID = id
	raw, err := workspace.EncodeAuditEvent(event)
	if err != nil {
		t.Fatalf("EncodeAuditEvent refuses the event NewAuditEventID minted an id for: %v", err)
	}
	if err := c1Validate(t, c1Schema(t, c1AuditSchemaFile), raw); err != nil {
		t.Errorf("schemas/%s refuses the id NewAuditEventID mints: %v\n%s", c1AuditSchemaFile, err, raw)
	}

	// Two events made in one second are two documents: the suffix is 8 random
	// bytes, and a second write of one id is Firestore's create-only refusal
	// and not a replacement of an audit entry.
	seen := map[string]bool{id: true}
	for i := 0; i < 32; i++ {
		next, err := workspace.NewAuditEventID(now, rand.Reader)
		if err != nil {
			t.Fatalf("NewAuditEventID: %v", err)
		}
		if seen[next] {
			t.Fatalf("NewAuditEventID answered %q twice for one instant", next)
		}
		seen[next] = true
	}

	// An id for an instant an hour later sorts after it, and a clock that is
	// not there at all is refused rather than minting an id for the zero
	// instant.
	later, err := workspace.NewAuditEventID(now.Add(time.Hour), rand.Reader)
	if err != nil {
		t.Fatalf("NewAuditEventID: %v", err)
	}
	if later <= id {
		t.Errorf("an id minted an hour later (%q) does not sort after %q", later, id)
	}
	if _, err := workspace.NewAuditEventID(time.Time{}, rand.Reader); err == nil {
		t.Error("NewAuditEventID mints an id for the zero instant, and an audit entry whose id names no instant is an entry nothing can order")
	}
	if _, err := workspace.NewAuditEventID(now, nil); err == nil {
		t.Error("NewAuditEventID mints an id with no source of randomness, and two events in one second would then be one document")
	}
}

// TestRosterMirrorEntryWireShape pins the one shape of the contract whose
// document another task owns: the entry a member's own mirror holds. The mirror
// node is C2's, and this type is the entry it carries, so what is fixed here is
// the entry's three keys and its validation -- a mirror a member's admin
// rewrote is advisory (D2813), and the reader of it is the command that prints
// the drift.
func TestRosterMirrorEntryWireShape(t *testing.T) {
	entry := workspace.MirrorEntry{Address: "alice@example.com", Kind: workspace.KindHuman}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var written map[string]any
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(written)); !slices.Equal(got, []string{"address", "external", "kind"}) {
		t.Errorf("a mirror entry is written as %v, and it carries the address, the kind and the external mark", got)
	}
	if err := entry.Validate(); err != nil {
		t.Errorf("a mirror entry of a human with no roles is refused: %v", err)
	}
	for _, bad := range []workspace.MirrorEntry{
		{Address: "alice", Kind: workspace.KindHuman},
		{Address: "alice@example.com"},
		{Address: "alice@example.com", Kind: "machine"},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("a mirror entry of %+v is accepted", bad)
		}
	}
}

// c1AuditFixture is an audit event with every optional field written: a member
// added to a project of the workspace.
func c1AuditFixture() workspace.AuditEvent {
	return workspace.AuditEvent{
		Version: workspace.AuditEventVersion,
		ID:      "20261120T175820Z-0f1e2d3c4b5a6978",
		Time:    time.Date(2026, 11, 20, 17, 58, 20, 0, time.UTC),
		Actor:   "alice@example.com",
		Action:  workspace.ActionUserAdd,
		Project: "acme-app",
		Subject: "bob@outside.test",
		Role:    workspace.RoleViewer,
	}
}

// c1Document is a document of the fixture, decoded: the test's own writer of
// the same JSON, so a case can hand the reader a document that differs in one
// place without the encoder refusing it first.
func c1Document(t *testing.T, r *workspace.Roster) map[string]any {
	t.Helper()
	raw, err := workspace.EncodeRoster(r)
	if err != nil {
		t.Fatalf("EncodeRoster refuses the fixture: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// c1Mutated is a document of the fixture with one difference: a fresh one every
// time, because a case that reached into a nested object would otherwise leave
// its change in the document the next case starts from.
func c1Mutated(t *testing.T, mutate func(doc map[string]any)) map[string]any {
	t.Helper()
	doc := c1Document(t, c1RosterFixture())
	mutate(doc)
	return doc
}

func c1Remarshal(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func c1RosterBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := workspace.EncodeRoster(c1RosterFixture())
	if err != nil {
		t.Fatalf("EncodeRoster refuses the fixture: %v", err)
	}
	return raw
}

func c1EventBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := workspace.EncodeAuditEvent(c1AuditFixture())
	if err != nil {
		t.Fatalf("EncodeAuditEvent refuses the fixture: %v", err)
	}
	return raw
}

// c1Patched is the fixture's document with one stretch of its bytes written
// differently. A key spelled in another case, or written twice, is what a
// document built from a Go map cannot carry -- two spellings of one key
// collapse in it -- so those cases are the encoded fixture, patched as text,
// and a patch that matched nothing is a failed test rather than a document
// nobody changed.
func c1Patched(t *testing.T, base []byte, old, new string) []byte {
	t.Helper()
	if !bytes.Contains(base, []byte(old)) {
		t.Fatalf("the fixture's document does not carry %q, so the patch would change nothing:\n%s", old, base)
	}
	return bytes.Replace(base, []byte(old), []byte(new), 1)
}

func c1Person(t *testing.T, doc map[string]any, i int) map[string]any {
	t.Helper()
	return c1Map(t, c1List(t, doc["people"])[i])
}

func c1Role(t *testing.T, doc map[string]any, person, i int) map[string]any {
	t.Helper()
	roles, ok := c1Person(t, doc, person)["roles"].([]any)
	if !ok || len(roles) <= i {
		t.Fatalf("the fixture's person %d carries no role %d: %v", person, i, c1Person(t, doc, person))
	}
	return c1Map(t, roles[i])
}

func c1Grant(t *testing.T, doc map[string]any, i int) map[string]any {
	t.Helper()
	return c1Map(t, c1List(t, doc["grants"])[i])
}

func c1Map(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%v is not an object", v)
	}
	return m
}

func c1List(t *testing.T, v any) []any {
	t.Helper()
	l, ok := v.([]any)
	if !ok {
		t.Fatalf("%v is not a list", v)
	}
	return l
}

// c1Names renders a list of the contract's named strings as strings, so an
// enum and a Go list can be compared as one list of spellings.
func c1Names[T ~string](list []T) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		out = append(out, string(v))
	}
	return out
}

// c1Schema compiles one of the two schema files this task adds, under its own
// $id, the way a consumer outside this repository would fetch it.
func c1Schema(t *testing.T, file string) *jsonschema.Schema {
	t.Helper()
	doc := c1SchemaDocument(t, file)
	id, _ := doc["$id"].(string)
	if id == "" {
		t.Fatalf("schemas/%s has no $id", file)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

func c1SchemaDocument(t *testing.T, file string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", file))
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return c1Map(t, inst)
}

// c1Validate validates a JSON document against a compiled schema. It is a
// t.Fatalf only when the document this test wrote is not JSON at all.
func c1Validate(t *testing.T, sch *jsonschema.Schema, raw []byte) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the document this test wrote is not JSON: %v\n%s", err, raw)
	}
	return sch.Validate(inst)
}

// c1Agrees holds the keys of one object of the document against the properties
// the schema declares for it, in both directions.
func c1Agrees(t *testing.T, file string, doc, written map[string]any, label string, at ...string) {
	t.Helper()
	props, ok := c1Node(t, doc, append(slices.Clone(at), "properties")...).(map[string]any)
	if !ok {
		t.Fatalf("schemas/%s's %s.properties is not an object", file, strings.Join(at, "."))
	}
	got, want := slices.Sorted(maps.Keys(written)), slices.Sorted(maps.Keys(props))
	if !slices.Equal(got, want) {
		t.Errorf("the document writes %v and schemas/%s declares %v for %s: a key one side knows and the other does not is a document valid in one place and refused in the other", got, file, want, label)
	}
}

// c1Node walks a decoded schema document to the value at path.
func c1Node(t *testing.T, doc map[string]any, path ...string) any {
	t.Helper()
	var cur any = doc
	for i, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%s is not an object", strings.Join(path[:i], "."))
		}
		v, ok := m[k]
		if !ok {
			t.Fatalf("the schema has no %s", strings.Join(path[:i+1], "."))
		}
		cur = v
	}
	return cur
}

// c1Strings is the string list at a path of a decoded schema document.
func c1Strings(t *testing.T, doc map[string]any, path ...string) []string {
	t.Helper()
	list := c1List(t, c1Node(t, doc, path...))
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("the schema's %s holds %v, which is not a string", strings.Join(path, "."), v)
		}
		out = append(out, s)
	}
	return out
}
