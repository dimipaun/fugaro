package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// routingLocalExtra is what the routing fixtures append to the project config
// writeProject writes: the project's own credentials and the one pool over
// them, two crafts' models, the route the coder's model goes down, the budget
// the table's chains are judged against (R17: a paid source after a pool needs
// an enforcing budget with a cap above zero), and the table itself — a Claude
// entry on a subscription pool behind the launcher's own pool, and the vendor's
// model on the project's provider key.
const routingLocalExtra = `
credentials:
  - {name: claude-1, type: claude-oauth}
  - {name: anthropic-key, type: anthropic}
pools:
  - {name: claude-pool, members: [claude-1, anthropic-key]}
crafts:
  coder: {model: deepseek/deepseek-v4.1-flash}
  senior-review: {model: claude-opus-5-5}
budget:
  mode: enforce
  per_run_usd: 10
providers:
  openrouter-main:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: [deepseek/*]
model_routing:
  - match: "claude-*"
    chain:
      - {kind: launcher-pool}
      - {kind: pool, pool: claude-pool}
  - match: "deepseek/*"
    chain:
      - {kind: provider, provider: openrouter-main}
`

// routingRepoYAML is the checkout's own fugaro.yaml: the security review's
// model, so the models the read judges come from two documents (the
// installation's config and the checkout's file) rather than one. gcp_project
// names the cached layer's, without which that layer's defaults apply to this
// file at no pass at all (config.Resolve's anchor rule).
const routingRepoYAML = `version: 1
project: aurora
gcp_project: aurora-gcp
git: { provider: github }
workflows:
  web: { commands: { build: sh b.sh, test: sh t.sh } }
agent:
  auth: api-key
  crafts:
    security-review: {model: claude-sonnet-5}
  models:
    vision: claude-haiku-4-5
`

// routingProjectLayer is the project layer file the last cloud read cached: the
// first-line review's model, which no narrower layer sets, and two agent keys a
// run of the project is served on through the layer rather than through the
// checkout's own file — defaults.agent.model, which both the coder's and the
// reviewer's pin fall back to, and its background pin. The read carries both,
// because a project layer naming them names models every repository of the
// project calls (the level rung has its own test below, so this fixture's layer
// names no level).
const routingProjectLayer = `version: 1
project: aurora
gcp_project: aurora-gcp
defaults:
  git: { provider: github }
  agent:
    auth: api-key
    model: claude-fable-5-1
    models:
      background: claude-opus-5
    crafts:
      review: {model: claude-sonnet-5-5}
`

