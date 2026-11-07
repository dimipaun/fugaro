//go:build firebase

package rules

// M3 of the hosted MCP server's plan (docs/mcp-server/implementation-plan.md;
// D922, answered and restated on 2026-11-04): the token records, the client
// registrations, the redirect URIs, the revocation state and the whole
// tool-call audit live in a top-level `mcpauth` subtree that is refused to
// every client. These rules are the only place that refusal is written down.
//
// The name is half the assertion, and it is the half the emulator cannot make:
// the root is already `".read": false` and `".write": false`, so a subtree the
// template never mentions is refused everything by a rule nobody reading the
// file can see -- and the next person who adds a grant to this document has to
// meet it. So the template and the generated document are read for the node by
// name, and the emulator then proves the deployed document refuses it to the
// two client identities that could reach a top-level node at all: the
// signed-in reader a phone authenticates as (USER) and a run's own token
// (TOKEN). Each of the two reaches a family the rules do grant it in the same
// database, so a refusal here is this node's rule and not an empty tree's.
//
// It deliberately does not assert that the subtree is *readable* by the
// coordinator: the coordinator's account reaches it through
// roles/firebasedatabase.admin (deploy/terraform/gcp/modules/firebase/iam.tf),
// which bypasses the rules, so no grant in this file is what admits it -- the
// harness's own admin client is that path, and it is the positive control that
// the node holds data at all.
//
// Hand mutation each fails on: dropping the node from the template (the
// generated document has no grant to read), giving it `coordinator`'s
// `.read: "USER"` (the signed-in reader is admitted to the token records),
// giving it a `.write` anywhere (a client can write the revocation state or
// the audit), and a `$child` under it carrying a grant (a rule hidden one
// level down, which the name check reads out of the generated paths).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

func TestMCPAuthIsNamedAndDeniedToEveryClient(t *testing.T) {
	// The template names it, and names it closed. Read out of the embedded
	// template rather than out of the generated document, so the key a person
	// edits is the key this reads.
	var tmpl map[string]any
	if err := json.Unmarshal([]byte(source), &tmpl); err != nil {
		t.Fatalf("the rules template is not JSON: %v", err)
	}
	rules, ok := tmpl["rules"].(map[string]any)
	if !ok {
		t.Fatalf("the rules template carries no \"rules\" object")
	}
	node, ok := rules[PathMCPAuth].(map[string]any)
	if !ok {
		t.Fatalf("the template carries no top-level /%s subtree: D922's answer of 2026-11-04 puts the token records, the client list, the revocation state and the audit there, and a subtree the rules never name is one no reader of this file can see", PathMCPAuth)
	}
	for _, attr := range []string{".read", ".write"} {
		if v, has := node[attr]; !has || v != false {
			t.Errorf("the template's /%s carries %s = %v, want the explicit false D922's answer gives every client: the node is refused whole, and an inherited refusal is one a later grant can widen without meeting this check", PathMCPAuth, attr, v)
		}
	}

	// And the generated document, which is what a deployment carries.
	generated, ok := generatedPaths(t)[PathMCPAuth]
	if !ok {
		t.Fatalf("the generated rules carry no /%s node", PathMCPAuth)
	}
	if v, has := generated[".read"]; !has || v != false {
		t.Errorf("generated /%s .read = %v, want false", PathMCPAuth, v)
	}
	if v, has := generated[".write"]; !has || v != false {
		t.Errorf("generated /%s .write = %v, want false", PathMCPAuth, v)
	}
	// Nothing beneath it may carry a grant of its own: the subtree is one node
	// denied whole, and a child with a rule is the shape a widening hides in.
	for path, attrs := range generatedPaths(t) {
		if !strings.HasPrefix(path, PathMCPAuth+"/") {
			continue
		}
		for _, attr := range []string{".read", ".write"} {
			if v, has := attrs[attr]; has && v != false {
				t.Errorf("generated /%s carries %s = %v, under a subtree D922's answer refuses whole", path, attr, v)
			}
		}
	}

	h := newHarness(t)
	h.seed(leased(2 * usd))
	alice, run := h.phone(mbAlice), h.run("aurora", "r1")
	// The two identities each reach a family the rules do grant them, so the
	// refusals below are refusals of this node rather than of an empty
	// database.
	h.mustRead(alice, budget.PathCapsGlobal)
	h.mustRead(run, budget.PathCapsGlobal)

	// The coordinator's own write, over IAM. The harness's admin client is that
	// path (the rules bind no client), and the record it leaves is what the two
	// clients below are refused.
	h.set(PathMCPAuth+"/families/f1", true)
	for _, p := range []string{PathMCPAuth, PathMCPAuth + "/families", PathMCPAuth + "/families/f1"} {
		h.mustReadDenied(alice, p)
		h.mustReadDenied(run, p)
	}
	for _, c := range []struct {
		who   string
		write func() map[string]any
	}{
		{"the signed-in reader", func() map[string]any { return put(PathMCPAuth+"/families/f1", true) }},
		{"the signed-in reader", func() map[string]any { return put(PathMCPAuth+"/families/f1", false) }},
		{"a run's token", func() map[string]any { return put(PathMCPAuth+"/families/f2", true) }},
		{"a run's token", func() map[string]any { return put(PathMCPAuth+"/families/f1", false) }},
	} {
		client := alice
		if c.who == "a run's token" {
			client = run
		}
		h.mustDeny(client, c.who+" writing a token record", c.write())
	}
	// The revocation one delete undoes (D930) is refused like every other write,
	// so a client cannot revoke a family either.
	h.mustDeny(alice, "the signed-in reader deleting a token record", map[string]any{PathMCPAuth + "/families/f1": nil})
	h.wantValue(PathMCPAuth+"/families/f1", "true")
}
