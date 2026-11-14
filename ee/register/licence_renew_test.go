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
//   - a renewal's two writes can be parted by a crash, and the half-written
//     pair is repaired rather than abandoned
//     (TestTheJobCompletesAPairACrashLeftOpen), which is what the order of the
//     two writes is for (TestAFailedSecretWriteRenewsNothing);
//   - the renewal secret goes to the licence server's address and nowhere else,
//     on the check route as well as the renewal one
//     (TestLicenceRenewJobRefusesARedirect);
//   - a revocation the server answers is recorded and not acted on: no key is
//     deleted, and the job stops asking about it
//     (TestRevokedKeyIsRecordedNotDeleted);
//   - a manual key is checked and never renewed, and the job says which command
//     renews it (TestAManualKeyIsCheckedAndNeverRenewed);
//   - nothing of the renewal is written before the answer is one the project
//     will apply, neither a key that names a secret this installation does not
//     hold nor one the project's later term beats
//     (TestARenewalThatIsNotThePairWritesNothing);
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
	"github.com/dimipaun/fugaro/internal/localcfg"
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
	// redirectTo, when set, is the Location every answer carries: the address a
	// client that follows one would carry the call to next, which is what the
	// job's own client must not do.
	redirectTo string
}

// redirect makes the server answer every route with a 308 to location. It takes
// the lock the answer is built under so that a test setting it and the server
// goroutine reading it are ordered.
func (s *jobServer) redirect(location string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redirectTo = location
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
		location := s.redirectTo
		s.mu.Unlock()
		if location != "" {
			// A 308 keeps the method and the body: what a client that follows
			// one re-sends is this very call, renewal secret and all.
			status, out = http.StatusPermanentRedirect, ""
			w.Header().Set("Location", location)
		}
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

// manual is the renewal mode whose next term is a person's `licence refresh`
// rather than the job's: the job still checks the key — a revocation reaches it
// as it does any other — and never fetches one (spec section 7).
func manual(c *licence.Claims) { c.RenewalMode = licence.RenewalManual }

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

// crashAfterTheSecret leaves the state a renewal leaves when it writes its new
// secret version and dies before the key: the container's newest version is the
// secret the successor names, and the key that renews is still the one whose
// hash names the version beside it. It writes that version through the same
// helper the job writes one with, so the fixture is the state rather than a
// second opinion about it.
func (f *licenceFixture) crashAfterTheSecret(t *testing.T, secret string) {
	t.Helper()
	lc, err := localcfg.Load(f.config)
	if err != nil {
		t.Fatalf("reading the fixture's project config: %v", err)
	}
	secrets, err := openLicenceSecrets(context.Background(), lc)
	if err != nil {
		t.Fatalf("opening the renewal secret container: %v", err)
	}
	if err := secrets.add(context.Background(), []byte(secret)); err != nil {
		t.Fatalf("writing the version a crashed renewal left: %v", err)
	}
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

	t.Run("a check about another key leaves this one due", func(t *testing.T) {
		// The check record is about a key, and a key installed since the last
		// check — the successor a renewal wrote, or one a person set — has
		// never been checked at all: its revocation is asked about at once
		// rather than a check_interval after another key's answer.
		f := newLicenceFixture(t)
		_, _, claims := f.installed(t, func(c *licence.Claims) {
			// A term that is not running out, so the window is shut and the
			// only thing the job may do is ask the check.
			c.PaidThrough = f.now.Add(40 * 24 * time.Hour)
			c.RenewBeforeDays = 30
		})
		if err := f.store(t).RecordCheck(context.Background(), f.now, project.CheckOK, "lk_test_k41_0002"); err != nil {
			t.Fatal(err)
		}
		server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
		})

		if _, _, err := f.renewJob(t); err != nil {
			t.Fatalf("licence renew --job = %v", err)
		}
		if got := len(server.callsTo(keyservice.RouteStatus)); got != 1 {
			t.Errorf("the job asked /status %d times for a key the last check was not about, want once: that key has never been checked", got)
		}
		if got := len(server.callsTo(keyservice.RouteRenew)); got != 0 {
			t.Errorf("the job asked /renew %d times outside the renewal window, want none", got)
		}
		if obj := f.object(t); obj.Check == nil || obj.Check.KeyID != claims.KeyID {
			t.Errorf("the check record is %+v, want the check of key %s this run made", obj.Check, claims.KeyID)
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

// TestAManualKeyIsCheckedAndNeverRenewed holds spec section 7 for the other
// mode the job does not renew: only an auto key is fetched by it. A manual key
// is still checked, because a revocation reaches it as it does any other and
// the record of that is what a person reads, but its next term is the person's
// `fugaro licence refresh` — the job says so rather than rotating a key its
// owner meant to rotate by hand.
func TestAManualKeyIsCheckedAndNeverRenewed(t *testing.T) {
	t.Run("a manual key in its window is checked and not renewed", func(t *testing.T) {
		f := newLicenceFixture(t)
		key, secret, claims := f.installed(t, shortTerm(f), manual)
		next, nextSecret, _ := f.successor(t, claims)
		server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			if route == keyservice.RouteRenew {
				return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK, Key: next, RenewalSecret: nextSecret})
			}
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
		})

		out, _, err := f.renewJob(t)
		if err != nil {
			t.Fatalf("licence renew --job of a manual key in its window = %v", err)
		}
		if got := len(server.callsTo(keyservice.RouteStatus)); got != 1 {
			t.Errorf("the job asked /status %d times for a manual key, want once: the check is the job's for every key, a manual one included", got)
		}
		if got := len(server.callsTo(keyservice.RouteRenew)); got != 0 {
			t.Errorf("the job asked /renew %d times for a manual key, want none: the job fetches an auto key and no other (spec section 7)", got)
		}
		if obj := f.object(t); len(obj.Keys) != 1 || obj.Keys[0] != key {
			t.Errorf("the project holds %d keys after a manual key's job run, want the one it had", len(obj.Keys))
		}
		if n := secretWrites(f); n != 0 {
			t.Errorf("the container was given %d versions, want none: nothing of the renewal happened", n)
		}
		if got := f.secrets.Latest(f.secretContainer()); string(got) != secret {
			t.Error("a manual key's job run wrote the container")
		}
		if obj := f.object(t); obj.Check == nil || obj.Check.Status != project.CheckOK || !obj.Check.LastOK.Equal(f.now) {
			t.Errorf("the check record is %+v, want the check the job made and no renewal", obj.Check)
		}
		if !strings.Contains(out, "renewal_mode: manual") || !strings.Contains(out, "`fugaro licence refresh`") {
			t.Errorf("the job's answer %q does not say the key renews by hand and does not name the command that renews it", out)
		}
		assertNoKeyMaterial(t, "the job's answer", out, key, secret)
	})

	t.Run("a manual key the server has a newer key for is not renewed either", func(t *testing.T) {
		// D2200's newer_key is a renewal the job may fetch whatever the window
		// says, and for a manual key the window is not what decides.
		f := newLicenceFixture(t)
		key, _, _ := f.installed(t, func(c *licence.Claims) {
			// A term that is not running out, so the check's own answer is the
			// only thing that could make this run renew.
			c.PaidThrough = f.now.Add(40 * 24 * time.Hour)
			c.RenewBeforeDays = 30
			c.RenewalMode = licence.RenewalManual
		})
		server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			if route == keyservice.RouteRenew {
				t.Error("the job asked for the renewal of a manual key the server has a newer key for")
			}
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK, NewerKey: true})
		})

		out, _, err := f.renewJob(t)
		if err != nil {
			t.Fatalf("licence renew --job of a manual key with a newer key on the server = %v", err)
		}
		if got := len(server.callsTo(keyservice.RouteRenew)); got != 0 {
			t.Errorf("the job asked /renew %d times, want none", got)
		}
		if obj := f.object(t); len(obj.Keys) != 1 || obj.Keys[0] != key {
			t.Errorf("the project holds %d keys, want the one it had", len(obj.Keys))
		}
		if !strings.Contains(out, "`fugaro licence refresh`") {
			t.Errorf("the job's answer %q does not point at the command that renews a manual key", out)
		}
	})

	t.Run("a manual key outside its window is only checked", func(t *testing.T) {
		// The line is about a renewal the job is weighing, and a run with the
		// window shut is not weighing one: saying it every night would be noise
		// in a log nobody reads for that.
		f := newLicenceFixture(t)
		_, _, claims := f.installed(t, func(c *licence.Claims) {
			c.PaidThrough = f.now.Add(40 * 24 * time.Hour)
			c.RenewBeforeDays = 30
			c.RenewalMode = licence.RenewalManual
		})
		server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			if route != keyservice.RouteStatus {
				t.Errorf("the job called %s for a manual key outside its renewal window", route)
			}
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
		})

		out, _, err := f.renewJob(t)
		if err != nil {
			t.Fatalf("licence renew --job of a manual key outside its window = %v", err)
		}
		if got := len(server.callsTo(keyservice.RouteStatus)); got != 1 {
			t.Errorf("the job asked /status %d times, want once: the check is the job's for every key", got)
		}
		if strings.Contains(out, "renews manually") {
			t.Errorf("the job's answer %q talks about a renewal it is not weighing: the window is shut and the server holds no newer key", out)
		}
		if obj := f.object(t); obj.Check == nil || obj.Check.KeyID != claims.KeyID {
			t.Errorf("the check record is %+v, want the check this run made", obj.Check)
		}
	})

	t.Run("a manual key in its window with a fresh check reads no container", func(t *testing.T) {
		// A run with neither a check nor a renewal to make needs no proof of
		// anything, and the container this installation holds may belong to a
		// key that was replaced, or be a container its own account may not list.
		// Reading it here would fail the job every night of a window in which
		// it is never going to renew, over a proof it never uses.
		f := newLicenceFixture(t)
		key, secret, claims := f.bundle(t, shortTerm(f), manual)
		install(t, f, key)
		if err := f.store(t).RecordCheck(context.Background(), f.now, project.CheckOK, claims.KeyID); err != nil {
			t.Fatal(err)
		}
		server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			t.Errorf("the job called %s, want none: the check is fresh and the job renews no manual key", route)
			return http.StatusOK, `{"status":"ok"}`
		})

		out, _, err := f.renewJob(t)
		if err != nil {
			t.Fatalf("licence renew --job of a manual key whose check is not due = %v", err)
		}
		if server.count() != 0 {
			t.Errorf("the job called the licence server %d times, want none", server.count())
		}
		if got := f.secrets.Requests(); len(got) != 0 {
			t.Errorf("the job made %d calls to Secret Manager, want none: nothing this run does needs the renewal secret", len(got))
		}
		if obj := f.object(t); len(obj.Keys) != 1 || obj.Keys[0] != key {
			t.Errorf("the project holds %d keys, want the one it had", len(obj.Keys))
		}
		if !strings.Contains(out, "renews manually") {
			t.Errorf("the job's answer %q does not say the key renews by hand", out)
		}
		assertNoKeyMaterial(t, "the job's answer", out, key, secret)
	})
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

