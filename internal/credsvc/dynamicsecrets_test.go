package credsvc

// DS3.3's tests: the per-secret read, R6's order against the bundle, and the
// two rules that make the order safe — the absence that falls to the bundle
// and the failure that does not (R7, D143).
//
// The calls are driven through Server.forward rather than over HTTP, because
// the candidate list is where a per-secret name enters this service and that
// list is the slip's own (DS1.6): nothing on this branch builds one, so a test
// that went through the messages route could reach no per-secret container at
// all. Everything after the list is the service's own — the same selection,
// the same send, the same refusal, the same audit line — and perSecretSvc
// hands forward exactly what the route hands it.
//
// Each test names the edit that turns it red, as the rest of this package
// does; TestPerSecretBeforeBundle's is the plan's own red step (the bundle
// consulted first).

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/secretname"
	"google.golang.org/api/option"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

// dynReader is the fixture's Secret Manager: the bundle document the service's
// own fixture holds, and the per-secret containers beside it. One type holds
// both because the deployment's does — the service reads both stores through
// the one client and project — and because perSecrets derives the per-secret
// reader from the bundle's own source rather than from a field of its own
// (dynamicsecrets.go).
type dynReader struct {
	doc    []byte
	values map[string]string
	// err is what Value answers for every name while it is set, so a read that
	// failed needs no cloud and no transport.
	err error
	// reads is every secret ID this fixture was asked for, in order, which is
	// what makes "read once, by the candidate's own name" a fact and not an
	// inference.
	reads []string
}

func (d *dynReader) Latest(context.Context) ([]byte, error) { return d.doc, nil }

func (d *dynReader) Value(_ context.Context, secretID string) (string, bool, error) {
	d.reads = append(d.reads, secretID)
	if d.err != nil {
		return "", false, d.err
	}
	v, ok := d.values[secretID]
	return v, ok, nil
}

// docOnly is a source that reads no containers: it is a BundleSource and not a
// PerSecretSource, which is every installation whose bundle is not read through
// Secret Manager's.
type docOnly struct{ doc []byte }

func (d docOnly) Latest(context.Context) ([]byte, error) { return d.doc, nil }

// perSecretSvc is the fixture the four tests read: auditSvc's service, so the
// audit lines land in a buffer the test holds, with the bundle's source stood
// in for by one that answers containers too.
type perSecretSvc struct {
	*svc
	dyn      *dynReader
	loader   *BundleLoader
	auditLog *logBuf
}

func newPerSecretSvc(t *testing.T) *perSecretSvc {
	t.Helper()
	f := &perSecretSvc{
		dyn:    &dynReader{doc: testBundleDoc(t), values: map[string]string{}},
		loader: &BundleLoader{},
	}
	f.loader.Source = f.dyn
	f.svc, f.auditLog = auditSvc(t, func(_ *svc, cfg *Config) { cfg.Bundles = f.loader })
	return f
}

// serve drives one Messages call whose candidate list is exactly cands, the
// way the messages route does: the same forward, with the selector the handler
// builds for it and the identity it verified.
func (f *perSecretSvc) serve(t *testing.T, cands ...creds.Credential) (*httptest.ResponseRecorder, *Bundle) {
	t.Helper()
	ctx := context.Background()
	b, err := f.loader.Load(ctx)
	if err != nil {
		t.Fatalf("the bundle was not read: %v", err)
	}
	rec := httptest.NewRecorder()
	sel := &creds.Selector{
		Store:    f.state,
		Settings: settingsOf(f.policy.shared),
		Now:      func() time.Time { return f.now },
	}
	f.server.forward(ctx, rec, providerCall(t, MessagesPath), sel,
		Identity{Slug: f.slug, Run: f.run, Job: f.job, JobAccount: testJobSA, JobClass: ClassRepository},
		b, cands, nil)
	return rec, b
}

// entryValue is the fixture's bundle entry for a credential NAME, which is
// what the bundle half of R6's order answers with.
func entryValue(t *testing.T, b *Bundle, name string) string {
	t.Helper()
	e, ok := b.Lookup(name)
	if !ok {
		t.Fatalf("the fixture's bundle holds no entry for %q: the test cannot say what the bundle would have served", name)
	}
	return e.Value()
}

