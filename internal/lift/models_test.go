package lift

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pricing"
	"github.com/dimipaun/fugaro/internal/task"
)

// wantPorts is B3's fourteen keys — agent.model, the five role slots, the five
// crafts' models and, by D3291, the three review levels' (spec §5.1) — with the
// name update's line calls each by, spelled out as this test's own expectation
// so that it is red before the planner exists and so that a key C1 adds to the
// model move is named here rather than migrated in silence.
var wantPorts = []struct{ key, label string }{
	{"agent.crafts.coder.model", "craft coder"},
	{"agent.crafts.merge-judge.model", "craft merge-judge"},
	{"agent.crafts.review.model", "craft review"},
	{"agent.crafts.security-review.model", "craft security-review"},
	{"agent.crafts.senior-review.model", "craft senior-review"},
	{"agent.levels.l1.model", "level l1"},
	{"agent.levels.l2.model", "level l2"},
	{"agent.levels.l3.model", "level l3"},
	{"agent.model", "model"},
	{"agent.models.background", "background"},
	{"agent.models.coder", "coder"},
	{"agent.models.diagnose", "diagnose"},
	{"agent.models.reviewer", "reviewer"},
	{"agent.models.vision", "vision"},
}

// TestPlanModels holds B3's planner to the migration the owner ruled (D3237,
// D3290, D3291, R17): the model choice moves to the project layer, a project
// value already written is never replaced, a silent project takes the value
// most repositories write, and every repository whose model changes — or whose
// own allow-list refuses the value the project takes — is named before any
// write.
func TestPlanModels(t *testing.T) {
	t.Run("every B3 key is a port", func(t *testing.T) {
		if len(modelPorts) != len(wantPorts) {
			t.Fatalf("MC1 moves %d keys, want %d: %+v", len(modelPorts), len(wantPorts), modelPorts)
		}
		for i, want := range wantPorts {
			got := modelPorts[i]
			if got.key != want.key || got.label != want.label {
				t.Errorf("modelPorts[%d] = %s (%q), want %s (%q)", i, got.key, got.label, want.key, want.label)
			}
			if want := "defaults." + got.key; strings.Join(got.home, ".") != want {
				t.Errorf("%s's home is %s, want %s", got.key, strings.Join(got.home, "."), want)
			}
			if !strings.HasPrefix(got.why, got.key) {
				t.Errorf("%s carries no sentence of its own: %q", got.key, got.why)
			}
		}
	})

	t.Run("project sets", func(t *testing.T) {
		p := Project{Layer: layerOf(t, "defaults:\n  agent:\n    models:\n      coder: claude-sonnet-5-5\n")}
		app := repoAt(t, "acme/app", "agent:\n  models:\n    coder: claude-opus-5-5\n")
		web := repoAt(t, "acme/web", "")
		api := repoAt(t, "acme/api", "agent:\n  models:\n    coder: claude-sonnet-5-5\n")
		c := planModels(t, p, app, web, api)

		checkChange(t, c, nil, []string{
			"acme/app: coder claude-opus-5-5 -> claude-sonnet-5-5 (the project's)",
		})
		checkRecords(t, c, app, web, api)
	})

	t.Run("all agree", func(t *testing.T) {
		// One fixture over a craft, agent.model and a role slot: the project
		// takes what both writers write, and the repository that writes nothing
		// moves onto it.
		const same = "agent:\n  model: claude-opus-5-5\n  models:\n    coder: claude-sonnet-5-5\n  crafts:\n    coder:\n      model: claude-haiku-4-5\n"
		app := repoAt(t, "acme/app", same)
		web := repoAt(t, "acme/web", same)
		api := repoAt(t, "acme/api", "")
		c := planModels(t, Project{}, app, web, api)

		checkChange(t, c, []LayerEdit{
			edit("defaults.agent.crafts.coder.model", "claude-haiku-4-5"),
			edit("defaults.agent.model", "claude-opus-5-5"),
			edit("defaults.agent.models.coder", "claude-sonnet-5-5"),
		}, []string{
			"acme/api: craft coder Fugaro's default -> claude-haiku-4-5 (the project's)",
			"acme/api: model Fugaro's default -> claude-opus-5-5 (the project's)",
			"acme/api: coder Fugaro's default -> claude-sonnet-5-5 (the project's)",
		})
	})

	t.Run("majority", func(t *testing.T) {
		app := repoAt(t, "acme/app", "agent:\n  models:\n    coder: claude-sonnet-5-5\n")
		web := repoAt(t, "acme/web", "agent:\n  models:\n    coder: claude-sonnet-5-5\n")
		api := repoAt(t, "acme/api", "agent:\n  models:\n    coder: claude-opus-5-5\n")
		c := planModels(t, Project{}, app, web, api)

		checkChange(t, c, []LayerEdit{edit("defaults.agent.models.coder", "claude-sonnet-5-5")}, []string{
			"acme/api: coder claude-opus-5-5 -> claude-sonnet-5-5 (the project's)",
		})
	})

	t.Run("tie by price", func(t *testing.T) {
		// One repository each, and the price table is the only thing that can
		// settle it: by ID claude-opus-5-5 is the earlier name, and at $4/$20
		// against $2/$10 it is the dearer model, so the project takes
		// claude-sonnet-5 (D3290). Drop the price rule and this fixture answers
		// opus, which is what keeps the rule load-bearing here.
		app := repoAt(t, "acme/app", "agent:\n  models:\n    coder: claude-opus-5-5\n")
		web := repoAt(t, "acme/web", "agent:\n  models:\n    coder: claude-sonnet-5\n")
		c := planModels(t, Project{Prices: pricing.Embedded()}, app, web)

		checkChange(t, c, []LayerEdit{edit("defaults.agent.models.coder", "claude-sonnet-5")}, []string{
			"acme/app: coder claude-opus-5-5 -> claude-sonnet-5 (the project's)",
		})
	})

	t.Run("a tie price does not settle", func(t *testing.T) {
		// claude-sonnet-5 and claude-sonnet-5-5 are both $2/$10 in the table, so
		// the pair of them is no answer price can give and the ID decides, which
		// is the answer that does not move between runs. claude-zzz-9 has no row
		// at all, which is the dearest there is — the table's own rule is that
		// nothing is priced free — so it loses to both.
		app := repoAt(t, "acme/app", "agent:\n  models:\n    coder: claude-zzz-9\n")
		web := repoAt(t, "acme/web", "agent:\n  models:\n    coder: claude-sonnet-5\n")
		api := repoAt(t, "acme/api", "agent:\n  models:\n    coder: claude-sonnet-5-5\n")
		c := planModels(t, Project{Prices: pricing.Embedded()}, app, web, api)

		checkChange(t, c, []LayerEdit{edit("defaults.agent.models.coder", "claude-sonnet-5")}, []string{
			"acme/app: coder claude-zzz-9 -> claude-sonnet-5 (the project's)",
			"acme/api: coder claude-sonnet-5-5 -> claude-sonnet-5 (the project's)",
		})
	})

	t.Run("levels models", func(t *testing.T) {
		app := repoAt(t, "acme/app", "agent:\n  levels:\n    l2:\n      model: claude-sonnet-5-5\n    l3:\n      model: none\n")
		web := repoAt(t, "acme/web", "")
		c := planModels(t, Project{}, app, web)

		checkChange(t, c, []LayerEdit{edit("defaults.agent.levels.l2.model", "claude-sonnet-5-5")}, []string{
			"acme/web: level l2 Fugaro's default -> claude-sonnet-5-5 (the project's)",
		})
		if got := strings.Join(c.Lines, "\n"); strings.Contains(got, "l3") {
			t.Errorf("a level written `model: none` is off, not a model: %q", got)
		}
		for _, e := range c.LayerEdits {
			if slices.Contains(e.Path, "l3") {
				t.Errorf("MC1 moves `model: none` into the project: %+v", e)
			}
		}
	})

	t.Run("a repository may not choose a model its allow-list excludes", func(t *testing.T) {
		// app writes claude-opus-5-5, which its own budget.allowed_models does
		// not admit: that is no vote for opus (R17), so web's opus and api's
		// haiku tie and the cheaper model wins. Counting app's vote would have
		// made opus the project's model, which api could not run.
		app := repoAt(t, "acme/app", "agent:\n  models:\n    coder: claude-opus-5-5\nbudget:\n  allowed_models: [claude-haiku-4-5]\n")
		web := repoAt(t, "acme/web", "agent:\n  models:\n    coder: claude-opus-5-5\n")
		api := repoAt(t, "acme/api", "agent:\n  models:\n    coder: claude-haiku-4-5\n")
		c := planModels(t, Project{Prices: pricing.Embedded()}, app, web, api)

		checkChange(t, c, []LayerEdit{edit("defaults.agent.models.coder", "claude-haiku-4-5")}, []string{
			"acme/app: coder claude-opus-5-5 -> claude-haiku-4-5 (the project's)",
			"acme/web: coder claude-opus-5-5 -> claude-haiku-4-5 (the project's)",
		})
		for _, e := range c.LayerEdits {
			if e.Path[0] == "budget" {
				t.Errorf("MC1 writes budget.allowed_models, which B7 keeps in the repository: %+v", e)
			}
		}
	})

	t.Run("a move onto a model its allow-list excludes is named with the fix", func(t *testing.T) {
		app := repoAt(t, "acme/app", "agent:\n  models:\n    coder: claude-opus-5-5\n")
		web := repoAt(t, "acme/web", "agent:\n  models:\n    coder: claude-opus-5-5\n")
		api := repoAt(t, "acme/api", "budget:\n  allowed_models: [claude-haiku-4-5]\n")
		c := planModels(t, Project{}, app, web, api)

		checkChange(t, c, []LayerEdit{edit("defaults.agent.models.coder", "claude-opus-5-5")}, []string{
			"acme/api: coder Fugaro's default -> claude-opus-5-5 (the project's);" +
				" claude-opus-5-5 is not in its budget.allowed_models (claude-haiku-4-5):" +
				" add it there, or set the project's model to one it allows",
		})
	})

	t.Run("an excluded value nothing else writes does not move", func(t *testing.T) {
		app := repoAt(t, "acme/app", "agent:\n  models:\n    coder: claude-opus-5-5\nbudget:\n  allowed_models: [claude-haiku-4-5]\n")
		web := repoAt(t, "acme/web", "")
		c := planModels(t, Project{}, app, web)

		checkChange(t, c, nil, []string{
			"acme/app: coder claude-opus-5-5 did not move (its budget.allowed_models is claude-haiku-4-5):" +
				" add it there, or set the project's model to one it allows",
		})
		checkRecords(t, c, app, web)
	})

	t.Run("a repository named twice counts once", func(t *testing.T) {
		// Two spellings of one repository are one repository: counting it three
		// times would make its model the project's over the repository that
		// wrote the cheaper one, and its record would be written three times.
		app := repoAt(t, "acme/app", "agent:\n  models:\n    coder: claude-opus-5-5\n")
		web := repoAt(t, "acme/web", "agent:\n  models:\n    coder: claude-haiku-4-5\n")
		c := planModels(t, Project{}, app, web, app, app)

		checkChange(t, c, []LayerEdit{edit("defaults.agent.models.coder", "claude-haiku-4-5")}, []string{
			"acme/app: coder claude-opus-5-5 -> claude-haiku-4-5 (the project's)",
		})
		checkRecords(t, c, app, web)
	})

	t.Run("a repository this cannot key is named", func(t *testing.T) {
		// No slug means no record can be written for it and no earlier update's
		// record can be read, so it is named rather than dropped: a listing entry
		// that is not owner/name is the one shape here that no key exists for.
		app := repoAt(t, "acme", "agent:\n  models:\n    coder: claude-opus-5-5\n")
		web := repoAt(t, "acme/web", "agent:\n  models:\n    coder: claude-opus-5-5\n")
		c := planModels(t, Project{}, app, web)

		checkEdits(t, c, []LayerEdit{edit("defaults.agent.models.coder", "claude-opus-5-5")})
		if len(c.Lines) != 1 || !strings.HasPrefix(c.Lines[0], "acme: its provider and name are not a repository this can key (") {
			t.Errorf("a repository no slug can be built for is named with why: %q", c.Lines)
		}
		checkRecords(t, c, web)
	})

	t.Run("a migrated repository is named and never written", func(t *testing.T) {
		app := repoAt(t, "acme/app", "agent:\n  models:\n    coder: claude-opus-5-5\n")
		web := repoAt(t, "acme/web", "agent:\n  models:\n    coder: claude-opus-5-5\n")
		p := Project{Migrated: map[string]bool{slugOf(t, "acme/app"): true}}
		c := planModels(t, p, app, web)

		checkChange(t, c, []LayerEdit{edit("defaults.agent.models.coder", "claude-opus-5-5")}, []string{
			"acme/app: " + whyOf(t, "agent.models.coder"),
		})
		checkRecords(t, c, web)
	})

	t.Run("a second update changes nothing", func(t *testing.T) {
		app := repoAt(t, "acme/app", "")
		web := repoAt(t, "acme/web", "")
		p := Project{Migrated: map[string]bool{slugOf(t, "acme/app"): true, slugOf(t, "acme/web"): true}}
		c := planModels(t, p, app, web)

		if !c.Empty() {
			t.Errorf("a converged project's second update has a change: %+v", c)
		}
	})

	t.Run("a legal empty block is read, not refused", func(t *testing.T) {
		// A block whose members are all commented out, or a key with nothing
		// indented under it, is YAML's null: the strict decode reads it as the
		// zero value, so the file is a legal one that writes nothing at those
		// keys. Reading it as a shape no model name is would drop the whole
		// repository — its vote, its record and its line — over a file a person
		// commented out.
		web := repoAt(t, "acme/web", "budget:\nagent:\n  models:\n  levels:\n    l2:\n  crafts:\n    coder:\n")
		app := repoAt(t, "acme/app", "agent:\n  models:\n    coder: claude-sonnet-5-5\n")
		c := planModels(t, Project{}, web, app)

		checkChange(t, c, []LayerEdit{edit("defaults.agent.models.coder", "claude-sonnet-5-5")}, []string{
			"acme/web: coder Fugaro's default -> claude-sonnet-5-5 (the project's)",
		})
		checkRecords(t, c, web, app)
	})

	t.Run("a file update cannot read is named and left alone", func(t *testing.T) {
		app := repoAt(t, "acme/app", "agent: [oops\n")
		web := repoAt(t, "acme/web", "agent:\n  models:\n    coder: claude-sonnet-5-5\n")
		c := planModels(t, Project{}, app, web)

		checkEdits(t, c, []LayerEdit{edit("defaults.agent.models.coder", "claude-sonnet-5-5")})
		if len(c.Lines) != 1 || !strings.HasPrefix(c.Lines[0], "acme/app: its fugaro.yaml cannot be read as Fugaro reads one: ") {
			t.Errorf("a repository nothing could be read for is named with why: %q", c.Lines)
		}
		checkRecords(t, c, web)
	})

	t.Run("a repository the reader could not read is left to update", func(t *testing.T) {
		app := repoAt(t, "acme/app", "agent:\n  models:\n    coder: claude-sonnet-5-5\n")
		app.Unreadable = "the provider answered 404"
		c := planModels(t, Project{}, app)

		if !c.Empty() {
			t.Errorf("a repository no planner could read moved something: %+v", c)
		}
	})
}

