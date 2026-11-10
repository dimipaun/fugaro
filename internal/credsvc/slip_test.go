package credsvc

// DS1.3's tests: the two routes a run's credentials hang on (D1937, D1941).
//
// The grant is the launcher's act and the claim is the run's, and they are the
// only two routes in this service whose caller is not already holding the thing
// the route is about:
//
//	POST /v1/slip         a launcher asks for a run's grant; the slip is written
//	                      and its id answered (launcher class, D1938)
//	POST /v1/slip/claim   the run claims it ONCE with the identity and the
//	                      account the slip names, and is answered the claim
//	                      secret it must present on every call after (D1941)
//
// What each test is for, in one line: the owner rule at the grant (R7), the id
// never being a ticket (G3), the three ways a claim is refused (another run,
// another job account, a second claim), the claim secret (D1941), the empty
// launcher list (F31), the two classes against each other (G13), and the
// served route set (G10).
//
// Everything here is hermetic (Convention 9): the slips are written to
// internal/gcpfake's Realtime Database through the real creds.RTDBStore, so the
// conditional write and the compare-and-set a claim is decided by are exercised
// and not faked — a fixture store that claimed unconditionally would make
// TestSecondClaimRefused pass on a service that hands two runners one slip.
// Nothing in this file is, or looks like, a credential: every value is drawn at
// run time (Convention 2), and a name that is written down is a NAME
// (a secret ID of the grammar, or a bundle entry's own constant).

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/secretname"
	"github.com/dimipaun/fugaro/internal/task"
)

const (
	// testOtherSub is a Google account id that is not the fixture's launcher's:
	// the owner of a per-user credential that launcher may not name (R7). It is
	// digits only and of the width a real account id has, because that is the
	// rule the grammar and the launcher class hold it to (DS1.2).
	testOtherSub = "104729113355608719402"
	// testSiblingSA is a SECOND job account bound to the fixture's own
	// repository. One repository has one job account per workflow, so the
	// account a slip expects is not the only one a run of its repository can
	// call as — which is what makes the slip's own expected account the thing
	// TestOtherJobAccountCannotClaim turns on, rather than the pairing.
	testSiblingSA = "fugaro-job-second@aurora-dev.iam.gserviceaccount.com"
	// testSlipCredential is a credential a grant may name that is NOT of the
	// per-user grammar: a bundle entry's own name (C1.11). No owner rule
	// applies to it — it is nobody's — which is why the tests that are about
	// the owner rule name per-user credentials and the rest name this.
	testSlipCredential = config.ClaudeOAuthToken
	// slipNode is where the slips live in the Realtime Database, spelled here
	// because a test reads the raw tree and not only the store: what a REFUSED
	// grant left behind is a fact about the database, and the store's own
	// readers would hide a record written under a node nothing serves. Renaming
	// the node without this line fails TestGrantRefusedForAnotherOwnersToken's
	// positive assertion (it names the id the service answered), which is why
	// the literal is safe to hold here.
	slipNode = "creds/slips"
)

// otherRun is another run id of the same repository. It is a REAL one of the
// shape the launcher mints (task.ValidRunID, fifteen bytes of launch time and a
// suffix): the identity's liveness check reads the launch day out of the id, so
// a run id this service cannot date would be refused before the slip was
// reached, and TestOtherRunCannotClaim would pass for the wrong reason.
func otherRun() string {
	return testNow().Add(-time.Hour).Format("20060102-150405") + "-cd34"
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// slips is one installation's service with BOTH slip routes wired, the launcher
// class configured from the list the test gives, and the fake database the
// slips are written to. db and store are the two readings a test needs of what
// a call wrote: the raw tree (which ids exist at all) and the real store (what
// the record says).
type slips struct {
	*svc
	db    *gcpfake.RTDB
	store *creds.RTDBStore
}

// slipStore is the real slip store over a fake database, wired the way
// internal/cli/credentials_serve.go wires it for the deployment and no
// differently: *rtdb.Client is the SlipStore.
func slipStore(t *testing.T, db *gcpfake.RTDB) *creds.RTDBStore {
	t.Helper()
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	return &creds.RTDBStore{DB: client}
}

// testSlips is the fixture's own slip store, over a database of its own that
// no test reads: what server_test.go's Config needs to satisfy NewServer, which
// refuses a service that cannot write a slip. It is here rather than in
// server_test.go so that the file's own imports do not change for a store the
// tests that use it never look at; slipStore is the body, so the store the
// untouched routes are given is the same one these two are wired with.
func testSlips(t *testing.T) *creds.RTDBStore {
	t.Helper()
	return slipStore(t, gcpfake.NewRTDB(t))
}

// newSlips is the fixture: the service, and a launcher class holding exactly
// the addresses given (an empty list is a class that admits nobody, F31).
//
// The authenticator is rebuilt here rather than taken from the fixture because
// the launcher list is part of the class's configuration (DS1.2) and the
// fixture's own classes are the job's: this is the one place a test says who
// may ask for a slip.
func newSlips(t *testing.T, launchers ...string) *slips {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	store := slipStore(t, db)
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		cfg.Slips = store
		auth, err := NewAuthenticator(AuthConfig{
			Project:          testProject,
			FirebaseProject:  testFirebase,
			Audience:         testAudience,
			JobAccounts:      []string{testJobSA, testSiblingSA},
			Keys:             s.keys,
			JobKeys:          s.jobKeys,
			Outcomes:         RTDBOutcomes{DB: s.db},
			LauncherAccounts: launchers,
			Now:              func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's launcher class cannot be configured: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		// The sibling account is bound to the fixture's own repository and runs
		// as a job of its own (one repository, one account per workflow), so a
		// call it makes pairs and is then refused by the SLIP's own expected
		// account — which is the property TestOtherJobAccountCannotClaim states.
		cfg.JobAccounts = append(cfg.JobAccounts, JobAccount{Email: testSiblingSA, Slug: s.slug, Repo: testRepo})
	})
	return &slips{svc: s, db: db, store: store}
}

// slipIDs is the ids the database holds under creds/slips, sorted: what the
// grants wrote, whatever they answered. It is how a test states that a refused
// grant wrote no record at all.
func (s *slips) slipIDs(t *testing.T) []string {
	t.Helper()
	node, _ := s.db.Value(slipNode).(map[string]any)
	ids := make([]string, 0, len(node))
	for id := range node {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// slip reads one slip back through the store the service writes with, which is
// what a test reads when it needs to know what a call left in the record.
func (s *slips) slip(t *testing.T, id string) creds.Slip {
	t.Helper()
	sl, found, err := s.store.Slip(context.Background(), id)
	if err != nil {
		t.Fatalf("reading the slip %q back: %v", id, err)
	}
	if !found {
		t.Fatalf("the slip %q is not in the database", id)
	}
	return sl
}

// ---------------------------------------------------------------------------
// The calls
// ---------------------------------------------------------------------------

// slipPost sends one slip call: the body and the headers the test chooses, with
// the answer read whole. It is postRaw's sibling for the two routes whose
// requests are this service's own contract and whose callers may hold no
// identity at all.
func (s *slips) slipPost(t *testing.T, path, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the call: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, m := range mutate {
		m(req)
	}
	resp, err := s.http.Client().Do(req)
	if err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, b
}

// launcherBearer is the one credential the grant route reads: the launcher's
// own Google ID token, in the bearer the contract puts every caller's token in.
func (s *slips) launcherBearer() func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+s.launcherToken()) }
}

