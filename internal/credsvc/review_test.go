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
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
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
			SharedJobAccounts: []string{testSharedSA},
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

// reviewMintSvc is the review fixture behind the mint's fake GitHub, with the
// bundle a mint needs: a caller the pairing admits, on the one route of this
// service the class is not admitted to, where the refusal cannot be about the
// identity, the pairing or the bundle.
func reviewMintSvc(t *testing.T) (*svc, *ghFake) {
	t.Helper()
	gh := newGHFake(t)
	s := newReviewSvcSpelled(t, testReviewSA, testReviewSA, func(s *svc, cfg *Config) {
		cfg.Client = &http.Client{Transport: ghTransport{srv: gh.srv}}
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
// The table is keyed by the folded email for that reason, and this is the call
// that is refused if it ever stops being.
func TestReviewTableIsKeyedTheWayTheVerifierKeysItsOwn(t *testing.T) {
	loud := strings.ToUpper(testReviewSA)
	if loud == testReviewSA {
		t.Fatal("the fixture's own service account is already upper case, so this test would cover nothing")
	}
	s := newReviewSvcSpelled(t, testReviewSA, loud)
	resp, body := s.callTokens(t, callBody("review-case-folded"), s.reviewRunToken(), s.reviewJobToken(), s.job)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the review caller was answered %d, want the upstream's 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderCredential); got != testReviewAccount {
		t.Errorf("%s = %q, want the review job's own account %q", HeaderCredential, got, testReviewAccount)
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

// TestReviewCallerIsRefusedTheMint: the mint is the one route of this service
// the review class is not admitted to, and this is the guard that says so
// (C3.9b replaces it with CSR1's read-only permission set, and C3.9b must land
// before anything fills the review set).
//
// The caller here is otherwise perfectly good: its two identities are whole,
// the pairing admits it, and the bundle holds the App key a mint would be
// issued from — so the only thing that can refuse it is the class. Left
// unguarded it would be handed a token carrying github.RunTokenPermissions
// over the run identity's repository, which is the WRITE set of the very
// repository the review is about: the review job mounts no repository and
// holds no installation token of its own (O1), and this endpoint is where that
// would be undone.
func TestReviewCallerIsRefusedTheMint(t *testing.T) {
	s, gh := reviewMintSvc(t)
	resp, body := s.mintTokens(t, mintBody(testAppID), s.reviewRunToken(), s.reviewJobToken(), s.job)
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	got := refusal(t, resp.StatusCode, body)
	if got.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", got.Error, ErrorUnverifiedRunIdentity)
	}
	var ans MintResponse
	if err := decodeStrict(body, &ans); err == nil {
		t.Errorf("the refusal carries a mint's answer: %+v", ans)
	}
	if n := gh.count(); n != 0 {
		t.Errorf("GitHub saw %d requests, want none: no installation token was asked for", n)
	}
	if n := s.src.readCount(); n != 0 {
		t.Errorf("the bundle was read %d times, want none: the class is refused before it", n)
	}
	lines := mintAuditLines(t, s)
	if len(lines) != 1 {
		t.Fatalf("the refused mint wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
	}
	if got := lines[0]["purpose"]; got != PurposeMint {
		t.Errorf("the refusal's line records purpose %v, want %q: the act the run asked for", got, PurposeMint)
	}
	if got := lines[0]["credential"]; got != config.GitHubAppKeySecret {
		t.Errorf("the refusal's line names credential %v, want the App key's name %q", got, config.GitHubAppKeySecret)
	}
}

// TestSharedJobCallerIsStillRefusedOnEveryRoute: the gate is narrowed by this
// task and not lifted (F2, Global Constraint 6). A coding shared project job's
// account is refused on every route the service has — the model path and its
// token-count sibling, and the mint — with the sentence it is refused with
// today, and the refusal is the caller's verified email and never the job name
// it reports.
func TestSharedJobCallerIsStillRefusedOnEveryRoute(t *testing.T) {
	for _, path := range []string{MessagesPath, CountTokensPath, MintPath} {
		t.Run(path, func(t *testing.T) {
			s := newSvc(t)
			tok := s.jobToken(func(c map[string]any) { c["email"] = testSharedSA })
			resp := s.postTokens(t, path, callBody("shared"), s.runToken(), tok, s.job)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("reading the answer: %v", err)
			}
			if resp.StatusCode != StatusUnverified {
				t.Fatalf("the answer is %d, want %d", resp.StatusCode, StatusUnverified)
			}
			if got := refusal(t, resp.StatusCode, body); got.Error != ErrorUnverifiedJobIdentity {
				t.Errorf("the refusal is %q, want %q: %s", got.Error, ErrorUnverifiedJobIdentity, got.Message)
			}
			if n := s.up.Count(); n != 0 {
				t.Errorf("the upstream saw %d requests, want none", n)
			}
		})
	}
}
