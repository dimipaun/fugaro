package lift

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// TestPlanCaps is RC4's own test: B18's item lowers every repository cap node
// that stands above the project's ceiling to that ceiling — the global cap
// where it is set, the default repository cap otherwise (RC1, spec §5.6) —
// names each node it lowers, and leaves a node a run already meets exactly
// where it stands. A node of a repository this run did not read, or of one it
// has already migrated, is named and never written (D3305, D3306).
func TestPlanCaps(t *testing.T) {
	t.Run("lowered to the default repository cap", func(t *testing.T) {
		slug := slugOf(t, "github", "acme/app")
		got := planCaps(t, Project{Caps: Caps{
			Defaults: &budget.DefaultCaps{RepoDailyMicros: capMicros(20_000_000), RepoPerRunMicros: capMicros(10_000_000)},
			Repos:    map[string]budget.RepoCaps{slug: capNode("acme/app", 100_000_000, 50_000_000)},
		}}, repoAt(t, "acme/app", ""))
		wantCaps(t, got, slug, budget.RepoCaps{
			DailyMicros: capMicros(20_000_000), PerRunMicros: capMicros(10_000_000), Repo: "acme/app"})
		checkChange(t, got, nil, []string{
			"acme/app: config/caps/repos/" + slug + " daily $100.00 -> $20.00 (the default repository cap; a run in flight meets it at its next lease)",
			"acme/app: config/caps/repos/" + slug + " per-run $50.00 -> $10.00 (the default repository cap; a run in flight meets it at its next lease)",
		})
		if want := []string{slug}; !slices.Equal(got.Records, want) {
			t.Errorf("Records = %v, want %v: a repository whose cap this item lowered is migrated once (D3305)", got.Records, want)
		}
	})

	t.Run("lowered to the global cap", func(t *testing.T) {
		// With a global cap set, the lowering changes nothing a run meets: a
		// lease already compares against the global figure (spec §5.6), and
		// the line says so.
		slug := slugOf(t, "github", "acme/app")
		got := planCaps(t, Project{Caps: Caps{
			Global:   &budget.GlobalCaps{DailyMicros: capMicros(25_000_000), PerRunMicros: capMicros(5_000_000)},
			Defaults: &budget.DefaultCaps{RepoDailyMicros: capMicros(20_000_000), RepoPerRunMicros: capMicros(10_000_000)},
			Repos:    map[string]budget.RepoCaps{slug: capNode("acme/app", 100_000_000, 50_000_000)},
		}}, repoAt(t, "acme/app", ""))
		wantCaps(t, got, slug, budget.RepoCaps{
			DailyMicros: capMicros(25_000_000), PerRunMicros: capMicros(5_000_000), Repo: "acme/app"})
		checkChange(t, got, nil, []string{
			"acme/app: config/caps/repos/" + slug + " daily $100.00 -> $25.00 (the global cap; no run's limit changes)",
			"acme/app: config/caps/repos/" + slug + " per-run $50.00 -> $5.00 (the global cap; no run's limit changes)",
		})
	})

	t.Run("one dimension from the global and one from the default", func(t *testing.T) {
		// The ceiling falls back on its own per dimension (RC1): a global
		// per-run cap with no global daily cap leaves the daily dimension to
		// the default, and each line names the figure that decided it.
		slug := slugOf(t, "github", "acme/app")
		got := planCaps(t, Project{Caps: Caps{
			Global:   &budget.GlobalCaps{PerRunMicros: capMicros(5_000_000)},
			Defaults: &budget.DefaultCaps{RepoDailyMicros: capMicros(80_000_000)},
			Repos:    map[string]budget.RepoCaps{slug: capNode("acme/app", 100_000_000, 50_000_000)},
		}}, repoAt(t, "acme/app", ""))
		wantCaps(t, got, slug, budget.RepoCaps{
			DailyMicros: capMicros(80_000_000), PerRunMicros: capMicros(5_000_000), Repo: "acme/app"})
		checkChange(t, got, nil, []string{
			"acme/app: config/caps/repos/" + slug + " daily $100.00 -> $80.00 (the default repository cap; a run in flight meets it at its next lease)",
			"acme/app: config/caps/repos/" + slug + " per-run $50.00 -> $5.00 (the global cap; no run's limit changes)",
		})
	})

	t.Run("one dimension above and one below", func(t *testing.T) {
		// Only the dimension that stands above its ceiling moves: the other
		// half of the node is the repository's own figure and is not the
		// default's to replace.
		slug := slugOf(t, "github", "acme/app")
		got := planCaps(t, Project{Caps: Caps{
			Defaults: &budget.DefaultCaps{RepoDailyMicros: capMicros(20_000_000), RepoPerRunMicros: capMicros(10_000_000)},
			Repos:    map[string]budget.RepoCaps{slug: capNode("acme/app", 15_000_000, 50_000_000)},
		}}, repoAt(t, "acme/app", ""))
		wantCaps(t, got, slug, budget.RepoCaps{
			DailyMicros: capMicros(15_000_000), PerRunMicros: capMicros(10_000_000), Repo: "acme/app"})
		checkChange(t, got, nil, []string{
			"acme/app: config/caps/repos/" + slug + " per-run $50.00 -> $10.00 (the default repository cap; a run in flight meets it at its next lease)",
		})
	})

	t.Run("a node under its ceiling stands", func(t *testing.T) {
		slug := slugOf(t, "github", "acme/app")
		got := planCaps(t, Project{Caps: Caps{
			Global:   &budget.GlobalCaps{DailyMicros: capMicros(100_000_000), PerRunMicros: capMicros(50_000_000)},
			Defaults: &budget.DefaultCaps{RepoDailyMicros: capMicros(20_000_000)},
			Repos:    map[string]budget.RepoCaps{slug: capNode("acme/app", 10_000_000, 5_000_000)},
		}}, repoAt(t, "acme/app", ""))
		if !got.Empty() {
			t.Errorf("a node under its ceiling changed something: %+v", got)
		}
	})

	t.Run("no ceiling leaves the node standing", func(t *testing.T) {
		// Neither a global nor a default cap: there is nothing above the node
		// to lower it to (spec §5.6).
		slug := slugOf(t, "github", "acme/app")
		got := planCaps(t, Project{Caps: Caps{
			Repos: map[string]budget.RepoCaps{slug: capNode("acme/app", 100_000_000, 50_000_000)},
		}}, repoAt(t, "acme/app", ""))
		if !got.Empty() {
			t.Errorf("a project with no ceiling changed something: %+v", got)
		}
	})

	t.Run("a repository already migrated is named and not written", func(t *testing.T) {
		// D3305: the repository's migration is behind it, so its node is named
		// and left where it stands (spec §5.6's clamp is what holds it, and no
		// second update writes the node again).
		slug := slugOf(t, "github", "acme/app")
		got := planCaps(t, Project{
			Caps: Caps{
				Global: &budget.GlobalCaps{PerRunMicros: capMicros(5_000_000)},
				Repos:  map[string]budget.RepoCaps{slug: capNode("acme/app", 0, 50_000_000)},
			},
			Migrated: map[string]bool{slug: true},
		}, repoAt(t, "acme/app", ""))
		checkChange(t, got, nil, []string{
			"acme/app: config/caps/repos/" + slug + " per-run $50.00 stays above the ceiling $5.00; the repository is already migrated",
		})
		checkNoCapWrite(t, got)
	})

	t.Run("a repository this run did not read is left alone", func(t *testing.T) {
		// D3306: no planner acts on a repository update could not read — and
		// recording one as migrated would keep its file's values from ever
		// being moved by the update that can read it.
		slug := slugOf(t, "github", "acme/app")
		got := planCaps(t, Project{Caps: Caps{
			Global: &budget.GlobalCaps{PerRunMicros: capMicros(5_000_000)},
			Repos:  map[string]budget.RepoCaps{slug: capNode("acme/app", 0, 50_000_000)},
		}}, Repo{Repo: "acme/app", Provider: "github", Unreadable: "the App ID is not set"})
		checkChange(t, got, nil, []string{
			"acme/app: config/caps/repos/" + slug + " per-run $50.00 stays above the ceiling $5.00; this run did not read the repository",
		})
		checkNoCapWrite(t, got)
	})

	t.Run("a repository this run cannot list is left alone", func(t *testing.T) {
		// The node's own repository is not among the repositories this run
		// read at all: the same answer, with the name the node carries.
		slug := slugOf(t, "github", "acme/stale")
		got := planCaps(t, Project{Caps: Caps{
			Global: &budget.GlobalCaps{PerRunMicros: capMicros(5_000_000)},
			Repos:  map[string]budget.RepoCaps{slug: capNode("acme/stale", 0, 50_000_000)},
		}}, repoAt(t, "acme/app", ""))
		checkChange(t, got, nil, []string{
			"acme/stale: config/caps/repos/" + slug + " per-run $50.00 stays above the ceiling $5.00; this run did not read the repository",
		})
		checkNoCapWrite(t, got)
	})

	t.Run("two repositories are ordered by their node's key", func(t *testing.T) {
		first, second := slugOf(t, "github", "acme/app"), slugOf(t, "github", "acme/web")
		got := planCaps(t, Project{Caps: Caps{
			Defaults: &budget.DefaultCaps{RepoPerRunMicros: capMicros(10_000_000)},
			Repos: map[string]budget.RepoCaps{
				second: capNode("acme/web", 0, 50_000_000),
				first:  capNode("acme/app", 0, 40_000_000),
			},
		}}, repoAt(t, "acme/app", ""), repoAt(t, "acme/web", ""))
		if want := []string{budget.PathCapsRepo(first), budget.PathCapsRepo(second)}; !slices.Equal(capPaths(got), want) {
			t.Errorf("the writes are %v, want %v: the lines and the writes do not move between two runs", capPaths(got), want)
		}
		if want := []string{first, second}; !slices.Equal(got.Records, want) {
			t.Errorf("Records = %v, want %v", got.Records, want)
		}
	})
}

