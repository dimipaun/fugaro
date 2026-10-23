package credsvc

// audit_answer_test.go is the answer path's share of C1.13's audit log: every
// call this service answers with a credential is recorded, and so is every call
// it refuses after admission — one line each, through the service's own auditor,
// with the tier and the credential's NAME and never a value.
//
// The lines are read out of a handler of their own, because the auditor the
// answer path writes through is the one the service builds itself (NewServer's
// NewProbeAuditor) and armed with the bundle loader's own redaction — which is
// the point of it: the code that holds the values is the code that removes them
// (audit.go's WithAuditScrub, C1.11's loader).

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// auditSvc is the fixture with the service's own audit lines in a buffer of
// their own: the handler is the one NewServer builds its auditor on, so what
// this reads is the answer path's lines and nothing the service's logger wrote.
func auditSvc(t *testing.T, mutate func(*svc, *Config)) (*svc, *logBuf) {
	t.Helper()
	auditLog := &logBuf{}
	s := newSvcWith(t, func(sv *svc, cfg *Config) {
		cfg.AuditLog = slog.NewJSONHandler(auditLog, nil)
		if mutate != nil {
			mutate(sv, cfg)
		}
	})
	return s, auditLog
}

// forwardLines is the lines a buffer holds for the answer path's own acts —
// purpose "forward", which is every line an issuance or a refusal of a model
// call writes — in the order they were written. Lines for another purpose (a
// probe's own) are left out: a probe is a different act of the service, audited
// by C1.12, and a call that made one writes both.
func forwardLines(t *testing.T, buf *logBuf) []map[string]any {
	t.Helper()
	if buf.Len() == 0 {
		return nil
	}
	var out []map[string]any
	for _, line := range auditLines(t, buf) {
		if line["purpose"] == PurposeForward {
			out = append(out, line)
		}
	}
	return out
}

// fixtureValues is every credential value the fixture's own bundle holds: the
// three the answer path could attach, and the strings a line must never carry.
func fixtureValues(t *testing.T) []string {
	t.Helper()
	return []string{fakeCred(t, "default"), fakeCred(t, testAccount), fakeCred(t, "paid")}
}

