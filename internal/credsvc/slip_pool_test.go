package credsvc

// DS3.2's tests: pool authorisation at issue time (D1936, R11, G8).
//
// A slip is the one place a whole run's credentials are written down, so the
// grant is the one place a pool's roster can be checked for a run: what the
// launcher asks for is the set the service will later serve that run from (R9),
// and a name a pool governs is served only to the pool's owner and to the users
// it allows (D1936).
//
// The check is made at ISSUE time and is never cached: the published policy is
// read at every grant, so a member the publisher takes off a roster vanishes
// from the next slip (G8's resolution rule, TestPoolResolvedAtIssue).
//
// The ways a grant names a pool, and each has a test below:
//
//   - the pool's own name, which is what a craft's `access` writes when a craft
//     runs on a pool (config.Craft.Access names a credential or a pool);
//   - a shared token, `fugaro_p_<name>`, whose credential the pool lists as one
//     of its members: sharing is a project-shared secret under a pool's allowed
//     users (R11, G8);
//   - no pool at all. A name the published node governs with no pool — a bundle
//     entry's own name, or a `fugaro_p_` credential no pool lists — is left
//     exactly as it was: a slip that names no pool is not one pool
//     authorisation answers for (creds.Slip.Pool), and R9 keeps the path that
//     names no pool serving what it served before. The `fugaro_p_` name of
//     TestGrantRefusedForAnotherOwnersToken is that case, and its fixture
//     publishes no pool.
//
// A roster entry and the caller's own address are compared by this service's
// own fold (foldEmail) and never byte for byte, on both sides, so a pool whose
// owner or allowed user is spelled in another case still serves the person it
// names (TestPoolRosterIsComparedByTheAddressFold).
//
// A grant that names several pools is decided one NAME at a time: a roster
// answers for the names its pool governs and for no other pool's, so a launcher
// served by one pool is refused the grant that also asks for another pool's
// name (TestEachNameIsAuthorisedByThePoolThatGovernsIt). The slip's list is what
// the run is served from, so a name admitted here is a name that run is handed.
//
// A per-user name (`fugaro_u_<id>_<name>`) is NOT looked up against a pool: a
// launcher may name only its own (checkGrantedNames), so there is nothing for a
// pool to lend — the spec's pool of one person's own tokens is that person's own
// (R11).
//
// Everything here is hermetic (Convention 9): the slips are written to
// internal/gcpfake's Realtime Database through the real creds.RTDBStore, and the
// policy documents are decoded by projectpolicy.Decode, the reader the service
// deploys, so a document no operator could publish cannot be a fixture here.
// Every value is a NAME (a pool, a credential, an address) and never a
// credential's value (Convention 2).

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/secretname"
)

const (
	// testSlipPoolName is the pool the fixtures publish, and it is a name of the
	// shape a craft's access holds (config.ValidCredentialRef).
	testSlipPoolName = "team-pool"
	// testPoolCredential is the credential the fixtures' pools list as a member
	// and the fixture's document defines: a shared token is that credential's
	// project-shared secret, fugaro_p_<name> (R7's mapping of a name to its
	// per-secret name).
	testPoolCredential = "team-token"
	// testSecondPoolName and testSecondPoolCredential are a SECOND pool and the
	// credential it lists, so that a grant can name two pools of two different
	// rosters (TestEachNameIsAuthorisedByThePoolThatGovernsIt).
	testSecondPoolName       = "other-pool"
	testSecondPoolCredential = "other-token"
	// testSecondLauncherAccount and testThirdLauncherAccount are two more of the
	// installation's launchers: people the launcher class admits and the pools'
	// rosters name or do not.
	testSecondLauncherAccount = "launcher-2@example.invalid"
	testThirdLauncherAccount  = "launcher-3@example.invalid"
	// testSecondLauncherSub and testThirdLauncherSub are the account ids their
	// tokens carry. They are names, not credentials, and they are of the width a
	// real account id has (see testLauncherSub).
	testSecondLauncherSub = "107231109945501823377"
	testThirdLauncherSub  = "113640220817339118864"
)

