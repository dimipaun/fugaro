package lift

import (
	"slices"
	"strings"
	"testing"
)

// planTighten is B4's planner over one fixture.
func planTighten(t *testing.T, p Project, repos []Repo) Change {
	t.Helper()
	got, err := PlanTighten(p, repos)
	if err != nil {
		t.Fatalf("PlanTighten: %v", err)
	}
	return got
}

// tightenNamesNothing is RT3's own promise (C2): the item that only names
// writes nothing at all — no layer edit, no store object, no database node and
// no migration record — so a tightened key stays in the repository's own file
// and the fix is the person's.
func tightenNamesNothing(t *testing.T, c Change) {
	t.Helper()
	if len(c.LayerEdits) != 0 || len(c.StoreWrites) != 0 || len(c.DBWrites) != 0 || len(c.Records) != 0 {
		t.Errorf("the tighten item writes something: %+v", c)
	}
}

// TestPlanTightenNamesTheBudgetCap is RT3's own test. The change writes
// nothing: it names, one line per repository, every value B4's rule drops —
// above all a repository's agent.max_budget_usd above the baseline its runs get
// where the project says nothing, config.DefaultMaxBudgetUSD (R20, spec §5.2),
// because those runs now stop earlier than its own file asks.
func TestPlanTightenNamesTheBudgetCap(t *testing.T) {
	t.Run("a silent project leaves the baseline, so a larger figure stops earlier", func(t *testing.T) {
		got := planTighten(t, Project{Layer: layerOf(t, "version: 1\n")}, []Repo{
			repoAt(t, "acme/app", "agent:\n  max_budget_usd: 50\n"),
			repoAt(t, "acme/web", "agent:\n  max_budget_usd: 10\n"),
			repoAt(t, "acme/api", "agent:\n  models:\n    coder: claude-sonnet-5\n"),
		})
		checkChange(t, got, nil, []string{
			"acme/app: agent.max_budget_usd 50 dropped; runs stop at $25; fix: `fugaro settings set agent.max_budget_usd 50 --layer project`",
		})
		tightenNamesNothing(t, got)
	})

	t.Run("the project's own figure is named where it stands", func(t *testing.T) {
		p := Project{Layer: layerOf(t, "version: 1\ndefaults:\n  agent:\n    max_budget_usd: 10\n")}
		got := planTighten(t, p, []Repo{
			repoAt(t, "acme/app", "agent:\n  max_budget_usd: 50\n"),
			repoAt(t, "acme/web", "agent:\n  max_budget_usd: 5\n"),
		})
		checkChange(t, got, nil, []string{
			"acme/app: agent.max_budget_usd 50 dropped; the project's 10 stands",
		})
		tightenNamesNothing(t, got)
	})

	// B4 migrates nothing, so the record says nothing about these keys: a
	// repository already migrated still writes the value its runs no longer
	// get, and every update names it (contract-migrate.md: the one thing a
	// planner still does for a migrated repository is name a value its file
	// still writes).
	t.Run("a repository already migrated is still named", func(t *testing.T) {
		p := Project{
			Layer:    layerOf(t, "version: 1\n"),
			Migrated: map[string]bool{slugOf(t, "github", "acme/app"): true},
		}
		got := planTighten(t, p, []Repo{repoAt(t, "acme/app", "agent:\n  max_budget_usd: 50\n")})
		checkChange(t, got, nil, []string{
			"acme/app: agent.max_budget_usd 50 dropped; runs stop at $25; fix: `fugaro settings set agent.max_budget_usd 50 --layer project`",
		})
		tightenNamesNothing(t, got)
	})

	t.Run("a file that writes no budget is not named", func(t *testing.T) {
		got := planTighten(t, Project{Layer: layerOf(t, "version: 1\n")}, []Repo{
			repoAt(t, "acme/app", "agent:\n  review_rounds: 4\n"),
			repoAt(t, "acme/tools", ""),
			{Repo: "acme/gone", Provider: "github", Unreadable: "the repository is not there"},
		})
		checkChange(t, got, nil, nil)
		tightenNamesNothing(t, got)
	})
}