// grantCall is the launcher's own grant call, with the body the test writes.
func (s *slips) grantCall(t *testing.T, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.slipPost(t, SlipPath, body, append([]func(*http.Request){s.launcherBearer()}, mutate...)...)
}

// grantOK asks for a slip and answers what the service granted, failing the
// test on any refusal.
func (s *slips) grantOK(t *testing.T, body string) SlipGrantResponse {
	t.Helper()
	resp, b := s.grantCall(t, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the grant is answered %d, want 200: %s", resp.StatusCode, b)
	}
	var ans SlipGrantResponse
	if err := json.Unmarshal(b, &ans); err != nil {
		t.Fatalf("the grant's answer is not one this contract reads: %v", err)
	}
	if ans.Slip == "" {
		t.Fatal("the grant answered no slip id")
	}
	if ans.MaxRuntimeSeconds <= 0 {
		t.Fatalf("the grant answered the runtime %d, want a positive one", ans.MaxRuntimeSeconds)
	}
	return ans
}

// jobIdentities is the three headers the shared parse reads for a job-class
// call: the job's own token in the bearer, the run identity, and the job name
// the run reports.
func (s *slips) jobIdentities() func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.jobToken())
		r.Header.Set(HeaderRunIdentity, s.runToken())
		r.Header.Set(HeaderRunJob, s.job)
	}
}

// runTokenFor is the fixture's run identity for ANOTHER run of the same
// repository: the uid, its two spellings and the fr claim the verifier reads
// all move together, so the identity is whole and it is the slip's binding that
// has to refuse it.
func (s *slips) runTokenFor(run string) string {
	return s.runToken(func(c map[string]any) {
		uid := "r~" + s.slug + "~" + run
		c["sub"], c["user_id"], c["fr"] = uid, uid, run
	})
}

// claimCall is the run's claim call: the identities above, the slip the test
// names, and whatever the test adds to it (a claim secret, another identity).
func (s *slips) claimCall(t *testing.T, slip, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	head := s.jobIdentities()
	return s.slipPost(t, SlipClaimPath, body, append([]func(*http.Request){func(r *http.Request) {
		head(r)
		r.Header.Set(HeaderSlip, slip)
	}}, mutate...)...)
}

// claimOK claims a slip and answers what the service answered, failing the test
// on any refusal. It does NOT print the answer: the claim secret is in it.
func (s *slips) claimOK(t *testing.T, slip, body string, mutate ...func(*http.Request)) SlipClaimResponse {
	t.Helper()
	resp, b := s.claimCall(t, slip, body, mutate...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the claim is answered %d, want 200: %s", resp.StatusCode, b)
	}
	var ans SlipClaimResponse
	if err := json.Unmarshal(b, &ans); err != nil {
		t.Fatalf("the claim's answer is not one this contract reads: %v", err)
	}
	return ans
}

// withClaim is a call presenting a claim secret.
func withClaim(secret string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(HeaderSlipClaim, secret) }
}

// ---------------------------------------------------------------------------
// The requests, built from the contract's own types
// ---------------------------------------------------------------------------

// grantBody is a grant's body: the run the launcher asks for, the account it
// expects to see at the claim, how long the run may be served and the
// credentials it names. It is built from the request type itself, so a field
// renamed in the contract is renamed here too and no test can pass against a
// service that reads a shape this file does not write.
func grantBody(t *testing.T, slug, run, account string, maxRuntimeSeconds int64, credentials ...string) string {
	t.Helper()
	return mustBody(t, SlipGrantRequest{
		Slug: slug, Run: run, Account: account,
		Credentials: credentials, MaxRuntimeSeconds: maxRuntimeSeconds,
	})
}

// claimBody is a claim's body: the Cloud Run execution the claim records for
// audit (D1937) — "" is a caller with none to state, which is a call this
// contract reads.
func claimBody(t *testing.T, execution string) string {
	t.Helper()
	return mustBody(t, SlipClaimRequest{Execution: execution})
}

func mustBody(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("the contract's own type does not marshal: %v", err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------

// wantRefusal holds an answer to the wire contract's refusal: the status the
// caller acts on and the error type it reads. The message is deliberately not
// pinned — it is the service's own sentence, and what a test pins about it is
// that no value of the call is in it, which the lines below assert where a line
// is written.
func wantRefusal(t *testing.T, resp *http.Response, body []byte, status int, typ string) WireError {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, status, body)
	}
	e := refusal(t, resp.StatusCode, body)
	if e.Error != typ {
		t.Errorf("the refusal is %q, want %q: %s", e.Error, typ, body)
	}
	return e
}

// wantSlipLine holds one audit line to the contract's shape and to the fields
// the act carries. It is checkAuditShape (audit_test.go) plus the four fields a
// test names, and it is written here rather than inline so that every line of
// these two routes is held to the SAME shape: a line that grew a field — which
// is how a value would reach it — fails wherever it is read.
func wantSlipLine(t *testing.T, line map[string]any, purpose, status string, fields map[string]any) {
	t.Helper()
	checkAuditShape(t, line)
	if got := line["purpose"]; got != purpose {
		t.Errorf("the line's purpose is %v, want %q", got, purpose)
	}
	if got := line["status"]; got != status {
		t.Errorf("the line's status is %v, want %q", got, status)
	}
	for field, want := range fields {
		if got := line[field]; got != want {
			t.Errorf("the line's %s is %v, want %q", field, got, want)
		}
	}
}

