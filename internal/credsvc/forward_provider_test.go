package credsvc

// KS4's route: the provider route serves a named provider under the policy's
// rules (R1, R11, D3003).
//
// The route is the one this service has whose caller NAMES the provider its call
// is for: the run's in-runner gateway routes a non-Claude model to a provider's
// endpoint and holds no key for it, so the call arrives here with the provider's
// name in X-Fugaro-Provider (C1's wire) and the service derives the entry — the
// one that repository's runs may route to — from the run's VERIFIED repository
// and that name, and forwards.
//
// What these tests pin, and each for a reason of its own:
//
//   - a hand-built call is refused. The job's own ID token reaches this service
//     directly (an agent inside the job holds it), so a body an agent composed
//     could be posted here past the run's own gateway and past its pins. Every
//     rule the gateway applies to a routed call is applied here first — the
//     project's allow-list, the variant suffix, the foreign field, the shape and
//     the max_tokens bound — over the routes the PUBLISHED POLICY builds, and
//     the provider must be one the policy names and one whose own models list
//     claims the model the call asks for (D3003, R11).
//   - an honest routed call is served, and is not refused as unpinned: the
//     project's allow-list is the pin, and a model the list holds and the
//     provider claims is a call this service forwards.
//   - the entry is derived from the VERIFIED repository and never from anything
//     the call says: two repositories of one project hold their own entries for
//     the same provider, and neither is served the other's (HG1's reach rule).
//   - the key that goes out is the named entry's, under the auth shape the entry
//     names, and the body the caller sent is the body the provider receives,
//     byte for byte.
//   - the address is the ENTRY's and never the call's: the caller's own headers
//     cannot move it, and the route's own prefix never reaches the provider.
//
// Everything here is hermetic (Convention 9): the entries' base URLs are this
// test's own hosts and the fixture's client rewrites every host to a loopback
// fake, so the URL the service builds is the one the document holds and no
// request leaves the machine. Nothing is, or looks like, a credential
// (Convention 2): every value comes from fakeCred.

