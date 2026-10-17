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

// TestGuardMatchesTheWholeAddressOnly: an approved address approves that
// address and nothing it is a prefix, a suffix or a substring of, nothing
// that differs from it in case or by a space, and nothing under it in a
// module. Each row is a delete the guard must refuse.
func TestGuardMatchesTheWholeAddressOnly(t *testing.T) {
	for _, tc := range []struct{ approved, planned string }{
		{"google_storage_bucket.data", "google_storage_bucket.data_old"},
		{"module.web", "module.web.google_sql_database_instance.db"},
		{`google_storage_bucket.x`, `google_storage_bucket.x["ab"]`},
		{`x["ab"]`, `x["a"]`},
		{`x["a"]`, `x["ab"]`},
		{"module.a.google_storage_bucket.data", "module.b.google_storage_bucket.data"},
		{"google_storage_bucket.data", "module.old.google_storage_bucket.data"},
		{"google_storage_bucket.data", "module.old.google_storage_bucket.data_v2"},
		{"Google_Storage_Bucket.Data", "google_storage_bucket.data"},
		{"google_storage_bucket.data ", "google_storage_bucket.data"},
		{"google_storage_bucket.data_old", "google_storage_bucket.data"},
	} {
		p := &Plan{FormatVersion: "1.2", ResourceChanges: []ResourceChange{{
			Address: tc.planned, Type: "google_storage_bucket", Change: Change{Actions: []string{"delete"}},
		}}}
		if Guard(p, []string{tc.approved}) == nil {
			t.Errorf("approving %q let %q be deleted", tc.approved, tc.planned)
		}
		if err := Guard(p, []string{tc.planned}); err != nil {
			t.Errorf("approving %q itself was refused: %v", tc.planned, err)
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

// movedDeletePlanJSON is the plan terraform writes for
// `moved { from = google_storage_bucket.a, to = google_storage_bucket.b }`
// when b is in neither the state nor the configuration: the change is at b,
// its object — a's, in before — came from a, and the action is a delete with
// the reason delete_because_no_move_target. Decoded from JSON rather than
// built as a struct, so the fields' tags are what the test reads.
const movedDeletePlanJSON = `{"format_version":"1.2","resource_changes":[{
  "address":"google_storage_bucket.b","previous_address":"google_storage_bucket.a",
  "mode":"managed","type":"google_storage_bucket","name":"b",
  "provider_name":"registry.terraform.io/hashicorp/google",
  "action_reason":"delete_because_no_move_target",
  "change":{"actions":["delete"],"before":{"name":"fugaro-m-a"},"after":null,"after_unknown":{}}}]}`

// TestGuardRefusesAMovedDeleteUnderAListedAddress: approving b names what may
// be removed at b; the plan above removes a's object under b's address, and
// the guard refuses it although b is listed.
func TestGuardRefusesAMovedDeleteUnderAListedAddress(t *testing.T) {
	p, err := parsePlan([]byte(movedDeletePlanJSON))
	if err != nil {
		t.Fatal(err)
	}
	rc := p.ResourceChanges[0]
	if rc.PreviousAddress != "google_storage_bucket.a" || rc.ActionReason != "delete_because_no_move_target" {
		t.Fatalf("the plan decodes previous_address %q and action_reason %q", rc.PreviousAddress, rc.ActionReason)
	}
	err = Guard(p, []string{"google_storage_bucket.b"})
	if err == nil || !strings.Contains(err.Error(), "moved here from google_storage_bucket.a") {
		t.Fatalf("err = %v, want the moved delete refused although its address is listed", err)
	}
	// Each signal alone refuses it: the previous address without the reason
	// (a moved replace carries no delete_because_* reason), and the reason
	// without the previous address.
	for name, mut := range map[string]func(*ResourceChange){
		"previous_address alone, a replace": func(rc *ResourceChange) {
			rc.ActionReason = "replace_because_cannot_update"
			rc.Change.Actions = []string{"delete", "create"}
		},
		"previous_address alone, create first": func(rc *ResourceChange) {
			rc.ActionReason = ""
			rc.Change.Actions = []string{"create", "delete"}
		},
		"delete_because_no_move_target alone": func(rc *ResourceChange) { rc.PreviousAddress = "" },
	} {
		t.Run(name, func(t *testing.T) {
			q := &Plan{FormatVersion: "1.2", ResourceChanges: []ResourceChange{rc}}
			mut(&q.ResourceChanges[0])
			if Guard(q, []string{rc.Address}) == nil {
				t.Errorf("a listed address admitted %+v", q.ResourceChanges[0])
			}
		})
	}
}

// TestGuardAdmitsTheListedAddressOwnRemovals: what the moved rule must not
// refuse — a previous_address equal to the address, a deposed object of the
// listed address, and every delete_because_* reason that removes the
// address's own object.
func TestGuardAdmitsTheListedAddressOwnRemovals(t *testing.T) {
	base := ResourceChange{Address: `google_storage_bucket.b["k"]`, Mode: "managed", Type: "google_storage_bucket",
		Change: Change{Actions: []string{"delete"}, Before: map[string]any{"name": "x"}}}
	cases := map[string]func(*ResourceChange){
		"previous_address is the address": func(rc *ResourceChange) { rc.PreviousAddress = rc.Address },
		"a deposed object":                func(rc *ResourceChange) { rc.Deposed = "00000001" },
	}
	for _, r := range []string{"delete_because_no_resource_config", "delete_because_wrong_repetition",
		"delete_because_count_index", "delete_because_each_key", "delete_because_no_module"} {
		cases[r] = func(rc *ResourceChange) { rc.ActionReason = r }
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			rc := base
			mut(&rc)
			p := &Plan{FormatVersion: "1.2", ResourceChanges: []ResourceChange{rc}}
			if err := Guard(p, []string{rc.Address}); err != nil {
				t.Errorf("a listed removal of the address's own object refused: %v", err)
			}
		})
	}
}
