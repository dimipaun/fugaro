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
