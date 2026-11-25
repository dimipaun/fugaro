package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// craftLocalExtra is what the craft fixtures append to the project config
// writeProject writes: the installation's own answer about the crafts
// (spec §1–3) — four credentials, a pool of the two subscriptions beside the
// Anthropic key, and the two crafts the spec makes the owner set — and the
// route the coder's model goes down, which is what makes the fixture the
// spec's own example served end to end: a DeepSeek model through OpenRouter
// needs a provider entry claiming it, or the read reports, correctly, that no
// route serves the model the coder names. The two subscriptions are what makes
// the pool inert while the project's subscription-spread switch is off
// (D1622), which is one of the four things a row has to be able to say.
//
// The model_prices block is the owner's own answer for the two Opus IDs the
// crafts pin, and the shape every installation of one is in now: a craft's model
// is judged by its price wherever a run has a gateway, whatever the budget mode
// says (AB4, D3020: the mode was the second condition and it is gone), and
// neither ID is in the built-in table, so the read refuses the row until a price
// is written here — in the words this block is the answer to.
const craftLocalExtra = `
credentials:
  - {name: openrouter-main, type: openrouter}
  - {name: claude-1, type: claude-oauth}
  - {name: claude-2, type: claude-oauth}
  - {name: anthropic-key, type: anthropic}
pools:
  - {name: claude-pool, members: [claude-1, claude-2, anthropic-key]}
crafts:
  coder: {model: deepseek/deepseek-v4.1-flash, access: openrouter-main}
  senior-review: {model: claude-opus-4, access: claude-pool}
model_prices:
  claude-opus-4: {input_per_m: 15, output_per_m: 75}
  claude-opus-4-1: {input_per_m: 15, output_per_m: 75}
providers:
  openrouter-main:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: [deepseek/*]
`

// craftRoutingTable is the routing table the craft fixtures append beside
// craftLocalExtra: the project's own answer about where each model goes, and
// one RT3 accepts whatever budget the arms below write. The Claude entry puts
// the launcher's own credentials before the project's pool (D3176) and names no
// paid source at all, because a paid source after a pool is R17's refusal
// without an enforcing budget — which is what `validate` reports a table for
// (RT6) and what these arms are not about. The arms that answer the budget
// append their own block to this config, so this one may not write one.
//
// It is a block of its own rather than a line of the const above because the
// bridge is a state of its own: a project that publishes no table at all
// (D3170), which craftFixtureBridge is the fixture for.
const craftRoutingTable = `
model_routing:
  - match: deepseek/*
    chain:
      - {kind: provider, provider: openrouter-main}
  - match: claude-*
    chain:
      - {kind: launcher-pool}
      - {kind: pool, pool: claude-pool}
`

// craftRepoYAML is the checkout's own fugaro.yaml: it selects the first-line
// review's access — a name the installation config defines, which is the
// ordinary case a repository's file writes (F12, D1628) — and the security
// review's model, so the two halves of one craft come from two layers.
const craftRepoYAML = `version: 1
project: aurora
git: { provider: github }
agent:
  auth: api-key
  crafts:
    review: {access: anthropic-key}
    security-review: {model: claude-opus-4-1}
`

// craftProjectLayer is the project layer file the last cloud read cached, the
// third layer the read resolves: the only place a craft's MODEL may be set
// besides the two above (config.Scopes refuses a craft's access there, because
// anyone who can write the runs bucket can write that file).
const craftProjectLayer = `version: 1
project: aurora
gcp_project: aurora-gcp
defaults:
  git: { provider: github }
  agent:
    auth: api-key
    crafts:
      review: {model: claude-sonnet-5-5}
`

// craftProjectOAuthLayer is craftProjectLayer under the project's own
// `defaults.agent.auth: oauth`: an oauth run has no gateway, so nothing its
// crafts name is priced (validate's own gate, cfg.Agent.Auth beside the mode).
// The auth is written here and not in the checkout's file because the project
// layer is where a project answers the question, and the two documents
// disagreeing is what the arm using this layer is about.
const craftProjectOAuthLayer = `version: 1
project: aurora
gcp_project: aurora-gcp
defaults:
  git: { provider: github }
  agent:
    auth: oauth
    crafts:
      review: {model: claude-sonnet-5-5}
`

// craftFixture is the three-layer world every craft test reads: project aurora
// in a config home of its own, with the crafts, credentials and pools appended
// to its config, a cached project layer, and the test standing in a real
// checkout whose fugaro.yaml selects the two halves craftRepoYAML writes. The
// checkout is what the read resolves for its narrowest layer, and standing in
// it is what a person running the command does.
//
// It returns the directory the test started in — this package's own — because
// the checkout is a temporary directory the test changes into, and a relative
// path to anything else on this tree stops resolving once it has.
func craftFixture(t *testing.T) string {
	t.Helper()
	return craftFixtureWith(t, "")
}

