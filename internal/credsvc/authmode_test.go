package credsvc

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
)

// authRecord writes the record `fugaro update` would have written for the
// fixture's repository: the one document this service decides a non-cascade
// repository's reach from (creds/repoauth, D2070 (a)).
func (s *svc) authRecord(t *testing.T, auth string, accounts ...string) {
	t.Helper()
	s.state.setRepoAuth(s.slug, creds.RepoAuth{Version: creds.RepoAuthVersion, Auth: auth, Accounts: accounts})
}

// candidatesFor runs the service's own candidate walk for the fixture's
// repository, with what the call declared. It is the one function that decides
// which credentials a call may be offered, so the narrowing is stated over its
// answer rather than over a served call wherever the list itself is the claim.
func (s *svc) candidatesFor(t *testing.T, mode, account string) ([]creds.Credential, []Tried, error) {
	t.Helper()
	b, err := s.server.cfg.Bundles.Load(context.Background())
	if err != nil {
		t.Fatalf("loading the fixture's own bundle: %v", err)
	}
	id := Identity{Slug: s.slug, JobClass: ClassRepository, AuthMode: mode, AuthAccount: account}
	cands, _, skipped, err := s.server.candidates(context.Background(), id, testRepo, s.policy.shared, b)
	return cands, skipped, err
}

// declaredCall is one call carrying the two auth headers, which is what the
// run's own gateway will send once B2.2 lands.
func declaredCall(mode, account string) func(*http.Request) {
	return func(r *http.Request) {
		if mode != "" {
			r.Header.Set(HeaderAuthMode, mode)
		}
		if account != "" {
			r.Header.Set(HeaderAuthAccount, account)
		}
	}
}

// authRefusal fails unless err is the service's own refusal sentence for a call
// its repository's record does not admit, and returns it.
func authRefusal(t *testing.T, err error) *poolRefusal {
	t.Helper()
	if err == nil {
		t.Fatal("the call was admitted; want a refusal")
	}
	var refusal *poolRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("the refusal is %T (%v); want the service's own sentence", err, err)
	}
	return refusal
}

// TestAuthModeOAuthServesOnlyARecordedAccount: a repository whose record says
// `oauth` is served the one tier-A credential of the account it declares, and
// nothing else — not the other subscription the bundle holds, and not the
// project's paid key. The end-to-end half is the point: with that account
// exhausted the call is answered the typed wait and the upstream sees nothing,
// where the same state under the cascade reaches tier B (TestCascadeAutoFallsToB).
func TestAuthModeOAuthServesOnlyARecordedAccount(t *testing.T) {
	s := newSvc(t)
	s.authRecord(t, "oauth", testAccount)

	cands, skipped, err := s.candidatesFor(t, "oauth", testAccount)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(cands) != 1 || cands[0].Name != testAccount || cands[0].Tier != creds.TierA {
		t.Fatalf("the call was offered %v; want exactly the recorded account %q at tier A", cands, testAccount)
	}
	reported := reportedNotAdmitted(t, skipped)
	for _, name := range []string{config.DefaultClaudeAccount, config.AnthropicSecret} {
		got, ok := reported[name]
		if !ok {
			t.Errorf("the credential %q was neither offered nor reported as not admitted", name)
			continue
		}
		if got.Reason != ReasonNotAdmitted {
			t.Errorf("the credential %q was reported as %q, want %q", name, got.Reason, ReasonNotAdmitted)
		}
	}

	s.hold(testAccount, s.now.Add(-time.Minute), s.now.Add(6*time.Hour))
	resp, body := s.call(t, callBody("oauth-record"), declaredCall("oauth", testAccount))
	if resp.StatusCode != StatusExhaustedUntil {
		t.Fatalf("the answer is %d, want the typed wait (%d): the recorded account is held and the paid key is not this repository's: %s",
			resp.StatusCode, StatusExhaustedUntil, body)
	}
	if n := s.up.Count(); n != 0 {
		t.Fatalf("the upstream saw %d requests, want none: a repository recorded as oauth never falls to the paid key", n)
	}
}

