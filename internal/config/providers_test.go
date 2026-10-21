package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"gopkg.in/yaml.v3"
)

// TestProvidersAreTheGitprovKinds keeps config.Providers in step with the
// provider kinds gitprov implements.
func TestProvidersAreTheGitprovKinds(t *testing.T) {
	want := []string{gitprov.KindGitHub, gitprov.KindBitbucket}
	if got := slices.Sorted(slices.Values(Providers)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Fatalf("config.Providers = %v, gitprov kinds = %v", Providers, want)
	}
}

// TestRequireZDRDefaultsTrue pins the default of the route's first switch: a
// provider that says nothing prices the ZDR-eligible endpoints, because the
// safe end of a marketplace route is the one the owner would have picked, and
// only an explicit false prices every endpoint (design D3, D9).
func TestRequireZDRDefaultsTrue(t *testing.T) {
	var p ModelProvider
	if !p.RequireZDROn() {
		t.Error("an unset require_zdr is not on")
	}
	no := false
	p.RequireZDR = &no
	if p.RequireZDROn() {
		t.Error("require_zdr: false is on")
	}
	yes := true
	p.RequireZDR = &yes
	if !p.RequireZDROn() {
		t.Error("require_zdr: true is off")
	}
}

// TestPricePolicyDefaultIsCheapest pins the second switch's default and its
// effective value: unset means cheapest, spelled out so the compiler of a
// catalog row never sees the empty string (design D3).
func TestPricePolicyDefaultIsCheapest(t *testing.T) {
	var p ModelProvider
	if got := p.PricePolicySet(); got != PricePolicyCheapest {
		t.Errorf("an unset price_policy is %q, want %q", got, PricePolicyCheapest)
	}
	p.PricePolicy = PricePolicyHighest
	if got := p.PricePolicySet(); got != PricePolicyHighest {
		t.Errorf("price_policy: highest is %q", got)
	}
	if PricePolicyCheapest != "cheapest" || PricePolicyHighest != "highest" {
		t.Errorf("the policy names are %q/%q, want cheapest/highest: they are the config's own spelling", PricePolicyCheapest, PricePolicyHighest)
	}
}

// TestValidateModelProvidersRefusesUnknownPolicy pins that a typo in
// price_policy is a validate problem naming the provider and the field, not a
// route that silently prices the wrong end of the range.
func TestValidateModelProvidersRefusesUnknownPolicy(t *testing.T) {
	for _, policy := range []string{"", PricePolicyCheapest, PricePolicyHighest} {
		p := okProvider()
		p.PricePolicy = policy
		if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p}); len(ps) != 0 {
			t.Errorf("price_policy %q: %s", policy, problemsText(ps))
		}
	}
	p := okProvider()
	p.PricePolicy = "cheapest-ish"
	ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p})
	if len(ps) == 0 {
		t.Fatal("an unknown price_policy was accepted")
	}
	got := problemsText(ps)
	for _, want := range []string{"providers.openrouter.price_policy", "cheapest", "highest", "cheapest-ish"} {
		if !strings.Contains(got, want) {
			t.Errorf("the problem does not name %q: %s", want, got)
		}
	}
}

