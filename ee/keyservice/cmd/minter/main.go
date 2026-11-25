// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

// The key minter's program (portal plan P1.7, design
// docs/key-signing-service/spec.md §3.2, §3.3).
//
// It is the second of the deployment's two Cloud Run services and it is the one
// that can sign: it builds the KMS signer, hands it to Minter, and serves
// `/mint` and `/mint/v1` on the port the platform gives it. Nothing else is
// here, and that is the shape of the identity boundary rather than a small
// program — there is no store, no mail path, no merchant secret and no admin
// secret in this process, because the only thing this service is for is turning
// a claim set the intake computed into a signature (§3.3, Check 174).
//
// **What it does not have is enforced, not promised.** The configuration it
// reads is LoadMinterConfig's three variables, and that loader refuses every
// other FUGARO_ variable: a deployment that reused the API's environment here
// would not start, so the signer cannot be handed a customer's secrets by a
// mistake in a deploy script. The boundary case in config_test.go reads this
// file as text for the same reason — the other half of the boundary is that this
// program never names the fields that hold them, and this file has to stay
// readable by that case.
//
// **The public invoker is the platform's business.** This service is deployed
// with no `allUsers` invoker and with the API's account as the only principal
// allowed to call it (P1.5's IAM, P9.2's deploy script); the ID token the intake
// presents is what says a call came from its own other half (§3.2). Nothing in
// this file can hold that, and it says so here rather than pretending the code
// is the fence.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	kms "cloud.google.com/go/kms/apiv1"

	"github.com/dimipaun/fugaro/ee/keyservice"
)

// port is the platform's own: Cloud Run sets PORT and routes to it, and a
// container that listened elsewhere would be one nobody can reach.
const defaultPort = "8080"

// shutdownGrace is how long a request that is in flight has to finish once the
// platform asks the container to stop. A mint is a signing call and a JSON
// answer, so the grace is short and it is here so a deploy does not cut a
// signature off after the key ring made it.
const shutdownGrace = 20 * time.Second

func main() {
	if err := run(); err != nil {
		// The message goes to the platform's log, and it names what failed
		// rather than a stack: this program is only ever started by a deploy,
		// and the operator reading the log is the one who wrote the variables.
		log.Fatalf("minter: %v", err)
	}
}

func run() error {
	cfg, err := keyservice.LoadMinterConfig(os.Environ())
	if err != nil {
		return err
	}
	// The client is reached with the container's own service account, which is
	// the identity P1.5 grants signer on the one key version this is configured
	// with: no key file, no credential in the environment.
	ctx := context.Background()
	client, err := kms.NewKeyManagementClient(ctx)
	if err != nil {
		return fmt.Errorf("the Cloud KMS client: %w", err)
	}
	defer client.Close()
	signer, err := keyservice.NewKMSSigner(client, cfg.SignerKeyVersion)
	if err != nil {
		return err
	}
	minter := keyservice.Minter{
		Signer:      signer,
		Keys:        cfg.Keys,
		Environment: cfg.Environment,
		Now:         time.Now,
	}
	return serve(minter.Handler())
}

// serve runs the handler on the platform's port until the platform asks the
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
