package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/catalog"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
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

// catalogBuild is what a test's fixture says one entry's build record states:
// the epoch the build ran under (0 is a record written before the field
// existed — no epoch stated, which is stale), how long ago it ran, and the
// base it names. The last is carried and never read: a record names a base,
// and a test that puts a release behind's base there is the one that pins the
// run's answer against it (TestReleaseAloneIsNotStale).
type catalogBuild struct {
	base  string
	epoch int
	age   time.Duration
}

// thisReleaseBuild is an entry built under this binary's epoch and recently:
// current on both counts, so the run says nothing about it.
func thisReleaseBuild() catalogBuild {
	return catalogBuild{base: pinnedRef(config.BaseKind, convergeRelease), epoch: imagecheck.BaseEpoch}
}

// releaseBehindBuild is the record the release before VS3a wrote: built FROM
// its own base image and stating no epoch at all. It is release A's eager
// rebuild (D3445), and the release in its base ref is not what makes it stale.
func releaseBehindBuild() catalogBuild {
	return catalogBuild{base: pinnedRef(config.BaseKind, convergePrev)}
}

// oldBuild is an entry built under this binary's epoch, past its rebuild age:
// current and merely old, which is the nightly rebuild's (D3444).
func oldBuild() catalogBuild {
	return catalogBuild{base: pinnedRef(config.BaseKind, convergeRelease), epoch: imagecheck.BaseEpoch, age: config.DefaultMaxAge + time.Hour}
}

// catalogStaleRig is the converge rig in that state, with each entry's build
// record the test passes for it.
//
// The rebuilds are recorded rather than submitted — a hermetic test has no
// Cloud Build account, and the decision is what this file is about — and the
// returned value is the entries the run rebuilt, in order, with the options
// each rebuild was handed. The recorder is guarded: four rebuilds run at once,
// and one test drives that.
func catalogStaleRig(t *testing.T, server, worker catalogBuild) (*convergeRig, *[]string, *[]imageBuildOptions) {
	t.Helper()
	useSelf(t)
	r := newConvergeRig(t, currentNode())
	r.appendConfig(t, catalogStaleYAML)
	// The config the run selects is the canonical copy under projects/, and
	// the checkout names the project that has one there (localcfg's selection,
	// step 3): the append reaches the run through the copy, not through the
	// rig's own file.
	registerInstallation(t, r.initRig)
	// The guard `fugaro update` runs before its first stage
	// (update_catalog_guard.go) reads each entry's repository, so the catalog
	// build's provider seam points at a fake repository holding both
	// definitions: the two are the entries' own and ask for no secret, so the
	// guard has nothing to say about this catalog and the tests below are about
	// the images stage alone.
	stubCatalogCredential(t)
	stubCatalogProviderState(t, fake.State{Files: map[string]string{
		"images/server.Dockerfile": "FROM example.invalid/base\nRUN true\n",
		"images/worker.Dockerfile": "FROM example.invalid/base\nRUN true\n",
	}})
	putCatalogRecord(t, mustSlug("github", "acme/app"), "server", server)
	putCatalogRecord(t, mustSlug("bitbucket", "acme/sandbox"), "worker", worker)
	var mu sync.Mutex
	var built []string
	var opts []imageBuildOptions
	prev := catalogRebuild
	catalogRebuild = func(_ *cobra.Command, entry string, o imageBuildOptions) error {
		mu.Lock()
		defer mu.Unlock()
		built = append(built, entry)
		opts = append(opts, o)
		return nil
	}
	t.Cleanup(func() { catalogRebuild = prev })
	return r, &built, &opts
}

