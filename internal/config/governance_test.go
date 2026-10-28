package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/policy"
)

// TestGovernanceStrictDecode: governance: is a block like every other one.
// Its keys are the ones this release models and nothing else — a misspelt key,
// a stage that has no governance budget (an artifact run's plan and render,
// T7.1), and a tier name outside the vocabulary (governance.tiers, T8.1) are
// all refused rather than read as "no limit". A key left out, or written as
// null or 0, is unset and takes Fugaro's default (policy.DefaultGovernance).
//
// A count written as a fraction is not one of the cases: yaml.v3 reads 2.5
// into every int64 key of fugaro.yaml as 2, max_run_tokens included, and the
// JSON Schema is the surface that refuses it (fugaro.schema.json's integer
// type). Making governance the one block that reads the raw node would be a
// rule of its own for a key no run can use wrongly twice. The counting keys do
// carry an upper bound, though, and it is the schema's own (the constants
// above validateGovernance, held equal by TestSchemaGovernanceBoundsMatchGo).
func TestGovernanceStrictDecode(t *testing.T) {
	for _, c := range []struct {
		name string
		yaml string
		ok   bool
	}{
		{"the whole block", "governance:\n  review_ratio: 3\n  review_floor_calls: 10\n  max_subagents: 2\n  stage_budget: { implement: 0.6, review: 0.4, fix: 0.25 }\n  stage_tokens: { implement: 1000000, review: 500000, fix: 250000 }\n  stage_timeout: { implement: 30m, review: 20m, fix: 10m }\n", true},
		{"one key", "governance: { review_ratio: 3 }\n", true},
		{"an empty block", "governance: {}\n", true},
		{"a null block", "governance:\n", true},
		{"a null key", "governance: { review_ratio: ~ }\n", true},
		{"a zero key", "governance: { review_ratio: 0, max_subagents: 0 }\n", true},
		{"a zero stage budget", "governance: { stage_budget: { implement: 0 } }\n", true},
		{"a negative ratio", "governance: { review_ratio: -1 }\n", false},
		{"a negative floor", "governance: { review_floor_calls: -1 }\n", false},
		{"a negative fan-out", "governance: { max_subagents: -1 }\n", false},
		{"a misspelt ratio", "governance: { review_ratios: 3 }\n", false},
		{"a stage budget that reaches the cap", "governance: { stage_budget: { implement: 1 } }\n", false},
		{"a stage budget above the cap", "governance: { stage_budget: { review: 1.5 } }\n", false},
		{"a negative stage budget", "governance: { stage_budget: { fix: -0.5 } }\n", false},
		{"a stage with no governance budget", "governance: { stage_budget: { verify: 0.5 } }\n", false},
		{"an artifact stage", "governance: { stage_tokens: { plan: 1000 } }\n", false},
		{"an artifact timeout", "governance: { stage_timeout: { render: 5m } }\n", false},
		{"a negative token ceiling", "governance: { stage_tokens: { review: -1 } }\n", false},
		{"a zero timeout", "governance: { stage_timeout: { fix: 0s } }\n", false},
		{"an unreadable timeout", "governance: { stage_timeout: { fix: 5x } }\n", false},
		{"a block that is not a mapping", "governance: 0.7\n", false},
		{"a stage block that is not a mapping", "governance: { stage_budget: 0.7 }\n", false},
		{"the tiers block", "governance: { tiers: { trivial: { max_subagents: 3 } } }\n", true},
		{"every tier", "governance: { tiers: { trivial: { expected_usd: 0.25, expected_minutes: 10, max_subagents: 3 }, small: { expected_usd: 1, expected_minutes: 30, max_subagents: 3 }, medium: { expected_usd: 3, expected_minutes: 90 }, large: { expected_usd: 8, expected_minutes: 240 } } }\n", true},
		{"an unknown tier name", "governance: { tiers: { enormous: { max_subagents: 3 } } }\n", false},
		{"a tier's own unknown key", "governance: { tiers: { small: { expected_dollars: 1 } } }\n", false},
		{"a negative expected cost", "governance: { tiers: { small: { expected_usd: -1 } } }\n", false},
		{"a negative expected duration", "governance: { tiers: { small: { expected_minutes: -1 } } }\n", false},
		{"a negative tier fan-out", "governance: { tiers: { small: { max_subagents: -1 } } }\n", false},
		{"a tier fan-out above the schema's bound", "governance: { tiers: { small: { max_subagents: 101 } } }\n", false},
		{"a ratio above the schema's bound", "governance: { review_ratio: 1001 }\n", false},
		{"a fan-out above the schema's bound", "governance: { max_subagents: 101 }\n", false},
		{"a token ceiling above the schema's bound", "governance: { stage_tokens: { review: 1000000000001 } }\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, ps := Parse([]byte(minimalYAML + c.yaml))
			if (len(ps) == 0) != c.ok {
				t.Fatalf("problems = %v, want ok=%v", ps, c.ok)
			}
		})
	}
}

