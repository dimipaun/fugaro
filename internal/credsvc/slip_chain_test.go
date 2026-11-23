package credsvc

// CS14's tests: the grant carries the chain's sources and the named pool (C2's
// `sources` and `named_pool`), the pools a grant names are held to the pools the
// project publishes and to the launcher they serve, and the classes the grant
// route admits are the three the identity routes already admit — a person of the
// installation's roster, one of its roster service accounts (LM3, D3123), and
// the orchestrator daemon (E16).
//
// What a slip is after CS14, sentence by sentence:
//
//   - `sources` are the sources this grant COVERS (R4, D3171), each a kind of
//     the table's own five (C1) and, where the kind names one, a pool's name. The
//     list is positive and never a wildcard: an empty one is a grant that covers
//     nothing of the new kinds — what a release-behind launcher asks for — and a
//     source the grant does not carry is a source the run is not served from
//     (CS1). What the grant does NOT carry is the table's provider, model and
//     spelling: those are read again at each call.
//   - `named_pool` is the pool a launch named with `--pool` (D3176, CS12), and
//     `Pool` KEEPS ITS MEANING and is not it: `Pool` is the pool the granted
//     CREDENTIALS belong to, the one whose owner and allowed users the grant
//     checked, and a launch that named no pool still has one.
//   - the launcher is the VERIFIED principal (plan constraint row 67, R7, R8):
//     a person's Google subject, or a verified service account. It is recorded
//     as a kind beside an id because the owner alone cannot say which of the two
//     it is — a person's id is a numeric subject and a service account's is its
//     address — and that difference is the fact D2976 turns on: a service
//     account's run never draws on a person's pool, except where D3191 resumes
//     one.
//
// What this route still cannot decide, and where the decision is instead: the
// TABLE itself. No release reads a published `model_routing` yet (RT4 publishes
// it, and CS14 consumes C1's types and not the publisher), so "a source the
// table does not give this repository" is asked of the two things the service
// does hold — the table's closed set of kinds (C1's ValidKind) and the pools the
// project publishes (the policy) — and the repository's own admission of a
// source (the table's `admit`, D3182) is decided at the CALL, by CS1's walk.
// The suite below pins both halves it can decide, and says which half it cannot.
//
// The orchestrator's own grant is the one class whose answer is a refusal, and
// it is the plan's own state rather than a gap (CS1: "a run with no slip (the
// grant is best-effort at launch, and an orchestrator's launch has none)"): a
// slip's owner is the launcher's numeric Google account id (U2, and audit.go's
// validSlipOwner holds every line to it), VerifyOrchestrator answers the
// configured ADDRESS and no numeric subject at all, and a daemon re-issues a run
// rather than launching one of its own — so the launcher of such a grant is the
// one the run's own record names, which is PR3's resume to resolve. What CS14
// adds for the class is that it is ADMITTED — a token of it is no longer read as
// "not a launcher", and the principal its grant carries is built — and
// TestTheGrantsLauncherIsTheVerifiedPrincipal pins both halves of that.
//
// Everything here is hermetic (Convention 9): the slips are written to
// internal/gcpfake's Realtime Database through the real creds.RTDBStore, so the
// record a test reads back is the one the service really wrote, and the policy
// documents are decoded by projectpolicy.Decode, the reader the deployment uses.
// Every value is a NAME — a pool, a credential, an address — and never a
// credential's value (Convention 2).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/routing"
)

const (
	// testRosterServiceAccount is one of the installation's rosters' service
	// accounts (LM3, D3123): the class a pipeline launches runs as today (F4) and
	// the one CS14 teaches the grant route to admit beside a person's. It is a
	// `*.gserviceaccount.com` address because that shape is the whole of what
	// tells the class apart from the person list (foldLauncherAccounts).
	testRosterServiceAccount = "pipeline@example-project.iam.gserviceaccount.com"
	// testOrchestratorAccount is the resident daemon's own account (E16, D2049):
	// another service account, and the one the merge mint admits. It is NOT on
	// the roster's service-account list, and NewAuthenticator refuses an address
	// that is on both: one caller cannot be two classes.
	testOrchestratorAccount = "fugaro-orchestrator@example-project.iam.gserviceaccount.com"
	// testRosterServiceAccountSub and testOrchestratorSub are the account ids
	// those tokens carry. They are names, not credentials, and they are of the
	// width a real account id has (see testLauncherSub): the slip's owner is
	// written from the first and only the first — the orchestrator's class
	// answers no account id at all, which is the fact its refusal rests on.
	testRosterServiceAccountSub = "107511224455667788990"
	testOrchestratorSub         = "110223344556677889900"
)

