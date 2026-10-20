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
//   - Task 3 (built): the compute cost of a VM -- the machine type's
//     quantities, the one-minute billing floor and the boot window -- whose
//     window this package reports a machine type for. Started and Completed
//     are still left zero, because the job's status events are where they
//     come from and their shape is UNVERIFIED (Check 57a steps 7 and 8): a
//     finished job therefore has no window to price and reads "compute not
//     estimated" rather than a figure that grows with the clock
//     (backend.Execution.BilledWindow).
//   - Task 4 (built): the installation's log sink arm for
//     batch.googleapis.com/Job, the labels.job_uid filter `fugaro logs`
//     reads a job's task logs by, and following a job's logs.
//   - Task 5 (built): the reaper in reap.go.
//   - Task 6: provisioning (the Batch API, the service accounts, IAM, the
//     boot image, the NAT/router) and the IAM that lets a launcher create a
//     job.
//   - Task 7 (built): the job definition -- the VM's machine type and
//     preloaded boot disk, the network and subnetwork with no external IP,
//     the task's own quantities, the Docker socket mount and the env that
//     points at it, and the refusals -- plus Launch, Execution reading the
//     job's own machine type back, and the SECURITY.md section for the
//     backend's model.
//   - Task 8: the config plumbing that fills Options in, including a
//     workflow's `docker: true` reaching LaunchSpec.Docker and
//     agent.EnvSpec.Docker.
package batch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"sort"
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
// real Google endpoint. Compute is the reaper's (reap.go): the backend itself
// never calls the Compute Engine API.
type Endpoints struct {
	Batch, Logging, Compute string
	NoAuth                  bool
}

