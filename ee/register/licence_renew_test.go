// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package register

// The tests of `fugaro licence renew --job` (plan K4.1): the installation's own
// renewal and revocation job, which K4.2's Scheduler job runs on the
// installation's timer rather than a person's.
//
// The fixture is licence_test.go's, so this file mints its keys and its renewal
// secrets the same way and nothing here is a literal: a key or a secret in a
// test is key material in a repository (.claude/rules/ee.md).
//
// What makes a job different from `licence refresh` is what the tests are
// shaped around, and each property has a test that fails if it is dropped:
//
//   - a licence server that cannot be reached is never a revocation, and never
//     a failed job: the key keeps its state and the check is recorded
//     (TestAnUnreachableServerNeverRevokes);
//   - a renewal writes the next key beside the old one, with the secret that
//     renews it in the container first, and nothing is deleted
//     (TestRenewalWritesTheNextKeyBesideTheOld);
//   - a revocation the server answers is recorded and not acted on: no key is
//     deleted, and the job stops asking about it
//     (TestRevokedKeyIsRecordedNotDeleted);
//   - an offline key contacts nothing at all, not even the secret container
//     (TestOfflineKeyNeverContactsTheServer).

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/keyservice"
	"github.com/dimipaun/fugaro/ee/licence"
	"github.com/dimipaun/fugaro/ee/licence/project"
	fcli "github.com/dimipaun/fugaro/internal/cli"
)

// jobServer is the licence server as the renewal job meets it: both of K2.3's
// routes on one address, each answering what the test tells it to and each
// keeping its own list of calls, because what the job asks next depends on what
// the route before it answered.
type jobServer struct {
	ts     *httptest.Server
	mu     sync.Mutex
	calls  map[string][]keyservice.RenewalCall
	answer func(route string, call keyservice.RenewalCall) (int, string)
}

// serveJobServer points the command's licence server at a fake carrying both
// routes. Every call is recorded before it is answered, so a test that makes
// the server refuse still reads what the job sent.
func (f *licenceFixture) serveJobServer(t *testing.T, answer func(route string, call keyservice.RenewalCall) (int, string)) *jobServer {
	t.Helper()
	s := &jobServer{calls: map[string][]keyservice.RenewalCall{}, answer: answer}
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || (r.URL.Path != keyservice.RouteStatus && r.URL.Path != keyservice.RouteRenew) {
			t.Errorf("the licence server was called %s %s, want POST %s or %s", r.Method, r.URL.Path, keyservice.RouteStatus, keyservice.RouteRenew)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the %s call's body: %v", r.URL.Path, err)
		}
		var call keyservice.RenewalCall
		if err := json.Unmarshal(raw, &call); err != nil {
			t.Errorf("the %s call's body is not a RenewalCall: %v", r.URL.Path, err)
		}
		status, out := http.StatusOK, `{"status":"ok"}`
		if s.answer != nil {
			status, out = s.answer(r.URL.Path, call)
		}
		s.mu.Lock()
		s.calls[r.URL.Path] = append(s.calls[r.URL.Path], call)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(s.ts.Close)
	licenceServerURL = s.ts.URL
	return s
}

// callsTo answers the calls one route received, in order.
func (s *jobServer) callsTo(route string) []keyservice.RenewalCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]keyservice.RenewalCall(nil), s.calls[route]...)
}

// count is how many calls every route received together, which is what a test
// asserting that the job asked nothing reads.
func (s *jobServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		n += len(c)
	}
	return n
}

// renewJob runs the job's own command line.
func (f *licenceFixture) renewJob(t *testing.T) (out, errOut string, err error) {
	t.Helper()
	return f.exec(t, "", "licence", "renew", "--job")
}

// licenceAnswerJSON renders one answer of the licence server's routes.
func licenceAnswerJSON(t *testing.T, answer keyservice.Answer) (int, string) {
	t.Helper()
	data, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	return http.StatusOK, string(data)
}