// slipChainSvc is the fixture with the three classes the grant route admits and
// the pools the test publishes: the launcher list holds the three people the
// pool tests use, the roster's service-account list holds one address and the
// orchestrator list one more, so one test can ask the route as any of the three
// without a second fixture.
//
// It is newSlips' body with those two lists added, and it is bound to the same
// slip store the deployment really writes through (slipStore) rather than to a
// stand-in: what these tests read back is a record the service wrote.
func slipChainSvc(t *testing.T, pools ...config.Pool) *slips {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	store := slipStore(t, db)
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		cfg.Slips = store
		auth, err := NewAuthenticator(AuthConfig{
			Project:                 testProject,
			FirebaseProject:         testFirebase,
			Audience:                testAudience,
			JobAccounts:             []string{testJobSA, testSiblingSA},
			Keys:                    s.keys,
			JobKeys:                 s.jobKeys,
			Outcomes:                RTDBOutcomes{DB: s.db},
			LauncherAccounts:        []string{testLauncherAccount, testSecondLauncherAccount, testThirdLauncherAccount},
			LauncherServiceAccounts: []string{testRosterServiceAccount},
			OrchestratorAccounts:    []string{testOrchestratorAccount},
			Now:                     func() time.Time { return s.now },
		})
		if err != nil {
			t.Fatalf("the fixture's three classes cannot be configured: %v", err)
		}
		cfg.Auth, s.auth = auth, auth
		cfg.JobAccounts = append(cfg.JobAccounts, JobAccount{Email: testSiblingSA, Slug: s.slug, Repo: testRepo})
		s.policy.shared = decodeTestPolicy(t, poolPolicyDoc(t, pools...))
	})
	return &slips{svc: s, db: db, store: store}
}

// ---------------------------------------------------------------------------
// The calls, and the two bodies
// ---------------------------------------------------------------------------

// chainBody is a grant's body over the contract's own request type, carrying
// the two members CS14 adds: the sources the grant covers and the pool `--pool`
// named. It is grantBody's sibling, built from SlipGrantRequest itself so that a
// member renamed in the contract is renamed here too.
func chainBody(t *testing.T, slug, run, account string, maxRuntimeSeconds int64, named string, sources []creds.GrantSource, credentials ...string) string {
	t.Helper()
	return mustBody(t, SlipGrantRequest{
		Slug: slug, Run: run, Account: account,
		Credentials: credentials, MaxRuntimeSeconds: maxRuntimeSeconds,
		Sources: sources, NamedPool: named,
	})
}

// legacyGrantBody is an OLDER launcher's grant body: the members this route read
// before CS14 and no others. It is written by hand rather than through
// SlipGrantRequest, and that is the point of it: the type now carries the two
// new members, so a body built from it would be the new shape with empty values
// rather than the shape an older CLI really sends — and "served as today" is a
// claim about the older shape.
func legacyGrantBody(t *testing.T, slug, run, account string, maxRuntimeSeconds int64, credentials ...string) string {
	t.Helper()
	names, err := json.Marshal(credentials)
	if err != nil {
		t.Fatalf("the test's own credential names do not marshal: %v", err)
	}
	return fmt.Sprintf(`{"slug":%q,"run":%q,"account":%q,"credentials":%s,"max_runtime_seconds":%d}`,
		slug, run, account, names, maxRuntimeSeconds)
}

// serviceAccountBearer is one service account's own ID token in the bearer: the
// roster's class and the daemon's class both mint for THIS service's own URL
// alone (verifyServiceAccountCaller, VerifyOrchestrator), which is what the
// fixture's other tokens deliberately do not carry — launcherToken mints
// gcloud's audience, the only one a person's ambient credentials can produce.
func (s *slips) serviceAccountBearer(email, sub string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.jobToken(func(c map[string]any) {
			c["email"], c["sub"] = email, sub
		}))
	}
}

