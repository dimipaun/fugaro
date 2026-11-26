package credsvc

// PL6's tests: a service account's pool (D3181, D2976).
//
// The plan's three claims, and where each of them is read:
//
//   - `TestAServiceAccountsPool`: a run whose slip names a verified service
//     account resolves "the launcher's pool" to the project pool whose
//     principal it is — the one an admin fills, because a pipeline's run holds
//     no session to fill or designate one (D3181). It is the state such a run is
//     in: it has designated nothing, and the answer is the account's own pool
//     rather than "none". The last subtest is the whole loop, through
//     ServeHTTP: a real grant asked for by `VerifyServiceAccountLauncher`, a
//     real claim, and a model call served from the account's pool — which is
//     what PL6's Depends-on means by "it resolves a service account from the
//     grant CS14 teaches to admit that class".
//   - `TestAServiceAccountNeverGetsAPersonsPool`: D2976. A pool whose principal
//     is a PERSON is never that account's, however its display `owner` or its
//     designation reads, and a person's run is never served the account's pool
//     either — the resolution reads the pool's own principal against the
//     verified principal on the slip and nothing else (R7, R21).
//   - `TestAReissuedRunKeepsItsLauncher`: R8's record, which PR3 writes and this
//     lookup reads: a run the orchestrator reissues carries the launcher of the
//     ORIGINAL slip and the pool that launch named. So a resumed run of a
//     service account's launch resolves the account's pool with no designation
//     beside it, the pool the launch named wins over any designation, and a run
//     that resumes a person's paused run draws on that person's pool (D3191) —
//     never on the account's the job happens to run as.
//
// Everything here is hermetic (Convention 9): the designations are written
// through internal/creds' own store over internal/gcpfake's Realtime Database,
// so a designation is read back from the record the route really wrote, and
// every value is a NAME and never a credential (Convention 2).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

const (
	// saOwnPool is the project pool an admin filled for the fixture's service
	// account: the pool whose principal is that account's own address (D3181).
	saOwnPool = "sa-pool"
	// saOtherPool is a second pool of the same account's, for the one case a
	// designation decides which of them answers.
	saOtherPool = "sa-other-pool"
	// saPersonsPool is a PERSON's pool — its principal is that person's Google
	// subject — whose display owner is written with the account's address, which
	// is the mutation D2976 turns on.
	saPersonsPool = "person-pool"
)

// saPrincipal is the fixture's launcher, as its slip records one (R7): one of
// the installation's roster service accounts, and the account's own ADDRESS —
// never the numeric id beside it, which is the account's `Owner` and no
// principal.
func saPrincipal() routing.Principal {
	return routing.Principal{Kind: routing.PrincipalServiceAccount, ID: testRosterServiceAccount}
}

// saPool is the pool the fixture's admin filled for that account: the account's
// address as the pool's OWNER (the roster member the grant is checked against)
// and as its PRINCIPAL (the enforcing half PL6 resolves from), with the two
// members the chain fixture's bundle holds.
func saPool(name string) config.Pool {
	return lpPool(name, testRosterServiceAccount,
		lpPoolPrincipal(routing.PrincipalServiceAccount, testRosterServiceAccount),
		testPoolCredential, testSecondPoolCredential)
}

// newServiceAccountPools is newLauncherPools with the roster's
// service-account class wired (LM3, D3123: AuthConfig.LauncherServiceAccounts),
// which PL4's own fixture deliberately does not hold: the class is what lets a
// service account ask for a grant at all, and the grant it asks for is the slip
// this file resolves a pool from. Everything else is that fixture's — a
// migrated policy carrying `model_routing`, the chain fixture's bundle and
// admissions, and, where storeNet is true, a REAL database behind Config.DB, so
// a designation is written and read back through the store the deployment uses.
// storeNet false is that fixture's other wiring and needs no third one: the
// default Config.DB patches only, so a designation cannot be READ from it.
func newServiceAccountPools(t *testing.T, storeNet bool, pools ...config.Pool) *lpFixture {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		if storeNet {
			cfg.DB = client
		}
		auth, err := NewAuthenticator(AuthConfig{
			Project:         testProject,
			FirebaseProject: testFirebase,
			Audience:        testAudience,
			JobAccounts:     []string{testJobSA, testSiblingSA},
			Keys:            s.keys,
			JobKeys:         s.jobKeys,
			Outcomes:        RTDBOutcomes{DB: s.db},
			LauncherAccounts: []string{
				testLauncherAccount, testOtherLauncherAccount,
			},
			LauncherServiceAccounts: []string{testRosterServiceAccount},
			Now:                     func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's launcher class cannot be configured: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		s.src.docs = [][]byte{chainBundleDoc(t)}
		s.policy.shared = decodeTestPolicy(t, lpPolicyDoc(t, pools...))
		s.policy.admissions[s.slug] = decodeTestChild(t, chainAdmits())
	})
	installTable(t, lpTable())
	return &lpFixture{svc: s, db: db, dbs: client}
}