// TestGovernanceReadsAndConverts: the block lands in the config as written,
// and the conversion the merge reads is the same numbers (the policy package
// holds the merge's own shape, config the file's).
func TestGovernanceReadsAndConverts(t *testing.T) {
	cfg, ps := Parse([]byte(minimalYAML + "governance:\n  review_ratio: 3\n  max_subagents: 2\n  stage_budget: { fix: 0.25 }\n  stage_timeout: { implement: 45m }\n"))
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	g := cfg.Governance
	if g == nil {
		t.Fatal("governance = nil")
	}
	if g.ReviewRatio != 3 || g.MaxSubagents != 2 || g.StageBudget.Fix != 0.25 {
		t.Fatalf("governance = %+v", g)
	}
	if g.StageTimeout.Implement.Duration != 45*time.Minute || !g.StageTimeout.Implement.Set {
		t.Fatalf("stage_timeout.implement = %+v", g.StageTimeout.Implement)
	}
	want := policy.Governance{ReviewRatio: 3, MaxSubagents: 2,
		StageBudget: policy.StageBudget{Fix: 0.25}, StageTimeout: policy.StageTimeout{Implement: 45 * time.Minute}}
	if got := g.Policy(); got != want {
		t.Errorf("Policy() = %+v, want %+v", got, want)
	}
	// A file with no block converts to no governance, so a repository that
	// writes none leaves the project layer's ceiling alone.
	plain, ps := Parse([]byte(minimalYAML))
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	if plain.Governance != nil {
		t.Errorf("a file with no governance: block = %+v", plain.Governance)
	}
	if got := plain.Governance.Policy(); got != (policy.Governance{}) {
		t.Errorf("Policy() = %+v, want the zero value", got)
	}
}

// TestProjectLayerGovernanceIsTopLevel: the layer's block is a top-level key
// of the layer file, like claude_models, and not a member of defaults:. The
// defaults overlay is replace-not-tighten, so a block under it would be a
// value a repository's own file could replace with a looser one; a top-level
// block never meets the repository's file except in the tighten-only merge
// (internal/policy). A layer that writes the key under defaults: is refused
// as an unknown key there, which is what makes the placement a rule and not a
// convention.
func TestProjectLayerGovernanceIsTopLevel(t *testing.T) {
	head := "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"
	l, ps := ParseProjectLayer([]byte(head+"governance:\n  review_ratio: 20\n  stage_budget: { implement: 0.9 }\n"), testAnchor)
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	if l.Governance == nil || l.Governance.ReviewRatio != 20 || l.Governance.StageBudget.Implement != 0.9 {
		t.Fatalf("layer governance = %+v", l.Governance)
	}
	// The looser-than-default values a repository may not write are valid
	// here: the project layer is the one layer that loosens the guardrails,
	// and the merge is what drops a repository's own loosening (G6, D330).
	if _, ps := ParseProjectLayer([]byte(head+"defaults:\n  governance:\n    review_ratio: 3\n"), testAnchor); len(ps) == 0 {
		t.Error("defaults.governance was accepted: the block is a top-level key of the layer file")
	}
}