// TestTheJobCompletesAPairACrashLeftOpen holds the half of the crash the job
// adds rather than repeats: a renewal's two writes can be parted, and the state
// that leaves — a container one version ahead of the key, holding a secret
// named by a key that never landed — is repaired by the run that follows rather
// than abandoned. The installed key proves itself with the version beside the
// newest one, and the pair is completed with the answer the server repeats
// inside its twenty-four hour handover.
func TestTheJobCompletesAPairACrashLeftOpen(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, claims := f.installed(t, shortTerm(f))
	next, nextSecret, _ := f.successor(t, claims)
	f.crashAfterTheSecret(t, nextSecret)
	wrote := secretWrites(f)

	server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
		if call.RenewalSecret != secret {
			t.Errorf("the %s call proved itself with a secret that is not the installed key's: the container's newest version was written for a key that never landed, and one version behind is the only proof this key has", route)
		}
		if route == keyservice.RouteStatus {
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
		}
		return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK, Key: next, RenewalSecret: nextSecret})
	})

	out, _, err := f.renewJob(t)
	if err != nil {
		t.Fatalf("licence renew --job over a half-written pair = %v, want the pair completed", err)
	}
	if n := server.count(); n != 2 {
		t.Errorf("the job called the licence server %d times, want the check and the renewal: a key one version behind is what the job is here to repair", n)
	}
	obj := f.object(t)
	if len(obj.Keys) != 2 || obj.Keys[0] != key || obj.Keys[1] != next {
		t.Errorf("the project holds %d keys over a half-written pair, want the key and the successor that completes it", len(obj.Keys))
	}
	if got := string(f.secrets.Latest(f.secretContainer())); got != nextSecret {
		t.Error("the container's newest version is no longer the one the crashed renewal wrote")
	}
	if n := secretWrites(f); n != wrote {
		t.Errorf("the job wrote %d further versions of the container, want none: the secret the answer carries is the version the crash left, and a second copy of it is not a renewal", n-wrote)
	}
	if !strings.Contains(out, "renewed") {
		t.Errorf("the job's answer %q does not say the pair was completed", out)
	}
	assertNoKeyMaterial(t, "the job's answer", out, key, secret)
	assertNoKeyMaterial(t, "the job's answer", out, next, nextSecret)
}

