package infra

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// projectCopyState is the state of an installation the migration reaches: the
// project-wide container of one workflow secret IS5 copied, one of a
// project_secret whose owner emptied its allow-list, and one resource of the
// root that has nothing to do with any of it.
func projectCopyState() []string {
	return []string{
		projectCopyAddress("npm-token"),
		projectCopyAddress("bitbucket-token"),
		"module.installation.google_secret_manager_secret.credentials_bundle[0]",
	}
}

func projectCopyAddresses(names ...string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, projectCopyAddress(n))
	}
	return out
}

// TestProjectCopyLeavesStateBeforeApply is IS13's red step: the release that
// drops a workflow secret's key makes the plan destructions of the instance
// `for_each = var.project_secrets` still holds, and the module's
// google_secret_manager_secret.project carries prevent_destroy — so terraform
// refuses that plan before Fugaro's own guard, the apply, or any other code of
// this program runs. The state removal is what makes the plan runnable, and it
// has to happen before the plan, once, for exactly that instance.
func TestProjectCopyLeavesStateBeforeApply(t *testing.T) {
	state := projectCopyState()
	// The copied name the configuration no longer derives a container for:
	// planned is the module's for_each, which is what the plan is made from.
	copies := RetiredProjectCopies([]string{"npm-token"}, map[string]string{}, "acme")
	if len(copies) != 1 || copies[0].Name != "npm-token" {
		t.Fatalf("retired = %+v, want the one copied name", copies)
	}
	if want := gcp.ProjectSecretID("acme", "npm-token"); copies[0].ID != want {
		t.Fatalf("the copy is container %s, want the installation's own %s", copies[0].ID, want)
	}
	addrs := ProjectCopyRemovals(state, copies)
	want := []string{`module.installation.google_secret_manager_secret.project["npm-token"]`}
	if !slices.Equal(addrs, want) {
		t.Fatalf("removals = %q, want exactly %q: the instance the for_each made, and no other row of the state", addrs, want)
	}

	// The call terraform is handed, and the state the plan is then made from:
	// the instance is gone from it, which is the whole of what prevent_destroy
	// needs to stop refusing.
	var called []string
	stateRm := func(_ context.Context, got ...string) error {
		if len(got) != 1 {
			t.Errorf("state rm called with %d addresses at once (%q); the removal is per-address", len(got), got)
		}
		called = append(called, got...)
		return nil
	}
	removed, gone, err := RemoveFromState(context.Background(), stateRm, addrs)
	if err != nil || len(gone) != 0 {
		t.Fatalf("removing %q: removed %q, gone %q, err %v", addrs, removed, gone, err)
	}
	if !slices.Equal(called, want) {
		t.Fatalf("state rm was called with %q, want %q", called, want)
	}
	after := slices.DeleteFunc(slices.Clone(state), func(a string) bool { return slices.Contains(removed, a) })
	if slices.Contains(after, want[0]) {
		t.Fatalf("the state the plan is made from still holds %s, and the module's prevent_destroy refuses that plan", want[0])
	}
}

// TestRetiredCopyDeletedOnlyWhenUnreferenced is G4: the state removal destroys
// nothing, and the container is deleted only on a run whose read found no
// deployed job naming it. A job still mounting the copy is a run of the release
// behind — its template names the container — so the copy stays alive for it.
func TestRetiredCopyDeletedOnlyWhenUnreferenced(t *testing.T) {
	copies := RetiredProjectCopies([]string{"npm-token", "pypi-token"}, map[string]string{}, "acme")
	mounted := map[string]bool{gcp.ProjectSecretID("acme", "npm-token"): true}
	got := ProjectCopiesToDelete(copies, mounted)
	want := []ProjectCopy{{Name: "pypi-token", ID: gcp.ProjectSecretID("acme", "pypi-token")}}
	if !slices.Equal(got, want) {
		t.Fatalf("deleted = %+v, want only the copy no deployed job names", got)
	}

	// Nothing names one of them: every copy goes.
	if got := ProjectCopiesToDelete(copies, map[string]bool{}); len(got) != len(copies) {
		t.Fatalf("deleted = %+v, want every copy when no job names one", got)
	}

	// The read that could not be made is NOT "nothing names it": the CLI warns
	// (DeployedSecretIDs' empty set comes with its warning) and deletes
	// nothing, which is the direction a call that takes a container away needs.
	f := newCloud(t)
	spec := InstallationSpec{Project: "proj-1234", FugaroProject: "acme", Region: "us-east5"}
	f.run.AddJob("fugaro-acme-app-web", "1", "512Mi")
	f.run.SetJobSecretRefs("fugaro-acme-app-web", map[string]string{
		"PACKAGE_TOKEN": gcp.ProjectSecretID("acme", "npm-token"),
	})
	f.run.FailListWith = http.StatusForbidden
	ids, warning := DeployedSecretIDs(context.Background(), f.c, spec)
	if len(ids) != 0 || warning == "" {
		t.Fatalf("a refused listing answered %v with warning %q, want no IDs and the warning", ids, warning)
	}
	if !strings.Contains(warning, "gcloud run jobs list") {
		t.Errorf("the warning names no way to check by hand: %q", warning)
	}
}

