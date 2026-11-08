package craft

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// specs is the spec's own example (docs/model-crafts/spec.md §2–3): the coder
// is DeepSeek through OpenRouter, the senior review is Opus on a pool of Claude
// accounts with the Anthropic key as its last member, the first-line review
// goes out on the key, and the security review changes only its model and keeps
// the senior review's pool. The repository's file writes the two crafts that
// select, the project's answer writes the two the spec makes required, and the
// credentials and pools live with the project's answer.
func specs() []Layer {
	return []Layer{
		{
			Name: config.SourceRepo,
			Crafts: config.Crafts{
				Review:         config.Craft{Access: "anthropic-key"},
				SecurityReview: config.Craft{Model: "claude-opus-4-1"},
			},
		},
		{
			Name: config.SourceProject,
			Crafts: config.Crafts{
				Coder:        config.Craft{Model: "deepseek/deepseek-v4.1-flash", Access: "openrouter-main"},
				SeniorReview: config.Craft{Model: "claude-opus-4", Access: "claude-pool"},
			},
			Credentials: []config.Credential{
				{Name: "openrouter-main", Type: config.CredentialTypeOpenRouter},
				{Name: "claude-1", Type: config.CredentialTypeClaudeOAuth},
				{Name: "claude-2", Type: config.CredentialTypeClaudeOAuth},
				{Name: "anthropic-key", Type: config.CredentialTypeAnthropic},
			},
			Pools: []config.Pool{
				{Name: "claude-pool", Members: []string{"claude-1", "claude-2", "anthropic-key"}},
			},
		},
	}
}

// TestResolveTheSpecsExample: the four crafts, each half answered on its own.
// A craft that sets one half keeps the other half it follows — the review craft
// changes only the access and keeps the coder's model, the security review
// changes only the model and keeps the senior review's pool — and each half
// names where it came from: the layer that wrote it, or the craft it was
// inherited from (spec §3, F16).
func TestResolveTheSpecsExample(t *testing.T) {
	got, ps := Resolve(specs(), false)
	if len(ps) > 0 {
		t.Fatalf("the spec's own example has problems: %v", ps)
	}
	for _, tc := range []struct {
		craft string
		want  Effective
	}{
		{
			craft: config.CraftCoder,
			want: Effective{
				Model: "deepseek/deepseek-v4.1-flash", ModelFrom: config.SourceProject,
				Access: "openrouter-main", AccessFrom: config.SourceProject,
			},
		},
		{
			// The first-line review keeps the coder's model and goes out on
			// its own access: the halves part.
			craft: config.CraftReview,
			want: Effective{
				Model: "deepseek/deepseek-v4.1-flash", ModelFrom: InheritedFrom(config.CraftCoder),
				Access: "anthropic-key", AccessFrom: config.SourceRepo,
			},
		},
		{
			// Two Claude OAuth members, so the pool is inert while the
			// subscription-spread switch is off (D1622).
			craft: config.CraftSeniorReview,
			want: Effective{
				Model: "claude-opus-4", ModelFrom: config.SourceProject,
				Access: "claude-pool", AccessFrom: config.SourceProject, Inert: true,
			},
		},
		{
			// The spec's own example of the halves parting the other way: the
			// model is the repository's, the pool the senior review's.
			craft: config.CraftSecurityReview,
			want: Effective{
				Model: "claude-opus-4-1", ModelFrom: config.SourceRepo,
				Access: "claude-pool", AccessFrom: InheritedFrom(config.CraftSeniorReview), Inert: true,
			},
		},
	} {
		if got[tc.craft] != tc.want {
			t.Errorf("%s resolves to %+v, want %+v", tc.craft, got[tc.craft], tc.want)
		}
	}
}

// TestResolveProvenanceNamesTheLayer: every half says which layer set it, and
// the narrowest layer that sets one wins it — the checkout's file, then the
// installation's own config, then the project's answer, which is the order
// config resolves a fugaro.yaml key in (config.EffectiveBaseBranch). The two
// halves are taken from their own layers: a model the repository writes and an
// access the installation config writes are one craft's answer.
func TestResolveProvenanceNamesTheLayer(t *testing.T) {
	got, ps := Resolve([]Layer{
		{Name: config.SourceRepo, Crafts: config.Crafts{Coder: config.Craft{Model: "deepseek-v3"}}},
		{
			Name:        SourceLocal,
			Crafts:      config.Crafts{Coder: config.Craft{Model: "opus-4", Access: "claude-1"}},
			Credentials: []config.Credential{{Name: "claude-1", Type: config.CredentialTypeClaudeOAuth}},
		},
		{
			Name:        config.SourceProject,
			Crafts:      config.Crafts{Coder: config.Craft{Model: "sonnet-5", Access: "claude-2"}},
			Credentials: []config.Credential{{Name: "claude-2", Type: config.CredentialTypeClaudeOAuth}},
		},
	}, false)
	if len(ps) > 0 {
		t.Fatalf("every access names a credential a layer holds: %v", ps)
	}
	want := Effective{
		// The repository's model wins the project's and the installation's.
		Model: "deepseek-v3", ModelFrom: config.SourceRepo,
		// The access the installation config writes wins the project's.
		Access: "claude-1", AccessFrom: SourceLocal,
	}
	if got[config.CraftCoder] != want {
		t.Errorf("the coder resolves to %+v, want %+v", got[config.CraftCoder], want)
	}
}

