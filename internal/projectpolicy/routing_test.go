package projectpolicy

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// routing_test.go is the published routing table (RT4 of
// docs/model-credentials-routing, R21, F49): the one new top-level key a
// project's runs and the credentials service are served by, and the two facts
// the compatibility story rests on — the key is read by this Fugaro and
// tolerated by a reader of the release behind, and a table the checks refuse is
// refused here rather than by the run that cannot start.

// routingNode is the node a project that has published a table holds: one
// project credential, the pool of it, the craft whose model the entry answers,
// and the key itself. The pools member is the craft_pools pool with the three
// members only this key may carry.
func routingNode(table string) string {
	return `{"version": ` + itoa(Version) + `,` +
		`"budget": {"mode": "enforce", "per_run_usd": 5},` +
		`"craft_credentials": [{"name": "acme-main", "type": "anthropic"}],` +
		`"craft_pools": [{"name": "acme-pool", "members": ["acme-main"], "owner": "owner@example.com"}],` +
		`"crafts": {"coder": {"model": "claude-opus-5-5"}},` +
		`"model_routing": ` + table + `}`
}

// routingTable is the table a migrated project publishes: one entry answering
// the craft's model through the project's pool, with the admission written on
// the source (D3182) and the pool in its new shape.
const routingTable = `{"entries": [{"match": "claude-*", "chain": [{"kind": "pool", "pool": "acme-pool", "admit": ["github/acme/app"]}]}],` +
	`"pools": [{"name": "acme-pool", "members": ["acme-main"], "owner": "owner@example.com", "rule": "ordered", "principal": {"kind": "project", "id": "aurora"}}]}`

// TestPolicyCarriesModelRouting: the node reads the key — the table, the
// admissions on its sources and the pools in the shape PL1 gives them — and
// writes it back, so a run of this release and the credentials service are
// served by one document. The refusals below are the same read from the other
// side: a table routing.Problems refuses (RT3) is a table this package refuses,
// which is what keeps a project from publishing a table no run can start on.
func TestPolicyCarriesModelRouting(t *testing.T) {
	t.Run("carries the table", func(t *testing.T) {
		p, found, err := Decode([]byte(routingNode(routingTable)))
		if err != nil || !found || p == nil {
			t.Fatalf("Decode = (%v, %v, %v), want the node read", p, found, err)
		}
		if len(p.UnknownKeys) != 0 {
			t.Errorf("UnknownKeys = %v, want none: model_routing is a key this Fugaro reads", p.UnknownKeys)
		}
		if p.ModelRouting == nil {
			t.Fatal("the node carries no model_routing")
		}
		if len(p.ModelRouting.Entries) != 1 || p.ModelRouting.Entries[0].Match != "claude-*" {
			t.Fatalf("Entries = %+v, want the one entry the node wrote", p.ModelRouting.Entries)
		}
		src := p.ModelRouting.Entries[0].Chain
		if len(src) != 1 || src[0].Kind != "pool" || src[0].Pool != "acme-pool" {
			t.Fatalf("the entry's chain = %+v, want the pool the node wrote", src)
		}
		if want := []string{"github/acme/app"}; !slices.Equal(src[0].Admit, want) {
			t.Errorf("the source's admit list = %v, want %v: the admissions travel on the entry's own sources (D3182)", src[0].Admit, want)
		}
		if len(p.ModelRouting.Pools) != 1 {
			t.Fatalf("Pools = %+v, want the one pool", p.ModelRouting.Pools)
		}
		pool := p.ModelRouting.Pools[0]
		if pool.Rule != "ordered" {
			t.Errorf("the pool's rule = %q, want the config's %q: the pools travel in their new shape here (R21)", pool.Rule, "ordered")
		}
		if pool.Principal == nil || pool.Principal.Kind != "project" || pool.Principal.ID != "aurora" {
			t.Errorf("the pool's principal = %+v, want the node's own", pool.Principal)
		}
		if pool.Members[0] != "acme-main" || pool.Owner != "owner@example.com" {
			t.Errorf("the pool lost members or its owner: %+v", pool)
		}

		// The member is one the publisher writes, so what is decoded is what is
		// encoded: a member this package reads and does not write back would
		// drop the table from the node on the next publish of a config that
		// states none.
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		back, found, err := Decode(b)
		if err != nil || !found || back.ModelRouting == nil {
			t.Fatalf("the node this package wrote does not decode back = (%v, %v, %v)", back, found, err)
		}
		if back.ModelRouting.Entries[0].Match != "claude-*" || back.ModelRouting.Pools[0].Rule != "ordered" {
			t.Errorf("the round trip lost the table: %+v", back.ModelRouting)
		}
	})

	// The table's own problems, one sub-test per rule the checks answer with:
	// each is refused with the path the fix is written at and the check's own
	// sentence, which names the fix.
	for _, tc := range []struct {
		name, table, want string
	}{
		{
			name:  "a chain of only the launcher's pool",
			table: `{"entries": [{"match": "claude-*", "chain": [{"kind": "launcher-pool"}]}]}`,
			want:  "model_routing[0].chain",
		},
		{
			name:  "a source naming a pool the node does not carry",
			table: `{"entries": [{"match": "claude-*", "chain": [{"kind": "pool", "pool": "acme-absent"}]}]}`,
			want:  "acme-absent",
		},
		{
			name: "a craft model no entry matches",
			table: `{"entries": [{"match": "claude-haiku-*", "chain": [{"kind": "pool", "pool": "acme-pool"}]}],` +
				`"pools": [{"name": "acme-pool", "members": ["acme-main"], "owner": "owner@example.com"}]}`,
			want: "claude-opus-5-5",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Decode([]byte(routingNode(tc.table)))
			if err == nil {
				t.Fatalf("the node carrying %s was read", tc.table)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name the problem (%q): %v", tc.want, err)
			}
			if !strings.Contains(err.Error(), "model_routing") {
				t.Errorf("the refusal does not say which key the problem is in: %v", err)
			}
		})
	}

	// A member this Fugaro does not model inside the key is refused rather than
	// read as nothing (the strict decode every block of this node is held to),
	// and so is a pool the rules a published pool is held to refuse: craft_pools
	// carries the same set beside the key, and the members only this key has are
	// the ones no earlier release could have refused.
	for _, tc := range []struct{ name, table, want string }{
		{
			name:  "a member this Fugaro does not model",
			table: `{"entries": [], "pools": [], "admissions": []}`,
			want:  `unknown field "admissions"`,
		},
		{
			name:  "a pool whose rule is not one of the rules",
			table: `{"entries": [], "pools": [{"name": "acme-pool", "members": ["acme-main"], "owner": "owner@example.com", "rule": "random"}]}`,
			want:  "must be one of",
		},
		{
			name:  "a pool no owner answers for",
			table: `{"entries": [], "pools": [{"name": "acme-pool", "members": ["acme-main"]}]}`,
			want:  "owner is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Decode([]byte(routingNode(tc.table)))
			if err == nil {
				t.Fatalf("the node carrying %s was read", tc.table)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name the problem (%q): %v", tc.want, err)
			}
		})
	}

	// A block that sets neither an entry nor a pool is no block, so a node that
	// carries the empty object re-marshals without the key — the crafts' rule,
	// and the one that keeps a publish from writing a member that says nothing.
	t.Run("an empty block is no member", func(t *testing.T) {
		p, _, err := Decode([]byte(routingNode(`{"entries": [], "pools": []}`)))
		if err != nil {
			t.Fatal(err)
		}
		if p.ModelRouting != nil {
			t.Errorf("an empty block was kept: %+v", p.ModelRouting)
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "model_routing") {
			t.Errorf("a policy decoded from an empty block re-marshals with the member: %s", b)
		}
	})
}