// withEmptyServiceAccountPool is the fixture whose published node carries a pool
// of the account's with no member: written onto the DECODED policy and not into
// the document, because the reader refuses such a pool where it is published (a
// pool with no members serves nothing) — see lpFixture.withEmptyPool, which is
// the same state for a person's pool.
func (f *lpFixture) withEmptyServiceAccountPool(t *testing.T, name string) *lpFixture {
	t.Helper()
	f.policy.shared.CraftPools = append(f.policy.shared.CraftPools,
		lpPool(name, testRosterServiceAccount,
			lpPoolPrincipal(routing.PrincipalServiceAccount, testRosterServiceAccount)))
	return f
}

// saGrant is lpGrant as the SERVICE ACCOUNT asks for one: the bearer is the
// account's own Google ID token, minted for this service's own URL alone
// (verifyServiceAccountCaller), which is the class's whole admission. The rest
// of the exchange is the run's and not the launcher's, so lpClaim and the calls
// after it are the PL4 fixture's own.
func (f *lpFixture) saGrant(t *testing.T, body string) SlipGrantResponse {
	t.Helper()
	bearer := func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.jobToken(func(c map[string]any) {
			c["email"], c["sub"] = testRosterServiceAccount, testRosterServiceAccountSub
		}))
	}
	resp := f.postWith(t, f.http.Client(), SlipPath, body, "", "", "", bearer)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the grant's answer: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the service account's grant is %d, want 200: %s", resp.StatusCode, raw)
	}
	var ans SlipGrantResponse
	if err := json.Unmarshal(raw, &ans); err != nil {
		t.Fatalf("the grant's answer is not one this contract reads: %v", err)
	}
	return ans
}

// ---------------------------------------------------------------------------
// A service account's pool is the project pool whose principal it is
// ---------------------------------------------------------------------------