// TestResolveLayerNames: the strings a caller prints are the ones config's own
// resolution already answers for the repository and the project (a reader who
// sees "project" in `fugaro craft ls` and in `fugaro config show` is reading
// one vocabulary), this package's name for the installation's config — which is
// no fugaro.yaml layer — and the two shapes of an inherited half, which is
// F16's own wording.
func TestResolveLayerNames(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{SourceRepo, "repo"},
		{SourceProject, "project"},
		{SourceDefault, "default"},
		{SourceLocal, "local"},
		{InheritedFrom(config.CraftCoder), "inherited from coder"},
	} {
		if tc.got != tc.want {
			t.Errorf("a layer is named %q, want %q", tc.got, tc.want)
		}
	}
}

// TestResolveInheritsWithoutASetting: a craft turned on without settings of its
// own is the craft it follows, both halves (spec §3).
func TestResolveInheritsWithoutASetting(t *testing.T) {
	got, ps := Resolve([]Layer{{
		Name:        config.SourceProject,
		Crafts:      config.Crafts{SeniorReview: config.Craft{Model: "claude-opus-4", Access: "claude-1"}},
		Credentials: []config.Credential{{Name: "claude-1", Type: config.CredentialTypeClaudeOAuth}},
	}}, false)
	if len(ps) > 0 {
		t.Fatalf("the senior review's access names a credential the same layer holds: %v", ps)
	}
	want := Effective{
		Model: "claude-opus-4", ModelFrom: InheritedFrom(config.CraftSeniorReview),
		Access: "claude-1", AccessFrom: InheritedFrom(config.CraftSeniorReview),
	}
	if got[config.CraftSecurityReview] != want {
		t.Errorf("a security review with no settings resolves to %+v, want the senior review's %+v",
			got[config.CraftSecurityReview], want)
	}
}

// TestResolveUnsetCraftTakesTheDefaultCredential: a craft no layer sets
// anywhere resolves to the project's default credential, and says so — not to
// a credential called "default", which is a name nothing may define (D1624).
// Its model stays empty: naming one no layer wrote would hide that no run can
// serve it.
func TestResolveUnsetCraftTakesTheDefaultCredential(t *testing.T) {
	got, ps := Resolve(nil, false)
	if len(ps) > 0 {
		t.Fatalf("no layers have no unknown access: %v", ps)
	}
	want := Effective{
		ModelFrom:  config.SourceDefault,
		Access:     config.DefaultCredential,
		AccessFrom: config.SourceDefault,
	}
	if got[config.CraftCoder] != want {
		t.Errorf("the coder of a project with no crafts resolves to %+v, want %+v", got[config.CraftCoder], want)
	}
	// The review inherits the coder's access, default and all: the craft it
	// follows is where the answer came from, whichever layer finally wrote it.
	want.AccessFrom = InheritedFrom(config.CraftCoder)
	if got[config.CraftReview] != want {
		t.Errorf("the review craft of a project with no crafts resolves to %+v, want %+v", got[config.CraftReview], want)
	}
	if got[config.CraftSecurityReview].AccessFrom != InheritedFrom(config.CraftSeniorReview) {
		t.Errorf("the security review inherits %q, want the senior review's access",
			got[config.CraftSecurityReview].AccessFrom)
	}
}

