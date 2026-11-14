package credsvc

// The review class's own tests (C3.9a): the third account class, the
// repository table that resolves a run identity's slug to the canonical
// repository a review call is about, and the claim rule that makes a run
// identity a review one (docs/c39-review-identity/spec.md §2, §3).
//
// Everything here is hermetic (Convention 9): the upstream is
// internal/gateway/anthropicfake on loopback, the policy is a decoded
// document, the state is a map, and nothing in this file is, or looks like, a
// credential (Convention 2) — every value is built at run time by fakeCred.
// The service accounts here are names and never tokens.

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

const (
	// testReviewAccount is the Claude account the fixture's review job is
	// served as, by the name the published admissions spell it with: the
	// fixture's bundle holds it, and the fixture's admissions offer it to the
	// fixture's repository.
	testReviewAccount = testAccount
	// testReviewProfile is the profile the fixture's review job is of, and
	// testReviewSA the service account it runs as: the variant's own spelling
	// (ReviewServiceAccountID), which is what makes the account the deployer
	// would create the account this fixture's verifier admits. It is a name and
	// not a credential.
	testReviewProfile = "profile-one"
)

// testReviewSA is the review job's service account, derived the one way the
// deployer derives it.
var testReviewSA = serviceAccountEmail(ReviewServiceAccountID(testReviewProfile, "anthropic", testReviewAccount), testFirebase)

// newReviewSvc is the fixture with the one review job account this class is
// about: the account the caller is verified as, the row that names the Claude
// account a call of it is served by, and the table that resolves the run
// identity's slug.
func newReviewSvc(t *testing.T) *svc {
	t.Helper()
	return newReviewSvcSpelled(t, testReviewSA, testReviewSA)
}

// newReviewSvcSpelled is newReviewSvc with the two lists' own spellings of the
// one service account: the verifier's (AuthConfig.ReviewJobAccounts, which is
// what a presented claim is matched against and what the identity then carries)
// and the pairing's table (Config.ReviewAccounts). A deployment configures them
// together, and the one test that spells them differently is what holds the
// table to the fold the verifier matches by.
func newReviewSvcSpelled(t *testing.T, verifierEmail, tableEmail string, extra ...func(*svc, *Config)) *svc {
	t.Helper()
	return newSvcWith(t, func(s *svc, cfg *Config) {
		auth, err := NewAuthenticator(AuthConfig{
			Project:           testProject,
			FirebaseProject:   testFirebase,
			Audience:          testAudience,
			JobAccounts:       []string{testJobSA},
			Keys:              s.keys,
			JobKeys:           s.jobKeys,
			Outcomes:          RTDBOutcomes{DB: s.db},
			SharedJobAccounts: []SharedJobAccount{sharedRow()},
			ReviewJobAccounts: []ReviewJobAccount{{Email: verifierEmail, Account: testReviewAccount}},
			Now:               func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's own verifier configuration is not one NewAuthenticator accepts: %v", err)
		}
		// The fixture's verifier and the server's are the same object, so a
		// test can call either and be asking the same configuration.
		cfg.Auth, s.auth = auth, auth
		cfg.ReviewAccounts = []ReviewJobAccount{{Email: tableEmail, Account: testReviewAccount}}
		cfg.Repos = map[string]string{s.slug: testRepo}
		for _, m := range extra {
			m(s, cfg)
		}
	})
}

// newAnthropicKeyReviewSvc is the review fixture whose pairing names the
// sentinel instead of a Claude account: the installation publishes
// config.AnthropicKeyAccount in the shared node's accounts block, offers it to
// the fixture's repository, and the repository's own node admits it — so the
// pairing resolves the caller exactly as it resolves an account's name, and the
// credential that name stands for is the bundle's tier-B key.
//
// The verifier's own list is left as it is, and deliberately: the class and the
// email are all verifyJob reads of it (auth.go), while the account name a review
// call is served by is the pairing's table's (server.go's ReviewAccounts). So
// this fixture changes the one spelling that decides what the call is served by
// — which is the same split the two lists have in a deployment, where both are
// derived from one axis (ReviewJobAccountsFor).
func newAnthropicKeyReviewSvc(t *testing.T, extra ...func(*svc, *Config)) *svc {
	t.Helper()
	return newReviewSvcSpelled(t, testReviewSA, testReviewSA, append([]func(*svc, *Config){
		func(s *svc, cfg *Config) {
			cfg.ReviewAccounts = []ReviewJobAccount{{Email: testReviewSA, Account: config.AnthropicKeyAccount}}
			s.policy.shared = decodeTestPolicy(t, testPolicyDocAccounts("auto",
				`"default": {"allow_repos": ["`+testRepo+`"]},
					"`+testAccount+`": {"allow_repos": ["`+testRepo+`"]},
					"`+config.AnthropicKeyAccount+`": {"allow_repos": ["`+testRepo+`"]}`))
			s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc(config.AnthropicKeyAccount))
		},
	}, extra...)...)
}

// reviewMintSvc is the review fixture behind the mint's fake GitHub, with the
// bundle a mint needs: a caller the pairing admits, on the route whose answer
// the class decides, where a refusal cannot be about the identity, the pairing
// or the bundle.
//
// Its repository table holds the installation's OTHER repository beside the one
// the fixture's run identity names, because a mint's repository is only visible
// against a second row: a table of one row answers the run identity's own
// repository whichever row a mint read, so a mint that resolved the wrong one
// would look right. TestReviewMintIsScopedToTheRunIdentitysRepository is what
// reads the two rows apart.
func reviewMintSvc(t *testing.T) (*svc, *ghFake) {
	t.Helper()
	gh := newGHFake(t)
	s := newReviewSvcSpelled(t, testReviewSA, testReviewSA, func(s *svc, cfg *Config) {
		cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
		cfg.Repos[testSlug(t, testOtherRepoPath)] = testOtherRepo
		s.src.docs = [][]byte{mintBundleDoc(t)}
	})
	return s, gh
}

// reviewRunToken is the run identity of a review run: it pushes nothing (no
// fb) and it names the pull requests it reviews (fpr, spec §3.3).
func (s *svc) reviewRunToken(mutate ...func(map[string]any)) string {
	return s.runToken(append([]func(map[string]any){
		func(c map[string]any) { c["fpr"] = []any{7} },
	}, mutate...)...)
}

// reviewJobToken is the caller's Google ID token as the review job's own
// service account.
func (s *svc) reviewJobToken(mutate ...func(map[string]any)) string {
	return s.jobToken(append([]func(map[string]any){
		func(c map[string]any) { c["email"] = testReviewSA },
	}, mutate...)...)
}

// TestReviewAccountIsAdmittedOnTheModelPathForItsOwnAccount is C3.9a's red
// step, and it is the whole of what the class buys on the route a review run
// reaches first.
//
// A review job's account names no repository, so the binding the service makes
// for every other caller (ja.Slug == id.Slug) cannot be made for it: the
// repository is the run identity's own slug resolved through the repository
// table, and the account's admission to it is the pairing. Without the class
// the caller is refused with "the caller is not one of the installation's job
// service accounts", which is the state this task exists to change.
func TestReviewAccountIsAdmittedOnTheModelPathForItsOwnAccount(t *testing.T) {
	s := newReviewSvc(t)
	resp, body := s.callTokens(t, callBody("review-admitted"), s.reviewRunToken(), s.reviewJobToken(), s.job)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the review caller was answered %d, want the upstream's 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != testReviewAccount {
		t.Errorf("%s = %q, want the review job's own account %q", HeaderCredential, got, testReviewAccount)
	}
	if got := resp.Header.Get(HeaderTier); got != TierA {
		t.Errorf("%s = %q, want %q: a review caller is served by a shared account", HeaderTier, got, TierA)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1", n)
	}
	auth := s.up.Seen()[0].Header.Get("Authorization")
	if !strings.Contains(auth, fakeCred(t, testReviewAccount)) {
		t.Error("the call did not carry the account the pairing resolved")
	}
	if strings.Contains(auth, fakeCred(t, "default")) {
		t.Error("the call carried an account the pairing did not resolve")
	}
	if got := s.policy.readAdmissions(); !slices.Equal(got, []string{s.slug}) {
		t.Errorf("the admissions read were %v, want just the run identity's repository", got)
	}
}

