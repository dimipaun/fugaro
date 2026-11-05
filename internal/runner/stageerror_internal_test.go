package runner

import "testing"

// TestSubscriptionUsageLimitIsTheAccountsOwnSignal: the classifier behind the
// named reason fires on each shape a spent OAuth allowance arrives as, and on
// nothing else: a stage on an API key or on a provider route cannot be
// reported as the account's allowance, and a repository's own rate-limit
// wording in a result text is not read as one.
//
// Mutation (drop the auth check): the api-key and vertex rows classify, so a
// run that has no subscription allowance is told it spent one.
// Mutation (drop a phrase from oauthLimitPhrases): its row stops classifying,
// and that shape keeps the generic reason.
// Mutation (widen a phrase -- rate_limit_error to rate_limit, to ratelimit, or
// to the English "rate limit"): the lookalike rows below classify, so a
// repository's own wording, a header and another API error type are all read
// as the account's allowance.
func TestSubscriptionUsageLimitIsTheAccountsOwnSignal(t *testing.T) {
	cases := []struct {
		name string
		auth string
		text string
		want bool
	}{
		{"the CLI's allowance wall", "oauth", "Claude AI usage limit reached|1760000000", true},
		{"the newer allowance wall", "oauth", "You've hit your limit - resets at 4pm (UTC)", true},
		{"the API refusal of a spent token", "oauth", `API Error: 429 {"type":"error","error":{"type":"rate_limit_error"}}`, true},
		{"the same text on an API key", "api-key", "Claude AI usage limit reached|1760000000", false},
		{"the same text on a provider route", "vertex", "rate_limit_error", false},
		{"an unrelated refusal", "oauth", `API Error: 500 {"type":"error","error":{"type":"api_error"}}`, false},
		{"a repository's own rate limit", "oauth", "the test command hit a rate limit and backed off", false},
		{"another error type that starts the same", "oauth", `API Error: 429 {"type":"error","error":{"type":"rate_limit_exceeded"}}`, false},
		{"a rate-limit header, not the allowance", "oauth", "x-ratelimit-remaining: 0", false},
		{"the words in a message, not the type", "oauth", "the upstream said: rate limit reached, try later", false},
		{"no text at all", "oauth", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subscriptionUsageLimit(tc.auth, tc.text); got != tc.want {
				t.Fatalf("subscriptionUsageLimit(%q, %q) = %v, want %v", tc.auth, tc.text, got, tc.want)
			}
		})
	}
}
