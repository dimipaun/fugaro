package lift

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestPlanRigor is RG2's own test: the rigor item of the migration moves each
// repository's rigor.default and rigor.areas into the project layer, keeps the
// project's value where it already states one, and names every repository and
// path whose level moves either way under D3294's order
// (docs/repo-override-rulings/implementation-plan.md, R4, R5, D3305).
func TestPlanRigor(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		// The block form and the scalar shorthand D20 shipped: both are the
		// repository's level, and both become defaults.rigor.repos.<repo>.
		got := planRigor(t, Project{}, []Repo{
			repoWith(t, "acme/app", "rigor:\n  default: poc\n"),
			repoWith(t, "acme/tools", "rigor: pro\n"),
		})
		wantEdits := []LayerEdit{
			{Path: []string{"defaults", "rigor", "repos", "acme/app"}, Value: "poc"},
			{Path: []string{"defaults", "rigor", "repos", "acme/tools"}, Value: "pro"},
		}
		if !slices.EqualFunc(got.LayerEdits, wantEdits, sameEdit) {
			t.Errorf("LayerEdits = %+v, want %+v", got.LayerEdits, wantEdits)
		}
		wantLine(t, got, "acme/app: rigor.default poc -> defaults.rigor.repos.acme/app")
		wantLine(t, got, "acme/tools: rigor.default pro -> defaults.rigor.repos.acme/tools")
		if want := []string{slugOf(t, "acme/app"), slugOf(t, "acme/tools")}; !slices.Equal(got.Records, want) {
			t.Errorf("Records = %v, want %v (a repository this item moved is migrated once, D3305)", got.Records, want)
		}
		if n := len(got.Lines); n != 2 {
			t.Errorf("Lines = %v, want one line per repository and nothing else", got.Lines)
		}
	})

	t.Run("existing entry kept", func(t *testing.T) {
		// The project already answers for this repository: its value stands and
		// the file's is named (RG2's acceptance). The level does move — the
		// file's value outranks the entry today and stops applying — so R5
		// names it with the old and the new level.
		got := planRigor(t, Project{Layer: layerTree(t, "defaults:\n  rigor:\n    repos:\n      acme/app: critical\n")}, []Repo{
			repoWith(t, "acme/app", "rigor:\n  default: poc\n"),
		})
		if len(got.LayerEdits) != 0 {
			t.Errorf("LayerEdits = %+v, want none: the project's entry stands", got.LayerEdits)
		}
		wantLine(t, got, "acme/app: rigor.default poc dropped; the project's defaults.rigor.repos.acme/app (critical) stands")
		wantLine(t, got, "acme/app: every path poc -> critical")
		if len(got.Records) != 0 {
			t.Errorf("Records = %v, want none: this item wrote nothing (D3305)", got.Records)
		}
	})

	t.Run("areas", func(t *testing.T) {
		// Each of the repository's rules becomes a project area with one member
		// — {repo, paths: [its match]} — keeping its name (RG2's acceptance).
		got := planRigor(t, Project{}, []Repo{
			repoWith(t, "acme/app", `rigor:
  areas:
    - name: ui
      match: ui/**
      level: startup
    - name: api
      match: internal/api/**
      level: critical
`),
		})
		want := []LayerEdit{
			areaWant("ui", "acme/app", "ui/**", "startup"),
			areaWant("api", "acme/app", "internal/api/**", "critical"),
		}
		if !slices.EqualFunc(got.LayerEdits, want, sameEdit) {
			t.Errorf("LayerEdits = %+v, want %+v", got.LayerEdits, want)
		}
		wantLine(t, got, `acme/app: rigor.areas "ui" startup -> defaults.rigor.areas "ui" (member acme/app, paths ui/**)`)
		wantLine(t, got, `acme/app: rigor.areas "api" critical -> defaults.rigor.areas "api" (member acme/app, paths internal/api/**)`)
		if want := []string{slugOf(t, "acme/app")}; !slices.Equal(got.Records, want) {
			t.Errorf("Records = %v, want %v", got.Records, want)
		}
	})

	t.Run("name taken", func(t *testing.T) {
		// The name is taken by any project area, not only by another
		// repository's moved one: F11's dedupe is by name, so a shared name
		// would merge two rules into one. R4 appends -<owner-repo>.
		got := planRigor(t, Project{Layer: layerTree(t, `defaults:
  rigor:
    areas:
      - name: ui
        match: web/**
        level: startup
`)}, []Repo{
			repoWith(t, "acme/app", "rigor:\n  areas:\n    - name: ui\n      match: ui/**\n      level: poc\n"),
		})
		if n := len(got.LayerEdits); n != 1 {
			t.Fatalf("LayerEdits = %+v, want the one moved area", got.LayerEdits)
		}
		if key := got.LayerEdits[0].Item.Key; key != "ui-acme-app" {
			t.Errorf("the moved area's name = %q, want %q", key, "ui-acme-app")
		}
		wantLine(t, got, `defaults.rigor.areas "ui-acme-app" (member acme/app, paths ui/**; the name "ui" is taken)`)
	})

	t.Run("name too long", func(t *testing.T) {
		// A name at MaxAreaName plus -<owner-repo> does not fit, and the cut
		// that fits may be taken too: R4 cuts to 40 and takes a short hash on
		// a second collision. Two rules of one repository can share a name
		// (only a repeated match is refused), so the second one needs the hash
		// as well — otherwise the two would merge in the layer.
		long := strings.Repeat("u", rigor.MaxAreaName)
		got := planRigor(t, Project{Layer: layerTree(t, "defaults:\n  rigor:\n    areas:\n"+
			"      - name: "+long+"\n        match: web/**\n        level: startup\n")}, []Repo{
			repoWith(t, "acme/app", "rigor:\n  areas:\n"+
				"    - name: "+long+"\n      match: ui/**\n      level: poc\n"+
				"    - name: "+long+"\n      match: lib/**\n      level: startup\n"),
		})
		if n := len(got.LayerEdits); n != 2 {
			t.Fatalf("LayerEdits = %+v, want the two moved areas", got.LayerEdits)
		}
		hashName := regexp.MustCompile(`^.{0,31}-[0-9a-f]{8}$`)
		seen := map[string]bool{}
		for _, e := range got.LayerEdits {
			key := e.Item.Key
			if len(key) > rigor.MaxAreaName {
				t.Errorf("the moved area's name %q is longer than %d", key, rigor.MaxAreaName)
			}
			if key == long || seen[key] {
				t.Errorf("the moved area's name %q repeats one the project holds or another moved rule", key)
			}
			if !hashName.MatchString(key) {
				t.Errorf("the moved area's name %q is not the cut name with a short hash", key)
			}
			seen[key] = true
			// The layer's own validation is what the name has to satisfy.
			if msg := itemArea(e.Item).ValidateProject(); msg != "" {
				t.Errorf("the moved area %q is one the project layer refuses: %s", key, msg)
			}
		}
	})

	t.Run("empty block", func(t *testing.T) {
		// A rigor: block that sets nothing is the repository's opt-in
		// (Configured) and has no level of its own: it becomes
		// rigor.repos.<repo> at the level it resolves to today, so release B's
		// retirement of the block does not drop the opt-in (RG2's acceptance).
		// The block is written both ways an empty one is: `rigor:` and
		// `rigor: {}`.
		got := planRigor(t, Project{Layer: layerTree(t, "defaults:\n  rigor:\n    default: startup\n")}, []Repo{
			repoWith(t, "acme/app", "rigor:\n"),
			repoWith(t, "acme/tools", "rigor: {}\n"),
		})
		want := []LayerEdit{
			{Path: []string{"defaults", "rigor", "repos", "acme/app"}, Value: "startup"},
			{Path: []string{"defaults", "rigor", "repos", "acme/tools"}, Value: "startup"},
		}
		if !slices.EqualFunc(got.LayerEdits, want, sameEdit) {
			t.Errorf("LayerEdits = %+v, want %+v", got.LayerEdits, want)
		}
		wantLine(t, got, "acme/app: an empty rigor: block -> defaults.rigor.repos.acme/app (startup), the level it resolves to today")
		wantLine(t, got, "acme/tools: an empty rigor: block -> defaults.rigor.repos.acme/tools (startup), the level it resolves to today")
	})

	t.Run("no room for another repository level", func(t *testing.T) {
		// The project layer already holds MaxRepoLevels repositories, and both
		// writes this item makes — the default, and the empty block's resolved
		// level — add one more key. validateRigorRepos refuses a layer one entry
		// over the bound, so the repository is named and left alone instead:
		// publishing a layer every run then fails to load is not a migration.
		var b strings.Builder
		b.WriteString("defaults:\n  rigor:\n    repos:\n")
		for i := range rigor.MaxRepoLevels {
			fmt.Fprintf(&b, "      acme/r%d: pro\n", i)
		}
		got := planRigor(t, Project{Layer: layerTree(t, b.String())}, []Repo{
			repoWith(t, "acme/empty", "rigor:\n"),
			repoWith(t, "acme/level", "rigor:\n  default: poc\n"),
		})
		if len(got.LayerEdits) != 0 || len(got.Records) != 0 {
			t.Errorf("LayerEdits = %+v, Records = %v, want none: the map holds all the repositories the layer may", got.LayerEdits, got.Records)
		}
		for _, repo := range []string{"acme/empty", "acme/level"} {
			wantLine(t, got, fmt.Sprintf("%s: the project layer already states the level of %d repositories, the most it may hold", repo, rigor.MaxRepoLevels))
		}
	})

	t.Run("a raise named", func(t *testing.T) {
		// The moved rule and an existing project area naming this repository
		// now sit at one rank, where the strictest wins: the path rises from
		// the repository's own rule to the project area's level (F12, R5).
		got := planRigor(t, Project{Layer: layerTree(t, `defaults:
  rigor:
    areas:
      - name: ui-tests
        members:
          - repo: acme/app
            paths: [ui/**]
        level: startup
`)}, []Repo{
			repoWith(t, "acme/app", "rigor:\n  default: pro\n  areas:\n    - name: ui\n      match: ui/**\n      level: poc\n"),
		})
		wantLine(t, got, "acme/app: ui/** poc -> startup")
	})

	t.Run("a lowering named", func(t *testing.T) {
		// D3294's order moves levels the other way too: a project area naming
		// this repository outranks rigor.repos, where the repository's own
		// default outranked that area before (R5).
		got := planRigor(t, Project{Layer: layerTree(t, `defaults:
  rigor:
    areas:
      - name: payments
        members:
          - repo: acme/app
        level: poc
`)}, []Repo{
			repoWith(t, "acme/app", "rigor:\n  default: critical\n"),
		})
		wantLine(t, got, "acme/app: every path critical -> poc")
	})

	t.Run("already migrated", func(t *testing.T) {
		// A repository with a record is never migrated again (D3305): the
		// planner writes nothing for it and only names the keys its file still
		// writes, with C1's own sentence.
		got := planRigor(t, Project{Migrated: map[string]bool{slugOf(t, "acme/app"): true}}, []Repo{
			repoWith(t, "acme/app", "rigor:\n  default: poc\n  areas:\n    - name: ui\n      match: ui/**\n      level: startup\n"),
		})
		if len(got.LayerEdits) != 0 || len(got.Records) != 0 {
			t.Errorf("LayerEdits = %+v, Records = %v, want none for a migrated repository (D3305)", got.LayerEdits, got.Records)
		}
		if n := len(got.Lines); n != 2 {
			t.Fatalf("Lines = %v, want one line per lifted key the file still writes", got.Lines)
		}
		for _, line := range got.Lines {
			if !strings.HasPrefix(line, "acme/app: rigor.") || !strings.Contains(line, "is the project's") {
				t.Errorf("line %q does not name the lifted key and its fix", line)
			}
		}
	})

	t.Run("a block that cannot be moved", func(t *testing.T) {
		// A repository the migration cannot move is named and left alone:
		// half-migrating it would drop the rest of its block when release A
		// stops honouring it, so nothing is written for it at all.
		got := planRigor(t, Project{}, []Repo{
			repoWith(t, "acme/app", "rigor:\n  default: [poc\n"),
			repoWith(t, "acme/bad-rule", "rigor:\n  default: poc\n  areas:\n    - name: ui\n      match: /ui/**\n      level: startup\n"),
			Repo{Repo: "acme/unread", Provider: "github", Branch: "main", Unreadable: "the default branch could not be read"},
		})
		if len(got.LayerEdits) != 0 {
			t.Errorf("LayerEdits = %+v, want none", got.LayerEdits)
		}
		if n := len(got.Lines); n != 2 {
			t.Fatalf("Lines = %v, want one line for each repository this item cannot move and none for the unreadable one (it is the reader's line)", got.Lines)
		}
		for _, line := range got.Lines {
			if !strings.Contains(line, "nothing of its rigor: block was moved") {
				t.Errorf("line %q does not say the repository was left alone", line)
			}
		}
		wantLine(t, got, "acme/app: fugaro.yaml cannot be read")
		wantLine(t, got, "acme/bad-rule: rigor.areas[0]")
	})

	t.Run("no rigor block", func(t *testing.T) {
		// A repository that writes no rigor: key is not this item's business:
		// no line, no write, no record.
		got := planRigor(t, Project{}, []Repo{repoWith(t, "acme/app", "workflows:\n  default: {}\n")})
		if !got.Empty() {
			t.Errorf("the change = %+v, want nothing at all", got)
		}
	})
}