// TestReviewPairingMayNameTheProjectsAnthropicKey is AK4's red step: a review
// caller whose pairing names the sentinel is served by the bundle's tier-B
// entry, and the forwarded call carries the project's own key to Anthropic's
// own address.
//
// Before the change the pairing's name resolved to a tier-A account and to
// nothing else (reviewCandidates), so a pairing that named the sentinel had no
// credential to offer at all: the caller was answered with the typed wait and
// the upstream saw nothing — F13's state, and the state a review run whose
// project holds no Claude account would be left in for good.
//
// The two accounts the bundle also holds are the strongest part of this: both
// are tier A, both are admitted to the fixture's repository, and the fixture's
// policy is `fallback: auto`, so a service that treated the sentinel as one
// name among the accounts would answer with one of them instead of the key.
func TestReviewPairingMayNameTheProjectsAnthropicKey(t *testing.T) {
	s := newAnthropicKeyReviewSvc(t)
	resp, body := s.callTokens(t, callBody("review-project-key"), s.reviewRunToken(), s.reviewJobToken(), s.job)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the review caller was answered %d, want the upstream's 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != config.AnthropicSecret {
		t.Errorf("%s = %q, want the project's own key %q", HeaderCredential, got, config.AnthropicSecret)
	}
	if got := resp.Header.Get(HeaderTier); got != TierB {
		t.Errorf("%s = %q, want %q: the project's key is real money", HeaderTier, got, TierB)
	}
	if got := resp.Header.Get(HeaderSwitch); got != "" {
		t.Errorf("%s = %q, want none: a review call is served by one credential or by none", HeaderSwitch, got)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1", n)
	}
	req := s.up.Seen()[0]
	if got := req.Header.Get("x-api-key"); got != fakeCred(t, "paid") {
		t.Error("the call did not carry the project's key")
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Error("the call carried an Authorization header a tier-B call must not have")
	}
	if got := s.policy.readAdmissions(); !slices.Equal(got, []string{s.slug}) {
		t.Errorf("the admissions read were %v, want just the run identity's repository", got)
	}

	// The key reaches Anthropic and nowhere else: the destination is read off
	// the request the service built, before the fixture's host rewriter would
	// have made every host look the same (forward_test's recordingTransport).
	t.Run("the call is built for Anthropic's own endpoint", func(t *testing.T) {
		rec := &recordingTransport{}
		r := newAnthropicKeyReviewSvc(t, func(_ *svc, cfg *Config) {
			cfg.Client = &http.Client{Transport: rec}
		})
		resp, _ := r.callTokens(t, callBody("review-project-key-destination"), r.reviewRunToken(), r.reviewJobToken(), r.job)
		if resp.StatusCode == http.StatusOK {
			t.Fatal("the answer is 200: no request may leave a recording transport")
		}
		urls := rec.seen()
		if len(urls) != 1 {
			t.Fatalf("the service built %d requests, want 1: %v", len(urls), urls)
		}
		if want := gateway.AnthropicBase(gateway.Upstream{}) + MessagesPath; urls[0] != want {
			t.Errorf("the call was built for %q, want %q: the project's key goes to Anthropic's own endpoint or nowhere", urls[0], want)
		}
	})
}