// poolPolicyDoc is the fixture's policy with the pools a test publishes and the
// one credential they list, written the way an operator's publish writes it: the
// members are carried by the two craft members beside the rest of the node, and
// the document is decoded by Decode — a pool listing a credential the same
// document does not define, or a published pool naming no owner, is refused
// there rather than read here (config.ValidatePublishedPools).
func poolPolicyDoc(t *testing.T, pools ...config.Pool) string {
	t.Helper()
	encoded, err := json.Marshal(pools)
	if err != nil {
		t.Fatalf("the test's own pools do not marshal: %v", err)
	}
	creds, err := json.Marshal([]config.Credential{
		{Name: testPoolCredential, Type: config.CredentialTypeClaudeOAuth},
		{Name: testSecondPoolCredential, Type: config.CredentialTypeClaudeOAuth},
	})
	if err != nil {
		t.Fatalf("the test's own credential does not marshal: %v", err)
	}
	return `{
		"version": 5,
		"budget": ` + testBudgetDoc + `,
		"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"],
		"accounts": {
			"default": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]},
			"` + testAccount + `": {"allow_repos": ["` + testRepo + `", "` + testOtherRepo + `"]}
		},
		"credentials": {"fallback": "auto"},
		"craft_credentials": ` + string(creds) + `,
		"craft_pools": ` + string(encoded) + `
	}`
}

// slipPoolSvc is the fixture with the pools the test publishes and three launchers
// the class admits, so that one of them is the pool's owner, one is a user it
// allows and one is neither.
func slipPoolSvc(t *testing.T, pools ...config.Pool) *slips {
	t.Helper()
	s := newSlips(t, testLauncherAccount, testSecondLauncherAccount, testThirdLauncherAccount)
	s.policy.shared = decodeTestPolicy(t, poolPolicyDoc(t, pools...))
	return s
}

// bearerFor is the grant's bearer for one of the fixture's launchers: the
// address the rosters are keyed by, and the account id the class answers.
func (s *slips) bearerFor(email, sub string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.launcherToken(func(c map[string]any) {
			c["email"] = email
			c["sub"] = sub
		}))
	}
}

// grantAs is one launcher's grant call, whatever the fixture's first launcher
// is: the call a test makes when the caller is the thing under test.
func (s *slips) grantAs(t *testing.T, email, sub, body string) (*http.Response, []byte) {
	t.Helper()
	return s.slipPost(t, SlipPath, body, s.bearerFor(email, sub))
}

// sharedPolicyReads is how many times the service read the project's shared
// node, which is how a test states that the roster is read at ISSUE time and not
// once and remembered (G8).
func (s *slips) sharedPolicyReads() int {
	s.policy.mu.Lock()
	defer s.policy.mu.Unlock()
	return s.policy.sharedReads
}

// mustSharedToken is one shared credential's secret ID, built through the
// grammar itself so a test cannot state a name the intake could never write.
func mustSharedToken(t *testing.T, name string) string {
	t.Helper()
	shared, err := secretname.Project(name)
	if err != nil {
		t.Fatalf("the grammar refuses the shared name %q: %v", name, err)
	}
	return shared
}

// wantNoSlipWritten holds a refused grant to having written no record at all: a
// refused grant is not a grant, and a slip nobody asked for is a run's
// credentials waiting for a caller.
func wantNoSlipWritten(t *testing.T, s *slips) {
	t.Helper()
	if got := s.slipIDs(t); len(got) != 0 {
		t.Errorf("the refused grant wrote %v, want no record", got)
	}
}

// wantGrantRefusal holds a refused grant to the wire contract's refusal, to
// exactly ONE audit line, and to that line naming the caller and none of the
// call's values — which is the rule a name may never be echoed by. It says
// nothing about records, because a test may have granted a slip before the one
// under test.
func wantGrantRefusal(t *testing.T, s *slips, resp *http.Response, body []byte, caller string, values ...string) {
	t.Helper()
	if e := wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused); e.Message != "" {
		for _, value := range values {
			if strings.Contains(e.Message, value) {
				t.Errorf("the refusal echoes %q: %q", value, e.Message)
			}
		}
	}
	lines := auditLines(t, s.audit)
	if len(lines) != 1 {
		t.Fatalf("the refused grant wrote %d audit lines, want exactly one: %v", len(lines), lines)
	}
	wantSlipLine(t, lines[0], PurposeSlipGrant, ErrorRequestRefused, map[string]any{
		"caller": caller, "run": "", "repository": "", "job": "",
		"credential": "", "tier": "",
	})
}

// wantOneGrantRefusal is wantGrantRefusal for a test whose grant under test is
// the only one that was ever asked for, so the refusal must also have written
// no record.
func wantOneGrantRefusal(t *testing.T, s *slips, resp *http.Response, body []byte, caller string, values ...string) {
	t.Helper()
	wantGrantRefusal(t, s, resp, body, caller, values...)
	wantNoSlipWritten(t, s)
}