// TestPlanTightenNamesTheFix: the line is what a person acts on, so it names
// the value, what a run now does, and the one command that sets the project's
// own value — and it is one line under the bound CLAUDE.md gives a message.
func TestPlanTightenNamesTheFix(t *testing.T) {
	got := planTighten(t, Project{Layer: layerOf(t, "version: 1\n")}, []Repo{
		repoAt(t, "acme/app", "agent:\n  max_budget_usd: 500\n"),
	})
	if len(got.Lines) != 1 {
		t.Fatalf("Lines = %q, want one", got.Lines)
	}
	line := got.Lines[0]
	for _, want := range []string{
		"agent.max_budget_usd 500 dropped",
		"runs stop at $25",
		"`fugaro settings set agent.max_budget_usd 500 --layer project`",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the line does not name %q: %s", want, line)
		}
	}
	if n := len([]rune(line)); n > 140 {
		t.Errorf("the line is %d characters, over the 140 a message gets: %s", n, line)
	}
}

// TestPlanTightenNamesEveryValueTheProjectDrops: what RT3 names is what RT1
// drops (the plan's Depends line). Where the project sets the key, the
// repository's value is named for every key of spec §5.2's table that can be
// dropped, and a value that tightens the project's is not.
func TestPlanTightenNamesEveryValueTheProjectDrops(t *testing.T) {
	t.Run("rounds", func(t *testing.T) {
		p := Project{Layer: layerOf(t, "version: 1\ndefaults:\n  agent:\n    review_rounds: 2\n    first_line_rounds: 3\n")}
		got := planTighten(t, p, []Repo{
			repoAt(t, "acme/app", "agent:\n  review_rounds: 1\n  first_line_rounds: 3\n"),
		})
		checkChange(t, got, nil, []string{
			"acme/app: agent.review_rounds 1 dropped; the project's 2 stands",
		})
	})

	t.Run("the first-line review's rank", func(t *testing.T) {
		p := Project{Layer: layerOf(t, "version: 1\ndefaults:\n  agent:\n    first_line_review: on\n")}
		got := planTighten(t, p, []Repo{
			repoAt(t, "acme/app", "agent:\n  first_line_review: off\n"),
			repoAt(t, "acme/web", "agent:\n  first_line_review: on\n"),
		})
		checkChange(t, got, nil, []string{
			"acme/app: agent.first_line_review off dropped; the project's on stands",
		})
	})

	t.Run("a level's effort follows config.Efforts", func(t *testing.T) {
		p := Project{Layer: layerOf(t, "version: 1\ndefaults:\n  agent:\n    levels:\n      l1:\n        effort: high\n")}
		got := planTighten(t, p, []Repo{
			repoAt(t, "acme/app", "agent:\n  levels:\n    l1:\n      effort: low\n"),
		})
		checkChange(t, got, nil, []string{
			"acme/app: agent.levels.l1.effort low dropped; the project's high stands",
		})
	})

	t.Run("a level the project runs", func(t *testing.T) {
		p := Project{Layer: layerOf(t, "version: 1\ndefaults:\n  agent:\n    levels:\n      l3:\n        effort: high\n")}
		got := planTighten(t, p, []Repo{
			repoAt(t, "acme/app", "agent:\n  levels:\n    l3:\n      none: true\n"),
		})
		checkChange(t, got, nil, []string{
			"acme/app: agent.levels.l3.none true dropped; the project runs l3",
		})
	})

	t.Run("a level the project turns off too is not dropped", func(t *testing.T) {
		p := Project{Layer: layerOf(t, "version: 1\ndefaults:\n  agent:\n    levels:\n      l3:\n        none: true\n")}
		got := planTighten(t, p, []Repo{
			repoAt(t, "acme/app", "agent:\n  levels:\n    l3:\n      none: true\n"),
		})
		checkChange(t, got, nil, nil)
	})

	t.Run("a value that tightens the project's is not named", func(t *testing.T) {
		p := Project{Layer: layerOf(t, "version: 1\ndefaults:\n  agent:\n    review_rounds: 2\n    review_stop_fraction: 0.5\n")}
		got := planTighten(t, p, []Repo{
			repoAt(t, "acme/app", "agent:\n  review_rounds: 6\n  review_stop_fraction: 0.9\n"),
		})
		checkChange(t, got, nil, nil)
	})
}

// TestPlanTightenLeavesTheRunsModeAlone: a key whose baseline is the run's
// mode — a level's switch and the round counts — is RT2's, and update names
// none of them: a silent project leaves the repository's value unchecked, and
// the run takes the tighter of it and the mode's own row (R20, R3).
func TestPlanTightenLeavesTheRunsModeAlone(t *testing.T) {
	got := planTighten(t, Project{Layer: layerOf(t, "version: 1\n")}, []Repo{
		repoAt(t, "acme/app", "agent:\n  review_rounds: 1\n  levels:\n    l3:\n      none: true\n"),
	})
	checkChange(t, got, nil, nil)
	tightenNamesNothing(t, got)
}

