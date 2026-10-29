package gcpfake

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// runServiceCall is one call to the services fake, answered as a client sees
// it: the status and the body.
func runServiceCall(t *testing.T, f *RunServices, method, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, f.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// The policy a service was given is the one getIamPolicy answers, at version
// 3, so the deployer read sees the bindings a real service carries.
func TestRunServicesServesTheStoredPolicy(t *testing.T) {
	f := NewRunServices(t)
	f.AddService("fugaro-credentials")
	f.SetServicePolicy("fugaro-credentials", Binding{Role: "roles/run.developer", Members: []string{"user:person-1@example.com"}})

	code, body := runServiceCall(t, f, http.MethodGet, "/v2/projects/proj-1234/locations/us-east5/services/fugaro-credentials:getIamPolicy?optionsRequestedPolicyVersion=3")
	if code != http.StatusOK {
		t.Fatalf("getIamPolicy: %d %s", code, body)
	}
	var pol struct {
		Version  int       `json:"version"`
		Bindings []Binding `json:"bindings"`
	}
	if err := json.Unmarshal([]byte(body), &pol); err != nil {
		t.Fatalf("json: %v\n%s", err, body)
	}
	if len(pol.Bindings) != 1 || pol.Bindings[0].Role != "roles/run.developer" ||
		len(pol.Bindings[0].Members) != 1 || pol.Bindings[0].Members[0] != "user:person-1@example.com" {
		t.Fatalf("policy = %+v", pol)
	}
}

// A service nobody granted anything on serves the empty policy, not a 404:
// the resource exists, and "no deployer" is an answer.
func TestRunServicesWithoutAPolicyServesItEmpty(t *testing.T) {
	f := NewRunServices(t)
	f.AddService("fugaro-credentials")
	code, body := runServiceCall(t, f, http.MethodGet, "/v2/projects/proj-1234/locations/us-east5/services/fugaro-credentials:getIamPolicy?optionsRequestedPolicyVersion=3")
	if code != http.StatusOK || !strings.Contains(body, `"bindings":[]`) {
		t.Fatalf("empty policy: %d %s", code, body)
	}
}

// A service the fake does not have is 404, as the real API's is: a caller
// reading a policy off a service that was never deployed must be able to tell
// that apart from a service with no grants.
func TestRunServicesUnknownServiceIsNotFound(t *testing.T) {
	f := NewRunServices(t)
	code, body := runServiceCall(t, f, http.MethodGet, "/v2/projects/proj-1234/locations/us-east5/services/fugaro-credentials:getIamPolicy?optionsRequestedPolicyVersion=3")
	if code != http.StatusNotFound || !strings.Contains(body, "NOT_FOUND") {
		t.Fatalf("unknown service: %d %s", code, body)
	}
}

// A call this fake does not implement fails the test rather than being
// answered: it speaks one call, and a client that grows a second one (a
// service get, a job's policy through this fake) is a change a test has to
// notice.
func TestRunServicesRefusesAnUnimplementedCall(t *testing.T) {
	f := NewRunServices(t)
	f.AddService("fugaro-credentials")
	var failed int
	f.failf = func(string, ...any) { failed++ }
	for _, path := range []string{
		"/v2/projects/proj-1234/locations/us-east5/services/fugaro-credentials",
		"/v2/projects/proj-1234/locations/us-east5/jobs/fugaro-x-web:getIamPolicy",
	} {
		if code, _ := runServiceCall(t, f, http.MethodGet, path); code != http.StatusBadRequest {
			t.Errorf("GET %s: %d, want 400 (the fake fails the test)", path, code)
		}
	}
	if code, _ := runServiceCall(t, f, http.MethodGet, "/v2/projects/proj-1234/locations/us-east5/services/fugaro-credentials:getIamPolicy"); code != http.StatusOK {
		t.Errorf("the call it serves: %d, want 200", code)
	}
	if failed != 2 {
		t.Fatalf("the fake failed the test %d times, want 2", failed)
	}
}
