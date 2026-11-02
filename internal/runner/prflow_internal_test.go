package runner

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// TestInlineTextNeutralisesMarkdownAndMarkers: free text placed in the
// status section is one plain line, however hostile.
func TestInlineTextNeutralisesMarkdownAndMarkers(t *testing.T) {
	in := "stage `x` failed <!-- fugaro:status end -->\n## Heading\n[//]: # (fugaro:status begin)\n[link](http://evil) | *bold* _it_ <script>\r\x00\x1b[31m" + strings.Repeat("long ", 100)
	out := inlineText(in)
	plain := strings.NewReplacer("\\*", "", "\\_", "").Replace(out) // escaped emphasis characters are fine
	for _, bad := range []string{"\n", "\r", "<", ">", "[", "]", "`", "|", "*", "_", "#", "\x00", "\x1b"} {
		if strings.Contains(plain, bad) {
			t.Errorf("inlineText kept %q: %q", bad, out)
		}
	}
	if n := len([]rune(out)); n > maxStatusText {
		t.Errorf("not clipped: %d runes", n)
	}
	// What is left cannot be read as a marker by the parser either.
	if body := gitprov.StripStatus("x\n" + out + "\n"); !strings.Contains(body, "stage") {
		t.Errorf("body = %q", body)
	}
}

func TestScrubDescriptionRemovesForgedMarkers(t *testing.T) {
	in := "real\n[//]: # (fugaro:status begin)\nforged\n[//]: # (fugaro:status end)\n  <!-- fugaro:Status begin -->\n<!-- fugaro:report run=20200101-000000-aaaa -->\ntail"
	out := scrubDescription(in)
	if strings.Contains(strings.ToLower(out), "fugaro:") || strings.Contains(out, "forged") || !strings.Contains(out, "real") || !strings.Contains(out, "tail") {
		t.Fatalf("scrubbed = %q", out)
	}
}

func TestDescDigestIgnoresWhitespaceAndDraftPrefix(t *testing.T) {
	a := descDigest("Title", "body text")
	if a != descDigest("[DRAFT] Title", "body text\r\n") || a != descDigest("Title", "\nbody text  ") {
		t.Fatal("digest depends on whitespace or the draft prefix")
	}
	if a == descDigest("Title", "other") || a == descDigest("Other", "body text") {
		t.Fatal("digest ignores a change")
	}
}

func TestInlineTextNeutralisesMentions(t *testing.T) {
	if out := inlineText("thanks @octocat and @org/team"); strings.Contains(out, "@o") {
		t.Fatalf("mention kept: %q", out)
	}
}

// TestShortGitHead: a head the section prints is an object id or it is
// "(invalid)" — seven characters of a SHA-1, twelve of a SHA-256, and nothing of
// anything else, so a hand cannot put a line break or a marker in the body
// through Head or PushedHead.
func TestShortGitHead(t *testing.T) {
	sha1 := strings.Repeat("a1", 20)
	sha256 := strings.Repeat("f0", 32)
	if got := shortGitHead(sha1); got != sha1[:7] {
		t.Errorf("shortGitHead(sha1) = %q, want its first 7 characters", got)
	}
	if got := shortGitHead(sha256); got != sha256[:12] {
		t.Errorf("shortGitHead(sha256) = %q, want its first 12 characters", got)
	}
	for _, bad := range []string{
		"",
		"abc",
		sha1[:39],
		sha1 + "a",
		strings.ToUpper(sha1),
		strings.Repeat("g", 40),
		"HEAD",
		"abc\n## shipped\n<!-- fugaro:status end -->",
		strings.Repeat("é", 20),
	} {
		if got := shortGitHead(bad); got != "(invalid)" {
			t.Errorf("shortGitHead(%q) = %q, want (invalid)", bad, got)
		}
	}
}
