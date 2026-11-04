package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	managed "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/initflow"
	managedmode "github.com/dimipaun/fugaro/internal/managed"
)

// doctor_managed_audit.go's tests (M7, D1014, spec §6).
//
// What these hold. The check reads the family the audit record is written into
// and prints the last apply and the day's count, and the two ways it can be
// wrong are both failures to say something: a check that printed nothing for a
// project whose records it could not read would be a silent pass, and one that
// printed a line for every ordinary installation would put a pointer on a
// screen with nothing wrong in it (the owner's rule of 2026-10-31). Neither is
// visible from the code, so each gets a test: the read that failed, and the
// project that is not managed.
//
// Every world here is hermetic: internal/gcpfake's GCS, Resource Manager,
// Billing and Secret Manager fakes, t.TempDir() for the config, and a pinned
// clock, since a record's date directory and the check's own day both come from
// the time and a test that let the clock decide would flake at UTC midnight.

// managedAuditTestNow is the day every record in this file is filed under and
// the day the check is asked about.
var managedAuditTestNow = time.Date(2026, 11, 3, 9, 30, 0, 0, time.UTC)

// managedAuditRig is a doctor installation that is managed: the local config
// and the fakes of newDoctorRig, plus the runs bucket and the two marks the
// design's read requires — the bucket's fugaro=managed label, the project's
// fugaro_managed=true label and the creation fact itself.
type managedAuditRig struct {
	*doctorRig
	gcs *gcpfake.GCS
}

// managedAuditOpts is what a rig leaves out: everything is in place unless a
// field says otherwise, so a test that removes one half of the mark is a test
// about that half.
type managedAuditOpts struct {
	noMarker       bool
	noProjectLabel bool
}

func newManagedAuditRig(t *testing.T, o managedAuditOpts) *managedAuditRig {
	t.Helper()
	r := newDoctorRig(t)
	g := gcpfake.NewGCS(t)
	r.withRunsBucket(t, g, dbLists, hardenedPolicy()...)
	if !o.noProjectLabel {
		labelled(r.crm, "proj-1234")
	}
	if !o.noMarker {
		g.Put(dbBucket, managed.MarkerObject, []byte(`{"version":1,"gcp_project":"proj-1234"}`))
	}
	old := doctorManagedAuditNow
	doctorManagedAuditNow = func() time.Time { return managedAuditTestNow }
	t.Cleanup(func() { doctorManagedAuditNow = old })
	return &managedAuditRig{doctorRig: r, gcs: g}
}