// TestReviewRowsCarryEachAxisValueOfItsOwn is AK6a's half of this file: a
// review row's Account is the axis value the job it names was deployed under,
// and the axis's two non-account values are two values with two rows — one
// credential each, which is what the pairing looks a caller up under and what
// the offer rule then matches by name and tier.
//
// The rows are derived over the published policy alone (ReviewJobAccountsFor
// reads config.ProjectJobAccounts), so nothing here needs a bundle entry: the
// provider key's own credential is AK6b's bundle key, and until it exists a
// caller paired with this value is offered nothing at all (offeredToReview
// answers it by name and tier, one credential, no fallback) — the fail-closed
// state, not a second credential borrowed from the sentinel.
//
// Mutation (run, restore): let the provider key ride config.AnthropicKeyAccount
// — the value is the admission unit, so both rows are derived over the
// sentinel's variant account segment and the two collapse into one
// (slices.Compact); and the profile's own list then holds the same value twice,
// which is the state that makes a repository admitted to one credential the one
// the other's job is not the one for.
func TestReviewRowsCarryEachAxisValueOfItsOwn(t *testing.T) {
	doc := `{
		"version": 5,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `"],
		"accounts": {
			"` + config.AnthropicKeyAccount + `": {"allow_repos": ["` + testRepo + `"]},
			"` + config.ProviderKeyAccount + `": {"allow_repos": ["` + testRepo + `"]}
		},
		"profiles": {"` + projectpolicy.DefaultProfileName + `": {"provider": "github", "cpu": 2, "memory": "8Gi", "timeout_class": "6h",
			"allow_repos": ["` + testRepo + `"],
			"claude_accounts": ["` + config.AnthropicKeyAccount + `", "` + config.ProviderKeyAccount + `"]}},
		"credentials": {"fallback": "auto"}
	}`
	p := decodeTestPolicy(t, doc)
	got := ReviewJobAccountsFor(p, testFirebase)
	// The want is written out rather than derived through the function under
	// test's own helper, so that a value mapped onto the other's account
	// segment shows up as the collapse it is: the two rows would be one.
	want := []ReviewJobAccount{
		{
			// The profile's own no-account job, which every profile keeps and
			// whose row the admissions spell as the default account.
			Email:   serviceAccountEmail(ReviewServiceAccountID(projectpolicy.DefaultProfileName, "github", config.DefaultClaudeAccount), testFirebase),
			Account: config.DefaultClaudeAccount,
		},
		{
			Email:   serviceAccountEmail(ReviewServiceAccountID(projectpolicy.DefaultProfileName, "github", config.AnthropicKeyAccount), testFirebase),
			Account: config.AnthropicKeyAccount,
		},
		{
			Email:   serviceAccountEmail(ReviewServiceAccountID(projectpolicy.DefaultProfileName, "github", config.ProviderKeyAccount), testFirebase),
			Account: config.ProviderKeyAccount,
		},
	}
	slices.SortFunc(want, func(a, b ReviewJobAccount) int { return cmp.Compare(a.Email, b.Email) })
	if !slices.Equal(got, want) {
		t.Fatalf("ReviewJobAccountsFor = %v, want one row per axis value, %v", got, want)
	}
}

// TestReviewAccountIsRefusedTheModelPathForAnotherAccount: the pairing is the
// class's, and it admits the caller for its own account only. The repository's
// published node admits the default account and not the one the review job is
// of, so the account that serves this call is not the one the pairing resolved
// — and there is no other account to be served by, which is the whole of what
// "for its own account only" means on this route.
func TestReviewAccountIsRefusedTheModelPathForAnotherAccount(t *testing.T) {
	s := newReviewSvc(t)
	s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc("default"))
	resp, body := s.callTokens(t, callBody("review-other-account"), s.reviewRunToken(), s.reviewJobToken(), s.job)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q: %s", got.Error, ErrorUnverifiedRunIdentity, got.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: the caller was refused before a credential was chosen", n)
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("the bundle was read %d times, want none: the pairing comes first", n)
	}
}

// TestReviewCallerOnAnUnpublishedRepositoryIsRefused: the pairing reads what
// the installation publishes, and a repository the published policy does not
// admit it refuses rather than guesses (F13). Every step below is a refusal
// and never a fallback, and the last one is the fail-closed rule the model
// path already follows: a node that cannot be read is not an empty node.
func TestReviewCallerOnAnUnpublishedRepositoryIsRefused(t *testing.T) {
	t.Run("the repository's node is not published", func(t *testing.T) {
		s := newReviewSvc(t)
		delete(s.policy.admissions, s.slug)
		resp, body := s.callTokens(t, callBody("review-unpublished"), s.reviewRunToken(), s.reviewJobToken(), s.job)
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
		}
		if n := s.up.Count(); n != 0 {
			t.Errorf("the upstream saw %d requests, want none", n)
		}
	})
	t.Run("the project's allow-list does not name the repository", func(t *testing.T) {
		// The gap `fugaro project repos rm` leaves: the repository's own node
		// and the account's allow_repos still name it, and the project does
		// not. Reading the child node alone would keep admitting it.
		s := newReviewSvc(t)
		s.policy.shared = decodeTestPolicy(t, testPolicyDocAllowRepos(
			`["`+testOtherRepo+`"]`,
			`"default": {"allow_repos": ["`+testRepo+`", "`+testOtherRepo+`"]},
			"`+testAccount+`": {"allow_repos": ["`+testRepo+`", "`+testOtherRepo+`"]}`))
		resp, body := s.callTokens(t, callBody("review-withdrawn"), s.reviewRunToken(), s.reviewJobToken(), s.job)
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
		}
		if n := s.up.Count(); n != 0 {
			t.Errorf("the upstream saw %d requests, want none", n)
		}
	})
	t.Run("the repository's node cannot be read", func(t *testing.T) {
		s := newReviewSvc(t)
		s.policy.admErr = errors.New("the database refused the read")
		resp, body := s.callTokens(t, callBody("review-unreadable"), s.reviewRunToken(), s.reviewJobToken(), s.job)
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d: a node that cannot be read is a refusal, not an empty admission: %s", resp.StatusCode, StatusUnavailable, body)
		}
		if got := refusal(t, resp.StatusCode, body); got.Error != ErrorStateUnavailable {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
		}
		if n := s.up.Count(); n != 0 {
			t.Errorf("the upstream saw %d requests, want none", n)
		}
	})
}

// TestReviewTableIsKeyedTheWayTheVerifierKeysItsOwn: the verifier's list and
// the pairing's table are two lists, and the account Verify answers with is the
// spelling the VERIFIER'S list carries — so a deployment whose two lists spell
// the same address in different cases would have a caller the verifier admits
// and the pairing cannot look up, which is a refusal no operator could read.
//
// Both directions are covered, because the two sides are looked up in opposite
// ones: the table is keyed by the folded email (NewServer), and the account the
// pairing looks up is the verifier's own spelling, so it is folded on the way
// in (Server.reviewAccount). Either fold alone leaves the other direction
// missing — the first subtest passes with only the key folded, the second only
// with the lookup folded.
func TestReviewTableIsKeyedTheWayTheVerifierKeysItsOwn(t *testing.T) {
	loud := strings.ToUpper(testReviewSA)
	if loud == testReviewSA {
		t.Fatal("the fixture's own service account is already upper case, so this test would cover nothing")
	}
	for _, tc := range []struct {
		name                      string
		verifierEmail, tableEmail string
	}{
		{"the table's list is the loud one", testReviewSA, loud},
		{"the verifier's list is the loud one", loud, testReviewSA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newReviewSvcSpelled(t, tc.verifierEmail, tc.tableEmail)
			resp, body := s.callTokens(t, callBody("review-case-folded"), s.reviewRunToken(), s.reviewJobToken(), s.job)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the review caller was answered %d, want the upstream's 200: %s", resp.StatusCode, body)
			}
			if got := resp.Header.Get(HeaderCredential); got != testReviewAccount {
				t.Errorf("%s = %q, want the review job's own account %q", HeaderCredential, got, testReviewAccount)
			}
		})
	}
}

// TestRepositoryTableResolvesASlugToItsCanonicalName: the table the pairing
// resolves the run identity's slug through is the per-repository binding table
// read the other way round, so a service cannot bind an account one way and
// resolve a slug another. Its keys and values are the two derivations the run
// identity and the mint already use.
func TestRepositoryTableResolvesASlugToItsCanonicalName(t *testing.T) {
	table, err := RepositoryTableFor(testProject, testFirebase, "github", []RepoJob{
		{Repo: testRepoPath, Workflow: "web"},
		{Repo: testOtherRepoPath, Workflow: "web"},
	})
	if err != nil {
		t.Fatalf("RepositoryTableFor: %v", err)
	}
	want := map[string]string{
		testSlug(t, testRepoPath):      testRepo,
		testSlug(t, testOtherRepoPath): testOtherRepo,
	}
	if len(table) != len(want) {
		t.Fatalf("the table holds %d rows, want %d: %v", len(table), len(want), table)
	}
	for slug, canonical := range want {
		if got := table[slug]; got != canonical {
			t.Errorf("table[%q] = %q, want %q", slug, got, canonical)
		}
	}
}

// TestRepositoryTableRefusesAnUnknownSlug: the table is the only place a slug
// becomes a repository name, and a slug it does not hold is refused (D1066,
// F4). The refusal is the pairing's, and it happens before the bundle is read:
// a canonical name guessed from the slug's own text would be a repository the
// installation's policy was never asked about.
func TestRepositoryTableRefusesAnUnknownSlug(t *testing.T) {
	s := newReviewSvc(t)
	otherSlug := testSlug(t, testOtherRepoPath)
	if _, ok := s.server.cfg.Repos[otherSlug]; ok {
		t.Fatal("the fixture's own table holds the slug this test means to leave out")
	}
	resp, body := s.callTokens(t, callBody("review-unknown-slug"),
		s.reviewRunToken(func(c map[string]any) {
			c["fs"] = otherSlug
			c["sub"] = "r~" + otherSlug + "~" + s.run
			c["user_id"] = c["sub"]
		}), s.reviewJobToken(), s.job)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("the bundle was read %d times, want none", n)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none", n)
	}
}

// TestReviewIdentityRequiresNoBranchAndAtLeastOnePR: the claim rule (spec
// §3.3) admits exactly the shape a review run has — it pushes nothing and it
// reviews at least one pull request — and the rule is the review class's: a
// per-repository caller with neither claim is admitted as it is today (F2).
func TestReviewIdentityRequiresNoBranchAndAtLeastOnePR(t *testing.T) {
	s := newReviewSvc(t)
	id, err := s.verify(s.reviewRunToken(), s.reviewJobToken(), s.job)
	if err != nil {
		t.Fatalf("Verify refused a review run: %v", err)
	}
	if id.JobClass != ClassReview {
		t.Errorf("class = %q, want %q", id.JobClass, ClassReview)
	}
	if id.JobAccount != testReviewSA {
		t.Errorf("job account = %q, want %q", id.JobAccount, testReviewSA)
	}
	if id.Branch != "" || len(id.PRs) != 1 || id.PRs[0] != 7 {
		t.Errorf("identity = branch %q, prs %v, want no branch and the one pull request", id.Branch, id.PRs)
	}

	repo := newSvc(t)
	other, err := repo.verify(repo.runToken(), repo.jobToken(), repo.job)
	if err != nil {
		t.Fatalf("Verify refused a per-repository run with neither claim: %v", err)
	}
	if other.JobClass != ClassRepository {
		t.Errorf("class = %q, want %q", other.JobClass, ClassRepository)
	}
}

// TestReviewIdentityWithABranchIsRefused: a coding run launched into the
// review job is refused, never downgraded to a review. Its identity is a
// different act — it means to push — and answering it as a review would hand
// the read-only set to it.
//
// The refusal is the pairing's last step (spec §3.2 step 4), and it is asked
// on the HTTP path for that reason: both identities are whole by then, so the
// area rule holds the refusal to exactly one audit line. A claim rule asked
// inside Verify would refuse a caller the service has no identity to write a
// line about — zero lines, on a call whose two identities were both verified.
func TestReviewIdentityWithABranchIsRefused(t *testing.T) {
	s := newReviewSvc(t)
	fb := "fugaro/" + s.run
	resp, body := s.callTokens(t, callBody("review-with-branch"),
		s.reviewRunToken(func(c map[string]any) { c["fb"] = fb }), s.reviewJobToken(), s.job)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	got := refusal(t, resp.StatusCode, body)
	if got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	if !strings.Contains(got.Message, "branch") {
		t.Errorf("the refusal is %q, want it to name the claim that failed", got.Message)
	}
	if strings.Contains(got.Message, fb) {
		t.Errorf("the refusal %q echoes the branch: an fb is caller text", got.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none", n)
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("the bundle was read %d times, want none: the claim rule comes before it", n)
	}
	if lines := auditLines(t, s.audit); len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
	}
}

// TestReviewIdentityWithAnEmptyPRListIsRefused: an artifact run pushes nothing
// and writes no pull request either, so the empty fpr is what tells it from a
// review — and an fpr that is present and empty is a shape the claim reader
// admits, which is why the rule is checked here and not there. It is the same
// refusal as the branch's, answered the same way and recorded the same way.
func TestReviewIdentityWithAnEmptyPRListIsRefused(t *testing.T) {
	s := newReviewSvc(t)
	resp, body := s.callTokens(t, callBody("review-no-prs"),
		s.reviewRunToken(func(c map[string]any) { c["fpr"] = []any{} }), s.reviewJobToken(), s.job)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	got := refusal(t, resp.StatusCode, body)
	if got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	if !strings.Contains(got.Message, "pull request") {
		t.Errorf("the refusal is %q, want it to name the claim that failed", got.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none", n)
	}
	if lines := auditLines(t, s.audit); len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
	}
}

// TestReviewMintServesTheReadOnlySetForTheRunsRepository is C3.9b's red step:
// the mint admits the review class and serves it CSR1's read-only set, where
// before this task the class was refused this route whole.
//
// The caller here is otherwise perfectly good: its two identities are whole,
// the pairing admits it, and the bundle holds the App key a mint is issued
// from — so the class decides the SET it is served and the pairing decides the
// repository. What it must NEVER be served is github.RunTokenPermissions, the
// WRITE set of the very repository the review is about: the review job mounts
// no repository and holds no installation token of its own (O1), and this
// endpoint is where that would be undone.
//
// The fixture's table holds a second repository (reviewMintSvc), so the
// repository this mint is scoped to is a name the answer can be wrong about
// rather than the table's only row: the run identity's own repository is
// asserted here, and TestReviewMintIsScopedToTheRunIdentitysRepository asks the
// same question of the table's other row.
func TestReviewMintServesTheReadOnlySetForTheRunsRepository(t *testing.T) {
	s, gh := reviewMintSvc(t)
	resp, body := s.mintTokens(t, mintBody(testAppID), s.reviewRunToken(), s.reviewJobToken(), s.job)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the review caller was answered %d, want 200: %s", resp.StatusCode, body)
	}
	ans := mintAnswer(t, body)
	if ans.Token != gh.token || !ans.ExpiresAt.Equal(ghExpiry) {
		t.Errorf("the answer = %+v, want the fake's token expiring %s", ans, ghExpiry)
	}
	// The repository the token is FOR is the pairing's (D1066, D893 (c)): the
	// caller has no field to name one in, and the answer's own field is what
	// tells the run which repository its token reaches.
	if ans.Repository != testRepo {
		t.Errorf("the answer's repository is %q, want the run identity's own %q", ans.Repository, testRepo)
	}
	lookup, mint := gh.both(t)
	if want := "/repos/" + testRepoPath + "/installation"; lookup.path != want {
		t.Errorf("the installation lookup was %s, want the run identity's own repository", lookup.path)
	}
	var sent struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(mint.body), &sent); err != nil {
		t.Fatalf("the mint's body is not the JSON this test reads: %v (%s)", err, mint.body)
	}
	if maps.Equal(sent.Permissions, github.RunTokenPermissions()) {
		t.Errorf("the review's mint asked GitHub for the run's own set %v: that is the push and the pull request the reviewing run must not be able to make, and D883 exists to remove exactly this exposure", sent.Permissions)
	}
	if want := github.ReviewTokenPermissions(); !maps.Equal(sent.Permissions, want) {
		t.Errorf("the review's mint asked GitHub for %v, want CSR1's read-only set %v", sent.Permissions, want)
	}
	// The table holds a second repository, so "aurora" is the row the run
	// identity resolved to and not the only name the lookup could have answered.
	if len(sent.Repositories) != 1 || sent.Repositories[0] != "aurora" {
		t.Errorf("the mint asked for repositories %v, want the run identity's own alone", sent.Repositories)
	}
	// One line per act, the mint's own purpose, the App key's name and no
	// value: the issuance a review run's token is (C1.13, D1065).
	lines := mintAuditLines(t, s)
	if len(lines) != 1 {
		t.Fatalf("the review's mint wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
	}
	for field, want := range map[string]any{
		"purpose":    PurposeMint,
		"credential": config.GitHubAppKeySecret,
		"status":     StatusOK,
		"run":        s.run,
		"repository": s.slug,
		"caller":     testReviewSA,
		"tier":       "",
	} {
		if got := lines[0][field]; got != want {
			t.Errorf("the line records %s = %v, want %q", field, got, want)
		}
	}
}

// TestReviewMintIsScopedToTheRunIdentitysRepository: a review caller's token is
// minted for the repository its RUN IDENTITY names, resolved through the
// installation's table (D1066) — and the table holds a second repository, so
// the same call is made for each of its rows and each answer must name the one
// the caller's run belongs to.
//
// This is the test the fixture's second row exists for. Against a table of one
// row the mint in TestReviewMintServesTheReadOnlySetForTheRunsRepository could
// not have been seen resolving the wrong one: the row it read and the row it
// should have read were the same. Here the two rows are asked for in turn, and
// a mint that answered the other repository names it in the answer, in the
// installation lookup and in the scope GitHub is asked to mint within.
//
// The account is admitted to both repositories — the fixture's shared node
// offers it both and each repository's own node names it — so the repository is
// decided by the run identity and by nothing the caller sent, which is the
// property the mint's whole shape rests on: MintRequest has one field and no
// repository in it.
func TestReviewMintIsScopedToTheRunIdentitysRepository(t *testing.T) {
	for _, tc := range []struct {
		name      string
		repoPath  string
		canonical string
		minted    string
	}{
		{"the fixture's own repository", testRepoPath, testRepo, "aurora"},
		{"the table's other repository", testOtherRepoPath, testOtherRepo, "borealis"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh := reviewMintSvc(t)
			slug := testSlug(t, tc.repoPath)
			// The fixture's admissions hold a node for the repository its own run
			// is of; the other row gets one naming the same account, so the two
			// calls differ in nothing but the repository the run identity names.
			s.policy.admissions[slug] = decodeTestChild(t, testChildDoc("default", testAccount))
			run := s.reviewRunToken(func(c map[string]any) {
				c["fs"] = slug
				c["sub"] = "r~" + slug + "~" + s.run
				c["user_id"] = c["sub"]
			})
			resp, body := s.mintTokens(t, mintBody(testAppID), run, s.reviewJobToken(), s.job)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
			}
			if ans := mintAnswer(t, body); ans.Repository != tc.canonical {
				t.Errorf("the answer's repository is %q, want the run identity's own %q", ans.Repository, tc.canonical)
			}
			lookup, mint := gh.both(t)
			if want := "/repos/" + tc.repoPath + "/installation"; lookup.path != want {
				t.Errorf("the installation lookup was %s, want the run identity's own repository %s", lookup.path, want)
			}
			var sent struct {
				Repositories []string `json:"repositories"`
			}
			if err := json.Unmarshal([]byte(mint.body), &sent); err != nil {
				t.Fatalf("the mint's body is not the JSON this test reads: %v (%s)", err, mint.body)
			}
			if want := []string{tc.minted}; !slices.Equal(sent.Repositories, want) {
				t.Errorf("the mint asked GitHub for repositories %v, want the run identity's own %v alone", sent.Repositories, want)
			}
			// The line names the run's own slug and never the canonical name, as
			// every mint line does (C1.13): the two spellings are different
			// strings and the line carries the one the run's record is keyed by.
			if lines := mintAuditLines(t, s); len(lines) != 1 || lines[0]["repository"] != slug {
				t.Errorf("the mint's audit line = %v, want exactly one naming the run's own slug %q", lines, slug)
			}
		})
	}
}

// TestReviewMintRefusesARunIdentityWithABranch: a coding run launched into the
// review job is refused at the mint and never downgraded to a review, because
// the identity it carries is a different act — it means to push — and answering
// it as a review would serve it the read-only set.
//
// The refusal is the corresponding one of the mint's two decisions (D1065): the
// endpoint stops, at last, refusing the class outright, and what is left in
// front of it is the pairing — the claim rule included — so the sentence the
// caller gets is the claim rule's own, naming the claim that failed.
func TestReviewMintRefusesARunIdentityWithABranch(t *testing.T) {
	s, gh := reviewMintSvc(t)
	fb := "fugaro/" + s.run
	run := s.reviewRunToken(func(c map[string]any) { c["fb"] = fb })
	resp, body := s.mintTokens(t, mintBody(testAppID), run, s.reviewJobToken(), s.job)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	got := refusal(t, resp.StatusCode, body)
	if got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	if !strings.Contains(got.Message, "branch") {
		t.Errorf("the refusal is %q, want the claim rule's own sentence: a caller refused for its shape has to be told which claim failed", got.Message)
	}
	if strings.Contains(got.Message, fb) {
		t.Errorf("the refusal %q echoes the branch: an fb is caller text", got.Message)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("GitHub saw %d requests, want none: no installation token was asked for", n)
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("the bundle was read %d times, want none: the claim rule comes before it", n)
	}
	if lines := mintAuditLines(t, s); len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
	}

	// The chooser asks the same rule, and it has to: it is the function that
	// picks the set, and the class alone does not carry the shape. What it must
	// never do is answer a review-class identity the run's write set, and what
	// it must never do either is serve the run's set by default to a class this
	// service does not mint for. Through the HTTP path above neither is
	// reachable — the pairing refuses first — so the calls below are the
	// chooser itself. A shared coding caller is NOT one of those classes (B5.1):
	// it is served the run's own set, and the test that holds that is
	// TestSharedCodingCallerServedForAProfileRepository.
	shape, err := s.verify(run, s.reviewJobToken(), s.job)
	if err != nil {
		t.Fatalf("the fixture's own identity is not one Verify answers: %v", err)
	}
	for _, tc := range []struct {
		name string
		id   Identity
	}{
		{"a review identity carrying a branch", shape},
		{"a class this service does not mint for", Identity{Slug: s.slug, Run: s.run, JobAccount: testSharedSA}},
	} {
		perms, err := mintPermissions(tc.id)
		if err == nil {
			t.Errorf("mintPermissions served the set %v to %s", perms, tc.name)
			continue
		}
		if !errors.Is(err, ErrUnverified) {
			t.Errorf("the refusal for %s is %v, want an ErrUnverified", tc.name, err)
		}
		if perms != nil {
			t.Errorf("the refusal for %s carried a set: %v", tc.name, perms)
		}
	}
}

// TestReviewMintRefusesARunIdentityWithNoPullRequests: an artifact run pushes
// nothing and writes no pull request either, so the empty fpr is what tells it
// from a review — and an fpr that is present and empty is a shape the claim
// reader admits, which is why the rule is asked at the pairing and not read off
// the claim. It is the branch's refusal in every other respect.
func TestReviewMintRefusesARunIdentityWithNoPullRequests(t *testing.T) {
	s, gh := reviewMintSvc(t)
	resp, body := s.mintTokens(t, mintBody(testAppID),
		s.reviewRunToken(func(c map[string]any) { c["fpr"] = []any{} }), s.reviewJobToken(), s.job)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	got := refusal(t, resp.StatusCode, body)
	if got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	if !strings.Contains(got.Message, "pull request") {
		t.Errorf("the refusal is %q, want it to name the claim that failed", got.Message)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("GitHub saw %d requests, want none: no installation token was asked for", n)
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("the bundle was read %d times, want none: the claim rule comes before it", n)
	}
	if lines := mintAuditLines(t, s); len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
	}
}

// TestReviewMintRefusesARepositoryTheAccountIsNotAdmittedFor: D1064's third
// step, read on the route this task opens. The repository is the run identity's
// own slug resolved through the table, and the calling account's admission to
// it is the model path's own check — the intersection of the repository's node,
// the shared node's offer and the two allow-lists — so a review job whose
// account the owner has not admitted to that repository is refused rather than
// served a token for it.
//
// Both halves are here because both are the same intersection, and each alone
// is a way the repository can be withdrawn: the child node that does not name
// the account, and the project's allow-list that has dropped the repository
// (`fugaro project repos rm`, which leaves the child untouched).
func TestReviewMintRefusesARepositoryTheAccountIsNotAdmittedFor(t *testing.T) {
	t.Run("the repository's node does not name the account", func(t *testing.T) {
		s, gh := reviewMintSvc(t)
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc("default"))
		resp, body := s.mintTokens(t, mintBody(testAppID), s.reviewRunToken(), s.reviewJobToken(), s.job)
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
		}
		got := refusal(t, resp.StatusCode, body)
		if got.Error != ErrorUnverifiedRunIdentity {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
		}
		if !strings.Contains(got.Message, "admitted") {
			t.Errorf("the refusal is %q, want it to name the admission that failed", got.Message)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("GitHub saw %d requests, want none: no installation token was asked for", n)
		}
		if n := s.src.readCount(); n != 0 {
			t.Errorf("the bundle was read %d times, want none: the pairing comes before it", n)
		}
		if lines := mintAuditLines(t, s); len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
		}
	})
	t.Run("the project's allow-list does not name the repository", func(t *testing.T) {
		s, gh := reviewMintSvc(t)
		s.policy.shared = decodeTestPolicy(t, testPolicyDocAllowRepos(
			`["`+testOtherRepo+`"]`,
			`"default": {"allow_repos": ["`+testRepo+`", "`+testOtherRepo+`"]},
			"`+testAccount+`": {"allow_repos": ["`+testRepo+`", "`+testOtherRepo+`"]}`))
		resp, body := s.mintTokens(t, mintBody(testAppID), s.reviewRunToken(), s.reviewJobToken(), s.job)
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("GitHub saw %d requests, want none", n)
		}
		if n := s.src.readCount(); n != 0 {
			t.Errorf("the bundle was read %d times, want none: the pairing comes before it", n)
		}
	})
}

// TestReviewMintRefusesBeforeReadingTheBundle: the refusal order is part of the
// contract, and it is the code's to fix: unverified identities, the class the
// mint does not admit, the pairing (the claim rule with it), the stop flag, the
// bundle, and the mint. Two facts hold it on this route, and neither is about
// the text of a refusal.
//
// The first is that the pairing is asked BEFORE the stop flag: a caller whose
// claim rule fails is answered the pairing's refusal even while the operator's
// switch is on, which is the order ServeHTTP makes the two in and not a second
// arrangement for the new class. The second is that a caller the pairing admits
// is refused by the switch with the bundle unread — the switch is read before
// the one document that holds every credential, exactly as the comment on the
// order says — so a stopped service reads no key material at all.
func TestReviewMintRefusesBeforeReadingTheBundle(t *testing.T) {
	t.Run("the pairing is asked before the stop flag", func(t *testing.T) {
		s, gh := reviewMintSvc(t)
		s.kills.global = true
		resp, body := s.mintTokens(t, mintBody(testAppID),
			s.reviewRunToken(func(c map[string]any) { c["fb"] = "fugaro/" + s.run }), s.reviewJobToken(), s.job)
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the answer is %d, want %d: the pairing comes first, so the switch is not what refuses this caller: %s", resp.StatusCode, StatusUnverified, body)
		}
		got := refusal(t, resp.StatusCode, body)
		if got.Error != ErrorUnverifiedRunIdentity {
			t.Errorf("the refusal is %q, want %q: the stop flag's state_unavailable would mean the order is the other way round", got.Error, ErrorUnverifiedRunIdentity)
		}
		if !strings.Contains(got.Message, "branch") {
			t.Errorf("the refusal is %q, want the claim rule's own sentence", got.Message)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("GitHub saw %d requests, want none", n)
		}
		if n := s.src.readCount(); n != 0 {
			t.Errorf("the bundle was read %d times, want none: the pairing comes before the switch and before the bundle", n)
		}
		if lines := mintAuditLines(t, s); len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
		}
	})
	t.Run("the stop flag is answered with the bundle unread", func(t *testing.T) {
		s, gh := reviewMintSvc(t)
		s.kills.repos[s.slug] = true
		resp, body := s.mintTokens(t, mintBody(testAppID), s.reviewRunToken(), s.reviewJobToken(), s.job)
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		got := refusal(t, resp.StatusCode, body)
		if got.Error != ErrorStateUnavailable {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("GitHub saw %d requests, want none: a stopped service mints nothing", n)
		}
		if n := s.src.readCount(); n != 0 {
			t.Errorf("the bundle was read %d times, want none: the switch is read before the one document that holds every credential", n)
		}
		if lines := mintAuditLines(t, s); len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
		}
	})
}

// TestMintRefusalsAreAuditedAndCarryNoToken: every refusal this endpoint
// answers is recorded exactly once (C1.13) and hands out no token — the two
// properties the endpoint is held to whichever class called it, swept over the
// refusals C3.9b is about and over the ones it has to leave alone.
//
// The sweep is a table because the property is the same for all of them and
// what differs is only how the caller failed: the review class's two refusals
// on this route (the claim rule and the repository's admission), the repository
// class's binding refusal, and the endpoint's own refusal of a body it cannot
// read. Each is an answer the run halts on, so each one's line is the only
// record that the act was asked for at all.
func TestMintRefusalsAreAuditedAndCarryNoToken(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		errType string
		call    func(*testing.T) (*svc, *ghFake, *http.Response, []byte)
	}{
		{
			name:    "a review identity carrying a branch",
			status:  StatusUnverified,
			errType: ErrorUnverifiedRunIdentity,
			call: func(t *testing.T) (*svc, *ghFake, *http.Response, []byte) {
				s, gh := reviewMintSvc(t)
				resp, body := s.mintTokens(t, mintBody(testAppID),
					s.reviewRunToken(func(c map[string]any) { c["fb"] = "fugaro/" + s.run }), s.reviewJobToken(), s.job)
				return s, gh, resp, body
			},
		},
		{
			name:    "a review identity naming no pull request",
			status:  StatusUnverified,
			errType: ErrorUnverifiedRunIdentity,
			call: func(t *testing.T) (*svc, *ghFake, *http.Response, []byte) {
				s, gh := reviewMintSvc(t)
				resp, body := s.mintTokens(t, mintBody(testAppID),
					s.reviewRunToken(func(c map[string]any) { c["fpr"] = []any{} }), s.reviewJobToken(), s.job)
				return s, gh, resp, body
			},
		},
		{
			name:    "a review account not admitted to the run's repository",
			status:  StatusUnverified,
			errType: ErrorUnverifiedRunIdentity,
			call: func(t *testing.T) (*svc, *ghFake, *http.Response, []byte) {
				s, gh := reviewMintSvc(t)
				s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc("default"))
				resp, body := s.mintTokens(t, mintBody(testAppID), s.reviewRunToken(), s.reviewJobToken(), s.job)
				return s, gh, resp, body
			},
		},
		{
			name:    "a run identity that is not the calling job's",
			status:  StatusUnverified,
			errType: ErrorUnverifiedRunIdentity,
			call: func(t *testing.T) (*svc, *ghFake, *http.Response, []byte) {
				s, gh := mintSvc(t, nil)
				other := testSlug(t, testOtherRepoPath)
				run := s.runToken(func(c map[string]any) {
					c["fs"] = other
					c["sub"] = "r~" + other + "~" + s.run
					c["user_id"] = c["sub"]
				})
				resp, body := s.mintTokens(t, mintBody(testAppID), run, s.jobToken(), s.job)
				return s, gh, resp, body
			},
		},
		{
			name:    "a body this contract cannot read",
			status:  StatusUnavailable,
			errType: ErrorStateUnavailable,
			call: func(t *testing.T) (*svc, *ghFake, *http.Response, []byte) {
				s, gh := reviewMintSvc(t)
				resp, body := s.mintTokens(t, mintBody("not-a-number"), s.reviewRunToken(), s.reviewJobToken(), s.job)
				return s, gh, resp, body
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, gh, resp, body := tc.call(t)
			if resp.StatusCode != tc.status {
				t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, tc.status, body)
			}
			if got := refusal(t, resp.StatusCode, body); got.Error != tc.errType {
				t.Errorf("the refusal is %q, want %q", got.Error, tc.errType)
			}
			var ans MintResponse
			if err := decodeStrict(body, &ans); err == nil {
				t.Errorf("the refusal carries a mint's answer: %+v", ans)
			}
			for _, secret := range []string{gh.token, pemCore(t)} {
				if strings.Contains(string(body), secret) {
					t.Errorf("the refusal's body carries %q", secret)
				}
				if strings.Contains(s.audit.String(), secret) {
					t.Errorf("the refusal's audit line carries %q", secret)
				}
				if strings.Contains(s.logs.String(), secret) {
					t.Errorf("the refusal wrote %q to the service's log", secret)
				}
			}
			if n := gh.count(); n != 0 {
				t.Errorf("GitHub saw %d requests for a refused mint, want none", n)
			}
			if n := s.src.readCount(); n != 0 {
				t.Errorf("the bundle was read %d times, want none: every refusal above is answered before it", n)
			}
			lines := mintAuditLines(t, s)
			if len(lines) != 1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
			}
			checkAuditShape(t, lines[0])
			for field, want := range map[string]any{
				"purpose":    PurposeMint,
				"credential": config.GitHubAppKeySecret,
				"status":     tc.errType,
				"run":        s.run,
				"tier":       "",
			} {
				if got := lines[0][field]; got != want {
					t.Errorf("the refusal's line records %s = %v, want %q", field, got, want)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The coding half of the class (B5.1, D2078, F40, G21)
// ---------------------------------------------------------------------------

// sharedCodingProfile is the profile the fixture's shared project job is of:
// the name verifyJob answers beside the account, and the one the fixture's
// policy defines it under. It is a name and not a credential.
const sharedCodingProfile = testSharedProfile

// testPolicyDocOneProfile is the project's published policy with a profiles
// block: the profile named, serving github, bound to the fixture's repository
// and listing the accounts given — and, beside it, the default profile every
// policy must define, serving the same accounts so that a service reading the
// wrong one of the two is not caught by this fixture alone.
//
// B5.1's second binding is read out of this block and out of nothing else. The
// repositories a shared caller's row is bound to are the profile's own
// allow_repos (G21, the row the deployment hands the service), and the accounts
// its call may be served by are the ones its profile is deployed for — held
// against the repository's own admission and the project's (admittedAccounts).
// The block is written out rather than left out, because a policy that
// publishes no profile describes an installation that deploys no shared job at
// all.
func testPolicyDocOneProfile(name string, accounts ...string) string {
	quoted := make([]string, 0, len(accounts))
	for _, a := range accounts {
		quoted = append(quoted, `"`+a+`"`)
	}
	body := `{"provider": "github", "cpu": 2, "memory": "8Gi", "timeout_class": "6h",
			"allow_repos": ["` + testRepo + `"],
			"claude_accounts": [` + strings.Join(quoted, ", ") + `]}`
	profiles := `"` + projectpolicy.DefaultProfileName + `": ` + body
	if name != projectpolicy.DefaultProfileName {
		profiles += `, "` + name + `": ` + body
	}
	return `{
		"version": 5,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			"default": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + config.AnthropicKeyAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}
		},
		"profiles": {` + profiles + `},
		"credentials": {"fallback": "auto"}
	}`
}

// newSharedCodingSvc is the installation under test for the coding half of
// C3.9 (B5.1): the caller's verified email is a shared project job's account,
// the row binds it to one profile, and the published policy defines that
// profile — bound to the fixture's repository, serving the fixture's second
// account.
//
// The verifier's list and the pairing's table are the same row, because a
// deployment derives both from one place (SharedJobAccountsFor): the row
// carries the profile the class is answered with and the repositories the
// profile is bound to.
func newSharedCodingSvc(t *testing.T, extra ...func(*svc, *Config)) *svc {
	t.Helper()
	return newSvcWith(t, func(s *svc, cfg *Config) {
		auth, err := NewAuthenticator(AuthConfig{
			Project:           testProject,
			FirebaseProject:   testFirebase,
			Audience:          testAudience,
			JobAccounts:       []string{testJobSA},
			Keys:              s.keys,
			JobKeys:           s.jobKeys,
			Outcomes:          RTDBOutcomes{DB: s.db},
			SharedJobAccounts: []SharedJobAccount{sharedRow()},
			Now:               func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's own verifier configuration is not one NewAuthenticator accepts: %v", err)
		}
		// The fixture's verifier and the server's are the same object, so a
		// test can call either and be asking the same configuration.
		cfg.Auth, s.auth = auth, auth
		cfg.SharedAccounts = []SharedJobAccount{sharedRow()}
		// The installation's repository table: what resolves a run identity's
		// slug to the canonical repository the pairing reads the profile's
		// binding against. It holds the fixture's OTHER repository too, so that
		// a shared caller refused for a repository outside its profile is
		// refused by the profile and not by a table that names one repository.
		cfg.Repos = map[string]string{
			s.slug:                         testRepo,
			testSlug(t, testOtherRepoPath): testOtherRepo,
		}
		pol := &fakePolicy{
			shared:      decodeTestPolicy(t, testPolicyDocOneProfile(sharedCodingProfile, testAccount)),
			sharedFound: true,
		}
		cfg.Policy, s.policy = pol, pol
		// The repository's own node admits the profile's one account, and the
		// project's shared node offers it to the repository too: O4's
		// intersection is satisfied by both legs, so a refusal below is the
		// class's own and not the admission's.
		s.policy.admissions = map[string]*projectpolicy.RepoPolicy{
			s.slug: decodeTestChild(t, testChildDoc(testAccount)),
		}
		for _, m := range extra {
			m(s, cfg)
		}
	})
}

// sharedRunToken is the run identity of a run in the fixture's shared project
// job: a run of the fixture's repository, which is one the profile is bound to.
func (s *svc) sharedRunToken(mutate ...func(map[string]any)) string {
	return s.runToken(mutate...)
}

// sharedRunTokenOf is the same identity for ANOTHER repository: the slug claims
// of a run of that repository. It is how a test presents a shared caller whose
// run is outside the repositories the calling job's profile is bound to.
func (s *svc) sharedRunTokenOf(slug string) string {
	return s.runToken(
		func(c map[string]any) { c["fs"] = slug },
		func(c map[string]any) { c["sub"] = "r~" + slug + "~" + s.run },
		func(c map[string]any) { c["user_id"] = "r~" + slug + "~" + s.run },
	)
}

// sharedJobToken is the caller's Google ID token as the shared project job's
// own service account: the account the class is decided from, and never the job
// name the call reports.
func (s *svc) sharedJobToken(mutate ...func(map[string]any)) string {
	return s.jobToken(append([]func(map[string]any){
		func(c map[string]any) { c["email"] = testSharedSA },
	}, mutate...)...)
}

// TestSharedCodingCallerServedForAProfileRepository is B5.1's red step: a
// coding run in a shared project job is served for its own repository, which is
// one the profile its job is of is bound to.
//
// Before this task the account was refused whole (Global Constraint 6), so the
// caller was answered "the caller is not one of the installation's job service
// accounts" whatever repository it was on — the state a shared job's runs were
// in with the service deployed and switched on. What the class buys is the
// pairing and nothing else (C3.9's coding half): the run is served an account
// the repository's admission and its profile's own list both hold, exactly as a
// per-repository job's run is served one its repository admits.
func TestSharedCodingCallerServedForAProfileRepository(t *testing.T) {
	s := newSharedCodingSvc(t)
	resp, body := s.callTokens(t, callBody("shared-coding"), s.sharedRunToken(), s.sharedJobToken(), s.job)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the shared coding caller was answered %d, want the upstream's 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != testAccount {
		t.Errorf("%s = %q, want the account the profile serves and the repository admits, %q", HeaderCredential, got, testAccount)
	}
	if got := resp.Header.Get(HeaderTier); got != TierA {
		t.Errorf("%s = %q, want %q: the account is an OAuth token", HeaderTier, got, TierA)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1", n)
	}
	auth := s.up.Seen()[0].Header.Get("Authorization")
	if !strings.Contains(auth, fakeCred(t, testAccount)) {
		t.Error("the call did not carry the account the pairing resolved")
	}
	if strings.Contains(auth, fakeCred(t, "default")) {
		t.Error("the call carried an account the repository's own node does not admit")
	}
	// The admission is read for the RUN IDENTITY's repository (the class's
	// pairing), exactly as it is for a review caller's.
	if got := s.policy.readAdmissions(); !slices.Equal(got, []string{s.slug}) {
		t.Errorf("the admissions read were %v, want just the run identity's repository", got)
	}
	// The line names the shared job's own account, which is what an operator
	// searches the class by: the account is the caller's, and the job name the
	// run reports is not.
	lines := forwardLines(t, s.audit)
	if len(lines) != 1 {
		t.Fatalf("an answered shared call wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
	}
	for field, want := range map[string]any{
		"caller":     testSharedSA,
		"credential": testAccount,
		"tier":       TierA,
		"status":     StatusOK,
	} {
		if got := lines[0][field]; got != want {
			t.Errorf("the audit line's %s = %v, want %q", field, got, want)
		}
	}

	// The mint, the class's other route (C3.9's coding half): a shared coding
	// caller is handed the run's own write set, scoped to the one repository
	// the pairing resolved — the class is one job serving several repositories,
	// and the reach of the token stays the one repository its profile is bound
	// to.
	t.Run("the GitHub mint", func(t *testing.T) {
		gh := newGHFake(t)
		m := newSharedCodingSvc(t, func(s *svc, cfg *Config) {
			cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
			s.src.docs = [][]byte{mintBundleDoc(t)}
		})
		resp, body := m.mintTokens(t, mintBody(testAppID), m.sharedRunToken(), m.sharedJobToken(), m.job)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the shared caller's mint answered %d, want 200: %s", resp.StatusCode, body)
		}
		if ans := mintAnswer(t, body); ans.Repository != testRepo {
			t.Errorf("the mint's answer names the repository %q, want the pairing's own %q", ans.Repository, testRepo)
		}
		_, sent := gh.both(t)
		var asked struct {
			Repositories []string          `json:"repositories"`
			Permissions  map[string]string `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(sent.body), &asked); err != nil {
			t.Fatalf("the mint's body is not the JSON this test reads: %v (%s)", err, sent.body)
		}
		if want := github.RunTokenPermissions(); !maps.Equal(asked.Permissions, want) {
			t.Errorf("the mint asked GitHub for %v, want the run's own set %v: a shared coding run does what any coding run does with its repository", asked.Permissions, want)
		}
		_, wantRepo, _ := strings.Cut(testRepoPath, "/")
		if len(asked.Repositories) != 1 || asked.Repositories[0] != wantRepo {
			t.Errorf("the mint asked for repositories %v, want the paired repository alone", asked.Repositories)
		}
	})
}

