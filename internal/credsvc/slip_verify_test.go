package credsvc

// DS1.6's tests: the slip on every call AFTER the claim (G3, G4, G12, R4, R5,
// R9, R10).
//
// The claim (DS1.3) decides the run's first call and answers it a claim secret;
// every call after it presents the slip's id and that secret, and this file is
// what the service does with the two. Four rules, and the test that pins each:
//
//   - the binding, checked per call (G3, R8): the id, the run identity and the
//     job account the call presents, and the claim secret, are held against the
//     record the id names, and the record must be live. An id this service
//     never wrote, another run's slip, a missing or wrong secret and a runtime
//     that has run out are one answer with one sentence
//     (TestOneAuditLinePerRefusal, TestMaxRuntimeEndsIssuance).
//   - a credential of the secret grammar — per-user or project-shared — is
//     served only under a slip that names it, and a slip-less call is offered
//     none of them (G12, R9). The slip-less path goes on serving exactly what
//     it served before this task (TestSlipLessCallForOwnedCredentialRefused).
//   - the slip BODY is cached five minutes and nothing else is (R4, F14): the
//     run's outcome and the operator's stop switches are read per call, so a
//     run that has ended is refused on its next call whatever the cache holds
//     (TestFinishedRunRefusedUncached), and what the cache buys and what it
//     costs are pinned together (TestCacheOnlyHoldsSlipBody).
//   - the audit line gains the slip and its owner (R5), and the rule that a
//     refusal writes exactly ONE line is held for the refusals this task adds
//     (TestOneAuditLinePerRefusal).
//
// Everything here is hermetic (Convention 9) and nothing in it is, or looks
// like, a credential (Convention 2): the claim secret is drawn by the service
// on the way through, and every name written down is a NAME — a bundle entry's
// own constant, or a secret ID built by the grammar itself.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// ---------------------------------------------------------------------------
// The fixture's own slip and the calls that carry it
// ---------------------------------------------------------------------------

// aSlip is a granted and claimed slip of the fixture's own run: the id its
// calls present, and the claim secret its runner holds in memory (D1941).
type aSlip struct {
	id     string
	secret string
}

// claimedSlip grants a slip for the fixture's own run naming the credentials
// the test gives, claims it as that run, and answers what a runner holds after
// the claim. It fails the test if either half is refused: a test that means to
// ask about a LATER call must not be passing because the slip was never there.
func (s *slips) claimedSlip(t *testing.T, runtimeSeconds int64, allowed ...string) aSlip {
	t.Helper()
	granted := s.grantOK(t, grantBody(t, s.slug, s.run, testJobSA, runtimeSeconds, allowed...))
	claimed := s.claimOK(t, granted.Slip, claimBody(t, ""))
	if claimed.ClaimSecret == "" {
		t.Fatal("the claim answered no claim secret")
	}
	return aSlip{id: granted.Slip, secret: claimed.ClaimSecret}
}

// withSlip is a call presenting a slip and the claim secret its runner holds:
// the two headers every call after the claim carries (G3, D1941).
func withSlip(id, secret string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set(HeaderSlip, id)
		r.Header.Set(HeaderSlipClaim, secret)
	}
}

// slipCall is the run's own model call under its slip: what a gateway in the
// runner sends once its run has claimed (DS1.5).
func (s *slips) slipCall(t *testing.T, sl aSlip, marker string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	return s.callTokens(t, callBody(marker), s.runToken(), s.jobToken(), s.job,
		append([]func(*http.Request){withSlip(sl.id, sl.secret)}, mutate...)...)
}

// serveCalls lets the fake upstream answer n calls: the fixture scripts one
// answer, and a test that serves a call more than once — from the cache, or
// after a failure it did not cause — has to say so.
func (s *slips) serveCalls(n int) {
	script := make([]anthropicfake.Reply, n)
	for i := range script {
		script[i] = anthropicfake.MessageOK(testModel, testUsage)
	}
	s.up.Script = script
}

// endTheRun writes the run's write-once outcome, which is what the service
// reads on every call from then on (auth.go's checkLive): the run has ended,
// whatever any slip record still says.
func (s *slips) endTheRun() {
	s.svc.db.set(budget.PathOutcome(budget.Day(s.now), s.slug, s.run))
}

