package infra

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// --- Retirements (plan docs/plans/2026-10-14-config-transitions.md Task 2)

// The origin fixture's installation: one member who both operates and
// launches. That is not decoration: every count in design §1 is per
// installation, and the design's 14/12/2/3 are the numbers of an
// installation with one operator and one launcher. A launcher grant is made
// per member of launchers ∪ operators (tfvars.go's launchersWithOperators),
// so a second member would make the retired variant own eight addresses and
// every count in the design would move.
const originMember = "user:owner@example.com"

// originLC is the origin's local config: acme/webapp on GitHub with one
// workflow, and the named Claude account bmb allowed to send its code.
func originLC(t *testing.T) *localcfg.Config {
	t.Helper()
	return originLCWith(t, "[go]")
}

// originLCWith is originLC with the repository's own workflow list given, so
// a test can remove one workflow while the config still defines another (a
// repository with no workflows at all is not a config).
func originLCWith(t *testing.T, workflows string) *localcfg.Config {
	t.Helper()
	return parseLC(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry_host: us-east5-docker.pkg.dev/proj-1234
base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc}
repos:
  acme/webapp: { provider: github, base_branch: main, workflows: `+workflows+`, github_app_id: "123456" }
claude_accounts:
  bmb: { allow_data_to: [acme/webapp] }
`)
}

// originCfg is the origin's fugaro.yaml: one baked, daily-checked workflow
// named go, mounting only the git credential, under the agent block given.
func originCfg(agent string) string {
	return `version: 1
project: aurora
git: { provider: github, base_branch: main }
` + agent + `
workflows:
  go:
    commands: { build: npm run build, test: npm test }
`
}

// originSpec is the origin's spec for the agent block given. The origin
// ran it with `agent: { auth: api-key }`; `oauth` is the state it came from
// and the state it can go back to.
func originSpec(t *testing.T, agent string) RepoSpec {
	t.Helper()
	out := installationOutputs()
	out.Operators = []string{originMember}
	out.Launchers = []string{originMember}
	rs, err := Repo(Inputs{LC: originLC(t), Repo: "acme/webapp", Cfg: parseCfg(t, []byte(originCfg(agent))), Installation: out})
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// originState is the fixture state: internal/infra/testdata/state_origin.json,
// the state the origin's repository root managed before the transition, as
// `terraform show -json` prints it. It is decoded here rather than through
// tf.ShowState, which needs a Terraform binary and a workdir; the struct is
// exported and this is exactly what that method unmarshals into.
func originState(t *testing.T) *tf.State {
	t.Helper()
	data, err := os.ReadFile("testdata/state_origin.json")
	if err != nil {
		t.Fatal(err)
	}
	var st tf.State
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	if !st.Managed() {
		t.Fatal("the fixture state manages nothing")
	}
	return &st
}

// originRetired are the design's §1 fourteen addresses, spelled out. The
// test asserts the set, not just its size, because the counts are only
// meaningful over this exact list.
func originRetired() []string {
	const (
		variant  = `module.repo.module.workflow["go_bmb"].`
		survivor = `module.repo.module.workflow["go"].`
		grant    = `module.repo.google_secret_manager_secret_iam_member.operator[`
	)
	var out []string
	for _, a := range []string{
		// The retired named-account variant's seven (design §2's "what one
		// workflow variant owns"): the job, the account, the operator grant,
		// the launcher grant, its two secret accessors, the bucket grant.
		variant + "google_cloud_run_v2_job.this[0]",
		variant + "google_service_account.job",
		variant + `google_service_account_iam_member.operator["` + originMember + `"]`,
		variant + `google_cloud_run_v2_job_iam_member.launcher["` + originMember + `"]`,
		variant + `google_secret_manager_secret_iam_member.job["claude-oauth-token-bmb"]`,
		variant + `google_secret_manager_secret_iam_member.job["github-app-key"]`,
		variant + "google_storage_bucket_iam_member.job",
		// The surviving default variant keeps existing and stops mounting the
		// OAuth token: its accessor goes.
		survivor + `google_secret_manager_secret_iam_member.job["claude-oauth-token"]`,
		// The two containers themselves.
		`module.repo.google_secret_manager_secret.this["claude-oauth-token"]`,
		`module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`,
		// Each container's two operator grants.
		grant + `"claude-oauth-token ` + originMember + ` secretVersionAdder"]`,
		grant + `"claude-oauth-token ` + originMember + ` viewer"]`,
		grant + `"claude-oauth-token-bmb ` + originMember + ` secretVersionAdder"]`,
		grant + `"claude-oauth-token-bmb ` + originMember + ` viewer"]`,
	} {
		out = append(out, a)
	}
	return out
}

// The real 2026-10-14 case: a state holding the two OAuth containers and
// the go_bmb variant's addresses, and an api-key spec. The counts of design
// §1 are asserted here so the table cannot drift from the fixture: 14
// retired = 12 going away + 2 kept containers.
//
// The spec is the config's own, with no discovery in front of it. In the
// real run Readiness unions the live labelled containers back in
// (Existing.ExtraSecrets, project-secrets.md N4), which is what makes the
// two OAuth containers re-adoptable instead of retired —
// TestRetirementsProviderDroppedIsTheUnionCase is the half of this that
// pins that, and the CLI-level tests exercise the union end to end.
func TestRetirementsOriginTransition(t *testing.T) {
	st := originState(t)
	spec := originSpec(t, `agent: { auth: api-key }`)
	rets := Retirements(st.Addresses(), spec)

	var got []string
	for _, r := range rets {
		got = append(got, r.Address)
	}
	want := originRetired()
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("Retirements returned %d addresses, want %d\n missing: %v\n unexpected: %v",
			len(got), len(want), missingFrom(want, got), missingFrom(got, want))
	}
	if len(rets) != 14 {
		t.Fatalf("Retirements = %d addresses, want the design's 14", len(rets))
	}

	// 12 go away, 2 are removed from state and kept: the count is Kept's,
	// not a second list.
	var going, kept []string
	for _, r := range rets {
		if r.Kept == "" {
			going = append(going, r.Address)
			continue
		}
		kept = append(kept, r.Address)
	}
	if len(going) != 12 || len(kept) != 2 {
		t.Errorf("going away = %d, kept = %d; want 12 and 2 (14 = 12 + 2)", len(going), len(kept))
	}
	// 12 = 11 by the apply + 1 (the orphaned job) through the Cloud Run API.
	var api []string
	for _, r := range rets {
		if r.Kind == "workflow job" {
			api = append(api, r.Address)
		}
	}
	if len(api) != 1 {
		t.Errorf("workflow jobs retired = %v, want the one orphaned job the API deletes", api)
	}

	// Both containers carry the plain-words promise; no grant does: an
	// accessor to a kept secret is still a grant that goes away, and only
	// the container is kept (Review Focus 2).
	const keptWords = "the secret and its value stay in Secret Manager; its container carries the fugaro labels, so re-adding it re-adopts it"
	for _, r := range rets {
		switch {
		case r.Kind == "secret container":
			if r.Kept != keptWords {
				t.Errorf("%s: Kept = %q, want %q", r.Address, r.Kept, keptWords)
			}
			if !strings.HasPrefix(r.Cause, "agent.auth no longer mounts ") {
				t.Errorf("%s: Cause = %q, want it to name the unmounted Claude credential", r.Address, r.Cause)
			}
		case r.Kept != "":
			t.Errorf("%s: Kept = %q; only a container is kept", r.Address, r.Kept)
		}
	}

	// Nothing the spec still produces is returned, and the state really
	// holds addresses it does produce (otherwise the assertion above would
	// be vacuous).
	produced := spec.Addresses()
	for _, r := range rets {
		if slices.Contains(produced, r.Address) {
			t.Errorf("%s is retired but the spec still produces it", r.Address)
		}
	}
	if len(produced) == 0 {
		t.Error("fixture bug: the spec produces nothing")
	}
	if !slices.Contains(st.Addresses(), `module.repo.google_secret_manager_secret.this["github-app-key"]`) {
		t.Error("fixture bug: the state must hold a container the spec still produces")
	}

	// The kinds and causes of the fixture rows, by address (the design's
	// §2 rows A and A/C/D, and the strings Task 6's golden prints).
	wantKind := map[string]string{
		`module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job.this[0]`:                                       "workflow job",
		`module.repo.module.workflow["go_bmb"].google_service_account.job`:                                            "service account",
		`module.repo.module.workflow["go_bmb"].google_service_account_iam_member.operator["` + originMember + `"]`:    "IAM member",
		`module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job_iam_member.launcher["` + originMember + `"]`:   "IAM member",
		`module.repo.module.workflow["go_bmb"].google_secret_manager_secret_iam_member.job["claude-oauth-token-bmb"]`: "secret accessor",
		`module.repo.module.workflow["go_bmb"].google_secret_manager_secret_iam_member.job["github-app-key"]`:         "secret accessor",
		`module.repo.module.workflow["go_bmb"].google_storage_bucket_iam_member.job`:                                  "bucket grant",
		`module.repo.module.workflow["go"].google_secret_manager_secret_iam_member.job["claude-oauth-token"]`:         "secret accessor",
		`module.repo.google_secret_manager_secret.this["claude-oauth-token"]`:                                         "secret container",
		`module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`:                                     "secret container",
	}
	wantCause := map[string]string{
		`module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job.this[0]`:                               "the named Claude account bmb is no longer a variant this repository deploys",
		`module.repo.module.workflow["go"].google_secret_manager_secret_iam_member.job["claude-oauth-token"]`: "agent.auth no longer mounts claude-oauth-token",
		`module.repo.google_secret_manager_secret.this["claude-oauth-token"]`:                                 "agent.auth no longer mounts claude-oauth-token",
		`module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`:                             "agent.auth no longer mounts claude-oauth-token-bmb",
	}
	for _, r := range rets {
		if want, ok := wantKind[r.Address]; ok && r.Kind != want {
			t.Errorf("%s: Kind = %q, want %q", r.Address, r.Kind, want)
		}
		if want, ok := wantCause[r.Address]; ok && r.Cause != want {
			t.Errorf("%s: Cause = %q, want %q", r.Address, r.Cause, want)
		}
	}
	// Every address of the retired variant carries the same cause, the
	// account's, including the two accessors that mount a secret the
	// surviving variant still mounts.
	for _, r := range rets {
		if !strings.HasPrefix(r.Address, `module.repo.module.workflow["go_bmb"].`) {
			continue
		}
		if want := wantCause[`module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job.this[0]`]; r.Cause != want {
			t.Errorf("%s: Cause = %q, want %q", r.Address, r.Cause, want)
		}
	}
	// And the two operator grants of a container carry the container's
	// cause, so the summary reads as one change per secret.
	for _, r := range rets {
		if !strings.HasPrefix(r.Address, `module.repo.google_secret_manager_secret_iam_member.operator["claude-oauth-token-bmb `) {
			continue
		}
		if want := wantCause[`module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`]; r.Cause != want {
			t.Errorf("%s: Cause = %q, want %q", r.Address, r.Cause, want)
		}
	}
}

