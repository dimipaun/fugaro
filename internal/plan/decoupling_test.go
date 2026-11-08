package plan

import (
	"encoding/json"
	"errors"
	"fmt"
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

// refused asserts that Parse refuses a document and that the refusal says what
// the test is about.
func refused(t *testing.T, body string, wants ...string) {
	t.Helper()
	_, err := Parse("graph.json", []byte(body))
	if err == nil {
		t.Fatal("Parse accepted a document it has to refuse")
	}
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Parse's error is %T, want a *Refusal", err)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal = %q, want it to say %q", err, want)
		}
	}
}

// TestGraphV3RefusesWhatItDoesNotKnow: the field set is closed at schema 3 too,
// and the kind is one of four. The clause `kind` is read by is the one a
// document from a later planner would arrive through, so a value in it that
// this build does not know is a refusal rather than a node drawn as a task.
func TestGraphV3RefusesWhatItDoesNotKnow(t *testing.T) {
	t.Run("an unknown field", func(t *testing.T) {
		refused(t, v3Doc(t, `"schema_version": 3,`, `"schema_version": 3, "gain": 1,`),
			"not a schema version 3 graph", "gain")
	})

	t.Run("an unknown kind", func(t *testing.T) {
		refused(t, v3Doc(t, `{"id": "I1", "title": "the integration", "kind": "integration"`,
			`{"id": "I1", "title": "the integration", "kind": "spy"`),
			`node "I1" carries the kind "spy"`, "contract_change")
	})

	t.Run("a size of zero", func(t *testing.T) {
		refused(t, v3Doc(t, `{"id": "A", "title": "the base", "size": 3,`, `{"id": "A", "title": "the base", "size": 0,`),
			`node "A" carries the size 0`, "greater than 0")
	})

	t.Run("a negative size", func(t *testing.T) {
		refused(t, v3Doc(t, `{"id": "C1", "title": "the contract", "kind": "contract", "size": 0.5,`,
			`{"id": "C1", "title": "the contract", "kind": "contract", "size": -1,`),
			`node "C1" carries the size -1`)
	})

	t.Run("stubs on a task", func(t *testing.T) {
		refused(t, v3Doc(t, `{"id": "B", "title": "the dependent", `,
			`{"id": "B", "title": "the dependent", "stubs": ["internal/dep/stub.go"], `),
			`node "B" carries stubs and is a task`)
	})
}

// TestV2DocumentWithAV3FieldIsRefused: the third schema's fields in a document
// written to one of the two older schemas: refused, not read with the fields
// quietly dropped. The same rule as a stream in a v1 document, and it holds for
// every key the schema adds wherever it sits — a node's field as much as the
// document's own.
func TestV2DocumentWithAV3FieldIsRefused(t *testing.T) {
	node := func(version int, fields string) string {
		return fmt.Sprintf(`{"schema_version": %d, "plan": "p.md", "nodes": [
		{"id": "a", "title": "x", %s "footprint": {"prediction": true, "paths": []}}], "edges": []}`, version, fields)
	}
	doc := func(version int, fields string) string {
		return fmt.Sprintf(`{"schema_version": %d, "plan": "p.md", "nodes": [], "edges": [], %s}`, version, fields)
	}
	for _, tc := range []struct {
		name    string
		body    string
		key     string
		version int
	}{
		{"a kind in a v2 document", node(2, `"kind": "contract",`), "kind", 2},
		{"a size in a v2 document", node(2, `"size": 2,`), "size", 2},
		{"a stub list in a v2 document", node(2, `"stubs": ["internal/x.go"],`), "stubs", 2},
		{"a decoupling record in a v2 document", doc(2, `"decoupling": []`), "decoupling", 2},
		{"a change request in a v2 document", doc(2, `"contract_changes": []`), "contract_changes", 2},
		{"a kind in a v1 document", node(1, `"kind": "contract",`), "kind", 1},
		{"a change request in a v1 document", doc(1, `"contract_changes": []`), "contract_changes", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refused(t, tc.body, fmt.Sprintf("schema version %d and carries", tc.version), tc.key, "schema version 3 field")
		})
	}
	// The same documents without the fields, so the table above refuses the
	// field and not the shape.
	if _, err := Parse("graph.json", []byte(node(2, ""))); err != nil {
		t.Fatalf("a v2 document was refused: %v", err)
	}
	if _, err := Parse("graph.json", []byte(node(1, ""))); err != nil {
		t.Fatalf("a v1 document was refused: %v", err)
	}
	if _, err := Parse("graph.json", []byte(doc(2, `"stream": "cli"`))); err != nil {
		t.Fatalf("a v2 document naming its stream was refused: %v", err)
	}
}