// TestResolveInertPool: a pool of two or more Claude OAuth subscription
// credentials is inert while the project's subscription-spread switch is off
// and serves as soon as the owner turns it on (D1622, D1636). One OAuth member
// beside other kinds is not such a pool — the rule for skipping it inside the
// pool the run walks is the run's own (D1626) — and neither is a lone OAuth
// credential, an API key or the built-in vertex.
func TestResolveInertPool(t *testing.T) {
	layers := []Layer{{
		Name: config.SourceProject,
		Crafts: config.Crafts{
			Coder:        config.Craft{Access: "one-oauth"},
			Review:       config.Craft{Access: "mixed"},
			SeniorReview: config.Craft{Access: "two-oauth"},
		},
		Credentials: []config.Credential{
			{Name: "claude-1", Type: config.CredentialTypeClaudeOAuth},
			{Name: "claude-2", Type: config.CredentialTypeClaudeOAuth},
			{Name: "claude-3", Type: config.CredentialTypeClaudeOAuth},
			{Name: "anthropic-key", Type: config.CredentialTypeAnthropic},
			{Name: "openrouter-main", Type: config.CredentialTypeOpenRouter},
		},
		Pools: []config.Pool{
			{Name: "one-oauth", Members: []string{"claude-1", "anthropic-key"}},
			{Name: "two-oauth", Members: []string{"claude-1", "claude-2", "anthropic-key"}},
			{Name: "mixed", Members: []string{"claude-1", "claude-2", "claude-3"}},
		},
	}}
	got, ps := Resolve(layers, false)
	if len(ps) > 0 {
		t.Fatalf("every access is defined: %v", ps)
	}
	for _, tc := range []struct {
		craft string
		inert bool
		why   string
	}{
		{config.CraftCoder, false, "one OAuth member beside the key is not a pool of two"},
		{config.CraftReview, true, "three OAuth members and nothing else"},
		{config.CraftSeniorReview, true, "two OAuth members beside the key"},
		{config.CraftSecurityReview, true, "it inherits the senior review's pool"},
	} {
		if got[tc.craft].Inert != tc.inert {
			t.Errorf("%s: Inert is %v, want %v (%s)", tc.craft, got[tc.craft].Inert, tc.inert, tc.why)
		}
	}
	// A lone Claude OAuth credential is what an installation has today, and the
	// built-in vertex is no pool at all: neither is inert (D1624).
	if inert("claude-1", layers, false) || inert(config.CredentialVertex, layers, false) {
		t.Error("a single credential is inert: only a pool of two or more subscriptions is")
	}
	// The switch on: the same pools serve, so nothing is inert.
	got, _ = Resolve(layers, true)
	for _, name := range config.CraftNames {
		if got[name].Inert {
			t.Errorf("%s is inert with the subscription-spread switch on: %+v", name, got[name])
		}
	}
}

// TestResolveUnknownAccessRefused: an access that names no credential and no
// pool any layer holds is a problem of the resolver's — a repository's file
// naming what the owner created in another layer is the ordinary case (D1628),
// so whether a name exists is answered once the layers are together — and it is
// reported once, on the craft that wrote it, not again on each craft that
// inherits it. The built-in vertex exists without any layer writing it.
func TestResolveUnknownAccessRefused(t *testing.T) {
	layers := []Layer{{
		Name:   config.SourceRepo,
		Crafts: config.Crafts{Coder: config.Craft{Access: "claude-9"}},
	}}
	got, ps := Resolve(layers, false)
	if len(ps) != 1 {
		t.Fatalf("got %d problems, want one: %v", len(ps), ps)
	}
	if ps[0].Path != "agent.crafts.coder.access" {
		t.Errorf("the problem is at %q, want agent.crafts.coder.access", ps[0].Path)
	}
	if !strings.Contains(ps[0].Message, "no credential or pool") {
		t.Errorf("the problem does not say the name resolves to nothing: %s", ps[0].Message)
	}
	// The craft that wrote it keeps the name it wrote and the layer it wrote
	// it in: a reader has to see the misconfiguration, not a silent fallback
	// to a credential the file never named.
	want := Effective{ModelFrom: config.SourceDefault, Access: "claude-9", AccessFrom: config.SourceRepo}
	if got[config.CraftCoder] != want {
		t.Errorf("the coder resolves to %+v, want the name as written: %+v", got[config.CraftCoder], want)
	}

	// The built-in vertex is defined with no layer at all, so it is no
	// problem (D1624); an empty access is the default credential, not an
	// unknown one.
	_, ps = Resolve([]Layer{{Name: config.SourceRepo, Crafts: config.Crafts{Coder: config.Craft{Access: config.CredentialVertex}}}}, false)
	if len(ps) > 0 {
		t.Errorf("the built-in vertex is refused: %v", ps)
	}
}

// TestResolveWalksConfigsOwnChain: the craft a craft follows is config's own
// rule (config.CraftParent, Crafts.Inherited), not a second copy of it here, so
// the resolver and the keys it reads cannot disagree about which craft a half
// comes from.
func TestResolveWalksConfigsOwnChain(t *testing.T) {
	for _, parent := range []string{config.CraftCoder, config.CraftSeniorReview} {
		for _, name := range config.CraftNames {
			if name == parent {
				continue // a craft inherits from the craft it follows, never from itself
			}
			// A block that sets the parent's model and nothing else: the craft
			// that follows the parent takes exactly that model, and no other
			// craft does.
			c := config.Crafts{}
			switch parent {
			case config.CraftCoder:
				c.Coder = config.Craft{Model: "parent-model"}
			case config.CraftSeniorReview:
				c.SeniorReview = config.Craft{Model: "parent-model"}
			}
			follows := config.CraftParent(name) == parent
			inherits := c.Inherited(name).Model == "parent-model"
			if inherits != follows {
				t.Errorf("%s: config.CraftParent says it follows %q (%v) and Inherited says it inherits from it (%v)",
					name, parent, follows, inherits)
			}
		}
	}
}