import (
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// The routed fixture's names and models. None is a credential: a provider's name
// is a word the policy publishes, and a model ID is text a call carries.
const (
	// routedProvider and routedOther are the two providers the project's policy
	// names and the fixture's bundle holds entries for — one of them for BOTH
	// repositories of the project, which is what tells "the entry the verified
	// repository derives" apart from "the entry the project holds".
	routedProvider = "openrouter"
	routedOther    = "deepseek"
	// routedUnheld is a third provider the policy names and no bundle holds:
	// the policy's own rule and the bundle's are two, and this is the name only
	// the first admits.
	routedUnheld = "together"
	// routedStranger is a provider's name the policy does NOT name.
	routedStranger = "mistral"
	// routedModel, routedOtherModel and routedVariant are models the providers'
	// own blocks claim, through the fixture's one pattern per provider
	// ("<name>/*", testPolicyDocProvidersWith).
	routedModel      = "openrouter/openrouter-v4"
	routedOtherModel = "deepseek/deepseek-v4.1-flash"
	// routedVariant is routedOtherModel carrying a provider variant suffix
	// (":free"). It is deliberately NOT a member of the allow-list: the
	// published policy refuses to carry one at all — Decode answers a
	// catalogue-known variant as an alias and names the explicit ID to write
	// instead — so the pin is one of the two rules that refuse it below, and the
	// other is the gateway's own variant rule (which a policy that DID carry one
	// would reach).
	routedVariant = routedOtherModel + ":free"
	// routedUnlisted is a model routedOther's block CLAIMS and the project's
	// allow-list does not hold: the other half of the two rules, and the one
	// the pin refuses.
	routedUnlisted = "deepseek/deepseek-v9"
)

// routedBase is one provider's own endpoint as the fixture's bundle records it,
// and it is deliberately not the address the policy's providers block carries
// (which is routedBase(provider)+"/v1", the fixture helper's own shape): a call
// that went to the policy's address rather than the entry's would be a call to
// an address nobody chose for this credential (D1418), and the two spellings
// have to differ for that to be assertable.
func routedBase(provider string) string { return "https://" + provider + ".example.com/api" }

// routedValue is the credential value one repository's entry for one provider
// holds. It differs per repository AND per provider, so an upstream request says
// which of the entries was attached.
func routedValue(t *testing.T, repo, provider string) string {
	t.Helper()
	return fakeCred(t, strings.ReplaceAll(repo, "/", "-")+"-"+provider)
}

// routedAuth is the auth shape the fixture's bundle names for one entry: both
// shapes are used, and the same provider's two repositories are given DIFFERENT
// ones, so the shape the upstream request carries says which entry served it as
// plainly as the value does.
func routedAuth(repo, provider string) string {
	if repo == testRepoPath && provider == routedOther {
		return providerAuthXAPIKey
	}
	return providerAuthBearer
}

// routedBundleDoc is the fixture's bundle: the platform's own document, the
// project's single provider key BESIDE the named family, and one named entry per
// repository and provider. The project's key is in it on purpose — it is the
// entry an AK6b-style route would have attached whatever provider a call named,
// and the red step of this task is exactly that it is NOT the one served.
func routedBundleDoc(t *testing.T) []byte {
	t.Helper()
	doc := append([]byte{}, testBundleDoc(t)...)
	doc = append(doc, providerKeyDoc(t, testProviderBase, providerAuthBearer)...)
	for _, e := range []struct{ repo, provider string }{
		{testRepoPath, routedProvider},
		{testRepoPath, routedOther},
		{testOtherRepoPath, routedOther},
	} {
		key := repoEntryKeyFor(t, "github", e.repo, e.provider)
		doc = append(doc, repoEntryDoc(t, key, routedValue(t, e.repo, e.provider),
			routedBase(e.provider), routedAuth(e.repo, e.provider))...)
	}
	return doc
}

// routedBudget is the project's published allow-list, written out with the exact
// model IDs the fixture's calls name.
//
// The IDs are exact on purpose: the pin is read by the gateway's own
// sameModel, which resolves two SPELLINGS of one model through the price table
// but never through a route (a model a route claims is the same model only where
// the IDs are equal), so a pattern here would pin nothing.
func routedBudget(models ...string) string {
	quoted := make([]string, 0, len(models))
	for _, m := range models {
		quoted = append(quoted, `"`+m+`"`)
	}
	return `{"mode": "enforce", "per_run_usd": 5, "allowed_models": [` + strings.Join(quoted, ", ") + `]}`
}

// routedPolicyDoc is the fixture's shared node with the project's providers
// block written out and a chosen budget document in place of the fixture's own.
func routedPolicyDoc(t *testing.T, budget string) string {
	t.Helper()
	doc := testPolicyDocProviders(routedProvider, routedOther, routedUnheld)
	if !strings.Contains(doc, testBudgetDoc) {
		t.Fatal("the providers fixture no longer writes testBudgetDoc; the routed fixture cannot publish its own allow-list")
	}
	return strings.Replace(doc, testBudgetDoc, budget, 1)
}

// routedSvc is the state every test here is about: a project whose policy names
// the three providers and publishes the allow-list, and a bundle holding the
// project's key and one named entry per repository and provider.
func routedSvc(t *testing.T) *svc {
	s, _ := routedSvcWithAudit(t)
	return s
}

// routedSvcWithAudit is routedSvc with the service's own audit lines in a buffer
// of their own, for the assertions about what the line names.
func routedSvcWithAudit(t *testing.T) (*svc, *logBuf) {
	t.Helper()
	return auditSvc(t, func(s *svc, cfg *Config) { routedInstall(t, s, cfg) })
}

// routedSvcOn is routedSvc with the client the upstream calls go out over, for
// the test that reads the URL the service built rather than an answer.
func routedSvcOn(t *testing.T, client *http.Client) *svc {
	t.Helper()
	s, _ := auditSvc(t, func(s *svc, cfg *Config) {
		routedInstall(t, s, cfg)
		cfg.Client = client
	})
	return s
}

// routedInstall is the state above, installed on a fixture the caller built.
func routedInstall(t *testing.T, s *svc, cfg *Config) {
	t.Helper()
	s.policy.shared = decodeTestPolicy(t, routedPolicyDoc(t,
		routedBudget(testModel, routedModel, routedOtherModel)))
	s.src.docs = [][]byte{routedBundleDoc(t)}
	// The verifier's own list is rebuilt with the second repository's job
	// account, because a caller's class is decided from the verified email: a
	// call of the other repository would otherwise be refused as an unknown
	// caller and would never reach the pairing or this route, and which entry
	// the VERIFIED repository derives is exactly what those cases are about (the
	// workflow-secrets fixture rebuilds it for the same reason).
	auth, err := NewAuthenticator(AuthConfig{
		Project:         testProject,
		FirebaseProject: testFirebase,
		Audience:        testAudience,
		JobAccounts:     []string{testJobSA, testOtherSA},
		Keys:            s.keys,
		JobKeys:         s.jobKeys,
		Outcomes:        RTDBOutcomes{DB: s.db},
		Now:             func() time.Time { return s.now },
	})
	if err != nil {
		t.Fatalf("the fixture's own verifier configuration is not one NewAuthenticator accepts: %v", err)
	}
	cfg.Auth, s.auth = auth, auth
}

// routedName is the header mutation that names one provider on a call.
func routedName(name string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(HeaderProvider, name) }
}

// routedCallBody is a routed call's Messages body: a model, a bound the budget
// can price, and one message.
func routedCallBody(model string) string {
	return `{"model":` + strconv.Quote(model) + `,"max_tokens":16,"messages":[{"role":"user","content":"routed"}]}`
}

// routedOtherRepo is the other of the two repositories the fixture's project
// holds, for the claims about whose entry was attached.
func routedOtherRepo(repo string) string {
	if repo == testRepoPath {
		return testOtherRepoPath
	}
	return testRepoPath
}

// routedModelOf is the model the fixture's calls name for one provider: the one
// that provider's own block claims.
func routedModelOf(provider string) string {
	if provider == routedProvider {
		return routedModel
	}
	return routedOtherModel
}

// callAs is one call made by a run of the repository named, with the job account
// bound to it: what "the run's verified repository" is, which is the whole of
// what the entry is derived from. The fixture's own repository is the cheap case
// and needs no mutation.
func (s *svc) callAs(t *testing.T, repo, path, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	if testSlug(t, repo) == s.slug {
		return s.callPath(t, path, body, mutate...)
	}
	slug := testSlug(t, repo)
	run := s.runToken(func(c map[string]any) {
		c["fs"] = slug
		c["sub"] = "r~" + slug + "~" + s.run
		c["user_id"] = c["sub"]
	})
	job := s.jobToken(func(c map[string]any) { c["email"] = testOtherSA })
	resp := s.postTokens(t, path, body, run, job, s.job, mutate...)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, b
}