// TestCatalogEligible pins which routes the built-in price catalog may price:
// OpenRouter itself (any subdomain) and a loopback fake in a test. Everything
// else is a vendor's own endpoint, priced by model_prices (design D5, P6).
func TestCatalogEligible(t *testing.T) {
	for url, want := range map[string]bool{
		"https://openrouter.ai/api/v1": true,
		"https://openrouter.ai":        true,
		"https://api.openrouter.ai/v1": true,
		"https://openrouter.ai.evil.t": false,
		"https://notopenrouter.ai/api": false,
		"http://127.0.0.1:8123":        true,
		"http://[::1]:8123":            true,
		"http://localhost:8123":        true,
		"https://api.deepseek.com/api": false,
		"https://api.deepseek.com":     false,
		"https://bedrock-runtime.aws":  false,
		"":                             false,
		"openrouter.ai":                false,
	} {
		if got := CatalogEligible(url); got != want {
			t.Errorf("CatalogEligible(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestAllowsDataEmptyMeansAnyRepo(t *testing.T) {
	for _, p := range []ModelProvider{{}, {AllowDataTo: nil}} {
		if !p.AllowsData("acme/anything") {
			t.Fatalf("AllowsData(%+v) = false, want true (no list set, owner's final ruling)", p)
		}
	}
	// An explicit, non-nil, zero-length list is NOT the same state (P7,
	// design §3a round 5): ValidateModelProviders refuses it outright, and
	// AllowsData stays fail-closed on it rather than treating it as
	// "every repository", so a bypass of that validation can never widen
	// access by accident. The plan's own test text listed
	// {AllowDataTo: []string{}} among the any-repo cases, which contradicts
	// its own P7 implementation; the decision wins here.
	if (ModelProvider{AllowDataTo: []string{}}).AllowsData("acme/anything") {
		t.Fatal("an explicit, non-nil empty allow_data_to was treated as \"every repository\"; it must stay fail-closed")
	}
}

func TestAllowsDataWildcardMeansAnyRepo(t *testing.T) {
	p := ModelProvider{AllowDataTo: []string{"*"}}
	for _, repo := range []string{"acme/app", "ANYONE/anything"} {
		if !p.AllowsData(repo) {
			t.Errorf("AllowsData(%q) = false, want true (explicit [\"*\"] marker)", repo)
		}
	}
}

func TestAllowsDataNonEmptyListUnaffectedByWildcardRule(t *testing.T) {
	// The mutation-proof case: the wildcard/empty-list rule must be purely
	// additive, never weakening a real, non-empty, non-wildcard list.
	p := ModelProvider{AllowDataTo: []string{"acme/app", "Acme/Sandbox"}}
	for _, repo := range []string{"acme/app", "acme/sandbox"} {
		if !p.AllowsData(repo) {
			t.Errorf("AllowsData(%q) = false, want true (listed)", repo)
		}
	}
	if p.AllowsData("acme/other") {
		t.Fatal("an unlisted repo was allowed against a real, non-empty, non-wildcard list")
	}
}

func TestAllowsDataWildcardIsExactNotGlob(t *testing.T) {
	// "acme/*" is NOT the wildcard: only the literal entry "*" is.
	p := ModelProvider{AllowDataTo: []string{"acme/*"}}
	if p.AllowsData("acme/app") {
		t.Fatal("\"acme/*\" was treated as a glob; only the literal \"*\" entry is the wildcard")
	}
}

func TestValidateModelProvidersRefusesExplicitEmptyList(t *testing.T) {
	// MUST-FIX 2, orchestrator's ruling (round 5): an absent key and ["*"]
	// both mean "any repository," but an EXPLICIT allow_data_to: [] is
	// ambiguous (did the operator mean "any repo" or "I haven't decided
	// yet") and must be refused, not silently accepted as either meaning.
	p := okProvider()
	p.AllowDataTo = []string{} // non-nil, explicit, zero-length -- NOT the same input as omitting the key
	ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p})
	if len(ps) == 0 {
		t.Fatal("an explicit allow_data_to: [] was accepted; it must be refused (write [\"*\"] or remove the key)")
	}
	if !strings.Contains(problemsText(ps), "ambiguous") {
		t.Fatalf("problems = %q, want a message naming the ambiguity", problemsText(ps))
	}
	// The absent-key case (the zero value, never touched) must still be
	// accepted -- this test would also catch a mutation that refused BOTH
	// states instead of only the explicit one.
	if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": okProvider()}); len(ps) != 0 {
		t.Fatalf("an absent allow_data_to (the zero value) was wrongly refused too: %+v", ps)
	}
}

