package credsvc

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
)

// This file is the OFFERING's half of KS3 (F32, D3008, F42): which credentials a
// coding caller is offered in the first place. The selection's half — that a
// provider's key serves a model that is not Claude's and waits for Check 50 for
// one that is — is internal/creds' own (creds.Select, D3000), and the route
// that carries a provider's name is KS4's; what is proved here is that the list
// this service builds holds the entries the call's own policy and its own
// verified repository entitle it to:
//
//   - one named provider credential per provider the published policy names,
//     offered at tier C under EVERY auth this service serves and not only under
//     `api-key` (F32) — a provider's key is not a subscription and not the
//     project's own Anthropic key, so which auth a repository's Claude calls go
//     out on says nothing about it (D3007);
//   - the repository's OWN Anthropic entry where the bundle holds one, instead
//     of the project's, and never another repository's, whatever the pool the
//     scope answered happens to list (D3008, F42).
//
// Both are read out of the bundle through C1's accessor and never out of a
// field of this package's own: an entry the accessor does not hold is not a
// candidate at all, which is the answer a repository whose key was never copied
// must meet (ReadProviderKey, contract-provider.md).
//
// The accessor is a package variable that KS1 fills and the stub refuses
// through until then, so a test that wants the offering to see an entry swaps
// the answer for its own (withProviderKeys) and restores it: the alternative —
// writing the family into the bundle — is KS1's task and would make this file's
// claims about the bundle rather than about the offering.

// providerKey is one entry of C1's family as a test writes it. Only the slug
// and the credential's name are read by anything here; the address and the auth
// form are named so the entry is not a half-built one, and they are the route's
// and the copy's (KS4, KS12) rather than this file's claim.
func providerKey(slug, credential string) ProviderKeyEntry {
	return ProviderKeyEntry{Slug: slug, Credential: credential,
		BaseURL: "https://providers.example.com/v1", Auth: "bearer"}
}

// withProviderKeys answers C1's accessor out of the entries a test names,
// instead of out of the bundle: the package variable exists so that the sides
// written against the accessor's signature can run before KS1 teaches the
// bundle to hold a named entry, and a test that wants the offering to see one
// has to be the side that answers it.
//
// What answered before is restored on cleanup, because the variable is the
// package's and provider_wire_test.go's own case reads it.
func withProviderKeys(t *testing.T, entries ...ProviderKeyEntry) {
	t.Helper()
	held := make(map[string]map[string]ProviderKeyEntry, len(entries))
	for _, e := range entries {
		if held[e.Slug] == nil {
			held[e.Slug] = map[string]ProviderKeyEntry{}
		}
		held[e.Slug][e.Credential] = e
	}
	before := ReadProviderKey
	ReadProviderKey = func(_ *Bundle, slug, credential string) (ProviderKeyEntry, bool) {
		e, ok := held[slug][credential]
		return e, ok
	}
	t.Cleanup(func() { ReadProviderKey = before })
}

// familyName is the name one repository's named credential is held under and
// offered as (C1's naming rule, RepoCredentialKey). It is computed here rather
// than written down, so a case that names an entry holds the naming rule to the
// tree's own and not to a literal this file could get wrong.
func familyName(t *testing.T, slug, credential string) string {
	t.Helper()
	name, err := RepoCredentialKey(slug, credential)
	if err != nil {
		t.Fatalf("RepoCredentialKey(%q, %q): %v; the test needs a name it can build a candidate from", slug, credential, err)
	}
	return name
}

// testPolicyDocProviders is the fixture's shared node with the project's
// `providers` block written out: the providers a run of this project may route
// a model to, whose keys are the credential half of C1's family (R2, KS3's
// offering reads this block and nothing else about a provider).
func testPolicyDocProviders(names ...string) string {
	return testPolicyDocProvidersWith(`"credentials": {"fallback": "auto"}`, names...)
}

// testPolicyDocProvidersProbeable is the same node with D142's window control
// written off, which is the probe fixture's own setting (probeSvc): with the
// control on, a backed-off credential's earliest usable time is the end of the
// window its exhaustion opened, so a selection answers a wait long enough to
// move the cascade on to the next tier instead of owing the probe the test is
// about.
func testPolicyDocProvidersProbeable(names ...string) string {
	return testPolicyDocProvidersWith(`"credentials": {"fallback": "auto", "stop_after_exhaustion_per_window": false}`, names...)
}

