package gcpfake

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A policy with a condition is served only at version 3, as the real APIs
// do, so a client that forgets to ask for it fails instead of silently
// reading a policy without its conditions.
func TestPolicyNeedsVersion3ForConditions(t *testing.T) {
	g := NewGCS(t)
	g.AddBucket("b", 1, nil)
	g.SetBucketPolicy("b", []Binding{{Role: "roles/storage.objectUser", Members: []string{"user:a@example.com"},
		Condition: &IAMCondition{Title: "t", Expression: "true"}}})
	get := func(query string) (int, map[string]any) {
		t.Helper()
		resp, err := http.Get(g.URL + "/storage/v1/b/b/iam" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body
	}
	if code, _ := get(""); code != http.StatusBadRequest {
		t.Errorf("without version 3: %d, want 400", code)
	}
	code, body := get("?optionsRequestedPolicyVersion=3")
	if code != http.StatusOK || body["version"] != float64(3) {
		t.Errorf("with version 3: %d %v", code, body)
	}

	sm := NewSecrets(t)
	sm.Seed("s", nil, nil)
	sm.SetPolicy("s", []Binding{{Role: "roles/secretmanager.secretAccessor", Members: []string{"user:a@example.com"},
		Condition: &IAMCondition{Title: "t", Expression: "true"}}})
	for q, want := range map[string]int{"": http.StatusBadRequest, "?options.requestedPolicyVersion=3": http.StatusOK} {
		resp, err := http.Get(sm.URL + "/v1/projects/p/secrets/s:getIamPolicy" + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("secret policy %q: %d, want %d", q, resp.StatusCode, want)
		}
	}
}