// planModels runs MC1 over a fixture and holds it to the two things it never
// does: answer an error, and write anything but a layer edit and a migration
// record (a bug's writes are the budget database's and the store's, which B3
// does not touch).
func planModels(t *testing.T, p Project, repos ...Repo) Change {
	t.Helper()
	c, err := PlanModels(p, repos)
	if err != nil {
		t.Fatalf("PlanModels: %v", err)
	}
	if len(c.DBWrites) != 0 {
		t.Errorf("MC1 writes the budget database: %+v", c.DBWrites)
	}
	if len(c.StoreWrites) != 0 {
		t.Errorf("MC1 writes the store: %+v", c.StoreWrites)
	}
	return c
}

func checkChange(t *testing.T, c Change, wantEdits []LayerEdit, wantLines []string) {
	t.Helper()
	checkEdits(t, c, wantEdits)
	if !slices.Equal(c.Lines, wantLines) {
		t.Errorf("Lines = %q, want %q", c.Lines, wantLines)
	}
}

func checkEdits(t *testing.T, c Change, want []LayerEdit) {
	t.Helper()
	if len(c.LayerEdits) != len(want) {
		t.Fatalf("LayerEdits = %+v, want %+v", c.LayerEdits, want)
	}
	for i := range want {
		got := c.LayerEdits[i]
		if !slices.Equal(got.Path, want[i].Path) || got.Value != want[i].Value || got.Item != nil {
			t.Errorf("LayerEdits[%d] = %+v, want %+v", i, got, want[i])
		}
	}
}

