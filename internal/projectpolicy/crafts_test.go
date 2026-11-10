package projectpolicy

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// The published crafts block (docs/model-crafts/spec.md §3, plan MC1): which
// credential or pool each craft's calls go out on, with the credentials and the
// pools it names beside it. A project that uses no craft must publish no member
// at all, which is the rule an empty clone_urls and an empty orchestrator are
// held to — and here it is a rule about a reader, not about tidiness: `crafts`
// is a key an older CLI does not know, and a member written for every project
// would make it refuse the node of a project that never asked for a craft.

// TestPolicyWithNoCraftsWritesNoMember: the member is a pointer for the reason
// Orchestrator's is (omitempty is a no-op on a struct), and the assertion is
// the marshal rather than the field, because the publisher marshals this struct
// straight to the node.
//
// The field behind it is a pointer and this test is written against one, so a
// value field is a build failure here rather than a red assertion; the document
// it would write was probed instead — json.Marshal of a struct carrying a bare
// config.Crafts with omitempty writes
// `"crafts":{"coder":{},"review":{},"senior-review":{},"security-review":{}}`,
// while the pointer writes nothing at all.
func TestPolicyWithNoCraftsWritesNoMember(t *testing.T) {
	raw, err := json.Marshal(&Policy{Version: Version})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "crafts") {
		t.Errorf("a policy with no craft writes a member: %s", raw)
	}
}

// TestCraftBlockNormalizesAnEmptyOne: a member written by another writer is
// read, and a block that sets no craft is no block — nil is what an absent
// member decodes to, so the two spellings of "this project sets no craft" are
// one answer and a policy that round trips through this package writes no
// member for it.
func TestCraftBlockNormalizesAnEmptyOne(t *testing.T) {
	for _, raw := range []string{
		`{"version":4}`,
		`{"version":4,"crafts":{}}`,
		`{"version":4,"crafts":null}`,
		`{"version":4,"crafts":{"coder":{},"review":{}}}`,
	} {
		p, found, err := Decode([]byte(raw))
		if err != nil || !found || p == nil {
			t.Fatalf("Decode(%s) = (%v, %v, %v), want a policy", raw, p, found, err)
		}
		if p.Crafts != nil {
			t.Errorf("%s decodes Crafts = %+v, want nil: nothing is set", raw, p.Crafts)
		}
		if b, err := json.Marshal(p); err != nil {
			t.Fatal(err)
		} else if strings.Contains(string(b), "crafts") {
			t.Errorf("a policy decoded from %s re-marshals with a crafts member: %s", raw, b)
		}
	}
}

// TestCraftBlockRoundTrips: what the writer writes is what the reader reads,
// and a node that carries a craft keeps it — the model and the access each on
// their own, since a craft may set one and inherit the other.
func TestCraftBlockRoundTrips(t *testing.T) {
	raw := []byte(`{"version":4,"crafts":{"coder":{"model":"claude-opus-5-5","access":"acme-main"},"security-review":{"model":"claude-sonnet-5"}}}`)
	p, found, err := Decode(raw)
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want a policy", p, found, err)
	}
	if p.Crafts == nil {
		t.Fatal("Crafts = nil, want the block")
	}
	if got := p.Crafts.Coder; got.Model != "claude-opus-5-5" || got.Access != "acme-main" {
		t.Errorf("coder = %+v, want the model and the access", got)
	}
	if got := p.Crafts.SecurityReview; got.Model != "claude-sonnet-5" || got.Access != "" {
		t.Errorf("security-review = %+v, want the model alone", got)
	}
	// The member this package wrote is the member it reads: the round trip is
	// taken of the member alone, a policy written by hand having no budget
	// block to decode.
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	back, _, err := Decode([]byte(`{"version":4,"crafts":` + string(doc["crafts"]) + `}`))
	if err != nil || back == nil || back.Crafts == nil {
		t.Fatalf("the member this package wrote does not decode = (%v, %v)", back, err)
	}
	if *back.Crafts != *p.Crafts {
		t.Errorf("the round trip = %+v, want %+v", *back.Crafts, *p.Crafts)
	}
}