// TestAServiceAccountsPool is PL6's red step: a run whose slip names a verified
// service account resolves "the launcher's pool" to the published pool whose
// principal that account is (D3181) — the pool an admin fills, and the state
// such a run is really in, since it has designated nothing. Against the lookup
// before PL6 every subtest here resolved to "none" and the walk fell back to the
// repository's key behind the source.
func TestAServiceAccountsPool(t *testing.T) {
	t.Run("the pool whose principal the account is, with no designation", func(t *testing.T) {
		s := newServiceAccountPools(t, true, append(lpPools(), saPool(saOwnPool))...)
		w := s.walk(t, callBody("sa-pool"), lpBinding(saPrincipal(), lpCoverage(), ""))
		wantPool(t, w, saOwnPool)
	})

	t.Run("the pool the account designated itself", func(t *testing.T) {
		// A service account may still designate one (the route admits its
		// class), and a designation is the principal's own statement: where one
		// is written it decides, and the account's other pool is not reached by
		// the principal-match beside it.
		s := newServiceAccountPools(t, true, append(lpPools(), saPool(saOwnPool), saPool(saOtherPool))...)
		s.designates(t, saPrincipal(), testProject, saOtherPool)
		w := s.walk(t, callBody("sa-pool-designated"), lpBinding(saPrincipal(), lpCoverage(), ""))
		wantPool(t, w, saOtherPool)
	})

	t.Run("the address is read as the roster reads it", func(t *testing.T) {
		// The class admits the account's token BY the fold (foldLauncherServiceAccounts),
		// so two spellings of one address are one caller there and one pool here:
		// a pool whose principal was written in another case is that account's,
		// and a run must not fall back to a paid source over the case of a letter.
		odd := lpPool(saOwnPool, testRosterServiceAccount,
			lpPoolPrincipal(routing.PrincipalServiceAccount, strings.ToUpper(testRosterServiceAccount)),
			testPoolCredential, testSecondPoolCredential)
		s := newServiceAccountPools(t, true, append(lpPools(), odd)...)
		w := s.walk(t, callBody("sa-pool-case"), lpBinding(saPrincipal(), lpCoverage(), ""))
		wantPool(t, w, saOwnPool)
	})

	t.Run("a project that publishes no pool of the account's", func(t *testing.T) {
		// No pool of the account's is one "none" (D3180): the walk moves on to
		// the chain's next source, and a project that named none in its table
		// serves the run from the key behind it.
		s := newServiceAccountPools(t, true, lpPools()...)
		w := s.walk(t, callBody("sa-pool-absent"), lpBinding(saPrincipal(), lpCoverage(), ""))
		wantFallback(t, w)
	})

	t.Run("the account's pool with no members", func(t *testing.T) {
		s := newServiceAccountPools(t, true, lpPools()...).withEmptyServiceAccountPool(t, saOwnPool)
		w := s.walk(t, callBody("sa-pool-empty"), lpBinding(saPrincipal(), lpCoverage(), ""))
		wantFallback(t, w)
	})

	t.Run("a real service account's grant reaches it", func(t *testing.T) {
		// The whole loop, and the one subtest here whose binding is not
		// hand-built: the account asks for a slip over ServeHTTP, its run claims
		// it, and the run's model call is served from the account's pool. What
		// it holds is the PLAN's own sentence — a run whose slip names a
		// verified service account (VerifyServiceAccountLauncher) — because the
		// slip it resolves from is the one verifyCallSlip really builds, and a
		// reader that left the class's principal out of it would resolve no pool
		// here.
		s := newServiceAccountPools(t, true, saPool(saOwnPool))

		granted := s.saGrant(t, chainBody(t, s.slug, s.run, testJobSA, 3600, "", lpCoverage(),
			mustSharedToken(t, testPoolCredential), mustSharedToken(t, testSecondPoolCredential)))
		if granted.Slip == "" {
			t.Fatal("the grant answered no slip")
		}
		claimSecret := s.lpClaim(t, granted.Slip)

		resp, body := s.call(t, callBody("sa-real-slip"), func(r *http.Request) {
			r.Header.Set(HeaderSlip, granted.Slip)
			r.Header.Set(HeaderSlipClaim, claimSecret)
			r.Header.Set(HeaderStage, "coder")
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the run's call is %d, want 200: %s", resp.StatusCode, body)
		}
		var name routing.SourceName
		if err := json.Unmarshal([]byte(resp.Header.Get(HeaderSource)), &name); err != nil {
			t.Fatalf("the answer's source member %q is not a routing.SourceName: %v", resp.Header.Get(HeaderSource), err)
		}
		if name.Kind != routing.KindLauncherPool || name.Name != saOwnPool {
			t.Errorf("the call was served from %+v, want the service account's own pool %q: the run's slip carries the "+
				"account's verified address and the pool whose principal it is answers (D3181)", name, saOwnPool)
		}
		if got := resp.Header.Get(HeaderFallback); got != FallbackNo {
			t.Errorf("the answer says fallback=%q, want %q: the chain's first source served (C1)", got, FallbackNo)
		}
		sent := s.up.Seen()
		if len(sent) != 1 {
			t.Fatalf("the upstream saw %d calls, want exactly 1", len(sent))
		}
		switch auth := sent[0].Header.Get("Authorization"); auth {
		case "Bearer " + fakeCred(t, testPoolCredential), "Bearer " + fakeCred(t, testSecondPoolCredential):
		default:
			t.Errorf("the upstream was sent the credential %q, want one of %q's members", auth, saOwnPool)
		}
	})
}

// ---------------------------------------------------------------------------
// A service account never gets a person's pool
// ---------------------------------------------------------------------------

// TestAServiceAccountNeverGetsAPersonsPool is D2976: a service account's run
// never draws on a person's pool, and the reason is the same one PL4's own
// mutation test gives from the other seat — the resolution reads the POOL's own
// principal against the principal the slip carries, and neither the display
// `owner` an operator wrote nor a designation can move a run onto somebody
// else's tokens (R7, R21).
//
// The last subtest is the rule read the other way round: the account's pool is
// the account's, so a person's run is not served it either — a run draws on a
// pool whose principal it is, or on the project's, and on nothing beside.
func TestAServiceAccountNeverGetsAPersonsPool(t *testing.T) {
	t.Run("a person's pool whose owner names the account", func(t *testing.T) {
		// The pool is a PERSON's by its principal, and its `owner` — the display
		// text an operator reads, and the roster the grant is checked against —
		// names the account. The account gets nothing: the enforcing half is the
		// principal, and a pool's owner decides nothing on this path (R7, R21).
		mine := lpPool(saPersonsPool, testRosterServiceAccount,
			lpPoolPrincipal(routing.PrincipalPerson, testLauncherSub),
			testPoolCredential, testSecondPoolCredential)
		s := newServiceAccountPools(t, true, append(lpPools(), mine)...)
		w := s.walk(t, callBody("sa-forged-owner"), lpBinding(saPrincipal(), lpCoverage(), ""))
		wantFallback(t, w)
	})

	t.Run("the account names a person's pool with --pool", func(t *testing.T) {
		s := newServiceAccountPools(t, true, append(lpPools(), saPool(saOwnPool))...)
		w := s.walk(t, callBody("sa-named-other"), lpBinding(saPrincipal(), lpCoverage(), lpOtherPool))
		wantFallback(t, w)
	})

	t.Run("a designation of the account's that names a person's pool", func(t *testing.T) {
		// A designation is a statement about one principal and never a way
		// round the entitlement: one that names a pool the account does not own
		// answers "none" (D3180), which is the same rule the named case follows.
		s := newServiceAccountPools(t, true, append(lpPools(), saPool(saOwnPool))...)
		s.designates(t, saPrincipal(), testProject, lpOtherPool)
		w := s.walk(t, callBody("sa-designated-other"), lpBinding(saPrincipal(), lpCoverage(), ""))
		wantFallback(t, w)
	})

	t.Run("a person's run is not served the account's pool", func(t *testing.T) {
		s := newServiceAccountPools(t, true, append(lpPools(), saPool(saOwnPool))...)
		// The person designated nothing of their own, so their run resolves
		// "none" — the account's pool is not a pool they may be served.
		wantFallback(t, s.walk(t, callBody("person-needs-own"), lpBinding(lpLauncher(), lpCoverage(), "")))
		// And with their own designation in place the same run is served from
		// theirs, and never from the account's.
		s.designates(t, lpLauncher(), testProject, lpOwnPool)
		wantPool(t, s.walk(t, callBody("person-own"), lpBinding(lpLauncher(), lpCoverage(), "")), lpOwnPool)
	})
}

// ---------------------------------------------------------------------------
// A reissued run keeps its launcher and the pool its launch named
// ---------------------------------------------------------------------------

// TestAReissuedRunKeepsItsLauncher is R8's record (D3191): the launcher of a
// paused run, and the pool its launch named, are the service's own record of the
// run's GRANT, and the run that resumes it is bound to them — never to the class
// of the job that starts it, which is the orchestrator's service account.
//
// The bindings below are hand-built because the record is PR3's to write; what
// this file holds is that the LOOKUP reads it that way. The walks carry the
// orchestrator's own claim in the run identity's `rb` (the requested_by a
// caller writes) beside a slip that names somebody else, so a resolution that
// read the identity rather than the slip would answer the account's pool here
// and the plan's own sentence would be false.
func TestAReissuedRunKeepsItsLauncher(t *testing.T) {
	t.Run("a resumed run of a service account's launch resolves the account's pool", func(t *testing.T) {
		// The account launched a run, it paused, and the orchestrator reissued
		// it: the pool is the account's own, resolved with no designation beside
		// it — the state PR3's resume is really in, and the reason the
		// principal-match path exists at all (D3181, the plan's PR3 → PL6 line).
		s := newServiceAccountPools(t, true, append(lpPools(), saPool(saOwnPool))...)
		w := s.forged(t, testOrchestratorAccount, callBody("sa-reissued"),
			lpBinding(saPrincipal(), lpCoverage(), ""))
		wantPool(t, w, saOwnPool)
	})

	t.Run("and the pool that launch named", func(t *testing.T) {
		// The launch named the project's shared pool, and the resumed run draws
		// on it (D3191: a resumed run keeps the pool its launch named). The
		// account's designation, written after the fact, moves nothing: the name
		// is what the record carries.
		s := newServiceAccountPools(t, true, append(lpPools(), saPool(saOwnPool))...)
		s.designates(t, saPrincipal(), testProject, saOwnPool)
		w := s.forged(t, testOrchestratorAccount, callBody("sa-reissued-named"),
			lpBinding(saPrincipal(), lpCoverage(), lpSharedPool))
		wantPool(t, w, lpSharedPool)
	})

	t.Run("a run that resumes a person's paused run draws on that person's pool", func(t *testing.T) {
		// D3191's one case, and D2976 read through it: the job runs as the
		// orchestrator's service account, the slip names the PERSON whose run
		// paused, and the pool is that person's — the account's pool is not
		// reached, and the person is not read off the identity that presents the
		// call (R7, R8).
		s := newServiceAccountPools(t, true, append(lpPools(), saPool(saOwnPool))...)
		s.designates(t, lpLauncher(), testProject, lpOwnPool)
		w := s.forged(t, testOrchestratorAccount, callBody("person-resumed"),
			lpBinding(lpLauncher(), lpCoverage(), ""))
		wantPool(t, w, lpOwnPool)
	})

	t.Run("a store that cannot be read is an error and never none", func(t *testing.T) {
		// A service account's resolution consults the designation store like
		// every other principal's — the pool whose principal it is answers only
		// where NO designation was found — so the one answer that must never be
		// read as "none" is an error here too (D3180): a deployment that cannot
		// look cannot tell an absent designation from an unreadable one, and
		// "none" would serve the key behind a pool that exists.
		s := newServiceAccountPools(t, false, saPool(saOwnPool))
		ref, found, err := launcherPools{s: s.server}.LauncherPool(context.Background(), saPrincipal(), "")
		if err == nil {
			t.Fatalf("a deployment that cannot read a designation answered (%+v, %v, nil), want an error", ref, found)
		}
		if found || ref.Pool != "" {
			t.Errorf("the error answer carries (%+v, %v): a refusal carries no value", ref, found)
		}
	})
}