func planRigor(t *testing.T, p Project, repos []Repo) Change {
	t.Helper()
	got, err := PlanRigor(p, repos)
	if err != nil {
		t.Fatalf("PlanRigor: %v", err)
	}
	return got
}

// layerTree is the project layer's tree as M2 hands it to a planner: the
// decoded YAML of the local layer file.
func layerTree(t *testing.T, text string) map[string]any {
	t.Helper()
	var tree map[string]any
	if err := yaml.Unmarshal([]byte(text), &tree); err != nil {
		t.Fatalf("the layer fixture does not decode: %v", err)
	}
	return tree
}

func repoWith(t *testing.T, name, text string) Repo {
	t.Helper()
	return Repo{Repo: name, Provider: "github", Branch: "main", Config: []byte(text)}
}

func slugOf(t *testing.T, repo string) string {
	t.Helper()
	slug, err := task.Slug("github", repo)
	if err != nil {
		t.Fatalf("task.Slug(%q): %v", repo, err)
	}
	return slug
}

// wantLine fails unless some line of the change holds want.
func wantLine(t *testing.T, c Change, want string) {
	t.Helper()
	for _, line := range c.Lines {
		if strings.Contains(line, want) {
			return
		}
	}
	t.Errorf("Lines = %v, want a line holding %q", c.Lines, want)
}

