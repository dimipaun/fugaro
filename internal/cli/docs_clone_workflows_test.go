package cli

import (
	"strings"
	"testing"
)

// TestChecklistHasCheck38: docs/gcp-live-checklist.md's Check 38 is the
// owner-run live check for checkout: clone (design generic-tool.md G4, plan
// Tasks 16 to 18). Since 0.7.5 the check must be runnable as written: the
// release's runner reads FUGARO_CHECKOUT and clones blobless, cloneSince is
// "0.7.5" and the sandbox installation's base_images.base is already that
// release image, so the ONLY build the check makes is a local binary from a
// scratch checkout of origin/main with cloneShipped set to true by hand. The
// stale version of this section made the owner build and push a dev base
// image (about 1.5 GB), rewrite base_images.base and delete it again; none of
// that is needed, and a doc that still says so sends the owner to a registry
// write this check does not want. The assertions below pin both halves: the
// shape every owner-run check in this file keeps, and the absence of the old
// step 0's procedure.
func TestChecklistHasCheck38(t *testing.T) {
	const path = "docs/gcp-live-checklist.md"
	doc := readDoc(t, path)
	section := docSection(t, path, doc, "## Check 38")

	// The shape: user-run, not run, sandbox only, never belong.
	for _, want := range []string{"USER-RUN, NOT RUN", "run by you", "sandbox", "Never `belong`"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section does not say %q", want)
		}
	}

	// The guard block, re-run at the start of every step: the project is
	// hard-coded, every fugaro command names its project, doctor is the
	// installation check and the checkout's origin is the sandbox.
	for _, want := range []string{
		"RE-RUN THIS WHOLE BLOCK", "P=fugaro-dev", "${P:?}",
		"fugaro doctor --project fugaro", "--project fugaro --gcp-project",
		"edgeappinc/fugarosandbox",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's guard block does not carry %q", want)
		}
	}

	// The recorded state: taken once, before the first change, and a leftover
	// record from an earlier attempt refuses the run (the rule Checks 50 and
	// 58 use).
	for _, want := range []string{"check38-state", "left over", "before the first change"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's recorded state does not say %q", want)
		}
	}

	// The one build: the local binary, the flag set by hand, never pushed.
	for _, want := range []string{"cloneShipped", "go build", "check38-bin", "never committed", "scratch checkout of `origin/main`"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's one build does not say %q", want)
		}
	}

	// The base image is verified, not built: read out of the installation,
	// judged against cloneSince, and fixed with the release binary if it is
	// older.
	for _, want := range []string{"base_images.base", "cloneSince", "fugaro init --base", "fugaro image refresh"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Check 38 section's base-image step does not name %q", want)
		}
	}

	// With a release base image at cloneSince the gate warns about nothing,
	// and the section has to say so: the old expectation ("not a release base
	// image", once in step 1 and once per launch) belonged to the dev base
	// image that is gone.
	if !strings.Contains(section, "no gate warning") {
		t.Error("the Check 38 section does not say that a release base image at cloneSince draws no gate warning")
	}

	// The old step 0, gone: no dev base image, no docker, no registry write,
	// no billable storage, no base-image rewrite.
	for _, banned := range []string{
		"images/build-base.sh", "docker push", "docker images delete",
		"configure-docker", "--base-image", "dev-<commit>", "1.5 GB",
	} {
		if strings.Contains(section, banned) {
			t.Errorf("the Check 38 section still carries the old step 0's %q", banned)
		}
	}
}