// authOf is the credential the upstream was handed, which is the one fact that
// says which store served the call.
func authOf(t *testing.T, s *perSecretSvc) string {
	t.Helper()
	if n := s.up.Count(); n != 1 {
		t.Fatalf("the upstream saw %d requests, want exactly 1", n)
	}
	return s.up.Seen()[0].Header.Get("Authorization")
}

// TestPerSecretBeforeBundle is R6's order: the container the credential's
// rotation wrote is read and served AHEAD of the bundle's entry, so a rotated
// credential is the one a call carries. The two stores hold the same
// credential here — the name is the grammar's per-user spelling of the account
// the fixture's bundle also holds — so the answer says which of them served it.
//
// Mutation (run, restore): ask the bundle first, for the credential NAME the
// per-secret name holds — in cascade.go's resolveEntry, ahead of the per-secret
// read:
//
//	if p, err := secretname.Parse(c.Name); err == nil {
//		if e, ok := entryFor(b, creds.Credential{Name: p.Name, Tier: c.Tier}); ok {
//			return e, true, nil
//		}
//	}
//
// (cascade.go needs secretname imported for that line.) This test is red: the
// call carries the bundle's value for a credential a container was written for,
// and no container is read at all.
//
// Hoisting the entryFor call that is already there, unchanged, is NOT this
// mutation and leaves this test GREEN — checked, because it is the edit the
// sentence "ask the bundle first" first suggests. It asks the bundle for the
// candidate's OWN name (fugaro_u_<id>_<name>), which no entry is under: byName
// is keyed by the name the cascade speaks (bundle.go), so it finds nothing, and
// the per-secret read below it happens anyway. The order this test pins is over
// the name the grammar HOLDS, and a mutation that does not ask for that name
// does not exercise it.
func TestPerSecretBeforeBundle(t *testing.T) {
	t.Run("the container serves the call", func(t *testing.T) {
		s := newPerSecretSvc(t)
		name := mustUserName(t, testLauncherSub, testAccount)
		rotated := fakeCred(t, "rotated")
		s.dyn.values[name] = rotated

		rec, b := s.serve(t, creds.Credential{Name: name, Tier: creds.TierA})
		if rec.Code != http.StatusOK {
			t.Fatalf("the answer is %d, want the upstream's 200: %s", rec.Code, rec.Body)
		}
		if held := entryValue(t, b, testAccount); held == rotated {
			t.Fatal("the fixture's two stores hold the same value, so nothing here says which one served the call")
		}
		if got, want := authOf(t, s), "Bearer "+rotated; got != want {
			t.Errorf("the call carried %q, want %q: the per-secret container is read before the bundle (R6)", got, want)
		}
		if got := rec.Header().Get(HeaderCredential); got != name {
			t.Errorf("%s = %q, want %q: the answer names the credential that served it", HeaderCredential, got, name)
		}
		if reads := s.dyn.reads; len(reads) != 1 || reads[0] != name {
			t.Errorf("the service read %v, want exactly [%s]: the read is by the candidate's own name, once", reads, name)
		}
	})

	// The project's prefix is read the same way: R1's grammar has two prefixes
	// and this file reads one rule, so a shared container serves its call
	// exactly as a person's does.
	t.Run("a project-shared container serves the call", func(t *testing.T) {
		s := newPerSecretSvc(t)
		shared, err := secretname.Project(config.DefaultClaudeAccount)
		if err != nil {
			t.Fatalf("the grammar refuses a project-shared name: %v", err)
		}
		rotated := fakeCred(t, "rotated-shared")
		s.dyn.values[shared] = rotated

		rec, _ := s.serve(t, creds.Credential{Name: shared, Tier: creds.TierA})
		if rec.Code != http.StatusOK {
			t.Fatalf("the answer is %d, want the upstream's 200: %s", rec.Code, rec.Body)
		}
		if got, want := authOf(t, s), "Bearer "+rotated; got != want {
			t.Errorf("the call carried %q, want %q: the project's prefix is not a second rule", got, want)
		}
	})

	// A name the grammar does not admit is not a container and is not read as
	// one: the bundle's own credential — here the account the cascade speaks,
	// which the document spells claude-oauth-token-<name> — is served exactly
	// as it was before this service read containers at all (G7).
	t.Run("a name the grammar does not admit is not read", func(t *testing.T) {
		s := newPerSecretSvc(t)
		rec, b := s.serve(t, creds.Credential{Name: testAccount, Tier: creds.TierA})
		if rec.Code != http.StatusOK {
			t.Fatalf("the answer is %d, want the upstream's 200: %s", rec.Code, rec.Body)
		}
		if got, want := authOf(t, s), "Bearer "+entryValue(t, b, testAccount); got != want {
			t.Errorf("the call carried %q, want the bundle's %q", got, want)
		}
		if reads := s.dyn.reads; len(reads) != 0 {
			t.Errorf("the service read %v, want none: a name outside the grammar is no container", reads)
		}
	})
}