// areaWant is the edit a moved rule becomes: the project area the rule's name
// (or the name R4 gives it), one member for the repository, and its match as
// that member's paths.
func areaWant(name, repo, match, level string) LayerEdit {
	return LayerEdit{
		Path: []string{"defaults", "rigor", "areas"},
		Item: &LayerItem{
			KeyField: "name",
			Key:      name,
			Fields:   map[string]string{"level": level},
			Items: map[string][]LayerItem{"members": {{
				KeyField: "repo",
				Key:      repo,
				Lists:    map[string][]string{"paths": {match}},
			}}},
		},
	}
}

// itemArea is the area a layer item spells, so a test can hold the item to the
// layer's own validation.
func itemArea(it *LayerItem) rigor.Area {
	out := rigor.Area{Name: it.Key, Level: rigor.Rigor(it.Fields["level"])}
	for _, m := range it.Items["members"] {
		out.Members = append(out.Members, rigor.Member{Repo: m.Key, Paths: m.Lists["paths"]})
	}
	return out
}

func sameEdit(a, b LayerEdit) bool {
	if !slices.Equal(a.Path, b.Path) || a.Value != b.Value {
		return false
	}
	if (a.Item == nil) != (b.Item == nil) {
		return false
	}
	if a.Item == nil {
		return true
	}
	return a.Item.KeyField == b.Item.KeyField && a.Item.Key == b.Item.Key &&
		slices.Equal(mapsOf(a.Item.Fields), mapsOf(b.Item.Fields)) &&
		slices.Equal(mapsOf(a.Item.Lists), mapsOf(b.Item.Lists)) &&
		sameItems(a.Item.Items, b.Item.Items)
}

// mapsOf flattens a map for comparison: the layer's items are written by field
// name, and a test cares which fields and values, not their order.
func mapsOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+strings.Join(strings.Fields(sprint(v)), " "))
	}
	slices.Sort(out)
	return out
}

func sameItems(a, b map[string][]LayerItem) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !sameEdit(LayerEdit{Item: &av[i]}, LayerEdit{Item: &bv[i]}) {
				return false
			}
		}
	}
	return true
}

func sprint(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []string:
		return strings.Join(x, ",")
	}
	return ""
}