// TestProviderAllowDataToSpellings is the round-6 ruling: every spelling of
// "written, but with no repositories listed" is the same ambiguity as an
// explicit [], and is refused the same way. The dangerous ones are the null
// spellings -- `allow_data_to:` with every entry commented out, or `~` --
// which YAML gives the same Go value as an absent key, and which therefore
// used to mean "every repository" by accident (design §3a: absent, or
// ["*"], is every repository).
func TestProviderAllowDataToSpellings(t *testing.T) {
	const body = "kind: anthropic-compat\nbase_url: https://openrouter.ai/api\nauth: bearer\n" +
		"secret: openrouter-api-key\nmodels: [\"deepseek/*\"]\n"
	for name, tc := range map[string]struct {
		tail    string
		anyRepo bool
		open    bool
		refused bool
	}{
		"absent":              {"", true, true, false},
		"wildcard":            {"allow_data_to: [\"*\"]\n", true, true, false},
		"listed":              {"allow_data_to: [acme/app]\n", false, false, false},
		"explicit empty list": {"allow_data_to: []\n", false, false, true},
		"null":                {"allow_data_to:\n", false, false, true},
		"tilde":               {"allow_data_to: ~\n", false, false, true},
		"only comments":       {"allow_data_to:\n  # - acme/app\n", false, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			var p ModelProvider
			if err := yaml.Unmarshal([]byte(body+tc.tail), &p); err != nil {
				t.Fatal(err)
			}
			ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p})
			if tc.refused {
				if len(ps) != 1 || ps[0].Path != "providers.openrouter.allow_data_to" {
					t.Fatalf("%s: problems = %q, want one refusal of allow_data_to", name, problemsText(ps))
				}
				if !strings.Contains(ps[0].Message, "ambiguous") {
					t.Fatalf("%s: the refusal does not name the ambiguity: %q", name, ps[0].Message)
				}
				// ... and it stays fail-closed even if validation is
				// bypassed, exactly as the explicit [] does.
				if p.AllowsData("acme/anything") {
					t.Fatalf("%s: allows every repository despite being refused", name)
				}
				return
			}
			if len(ps) != 0 {
				t.Fatalf("%s: refused though it must be accepted: %q", name, problemsText(ps))
			}
			if got := p.AllowsData("acme/anything"); got != tc.anyRepo {
				t.Errorf("%s: AllowsData(acme/anything) = %v, want %v", name, got, tc.anyRepo)
			}
			// OpenToEveryRepo is the one question validate, init and doctor
			// ask to make the widest setting visible, so it must agree with
			// AllowsData here and must not call a refused spelling open.
			if got := p.OpenToEveryRepo(); got != tc.open {
				t.Errorf("%s: OpenToEveryRepo() = %v, want %v", name, got, tc.open)
			}
			named := OpenProviders(map[string]ModelProvider{"openrouter": p, "other": okProvider()})
			if tc.open {
				if len(named) != 1 || named[0] != "openrouter" {
					t.Errorf("%s: OpenProviders = %q, want [openrouter]", name, named)
				}
			} else if len(named) != 0 {
				t.Errorf("%s: OpenProviders = %q, want none", name, named)
			}
		})
	}
	// Reading the mapping node to tell a null from an absent key must not
	// cost the decoder's own strictness: an unknown field is still refused,
	// and still names the type, as yaml.v3's KnownFields does.
	var p ModelProvider
	err := yaml.Unmarshal([]byte(body+"api_key: sk-secret\n"), &p)
	if err == nil || !strings.Contains(err.Error(), "api_key") || !strings.Contains(err.Error(), "not found in type") {
		t.Fatalf("an unknown provider field: %v", err)
	}
}

func TestValidateModelProvidersAcceptsWildcardEntry(t *testing.T) {
	p := okProvider()
	p.AllowDataTo = []string{"*"}
	if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p}); len(ps) != 0 {
		t.Fatalf("the explicit [\"*\"] marker was refused: %+v", ps)
	}
	p.AllowDataTo = []string{"*", "acme/app"}
	if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p}); len(ps) != 0 {
		t.Fatalf("a mixed list with \"*\" was refused: %+v", ps)
	}
}

// A `.secret` that holds an API key used to be accepted whenever the key was
// spelled like a name (lower-case letters, digits and dashes), which is how
// most providers' keys begin. Every one of them starts with "sk-", so a value
// that does is refused as the key it is, not stored as the name of a secret.
func TestValidateModelProvidersRefusesAKeyWhereItsNameBelongs(t *testing.T) {
	for _, key := range []string{"sk-ant-abc123", "sk-proj-0123456789", "sk-or-v1-deadbeef"} {
		p := okProvider()
		p.Secret = key
		ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p})
		if len(ps) == 0 {
			t.Fatalf("%q was accepted as a secret name", key)
		}
		if !strings.Contains(problemsText(ps), "beginning of an API key") {
			t.Fatalf("problems = %q, want the key-pasted-here message", problemsText(ps))
		}
		// M1 (security review of PR #379): the whole point of the check is
		// that the value IS a key, so the refusal names the field and the
		// "sk-" it starts with, never the value — this text reaches a
		// terminal, a log, a halt message and result.json.
		if strings.Contains(problemsText(ps), key) {
			t.Fatalf("problems = %q: the refusal printed the key", problemsText(ps))
		}
	}
	// A name that merely contains the prefix is a name: only the beginning of
	// the value is the key's.
	p := okProvider()
	p.Secret = "openrouter-sk-key"
	if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p}); len(ps) != 0 {
		t.Fatalf("a name containing the prefix was refused: %+v", ps)
	}
}

