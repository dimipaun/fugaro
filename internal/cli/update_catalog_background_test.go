package cli

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/spf13/cobra"
)

// laterStepRan says whether a step the update runs after the images stage has
// written its marker.
func laterStepRan(r *convergeRig) bool {
	return r.db.Value(rules.PathOrchestratorRulesMarker()) != nil
}

// sawLaterStep waits up to d for a step after the images stage to write its
// marker, and says whether it did. A build the stage waits for never sees one:
// the step that writes it cannot start until the build returns.
func sawLaterStep(r *convergeRig, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if laterStepRan(r) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestUpdateRunsItsOtherStepsWhileACatalogImageBuilds: the images stage
// submits the entry that is past its rebuild age with --no-wait and returns,
// the run's later steps run while that build goes on, and the run ends without
// waiting for it — nothing of this run joins a background build any more
// (D3446, F27's joinCatalogBuilds).
func TestUpdateRunsItsOtherStepsWhileACatalogImageBuilds(t *testing.T) {
	r, built, opts := catalogStaleRig(t, oldBuild(), thisReleaseBuild())

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("the update did not converge (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if got := strings.Join(*built, ","); got != "server" {
		t.Errorf("the run submitted %q, want the entry past its rebuild age of this checkout alone", got)
	}
	if len(*opts) != 1 || !(*opts)[0].noWait {
		t.Errorf("the run submitted the background rebuild with %v, want --no-wait: nobody waits for this build", *opts)
	}
	if !laterStepRan(r) {
		t.Errorf("the run's later steps did not run:\n%s", out)
	}
	if want := "submitted 1 catalog image(s) to rebuild in the background: server"; !strings.Contains(out, want) {
		t.Errorf("the run does not say what it left running:\n%s\nwant %q", out, want)
	}
	for _, unwanted := range []string{"not covered", "waiting for"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a run that submitted a background rebuild says %q:\n%s", unwanted, out)
		}
	}
}

// TestUpdateWaitsOnlyOnEpoch: an entry whose record states another epoch — or
// none at all — is rebuilt here and waited for, because the image is on a base
// the launcher may refuse: the run's later steps do not start until the
// rebuild returns, and the run says it rebuilt the entry rather than submitting
// it (D3445, D3446). The entry past its age alone is submitted and not waited
// for, in the same run.
func TestUpdateWaitsOnlyOnEpoch(t *testing.T) {
	r, built, opts := catalogPoolRig(t, releaseBehindBuild(), oldBuild())

	var mu sync.Mutex
	sawLater := map[bool]bool{}
	prev := catalogRebuild
	catalogRebuild = func(cmd *cobra.Command, entry string, o imageBuildOptions) error {
		if !o.noWait {
			later := sawLaterStep(r, 2*time.Second)
			mu.Lock()
			sawLater[o.noWait] = later
			mu.Unlock()
		}
		return prev(cmd, entry, o)
	}
	t.Cleanup(func() { catalogRebuild = prev })

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("the update did not converge (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if got := strings.Join(*built, ","); got != "app0,app1" {
		t.Errorf("the run submitted %q, want the epoch-stale entry and the one past its age", got)
	}
	if !strings.Contains(out, "rebuilt 1 catalog image(s) FROM this release's base image: app0") {
		t.Errorf("the run does not say it rebuilt the epoch-stale entry here:\n%s", out)
	}
	var background int
	for _, o := range *opts {
		if o.noWait {
			background++
		}
	}
	if background != 1 {
		t.Errorf("%d of %d rebuild(s) were submitted in the background, want 1: only the entry past its age", background, len(*opts))
	}
	if sawLater[false] {
		t.Errorf("a later step ran while the eager rebuild was in flight: the images stage did not wait for it")
	}
}

