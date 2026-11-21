// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The deployment's configuration (portal plan P1.7, design
// docs/key-signing-service/spec.md S3.5 and S3.7): what the two programs read
// out of the process's environment and Secret Manager before either one starts
// serving.
//
// **Why the reading is here and not in each program.** Config's own comment
// says the deploy's shape — environment variables, Secret Manager, a config
// file — is S3.5's and S3.7's and that this package builds the Config rather
// than decoding one. P1.7 is where that lands, and it lands in the package for
// the reason the Config itself is there: the fields, their meanings and what
// New refuses about them are this package's, and a decoder in a program would
// be a second statement of all three that nothing keeps in step. What the
// programs add is only what is theirs — the clients, the listeners and the
// `seed-operations` subcommand (cmd/portal-api, cmd/minter).
//
// **Strict, in both directions.** A variable this file does not know is refused
// rather than skipped: the environment of a service is written by whoever
// deploys it (P9.2's script, the owner's `gcloud run deploy`), and a name that
// is misspelled or belongs to a field that moved would otherwise be a
// deployment running on a default nobody chose — a value that is silently not
// what the operator set is exactly the failure this refuses. The refusal names
// the variable it did not read, so the operator's next step is a rename and not
// a hunt. What is *not* refused is a variable without this prefix (`PATH`, the
// platform's own `PORT`, `K_SERVICE`): the process's environment is not this
// service's to police.
//
// The two programs read different sets, and each refuses the other's: the
// minter's names three variables and none of the secrets, so a deployment that
// handed the signer the API's environment would be refused at the start rather
// than run with a merchant secret and a mail key in the one process that must
// hold neither (Check 174, §3.3, .claude/rules/ee.md R5). That is the machine's
// half of "the minter holds no applicant's secret", and
// TestMinterBinaryHoldsNoMailOrMerchantSecret is the other half.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/option"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

// deployVarPrefix is what every variable this service reads starts with. It is
// the whole of the strictness rule: a FUGARO_ variable that is not one of the
// names below is a mistake somebody made, and anything without the prefix
// belongs to something else.
const deployVarPrefix = "FUGARO_"

// The API's own variables, one per thing the deploy sets. The names are the
// fields' and not the pages': an operator reading `gcloud run deploy` sees the
// same words as a reader of Config.
const (
	varEnvironment     = "FUGARO_ENVIRONMENT"
	varKeys            = "FUGARO_KEYS"
	varURL             = "FUGARO_URL"
	varPlans           = "FUGARO_PLANS"
	varFreeMailDomains = "FUGARO_FREE_MAIL_DOMAINS"
	varVerificationTTL = "FUGARO_VERIFICATION_TTL"
	varCodeHosts       = "FUGARO_CODE_HOSTS"
	varMaxRepositories = "FUGARO_MAX_REPOSITORIES"
	varThrottle        = "FUGARO_THROTTLE"
	varInviteRate      = "FUGARO_INVITE_RATE"
	varAdminAccount    = "FUGARO_ADMIN_ACCOUNT"
	varAdminTokenTTL   = "FUGARO_ADMIN_TOKEN_TTL"
	varPaddlePriceID   = "FUGARO_PADDLE_PRICE_ID"
	varSchedulerAudien = "FUGARO_SCHEDULER_AUDIENCE"
	varSchedulerAcct   = "FUGARO_SCHEDULER_ACCOUNT"
	varSealerKeyName   = "FUGARO_SEALER_KEY_NAME"
	varTenantsColl     = "FUGARO_TENANTS_COLLECTION"
	varTermsVersion    = "FUGARO_TERMS_VERSION"
	varSecrets         = "FUGARO_SECRETS"
	varProject         = "FUGARO_PROJECT"
	varMinterURL       = "FUGARO_MINTER_URL"
	varMailFrom        = "FUGARO_MAIL_FROM"
	varNotifyAddress   = "FUGARO_NOTIFY_ADDRESS"
)