// TestPlanTightenNamesTheStopFraction: the other key R20 hands to Fugaro's own
// defaults rather than to a mode row (config.DefaultReviewStopFraction): a
// repository that drops out of its loop earlier than 0.8 has its runs review
// longer than its file asks, so it is named too.
func TestPlanTightenNamesTheStopFraction(t *testing.T) {
	p := Project{Layer: layerOf(t, "version: 1\n")}
	got := planTighten(t, p, []Repo{
		repoAt(t, "acme/app", "agent:\n  review_stop_fraction: 0.5\n"),
		repoAt(t, "acme/web", "agent:\n  review_stop_fraction: 0.9\n"),
		repoAt(t, "acme/tools", "agent:\n  review_stop_fraction: 0\n"),
	})
	checkChange(t, got, nil, []string{
		"acme/app: agent.review_stop_fraction 0.5 dropped; the project sets none, so the run reviews to 0.8 of its budget",
	})
	tightenNamesNothing(t, got)
}

// TestPlanTightenJudgesEveryTightenKey pins spec §5.2's table as this item
// reads it: the keys a drop can be named for, and the ones it leaves to the
// rule that owns them — focus is additive, agent.levels.lN.model is a lifted
// key (D3291), and agent.recipe and agent.workspace_beat are replaced whole
// (D3292, D3293), so none of the four is ever dropped.
func TestPlanTightenJudgesEveryTightenKey(t *testing.T) {
	want := []string{
		"agent.adversarial_rounds", "agent.first_line_review", "agent.first_line_rounds",
		"agent.levels.l1.effort", "agent.levels.l1.none",
		"agent.levels.l2.effort", "agent.levels.l2.none",
		"agent.levels.l3.effort", "agent.levels.l3.none",
		"agent.max_budget_usd", "agent.review_rounds", "agent.review_stop_fraction",
	}
	var got []string
	for _, key := range want {
		if tightenKindOf(key) == tightenReplace {
			t.Errorf("%s is a tighten-only key of spec §5.2 and this item does not judge it", key)
		}
		got = append(got, key)
	}
	if !slices.Equal(got, want) {
		t.Errorf("judged keys = %q, want %q", got, want)
	}
	for _, kept := range []string{
		"agent.levels.l1.focus", "agent.levels.l1.model", "agent.recipe", "agent.workspace_beat",
		"agent.model", "ab.rate", "rigor.default",
	} {
		if tightenKindOf(kept) != tightenReplace {
			t.Errorf("%s is not a key this item drops, and it judges it", kept)
		}
	}
}

// TestPlanTightenSkipsWhatItCannotRead: a repository whose file is not YAML,
// one whose provider and name are not a repository this can key, and one the
// reader could not read at all are named by no line of this item — there is no
// drop to name for them, and M2 names the repository itself.
func TestPlanTightenSkipsWhatItCannotRead(t *testing.T) {
	got := planTighten(t, Project{Layer: layerOf(t, "version: 1\n")}, []Repo{
		repoAt(t, "acme/app", "agent: [not a mapping\n"),
		repoAt(t, "acme", "agent:\n  max_budget_usd: 50\n"),
		{Repo: "acme/gone", Provider: "github", Unreadable: "not there"},
	})
	checkChange(t, got, nil, nil)
	tightenNamesNothing(t, got)
}

// TestPlanTightenLinesDoNotMove: two runs of update over the same project print
// the same lines in the same order, so a person reading two updates of one
// project reads them the same way (C2).
func TestPlanTightenLinesDoNotMove(t *testing.T) {
	p := Project{Layer: layerOf(t, "version: 1\ndefaults:\n  agent:\n    review_rounds: 4\n")}
	repos := []Repo{
		repoAt(t, "acme/web", "agent:\n  review_rounds: 1\n  max_budget_usd: 90\n"),
		repoAt(t, "acme/app", "agent:\n  review_rounds: 2\n  max_budget_usd: 50\n"),
	}
	first, second := planTighten(t, p, repos), planTighten(t, p, repos)
	if !slices.Equal(first.Lines, second.Lines) {
		t.Errorf("two runs printed %q and %q", first.Lines, second.Lines)
	}
	if len(first.Lines) != 4 {
		t.Fatalf("Lines = %q, want four drops", first.Lines)
	}
	for i, want := range []string{"acme/app: agent.max_budget_usd", "acme/app: agent.review_rounds",
		"acme/web: agent.max_budget_usd", "acme/web: agent.review_rounds"} {
		if !strings.HasPrefix(first.Lines[i], want) {
			t.Errorf("Lines[%d] = %q, want %s", i, first.Lines[i], want)
		}
	}
}
