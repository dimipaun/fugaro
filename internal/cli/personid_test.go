package cli

// The launcher's own Google ID token (A3, D902, personid.go): the argv the mint
// is run with, and the refusals a machine that cannot mint one gets. Every test
// here replaces the two gcloud seams, so no test runs a real gcloud and none
// mints a real credential.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// personGcloudStub replaces the mint's two process seams for one test: what
// `gcloud config list` answers, whether the binary is on PATH, and what
// `gcloud auth print-identity-token` answers. It records the argv of every
// call, because the argv IS the subject: which flags a user account, a service
// account and an impersonated account each take is the whole of
// personIdentityArgs.
type personGcloudStub struct {
	lookPathErr error
	configOut   string
	configErr   error
	tokenOut    string
	tokenErr    error
	args        [][]string
}

func (s *personGcloudStub) use(t *testing.T) *personGcloudStub {
	t.Helper()
	oldLookPath, oldRun := reviewGcloudLookPath, reviewGcloudRun
	reviewGcloudLookPath = func(string) (string, error) {
		if s.lookPathErr != nil {
			return "", s.lookPathErr
		}
		return "/usr/bin/" + reviewGcloudBin, nil
	}
	reviewGcloudRun = func(ctx context.Context, args ...string) ([]byte, error) {
		s.args = append(s.args, args)
		if len(args) >= 2 && args[0] == "config" {
			return []byte(s.configOut), s.configErr
		}
		return []byte(s.tokenOut), s.tokenErr
	}
	t.Cleanup(func() { reviewGcloudLookPath, reviewGcloudRun = oldLookPath, oldRun })
	return s
}

// TestPersonIdentityArgsIsTheAccountsOwnMint is the argv rule the mint's
// correctness rests on: gcloud refuses --audiences outright for a user account,
// so a person's own login takes the plain mint — the token whose audience is
// gcloud's own client id, the second audience the credentials service's person
// class admits — while a service account takes the audience-scoped one, and an
// impersonated identity takes --include-email as well, because the email claim
// is what the service looks a caller up by and an impersonated mint's elevation
// call defaults it off.
func TestPersonIdentityArgsIsTheAccountsOwnMint(t *testing.T) {
	const url = "https://fugaro-credentials-abc-uc.a.run.app"
	// personGcloudConfig is gcloud's own JSON shape, and the three accounts
	// differ only in two nested strings: they are set field by field rather
	// than as composite literals that would restate that shape here.
	var user, service, impersonated personGcloudConfig
	service.Core.Account = "fugaro-run@proj-1234.iam.gserviceaccount.com"
	impersonated.Auth.ImpersonateServiceAccount = "fugaro-run@proj-1234.iam.gserviceaccount.com"

	for _, tc := range []struct {
		name string
		cfg  personGcloudConfig
		want []string
	}{
		{"a user account", user, []string{"auth", "print-identity-token", "--quiet"}},
		{"a logged-in service account", service, []string{"auth", "print-identity-token", "--audiences=" + url, "--quiet"}},
		{"an impersonated identity", impersonated, []string{"auth", "print-identity-token", "--audiences=" + url, "--include-email", "--quiet"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := personIdentityArgs(url, tc.cfg)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("personIdentityArgs = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPersonIDTokenMintsTheTrimmedAnswer: gcloud's answer is a token and a
// newline, and what the service is presented with is the token.
func TestPersonIDTokenMintsTheTrimmedAnswer(t *testing.T) {
	s := (&personGcloudStub{
		configOut: `{"core":{"account":"operator@example.invalid"}}`,
		tokenOut:  "a-token-fixture\n",
	}).use(t)

	tok, err := personIDToken(context.Background(), "https://fugaro-credentials-abc-uc.a.run.app")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if tok != "a-token-fixture" {
		t.Errorf("the mint answered %q, want the trimmed token", tok)
	}
	if len(s.args) != 2 || s.args[1][1] != "print-identity-token" {
		t.Fatalf("the mint ran %q, want gcloud's own listing and then the mint", s.args)
	}
}

// TestPersonIDTokenRefusesWithoutAUsableCredential: every state in which this
// machine cannot present an identity is a refusal naming the fix, and none of
// them carries a value: the token is a credential and gcloud's own words are
// another writer's.
//
// Mutation (run, restore): return the raw output where the checks refuse — a
// token that is not one header value reaches the service's Authorization
// header, which is the request a header-splitting value would corrupt.
func TestPersonIDTokenRefusesWithoutAUsableCredential(t *testing.T) {
	const url = "https://fugaro-credentials-abc-uc.a.run.app"
	for _, tc := range []struct {
		name string
		stub *personGcloudStub
		want string
	}{
		{"no gcloud on PATH", &personGcloudStub{lookPathErr: errors.New("not found")}, "is not on PATH"},
		{"no account configured", &personGcloudStub{configOut: `{}`}, "has no Google account"},
		{"the listing cannot be read", &personGcloudStub{configOut: `not json`}, "has no Google account"},
		{"the mint fails", &personGcloudStub{configOut: `{"core":{"account":"operator@example.invalid"}}`, tokenErr: errors.New("gcloud said no")}, "could not be minted"},
		{"an empty answer", &personGcloudStub{configOut: `{"core":{"account":"operator@example.invalid"}}`, tokenOut: "  \n"}, "no usable identity token"},
		{"an answer with a newline inside", &personGcloudStub{configOut: `{"core":{"account":"operator@example.invalid"}}`, tokenOut: "one\ntwo\n"}, "no usable identity token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.stub.use(t)
			tok, err := personIDToken(context.Background(), url)
			if ExitCode(err) != ExitUserError {
				t.Fatalf("mint = %q, %v, want a user error", tok, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
			if tok != "" {
				t.Errorf("the refused mint answered %q, want no token at all", tok)
			}
		})
	}
}