// TestClaimMustBeExplicit pins the whole claim rule (Review Focus 2): the
// opt-in is one literal "anthropic/<id>" route entry, and the entry is the
// permission. A wildcard over the prefix, a bare claude-* spelling, a
// variant, a bracketed context window and an over-length ID all stay refused
// with the message this rule has always carried; a well-formed explicit ID is
// accepted, by a route's models here and (through the gateway's own call to
// CheckModelPattern) by the gateway's routes too.
func TestClaimMustBeExplicit(t *testing.T) {
	for _, m := range []string{
		"anthropic/claude-sonnet-5.5", "anthropic/claude-opus-5.5",
		"deepseek/*", "openai/gpt-5*",
	} {
		if msg := CheckModelPattern(m); msg != "" {
			t.Errorf("CheckModelPattern(%q) = %q, want accepted", m, msg)
		}
	}
	for _, m := range []string{
		"anthropic/*", "anthropic/claude-*", "claude-*", "claude-sonnet-5-5",
		"anthropic/claude-sonnet-5.5:free", "anthropic/claude-sonnet-5.5[1m]",
		// A padded route entry is refused too: the pattern rule and the claim
		// rule read the same string the YAML carried, with nothing trimmed.
		"anthropic/claude-sonnet-5.5 ", "anthropic\t/claude-sonnet-5.5",
		// Over CheckModelPattern's own 100-character bound, which refuses it
		// before the claim rule ever looks: pricing.IsProviderModelID is the
		// grammar alone and carries no bound of its own (Task 4).
		"anthropic/" + strings.Repeat("a", 100),
	} {
		if msg := CheckModelPattern(m); msg == "" {
			t.Errorf("CheckModelPattern(%q) = accepted, want refused", m)
		}
	}
	// The message a refused claim meets is the one it always did: the gateway
	// reads it, and a claim is not a licence to widen it.
	if msg := CheckModelPattern("anthropic/*"); !strings.Contains(msg, "would claim Claude models, which go direct to Anthropic") {
		t.Errorf("the refusal reads %q", msg)
	}
}

// TestCheckClaudeClaimIsOneShape: the exported helper is the rule, spelled
// once, so a later writer (the project layer's claim key) judges a claim by
// asking it rather than by repeating the grammar.
func TestCheckClaudeClaimIsOneShape(t *testing.T) {
	for _, m := range []string{"anthropic/claude-sonnet-5.5", "anthropic/claude-opus-5.5"} {
		if msg := CheckClaudeClaim(m); msg != "" {
			t.Errorf("CheckClaudeClaim(%q) = %q, want accepted", m, msg)
		}
	}
	for _, m := range []string{
		"anthropic/*", "anthropic/claude-*", "claude-*", "claude-sonnet-5-5", "anthropic/",
		"anthropic/claude-sonnet-5.5:free", "anthropic/claude-sonnet-5.5[1m]",
		"anthropic/Claude-Opus", "deepseek/deepseek-v4-flash",
		// Whitespace is not part of the grammar and is never trimmed away:
		// the helper is the rule, and Task 7 calls it directly on a layer
		// value, so a padded spelling is judged here and not by whoever
		// parsed the YAML. A space, a tab, a newline and a zero-width space,
		// on either side of an otherwise perfect claim.
		"anthropic/claude-sonnet-5.5 ", " anthropic/claude-sonnet-5.5",
		"\tanthropic/claude-sonnet-5.5", "anthropic/claude-sonnet-5.5\t",
		"anthropic/claude-sonnet-5.5\n", "\nanthropic/claude-sonnet-5.5",
		"anthropic/claude-sonnet-5.5\u200b",
	} {
		if msg := CheckClaudeClaim(m); msg == "" {
			t.Errorf("CheckClaudeClaim(%q) = accepted, want refused", m)
		}
	}
}