// TestCraftMembersAreUnknownToAReaderThatPredatesThem: the three members are
// additive and optional, so no policy Version moves for them (version.go): what
// moves a version is a *runner* having to enforce something it does not know,
// and nothing here is enforced by one — the reader that resolves a craft's
// access is MC2's resolver, and it ships in the same release as the keys. The
// same node is decoded twice, once under this Fugaro's keys and once under a
// copy of the list as it stood before the three, and the reader that predates
// them lists them and goes on: that is the node a CLI of that age refuses at
// publish time (project_cmd.go's "publishing would drop them") and a runner of
// it logs (P8), which is why the publisher keeps them and writes none of them.
func TestCraftMembersAreUnknownToAReaderThatPredatesThem(t *testing.T) {
	if Version != 4 {
		t.Fatalf("the policy Version is %d, and this test's node is written for 4: update both together, deliberately", Version)
	}
	raw := []byte(`{"version":4,"budget":{"mode":"observe"},` +
		`"craft_credentials":[{"name":"acme-main","type":"openrouter"}],` +
		`"craft_pools":[{"name":"acme-pool","members":["acme-main"]}],` +
		`"crafts":{"coder":{"model":"claude-opus-5-5","access":"acme-pool"}}}`)
	p, found, err := Decode(raw)
	if err != nil || !found || p == nil {
		t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
	}
	if len(p.UnknownKeys) != 0 {
		t.Errorf("UnknownKeys = %v, want none: the three are keys this Fugaro reads", p.UnknownKeys)
	}
	if len(p.CraftCredentials) != 1 || len(p.CraftPools) != 1 || p.Crafts == nil {
		t.Errorf("the node was not read whole: %+v %+v %+v", p.CraftCredentials, p.CraftPools, p.Crafts)
	}

	preKey := make(map[string]bool, len(knownTopLevelKeys))
	for k, v := range knownTopLevelKeys {
		preKey[k] = v
	}
	for _, k := range []string{"crafts", "craft_credentials", "craft_pools"} {
		delete(preKey, k)
	}
	q, found, err := decodeWithKeys(raw, preKey)
	if err != nil || !found {
		t.Fatalf("the reader that predates the keys refused the node: (%v, %v, %v)", q, found, err)
	}
	if want := []string{"craft_credentials", "craft_pools", "crafts"}; !slices.Equal(q.UnknownKeys, want) {
		t.Errorf("the reader that predates the keys lists UnknownKeys = %v, want %v: that is the node a CLI of that age refuses and a runner of it logs", q.UnknownKeys, want)
	}
	if q.Budget.Mode != "observe" {
		t.Errorf("Budget.Mode = %q, want observe: the rest of the policy stays in force beside keys the reader does not know", q.Budget.Mode)
	}
}

// TestCraftBlockDecodesStrictly: the block is a closed set of four crafts whose
// members are a closed set of two, so a misspelling is refused by the decoder
// and never quietly sets nothing — the rule config.Crafts' struct shape exists
// for. A craft itself carries no credential: its access names one, which is the
// block beside it.
func TestCraftBlockDecodesStrictly(t *testing.T) {
	for _, tc := range []struct{ name, block, want string }{
		{name: "a craft that is not one", block: `{"codr":{"model":"claude-opus-5-5"}}`, want: `unknown field "codr"`},
		{name: "a member the craft does not have", block: `{"coder":{"credential":"acme-main"}}`, want: `unknown field "credential"`},
		{name: "an access that is not a name", block: `{"coder":{"access":"Not A Name"}}`, want: "must match"},
		{name: "the default account as an access", block: `{"coder":{"access":"default"}}`, want: "not a credential's name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, err := Decode([]byte(`{"version":4,"crafts":` + tc.block + `}`))
			if err == nil {
				t.Fatalf("the block %s was read: %+v", tc.block, p.Crafts)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal %q does not name %q", err, tc.want)
			}
		})
	}
}
