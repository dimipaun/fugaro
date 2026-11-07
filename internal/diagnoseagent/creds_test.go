package diagnoseagent

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// credsFrag is the harmless constant every fixture credential is built from.
// No value in this file is a secret-shaped literal (the repository's
// Convention 2), and a test value needs no shape: what these tests assert about
// a credential is where it came from and that it reached one place, never that
// it looks like a token.
const credsFrag = "ZQ7X4"

// credsValue is one test's fixture credential: the constant, the test's own
// name and a tag, so two tests in this file never share a value and a leak
// assertion can look for the value the test built.
func credsValue(t *testing.T, tag string) string {
	t.Helper()
	return credsFrag + "-" + t.Name() + "-" + tag
}

// credsOAuthVar and credsKeyVar are the two variables the person's own login
// writes, taken from the one place the platform spells them.
var (
	credsOAuthVar = config.ReservedSecrets[config.ClaudeOAuthToken]
	credsKeyVar   = config.ReservedSecrets[config.AnthropicSecret]
)

// credsReader records every secret it is asked for and answers with value.
func credsReader(value string, asked *[]string) SecretReader {
	return func(name string) (string, error) {
		*asked = append(*asked, name)
		return value, nil
	}
}

// credsUnread is the reader of a test whose point is that the project's account
// is NOT read: a resolution that reaches it has already failed the test.
func credsUnread(t *testing.T) SecretReader {
	t.Helper()
	return func(name string) (string, error) {
		t.Errorf("the project's %s was read, and this case must not read it", name)
		return "", nil
	}
}

// credsAccounts is a checkout of acme/app whose project config defines one
// account, team-b, allowed for that repository; chosen is what
// default_claude_account names, and "" leaves the choice to the reserved
// default account.
func credsAccounts(chosen string) Accounts {
	a := Accounts{
		Repo:  "acme/app",
		Block: map[string]config.ClaudeAccount{"team-b": {AllowDataTo: []string{"acme/app"}}},
	}
	a.Default = chosen
	return a
}

// TestCredsResolvePrefersThePersonsOwnCredential is D1601's order: the
// credential this machine already holds comes first, in the order a local
// Claude login writes the two variables, and it costs no cloud call at all —
// the reader fails the test if it is reached, which is what makes "the
// project's account is not read when the environment holds one" a property
// rather than a hope.
//
// The handle carries exactly one variable of the two, so a diagnosis run on the
// person's subscription token cannot also present their API key to the child.
func TestCredsResolvePrefersThePersonsOwnCredential(t *testing.T) {
	oauth, key := credsValue(t, "oauth"), credsValue(t, "key")
	h, err := Resolve([]string{"PATH=/usr/bin", credsOAuthVar + "=" + oauth, credsKeyVar + "=" + key}, credsAccounts("team-b"), credsUnread(t))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !h.Own() {
		t.Error("the credential found in the environment is not reported as the person's own")
	}
	if h.Account() != "" {
		t.Errorf("a local credential names the account %q, and no installation's configuration names it", h.Account())
	}
	if h.Secret() != oauth {
		t.Error("the handle does not carry the subscription token the environment holds")
	}
	if got, want := h.Env(), []string{credsOAuthVar + "=" + oauth}; !slices.Equal(got, want) {
		t.Errorf("Env() = %v, want the subscription token alone: %v", got, want)
	}

	// The API key is the second rung, and it is chosen when the OAuth token is
	// absent or empty — an entry set to nothing is an unset one.
	h, err = Resolve([]string{credsOAuthVar + "=", credsKeyVar + "=" + key}, credsAccounts("team-b"), credsUnread(t))
	if err != nil {
		t.Fatalf("Resolve with only an API key: %v", err)
	}
	if h.Secret() != key || !h.Own() {
		t.Error("an empty OAuth token was taken as a credential instead of the API key below it")
	}
	if got, want := h.Env(), []string{credsKeyVar + "=" + key}; !slices.Equal(got, want) {
		t.Errorf("Env() = %v, want the API key alone: %v", got, want)
	}
}