// The minter's own three, and the whole of what it reads: the environment it
// signs in, the key ids, and the KMS key version it signs with. There is no
// secret in the list, which is what the identity boundary is made of in code
// rather than in a comment (§3.3).
const (
	varSignerKeyVersion = "FUGARO_SIGNER_KEY_VERSION"
)

// apiVariables is every variable LoadConfig reads, and minterVariables every one
// LoadMinterConfig reads. A variable outside the set the loader was called with
// is a refusal, which is what makes the two sets two fences.
var (
	apiVariables = []string{
		varEnvironment, varKeys, varURL, varPlans, varFreeMailDomains, varVerificationTTL,
		varCodeHosts, varMaxRepositories, varThrottle, varInviteRate, varAdminAccount,
		varAdminTokenTTL, varPaddlePriceID, varSchedulerAudien, varSchedulerAcct, varSealerKeyName,
		varTenantsColl,
		varTermsVersion, varSecrets, varProject, varMinterURL, varMailFrom, varNotifyAddress,
	}
	minterVariables = []string{varEnvironment, varKeys, varSignerKeyVersion}
)

// Secrets reads one Secret Manager secret's current version. It is a seam so
// that a test loads a configuration without a cloud
// (config_test.go), and SecretManager below is the deployment's.
//
// The name it is handed is a Secret Manager secret id and never a path: which
// project the secret lives in is the reader's (P1.5 creates the containers in
// the same project as the service), so a configuration cannot name a secret in
// somebody else's project.
type Secrets interface {
	Secret(ctx context.Context, name string) ([]byte, error)
}

// SecretManager is the deployment's reader: the Secret Manager secret the
// deploy filled (P0.2), read at `versions/latest`.
//
// The latest version and not a pinned one, because rotating a secret is adding
// a version: an installation pinned to a version number would keep serving an
// old merchant secret or an old mail key until an apply changed the
// configuration, which is the one thing a rotation is for. The container's
// accessor is the API's account alone (P1.5), so this reads nothing a second
// identity could.
type SecretManager struct {
	service *secretmanager.Service
	project string
}

// NewSecretManager builds the reader one deployment reads its secrets with. The
// credentials are the deploy's own — on Cloud Run, the service's account — and
// the project is the one the secrets were created in.
func NewSecretManager(ctx context.Context, project string, opts ...option.ClientOption) (*SecretManager, error) {
	if strings.TrimSpace(project) == "" {
		return nil, errors.New("keyservice: a Secret Manager reader names no project, and a secret is read from a project's own container")
	}
	service, err := secretmanager.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("keyservice: the Secret Manager client: %w", err)
	}
	return &SecretManager{service: service, project: strings.TrimSpace(project)}, nil
}

// Secret reads one secret's current version.
//
// The payload's own CRC32C is checked when Secret Manager wrote one, which it
// does for every version added with `gcloud secrets versions add`: what arrives
// is the deployment's merchant secret or mail key, and a value that arrived
// damaged is a mail path or a webhook that fails in a way nobody can read from
// either side.
func (s *SecretManager) Secret(ctx context.Context, name string) ([]byte, error) {
	id := strings.TrimSpace(name)
	if id == "" {
		return nil, errors.New("keyservice: the configuration names no secret to read")
	}
	answer, err := s.service.Projects.Secrets.Versions.
		Access("projects/" + s.project + "/secrets/" + id + "/versions/latest").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("keyservice: reading the secret %s: %w", id, err)
	}
	if answer.Payload == nil {
		return nil, fmt.Errorf("keyservice: the secret %s answered no payload, so the container holds no version", id)
	}
	value, err := base64.StdEncoding.DecodeString(answer.Payload.Data)
	if err != nil {
		return nil, fmt.Errorf("keyservice: the secret %s is not base64, which is how Secret Manager carries a value: %w", id, err)
	}
	if len(value) == 0 {
		return nil, fmt.Errorf("keyservice: the secret %s is empty, and a deployment that reads an empty secret is one that would sign with nothing", id)
	}
	if counted := answer.Payload.DataCrc32c; counted != 0 && counted != int64(crc32.Checksum(value, crcTable)) {
		return nil, fmt.Errorf("keyservice: the secret %s is not the version Secret Manager counted, so what was read is not what was stored", id)
	}
	return value, nil
}