// installed gives the installation a key and the renewal secret that renews it,
// written the way `licence set` writes the pair: the key through the store, and
// one version of the container. It answers the key, the secret and the claim
// set, so a test never re-mints a pair it has to agree with.
func (f *licenceFixture) installed(t *testing.T, edit ...func(*licence.Claims)) (key, secret string, claims licence.Claims) {
	t.Helper()
	key, secret, claims = f.bundle(t, edit...)
	f.seedSecretContainer([]byte(secret))
	f.secrets.AllowAccess()
	install(t, f, key)
	return key, secret, claims
}

// shortTerm is D2202's connected key: a term of at most thirty days, renewed
// while the entitlement runs, so its renewal window — renew_before_days before
// the end of a term that is running out — is open every day of the last month
// of it. The fixture's own claims are a year long, which is the offline shape.
func shortTerm(f *licenceFixture) func(*licence.Claims) {
	return func(c *licence.Claims) {
		c.PaidThrough = f.now.Add(10 * 24 * time.Hour)
		c.RenewBeforeDays = 30
	}
}

// successor mints the key a renewal answers with: the next term of the same
// entitlement, a new key_id, and the renewal secret it renews with.
func (f *licenceFixture) successor(t *testing.T, from licence.Claims) (key, secret string, claims licence.Claims) {
	t.Helper()
	return f.bundle(t, func(c *licence.Claims) {
		c.KeyID = "lk_test_k41_0002"
		c.IssuedAt = f.now
		c.PaidThrough = from.PaidThrough.Add(30 * 24 * time.Hour)
	})
}

// secretWrites is how many versions the container was given, counted from the
// calls the installation made rather than by reading the container back: a read
// is itself a call, and a test asserting that a renewal wrote nothing has to
// count the writes and not its own bookkeeping.
func secretWrites(f *licenceFixture) int {
	n := 0
	for _, r := range f.secrets.Requests() {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, ":addVersion") {
			n++
		}
	}
	return n
}

