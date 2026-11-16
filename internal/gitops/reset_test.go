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
//
// The last two rows are the shape a link makes: git follows a symbolic link, so
// a .git/modules — or a .git/modules/<name> a submodule's .git file names — that
// is one keeps the configuration it names outside the checkout, where neither
// the walk of .git/modules nor the check of a submodule's worktree (which reads
// that very path as one the checkout holds) reaches it, and the run's own git
// runs its core.fsmonitor all the same: a top-level status runs the submodule's
// own status.
//
// The commondir and alternates rows are the two shapes the top-level .git is
// refused for, one level in: a commondir in a submodule's git directory names
// the git directory git reads that submodule's configuration from, and an
// alternates file there makes an external store's objects the submodule's.
// Neither is named config, so the walk's name check and the worktree check both
// miss them — measured in a scratch worktree: a submodule whose git directory
// held only HEAD and a commondir naming an external repository made a plain
// top-level git status run that repository's core.fsmonitor.
//
// Mutation (run, restored): drop the symlink refusal from refuseSubmoduleState
// and the last two rows are accepted, the run's git finding the linked
// configuration; drop the commondir or the alternates refusal — each was run on
// its own — and that row alone is accepted the same way, ResetBakedGit
// included.
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
		{"a submodule's commondir", "names another git directory as the one shared by the submodule", func(t *testing.T, repo *Repo) {
			plantGitlink(t, repo, "vendor/dep")
			writeAt(t, filepath.Join(repo.Dir, "vendor", "dep", ".git"), "gitdir: ../../.git/modules/dep\n")
			writeAt(t, filepath.Join(repo.Dir, ".git", "modules", "dep", "commondir"), filepath.Join(t.TempDir(), "external")+"\n")
		}},
		{"a submodule's alternates", "objects from a store outside the checkout", func(t *testing.T, repo *Repo) {
			writeAt(t, filepath.Join(repo.Dir, ".git", "modules", "dep", "objects", "info", "alternates"), "../elsewhere/objects\n")
		}},
		{"a symlinked .git/modules", "is a symbolic link", func(t *testing.T, repo *Repo) {
			external := t.TempDir()
			writeAt(t, filepath.Join(external, "sub", "config"), "[core]\n\tfsmonitor = /bin/true\n")
			if err := os.Symlink(external, filepath.Join(repo.Dir, ".git", "modules")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a submodule's worktree behind a symlinked .git/modules/dep", "is a symbolic link", func(t *testing.T, repo *Repo) {
			external := t.TempDir()
			writeAt(t, filepath.Join(external, "config"), "[core]\n\tfsmonitor = /bin/true\n")
			plantGitlink(t, repo, "vendor/dep")
			writeAt(t, filepath.Join(repo.Dir, "vendor", "dep", ".git"), "gitdir: ../../.git/modules/dep\n")
			if err := os.MkdirAll(filepath.Join(repo.Dir, ".git", "modules"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, filepath.Join(repo.Dir, ".git", "modules", "dep")); err != nil {
				t.Fatal(err)
			}
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

// A submodule's working directory is refused when its .git file names a git
// directory the checkout's own .git/modules does not hold (F27). clean -ffdx
// never reaches that directory — the path is tracked, so the whole submodule
// directory is left alone — and the configuration git reads from it for the
// submodule goes on being read in the checkout this run works in: the run's own
// git runs the submodule's own status from the top level, so a core.fsmonitor
// the image wrote into that directory is a command of the image's that runs
// there.
//
// The middle case (F29) is the same worktree with the gitlink only in HEAD: the
// index is the image's, so an entry the image dropped from it (update-index
// --force-remove) is one the reset's own reset --hard HEAD puts back — measured:
// the entry is in the index again after that command — and the clean that
// follows leaves the planted directory alone. The paths read for the refusal are
// therefore HEAD's tree's gitlinks as well as the index's, and the row fails
// whenever only the index is read.
//
// The last case is the shape git itself writes: a .git file naming the
// checkout's own .git/modules is accepted, so what is refused is the directory
// the file names and not the file.
//
// Mutation (run, restored): drop the refuseSubmoduleWorktrees call from
// refuseUnresettableBakedGit. Both entry points return nil for the first case —
// "BakedGitOrigin err = <nil>, want a refusal naming ...", and the same for
// ResetBakedGit — so the external git directory outlives the reset. Read the
// index alone (drop the read of HEAD's tree) and the middle case is accepted the
// same way, ResetBakedGit included.
func TestBakedCheckoutResetRefusesASubmoduleWorktreeNamingAnotherGitdir(t *testing.T) {
	for _, tc := range []struct {
		name  string
		want  string
		plant func(t *testing.T, repo *Repo)
	}{
		{"a git directory outside the checkout", "this checkout's own .git/modules does not hold", func(t *testing.T, repo *Repo) {
			plantGitlink(t, repo, "vendor/dep")
			writeAt(t, filepath.Join(repo.Dir, "vendor", "dep", ".git"), "gitdir: "+t.TempDir()+"\n")
		}},
		{"a gitlink HEAD holds and the index does not", "this checkout's own .git/modules does not hold", func(t *testing.T, repo *Repo) {
			plantGitlink(t, repo, "vendor/dep")
			testutil.Git(t, repo.Dir, "commit", "--quiet", "-m", "a submodule the image committed")
			testutil.Git(t, repo.Dir, "update-index", "--force-remove", "vendor/dep")
			writeAt(t, filepath.Join(repo.Dir, "vendor", "dep", ".git"), "gitdir: "+t.TempDir()+"\n")
		}},
		{"the checkout's own .git/modules", "", func(t *testing.T, repo *Repo) {
			plantGitlink(t, repo, "vendor/dep")
			if err := os.MkdirAll(filepath.Join(repo.Dir, ".git", "modules", "dep"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeAt(t, filepath.Join(repo.Dir, "vendor", "dep", ".git"), "gitdir: ../../.git/modules/dep\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, remote := setup(t)
			tc.plant(t, repo)
			_, err := repo.BakedGitOrigin(ctx)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("BakedGitOrigin err = %v, want the checkout accepted", err)
				}
				if err := repo.ResetBakedGit(ctx, remote, filepath.Join(t.TempDir(), "hooks")); err != nil {
					t.Fatalf("ResetBakedGit err = %v, want the checkout accepted", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("BakedGitOrigin err = %v, want a refusal naming %q", err, tc.want)
			}
			if err := repo.ResetBakedGit(ctx, remote, filepath.Join(t.TempDir(), "hooks")); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ResetBakedGit err = %v, want a refusal naming %q", err, tc.want)
			}
		})
	}
}

// plantGitlink records path in the index as a submodule, the way a checkout of
// one does: what makes git leave the directory alone through a clean -ffdx is
// the entry, not the .git file in it.
func plantGitlink(t *testing.T, repo *Repo, path string) {
	t.Helper()
	testutil.Git(t, repo.Dir, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("a", 40)+","+path)
}

// ResetBakedGit leaves the checkout in the state the run works from: a
// configuration holding the origin the run computed and the hook directory it
// owns, and nothing the image's build wrote into it — no push URL, no URL
// rewrite, no remote whose name is a URL, no credential helper, no include of
// another file — with the hooks, the replace refs (loose and packed) and the
// grafts gone, and everything git does not track removed from the tree.
//
// The tracked file the build edited is put back at the commit HEAD names by the
// reset itself: the run's own checkout of the ref it fetched is what puts the
// tree at the fetched state (runner's bootstrap, CheckoutNewBranch), and a
// checkout of that ref cannot restore a file the image's index told git to
// leave alone (the skip-worktree case below).
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
	if edited, err := os.ReadFile(filepath.Join(repo.Dir, "README.md")); err != nil || string(edited) != "hello\n" {
		t.Errorf("README.md = %q (%v): the reset puts a tracked file back at the commit HEAD names", edited, err)
	}
}

// ResetBakedGit puts tracked files back at the commit HEAD names, including the
// ones the build's own checkout state would hide from every later checkout. The
// index the image leaves is also where a skip-worktree bit lives, and git keeps
// the working-tree copy of a file marked that way; the sparse checkout a build
// ran under sets exactly that bit and leaves the files its pattern excluded
// absent from the tree. The configuration the reset writes turns sparse
// checkout off, but the bit is not the configuration's — it goes only with the
// index. The run's own checkout of the fetched ref reads the same bit, so it
// cannot correct either one: the build's edit would sit in the agent's tree,
// and the agent's own edit to that file would never be staged.
//
// Mutation (run, restored): drop the .git/index removal and the reset --hard
// HEAD from ResetBakedGit. README.md still reads the build's edit, and the
// tracked file the build's sparse pattern left out is still missing.
func TestBakedCheckoutResetRestoresASkipWorktreeOrSparseFile(t *testing.T) {
	repo, remote := setup(t)
	writeAt(t, filepath.Join(repo.Dir, "docs", "guide.md"), "the guide\n")
	testutil.Git(t, repo.Dir, "add", "docs/guide.md")
	testutil.Git(t, repo.Dir, "commit", "--quiet", "-m", "a file the build's own commit added")
	testutil.Git(t, repo.Dir, "config", "core.sparseCheckout", "true")
	writeAt(t, filepath.Join(repo.Dir, ".git", "info", "sparse-checkout"), "/README.md\n")
	testutil.Git(t, repo.Dir, "update-index", "--skip-worktree", "README.md")
	writeAt(t, filepath.Join(repo.Dir, "README.md"), "the build edited this\n")
	if err := os.RemoveAll(filepath.Join(repo.Dir, "docs")); err != nil {
		t.Fatal(err)
	}

	if err := repo.ResetBakedGit(ctx, remote, filepath.Join(t.TempDir(), "hooks")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(repo.Dir, "README.md")); err != nil || string(got) != "hello\n" {
		t.Errorf("README.md = %q (%v), want the commit's own %q: the build's edit to a skip-worktree file must not reach the run", got, err, "hello\n")
	}
	if got, err := os.ReadFile(filepath.Join(repo.Dir, "docs", "guide.md")); err != nil || string(got) != "the guide\n" {
		t.Errorf("docs/guide.md = %q (%v): a tracked file the build's sparse checkout left out must be back", got, err)
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