// TestPerSecretCandidateIsProbed is R6's order on the probe's own path (C1.12):
// a backed-off per-secret candidate is asked whether it works before real work
// is routed to it, exactly as a bundle one is, and it is asked WITH the
// container's value — the credential the call itself would be served by.
//
// The probe resolves the candidate the way the call does, and that is the whole
// of this test: a probe that looked the candidate up in the bundle under its own
// name would find nothing there (no bundle entry is named by the grammar), so it
// would not be made at all. The mark would then never move, and the run would be
// answered the floor — fifteen seconds ahead of itself — on every attempt, which
// is the loop waitFloor exists to bound and not to cause: a rotated credential
// whose wait had passed would never be asked and its run would burn its budget
// waiting for a probe nobody makes.
//
// Mutation (run, restore): resolve the probe's credential with b.Lookup(c.Name)
// instead of s.resolveEntry (probe.go's probeDue). This test is red: the call is
// answered the typed wait and the upstream sees no probe.
func TestPerSecretCandidateIsProbed(t *testing.T) {
	s := newPerSecretSvc(t)
	// D142's window control written off, so what this reads is the probe's own
	// effect on the mark rather than the window its success opens (probeSvc's
	// fixture writes the same line off for the same reason): with the control on,
	// a probe that works leaves the credential held until the window's end, and
	// the call this test wants served would be refused — correctly, and for a
	// reason that is not the probe's being made.
	s.policy.shared = decodeTestPolicy(t, testPolicyDocControls("oauth-only", false, 0))
	name := mustUserName(t, testLauncherSub, testAccount)
	rotated := fakeCred(t, "rotated")
	s.dyn.values[name] = rotated
	// The state a probe is owed for: a backed-off mark whose own wait has passed.
	s.backedOff(name, s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{
		{Status: http.StatusOK, Body: `{"type":"message","probe_answer":"probe-answer"}`},
		anthropicfake.MessageOK(testModel, testUsage),
	}

	rec, _ := s.serve(t, creds.Credential{Name: name, Tier: creds.TierA})
	if rec.Code != http.StatusOK {
		t.Fatalf("the answer is %d, want the upstream's 200: a probe that is not made leaves the run waiting for one (%s)", rec.Code, rec.Body)
	}
	if got := s.up.Count(); got != 2 {
		t.Fatalf("the upstream saw %d requests, want the probe and then the run's call", got)
	}
	seen := s.up.Seen()
	if got, want := seen[0].Header.Get("Authorization"), "Bearer "+rotated; got != want {
		t.Errorf("the probe carried %q, want the container's %q: the probe is made with the credential the call is served by (R6)", got, want)
	}
	if got, want := seen[1].Header.Get("Authorization"), "Bearer "+rotated; got != want {
		t.Errorf("the call carried %q, want %q", got, want)
	}
	if m, _ := s.state.mark(name); m.State != creds.StateAvailable {
		t.Errorf("the mark is %q after a probe that worked, want %q: a mark that does not move is a probe that was not made",
			m.State, creds.StateAvailable)
	}
	line, ok := probeLine(t, s.auditLog)
	if !ok {
		t.Fatal("a probe wrote no audit line")
	}
	checkAuditShape(t, line)
	for field, want := range map[string]any{
		"purpose":    PurposeProbe,
		"credential": name,
		"tier":       TierA,
		"status":     StatusOK,
	} {
		if got := line[field]; got != want {
			t.Errorf("the probe's audit line %s = %v, want %q", field, got, want)
		}
	}
}

// TestBundleEntryStillServes is R7's fall, and it is what makes a rotation safe
// to do at all: a per-secret name whose container is not there — nothing
// written under it, or a version not yet written — is served by the bundle's
// entry for the credential the name HOLDS, which is the value the installation
// ran on before the rotation. A slip-less older CLI needs no mechanism of its
// own for it (D1945).
//
// Mutation (run, restore): answer a container that is not there with a refusal
// instead of the fall (dynamicsecrets.go's read, the `if !found` branch). This
// test is red.
func TestBundleEntryStillServes(t *testing.T) {
	t.Run("nothing is written under the name", func(t *testing.T) {
		s := newPerSecretSvc(t)
		name := mustUserName(t, testLauncherSub, testAccount)
		rec, b := s.serve(t, creds.Credential{Name: name, Tier: creds.TierA})
		if rec.Code != http.StatusOK {
			t.Fatalf("the answer is %d, want the upstream's 200: %s", rec.Code, rec.Body)
		}
		if got, want := authOf(t, s), "Bearer "+entryValue(t, b, testAccount); got != want {
			t.Errorf("the call carried %q, want the bundle's %q: a container that is not there falls to the bundle (R7)", got, want)
		}
		if got := rec.Header().Get(HeaderCredential); got != name {
			t.Errorf("%s = %q, want %q: the answer still names what the run asked for", HeaderCredential, got, name)
		}
		if reads := s.dyn.reads; len(reads) != 1 || reads[0] != name {
			t.Errorf("the service read %v, want exactly [%s]: the fall is a read that found nothing", reads, name)
		}
	})

	t.Run("a project-shared name falls the same way", func(t *testing.T) {
		s := newPerSecretSvc(t)
		shared, err := secretname.Project(config.DefaultClaudeAccount)
		if err != nil {
			t.Fatalf("the grammar refuses a project-shared name: %v", err)
		}
		rec, b := s.serve(t, creds.Credential{Name: shared, Tier: creds.TierA})
		if rec.Code != http.StatusOK {
			t.Fatalf("the answer is %d, want the upstream's 200: %s", rec.Code, rec.Body)
		}
		if got, want := authOf(t, s), "Bearer "+entryValue(t, b, config.DefaultClaudeAccount); got != want {
			t.Errorf("the call carried %q, want the bundle's %q: the name the grammar holds is what the bundle is asked for", got, want)
		}
	})

	// An installation whose bundle source is not Secret Manager's reads no
	// container at all, and that is an ordinary answer: a per-secret-named
	// candidate is served by the bundle's entry for the credential it holds,
	// exactly as it was before this file existed (G7). The reader is derived
	// from the source the bundle is read through, so a source that reads no
	// containers has no per-secret reader to fall from — and the name it falls
	// BY is still the grammar's reading of the candidate and not the
	// candidate's own (dynamicsecrets.go's read).
	t.Run("an installation with no per-secret reader falls too", func(t *testing.T) {
		s := newPerSecretSvc(t)
		name := mustUserName(t, testLauncherSub, testAccount)
		s.loader.Source = docOnly{doc: testBundleDoc(t)}
		if got := s.server.perSecrets(); got != nil {
			t.Fatalf("perSecrets answered %#v for a source that reads no containers, want none", got)
		}

		rec, b := s.serve(t, creds.Credential{Name: name, Tier: creds.TierA})
		if rec.Code != http.StatusOK {
			t.Fatalf("the answer is %d, want the upstream's 200: %s", rec.Code, rec.Body)
		}
		if got, want := authOf(t, s), "Bearer "+entryValue(t, b, testAccount); got != want {
			t.Errorf("the call carried %q, want the bundle's %q", got, want)
		}
	})
}

// TestFailedReadFailsClosed is D143's rule at this read: a container that could
// not be READ is neither an absence nor a fallback. The service refuses the
// call — the class, one audit line, and the upstream untouched — rather than
// serving the bundle's entry, which for a rotated credential is exactly the
// value an operator rotated away. That fallback would be silent and it would
// happen for every run at once, which is why the tempting "the bundle is
// better than nothing" reading is what this test forbids.
//
// Mutation (run, restore): fall to the bundle on a failure (dynamicsecrets.go's
// read, the `err != nil` branch answering the parsed name instead of the
// error). This test is red: the call is served by the value the rotation
// retired.
func TestFailedReadFailsClosed(t *testing.T) {
	t.Run("a failed read refuses the call", func(t *testing.T) {
		s := newPerSecretSvc(t)
		name := mustUserName(t, testLauncherSub, testAccount)
		s.dyn.err = errors.New("the container could not be read")

		rec, _ := s.serve(t, creds.Credential{Name: name, Tier: creds.TierA})
		if rec.Code != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d: a read that failed refuses the call (%s)",
				rec.Code, StatusUnavailable, rec.Body)
		}
		e := refusal(t, rec.Code, rec.Body.Bytes())
		if e.Error != ErrorStateUnavailable {
			t.Errorf("the refusal's class is %q, want %q", e.Error, ErrorStateUnavailable)
		}
		if e.Message != perSecretRefusal {
			t.Errorf("the refusal is %q, want the read's own class %q", e.Message, perSecretRefusal)
		}
		if n := s.up.Count(); n != 0 {
			t.Fatalf("the upstream saw %d requests, want none: a failed read never falls back to the bundle's value", n)
		}
		lines := forwardLines(t, s.auditLog)
		if len(lines) != 1 {
			t.Fatalf("a refused call wrote %d forward lines, want exactly 1: %v", len(lines), lines)
		}
		checkAuditShape(t, lines[0])
		for field, want := range map[string]any{
			"run":        s.run,
			"repository": s.slug,
			"job":        s.job,
			"caller":     testJobSA,
			"credential": name,
			"tier":       TierA,
			"purpose":    PurposeForward,
			"status":     ErrorStateUnavailable,
		} {
			if got := lines[0][field]; got != want {
				t.Errorf("the refusal's audit line %s = %v, want %q", field, got, want)
			}
		}
	})

	// A read that cannot be made at all is a failure and not an absence: a
	// reader with no Secret Manager client refuses rather than answering an
	// empty value, because an answer here is a credential this service would
	// attach to a call.
	t.Run("a reader with no client is a failure", func(t *testing.T) {
		name := mustUserName(t, testLauncherSub, testAccount)
		value, found, err := SecretManagerPerSecrets{}.Value(context.Background(), name)
		if err == nil || found {
			t.Fatalf("a reader with no Secret Manager client answered (%q, %v), want a failure", value, found)
		}
		if !errors.Is(err, ErrPerSecretUnreadable) {
			t.Errorf("the failure is %v, want one of %v", err, ErrPerSecretUnreadable)
		}
		if value != "" {
			t.Error("the failure carries a value")
		}
	})
}