// craftFixtureWith is craftFixture with one more block appended to the
// installation's own config, which is where the subscription-spread switch is
// written: the read takes the switch from there, so a test about it needs a
// world whose config says so.
func craftFixtureWith(t *testing.T, extra string) string {
	t.Helper()
	return craftFixtureBody(t, craftLocalExtra+craftRoutingTable+extra)
}

// craftFixtureBridge is the same world with no routing table at all: the state
// every installation that has not migrated is in (D3170, R25), where the crafts
// are reached the release-1 way and the table is not the document that answers
// for their calls. Everything else — the credentials, the pool, the crafts and
// their accesses — is the fixture above's.
func craftFixtureBridge(t *testing.T) string {
	t.Helper()
	return craftFixtureBody(t, craftLocalExtra)
}

// craftRoutingTableElsewhere is a table that serves another repository and not
// this checkout's own: every source names an admit list the checkout is not in
// (D3182), so no entry of it governs a run of this repository — which is the
// second way a project is on the release-1 bridge, beside publishing no table at
// all (O7, R25, D3196).
const craftRoutingTableElsewhere = `
model_routing:
  - match: claude-*
    chain:
      - {kind: anthropic, admit: [other/repo]}
`

// craftFixtureElsewhere is the fixture world whose routing table serves another
// repository: the checkout names aurora/api as its origin and the table admits
// only other/repo, so the project publishes a table that is not in force for
// these runs.
func craftFixtureElsewhere(t *testing.T) string {
	t.Helper()
	orig := craftFixtureBody(t, craftLocalExtra+craftRoutingTableElsewhere)
	testutil.Git(t, ".", "remote", "add", "origin", "https://github.com/aurora/api.git")
	return orig
}

// craftFixtureBody is the world every craft fixture builds, from the text
// appended to the installation's own config.
func craftFixtureBody(t *testing.T, body string) string {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	path := isolateProjects(t, home)
	base, err := os.ReadFile(writeProject(t, "aurora", "aurora-gcp"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(base, []byte(body)...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	writeCraftLayer(t, craftProjectLayer)
	testutil.IsolateGit(t)
	dir := filepath.Join(home, "checkout")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": craftRepoYAML})
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	t.Chdir(dir)
	return orig
}

// writeCraftLayer caches the project layer file the read resolves, in the
// config home the fixture isolated: the fixture's own layer, or another a test
// hands the same world — a project answers the crafts' models, the auth its
// runs are under and the rest of its defaults there, so an arm about one of
// those answers writes this file.
func writeCraftLayer(t *testing.T, layer string) {
	t.Helper()
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "aurora-gcp", Bucket: "fugaro-runs-aurora-gcp",
		CheckedAt: time.Now(), YAML: layer,
	}); err != nil {
		t.Fatal(err)
	}
}

// craftRows is the document the fixture resolves to, spelled once so the two
// tests below cannot disagree about what the layers mean. It is the spec's own
// example read through the command: the coder is a DeepSeek model the table
// routes to the OpenRouter provider, the three Claude models resolve to the
// launcher's own credentials and then the project's pool of two subscriptions
// (D3176, D3180: a chain is written, and the second source is what the first
// falls back to), the first-line review keeps the model the project layer set,
// and the security review takes a model of its own.
//
// Every row is servable here, which is the one thing this fixture has to say
// about MC16's member: the coder's model is claimed by the route the fixture's
// provider entry declares and so carries its starter price, the three Claude IDs
// are ones the routing switch does not name and so go direct, and every one of
// them is priced — the installation's own model_prices block covers the two Opus
// pins and the built-in table the review's claude-sonnet-5-5 — because a craft's
// model is judged by its price wherever a run has a gateway (AB4, D3020: the
// mode is not read here). The rows that cannot be served are the next test's,
// with the causes it names.
func craftRows() []craftLsRow {
	return []craftLsRow{
		{
			Craft: config.CraftCoder, Model: "deepseek/deepseek-v4.1-flash", ModelFrom: craft.SourceLocal,
			Chain: []string{"provider:openrouter-main"}, Servable: true,
		},
		{
			// The model is the project layer's (nothing narrower sets one), and
			// the chain is the table's, which no layer of a craft writes.
			Craft: config.CraftReview, Model: "claude-sonnet-5-5", ModelFrom: craft.SourceProject,
			Chain: []string{"launcher-pool", "pool:claude-pool"}, Servable: true,
		},
		{
			Craft: config.CraftSeniorReview, Model: "claude-opus-4", ModelFrom: craft.SourceLocal,
			Chain: []string{"launcher-pool", "pool:claude-pool"}, Servable: true,
		},
		{
			// The spec's own example of a craft that keeps a model it inherits
			// nothing of: its model is the repository's and its chain the
			// table's, like every other row's.
			Craft: config.CraftSecurityReview, Model: "claude-opus-4-1", ModelFrom: craft.SourceRepo,
			Chain: []string{"launcher-pool", "pool:claude-pool"}, Servable: true,
		},
	}
}

// TestCraftLsTheThreeLayers is the read's own test: the fixture's crafts, each
// half with the layer it came from — the checkout's file, the installation's
// config, the project layer file — and the pool that serves nothing yet said
// so on the rows that name it, including the row that inherits it.
func TestCraftLsTheThreeLayers(t *testing.T) {
	craftFixture(t)
	out, errOut, err := execute(t, "craft", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "crafts for project aurora (offline)") {
		t.Errorf("the read does not say which project it answered for:\n%s", out)
	}
	for _, want := range []string{"CRAFT", "MODEL", "MODEL FROM", "CHAIN", "SERVES"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table has no %s column:\n%s", want, out)
		}
	}
	for _, row := range craftRows() {
		line := craftLine(t, out, row.Craft)
		for _, cell := range []string{row.Model, row.ModelFrom, strings.Join(row.Chain, " → ")} {
			if !strings.Contains(line, cell) {
				t.Errorf("the %s row does not carry %q: %q", row.Craft, cell, line)
			}
		}
	}
	if errOut != "" && strings.Contains(errOut, "fugaro: problem:") {
		t.Errorf("every access names a credential or a pool a layer holds, so the read has nothing to warn about: %q", errOut)
	}
}