// clientOptions builds one Google client's options: a discarding logger (see
// discardLogger), the endpoint override a fake or emulator needs, and either
// ADC or no authentication at all.
func clientOptions(endpoint string, o Endpoints, httpClient *http.Client, quotaProject string) []option.ClientOption {
	opts := []option.ClientOption{option.WithLogger(discardLogger)}
	if endpoint != "" {
		opts = append(opts, option.WithEndpoint(endpoint))
	}
	if o.NoAuth {
		opts = append(opts, option.WithoutAuthentication())
	}
	if httpClient != nil {
		opts = append(opts, option.WithHTTPClient(httpClient))
	}
	if !o.NoAuth {
		opts = append(opts, option.WithQuotaProject(quotaProject))
	}
	return opts
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

// HostSocketPath is where Container-Optimized OS keeps its Docker socket,
// on the VM and therefore inside the container the mount puts it in (design
// §2.2). It is the default for Docker.SocketPath.
const HostSocketPath = "/var/run/docker.sock"

// defaultBootDiskGB is the boot disk a run's VM gets when Options.BootDiskGB
// is unset: the 50 GB Check 57a's jobs carried (design §7.2), which a disk
// created from the preloaded image is grown to.
const defaultBootDiskGB = 50

// defaultMachineType is the machine type a run's VM gets when
// Options.MachineType is unset: the smallest e2-standard type whose task share
// (half its vCPUs and a quarter of its memory, run.go's taskResources) carries
// what a workflow that names no `resources:` asks for -- the local config's
// default of 4 CPUs and 8Gi (internal/config/defaults.go), which is 8 vCPUs
// and 32 GiB of machine, `e2-standard-8`.
//
// The share has to hold the default ask rather than nearly hold it. The design's
// own answer for a run that wants more than its share is a larger machine type
// (design §9 step 9), and CheckResources refuses the ask that exceeds it -- so a
// default machine type below the default ask is a default installation on which
// every workflow that names no resources is refused until the operator raises
// `batch.machine_type`: no default at all. That is the shape this constant had
// while it was `e2-standard-4`, whose task share is 2000m/4096MiB -- Check 57a's
// measured request, and below the default ask.
//
// The design's e2-standard-4 (§1.3: "the likely default", and the type 57a
// measured the cold start, the shares and the price on, §7.2) is therefore not
// this fallback's value. Its likely-default sentence is about which type the
// measurements were taken on; what a run gets when the installation names no
// type is a choice, and the one that works is the type sized to the default ask.
// An installation that wants the cheaper measured type names it: `batch.
// machine_type: e2-standard-4` and workflows that name no resources are then
// refused with the reason and the larger type named, which is the operator's
// decision to make rather than a silent one.
const defaultMachineType = "e2-standard-8"

// Docker is the VM's Docker daemon as a run reaches it: the socket, mounted
// from the host (design §2.2, C2). It is the installation's, not the run's --
// the gid belongs to the boot image, so every run of an installation reads the
// same one -- and a run asks for it with LaunchSpec.Docker.
type Docker struct {
	// SocketGID is the gid of the socket *on the VM*, read there rather than
	// assumed: Check 57a step 6 read `stat -c %g /var/run/docker.sock` off the
	// boot image it used and got 412, and a gid written into this package
	// would be a guess about whatever boot image an installation runs. Task 6
	// reads it off the VM it builds the preloaded image on and records it;
	// nothing here carries a literal one. It must be a real gid, because the
	// recipe is `--group-add <gid>`: the name form (`--group-add docker`) is
	// resolved inside the container's own /etc/group, where the base image has
	// no docker group, so it fails before the daemon is ever reached.
	//
	// Zero means this installation has no socket recorded: a run that asked
	// for Docker is then refused (ErrRejected) rather than launched with a
	// socket it cannot open.
	SocketGID int
	// SocketPath is the socket's path on the VM and, through the mount, in the
	// container. Empty means HostSocketPath.
	SocketPath string
}

// path is the socket's path, with the default filled in.
func (d Docker) path() string {
	if d.SocketPath == "" {
		return HostSocketPath
	}
	return d.SocketPath
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
	// MachineType is the Compute Engine machine type a run's VM is (design
	// §1.3, §4.3). It sets the job's computeResource and the machine type
	// Execution reports, and its quantities are what the run's compute cost
	// is priced from (design §5.2) -- so it must be one the price table
	// carries (prices.go): a type whose figures are unknown can neither be
	// priced nor reported honestly. Empty means defaultMachineType, the
	// smallest type whose task share carries the config's default ask.
	MachineType string
	// BootImage is the boot disk's image: the preloaded image Task 6 builds
	// per base version, with fugaro-base already in its Docker data root
	// (design §1.2, C4/C19). Required, and not an optimisation: a run that
	// pulled the image instead cold-starts in about 145 s against the
	// budget's 133 s ceiling, and the other fast path, image streaming,
	// makes Batch ignore the options field a socket needs (§1.1/§1.2) -- so
	// a Docker run's only fast start is this image.
	BootImage string
	// BootDiskGB is the VM's boot disk size in GB (Batch's Disk.sizeGb).
	// Zero means defaultBootDiskGB, the 50 Check 57a's jobs carried.
	BootDiskGB int64
	// Network and Subnetwork are the interface a run's VMs are placed in, as
	// full resource names: projects/<p>/global/networks/<n> and
	// projects/<p>/regions/<region>/subnetworks/<s>. Both are required and
	// both are sent, because a run's VMs carry no external IP (§1.7, C7) and
	// Check 57a measured that Batch refuses such a job unless both are named
	// explicitly -- noExternalIpAddress is not expressible with the field
	// alone. The network may live in another project (a shared VPC); the
	// subnetwork must be in the backend's own region, because that is where
	// the job's VMs are.
	Network, Subnetwork string
	// Docker is how a run that asked for Docker reaches the VM's daemon
	// (design §2.2). Its zero value means this installation has no socket
	// recorded, and a run that asks for one is refused.
	Docker Docker
	// ImageStreaming turns on Batch's image streaming for a run's runnable
	// (design §1.2). It is off by default and is refused for a Docker run,
	// because with streaming on Batch disregards the runnable's options
	// field -- measured on 2026-10-15 with `--group-add` and streaming
	// together, one variant, one job -- so the socket recipe could not be
	// passed and the container would come up unable to open the socket §2.2
	// mounts. A run that needs no socket may stream.
	ImageStreaming bool
	// Timeouts is where the workflow timeouts come from. Required.
	Timeouts  Timeouts
	Endpoints Endpoints
	// HTTPClient, when set, is the client the API calls use (tests).
	HTTPClient *http.Client
	// LogView, when set, is the log view
	// (projects/<p>/locations/<l>/buckets/<b>/views/<v>) logs are read
	// through and log URLs open; empty reads the project's default logs.
	LogView string
	// LogSettle is how long Logs(follow) waits after a job has ended with
	// no new entry before it returns: Cloud Logging's ingestion lag. Zero
	// means 30 seconds, gcp's own default.
	LogSettle time.Duration
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
	if err := CheckOptions(&o); err != nil {
		return nil, err
	}
	if o.Warn == nil {
		o.Warn = func(string) {}
	}
	if o.LogSettle == 0 {
		o.LogSettle = 30 * time.Second
	}
	bs, err := batch.NewService(ctx, clientOptions(o.Endpoints.Batch, o.Endpoints, o.HTTPClient, o.GCPProject)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Batch: %w", err)
	}
	ls, err := logging.NewService(ctx, clientOptions(o.Endpoints.Logging, o.Endpoints, o.HTTPClient, o.GCPProject)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Logging: %w", err)
	}
	return &Backend{o: o, batch: bs, logs: ls, codec: backend.BatchCodec(), now: time.Now, region: o.Region, listPageSize: 100}, nil
}

// CheckOptions refuses an Options this backend cannot build a run's job from,
// and fills the VM defaults in (see checkVM). It is the whole of New's
// validation, as one function, because there are two callers: New, which
// builds a client behind it, and `fugaro doctor`, which reports the same
// refusals against an installation's recorded facts before anything is read
// from the project (design compute-backend.md §4.4's doctor half: "the job
// JSON's network and subnetwork named"). A doctor that had to construct a
// backend to learn this would open a Batch and a Logging client to answer a
// question that is about the local config alone.
func CheckOptions(o *Options) error {
	switch {
	case o.GCPProject == "" || o.Region == "":
		return errors.New("the Batch backend needs a GCP project and a region")
	case o.FugaroProject == "":
		return errors.New("the Batch backend needs the Fugaro project's name (the job env's FUGARO_PROJECT)")
	case o.Image == "":
		return errors.New("the Batch backend needs the image a run's job runs")
	case o.Timeouts == nil:
		return errors.New("the Batch backend needs a source for the workflow timeouts")
	}
	if err := checkServiceAccount(o.ServiceAccount); err != nil {
		return err
	}
	if err := checkSecrets(o.Secrets); err != nil {
		return err
	}
	return checkVM(o)
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

// envNameRE is what an environment variable name looks like: the form every
// container runtime accepts, and therefore the form Options.Secrets' keys must
// have.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// secretNameRE is what Options.Secrets' values must look like: a Secret
// Manager secret's resource name, with or without an explicit version. The
// version is optional because Batch accepts the name and resolves the latest
// version on the VM, and an installation that wants a pinned version gets one
// by naming it (design §1.6).
var secretNameRE = regexp.MustCompile(`^projects/([a-z][a-z0-9-]{4,28}[a-z0-9])/secrets/([A-Za-z0-9_-]{1,255})(/versions/([A-Za-z0-9_-]{1,255}))?$`)

// checkSecrets refuses a Secrets map that could put a secret's VALUE into a
// job definition. The job object is readable by anyone with batch.jobs.get,
// and the whole point of the field is that the values never enter it (design
// §1.6, C10): the map is environment variable NAME -> Secret Manager secret
// NAME, and the VM resolves the names as the job's service account.
//
// Both halves are checked, because either can be misused to smuggle a value
// in: a caller that passes a value where a name belongs, and a caller that
// passes a value as the environment variable name. The check is here rather
// than at the create so that an installation misconfigured once is refused at
// startup, not at the first run that asked for a secret.
//
// **What the check can and cannot tell apart.** The value slot has a shape to
// check -- a Secret Manager resource name is not a secret's value -- but the
// key slot does not: an environment variable name and a secret's value can be
// spelled identically. `AKIAIOSFODNN7EXAMPLE` is an AWS access key id and a
// legal environment variable name; so is a GitHub token. So a key that passes
// envNameRE **may still be a secret's value**, and nothing here can say which
// it is. What this function refuses is the two shapes it can recognise: a
// value passed as a key in a name-shaped spelling is accepted, reaches the job
// as a `secretVariables` entry, and sets a variable the workflow never asked
// for -- which is why the map is built by the config plumbing from the
// installation's own configuration (Task 8) and never from anything a workflow
// or a run can write.
//
// **Neither refusal repeats what it refused.** The key slot's refusal names
// nothing, because a key that failed envNameRE may be a secret's value. The
// value slot's refusal names the entry **by position** and not by its key, for
// the same reason: the key is only *usually* the variable's name, so a caller
// who put a value there (Secrets{"AKIAIOSFODNN7EXAMPLE": "AWS_ACCESS_KEY_ID"})
// would otherwise have that value printed back at them. An error string is
// written to terminals, logs and issue reports; it is never a place to print a
// secret.
func checkSecrets(secrets map[string]string) error {
	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	// Sorted, so an installation with two bad entries reports the same one
	// every time (a map's order is not one), and so an entry's position in the
	// messages below is stable.
	sort.Strings(keys)
	for i, k := range keys {
		if !envNameRE.MatchString(k) {
			return errors.New(`an entry of the Batch backend's secrets has a key that is not an environment variable name: the map is variable name -> Secret Manager secret name, so each key is the variable's name ([A-Za-z_][A-Za-z0-9_]*). The refused key is not repeated here, because a secret's value is the one thing that must not be: if a value was passed as a key, this is where it stops, and this message is not the place to print it (design compute-backend.md §1.6, C10)`)
		}
		if !secretNameRE.MatchString(secrets[k]) {
			return fmt.Errorf("entry %d of %d in the Batch backend's secrets is not a Secret Manager secret's resource name: it must be projects/<project>/secrets/<secret> or projects/<project>/secrets/<secret>/versions/<version>. The entry is named by its position and not by its key, because a key is only usually the variable's name: if a secret's value was passed on either side of the map, this message must not print it. A job carries names and never values (design compute-backend.md §1.6, C10)", i+1, len(keys))
		}
	}
	return nil
}

// checkVM validates and completes the VM half of a job definition (design
// §1.2, §1.3, §1.7, §2.2), and fills the defaults in. Every field it refuses
// would otherwise be a run that cold-starts too slowly, is placed nowhere, or
// asks for a socket it cannot open -- each of which fails after the launcher
// has told the operator the run started.
func checkVM(o *Options) error {
	if o.MachineType == "" {
		o.MachineType = defaultMachineType
	}
	if _, err := MachineTypeOf(o.MachineType); err != nil {
		return fmt.Errorf("the Batch backend's machine type: %w", err)
	}
	if o.BootImage == "" {
		return errors.New("the Batch backend needs the preloaded boot image a run's VM boots from: a run that pulled the image instead cold-starts over the tier's budget, and image streaming cannot carry the Docker socket this backend exists for (design §1.2, C4)")
	}
	switch {
	case o.BootDiskGB < 0:
		return fmt.Errorf("the Batch backend's boot disk size %d is negative", o.BootDiskGB)
	case o.BootDiskGB == 0:
		o.BootDiskGB = defaultBootDiskGB
	}
	return checkNetwork(o)
}

// networkNameRE is the network part of a network interface: a global network's
// full resource name. Anything shorter (a bare "default") is what gcloud fills
// in from its ambient configuration, and a job sent with one is a job placed in
// whatever project the caller's shell happened to name (the trap Check 57's
// guard block is written around).
var networkNameRE = regexp.MustCompile(`^projects/([a-z][a-z0-9-]{4,28}[a-z0-9])/global/networks/([a-z0-9-]{1,63})$`)

// subnetworkNameRE is the subnetwork part of a network interface. The second
// capture is its region, which checkNetwork compares with the backend's own:
// the region is the one field of the name this backend acts on, so it parses
// it out rather than trusting the caller to have matched them.
var subnetworkNameRE = regexp.MustCompile(`^projects/([a-z][a-z0-9-]{4,28}[a-z0-9])/regions/([a-z][a-z0-9-]{0,62})/subnetworks/([a-z0-9-]{1,63})$`)

// checkNetwork refuses a network interface Batch cannot place a VM in. Both
// names are required: Check 57a measured that Batch refuses
// noExternalIpAddress unless both the network and the subnetwork are named
// explicitly (design §1.7), and a job with neither would take the project's
// defaults and an external IP, which C7 has the run without.
func checkNetwork(o *Options) error {
	if o.Network == "" || o.Subnetwork == "" {
		return errors.New("the Batch backend needs a network and a subnetwork for a run's VMs, both as full resource names (projects/<p>/global/networks/<n>, projects/<p>/regions/<r>/subnetworks/<s>): Batch refuses a no-external-IP job unless both are named explicitly (design §1.7)")
	}
	if !networkNameRE.MatchString(o.Network) {
		return fmt.Errorf("the Batch backend's network %q is not a network's full resource name (projects/<p>/global/networks/<n>)", o.Network)
	}
	m := subnetworkNameRE.FindStringSubmatch(o.Subnetwork)
	if m == nil {
		return fmt.Errorf("the Batch backend's subnetwork %q is not a subnetwork's full resource name (projects/<p>/regions/<r>/subnetworks/<s>)", o.Subnetwork)
	}
	// A shared VPC's network may live in another project, but the subnetwork
	// is where the VMs are: a job whose subnetwork is in a second region is a
	// job whose VMs Batch places somewhere this backend's List, reaper and
	// logs do not look.
	if m[2] != o.Region {
		return fmt.Errorf("the Batch backend's subnetwork %s is in region %s, not in the backend's own %s", o.Subnetwork, m[2], o.Region)
	}
	return checkDocker(o.Docker)
}

// checkDocker refuses a socket configuration that could not work. A gid is
// read off the VM (design §2.2), so a negative one is a value no VM answered,
// and a path has to be the path of a socket on that VM: the mount is
// `<path>:<path>`, and `--group-add <gid>` is what opens it, so a path that is
// the root directory, a relative path or a file that is not the daemon's
// socket is a mount of nothing, a mount of somewhere else, or a bind of a
// non-socket that the container comes up unable to talk to.
//
// The name check is deliberately narrow -- the last path element must be
// `docker.sock` -- because this field is the one thing about the socket that
// comes from an operator rather than from the VM, and every value it accepts
// is a path the mount and the run's `DOCKER_HOST` both carry. An installation
// whose daemon listens somewhere else is a fact to bring to this package, not
// something to spell around.
func checkDocker(d Docker) error {
	switch {
	case d.SocketGID < 0:
		return fmt.Errorf("the Docker socket's gid %d is not a gid: read it off the VM with `stat -c %%g %s` (design §2.2)", d.SocketGID, d.path())
	case d.SocketGID == 0 && d.SocketPath != "":
		return errors.New("the Docker socket has a path but no gid: without the gid a run that asks for Docker is refused, and a configured path alone cannot open the socket")
	case !path.IsAbs(d.path()):
		return fmt.Errorf("the Docker socket's path %q is not an absolute path on the VM", d.path())
	case path.Clean(d.path()) == "/":
		return fmt.Errorf("the Docker socket's path %q is the VM's root directory, not a socket: the job mounts this path from the host into the container, so it would hand the run the whole filesystem of the machine that has the daemon (design §2.2)", d.SocketPath)
	case path.Clean(d.path()) != d.path():
		return fmt.Errorf("the Docker socket's path %q is not a clean path (%q): the job mounts it verbatim, so it must be spelled the way it is on the VM", d.SocketPath, path.Clean(d.path()))
	case path.Base(d.path()) != "docker.sock":
		return fmt.Errorf("the Docker socket's path %q does not end in docker.sock: it is the daemon's socket on the VM (Container-Optimized OS keeps it at %s), not a directory or another file (design §2.2)", d.path(), HostSocketPath)
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
// region whose every part is [a-z0-9-]. Callers that know the run bind it to
// the run by FUGARO_RUN (gcp.CheckRunExecution), not by the job, which a
// shared job no longer makes an identity.
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

// notCreated marks a Launch failure from before the create: nothing was sent
// to Batch, so nothing was created, whatever the cause. ErrRejected is how a
// backend says that (design §2.3), and the launcher acts on it by dropping
// the run's claim and the budget token it minted and telling the operator to
// fix the cause and retry (internal/cli/run.go). Without it a bad argument or
// an unwritable label reads as "the outcome is unknown; don't relaunch before
// <claim TTL>", which is both untrue and what makes a corrected retry wait.
//
// The wrapped error stays visible to errors.Is, so an unknown workflow is
// still ErrNotFound as well.
func notCreated(err error) error {
	return fmt.Errorf("%w: %w", backend.ErrRejected, err)
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
