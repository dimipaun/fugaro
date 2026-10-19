package infra

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// SecretMounts is what a job mounts: the git credential always, the one
// Claude credential agent.auth names (none for vertex), the allowed
// providers' keys only with api-key, and the workflow's own secrets.
func TestSecretMounts(t *testing.T) {
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"openrouter": {Secret: "openrouter-api-key", AllowDataTo: []string{"acme/app"}},
		"elsewhere":  {Secret: "elsewhere-key", AllowDataTo: []string{"acme/other"}},
	}}
	w := config.Workflow{Secrets: []config.Secret{{Name: "npm-token", Env: "NPM_TOKEN"}}}
	names := func(auth string) []string {
		ms, err := SecretMounts("bitbucket-token", &config.Config{Agent: config.Agent{Auth: auth}}, "app", config.DefaultClaudeAccount, w, lc, "acme/app")
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range ms {
			out = append(out, m.Logical)
		}
		return out
	}
	for auth, want := range map[string][]string{
		"vertex":  {"bitbucket-token", "npm-token"},
		"oauth":   {"bitbucket-token", "claude-oauth-token", "npm-token"},
		"api-key": {"bitbucket-token", "anthropic-api-key", "openrouter-api-key", "npm-token"},
	} {
		if got := names(auth); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", auth, got, want)
		}
	}
	// A named account's token is mounted under its own derived secret, not
	// the bare default: config.ClaudeAccountSecret is the one place that
	// decides the name, so this is the same behavior as calling it directly.
	ms, err := SecretMounts("bitbucket-token", &config.Config{Agent: config.Agent{Auth: "oauth"}}, "app", "acme-personal", w, lc, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	var claude string
	for _, m := range ms {
		if m.Env == config.ReservedSecrets["claude-oauth-token"] {
			claude = m.Logical
		}
	}
	if claude != "claude-oauth-token-acme-personal" {
		t.Errorf("named account acme-personal mounts logical secret %q, want claude-oauth-token-acme-personal", claude)
	}
	// Each mount carries the variable its secret becomes.
	ms, err = SecretMounts("github-app-key", &config.Config{Agent: config.Agent{Auth: "api-key"}}, "app", config.DefaultClaudeAccount, w, lc, "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	wantEnv := []SecretMount{
		{"github-app-key", "FUGARO_GITHUB_APP_PRIVATE_KEY"},
		{"anthropic-api-key", "ANTHROPIC_API_KEY"},
		{"openrouter-api-key", lc.Providers["openrouter"].SecretEnv()},
		{"npm-token", "NPM_TOKEN"},
	}
	if !slices.Equal(ms, wantEnv) {
		t.Errorf("mounts %v, want %v", ms, wantEnv)
	}
	// A workflow may not declare a provider's key.
	bad := config.Workflow{Secrets: []config.Secret{{Name: "openrouter-api-key", Env: "X"}}}
	if _, err := SecretMounts("bitbucket-token", &config.Config{}, "app", config.DefaultClaudeAccount, bad, lc, "acme/app"); err == nil {
		t.Error("a workflow secret naming a provider's key was accepted")
	}
}

