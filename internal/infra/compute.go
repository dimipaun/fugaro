package infra

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	scheduler "google.golang.org/api/cloudscheduler/v1"
	"google.golang.org/api/googleapi"
	iam "google.golang.org/api/iam/v1"
)

// The Batch backend's installation-level provisioning (design
// docs/design/compute-backend.md §3.2, §3.7, §8.1 task 6). It is a root of
// its own, in a workdir and a state of its own, because what it changes is
// not what the installation root changes: the installation's tfvars, imports
// and saved plan must not be rewritten by a root that only the compute
// backend's setup uses. The installation root's arm of the log sink already
// routes a Batch job's lines (Task 4, merged), so nothing here touches
// logging.
//
// Nothing in this file is reachable from a run: `backend: cloud-batch` stays
// refused by config validation until the enable task (§8.1 task 9), and the
// stage that applies the root is behind its own flag.

// StatePrefixCompute is where the compute root's state lives in the state
// bucket: a prefix of its own, so neither root ever reads or writes the
// other's state.
const StatePrefixCompute = "fugaro/compute"

// The compute root's own names, the module's `names` object. They are
// constants rather than derived: a role id and an account id are what the
// reaper's configuration, the launcher's Condition and a later `doctor` check
// have to agree on, and a name that moved between releases would orphan the
// account it names.
const (
	// JobPrefix is the prefix every Batch job's name starts with. The
	// launcher's Batch Job Editor grant is narrowed to exactly it (57a-9's
	// Condition, measured accepted and read back as written on 2026-10-15,
	// §3.7), and the reaper's sweep is defined over the same prefix, so
	// both read this one constant.
	JobPrefix = "fugaro-"
	// ReaperAccountID is the reaper's dedicated service account (§3.7).
	ReaperAccountID = "fugaro-reaper"
	// ReaperRoleID is the reaper's custom role: exactly the six
	// permissions the reaper's code uses, never roles/batch.jobsEditor and
	// never roles/compute.instanceAdmin.v1.
	ReaperRoleID = "fugaroReaper"
	// PreloadAccountID is the account the preloaded boot image's build runs
	// as: the Cloud Build step itself, and the Cloud Scheduler call that
	// starts it.
	PreloadAccountID = "fugaro-preload"
	// PreloadRoleID is the preload builder's custom role: boot one COS VM,
	// read its startup script's serial output, stop it, image its disk and
	// delete it — and nothing else. It is longer than the four calls suggest,
	// because a create is checked argument by argument (boot disk, interface,
	// startup script, labels, the account the VM runs as, the image it boots
	// from); it holds no instance or image admin role, and no permission to
	// create a job or a second VM.
	PreloadRoleID = "fugaroPreloadBuilder"
	// PreloadImage is the prefix of the preloaded boot image's name in the
	// installation's project. The module appends the hash of the pinned
	// base version (PreloadedImage), so one pin is one image; a new pin is a
	// new name and the old image is swept. Google rolling the base family
	// does NOT move it — the pin does not move with it — which is why the
	// release an image was built from is recorded on the image itself
	// (preload.sh's base-release label) and why the scheduled rebuild
	// replaces an image that is stale or past its window.
	PreloadImage = "fugaro-preload"
	// PreloadSchedulerJob is the Cloud Scheduler job that rebuilds it: a
	// Batch custom image's supported lifetime can end 60 days after its
	// start, so the image is rebuilt on a schedule, never built once
	// (§1.2).
	PreloadSchedulerJob = "fugaro-preload-rebuild"
	// PreloadRefreshDaysDefault is how old the preloaded image may be before
	// the scheduled rebuild replaces it: half the 60 days Batch documents,
	// so a weekly schedule has a spare attempt inside the lifetime. The
	// module and the root default to the same number, and the module, this
	// package and the root all refuse 60 or more
	// (TestPreloadRefreshWindowIsInsideTheSupportedLifetime).
	PreloadRefreshDaysDefault = 30
)

