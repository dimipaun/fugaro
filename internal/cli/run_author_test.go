package cli

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

// releaseAuthor and releaseBeforeAuthor are the two job images the tests here
// judge: one built from a base at the release that ships the author field and
// the git.author key, one from the release before it.
const (
	releaseAuthor       = "ghcr.io/dimipaun/fugaro-base:0.14.0"
	releaseBeforeAuthor = "ghcr.io/dimipaun/fugaro-base:0.13.0"
)

// authorYAML is a fugaro.yaml whose git: block names author as git.author.
func authorYAML(author string) string {
	return "version: 1\nproject: aurora\ngit: { provider: github, author: " + author + " }\nworkflows:\n  web: { commands: { build: sh b.sh, test: sh t.sh } }\n"
}

// TestRunAuthorFromLocalConfig: a repository that opted in (git.author:
// launcher) gets a task carrying the launcher's own identity, taken from the
// local config's user: — the name being git's user.name, else the address's
// local part.
func TestRunAuthorFromLocalConfig(t *testing.T) {
	f := newCloudFixture(t)
	writeBuildRecord(t, f, appSlug, "web", releaseAuthor)
	baseBranchCheckout(t, "acme/app", authorYAML(config.GitAuthorLauncher), "main")
	if _, _, err := execute(t, "run", "--run-id", "20261030-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	spec := readSpec(t, f, "20261030-100000-abcd")
	want := &task.Person{Name: "someone", Email: "someone@example.com"}
	if spec.Author == nil || *spec.Author != *want {
		t.Fatalf("author = %+v, want %+v: the launch recorded no author for a repository that opted in", spec.Author, want)
	}
}