// TestAnAnsweredCallIsAuditedOnce is the headline: one credential answered one
// call, so exactly one line is written, and it names the whole act — the run
// the service verified, its repository, the job the run reported, the account
// that called, the credential's NAME, its tier, the purpose and the status.
//
// Mutation (run, restore): drop the audit write from cascade.go's answer. The
// answered call writes no line and this test is red.
func TestAnAnsweredCallIsAuditedOnce(t *testing.T) {
	s, auditLog := auditSvc(t, nil)
	resp, body := s.call(t, callBody("audited"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	name, tier := resp.Header.Get(HeaderCredential), resp.Header.Get(HeaderTier)
	if name == "" || tier == "" {
		t.Fatalf("the answer names no credential (%q) or no tier (%q): there is nothing to audit it by", name, tier)
	}
	lines := forwardLines(t, auditLog)
	if len(lines) != 1 {
		t.Fatalf("an answered call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	checkAuditShape(t, line)
	for field, want := range map[string]any{
		"run":        s.run,
		"repository": s.slug,
		"job":        s.job,
		"caller":     testJobSA,
		"credential": name,
		"tier":       tier,
		"purpose":    PurposeForward,
		"status":     StatusOK,
	} {
		if got := line[field]; got != want {
			t.Errorf("the audit line's %s = %v, want %q", field, got, want)
		}
	}
	// The line names the credential and carries none of the values the bundle
	// holds: a name is what an operator reads and a value is what they must not.
	for _, v := range fixtureValues(t) {
		if strings.Contains(auditLog.String(), v) {
			t.Errorf("the audit line carries a credential's value: %s", auditLog.String())
		}
	}
}

// TestARefusedCallIsAudited: a call the service refuses after admission is
// recorded too, whether the refusal is the typed wait a run resumes under or
// the halt it takes. The line carries what was known when the call was refused
// — the run, its repository, the job and the account — and no credential: no
// single credential was chosen, and the ones that were considered are the run's
// own answer (the tried list on the body).
func TestARefusedCallIsAudited(t *testing.T) {
	t.Run("the typed wait", func(t *testing.T) {
		s, auditLog := auditSvc(t, func(s *svc, _ *Config) {
			// oauth-only, so a wait longer than the threshold is still a wait:
			// the paid key is in the bundle and the policy keeps it out.
			s.policy.shared = decodeTestPolicy(t, testPolicyDocWith("oauth-only"))
		})
		held := s.now.Add(6 * time.Hour)
		s.hold("alice", s.now.Add(-time.Minute), held)
		s.hold("default", s.now.Add(-time.Minute), held)

		resp, body := s.call(t, callBody("wait"))
		if resp.StatusCode != StatusExhaustedUntil {
			t.Fatalf("the answer is %d, want the typed wait (%d): %s", resp.StatusCode, StatusExhaustedUntil, body)
		}
		lines := forwardLines(t, auditLog)
		if len(lines) != 1 {
			t.Fatalf("a refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		line := lines[0]
		checkAuditShape(t, line)
		for field, want := range map[string]any{
			"run":        s.run,
			"repository": s.slug,
			"job":        s.job,
			"caller":     testJobSA,
			"credential": "",
			"tier":       "",
			"purpose":    PurposeForward,
			"status":     ErrorExhaustedUntil,
		} {
			if got := line[field]; got != want {
				t.Errorf("the wait's audit line %s = %v, want %q", field, got, want)
			}
		}
	})

	t.Run("a refusal that halts", func(t *testing.T) {
		s, auditLog := auditSvc(t, nil)
		// The operator's stop switch: the service issues no credential at all,
		// and the call is refused before the bundle is read.
		s.kills.repos[s.slug] = true

		resp, body := s.call(t, callBody("stopped"))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
			t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
		}
		lines := forwardLines(t, auditLog)
		if len(lines) != 1 {
			t.Fatalf("a halted call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		line := lines[0]
		checkAuditShape(t, line)
		for field, want := range map[string]any{
			"run":        s.run,
			"repository": s.slug,
			"job":        s.job,
			"caller":     testJobSA,
			"credential": "",
			"tier":       "",
			"purpose":    PurposeForward,
			"status":     ErrorStateUnavailable,
		} {
			if got := line[field]; got != want {
				t.Errorf("the refusal's audit line %s = %v, want %q", field, got, want)
			}
		}
		if n := s.up.Count(); n != 0 {
			t.Errorf("the upstream saw %d requests with the stop switch on, want none", n)
		}
	})
}

// TestABindingRefusalIsAudited: the pairing of the two identities (server.go's
// N1 check) is a refusal AFTER admission — Verify returned a whole identity, the
// run, the repository, the job and the account among it — so it is recorded like
// every other refusal on this path: one line, with the identity the service
// verified and the refusal's own class. The repository the line names is the
// identity's own and not the calling job's, which is the whole of what the
// refusal is about.
//
// The credential field is empty, and truthfully so: no credential was chosen and
// none was read — the binding is asked before the bundle — so the line names
// none.
//
// Mutation (run, restore): answer this refusal with s.refuse instead of
// refuseCall (server.go). The refused call writes no line and this test is red.
func TestABindingRefusalIsAudited(t *testing.T) {
	s, auditLog := auditSvc(t, nil)
	// A well-formed run identity of the other repository of the same project:
	// its uid spells its own slug, so the run token verifies, and it is the
	// PAIRING of it with the calling job's account that refuses the call (N1).
	otherSlug := testSlug(t, testOtherRepoPath)
	other := s.runToken(func(c map[string]any) {
		c["fs"] = otherSlug
		c["sub"] = "r~" + otherSlug + "~" + s.run
		c["user_id"] = c["sub"]
	})
	resp := s.postTokens(t, MessagesPath, callBody("bound-elsewhere"), other, s.jobToken(), s.job)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer: %v", err)
	}
	if resp.StatusCode != StatusUnverified {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnverified, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorUnverifiedRunIdentity {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorUnverifiedRunIdentity)
	}
	lines := forwardLines(t, auditLog)
	if len(lines) != 1 {
		t.Fatalf("a refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	checkAuditShape(t, line)
	for field, want := range map[string]any{
		"run":        s.run,
		"repository": otherSlug,
		"job":        s.job,
		"caller":     testJobSA,
		"credential": "",
		"tier":       "",
		"purpose":    PurposeForward,
		"status":     ErrorUnverifiedRunIdentity,
	} {
		if got := line[field]; got != want {
			t.Errorf("the binding refusal's audit line %s = %v, want %q", field, got, want)
		}
	}
	if n := s.up.Count(); n != 0 {
		t.Errorf("the upstream saw %d requests, want none: the call was refused before it was forwarded", n)
	}
	// And the line carries no credential VALUE anywhere in it — not the names
	// the bundle holds, and not the values behind them. The refusal is answered
	// before the bundle is read, so there is no value for it to carry, and the
	// assertion is here so that a later change that reads one before this
	// refusal cannot put it in the line unnoticed.
	for _, v := range fixtureValues(t) {
		if strings.Contains(auditLog.String(), v) {
			t.Errorf("the binding refusal's audit line carries a credential's value: %s", auditLog.String())
		}
	}
}

// TestARequestShapeRefusalIsAudited: the request-shape refusals
// (request_refused) are answered after admission — the run, its repository, the
// job and the account are verified — so they are recorded like every other
// refusal on this path, and the status is the contract's own word for them
// (wire.go's ErrorRequestRefused, which audit.go's ValidStatus carries): the
// line's vocabulary is the wire's, so there was never a name to invent.
//
// No credential is named, and truthfully so: the whole point of answering these
// here is that the bundle has not been read, so no credential was chosen and
// none was touched. The refusal's sentence is the run's and not the line's — the
// line is the contract's fields.
func TestARequestShapeRefusalIsAudited(t *testing.T) {
	for _, tc := range []struct {
		name  string
		share func(t *testing.T) string
		body  string
	}{
		{
			name: "the project publishes no allow-list",
			share: func(t *testing.T) string {
				return testPolicyDocNoAllowList()
			},
			body: callBody("no-allow-list"),
		},
		{
			name: "the model is not one the project pins",
			share: func(t *testing.T) string {
				return testPolicyDocAllowed(testModel)
			},
			body: `{"model":"claude-opus-5-5","max_tokens":16,"messages":[{"role":"user","content":"unpinned"}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, auditLog := auditSvc(t, func(s *svc, _ *Config) {
				s.policy.shared = decodeTestPolicy(t, tc.share(t))
			})
			resp, body := s.call(t, tc.body)
			if resp.StatusCode != StatusRequestRefused {
				t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusRequestRefused, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorRequestRefused {
				t.Errorf("the refusal is %q, want %q", e.Error, ErrorRequestRefused)
			}
			lines := forwardLines(t, auditLog)
			if len(lines) != 1 {
				t.Fatalf("a refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
			}
			line := lines[0]
			checkAuditShape(t, line)
			for field, want := range map[string]any{
				"run":        s.run,
				"repository": s.slug,
				"job":        s.job,
				"caller":     testJobSA,
				"credential": "",
				"tier":       "",
				"purpose":    PurposeForward,
				"status":     ErrorRequestRefused,
			} {
				if got := line[field]; got != want {
					t.Errorf("the request refusal's audit line %s = %v, want %q", field, got, want)
				}
			}
			if n := s.up.Count(); n != 0 {
				t.Errorf("the upstream saw %d requests, want none: a refused request is not forwarded", n)
			}
			if n := s.src.readCount(); n != 0 {
				t.Errorf("the bundle was read %d times, want 0: the refusal comes before the one document that holds a credential", n)
			}
			for _, v := range fixtureValues(t) {
				if strings.Contains(auditLog.String(), v) {
					t.Errorf("the refusal's audit line carries a credential's value: %s", auditLog.String())
				}
			}
		})
	}
}

// failFirstHandler records every audit line but the first, whose write it
// fails. It is the one way to observe what an issuance's failed line is
// answered with: the refusal that follows the failure is audited through the
// same auditor, so a handler that failed only once lets the REFUSAL's line be
// read — and a test that finds no line has found a refusal that was answered
// without one.
type failFirstHandler struct {
	inner  slog.Handler
	failed *bool
}

func (h *failFirstHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *failFirstHandler) Handle(ctx context.Context, r slog.Record) error {
	if !*h.failed {
		*h.failed = true
		return errors.New("the log refused the line it was handed")
	}
	return h.inner.Handle(ctx, r)
}

func (h *failFirstHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return &failFirstHandler{inner: h.inner.WithAttrs(a), failed: h.failed}
}

func (h *failFirstHandler) WithGroup(name string) slog.Handler {
	return &failFirstHandler{inner: h.inner.WithGroup(name), failed: h.failed}
}

// TestTheRefusalOfAnUnauditedIssuanceIsAudited: a call refused because its
// issuance's line could not be written is a call refused after admission like
// any other, so the refusal is audited too — through the same auditor, which is
// why the handler below fails one line and records the next. What the line
// names is the credential that answered (its name and its tier: it was chosen
// and it was spent, and the refusal is what withheld its effect) and the
// refusal's own class.
//
// A refusal that left no line would be a call this service answered with a
// refusal no operator can see, which is the one thing this log exists to make
// impossible.
func TestTheRefusalOfAnUnauditedIssuanceIsAudited(t *testing.T) {
	failed := false
	auditLog := &logBuf{}
	s := newSvcWith(t, func(s *svc, cfg *Config) {
		cfg.AuditLog = &failFirstHandler{inner: slog.NewJSONHandler(auditLog, nil), failed: &failed}
	})
	s.up.Script = []anthropicfake.Reply{anthropicfake.MessageOK(testModel, testUsage)}

	resp, body := s.call(t, callBody("unaudited-then-refused"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: an answer that cannot be audited must not be given", resp.StatusCode, StatusUnavailable)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("the upstream saw %d requests, want 1: the credential was spent before its line could be written", n)
	}
	lines := forwardLines(t, auditLog)
	if len(lines) != 1 {
		t.Fatalf("the refused call wrote %d audit lines, want exactly 1 (the refusal's): %v", len(lines), lines)
	}
	line := lines[0]
	checkAuditShape(t, line)
	for field, want := range map[string]any{
		"run":        s.run,
		"repository": s.slug,
		"job":        s.job,
		"caller":     testJobSA,
		"credential": testAccount,
		"tier":       TierA,
		"purpose":    PurposeForward,
		"status":     ErrorStateUnavailable,
	} {
		if got := line[field]; got != want {
			t.Errorf("the refusal's audit line %s = %v, want %q", field, got, want)
		}
	}
}

// TestACallIsRefusedWhenItCannotBeAudited is C1.13's fail-closed rule on the
// answer path (D143): an issuance this service cannot audit is not made. The
// upstream answered — the call is out of this service's hands by then — so what
// is withheld is the credential's EFFECT: no byte of the upstream's answer
// reaches the run, and the run halts on the refusal instead.
func TestACallIsRefusedWhenItCannotBeAudited(t *testing.T) {
	s, _ := auditSvc(t, func(_ *svc, cfg *Config) { cfg.AuditLog = silentHandler{} })
	s.up.Script = []anthropicfake.Reply{anthropicfake.MessageOK(testModel, testUsage)}

	resp, body := s.call(t, callBody("unaudited"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: an answer that cannot be audited must not be given", resp.StatusCode, StatusUnavailable)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if n := s.up.Count(); n != 1 {
		t.Errorf("the upstream saw %d requests, want 1: the credential was spent before its line could be written", n)
	}
	if !strings.Contains(s.logs.String(), "audit line was not written") {
		t.Errorf("the service's log says nothing about the line it could not write: %s", s.logs.String())
	}
}

// failingTransport is a transport that never reaches the upstream, so the
// service's own send turns it into a refusal (forward.go). The error it returns
// is the test's, which is how a credential's value is put in the one place an
// upstream's error would carry one.
type failingTransport struct{ err error }

func (t failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

// TestAnAuditLineNeverCarriesACredentialValue is the property the whole of
// audit.go exists for, on the answer path: the line carries the NAME the answer
// used and never the value — not when the upstream echoes the value it was
// handed in its answer, and not when its own error carries one.
//
// The remedy is not luck: the auditor the answer path writes through is armed
// with the bundle loader's own redaction (NewProbeAuditor), so a value that
// reached a field would be removed before any rule read it. The last third of
// this test shows that arming on the object the answer path really uses, so the
// assertion above is about the redaction and not about a value that happened
// never to be written.
func TestAnAuditLineNeverCarriesACredentialValue(t *testing.T) {
	value := fakeCred(t, "echoed")

	t.Run("the upstream's answer echoes it", func(t *testing.T) {
		tr := &echoTransport{status: http.StatusOK}
		s, auditLog := auditSvc(t, func(s *svc, cfg *Config) {
			s.src.docs = [][]byte{[]byte(config.ClaudeOAuthToken + ": " + value + "\n")}
			cfg.Client = &http.Client{Transport: tr}
		})
		tr.body = `{"type":"error","error":{"message":"you sent me ` + value + `"}}`

		resp, body := s.call(t, callBody("echoed"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), redactedValue) {
			t.Fatalf("the answer carries no redaction, so it is not the upstream's answer scrubbed:\n%s", body)
		}
		lines := forwardLines(t, auditLog)
		if len(lines) != 1 {
			t.Fatalf("the answered call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		if got := lines[0]["credential"]; got == value {
			t.Errorf("the audit line's credential is the VALUE, want the credential's name: %v", got)
		}
		if strings.Contains(auditLog.String(), value) {
			t.Errorf("the audit line carries the credential's value: %s", auditLog.String())
		}
	})

	t.Run("the upstream's error carries it", func(t *testing.T) {
		s, auditLog := auditSvc(t, func(_ *svc, cfg *Config) {
			cfg.Client = &http.Client{Transport: failingTransport{err: errors.New("dial tcp: x-api-key " + value + " was refused")}}
		})

		resp, body := s.call(t, callBody("error-echo"))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		if strings.Contains(string(body), value) {
			t.Errorf("the refusal carries the credential's value:\n%s", body)
		}
		lines := forwardLines(t, auditLog)
		if len(lines) != 1 {
			t.Fatalf("the refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
		}
		line := lines[0]
		checkAuditShape(t, line)
		if got := line["status"]; got != ErrorStateUnavailable {
			t.Errorf("the refusal's audit line status is %v, want %q", got, ErrorStateUnavailable)
		}
		if got := line["credential"]; got == "" || got == value {
			t.Errorf("the refusal's audit line names %v, want the credential it would have used and never its value", got)
		}
		if strings.Contains(auditLog.String(), value) {
			t.Errorf("the audit line carries the credential's value: %s", auditLog.String())
		}
	})
}

// TestTheAnswerPathsAuditorIsArmedFromTheLoader: the arming, on the object the
// answer path writes through. A value handed to that auditor where a NAME
// belongs is replaced by the redaction before any rule sees it, and the
// replacement is the loader's — which is what makes "a value can never reach a
// line" a property of the service's construction rather than of its callers.
func TestTheAnswerPathsAuditorIsArmedFromTheLoader(t *testing.T) {
	s, auditLog := auditSvc(t, nil)
	value := fakeCred(t, "default")
	loader := s.server.cfg.Bundles
	if _, err := loader.Load(context.Background()); err != nil {
		t.Fatalf("loading the bundle: %v", err)
	}
	if got := loader.Scrub("x=" + value + ";"); strings.Contains(got, value) {
		t.Fatalf("the loader's own scrub left a credential's value in %q", got)
	}
	id := Identity{Slug: s.slug, Run: s.run, Job: s.job, JobAccount: testJobSA}
	if err := s.server.audit.Write(context.Background(), id.Audit(AuditEvent{
		Credential: value, Tier: TierA, Purpose: PurposeForward, Status: StatusOK,
	})); err != nil {
		t.Fatalf("the answer path's auditor refused a line it should have redacted rather than refused: %v", err)
	}
	if strings.Contains(auditLog.String(), value) {
		t.Errorf("the answer path's auditor wrote a credential's value into its line: %s", auditLog.String())
	}
	lines := forwardLines(t, auditLog)
	if len(lines) != 1 {
		t.Fatalf("the auditor wrote %d lines, want 1: %v", len(lines), lines)
	}
	if got := lines[0]["credential"]; got != redactedValue {
		t.Errorf("the credential field is %v, want the redaction %q", got, redactedValue)
	}
}

// TestAProbeBesideAnAnswerIsAuditedApart: a call that made a probe writes two
// lines — the probe's own (C1.12) and the answer's — each naming the credential
// of its own act, which is what keeps "one line per issuance" a count an
// operator can take rather than an average.
func TestAProbeBesideAnAnswerIsAuditedApart(t *testing.T) {
	// probeSvc is the fixture this needs: oauth-only, one admitted credential,
	// so the backed-off mark is what the call meets first (C1.12's own fixture).
	s, auditLog := probeSvc(t, nil)
	s.backedOff("default", s.now.Add(-time.Hour), creds.DefaultBackoffInitial)
	s.up.Script = []anthropicfake.Reply{
		anthropicfake.MessageOK(testModel, testUsage),
		anthropicfake.MessageOK(testModel, testUsage),
	}

	resp, body := s.call(t, callBody("probe-then-answer"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(HeaderProbe); got == "" {
		t.Fatal("the answer carries no probe header, so this call made no probe")
	}
	all := auditLines(t, auditLog)
	if len(all) != 2 {
		t.Fatalf("the call wrote %d audit lines, want the probe's and the answer's: %v", len(all), all)
	}
	var purposes []any
	for _, line := range all {
		purposes = append(purposes, line["purpose"])
	}
	if !slices.Contains(purposes, PurposeProbe) || !slices.Contains(purposes, PurposeForward) {
		t.Errorf("the two lines are %v, want one probe line and one forward line", purposes)
	}
	fw := forwardLines(t, auditLog)
	if len(fw) != 1 {
		t.Fatalf("the answer wrote %d forward lines, want 1: %v", len(fw), fw)
	}
	if got := fw[0]["credential"]; got != resp.Header.Get(HeaderCredential) {
		t.Errorf("the answer's line names %v, want the credential the answer carried (%q)", got, resp.Header.Get(HeaderCredential))
	}
	for _, v := range fixtureValues(t) {
		if strings.Contains(auditLog.String(), v) {
			t.Errorf("an audit line carries a credential's value: %s", auditLog.String())
		}
	}
}

// ---------------------------------------------------------------------------
// Every refusal of the answer path, and the line beside it
// ---------------------------------------------------------------------------

// TestEveryRefusalBeforeACredentialAnswersIsAudited is the refusal half of
// C1.13's rule, one case per refusal the answer path answers before the
// upstream is ever reached — the project's policy, its model prices, the
// bundle, the repository's admissions, the cascade's own state, and a mark the
// service could not write.
//
// Each case asserts the two things together and not one of them: the run gets
// the typed refusal it is owed, AND the operator's log carries one line for it
// — the run, its repository, the job the run reported and the account that
// called, the purpose the route asked for, the refusal's own wire error type as
// the status, and the credential only where one was chosen (a refusal taken
// before the cascade answered names none; a mark that could not be written is a
// credential this call had already spent, and it is named).
//
// The count is what makes it a test of the log rather than of the answer: a
// refusal that left no line would be a call this service answered with a
// refusal no operator can see, which is the one thing this log exists to make
// impossible.
//
// Mutation (run, restore): answer any one of these refusals with s.refuse
// instead of refuseCall, and that case is red ("wrote 0 audit lines, want
// exactly 1") while the others stay green.
func TestEveryRefusalBeforeACredentialAnswersIsAudited(t *testing.T) {
	inf := math.Inf(1)
	cases := []struct {
		name string
		// setup wires the state the case is about, before the call is made.
		setup func(t *testing.T, s *svc)
		// status, errorType and one of message/messageHas are the refusal the
		// run reads; credential, tier and upstream are what else the case is
		// about.
		status     int
		errorType  string
		message    string
		messageHas string
		credential string
		tier       string
		upstream   int
	}{
		{
			name: "the project's policy could not be read",
			setup: func(_ *testing.T, s *svc) {
				s.policy.sharedErr = errors.New("the shared node could not be read")
			},
			status: StatusUnavailable, errorType: ErrorStateUnavailable,
			message: "the project's policy could not be read",
		},
		{
			name: "the project's model prices could not be read",
			setup: func(_ *testing.T, s *svc) {
				// A rate the catalogue cannot render — an infinity is not JSON —
				// so the table this service bounds a call by cannot be built.
				// That is the service's own state and not the caller's request,
				// which is why the two are refused differently (request.go).
				s.policy.shared.Prices = projectpolicy.Prices{
					testModel: {InputPerM: &inf, OutputPerM: &inf},
				}
			},
			status: StatusUnavailable, errorType: ErrorStateUnavailable,
			message: "the project's model prices could not be read",
		},
		{
			name: "the bundle could not be read",
			setup: func(_ *testing.T, s *svc) {
				s.src.errs = []error{errors.New("secretmanager: permission denied on projects/aurora/secrets/creds-bundle/versions/latest")}
			},
			status: StatusUnavailable, errorType: ErrorStateUnavailable,
			message: ErrBundleUnreadable.Error(),
		},
		{
			name: "the repository's admissions could not be read",
			setup: func(_ *testing.T, s *svc) {
				s.policy.admErr = errors.New("the child node could not be read")
			},
			status: StatusUnavailable, errorType: ErrorStateUnavailable,
			message: "the repository's admissions could not be read",
		},
		{
			name: "the cascade's state could not be read",
			setup: func(_ *testing.T, s *svc) {
				s.state.err = errors.New("the marks node could not be read")
			},
			status: StatusUnavailable, errorType: ErrorStateUnavailable,
			messageHas: "the marks node could not be read",
		},
		{
			name: "a mark could not be written",
			setup: func(_ *testing.T, s *svc) {
				// The credential is spent before its mark can be recorded: the
				// upstream answers a usage limit, and the write of the state that
				// limit is made of fails. The refusal names the credential the
				// call used, because what the failure costs the NEXT call is that
				// call being sent to this credential again.
				s.state.updateErr = errors.New("the mark could not be written")
				s.up.Script = []anthropicfake.Reply{
					anthropicfake.Error(http.StatusTooManyRequests, "rate_limit_error", "usage limit reached"),
				}
			},
			status: StatusUnavailable, errorType: ErrorStateUnavailable,
			message: markRefusal,
			// The candidate the fixture's selection answers with — the account
			// the fixture's child node admits, whose token is a tier-A one — and
			// the credential this call spent before its mark could be written.
			credential: testAccount, tier: TierA, upstream: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, auditLog := auditSvc(t, func(s *svc, _ *Config) { tc.setup(t, s) })

			resp, body := s.call(t, callBody("refused"))
			if resp.StatusCode != tc.status {
				t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, tc.status, body)
			}
			e := refusal(t, resp.StatusCode, body)
			if e.Error != tc.errorType {
				t.Errorf("the refusal is %q, want %q", e.Error, tc.errorType)
			}
			switch {
			case tc.message != "" && e.Message != tc.message:
				t.Errorf("the refusal's message is %q, want %q", e.Message, tc.message)
			case tc.messageHas != "" && !strings.Contains(e.Message, tc.messageHas):
				t.Errorf("the refusal's message is %q, want one carrying %q", e.Message, tc.messageHas)
			}
			if n := s.up.Count(); n != tc.upstream {
				t.Errorf("the upstream saw %d requests, want %d", n, tc.upstream)
			}

			lines := forwardLines(t, auditLog)
			if len(lines) != 1 {
				t.Fatalf("the refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
			}
			line := lines[0]
			checkAuditShape(t, line)
			for field, want := range map[string]any{
				"run":        s.run,
				"repository": s.slug,
				"job":        s.job,
				"caller":     testJobSA,
				"credential": tc.credential,
				"tier":       tc.tier,
				"purpose":    PurposeForward,
				"status":     tc.errorType,
			} {
				if got := line[field]; got != want {
					t.Errorf("the refusal's audit line %s = %v, want %q", field, got, want)
				}
			}
			// The line is a name and never a value, whatever the refusal was.
			for _, v := range fixtureValues(t) {
				if strings.Contains(auditLog.String(), v) {
					t.Errorf("the refusal's audit line carries a credential's value: %s", auditLog.String())
				}
			}
		})
	}
}

// TestARefusalBelowTheMintBranchCarriesTheRoutesPurpose holds every refusal
// answered BELOW the mint's branch — the project's policy, the call's own body,
// the request's check, the bundle, the repository's admissions (server.go) — to
// the ONE rule a line's purpose comes from, on BOTH routes the branch is reached
// for: the answer the call asked for is the route's own and not the branch's, so
// the line is read against routeAct(path) and never against a spelling written
// here.
//
// The rule is what is being pinned, and it is the reason this test is written
// the hard way (the expectation comes from routeAct, the call is driven on
// /v1/messages AND /v1/messages/count_tokens): these lines used to carry a
// hard-coded `forward`, and a literal is a value that can stop being the
// route's without anything failing — the mint's own branch returns above them
// today, so today's answer for every route that reaches them happens to be
// `forward` and a test that compared against that word would agree with a
// revert as readily as with the rule. What this reading catches is a purpose
// invented in a branch (the mint's, a probe's, an empty one) and a branch that
// stops reading the route: the moment the rule's answer for a route differs
// from a literal in a branch, this is red — which is the drift the rule exists
// to make impossible.
//
// Each case also pins the credential the route's read gives, which for a model
// call is none: no credential was chosen before the bundle was read, and a line
// that named one would be a line this service cannot write (audit.go's
// both-or-neither rule would refuse it, and the call would go unaudited).
func TestARefusalBelowTheMintBranchCarriesTheRoutesPurpose(t *testing.T) {
	cases := []struct {
		name string
		// path is the one route the case is reached for; empty means both.
		path string
		// setup wires the state the refusal is about.
		setup func(t *testing.T, s *svc)
		// broken is a body the service cannot read at all, which is answered
		// through the handler and not over a socket (directCallAt).
		broken bool
	}{
		{
			name: "the project's policy could not be read",
			setup: func(_ *testing.T, s *svc) {
				s.policy.sharedErr = errors.New("the shared node could not be read")
			},
		},
		{
			name:   "the call's body could not be read",
			broken: true,
		},
		{
			// The request's own check is asked on the Messages path alone: the
			// token count's body is not a Messages body (server.go).
			name: "the request is refused before any credential is read",
			path: MessagesPath,
			setup: func(_ *testing.T, s *svc) {
				s.policy.shared = decodeTestPolicy(t, testPolicyDocNoAllowList())
			},
		},
		{
			name: "the bundle could not be read",
			setup: func(_ *testing.T, s *svc) {
				s.src.errs = []error{errors.New("secretmanager: permission denied on projects/aurora/secrets/creds-bundle/versions/latest")}
			},
		},
		{
			name: "the repository's admissions could not be read",
			setup: func(_ *testing.T, s *svc) {
				s.policy.admErr = errors.New("the child node could not be read")
			},
		},
	}
	for _, tc := range cases {
		for _, path := range []string{MessagesPath, CountTokensPath} {
			if tc.path != "" && tc.path != path {
				continue
			}
			t.Run(tc.name+" — "+path, func(t *testing.T) {
				s, auditLog := auditSvc(t, func(s *svc, _ *Config) {
					if tc.setup != nil {
						tc.setup(t, s)
					}
				})
				var code int
				var body []byte
				if tc.broken {
					rec := s.directCallAt(t, path, brokenBody{})
					code, body = rec.Code, rec.Body.Bytes()
				} else {
					resp, b := s.callPath(t, path, callBody("route-purpose"))
					code, body = resp.StatusCode, b
				}
				if code != StatusUnavailable && code != StatusRequestRefused {
					t.Fatalf("the answer is %d, want a refusal: %s", code, body)
				}
				// The oracle: the rule the line's purpose is read off, and the
				// credential that comes with it.
				purpose, credential := routeAct(path)
				if !ValidPurpose(purpose) {
					t.Fatalf("routeAct(%q) gives %q, which is not a purpose this service writes: this test's oracle has moved", path, purpose)
				}
				lines := auditLines(t, auditLog)
				if len(lines) != 1 {
					t.Fatalf("the refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
				}
				line := lines[0]
				checkAuditShape(t, line)
				if got := line["purpose"]; got != purpose {
					t.Errorf("the refusal's audit line carries purpose %v, want the route's own %q: the line must name the act the call asked for and not the branch it was answered in", got, purpose)
				}
				if got := line["credential"]; got != credential {
					t.Errorf("the refusal's audit line carries credential %v, want the route's own %q", got, credential)
				}
				if got := line["tier"]; got != "" {
					t.Errorf("the refusal's audit line carries tier %v, want none: no credential was chosen", got)
				}
			})
		}
	}
}

// brokenBody is a request body the service cannot read at all: the one state
// readCall refuses for that is not a body over the gateway's size bound.
type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) {
	return 0, errors.New("the body was cut off")
}

// directCall answers one call through the service's handler without a socket,
// so a test can hand it a request the transport could not: a body whose own
// read fails. The call is otherwise the fixture's own — the same job token, the
// same run identity and the same reported job name — so what it reaches is the
// path a real call reaches.
func (s *svc) directCall(t *testing.T, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	return s.directCallAt(t, MessagesPath, body)
}

// directCallAt is directCall on either of the service's forward routes, for a
// test that drives the same refusal on both of them.
func (s *svc) directCallAt(t *testing.T, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Authorization", "Bearer "+s.jobToken())
	req.Header.Set(HeaderRunIdentity, s.runToken())
	req.Header.Set(HeaderRunJob, s.job)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.server.ServeHTTP(rec, req)
	return rec
}

// TestAReadCallRefusalIsAudited: the call's own body could not be read, so
// there is nothing to forward and nothing to check — the refusal is answered
// before any credential is touched, and it is recorded like every other refusal
// this service answers once it has an identity in hand: one line, the identity
// the service verified, the refusal's own class, and no credential, because
// none was read.
func TestAReadCallRefusalIsAudited(t *testing.T) {
	s, auditLog := auditSvc(t, nil)

	rec := s.directCall(t, brokenBody{})
	if rec.Code != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", rec.Code, StatusUnavailable, rec.Body.String())
	}
	e := refusal(t, rec.Code, rec.Body.Bytes())
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if !strings.Contains(e.Message, "the call's body could not be read") {
		t.Errorf("the refusal is %q, want this file's own sentence about the body", e.Message)
	}

	lines := forwardLines(t, auditLog)
	if len(lines) != 1 {
		t.Fatalf("the refused call wrote %d audit lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	checkAuditShape(t, line)
	for field, want := range map[string]any{
		"run":        s.run,
		"repository": s.slug,
		"job":        s.job,
		"caller":     testJobSA,
		"credential": "",
		"tier":       "",
		"purpose":    PurposeForward,
		"status":     ErrorStateUnavailable,
	} {
		if got := line[field]; got != want {
			t.Errorf("the refusal's audit line %s = %v, want %q", field, got, want)
		}
	}
}

// TestAnAnswerThatCannotCarryTheCredentialIsRefusedAndTheLogSaysWhy is the one
// refusal of the answer path whose LINE cannot be written, and it pins what
// happens then rather than leaving it to be discovered.
//
// The answer's own headers are built and validated before any of them is
// written, so an answer that cannot carry the tier and the credential's name is
// refused instead of sent — and this is the refusal of the pair whose line
// cannot be written either: the credential is known and its tier is not one
// this contract spells, and the auditor refuses a line that names one without
// the other (audit.go's both-or-neither rule, which is the same rule the wire
// keeps). So the refusal is answered and the log carries the auditor's own
// refusal of the event — never a line claiming the call was served.
//
// The path is unreachable through a real bundle — the loader builds its
// credentials from the keys the platform defines and every tier it can produce
// is one wireTier spells — which is why the tier below is built by hand: the
// test is about the branch, and the branch is the code's answer to a bundle
// this service does not believe it can be handed (server_test.go's fixture is
// where a real one comes from).
//
// Mutation (run, restore): drop the SetCredentialHeaders check from
// cascade.go's answer and this test is red — the answer goes out under a tier
// the contract cannot read back.
func TestAnAnswerThatCannotCarryTheCredentialIsRefusedAndTheLogSaysWhy(t *testing.T) {
	s, auditLog := auditSvc(t, nil)
	id := Identity{Slug: s.slug, Run: s.run, Job: s.job, JobAccount: testJobSA}
	cl := &call{}
	ans := &answer{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		},
		credential: fakeCred(t, "default"),
	}
	rec := httptest.NewRecorder()
	s.server.answer(context.Background(), rec, id, cl, ans,
		creds.Credential{Name: testAccount, Tier: creds.Tier("Z")}, nil, nil)

	if rec.Code != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", rec.Code, StatusUnavailable, rec.Body.String())
	}
	e := refusal(t, rec.Code, rec.Body.Bytes())
	if e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
	}
	if !strings.Contains(e.Message, "could not carry the credential") {
		t.Errorf("the refusal is %q, want the sentence about the credential the answer could not carry", e.Message)
	}
	if strings.Contains(rec.Body.String(), ans.credential) {
		t.Errorf("the refusal carries the credential's value:\n%s", rec.Body.String())
	}

	lines := auditLines(t, auditLog)
	if len(lines) != 1 {
		t.Fatalf("the refused call wrote %d audit records, want exactly 1 (the auditor's refusal of the event): %v", len(lines), lines)
	}
	rec0 := lines[0]
	if got := rec0["msg"]; got != auditRefusedMessage {
		t.Errorf("the record's message is %v, want the auditor's refusal %q: a line claiming this call was served would be a lie", got, auditRefusedMessage)
	}
	if got := rec0["field"]; got != "credential" {
		t.Errorf("the auditor refused the event's %v, want the credential (the half of the pair that could not be written)", got)
	}
	if !strings.Contains(s.logs.String(), "could not be audited") {
		t.Errorf("the service's log says nothing about the line it could not write: %s", s.logs.String())
	}
}

// TestAFailedStreamIsAudited: the issuance's line is written before a byte of
// the answer (fail closed, D143), so it says the credential answered — which it
// did — and the outcome of the answer itself is a second line.
//
// A stream that does not end is a call that got no whole answer: the upstream's
// body failed mid-stream, or the client's own connection did. Without the
// second line the log of such a call reads ok and nothing else, which is the
// one thing an operator reading the audit trail cannot act on.
//
// Mutation (run, restore): drop the outcome line from cascade.go's answer (or
// ignore Pump's bool, as it was ignored before) and this test is red — the
// truncated call writes one line, the issuance's, and reads as served.
func TestAFailedStreamIsAudited(t *testing.T) {
	s, auditLog := auditSvc(t, nil)
	// One event, then the connection is dropped: the service's own read of the
	// upstream's body fails after the answer has begun.
	s.up.Script = []anthropicfake.Reply{{
		Status: http.StatusOK,
		Events: []anthropicfake.Event{
			anthropicfake.StartEvent(testModel, testUsage),
			{Name: "content_block_delta", Data: `{"type":"content_block_delta","index":0}`},
		},
		CutAfter: 1,
	}}

	resp := s.post(t, MessagesPath, callBody("truncated-stream"), func(r *http.Request) {
		r.Header.Set("Accept", "text/event-stream")
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the answer is %d, want 200: the refusal belongs to the headers, not to the stream", resp.StatusCode)
	}
	// The client's read may fail where the stream was cut, and that is the
	// point of the test rather than a failure of it.
	_, _ = io.Copy(io.Discard, resp.Body)

	waitFor(t, "the failed stream's own audit line", func() bool {
		return len(forwardLines(t, auditLog)) >= 2
	})
	lines := forwardLines(t, auditLog)
	if len(lines) != 2 {
		t.Fatalf("the truncated call wrote %d audit lines, want two — the issuance's and the failure's: %v", len(lines), lines)
	}
	name, tier := resp.Header.Get(HeaderCredential), resp.Header.Get(HeaderTier)
	if name == "" || tier == "" {
		t.Fatalf("the answer names no credential (%q) or no tier (%q)", name, tier)
	}
	for i, line := range lines {
		checkAuditShape(t, line)
		for field, want := range map[string]any{
			"run":        s.run,
			"repository": s.slug,
			"job":        s.job,
			"caller":     testJobSA,
			"credential": name,
			"tier":       tier,
			"purpose":    PurposeForward,
		} {
			if got := line[field]; got != want {
				t.Errorf("line %d: %s = %v, want %q", i, field, got, want)
			}
		}
	}
	if got := lines[0]["status"]; got != StatusOK {
		t.Errorf("the first line's status is %v, want %q: the issuance is written before the answer", got, StatusOK)
	}
	if got := lines[1]["status"]; got != ErrorStateUnavailable {
		t.Errorf("the second line's status is %v, want %q: the call got no whole answer", got, ErrorStateUnavailable)
	}
}
