package batch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// TestNewReaperRefusesTheProjectsOutOfBounds: the GCP projects the plan puts
// out of bounds are refused BY NAME, in NewReaper, so the refusal is on every
// path that can produce a project -- a project config's gcp_project and the
// reaper job's own environment alike. It is a refusal, not a filter: a pass
// that names one of them never opens a client, so it can read nothing and
// delete nothing.
//
// The two are belong's: the installation the plan puts out of bounds, under
// both the id it was created with and the one it answers to today. The list is
// matched case-insensitively (a project id is not case-sensitive to Google's
// own API, so `Edge-Devel-Dimi` is the same project) and EXACTLY (a project
// whose id merely begins with one of the two is another project, and a reaper
// that refused it would be refusing a project nobody named).
func TestNewReaperRefusesTheProjectsOutOfBounds(t *testing.T) {
	for _, project := range []string{
		"edge-devel-dimi",
		"fugaro-belong",
		"EDGE-DEVEL-DIMI",
		"Fugaro-Belong",
		// The environment path is handed its value with no validation of its
		// own (a project config's gcp_project does get one, localcfg's
		// GCPProjectRE), so a space is a spelling of the same project and must
		// not be a way around the list.
		" edge-devel-dimi ",
	} {
		t.Run(project, func(t *testing.T) {
			bf := gcpfake.NewBatch(t)
			r, err := NewReaper(context.Background(), ReaperOptions{
				GCPProject: project, Region: "us-east5",
				Timeouts:  testTimeouts{longest: time.Minute},
				Endpoints: Endpoints{Batch: bf.URL + "/", NoAuth: true},
			})
			if err == nil {
				t.Fatalf("NewReaper accepted GCP project %q: a pass over it would sweep a project the plan puts out of bounds", project)
			}
			if r != nil {
				t.Fatal("NewReaper returned a reaper along with the refusal")
			}
			for _, want := range []string{project, "edge-devel-dimi", "fugaro-belong"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal = %q, want it to name %q: the rule is what makes the refusal actionable", err, want)
				}
			}
		})
	}

	// Exact, not a prefix: these are other projects, and refusing them would
	// be refusing a project nobody named.
	for _, project := range []string{"edge-devel-dimi-staging", "my-fugaro-belong", "belong"} {
		t.Run("allowed/"+project, func(t *testing.T) {
			bf := gcpfake.NewBatch(t)
			if _, err := NewReaper(context.Background(), ReaperOptions{
				GCPProject: project, Region: "us-east5",
				Timeouts:  testTimeouts{longest: time.Minute},
				Endpoints: Endpoints{Batch: bf.URL + "/", NoAuth: true},
			}); err != nil {
				t.Errorf("NewReaper refused %q: the list is exact, and this is another project (%v)", project, err)
			}
		})
	}
}