// Deploy is what the API's program needs beyond the Config: the values that
// wire the deployment's own clients together rather than describing the intake.
//
// They are here and not on Config because Config is the intake's own shape,
// written by P1.0 and P2.1 (the plan's shared-files table), and a task of this
// level adds no field to it: what these name — the project the sign-in verifier
// and the store reach, the address of the other half of the deployment, the
// sender a mail goes out as, and where the owner is told a request is waiting —
// is the deployment's wiring, and the program that builds the clients is where
// it belongs.
type Deploy struct {
	// Project is the licence project: the audience the sign-in verifier pins,
	// the project the portal's documents live in, and the one the minter's
	// service is reached in (P0.1, P1.5).
	Project string
	// MinterURL is the minter service's own address, which is the URL the API
	// posts a mint to and the audience the ID token it presents is minted for
	// (§3.2, minterclient.go).
	MinterURL string
	// MailFrom is the address the deployment's mail goes out as. It is the
	// sender Resend has verified for the deployment's domain, and a mail path
	// with none is refused where the mailer is built (resend.go).
	MailFrom string
	// NotifyAddress is where the owner is told a request is waiting in the
	// manual queue (§4.5). It is the deployment's, like the sender above.
	NotifyAddress string
	// SchedulerAccount is the service account Cloud Scheduler's jobs run as
	// (F33), which is half of what NewSchedulerVerifier checks a job's token
	// against: without it the verifier would admit a token any other job in the
	// project minted. It is here rather than on Config for the reason the rest
	// of this struct is — the verifier it builds is the deployment's wiring —
	// and it is the one value the scheduler's own route cannot be given from
	// anywhere else.
	SchedulerAccount string
}

// DeployProject answers the licence project from an environment, and it exists
// for one ordering reason: the Secret Manager reader is built with the project
// before the configuration is read, because reading a secret is how half of that
// configuration arrives. It reads the same variable LoadConfig does, so the two
// cannot disagree about which project this is, and it refuses an environment
// that names none rather than reaching for a default the deployment never chose
// (the platform fills GOOGLE_CLOUD_PROJECT, and a service that fell back to it
// would read another project's secrets the day the two differed).
func DeployProject(environ []string) (string, error) {
	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if ok && name == varProject {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed, nil
			}
			return "", fmt.Errorf("keyservice: %s is set to nothing, so there is no project to read the deployment's secrets from", varProject)
		}
	}
	return "", fmt.Errorf("keyservice: the environment does not set %s, and the deployment's secrets live in one project's own containers", varProject)
}