// stillThere reports whether the slip's node is in the database, read through
// the same store the service writes it with.
func (s *slips) stillThere(t *testing.T, id string) bool {
	t.Helper()
	_, found, err := s.store.Slip(context.Background(), id)
	if err != nil {
		t.Fatalf("reading the slip %q back: %v", id, err)
	}
	return found
}

// anIdNobodyWrote is a slip id this service never minted, of the width and the
// alphabet a real one has (maxSlipIDBytes of hex, DS1.1's newSlipID): the id a
// call may present and the record it finds are two different things (G3), and
// this is the one that names nothing.
const anIdNobodyWrote = "0123456789abcdef0123456789abcdef"

// ---------------------------------------------------------------------------
// R9: the fence on credentials that belong to somebody
// ---------------------------------------------------------------------------

// TestSlipLessCallForOwnedCredentialRefused: G12, R9. A per-user or
// project-shared secret is served only under a slip that names it, and a call
// without one is offered none of them — the fence is the service's and not the
// resolution task's, because "a slip-less call for one fails closed" is a
// property of every call this service answers.
//
// The rule is asked of the one function that decides it (slipOffering), and it
// is asked with an owned name in the offering by hand, because today NO
// offering can hold one: the bundle's keys are a closed set of platform
// credentials (CredentialKey), so a per-user secret reaches a caller's list only
// through DS3.3's per-secret resolution, which resolves it under a slip. The
// fence is therefore inert until that task lands and fail-closed from the day it
// does, and this is the test that says which of the two it is.
//
// The end-to-end half is R9's second sentence, and it is the half that can be
// stated today: the slip-less path goes on serving exactly what it served
// before this task — the fixture's own run, carrying no slip headers at all, is
// served as it always was.
func TestSlipLessCallForOwnedCredentialRefused(t *testing.T) {
	owned := mustUserName(t, testOtherSub, "github-pat")
	bundle := creds.Credential{Name: testSlipCredential, Tier: creds.TierA}
	offered := func() []creds.Credential {
		return []creds.Credential{{Name: owned, Tier: creds.TierA}, bundle}
	}
	names := func(cands []creds.Credential) []string {
		out := make([]string, 0, len(cands))
		for _, c := range cands {
			out = append(out, c.Name)
		}
		return out
	}

	t.Run("a slip-less call is offered none of them", func(t *testing.T) {
		out, skipped := slipOffering(nil, offered(), nil)
		if got := names(out); len(got) != 1 || got[0] != bundle.Name {
			t.Errorf("the call is offered %v, want only the bundle's own entry", got)
		}
		wantSkipped(t, skipped, owned)
	})

	t.Run("a slip that does not name it is offered none of it either", func(t *testing.T) {
		sl := &slipBinding{id: anIdNobodyWrote, owner: testLauncherSub, allowed: []string{bundle.Name}}
		out, skipped := slipOffering(sl, offered(), nil)
		if got := names(out); len(got) != 1 || got[0] != bundle.Name {
			t.Errorf("the call is offered %v, want only the name the slip lists", got)
		}
		wantSkipped(t, skipped, owned)
	})

	t.Run("a slip that names it is served it", func(t *testing.T) {
		sl := &slipBinding{id: anIdNobodyWrote, owner: testLauncherSub, allowed: []string{owned, bundle.Name}}
		out, skipped := slipOffering(sl, offered(), nil)
		if len(out) != 2 {
			t.Errorf("the call is offered %v, want both names the slip lists", names(out))
		}
		if len(skipped) != 0 {
			t.Errorf("a call the slip covers was refused a credential: %v", skipped)
		}
	})

	t.Run("a call whose only credential needs a slip is offered nothing", func(t *testing.T) {
		out, skipped := slipOffering(nil, []creds.Credential{{Name: owned, Tier: creds.TierA}}, nil)
		if len(out) != 0 {
			t.Errorf("the call is offered %v, want nothing: an owned credential needs a slip", names(out))
		}
		wantSkipped(t, skipped, owned)
	})

	t.Run("a name the grammar cannot read still needs a slip", func(t *testing.T) {
		// The prefix is the whole of the test, as it is where a grant is held
		// to the same rule (checkGrantedNames): a name that announces a person
		// or the project and cannot be read as one is not thereby a bundle
		// entry's, so the fence is closed for it and not open.
		for _, name := range []string{
			secretname.UserPrefix + testOtherSub + "_GitHub-Pat",
			secretname.UserPrefix + testOtherSub,
			secretname.ProjectPrefix + "Shared-Token",
		} {
			out, skipped := slipOffering(nil, []creds.Credential{{Name: name, Tier: creds.TierA}}, nil)
			if len(out) != 0 {
				t.Errorf("a slip-less call is offered %v, want nothing: %q carries a grammar prefix and no slip covers it", names(out), name)
			}
			wantSkipped(t, skipped, name)
		}
	})

	t.Run("the slip-less path serves what it served before", func(t *testing.T) {
		s := newSlips(t, testLauncherAccount)
		resp, body := s.call(t, callBody("slip-less"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("a call with no slip at all is answered %d, want the 200 it was served before this task: %s", resp.StatusCode, body)
		}
	})
}

// wantSkipped holds the tried list to naming the one credential the call was
// not offered and why, which is how the run reads the fence.
func wantSkipped(t *testing.T, tried []Tried, name string) {
	t.Helper()
	for _, tr := range tried {
		if tr.Name == name && tr.Reason == ReasonNotAdmitted {
			return
		}
	}
	t.Errorf("the credential %q is not reported as not admitted in the tried list: %v", name, tried)
}

// ---------------------------------------------------------------------------
// R4: what is read per call and what is not
// ---------------------------------------------------------------------------

// TestFinishedRunRefusedUncached: G4, R4, F14. The run's liveness is the one
// thing the cache must never answer. It is read on every call (auth.go's
// checkLive asks the outcome node), and this is the test that fails if a call
// is decided from a slip body the cache is holding: the slip is claimed,
// unexpired and cached, and the run it belongs to has ended.
//
// The order is the point. An id this service never wrote is refused first — a
// cache keyed by an id the CALLER chose decides nothing, so nothing may be read
// from it and nothing may be written to it — and then the run's own slip serves
// a call, which is what warms the cache. Only then does the run end: the next
// call presents the same id and the same secret, the cached body still says the
// slip is claimed and inside its runtime, and the call is refused all the same.
func TestFinishedRunRefusedUncached(t *testing.T) {
	t.Run("an id this service never wrote is refused", func(t *testing.T) {
		s := newSlips(t, testLauncherAccount)
		sl := s.claimedSlip(t, 3600, testSlipCredential)

		// An id nobody wrote, with a secret that is not the claim's: refused,
		// and refused as the ONE sentence every refusal of the record is
		// answered with, so a caller learns nothing about which ids exist.
		resp, body := s.slipCall(t, aSlip{id: anIdNobodyWrote, secret: sl.secret}, "unknown-slip")
		if e := wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity); e.Message != slipNotThisRuns {
			t.Errorf("the refusal is answered %q, want %q", e.Message, slipNotThisRuns)
		}
	})

	t.Run("a run that has ended is refused on its next call", func(t *testing.T) {
		s := newSlips(t, testLauncherAccount)
		sl := s.claimedSlip(t, 3600, testSlipCredential)

		// The run's own slip, live: the call is served, which is what puts the
		// body in the cache.
		resp, body := s.slipCall(t, sl, "warm-the-cache")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the slip's own call is answered %d, want 200: %s", resp.StatusCode, body)
		}

		// The run ends. Its outcome is write-once and is read on every call —
		// and it is read from the database, not from the cached slip body,
		// which still says the slip is claimed and inside its runtime.
		s.endTheRun()
		resp, body = s.slipCall(t, sl, "after-the-outcome")
		e := wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)
		if !strings.Contains(e.Message, "the run has ended") {
			t.Errorf("the refusal is answered %q, want the run's own end to be why: a cached slip body is not what decides a finished run (R4)", e.Message)
		}
		// The run's outcome is also where its slip ends (DS1.1's End, R10's
		// other half): a run that has ended makes no further call this service
		// would serve, so nothing else would ever see the record again and it
		// would outlive the run it was granted for by up to its whole runtime.
		if s.stillThere(t, sl.id) {
			t.Error("the ended run's slip is still in the database: issuance ends at the run's outcome too")
		}
	})

	t.Run("the stop switches are read per call too", func(t *testing.T) {
		s := newSlips(t, testLauncherAccount)
		sl := s.claimedSlip(t, 3600, testSlipCredential)

		if resp, body := s.slipCall(t, sl, "warm-the-cache"); resp.StatusCode != http.StatusOK {
			t.Fatalf("the slip's own call is answered %d, want 200: %s", resp.StatusCode, body)
		}
		// The operator stops the service and the SAME call is refused for the
		// switch — not served from the cache, which still holds a live body.
		s.kills.global = true
		resp, body := s.slipCall(t, sl, "after-the-stop")
		wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable)
	})
}