// TestAHandBuiltCallOutsideThePolicyIsRefused is D3003's red step: the route
// judges a call by the policy's own rules before any credential is read, so a
// body an agent composed — an agent inside the job holds the job's own ID token
// and can post here directly, past the run's gateway and past its pins — is
// refused rather than forwarded with a key attached.
//
// Mutation (run, restore): drop the providerRequestRefusal call from providerCall
// and the two subtests that only a route's rules can refuse — the variant suffix
// and the foreign field — are forwarded instead, which is the escape D3003 is
// about; drop the models-list claim check and the "a model the named provider
// does not serve" case forwards another provider's model with this provider's
// key; drop requestNoAllowListRefusal and a project that publishes no allow-list
// is forwarded under no ceiling; read the provider's name off the request's body
// or a header readCall keeps, and the header cases stop being refused.
func TestAHandBuiltCallOutsideThePolicyIsRefused(t *testing.T) {
	s, auditLog := routedSvcWithAudit(t)
	// The document key one repository's entry for routedProvider is held under:
	// a name derived from the repository, and never a provider's own name.
	docKey := familyName(t, s.slug, routedProvider)
	for _, tc := range []struct {
		what string
		// names is what the call carries in X-Fugaro-Provider: none for a call
		// that names no provider, two for a caller that named two, and any
		// value whose shape the wire refuses.
		names []string
		body  string
		// want is the refusal's own type: provider_not_allowed is the
		// published policy's answer and request_refused is the gateway's
		// request check's (C1's wire).
		want string
		// noEcho is a caller's own text the refusal must not carry: a refusal
		// names the field, never its content.
		noEcho []string
	}{
		{
			what: "a call that names no provider at all",
			body: routedCallBody(routedModel),
			want: ErrorRequestRefused,
		},
		{
			what:  "a call that names two providers",
			names: []string{routedProvider, routedOther},
			body:  routedCallBody(routedModel),
			want:  ErrorRequestRefused,
		},
		{
			what:   "a call whose value is not a provider's name",
			names:  []string{docKey},
			body:   routedCallBody(routedModel),
			want:   ErrorRequestRefused,
			noEcho: []string{docKey},
		},
		{
			what:  "a provider the project does not publish",
			names: []string{routedStranger},
			body:  routedCallBody(routedModel),
			want:  ErrorProviderNotAllowed,
		},
		{
			what:  "a model the named provider does not serve",
			names: []string{routedProvider},
			body:  routedCallBody(routedOtherModel),
			want:  ErrorProviderNotAllowed,
		},
		{
			// The gateway's own sentence names the model it refused, which is
			// what the run would have been told by its own gateway, and this
			// case keeps that: it is the model the CALLER named in its own body
			// and not a credential.
			what:  "a model the project's allow-list does not hold",
			names: []string{routedOther},
			body:  routedCallBody(routedUnlisted),
			want:  ErrorProviderNotAllowed,
		},
		{
			what:  "a model ID carrying a variant suffix",
			names: []string{routedOther},
			body:  routedCallBody(routedVariant),
			want:  ErrorRequestRefused,
		},
		{
			what:  "a top-level field a provider route does not take",
			names: []string{routedOther},
			body: `{"model":"` + routedOtherModel + `","max_tokens":16,"messages":[{"role":"user","content":"routed"}],` +
				`"plugins":[{"id":"web"}]}`,
			want: ErrorRequestRefused,
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			before, lines := s.up.Count(), len(forwardLines(t, auditLog))
			resp, body := s.callPath(t, ProviderRoutePath, tc.body, func(r *http.Request) {
				for _, n := range tc.names {
					r.Header.Add(HeaderProvider, n)
				}
			})
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != tc.want {
				t.Errorf("the refusal is %q, want %q: %s", e.Error, tc.want, e.Message)
			}
			for _, text := range tc.noEcho {
				if strings.Contains(e.Message, text) {
					t.Errorf("the refusal carries the caller's own text %q: a refusal names the field, never its content:\n%s", text, e.Message)
				}
			}
			if n := s.up.Count() - before; n != 0 {
				t.Errorf("the provider saw %d requests, want none: the call was refused before it was forwarded", n)
			}
			// The refusal is after the caller's identity was verified, so it
			// owes the operator exactly one line — and the line names no
			// credential, because none was read.
			got := forwardLines(t, auditLog)
			if len(got) != lines+1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly 1: %v", len(got)-lines, got[lines:])
			}
			line := got[len(got)-1]
			checkAuditShape(t, line)
			if c := auditField(line, "credential"); c != "" {
				t.Errorf("the audit line names the credential %q: no credential was chosen and none was read", c)
			}
		})
	}

	// The routes the cases above are judged by are the PUBLISHED POLICY's own
	// (R11): one per provider it names, claiming exactly the models that
	// provider's block claims. That is what makes the last two cases fire at all
	// — a route is the only thing that can refuse a variant suffix or a foreign
	// field — and what a route carries beyond the two is deliberately nothing:
	// no address and no credential, because this is the shape a REQUEST is
	// judged by, and where a call goes and how its key is sent are the bundle
	// entry's alone (D1418).
	t.Run("the routes are the published policy's own", func(t *testing.T) {
		routes := providerRoutes(s.policy.shared)
		if len(routes) != len(s.policy.shared.Providers) {
			t.Fatalf("the check is run against %d routes for a policy naming %d providers", len(routes), len(s.policy.shared.Providers))
		}
		names := make([]string, 0, len(routes))
		for _, r := range routes {
			names = append(names, r.Name)
			p, ok := s.policy.shared.Providers[r.Name]
			if !ok {
				t.Errorf("the check is run against a route for %q, a provider the policy does not name", r.Name)
				continue
			}
			if !slices.Equal(r.Models, p.Models) {
				t.Errorf("the route for %q claims %v, want the provider's own models %v", r.Name, r.Models, p.Models)
			}
			if r.BaseURL != "" || r.Auth != "" || r.Credential != nil {
				t.Errorf("the route for %q carries an address, an auth shape or a credential: a route built from the policy is the shape a request is judged by, and nothing of what a call needs to go out", r.Name)
			}
		}
		if want := slices.Sorted(maps.Keys(s.policy.shared.Providers)); !slices.Equal(names, want) {
			t.Errorf("the routes are %v, want the policy's providers sorted by name %v: a refusal cannot depend on a map's iteration order", names, want)
		}
	})

	// A project that publishes no model allow-list refuses every provider call:
	// this service has no ceiling to hold the call to and will not forward under
	// none (the owner's ruling of 2026-10-22, requestNoAllowListRefusal).
	t.Run("a model under a project that publishes no allow-list", func(t *testing.T) {
		s := newSvcWith(t, func(s *svc, _ *Config) {
			s.policy.shared = decodeTestPolicy(t, routedPolicyDoc(t, `{"mode": "enforce", "per_run_usd": 5}`))
			s.src.docs = [][]byte{routedBundleDoc(t)}
		})
		resp, body := s.callPath(t, ProviderRoutePath, routedCallBody(routedOtherModel), routedName(routedOther))
		if resp.StatusCode != StatusRequestRefused {
			t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
		}
		if e := refusal(t, resp.StatusCode, body); e.Error != ErrorProviderNotAllowed {
			t.Errorf("the refusal is %q, want %q: a project that publishes no allow-list refuses the call rather than forwarding under none", e.Error, ErrorProviderNotAllowed)
		}
		if n := s.up.Count(); n != 0 {
			t.Errorf("the provider saw %d requests, want none", n)
		}
	})
}