// TestRenewalWritesTheNextKeyBesideTheOld holds the job's whole working day:
// the check, the renewal, the two writes in the order that leaves a pair rather
// than an installation that cannot renew, the record of the check, and the
// answer a person reads in the job's log.
func TestRenewalWritesTheNextKeyBesideTheOld(t *testing.T) {
	t.Run("the check, the renewal and the two writes", func(t *testing.T) {
		f := newLicenceFixture(t)
		key, secret, claims := f.installed(t, shortTerm(f))
		next, nextSecret, _ := f.successor(t, claims)
		server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			if route == keyservice.RouteStatus {
				return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
			}
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK, Key: next, RenewalSecret: nextSecret})
		})

		out, _, err := f.renewJob(t)
		if err != nil {
			t.Fatalf("licence renew --job = %v", err)
		}
		status, renew := server.callsTo(keyservice.RouteStatus), server.callsTo(keyservice.RouteRenew)
		if len(status) != 1 || len(renew) != 1 {
			t.Fatalf("the job asked /status %d times and /renew %d times, want one of each", len(status), len(renew))
		}
		for what, call := range map[string]keyservice.RenewalCall{"status": status[0], "renew": renew[0]} {
			if call.KeyID != claims.KeyID || call.WorkspaceID != licenceTestFirebase {
				t.Errorf("the %s call named key %q workspace %q, want %q and %q", what, call.KeyID, call.WorkspaceID, claims.KeyID, licenceTestFirebase)
			}
			if call.RenewalSecret != secret {
				t.Errorf("the %s call did not carry the secret the installation holds", what)
			}
		}

		obj := f.object(t)
		if len(obj.Keys) != 2 || obj.Keys[0] != key || obj.Keys[1] != next {
			t.Errorf("the project holds %d keys after the renewal, want the old key and the next one beside it: the server's overlap is what lets a machine that has not read the object yet keep working", len(obj.Keys))
		}
		if got := string(f.secrets.Latest(f.secretContainer())); got != nextSecret {
			t.Error("the renewal's new secret is not the container's newest version")
		}
		if n := secretWrites(f); n != 1 {
			t.Errorf("the container was given %d versions, want one: the secret the renewal replaced keeps working for the server's handover window and nothing retires it", n)
		}
		if obj.Check == nil {
			t.Fatal("the job recorded no check")
		}
		if obj.Check.Status != project.CheckOK || obj.Check.KeyID != claims.KeyID || obj.Check.Failures != 0 || !obj.Check.LastOK.Equal(f.now) {
			t.Errorf("the check record is %+v, want an ok check of key %s with no failures and last_ok at the check's own time", *obj.Check, claims.KeyID)
		}
		if !strings.Contains(out, "renewed") || !strings.Contains(out, project.Object) {
			t.Errorf("the job's answer %q does not say what was renewed or where it landed", out)
		}
		assertNoKeyMaterial(t, "the job's answer", out, key, secret)
		assertNoKeyMaterial(t, "the job's answer", out, next, nextSecret)

		// The installation reads the renewal back: the console's state is the
		// key the job wrote and the check it recorded.
		if got := f.status(t); got["state"] != "active" {
			t.Errorf("the installation reads %v after the renewal, want the next key's active term", got["state"])
		}
	})

	t.Run("the answer's secret is kept when the server sends none", func(t *testing.T) {
		// D2200: the server returns the entitlement's newest key, and a caller
		// that already holds it is not rotated, so the answer carries no secret
		// and the installation keeps the one it has.
		f := newLicenceFixture(t)
		key, secret, _ := f.installed(t, shortTerm(f))
		server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			if route == keyservice.RouteStatus {
				return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
			}
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK, Key: key})
		})

		out, _, err := f.renewJob(t)
		if err != nil {
			t.Fatalf("licence renew --job with an answer holding no secret = %v", err)
		}
		if len(server.callsTo(keyservice.RouteRenew)) != 1 {
			t.Fatal("the job did not ask for the renewal its window is open for")
		}
		if got := f.secrets.Latest(f.secretContainer()); string(got) != secret {
			t.Error("an answer that brought no new secret replaced the container's newest version")
		}
		if n := secretWrites(f); n != 0 {
			t.Errorf("the job wrote %d versions of the container, want none: D2200's answer carries no secret, so the installation keeps the one it renews with", n)
		}
		if obj := f.object(t); len(obj.Keys) != 1 || obj.Keys[0] != key {
			t.Errorf("the project holds %d keys, want the one the installation already had: a key it already holds is not a renewal", len(obj.Keys))
		}
		if !strings.Contains(out, "already holds") {
			t.Errorf("the job's answer %q does not say the key it was answered with is the one the installation runs on", out)
		}
	})

	t.Run("a newer key outside the window is fetched", func(t *testing.T) {
		// The other half of D2200: the store holds a key issued after this one,
		// which is the successor of a renewal this installation never read, so
		// the check asks for it whatever the window says.
		f := newLicenceFixture(t)
		_, _, claims := f.installed(t, func(c *licence.Claims) {
			// A term that is not running out: the window opens in ten days.
			c.PaidThrough = f.now.Add(40 * 24 * time.Hour)
			c.RenewBeforeDays = 30
		})
		next, nextSecret, _ := f.successor(t, claims)
		server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			if route == keyservice.RouteStatus {
				return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK, NewerKey: true})
			}
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK, Key: next, RenewalSecret: nextSecret})
		})

		if _, _, err := f.renewJob(t); err != nil {
			t.Fatalf("licence renew --job with a newer key on the server = %v", err)
		}
		if len(server.callsTo(keyservice.RouteRenew)) != 1 {
			t.Fatalf("the job asked for the renewal %d times, want once: the check answered that the store holds a key issued after this one", len(server.callsTo(keyservice.RouteRenew)))
		}
		if obj := f.object(t); len(obj.Keys) != 2 || obj.Keys[1] != next {
			t.Errorf("the project holds %d keys, want the successor the server already minted", len(obj.Keys))
		}
		if got := string(f.secrets.Latest(f.secretContainer())); got != nextSecret {
			t.Error("the fetched key's own secret is not the container's newest version")
		}
	})

	t.Run("a key that is not due is left alone", func(t *testing.T) {
		f := newLicenceFixture(t)
		_, _, claims := f.installed(t, func(c *licence.Claims) {
			c.PaidThrough = f.now.Add(40 * 24 * time.Hour)
			c.RenewBeforeDays = 30
		})
		if err := f.store(t).RecordCheck(context.Background(), f.now, project.CheckOK, claims.KeyID); err != nil {
			t.Fatal(err)
		}
		before := f.object(t).Raw
		server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			t.Errorf("the job called %s for a key whose check is not due and whose window is shut", route)
			return http.StatusOK, `{"status":"ok"}`
		})

		out, _, err := f.renewJob(t)
		if err != nil {
			t.Fatalf("licence renew --job of a key with nothing due = %v", err)
		}
		if server.count() != 0 {
			t.Fatalf("the job called the licence server %d times, want none", server.count())
		}
		if len(f.secrets.Requests()) != 0 {
			t.Error("the job read the renewal secret container for a key with nothing due")
		}
		if got := f.object(t); !bytes.Equal(got.Raw, before) {
			t.Error("a job with nothing due rewrote the project's object")
		}
		if !strings.Contains(out, "nothing to do") {
			t.Errorf("the job's answer %q does not say there was nothing to do", out)
		}

		// One check_interval later the check is due, and it says so by asking
		// once — the installation's own cadence and not the job's schedule.
		f.now = f.now.Add(24 * time.Hour)
		server = f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
		})
		if _, _, err := f.renewJob(t); err != nil {
			t.Fatalf("licence renew --job a day later = %v", err)
		}
		if got := len(server.callsTo(keyservice.RouteStatus)); got != 1 {
			t.Errorf("the job asked /status %d times a check_interval later, want once", got)
		}
		if got := len(server.callsTo(keyservice.RouteRenew)); got != 0 {
			t.Errorf("the job asked /renew %d times outside the renewal window, want none", got)
		}
	})
}