// mustUserName is a credential name of the per-user grammar, built through the
// grammar itself so a test cannot state an id or a name the intake could never
// write.
func mustUserName(t *testing.T, id, name string) string {
	t.Helper()
	secret, err := secretname.User(id, name)
	if err != nil {
		t.Fatalf("the grammar refuses the name %q of %q: %v", name, id, err)
	}
	return secret
}

// ---------------------------------------------------------------------------
// The grant
// ---------------------------------------------------------------------------

// TestServiceNeedsASlipStore: the store is required at construction, like the
// state and the probe lease, and for the reason the guard itself gives — a
// service that cannot write a slip can grant no run anything, so it is not a
// service this build starts. Without the guard the grant would reach a nil
// store and the answer would be a panic, which is a refusal an operator reads
// as a crash and a run cannot act on.
func TestServiceNeedsASlipStore(t *testing.T) {
	var captured *Config
	newSvcWith(t, func(_ *svc, cfg *Config) { captured = cfg })
	if captured == nil {
		t.Fatal("the fixture handed the test no configuration")
	}
	without := *captured
	without.Slips = nil
	if _, err := NewServer(without); err == nil {
		t.Error("NewServer accepted a configuration whose slip store is nil")
	}
	// And the fixture's own configuration, which has one, is one it accepts.
	if _, err := NewServer(*captured); err != nil {
		t.Errorf("NewServer refused the fixture's own configuration: %v", err)
	}
}

// TestGrantRefusedForAnotherOwnersToken: R7's owner-only rule, at the one place
// it can be enforced for a whole run — the grant. A per-user credential is
// served only to the person whose Google account id its name carries, and the
// launcher asking for the slip IS that person (D1938 keys the class on a
// verified email and answers the token's numeric sub), so the launcher's own
// `fugaro_u_<sub>_<name>` is the only per-user name it may put in a slip.
//
// The three properties, and why each is here:
//
//   - the launcher's own per-user credential is granted, and so is a
//     `fugaro_p_` one: sharing is a project-shared name under a pool's
//     allowed_users, which the SERVICE decides from the published policy and
//     which is not this route's check (R11, G8, DS3.2);
//   - another account's per-user credential is refused as a request this
//     contract does not read, before the slip is written at all;
//   - the refusal writes exactly ONE audit line, it names the verified launcher
//     and no run, no job and no credential, and the database is left with the
//     one slip the first grant wrote — a refused grant is not a grant.
func TestGrantRefusedForAnotherOwnersToken(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	own := mustUserName(t, testLauncherSub, "model-token")
	shared, err := secretname.Project("team-token")
	if err != nil {
		t.Fatalf("the grammar refuses a project-shared name: %v", err)
	}
	other := mustUserName(t, testOtherSub, "model-token")

	granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, own, shared))
	if got := s.slipIDs(t); len(got) != 1 || got[0] != granted.Slip {
		t.Fatalf("the grant wrote %v, want exactly the slip it answered (%q)", got, granted.Slip)
	}
	sl := s.slip(t, granted.Slip)
	if !slices.Contains(sl.Allowed, own) || !slices.Contains(sl.Allowed, shared) {
		t.Errorf("the slip permits %v, want the two names the grant asked for", sl.Allowed)
	}
	if sl.Owner != testLauncherSub {
		t.Errorf("the slip's owner is %q, want the launcher's verified sub %q", sl.Owner, testLauncherSub)
	}

	resp, body := s.grantCall(t, grantBody(t, s.slug, s.run, testJobSA, 3600, other))
	wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)
	if got := s.slipIDs(t); len(got) != 1 {
		t.Errorf("a refused grant wrote %v, want no second record", got)
	}

	lines := auditLines(t, s.audit)
	if len(lines) != 1 {
		t.Fatalf("the grant wrote %d audit lines, want exactly 1 (the refusal; a grant that granted nothing has no line to write): %v", len(lines), lines)
	}
	wantSlipLine(t, lines[0], PurposeSlipGrant, ErrorRequestRefused, map[string]any{
		"caller": testLauncherAccount, "run": "", "repository": "", "job": "",
		"credential": "", "tier": "",
	})
}

// TestGrantTakesNoRunIdentity: the grant's door, and the sibling of
// TestRunIdentityRefusedOnReadRoute (auth_test.go) one class along. A grant is
// asked for BEFORE the run exists, so a caller presenting a run identity — or
// naming a job — is a call of a shape this route does not have. The caller was
// verified by its own class by then, so the refusal is written: exactly one
// line, naming the launcher and the act, with no run, no job and no repository
// in it. It is what keeps the launcher class from becoming a second way in for
// a run, and no slip may be written by a call it refuses.
func TestGrantTakesNoRunIdentity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra func(*http.Request)
	}{
		{"a run identity", func(r *http.Request) { r.Header.Set(HeaderRunIdentity, "a-run-identity-token") }},
		{"a job name", func(r *http.Request) { r.Header.Set(HeaderRunJob, "fugaro-a-job") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSlips(t, testLauncherAccount)
			resp, body := s.grantCall(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential), tc.extra)
			wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)
			if got := s.slipIDs(t); len(got) != 0 {
				t.Errorf("a grant refused at the door wrote %v", got)
			}
			lines := auditLines(t, s.audit)
			if len(lines) != 1 {
				t.Fatalf("the refused grant wrote %d audit lines, want exactly 1: %v", len(lines), lines)
			}
			wantSlipLine(t, lines[0], PurposeSlipGrant, ErrorUnverifiedRunIdentity, map[string]any{
				"caller": testLauncherAccount, "run": "", "repository": "", "job": "",
				"credential": "", "tier": "",
			})
		})
	}
}

