// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package main

// The seed through this program's own path (portal plan P0.5, P1.7): the
// environment the deployment's job carries, the argv the deploy gives it, and the
// four seams the deployment's own clients stand behind. What SeedOperations does
// with a store is invites_test.go's; what is held here is that the command
// starts — it is the one step of a first installation, and a run that dies on a
// missing variable leaves the installation with no operations organisation and no
// first invitation while the site and both services are already deployed.
//
// Nothing here reaches a cloud: the two seams a deployment fills with Firestore
// and Cloud KMS are one in-memory database and a sealer that is its own inverse,
// and the mail path keeps what it was asked to send instead of sending it.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/keyservice"
	"github.com/dimipaun/fugaro/ee/keyservice/docstore"
)

// opsAddress is the address a first installation invites: the operations
// organisation's first owner, which is the person running the deploy.
const opsAddress = "owner@fugaro.example"

// deploymentEnvironment is one deployment's environment, whole: every variable
// P1.7's loader reads, with the values a staging installation would carry. It is
// a second copy of what ee/keyservice/config_test.go writes, and it has to be —
// that fixture is unexported in another package and this case's whole point is
// this program's own path. What keeps the copy honest is the loader itself: a
// name the loader gained and this fixture did not is a seed that refuses its own
// environment, which is the second case below.
//
// The values are shapes and never credentials (CLAUDE.md): the secrets are read
// through the stand-in below, and the key ids name a ring rather than holding
// anything.
func deploymentEnvironment(t *testing.T) []string {
	t.Helper()
	return []string{
		"PATH=/usr/local/bin:/usr/bin",
		"FUGARO_ENVIRONMENT=staging",
		`FUGARO_KEYS={"production":"fugaro-licence-production","staging":"fugaro-licence-staging"}`,
		"FUGARO_URL=" + intakeOrigin,
		`FUGARO_PLANS={"free_seat":{"name":"free-seat","tier":"team","features":["seats"],"seats":1,"term_days":14},` +
			`"open_source":{"name":"open-source","tier":"team","features":["seats"],"seats":1,"term_days":365},` +
			`"paid":{"name":"paid","tier":"team","features":["seats"]},` +
			`"page":[{"name":"enterprise","tier":"enterprise","features":["seats"],"seats":25,"term_days":365}],` +
			`"mint_caps":{"community":{"requests":20,"window":"1h"},"team":{"requests":20,"window":"1h"},"enterprise":{"requests":20,"window":"1h"}}}`,
		"FUGARO_FREE_MAIL_DOMAINS=gmail.example,outlook.example",
		"FUGARO_VERIFICATION_TTL=24h",
		"FUGARO_CODE_HOSTS=code.example",
		"FUGARO_MAX_REPOSITORIES=8",
		"FUGARO_THROTTLE=50/1h",
		"FUGARO_INVITE_RATE=50/1h",
		"FUGARO_ADMIN_ACCOUNT=" + opsAddress,
		"FUGARO_ADMIN_TOKEN_TTL=15m",
		"FUGARO_PADDLE_PRICE_ID=pri_portal_team_month",
		"FUGARO_SCHEDULER_AUDIENCE=" + intakeOrigin,
		"FUGARO_SCHEDULER_ACCOUNT=fugaro-portal-scheduler@fugaro-portal.iam.gserviceaccount.com",
		"FUGARO_SEALER_KEY_NAME=projects/fugaro-portal/locations/us-east5/keyRings/fugaro-portal-handover/cryptoKeys/fugaro-portal-handover",
		"FUGARO_TENANTS_COLLECTION=tenants",
		"FUGARO_TERMS_VERSION=2026-11-01",
		`FUGARO_SECRETS={"merchant":"fugaro-portal-paddle-webhook-secret","admin":"fugaro-portal-admin-secret",` +
			`"paddle_api_key":"fugaro-portal-paddle-api-key","resend_api_key":"fugaro-portal-resend-api-key"}`,
		"FUGARO_PROJECT=fugaro-portal",
		"FUGARO_MINTER_URL=https://fugaro-key-minter-abc123.us-east5.run.app",
		"FUGARO_MAIL_FROM=keys@fugaro.example",
		"FUGARO_NOTIFY_ADDRESS=" + opsAddress,
	}
}

