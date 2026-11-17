// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The deployment's configuration (portal plan P1.7). Two facts are held here
// and no other case can state either of them: a variable this service does not
// know is a deployment that does not start, and the minter's program names
// neither the mail key nor the merchant's secret — in the variables it reads and
// in its own source, which are the two ways one could reach it.
//
// The environment below is a whole deployment's, written out: every variable
// the API reads is set, so a case that wants to see one refusal overrides one
// name rather than building an environment that is missing ten others, and a
// variable added to the loader without a case that sets it fails every case here
// rather than passing quietly.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// secretStore is Secret Manager as the loader sees it: a name to a value,
// handed to LoadConfig as the seam.
type secretStore map[string]string

func (s secretStore) Secret(_ context.Context, name string) ([]byte, error) {
	value, ok := s[name]
	if !ok {
		return nil, errors.New("keyservice: no such secret: " + name)
	}
	return []byte(value), nil
}

// secretsFixture is one deployment's four secrets, named the way the secret
// containers are (P0.2). None of the values is a real credential and none is
// shaped like one: what this case is about is where a value comes from, not what
// it says.
func secretsFixture() secretStore {
	return secretStore{
		"fugaro-merchant-secret": "merchant-of-record-signing-secret",
		"fugaro-admin-secret":    "admin-token-signing-secret",
		"fugaro-paddle-api-key":  "paddle-api-key",
		"fugaro-resend-api-key":  "resend-api-key",
	}
}

// apiEnvironment is a whole deployment's environment: the API's own variables,
// the four Secret Manager names, and the two values a deploy names for the
// wiring (the project, the minter's address, the sender, the owner's address).
// A case edits one entry or adds one, which is what makes each case about the
// change it names.
func apiEnvironment(t *testing.T) []string {
	t.Helper()
	env := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"PORT=8080",
		varEnvironment + "=" + string(Production),
		varKeys + "=" + `{"production":"` + productionKeyID + `","staging":"` + stagingKeyID + `"}`,
		varURL + "=" + intakeOrigin,
		varPlans + "=" + `{"free_seat":{"name":"` + freePlanName + `","tier":"team","features":["seats"],"seats":1,"term_days":14},` +
			`"open_source":{"name":"` + openPlanName + `","tier":"team","features":["seats"],"seats":1,"term_days":365},` +
			`"paid":{"name":"` + paidPlanName + `","tier":"team","features":["seats"]},` +
			`"page":[{"name":"` + enterprisePlanName + `","tier":"enterprise","features":["seats"],"seats":25,"term_days":365}],` +
			`"mint_caps":{"community":{"requests":20,"window":"1h"},"team":{"requests":20,"window":"1h"},"enterprise":{"requests":20,"window":"1h"}}}`,
		varFreeMailDomains + "=gmail.example,outlook.example",
		varVerificationTTL + "=24h",
		varCodeHosts + "=code.example",
		varMaxRepositories + "=8",
		varThrottle + "=50/1h",
		varInviteRate + "=50/1h",
		varAdminAccount + "=" + ownerAccount,
		varAdminTokenTTL + "=15m",
		varPaddlePriceID + "=pri_portal_team_month",
		varSchedulerAudien + "=https://keys.fugaro.example",
		varSchedulerAcct + "=scheduler@fugaro.example.iam.gserviceaccount.com",
		varSealerKeyName + "=" + sealerKeyName,
		varTenantsColl + "=" + tenantsCollection,
		varTermsVersion + "=" + termsVersion,
		varSecrets + "=" + `{"merchant":"fugaro-merchant-secret","admin":"fugaro-admin-secret",` +
			`"paddle_api_key":"fugaro-paddle-api-key","resend_api_key":"fugaro-resend-api-key"}`,
		varProject + "=fugaro-portal",
		varMinterURL + "=https://fugaro-key-minter-abc.a.run.app",
		varMailFrom + "=keys@fugaro.example",
		varNotifyAddress + "=" + ownerAccount,
	}
	return env
}

// withVar is the same environment with one name set to one value.
func withVar(t *testing.T, name, value string) []string {
	t.Helper()
	env := apiEnvironment(t)
	for i, entry := range env {
		if key, _, _ := strings.Cut(entry, "="); key == name {
			env[i] = name + "=" + value
			return env
		}
	}
	return append(env, name+"="+value)
}

