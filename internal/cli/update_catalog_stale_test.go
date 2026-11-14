package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/spf13/cobra"
)

// The gap these tests pin is the one the owner met twice on 2026-11-14, after
// 0.24.0: `fugaro update` said "No changes" and every launch was refused,
// because the images stage pins the base and copies the history image and
// rebuilds nothing FROM the base it recorded. The catalog's entries are the
// images a catalog project's jobs run (D1900), and the stage now rebuilds the
// ones of the checkout it runs in and names the ones only another checkout can
// build.
//
// The fixture is the converge rig — a real checkout of acme/app at the release
// behind, the fakes the update's other tests use — with a catalog whose two
// entries are built in two different repositories of the installation:
// `server` in acme/app, the checkout this run is in, and `worker` in
// acme/sandbox, which no checkout here is. Which of the two the run rebuilds
// is what each test varies, and nothing is submitted to a Cloud Build: the
// submission is image_build_catalog_test.go's, and what these tests drive is
// the stage's decision.

// catalogStaleYAML is the catalog of an installation whose entries are built
// in two of its repositories. The two are the rig's own: acme/app, the
// checkout the run is in, and acme/sandbox, a repository of the project that
// no checkout on this machine is.
const catalogStaleYAML = `image_catalog:
  entries:
    - name: server
      source: { kind: dockerfile, repo: acme/app, path: images/server.Dockerfile }
    - name: worker
      source: { kind: dockerfile, repo: acme/sandbox, path: images/worker.Dockerfile }
`

// catalogStaleRig is the converge rig in that state, with each entry's build
// record saying it was built FROM the base image the test passes: the release
// behind's (stale), this release's (current), or anything else.
//
// The rebuilds are recorded rather than submitted — a hermetic test has no
// Cloud Build account, and the decision is what this file is about — and the
// returned value is the entries the run rebuilt, in order, with the options
// each rebuild was handed.
func catalogStaleRig(t *testing.T, serverBase, workerBase string) (*convergeRig, *[]string, *[]imageBuildOptions) {
	t.Helper()
	useSelf(t)
	r := newConvergeRig(t, v7Node())
	r.appendConfig(t, catalogStaleYAML)
	// The config the run selects is the canonical copy under projects/, and
	// the checkout names the project that has one there (localcfg's selection,
	// step 3): the append reaches the run through the copy, not through the
	// rig's own file.
	registerInstallation(t, r.initRig)
	putCatalogRecord(t, mustSlug("github", "acme/app"), "server", serverBase)
	putCatalogRecord(t, mustSlug("bitbucket", "acme/sandbox"), "worker", workerBase)
	var built []string
	var opts []imageBuildOptions
	prev := catalogRebuild
	catalogRebuild = func(_ *cobra.Command, entry string, o imageBuildOptions) error {
		built = append(built, entry)
		opts = append(opts, o)
		return nil
	}
	t.Cleanup(func() { catalogRebuild = prev })
	return r, &built, &opts
}