// TestARenewalThatIsNotThePairWritesNothing holds the three guards between the
// answer and the writes. All are refusals of an answer the server may send and
// this installation may not act on, and all are answered before either write:
// the renewal secret written beside a key the installation does not run on
// takes the renewal away from the key it does, and the key written beside one it
// has recorded revoked is the downgrade that record names.
func TestARenewalThatIsNotThePairWritesNothing(t *testing.T) {
	t.Run("a key whose digest names another secret", func(t *testing.T) {
		// D2135: the key carries the digest of the secret it renews with, and a
		// key naming a secret this installation does not hold would be
		// installed beside the version that proves the key it replaces.
		f := newLicenceFixture(t)
		key, secret, claims := f.installed(t, shortTerm(f))
		next, _, _ := f.successor(t, claims)
		stranger := licenceTestSecret(t)
		server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			if route == keyservice.RouteStatus {
				return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
			}
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK, Key: next, RenewalSecret: stranger})
		})
		wrote := secretWrites(f)

		out, errOut, err := f.renewJob(t)
		if err == nil {
			t.Fatal("a renewal whose key names a secret this installation does not hold was written")
		}
		if code := fcli.ExitCode(err); code != fcli.ExitRemoteError {
			t.Errorf("the refusal exited %d, want %d: %v", code, fcli.ExitRemoteError, err)
		}
		if got := len(server.callsTo(keyservice.RouteRenew)); got != 1 {
			t.Errorf("the job asked /renew %d times, want once: the answer is what the guard refused", got)
		}
		if got := secretWrites(f); got != wrote {
			t.Errorf("the container was given %d further versions, want none: the key they would renew with names a secret the installation does not hold", got-wrote)
		}
		obj := f.object(t)
		if len(obj.Keys) != 1 || obj.Keys[0] != key {
			t.Errorf("the project holds %d keys after an answer that is not the pair, want the one it had", len(obj.Keys))
		}
		if got := string(f.secrets.Latest(f.secretContainer())); got != secret {
			t.Error("the container's newest version is not the secret the installed key renews with")
		}
		if !strings.Contains(err.Error(), "digest of another secret") {
			t.Errorf("the refusal %q does not say what is wrong with the pair", err.Error())
		}
		msg := out + errOut + err.Error()
		assertNoKeyMaterial(t, "the job's refusal", msg, key, secret)
		assertNoKeyMaterial(t, "the job's refusal", msg, next, stranger)
	})

	t.Run("a key the project would not apply", func(t *testing.T) {
		// T26: the project keeps the later term, so an answer whose term ends
		// before the applied key's is written to the object and never used —
		// and it would carry the renewal secret with it.
		f := newLicenceFixture(t)
		key, _, _ := f.installed(t, shortTerm(f))
		stale, staleSecret, _ := f.bundle(t, func(c *licence.Claims) {
			c.KeyID = "lk_test_k41_0003"
			c.PaidThrough = f.now.Add(5 * 24 * time.Hour)
		})
		server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			if route == keyservice.RouteStatus {
				return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
			}
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK, Key: stale, RenewalSecret: staleSecret})
		})
		wrote := secretWrites(f)

		out, errOut, err := f.renewJob(t)
		if err == nil {
			t.Fatal("a renewal the project would not apply was written")
		}
		if code := fcli.ExitCode(err); code != fcli.ExitRemoteError {
			t.Errorf("the refusal exited %d, want %d: %v", code, fcli.ExitRemoteError, err)
		}
		if got := len(server.callsTo(keyservice.RouteRenew)); got != 1 {
			t.Errorf("the job asked /renew %d times, want once: the answer is what the guard refused", got)
		}
		if got := secretWrites(f); got != wrote {
			t.Errorf("the container was given %d further versions, want none: the key they would renew with is not the key this installation runs on", got-wrote)
		}
		obj := f.object(t)
		if len(obj.Keys) != 1 || obj.Keys[0] != key {
			t.Errorf("the project holds %d keys after an answer it would not apply, want the one it had", len(obj.Keys))
		}
		if !strings.Contains(err.Error(), "keeps the later term") {
			t.Errorf("the refusal %q does not say why the answer was refused", err.Error())
		}
		assertNoKeyMaterial(t, "the job's refusal", out+errOut+err.Error(), key, staleSecret)
	})

	t.Run("a key the installation has recorded revoked", func(t *testing.T) {
		// The revoked list is the installation's own record of what the server
		// answered once, and it is not the check's: a key on it stays revoked
		// however many checks follow (D2140, spec section 8). A server that
		// answered a key on that list would be handing back a key this project
		// reads as Free, so the answer is refused before either write rather
		// than landing a downgrade as a renewal.
		f := newLicenceFixture(t)
		key, secret, claims := f.installed(t, shortTerm(f))
		next, nextSecret, nextClaims := f.successor(t, claims)
		if err := f.store(t).RecordCheck(context.Background(), f.now, project.CheckRevoked, nextClaims.KeyID); err != nil {
			t.Fatal(err)
		}
		server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
			if route == keyservice.RouteStatus {
				return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
			}
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK, Key: next, RenewalSecret: nextSecret})
		})
		wrote := secretWrites(f)

		out, errOut, err := f.renewJob(t)
		if err == nil {
			t.Fatal("a key this installation has recorded revoked was written as a renewal")
		}
		if code := fcli.ExitCode(err); code != fcli.ExitRemoteError {
			t.Errorf("the refusal exited %d, want %d: %v", code, fcli.ExitRemoteError, err)
		}
		if got := len(server.callsTo(keyservice.RouteRenew)); got != 1 {
			t.Errorf("the job asked /renew %d times, want once: the answer is what the guard refused", got)
		}
		if got := secretWrites(f); got != wrote {
			t.Errorf("the container was given %d further versions, want none: the key they would renew with is the key this installation has revoked", got-wrote)
		}
		obj := f.object(t)
		if len(obj.Keys) != 1 || obj.Keys[0] != key {
			t.Errorf("the project holds %d keys after an answer naming a revoked key, want the one it had", len(obj.Keys))
		}
		if got := string(f.secrets.Latest(f.secretContainer())); got != secret {
			t.Error("the container's newest version is not the secret the installed key renews with")
		}
		if !strings.Contains(err.Error(), "already recorded that key as revoked") {
			t.Errorf("the refusal %q does not say why the answer was refused", err.Error())
		}
		assertNoKeyMaterial(t, "the job's refusal", out+errOut+err.Error(), key, secret)
		assertNoKeyMaterial(t, "the job's refusal", out+errOut+err.Error(), next, nextSecret)
	})
}