// TestCacheOnlyHoldsSlipBody: R4 from the other side, and it is the two halves
// of one trade (F14). What the cache buys is a call that is not answered by a
// read of the database on every model call; what it costs is that a change to
// the slip's OWN fields — an ended record, an edited Allowed set — is seen up
// to five minutes late. Both are pinned here: the call after the record was
// ended is still served, and the same call is refused once the five minutes are
// up.
//
// The run's liveness is deliberately NOT what this test shows, and it must not
// be confused with it: a run that ends is refused on its next call
// (TestFinishedRunRefusedUncached), because the outcome is read per call. What
// the cache holds is the record, and for as long as the entry lives the record
// is read from it and from nowhere else.
func TestCacheOnlyHoldsSlipBody(t *testing.T) {
	if slipBodyTTL != 5*time.Minute {
		t.Fatalf("the slip body is cached for %s, want the spec's five minutes (R4)", slipBodyTTL)
	}

	s := newSlips(t, testLauncherAccount)
	s.serveCalls(2)
	sl := s.claimedSlip(t, 3600, testSlipCredential)

	resp, body := s.slipCall(t, sl, "warm-the-cache")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the slip's own call is answered %d, want 200: %s", resp.StatusCode, body)
	}

	// The record is ended under the service — what a revocation, or an
	// operator's own hand at the database, looks like — and the call is served
	// all the same: the body the cache holds is the one the call was admitted
	// by, and the entry has not reached its five minutes.
	if err := s.store.End(context.Background(), sl.id); err != nil {
		t.Fatalf("ending the slip: %v", err)
	}
	if s.stillThere(t, sl.id) {
		t.Fatal("the ended slip is still in the database")
	}
	resp, body = s.slipCall(t, sl, "served-from-the-cache")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call after the record was ended is answered %d, want the cached body's 200: R4 caches the slip body: %s", resp.StatusCode, body)
	}

	// A second past the entry's life the record is read again, and it is gone:
	// the id is nobody's and the call is refused. This is what the cache is
	// worth: five minutes, and no longer.
	s.now = s.now.Add(slipBodyTTL + time.Second)
	resp, body = s.slipCall(t, sl, "after-the-cache")
	if e := wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity); e.Message != slipNotThisRuns {
		t.Errorf("the refusal is answered %q, want %q", e.Message, slipNotThisRuns)
	}
}

