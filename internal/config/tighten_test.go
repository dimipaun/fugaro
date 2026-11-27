package config

import (
	"slices"
	"strconv"
	"testing"

	"github.com/dimipaun/fugaro/internal/policy"
)

// tightenLayer is a project layer whose defaults block is defaults, with the
// one profile a repository file that names no workflow of its own resolves
// through.
func tightenLayer(t *testing.T, defaults string) *ProjectLayer {
	t.Helper()
	return mustLayer(t, "version: 1\nproject: acme\ngcp_project: acme-fugaro\ndefaults: "+defaults+
		"\nprofiles:\n  p:\n    commands: { build: make, test: make test }\ndefault_profile: p\n")
}

// ftoa is a resolved float as the merge renders the text a file wrote.
func ftoa(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// TestTightenOnly is B4 (spec §5.2) key by key: a repository value looser than
// the project's is dropped, the project's stands and the resolution records
// it (policy.Ignored); a tighter one wins; and where the project says nothing
// the repository's value stands, marked unchecked (R3, RT2).
//
// One sub-test per row of §5.2's table. agent.levels.lN.model is the one row
// missing: D3291 makes it a lifted key, dropped before this merge ever sees
// it (TestLiftedDrop). agent.recipe and agent.workspace_beat keep the replace
// rule (D3292, D3293) and are pinned as such below.
func TestTightenOnly(t *testing.T) {
	for _, tc := range []struct {
		name      string
		project   string // the project layer's defaults block
		loose     string // the repository's agent block, looser than the project's
		tight     string // the repository's agent block, tighter than the project's
		path      string // the resolved path the rule is about
		projText  string // the project's value, as the record renders it
		looseText string // the repository's looser value, as the record renders it
		tightText string // the repository's tighter value, and the one that stands
		// silentText is what a silent project resolves to, where the default
		// the key supplies is not the text the file wrote — `max_budget_usd`
		// written 0 is the built-in 25 (empty: the repository's own text).
		silentText string
		get        func(*Config) string
	}{
		{
			name:      "review_rounds",
			project:   "{ git: { provider: github }, agent: { review_rounds: 5 } }",
			loose:     "agent:\n  review_rounds: 2\n",
			tight:     "agent:\n  review_rounds: 9\n",
			path:      "agent.review_rounds",
			projText:  "5",
			looseText: "2",
			tightText: "9",
			get:       func(c *Config) string { return itoa(c.Agent.ReviewRounds) },
		},
		{
			name:      "max_budget_usd",
			project:   "{ git: { provider: github }, agent: { max_budget_usd: 10 } }",
			loose:     "agent:\n  max_budget_usd: 25\n",
			tight:     "agent:\n  max_budget_usd: 5\n",
			path:      "agent.max_budget_usd",
			projText:  "10",
			looseText: "25",
			tightText: "5",
			get:       func(c *Config) string { return ftoa(c.Agent.MaxBudgetUSD) },
		},
		{
			// 0 is not a cap: it is the key's "unset" spelling, which the
			// defaults turn into the built-in 25, so a repository writing it
			// against a project's cap asks for a larger one rather than a
			// smaller, and it is dropped like any other looser value.
			name:       "max_budget_usd_zero",
			project:    "{ git: { provider: github }, agent: { max_budget_usd: 5 } }",
			loose:      "agent:\n  max_budget_usd: 0\n",
			tight:      "agent:\n  max_budget_usd: 1\n",
			path:       "agent.max_budget_usd",
			projText:   "5",
			looseText:  "0",
			tightText:  "1",
			silentText: "25",
			get:        func(c *Config) string { return ftoa(c.Agent.MaxBudgetUSD) },
		},
		{
			name:      "first_line_review",
			project:   "{ git: { provider: github }, agent: { first_line_review: auto } }",
			loose:     "agent:\n  first_line_review: off\n",
			tight:     "agent:\n  first_line_review: on\n",
			path:      "agent.first_line_review",
			projText:  "auto",
			looseText: "off",
			tightText: "on",
			get:       func(c *Config) string { return c.Agent.FirstLineReview },
		},
		{
			name:      "first_line_rounds",
			project:   "{ git: { provider: github }, agent: { first_line_rounds: 2 } }",
			loose:     "agent:\n  first_line_rounds: 1\n",
			tight:     "agent:\n  first_line_rounds: 3\n",
			path:      "agent.first_line_rounds",
			projText:  "2",
			looseText: "1",
			tightText: "3",
			get:       func(c *Config) string { return itoa(c.Agent.FirstLineRounds) },
		},
		{
			name:      "adversarial_rounds",
			project:   "{ git: { provider: github }, agent: { adversarial_rounds: 5 } }",
			loose:     "agent:\n  adversarial_rounds: 2\n",
			tight:     "agent:\n  adversarial_rounds: 8\n",
			path:      "agent.adversarial_rounds",
			projText:  "5",
			looseText: "2",
			tightText: "8",
			get:       func(c *Config) string { return itoa(c.Agent.AdversarialRounds) },
		},
		{
			name:      "review_stop_fraction",
			project:   "{ git: { provider: github }, agent: { review_stop_fraction: 0.5 } }",
			loose:     "agent:\n  review_stop_fraction: 0.2\n",
			tight:     "agent:\n  review_stop_fraction: 0.9\n",
			path:      "agent.review_stop_fraction",
			projText:  "0.5",
			looseText: "0.2",
			tightText: "0.9",
			get:       func(c *Config) string { return ftoa(c.Agent.ReviewStopFraction) },
		},
		{
			name:      "levels_effort",
			project:   "{ git: { provider: github }, agent: { levels: { l3: { effort: medium } } } }",
			loose:     "agent:\n  levels:\n    l3: { effort: low }\n",
			tight:     "agent:\n  levels:\n    l3: { effort: xhigh }\n",
			path:      "agent.levels.l3.effort",
			projText:  "medium",
			looseText: "low",
			tightText: "xhigh",
			get:       func(c *Config) string { return c.Agent.Levels.L3.Effort },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := tightenLayer(t, tc.project)

			// A looser value is dropped: the project's stands, its source is
			// the project's, and the resolution records the value it dropped.
			c, res, ps := Resolve([]byte(minimalRepo+tc.loose), l)
			if len(ps) > 0 {
				t.Fatalf("a looser repository value: %s", problemsText(ps))
			}
			if got := tc.get(c); got != tc.projText {
				t.Errorf("a looser repository value: %s = %q, want the project's %q", tc.path, got, tc.projText)
			}
			if src := res.SourceOf(tc.path); src != SourceProject {
				t.Errorf("a looser repository value: SourceOf(%s) = %q, want %q", tc.path, src, SourceProject)
			}
			want := []policy.Ignored{{Key: tc.path, Value: tc.looseText, Effective: tc.projText, Source: SourceProject}}
			if !slices.Equal(res.Ignored, want) {
				t.Errorf("a looser repository value: ignored = %+v, want %+v", res.Ignored, want)
			}

			// A tighter one wins.
			c, res, ps = Resolve([]byte(minimalRepo+tc.tight), l)
			if len(ps) > 0 {
				t.Fatalf("a tighter repository value: %s", problemsText(ps))
			}
			if got := tc.get(c); got != tc.tightText {
				t.Errorf("a tighter repository value: %s = %q, want %q", tc.path, got, tc.tightText)
			}
			if src := res.SourceOf(tc.path); src != SourceRepo {
				t.Errorf("a tighter repository value: SourceOf(%s) = %q, want %q", tc.path, src, SourceRepo)
			}
			if len(res.Ignored) != 0 {
				t.Errorf("a tighter repository value: ignored = %+v, want none", res.Ignored)
			}

			// A silent project has nothing to tighten against: the
			// repository's value stands, marked unchecked (R3).
			silent := tightenLayer(t, "{ git: { provider: github } }")
			c, res, ps = Resolve([]byte(minimalRepo+tc.loose), silent)
			if len(ps) > 0 {
				t.Fatalf("a silent project: %s", problemsText(ps))
			}
			silentText := tc.silentText
			if silentText == "" {
				silentText = tc.looseText
			}
			if got := tc.get(c); got != silentText {
				t.Errorf("a silent project: %s = %q, want the repository's %q", tc.path, got, silentText)
			}
			if !slices.Contains(res.Unchecked, tc.path) {
				t.Errorf("a silent project: unchecked = %v, want it to name %s", res.Unchecked, tc.path)
			}
		})
	}

	t.Run("levels_none", func(t *testing.T) {
		// A level on beats off: the project turns the level on and the
		// repository turns it off, so the off is dropped and recorded.
		on := tightenLayer(t, "{ git: { provider: github }, agent: { levels: { l3: { effort: high } } } }")
		c, res, ps := Resolve([]byte(minimalRepo+"agent:\n  levels:\n    l3: { none: true }\n"), on)
		if len(ps) > 0 {
			t.Fatalf("the repository turns the project's level off: %s", problemsText(ps))
		}
		if l3 := c.Agent.Levels.L3; l3 == nil || l3.Off || l3.Effort != "high" {
			t.Errorf("the repository turns the project's level off: l3 = %+v, want the project's level on", l3)
		}
		want := []policy.Ignored{{Key: "agent.levels.l3.none", Value: "true", Effective: "false", Source: SourceProject}}
		if !slices.Equal(res.Ignored, want) {
			t.Errorf("the repository turns the project's level off: ignored = %+v, want %+v", res.Ignored, want)
		}

		// The other direction tightens, so it wins: a repository turns on a
		// level the project turned off.
		off := tightenLayer(t, "{ git: { provider: github }, agent: { levels: { l3: { none: true } } } }")
		c, res, ps = Resolve([]byte(minimalRepo+"agent:\n  levels:\n    l3: { effort: xhigh }\n"), off)
		if len(ps) > 0 {
			t.Fatalf("the repository turns the project's none back on: %s", problemsText(ps))
		}
		if l3 := c.Agent.Levels.L3; l3 == nil || l3.Off || l3.Effort != "xhigh" {
			t.Errorf("the repository turns the project's none back on: l3 = %+v, want it on at xhigh", l3)
		}
		if len(res.Ignored) != 0 {
			t.Errorf("the repository turns the project's none back on: ignored = %+v, want none", res.Ignored)
		}

		// A project that sets no level at all leaves the repository's off as
		// its own answer, unjudged.
		silent := tightenLayer(t, "{ git: { provider: github } }")
		c, res, ps = Resolve([]byte(minimalRepo+"agent:\n  levels:\n    l3: { none: true }\n"), silent)
		if len(ps) > 0 {
			t.Fatalf("a silent project: %s", problemsText(ps))
		}
		if l3 := c.Agent.Levels.L3; l3 == nil || !l3.Off {
			t.Errorf("a silent project: l3 = %+v, want the repository's level off", l3)
		}
		if !slices.Contains(res.Unchecked, "agent.levels.l3.none") {
			t.Errorf("a silent project: unchecked = %v, want it to name agent.levels.l3.none", res.Unchecked)
		}
	})

	t.Run("levels_focus", func(t *testing.T) {
		// Focus is additive: both texts stand, one line each, and neither is
		// dropped.
		l := tightenLayer(t, "{ git: { provider: github }, agent: { levels: { l3: { focus: project } } } }")
		c, res, ps := Resolve([]byte(minimalRepo+"agent:\n  levels:\n    l3: { focus: repo }\n"), l)
		if len(ps) > 0 {
			t.Fatalf("both layers give a focus: %s", problemsText(ps))
		}
		if got := c.Agent.Levels.L3.Focus; got != "project\n\nrepo" {
			t.Errorf("both layers give a focus: l3.focus = %q, want both texts quoted", got)
		}
		if len(res.Ignored) != 0 {
			t.Errorf("both layers give a focus: ignored = %+v, want none", res.Ignored)
		}

		// A silent project leaves the repository's own text, marked unchecked.
		silent := tightenLayer(t, "{ git: { provider: github } }")
		c, res, ps = Resolve([]byte(minimalRepo+"agent:\n  levels:\n    l3: { focus: repo }\n"), silent)
		if len(ps) > 0 {
			t.Fatalf("a silent project: %s", problemsText(ps))
		}
		if got := c.Agent.Levels.L3.Focus; got != "repo" {
			t.Errorf("a silent project: l3.focus = %q, want the repository's", got)
		}
		if !slices.Contains(res.Unchecked, "agent.levels.l3.focus") {
			t.Errorf("a silent project: unchecked = %v, want it to name agent.levels.l3.focus", res.Unchecked)
		}
	})

	t.Run("recipe", func(t *testing.T) {
		// D3292: a repository may name any project or catalog recipe, so the
		// replace rule stays.
		l := tightenLayer(t, "{ git: { provider: github }, agent: { recipe: claude-solo } }")
		c, res, ps := Resolve([]byte(minimalRepo+"agent:\n  recipe: mine\n"), l)
		if len(ps) > 0 {
			t.Fatalf("the repository names another recipe: %s", problemsText(ps))
		}
		if c.Agent.Recipe != "mine" {
			t.Errorf("the repository names another recipe: recipe = %q, want %q", c.Agent.Recipe, "mine")
		}
		if src := res.SourceOf("agent.recipe"); src != SourceRepo {
			t.Errorf("the repository names another recipe: SourceOf(agent.recipe) = %q, want %q", src, SourceRepo)
		}
		if len(res.Ignored) != 0 {
			t.Errorf("the repository names another recipe: ignored = %+v, want none", res.Ignored)
		}
	})

	t.Run("workspace_beat", func(t *testing.T) {
		// D3293: workspace_beat is not review depth, so the replace rule
		// stays.
		l := tightenLayer(t, "{ git: { provider: github }, agent: { workspace_beat: true } }")
		c, res, ps := Resolve([]byte(minimalRepo+"agent:\n  workspace_beat: false\n"), l)
		if len(ps) > 0 {
			t.Fatalf("the repository turns the beat off: %s", problemsText(ps))
		}
		if c.Agent.WorkspaceBeatOn() {
			t.Error("the repository turns the beat off: the beat is on, want the repository's off")
		}
		if src := res.SourceOf("agent.workspace_beat"); src != SourceRepo {
			t.Errorf("the repository turns the beat off: SourceOf(agent.workspace_beat) = %q, want %q", src, SourceRepo)
		}
		if len(res.Ignored) != 0 {
			t.Errorf("the repository turns the beat off: ignored = %+v, want none", res.Ignored)
		}
	})
}

