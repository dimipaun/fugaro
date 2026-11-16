// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// The portal API's program (portal plan P1.7, design
// docs/design/key-signing-service.md §3.1, S3.5, S3.7).
//
// It is the deployment's public half: it reads its configuration out of the
// environment and Secret Manager, builds the seams the routes reach, and serves
// Service.Handler() on the port the platform gives it. It holds no signer — the
// signature comes from the minter's service over the platform-authenticated call
// (§3.2, minterclient.go) — and that is the boundary the two programs are split
// along: this process holds the store, the mail key and the merchant's secret,
// and the other holds the key ring (§3.3, Check 174).
//
// **Three seams have no implementation in this repository.** CodeHost (§4.4's
// repository check), Owner (§6's admin page identity) and Proofs are declared by
// P1.1 and P1.2 and nothing writes them:
//
//   - CodeHost and Proofs belong to the open-source programme, and **D452 is
//     open**: Proofs is nil here, so §4.4's check refuses every application
//     before any repository is read (api_opensource.go), which is what a
//     deployment with no proof check is supposed to do. The code host is a
//     refusing one for the same reason — it is reached by nothing today, and a
//     stand-in that answered `public: true` would be a licence check that passed
//     on a repository nobody read.
//   - Owner is what the admin page reads to find the account a request is signed
//     in as. The deployment's identity provider is the platform's (IAP in front
//     of the page, §3.3's identity table) and nothing here reads it yet, so this
//     refuses: the page renders as "no account", and P9.2's deploy script has to
//     wire the real one before the Licensor can mint from it. A stand-in that
//     named the admin account would be a page anyone who can reach the URL may
//     mint from, which is the one thing §6's row is about.
//
// The rest is real wiring, and it is what P1.7 is for: the document store, the
// mail path, the merchant of record, the two token verifiers and the handover's
// sealer, each built from its own secret and its own variable.
//
// **The one subcommand.** `seed-operations <address>` writes the operations
// organisation and its first invitation through P2.2's SeedOperations and
// refuses once the organisation exists (invites.go). It is a subcommand of this
// program rather than a program of its own because it needs nothing the API does
// not already build, and because a second binary that writes the portal's
// records would be a second thing to deploy.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	kms "cloud.google.com/go/kms/apiv1"

	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	"github.com/dimipaun/fugaro/ee/keyservice"
	"github.com/dimipaun/fugaro/ee/keyservice/docstore"
)

// portalDatabase is the Firestore database the portal's records live in (P1.5).
// It is the database's own P1.5 name rather than a variable: the deploy creates
// one, and a deployment pointed at another would be one whose records are in a
// database nobody backs up.
const portalDatabase = "portal"

const defaultPort = "8080"

// shutdownGrace is how long a request in flight has to finish once the platform
// asks the container to stop. A request here can be a merchant event that mints,
// so the grace is longer than the minter's.
const shutdownGrace = 30 * time.Second

// mintTimeout bounds one call to the minter's service. A mint is a signing call
// and a JSON answer between two services in one project; a call that has not
// answered by then is one the merchant's retry will make again.
const mintTimeout = 30 * time.Second

func main() {
	if err := run(); err != nil {
		log.Fatalf("portal-api: %v", err)
	}
}