// A workflow removed from fugaro.yaml entirely (design row C): its variants'
// addresses are retired with a cause that names the workflow, and the
// default variant's key is the workflow name, so the two shapes cannot be
// confused with row A's named account.
func TestRetirementsWorkflowRemovedFromTheConfig(t *testing.T) {
	st := originState(t)
	// The config no longer has go: another workflow took its place. (Its
	// state is the origin's, so the new workflow's own addresses are
	// creates, not retirements, and are not this test's subject.)
	out := installationOutputs()
	out.Operators = []string{originMember}
	out.Launchers = []string{originMember}
	cfg := `version: 1
project: aurora
git: { provider: github, base_branch: main }
agent: { auth: oauth }
workflows:
  keep:
    commands: { build: npm run build, test: npm test }
`
	spec, err := Repo(Inputs{LC: originLCWith(t, "[go, keep]"), Repo: "acme/webapp", Cfg: parseCfg(t, []byte(cfg)), Installation: out})
	if err != nil {
		t.Fatal(err)
	}
	var causes []string
	for _, r := range Retirements(st.Addresses(), spec) {
		if strings.HasPrefix(r.Address, `module.repo.module.workflow["go"]`) {
			causes = append(causes, r.Cause)
		}
	}
	if len(causes) == 0 {
		t.Fatal("the removed workflow's default variant was not retired")
	}
	for _, c := range causes {
		if c != "workflow go was removed from fugaro.yaml" {
			t.Errorf("Cause = %q, want the workflow's own removal", c)
		}
	}
}

