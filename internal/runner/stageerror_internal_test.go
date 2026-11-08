package runner

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/failure"
)

// TestSubscriptionUsageLimitIsTheAccountsOwnSignal: the classifier behind the
// named reason fires on each shape a spent OAuth allowance arrives as, and on
// nothing else: a stage on an API key or on a provider route cannot be
// reported as the account's allowance, and neither can a limit that is a
// repository's own wording, a resource's, a header's, or an error type merely
// named in the text.
//
// Mutation (drop the auth check): the api-key and vertex rows classify, so a
// run that has no subscription allowance is told it spent one.
// Mutation (drop a phrase from oauthLimitPhrases): its row stops classifying,
// and that shape keeps the generic reason.
// Mutation (widen a phrase -- rate_limit_error to a bare rate_limit_error or
// to rate_limit, "claude ai usage limit" to "usage limit", or any phrase to
// the English "rate limit"): the lookalike rows below classify, so a
// repository's own wording, a header, a resource limit, an echoed snippet or
// another API error type is read as the account's allowance.
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
		{"a resource limit, not the allowance", "oauth", "the build failed: disk usage limit reached", false},
		{"the type in a field of another name", "oauth", `the log said "previous_error_type": "rate_limit_error" was retried`, false},
		{"the type named in prose", "oauth", "the gateway answers 429 rate_limit_error when too many calls are in flight", false},
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

// stageFailureClassCase is one row of the classifier's tables: the result the
// agent reported and the one value of the vocabulary it must be read as.
type stageFailureClassCase struct {
	name string
	auth string
	text string
	want failure.Class
}

// stageFailureClassOf is the classifier's answer for one row, with the result
// shaped the way Claude Code reports a stage error: is_error set, no Go error,
// and the subtype "success" a refused API call arrives under.
func stageFailureClassOf(t *testing.T, tc stageFailureClassCase) failure.Class {
	t.Helper()
	res := agent.Result{Text: tc.text, IsError: true, Subtype: "success"}
	return (&run{}).stageFailureClass(tc.auth, res)
}

