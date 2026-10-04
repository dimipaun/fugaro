package tf

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// loadPlan reads a fixture captured from real `terraform show -json` output
// (see TestRealTerraformPlanJSONShape).
func loadPlan(t *testing.T, name string) *Plan {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	p, err := parsePlan(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ResourceChanges) == 0 {
		t.Fatalf("%s holds no resource changes", name)
	}
	return p
}

func TestGuardRefusesReplace(t *testing.T) {
	p := loadPlan(t, "plan_replace.json")
	err := Guard(p, nil)
	if err == nil {
		t.Fatal("the guard let a replace through")
	}
	// Both orders of a replace: delete first, and create_before_destroy.
	for _, want := range []string{
		"terraform_data.r (delete, create)",
		"terraform_data.c (create, delete)",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q doesn't name %s", err, want)
		}
	}
	if strings.Contains(err.Error(), "terraform_data.a") {
		t.Errorf("the refusal %q names an unchanged resource", err)
	}
}

func TestGuardRefusesDelete(t *testing.T) {
	p := loadPlan(t, "plan_delete.json")
	err := Guard(p, nil)
	if err == nil || !strings.Contains(err.Error(), "terraform_data.d (delete)") {
		t.Fatalf("err = %v, want a refusal naming terraform_data.d (delete)", err)
	}
	// Only the exact address counts.
	for _, allow := range [][]string{{"terraform_data"}, {"terraform_data.d[0]"}, {"terraform_data.dd"}, {" terraform_data.d"}} {
		if Guard(p, allow) == nil {
			t.Errorf("--allow-delete %q let terraform_data.d be deleted", allow)
		}
	}
}

func TestGuardAllowsNamedDelete(t *testing.T) {
	if err := Guard(loadPlan(t, "plan_delete.json"), []string{"terraform_data.d"}); err != nil {
		t.Errorf("named delete refused: %v", err)
	}
	// Naming one of two replaced resources still refuses the other.
	err := Guard(loadPlan(t, "plan_replace.json"), []string{"terraform_data.r"})
	if err == nil || strings.Contains(err.Error(), "terraform_data.r ") || !strings.Contains(err.Error(), "terraform_data.c") {
		t.Errorf("err = %v, want a refusal of terraform_data.c alone", err)
	}
}

func TestGuardAllowsForget(t *testing.T) {
	p := loadPlan(t, "plan_forget.json")
	if !hasAction(p, "forget") {
		t.Fatal("the fixture holds no forget")
	}
	if err := Guard(p, nil); err != nil {
		t.Errorf("a removed block was refused: %v", err)
	}
}

