package config

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/policy"
)

// TestGovernanceStrictDecode: governance: is a block like every other one.
// Its keys are the ones this release models and nothing else — a misspelt
// key, a stage that has no governance budget (an artifact run's plan and
// render, T7.1), and a key a later release defines (governance.tiers, T8.1)
// are all refused rather than read as "no limit". A key left out, or written
// as null or 0, is unset and takes Fugaro's default (policy.DefaultGovernance).
//
// A count written as a fraction is not one of the cases: yaml.v3 reads 2.5
// into every int64 key of fugaro.yaml as 2, max_run_tokens included, and the
// JSON Schema is the surface that refuses it (fugaro.schema.json's integer
// type). Making governance the one block that reads the raw node would be a
// rule of its own for a key no run can use wrongly twice.
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
		{"a later release's key", "governance: { tiers: { trivial: { max_subagents: 3 } } }\n", false},
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