// TestAuthModeOAuthRefusesAnAccountTheRecordDoesNotName: the account a call
// declares has to be one the repository's own record names. A call that
// declares another account — or none, which reads as the unsuffixed default —
// is refused rather than served the cascade, and the sentence names the account
// it declared and the accounts the record holds, so an operator reads the
// disagreement rather than a missing credential.
func TestAuthModeOAuthRefusesAnAccountTheRecordDoesNotName(t *testing.T) {
	// A record that names the unsuffixed default account: a call that declares
	// another account is refused.
	s := newSvc(t)
	s.authRecord(t, "oauth", config.DefaultClaudeAccount)
	_, _, err := s.candidatesFor(t, "oauth", testAccount)
	refusal := authRefusal(t, err)
	if !strings.Contains(refusal.message, config.DefaultClaudeAccount) {
		t.Errorf("the refusal does not name the account the record holds: %q", refusal.message)
	}
	if !strings.Contains(refusal.message, testAccount) {
		t.Errorf("the refusal does not name the account the call declared: %q", refusal.message)
	}

	// A record that does not name the default account: a call that declares no
	// account at all reads as the default one, and it is refused for the same
	// reason — the record is what decides, and the unsuffixed account is a name
	// like any other.
	named := newSvc(t)
	named.authRecord(t, "oauth", testAccount)
	if _, _, err := named.candidatesFor(t, "oauth", ""); err == nil {
		t.Fatal("a call declaring no account was served the default account, which its record does not name")
	}

	resp, body := s.call(t, callBody("oauth-other-account"), declaredCall("oauth", testAccount))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if n := s.up.Count(); n != 0 {
		t.Fatalf("the upstream saw %d requests, want none: the call was served where it must be refused", n)
	}
}

// TestAuthModeAPIKeyServesTierBOnly: a repository recorded as `api-key` is
// served the project's paid key and no subscription at all, whatever the
// bundle holds and whatever the repository is admitted to. The end-to-end half
// states it the other way round from the oauth test: the call IS served, and by
// the key, with the two tier-A accounts of the bundle left out of its list.
func TestAuthModeAPIKeyServesTierBOnly(t *testing.T) {
	s := newSvc(t)
	s.authRecord(t, "api-key")

	cands, skipped, err := s.candidatesFor(t, "api-key", "")
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(cands) != 1 || cands[0].Name != config.AnthropicSecret || cands[0].Tier != creds.TierB {
		t.Fatalf("the call was offered %v; want exactly the project's key %q at tier B", cands, config.AnthropicSecret)
	}
	reported := reportedNotAdmitted(t, skipped)
	for _, name := range []string{config.DefaultClaudeAccount, testAccount} {
		if got, ok := reported[name]; !ok || got.Reason != ReasonNotAdmitted {
			t.Errorf("the subscription %q was neither offered nor reported as not admitted (%v)", name, got)
		}
	}

	resp, _ := s.call(t, callBody("api-key-record"), declaredCall("api-key", ""))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: an api-key repository is served the project's key", resp.StatusCode)
	}
	if got := resp.Header.Get(HeaderCredential); got != config.AnthropicSecret {
		t.Fatalf("%s = %q, want %q: the subscriptions are not this repository's", HeaderCredential, got, config.AnthropicSecret)
	}
	if got := resp.Header.Get(HeaderTier); got != TierB {
		t.Fatalf("%s = %q, want %q", HeaderTier, got, TierB)
	}
}

