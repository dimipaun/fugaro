package config

import (
	"fmt"
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
	// The craft is a project layer's now (D3237, R2): a repository's file may
	// write agent.crafts.*.model and no longer decides it, so the craft the
	// level-3 switch has to stay clear of is the one a project states.
	const head = `version: 1
project: aurora
gcp_project: aurora-fugaro
defaults:
  agent:
    auth: vertex
    models: {coder: deepseek-v3, reviewer: opus-4}
`
	const repo = `version: 1
project: aurora
gcp_project: aurora-fugaro
git: {provider: github}
workflows:
  server: {commands: {build: make, test: make test}}
`
	layer := func(extra string) *ProjectLayer {
		l, ps := ParseProjectLayer([]byte(head+extra), LayerAnchor{Project: "aurora", GCPProject: "aurora-fugaro"})
		if len(ps) > 0 {
			t.Fatalf("the layer %q has problems: %v", extra, ps)
		}
		return l
	}
	plain, _, ps := Resolve([]byte(repo), layer(""))
	if len(ps) > 0 {
		t.Fatalf("the layer without the craft has problems: %v", ps)
	}
	crafted, _, ps := Resolve([]byte(repo), layer("    crafts:\n      security-review: {model: opus-4-1}\n"))
	if len(ps) > 0 {
		t.Fatalf("the layer with the security-review craft has problems: %v", ps)
	}
	if got, want := crafted.Agent.Crafts.SecurityReview, (Craft{Model: "opus-4-1"}); got != want {
		t.Errorf("the layer's security-review craft is %+v, want the model it wrote and no access of its own: %+v", got, want)
	}

	// The craft is the only difference: no level's block, and nothing else in
	// the configuration, moved. The layer a config carries is the field the
	// two resolutions' own layers differ in, so it goes with the craft.
	crafted.Agent.Crafts = Crafts{}
	plain.Layer, crafted.Layer = nil, nil
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

// TestPoolMayTakeItsOnlyMembersName is the one collision ValidatePools admits
// between a pool's name and a credential's, and the boundary the rule draws.
//
// A pool of one named after its only member is the shape every subscription
// token is written in (D3176): `fugaro accounts add` writes it for a new token
// and `fugaro update` migrates every lone account into one, so the config that
// holds a token would be a config its own next read refuses if the namespace
// rule did not admit the pair. What is still refused is every pool that would
// leave the name meaning more than that one credential: a second member, or a
// member that is another credential under the shared name.
func TestPoolMayTakeItsOnlyMembersName(t *testing.T) {
	creds := []Credential{{Name: "team-a", Type: CredentialTypeClaudeOAuth}}
	if ps := ValidatePools([]Pool{{Name: "team-a", Members: []string{"team-a"}}}, creds); len(ps) != 0 {
		t.Errorf("a pool of one named after its only member is refused: %v", ps)
	}
	for _, bad := range []Pool{
		{Name: "team-a", Members: []string{"team-a", CredentialVertex}},
		{Name: "team-a", Members: []string{CredentialVertex, "team-a"}},
		{Name: "team-a", Members: []string{CredentialVertex}},
	} {
		ps := ValidatePools([]Pool{bad}, creds)
		if len(ps) == 0 {
			t.Errorf("%+v is written and nothing refuses the name it shares with a credential", bad)
			continue
		}
		if !strings.Contains(ps[0].Message, "a credential of this name is defined in the same block") {
			t.Errorf("%+v is refused with %q, want the namespace rule's own sentence", bad, ps[0].Message)
		}
	}
	// The built-in vertex is a name a reference may carry and a definition
	// never does, so a pool named after it is no pool of the block's own
	// credential and the name rule refuses it as it always did.
	for _, members := range [][]string{{CredentialVertex}, {"team-a", CredentialVertex}} {
		if ps := ValidatePools([]Pool{{Name: CredentialVertex, Members: members}}, creds); len(ps) == 0 {
			t.Errorf("a pool named %s with members %v is written and nothing refuses it", CredentialVertex, members)
		}
	}
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

// poolCrossingMembers is the credentials block the crossing tests read: two
// subscriptions and a key on the credentials service's path, and a route on the
// run's own gateway's, so a pool can be written on either side of the seam and
// with a member between the crossing's two.
func poolCrossingMembers() []Credential {
	return []Credential{
		{Name: "sub-one", Type: CredentialTypeClaudeOAuth},
		{Name: "sub-two", Type: CredentialTypeClaudeOAuth},
		{Name: "key-one", Type: CredentialTypeAnthropic},
		{Name: "router-one", Type: CredentialTypeOpenRouter},
	}
}

// TestPoolCannotCrossTheTwoPaths is D1668 and this task's red step: a pool whose
// members are not all served on one path is refused, whichever way it lists
// them, at the member its own order crosses at — the first one whose half is not
// the first member's. The order is what makes the refusal necessary rather than
// tidy: failover walks the list (D1632), and neither half can walk a list that
// reaches into the other (F7, F20).
func TestPoolCannotCrossTheTwoPaths(t *testing.T) {
	for _, tc := range []struct {
		name    string
		members []string
		at      int
	}{
		{"a subscription then a route", []string{"sub-one", "router-one"}, 1},
		{"a route then a subscription", []string{"router-one", "sub-one"}, 1},
		{"an API key then the built-in vertex", []string{"key-one", CredentialVertex}, 1},
		{"a crossing behind two members of one half", []string{"sub-one", "sub-two", "router-one"}, 2},
		{"a crossing, and then the half it started on", []string{"router-one", "sub-one", "key-one"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := ValidatePoolPaths([]Pool{{Name: "mixed", Members: tc.members}}, poolCrossingMembers())
			if len(ps) != 1 {
				t.Fatalf("the pool %v is answered with %d problem(s), want one: %v", tc.members, len(ps), ps)
			}
			if want := fmt.Sprintf("pools.mixed.members[%d]", tc.at); ps[0].Path != want {
				t.Errorf("the refusal is at %q, want %q: the member the pool's own order crosses at", ps[0].Path, want)
			}
		})
	}
	// A pool this document does not define a member of is reported for that and
	// not also for a crossing: a name no layer places is not a half, and the
	// operator has one thing to fix, not two.
	undefined := []Pool{{Name: "mixed", Members: []string{"sub-one", "nowhere"}}}
	ps := ValidatePools(undefined, poolCrossingMembers())
	if len(ps) != 1 || !strings.Contains(ps[0].Message, "no credential of this name is defined") {
		t.Errorf("a pool naming an undefined member is answered with %v, want the member's own problem alone", ps)
	}
	if ps := ValidatePoolPaths(undefined, poolCrossingMembers()); len(ps) != 0 {
		t.Errorf("a pool naming an undefined member is refused a second time for a crossing: %v", ps)
	}
}

// TestTheCrossingRefusalHoldsAtTheDoorAndTheNodesRead is the staging this
// refusal ships under, and it is the test that keeps it deliberate. D1668's rule
// is held at two doors and at two only: the one a NEW pool is written through
// (ValidatePoolPaths, whose one caller is `fugaro accounts pool add`) and the
// read of a PUBLISHED node (ValidatePublishedPools, which projectpolicy's Decode
// runs), so a node that already carries a crossing is refused by the runner's
// Read, the credentials service's per-call re-read, `doctor` and `update` rather
// than served to a selector whose listed order it cannot honour. MC21's node-side
// step is what makes that read safe: the version-8 node the step migrates is
// split before Decode sees it, and a node below the carry's floor is refused
// with the update's own remedy.
//
// It is deliberately NOT a rule of ValidatePools, and this test is what fails if
// it becomes one. ValidatePools is the load of the INSTALLATION's own config —
// localcfg's validate asks it on every read, and every stage of `fugaro update`
// parses the file before it writes one — so a rule here would leave the
// installation unable to read the config its own update splits, which is the
// broken installation CLAUDE.md's HARD RULE forbids. What protects the config
// side is the door a new pool comes through, and MC21 converges it by splitting
// the pools on every write of that file (internal/cli's derivePathPools), so the
// file is never left carrying a crossing and no read ever has to refuse one.
func TestTheCrossingRefusalHoldsAtTheDoorAndTheNodesRead(t *testing.T) {
	pool := Pool{Name: "mixed", Members: []string{"sub-one", "router-one"}, Owner: "owner@example.com"}
	if ps := ValidatePools([]Pool{pool}, poolCrossingMembers()); len(ps) != 0 {
		t.Errorf("ValidatePools refuses a pool an earlier release wrote: %v — the installation's own load has to read the file `fugaro update` splits", ps)
	}
	for name, ps := range map[string][]Problem{
		"ValidatePoolPaths":      ValidatePoolPaths([]Pool{pool}, poolCrossingMembers()),
		"ValidatePublishedPools": ValidatePublishedPools([]Pool{pool}, poolCrossingMembers()),
	} {
		if len(ps) != 1 {
			t.Errorf("%s answers a crossing pool with %d problem(s), want the one refusal: %v", name, len(ps), ps)
		}
	}
}

// TestCrossPathPoolRefusedWithTheSplitSentence pins the ONE sentence D1668 rules
// the refusal is: it names the two members, the half each is served by, and the
// fix — two pools, one per half, and each craft's access naming the one its
// stages need. It is the sentence the two doors that hold the rule say — the
// write of a new pool and the read of a published node (MC21) — so the operator
// reads the same thing whichever door they met it at.
func TestCrossPathPoolRefusedWithTheSplitSentence(t *testing.T) {
	creds := []Credential{
		{Name: "openrouter-x", Type: CredentialTypeOpenRouter},
		{Name: "anthropic-y", Type: CredentialTypeAnthropic},
	}
	const want = "members anthropic-y and openrouter-x are served by different halves of a run (anthropic-y's type is served by the credentials service, openrouter-x's by the run's own gateway), and a pool's order can therefore mean nothing: name two pools, one per half, and let each craft's access name the one its stages need"
	for _, tc := range []struct {
		name    string
		members []string
	}{
		{"a route then a key", []string{"openrouter-x", "anthropic-y"}},
		{"a key then a route", []string{"anthropic-y", "openrouter-x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := Pool{Name: "mixed", Members: tc.members, Owner: "owner@example.com"}
			ps := ValidatePoolPaths([]Pool{pool}, creds)
			if len(ps) != 1 {
				t.Fatalf("the pool %v is answered with %d problem(s), want one: %v", tc.members, len(ps), ps)
			}
			if ps[0].Message != want {
				t.Errorf("the refusal is\n%s\nwant\n%s", ps[0].Message, want)
			}
			if ps[0].Path != "pools.mixed.members[1]" {
				t.Errorf("the refusal is at %q, want pools.mixed.members[1]", ps[0].Path)
			}
		})
	}
}

// TestPoolOfOnePathStillAdmitsEveryMemberOfIt is the other half of the rule, and
// the reason it is about the PATH rather than about a type: a subscription and
// an API key are one path — the credentials service, which selects between them
// by tier (D154, D1626) — and a route and the built-in vertex are the other, so
// every member of a half may be listed, however many, in whatever order.
func TestPoolOfOnePathStillAdmitsEveryMemberOfIt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		members []string
	}{
		{"a subscription and an API key", []string{"sub-one", "key-one"}},
		{"two subscriptions and the key beside them", []string{"sub-one", "sub-two", "key-one"}},
		{"the key first and a subscription after it", []string{"key-one", "sub-one"}},
		{"a route and the built-in vertex", []string{"router-one", CredentialVertex}},
		{"the built-in alone", []string{CredentialVertex}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pools := []Pool{{Name: "claude", Members: tc.members}}
			if ps := ValidatePools(pools, poolCrossingMembers()); len(ps) != 0 {
				t.Errorf("the pool %v is refused: %v", tc.members, ps)
			}
			if ps := ValidatePoolPaths(pools, poolCrossingMembers()); len(ps) != 0 {
				t.Errorf("the pool %v is refused where it would be written: %v", tc.members, ps)
			}
		})
	}
}
