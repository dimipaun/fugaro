package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob" // file:// buckets for local runs

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
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
	f.StringVar(&o.workDir, "workdir", "/work/repo", "repository checkout")
	f.StringVar(&o.remote, "remote", "", "clone this remote when --workdir has no checkout")
	f.StringVar(&o.stateDir, "state-dir", "/work/state", "run state directory, outside the checkout")
	f.StringVar(&o.provider, "provider", "", `git provider; only "fake" until real providers land`)
	f.StringVar(&o.providerState, "provider-state", "", "state file for --provider fake")
	f.StringVar(&o.claudeBin, "claude", "claude", "claude binary")
	f.DurationVar(&o.cancelPoll, "cancel-poll", 30*time.Second, "how often to check for a cancel request")
	return cmd
}

func runExec(cmd *cobra.Command, o execOptions) error {
	ctx := cmd.Context()
	if o.bucket == "" {
		return errors.New("--bucket (or FUGARO_BUCKET) is required")
	}
	if o.taskFile != "" && o.run != "" {
		return errors.New("--task-file and --run (or FUGARO_RUN) are mutually exclusive; pass exactly one")
	}
	provider, err := openProvider(o)
	if err != nil {
		return err
	}
	bucket, err := blob.OpenBucket(ctx, o.bucket)
	if err != nil {
		return fmt.Errorf("opening bucket %s: %w", o.bucket, err)
	}
	defer bucket.Close()

	var slug, runID string
	if o.taskFile != "" {
		spec, err := readTaskFile(o.taskFile, cmd.InOrStdin())
		if err != nil {
			return err
		}
		slug, runID = task.Slug(spec.Repo), spec.RunID
		if err := runstore.Open(bucket, slug, runID).WriteTask(ctx, spec); err != nil {
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
	log := runner.NewLogger(cmd.ErrOrStderr(), "run_id", runID, "repo", slug)
	rec, runErr := runner.Run(ctx, runner.Deps{
		Store: runstore.Open(bucket, slug, runID), Provider: provider, Agent: agent.Claude{Bin: o.claudeBin},
		WorkDir: workDir, Remote: o.remote, StateDir: stateDir, Env: os.Environ(),
		PathPrepend: filepath.Dir(exe), Log: log, CancelPoll: o.cancelPoll,
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

func openProvider(o execOptions) (gitprov.Provider, error) {
	switch o.provider {
	case "fake":
		return &fake.Provider{Path: o.providerState}, nil
	default:
		return nil, errors.New("real git providers are not implemented yet; use --provider fake")
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