// TestTheSecretNameRuleIsOneRule: CheckSecretName (the project policy's
// profiles hold every secret name to it) and ValidateModelProviders (the
// providers block) are two readers with two wordings and one rule —
// SecretNameRE and keyPrefix. This test is what holds them together: for
// every name the shared rule refuses, the providers block refuses it too, and
// for every name it accepts the providers block accepts it (the provider-only
// rules — a name Fugaro already uses, two providers claiming one secret — are
// outside the comparison, which is the point: they are the provider path's
// own, and a profile's secrets do not have them).
//
// The mutation is a reader that keeps its own copy of the rule: give either
// one a regex or a prefix the other does not have and the verdicts part.
func TestTheSecretNameRuleIsOneRule(t *testing.T) {
	for _, name := range []string{
		"openrouter-api-key", "deploy-key", "a", "sk-ant-abc123", "sk-or-v1-deadbeef",
		"UpperCase", "with_underscore", "has space", "", "trailing-",
	} {
		shared := CheckSecretName(name)
		p := okProvider()
		p.Secret = name
		ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p})
		if shared != "" && len(ps) == 0 {
			t.Errorf("CheckSecretName refuses %q and the providers block accepts it", name)
		}
		if shared == "" && len(ps) != 0 && !strings.Contains(problemsText(ps), "already") {
			t.Errorf("CheckSecretName accepts %q and the providers block refuses it: %s", name, problemsText(ps))
		}
	}
}

// TestModelSecretEnvsAreTheReservedOnes: the variables a model credential is
// mounted as are the reserved names themselves, never a second spelling of
// them. A rename of a reserved key that left ModelSecretEnvs behind would
// make the credentials service's job-account rule (modules/workflow/sa.tf)
// drop the accessor for a variable no job has, while the renamed one kept it.
//
// The mutation is ModelSecretEnvs built from a literal instead of
// ReservedSecrets: the two then part as soon as a reserved key is renamed.
func TestModelSecretEnvsAreTheReservedOnes(t *testing.T) {
	want := map[string]string{ClaudeOAuthToken: "CLAUDE_CODE_OAUTH_TOKEN", AnthropicSecret: "ANTHROPIC_API_KEY"}
	if len(ModelSecretEnvs) != len(want) {
		t.Fatalf("ModelSecretEnvs = %v, want the two reserved model credentials", ModelSecretEnvs)
	}
	for _, env := range ModelSecretEnvs {
		found := ""
		for _, logical := range []string{ClaudeOAuthToken, AnthropicSecret} {
			if ReservedSecrets[logical] == env {
				found = logical
			}
		}
		if found == "" {
			t.Errorf("ModelSecretEnvs holds %q, which no reserved model credential maps to", env)
			continue
		}
		if env != want[found] {
			t.Errorf("ReservedSecrets[%s] = %q, want %q", found, env, want[found])
		}
	}
}

// TestIsModelSecretEnv is the MODEL half of the credentials service's rule
// (C1.18): the two reserved model variables and every provider key's
// namespace, and nothing else. Both git credentials are deliberately out —
// this predicate is about model credentials, and it says nothing about which
// of the two the service holds: Bitbucket's token stays with the job (D145),
// the GitHub App key does not (C2.3), and IsServiceHeldSecretEnv is where that
// difference lives.
//
// The mutation is IsProviderKeyEnv's prefix read case-sensitively, or a third
// name added to ModelSecretEnvs: either one drops an accessor from a job that
// still mounts the secret, or keeps one for a secret the service holds.
func TestIsModelSecretEnv(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want bool
	}{
		{"CLAUDE_CODE_OAUTH_TOKEN", true},
		{"ANTHROPIC_API_KEY", true},
		{ProviderKeyEnv("openrouter-api-key"), true},
		{ProviderKeyEnvPrefix + "OPENROUTER_API_KEY", true},
		{"FUGARO_PROVIDER_KEY", false},           // the prefix alone is not a key
		{"FUGARO_BITBUCKET_TOKEN", false},        // the git credentials are not model credentials
		{"FUGARO_GITHUB_APP_PRIVATE_KEY", false}, // ... neither of them
		{"FUGARO_BITBUCKET_TOKEN ", false},       // no trailing space is trimmed into a match
		{"SANDBOX_PROBE", false},
		{"", false},
	} {
		if got := IsModelSecretEnv(tc.env); got != tc.want {
			t.Errorf("IsModelSecretEnv(%q) = %v, want %v", tc.env, got, tc.want)
		}
	}
}