// planCaps runs RC4 over a fixture and holds it to the two things this item
// never does: answer an error, and write anything but the budget database (a
// bug's writes are the layer file's and the store's, which B18 does not touch).
func planCaps(t *testing.T, p Project, repos ...Repo) Change {
	t.Helper()
	c, err := PlanCaps(p, repos)
	if err != nil {
		t.Fatalf("PlanCaps: %v", err)
	}
	if len(c.LayerEdits) != 0 {
		t.Errorf("RC4 writes the layer file: %+v", c.LayerEdits)
	}
	if len(c.StoreWrites) != 0 {
		t.Errorf("RC4 writes the store: %+v", c.StoreWrites)
	}
	return c
}

// wantCaps holds the change to the one write it makes for a node, and to the
// value it lowers it to.
func wantCaps(t *testing.T, c Change, slug string, want budget.RepoCaps) {
	t.Helper()
	if len(c.DBWrites) != 1 {
		t.Fatalf("DBWrites = %+v, want the one lowered cap node", c.DBWrites)
	}
	got := c.DBWrites[0]
	if got.Path != budget.PathCapsRepo(slug) {
		t.Errorf("the write is at %s, want %s", got.Path, budget.PathCapsRepo(slug))
	}
	node, ok := got.Value.(budget.RepoCaps)
	if !ok {
		t.Fatalf("the write's value is %T, want a repository cap node", got.Value)
	}
	if !sameCapNode(node, want) {
		t.Errorf("the node written is %+v, want %+v", node, want)
	}
}

