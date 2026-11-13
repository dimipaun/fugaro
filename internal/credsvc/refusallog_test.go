package credsvc

// refusallog_test.go holds the one line a refusal leaves an operator besides
// its audit line (refusallog.go) to its two rules: it NAMES the read that
// failed and the Go type of the error it failed with, and it carries no error
// text — an RTDB error embeds the path it read — and therefore no value a
// read's error might quote, and no credential, token or request body.
//
// The incident the line exists for was seen twice: a run's first /v1/messages
// forward was answered 503 state_unavailable by a warm service, on 2026-11-12
// and again on 2026-11-13, and the service's log said nothing about which of
// its reads had failed. Every test here fails exactly ONE read and reads the
// line back, so a read that stops being named — or a label that starts being
// written for the wrong one — is red. The candidate list's own build is the one
// refusal the line does not label, and its case below pins that shape too: an
// empty read beside a message that names the read itself.
//
// The sentinel is the second half of that. Each failure carries a
// credential-shaped string built at test time and planted INSIDE the error's
// text, which is the shape an error that quotes what it was reading has, and
// the line's own JSON must contain none of it: the class the line carries is
// the planted type's (%T) and never its text.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

// refusalTestErr is a read's failure as a test plants it: the text it carries
// is the sentinel the line must not repeat, and its Go type is the class the
// line must name.
type refusalTestErr struct{ text string }

func (e *refusalTestErr) Error() string { return e.text }

// refusalTestClass is what %T answers for the type above, spelled the way the
// line writes it.
const refusalTestClass = "*credsvc.refusalTestErr"

// refusalSvc is the fixture with the service's OWN lines read as the JSON
// objects a deployment's log viewer reads them as, so the fields of the
// refusal line can be checked one by one, and with the audit lines in a buffer
// of their own (auditSvc's property), so a test can hold the two apart.
func refusalSvc(t *testing.T, mutate func(*svc, *Config)) (*svc, *logBuf) {
	t.Helper()
	auditLog := &logBuf{}
	s := newSvcWith(t, func(sv *svc, cfg *Config) {
		cfg.Log = slog.New(slog.NewJSONHandler(sv.logs, nil))
		cfg.AuditLog = slog.NewJSONHandler(auditLog, nil)
		if mutate != nil {
			mutate(sv, cfg)
		}
	})
	return s, auditLog
}

// refusalLines is the refusals the service logged, in order.
func refusalLines(t *testing.T, s *svc) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(s.logs.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("a line of the service's log is not one JSON object: %v (%q)", err, line)
		}
		if m["msg"] == refusedMessage {
			out = append(out, m)
		}
	}
	return out
}

// refusalFields is the shape of the line: the refusal's own fields, and no
// others. A field added to the line — which is how a credential's value or a
// request body would reach it — fails every test that reads this.
var refusalFields = []string{"type", "status", "repository", "run", "message", "read", "error_class"}

// oneRefusalLine is the line a refused call wrote, held to its shape and to
// the fields every refusal carries: the message it is logged under, the
// warning level, and the run and repository the refusal was about.
func oneRefusalLine(t *testing.T, s *svc) map[string]any {
	t.Helper()
	lines := refusalLines(t, s)
	if len(lines) != 1 {
		t.Fatalf("the service logged %d refusal lines, want exactly 1: %v", len(lines), lines)
	}
	line := lines[0]
	if line["level"] != "WARN" {
		t.Errorf("the refusal's line is logged at %v, want WARN: a refusal is an operator's business", line["level"])
	}
	var got []string
	for k := range line {
		// The JSON handler's own three, which are not the line's.
		if k == "time" || k == "level" || k == "msg" {
			continue
		}
		got = append(got, k)
	}
	want := slices.Clone(refusalFields)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the refusal's line carries %v, want exactly %v", got, want)
	}
	for field, value := range map[string]any{"repository": s.slug, "run": s.run} {
		if line[field] != value {
			t.Errorf("the refusal's line %s = %v, want %q", field, line[field], value)
		}
	}
	return line
}

// refusedStatus is the status of the line as reading it back hands it over: a
// number that went through JSON is a float64, and spelling that out at each
// call site would be noise rather than a check.
func refusedStatus(status int) float64 { return float64(status) }

