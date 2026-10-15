// Package batch implements the CLI's backend seam on Google Cloud Batch
// (design docs/design/compute-backend.md): one Batch job per run, on Compute
// Engine VMs, for the workflows that need a real Docker daemon.
//
// Nothing selects it yet. `backend: cloud-batch` is refused by config
// validation, doctor and init until the enable task, which by construction
// comes after logs, cost and the reaper exist (design §8.1): a Batch run
// without log routing leaves its output in the project's default logs, and a
// Batch run without a reaper leaks VMs. What exists here is the seam's
// second implementation, held to backendtest.Run, so that `jobs.cancel`
// rather than `jobs.delete` is a tested requirement rather than a comment
// (§1.4), and so that the shape of a Batch run's identity, states and job
// definition is pinned before anything can be enabled.
//
// What is deliberately NOT here, and which task owns it:
//
//   - Task 3: the compute cost of a VM (the machine type's quantities, the
//     one-minute billing floor, the boot window), which is why Started and
//     Completed are left zero rather than guessed.
//   - Task 4: the installation's log sink arm for batch.googleapis.com/Job,
//     and following a job's logs.
//   - Task 6: provisioning (the Batch API, the service accounts, IAM, the
//     boot image, the NAT/router).
//   - Task 7: the real job definition -- the socket mount, the env
//     allowlist, the resource quantities -- and the IAM that lets a launcher
//     create a job. This package builds the fields a fake-level test can
//     hold it to, from Options.
//   - Task 8: the config plumbing that fills Options in.
package batch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	batch "google.golang.org/api/batch/v1"
	"google.golang.org/api/googleapi"
	logging "google.golang.org/api/logging/v2"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/internal/backend"
)

// discardLogger is every client's logger. Without one, a Google client
// builds its own from GOOGLE_SDK_GO_LOGGING_LEVEL, which at debug logs each
// request and its body to stderr: for Secret Manager's addVersion, the
// secret itself. (gcp's is the same, unexported there.)
var discardLogger = slog.New(slog.DiscardHandler)

var _ backend.Backend = (*Backend)(nil)

// Endpoints override API roots, for fakes and emulators. Empty means the
// real Google endpoint.
type Endpoints struct {
	Batch, Logging string
	NoAuth         bool
}

// Timeouts is where a Batch backend reads a workflow's timeouts from.
//
// On Cloud Run they are read back from the deployed job; on Batch there is
// no deployed job to read (a job is created per run and is single-use), so
// they are a config/policy read (design §2.3). Task 8 wires the real one;
// the seam exists here because backendtest asks both questions.
type Timeouts interface {
	// TaskTimeout is the task timeout of (slug, workflow)'s workflow job,
	// backend.DefaultTaskTimeout when the workflow sets none. It returns an
	// error wrapping backend.ErrNotFound for a workflow that does not exist.
	TaskTimeout(slug, workflow string) (time.Duration, error)
	// LongestTaskTimeout is the longest task timeout among the Fugaro
	// workflow jobs, zero when there are none. No run of them can be active
	// for longer, so it bounds how far back an active-only List must look.
	LongestTaskTimeout() (time.Duration, error)
}

// Options configure a Backend.
type Options struct {
	GCPProject, Region string
	// FugaroProject is the Fugaro project's name (the job env's
	// FUGARO_PROJECT, not the GCP project ID). A run's RequireProject check
	// reads it, so a job that omits it fails closed on a VM.
	FugaroProject string
	// Image is the container image a run's job runs: the workflow's derived
	// image. New refuses an empty one -- a job with no runnable image would
	// be created and fail on the VM, after the launcher had reported it
	// started. Task 7/8 resolve it per (slug, workflow); here it is one
	// installation's.
	Image string
	// ServiceAccount is the email of the dedicated service account the job's
	// VMs run as (design §3.2). New refuses an empty one and the default
	// Compute Engine service account: that account is Editor, "not
	// recommended for production", and shared with everything else in the
	// project, so a run on it would hold far more than its own resources.
	ServiceAccount string
	// Secrets maps an environment variable name to a Secret Manager secret
	// name -- never a value. They are sent as Environment.secretVariables,
	// so the values never enter the job definition, which anyone with read
	// access to the job can read (design §1.6, C10).
	Secrets map[string]string
	// Timeouts is where the workflow timeouts come from. Required.
	Timeouts  Timeouts
	Endpoints Endpoints
	// HTTPClient, when set, is the client the API calls use (tests).
	HTTPClient *http.Client
	// LogView, when set, is the log view
	// (projects/<p>/locations/<l>/buckets/<b>/views/<v>) logs are read
	// through and log URLs open; empty reads the project's default logs.
	LogView string
	// Warn, when set, receives problems that don't fail a call: a job whose
	// state or create time the backend cannot read (it is kept, with the
	// state read as pending and a zero time, so one bad job does not fail a
	// whole listing).
	Warn func(msg string)
}

