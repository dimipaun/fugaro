//go:build terraform

package infra

import (
	"maps"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/place"
)

// The installation's log bucket is where the installation is (LP6, D3082, R5):
// a bucket created from now on is in place.For's location for the region — the
// region itself where Cloud Logging offers a bucket there, else the place's
// multi-region, else global — and a bucket that already EXISTS keeps its own,
// whatever the mapping says now, because a bucket's location is permanent and a
// plan that wrote the mapping's answer over it would replace the bucket and
// delete the run output in it (the resource's ignore_changes, F11).
//
// They are planned against a PRIOR STATE (installationPlanAgainst), as LP5a's
// replication tests are: only a state makes the real provider compute the diff
// for a bucket that exists, and the mock provider cannot — it plans the config's
// own value. The two-run mock pair in
// roots/installation/tests/installation.tftest.hcl is the other half of the
// same fact (the root's own test pins the created bucket's location).

// logIsolationState is the terraform state an installation whose log isolation
// is ON would have written: the bucket in `location`, the sink and the
// exclusion that carry its lines there, and the view its readers read. The
// names come from the tfvars the plan is handed, so the state and the config
// name one installation.
func logIsolationState(t *testing.T, vars map[string]any, location string) map[string]any {
	t.Helper()
	project, _ := vars["project"].(string)
	names, ok := vars["names"].(map[string]any)
	if !ok {
		t.Fatalf("the installation's tfvars carry no names: %v", vars["names"])
	}
	log, ok := names["log"].(map[string]any)
	if !ok {
		t.Fatalf("the installation's tfvars carry no names.log: %v", names["log"])
	}
	bucket, _ := log["bucket"].(string)
	desc, _ := vars["log_bucket_description"].(string)
	path := "projects/" + project + "/locations/" + location + "/buckets/" + bucket
	return map[string]any{
		"version": 4,
		"serial":  1,
		"lineage": "00000000-0000-0000-0000-000000000000",
		"outputs": map[string]any{},
		"resources": []any{
			stateResource(t, "google_logging_project_bucket_config", "fugaro", project, 0, map[string]any{
				"project": project, "location": location, "bucket_id": bucket,
				"retention_days": 30, "description": desc,
				"cmek_settings": []any{}, "index_configs": []any{},
				"id": path, "name": path, "lifecycle_state": "ACTIVE",
			}),
			stateResource(t, "google_logging_project_sink", "fugaro", project, 0, map[string]any{
				"project": project, "name": log["sink"],
				"destination": "logging.googleapis.com/" + path, "filter": "",
				"unique_writer_identity": true,
				"writer_identity":        "serviceAccount:fake@fake.iam.gserviceaccount.com",
				"exclusions":             []any{}, "deletion_policy": "DELETE",
				"id": "projects/" + project + "/sinks/" + log["sink"].(string),
			}),
			stateResource(t, "google_logging_project_exclusion", "fugaro_from_default", project, 0, map[string]any{
				"project": project, "name": log["exclusion"], "filter": "", "disabled": false,
				"id": "projects/" + project + "/exclusions/" + log["exclusion"].(string),
			}),
			stateResource(t, "google_logging_log_view", "runs", project, 0, map[string]any{
				"bucket": path, "name": log["view"], "description": "", "filter": "",
				"id": path + "/views/" + log["view"].(string),
			}),
		},
	}
}

// stateResource is one resource of that state, at instance index of a
// count-based resource (the module's log isolation is `count = 1`, so index 0).
func stateResource(t *testing.T, typ, name, project string, index int, attrs map[string]any) map[string]any {
	t.Helper()
	if project == "" {
		t.Fatalf("%s: the installation's project is empty", typ)
	}
	return map[string]any{
		"mode":     "managed",
		"type":     typ,
		"name":     name,
		"provider": `provider["registry.terraform.io/hashicorp/google"]`,
		"module":   "module.installation",
		"instances": []any{map[string]any{
			"schema_version":       0,
			"index_key":            index,
			"attributes":           attrs,
			"sensitive_attributes": []any{},
		}},
	}
}

// logged asks for the installation's log bucket in the plan, failing when the
// plan holds no row for it.
func logged(t *testing.T, rows map[string]plannedResource) plannedResource {
	t.Helper()
	rc, ok := rows[LogBucketAddress]
	if !ok {
		t.Fatalf("the plan holds no log bucket: %v", slices.Sorted(maps.Keys(rows)))
	}
	return rc
}