// TestConfigRefusesUnknownVariables is the task's own: a variable this service
// does not read is refused rather than ignored. The two halves are one mistake
// at the operator's end — a name that is misspelled, and a name that belongs to
// the other program — and both are refused, which is what makes the minter's
// three variables a fence around the process that signs.
func TestConfigRefusesUnknownVariables(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		env   []string
		wants string
	}{
		{
			name:  "a variable whose name is misspelled",
			env:   withVar(t, "FUGARO_VERIFICATON_TTL", "24h"),
			wants: "FUGARO_VERIFICATON_TTL",
		},
		{
			name:  "a variable that belongs to the other program",
			env:   withVar(t, "FUGARO_SIGNER_KEY_VERSION", "projects/portal/locations/europe-west1/keyRings/fugaro-keys/cryptoKeys/licence/cryptoVersions/1"),
			wants: "FUGARO_SIGNER_KEY_VERSION",
		},
		{
			name:  "a name that is not this service's at all",
			env:   append(apiEnvironment(t), "FUGARO_MAIL_KEY=fugaro-resend-api-key"),
			wants: "FUGARO_MAIL_KEY",
		},
		{
			name:  "a variable set to nothing",
			env:   withVar(t, varURL, ""),
			wants: varURL,
		},
		{
			name:  "a variable that is not there",
			env:   withoutVar(t, varTermsVersion),
			wants: varTermsVersion,
		},
		{
			name:  "an entry that is not a NAME=value pair",
			env:   append(apiEnvironment(t), "FUGARO_ENVIRONMENT"),
			wants: "FUGARO_ENVIRONMENT",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := LoadConfig(ctx, tc.env, secretsFixture())
			if err == nil {
				t.Fatalf("the loader accepted an environment it should have refused")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("the refusal reads %q, and it should name %s", err, tc.wants)
			}
		})
	}
	// The environment is otherwise whole: the loader must accept it, so a case
	// above is about the one thing it changes.
	cfg, deploy, err := LoadConfig(ctx, apiEnvironment(t), secretsFixture())
	if err != nil {
		t.Fatalf("the loader refused a whole deployment's environment: %v", err)
	}
	if cfg.Keys.Production != productionKeyID || cfg.Environment != Production {
		t.Fatalf("the loader read the environment as %s/%s", cfg.Environment, cfg.Keys.Production)
	}
	// What the environment says reaches the Config's own shapes, which is the
	// half a strict reader could get wrong: the plans and the caps are one
	// document, and the secrets are read through the seam.
	if cfg.Paid.Name != paidPlanName || cfg.PagePlans[0].Seats != 25 {
		t.Fatalf("the plans were read as %+v and %+v", cfg.Paid, cfg.PagePlans)
	}
	if cap := cfg.MintCaps[licence.TierEnterprise]; cap.Requests != 20 || cap.Window != time.Hour {
		t.Fatalf("the enterprise cap was read as %+v", cap)
	}
	if len(cfg.FreeMailDomains) != 2 || cfg.FreeMailDomains[1] != "outlook.example" {
		t.Fatalf("the free-mail list was read as %v", cfg.FreeMailDomains)
	}
	if string(cfg.MerchantSecret) != "merchant-of-record-signing-secret" || string(cfg.ResendKey) != "resend-api-key" {
		t.Fatal("the secrets were not read from Secret Manager")
	}
	if cfg.SentryDSN != "" {
		t.Fatalf("a deployment that names no error DSN read %q, and P1.6's off is empty", cfg.SentryDSN)
	}
	if deploy.Project != "fugaro-portal" || deploy.NotifyAddress != ownerAccount {
		t.Fatalf("the wiring was read as %+v", deploy)
	}
	// A secret the deployment did not name is a deployment that cannot mint or
	// cannot mail, refused at the start: the merchant's secret is the first.
	held := secretsFixture()
	delete(held, "fugaro-merchant-secret")
	if _, _, err := LoadConfig(ctx, apiEnvironment(t), held); err == nil {
		t.Fatal("the loader accepted a deployment whose merchant secret is not there")
	}
	// And the DSN, which is the one secret whose absence is a setting.
	env := withVar(t, varSecrets, `{"merchant":"fugaro-merchant-secret","admin":"fugaro-admin-secret",`+
		`"paddle_api_key":"fugaro-paddle-api-key","resend_api_key":"fugaro-resend-api-key","sentry_dsn":"fugaro-sentry-dsn"}`)
	held = secretsFixture()
	held["fugaro-sentry-dsn"] = "https://errors.example/1"
	cfg, _, err = LoadConfig(ctx, env, held)
	if err != nil {
		t.Fatalf("a deployment that names an error DSN was refused: %v", err)
	}
	if cfg.SentryDSN != "https://errors.example/1" {
		t.Fatalf("the error DSN was read as %q", cfg.SentryDSN)
	}
	// A role this service does not know is refused, for the reason every other
	// name is.
	if _, _, err := LoadConfig(ctx, withVar(t, varSecrets, `{"merchant":"fugaro-merchant-secret","admin":"fugaro-admin-secret",`+
		`"paddle_api_key":"fugaro-paddle-api-key","resend_api_key":"fugaro-resend-api-key","stripe_api_key":"x"}`), secretsFixture()); err == nil {
		t.Fatal("the loader accepted a secret role it does not read")
	}
}