// TestCredsResolveUsesTheProjectsAccountWhenTheEnvironmentHoldsNone is the
// fallback's happy path: the account the configuration's choice keys name, its
// own secret, and the token handed to the child under the variable a Claude
// OAuth credential belongs under.
func TestCredsResolveUsesTheProjectsAccountWhenTheEnvironmentHoldsNone(t *testing.T) {
	value := credsValue(t, "project")
	var asked []string
	h, err := Resolve(nil, credsAccounts("team-b"), credsReader(value, &asked))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := []string{config.ClaudeAccountSecret("team-b")}; !slices.Equal(asked, want) {
		t.Errorf("secrets read = %v, want exactly the chosen account's own: %v", asked, want)
	}
	if h.Own() {
		t.Error("the project's account is reported as the person's own credential")
	}
	if h.Account() != "team-b" {
		t.Errorf("Account() = %q, want team-b", h.Account())
	}
	if h.Secret() != value {
		t.Error("the handle does not carry the token the reader returned")
	}
	if got, want := h.Env(), []string{credsOAuthVar + "=" + value}; !slices.Equal(got, want) {
		t.Errorf("Env() = %v, want the account's token under %s: %v", got, credsOAuthVar, want)
	}
}

// TestCredsResolveRefusesTheProjectsAccountItsAllowDataToDoesNotList is the
// gate (D1601): a project's token may go only to a repository its
// allow_data_to names. The reader is never reached, so the refusal is not a
// check that happens after the value was read — the point of the ordering the
// read sits behind.
func TestCredsResolveRefusesTheProjectsAccountItsAllowDataToDoesNotList(t *testing.T) {
	value := credsValue(t, "unlisted")
	var asked []string
	a := Accounts{
		Repo:    "acme/app",
		Block:   map[string]config.ClaudeAccount{"team-b": {AllowDataTo: []string{"acme/other"}}},
		Default: "team-b",
	}
	_, err := Resolve(nil, a, credsReader(value, &asked))
	if err == nil {
		t.Fatal("a project account whose allow_data_to omits this repository was used: the gate D1601 rules is missing")
	}
	for _, want := range []string{"team-b", "allow_data_to", "acme/app", "acme/other"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if len(asked) != 0 {
		t.Errorf("the token was read before the account was checked: %v", asked)
	}
	if strings.Contains(err.Error(), value) {
		t.Errorf("the refusal carries the credential: %v", err)
	}
}

// TestCredsResolveFailsClosedOutsideACheckout is F22. allow_data_to is a
// per-repository list, a failed `fugaro update` has no repository, and the
// project's account is then refused rather than read — with the refusal naming
// the account the command would have used and the key that chooses it, so the
// person can tell which account is being held back and why.
func TestCredsResolveFailsClosedOutsideACheckout(t *testing.T) {
	value := credsValue(t, "outside")
	var asked []string
	reader := credsReader(value, &asked)

	for _, tc := range []struct {
		name     string
		accounts Accounts
		wants    []string
	}{
		{
			name:     "the configured default",
			accounts: Accounts{Block: map[string]config.ClaudeAccount{"team-b": {AllowDataTo: []string{"acme/app"}}}, Default: "team-b"},
			wants:    []string{"team-b", "default_claude_account"},
		},
		{
			name:     "a stage's own choice",
			accounts: Accounts{Stages: map[string]string{"review": "team-c"}, Stage: "review"},
			wants:    []string{"team-c", "stages.review.claude_account"},
		},
		{
			name:     "nothing configured at all",
			accounts: Accounts{},
			wants:    []string{config.DefaultClaudeAccount},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(nil, tc.accounts, reader)
			if err == nil {
				t.Fatal("the project's account was read outside a checkout, with no repository to check its allow_data_to against")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), value) {
				t.Errorf("the refusal carries the credential: %v", err)
			}
		})
	}
	if len(asked) != 0 {
		t.Errorf("a token was read outside a checkout: %v", asked)
	}
}

