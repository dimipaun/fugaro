// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/dimipaun/fugaro/ee/coordinator/mcpauth"
	"github.com/dimipaun/fugaro/internal/blobx"
	fcli "github.com/dimipaun/fugaro/internal/cli"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// newCoordinatorServeCmd is `fugaro coordinator serve`: the resident service's
// process, and the only thing that runs on the front door's Cloud Run revision
// (mcp-server plan task M4; the module names this pair as the container's
// command and args, coordinator.tf).
//
// It is hidden, like the sweep and like the credentials service's own serve,
// because it is not a command of the coordinator's surface: it takes no
// argument, no flag and no config file, and everything it can be given is the
// environment the installation module sets on the service. `fugaro coordinator
// --help` is where that is checked (TestCoordinatorServeIsHidden), and the
// generated CLI reference skips a hidden command, so the coordinator's
// documented surface does not change.
//
// The image is the history image's (internal/infra's MCPServer names it), whose
// default CMD is `version` and whose entrypoint is the fugaro binary: the
// module's explicit command and args are what make the revision the front door
// rather than a container that prints a version and exits.
//
// What the process refuses, and in which order, is the credentials service's
// order and for the same reasons: GODEBUG=http2debug makes Go print the
// requests it handles, headers included, and the front door's headers carry a
// caller's bearer token, so that refusal comes first, before anything is read;
// then the environment, because a revision that mounted a model credential is
// the one thing spec.md §9 forbids and it must fail to start rather than serve
// (coordinator.RefuseModelCredential); and only then a listener. os.Interrupt
// is beside SIGTERM because the same binary is what a developer runs by hand,
// and the context below is what drains the requests in flight before the
// process returns. That context is derived from the command's own, so it is
// done on either: a signal, or a cancellation from whatever ran this command
// (cobra's ExecuteContext), which reaches the same drain by the same path and
// needs no second switch of its own.
func newCoordinatorServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "serve",
		Short:  "Run the coordinator's front door in its image (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if err := fcli.RefuseHTTP2Debug(os.Getenv); err != nil {
				return err
			}
			if err := coordinator.RefuseModelCredential(os.Environ); err != nil {
				return err
			}
			svc, err := coordinatorFrontDoor(ctx, os.LookupEnv, newFrontDoorWiring())
			if err != nil {
				return err
			}
			return coordinator.Serve(ctx, svc, os.Getenv, cmd.ErrOrStderr())
		},
	}
}

// frontDoorWiring is the four things this command opens that a test cannot:
// the store the token records live in, the runs bucket M8's read tools read,
// the database O16b's state group reads the orchestrator's node in, and the
// verifier the sign-in step consumes a Firebase ID token with. Every one of
// them is a cloud client, so each is a function here and the real
// implementation is the only one production uses (the sweep's own
// coordinatorSweepWiring is the same seam in the same package).
type frontDoorWiring struct {
	store    func(ctx context.Context, project string) (mcpauth.Store, error)
	bucket   func(ctx context.Context, name string) (*blobx.Bucket, error)
	state    func(ctx context.Context, url string) (mcpauth.StateReach, error)
	verifier func(firebaseProject string) (*mcpauth.Verifier, error)
}

// newFrontDoorWiring is the real one. The store's URL is built from the
// project and never taken from the environment, so the one name this process
// may open is the store's own bucket (mcpauth.OpenStore refuses anything else
// by name): a front door handed a URL could be pointed at another
// installation's records, which is the whole of what D935's bucket name is
// for.
func newFrontDoorWiring() frontDoorWiring {
	return frontDoorWiring{
		store: func(ctx context.Context, project string) (mcpauth.Store, error) {
			return mcpauth.OpenStore(ctx, project, "gs://"+mcpauth.StoreBucketName(project))
		},
		bucket: func(ctx context.Context, name string) (*blobx.Bucket, error) {
			return blobx.Open(ctx, "gs://"+name)
		},
		state: func(ctx context.Context, url string) (mcpauth.StateReach, error) {
			return openFrontDoorState(ctx, url)
		},
		verifier: func(firebaseProject string) (*mcpauth.Verifier, error) {
			return mcpauth.NewVerifier(firebaseProject, mcpauth.NewSecureTokenKeys())
		},
	}
}