// TestSharedCodingCallerRefusedForARepositoryOutsideItsProfile: the row's
// repositories are the binding this class is made of (G21, F40). A shared job
// serves every repository its profile is bound to and no other, so a run
// identity of another repository is refused rather than served the profile's
// account — which is the whole difference between this class and a
// per-repository job's, whose account names one repository.
//
// The refusal is a pairing refusal and carries the pairing's vocabulary: the
// account is verified (the class is answered), so the call is refused once and
// recorded once, with the sentence the per-repository class's own binding
// refusal carries.
func TestSharedCodingCallerRefusedForARepositoryOutsideItsProfile(t *testing.T) {
	s := newSharedCodingSvc(t)
	other := testSlug(t, testOtherRepoPath)
	if _, served := s.server.cfg.Repos[other]; !served {
		t.Fatal("the fixture's table does not hold the other repository, so the refusal below would be the table's and not the profile's")
	}
	resp, body := s.callTokens(t, callBody("shared-other-repo"), s.sharedRunTokenOf(other), s.sharedJobToken(), s.job)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	got := refusal(t, resp.StatusCode, body)
	if got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	if !strings.Contains(got.Message, "profile") {
		t.Errorf("the refusal is %q, want it to name what the run identity failed: the profile the calling job is of", got.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: the caller was refused before a credential was chosen", n)
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("the bundle was read %d times, want none: the pairing comes first", n)
	}
	if lines := parseAuditLines(t, s.audit.String()); len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
	}
}