// TestAnUnknownBodyMemberIsRefused: both new bodies decode STRICTLY, as every
// body of this service does. A member this release does not model is a refusal
// rather than a member silently dropped — and on these two routes the dropped
// member would be the worst kind: a grant that permits nothing, or one that
// never ends, decided by a field nobody read.
func TestAnUnknownBodyMemberIsRefused(t *testing.T) {
	// The contract's own body with one member this release does not model
	// appended, so the refusal is the unknown member and not a body that was
	// malformed in some other way.
	withUnknown := func(body string) string {
		return strings.TrimSuffix(body, "}") + `,"unknown":1}`
	}

	t.Run("the grant's body", func(t *testing.T) {
		s := newSlips(t, testLauncherAccount)
		body := withUnknown(grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))
		resp, answer := s.grantCall(t, body)
		wantRefusal(t, resp, answer, StatusRequestRefused, ErrorRequestRefused)
		if got := s.slipIDs(t); len(got) != 0 {
			t.Errorf("a grant whose body carried an unknown member wrote %v", got)
		}
		lines := auditLines(t, s.audit)
		if len(lines) != 1 {
			t.Fatalf("the refused grant wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		wantSlipLine(t, lines[0], PurposeSlipGrant, ErrorRequestRefused, map[string]any{
			"caller": testLauncherAccount, "run": "", "repository": "", "job": "",
			"credential": "", "tier": "",
		})
	})

	t.Run("the claim's body", func(t *testing.T) {
		s := newSlips(t, testLauncherAccount)
		granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))
		body := withUnknown(claimBody(t, "fugaro-aurora-app-abc12"))
		resp, answer := s.claimCall(t, granted.Slip, body)
		wantRefusal(t, resp, answer, StatusRequestRefused, ErrorRequestRefused)
		if sl := s.slip(t, granted.Slip); sl.ClaimHash != "" {
			t.Error("a claim whose body carried an unknown member claimed the slip")
		}
		lines := auditLines(t, s.audit)
		if len(lines) != 1 {
			t.Fatalf("the refused claim wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		wantSlipLine(t, lines[0], PurposeSlipClaim, ErrorRequestRefused, map[string]any{
			"run": s.run, "repository": s.slug, "job": s.job, "caller": testJobSA,
			"credential": "", "tier": "",
		})
	})
}

// TestTheSlipHeadersAreReadOnce: the two headers the new routes read carry one
// value each — the slip the call is about, and the claim secret it presents —
// and a call that sends either twice is a request this contract does not read.
// It is the rule every other header of this service keeps (oneHeader), and on
// these two it matters more than usual: a header read twice is a service
// choosing between two values a caller sent, which is how a fence becomes a
// guess.
func TestTheSlipHeadersAreReadOnce(t *testing.T) {
	t.Run("the slip id", func(t *testing.T) {
		s := newSlips(t, testLauncherAccount)
		granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))
		// claimCall sets the header once; this adds a second value to it.
		resp, body := s.claimCall(t, granted.Slip, claimBody(t, ""), func(r *http.Request) {
			r.Header.Add(HeaderSlip, granted.Slip)
		})
		wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)
		if sl := s.slip(t, granted.Slip); sl.ClaimHash != "" {
			t.Error("a call carrying two slip ids claimed the slip")
		}
		lines := auditLines(t, s.audit)
		if len(lines) != 1 {
			t.Fatalf("the refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		wantSlipLine(t, lines[0], PurposeSlipClaim, ErrorRequestRefused, map[string]any{
			"run": s.run, "repository": s.slug, "job": s.job, "caller": testJobSA,
			"credential": "", "tier": "",
		})
	})

	t.Run("the claim secret", func(t *testing.T) {
		s := newSlips(t, testLauncherAccount)
		granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))
		resp, body := s.claimCall(t, granted.Slip, claimBody(t, ""), func(r *http.Request) {
			r.Header.Add(HeaderSlipClaim, fakeCred(t, "one-claim-secret"))
			r.Header.Add(HeaderSlipClaim, fakeCred(t, "another-claim-secret"))
		})
		wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)
		if sl := s.slip(t, granted.Slip); sl.ClaimHash != "" {
			t.Error("a call presenting two claim secrets claimed the slip")
		}
		lines := auditLines(t, s.audit)
		if len(lines) != 1 {
			t.Fatalf("the refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		wantSlipLine(t, lines[0], PurposeSlipClaim, ErrorRequestRefused, map[string]any{
			"run": s.run, "repository": s.slug, "job": s.job, "caller": testJobSA,
			"credential": "", "tier": "",
		})
	})
}

// TestSlipIDAloneIsNotEnough: G3. The id is not a bearer ticket — it travels to
// the job in its environment and the agent can read that (F22) — so every call
// is admitted by what the slip binds and never by the id.
//
// Four calls, and the id is all they have in common:
//
//   - the id and nothing else: this contract's own read of the request refuses
//     it before any class is consulted, and NOTHING is recorded, because there
//     is no verified caller for a line to name;
//   - the run's two identities and no id: a call this route does not have, and
//     it is refused with exactly one line — the caller IS verified, so the
//     refusal is the run's own;
//   - an id that names no grant: refused as not this run's, because an id this
//     service never wrote is nobody's;
//   - an id that is not one header value: refused as a request this contract
//     does not read, before the database is asked at all.
func TestSlipIDAloneIsNotEnough(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))

	resp, body := s.slipPost(t, SlipClaimPath, claimBody(t, ""), func(r *http.Request) {
		r.Header.Set(HeaderSlip, granted.Slip)
	})
	wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)
	if s.audit.Len() != 0 {
		t.Errorf("a call refused before its caller was verified wrote an audit line: %s", s.audit.String())
	}

	resp, body = s.slipPost(t, SlipClaimPath, claimBody(t, ""), s.jobIdentities())
	wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)

	resp, body = s.claimCall(t, "no-such-slip", claimBody(t, ""))
	wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)

	resp, body = s.claimCall(t, "not a slip id", claimBody(t, ""))
	wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)

	// Three lines, one per refused call after the first: each names the
	// VERIFIED run, and each states the refusal of its own class — which is the
	// other half of "the id is not a ticket". The missing id is a refused
	// request; an id this service never wrote and an id that is not one header
	// value are told apart by their status, so the three lines cannot be one
	// line written three times by accident.
	want := []struct{ status, why string }{
		{ErrorRequestRefused, "the run's identities and no id: a call this route does not have"},
		{ErrorUnverifiedRunIdentity, "an id that names no grant: an id this service never wrote is nobody's"},
		{ErrorRequestRefused, "an id that is not one header value: refused before the database is asked"},
	}
	lines := auditLines(t, s.audit)
	if len(lines) != len(want) {
		t.Fatalf("the refused calls wrote %d audit lines, want exactly %d: %v", len(lines), len(want), lines)
	}
	for i, w := range want {
		wantSlipLine(t, lines[i], PurposeSlipClaim, w.status, map[string]any{
			"run": s.run, "repository": s.slug, "job": s.job, "caller": testJobSA,
			"credential": "", "tier": "",
		})
	}
}

