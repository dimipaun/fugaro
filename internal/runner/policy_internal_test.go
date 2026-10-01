package runner

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// The default branch is fetched and read once per run: a later call gets
// the first answer, so the project check and the policy see the same file
// and the agent can't move it between them.
func TestDefaultBranchFileReadOnce(t *testing.T) {
	testutil.IsolateGit(t)
	files := testutil.FixtureFiles(t)
	remote := testutil.NewRemote(t, files)
	repo, err := gitops.OpenOrClone(context.Background(), filepath.Join(t.TempDir(), "w"), remote, gitops.WithVars(gitops.IdentityEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	r := &run{repo: repo}
	ctx := context.Background()
	first, def, err := r.defaultBranchFile(ctx)
	if err != nil || def != "main" || string(first) != files["fugaro.yaml"] {
		t.Fatalf("%q, %q, %v", first, def, err)
	}
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", remote, seed)
	testutil.WriteFiles(t, seed, map[string]string{"fugaro.yaml": files["fugaro.yaml"] + "# moved\n"})
	testutil.Git(t, seed, "commit", "--quiet", "-am", "move")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	second, _, err := r.defaultBranchFile(ctx)
	if err != nil || string(second) != string(first) {
		t.Fatalf("second read differs: %q, %v", second, err)
	}
}
