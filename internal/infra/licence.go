package infra

import (
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The licence renewal job (plan K4.2 of docs/licence-key-v2): the installation's
// own scheduled `fugaro licence renew --job`, whose CODE is the Subscription
// Edition's (ee/register/licence_renew.go, K4.1) and whose resources are this
// installation root's.
//
// Nothing here reads a key, and that is D524 rather than an omission: the core
// provisions a container, an account and a schedule, the container starts
// EMPTY, and the only thing that ever writes a value into it is a person
// running `fugaro licence set` in a tagged build. An installation that buys no
// licence therefore holds the job, the account and the empty container, and the
// job's own command no-ops and says so (licence_renew.go's third difference: an
// installation with no key is the ordinary state of one that never bought one).
//
// The job is created under the COORDINATOR's own condition and not one of its
// own: its image is the coordinator's (one CLI binary serves the sweep, the
// front door and this job), so an installation that has not opted into the
// coordinator has no tagged image to run and no job, and `fugaro licence
// status` says renewal is by hand there (TN5). modules/installation/licence.tf
// holds the switch and reads it from local.deploy_coordinator, so the two
// cannot drift.

const (
	// LicenceAccountID is the renewal job's account: the one identity that may
	// read and write the installation's licence object and the four calls of
	// its renewal secret, and nothing else of the installation's. It is
	// tf.LicenceAccountID on the review's side, and the module's variable
	// validation pins the account ID it is given to that literal.
	LicenceAccountID = tf.LicenceAccountID
	// LicenceJob is the Cloud Run job's name. It must not start with fugaro-,
	// which `fugaro runs ls` and max_parallel count as workflow jobs (the
	// history job's rule, HistoryJob).
	LicenceJob = "fugarolicence"
	// LicenceSchedulerJob is the Scheduler job that runs it. Scheduler names
	// are not counted anywhere, so this one carries the prefix
	// (CoordinatorSchedulerJob's reason).
	LicenceSchedulerJob = "fugaro-licence-renew"

	// LicenceObject is the project's licence object in the runs bucket: the
	// whole of a project's licence, and the one object the job writes. It is
	// ee/licence/project's Object, which the core may not import (D524, and
	// that package's own header says the split is what keeps the verifier's
	// package free of a network client), so the name is a literal here and
	// ee/register's TestTheLicenceObjectIsTheStores pins the two.
	LicenceObject = "fugaro/licence.json"

	// LicenceRenewalSecretLogical is the logical name of the Secret Manager
	// container holding the installation's per-key renewal secret, the name
	// gcp.ProjectSecretID derives the container's ID from. K3.2's `fugaro
	// licence set` writes versions into that container and refuses before it
	// exists with one line naming `fugaro update`; the ID is a hash and cannot
	// be computed in Terraform, so the derivation lives here and the spec below
	// carries the answer (ee/register's licenceRenewalSecretID delegates to
	// LicenceRenewalSecretID, so the two sides cannot drift).
	LicenceRenewalSecretLogical = "licence-renewal"
)

// LicenceRenewalSecretID is the ID of that container for the Fugaro project
// named fugaroProject (the local config's name, not the GCP project).
func LicenceRenewalSecretID(fugaroProject string) string {
	return gcp.ProjectSecretID(fugaroProject, LicenceRenewalSecretLogical)
}

// LicenceSpec is the installation root's licence variable: the renewal job and
// the account it runs as, plus the container the secret it renews with lives
// in. Every value is computed here or by the module; nothing in the module
// derives one.
type LicenceSpec struct {
	AccountID string `json:"account_id"`
	Job       string `json:"job"`
	// Image is the coordinator's image (the history image, HistoryImage): one
	// CLI binary serves the sweep, the front door and this job, so the licence
	// job needs no image of its own, no second push and no second entry in the
	// mirror that fills the base registry.
	Image           string `json:"image"`
	SchedulerJob    string `json:"scheduler_job"`
	SchedulerRegion string `json:"scheduler_region"`
	// Env is the job's environment: the platform variables every job has
	// (platformEnvFor), and nothing else. FUGARO_PROJECT and FUGARO_GCP_PROJECT
	// are the whole of what the job needs to find its project — it has no
	// checkout and no config file on its machine, so it resolves the project
	// from those two names and reads the installation's published config out of
	// the bucket they point at (internal/cli's selectWith) — and no entry here
	// is a credential: the job mounts no secret, and its FUGARO_BUCKET is the
	// bucket it already holds a grant on.
	Env map[string]string `json:"env"`
	// ReadCondition is the account's read of the runs bucket — the project
	// marker, the published config and the licence object, by name, and nothing
	// else of the bucket — byte for byte the condition gcp builds.
	//
	// The marker and the config are the other two names and not an accident:
	// the job has no local config (it is a Cloud Run container), so finding
	// WHICH installation it renews for is those two reads, the same two every
	// unconfigured machine makes. A grant naming the licence object alone would
	// be a job that fails before it reaches the object it exists for. What the
	// read does not carry is any listing: a List request names no object, so no
	// clause of a name condition matches one, which is what keeps the account
	// out of the run records, the prompts and the transcripts the bucket holds.
	ReadCondition Condition `json:"read_condition"`
	// WriteCondition is its write of the one object the job exists to renew
	// (LicenceObject), byte for byte the condition gcp builds: objectUser under
	// a name condition, because the write is a compare-and-set that replaces the
	// object the job read in a versioned bucket.
	WriteCondition Condition `json:"write_condition"`
	// RenewalSecret is the ID of the Secret Manager container the job reads and
	// writes. The module creates it EMPTY and never reads a version of it
	// (D524): `fugaro licence set` is what puts the first value in, and the job
	// is granted its four calls (secrets.get, versions.list, versions.access,
	// versions.add) on that one container and on nothing else in the project.
	RenewalSecret string `json:"renewal_secret"`
}

// Licence is the renewal job's spec for lc, whose runs bucket is bucket (the
// installation's validated one; the two conditions are built from it).
//
// The image is the history image's for the reason above, and the schedule is
// the module's own (a fixed daily tick): `check_interval` is P1D at its finest
// (ee/licence/key.go's ErrDuration reads only two ISO forms), so one tick a day
// is what a key's own terms can ask for, and the job decides from the key
// whether this tick has anything to do.
func Licence(lc *localcfg.Config, bucket string) (LicenceSpec, error) {
	if !gcp.ValidBucketName(bucket) {
		// The conditions are CEL strings built from the bucket and compared byte
		// for byte by the plan review, so a bad name must be refused here rather
		// than written into a grant nothing matches (Installation's own read).
		return LicenceSpec{}, userErr("the runs bucket %q has a byte outside a-z, 0-9, '.', '_' and '-'", bucket)
	}
	host, err := RegistryHost(lc)
	if err != nil {
		return LicenceSpec{}, err
	}
	region := lc.SchedulerRegion
	if region == "" {
		if region, err = gcp.SchedulerRegion(lc.Region); err != nil {
			return LicenceSpec{}, userErr("%v", err)
		}
	}
	return LicenceSpec{
		AccountID:       LicenceAccountID,
		Job:             LicenceJob,
		Image:           HistoryImage(host),
		SchedulerJob:    LicenceSchedulerJob,
		SchedulerRegion: region,
		Env:             platformEnvFor(lc, lc.Name, bucket),
		ReadCondition: Condition{
			Title:      gcp.LicenceReadConditionTitle(tf.LicenceAccountID),
			Expression: gcp.LicenceReadCondition(bucket),
		},
		WriteCondition: Condition{
			Title:      gcp.LicenceWriteConditionTitle(tf.LicenceAccountID),
			Expression: gcp.LicenceWriteCondition(bucket),
		},
		RenewalSecret: LicenceRenewalSecretID(lc.Name),
	}, nil
}