// Backend is the Cloud Batch backend.
type Backend struct {
	o      Options
	batch  *batch.Service
	logs   *logging.Service
	codec  backend.Codec
	now    func() time.Time
	region string
	// listPageSize is List's page size; tests shrink it to exercise paging.
	listPageSize int64
}

// New connects to Cloud Batch and Cloud Logging with Application Default
// Credentials (gcloud auth application-default login) unless NoAuth.
func New(ctx context.Context, o Options) (*Backend, error) {
	switch {
	case o.GCPProject == "" || o.Region == "":
		return nil, errors.New("the Batch backend needs a GCP project and a region")
	case o.FugaroProject == "":
		return nil, errors.New("the Batch backend needs the Fugaro project's name (the job env's FUGARO_PROJECT)")
	case o.Image == "":
		return nil, errors.New("the Batch backend needs the image a run's job runs")
	case o.Timeouts == nil:
		return nil, errors.New("the Batch backend needs a source for the workflow timeouts")
	}
	if err := checkServiceAccount(o.ServiceAccount); err != nil {
		return nil, err
	}
	if o.Warn == nil {
		o.Warn = func(string) {}
	}
	opts := func(endpoint string) []option.ClientOption {
		opts := []option.ClientOption{option.WithLogger(discardLogger)}
		if endpoint != "" {
			opts = append(opts, option.WithEndpoint(endpoint))
		}
		if o.Endpoints.NoAuth {
			opts = append(opts, option.WithoutAuthentication())
		}
		if o.HTTPClient != nil {
			opts = append(opts, option.WithHTTPClient(o.HTTPClient))
		}
		if !o.Endpoints.NoAuth {
			opts = append(opts, option.WithQuotaProject(o.GCPProject))
		}
		return opts
	}
	bs, err := batch.NewService(ctx, opts(o.Endpoints.Batch)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Batch: %w", err)
	}
	ls, err := logging.NewService(ctx, opts(o.Endpoints.Logging)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Logging: %w", err)
	}
	return &Backend{o: o, batch: bs, logs: ls, codec: backend.BatchCodec(), now: time.Now, region: o.Region, listPageSize: 100}, nil
}

// defaultComputeServiceAccountRE is the default Compute Engine service
// account's email: <project number>-compute@developer.gserviceaccount.com.
var defaultComputeServiceAccountRE = regexp.MustCompile(`^[0-9]+-compute@developer\.gserviceaccount\.com$`)

// checkServiceAccount refuses a service account a job's VMs must not run as
// (design §3.2, C6/C21): empty means "let Batch choose", which is the
// default Compute Engine account, and that account is Editor on the whole
// project.
func checkServiceAccount(email string) error {
	switch {
	case email == "":
		return errors.New("the Batch backend needs the dedicated service account's email: left unset, Batch runs the job's VMs as the default Compute Engine service account, which is Editor on the project")
	case defaultComputeServiceAccountRE.MatchString(email):
		return fmt.Errorf("the Batch backend must not run a job's VMs as the default Compute Engine service account (%s): use the repository's dedicated account", email)
	}
	return nil
}