// ComputeNames is the module's `names` object.
type ComputeNames struct {
	JobPrefix           string `json:"job_prefix"`
	ReaperAccountID     string `json:"reaper_account_id"`
	ReaperRoleID        string `json:"reaper_role_id"`
	PreloadAccountID    string `json:"preload_account_id"`
	PreloadRoleID       string `json:"preload_role_id"`
	PreloadImage        string `json:"preload_image"`
	PreloadSchedulerJob string `json:"preload_scheduler_job"`
}

// ComputeNamesFor is an installation's names object: the constants above, and
// nothing a caller may vary.
func ComputeNamesFor() ComputeNames {
	return ComputeNames{
		JobPrefix:           JobPrefix,
		ReaperAccountID:     ReaperAccountID,
		ReaperRoleID:        ReaperRoleID,
		PreloadAccountID:    PreloadAccountID,
		PreloadRoleID:       PreloadRoleID,
		PreloadImage:        PreloadImage,
		PreloadSchedulerJob: PreloadSchedulerJob,
	}
}

// ComputeSpec is the compute root's terraform.tfvars.json (§8.1 task 6).
// Every field is an input of the root's own variables.tf and its key is that
// file's variable name; TestComputeVarsMatchRootVariables holds the two
// together, so a variable added to the root and not fed here fails a test
// rather than a plan.
type ComputeSpec struct {
	Project       string `json:"project"`
	ProjectNumber string `json:"project_number"`
	Region        string `json:"region"`
	// Network and Subnetwork are required, and both in full: Batch REFUSES
	// noExternalIpAddress unless the job's interface names BOTH (measured
	// 2026-10-15, Check 57a; §1.7, §7.2). The NAT is created on them.
	Network    string `json:"network"`
	Subnetwork string `json:"subnetwork"`

	Names ComputeNames `json:"names"`
	// ManageAPIs is false with init --skip-apis: the project's APIs are
	// managed elsewhere, so this root enables neither Compute nor Batch.
	ManageAPIs bool `json:"manage_apis"`

	Launchers []string `json:"launchers"`
	Operators []string `json:"operators"`
	// RunAccounts are the accounts a run's VM runs as (§3.2), as IAM
	// members (serviceAccount:<email>): the module grants them Batch Agent
	// Reporter, Logs Writer and a reader grant on the registry repository
	// below, and gives the launchers actAs on them. Nothing else, and never
	// the default Compute Engine service account: the design forbids it and
	// the module refuses an empty set.
	RunAccounts []string `json:"run_accounts"`

	// RegistryRepository is the Artifact Registry repository the
	// installation's pin names — the remote fugaro-ghcr on 2026-10-15, not
	// the fugaro-base constant (§7.2). It comes from the installation's own
	// outputs, never written out by hand.
	RegistryRepository string `json:"registry_repository"`

	// BaseImage is the image the preload pulls onto the boot image: the
	// installation's pinned base. The preloaded image's own name carries a
	// hash of it (PreloadedImage), so a new pin is a new image and the old
	// one is stale.
	BaseImage string `json:"base_image"`
	// BaseProject and BaseFamily are GOOGLE's, not the installation's: the
	// COS Batch family lives in batch-custom-image (§1.2; §7.2's setup
	// findings — an installation project has no such family).
	BaseProject string `json:"base_project"`
	BaseFamily  string `json:"base_family"`

	PreloadZone     string `json:"preload_zone"`
	PreloadSchedule string `json:"preload_schedule"`
	// PreloadRefreshDays is how old the preloaded image may be before the
	// scheduled rebuild replaces it, in days: the window that keeps the
	// image inside the 60-day lifetime Batch documents for a custom image,
	// and the only thing that refreshes an image whose base family has not
	// rolled (§1.2, C19).
	PreloadRefreshDays int    `json:"preload_refresh_days"`
	BuilderMachineType string `json:"builder_machine_type"`

	// BuildSubmitterRole is the installation's fugaroBuildSubmitter role,
	// which the preload account holds: creating the Cloud Build that builds
	// the image needs it. It is the installation's own role, passed by
	// reference so it cannot drift from the installation that made it.
	BuildSubmitterRole string `json:"build_submitter_role"`
}

