package gcpfake

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

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

// billingAccountID is a billing account ID built at run time from the test's
// name and tag, so that no literal in this file is an account's.
func billingAccountID(t *testing.T, tag string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name() + "/" + tag))
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	return h[0:6] + "-" + h[6:12] + "-" + h[12:18]
}

func TestBillingAccountsList(t *testing.T) {
	f := NewBilling(t)
	open, closed := billingAccountID(t, "open"), billingAccountID(t, "closed")
	f.AddAccountNamed(open, "an open account", true)
	f.AddAccount(closed, false)

	// Every account is answered, the closed one too: dropping it is the
	// client's rule. The order is by ID, so a page is a page of a stable list.
	code, body := getJSON(t, f.URL+"/v1/billingAccounts")
	accounts, _ := body["billingAccounts"].([]any)
	if code != 200 || len(accounts) != 2 || body["nextPageToken"] != nil {
		t.Fatalf("the list: %d %v", code, body)
	}
	got := map[string]any{}
	for _, a := range accounts {
		m, _ := a.(map[string]any)
		name, _ := m["name"].(string)
		got[name] = m["open"]
		if name == "billingAccounts/"+open && m["displayName"] != "an open account" {
			t.Errorf("the open account's display name: %v", m)
		}
	}
	want := map[string]any{"billingAccounts/" + open: true, "billingAccounts/" + closed: false}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the accounts: %v, want %v", got, want)
	}

	// A page size and a token, as the API's own paging works.
	_, page := getJSON(t, f.URL+"/v1/billingAccounts?pageSize=1")
	if p, _ := page["billingAccounts"].([]any); len(p) != 1 {
		t.Fatalf("a page of one: %v", page)
	}
	token, _ := page["nextPageToken"].(string)
	if token == "" {
		t.Fatalf("a page of one of two accounts carries no token: %v", page)
	}
	_, last := getJSON(t, f.URL+"/v1/billingAccounts?pageSize=1&pageToken="+token)
	if p, _ := last["billingAccounts"].([]any); len(p) != 1 || last["nextPageToken"] != nil {
		t.Errorf("the last page: %v", last)
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

func TestFirebaseDBInstances(t *testing.T) {
	f := NewFirebaseDB(t)
	_, body := getJSON(t, f.URL+"/v1beta/projects/aurora-fp/locations/-/instances")
	if l, _ := body["instances"].([]any); len(l) != 0 {
		t.Errorf("a project without a database: %v", body)
	}
	f.AddInstance("aurora-fp", "https://aurora-fp-default-rtdb.firebaseio.com")
	_, body = getJSON(t, f.URL+"/v1beta/projects/aurora-fp/locations/-/instances")
	l, _ := body["instances"].([]any)
	if len(l) != 1 || l[0].(map[string]any)["databaseUrl"] != "https://aurora-fp-default-rtdb.firebaseio.com" {
		t.Errorf("instances = %v", body)
	}
}
