package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// hotfixCommitsCheck runs scripts/hotfix-commits.sh's rule on tip, in dir, the
// way release.sh and verify-release.sh source it.
func hotfixCommitsCheck(t *testing.T, dir, tag, tip string) (string, bool) {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", `set -euo pipefail; source "$1/scripts/hotfix-commits.sh"; hotfix_commits_check "$2" "$3"`, "bash", root, tag, tip)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err == nil
}

// A second patch of a line is judged on what it adds. The first patch's own
// text commit (docs/releases/v0.12.1.md, on no commit of main by design) is on
// the branch, was held to the rule when v0.12.1 was tagged, and must not refuse
// v0.12.2: before this, every patch after the first was refused.
//
// Mutations caught: judging the whole branch against main alone (the first
// patch's notes refuse the second patch), and excluding the earlier tag without
// holding what comes after it (a hand-written change on top of the second patch
// is still refused).
func TestHotfixCommitsJudgesASecondPatchOnWhatItAdds(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string { return testutil.Git(t, dir, args...) }
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) string {
		git("add", "-A")
		git("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--quiet", "-m", msg)
		return git("rev-parse", "HEAD")
	}
	git("init", "--quiet", "-b", "main")
	write("notes.txt", "one\ntwo\nthree\n")
	commit("v0.12.0")
	git("tag", "v0.12.0")
	write("notes.txt", "one\ntwo, fixed\nthree\n")
	fix1 := commit("fix one")
	write("other.txt", "a\nb\nc\n")
	fix2 := commit("fix two")
	git("update-ref", "refs/remotes/origin/main", "HEAD")

	git("checkout", "--quiet", "-b", "release/0.12", "v0.12.0")
	git("-c", "user.name=t", "-c", "user.email=t@example.com", "cherry-pick", "-x", fix1)
	write("docs/releases/v0.12.1.md", "- notes\n")
	commit("release: v0.12.1 notes")
	git("tag", "v0.12.1")

	git("-c", "user.name=t", "-c", "user.email=t@example.com", "cherry-pick", "-x", fix2)
	write("docs/releases/v0.12.2.md", "- notes\n")
	commit("release: v0.12.2 notes")
	tip := git("rev-parse", "HEAD")

	if out, ok := hotfixCommitsCheck(t, dir, "v0.12.2", tip); !ok || out != "1" {
		t.Fatalf("the second patch was refused or miscounted (ok=%v): %q, want it to pass with the one new pick", ok, out)
	}

	write("notes.txt", "one\ntwo, fixed\nthree, by hand\n")
	hand := commit("a change made on the branch")
	if out, ok := hotfixCommitsCheck(t, dir, "v0.12.2", hand); ok || !strings.Contains(out, "notes.txt") {
		t.Fatalf("a hand-written change after the earlier patch passed (ok=%v): %q", ok, out)
	}
}