// testPolicyDocProvidersCraft is the same node with a crafts block: the coder
// craft goes out on the project's own Anthropic key, which is the only kind of
// name a craft CAN go out on — craftOffering builds a candidate for a pool's
// members and for a credential the bundle holds at tier A or B, and a tier-C
// name is refused there (an access that named a provider's key would refuse the
// caller's whole call) — so the crafts always narrow a call to names of the old
// vocabulary and never to a named entry of C1's family.
func testPolicyDocProvidersCraft(names ...string) string {
	return testPolicyDocProvidersWith(`"credentials": {"fallback": "auto"},
		"craft_credentials": [{"name": "`+config.AnthropicSecret+`", "type": "anthropic"}],
		"crafts": {"coder": {"access": "`+config.AnthropicSecret+`"}}`, names...)
}

func testPolicyDocProvidersWith(credentials string, names ...string) string {
	entries := make([]string, 0, len(names))
	for _, n := range names {
		// One model pattern per provider, because the policy refuses two
		// providers that both claim one model (validProviders): the fixture's
		// names are preferences, not models, and what the block is read for
		// here is the NAMES.
		entries = append(entries, `"`+n+`": {"kind": "anthropic-compat", "base_url": "https://`+n+`.example.com/api/v1", `+
			`"auth": "bearer", "secret": "`+n+`-secret", "models": ["`+n+`/*"]}`)
	}
	return `{
		"version": 9,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			"default": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}
		},
		` + credentials + `,
		"providers": {` + strings.Join(entries, ", ") + `}
	}`
}

// providerSvc is the fixture with a project whose policy names two providers
// and a bundle the accessor answers for one of them: the state every case below
// is about. One of the two is the point — an entry the accessor refuses is not
// a candidate, and that is a claim of the same walk.
//
// The repository's own slug is the fixture's, which is the slug the run
// identity resolves to, so the entry offered is the one built for the
// repository the service verified and never one a call named.
func providerSvc(t *testing.T) (*svc, string) {
	t.Helper()
	return providerSvcWith(t, testPolicyDocProviders("openrouter", "deepseek"))
}

// providerSvcWith is providerSvc over a chosen shared node, for the case whose
// policy differs in its credentials block alone.
func providerSvcWith(t *testing.T, doc string) (*svc, string) {
	t.Helper()
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.policy.shared = decodeTestPolicy(t, doc)
	})
	withProviderKeys(t, providerKey(s.slug, "openrouter"))
	return s, familyName(t, s.slug, "openrouter")
}

