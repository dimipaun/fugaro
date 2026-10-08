package gcpfake

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
)

func TestRunFakeAcceptsEveryNameForm(t *testing.T) {
	f := NewRun(t)
	f.Project, f.Region = "proj-1", "r1"
	f.AddJob("fugaro-x-web", "1", "512Mi")
	full := f.Start("fugaro-x-web")
	id, _ := backend.ParseExecution(full)
	f.SetState(id.Name, backend.StateRunning) // short
	if f.State(full) != backend.StateRunning {
		t.Fatal("short-name SetState not seen by full name")
	}
	id.GCPProject = "999"
	f.SetState(id.String(), backend.StateSucceeded) // number form
	if f.State(full) != backend.StateSucceeded {
		t.Fatal("number-form SetState not seen")
	}
	f.OnRun = func(c RunCall) { f.SetState(c.Execution, backend.StateRunning) } // must not deadlock
	resp0, err := http.Post(f.URL+"/v2/projects/proj-1/locations/r1/jobs/fugaro-x-web:run", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp0.Body.Close()
	if f.State("fugaro-x-web-2") != backend.StateRunning {
		t.Fatal("OnRun's SetState was lost")
	}
	f.ProjectNumber = "999"
	resp, err := http.Get(f.URL + "/v2/projects/proj-1/locations/r1/jobs/-/executions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"projects/999/locations/r1/jobs/fugaro-x-web/executions/fugaro-x-web-1"`) {
		t.Fatalf("list did not use the project number: %s", body)
	}
	if got := f.Executions(); len(got) != 2 || got[0] != "fugaro-x-web-1" || got[1] != "fugaro-x-web-2" {
		t.Fatalf("Executions = %v", got)
	}
}

func TestLoggingFakeRefusesUnknownFilters(t *testing.T) {
	l := NewLogging(t)
	var failed string
	l.failf = func(format string, args ...any) { failed = format }
	resp, err := http.Post(l.URL+"/v2/entries:list", "application/json",
		strings.NewReader(`{"resourceNames":["projects/p"],"filter":"severity>=ERROR"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || failed == "" {
		t.Fatalf("status %d, failed %q", resp.StatusCode, failed)
	}
}

// TestRunFakeSetOnRunIsSynchronized sets OnRun while :run requests are
// served. Under -race it fails for a plain assignment to OnRun, which is
// what a test whose requests come from a child process needs: the race
// detector can't see that the assignment happened before them.
func TestRunFakeSetOnRunIsSynchronized(t *testing.T) {
	f := NewRun(t)
	f.AddJob("fugaro-x-web", "1", "512Mi")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 10 {
			resp, err := http.Post(f.URL+"/v2/projects/p/locations/r1/jobs/fugaro-x-web:run", "application/json", strings.NewReader(`{}`))
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
		}
	}()
	var calls atomic.Int32
	for range 10 {
		f.SetOnRun(func(RunCall) { calls.Add(1) })
	}
	<-done
	f.SetOnRun(func(RunCall) { calls.Add(1) })
	resp, err := http.Post(f.URL+"/v2/projects/p/locations/r1/jobs/fugaro-x-web:run", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if calls.Load() == 0 {
		t.Fatal("OnRun set by SetOnRun was never called")
	}
}

func TestRunFakePatchesAJobWithItsEtag(t *testing.T) {
	f := NewRun(t)
	f.SetJob("fugarochk-x", map[string]string{"fugaro": "managed"}, "img:1")
	f.SetJobEnv("fugarochk-x", map[string]string{"A": "1", "FUGARO_CHECK_SPEC": "old"})
	const path = "/v2/projects/proj-1/locations/r1/jobs/fugarochk-x"
	get := func() map[string]any {
		resp, err := http.Get(f.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var j map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&j); err != nil {
			t.Fatal(err)
		}
		return j
	}
	patch := func(j map[string]any) int {
		data, _ := json.Marshal(j)
		req, _ := http.NewRequest(http.MethodPatch, f.URL+path, strings.NewReader(string(data)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	j := get()
	etag, _ := j["etag"].(string)
	if etag == "" {
		t.Fatalf("no etag: %v", j)
	}
	c := j["template"].(map[string]any)["template"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	c["image"] = "img:2"
	c["env"] = []any{map[string]any{"name": "A", "value": "1"}, map[string]any{"name": "FUGARO_CHECK_SPEC", "value": "new"}}
	if code := patch(j); code != http.StatusOK {
		t.Fatalf("patch: %d", code)
	}
	after := get()
	ac := after["template"].(map[string]any)["template"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	if ac["image"] != "img:2" || after["etag"] == etag || len(f.Patches()) != 1 {
		t.Fatalf("after: %v, patches %d", after, len(f.Patches()))
	}
	if env := ac["env"].([]any); env[1].(map[string]any)["value"] != "new" {
		t.Fatalf("env %v", env)
	}
	// The etag read before the first patch is stale now.
	if code := patch(j); code != http.StatusConflict {
		t.Fatalf("stale etag: %d", code)
	}
}
