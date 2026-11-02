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
	"net/http"
	"slices"
	"strings"
	"testing"
)

const (
	// testReviewAccount is the Claude account the fixture's review job is
	// served as, by the name the published admissions spell it with: the
	// fixture's bundle holds it, and the fixture's admissions offer it to the
	// fixture's repository.
	testReviewAccount = testAccount
	// testReviewSA is the review job's own service account, by the spelling
	// internal/infra deploys the variant's account with. It is a name and not
	// a credential.
	testReviewSA = "fugaro-p-profile-one-anthropic-alice-review-2f0a5c19@aurora-dev.iam.gserviceaccount.com"
)

// newReviewSvc is the fixture with the one review job account this class is
// about: the account the caller is verified as, and the table that resolves
// the run identity's slug.
func newReviewSvc(t *testing.T) *svc {
	t.Helper()
	return newSvcWith(t, nil)
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