// TestAnHonestRoutedModelIsNotRefusedAsUnpinned is the green half: a model the
// project's allow-list holds and the named provider's block claims IS a call
// this service serves, on both endpoints of the route — and the check that
// refuses the calls above accepts it, so the route's own rules are a ceiling and
// not a wall.
//
// The control is asserted the way the service holds it: through the gateway's
// own check over the project's pin and the policy's routes, which is the same
// function and the same arguments providerCall hands it. A routed model that a
// stage's pin would not know is therefore not "unpinned" here — the project's
// allow-list is the pin on this route, and it is the policy's own list (R11).
//
// Both endpoints are asked because the count-tokens path is served without the
// request check, for the reason the messages route gives: the body carries no
// max_tokens and the gateway's own route does not hold it to the rules either.
//
// Mutation (run, restore): pin the call to a stage's models rather than the
// project's list — an empty or foreign pin — and the messages subtest is red on
// the refusal the service answers instead of the upstream's own answer.
func TestAnHonestRoutedModelIsNotRefusedAsUnpinned(t *testing.T) {
	body := routedCallBody(routedModel)
	for _, tc := range []struct{ what, path, body string }{
		{"the messages endpoint", ProviderRoutePath, body},
		{"the token count", ProviderCountTokensPath, body},
	} {
		t.Run(tc.what, func(t *testing.T) {
			s := routedSvc(t)
			if tc.path == ProviderRoutePath {
				refusal, err := gateway.RequestRefusal([]byte(tc.body), "", requestPin(s.policy.shared),
					pricing.Embedded(), providerRoutes(s.policy.shared))
				if err != nil || refusal != "" {
					t.Fatalf("the gateway's own check refuses the honest call (%v: %s): the service refuses a call the rules the wire names accept", err, refusal)
				}
			}
			resp, ans := s.callPath(t, tc.path, tc.body, routedName(routedProvider))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, http.StatusOK, ans)
			}
			if n := s.up.Count(); n != 1 {
				t.Fatalf("the provider saw %d requests, want 1", n)
			}
			// The answer is the provider's call and the third tier, settled
			// under the DOCUMENT KEY: one repository's entry for one provider,
			// which is the name an operator's tooling reads (R5).
			if got, want := resp.Header.Get(HeaderCredential), familyName(t, s.slug, routedProvider); got != want {
				t.Errorf("%s = %q, want the entry's document key %q", HeaderCredential, got, want)
			}
			if got := resp.Header.Get(HeaderTier); got != TierC {
				t.Errorf("%s = %q, want %q", HeaderTier, got, TierC)
			}
			req := s.up.Seen()[0]
			if got, want := req.Header.Get("Authorization"), "Bearer "+routedValue(t, testRepoPath, routedProvider); got != want {
				t.Error("the provider was not handed this repository's own entry for the provider the call named")
			}
			if got, want := req.URL.Path, strings.TrimPrefix(tc.path, ProviderRoutePrefix); got != "/api"+want {
				t.Errorf("the provider was called at %q, want the entry's own base and the endpoint the route serves", got)
			}
		})
	}
}

