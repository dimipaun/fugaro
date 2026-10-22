package credsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// auditValue is a fake credential VALUE, built at run time from the test's
// name (Convention 2): no literal here is, or looks like, a credential. It is
// what must never reach an audit line, and it is distinctive enough that a
// line carrying it cannot be read as one that does not.
func auditValue(t *testing.T) string {
	t.Helper()
	return "credsvc-value-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
}

// auditName is a credential's NAME: the only thing about a credential an
// audit line may carry.
const auditName = "shared-one"

// newAudit returns an Auditor writing one JSON line per act into a buffer. The
// auditor is built from the handler, not from a logger (NewAuditor), which is
// how the service builds it too.
//
// An auditor cannot be built without the service's own redaction (NewAuditor),
// so the default here is the scrub that removes nothing: the tests that are
// about the scrub arm a real one, and a test that wanted an unarmed auditor
// cannot have one.
func newAudit(t *testing.T, opts ...AuditOption) (*Auditor, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	a, err := NewAuditor(slog.NewJSONHandler(&buf, nil),
		append([]AuditOption{armed()}, opts...)...)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	return a, &buf
}

// armed is the arming an auditor cannot be built without (NewAuditor): the
// service's own redaction, which in a test that writes names and no values
// removes nothing.
func armed() AuditOption { return WithAuditScrub(func(s string) string { return s }) }

// recordHandler is a slog.Handler that keeps the records it is handed and can
// be made to fail, so a test can read what the auditor built rather than what
// a handler made of it — a record's own time and level are the auditor's, and
// a JSON handler's rendering of them is the JSON handler's.
type recordHandler struct {
	records []slog.Record
	err     error // what Handle returns, for the tests about a log that cannot write
}

func (h *recordHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return h.err
}

func (h *recordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *recordHandler) WithGroup(string) slog.Handler { return h }

// auditLines parses what an auditor wrote: one JSON object per physical line,
// which is what "one structured line" means here — a field carrying a newline
// would have to open a second one, and this would see it.
func auditLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	return parseAuditLines(t, buf.String())
}

// parseAuditLines is auditLines for a test whose audit lines were written to
// somewhere other than a plain buffer — the service fixture's own log buffer,
// which the handler writes to through a mutex (server_test.go).
func parseAuditLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	if raw == "" {
		t.Fatal("the auditor wrote no line")
	}
	if !strings.HasSuffix(raw, "\n") {
		t.Fatalf("the audit line does not end in a newline: %q", raw)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("an audit line is not one JSON object: %v (%q)", err, line)
		}
		out = append(out, m)
	}
	return out
}

// auditFields is the shape of the line: the plan's fields, and no others.
// A field added to the line — which is how a credential's value would reach
// it — fails every test that reads this.
var auditFields = []string{"run", "repository", "job", "caller", "credential", "purpose", "tier", "status"}

// checkAuditShape holds a written line to that shape: every field of the
// contract is there, and nothing else is.
func checkAuditShape(t *testing.T, line map[string]any) {
	t.Helper()
	for _, f := range auditFields {
		if _, ok := line[f]; !ok {
			t.Errorf("the audit line carries no %q field: %v", f, line)
		}
	}
	for k := range line {
		// time, level and msg are the handler's own.
		if k != "time" && k != "level" && k != "msg" && !slices.Contains(auditFields, k) {
			t.Errorf("the audit line carries a field it does not own: %q", k)
		}
	}
}

// TestAuditVocabulary pins the words the line is read by: the message an
// operator greps for the service's acts (the package doc says so), the message
// an act that could not be recorded leaves, and the classes a refusal gives.
// The field names are pinned the same way and as literals (auditFields); these
// are the rest of the vocabulary a check, a runbook or an operator greps, and
// nothing else in the repository pins them — a rename is a change to a contract
// and has to come here and argue for itself.
func TestAuditVocabulary(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{auditMessage, "credsvc: audit"},
		{auditRefusedMessage, "credsvc: audit event refused"},
		{refusalMissing, "missing"},
		{refusalMalformed, "malformed"},
		{refusalUnknown, "unknown"},
		{refusalUnexpected, "unexpected"},
	} {
		if c.got != c.want {
			t.Errorf("the audit vocabulary changed: %q, want %q", c.got, c.want)
		}
	}
}

