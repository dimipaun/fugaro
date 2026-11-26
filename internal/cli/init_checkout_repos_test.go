package cli

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// U4's tests: the checkout stage reads the project's repositories (contract
// C1) and runs the cloud step for every one of them it may build, from that
// repository's own fugaro.yaml and head commit — never from the directory the
// run was started in (R6, R10). The reader is stubbed (stubEnrolledRepos,
// update_repos_test.go), because what the stage does with the views is what
// these tests are about; the views themselves are read from real checkouts on
// disk, at their default branches, which is what the reader answers with.

// viewFrom is one repository as the reader answers it: its default branch's
// fugaro.yaml, parsed, and that branch's head commit. The checkout it is read
// from is not the working directory the run is started in.
func viewFrom(t *testing.T, dir, repo string) repoView {
	t.Helper()
	ctx := context.Background()
	db := defaultBranchParse(ctx, dir)
	if db.ref == "" || !db.read || db.cfg == nil {
		t.Fatalf("%s could not be read at its default branch: %+v", dir, db)
	}
	return repoView{Repo: repo, Provider: "github", Branch: strings.TrimPrefix(db.ref, "origin/"),
		Head: headOfDefaultBranch(ctx, dir, db.ref), YAML: db.data, Cfg: db.cfg}
}

// TestCheckoutStageRefreshesEveryStaleRepository: the stage runs the cloud step
// for every repository of the project the machine may build, and it does so
// wherever the run was started — the working directory is not read (R6, R10).
// The repositories' views come from their own default branches, so this run
// refreshes images no checkout of the run's directory holds.
//
// The red step of U4 is this test's premise: while the stage called
// pluginwire.Locate(".") it skipped here, a directory that is no checkout
// having no job images to refresh.
func TestCheckoutStageRefreshesEveryStaleRepository(t *testing.T) {
	r, app := upgradeRig(t)
	web := repoCheckout(t, "https://github.com/acme/web.git", refreshYAML)
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		return []repoView{viewFrom(t, app, "acme/app"), viewFrom(t, web, "acme/web")}, nil
	})
	t.Chdir(t.TempDir())
	ran := fakeSteps(t, "", nil)

	e, out := checkoutEngine(t, &initOptions{yes: true})
	// This machine's own config, as a run reads it: the stage builds from it
	// (the refresh's installation spec is made from the run's config), and it
	// lists both repositories, which is what lets the stage build them (V1).
	e.lc, e.path, e.old = rigConfig(t, r, "acme/app", "acme/web")
	st := checkoutStageOf(t, e)
	res := stageOutcome(t, e, st)
	if res.Failed != nil || len(res.Left) != 0 {
		t.Fatalf("the run did not finish: failed %+v, left %+v\n%s", res.Failed, res.Left, out)
	}
	if strings.Contains(out.String(), "no repository of this project was read") {
		t.Fatalf("the stage read the directory it was started in rather than the project's repositories:\n%s", out)
	}
	if got := stateOf(res, checkoutStageName); got != initflow.Changed {
		t.Errorf("the checkout stage is %q, want %q: both repositories had images to move\n%s", got, initflow.Changed, out)
	}
	// The refresh's own steps, once per repository, in the order the project's
	// list holds them (canonical owner/name).
	if want := []string{"base", "check job", "builds", "base", "check job", "builds"}; !slices.Equal(*ran, want) {
		t.Errorf("the refresh ran %q, want %q: every repository of the project is refreshed from its own default branch\n%s", *ran, want, out)
	}
	for _, want := range []string{"cloud: done: acme/app:", "cloud: done: acme/web:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the run does not say %q:\n%s", want, out)
		}
	}
	// And the run's own account of them, which the loop carries into its result
	// and its event (RepoLines, U13).
	lines := st.(*checkoutStage).RepoLines()
	if len(lines) != 2 || lines[0].Repo != "acme/app" || lines[1].Repo != "acme/web" {
		t.Fatalf("the stage reported %+v, want one line per repository in canonical order", lines)
	}
	for _, l := range lines {
		if l.State != initflow.RepoDone {
			t.Errorf("%s is reported as %q, want %q: its images were refreshed\n%s", l.Repo, l.State, initflow.RepoDone, out)
		}
	}
}

// rigConfig is the rig's own local config as a run reads it, with the
// repositories named enrolled in this machine's terms (a provider each): the
// refresh's installation spec is built from the run's config, so a stage handed
// a config the rig never wrote is one no refresh can plan from.
func rigConfig(t *testing.T, r *anchorModeRig, repos ...string) (*localcfg.Config, string, []byte) {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range repos {
		lc.Repos[repo] = localcfg.Repo{Provider: "github"}
	}
	return lc, r.cfg, nil
}

