package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/gitprov/providers"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

type execOptions struct {
	bucket, run, taskFile     string
	workDir, remote, stateDir string
	provider, providerState   string
	claudeBin                 string
	cancelPoll                time.Duration
}

func newExecCmd() *cobra.Command {
	var o execOptions
	cmd := &cobra.Command{
		Use:    "exec",
		Short:  "Execute a run inside a Fugaro worker (the container entrypoint)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   func(cmd *cobra.Command, _ []string) error { return runExec(cmd, o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.bucket, "bucket", os.Getenv("FUGARO_BUCKET"), "runs bucket URL (gs://… or file://…)")
	f.StringVar(&o.run, "run", os.Getenv("FUGARO_RUN"), "run to execute, as <repo-slug>/<run-id>")
	f.StringVar(&o.taskFile, "task-file", "", "store this task spec (path, or - for stdin) and run it; a run ID is generated if missing")
	f.StringVar(&o.workDir, "workdir", "/work/repo", "repository checkout; it is force-reset to the task's ref and untracked files are removed (ignored ones are kept)")
	f.StringVar(&o.remote, "remote", "", "clone this remote when --workdir has no checkout")
	f.StringVar(&o.stateDir, "state-dir", "/work/state", "run state directory, outside the checkout; created if absent, and only Fugaro's own entries in it (verify/, workflow.json, pr.md) are replaced")
	f.StringVar(&o.provider, "provider", os.Getenv("FUGARO_GIT_PROVIDER"), `git provider: github or bitbucket to open it before fugaro.yaml is read (it must match git.provider), or "fake" for the file-backed test provider; empty lets the origin URL's host, then git.provider, decide`)
	f.StringVar(&o.providerState, "provider-state", "", "state file for --provider fake")
	f.StringVar(&o.claudeBin, "claude", "claude", "claude binary")
	f.DurationVar(&o.cancelPoll, "cancel-poll", 30*time.Second, "how often to check for a cancel request")
	return cmd
}

func runExec(cmd *cobra.Command, o execOptions) error {
	ctx := cmd.Context()
	// The runner talks to Google too (the bucket, the metadata server), so
	// it refuses http2debug as every cloud command does: Go would log its
	// bearer tokens.
	if err := refuseHTTP2Debug(os.Getenv); err != nil {
		return err
	}
	if o.bucket == "" {
		return errors.New("--bucket (or FUGARO_BUCKET) is required")
	}
	if o.taskFile != "" && o.run != "" {
		return errors.New("--task-file and --run (or FUGARO_RUN) are mutually exclusive; pass exactly one")
	}
	env := os.Environ()
	// The run's logger needs the run ID, known only once the task is read,
	// but the provider options are checked before anything is written.
	// Adapter warnings (such as Bitbucket's unsupported labels) come
	// later, once the run is under way, so they go to the logger then.
	var log *slog.Logger
	warn := func(msg string) {
		if log != nil {
			log.Warn(msg)
		}
	}
	providerKind, openProvider, err := providerOptions(o, env, warn)
	if err != nil {
		return err
	}
	// On Cloud Run the job's own names come first: a job set up before M9a
	// fails here, cleanly, before anything compares its old FUGARO_PROJECT
	// (a GCP ID) with a project name.
	if err := refuseOldJobEnv(ctx, cmd, o, os.Getenv, time.Now); err != nil {
		return err
	}
	// On Cloud Run, the canonical execution name; "" for a local run.
	execName, err := backend.ExecutionFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	if execName != "" && o.taskFile != "" {
		// --task-file writes task.json before the runner can tell a
		// duplicate execution apart; Cloud Run launches name the run.
		return errors.New("--task-file is for local runs; on Cloud Run the run comes from FUGARO_RUN (or --run)")
	}
	bucket, err := blobx.Open(ctx, o.bucket)
	if err != nil {
		return err
	}
	defer bucket.Close()

	var slug, runID string
	if o.taskFile != "" {
		spec, err := readTaskFile(o.taskFile, cmd.InOrStdin())
		if err != nil {
			return err
		}
		// The provider kind is part of the slug. It comes only from
		// --provider (or FUGARO_GIT_PROVIDER), never from the origin host:
		// on Cloud Run the slug arrives whole in FUGARO_RUN instead.
		if o.provider == "" {
			return errors.New("--task-file needs --provider (or FUGARO_GIT_PROVIDER): the provider kind is part of the repository's storage slug")
		}
		if slug, err = task.Slug(o.provider, spec.Repo); err != nil {
			return fmt.Errorf("task file: %w", err)
		}
		runID = spec.RunID
		if err := runstore.Open(bucket.Bucket, slug, runID).WriteTask(ctx, spec); err != nil {
			return fmt.Errorf("writing task file: %w", err)
		}
	} else if slug, runID, err = runstore.ParseRef(o.run); err != nil {
		return err
	}

	workDir, err := filepath.Abs(o.workDir)
	if err != nil {
		return fmt.Errorf("resolving --workdir %q: %w", o.workDir, err)
	}
	stateDir, err := filepath.Abs(o.stateDir)
	if err != nil {
		return fmt.Errorf("resolving --state-dir %q: %w", o.stateDir, err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding the fugaro executable: %w", err)
	}
	log = runner.NewLogger(cmd.ErrOrStderr(), "run_id", runID, "repo", slug)
	prices := execPrices(os.Getenv, warn)
	rec, runErr := runner.Run(ctx, runner.Deps{
		Store: runstore.Open(bucket.Bucket, slug, runID), OpenProvider: openProvider, ProviderKind: providerKind, Agent: agent.Claude{Bin: o.claudeBin},
		WorkDir: workDir, Remote: o.remote, StateDir: stateDir, Env: env,
		PathPrepend: filepath.Dir(exe), Log: log, CancelPoll: o.cancelPoll,
		Bucket: bucket, Execution: execName, BaseImage: os.Getenv("FUGARO_BASE_IMAGE"),
		Prices: prices,
		// A job belongs to one Fugaro project; on Cloud Run a job that
		// doesn't say which is refused at bootstrap.
		Project: os.Getenv("FUGARO_PROJECT"), RequireProject: backend.OnCloudRun(os.Getenv),
	})
	var writeErr error
	if rec != nil {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		writeErr = enc.Encode(rec)
	}
	if runErr != nil {
		return &ExitError{Code: ExitRemoteError, Err: runErr}
	}
	if writeErr != nil {
		return fmt.Errorf("writing run record: %w", writeErr)
	}
	return nil
}

// oldJobEnvReason says what to do when a Cloud Run job lacks one of the
// names every job carries since M9a.
func oldJobEnvReason(missing string) string {
	if missing == "FUGARO_GCP_PROJECT" {
		return "job environment lacks FUGARO_GCP_PROJECT (it was set up before M9a): run fugaro init --repo from the repository's checkout; see docs/design/m9-budget-and-dashboard.md §13.1"
	}
	return "job environment lacks " + missing + ": run fugaro init --repo from the repository's checkout; see docs/design/m9-budget-and-dashboard.md §13.1"
}

// refuseOldJobEnv is nil unless this is a Cloud Run execution whose job
// lacks FUGARO_GCP_PROJECT or FUGARO_PROJECT. Then it records an
// infra_error for the run (create-if-absent, like the runner's first
// record, so it never replaces another execution's) and returns the exit-2
// error. The record carries no execution name: without the GCP project one
// can't be formed.
func refuseOldJobEnv(ctx context.Context, cmd *cobra.Command, o execOptions, getenv func(string) string, now func() time.Time) error {
	if !backend.OnCloudRun(getenv) {
		return nil
	}
	missing := ""
	for _, k := range []string{"FUGARO_GCP_PROJECT", "FUGARO_PROJECT"} {
		if getenv(k) == "" {
			missing = k
			break
		}
	}
	if missing == "" {
		return nil
	}
	reason := oldJobEnvReason(missing)
	fail := &ExitError{Code: ExitRemoteError, Err: errors.New(reason)}
	if o.taskFile != "" {
		return fail // the later check refuses --task-file on Cloud Run
	}
	slug, runID, err := runstore.ParseRef(o.run)
	if err != nil {
		return fail
	}
	bucket, err := blobx.Open(ctx, o.bucket)
	if err != nil {
		return fail
	}
	defer bucket.Close()
	store := runstore.Open(bucket.Bucket, slug, runID)
	rec := &runstore.Record{Version: 1, RunID: runID, Status: runstore.StatusInfraError, Stage: "bootstrap",
		Outcome: runstore.OutcomeNone, Reason: reason, StartedAt: now().UTC()}
	if spec, err := store.ReadTask(ctx); err == nil {
		rec.Repo, rec.Workflow = spec.Repo, spec.Workflow
	}
	finished := rec.StartedAt
	rec.FinishedAt = &finished
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := store.CreateRecord(wctx, rec); err != nil && !errors.Is(err, runstore.ErrExists) {
		fmt.Fprintf(cmd.ErrOrStderr(), "fugaro exec: could not record the failure: %v\n", err)
	}
	return fail
}

// fakeSelfID is the account ID `exec --provider fake` posts as.
const fakeSelfID = "fugaro-bot"

// providerOptions turns --provider into the runner's provider settings: the
// kind to open before fugaro.yaml is read ("" lets the origin URL's host or
// git.provider decide), and how to open it. warn receives the adapters'
// warnings.
func providerOptions(o execOptions, env []string, warn func(string)) (string, gitprov.Opener, error) {
	switch o.provider {
	case "fake":
		// The fake learns the task's repository from the runner, and reads
		// branch heads from --remote, as a real host would show them. It
		// posts as fakeSelfID, which is also its PRs' author, so Fugaro's
		// own comments and PRs look as they would on a real host.
		return "", func(_ context.Context, _, repo string) (gitprov.Provider, []string, error) {
			return &fake.Provider{Path: o.providerState, Repo: repo, Remote: o.remote, SelfID: fakeSelfID}, nil, nil
		}, nil
	case "", gitprov.KindGitHub, gitprov.KindBitbucket:
		if o.providerState != "" {
			return "", nil, errors.New("--provider-state only applies to --provider fake")
		}
		return o.provider, providers.FromEnv(env, nil, warn), nil
	default:
		return "", nil, fmt.Errorf("--provider must be github, bitbucket or fake, not %q", o.provider)
	}
}

func readTaskFile(path string, stdin io.Reader) (*task.Spec, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var s task.Spec
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("task file: %w", err)
	}
	if s.Version == 0 {
		s.Version = 1
	}
	if s.RunID == "" {
		if s.RunID, err = task.NewRunID(time.Now(), rand.Reader); err != nil {
			return nil, err
		}
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("task file: %w", err)
	}
	return &s, nil
}

// execPrices are the prices the runner estimates compute with: on Cloud
// Run, the job's FUGARO_COMPUTE_PRICES override, else (or when it is
// malformed, with a warning) the list price of FUGARO_REGION; nil for a
// local run, whose compute isn't estimated.
func execPrices(getenv func(string) string, warn func(string)) *backend.Prices {
	if getenv("FUGARO_BACKEND") != backend.CloudRun {
		return nil
	}
	p, ok, err := runner.PricesFromEnv(getenv)
	if err != nil {
		warn(err.Error() + "; using the list price")
	}
	if !ok {
		p = gcp.ListPrices(getenv("FUGARO_REGION"))
	}
	return &p
}