// putCatalogRecord stores one entry's build record, built FROM base, in the
// bucket this rig's records are read through.
func putCatalogRecord(t *testing.T, slug, entry, base string) {
	t.Helper()
	key, err := gcp.CatalogRecordKey(slug, entry)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := json.Marshal(imagecheck.CatalogRecordState{
		Version: imagecheck.CatalogRecordVersion, Entry: entry,
		DefinitionSHA256: strings.Repeat("b", 64),
		BaseRef:          base, BaseDigest: "sha256:" + strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	records[key] = string(rec)
}

// TestUpdateRebuildsTheStaleCatalogImageOfThisCheckout: an installation whose
// catalog entry of the checkout the run is in was last built FROM the release
// behind's base image reaches this release with `fugaro update` alone — the
// run rebuilds it, says so, and leaves nothing for the user.
//
// This is the owner's failure: before the images stage read the catalog's
// records, this run printed "No changes" over an entry whose image the
// launcher refuses, because the entry's build is a nightly job's and nothing
// in this run built one.
func TestUpdateRebuildsTheStaleCatalogImageOfThisCheckout(t *testing.T) {
	_, built, _ := catalogStaleRig(t, pinnedRef(config.BaseKind, convergePrev), pinnedRef(config.BaseKind, convergeRelease))

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("a catalog entry on the previous release's base image does not reach this release with `fugaro update` alone (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if got := strings.Join(*built, ","); got != "server" {
		t.Errorf("the run rebuilt %q, want the stale entry of this checkout alone: `worker` is on this release's base", got)
	}
	if want := "rebuilt 1 catalog image(s) FROM this release's base image: server"; !strings.Contains(out, want) {
		t.Errorf("the run does not say it rebuilt the entry:\n%s\nwant %q in it", out, want)
	}
	if strings.Contains(out, "not covered") {
		t.Errorf("a run that rebuilt the only stale entry of its own checkout left a step behind:\n%s", out)
	}
}

// TestUpdateNamesTheStaleCatalogImageOfAnotherCheckout: an entry whose build
// belongs to another repository of the installation cannot be built from this
// checkout — a catalog build runs as the build account of the repository it is
// submitted from, with that repository's credential and secrets — so the run
// must NOT pass it over in silence and must not claim the installation is
// done: it prints the entry, the checkout and the exact command, and it ends
// not finished (exit 1).
func TestUpdateNamesTheStaleCatalogImageOfAnotherCheckout(t *testing.T) {
	_, built, _ := catalogStaleRig(t, pinnedRef(config.BaseKind, convergeRelease), pinnedRef(config.BaseKind, convergePrev))

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err == nil {
		t.Fatalf("a run that did not rebuild a stale catalog image of this installation reported success:\n%s", out)
	}
	if got := ExitCode(err); got != 1 {
		t.Errorf("exit %d, want 1: the run leaves a step for the user and must not read as done:\n%s\n%s", got, out, errOut)
	}
	if len(*built) != 0 {
		t.Errorf("the run rebuilt %q from a checkout that is not the entry's own: a catalog build needs that repository's credential and secrets", *built)
	}
	all := out + errOut
	for _, want := range []string{
		// One line per entry, naming the entry, the checkout its build runs
		// as and the exact command that rebuilds it there.
		"worker: NOT rebuilt: its build runs as acme/sandbox's build account, with that repository's credential and secrets; run fugaro image build worker in acme/sandbox's checkout",
		// And the run does not end on a success claim: it says what it did
		// not cover, and it leaves the command as the step for the user.
		"not covered: 1 catalog image(s)",
		"left for you (images, command): this run did not rebuild 1 catalog image(s)",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the run does not name %q, so the entry is passed over in silence:\n%s", want, all)
		}
	}
	// The stage's own line and no other: the installation has fifteen other
	// stages, and "No changes" from one of them is that stage's business. A
	// marker, the stage's name and its detail is what the converge prints, so
	// this can only match the images stage's.
	if strings.Contains(all, "images: No changes") {
		t.Errorf("the images stage reported nothing to do while a catalog image of this installation is on an older base:\n%s", all)
	}
}

// TestUpdateSaysNothingAboutACatalogOnThisReleaseBase: the other half of the
// rule — an installation whose entries were both built FROM this release's
// base image is not touched at all. A run that rebuilt or named one of them
// would be one rebuilding images that are current, which is a billable build
// the user did not ask for.
func TestUpdateSaysNothingAboutACatalogOnThisReleaseBase(t *testing.T) {
	_, built, _ := catalogStaleRig(t, pinnedRef(config.BaseKind, convergeRelease), pinnedRef(config.BaseKind, convergeRelease))

	out, errOut, err := executeStdin(t, "", "update", "--yes")
	if err != nil {
		t.Fatalf("a catalog on this release's base image is not an update's business, and this run failed over it (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if len(*built) != 0 {
		t.Errorf("the run rebuilt %q, want nothing: both entries are on this release's base image", *built)
	}
	all := out + errOut
	for _, unwanted := range []string{"catalog image(s) FROM this release's base image", "not covered"} {
		if strings.Contains(all, unwanted) {
			t.Errorf("the run says %q about a catalog that is current:\n%s", unwanted, all)
		}
	}
}

// TestUpdateJSONDoesNotReachTheCatalogRebuild: --json is the update's own flag,
// the document this run prints, and it is NOT the nested build's session. Handed
// on it becomes the build's own Conditions.JSON, which takes the session out of
// judgedHere (PersonAtTheTerminal) — and openBuildManaged then returns nil, so
// the build is submitted with no kill-switch read, no project build rules and no
// admission record, under its lease alone. That is the bound the stage's own
// comment claims, gone in exactly the session where nobody is watching it
// (`fugaro image build` never gets there: runImageBuild refuses --json for a
// Cloud Build before this machinery is reached).
func TestUpdateJSONDoesNotReachTheCatalogRebuild(t *testing.T) {
	_, built, opts := catalogStaleRig(t, pinnedRef(config.BaseKind, convergePrev), pinnedRef(config.BaseKind, convergeRelease))

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err != nil {
		t.Fatalf("a catalog entry on the previous release's base image does not reach this release with `fugaro update --json` (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if len(*built) != 1 {
		t.Fatalf("the run rebuilt %q, want the stale entry of this checkout alone:\n%s\n%s", *built, out, errOut)
	}
	if (*opts)[0].asJSON {
		t.Errorf("the rebuild was handed --json, which the nested build reads as its own session: the managed gate is not read for that one, so the build would be submitted with no kill-switch read, no project build rules and no admission record")
	}
}