// openFrontDoorState opens the installation's database as the front door's own
// account and builds the state group's reach over it (O16b, D2020 (a)).
//
// **The account the process runs as is the only identity in it.** The client's
// token comes from the metadata server, exactly as the sweep's does, and the
// custom role that account holds — `firebasedatabase.instances.get` on the
// installation's database and nothing else — is what makes the reads land. No
// credential of its own is opened, read or held here: the reach is a client over
// the database and its identity is the revision's (spec.md §8, "there is no
// credential to reach").
//
// **Nothing checks that the database is this project's own, and that is the
// difference between this open and the sweep's.** The sweep holds
// `firebasedatabase.admin` on the Firebase project and so can WRITE any database
// it is pointed at, which is why it reads the project's marker before it writes
// anything; this account holds a read on one database, granted by this
// installation's own Terraform, so a URL naming anything else is a read IAM
// refuses at the first call. The entry is checked to be a database address
// (coordinatorFrontDoor), and what may be read through it is the account's.
func openFrontDoorState(ctx context.Context, url string) (mcpauth.StateReach, error) {
	ts, err := google.DefaultTokenSource(ctx, fcli.BudgetScopes()...)
	if err != nil {
		return nil, fcli.RemoteErr(fmt.Errorf("no credentials for the coordinator's database: %w", err))
	}
	db, err := rtdb.New(url, rtdb.Auth{Source: ts})
	if err != nil {
		return nil, fcli.UserErr("%s: %v", fcli.EnvRTDBURL, err)
	}
	reach, err := mcpauth.NewProjectState(db)
	if err != nil {
		return nil, fcli.UserErr("%v", err)
	}
	return reach, nil
}