// TestAnUnreachableServerNeverRevokes holds D2140 for the job: a check that
// never reached the server is not an answer about the key, so the key keeps its
// state, the streak of failed checks is recorded, and the job is not a failure
// — a Scheduler job that failed every night an installation's route to the
// server was down would be retried and paged over a key that is still good.
func TestAnUnreachableServerNeverRevokes(t *testing.T) {
	t.Run("the key keeps its state", func(t *testing.T) {
		f := newLicenceFixture(t)
		// The fixture's default address is one nothing listens on, which is
		// what an installation with no route to the licence server meets.
		key, _, claims := f.installed(t, shortTerm(f))

		out, errOut, err := f.renewJob(t)
		if err != nil {
			t.Fatalf("licence renew --job against an unreachable server = %v, want the record to carry the news and the job to succeed", err)
		}
		obj := f.object(t)
		if len(obj.Revoked) != 0 {
			t.Errorf("a check that never reached the server recorded %v as revoked: only an explicit answer revokes", obj.Revoked)
		}
		if obj.Check == nil {
			t.Fatal("the job recorded no check for an unreachable server")
		}
		if obj.Check.Status != project.CheckUnreachable || obj.Check.Failures != 1 || obj.Check.KeyID != claims.KeyID {
			t.Errorf("the check record is %+v, want one unreachable check of key %s and a failure count of one", *obj.Check, claims.KeyID)
		}
		if !obj.Check.LastOK.IsZero() {
			t.Errorf("an unreachable check moved last_ok to %s: it records the last answer, and there was none", obj.Check.LastOK)
		}
		if len(obj.Keys) != 1 || obj.Keys[0] != key {
			t.Errorf("the project holds %d keys after an unreachable check, want the one it had", len(obj.Keys))
		}
		if strings.Contains(out+errOut, "cannot verify revocation") {
			t.Errorf("one failed check set the thirty-day warning:\n%s%s", out, errOut)
		}
		if got := f.status(t); got["state"] != "active" {
			t.Errorf("the installation reads %v after an unreachable check, want the key's own active term", got["state"])
		}
	})

	t.Run("thirty days of silence is said out loud", func(t *testing.T) {
		// D2140's warning counts from the last answer the server gave, so a
		// check that fails on top of one that answered a month ago is the
		// thirty-first day of an installation that cannot verify a revocation.
		f := newLicenceFixture(t)
		_, _, claims := f.installed(t, shortTerm(f))
		if err := f.store(t).RecordCheck(context.Background(), f.now.Add(-31*24*time.Hour), project.CheckOK, claims.KeyID); err != nil {
			t.Fatal(err)
		}

		out, errOut, err := f.renewJob(t)
		if err != nil {
			t.Fatalf("licence renew --job = %v", err)
		}
		if !strings.Contains(out+errOut, "cannot verify revocation") {
			t.Errorf("the job's answer does not warn that a revocation cannot be verified for an installation the server last answered 31 days ago:\n%s%s", out, errOut)
		}
		obj := f.object(t)
		if obj.Check == nil || obj.Check.Failures != 1 || !obj.Check.LastOK.Equal(f.now.Add(-31*24*time.Hour)) {
			t.Errorf("the check record is %+v, want the streak raised to one with last_ok where the server last answered", *obj.Check)
		}
	})

	t.Run("a streak with no answer at all", func(t *testing.T) {
		// The other way to thirty days: no answer ever, and the streak itself
		// is the span, one check interval per failure.
		f := newLicenceFixture(t)
		key, _, claims := f.installed(t, shortTerm(f))
		raw, err := json.Marshal(map[string]any{
			"v":               1,
			"keys":            []string{key},
			"revoked_key_ids": []string{},
			"last_check": map[string]any{
				"key_id":   claims.KeyID,
				"at":       f.now.Add(-29 * 24 * time.Hour).UTC().Format(time.RFC3339),
				"status":   project.CheckUnreachable,
				"failures": 30,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		writeRawObject(t, f, string(raw))

		out, errOut, err := f.renewJob(t)
		if err != nil {
			t.Fatalf("licence renew --job over a recorded streak = %v", err)
		}
		if !strings.Contains(out+errOut, "cannot verify revocation") {
			t.Errorf("the job's answer does not warn about a revocation it has never been able to verify:\n%s%s", out, errOut)
		}
		if obj := f.object(t); obj.Check == nil || obj.Check.Failures != 31 {
			t.Errorf("the check record is %+v, want the streak raised to 31", obj.Check)
		}
		if obj := f.object(t); len(obj.Revoked) != 0 {
			t.Errorf("a streak of unreachable checks recorded %v as revoked", obj.Revoked)
		}
	})
}

// TestRevokedKeyIsRecordedNotDeleted holds the server's one answer that is not
// a renewal: the key is revoked, the job records it — which is what makes the
// refusal true where a person looks next — and deletes nothing, because the key
// that is still in the object is the record of what was revoked, and a leaked
// key put back by hand must not come back clean (spec section 8).
func TestRevokedKeyIsRecordedNotDeleted(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, claims := f.installed(t, shortTerm(f))
	server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
		if route != keyservice.RouteStatus {
			t.Errorf("a revoked key was renewed: the job called %s", route)
		}
		return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusRevoked})
	})

	out, _, err := f.renewJob(t)
	if err != nil {
		t.Fatalf("licence renew --job of a revoked key = %v, want the record and a line", err)
	}
	if len(server.callsTo(keyservice.RouteRenew)) != 0 {
		t.Fatal("the job asked for a renewal after the server answered that the key is revoked")
	}
	obj := f.object(t)
	if !slices.Contains(obj.Revoked, claims.KeyID) {
		t.Errorf("the object records revocations %v, and the server revoked %s", obj.Revoked, claims.KeyID)
	}
	if len(obj.Keys) != 1 || obj.Keys[0] != key {
		t.Errorf("the project holds %d keys after a revocation, want the revoked key still there beside the record of it", len(obj.Keys))
	}
	if obj.Check == nil || obj.Check.Status != project.CheckRevoked {
		t.Errorf("the check record is %+v, want the revocation the server answered", obj.Check)
	}
	if got := f.status(t); got["state"] != "expired" || got["revoked"] != true {
		t.Errorf("the installation reads state %v revoked %v, want the revocation out loud", got["state"], got["revoked"])
	}
	if !strings.Contains(out, "revoked") {
		t.Errorf("the job's answer %q does not say the key was revoked", out)
	}
	assertNoKeyMaterial(t, "the job's answer", out, key, secret)

	// A later run asks nothing: a revoked key is not checked again, and it is
	// certainly not renewed, so an installation the server has answered for
	// stops talking to it until a person installs a new key.
	server = f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
		t.Errorf("the job called %s for a key the server has already answered revoked for", route)
		return http.StatusOK, `{"status":"ok"}`
	})
	if _, _, err := f.renewJob(t); err != nil {
		t.Fatalf("a second licence renew --job of a revoked key = %v", err)
	}
	if server.count() != 0 {
		t.Errorf("the second run called the licence server %d times, want none", server.count())
	}
	if obj := f.object(t); len(obj.Revoked) != 1 || !slices.Contains(obj.Revoked, claims.KeyID) {
		t.Errorf("the second run left revocations %v, want the one the server answered", obj.Revoked)
	}
}