// refusedJSON is the line as it was written, for the tests that search it for
// text that must not be in it at all.
func refusedJSON(t *testing.T, s *svc) string {
	t.Helper()
	lines := refusalLines(t, s)
	if len(lines) == 0 {
		t.Fatal("the service logged no refusal line")
	}
	raw, err := json.Marshal(lines)
	if err != nil {
		t.Fatalf("the refusal's line is not one this test can read back: %v", err)
	}
	return string(raw)
}

// TestRefusalLogNamesTheReadThatFailed is the heart of it: one failing read per
// case, and the line says which read it was and what class of error it failed
// with, and nothing of the error's text.
//
// Mutation (run, restore): drop the labelRead from any one of the reads this
// table fails — server.go's stop switches, its policy read or its bundle load,
// or creds' state wrapper (refusallog.go's stateReads) — and that case is red:
// the line names no read.
//
// The candidate list has a case of its own below, and it is the one refusal
// this line writes with no read: the list is a step and not a read of one node
// (refusallog.go's label block).
func TestRefusalLogNamesTheReadThatFailed(t *testing.T) {
	cases := []struct {
		// what the case is about, and the read the line must name.
		name, read string
		// fail is the one read this case breaks.
		fail func(*svc, *Config, error)
		// message is the sentence the line must carry, which is the one the
		// run is sent.
		message string
	}{
		{
			name: "the operator's stop switches", read: readKills,
			fail:    func(s *svc, _ *Config, cause error) { s.kills.err = cause },
			message: "the operator's stop switches could not be read",
		},
		{
			name: "the project's shared node", read: readPolicyShared,
			fail:    func(s *svc, _ *Config, cause error) { s.policy.sharedErr = cause },
			message: "the project's policy could not be read",
		},
		{
			name: "the bundle", read: readBundle,
			fail: func(s *svc, _ *Config, cause error) {
				s.src.docs, s.src.errs = nil, []error{cause}
			},
			message: ErrBundleUnreadable.Error(),
		},
		{
			name: "the marks", read: readSelectorMarks,
			fail:    func(s *svc, _ *Config, cause error) { s.state.marksErr = cause },
			message: selectionUnreadable,
		},
		{
			name: "the leases", read: readSelectorLeases,
			fail:    func(s *svc, _ *Config, cause error) { s.state.leasesErr = cause },
			message: selectionUnreadable,
		},
		{
			name: "the run's own placement", read: readSelectorCurrent,
			fail:    func(s *svc, _ *Config, cause error) { s.state.currentErr = cause },
			message: selectionUnreadable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sentinel := fakeCred(t, "sentinel")
			s, auditLog := refusalSvc(t, func(s *svc, cfg *Config) {
				tc.fail(s, cfg, &refusalTestErr{text: "reading " + sentinel + " failed"})
			})

			resp, body := s.call(t, callBody("refused"))
			if resp.StatusCode != StatusUnavailable {
				t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
			}
			if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
				t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
			}

			line := oneRefusalLine(t, s)
			for field, want := range map[string]any{
				"type":        ErrorStateUnavailable,
				"status":      refusedStatus(StatusUnavailable),
				"read":        tc.read,
				"error_class": refusalTestClass,
				"message":     tc.message,
			} {
				if line[field] != want {
					t.Errorf("the refusal's line %s = %v, want %q", field, line[field], want)
				}
			}
			// The class and the label, and never the text: the sentinel is in
			// the error and in nothing the operator reads.
			if logged := s.logs.String(); strings.Contains(logged, sentinel) {
				t.Errorf("the service's log carries the text of the error it refused on: %s", logged)
			}

			// One refusal, one audit line — and the audit is the record of the
			// act, which this line is written beside and never instead of.
			if lines := forwardLines(t, auditLog); len(lines) != 1 {
				t.Fatalf("a refused call wrote %d forward audit lines, want exactly 1: %v", len(lines), lines)
			} else {
				checkAuditShape(t, lines[0])
			}
		})
	}
}

