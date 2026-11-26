package cli

// I1's launch-to-session test: one launch on an installation whose credentials
// service is the REAL one — built by credentialsServiceConfig as a deployment
// builds it, with credentials_serve.go's own wiring of the issuance store, the
// launch scope and the signer — driven from the ask for a ticket to a claimed
// custom token exchanged at Identity Toolkit for a live session.
//
// It is the one test that holds the two ends of the contract to each other:
// internal/credsvc's own end-to-end test (identity_e2e_test.go) drives the
// routes over the socket with callers it signs itself, and this one drives the
// CLI's launcher against them, so a change that leaves both halves passing
// while the ask they exchange is not the one the route reads fails here.
//
// Why Auth is replaced rather than the keys: NewGoogleKeys has no endpoint seam,
// so a configuration built for a test would fetch Google's own certificates. The
// replacement is a real credsvc.NewAuthenticator over the test's own key pair —
// the same construction a deployment gets, which keeps the two type assertions
// the routes make (installationProject's *Authenticator, the claim's
// jobVerifier) satisfied — and everything else of the configuration is the
// builder's own output, unmodified.
//
// The runs' signer is the one seam this test moves on the service's side
// (credsvc.IdentitySignerFor): what it replaces is where the service's own
// credential comes from, and the claims' checks, the payload and token.MintAt's
// re-read are the real code. Both here and at the claim the run's identity is
// the service's — this machine's own signer is never asked (the launch's red
// step, LM6).
//
// Everything is hermetic: the database, the IAM Credentials endpoint the signer
// signs over and the Identity Toolkit the custom token is exchanged at are
// internal/gcpfake's, and every value that stands for a secret is drawn at run
// time by the code under test.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

const (
	// issuerKeyID is the kid of the test's own key pair, which is the only key
	// the service's verifier publishes.
	issuerKeyID = "identity-launch-e2e"
	// issuerSigner is the account the runs' identities are signed as (D3121),
	// which the sign route and the claim both name and which the fake IAM
	// Credentials endpoint signs as. It is not a credential.
	issuerSigner = "fugaro-runs@proj-1234.iam.gserviceaccount.com"
	// issuerPerson is the launcher: the address a Google ID token's email claim
	// carries, which is the fixture's own `user:` (cloud_test.go), so the ask
	// this launcher makes names the person the service's class list admits.
	issuerPerson = "someone@example.com"
	// issuerSub is the Google account id a person's token carries in `sub`. It
	// is the width a real one has and not a round number, because a class whose
	// bound on this claim was derived from a small number would refuse every
	// launcher there is (auth_test.go's rule for the same fixture).
	issuerSub = "104329501182774832335"
)

// issuerListener is an installation's credentials service as this test reaches
// it: a listener whose address the config names and whose certificate the
// process's default transport trusts — the launch's client is built from
// http.DefaultTransport (slipClient), so that is where the trust is installed,
// for the length of the test.
//
// It is TLS because the installation's own configuration holds
// credentials_service.url to https (localcfg's validCredentialsServiceURL) and
// the launch reads that block, and because the audience every caller's ID token
// is minted for is this address: a plain server is one no config of this
// installation could name.
//
// The handler is handed over after the listener is started, because the service
// CANNOT be built before it: its own URL is the audience its verifier holds every
// caller's token to, so the address has to exist first. A call that arrives in
// between is answered by a sentence rather than by a service, which is the one
// thing this order could hide.
type issuerListener struct {
	*httptest.Server
	handler http.Handler
}

func newIssuerListener(t *testing.T) *issuerListener {
	t.Helper()
	l := &issuerListener{}
	l.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l.handler == nil {
			http.Error(w, "the service was called before it was built", http.StatusInternalServerError)
			return
		}
		l.handler.ServeHTTP(w, r)
	}))
	l.Server.StartTLS()
	t.Cleanup(l.Server.Close)

	prev := http.DefaultTransport
	http.DefaultTransport = l.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = prev })
	return l
}

// serves hands this listener the service that answers its calls.
func (l *issuerListener) serves(srv *credsvc.Server) { l.handler = srv }

// url is where this service is reached: the address the installation's config
// carries and the audience every token this test signs is minted for.
func (l *issuerListener) url() string { return strings.Replace(l.URL, "http://", "https://", 1) }

