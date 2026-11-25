// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// The portal API's program (portal plan P1.7, design
// docs/key-signing-service/spec.md §3.1, S3.5, S3.7).
//
// It is the deployment's public half: it reads its configuration out of the
// environment and Secret Manager, builds the seams the routes reach, and serves
// Service.Handler() on the port the platform gives it. It holds no signer — the
// signature comes from the minter's service over the platform-authenticated call
// (§3.2, minterclient.go) — and that is the boundary the two programs are split
// along: this process holds the store, the mail key and the merchant's secret,
// and the other holds the key ring (§3.3, Check 174).
//
// **Two seams have no implementation in this repository.** CodeHost (§4.4's
// repository check) and Proofs are declared by P1.1 and P1.2 and nothing writes
// them. Both belong to the open-source programme, and **D452 is open**: Proofs is
// nil here, so §4.4's check refuses every application before any repository is
// read (api_opensource.go), which is what a deployment with no proof check is
// supposed to do. The code host is a refusing one for the same reason — it is
// reached by nothing today, and a stand-in that answered `public: true` would be
// a licence check that passed on a repository nobody read.
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
//
// **It runs in the deployment's own environment, as this image's one job.**
// Seeding needs the whole of what the API needs — the same configuration, the
// same secrets, the same store — so no terminal can start it: an owner's
// credentials reach Firestore and Secret Manager but carry none of the twenty-odd
// FUGARO_ variables a deployment's environment holds, and the loader refuses an
// environment that is short one. The seed is therefore a Cloud Run job carrying
// exactly this revision's environment
// (ee/keyservice/terraform/services, `google_cloud_run_v2_job.seed`), and the
// deploy executes it with the address to invite
// (scripts/deploy-portal.sh's `--seed-operations`).

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

// seedTimeout bounds the seed's whole run: it reads and writes a handful of
// records and sends one mail, so a run still going after two minutes has failed
// in a way its own message should end. The job's task timeout
// (ee/keyservice/terraform/services) is longer, so this ends a stuck seed first
// and with a line in the log rather than a killed task.
const seedTimeout = 2 * time.Minute

func main() {
	if err := run(); err != nil {
		log.Fatalf("portal-api: %v", err)
	}
}

// run is the program as the platform starts it: the process's own argv and
// environment, and the deployment's own seams.
func run() error {
	return runWith(os.Args[1:], os.Environ(), nil, openSeams)
}

// runWith is run with its two ambient inputs named — the argv this process was
// given and the environment it runs in — and with the seams that reach a cloud
// passed in. Nothing else differs, which is what makes the seed's own case a
// drive of this program rather than of the pieces under it (main_test.go): what
// the deployment's job runs is this function with the deployment's environment.
func runWith(args, environ []string, secrets keyservice.Secrets, open seamsOpener) error {
	if len(args) > 0 && args[0] == "seed-operations" {
		return seedOperations(args[1:], environ, secrets, open)
	}
	if len(args) > 0 {
		return fmt.Errorf("this program serves, and its one subcommand is `seed-operations <address>`; %q is neither", args[0])
	}
	ctx := context.Background()
	svc, cleanup, err := build(ctx, environ, secrets, open)
	if err != nil {
		return err
	}
	defer cleanup()
	return serve(svc.Handler())
}

// deploymentSeams is the four seams a deployment's own clients stand behind: the
// licence records, the portal's documents, the handover's sealer and the mail
// path. They are opened together and passed to build because they are the only
// things in this program a case cannot build — Firestore, Cloud KMS and Resend
// all need a credential or a network — and separating them from the environment
// is what lets a case drive the whole path from one (main_test.go).
type deploymentSeams struct {
	Store keyservice.Store
	Docs  keyservice.Docs
	// Sealer is the handover's encryption, on the KMS client below.
	Sealer keyservice.Sealer
	// Mailer is nil in the deployment and the mail path is built from the
	// configuration then; a case that means to run a mail path stands one in.
	Mailer keyservice.Mailer
	// Close releases what was opened. It is never nil on a deployment's seams.
	Close func()
}

