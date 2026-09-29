package tf

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustPlan(t *testing.T, s string) *Plan {
	t.Helper()
	p, err := parsePlan([]byte(strings.Replace(s, `{`, `{"format_version":"1.2",`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const jobBefore = `{"name":"j","labels":{"a":"1"},"template":[{"template":[{"service_account":"sa@p","max_retries":0,"containers":[{"image":"old","env":[{"name":"A","value":"1"}]}]}]}]}`

func TestSummaryHighlightsSensitiveUpdates(t *testing.T) {
	labelOnly := strings.Replace(jobBefore, `"a":"1"`, `"a":"2"`, 1)
	newImage := strings.Replace(jobBefore, `"old"`, `"new"`, 1)
	p := mustPlan(t, `{"resource_changes":[
	  {"address":"module.w.google_cloud_run_v2_job.labels","type":"google_cloud_run_v2_job","change":{"actions":["update"],"before":`+jobBefore+`,"after":`+labelOnly+`}},
	  {"address":"module.w.google_cloud_run_v2_job.image","type":"google_cloud_run_v2_job","change":{"actions":["update"],"before":`+jobBefore+`,"after":`+newImage+`}}
	]}`)
	s := Summary(p)
	t.Log("\n" + s)

	lines := strings.Split(s, "\n")
	iImage, iLabels := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "google_cloud_run_v2_job.image") {
			iImage = i
		}
		if strings.Contains(l, "google_cloud_run_v2_job.labels") {
			iLabels = i
		}
	}
	if iImage < 0 || iLabels < 0 || iImage > iLabels {
		t.Fatalf("the image update (line %d) isn't listed before the label update (line %d)", iImage, iLabels)
	}
	if l := lines[iImage]; !strings.Contains(l, "⚠") || !strings.Contains(l, "template.template.containers[0].image") {
		t.Errorf("the image update isn't marked with its path: %q", l)
	}
	if l := lines[iLabels]; strings.Contains(l, "⚠") || !strings.Contains(l, "labels.a") {
		t.Errorf("the label update is marked or lacks its path: %q", l)
	}
	if !strings.Contains(lines[0], "0 to import, 0 to create, 2 to update, 0 to forget") {
		t.Errorf("counts line = %q", lines[0])
	}
}

func TestSummarySensitiveAttributes(t *testing.T) {
	for _, tc := range []struct {
		typ, before, after, path string
	}{
		{"google_cloud_run_v2_job", jobBefore, strings.Replace(jobBefore, `"sa@p"`, `"sa2@p"`, 1), "template.template.service_account"},
		{"google_logging_project_bucket_config", `{"bucket_id":"fugaro","retention_days":365}`, `{"bucket_id":"fugaro","retention_days":30}`, "retention_days"},
		{"google_cloud_run_v2_job", jobBefore, strings.Replace(jobBefore, `"max_retries":0`, `"max_retries":3`, 1), "template.template.max_retries"},
		{"google_cloud_run_v2_job", jobBefore, strings.Replace(jobBefore, `"value":"1"`, `"value":"2"`, 1), "template.template.containers[0].env[0].value"},
		{"google_storage_bucket", `{"lifecycle_rule":[]}`, `{"lifecycle_rule":[{"action":[{"type":"Delete"}]}]}`, "lifecycle_rule[0]"},
		{"google_artifact_registry_repository", `{"cleanup_policy_dry_run":true}`, `{"cleanup_policy_dry_run":false}`, "cleanup_policy_dry_run"},
		{"google_artifact_registry_repository", `{"cleanup_policies":[{"id":"a"}]}`, `{"cleanup_policies":[{"id":"b"}]}`, "cleanup_policies[0].id"},
		{"google_cloud_scheduler_job", `{"paused":true}`, `{"paused":false}`, "paused"},
	} {
		p := mustPlan(t, `{"resource_changes":[{"address":"x.y","type":"`+tc.typ+`","change":{"actions":["update"],"before":`+tc.before+`,"after":`+tc.after+`}}]}`)
		s := Summary(p)
		var line string
		for _, l := range strings.Split(s, "\n") {
			if strings.Contains(l, "x.y") {
				line = l
			}
		}
		if !strings.Contains(line, "⚠") || !strings.Contains(line, tc.path) {
			t.Errorf("%s %s: line %q isn't marked with %s\n%s", tc.typ, tc.path, line, tc.path, s)
		}
	}
	// The same attribute name on another type is not sensitive.
	p := mustPlan(t, `{"resource_changes":[{"address":"x.y","type":"google_storage_bucket","change":{"actions":["update"],"before":{"paused":true},"after":{"paused":false}}}]}`)
	if s := Summary(p); strings.Contains(s, "⚠") {
		t.Errorf("a bucket's paused was marked:\n%s", s)
	}
	// Nor is a job label that happens to be called image.
	p = mustPlan(t, `{"resource_changes":[{"address":"x.y","type":"google_cloud_run_v2_job","change":{"actions":["update"],"before":{"labels":{"image":"a"}},"after":{"labels":{"image":"b"}}}}]}`)
	if s := Summary(p); strings.Contains(s, "⚠") {
		t.Errorf("a job label named image was marked:\n%s", s)
	}
}

func TestSummaryMarksUnknownSensitiveValues(t *testing.T) {
	for _, tc := range []struct {
		name, after, unknown, path string
	}{
		// A whole block that becomes unknown holds the image.
		{"ancestor", `{"name":"j","labels":{"a":"1"}}`, `{"template":true}`, "template"},
		// null to unknown: before and after both lack a value.
		{"null", strings.Replace(jobBefore, `"service_account":"sa@p",`, ``, 1), `{"template":[{"template":[{"service_account":true}]}]}`, "template.template.service_account"},
	} {
		before := jobBefore
		if tc.name == "null" {
			before = strings.Replace(jobBefore, `"sa@p"`, `null`, 1)
		}
		p := mustPlan(t, `{"resource_changes":[{"address":"x.y","type":"google_cloud_run_v2_job","change":{"actions":["update"],"before":`+before+`,"after":`+tc.after+`,"after_unknown":`+tc.unknown+`}}]}`)
		s := Summary(p)
		if !strings.Contains(s, "⚠ update x.y: ") || !strings.Contains(s, tc.path) {
			t.Errorf("%s: an unknown sensitive value isn't marked with %s:\n%s", tc.name, tc.path, s)
		}
	}
}

func TestSummaryIdentityImportHasNoEmptyID(t *testing.T) {
	p := mustPlan(t, `{"resource_changes":[
	  {"address":"a.b","type":"t","change":{"actions":["no-op"],"importing":{"identity":{"name":"n"}}}},
	  {"address":"c.d","type":"t","change":{"actions":["update"],"before":{"x":1},"after":{"x":2},"importing":{"identity":{"name":"n"}}}}
	]}`)
	s := Summary(p)
	if strings.Contains(s, "(id") || !strings.Contains(s, "import a.b\n") || !strings.Contains(s, "import and update c.d: x") {
		t.Errorf("identity imports:\n%s", s)
	}
}

func TestSummaryMarkedBlockSensitiveUpdatesFirst(t *testing.T) {
	p := loadPlan(t, "plan_delete.json")
	var upd ResourceChange
	if err := json.Unmarshal([]byte(`{"address":"zzz.sched","type":"google_cloud_scheduler_job","change":{"actions":["update"],"before":{"paused":true},"after":{"paused":false}}}`), &upd); err != nil {
		t.Fatal(err)
	}
	p.ResourceChanges = append(p.ResourceChanges, upd)
	s := Summary(p)
	iUpd, iDel := strings.Index(s, "⚠ update zzz.sched"), strings.Index(s, "⚠ delete terraform_data.d")
	if iUpd < 0 || iDel < 0 || iUpd > iDel {
		t.Errorf("the sensitive update isn't ahead of the delete:\n%s", s)
	}
}

func TestSummaryListsImportsFirst(t *testing.T) {
	p := loadPlan(t, "plan_import.json")
	// Put a create ahead of the import in plan order.
	var create ResourceChange
	if err := json.Unmarshal([]byte(`{"address":"aaa.first","type":"terraform_data","change":{"actions":["create"],"before":null,"after":{"input":"x"}}}`), &create); err != nil {
		t.Fatal(err)
	}
	p.ResourceChanges = append([]ResourceChange{create}, p.ResourceChanges...)
	s := Summary(p)
	t.Log("\n" + s)
	iImport := strings.Index(s, "import terraform_data.i")
	iCreate := strings.Index(s, "create aaa.first")
	if iImport < 0 || iCreate < 0 || iImport > iCreate {
		t.Errorf("imports aren't listed first:\n%s", s)
	}
	if !strings.Contains(s, "1 to import, 1 to create, 0 to update, 0 to forget") {
		t.Errorf("counts wrong:\n%s", s)
	}
	if strings.Contains(s, "terraform_data.a") {
		t.Errorf("a no-op was listed:\n%s", s)
	}
}

func TestSummaryShowsDeletesAndForgets(t *testing.T) {
	s := Summary(loadPlan(t, "plan_delete.json"))
	if !strings.Contains(s, "1 to delete") || !strings.Contains(s, "⚠ delete terraform_data.d") {
		t.Errorf("the delete isn't shown and marked:\n%s", s)
	}
	s = Summary(loadPlan(t, "plan_replace.json"))
	if !strings.Contains(s, "2 to replace") || !strings.Contains(s, "⚠ replace terraform_data.r") {
		t.Errorf("the replace isn't shown and marked:\n%s", s)
	}
	s = Summary(loadPlan(t, "plan_forget.json"))
	if !strings.Contains(s, "1 to forget") || !strings.Contains(s, "forget terraform_data.d") {
		t.Errorf("the forget isn't shown:\n%s", s)
	}
}