// TestOtherRunCannotClaim: F23/R8. Runs of one repository share a job account
// and a launcher can mint a run identity for any of them (SECURITY.md), so the
// account alone does not say which run is calling. The slip names the run as
// well, and a claim of another run of the same repository is refused.
//
// The refused claim must also leave the record untouched: the slip is still
// unclaimed, so the run it names can take it afterwards. A refusal that
// consumed the slip would be a way to deny a run its credentials by guessing
// its id.
func TestOtherRunCannotClaim(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))
	other := otherRun()

	resp, body := s.claimCall(t, granted.Slip, claimBody(t, ""), func(r *http.Request) {
		r.Header.Set(HeaderRunIdentity, s.runTokenFor(other))
	})
	wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)

	if sl := s.slip(t, granted.Slip); sl.ClaimHash != "" || sl.Claimed != (creds.Claim{}) {
		t.Errorf("a refused claim wrote a claim into the record: %+v", sl.Claimed)
	}
	claimed := s.claimOK(t, granted.Slip, claimBody(t, ""))
	if claimed.ClaimSecret == "" {
		t.Error("the claim of the run the slip names answered no claim secret")
	}
	// The line names the run that CALLED and not the run the slip names, which
	// is what the shared parse verified: a line whose run was the slip's would
	// be recording an identity this service never saw, and it is the one fact an
	// operator reads to see which run tried to take another's slip.
	wantSlipLine(t, auditLines(t, s.audit)[0], PurposeSlipClaim, ErrorUnverifiedRunIdentity, map[string]any{
		"run": other, "repository": s.slug, "job": s.job, "caller": testJobSA,
	})
}

// TestCallWithoutClaimSecretRefused: D1941. The claim answers a claim secret
// exactly once, and every call after it must present that secret (G3, R8). The
// secret is what fences one launcher from another: another launcher can mint
// the same run identity and is refused the claim (TestSecondClaimRefused), but
// without the secret it must also be refused everything the claimed slip would
// serve.
//
// Three calls after the claim: no secret, a secret that is not the one, and the
// one. The first two are refused and write one line each; the third is served,
// and what it answers is a reading of the SAME claim — the secret is not
// re-minted and the expiry does not move.
func TestCallWithoutClaimSecretRefused(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))
	claimed := s.claimOK(t, granted.Slip, claimBody(t, "fugaro-aurora-app-review"))
	if claimed.ClaimSecret == "" {
		t.Fatal("the claim answered no claim secret")
	}
	if claimed.ExpiresAt.IsZero() {
		t.Fatal("the claim answered no expiry")
	}

	resp, body := s.claimCall(t, granted.Slip, claimBody(t, ""))
	wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)

	resp, body = s.claimCall(t, granted.Slip, claimBody(t, ""), withClaim(fakeCred(t, "wrong-claim-secret")))
	wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)

	kept := s.claimOK(t, granted.Slip, claimBody(t, ""), withClaim(claimed.ClaimSecret))
	if kept.ClaimSecret != "" {
		t.Error("a call that presented the claim secret was answered a second one")
	}
	if !kept.ExpiresAt.Equal(claimed.ExpiresAt) {
		t.Errorf("the call's expiry is %s, want the claim's own %s: the runtime is measured from the claim and not from every call", kept.ExpiresAt, claimed.ExpiresAt)
	}

	lines := auditLines(t, s.audit)
	if len(lines) != 2 {
		t.Fatalf("the two refused calls wrote %d audit lines, want exactly 2: %v", len(lines), lines)
	}
	for _, line := range lines {
		wantSlipLine(t, line, PurposeSlipClaim, ErrorUnverifiedRunIdentity, map[string]any{
			"run": s.run, "repository": s.slug, "job": s.job, "caller": testJobSA,
		})
	}
}

// TestAnotherRunCannotReadASlipBack: the READING form's half of F23. The claim
// is once-only, so every call after it presents the claim secret and is
// answered the claim's own reading (D1941) — and that call is decided by the
// SAME binding: the slip's run, its repository and its expected account. A
// second run of the repository holds the secret (a launcher mints run
// identities, SECURITY.md) and must still be refused, because the slip is not
// its own. This is the branch DS1.6 serves credential values from, so it is
// pinned before anything is served from it.
//
// The refusal is the ONE sentence every refusal the record makes is answered
// with, and exactly one line records it — naming the run that CALLED, which is
// the identity the shared parse verified.
func TestAnotherRunCannotReadASlipBack(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))
	claimed := s.claimOK(t, granted.Slip, claimBody(t, ""))
	if claimed.ClaimSecret == "" {
		t.Fatal("the claim answered no claim secret")
	}

	other := otherRun()
	resp, body := s.claimCall(t, granted.Slip, claimBody(t, ""),
		withClaim(claimed.ClaimSecret),
		func(r *http.Request) { r.Header.Set(HeaderRunIdentity, s.runTokenFor(other)) })
	if e := wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity); e.Message != slipNotThisRuns {
		t.Errorf("the refusal is answered %q, want the one sentence every refusal of the record gives (%q)", e.Message, slipNotThisRuns)
	}

	// The slip the run it names holds is untouched, and it still answers ITS
	// runner: a reading call refused by another run is not a revocation.
	kept := s.claimOK(t, granted.Slip, claimBody(t, ""), withClaim(claimed.ClaimSecret))
	if !kept.ExpiresAt.Equal(claimed.ExpiresAt) {
		t.Errorf("the reading answers the expiry %s, want the claim's own %s", kept.ExpiresAt, claimed.ExpiresAt)
	}
	lines := auditLines(t, s.audit)
	if len(lines) != 1 {
		t.Fatalf("the refused reading wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	wantSlipLine(t, lines[0], PurposeSlipClaim, ErrorUnverifiedRunIdentity, map[string]any{
		"run": other, "repository": s.slug, "job": s.job, "caller": testJobSA,
		"credential": "", "tier": "",
	})
}

// TestAReadingCallAfterTheRuntimeIsRefused: the reading form's other half, and
// R10's own point — a slip serves for the runtime it was granted and no longer.
// The secret is the right one and the identity is the slip's own run; what has
// run out is the slip, so the call is refused with the same one sentence, one
// line, and no reading. The clock is the fixture's, so the runtime is granted
// short and then passed rather than waited out.
func TestAReadingCallAfterTheRuntimeIsRefused(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	const runtimeSeconds = 60
	granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, runtimeSeconds, testSlipCredential))
	claimed := s.claimOK(t, granted.Slip, claimBody(t, ""))
	if claimed.ClaimSecret == "" {
		t.Fatal("the claim answered no claim secret")
	}

	// One second past what the slip was granted, measured from the claim (the
	// reading form's own boundary is the claim's instant and not the call's).
	s.now = s.now.Add((runtimeSeconds + 1) * time.Second)

	resp, body := s.claimCall(t, granted.Slip, claimBody(t, ""), withClaim(claimed.ClaimSecret))
	if e := wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity); e.Message != slipNotThisRuns {
		t.Errorf("the refusal is answered %q, want the one sentence every refusal of the record gives (%q)", e.Message, slipNotThisRuns)
	}
	lines := auditLines(t, s.audit)
	if len(lines) != 1 {
		t.Fatalf("the refused reading wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	wantSlipLine(t, lines[0], PurposeSlipClaim, ErrorUnverifiedRunIdentity, map[string]any{
		"run": s.run, "repository": s.slug, "job": s.job, "caller": testJobSA,
		"credential": "", "tier": "",
	})
}

