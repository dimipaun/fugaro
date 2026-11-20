package recipe

import (
	"strings"
	"testing"
)

// byLevelText is the shape every test below varies: a recipe whose own
// roles: gives the reviewer the coder's cheap model, and whose by_level:
// takes that back at two levels.
const byLevelText = `version: 1
name: tiered
roles:
  reviewer: coder
by_level:
  poc:
    roles: { coder: reviewer }
  critical:
    roles: {}
steps:
  - first_line: {}
  - review: {}
`

// TestByLevelRolesResolvePerLevel: the level's own mapping is what
// RolesFor returns at a level the recipe named, and the recipe's own roles:
// at every level it did not — a level named with roles: {} included, which
// is how a recipe says the level runs with no mapping (LD9: the level is
// available to a recipe, and roles is the whole of what it may vary).
func TestByLevelRolesResolvePerLevel(t *testing.T) {
	r := mustParse(t, byLevelText)
	for _, tc := range []struct {
		level      string
		revIsCoder bool
		coderIsRev bool
		why        string
	}{
		{"poc", false, true, "the level's own entry"},
		{"critical", false, false, "roles: {} — no mapping at this level, and not the recipe's own either"},
		{"startup", true, false, "a level the block does not name keeps the recipe's own roles:"},
		{"pro", true, false, "a level the block does not name keeps the recipe's own roles:"},
		{"hotfix", true, false, "a level the block does not name keeps the recipe's own roles:"},
		{"not-a-level", true, false, "a name the format refuses cannot come from a recipe, and takes the recipe's own"},
	} {
		m := r.RolesFor(tc.level)
		if m.ReviewerIsCoder != tc.revIsCoder || m.CoderIsReviewer != tc.coderIsRev {
			t.Errorf("RolesFor(%q) = %+v, want reviewerIsCoder=%v coderIsReviewer=%v: %s",
				tc.level, m, tc.revIsCoder, tc.coderIsRev, tc.why)
		}
	}
	if r.ReviewerIsCoder != true || r.CoderIsReviewer {
		t.Errorf("the by_level: block changed the recipe's own roles: %+v", r)
	}
	// A recipe with no by_level: block at all resolves to its own roles: at
	// every level, which is every recipe written before the key existed.
	plain := mustParse(t, "version: 1\nname: plain\nroles: { coder: reviewer }\nsteps:\n  - review: {}\n")
	for _, level := range LevelNames {
		if m := plain.RolesFor(level); m.CoderIsReviewer != true || m.ReviewerIsCoder {
			t.Errorf("RolesFor(%q) on a recipe with no by_level: = %+v, want the recipe's own roles:", level, m)
		}
	}
}

// TestByLevelRefusesAnUnknownLevel: a level name the vocabulary does not
// know is refused rather than dropped. A dropped entry would be a recipe
// that silently does nothing at the level its author meant, and the
// message names every level there is, so the fix is in the refusal.
func TestByLevelRefusesAnUnknownLevel(t *testing.T) {
	for _, tc := range []struct{ level, want string }{
		{"fast", "must be a rigor level"},
		{"POC", "must be a rigor level"},
		{"critical ", "must be a rigor level"},
		{"", "must be a rigor level"},
	} {
		_, ps := Parse([]byte("version: 1\nname: a\nby_level:\n  \"" + tc.level + "\":\n    roles: { reviewer: coder }\nsteps:\n  - review: {}\n"))
		if !problemsHave(ps, "by_level."+tc.level, tc.want) {
			t.Errorf("by_level level %q: problems %v lack %q under by_level.%s", tc.level, ps, tc.want, tc.level)
		}
	}
	text := ProblemsText(mustProblems(t, "version: 1\nname: a\nby_level:\n  fast:\n    roles: { reviewer: coder }\nsteps:\n  - review: {}\n"))
	for _, level := range LevelNames {
		if !strings.Contains(text, level) {
			t.Errorf("the refusal does not name the level %q: %s", level, text)
		}
	}
}

