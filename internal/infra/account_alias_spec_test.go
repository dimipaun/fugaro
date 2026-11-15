package infra

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The job set is one of the readers an alias is resolved at (docs/account-aliases, D2091): the
// stage keys and the project default may name one, and the variants this file builds are the ones
// Terraform deploys — a variant read by its own alias would leave the account with no job at all
// while the node the runner reads the name off still sends runs to it, which is the direction that
// costs a run. The tests here are the variant a repoint builds and the old target's variant it does
// not delete (F9, D2095).

// aliasSpecLC is the origin installation under an alias: the same repository, the same two accounts
// of it, and a review stage naming the alias instead of the account. A repoint is one edit of this
// block and no other line (D2093), so the two specs below differ in that edit alone.
func aliasSpecLC(t *testing.T, target string) *localcfg.Config {
	t.Helper()
	return parseLC(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry_host: us-east5-docker.pkg.dev/proj-1234
base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc}
repos:
  acme/webapp: { provider: github, base_branch: main, workflows: [go], github_app_id: "123456" }
claude_accounts:
  bmb: { allow_data_to: [acme/webapp] }
  other: { allow_data_to: [acme/webapp] }
claude_account_aliases:
  reviewer: `+target+`
stages:
  review: { claude_account: reviewer }
`)
}

// aliasSpec is the repository spec the origin's installation resolves through an alias standing for
// target: an api-key repository whose review stage names the alias, so the account it stands for is
// the one an entry of the job set belongs to.
func aliasSpec(t *testing.T, target string) RepoSpec {
	t.Helper()
	out := installationOutputs()
	out.Operators = []string{originMember}
	out.Launchers = []string{originMember}
	rs, err := Repo(Inputs{
		LC:           aliasSpecLC(t, target),
		Repo:         "acme/webapp",
		Cfg:          parseCfg(t, []byte(originCfg(`agent: { auth: api-key }`))),
		Installation: out,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// TestJobSetBuildsTheTargetVariant: the variant the job set builds is the account the alias stands
// for, so a run the launch and the node send to that account finds a job with its token mounted —
// and no variant carries the alias' own name, which is a name no image and no Terraform address
// knows (D2090).
func TestJobSetBuildsTheTargetVariant(t *testing.T) {
	spec := aliasSpec(t, "bmb")
	want := gcp.WorkflowVariant("go", "bmb")
	if _, ok := spec.Workflows[want]; !ok {
		t.Fatalf("the job set has no %s variant, want the account the alias stands for: %v", want, slices.Sorted(maps.Keys(spec.Workflows)))
	}
	if got := spec.Workflows[want].SecretIDs["claude-oauth-token-bmb"]; got == "" {
		t.Errorf("the %s variant mounts no %s, want the account's own token", want, "claude-oauth-token-bmb")
	}
	if _, ok := spec.Workflows[gcp.WorkflowVariant("go", "reviewer")]; ok {
		t.Errorf("the job set builds a variant named after the alias: %v", slices.Sorted(maps.Keys(spec.Workflows)))
	}
	// The unsuffixed variant is the base every repository has, and the alias adds the account's own
	// beside it rather than replacing it.
	if _, ok := spec.Workflows[gcp.WorkflowVariant("go", "")]; !ok {
		t.Errorf("the job set lost the base variant: %v", slices.Sorted(maps.Keys(spec.Workflows)))
	}
}

// TestRepointKeepsTheOldTargetVariant is D2095 at the job set: a repoint moves which account the
// project's runs go out on, the variant the configuration no longer produces leaves the set, and
// nothing that runs afterwards deletes the old target's job by itself. The old variant is a pure
// function's absence — this file holds no history — so what keeps the job alive is the state: its
// deletion_protection is still on, which puts the address out of reach of the plan's own delete and
// leaves the retirement flow (the API delete, and the state removal before it) as the one thing
// that finishes it, naming the executions in flight. `fugaro update --retire` is that flow, and a
// managed project's plain `fugaro update` is the operator asking for the same retirement.
func TestRepointKeepsTheOldTargetVariant(t *testing.T) {
	old, repointed := aliasSpec(t, "bmb"), aliasSpec(t, "other")
	before, after := gcp.WorkflowVariant("go", "bmb"), gcp.WorkflowVariant("go", "other")
	if _, ok := old.Workflows[before]; !ok {
		t.Fatalf("fixture bug: the spec before the repoint has no %s variant: %v", before, slices.Sorted(maps.Keys(old.Workflows)))
	}
	if _, ok := repointed.Workflows[after]; !ok {
		t.Fatalf("the repoint built no %s variant: %v", after, slices.Sorted(maps.Keys(repointed.Workflows)))
	}
	if _, ok := repointed.Workflows[before]; ok {
		t.Fatalf("the spec after the repoint still produces %s", before)
	}

	// The state is the origin installation's, which deployed the variant the repoint drops.
	st := originState(t)
	rets := Retirements(st.Addresses(), repointed)
	job := `module.repo.module.workflow["` + before + `"].google_cloud_run_v2_job.this[0]`
	i := slices.IndexFunc(rets, func(r Retirement) bool { return r.Address == job })
	if i < 0 {
		t.Fatalf("the old target's job is not a retirement of the repointed spec:\n%v", rets)
	}
	if r := rets[i]; r.Kind != "workflow job" || r.Kept != "" || !strings.Contains(r.Cause, "bmb") {
		t.Errorf("%s = %+v, want an api-deleted workflow job whose cause names bmb", job, r)
	}

	// The state's own deletion_protection is what makes it a job no apply may delete: the ordinary
	// plan would have to destroy it, and Terraform refuses.
	FillFromState(st, rets)
	if !rets[i].Protected || !rets[i].ProtectedRead {
		t.Errorf("%s = %+v, want the state's deletion_protection read as true", job, rets[i])
	}
	api, apply, kept := RetireSplit(st.Addresses(), repointed, rets)
	if !slices.ContainsFunc(api, func(r Retirement) bool { return r.Address == job }) {
		t.Errorf("the old target's job is not finished through the Cloud Run API: api = %v", api)
	}
	if slices.ContainsFunc(apply, func(r Retirement) bool { return r.Address == job }) {
		t.Errorf("the old target's job is in the plan's own apply set, which would destroy it: apply = %v", apply)
	}
	if slices.ContainsFunc(kept, func(r Retirement) bool { return r.Address == job }) {
		t.Errorf("the old target's job is reported as kept, and a job is not: kept = %v", kept)
	}
	if !slices.Contains(Removals(st.Addresses(), rets), job) {
		t.Errorf("the old target's job is not among the addresses the retirement takes out of the state: %v", Removals(st.Addresses(), rets))
	}
}