// TestAReleaseBehindReaderStillDecodes is the compatibility story this key
// exists for (R21, F49, contract-table.md): the release behind this one knows
// no model_routing, so it lists the key in UnknownKeys and reads every block it
// does know — craft_pools, craft_credentials, the budget — in full. That is why
// the table travels as a key of its own and not as a member of a block that
// reader decodes strictly: a member there would make it refuse the WHOLE node,
// and the node is re-read while runs are in flight.
func TestAReleaseBehindReaderStillDecodes(t *testing.T) {
	raw := routingNode(routingTable)
	p, found, err := Decode([]byte(raw))
	if err != nil || !found || p.ModelRouting == nil {
		t.Fatalf("this Fugaro does not read its own node = (%v, %v, %v)", p, found, err)
	}

	preKey := make(map[string]bool, len(knownTopLevelKeys))
	for k, v := range knownTopLevelKeys {
		preKey[k] = v
	}
	delete(preKey, "model_routing")
	q, found, err := decodeWithKeys([]byte(raw), preKey)
	if err != nil || !found {
		t.Fatalf("the reader that predates the key refused the node: (%v, %v, %v)", q, found, err)
	}
	if want := []string{"model_routing"}; !slices.Equal(q.UnknownKeys, want) {
		t.Errorf("the reader that predates the key lists UnknownKeys = %v, want %v", q.UnknownKeys, want)
	}
	if q.Budget.Mode != "enforce" || q.Budget.PerRunUSD != 5 {
		t.Errorf("the rest of the policy is not in force beside the key: %+v", q.Budget)
	}
	if len(q.CraftPools) != 1 || q.CraftPools[0].Name != "acme-pool" || q.CraftPools[0].Members[0] != "acme-main" {
		t.Errorf("the reader that predates the key did not read craft_pools whole: %+v", q.CraftPools)
	}
	if len(q.CraftCredentials) != 1 || q.CraftCredentials[0].Name != "acme-main" {
		t.Errorf("the reader that predates the key did not read craft_credentials whole: %+v", q.CraftCredentials)
	}
	// The seam models the older reader's *key list* and not its body, so the
	// block it does not know is still decoded here (decodeWithKeys' own note).
	// What that reader does with the node is `update`'s business and not this
	// test's: the row that matters is that it refuses nothing and reads every
	// block it knows, which is what the assertions above say.
	if q.ModelRouting == nil {
		t.Error("the seam read no table at all, and the note above says the block is decoded whatever the key list: the test would pass on a reader that dropped it")
	}
}