// LoadConfig builds the portal API's Config and Deploy from the process's own
// environment and the deployment's secrets.
//
// Every refusal it makes is a deployment that would otherwise have run: a
// variable it does not know, a value it cannot read, a secret that is not
// there, and a field New would refuse anyway are all answered here, before the
// listener opens, so the operator sees the mistake in the revision's startup
// rather than at the first customer's request.
func LoadConfig(ctx context.Context, environ []string, secrets Secrets) (Config, Deploy, error) {
	env, err := readDeployEnv(environ, apiVariables)
	if err != nil {
		return Config{}, Deploy{}, err
	}
	if secrets == nil {
		return Config{}, Deploy{}, errors.New("keyservice: this deployment has no Secret Manager reader, so it can read no merchant secret and no mail key")
	}
	environment, err := env.environment()
	if err != nil {
		return Config{}, Deploy{}, err
	}
	keys, err := env.keys()
	if err != nil {
		return Config{}, Deploy{}, err
	}
	plans, err := env.plans()
	if err != nil {
		return Config{}, Deploy{}, err
	}
	verificationTTL, err := env.duration(varVerificationTTL)
	if err != nil {
		return Config{}, Deploy{}, err
	}
	adminTokenTTL, err := env.duration(varAdminTokenTTL)
	if err != nil {
		return Config{}, Deploy{}, err
	}
	maxRepositories, err := env.integer(varMaxRepositories)
	if err != nil {
		return Config{}, Deploy{}, err
	}
	throttle, err := env.limits(varThrottle)
	if err != nil {
		return Config{}, Deploy{}, err
	}
	inviteRate, err := env.limits(varInviteRate)
	if err != nil {
		return Config{}, Deploy{}, err
	}
	held, err := env.secretNames()
	if err != nil {
		return Config{}, Deploy{}, err
	}
	read := func(role string) ([]byte, error) {
		id := held[role]
		if id == "" {
			return nil, fmt.Errorf("keyservice: the deployment names no secret for %s, and a service that starts without it would fail at the first request that needs one", role)
		}
		value, err := secrets.Secret(ctx, id)
		if err != nil {
			return nil, err
		}
		return value, nil
	}
	merchant, err := read(secretMerchant)
	if err != nil {
		return Config{}, Deploy{}, err
	}
	admin, err := read(secretAdmin)
	if err != nil {
		return Config{}, Deploy{}, err
	}
	paddleKey, err := read(secretPaddleKey)
	if err != nil {
		return Config{}, Deploy{}, err
	}
	resendKey, err := read(secretResendKey)
	if err != nil {
		return Config{}, Deploy{}, err
	}
	// The error DSN is the one secret that may be absent, and its absence is
	// P1.6's "off": a deployment that reports nowhere is a deployment the SPA
	// still works on, while a missing merchant secret is one that can serve
	// nothing at all.
	sentry := ""
	if id := held[secretSentryDSN]; id != "" {
		dsn, err := secrets.Secret(ctx, id)
		if err != nil {
			return Config{}, Deploy{}, err
		}
		sentry = strings.TrimSpace(string(dsn))
	}
	cfg := Config{
		Environment:       environment,
		Keys:              keys,
		URL:               env.value(varURL),
		FreeSeat:          plans.FreeSeat.plan(),
		OpenSource:        plans.OpenSource.plan(),
		Paid:              planOf(plans.Paid),
		FreeMailDomains:   env.list(varFreeMailDomains),
		VerificationTTL:   verificationTTL,
		MerchantSecret:    merchant,
		CodeHosts:         env.list(varCodeHosts),
		MaxRepositories:   maxRepositories,
		Throttle:          throttle,
		InviteRate:        inviteRate,
		AdminAccount:      env.value(varAdminAccount),
		AdminSecret:       admin,
		AdminTokenTTL:     adminTokenTTL,
		MintCaps:          plans.caps(),
		PagePlans:         plans.page(),
		PaddleAPIKey:      paddleKey,
		PaddlePriceID:     env.value(varPaddlePriceID),
		ResendKey:         resendKey,
		SentryDSN:         sentry,
		SchedulerAudience: env.value(varSchedulerAudien),
		SealerKeyName:     env.value(varSealerKeyName),
		TenantsCollection: env.value(varTenantsColl),
		TermsVersion:      env.value(varTermsVersion),
	}
	deploy := Deploy{
		Project:       env.value(varProject),
		MinterURL:     env.value(varMinterURL),
		MailFrom:      env.value(varMailFrom),
		NotifyAddress: env.value(varNotifyAddress),

		SchedulerAccount: env.value(varSchedulerAcct),
	}
	return cfg, deploy, nil
}