// ---------------------------------------------------------------------------
// D1936's default: the owner alone
// ---------------------------------------------------------------------------

// TestOwnerOnlyByDefault: a pool that names no allowed users is served to its
// owner and to nobody else, which is D1936's default and the shape of the
// spec's own pool (one person's tokens).
//
// The two names that reach a pool are both here, because they are the two
// spellings a grant may hold and one rule answers for both: the pool's own name
// (what a craft's access writes when a craft runs on a pool) and the shared
// token of a credential the pool lists (R11: sharing is a project-shared secret
// under a pool's allowed users). Both write the pool into the record, which is
// what an operator reads as "which pool decided this grant", and the second
// launcher is refused on both — before the slip is written, with one line, and
// with neither the name nor the pool in the answer.
func TestOwnerOnlyByDefault(t *testing.T) {
	shared := mustSharedToken(t, testPoolCredential)
	pool := config.Pool{
		Name:    testSlipPoolName,
		Owner:   testLauncherAccount,
		Members: []string{testPoolCredential},
	}

	t.Run("the owner is granted the pool's own name", func(t *testing.T) {
		s := slipPoolSvc(t, pool)
		ans := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipPoolName))
		sl := s.slip(t, ans.Slip)
		if sl.Pool != testSlipPoolName {
			t.Errorf("the slip records the pool %q, want %q: the grant named it", sl.Pool, testSlipPoolName)
		}
		if !slices.Contains(sl.Allowed, testSlipPoolName) {
			t.Errorf("the slip permits %v, want the name the grant asked for", sl.Allowed)
		}
	})

	t.Run("the owner is granted the pool's shared token", func(t *testing.T) {
		s := slipPoolSvc(t, pool)
		ans := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, shared))
		sl := s.slip(t, ans.Slip)
		if sl.Pool != testSlipPoolName {
			t.Errorf("the slip records the pool %q, want %q: the pool lists the credential the name holds", sl.Pool, testSlipPoolName)
		}
		if !slices.Contains(sl.Allowed, shared) {
			t.Errorf("the slip permits %v, want the name the grant asked for", sl.Allowed)
		}
	})

	t.Run("a launcher who is not the owner is refused", func(t *testing.T) {
		s := slipPoolSvc(t, pool)
		body := grantBody(t, s.slug, s.run, testJobSA, 3600, shared)
		resp, answer := s.grantAs(t, testSecondLauncherAccount, testSecondLauncherSub, body)
		wantOneGrantRefusal(t, s, resp, answer, testSecondLauncherAccount, shared, testPoolCredential, testSlipPoolName)
	})
}

// ---------------------------------------------------------------------------
// D1936's other half: the users a pool names
// ---------------------------------------------------------------------------

// TestSlipRefusedForNonMember: the users a pool allows are served it and a
// person it does not name is refused, which is the pair D1936's roster is for.
//
// The allowed user is not the owner, so a service that answered by the owner
// alone fails the first half, and the third launcher is named nowhere in the
// pool's roster, so one that served everyone fails the second.
func TestSlipRefusedForNonMember(t *testing.T) {
	shared := mustSharedToken(t, testPoolCredential)
	pool := config.Pool{
		Name:         testSlipPoolName,
		Owner:        testLauncherAccount,
		AllowedUsers: []string{testSecondLauncherAccount},
		Members:      []string{testPoolCredential},
	}
	body := func(s *slips) string {
		return grantBody(t, s.slug, s.run, testJobSA, 3600, shared)
	}

	t.Run("a user the pool allows is served it", func(t *testing.T) {
		s := slipPoolSvc(t, pool)
		resp, answer := s.grantAs(t, testSecondLauncherAccount, testSecondLauncherSub, body(s))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the grant is answered %d, want 200: a user the pool allows is served it: %s", resp.StatusCode, answer)
		}
	})

	t.Run("a person the pool does not name is refused", func(t *testing.T) {
		s := slipPoolSvc(t, pool)
		resp, answer := s.grantAs(t, testThirdLauncherAccount, testThirdLauncherSub, body(s))
		wantOneGrantRefusal(t, s, resp, answer, testThirdLauncherAccount, shared, testPoolCredential, testSlipPoolName)
	})
}

// ---------------------------------------------------------------------------
// The check is made per NAME, not per grant
// ---------------------------------------------------------------------------