// Codec is Cloud Batch's name codec (design §2.5): the six-field job name,
// and the signal (BATCH_TASK_INDEX/BATCH_TASK_COUNT) that says a process is
// one of this backend's runs.
func (b *Backend) Codec() backend.Codec { return b.codec }

// location is projects/<p>/locations/<r>, the parent of the jobs.
func (b *Backend) location() string {
	return "projects/" + b.o.GCPProject + "/locations/" + b.o.Region
}

// canonical rebuilds a job name with the backend's project ID, so a project
// number the API answers with never reaches a caller.
//
// Names reach it from bucket objects a run's own service account can write
// (launch.json, result.json), so it follows only a name in the backend's
// region whose every part is [a-z0-9-]. Callers that know the run bind the
// job too, through the codec's ValidateRun.
func (b *Backend) canonical(name string) (backend.ExecID, error) {
	id, err := b.codec.ParseInRegion(name, b.o.Region)
	if err != nil {
		return backend.ExecID{}, err
	}
	id.GCPProject = b.o.GCPProject
	return id, nil
}

// apiError wraps a Batch error, mapping 404 to ErrNotFound.
func apiError(what string, err error) error {
	var ge *googleapi.Error
	if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
		return fmt.Errorf("%s: %w: %w", what, backend.ErrNotFound, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// statusClientClosed is 499, gRPC CANCELLED over HTTP: the request was
// abandoned, not refused, so it is ambiguous.
const statusClientClosed = 499

// launchError classifies a jobs.create failure. Only a definitive refusal (a
// 4xx other than 408, 429 and 499) wraps ErrRejected: nothing can have been
// created. Anything else, timeouts and transport errors included, is
// ambiguous -- the job may exist and be running.
func launchError(job string, err error) error {
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		switch {
		case ge.Code == http.StatusConflict:
			// Batch job ids are per run and a job is single-use, so this is
			// a run id launched twice.
			return fmt.Errorf("launching: job %s already exists (the same run was launched twice): %w: %w", job, backend.ErrRejected, err)
		case ge.Code >= 400 && ge.Code < 500 && ge.Code != http.StatusRequestTimeout && ge.Code != http.StatusTooManyRequests &&
			ge.Code != statusClientClosed:
			return fmt.Errorf("launching %s: %w: %w", job, backend.ErrRejected, err)
		}
	}
	return fmt.Errorf("launching %s (the job may have started): %w", job, err)
}

// stateOf maps a Batch JobStatus.State to the seam's State, and reports
// whether this build knows the value.
//
// The table is the complete JobStatus.State enum (design §2.3, §9):
// STATE_UNSPECIFIED/QUEUED/SCHEDULED are pending, RUNNING is running,
// SUCCEEDED and FAILED are terminal, CANCELLATION_IN_PROGRESS and CANCELLED
// are cancelled, and DELETION_IN_PROGRESS is cancelled too -- but that one
// should only ever be seen for a job the reaper is deleting (§3.4), because
// a user's cancel is jobs.cancel and ends CANCELLED (§1.4).
//
// Which of those states a cancelled or a preempted job really passes
// through, and how long it sits in each, is UNVERIFIED: Check 57a steps 7
// and 8 are what settle it (design §9). The names and their meaning are
// documented; the transitions are not.
//
// An unrecognised value is not terminal and is logged by the caller: a value
// this build does not know must never read as "finished", which would make
// the sweeper ignore a run whose VM is still up and a watch exit while the
// job runs. That is gcp's stateOf shape too: read defensively rather than
// trust one field.
func stateOf(state string) (backend.State, bool) {
	switch state {
	case "STATE_UNSPECIFIED", "QUEUED", "SCHEDULED":
		return backend.StatePending, true
	case "RUNNING":
		return backend.StateRunning, true
	case "SUCCEEDED":
		return backend.StateSucceeded, true
	case "FAILED":
		return backend.StateFailed, true
	case "CANCELLATION_IN_PROGRESS", "CANCELLED", "DELETION_IN_PROGRESS":
		return backend.StateCancelled, true
	}
	return backend.StatePending, false
}

// parseTime parses an API timestamp; empty is the zero time.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp %q: %w", s, err)
	}
	return t, nil
}