// TestOfflineKeyNeverContactsTheServer holds spec section 7 for the job: an
// air-gapped key's installation contacts nothing at all — not the secret
// container, which it may hold a version in from a key that was replaced, and
// not the server — and the job's own two states that are not a renewal at all
// are answered rather than failed.
func TestOfflineKeyNeverContactsTheServer(t *testing.T) {
	f := newLicenceFixture(t)
	claims := f.claims(func(c *licence.Claims) {
		c.RenewalMode = licence.RenewalNone
		c.CheckInterval = nil
	})
	key := f.issuer.mint(t, claims)
	install(t, f, key)
	// The container is there with a version in it, so a job that read one is
	// caught rather than passing for lack of anything to read.
	f.seedSecretContainer([]byte(licenceTestSecret(t)))
	f.secrets.AllowAccess()
	before := f.object(t).Raw
	server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
		t.Errorf("an offline key's renewal job called %s", route)
		return http.StatusOK, `{"status":"ok"}`
	})

	out, _, err := f.renewJob(t)
	if err != nil {
		t.Fatalf("licence renew --job of an offline key = %v", err)
	}
	if !strings.Contains(out, "offline") {
		t.Errorf("the job's answer %q does not say the key is offline", out)
	}
	if server.count() != 0 {
		t.Errorf("the job called the licence server %d times for an offline key, want none", server.count())
	}
	if got := f.secrets.Requests(); len(got) != 0 {
		t.Errorf("the job made %d calls to Secret Manager for an offline key, want none: an offline key's installation reads no secret", len(got))
	}
	if got := f.object(t); !bytes.Equal(got.Raw, before) {
		t.Error("the job rewrote the project's object for an offline key")
	}
}