// TestReleaseAloneIsNotStale: an entry whose record states this binary's epoch
// and was built recently is not stale, whatever release its base ref names —
// the release behind's included. Nothing is rebuilt and nothing is submitted:
// a release no longer makes an image stale (D3446).
func TestReleaseAloneIsNotStale(t *testing.T) {
	behind := thisReleaseBuild()
	behind.base = pinnedRef(config.BaseKind, convergePrev)
	_, built, opts := catalogStaleRig(t, behind, behind)

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("an installation on the release behind's base image did not converge (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if len(*built) != 0 || len(*opts) != 0 {
		t.Errorf("the run rebuilt or submitted %q, want nothing: the epoch is this binary's and the builds are recent", *built)
	}
	for _, unwanted := range []string{"catalog image(s) FROM this release's base image", "rebuild in the background", "not covered"} {
		if strings.Contains(out+errOut, unwanted) {
			t.Errorf("the run says %q about a catalog that is current:\n%s", unwanted, out+errOut)
		}
	}
}

// TestCatalogBuildsFourAtOnce: five stale entries are submitted four at a time
// (D3444, D3446). The fifth waits for a slot rather than starting beside the
// four — an installation's whole catalog is not one build after another, and
// it is not fifteen Cloud Builds at once either.
func TestCatalogBuildsFourAtOnce(t *testing.T) {
	const entries = 5
	states := make([]catalogBuild, entries)
	for i := range states {
		states[i] = oldBuild()
	}
	_, built, _ := catalogPoolRig(t, states...)

	var mu sync.Mutex
	inFlight, most := 0, 0
	arrived := make(chan struct{}, entries)
	release := make(chan struct{})
	prev := catalogRebuild
	catalogRebuild = func(cmd *cobra.Command, entry string, o imageBuildOptions) error {
		mu.Lock()
		inFlight++
		if inFlight > most {
			most = inFlight
		}
		mu.Unlock()
		arrived <- struct{}{}
		<-release
		mu.Lock()
		inFlight--
		mu.Unlock()
		return prev(cmd, entry, o)
	}
	t.Cleanup(func() { catalogRebuild = prev })

	fakeTerminal(t)
	done := make(chan error, 1)
	go func() {
		_, _, err := executeStdin(t, "", "update")
		done <- err
	}()
	// Four arrive and none of them returns, so a fifth beside them would be a
	// fifth at once. A pool that ran them one at a time never reaches four.
	for i := 0; i < catalogRebuildAtOnce; i++ {
		select {
		case <-arrived:
		case <-time.After(60 * time.Second):
			close(release)
			t.Fatalf("%d rebuild(s) reached the build in 60s: the stage does not submit four at a time", i)
		}
	}
	mu.Lock()
	atFour := inFlight
	mu.Unlock()
	if atFour != catalogRebuildAtOnce {
		t.Errorf("%d rebuild(s) in flight once %d had arrived, want %d", atFour, catalogRebuildAtOnce, catalogRebuildAtOnce)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("the update did not converge (exit %d): %v", ExitCode(err), err)
	}
	mu.Lock()
	defer mu.Unlock()
	if most != catalogRebuildAtOnce {
		t.Errorf("%d rebuild(s) ran at once, want %d", most, catalogRebuildAtOnce)
	}
	if len(*built) != entries {
		t.Errorf("the run submitted %d rebuild(s), want %d", len(*built), entries)
	}
}