// TestGovernanceCorpus reads the two fixtures the schema corpus shares with
// this package, so the same file is held to both surfaces: the valid one
// parses to the values it writes, and each invalid one is refused at the path
// it exists to exercise (a misspelt key, and a stage budget that reaches the
// per-run cap).
func TestGovernanceCorpus(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "config", "valid", "governance.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, ps := Parse(data)
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	g := cfg.Governance
	if g == nil {
		t.Fatal("governance = nil")
	}
	if g.ReviewRatio != 4 || g.ReviewFloorCalls != 12 || g.MaxSubagents != 3 {
		t.Errorf("governance = %+v", g)
	}
	if g.StageBudget != (StageBudget{Implement: 0.6, Review: 0.4, Fix: 0.2}) {
		t.Errorf("stage_budget = %+v", g.StageBudget)
	}
	if g.StageTokens != (StageTokens{Implement: 1500000, Review: 800000}) {
		t.Errorf("stage_tokens = %+v", g.StageTokens)
	}
	if !g.StageTimeout.Implement.Set || g.StageTimeout.Implement.Duration != 45*time.Minute {
		t.Errorf("stage_timeout.implement = %+v", g.StageTimeout.Implement)
	}
	// The unknown key is the decoder's own problem, which carries the line
	// rather than a path (the block it sits in is what strictDecode names);
	// the fraction is validateGovernance's, which names the key.
	for file, want := range map[string]struct{ path, msg string }{
		"governance-unknown-key":      {"", "field stage_budgets not found in type config.Governance"},
		"governance-stage-budget-cap": {"governance.stage_budget.implement", "stays the ceiling"},
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "config", "invalid", file+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if _, ps := Parse(data); !hasProblem(ps, want.path, want.msg, 0) {
			t.Errorf("%s: want a problem at %q mentioning %q, got %v", file, want.path, want.msg, ps)
		}
	}
}

// TestGovernanceAbsentIsOutOfTheHash: SHA256 marshals the resolved config
// (resolve.go), so an absent block must not write a null key into every file
// that writes none — the same reason rigor: is a pointer with a json name
// (config.go), and the reason the field cannot simply be a value. A file that
// writes the block hashes differently from one that does not, or the two are
// one config to everything that keys a cache or a job by the hash.
func TestGovernanceAbsentIsOutOfTheHash(t *testing.T) {
	with, ps := Parse([]byte(minimalYAML + "governance: { review_ratio: 3 }\n"))
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	without, ps := Parse([]byte(minimalYAML))
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	if with.SHA256() == without.SHA256() {
		t.Error("the block is not part of the hash")
	}
	data, err := json.Marshal(without)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["governance"]; ok {
		t.Errorf("a config with no governance: block marshals a key for it: %s", data)
	}
}

// TestGovernanceCeilingReachesTheResolvedConfig settles where the two
// surfaces live: both are on one resolved config. The project layer's block —
// the ceiling's input, the one layer that may loosen a guardrail — is
// Config.Layer.Governance, and the repository's own block, which may only
// tighten it, is Config.Governance. That is why internal/projectpolicy needs
// no governance member: the published policy node is a different document
// (config/policy/shared, the owner's `fugaro project policy publish`) and
// carries no layer blocks at all — it has no rigor member either — so a
// governance key there would be a second home T7.2's wiring does not read.
// Governance lives in the layer file, published by `fugaro config publish
// FILE`.
func TestGovernanceCeilingReachesTheResolvedConfig(t *testing.T) {
	l := mustLayer(t, testLayer+"governance:\n  review_ratio: 20\n  stage_budget: { implement: 0.9 }\n")
	c, _, ps := Resolve([]byte(minimalRepo+"governance:\n  review_ratio: 3\n"), l)
	if len(ps) > 0 {
		t.Fatalf("problems: %v", ps)
	}
	if c.Layer == nil || c.Layer.Governance == nil || c.Layer.Governance.ReviewRatio != 20 ||
		c.Layer.Governance.StageBudget.Implement != 0.9 {
		t.Fatalf("the layer's block is not on the resolved config: %+v", c.Layer.Governance)
	}
	if c.Governance == nil || c.Governance.ReviewRatio != 3 || c.Governance.StageBudget.Implement != 0 {
		t.Fatalf("the repository's block = %+v", c.Governance)
	}
	// The merge is what brings them together, and the repository's tighter
	// value is the effective one (G6) while a key only the layer writes keeps
	// the layer's, which is the loosening the project layer is allowed.
	ceiling := policy.Layer{Governance: c.Layer.Governance.Policy()}.Governance.WithDefaults()
	// The middle layer is the default branch's own file, which a repository
	// that keeps the block only on its branch does not write.
	e := policy.Merge(policy.Layer{Governance: ceiling}, policy.Layer{}, policy.Layer{Governance: c.Governance.Policy()})
	if e.Governance.ReviewRatio != 3 || e.Governance.StageBudget.Implement != 0.9 {
		t.Fatalf("effective governance = %+v", e.Governance)
	}
	if e.Sources[policy.KeyReviewRatio] != policy.SourceBranch ||
		e.Sources[policy.KeyStageBudgetImplement] != policy.SourceCeiling {
		t.Errorf("sources = %v", e.Sources)
	}
}