// mappedElsewhere is the location a fixture's region maps to, and it must not
// be `global`: a test whose answer was `global` would pass against the module
// that pinned it (this release's red step).
func mappedElsewhere(t *testing.T, region string) string {
	t.Helper()
	where := place.For(region).LogBucket.Where
	if where == "global" {
		t.Fatalf("place.For(%s) answers `global`, which is what this release's mapping replaces: the fixture must be a region whose answer is elsewhere", region)
	}
	return where
}

// TestANewLogBucketIsInTheRegion (LP6, D3082): the log bucket an installation
// creates from now on is in place.For's location for its region. This is the
// test the plan names as the red step — before this release the module pins
// `global`.
func TestANewLogBucketIsInTheRegion(t *testing.T) {
	spec := installationSpec(t)
	want := mappedElsewhere(t, spec.Region)
	_, rows := installationPlan(t, spec)
	if got := logged(t, rows).After["location"]; got != want {
		t.Errorf("a log bucket created by this release is in %v, want %v (place.For(%s).LogBucket.Where)", got, want, spec.Region)
	}
}

// TestAGlobalLogBucketPlansNoChange (LP6, R5, F11): an installation made before
// this mapping has its log bucket in `global`, and this release's mapping
// points elsewhere. The plan must leave it exactly where it is: the bucket's
// location is permanent, and a plan that wrote the mapping's answer over it
// would replace the bucket and delete the run output it holds. What makes the
// plan a no-op is the resource's own ignore_changes, against a state the
// provider diffs.
func TestAGlobalLogBucketPlansNoChange(t *testing.T) {
	spec := installationSpec(t)
	mappedElsewhere(t, spec.Region)
	_, rows := installationPlanAgainst(t, spec, nil, func(vars map[string]any) map[string]any {
		return logIsolationState(t, vars, "global")
	})
	rc := logged(t, rows)
	if !slices.Equal(rc.Actions, []string{"no-op"}) {
		t.Errorf("the plan acts on the bucket an installation already has (%v), which would replace it and delete the run output in it", rc.Actions)
	}
	if got := rc.After["location"]; got != "global" {
		t.Errorf("the plan leaves the bucket in %v, want `global`, where the installation made it", got)
	}
}

// TestALogBucketMovedByHandPlansNoChange (LP6, R5): the same holds for a bucket
// an operator moved somewhere else entirely — an installation's bucket is never
// given the mapping's answer, whatever the mapping happens to say now.
func TestALogBucketMovedByHandPlansNoChange(t *testing.T) {
	spec := installationSpec(t)
	elsewhere := "europe-west1"
	if place.For(spec.Region).LogBucket.Where == elsewhere {
		t.Fatalf("the fixture's mapping already points at %s", elsewhere)
	}
	_, rows := installationPlanAgainst(t, spec, nil, func(vars map[string]any) map[string]any {
		return logIsolationState(t, vars, elsewhere)
	})
	rc := logged(t, rows)
	if !slices.Equal(rc.Actions, []string{"no-op"}) {
		t.Errorf("the plan acts on a bucket that was moved by hand (%v)", rc.Actions)
	}
	if got := rc.After["location"]; got != elsewhere {
		t.Errorf("the plan leaves the bucket in %v, want %s", got, elsewhere)
	}
}

// TestForgetPlansWhatItPlansToday (LP6, F10): `fugaro update --forget` turns
// log isolation off, and the rollback's plan is exactly what it was — the
// bucket, the sink, the exclusion and the view are deleted and nothing of them
// is created. That path runs no discovery and reads no bucket, so its plan is
// the same whether the isolation it finds is in `global` or anywhere else.
func TestForgetPlansWhatItPlansToday(t *testing.T) {
	spec := ForgetSpec(installationSpec(t))
	_, rows := installationPlanAgainst(t, spec, nil, func(vars map[string]any) map[string]any {
		return logIsolationState(t, vars, "global")
	})
	// The four the rollback deletes, and each view grant beside them when the
	// installation has one (this fixture names no launcher, so the plan holds
	// none).
	for _, addr := range ForgetDeletes {
		rc, ok := rows[addr]
		if !ok {
			t.Errorf("the rollback's plan holds no change for %s", addr)
			continue
		}
		if !slices.Equal(rc.Actions, []string{"delete"}) {
			t.Errorf("the rollback plans %v for %s, want a delete", rc.Actions, addr)
		}
	}
}
