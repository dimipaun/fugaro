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
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
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
// same is true of a node that is present but empty: an empty list is not an
// absent one, and neither is a wildcard.
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

	f.Set(PathNode, json.RawMessage(`{"grants":[]}`))
	present, err := Read(ctx, db)
	if err != nil {
		t.Fatalf("Read of an empty node: %v", err)
	}
	if !present.Found || len(present.Grants) != 0 {
		t.Fatalf("Read of an empty node = %+v, want found with no grants", present)
	}
	in = Input{Grants: present.Grants, AllowRepos: []string{"github/acme/app"}}
	if got := Effective(in, alice, "github/acme/app"); got != RoleNone {
		t.Errorf("an empty node granted %s", got)
	}
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

// TestAccessNodeSchemaAgreesWithTheClosedSet: the JSON Schema in schemas/ is
// the authoring aid a reader validates a hand-written node against, and the Go
// reader is the authority. The two cannot drift: the schema's role enum is the
// closed set, its count cap is MaxGrants, and both levels refuse an unknown
// field, which is what makes the schema's field set the shape rather than a
// suggestion.
func TestAccessNodeSchemaAgreesWithTheClosedSet(t *testing.T) {
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