// TestProjectRaiseKeepsRepoFileValid: a project layer that raises its review
// depth never makes a repository's file invalid (spec §5.2). Every key the
// repository writes looser than the project's is dropped and recorded, and
// the file resolves with no problem of its own — release B, not release A, is
// what refuses one.
func TestProjectRaiseKeepsRepoFileValid(t *testing.T) {
	l := tightenLayer(t, "{ git: { provider: github }, agent: { review_rounds: 8, max_budget_usd: 40, first_line_review: on, first_line_rounds: 3, adversarial_rounds: 9, review_stop_fraction: 0.9, levels: { l3: { effort: max, focus: project } } } }")
	repo := minimalRepo + `agent:
  review_rounds: 1
  max_budget_usd: 100
  first_line_review: off
  first_line_rounds: 1
  adversarial_rounds: 1
  review_stop_fraction: 0.1
  levels:
    l3: { effort: low, focus: repo }
`
	c, res, ps := Resolve([]byte(repo), l)
	if len(ps) > 0 {
		t.Fatalf("a repository file looser than the project: %s", problemsText(ps))
	}
	if c.Agent.ReviewRounds != 8 || c.Agent.MaxBudgetUSD != 40 || c.Agent.FirstLineReview != FirstLineOn ||
		c.Agent.FirstLineRounds != 3 || c.Agent.AdversarialRounds != 9 || c.Agent.ReviewStopFraction != 0.9 {
		t.Errorf("agent = %+v, want the project's values", c.Agent)
	}
	if l3 := c.Agent.Levels.L3; l3 == nil || l3.Effort != "max" || l3.Focus != "project\n\nrepo" {
		t.Errorf("agent.levels.l3 = %+v, want the project's effort and both focus texts", l3)
	}
	want := []policy.Ignored{
		{Key: "agent.adversarial_rounds", Value: "1", Effective: "9", Source: SourceProject},
		{Key: "agent.first_line_review", Value: "off", Effective: "on", Source: SourceProject},
		{Key: "agent.first_line_rounds", Value: "1", Effective: "3", Source: SourceProject},
		{Key: "agent.levels.l3.effort", Value: "low", Effective: "max", Source: SourceProject},
		{Key: "agent.max_budget_usd", Value: "100", Effective: "40", Source: SourceProject},
		{Key: "agent.review_rounds", Value: "1", Effective: "8", Source: SourceProject},
		{Key: "agent.review_stop_fraction", Value: "0.1", Effective: "0.9", Source: SourceProject},
	}
	if !slices.Equal(res.Ignored, want) {
		t.Errorf("ignored = %+v, want %+v", res.Ignored, want)
	}
}