// TestAuditCarriesNamesOnly is C1.13's red step: an audit line carries the
// names of what the service did — the run, the repository, the job, the
// caller, the credential's name, the purpose, the tier and the status — and
// never a credential's value (F32; Check 94's "the audit log carries names,
// never values").
func TestAuditCarriesNamesOnly(t *testing.T) {
	f := newFixture(t)
	id := f.verifyOK()
	value := auditValue(t)

	t.Run("an issuance carries the name and not the value", func(t *testing.T) {
		a, buf := newAudit(t)
		for _, purpose := range []string{PurposeForward, PurposeProbe} {
			buf.Reset()
			if err := a.Write(context.Background(), id.Audit(AuditEvent{Credential: auditName, Tier: TierA, Purpose: purpose, Status: StatusOK})); err != nil {
				t.Fatalf("Write(%s): %v", purpose, err)
			}
			lines := auditLines(t, buf)
			if len(lines) != 1 {
				t.Fatalf("Write(%s) wrote %d lines, want one", purpose, len(lines))
			}
			checkAuditShape(t, lines[0])
			for field, want := range map[string]string{
				"run":        f.run,
				"repository": f.slug,
				"job":        f.job,
				"caller":     testJobSA,
				"credential": auditName,
				"purpose":    purpose,
				"tier":       TierA,
				"status":     StatusOK,
			} {
				if got := lines[0][field]; got != want {
					t.Errorf("Write(%s): %s = %v, want %q", purpose, field, got, want)
				}
			}
			// That the line carries no credential VALUE is not asserted here:
			// this event was given a name, so an assertion that the value is
			// absent could not fail. It is asserted where the value is actually
			// present — the scrub subtests below.
		}
	})

	t.Run("a mint names the App key and carries no tier", func(t *testing.T) {
		// The mint's credential is the App key in the bundle (C2.1), and the
		// App key is not a model credential of the cascade: it is of no tier.
		// So the shape a mint is audited in is this one — the key's name and no
		// tier — and C2.1's TestMintAudited writes it without inventing a tier
		// the key does not have. The name is
		// config.GitHubAppKeySecret's and not a literal here: it is the name the
		// service will actually write, and this test is about that mint.
		a, buf := newAudit(t)
		if err := a.Write(context.Background(), id.Audit(AuditEvent{Credential: config.GitHubAppKeySecret, Purpose: PurposeMint, Status: StatusOK})); err != nil {
			t.Fatalf("Write(mint): %v", err)
		}
		line := auditLines(t, buf)[0]
		checkAuditShape(t, line)
		for field, want := range map[string]string{
			"run":        f.run,
			"repository": f.slug,
			"job":        f.job,
			"caller":     testJobSA,
			"credential": config.GitHubAppKeySecret,
			"purpose":    PurposeMint,
			"tier":       "",
			"status":     StatusOK,
		} {
			if got := line[field]; got != want {
				t.Errorf("the mint's line: %s = %v, want %q", field, got, want)
			}
		}
	})

	t.Run("the scrub is what keeps a value out when a caller hands one", func(t *testing.T) {
		// A caller that hands the credential's VALUE where its name belongs is
		// the mistake the scrub exists for — redactWriter's rule in the
		// gateway, extended to this line (F32). The auditor is armed with the
		// service's own redaction, so the value reaches neither the line nor
		// the error a refused line returns.
		const marker = "[redacted]"
		a, buf := newAudit(t, WithAuditScrub(func(s string) string {
			return strings.ReplaceAll(s, value, marker)
		}))
		if err := a.Write(context.Background(), id.Audit(AuditEvent{Credential: value, Tier: TierA, Purpose: PurposeForward, Status: StatusOK})); err != nil {
			t.Fatalf("Write: %v", err)
		}
		raw := buf.String()
		if strings.Contains(raw, value) {
			t.Fatalf("the audit line carries the credential's value: %s", raw)
		}
		if got := auditLines(t, buf)[0]["credential"]; got != marker {
			t.Errorf("credential = %v, want the marker %q", got, marker)
		}
	})

	t.Run("the scrub reaches every field, not only the credential", func(t *testing.T) {
		// The promise is that the service's redaction is applied to EVERY field
		// before any rule looks at one, so a value a caller hands reaches
		// neither the line nor the refusal's error. Each case hands the value
		// where that field's own value belongs and arms a scrub that puts a
		// substitute the field may hold in its place: the line is written and
		// carries the substitute, so the substitute's presence is what says the
		// scrub ran on that field.
		//
		// Every case fails if the scrub skips its field: the value is left
		// there, and it is either written (run, repository, credential, and the
		// caller, which is one clean header value too) or refused, which the
		// error assertion catches. That is why the substitutes are valid ones
		// rather than a marker: a marker would make the refusal the expected
		// outcome and hide the difference.
		for _, c := range []struct {
			field string
			set   func(*AuditEvent, string)
			repl  string // what the scrub puts in the field's place: a value it may hold
		}{
			{"run", func(ev *AuditEvent, v string) { ev.Run = v }, "scrubbed-run"},
			{"repository", func(ev *AuditEvent, v string) { ev.Repository = v }, "scrubbed-repo"},
			{"job", func(ev *AuditEvent, v string) { ev.Job = v }, "scrubbed-job"},
			{"caller", func(ev *AuditEvent, v string) { ev.Caller = v }, "scrubbed@example.invalid"},
			{"credential", func(ev *AuditEvent, v string) { ev.Credential = v }, "scrubbed-credential"},
			{"tier", func(ev *AuditEvent, v string) { ev.Tier = v }, TierA},
			{"purpose", func(ev *AuditEvent, v string) { ev.Purpose = v }, PurposeForward},
			{"status", func(ev *AuditEvent, v string) { ev.Status = v }, StatusOK},
		} {
			t.Run(c.field, func(t *testing.T) {
				value := auditValue(t)
				a, buf := newAudit(t, WithAuditScrub(func(s string) string {
					return strings.ReplaceAll(s, value, c.repl)
				}))
				ev := id.Audit(AuditEvent{Credential: auditName, Tier: TierA, Purpose: PurposeForward, Status: StatusOK})
				c.set(&ev, value)
				if err := a.Write(context.Background(), ev); err != nil {
					t.Fatalf("Write with the scrub armed: %v", err)
				}
				raw := buf.String()
				if strings.Contains(raw, value) {
					t.Fatalf("the audit line carries the credential's value: %s", raw)
				}
				if got := auditLines(t, buf)[0][c.field]; got != c.repl {
					t.Errorf("%s = %v, want the scrub's %q: the scrub did not reach this field", c.field, got, c.repl)
				}
			})
		}
	})

	t.Run("a field that cannot be one is refused, and the refusal names it and not what it held", func(t *testing.T) {
		// Every field of the line is held to the rule the rest of the package
		// already holds it to. The table is the rules: a field that cannot be
		// one is refused, the refusal names the field, and neither the line nor
		// the error repeats what the field held.
		full := func(mutate func(*AuditEvent)) AuditEvent {
			ev := id.Audit(AuditEvent{Credential: auditName, Tier: TierA, Purpose: PurposeForward, Status: StatusOK})
			mutate(&ev)
			return ev
		}
		for _, c := range []struct {
			name    string
			ev      AuditEvent
			field   string
			reason  string // the class of thing wrong: missing, malformed or unknown
			content string // what the field held, which no line and no error may carry
		}{
			{"a credential name that is not one header value", full(func(ev *AuditEvent) { ev.Credential = value + " " + value }), "credential", refusalMalformed, value},
			{"a credential with no tier", full(func(ev *AuditEvent) { ev.Tier = "" }), "credential", refusalMissing, ""},
			{"a tier with no credential", full(func(ev *AuditEvent) { ev.Credential = "" }), "credential", refusalMissing, ""},
			{"a tier the contract does not carry", full(func(ev *AuditEvent) { ev.Tier = "D" }), "tier", refusalUnknown, "D"},
			// A mint's credential is the App key, which is not a model
			// credential and has no tier: a tier on a mint is a fact the service
			// does not have, so it is refused rather than recorded, and the
			// class says the field does not belong to this kind of event rather
			// than that its value is not a tier. (No content assertion: the
			// value is one letter, and the line's own level carries it.)
			{"a tier on a mint, whose credential has none", id.Audit(AuditEvent{Credential: config.GitHubAppKeySecret, Tier: TierA, Purpose: PurposeMint, Status: StatusOK}), "tier", refusalUnexpected, ""},
			{"a run id that is not a path segment", full(func(ev *AuditEvent) { ev.Run = value + "/" + value }), "run", refusalMalformed, value},
			{"a run id with no repository", full(func(ev *AuditEvent) { ev.Repository = "" }), "run", refusalMissing, ""},
			// The repository's own guards, which the run's do not stand in for:
			// a slug is held to the same segment rule and escaped by the same
			// safetext call, and neither is pinned by a case that only moves Run.
			{"a repository slug that is not a path segment", full(func(ev *AuditEvent) { ev.Repository = value + "/" + value }), "repository", refusalMalformed, value},
			{"a repository with no run", full(func(ev *AuditEvent) { ev.Run = "" }), "run", refusalMissing, ""},
			{"a job name that is not one", full(func(ev *AuditEvent) { ev.Job = "not a job name" }), "job", refusalMalformed, "not a job name"},
			{"a caller that is not an address", full(func(ev *AuditEvent) { ev.Caller = "not an address" }), "caller", refusalMalformed, "not an address"},
			{"a job with no caller", full(func(ev *AuditEvent) { ev.Caller = "" }), "job", refusalMissing, ""},
			{"an issuance that names no credential", full(func(ev *AuditEvent) { ev.Credential, ev.Tier = "", "" }), "credential", refusalMissing, ""},
			{"an issuance with no purpose", full(func(ev *AuditEvent) { ev.Purpose = "" }), "purpose", refusalMissing, ""},
			{"an issuance with no run", full(func(ev *AuditEvent) { ev.Run, ev.Repository = "", "" }), "run", refusalMissing, ""},
			{"an issuance with no job", full(func(ev *AuditEvent) { ev.Job, ev.Caller = "", "" }), "job", refusalMissing, ""},
		} {
			t.Run(c.name, func(t *testing.T) {
				a, buf := newAudit(t)
				err := a.Write(context.Background(), c.ev)
				if err == nil {
					t.Fatalf("Write accepted %s", c.name)
				}
				if c.content != "" && strings.Contains(err.Error(), c.content) {
					t.Errorf("the refusal repeats what the field held: %v", err)
				}
				raw := buf.String()
				if c.content != "" && strings.Contains(raw, c.content) {
					t.Errorf("the audit line carries what the field held: %s", raw)
				}
				lines := auditLines(t, buf)
				if len(lines) != 1 {
					t.Fatalf("a refused event wrote %d lines, want the one refusal", len(lines))
				}
				if lines[0]["field"] != c.field {
					t.Errorf("the refusal names field %v, want %q", lines[0]["field"], c.field)
				}
				// The class is what tells an operator reading the line whether
				// the service was handed nothing, something of the wrong shape,
				// or something out of a set it owns — without the line
				// repeating what it was handed.
				if lines[0]["reason"] != c.reason {
					t.Errorf("the refusal gives reason %v, want %q", lines[0]["reason"], c.reason)
				}
			})
		}
	})

	t.Run("the verified identity is the line's who, whatever the caller put there", func(t *testing.T) {
		// Identity.Audit completes an event with what auth.go verified. A
		// caller that filled those fields in itself — or with its own text —
		// does not get them written: the line's "who" is the verified one.
		a, buf := newAudit(t)
		ev := id.Audit(AuditEvent{
			Run:        "another-run",
			Repository: "another-repo",
			Job:        "another-job",
			Caller:     "someone-else@example.invalid",
			Credential: auditName,
			Tier:       TierA,
			Purpose:    PurposeForward,
			Status:     StatusOK,
		})
		if err := a.Write(context.Background(), ev); err != nil {
			t.Fatalf("Write: %v", err)
		}
		line := auditLines(t, buf)[0]
		for field, want := range map[string]string{
			"run":        f.run,
			"repository": f.slug,
			"job":        f.job,
			"caller":     testJobSA,
		} {
			if got := line[field]; got != want {
				t.Errorf("%s = %v, want the verified %q", field, got, want)
			}
		}
	})

	t.Run("the line is the contract's fields whatever the service's logger carries", func(t *testing.T) {
		// The auditor is built from the handler, not from a logger, and this is
		// why: a logger is a handler plus what it was built with — Logger.With
		// and Logger.WithGroup apply to the handler Logger.Handler hands back —
		// so an auditor built from the service's logger would write the
		// service's attrs on every audit line, and, under a group, would nest
		// the contract's own eight fields where a reader does not look for them.
		//
		// The service's logger is built with both here, and writes its own line
		// first: that line is what proves the attrs and the group are real on
		// this logger, so the audit line's lack of them is the auditor's doing
		// and not the logger's.
		var buf bytes.Buffer
		h := slog.NewJSONHandler(&buf, nil)
		svcLog := slog.New(h).With("service", "fugaro-credentials").WithGroup("credsvc")
		svcLog.Info("the service's own line")

		a, err := NewAuditor(h, armed())
		if err != nil {
			t.Fatalf("NewAuditor: %v", err)
		}
		if err := a.Write(context.Background(), id.Audit(AuditEvent{Credential: auditName, Tier: TierA, Purpose: PurposeForward, Status: StatusOK})); err != nil {
			t.Fatalf("Write: %v", err)
		}
		lines := auditLines(t, &buf)
		if len(lines) != 2 {
			t.Fatalf("the buffer holds %d lines, want the service's and the audit's", len(lines))
		}
		if _, ok := lines[0]["service"]; !ok {
			t.Fatalf("the service's own line does not carry its attrs: %v", lines[0])
		}
		checkAuditShape(t, lines[1])
	})

	t.Run("a name that carries a bidi control is escaped, not carried", func(t *testing.T) {
		// A run id and a repository slug are held to token.ValidSegment, which
		// refuses '~', '/' and controls — and a bidi control is neither, so it
		// passes that rule and reaches this line. internal/safetext is what
		// stops it reaching the terminal (F32: the names that travel are text
		// from another writer). Both fields are checked: the two take the same
		// rule and the same escaping, and neither call stands in for the other.
		const rlo = "‮"
		for _, c := range []struct {
			field string
			set   func(*AuditEvent, string)
			raw   string
		}{
			{"run", func(ev *AuditEvent, v string) { ev.Run = v }, f.run + rlo + "gpj.exe"},
			{"repository", func(ev *AuditEvent, v string) { ev.Repository = v }, f.slug + rlo + "gpj"},
		} {
			t.Run(c.field, func(t *testing.T) {
				a, buf := newAudit(t)
				ev := id.Audit(AuditEvent{Credential: auditName, Tier: TierA, Purpose: PurposeForward, Status: StatusOK})
				c.set(&ev, c.raw)
				if err := a.Write(context.Background(), ev); err != nil {
					t.Fatalf("Write: %v", err)
				}
				if strings.Contains(buf.String(), rlo) {
					t.Errorf("the audit line carries a bidi control: %q", buf.String())
				}
				if got := auditLines(t, buf)[0][c.field]; got == c.raw {
					t.Errorf("%s = %q, want the control escaped", c.field, got)
				}
			})
		}
	})
}