// seamsOpener opens them for one deployment: the project the store lives in and
// the configuration the sealer is built from.
type seamsOpener func(ctx context.Context, project string, cfg keyservice.Config) (deploymentSeams, error)

// openSeams is the deployment's own: the portal's Firestore database and one KMS
// client for the sealer. Both read the service's own credentials, and neither is
// told anything this program holds as a secret.
func openSeams(ctx context.Context, project string, cfg keyservice.Config) (deploymentSeams, error) {
	firestoreClient, err := firestore.NewClientWithDatabase(ctx, project, portalDatabase)
	if err != nil {
		return deploymentSeams{}, fmt.Errorf("the Firestore client: %w", err)
	}
	kmsClient, err := kms.NewKeyManagementClient(ctx)
	if err != nil {
		firestoreClient.Close()
		return deploymentSeams{}, fmt.Errorf("the Cloud KMS client: %w", err)
	}
	sealer, err := keyservice.NewKMSSealer(kmsClient, cfg.SealerKeyName)
	if err != nil {
		kmsClient.Close()
		firestoreClient.Close()
		return deploymentSeams{}, err
	}
	// One database, two faces of it: the licence records (firestore_store.go)
	// and the portal's own documents (docstore_docs.go). A deployment that gave
	// the two seams two stores would commit a binding's withdrawal onto records
	// no reader sees (api.go's Tx.Revoke).
	db := docstore.NewFirestore(firestoreClient)
	return deploymentSeams{
		Store:  keyservice.NewDocStore(db),
		Docs:   keyservice.NewDocStoreDocs(db),
		Sealer: sealer,
		Close: func() {
			kmsClient.Close()
			firestoreClient.Close()
		},
	}, nil
}

// build is the whole of the wiring, and it is one function so that the seed
// subcommand reaches a service built exactly the way the server's is: what the
// seed writes has to be visible to the API that serves it, and two assemblies
// could differ in a database name.
//
// A nil secrets is the deployment's own reader, which is what a revision has:
// the API's account reads the containers through Secret Manager. Every other
// value is the environment's, and the reader refuses one that is short.
func build(ctx context.Context, environ []string, secrets keyservice.Secrets, open seamsOpener) (*keyservice.Service, func(), error) {
	project, err := keyservice.DeployProject(environ)
	if err != nil {
		return nil, nil, err
	}
	if secrets == nil {
		secrets, err = keyservice.NewSecretManager(ctx, project)
		if err != nil {
			return nil, nil, err
		}
	}
	cfg, deploy, err := keyservice.LoadConfig(ctx, environ, secrets)
	if err != nil {
		return nil, nil, err
	}
	seams, err := open(ctx, project, cfg)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		if seams.Close != nil {
			seams.Close()
		}
	}
	mailer := seams.Mailer
	if mailer == nil {
		mailer, err = keyservice.NewResendMailer(cfg.ResendKey, deploy.MailFrom, nil)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
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
	minter := keyservice.RemoteMints{
		URL:    deploy.MinterURL,
		Client: &http.Client{Timeout: mintTimeout},
		Tokens: keyservice.MetadataTokens{},
	}
	svc, err := keyservice.New(cfg, keyservice.Deps{
		Store:     seams.Store,
		Docs:      seams.Docs,
		Mints:     minter,
		MintsV1:   minter,
		Mailer:    mailer,
		CodeHost:  refusingCodeHost{},
		Notifier:  ownerMail{mailer: mailer, to: deploy.NotifyAddress},
		SignIn:    keyservice.PortalSignIn{Verifier: signIn},
		Paddle:    paddle,
		Scheduler: jobSignIn{verifier: scheduler, account: deploy.SchedulerAccount},
		Sealer:    seams.Sealer,
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
func seedOperations(args, environ []string, secrets keyservice.Secrets, open seamsOpener) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return errors.New("seed-operations writes the operations organisation and its first invitation, and it needs the one address to invite: `seed-operations <address>`")
	}
	ctx, cancel := context.WithTimeout(context.Background(), seedTimeout)
	defer cancel()
	svc, cleanup, err := build(ctx, environ, secrets, open)
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
