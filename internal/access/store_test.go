package access

// The grants node under config/admin/ (RS2): the read and the conditional
// write, and the four properties the plan pins — it decodes strictly, it
// refuses a role outside the closed set, an absent node grants nothing new, and
// it refuses a document past either bound. The rules half of the fence — no run
// token and no signed-in user reads the node — lives where the rules are
// generated (internal/budget/rules, TestNoRunTokenReadsTheAccessNode), because
// that is the package that can see the generated document.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// accessDB is a database acting as an operator: the reads and writes the node
// takes are the operator's own, over IAM, with no run token involved.
func accessDB(t *testing.T) (*gcpfake.RTDB, *rtdb.Client) {
	t.Helper()
	f := gcpfake.NewRTDB(t)
	c, err := rtdb.New(f.URL, rtdb.Auth{IDToken: func() string { return "operator-id-token" }})
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

// encoded is Encode for a document a test knows is valid.
func encoded(t *testing.T, grants ...Grant) []byte {
	t.Helper()
	raw, err := Encode(grants)
	if err != nil {
		t.Fatalf("Encode(%v): %v", grants, err)
	}
	return raw
}

// TestAccessNodeDecodesStrictly: the node's document is read whole or not at
// all. Every case here is a document a reader could be handed and must not act
// on: an unknown field (the older CLI that does not know a member must refuse
// the node, F27), a missing or null grants list, a second JSON value, a
// top-level array, a member that is not a `user:`, a member that is not the
// canonical spelling, and a scope ParseScope refuses.
func TestAccessNodeDecodesStrictly(t *testing.T) {
	grants, err := Decode(encoded(t,
		grant(alice, RoleOperator, scope(t, "github/acme/api-*")),
		grant(bob, RoleViewer, scope(t, "project")),
	))
	if err != nil {
		t.Fatalf("Decode of a document Encode wrote: %v", err)
	}
	if len(grants) != 2 || grants[0].User != alice || grants[0].Role != RoleOperator || grants[1].Scope.String() != "project" {
		t.Fatalf("Decode = %+v, want the two grants back", grants)
	}

	for name, raw := range map[string]string{
		"an unknown top-level field":     `{"grants":[],"operators":["user:alice@example.com"]}`,
		"an unknown field in a grant":    `{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"project","extra":1}]}`,
		"no grants list":                 `{}`,
		"a null grants list":             `{"grants":null}`,
		"a second JSON value":            `{"grants":[]}{"grants":[]}`,
		"a top-level array":              `[]`,
		"a top-level string":             `"grants"`,
		"a grant that is not an object":  `{"grants":["user:alice@example.com"]}`,
		"a member that is not a user":    `{"grants":[{"user":"group:eng@example.com","role":"viewer","scope":"project"}]}`,
		"a member with no user prefix":   `{"grants":[{"user":"alice@example.com","role":"viewer","scope":"project"}]}`,
		"a member that is not canonical": `{"grants":[{"user":"user:alice@EXAMPLE.com","role":"viewer","scope":"project"}]}`,
		"a scope that does not parse":    `{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"*"}]}`,
		"an empty scope":                 `{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":""}]}`,
	} {
		if got, err := Decode([]byte(raw)); err == nil {
			t.Errorf("%s: Decode accepted %s (got %+v)", name, raw, got)
		}
	}
}

// TestAccessNodeRefusesUnknownRole: the role is the closed set, spelled
// exactly. A role outside it — an administrator, a case variant, the empty
// string a missing key leaves — is refused at load rather than read as
// something, because a role this package did not parse grants nothing and a
// node carrying one is a node this reader cannot vouch for.
func TestAccessNodeRefusesUnknownRole(t *testing.T) {
	for _, role := range []string{"administrator", "Admin", "ADMIN", "", "owner", "launcher", "operator "} {
		raw := `{"grants":[{"user":"user:alice@example.com","role":"` + role + `","scope":"project"}]}`
		if _, err := Decode([]byte(raw)); err == nil {
			t.Errorf("Decode accepted the role %q", role)
		}
	}
	for _, role := range Roles() {
		raw := `{"grants":[{"user":"user:alice@example.com","role":"` + string(role) + `","scope":"project"}]}`
		if _, err := Decode([]byte(raw)); err != nil {
			t.Errorf("Decode refused the role %q: %v", role, err)
		}
	}
}

// TestAbsentAccessNodeGrantsNothingNew: an installation whose node has not been
// written is Found=false and holds no grants — the migrated lists' meaning,
// never "everyone". A caller the policy admits holds nothing from it, and the
// same is true of a document whose grants list is empty: the database keeps no
// member for an empty list, so there is no third "present and empty" state, and
// neither shape is a wildcard.
//
// The fold that makes those two one state is the database's, and gcpfake does
// not apply it (see foldAsTheDatabaseKeeps): the document is folded here, which
// is the state the read below answers for.
func TestAbsentAccessNodeGrantsNothingNew(t *testing.T) {
	ctx := context.Background()
	f, db := accessDB(t)

	n, err := Read(ctx, db)
	if err != nil {
		t.Fatalf("Read of an absent node: %v", err)
	}
	if n.Found || len(n.Grants) != 0 || n.ETag == "" {
		t.Fatalf("Read of an absent node = %+v, want not found, no grants, and the null ETag", n)
	}
	in := Input{Grants: n.Grants, AllowRepos: []string{"github/acme/app"}}
	if got := Effective(in, alice, "github/acme/app"); got != RoleNone {
		t.Errorf("an absent node granted %s: absent means the lists' meaning, never everyone", got)
	}

	// A document holding an empty grants list is the absent state, not a third
	// one: the database keeps no member for an empty list, so nothing is left at
	// the node. gcpfake stores the empty list it is handed rather than folding it
	// (foldAsTheDatabaseKeeps), so the document is folded here and what is
	// planted is the node the real database is left holding — the state the read
	// below answers for.
	var doc any
	if err := json.Unmarshal([]byte(`{"grants":[]}`), &doc); err != nil {
		t.Fatal(err)
	}
	f.Set(PathNode, foldAsTheDatabaseKeeps(doc))
	if v := f.Value(PathNode); v != nil {
		t.Fatalf("the database kept %#v at the node: a list is an object with numeric keys there, so an empty one is pruned (#1658)", v)
	}
	empty, err := Read(ctx, db)
	if err != nil {
		t.Fatalf("Read after writing an empty grants list: %v", err)
	}
	if empty.Found || len(empty.Grants) != 0 {
		t.Fatalf("Read after writing an empty grants list = %+v, want the absent node", empty)
	}
	in = Input{Grants: empty.Grants, AllowRepos: []string{"github/acme/app"}}
	if got := Effective(in, alice, "github/acme/app"); got != RoleNone {
		t.Errorf("a node with an empty grants list granted %s", got)
	}
}

// TestAccessNodeZeroGrantWriteLeavesTheAbsentState: the write of "no grants" is
// a real conditional write, and what it leaves is the absent node — the database
// keeps no member for an empty list, so {"grants":[]} is pruned whole and the
// node is gone (WriteIfMatch). Clearing the roster therefore lands the
// installation back in the state it started in, which grants nothing beyond the
// implicit admins.
//
// gcpfake stores the empty list it is handed and does not fold it
// (foldAsTheDatabaseKeeps), so the fold is applied here to the document the
// write actually left at the node, and the reads below answer for the node the
// real database holds. Teaching the fake to fold is not this change's to make:
// four unrelated cli tests seed nodes holding empty lists — states the real
// database cannot hold — and one of them pins a doctor line no installation can
// reach.
func TestAccessNodeZeroGrantWriteLeavesTheAbsentState(t *testing.T) {
	ctx := context.Background()
	f, db := accessDB(t)

	absent, err := Read(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteIfMatch(ctx, db, nil, absent.ETag); err != nil {
		t.Fatalf("WriteIfMatch of no grants on an absent node: %v", err)
	}
	if got, want := string(mustJSON(t, f.Value(PathNode))), `{"grants":[]}`; got != want {
		t.Fatalf("the write left %s at the node, want the zero-grants document %s", got, want)
	}
	f.Set(PathNode, foldAsTheDatabaseKeeps(f.Value(PathNode)))
	if v := f.Value(PathNode); v != nil {
		t.Fatalf("the database holds %#v at the node: an empty grants list is an empty object there and is pruned (#1658)", v)
	}

	if err := WriteIfMatch(ctx, db, []Grant{grant(alice, RoleViewer, Scope{Kind: ScopeProject})}, absent.ETag); err != nil {
		t.Fatalf("WriteIfMatch of one grant: %v", err)
	}
	held, err := Read(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !held.Found || len(held.Grants) != 1 {
		t.Fatalf("Read = %+v, want the one grant just written", held)
	}
	if err := WriteIfMatch(ctx, db, nil, held.ETag); err != nil {
		t.Fatalf("WriteIfMatch of no grants: %v", err)
	}
	f.Set(PathNode, foldAsTheDatabaseKeeps(f.Value(PathNode)))
	if v := f.Value(PathNode); v != nil {
		t.Fatalf("the database holds %#v at the node: the emptied list is pruned with it", v)
	}
	after, err := Read(ctx, db)
	if err != nil {
		t.Fatalf("Read after clearing the node: %v", err)
	}
	if after.Found || len(after.Grants) != 0 {
		t.Fatalf("Read after clearing the node = %+v, want the absent node that grants nothing new", after)
	}
}

// foldAsTheDatabaseKeeps returns what the Realtime Database keeps of a written
// value: a list is an object with numeric keys there, so an empty list is an
// empty object and is pruned whole, and an object left with no member is gone
// (#1658, the finding internal/cli's sameNode and dropAbsentMembers carry for
// the same reason). It walks the JSON model, which is what the fake hands back.
//
// It is stated here because gcpfake's normalise keeps the empty list it is
// given: the fake is shared with tests that seed states the real database cannot
// hold, so the fold this node's contract rests on is applied where the contract
// is tested rather than changed under those tests.
func foldAsTheDatabaseKeeps(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, c := range x {
			if c = foldAsTheDatabaseKeeps(c); c != nil {
				out[k] = c
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		if len(x) == 0 {
			return nil
		}
		return x
	default:
		return v
	}
}

// mustJSON renders a stored node for an error message.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling %#v: %v", v, err)
	}
	return b
}

// TestAccessNodeOverCapRefused: both bounds hold on both sides. A document past
// MaxNodeBytes is refused before it is decoded, a list past MaxGrants is
// refused, and Encode refuses to write what Decode would refuse to read. A
// node exactly at the count cap is accepted, so the cap bounds the node rather
// than one short of it.
func TestAccessNodeOverCapRefused(t *testing.T) {
	big := append([]byte(`{"grants":[]}`), bytes.Repeat([]byte(" "), MaxNodeBytes)...)
	if _, err := Decode(big); err == nil {
		t.Errorf("Decode accepted a %d-byte node; at most %d are allowed", len(big), MaxNodeBytes)
	}

	if _, err := Decode(manyGrants(MaxGrants + 1)); err == nil {
		t.Errorf("Decode accepted %d grants; at most %d are allowed", MaxGrants+1, MaxGrants)
	}
	if _, err := Decode(manyGrants(MaxGrants)); err != nil {
		t.Errorf("Decode refused %d grants, the cap itself: %v", MaxGrants, err)
	}

	atCap := make([]Grant, MaxGrants)
	for i := range atCap {
		atCap[i] = grant(alice, RoleViewer, Scope{Kind: ScopeProject})
	}
	if _, err := Encode(atCap); err != nil {
		t.Errorf("Encode refused %d grants, the cap itself: %v", MaxGrants, err)
	}
	if _, err := Encode(append(atCap, grant(bob, RoleViewer, Scope{Kind: ScopeProject}))); err == nil {
		t.Errorf("Encode accepted %d grants; at most %d are allowed", MaxGrants+1, MaxGrants)
	}

	// The count cap does not bound the bytes on its own: MaxGrants grants of a
	// scope at projectpolicy.MaxPatternLen are ~80 KiB, and Encode must refuse
	// that document for its size rather than write it. A write that skipped this
	// check would store a node Decode refuses, so the read of the node it just
	// wrote would fail — the node no installation could read (the review finding
	// on this change). The document is exactly at the count cap, so the count
	// check cannot be what refuses it and the error must name the byte cap.
	fat := make([]Grant, MaxGrants)
	for i := range fat {
		fat[i] = grant(alice, RoleViewer, scope(t, longScope(projectpolicy.MaxPatternLen)))
	}
	raw, err := Encode(fat)
	if err == nil {
		t.Errorf("Encode wrote a %d-byte node of %d grants at the count cap; at most %d bytes are allowed, and Decode refuses the node it wrote", len(raw), MaxGrants, MaxNodeBytes)
	} else if !strings.Contains(err.Error(), "bytes") {
		t.Errorf("Encode refused %d grants at the count cap with %v, want the byte cap: the count cap is %d", MaxGrants, err, MaxGrants)
	}
}

// manyGrants is a node document holding n identical grants, as bytes, so a cap
// test does not go through Encode (which refuses the very document the Decode
// half must refuse).
func manyGrants(n int) []byte {
	entries := make([]nodeGrant, n)
	for i := range entries {
		entries[i] = nodeGrant{User: alice, Role: string(RoleViewer), Scope: "project"}
	}
	raw, err := json.Marshal(nodeDoc{Grants: &entries})
	if err != nil {
		panic(err)
	}
	return raw
}

// TestAccessNodeRoundTripsThroughTheDatabase: the read and the conditional
// write, over a real (fake) database. A grant written at the null ETag lands
// and reads back; the write that judged the now-stale ETag is refused with
// rtdb.ErrPrecondition, which is what makes two concurrent writers re-judge
// (F23); the write carrying the fresh ETag lands.
func TestAccessNodeRoundTripsThroughTheDatabase(t *testing.T) {
	ctx := context.Background()
	f, db := accessDB(t)

	absent, err := Read(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	want := []Grant{
		grant(alice, RoleAdmin, scope(t, "github/acme/api-*")),
		grant(bob, RoleViewer, Scope{Kind: ScopeAllRepos}),
	}
	if err := WriteIfMatch(ctx, db, want, absent.ETag); err != nil {
		t.Fatalf("WriteIfMatch at the null ETag: %v", err)
	}
	if f.Value(PathNode) == nil {
		t.Fatal("nothing was written at the node")
	}

	got, err := Read(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Found || len(got.Grants) != 2 || got.ETag == absent.ETag {
		t.Fatalf("Read after the write = %+v, want two grants and a new ETag", got)
	}
	if got.Grants[0].User != alice || got.Grants[0].Role != RoleAdmin || got.Grants[1].Scope.Kind != ScopeAllRepos {
		t.Errorf("Read = %+v, want the grants back with their roles and scopes", got.Grants)
	}

	err = WriteIfMatch(ctx, db, []Grant{grant(bob, RoleViewer, Scope{Kind: ScopeProject})}, absent.ETag)
	if !errors.Is(err, rtdb.ErrPrecondition) {
		t.Errorf("WriteIfMatch with the stale ETag = %v, want rtdb.ErrPrecondition", err)
	}
	if err := WriteIfMatch(ctx, db, []Grant{grant(bob, RoleViewer, Scope{Kind: ScopeProject})}, got.ETag); err != nil {
		t.Errorf("WriteIfMatch with the fresh ETag: %v", err)
	}
}

// TestAccessNodeWriteRefusesWhatDecodeWouldRefuse: a writer cannot store a
// document its own reader would refuse. An unknown role, an unparsable scope
// and a member that is not a canonical user are all refused by Encode, so the
// node never holds an entry Decode would halt on.
func TestAccessNodeWriteRefusesWhatDecodeWouldRefuse(t *testing.T) {
	for name, g := range map[string]Grant{
		"an unknown role":          {User: alice, Role: Role("administrator"), Scope: Scope{Kind: ScopeProject}},
		"a scope never parsed":     {User: alice, Role: RoleAdmin, Scope: Scope{Kind: ScopeRepos}},
		"a member that is not one": {User: "group:eng@example.com", Role: RoleViewer, Scope: Scope{Kind: ScopeProject}},
		"a member not canonical":   {User: "user:alice@EXAMPLE.com", Role: RoleViewer, Scope: Scope{Kind: ScopeProject}},
	} {
		if _, err := Encode([]Grant{g}); err == nil {
			t.Errorf("Encode accepted %s", name)
		}
	}
}

// TestAccessNodeWriteStoresTheCanonicalSpelling: the node holds one spelling of
// a scope, whatever the caller held. A Scope built by hand rather than by
// ParseScope can carry a variant Valid accepts — an upper-case pattern, a
// trailing ".git" — and writing it as written would put a second spelling of one
// scope in the node, so a read would hand back a Scope the caller did not write
// and `fugaro user ls` could print two names for one repository. The write
// canonicalises: both spellings here are one scope, and the node holds it once.
func TestAccessNodeWriteStoresTheCanonicalSpelling(t *testing.T) {
	ctx := context.Background()
	f, db := accessDB(t)

	hand := Grant{
		User:  alice,
		Role:  RoleViewer,
		Scope: Scope{Kind: ScopeRepos, Repos: []string{"GitHub/ACME/APP", "github/acme/app.git"}},
	}
	if !hand.Scope.Valid() {
		t.Fatalf("%+v is not a scope Valid accepts, so it does not test the write", hand.Scope)
	}
	absent, err := Read(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteIfMatch(ctx, db, []Grant{hand}, absent.ETag); err != nil {
		t.Fatalf("WriteIfMatch: %v", err)
	}

	stored, err := json.Marshal(f.Value(PathNode))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("ACME")) || bytes.Contains(stored, []byte(".git")) {
		t.Errorf("the node holds %s: the write stored the caller's spelling rather than the canonical one", stored)
	}
	got, err := Read(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Grants) != 1 || !slices.Equal(got.Grants[0].Scope.Repos, []string{"github/acme/app"}) {
		t.Errorf("Read = %+v, want the one canonical pattern the two spellings name", got.Grants)
	}
}

// nodeSchema compiles schemas/access.schema.json under its own $id, the way a
// person validating a hand-written node would.
func nodeSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "access.schema.json"))
	if err != nil {
		t.Fatalf("reading the node's schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the node's schema is not JSON: %v", err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("the node's schema carries no $id, so nothing can compile it")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatalf("compiling the node's schema: %v", err)
	}
	return sch
}

// schemaAccepts reports whether the schema accepts one document. A document
// that is not JSON at all is one the schema cannot accept, which is what Decode
// says of it too.
func schemaAccepts(t *testing.T, sch *jsonschema.Schema, doc string) bool {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(doc))
	if err != nil {
		return false
	}
	return sch.Validate(inst) == nil
}

// readerAccepts reports whether the node's reader accepts one document.
func readerAccepts(doc string) bool {
	_, err := Decode([]byte(doc))
	return err == nil
}

// longScope is a one-entry scope of exactly n characters: a provider and an
// owner, then a repository name that fills the rest. projectpolicy.MaxPatternLen
// is the longest entry the reader accepts, so n at that length is the boundary
// and n past it is over.
func longScope(n int) string {
	const prefix = "github/acme/"
	return prefix + strings.Repeat("a", n-len(prefix))
}

// overByteCapNode is the document that pins the one gap between the schema and
// the reader: MaxGrants grants whose scopes each sit exactly at
// projectpolicy.MaxPatternLen, which is the largest document the schema's own
// bounds let a node reach. Every entry of it is one the schema accepts; the
// document is over MaxNodeBytes, so the reader refuses it. It is built from
// nodeGrant rather than through Encode, which refuses the very document the
// Decode half must refuse.
func overByteCapNode(t *testing.T) string {
	t.Helper()
	grants := make([]nodeGrant, MaxGrants)
	for i := range grants {
		grants[i] = nodeGrant{User: alice, Role: string(RoleViewer), Scope: longScope(projectpolicy.MaxPatternLen)}
	}
	raw, err := json.Marshal(nodeDoc{Grants: &grants})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= MaxNodeBytes {
		t.Fatalf("the document is %d bytes, at most %d are allowed: it does not pin the gap between the schema and the reader's byte cap", len(raw), MaxNodeBytes)
	}
	return string(raw)
}

// TestAccessNodeSchemaAndReaderAgree: the schema in schemas/ is the aid a person
// validates a hand-written node against, and internal/access.Decode is the
// authority that reads it. The two are held together here in the one direction
// that matters — every document the schema accepts must be one the reader
// accepts — because the other way round is how a node that passes the schema
// goes on to halt every CLI read of it (the review finding on this change).
//
// One bound is out of reach and is pinned by name instead: the reader's byte cap
// is on the serialized document, which no keyword of this schema counts, so a
// node the schema accepts can still be over MaxNodeBytes. overByteCapNode below
// is that node — every entry in it is one the schema accepts, and the document
// as a whole is over the cap — so the gap is a case in this corpus rather than
// something a later review rediscovers.
//
// The schema is deliberately stricter than the reader in the spelling it
// requires: it spells the canonical form Encode stores, while Decode folds a
// variant rather than refusing it. The strictness is pinned here rather than
// left implicit, and the other half of the agreement — that everything the
// writer writes validates — is pinned with it, so a schema no installation
// could satisfy fails as loudly as a schema that lets a bad node through.
func TestAccessNodeSchemaAndReaderAgree(t *testing.T) {
	sch := nodeSchema(t)

	// Documents both accept: an empty node, one grant of each role and scope
	// kind, a list of two patterns, a nested repository path, a wildcard, and
	// the four-wildcard cap itself.
	for _, doc := range []string{
		`{"grants":[]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"project"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"operator","scope":"all-repos"}]}`,
		`{"grants":[{"user":"user:Alice@example.com","role":"admin","scope":"github/acme/api-*"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/acme/app,bitbucket/other/thing"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/acme/group/nested"}]}`,
		`{"grants":[{"user":"user:a.b+tag@example.com","role":"viewer","scope":"github/*/app"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/*/*/*/*"}]}`,
		// Four wildcards is the cap per entry, not per scope, and the length
		// bound is per entry too: both are documents the reader accepts and the
		// writer can write, so the schema must accept them.
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/*/*/*,github/acme/*/*/*"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"` + longScope(projectpolicy.MaxPatternLen) + `"}]}`,
		// Wildcards inside a segment, which the reader allows and the writer
		// writes: Encode stores what ParseScope parsed, and ParseScope refuses
		// only a doubled "*" (it matches inside one segment and never crosses
		// "/"). A pattern that let a segment carry one wildcard refused both of
		// these (the review finding on this change).
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/acme/a*b*c"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/acme/*b*"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/a*b/c*,bitbucket/other/*app*"}]}`,
	} {
		if !readerAccepts(doc) {
			t.Errorf("Decode refused %s: the corpus of documents both accept is wrong", doc)
		}
		if !schemaAccepts(t, sch, doc) {
			t.Errorf("the schema refused %s, a document the reader accepts and the writer can write", doc)
		}
	}

	// Documents the schema must refuse, every one of them a document Decode
	// refuses too. This is the finding's own list — a member that is not the
	// canonical user, a control byte, a scope of "*", "nonsense" or
	// "project,all-repos" — plus the rest of the grammar a hand-written node
	// gets wrong: an unknown field, a role outside the closed set, an empty or
	// missing scope, a pattern that names no provider, a doubled star, a fifth
	// wildcard and a "." or ".." segment.
	for _, doc := range []string{
		`{}`,
		`{"grants":null}`,
		`[]`,
		`"grants"`,
		`{"grants":[]}{"grants":[]}`,
		`{"grants":[],"operators":["user:alice@example.com"]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"project","extra":1}]}`,
		`{"grants":["user:alice@example.com"]}`,
		`{"grants":[{"user":"group:eng@example.com","role":"viewer","scope":"project"}]}`,
		`{"grants":[{"user":"alice@example.com","role":"viewer","scope":"project"}]}`,
		`{"grants":[{"user":"user:alice@EXAMPLE.com","role":"viewer","scope":"project"}]}`,
		`{"grants":[{"user":"user:alice@@example.com","role":"viewer","scope":"project"}]}`,
		`{"grants":[{"user":"user:alice@exa mple.com","role":"viewer","scope":"project"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"administrator","scope":"project"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"","scope":"project"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"*"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":""}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"nonsense"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"project,all-repos"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"project,github/acme/app"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/acme"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/acme/"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"*/acme/app"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"foo/acme/app"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/acme/**"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/acme/.."}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/*/*/*/*/*"}]}`,
		// A fifth wildcard in one entry, past the cap; a fifth in a second entry
		// is not past it and is accepted above.
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/*/*/*,github/acme/*/*/*/*/*"}]}`,
		// An entry past projectpolicy.MaxPatternLen, which the reader refuses
		// though the scope as a whole is well under any byte cap.
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"` + longScope(projectpolicy.MaxPatternLen+1) + `"}]}`,
	} {
		if readerAccepts(doc) {
			t.Errorf("Decode accepted %s: the corpus of refused documents is wrong", doc)
		}
		if schemaAccepts(t, sch, doc) {
			t.Errorf("the schema accepted %s, which Decode refuses: a hand-written node passing the schema would halt every CLI read of it", doc)
		}
	}

	// The one gap the schema cannot close, and the reason this test states its
	// claim as "everything the schema accepts, except the byte cap". A JSON
	// Schema counts members and string lengths, never the serialized document,
	// so the document below — MaxGrants grants, each scope at
	// projectpolicy.MaxPatternLen, every entry one the schema accepts — passes
	// the schema and is over MaxNodeBytes. The reader refuses it, which is the
	// fail-closed answer and the only one available to it; the byte cap is the
	// reader's, and a schema cannot make the two agree about it. The one keyword
	// that could is maxItems, and it cannot here: 256 grants of a 255-character
	// scope are about 80 KiB while 256 short ones are about 17 KiB, so any count
	// low enough to bound the bytes refuses documents Encode writes.
	over := overByteCapNode(t)
	if !schemaAccepts(t, sch, over) {
		t.Errorf("the schema refused the %d-byte document of %d grants: the one gap between it and the reader is not the byte cap", len(over), MaxGrants)
	}
	if _, err := Decode([]byte(over)); err == nil || !strings.Contains(err.Error(), "bytes; at most") {
		t.Errorf("Decode of the %d-byte document = %v, want the byte cap's own refusal", len(over), err)
	}

	// Documents the schema refuses and the reader folds and accepts, because
	// the schema spells the canonical form the writer stores and the reader
	// normalises a variant rather than refusing it. Listed so the strictness is
	// deliberate and visible: the reader is the authority, and a node the
	// schema refuses here is still read.
	for _, doc := range []string{
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"GitHub/ACME/APP"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":"github/acme/app.git"}]}`,
		`{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":" project "}]}`,
	} {
		if !readerAccepts(doc) {
			t.Errorf("Decode refused %s: the reader is supposed to fold this spelling", doc)
		}
		if schemaAccepts(t, sch, doc) {
			t.Errorf("the schema accepted %s: it spells the canonical form the writer stores", doc)
		}
	}

	// Everything Encode writes validates, which is the other half: a schema
	// that refused a document the writer writes would be a schema no
	// installation could satisfy. The last grant is built by hand with two
	// spellings of one scope, so this holds the writer to the canonical
	// spelling as well.
	for _, grants := range [][]Grant{
		{{User: alice, Role: RoleViewer, Scope: Scope{Kind: ScopeProject}}},
		{{User: bob, Role: RoleOperator, Scope: Scope{Kind: ScopeAllRepos}}},
		{grant(alice, RoleAdmin, scope(t, "github/acme/api-*"))},
		{grant(alice, RoleViewer, scope(t, "github/acme/app,bitbucket/other/thing"))},
		{grant(bob, RoleViewer, scope(t, "github/acme/group/nested"))},
		// Six wildcards across two entries: the cap is per entry, so this is a
		// scope the writer writes and the schema must accept.
		{grant(alice, RoleViewer, scope(t, "github/*/*/*,github/acme/*/*/*"))},
		{{User: alice, Role: RoleViewer, Scope: Scope{Kind: ScopeRepos, Repos: []string{"GitHub/ACME/APP", "github/acme/app.git"}}}},
	} {
		raw, err := Encode(grants)
		if err != nil {
			t.Fatalf("Encode(%+v): %v", grants, err)
		}
		if !schemaAccepts(t, sch, string(raw)) {
			t.Errorf("Encode wrote %s, which the schema refuses", raw)
		}
	}
}

