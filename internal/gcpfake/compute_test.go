package gcpfake

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

// The compute fake scopes an instance to its project and zone, as the API
// does: an aggregated list of another project is empty and a delete naming
// another zone is NOT_FOUND, so a reaper that lost either on the way to a
// request fails here rather than in a project.
func TestComputeFakeScopesInstancesByProjectAndZone(t *testing.T) {
	f := NewCompute(t)
	f.Project, f.Zones = "p", []string{"r-a", "r-b"}
	f.AddInstance("r-a", "vm-1", map[string]string{"fugaro": "managed"})

	items := listInstances(t, f, "p", `labels.fugaro = "managed"`)
	if got := len(items["zones/r-a"]); got != 1 {
		t.Fatalf("r-a holds %d instances, want 1: %v", got, items)
	}
	if got := len(items["zones/r-b"]); got != 0 {
		t.Fatalf("r-b holds %d instances, want 0: %v", got, items)
	}
	if items2 := listInstances(t, f, "other", `labels.fugaro = "managed"`); len(items2) != 0 {
		t.Fatalf("another project's listing = %v, want none", items2)
	}
	// A delete in the wrong zone is NOT_FOUND, and the instance survives.
	if code := doDelete(t, f, "p", "r-b", "vm-1"); code != http.StatusNotFound {
		t.Fatalf("delete in the wrong zone answered %d, want 404", code)
	}
	if got := f.Instances(); len(got) != 1 {
		t.Fatalf("instances = %v, want the instance untouched", got)
	}
	if code := doDelete(t, f, "p", "r-a", "vm-1"); code != http.StatusOK {
		t.Fatalf("delete answered %d, want 200", code)
	}
	if got := f.Deletes(); len(got) != 1 || got[0] != "r-a/vm-1" {
		t.Fatalf("deletes = %v, want [r-a/vm-1]", got)
	}
	if got := f.Instances(); len(got) != 0 {
		t.Fatalf("instances after the delete = %v, want none", got)
	}
}

// The filter is answered, and IgnoreFilter is what a caller's own client-side
// check is exercised against: an instance the filter does not match is still
// returned, so the caller must refuse it itself.
func TestComputeFakeFilterAndIgnoreFilter(t *testing.T) {
	f := NewCompute(t)
	f.Project = "p"
	f.AddInstance("r-a", "ours", map[string]string{"fugaro": "managed", "fugaro_run": "acme-app-x"})
	f.AddInstance("r-a", "theirs", map[string]string{"fugaro": "someone-else"})

	items := listInstances(t, f, "p", `labels.fugaro = "managed"`)
	if got := instancesOf(items); len(got) != 1 || got[0]["name"] != "ours" {
		t.Fatalf("filtered listing = %v, want only ours", got)
	}
	// An AND of two label equalities is the other shape the reaper may send.
	items = listInstances(t, f, "p", `labels.fugaro = "managed" AND labels.fugaro_run = "acme-app-x"`)
	if got := instancesOf(items); len(got) != 1 || got[0]["name"] != "ours" {
		t.Fatalf("two-clause filtered listing = %v, want only ours", got)
	}
	f.IgnoreFilter = true
	items = listInstances(t, f, "p", `labels.fugaro = "managed"`)
	if got := instancesOf(items); len(got) != 2 {
		t.Fatalf("a server ignoring the filter returned %v, want both instances", got)
	}
}

// The fake fails a call it does not implement rather than answering one, so a
// client that starts sending a third Compute call is noticed. instances.insert
// is the one such call the reaper must never make.
func TestComputeFakeRefusesWhatItDoesNotImplement(t *testing.T) {
	f := NewCompute(t)
	failed := false
	f.failf = func(string, ...any) { failed = true }
	resp, err := http.Post(f.URL+"/compute/v1/projects/p/zones/r-a/instances", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !failed {
		t.Fatal("an unimplemented call was not reported")
	}
}

func listInstances(t *testing.T, f *Compute, project, filter string) map[string][]map[string]any {
	t.Helper()
	u := f.URL + "/compute/v1/projects/" + project + "/aggregated/instances?filter=" + url.QueryEscape(filter)
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Items map[string]struct {
			Instances []map[string]any `json:"instances"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("%s: %v", u, err)
	}
	out := map[string][]map[string]any{}
	for scope, s := range body.Items {
		out[scope] = s.Instances
	}
	return out
}

func instancesOf(items map[string][]map[string]any) []map[string]any {
	var out []map[string]any
	for _, s := range items {
		out = append(out, s...)
	}
	return out
}

func doDelete(t *testing.T, f *Compute, project, zone, name string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, f.URL+"/compute/v1/projects/"+project+"/zones/"+zone+"/instances/"+name, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