// Design row E, both directions, for a check job the STATE still holds. The
// label half — a check job the state has lost but the cloud still has — is
// Task 4's Orphans pair; this is the half a state can answer alone.
func TestRetirementsBakedCloneCheckJob(t *testing.T) {
	check := []string{
		"module.repo.google_cloud_run_v2_job.check[0]",
		"module.repo.google_cloud_run_v2_job_iam_member.check_invoker[0]",
		"module.repo.google_cloud_scheduler_job.check[0]",
	}
	// The state holds what a baked repository's root holds: the check job,
	// its invoker grant and its Scheduler job.
	state := []string{
		"module.repo.google_cloud_run_v2_job.check[0]",
		"module.repo.google_cloud_run_v2_job_iam_member.check_invoker[0]",
		"module.repo.google_cloud_scheduler_job.check[0]",
	}

	// baked -> clone, and the only workflow was the checked one: the spec
	// has no check any more.
	clone := `version: 1
project: aurora
git: { provider: github, base_branch: main }
agent: { auth: oauth }
workflows:
  go:
    checkout: clone
    commands: { build: npm run build, test: npm test }
`
	out := installationOutputs()
	out.Operators = []string{originMember}
	out.Launchers = []string{originMember}
	spec, err := Repo(Inputs{LC: originLC(t), Repo: "acme/webapp", Cfg: parseCfg(t, []byte(clone)), Installation: out})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Check != nil {
		t.Fatalf("fixture bug: a checkout: clone workflow must not be checked, got %+v", spec.Check)
	}
	rets := Retirements(state, spec)
	if len(rets) != 3 {
		t.Fatalf("baked -> clone retired %d addresses (%v), want the check job and its two companions", len(rets), rets)
	}
	wantKind := map[string]string{
		"module.repo.google_cloud_run_v2_job.check[0]":                    "check job",
		"module.repo.google_cloud_run_v2_job_iam_member.check_invoker[0]": "IAM member",
		"module.repo.google_cloud_scheduler_job.check[0]":                 "Scheduler job",
	}
	const cause = "no workflow is checked any more (rebuild.check: off everywhere)"
	for _, r := range rets {
		if want, ok := wantKind[r.Address]; !ok {
			t.Errorf("%s: not one of the check's three addresses", r.Address)
		} else if r.Kind != want {
			t.Errorf("%s: Kind = %q, want %q", r.Address, r.Kind, want)
		}
		if r.Cause != cause {
			t.Errorf("%s: Cause = %q, want %q", r.Address, r.Cause, cause)
		}
		if r.Kept != "" {
			t.Errorf("%s: Kept = %q; the check job's image is in the registry, nothing is kept here", r.Address, r.Kept)
		}
	}

	// The other direction: the same state, a baked and checked spec. The
	// check stays, so nothing of it retires.
	baked := originSpec(t, `agent: { auth: oauth }`)
	if baked.Check == nil {
		t.Fatal("fixture bug: the baked spec must have a check")
	}
	for _, r := range Retirements(state, baked) {
		if slices.Contains(check, r.Address) {
			t.Errorf("clone -> baked retired %s; the check job is still produced", r.Address)
		}
	}
}

// The reverse transition: the same state, back to `agent.auth: oauth`. The
// named account is a variant again and the OAuth credentials are mounted
// again, so every one of the 14 addresses the forward transition retired is
// produced by the spec once more — nothing at all retires, and in
// particular no secret container does (design §6's "zero deletes of any
// google_secret_manager_secret.*").
//
// The real reverse state is this one plus the api-key era's own resources,
// with the two containers re-adopted by label; the api-key resources the
// oauth spec no longer produces (the anthropic-api-key container and its
// grants) are what a reverse transition retires in turn, which is design
// row A's own "it is the same shape again" and not this test's subject.
func TestRetirementsReverseAdoptsCleanly(t *testing.T) {
	st := originState(t)
	spec := originSpec(t, `agent: { auth: oauth }`)
	rets := Retirements(st.Addresses(), spec)
	var addrs []string
	for _, r := range rets {
		addrs = append(addrs, r.Address)
	}
	if len(rets) != 0 {
		t.Errorf("the reverse transition retired %v, want nothing: everything the forward one retired is produced again", addrs)
	}
	for _, r := range rets {
		if strings.HasPrefix(r.Address, "module.repo.google_secret_manager_secret.") {
			t.Errorf("%s retired; the reverse transition must never retire a secret container", r.Address)
		}
	}
	// And the OAuth containers really are produced again, so the assertion
	// above is not vacuous.
	produced := spec.Addresses()
	for _, want := range []string{
		`module.repo.google_secret_manager_secret.this["claude-oauth-token"]`,
		`module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`,
		`module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job.this[0]`,
	} {
		if !slices.Contains(produced, want) {
			t.Errorf("the oauth spec does not produce %s", want)
		}
	}
}

// The row-F pin (P6): a named Claude account dropped from the local config
// is not a retirement, because discovery finds the container it left behind
// and Readiness unions it back into the spec's Secrets (project-secrets.md
// §5 N4, merged as #343). This design adds no second mechanism, and this
// test is what says so: the container is in the state, the config no longer
// computes it, and Retirements does not return it.
func TestRetirementsProviderDroppedIsTheUnionCase(t *testing.T) {
	ctx := context.Background()
	// The config no longer names bmb: no named variant, and no
	// claude-oauth-token-bmb in rs.Secrets.
	out := installationOutputs()
	out.Operators = []string{originMember}
	out.Launchers = []string{originMember}
	cfg := `version: 1
project: aurora
git: { provider: github, base_branch: main }
agent: { auth: oauth }
workflows:
  go:
    commands: { build: npm run build, test: npm test }
`
	lc := parseLC(t, `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry_host: us-east5-docker.pkg.dev/proj-1234
base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-0123abc}
repos:
  acme/webapp: { provider: github, base_branch: main, workflows: [go], github_app_id: "123456" }
`)
	spec, err := Repo(Inputs{LC: lc, Repo: "acme/webapp", Cfg: parseCfg(t, []byte(cfg)), Installation: out})
	if err != nil {
		t.Fatal(err)
	}
	const logical = "claude-oauth-token-bmb"
	const id = "fugaro-aurora-claude-oauth-token-bmb"
	if _, ok := spec.Secrets[logical]; ok {
		t.Fatal("fixture bug: the dropped account's secret must not be in the config's own Secrets")
	}

	// Discovery found it live and labelled it ours; Readiness unions it in.
	f := newCloud(t)
	f.gcs.AddBucket(spec.Installation.RunsBucket, testProjectNumber, managed)
	ex := Existing{
		Jobs:          map[string]string{},
		DisplayNames:  map[string]string{},
		CheckJob:      true,
		ExtraSecrets:  map[string]string{logical: id},
		ProjectNumber: testProjectNumber,
	}
	got, _, err := Readiness(ctx, f.c, spec, ex)
	if err != nil {
		t.Fatal(err)
	}
	if got.Secrets[logical] != id {
		t.Fatalf("Readiness did not union the labelled container back in: Secrets[%s] = %q", logical, got.Secrets[logical])
	}
	if !slices.Contains(got.Addresses(), `module.repo.google_secret_manager_secret.this["`+logical+`"]`) {
		t.Fatal("the unioned container is not among the addresses the spec produces")
	}

	st := originState(t)
	for _, r := range Retirements(st.Addresses(), got) {
		if strings.HasPrefix(r.Address, "module.repo.google_secret_manager_secret.") {
			t.Errorf("%s retired; a container discovery found by label is unioned back, never retired", r.Address)
		}
	}
	// The named variant really is gone from the spec, so the union is the
	// only thing keeping the container: without it, this is a retirement.
	plain := Retirements(st.Addresses(), spec)
	if !slices.ContainsFunc(plain, func(r Retirement) bool {
		return r.Address == `module.repo.google_secret_manager_secret.this["`+logical+`"]`
	}) {
		t.Error("fixture bug: without the union the dropped account's container must be a retirement")
	}
}

