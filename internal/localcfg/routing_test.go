package localcfg

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/routing"
)

// routingDoc is an installation's config with a table in it, written the way a
// person keeps the file: a comment above the block saying what it is for, a
// comment inside an entry, blank lines between the blocks, and keys of the
// installation's own plumbing on both sides of it. Every one of those is what
// TestModelRoutingRoundTrips holds the writer to.
const routingDoc = `version: 1
name: aurora
gcp_project: my-project
region: us-central1
runs_bucket: my-runs

# Where this project's models are reached: the launcher's own pool first, then
# the project's key, and only for the repositories that may go out on it.
model_routing:
  - match: claude-sonnet-5-5
    chain:
      - kind: launcher-pool
      # The key is what pays once the subscriptions are spent.
      - kind: anthropic
        admit: [acme/web]
  - match: claude-*
    chain:
      - kind: vertex
        admit: [acme/api, acme/web]

max_parallel: 7
`

// routingTable is routingDoc's block, as the loader reads it.
var routingTable = routing.Table{
	{Match: "claude-sonnet-5-5", Chain: []routing.Source{
		{Kind: routing.KindLauncherPool},
		{Kind: routing.KindAnthropic, Admit: []string{"acme/web"}},
	}},
	{Match: "claude-*", Chain: []routing.Source{
		{Kind: routing.KindVertex, Admit: []string{"acme/api", "acme/web"}},
	}},
}

// TestModelRoutingDecodesStrictly is the block's own decode: the installation's
// config reads a `model_routing:` list of entries, the members it carries are
// the contract's, and every member a kind cannot use — and every key a
// repository's file or the project layer is not allowed to name — is refused
// with a sentence rather than stored and never read (D3182, contract-table.md).
func TestModelRoutingDecodesStrictly(t *testing.T) {
	lc, err := Parse([]byte(routingDoc))
	if err != nil {
		t.Fatalf("a config with a table: %v", err)
	}
	if !reflect.DeepEqual(lc.ModelRouting, routingTable) {
		t.Errorf("model_routing reads as\n%+v\nwant\n%+v", lc.ModelRouting, routingTable)
	}

	// The kinds are a closed set and the members follow the kind
	// (internal/routing's Source): what a table carries and no walk reads is
	// refused here, because nothing else would ever look at it.
	for name, tc := range map[string]struct{ block, want string }{
		"unknown kind": {`  - match: claude-*
    chain:
      - kind: gemini
`, "kind \"gemini\" must be one of launcher-pool, pool, anthropic, provider, vertex"},
		"provider on a pool source": {`  - match: claude-*
    chain:
      - kind: pool
        pool: team
        provider: openrouter
`, "kind pool names no provider"},
		"pool on the project's key": {`  - match: claude-*
    chain:
      - kind: anthropic
        pool: team
`, "kind anthropic names no pool"},
		"provider with no provider": {`  - match: claude-*
    chain:
      - kind: provider
`, "a source of kind provider with no provider names no credential"},
		"pool with no pool": {`  - match: claude-*
    chain:
      - kind: pool
`, "a source of kind pool with no pool names no credential"},
		"entry with no match": {`  - chain:
      - kind: vertex
`, "an entry with no match answers no model"},
		"unknown member of an entry": {`  - match: claude-*
    chian:
      - kind: vertex
`, "chian"},
		"unknown member of a source": {`  - match: claude-*
    chain:
      - kind: vertex
        admits: [acme/web]
`, "admits"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(sample + "model_routing:\n" + tc.block))
			if err == nil {
				t.Fatalf("the config is read, and %s", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal is %q, want it to name %s", err, tc.want)
			}
		})
	}

	// A repository does not choose its own routing: the project's table is the
	// project's, so both documents refuse the key as the unknown field it is.
	t.Run("a repository's file", func(t *testing.T) {
		_, ps := config.Parse([]byte("version: 1\nproject: acme\nmodel_routing:\n  - match: claude-*\n    chain:\n      - kind: vertex\n"))
		if len(ps) == 0 {
			t.Fatal("the repository's own file reads the project's routing table")
		}
		if got := ps[0].String(); !strings.Contains(got, "model_routing") {
			t.Errorf("the refusal is %q, want it to name the key a repository may not state", got)
		}
	})
	t.Run("the project layer", func(t *testing.T) {
		_, ps := config.ParseProjectLayer([]byte("version: 1\nproject: acme\ngcp_project: acme-fugaro\nmodel_routing:\n  - match: claude-*\n    chain:\n      - kind: vertex\n"), config.LayerAnchor{Project: "acme", GCPProject: "acme-fugaro"})
		if len(ps) == 0 {
			t.Fatal("the project layer reads the routing table")
		}
		if got := ps[0].String(); !strings.Contains(got, "model_routing") {
			t.Errorf("the refusal is %q, want it to name the key the layer may not state", got)
		}
	})
}