// MinterConfig is the minter's own configuration, and the whole of it: the
// environment it signs in, the two key ids, and the KMS key version its signer
// names. There is no seam here for a store, a mail path or the merchant of
// record, because the minter holds none of them (§3.2, Check 174).
type MinterConfig struct {
	Environment Environment
	Keys        Keys
	// SignerKeyVersion is the KMS key version the minter signs with, as the
	// resource name Cloud KMS answers to
	// (`projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>/cryptoVersions/<v>`).
	SignerKeyVersion string
}

// LoadMinterConfig builds the minter's configuration from its own environment.
//
// It refuses the API's variables rather than ignoring them, and that is the
// point of the second variable set: the minter's deployment is the one process
// that must hold no merchant secret, no mail key and no store, so an
// environment built for the API is a refusal at the start rather than a signer
// that quietly carries a customer's secrets
// (TestMinterBinaryHoldsNoMailOrMerchantSecret).
func LoadMinterConfig(environ []string) (MinterConfig, error) {
	env, err := readDeployEnv(environ, minterVariables)
	if err != nil {
		return MinterConfig{}, err
	}
	environment, err := env.environment()
	if err != nil {
		return MinterConfig{}, err
	}
	keys, err := env.keys()
	if err != nil {
		return MinterConfig{}, err
	}
	return MinterConfig{
		Environment:      environment,
		Keys:             keys,
		SignerKeyVersion: env.value(varSignerKeyVersion),
	}, nil
}

// The roles a deployment names a Secret Manager secret for. They are the
// config's fields' names and not the containers' ids: which container holds
// which secret is the deploy's (P0.2's `gcloud secrets versions add`), and a
// rename of one is a value in FUGARO_SECRETS and not a change here.
const (
	secretMerchant  = "merchant"
	secretAdmin     = "admin"
	secretPaddleKey = "paddle_api_key"
	secretResendKey = "resend_api_key"
	secretSentryDSN = "sentry_dsn"
)

// knownSecretRoles is every role FUGARO_SECRETS may name.
var knownSecretRoles = []string{secretMerchant, secretAdmin, secretPaddleKey, secretResendKey, secretSentryDSN}

// deployEnv is one program's environment, read: the values it names, and the
// set it was read against.
type deployEnv struct {
	values map[string]string
}

// readDeployEnv reads an environment against the set of names one program
// reads, refusing both halves of a mistake: a variable of this service's that
// is not in the set, and a name in the set that is not there.
//
// A name that is present but empty is refused here as well, because the two are
// one mistake at the operator's end and the second reads worse: an empty
// FUGARO_URL is a deployment whose mailed links point nowhere, and the refusal
// names the variable either way.
func readDeployEnv(environ, known []string) (deployEnv, error) {
	allowed := make(map[string]bool, len(known))
	for _, name := range known {
		allowed[name] = true
	}
	env := deployEnv{values: map[string]string{}}
	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			return deployEnv{}, fmt.Errorf("keyservice: the environment holds %q, which is not a NAME=value pair", entry)
		}
		if !strings.HasPrefix(name, deployVarPrefix) {
			continue
		}
		if !allowed[name] {
			return deployEnv{}, fmt.Errorf("keyservice: the environment sets %s, which this program does not read: a variable spelled wrongly or set for the other program would otherwise run as a default nobody chose", name)
		}
		if strings.TrimSpace(value) == "" {
			return deployEnv{}, fmt.Errorf("keyservice: the environment sets %s to nothing, and a variable this service reads has no empty meaning", name)
		}
		env.values[name] = value
	}
	for _, name := range known {
		if _, ok := env.values[name]; !ok {
			return deployEnv{}, fmt.Errorf("keyservice: the environment does not set %s, and the deployment cannot start without it", name)
		}
	}
	return env, nil
}

func (e deployEnv) value(name string) string { return strings.TrimSpace(e.values[name]) }