// TestRetireSkipsAnUncopiedName: only a name whose value the grammar holds —
// IS5's project record, and the intake's own marker on the container — may be
// retired. A project_secret whose owner emptied its allow-list looks exactly
// like a dropped key from the configuration's side, and its project copy is
// what a run still reads: nothing here may touch it.
func TestRetireSkipsAnUncopiedName(t *testing.T) {
	state := projectCopyState()
	copies := RetiredProjectCopies([]string{"npm-token"}, map[string]string{}, "acme")
	if addrs := ProjectCopyRemovals(state, copies); slices.Contains(addrs, projectCopyAddress("bitbucket-token")) {
		t.Fatalf("removals = %q: an uncopied name's copy left the state", addrs)
	}

	// A name the record does not hold is not retired, whether the config
	// derives a container for it or not.
	if got := RetiredProjectCopies(nil, map[string]string{}, "acme"); len(got) != 0 {
		t.Fatalf("retired = %+v with nothing copied", got)
	}
	// The same name, once its value IS in the grammar but while the config
	// still derives the container: the release that drops the key has not run
	// yet, so the copy stays and the step is inert.
	planned := map[string]string{"bitbucket-token": gcp.ProjectSecretID("acme", "bitbucket-token")}
	if got := RetiredProjectCopies([]string{"bitbucket-token"}, planned, "acme"); len(got) != 0 {
		t.Fatalf("retired = %+v while the configuration still produces the container", got)
	}
}

// TestRetireConvergesFromPreviousRelease: an installation at the release
// behind converges. It retires nothing there — every name IS5 copied is still
// planned, which is what "inert until IS6 drops the keys" means — then, at the
// release that drops them, every copied instance leaves the state in one run,
// and the next run names nothing and calls terraform for nothing.
func TestRetireConvergesFromPreviousRelease(t *testing.T) {
	copied := []string{"npm-token", "pypi-token"}
	planned := map[string]string{
		"npm-token":  gcp.ProjectSecretID("acme", "npm-token"),
		"pypi-token": gcp.ProjectSecretID("acme", "pypi-token"),
	}
	if got := RetiredProjectCopies(copied, planned, "acme"); len(got) != 0 {
		t.Fatalf("retired = %+v at the release behind: nothing is retired before the configuration drops the key", got)
	}

	// The release that drops them, on the state an installation at the release
	// behind has: both instances, and nothing else of the root's.
	state := append(projectCopyAddresses(copied...), "module.installation.google_cloud_run_v2_job.coordinator[0]")
	copies := RetiredProjectCopies(copied, map[string]string{}, "acme")
	addrs := ProjectCopyRemovals(state, copies)
	if want := projectCopyAddresses("npm-token", "pypi-token"); !slices.Equal(addrs, want) {
		t.Fatalf("removals = %q, want %q", addrs, want)
	}
	var called []string
	stateRm := func(_ context.Context, got ...string) error {
		called = append(called, got...)
		return nil
	}
	removed, _, err := RemoveFromState(context.Background(), stateRm, addrs)
	if err != nil || !slices.Equal(called, addrs) {
		t.Fatalf("removing %q: called %q, err %v", addrs, called, err)
	}

	// The converge, on the state the removal left: the instances are already
	// gone, so the next run of the same command names nothing and calls
	// terraform for nothing.
	left := slices.DeleteFunc(slices.Clone(state), func(a string) bool { return slices.Contains(removed, a) })
	if addrs := ProjectCopyRemovals(left, copies); len(addrs) != 0 {
		t.Fatalf("the second run still removes %q", addrs)
	}
	// And an installation that never created the container — a first apply
	// that stopped before it — names nothing either: the removal is driven by
	// the state, not by the retired set.
	if addrs := ProjectCopyRemovals([]string{"module.installation.google_cloud_run_v2_job.coordinator[0]"}, copies); len(addrs) != 0 {
		t.Fatalf("removals = %q for a state that never held a copy", addrs)
	}
}
