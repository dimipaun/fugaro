package lift

import (
	"bytes"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/task"
)

// recipeText is a valid recipe of one name; text marks a variant of it, so two
// files with different text are two recipes of one name (D3301's clash).
func recipeText(name, text string) []byte {
	return []byte("version: 1\nname: " + name + "\ndescription: " + text + "\nsteps:\n  - review: {}\n")
}

func recipeFileAt(path string, data []byte) RecipeFile { return RecipeFile{Path: path, Data: data} }

func repoWith(repo, provider string, files ...RecipeFile) Repo {
	return Repo{Repo: repo, Provider: provider, Branch: "main", Recipes: files}
}

func slugOf(t *testing.T, provider, repo string) string {
	t.Helper()
	slug, err := task.Slug(provider, repo)
	if err != nil {
		t.Fatalf("task.Slug(%q, %q): %v", provider, repo, err)
	}
	return slug
}

func copyOf(t *testing.T, name, provider, repo string) string {
	t.Helper()
	copyName := recipe.CopyName(name, provider, repo)
	if copyName == "" {
		t.Fatalf("recipe.CopyName(%q, %q, %q) is empty", name, provider, repo)
	}
	return copyName
}

// TestPlanRecipes covers B16's migration item (PR4): a file nothing else
// answers is published flat, a file a name already answers with other text
// becomes the repository's copy with its name: line rewritten, and every file
// the migration cannot publish is named and left.
func TestPlanRecipes(t *testing.T) {
	app, web := "acme/app", "acme/web"
	slugApp, slugWeb := slugOf(t, "github", app), slugOf(t, "github", web)
	copyReviewApp := copyOf(t, "review", "github", app)
	copyReviewWeb := copyOf(t, "review", "github", web)
	copyDefaultApp := copyOf(t, "default", "github", app)
	catalogReviewOnly, ok := recipe.CatalogText("review-only")
	if !ok {
		t.Fatal("the catalog holds no review-only recipe, and this test's catalog-clash case needs one")
	}

	// A recipe file at the 16 KiB limit exactly, which a copy's longer name:
	// line pushes over it.
	base := recipeText("default", "one")
	padding := recipe.MaxBytes - len(base) - 1
	atLimit := append(append([]byte{}, base...), '#')
	atLimit = append(atLimit, bytes.Repeat([]byte("x"), padding)...)
	if len(atLimit) != recipe.MaxBytes {
		t.Fatalf("the fixture is %d bytes, want %d", len(atLimit), recipe.MaxBytes)
	}
	overLimit := recipe.MaxBytes - len("default") + len(copyDefaultApp)

	broken := []byte("version: 1\nname: broken\nsteps: [\n")
	_, problems := recipe.Parse(broken)
	if len(problems) == 0 {
		t.Fatal("the fixture parses, and the invalid-file case needs a file that does not")
	}

	tests := []struct {
		name    string
		project Project
		repos   []Repo
		writes  map[string]string
		records []string
		lines   []string
	}{
		{
			name: "unique",
			repos: []Repo{repoWith(app, "github",
				recipeFileAt("review.yaml", recipeText("review", "one")))},
			writes:  map[string]string{recipe.ObjectKey("review"): string(recipeText("review", "one"))},
			records: []string{slugApp},
			lines: []string{
				"acme/app: .fugaro/recipes/review.yaml is published as the project recipe review",
			},
		},
		{
			name: "two-repositories-clash",
			repos: []Repo{
				repoWith(app, "github", recipeFileAt("review.yaml", recipeText("review", "one"))),
				repoWith(web, "github", recipeFileAt("review.yaml", recipeText("review", "two"))),
			},
			writes: map[string]string{
				recipe.ObjectKey(copyReviewApp): string(recipeText(copyReviewApp, "one")),
				recipe.ObjectKey(copyReviewWeb): string(recipeText(copyReviewWeb, "two")),
			},
			records: []string{slugApp, slugWeb},
			lines: []string{
				fmt.Sprintf("acme/app: .fugaro/recipes/review.yaml is published as %s, its name: line rewritten and nothing else", copyReviewApp),
				fmt.Sprintf("acme/web: .fugaro/recipes/review.yaml is published as %s, its name: line rewritten and nothing else", copyReviewWeb),
			},
		},
		{
			name:    "project-clash",
			project: Project{Recipes: map[string]string{"review": recipe.Sum(recipeText("review", "the project's"))}},
			repos: []Repo{repoWith(app, "github",
				recipeFileAt("review.yaml", recipeText("review", "one")))},
			writes: map[string]string{
				recipe.ObjectKey(copyReviewApp): string(recipeText(copyReviewApp, "one")),
			},
			records: []string{slugApp},
			lines: []string{
				fmt.Sprintf("acme/app: .fugaro/recipes/review.yaml is published as %s, its name: line rewritten and nothing else", copyReviewApp),
			},
		},
		{
			name: "catalog-clash",
			repos: []Repo{repoWith(app, "github",
				recipeFileAt("default.yaml", recipeText("default", "one")))},
			writes: map[string]string{
				recipe.ObjectKey(copyDefaultApp): string(recipeText(copyDefaultApp, "one")),
			},
			records: []string{slugApp},
			lines: []string{
				fmt.Sprintf("acme/app: .fugaro/recipes/default.yaml is published as %s, its name: line rewritten and nothing else", copyDefaultApp),
			},
		},
		{
			name: "catalog-identical",
			repos: []Repo{repoWith(app, "github",
				recipeFileAt("review-only.yaml", catalogReviewOnly))},
			records: []string{slugApp},
		},
		{
			name: "same-text",
			repos: []Repo{
				repoWith(app, "github", recipeFileAt("review.yaml", recipeText("review", "one"))),
				repoWith(web, "github", recipeFileAt("review.yaml", recipeText("review", "one"))),
			},
			writes:  map[string]string{recipe.ObjectKey("review"): string(recipeText("review", "one"))},
			records: []string{slugApp, slugWeb},
			lines: []string{
				"acme/app: .fugaro/recipes/review.yaml is published as the project recipe review",
				"acme/web: .fugaro/recipes/review.yaml is published as the project recipe review",
			},
		},
		{
			name: "invalid-file",
			repos: []Repo{repoWith(app, "github",
				recipeFileAt("broken.yaml", broken),
				recipeFileAt("review.yaml", recipeText("review", "one")))},
			writes:  map[string]string{recipe.ObjectKey("review"): string(recipeText("review", "one"))},
			records: []string{slugApp},
			lines: []string{
				"acme/app: .fugaro/recipes/broken.yaml is left: " + safetext.OneLine(recipe.ProblemsText(problems)),
				"acme/app: .fugaro/recipes/review.yaml is published as the project recipe review",
			},
		},
		{
			name: "name-mismatch",
			repos: []Repo{repoWith(app, "github",
				recipeFileAt("review.yaml", recipeText("other", "one")))},
			records: []string{slugApp},
			lines: []string{
				`acme/app: .fugaro/recipes/review.yaml is left: the file is review.yaml but the recipe names itself "other"; the file name and the name must agree`,
			},
		},
		{
			// The reader names a repository once, so two entries whose copies
			// would take one name are one repository in two spellings — or a
			// searched-for slug collision, which no test can build. What the
			// planner owes either way: both are named, and neither file is
			// published over the other.
			name: "copy-name-collision",
			repos: []Repo{
				repoWith(app, "github", recipeFileAt("review.yaml", recipeText("review", "one"))),
				repoWith(app, "github", recipeFileAt("review.yaml", recipeText("review", "two"))),
			},
			records: []string{slugApp},
			lines: []string{
				fmt.Sprintf("acme/app: .fugaro/recipes/review.yaml is left: the copy name %s is claimed by another repository's file", copyReviewApp),
				fmt.Sprintf("acme/app: .fugaro/recipes/review.yaml is left: the copy name %s is claimed by another repository's file", copyReviewApp),
			},
		},
		{
			name: "copy-name-taken",
			project: Project{Recipes: map[string]string{
				copyReviewApp: recipe.Sum(recipeText("review", "an admin's")),
			}},
			repos: []Repo{
				repoWith(app, "github", recipeFileAt("review.yaml", recipeText("review", "one"))),
				repoWith(web, "github", recipeFileAt("review.yaml", recipeText("review", "two"))),
			},
			writes: map[string]string{
				recipe.ObjectKey(copyReviewWeb): string(recipeText(copyReviewWeb, "two")),
			},
			records: []string{slugApp, slugWeb},
			lines: []string{
				fmt.Sprintf("acme/app: .fugaro/recipes/review.yaml is left: the copy name %s is already taken", copyReviewApp),
				fmt.Sprintf("acme/web: .fugaro/recipes/review.yaml is published as %s, its name: line rewritten and nothing else", copyReviewWeb),
			},
		},
		{
			name: "copy-over-the-limit",
			repos: []Repo{repoWith(app, "github",
				recipeFileAt("default.yaml", atLimit))},
			records: []string{slugApp},
			lines: []string{
				fmt.Sprintf("acme/app: .fugaro/recipes/default.yaml is left: its copy is %d bytes, over the %d byte limit", overLimit, recipe.MaxBytes),
			},
		},
		{
			name:    "already-migrated",
			project: Project{Migrated: map[string]bool{slugApp: true}},
			repos: []Repo{repoWith(app, "github",
				recipeFileAt("review.yaml", recipeText("review", "one")))},
			lines: []string{
				"acme/app: .fugaro/recipes/review.yaml is left: acme/app is already migrated, and its runs read the project's copy",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PlanRecipes(tt.project, tt.repos)
			if err != nil {
				t.Fatalf("PlanRecipes() error = %v", err)
			}
			writes := map[string]string{}
			for _, w := range got.StoreWrites {
				writes[w.Key] = string(w.Data)
			}
			if !maps.Equal(writes, tt.writes) {
				t.Errorf("store writes =\n%q\nwant\n%q", writes, tt.writes)
			}
			if !slices.Equal(got.Records, tt.records) {
				t.Errorf("records = %q, want %q", got.Records, tt.records)
			}
			if !slices.Equal(got.Lines, tt.lines) {
				t.Errorf("lines =\n%s\nwant\n%s", strings.Join(got.Lines, "\n"), strings.Join(tt.lines, "\n"))
			}
			if len(got.LayerEdits) != 0 || len(got.DBWrites) != 0 {
				t.Errorf("the recipes item writes the layer and the database nothing, and this change carries %d layer edits and %d database writes", len(got.LayerEdits), len(got.DBWrites))
			}
			again, err := PlanRecipes(tt.project, tt.repos)
			if err != nil {
				t.Fatalf("PlanRecipes() second run error = %v", err)
			}
			if !reflect.DeepEqual(got, again) {
				t.Errorf("a second plan of the same project differs, and update's lines would move between runs:\n%+v\n%+v", got, again)
			}
		})
	}
}

