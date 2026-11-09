package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
)

// doctor_coordinator_test.go is the owner's ruling of 2026-11-09: the
// coordinator must never hold run.jobs.runWithOverrides. The coordinator
// starts a run through the start-only role (infra.RoleJobStarter), and
// per-launch data travels in the signed task in the runs bucket rather than in
// an execution override, so a binding that gives one of the coordinator work's
// own accounts a role carrying the override permission is a finding doctor
// reports.

// coordinatorMember is the sweep's own service account as a binding spells it.
func coordinatorMember(project string) string {
	return "serviceAccount:" + infra.CoordinatorAccountID + "@" + project + ".iam.gserviceaccount.com"
}

// frontDoorMember is the front door's account (M4), the identity M9's launch
// grant lands on, as a binding spells it.
func frontDoorMember(project string) string {
	return "serviceAccount:" + infra.MCPServerAccountID + "@" + project + ".iam.gserviceaccount.com"
}

// withCoordinatorOptIn turns the coordinator on in the rig's config and points
// its endpoints at an IAM fake, which is where a custom role's permissions are
// resolved from.
func (r *doctorRig) withCoordinatorOptIn(t *testing.T) *gcpfake.IAM {
	t.Helper()
	iam := gcpfake.NewIAM(t)
	cfg := strings.Replace(r.cfg, "endpoints: { ", "endpoints: { iam: "+iam.URL+"/, ", 1) +
		"terraform: { coordinator: true }\n"
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return iam
}

// TestDoctorCoordinatorHoldsNoOverrides: the sweep's account holding the
// start-only role is the state the ruling asks for, and doctor says so without
// a finding.
func TestDoctorCoordinatorHoldsNoOverrides(t *testing.T) {
	r := newDoctorRig(t)
	iam := r.withCoordinatorOptIn(t)
	iam.AddRole("proj-1234", infra.RoleJobStarter, "Fugaro job starter", false)
	iam.SetRolePermissions("projects/proj-1234/roles/"+infra.RoleJobStarter, "run.jobs.run")
	r.crm.SetPolicy("proj-1234",
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}},
		gcpfake.Binding{Role: "projects/proj-1234/roles/" + infra.RoleJobStarter, Members: []string{coordinatorMember("proj-1234")}})

	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "coordinator-overrides")
	if !found {
		t.Fatal("doctor reports nothing about the coordinator's override permission")
	}
	if !c.OK {
		t.Fatalf("check = %+v, want ok: the coordinator holds the start-only role", c)
	}
}

// TestDoctorReportsTheCoordinatorsOverridesGrant: the installation's own job
// runner role carries run.jobs.runWithOverrides, so a binding that gives it to
// the coordinator is the regression the ruling forbids — and it is named.
func TestDoctorReportsTheCoordinatorsOverridesGrant(t *testing.T) {
	r := newDoctorRig(t)
	iam := r.withCoordinatorOptIn(t)
	iam.AddRole("proj-1234", infra.RoleJobRunner, "Fugaro job runner", false)
	iam.SetRolePermissions("projects/proj-1234/roles/"+infra.RoleJobRunner, "run.jobs.run", "run.jobs.runWithOverrides")
	r.crm.SetPolicy("proj-1234",
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}},
		gcpfake.Binding{Role: "projects/proj-1234/roles/" + infra.RoleJobRunner, Members: []string{coordinatorMember("proj-1234")}})

	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "coordinator-overrides")
	if !found {
		t.Fatal("doctor reports nothing about the coordinator's override permission")
	}
	if c.OK || c.Severity != "warning" {
		t.Fatalf("check = %+v, want a warning", c)
	}
	if !strings.Contains(c.Problem, infra.RoleJobRunner) {
		t.Errorf("Problem does not name the role that carries the override permission: %s", c.Problem)
	}
	if !strings.Contains(c.Problem, infra.CoordinatorAccountID) {
		t.Errorf("Problem does not name the account that holds it: %s", c.Problem)
	}
}

// A predefined role that carries the permission counts too: the check is about
// the permission the coordinator's account holds, not about who granted it.
func TestDoctorReportsAPredefinedOverridesGrant(t *testing.T) {
	r := newDoctorRig(t)
	r.withCoordinatorOptIn(t)
	r.crm.SetPolicy("proj-1234",
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}},
		gcpfake.Binding{Role: "roles/run.admin", Members: []string{frontDoorMember("proj-1234")}})

	c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "coordinator-overrides")
	if !found {
		t.Fatal("doctor reports nothing about the coordinator's override permission")
	}
	if c.OK || c.Severity != "warning" {
		t.Fatalf("check = %+v, want a warning", c)
	}
	for _, want := range []string{"roles/run.admin", infra.MCPServerAccountID} {
		if !strings.Contains(c.Problem, want) {
			t.Errorf("Problem does not name %s: %s", want, c.Problem)
		}
	}
}

// An installation with no coordinator gets nothing: the accounts do not exist,
// so there is no fact to report.
func TestDoctorSaysNothingAboutOverridesWithoutACoordinator(t *testing.T) {
	r := newDoctorRig(t)
	r.crm.SetPolicy("proj-1234",
		gcpfake.Binding{Role: "roles/owner", Members: []string{"user:owner@example.com"}},
		gcpfake.Binding{Role: "roles/run.admin", Members: []string{coordinatorMember("proj-1234")}})

	if c, found := doctorCheckByID(r.runDoctorJSON(t).Checks, "coordinator-overrides"); found {
		t.Errorf("doctor reports %+v on an installation with no coordinator", c)
	}
}