func checkRecords(t *testing.T, c Change, repos ...Repo) {
	t.Helper()
	want := make([]string, len(repos))
	for i, r := range repos {
		want[i] = slugOf(t, r.Repo)
	}
	if !slices.Equal(c.Records, want) {
		t.Errorf("Records = %q, want %q", c.Records, want)
	}
}

// repoAt is one repository of a fixture: the owner/name the reader lists and
// the fugaro.yaml its default branch holds ("" for none), as M1's reader
// answers.
func repoAt(t *testing.T, name, cfg string) Repo {
	t.Helper()
	return Repo{Repo: name, Provider: "github", Branch: "main", Config: []byte(cfg)}
}

// layerOf is a project layer file as update hands it to a planner: the decoded
// YAML of the local layer file (C2's Project.Layer).
func layerOf(t *testing.T, text string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("the fixture layer is not YAML: %v", err)
	}
	return m
}

func slugOf(t *testing.T, name string) string {
	t.Helper()
	s, err := task.Slug("github", name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// whyOf is C1's sentence for a lifted key: the one text release B prints, which
// update repeats where a migrated repository still writes the key.
func whyOf(t *testing.T, key string) string {
	t.Helper()
	for _, k := range config.LiftedKeys {
		if k.Key == key {
			return k.Why
		}
	}
	t.Fatalf("%s is not a lifted key", key)
	return ""
}

func edit(path, value string) LayerEdit {
	return LayerEdit{Path: strings.Split(path, "."), Value: value}
}