// coordinatorFrontDoor is the front door this revision runs, out of the
// environment the installation module set on it. It is a function of its own
// rather than the body of the command, because it is where the switch, the two
// lists and the tools are read and it is the only part of this command a test
// can drive without a cloud.
//
// **The switch is the module's and this reads it rather than keeping one of its
// own** (D934, and service.go's own paragraph on the second switch): the entry
// mcpauth.FrontDoorEnv is written onto the revision under var.mcp_server and
// nowhere else, and its presence is "this installation has a front door". A
// revision without it mounts nothing, which is exactly what this process served
// before M12 — a 404 on every path of the door — and the door's metadata
// document is one of them.
//
// With it on, everything the door serves with comes from the environment the
// installation already renders on this service: the store's bucket from
// FUGARO_PROJECT (D935, whose only member is this account), the read tools'
// reach from FUGARO_RUNS_BUCKET and FUGARO_COORDINATOR_REPOS (M8, the same
// repository list the sweep reconciles, and a token's own filter is applied on
// top of it by the tool itself), the sign-in step's verifier from
// FUGARO_FIREBASE_PROJECT (M6), and the two member lists and the issuer from
// the door's own entry (M12, roster.go).
//
// Two things a server can be configured with are deliberately left at their
// zero values, and they are the project's own rather than this command's to
// invent: Config.RedirectAllowlist (D928's line in the project's own
// configuration, which admits a hosted client's callback) and Config.Limits
// (D927's caps, which only ever narrow what a client asks for). The module
// declares no source for either — var.mcp_server is the account, the service
// name and the image — so a door wired here admits the native clients D928
// always allows (loopback) and narrows nothing, and whoever gives an
// installation those two lines renders them onto this same entry rather than
// putting the project's configuration in a second place. Nothing about the
// tokens' own bounds moves with it: a family's limits are what the mint
// resolved, and the run's spend ceiling is the run's own.
func coordinatorFrontDoor(ctx context.Context, lookup func(string) (string, bool), w frontDoorWiring) (*coordinator.Service, error) {
	cfg, on, err := mcpauth.DoorConfigFromEnv(lookup)
	if err != nil {
		return nil, fcli.UserErr("%v", err)
	}
	if !on {
		return coordinator.FrontDoor(coordinator.FrontDoorSpec{})
	}
	getenv := func(name string) string {
		value, _ := lookup(name)
		return value
	}
	env := map[string]string{}
	for _, k := range []string{fcli.EnvProject, fcli.EnvFirebaseFP, fcli.EnvRTDBURL, fcli.EnvRunsBucket, envCoordinatorRepos} {
		if env[k] = getenv(k); env[k] == "" {
			return nil, fcli.UserErr("the coordinator's front door is on and needs %s in its environment (Terraform sets it on the service)", k)
		}
	}
	if strings.ContainsAny(env[fcli.EnvRunsBucket], "/: ") {
		return nil, fcli.UserErr("%s must be a bucket name", fcli.EnvRunsBucket)
	}
	// The database the state group reads is named by the module and never taken
	// from anywhere else. It is checked to be a database's address — https, the
	// database's own host and nothing else — before the wiring opens it, so an
	// entry a hand-edit turned into a path or a URL carrying credentials fails
	// the revision's start rather than a client's read. What may be READ there
	// is the account's IAM and not this check's (openFrontDoorState).
	if err := rtdb.ValidateURL(env[fcli.EnvRTDBURL], false); err != nil {
		return nil, fcli.UserErr("%s: %v", fcli.EnvRTDBURL, err)
	}
	repos, err := coordinatorSweepRepos(env[envCoordinatorRepos])
	if err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(repos))
	for _, r := range repos {
		slugs = append(slugs, r.Slug)
	}

	store, err := w.store(ctx, env[fcli.EnvProject])
	if err != nil {
		return nil, fcli.RemoteErr(fmt.Errorf("opening the store the token records live in: %w", err))
	}
	bucket, err := w.bucket(ctx, env[fcli.EnvRunsBucket])
	if err != nil {
		return nil, fcli.RemoteErr(fmt.Errorf("opening the installation's runs bucket: %w", err))
	}
	reach, err := mcpauth.NewProjectRuns(bucket.Bucket, slugs)
	if err != nil {
		return nil, fcli.UserErr("%v", err)
	}
	registry := mcpauth.NewRegistry()
	if err := mcpauth.RegisterReadTools(registry, reach); err != nil {
		return nil, fcli.UserErr("%v", err)
	}
	// O16b's state group, registered beside M8's read tools and over its own
	// reach: get_state and get_events read one orchestrator's node through the
	// account's database read (RegisterStateTools). apply_patch is not
	// registered by anything (D2021) — the state group's writes stay with
	// `fugaro orchestrator patch` and the person's own credential (D1223).
	stateReach, err := w.state(ctx, env[fcli.EnvRTDBURL])
	if err != nil {
		return nil, fcli.RemoteErr(fmt.Errorf("opening the installation's database for the state group: %w", err))
	}
	if err := mcpauth.RegisterStateTools(registry, stateReach); err != nil {
		return nil, fcli.UserErr("%v", err)
	}
	verifier, err := w.verifier(env[fcli.EnvFirebaseFP])
	if err != nil {
		return nil, fcli.UserErr("%v", err)
	}
	srv, err := mcpauth.NewServer(mcpauth.Config{Issuer: cfg.Issuer}, store, verifier,
		mcpauth.WithRoster(cfg.Roster()), mcpauth.WithTools(registry))
	if err != nil {
		return nil, fcli.UserErr("%v", err)
	}
	return coordinator.FrontDoor(coordinator.FrontDoorSpec{
		Enabled:   true,
		Delivered: mcpauth.DeviceDeliveryBuilt,
		Door:      srv,
	})
}