// ComputeVars is the compute root's terraform.tfvars.json. It refuses what
// the module refuses and for the same reasons, so a spec that could only
// produce a failing apply fails here instead: a run account set that is empty
// (no job could report its state), and a network or subnetwork that is not
// named (Batch refuses noExternalIpAddress without both).
func ComputeVars(spec ComputeSpec) ([]byte, error) {
	switch {
	case spec.Project == "" || spec.Region == "":
		return nil, fmt.Errorf("the compute root needs a project and a region")
	case spec.Network == "" || spec.Subnetwork == "":
		return nil, fmt.Errorf("the compute root needs the network and the subnetwork: Batch refuses noExternalIpAddress unless both are named (design compute-backend §1.7)")
	case len(spec.RunAccounts) == 0:
		return nil, fmt.Errorf("the compute root needs at least one run account: the design forbids the default Compute Engine service account, and no job could report its state")
	case spec.RegistryRepository == "":
		return nil, fmt.Errorf("the compute root needs the registry repository the installation's pin names (design compute-backend §7.2)")
	case spec.BaseImage == "":
		return nil, fmt.Errorf("the compute root needs the installation's pinned base image: the preloaded boot image is that image pulled onto a COS boot disk, so an empty one would build an image with nothing in it")
	case spec.PreloadRefreshDays <= 0 || spec.PreloadRefreshDays >= 60:
		return nil, fmt.Errorf("the compute root's preload refresh window must be 1-59 days, not %d: a Batch custom image's supported lifetime can end 60 days after its starting date, so a longer window would leave the image unsupported before anything replaced it (design compute-backend §1.2)", spec.PreloadRefreshDays)
	case spec.BuildSubmitterRole == "":
		return nil, fmt.Errorf("the compute root needs the installation's build-submitter role: the preload build is started with it")
	}
	return sortedJSON(spec)
}

// DefaultNetwork is the network the job's VMs are placed on when the caller
// names none: the project's default network. Fugaro does not create a network
// — provisioning one is the owner's decision, and an installation that has
// none gets the default, which enabling Compute creates.
func DefaultNetwork(project string) string {
	return "projects/" + project + "/global/networks/default"
}

// DefaultSubnetwork is the default network's subnetwork of region.
func DefaultSubnetwork(project, region string) string {
	return "projects/" + project + "/regions/" + region + "/subnetworks/default"
}

// ComputeWorkdir is where the compute root is planned and applied:
// $XDG_STATE_HOME/fugaro/terraform/<project>/compute, beside the
// installation's workdir. It is a workdir of its own, so the installation's
// tfvars, imports and saved plan are never rewritten by this root's.
func ComputeWorkdir(getenv func(string) string, project string) (string, error) {
	dir, err := InstallationWorkdir(getenv, project)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(dir), "compute"), nil
}

// ComputeOutputs are what the compute root reads back: the names the stage
// reports and a later `doctor` check reads (§8.1 tasks 6 and 8). Nothing here
// is a secret, and nothing here is a permission.
type ComputeOutputs struct {
	PreloadImage     string `json:"preload_image"`
	ReaperAccount    string `json:"reaper_account"`
	ReaperRole       string `json:"reaper_role"`
	PreloadAccount   string `json:"preload_account"`
	PreloadScheduler string `json:"preload_scheduler_job"`
	Router           string `json:"router"`
	NAT              string `json:"nat"`
	Network          string `json:"network"`
	Subnetwork       string `json:"subnetwork"`
}

// DecodeComputeOutputs reads the compute root's outputs.
func DecodeComputeOutputs(raw map[string]json.RawMessage) (ComputeOutputs, error) {
	var o ComputeOutputs
	if len(raw) == 0 {
		return o, ErrNoOutputs
	}
	typ := reflect.TypeFor[ComputeOutputs]()
	v := reflect.ValueOf(&o).Elem()
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		val, ok := raw[name]
		if !ok || bytes.Equal(bytes.TrimSpace(val), []byte("null")) {
			continue
		}
		if err := json.Unmarshal(val, v.Field(i).Addr().Interface()); err != nil {
			return ComputeOutputs{}, fmt.Errorf("the compute root's output %s: %w", name, err)
		}
	}
	return o, nil
}

