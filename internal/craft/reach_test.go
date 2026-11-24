package craft

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// The project the tests below declare their credentials and providers with: a
// Claude subscription (acme-oauth), an Anthropic key (acme-key) and the
// provider acme-router, which serves deepseek's models. admission names what
// the project admits for the repository the craft belongs to.
func reachProject(admission ...string) Reach {
	providers := map[string]config.ModelProvider{
		"acme-router": {Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer", Secret: "acme-router-api-key", Models: []string{"deepseek/*"}},
	}
	return Reach{
		Declared: []config.Credential{
			{Name: "acme-oauth", Type: config.CredentialTypeClaudeOAuth},
			{Name: "acme-key", Type: config.CredentialTypeAnthropic},
			{Name: "acme-router", Type: config.CredentialTypeOpenRouter},
		},
		Providers: providers,
		Admitted:  admission,
	}
}

// TestASubscriptionAndAKeyBothReachAClaudeModel: R2's first two types reach a
// Claude model, whichever of them a project declares — a subscription token and
// an Anthropic key are two ways of reaching the same model (D3060, spec §1) —
// and each of them alone is the answer an unset access gets.
func TestASubscriptionAndAKeyBothReachAClaudeModel(t *testing.T) {
	for _, cred := range []config.Credential{
		{Name: "acme-oauth", Type: config.CredentialTypeClaudeOAuth},
		{Name: "acme-key", Type: config.CredentialTypeAnthropic},
	} {
		r := reachProject(cred.Name)
		r.Craft = config.CraftCoder
		r.Model = "claude-sonnet-5"
		r.Declared = []config.Credential{cred}

		got, ps := ByReach(r)
		if got != cred.Name {
			t.Errorf("an unset access with %s (%s) declared answers %q, want %q", cred.Name, cred.Type, got, cred.Name)
		}
		if len(ps) != 0 {
			t.Errorf("%s reaches a Claude model and is admitted, so it has no problem: %v", cred.Name, ps)
		}
	}
}

// TestAProviderCredentialReachesOnlyItsProvidersModels: a provider credential
// reaches the models its own provider entry claims and nothing else (R2) — the
// entry of the same name (config.ProviderFor) and not another one's, and no
// Claude model, which goes direct to Anthropic.
func TestAProviderCredentialReachesOnlyItsProvidersModels(t *testing.T) {
	r := reachProject("acme-router")
	r.Craft = config.CraftCoder
	r.Declared = []config.Credential{{Name: "acme-router", Type: config.CredentialTypeOpenRouter}}

	r.Model = "deepseek/deepseek-v4.1-flash"
	if got, ps := ByReach(r); got != "acme-router" || len(ps) != 0 {
		t.Errorf("the model its provider claims answers %q with %v, want acme-router and no problem", got, ps)
	}

	// A model of the same shape that acme-router's own entry does not claim:
	// another provider's, which this credential may not be handed.
	for _, model := range []string{"claude-sonnet-5", "qwen/qwen3-max"} {
		r.Model = model
		got, ps := ByReach(r)
		if got != "" || len(ps) != 1 {
			t.Errorf("%s is not acme-router's, so an unset access answers %q with %d problems, want none and one", model, got, len(ps))
		}
	}

	// A provider entry of another name claiming the same models is no road for
	// this credential: a credential reaches what ITS provider claims.
	r.Providers = map[string]config.ModelProvider{
		"other-router": {Kind: config.ModelProviderKind, BaseURL: "https://openrouter.ai/api", Auth: "bearer", Secret: "other-api-key", Models: []string{"deepseek/*"}},
	}
	r.Model = "deepseek/deepseek-v4.1-flash"
	if got, ps := ByReach(r); got != "" || len(ps) != 1 {
		t.Errorf("another provider's entry answers %q with %d problems, want none and one", got, len(ps))
	}
}

// TestAnUnsetAccessIsTheFirstReachingCredentialOnly: an access nobody wrote is
// the first credential the project declares, in the order it declares them,
// that reaches the model AND admits the repository, and nothing after it
// (D3061 as amended). "Unset" is judged after the craft has followed the craft
// it inherits from (config.CraftParent): a craft that writes no access of its
// own takes the one its parent wrote, as written, and only where the whole
// chain wrote none is the project's order read.
func TestAnUnsetAccessIsTheFirstReachingCredentialOnly(t *testing.T) {
	r := reachProject("acme-oauth")
	r.Craft = config.CraftCoder
	r.Model = "claude-sonnet-5"
	r.Declared = []config.Credential{
		{Name: "acme-oauth", Type: config.CredentialTypeClaudeOAuth},
		{Name: "acme-key", Type: config.CredentialTypeAnthropic},
	}
	if got, ps := ByReach(r); got != "acme-oauth" || len(ps) != 0 {
		t.Errorf("the first declared that reaches answers %q with %v, want acme-oauth and no problem", got, ps)
	}

	// The order is the project's, not the types': with both admitted, the key
	// first answers the key, and the subscription is not consulted at all.
	r.Admitted = []string{"acme-oauth", "acme-key"}
	r.Declared = []config.Credential{
		{Name: "acme-key", Type: config.CredentialTypeAnthropic},
		{Name: "acme-oauth", Type: config.CredentialTypeClaudeOAuth},
	}
	if got, _ := ByReach(r); got != "acme-key" {
		t.Errorf("the project's order decides: got %q, want acme-key", got)
	}

	// A credential the repository is not admitted to is skipped, and the scan
	// goes on: the first one that reaches AND admits wins.
	r.Declared = []config.Credential{
		{Name: "acme-oauth", Type: config.CredentialTypeClaudeOAuth},
		{Name: "acme-key", Type: config.CredentialTypeAnthropic},
	}
	r.Admitted = []string{"acme-key"}
	if got, ps := ByReach(r); got != "acme-key" || len(ps) != 0 {
		t.Errorf("an unadmitted credential is skipped, so the answer is %q with %v, want acme-key and no problem", got, ps)
	}

	// A craft that writes no access takes its parent's (the merge judge follows
	// the coder), and the parent's access is answered as written rather than
	// judged by the project's order.
	r.Admitted = []string{"acme-oauth"}
	r.Crafts = config.Crafts{Coder: config.Craft{Access: "acme-key"}}
	r.Craft = config.CraftMergeJudge
	if got, ps := ByReach(r); got != "acme-key" || len(ps) != 0 {
		t.Errorf("an inherited access answers %q with %v, want acme-key and no problem", got, ps)
	}
}

// TestVertexIsNeverImplied: vertex is a craft's only where an access or a pool
// names it (D3061 as amended). An unset access never falls through to it, not
// even where the caller's declaration list carries it — and writing it, or
// naming the pool that lists it, is how a craft goes out on the project's own
// identity.
func TestVertexIsNeverImplied(t *testing.T) {
	r := reachProject()
	r.Craft = config.CraftCoder
	r.Model = "claude-sonnet-5"
	r.Declared = config.BuiltinCredentials()
	r.Admitted = []string{config.CredentialVertex}

	got, ps := ByReach(r)
	if got == config.CredentialVertex {
		t.Errorf("an unset access with vertex declared answers %q: vertex is never implied", got)
	}
	if got != "" || len(ps) != 1 {
		t.Errorf("nothing but vertex is declared, so the answer is %q with %d problems, want none and one", got, len(ps))
	}

	// Nothing declared at all: still no vertex.
	r.Declared = nil
	if got, ps := ByReach(r); got == config.CredentialVertex || len(ps) != 1 {
		t.Errorf("an empty declaration answers %q with %d problems, want no answer and one problem", got, len(ps))
	}

	// Written, it is answered as written, and judged like any other credential:
	// it reaches a Claude model and no provider's.
	r.Declared = []config.Credential{{Name: "acme-router", Type: config.CredentialTypeOpenRouter}}
	r.Crafts = config.Crafts{Coder: config.Craft{Access: config.CredentialVertex}}
	if got, ps := ByReach(r); got != config.CredentialVertex || len(ps) != 0 {
		t.Errorf("a written vertex answers %q with %v, want vertex and no problem", got, ps)
	}
	r.Model = "deepseek/deepseek-v4.1-flash"
	if _, ps := ByReach(r); len(ps) != 1 {
		t.Errorf("vertex cannot reach a provider's model, so it has one problem: %v", ps)
	}

	// A pool that names it is the other road: the access is answered as it was
	// written, and the pool's own members are the config's to judge (D1637).
	r.Model = "claude-sonnet-5"
	r.Crafts = config.Crafts{Coder: config.Craft{Access: "acme-pool"}}
	r.Pools = []config.Pool{{Name: "acme-pool", Members: []string{config.CredentialVertex}}}
	if got, ps := ByReach(r); got != "acme-pool" || len(ps) != 0 {
		t.Errorf("an access naming the pool answers %q with %v, want acme-pool and no problem", got, ps)
	}
}

// TestAnUnsetAccessNothingReachesIsRefusedWithTheFix: a craft that writes no
// access and that nothing the project declares reaches is refused, at the key
// where the fix is written, with the two ways out D3061 as amended names —
// declare a credential, or write access: vertex. The refusal is the
// declaration's own problem and not a fallback: a repository whose project
// declares nothing that reaches its model launches nothing until someone
// chooses (O7).
func TestAnUnsetAccessNothingReachesIsRefusedWithTheFix(t *testing.T) {
	r := reachProject("acme-router")
	r.Craft = config.CraftCoder
	r.Model = "claude-sonnet-5"
	r.Declared = []config.Credential{{Name: "acme-router", Type: config.CredentialTypeOpenRouter}}

	got, ps := ByReach(r)
	if got != "" {
		t.Errorf("nothing declared reaches a Claude model, so the answer is %q, want none", got)
	}
	if len(ps) != 1 {
		t.Fatalf("want one problem, got %v", ps)
	}
	if ps[0].Path != "agent.crafts.coder.access" {
		t.Errorf("the refusal is reported at %q, want agent.crafts.coder.access", ps[0].Path)
	}
	for _, fix := range []string{"declare a credential", "access: vertex"} {
		if !strings.Contains(ps[0].Message, fix) {
			t.Errorf("the refusal does not name the fix %q: %s", fix, ps[0].Message)
		}
	}

	// A credential that reaches the model and is NOT admitted for this
	// repository is no answer either: reach and admission are one question.
	r.Declared = []config.Credential{{Name: "acme-oauth", Type: config.CredentialTypeClaudeOAuth}}
	r.Admitted = nil
	if got, ps := ByReach(r); got != "" || len(ps) != 1 {
		t.Errorf("an unadmitted credential answers %q with %d problems, want none and one", got, len(ps))
	}

	// The path is the document's own: the installation's config writes its
	// crafts under `crafts` (Layer.KeyPrefix), and a reader sent to
	// agent.crafts would be sent to the wrong file.
	r.KeyPrefix = "crafts"
	if _, ps := ByReach(r); len(ps) != 1 || ps[0].Path != "crafts.coder.access" {
		t.Errorf("the installation config's key is reported as %v, want crafts.coder.access", ps)
	}
}

// TestAnAccessThatCannotReachItsModelIsAProblem: an access someone wrote is
// answered as written and refused where its credential cannot reach the craft's
// model — at the key that wrote it, which for an access a craft inherits is the
// key of the craft that wrote it (D3061 as amended, F38).
func TestAnAccessThatCannotReachItsModelIsAProblem(t *testing.T) {
	r := reachProject("acme-router")
	r.Craft = config.CraftMergeJudge
	r.Model = "claude-opus-4-1"
	r.Crafts = config.Crafts{Coder: config.Craft{Access: "acme-router"}}

	got, ps := ByReach(r)
	if got != "acme-router" {
		t.Errorf("the access is answered as written: got %q, want acme-router", got)
	}
	if len(ps) != 1 {
		t.Fatalf("acme-router serves providers' models, so inheriting it for a Claude model is one problem, got %v", ps)
	}
	if ps[0].Path != "agent.crafts.coder.access" {
		t.Errorf("the problem is reported at %q, want the key that wrote it, agent.crafts.coder.access", ps[0].Path)
	}

	// A written access that reaches its model has nothing to say, and so has
	// what cannot be judged here: a pool's members are held to serving the
	// model by the config's own rule at this key (D1637), and a name no
	// document defines is the resolver's problem (Resolve).
	for _, tc := range []struct {
		name   string
		access string
		pools  []config.Pool
	}{
		{"a credential that reaches the model", "acme-oauth", nil},
		{"the built-in vertex", config.CredentialVertex, nil},
		{"a pool", "acme-pool", []config.Pool{{Name: "acme-pool", Members: []string{"acme-oauth"}}}},
		{"a name that is neither", "acme-typo", nil},
	} {
		r.Crafts = config.Crafts{Coder: config.Craft{Access: tc.access}}
		r.Pools = tc.pools
		if got, ps := ByReach(r); got != tc.access || len(ps) != 0 {
			t.Errorf("%s answers %q with %v, want %q and no problem", tc.name, got, ps, tc.access)
		}
	}

	// A craft that names no model is not judged: its stages run the role slot
	// below it, and the pins there are what judge them (F17).
	r.Model = ""
	r.Crafts = config.Crafts{Coder: config.Craft{Access: "acme-router"}}
	if got, ps := ByReach(r); got != "acme-router" || len(ps) != 0 {
		t.Errorf("a craft with no model answers %q with %v, want its access as written and no problem", got, ps)
	}

	// An unset access is not refused either, and this is the ordinary case
	// rather than a corner one: agent.models.* stays the rungs a craft's model
	// is read from (D1662, D1666), so a project that wrote no craft model
	// resolves no model for any craft — and with no model there is nothing for
	// a credential to reach, so nothing is the answer (F17).
	r.Crafts = config.Crafts{}
	if got, ps := ByReach(r); got != "" || len(ps) != 0 {
		t.Errorf("an unset access on a craft with no model answers %q with %v, want no answer and no problem", got, ps)
	}
}
