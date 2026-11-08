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
	"github.com/dimipaun/fugaro/internal/craft"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// craftLocalExtra is what the craft fixtures append to the project config
// writeProject writes: the installation's own answer about the crafts
// (spec §1–3) — four credentials, a pool of the two subscriptions beside the
// Anthropic key, and the two crafts the spec makes the owner set. The two
// subscriptions are what makes the pool inert while the project's
// subscription-spread switch is off (D1622), which is one of the four things
// a row has to be able to say.
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
	if err := os.WriteFile(path, append(base, []byte(craftLocalExtra)...), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	if err := localcfg.SaveLayerCache(os.Getenv, "aurora", localcfg.SharedCacheEntry{
		GCPProject: "aurora-gcp", Bucket: "fugaro-runs-aurora-gcp",
		CheckedAt: time.Now(), YAML: craftProjectLayer,
	}); err != nil {
		t.Fatal(err)
	}
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

// craftRows is the document the fixture resolves to, spelled once so the two
// tests below cannot disagree about what the layers mean. It is the spec's own
// example read through the command: the coder is DeepSeek through OpenRouter,
// the senior review is Opus on a pool of two subscriptions, the first-line
// review goes out on the Anthropic key and keeps the coder's model, and the
// security review takes the senior review's pool and a model of its own.
func craftRows() []craftLsRow {
	return []craftLsRow{
		{
			Craft: config.CraftCoder, Model: "deepseek/deepseek-v4.1-flash", ModelFrom: craft.SourceLocal,
			Access: "openrouter-main", AccessFrom: craft.SourceLocal,
		},
		{
			// The halves part: the model is the project layer's (nothing
			// narrower sets one) and the access the repository's.
			Craft: config.CraftReview, Model: "claude-sonnet-5-5", ModelFrom: craft.SourceProject,
			Access: "anthropic-key", AccessFrom: craft.SourceRepo,
		},
		{
			Craft: config.CraftSeniorReview, Model: "claude-opus-4", ModelFrom: craft.SourceLocal,
			Access: "claude-pool", AccessFrom: craft.SourceLocal, Inert: true,
		},
		{
			// The spec's own example of the halves parting the other way.
			Craft: config.CraftSecurityReview, Model: "claude-opus-4-1", ModelFrom: craft.SourceRepo,
			Access: "claude-pool", AccessFrom: craft.InheritedFrom(config.CraftSeniorReview), Inert: true,
		},
	}
}

// TestCraftLsTheThreeLayers is the read's own test: the four crafts, each
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
	for _, want := range []string{"CRAFT", "MODEL", "ACCESS", "SERVES"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table has no %s column:\n%s", want, out)
		}
	}
	for _, row := range craftRows() {
		line := craftLine(t, out, row.Craft)
		for _, cell := range []string{row.Model, row.ModelFrom, row.Access, row.AccessFrom} {
			if !strings.Contains(line, cell) {
				t.Errorf("the %s row does not carry %q: %q", row.Craft, cell, line)
			}
		}
		if got := strings.Contains(line, "no: the subscription-spread switch is off"); got != row.Inert {
			t.Errorf("the %s row says inert=%v, want %v: %q", row.Craft, got, row.Inert, line)
		}
	}
	if errOut != "" && strings.Contains(errOut, "fugaro: problem:") {
		t.Errorf("every access names a credential or a pool a layer holds, so the read has nothing to warn about: %q", errOut)
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
		if decoded.Crafts[i] != want {
			t.Errorf("crafts[%d] = %+v, want %+v", i, decoded.Crafts[i], want)
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
	// And the names it does carry are the ones the layers wrote, never a
	// substituted default: an access that resolved to something else would
	// hide the misconfiguration from the reader who has to fix it.
	for _, row := range decoded.Crafts {
		if row.Access == "" {
			t.Errorf("the %s row names no access: an access no layer sets is the default credential, which is never an empty name", row.Craft)
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
// file. The craft that wrote it keeps the name it wrote and the layer it wrote
// it in: a reader has to see the misconfiguration, not a silent fallback.
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
	line := craftLine(t, out, config.CraftReview)
	if !strings.Contains(line, "claude-9") || !strings.Contains(line, craft.SourceRepo) {
		t.Errorf("the review row does not carry the name as written and the layer it was written in: %q", line)
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