// TestSetsGovernanceReadsTheKeyNotTheValue: a runner before 0.13.0 decodes the
// project layer with KnownFields, so what it refuses is governance: being
// written at all, and an empty block is refused exactly like a full one. The
// skew gate in fugaro update --repo asks this, so the answer has to come from
// the text: Governance is a value, and its zero value cannot tell
// `governance: {}` from a layer that never wrote the key.
func TestSetsGovernanceReadsTheKeyNotTheValue(t *testing.T) {
	const head = "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"
	for _, c := range []struct {
		text string
		want bool
	}{
		{head + "governance: { review_ratio: 20 }\n", true},
		{head + "governance: { tiers: { trivial: { max_subagents: 3 } } }\n", true},
		{head + "governance: {}\n", true},
		{head + "governance:\n", true},
		{head + "defaults:\n  git: { provider: github }\n", false},
		{head, false},
	} {
		if got := mustLayer(t, c.text).SetsGovernance(); got != c.want {
			t.Errorf("SetsGovernance() = %v, want %v for\n%s", got, c.want, c.text)
		}
	}
	if (*ProjectLayer)(nil).SetsGovernance() {
		t.Error("a nil layer sets nothing")
	}
	// The placement is read at the root, not under defaults:: the layer the
	// method is handed is one ParseProjectLayer accepted, and that one refuses
	// defaults.governance outright (TestProjectLayerGovernanceIsTopLevel), so
	// the only key this can ever find is the top-level one.
}

// TestRepoGovernanceSetReadsTheKeyNotTheValue: the repository's own file gets
// the same answer SetsGovernance gives a project layer, and for the same
// reason — a runner before 0.13.0 decodes fugaro.yaml with KnownFields, so
// what it refuses is the key being written at all, an explicit null included.
// Governance is a value and cannot answer (`governance:` decodes to a nil
// pointer), and the skew gates read this field rather than that one.
func TestRepoGovernanceSetReadsTheKeyNotTheValue(t *testing.T) {
	layer := mustLayer(t, testLayer)
	for _, c := range []struct {
		text string
		want bool
	}{
		{minimalRepo + "governance: { review_ratio: 3 }\n", true},
		{minimalRepo + "governance: { tiers: { trivial: { max_subagents: 3 } } }\n", true},
		{minimalRepo + "governance: {}\n", true},
		{minimalRepo + "governance:\n", true},
		{minimalRepo + "git: { pr: { labels: [mine] } }\n", false},
		{minimalRepo, false},
	} {
		cfg, _, ps := Resolve([]byte(c.text), layer)
		if len(ps) > 0 {
			t.Fatalf("%s: %v", c.text, ps)
		}
		if cfg.RepoGovernanceSet != c.want {
			t.Errorf("RepoGovernanceSet = %v, want %v for\n%s", cfg.RepoGovernanceSet, c.want, c.text)
		}
	}
	// The answer is the file's own key, never the merged one: a file that
	// writes the key over a layer that sets nothing still counts.
	null, _, ps := Resolve([]byte(minimalRepo+"governance:\n"), layer)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if !null.RepoGovernanceSet {
		t.Error("a file that writes governance: over a project layer lost its own key")
	}
	if null.Governance != nil {
		t.Errorf("a null block resolved a value: %+v", null.Governance)
	}
}