// testPolicyDocProfileWiderThanTheProject is the project's policy with the
// project enrolled in ONE repository and the profiles naming one more: the
// state projectpolicy.ProfileAllowRepos warns about in its own sentence ("a
// profile's list may be wider than the project's spell for spell"), and the one
// a binding read off that accessor alone would let through.
//
// The accounts cover both, because a profile's list must be covered by each
// account it lists — that rule is about the token's reach and says nothing
// about the project's enrollment, which is the gate this document narrows.
func testPolicyDocProfileWiderThanTheProject() string {
	return testPolicyDocProfilesNameBoth(`["` + testRepo + `"]`)
}

// testPolicyDocTheProjectEnrollsBothRepositories is the same document with the
// project's own list as wide as the profiles': the state in which
// projectpolicy.Admits says yes to the second repository, so the only thing
// left that can refuse a shared caller for it is the row its job was deployed
// over (SharedJobAccountFor carries the list as it stood when the job was
// deployed, and nothing widens it when the published profile grows).
func testPolicyDocTheProjectEnrollsBothRepositories() string {
	return testPolicyDocProfilesNameBoth(`["` + testRepo + `", "` + testOtherRepo + `"]`)
}

// testPolicyDocProfilesNameBoth is the document the two policy tests are told
// apart by: both profiles list BOTH repositories, and the project's own
// allow_repos is the one axis the caller sets — one repository, or both.
func testPolicyDocProfilesNameBoth(project string) string {
	both := `["` + testRepo + `", "` + testOtherRepo + `"]`
	body := `{"provider": "github", "cpu": 2, "memory": "8Gi", "timeout_class": "6h",
			"allow_repos": ` + both + `,
			"claude_accounts": ["` + testAccount + `"]}`
	profiles := `"` + projectpolicy.DefaultProfileName + `": ` + body
	if sharedCodingProfile != projectpolicy.DefaultProfileName {
		profiles += `, "` + sharedCodingProfile + `": ` + body
	}
	return `{
		"version": 5,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ` + project + `,
		"accounts": {
			"default": {"allow_repos": ` + both + `},
			"` + testAccount + `": {"allow_repos": ` + both + `}
		},
		"profiles": {` + profiles + `},
		"credentials": {"fallback": "auto"}
	}`
}