// The attribute names are the provider schema's, and a field that cannot be
// read is left empty, never guessed. Protected is the exception in the
// conservative direction: a job whose values cannot be read counts as
// protected (Task 3 then takes it out of the state and the API delete
// finishes it, which works whatever the flag said), while the gate that
// refuses an ordinary delete reads only a value it actually read as true.
func TestFillFromStateReadsTheProvidersOwnAttributes(t *testing.T) {
	st := originState(t)
	spec := originSpec(t, `agent: { auth: api-key }`)
	rets := Retirements(st.Addresses(), spec)

	// Nothing is filled before FillFromState runs: Retirements leaves every
	// optional field at its zero value, so the state is the only source.
	for _, r := range rets {
		if r.Logical != "" || r.SecretID != "" || r.Job != "" || r.Region != "" || r.Protected {
			t.Fatalf("%s: Retirements filled %+v; the optional fields come from the state", r.Address, r)
		}
	}
	FillFromState(st, rets)

	byAddr := map[string]Retirement{}
	for _, r := range rets {
		byAddr[r.Address] = r
	}
	// A container: the for_each key is the logical name, secret_id is the
	// Secret Manager ID (never values.id, the full resource name), and
	// prevent_destroy is not an attribute — the kind is what says so.
	c := byAddr[`module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`]
	if c.Logical != "claude-oauth-token-bmb" {
		t.Errorf("Logical = %q, want the for_each key", c.Logical)
	}
	if c.SecretID != "fugaro-acme-webapp-claude-oauth-token-bmb-43883f0cd8a28551" {
		t.Errorf("SecretID = %q, want the state's secret_id attribute", c.SecretID)
	}
	if !c.Protected {
		t.Error("a secret container is protected: its module carries prevent_destroy")
	}
	if c.Job != "" || c.Region != "" {
		t.Errorf("a container filled a job's fields: %+v", c)
	}

	// A job: name and location from the state, deletion_protection true.
	j := byAddr[`module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job.this[0]`]
	if j.Job != "fugaro-acme-webapp-go-bmb-56f1e9f74859" {
		t.Errorf("Job = %q, want the state's name attribute", j.Job)
	}
	if j.Region != "us-east5" {
		t.Errorf("Region = %q, want the state's location attribute", j.Region)
	}
	if !j.Protected {
		t.Error("this job's state says deletion_protection = true")
	}
	if j.Logical != "" || j.SecretID != "" {
		t.Errorf("a job filled a container's fields: %+v", j)
	}

	// A grant is neither: it has no attribute of its own to read.
	if g := byAddr[`module.repo.module.workflow["go_bmb"].google_storage_bucket_iam_member.job`]; g.Protected || g.Logical != "" || g.SecretID != "" || g.Job != "" || g.Region != "" {
		t.Errorf("a bucket grant filled fields: %+v", g)
	}

	// The three readings of a job's deletion_protection, which is what
	// Removals keys on. false: the apply deletes it, and state-removing it
	// would leave it alive in GCP with nothing managing it. true: no apply
	// can delete it. No attribute at all, or no state at all: protected, the
	// conservative direction, because the state does not say otherwise.
	bare := []Retirement{
		{Address: `module.repo.module.workflow["gone"].google_cloud_run_v2_job.this[0]`},
		{Address: `module.repo.module.workflow["gone"].google_cloud_run_v2_job.this[1]`},
		{Address: `module.repo.module.workflow["gone"].google_cloud_run_v2_job.this[2]`},
		{Address: `module.repo.module.workflow["gone"].google_cloud_run_v2_job.this[9]`},
		{Address: `module.repo.module.workflow["gone"].google_secret_manager_secret_iam_member.job["x"]`},
		{Address: `module.repo.google_secret_manager_secret.this["x"]`},
	}
	FillFromState(protectedState(t), bare)
	if bare[0].Protected {
		t.Error("a job whose state says deletion_protection = false is not protected")
	}
	if c := bare[0]; c.Job != "fugaro-acme-webapp-gone" || c.Region != "us-east5" {
		t.Errorf("the unprotected job filled %+v; want the state's own name and location", c)
	}
	if !bare[1].Protected {
		t.Error("a job whose state says deletion_protection = true is protected")
	}
	if !bare[2].Protected {
		t.Error("a job whose state has no deletion_protection attribute counts as protected")
	}
	if !bare[3].Protected || bare[3].Job != "" || bare[3].Region != "" {
		t.Errorf("a job the state does not hold: %+v; want protected, with no guessed name", bare[3])
	}
	if bare[4].Protected {
		t.Error("a grant is never protected")
	}
	if !bare[5].Protected || bare[5].Logical != "x" {
		t.Errorf("a container not in the state: %+v; want protected with its logical name from the address", bare[5])
	}
}

