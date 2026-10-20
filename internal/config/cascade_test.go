package config

import (
	"strings"
	"testing"
)

// cascadeYAML is a valid `agent.auth: cascade` file: the credential cascade
// of docs/credentials-service (D133, D150) asked for by a repository, with
// the budget mode on, which is the one shape a cascade workflow may have.
const cascadeYAML = `
version: 1
project: aurora
git: { provider: github }
agent:
  auth: cascade
budget:
  mode: observe
  per_run_usd: 2
workflows:
  server:
    commands:
      build: ./gradlew assemble
      test: ./gradlew test
`

// TestValidateAcceptsCascade is the red step of Task C1.1: `cascade` is one
// of the values agent.auth may take, and the file above is valid with it —
// not a misspelling the auth list refuses, and not an auth that only the
// schema knows. AgentAuths is the one list every reader shares
// (internal/config/defaults.go): the validator, the layer's own auth key and
// the launcher's check of a run's spec all read it, so a value it accepts is
// never one of them refuses as unknown.
//
// Mutation (run, restored): drop "cascade" from AgentAuths in defaults.go —
// this test fails on KnownAgentAuth, and TestLayerAcceptsCascade fails on the
// layer's own defaults.agent.auth, which is the same list read twice.
func TestValidateAcceptsCascade(t *testing.T) {
	if !KnownAgentAuth(AuthCascade) {
		t.Fatalf("KnownAgentAuth(%q) = false, want true: AgentAuths = %v", AuthCascade, AgentAuths)
	}
	cfg, problems := Parse([]byte(cascadeYAML))
	if len(problems) > 0 {
		t.Fatalf("a cascade file was refused at parse time: %s", problemsText(problems))
	}
	if cfg.Agent.Auth != AuthCascade {
		t.Fatalf("agent.auth = %q, want %q", cfg.Agent.Auth, AuthCascade)
	}
	// The list is the sentence a misspelling reads: cascade has to be in it,
	// or the fix line sends the author to a value that is not the one they
	// meant.
	_, problems = Parse([]byte(strings.Replace(cascadeYAML, "auth: cascade", "auth: cascad", 1)))
	if got := problemsText(problems); !strings.Contains(got, "agent.auth: must be one of") || !strings.Contains(got, AuthCascade) {
		t.Fatalf("a misspelt auth was not refused naming %q: %s", AuthCascade, got)
	}
}

// TestCascadeRefusedWithBudgetOff is the gateway rule a cascade workflow is
// held to: its calls are made by the gateway, so a mode that is off — written
// as off, or unset — is refused, naming the mode it found. observe and
// enforce both pass: the cascade needs the gateway to be on, not enforcing,
// because what the gateway does for a cascade run is carry the run's
// credential and its accounting (D143), not cap a provider's dollars.
//
// mode is the run's *effective* mode (policy.Merge over the ceiling and the
// file), which is why this is a function of a mode rather than a rule inside
// Validate: a file that writes no budget under an installation whose ceiling
// enforces is a cascade workflow whose gateway is on.
//
// Mutation (run, restored): return nil from CheckCascadeGateway — the "" and
// "off" rows fail, naming the modes it no longer refuses.
func TestCascadeRefusedWithBudgetOff(t *testing.T) {
	a := Agent{Auth: AuthCascade}
	for _, mode := range []string{"", "off"} {
		got := problemsText(CheckCascadeGateway(a, mode))
		if !strings.Contains(got, "agent.auth") || !strings.Contains(got, "budget.mode") {
			t.Errorf("mode %q: not refused: %s", mode, got)
		}
		want := "off"
		if mode == "" {
			want = "not set"
		}
		if !strings.Contains(got, want) {
			t.Errorf("mode %q: the refusal does not name the mode it found (%q): %s", mode, want, got)
		}
	}
	for _, mode := range []string{"observe", "enforce"} {
		if got := problemsText(CheckCascadeGateway(a, mode)); got != "" {
			t.Errorf("mode %q: refused, want accepted: %s", mode, got)
		}
	}
	// Another auth is not this rule's business, whatever the mode.
	for _, auth := range []string{"vertex", "api-key", "oauth"} {
		if got := problemsText(CheckCascadeGateway(Agent{Auth: auth}, "off")); got != "" {
			t.Errorf("auth %q: refused by the cascade rule: %s", auth, got)
		}
	}
}