// withoutVar is an environment with one name taken out.
func withoutVar(t *testing.T, name string) []string {
	t.Helper()
	out := []string{}
	for _, entry := range apiEnvironment(t) {
		if key, _, _ := strings.Cut(entry, "="); key == name {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// TestMinterBinaryHoldsNoMailOrMerchantSecret is the task's own second half, and
// it is the identity boundary read as text: the minter's program must not name
// the fields that hold what only the API may hold, and its loader must refuse
// the API's environment rather than run with it.
//
// The names below are the Config's own (service.go), and they are what a
// mistake looks like: a `cfg.MerchantSecret` in this program is the merchant of
// record's signing secret in the process that signs, which is the one thing
// §3.2 and Check 174 say the minter does not hold.
func TestMinterBinaryHoldsNoMailOrMerchantSecret(t *testing.T) {
	source, err := os.ReadFile("cmd/minter/main.go")
	if err != nil {
		t.Fatalf("reading the minter's program: %v", err)
	}
	text := string(source)
	for _, forbidden := range []string{
		"MerchantSecret", "ResendKey", "AdminSecret", "PaddleAPIKey", "SentryDSN",
		"firestore", "docstore", "Store", "Mailer", "Notifier", "Docs",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("the minter's program names %q: the process that signs holds no store, no mail path and no applicant's secret (§3.2, Check 174)", forbidden)
		}
	}
	// And what it does name is the signer and the three variables, so this case
	// fails if the file were emptied rather than kept to its shape.
	for _, wanted := range []string{"NewKMSSigner", "SignerKeyVersion", "keyservice.Minter{"} {
		if !strings.Contains(text, wanted) {
			t.Errorf("the minter's program does not name %q, so this case is not reading the program it means to hold", wanted)
		}
	}
	// The other half of the boundary is the loader: the API's own environment,
	// which LoadConfig accepts, is refused by the minter's.
	if _, err := LoadMinterConfig(apiEnvironment(t)); err == nil {
		t.Fatal("the minter accepted the API's environment, so a deploy script that reused one could hand the signer a customer's secrets")
	} else if !strings.Contains(err.Error(), "does not read") || !strings.Contains(err.Error(), "FUGARO_") {
		// The refusal names the variable the minter does not read — whichever
		// of the API's it reaches first — and never a value: an operator
		// reading the log gets the name to fix.
		t.Fatalf("the refusal reads %q, and it should name a variable this program does not read", err)
	}
	// The minter's own three are accepted, which is what makes the refusal
	// above about the API's variables and not about the loader refusing
	// everything.
	cfg, err := LoadMinterConfig([]string{
		varEnvironment + "=" + string(Staging),
		varKeys + "=" + `{"production":"` + productionKeyID + `","staging":"` + stagingKeyID + `"}`,
		varSignerKeyVersion + "=projects/portal/locations/europe-west1/keyRings/fugaro-keys/cryptoKeys/licence/cryptoVersions/1",
	})
	if err != nil {
		t.Fatalf("the minter refused its own environment: %v", err)
	}
	if cfg.Environment != Staging || cfg.Keys.Staging != stagingKeyID || cfg.SignerKeyVersion == "" {
		t.Fatalf("the minter read its environment as %+v", cfg)
	}
	// An environment that names no key version is refused: an asymmetric key
	// has no primary version to fall back to, so a signer built with none would
	// fail at the first mint.
	if _, err := LoadMinterConfig([]string{
		varEnvironment + "=" + string(Production),
		varKeys + "=" + `{"production":"` + productionKeyID + `","staging":"` + stagingKeyID + `"}`,
	}); err == nil {
		t.Fatal("the minter accepted an environment that names no key version")
	}
}

// TestDeployProjectNamesTheProject holds the one value the Secret Manager reader
// is built with before the configuration is read: it is read from the same
// variable LoadConfig reads, so the two cannot disagree about which project this
// deployment's secrets live in.
func TestDeployProjectNamesTheProject(t *testing.T) {
	project, err := DeployProject(apiEnvironment(t))
	if err != nil {
		t.Fatalf("reading the project: %v", err)
	}
	if project != "fugaro-portal" {
		t.Fatalf("the project was read as %q", project)
	}
	if _, err := DeployProject([]string{"PATH=/usr/bin"}); err == nil {
		t.Fatal("an environment naming no project was accepted, and the reader would then reach for another project's secrets")
	}
	if _, err := DeployProject([]string{varProject + "="}); err == nil {
		t.Fatal("an environment naming an empty project was accepted")
	}
}
