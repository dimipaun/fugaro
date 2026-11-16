package config

import (
	"reflect"
	"strings"
	"testing"
)

// TestCraftInheritance: a craft that sets nothing takes the model and the
// access of the craft it follows — review follows coder, security-review
// follows senior-review — and the two halves inherit separately, so a craft
// that sets only its access keeps the model it inherits and one that sets only
// its model keeps the access it inherits (docs/model-crafts/spec.md §3,
// reconciliation D1620).
func TestCraftInheritance(t *testing.T) {
	c := Crafts{
		Coder:          Craft{Model: "deepseek-v3", Access: "openrouter-main"},
		Review:         Craft{Access: "anthropic-key"},
		SeniorReview:   Craft{Model: "opus-4", Access: "claude-1"},
		SecurityReview: Craft{Model: "opus-4-1"},
	}
	for _, tc := range []struct {
		craft string
		want  Craft
	}{
		// The coder sets both and inherits nothing: it is the one craft a run
		// cannot do without (spec §3).
		{CraftCoder, Craft{Model: "deepseek-v3", Access: "openrouter-main"}},
		// The review craft keeps the coder's model and overrides its access
		// alone: inheritance is per half.
		{CraftReview, Craft{Model: "deepseek-v3", Access: "anthropic-key"}},
		// The senior review sets both and follows nobody.
		{CraftSeniorReview, Craft{Model: "opus-4", Access: "claude-1"}},
		// The security review keeps the senior's access and changes only the
		// model — the spec's own example of the two halves parting.
		{CraftSecurityReview, Craft{Model: "opus-4-1", Access: "claude-1"}},
	} {
		if got := c.Inherited(tc.craft); got != tc.want {
			t.Errorf("%s inherits %+v, want %+v", tc.craft, got, tc.want)
		}
	}

	// A security review turned on without settings is the senior review's
	// settings, both halves (spec §3).
	only := Crafts{SeniorReview: Craft{Model: "opus-4", Access: "claude-1"}}
	if got, want := only.Inherited(CraftSecurityReview), only.SeniorReview; got != want {
		t.Errorf("a security review with no settings inherits %+v, want the senior review's %+v", got, want)
	}
	// Nothing set anywhere leaves both halves empty: what an unset craft
	// resolves to is the project's own fallback, not this block's business.
	if got := (Crafts{}).Inherited(CraftSecurityReview); got != (Craft{}) {
		t.Errorf("an empty crafts block inherits %+v, want nothing", got)
	}
	// An unknown name is no craft: it must not silently inherit the coder's
	// settings.
	if got := c.Inherited("code-review"); got != (Craft{}) {
		t.Errorf("an unknown craft name answers %+v, want nothing", got)
	}
}

// TestSecurityCraftDoesNotEnableLevel3: the security-review craft decides a
// model and an access when level 3 is ON, and nothing else. The switch that
// turns level 3 on stays the review levels' business (internal/rigor, the
// recipe and `fugaro run --review-levels`), so a file that sets the craft and
// nothing else parses to the same configuration as the same file without it,
// its agent.levels block included (reconciliation F14).
func TestSecurityCraftDoesNotEnableLevel3(t *testing.T) {
	const without = `version: 1
project: aurora
git: {provider: github}
agent:
  auth: vertex
  models: {coder: deepseek-v3, reviewer: opus-4}
workflows:
  server: {commands: {build: make, test: make test}}
`
	const with = `version: 1
project: aurora
git: {provider: github}
agent:
  auth: vertex
  models: {coder: deepseek-v3, reviewer: opus-4}
  crafts:
    security-review: {model: opus-4-1}
workflows:
  server: {commands: {build: make, test: make test}}
`
	plain, ps := Parse([]byte(without))
	if len(ps) > 0 {
		t.Fatalf("the file without the craft has problems: %v", ps)
	}
	crafted, ps := Parse([]byte(with))
	if len(ps) > 0 {
		t.Fatalf("the file with the security-review craft has problems: %v", ps)
	}
	if got, want := crafted.Agent.Crafts.SecurityReview, (Craft{Model: "opus-4-1"}); got != want {
		t.Errorf("the file's security-review craft is %+v, want the model it wrote and no access of its own: %+v", got, want)
	}

	// The craft is the only difference: no level's block, and nothing else in
	// the configuration, moved.
	crafted.Agent.Crafts = Crafts{}
	if !reflect.DeepEqual(plain, crafted) {
		t.Errorf("setting the security-review craft changed more than the craft:\n without: %+v\n with:    %+v", plain, crafted)
	}
	// Spelled out, because it is the fact the test is named for: level 3 is
	// off in both, and the craft's three siblings are untouched.
	if crafted.Agent.Levels.L3 != nil {
		t.Errorf("the security-review craft turned level 3 on: %+v", crafted.Agent.Levels.L3)
	}
}

