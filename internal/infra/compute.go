package infra

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
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
	// delete it — and nothing else, setMetadata included.
	PreloadRoleID = "fugaroPreloadBuilder"
	// PreloadImage is the prefix of the preloaded boot image's name in the
	// installation's project. The module appends the hash of the pinned
	// base version (PreloadedImage), so one pin is one image and a rebuild
	// after the family rolls produces a new name.
	PreloadImage = "fugaro-preload"
	// PreloadSchedulerJob is the Cloud Scheduler job that rebuilds it: a
	// Batch custom image's supported lifetime can end 60 days after its
	// start, so the image is rebuilt on a schedule, never built once
	// (§1.2).
	PreloadSchedulerJob = "fugaro-preload-rebuild"
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

// LauncherRunGrant is one (launcher, run account) pair: the launcher holds
// Service Account User on that one account and on no other. The module is
// given the pairs, never the cross product, so a launcher cannot act as an
// account this installation did not name.
type LauncherRunGrant struct {
	Member  string `json:"member"`
	Account string `json:"account"`
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
	// LauncherRunGrants are the (launcher, run account) actAs pairs.
	LauncherRunGrants []LauncherRunGrant `json:"launcher_run_grants"`
	// RunAccounts are the accounts a run's VM runs as (§3.2). They hold
	// Batch Agent Reporter, Logs Writer and a reader grant on the registry
	// repository below, and nothing else. Never the default Compute Engine
	// service account: the design forbids it and the module refuses an
	// empty set.
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

	PreloadZone        string `json:"preload_zone"`
	PreloadSchedule    string `json:"preload_schedule"`
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

// PreloadedImage is the preloaded boot image's name for one pinned base
// image: the module's own derivation (preload.tf), restated here so a caller
// can name the image it expects without reading the state back. The eight hex
// digits are the first of sha256(base), which is what the module's
// substr(sha256(var.base_image), 0, 8) computes.
func PreloadedImage(base string) string {
	sum := sha256.Sum256([]byte(base))
	return PreloadImage + "-" + hex.EncodeToString(sum[:])[:8]
}