// protectedState is a three-job state: one whose protection is already
// false, one whose state says true, and one that carries no
// deletion_protection attribute at all (an older
// provider, or a job the state lost the attribute for).
func protectedState(t *testing.T) *tf.State {
	t.Helper()
	const state = `{
	  "format_version": "1.0",
	  "values": {
	    "root_module": {
	      "child_modules": [
	        {
	          "address": "module.repo",
	          "child_modules": [
	            {
	              "address": "module.repo.module.workflow[\"gone\"]",
	              "resources": [
	                {
	                  "address": "module.repo.module.workflow[\"gone\"].google_cloud_run_v2_job.this[0]",
	                  "mode": "managed",
	                  "type": "google_cloud_run_v2_job",
	                  "name": "this",
	                  "index": 0,
	                  "values": {"name": "fugaro-acme-webapp-gone", "location": "us-east5", "deletion_protection": false}
	                },
	                {
	                  "address": "module.repo.module.workflow[\"gone\"].google_cloud_run_v2_job.this[1]",
	                  "mode": "managed",
	                  "type": "google_cloud_run_v2_job",
	                  "name": "this",
	                  "index": 1,
	                  "values": {"name": "fugaro-acme-webapp-gone-2", "location": "us-east5", "deletion_protection": true}
	                },
	                {
	                  "address": "module.repo.module.workflow[\"gone\"].google_cloud_run_v2_job.this[2]",
	                  "mode": "managed",
	                  "type": "google_cloud_run_v2_job",
	                  "name": "this",
	                  "index": 2,
	                  "values": {"name": "fugaro-acme-webapp-gone-3", "location": "us-east5"}
	                }
	              ]
	            }
	          ]
	        }
	      ]
	    }
	  }
	}`
	var st tf.State
	if err := json.Unmarshal([]byte(state), &st); err != nil {
		t.Fatal(err)
	}
	return &st
}

// Retirements is a pure set difference: the same inputs give the same
// answer, in address order, whatever order the state listed them in, and an
// address the state holds twice is one retirement.
func TestRetirementsIsASortedSetDifference(t *testing.T) {
	st := originState(t)
	spec := originSpec(t, `agent: { auth: api-key }`)
	first := Retirements(st.Addresses(), spec)

	reversed := slices.Clone(st.Addresses())
	slices.Reverse(reversed)
	reversed = append(reversed, `module.repo.google_secret_manager_secret.this["claude-oauth-token"]`)
	second := Retirements(reversed, spec)

	if len(first) != len(second) {
		t.Fatalf("a repeated address changed the set: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Address != second[i].Address {
			t.Fatalf("the order of the state's addresses changed the result: %s vs %s", first[i].Address, second[i].Address)
		}
	}
	if !slices.IsSortedFunc(first, func(a, b Retirement) int { return strings.Compare(a.Address, b.Address) }) {
		t.Error("Retirements must be sorted by address")
	}
	// An empty state retires nothing, and a state that is exactly the spec
	// retires nothing either.
	if got := Retirements(nil, spec); len(got) != 0 {
		t.Errorf("an empty state retired %v", got)
	}
	if got := Retirements(spec.Addresses(), spec); len(got) != 0 {
		t.Errorf("a state that is the spec retired %v", got)
	}
}

// The registry is the second prevent_destroy resource in the repository
// module (registry.tf:59), so a retirement of it is protected the same way a
// container is: state-removed, never destroyed. The address is fixed
// (`google_artifact_registry_repository.images`), so no config change in the
// inventory retires it while the root still manages it -- the rule is
// asserted where it lives rather than through a spec that cannot reach it.
func TestFillFromStateProtectsTheRegistry(t *testing.T) {
	rets := []Retirement{{Address: "module.repo.google_artifact_registry_repository.images"}}
	FillFromState(originState(t), rets)
	if !rets[0].Protected {
		t.Error("the registry carries prevent_destroy, so it is protected")
	}
	if rets[0].Logical != "" || rets[0].Job != "" || rets[0].Region != "" || rets[0].SecretID != "" {
		t.Errorf("the registry filled %+v; it has none of those attributes", rets[0])
	}
	if got := retirement("module.repo.google_artifact_registry_repository.images", RepoSpec{}); got.Kind != "image registry" || got.Kept == "" {
		t.Errorf("retirement = %+v, want the registry's own words", got)
	}
}

// The kinds are the design's vocabulary, and every address of the origin's
// state gets one: an address this package cannot classify is still a
// retirement (the state manages it and the spec does not), and it says so
// rather than being dropped.
func TestRetirementsClassifiesEveryAddress(t *testing.T) {
	st := originState(t)
	spec := originSpec(t, `agent: { auth: api-key }`)
	known := map[string]bool{
		"workflow job": true, "check job": true, "Scheduler job": true, "service account": true,
		"IAM member": true, "secret accessor": true, "bucket grant": true, "secret container": true,
		"image registry": true,
	}
	for _, r := range Retirements(st.Addresses(), spec) {
		if r.Kind == "" {
			t.Errorf("%s: no Kind", r.Address)
		}
		if r.Cause == "" {
			t.Errorf("%s: no Cause", r.Address)
		}
		if !known[r.Kind] && r.Kind != "resource" {
			t.Errorf("%s: Kind = %q, outside the design's vocabulary", r.Address, r.Kind)
		}
	}
	// An address from another root, or one of a type this package has never
	// seen, is still retired — with the generic words, never silently
	// dropped.
	got := Retirements([]string{"module.other.google_compute_instance.vm[0]"}, spec)
	if len(got) != 1 || got[0].Kind != "resource" || got[0].Cause == "" {
		t.Errorf("Retirements = %+v, want one generic retirement", got)
	}
}

// The spec's own map decides the container causes: a workflow-only secret
// that no workflow mounts any more is not an agent credential, and its
// cause must not claim it is.
func TestRetirementsWorkflowOnlySecretCause(t *testing.T) {
	st := originState(t)
	// The config keeps go and drops nothing, but a workflow-only secret the
	// state holds is no longer mounted: n/a for the origin, so the address
	// is added to the state by hand.
	spec := originSpec(t, `agent: { auth: api-key }`)
	state := append(slices.Clone(st.Addresses()),
		`module.repo.google_secret_manager_secret.this["acme-deploy-token"]`,
		`module.repo.google_secret_manager_secret_iam_member.operator["acme-deploy-token `+originMember+` viewer"]`,
	)
	rets := Retirements(state, spec)
	want := "no workflow mounts acme-deploy-token any more"
	var seen int
	for _, r := range rets {
		if !strings.Contains(r.Address, "acme-deploy-token") {
			continue
		}
		seen++
		if r.Cause != want {
			t.Errorf("%s: Cause = %q, want %q", r.Address, r.Cause, want)
		}
	}
	if seen != 2 {
		t.Errorf("retired %d addresses for the unmounted workflow secret, want 2", seen)
	}
	// The reserved namespace is what makes a secret an agent credential:
	// claude-oauth-token itself, and every claude-oauth-token-<name>.
	for _, logical := range []string{"claude-oauth-token", "claude-oauth-token-bmb"} {
		got := Retirements([]string{`module.repo.google_secret_manager_secret.this["` + logical + `"]`}, spec)
		if len(got) != 1 {
			t.Fatalf("%s: %d retirements", logical, len(got))
		}
		if want := "agent.auth no longer mounts " + logical; got[0].Cause != want {
			t.Errorf("%s: Cause = %q, want %q", logical, got[0].Cause, want)
		}
	}
}

