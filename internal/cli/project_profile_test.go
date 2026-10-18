package cli

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// projectProfilesYAML is an operator's profiles block: the default profile
// every workflow that names none runs on, plus one that pins its own admission
// list. It is appended after the fixture's repos: block, so it is a top-level
// key.
const projectProfilesYAML = "profiles:\n" +
	"  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, secrets: [deploy-key] }\n" +
	"  wide: { provider: github, cpu: 4, memory: 8Gi, timeout_class: 12h, allow_repos: [\"github/acme/*\"] }\n"

// TestPublishWritesTheProfilesAndShowPrintsThemWithTheirJobs: step 5. The
// published node carries what the operator's config declares, `project show`
// prints each profile with the job it maps to, and the secrets it prints are
// names, never values (the node is readable by every run token).
func TestPublishWritesTheProfilesAndShowPrintsThemWithTheirJobs(t *testing.T) {
	f := newProjectFixture(t, projectProfilesYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	p := publishedPolicy(t, f)
	if len(p.Profiles) != 2 {
		t.Fatalf("profiles = %v, want the operator's two", p.ProfileNames())
	}
	standard := p.Profiles[projectpolicy.DefaultProfileName]
	if standard.Provider != "github" || standard.CPU != 2 || standard.Memory != "4Gi" || standard.TimeoutClass != "6h" {
		t.Errorf("standard = %+v, want the operator's profile whole", standard)
	}
	if len(standard.AllowRepos) != 0 {
		t.Errorf("standard.allow_repos = %v, want none (the project's list applies)", standard.AllowRepos)
	}
	// An empty profile list falls back to the project's, never to everything.
	adm, err := p.ProfileAllowRepos("standard")
	if err != nil {
		t.Fatal(err)
	}
	if !projectpolicy.AdmitRepo(adm, "github/acme/app") || projectpolicy.AdmitRepo(adm, "github/acme/elsewhere") {
		t.Errorf("standard's own list is %v, want the project's own list", adm)
	}
	// The profile's own list is the one that applies where it has one.
	adm, err = p.ProfileAllowRepos("wide")
	if err != nil {
		t.Fatal(err)
	}
	if len(adm) != 1 || adm[0] != "github/acme/*" {
		t.Errorf("wide's own list = %v, want its own list", adm)
	}

	out, _, err := execute(t, "project", "policy", "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"profile standard: provider github, cpu 2, memory 4Gi, timeout 6h, secrets deploy-key, allow_repos the project's allow_repos",
		"profile standard jobs: one job per (profile, provider, account): (standard, github, <account>)",
		"profile wide: provider github, cpu 4, memory 8Gi, timeout 12h, secrets none, allow_repos github/acme/*",
		// A secret-bearing profile with no list of its own is selectable by
		// every enrolled repository (job_profile:), so show says so.
		"secrets deploy-key, allow_repos the project's allow_repos (warning: every enrolled repository can select this profile and read its secrets; give it its own allow_repos)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("policy show does not print %q:\n%s", want, out)
		}
	}
	// `project show` prints the same profiles.
	out, _, err = execute(t, "project", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "profile standard:") || !strings.Contains(out, "(standard, github, <account>)") {
		t.Errorf("project show does not print the profiles and their jobs:\n%s", out)
	}
}

// TestPublishWithoutProfilesDropsThemAndSaysSo: a publish is built from this
// machine's config, so a config that sets no profiles: publishes no block —
// the same rule providers, prices and the budget follow — and every workflow
// that selected one is then refused at run start. The operator must read that
// before it is written, not discover it one run at a time.
func TestPublishWithoutProfilesDropsThemAndSaysSo(t *testing.T) {
	f := newProjectFixture(t, projectProfilesYAML)
	if _, _, err := execute(t, "project", "policy", "publish"); err != nil {
		t.Fatal(err)
	}
	if len(publishedPolicy(t, f).Profiles) == 0 {
		t.Fatal("the first publish wrote no profiles")
	}
	// The same config, with the profiles: block taken back out.
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	without := strings.Replace(string(raw), projectProfilesYAML, "", 1)
	if without == string(raw) {
		t.Fatal("the fixture config does not carry the profiles block this test removes")
	}
	if err := os.WriteFile(f.path, []byte(without), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "project", "policy", "publish")
	if err != nil {
		t.Fatal(err)
	}
	if got := publishedPolicy(t, f).Profiles; len(got) != 0 {
		t.Errorf("profiles = %v, want none: the config sets none", got)
	}
	if !strings.Contains(out, "- profile standard") {
		t.Errorf("the publish does not say the profile is dropped:\n%s", out)
	}
}

// TestPublishRefusesAnUndefinedProfile: step 1's publish half. A config that
// publishes profiles must define the one every workflow that names none runs
// on, because a repository selecting it would have every run refused at run
// start, one run at a time.
func TestPublishRefusesAnUndefinedProfile(t *testing.T) {
	f := newProjectFixture(t, "profiles:\n  big: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h }\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("publishing a profiles block without %s succeeded:\n%s", projectpolicy.DefaultProfileName, out)
	}
	if !strings.Contains(err.Error(), projectpolicy.DefaultProfileName) {
		t.Errorf("the refusal does not name %s: %v", projectpolicy.DefaultProfileName, err)
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a refused publish wrote the node")
	}
}

