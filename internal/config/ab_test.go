package config

import "testing"

// testLayerAB is a project layer carrying the A/B block in full
// (docs/model-ab-testing, D550-D573).
const testLayerAB = `version: 1
project: acme
gcp_project: acme-fugaro
ab:
  rate: 0.05
  max_pairs_per_day: 2
  challengers: [deepseek/chat-x, anthropic/claude-haiku-4-5]
  judge_model: anthropic/claude-opus-5-5
  exchange_rate: { poc: 0, startup: 0.25, pro: 0.5, critical: 2, hotfix: 0 }
default_profile: node-web
profiles:
  node-web:
    commands: { build: npm run build, test: npm test }
`

// TestABStrictDecode pins that a project layer may carry an ab: block (D550:
// the A/B keys are core MIT). Every key of the block is read by the strict
// decoder, so all five must exist as fields of the block rather than be
// refused as unknown keys; the repository's own file is a separate test
// (TestABRepoMayOnlyOptOut), because the two files may carry different keys.
func TestABStrictDecode(t *testing.T) {
	_, ps := ParseProjectLayer([]byte(testLayerAB), testAnchor)
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
}
