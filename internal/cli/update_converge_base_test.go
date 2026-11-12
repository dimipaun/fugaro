package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// TestUpdateConvergesSlimBase is the convergence test CLAUDE.md's HARD RULE
// requires for the base's new contract (image setup skill IS4): the base this
// release publishes carries Claude Code alone, and an installation whose pin
// and whose job images still name the previous base reaches it with
// `fugaro update` alone.
//
// The fixture is the installation between `brew upgrade` and `fugaro update`:
// its config records the release behind's base pin and its workflow records
// were built on it, so doctor fires before the run. The trap is that the old
// base stays pullable — nothing refuses it — so an update that left the pin
// where it is would converge every other check and leave this installation on
// a base no release maintains, still carrying the seven harnesses. Both halves
// are the run's work: re-pin the base, and rebuild the repository's own job
// images on the new pin rather than on the one the records still show.
func TestUpdateConvergesSlimBase(t *testing.T) {
	r := newConvergeRig(t, v4Node())
	// The fixture is the previous release's base, or the assertions after the
	// run prove nothing: an installation already on this release's pin would
	// converge without the images stage touching it.
	prev := pinnedRef(config.BaseKind, convergePrev)
	if got := projectConfigOf(t, r).BaseImages[config.BaseKind]; got != prev {
		t.Fatalf("the fixture records base_images.base = %s, want the previous release's %s", got, prev)
	}

	// The node the previous release published is a version-4 one, so
	// policy-older-than-binary is the check that fires before the run — and it
	// is the policy stage's, which migrates the node in place. The stale base
	// is what the assertions after the run are about: the job images the
	// records show were built on the previous release's base, which is what the
	// images stage has to move.
	convergeOn(t, r, []string{"policy-older-than-binary", "policy-rules-skew", "plugin-pin"})

	// The pin this release's images stage resolves, verifies and records: the
	// one value whose movement is this installation reaching the slim base.
	want := r.pinnedBase(config.BaseKind, convergeRelease)
	if got := projectConfigOf(t, r).BaseImages[config.BaseKind]; got != want {
		t.Errorf("base_images.base = %s after one fugaro update, want this release's %s: the installation is still on the base the previous release published", got, want)
	}
	// And the job images the run rebuilt are on that pin, not on the old one: a
	// re-pin whose builds still pass the previous base would leave this
	// installation's workflows running a base the pin no longer names.
	if len(r.builder.specs) == 0 {
		t.Fatal("the update rebuilt no job image, so nothing here shows a workflow reaching the new base")
	}
	for _, s := range r.builder.specs {
		if s.Base != want {
			t.Errorf("the build of %s on workflow %s passes base %s, want this release's %s", s.Slug, s.Workflow, s.Base, want)
		}
	}
}

// projectConfigOf is the project config the update wrote, parsed: the
// registered project file (registerInstallation) that the update and doctor
// both select in the converge rig, of which the rig's own config file —
// localConfigOf's — is only the seed. Reading the seed back would assert on a
// file this run never writes.
func projectConfigOf(t *testing.T, r *convergeRig) *localcfg.Config {
	t.Helper()
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fugaro", "projects", initProjectName+".yaml")
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatalf("the project config at %s does not parse: %v", path, err)
	}
	return lc
}