// TestEachNameIsAuthorisedByThePoolThatGovernsIt: one grant naming two pools is
// two questions, and neither pool's roster answers for the other's name.
//
// The slip's list is what the run is served from later (DS1.6), so a service
// that read the grant as "is this launcher served by some pool of this call"
// would write — and then serve — a name the launcher is neither the owner of nor
// allowed by. The first two subtests are that exact grant, one way round and
// then the other, so that a check made on the first name alone cannot pass; the
// third is the same hole entered through the other spelling, a shared token of
// the pool the launcher is not served by.
//
// The union is per NAME and never across the names of one call, which the last
// subtest is the other half of: a launcher both rosters serve gets both names in
// one slip.
func TestEachNameIsAuthorisedByThePoolThatGovernsIt(t *testing.T) {
	mine := config.Pool{
		Name:    testSlipPoolName,
		Owner:   testLauncherAccount,
		Members: []string{testPoolCredential},
	}
	theirs := config.Pool{
		Name:    testSecondPoolName,
		Owner:   testSecondLauncherAccount,
		Members: []string{testSecondPoolCredential},
	}
	theirToken := mustSharedToken(t, testSecondPoolCredential)
	pair := func(s *slips, names ...string) (*http.Response, []byte) {
		return s.grantCall(t, grantBody(t, s.slug, s.run, testJobSA, 3600, names...))
	}

	t.Run("two pools, one of them the launcher's own", func(t *testing.T) {
		s := slipPoolSvc(t, mine, theirs)
		resp, answer := pair(s, testSlipPoolName, testSecondPoolName)
		wantOneGrantRefusal(t, s, resp, answer, testLauncherAccount, testSlipPoolName, testSecondPoolName)
	})

	t.Run("the same two names the other way round", func(t *testing.T) {
		s := slipPoolSvc(t, mine, theirs)
		resp, answer := pair(s, testSecondPoolName, testSlipPoolName)
		wantOneGrantRefusal(t, s, resp, answer, testLauncherAccount, testSlipPoolName, testSecondPoolName)
	})

	t.Run("the second name is that pool's shared token", func(t *testing.T) {
		s := slipPoolSvc(t, mine, theirs)
		resp, answer := pair(s, testSlipPoolName, theirToken)
		wantOneGrantRefusal(t, s, resp, answer, testLauncherAccount, theirToken, testSecondPoolCredential, testSecondPoolName)
	})

	t.Run("a launcher both rosters serve is granted both names", func(t *testing.T) {
		s := slipPoolSvc(t, mine, config.Pool{
			Name:         testSecondPoolName,
			Owner:        testSecondLauncherAccount,
			AllowedUsers: []string{testLauncherAccount},
			Members:      []string{testSecondPoolCredential},
		})
		sl := s.slip(t, s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipPoolName, testSecondPoolName)).Slip)
		for _, name := range []string{testSlipPoolName, testSecondPoolName} {
			if !slices.Contains(sl.Allowed, name) {
				t.Errorf("the slip permits %v, want both names the grant asked for", sl.Allowed)
			}
		}
		if sl.Pool != testSlipPoolName {
			t.Errorf("the slip records the pool %q, want %q: the first pool of the policy that admitted a name", sl.Pool, testSlipPoolName)
		}
	})
}

// ---------------------------------------------------------------------------
// The roster is compared by this service's own address fold
// ---------------------------------------------------------------------------

