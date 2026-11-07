package redact

import (
	"strings"
	"testing"
	"time"
)

// fake joins the pieces of a fixture. Nothing here is written whole: a test
// file in this repository must not carry a secret-shaped literal, and a
// pattern pass is tested on shapes, not on values.
func fake(parts ...string) string { return strings.Join(parts, "") }

// patternCases are what the exact pass cannot know: credentials Fugaro never
// held, printed by something else — a failing cloud call, a provider's error
// body, a signed URL in a message, a key block in a Terraform stream.
var patternCases = []struct {
	name string
	in   string
	leak string
	keep string
}{
	{
		name: "anthropic key",
		in:   "api error: " + fake("sk-", "ant-api03-", strings.Repeat("Qw3Z", 8)) + " was rejected",
		leak: fake("sk-", "ant-api03-"),
		keep: "was rejected",
	},
	{
		name: "openai key",
		in:   "curl: " + fake("sk-", "proj-", strings.Repeat("Ab3d", 7)),
		leak: fake("sk-", "proj-"),
	},
	{
		name: "stripe key",
		in:   "charge refused: " + fake("sk_", "live_", strings.Repeat("Aa1", 8)),
		leak: fake("sk_", "live_"),
	},
	{
		name: "github token",
		in:   "remote: " + fake("ghp_", strings.Repeat("A1b2C3d4", 5)) + " is not authorised",
		leak: fake("ghp_"),
		keep: "is not authorised",
	},
	{
		name: "github fine-grained token",
		in:   fake("github_pat_", strings.Repeat("A1b2C3d4e5", 3)),
		leak: fake("github_pat_"),
	},
	{
		name: "gitlab token",
		in:   fake("glpat-", strings.Repeat("Xy9", 8)),
		leak: fake("glpat-"),
	},
	{
		name: "slack token",
		in:   fake("xoxb-", "123456789012-", "abcdefghijklmnop"),
		leak: fake("xoxb-"),
	},
	{
		name: "aws access key id",
		in:   "SignatureDoesNotMatch for " + fake("AKIA", strings.Repeat("QWERTYUI", 2)),
		leak: fake("AKIA"),
	},
	{
		name: "google api key",
		in:   "maps refused: " + fake("AIza", strings.Repeat("SyD", 12)),
		leak: fake("AIza"),
	},
	{
		name: "google oauth token",
		in:   fake("ya29.", strings.Repeat("0Ab-", 6)),
		leak: fake("ya29."),
	},
	{
		name: "jwt",
		in:   "answer: " + fake("eyJ", "hbGciOiJIUzI1NiJ9.", "eyJzdWIiOiIxIn0.", "c2lnbmF0dXJlMTIz") + " expired",
		leak: fake("eyJ", "hbGciOiJIUzI1NiJ9."),
		keep: "expired",
	},
	{
		name: "authorization header",
		in:   "Authorization: Bearer " + fake(strings.Repeat("A1b2C3d4", 3)) + "\nnext line",
		leak: strings.Repeat("A1b2C3d4", 3),
		keep: "next line",
	},
	{
		name: "lowercase proxy header",
		in:   "proxy-authorization: basic " + fake(strings.Repeat("dXNlcjpw", 3)) + "\n",
		leak: strings.Repeat("dXNlcjpw", 3),
	},
	{
		name: "api key header",
		in:   "x-api-key: " + fake(strings.Repeat("Zx9", 8)) + "\n",
		leak: strings.Repeat("Zx9", 8),
	},
	{
		name: "header named inside a json object",
		in:   `{"x-api-key": "` + fake(strings.Repeat("Zx9", 8)) + `"}`,
		leak: strings.Repeat("Zx9", 8),
	},
	{
		name: "bearer without a header name",
		in:   "retrying with Bearer " + fake(strings.Repeat("A1b2C3d4", 3)) + " now",
		leak: strings.Repeat("A1b2C3d4", 3),
		keep: "now",
	},
	{
		name: "signed url",
		in:   "https://b.s3.amazonaws.com/o?X-Amz-Signature=" + fake(strings.Repeat("ab", 16)) + "&X-Amz-Expires=3600",
		leak: strings.Repeat("ab", 16),
		keep: "X-Amz-Expires=3600",
	},
	{
		name: "google signed url",
		in:   "https://storage.googleapis.com/o?X-Goog-Signature=" + fake(strings.Repeat("cd", 16)) + "&X-Goog-Expires=60",
		leak: strings.Repeat("cd", 16),
		keep: "X-Goog-Expires=60",
	},
	{
		name: "token query parameter",
		in:   "https://host/cb?access_token=" + fake(strings.Repeat("ef", 16)) + "&state=ok",
		leak: strings.Repeat("ef", 16),
		keep: "state=ok",
	},
	{
		name: "private key block",
		in: fake("-----BEGIN ", "RSA PRIVATE KEY-----\n",
			strings.Repeat("b2R5LW1hdGVyaWFs", 4), "\n-----END ", "RSA PRIVATE KEY-----") + "\nafter",
		leak: strings.Repeat("b2R5LW1hdGVyaWFs", 4),
		keep: "after",
	},
	{
		name: "openssh key block",
		in:   fake("-----BEGIN ", "OPENSSH PRIVATE KEY-----\n", "c2VjcmV0", "\n-----END ", "OPENSSH PRIVATE KEY-----"),
		leak: "c2VjcmV0",
	},
	{
		name: "unterminated key block",
		in:   fake("-----BEGIN ", "PRIVATE KEY-----\n", "dW50ZXJtaW5hdGVk"),
		leak: "dW50ZXJtaW5hdGVk",
	},
}