// TestSharedCodingCallerRefusedForARepositoryTheProjectDenies: the profile's own
// list is not the admission, and a shared caller is held to the project's
// enrollment as well (G21 with design §4.3: "there is never a state in which a
// shared job serves a repository that has not been allowed").
//
// The fixture is the state that tells the two apart, and it is a policy an
// owner may publish: the project enrolls one repository and the profile the
// caller's job serves names that one and one more. A binding read off the
// row's list alone — the profile's own allow_repos, which is what
// SharedJobAccountFor carries — would serve this caller the GitHub write token
// and the project's model credential for a repository the project denies.
// projectpolicy.Admits is the verdict, and the pairing reads it live.
//
// The refusal is the pairing's: the account is verified and the repository is
// one this installation's table serves, so the call is refused once and
// recorded once, beside the class's own tests.
func TestSharedCodingCallerRefusedForARepositoryTheProjectDenies(t *testing.T) {
	s := newSharedCodingSvc(t, func(s *svc, cfg *Config) {
		doc := decodeTestPolicy(t, testPolicyDocProfileWiderThanTheProject())
		s.policy.shared = doc
		// The row is the deployment's own derivation (SharedJobAccountsFor):
		// the profile's list, both repositories, which is exactly what the
		// project's enrollment does not admit.
		cfg.SharedAccounts = []SharedJobAccount{{
			Email:   testSharedSA,
			Profile: sharedCodingProfile,
			Repos:   []string{testRepo, testOtherRepo},
		}}
		if !projectpolicy.AdmitRepo(cfg.SharedAccounts[0].Repos, testOtherRepo) {
			t.Fatal("the fixture's row does not admit the repository the project denies, so the refusal below would not be this test's")
		}
	})
	other := testSlug(t, testOtherRepoPath)
	resp, body := s.callTokens(t, callBody("shared-project-denies"), s.sharedRunTokenOf(other), s.sharedJobToken(), s.job)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	got := refusal(t, resp.StatusCode, body)
	if got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	if !strings.Contains(got.Message, "project") {
		t.Errorf("the refusal is %q, want it to name the project's admission: the profile's own list admitted this repository", got.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: the caller was refused before a credential was chosen", n)
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("the bundle was read %d times, want none: the pairing comes first", n)
	}
	if lines := parseAuditLines(t, s.audit.String()); len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
	}

	// And the class's other route, which is what the review named: the mint
	// reads no policy of its own, so a shared job whose profile list is wider
	// than the project's would be handed the GitHub write token over a
	// repository the project denies — the pairing is the only check on the
	// route, and it is the same check.
	t.Run("the GitHub mint", func(t *testing.T) {
		gh := newGHFake(t)
		m := newSharedCodingSvc(t, func(s *svc, cfg *Config) {
			cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
			s.src.docs = [][]byte{mintBundleDoc(t)}
			s.policy.shared = decodeTestPolicy(t, testPolicyDocProfileWiderThanTheProject())
			cfg.SharedAccounts = []SharedJobAccount{{
				Email:   testSharedSA,
				Profile: sharedCodingProfile,
				Repos:   []string{testRepo, testOtherRepo},
			}}
		})
		resp, body := m.mintTokens(t, mintBody(testAppID), m.sharedRunTokenOf(other), m.sharedJobToken(), m.job)
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the mint answered %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
		}
		if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedRunIdentity {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("GitHub saw %d requests, want none: no installation token is minted for a repository the project denies", n)
		}
		if lines := parseAuditLines(t, m.audit.String()); len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), m.audit.String())
		}
	})
}

