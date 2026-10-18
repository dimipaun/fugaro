package gcpfake

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A shared project job is one job for every repository admitted to a profile
// (design §4.1), so its name no longer says which run an execution is. The
// fake therefore holds a launch of one to the run's own FUGARO_RUN override,
// and merges the job's own environment under it, as Cloud Run does.
func TestProjectJobIsRegisteredByTripleAndRequiresTheRunsOwnEnv(t *testing.T) {
	f := NewRun(t)
	const job = "fugaro-p-standard-github-1a2b3c4d5e6f"
	f.AddProjectJob(job, "standard", "github", "", "2", "4Gi")
	if profile, provider, account, ok := f.ProjectJobOf(job); !ok || profile != "standard" || provider != "github" || account != "" {
		t.Fatalf("ProjectJobOf(%q) = %q, %q, %q, %v", job, profile, provider, account, ok)
	}
	if _, _, _, ok := f.ProjectJobOf("fugaro-acme-app-web-1a2b3c4d"); ok {
		t.Fatal("a per-repository job was reported as a project job")
	}
	// Without FUGARO_RUN nothing says which run an execution of a shared job
	// is, and every view of the run reads it: the launch is refused.
	if code, body := projectRun(t, f, job, `{}`); code == http.StatusOK {
		t.Fatalf("a launch of a project job without FUGARO_RUN was accepted: %s", body)
	}
	if n := len(f.Executions()); n != 0 {
		t.Fatalf("%d executions after the refused launch, want none", n)
	}
	// With it, the execution reports the run — and the job's own environment
	// stays under the override's, the override winning by name, which is how
	// Cloud Run merges a run's overrides into the job's template.
	f.SetJobEnv(job, map[string]string{"FUGARO_PROJECT": "aurora", "FUGARO_RUN": "a stale value from a hand-run execution"})
	const run = "github-acme-app-1a2b3c/20261016-100000-aaaa"
	body := `{"overrides":{"containerOverrides":[{"env":[{"name":"FUGARO_RUN","value":"` + run + `"}]}]}}`
	if code, out := projectRun(t, f, job, body); code != http.StatusOK {
		t.Fatalf("launch with FUGARO_RUN: %d %s", code, out)
	}
	env := executionEnv(t, f, job)
	if env["FUGARO_RUN"] != run {
		t.Errorf("the execution reports FUGARO_RUN %q, want %q", env["FUGARO_RUN"], run)
	}
	if env["FUGARO_PROJECT"] != "aurora" {
		t.Errorf("the job's own FUGARO_PROJECT is not in the execution's env: %v", env)
	}
}

// projectRun posts a :run of job and returns the status and the body.
func projectRun(t *testing.T, f *Run, job, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(f.URL+"/v2/projects/proj-1/locations/r1/jobs/"+job+":run", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// executionEnv is the env of the first execution of job, as jobs get of it
// reports the container's environment.
func executionEnv(t *testing.T, f *Run, job string) map[string]string {
	t.Helper()
	resp, err := http.Get(f.URL + "/v2/projects/proj-1/locations/r1/jobs/" + job + "/executions/" + job + "-1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e struct {
		Template struct {
			Containers []struct {
				Env []struct{ Name, Value string }
			}
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatal(err)
	}
	if len(e.Template.Containers) == 0 {
		t.Fatal("the execution reports no container")
	}
	out := map[string]string{}
	for _, kv := range e.Template.Containers[0].Env {
		out[kv.Name] = kv.Value
	}
	return out
}