// The fixture state is a real `terraform show -json` shape: the fields this
// package decodes are where Terraform puts them, and every address the
// design enumerates is in it.
func TestOriginStateFixtureShape(t *testing.T) {
	st := originState(t)
	addrs := st.Addresses()
	if len(addrs) < 14 {
		t.Fatalf("the fixture state holds %d addresses, want the origin's own state", len(addrs))
	}
	for _, want := range originRetired() {
		if !slices.Contains(addrs, want) {
			t.Errorf("the fixture state does not hold %s", want)
		}
	}
	// The go_bmb job carries the attribute the removal set keys on, and the
	// containers carry the one the summary prints.
	var raw []byte
	raw, _ = json.Marshal(st)
	if !strings.Contains(string(raw), `"deletion_protection":true`) {
		t.Error("no resource in the fixture state carries deletion_protection")
	}
	// The fixture is the repository root's state: every address is in
	// module.repo, so it can never be mistaken for an installation's.
	for _, a := range addrs {
		if !strings.HasPrefix(a, "module.repo.") {
			t.Errorf("%s is outside module.repo", a)
		}
	}
	// And it holds nothing the labels would disagree with: the two
	// containers are the ones the design names.
	var containers []string
	for _, a := range addrs {
		if rest, ok := strings.CutPrefix(a, "module.repo.google_secret_manager_secret.this["); ok {
			containers = append(containers, strings.TrimSuffix(rest, "]"))
		}
	}
	slices.Sort(containers)
	want := []string{`"claude-oauth-token"`, `"claude-oauth-token-bmb"`, `"github-app-key"`}
	if !slices.Equal(containers, want) {
		t.Errorf("containers = %v, want %v", containers, want)
	}
	// The retired variant's job name is the one the spec derives for it, so
	// the fixture is the origin's real name rather than a placeholder.
	if got := gcp.JobName("acme/webapp", "go_bmb"); !strings.Contains(string(raw), got) {
		t.Errorf("no resource in the fixture state is named %s (the spec's own derived name)", got)
	}
}

// --- Removals and RetireAllowDelete (plan Task 3)

// originRetirements is the origin's retirement set, with the state's own
// attribute values filled in: what Task 5 hands both functions.
func originRetirements(t *testing.T) []Retirement {
	t.Helper()
	st := originState(t)
	rets := Retirements(st.Addresses(), originSpec(t, `agent: { auth: api-key }`))
	FillFromState(st, rets)
	return rets
}

// removalsWant are the three addresses of the origin that leave the state
// before the plan: the two prevent_destroy containers and the orphaned job.
func removalsWant() []string {
	return []string{
		`module.repo.google_secret_manager_secret.this["claude-oauth-token"]`,
		`module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`,
		`module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job.this[0]`,
	}
}

// The origin's 14 retired addresses in, the protected 3 out. The other 11
// stay in state so the plan deletes them: taking them out here would plan
// zero deletes and leave a service account, its IAM members and a bucket
// grant alive in GCP with nothing managing them (design §4.2a).
func TestRemovalsKeepsTheAddressesTheApplyMustDelete(t *testing.T) {
	st := originState(t)
	rets := originRetirements(t)
	got := Removals(st.Addresses(), rets)
	want := removalsWant()
	if !slices.Equal(got, want) {
		t.Fatalf("Removals = %q\nwant %q", got, want)
	}
	// Named one by one, because the failure mode is silent: a too-wide
	// Removals produces a clean plan and leftovers in GCP.
	for _, a := range []string{
		`module.repo.module.workflow["go_bmb"].google_service_account.job`,
		`module.repo.module.workflow["go_bmb"].google_service_account_iam_member.operator["` + originMember + `"]`,
		`module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job_iam_member.launcher["` + originMember + `"]`,
		`module.repo.module.workflow["go_bmb"].google_secret_manager_secret_iam_member.job["claude-oauth-token-bmb"]`,
		`module.repo.module.workflow["go_bmb"].google_storage_bucket_iam_member.job`,
		`module.repo.module.workflow["go"].google_secret_manager_secret_iam_member.job["claude-oauth-token"]`,
		`module.repo.google_secret_manager_secret_iam_member.operator["claude-oauth-token ` + originMember + ` viewer"]`,
	} {
		if slices.Contains(got, a) {
			t.Errorf("Removals returned %s; the apply must delete it", a)
		}
		if !slices.ContainsFunc(rets, func(r Retirement) bool { return r.Address == a }) {
			t.Errorf("fixture bug: %s is not in the retirement set", a)
		}
	}
	// And the arithmetic of the origin: 14 retired, 3 state-removed, 11 left
	// for the apply.
	if len(rets) != 14 || len(got) != 3 {
		t.Errorf("the origin is 14 retired and 3 state-removed; got %d and %d", len(rets), len(got))
	}
	// The guard, over a plan that deletes exactly the retirement set, allows
	// exactly the deletes the removal left behind -- and refuses anything
	// else. This is the pair Task 5 wires together.
	var p tf.Plan
	for _, r := range rets {
		if slices.Contains(got, r.Address) {
			continue
		}
		p.ResourceChanges = append(p.ResourceChanges, change(r.Address, "delete"))
	}
	p.ResourceChanges = append(p.ResourceChanges, change("module.repo.google_service_account.build", "delete"))
	allow := RetireAllowDelete(&p, rets)
	if len(allow) != 11 {
		t.Errorf("the guard's allow-list has %d addresses, want the 11 the apply deletes: %v", len(allow), allow)
	}
	if err := tf.Guard(&p, allow); err == nil || !strings.Contains(err.Error(), "google_service_account.build") {
		t.Errorf("the guard allowed a delete outside the retirement set: %v", err)
	}
}