// TestCallsDoNotSlideTheCacheWindow: the spec's cache is "keyed by slip ID,
// 5-minute lifetime", and a lifetime that every call renewed would be no
// lifetime at all for the run that calls this service most — every model call
// of a long run would renew it, and the "finished or revoked job keeps access
// for up to 5 minutes" the spec accepts would become "for as long as it keeps
// calling".
//
// The record is ended out from under the cache (the store's End, as another
// instance's max-runtime end or an operator's own hand would), the call inside
// the window is served from the body, and the call after the window — measured
// from the READ, not from the last hit — reads the record again and finds it
// gone. An implementation that put the body back on every call fails on the
// last step, which is the one this test exists for.
func TestCallsDoNotSlideTheCacheWindow(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	// Three answers: the mutant this test is written against serves the last
	// call rather than refusing it, and a script that ran out would report the
	// fake's own error instead of the answer the test is about.
	s.serveCalls(3)
	sl := s.claimedSlip(t, 3600, testSlipCredential)

	if resp, body := s.slipCall(t, sl, "warm-the-cache"); resp.StatusCode != http.StatusOK {
		t.Fatalf("the slip's own call is answered %d, want 200: %s", resp.StatusCode, body)
	}
	if err := s.store.End(context.Background(), sl.id); err != nil {
		t.Fatalf("ending the slip: %v", err)
	}

	// Inside the window of the read that cached the body, so the hit is
	// served — and, under a sliding window, would be the hit that renews it.
	s.now = s.now.Add(slipBodyTTL - time.Minute)
	if resp, body := s.slipCall(t, sl, "inside-the-window"); resp.StatusCode != http.StatusOK {
		t.Fatalf("the call inside the window is answered %d, want the cached body's 200: %s", resp.StatusCode, body)
	}

	// Past the window of that read, and two minutes short of the window a
	// sliding refresh would have opened.
	s.now = s.now.Add(2 * time.Minute)
	resp, body := s.slipCall(t, sl, "past-the-window")
	if e := wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity); e.Message != slipNotThisRuns {
		t.Errorf("the refusal is answered %q, want %q", e.Message, slipNotThisRuns)
	}
}