// routingFixture is the world the routing tests read: project aurora in a
// config home of its own with the table appended, a cached project layer, and
// the test standing in a real checkout whose fugaro.yaml pins the security
// review's model and the vision slot. The three documents are the three
// `fugaro routing ls` resolves, and the checkout is what a person running the
// command stands in.
// It returns the directory the test started in — this package's own — because
// the checkout is a temporary directory the test changes into, and a relative
// path to anything else on this tree stops resolving once it has.
func routingFixture(t *testing.T) (configPath, orig string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	path := isolateProjects(t, home)
	base, rerr := os.ReadFile(writeProject(t, "aurora", "aurora-gcp"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if err := os.WriteFile(path, append(base, []byte(routingLocalExtra)...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	routingSaveLayer(t, routingProjectLayer)
	testutil.IsolateGit(t)
	dir := filepath.Join(home, "checkout")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": routingRepoYAML})
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "-q", "-m", "the repository's files")
	t.Chdir(dir)
	// A write is refused in a coding agent's session and the fence reads the
	// streams: this test's world is a person's terminal, exactly as the
	// commands' own tests of the refusal spell it the other way round.
	clearAgentMarkers(t)
	terminal(t, false)
	return path, orig
}

// routingSaveLayer caches a project layer file as the last cloud read, the one
// modelsLayer reads. A test that needs a layer of its own writes it here rather
// than building a second world around the fixture.
func routingSaveLayer(t *testing.T, layer string) {
	t.Helper()
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "aurora-gcp", Bucket: "fugaro-runs-aurora-gcp",
		CheckedAt: time.Now(), YAML: layer,
	}); err != nil {
		t.Fatal(err)
	}
}

// routingModelsWant is the models the fixture resolves to, in the order the
// read names them: the crafts first — the coder's vendor model (which the merge
// judge inherits, so both crafts are named on its row), the first-line review's
// from the project layer, the senior review's from the installation's config and
// the security review's from the checkout — then the agent's pins, which the
// layer answers for as much as the checkout does: the layer's agent.model (one
// row for the coder and the reviewer, since both fall back to it) and its
// background pin, then the vision slot the checkout pins.
// It is spelled once so the text read and the document cannot disagree about it.
var routingModelsWant = []struct {
	model  string
	crafts string
	entry  string
}{
	{model: "deepseek/deepseek-v4.1-flash", crafts: config.CraftCoder + "," + config.CraftMergeJudge, entry: "deepseek/*"},
	{model: "claude-sonnet-5-5", crafts: config.CraftReview, entry: "claude-*"},
	{model: "claude-opus-5-5", crafts: config.CraftSeniorReview, entry: "claude-*"},
	{model: "claude-sonnet-5", crafts: config.CraftSecurityReview, entry: "claude-*"},
	{model: "claude-fable-5-1", crafts: "", entry: "claude-*"},
	{model: "claude-opus-5", crafts: "", entry: "claude-*"},
	{model: "claude-haiku-4-5", crafts: "", entry: "claude-*"},
}

// TestRoutingLs is the read: the table as the project wrote it, and one row per
// model a run of the project can be served on, with the entry that wins and why
// it won — the only entry that matches here, since the fixture's two patterns
// answer disjoint models.
func TestRoutingLs(t *testing.T) {
	routingFixture(t)
	out, _, err := execute(t, "routing", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "routing for project aurora (offline)") {
		t.Errorf("the read does not say which project it answered for:\n%s", out)
	}
	table := routingSection(t, out, "MATCH")
	for _, want := range []string{"claude-*", "deepseek/*", "launcher-pool", "pool:claude-pool", "provider:openrouter-main"} {
		if !strings.Contains(table, want) {
			t.Errorf("the table does not carry %q:\n%s", want, table)
		}
	}
	models := routingSection(t, out, "MODEL")
	if !strings.Contains(out, "WHY") {
		t.Errorf("the models table has no WHY column, and the read's whole point is saying why an entry won:\n%s", out)
	}
	for _, want := range routingModelsWant {
		line := routingLine(t, models, want.model)
		for _, cell := range []string{want.crafts, want.entry, "the only entry that matches"} {
			if !strings.Contains(line, cell) {
				t.Errorf("the %s row does not carry %q: %q", want.model, cell, line)
			}
		}
	}
}

// TestRoutingLevelModelComesFromTheLayer is the review level's rung of the
// model list: a project layer naming defaults.agent.levels.<level>.model names
// a model every repository of the project reviews on, and the read carries it
// even though no other document of this machine does. The layer cached here
// names that key alone, so the row can come from nowhere else.
func TestRoutingLevelModelComesFromTheLayer(t *testing.T) {
	routingFixture(t)
	routingSaveLayer(t, `version: 1
project: aurora
gcp_project: aurora-gcp
defaults:
  git: { provider: github }
  agent:
    auth: api-key
    levels:
      l2:
        model: claude-opus-5
`)
	out, _, err := execute(t, "routing", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	line := routingLine(t, routingSection(t, out, "MODEL"), "claude-opus-5")
	if !strings.Contains(line, "claude-*") {
		t.Errorf("the layer's level model is not a row of the read: %q\n%s", line, out)
	}
}

// TestRoutingOutsideACheckoutStillJudgesTheLayer: a write is judged against
// every model a run of the project can call, and the project layer names them
// whether or not the directory the command is run from is a checkout at all.
// The layer cached here pins a model no entry of the fixture's table matches —
// its background model, which no craft names — and the test then stands outside
// every repository. A model list that dropped the layer's pins there would
// accept a write that leaves that model unrouted, which is the fail-open R1 and
// R22 refuse.
func TestRoutingOutsideACheckoutStillJudgesTheLayer(t *testing.T) {
	path, _ := routingFixture(t)
	routingSaveLayer(t, `version: 1
project: aurora
gcp_project: aurora-gcp
defaults:
  git: { provider: github }
  agent:
    auth: api-key
    models:
      background: gemini-3-pro
`)
	t.Chdir(t.TempDir())
	out, _, err := execute(t, "routing", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if line := routingLine(t, routingSection(t, out, "MODEL"), "gemini-3-pro"); !strings.Contains(line, "no entry matches") {
		t.Errorf("the layer's own model is not a row of the read outside a checkout: %q\n%s", line, out)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, err := execute(t, "routing", "set", "claude-*", "pool:claude-pool")
	if err == nil {
		t.Fatalf("the write was accepted outside a checkout, leaving the layer's model unrouted:\n%s%s", out, errOut)
	}
	if !strings.Contains(err.Error(), "gemini-3-pro") {
		t.Errorf("the refusal is %q, and it must name the model the layer names", err.Error())
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(before, after) {
		t.Error("a refused write left the file changed")
	}
}

// TestRoutingLsJSONMatchesSchema validates the document the read writes against
// schemas/routing-ls.schema.json, the contract a consumer reads it by (D784),
// and holds the loaded document to the same rows the text read prints.
func TestRoutingLsJSONMatchesSchema(t *testing.T) {
	_, orig := routingFixture(t)
	out, _, err := execute(t, "routing", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(orig, "..", "..", "schemas", "routing-ls.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/routing-ls.schema.json has no $id")
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(id, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(out))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := sch.Validate(inst); err != nil {
		t.Fatalf("the document does not match schemas/routing-ls.schema.json: %v\n%s", err, out)
	}

	// The document is only worth validating while it carries the members the
	// schema closes over: a fixture that stopped producing an entry, a chain
	// longer than one source or a model row would leave those branches
	// unchecked and still green.
	var decoded routingLsDoc
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != routingLsDocVersion || decoded.Project != "aurora" {
		t.Errorf("the document says version %d of project %q, want %d of aurora", decoded.Version, decoded.Project, routingLsDocVersion)
	}
	if len(decoded.Entries) != 2 || len(decoded.Entries[0].Chain) != 2 || len(decoded.Entries[0].Sources) != 2 {
		t.Fatalf("the document's entries are not the fixture's two, the first with a two-source chain: %+v", decoded.Entries)
	}
	if len(decoded.Models) != len(routingModelsWant) {
		t.Fatalf("the document has %d model rows, want %d: %+v", len(decoded.Models), len(routingModelsWant), decoded.Models)
	}
	for i, want := range routingModelsWant {
		got := decoded.Models[i]
		if got.Model != want.model || got.Entry != want.entry || strings.Join(got.Crafts, ",") != want.crafts {
			t.Errorf("model row %d is %q/%v/%q, want %q/%s/%q", i, got.Model, got.Crafts, got.Entry, want.model, want.crafts, want.entry)
		}
	}

	// The spelling the document carries is the one `fugaro routing set` takes
	// back: the two are one grammar, and a spelling only one of them knows is a
	// table a person cannot write with the values the read hands them.
	for _, e := range decoded.Entries {
		for i, s := range e.Chain {
			src, err := routingParseSource(s)
			if err != nil {
				t.Fatalf("the documented spelling %q of %s's chain does not parse back: %v", s, e.Match, err)
			}
			if got := routingSourceString(src); got != s {
				t.Errorf("the spelling %q round-trips to %q, so `ls` and `set` disagree about it", s, got)
			}
			if got := e.Sources[i].Source; got != s {
				t.Errorf("the chain spells %q and the source row %q, and they are the same source", s, got)
			}
		}
	}
}

// TestRoutingSetWritesTheEntry is the write's own test: `set` appends a new
// pattern, replaces the entry a pattern already answers where it stands, and
// leaves every comment and every other key of the file alone.
func TestRoutingSetWritesTheEntry(t *testing.T) {
	path, _ := routingFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, err := execute(t, "routing", "set", "claude-opus-5-5", "pool:claude-pool", "anthropic")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	table := routingTableOf(t, after)
	if len(table) != 3 {
		t.Fatalf("the write left %d entries, want the fixture's two and the new one: %+v", len(table), table)
	}
	if table[2].Match != "claude-opus-5-5" || routingSourceString(table[2].Chain[0]) != "pool:claude-pool" {
		t.Errorf("the appended entry is %+v, want claude-opus-5-5 on pool:claude-pool", table[2])
	}
	// The document is spliced, not re-marshalled: everything outside the block
	// is the byte range it was (D790).
	if !strings.Contains(string(after), string(before[:bytes.Index(before, []byte("model_routing:"))])) {
		t.Error("the write rewrote what came before the model_routing block")
	}

	// A pattern the table already answers replaces its entry where it stands
	// rather than appending a second one, which the resolver would refuse as a
	// twin pattern (D3172).
	out, errOut, err = execute(t, "routing", "set", "claude-*", "vertex")
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, errOut)
	}
	replaced, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	table = routingTableOf(t, replaced)
	if len(table) != 3 || table[0].Match != "claude-*" || len(table[0].Chain) != 1 || table[0].Chain[0].Kind != routing.KindVertex {
		t.Fatalf("the second write left %+v, want claude-* replaced in place by one vertex source", table)
	}

	// What the write left is what the read prints, which is the whole of the
	// pair's contract: a table a person sets and a table a person reads are one
	// document.
	out, _, err = execute(t, "routing", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	models := routingSection(t, out, "MODEL")
	if line := routingLine(t, models, "claude-opus-5-5"); !strings.Contains(line, "claude-opus-5-5") || !strings.Contains(line, "the exact model ID") {
		t.Errorf("the read does not say the exact ID answers the model the write added: %q", line)
	}
}

// TestRoutingSetRefusesWithTheFix is the refusal half of both writes: every
// answer routing.Problems gives is a write that left nothing behind, and every
// argument this file refuses names what to write instead.
func TestRoutingSetRefusesWithTheFix(t *testing.T) {
	path, _ := routingFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			// RT3's CodeNoEntry: the three Claude models a run of this project
			// can be served on would be left with no entry at all.
			name: "removing the entry a model goes out on",
			args: []string{"routing", "rm", "claude-*"},
			want: "no entry of the table matches",
		},
		{
			// RT3's CodeNoReach: the project's Anthropic key serves Anthropic's
			// models and nothing else.
			name: "a source that cannot reach the model",
			args: []string{"routing", "set", "deepseek/*", "anthropic"},
			want: "reaches Anthropic's models",
		},
		{
			name: "a pool the project does not declare",
			args: []string{"routing", "set", "deepseek/*", "pool:nowhere"},
			want: "declares no pool by that name",
		},
		{
			name: "a source that is not one of the five kinds",
			args: []string{"routing", "set", "claude-*", "bucket:foo"},
			want: "is not a credential source",
		},
		{
			name: "a pool source with no pool",
			args: []string{"routing", "set", "claude-*", "pool"},
			want: "names no pool",
		},
		{
			name: "a downgrade with no model",
			args: []string{"routing", "set", "claude-*", "provider:openrouter-main@"},
			want: "has no model after",
		},
		{
			name: "a pattern the table does not carry",
			args: []string{"routing", "rm", "gemini-*"},
			want: "nothing to remove",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := execute(t, tc.args...)
			if err == nil {
				t.Fatalf("%v wrote a table it should have refused:\n%s%s", tc.args, out, errOut)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%v refused with %q, and the refusal must say %q", tc.args, err.Error(), tc.want)
			}
			after, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("%v wrote the file, and a refused write leaves it byte-identical:\n%s", tc.args, after)
			}
		})
	}

	// The launcher-pool-only chain is RT3's own rule, and it is reached through
	// `set`, which replaces the entry rather than removing it: the chain it
	// writes is what is refused.
	out, errOut, err := execute(t, "routing", "set", "deepseek/*", "launcher-pool")
	if err == nil {
		t.Fatalf("set wrote a launcher-pool-only chain:\n%s%s", out, errOut)
	}
	if !strings.Contains(err.Error(), "launcher's pool and nothing else") {
		t.Errorf("a launcher-pool-only chain refused with %q, and D3180's own sentence is what a person needs", err.Error())
	}

	// A refusal names the command that fixes it, with the model the row is
	// about, so the person reading it runs one thing rather than looking the
	// grammar up.
	out, errOut, err = execute(t, "routing", "rm", "claude-*")
	if err == nil {
		t.Fatalf("rm removed the entry the Claude models go out on:\n%s%s", out, errOut)
	}
	if !strings.Contains(err.Error(), "fugaro routing set") || !strings.Contains(err.Error(), "<source>") {
		t.Errorf("the refusal does not name the command and the shape of the fix: %q", err.Error())
	}
}

// TestRoutingSetRefusedInAnAgentSession: `set` is refused in a coding agent's
// session exactly as a cloud write is (the write fence of applySettingsEdit,
// SD9), because the file it writes decides how every run of the project spends.
// Nothing is written, and the refusal names the command to run at one's own
// terminal.
func TestRoutingSetRefusedInAnAgentSession(t *testing.T) {
	path, _ := routingFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDECODE", "1")
	out, errOut, err := execute(t, "routing", "set", "claude-opus-5-5", "vertex")
	if err == nil {
		t.Fatalf("set wrote the table in a coding agent's session:\n%s%s", out, errOut)
	}
	if !strings.Contains(err.Error(), "coding agent's session") {
		t.Errorf("the refusal is %q, and SD9's own sentence is what a person needs", err.Error())
	}
	after, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(before, after) {
		t.Error("a refused write left the file changed")
	}
	// The read is not a write and stays available: a coding agent may still
	// answer which entry a model goes out on.
	if _, _, err := execute(t, "routing", "ls"); err != nil {
		t.Errorf("routing ls is refused in a coding agent's session too: %v", err)
	}
}

// TestRoutingAsksNothing: no leaf of the family asks a question with its stdin
// closed, which is the policy's own rule (D1540: what replaced a confirmation
// is the sentence naming the change) and the reason `set` is refused by a fence
// rather than by a prompt.
func TestRoutingAsksNothing(t *testing.T) {
	for _, path := range []string{"fugaro routing ls", "fugaro routing set", "fugaro routing rm"} {
		t.Run(path, func(t *testing.T) {
			closedStdinWorld(t)
			out, returned, entered, err := closedStdinRun(t, path)
			if !returned {
				t.Fatalf("%s did not return with its stdin closed: it printed %q", path, out)
			}
			if !entered {
				t.Fatalf("%s never reached its own body with its stdin closed (%v), so this run says nothing about the command", path, err)
			}
			assertNoPrompt(t, out)
			closedStdinAsked(t, path, out)
		})
	}
}

// routingSection is one table of the read's text form, from its header to the
// blank line that ends it.
func routingSection(t *testing.T, out, header string) string {
	t.Helper()
	i := strings.Index(out, header)
	if i < 0 {
		t.Fatalf("the read has no %s section:\n%s", header, out)
	}
	rest := out[i:]
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// routingLine is the row of a table whose first cell is a model, matched whole
// rather than as a prefix: claude-sonnet-5 and claude-sonnet-5-5 are two models
// and two rows, and a prefix match would read the wrong one's answer.
func routingLine(t *testing.T, table, model string) string {
	t.Helper()
	for _, line := range strings.Split(table, "\n") {
		if cell, _, _ := strings.Cut(line, " "); cell == model {
			return line
		}
	}
	t.Fatalf("the table has no row for %s:\n%s", model, table)
	return ""
}

// routingTableOf reads the model_routing block back out of a written config, so
// a test judges the file the command left rather than the command's own idea of
// what it wrote.
func routingTableOf(t *testing.T, data []byte) routing.Table {
	t.Helper()
	cfg, err := localcfg.Parse(data)
	if err != nil {
		t.Fatalf("the written config does not read back: %v\n%s", err, data)
	}
	return cfg.ModelRouting
}
