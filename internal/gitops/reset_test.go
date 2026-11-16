package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// The shapes of a baked .git this package cannot reset are refused, and the
// refusal names the shape — a run that meets one halts with that sentence
// (runner's checkProjectCheckoutOrigin), so what a reader of the halt sees is
// which of them the image held.
//
// Both entry points are asked: the read of the origin that a caller makes before
// it decides anything (BakedGitOrigin) and the reset itself refuse, so a caller
// that forgot the first cannot reach a git command through the second.
func TestBakedCheckoutResetRefusesAGitdirFileAlternatesOrSubmoduleHooks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		want  string
		plant func(t *testing.T, repo *Repo)
	}{
		{"a .git file", "naming a git directory outside the checkout", func(t *testing.T, repo *Repo) {
			if err := os.RemoveAll(filepath.Join(repo.Dir, ".git")); err != nil {
				t.Fatal(err)
			}
			writeAt(t, filepath.Join(repo.Dir, ".git"), "gitdir: "+t.TempDir()+"\n")
		}},
		{"alternates", "objects from a store outside the checkout", func(t *testing.T, repo *Repo) {
			writeAt(t, filepath.Join(repo.Dir, ".git", "objects", "info", "alternates"), "../elsewhere/objects\n")
		}},
		{"a commondir", "another repository's git directory", func(t *testing.T, repo *Repo) {
			writeAt(t, filepath.Join(repo.Dir, ".git", "commondir"), "../elsewhere\n")
		}},
		{"a submodule's configuration", "a submodule's configuration", func(t *testing.T, repo *Repo) {
			writeAt(t, filepath.Join(repo.Dir, ".git", "modules", "sub", "config"), "[remote \"origin\"]\n\turl = https://github.com/evil/other.git\n")
		}},
		{"a submodule's hooks", "a submodule's hook directory", func(t *testing.T, repo *Repo) {
			writeAt(t, filepath.Join(repo.Dir, ".git", "modules", "sub", "hooks", "pre-commit"), "#!/bin/sh\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, remote := setup(t)
			tc.plant(t, repo)
			if _, err := repo.BakedGitOrigin(ctx); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("BakedGitOrigin err = %v, want a refusal naming %q", err, tc.want)
			}
			if err := repo.ResetBakedGit(ctx, remote, filepath.Join(t.TempDir(), "hooks")); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ResetBakedGit err = %v, want a refusal naming %q", err, tc.want)
			}
		})
	}
}

// ResetBakedGit leaves the checkout in the state the run works from: a
// configuration holding the origin the run computed and the hook directory it
// owns, and nothing the image's build wrote into it — no push URL, no URL
// rewrite, no remote whose name is a URL, no credential helper, no include of
// another file — with the hooks, the replace refs (loose and packed) and the
// grafts gone, and everything git does not track removed from the tree.
//
// The tracked file the build edited is deliberately still edited: this package's
// reset does not touch tracked files, because the run's own checkout of the ref
// it fetched is what puts the tree at the fetched state (runner's bootstrap,
// CheckoutNewBranch), and that checkout is the runner's to make and not a
// checkout's to assume.
//
// The included file is what tells a replaced configuration apart from a
// configuration with keys removed: it sets core.fsmonitor, which is a command
// git runs, and it is unreachable afterwards only because the file that pulled
// it in is gone.
func TestBakedCheckoutResetDropsReplaceRefsAndGraftsAndBuildLeftovers(t *testing.T) {
	repo, remote := setup(t)
	const computed = "https://github.com/acme/app.git"
	bakedHooks := filepath.Join(t.TempDir(), "baked-hooks")
	writeAt(t, filepath.Join(bakedHooks, "pre-commit"), "#!/bin/sh\n")
	included := filepath.Join(t.TempDir(), "included")
	writeAt(t, included, "[core]\n\tfsmonitor = /bin/true\n")
	// What the image's build is taken to have left in the checkout.
	for _, kv := range [][2]string{
		{"core.hooksPath", bakedHooks},
		{"credential.helper", "store --file=/run/secrets/git-credentials"},
		{"include.path", included},
		{"remote.origin.pushurl", "https://github.com/evil/other.git"},
		{"url.https://github.com/evil/other.git.insteadOf", remote},
		{"remote." + computed + ".url", "https://github.com/evil/other.git"},
	} {
		testutil.Git(t, repo.Dir, "config", kv[0], kv[1])
	}
	replaced := testutil.Git(t, repo.Dir, "commit-tree", "-m", "replaced", testutil.Git(t, repo.Dir, "rev-parse", "HEAD^{tree}"))
	testutil.Git(t, repo.Dir, "replace", testutil.Git(t, repo.Dir, "rev-parse", "HEAD"), replaced)
	testutil.Git(t, repo.Dir, "pack-refs", "--all")
	writeAt(t, filepath.Join(repo.Dir, ".git", "info", "grafts"), testutil.Git(t, repo.Dir, "rev-parse", "HEAD")+"\n")
	testutil.WriteFiles(t, repo.Dir, map[string]string{
		"build/stale.o":             "built\n",
		"node_modules/dep/index.js": "installed\n",
		"stray.txt":                 "untracked\n",
		"README.md":                 "the build edited this\n",
	})
	runHooks := filepath.Join(t.TempDir(), "run-hooks") // the run's own, emptied by the reset
	writeAt(t, filepath.Join(runHooks, "left-behind"), "an earlier execution's file\n")

	if err := repo.ResetBakedGit(ctx, computed, runHooks); err != nil {
		t.Fatal(err)
	}
	want := "core.hookspath=" + runHooks + "\nremote.origin.url=" + computed + "\nremote.origin.fetch=+refs/heads/*:refs/remotes/origin/*"
	if got := testutil.Git(t, repo.Dir, "config", "--local", "--list"); got != want {
		t.Errorf("the reset configuration is\n%s\nwant\n%s", got, want)
	}
	if entries, err := os.ReadDir(runHooks); err != nil || len(entries) != 0 {
		t.Errorf("the run's hook directory holds %v (%v), want it emptied", entries, err)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".git", "hooks")); err == nil {
		t.Error("the hooks the image's build installed are still there")
	}
	if got := testutil.Git(t, repo.Dir, "for-each-ref", "--format=%(refname)", "refs/replace/"); got != "" {
		t.Errorf("the replacement %q is still there, packed or loose", got)
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, ".git", "info", "grafts")); err == nil {
		t.Error("the graft is still there")
	}
	for _, path := range []string{"build", "node_modules", "stray.txt"} {
		if _, err := os.Stat(filepath.Join(repo.Dir, path)); err == nil {
			t.Errorf("%s is still in the tree: what the image's build left is not the run's", path)
		}
	}
	if edited, err := os.ReadFile(filepath.Join(repo.Dir, "README.md")); err != nil || string(edited) != "the build edited this\n" {
		t.Errorf("README.md = %q (%v): the reset must leave tracked files to the run's own checkout of its ref", edited, err)
	}
}

// writeAt writes content at path, creating its directory.
func writeAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