// ---------------------------------------------------------------------------
// R10: issuance ends at the max runtime
// ---------------------------------------------------------------------------

// TestMaxRuntimeEndsIssuance: R10, and the sentence is "ends", not "stops
// answering". A slip serves for the runtime it was granted and no longer, and
// when a call arrives past that runtime the record is ENDED — deleted, as
// DS1.1's End does it — so the id is nobody's for every later call and for
// every other reader of the database, and not merely for the call that found it
// expired.
//
// The cache does not extend the runtime, and that is the other half of the same
// rule: the BODY a call is decided from is cached, the decision is computed from
// it and the current clock per call, so an expiry that arrives inside the five
// minutes is still an expiry. The test warms the cache first and passes the
// runtime after, which is exactly the call a runtime read off the cached body
// alone would have served.
//
// The clock is the fixture's, so the runtime is granted short and then passed
// rather than waited out; the runtime is measured from the CLAIM (creds.Slip),
// which is where the run's own clock starts.
func TestMaxRuntimeEndsIssuance(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	const runtimeSeconds = 60
	sl := s.claimedSlip(t, runtimeSeconds, testSlipCredential)

	// Inside the runtime first, so the body is in the cache.
	if resp, body := s.slipCall(t, sl, "warm-the-cache"); resp.StatusCode != http.StatusOK {
		t.Fatalf("the slip's own call is answered %d, want 200: %s", resp.StatusCode, body)
	}

	// One second past what the slip was granted.
	s.now = s.now.Add((runtimeSeconds + 1) * time.Second)

	resp, body := s.slipCall(t, sl, "past-the-runtime")
	if e := wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity); e.Message != slipNotThisRuns {
		t.Errorf("the refusal is answered %q, want %q", e.Message, slipNotThisRuns)
	}

	// Issuance ends: the record is gone.
	if s.stillThere(t, sl.id) {
		t.Error("the slip is still in the database after its runtime passed: issuance ends at the max runtime (R10)")
	}

	// The ended id goes on being nobody's: the call is refused again, and so is
	// the same call made with the cache's own window passed.
	s.now = s.now.Add(slipBodyTTL + time.Second)
	resp, body = s.slipCall(t, sl, "after-the-runtime")
	if e := wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity); e.Message != slipNotThisRuns {
		t.Errorf("the refusal is answered %q, want %q", e.Message, slipNotThisRuns)
	}
}

// ---------------------------------------------------------------------------
// R5: one line per refusal, and the line's two new fields
// ---------------------------------------------------------------------------

