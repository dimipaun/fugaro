package credsvc

// PL9's service half: the route a person's OWN pool in a project is written and
// read through (personalpool_route.go).
//
// The claim this file exists for is the one no database rule can hold (F61): the
// pool's members and rule are keyed on the subject the service VERIFIED, so a
// member of the project cannot write another person's pool, cannot put a token
// in one, and cannot name a container that is not their own. The record carries
// names and never a value (F19).
//
// Everything here is hermetic in the launcherpool tests' sense: the store is the
// fake Realtime Database over the same rtdb client the service uses, and every
// string is a NAME.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/routing"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// personalPost posts one call of either personal-pool route as the launcher,
// with no run identity (both routes are the launcher class's).
func personalPost(t *testing.T, f *lpFixture, path, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+f.launcherToken()) }
	resp := f.postWith(t, f.http.Client(), path, body, "", "", "",
		append([]func(*http.Request){bearer}, mutate...)...)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	return resp, raw
}

// personalRecord reads a principal's pool straight out of the store, which is
// what "nothing was written" is judged by.
func personalRecord(t *testing.T, f *lpFixture, principal routing.Principal, pool string) (PersonalPoolRecord, bool) {
	t.Helper()
	rec, found, err := readPersonalPool(context.Background(), f.dbs, principal, pool)
	if err != nil {
		t.Fatalf("reading the fixture's store: %v", err)
	}
	return rec, found
}

// wantLine holds ONE act of the personal-pool routes to having written exactly
// one line, with the verified caller and the route's own purpose, and with no
// credential, run, repository or job — the shape a credentialless purpose's line
// has. It is wantDesignationLine with the caller as a parameter, because this
// route is answered for two different people in one test.
func wantLine(t *testing.T, s *lpFixture, before int, status, caller string) {
	t.Helper()
	lines := lpAudit(t, s)
	if len(lines) != before+1 {
		t.Fatalf("the route has written %d audit lines, want exactly one more than the %d before it", len(lines), before)
	}
	line := lines[before]
	if line["purpose"] != PurposePoolDesignate || line["status"] != status {
		t.Errorf("the line is %v/%v, want %q/%q", line["purpose"], line["status"], PurposePoolDesignate, status)
	}
	if line["caller"] != caller {
		t.Errorf("the line's caller is %v, want the verified address %q", line["caller"], caller)
	}
	for _, field := range []string{"credential", "tier", "run", "repository", "job"} {
		if got := line[field]; got != "" {
			t.Errorf("the line carries %s = %v, want none: a personal pool touches no credential and no run", field, got)
		}
	}
}