// TestPoolRosterIsComparedByTheAddressFold: a roster entry and the caller's own
// address are compared folded, so a pool whose owner or allowed user is spelled
// in another case still serves the person it names.
//
// The fold is not decoration. An operator writes a pool's roster by hand into a
// published node and the installation writes its launcher list by hand into its
// own config, and nothing canonicalises either of them; a service that compared
// the two byte for byte would shut an owner out of their own pool, which is the
// opposite of the failure D1936's gate is for (what it refuses is a person the
// roster does NOT name). Both sides are folded here, so both sides have a case
// below — the launcher list spelled one way and the roster the other, the roster
// as the owner and as an allowed user.
//
// The last case is the fold's own edge, and it is the fail-closed side of it: an
// entry that is no printable-ASCII address is one this service cannot fold, and
// it names nobody rather than matching a caller by accident.
func TestPoolRosterIsComparedByTheAddressFold(t *testing.T) {
	shared := mustSharedToken(t, testPoolCredential)
	members := []string{testPoolCredential}
	build := func(t *testing.T, launchers []string, pool config.Pool) *slips {
		t.Helper()
		s := newSlips(t, launchers...)
		s.policy.shared = decodeTestPolicy(t, poolPolicyDoc(t, pool))
		return s
	}
	grantAs := func(t *testing.T, s *slips, email string) (*http.Response, []byte) {
		t.Helper()
		return s.grantAs(t, email, testSecondLauncherSub, grantBody(t, s.slug, s.run, testJobSA, 3600, shared))
	}

	t.Run("the roster is spelled in another case than the launcher list", func(t *testing.T) {
		s := build(t, []string{"owner@example.invalid"}, config.Pool{
			Name: testSlipPoolName, Owner: "Owner@Example.INVALID", Members: members,
		})
		if resp, answer := grantAs(t, s, "owner@example.invalid"); resp.StatusCode != http.StatusOK {
			t.Fatalf("the grant is answered %d, want 200: the pool names this person as its owner (D1936): %s", resp.StatusCode, answer)
		}
	})

	t.Run("the launcher list is spelled in another case than the roster", func(t *testing.T) {
		s := build(t, []string{"Owner@Example.INVALID"}, config.Pool{
			Name: testSlipPoolName, Owner: "owner@example.invalid", Members: members,
		})
		if resp, answer := grantAs(t, s, "owner@example.invalid"); resp.StatusCode != http.StatusOK {
			t.Fatalf("the grant is answered %d, want 200: the pool names this person as its owner (D1936): %s", resp.StatusCode, answer)
		}
	})

	t.Run("an allowed user is spelled in another case too", func(t *testing.T) {
		s := build(t, []string{"Allowed@Example.INVALID"}, config.Pool{
			Name: testSlipPoolName, Owner: "owner@example.invalid",
			AllowedUsers: []string{"allowed@example.invalid"}, Members: members,
		})
		if resp, answer := grantAs(t, s, "Allowed@Example.INVALID"); resp.StatusCode != http.StatusOK {
			t.Fatalf("the grant is answered %d, want 200: the pool lists this person among its allowed users (D1936): %s", resp.StatusCode, answer)
		}
	})

	t.Run("a roster entry that is no printable-ASCII address names nobody", func(t *testing.T) {
		s := build(t, []string{"owner@example.invalid"}, config.Pool{
			Name: testSlipPoolName, Owner: "owner@exämple.invalid", Members: members,
		})
		resp, answer := grantAs(t, s, "owner@example.invalid")
		wantOneGrantRefusal(t, s, resp, answer, "owner@example.invalid", shared, testPoolCredential, testSlipPoolName)
	})
}

// ---------------------------------------------------------------------------
// G8: the roster is read at ISSUE time
// ---------------------------------------------------------------------------

// TestPoolResolvedAtIssue: the pools are resolved when the slip is issued and
// never remembered, so the member a publisher takes off a roster is refused at
// the very next grant (G8's "removed members vanish").
//
// It is the one property of this check a cache would hide, and the cache here is
// the SERVICE's own slip cache (R4): that cache holds a slip's BODY after a
// grant, and this test fails if the pool decision is made once and held beside
// it — the second grant would be answered from the decision the first one made.
// The read count is asserted beside the refusal, because a service that refuses
// the second grant for some other reason would pass the refusal alone.
func TestPoolResolvedAtIssue(t *testing.T) {
	shared := mustSharedToken(t, testPoolCredential)
	pools := func(allowed ...string) string {
		return poolPolicyDoc(t, config.Pool{
			Name:         testSlipPoolName,
			Owner:        testLauncherAccount,
			AllowedUsers: allowed,
			Members:      []string{testPoolCredential},
		})
	}

	s := slipPoolSvc(t, config.Pool{
		Name:         testSlipPoolName,
		Owner:        testLauncherAccount,
		AllowedUsers: []string{testSecondLauncherAccount},
		Members:      []string{testPoolCredential},
	})
	resp, answer := s.grantAs(t, testSecondLauncherAccount, testSecondLauncherSub, grantBody(t, s.slug, s.run, testJobSA, 3600, shared))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the first grant is answered %d, want 200: the pool allows this launcher: %s", resp.StatusCode, answer)
	}
	before := s.sharedPolicyReads()

	// The publisher takes the launcher off the roster and publishes the node
	// again. Nothing else changes: the pool keeps its owner and its member.
	s.policy.shared = decodeTestPolicy(t, pools())

	grantsSoFar := len(s.slipIDs(t))
	resp, answer = s.grantAs(t, testSecondLauncherAccount, testSecondLauncherSub, grantBody(t, s.slug, s.run, testJobSA, 3600, shared))
	wantGrantRefusal(t, s, resp, answer, testSecondLauncherAccount, shared, testPoolCredential, testSlipPoolName)
	if got := s.slipIDs(t); len(got) != grantsSoFar {
		t.Errorf("the refused grant wrote %v, want the %d record(s) already there", got, grantsSoFar)
	}
	if got := s.sharedPolicyReads(); got <= before {
		t.Errorf("the policy was read %d times over two grants, want a read at each: the roster is resolved at issue time and not remembered (G8)", got)
	}

	// And the owner still is: the pool was not emptied, one user was taken off
	// it.
	s.policy.shared = decodeTestPolicy(t, pools(testSecondLauncherAccount))
	if ans := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, 3600, testSlipPoolName)); ans.Slip == "" {
		t.Fatal("the grant answered no slip id")
	}
}