// The two origins of the deployment above. FUGARO_URL is the intake's — the one
// origin a deployment states — and the portal's own is the same site under the
// other label (invites.go's portalOrigin), which is what a mailed invitation's
// link is built from and what the case below reads the mail for.
const (
	intakeOrigin = "https://keys.fugaro.example"
	portalOrigin = "https://app.fugaro.example"
)

// secretStore is Secret Manager as the loader sees it. None of the values is a
// credential and none is shaped like one: what the seed's path is held to is
// where a value comes from, not what it says.
type secretStore map[string]string

func (s secretStore) Secret(_ context.Context, name string) ([]byte, error) {
	value, ok := s[name]
	if !ok {
		return nil, errors.New("the deployment holds no secret named " + name)
	}
	return []byte(value), nil
}

func secretStoreFixture() secretStore {
	return secretStore{
		"fugaro-portal-paddle-webhook-secret": "merchant-of-record-signing-secret",
		"fugaro-portal-admin-secret":          "admin-token-signing-secret",
		"fugaro-portal-paddle-api-key":        "paddle-api-key",
		"fugaro-portal-resend-api-key":        "resend-api-key",
	}
}

// keptMail is the mail path as a case reads it: every message the seed asked to
// send, in order, and nothing sent anywhere.
type keptMail struct{ sent []keyservice.Message }

func (m *keptMail) Send(_ context.Context, message keyservice.Message) error {
	m.sent = append(m.sent, message)
	return nil
}

// reversibleSealer is the handover's sealer with no cloud behind it. Nothing in
// the seed's path seals or opens — the seam is here because the service refuses
// to start without one — and the sealing itself is kmsseal_test.go's.
type reversibleSealer struct{}

func (reversibleSealer) Seal(_ context.Context, plaintext []byte) ([]byte, error) {
	return append([]byte("sealed:"), plaintext...), nil
}

func (reversibleSealer) Open(_ context.Context, sealed []byte) ([]byte, error) {
	return bytes.TrimPrefix(sealed, []byte("sealed:")), nil
}

// memorySeams is the deployment's own opener with the cloud stood in for: one
// in-memory database for the two store seams, the sealer above and the mail path
// the caller holds.
func memorySeams(db docstore.Store, mail keyservice.Mailer) seamsOpener {
	return func(_ context.Context, _ string, _ keyservice.Config) (deploymentSeams, error) {
		return deploymentSeams{
			Store:  keyservice.NewDocStore(db),
			Docs:   keyservice.NewDocStoreDocs(db),
			Sealer: reversibleSealer{},
			Mailer: mail,
		}, nil
	}
}

// TestSeedOperationsRunsOnTheDeploymentsEnvironment is the finding this case was
// written for, inverted: the seed is given the environment the deployment's job
// carries — and the deploy's own argv — and it has to reach the end, write the
// organisation and its invitation and send the one mail, with no variable
// standing in for anything.
//
// The second run is the same path again, and it is what holds that the records
// landed: what refuses it is the organisation the first run wrote, read back
// through the store this program was built with.
func TestSeedOperationsRunsOnTheDeploymentsEnvironment(t *testing.T) {
	env := deploymentEnvironment(t)
	db := docstore.NewMemory()
	mail := &keptMail{}
	open := memorySeams(db, mail)

	out, err := seedOutput(t, func() error {
		return runWith([]string{"seed-operations", opsAddress}, env, secretStoreFixture(), open)
	})
	if err != nil {
		t.Fatalf("the seed refused a whole deployment's environment, which is the environment the deploy gives its job: %v", err)
	}
	if !seededLine.MatchString(out) {
		t.Fatalf("the seed printed %q, and what an operator reads is the organisation it wrote: `seeded <name> (<id>)`", out)
	}
	if len(mail.sent) != 1 {
		t.Fatalf("the seed sent %d message(s), want 1: the invitation it writes is mailed to the address it was given", len(mail.sent))
	}
	if mail.sent[0].To != opsAddress {
		t.Errorf("the invitation went to %q, want %s: the address is the argument the deploy passes the job", mail.sent[0].To, opsAddress)
	}
	if !strings.Contains(mail.sent[0].Body, portalOrigin) {
		t.Errorf("the invitation's body holds no link to %s:\n%s\nthe origin is the deployment's own variable (FUGARO_URL), and a link built without it is one nobody can accept", portalOrigin, mail.sent[0].Body)
	}
	if err := runWith([]string{"seed-operations", opsAddress}, env, secretStoreFixture(), open); err == nil {
		t.Fatal("a second seed run was accepted: the organisation the first run wrote is what refuses it, so this passing means the first run wrote nothing")
	} else if !strings.Contains(err.Error(), "already seeded") {
		t.Fatalf("the second run failed with %q, and what an operator reads is that the deployment is already seeded", err)
	}
}