// file writes one record of the day through the writer the commands themselves
// call, so what the check reads back is an object this program produced and not
// one a test hand-wrote.
func (r *managedAuditRig) file(t *testing.T, command, doc string, verdict managedmode.AuditVerdict, at time.Time) managedmode.AuditRecord {
	t.Helper()
	conv := managedmode.ConvergeAdmitted([]byte(doc), initflow.YesOrTyped, []string{"every key is in the registry"})
	if verdict == managedmode.AuditRefused {
		conv = managedmode.ConvergeRefused([]byte(doc), initflow.YesOrTyped, []string{"the plan removes an address the owner's list does not name"})
	}
	rec := managedmode.AuditRecordFor(managedmode.AuditFacts{
		Command: command, Project: "aurora", GCPProject: "proj-1234",
		Identity: "user:owner@example.com", MarkerVersion: 1, At: at,
	}, conv)
	if _, err := managedmode.WriteAudit(context.Background(), managedStorage(t, r.gcs), dbBucket, rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestDoctorShowsTheLastManagedAgentApply: a managed project's check names the
// newest recorded apply — its command, its verdict and the digest of the plan
// the gate judged — with the day's count, and says so plainly when no apply has
// been recorded yet, without failing a project that never ran one.
func TestDoctorShowsTheLastManagedAgentApply(t *testing.T) {
	t.Run("the newest record is the one shown", func(t *testing.T) {
		r := newManagedAuditRig(t, managedAuditOpts{})
		older := r.file(t, "fugaro config publish", `{"version":1,"keys":{"rigor":"standard"}}`, managedmode.AuditRefused, managedAuditTestNow.Add(-3*time.Hour))
		newest := r.file(t, "fugaro update", `{"version":2,"add":["image"]}`, managedmode.AuditAdmitted, managedAuditTestNow.Add(-90*time.Minute))
		o := doctorJSON(t)
		c, ok := doctorCheckByID(o.Checks, "managed-audit")
		if !ok {
			t.Fatalf("no managed-audit check: %+v", o.Checks)
		}
		if !strings.Contains(c.Problem, newest.Command) || !strings.Contains(c.Problem, string(newest.Verdict)) {
			t.Errorf("problem = %q, want the last apply's command (%q) and verdict (%q)", c.Problem, newest.Command, newest.Verdict)
		}
		if !strings.Contains(c.Problem, newest.PlanDigest) {
			t.Errorf("problem = %q, want the digest of the plan the gate judged (%q)", c.Problem, newest.PlanDigest)
		}
		if strings.Contains(c.Problem, older.PlanDigest) || strings.Contains(c.Problem, older.Command) {
			t.Errorf("problem = %q, want the NEWEST record: the older one (%q) is not the last apply", c.Problem, older.Command)
		}
		if !strings.Contains(c.Problem, "2 records") {
			t.Errorf("problem = %q, want the day's count (2 records)", c.Problem)
		}
		if c.OK != true || c.Severity != "info" {
			t.Errorf("check = %+v, want an informational measurement: a managed apply that was refused is the gate working, not a fault", c)
		}
		if !o.OK {
			t.Errorf("doctor failed on a project whose only record is a refusal: %+v", o.Checks)
		}
	})
	t.Run("no record is not a failure", func(t *testing.T) {
		newManagedAuditRig(t, managedAuditOpts{})
		o := doctorJSON(t)
		c, ok := doctorCheckByID(o.Checks, "managed-audit")
		if !ok {
			t.Fatalf("no managed-audit check: %+v", o.Checks)
		}
		if !strings.Contains(c.Problem, "no managed agent apply") {
			t.Errorf("problem = %q, want the check to say none was recorded", c.Problem)
		}
		if !c.OK || c.Severity != "info" {
			t.Errorf("check = %+v, want a project that never ran a managed apply to pass, as a measurement", c)
		}
		if !o.OK {
			t.Errorf("doctor failed on a project that never ran a managed apply: %+v", o.Checks)
		}
	})
}

// TestDoctorManagedAuditIsSilentOutsideAManagedProject: a project that is not
// managed files no records at all (D1025), so the check says nothing there —
// the pointer line on an ordinary installation's clean screen is exactly what
// the design's "in a managed project and in no other" rules out.
func TestDoctorManagedAuditIsSilentOutsideAManagedProject(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts managedAuditOpts
	}{
		{"the project carries no managed label", managedAuditOpts{noProjectLabel: true}},
		{"the creation fact is absent", managedAuditOpts{noMarker: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newManagedAuditRig(t, tc.opts)
			if c, ok := doctorCheckByID(doctorJSON(t).Checks, "managed-audit"); ok {
				t.Errorf("check = %+v, want no managed-audit check for a project that is not managed", c)
			}
		})
	}
}

// TestDoctorManagedAuditNeverPassesSilentlyOnAReadThatFailed: a family that
// could not be listed is an unknown, and an unknown printed as "no apply was
// recorded" would be the silent pass — the one shape that reads exactly like
// the clean state.
func TestDoctorManagedAuditNeverPassesSilentlyOnAReadThatFailed(t *testing.T) {
	r := newManagedAuditRig(t, managedAuditOpts{})
	r.gcs.FailListings(dbBucket)
	o := doctorJSON(t)
	c, ok := doctorCheckByID(o.Checks, "managed-audit")
	if !ok {
		t.Fatalf("no managed-audit check: %+v", o.Checks)
	}
	if c.OK {
		t.Errorf("check = %+v, want a read that failed to be a finding", c)
	}
	if c.Severity != "warning" || c.Class != doctorClassActionable {
		t.Errorf("check = %+v, want a warning the default view prints: a read that did not answer is not a measurement", c)
	}
	if !strings.Contains(c.Problem, "could not") {
		t.Errorf("problem = %q, want the sentence to say the records could not be read", c.Problem)
	}
}