// ---------------------------------------------------------------------------
// D1942: a pool the migration found ownerless
// ---------------------------------------------------------------------------

// TestLegacyOpenPoolServesWhomItServedBefore: a pool carrying the migration's
// marker is served to everyone, exactly as the installation served it before
// pools had owners (D1942).
//
// It is the case the whole marker exists for (F34): `update` migrates every
// published pool that names no owner into a legacy-open one, and a service that
// read the marker as "the owner is still to be named" would refuse every run on
// such a pool at its next grant — a run that worked before the upgrade, stopped
// by an upgrade that is never allowed to stop one. So the launcher here is a
// person no roster names, and the grant is served.
func TestLegacyOpenPoolServesWhomItServedBefore(t *testing.T) {
	shared := mustSharedToken(t, testPoolCredential)
	s := slipPoolSvc(t, config.Pool{
		Name:       testSlipPoolName,
		Members:    []string{testPoolCredential},
		LegacyOpen: true,
	})
	resp, answer := s.grantAs(t, testThirdLauncherAccount, testThirdLauncherSub, grantBody(t, s.slug, s.run, testJobSA, 3600, shared))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the grant is answered %d, want 200: a legacy-open pool serves whom the installation served before it (D1942): %s", resp.StatusCode, answer)
	}
	var granted SlipGrantResponse
	if err := json.Unmarshal(answer, &granted); err != nil {
		t.Fatalf("the grant's answer is not one this contract reads: %v", err)
	}
	if sl := s.slip(t, granted.Slip); sl.Pool != testSlipPoolName {
		t.Errorf("the slip records the pool %q, want %q", sl.Pool, testSlipPoolName)
	}
}

// ---------------------------------------------------------------------------
// The other way the check can fail
// ---------------------------------------------------------------------------

// TestGrantRefusedWhenThePolicyCannotBeRead: a grant whose pools cannot be
// decided is refused, and refused as an unavailable service rather than as a
// request this contract does not read.
//
// The direction is the service's own rule for every other unreadable document
// (an unreadable switch is not an off switch, and an unreadable admission is not
// an empty one, D143): a policy that could not be read is a pool whose roster is
// unknown, and a roster that is unknown admits nobody. What the launcher does
// with the refusal is its own business and not a downgrade — it starts the run
// slip-less, and a credential only a slip serves is refused at the call (R9).
func TestGrantRefusedWhenThePolicyCannotBeRead(t *testing.T) {
	shared := mustSharedToken(t, testPoolCredential)
	s := slipPoolSvc(t, config.Pool{
		Name:    testSlipPoolName,
		Owner:   testLauncherAccount,
		Members: []string{testPoolCredential},
	})
	s.policy.sharedErr = errors.New("the policy node could not be read")

	resp, body := s.grantCall(t, grantBody(t, s.slug, s.run, testJobSA, 3600, shared))
	wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable)
	wantNoSlipWritten(t, s)
	lines := auditLines(t, s.audit)
	if len(lines) != 1 {
		t.Fatalf("the refused grant wrote %d audit lines, want exactly one: %v", len(lines), lines)
	}
	wantSlipLine(t, lines[0], PurposeSlipGrant, ErrorStateUnavailable, map[string]any{
		"caller": testLauncherAccount, "run": "", "repository": "", "job": "",
		"credential": "", "tier": "",
	})
}
