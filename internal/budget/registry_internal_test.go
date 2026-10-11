package budget

import (
	"strings"
	"testing"
)

// TestClipEntryClampsTokens (review focus 4): a Tokens value outside
// [0, MaxEntryTokens], or one the rules would also refuse, never leaves
// clipEntry with it unclamped. StageTokens can exceed the limit (it counts
// cache tokens on top of input/output) and a bug could send a negative
// figure; either would otherwise make the rules refuse the whole entry and
// freeze the heartbeat the way the pre-clamp code did.
func TestClipEntryClampsTokens(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   int64
		want int64
	}{
		{"zero", 0, 0},
		{"ordinary", 1200, 1200},
		{"exactly the limit", MaxEntryTokens, MaxEntryTokens},
		{"one over the limit", MaxEntryTokens + 1, MaxEntryTokens},
		{"far over the limit", MaxEntryTokens * 100, MaxEntryTokens},
		{"negative", -1, 0},
		{"very negative", -1_000_000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &AgentEntry{Tokens: tc.in}
			clipEntry(e)
			if e.Tokens != tc.want {
				t.Fatalf("clipEntry(%d) = %d, want %d", tc.in, e.Tokens, tc.want)
			}
		})
	}
}

// TestClipStringStripsSpoofingCharacters (review focus 4): clipString must
// catch what unicode.IsControl alone misses, since a dashboard reader's
// terminal trusts this text: an ESC sequence (which could rewrite the line)
// and a BiDi override or zero-width character (which could make it read
// differently from what it is, e.g. reordering a path or splitting a PR
// URL invisibly).
func TestClipStringStripsSpoofingCharacters(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"ESC CSI color escape", "tool Bash: \x1b[31mrm -rf /\x1b[0m"},
		{"RLO override (U+202E)", "tool Read: safe‮txt.exe"},
		{"zero-width space (U+200B)", "tool Bash: go​ test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := clipString(tc.in)
			for _, bad := range []string{"\x1b", "‮", "​"} {
				if strings.Contains(got, bad) {
					t.Fatalf("clipString(%q) = %q still carries %q", tc.in, got, bad)
				}
			}
		})
	}
}
