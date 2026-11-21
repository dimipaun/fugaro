package credsvc

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestWorkspaceBundleIsOneSecretWithOneGrant: Q6's shape, applied to the
// workspace. A workspace's model credentials live in ONE Secret Manager
// container, read with ONE `secretmanager.secretAccessor` grant, and the pool
// the service offers is read out of that one document: a per-credential container
// would multiply the grants this deployment holds (each a full read of a
// credential that pays the workspace's money) and would make "which credentials
// does the workspace hold" a secret listing rather than a document an owner
// reads.
//
// Three claims, and each is a property of the code rather than of a comment:
// the container's ID is the one logical secret (`creds-bundle`) for the
// workspace's own project; every shared credential comes out of ONE parsed
// document; and the pool's answer carries names and tiers and never a value.
func TestWorkspaceBundleIsOneSecretWithOneGrant(t *testing.T) {
	// One container. The workspace's own installation IS a Fugaro project
	// named after the workspace, so the container an installation named
	// `workspace-alpha` writes and the one a workspace named `workspace-alpha`
	// serves from are one container — and it is derived from the one logical
	// name, not chosen per credential.
	if got, want := WorkspaceBundleSecretID(wsWorkspace), gcp.CredsBundleSecretID(wsWorkspace); got != want {
		t.Errorf("WorkspaceBundleSecretID(%q) = %q, want the one bundle container %q", wsWorkspace, got, want)
	}
	if got, want := WorkspaceBundleSecretID(wsWorkspace), BundleSecretID(wsWorkspace); got != want {
		t.Errorf("WorkspaceBundleSecretID(%q) = %q and BundleSecretID(%q) = %q: the workspace's own project's bundle is the workspace's bundle", wsWorkspace, got, wsWorkspace, want)
	}

	// One document, and the pool read out of it. The document holds three
	// model credentials and the node declares two of them shared; the third is
	// the state of every credential an owner puts in the workspace's bundle
	// before anyone is on its allow-list.
	accountKey := config.ClaudeAccountSecret("team")
	accountName, kind, tier, ok := CredentialKey(accountKey)
	if !ok || kind != KindModel || tier != creds.TierA {
		t.Fatalf("CredentialKey(%q) = (%q, %v, %v, %v), want a tier-A model credential", accountKey, accountName, kind, tier, ok)
	}
	unsharedKey := config.ClaudeAccountSecret("retired")
	unsharedName, _, _, ok := CredentialKey(unsharedKey)
	if !ok {
		t.Fatalf("CredentialKey(%q) refused a name of its own", unsharedKey)
	}
	doc := accountKey + ": " + fakeCred(t, "team") + "\n" +
		config.AnthropicSecret + ": " + fakeCred(t, "paid") + "\n" +
		unsharedKey + ": " + fakeCred(t, "retired") + "\n"
	b, err := ParseBundle([]byte(doc))
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	node := &projectpolicy.WorkspaceCredentials{
		Name: wsWorkspace,
		Shared: []projectpolicy.WorkspaceShared{
			{Name: accountName, Projects: []string{wsMemberA, wsMemberB}},
			{Name: config.AnthropicSecret, Projects: []string{wsMemberA}},
			// A credential the owner declared shared and the bundle does not
			// hold: there is no value to attach, so it is not offered.
			{Name: "claude-oauth-token-gone", Projects: []string{wsMemberA}},
		},
	}
	got := WorkspaceShared(b, node)
	want := []creds.Shared{
		{Credential: creds.Credential{Name: accountName, Tier: creds.TierA}, Projects: []string{wsMemberA, wsMemberB}},
		{Credential: creds.Credential{Name: config.AnthropicSecret, Tier: creds.TierB}, Projects: []string{wsMemberA}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WorkspaceShared =\n%+v\nwant\n%+v", got, want)
	}
	// The bundle's undeclared credential is offered to nobody: no projects, so
	// no member, which is the direction that matters — a credential read as
	// "shared with everyone" by omission is D1298's refused option (B).
	for _, s := range got {
		if s.Name == unsharedName {
			t.Errorf("WorkspaceShared offered %q, which the owner's node does not declare shared: an undeclared credential is offered to nobody", unsharedName)
		}
	}
	if len(got) != 2 {
		t.Errorf("WorkspaceShared = %d credentials, want the two the owner declared and the bundle holds", len(got))
	}

	// No value, anywhere in the answer: the pool is spoken of in names and
	// tiers, and the whole answer rendered as text carries no credential.
	rendered := fmt.Sprint(got)
	for _, e := range b.Entries() {
		if v := e.Value(); v != "" && strings.Contains(rendered, v) {
			t.Fatal("the pool's answer renders a credential's value: rule 2 is that no run, no CLI and no person is handed one")
		}
	}
	fields := reflect.TypeOf(creds.Shared{})
	for i := 0; i < fields.NumField(); i++ {
		if strings.Contains(strings.ToLower(fields.Field(i).Name), "value") {
			t.Errorf("creds.Shared carries a %s field", fields.Field(i).Name)
		}
	}

	// A workspace that published no node and a deployment that holds no
	// document each have no pool — not a pool of everything.
	if WorkspaceShared(b, nil) != nil {
		t.Error("WorkspaceShared with no node = a pool, want none: a workspace that declared nothing shares nothing")
	}
	if WorkspaceShared(nil, node) != nil {
		t.Error("WorkspaceShared with no bundle = a pool, want none: a name with no value behind it is not a credential")
	}
}

// TestThePoolCarriesForgeEntries: the pool is every kind of credential the
// service serves and not the model ones alone (D2978, D2984, F15).
//
// A workspace's bundle is one container holding every kind its runs are served
// from — the model credentials, the GitHub App's key and the Bitbucket tokens —
// and the owner's node declares which of them is shared, to whom, and, for a
// forge credential, which repositories it reaches (U2, C3's `covers`). So the
// pool a member's call is offered has to carry a forge entry with its kind and
// that coverage, and a forge entry the bundle does not hold is skipped exactly
// as a model one is: there is no value to attach, so it is not offered.
func TestThePoolCarriesForgeEntries(t *testing.T) {
	legacy := config.BitbucketSecret
	named := config.BitbucketTokenKey("acme")
	gone := config.BitbucketTokenKey("gone")
	if named == "" || gone == "" {
		t.Fatalf("config.BitbucketTokenKey refused a name of its own family: %q, %q", named, gone)
	}
	doc := config.GitHubAppKeySecret + ": " + fakeCred(t, "appkey") + "\n" +
		legacy + ": " + fakeCred(t, "legacy") + "\n" +
		named + ": " + fakeCred(t, "acme") + "\n"
	b, err := ParseBundle([]byte(doc))
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	node := &projectpolicy.WorkspaceCredentials{
		Name: wsWorkspace,
		// The node's entries are in name order, which is what Validate asks of
		// them: the legacy key, the family's two members, then the App key.
		Shared: []projectpolicy.WorkspaceShared{
			{Name: legacy, Kind: gitprov.KindBitbucket, Projects: []string{wsMemberA, wsMemberB}, Covers: []string{"bitbucket/acme/app"}},
			{Name: named, Kind: gitprov.KindBitbucket, Projects: []string{wsMemberA}, Covers: []string{"bitbucket/acme/app", "bitbucket/acme/other"}},
			// A credential the owner declared shared and the bundle does not
			// hold: no value to attach, so it is not offered.
			{Name: gone, Kind: gitprov.KindBitbucket, Projects: []string{wsMemberA}, Covers: []string{"bitbucket/acme/app"}},
			{Name: config.GitHubAppKeySecret, Kind: gitprov.KindGitHub, Projects: []string{wsMemberB}, Covers: []string{"github/acme/app"}},
		},
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("the test's own node is not one C3 accepts: %v", err)
	}
	got := WorkspaceShared(b, node)

	// Every entry the bundle holds, in the node's order, each carrying the kind
	// it is and the coverage the owner declared — and no tier, because a forge
	// credential is spent nowhere (Entry's own rule).
	want := []creds.Shared{
		{Credential: creds.Credential{Name: legacy}, Projects: []string{wsMemberA, wsMemberB}, Kind: gitprov.KindBitbucket, Covers: []string{"bitbucket/acme/app"}},
		{Credential: creds.Credential{Name: named}, Projects: []string{wsMemberA}, Kind: gitprov.KindBitbucket, Covers: []string{"bitbucket/acme/app", "bitbucket/acme/other"}},
		{Credential: creds.Credential{Name: config.GitHubAppKeySecret}, Projects: []string{wsMemberB}, Kind: gitprov.KindGitHub, Covers: []string{"github/acme/app"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WorkspaceShared =\n%+v\nwant\n%+v\nthe pool carries every kind of credential the bundle holds and the owner shared, with the kind and the coverage the node declares, and a forge entry the bundle does not hold is skipped as a model one is", got, want)
	}
	for _, s := range got {
		if s.Name == gone {
			t.Errorf("WorkspaceShared offered %q, which the bundle does not hold: a name with no value behind it is not a credential", gone)
		}
	}

	// A node entry whose kind and whose name disagree names no credential this
	// bundle holds: the App key's name under a Bitbucket kind is a skip and
	// never the other forge's credential.
	confused := &projectpolicy.WorkspaceCredentials{
		Name:   wsWorkspace,
		Shared: []projectpolicy.WorkspaceShared{{Name: config.GitHubAppKeySecret, Kind: gitprov.KindBitbucket, Projects: []string{wsMemberA}, Covers: []string{"bitbucket/acme/app"}}},
	}
	if got := WorkspaceShared(b, confused); len(got) != 0 {
		t.Errorf("WorkspaceShared offered %+v for an entry whose kind and name disagree: the kind is what tells the service which route may serve the entry (C3's rule 4), so the two have to name one credential", got)
	}

	// No value, anywhere in the answer: a forge credential is a name, a kind
	// and a coverage, and its token is in the container alone.
	rendered := fmt.Sprint(got)
	for _, e := range b.Entries() {
		if v := e.Value(); v != "" && strings.Contains(rendered, v) {
			t.Fatal("the pool's answer renders a credential's value: rule 2 is that no run, no CLI and no person is handed one")
		}
	}
}

// TestASharedProviderKeyIsOfferedToItsMembers is WC1's pool half (D2978, F7, U7):
// a provider key the workspace's bundle holds is offered to the members its
// allow-list names, and the pool carries it as the model entry a call through one
// is.
//
// Both shapes the bundle holds a provider's key in are read, and the two arms
// are what tells them from the shape that is NOT offered: the project's own
// (KindProvider, config.ProviderKeySecret), which is deliberately not in the
// model index because an account may legally be named after it, and one
// repository's named entry for a provider (KindRepoCredential, keyed by its
// document key), which credsvc.CredentialKey answers as that kind and NOT as
// KindProvider — U7's question, settled in `fugaro workspace share` and read back
// here. What a coding caller's own list (Credentials) holds is unchanged: neither
// is in it, and a tier-C entry there would refuse that caller's whole call.
//
// A named entry whose credential half is the repository's OWN Anthropic key is
// skipped, exactly as the write refuses that name: it is one repository's by
// construction (plan §10), and a member's runs are other repositories'.
func TestASharedProviderKeyIsOfferedToItsMembers(t *testing.T) {
	projectKey := repoEntryKeyFor(t, "github", "acme/one", "openrouter")
	ownKey := repoEntryKeyFor(t, "github", "acme/one", config.AnthropicKeyAccount)
	if kind, tier, _ := kindOf(t, projectKey); kind != KindRepoCredential || tier != creds.TierC {
		t.Fatalf("the test's named provider entry is a %v at tier %v, want the family's provider half: this test's claim is about a name the loader reads as one", kind, tier)
	}
	if kind, tier, _ := kindOf(t, ownKey); kind != KindRepoCredential || tier != creds.TierB {
		t.Fatalf("the test's repository-own entry is a %v at tier %v, want the family's paid half", kind, tier)
	}
	doc := append([]byte{}, providerKeyDoc(t, testProviderBase, providerAuthBearer)...)
	doc = append(doc, repoEntryDoc(t, projectKey, fakeCred(t, "openrouter"), testProviderBase, providerAuthBearer)...)
	doc = append(doc, repoEntryDoc(t, ownKey, fakeCred(t, "mine"), "https://api.anthropic.com", providerAuthXAPIKey)...)
	b, err := ParseBundle(doc)
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	if e, ok := b.ProviderKey(); !ok || e.Name != config.ProviderKeySecret || e.Tier != creds.TierC {
		t.Fatalf("the bundle's provider key is %+v (present %v), want %q at tier C", e, ok, config.ProviderKeySecret)
	}
	node := &projectpolicy.WorkspaceCredentials{
		Name: wsWorkspace,
		Shared: []projectpolicy.WorkspaceShared{
			{Name: config.ProviderKeySecret, Kind: projectpolicy.SharedKindModel, Projects: []string{wsMemberA, wsMemberB}},
			// The family's other half, offered by a node that a hand-written file
			// could carry: it is skipped here as well as at the write, and it is
			// named before the provider's entry because the node is in name order.
			{Name: ownKey, Kind: projectpolicy.SharedKindModel, Projects: []string{wsMemberA}},
			{Name: projectKey, Kind: projectpolicy.SharedKindModel, Projects: []string{wsMemberA}},
		},
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("the test's own node is not one C3 accepts: %v", err)
	}
	got := WorkspaceShared(b, node)
	want := []creds.Shared{
		{Credential: creds.Credential{Name: config.ProviderKeySecret, Tier: creds.TierC}, Projects: []string{wsMemberA, wsMemberB}},
		{Credential: creds.Credential{Name: projectKey, Tier: creds.TierC}, Projects: []string{wsMemberA}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WorkspaceShared =\n%+v\nwant\n%+v\na provider key is offered as a model entry, in both shapes the bundle holds one, and the repository's own key is not offered at all", got, want)
	}
	for _, s := range got {
		if s.Kind != "" || len(s.Covers) != 0 {
			t.Errorf("the pool's entry %+v names a kind or a coverage, and a model entry carries neither", s)
		}
	}

	// The members the allow-list names, and no others: the pool is the
	// workspace's side of D1298's intersection and the member's own policy the
	// other (AdmitShared). The member's policy is keyed by the member's own
	// names, so the leg is asked here with the pool's — what this test holds is
	// the workspace's side.
	admitted := func(string) bool { return true }
	cands := creds.SharedFor(wsMemberA, got, admitted)
	if len(cands) != 2 || cands[0].Name != config.ProviderKeySecret || cands[1].Name != projectKey {
		t.Fatalf("the candidates for %s = %+v, want the two provider keys: a member named by the allow-list is offered them", wsMemberA, cands)
	}
	for _, c := range cands {
		if c.Tier != creds.TierC {
			t.Errorf("the candidate %+v is not at tier C, and a provider's key is spent on the third-party route", c)
		}
	}
	if len(creds.SharedFor(wsMemberB, got, admitted)) != 1 {
		t.Errorf("the candidates for %s = %+v, want the project's provider key alone: the named entry's allow-list does not name this member", wsMemberB, creds.SharedFor(wsMemberB, got, admitted))
	}
	if len(creds.SharedFor(wsMemberA, got, func(string) bool { return false })) != 0 {
		t.Error("a member whose own policy admits nothing was still offered the pool: the workspace can only tighten a member")
	}

	// No value, anywhere in the answer, and no second reading of what a bundle
	// key is: the pool is names and tiers, and each provider key was reached
	// through the accessor that owns its kind.
	rendered := fmt.Sprint(got)
	for _, e := range b.Entries() {
		if v := e.Value(); v != "" && strings.Contains(rendered, v) {
			t.Fatal("the pool's answer renders a credential's value: rule 2 is that no run, no CLI and no person is handed one")
		}
	}
	// And a coding caller's own list is untouched by any of this: a tier-C entry
	// in it would refuse that caller's whole call (checkCandidates).
	for _, c := range b.Credentials() {
		if c.Tier == creds.TierC {
			t.Errorf("Credentials offers %+v, and a coding caller is offered no tier-C credential", c)
		}
	}
}

// kindOf is what credsvc.CredentialKey answers for a name, in the order this
// file reads it: the tests hold their own names to the reader that decides what
// a bundle key is, rather than to a kind written beside them.
func kindOf(t *testing.T, key string) (Kind, creds.Tier, bool) {
	t.Helper()
	_, kind, tier, ok := CredentialKey(key)
	return kind, tier, ok
}

// TestAClaudeModelThroughASharedProviderKeyWaits is WC1's selection half, and it
// is D3000's gate reached through the POOL: the credential a shared provider key
// offers a member's run is the one the selection refuses for a Claude model —
// the reserved tier C, whose route this release does not serve, waiting for Check
// 50 (D150, C3.6, R3).
//
// It is the existing rule and not a new one: checkCandidates drops a tier-C
// candidate for a Claude model and answers the sentence that names the check, and
// what this test adds is that the credential a workspace's pool hands a member is
// the one that rule is about. The non-Claude half of the same rule is
// internal/creds' own (TestANonClaudeProviderModelIsServed), and the offering's
// half — which named entries a call is offered — cascade_provider_test.go's.
func TestAClaudeModelThroughASharedProviderKeyWaits(t *testing.T) {
	doc := append([]byte{}, providerKeyDoc(t, testProviderBase, providerAuthBearer)...)
	doc = append(doc, repoEntryDoc(t, repoEntryKeyFor(t, "github", "acme/one", "openrouter"), fakeCred(t, "openrouter"), testProviderBase, providerAuthBearer)...)
	b, err := ParseBundle(doc)
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	node := &projectpolicy.WorkspaceCredentials{
		Name: wsWorkspace,
		Shared: []projectpolicy.WorkspaceShared{
			{Name: config.ProviderKeySecret, Kind: projectpolicy.SharedKindModel, Projects: []string{wsMemberA}},
		},
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("the test's own node is not one C3 accepts: %v", err)
	}
	cands := creds.SharedFor(wsMemberA, WorkspaceShared(b, node), func(string) bool { return true })
	if len(cands) != 1 || cands[0].Tier != creds.TierC {
		t.Fatalf("the member's candidates = %+v, want the shared provider key at tier C: this test needs the credential a member's call is offered", cands)
	}

	// The selection is the pool's own caller — the service's cascade builds the
	// same Selector for a call (cascade.go) — and MayServe is the judgement
	// without the placement: it writes nothing, so the state the test hands it is
	// the empty one newFakeStore answers.
	slug := slugFor(t, "github", "acme/one")
	sel := &creds.Selector{Store: newFakeStore(), Model: testModel}
	err = sel.MayServe(context.Background(), creds.Run{Slug: slug, ID: "run-wc1"}, cands[0])
	if err == nil {
		t.Fatalf("a Claude model through the shared provider key was served: %+v", cands[0])
	}
	for _, want := range []string{config.ProviderKeySecret, "tier C", "Check 50", "D150"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not say %q", err, want)
		}
	}

	// And the same credential for a model that is not Claude's is not refused by
	// this rule: the tie is the caller's (Selector.Provider), and a key with no
	// provider of its own answers the other sentence — which is the proof that
	// the refusal above is D3000's gate and not "no credential is offered".
	other := &creds.Selector{Store: newFakeStore(), Model: openRouterModel, Provider: "openrouter"}
	if err := other.MayServe(context.Background(), creds.Run{Slug: slug, ID: "run-wc2"}, cands[0]); err == nil ||
		strings.Contains(err.Error(), "Check 50") {
		t.Errorf("a non-Claude model through the shared provider key = %v, want the provider tie's own refusal and not Check 50's", err)
	}
}

// TestAModelEntryIsUnchanged: version 2's columns are a FORGE entry's, and a
// model entry is what it always was — a name, a tier and its allow-list (D2978,
// F15, and the acceptance's "a model entry is unchanged").
//
// The claim has two halves, and the second is the one that would break a member
// silently: the pool a call is offered now carries forge entries beside the
// model ones, and the list the model cascade is offered — SharedFor's — is
// still the model credentials and only them. A Bitbucket token in a model
// candidate list is a candidate the cascade cannot attach, so the pool growing
// a kind must not grow that list.
func TestAModelEntryIsUnchanged(t *testing.T) {
	accountKey := config.ClaudeAccountSecret("team")
	accountName, kind, tier, ok := CredentialKey(accountKey)
	if !ok || kind != KindModel {
		t.Fatalf("CredentialKey(%q) = (%q, %v, %v, %v), want a model credential", accountKey, accountName, kind, tier, ok)
	}
	doc := accountKey + ": " + fakeCred(t, "team") + "\n" +
		config.BitbucketSecret + ": " + fakeCred(t, "legacy") + "\n"
	b, err := ParseBundle([]byte(doc))
	if err != nil {
		t.Fatalf("ParseBundle: %v", err)
	}
	node := &projectpolicy.WorkspaceCredentials{
		Name: wsWorkspace,
		Shared: []projectpolicy.WorkspaceShared{
			{Name: config.BitbucketSecret, Kind: gitprov.KindBitbucket, Projects: []string{wsMemberA}, Covers: []string{"bitbucket/acme/app"}},
			{Name: accountName, Kind: projectpolicy.SharedKindModel, Projects: []string{wsMemberA}},
		},
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("the test's own node is not one C3 accepts: %v", err)
	}
	got := WorkspaceShared(b, node)
	if len(got) != 2 {
		t.Fatalf("WorkspaceShared = %+v, want the forge entry and the model one: the pool carries every kind the owner shared", got)
	}

	// The model entry, byte for byte: what it was before version 2's columns
	// existed.
	want := creds.Shared{Credential: creds.Credential{Name: accountName, Tier: tier}, Projects: []string{wsMemberA}}
	if !reflect.DeepEqual(got[1], want) {
		t.Fatalf("the pool's model entry = %+v, want %+v: a model entry carries its name, its tier and its allow-list and nothing of version 2's", got[1], want)
	}

	// The cascade's list, unchanged by the pool carrying a forge entry beside
	// the model one.
	candidates := creds.SharedFor(wsMemberA, got, func(string) bool { return true })
	if len(candidates) != 1 || candidates[0] != want.Credential {
		t.Fatalf("the model candidates = %+v, want just %+v: a forge entry is not a model candidate, so the pool carrying one must not put it in the cascade's list", candidates, want.Credential)
	}
}
