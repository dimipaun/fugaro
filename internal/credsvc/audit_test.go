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

	t.Run("a field that cannot be a name is refused, and the refusal does not repeat it", func(t *testing.T) {
		a, buf := newAudit(t)
		// A space: not one header value, so not a credential name.
		err := a.Write(context.Background(), id.Audit(PurposeForward, value+" "+value, TierA, StatusOK))
		if err == nil {
			t.Fatal("Write accepted a credential field that is not a name")
		}
		if strings.Contains(err.Error(), value) {
			t.Errorf("the refusal repeats what the field held: %v", err)
		}
		raw := buf.String()
		if strings.Contains(raw, value) {
			t.Errorf("the audit line carries what the field held: %s", raw)
		}
		lines := auditLines(t, buf)
		if len(lines) != 1 {
			t.Fatalf("a refused event wrote %d lines, want the one refusal", len(lines))
		}
		if lines[0]["field"] != "credential" {
			t.Errorf("the refusal names field %v, want credential", lines[0]["field"])
		}
	})

	t.Run("an issuance that names no credential is refused", func(t *testing.T) {
		// StatusOK says a credential answered, so the line that says it names
		// the credential, its tier and what it was for.
		a, _ := newAudit(t)
		if err := a.Write(context.Background(), id.Audit(PurposeForward, "", "", StatusOK)); err == nil {
			t.Fatal("Write accepted an issuance that names no credential")
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