// TestAdmissionPerSource is the admission's own shape (D3182, R20): `admit` is
// a list of repositories per SOURCE and not per kind, so the same provider key
// is admitted to one repository and not another; it is carried on the three
// kinds that are a single credential of the project; and it is refused on the
// pooled kinds, whose members carry their own `allow_data_to` — a list there
// would look like an admission and be read by nothing.
func TestAdmissionPerSource(t *testing.T) {
	const doc = `version: 1
name: aurora
gcp_project: my-project
region: us-central1
runs_bucket: my-runs
model_routing:
  - match: claude-sonnet-5-5
    chain:
      - kind: provider
        provider: openrouter
        admit: [acme/web]
      - kind: provider
        provider: openrouter
        admit: [acme/api]
      - kind: anthropic
        admit: [acme/web]
      - kind: vertex
        admit: []
`
	lc, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("a table with admissions: %v", err)
	}
	chain := lc.ModelRouting[0].Chain
	if len(chain) != 4 {
		t.Fatalf("the chain reads as %+v, want four sources", chain)
	}
	// The two provider sources name one provider and two admissions: the list
	// belongs to the source, and folding it onto the kind would admit
	// acme/api to a source it was never written on.
	if chain[0].Provider != chain[1].Provider {
		t.Fatalf("the two sources name %s and %s, want the one provider", chain[0].Provider, chain[1].Provider)
	}
	if got := chain[0].Admit; !reflect.DeepEqual(got, []string{"acme/web"}) {
		t.Errorf("the first provider's admit is %v, want [acme/web] alone", got)
	}
	if got := chain[1].Admit; !reflect.DeepEqual(got, []string{"acme/api"}) {
		t.Errorf("the second provider's admit is %v, want [acme/api] alone", got)
	}
	if got := chain[2].Admit; !reflect.DeepEqual(got, []string{"acme/web"}) {
		t.Errorf("the project's key admits %v, want [acme/web]", got)
	}
	// An empty list admits nobody, which is what the key absent says too: the
	// two spellings are one table, so a write of the same table does not
	// rewrite the line a person wrote.
	if len(chain[3].Admit) != 0 {
		t.Errorf("the vertex source admits %v, and an empty list admits nobody", chain[3].Admit)
	}
	without := routing.Table{{Match: "claude-sonnet-5-5", Chain: []routing.Source{
		{Kind: routing.KindProvider, Provider: "openrouter", Admit: []string{"acme/web"}},
		{Kind: routing.KindProvider, Provider: "openrouter", Admit: []string{"acme/api"}},
		{Kind: routing.KindAnthropic, Admit: []string{"acme/web"}},
		{Kind: routing.KindVertex},
	}}}
	if !sameTable(lc.ModelRouting, without) {
		t.Errorf("a written `admit: []` and an absent list are read as two tables:\n%+v\n%+v", lc.ModelRouting, without)
	}
	if out, err := SetModelRouting([]byte(doc), lc.ModelRouting); err != nil {
		t.Errorf("writing the table the file already holds: %v", err)
	} else if !bytes.Equal(out, []byte(doc)) {
		t.Errorf("writing the table the file already holds rewrites the file:\n%s", out)
	}

	// Where the admission may not be written: the pooled kinds, whose members'
	// own allow_data_to decides who is served (R20, F41).
	for name, tc := range map[string]struct{ block, want string }{
		"launcher-pool": {`  - match: claude-*
    chain:
      - kind: launcher-pool
        admit: [acme/web]
`, "kind launcher-pool carries no admit list"},
		"pool": {`  - match: claude-*
    chain:
      - kind: pool
        pool: team
        admit: [acme/web]
`, "kind pool carries no admit list"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(sample + "model_routing:\n" + tc.block))
			if err == nil {
				t.Fatalf("the config is read, and %s", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal is %q, want it to name the rule", err)
			}
		})
	}
}