// TestSecretMountsRefusesAnotherProvidersConventionalName: a workflow may not
// declare the name reserved for a configured provider's key either, even when
// that provider stores its key under another name — the name belongs to that
// provider's key (config.ReservedProviderSecret, the <entry>-api-key convention
// of 2026-10-18), so a value stored under it would be read as the provider's
// credential the day the key is renamed to the convention, and `fugaro secrets
// set` refuses to store one. Refused here, at the mount, rather than left to
// the readiness check: the job would otherwise mount a name that can never have
// a version, and be refused as a missing secret.
//
// The mutation is dropping the second refusal: the off-convention list below
// mounts.
func TestSecretMountsRefusesAnotherProvidersConventionalName(t *testing.T) {
	// openrouter's key is off convention on purpose: openrouter-api-key is then
	// reserved for it and nobody's .secret today.
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"openrouter": {Secret: "other-key", AllowDataTo: []string{"acme/app"}},
		"deepseek":   {Secret: "deepseek-api-key", AllowDataTo: []string{"acme/app"}},
		"thief":      {Secret: "thief-key", AllowDataTo: []string{"acme/app"}},
	}}
	for _, name := range []string{"openrouter-api-key", "thief-api-key"} {
		w := config.Workflow{Secrets: []config.Secret{{Name: name, Env: "X"}}}
		_, err := SecretMounts("bitbucket-token", &config.Config{}, "app", config.DefaultClaudeAccount, w, lc, "acme/app")
		if err == nil {
			t.Errorf("a workflow secret naming %s was accepted", name)
			continue
		}
		for _, want := range []string{"reserved for model provider", name} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal for %s does not say %q: %v", name, want, err)
			}
		}
	}
	// A provider whose key IS its conventional name is refused by the sharper
	// rule above, which names it a provider's key: the two refusals do not
	// overlap, and a name is only ever refused once.
	w := config.Workflow{Secrets: []config.Secret{{Name: "deepseek-api-key", Env: "X"}}}
	if _, err := SecretMounts("bitbucket-token", &config.Config{}, "app", config.DefaultClaudeAccount, w, lc, "acme/app"); err == nil ||
		!strings.Contains(err.Error(), "is the key of model provider deepseek") {
		t.Errorf("a provider's own key reads %v, want the sharper refusal", err)
	}
	// A name no entry's convention gives it is an ordinary workflow secret:
	// this rule claims only the conventional name, and the provider's own key
	// (whatever it is spelled) stays the sharper rule above.
	w = config.Workflow{Secrets: []config.Secret{{Name: "some-other-name", Env: "X"}}}
	if _, err := SecretMounts("bitbucket-token", &config.Config{}, "app", config.DefaultClaudeAccount, w, lc, "acme/app"); err != nil {
		t.Errorf("an ordinary workflow secret was refused: %v", err)
	}
}

// GitSecret is the git credential's logical name per provider, the one source
// the spec and the secrets stage share.
func TestGitSecret(t *testing.T) {
	for provider, want := range map[string]string{"github": "github-app-key", "bitbucket": "bitbucket-token"} {
		if got, ok := GitSecret(provider); !ok || got != want {
			t.Errorf("GitSecret(%q) = %q, %v", provider, got, ok)
		}
	}
	if got, ok := GitSecret("gitlab"); ok || got != "" {
		t.Errorf("GitSecret(gitlab) = %q, %v", got, ok)
	}
}

// Every git credential GitSecret names is classified for scope, and the
// per-repository one is the scope D2's sweep must leave alone (round 2, item
// 2): a provider's credential is either per repository -- issued for one
// repository, so never stored project-wide (config.PerRepositorySecret, the
// owner's ruling of 2026-10-15, which is bitbucket-token) -- or the project's
// (github-app-key, one App serving a team's repositories). This is the check
// the review asked for over "the git-provider secret names in the code": a
// provider added here without a decision fails rather than inheriting a scope.
func TestGitSecretScopeIsDecidedPerProvider(t *testing.T) {
	perRepo := map[string]bool{
		gitprov.KindBitbucket: true,
		gitprov.KindGitHub:    false,
	}
	for provider, want := range perRepo {
		logical, ok := GitSecret(provider)
		if !ok {
			t.Fatalf("GitSecret(%q) = no secret: this test's table is out of date", provider)
		}
		if _, reserved := config.ReservedSecrets[logical]; !reserved {
			t.Errorf("%s's credential %q is not a reserved secret, so no scope rule reaches it", provider, logical)
		}
		if got := config.PerRepositorySecret(logical); got != want {
			t.Errorf("PerRepositorySecret(%q) = %v, want %v for %s's credential", logical, got, want, provider)
		}
		// A per-repository credential is never swept into project scope; a
		// project's is.
		if got := config.ProjectScopeByDefault(logical); got != !want {
			t.Errorf("ProjectScopeByDefault(%q) = %v, want %v (%s's credential is %s)",
				logical, got, !want, provider, map[bool]string{true: "per repository", false: "the project's"}[want])
		}
	}
}

// TestSecretMountsNoListProviderAvailableToAnyRepo proves the automatic
// consequence of Task 1's AllowsData change: spec.go's per-provider mount
// condition is unedited, and a provider with no allow_data_to at all is
// now eligible for every repository (design §3a).
func TestSecretMountsNoListProviderAvailableToAnyRepo(t *testing.T) {
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"openrouter": {Secret: "openrouter-api-key"}, // no AllowDataTo at all
	}}
	w := config.Workflow{}
	ms, err := SecretMounts("bitbucket-token", &config.Config{Agent: config.Agent{Auth: "api-key"}}, "app", config.DefaultClaudeAccount, w, lc, "acme/never-named")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range ms {
		if m.Logical == "openrouter-api-key" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mounts = %+v, want openrouter-api-key present for a repo the no-list provider never named (design §3a)", ms)
	}
}