// TestPublishRefusesAProfileWithoutAProvider: finding 4, at the publisher. A
// profile's provider decides which repositories its job serves and is part of
// the job's identity (task 3.2), so it is required — and the refusal comes
// from the policy's own rule, because the node is what the runner reads.
func TestPublishRefusesAProfileWithoutAProvider(t *testing.T) {
	f := newProjectFixture(t, "profiles:\n  standard: { cpu: 2, memory: 4Gi, timeout_class: 6h }\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("publishing a profile with no provider succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Errorf("the refusal does not say a provider is required: %v", err)
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a refused publish wrote the node")
	}
}

// TestPublishRefusesAnAllowRepoPatternOfAnotherProvider: step 3, at the
// publisher. A profile serves one provider's repositories, so a pattern of
// another provider could never match one; admitting it silently would leave
// the owner believing it admits something.
func TestPublishRefusesAnAllowRepoPatternOfAnotherProvider(t *testing.T) {
	f := newProjectFixture(t, "profiles:\n  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, allow_repos: [\"bitbucket/acme/*\"] }\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("publishing a bitbucket pattern on a github profile succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "bitbucket") {
		t.Errorf("the refusal does not name the pattern it refused: %v", err)
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a refused publish wrote the node")
	}
}

// TestPublishRefusesAnInvalidResourceForm: step 4, at the publisher. Cloud Run
// cannot deploy a job the API would refuse, so the profile that names it is
// refused where the operator reads the line, not at the deploy.
func TestPublishRefusesAnInvalidResourceForm(t *testing.T) {
	newProjectFixture(t, "profiles:\n  standard: { provider: github, cpu: 3, memory: 4Gi, timeout_class: 6h }\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("publishing a profile with 3 CPUs succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "cpu") {
		t.Errorf("the refusal does not name cpu: %v", err)
	}
}

// TestPublishRefusesASecretValueInAProfile: a profile's secrets are NAMES. A
// pasted key is refused, and the refusal never echoes it — the node is
// readable by every run token of the job and printed by `policy show`, so a
// value written there is a value published, and a value printed back is the
// same leak one terminal later.
func TestPublishRefusesASecretValueInAProfile(t *testing.T) {
	f := newProjectFixture(t, "profiles:\n  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, secrets: [\"sk-ant-api03-abc\"] }\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("publishing a pasted API key as a secret name succeeded:\n%s", out)
	}
	if strings.Contains(err.Error(), "api03") || strings.Contains(out, "api03") {
		t.Errorf("the refusal echoes the value it refused: %v\n%s", err, out)
	}
	if !strings.Contains(err.Error(), "secrets[0]") {
		t.Errorf("the refusal does not say which entry it refused: %v", err)
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a refused publish wrote the node")
	}
}

// TestPublishRefusesAnEmptyProfileAllowRepos: an operator writing `allow_repos:
// []` means "this profile admits nobody", and the policy cannot carry the
// difference between that and the key left out (the carrier's omitempty drops
// the empty list on the way in). Read as "the project's list applies", it
// would hand the profile every repository the project enrolls — the one
// direction a profile's list must never move — so the publisher refuses it.
// Through the CLI it is localcfg's validate that refuses the file, before the
// publisher's own check runs; that check (defence in depth) has its own test,
// TestBuildProjectPolicyRefusesAnEmptyProfileAllowRepos.
func TestPublishRefusesAnEmptyProfileAllowRepos(t *testing.T) {
	f := newProjectFixture(t, "profiles:\n  standard: { provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, allow_repos: [] }\n")
	out, _, err := execute(t, "project", "policy", "publish")
	if err == nil {
		t.Fatalf("publishing a profile with an empty allow_repos succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "profiles.standard.allow_repos") {
		t.Errorf("the refusal does not name the key it refused: %v", err)
	}
	if f.db.Value(projectpolicy.PathShared()) != nil {
		t.Error("a refused publish wrote the node")
	}
	// The project's list is seeded from repos:, so a publish that had gone
	// through would have admitted both of them — which is what this refusal
	// exists to prevent.
	if strings.Contains(out, "acme/other") && err == nil {
		t.Errorf("the empty list was widened to the project's allow-list:\n%s", out)
	}
}

// TestBuildProjectPolicyRefusesAnEmptyProfileAllowRepos: the publisher's own
// door on the empty-list rule, reached directly. Through the CLI localcfg's
// validate refuses the file first (TestPublishRefusesAnEmptyProfileAllowRepos
// passes on that refusal alone), so this builds the config in code — a profile
// decoded on its own, never validated — and hands it to buildProjectPolicy.
// The mutation is dropping the AllowReposWritten check from
// buildProjectPolicy: the profile then publishes with no list, which the node
// reads as the project's whole allow_repos.
func TestBuildProjectPolicyRefusesAnEmptyProfileAllowRepos(t *testing.T) {
	var pr config.JobProfile
	if err := yaml.Unmarshal([]byte("{ provider: github, cpu: 2, memory: 4Gi, timeout_class: 6h, allow_repos: [] }"), &pr); err != nil {
		t.Fatal(err)
	}
	if !pr.AllowReposWritten() {
		t.Fatal("the fixture's profile does not record its written allow_repos")
	}
	lc := &localcfg.Config{
		Name:     "acme",
		Budget:   &localcfg.Budget{Mode: "observe"},
		Profiles: map[string]config.JobProfile{projectpolicy.DefaultProfileName: pr},
	}
	pol, _, err := buildProjectPolicy(lc, []string{"github/acme/app"}, nil, false)
	if err == nil {
		t.Fatalf("an empty profile allow_repos was built into a policy: %+v", pol.Profiles)
	}
	if !strings.Contains(err.Error(), "profiles.standard.allow_repos") {
		t.Errorf("the refusal does not name the key it refused: %v", err)
	}
}