// TestLicenceRenewWithoutTheJobFlagIsRefused holds the flag that makes this
// verb the job's: the command does nothing but renew on a timer, and a person
// who typed it by hand is sent to the command that does what they meant.
func TestLicenceRenewWithoutTheJobFlagIsRefused(t *testing.T) {
	f := newLicenceFixture(t)
	f.installed(t, shortTerm(f))
	server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
		t.Errorf("a refused invocation called %s", route)
		return http.StatusOK, `{"status":"ok"}`
	})

	root := fcli.NewRootCmd()
	target, _, err := root.Find([]string{"licence"})
	if err != nil {
		t.Fatal(err)
	}
	var verbs []string
	for _, sub := range target.Commands() {
		verbs = append(verbs, sub.Name())
	}
	if !slices.Contains(verbs, "renew") {
		t.Fatalf("the built tree's `licence` has verbs %v, and `renew` is not one of them", verbs)
	}

	out, _, err := f.exec(t, "", "licence", "renew")
	if err == nil {
		t.Fatal("licence renew ran without --job")
	}
	if code := fcli.ExitCode(err); code != fcli.ExitUserError {
		t.Errorf("the refusal exited %d, want %d: %v", code, fcli.ExitUserError, err)
	}
	if !strings.Contains(err.Error(), "`fugaro licence refresh`") {
		t.Errorf("the refusal %q does not name the command a person meant", err.Error())
	}
	if out != "" {
		t.Errorf("a refused invocation wrote %q to stdout", out)
	}
	if server.count() != 0 {
		t.Errorf("a refused invocation called the licence server %d times", server.count())
	}
	if len(f.secrets.Requests()) != 0 {
		t.Error("a refused invocation read the renewal secret container")
	}
}

