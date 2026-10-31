package yamledit_test

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/yamledit"
)

// TestSetScalarPreservesComments is the reason the package exists: a settings
// file written by hand keeps every comment and every blank line, and only the
// bytes of the value change.
func TestSetScalarPreservesComments(t *testing.T) {
	const doc = `# Project settings for owner/repo-a.
# Edited by hand; keep the comments.

project: workspace-a

# Where the run's code may go.
allow_data_to:
  - owner/repo-a

# The default rigor mode for this project.
rigor:
  default: standard   # or startup
`

	const want = `# Project settings for owner/repo-a.
# Edited by hand; keep the comments.

project: workspace-a

# Where the run's code may go.
allow_data_to:
  - owner/repo-a

# The default rigor mode for this project.
rigor:
  default: pro   # or startup
`

	got, err := yamledit.SetScalar([]byte(doc), []string{"rigor", "default"}, "pro")
	if err != nil {
		t.Fatalf("SetScalar: %v", err)
	}
	if string(got) != want {
		t.Fatalf("comments or bytes around the edit changed:\n got %q\nwant %q", got, want)
	}
	if !yamledit.Changed([]byte(doc), got) {
		t.Fatal("Changed = false after a real edit")
	}
}

// TestSetScalarQuotesOnlyWhenYAMLNeedsIt pins the quoting decision: a value is
// left plain whenever it re-parses as that same string and quoted when it would
// re-parse as another type or as something that is not a scalar at all.
func TestSetScalarQuotesOnlyWhenYAMLNeedsIt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{"plain", "pro", "a: pro\n"},
		{"boolean", "true", "a: \"true\"\n"},
		{"integer", "20", "a: \"20\"\n"},
		{"float", "1.5", "a: \"1.5\"\n"},
		{"null", "null", "a: \"null\"\n"},
		{"tilde", "~", "a: \"~\"\n"},
		{"empty", "", "a: \"\"\n"},
		{"hash", "#comment", "a: \"#comment\"\n"},
		{"colon space", "a: b", "a: \"a: b\"\n"},
		{"leading space", " pro", "a: \" pro\"\n"},
		{"trailing space", "pro ", "a: \"pro \"\n"},
		{"sequence", "- pro", "a: \"- pro\"\n"},
		{"alias", "*pro", "a: \"*pro\"\n"},
		{"newline", "pro\npro", "a: \"pro\\npro\"\n"},
		{"tab", "pro\tpro", "a: \"pro\\tpro\"\n"},
		{"timestamp", "2026-10-31", "a: \"2026-10-31\"\n"},
		{"empty after a colon", "a:b", "a: a:b\n"},
		{"yaml 1.2 word", "yes", "a: yes\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := yamledit.SetScalar([]byte("a: old\n"), []string{"a"}, tc.value)
			if err != nil {
				t.Fatalf("SetScalar(%q): %v", tc.value, err)
			}
			if string(got) != tc.want {
				t.Fatalf("SetScalar(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}
