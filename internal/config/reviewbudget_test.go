package config

import (
	"strings"
	"testing"
)

// reviewBudgetHead is a repo file whose one workflow names the timeouts block
// these tests write a share into.
const reviewBudgetHead = minimalRepo + "git: { provider: github }\nworkflows:\n  server:\n    commands: { build: make, test: make test }\n"

// TestReviewBudgetPctDefaultAndWrittenValue: the share of the budget the
// review and fix rounds may spend (BF1). A file that names none gets 80% of
// its total, a written 0 is the absent key — the shape every unset timeout's
// zero value has — and a file that names 100 has turned the stop off, since
// the threshold then is the run's own finalize reserve and the rounds end
// exactly as they did before this key existed.
func TestReviewBudgetPctDefaultAndWrittenValue(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		want       int
	}{
		{"unset", "", DefaultReviewBudgetPct},
		{"written as zero, the unset value", "    timeouts: { review_budget_pct: 0 }\n", DefaultReviewBudgetPct},
		{"a share of its own", "    timeouts: { review_budget_pct: 60 }\n", 60},
		{"the ceiling", "    timeouts: { review_budget_pct: 100 }\n", 100},
		// A short total does not move the share: it is a percentage of
		// whatever the run's total is, which is why the key is a percentage
		// and not a duration beside the total.
		{"a short total", "    timeouts: { total: 10m, review_budget_pct: 50 }\n", 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, ps := Parse([]byte(reviewBudgetHead + tc.line))
			if len(ps) > 0 {
				t.Fatalf("problems: %v", ps)
			}
			if got := cfg.Workflows["server"].Timeouts.ReviewBudgetPct; got != tc.want {
				t.Errorf("review_budget_pct = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestReviewBudgetPctRefused: a share outside 0..100 is refused on both
// surfaces a layer can write it on, each with the path and the wording of its
// own side — the repository file names the default, a profile names the
// workflow's own value, which is what 0 means there.
func TestReviewBudgetPctRefused(t *testing.T) {
	for _, tc := range []struct {
		name, line, path, msg string
	}{
		{"above the ceiling", "    timeouts: { review_budget_pct: 101 }\n", "workflows.server.timeouts.review_budget_pct", "must be a percentage from 1 to 100, or 0 for the default (80)"},
		{"below zero", "    timeouts: { review_budget_pct: -1 }\n", "workflows.server.timeouts.review_budget_pct", "must be a percentage from 1 to 100, or 0 for the default (80)"},
		{"not a number", "    timeouts: { review_budget_pct: most }\n", "", "cannot unmarshal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ps := Parse([]byte(reviewBudgetHead + tc.line)); !hasProblem(ps, tc.path, tc.msg, 0) {
				t.Errorf("want a problem at %q mentioning %q, got %v", tc.path, tc.msg, ps)
			}
		})
	}
	layerHead := "version: 1\nproject: acme\ngcp_project: acme-fugaro\nprofiles:\n  p:\n    commands: { build: make, test: make test }\n"
	for _, tc := range []struct{ name, line string }{
		{"above the ceiling", "    timeouts: { review_budget_pct: 101 }\n"},
		{"below zero", "    timeouts: { review_budget_pct: -1 }\n"},
	} {
		if got := layerProblems(t, layerHead+tc.line); !strings.Contains(got, "profiles.p.timeouts.review_budget_pct: must be a percentage from 1 to 100, or 0 for the workflow's own") {
			t.Errorf("%s: problems %q, want the profile's own path and wording", tc.name, got)
		}
	}
}

// TestReviewBudgetPctFromAProfile: the key is InProfile | InRepo (config.Scopes,
// docs/project-layer.md's table), so a profile may set it and the value reaches
// the workflow that started from it — the key-by-key merge that makes
// InProfile mean anything. Without the field on Timeouts a profile carrying
// it was refused by the strict decoder as an unknown key while the scope table
// and the docs said it was allowed.
func TestReviewBudgetPctFromAProfile(t *testing.T) {
	layer := mustLayer(t, "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"+
		"defaults:\n  git: { provider: github }\ndefault_profile: p\n"+
		"profiles:\n  p:\n    commands: { build: make, test: make test }\n    timeouts: { review_budget_pct: 60 }\n")
	if got := layer.Profiles["p"].Timeouts.ReviewBudgetPct; got != 60 {
		t.Errorf("Profiles[p].Timeouts.ReviewBudgetPct = %d, want 60", got)
	}
	c, res, ps := Resolve([]byte(minimalRepo), layer)
	if len(ps) > 0 {
		t.Fatalf("resolve problems: %v", ps)
	}
	if got := c.Workflows[ImplicitWorkflow].Timeouts.ReviewBudgetPct; got != 60 {
		t.Errorf("the resolved workflow's review_budget_pct = %d, want the profile's 60", got)
	}
	if got := res.SourceOf("workflows.default.timeouts.review_budget_pct"); got != SourceProfile("p") {
		t.Errorf("SourceOf(workflows.default.timeouts.review_budget_pct) = %q, want %q", got, SourceProfile("p"))
	}
	// A profile that names 0 leaves the workflow's own value in place.
	bare := mustLayer(t, "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"+
		"defaults:\n  git: { provider: github }\ndefault_profile: p\n"+
		"profiles:\n  p:\n    commands: { build: make, test: make test }\n    timeouts: { review_budget_pct: 0 }\n")
	c, _, ps = Resolve([]byte(minimalRepo), bare)
	if len(ps) > 0 {
		t.Fatalf("resolve problems: %v", ps)
	}
	if got := c.Workflows[ImplicitWorkflow].Timeouts.ReviewBudgetPct; got != DefaultReviewBudgetPct {
		t.Errorf("review_budget_pct = %d, want the default %d: a profile that names no share leaves the workflow's own", got, DefaultReviewBudgetPct)
	}
}

// TestReviewBudgetPctSchemaMatchesTheDecoder pins that both schemas accept and
// refuse the shares the strict decoder does: two surfaces read the same file —
// the decoder a run uses and the published schema an editor or a repository's
// CI checks against — and a value one refuses while the other accepts is a red
// check nobody can reproduce (the reason TestABSchemasMatchDecoder exists for
// ab:).
//
// A share written as a fraction is not one of the cases: yaml.v3 reads 80.5
// into every int key of fugaro.yaml as 80, and the JSON Schema is the surface
// that refuses it (fugaro.schema.json's integer type), the asymmetry
// TestGovernanceStrictDecode's own note records for the counting keys.
func TestReviewBudgetPctSchemaMatchesTheDecoder(t *testing.T) {
	repoSchema := compileABSchema(t, "fugaro.schema.json")
	layerSchema := compileABSchema(t, "project-layer.schema.json")
	layerHead := "version: 1\nproject: acme\ngcp_project: acme-fugaro\nprofiles:\n  p:\n    commands: { build: make, test: make test }\n"
	for _, tc := range []struct {
		name, value string
		ok          bool
	}{
		{"the unset value", "0", true},
		{"the lowest share", "1", true},
		{"a share of its own", "60", true},
		{"the ceiling, which turns the stop off", "100", true},
		{"a share over the ceiling", "101", false},
		{"a share below zero", "-1", false},
		{"a share that is not a number", "most", false},
	} {
		t.Run("repo/"+tc.name, func(t *testing.T) {
			text := reviewBudgetHead + "    timeouts: { review_budget_pct: " + tc.value + " }\n"
			if _, ps := Parse([]byte(text)); (len(ps) == 0) != tc.ok {
				t.Errorf("the decoder accepted = %v, want %v (problems: %v)", len(ps) == 0, tc.ok, ps)
			}
			if got := abSchemaAccepts(t, repoSchema, text); got != tc.ok {
				t.Errorf("fugaro.schema.json accepted = %v, want %v: the schema and the decoder disagree", got, tc.ok)
			}
		})
		t.Run("layer/"+tc.name, func(t *testing.T) {
			text := layerHead + "    timeouts: { review_budget_pct: " + tc.value + " }\n"
			if _, ps := ParseProjectLayer([]byte(text), testAnchor); (len(ps) == 0) != tc.ok {
				t.Errorf("the decoder accepted = %v, want %v (problems: %v)", len(ps) == 0, tc.ok, ps)
			}
			if got := abSchemaAccepts(t, layerSchema, text); got != tc.ok {
				t.Errorf("project-layer.schema.json accepted = %v, want %v: the schema and the decoder disagree", got, tc.ok)
			}
		})
	}
}