// TestAFailedSecretWriteRenewsNothing holds the order of the two writes: the
// secret first, the key second. The other order leaves a key whose
// renewal_secret_hash names a secret the container does not hold — an
// installation that cannot renew and cannot repair itself, which is exactly
// what the order here avoids.
func TestAFailedSecretWriteRenewsNothing(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, claims := f.installed(t, shortTerm(f))
	next, nextSecret, _ := f.successor(t, claims)
	server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
		if route == keyservice.RouteStatus {
			return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
		}
		return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK, Key: next, RenewalSecret: nextSecret})
	})
	f.secrets.FailAddVersion = "the container refused the new version"

	out, errOut, err := f.renewJob(t)
	if err == nil {
		t.Fatal("a renewal whose secret could not be written succeeded")
	}
	if code := fcli.ExitCode(err); code != fcli.ExitRemoteError {
		t.Errorf("the refusal exited %d, want %d: %v", code, fcli.ExitRemoteError, err)
	}
	if len(server.callsTo(keyservice.RouteRenew)) != 1 {
		t.Fatalf("the job asked /renew %d times, want once", len(server.callsTo(keyservice.RouteRenew)))
	}
	obj := f.object(t)
	if len(obj.Keys) != 1 || obj.Keys[0] != key {
		t.Errorf("the project holds %d keys after a renewal whose secret was refused, want the one it had: a key written before its secret names a secret the container does not hold", len(obj.Keys))
	}
	if got := string(f.secrets.Latest(f.secretContainer())); got != secret {
		t.Error("the container's newest version is not the secret the installed key renews with")
	}
	if obj.Check == nil || obj.Check.KeyID != claims.KeyID {
		t.Errorf("the check record is %+v, want the check this run made", obj.Check)
	}
	assertNoKeyMaterial(t, "the job's refusal", out+errOut+err.Error(), key, nextSecret)
}

