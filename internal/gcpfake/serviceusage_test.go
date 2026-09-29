package gcpfake

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// errorInfoReason is the reason of the first ErrorInfo detail of an
// error answer, and its metadata's service.
func errorInfoReason(body map[string]any) (reason, service string) {
	e, _ := body["error"].(map[string]any)
	details, _ := e["details"].([]any)
	for _, d := range details {
		m, _ := d.(map[string]any)
		if m["@type"] == "type.googleapis.com/google.rpc.ErrorInfo" {
			md, _ := m["metadata"].(map[string]any)
			s, _ := md["service"].(string)
			r, _ := m["reason"].(string)
			return r, s
		}
	}
	return "", ""
}

// A disabled API answers every call 403 SERVICE_DISABLED, naming itself,
// until an enable turns it on (after the propagation calls).
func TestServiceUsageDisablesAndEnables(t *testing.T) {
	su := NewServiceUsage(t)
	su.Propagation = 1
	sched := NewScheduler(t)
	su.Disable("cloudscheduler.googleapis.com", sched.Server)
	job := sched.URL + "/v1/projects/p/locations/us-east4/jobs/j"

	code, body := getJSON(t, job)
	if reason, service := errorInfoReason(body); code != http.StatusForbidden || reason != "SERVICE_DISABLED" || service != "cloudscheduler.googleapis.com" {
		t.Fatalf("disabled: %d %v", code, body)
	}
	resp, err := http.Post(su.URL+"/v1/projects/p/services/cloudscheduler.googleapis.com:enable", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var op map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&op)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || op["done"] != true || !su.Enabled("cloudscheduler.googleapis.com") {
		t.Fatalf("enable: %d %v", resp.StatusCode, op)
	}
	if code, _ := getJSON(t, job); code != http.StatusForbidden {
		t.Errorf("the propagation call: %d, want 403", code)
	}
	if code, _ := getJSON(t, job); code != http.StatusNotFound {
		t.Errorf("enabled: %d, want the job's 404", code)
	}
	if got := su.Enables(); !slices.Equal(got, []string{"cloudscheduler.googleapis.com"}) {
		t.Errorf("enables = %q", got)
	}
}

// A pending enable's operation is done after PendingPolls gets; enabling
// an enabled service is done at once.
func TestServiceUsagePendingOperation(t *testing.T) {
	su := NewServiceUsage(t)
	su.PendingPolls = 1
	su.Disable("x.googleapis.com")
	resp, err := http.Post(su.URL+"/v1/projects/p/services/x.googleapis.com:enable", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var op map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&op)
	resp.Body.Close()
	name, _ := op["name"].(string)
	if op["done"] == true || name == "" {
		t.Fatalf("enable: %v", op)
	}
	if _, body := getJSON(t, su.URL+"/v1/"+name); body["done"] == true {
		t.Errorf("first poll done: %v", body)
	}
	if _, body := getJSON(t, su.URL+"/v1/"+name); body["done"] != true {
		t.Errorf("second poll not done: %v", body)
	}
	resp, err = http.Post(su.URL+"/v1/projects/p/services/x.googleapis.com:enable", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	op = nil
	_ = json.NewDecoder(resp.Body).Decode(&op)
	resp.Body.Close()
	if op["done"] != true {
		t.Errorf("enabling an enabled service: %v", op)
	}
}

// Refuse answers every call with the error, its reason in an ErrorInfo.
func TestServerRefuse(t *testing.T) {
	sched := NewScheduler(t)
	sched.Refuse(http.StatusForbidden, "PERMISSION_DENIED", "IAM_PERMISSION_DENIED", "Permission denied")
	code, body := getJSON(t, sched.URL+"/v1/projects/p/locations/r/jobs/j")
	if reason, _ := errorInfoReason(body); code != http.StatusForbidden || reason != "IAM_PERMISSION_DENIED" {
		t.Fatalf("refused: %d %v", code, body)
	}
	sched.Refuse(0, "", "", "")
	if code, _ := getJSON(t, sched.URL+"/v1/projects/p/locations/r/jobs/j"); code != http.StatusNotFound {
		t.Errorf("lifted: %d", code)
	}
}