// TestAnOAuthRepositoryIsOfferedItsProviderCredential is F32: a named provider
// credential is offered the call under EVERY auth this service serves, and not
// only under `api-key`. A provider's key is a third party's and neither a
// subscription nor the project's own Anthropic key, so the auth a repository's
// Claude calls go out on does not decide whether its provider models can be
// served (D3007) — a repository on oauth that was offered no provider
// credential at all is exactly the case the plan's red step is about.
//
// The four cases are the four states the narrowing serves a list under: an
// oauth record, an api-key record, a cascade record and no record (which is the
// cascade too, and what an installation this release has not written still
// is). The account beside the oauth record is the one its own node admits.
func TestAnOAuthRepositoryIsOfferedItsProviderCredential(t *testing.T) {
	for _, tc := range []struct {
		what     string
		auth     string
		accounts []string
		mode     string
		account  string
		// kept is the credential the auth's own arm serves beside the provider
		// key, which is what tells "offered beside the cascade's own" apart from
		// "offered instead of it".
		kept []string
		// crafts is the same call in a project that publishes a crafts block,
		// which narrows the pool to the names the crafts go out on and so to
		// none of C1's family: the named entries join the list after that
		// narrowing or not at all (the offering's own rule).
		crafts bool
	}{
		{"an oauth repository", "oauth", []string{testAccount}, "oauth", testAccount, []string{testAccount}, false},
		{"an api-key repository", "api-key", nil, "api-key", "", []string{config.AnthropicSecret}, false},
		{"a cascade repository", config.AuthCascade, nil, "", "", []string{config.DefaultClaudeAccount}, false},
		{"a repository no update has recorded", "", nil, "", "", []string{config.DefaultClaudeAccount}, false},
		{"a crafts project's api-key repository", "api-key", nil, "api-key", "", []string{config.AnthropicSecret}, true},
		{"a crafts project's cascade repository", config.AuthCascade, nil, "", "", []string{config.AnthropicSecret}, true},
	} {
		t.Run(tc.what, func(t *testing.T) {
			s, want := providerSvc(t)
			if tc.crafts {
				s, want = providerSvcWith(t, testPolicyDocProvidersCraft("openrouter", "deepseek"))
			}
			if tc.auth != "" {
				s.authRecord(t, tc.auth, tc.accounts...)
			}
			cands, skipped, err := s.candidatesFor(t, tc.mode, tc.account)
			if err != nil {
				t.Fatalf("candidates: %v; want the call admitted: the provider key is neither a subscription nor the project's own key, and no auth this service serves takes it away (F32, D3007)", err)
			}
			c, ok := lookupCredential(cands, want)
			if !ok {
				t.Fatalf("the call is offered %v, and %q is not among them: a repository whose policy names the provider is offered its own entry under every auth the service serves", credentialNames(cands), want)
			}
			if c.Tier != creds.TierC {
				t.Errorf("the provider credential %q is offered at tier %s, want tier C: a provider's key is the third-party route and never the project's paid key or a subscription", want, c.Tier)
			}
			if c.Provider != "openrouter" {
				t.Errorf("the provider credential %q is offered with provider %q, want %q: the tie is what keeps a list of several providers' keys from serving the wrong one (creds.Credential.Provider)", want, c.Provider, "openrouter")
			}
			if got, ok := reportedNotAdmitted(t, skipped)[want]; ok {
				t.Errorf("the provider credential %q is reported as %q: the crafts go out on other names and this one is offered beside them, so nothing left it out (the entries join the list after the crafts narrow it, KS3)", want, got.Reason)
			}
			for _, name := range tc.kept {
				if _, ok := lookupCredential(cands, name); !ok {
					t.Errorf("the call is offered %v and not %q: the provider key is offered BESIDE what the repository's auth serves, never instead of it", credentialNames(cands), name)
				}
			}
			// The project's policy names a second provider and the bundle holds
			// nothing for it: the accessor's refusal is what makes it no
			// candidate, and the alternative — offering a name the service
			// would then fail to attach — would stop the call instead.
			other := familyName(t, s.slug, "deepseek")
			if _, ok := lookupCredential(cands, other); ok {
				t.Errorf("the call is offered %q, an entry the bundle does not hold: an entry the accessor refuses is not a candidate (ReadProviderKey)", other)
			}
		})
	}
}

