package plan

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The tests below are CD2 of docs/contract-first-decoupling: schema 3 of the
// graph artifact, the fields the decoupling pass needs and the validation that
// makes each of them checkable. The document is untrusted plan text (D1821), so
// every rule here is a refusal rather than a best-effort reading.
//
// v3Graph is the document they are written against: a task broken through a
// contract, the one integration node that depends on both of its endpoints,
// the stub the contract names, and one change request raised against it.
const v3Graph = `{
  "schema_version": 3,
  "plan": "docs/plans/decoupled.md",
  "nodes": [
    {"id": "A", "title": "the base", "size": 3, "footprint": {"prediction": true, "paths": ["internal/base/**"]}},
    {"id": "B", "title": "the dependent", "footprint": {"prediction": true, "paths": ["internal/dep/**"]}},
    {"id": "C1", "title": "the contract", "kind": "contract", "size": 0.5, "stubs": ["internal/contract/stub.go"], "footprint": {"prediction": true, "paths": ["internal/contract/types.go", "internal/contract/stub.go"]}},
    {"id": "I1", "title": "the integration", "kind": "integration", "footprint": {"prediction": true, "paths": ["internal/contract/stub.go"]}},
    {"id": "R1", "title": "a change request", "kind": "contract_change", "footprint": {"prediction": true, "paths": ["internal/contract/types.go"]}}
  ],
  "edges": [
    {"task": "C1", "needs": "A", "reason": "the contract is written against A"},
    {"task": "B", "needs": "C1", "reason": "B is built against the contract"},
    {"task": "I1", "needs": "A", "reason": "the integration runs both sides"},
    {"task": "I1", "needs": "B", "reason": "the integration runs both sides"}
  ],
  "decoupling": [
    {"contract": "C1", "was": {"task": "B", "needs": "A"}, "integration": "I1", "reason": "B needed A's interface only"}
  ],
  "contract_changes": [
    {"change": "R1", "contract": "C1", "raised_by": "B", "reason": "the interface moved", "affects": ["B"]}
  ]
}`

// v3Doc is that document with one thing changed, so a test states the single
// fault it is refusing rather than restating the whole graph.
func v3Doc(t *testing.T, replacements ...string) string {
	t.Helper()
	if len(replacements)%2 != 0 {
		t.Fatal("v3Doc takes old/new pairs")
	}
	out := v3Graph
	for i := 0; i < len(replacements); i += 2 {
		old, new := replacements[i], replacements[i+1]
		if !strings.Contains(out, old) {
			t.Fatalf("v3Doc: %q is not in the fixture", old)
		}
		out = strings.Replace(out, old, new, 1)
	}
	return out
}

// TestGraphV3RoundTrips: a schema-3 document this build reads comes back out as
// the document it read. Every field the schema adds survives a write and a
// re-read, so a planner's file and the read cannot disagree about the graph.
func TestGraphV3RoundTrips(t *testing.T) {
	doc := parseDoc(t, v3Graph)
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	again, err := Parse("graph.json", body)
	if err != nil {
		t.Fatalf("the document this build wrote was refused: %v\n%s", err, body)
	}
	if !reflect.DeepEqual(doc, again) {
		t.Errorf("a round trip changed the document:\n%+v\n%+v", doc, again)
	}
}