// grantWith is one grant call as the caller the test names, whatever class it is
// in: grantAs' sibling for the two classes whose token is not a person's.
func (s *slips) grantWith(t *testing.T, bearer func(*http.Request), body string) (*http.Response, []byte) {
	t.Helper()
	return s.slipPost(t, SlipPath, body, bearer)
}

// ---------------------------------------------------------------------------
// The grant covers the table's sources, and only those
// ---------------------------------------------------------------------------

// TestTheGrantCoversOnlyTheTablesSources: a grant records the sources it covers,
// and a source the service cannot vouch for is refused rather than written down.
//
// The first subtest is the record: the sources the call named and no others, in
// the call's own order, with the pool that governs the name it asked for still
// recorded as `Pool` — the two members side by side and neither one reused for
// the other.
//
// The refusals are the whole of what this route can decide about a source, and
// each is a different fact:
//
//   - a KIND the table does not have. The table's kinds are a closed set (C1:
//     launcher-pool, pool, anthropic, provider, vertex), and a sixth is a source
//     no side serves — the call nobody can place, which is why it is refused
//     rather than stored and never read.
//   - a kind carrying a member it cannot use: a pool on a source that names none
//     (the launcher's pool is decided per run, and a key, a provider key and
//     Vertex are one credential of the project each). C1's decoder refuses this
//     in a table, and a grant that kept it would be a member nothing reads.
//   - a POOL the project does not publish, and a pool that does not serve this
//     launcher — the two halves of the pool rule, which are separate answers: the
//     first is a name that could never be served from, the second is a pool that
//     exists and is somebody else's (D1936).
//
// What is NOT here, and where it is instead: the repository's own admission of a
// source (the table's `admit`, D3182). The service reads no table in this
// release, so that half is decided per call by CS1's walk — and the run the
// grant writes is the set of sources it may be served from, so a source the
// repository is not admitted to is skipped there rather than granted here.
func TestTheGrantCoversOnlyTheTablesSources(t *testing.T) {
	mine := config.Pool{
		Name:    slipTestPoolName,
		Owner:   testLauncherAccount,
		Members: []string{testPoolCredential},
	}
	theirs := config.Pool{
		Name:    testSecondPoolName,
		Owner:   testSecondLauncherAccount,
		Members: []string{testSecondPoolCredential},
	}
	shared := mustSharedToken(t, testPoolCredential)

	t.Run("the sources the grant names are the ones the record covers", func(t *testing.T) {
		s := slipChainSvc(t, mine)
		sources := []creds.GrantSource{
			{Kind: string(routing.KindLauncherPool)},
			{Kind: string(routing.KindPool), Pool: slipTestPoolName},
			{Kind: string(routing.KindAnthropic)},
			{Kind: string(routing.KindProvider)},
			{Kind: string(routing.KindVertex)},
		}
		sl := s.slip(t, s.grantOK(t, chainBody(t, s.slug, s.run, testJobSA, 3600, "", sources, shared)).Slip)
		if !slices.Equal(sl.Sources, sources) {
			t.Errorf("the slip covers %v, want %v: the sources the grant named, in its own order", sl.Sources, sources)
		}
		if sl.NamedPool != "" {
			t.Errorf("the slip records the named pool %q, want none: the grant named no pool with --pool", sl.NamedPool)
		}
		if sl.Pool != slipTestPoolName {
			t.Errorf("the slip records the pool %q, want %q: the pool that governs the name the grant asked for, which is not the named pool", sl.Pool, slipTestPoolName)
		}
		if !slices.Contains(sl.Allowed, shared) {
			t.Errorf("the slip permits %v, want the name the grant asked for", sl.Allowed)
		}
	})

	t.Run("a kind the table does not have is refused", func(t *testing.T) {
		s := slipChainSvc(t, mine)
		body := chainBody(t, s.slug, s.run, testJobSA, 3600, "",
			[]creds.GrantSource{{Kind: "openrouter"}}, shared)
		resp, answer := s.grantCall(t, body)
		wantOneGrantRefusal(t, s, resp, answer, testLauncherAccount, "openrouter", shared)
	})

	t.Run("a kind that names no pool may not carry one", func(t *testing.T) {
		s := slipChainSvc(t, mine)
		body := chainBody(t, s.slug, s.run, testJobSA, 3600, "",
			[]creds.GrantSource{{Kind: string(routing.KindAnthropic), Pool: slipTestPoolName}}, shared)
		resp, answer := s.grantCall(t, body)
		wantOneGrantRefusal(t, s, resp, answer, testLauncherAccount, slipTestPoolName, shared)
	})

	t.Run("a pool source that names no pool is refused", func(t *testing.T) {
		s := slipChainSvc(t, mine)
		body := chainBody(t, s.slug, s.run, testJobSA, 3600, "",
			[]creds.GrantSource{{Kind: string(routing.KindPool)}}, shared)
		resp, answer := s.grantCall(t, body)
		wantOneGrantRefusal(t, s, resp, answer, testLauncherAccount, shared)
	})

	t.Run("a pool the project does not publish is refused", func(t *testing.T) {
		s := slipChainSvc(t, mine)
		body := chainBody(t, s.slug, s.run, testJobSA, 3600, "",
			[]creds.GrantSource{{Kind: string(routing.KindPool), Pool: "nobody-pool"}}, shared)
		resp, answer := s.grantCall(t, body)
		wantOneGrantRefusal(t, s, resp, answer, testLauncherAccount, "nobody-pool", shared)
	})

	t.Run("a pool published to another launcher is refused", func(t *testing.T) {
		s := slipChainSvc(t, mine, theirs)
		body := chainBody(t, s.slug, s.run, testJobSA, 3600, "",
			[]creds.GrantSource{{Kind: string(routing.KindPool), Pool: testSecondPoolName}}, shared)
		resp, answer := s.grantCall(t, body)
		wantOneGrantRefusal(t, s, resp, answer, testLauncherAccount, testSecondPoolName, shared)
	})
}