// TestARepositoryIsNeverServedAnotherRepositorysAnthropicKey is D3008's own
// guard (F42), and it is the one claim about the family that no tier can make:
// two repositories' entries for their own Anthropic key are both tier B, and
// the project's own key is tier B beside them, so the LIST is where the three
// are told apart.
//
// The pool is the shape a bundle that lists every repository's entries answers.
// Two things are asserted of the same walk:
//
//   - this repository's own entry is offered and the project's is not: a
//     repository the copy wrote an entry for is served THAT one, and the
//     project's key is what a repository the copy wrote none for keeps.
//   - another repository's entry is not offered, under the cascade record as
//     well as under `api-key`: the cascade serves the whole list, so a guard
//     that only fired for a narrowing record would leave the entry served to
//     every repository the service does not narrow.
//
// Both are REPORTED as not admitted rather than dropped in silence, because the
// run's halt message reads the list it was served from and what was left out of
// it (keepCandidates).
func TestARepositoryIsNeverServedAnotherRepositorysAnthropicKey(t *testing.T) {
	for _, tc := range []struct{ what, auth, mode string }{
		{"an api-key repository", "api-key", "api-key"},
		{"a cascade repository", config.AuthCascade, ""},
		{"a repository no update has recorded", "", ""},
	} {
		t.Run(tc.what, func(t *testing.T) {
			s := newSvc(t)
			mine := familyName(t, s.slug, config.AnthropicKeyAccount)
			theirs := familyName(t, testSlug(t, testOtherRepoPath), config.AnthropicKeyAccount)
			if tc.auth != "" {
				s.authRecord(t, tc.auth)
			}
			pool := poolOfferingWithout{
				{Name: testAccount, Tier: creds.TierA},
				{Name: config.AnthropicSecret, Tier: creds.TierB},
				{Name: theirs, Tier: creds.TierB},
				{Name: mine, Tier: creds.TierB},
			}
			cands, skipped, err := s.candidatesFromPool(t, pool, s.identity(tc.mode, ""))
			if err != nil {
				t.Fatalf("candidates: %v", err)
			}
			if _, ok := lookupCredential(cands, mine); !ok {
				t.Fatalf("the call is offered %v, and this repository's own entry %q is not among them: a repository with an Anthropic entry of its own is served that one (D3008)", credentialNames(cands), mine)
			}
			if _, ok := lookupCredential(cands, theirs); ok {
				t.Errorf("the call is offered %q, another repository's Anthropic key: a call is served only its own repository's entries, and the family's name carries the repository it was entered for (R2, F42)", theirs)
			}
			if _, ok := lookupCredential(cands, config.AnthropicSecret); ok {
				t.Errorf("the call is offered %q beside this repository's own entry %q: one repository, one entry for its Anthropic key, and the repository's own is the one its runs are served (D3008)", config.AnthropicSecret, mine)
			}
			reported := reportedNotAdmitted(t, skipped)
			for _, name := range []string{theirs, config.AnthropicSecret} {
				got, ok := reported[name]
				if !ok {
					t.Errorf("the credential %q is neither offered nor reported as not admitted: the run's halt message reads both lists", name)
					continue
				}
				if got.Reason != ReasonNotAdmitted {
					t.Errorf("the credential %q is reported as %q, want %q", name, got.Reason, ReasonNotAdmitted)
				}
			}
		})
	}
}