// TestCredsResolveWritesNothingToDisk is F7: the project's token is held in
// memory and never written. Every directory a credential could land in — a
// gcloud config, a cache, a temporary file — is pointed at one directory made
// read-only, so a resolution that persisted anything would fail here rather
// than pass unnoticed, and the directory is required to be empty afterwards.
func TestCredsResolveWritesNothingToDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("making the fixture directory read-only: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restoring the fixture directory's mode: %v", err)
		}
	})
	env := []string{
		"HOME=" + dir, "TMPDIR=" + dir, "CLOUDSDK_CONFIG=" + dir,
		"XDG_CONFIG_HOME=" + dir, "XDG_CACHE_HOME=" + dir, "XDG_STATE_HOME=" + dir,
	}
	value := credsValue(t, "ondisk")
	h, err := Resolve(env, credsAccounts("team-b"), func(string) (string, error) { return value, nil })
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if h.Secret() != value {
		t.Error("the handle does not carry the token the reader returned")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the fixture directory: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("resolving the credential wrote %v: F7's rule is that the project's token is held in memory only", names)
	}
}

// TestCredsResolveWithoutACredentialNamesTheFix is F13: the command asks
// nothing, so the answer to "you have no credential" is one refusal that names
// every way out — the two variables a local login writes and the project's own
// secret — rather than a prompt.
//
// The two shapes of "nothing there" are the reader that answers with no value
// and the command that has no reader at all: both refuse, and neither is
// silently resolved into an empty credential.
func TestCredsResolveWithoutACredentialNamesTheFix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reader SecretReader
	}{
		{"nothing stored under the account's secret", func(string) (string, error) { return "", nil }},
		{"no reader at all", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(nil, credsAccounts(""), tc.reader)
			if err == nil {
				t.Fatal("a diagnosis with no credential at all resolved to something")
			}
			for _, want := range []string{credsOAuthVar, credsKeyVar, config.ClaudeAccountSecret(config.DefaultClaudeAccount), config.DefaultClaudeAccount} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

// TestCredsResolveReportsAReaderFailure: a reader that fails is not "no
// credential" — the difference is what the person does next — and the refusal
// names the secret that could not be read, never a value.
func TestCredsResolveReportsAReaderFailure(t *testing.T) {
	_, err := Resolve(nil, credsAccounts("team-b"), func(string) (string, error) {
		return "", errors.New("permission denied")
	})
	if err == nil {
		t.Fatal("a reader that failed resolved to a credential")
	}
	for _, want := range []string{config.ClaudeAccountSecret("team-b"), "permission denied", credsOAuthVar} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}

// TestCredsResolveRefusesACredentialTooShortToRedact: a credential of a few
// bytes cannot be replaced in what a diagnosis prints without replacing
// ordinary words, so it is refused rather than used — the same refusal
// internal/agent makes of an OAuth token it could not redact. Both sources are
// held to it, because both reach the same printer.
func TestCredsResolveRefusesACredentialTooShortToRedact(t *testing.T) {
	_, err := Resolve([]string{credsOAuthVar + "=" + credsFrag[:3]}, credsAccounts("team-b"), credsUnread(t))
	if err == nil {
		t.Fatal("a three-byte credential in the environment was used")
	}
	for _, want := range []string{credsOAuthVar, "redact"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}

	var asked []string
	_, err = Resolve(nil, credsAccounts("team-b"), credsReader(credsFrag[:2], &asked))
	if err == nil {
		t.Fatal("a two-byte credential from the project's account was used")
	}
	if !strings.Contains(err.Error(), config.ClaudeAccountSecret("team-b")) {
		t.Errorf("the refusal does not name the secret the value came from: %v", err)
	}
}
