package cli

import (
	"regexp"
	"strings"
	"testing"
)

// bareReviewToken is the retired flag as a whole word: `--token` and not the
// prefix of a longer name, which a check that matched the prefix would pass on.
var bareReviewToken = regexp.MustCompile(`--` + retiredReviewToken + `([^-a-z0-9]|$)`)

// TestReviewTokenFlagIsRejectedWithTheFix: `fugaro review` takes no `--token`
// (CS15). The review job reaches its model by the project's routing table, so
// there is no account for this command to name, and the flag is removed rather
// than aliased: a scripted launch that still passes it is refused with a
// sentence naming the fix, not pflag's bare "unknown flag".
//
// What the launch still resolves — agent.oauth_token in the checkout at the
// ref, else the project's default_claude_account — is the account the
// review-only job it runs on is chosen by, and that resolution is
// review_test.go's subject. This file's is the flag's absence: the command
// offers it nowhere, refuses that one spelling by name, and leaves every other
// unknown flag to pflag.
func TestReviewTokenFlagIsRejectedWithTheFix(t *testing.T) {
	cmd, _, err := NewRootCmd().Find([]string{"review"})
	if err != nil || cmd == nil {
		t.Fatalf("the tree has no review command: %v", err)
	}
	if cmd.Flags().Lookup(retiredReviewToken) != nil {
		t.Errorf("fugaro review still declares --%s: it is removed, not aliased", retiredReviewToken)
	}
	// The offer, in the two places a command makes one: the flag list it prints
	// and its Long. A *refusal* may name the retired flag (that is the sentence
	// read below), which is why this reads the two surfaces that offer things.
	for _, text := range []string{cmd.Flags().FlagUsages(), cmd.Long} {
		if m := bareReviewToken.FindAllString(text, -1); len(m) > 0 {
			t.Errorf("fugaro review's help offers --%s %d time(s): the flag is gone", retiredReviewToken, len(m))
		}
	}

	f := newReviewFixture(t)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
	before := len(f.run.Executions())
	out, errOut, err := reviewCLI(t, "--pr", "7", "--"+retiredReviewToken, "acme-personal")
	if err == nil {
		t.Fatalf("fugaro review --%s exited 0 (out %q err %q)", retiredReviewToken, out, errOut)
	}
	if got := ExitCode(err); got != ExitUserError {
		t.Errorf("exit %d, want %d", got, ExitUserError)
	}
	msg := out + errOut + ErrorText(err)
	for _, want := range []string{"--" + retiredReviewToken, "fugaro routing"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not name %q: %q", want, msg)
		}
	}
	if after := len(f.run.Executions()); after != before {
		t.Errorf("the refused launch started %d executions, want none: the flag is answered while the flags are parsed, before the cloud is opened", after-before)
	}

	// Only that one spelling is answered, and the comparison is pflag's whole
	// message. A typo that merely begins with it is a different question with a
	// different answer: telling its author that --token is gone would name a
	// retirement that has nothing to do with what they typed.
	for _, spelling := range []string{"--tokens", "--token-x", "--no-such-flag"} {
		t.Run(spelling, func(t *testing.T) {
			out, errOut, err := reviewCLI(t, "--pr", "7", spelling, "acme-personal")
			if err == nil {
				t.Fatalf("fugaro review %s exited 0", spelling)
			}
			if got := ExitCode(err); got != ExitUserError {
				t.Errorf("exit %d, want %d", got, ExitUserError)
			}
			got := out + errOut + ErrorText(err)
			if !strings.Contains(got, "unknown flag: "+spelling) {
				t.Errorf("the error is not pflag's own for %s: %q", spelling, got)
			}
			if strings.Contains(got, "is gone") {
				t.Errorf("%s was answered with the retired flag's sentence:\n%s", spelling, got)
			}
		})
	}

	// review show never had the flag, and its own unknown flags are not this
	// retirement's business: the FlagErrorFunc it inherits answers for the
	// command the flag was removed from and no other.
	out, errOut, err = reviewCLI(t, "show", "--"+retiredReviewToken, "acme/app/20261126-090000-c515")
	if err == nil {
		t.Fatalf("fugaro review show --%s exited 0 (out %q err %q)", retiredReviewToken, out, errOut)
	}
	if got := out + errOut + ErrorText(err); !strings.Contains(got, "unknown flag: --"+retiredReviewToken) {
		t.Errorf("review show --%s was not answered with pflag's own error: %q", retiredReviewToken, got)
	}
}