// TestLicenceRenewJobRefusesARedirect holds the job's own client rule on the
// status route, the twin of the renewal route's
// (TestLicenceRefreshRefusesARedirect): a 308 from the licence server's address
// is the answer and is not followed. The check carries the renewal secret too,
// and its answer is the one written into the project's object, so a followed
// redirect would both hand the secret to whatever host the Location names and
// let a `revoked` there make the installation Free for good.
func TestLicenceRenewJobRefusesARedirect(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, claims := f.installed(t, shortTerm(f))

	// Where the redirect points, and the one answer that must never be read.
	elsewhere := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
		return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusRevoked})
	})
	server := f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
		return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
	})
	// Another host over plain http: a redirect changes the scheme as readily as
	// the address, and both are what the client refuses to take an answer from.
	server.redirect(elsewhere.ts.URL + keyservice.RouteStatus)

	out, errOut, err := f.renewJob(t)
	if err != nil {
		t.Fatalf("a redirect failed the job: a check that reached nobody is a line and a record rather than a failure: %v", err)
	}
	if got := elsewhere.count(); got != 0 {
		t.Errorf("the redirect delivered the check %d time(s) to the host it named: the renewal secret goes to the licence server's address and nowhere else", got)
	}
	if len(server.callsTo(keyservice.RouteRenew)) != 0 {
		t.Error("the job renewed from an answer that is not the licence server's")
	}
	obj := f.object(t)
	if len(obj.Revoked) != 0 {
		t.Errorf("a redirect recorded %v as revoked: what the licence server did not answer is not written", obj.Revoked)
	}
	if obj.Check == nil || obj.Check.Status != project.CheckUnreachable || obj.Check.KeyID != claims.KeyID {
		t.Errorf("the check record is %+v, want the redirect recorded as the check that reached nobody", obj.Check)
	}
	if len(obj.Keys) != 1 || obj.Keys[0] != key {
		t.Errorf("a redirect left %d keys in the project, want the one it had", len(obj.Keys))
	}
	if got := f.status(t); got["state"] != "active" || got["revoked"] == true {
		t.Errorf("the installation reads %v after a redirect, want the key's own active term", got)
	}
	assertNoKeyMaterial(t, "the job's answer", out+errOut, key, secret)
}