// The resume mechanism (Review Focus 6): an address an earlier run already
// removed is never passed to state rm again, because the filter is what
// keeps the common path clean and the runner would fail on it.
func TestRemovalsSkipsWhatIsAlreadyGone(t *testing.T) {
	st := originState(t)
	rets := originRetirements(t)

	// A run that got as far as the first container and died: that address
	// is out of the state, the other two are not.
	gone := `module.repo.google_secret_manager_secret.this["claude-oauth-token"]`
	var state []string
	for _, a := range st.Addresses() {
		if a != gone {
			state = append(state, a)
		}
	}
	got := Removals(state, rets)
	want := []string{
		`module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`,
		`module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job.this[0]`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("Removals = %q\nwant %q", got, want)
	}
	// Every one of them gone: a rerun of a run that finished the removals
	// passes nothing at all.
	if got := Removals(nil, rets); len(got) != 0 {
		t.Errorf("an empty state: Removals = %q, want nothing", got)
	}
	all := append(slices.Clone(want), gone)
	if got := Removals(slices.DeleteFunc(slices.Clone(st.Addresses()), func(a string) bool {
		return slices.Contains(all, a)
	}), rets); len(got) != 0 {
		t.Errorf("every protected address already removed: Removals = %q, want nothing", got)
	}
}

// The other direction: every retired container still in state is returned
// (prevent_destroy), and so is every retired job whose state says
// deletion_protection = true — because neither can be deleted by an apply.
func TestRemovalsReturnsEveryProtectedAddressStillInState(t *testing.T) {
	st := originState(t)
	rets := originRetirements(t)
	state := st.Addresses()
	got := Removals(state, rets)
	for _, r := range rets {
		want := r.Protected && slices.Contains(state, r.Address)
		if slices.Contains(got, r.Address) != want {
			t.Errorf("%s: returned %v, want %v (protected %v, in state %v)",
				r.Address, slices.Contains(got, r.Address), want, r.Protected, slices.Contains(state, r.Address))
		}
	}
	// A protected address the state has already lost is not returned: there
	// is nothing to remove, and state rm of it would fail.
	if got := Removals(nil, []Retirement{{
		Address:   `module.repo.google_secret_manager_secret.this["x"]`,
		Protected: true,
	}}); len(got) != 0 {
		t.Errorf("a protected address the state lost: Removals = %q, want nothing", got)
	}
}

// Review B4's invariant, and the reason the rule is per-address rather than
// a property of the set: two retirements differing only in Protected must
// give different answers. The unprotected one is the user who followed
// gcp-setup.md and lowered the protection before editing fugaro.yaml; the
// apply deletes that job, and state-removing it would leave it alive in GCP
// with nothing managing it.
func TestRemovalsKeepsAnUnprotectedJobForTheApply(t *testing.T) {
	addr := `module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job.this[0]`
	state := []string{addr}
	protected := []Retirement{{Address: addr, Kind: "workflow job", Protected: true}}
	unprotected := []Retirement{{Address: addr, Kind: "workflow job", Protected: false}}
	if got := Removals(state, protected); !slices.Equal(got, state) {
		t.Errorf("a protected job: Removals = %q, want it removed from state", got)
	}
	if got := Removals(state, unprotected); len(got) != 0 {
		t.Errorf("an unprotected job: Removals = %q, want it left for the apply", got)
	}

	// The same difference read from the state, so the two functions cannot
	// disagree: only the job whose values say deletion_protection = true is
	// returned.
	pair := []Retirement{
		{Address: `module.repo.module.workflow["gone"].google_cloud_run_v2_job.this[0]`},
		{Address: `module.repo.module.workflow["gone"].google_cloud_run_v2_job.this[1]`},
	}
	FillFromState(protectedState(t), pair)
	if pair[0].Protected || !pair[1].Protected {
		t.Fatalf("fixture bug: %+v / %+v", pair[0], pair[1])
	}
	state = []string{pair[0].Address, pair[1].Address}
	if got := Removals(state, pair); !slices.Equal(got, []string{pair[1].Address}) {
		t.Errorf("Removals = %q, want only the protected job", got)
	}
}

// Sorted, per-address, and never a string that could be read as a flag or
// as "every address": StateRm refuses both, so Removals never hands it one.
func TestRemovalsIsSortedAndNeverTakesAFlagLookingAddress(t *testing.T) {
	state := []string{
		`module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job.this[0]`,
		`module.repo.google_secret_manager_secret.this["a"]`,
		`module.repo.google_secret_manager_secret.this["b"]`,
	}
	rets := []Retirement{
		{Address: state[0], Protected: true},
		{Address: state[1], Protected: true},
		{Address: state[2], Protected: true},
		{Address: "", Protected: true},
		{Address: "-chdir=/tmp", Protected: true},
		{Address: "-state=other.tfstate", Protected: true},
	}
	got := Removals(append(slices.Clone(state), "", "-chdir=/tmp", "-state=other.tfstate"), rets)
	slices.Sort(state)
	if !slices.Equal(got, state) {
		t.Errorf("Removals = %q, want %q", got, state)
	}
	if !slices.IsSorted(got) {
		t.Error("Removals must be sorted")
	}
	// Every address it returns is one it was given: it invents none.
	for _, a := range got {
		if !slices.Contains(state, a) {
			t.Errorf("Removals returned %q, which was not in the state", a)
		}
	}
	if got := Removals(nil, nil); len(got) != 0 {
		t.Errorf("no state and no retirements: Removals = %q", got)
	}
}

