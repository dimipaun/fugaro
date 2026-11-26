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
	copies := RetiredProjectCopies([]string{"npm-token"}, "acme", true)
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
	copies := RetiredProjectCopies([]string{"npm-token", "pypi-token"}, "acme", true)
	mounted := map[string]bool{gcp.ProjectSecretID("acme", "npm-token"): true}
	got := ProjectCopiesToDelete(copies, mounted, true)
	want := []ProjectCopy{{Name: "pypi-token", ID: gcp.ProjectSecretID("acme", "pypi-token")}}
	if !slices.Equal(got, want) {
		t.Fatalf("deleted = %+v, want only the copy no deployed job names", got)
	}

	// Nothing names one of them: every copy goes.
	if got := ProjectCopiesToDelete(copies, map[string]bool{}, true); len(got) != len(copies) {
		t.Fatalf("deleted = %+v, want every copy when no job names one", got)
	}

	// The read that could not be made is NOT "nothing names it": an empty set
	// the read did answer deletes everything, and the same empty set it did not
	// answer deletes nothing, which is the direction a call that takes a
	// container away needs.
	if got := ProjectCopiesToDelete(copies, nil, false); len(got) != 0 {
		t.Fatalf("deleted = %+v for a read that was never made, want every copy kept", got)
	}

	// And the CLI's one input is that answer, whatever shape the failure has:
	// a refused listing, and a read with no client to make it, both answer no
	// IDs with answered false, so neither can reach a deletion.
	f := newCloud(t)
	spec := InstallationSpec{Project: "proj-1234", FugaroProject: "acme", Region: "us-east5"}
	f.run.AddJob("fugaro-acme-app-web", "1", "512Mi")
	f.run.SetJobSecretRefs("fugaro-acme-app-web", map[string]string{
		"PACKAGE_TOKEN": gcp.ProjectSecretID("acme", "npm-token"),
	})
	f.run.FailListWith = http.StatusForbidden
	ids, answered, warning := DeployedSecretIDs(context.Background(), f.c, spec)
	if len(ids) != 0 || answered || warning == "" {
		t.Fatalf("a refused listing answered %v (answered %v) with warning %q, want no IDs, no answer and the warning", ids, answered, warning)
	}
	if !strings.Contains(warning, "gcloud run jobs list") {
		t.Errorf("the warning names no way to check by hand: %q", warning)
	}
	ids, answered, warning = DeployedSecretIDs(context.Background(), &Clients{}, spec)
	if len(ids) != 0 || answered || warning == "" {
		t.Fatalf("a read with no Run client answered %v (answered %v) with warning %q, want no IDs, no answer and the warning", ids, answered, warning)
	}
	if !strings.Contains(warning, "gcloud run jobs list") {
		t.Errorf("the warning names no way to check by hand: %q", warning)
	}
	// A run whose own context is done is the one case with no line to print —
	// the run is already over — and it is kept alive the same way.
	done, cancel := context.WithCancel(context.Background())
	cancel()
	ids, answered, warning = DeployedSecretIDs(done, f.c, spec)
	if len(ids) != 0 || answered || warning != "" {
		t.Fatalf("an abandoned run answered %v (answered %v) with warning %q, want no IDs, no answer and no line", ids, answered, warning)
	}
	if got := ProjectCopiesToDelete(copies, ids, answered); len(got) != 0 {
		t.Fatalf("deleted = %+v on a run whose own read was abandoned, want every copy kept", got)
	}
}

// TestRetireSkipsAnUncopiedName: only a name whose value the grammar holds —
// IS5's project record, and the intake's own marker on the container — may be
// retired. A project_secret whose owner emptied its allow-list looks exactly
// like a dropped key from the configuration's side, and its project copy is
// what a run still reads: nothing here may touch it.
func TestRetireSkipsAnUncopiedName(t *testing.T) {
	state := projectCopyState()
	copies := RetiredProjectCopies([]string{"npm-token"}, "acme", true)
	if addrs := ProjectCopyRemovals(state, copies); slices.Contains(addrs, projectCopyAddress("bitbucket-token")) {
		t.Fatalf("removals = %q: an uncopied name's copy left the state", addrs)
	}

	// A name the record does not hold is not retired, whether the config
	// derives a container for it or not.
	if got := RetiredProjectCopies(nil, "acme", true); len(got) != 0 {
		t.Fatalf("retired = %+v with nothing copied", got)
	}
	// The same name, held by the record and still produced by the plan the run
	// is about to apply: the release did not drop it here — this installation's
	// own project_secret still declares it — so the run marks nothing, deletes
	// nothing and takes no row out of the state.
	planned := map[string]string{"npm-token": gcp.ProjectSecretID("acme", "npm-token")}
	if got := UnplannedCopies(RetiredProjectCopies([]string{"npm-token"}, "acme", true), planned); len(got) != 0 {
		t.Fatalf("acted on = %+v while the plan still produces the container", got)
	}
	// And the plan discovery hands the removal is not the config's own: a name
	// that reappears there because discovery adopted its container back — the
	// mark could not be written — keeps its row exactly the same, or the apply
	// creates a container the state no longer knows about.
	discovered := map[string]string{"npm-token": gcp.ProjectSecretID("acme", "npm-token")}
	if addrs := ProjectCopyRemovals(state, UnplannedCopies(RetiredProjectCopies([]string{"npm-token"}, "acme", true), discovered)); len(addrs) != 0 {
		t.Fatalf("removals = %q for a name the plan still produces", addrs)
	}
}

// TestRetireConvergesFromPreviousRelease: an installation at the release
// behind converges. It retires nothing there — the switch that says this
// release dropped the keys is off, whatever an operator's own config holds, so
// a copy the config never planned is left as it is rather than retired out from
// under the next operator's update — then, at the release that drops them,
// every copied instance leaves the state in one run, and the next run names
// nothing and calls terraform for nothing.
func TestRetireConvergesFromPreviousRelease(t *testing.T) {
	copied := []string{"npm-token", "pypi-token"}
	if got := RetiredProjectCopies(copied, "acme", false); len(got) != 0 {
		t.Fatalf("retired = %+v at the release behind: nothing is retired before the release drops the keys", got)
	}
	// Not even when the operator's own config plans none of them, which is the
	// reading this step used to make: one operator's config decided retirement,
	// so the next operator's update met a container Fugaro had taken away.
	if got := UnplannedCopies(RetiredProjectCopies(copied, "acme", false), map[string]string{}); len(got) != 0 {
		t.Fatalf("acted on = %+v at the release behind with a config that plans nothing", got)
	}

	// The release that drops them, on the state an installation at the release
	// behind has: both instances, and nothing else of the root's.
	state := append(projectCopyAddresses(copied...), "module.installation.google_cloud_run_v2_job.coordinator[0]")
	copies := RetiredProjectCopies(copied, "acme", true)
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
