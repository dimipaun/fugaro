package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestServicesStartWithAgentEnv: the services an image declares are started
// with the environment a stage's own tests run in — agent.BuildEnv's stripped
// one, r.env — and with the entry's list, which is the only source the run
// starts from (D1904).
//
// What is pinned is the environment the start runs WITH, not a claim that it
// is credential-free: r.env is the agent's own, and what a stage's commands
// are handed is exactly what is handed here — the run's model credential
// among it, since agent.BuildEnv puts the user's key in the agent's
// environment under api-key with the gateway off (provision.go's startServices
// says why that is the right boundary). What must NOT be the base is the job
// environment (r.d.Env): it is the runner's own, holding the git token and the
// budget marker, and a service started there would be one none of the
// repository's tests agrees with — and would wear the run's credentials too.
// And the stamp is APPENDED to the agent's environment rather than read out of
// it, because
// config.ReservedEnv drops every FUGARO_ name from that environment — an
// image's own ENV included, which is exactly the forgery the launch's
// always-written stamp exists to beat (backend.ServicesEnv).
//
// The empty stamp is the third half, and it is a refusal rather than a
// default: no entry declared a service, so the run starts none — the script's
// own default of all three is a list read from somewhere other than the entry
// (TestServicesFromEntryOnly's rule at the runner's end).
func TestServicesStartWithAgentEnv(t *testing.T) {
	work := t.TempDir()
	bin := t.TempDir()
	log := filepath.Join(work, "started")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" > " + log + "\n" +
		"env | sort >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(bin, servicesScript), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// PATH, and only that: procgroup hands the command exactly the
	// environment it was given, so a runner that passed r.d.Env would find no
	// script here at all and this test would fail on the start rather than on
	// the environment it read.
	agentPath := bin + ":" + os.Getenv("PATH")
	agentEnv := []string{"PATH=" + agentPath, "HOME=" + work, "ANTHROPIC_API_KEY=model-secret", ServicesEnv + "=postgres"}

	newRun := func(stamp string) *run {
		return &run{
			d: Deps{
				WorkDir: work,
				Env: []string{
					"GITHUB_TOKEN=from-the-job-environment",
					"FUGARO_BUDGET_TOKEN=from-the-job-environment",
					stamp,
				},
			},
			env: agentEnv,
			wf:  config.Workflow{Timeouts: config.Timeouts{Verify: config.Duration{Duration: time.Minute}}},
		}
	}

	r := newRun(ServicesEnv + "=postgres redis")
	if err := r.startServices(context.Background()); err != nil {
		t.Fatalf("startServices = %v, want the script run", err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the services script did not run: %v", err)
	}
	got := string(raw)
	if first, _, _ := strings.Cut(got, "\n"); first != "start" {
		t.Errorf("the script was called with %q, want start", first)
	}
	for _, want := range []string{
		ServicesEnv + "=postgres redis",
		"HOME=" + work,
		"PATH=" + agentPath,
		// The agent's environment reaches the process whole, the model
		// credential included: this is the environment a stage's own test
		// command starts the services under, so the two must be the same
		// one.
		"ANTHROPIC_API_KEY=model-secret",
	} {
		if !slices.Contains(strings.Split(got, "\n"), want) {
			t.Errorf("the script's environment has no %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"GITHUB_TOKEN=", "FUGARO_BUDGET_TOKEN="} {
		if strings.Contains(got, unwanted) {
			t.Errorf("the script's environment carries %s, which is the run's own credential and belongs to no backing service:\n%s", unwanted, got)
		}
	}

	// The empty stamp, written by every launch that starts a job the catalog
	// speaks for and declares no service (cli's servicesLaunchEnv).
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	r = newRun(ServicesEnv + "=")
	if err := r.startServices(context.Background()); err != nil {
		t.Fatalf("startServices with no service declared = %v, want nothing started and no error", err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("a run whose entry declares no service started one anyway: the empty stamp means none, not the script's own default")
	}
	// And an unstamped launch — a job whose image the catalog does not speak
	// for — is the same answer.
	r = newRun("")
	if err := r.startServices(context.Background()); err != nil {
		t.Fatalf("startServices on an unstamped launch = %v, want nothing started and no error", err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("an unstamped launch started a service, and no entry declared one")
	}
}
