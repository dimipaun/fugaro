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