// TestOneAuditLinePerRefusal: the credentials service's own rule — once the
// caller's identity is verified every refusal writes exactly ONE audit line —
// held for the refusals this task adds, and R5's two new fields held to what
// they may carry.
//
// Five refusals, and the line each writes: two slip ids, an id with no claim
// secret, an id this service never wrote, another run's slip, and a slip whose
// runtime has passed. Each is refused, each writes one line, and each line
// names the VERIFIED caller (the run, its repository, the job and the account)
// and NO slip: none of these calls was admitted by one, so there is no slip the
// line could honestly name.
//
// Then the other side, which is what R5 is for. A call that WAS admitted by a
// live slip carries it: the line of a call refused later on its own way (the
// project's policy could not be read) names the slip and the launcher that
// asked for the grant, and so does the line of a call that was served.
func TestOneAuditLinePerRefusal(t *testing.T) {
	refusals := []struct {
		name   string
		status int
		typ    string
		run    string
		call   func(t *testing.T, s *slips, sl aSlip) (*http.Response, []byte)
	}{
		{
			name:   "two slip ids",
			status: StatusRequestRefused, typ: ErrorRequestRefused,
			call: func(t *testing.T, s *slips, sl aSlip) (*http.Response, []byte) {
				return s.slipCall(t, sl, "two-ids", func(r *http.Request) { r.Header.Add(HeaderSlip, sl.id) })
			},
		},
		{
			name:   "an id with no claim secret",
			status: StatusRequestRefused, typ: ErrorRequestRefused,
			call: func(t *testing.T, s *slips, sl aSlip) (*http.Response, []byte) {
				return s.slipCall(t, sl, "no-secret", func(r *http.Request) { r.Header.Del(HeaderSlipClaim) })
			},
		},
		{
			name:   "an id this service never wrote",
			status: StatusUnverified, typ: ErrorUnverifiedRunIdentity,
			call: func(t *testing.T, s *slips, sl aSlip) (*http.Response, []byte) {
				return s.slipCall(t, aSlip{id: anIdNobodyWrote, secret: sl.secret}, "unknown-id")
			},
		},
		{
			name:   "another run's slip",
			status: StatusUnverified, typ: ErrorUnverifiedRunIdentity,
			// The line names the identity Verify RETURNED — the run the call
			// proved and not the one the slip was granted for.
			run: otherRun(),
			call: func(t *testing.T, s *slips, sl aSlip) (*http.Response, []byte) {
				return s.slipCall(t, sl, "another-run", func(r *http.Request) {
					r.Header.Set(HeaderRunIdentity, s.runTokenFor(otherRun()))
				})
			},
		},
		{
			name:   "a slip whose runtime has passed",
			status: StatusUnverified, typ: ErrorUnverifiedRunIdentity,
			call: func(t *testing.T, s *slips, sl aSlip) (*http.Response, []byte) {
				s.now = s.now.Add(time.Hour)
				return s.slipCall(t, sl, "past-the-runtime")
			},
		},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			s := newSlips(t, testLauncherAccount)
			sl := s.claimedSlip(t, 3600, testSlipCredential)

			resp, body := tc.call(t, s, sl)
			if e := wantRefusal(t, resp, body, tc.status, tc.typ); e.Message == "" {
				t.Error("the refusal names no reason")
			}
			run := s.run
			if tc.run != "" {
				run = tc.run
			}
			lines := auditLines(t, s.audit)
			if len(lines) != 1 {
				t.Fatalf("the refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
			}
			wantSlipLine(t, lines[0], PurposeForward, tc.typ, map[string]any{
				"run": run, "repository": s.slug, "job": s.job, "caller": testJobSA,
				"slip": "", "owner": "", "credential": "", "tier": "",
			})
		})
	}

	t.Run("a call admitted by a slip names it, refused later", func(t *testing.T) {
		s := newSlips(t, testLauncherAccount)
		sl := s.claimedSlip(t, 3600, testSlipCredential)
		s.policy.sharedErr = errors.New("the shared node could not be read")

		resp, body := s.slipCall(t, sl, "policy-unreadable")
		wantRefusal(t, resp, body, StatusUnavailable, ErrorStateUnavailable)

		lines := auditLines(t, s.audit)
		if len(lines) != 1 {
			t.Fatalf("the refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		wantSlipLine(t, lines[0], PurposeForward, ErrorStateUnavailable, map[string]any{
			"run": s.run, "repository": s.slug, "job": s.job, "caller": testJobSA,
			"slip": sl.id, "owner": testLauncherSub,
		})
	})

	t.Run("a served call names it too", func(t *testing.T) {
		s := newSlips(t, testLauncherAccount)
		sl := s.claimedSlip(t, 3600, testSlipCredential)

		resp, body := s.slipCall(t, sl, "served")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the call is answered %d, want 200: %s", resp.StatusCode, body)
		}
		lines := auditLines(t, s.audit)
		if len(lines) != 1 {
			t.Fatalf("the served call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		wantSlipLine(t, lines[0], PurposeForward, StatusOK, map[string]any{
			"run": s.run, "repository": s.slug, "job": s.job, "caller": testJobSA,
			"slip": sl.id, "owner": testLauncherSub,
		})
		if lines[0]["credential"] == "" {
			t.Error("the issuance line names no credential")
		}
	})

	t.Run("a refusal never repeats the claim secret", func(t *testing.T) {
		// The claim secret is the one credential of these routes: a refusal,
		// the service's own log line and the answer the caller reads carry the
		// FIELD that was wrong and never what the call sent it with (the
		// no-echo rule).
		s := newSlips(t, testLauncherAccount)
		sl := s.claimedSlip(t, 3600, testSlipCredential)

		resp, body := s.slipCall(t, aSlip{id: anIdNobodyWrote, secret: sl.secret}, "unknown-id")
		wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)
		raw := s.audit.String() + s.logs.String() + string(body)
		if strings.Contains(raw, sl.secret) {
			t.Error("a refusal repeated the claim secret the call presented")
		}
	})
}