// TestTheEntryIsDerivedFromTheVerifiedRepository is R2 and HG1's reach rule on
// this route: the entry is the pair (the run's VERIFIED repository, the provider
// the call named) and never anything a call carries. Two repositories of one
// project hold their own entries for the same provider — with different values,
// different auth shapes and the same base URL — so an upstream request says
// which of them served it, and a call of the repository that holds none for a
// provider the policy names is refused rather than served another's.
//
// Mutation (run, restore): derive the entry from the provider alone — the
// project's key, or the first entry of the family the bundle holds — and the
// other repository's subtest goes red on the value the provider was handed, and
// the not-served case is served instead of refused.
func TestTheEntryIsDerivedFromTheVerifiedRepository(t *testing.T) {
	for _, tc := range []struct {
		what     string
		repo     string
		provider string
		served   bool
	}{
		{"this run's repository and the provider it holds", testRepoPath, routedProvider, true},
		{"this run's repository and its second provider", testRepoPath, routedOther, true},
		{"the other repository of the project and the provider it holds", testOtherRepoPath, routedOther, true},
		{"the other repository and a provider it holds no entry for", testOtherRepoPath, routedProvider, false},
	} {
		t.Run(tc.what, func(t *testing.T) {
			s := routedSvc(t)
			slug := testSlug(t, tc.repo)
			resp, ans := s.callAs(t, tc.repo, ProviderRoutePath, routedCallBody(routedModelOf(tc.provider)), routedName(tc.provider))
			// The OTHER repository's entry for the same provider is the value
			// that must not have been attached, on either path (for a call of
			// the other repository itself, that is this run's own repository).
			elsewhere := routedValue(t, routedOtherRepo(tc.repo), tc.provider)
			if !tc.served {
				if resp.StatusCode != StatusUnavailable {
					t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, ans)
				}
				if e := refusal(t, resp.StatusCode, ans); e.Error != ErrorProviderNotServed {
					t.Errorf("the refusal is %q, want %q: the bundle holds no entry for this repository and this provider", e.Error, ErrorProviderNotServed)
				}
				if n := s.up.Count(); n != 0 {
					t.Errorf("the provider saw %d requests, want none", n)
				}
				for _, v := range []string{elsewhere, routedValue(t, tc.repo, tc.provider), fakeCred(t, "provider-key")} {
					if strings.Contains(string(ans), v) {
						t.Error("the refusal carries a credential value: a refusal names the field, never its content")
					}
				}
				return
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, http.StatusOK, ans)
			}
			if n := s.up.Count(); n != 1 {
				t.Fatalf("the provider saw %d requests, want 1", n)
			}
			mine := routedValue(t, tc.repo, tc.provider)
			req := s.up.Seen()[0]
			switch routedAuth(tc.repo, tc.provider) {
			case providerAuthBearer:
				if got, want := req.Header.Get("Authorization"), "Bearer "+mine; got != want {
					t.Errorf("the provider was handed %q under the bearer shape, want this repository's own entry", got)
				}
				if got := req.Header.Get("x-api-key"); got != "" {
					t.Errorf("the call also carried x-api-key = %q: one entry, one shape", got)
				}
			default:
				if got := req.Header.Get("x-api-key"); got != mine {
					t.Error("the provider was not handed this repository's own entry under the shape the entry names")
				}
				if got := req.Header.Get("Authorization"); strings.Contains(got, mine) {
					t.Error("the entry's key was sent under the bearer shape too: one entry, one shape")
				}
			}
			if got := req.Header.Get("x-api-key"); got == elsewhere || (got != "" && strings.Contains(got, elsewhere)) {
				t.Error("the provider was handed ANOTHER repository's entry for the same provider")
			}
			if got := req.Header.Get("Authorization"); strings.Contains(got, elsewhere) {
				t.Error("the provider was handed ANOTHER repository's entry for the same provider")
			}
			if got, want := resp.Header.Get(HeaderCredential), familyName(t, slug, tc.provider); got != want {
				t.Errorf("%s = %q, want %q: the name travels with the entry the verified repository derives", HeaderCredential, got, want)
			}
			if got := resp.Header.Get(HeaderTier); got != TierC {
				t.Errorf("%s = %q, want %q", HeaderTier, got, TierC)
			}
		})
	}

	// What the operator reads names the document key — the repository and the
	// credential together — and holds no value.
	t.Run("the audit line names the entry and no value", func(t *testing.T) {
		s, auditLog := routedSvcWithAudit(t)
		resp, ans := s.callPath(t, ProviderRoutePath, routedCallBody(routedModelOf(routedProvider)), routedName(routedProvider))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, http.StatusOK, ans)
		}
		lines := forwardLines(t, auditLog)
		if len(lines) != 1 {
			t.Fatalf("the served call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		line := lines[0]
		checkAuditShape(t, line)
		if got, want := auditField(line, "credential"), familyName(t, s.slug, routedProvider); got != want {
			t.Errorf("the audit line carries credential %v, want the entry's document key %q", got, want)
		}
		if got := auditField(line, "tier"); got != TierC {
			t.Errorf("the audit line carries tier %v, want %q", got, TierC)
		}
		raw := auditLog.String()
		for _, v := range []string{routedValue(t, testRepoPath, routedProvider), routedValue(t, testRepoPath, routedOther), fakeCred(t, "provider-key")} {
			if strings.Contains(raw, v) {
				t.Error("the audit line carries a credential value")
			}
		}
		// The line names the repository the entry was derived for, and it is
		// this run's own: another repository's entry is never read for this
		// call, so nothing of it can be in the line.
		if other := testSlug(t, testOtherRepoPath); strings.Contains(raw, other) {
			t.Errorf("the audit line carries another repository's slug %q", other)
		}
	})
}

