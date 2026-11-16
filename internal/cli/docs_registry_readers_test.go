package cli

// C6 of docs/forge-authorship-images, as re-pointed by the owner's answer to
// D651 (docs/forge-authorship-images/reconciliation-image-setup.md §5): the
// images a repository's jobs actually run are the catalog's, so the sentence
// "an image holds its source" is written about those and about a catalog
// entry's bake, not about the per-repository derived image the catalog
// retires (D1900, IS1/IS2).
//
// The three claims pinned here are the ones a reader of SECURITY.md cannot
// check from the document: the source an image carries (read against the bake
// step's own root constant, so the path in the document is the path the code
// writes), who can read it once it is pushed (the shared registry's grants),
// and that image builds are Cloud Build cost the run's budget never sees
// (D621). The context-repository half is pinned as what the mechanism is —
// a read-only clone for one run, never baked — and says in the document that
// it is not built yet, because a security document may not describe an
// unbuilt mechanism as one that is running.
//
// The red step is this file's first run: the section is not written yet.

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/imagebake"
)

// imageSourceHeading is the section SECURITY.md states this in: a "###" of
// "Trust model and known limits", beside the shared key's own section.
const imageSourceHeading = "### The source an image carries, and who can read it"

// TestDocsRegistryReaders: an image a job runs carries source, whoever holds
// pull on the registry it was pushed to reads it, every job that runs it can
// read it too, and a context repository is never baked.
func TestDocsRegistryReaders(t *testing.T) {
	doc := readDoc(t, "SECURITY.md")
	section := flattenDoc(docSection(t, "SECURITY.md", doc, imageSourceHeading))
	for _, want := range []string{
		// What an image carries, and that the path is the bake step's own:
		// imagebake.Root is what a generated definition writes the checkout
		// to, and the document may not name another directory than that.
		"bakes that repository's checkout",
		"at the commit it was built from",
		"`/work/repo`",
		imagebake.Root + "/<slug>",
		// The switch that turns the bake on, by the name the policy gives it.
		"`bake_repos`",
		// Who reads it. The registry is the installation's one shared one
		// (deploy/terraform/gcp/modules/installation/registry.tf), and its
		// readers are the grants modules/repo/registry.tf writes: the build
		// account and the repository's job accounts.
		"Whoever holds pull on the registry the image sits in reads that source",
		"each repository's build account and each repository's job accounts",
		"every job that runs the image can read it too",
		// Not only the run's own repository, and nothing in the run takes the
		// others away today (docs/shared-jobs-default's D2309 is not built).
		"the run removes none of the checkouts it does not work on",
		// And what the layout does bound: the run works on its own checkout.
		"another repository's baked checkout is readable",
		// A context repository is not baked: one run's read-only clone, and
		// the document says the mechanism is not built yet.
		"context repository is never baked",
		"clone for one run alone",
		"not built yet",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SECURITY.md's %q section never says %q: a reader cannot check what source an image carries, or who reads it, from the document", imageSourceHeading, want)
		}
	}
	// The other repositories' checkouts are not pinned to a commit, which is
	// the one thing a build cannot do for a repository it does not run for.
	// A document claiming otherwise would be wrong in the direction that
	// flatters the product, so the limit is pinned rather than left out.
	for _, want := range []string{
		"the head of its branch when the build ran",
		"the build's own",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SECURITY.md's %q section never says %q: the entry's other repositories bake their branch's head at build time and not a pinned commit, and that limit is part of the claim", imageSourceHeading, want)
		}
	}
}

// TestDocsBuildsOutsideRunBudget: an image build is Cloud Build cost, outside
// the run budget (D621) — the run's caps count model calls, and a build
// happens before the run that pulls it starts.
func TestDocsBuildsOutsideRunBudget(t *testing.T) {
	doc := readDoc(t, "SECURITY.md")
	section := flattenDoc(docSection(t, "SECURITY.md", doc, imageSourceHeading))
	for _, want := range []string{
		"Cloud Build",
		"outside the run budget",
		"`budget.per_run_usd`",
		"before the run that pulls it starts",
		"D621",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("SECURITY.md's %q section never says %q: a build is unmetered by the run's caps, and this is where a reader learns it", imageSourceHeading, want)
		}
	}
}