// TestSecretMountsListedProviderStillRefusesUnlistedRepo is the paired
// mutation-proof case: P7 must not weaken a real, non-empty, non-wildcard
// list.
func TestSecretMountsListedProviderStillRefusesUnlistedRepo(t *testing.T) {
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"openrouter": {Secret: "openrouter-api-key", AllowDataTo: []string{"acme/app"}},
	}}
	w := config.Workflow{}
	ms, err := SecretMounts("bitbucket-token", &config.Config{Agent: config.Agent{Auth: "api-key"}}, "app", config.DefaultClaudeAccount, w, lc, "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Logical == "openrouter-api-key" {
			t.Fatalf("mounts = %+v, want no openrouter-api-key for a repo an explicit, non-empty list excludes (P7 must not weaken this case)", ms)
		}
	}
}

// TestSecretMountsRequiredSecretsIsTheMountSet: RequiredSecrets (what `fugaro
// secrets ls` checks for MISSING) is exactly the union of what the jobs of
// the repository's workflows mount, per provider and per auth, less the
// optional mounts (OptionalSecret) a job runs without, and never a
// name no job of it mounts: the other provider's git credential, a model
// credential another auth names, or the bare default token in place of a
// named account's.
func TestSecretMountsRequiredSecretsIsTheMountSet(t *testing.T) {
	lc := &localcfg.Config{
		Providers: map[string]config.ModelProvider{
			"openrouter": {Secret: "openrouter-api-key", AllowDataTo: []string{"acme/app"}},
			"elsewhere":  {Secret: "elsewhere-key", AllowDataTo: []string{"acme/other"}},
		},
		ClaudeAccounts: map[string]config.ClaudeAccount{
			"work":  {AllowDataTo: []string{"acme/app"}},
			"other": {AllowDataTo: []string{"acme/other"}},
		},
	}
	cfg := func(provider, auth string) *config.Config {
		return &config.Config{
			Git:   config.Git{Provider: provider},
			Agent: config.Agent{Auth: auth},
			Workflows: map[string]config.Workflow{
				"web": {Secrets: []config.Secret{{Name: "npm-token", Env: "NPM_TOKEN"}}},
				"api": {Secrets: []config.Secret{{Name: "pypi-token", Env: "PYPI_TOKEN"}}},
			},
		}
	}
	for _, c := range []struct {
		provider, auth string
		want           []string
	}{
		{"github", "vertex", []string{"github-app-key", "npm-token", "pypi-token"}},
		{"bitbucket", "vertex", []string{"bitbucket-token", "npm-token", "pypi-token"}},
		{"github", "oauth", []string{"claude-oauth-token", "claude-oauth-token-work", "github-app-key", "npm-token", "pypi-token"}},
		// anthropic-api-key is mounted under api-key but optional (OptionalSecret:
		// the job mounts it only when it has a version), so it is not required.
		{"github", "api-key", []string{"github-app-key", "npm-token", "openrouter-api-key", "pypi-token"}},
	} {
		got, err := RequiredSecrets(cfg(c.provider, c.auth), lc, "acme/app")
		if err != nil {
			t.Fatalf("%s/%s: %v", c.provider, c.auth, err)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s/%s: %v, want %v", c.provider, c.auth, got, c.want)
		}
		// The same set the deployed jobs mount, variant by variant, less the
		// mounts a job may go without.
		var mounted []string
		for _, name := range []string{"api", "web"} {
			for _, tok := range WorkflowAccounts(cfg(c.provider, c.auth), lc, "acme/app") {
				git, _ := GitSecret(c.provider)
				ms, err := SecretMounts(git, cfg(c.provider, c.auth), name, tok, cfg(c.provider, c.auth).Workflows[name], lc, "acme/app")
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range ms {
					if !slices.Contains(mounted, m.Logical) && !OptionalSecret(m.Logical) {
						mounted = append(mounted, m.Logical)
					}
				}
			}
		}
		slices.Sort(mounted)
		if !slices.Equal(got, mounted) {
			t.Errorf("%s/%s: required %v, mounted %v", c.provider, c.auth, got, mounted)
		}
	}
	// A provider that is neither is refused, not guessed at.
	if _, err := RequiredSecrets(cfg("gitlab", "vertex"), lc, "acme/app"); err == nil {
		t.Error("an unknown git provider gave a required set")
	}
}