// TestARefusedVersionListNamesItsOwnPermission holds the third grant apart from
// the two a payload read needs: listing the container's versions is its own
// call, and an operator sent to grant secretmanager.secrets.get and
// secretmanager.versions.access is refused here again. The refusal names the
// permission whose absence it is.
func TestARefusedVersionListNamesItsOwnPermission(t *testing.T) {
	f := newLicenceFixture(t)
	key, secret, _ := f.installed(t, shortTerm(f))
	f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
		return licenceAnswerJSON(t, keyservice.Answer{Status: keyservice.StatusOK})
	})
	// The fake refuses every version list after the first n, so a run meets the
	// refusal only once one list has been served: this run serves it, and the
	// next meets the refusal.
	if _, _, err := f.renewJob(t); err != nil {
		t.Fatalf("the run before the refused list = %v", err)
	}
	f.secrets.FailVersionsAfter = f.secrets.VersionsListCalls()
	f.serveJobServer(t, func(route string, call keyservice.RenewalCall) (int, string) {
		t.Errorf("the job called %s although it could not list the container's versions", route)
		return http.StatusOK, `{"status":"ok"}`
	})

	out, errOut, err := f.renewJob(t)
	if err == nil {
		t.Fatal("a job that could not list the container's versions renewed anyway")
	}
	if code := fcli.ExitCode(err); code != fcli.ExitUserError {
		t.Errorf("the refusal exited %d, want %d: %v", code, fcli.ExitUserError, err)
	}
	if !strings.Contains(err.Error(), "secretmanager.versions.list") {
		t.Errorf("the refusal %q does not name the permission its own call needs", err.Error())
	}
	for _, unused := range []string{"secretmanager.secrets.get", "secretmanager.versions.access"} {
		if strings.Contains(err.Error(), unused) {
			t.Errorf("the refusal %q names %s, which the list it refused does not use: an operator who grants those is refused again", err.Error(), unused)
		}
	}
	assertNoKeyMaterial(t, "the job's answer", out+errOut+err.Error(), key, secret)
}