// TestAuditFailsClosed: an act the service could not record is not recorded,
// and the caller is told — the fail-closed half of the file's contract (D143).
// It is a third test beside the plan's two because the plan's two are about
// what a line carries and this one is about whether there is a line at all:
// slog's own LogAttrs drops a record silently when the handler's level
// excludes it and returns no error when the handler fails, so a Write built on
// it would answer "audited" for an act that left nothing behind.
func TestAuditFailsClosed(t *testing.T) {
	f := newFixture(t)
	id := f.verifyOK()
	ok := func() AuditEvent {
		return id.Audit(AuditEvent{Credential: auditName, Tier: TierA, Purpose: PurposeForward, Status: StatusOK})
	}

	t.Run("a log that does not record the line's level is refused", func(t *testing.T) {
		var buf bytes.Buffer
		a, err := NewAuditor(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}), armed())
		if err != nil {
			t.Fatalf("NewAuditor: %v", err)
		}
		if err := a.Write(context.Background(), ok()); err == nil {
			t.Fatal("Write reported an issuance it did not write: the handler does not record at the line's level")
		}
		if buf.Len() != 0 {
			t.Errorf("Write wrote a line after all: %s", buf.String())
		}
	})

	t.Run("a handler that cannot write is refused", func(t *testing.T) {
		want := errors.New("the disk is full")
		h := &recordHandler{err: want}
		a, err := NewAuditor(h, armed())
		if err != nil {
			t.Fatalf("NewAuditor: %v", err)
		}
		err = a.Write(context.Background(), ok())
		if err == nil {
			t.Fatal("Write reported an issuance the handler could not write")
		}
		if !errors.Is(err, want) {
			t.Errorf("the handler's own error is not in Write's: %v", err)
		}
	})

	t.Run("a refused event is an error whether or not its line could be written", func(t *testing.T) {
		// The refusal is the caller's answer and the refusal line is the
		// operator's: a log that records neither must not turn a refusal into a
		// silent success, and a handler that fails must not be silent either.
		bad := ok()
		bad.Run, bad.Repository = "", ""

		var buf bytes.Buffer
		quiet, err := NewAuditor(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}), armed())
		if err != nil {
			t.Fatalf("NewAuditor: %v", err)
		}
		if err := quiet.Write(context.Background(), bad); err == nil {
			t.Fatal("Write accepted an issuance that names no run")
		}

		want := errors.New("the disk is full")
		failing, err := NewAuditor(&recordHandler{err: want}, armed())
		if err != nil {
			t.Fatalf("NewAuditor: %v", err)
		}
		err = failing.Write(context.Background(), bad)
		if err == nil {
			t.Fatal("Write accepted an issuance that names no run")
		}
		if !strings.Contains(err.Error(), "run") {
			t.Errorf("the refusal is not in the error: %v", err)
		}
		if !errors.Is(err, want) {
			t.Errorf("the handler's own error was dropped from the refusal's: %v", err)
		}
	})

	t.Run("the line's time is UTC", func(t *testing.T) {
		// A line is read beside lines from other hosts and other zones, so its
		// time is the record's and not the host's. The check is on the record
		// rather than on a JSON handler's rendering of it: a host whose local
		// zone is UTC would render a local time exactly as it renders this one.
		h := &recordHandler{}
		a, err := NewAuditor(h, armed())
		if err != nil {
			t.Fatalf("NewAuditor: %v", err)
		}
		if err := a.Write(context.Background(), ok()); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if len(h.records) != 1 {
			t.Fatalf("the handler was handed %d records, want one", len(h.records))
		}
		if got := h.records[0].Time.Location(); got != time.UTC {
			t.Errorf("the line's time is in %v, want UTC", got)
		}
		if got := h.records[0].Level; got != slog.LevelInfo {
			t.Errorf("the line's level is %v, want %v", got, slog.LevelInfo)
		}
		if got := h.records[0].Message; got != auditMessage {
			t.Errorf("the line's message is %q, want %q", got, auditMessage)
		}
	})

	t.Run("an auditor that was not armed with the service's redaction is refused", func(t *testing.T) {
		// The premise first, asserted: a credential's value is one clean header
		// value, so ValidCredentialName accepts it — no field rule refuses a
		// caller that hands a value where a name belongs. That is the threat,
		// and arming the auditor is the plan's answer to it (F32's redactWriter
		// rule, C1.11's loader holding the values and doing the redacting). So
		// an auditor that was not armed is one this service must not have: a
		// caller that forgets gets no auditor, and so issues nothing (D143),
		// rather than one that writes the value it was handed.
		if value := auditValue(t); !ValidCredentialName(value) {
			t.Fatalf("the fake value is not credential-shaped, so this test no longer states the threat: %q", value)
		}
		if _, err := NewAuditor(&recordHandler{}); err == nil {
			t.Fatal("NewAuditor returned an unarmed auditor: it would write a credential's value into the line")
		}
		if _, err := NewAuditor(&recordHandler{}, WithAuditScrub(nil)); err == nil {
			t.Fatal("WithAuditScrub(nil) armed the auditor with nothing, and NewAuditor accepted it")
		}
	})

	t.Run("no handler is refused rather than dropped", func(t *testing.T) {
		if _, err := NewAuditor(nil, WithAuditScrub(func(s string) string { return s })); err == nil {
			t.Fatal("NewAuditor accepted a nil handler: an issuance it could not record would be silent")
		}
	})
}