// ---------------------------------------------------------------------------
// The named pool: the launcher it belongs to, and the pool it is not
// ---------------------------------------------------------------------------

// TestANamedPoolIsGrantedOnlyToItsOwner: `--pool` names a pool the launcher owns
// or is allowed on, and nothing else (D3176, D1936) — and the pool it names is
// recorded as `named_pool`, never as `Pool`.
//
// The rule is the pools' own, which is why it is the same one the grant already
// applies to a name a pool governs (config.Pool.Serves, D1936): the owner, the
// users it allows and a pool the migration found ownerless are served, and
// nobody else is. Naming a pool can therefore never widen what a run may be
// served from, which is the whole of what the flag is not allowed to do.
//
// The two members are pinned apart in one call: a grant that names a pool with
// `--pool` AND asks for a credential another pool governs records BOTH facts —
// `named_pool` is the one the flag named, `Pool` is the one that decided the
// credentials. A service that reused `Pool` for the flag would answer an
// operator's "which pool decided this grant" with the flag instead.
//
// The launcher on the record is the verified principal (plan constraint row 67,
// R7): the person's own Google subject, beside the owner the grant already
// wrote.
func TestANamedPoolIsGrantedOnlyToItsOwner(t *testing.T) {
	mine := config.Pool{
		Name:    slipTestPoolName,
		Owner:   testLauncherAccount,
		Members: []string{testPoolCredential},
	}
	theirs := config.Pool{
		Name:    testSecondPoolName,
		Owner:   testSecondLauncherAccount,
		Members: []string{testSecondPoolCredential},
	}
	shared := mustSharedToken(t, testPoolCredential)

	t.Run("the owner's named pool is recorded beside the pool the names decided", func(t *testing.T) {
		s := slipChainSvc(t, mine, config.Pool{
			Name:         testSecondPoolName,
			Owner:        testSecondLauncherAccount,
			AllowedUsers: []string{testLauncherAccount},
			Members:      []string{testSecondPoolCredential},
		})
		sl := s.slip(t, s.grantOK(t, chainBody(t, s.slug, s.run, testJobSA, 3600, testSecondPoolName, nil, shared)).Slip)
		if sl.NamedPool != testSecondPoolName {
			t.Errorf("the slip records the named pool %q, want %q: the pool the launch asked for", sl.NamedPool, testSecondPoolName)
		}
		if sl.Pool != slipTestPoolName {
			t.Errorf("the slip records the pool %q, want %q: Pool is the pool that decided the grant's names, and the named pool is not it", sl.Pool, slipTestPoolName)
		}
		if want := (routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub}); sl.Launcher != want {
			t.Errorf("the slip records the launcher %+v, want %+v: the verified principal that asked for the grant", sl.Launcher, want)
		}
		if sl.Owner != testLauncherSub {
			t.Errorf("the slip records the owner %q, want %q: the verified account id is what a line's owner is written from", sl.Owner, testLauncherSub)
		}
	})

	t.Run("a named pool the launcher is not entitled to is refused", func(t *testing.T) {
		s := slipChainSvc(t, mine, theirs)
		body := chainBody(t, s.slug, s.run, testJobSA, 3600, testSecondPoolName, nil, shared)
		resp, answer := s.grantCall(t, body)
		wantOneGrantRefusal(t, s, resp, answer, testLauncherAccount, testSecondPoolName, shared)
	})

	t.Run("a named pool the project does not publish is refused", func(t *testing.T) {
		s := slipChainSvc(t, mine)
		body := chainBody(t, s.slug, s.run, testJobSA, 3600, "nobody-pool", nil, shared)
		resp, answer := s.grantCall(t, body)
		wantOneGrantRefusal(t, s, resp, answer, testLauncherAccount, "nobody-pool", shared)
	})

	t.Run("a user the pool allows is served its name too", func(t *testing.T) {
		s := slipChainSvc(t, config.Pool{
			Name:         slipTestPoolName,
			Owner:        testLauncherAccount,
			AllowedUsers: []string{testSecondLauncherAccount},
			Members:      []string{testPoolCredential},
		})
		body := chainBody(t, s.slug, s.run, testJobSA, 3600, slipTestPoolName, nil, shared)
		resp, answer := s.grantWith(t, s.bearerFor(testSecondLauncherAccount, testSecondLauncherSub), body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the grant is answered %d, want 200: a pool is served to its owner and to the users it allows (D1936): %s", resp.StatusCode, answer)
		}
		var granted SlipGrantResponse
		if err := json.Unmarshal(answer, &granted); err != nil {
			t.Fatalf("the grant's answer is not one this contract reads: %v", err)
		}
		if sl := s.slip(t, granted.Slip); sl.NamedPool != slipTestPoolName {
			t.Errorf("the slip records the named pool %q, want %q", sl.NamedPool, slipTestPoolName)
		}
	})
}