// TestARepositoryWithItsOwnAnthropicEntryIsServedThatOne is the offering's half
// of D3008: the entry is read through C1's accessor for the repository the
// service verified, and it is offered even where the pool the scope answered
// does not hold it — which is every installation's pool until the bundle itself
// offers the family (KS1).
//
// Its two halves are the pair the rule is made of: a repository the copy wrote
// an entry for is served that entry and the project's key leaves its list, and
// a repository the copy wrote none for keeps the project's key exactly as it is
// served it today. The second half is what makes the first one a substitution
// rather than a new requirement: an installation that copies no key keeps
// working.
func TestARepositoryWithItsOwnAnthropicEntryIsServedThatOne(t *testing.T) {
	// The repository the copy has written an entry for. The accessor's answer
	// is swapped per case — the swap is a package variable — so the two are
	// subtests rather than two halves of one body.
	t.Run("an entry of its own", func(t *testing.T) {
		s := newSvc(t)
		withProviderKeys(t, providerKey(s.slug, config.AnthropicKeyAccount))
		s.authRecord(t, "api-key")
		own := familyName(t, s.slug, config.AnthropicKeyAccount)
		cands, skipped, err := s.candidatesFor(t, "api-key", "")
		if err != nil {
			t.Fatalf("candidates: %v", err)
		}
		if len(cands) != 1 || cands[0].Name != own || cands[0].Tier != creds.TierB {
			t.Fatalf("an api-key repository with an entry of its own is offered %v; want exactly %q at tier B, the entry the accessor holds for the repository the service verified", credentialNames(cands), own)
		}
		if got, ok := reportedNotAdmitted(t, skipped)[config.AnthropicSecret]; !ok || got.Reason != ReasonNotAdmitted {
			t.Errorf("the project's key is not reported as left out (%+v): the list and what was left out of it are the two things the halt message reads", got)
		}
	})

	// And the same repository in a project that publishes a crafts block. The
	// crafts go out on the project's key and can go out on nothing of C1's
	// family (craftOffering refuses a tier-C access), so a list built by
	// narrowing the pool to them holds neither this repository's own Anthropic
	// entry nor its provider's key — while the repository's runs are owed both:
	// its own key instead of the project's (D3008) and its provider's key
	// beside them under every auth (F32).
	t.Run("an entry of its own, in a project that publishes crafts", func(t *testing.T) {
		s := newSvcWith(t, func(s *svc, _ *Config) {
			s.policy.shared = decodeTestPolicy(t, testPolicyDocProvidersCraft("openrouter"))
		})
		withProviderKeys(t, providerKey(s.slug, config.AnthropicKeyAccount), providerKey(s.slug, "openrouter"))
		s.authRecord(t, "api-key")
		own := familyName(t, s.slug, config.AnthropicKeyAccount)
		provider := familyName(t, s.slug, "openrouter")
		cands, skipped, err := s.candidatesFor(t, "api-key", "")
		if err != nil {
			t.Fatalf("candidates: %v", err)
		}
		if _, ok := lookupCredential(cands, own); !ok {
			t.Fatalf("the call is offered %v, and this repository's own entry %q is not among them: a crafts project narrows its pool to the names its crafts go out on, and the repository's own entries join the list after that (D3008, F32)", credentialNames(cands), own)
		}
		if _, ok := lookupCredential(cands, config.AnthropicSecret); ok {
			t.Errorf("the call is offered %q beside this repository's own entry %q: one repository, one entry for its Anthropic key, crafts or no crafts (D3008)", config.AnthropicSecret, own)
		}
		if _, ok := lookupCredential(cands, provider); !ok {
			t.Errorf("the call is offered %v, and the repository's provider credential %q is not among them: the crafts narrow the pool and never the repository's own named entries, which join it after the narrowing (F32)", credentialNames(cands), provider)
		}
		if got, ok := reportedNotAdmitted(t, skipped)[provider]; ok {
			t.Errorf("the provider credential %q is reported as %q: the crafts go out on other names and this entry is offered beside them, so nothing left it out", provider, got.Reason)
		}
	})

	// And the repository the copy has written none for, which is every
	// installation today: the project's key, unchanged.
	t.Run("no entry of its own", func(t *testing.T) {
		s := newSvc(t)
		s.authRecord(t, "api-key")
		cands, _, err := s.candidatesFor(t, "api-key", "")
		if err != nil {
			t.Fatalf("candidates: %v", err)
		}
		if len(cands) != 1 || cands[0].Name != config.AnthropicSecret {
			t.Fatalf("an api-key repository with no entry of its own is offered %v; want exactly the project's key %q, which is what it is served today", credentialNames(cands), config.AnthropicSecret)
		}
	})
}

// candidatesFromPool runs the service's candidate walk over a pool of the
// test's own rather than over the scope's bundle (C1's OfferingReader seam): a
// list that holds every repository's entries is the shape the family's own
// guard exists for, and no bundle this test can build through the accessor
// produces one — ownNamedEntries only ever builds the verified repository's.
func (s *svc) candidatesFromPool(t *testing.T, pool poolOfferingWithout, id Identity) ([]creds.Credential, []Tried, error) {
	t.Helper()
	b, err := s.server.cfg.Bundles.Load(context.Background())
	if err != nil {
		t.Fatalf("loading the fixture's own bundle: %v", err)
	}
	sc := scopeWithout{Scope: installationScope{s: s.server}, offering: pool}
	cands, _, _, skipped, err := s.server.candidates(withScope(context.Background(), sc), id, testRepo, s.policy.shared, b)
	return cands, skipped, err
}

// identity is the caller the service verified for the fixture's repository: the
// slug the run resolves to, the class a model call is of, and the auth the call
// declared. It is written here rather than read off the scope because the walk
// takes it as an argument (candidates), and a test that wants another
// repository's entry in the list has to say which repository the call IS.
func (s *svc) identity(mode, account string) Identity {
	return Identity{Slug: s.slug, JobClass: ClassRepository, AuthMode: mode, AuthAccount: account}
}