// TestSharedCodingCallerRefusedForARepositoryTheRowDoesNotName: the row is the
// pairing's OTHER gate, and this is the state in which only the row can refuse.
//
// The row is the repositories the calling job was DEPLOYED for
// (SharedJobAccountFor writes the published profile's allow_repos into it), and
// a published profile is a document an owner edits: the live profile and the
// project's enrollment may have been widened since. projectpolicy.Admits then
// answers yes for a repository the row never named, so a pairing held to the
// project's verdict alone would serve this job — which was never deployed for
// that repository — the GitHub write token and the project's model credential
// over it. The row is what says the job's own reach, and both gates are read.
//
// The fixture asserts its own premise: the live policy admits the second
// repository, so the refusal below is the row's and not the project's, on the
// model path and on the mint alike.
func TestSharedCodingCallerRefusedForARepositoryTheRowDoesNotName(t *testing.T) {
	other := testSlug(t, testOtherRepoPath)
	s := newSharedCodingSvc(t, func(s *svc, cfg *Config) {
		doc := decodeTestPolicy(t, testPolicyDocTheProjectEnrollsBothRepositories())
		s.policy.shared = doc
		// The row as a deployment derived it: the profile's list as it stood
		// when the job was deployed, which names the fixture's repository alone.
		cfg.SharedAccounts = []SharedJobAccount{{
			Email:   testSharedSA,
			Profile: sharedCodingProfile,
			Repos:   []string{testRepo},
		}}
		if admits, err := doc.Admits(sharedCodingProfile, testOtherRepo); err != nil || !admits {
			t.Fatalf("the fixture's policy does not admit the second repository (%v, %v): the refusal below would be the project's, and this test would pin the other gate", admits, err)
		}
	})
	resp, body := s.callTokens(t, callBody("shared-row-names-one-repo"), s.sharedRunTokenOf(other), s.sharedJobToken(), s.job)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	got := refusal(t, resp.StatusCode, body)
	if got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	if !strings.Contains(got.Message, "bound to") {
		t.Errorf("the refusal is %q, want it to name the calling job's own binding: the live policy admits this repository", got.Message)
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: the caller was refused before a credential was chosen", n)
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("the bundle was read %d times, want none: the pairing comes first", n)
	}
	if lines := parseAuditLines(t, s.audit.String()); len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
	}

	t.Run("the GitHub mint", func(t *testing.T) {
		gh := newGHFake(t)
		m := newSharedCodingSvc(t, func(s *svc, cfg *Config) {
			cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
			s.src.docs = [][]byte{mintBundleDoc(t)}
			s.policy.shared = decodeTestPolicy(t, testPolicyDocTheProjectEnrollsBothRepositories())
			cfg.SharedAccounts = []SharedJobAccount{{
				Email:   testSharedSA,
				Profile: sharedCodingProfile,
				Repos:   []string{testRepo},
			}}
		})
		resp, body := m.mintTokens(t, mintBody(testAppID), m.sharedRunTokenOf(other), m.sharedJobToken(), m.job)
		if resp.StatusCode != StatusUnverified {
			t.Fatalf("the mint answered %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
		}
		if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedRunIdentity {
			t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
		}
		if n := gh.count(); n != 0 {
			t.Errorf("GitHub saw %d requests, want none: no installation token is minted for a repository the calling job was not deployed for", n)
		}
		if lines := parseAuditLines(t, m.audit.String()); len(lines) != 1 {
			t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), m.audit.String())
		}
	})
}