// TestByLevelVariesRolesOnly: the cap a run gets is its level's own
// (reconciliation LD9, plan T6.1, D16) and the loop's shape is the
// recipe's, so a by_level: entry holding anything but roles is refused by
// name — a budget word with a message that says where the cap really comes
// from, and a model key with the model message it gets everywhere.
func TestByLevelVariesRolesOnly(t *testing.T) {
	for _, tc := range []struct{ key, want string }{
		{"budget", byLevelOnlyRoles},
		{"max_cost", byLevelOnlyRoles},
		{"max_budget_usd", byLevelOnlyRoles},
		{"cap", byLevelOnlyRoles},
		{"cost_cap", byLevelOnlyRoles},
		{"steps", byLevelOnlyRoles},
		{"check", byLevelOnlyRoles},
		{"max_rounds", byLevelOnlyRoles},
		{"mode", byLevelOnlyRoles},
		{"model", modelMsg},
		{"models", modelMsg},
	} {
		_, ps := Parse([]byte("version: 1\nname: a\nby_level:\n  pro:\n    roles: { reviewer: coder }\n    " + tc.key + ": 1\nsteps:\n  - review: {}\n"))
		if !problemsHave(ps, "by_level.pro."+tc.key, tc.want) {
			t.Errorf("by_level.pro.%s: problems %v lack %q", tc.key, ps, tc.want)
		}
	}
	// The recipe is refused, not repaired: nothing from the entry is kept.
	r, ps := Parse([]byte("version: 1\nname: a\nby_level:\n  pro:\n    budget: 5\nsteps:\n  - review: {}\n"))
	if r != nil || len(ps) == 0 {
		t.Fatalf("a by_level: entry holding a budget = %+v, %v; want a refusal", r, ps)
	}
	// A budget beside roles is refused as well: the entry is not read for
	// its legal keys while an illegal one sits in it.
	if !problemsHave(mustProblems(t, "version: 1\nname: a\nby_level:\n  pro:\n    roles: { reviewer: coder }\n    budget: 5\nsteps:\n  - review: {}\n"),
		"by_level.pro.budget", byLevelOnlyRoles) {
		t.Error("a budget beside roles is not refused")
	}
	if _, ps := Parse([]byte("version: 1\nname: a\nby_level:\n  pro: {}\nsteps:\n  - review: {}\n")); !problemsHave(ps, "by_level.pro", "must set roles") {
		t.Errorf("an empty by_level: entry: problems %v lack the roles requirement", ps)
	}
	if _, ps := Parse([]byte("version: 1\nname: a\nby_level:\n  pro: reviewer\nsteps:\n  - review: {}\n")); !problemsHave(ps, "by_level.pro", "must be a mapping holding roles") {
		t.Errorf("a by_level: entry that is not a mapping: problems %v", ps)
	}
	if _, ps := Parse([]byte("version: 1\nname: a\nby_level: [poc]\nsteps:\n  - review: {}\n")); !problemsHave(ps, "by_level", "must be a mapping") {
		t.Errorf("a by_level: that is not a mapping: problems %v", ps)
	}
}

// TestByLevelRolesAreCheckedLikeTheTopLevel: the roles: block inside an
// entry is the top-level block's own reader (parser.roleMap), so it refuses
// the same three things, under the entry's own path — a mapping the author
// can find.
func TestByLevelRolesAreCheckedLikeTheTopLevel(t *testing.T) {
	for _, tc := range []struct{ roles, path, want string }{
		{"{ reviewer: coder, coder: reviewer }", "by_level.pro.roles", "exclude each other"},
		{"{ reviewer: reviewer }", "by_level.pro.roles.reviewer", "must be coder"},
		{"{ reviewer: claude-opus-5-5 }", "by_level.pro.roles.reviewer", modelMsg},
		{"{ background: coder }", "by_level.pro.roles.background", "only the reviewer and coder roles can be mapped"},
		{"coder", "by_level.pro.roles", "must be a mapping"},
	} {
		_, ps := Parse([]byte("version: 1\nname: a\nby_level:\n  pro:\n    roles: " + tc.roles + "\nsteps:\n  - review: {}\n"))
		if !problemsHave(ps, tc.path, tc.want) {
			t.Errorf("by_level roles %s: problems %v lack %q under %s", tc.roles, ps, tc.want, tc.path)
		}
	}
}