// putCatalogRecord stores one entry's build record in the bucket this rig's
// records are read through, as the fixture states it.
func putCatalogRecord(t *testing.T, slug, entry string, b catalogBuild) {
	t.Helper()
	key, err := gcp.CatalogRecordKey(slug, entry)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	if b.age > 0 {
		at = at.Add(-b.age)
	}
	rec, err := json.Marshal(imagecheck.CatalogRecordState{
		Version: imagecheck.CatalogRecordVersion, Entry: entry,
		DefinitionSHA256: strings.Repeat("b", 64),
		Epoch:            b.epoch,
		BaseRef:          b.base, BaseDigest: "sha256:" + strings.Repeat("c", 64),
		BuiltAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	records[key] = string(rec)
}

// TestUpdateRebuildsTheStaleCatalogImageOfThisCheckout: an installation whose
// catalog entry of the checkout the run is in carries a build record from
// before the epoch existed — the state every entry is in on the release that
// introduces it (D3445) — reaches this release with `fugaro update` alone: the
// run rebuilds it here, waits for it, says so, and leaves nothing for the user.
//
// This is the owner's failure: before the images stage read the catalog's
// records, this run printed "No changes" over an entry whose image the
// launcher refuses, because the entry's build is a nightly job's and nothing
// in this run built one.
func TestUpdateRebuildsTheStaleCatalogImageOfThisCheckout(t *testing.T) {
	_, built, _ := catalogStaleRig(t, releaseBehindBuild(), thisReleaseBuild())

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("an entry whose record states no epoch does not reach this release with `fugaro update` alone (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if got := strings.Join(*built, ","); got != "server" {
		t.Errorf("the run rebuilt %q, want the stale entry of this checkout alone: `worker` needs no rebuild", got)
	}
	if want := "rebuilt 1 catalog image(s) FROM this release's base image: server"; !strings.Contains(out, want) {
		t.Errorf("the run does not say it rebuilt the entry:\n%s\nwant %q in it", out, want)
	}
	if strings.Contains(out, "not covered") {
		t.Errorf("a run that rebuilt the only stale entry of its own checkout left a step behind:\n%s", out)
	}
}

// TestUpdateNamesTheStaleCatalogImageOfAnotherCheckout: an entry behind this
// release's base image whose build belongs to another repository of the
// installation cannot be built from this checkout — a catalog build runs as the build account of the repository it is
// submitted from, with that repository's credential and secrets — so the run
// must NOT pass it over in silence and must not claim the installation is
// done: it prints the entry, the checkout and the exact command, and it ends
// not finished (exit 1).
func TestUpdateNamesTheStaleCatalogImageOfAnotherCheckout(t *testing.T) {
	_, built, _ := catalogStaleRig(t, thisReleaseBuild(), releaseBehindBuild())

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("a run that did not rebuild a catalog image behind this release's base reported success:\n%s", out)
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
		// The step's reason, on the line under it: the stage applied what it
		// could, so its own line is a success line and not one — the catalog
		// half sets this itself (catalogLeftFor), and the loop takes no reason
		// from an applying stage's detail.
		"    not covered: 1 catalog image(s) this run did not rebuild",
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
	_, built, _ := catalogStaleRig(t, thisReleaseBuild(), thisReleaseBuild())

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
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

// TestUpdateJSONDoesNotReachTheCatalogRebuild: a `fugaro update --json` reaches
// no catalog rebuild at all, and that is what keeps --json out of the nested
// build's own session. The run's first step that a person's presence confirms is
// the installation stage's write, and --json confirms no step (T11,
// docs/no-prompt-policy), so the run stops there and nothing is submitted.
//
// The flag must not reach the build: handed on it becomes the build's own
// Conditions.JSON, which takes the session out of judgedHere
// (PersonAtTheTerminal) — and openBuildManaged then returns nil, so the build
// would be submitted with no kill-switch read, no project build rules and no
// admission record, under its lease alone. imagesStage.rebuildCatalogEntry
// builds the nested options from scratch, so the flag cannot ride along on the
// struct; what this pins is that the command offers no route that carries it
// there (`fugaro image build` never gets there either: runImageBuild refuses
// --json for a Cloud Build before this machinery is reached).
func TestUpdateJSONDoesNotReachTheCatalogRebuild(t *testing.T) {
	_, built, opts := catalogStaleRig(t, releaseBehindBuild(), thisReleaseBuild())

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update", "--json")
	if err == nil {
		t.Fatalf("a --json run applied a step no word confirmed (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if len(*built) != 0 || len(*opts) != 0 {
		t.Fatalf("a --json update submitted the catalog rebuild(s) %q, though it reaches no stage a word confirms:\n%s\n%s", *built, out, errOut)
	}
	if all := out + errOut + newestLog(t, "update"); !strings.Contains(all, "not confirmed (--json confirms no step") {
		t.Errorf("the run does not name --json as the reason it stopped before the stages:\n%s", all)
	}
}

// TestUpdateSaysWhyACatalogRebuildDidNotFinish: a rebuild the stage submitted
// and that was refused is named with its reason in the step left for the person,
// not only in the log. Without it the run ended on "run fugaro image build go,
// then fugaro update again", a command that fails the same way and says nothing
// about what to change.
func TestUpdateSaysWhyACatalogRebuildDidNotFinish(t *testing.T) {
	_, _, _ = catalogStaleRig(t, oldBuild(), thisReleaseBuild())
	catalogRebuild = func(_ *cobra.Command, entry string, _ imageBuildOptions) error {
		return errors.New("the catalog entry " + entry + ": line 31 of its Dockerfile asks for a secret")
	}

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("a run whose rebuild was refused reported success:\n%s", out)
	}
	want := "server (acme/app's checkout): its rebuild did not finish (the catalog entry server: line 31 of its Dockerfile asks for a secret)"
	if all := out + errOut; !strings.Contains(all, "left for you (images, command): ") || !strings.Contains(all, want) {
		t.Errorf("the step left for the person does not carry the refusal %q:\n%s", want, all)
	}
}

// TestUpdateOffersNoCommandThatWouldRefuse: a rebuild that ends on the entry's
// OWN definition being refused leaves the refusal and no command.
//
// The run has already run the command — `fugaro image build <entry>` IS what
// the stage submits, and the resolve and the validation of the definition are
// its first stage — so a definition it watched refuse is one the person's own
// `fugaro image build server` refuses the same way, on the same bytes at the
// same pinned ref: offering it is handing the reader a step that fails and
// says nothing about the line to change. What the step says instead is the
// refusal's own sentence, which names the line, and the change it asks for.
//
// The counterpart is TestUpdateSaysWhyACatalogRebuildDidNotFinish's failure
// (a rebuild that did not finish and did not refuse): its command IS printed,
// because that run's failure is this run's (a lease, a quota, a read) and the
// command may well work where this run's did not.
func TestUpdateOffersNoCommandThatWouldRefuse(t *testing.T) {
	const refusal = "the catalog entry server: line 31 of its Dockerfile asks for a secret; a catalog entry carries no secret of its own — the clone step supplies the checkout — so remove it (read from acme/app at legacy)"
	_, _, _ = catalogStaleRig(t, oldBuild(), thisReleaseBuild())
	catalogRebuild = func(_ *cobra.Command, entry string, _ imageBuildOptions) error {
		if entry != "server" {
			t.Fatalf("the run rebuilt %q, want the stale entry of this checkout alone", entry)
		}
		return catalog.Refused(errors.New(refusal))
	}

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("a run whose entry's definition was refused reported success:\n%s", out)
	}
	if got := ExitCode(err); got != 1 {
		t.Errorf("exit %d, want 1: the entry is still on an older base and the step is the person's:\n%s\n%s", got, out, errOut)
	}
	all := out + errOut
	for _, want := range []string{
		"left for you (images, command): ",
		"server (acme/app's checkout): " + refusal,
		"Change what the refusals name, then fugaro update again",
		// The line in flight carries the refusal too, and no command either.
		"server: NOT rebuilt: " + refusal,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the run does not say %q:\n%s", want, all)
		}
	}
	for _, unwanted := range []string{
		"image build server",
		"Run each command below",
		"its rebuild did not finish",
	} {
		if strings.Contains(all, unwanted) {
			t.Errorf("the run offers %q for a definition its own rebuild watched refuse:\n%s", unwanted, all)
		}
	}
}