// TestStageFailureClassNamesTheAuthRefusal: a stage whose text is the route or
// the gateway refusing the run's credential is provider_auth_refused, and only
// that is: each phrase is read inside the envelope that carries it, so a type
// merely named in the text is not a refusal of the credential.
//
// Mutation (match on the subtype alone): every row here is subtype "success",
// which is what Claude Code reports a refused API call as, so no row is named.
// Mutation (drop a phrase from authRefusedPhrases): its row falls back to
// stage_error_unexplained.
// Mutation (widen a phrase -- the envelope `"error":{"type":"authentication_
// error"` to a bare "authentication_error", or the gateway's sentence to
// "status 403"): the lookalike rows below classify, so a type quoted in the
// text, or a 403 that is not the provider's refusal, is read as one.
func TestStageFailureClassNamesTheAuthRefusal(t *testing.T) {
	cases := []stageFailureClassCase{
		{"the provider's 403, relayed by the gateway", "api-key",
			`API Error: 403 {"type":"error","error":{"type":"permission_error","message":"fugaro: the provider answered with status 403"}}`,
			failure.ProviderAuthRefused},
		{"a refused API key", "api-key",
			`API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			failure.ProviderAuthRefused},
		{"the gateway's refusal of its own token", "api-key",
			`{"type":"error","error":{"type":"authentication_error","message":"fugaro: the gateway token is missing or wrong"}}`,
			failure.ProviderAuthRefused},
		{"the same refusal over OAuth", "oauth",
			`API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"invalid bearer token"}}`,
			failure.ProviderAuthRefused},
		{"the type merely named in the text", "api-key",
			`the log said "previous_error_type":"authentication_error" was retried`, failure.StageErrorUnexplained},
		{"another API error type", "api-key",
			`API Error: 400 {"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: field required"}}`,
			failure.StageErrorUnexplained},
		{"the type in prose", "api-key",
			"the gateway answers authentication_error when the key is wrong", failure.StageErrorUnexplained},
		{"a 403 that is not the provider's sentence", "api-key",
			"the endpoint answered 403 forbidden", failure.StageErrorUnexplained},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stageFailureClassOf(t, tc); got != tc.want {
				t.Fatalf("stageFailureClass(%q, %q) = %q, want %q", tc.auth, tc.text, got, tc.want)
			}
		})
	}
}

// TestStageFailureClassNamesTheStreamFailure: a stage whose text is a call's
// stream or connection ending before its response finished is transport_failed,
// the same sentence the retry reads (transientStagePhrases) and the one shape
// it holds.
//
// The list is deliberately narrower than the family it names: a connection
// dropped in other words is not matched, and a wall the table cannot read
// leaves the run stage_error_unexplained -- counted rather than guessed at.
//
// Mutation (match on the subtype alone): no row here is named, for the same
// reason as the auth refusal's.
// Mutation (drop the phrase): every transport row falls back.
// Mutation (widen it -- to "stream closed", or to "connection"): the lookalike
// rows below classify, so a stage that closed a stream on its own terms, or one
// that merely names a connection, is read as a lost answer.
func TestStageFailureClassNamesTheStreamFailure(t *testing.T) {
	cases := []stageFailureClassCase{
		{"Claude Code's own sentence", "api-key",
			"API Error: stream closed before completion", failure.TransportFailed},
		{"the sentence inside a longer text", "api-key",
			"the stage ended: API Error: stream closed before completion (after 2 attempts)",
			failure.TransportFailed},
		{"the same over OAuth", "oauth",
			"API Error: stream closed before completion", failure.TransportFailed},
		{"a stream that closed on the stage's own terms", "api-key",
			"the review closed the stream before completion of its own pass", failure.StageErrorUnexplained},
		{"a connection dropped in other words", "api-key",
			"API Error: connection reset by peer", failure.StageErrorUnexplained},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stageFailureClassOf(t, tc); got != tc.want {
				t.Fatalf("stageFailureClass(%q, %q) = %q, want %q", tc.auth, tc.text, got, tc.want)
			}
		})
	}
}

// TestStageFailureClassFallsBackWithoutText: a result the table does not name
// is stage_error_unexplained, with or without text, and never a guess. That is
// the class the observed runs whose agent wrote a normal final message and was
// recorded as an error anyway take, and it is the one the OAuth account's spent
// subscription allowance takes too: O6 has not answered which class that stop
// deserves, so the classifier answers before its tables and the allowance can
// never be read as a refusal of the credential, whatever a later table gains.
//
// Mutation (drop the allowance check, and add its own shape -- the rate-limit
// type oauthLimitPhrases holds -- to authRefusedPhrases): the allowance row
// classifies as a refusal of the credential, which is a claim about the
// account the run has not made.
func TestStageFailureClassFallsBackWithoutText(t *testing.T) {
	cases := []stageFailureClassCase{
		{"no text at all", "api-key", "", failure.StageErrorUnexplained},
		{"whitespace only", "api-key", "  \n\t ", failure.StageErrorUnexplained},
		{"the normal final message the observed runs wrote", "api-key",
			"I have updated the module and the tests pass.", failure.StageErrorUnexplained},
		{"an execution error with no words for it", "api-key",
			"claude exited with code 1", failure.StageErrorUnexplained},
		{"another API error", "api-key",
			`API Error: 500 {"type":"error","error":{"type":"api_error","message":"internal server error"}}`,
			failure.StageErrorUnexplained},
		{"the OAuth account's spent allowance", "oauth",
			"Claude AI usage limit reached|1760000000", failure.StageErrorUnexplained},
		{"the same allowance text on an API key", "api-key",
			"Claude AI usage limit reached|1760000000", failure.StageErrorUnexplained},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stageFailureClassOf(t, tc); got != tc.want {
				t.Fatalf("stageFailureClass(%q, %q) = %q, want %q", tc.auth, tc.text, got, tc.want)
			}
		})
	}
}

// TestStageFailureClassReadsTheFoldedText: the classifier reads the text as the
// reason carries it -- redacted, on one line and clipped (stageErrorText) -- and
// not as the result event handed it over, so the one thing it may recognise a
// phrase in is a byte the run already publishes.
//
// Mutation (match res.Text instead of the folded text): the split phrase below
// is not found, because OneLine is what deletes the escape sequence between its
// halves, and the row falls back to stage_error_unexplained.
func TestStageFailureClassReadsTheFoldedText(t *testing.T) {
	const phrase = "stream closed before completion"
	half := len(phrase) / 2
	split := agent.Result{Text: phrase[:half] + "\x1b[0m" + phrase[half:], IsError: true, Subtype: "success"}
	if got := (&run{}).stageFailureClass("api-key", split); got != failure.TransportFailed {
		t.Fatalf("stageFailureClass over a text whose phrase an escape sequence splits = %q, want %q: the classifier reads the folded text the reason carries, and OneLine is what joins the halves", got, failure.TransportFailed)
	}
}