// TestDecouplingNeedsIntegrationOverBothEndpoints: every broken edge is covered
// by an integration node that depends on both of its endpoints, and one
// contract's broken edges are one integration node and not one each (D1817).
// The edge is gone from the graph and its integration task is what re-joins the
// two sides, so a record whose integration does not wait on both of them names
// a task that cannot do that.
func TestDecouplingNeedsIntegrationOverBothEndpoints(t *testing.T) {
	if _, err := Parse("graph.json", []byte(v3Graph)); err != nil {
		t.Fatalf("the fixture was refused: %v", err)
	}

	t.Run("an integration that is not a node", func(t *testing.T) {
		refused(t, v3Doc(t, `"integration": "I1"`, `"integration": "ghost"`),
			`names the integration "ghost"`, "not an integration node")
	})

	t.Run("an integration that is a task", func(t *testing.T) {
		refused(t, v3Doc(t, `"integration": "I1"`, `"integration": "A"`),
			`names the integration "A"`, "not an integration node")
	})

	t.Run("an integration that waits on one endpoint only", func(t *testing.T) {
		refused(t, v3Doc(t, `,
    {"task": "I1", "needs": "B", "reason": "the integration runs both sides"}`, ""),
			`the integration node "I1" does not depend on both sides`, "B -> A")
	})

	t.Run("two integration nodes for one contract", func(t *testing.T) {
		body := v3Doc(t,
			`{"id": "I1", "title": "the integration", "kind": "integration", "footprint": {"prediction": true, "paths": ["internal/contract/stub.go"]}},`,
			`{"id": "I1", "title": "the integration", "kind": "integration", "footprint": {"prediction": true, "paths": ["internal/contract/stub.go"]}},
    {"id": "I2", "title": "another integration", "kind": "integration", "footprint": {"prediction": true, "paths": []}},`,
			`    {"task": "I1", "needs": "A", "reason": "the integration runs both sides"},
`,
			`    {"task": "I1", "needs": "A", "reason": "the integration runs both sides"},
    {"task": "I2", "needs": "A", "reason": "the integration runs both sides"},
    {"task": "I2", "needs": "B", "reason": "the integration runs both sides"},
`,
			`{"contract": "C1", "was": {"task": "B", "needs": "A"}, "integration": "I1", "reason": "B needed A's interface only"}`,
			`{"contract": "C1", "was": {"task": "B", "needs": "A"}, "integration": "I1", "reason": "B needed A's interface only"},
    {"contract": "C1", "was": {"task": "B", "needs": "A"}, "integration": "I2", "reason": "B needed A's interface only"}`)
		refused(t, body, `the contract "C1" is covered by two integration nodes`, "D1817")
	})
}

// TestDecouplingWasEdgeMustNotStillBeAnEdge: the record says the pass broke
// task -> needs, so the graph must not still carry it. Both at once is a
// document disagreeing with itself about whether the dependency is there, and
// every answer computed from it — what a task waits for, what is ready — would
// be one of the two.
func TestDecouplingWasEdgeMustNotStillBeAnEdge(t *testing.T) {
	if _, err := Parse("graph.json", []byte(v3Graph)); err != nil {
		t.Fatalf("the fixture was refused: %v", err)
	}
	refused(t, v3Doc(t, `"edges": [`, `"edges": [
    {"task": "B", "needs": "A", "reason": "the edge the pass broke"},`),
		"carries the edge B -> A", "records it as broken")
}