// TestTheNamedKeyIsAttachedAndTheBodyUntouched is the forwarding half of the
// route: the entry's value is attached under the auth shape the ENTRY names and
// under no other, the body the caller sent is the body the provider receives —
// byte for byte, with nothing added, dropped or rewritten — and the caller's own
// addressing header, the contract's headers and the project's single provider
// key are all absent from what the provider sees.
//
// The bytes matter here and not only the fields: this service replaces the
// caller's Authorization with a credential the caller never held, and a body
// edited on the way through would be a call the run's own gateway priced
// differently from the one that went out.
//
// Mutation (run, restore): send the entry's key under both auth shapes and the
// absent-header assertion is red; copy the incoming header set instead of the
// gateway's provider allow-list and X-Fugaro-Provider reaches the provider; build
// the upstream request from a decoded-and-re-encoded body and the byte
// comparison is red.
func TestTheNamedKeyIsAttachedAndTheBodyUntouched(t *testing.T) {
	for _, tc := range []struct {
		what     string
		provider string
	}{
		{"the entry whose auth shape is bearer", routedProvider},
		{"the entry whose auth shape is x-api-key", routedOther},
	} {
		t.Run(tc.what, func(t *testing.T) {
			s := routedSvc(t)
			body := routedCallBody(routedModelOf(tc.provider))
			resp, ans := s.callPath(t, ProviderRoutePath, body, routedName(tc.provider))
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the call is answered %d, want %d: %s", resp.StatusCode, http.StatusOK, ans)
			}
			req := s.up.Seen()[0]
			if got, want := string(s.up.Body(0)), body; got != want {
				t.Errorf("the provider was handed a body the caller did not send:\n got %s\nwant %s", got, want)
			}
			mine := routedValue(t, testRepoPath, tc.provider)
			project := fakeCred(t, "provider-key")
			for k, vs := range req.Header {
				for _, v := range vs {
					if strings.Contains(v, project) {
						t.Errorf("the request's %s header carries the project's single provider key: the named entry is the one this route serves", k)
					}
					if k == HeaderProvider || strings.HasPrefix(strings.ToLower(k), strings.ToLower(headerPrefixFugaro)) {
						t.Errorf("the caller's own %s header reached the provider: every X-Fugaro- header of a call is dropped when it is read", k)
					}
				}
			}
			if got := req.URL.RawQuery; strings.Contains(got, mine) {
				t.Error("the entry's key reached the query")
			}
			switch routedAuth(testRepoPath, tc.provider) {
			case providerAuthBearer:
				if got, want := req.Header.Get("Authorization"), "Bearer "+mine; got != want {
					t.Error("the entry's key was not attached under the shape the entry names")
				}
				if got := req.Header.Get("x-api-key"); got != "" {
					t.Error("the entry's key was attached under a second shape")
				}
			default:
				if got := req.Header.Get("x-api-key"); got != mine {
					t.Error("the entry's key was not attached under the shape the entry names")
				}
				if got := req.Header.Get("Authorization"); strings.Contains(got, mine) {
					t.Error("the entry's key was attached under a second shape")
				}
			}
		})
	}
}

