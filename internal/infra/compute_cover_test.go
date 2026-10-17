package infra

import (
	"io/fs"
	"strings"
	"testing"

	terraform "github.com/dimipaun/fugaro/deploy/terraform"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra/tf"
)

// computeGrantPlan is the compute module's grants as a first apply plans them
// (modules/compute/iam.tf): the members and roles computed from the accounts
// and custom roles the same apply creates are unknown, everything else is the
// spec's. jobsCond is the launchers' Batch Job Editor condition.
func computeGrantPlan(spec ComputeSpec, jobsCond map[string]any) *tf.Plan {
	const m = "module.compute."
	unk := map[string]any{"member": true}
	unkRole := map[string]any{"member": true, "role": true}
	preloadName := map[string]any{"service_account_id": true}
	rc := func(addr, typ string, after map[string]any, afterUnknown map[string]any) tf.ResourceChange {
		return tf.ResourceChange{Address: m + typ + "." + addr, Type: typ,
			Change: tf.Change{Actions: []string{"create"}, After: after, AfterUnknown: afterUnknown}}
	}
	run, launcher := spec.RunAccounts[0], spec.Launchers[0]
	agents := ComputeServiceAgents(spec.ProjectNumber)
	p := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		rc("reaper", "google_service_account", map[string]any{"project": spec.Project, "account_id": ReaperAccountID}, nil),
		rc("preload", "google_service_account", map[string]any{"project": spec.Project, "account_id": PreloadAccountID}, nil),
		rc("reaper", "google_project_iam_custom_role", map[string]any{"project": spec.Project, "role_id": ReaperRoleID}, nil),
		rc("preload_builder", "google_project_iam_custom_role", map[string]any{"project": spec.Project, "role_id": PreloadRoleID}, nil),
		rc(`run_agent_reporter["k"]`, "google_project_iam_member", map[string]any{"project": spec.Project, "role": "roles/batch.agentReporter", "member": run}, nil),
		rc(`run_log_writer["k"]`, "google_project_iam_member", map[string]any{"project": spec.Project, "role": "roles/logging.logWriter", "member": run}, nil),
		rc(`run_registry_reader["k"]`, "google_artifact_registry_repository_iam_member", map[string]any{"project": spec.Project, "location": spec.Region,
			"repository": spec.RegistryRepository, "role": "roles/artifactregistry.reader", "member": run}, nil),
		rc(`launcher_jobs_editor["k"]`, "google_project_iam_member", map[string]any{"project": spec.Project, "role": "roles/batch.jobsEditor", "member": launcher,
			"condition": []any{jobsCond}}, nil),
		rc(`launcher_user["k"]`, "google_service_account_iam_member", map[string]any{"service_account_id": "projects/" + spec.Project + "/serviceAccounts/" + strings.TrimPrefix(run, "serviceAccount:"),
			"role": "roles/iam.serviceAccountUser", "member": launcher}, nil),
		rc("reaper", "google_project_iam_member", map[string]any{"project": spec.Project}, unkRole),
		rc("preload_builder", "google_project_iam_member", map[string]any{"project": spec.Project}, unkRole),
		rc("preload_build_submitter", "google_project_iam_member", map[string]any{"project": spec.Project, "role": spec.BuildSubmitterRole}, unk),
		rc("preload_log_writer", "google_project_iam_member", map[string]any{"project": spec.Project, "role": "roles/logging.logWriter"}, unk),
		rc("preload_scheduler_agent", "google_service_account_iam_member", map[string]any{"role": "roles/iam.serviceAccountUser", "member": agents[1]}, preloadName),
		rc("preload_cloud_build_agent", "google_service_account_iam_member", map[string]any{"role": "roles/iam.serviceAccountUser", "member": agents[0]}, preloadName),
		rc("preload_self_user", "google_service_account_iam_member", map[string]any{"role": "roles/iam.serviceAccountUser"}, map[string]any{"service_account_id": true, "member": true}),
		rc(`preload_run_account_user["k"]`, "google_service_account_iam_member", map[string]any{"service_account_id": "projects/" + spec.Project + "/serviceAccounts/" + strings.TrimPrefix(run, "serviceAccount:"),
			"role": "roles/iam.serviceAccountUser"}, unk),
		// Not grants: the cover leaves them to the stage's confirmation.
		rc("preload_rebuild", "google_cloud_scheduler_job", map[string]any{"project": spec.Project,
			"http_target": []any{map[string]any{"uri": "https://cloudbuild.googleapis.com/v1/projects/" + spec.Project + "/builds"}}}, nil),
		rc("this", "google_compute_router_nat", map[string]any{"project": spec.Project}, nil),
	}}
	for _, op := range spec.Operators {
		p.ResourceChanges = append(p.ResourceChanges, rc(`preload_operator["`+op+`"]`, "google_service_account_iam_member",
			map[string]any{"role": "roles/iam.serviceAccountUser", "member": op}, preloadName))
	}
	return p
}

