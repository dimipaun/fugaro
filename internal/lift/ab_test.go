package lift

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/rigor"
)

// TestPlanAB is AB3's own test: the A/B item of the migration moves every
// repository whose default branch writes ab.rate: 0 into the project layer's
// ab.repos.<owner/name>, which is where AB1 put the opt-out and ee/ab reads it
// once release A stops honouring the file's key
// (docs/repo-override-rulings/implementation-plan.md, spec §5.3, D3237, D3305).
func TestPlanAB(t *testing.T) {
	t.Run("rate zero", func(t *testing.T) {
		// The block form and the flow mapping are one key: both are the
		// repository's opt-out, and both become ab.repos.<repo>.
		got := planAB(t, Project{}, []Repo{
			abRepoWith(t, "acme/app", "ab:\n  rate: 0\n"),
			abRepoWith(t, "acme/tools", "ab: {rate: 0}\n"),
		})
		wantEdits := []LayerEdit{
			{Path: []string{"ab", "repos", "acme/app"}, Value: "0"},
			{Path: []string{"ab", "repos", "acme/tools"}, Value: "0"},
		}
		if !slices.EqualFunc(got.LayerEdits, wantEdits, sameEdit) {
			t.Errorf("LayerEdits = %+v, want %+v", got.LayerEdits, wantEdits)
		}
		wantLine(t, got, "acme/app: ab.rate 0 -> ab.repos.acme/app")
		wantLine(t, got, "acme/tools: ab.rate 0 -> ab.repos.acme/tools")
		if want := []string{rigorSlugOf(t, "acme/app"), rigorSlugOf(t, "acme/tools")}; !slices.Equal(got.Records, want) {
			t.Errorf("Records = %v, want %v (a repository this item moved is migrated once, D3305)", got.Records, want)
		}
		if n := len(got.Lines); n != 2 {
			t.Errorf("Lines = %v, want one line per repository and nothing else", got.Lines)
		}
		if len(got.StoreWrites) != 0 || len(got.DBWrites) != 0 {
			t.Errorf("the change writes more than the layer: %+v, %+v", got.StoreWrites, got.DBWrites)
		}
	})

	t.Run("nothing written", func(t *testing.T) {
		// A file with no ab: block, one with a block that states no rate, and
		// one with no file at all say nothing this item moves: the key that
		// lifts is ab.rate, and only a repository that wrote it is a change.
		got := planAB(t, Project{}, []Repo{
			abRepoWith(t, "acme/app", "version: 1\n"),
			abRepoWith(t, "acme/tools", "ab: {}\n"),
			{Repo: "acme/lib", Provider: "github", Branch: "main"},
		})
		if !got.Empty() {
			t.Errorf("Change = %+v, want nothing: none of the three writes ab.rate", got)
		}
	})

	t.Run("already written", func(t *testing.T) {
		// The project already answers for this repository: its value stands and
		// the file's opt-out is named (C2 — this item never overwrites a layer
		// value an admin has written).
		got := planAB(t, Project{Layer: layerTree(t, "ab:\n  rate: 0.05\n  repos:\n    acme/app: 0.02\n")}, []Repo{
			abRepoWith(t, "acme/app", "ab:\n  rate: 0\n"),
		})
		if len(got.LayerEdits) != 0 {
			t.Errorf("LayerEdits = %+v, want none: the project's entry stands", got.LayerEdits)
		}
		wantLine(t, got, "acme/app: ab.rate 0 dropped; the project's ab.repos.acme/app (0.02) stands")
		if len(got.Records) != 0 {
			t.Errorf("Records = %v, want none: this item wrote nothing (D3305)", got.Records)
		}
	})

	t.Run("already migrated", func(t *testing.T) {
		// D3305: a repository update has already migrated is named — C1's own
		// sentence — and moved no further. update cannot commit to a repository,
		// so deleting the key is the person's.
		got := planAB(t, Project{Migrated: map[string]bool{rigorSlugOf(t, "acme/app"): true}}, []Repo{
			abRepoWith(t, "acme/app", "ab:\n  rate: 0\n"),
		})
		if len(got.LayerEdits) != 0 || len(got.Records) != 0 {
			t.Errorf("Change = %+v, want no write and no record for a repository already migrated", got)
		}
		wantLine(t, got, "acme/app: ab.rate is the project's (D3237): remove the ab: block from fugaro.yaml")
	})

	t.Run("a rate a repository may not write", func(t *testing.T) {
		// validateRepoAB refuses any rate but 0 (D557), so this file is one the
		// config loader refuses: nothing of its block is moved and the line says
		// so, rather than the opt-out a person believes they wrote being moved
		// into the project as a rate.
		got := planAB(t, Project{}, []Repo{
			abRepoWith(t, "acme/app", "ab:\n  rate: 0.03\n"),
		})
		if len(got.LayerEdits) != 0 || len(got.Records) != 0 {
			t.Errorf("Change = %+v, want no write and no record", got)
		}
		wantLine(t, got, "acme/app: ab.rate 0.03 is not a repository's to write")
	})

	t.Run("a file that is not YAML", func(t *testing.T) {
		got := planAB(t, Project{}, []Repo{
			abRepoWith(t, "acme/app", "ab: [\n"),
		})
		if len(got.LayerEdits) != 0 {
			t.Errorf("LayerEdits = %+v, want none", got.LayerEdits)
		}
		wantLine(t, got, "acme/app: fugaro.yaml cannot be read")
		wantLine(t, got, "nothing of its ab: block was moved")
	})

	t.Run("ab.repos is full", func(t *testing.T) {
		// validateAB refuses a layer holding more than MaxRepoLevels
		// repositories, and a layer the loader refuses is one every run of the
		// project then fails to read: the last repository is named and left
		// alone instead.
		var b strings.Builder
		b.WriteString("ab:\n  rate: 0.05\n  repos:\n")
		for i := range rigor.MaxRepoLevels {
			b.WriteString("    acme/app-" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + ": 0\n")
		}
		got := planAB(t, Project{Layer: layerTree(t, b.String())}, []Repo{
			abRepoWith(t, "acme/last", "ab:\n  rate: 0\n"),
		})
		if len(got.LayerEdits) != 0 || len(got.Records) != 0 {
			t.Errorf("Change = %+v, want no write and no record: the map is full", got)
		}
		wantLine(t, got, "acme/last: ab.repos is full")
	})

	t.Run("one repository listed twice", func(t *testing.T) {
		// One repository named twice is one repository: one write, one line and
		// one record, or the second write would double the same entry (C2).
		got := planAB(t, Project{}, []Repo{
			abRepoWith(t, "acme/app", "ab:\n  rate: 0\n"),
			abRepoWith(t, "acme/app", "ab:\n  rate: 0\n"),
		})
		if n := len(got.LayerEdits); n != 1 {
			t.Errorf("LayerEdits = %+v, want the one write", got.LayerEdits)
		}
		if n := len(got.Records); n != 1 {
			t.Errorf("Records = %v, want the one record", got.Records)
		}
		if n := len(got.Lines); n != 1 {
			t.Errorf("Lines = %v, want the one line", got.Lines)
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		// M2 names every repository the reader could not read, in a line of its
		// own (D3306), and no planner acts on one: this item says nothing about
		// it, or the same repository would be named twice.
		got := planAB(t, Project{}, []Repo{
			{Repo: "acme/app", Provider: "github", Unreadable: "the default branch could not be read"},
			{Repo: "", Provider: "github"},
		})
		if !got.Empty() {
			t.Errorf("Change = %+v, want nothing", got)
		}
	})
}

func planAB(t *testing.T, p Project, repos []Repo) Change {
	t.Helper()
	got, err := PlanAB(p, repos)
	if err != nil {
		t.Fatalf("PlanAB: %v", err)
	}
	return got
}

func abRepoWith(t *testing.T, name, text string) Repo {
	t.Helper()
	return Repo{Repo: name, Provider: "github", Branch: "main", Config: []byte(text)}
}
