package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestProfileDefaultsToStandard: a workflow that names no job profile runs on
// DefaultJobProfile. The mutation is a default of "" (which the policy would
// have to interpret) or of a profile the policy is not required to define.
func TestProfileDefaultsToStandard(t *testing.T) {
	var w Workflow
	if got := w.JobProfileOrDefault(); got != "standard" {
		t.Errorf("an unset job profile defaulted to %q, want standard", got)
	}
	w.JobProfile = "big"
	if got := w.JobProfileOrDefault(); got != "big" {
		t.Errorf("a named job profile became %q", got)
	}
	if DefaultJobProfile != "standard" {
		t.Errorf("DefaultJobProfile = %q, want standard", DefaultJobProfile)
	}
}

// TestJobProfileDoesNotStealTheLayerProfileKey: job_profile and profile are two
// different selections — the project layer's workflow template and the
// project policy's job profile — and one key cannot carry both. A repository
// that uses a layer profile (profile: server) must not thereby also select a
// job profile called server.
func TestJobProfileDoesNotStealTheLayerProfileKey(t *testing.T) {
	var w Workflow
	if err := yaml.Unmarshal([]byte("profile: server\njob_profile: big\n"), &w); err != nil {
		t.Fatal(err)
	}
	if w.Profile != "server" {
		t.Errorf("profile = %q, want the layer profile server", w.Profile)
	}
	if w.JobProfile != "big" {
		t.Errorf("job_profile = %q, want big", w.JobProfile)
	}
	// And a workflow that names only a layer profile keeps the default job
	// profile.
	var onlyLayer Workflow
	if err := yaml.Unmarshal([]byte("profile: server\n"), &onlyLayer); err != nil {
		t.Fatal(err)
	}
	if onlyLayer.JobProfileOrDefault() != "standard" {
		t.Errorf("job profile = %q, want the default", onlyLayer.JobProfileOrDefault())
	}
}

// TestValidateRefusesAMisspeltJobProfile: the name is held to the rule the
// policy holds its own profile names to, so a selection that could never name
// a defined profile fails where it was written.
func TestValidateRefusesAMisspeltJobProfile(t *testing.T) {
	c := &Config{Workflows: map[string]Workflow{"web": {JobProfile: "Not A Profile"}}}
	var found bool
	for _, p := range Validate(c) {
		if strings.HasSuffix(p.Path, "job_profile") {
			found = true
		}
	}
	if !found {
		t.Fatal("Validate accepted job_profile: \"Not A Profile\"")
	}
	// And a name the rule accepts produces no job_profile problem. (A bare
	// Config has plenty of other problems; only this key's is under test.)
	valid := &Config{Workflows: map[string]Workflow{"web": {JobProfile: "standard-bitbucket"}}}
	for _, p := range Validate(valid) {
		if strings.HasSuffix(p.Path, "job_profile") {
			t.Fatalf("Validate refused the valid job profile standard-bitbucket: %s", p)
		}
	}
}
