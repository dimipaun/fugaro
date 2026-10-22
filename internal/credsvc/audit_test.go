package credsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
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

// newAudit returns an Auditor writing one JSON line per act into a buffer.
func newAudit(t *testing.T, opts ...AuditOption) (*Auditor, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	a, err := NewAuditor(slog.New(slog.NewJSONHandler(&buf, nil)), opts...)
	if err != nil {
		t.Fatalf("NewAuditor: %v", err)
	}
	return a, &buf
}

// auditLines parses what an auditor wrote: one JSON object per physical line,
// which is what "one structured line" means here — a field carrying a newline
// would have to open a second one, and this would see it.
func auditLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	raw := buf.String()
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
		switch k {
		case "time", "level", "msg":
			continue
		}
		owned := false
		for _, f := range auditFields {
			owned = owned || k == f
		}
		if !owned {
			t.Errorf("the audit line carries a field it does not own: %q", k)
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
		for _, purpose := range []string{PurposeForward, PurposeProbe, PurposeMint} {
			buf.Reset()
			if err := a.Write(context.Background(), id.Audit(purpose, auditName, TierA, StatusOK)); err != nil {
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
			if strings.Contains(buf.String(), value) {
				t.Errorf("Write(%s): the audit line carries the credential's value: %s", purpose, buf.String())
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
		if err := a.Write(context.Background(), id.Audit(PurposeForward, value, TierA, StatusOK)); err != nil {
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

	t.Run("a field that cannot be one is refused, and the refusal names it and not what it held", func(t *testing.T) {
		// Every field of the line is held to the rule the rest of the package
		// already holds it to. The table is the rules: a field that cannot be
		// one is refused, the refusal names the field, and neither the line nor
		// the error repeats what the field held.
		full := func(mutate func(*AuditEvent)) AuditEvent {
			ev := id.Audit(PurposeForward, auditName, TierA, StatusOK)
			mutate(&ev)
			return ev
		}
		for _, c := range []struct {
			name    string
			ev      AuditEvent
			field   string
			content string // what the field held, which no line and no error may carry
		}{
			{"a credential name that is not one header value", full(func(ev *AuditEvent) { ev.Credential = value + " " + value }), "credential", value},
			{"a credential with no tier", full(func(ev *AuditEvent) { ev.Tier = "" }), "credential", ""},
			{"a tier with no credential", full(func(ev *AuditEvent) { ev.Credential = "" }), "credential", ""},
			{"a tier the contract does not carry", full(func(ev *AuditEvent) { ev.Tier = "D" }), "tier", ""},
			{"a run id that is not a path segment", full(func(ev *AuditEvent) { ev.Run = value + "/" + value }), "run", value},
			{"a run id with no repository", full(func(ev *AuditEvent) { ev.Repository = "" }), "run", ""},
			{"a job name that is not one", full(func(ev *AuditEvent) { ev.Job = "not a job name" }), "job", ""},
			{"a caller that is not an address", full(func(ev *AuditEvent) { ev.Caller = "not an address" }), "caller", ""},
			{"a job with no caller", full(func(ev *AuditEvent) { ev.Caller = "" }), "job", ""},
			{"an issuance that names no credential", full(func(ev *AuditEvent) { ev.Credential, ev.Tier = "", "" }), "credential", ""},
			{"an issuance with no purpose", full(func(ev *AuditEvent) { ev.Purpose = "" }), "purpose", ""},
			{"an issuance with no run", full(func(ev *AuditEvent) { ev.Run, ev.Repository = "", "" }), "run", ""},
			{"an issuance with no job", full(func(ev *AuditEvent) { ev.Job, ev.Caller = "", "" }), "job", ""},
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
			})
		}
	})

	t.Run("a name that carries a bidi control is escaped, not carried", func(t *testing.T) {
		// A slug is held to token.ValidSegment, which refuses '~', '/' and
		// controls — and a bidi control is neither, so it passes that rule and
		// reaches this line. internal/safetext is what stops it reaching the
		// terminal (F32: the names that travel are text from another writer).
		const rlo = "‮"
		a, buf := newAudit(t)
		ev := id.Audit(PurposeForward, auditName, TierA, StatusOK)
		ev.Run = f.run + rlo + "gpj.exe"
		if err := a.Write(context.Background(), ev); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if strings.Contains(buf.String(), rlo) {
			t.Errorf("the audit line carries a bidi control: %q", buf.String())
		}
		if got := auditLines(t, buf)[0]["run"]; got == f.run+rlo+"gpj.exe" {
			t.Errorf("run = %q, want the control escaped", got)
		}
	})

	t.Run("no logger is refused rather than dropped", func(t *testing.T) {
		if _, err := NewAuditor(nil); err == nil {
			t.Fatal("NewAuditor accepted a nil logger: an issuance it could not record would be silent")
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
		if err := a.Write(context.Background(), id.Audit(PurposeForward, auditName, TierB, ReasonStopped)); err != nil {
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
		if err := a.Write(context.Background(), id.Audit(PurposeForward, auditName, TierA, invented)); err == nil {
			t.Fatal("Write accepted a status the contract does not carry")
		}
		if strings.Contains(buf.String(), invented) {
			t.Errorf("the refusal repeats the status it refused: %s", buf.String())
		}
		if got := auditLines(t, buf)[0]["field"]; got != "status" {
			t.Errorf("the refusal names field %v, want status", got)
		}
	})

	t.Run("a purpose the service does not issue for is refused", func(t *testing.T) {
		a, buf := newAudit(t)
		if err := a.Write(context.Background(), id.Audit("whatever", auditName, TierA, StatusOK)); err == nil {
			t.Fatal("Write accepted a purpose the service does not have")
		}
		if got := auditLines(t, buf)[0]["field"]; got != "purpose" {
			t.Errorf("the refusal names field %v, want purpose", got)
		}
	})
}