// TestCheckoutStageNamesARepositoryItCannotBuild: a repository the project
// lists and this machine has no local config entry for is one this stage cannot
// build — the region, the registry host and the image names are read from that
// entry — so it is named, with the one step that changes it, and nothing is
// built for it (V1, D3350, R20).
func TestCheckoutStageNamesARepositoryItCannotBuild(t *testing.T) {
	upgradeRig(t)
	beta := repoCheckout(t, "https://github.com/beta/web.git", refreshYAML)
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		return []repoView{viewFrom(t, beta, "beta/web")}, nil
	})
	t.Chdir(t.TempDir())
	ran := fakeSteps(t, "", nil)

	e, out := checkoutEngine(t, &initOptions{yes: true})
	st := checkoutStageOf(t, e)
	res := stageOutcome(t, e, st)
	if res.Failed != nil || len(*ran) != 0 {
		t.Fatalf("the stage built for a repository this machine cannot build: failed %+v, steps %q\n%s", res.Failed, *ran, out)
	}
	lines := st.(*checkoutStage).RepoLines()
	if len(lines) != 1 || lines[0].Repo != "beta/web" || lines[0].State != initflow.RepoNeedsYou {
		t.Fatalf("the stage reported %+v, want one needs-you line naming beta/web", lines)
	}
	if !strings.Contains(lines[0].Detail, "no local config entry") || !strings.Contains(lines[0].Detail, selfCommand()+" update --repo") {
		t.Errorf("the line does not say what is missing and what to run: %q", lines[0].Detail)
	}
	if got := stateOf(res, checkoutStageName); got != initflow.Skipped {
		t.Errorf("the checkout stage is %q, want %q: there is nothing here it may build\n%s", got, initflow.Skipped, out)
	}
	if !strings.Contains(out.String(), "beta/web: needs you: ") {
		t.Errorf("the run does not name the repository it cannot build:\n%s", out)
	}
}

// TestCheckoutStageReportsARepositoryItCannotRead: a repository the reader
// could not read is never planned from (R13) — the stage reports the reader's
// own reason, and builds nothing for it.
func TestCheckoutStageReportsARepositoryItCannotRead(t *testing.T) {
	upgradeRig(t)
	gamma := repoCheckout(t, "https://github.com/acme/gamma.git", "")
	stubEnrolledRepos(t, func(context.Context, enrolledIn) ([]repoView, []string) {
		v := viewFromRepoOnly(t, gamma, "acme/gamma")
		return []repoView{v}, nil
	})
	t.Chdir(t.TempDir())
	ran := fakeSteps(t, "", nil)

	e, out := checkoutEngine(t, &initOptions{yes: true})
	st := checkoutStageOf(t, e)
	res := stageOutcome(t, e, st)
	if res.Failed != nil || len(*ran) != 0 {
		t.Fatalf("the stage planned from a repository it could not read: failed %+v, steps %q\n%s", res.Failed, *ran, out)
	}
	lines := st.(*checkoutStage).RepoLines()
	if len(lines) != 1 || lines[0].State != initflow.RepoUnread {
		t.Fatalf("the stage reported %+v, want one unread line", lines)
	}
	if !strings.Contains(lines[0].Detail, "no fugaro.yaml on the default branch") {
		t.Errorf("the line does not carry the reader's own reason: %q", lines[0].Detail)
	}
}

// TestRefreshManagedGateOpensFromTheView: a refresh for a repository the run
// read from the project (refreshOptions.view) opens the managed gate from the
// view's own selection of this machine's config, never from the directory the
// run was started in — which, outside a checkout, holds no marks to read and is
// not what the plan was made against (U4, R6, R10).
//
// The mutation caught: openRefreshManaged reaching o.checkoutDir() for the
// installation. The gate then stays shut wherever the update was started
// outside a checkout, and a managed project's base pin, check-job config and
// job move ask for the typed project name — or are refused — depending on that
// directory.
func TestRefreshManagedGateOpensFromTheView(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newManagedRefreshRig(t, managedRefreshMarker(1))
	// Where the update was started: no checkout, and no config a door that
	// reads the working directory could find in one (R10).
	t.Chdir(t.TempDir())
	t.Setenv("FUGARO_CONFIG", "")

	rv := &refreshView{
		v:      repoView{Repo: "acme/app", Provider: "github", Cfg: r.tg.cfg},
		lc:     r.tg.lc,
		lcPath: r.cfg,
	}
	m := openRefreshManaged(t.Context(), gateCmd(), refreshOptions{view: rv},
		&initOptions{cloud: cloudOptions{stderr: func() io.Writer { return io.Discard }}})
	if m == nil {
		t.Fatal("the gate's door stayed shut for a repository the caller read from the project, though both marks are in place: the door was opened from the directory this run was started in, which is not a checkout")
	}
	if m.lc != rv.lc {
		t.Errorf("the gate read another installation than the view's own: the plan would be judged against one and applied to another (refreshManaged.lc)")
	}
}

// viewFromRepoOnly is the reader's other shape: a repository whose default
// branch holds no fugaro.yaml, so it answers unread with the reason and nothing
// else (R13).
func viewFromRepoOnly(t *testing.T, dir, repo string) repoView {
	t.Helper()
	db := defaultBranchParse(context.Background(), dir)
	if db.read {
		t.Fatalf("%s holds a fugaro.yaml on its default branch, so the fixture proves nothing", dir)
	}
	return repoView{Repo: repo, Provider: "github", Unread: "no fugaro.yaml on the default branch"}
}
