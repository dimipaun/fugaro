package pricing

import "testing"

func TestIsAlias(t *testing.T) {
	for _, s := range []string{
		"sonnet", "opus", "haiku", "opusplan", "default", "best", "fable",
		"Sonnet", "OPUS", "sonnet[1m]", "claude-opus-5-5[1m]", "claude-sonnet-5[1M]",
		"claude-3-5-sonnet-latest", "anthropic.claude-opus-5-5", "us.anthropic.claude-opus-5-5",
		"", " ", "claude-", "claude-opus-5-5 ", "opus-5-5", "gpt-5", "claude-opus-5-5@", "claude-opus_5",
		"deepseek/deepseek-v4-flash:free", "deepseek/deepseek-v4-flash:online", "DeepSeek/x", "deepseek/", "/x", "a/b/c", "anthropic/claude-opus-5-5", "deepseek/x y",
	} {
		if !IsAlias(s) {
			t.Errorf("IsAlias(%q) = false, want true", s)
		}
	}
	for _, s := range []string{
		"claude-opus-5-5", "claude-sonnet-5", "claude-haiku-4-5", "claude-haiku-4-5-20251001",
		"claude-haiku-4-5@20251001", "claude-fable-5-1",
		"deepseek/deepseek-v4-flash", "qwen/qwen3-coder", "moonshotai/kimi-k2.5",
	} {
		if IsAlias(s) {
			t.Errorf("IsAlias(%q) = true, want false", s)
		}
	}
}
