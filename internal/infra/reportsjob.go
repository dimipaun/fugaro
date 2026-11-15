package infra

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// ReportsJobDocEnv is where the project's reports job reads its assignment:
// the JSON ReportsJobDoc below, rendered into the installation's tfvars at
// var.reports.env. The job's own reader is the reports command (R11), and this
// release deploys the job without it — the schedule ships stopped, so nothing
// runs the document until the command that reads it lands: what is fixed here
// is the wire format the reader is written against, as CheckSpecEnv's and
// CatalogCheckSpecEnv's are.
const ReportsJobDocEnv = "FUGARO_REPORTS_JOB_SPEC"

// ReportsJobDocVersion is the version of the document ReportsJobDocEnv
// carries. It is what a later release bumps when the document's meaning
// changes (a field never changes meaning in place), and what the reader
// refuses a version below.
const ReportsJobDocVersion = 1

// The reports job's two names (E4). The job's must start with fugarorpt- for
// CheckJobName's reason: `fugaro runs ls` and max_parallel count the fugaro-*
// ones as workflow jobs, and the alert policy matches ^fugarorpt-
// (modules/installation/reports.tf). The Scheduler job's may start with
// fugaro-: no listing counts a Scheduler job (CoordinatorSchedulerJob's
// reason).
const (
	ReportsJobName          = "fugarorpt-reports"
	ReportsSchedulerJobName = "fugaro-reports"
)

// ReportsJobSpec is the installation root's var.reports: the project's one
// scheduled reports job, or nil on an installation this run cannot deploy it
// for.
//
// The job's whole assignment is Env's ReportsJobDocEnv. It has no checkout and
// no local config on its machine — it runs in the project, over the runs
// bucket — so everything it cannot read for itself is in that document: the
// project, the bucket, the repository slugs and the periods to close (D1148's
// reason for the catalog check's own document). Nothing else about the
// installation changes for the job: its account holds the two prefix-limited
// grants the module writes (runs/ read, reports/ write; R12, ruling 3) and no
// project role, it mounts no secret, and its environment is the platform
// variables every job has beside the document.
type ReportsJobSpec struct {
	AccountID string `json:"account_id"`
	Job       string `json:"job"`
	Image     string `json:"image"`
	// SchedulerJob and SchedulerRegion are the Scheduler job that fires the
	// job and the region it runs in, which is not always the job's own:
	// Scheduler is not offered in every Cloud Run region, and the module's
	// http_target names the job's own URI (CatalogCheckSpec's field, the same
	// read).
	SchedulerJob    string `json:"scheduler_job"`
	SchedulerRegion string `json:"scheduler_region"`
	// Paused stops the schedule. The module's variable defaults it to true and
	// applies whatever it is given on every apply, so a `gcloud scheduler jobs
	// resume` would be reverted by the next `fugaro update`: the setting is the
	// layer's jobs.reports.paused (R13), which the owner writes and this run
	// renders (InstallOptions.ReportsPaused).
	Paused bool `json:"paused"`
	// Env is the job's environment: the platform variables every job has
	// (platformEnvFor, the same bytes the catalog check job's env carries) and
	// ReportsJobDocEnv's document, and nothing else — no model credential, no
	// git credential, no secret of any kind (E4).
	Env map[string]string `json:"env"`
	// ReadCondition is the job account's read-only grant on every repository's
	// run records (runs/), byte for byte the condition gcp builds and the
	// installation module writes.
	ReadCondition Condition `json:"read_condition"`
	// WriteCondition is its read-write grant on the stored reports (reports/),
	// byte for byte the condition gcp builds: the two are the whole of the
	// account's reach in the project.
	WriteCondition Condition `json:"write_condition"`
}

// ReportsJobDoc is the document ReportsJobDocEnv carries: one run's whole
// assignment, and the wire format the reports command (R11) reads. The two
// halves spell it once each — this type renders it into the plan, that command
// declares its own copy to decode it — and the round trip over these bytes
// (CheckSpec's own arrangement) is what keeps them in step.
type ReportsJobDoc struct {
	Version int `json:"version"`
	// Project is the installation's GCP project, the one the runs bucket and
	// the job's own account live in. It is the value the job's environment
	// carries as FUGARO_GCP_PROJECT, and one local read twice: the module's
	// precondition holds the two spellings to the same project.
	Project string `json:"project"`
	// Bucket is the runs bucket's name, bare, as the module's own resources
	// name it (the gs:// form is the environment's FUGARO_BUCKET). The stored
	// reports are reports/ of it.
	Bucket string `json:"bucket"`
	// Repositories are the slug of every repository the project reports,
	// sorted (ReportsRepos): the job writes one report per repository and
	// period under reports/<slug>/, and a slug is a function of the provider
	// and the name (task.Slug), so a job with no checkout cannot derive one
	// from a name it is given instead.
	Repositories []string `json:"repositories"`
	// Periods are the period kinds one run closes, in this project's own
	// vocabulary (internal/digest's Dim): the job takes each kind's most
	// recently closed period from its own clock, which is the one thing here a
	// nightly run cannot be handed at plan time. They are rendered as the four
	// this installation closes rather than left to the job, so which periods a
	// stored series holds is a decision of the plan.
	Periods []string `json:"periods"`
}

