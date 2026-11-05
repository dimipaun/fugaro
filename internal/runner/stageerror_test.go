package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// oauthToken is the account token of the harness's OAuth runs: long enough
// for BuildEnv's redaction (4 bytes) and in nobody's fixture.
const oauthToken = "oauth-token-for-tests-1234"

// usageLimitText is what Claude Code's result event carries when the OAuth
// account has spent its subscription allowance.
const usageLimitText = "Claude AI usage limit reached|1760000000"

// oauthHarness is a harness whose config authenticates with the account's
// OAuth token, as a job of that account starts: api-key out of the
// environment, the token in it.
func oauthHarness(t *testing.T) *harness {
	t.Helper()
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "auth: api-key", "auth: oauth", 1)
	h := newHarness(t, cfg, nil)
	h.deps.Env = append(filterEnv(h.deps.Env, "ANTHROPIC_API_KEY"), "CLAUDE_CODE_OAUTH_TOKEN="+oauthToken)
	return h
}

// failingReview is a senior review stage that reported an error result with
// text: is_error set, no Go error, the shape an API the provider refused
// arrives in (Claude Code reports it as subtype "success").
func failingReview(subtype, text string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		return agent.Result{IsError: true, Subtype: subtype, Text: text}, nil
	}
}

// TestStageErrorReasonNamesTheOAuthUsageLimit: a stage on an OAuth account
// that reports the subscription usage limit ends the run with a reason that
// names that stop, rather than with the generic "the agent reported an
// error" its subtype would give. The same text from a stage on an API key
// is not named that way: an API key has no subscription allowance to be out
// of.
//
// Mutation (drop the auth check in subscriptionUsageLimit): the api-key case
// below is named after an OAuth allowance the run does not have.
// Mutation (drop a phrase from oauthLimitPhrases): the oauth case here falls
// back to the generic reason and fails.
func TestStageErrorReasonNamesTheOAuthUsageLimit(t *testing.T) {
	const named = "stage review: the OAuth account's subscription usage limit was reached, so the run stops here: " +
		"the account's allowance is shared with the owner's own Claude Code use and resets on its own schedule: " + usageLimitText
	t.Run("oauth", func(t *testing.T) {
		rec, err := oauthHarness(t).run(t, implement("feature"), failingReview("success", usageLimitText))
		if err != nil || rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if rec.Reason != named {
			t.Fatalf("reason = %q, want %q", rec.Reason, named)
		}
	})
	t.Run("api key", func(t *testing.T) {
		rec, err := newHarness(t, "", nil).run(t, implement("feature"), failingReview("success", usageLimitText))
		if err != nil || rec.Status != runstore.StatusFailed {
			t.Fatalf("rec = %+v, err = %v", rec, err)
		}
		if want := "stage review: the agent reported an error: " + usageLimitText; rec.Reason != want {
			t.Fatalf("reason = %q, want %q", rec.Reason, want)
		}
	})
}

// TestStageErrorReasonQuotesTheAgentsText: a stage that reported an error
// carries the result event's own text in the reason, so the record, the
// report and the draft pull request say why the call failed instead of
// hiding it behind a subtype that is "success" for an API error. A subtype
// that is not "success" is still named beside it, and a stage whose text is
// empty is the reason it always was.
func TestStageErrorReasonQuotesTheAgentsText(t *testing.T) {
	cases := []struct {
		name    string
		subtype string
		text    string
		want    string
	}{
		{"an API error", "success", `API Error: 429 {"type":"error","error":{"type":"rate_limit_error"}}`,
			`stage review: the agent reported an error: API Error: 429 {"type":"error","error":{"type":"rate_limit_error"}}`},
		{"a stage that died mid-execution", "error_during_execution", "claude exited with code 1",
			"stage review: the agent reported an error (error_during_execution): claude exited with code 1"},
		{"no subtype at all", "", "boom", "stage review: the agent reported an error: boom"},
		{"no text", "success", "", "stage review: the agent reported an error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := newHarness(t, "", nil).run(t, implement("feature"), failingReview(tc.subtype, tc.text))
			if err != nil || rec.Status != runstore.StatusFailed {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			if rec.Reason != tc.want {
				t.Fatalf("reason = %q, want %q", rec.Reason, tc.want)
			}
		})
	}
}

// TestStageErrorTextIsRedactedOneLineAndClipped: the result text is the
// agent's own, so it is redacted of the run's secrets before it is
// published, put on one line, and clipped — a reason is a sentence, and the
// account's token must not reach the record, the report or the draft pull
// request through it.
//
// A separator inside the token is the half the single pass misses: OneLine
// deletes escape sequences, so the two halves the redactor saw separately
// come back together in the line the reason carries. The rows below build
// that line here rather than writing a token into source.
//
// Mutation (drop the r.redact call): the token is published in the reason.
// Mutation (drop the second, outer r.redact, keeping the inner one): the ESC,
// CSI and OSC rows publish the token.
// Mutation (drop safetext.OneLine): the newline inside the text reaches the
// record.
// Mutation (drop logtail.Clip): the whole text is published.
func TestStageErrorTextIsRedactedOneLineAndClipped(t *testing.T) {
	long := strings.Repeat("y", 400)
	text := "API Error: 500 upstream echoed " + oauthToken + "\nsecond line " + long
	rec, err := oauthHarness(t).run(t, implement("feature"), failingReview("success", text))
	if err != nil || rec.Status != runstore.StatusFailed {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if strings.Contains(rec.Reason, oauthToken) {
		t.Fatalf("the reason publishes the account's token: %q", rec.Reason)
	}
	if !strings.Contains(rec.Reason, "[REDACTED]") {
		t.Fatalf("the reason was not redacted at all: %q", rec.Reason)
	}
	if strings.ContainsAny(rec.Reason, "\n\r") {
		t.Fatalf("the reason is not one line: %q", rec.Reason)
	}
	if !strings.HasSuffix(rec.Reason, "…") {
		t.Fatalf("the reason's text was not clipped: %q", rec.Reason)
	}
	if strings.Contains(rec.Reason, long) {
		t.Fatalf("the reason publishes the whole text: %q", rec.Reason)
	}

	// mark is whether the row's separator is one OneLine deletes, so the
	// halves join and the redaction mark is expected; the newline is the
	// separator that does not join — OneLine writes a "?" where it was.
	for _, tc := range []struct {
		name string
		sep  string
		mark bool
	}{
		{"an ESC sequence", "\x1b(B", true},
		{"a CSI sequence", "\x1b[0m", true},
		{"an OSC sequence", "\x1b]0;title\x07", true},
		{"a newline", "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			half := len(oauthToken) / 2
			split := "API Error: 500 upstream echoed " + oauthToken[:half] + tc.sep + oauthToken[half:]
			rec, err := oauthHarness(t).run(t, implement("feature"), failingReview("success", split))
			if err != nil || rec.Status != runstore.StatusFailed {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			if strings.Contains(rec.Reason, oauthToken) {
				t.Fatalf("the reason publishes the account's token: %q", rec.Reason)
			}
			if strings.ContainsAny(rec.Reason, "\n\r") {
				t.Fatalf("the reason is not one line: %q", rec.Reason)
			}
			if got := strings.Contains(rec.Reason, "[REDACTED]"); got != tc.mark {
				t.Fatalf("the reason holds a redaction mark = %v, want %v: %q", got, tc.mark, rec.Reason)
			}
		})
	}
}
