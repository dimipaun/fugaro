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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/secretname"
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
// Mutation (run, restore): ask the bundle first — in cascade.go's resolveEntry,
// move the entryFor call above the per-secret read. This test is red: the call
// carries the bundle's value for a credential a container was written for.
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