// TestTheRefusalLogLeavesTheCandidateListsOwnFailureUnlabelled: the repository's
// own node cannot be read, and the refusal of it is the candidate list's — the
// crafts narrow the list and the admissions fill it, and the one sentence they
// share names the read for the operator ("the repository's admissions could not
// be read", server.go's admissions branch), so the line carries that sentence
// and no label of its own.
//
// The two empty fields are the point of the case: an operator must not read
// read="" as "no read failed" — here a read did, and the sentence says which.
func TestTheRefusalLogLeavesTheCandidateListsOwnFailureUnlabelled(t *testing.T) {
	sentinel := fakeCred(t, "sentinel")
	s, auditLog := refusalSvc(t, func(s *svc, _ *Config) {
		s.policy.admErr = &refusalTestErr{text: "reading " + sentinel + " failed"}
	})

	resp, body := s.call(t, callBody("refused"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	line := oneRefusalLine(t, s)
	for field, want := range map[string]any{
		"type":        ErrorStateUnavailable,
		"status":      refusedStatus(StatusUnavailable),
		"read":        "",
		"error_class": "",
		"message":     "the repository's admissions could not be read",
	} {
		if line[field] != want {
			t.Errorf("the refusal's line %s = %v, want %q", field, line[field], want)
		}
	}
	if logged := s.logs.String(); strings.Contains(logged, sentinel) {
		t.Errorf("the service's log carries the text of the error it refused on: %s", logged)
	}
	if lines := forwardLines(t, auditLog); len(lines) != 1 {
		t.Fatalf("a refused call wrote %d forward audit lines, want exactly 1: %v", len(lines), lines)
	} else {
		checkAuditShape(t, lines[0])
	}
}

// TestRefusalLogNamesTheSecretReadThatFailed is R6's read: the container a
// credential's rotation wrote. The refusal is the read's own class sentence
// (perSecretRefusal, which carries no container and no value), and the line
// beside it names the read and the class of the failure underneath.
//
// Mutation (run, restore): drop the labelRead from the per-secret read
// (cascade.go's forward) and the line names an empty read.
func TestRefusalLogNamesTheSecretReadThatFailed(t *testing.T) {
	sentinel := fakeCred(t, "sentinel")
	dyn := &dynReader{doc: testBundleDoc(t), values: map[string]string{}}
	loader := &BundleLoader{}
	loader.Source = dyn
	s, auditLog := refusalSvc(t, func(_ *svc, cfg *Config) { cfg.Bundles = loader })
	dyn.err = &refusalTestErr{text: "the container holds " + sentinel + " and could not be decoded"}

	name := mustUserName(t, testLauncherSub, testAccount)
	ctx := context.Background()
	b, err := loader.Load(ctx)
	if err != nil {
		t.Fatalf("the bundle was not read: %v", err)
	}
	rec := httptest.NewRecorder()
	sel := &creds.Selector{
		Store:    &stateReads{Store: s.state},
		Settings: settingsOf(s.policy.shared),
		Now:      func() time.Time { return s.now },
	}
	s.server.forward(ctx, rec, providerCall(t, MessagesPath), sel,
		Identity{Slug: s.slug, Run: s.run, Job: s.job, JobAccount: testJobSA, JobClass: ClassRepository},
		b, []creds.Credential{{Name: name, Tier: creds.TierA}}, nil)

	if rec.Code != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", rec.Code, StatusUnavailable, rec.Body)
	}
	line := oneRefusalLine(t, s)
	for field, want := range map[string]any{
		"type":        ErrorStateUnavailable,
		"status":      refusedStatus(StatusUnavailable),
		"read":        readSecret,
		"error_class": refusalTestClass,
		"message":     perSecretRefusal,
	} {
		if line[field] != want {
			t.Errorf("the refusal's line %s = %v, want %q", field, line[field], want)
		}
	}
	for _, text := range []string{sentinel, "could not be decoded"} {
		if strings.Contains(refusedJSON(t, s), text) {
			t.Errorf("the refusal's line carries %q, want the read's class and never its text", text)
		}
	}
	if lines := forwardLines(t, auditLog); len(lines) != 1 {
		t.Fatalf("a refused call wrote %d forward audit lines, want exactly 1: %v", len(lines), lines)
	}
}

// refusalTransport is a client whose one failure is the sentinel-carrying
// transport error of a call that never left the service.
type refusalTransport struct{ err error }

func (t refusalTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

// TestRefusalLogNamesTheUpstreamSendThatFailed: the send's own rule is that the
// transport's error is not carried (forward.go), because it quotes the URL the
// call was going to, and the line keeps that rule in its own terms — the sentinel
// inside the transport's error appears nowhere.
func TestRefusalLogNamesTheUpstreamSendThatFailed(t *testing.T) {
	sentinel := fakeCred(t, "sentinel")
	s, auditLog := refusalSvc(t, func(_ *svc, cfg *Config) {
		cfg.Client = &http.Client{Transport: refusalTransport{err: &refusalTestErr{
			text: "the transport to api.anthropic.com/" + sentinel + " failed",
		}}}
	})

	resp, body := s.call(t, callBody("refused"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	line := oneRefusalLine(t, s)
	for field, want := range map[string]any{
		"type":    ErrorStateUnavailable,
		"status":  refusedStatus(StatusUnavailable),
		"read":    readUpstream,
		"message": "credsvc: the upstream could not be reached",
	} {
		if line[field] != want {
			t.Errorf("the refusal's line %s = %v, want %q", field, line[field], want)
		}
	}
	// The transport's error is a *refusalTestErr and send answers a sentence of
	// this package's own in its place, so the class here is that sentence's.
	if class, _ := line["error_class"].(string); class != "*errors.errorString" {
		t.Errorf("the refusal's line error_class = %q, want %q: the send answers its own sentence",
			class, "*errors.errorString")
	}
	if logged := s.logs.String(); strings.Contains(logged, sentinel) {
		t.Errorf("the service's log carries the transport's error: %s", logged)
	}
	if lines := forwardLines(t, auditLog); len(lines) != 1 {
		t.Fatalf("a refused call wrote %d forward audit lines, want exactly 1: %v", len(lines), lines)
	}
}

// TestRefusalLogNamesTheWaitAndTheStopSwitch: the two refusals that are not a
// failure of anything. A selection that nothing can serve now leaves a typed
// wait — the run acts on it rather than halting — and the operator's stop
// switch being ON is a refusal by decision. Both are refused all the same, and
// both leave the line: the shape is one shape, and an empty read with an empty
// class is how it says "nothing failed here".
func TestRefusalLogNamesTheWaitAndTheStopSwitch(t *testing.T) {
	t.Run("a typed wait", func(t *testing.T) {
		// oauth-only: the project's paid key is the credential the cascade
		// could still fall to, and this policy does not admit it (D152), so a
		// held subscription is a wait and not a fallback.
		s, auditLog := refusalSvc(t, func(s *svc, _ *Config) {
			s.policy.shared = decodeTestPolicy(t, testPolicyDocWith("oauth-only"))
		})
		// Every credential this repository is admitted to, held for longer than
		// the policy's threshold: nothing is usable now and something becomes
		// usable at an instant, which is the run's answer to wait on.
		for _, name := range []string{config.DefaultClaudeAccount, testAccount} {
			s.hold(name, s.now.Add(-time.Minute), s.now.Add(6*time.Hour))
		}

		resp, body := s.call(t, callBody("waiting"))
		if resp.StatusCode != StatusExhaustedUntil {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusExhaustedUntil, body)
		}
		e := refusal(t, resp.StatusCode, body)
		line := oneRefusalLine(t, s)
		for field, want := range map[string]any{
			"type":        ErrorExhaustedUntil,
			"status":      refusedStatus(StatusExhaustedUntil),
			"read":        "",
			"error_class": "*creds.ExhaustedUntil",
			// The sentence the run is sent, and not a second rendering of it.
			"message": e.Message,
		} {
			if line[field] != want {
				t.Errorf("the refusal's line %s = %v, want %q", field, line[field], want)
			}
		}
		if lines := forwardLines(t, auditLog); len(lines) != 1 {
			t.Fatalf("a refused call wrote %d forward audit lines, want exactly 1: %v", len(lines), lines)
		}
	})

	t.Run("the operator's stop switch is on", func(t *testing.T) {
		s, auditLog := refusalSvc(t, func(s *svc, _ *Config) { s.kills.repos[s.slug] = true })

		resp, body := s.call(t, callBody("stopped"))
		if resp.StatusCode != StatusUnavailable {
			t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
		}
		line := oneRefusalLine(t, s)
		for field, want := range map[string]any{
			"type":        ErrorStateUnavailable,
			"status":      refusedStatus(StatusUnavailable),
			"read":        "",
			"error_class": "",
			"message":     "the credentials service is stopped by the repo kill switch",
		} {
			if line[field] != want {
				t.Errorf("the refusal's line %s = %v, want %q", field, line[field], want)
			}
		}
		if lines := forwardLines(t, auditLog); len(lines) != 1 {
			t.Fatalf("a refused call wrote %d forward audit lines, want exactly 1: %v", len(lines), lines)
		}
	})
}

// TestRefusalLogNamesTheSlipReadThatFailed: the slip a call presents is a read
// of this service's own state like the marks and the bundle — the record lives
// in the database, and the in-memory copy holds only the slips this instance
// has already verified — and it is the one read of the forward path whose
// failure the run is told as a class alone ("the slip could not be read"). The
// line is where the read and the error's class are, and it is also where the
// claim secret the call carried must NOT be: the store's error quotes the slip
// id it read, and the secret is the one credential a slip-carrying call holds.
func TestRefusalLogNamesTheSlipReadThatFailed(t *testing.T) {
	secret := fakeCred(t, "sentinel")
	slipID := strings.Repeat("ab", 16)
	s, auditLog := refusalSvc(t, func(_ *svc, cfg *Config) {
		db := gcpfake.NewRTDB(t)
		db.DenyRead(creds.PathSlip(slipID))
		cfg.Slips = slipStore(t, db)
	})

	resp, body := s.call(t, callBody("slip"), func(r *http.Request) {
		r.Header.Set(HeaderSlip, slipID)
		r.Header.Set(HeaderSlipClaim, secret)
	})
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}
	if e := refusal(t, resp.StatusCode, body); e.Error != ErrorStateUnavailable {
		t.Errorf("the refusal is %q, want %q", e.Error, ErrorStateUnavailable)
	}

	line := oneRefusalLine(t, s)
	// The class is the database's own: the store wraps whatever the read
	// answered with (creds' Slip), and a read the rules deny is an error this
	// client builds from the answer and not one a test plants here — which is
	// why this case is the one that names a class the test does not own, and
	// the label beside it is what tells it from any other sentence of that
	// class.
	for field, want := range map[string]any{
		"type":        ErrorStateUnavailable,
		"status":      refusedStatus(StatusUnavailable),
		"read":        readSlip,
		"error_class": "*errors.errorString",
		"message":     "the slip could not be read",
	} {
		if line[field] != want {
			t.Errorf("the refusal's line %s = %v, want %q", field, line[field], want)
		}
	}
	if logged := s.logs.String(); strings.Contains(logged, secret) {
		t.Errorf("the service's log carries the claim secret the call presented: %s", logged)
	}
	if lines := forwardLines(t, auditLog); len(lines) != 1 {
		t.Fatalf("a refused call wrote %d forward audit lines, want exactly 1: %v", len(lines), lines)
	} else {
		checkAuditShape(t, lines[0])
	}
}

// TestTheRefusalLogIsWrittenBesideTheAuditLog: the two are separate acts with
// separate messages, and an operator reads them apart — the audit line is the
// record of what the service did with a credential (C1.13) and this line is the
// diagnosis. Neither is written into the other's stream, and a refusal writes
// exactly one of each: a second line would be two refusals where the run was
// sent one.
func TestTheRefusalLogIsWrittenBesideTheAuditLog(t *testing.T) {
	sentinel := fakeCred(t, "sentinel")
	s, auditLog := refusalSvc(t, func(s *svc, _ *Config) {
		s.state.marksErr = &refusalTestErr{text: "reading " + sentinel + " failed"}
	})

	resp, body := s.call(t, callBody("refused"))
	if resp.StatusCode != StatusUnavailable {
		t.Fatalf("the answer is %d, want %d: %s", resp.StatusCode, StatusUnavailable, body)
	}

	if lines := refusalLines(t, s); len(lines) != 1 {
		t.Fatalf("a refused call wrote %d refusal lines, want exactly 1", len(lines))
	}
	if logged := s.logs.String(); strings.Contains(logged, auditMessage) {
		t.Errorf("the service's log carries an audit line: %s", logged)
	}
	lines := forwardLines(t, auditLog)
	if len(lines) != 1 {
		t.Fatalf("a refused call wrote %d forward audit lines, want exactly 1: %v", len(lines), lines)
	}
	checkAuditShape(t, lines[0])
	for field, want := range map[string]any{
		"credential": "",
		"tier":       "",
		"purpose":    PurposeForward,
		"status":     ErrorStateUnavailable,
	} {
		if got := lines[0][field]; got != want {
			t.Errorf("the refusal's audit line %s = %v, want %q", field, got, want)
		}
	}
}