// TestNotYetTypesRefuse: the credential types this release ships are four, and
// the two the spec lists as future work — an OpenAI API key and a Codex OAuth
// credential — refuse in words that say so rather than as an unknown type or,
// worse, by being accepted (reconciliation F3, D1623).
func TestNotYetTypesRefuse(t *testing.T) {
	for _, typ := range []string{CredentialTypeOpenAI, CredentialTypeCodexOAuth} {
		ps := ValidateCredentials([]Credential{{Name: "key-1", Type: typ}})
		if len(ps) != 1 {
			t.Fatalf("%s: got %d problems, want one: %v", typ, len(ps), ps)
		}
		if !strings.Contains(ps[0].Message, "not yet") {
			t.Errorf("%s: the refusal does not say the type is not yet supported: %s", typ, ps[0].Message)
		}
		if !strings.Contains(ps[0].Message, typ) {
			t.Errorf("%s: the refusal does not name the type: %s", typ, ps[0].Message)
		}
		if ps[0].Path != "credentials.key-1.type" {
			t.Errorf("%s: the refusal is at %q, want credentials.key-1.type", typ, ps[0].Path)
		}
	}
	// Every type that does ship is accepted, and an unknown one is refused as
	// unknown rather than as a "not yet".
	for _, typ := range []string{CredentialTypeOpenRouter, CredentialTypeAnthropic, CredentialTypeClaudeOAuth} {
		if ps := ValidateCredentials([]Credential{{Name: "key-1", Type: typ}}); len(ps) > 0 {
			t.Errorf("%s is refused: %v", typ, ps)
		}
	}
	if ps := ValidateCredentials([]Credential{{Name: "key-1", Type: "gemini"}}); len(ps) != 1 || strings.Contains(ps[0].Message, "not yet") {
		t.Errorf("an unknown type is not refused as unknown: %v", ps)
	}
}

// TestReservedCredentialNamesRefuse: a name the platform already holds a
// credential under is refused as a DEFINITION and admitted as a reference,
// which is the rule claude_accounts states for `default` and the shape this
// block's two ends have to mirror — a credential that defines `vertex` would be
// a second credential no run could tell from the built-in one, and one that
// defines `default` would be an entry nothing can reach, since a craft that sets
// no access already resolves to the default.
func TestReservedCredentialNamesRefuse(t *testing.T) {
	for _, name := range []string{DefaultClaudeAccount, CredentialVertex, AnthropicKeyAccount, "acme-review", "Not A Name", strings.Repeat("x", MaxClaudeAccountNameLen+1), ""} {
		if ps := ValidateCredentials([]Credential{{Name: name, Type: CredentialTypeAnthropic}}); len(ps) == 0 {
			t.Errorf("%q is defined as a credential and nothing refuses it", name)
		}
		if ps := ValidatePools([]Pool{{Name: name, Members: []string{CredentialVertex}}}, nil); len(ps) == 0 {
			t.Errorf("%q is named as a pool and nothing refuses it", name)
		}
	}
	if ps := ValidateCredentials([]Credential{{Name: "acme-main", Type: CredentialTypeAnthropic}}); len(ps) > 0 {
		t.Errorf("an ordinary name is refused: %v", ps)
	}
	// The other end: a reference to the built-in credential is valid, and the
	// default is not — a craft whose access is `default` names nothing a run
	// could resolve, because that is what an unset access already resolves to.
	if msg := ValidCredentialRef(CredentialVertex); msg != "" {
		t.Errorf("a reference to the built-in %s is refused: %s", CredentialVertex, msg)
	}
	if msg := ValidCredentialRef(DefaultClaudeAccount); msg == "" {
		t.Errorf("%q is accepted as a craft's access", DefaultClaudeAccount)
	}
}

// poolMembers is the credentials block the pool tests read: one subscription
// credential a pool of one may name.
func poolMembers() []Credential {
	return []Credential{{Name: "oauth-one", Type: CredentialTypeClaudeOAuth}}
}