func moduleJobsCond(project, region string) map[string]any {
	return map[string]any{"title": gcp.LauncherJobsConditionTitle, "description": gcp.LauncherJobsConditionDescription,
		"expression": gcp.LauncherJobsCondition(project, region)}
}

// The compute stage runs ComputeCover's GrantsNotCovered over the root's plan.
// The cover is the COMPUTE ROOT's: its project and region are the spec's, so
// the launchers' condition is checked against the region the module renders it
// for, and every grant the module makes is covered while the ones the design
// forbids are not.
func TestComputeCoverIsTheComputeRoots(t *testing.T) {
	spec := computeSpec()
	spec.Region = "us-west1" // not the region any installation default would name
	spec.Operators = []string{"user:operator@example.com"}
	c := ComputeCover(spec)

	if got := c.GrantsNotCovered(computeGrantPlan(spec, moduleJobsCond(spec.Project, spec.Region))); len(got) != 0 {
		t.Fatalf("the module's own grants are refused: %v", got)
	}
	for name, mut := range map[string]func(p *tf.Plan){
		"the launchers' condition for another region": func(p *tf.Plan) {
			setAfter(p, "launcher_jobs_editor", "condition", []any{moduleJobsCond(spec.Project, "us-east5")})
		},
		"an unconditioned jobsEditor": func(p *tf.Plan) { setAfter(p, "launcher_jobs_editor", "condition", []any{}) },
		"jobsEditor to the reaper": func(p *tf.Plan) {
			setAfter(p, "launcher_jobs_editor", "member", "serviceAccount:"+ReaperAccountID+"@"+spec.Project+".iam.gserviceaccount.com")
		},
		"an operator who is a whole domain": func(p *tf.Plan) {
			setAfter(p, "preload_operator", "member", "domain:example.com")
		},
		"a run account the stage did not read": func(p *tf.Plan) {
			setAfter(p, "run_agent_reporter", "member", "serviceAccount:someone@"+spec.Project+".iam.gserviceaccount.com")
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := computeGrantPlan(spec, moduleJobsCond(spec.Project, spec.Region))
			mut(p)
			if got := c.GrantsNotCovered(p); len(got) == 0 {
				t.Fatal("covered, want refused")
			}
		})
	}
}

func setAfter(p *tf.Plan, addr, key string, v any) {
	for i := range p.ResourceChanges {
		if strings.Contains(p.ResourceChanges[i].Address, "."+addr) {
			p.ResourceChanges[i].Change.After[key] = v
		}
	}
}

// The two service agents the cover accepts are the two the module grants
// actAs on the preload account to, spelled as iam.tf derives them.
func TestComputeServiceAgentsAreTheModules(t *testing.T) {
	b, err := fs.ReadFile(terraform.FS, "gcp/modules/compute/iam.tf")
	if err != nil {
		t.Fatal(err)
	}
	const n = "123456789"
	for i, tmpl := range []string{
		`"serviceAccount:service-${var.project_number}@gcp-sa-cloudbuild.iam.gserviceaccount.com"`,
		`"serviceAccount:service-${var.project_number}@gcp-sa-cloudscheduler.iam.gserviceaccount.com"`,
	} {
		if !strings.Contains(string(b), tmpl) {
			t.Fatalf("iam.tf no longer derives %s", tmpl)
		}
		if got, want := ComputeServiceAgents(n)[i], strings.Trim(strings.ReplaceAll(tmpl, "${var.project_number}", n), `"`); got != want {
			t.Errorf("ComputeServiceAgents[%d] = %s, the module's is %s", i, got, want)
		}
	}
}

// operators each get actAs on the preload account, which can act as the run
// accounts and create instances: a whole domain or a wildcard is refused here
// as the module's validation refuses it.
func TestComputeVarsRefuseAWideOperator(t *testing.T) {
	for _, m := range []string{"domain:example.com", "allUsers", "allAuthenticatedUsers", "user:", "someone@example.com"} {
		s := computeSpec()
		s.Operators = []string{"user:ok@example.com", m}
		if _, err := ComputeVars(s); err == nil || !strings.Contains(err.Error(), "operator") {
			t.Errorf("ComputeVars accepted the operator %q (err %v)", m, err)
		}
	}
	s := computeSpec()
	s.Operators = []string{"user:ok@example.com", "group:ops@example.com", "serviceAccount:ci@proj-1234.iam.gserviceaccount.com"}
	if _, err := ComputeVars(s); err != nil {
		t.Errorf("ComputeVars refused people and an account: %v", err)
	}
}