// TestPlanRecipesKeepsEverythingButTheNameLine pins R14: a copy is the
// repository's file with its name: line rewritten and nothing else, so a
// comment beside the name, the value's own spelling and the text's line
// endings all survive the rewrite.
func TestPlanRecipesKeepsEverythingButTheNameLine(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{
			name: "plain with a comment",
			data: "version: 1\nname: review # the recipe's own name\nsteps:\n  - review: {}\n",
			want: "version: 1\nname: %s # the recipe's own name\nsteps:\n  - review: {}\n",
		},
		{
			name: "double-quoted",
			data: "version: 1\nname: \"review\"\nsteps:\n  - review: {}\n",
			want: "version: 1\nname: %s\nsteps:\n  - review: {}\n",
		},
		{
			name: "single-quoted",
			data: "version: 1\nname: 'review'\nsteps:\n  - review: {}\n",
			want: "version: 1\nname: %s\nsteps:\n  - review: {}\n",
		},
		{
			name: "value on the next line",
			data: "version: 1\nname:\n  review\nsteps:\n  - review: {}\n",
			want: "version: 1\nname:\n  %s\nsteps:\n  - review: {}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, web := "acme/app", "acme/web"
			got, err := PlanRecipes(Project{}, []Repo{
				repoWith(app, "github", recipeFileAt("review.yaml", []byte(tt.data))),
				repoWith(web, "github", recipeFileAt("review.yaml", recipeText("review", "two"))),
			})
			if err != nil {
				t.Fatalf("PlanRecipes() error = %v", err)
			}
			copyName := copyOf(t, "review", "github", app)
			want := fmt.Sprintf(tt.want, copyName)
			var gotText string
			for _, w := range got.StoreWrites {
				if w.Key == recipe.ObjectKey(copyName) {
					gotText = string(w.Data)
				}
			}
			if gotText != want {
				t.Errorf("the copy =\n%q\nwant\n%q", gotText, want)
			}
		})
	}
}

// TestPlanRecipesUnreadableAndUnkeyable: a repository update could not read is
// named by update, and no planner acts on it (D3306); one it can list but cannot key is
// named and left rather than dropped in silence.
func TestPlanRecipesUnreadableAndUnkeyable(t *testing.T) {
	got, err := PlanRecipes(Project{}, []Repo{
		{Repo: "acme/app", Provider: "github", Unreadable: "its provider could not be opened"},
		{Repo: "acme", Provider: "github", Recipes: []RecipeFile{recipeFileAt("review.yaml", recipeText("review", "one"))}},
	})
	if err != nil {
		t.Fatalf("PlanRecipes() error = %v", err)
	}
	if len(got.StoreWrites) != 0 || len(got.Records) != 0 {
		t.Errorf("an unreadable repository and one that cannot be keyed publish nothing and record nothing, and this change carries %d writes and %q", len(got.StoreWrites), got.Records)
	}
	if len(got.Lines) != 1 || !strings.HasPrefix(got.Lines[0], "acme: .fugaro/recipes/ is left: ") {
		t.Errorf("lines = %q, want one line naming the repository that cannot be keyed", got.Lines)
	}
}