// TestOtherJobAccountCannotClaim: D1937/R8. The unverifiable execution id the
// spec's step 4 would have bound is replaced by the job service account, which
// the token really carries — and one repository has one account per workflow,
// so the account a slip EXPECTS is a fact the grant states and the claim
// checks. Another job account of the same repository pairs (it is a job of the
// run's repository) and is refused by the slip.
func TestOtherJobAccountCannotClaim(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))

	sibling := s.jobToken(func(c map[string]any) { c["email"] = testSiblingSA })
	resp, body := s.claimCall(t, granted.Slip, claimBody(t, ""), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+sibling)
	})
	wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)

	if sl := s.slip(t, granted.Slip); sl.ClaimHash != "" {
		t.Error("a refused claim wrote a claim into the record")
	}
	s.claimOK(t, granted.Slip, claimBody(t, ""))
	wantSlipLine(t, auditLines(t, s.audit)[0], PurposeSlipClaim, ErrorUnverifiedRunIdentity, map[string]any{
		"run": s.run, "repository": s.slug, "job": s.job, "caller": testSiblingSA,
	})
}

// TestSecondClaimRefused: D1941's whole point. A slip is claimed ONCE. Another
// launcher that minted the same run identity (F28) arrives with a claim of its
// own, and it must be refused — and, just as important, the FIRST runner must
// keep what it was answered: the record still holds the first claim's hash and
// no new secret exists. A conditional claim that answered the loser a secret
// would lock the winner out of its own run's credentials.
func TestSecondClaimRefused(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))
	first := s.claimOK(t, granted.Slip, claimBody(t, ""))
	if first.ClaimSecret == "" {
		t.Fatal("the first claim answered no claim secret")
	}

	resp, body := s.claimCall(t, granted.Slip, claimBody(t, ""))
	wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)

	sl := s.slip(t, granted.Slip)
	if !sl.ClaimMatches(first.ClaimSecret) {
		t.Error("the second claim replaced the first claim's secret: the runner that claimed first is locked out")
	}
	if sl.Claimed.Account != testJobSA {
		t.Errorf("the record's claimant is %q, want %q", sl.Claimed.Account, testJobSA)
	}
	kept := s.claimOK(t, granted.Slip, claimBody(t, ""), withClaim(first.ClaimSecret))
	if !kept.ExpiresAt.Equal(first.ExpiresAt) {
		t.Errorf("the first runner's expiry is %s, want the claim's own %s", kept.ExpiresAt, first.ExpiresAt)
	}
	wantSlipLine(t, auditLines(t, s.audit)[0], PurposeSlipClaim, ErrorUnverifiedRunIdentity, map[string]any{
		"run": s.run, "repository": s.slug, "job": s.job, "caller": testJobSA,
	})
}

// TestClaimIsStoppedByTheKillSwitch: the operator's stop switch, read on the
// claim's own path. The claim is the branch that hands a run the claim secret
// its later calls are answered under (D1941), so a stopped service must not
// claim anything: the switch is read before the branch (server.go), and both
// switches stop it — the global one and the repository's own.
//
// What the test pins, in the order it matters: the refusal is the state one and
// not a refusal of the call's shape; the record is untouched, so the slip is
// still there for the run when the operator clears the switch — a stop is not a
// burn; exactly ONE audit line is written, under the claim's own purpose (a
// purpose taken from the branch after the switch would be "forward" here, which
// is the same trap TestMintIsStoppedByTheKillSwitch names for the mint); and the
// call the switch cleared is served.
func TestClaimIsStoppedByTheKillSwitch(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop func(*svc)
	}{
		{"the global switch", func(s *svc) { s.kills.global = true }},
		{"the repository's own switch", func(s *svc) { s.kills.repos[s.slug] = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSlips(t, testLauncherAccount)
			granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))
			tc.stop(s.svc)

			resp, body := s.claimCall(t, granted.Slip, claimBody(t, ""))
			wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable)
			if sl := s.slip(t, granted.Slip); sl.ClaimHash != "" || sl.Claimed != (creds.Claim{}) {
				t.Errorf("a stopped service claimed the slip: %+v", sl.Claimed)
			}
			lines := auditLines(t, s.audit)
			if len(lines) != 1 {
				t.Fatalf("the stopped claim wrote %d audit lines, want exactly 1: %v", len(lines), lines)
			}
			wantSlipLine(t, lines[0], PurposeSlipClaim, ErrorStateUnavailable, map[string]any{
				"run": s.run, "repository": s.slug, "job": s.job, "caller": testJobSA,
				"credential": "", "tier": "",
			})

			// The switch is a stop and not a refusal of this call: cleared, the
			// same slip is claimed by the same run.
			s.kills.global, s.kills.repos = false, nil
			if claimed := s.claimOK(t, granted.Slip, claimBody(t, "")); claimed.ClaimSecret == "" {
				t.Error("the claim after the switch cleared answered no claim secret")
			}
		})
	}
}

// TestEmptyLauncherListRefusesAll: F31. The launcher class's allow-list is the
// installation's roster, and an installation whose roster is empty — or whose
// roster the deployment did not manage to fill — grants nobody anything. It is
// fail-closed rather than open, and the refusal is the class's own: it is
// answered before anything of the call is read, so nothing is written and no
// line is written either (there is no verified caller for one to name).
func TestEmptyLauncherListRefusesAll(t *testing.T) {
	s := newSlips(t)
	resp, body := s.grantCall(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))
	wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedJobIdentity)
	if got := s.slipIDs(t); len(got) != 0 {
		t.Errorf("a service with no launchers wrote %v", got)
	}
	if s.audit.Len() != 0 {
		t.Errorf("a refusal by the class itself wrote an audit line: %s", s.audit.String())
	}
}