// TestTheOfferingsProviderCredentialIsNotWhatAProbeRunsOn is where the two
// halves of KS3 meet: a repository whose published policy names a provider is
// offered its own named entry (ownNamedEntries), and a probe is a call this
// service makes on a credential the call was offered — carrying the call's OWN
// model in its body (probeCall). So the credential a probe may run on is the one
// that could serve the call, and no other (probeCandidates, creds.ProviderServes,
// D3000).
//
// The case is a Claude-model call, which is every call today: no route fills
// Selector.Model before KS4, and a call that named no model reads as a Claude
// one. The shared token is backed off with its own wait passed, so the selection
// owes a probe on it; the provider credential is planted as the more overdue of
// the two, so a probe list that did not ask the model's question answers it —
// and the probe built on the provider's entry carries a CLAUDE model to the
// third party, which is the one route Check 50 exists to measure. What is
// pinned here is that it is the shared token that is probed: the provider
// credential's mark is exactly what the exhaustion left, and the run's own call
// is served by the token the probe cleared.
func TestTheOfferingsProviderCredentialIsNotWhatAProbeRunsOn(t *testing.T) {
	s, provider := providerSvcWith(t, testPolicyDocProvidersProbeable("openrouter", "deepseek"))
	planted := s.backedOffDue(provider, s.now.Add(-time.Hour), creds.DefaultBackoffInitial, s.now.Add(-2*time.Minute))
	s.backedOffDue(config.DefaultClaudeAccount, s.now.Add(-time.Hour), creds.DefaultBackoffInitial, s.now.Add(-time.Minute))
	// The fixture's second shared token is held for an hour: it is the one the
	// call would otherwise be served by, and a mark whose own wait has not
	// passed is no probe's business (the first rule above).
	s.backedOffDue("alice", s.now.Add(-time.Hour), creds.DefaultBackoffInitial, s.now.Add(time.Hour))
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
	}

	resp, body := s.call(t, callBody("real-work"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call answered %d %s, want it served: the probe settles the shared token and the call goes out on it", resp.StatusCode, body)
	}
	if got := s.up.Count(); got != 2 {
		t.Fatalf("the upstream saw %d requests, want the probe on the shared token and the call it settled", got)
	}
	if probe := s.up.Body(0); !strings.Contains(string(probe), `"max_tokens":1`) {
		t.Errorf("the first call is %s, want the probe: nothing was asked of the shared token, and the credential a probe is due on is the one the model admits", probe)
	}
	if got, _ := s.state.mark(provider); !reflect.DeepEqual(got, planted) {
		t.Errorf("the provider credential's mark is %+v, want the one the exhaustion left (%+v): a probe ran on it, and the probe's body names this call's own model", got, planted)
	}
	if m, ok := s.state.mark(config.DefaultClaudeAccount); ok && !m.Available() {
		t.Errorf("the shared token's mark is %+v, want it cleared: it is the credential the probe was owed on", m)
	}
}

