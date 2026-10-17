package tf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

const (
	cjProject = "proj-1234"
	cjRegion  = "us-east5"
)

func jobsGrant(member, project string, conds []any) *Plan {
	return &Plan{ResourceChanges: []ResourceChange{{
		Address: `module.compute.google_project_iam_member.launcher_jobs_editor["k"]`,
		Type:    "google_project_iam_member",
		Change: Change{Actions: []string{"create"}, After: map[string]any{
			"project": project, "member": member, "role": "roles/batch.jobsEditor", "condition": conds,
		}, AfterUnknown: map[string]any{"condition": []any{map[string]any{}}}},
	}}}
}

func jobsCond(title, desc, expr string) map[string]any {
	return map[string]any{"title": title, "expression": expr, "description": desc}
}

// The launchers' roles/batch.jobsEditor is covered only under exactly the
// compute module's Fugaro-job-name condition: an unconditioned grant is
// project-wide job creation with any image, command and account.
func TestCoverBatchJobsEditorCondition(t *testing.T) {
	c := Cover{Projects: []string{cjProject}, Region: cjRegion, Listed: []string{cbPerson}}
	good := gcp.LauncherJobsCondition(cjProject, cjRegion)
	right := jobsCond(gcp.LauncherJobsConditionTitle, gcp.LauncherJobsConditionDescription, good)
	for _, tc := range []struct {
		name    string
		project string
		conds   []any
		covered bool
	}{
		{"under the Fugaro job-name condition", cjProject, []any{right}, true},
		{"unconditioned", cjProject, []any{}, false},
		{"condition attribute absent", cjProject, nil, false},
		{"condition is true", cjProject, []any{jobsCond(gcp.LauncherJobsConditionTitle, gcp.LauncherJobsConditionDescription, "true")}, false},
		{"a prefix of the condition (every job in the region)", cjProject,
			[]any{jobsCond(gcp.LauncherJobsConditionTitle, gcp.LauncherJobsConditionDescription,
				`resource.name.startsWith("projects/`+cjProject+`/locations/`+cjRegion+`/jobs/")`)}, false},
		{"the condition widened by a disjunction", cjProject,
			[]any{jobsCond(gcp.LauncherJobsConditionTitle, gcp.LauncherJobsConditionDescription, good+" || true")}, false},
		{"the condition with trailing text", cjProject,
			[]any{jobsCond(gcp.LauncherJobsConditionTitle, gcp.LauncherJobsConditionDescription, good+" ")}, false},
		{"another region", cjProject,
			[]any{jobsCond(gcp.LauncherJobsConditionTitle, gcp.LauncherJobsConditionDescription, gcp.LauncherJobsCondition(cjProject, "us-west1"))}, false},
		{"another project's condition", cjProject,
			[]any{jobsCond(gcp.LauncherJobsConditionTitle, gcp.LauncherJobsConditionDescription, gcp.LauncherJobsCondition("proj-other", cjRegion))}, false},
		{"granted on a project other than the condition's", "proj-other", []any{right}, false},
		{"another title", cjProject, []any{jobsCond("mine", gcp.LauncherJobsConditionTitle, good)}, false},
		{"another description", cjProject, []any{jobsCond(gcp.LauncherJobsConditionTitle, "x", good)}, false},
		{"two conditions", cjProject, []any{right, right}, false},
		{"a list entry that is not a block", cjProject, []any{right, "true"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := jobsGrant(cbPerson, tc.project, tc.conds)
			if tc.conds == nil {
				delete(p.ResourceChanges[0].Change.After, "condition")
			}
			got := c.NotCovered(p, nil)
			if tc.covered && len(got) != 0 {
				t.Fatalf("not covered: %v", got)
			}
			if !tc.covered && len(got) == 0 {
				t.Fatal("covered, want refused")
			}
		})
	}
}

func TestCoverBatchJobsEditorConditionFailsClosed(t *testing.T) {
	right := jobsCond(gcp.LauncherJobsConditionTitle, gcp.LauncherJobsConditionDescription, gcp.LauncherJobsCondition(cjProject, cjRegion))
	t.Run("no region in the cover", func(t *testing.T) {
		c := Cover{Projects: []string{cjProject}, Listed: []string{cbPerson}}
		if got := c.NotCovered(jobsGrant(cbPerson, cjProject, []any{right}), nil); len(got) == 0 {
			t.Fatal("covered without a region to check the condition against")
		}
	})
	t.Run("condition known only after apply", func(t *testing.T) {
		c := Cover{Projects: []string{cjProject}, Region: cjRegion, Listed: []string{cbPerson}}
		p := jobsGrant(cbPerson, cjProject, []any{right})
		p.ResourceChanges[0].Change.AfterUnknown = map[string]any{"condition": []any{map[string]any{"expression": true}}}
		if got := c.NotCovered(p, nil); len(got) == 0 {
			t.Fatal("covered with an unknown condition expression")
		}
	})
}

// The compute module's condition is the one the cover checks: its title and
// description literals, and its expression once the module's locals are
// substituted.
func TestCoverBatchJobsConditionIsTheModules(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "terraform", "gcp", "modules", "compute", "iam.tf"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`title       = "` + gcp.LauncherJobsConditionTitle + `"`,
		`description = "` + gcp.LauncherJobsConditionDescription + `"`,
		`expression  = "resource.name.startsWith(\"${local.job_name_prefix}\")"`,
		`job_name_prefix       = "projects/${var.project}/locations/${var.region}/jobs/${var.names.job_prefix}"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("modules/compute/iam.tf lacks %s", want)
		}
	}
	rendered := strings.NewReplacer("${var.project}", cjProject, "${var.region}", cjRegion, "${var.names.job_prefix}", gcp.BatchJobPrefix).
		Replace(`resource.name.startsWith("projects/${var.project}/locations/${var.region}/jobs/${var.names.job_prefix}")`)
	if rendered != gcp.LauncherJobsCondition(cjProject, cjRegion) {
		t.Errorf("module renders %s, gcp.LauncherJobsCondition is %s", rendered, gcp.LauncherJobsCondition(cjProject, cjRegion))
	}
}
