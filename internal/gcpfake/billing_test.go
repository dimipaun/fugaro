package gcpfake

import "testing"

func TestBillingInfo(t *testing.T) {
	f := NewBilling(t)
	f.SetBilling("aurora-fp", true)
	f.SetBilling("nobill-fp", false)
	code, body := getJSON(t, f.URL+"/v1/projects/aurora-fp/billingInfo")
	if code != 200 || body["billingEnabled"] != true || body["billingAccountName"] == nil {
		t.Errorf("billed: %d %v", code, body)
	}
	code, body = getJSON(t, f.URL+"/v1/projects/nobill-fp/billingInfo")
	if code != 200 || body["billingEnabled"] == true || body["billingAccountName"] != nil {
		t.Errorf("unbilled: %d %v", code, body)
	}
	if code, _ = getJSON(t, f.URL+"/v1/projects/unknown/billingInfo"); code != 403 {
		t.Errorf("unknown project: %d (the real API does not say a project is missing)", code)
	}
}

func TestCRMPolicyAndLifecycle(t *testing.T) {
	f := NewCRM(t)
	f.AddProject("proj-1234", 1)
	f.SetPolicy("proj-1234", Binding{Role: "roles/owner", Members: []string{"user:a@example.com"}})
	code, body := post(t, f.URL+"/v1/projects/proj-1234:getIamPolicy", "application/json", "", []byte(`{}`))
	bs, _ := body["bindings"].([]any)
	if code != 200 || len(bs) != 1 {
		t.Errorf("policy: %d %v", code, body)
	}
	if code, _ = post(t, f.URL+"/v1/projects/other:getIamPolicy", "application/json", "", []byte(`{}`)); code != 403 {
		t.Errorf("unknown project: %d", code)
	}
	if _, body = getJSON(t, f.URL+"/v1/projects/proj-1234"); body["lifecycleState"] != "ACTIVE" {
		t.Errorf("default state: %v", body)
	}
	f.SetLifecycle("proj-1234", "DELETE_REQUESTED")
	if _, body = getJSON(t, f.URL+"/v1/projects/proj-1234"); body["lifecycleState"] != "DELETE_REQUESTED" {
		t.Errorf("state: %v", body)
	}
}

func TestRTDBRulesAndShallow(t *testing.T) {
	f := NewRTDB(t)
	f.Set("a/b", 1)
	f.Set("c", "x")
	code, body := getJSON(t, f.URL+"/.json?shallow=true")
	if code != 200 || body["a"] != true || body["c"] != true || len(body) != 2 {
		t.Errorf("shallow: %d %v", code, body)
	}
	if f.Rules() != nil && len(f.Rules()) != 0 || f.RulePuts() != 0 {
		t.Error("rules before any deployment")
	}
}