// TestCascadeRefusedWithAllowPublic is D130's carry-over, from the held
// OAuth reviewer-stage plan (its P6): a follow-up run on a public repository
// is one a commenter who can push branches can steer, and the cascade is the
// auth whose whole point is a shared subscription credential behind it, so
// `followup.allow_public: true` and `agent.auth: cascade` are refused
// together, at the key that has to go. The same allow_public under any other
// auth stays valid: this is a rule about the pair, not about public
// follow-ups.
//
// The negative half is only worth anything if the file it parses really pairs
// that auth with allow_public, so each row asserts the parsed config is the
// one it meant to build (auth and allow_public both). The helper refuses to
// return a string it did not rewrite for the same reason: a needle built from
// the auth being asked for ("auth: "+auth) matches only the cascade row, and
// the other three rows then parse the cascade fixture unchanged — three
// assertions about vertex, api-key and oauth that were really assertions
// about a cascade file with no followup block at all.
//
// Mutation (run, restored): drop the validateCascadeFollowup call from
// Validate — the pair parses and the refusal's key, "%s" and "cascade" all
// disappear with it. Mutation (make validateCascadeFollowup refuse
// allow_public for every auth instead of only the cascade, i.e. drop the
// `a.Auth != AuthCascade` half): the negative rows fail, naming the auth the
// rule has no business refusing.
func TestCascadeRefusedWithAllowPublic(t *testing.T) {
	public := func(t *testing.T, auth string) string {
		t.Helper()
		const needle = "agent:\n  auth: " + AuthCascade + "\n"
		out := strings.Replace(cascadeYAML, needle,
			"agent:\n  auth: "+auth+"\nfollowup:\n  allow_public: true\n", 1)
		if out == cascadeYAML {
			t.Fatalf("the fixture no longer spells %q: the rewrite matched nothing, so every row would parse the cascade file itself", needle)
		}
		return out
	}
	// Parse validates, so the pair is refused there: the file has to be
	// written with another auth to be read at all.
	_, problems := Parse([]byte(public(t, AuthCascade)))
	got := problemsText(problems)
	if !strings.Contains(got, "followup.allow_public") {
		t.Fatalf("cascade with followup.allow_public was not refused at followup.allow_public: %s", got)
	}
	for _, want := range []string{"allow_public", "cascade"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not name %q: %s", want, got)
		}
	}
	for _, auth := range []string{"vertex", "api-key", "oauth"} {
		c, problems := Parse([]byte(public(t, auth)))
		if len(problems) > 0 {
			t.Fatalf("%s: refused at parse time: %s", auth, problemsText(problems))
		}
		// The row is about the pair, so the pair has to be in the file: this
		// is the assertion the three vacuous rows above could not make.
		if c.Agent.Auth != auth || !c.Followup.AllowPublic {
			t.Fatalf("%s: the file parsed as auth %q with allow_public = %v, so this row is not about %s with a public follow-up at all",
				auth, c.Agent.Auth, c.Followup.AllowPublic, auth)
		}
		if got := problemsText(Validate(c)); got != "" {
			t.Errorf("auth %q with followup.allow_public was refused: %s", auth, got)
		}
	}
}

// TestLayerAcceptsCascade: the project layer's defaults may set the same
// auth the file may, through the same list (internal/config/layer.go). A
// layer is what an operator writes over a repository's file, so a cascade
// installation that wants the cascade everywhere sets it there — and a layer
// that could not name it would leave the operator writing it into every
// repository's own fugaro.yaml.
func TestLayerAcceptsCascade(t *testing.T) {
	const text = `version: 1
project: acme
gcp_project: acme-fugaro
defaults:
  agent:
    auth: cascade
`
	l, ps := ParseProjectLayer([]byte(text), LayerAnchor{Project: "acme", GCPProject: "acme-fugaro"})
	if len(ps) > 0 {
		t.Fatalf("a layer defaulting agent.auth to cascade was refused: %s", problemsText(ps))
	}
	if l.Defaults.Agent.Auth != AuthCascade {
		t.Fatalf("defaults.agent.auth = %q, want %q", l.Defaults.Agent.Auth, AuthCascade)
	}
	// And the refusal still fires for a misspelling, over the same list: the
	// layer's check and the file's are one rule.
	_, ps = ParseProjectLayer([]byte(strings.Replace(text, "auth: cascade", "auth: cascad", 1)),
		LayerAnchor{Project: "acme", GCPProject: "acme-fugaro"})
	if got := problemsText(ps); !strings.Contains(got, "defaults.agent.auth") || !strings.Contains(got, AuthCascade) {
		t.Fatalf("a misspelt layer auth was not refused naming %q: %s", AuthCascade, got)
	}
}

// TestCheckProviderAuthCascadeNeverSendsOAuthToAProvider: the cascade's tier
// A is a shared subscription OAuth token, and such a token is never sent to
// another provider — the same rule that refuses `agent.auth: oauth` with a
// provider route. A cascade run's provider route carries the cascade's own
// credential (the gateway attaches what the cascade chose), and config time
// cannot know which credential that will be, so the pin a provider serves is
// refused outright: the fix is `agent.auth: api-key` for the run that names
// one. A cascade file whose pins no provider claims is untouched.
//
// Mutation (run, restored): drop the `case AuthCascade` arm of the switch in
// providers.go — the pin is still refused, but for vertex's reason
// ("vertex serves Claude only"), and the assertions on "OAuth" and on the
// cascade's own sentence fail. The arm is the refusal; the fallthrough is the
// reason it must not be reached with.
func TestCheckProviderAuthCascadeNeverSendsOAuthToAProvider(t *testing.T) {
	providers := map[string]ModelProvider{"openrouter": okProvider()}
	ps := CheckProviderAuth(Agent{Auth: AuthCascade, Models: ModelRoles{Coder: "deepseek/deepseek-v4-flash"}}, providers)
	if len(ps) != 1 || ps[0].Path != "agent.models.coder" {
		t.Fatalf("a cascade pin a provider serves was not refused once: %s", problemsText(ps))
	}
	got := problemsText(ps)
	for _, want := range []string{"deepseek/deepseek-v4-flash", "openrouter", "agent.auth: api-key", AuthCascade, "OAuth"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not name %q: %s", want, got)
		}
	}
	// The neighbouring auths keep their own refusals, and the cascade adds
	// one rather than replacing them.
	if got := problemsText(CheckProviderAuth(Agent{Auth: AuthCascade}, providers)); got != "" {
		t.Errorf("a cascade agent with no provider pin was refused: %s", got)
	}
	if got := problemsText(CheckProviderAuth(Agent{Auth: "oauth", Models: ModelRoles{Coder: "deepseek/deepseek-v4-flash"}}, providers)); !strings.Contains(got, "oauth") {
		t.Errorf("the oauth refusal changed: %s", got)
	}
	if got := problemsText(CheckProviderAuth(Agent{Auth: AuthCascade, Models: ModelRoles{Coder: "claude-sonnet-5-5"}}, providers)); got != "" {
		t.Errorf("a Claude pin was refused for a cascade run: %s", got)
	}
}