// ---------------------------------------------------------------------------
// What the service does with the two values
// ---------------------------------------------------------------------------

// TestTheSlipSecretIsNeverForwarded: the run's slip is the service's own
// business and not Anthropic's (C1.11's no-echo rule). The id and the claim
// secret travel in this service's own headers, which the forwarded call drops
// whole, and the SECRET is registered with the call's scrub as well: a value
// pasted into the request body — by the run, or by anything that got hold of
// it — must not reach the upstream's logs. The second half is the one that
// matters: a header rule alone would leave a secret in a prompt.
func TestTheSlipSecretIsNeverForwarded(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	sl := s.claimedSlip(t, 3600, testSlipCredential)

	// The claim secret pasted into the body of the call, which is where a leak
	// would land: the service replaces it before the request goes anywhere.
	body := callBody("warming up with " + sl.secret)
	resp, answer := s.callTokens(t, body, s.runToken(), s.jobToken(), s.job, withSlip(sl.id, sl.secret))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the call is answered %d, want 200: %s", resp.StatusCode, answer)
	}

	req := s.firstUpstream(t)
	for _, name := range []string{HeaderSlip, HeaderSlipClaim} {
		if got := req.Header.Values(name); len(got) != 0 {
			t.Errorf("the upstream call carries %s (%d values)", name, len(got))
		}
	}
	up := string(s.up.Body(0))
	if strings.Contains(up, sl.secret) {
		t.Error("the forwarded body carries the claim secret")
	}
	if !strings.Contains(up, redactedValue) {
		t.Error("the forwarded body was not scrubbed of the claim secret")
	}
}

// TestTheSlipCacheHoldsOnlyVerifiedBodies: the cache is keyed by an id the
// CALLER chose, so what may be written into it is the narrowest set there is: a
// record this service read back and admitted THIS call by. A cache that held
// what a refused call found would be a place for one run's call to plant a
// record another call is decided from.
//
// And the other half, which is a security property of its own: a wrong secret
// does not END the slip. The id is public (G5) and any run of the installation
// holds a job token, so ending the record of a call that presented a bad secret
// would be a way to stop a working run by knowing its id.
func TestTheSlipCacheHoldsOnlyVerifiedBodies(t *testing.T) {
	s := newSlips(t, testLauncherAccount)
	sl := s.claimedSlip(t, 3600, testSlipCredential)

	resp, body := s.slipCall(t, aSlip{id: sl.id, secret: sl.secret + "-not-it"}, "wrong-secret")
	wantRefusal(t, resp, body, StatusUnverified, ErrorUnverifiedRunIdentity)

	if _, cached := s.server.slips.get(sl.id, s.now); cached {
		t.Error("a refused call's record was cached: what is cached is the body a call was ADMITTED by")
	}
	if !s.stillThere(t, sl.id) {
		t.Error("a call with a wrong claim secret ended the slip")
	}

	if resp, body := s.slipCall(t, sl, "the-right-secret"); resp.StatusCode != http.StatusOK {
		t.Fatalf("the slip's own call is answered %d, want 200: %s", resp.StatusCode, body)
	}
	if _, cached := s.server.slips.get(sl.id, s.now); !cached {
		t.Error("the admitted call's record was not cached")
	}
}