// run builds the deployment, serves it, and answers the one subcommand.
func run() error {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "seed-operations" {
		return seedOperations(args[1:])
	}
	if len(args) > 0 {
		return fmt.Errorf("this program serves, and its one subcommand is `seed-operations <address>`; %q is neither", args[0])
	}
	ctx := context.Background()
	svc, cleanup, err := build(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	return serve(svc.Handler())
}

// build is the whole of the wiring, and it is one function so that the seed
// subcommand reaches a service built exactly the way the server's is: what the
// seed writes has to be visible to the API that serves it, and two assemblies
// could differ in a database name.
func build(ctx context.Context) (*keyservice.Service, func(), error) {
	project, err := keyservice.DeployProject(os.Environ())
	if err != nil {
		return nil, nil, err
	}
	secrets, err := keyservice.NewSecretManager(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	cfg, deploy, err := keyservice.LoadConfig(ctx, os.Environ(), secrets)
	if err != nil {
		return nil, nil, err
	}
	firestoreClient, err := firestore.NewClientWithDatabase(ctx, project, portalDatabase)
	if err != nil {
		return nil, nil, fmt.Errorf("the Firestore client: %w", err)
	}
	kmsClient, err := kms.NewKeyManagementClient(ctx)
	if err != nil {
		firestoreClient.Close()
		return nil, nil, fmt.Errorf("the Cloud KMS client: %w", err)
	}
	cleanup := func() {
		kmsClient.Close()
		firestoreClient.Close()
	}
	// One database, two faces of it: the licence records (firestore_store.go)
	// and the portal's own documents (docstore_docs.go). A deployment that gave
	// the two seams two stores would commit a binding's withdrawal onto records
	// no reader sees (api.go's Tx.Revoke).
	db := docstore.NewFirestore(firestoreClient)
	mailer, err := keyservice.NewResendMailer(cfg.ResendKey, deploy.MailFrom, nil)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	signIn, err := keyservice.NewPortalVerifier(deploy.Project, mcpauth.NewSecureTokenKeys())
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	scheduler, err := keyservice.NewSchedulerVerifier(deploy.SchedulerAccount, cfg.SchedulerAudience, mcpauth.NewSecureTokenKeys())
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	paddle, err := keyservice.NewPaddleClient(cfg.PaddleAPIKey, cfg.Environment)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	sealer, err := keyservice.NewKMSSealer(kmsClient, cfg.SealerKeyName)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	minter := keyservice.RemoteMints{
		URL:    deploy.MinterURL,
		Client: &http.Client{Timeout: mintTimeout},
		Tokens: keyservice.MetadataTokens{},
	}
	svc, err := keyservice.New(cfg, keyservice.Deps{
		Store:     keyservice.NewDocStore(db),
		Docs:      keyservice.NewDocStoreDocs(db),
		Mints:     minter,
		MintsV1:   minter,
		Mailer:    mailer,
		CodeHost:  refusingCodeHost{},
		Owner:     refusingOwner{},
		Notifier:  ownerMail{mailer: mailer, to: deploy.NotifyAddress},
		SignIn:    keyservice.PortalSignIn{Verifier: signIn},
		Paddle:    paddle,
		Scheduler: jobSignIn{verifier: scheduler, account: deploy.SchedulerAccount},
		Sealer:    sealer,
		Now:       time.Now,
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return svc, cleanup, nil
}

// seedOperations writes the operations organisation and its first invitation
// (§4.5, P2.2's SeedOperations), and a second run is refused by the service
// rather than by this program: what "already seeded" means is the organisation
// record's, and a program that read it first would be a second statement of the
// rule.
func seedOperations(args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return errors.New("seed-operations writes the operations organisation and its first invitation, and it needs the one address to invite: `seed-operations <address>`")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	svc, cleanup, err := build(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	org, err := svc.SeedOperations(ctx, strings.TrimSpace(args[0]))
	if err != nil {
		return err
	}
	// The id and the name and never the invitation's own token: this runs in an
	// operator's terminal, and what it prints is what the next step names.
	fmt.Printf("seeded %s (%s)\n", org.Name, org.ID)
	return nil
}

// serve runs the API on the platform's port until the platform asks the
// container to stop, then lets what is in flight finish.
func serve(handler http.Handler) error {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	done := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		done <- err
	}()
	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	select {
	case err := <-done:
		return err
	case <-stop.Done():
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		return server.Shutdown(ctx)
	}
}

// refusingCodeHost is §4.4's repository check with no code host behind it. It
// answers what the interface's own ErrRepository means — a repository this
// service has no answer for — and it is what a deployment with no code-host
// credentials should do rather than guess: what it must never do is answer that
// a repository is public and carries an approved licence, which is an
// open-source key minted from a repository nobody read.
//
// It is reached by nothing today: D452 is open, Proofs is nil, and §4.4's check
// refuses every application before a host is asked (api_opensource.go).
type refusingCodeHost struct{}

func (refusingCodeHost) Repository(context.Context, string, string) (keyservice.Repository, error) {
	return keyservice.Repository{}, keyservice.ErrRepository
}

// refusingOwner is §6's signed-in account with no identity provider behind it.
// It answers "no account", which is what the page does with anybody it cannot
// name: it renders read-only, and nothing there mints.
//
// This is the deployment's one known gap, and it is named in this file's head
// rather than papered over: the real Owner reads the platform's own assertion
// about the request (IAP, §3.3's identity table), and until it is wired the
// Licensor reaches the page but cannot mint from it.
type refusingOwner struct{}

func (refusingOwner) SignedIn(context.Context) (string, bool) { return "", false }

// jobSignIn is SchedulerVerifier behind the API's own seam (api.go's
// Scheduler), which is the other place this deployment's own verifier meets a
// route's: the seam answers the account beside the error and the verifier
// checked it, so what it answers is the account the deployment configured the
// job to run as — the token's own spelling of it has already been compared to
// that one and admitted nothing else.
type jobSignIn struct {
	verifier *keyservice.SchedulerVerifier
	account  string
}

func (j jobSignIn) Verify(ctx context.Context, bearer string) (string, error) {
	if err := j.verifier.Verify(ctx, bearer); err != nil {
		return "", err
	}
	return j.account, nil
}

// ownerMail tells the owner a request is waiting in the manual queue (§4.5). It
// is the deployment's own mail path and not a second one: the notification goes
// out as the same verified sender the applicant's mail does, to the address the
// deploy names.
type ownerMail struct {
	mailer keyservice.Mailer
	to     string
}

func (o ownerMail) Notify(ctx context.Context, alert keyservice.Alert) error {
	return o.mailer.Send(ctx, keyservice.Message{To: o.to, Subject: alert.Subject, Body: alert.Body})
}
