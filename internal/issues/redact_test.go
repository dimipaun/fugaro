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
	keep string
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
	{
		name: "home path in a file url",
		in:   "at " + "file:///home/" + "someone" + "/x.yaml the file is",
		leak: "/home/" + "someone",
	},
	{
		name: "home path under var",
		in:   "at " + "/var/home/" + "someone" + "/x.yaml the file is",
		leak: "/var/home/" + "someone",
	},
	{
		name: "password holding a quote",
		in:   "PGPASSWORD=" + "it's-a-" + strings.Repeat("Qw3", 3) + " psql: connection refused",
		leak: "it's-a-" + strings.Repeat("Qw3", 3),
	},
	{
		name: "password field of an escaped record",
		in: `{\"password\":\"` + strings.Repeat("Qw3", 5) +
			`\",\"step\":\"terraform apply\"}`,
		leak: strings.Repeat("Qw3", 5),
		keep: `\",\"step\":\"terraform apply\"}`,
	},
	{
		name: "password field inside a string value",
		in: `{"message":"invalid token: ` + strings.Repeat("Qw3", 5) +
			`","type":"auth"}`,
		leak: strings.Repeat("Qw3", 5),
		keep: `","type":"auth"}`,
	},
	{
		name: "password beginning with a bracket",
		in:   "PGPASSWORD=[" + strings.Repeat("Qw3", 5) + " psql: connection refused",
		leak: "[" + strings.Repeat("Qw3", 5),
		keep: "psql: connection refused",
	},
	{
		name: "email written the way a url writes it",
		in:   "filed by " + "runner" + "%40" + "acme-prod.iam.gserviceaccount.com" + " today",
		leak: "runner" + "%40" + "acme-prod.iam.gserviceaccount.com",
		keep: "today",
	},
	{
		name: "windows home path spelled the way the filesystem does",
		in:   "at " + "c:\\users\\" + "someone" + "\\AppData\\Local\\Temp it is",
		leak: "c:\\users\\" + "someone",
		keep: "it is",
	},
	{
		name: "address git invents with no identity set",
		in: "fatal: unable to auto-detect email address (got '" +
			"someone" + "@" + "someone-ThinkPad" + ".(none)" + "')",
		leak: "someone" + "@" + "someone-ThinkPad",
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
			if !strings.Contains(got, redactedValue) {
				t.Errorf("Redact(%q) = %q: nothing was redacted", tc.in, got)
			}
			if tc.keep != "" && !strings.Contains(got, tc.keep) {
				t.Errorf("Redact(%q) = %q: the gate ate %q as well", tc.in, got, tc.keep)
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
//
// The caller lists one casing and the text holds the other, so the match's
// case-insensitivity is exercised rather than spelled out by the list itself.
func TestScrubNames(t *testing.T) {
	t.Run("names go and the diagnosis stays", func(t *testing.T) {
		got := Scrub("the Acme-Widgets launcher failed; see https://github.com/acme-widgets/acme-widgets",
			[]string{"acme-widgets", " acme "})
		if strings.Contains(got, "Acme-Widgets") {
			t.Errorf("Scrub = %q: the capitalised name survived the lower-case one the caller listed", got)
		}
		for _, name := range []string{"acme-widgets", "acme"} {
			if strings.Contains(strings.ToLower(got), name) {
				t.Errorf("Scrub = %q: %q survived", got, name)
			}
		}
		if !strings.Contains(got, "launcher failed") {
			t.Errorf("Scrub = %q: it ate the diagnosis as well", got)
		}
	})

	t.Run("a name inside another one goes with it", func(t *testing.T) {
		got := Scrub("acme-widgets failed", []string{"acme", "acme-widgets"})
		if got != redactedValue+" failed" {
			t.Errorf("Scrub = %q, want the whole name gone", got)
		}
	})

	t.Run("a name too short to replace is left alone", func(t *testing.T) {
		if got := Scrub("a b c", []string{"", "a"}); got != "a b c" {
			t.Errorf("Scrub = %q, want the text unchanged", got)
		}
	})

	t.Run("a name inside a credential does not put the rest of it back", func(t *testing.T) {
		// D1722's post is stripped of identity and then redacted again, so the
		// second redaction reads text this pass wrote: a name taken out of the
		// middle of a password must not leave the rest of it standing beside
		// the marker it left behind.
		password := "PGPASSWORD=" + "acme-widgets-" + strings.Repeat("Qw3", 2)
		got := Redact(Scrub(password, []string{"acme-widgets"}), nil)
		if strings.Contains(got, strings.Repeat("Qw3", 2)) {
			t.Errorf("Redact(Scrub(%q)) = %q: the rest of the password survived the name's marker", password, got)
		}
	})

	t.Run("a name inside the marker is not read as text", func(t *testing.T) {
		// Every name below sits inside the marker itself (red, act, ted), and a
		// marker is in the text anyway: Redact writes one, and the pass reads
		// its own and the one the text arrived with.
		cases := []struct {
			what  string
			in    string
			names []string
			want  string
		}{
			{"the marker this pass writes", "Fugaro failed", []string{"Fugaro", "red"}, redactedValue + " failed"},
			{"the marker this pass writes, again", "Acme failed", []string{"Acme", "act"}, redactedValue + " failed"},
			{"the marker this pass writes, once more", "Widgets failed", []string{"Widgets", "ted"}, redactedValue + " failed"},
			{"a marker the text arrived with", "the value " + redactedValue + " failed", []string{"act"}, "the value " + redactedValue + " failed"},
			{"both", "Acme holds " + redactedValue + " failed", []string{"Acme", "act"}, redactedValue + " holds " + redactedValue + " failed"},
		}
		for _, tc := range cases {
			if got := Scrub(tc.in, tc.names); got != tc.want {
				t.Errorf("Scrub(%q, %v) = %q, want %q (%s)", tc.in, tc.names, got, tc.want, tc.what)
			}
		}
	})
}