// TestCallSlipHeadersAreReadOnce: the two headers are this contract's own and
// are read the way every header of it is (oneHeader, slipSecretFromHTTP): a
// value sent twice is a request this service does not read rather than one it
// guesses at, and a call that presents a claim secret and no slip id presents
// the secret of nothing.
func TestCallSlipHeadersAreReadOnce(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{
			name:   "the claim secret twice",
			mutate: func(r *http.Request) { r.Header.Add(HeaderSlipClaim, "another-value-entirely") },
		},
		{
			name:   "a claim secret and no slip id",
			mutate: func(r *http.Request) { r.Header.Del(HeaderSlip) },
		},
		{
			name:   "a slip id that is not one",
			mutate: func(r *http.Request) { r.Header.Set(HeaderSlip, strings.Repeat("z", maxSlipIDBytes+1)) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSlips(t, testLauncherAccount)
			sl := s.claimedSlip(t, 3600, testSlipCredential)

			resp, body := s.slipCall(t, sl, "unreadable", tc.mutate)
			wantRefusal(t, resp, body, StatusRequestRefused, ErrorRequestRefused)
			if lines := auditLines(t, s.audit); len(lines) != 1 {
				t.Fatalf("the refused call wrote %d audit lines, want exactly 1", len(lines))
			}
		})
	}
}

// TestSlipOwnerIsTheLauncherRule holds audit.go's spelling of the owner's rule
// to auth.go's (DS1.2). The owner a line carries is the launcher's `sub` that
// the claim verified and wrote into the record, so the two places that say
// what a launcher's `sub` IS say one thing: digits, at most
// maxLauncherSubDigits of them, and a string the issuer wrote rather than a
// number this code converted.
func TestSlipOwnerIsTheLauncherRule(t *testing.T) {
	cases := []struct {
		name string
		sub  string
		want bool
	}{
		{"a launcher's sub", testLauncherSub, true},
		{"the longest one", strings.Repeat("9", maxLauncherSubDigits), true},
		{"one digit too many", strings.Repeat("9", maxLauncherSubDigits+1), false},
		{"empty", "", false},
		{"a leading sign", "+" + testLauncherSub, false},
		{"a space", "1043 5011", false},
		{"a decimal point", "1043.5", false},
		{"a letter", "1043a5011", false},
		{"a non-ASCII digit", "١٤٣", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner := validSlipOwner(tc.sub)
			sub, subOK := launcherSub(map[string]any{"sub": tc.sub})
			if owner != subOK || (subOK && sub != tc.sub) {
				t.Fatalf("validSlipOwner(%q) = %v and launcherSub answered (%q, %v): one rule, two spellings, and they have drifted", tc.sub, owner, sub, subOK)
			}
			if owner != tc.want {
				t.Fatalf("validSlipOwner(%q) = %v, want %v", tc.sub, owner, tc.want)
			}
		})
	}
	// A `sub` that arrived as a JSON number is refused by both: it is not the
	// string the issuer writes, and a value this code stringified would be a
	// second spelling of an account id.
	if sub, ok := launcherSub(map[string]any{"sub": float64(104329501182774832335)}); ok {
		t.Fatalf("launcherSub accepted a numeric sub as %q", sub)
	}
	if validSlipOwner("1.0432950118277483e+20") {
		t.Fatal("validSlipOwner accepted a stringified number")
	}
}
