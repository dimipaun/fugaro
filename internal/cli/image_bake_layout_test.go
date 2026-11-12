package cli

import (
	"regexp"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/imagebake"
	"github.com/dimipaun/fugaro/internal/runner"
)

// baseWorkDir is the directory images/base/Dockerfile leaves a job's shell in:
// the last WORKDIR the file sets, which is the one a `docker run` and a Cloud
// Run job start in.
var baseWorkDir = regexp.MustCompile(`(?m)^WORKDIR[ \t]+(\S+)[ \t]*$`)

// TestBakedRootIsTheJobWorkDirectorysSibling: a bake writes the checkout into
// the image at internal/imagebake's Root, and a run looks for it beside its own
// work directory (internal/runner's BakedDir). The two are one path only while
// a job's work directory is the one its container starts in — the base image's
// WORKDIR, which is also `fugaro exec`'s --workdir default, because a job's CMD
// is a bare `fugaro exec` and passes no flag. Each of the three is in a
// different file, and none of the three packages can check itself against the
// others, so this is the one place the layout is read whole.
//
// What it catches is a silent failure. Move the base image's WORKDIR and every
// other test stays green — the runner's own tests derive the baked path from
// the temporary work directory they hand it, which moves both ends together —
// and every job of every baked image stops finding the checkout the image paid
// to clone and clones again at start, with nothing said.
func TestBakedRootIsTheJobWorkDirectorysSibling(t *testing.T) {
	slug := mustSlug(gitprov.KindGitHub, "acme/app")
	baked, err := imagebake.Path(slug)
	if err != nil {
		t.Fatal(err)
	}
	// The image's own path and the runner's derivation, over the work directory
	// a job actually starts in.
	if got := runner.BakedDir(defaultExecWorkDir, slug); got != baked {
		t.Errorf("a run whose work directory is %s opens %q, and the bake step writes %q: those are one checkout only while the two derivations agree", defaultExecWorkDir, got, baked)
	}
	// The third end: the work directory a job's container is actually in. The
	// job's CMD names no --workdir, so this value and the flag's default are
	// the same directory or no job of a baked image opens what was baked.
	m := baseWorkDir.FindAllStringSubmatch(readRepoFile(t, "images/base/Dockerfile"), -1)
	if len(m) == 0 {
		t.Fatal("images/base/Dockerfile sets no WORKDIR, so nothing says where a job's container starts")
	}
	if got := m[len(m)-1][1]; got != defaultExecWorkDir {
		t.Errorf("the base image starts a job in %s and fugaro exec's --workdir default is %s: a job of a baked image finds no checkout beside any other directory, and clones at start instead", got, defaultExecWorkDir)
	}
}