// TestTheUpstreamIsTheEntrysNotTheCallers is D1418 and R11 on the destination:
// the address a routed call goes to is the one the BUNDLE ENTRY holds and never
// one the call carries. The two spellings differ on purpose — the policy's
// providers block names "…/api/v1" and the entry names "…/api" — so a call built
// from the policy, from a caller's header or from Anthropic's own base URL is a
// different URL from the one recorded here, and the route's own prefix never
// appears in what the provider is asked for.
//
// The recording transport is what makes the assertion exact: it answers nothing
// (the route refuses with the upstream read, which is the honest consequence of
// a call that never left), so what is read is the URL the service BUILT.
//
// Mutation (run, restore): build the destination from gateway.AnthropicBase — or
// from the policy's providers block — and the recorded URL is red on the host,
// the path or both; append the route's own path rather than the endpoint it
// carries and the "/v1/provider" assertion is red.
func TestTheUpstreamIsTheEntrysNotTheCallers(t *testing.T) {
	for _, tc := range []struct{ what, path, endpoint string }{
		{"the messages endpoint", ProviderRoutePath, MessagesPath},
		{"the token count", ProviderCountTokensPath, CountTokensPath},
	} {
		t.Run(tc.what, func(t *testing.T) {
			rec := &recordingTransport{}
			s := routedSvcOn(t, &http.Client{Transport: rec})
			resp, ans := s.callPath(t, tc.path, routedCallBody(routedModelOf(routedProvider)), func(r *http.Request) {
				r.Header.Set(HeaderProvider, routedProvider)
				// A caller's own idea of where the call should go, in the
				// shapes a caller could try: none of them is an address this
				// service reads.
				r.Header.Set("X-Fugaro-Base-URL", "https://caller.example/api")
				r.Header.Set("Base-URL", "https://caller.example/api")
				r.Header.Set("X-Upstream", "https://caller.example/api")
			})
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the call is answered %d, want %d: the upstream was a recording transport and no answer can come back: %s", resp.StatusCode, StatusUnavailable, ans)
			}
			urls := rec.seen()
			if len(urls) != 1 {
				t.Fatalf("the service built %d requests, want 1: %v", len(urls), urls)
			}
			want := routedBase(routedProvider) + tc.endpoint
			if urls[0] != want {
				t.Errorf("the call was built for %q, want the ENTRY's own base and path %q", urls[0], want)
			}
			if strings.Contains(urls[0], ProviderRoutePrefix) {
				t.Errorf("the call to the provider carries this service's own prefix (%q): the route's path is the service's and never the provider's", urls[0])
			}
			if policy := routedBase(routedProvider) + "/v1" + tc.endpoint; urls[0] == policy {
				t.Errorf("the call was built for the policy's own address (%q): where a provider is, is the bundle entry's and nothing else (D1418)", policy)
			}
			if anthropic := gateway.AnthropicBase(gateway.Upstream{}); strings.HasPrefix(urls[0], anthropic) {
				t.Errorf("the call was built for Anthropic's own endpoint (%q), where a provider's key is nobody's business", anthropic)
			}
		})
	}
}

// routedReviewSvc is routedSvc's state under a REVIEW-class caller: the same
// policy, the same bundle and the same repository as every test above, with the
// verifier and the pairing both admitting the fixture's review job account
// instead of — beside — the repository's own. The state is deliberately the one
// the tests above serve calls from, because a class fence can only be read
// against a call the other classes ARE served: a fixture with nothing to attach
// would refuse for the wrong reason and look the same.
func routedReviewSvc(t *testing.T) (*svc, *logBuf) {
	t.Helper()
	return auditSvc(t, func(s *svc, cfg *Config) {
		s.policy.shared = decodeTestPolicy(t, routedPolicyDoc(t,
			routedBudget(testModel, routedModel, routedOtherModel)))
		s.src.docs = [][]byte{routedBundleDoc(t)}
		auth, err := NewAuthenticator(AuthConfig{
			Project:           testProject,
			FirebaseProject:   testFirebase,
			Audience:          testAudience,
			JobAccounts:       []string{testJobSA},
			ReviewJobAccounts: []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}},
			Keys:              s.keys,
			JobKeys:           s.jobKeys,
			Outcomes:          RTDBOutcomes{DB: s.db},
			Now:               func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's own verifier configuration is not one NewAuthenticator accepts: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		cfg.ReviewAccounts = []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}}
		cfg.Repos = map[string]string{s.slug: testRepo}
	})
}

// TestAReviewCallerIsRefusedTheProviderRoute: the class fence this route owes
// C3.9's review variant, and it is a hand-built call rather than a hypothetical
// one (D3003): a review job runs the same runner as any other, so it holds its
// own run identity and the job's own ID token and can post here past its
// gateway. What it must not reach is this route's whole credential — a
// repository's own NAMED provider entry — because a review caller is served its
// account's own list and nothing else (candidates reads ClassReview before the
// cascade narrows anything, and reviewCandidates offers the pairing's one
// account and no tier beyond it, so ownNamedEntries is never read for it).
//
// The refusal is asserted the way a hand-built one is above: the typed body, no
// upstream request at all, and exactly one audit line naming no credential —
// the line a verified caller's refusal owes.
//
// The control is the same fixture called by the REPOSITORY class, and it is what
// makes this about the class: that call goes out to the entry's own address and
// answers the upstream's 200, so the state has a key to attach and only the
// caller's class is the reason the review call is refused.
//
// Mutation (run, restore): drop the ClassReview arm from providerCall and the
// review call is forwarded with the repository's own provider key attached,
// answering 200 where this test wants the refusal.
func TestAReviewCallerIsRefusedTheProviderRoute(t *testing.T) {
	s, auditLog := routedReviewSvc(t)
	resp := s.postTokens(t, ProviderRoutePath, routedCallBody(routedModel), s.reviewRunToken(), s.reviewJobToken(), s.job,
		routedName(routedProvider))
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the review caller is answered %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q: the class this route does not serve is the refusal's own type, as it is on the sign route", e.Error, ErrorUnverifiedRunIdentity)
	}
	if !strings.Contains(e.Message, "coding caller") {
		t.Errorf("the refusal is %q, and it has to say which caller this route IS served to: a refusal that names no reason sends the operator to the bundle", e.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Fatalf("the provider saw %d requests, want none: the call was refused before it was forwarded", n)
	}
	got := forwardLines(t, auditLog)
	if len(got) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly 1: %v", len(got), got)
	}
	line := got[0]
	checkAuditShape(t, line)
	if c := auditField(line, "credential"); c != "" {
		t.Errorf("the audit line names the credential %q: no credential was chosen and none was read", c)
	}
	if v := routedValue(t, testRepoPath, routedProvider); strings.Contains(string(body), v) ||
		strings.Contains(auditField(line, "msg"), v) {
		t.Error("the refusal carries the provider key's value")
	}

	// The control: the same state, the same provider and the same model, called
	// by the class this route IS served to.
	t.Run("the coding class is served from the same state", func(t *testing.T) {
		resp, ans := s.callPath(t, ProviderRoutePath, routedCallBody(routedModel), routedName(routedProvider))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the repository caller is answered %d, want the upstream's 200: %s", resp.StatusCode, ans)
		}
		if got := resp.Header.Get(HeaderCredential); got != familyName(t, s.slug, routedProvider) {
			t.Errorf("%s = %q, want the entry the verified repository and the provider derive %q", HeaderCredential, got, familyName(t, s.slug, routedProvider))
		}
		if n := s.up.Count(); n != 1 {
			t.Errorf("the provider saw %d requests, want exactly the control's one", n)
		}
	})
}