// TestNewReaperRefusesTheProjectsTheCallerNames: the floor above is not the
// whole of the rule, and this is the other half of it -- a caller names the
// projects ITS installations must never sweep (ReaperOptions.
// OutOfBoundsProjects, which internal/cli fills from
// FUGARO_REAPER_REFUSE_PROJECTS on both of its paths), and the refusal is the
// same one, in the same place, with the same force: it happens in NewReaper,
// before a client exists, so a project named this way is one no pass can read
// or delete in.
//
// The matching is the floor's: trimmed (a caller's list is text, and
// " my-project " is a spelling of the same project), case-insensitive (a
// project id is not case-sensitive to Google's own API) and exact (a project
// whose id merely begins with a named one is another project).
func TestNewReaperRefusesTheProjectsTheCallerNames(t *testing.T) {
	for _, project := range []string{"acme-prod", "ACME-PROD", " acme-prod "} {
		t.Run(project, func(t *testing.T) {
			bf := gcpfake.NewBatch(t)
			r, err := NewReaper(context.Background(), ReaperOptions{
				GCPProject: project, Region: "us-east5",
				Timeouts:            testTimeouts{longest: time.Minute},
				OutOfBoundsProjects: []string{"acme-prod", "acme-staging"},
				Endpoints:           Endpoints{Batch: bf.URL + "/", NoAuth: true},
			})
			if err == nil {
				t.Fatalf("NewReaper accepted GCP project %q: the caller named it as one this pass must never touch", project)
			}
			if r != nil {
				t.Fatal("NewReaper returned a reaper along with the refusal")
			}
			if !strings.Contains(err.Error(), "out of bounds") {
				t.Errorf("refusal = %q, want it to say the project is out of bounds, as the floor's own refusal does", err)
			}
		})
	}

	// The caller's list is exact like the floor's, and it does not extend the
	// floor's reach: a project that merely begins with a named one is another
	// project, and the floor's two ids are refused whether or not the caller
	// named them.
	t.Run("allowed/acme-prod-staging", func(t *testing.T) {
		bf := gcpfake.NewBatch(t)
		if _, err := NewReaper(context.Background(), ReaperOptions{
			GCPProject: "acme-prod-staging", Region: "us-east5",
			Timeouts:            testTimeouts{longest: time.Minute},
			OutOfBoundsProjects: []string{"acme-prod"},
			Endpoints:           Endpoints{Batch: bf.URL + "/", NoAuth: true},
		}); err != nil {
			t.Errorf("NewReaper refused %q: the caller's list is exact, and this is another project (%v)", "acme-prod-staging", err)
		}
	})

	// The floor is not removable by naming something else: a caller that
	// supplies its own list gets that list AND the floor, never instead of it.
	t.Run("the caller's list cannot replace the floor", func(t *testing.T) {
		bf := gcpfake.NewBatch(t)
		if _, err := NewReaper(context.Background(), ReaperOptions{
			GCPProject: "fugaro-belong", Region: "us-east5",
			Timeouts:            testTimeouts{longest: time.Minute},
			OutOfBoundsProjects: []string{"acme-prod"},
			Endpoints:           Endpoints{Batch: bf.URL + "/", NoAuth: true},
		}); err == nil {
			t.Error("a caller's own list replaced the floor: the two ids in it are refused whatever an installation configures")
		}
	})
}

// TestReaperTwoOverlappingExecutionsDeleteOnce: nothing serialises two passes
// over one installation -- Cloud Scheduler starts one on its tick, an operator
// can start another by hand, and a pass that outran its period has the next
// queued behind it -- so the property that makes that safe is pinned here: a
// second execution, built over the same project before the first one ran,
// finds the resources gone, makes no second delete call and reports no action.
//
// It is the same-installation form of TestReaperIsIdempotent (which is one
// Reaper reaped twice); the interleaving this test cannot stage -- the second
// pass's listing taken before the first pass's delete -- is the one
// TestReaperToleratesADeleteOfSomethingAlreadyGone covers from the other side,
// where a delete the API answers 404 is read as success and not as a failure
// that ends the pass. Between them, an overlap costs a second 404 and nothing
// else, which is why reaper.tf says so instead of taking a lock.
func TestReaperTwoOverlappingExecutionsDeleteOnce(t *testing.T) {
	rt := newReaperTest(t, nil)
	rt.addJob(t, "20260927-100000-abcd", "RUNNING", rt.stuck())
	rt.vm("vm-1", runLabels(t, "20260927-100001-abcd"), rt.old())

	// The second execution: another Reaper over the same installation and the
	// same fakes, built before the first pass ran. That is what an overlap is
	// -- two passes that each read the installation for themselves.
	second, err := NewReaper(context.Background(), ReaperOptions{
		GCPProject: testProject, Region: testRegion,
		Timeouts:  testTimeouts{task: 30 * time.Minute, longest: 30 * time.Minute},
		Endpoints: Endpoints{Batch: rt.bf.URL + "/", Compute: rt.cf.URL + "/compute/v1/", NoAuth: true},
		Now:       func() time.Time { return rt.now },
	})
	if err != nil {
		t.Fatalf("NewReaper (the overlapping execution): %v", err)
	}

	first := rt.reap(t)
	if len(first.Actions) != 2 {
		t.Fatalf("first pass actions = %v, want the stuck job and the leaked VM", first.Lines())
	}
	rep, err := second.Reap(context.Background())
	if err != nil {
		t.Fatalf("the overlapping pass failed: %v", err)
	}
	if len(rep.Actions) != 0 {
		t.Fatalf("the overlapping pass acted on %v, want nothing: the first pass had deleted both", rep.Lines())
	}
	if got := rt.bf.Deletes(); len(got) != 1 {
		t.Errorf("jobs deleted = %v, want one: an overlap is not a second delete", got)
	}
	if got := rt.cf.Deletes(); len(got) != 1 {
		t.Errorf("instances deleted = %v, want one: an overlap is not a second delete", got)
	}
}