// TestAccessNodeSchemaRefusesNothingTheReaderReads: the corpus above pins the
// cases a person thought of, and both review findings on this schema were cases
// nobody thought of — a member spelling, a control byte, a star inside a
// segment — so the space is enumerated here rather than sampled: every scope the
// generator below can build, in both directions.
//
// The first direction is the dangerous one, and it is the whole claim of the
// schema: a document it accepts must be one Decode accepts, or a hand-written
// node that passes the schema halts every CLI read of it. The second is the
// writer's half of the same agreement: every spelling ParseScope accepts has a
// canonical form, and that form — the one Encode stores — must validate, or the
// schema would refuse a node the CLI itself writes.
func TestAccessNodeSchemaRefusesNothingTheReaderReads(t *testing.T) {
	sch := nodeSchema(t)

	// The pieces are the grammar's own edges: an empty segment, the wildcard
	// alone and inside a segment, a doubled and a tripled star, "." and "..",
	// the characters a repository name may carry and the ones it may not, an
	// upper-case spelling, and a provider no repository can carry.
	pieces := []string{"", "*", "a", "a*", "*a", "a*b", "a**", "a***b", "*a*", ".", "..", "a.b", "a.b.c", "A", "a b", "a+b", "_", "-"}
	providers := []string{"github", "bitbucket", "foo", "*", "", "GITHUB"}
	var scopes []string
	for _, p := range providers {
		for _, a := range pieces {
			for _, b := range pieces {
				scopes = append(scopes, p+"/"+a+"/"+b, p+"/"+a+"/"+b+"/"+a, p+"/"+a+"/"+b+","+p+"/"+a+"/"+b)
			}
		}
	}
	scopes = append(scopes,
		"project", "all-repos", "*", "", "project,github/a/b", "github/a/b,", ",github/a/b",
		" github/a/b ", "GITHUB/A/B", "github/a/b.git", "github/a/b.git.git", "github/a.git/b",
	)
	if len(scopes) < 5000 {
		t.Fatalf("the generator built %d scopes: it is the check's whole coverage, so a shortened one is a weakened test", len(scopes))
	}

	for _, s := range scopes {
		doc := `{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":` + strconv.Quote(s) + `}]}`
		if schemaAccepts(t, sch, doc) && !readerAccepts(doc) {
			t.Errorf("the schema accepted the scope %q, which Decode refuses: a hand-written node passing the schema halts every CLI read of it", s)
		}
		if canon, err := ParseScope(s); err == nil {
			canonical := `{"grants":[{"user":"user:alice@example.com","role":"viewer","scope":` + strconv.Quote(canon.String()) + `}]}`
			if !readerAccepts(canonical) {
				t.Errorf("Decode refused %q, the canonical spelling it parsed from %q: the reader cannot read back what it read", canon.String(), s)
			}
			if !schemaAccepts(t, sch, canonical) {
				t.Errorf("the schema refused %q, the canonical spelling of %q the writer stores", canon.String(), s)
			}
		}
	}
}