// TestARoutedCallLeavesTheRunsPlacementAlone: the provider route JUDGES its entry
// and never places the run on it (creds.Selector.MayServe), and this is the test
// that fails if it places it again.
//
// A run's placement is one fact — its current credential and its lease together
// (D154's stickiness, D142's slot) — and a run mixes the two routes: Claude calls
// go through the cascade to a shared subscription account, routed calls through
// this route to a provider's entry. The entry is DERIVED (the run's verified
// repository and the provider's name, the same one for every such call), so there
// is nothing for the run to be sticky about on this route — while placing it
// would move the run off the account, taking the lease it holds on it away: the
// account's concurrency slot is freed while the run is using it, and the next
// Claude call has no current credential to stay on and re-selects (or waits on
// the cap).
//
// So the fence is the pair of facts either side of the routed call: the cascade's
// own answer is what should have been written, and after a routed call the run is
// still exactly there — same credential, same lease, and not one write of any
// kind (no mark, no placement, no counter) that a placement could hide in.
//
// Mutation (run, restore): call creds.Selector.Select instead of MayServe in
// providerCall and the second half of this test fails with the run placed on the
// named entry and its lease moved with it.
func TestARoutedCallLeavesTheRunsPlacementAlone(t *testing.T) {
	s := routedSvc(t)
	// The routed call needs the provider's answer; the default script holds one.
	s.up.Script = append(s.up.Script, anthropicfake.MessageOK(routedModel, testUsage))

	// A Claude call first, through the cascade: that is the call that places a
	// run, and the credential it places it on is the account's.
	resp, ans := s.callPath(t, MessagesPath, callBody("placed"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the Claude call is answered %d, want %d: %s", resp.StatusCode, http.StatusOK, ans)
	}
	account := resp.Header.Get(HeaderCredential)
	if account == "" {
		t.Fatal("the Claude call's answer names no credential, so this test has no placement to keep")
	}
	if got := resp.Header.Get(HeaderTier); got != TierA {
		t.Fatalf("%s = %q, want %q: the credential a run mixes routes on has to be the shared account this test is about", HeaderTier, got, TierA)
	}
	if placed, ok := s.state.placed(s.credsRun()); !ok || placed != account {
		t.Fatalf("the run is placed on %q (%v), want the cascade's own answer %q", placed, ok, account)
	}
	before := s.state.snapshot(s.credsRun())

	// The routed call, on the same run, the same fixture and the same service.
	rresp, rans := s.callPath(t, ProviderRoutePath, routedCallBody(routedModel), routedName(routedProvider))
	if rresp.StatusCode != http.StatusOK {
		t.Fatalf("the routed call is answered %d, want the upstream's 200: %s", rresp.StatusCode, rans)
	}
	if got, want := rresp.Header.Get(HeaderCredential), familyName(t, s.slug, routedProvider); got != want {
		t.Errorf("%s = %q, want %q: the routed call does not carry the placement's name", HeaderCredential, got, want)
	}

	after := s.state.snapshot(s.credsRun())
	if after.current != account {
		t.Errorf("the run is placed on %q after a routed call, want the account %q it was on: a routed call must not move a run off the credential it holds", after.current, account)
	}
	if after.lease != before.lease || after.leased != before.leased {
		t.Errorf("the run's lease is %+v after a routed call, want the %+v it held: the account's slot (D142) is freed by a move nobody asked for", after.lease, before.lease)
	}
	if after.rr != before.rr {
		t.Errorf("the round-robin counter moved from %d to %d: a judgement of one credential has no tie to break", before.rr, after.rr)
	}
	if after.marks != before.marks {
		t.Errorf("the routed call wrote a mark (%d held, was %d): it attaches a key and answers with what the provider said, and it marks nothing", after.marks, before.marks)
	}
	if n := s.up.Count(); n != 2 {
		t.Errorf("the provider saw %d requests, want the Claude call's one and the routed call's one", n)
	}
}

// placement is the run's state as one reading: what it is placed on, the lease it
// holds with it, and the two other things a selection can write. It is read as a
// whole because the property under test is that NONE of it moved.
type placement struct {
	current string
	lease   creds.Lease
	leased  bool
	rr      int64
	marks   int
}

// snapshot reads the fixture's store for one run. It takes the store's own lock,
// for the reason every writer here does.
func (s *fakeStore) snapshot(run creds.Run) placement {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.leases[runKey(run)]
	return placement{current: s.current[runKey(run)], lease: lease, leased: ok, rr: s.rr, marks: len(s.marks)}
}