// TestServiceHeldSecretNamesAreTheReservedOnes: the one logical name in
// ServiceHeldSecretNames is the reserved name of the GitHub App's private key
// (C2.3, D145) and not a second spelling of it. A rename of the reserved key
// that left this list behind would drop the accessor of a name no job mounts,
// while the renamed one kept it — the App key would stay with the job on the
// installation that opted in precisely to take it away.
//
// The mutation is a literal in place of ReservedSecrets[GitHubAppKeySecret].
func TestServiceHeldSecretNamesAreTheReservedOnes(t *testing.T) {
	if len(ServiceHeldSecretNames) != 1 {
		t.Fatalf("ServiceHeldSecretNames = %v, want the one GitHub App key: the model credentials are identified by their variables, and Bitbucket's token is D145's exception", ServiceHeldSecretNames)
	}
	if got := ServiceHeldSecretNames[0]; got != GitHubAppKeySecret {
		t.Errorf("ServiceHeldSecretNames[0] = %q, want GitHubAppKeySecret (%q)", got, GitHubAppKeySecret)
	}
	env, ok := ReservedSecrets[GitHubAppKeySecret]
	if !ok {
		t.Fatalf("ReservedSecrets has no %q: the name this list drops the job's accessor for is not a reserved name at all", GitHubAppKeySecret)
	}
	if env != "FUGARO_GITHUB_APP_PRIVATE_KEY" {
		t.Errorf("ReservedSecrets[%s] = %q, want the App key's mount variable", GitHubAppKeySecret, env)
	}
}

// TestIsServiceHeldSecretEnv is the whole predicate the credentials service's
// accessor rule is written from (C1.18, C2.3): a model credential by its
// variable, the GitHub App's private key by its logical name, and nothing
// else. It is the question modules/workflow/sa.tf's job_secret_names asks and
// internal/infra's mirror repeats, so a clause lost here is an accessor a job
// keeps on a credential the service was to hold.
//
// The mutations are the two clauses: dropping the name clause leaves
// github-app-key with the job (C2.3's whole point), and dropping the model
// clause leaves the model credentials with it (C1.18's). The Bitbucket token
// and a workflow's own secret must stay false either way (D145).
func TestIsServiceHeldSecretEnv(t *testing.T) {
	for _, tc := range []struct {
		env, name string
		want      bool
	}{
		// The model credentials, by variable, whatever the name is.
		{"CLAUDE_CODE_OAUTH_TOKEN", ClaudeOAuthToken, true},
		{"ANTHROPIC_API_KEY", AnthropicSecret, true},
		{ProviderKeyEnv("openrouter-api-key"), "openrouter-api-key", true},
		{ProviderKeyEnvPrefix + "OPENROUTER_API_KEY", "some-provider-key", true},
		// The GitHub App's private key, by name.
		{"FUGARO_GITHUB_APP_PRIVATE_KEY", GitHubAppKeySecret, true},
		// D145's exceptions: Bitbucket's token and a workflow's own secret
		// keep the job's accessor, whatever they are mounted as.
		{"FUGARO_BITBUCKET_TOKEN", BitbucketSecret, false},
		{"NPM_TOKEN", "npm-token", false},
		{"SANDBOX_PROBE", "sandbox-probe", false},
		// The two halves are independent: the App key's NAME decides on its
		// own, whatever variable the entry carries (no workflow can mount a
		// reserved name under a variable of its own — config.ReservedEnv
		// refuses every FUGARO_ variable — so the two always agree in a real
		// spec, and the mirror's rule is the name's).
		{"NPM_TOKEN", GitHubAppKeySecret, true},
		// And neither half matches an unrelated pair.
		{"", "", false},
	} {
		if got := IsServiceHeldSecretEnv(tc.env, tc.name); got != tc.want {
			t.Errorf("IsServiceHeldSecretEnv(%q, %q) = %v, want %v", tc.env, tc.name, got, tc.want)
		}
	}
}
