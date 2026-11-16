//go:build terraform

package infra

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// reportsAddresses are the six resources the reports job is, as
// modules/installation/reports.tf names them: the account, its two conditioned
// bucket grants (runs/ read-only, reports/ read-write), the job, the
// Scheduler's invoker grant on that job, and the schedule.
var reportsAddresses = []string{
	"module.installation.google_service_account.reports[0]",
	"module.installation.google_storage_bucket_iam_member.reports_read[0]",
	"module.installation.google_storage_bucket_iam_member.reports_write[0]",
	"module.installation.google_cloud_run_v2_job.reports[0]",
	"module.installation.google_cloud_run_v2_job_iam_member.reports_invoker[0]",
	"module.installation.google_cloud_scheduler_job.reports[0]",
}

// catalogCheckAddresses are the widget-check job's own four, which the same
// update keeps: the job, its invoker grant, its schedule and the account they
// belong to. The 0.18.0 release deployed them, so an update that replaced them
// would stop a schedule this release never meant to touch.
var catalogCheckAddresses = []string{
	"module.installation.google_service_account.catalog_check[0]",
	"module.installation.google_cloud_run_v2_job.catalog_check[0]",
	"module.installation.google_cloud_run_v2_job_iam_member.catalog_check_invoker[0]",
	"module.installation.google_cloud_scheduler_job.catalog_check[0]",
}

// TestInstallationAtThePreviousVersionConvergesToTheReportsJob is the HARD
// RULE's own convergence claim, planned for real: an installation at the
// release before this one — the tfvars that release wrote, which carry no
// reports node — converges in ONE `fugaro update`, with the job created paused
// and everything the previous release's state holds kept and unchanged. No
// second command, no deletion and no manual repair: the update's plan is the
// previous plan plus six resources, and the second update is the first one
// again, so a schedule nobody started stays stopped.
//
// The other job is half of the claim, because the setting is one change and
// the owner's answer named both: the catalog check job and its schedule are
// kept, not replaced, and they are as paused after the update as before it.
//
// The types are the second half, and they are why this update asks for no new
// row in the cover tables: every resource the job adds is of a type the
// previous plan already declares — the catalog check's own job, invoker grant,
// schedule and account — so what the review reads is six more rows of a shape
// it has already admitted, and not a kind of resource nobody classified.
func TestInstallationAtThePreviousVersionConvergesToTheReportsJob(t *testing.T) {
	update, err := Installation(parseLC(t, catalogCheckInstallLC(false)), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if update.CatalogCheck == nil || update.Reports == nil {
		t.Fatalf("the fixture plans catalog check %v and reports %v, want both: this test is about the two jobs of one installation", update.CatalogCheck != nil, update.Reports != nil)
	}
	// The previous release planned this installation with no reports node at
	// all: R12's module is there (its lifecycle rule and its null variable),
	// and nothing wrote the variable, so the module created nothing of it.
	previous := update
	previous.Reports = nil

	before := installationPlanFor(t, previous)
	after := installationPlanFor(t, update)
	if len(before) == 0 || len(after) == 0 {
		t.Fatal("a plan came back empty")
	}

	// The fixture is the release before this one: the job is nowhere in its
	// plan, and the catalog check's own four are.
	for _, addr := range reportsAddresses {
		if _, ok := before[addr]; ok {
			t.Fatalf("the previous release's plan already holds %s: the fixture is not the release before this one", addr)
		}
	}
	for _, addr := range catalogCheckAddresses {
		if _, ok := before[addr]; !ok {
			t.Fatalf("the previous release's plan holds no %s: the fixture is not an installation the 0.18.0 job was deployed to", addr)
		}
	}

	// One update, and the whole of what it adds is the job.
	added := missingFrom(slices.Sorted(maps.Keys(after)), slices.Sorted(maps.Keys(before)))
	if want := slices.Sorted(slices.Values(reportsAddresses)); !slices.Equal(added, want) {
		t.Errorf("the update adds %v, want the reports job's own %v", added, want)
	}
	types := map[string]bool{}
	for _, r := range before {
		types[r.Type] = true
	}
	for _, addr := range reportsAddresses {
		r, ok := after[addr]
		if !ok {
			t.Errorf("the update plans no %s: the job cannot run, or cannot write the report it exists for", addr)
			continue
		}
		if !slices.Equal(r.Actions, []string{"create"}) {
			t.Errorf("the update plans %s as %v, want a create: one update converges on the job, and a replacement is what a second command would be needed for", addr, r.Actions)
		}
		if !types[r.Type] {
			t.Errorf("the update adds %s of type %s, which the previous plan never declared: the cover tables would need a row for it, and a plan is what the review reads", addr, r.Type)
		}
	}

	// The job ships stopped, and so does the job that was already there: the
	// setting is the project layer's, and a run whose layer says nothing leaves
	// both schedules alone.
	for _, addr := range []string{
		"module.installation.google_cloud_scheduler_job.reports[0]",
		"module.installation.google_cloud_scheduler_job.catalog_check[0]",
	} {
		if got := after[addr].After["paused"]; got != true {
			t.Errorf("%s plans paused = %v, want true: nothing here is enabled by default, and a `gcloud scheduler jobs resume` is reverted by the next update", addr, got)
		}
	}

	// Kept, not replaced: everything the previous release's plan holds is in
	// the update's plan, with the same values. A resource that left the plan
	// is a resource the apply would destroy, which is the operator step this
	// update may not need.
	for _, addr := range slices.Sorted(maps.Keys(before)) {
		got, ok := after[addr]
		if !ok {
			t.Errorf("the update no longer plans %s: the previous release's state would be destroyed rather than added to", addr)
			continue
		}
		if a, b := before[addr].json(), got.json(); a != b {
			t.Errorf("the update changes %s:\n before %s\n after  %s", addr, a, b)
		}
	}

	// The second update is the first one again: the same run of `fugaro update`
	// with the layer untouched writes the same plan, so a schedule nobody
	// started stays stopped and nothing about the job moves on its own.
	again := installationPlanFor(t, update)
	if len(again) != len(after) {
		t.Fatalf("the second update plans %d resources, the first %d", len(again), len(after))
	}
	for _, addr := range slices.Sorted(maps.Keys(after)) {
		if a, b := after[addr].json(), again[addr].json(); a != b {
			t.Errorf("the second update changes %s:\n first %s\n again %s", addr, a, b)
		}
	}

	// The job's own assignment reaches the container as an environment
	// variable and not as a mount: what it must read to write a report is in
	// the plan's env block, and it holds no secret to fetch.
	job := after["module.installation.google_cloud_run_v2_job.reports[0]"]
	if !strings.Contains(job.json(), "FUGARO_REPORTS_JOB_SPEC") {
		t.Error("the job's container carries no FUGARO_REPORTS_JOB_SPEC: a job deployed without it can only fail, once a night, unnoticed until the alert fires")
	}
}