// TestContractChangeReferencesMustExistAndMatchKinds: a change request is a
// node of the graph, and every name it carries is checked against the kinds of
// the nodes it names. A change against a task, or raised by a node that is not
// there, is a record nothing can act on: the follow-up run it would drive needs
// a contract file to name and a task to reach.
func TestContractChangeReferencesMustExistAndMatchKinds(t *testing.T) {
	const entry = `{"change": "R1", "contract": "C1", "raised_by": "B", "reason": "the interface moved", "affects": ["B"]}`
	t.Run("a change that is not a node", func(t *testing.T) {
		refused(t, v3Doc(t, entry, `{"change": "ghost", "contract": "C1", "raised_by": "B", "reason": "the interface moved", "affects": ["B"]}`),
			`the contract change "C1" names the node "ghost"`, "not a contract_change node")
	})

	t.Run("a change that is a task", func(t *testing.T) {
		refused(t, v3Doc(t, entry, `{"change": "B", "contract": "C1", "raised_by": "B", "reason": "the interface moved", "affects": ["B"]}`),
			`names the node "B"`, "not a contract_change node")
	})

	t.Run("a contract that is not a contract", func(t *testing.T) {
		refused(t, v3Doc(t, entry, `{"change": "R1", "contract": "A", "raised_by": "B", "reason": "the interface moved", "affects": ["B"]}`),
			`is against "A"`, "not a contract node")
	})

	t.Run("a raiser that is not a node", func(t *testing.T) {
		refused(t, v3Doc(t, entry, `{"change": "R1", "contract": "C1", "raised_by": "ghost", "reason": "the interface moved", "affects": ["B"]}`),
			`is raised by "ghost"`)
	})

	t.Run("an affected task that is not a node", func(t *testing.T) {
		refused(t, v3Doc(t, entry, `{"change": "R1", "contract": "C1", "raised_by": "B", "reason": "the interface moved", "affects": ["ghost"]}`),
			`affects "ghost"`)
	})

	t.Run("a change with no reason", func(t *testing.T) {
		refused(t, v3Doc(t, entry, `{"change": "R1", "contract": "C1", "raised_by": "B", "reason": "", "affects": ["B"]}`),
			`the contract change "R1" carries no reason`)
	})
}

// TestStubPathsAreLiteralRepoRelative: a stub is untrusted plan text that a
// later command stats (F9), so it is a literal repository-relative path and
// nothing else — never a glob, never absolute, never with a parent element. A
// pattern here would be a stub the integration task cannot be asked to have
// deleted, and an escaping path is a path read outside the checkout.
func TestStubPathsAreLiteralRepoRelative(t *testing.T) {
	for _, tc := range []struct {
		name string
		stub string
	}{
		{"a parent element", "../stub.go"},
		{"an absolute path", "/etc/stub.go"},
		{"a glob", "internal/*.go"},
		{"a recursive glob", "internal/**"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refused(t, v3Doc(t,
				`"paths": ["internal/contract/types.go", "internal/contract/stub.go"]`,
				`"paths": ["internal/contract/types.go", "`+tc.stub+`"]`,
				`"stubs": ["internal/contract/stub.go"]`,
				`"stubs": ["`+tc.stub+`"]`),
				fmt.Sprintf("names the stub %q", tc.stub), "literal repository-relative path")
		})
	}
	// The literal file the fixture names is read, so the table above refuses
	// the shape and not stubs.
	if _, err := Parse("graph.json", []byte(v3Graph)); err != nil {
		t.Fatalf("the fixture was refused: %v", err)
	}
}

// TestStubMustAppearLiterallyInTheContractFootprint: a stub is a whole file the
// contract names (D1815), so the contract's own footprint has to name it — by
// name, not through a pattern that happens to cover it. A stub admitted by a
// glob would be a file the planner never wrote down, and the F10 check would
// have no file to hold the contract to.
func TestStubMustAppearLiterallyInTheContractFootprint(t *testing.T) {
	t.Run("a stub only a glob covers", func(t *testing.T) {
		refused(t, v3Doc(t, `"paths": ["internal/contract/types.go", "internal/contract/stub.go"]`,
			`"paths": ["internal/contract/types.go", "internal/contract/**"]`),
			`names the stub "internal/contract/stub.go"`, "does not name that file")
	})

	t.Run("a stub the footprint never mentions", func(t *testing.T) {
		refused(t, v3Doc(t,
			`"paths": ["internal/contract/types.go", "internal/contract/stub.go"]`,
			`"paths": ["internal/contract/types.go"]`,
			`"stubs": ["internal/contract/stub.go"]`,
			`"stubs": ["internal/other/stub.go"]`),
			`names the stub "internal/other/stub.go"`, "does not name that file")
	})
}

// TestContractNodeNeedsALiteralFootprintPath: a contract's files are the whole
// files it names, and they are what the F10 check holds every other task
// against — so a contract whose footprint is patterns and nothing else has no
// files to hold anyone to and is refused rather than read as a contract that
// touches nothing in particular.
func TestContractNodeNeedsALiteralFootprintPath(t *testing.T) {
	refused(t, v3Doc(t, `"paths": ["internal/contract/types.go", "internal/contract/stub.go"]`,
		`"paths": ["internal/contract/**", "internal/other/**"]`),
		`node "C1" is a contract node whose footprint names no literal path`)
}