func TestGuardAllowsImportsAndUpdates(t *testing.T) {
	for _, name := range []string{"plan_import.json", "plan_update.json"} {
		if err := Guard(loadPlan(t, name), nil); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestGuardRefusesUnknownActionsAndNoPlan(t *testing.T) {
	var p Plan
	if err := json.Unmarshal([]byte(`{"resource_changes":[{"address":"x.y","type":"x","change":{"actions":["obliterate"]}}]}`), &p); err != nil {
		t.Fatal(err)
	}
	if err := Guard(&p, []string{"x.y"}); err == nil || !strings.Contains(err.Error(), "x.y (obliterate)") {
		t.Errorf("err = %v, want a refusal of an unknown action", err)
	}
	if err := Guard(nil, nil); err == nil {
		t.Error("the guard passed a nil plan")
	}

	// A change with no actions, as a renamed or moved field would decode,
	// is refused too.
	for _, change := range []string{`{"actions":[]}`, `{}`} {
		p, err := parsePlan([]byte(`{"format_version":"1.2","resource_changes":[{"address":"x.y","type":"x","change":` + change + `}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := Guard(p, []string{"x.y"}); err == nil || !strings.Contains(err.Error(), "x.y (no actions)") {
			t.Errorf("change %s: err = %v, want a refusal", change, err)
		}
	}
}

func TestParsePlanRequiresFormatVersionOne(t *testing.T) {
	for _, v := range []string{`"format_version":"1.2",`, `"format_version":"1.0",`} {
		if _, err := parsePlan([]byte(`{` + v + `"resource_changes":[]}`)); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
	for _, v := range []string{``, `"format_version":"",`, `"format_version":"2.0",`, `"format_version":"10.1",`, `"format_version":1.2,`} {
		if _, err := parsePlan([]byte(`{` + v + `"resource_changes":[]}`)); err == nil {
			t.Errorf("%q: parsed, want a refusal of the plan format", v)
		}
	}
}

func hasAction(p *Plan, action string) bool {
	for _, rc := range p.ResourceChanges {
		for _, a := range rc.Change.Actions {
			if a == action {
				return true
			}
		}
	}
	return false
}

func TestBeyond(t *testing.T) {
	rc := func(addr, typ string, before, after map[string]any, unknown any, acts ...string) ResourceChange {
		return ResourceChange{Address: addr, Type: typ, Change: Change{Actions: acts, Before: before, After: after, AfterUnknown: unknown}}
	}
	mem := func(role string, ms ...any) map[string]any { return map[string]any{"role": role, "members": ms} }
	for name, tc := range map[string]struct {
		changes []ResourceChange
		want    int
	}{
		"additive":               {[]ResourceChange{rc("a.b", "google_x", nil, nil, nil, "create"), rc("a.c", "google_y", nil, nil, nil, "update"), rc("a.d", "google_z", nil, nil, nil, "read", "no-op"), rc("a.e", "google_z", nil, nil, nil, "forget")}, 0},
		"delete":                 {[]ResourceChange{rc("a.b", "google_x", nil, nil, nil, "delete")}, 1},
		"replace":                {[]ResourceChange{rc("a.b", "google_x", nil, nil, nil, "delete", "create")}, 1},
		"replace, create first":  {[]ResourceChange{rc("a.b", "google_x", nil, nil, nil, "create", "delete")}, 1},
		"no actions":             {[]ResourceChange{rc("a.b", "google_x", nil, nil, nil)}, 1},
		"unknown action":         {[]ResourceChange{rc("a.b", "google_x", nil, nil, nil, "frobnicate")}, 1},
		"iam member gone":        {[]ResourceChange{rc("a.b", "google_project_iam_member", nil, nil, nil, "delete")}, 1},
		"binding gains":          {[]ResourceChange{rc("a.b", "google_project_iam_binding", mem("r", "u:a"), mem("r", "u:a", "u:b"), map[string]any{}, "update")}, 0},
		"binding loses":          {[]ResourceChange{rc("a.b", "google_project_iam_binding", mem("r", "u:a", "u:b"), mem("r", "u:a"), map[string]any{}, "update")}, 1},
		"binding role changes":   {[]ResourceChange{rc("a.b", "google_project_iam_binding", mem("r", "u:a"), mem("s", "u:a"), map[string]any{}, "update")}, 1},
		"binding unknown":        {[]ResourceChange{rc("a.b", "google_project_iam_binding", mem("r", "u:a"), mem("r"), map[string]any{"members": true}, "update")}, 1},
		"binding not comparable": {[]ResourceChange{rc("a.b", "google_project_iam_binding", nil, nil, nil, "update")}, 1},
		"policy rewritten":       {[]ResourceChange{rc("a.b", "google_project_iam_policy", nil, nil, nil, "update")}, 1},
		"policy created":         {[]ResourceChange{rc("a.b", "google_project_iam_policy", nil, nil, nil, "create")}, 0},
		"audit config":           {[]ResourceChange{rc("a.b", "google_project_iam_audit_config", nil, nil, nil, "update")}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			if got := Beyond(&Plan{ResourceChanges: tc.changes}); len(got) != tc.want {
				t.Fatalf("Beyond = %q, want %d", got, tc.want)
			}
		})
	}
	if len(Beyond(nil)) != 1 {
		t.Error("a missing plan is covered")
	}
}