// Review D2's second line of defence: Removals filters, but a race (a
// hand-run state rm in another terminal, a partial earlier failure) can
// still make one address vanish between ShowState and the call. The runner
// reports "No matching objects found"; the removal reports that address as
// already gone and finishes the rest. The filter is the mechanism and this
// is the tolerance, and neither replaces the other.
func TestStateRmToleratesAnAddressThatWentAwayBetweenShowStateAndTheCall(t *testing.T) {
	ctx := context.Background()
	addrs := removalsWant()
	var called []string
	stateRm := func(_ context.Context, got ...string) error {
		if len(got) != 1 {
			t.Errorf("state rm called with %d addresses at once (%q); the removal is per-address", len(got), got)
		}
		called = append(called, got[0])
		if got[0] == addrs[1] {
			// Exactly what terraform prints for an address the state no
			// longer holds (internal/e2e/init_test.go:1243 records the same
			// text for the same case).
			return &tf.ExitError{Command: "state rm", Code: 1,
				Stderr: "Error: Invalid target address\n\nNo matching objects found. To view the available instances, use \"terraform state list\".\n"}
		}
		return nil
	}
	removed, gone, err := RemoveFromState(ctx, stateRm, addrs)
	if err != nil {
		t.Fatalf("a vanished address turned the removal into a failure: %v", err)
	}
	if !slices.Equal(removed, []string{addrs[0], addrs[2]}) {
		t.Errorf("removed = %q, want the two that were still there", removed)
	}
	if !slices.Equal(gone, []string{addrs[1]}) {
		t.Errorf("gone = %q, want the address the state had already lost", gone)
	}
	// It kept going: the address after the vanished one was still removed,
	// in order.
	if !slices.Equal(called, addrs) {
		t.Errorf("state rm was called with %q, want every address in order", called)
	}

	// A real failure is not tolerated, and the caller still learns how far
	// the removal got (design §6's half-failure).
	called = nil
	failed := func(_ context.Context, got ...string) error {
		called = append(called, got[0])
		if got[0] == addrs[1] {
			return &tf.ExitError{Command: "state rm", Code: 1, Stderr: "Error: the state is locked"}
		}
		return nil
	}
	removed, gone, err = RemoveFromState(ctx, failed, addrs)
	if err == nil {
		t.Fatal("a state rm that really failed was tolerated")
	}
	if !slices.Equal(removed, []string{addrs[0]}) || len(gone) != 0 {
		t.Errorf("removed = %q, gone = %q; want the first address removed and nothing tolerated", removed, gone)
	}
	if !slices.Equal(called, addrs[:2]) {
		t.Errorf("the removal kept going after a real failure: %q", called)
	}
	// The text alone is not enough: only terraform's own state rm reports it.
	if _, _, err := RemoveFromState(ctx, func(context.Context, ...string) error {
		return &tf.ExitError{Command: "plan", Code: 1, Stderr: "No matching objects found"}
	}, addrs[:1]); err == nil {
		t.Error("an error from a command that is not state rm was tolerated")
	}
	if _, _, err := RemoveFromState(ctx, func(context.Context, ...string) error {
		return errors.New("no matching objects found")
	}, addrs[:1]); err == nil {
		t.Error("an error that is not terraform's was tolerated")
	}
	// Nothing to do is not an error, and nothing is called.
	removed, gone, err = RemoveFromState(ctx, func(context.Context, ...string) error {
		t.Error("state rm called with no addresses")
		return nil
	}, nil)
	if err != nil || len(removed) != 0 || len(gone) != 0 {
		t.Errorf("an empty removal: %q %q %v", removed, gone, err)
	}
}

// Review Focus 1: a retirement the summary did not name is allowed through.
// The allow-list is the retirement set's own addresses, and nothing else --
// so the guard still refuses a delete the retirement never predicted.
func TestRetireAllowDeleteNeverAllowsAnUnnamedAddress(t *testing.T) {
	rets := originRetirements(t)
	extra := "module.repo.google_service_account.build"
	p := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		change(`module.repo.google_secret_manager_secret.this["claude-oauth-token"]`, "delete"),
		change(extra, "delete"),
		change(`module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`, "delete"),
		// Not deletes at all: an update, a create, a read, a no-op.
		change(`module.repo.module.workflow["go"].google_cloud_run_v2_job.this[0]`, "update"),
		change(`module.repo.google_secret_manager_secret.this["anthropic-api-key"]`, "create"),
		change(`module.repo.google_artifact_registry_repository.images`, "read"),
		change(`module.repo.google_service_account.build`, "no-op"),
		// A delete of an address that merely looks like one in the set: the
		// same resource with another key.
		change(`module.repo.google_secret_manager_secret.this["claude-oauth-token-bmbx"]`, "delete"),
	}}
	allow := RetireAllowDelete(p, rets)
	want := []string{
		`module.repo.google_secret_manager_secret.this["claude-oauth-token"]`,
		`module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`,
	}
	if !slices.Equal(allow, want) {
		t.Fatalf("allow = %q\nwant %q", allow, want)
	}
	if err := tf.Guard(p, allow); err == nil || !strings.Contains(err.Error(), extra) {
		t.Errorf("the guard with the retirement's allow-list: %v", err)
	}
	// A plan with nothing of the retirement's in it allows nothing at all,
	// and a nil plan is not a panic.
	if got := RetireAllowDelete(p, nil); len(got) != 0 {
		t.Errorf("no retirements: allow = %q", got)
	}
	if got := RetireAllowDelete(nil, rets); len(got) != 0 {
		t.Errorf("no plan: allow = %q", got)
	}
}

// And the other half: the replaces a transition really needs are allowed.
// A replace is ["delete","create"], so it is a delete of an address in the
// set; anything else the plan does to a retired address is not a delete and
// needs no permission.
func TestRetireAllowDeleteAllowsTheReplacesTheTransitionNeeds(t *testing.T) {
	rets := originRetirements(t)
	accessor := `module.repo.module.workflow["go"].google_secret_manager_secret_iam_member.job["claude-oauth-token"]`
	job := `module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job.this[0]`
	p := &tf.Plan{ResourceChanges: []tf.ResourceChange{
		change(accessor, "delete", "create"),
		change(job, "delete"),
		change(`module.repo.module.workflow["go"].google_service_account.job`, "update"),
		change(`module.repo.google_secret_manager_secret.this["claude-oauth-token"]`, "forget"),
	}}
	allow := RetireAllowDelete(p, rets)
	want := []string{accessor, job}
	slices.Sort(want)
	if !slices.Equal(allow, want) {
		t.Errorf("allow = %q\nwant %q", allow, want)
	}
	// The guard accepts it: the replaces and the deletes are the
	// retirement's own, an update of a surviving resource is not a delete,
	// and a forget action -- from a removed block, which destroys nothing --
	// is allowed whatever the allow-list says. That last one is why the
	// retirement's mechanic could be a removed block at all, and why it is
	// not: state rm is the proven one (P3).
	if err := tf.Guard(p, allow); err != nil {
		t.Errorf("the guard refused the retirement's own plan: %v", err)
	}
	// The allow-list is the guard's, not the plan's: an address the plan
	// deletes twice appears once.
	p.ResourceChanges = append(p.ResourceChanges, change(job, "delete"))
	if got := RetireAllowDelete(p, rets); !slices.Equal(got, want) {
		t.Errorf("allow = %q, want %q with no repeats", got, want)
	}
}
