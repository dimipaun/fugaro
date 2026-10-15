package pricing

import (
	"strings"
	"testing"
)

func TestIsAlias(t *testing.T) {
	for _, s := range []string{
		"sonnet", "opus", "haiku", "opusplan", "default", "best", "fable",
		"Sonnet", "OPUS", "sonnet[1m]", "claude-opus-5-5[1m]", "claude-sonnet-5[1M]",
		"claude-3-5-sonnet-latest", "anthropic.claude-opus-5-5", "us.anthropic.claude-opus-5-5",
		"", " ", "claude-", "claude-opus-5-5 ", "opus-5-5", "gpt-5", "claude-opus-5-5@", "claude-opus_5",
		"deepseek/deepseek-v4-flash:free", "deepseek/deepseek-v4-flash:online", "DeepSeek/x", "deepseek/", "/x", "a/b/c", "deepseek/x y",
		"claude/x", "claude-opus-5-5/x", "claudex/y", "deepseek/" + strings.Repeat("a", 120), "claude-" + strings.Repeat("a", 125),
		// The "anthropic/" prefix is still an alias except for the one shape
		// a route may claim (P4): an explicit, well-formed provider model ID.
		// A wildcard, a variant, a bracketed context window, a capital, a
		// space, an empty model name or an over-length ID is not that shape.
		"anthropic/*", "anthropic/", "anthropic/claude-opus-5-5:free", "anthropic/claude-opus-5-5[1m]",
		"anthropic/Claude-Opus", "anthropic/claude opus", "anthropic/" + strings.Repeat("a", 120),
	} {
		if !IsAlias(s) {
			t.Errorf("IsAlias(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		"claude-opus-5-5", "claude-sonnet-5", "claude-haiku-4-5", "claude-haiku-4-5-20251001",
		"claude-haiku-4-5@20251001", "claude-fable-5-1",
		"deepseek/deepseek-v4-flash", "qwen/qwen3-coder", "moonshotai/kimi-k2.5",
		"deepseek/" + strings.Repeat("a", 119), // 128 characters: the longest ID
		// A claimed Claude ID is a real model ID, so the price table keys it
		// (P4: the table is not the gate for who may pin what).
		"anthropic/claude-sonnet-5.5", "anthropic/claude-opus-5.5", "anthropic/claude-opus-5-5",
		"anthropic/claude-3.5-sonnet", // a dot and a dash
	} {
		if IsAlias(s) {
			t.Errorf("IsAlias(%q) = true, want false", s)
		}
	}
}

func TestIsProviderModelID(t *testing.T) {
	for _, s := range []string{
		"anthropic/claude-sonnet-5.5", "deepseek/deepseek-v4-flash", "x/y.z-w",
	} {
		if !IsProviderModelID(s) {
			t.Errorf("IsProviderModelID(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		"anthropic/x[1m]", "anthropic/x:free", "anthropic/", "Anthropic/x", "anthropic/*",
		// Whitespace is not part of the ID and is never trimmed: the grammar
		// is anchored and holds no space, tab, newline or zero-width space,
		// so a padded spelling is refused here rather than accepted after a
		// TrimSpace (which would make "anthropic/x\n" a claim).
		"anthropic/x ", " anthropic/x", "anthropic/x\t", "\tanthropic/x",
		"anthropic/x\n", "\nanthropic/x", "anthropic/x\u200b", "\u200banthropic/x",
		"deepseek/deepseek-v4-flash ",
	} {
		if IsProviderModelID(s) {
			t.Errorf("IsProviderModelID(%q) = true, want false", s)
		}
	}
}