// ---------------------------------------------------------------------------
// The launcher classes, and the principal the grant records
// ---------------------------------------------------------------------------

// TestTheGrantsLauncherIsTheVerifiedPrincipal: the grant route admits the three
// classes the identity routes already admit — a person, a roster service account
// and the orchestrator daemon — and every slip it writes names the verified
// principal of the caller that asked for it (R7, R8, plan constraint row 67).
//
// The two classes that HOLD a launcher are served, and each records its own
// kind: a person's id is the Google subject the class verified, and a service
// account's is its address — which is why the record needs the kind and not the
// owner alone. A service account's run also draws its OWN pool (D3181: a project
// pool an admin fills), which the second case pins by asking as the service
// account for a pool whose owner its address is.
//
// The daemon's class is the third, and its two halves are pinned apart on
// purpose: the class IS admitted and its principal IS built — the unit case
// below asks the route's own classifier and gets `service-account` and the
// address, with no error — while the GRANT it asks for is refused, because a
// slip's owner is a launcher's numeric account id (U2; audit.go's
// validSlipOwner holds every line to it) and this class answers no such id. That
// is not a hole to be papered over: the plan's own CS1 says an orchestrator's
// launch has no slip, and the launcher of such a run is the one the run's own
// record names — PR3's resume, which this refusal is the seam for. A service
// that instead wrote a record whose owner was an address would write a slip
// every one of whose served calls fails to audit, which is a run that dies at
// its first model call.
func TestTheGrantsLauncherIsTheVerifiedPrincipal(t *testing.T) {
	t.Run("a person's grant records the person's own subject", func(t *testing.T) {
		s := slipChainSvc(t, config.Pool{
			Name:    slipTestPoolName,
			Owner:   testLauncherAccount,
			Members: []string{testPoolCredential},
		})
		sl := s.slip(t, s.grantOK(t, chainBody(t, s.slug, s.run, testJobSA, 3600, "", nil, mustSharedToken(t, testPoolCredential))).Slip)
		if want := (routing.Principal{Kind: routing.PrincipalPerson, ID: testLauncherSub}); sl.Launcher != want {
			t.Errorf("the slip records the launcher %+v, want %+v", sl.Launcher, want)
		}
		if sl.Owner != testLauncherSub {
			t.Errorf("the slip records the owner %q, want %q", sl.Owner, testLauncherSub)
		}
	})

	t.Run("a roster service account's grant records the service account, and its own pool", func(t *testing.T) {
		s := slipChainSvc(t, config.Pool{
			Name:    slipTestPoolName,
			Owner:   testRosterServiceAccount,
			Members: []string{testPoolCredential},
		})
		body := chainBody(t, s.slug, s.run, testJobSA, 3600, "", nil, mustSharedToken(t, testPoolCredential))
		resp, answer := s.grantWith(t, s.serviceAccountBearer(testRosterServiceAccount, testRosterServiceAccountSub), body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the grant is answered %d, want 200: the roster's service accounts are a class this route admits (D3123): %s", resp.StatusCode, answer)
		}
		var granted SlipGrantResponse
		if err := json.Unmarshal(answer, &granted); err != nil {
			t.Fatalf("the grant's answer is not one this contract reads: %v", err)
		}
		sl := s.slip(t, granted.Slip)
		want := routing.Principal{Kind: routing.PrincipalServiceAccount, ID: testRosterServiceAccount}
		if sl.Launcher != want {
			t.Errorf("the slip records the launcher %+v, want %+v: a service account's id is its address, which is what PL6 resolves its pool from", sl.Launcher, want)
		}
		if sl.Owner != testRosterServiceAccountSub {
			t.Errorf("the slip records the owner %q, want %q: the verified account id of the service account that asked", sl.Owner, testRosterServiceAccountSub)
		}
		if sl.Pool != slipTestPoolName {
			t.Errorf("the slip records the pool %q, want %q: the pool the service account owns decided the grant (D3181)", sl.Pool, slipTestPoolName)
		}
	})

	t.Run("the daemon's class is admitted, and carries no account id", func(t *testing.T) {
		s := slipChainSvc(t)
		caller, err := s.server.launcherCaller(context.Background(),
			s.jobToken(func(c map[string]any) {
				c["email"], c["sub"] = testOrchestratorAccount, testOrchestratorSub
			}))
		if err != nil {
			t.Fatalf("the grant route refuses the daemon's own token as a class it does not know: %v", err)
		}
		want := routing.Principal{Kind: routing.PrincipalServiceAccount, ID: testOrchestratorAccount}
		if caller.Principal != want {
			t.Errorf("the route answers the principal %+v, want %+v: the class is a verified service account", caller.Principal, want)
		}
		if caller.Sub != "" {
			t.Errorf("the route answers the account id %q, want none: VerifyOrchestrator returns the configured address and no numeric subject", caller.Sub)
		}
	})

	t.Run("an orchestrator's grant is refused, because it names no launcher of its own", func(t *testing.T) {
		s := slipChainSvc(t, config.Pool{
			Name:    slipTestPoolName,
			Owner:   testLauncherAccount,
			Members: []string{testPoolCredential},
		})
		body := chainBody(t, s.slug, s.run, testJobSA, 3600, "", nil, testSlipCredential)
		resp, answer := s.grantWith(t, s.serviceAccountBearer(testOrchestratorAccount, testOrchestratorSub), body)
		// The address is passed as a value the refusal must NOT echo: it is on the
		// audit line as the caller, which is the contract's own field, and it is in
		// no sentence this service answers.
		wantOneGrantRefusal(t, s, resp, answer, testOrchestratorAccount, testOrchestratorAccount, testSlipCredential)
	})
}