// TestCraftLsOnTheBridgeShowsNoChainProblem is the read on a project that
// publishes no routing table: its calls go out the release-1 way, on the access
// its crafts resolve to (D3170, R25), so there is no entry to match and nothing
// is missing — every row carries an empty chain and no problem, and the row's
// verdict stays the model's own.
//
// It is the state every installation is in until `fugaro update` writes a table
// (MG1's migration), so the answer matters more than the table's own: a read
// that asked the empty table anyway would report every craft of every unmigrated
// project as unserved, in the launch gate's words, for a refusal no launch makes
// — the gate asks whether the table serves the repository before it judges a
// model by it (chainRoutingGate, infra.ChainPlanFor).
func TestCraftLsOnTheBridgeShowsNoChainProblem(t *testing.T) {
	craftFixtureBridge(t)
	out, errOut, err := execute(t, "craft", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	jsOut, _, err := execute(t, "craft", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, jsOut)
	}
	craftLsOnTheReleaseOneBridge(t, out, errOut, jsOut)
}

// TestCraftLsWhereTheTableServesAnotherRepo is the same read on the second state
// that means the same thing: the project publishes a table, and every source of
// it is admitted to another repository (D3182, D3196), so no entry of it governs
// these runs. The read's answer is the one above's, row for row, because the
// question it asks first is the gate's own — whether the table serves this
// repository — and not whether a table exists at all.
func TestCraftLsWhereTheTableServesAnotherRepo(t *testing.T) {
	craftFixtureElsewhere(t)
	out, errOut, err := execute(t, "craft", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	jsOut, _, err := execute(t, "craft", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, jsOut)
	}
	craftLsOnTheReleaseOneBridge(t, out, errOut, jsOut)
}

// craftLsOnTheReleaseOneBridge holds a read to the release-1 answer: no row is
// refused and no row carries a chain or a problem, every craft that names a model
// is served by the checks of its own facts, and the document has no
// chain_problem member. The two states that mean it — a project that publishes no
// routing table and one whose every source is admitted to another repository
// (D3170, O7, R25, D3196) — are one answer, so the two tests above share this.
func craftLsOnTheReleaseOneBridge(t *testing.T, out, errOut, jsOut string) {
	t.Helper()
	if strings.Contains(out, "no entry") || strings.Contains(out, "no:") {
		t.Errorf("no table serves this repository, so no row of it is refused:\n%s", out)
	}
	for _, row := range craftRows() {
		line := craftLine(t, out, row.Craft)
		if !strings.Contains(line, "yes") {
			t.Errorf("the %s row is not served on the release-1 bridge: %q", row.Craft, line)
		}
		if strings.Contains(line, "→") {
			t.Errorf("the %s row prints a chain, and a table that does not serve this repository has none to print: %q", row.Craft, line)
		}
	}
	if errOut != "" && strings.Contains(errOut, "fugaro: problem:") {
		t.Errorf("the fixture's accesses all resolve, so the read has nothing to warn about: %q", errOut)
	}
	var doc craftLsDoc
	if err := json.Unmarshal([]byte(jsOut), &doc); err != nil {
		t.Fatal(err)
	}
	for _, row := range doc.Crafts {
		if len(row.Chain) != 0 || row.ChainProblem != "" {
			t.Errorf("the %s row resolves to %v / %q, and no table that serves this repository has an entry to resolve it by", row.Craft, row.Chain, row.ChainProblem)
		}
		if row.Model != "" && !row.Servable {
			t.Errorf("the %s row is not servable on the release-1 bridge: %s", row.Craft, row.ModelProblem)
		}
	}
	if strings.Contains(jsOut, "chain_problem") {
		t.Errorf("the document carries a chain_problem member where no table serves the repository:\n%s", jsOut)
	}
}