// mounted reports whether the plan's jobs still mount logical: a workflow's or
// the check job's own secret env, by the logical name each entry resolves
// through.
func mounted(spec RepoSpec, logical string) bool {
	is := func(m map[string]string) bool {
		return slices.Contains(slices.Collect(maps.Values(m)), logical)
	}
	for _, name := range slices.Sorted(maps.Keys(spec.Workflows)) {
		if is(spec.Workflows[name].SecretEnv) {
			return true
		}
	}
	return spec.Check != nil && is(spec.Check.SecretEnv)
}

// Task 2.2 step 1, at D2's default: with no project_secrets entry at all, the
// one predicate (ProjectSecretCandidate, through config.ProjectScopeByDefault)
// accepts the GitHub App key project-wide, so the repository's own job mounts
// the PROJECT's secret ID when the repository has no copy of its own. #343
// resolved the project copy only for a name the owner had opted in.
func TestProjectSecretMountsOnTheRepoJobWithoutARepoCopy(t *testing.T) {
	f := newCloud(t)
	spec, err := Repo(webappInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	logical := "github-app-key"
	cand, ok := spec.ProjectSecretCandidates[logical]
	if !ok {
		t.Fatalf("no ProjectSecretCandidates entry for %s (candidates = %v): D2's sweep is the default this test pins", logical, spec.ProjectSecretCandidates)
	}
	if want := gcp.ProjectSecretID(spec.Installation.ProjectName, logical); cand != want {
		t.Fatalf("candidate = %q, want the project's own ID %q", cand, want)
	}
	// The repository's container exists with no version -- what Terraform's
	// module leaves behind, and the state a project-scope store leaves the
	// repository in -- and the project's copy is live.
	f.sm.Seed(spec.Secrets[logical], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), nil)
	f.seedProjectSecret(spec, logical)
	got, missing, _ := readinessOf(t, f, spec)
	if id := got.MountSecretID(logical); id != cand {
		t.Errorf("the repository's job mounts %q for %s, want the project's %q", id, logical, cand)
	}
	if got.Secrets[logical] != spec.Secrets[logical] {
		t.Errorf("the repository's own container became %q, want %q: Terraform's for_each must not move", got.Secrets[logical], spec.Secrets[logical])
	}
	if !mounted(got, logical) {
		t.Errorf("no job mounts the resolved name any more: %v", got.Workflows)
	}
	if missingNames(missing, logical) {
		t.Errorf("missing = %v, want none: the project copy resolves", missing)
	}
}

// The other direction of the same rule: a repository copy with an enabled
// version wins, so the job keeps mounting the repository's own ID and
// Readiness records no project resolution -- even though the name is a project
// candidate and a project copy is live. A mount that preferred the project
// here would silently change which value a run reads the moment one is stored.
func TestRepoCopyStillWins(t *testing.T) {
	f := newCloud(t)
	spec, err := Repo(webappInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	logical := "github-app-key"
	if _, ok := spec.ProjectSecretCandidates[logical]; !ok {
		t.Fatalf("no ProjectSecretCandidates entry for %s: this test needs a name that could resolve project-wide", logical)
	}
	f.sm.Seed(spec.Secrets[logical], with(managed, gcp.LabelRepo, spec.Label, gcp.LabelSecret, logical), []byte("repo"))
	f.seedProjectSecret(spec, logical)
	got, missing, _ := readinessOf(t, f, spec)
	if id, ok := got.ProjectSecrets[logical]; ok {
		t.Errorf("ProjectSecrets[%s] = %q, want no entry: the repository's own copy has an enabled version", logical, id)
	}
	if id := got.MountSecretID(logical); id != spec.Secrets[logical] {
		t.Errorf("the repository's job mounts %q, want the repository's own %q", id, spec.Secrets[logical])
	}
	if !mounted(got, logical) {
		t.Errorf("no job mounts the name any more: %v", got.Workflows)
	}
	if missingNames(missing, logical) {
		t.Errorf("missing = %v, want none: the repository's own copy resolves", missing)
	}
}