// TestAuthModeMissingDeclarationOnANonCascadeRepositoryRefused is the plan's
// red step. Before this task a call that declared nothing was served the whole
// bundle: the record was not read at all, so an `oauth` repository's call
// reached the paid key with nothing on the wire saying which credential its own
// configuration asked for. The agent holds the run identity and the job's ID
// token (G16), so "I declared nothing" is exactly what a caller trying to reach
// the cascade would send, and it is refused here.
func TestAuthModeMissingDeclarationOnANonCascadeRepositoryRefused(t *testing.T) {
	s := newSvc(t)
	s.authRecord(t, "oauth", testAccount)

	_, _, err := s.candidatesFor(t, "", "")
	refusal := authRefusal(t, err)
	if !strings.Contains(refusal.message, "declares no auth") {
		t.Errorf("the refusal does not say the call declared nothing: %q", refusal.message)
	}
	if !strings.Contains(refusal.message, "oauth") {
		t.Errorf("the refusal does not name the repository's recorded auth: %q", refusal.message)
	}

	resp, body := s.call(t, callBody("no-declaration"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if n := s.up.Count(); n != 0 {
		t.Fatalf("the upstream saw %d requests, want none: a call that declares nothing is not served the cascade", n)
	}
}

// TestAuthModeMismatchedDeclarationRefused: the declaration is checked against
// the record, so a call that declares another auth — or an account beside an
// auth that names none, or an auth this service does not serve at all — is
// refused. The record decides and the caller is only ever compared against it,
// which is the whole of R7's answer to a caller that can write its own headers.
func TestAuthModeMismatchedDeclarationRefused(t *testing.T) {
	for _, tc := range []struct {
		what     string
		auth     string
		accounts []string
		mode     string
		account  string
	}{
		{"an oauth record declared as api-key", "oauth", []string{testAccount}, "api-key", ""},
		{"an api-key record declared as oauth", "api-key", nil, "oauth", ""},
		{"an api-key record with an account declared", "api-key", nil, "api-key", testAccount},
		{"an oauth record with no account", "oauth", nil, "oauth", testAccount},
		{"a vertex record", "vertex", nil, "vertex", ""},
		{"a vertex record declared as cascade", "vertex", nil, config.AuthCascade, ""},
	} {
		t.Run(tc.what, func(t *testing.T) {
			s := newSvc(t)
			s.authRecord(t, tc.auth, tc.accounts...)
			_, _, err := s.candidatesFor(t, tc.mode, tc.account)
			authRefusal(t, err)
		})
	}
}

// TestAuthModeNeverWidensTheAdmission: the narrowing is applied to the list O4's
// admission, the crafts and the bundle already produced, and can only ever take
// away from it. A record that names an account this repository is not admitted
// to, or one this installation's bundle does not hold, is refused — never
// served, and never a reason for the list to grow.
func TestAuthModeNeverWidensTheAdmission(t *testing.T) {
	// The fixture admits "default" and alice to the repository. A record naming
	// an account the admission dropped is a repository whose own configuration
	// and whose admission disagree, and the answer is the refusal, not the
	// credential the admission withheld.
	s := newSvcWith(t, func(s *svc, _ *Config) {
		s.policy.admissions[s.slug] = decodeTestChild(t, testChildDoc(config.DefaultClaudeAccount))
	})
	s.authRecord(t, "oauth", testAccount)
	if _, _, err := s.candidatesFor(t, "oauth", testAccount); err == nil {
		t.Fatal("a repository recorded as going out on an account it is not admitted to was served it: the record narrows the admission and never widens it")
	}

	// A record naming an account no credential of the bundle carries is refused
	// for the same reason, and it is the case a caller can reach by declaring
	// any well-formed name.
	other := newSvc(t)
	other.authRecord(t, "oauth", "bob")
	if _, _, err := other.candidatesFor(t, "oauth", "bob"); err == nil {
		t.Fatal("a repository recorded as going out on an account the bundle does not hold was served: the record narrows the admission and never widens it")
	}

	// And what it answers is always a subset of what the same call would have
	// been offered without a record: every credential of the narrowed list is
	// one the bundle holds, at the tier the bundle holds it.
	whole := newSvc(t)
	all, _, err := whole.candidatesFor(t, "", "")
	if err != nil {
		t.Fatalf("candidates with no record: %v", err)
	}
	recorded := newSvc(t)
	recorded.authRecord(t, "oauth", testAccount)
	narrowed, _, err := recorded.candidatesFor(t, "oauth", testAccount)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(narrowed) == 0 {
		t.Fatal("the narrowed list is empty, so the subset check below proves nothing")
	}
	for _, c := range narrowed {
		if !slices.Contains(all, c) {
			t.Errorf("the narrowed list holds %v, which the unnarrowed list does not: the record must only ever take away", c)
		}
	}
}

// TestNoRecordIsTheCascade: a repository no update has recorded is served
// exactly what it was served before this narrowing existed — the whole bundle,
// tier A and tier B — and a record that says `cascade` is the same answer. An
// installation this release has not written must not lose its runs, and a
// repository that asks for the cascade is asking for what it already had.
func TestNoRecordIsTheCascade(t *testing.T) {
	s := newSvc(t)
	b, err := s.server.cfg.Bundles.Load(context.Background())
	if err != nil {
		t.Fatalf("loading the fixture's own bundle: %v", err)
	}
	want := b.Credentials()
	if len(want) < 2 {
		t.Fatalf("the fixture's bundle holds %v, so this test cannot tell a narrowing from no narrowing", want)
	}

	for _, tc := range []struct{ what, mode string }{
		{"no record and no declaration", ""},
		{"no record and a declaration", "oauth"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			cands, skipped, err := s.candidatesFor(t, tc.mode, "")
			if err != nil {
				t.Fatalf("candidates: %v", err)
			}
			if len(cands) != len(want) {
				t.Fatalf("the call was offered %v; want the whole bundle %v — no record is the cascade", cands, want)
			}
			if len(skipped) != 0 {
				t.Fatalf("the call reported %v as not admitted; want nothing reported — no record is the cascade", skipped)
			}
		})
	}

	s.authRecord(t, config.AuthCascade)
	cands, _, err := s.candidatesFor(t, "", "")
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(cands) != len(want) {
		t.Fatalf("a cascade record offered %v; want the whole bundle %v", cands, want)
	}
}

// TestAuthModeHeaderSentTwiceRefused: the two headers are read the way every
// other header of this contract is — one value or none — and a call that sends
// one twice is refused rather than answered from whichever value the map kept.
// The boundary is decided from these headers, so a call carrying two of them is
// a call trying to have both answers.
func TestAuthModeHeaderSentTwiceRefused(t *testing.T) {
	base := func() *http.Request {
		r, err := http.NewRequest(http.MethodPost, "/v1/messages", nil)
		if err != nil {
			t.Fatalf("building the request: %v", err)
		}
		r.Header.Set("Authorization", "Bearer job-token")
		r.Header.Set(HeaderRunIdentity, "run-token")
		r.Header.Set(HeaderRunJob, "fugaro-aurora-app")
		return r
	}

	for _, tc := range []struct {
		what  string
		apply func(*http.Request)
	}{
		{"the mode twice", func(r *http.Request) {
			r.Header.Add(HeaderAuthMode, "oauth")
			r.Header.Add(HeaderAuthMode, "api-key")
		}},
		{"the account twice", func(r *http.Request) {
			r.Header.Add(HeaderAuthAccount, testAccount)
			r.Header.Add(HeaderAuthAccount, config.DefaultClaudeAccount)
		}},
		{"an empty mode", func(r *http.Request) { r.Header.Set(HeaderAuthMode, "") }},
		{"an empty account", func(r *http.Request) { r.Header.Set(HeaderAuthAccount, "") }},
		{"an auth this contract does not carry", func(r *http.Request) { r.Header.Set(HeaderAuthMode, "bedrock") }},
		{"an account that is not a name", func(r *http.Request) { r.Header.Set(HeaderAuthAccount, "Team A") }},
	} {
		t.Run(tc.what, func(t *testing.T) {
			r := base()
			tc.apply(r)
			if _, err := RequestFromHTTP(r); err == nil {
				t.Fatal("the request was read as one this contract carries; want a refusal")
			}
		})
	}

	// The headers are optional, and a call that sends neither is read.
	r := base()
	req, err := RequestFromHTTP(r)
	if err != nil {
		t.Fatalf("a call with neither header was refused: %v", err)
	}
	if req.AuthMode != "" || req.AuthAccount != "" {
		t.Fatalf("a call that declared nothing was read as declaring %q/%q", req.AuthMode, req.AuthAccount)
	}
}

// TestAuthModeRefusalWritesOneAuditLine: a refusal taken on the repository's
// record is a refusal like every other, and the area rule holds it to exactly
// one audit line (internal/credsvc's audit.go). The line carries the caller's
// identity and the fields of the contract and never a credential's value.
func TestAuthModeRefusalWritesOneAuditLine(t *testing.T) {
	s := newSvc(t)
	s.authRecord(t, "oauth", testAccount)

	resp, body := s.call(t, callBody("mismatched"), declaredCall("api-key", ""))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Fatalf("the refusal's type is %q, want %q", e.Error, ErrorStateUnavailable)
	}

	lines := parseAuditLines(t, s.audit.String())
	if len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
	}
	checkAuditShape(t, lines[0])
	for _, v := range []string{fakeCred(t, config.DefaultClaudeAccount), fakeCred(t, testAccount), fakeCred(t, "paid")} {
		if strings.Contains(s.audit.String(), v) || strings.Contains(s.logs.String(), v) || strings.Contains(string(body), v) {
			t.Fatal("a credential's value reached the audit line, the log or the refusal body")
		}
	}
}

// TestAuthModeUnreadableRecordRefused: a record the service cannot read is a
// refusal and never a fallback to the cascade. The read is the one thing
// standing between a non-cascade repository and the whole bundle, so a database
// that could not answer it must fail closed (G19) — a caller whose repository's
// auth is unknown is a caller nothing can be decided about.
func TestAuthModeUnreadableRecordRefused(t *testing.T) {
	s := newSvc(t)
	s.state.repoAuthErr = errors.New("the database could not be read")

	resp, body := s.call(t, callBody("unreadable-record"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if n := s.up.Count(); n != 0 {
		t.Fatalf("the upstream saw %d requests, want none: a record that could not be read is not the cascade", n)
	}
	if lines := parseAuditLines(t, s.audit.String()); len(lines) != 1 {
		t.Fatalf("the refusal wrote %d audit lines, want exactly one: %s", len(lines), s.audit.String())
	}
}