// craftLine is the table's row for one craft: the row whose first cell is the
// craft's name, which is what a reader finds it by. A missing row fails the
// test rather than returning the header, so a craft the table dropped is
// reported as a missing row and not as a row of wrong cells.
func craftLine(t *testing.T, out, craft string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == craft {
			return line
		}
	}
	t.Fatalf("the table has no %s row:\n%s", craft, out)
	return ""
}

// TestCraftLsJSONMatchesSchema validates the document the command writes
// against schemas/craft-ls.schema.json, the contract a consumer reads it by
// (F16: the document is versioned from its first release because the console
// reads it), and holds its rows to what the three layers mean.
func TestCraftLsJSONMatchesSchema(t *testing.T) {
	pkg := craftFixture(t)
	out, _, err := execute(t, "craft", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(pkg, "..", "..", "schemas", "craft-ls.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := doc.(map[string]any)["$id"].(string)
	if id == "" {
		t.Fatal("schemas/craft-ls.schema.json has no $id")
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
		t.Fatalf("the document does not match schemas/craft-ls.schema.json: %v\n%s", err, out)
	}

	var decoded craftLsDoc
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != craftLsDocVersion {
		t.Errorf("version = %d, want craftLsDocVersion (%d)", decoded.Version, craftLsDocVersion)
	}
	if decoded.Project != "aurora" {
		t.Errorf("project = %q, want aurora", decoded.Project)
	}
	if len(decoded.Crafts) != len(config.CraftNames) {
		t.Fatalf("the document carries %d rows, want one per craft (%d)", len(decoded.Crafts), len(config.CraftNames))
	}
	for i, want := range craftRows() {
		// Compared member by member rather than as a value: the row carries a
		// slice (the chain), so no two rows are Go-comparable.
		got := decoded.Crafts[i]
		if got.Craft != want.Craft || got.Model != want.Model || got.ModelFrom != want.ModelFrom ||
			got.ChainProblem != want.ChainProblem || got.Servable != want.Servable || got.ModelProblem != want.ModelProblem ||
			!slices.Equal(got.Chain, want.Chain) {
			t.Errorf("crafts[%d] = %+v, want %+v", i, got, want)
		}
	}
	if len(decoded.Problems) != 0 {
		t.Errorf("the fixture's accesses all resolve, so the read has no problems: %v", decoded.Problems)
	}

	// No secret value, and not by accident of the fixture: the document has no
	// member a value could ride in. A credential here is a name and a type
	// (spec §1) and the secret behind it is stored under a name derived from
	// this one, so a member of this document that could carry one is a defect
	// the schema would not catch.
	for _, member := range []string{`"secret"`, `"value"`, `"token"`, `"api_key"`, `"key"`} {
		if strings.Contains(out, member) {
			t.Errorf("the document carries a %s member, and a credential's value is stored nowhere a reader of it can reach:\n%s", member, out)
		}
	}
	// And every row that names a model resolves to a chain, since the fixture's
	// table routes every model it names: a model with no chain is one no run
	// could call, and a document that printed an empty one without saying so
	// would be the read hiding the refusal (R22).
	for _, row := range decoded.Crafts {
		if row.Model != "" && len(row.Chain) == 0 {
			t.Errorf("the %s row names %s and resolves to no chain: %s", row.Craft, row.Model, row.ChainProblem)
		}
	}
}

// craftRepoUnknownAccessYAML is the checkout's file with one access that names
// nothing any layer holds: a name the repository's own file may write — a
// repository's file selects among the credentials the owner created elsewhere
// (F12, D1628), so it cannot know whether the name exists — and the case the
// resolver reports rather than silently replacing.
const craftRepoUnknownAccessYAML = `version: 1
project: aurora
git: { provider: github }
agent:
  auth: api-key
  crafts:
    review: {access: claude-9}
`

// TestCraftLsReportsAnAccessThatNamesNothing: an access naming no credential
// and no pool any layer holds is the resolver's problem, reported at the path
// of the key in the document that wrote it — the repository's own
// agent.crafts.<craft>.access, the installation config's crafts.<craft>.access,
// the project layer's defaults.agent.crafts.<craft>.access — because a reader
// told to fix a key that is not in the file they have is sent to the wrong
// file. Nothing is substituted for it: the row carries the chain the craft's
// model resolves to, and the warning is where the key that names nothing is
// reported, so a reader is sent to the document they have (RT6: the row shows
// the chain, and the access is on its way out with the migration, D3170).
func TestCraftLsReportsAnAccessThatNamesNothing(t *testing.T) {
	craftFixture(t)
	testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": craftRepoUnknownAccessYAML})

	out, errOut, err := execute(t, "craft", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(errOut, "agent.crafts.review.access") {
		t.Errorf("the warning does not name the key in the document that wrote it:\n%s", errOut)
	}

	jsonOut, _, err := execute(t, "craft", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, jsonOut)
	}
	var decoded craftLsDoc
	if err := json.Unmarshal([]byte(jsonOut), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Problems) != 1 || !strings.Contains(decoded.Problems[0], "agent.crafts.review.access") {
		t.Errorf("problems = %v, want the one key the checkout's file wrote", decoded.Problems)
	}
}

// craftRepoUnservedModelYAML is the checkout's file with the coder's model set
// to one no route claims: a provider model reaches a call only through a
// provider entry the installation declares (config.craftRouteProblems), and the
// fixture's route claims deepseek/* alone.
const craftRepoUnservedModelYAML = `version: 1
project: aurora
git: { provider: github }
agent:
  auth: api-key
  crafts:
    coder: {model: qwen/qwen3-coder}
`

// craftRepoResolvableYAML is the same checkout file in a shape config.Resolve
// accepts, which is what puts the file into the merge the row is judged by: a
// file the rules refuse contributes no layer at all (the read judges no craft by
// a config no run would start on), so the arms that use the two files above
// never exercise the file half of the allow-list. Beyond the anchor
// (gcp_project) and the workflows block every fugaro.yaml needs, it is the
// fixture's own file plus one narrowing line: an allow-list that names the
// Claude IDs alone.
//
// The block writes no mode, which is what keeps this arm to one check: the mode
// a layer leaves unwritten is no mode at all for the readers here (ceilingLayer
// and craft's own `priced` flag), so the price rule is not asked and the
// allow-list is the row's one sentence. The word that used to say that (`off`)
// is refused by this very read now (AB2, D3009, D3022), which is why the file
// names no mode rather than the word.
const craftRepoResolvableYAML = `version: 1
project: aurora
gcp_project: aurora-gcp
git: { provider: github }
workflows:
  go: { commands: { build: go build ./..., test: go test ./... } }
budget: { allowed_models: [claude-sonnet-5-5] }
agent:
  auth: api-key
  crafts:
    review: {access: anthropic-key}
    security-review: {model: claude-opus-4-1}
`

// craftRepoUnpricedModelYAML is the same accepted file without an agent.auth of
// its own -- the project layer answers that, which is the arm's subject -- under
// a mode that prices (observe) and naming a model no price row covers. It is
// the one shape in which the price gate is visible: a run under a mode that
// prices and an auth with a gateway would refuse this model, and a run whose
// project answered oauth would charge nothing for it.
const craftRepoUnpricedModelYAML = `version: 1
project: aurora
gcp_project: aurora-gcp
git: { provider: github }
workflows:
  go: { commands: { build: go build ./..., test: go test ./... } }
budget: { mode: observe }
agent:
  crafts:
    coder: {model: deepseek/deepseek-v9}
`

// craftRowRaw is one row of the document as it was printed, keyed by member, so
// a test can tell an absent member from a member that says false: a struct's
// zero value would make the two the same answer, and "the member is there and
// says the model cannot be served" is the whole of what this test is about.
func craftRowRaw(t *testing.T, out, craft string) map[string]json.RawMessage {
	t.Helper()
	var doc struct {
		Crafts []map[string]json.RawMessage `json:"crafts"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, row := range doc.Crafts {
		var name string
		if err := json.Unmarshal(row["craft"], &name); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if name == craft {
			return row
		}
	}
	t.Fatalf("the document has no %s row:\n%s", craft, out)
	return nil
}

// craftRowServes reads one row's two members as a reader of the document does:
// the sentence, and whether the member that carries it is there at all.
func craftRowServes(t *testing.T, out, craft string) (bool, string) {
	t.Helper()
	row := craftRowRaw(t, out, craft)
	raw, ok := row["model_servable"]
	if !ok {
		t.Fatalf("the %s row carries no model_servable member:\n%s", craft, out)
	}
	var servable bool
	if err := json.Unmarshal(raw, &servable); err != nil {
		t.Fatalf("the %s row's model_servable is not a boolean: %v\n%s", craft, err, out)
	}
	why := ""
	if raw, ok := row["model_problem"]; ok {
		if err := json.Unmarshal(raw, &why); err != nil {
			t.Fatalf("the %s row's model_problem is not a string: %v\n%s", craft, err, out)
		}
	}
	return servable, why
}

// TestCraftLsRowNamesWhyAModelCannotBeServed (MC16, D1665): a row says whether
// a run can serve the model it names, and when it cannot, the one sentence
// naming what is missing — the same verdict MC15's checks report, asked over
// the same facts a run has (the allow-list a run of this checkout is under, the
// routes this repository's runs carry and the prices its calls are charged
// from). The arms are the checks MC16's member covers, and each one
// is a model the row would otherwise let a reader promise themselves a run on —
// the price ones three times over, because the gate that asks it is the auth
// alone now (AB4, D3020) and an arm that read the mode would answer instead.
//
// The member is what the row gained: a reader that finds no model_servable has
// an older CLI's row, which is why every arm reads the member out of the
// printed JSON rather than off a struct's zero value.
//
// Red step: every arm fails on the tree before this task, where the row carries
// no model_servable at all.
func TestCraftLsRowNamesWhyAModelCannotBeServed(t *testing.T) {
	t.Run("no route claims the model", func(t *testing.T) {
		craftFixture(t)
		testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": craftRepoUnservedModelYAML})
		out, _, err := execute(t, "craft", "ls", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		servable, problem := craftRowServes(t, out, config.CraftCoder)
		if servable {
			t.Errorf("the coder row says its model is servable, and no route this repository's runs carry claims qwen/qwen3-coder: %s", out)
		}
		for _, want := range []string{"qwen/qwen3-coder", "no provider serves it"} {
			if !strings.Contains(problem, want) {
				t.Errorf("the coder row's sentence does not name %q: %q", want, problem)
			}
		}
		// The other three rows are the control: the member is a verdict about
		// each model, not a blanket "no" wherever a layer or a route is unset.
		for _, craft := range []string{config.CraftReview, config.CraftSeniorReview, config.CraftSecurityReview} {
			if servable, problem := craftRowServes(t, out, craft); !servable || problem != "" {
				t.Errorf("the %s row says its model is not servable (%s), and its model is a Claude ID the routing switch does not name, which goes direct: %s", craft, problem, out)
			}
		}
	})

	t.Run("the allow-list excludes the model", func(t *testing.T) {
		// The coder's own model, under a project whose allow-list names the
		// Claude IDs alone: the arm the ceiling contributes, and the one a run
		// is refused by whatever its routes say. The block names no mode — the arm
		// that used to say the word `off`, which is refused now (AB2, D3009), and a
		// mode left unset is observe (C1, R3) — and the price rule is asked anyway:
		// a run with a gateway prices its calls in every mode (AB4, D3020), and the
		// coder's model carries the starter price its route's entry gives it, so the
		// allow-list is the one problem this arm sees: one check, one sentence.
		craftFixtureWith(t, "budget: { allowed_models: [claude-sonnet-5-5] }\n")
		out, _, err := execute(t, "craft", "ls", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		servable, problem := craftRowServes(t, out, config.CraftCoder)
		if servable {
			t.Errorf("the coder row says deepseek/deepseek-v4.1-flash is servable under an allow-list that does not name it: %s", out)
		}
		for _, want := range []string{"deepseek/deepseek-v4.1-flash", "not an allowed model", "claude-sonnet-5-5"} {
			if !strings.Contains(problem, want) {
				t.Errorf("the sentence does not name %q: %q", want, problem)
			}
		}
		if servable, _ := craftRowServes(t, out, config.CraftReview); !servable {
			t.Errorf("the review row is refused, and the project layer's claude-sonnet-5-5 is the one ID the allow-list names: %s", out)
		}
	})

	t.Run("no price row covers the model", func(t *testing.T) {
		// A model the fixture's route claims and the price table does not know:
		// the third check MC16's member covers, asked wherever a run really
		// charges for the craft — an auth with a gateway, which is now the whole
		// of the gate (AB4, D3020: the mode is not read).
		craftFixtureWith(t, "budget: { mode: observe }\n")
		testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": strings.Replace(craftRepoUnservedModelYAML,
			"qwen/qwen3-coder", "deepseek/deepseek-v9", 1)})
		out, _, err := execute(t, "craft", "ls", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		servable, problem := craftRowServes(t, out, config.CraftCoder)
		if servable {
			t.Errorf("the coder row says deepseek/deepseek-v9 is servable, and the price table has no row for it: %s", out)
		}
		for _, want := range []string{"deepseek/deepseek-v9", "no price"} {
			if !strings.Contains(problem, want) {
				t.Errorf("the sentence does not name %q: %q", want, problem)
			}
		}
	})

	t.Run("the mode is off and the price is still asked", func(t *testing.T) {
		// AB4 (D3020): the gate is the auth alone, so the mode that used to
		// switch the price rule off switches nothing off. This is the arm the
		// row stopped being silent about — the same world as the arm above, one
		// mode down — and it is what makes the read's own answer the run's: a
		// run with a gateway prices its calls in every mode.
		craftFixtureWith(t, "budget: { mode: off }\n")
		testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": craftRepoUnpricedModelYAML})
		out, _, err := execute(t, "craft", "ls", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		servable, problem := craftRowServes(t, out, config.CraftCoder)
		if servable {
			t.Errorf("the coder row says deepseek/deepseek-v9 is servable under an off mode, and this run has a gateway: every run with one is priced, whatever the mode says (AB4, D3020): %s", out)
		}
		for _, want := range []string{"deepseek/deepseek-v9", "no price"} {
			if !strings.Contains(problem, want) {
				t.Errorf("the sentence does not name %q: %q", want, problem)
			}
		}
	})

	t.Run("the project answers oauth, so nothing is priced", func(t *testing.T) {
		// The auth the price gate reads is the resolved one, and it is the
		// project's to write: this checkout's file names no auth at all, and
		// the layer answers oauth -- no gateway, nothing charged, so no price
		// row is required of a craft. The file names the same model the arm
		// above refuses, under an auth whose gateway would price it, so a gate
		// read off the checkout's own block (api-key by default) reports a
		// refusal where a run would charge nothing. The file is one
		// config.Resolve accepts because a refused file contributes no layer and
		// there would be no resolved auth to read at all.
		craftFixture(t)
		writeCraftLayer(t, craftProjectOAuthLayer)
		testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": craftRepoUnpricedModelYAML})
		out, _, err := execute(t, "craft", "ls", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		servable, problem := craftRowServes(t, out, config.CraftCoder)
		if !servable {
			t.Errorf("the coder row refuses deepseek/deepseek-v9 (%s), and this project's auth is oauth: no call of its goes through a gateway, so none of its crafts is priced: %s", problem, out)
		}
		// The control: the same model under a project whose auth has a gateway
		// is refused, which is the arm above's answer and the one this arm is
		// the exception to.
		writeCraftLayer(t, craftProjectLayer)
		out, _, err = execute(t, "craft", "ls", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if servable, problem := craftRowServes(t, out, config.CraftCoder); servable {
			t.Errorf("the coder row says deepseek/deepseek-v9 is servable under an api-key project, and no price row covers it: %s", out)
		} else if !strings.Contains(problem, "no price") {
			t.Errorf("the control's sentence is not the price rule's: %q", problem)
		}
	})

	t.Run("the checkout's own allow-list excludes the model", func(t *testing.T) {
		// The file half of the allow-list, and the arm that needs a checkout
		// file config.Resolve accepts: the fixture's other files are refused
		// (no gcp_project to anchor to the project layer, no workflows block),
		// so they contribute no layer at all and every other arm here judges by
		// the project's answer alone. This file narrows on its own -- the
		// coder's DeepSeek model is the ID it does not name -- and the read has
		// to reach that layer for the row to be refused, which is what makes
		// the file's bytes part of the answer rather than a document read for
		// nothing.
		craftFixture(t)
		testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": craftRepoResolvableYAML})
		out, _, err := execute(t, "craft", "ls", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		servable, problem := craftRowServes(t, out, config.CraftCoder)
		if servable {
			t.Errorf("the coder row says deepseek/deepseek-v4.1-flash is servable, and this checkout's own fugaro.yaml names claude-sonnet-5-5 alone: %s", out)
		}
		for _, want := range []string{"deepseek/deepseek-v4.1-flash", "not an allowed model", "claude-sonnet-5-5"} {
			if !strings.Contains(problem, want) {
				t.Errorf("the sentence does not name %q: %q", want, problem)
			}
		}
		// The control, so the arm is not a blanket refusal: the review row's
		// model is the one ID this file's list names.
		if servable, problem := craftRowServes(t, out, config.CraftReview); !servable {
			t.Errorf("the review row says claude-sonnet-5-5 cannot be served (%s), and it is the one ID this file's allow-list names: %s", problem, out)
		}
	})

	t.Run("the craft names no model", func(t *testing.T) {
		// The other side of the member, and the F17 rule it would be easy to
		// get wrong: a machine with no installation selected sets no craft's
		// model at all, and a craft that names no model is served by the role
		// slot below it rather than being a model of its own that is missing --
		// so the row says servable and writes no sentence, exactly as a row
		// whose model every check admits does.
		craftFixture(t)
		t.Setenv("FUGARO_CONFIG", "")
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		out, _, err := execute(t, "craft", "ls", "--json")
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		for _, name := range config.CraftNames {
			if servable, problem := craftRowServes(t, out, name); !servable || problem != "" {
				t.Errorf("the %s row says its model cannot be served (%s), and no run's facts are held here for a model to fail: %s", name, problem, out)
			}
		}
		row := craftRowRaw(t, out, config.CraftCoder)
		if _, ok := row["model"]; ok {
			t.Errorf("the coder row names a model, and no layer in this world sets one:\n%s", out)
		}
	})
}

// TestCraftLsSchemaPinsTheDocumentVersion holds schemas/craft-ls.schema.json to
// the rule the document is versioned by (F16, D1667, and CLAUDE.md's own): MC16
// added `model_servable` and `model_problem` within version 1, because a member
// added is a member a reader of that version may ignore, while RT6 removes the
// access, the layer it came from and the inert flag (D3170) -- a removal, which
// is what bumps the version rather than a name reused (CLAUDE.md). So the
// version is 2, and the members below are version 2's.
//
// The file is read as its own JSON rather than compiled, because what a consumer
// of this document reads is the file: a version const only the Go struct still
// carried, or a member typed one way here and another way there, would leave
// that consumer reading a promise this repository no longer makes. The compiled
// read is TestCraftLsJSONMatchesSchema's, which is the other direction -- the
// command's own output against this file.
func TestCraftLsSchemaPinsTheDocumentVersion(t *testing.T) {
	pkg := craftFixture(t)
	raw, err := os.ReadFile(filepath.Join(pkg, "..", "..", "schemas", "craft-ls.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	top, _ := doc["properties"].(map[string]any)
	version, _ := top["version"].(map[string]any)
	if got := version["const"]; got != float64(craftLsDocVersion) {
		t.Errorf("the schema's version const = %v, want craftLsDocVersion (%d): a member added within a version keeps the version, and only a renamed, retyped or removed member bumps it (D1667)", got, craftLsDocVersion)
	}

	row, _ := doc["$defs"].(map[string]any)["craft"].(map[string]any)
	props, _ := row["properties"].(map[string]any)
	if props == nil {
		t.Fatal("schemas/craft-ls.schema.json has no $defs.craft.properties")
	}
	// Every member a row carries and the type it is written in. A member that
	// changes meaning or type is a new member under a new name with the old one
	// kept, and one that goes is a bumped version -- so within a version this
	// list only ever grows, and it is spelled here rather than read off the
	// file, or a rename would move the pin with it.
	wantTypes := map[string]string{
		"craft":          "string",
		"model":          "string",
		"model_from":     "string",
		"chain":          "array",
		"chain_problem":  "string",
		"model_servable": "boolean",
		"model_problem":  "string",
	}
	var names []string
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(props) != len(wantTypes) {
		t.Errorf("the row carries %d members (%v), want %d: version 2's three halves, MC16's two and nothing else", len(props), names, len(wantTypes))
	}
	for name, want := range wantTypes {
		p, ok := props[name].(map[string]any)
		if !ok {
			t.Errorf("the row has no %q member: the members are %v, and two of them are MC16's (model_servable, model_problem)", name, names)
			continue
		}
		if got := p["type"]; got != want {
			t.Errorf("the row's %q member is typed %v, want %q: a retyped member under the same name draws a wrong figure in a consumer with no error, which is why it bumps the version instead", name, got, want)
		}
	}

	// model_servable is required because every row answers it, and
	// model_problem is not: a servable row writes no sentence at all, so the
	// member is absent rather than empty, and a consumer that read a blank one
	// as the reason would report a reason there is none of.
	required := map[string]bool{}
	list, ok := row["required"].([]any)
	if !ok {
		t.Fatal("schemas/craft-ls.schema.json's $defs.craft has no required list")
	}
	for _, r := range list {
		name, _ := r.(string)
		required[name] = true
	}
	for name, want := range map[string]bool{"model_servable": true, "model_problem": false, "chain": true, "chain_problem": false} {
		if required[name] != want {
			t.Errorf("the row's required list carries %q = %v, want %v", name, required[name], want)
		}
	}
}

// TestCraftLsShowsTheChain is the read's subject after RT6 (D3170): every row
// carries the model it runs and the chain the project's routing table resolves
// that model to, where the access and the layer it came from used to be. The
// column a release behind printed is gone -- a row that still said ACCESS would
// be this read and the table disagreeing about where a craft's calls go out --
// and a model no entry matches has no chain and says why, rather than being
// reached by whatever source happened to be printed first (R1, R22).
func TestCraftLsShowsTheChain(t *testing.T) {
	craftFixture(t)
	out, _, err := execute(t, "craft", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "ACCESS") {
		t.Errorf("the table still carries an access column:\n%s", out)
	}
	for _, want := range []string{"CRAFT", "MODEL", "MODEL FROM", "CHAIN", "SERVES"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table has no %s column:\n%s", want, out)
		}
	}
	for _, row := range craftRows() {
		line := craftLine(t, out, row.Craft)
		chain := strings.Join(row.Chain, " → ")
		if !strings.Contains(line, chain) {
			t.Errorf("the %s row does not carry the chain %q its model resolves to: %q", row.Craft, chain, line)
		}
	}

	// The other half of the column: a model no entry of the table matches is
	// the one a run cannot call, and the row says so in the checks' own words
	// and with the fix `fugaro routing ls` would give it (RT5's own sentence).
	testutil.WriteFiles(t, ".", map[string]string{"fugaro.yaml": craftRepoUnservedModelYAML})
	raw, _, err := execute(t, "craft", "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	coder := craftRowRaw(t, raw, config.CraftCoder)
	var chain []string
	if err := json.Unmarshal(coder["chain"], &chain); err != nil {
		t.Fatalf("the coder row's chain is not an array: %v (%s)", err, raw)
	}
	if len(chain) != 0 {
		t.Errorf("the coder row resolves qwen/qwen3-coder to %v, and no entry of the fixture's table matches it: %s", chain, raw)
	}
	var why string
	if err := json.Unmarshal(coder["chain_problem"], &why); err != nil {
		t.Fatalf("the coder row carries no chain_problem: %v (%s)", err, raw)
	}
	for _, want := range []string{"qwen/qwen3-coder", "no entry matches", "fugaro routing set"} {
		if !strings.Contains(why, want) {
			t.Errorf("the coder row's chain_problem does not name %q: %q", want, why)
		}
	}
	// And the text form says the same thing in the SERVES cell, which is the
	// whole verdict now that nothing resolves to an inert pool: a craft whose
	// model is priced and routed nowhere is served by no run.
	text, _, err := execute(t, "craft", "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if line := craftLine(t, text, config.CraftCoder); !strings.Contains(line, "no: ") {
		t.Errorf("the coder row serves no chain and its SERVES cell does not say so: %q", line)
	}
}