// ---------------------------------------------------------------------------
// A release-behind launcher
// ---------------------------------------------------------------------------

// TestAnOlderGrantRequestIsServedAsToday: a grant request that names no sources
// and no pool — the body an older CLI sends, and the one this route read before
// CS14 — is served exactly as it was, and the record it writes covers nothing.
//
// The two directions are both here, because "as today" is a claim about the
// whole route and not only its happy path:
//
//   - a request the route granted before is still granted, with the same owner,
//     the same allowed set and the same pool, and with `sources` EMPTY. The empty
//     list is the narrow answer and not the wide one: a source the grant does not
//     carry is a source the run is not served from (CS1), so a launcher that
//     knows nothing of the table is served the sources its repository is admitted
//     to and no launcher's pool — the documented path of a run whose grant failed
//     or that has no slip at all.
//   - a name a pool governs and does not serve the launcher is still REFUSED,
//     with one line and no record: the pools' own rule is not a rule about the
//     new members, and a version of this route that only applied it to a grant
//     naming sources would be one an older launcher could ask around.
func TestAnOlderGrantRequestIsServedAsToday(t *testing.T) {
	mine := config.Pool{
		Name:    slipTestPoolName,
		Owner:   testLauncherAccount,
		Members: []string{testPoolCredential},
	}
	shared := mustSharedToken(t, testPoolCredential)

	t.Run("a request with no new members is granted, and covers nothing", func(t *testing.T) {
		s := slipChainSvc(t, mine)
		sl := s.slip(t, s.grantOK(t, legacyGrantBody(t, s.slug, s.run, testJobSA, 3600, shared)).Slip)
		if len(sl.Sources) != 0 {
			t.Errorf("the slip covers %v, want nothing: a request that names no sources is a grant that covers no source of the table's kinds, and never one that covers everything", sl.Sources)
		}
		if sl.NamedPool != "" {
			t.Errorf("the slip records the named pool %q, want none", sl.NamedPool)
		}
		if sl.Pool != slipTestPoolName {
			t.Errorf("the slip records the pool %q, want %q: the pool that governs the name the request asked for", sl.Pool, slipTestPoolName)
		}
		if !slices.Contains(sl.Allowed, shared) {
			t.Errorf("the slip permits %v, want the name the request asked for", sl.Allowed)
		}
		if sl.Owner != testLauncherSub {
			t.Errorf("the slip records the owner %q, want %q", sl.Owner, testLauncherSub)
		}
	})

	t.Run("the refusals of the old shape are unchanged", func(t *testing.T) {
		s := slipChainSvc(t, mine)
		body := legacyGrantBody(t, s.slug, s.run, testJobSA, 3600, shared)
		resp, answer := s.grantAs(t, testSecondLauncherAccount, testSecondLauncherSub, body)
		wantOneGrantRefusal(t, s, resp, answer, testSecondLauncherAccount, shared, testPoolCredential, slipTestPoolName)
	})
}