// TestTextRedactsPatterns: a shape the exact pass cannot name is still a
// credential, and Text removes it while leaving the rest of the line alone.
func TestTextRedactsPatterns(t *testing.T) {
	for _, tc := range patternCases {
		t.Run(tc.name, func(t *testing.T) {
			got := Text(tc.in, nil)
			if strings.Contains(got, tc.leak) {
				t.Errorf("Text(%q) = %q: %q survived the pattern pass", tc.in, got, tc.leak)
			}
			if !strings.Contains(got, "[REDACTED]") {
				t.Errorf("Text(%q) = %q: nothing was redacted", tc.in, got)
			}
			if tc.keep != "" && !strings.Contains(got, tc.keep) {
				t.Errorf("Text(%q) = %q: the pattern pass ate %q as well", tc.in, got, tc.keep)
			}
		})
	}
}

// TestTextIsBothPasses: Text is the exact pass and then the pattern pass, so
// one string holding a value Fugaro holds and one it does not loses both.
func TestTextIsBothPasses(t *testing.T) {
	known := fake("known-", "value-0123456789")
	shaped := fake("ghp_", strings.Repeat("A1b2C3d4", 5))
	got := Text("line "+known+" and "+shaped+" end", []string{known})
	if strings.Contains(got, known) || strings.Contains(got, shaped) {
		t.Errorf("Text = %q: one of the two passes did not run", got)
	}
	if got != "line [REDACTED] and [REDACTED] end" {
		t.Errorf("Text = %q, want both matches replaced", got)
	}
}

// TestReportMasksThePerson: the issue report is the one output that must not
// carry the address or the home directory of whoever filed it.
func TestReportMasksThePerson(t *testing.T) {
	for _, tc := range []struct{ name, in, leak string }{
		{"email", "filed by " + fake("dimi", "@", "example.com") + " today", fake("dimi", "@", "example.com")},
		{"home path", "at " + fake("/home/", "someone", "/.config/fugaro/fugaro.yaml") + " the file is", fake("/home/", "someone")},
		{"mac home path", "at " + fake("/Users/", "someone", "/Library/logs") + " it is", fake("/Users/", "someone")},
		{"root home", "reading " + fake("/root", "/.config/fugaro") + " now", fake("/root", "/.config/fugaro")},
		{"windows home path", "at " + fake(`C:\\Users\\`, "someone", `\\AppData`) + " it is", fake(`C:\\Users\\`, "someone")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Report(tc.in)
			if strings.Contains(got, tc.leak) {
				t.Errorf("Report(%q) = %q: %q survived", tc.in, got, tc.leak)
			}
		})
	}
}