// TestModelRoutingRoundTrips is the writer's own contract: the block goes into
// the file a person keeps, and nothing else in it moves — not a comment, not a
// blank line, not a key of the installation's plumbing on either side of the
// block. The same write of the table the file already holds is no write at all.
func TestModelRoutingRoundTrips(t *testing.T) {
	const head = "# The installation's own facts.\nversion: 1\n"
	const tail = "\n# Above the plumbing, where the table ends.\nmax_parallel: 7\n"
	doc := []byte(head + routingDoc[len("version: 1\n"):strings.Index(routingDoc, "max_parallel")] + tail)

	lc, err := Parse(doc)
	if err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	// A table the file does not hold: one entry more, and the first entry's
	// chain serves another model through another provider.
	next := routing.Table{
		{Match: "claude-sonnet-5-5", Chain: []routing.Source{
			{Kind: routing.KindLauncherPool},
			{Kind: routing.KindProvider, Provider: "openrouter", Model: "claude-haiku-4-5"},
			{Kind: routing.KindAnthropic, Admit: []string{"acme/web"}},
		}},
		{Match: "claude-*", Chain: []routing.Source{
			{Kind: routing.KindVertex, Admit: []string{"acme/api", "acme/web"}},
		}},
	}
	out, err := SetModelRouting(doc, next)
	if err != nil {
		t.Fatalf("writing a table: %v", err)
	}
	// Everything the block does not occupy is the bytes the file had: the
	// comment above the block, the blank line that separates them, and the
	// plumbing below — comment and all.
	keyAt := bytes.Index(doc, []byte("model_routing:"))
	tailAt := bytes.Index(doc, []byte(tail))
	if keyAt < 0 || tailAt < 0 {
		t.Fatalf("the fixture does not hold the key and the tail: %d, %d", keyAt, tailAt)
	}
	if !bytes.HasPrefix(out, doc[:keyAt]) {
		t.Errorf("the write rewrote the head of the file:\n%s", out)
	}
	if !bytes.HasSuffix(out, doc[tailAt:]) {
		t.Errorf("the write rewrote the tail of the file:\n%s", out)
	}
	// The comment inside an entry is the entry's own: the entry it described
	// was replaced, so it went with it, and the block is what the write says it
	// is on every other line.
	if bytes.Contains(out, []byte("The key is what pays")) {
		t.Errorf("the write kept a comment of an entry it replaced:\n%s", out)
	}
	got, err := Parse(out)
	if err != nil {
		t.Fatalf("the bytes the write left do not read back: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(got.ModelRouting, next) {
		t.Errorf("the block reads back as\n%+v\nwant\n%+v", got.ModelRouting, next)
	}
	if !strings.Contains(string(out), "model_routing:\n  - match: claude-sonnet-5-5\n") {
		t.Errorf("the block is written at the document's own step (two here):\n%s", out)
	}

	// The same table the file holds is no write at all, byte for byte.
	if again, err := SetModelRouting(doc, lc.ModelRouting); err != nil {
		t.Fatalf("writing what the file holds: %v", err)
	} else if !bytes.Equal(again, doc) {
		t.Errorf("writing the table the file holds rewrote the file:\n%s", again)
	}

	// An empty table removes the block and nothing else: a table with no entry
	// routes nothing, and the absent key says the same in fewer lines.
	out, err = SetModelRouting(doc, nil)
	if err != nil {
		t.Fatalf("removing the block: %v", err)
	}
	if strings.Contains(string(out), "model_routing") {
		t.Errorf("the block is still written:\n%s", out)
	}
	if !bytes.HasPrefix(out, []byte(head)) || !bytes.HasSuffix(out, []byte(tail)) {
		t.Errorf("removing the block moved the bytes around it:\n%s", out)
	}
	if removed, err := Parse(out); err != nil {
		t.Fatalf("the file without the block does not read back: %v", err)
	} else if len(removed.ModelRouting) != 0 {
		t.Errorf("the file without the block reads a table: %+v", removed.ModelRouting)
	}

	// A file with no block gains one at its end, and every byte it had is
	// still there.
	grown, err := SetModelRouting([]byte(sample), routingTable)
	if err != nil {
		t.Fatalf("writing the first table: %v", err)
	}
	if !bytes.HasPrefix(grown, []byte(sample)) {
		t.Errorf("writing a first table rewrote the file:\n%s", grown)
	}
	if grown2, err := Parse(grown); err != nil {
		t.Fatalf("the file with its first table does not read back: %v", err)
	} else if !reflect.DeepEqual(grown2.ModelRouting, routingTable) {
		t.Errorf("the first table reads back as %+v", grown2.ModelRouting)
	}
	if idempotent, err := SetModelRouting(grown, routingTable); err != nil {
		t.Fatalf("writing the first table twice: %v", err)
	} else if !bytes.Equal(idempotent, grown) {
		t.Errorf("the second write of the same table changed the file:\n%s", idempotent)
	}

	// What it refuses rather than return bytes it cannot vouch for: every one
	// of these keeps the input unchanged, and says why.
	for name, tc := range map[string]struct {
		doc  []byte
		want string
	}{
		"more than one document": {[]byte("version: 1\n---\nname: aurora\n"), "more than one YAML document"},
		"not a mapping":          {[]byte("- version: 1\n- name: aurora\n"), "the local config is one YAML mapping"},
		"the key written twice":  {[]byte(sample + "model_routing:\n  - match: claude-*\n    chain:\n      - kind: vertex\nmodel_routing:\n  - match: claude-*\n    chain:\n      - kind: vertex\n"), "is written twice"},
		"an anchored block":      {[]byte(sample + "model_routing: &t\n  - match: claude-*\n    chain:\n      - kind: vertex\nother: *t\n"), "carries &t"},
		"a block it cannot measure": {[]byte(sample +
			"model_routing:\n  - match: claude-*\n    chain:\n      - kind: vertex\n      - model: >-\n          gemini-3-pro\n"), "more than the reader can measure"},
		"an empty mapping":            {[]byte("{}\n"), "the local config is one YAML mapping"},
		"a block that is not a table": {[]byte(sample + "model_routing: nonsense\n"), "fix the block"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := SetModelRouting(tc.doc, routingTable)
			if err == nil {
				t.Fatalf("the write is made, and the file is one it cannot vouch for:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal is %q, want it to say %s", err, tc.want)
			}
			if !bytes.Equal(out, tc.doc) {
				t.Errorf("the refusal left bytes behind:\n%s", out)
			}
		})
	}
}