// TestAccessNodeSchemaPinsTheClosedSet: the names and numbers the schema shares
// with the Go package, held to it so the two cannot drift. The count cap is
// MaxGrants, the role enum is the closed set, both levels refuse an unknown
// field — which is what makes the schema's field set the shape rather than a
// suggestion — and the providers a pattern may name are config.Providers, the
// kinds a canonical repository can carry.
func TestAccessNodeSchemaPinsTheClosedSet(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "access.schema.json"))
	if err != nil {
		t.Fatalf("reading the node's schema: %v", err)
	}
	var doc struct {
		Type                 string `json:"type"`
		AdditionalProperties bool   `json:"additionalProperties"`
		Required             []string
		Properties           struct {
			Grants struct {
				Type     string `json:"type"`
				MaxItems int    `json:"maxItems"`
				Items    struct {
					AdditionalProperties bool     `json:"additionalProperties"`
					Required             []string `json:"required"`
					Properties           struct {
						Role struct {
							Enum []string `json:"enum"`
						} `json:"role"`
						Scope json.RawMessage `json:"scope"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"grants"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the node's schema is not JSON: %v", err)
	}
	if doc.Type != "object" || !slices.Contains(doc.Required, "grants") {
		t.Errorf("the schema's top level is %q with required %v, want an object requiring grants", doc.Type, doc.Required)
	}
	if doc.AdditionalProperties {
		t.Error("the schema's top level accepts unknown fields: the reader refuses them (DisallowUnknownFields)")
	}
	g := doc.Properties.Grants
	if g.Type != "array" || g.MaxItems != MaxGrants {
		t.Errorf("the schema's grants is %q with maxItems %d, want an array of at most MaxGrants (%d)", g.Type, g.MaxItems, MaxGrants)
	}
	if g.Items.AdditionalProperties {
		t.Error("the schema accepts an unknown field in a grant: the reader refuses them")
	}
	for _, key := range []string{"user", "role", "scope"} {
		if !slices.Contains(g.Items.Required, key) {
			t.Errorf("the schema does not require %q in a grant", key)
		}
	}
	var want []string
	for _, r := range Roles() {
		want = append(want, string(r))
	}
	if !slices.Equal(g.Items.Properties.Role.Enum, want) {
		t.Errorf("the schema's role enum = %v, the closed set = %v", g.Items.Properties.Role.Enum, want)
	}
	providers := "(" + strings.Join(config.Providers, "|") + ")"
	if !bytes.Contains(g.Items.Properties.Scope, []byte(providers)) {
		t.Errorf("the scope pattern does not name the providers %v: a provider no repository can carry would pass the schema and halt the reader", config.Providers)
	}
}

// TestAccessNodeScopeStringRoundTrips: Scope.String is ParseScope's inverse on
// the values this package builds, which is what lets the node store a scope as
// the string a person types and read it back as the same Scope. The rendering
// is the canonical one — patterns sorted, as ParseScope stores them — and an
// invalid scope renders as the empty string, which ParseScope refuses. The
// invalid half matters: a project-kind scope carrying repositories would
// otherwise render as "project", the widest scope there is.
func TestAccessNodeScopeStringRoundTrips(t *testing.T) {
	for _, c := range []struct{ value, want string }{
		{"project", "project"},
		{"all-repos", "all-repos"},
		{"github/acme/app", "github/acme/app"},
		{"github/acme/api-*", "github/acme/api-*"},
		{"github/acme/*,bitbucket/other/thing", "bitbucket/other/thing,github/acme/*"},
	} {
		s := scope(t, c.value)
		if got := s.String(); got != c.want {
			t.Errorf("ParseScope(%q).String() = %q, want %q", c.value, got, c.want)
		}
		back, err := ParseScope(s.String())
		if err != nil || back.Kind != s.Kind || !slices.Equal(back.Repos, s.Repos) {
			t.Errorf("ParseScope(%q) = (%+v, %v), want %+v back", s.String(), back, err, s)
		}
	}
	for _, s := range []Scope{{}, {Kind: ScopeNone}, {Kind: ScopeRepos}, {Kind: ScopeProject, Repos: []string{"github/acme/app"}}} {
		if got := s.String(); got != "" {
			t.Errorf("the invalid scope %+v renders as %q, want the empty string ParseScope refuses", s, got)
		}
		if _, err := ParseScope(s.String()); err == nil {
			t.Errorf("ParseScope(%q) accepted an invalid scope's rendering", s.String())
		}
	}
}
