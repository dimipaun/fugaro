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