// TestNoValueInErrors is the property every path of this tree owes: a value of
// either store reaches no error, no audit line and no log line, whatever went
// wrong. The interesting value here is one a read FAILED on — that is the case
// that brings a store's own error text into the service's sentences — and the
// two stores' values sit beside it, so a sentence quoting the wrong one is
// caught too.
//
// The audit's own scrub (NewAuditor, armed from the bundle's loader) knows the
// DOCUMENT's values and cannot know a container's, so a value of the second
// store that reached a field would reach the line unredacted. That is why
// nothing here may put one in a field at all, and why the answer's own
// redaction — which is armed with the value the upstream was handed, whichever
// store it came from — is the only path a value is allowed to travel.
//
// Mutation (run, restore): carry the read's own error into the refusal
// (cascade.go's resolveEntry call site, perSecretRefusal → err.Error()). This
// test is red.
func TestNoValueInErrors(t *testing.T) {
	s := newPerSecretSvc(t)
	name := mustUserName(t, testLauncherSub, testAccount)
	rotated := fakeCred(t, "per-secret")
	s.dyn.values[name] = rotated
	values := append(fixtureValues(t), rotated)

	// noValueAnywhere is the whole assertion: the value is in none of the
	// places a run, an operator or the upstream's own text could carry it.
	noValueAnywhere := func(t *testing.T, s *perSecretSvc, where string, rec *httptest.ResponseRecorder) {
		t.Helper()
		for _, v := range values {
			if strings.Contains(rec.Body.String(), v) {
				t.Errorf("the %s carries the credential:\n%s", where, rec.Body)
			}
			for k, vs := range rec.Header() {
				for _, got := range vs {
					if strings.Contains(got, v) {
						t.Errorf("the %s's %s header carries the credential", where, k)
					}
				}
			}
			if strings.Contains(s.auditLog.String(), v) {
				t.Errorf("the audit lines carry the credential:\n%s", s.auditLog)
			}
			if strings.Contains(s.logs.String(), v) {
				t.Errorf("the service's own log carries the credential:\n%s", s.logs)
			}
		}
	}

	t.Run("a served call", func(t *testing.T) {
		// The upstream echoes the credential it was handed, which is the case
		// the answer's redaction exists for: it is armed with the value this
		// call was served by, and a per-secret value is one of them.
		s.up.Script = []anthropicfake.Reply{{
			Status: http.StatusOK,
			Body:   `{"type":"error","error":{"type":"api_error","message":"moved to ` + rotated + `"}}`,
		}}
		rec, _ := s.serve(t, creds.Credential{Name: name, Tier: creds.TierA})
		if rec.Code != http.StatusOK {
			t.Fatalf("the answer is %d, want the upstream's 200: %s", rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), redactedValue) {
			t.Errorf("the answer carries neither the credential nor where it stood:\n%s", rec.Body)
		}
		noValueAnywhere(t, s, "answer", rec)
		if got := rec.Header().Get(HeaderCredential); got != name {
			t.Errorf("%s = %q, want the credential's NAME %q: the answer names it and never serves it", HeaderCredential, got, name)
		}
	})

	t.Run("a failed read whose error carries the value", func(t *testing.T) {
		s := newPerSecretSvc(t)
		s.dyn.err = errors.New("the container holds " + rotated + " and could not be decoded")

		rec, _ := s.serve(t, creds.Credential{Name: name, Tier: creds.TierA})
		if rec.Code != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d: %s", rec.Code, StatusUnavailable, rec.Body)
		}
		noValueAnywhere(t, s, "refusal", rec)
		if e := refusal(t, rec.Code, rec.Body.Bytes()); e.Message != perSecretRefusal {
			t.Errorf("the refusal is %q, want the read's own class", e.Message)
		}
	})
}