// TestEntryForServesAFamilyEntryByItsOwnKey is the RESOLUTION's half of C1's
// family, and it is the half the copy (KS12) cannot work without: the offering
// hands a call a repository's own entry by NAME — a repository's own Anthropic
// key at tier B (ownNamedEntries, D3008) and its provider's key at the reserved
// tier C (F32) — and entryFor is the one place that name becomes a value. Two of
// the three arms it had could not answer one: the family is in no byName, so the
// lookup by name refused it, and a repository's own PROVIDER key carries the
// reserved tier C the PROJECT's provider key does, so the tier alone told
// neither from the other. What a refusal here costs is the whole point of the
// copy read backwards: a repository the copy has written an entry for is refused
// EVERY model call it makes ("the selected credential is not in the bundle"),
// which is worse than the state the copy set out to improve.
//
// The document is one a copy has run against (namedBundleDoc): two repositories
// hold ONE provider name and one of them holds an Anthropic key of its own, so a
// resolution that fell back to the name — or answered another repository's entry
// of the same name — attaches a value nobody selected for this call (R2, F42).
func TestEntryForServesAFamilyEntryByItsOwnKey(t *testing.T) {
	doc, _, _, values := namedBundleDoc(t)
	mine := repoEntryKeyFor(t, "github", "acme/one", "openrouter")
	theirs := repoEntryKeyFor(t, "github", "acme/two", "openrouter")
	own := repoEntryKeyFor(t, "github", "acme/one", config.AnthropicKeyAccount)
	b, err := ParseBundle(doc)
	if err != nil {
		t.Fatalf("ParseBundle of the document a copy has run against: %v", err)
	}

	// Each member resolves to the entry its own key names, to the value the
	// document holds under it and to nothing else: the kind is the family's own
	// (never KindModel, which is the answer that would make an entry a cascade
	// may select, F33) and the tier is the one the candidate was offered at.
	for _, tc := range []struct {
		what, key string
		tier      creds.Tier
	}{
		{"the repository's own Anthropic key", own, creds.TierB},
		{"the repository's own provider key", mine, creds.TierC},
	} {
		e, ok := entryFor(b, creds.Credential{Name: tc.key, Tier: tc.tier})
		if !ok {
			t.Fatalf("entryFor answered nothing for %s (%s at tier %s), and the document holds it: the offering offered it by name, and the call would be refused over a credential the bundle carries", tc.what, tc.key, tc.tier)
		}
		if e.Value() != values[tc.key] {
			t.Errorf("%s resolved to a value the document does not hold under %s: the entry a call is served is the one its own key names (D3008)", tc.what, tc.key)
		}
		if e.Key != tc.key || e.Kind != KindRepoCredential || e.Tier != tc.tier {
			t.Errorf("%s resolved to %+v, want the family's kind under the document key %q at tier %s", tc.what, e, tc.key, tc.tier)
		}
	}

	// And never to another repository's entry of the same credential name: the
	// two are one name in the document and two keys, and the other repository's
	// is the value the fallback would attach.
	if e, ok := entryFor(b, creds.Credential{Name: mine, Tier: creds.TierC}); !ok || e.Value() == values[theirs] {
		t.Errorf("the candidate %s resolved to %+v, and the document holds %q for the same provider under another repository (R2): the family's key carries the repository it was entered for", mine, e, values[theirs])
	}
	// A family-shaped name the document does not hold resolves to nothing: the
	// key is read as it stands and nothing is derived from it, so a candidate
	// whose key no entry carries is not one this bundle can serve (D143).
	absent := repoEntryKeyFor(t, "github", "acme/one", "deepseek")
	if e, ok := entryFor(b, creds.Credential{Name: absent, Tier: creds.TierC}); ok {
		t.Errorf("a family-shaped name the document does not hold resolved to %+v, want nothing", e)
	}

	// What entryFor answers is what send attaches to the call, so the
	// repository's own Anthropic key is read off the request the upstream saw:
	// its own value under the shape the entry names, and no other repository's
	// anywhere in the headers. The fixture's client rewrites every host to a
	// loopback fake, so a call to the key's own address — the Anthropic API —
	// really arrives there and no request leaves the machine (Convention 9).
	s := newSvc(t)
	e, ok := entryFor(b, creds.Credential{Name: own, Tier: creds.TierB})
	if !ok {
		t.Fatal("the repository's own Anthropic key did not resolve to an entry")
	}
	ans, err := s.server.send(context.Background(), providerCall(t, MessagesPath), e)
	if err != nil {
		t.Fatalf("the call served by the repository's own Anthropic key was not sent: %v", err)
	}
	defer ans.resp.Body.Close()
	if ans.resp.StatusCode != http.StatusOK {
		t.Fatalf("the upstream answered %d, want its 200", ans.resp.StatusCode)
	}
	if ans.credential != values[own] {
		t.Error("the answer is redacted with a value that is not the repository's own key's")
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1", n)
	}
	req := s.up.Seen()[0]
	if got, want := req.Header.Get("x-api-key"), values[own]; got != want {
		t.Errorf("the call carried x-api-key = %q, want the value of the entry its own key resolved to", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("the call also carried Authorization = %q: the entry names one shape (D1418)", got)
	}
	for name, vals := range req.Header {
		for _, v := range vals {
			for _, other := range []string{values[mine], values[theirs]} {
				if strings.Contains(v, other) {
					t.Errorf("the call carries another repository's key in %s", name)
				}
			}
		}
	}
	if got := req.URL.Path; got != MessagesPath {
		t.Errorf("the call went to %q, want the entry's own address and the served path: a repository's own Anthropic key goes to the Anthropic API", got)
	}
}