// TestSharedCodingCallerRefusedWhenThePolicyIsUnreadable: the project's own
// answer is what the pairing is held to, so a node the service cannot read is a
// refusal and never a fallback to the row alone (D143). The row here admits the
// repository, so only the node's absence can refuse the call.
//
// The route is the mint's, and that is the point: the model path reads the
// project's policy for itself a few checks later, so an unreadable node refuses
// a model call whether or not the pairing asked. The mint reads no policy at
// all (D145) — the pairing's read is the only one on that route — so this is
// where the fallback the test forbids would show, as an installation token
// issued for a repository nobody checked.
func TestSharedCodingCallerRefusedWhenThePolicyIsUnreadable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*fakePolicy)
	}{
		{name: "the node is absent", mutate: func(p *fakePolicy) { p.sharedFound = false }},
		{name: "the read failed", mutate: func(p *fakePolicy) { p.sharedErr = errors.New("the database is not answering") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gh := newGHFake(t)
			s := newSharedCodingSvc(t, func(s *svc, cfg *Config) {
				cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
				s.src.docs = [][]byte{mintBundleDoc(t)}
				tc.mutate(s.policy)
			})
			resp, body := s.mintTokens(t, mintBody(testAppID), s.sharedRunToken(), s.sharedJobToken(), s.job)
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
			}
			if got := refusal(t, resp.StatusCode, body); got.Error != ErrorStateUnavailable {
				t.Errorf("the refusal is %q, want %q", got.Error, ErrorStateUnavailable)
			}
			if n := gh.count(); n != 0 {
				t.Errorf("GitHub saw %d requests, want none: no installation token is issued off a policy that could not be read", n)
			}
			if n := s.src.readCount(); n != 0 {
				t.Errorf("the bundle was read %d times, want none: the pairing comes first", n)
			}
			if lines := parseAuditLines(t, s.audit.String()); len(lines) != 1 {
				t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
			}
		})
	}
}

// TestSharedCodingCallerAdmittedOnlyTheProfilesAccounts: a shared job is
// deployed for ONE account axis value of its profile, so the account its run
// may be served is one its profile is deployed for AND one the repository's own
// node and the project's shared node both admit.
//
// The fixture is built to tell the two legs apart: the repository's node admits
// the fixture's second Claude account, and the profile serves the project's key
// alone (the axis's other spelling). A service that read the admission alone —
// which is exactly what a per-repository job's class does — would answer with
// that account; the class's own binding answers with the key instead, because
// the account is not one the calling job's profile deploys.
func TestSharedCodingCallerAdmittedOnlyTheProfilesAccounts(t *testing.T) {
	s := newSharedCodingSvc(t, func(s *svc, cfg *Config) {
		s.policy.shared = decodeTestPolicy(t, testPolicyDocOneProfile(sharedCodingProfile, config.AnthropicKeyAccount))
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc(testAccount))
	})
	resp, body := s.callTokens(t, callBody("shared-profile-accounts"), s.sharedRunToken(), s.sharedJobToken(), s.job)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want the upstream's 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != config.AnthropicSecret {
		t.Errorf("%s = %q, want the project's own key %q: the profile serves that value and not the account the repository admits", HeaderCredential, got, config.AnthropicSecret)
	}
	if got := resp.Header.Get(HeaderTier); got != TierB {
		t.Errorf("%s = %q, want %q: the project's key is real money", HeaderTier, got, TierB)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1", n)
	}
	req := s.up.Seen()[0]
	if got := req.Header.Get("x-api-key"); got != fakeCred(t, "paid") {
		t.Error("the call did not carry the project's key")
	}
	if auth := req.Header.Get("Authorization"); auth != "" {
		t.Errorf("the call carried an Authorization header %q, want none: the account the repository admits is not one this job's profile deploys", auth)
	}
}

// TestReviewCallerUnchanged: the review class is not touched by B5.1. The
// account a review call is served by is the pairing's own and never a profile's
// list — a review job's account names no profile at all (Identity.Profile is
// empty for it), and the class's offering is still the one credential its job
// is of (reviewCandidates).
//
// The fixture is the one that would fail if the profile narrowing were applied
// to every class: its policy defines the DEFAULT profile — the profile an empty
// Identity.Profile resolves to — as serving the project's key alone, and its
// review job is of the fixture's second Claude account.
func TestReviewCallerUnchanged(t *testing.T) {
	p, found, err := projectpolicy.Decode([]byte(testPolicyDocOneProfile(projectpolicy.DefaultProfileName, config.AnthropicKeyAccount)))
	if err != nil || !found {
		t.Fatalf("the test's own policy document is not one Decode accepts: %v", err)
	}
	s := newReviewSvcSpelled(t, testReviewSA, testReviewSA, func(s *svc, cfg *Config) {
		s.policy.shared = p
	})
	resp, body := s.callTokens(t, callBody("review-unchanged"), s.reviewRunToken(), s.reviewJobToken(), s.job)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the review caller was answered %d, want the upstream's 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != testReviewAccount {
		t.Errorf("%s = %q, want the review job's own account %q: a review caller is served the pairing's credential and no profile's list", HeaderCredential, got, testReviewAccount)
	}
	if got := resp.Header.Get(HeaderTier); got != TierA {
		t.Errorf("%s = %q, want %q", HeaderTier, got, TierA)
	}
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want 1", n)
	}
	if auth := s.up.Seen()[0].Header.Get("Authorization"); !strings.Contains(auth, fakeCred(t, testReviewAccount)) {
		t.Error("the call did not carry the account the pairing resolved")
	}
	if got := s.policy.readAdmissions(); !slices.Equal(got, []string{s.slug}) {
		t.Errorf("the admissions read were %v, want just the run identity's repository", got)
	}
}