// TestJobRefusedOnGrantAndLauncherOnClaim: G13's cross-class rule, which is the
// pair the two routes exist for. A job service account may not ask for a slip —
// its run does not exist yet, and a grant is the launcher's act — and a
// launcher may not claim one, because a launcher holds no run identity and the
// claim is the run's first call.
//
// Both are refusals by a class, answered before anything of the call is read:
// neither writes a line, and neither writes a record.
func TestJobRefusedOnGrantAndLauncherOnClaim(t *testing.T) {
	s := newSlips(t, testLauncherAccount)

	resp, body := s.slipPost(t, SlipPath, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.jobToken())
	})
	wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedJobIdentity)

	resp, body = s.slipPost(t, SlipClaimPath, claimBody(t, ""), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.launcherToken())
		r.Header.Set(HeaderRunIdentity, s.runToken())
		r.Header.Set(HeaderRunJob, s.job)
		r.Header.Set(HeaderSlip, "no-such-slip")
	})
	wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedJobIdentity)

	if got := s.slipIDs(t); len(got) != 0 {
		t.Errorf("the two refused calls wrote %v", got)
	}
	if s.audit.Len() != 0 {
		t.Errorf("a call refused before its caller was verified wrote an audit line: %s", s.audit.String())
	}
}

// TestClaimExecutionIsOneCleanValue: the execution name a claim reports is the
// one free-text value either route carries, and it is the one value of them
// that is not a name of this service's own grammar — it is the caller's own
// string, and the service's line is where it lands (D1937: recorded and
// deciding nothing). A newline, an escape sequence or a bidi override in it
// would rewrite the line an operator reads it in, so it is held to one clean
// value at the door, as the proxy's own line keeps the same rule by carrying a
// class instead of a byte of the call.
//
// The refusal is this contract's refused REQUEST and it claims nothing: the
// slip is still there for the run to take with a name this service will keep.
func TestClaimExecutionIsOneCleanValue(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipCredential))

	for _, exec := range []string{
		"two words",
		strings.Repeat("a", maxSlipExecutionBytes+1),
		"line\nbreak",
		"bidi‮override",
	} {
		resp, body := s.claimCall(t, granted.Slip, claimBody(t, exec))
		wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)
	}
	if got := s.slip(t, granted.Slip); got.ClaimHash != "" {
		t.Error("a refused claim wrote a claim into the record")
	}
	lines := auditLines(t, s.audit)
	if len(lines) != 4 {
		t.Fatalf("the four refused calls wrote %d audit lines, want exactly 4: %v", len(lines), lines)
	}
	for _, line := range lines {
		wantSlipLine(t, line, PurposeSlipClaim, ErrorRequestRefused, map[string]any{
			"run": s.run, "repository": s.slug, "job": s.job, "caller": testJobSA,
			"credential": "", "tier": "",
		})
	}
	// Nothing a refused call sent reaches the service's own line either: the
	// refusal names the field and never the value, which is the whole point of
	// holding this field at the door.
	for _, sent := range []string{"two words", "line\nbreak", "bidi‮override"} {
		if strings.Contains(s.logs.String(), sent) {
			t.Errorf("a refused claim put what it sent on the service's own line: %s", s.logs.String())
		}
	}

	const exec = "fugaro-aurora-app-abc12"
	s.claimOK(t, granted.Slip, claimBody(t, exec))
	if logs := s.logs.String(); !strings.Contains(logs, "execution="+exec) {
		t.Errorf("the service's line does not carry the execution the claim reported: %s", logs)
	}
}

// TestGrantSlugIsOneCleanValue: the repository slug a grant names is a name of
// the launcher's own grammar, but it is ALSO caller text that lands on this
// service's own line (slipGrant), so it is held to one printable value at the
// door — the same rule the claim's execution name is held to, for the same
// reason: token.ValidSegment admits a bidi override (it is neither a control
// rune nor a space), and a bidi override in a value written raw to the line an
// operator reads it in rewrites that line. The fixture's handler is the text
// one, which escapes the rune and would hide the defect, so what is pinned here
// is the REFUSAL and not the line's bytes.
//
// The bound is held to task.Slug's own shape in the same breath: a real slug of
// the longest the launcher can mint is granted, one byte longer is not.
func TestGrantSlugIsOneCleanValue(t *testing.T) {
	s := newSlips(t, testLauncherAccount)

	longest, err := task.Slug("github", "acme/"+strings.Repeat("segment-", 20))
	if err != nil {
		t.Fatalf("building the longest slug a launcher can mint: %v", err)
	}
	if len(longest) != maxSlipSlugBytes {
		t.Fatalf("task.Slug's longest slug is %d bytes, want the bound %d: a real launcher's slug must fit the grant", len(longest), maxSlipSlugBytes)
	}
	for _, slug := range []string{
		s.slug,
		longest,
	} {
		granted := s.grantOK(t, grantBody(t, slug, s.run, testJobSA, 3600, testSlipCredential))
		if got := s.slip(t, granted.Slip).Slug; got != slug {
			t.Errorf("the record's slug is %q, want %q", got, slug)
		}
	}

	for _, slug := range []string{
		"bidi‮override",
		strings.Repeat("a", maxSlipSlugBytes+1),
		"two words",
		"line\nbreak",
	} {
		resp, body := s.grantCall(t, grantBody(t, slug, s.run, testJobSA, 3600, testSlipCredential))
		wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)
	}
	// A refused grant writes nothing at all, which is the other half of "the
	// refusal names the field and never the value".
	if ids := s.slipIDs(t); len(ids) != 2 {
		t.Errorf("the four refused grants left %d slips behind, want the two the granted ones wrote: %v", len(ids), ids)
	}
	// Nothing a refused call sent reaches the service's own line: the slug of a
	// refused grant is never written there, which is what the check at the door
	// is for. (The bidi rune is escaped by the fixture's text handler, so the
	// refusal above is the assertion that carries the rule; this is the belt to
	// that braces.)
	for _, sent := range []string{"bidi‮override", "two words", "line\nbreak"} {
		if strings.Contains(s.logs.String(), sent) {
			t.Errorf("a refused grant put what it sent on the service's own line: %s", s.logs.String())
		}
	}
}