// list reads a comma-separated variable: the two lists this configuration has
// (the free-mail domains and the code hosts) are short, ordered and hand-kept,
// and one variable each is the shape an operator can read in a deploy script.
func (e deployEnv) list(name string) []string {
	out := []string{}
	for _, part := range strings.Split(e.values[name], ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (e deployEnv) duration(name string) (time.Duration, error) {
	span, err := time.ParseDuration(e.value(name))
	if err != nil {
		return 0, fmt.Errorf("keyservice: %s is not a duration such as 72h: %w", name, err)
	}
	return span, nil
}

func (e deployEnv) integer(name string) (int, error) {
	number, err := strconv.Atoi(e.value(name))
	if err != nil {
		return 0, fmt.Errorf("keyservice: %s is not a whole number: %w", name, err)
	}
	return number, nil
}

// limits reads one rate, written `<requests>/<window>`: `20/1h` is twenty
// requests in an hour. It is a spelling of its own rather than a JSON document
// because a rate is one line an operator reads in a deploy script.
func (e deployEnv) limits(name string) (Limits, error) {
	rate, window, ok := strings.Cut(e.value(name), "/")
	if !ok {
		return Limits{}, fmt.Errorf("keyservice: %s is not a rate such as 20/1h", name)
	}
	requests, err := strconv.Atoi(strings.TrimSpace(rate))
	if err != nil {
		return Limits{}, fmt.Errorf("keyservice: %s names %q requests, which is not a whole number", name, rate)
	}
	span, err := time.ParseDuration(strings.TrimSpace(window))
	if err != nil {
		return Limits{}, fmt.Errorf("keyservice: %s names the window %q, which is not a duration such as 1h", name, window)
	}
	return Limits{Requests: requests, Window: span}, nil
}

func (e deployEnv) environment() (Environment, error) {
	environment := Environment(e.value(varEnvironment))
	switch environment {
	case Production, Staging:
		return environment, nil
	}
	return "", fmt.Errorf("keyservice: %s is %q, and the two environments are %s and %s", varEnvironment, environment, Production, Staging)
}

// keys reads the two key ids. They are one JSON object rather than two
// variables because they are one value — the pair the minter signs in one
// environment with — and a deployment that set one of two would be one whose
// keys half-configuration is visible at the start.
func (e deployEnv) keys() (Keys, error) {
	var held struct {
		Production string `json:"production"`
		Staging    string `json:"staging"`
	}
	if err := strictJSON(e.values[varKeys], &held); err != nil {
		return Keys{}, fmt.Errorf("keyservice: %s is not the two key ids: %w", varKeys, err)
	}
	return Keys{Production: held.Production, Staging: held.Staging}, nil
}

// deployPlans is the JSON shape of FUGARO_PLANS: the plans the routes mint
// under, the plans no route reads, and D454's per-tier mint caps.
//
// It is one document because the fields are one decision — what this deployment
// sells and what it allows — and because the alternative is five variables that
// can be set apart from each other. The document decodes strictly, so a key
// that is misspelled is a refusal rather than a plan that silently kept its
// zero value.
type deployPlans struct {
	FreeSeat   *deployPlan
	OpenSource *deployPlan
	Paid       deployPlan
	Page       []deployPlan
	// lim is the document's own caps, with each window already read as a
	// duration: caps() is what hands them to the Config, and keeping the
	// document's shape until then is what makes the reading a step an operator
	// can point at when a window is refused.
	lim map[string]deployLimit
}

type deployPlan struct {
	Name     string   `json:"name"`
	Tier     string   `json:"tier"`
	Features []string `json:"features"`
	Seats    int      `json:"seats"`
	TermDays int      `json:"term_days"`
}

// plan is the Config's own shape of one plan. The document's shape is this
// file's and the Config's is P1.0's, so the two are kept apart here rather than
// by tagging the Config's fields: a field added to Plan is a field this loader
// has to be told about, which is the right direction for a value an operator
// sets.
func (p deployPlan) plan() *Plan {
	return &Plan{Name: p.Name, Tier: p.Tier, Features: p.Features, Seats: p.Seats, TermDays: p.TermDays}
}

func planOf(p deployPlan) Plan { return *p.plan() }

func (p deployPlans) page() []Plan {
	out := make([]Plan, 0, len(p.Page))
	for _, one := range p.Page {
		out = append(out, planOf(one))
	}
	return out
}

func (p deployPlans) caps() map[string]Limits {
	out := make(map[string]Limits, len(p.lim))
	for tier, cap := range p.lim {
		out[tier] = Limits{Requests: cap.Requests, Window: cap.window}
	}
	return out
}

type deployLimit struct {
	Requests int    `json:"requests"`
	Window   string `json:"window"`
	window   time.Duration
}

// plans reads FUGARO_PLANS into the Config's own shapes, refusing a document
// that is not one: New re-checks what it is handed, but a missing plan or a cap
// that is not a rate is a deployment that does not start, and the operator
// reads the refusal here with the variable's name in it.
func (e deployEnv) plans() (deployPlans, error) {
	var held struct {
		FreeSeat   *deployPlan            `json:"free_seat"`
		OpenSource *deployPlan            `json:"open_source"`
		Paid       *deployPlan            `json:"paid"`
		Page       []deployPlan           `json:"page"`
		MintCaps   map[string]deployLimit `json:"mint_caps"`
	}
	if err := strictJSON(e.values[varPlans], &held); err != nil {
		return deployPlans{}, fmt.Errorf("keyservice: %s is not the deployment's plans: %w", varPlans, err)
	}
	if held.FreeSeat == nil || held.OpenSource == nil || held.Paid == nil {
		return deployPlans{}, fmt.Errorf("keyservice: %s names no free_seat, open_source or paid plan, and the intake mints under all three", varPlans)
	}
	caps := make(map[string]deployLimit, len(held.MintCaps))
	for tier, cap := range held.MintCaps {
		span, err := time.ParseDuration(cap.Window)
		if err != nil {
			return deployPlans{}, fmt.Errorf("keyservice: %s caps the tier %q at a window of %q, which is not a duration such as 1h", varPlans, tier, cap.Window)
		}
		if cap.Requests < 1 || span <= 0 {
			return deployPlans{}, fmt.Errorf("keyservice: %s caps the tier %q at %d mint(s) per %s, and a cap of none is not a limit (D454)", varPlans, tier, cap.Requests, span)
		}
		caps[tier] = deployLimit{Requests: cap.Requests, Window: cap.Window, window: span}
	}
	return deployPlans{
		FreeSeat:   held.FreeSeat,
		OpenSource: held.OpenSource,
		Paid:       *held.Paid,
		Page:       held.Page,
		lim:        caps,
	}, nil
}

// secretNames reads FUGARO_SECRETS: one Secret Manager secret id per role. A
// role this file does not know is refused, for the reason every other name is:
// the operator meant something by it, and a secret nobody read is a customer's
// checkout or a person's invitation that fails with no mention of why.
func (e deployEnv) secretNames() (map[string]string, error) {
	var held map[string]string
	if err := strictJSON(e.values[varSecrets], &held); err != nil {
		return nil, fmt.Errorf("keyservice: %s is not a set of Secret Manager secret names: %w", varSecrets, err)
	}
	for role := range held {
		if !slicesContains(knownSecretRoles, role) {
			return nil, fmt.Errorf("keyservice: %s names the secret %q, which this program does not read: the roles it knows are %s", varSecrets, role, strings.Join(knownSecretRoles, ", "))
		}
	}
	return held, nil
}

// strictJSON decodes one JSON value, refusing every field it does not know.
// Config and the wire formats decode strictly (CLAUDE.md), and a configuration
// document is the same bargain: a key the operator thinks they set and this
// service never read is worse than a startup that says so.
func strictJSON(raw string, into any) error {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("it holds more than one JSON value")
	}
	return nil
}

func slicesContains(held []string, want string) bool {
	for _, candidate := range held {
		if candidate == want {
			return true
		}
	}
	return false
}
