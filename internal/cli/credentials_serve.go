package cli

import (
	"os"

	"github.com/spf13/cobra"
)

// `fugaro credentials serve` is the credentials service's own entry point
// (plan task C1.14): the process the service's image runs, and the only thing
// that runs in it. It is the counterpart of the history job's
// `fugaro budget history --sweep` (history.go) -- a hidden command whose job
// is to be a Cloud Run process rather than something an operator types -- with
// one difference that decides its shape: a Cloud Run *job* is started with a
// command and its arguments (modules/workflow/job.tf sets both, and
// internal/infra/tf's knownCommands holds the pairs), while the service is a
// Cloud Run *service*, whose container sets neither, so the image's own
// entrypoint is the service and the command below is what the Dockerfile's
// CMD names (images/credentials/Dockerfile). internal/infra/tf's cover case
// for google_cloud_run_v2_service is written against exactly that: it refuses
// a plan whose container sets a command or args, because the module sets
// neither and the image's entrypoint is what runs.
//
// It is hidden twice -- the group and the subcommand -- because it is not an
// operator's command: it takes no argument, no flag and no config file, and
// everything it needs is the environment the installation module sets on the
// service. `fugaro --help` is where that is checked
// (TestCredentialsServeHidden).
//
// The environment is credentials.tf's, and it is exactly three names:
// FUGARO_PROJECT, FUGARO_GCP_PROJECT and FUGARO_REGION. They are the history
// job's own three constants (history.go) read the same way, and they are held
// by a second thing: internal/infra/tf's credentialsServiceEnv is the
// allow-list init's cover applies to the service's container, so a fourth
// name is refused at plan time and an environment variable is not a path a
// credential reaches this process by. The bundle secret is not one of them:
// the service derives its name from FUGARO_PROJECT (C1.11's loader, the
// project-wide container of the logical name "creds-bundle") and reads it as
// its own service account, which is the one grant it holds (Q6, Global
// Constraint 12). Nothing here reads a credential and nothing here prints one:
// the command has no flag that could carry a value, no output of its own, and
// every refusal below names a variable or a missing piece and never a value.
//
// WHAT IS NOT HERE YET, AND WHY THIS COMMAND DOES NOT SERVE.
//
// The service itself -- its two routes, the forward, the cascade, the bundle
// loader -- is plan task C1.11's (internal/credsvc's server.go, forward.go,
// cascade.go and bundle.go). **C1.11 is not in this build**: this tree's
// internal/credsvc carries the run-identity verification and the wire contract
// (C1.10) and the exported forwarding helpers the service reuses (C1.8), and
// nothing that can answer a call -- there is no credsvc.NewServer to host and
// no bundle loader to build it with. So this command refuses to start, naming
// that, instead of binding a port. That is the fail-closed direction this
// whole work is held to (D143: an unreachable service halts a run, and no code
// path falls back to a key in the job): a service that started and answered
// anything at all is a service whose answers a run acts on, and a revision
// that fails to start is what tells an owner the image they deployed cannot
// serve, while a service that answered every call with a refusal would look
// healthy and halt every run that reached it.
//
// When C1.11 lands, serveCredentials becomes the wiring: credsvc.NewServer's
// Config built from this environment -- the verifier (project, Firebase
// project, the service's own audience, the job account table, both key sets
// and the write-once outcome reader), the bundle loader over Secret Manager as
// the service's own account, the policy, kill and state readers over the
// database, and the patcher the ended-run cleanup needs -- hosted by an
// http.Server with the timeouts a long-lived streamed call allows and shut
// down on the signal Cloud Run sends. Nothing else in this file changes: the
// command, the environment, the refusals and the hidden registration are
// C1.14's and stay as they are.
func newCredentialsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "credentials",
		Short:  "Run the credentials service (internal; the service image's entrypoint)",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	cmd.AddCommand(newCredentialsServeCmd())
	return cmd
}

// newCredentialsServeCmd is `fugaro credentials serve`: the service's process,
// and nothing else. It has no flags at all -- not even a --port: Cloud Run
// gives the container the port it must listen on, and a flag would be a second
// way to start a process whose whole configuration is the platform's.
func newCredentialsServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "serve",
		Short:  "Run the credentials service in its image (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serveCredentials(cmd, os.Getenv)
		},
	}
}

// serveCredentials starts the service.
//
// The order below is the order of what an operator has to be told: first the
// http2debug refusal, which is every command of this repository's that opens
// or handles a credential (refuseHTTP2Debug: GODEBUG=http2debug makes Go
// print HTTP requests, headers included, to stderr), then the environment the
// module sets, and only then the service itself. A service whose deployment is
// wrong must be told so by name rather than by a failure to start for a reason
// the operator cannot see.
//
// getenv is a parameter rather than os.Getenv so the refusals can be tested
// without a process environment (history.go's runHistorySweep takes it the
// same way).
func serveCredentials(cmd *cobra.Command, getenv func(string) string) error {
	if err := refuseHTTP2Debug(getenv); err != nil {
		return err
	}
	// The three names credentials.tf sets on the service, and the allow-list
	// init's cover holds its container to (credentialsServiceEnv). An empty
	// variable is not a value the module can set: Terraform writes each of
	// these from a required input, so an empty one is a deployment that is
	// wrong and not a deployment with a default.
	env := map[string]string{}
	for _, k := range []string{envProject, envGCPProject, envRegion} {
		if env[k] = getenv(k); env[k] == "" {
			return userErr("the credentials service needs %s in its environment (the installation module sets it on the service)", k)
		}
	}
	_ = cmd
	return userErr("this build carries no credentials service: internal/credsvc holds the wire contract and the forwarding helpers, and the service's own code (credentials-service plan, task C1.11) is not in it, so there is nothing to serve. Nothing was started and no credential was issued; deploy an image built from a fugaro that has C1.11")
}
