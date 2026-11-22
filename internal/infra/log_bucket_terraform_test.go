//go:build terraform

package infra

import (
	"maps"
	"slices"
	"strings"
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

// The isolation's addresses that are not the bucket's: the sink that carries
// the lines, the exclusion that keeps them out of _Default, and the view their
// readers read. ForgetDeletes names all four in this order; these are the three
// the state below needs on their own (the log view's grants are named by their
// own address prefix, forgetViewGrants).
const (
	logSinkAddress      = "module.installation.google_logging_project_sink.fugaro[0]"
	logExclusionAddress = "module.installation.google_logging_project_exclusion.fugaro_from_default[0]"
	logViewAddress      = "module.installation.google_logging_log_view.runs[0]"
)

// logIsolationConfig is the log isolation as the module plans it when nothing
// of it exists yet: the four resources the rollback deletes and one
// roles/logging.viewAccessor grant per member of the fixture's roster. The
// state below is built from it, so the fixture cannot disagree with the module
// about a field the location does not touch — a hand-written `filter`, say, is
// a field no location reaches, and a fixture that got one wrong would show the
// sink and the exclusion moving in BOTH arms of a test about the location.
func logIsolationConfig(t *testing.T, spec InstallationSpec) map[string]plannedResource {
	t.Helper()
	_, rows := installationPlan(t, spec)
	out := make(map[string]plannedResource, len(ForgetDeletes))
	for _, addr := range ForgetDeletes {
		rc, ok := rows[addr]
		if !ok {
			t.Fatalf("this fixture's spec plans no %s, so there is no state to build: %v", addr, slices.Sorted(maps.Keys(rows)))
		}
		out[addr] = rc
	}
	for addr, rc := range rows {
		if strings.HasPrefix(addr, forgetViewGrants+"[") {
			out[addr] = rc
		}
	}
	return out
}

// logIsolationAddresses is a plan's rows for the isolation, the four of
// ForgetDeletes first and the view's grants after them: the addresses a test
// reads out of a plan of an installation whose isolation is on.
func logIsolationAddresses(rows map[string]plannedResource) []string {
	out := slices.Clone(ForgetDeletes)
	for addr := range rows {
		if strings.HasPrefix(addr, forgetViewGrants+"[") {
			out = append(out, addr)
		}
	}
	slices.Sort(out[len(ForgetDeletes):])
	return out
}

// logIsolationState is the terraform state an installation whose log isolation
// is ON would have written: the resources of config — the module's own values
// for them — with every field a location is built into moved to `location`: the
// bucket's own location, the sink's destination, the view's bucket path, and
// the location of each grant. Those are what a plan that carried another
// location than the bucket's own moves, and why the location is an argument
// rather than read out of the config: this state exists to be planned against a
// spec that disagrees with it.
func logIsolationState(t *testing.T, config map[string]plannedResource, location string) map[string]any {
	t.Helper()
	bucket := config[LogBucketAddress].After
	bucketID, _ := bucket["bucket_id"].(string)
	project, _ := bucket["project"].(string)
	if project == "" || bucketID == "" {
		t.Fatalf("the fixture's log bucket names no project or bucket id: %v", bucket)
	}
	path := "projects/" + project + "/locations/" + location + "/buckets/" + bucketID
	resources := make([]any, 0, len(config))
	for _, addr := range logIsolationAddresses(config) {
		rc, ok := config[addr]
		if !ok {
			t.Fatalf("the fixture holds no %s", addr)
		}
		attrs := maps.Clone(rc.After)
		var typ, name string
		var key any
		switch {
		case addr == LogBucketAddress:
			typ, name, key = "google_logging_project_bucket_config", "fugaro", 0
			attrs["location"] = location
			attrs["id"], attrs["name"] = path, path
		case addr == logSinkAddress:
			typ, name, key = "google_logging_project_sink", "fugaro", 0
			attrs["destination"] = "logging.googleapis.com/" + path
			attrs["id"] = "projects/" + project + "/sinks/" + stateValue(t, attrs, "name")
		case addr == logExclusionAddress:
			typ, name, key = "google_logging_project_exclusion", "fugaro_from_default", 0
			attrs["id"] = "projects/" + project + "/exclusions/" + stateValue(t, attrs, "name")
		case addr == logViewAddress:
			typ, name, key = "google_logging_log_view", "runs", 0
			attrs["bucket"] = path
			attrs["id"] = path + "/views/" + stateValue(t, attrs, "name")
		default:
			typ, name = "google_logging_log_view_iam_member", "runs"
			key = attrs["member"]
			attrs["location"] = location
			attrs["id"] = path + "/views/" + stateValue(t, attrs, "name") + "/" + stateValue(t, attrs, "role") + "/" + stateValue(t, attrs, "member")
		}
		resources = append(resources, stateResource(t, typ, name, project, key, attrs))
	}
	return map[string]any{
		"version":   4,
		"serial":    1,
		"lineage":   "00000000-0000-0000-0000-000000000000",
		"outputs":   map[string]any{},
		"resources": resources,
	}
}

// stateValue is one attribute of a row of that state, as a string. The ids
// above are built from them, and an id has to be there: an object in state
// carries one, and terraform plans a REPLACEMENT of every resource whose state
// id is null (replace_paths [[id]]), which would look like the defect these
// tests are about. A missing value is a fixture that no longer matches the
// module, not something to invent.
func stateValue(t *testing.T, attrs map[string]any, name string) string {
	t.Helper()
	s, _ := attrs[name].(string)
	if s == "" {
		t.Fatalf("the module's own values carry no %s: %v", name, attrs)
	}
	return s
}

// stateResource is one resource of that state, at the instance key it is one
// instance of: the index of a count-based resource (the module's log isolation
// is `count = 1`, so 0) or the member string of a for_each one.
func stateResource(t *testing.T, typ, name, project string, indexKey any, attrs map[string]any) map[string]any {
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
			"index_key":            indexKey,
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
	config := logIsolationConfig(t, spec)
	_, rows := installationPlanAgainst(t, spec, nil, func(vars map[string]any) map[string]any {
		return logIsolationState(t, config, "global")
	})
	rc := logged(t, rows)
	if !slices.Equal(rc.Actions, []string{"no-op"}) {
		t.Errorf("the plan acts on the bucket an installation already has (%v), which would replace it and delete the run output in it", rc.Actions)
	}
	if got := rc.After["location"]; got != "global" {
		t.Errorf("the plan leaves the bucket in %v, want `global`, where the installation made it", got)
	}
}

// TestAnExistingGlobalLogBucketPlansNoChangeEverywhere (LP6, R5, F11): the
// no-op TestAGlobalLogBucketPlansNoChange finds on the bucket is one row of the
// log isolation, and the bucket's location is not only its own field. The
// sink's destination, the view's bucket path and every viewAccessor grant's
// location are built from it too, and two of those three are what a run that
// handed Terraform the mapping's answer would break: the sink is pointed at a
// bucket that does not exist, and each grant — its location is part of the
// resource — is deleted and created again in a location with no view in it, so
// the apply stops with the grants gone. The third, the view, stays where it is
// either way, because the provider compares its bucket by resource NAME (the
// last path segment, the same bucket id in both locations); and the bucket
// itself stays because of its ignore_changes. That is what makes the damage
// quiet rather than a replace Terraform would refuse.
//
// What keeps it from happening at all is the run writing the location discovery
// read into the spec, which is internal/cli's line and its own test
// (TestUpdateKeepsAnExistingInstallationOnItsOwnBucketLocation). The spec here
// carries what that run writes; the control below carries the mapping's answer
// instead and requires the sink and the grants to move, so an arm that could
// not see the change cannot pass by accident.
func TestAnExistingGlobalLogBucketPlansNoChangeEverywhere(t *testing.T) {
	spec := installationSpec(t)
	elsewhere := mappedElsewhere(t, spec.Region)
	// One viewer, so the isolation has a grant to move: the fixture's roster is
	// otherwise empty and the grant arm would prove nothing.
	spec.Viewers = []string{"user:viewer@example.com"}
	config := logIsolationConfig(t, spec)
	spec.LogBucketLocation = "global"
	plan := func(spec InstallationSpec) map[string]plannedResource {
		t.Helper()
		_, rows := installationPlanAgainst(t, spec, nil, func(vars map[string]any) map[string]any {
			return logIsolationState(t, config, "global")
		})
		return rows
	}
	rows := plan(spec)
	addresses := logIsolationAddresses(rows)
	if want := len(ForgetDeletes) + 1; len(addresses) != want {
		t.Fatalf("the plan holds %d rows of the log isolation, want %d (the bucket, the sink, the exclusion, the view and the one grant): %v",
			len(addresses), want, addresses)
	}
	for _, addr := range addresses {
		rc, ok := rows[addr]
		if !ok {
			t.Errorf("the plan holds no row for %s", addr)
			continue
		}
		if !slices.Equal(rc.Actions, []string{"no-op"}) {
			t.Errorf("the plan acts on %s (%v), for an installation whose bucket is in `global` and whose spec carries that: the location is what the row is built from, and an installation that has a bucket must keep it", addr, rc.Actions)
		}
	}

	// The control: the same state and the same roster, planned with the
	// mapping's answer — what a run that did not write discovery's reading
	// would hand Terraform. The sink and the grants move, and those are exactly
	// the rows the assertion above is worth something for.
	spec.LogBucketLocation = elsewhere
	moved := plan(spec)
	for _, addr := range addresses {
		rc, ok := moved[addr]
		if !ok {
			t.Errorf("the plan holds no row for %s", addr)
			continue
		}
		moves := addr == logSinkAddress || strings.HasPrefix(addr, forgetViewGrants+"[")
		if got := !slices.Equal(rc.Actions, []string{"no-op"}); got != moves {
			t.Errorf("%s is %v when the spec carries the mapping's answer (%s), want it to move: %v (the sink's destination and a grant's location are built from the bucket's location, and nothing else of the isolation is)", addr, rc.Actions, elsewhere, moves)
		}
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
	config := logIsolationConfig(t, spec)
	_, rows := installationPlanAgainst(t, spec, nil, func(vars map[string]any) map[string]any {
		return logIsolationState(t, config, elsewhere)
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
	spec := installationSpec(t)
	// The state of an installation whose isolation is ON, which is what the
	// rollback finds; the plan is the off spec's.
	config := logIsolationConfig(t, spec)
	_, rows := installationPlanAgainst(t, ForgetSpec(spec), nil, func(vars map[string]any) map[string]any {
		return logIsolationState(t, config, "global")
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