// TestReportAlsoRedactsPatterns: Report is the last pass over the issue text,
// so a credential the model wrote into it does not reach the file either.
func TestReportAlsoRedactsPatterns(t *testing.T) {
	shaped := fake("ghp_", strings.Repeat("A1b2C3d4", 5))
	if got := Report("token " + shaped); strings.Contains(got, shaped) {
		t.Errorf("Report(%q) = %q: the token survived", shaped, got)
	}
}

// TestPassesLeaveCleanTextAlone: a diagnosis is mostly ordinary words, and a
// pass that redacted them would be unreadable.
func TestPassesLeaveCleanTextAlone(t *testing.T) {
	for _, s := range []string{
		"the update failed with exit code 1: terraform apply",
		"see https://github.com/dimipaun/fugaro/pull/1918 for the change",
		"retrying in 30s, attempt 3 of 5",
		"the task-sk-1234567890abcdefghij note is not a key",
		"see https://example.com/home/page and /var/home/user for the shape",
	} {
		if got := Text(s, []string{"a-secret-value"}); got != s {
			t.Errorf("Text(%q) = %q, want it unchanged", s, got)
		}
		if got := Report(s); got != s {
			t.Errorf("Report(%q) = %q, want it unchanged", s, got)
		}
	}
}

// TestPassesAreIdempotent: a diagnosis is redacted again on its way into the
// report, and a second pass must not keep eating the text.
func TestPassesAreIdempotent(t *testing.T) {
	for _, tc := range patternCases {
		exact := []string{"a-known-value", "another-known-value"}
		once := Text(tc.in, exact)
		if twice := Text(once, exact); twice != once {
			t.Errorf("Text is not idempotent for %s:\n once: %q\ntwice: %q", tc.name, once, twice)
		}
		reported := Report(once)
		if again := Report(reported); again != reported {
			t.Errorf("Report is not idempotent for %s:\n once: %q\ntwice: %q", tc.name, reported, again)
		}
	}
}

// TestPassesAreBounded: both passes run over a whole log, so their cost has
// to be linear in the text rather than in what the text repeats. The bound is
// loose on purpose — this runs under -race on a shared machine, where the
// same pass costs an order of magnitude more — and it is still far below what
// a pass that scanned the text again for every position would take.
func TestPassesAreBounded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("Authorization: Bearer " + strings.Repeat("A1b2C3d4", 3) + "\n")
		b.WriteString("-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("b2R5", 8) + "\n-----END RSA PRIVATE KEY-----\n")
		b.WriteString("?sig=" + strings.Repeat("ab", 16) + "&X-Amz-Expires=1\n")
		b.WriteString("eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJlMTIz\n")
		b.WriteString("reached dimi@example.com at /home/someone/.config\n")
	}
	in := b.String()
	if len(in) < 1<<17 {
		t.Fatalf("the fixture is %d bytes, too small to say anything about cost", len(in))
	}
	start := time.Now()
	got := Text(in, []string{"a-known-value"})
	got = Report(got)
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("redacting %d bytes took %s, which is not a pass over a log", len(in), d)
	}
	for _, leak := range []string{"dimi@example.com", "/home/someone", strings.Repeat("A1b2C3d4", 3), strings.Repeat("ab", 16)} {
		if strings.Contains(got, leak) {
			t.Errorf("%q survived a pass over %d bytes", leak, len(in))
		}
	}
}