// TestAPersonalPoolServesItsOwnersRun is PL9's interface: the record a person
// writes is a pool the serving lookup and the designation route RESOLVE, so the
// runs that person launches may be served from it. The pool is designated like
// any other; the lookup answers it with the rule the record carries and its
// members as the containers they name; and another person and a service account
// are answered none, because the store is keyed on the subject the service
// verified (R9, R26, D2976).
func TestAPersonalPoolServesItsOwnersRun(t *testing.T) {
	s := newLauncherPools(t, true, lpPools()...)
	const mine = "mine-pool-of-my-own"
	before := len(lpAudit(t, s))
	resp, body := personalPost(t, s, PersonalPoolPath,
		mustBody(t, PersonalPoolRequest{Pool: mine, Rule: "ordered", Members: []string{testPoolCredential}}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the honest declare is %d, want it written: %s", resp.StatusCode, body)
	}
	wantDesignationLine(t, s, before, StatusOK)

	// The person's own designation names it: the pool is not the project's
	// published block, so the route reads the caller's own store for it.
	before = len(lpAudit(t, s))
	resp, body = s.lpDesignate(t, mustBody(t, PoolDesignateRequest{Pool: mine}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("designating a pool of one's own is %d, want it written: %s", resp.StatusCode, body)
	}
	wantDesignationLine(t, s, before, StatusOK)

	container, err := secretname.User(lpLauncher().ID, testPoolCredential)
	if err != nil {
		t.Fatalf("the fixture's own container name: %v", err)
	}
	lookup := launcherPools{s: s.server}
	ref, found, err := lookup.LauncherPool(context.Background(), lpLauncher(), "")
	if err != nil || !found {
		t.Fatalf("the lookup answered (%+v, %v, %v), want the person's own pool", ref, found, err)
	}
	if ref.Pool != mine || ref.Rule != routing.RuleOrdered || ref.Project != testProject || ref.Principal != lpLauncher() {
		t.Errorf("the reference is %+v, want the recorded pool, its resolved rule and the project", ref)
	}

	// And it is SERVED: the chain's launcher-pool source is the person's own
	// pool, its rule is the one the record carries, and the member it serves with
	// is the container the name in the record builds for that subject.
	w := s.walk(t, callBody("pool-mine"), lpBinding(lpLauncher(), lpCoverage(), ""))
	step, kept := lpStep(w)
	if !kept {
		t.Fatalf("the chain dropped the person's own pool, want the call served from %q", mine)
	}
	if step.name.Name != mine || step.pool.Rule != routing.RuleOrdered {
		t.Errorf("the source resolved to %+v, want %q and the rule the record carries", step.pool, mine)
	}
	if got := w.picked(t); got.Name != container || got.Tier != creds.TierA {
		t.Errorf("the call was served by %+v, want the caller's own container %q at tier a", got, container)
	}

	// Another person naming the same pool is answered none: the read is keyed on
	// the subject the service verified, so one person's pool is not another's.
	other, otherFound, err := lookup.LauncherPool(context.Background(), lpOtherLauncher(), mine)
	if err != nil || otherFound {
		t.Errorf("another person's lookup answered (%+v, %v, %v), want none: a personal pool is its owner's alone", other, otherFound, err)
	}
	// And a service account's runs draw on the project's pools, never on one of
	// these (D2976, D3181).
	sa, saFound, err := lookup.LauncherPool(context.Background(), saPrincipal(), mine)
	if err != nil || saFound {
		t.Errorf("a service account's lookup answered (%+v, %v, %v), want none", sa, saFound, err)
	}
}

// TestAPersonalPoolCannotShadowAPublishedName pins the boundary between the two
// stores: a personal pool whose name the published block ALSO holds is not
// served. The block's pool of that name is another launcher's, so the lookup
// refuses it (D3176) — and it must not fall through to the store for it, because
// the chain re-reads the published block by NAME and would serve that other
// pool's members instead of the person's own container.
func TestAPersonalPoolCannotShadowAPublishedName(t *testing.T) {
	s := newLauncherPools(t, true, lpPools()...)
	// lpOtherPool is the other person's published pool, and this launcher
	// declares a pool of their own under the same name.
	resp, body := personalPost(t, s, PersonalPoolPath,
		mustBody(t, PersonalPoolRequest{Pool: lpOtherPool, Members: []string{testPoolCredential}}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the honest declare is %d, want it written: %s", resp.StatusCode, body)
	}
	lookup := launcherPools{s: s.server}
	ref, found, err := lookup.LauncherPool(context.Background(), lpLauncher(), lpOtherPool)
	if err != nil || found {
		t.Fatalf("the lookup answered (%+v, %v, %v), want none: the published pool of that name is not this launcher's", ref, found, err)
	}
	// And a designation of it is refused for the same reason: the route reads
	// the block before the store.
	if resp, body := s.lpDesignate(t, mustBody(t, PoolDesignateRequest{Pool: lpOtherPool})); resp.StatusCode == http.StatusOK {
		t.Errorf("a designation of another person's pool was written: %s", body)
	}
	// The run that names it with --pool falls back rather than being served that
	// pool's member.
	wantFallback(t, s.walk(t, callBody("pool-shadow"), lpBinding(lpLauncher(), lpCoverage(), lpOtherPool)))
}

// TestAPersonalPoolIsKeyedOnTheVerifiedSubject is PL9's service half: the pool a
// person declares is written under the subject the service verified, its members
// are the names of containers of their OWN (a name outside the grammar a
// container carries is refused), and another person's call of the same pool's
// name is another record — the first one untouched. Every refusal here is after
// the caller was verified, so each owes exactly one audit line and no value.
func TestAPersonalPoolIsKeyedOnTheVerifiedSubject(t *testing.T) {
	s := newLauncherPools(t, true, lpPools()...)
	const mine = "mine-pool-of-my-own"

	t.Run("the caller's own pool is written and read back", func(t *testing.T) {
		before := len(lpAudit(t, s))
		resp, body := personalPost(t, s, PersonalPoolPath,
			mustBody(t, PersonalPoolRequest{Pool: mine, Rule: "ordered", Members: []string{testPoolCredential, testSecondPoolCredential}}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the honest declare is %d, want it written: %s", resp.StatusCode, body)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store: the answer is the caller's own state", got)
		}
		var rec PersonalPoolRecord
		if err := json.Unmarshal(body, &rec); err != nil {
			t.Fatalf("the answer is not a personal pool: %v (%s)", err, body)
		}
		want := PersonalPoolRecord{
			Principal: lpLauncher(),
			Project:   testProject,
			Pool:      mine,
			Rule:      routing.RuleOrdered,
			Members:   []string{testPoolCredential, testSecondPoolCredential},
		}
		if rec.Principal != want.Principal || rec.Project != want.Project || rec.Pool != want.Pool ||
			rec.Rule != want.Rule || strings.Join(rec.Members, ",") != strings.Join(want.Members, ",") {
			t.Errorf("the answer is %+v, want %+v", rec, want)
		}
		// The record is keyed on the caller's SUBJECT and holds the project the
		// service derived, never anything the request said.
		back, found := personalRecord(t, s, lpLauncher(), mine)
		if !found || back.Principal != lpLauncher() || back.Pool != mine {
			t.Errorf("the store holds (%+v, %v), want the record the answer named", back, found)
		}
		wantDesignationLine(t, s, before, StatusOK)

		before = len(lpAudit(t, s))
		resp, body = personalPost(t, s, PersonalPoolGetPath, mustBody(t, PersonalPoolGetRequest{Pool: mine}))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the read is %d, want the record: %s", resp.StatusCode, body)
		}
		var got PersonalPoolRecord
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("the read's answer is not a personal pool: %v", err)
		}
		if got.Principal != lpLauncher() || got.Pool != mine {
			t.Errorf("the read answered %+v, want the caller's own record", got)
		}
		wantDesignationLine(t, s, before, StatusOK)
	})

	t.Run("another person's pool of the same name is another record", func(t *testing.T) {
		before := len(lpAudit(t, s))
		resp, body := personalPost(t, s, PersonalPoolPath,
			mustBody(t, PersonalPoolRequest{Pool: mine, Members: []string{testPoolCredential}}),
			func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+s.launcherToken(func(c map[string]any) {
					c["sub"], c["email"] = testOtherLauncherSub, testOtherLauncherAccount
				}))
			})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the second person's declare is %d, want it written: %s", resp.StatusCode, body)
		}
		var rec PersonalPoolRecord
		if err := json.Unmarshal(body, &rec); err != nil {
			t.Fatalf("the answer is not a personal pool: %v", err)
		}
		if rec.Principal != lpOtherLauncher() {
			t.Errorf("the answer's principal is %+v, want the caller the service verified", rec.Principal)
		}
		if other, found := personalRecord(t, s, lpOtherLauncher(), mine); !found || other.Principal != lpOtherLauncher() {
			t.Errorf("the second person's pool is (%+v, %v), want their own record", other, found)
		}
		// The first person's pool is exactly what it was: one name, two nodes.
		if first, found := personalRecord(t, s, lpLauncher(), mine); !found ||
			strings.Join(first.Members, ",") != testPoolCredential+","+testSecondPoolCredential {
			t.Errorf("the first person's pool is (%+v, %v), want it untouched", first, found)
		}
		// The line is the OTHER caller's: an act is recorded under the identity
		// the service verified, which is what makes the two records two.
		wantLine(t, s, before, StatusOK, testOtherLauncherAccount)
	})

	refusals := []struct {
		name string
		path string
		body string
		says string
	}{
		{"a member that is not a container's name", PersonalPoolPath,
			mustBody(t, PersonalPoolRequest{Pool: mine, Members: []string{"Not_A_Member"}}), personalPoolMemberForeign},
		{"a member listed twice", PersonalPoolPath,
			mustBody(t, PersonalPoolRequest{Pool: mine, Members: []string{"one", "one"}}), "listed twice"},
		{"a rule this release does not serve", PersonalPoolPath,
			mustBody(t, PersonalPoolRequest{Pool: mine, Rule: "sideways"}), "names no rule this release serves pools by"},
		{"a body naming a principal", PersonalPoolPath,
			`{"pool":"` + mine + `","principal":"` + testOtherLauncherSub + `"}`,
			"the principal is the one the service verified"},
		{"a pool that is not a name", PersonalPoolPath, `{"pool":"a b c"}`, "the call names no pool"},
		{"a pool the caller has not declared", PersonalPoolGetPath,
			mustBody(t, PersonalPoolGetRequest{Pool: "never-declared"}), personalPoolNoSuchPool},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			before := len(lpAudit(t, s))
			held, was := personalRecord(t, s, lpLauncher(), mine)
			resp, body := personalPost(t, s, tc.path, tc.body)
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("the call was served: %d %s", resp.StatusCode, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
				t.Errorf("the refusal's type is %q, want %q", e.Error, ErrorRequestRefused)
			} else if !strings.Contains(e.Message, tc.says) {
				t.Errorf("the refusal %q does not say %q", e.Message, tc.says)
			}
			wantDesignationLine(t, s, before, ErrorRequestRefused)
			if got, found := personalRecord(t, s, lpLauncher(), mine); found != was || got.Pool != held.Pool {
				t.Errorf("the store holds (%+v, %v), want the record before the refusal (%+v, %v)", got, found, held, was)
			}
		})
	}

	t.Run("a call presenting a run identity", func(t *testing.T) {
		before := len(lpAudit(t, s))
		resp, body := personalPost(t, s, PersonalPoolPath, mustBody(t, PersonalPoolRequest{Pool: mine}),
			func(r *http.Request) { r.Header.Set(HeaderRunIdentity, s.runToken()) })
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("a call presenting a run identity was served: %d %s", resp.StatusCode, body)
		}
		if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
			t.Errorf("the refusal's type is %q, want %q", e.Error, ErrorRequestRefused)
		} else if !strings.Contains(e.Message, personalPoolNoRunIdentity) {
			t.Errorf("the refusal %q does not say %q", e.Message, personalPoolNoRunIdentity)
		}
		wantDesignationLine(t, s, before, ErrorRequestRefused)
	})

	// A service account's call is refused on BOTH routes: a personal pool is one
	// PERSON's own, and a service account's runs are served from a project pool
	// an admin fills (D3181). The account's token is one the launcher class
	// verifies — it is the same bearer the grant route answers — so the refusal
	// is this route's own and not the class's.
	t.Run("a service account's call", func(t *testing.T) {
		// The launcher class must ADMIT the account's own token for this
		// refusal to be the route's, which is what this fixture wires
		// (LauncherServiceAccounts) and the one above does not.
		sa := newServiceAccountPools(t, true, lpPools()...)
		bearer := func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+sa.jobToken(func(c map[string]any) {
				c["email"], c["sub"] = testRosterServiceAccount, testRosterServiceAccountSub
			}))
		}
		for _, tc := range []struct{ name, path, body string }{
			{"the declare route", PersonalPoolPath, mustBody(t, PersonalPoolRequest{Pool: mine, Members: []string{testPoolCredential}})},
			{"the read route", PersonalPoolGetPath, mustBody(t, PersonalPoolGetRequest{Pool: mine})},
		} {
			t.Run(tc.name, func(t *testing.T) {
				before := len(lpAudit(t, sa))
				resp, body := personalPost(t, sa, tc.path, tc.body, bearer)
				if resp.StatusCode == http.StatusOK {
					t.Fatalf("a service account's call was served: %d %s", resp.StatusCode, body)
				}
				if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
					t.Errorf("the refusal's type is %q, want %q", e.Error, ErrorRequestRefused)
				} else if !strings.Contains(e.Message, personalPoolNotAPerson) {
					t.Errorf("the refusal %q does not say %q", e.Message, personalPoolNotAPerson)
				}
				wantLine(t, sa, before, ErrorRequestRefused, testRosterServiceAccount)
				if _, found := personalRecord(t, sa, lpLauncher(), mine); found {
					t.Errorf("a service account's call wrote the store")
				}
			})
		}
	})

	// The operator's switches, which these routes read themselves because they
	// are answered ahead of the shared parse that reads them for every other
	// route: a stopped service decides nothing, so a pool is neither written nor
	// read back while it is stopped.
	t.Run("a stopped service", func(t *testing.T) {
		before := len(lpAudit(t, s))
		held, was := personalRecord(t, s, lpLauncher(), mine)
		s.kills.global = true
		resp, body := personalPost(t, s, PersonalPoolPath, mustBody(t, PersonalPoolRequest{Pool: mine, Members: []string{testPoolCredential}}))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("a stopped service answered %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
			t.Errorf("the refusal's type is %q, want %q", e.Error, ErrorStateUnavailable)
		}
		wantDesignationLine(t, s, before, ErrorStateUnavailable)
		s.kills.global = false
		if got, found := personalRecord(t, s, lpLauncher(), mine); found != was || got.Pool != held.Pool {
			t.Errorf("the store holds (%+v, %v), want the record before the switch (%+v, %v)", got, found, held, was)
		}
	})
}