// TestEagerBuildFailureFailsStep: an eager rebuild that fails fails the images
// step, in one line naming the entry. The person or agent reruns `fugaro
// update`, which is the step the loop leaves: the entry is on a runner the
// launcher may refuse, and it is never left silently behind (D3446).
//
// The entry past its rebuild age beside it is NOT submitted: an eager failure
// stops the background submissions of the same run, so a run about to fail
// leaves no build of its own running (catalogOutcome's early return). Nothing
// else would catch that: without the return this run would start a billable
// build on its way out.
func TestEagerBuildFailureFailsStep(t *testing.T) {
	const reason = "Cloud Build ended FAILURE for app0"
	_, _, _ = catalogPoolRig(t, releaseBehindBuild(), oldBuild())
	var mu sync.Mutex
	var opts []imageBuildOptions
	catalogRebuild = func(_ *cobra.Command, _ string, o imageBuildOptions) error {
		mu.Lock()
		opts = append(opts, o)
		mu.Unlock()
		if o.noWait {
			return nil
		}
		return errors.New(reason)
	}

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err == nil {
		t.Fatalf("a run whose eager rebuild failed reported success:\n%s", out)
	}
	all := out + errOut
	if want := "app0: the rebuild from this release's base image did not finish (" + reason + ")"; !strings.Contains(all, want) {
		t.Errorf("the run does not name the entry and its failure in one line:\n%s\nwant %q", all, want)
	}
	if !strings.Contains(all, "fugaro update again") {
		t.Errorf("the run does not say what the person does next:\n%s", all)
	}
	for _, unwanted := range []string{"left for you (images, command)", "not covered", "submitted"} {
		if strings.Contains(all, unwanted) {
			t.Errorf("a failed eager rebuild says %q:\n%s", unwanted, all)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(opts) != 1 {
		t.Errorf("the run attempted %d rebuild(s), want the eager one alone: an eager failure stops the background submissions of the same run", len(opts))
	}
	for _, o := range opts {
		if o.noWait {
			t.Errorf("the run submitted a background build after its eager rebuild failed")
		}
	}
}

// TestPreviousReleaseConverges: an installation that last ran `update` on the
// release before converges on this one. Its catalog's records are the ones
// that release wrote — no epoch stated, its own base named — and the epoch is
// what rebuilds them, eagerly, once (D3445).
func TestPreviousReleaseConverges(t *testing.T) {
	_, built, opts := catalogStaleRig(t, releaseBehindBuild(), thisReleaseBuild())

	fakeTerminal(t)
	out, errOut, err := executeStdin(t, "", "update")
	if err != nil {
		t.Fatalf("an installation from the release before did not converge (exit %d):\n%s\n%s", ExitCode(err), out, errOut)
	}
	if got := strings.Join(*built, ","); got != "server" {
		t.Errorf("the run rebuilt %q, want the entry of this checkout — `worker`'s build belongs to another checkout", got)
	}
	for _, o := range *opts {
		if o.noWait {
			t.Errorf("the run submitted a record that states no epoch with --no-wait: release A's rebuild is the one it waits for")
		}
	}
	if !strings.Contains(out, "rebuilt 1 catalog image(s) FROM this release's base image: server") {
		t.Errorf("the run does not say it rebuilt the entry the release before left:\n%s", out)
	}
}

// catalogPoolRig is catalogStaleRig's fixture with one entry per state, every
// one of them in acme/app — the checkout this run is in — so that all of them
// are this run's to rebuild. Which of them is eager and which is past its age
// is the test's to say; an entry of another checkout would only be left
// behind, and the pool is about what this run submits itself.
func catalogPoolRig(t *testing.T, states ...catalogBuild) (*convergeRig, *[]string, *[]imageBuildOptions) {
	t.Helper()
	useSelf(t)
	r := newConvergeRig(t, currentNode())
	var yaml strings.Builder
	yaml.WriteString("image_catalog:\n  entries:\n")
	files := map[string]string{}
	for i := range states {
		name := fmt.Sprintf("app%d", i)
		fmt.Fprintf(&yaml, "    - name: %s\n      source: { kind: dockerfile, repo: acme/app, path: images/%s.Dockerfile }\n", name, name)
		files["images/"+name+".Dockerfile"] = "FROM example.invalid/base\nRUN true\n"
	}
	r.appendConfig(t, yaml.String())
	registerInstallation(t, r.initRig)
	stubCatalogCredential(t)
	stubCatalogProviderState(t, fake.State{Files: files})
	for i, b := range states {
		putCatalogRecord(t, mustSlug("github", "acme/app"), fmt.Sprintf("app%d", i), b)
	}
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