// TestAnOlderServiceReadsTheGrantThisLaunchBuilds: the body a launch of THIS
// release sends is read by a service of the release before CS14.
//
// It is the other half of the skew the two members introduce, and the half that
// costs a run. `fugaro update` deploys the service and a person upgrades the CLI
// first (CLAUDE.md's HARD RULE), so a launch routinely meets a reader that knows
// neither `sources` nor `named_pool` — and that reader is decodeStrict, where a
// member it does not know refuses the WHOLE body. The launch would then start
// with no slip and every call that needs a grammar-named credential would be
// refused at the call (slipRequiresSlip), which is the failure the two members
// being omitempty is what prevents.
//
// The request here is the one internal/cli's launch builds, member for member:
// runSlipEnv sets the five below and neither of the new members, because nothing
// there names a source or a pool yet (CS12). The reader below is the strict
// decoder with the member set this route had BEFORE CS14 — the same decoder and
// the same fields an older service runs — so the test fails the moment a new
// member is written unconditionally, and the assertions on what it read keep
// "readable" from being satisfied by a body that decodes into nothing.
func TestAnOlderServiceReadsTheGrantThisLaunchBuilds(t *testing.T) {
	s := slipChainSvc(t)
	body := mustBody(t, SlipGrantRequest{
		Slug:              s.slug,
		Run:               s.run,
		Account:           testJobSA,
		Credentials:       []string{testPoolCredential},
		MaxRuntimeSeconds: 3600,
	})
	var older struct {
		Slug              string   `json:"slug"`
		Run               string   `json:"run"`
		Account           string   `json:"account"`
		Credentials       []string `json:"credentials"`
		MaxRuntimeSeconds int64    `json:"max_runtime_seconds"`
	}
	if err := decodeStrict([]byte(body), &older); err != nil {
		t.Fatalf("a service of the release before CS14 refuses the grant body this release's launch builds, %s: %v", body, err)
	}
	if older.Slug != s.slug || older.Run != s.run || older.Account != testJobSA ||
		!slices.Equal(older.Credentials, []string{testPoolCredential}) || older.MaxRuntimeSeconds != 3600 {
		t.Errorf("the older reader read %+v from %s: want the five members the launch set, unchanged", older, body)
	}
}