// TestMaxSlipRuntimeIsThePlatformMaximum: R10's cap. A slip may serve a run for
// no longer than a run may live, and that bound is the compute platform's own
// (backend.MaxRunTime). The cap is spelled in slip.go rather than imported
// there, so that the service's binary carries no compute platform, and this is
// what holds the two spellings together: a value that drifted would either end
// a run the platform still lets work or grant a slip no run could use up.
//
// The request is CLAMPED rather than refused — a launcher asking for more is
// asking for the most there is — so the second half of the test is that the
// answer and the record both carry the cap and not what was asked for.
func TestMaxSlipRuntimeIsThePlatformMaximum(t *testing.T) {
	if maxSlipRuntime != backend.MaxRunTime {
		t.Errorf("a slip may serve for %s, want the platform's own maximum %s", maxSlipRuntime, backend.MaxRunTime)
	}
	s := newSlips(t, testLauncherAccount)
	cap := int64(backend.MaxRunTime / time.Second)
	granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, cap+3600, testSlipCredential))
	if granted.MaxRuntimeSeconds != cap {
		t.Errorf("a grant asking for %d seconds is answered %d, want the cap %d", cap+3600, granted.MaxRuntimeSeconds, cap)
	}
	if sl := s.slip(t, granted.Slip); sl.MaxRuntime != backend.MaxRunTime {
		t.Errorf("the record's runtime is %s, want the cap %s", sl.MaxRuntime, backend.MaxRunTime)
	}

	// And the clamp is applied in SECONDS, before the multiply: an ask no
	// duration can hold is still granted the cap and not refused, and it is not
	// wrapped into a slip that is expired (a negative or tiny runtime) or one
	// that never ends. The mutant this pins is `clampRuntime(time.Duration(n) *
	// time.Second)`, which overflows int64 for the value below.
	// The three asks: one that wraps NEGATIVE (refused as a request this
	// contract does not read, which is what the old shape answered), one that
	// wraps to 290448384 ns — a third of a second, so a slip that is over
	// before the run could use it, answered 200 — and the cap's own edge.
	for _, ask := range []int64{math.MaxInt64, 18446744074, cap + 1, maxSlipRuntimeSeconds + 1} {
		granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, ask, testSlipCredential))
		if granted.MaxRuntimeSeconds != cap {
			t.Errorf("a grant asking for %d seconds is answered %d, want the cap %d", ask, granted.MaxRuntimeSeconds, cap)
		}
		if sl := s.slip(t, granted.Slip); sl.MaxRuntime != backend.MaxRunTime {
			t.Errorf("a grant asking for %d seconds wrote the runtime %s, want the cap %s", ask, sl.MaxRuntime, backend.MaxRunTime)
		}
		// And the run can use it: a wrapped runtime would reach the run as a
		// slip that is already over, so the claim's own answer is read too.
		if claimed := s.claimOK(t, granted.Slip, claimBody(t, "")); claimed.MaxRuntimeSeconds != cap {
			t.Errorf("claiming a slip granted for %d seconds is answered the runtime %d, want the cap %d", ask, claimed.MaxRuntimeSeconds, cap)
		}
	}
}

// TestServedRoutesAreExactly: G10. The slip routes are this service's own
// contract and not an admin surface, and the way to hold that is to pin the
// served set exactly: every route the contract has is served with ITS method
// and reaches a handler (not the 404), and every near miss — another method, a
// trailing slash, a longer path, a path one character off — is nothing to
// reach.
//
// The second half of the pin is the 404's own sentence: it lists the served
// paths, so it is what an operator reads to find out what this service is. A
// route added to the service without the sentence (or a path in the sentence
// that is not served) fails here.
func TestServedRoutesAreExactly(t *testing.T) {
	s := newSlips(t, testLauncherAccount)

	served := []struct{ method, path string }{
		{http.MethodPost, MessagesPath},
		{http.MethodPost, CountTokensPath},
		{http.MethodPost, MintPath},
		{http.MethodPost, SignPath},
		{http.MethodPost, VerifyPath},
		{http.MethodPost, BitbucketReadPath},
		{http.MethodPost, GitHubReadPath},
		{http.MethodPost, SlipPath},
		{http.MethodPost, SlipClaimPath},
		{http.MethodGet, BitbucketProxyPrefix + "acme/aurora.git/info/refs"},
		{http.MethodPost, BitbucketProxyPrefix + "acme/aurora.git/git-receive-pack"},
		{http.MethodPut, BitbucketProxyPrefix + "acme/aurora.git/lfs/objects/batch"},
	}
	for _, tc := range served {
		t.Run("served "+tc.method+" "+tc.path, func(t *testing.T) {
			// The pin is what the SERVICE answers and not what servedRoute says:
			// a route the gate admits and no handler answers is a 404 all the
			// same, and a route that is served without the gate admitting it is
			// a route D143's "no admin surface" does not hold.
			resp, body := s.postRaw(t, tc.method, tc.path)
			if resp.StatusCode == http.StatusNotFound {
				t.Fatalf("%s %s is answered 404: %s", tc.method, tc.path, body)
			}
		})
	}

	notServed := []struct{ method, path string }{
		{http.MethodGet, SlipPath},
		{http.MethodPut, SlipPath},
		{http.MethodDelete, SlipPath},
		{http.MethodGet, SlipClaimPath},
		{http.MethodPost, SlipPath + "/"},
		{http.MethodPost, SlipClaimPath + "/"},
		{http.MethodPost, SlipPath + "/other"},
		{http.MethodPost, SlipClaimPath + "/other"},
		{http.MethodPost, "/v1/slips"},
		{http.MethodPost, "/v1/slip/claim/../claim"},
		{http.MethodGet, MessagesPath},
		{http.MethodGet, GitHubReadPath},
		{http.MethodPost, "/v1/admin"},
		{http.MethodPost, "/admin/slip"},
	}
	for _, tc := range notServed {
		t.Run("not served "+tc.method+" "+tc.path, func(t *testing.T) {
			resp, body := s.postRaw(t, tc.method, tc.path, func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+s.jobToken())
				r.Header.Set(HeaderRunIdentity, s.runToken())
				r.Header.Set(HeaderRunJob, s.job)
			})
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("%s %s is answered %d, want 404: this service serves no such route", tc.method, tc.path, resp.StatusCode)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != "not_found" {
				t.Errorf("the refusal is %q, want not_found", e.Error)
			}
			for _, p := range []string{MessagesPath, CountTokensPath, MintPath, SignPath, VerifyPath, BitbucketReadPath, GitHubReadPath, SlipPath, SlipClaimPath} {
				if !strings.Contains(e.Message, p) {
					t.Errorf("the 404's sentence does not name the route %q: %s", p, e.Message)
				}
			}
		})
	}
}
