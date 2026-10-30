//go:build terraform

package terraform

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The flip list's two tests (B1.8, D500, G9): what the drop DOES, not what it
// is spelled like. The module's list is empty on this branch, and with it empty
// no plan can tell a proxied repository from any other, so these read
// modules/workflow/sa.tf's own locals, fill the list the way B1.13 part two
// fills it, and evaluate them. That is the only state in which the drop is
// observable at all, and B1.13 part two's red step is this test with the real
// list: it fails while the list is empty.
//
// They need the terraform binary, so they carry the build tag the rest of the
// tree's real-binary tests carry: `go test -tags terraform ./deploy/terraform/`.
// Nothing is downloaded and no credentials are needed — the configuration is
// the module's locals, its variables stubbed, and two outputs, so there is no
// provider in it.

// proxiedModulePlan evaluates sa.tf's locals with proxied_git_secret_names =
// ["bitbucket-token"] and returns the job's accessor set (job_secret_names) and
// its container's mounts (job_mounts) for one repository. proxied is the
// per-repository fact; service is whether the installation has the credentials
// service, whose absence keeps every accessor and every mount (G9, D147).
//
// The workflow is the Bitbucket one the repo root's fixtures carry: the
// repository's own probe secret, the model credential every job of an opted-in
// installation gives up (C1.18), and the Bitbucket token this list is about.
func proxiedModulePlan(t *testing.T, proxied, service bool) (accessors []string, mounts map[string]string) {
	t.Helper()
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Fatalf("this test needs terraform on PATH: %v", err)
	}
	const path = "gcp/modules/workflow/sa.tf"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := stripComments(string(b))
	i := regexp.MustCompile(`(?m)^locals \{`).FindStringIndex(src)
	if i == nil {
		t.Fatalf("%s declares no locals block", path)
	}
	open := i[1] - 1
	end := matchBrace(src, open)
	if end < 0 {
		t.Fatalf("%s: unbalanced braces in its locals block", path)
	}
	locals := "locals {" + src[open+1:end] + "}"
	for _, name := range []string{"is_proxied_git", "is_service_held", "job_secret_names", "job_mounts"} {
		if !strings.Contains(locals, name) {
			t.Fatalf("%s: the locals block this test evaluates declares no %s, so it is not reading the module's own rule", path, name)
		}
	}
	filled := strings.Replace(locals, "proxied_git_secret_names = []", `proxied_git_secret_names = ["bitbucket-token"]`, 1)
	if filled == locals {
		t.Fatalf("%s: the flip list is no longer the empty literal this test fills, so the evaluation below would plan the inert state and prove nothing", path)
	}

	dir := t.TempDir()
	main := `variable "workflow" { type = any }
variable "credentials_service" { default = null }
variable "bitbucket_proxied" { type = bool }

` + filled + `
output "job_secret_names" { value = local.job_secret_names }
output "job_mounts" { value = local.job_mounts }
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}
	tfvars := map[string]any{
		"workflow": map[string]any{"secret_env": map[string]string{
			"CLAUDE_CODE_OAUTH_TOKEN": "claude-oauth-token",
			"FUGARO_BITBUCKET_TOKEN":  "bitbucket-token",
			"SANDBOX_PROBE":           "sandbox-probe",
		}},
		"bitbucket_proxied": proxied,
	}
	if service {
		tfvars["credentials_service"] = map[string]string{"name": "fugaro-credentials", "region": "us-east5"}
	}
	data, err := json.Marshal(tfvars)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfvars.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"-chdir=" + dir}, args...)...)
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			t.Fatalf("terraform %s: %v\n%s\n%s", strings.Join(args, " "), err, out.String(), errOut.String())
		}
		return out.Bytes()
	}
	run("init", "-backend=false", "-input=false")
	run("plan", "-input=false", "-out=plan.bin")

	var doc struct {
		PlannedValues struct {
			Outputs map[string]struct {
				Value json.RawMessage `json:"value"`
			} `json:"outputs"`
		} `json:"planned_values"`
	}
	if err := json.Unmarshal(run("show", "-json", "plan.bin"), &doc); err != nil {
		t.Fatalf("reading the plan's JSON: %v", err)
	}
	decode := func(name string, into any) {
		t.Helper()
		o, ok := doc.PlannedValues.Outputs[name]
		if !ok {
			t.Fatalf("the plan has no %s output: the locals this test evaluates no longer declare it", name)
		}
		if err := json.Unmarshal(o.Value, into); err != nil {
			t.Fatalf("decoding %s: %v", name, err)
		}
	}
	decode("job_secret_names", &accessors)
	decode("job_mounts", &mounts)
	slices.Sort(accessors)
	return accessors, mounts
}

// TestFilledProxiedListDropsMountAndAccessor is what the flip buys: with the
// list filled, a proxied repository's job loses its accessor on
// bitbucket-token and its container loses the mount — together, because a
// mount the account cannot resolve is a job Cloud Run refuses to create (C2.3).
// The workflow's own probe secret and the model credential's MOUNT are what
// stays: C1.20 is the task that removes the model mounts, and this list has
// nothing to do with either.
//
// The second half is G9: the drop is the opt-in AND the repository. With the
// installation's credentials service off, a proxied repository's job keeps
// both — there is no service to broker the credential, so the token must stay
// with the job that reads it.
func TestFilledProxiedListDropsMountAndAccessor(t *testing.T) {
	accessors, mounts := proxiedModulePlan(t, true, true)
	if want := []string{"sandbox-probe"}; !slices.Equal(accessors, want) {
		t.Errorf("a proxied repository's job keeps accessor on %v, want %v: the Bitbucket token's accessor is what the flip takes away, and the model credential's goes with C1.18's rule whatever the list says", accessors, want)
	}
	want := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "claude-oauth-token", "SANDBOX_PROBE": "sandbox-probe"}
	if !mapsEqual(mounts, want) {
		t.Errorf("a proxied repository's container mounts %v, want %v: the Bitbucket token's mount goes with its accessor, and nothing else moves (C2.3, C1.20 is the model mounts' task)", mounts, want)
	}

	offAccessors, offMounts := proxiedModulePlan(t, true, false)
	if want := []string{"bitbucket-token", "claude-oauth-token", "sandbox-probe"}; !slices.Equal(offAccessors, want) {
		t.Errorf("with the credentials service off, a proxied repository's job keeps accessor on %v, want %v: the flip is gated on the opt-in, and without the service there is nothing to broker the token (G9, D147)", offAccessors, want)
	}
	if want := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "claude-oauth-token", "FUGARO_BITBUCKET_TOKEN": "bitbucket-token", "SANDBOX_PROBE": "sandbox-probe"}; !mapsEqual(offMounts, want) {
		t.Errorf("with the credentials service off, a proxied repository's container mounts %v, want %v: nothing changes for an installation that has not opted in (G9, D147)", offMounts, want)
	}
}

// TestFilledProxiedListKeepsNonProxiedRepositories is G9's other half, and the
// one that makes the flip safe: with the same filled list, a repository the
// published node does NOT admit as proxied keeps its accessor and its mount.
// The model credential's accessor still goes — that half is identified by
// variable and is every repository's — so the only difference from the run
// above is the Bitbucket token, which is exactly the input's effect.
func TestFilledProxiedListKeepsNonProxiedRepositories(t *testing.T) {
	accessors, mounts := proxiedModulePlan(t, false, true)
	if want := []string{"bitbucket-token", "sandbox-probe"}; !slices.Equal(accessors, want) {
		t.Errorf("a non-proxied repository's job keeps accessor on %v, want %v: the flip touches proxied repositories alone, or every Bitbucket repository of an opted-in installation would lose the token its runner reads (G9)", accessors, want)
	}
	want := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "claude-oauth-token", "FUGARO_BITBUCKET_TOKEN": "bitbucket-token", "SANDBOX_PROBE": "sandbox-probe"}
	if !mapsEqual(mounts, want) {
		t.Errorf("a non-proxied repository's container mounts %v, want %v: the mount goes with the accessor, and neither moves for a repository nobody proxied (G9)", mounts, want)
	}
}

func mapsEqual(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