// TestModelRoutingYAMLEqualsTheContract holds the two spellings of the table's
// shapes together: the config's document is the contract's Table, written with
// yaml's own reading of the same members the wire names in json, so a member
// added to one of the two types and not the other — or spelled differently, or
// without the option that keeps an unused member out of a person's file — is
// red here rather than a block that quietly stops carrying a fact.
func TestModelRoutingYAMLEqualsTheContract(t *testing.T) {
	count := 0
	for _, typ := range []reflect.Type{reflect.TypeOf(routing.Entry{}), reflect.TypeOf(routing.Source{})} {
		for i := range typ.NumField() {
			f := typ.Field(i)
			count++
			jsonName, jsonOpts, _ := strings.Cut(f.Tag.Get("json"), ",")
			tag := f.Tag.Get("yaml")
			yamlName, yamlOpts, _ := strings.Cut(tag, ",")
			if tag == "" {
				t.Errorf("%s.%s: no yaml tag, and the block is this shape written into the config", typ.Name(), f.Name)
				continue
			}
			if yamlName != jsonName || yamlOpts != jsonOpts {
				t.Errorf("%s.%s: yaml %q reads as %q with %q, and the wire writes it as %q with %q: one shape is stated twice and the two may not drift", typ.Name(), f.Name, f.Tag.Get("yaml"), yamlName, yamlOpts, jsonName, jsonOpts)
			}
		}
	}
	if count < 7 {
		t.Fatalf("the walk of the shapes yields %d members: a walk that finds nothing proves nothing", count)
	}
	// And what that spelling writes: the members a kind uses, and no member it
	// does not — an empty `pool: ""` on the launcher's pool would be a line a
	// person reads and no walk looks at.
	for _, tc := range []struct {
		source routing.Source
		want   string
	}{
		{routing.Source{Kind: routing.KindLauncherPool}, "kind: launcher-pool\n"},
		{routing.Source{Kind: routing.KindVertex, Model: "gemini-3-pro"}, "kind: vertex\nmodel: gemini-3-pro\n"},
		{
			routing.Source{Kind: routing.KindProvider, Provider: "openrouter", Model: "claude-haiku-4-5", Admit: []string{"acme/web"}},
			"kind: provider\nprovider: openrouter\nmodel: claude-haiku-4-5\nadmit:\n    - acme/web\n",
		},
	} {
		got, err := yaml.Marshal(tc.source)
		if err != nil {
			t.Fatalf("marshalling %+v: %v", tc.source, err)
		}
		if string(got) != tc.want {
			t.Errorf("the source %+v writes\n%s\nwant\n%s", tc.source, got, tc.want)
		}
	}
}

// TestModelRoutingIsNeverPublished: the table says which credential serves
// which model, so it is local config and never the published shared config —
// the config a launcher can write is not where a credential's reach is decided
// (Shared's own reason, one step out). It travels to the policy node instead.
func TestModelRoutingIsNeverPublished(t *testing.T) {
	lc, err := Parse([]byte(routingDoc))
	if err != nil {
		t.Fatal(err)
	}
	if published := lc.Shared(); len(published.ModelRouting) != 0 {
		t.Errorf("the published shared config carries the table: %+v", published.ModelRouting)
	}
}