// jobMarkers are the display names a Fugaro job account carries: the current
// one and the M4 bootstrap's. A display name is a service account's ownership
// mark (gcp.JobSADisplayName; discovery compares it exactly and reports any
// other value as foreign), so a listing can tell the installation's run-class
// accounts from every other account in the project without reading a state.
var jobMarkers = []string{"Fugaro job ", "Fugaro M4 job "}

// JobRunAccounts lists the project's Fugaro job accounts, as IAM members
// (serviceAccount:<email>): the run-class identities a Batch job's VM runs as
// (§3.2). They are the accounts the repository roots created for the
// installation's workflows, which is why they are listed rather than derived:
// the local config records workflow names, not the token variants' accounts,
// and an account left out here is a run whose VM cannot report its state.
//
// An empty answer is not an error here but is refused by ComputeVars: with no
// run account the provisioning could only produce jobs that never report.
func JobRunAccounts(ctx context.Context, c *Clients, project string) ([]string, error) {
	if c.IAM == nil {
		return nil, errors.New("no IAM client")
	}
	var out []string
	err := c.IAM.Projects.ServiceAccounts.List("projects/"+project).PageSize(100).Pages(ctx, func(r *iam.ListServiceAccountsResponse) error {
		for _, sa := range r.Accounts {
			if sa.Email == "" || !slices.ContainsFunc(jobMarkers, func(m string) bool { return strings.HasPrefix(sa.DisplayName, m) }) {
				continue
			}
			out = append(out, "serviceAccount:"+sa.Email)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing the project's job accounts: %w", err)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// SchedulerJobName is the full name of one of an installation's Cloud
// Scheduler jobs, as the API takes it.
func SchedulerJobName(project, region, job string) string {
	return "projects/" + project + "/locations/" + region + "/jobs/" + job
}

// RunSchedulerJob starts job now, as its own schedule would. The compute
// stage uses it for the preload rebuild: the module creates the job, and a
// fresh installation must not wait a week for its first preloaded image.
//
// The call is retried while it comes back "forbidden": a binding created
// moments before — the scheduler's own agent's actAs on the account the job's
// OAuth token is minted from — is not readable everywhere at once, which is
// the propagation wait 57a measured (§7.2, §8.1 task 6). A refusal that
// outlasts runJobRetries is returned as it is: the caller reports the one
// command that starts the job by hand.
func RunSchedulerJob(ctx context.Context, c *Clients, project, region, job string) error {
	if c.Scheduler == nil {
		return errors.New("no Cloud Scheduler client")
	}
	name := SchedulerJobName(project, region, job)
	var err error
	for attempt := 0; ; attempt++ {
		if _, err = c.Scheduler.Projects.Locations.Jobs.Run(name, &scheduler.RunJobRequest{}).Context(ctx).Do(); err == nil {
			return nil
		}
		if !propagating(err) || attempt >= len(runJobRetries) {
			return fmt.Errorf("starting the scheduled job %s: %w", name, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("starting the scheduled job %s: %w", name, err)
		case <-time.After(runJobRetries[attempt]):
		}
	}
}

// runJobRetries are the waits between attempts at starting a scheduled job
// whose identity's bindings may not have propagated yet. Tests replace them.
var runJobRetries = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second}

// propagating reports whether err is the refusal a just-created binding
// gets: a 403 whose detail is a PERMISSION_DENIED about the caller or the
// identity it acts as. Only a 403 counts — a 404 (the job is not there) and
// an unreadable answer must fail at once, not be retried into a delay.
func propagating(err error) bool {
	var ae *googleapi.Error
	return errors.As(err, &ae) && ae.Code == http.StatusForbidden
}

// PreloadedImage is the preloaded boot image's name for one pinned base
// image: the module's own derivation (preload.tf), restated here so the Go
// side and the module cannot disagree about what the runs boot and with
// TestPreloadedImageMatchesTheModule holding the two together. The eight hex
// digits are the first of sha256(base), which is what the module's
// substr(sha256(var.base_image), 0, 8) computes. The name says nothing about
// Google's side of the image: the family's release is recorded on the image
// (its base-release label) and how old it is is its creation date, which is
// what the scheduled rebuild reads (preload.sh).
func PreloadedImage(base string) string {
	sum := sha256.Sum256([]byte(base))
	return PreloadImage + "-" + hex.EncodeToString(sum[:])[:8]
}
