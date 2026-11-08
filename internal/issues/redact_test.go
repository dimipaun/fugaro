package issues

import (
	"strings"
	"testing"
)

// corpus is one row per kind of secret an issue text carries, every value
// built from fragments: a test file in this repository must not hold a
// secret-shaped literal. The rows go through Redact, the gate an issue text
// passes on its way to the local file, the shared store and the public post;
// internal/redact's own corpus is the exhaustive one for the pattern rules.
var corpus = []struct {
	name string
	in   string
	leak string
}{
	{
		name: "api key",
		in:   "api error: " + "sk-" + "ant-api03-" + strings.Repeat("Qw3Z", 8) + " was rejected",
		leak: "sk-" + "ant-api03-",
	},
	{
		name: "github token",
		in:   "remote: " + "ghp_" + strings.Repeat("A1b2C3d4", 5) + " is not authorised",
		leak: "ghp_" + strings.Repeat("A1b2C3d4", 5),
	},
	{
		name: "jwt",
		in:   "answer: " + "eyJ" + "hbGciOiJIUzI1NiJ9." + "eyJzdWIiOiIxIn0." + "c2lnbmF0dXJlMTIz",
		leak: "eyJ" + "hbGciOiJIUzI1NiJ9.",
	},
	{
		name: "private key block",
		in: "-----BEGIN " + "RSA PRIVATE KEY-----\n" + strings.Repeat("b2R5LW1hdGVyaWFs", 4) +
			"\n-----END " + "RSA PRIVATE KEY-----",
		leak: strings.Repeat("b2R5LW1hdGVyaWFs", 4),
	},
	{
		name: "bearer header",
		in:   "Authorization: Bearer " + strings.Repeat("A1b2C3d4", 3) + "\nnext line",
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "password",
		in:   "PGPASSWORD=" + strings.Repeat("Qw3", 5) + " psql: connection refused",
		leak: strings.Repeat("Qw3", 5),
	},
	{
		name: "connection string",
		in:   "postgres://app:" + strings.Repeat("Qw3", 5) + "@db.example.com:5432/app refused",
		leak: strings.Repeat("Qw3", 5),
	},
	{
		name: "email",
		in:   "filed by " + "dimi" + "@" + "example.com" + " today",
		leak: "dimi" + "@" + "example.com",
	},
	{
		name: "home path",
		in:   "at " + "/home/" + "someone" + "/.config/fugaro.yaml the file is",
		leak: "/home/" + "someone",
	},
}

// TestRedactCorpus: the gate removes every kind the spec names, whoever wrote
// it and whatever it is worth, and says so in the text.
func TestRedactCorpus(t *testing.T) {
	for _, tc := range corpus {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.in, nil)
			if strings.Contains(got, tc.leak) {
				t.Errorf("Redact(%q) = %q: %q survived", tc.in, got, tc.leak)
			}
			if !strings.Contains(got, scrubMarker) {
				t.Errorf("Redact(%q) = %q: nothing was redacted", tc.in, got)
			}
		})
	}

	known := "known-" + strings.Repeat("Qw3", 4)
	if got := Redact("the value "+known+" failed", []string{known}); strings.Contains(got, known) {
		t.Errorf("Redact = %q: the value this machine holds survived", got)
	}
	if got := Redact("the value "+known+" failed", nil); !strings.Contains(got, known) {
		t.Errorf("Redact = %q: a value no caller named is not this pass's to remove", got)
	}
}

// TestRedactIsIdempotent: an issue text is redacted again on its way into the
// public post (F5), and the second pass must not keep eating the text.
func TestRedactIsIdempotent(t *testing.T) {
	for _, tc := range corpus {
		once := Redact(tc.in, nil)
		if twice := Redact(once, nil); twice != once {
			t.Errorf("Redact is not idempotent for %s:\n once: %q\ntwice: %q", tc.name, once, twice)
		}
	}
}

// TestRedactKeepsShortExcerpts: the gate substitutes and never cuts. An
// excerpt arrives short because the collector made it short (spec 3), and a
// redaction that dropped the lines around a match would take the diagnosis
// with it.
func TestRedactKeepsShortExcerpts(t *testing.T) {
	lines := []string{
		"fugaro update finished",
		"Authorization: Bearer " + strings.Repeat("A1b2C3d4", 3),
		"exit code 1",
	}
	got := Redact(strings.Join(lines, "\n"), nil)
	if strings.Contains(got, strings.Repeat("A1b2C3d4", 3)) {
		t.Errorf("Redact = %q: the token survived", got)
	}
	if strings.Count(got, "\n") != len(lines)-1 {
		t.Errorf("Redact = %q, want the %d lines of the excerpt", got, len(lines))
	}
	if !strings.HasPrefix(got, lines[0]+"\n") || !strings.HasSuffix(got, "\n"+lines[2]) {
		t.Errorf("Redact = %q: the lines around the match are not the ones that arrived", got)
	}
}

// TestScrubNames: a post goes to a public repository, and the project, the
// repository and the organisation are not its to name (D1722, F4), in text
// the reporter wrote as well as in a URL they pasted.
func TestScrubNames(t *testing.T) {
	t.Run("names go and the diagnosis stays", func(t *testing.T) {
		got := Scrub("the Acme-Widgets launcher failed; see https://github.com/acme-widgets/acme-widgets",
			[]string{"acme-widgets", "Acme-Widgets", " acme "})
		for _, name := range []string{"acme-widgets", "acme"} {
			if strings.Contains(strings.ToLower(got), name) {
				t.Errorf("Scrub = %q: %q survived", got, name)
			}
		}
		if !strings.Contains(got, "launcher failed") {
			t.Errorf("Scrub = %q: it ate the diagnosis as well", got)
		}
	})

	t.Run("a name too short to replace is left alone", func(t *testing.T) {
		if got := Scrub("a b c", []string{"", "a"}); got != "a b c" {
			t.Errorf("Scrub = %q, want the text unchanged", got)
		}
	})
}