// reportPeriods are the period kinds a run closes: the four internal/digest
// defines, day first, which is the order a reader of a stored series reads
// them in.
func reportPeriods() []string {
	return []string{string(digest.ByDay), string(digest.ByWeek), string(digest.ByMonth), string(digest.ByYear)}
}

// ReportsJob is the project's reports job for lc, or nil on an installation
// this run cannot deploy it for: one that records no base image, which is the
// image the job runs FROM and the module refuses an empty one. bucket is the
// installation's validated runs bucket.
//
// paused is the schedule's own state, already resolved: the layer's
// jobs.reports.paused, or true wherever it wrote none (InstallOptions'
// ReportsPaused and jobPaused). It is a parameter rather than a field the
// caller sets afterwards so that every field of the spec is built here, where
// the plan review reads it — and it is the ONLY thing about the job a `fugaro
// update` changes once the job exists, so a run whose layer says nothing
// leaves the schedule stopped (R13, E4).
//
// The job is what writes the stored reports at all: nothing else in the
// project writes reports/ (ruling 3), so an installation that never deploys
// this job holds no report series and its rules read the committed folder
// alone.
func ReportsJob(lc *localcfg.Config, bucket string, paused bool) (*ReportsJobSpec, error) {
	image := lc.BaseImage(config.BaseKind)
	if image == "" {
		// The installation records no base image, and this job runs FROM it:
		// the module refuses an empty one, and a job whose image this run
		// guessed would write reports from whatever the guess names. The job
		// is deployed by the first apply after the base image is pinned — the
		// images stage pins it in the same run and the installation root is
		// applied again behind it (#1843) — and an installation that stays
		// without one is the state it was in before the job existed. It is the
		// catalog check's own condition (CatalogCheck), for its reason: the
		// two jobs run the CLI out of the same image.
		return nil, nil
	}
	if !gcp.ValidBucketName(bucket) {
		return nil, userErr("the runs bucket %q has a byte outside a-z, 0-9, '.', '_' and '-'", bucket)
	}
	region := lc.SchedulerRegion
	if region == "" {
		r, err := gcp.SchedulerRegion(lc.Region)
		if err != nil {
			return nil, userErr("%v", err)
		}
		region = r
	}
	raw, err := json.Marshal(ReportsJobDoc{
		Version:      ReportsJobDocVersion,
		Project:      lc.GCPProject,
		Bucket:       bucket,
		Repositories: ReportsRepos(lc),
		Periods:      reportPeriods(),
	})
	if err != nil {
		return nil, err
	}
	env := platformEnvFor(lc, lc.Name, bucket)
	env[ReportsJobDocEnv] = string(raw)
	return &ReportsJobSpec{
		AccountID:       tf.ReportsAccountID,
		Job:             ReportsJobName,
		Image:           image,
		SchedulerJob:    ReportsSchedulerJobName,
		SchedulerRegion: region,
		Paused:          paused,
		Env:             env,
		ReadCondition: Condition{
			Title:      gcp.ReportsReadConditionTitle(tf.ReportsAccountID),
			Expression: gcp.ReportsReadCondition(bucket),
		},
		WriteCondition: Condition{
			Title:      gcp.ReportsWriteConditionTitle(tf.ReportsAccountID),
			Expression: gcp.ReportsWriteCondition(bucket),
		},
	}, nil
}

// ReportsRepos are the repository slugs one report run covers: every
// repository the local config records a provider kind for, as slugs, sorted
// and deduped. The order is the slugs' own rather than the config's keys': the
// list is a set of prefixes a run writes, and two keys that name one
// repository derive one slug between them.
//
// A repository the config records no provider for is left out, for
// CoordinatorRepos' reason: the job runs with no checkout, so nothing on its
// machine can read a provider out of fugaro.yaml, and the slug is derived from
// the provider and the name together (task.Slug) — a provider the config does
// not name is a repository the job could not address at all.
//
// Two keys can name one repository (the canonical fold, localcfg.RepoFor's
// read), and they derive one slug between them: the list is deduped, since a
// slug is what a report's path is made of and a run would otherwise write the
// same report twice.
func ReportsRepos(lc *localcfg.Config) []string {
	out := make([]string, 0, len(lc.Repos))
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		p := lc.Repos[name].Provider
		if !slices.Contains(config.Providers, p) {
			continue
		}
		slug, err := task.Slug(p, name)
		if err != nil {
			// Unreachable: localcfg refuses a repos key that is not an
			// owner/name and a provider kind it does not know.
			continue
		}
		out = append(out, slug)
	}
	return slices.Compact(slices.Sorted(slices.Values(out)))
}