// TestPerSecretValueClassifiesEveryAnswer is the deployment's own reader, and
// the only test here that is not a fixture of its own: the other tests hand the
// service a PerSecretSource and so say nothing about how
// SecretManagerPerSecrets turns an answer from the API into one of R6's three
// outcomes. That classification is the whole of the read's fail-closed rule — a
// container that is not there falls to the bundle, and EVERY other answer
// refuses the call — so a regression that answered an absence for a 403, for a
// 500 or for a version with no payload would quietly serve, for every run of the
// installation at once, the bundle's entry for a credential an operator had
// rotated away (D143).
//
// The client is the deployment's own: the real generated Secret Manager client,
// pointed at a loopback server by the options the deployment's other clients use
// (internal/cli/managed_test.go's shape), so the request the test reads is the
// request the service would make — the exact :access path of the container the
// grammar named. Everything is hermetic: no account, no ADC, no cloud.
//
// Mutation (run, restore): answer an absence for the 403 row — dynamicsecrets.go
// Value's `errors.As(err, &ge) && ge.Code == http.StatusForbidden` case returning
// "", false, nil. This test is red. The same mutation on the 500 row or on the
// empty-payload branch is red the same way, and no other test in this package
// notices either.
func TestPerSecretValueClassifiesEveryAnswer(t *testing.T) {
	const project = "fugaro-creds-test"
	name := mustUserName(t, testLauncherSub, testAccount)
	value := fakeCred(t, "container")
	// The request the API client must build from the secret ID the grammar
	// spelled: one container, named exactly, at its latest version, read the way
	// the API reads one (a GET of the :access method — the generated client
	// chooses it for this call, and nothing here sets a body).
	accessPath := "/v1/projects/" + project + "/secrets/" + name + "/versions/latest:access"

	// payload is the API's own answer shape, with the value base64 in it.
	payload := func(data string) string { return `{"payload":{"data":"` + data + `"}}` }
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	apiError := func(code int, status string) string {
		return `{"error":{"code":` + strconv.Itoa(code) + `,"message":"the API's own sentence","status":"` + status + `"}}`
	}
	answer := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
	}

	for _, tc := range []struct {
		name string
		// serve is what the container's endpoint answers.
		serve http.HandlerFunc
		// wantValue and wantFound are the read's answer; wantErr is the class it
		// must fail with, and nil is R6's fall to the bundle (the 404 row).
		wantValue string
		wantFound bool
		wantErr   error
	}{{
		name:      "the container answers",
		serve:     answer(http.StatusOK, payload(b64(value))),
		wantValue: value,
		wantFound: true,
	}, {
		// The absence R7 falls from, and the ordinary case: nothing has been
		// rotated under this name. It is NOT a failure — the bundle's entry for
		// the credential the name holds is what serves the call.
		name:  "the container is not there",
		serve: answer(http.StatusNotFound, apiError(http.StatusNotFound, "NOT_FOUND")),
	}, {
		name:    "the service is not allowed to read it",
		serve:   answer(http.StatusForbidden, apiError(http.StatusForbidden, "PERMISSION_DENIED")),
		wantErr: ErrPerSecretUnreadable,
	}, {
		name:    "the API failed",
		serve:   answer(http.StatusInternalServerError, apiError(http.StatusInternalServerError, "INTERNAL")),
		wantErr: ErrPerSecretUnreadable,
	}, {
		// A transport that dies mid-request is not an absence either: nothing was
		// established about the container, so nothing may be served from it.
		name: "the connection dies",
		serve: func(w http.ResponseWriter, _ *http.Request) {
			h, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("the fixture could not take the connection: %v", err)
				return
			}
			_ = h.Close()
		},
		wantErr: ErrPerSecretUnreadable,
	}, {
		// A version with no value is not a credential, and it must not be read as
		// an absence: an absence would serve the bundle's older value in its place.
		name:    "the version has no payload",
		serve:   answer(http.StatusOK, `{}`),
		wantErr: ErrPerSecretUnreadable,
	}, {
		name:    "the payload is not base64",
		serve:   answer(http.StatusOK, payload("this is not base64 at all !!")),
		wantErr: ErrPerSecretUnreadable,
	}, {
		name:    "the value is larger than the bound",
		serve:   answer(http.StatusOK, payload(b64(value+strings.Repeat("x", MaxBundleValueBytes)))),
		wantErr: ErrPerSecretUnreadable,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			// The handler runs on the server's goroutine and a dropped connection
			// lets the client return before the handler does, so what it records
			// is read under the same lock.
			var mu sync.Mutex
			var method, path string
			reads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				method, path, reads = r.Method, r.URL.Path, reads+1
				mu.Unlock()
				tc.serve(w, r)
			}))
			t.Cleanup(srv.Close)
			svc, err := secretmanager.NewService(context.Background(),
				option.WithoutAuthentication(), option.WithEndpoint(srv.URL+"/"))
			if err != nil {
				t.Fatalf("the fixture's Secret Manager client could not be built: %v", err)
			}

			got, found, err := SecretManagerPerSecrets{Project: project, Service: svc}.Value(context.Background(), name)

			mu.Lock()
			defer mu.Unlock()
			if reads != 1 {
				t.Fatalf("the container was asked for %d times, want once", reads)
			}
			if method != http.MethodGet || path != accessPath {
				t.Errorf("the read asked for %s %s, want GET %s: the container is read by the exact name the grammar spelled, at its latest version",
					method, path, accessPath)
			}
			if got != tc.wantValue || found != tc.wantFound {
				t.Errorf("the read answered (%q, %v), want (%q, %v)", got, found, tc.wantValue, tc.wantFound)
			}
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("the read failed: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("the failure is %v, want one of %v", err, tc.wantErr)
			}
			if found {
				t.Error("a failed read answered found=true")
			}
			// The class and nothing else: no value of either spelling — the
			// credential itself, and the base64 the API carries it in — reaches a
			// sentence a run or an operator reads.
			for _, v := range []string{value, b64(value)} {
				if strings.Contains(err.Error(), v) {
					t.Errorf("the failure carries the credential:\n%v", err)
				}
			}
		})
	}
}