// seededLine is what the seed prints: the organisation's name and its id, and
// never the invitation's own token (this is written where a job's log is read).
var seededLine = regexp.MustCompile(`(?m)^seeded .+ \([^()]+\)$`)

// TestSeedOperationsRefusesWhatItCannotStartWith is the other half: the two ways
// this command is handed something it cannot run on, and the fact that neither of
// them reaches a cloud. The first is the finding's own — a deployment whose
// environment is short one variable — and the refusal has to name it, because
// the operator's next step is a value in the apply rather than a hunt.
func TestSeedOperationsRefusesWhatItCannotStartWith(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		env     []string
		wants   string
		because string
	}{
		{
			name:    "the address to invite is missing",
			args:    []string{"seed-operations"},
			env:     deploymentEnvironment(t),
			wants:   "seed-operations <address>",
			because: "the invocation the job is executed with is `seed-operations <address>`, and an address the deploy forgot is a run that would otherwise seed nothing and say it succeeded",
		},
		{
			name:    "a variable the deployment sets is missing",
			args:    []string{"seed-operations", opsAddress},
			env:     withoutVariable(t, "FUGARO_MAIL_FROM"),
			wants:   "FUGARO_MAIL_FROM",
			because: "the loader reads the whole deployment's environment, so a job configured short one variable is a revision that never becomes ready, and the refusal has to name the variable to set",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			open := func(context.Context, string, keyservice.Config) (deploymentSeams, error) {
				reached = true
				return deploymentSeams{}, errors.New("the case reached the deployment's own clients, which it cannot build")
			}
			// The stand-in reader, so that what refuses either case is the
			// argument or the environment and never a credential this machine
			// does not have.
			_, err := seedOutput(t, func() error {
				return runWith(tc.args, tc.env, secretStoreFixture(), open)
			})
			if err == nil {
				t.Fatalf("the seed accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("the refusal reads %q, and it should name %s: %s", err, tc.wants, tc.because)
			}
			if reached {
				t.Errorf("the seed opened the deployment's clients before %s was settled: what refuses it is the argument and the environment, and a run that reaches Firestore first is one whose mistake is found a credential later", tc.name)
			}
		})
	}
}

// withoutVariable is the environment above with one name taken out.
func withoutVariable(t *testing.T, name string) []string {
	t.Helper()
	out := []string{}
	for _, entry := range deploymentEnvironment(t) {
		if key, _, _ := strings.Cut(entry, "="); key == name {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// seedOutput runs one seed invocation and answers what it printed. The seed's own
// report is half of what an operator has, so a case reads it the way a terminal
// would.
func seedOutput(t *testing.T, run func() error) (string, error) {
	t.Helper()
	file, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("opening a pipe for the seed's output: %v", err)
	}
	saved := os.Stdout
	os.Stdout = write
	err = run()
	os.Stdout = saved
	write.Close()
	out, readErr := io.ReadAll(file)
	file.Close()
	if readErr != nil {
		t.Fatalf("reading the seed's output: %v", readErr)
	}
	return string(out), err
}