// TestNewPoolWithoutOwnerRefused: a pool written at the version that gives
// pools an owner names one (D1936), and the rule is the PUBLISHED node's alone.
// Two documents carry a pool and they are not the same thing: the policy node
// is what every run of the project resolves a credential through, so a pool
// nobody owns there is a pool nobody answers for — `fugaro project policy
// publish` reads the node it replaces and refuses it. The installation's own
// config is the owner's own file, and `fugaro update` may not rewrite it: a
// config an upgrade starts refusing is a broken installation (CLAUDE.md's HARD
// RULE), and no pool of it is served from a slip until it is published.
//
// A pool that predates owners is the third case, and it is why the marker is a
// name of its own: `legacy_open` says "served to everyone, the way it was",
// and the absence of `owner` says nothing at all (F37).
func TestNewPoolWithoutOwnerRefused(t *testing.T) {
	ownerless := Pool{Name: "claude", Members: []string{"oauth-one"}}
	if ps := ValidatePools([]Pool{ownerless}, poolMembers()); len(ps) != 0 {
		t.Errorf("an installation's own pool with no owner is refused: %v", ps)
	}
	ps := ValidatePublishedPools([]Pool{ownerless}, poolMembers())
	if len(ps) != 1 {
		t.Fatalf("a published pool with no owner is answered with %d problem(s), want one: %v", len(ps), ps)
	}
	if ps[0].Path != "pools.claude" || !strings.Contains(ps[0].Message, "owner is required") {
		t.Errorf("the refusal is %q at %q, want the pool's owner named as missing", ps[0].Message, ps[0].Path)
	}
	for _, ok := range []Pool{
		{Name: "claude", Members: []string{"oauth-one"}, Owner: "owner@example.com"},
		{Name: "claude", Members: []string{"oauth-one"}, LegacyOpen: true},
	} {
		if ps := ValidatePublishedPools([]Pool{ok}, poolMembers()); len(ps) != 0 {
			t.Errorf("%+v is refused as a published pool: %v", ok, ps)
		}
	}
	// The marker and an owner are two answers to one question, and a pool that
	// carries both is refused wherever it is written rather than resolved to
	// one of them.
	for _, bad := range []Pool{
		{Name: "claude", Members: []string{"oauth-one"}, Owner: "owner@example.com", LegacyOpen: true},
		{Name: "claude", Members: []string{"oauth-one"}, AllowedUsers: []string{"teammate@example.com"}, LegacyOpen: true},
	} {
		if ps := ValidatePools([]Pool{bad}, poolMembers()); len(ps) == 0 {
			t.Errorf("%+v is written and nothing refuses it", bad)
		}
	}
	// The owner and the allowed users are addresses: the roster is keyed by
	// email (R10), so a value no address could be is a pool no person is ever
	// served from.
	for _, bad := range []Pool{
		{Name: "claude", Members: []string{"oauth-one"}, Owner: "owner"},
		{Name: "claude", Members: []string{"oauth-one"}, Owner: "owner@example.com", AllowedUsers: []string{"teammate"}},
		{Name: "claude", Members: []string{"oauth-one"}, Owner: "owner@example.com", AllowedUsers: []string{"one@example.com", "one@example.com"}},
	} {
		if ps := ValidatePools([]Pool{bad}, poolMembers()); len(ps) == 0 {
			t.Errorf("%+v is written and nothing refuses it", bad)
		}
	}
}

// TestNewPoolDefaultsToOnlyOwner: D1936's default is the owner alone, and a
// pool that names allowed users serves them too. A pool with no owner and no
// marker serves nobody — the state a pool is in while it is being written, and
// the direction a pool nobody answers for has to fail in.
func TestNewPoolDefaultsToOnlyOwner(t *testing.T) {
	p := Pool{Name: "claude", Members: []string{"oauth-one"}, Owner: "owner@example.com"}
	if !p.Serves("owner@example.com") {
		t.Error("a pool does not serve its own owner")
	}
	for _, email := range []string{"teammate@example.com", "", "OWNER@example.com"} {
		if p.Serves(email) {
			t.Errorf("a pool with no allowed users serves %q", email)
		}
	}
	p.AllowedUsers = []string{"teammate@example.com"}
	if !p.Serves("teammate@example.com") || !p.Serves("owner@example.com") {
		t.Error("a pool with an allowed user does not serve both that user and its owner")
	}
	if p.Serves("stranger@example.com") {
		t.Error("a pool serves someone neither the owner nor an allowed user")
	}
	if (Pool{Name: "claude", Members: []string{"oauth-one"}}).Serves("anyone@example.com") {
		t.Error("a pool with no owner and no marker serves someone")
	}
}