// TestAuditOnRefusal: a refusal is audited too, with what was known when the
// service refused and nothing invented.
func TestAuditOnRefusal(t *testing.T) {
	f := newFixture(t)
	id := f.verifyOK()

	t.Run("a call refused before the identity was verified records no identity", func(t *testing.T) {
		a, buf := newAudit(t)
		if err := a.Write(context.Background(), AuditEvent{Status: ErrorUnverifiedJobIdentity}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		line := auditLines(t, buf)[0]
		checkAuditShape(t, line)
		if line["status"] != ErrorUnverifiedJobIdentity {
			t.Errorf("status = %v, want %q", line["status"], ErrorUnverifiedJobIdentity)
		}
		for _, field := range []string{"run", "repository", "job", "caller", "credential", "tier", "purpose"} {
			if line[field] != "" {
				t.Errorf("a refusal before verification recorded %s = %v; nothing about the caller may be recorded", field, line[field])
			}
		}
	})

	t.Run("a refused run records the run, the repository and the credential it was about", func(t *testing.T) {
		a, buf := newAudit(t)
		if err := a.Write(context.Background(), id.Audit(AuditEvent{Credential: auditName, Tier: TierB, Purpose: PurposeForward, Status: ReasonStopped})); err != nil {
			t.Fatalf("Write: %v", err)
		}
		line := auditLines(t, buf)[0]
		checkAuditShape(t, line)
		for field, want := range map[string]string{
			"run":        f.run,
			"repository": f.slug,
			"job":        f.job,
			"caller":     testJobSA,
			"credential": auditName,
			"purpose":    PurposeForward,
			"tier":       TierB,
			"status":     ReasonStopped,
		} {
			if got := line[field]; got != want {
				t.Errorf("%s = %v, want %q", field, got, want)
			}
		}
	})

	t.Run("a status the contract does not know is refused, and not repeated", func(t *testing.T) {
		a, buf := newAudit(t)
		const invented = "spent-the-cap"
		if err := a.Write(context.Background(), id.Audit(AuditEvent{Credential: auditName, Tier: TierA, Purpose: PurposeForward, Status: invented})); err == nil {
			t.Fatal("Write accepted a status the contract does not carry")
		}
		if strings.Contains(buf.String(), invented) {
			t.Errorf("the refusal repeats the status it refused: %s", buf.String())
		}
		if got := auditLines(t, buf)[0]["field"]; got != "status" {
			t.Errorf("the refusal names field %v, want status", got)
		}
		if got := auditLines(t, buf)[0]["reason"]; got != refusalUnknown {
			t.Errorf("the refusal gives reason %v, want %q", got, refusalUnknown)
		}
	})

	t.Run("a purpose the service does not issue for is refused", func(t *testing.T) {
		a, buf := newAudit(t)
		if err := a.Write(context.Background(), id.Audit(AuditEvent{Credential: auditName, Tier: TierA, Purpose: "whatever", Status: StatusOK})); err == nil {
			t.Fatal("Write accepted a purpose the service does not have")
		}
		if got := auditLines(t, buf)[0]["field"]; got != "purpose" {
			t.Errorf("the refusal names field %v, want purpose", got)
		}
		if got := auditLines(t, buf)[0]["reason"]; got != refusalUnknown {
			t.Errorf("the refusal gives reason %v, want %q", got, refusalUnknown)
		}
	})
}
