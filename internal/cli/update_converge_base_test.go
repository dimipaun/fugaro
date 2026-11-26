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
// a base no release maintains, still carrying the seven harnesses. What the
// run's work is here is the re-pin, and since M7 it is the whole of it: this
// repository is admitted by the profile the same run writes for its provider
// (S5's default), so it runs on the project's shared job — there is no
// repository-level image left on the previous base to rebuild, and the job
// images the records show on it belong to a derived image nothing runs any
// more (M7, D2312 as amended; spec decision 3).
func TestUpdateConvergesSlimBase(t *testing.T) {
	// The node is the one this binary writes, and deliberately: this test is
	// about the base image, and a node one version behind would take doctor's
	// policy checks out of the run (doctorPolicyChecks returns the skew line
	// alone), so policy-vs-jobs — the check this fixture exists to fire — would
	// never be appended and the guard below would fail for a reason that has
	// nothing to do with the base.
	r := newConvergeRig(t, staleNode(t))
	// The fixture is the previous release's base, or the assertions after the
	// run prove nothing: an installation already on this release's pin would
	// converge without the images stage touching it.
	prev := pinnedRef(config.BaseKind, convergePrev)
	if got := projectConfigOf(t, r).BaseImages[config.BaseKind]; got != prev {
		t.Fatalf("the fixture records base_images.base = %s, want the previous release's %s", got, prev)
	}

	// The stale base is what the assertions after the run are about: the job
	// images the records show were built on the previous release's base, which
	// is what the images stage has to move.
	out := convergeOn(t, r, []string{"policy-rules-skew", "repo-plugin-pin"})

	// The pin this release's images stage resolves, verifies and records: the
	// one value whose movement is this installation reaching the slim base.
	want := r.pinnedBase(config.BaseKind, convergeRelease)
	if got := projectConfigOf(t, r).BaseImages[config.BaseKind]; got != want {
		t.Errorf("base_images.base = %s after one fugaro update, want this release's %s: the installation is still on the base the previous release published", got, want)
	}
	// And the pin is not moved beside a build still on the old base: the run
	// builds nothing for this repository at all, which is the same fact the
	// assertion above rests on and not a second, weaker one. What the
	// repository's workflows run is the project's shared job, and the project's
	// own images are built FROM base_images.base — so a build that still passed
	// the previous base could only be one of a repository-level job this
	// installation no longer launches.
	if n := len(r.builder.specs); n != 0 {
		t.Errorf("%d job image(s) reached Cloud Build, want none: the repository is admitted by the project's profile, so it has no job image of its own to move to %s\n%s", n, want, out)
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
