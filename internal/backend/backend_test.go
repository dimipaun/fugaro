package backend

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestMemoryGiB(t *testing.T) {
	for in, want := range map[string]float64{"512Mi": 0.5, "8Gi": 8, "1024Mi": 1, "32Gi": 32} {
		got, err := MemoryGiB(in)
		if err != nil || got != want {
			t.Errorf("MemoryGiB(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "8G", "8GB", "Gi", "-1Gi", "0Mi"} {
		if _, err := MemoryGiB(bad); err == nil {
			t.Errorf("MemoryGiB(%q) accepted", bad)
		}
	}
}

func TestComputeUSD(t *testing.T) {
	p := Prices{VCPUSecondUSD: 0.000018, GiBSecondUSD: 0.000002}
	// 4 vCPU, 8 GiB for an hour: 3600 × (4×0.000018 + 8×0.000002) = $0.3168.
	if got := p.ComputeUSD(4, 8, time.Hour); math.Abs(got-0.3168) > 1e-9 {
		t.Fatalf("ComputeUSD = %v", got)
	}
	if got := p.ComputeUSD(4, 8, -time.Second); got != 0 {
		t.Fatalf("a negative duration costs %v", got)
	}
}

func TestParseExecution(t *testing.T) {
	id, ok := ParseExecution("projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p")
	if !ok || id.Job != "fugaro-acme-app-web" || id.Name != "fugaro-acme-app-web-x7k2p" || id.Region != "us-east5" {
		t.Fatalf("id = %+v, %v", id, ok)
	}
	if id.String() != "projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p" {
		t.Fatalf("String = %s", id.String())
	}
	// The API may answer with the project number: still the same execution.
	if !SameExecution(id.String(), "projects/123456789/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p") {
		t.Fatal("project ID vs number must not change identity")
	}
	for _, bad := range []string{"", "fugaro-acme-app-web-x7k2p", "projects/p/locations/r/jobs/j", "projects//locations/r/jobs/j/executions/e"} {
		if _, ok := ParseExecution(bad); ok {
			t.Errorf("ParseExecution(%q) accepted", bad)
		}
	}
}

func TestExecutionFromEnvReadsGCPProject(t *testing.T) {
	env := map[string]string{"CLOUD_RUN_EXECUTION": "fugaro-acme-app-web-x7k2p", "CLOUD_RUN_JOB": "fugaro-acme-app-web", "FUGARO_GCP_PROJECT": "my-proj", "FUGARO_PROJECT": "aurora", "FUGARO_REGION": "us-east5"}
	name, err := ExecutionFromEnv(func(k string) string { return env[k] })
	if err != nil || name != "projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p" {
		t.Fatalf("name = %q, %v", name, err)
	}
	// The project name is never the GCP ID, and the GCP ID is read only
	// from FUGARO_GCP_PROJECT.
	delete(env, "FUGARO_GCP_PROJECT")
	if _, err := ExecutionFromEnv(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "FUGARO_GCP_PROJECT") {
		t.Fatalf("a missing FUGARO_GCP_PROJECT on Cloud Run: %v", err)
	}
	if name, err := ExecutionFromEnv(func(string) string { return "" }); name != "" || err != nil {
		t.Fatalf("outside Cloud Run = %q, %v", name, err)
	}
}

func TestOnCloudRunOneSignal(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if !OnCloudRun(get(map[string]string{"CLOUD_RUN_EXECUTION": "x"})) {
		t.Error("CLOUD_RUN_EXECUTION is the signal")
	}
	// Neither the job name nor the Fugaro variables say it.
	if OnCloudRun(get(map[string]string{"CLOUD_RUN_JOB": "j", "FUGARO_BACKEND": "cloud-run", "FUGARO_GCP_PROJECT": "p"})) {
		t.Error("only CLOUD_RUN_EXECUTION counts")
	}
	if OnCloudRun(get(nil)) {
		t.Error("an empty environment is not Cloud Run")
	}
}

func TestExecutionBilled(t *testing.T) {
	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	running := Execution{State: StateRunning, Started: t0}
	if got := running.Billed(t0.Add(90 * time.Second)); got != 90*time.Second {
		t.Fatalf("running Billed = %v", got)
	}
	done := Execution{State: StateSucceeded, Started: t0, Completed: t0.Add(time.Minute)}
	if got := done.Billed(t0.Add(time.Hour)); got != time.Minute {
		t.Fatalf("done Billed = %v", got)
	}
	if got := (Execution{State: StatePending}).Billed(t0); got != 0 {
		t.Fatalf("pending Billed = %v", got)
	}
	for s, terminal := range map[State]bool{StatePending: false, StateRunning: false, StateSucceeded: true, StateFailed: true, StateCancelled: true} {
		if s.Terminal() != terminal {
			t.Errorf("%s.Terminal() = %v", s, !terminal)
		}
	}
}