// TestByLevelIsRefusedWhereRolesIs: the modes with no coder or reviewer
// stage refuse the block by name. Removing roles: would not fix a recipe
// that wrote by_level:, so it gets a message of its own.
func TestByLevelIsRefusedWhereRolesIs(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"version: 1\nname: a\nmode: review\nby_level:\n  pro:\n    roles: { reviewer: coder }\nsteps:\n  - review: {}\n",
			"mode: review has no coder step to map roles onto: remove by_level"},
		{"version: 1\nname: a\nmode: evaluate\nby_level:\n  pro:\n    roles: { reviewer: coder }\nsteps:\n  - merge_judge: {}\n",
			"mode: evaluate has no agent steps to map roles onto: remove by_level"},
		{"version: 1\nname: a\nmode: artifact\noutputs: { globs: [\"out/*.png\"] }\nby_level:\n  pro:\n    roles: { reviewer: coder }\nsteps:\n  - render: {}\n",
			"mode: artifact has no agent steps to map roles onto: remove by_level"},
	} {
		_, ps := Parse([]byte(tc.text))
		if !problemsHave(ps, "by_level", tc.want) {
			t.Errorf("problems %v lack %q under by_level", ps, tc.want)
		}
	}
}

// TestUsesLevelRolesDecidesByPresence: the launch gate asks whether the job
// image's runner knows the key, and a parser that predates it refuses the
// whole recipe — so the answer is the key being written, never what its
// entries hold. by_level: {} is the case that separates the two.
func TestUsesLevelRolesDecidesByPresence(t *testing.T) {
	if r := mustParse(t, byLevelText); !UsesLevelRoles(r) {
		t.Error("a recipe with a by_level: block does not use level roles")
	}
	empty := mustParse(t, "version: 1\nname: a\nby_level: {}\nsteps:\n  - review: {}\n")
	if !UsesLevelRoles(empty) {
		t.Error("by_level: {} does not use level roles: a parser older than the key refuses it, so the image gate must see it")
	}
	if r := mustParse(t, "version: 1\nname: a\nroles: { reviewer: coder }\nsteps:\n  - review: {}\n"); UsesLevelRoles(r) {
		t.Error("a recipe with no by_level: uses level roles")
	}
}

// TestByLevelIsNotTheV07Flag: by_level: is 0.27.0's construct and newKeys is
// the 0.7.0 gate — the release every current job image already satisfies.
// Setting it here would send a by_level: launch through a gate that says
// nothing about the key, and the run would die at the bootstrap parse
// instead of being refused at the terminal; the gate it needs is its own,
// newer one (internal/cli/recipes_skew.go, levelRolesSince).
func TestByLevelIsNotTheV07Flag(t *testing.T) {
	if r := mustParse(t, byLevelText); r.newKeys || r.newKeysV08 {
		t.Error("a by_level: recipe carries an older release's gate: it needs its own")
	}
}

// mustProblems is the problem list Parse returns for text that must be
// refused, for the tests that assert on the message as a whole.
func mustProblems(t *testing.T, text string) []Problem {
	t.Helper()
	r, ps := Parse([]byte(text))
	if r != nil {
		t.Fatalf("recipe %+v parsed; want a refusal", r)
	}
	if len(ps) == 0 {
		t.Fatal("no problems")
	}
	return ps
}