// TestLicenceRenewJobIsRefusedInAnAgentSession holds the rule every cloud write
// of this package owes: the job's own command, run inside a coding agent's
// session, writes nothing and talks to nobody.
func TestLicenceRenewJobIsRefusedInAnAgentSession(t *testing.T) {
	f := newLicenceFixture(t)
	f.installed(t, shortTerm(f))
	server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
		t.Errorf("a refused invocation called %s", route)
		return http.StatusOK, `{"status":"ok"}`
	})
	before := f.object(t).Raw
	t.Setenv("CLAUDECODE", "1")

	out, _, err := f.renewJob(t)
	if err == nil {
		t.Fatal("licence renew --job ran inside a coding agent's session")
	}
	if code := fcli.ExitCode(err); code != fcli.ExitUserError {
		t.Errorf("the refusal exited %d, want %d: %v", code, fcli.ExitUserError, err)
	}
	if !strings.Contains(err.Error(), "a coding agent's session is present (CLAUDECODE is set)") {
		t.Errorf("the refusal %q does not name the marker", err.Error())
	}
	if !strings.Contains(err.Error(), "nothing was renewed") {
		t.Errorf("the refusal %q does not say nothing was renewed", err.Error())
	}
	if out != "" {
		t.Errorf("a refused invocation wrote %q to stdout", out)
	}
	if server.count() != 0 {
		t.Errorf("a refused invocation called the licence server %d times", server.count())
	}
	if len(f.secrets.Requests()) != 0 {
		t.Error("a refused invocation read the renewal secret container")
	}
	if got := f.object(t); !bytes.Equal(got.Raw, before) {
		t.Error("a refused invocation rewrote the project's object")
	}
}

// TestLicenceRenewWithoutAKeyDoesNothing holds K4.2's condition for the job
// itself: it is applied to every installation that has a coordinator and its
// command no-ops with no key, which is the ordinary state of one that never
// bought a licence rather than a failure the Scheduler would retry.
func TestLicenceRenewWithoutAKeyDoesNothing(t *testing.T) {
	f := newLicenceFixture(t)
	f.seedSecretContainer([]byte(licenceTestSecret(t)))
	f.secrets.AllowAccess()
	server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
		t.Errorf("the job called %s with no key installed", route)
		return http.StatusOK, `{"status":"ok"}`
	})

	out, _, err := f.renewJob(t)
	if err != nil {
		t.Fatalf("licence renew --job with no key = %v, want a no-op", err)
	}
	if !strings.Contains(out, "`fugaro licence set`") {
		t.Errorf("the job's answer %q does not name the command that installs a key", out)
	}
	if server.count() != 0 {
		t.Errorf("the job called the licence server %d times with no key installed", server.count())
	}
	if got := f.secrets.Requests(); len(got) != 0 {
		t.Errorf("the job made %d calls to Secret Manager with no key installed, want none", len(got))
	}
	if f.object(t).Present {
		t.Error("a job with no key wrote an object")
	}
}