// TestLaunchToSessionThroughTheIssuer drives the whole chain on an installation
// whose credentials service is the real one: the launcher asks for its run's
// ticket and signs nothing itself, the execution carries the ticket under the
// variable the run claims it by, the run's job account claims it with its own
// Google ID token and a nonce, and the custom token it is answered is exchanged
// at Identity Toolkit for a session whose uid and claims are the record's.
func TestLaunchToSessionThroughTheIssuer(t *testing.T) {
	ctx := context.Background()
	key := newE2EKey(t, issuerKeyID)
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fixture's database client: %v", err)
	}
	// The runs' signer's own endpoint: the service signs the run's identity here
	// (IdentitySignerFor below), and the exchange verifies that signature, so a
	// token signed by anything else is refused at the exchange rather than
	// accepted by a fake that believes whatever it is told.
	iam := gcpfake.NewIAMCredentials(t)
	iam.AddSigner(issuerSigner)
	itk := gcpfake.NewIdentityToolkit(t, iam, settingsFakeAPIKey, "aurora-fp")

	svc := newIssuerListener(t)
	env := map[string]string{
		envProject:               "aurora",
		envGCPProject:            "proj-1234",
		envRegion:                "us-east5",
		envRTDBURL:               "https://aurora-fp-default-rtdb.firebaseio.com",
		envFirebaseFP:            "aurora-fp",
		envCredentialsServiceURL: svc.url(),
		// The repository the fixture's installation has a job for, which is what
		// derives both the account its runs go out as and its slug
		// (credsvc.JobAccountsFor and RepositoryTableFor).
		envJobAccounts:      `[{"provider":"github","repo":"acme/app","workflow":"web"}]`,
		envSignerAccount:    issuerSigner,
		envLauncherAccounts: `["` + issuerPerson + `"]`,
		envPersonAccounts:   `["` + issuerPerson + `"]`,
	}
	credentialsTest = &credentialsWiring{
		tokens: func(context.Context) (oauth2.TokenSource, error) {
			return oauth2.StaticTokenSource(&oauth2.Token{}), nil
		},
		secrets:  func(context.Context) (*secretmanager.Service, error) { return &secretmanager.Service{}, nil },
		database: func(context.Context, string, oauth2.TokenSource) (*rtdb.Client, error) { return client, nil },
	}
	t.Cleanup(func() { credentialsTest = nil })

	cfg, err := credentialsServiceConfig(ctx, func(k string) string { return env[k] }, io.Discard)
	if err != nil {
		t.Fatalf("the configuration a deployment of this installation builds: %v", err)
	}
	// The one account the installation's job document derives, which is the
	// account the service holds a claim to (R4) and the one this launch asks as:
	// it is read off the configuration the builder made rather than spelled here,
	// so a row that derivation lost fails the claim below rather than this line.
	if len(cfg.JobAccounts) != 1 {
		t.Fatalf("the installation's job document derived %d accounts, want the one repository's", len(cfg.JobAccounts))
	}
	auth, err := credsvc.NewAuthenticator(credsvc.AuthConfig{
		Project:          "aurora",
		FirebaseProject:  "aurora-fp",
		Audience:         svc.url(),
		JobAccounts:      []string{cfg.JobAccounts[0].Email},
		LauncherAccounts: []string{issuerPerson},
		Keys:             key,
		JobKeys:          key,
		Outcomes:         credsvc.RTDBOutcomes{DB: client},
	})
	if err != nil {
		t.Fatalf("the fixture's front door: %v", err)
	}
	cfg.Auth = auth
	srv, err := credsvc.NewServer(cfg)
	if err != nil {
		t.Fatalf("the service a deployment of this installation builds: %v", err)
	}
	svc.serves(srv)

	prevSigner := credsvc.IdentitySignerFor
	credsvc.IdentitySignerFor = func(_ context.Context, email string) (token.Signer, error) {
		return token.NewIAMSigner(email,
			oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fake-service-credential-not-a-real-one"}),
			token.WithIAMEndpoint(iam.URL))
	}
	t.Cleanup(func() { credsvc.IdentitySignerFor = prevSigner })

	r := ticketWorld(t, releaseIdentity, svc.url(), nil)
	// The launcher's own Google ID token, minted for this service's URL: no test
	// may run gcloud, and the class the route verifies is the address the token
	// carries.
	launchIdentity = func(context.Context, string) (string, error) {
		return key.sign(t, map[string]any{
			"iss":            "https://accounts.google.com",
			"aud":            svc.url(),
			"sub":            issuerSub,
			"email":          issuerPerson,
			"email_verified": true,
			"iat":            time.Now().Add(-time.Minute).Unix(),
			"exp":            time.Now().Add(30 * time.Minute).Unix(),
		}), nil
	}

	// The run is today's, and that is a real requirement of this fixture rather
	// than a convenience: the ticket route refuses a run whose launch day is
	// older than any identity's fx can reach as a run that is OVER
	// (ticketRunEnded, maxRunDays), and LM6's own run id is fixed in the past
	// because its scripted service answers whatever it is asked.
	r.spec = raceSpecAt(t, r.env, time.Now().UTC().Format("20060102-150405")+"-abcd")

	executed, err := r.launch(t)
	// The red step first (LM6): a launch that minted its run's identity locally
	// has nothing to say about the rest of this test, and the one fact that tells
	// the two apart is this machine's own signer never being asked.
	if calls := r.f.iam.SignCalls(); len(calls) != 0 {
		t.Fatalf("the launch minted %d tokens with this machine's own signer (%v), want none: the run's identity is the service's ticket", len(calls), calls)
	}
	if err != nil {
		t.Fatalf("the launch the service serves a ticket for: %v", err)
	}
	ticket := executed[identitywire.TicketEnv]
	if ticket == "" {
		t.Fatalf("the execution was handed no %s: the service issued a ticket and the launch must put it on the run (R7); its environment is %v", identitywire.TicketEnv, executed)
	}
	if got := executed[envCredentialsServiceURL]; got != svc.url() {
		t.Fatalf("the execution was handed the service %q, want %q: the run claims its ticket at the address its own environment names", got, svc.url())
	}
	if obj := ticketObject(t, r.env, r.spec.RunID); obj != "" {
		t.Fatalf("the launch wrote a token object (%q) as well as holding a ticket: a launch the service serves writes none (R7)", obj)
	}

	// The issuance record the ticket route wrote: the run is the record's and
	// never the ask's, the ticket is on it as its digest alone, and the account
	// is the one this launch asked as — which is the account the execution runs
	// as, so the claim below presents exactly the identity the record expects.
	rec, found, err := cfg.Identity.Store.Get(ctx, appSlug, r.spec.RunID)
	if err != nil || !found {
		t.Fatalf("the service wrote no issuance record for %s/%s (found=%v, err=%v): the ticket is written with it", appSlug, r.spec.RunID, found, err)
	}
	if rec.TicketHash != identitywire.Digest(ticket) {
		t.Fatalf("the record's ticket digest is not the ticket the execution carries: the service answered a ticket it did not write")
	}
	if rec.Claims.Branch != "fugaro/"+r.spec.RunID || rec.Claims.Slug != appSlug || rec.Claims.Run != r.spec.RunID {
		t.Fatalf("the record's claims name %+v, want this run on its own branch", rec.Claims)
	}

	// The claim, as the run makes it at bootstrap: the job's own Google ID token
	// in the bearer, the ticket and the nonce in the body, and nothing of the run
	// in the call.
	custom := claimTicket(t, svc.url(), key.sign(t, map[string]any{
		"iss":            "https://accounts.google.com",
		"aud":            svc.url(),
		"sub":            "1234567890",
		"email":          rec.ExpectedAccount,
		"email_verified": true,
		"iat":            time.Now().Add(-time.Minute).Unix(),
		"exp":            time.Now().Add(30 * time.Minute).Unix(),
	}), ticket, "identity-launch-nonce-0001")

	session, err := token.Exchange(ctx, token.Config{
		APIKey:      settingsFakeAPIKey,
		IdentityURL: itk.URL, SecureTokenURL: itk.URL,
		Register: func(string) {},
	}, custom)
	if err != nil {
		t.Fatalf("the custom token the service signed could not be exchanged: %v", err)
	}
	if got := session.UID(); got != rec.Claims.UID() {
		t.Fatalf("the session's uid is %q, want the record's own %q: the uid is derived from the record's path (R3)", got, rec.Claims.UID())
	}
	got := session.Claims()
	if got.Slug != rec.Claims.Slug || got.Run != rec.Claims.Run || got.FP != rec.Claims.FP ||
		got.RB != rec.Claims.RB || got.Branch != rec.Claims.Branch {
		t.Fatalf("the session's claims %+v are not the record's %+v", got, rec.Claims)
	}
	if got.FX.Unix() != rec.Claims.FX.Unix() {
		t.Fatalf("the session's identity dies at %s, want the record's %s (R4)", got.FX, rec.Claims.FX)
	}
	if got.FP != env[envProject] || got.RB != issuerPerson || got.Branch != "fugaro/"+r.spec.RunID {
		t.Fatalf("the session carries %+v, want this installation's project, this launcher and this run's branch", got)
	}
}

// claimTicket makes the claim the run makes at bootstrap and answers the custom
// token the service signed, refusing anything but an answer of the contract's:
// this test's whole subject is that the token it gets back is one Identity
// Toolkit exchanges, so a refusal here is read as one and never carried on.
func claimTicket(t *testing.T, serviceURL, jobToken, ticket, nonce string) string {
	t.Helper()
	body, err := json.Marshal(identitywire.ClaimRequest{Ticket: ticket, Nonce: nonce})
	if err != nil {
		t.Fatalf("the claim's body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimSuffix(serviceURL, "/")+identitywire.ClaimPath, strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("the claim's request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+jobToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the claim could not be made: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTicketAnswerBytes+1))
	if err != nil {
		t.Fatalf("the claim's answer could not be read: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the run's own claim was refused (%d): %s", resp.StatusCode, data)
	}
	var out identitywire.ClaimResponse
	if err := json.Unmarshal(data, &out); err != nil || out.Token == "" {
		t.Fatalf("the claim's answer is not one of this contract's: %v (%s)", err, data)
	}
	return out.Token
}