// TestContractFilesBelongToTheContractAndItsChanges: a merged contract is a
// single point both sides trust (F10), so its files appear in no other task's
// footprint except the change requests against it and the integration node for
// the stubs it deletes. The check is the footprint's own matching, so a glob
// that covers a contract file is the same fault as naming it.
func TestContractFilesBelongToTheContractAndItsChanges(t *testing.T) {
	// The fixture is the shape that reads: the contract's own footprint, the
	// integration node on the stub it deletes, and the change node.
	if _, err := Parse("graph.json", []byte(v3Graph)); err != nil {
		t.Fatalf("the fixture was refused: %v", err)
	}

	t.Run("a task naming a contract file", func(t *testing.T) {
		refused(t, v3Doc(t, `{"id": "B", "title": "the dependent", "footprint": {"prediction": true, "paths": ["internal/dep/**"]}}`,
			`{"id": "B", "title": "the dependent", "footprint": {"prediction": true, "paths": ["internal/contract/types.go"]}}`),
			`node "B"'s footprint covers "internal/contract/types.go"`, `the contract "C1"`, "F10")
	})

	t.Run("a task whose glob covers a contract file", func(t *testing.T) {
		refused(t, v3Doc(t, `{"id": "B", "title": "the dependent", "footprint": {"prediction": true, "paths": ["internal/dep/**"]}}`,
			`{"id": "B", "title": "the dependent", "footprint": {"prediction": true, "paths": ["internal/contract/**"]}}`),
			`node "B"'s footprint covers "internal/contract/types.go"`)
	})

	t.Run("the integration node on a file that is not a stub", func(t *testing.T) {
		refused(t, v3Doc(t, `{"id": "I1", "title": "the integration", "kind": "integration", "footprint": {"prediction": true, "paths": ["internal/contract/stub.go"]}}`,
			`{"id": "I1", "title": "the integration", "kind": "integration", "footprint": {"prediction": true, "paths": ["internal/contract/types.go"]}}`),
			`node "I1"'s footprint covers "internal/contract/types.go"`)
	})
}

// TestGraphV3SpellsTargetVersions: the shape a target version has became a
// promise with the second schema, and it is checked at every schema from there
// on — so a v3 graph's versions are held to it too rather than read as free
// text because they are not the newest thing in the document.
func TestGraphV3SpellsTargetVersions(t *testing.T) {
	const b = `{"id": "B", "title": "the dependent", `
	if _, err := Parse("graph.json", []byte(v3Doc(t, b, `{"id": "B", "title": "the dependent", "version": "0.10", `))); err != nil {
		t.Fatalf("a v3 graph spelling a version MAJOR.MINOR was refused: %v", err)
	}
	refused(t, v3Doc(t, b, `{"id": "B", "title": "the dependent", "version": "0.10 beta", `),
		`node "B" carries the version "0.10 beta"`, "MAJOR.MINOR")
}

// TestV2GraphNamingAStreamStillReads: the stream is the second schema's field
// and it stays the second schema's field, so a v2 document naming one is read
// — the rule that refuses one is written against the version that introduced
// it, not against the newest version this build reads. Getting this wrong
// refuses every graph written before the third schema, which is every graph in
// the tree.
func TestV2GraphNamingAStreamStillReads(t *testing.T) {
	doc, err := Parse("graph.json", []byte(`{"schema_version": 2, "plan": "p.md", "stream": "cli", "nodes": [], "edges": []}`))
	if err != nil {
		t.Fatalf("a v2 graph naming its stream was refused: %v", err)
	}
	if doc.Stream == nil || *doc.Stream != "cli" {
		t.Errorf("stream = %v, want the cli the document names", doc.Stream)
	}
	// And at the third schema, where the field is still read.
	if _, err := Parse("graph.json", []byte(v3Doc(t, `"schema_version": 3,`, `"schema_version": 3, "stream": "console",`))); err != nil {
		t.Fatalf("a v3 graph naming its stream was refused: %v", err)
	}
}

// TestGraphV3CycleStillRefused: the cycle is not a drawing problem at any
// schema. A v3 graph is refused for it in the same words, whatever else the
// document carries.
func TestGraphV3CycleStillRefused(t *testing.T) {
	const cyclic = `{"schema_version": 3, "plan": "p.md", "nodes": [
		{"id": "a", "title": "first", "kind": "contract", "footprint": {"prediction": true, "paths": ["a.go"]}},
		{"id": "b", "title": "second", "footprint": {"prediction": true, "paths": ["b.go"]}}],
		"edges": [{"task": "b", "needs": "a", "reason": "b builds on a"}, {"task": "a", "needs": "b", "reason": "a builds on b"}]}`
	refused(t, cyclic, "the graph has a cycle", "a -> b -> a")
}