// checkNoCapWrite holds a change to leaving the node where it stands: a
// repository this run did not read or has already migrated is named and never
// written, and never recorded as migrated (D3305, D3306).
func checkNoCapWrite(t *testing.T, c Change) {
	t.Helper()
	if len(c.DBWrites) != 0 {
		t.Errorf("the node was written: %+v", c.DBWrites)
	}
	if len(c.Records) != 0 {
		t.Errorf("Records = %v, want none: this item wrote nothing for it", c.Records)
	}
}

func capPaths(c Change) []string {
	out := make([]string, 0, len(c.DBWrites))
	for _, w := range c.DBWrites {
		out = append(out, w.Path)
	}
	return out
}

// sameCapNode compares two cap nodes field by field: a cap that is not set is
// nil and never zero, so the pointers cannot be compared as values.
func sameCapNode(a, b budget.RepoCaps) bool {
	return sameMicros(a.DailyMicros, b.DailyMicros) && sameMicros(a.PerRunMicros, b.PerRunMicros) && a.Repo == b.Repo
}

func sameMicros(a, b *budget.Micros) bool {
	switch {
	case a == nil || b == nil:
		return a == nil && b == nil
	default:
		return *a == *b
	}
}

// capNode is one repository's cap node as the database holds it: the two
// figures and the repository the node names. A zero figure is written as an
// unset dimension, which is what the node carries when nobody set it.
func capNode(repo string, daily, perRun int64) budget.RepoCaps {
	node := budget.RepoCaps{Repo: repo}
	if daily != 0 {
		node.DailyMicros = capMicros(daily)
	}
	if perRun != 0 {
		node.PerRunMicros = capMicros(perRun)
	}
	return node
}

// capMicros is one cap figure as the database carries it: a pointer, because a
// cap that is not set is nil and never zero.
func capMicros(v int64) *budget.Micros {
	m := budget.Micros(v)
	return &m
}
