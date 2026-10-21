package images_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Fugaro's mise.toml pins the Go of go.mod's go line's minor series and the
// Terraform CI tests with, and keeps GOTOOLCHAIN local, as the go base did.
func TestRepositoryMisePins(t *testing.T) {
	mise, err := os.ReadFile("../mise.toml")
	if err != nil {
		t.Fatal(err)
	}
	pin := func(name string) string {
		m := regexp.MustCompile(`(?m)^` + name + ` = "([^"]+)"$`).FindStringSubmatch(string(mise))
		if m == nil {
			t.Fatalf("mise.toml has no %s pin", name)
		}
		return m[1]
	}
	mod, _ := os.ReadFile("../go.mod")
	gm := regexp.MustCompile(`(?m)^go (\d+\.\d+)`).FindStringSubmatch(string(mod))
	if gm == nil || !strings.HasPrefix(pin("go"), gm[1]+".") {
		t.Errorf("mise.toml go %s is not in go.mod's %v series", pin("go"), gm)
	}
	ci, _ := os.ReadFile("../.github/workflows/ci.yml")
	tm := regexp.MustCompile(`terraform_version: ([\d.]+)`).FindStringSubmatch(string(ci))
	if tm == nil || pin("terraform") != tm[1] {
		t.Errorf("mise.toml terraform %s, ci.yml %v", pin("terraform"), tm)
	}
	if !strings.Contains(string(mise), "GOTOOLCHAIN = \"local\"") {
		t.Error("mise.toml does not keep GOTOOLCHAIN local")
	}
}

// No built Windows binary is repository content. A bare
// `GOOS=windows go build ./cmd/fugaro` writes its .exe into the directory it
// ran in — the tree root for the CLI, internal/clidoc and the two generators
// for theirs — and 181 MB of them reached the Windows-port pull request that
// way (fugaro.exe and clidoc.exe at 78 MB each, fakeclaude.exe,
// faketerraform.exe, gencatalog.exe), which every clone and every job then
// paid for. `*.exe` in .gitignore keeps one from being staged; this is the pin
// for the case where it was staged anyway, because gitignore does not apply to
// a path git already tracks. Build them with `-o` outside the tree
// (docs/windows-cli/README.md's command writes to $TMPDIR).
//
// The listing is git's own (`git ls-files`), so it is exactly what a clone
// would fetch: an untracked .exe left in a working tree is a local artifact,
// not a repository one, and is deliberately not this test's business. gitFiles
// fails when root is not a checkout or git is not installed, and there is then
// no tracked set to check.
//
// Mutations: `touch stray.exe && git add stray.exe` fails it (and passes again
// after `git rm --cached stray.exe`), and deleting the `*.exe` line from
// .gitignore fails it on the second half below.
func TestRepositoryTracksNoBuiltBinaries(t *testing.T) {
	files, err := gitFiles("..")
	if err != nil {
		t.Skipf("no git listing to check, so no tracked set: %v", err)
	}
	for _, rel := range files {
		if strings.EqualFold(filepath.Ext(rel), ".exe") {
			t.Errorf("%s is tracked: a built Windows binary is not repository content; build it with -o outside the tree", rel)
		}
	}
	// The ignore rule and this pin are two halves of one decision, so a branch
	// that removes either is caught here rather than by the next 78 MB commit.
	ignore, err := os.ReadFile("../.gitignore")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ignore), "\n*.exe\n") {
		t.Error(".gitignore no longer ignores *.exe, so a `go build` into the tree can be staged again")
	}
}
