// Package gcp implements the backend on Google Cloud: Cloud Run jobs,
// Cloud Logging, Secret Manager and Cloud Build (design §3.4, §8).
package gcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Name limits and hash widths (checked 2026-09-28):
//   - Cloud Run job: at most 49 characters ("Cloud Run job names must be 49
//     characters or less", cloud.google.com/run/docs/create-jobs). The
//     execute page adds that job name plus an execution token must stay
//     under 63 (cloud.google.com/run/docs/execute/jobs), which 49 leaves
//     room for.
//   - Service account ID: 6 to 30 lowercase letters, digits and dashes
//     (cloud.google.com/iam/docs/service-accounts-create).
//   - Secret Manager ID: at most 255 of [A-Za-z0-9_-]
//     (secret-manager/docs/reference/rest/v1/projects.secrets/create).
//   - Image repository component: 128 is our own choice, well within the
//     Docker distribution's 255 in total; Artifact Registry states none.
//   - Artifact Registry repository ID: at most 63 lowercase letters,
//     digits and dashes, starting with a letter and ending with a letter or
//     digit (cloud.google.com/artifact-registry/docs/repositories/create-repos).
//
// Each name carries the widest hash its limit allows while keeping a
// readable prefix: 12 hex for jobs, 8 for service accounts (only 30
// characters), 16 for secrets and images. A hash stops accidental
// collisions; the control against a chosen one is the ownership check
// fugaro init makes before adopting a job, service account or secret.
const (
	maxJobName  = 49
	maxSAID     = 30
	maxSecretID = 255
	maxImage    = 128
	maxRegistry = 63

	jobHashHex      = 12
	saHashHex       = 8
	secretHashHex   = 16
	imageHashHex    = 16
	registryHashHex = 12
)

// Labels on the resources Fugaro manages (design §3.2): every one carries
// LabelManaged=ManagedValue; jobs and secrets carry LabelRepo (RepoLabel of
// the slug) and LabelWorkflow or LabelSecret. They are the ownership check
// before a reuse or a delete.
const (
	LabelManaged  = "fugaro"
	ManagedValue  = "managed"
	LabelRepo     = "fugaro_repo"
	LabelWorkflow = "fugaro_workflow"
	LabelSecret   = "fugaro_secret"
	// LabelRole marks a repository's image check job, which carries it
	// (with RoleCheck) in place of LabelWorkflow.
	LabelRole = "fugaro_role"
	// LabelProject names the Fugaro project on the runs bucket (a label
	// value is a valid project name).
	LabelProject = "fugaro_project"
	RoleCheck    = "check"
)

var unsafeLabelRE = regexp.MustCompile(`[^a-z0-9_-]`)

// RepoLabel is the LabelRepo value of a repository slug: every character
// outside [a-z0-9_-] becomes "_", the rule Secret Manager labels use too.
// GCP label values are at most 63 characters. The label derives from the
// slug and must never be truncated: a long slug ends in its hash suffix,
// and cutting it off would make two repositories' labels equal. So a label
// that would be too long is an error, not a shorter label.
func RepoLabel(slug string) (string, error) {
	v := unsafeLabelRE.ReplaceAllString(slug, "_")
	if len(v) > 63 {
		return "", fmt.Errorf("repository slug %s is too long for a GCP label (%d > 63 characters)", slug, len(v))
	}
	return v, nil
}

var unsafeNameRE = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitize(s string) string {
	s = unsafeNameRE.ReplaceAllString(strings.ToLower(s), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// derive is readable, truncated to fit limit, then "-" and hexLen hex of
// sha256(slug NUL second). The readable part is lossy (sanitizing and
// joining with '-' merge distinct inputs); the hash, over the raw pair, is
// what keeps distinct (slug, second) pairs apart. It always ends in a hex
// digit, and starts with readable's first character.
func derive(readable, slug, second string, limit, hexLen int) string {
	sum := sha256.Sum256([]byte(slug + "\x00" + second))
	suffix := "-" + hex.EncodeToString(sum[:])[:hexLen]
	if len(readable) > limit-len(suffix) {
		readable = readable[:limit-len(suffix)]
	}
	readable = strings.TrimRight(readable, "-")
	if readable == "" {
		return suffix[1:]
	}
	return readable + suffix
}

// slugHashRE is task.Slug's 16-hex hash suffix. The readable part of a name
// leaves it out, so it doesn't spend the name's budget; the name's own
// hash, over the full slug, keeps names unique. Stripping a genuine
// 16-hex tail from some other slug only shortens the readable part: it
// never changes the hash, so it can't make two names collide.
var slugHashRE = regexp.MustCompile(`-[0-9a-f]{16}$`)

func readableSlug(slug string) string { return slugHashRE.ReplaceAllString(slug, "") }

func stem(slug, workflow string) string {
	return sanitize("fugaro-" + readableSlug(slug) + "-" + workflow)
}

// JobName is the Cloud Run job of (slug, workflow) (design §3.2):
// fugaro-<slug>-<workflow>, sanitized and truncated, plus a hash of the
// pair.
func JobName(slug, workflow string) string {
	return derive(stem(slug, workflow), slug, workflow, maxJobName, jobHashHex)
}

// ServiceAccountID is the job's dedicated service account ID (design §6.1),
// built like JobName within 30 characters.
func ServiceAccountID(slug, workflow string) string {
	return derive(stem(slug, workflow), slug, workflow, maxSAID, saHashHex)
}

// SecretID is the Secret Manager secret holding a repository's logical
// secret (a workflow secret's name, or one of config.ReservedSecrets),
// built like JobName.
func SecretID(slug, logical string) string {
	return derive(sanitize("fugaro-"+readableSlug(slug)+"-"+logical), slug, logical, maxSecretID, secretHashHex)
}

// ImageName is the derived image of (slug, workflow) in registry, untagged,
// built like JobName without the fugaro- prefix.
func ImageName(registry, slug, workflow string) string {
	return strings.TrimSuffix(registry, "/") + "/" + derive(sanitize(readableSlug(slug)+"-"+workflow), slug, workflow, maxImage, imageHashHex)
}

// Each of the names below hashes the slug in its own domain (a prefix and a
// NUL before the slug), so none can equal a name of another kind: a build
// account is never a job account, even of a workflow named "build".

// BuildServiceAccountID is the repository's build service account, which
// its image builds and daily checks run as: fugaro-b-<slug>, within 30
// characters.
func BuildServiceAccountID(slug string) string {
	return derive(sanitize("fugaro-b-"+readableSlug(slug)), "build\x00"+slug, "", maxSAID, saHashHex)
}

// RegistryRepoID is the repository's own Artifact Registry repository,
// which holds its derived images: fugaro-<slug>-<12 hex>. It is never the
// legacy fugaro or the base registry, since it always ends in a hash.
func RegistryRepoID(slug string) string {
	return derive(sanitize("fugaro-"+readableSlug(slug)), "registry\x00"+slug, "", maxRegistry, registryHashHex)
}

// DefaultRegistryHost is the registry host, the prefix of every image
// registry, of a new installation in region of project:
// <region>-docker.pkg.dev/<project>.
func DefaultRegistryHost(region, project string) string {
	return region + "-docker.pkg.dev/" + project
}

// CheckJobName is the repository's daily image check job. Its fugarochk-
// prefix keeps it out of the fugaro- job listings (ls, max_parallel).
func CheckJobName(slug string) string {
	return derive(sanitize("fugarochk-"+readableSlug(slug)), "check\x00"+slug, "", maxJobName, jobHashHex)
}

// SchedulerJobName is the Cloud Scheduler job that starts CheckJobName
// daily, built the same way.
func SchedulerJobName(slug string) string {
	return derive(sanitize("fugarochk-"+readableSlug(slug)), "scheduler\x00"+slug, "", maxJobName, jobHashHex)
}

// SchedulerRegions are Cloud Scheduler's locations, from
// https://cloud.google.com/scheduler/docs/locations (checked 2026-09-29).
// Scheduler isn't offered in every Cloud Run region.
var SchedulerRegions = []string{
	"asia-east1", "asia-east2", "asia-northeast1", "asia-northeast2", "asia-northeast3",
	"asia-south1", "asia-south2", "asia-southeast1", "asia-southeast2", "asia-southeast3",
	"australia-southeast1", "europe-central2", "europe-west1", "europe-west12", "europe-west2",
	"europe-west3", "europe-west4", "europe-west6", "europe-west8", "europe-west9",
	"me-central1", "me-central2", "me-west1", "northamerica-northeast1", "northamerica-northeast2",
	"southamerica-east1", "us-central1", "us-east1", "us-east4", "us-south1",
	"us-west1", "us-west2", "us-west3", "us-west4",
}

// nearestSchedulerRegion is a fixed nearby Scheduler location for Cloud
// Run regions Scheduler doesn't offer. A Scheduler job can start a job in
// another region, since its URI names the job's location.
var nearestSchedulerRegion = map[string]string{
	"us-east5":             "us-east4",
	"us-west8":             "us-west4",
	"northamerica-south1":  "us-south1",
	"europe-north1":        "europe-central2",
	"europe-north2":        "europe-central2",
	"europe-southwest1":    "europe-west9",
	"australia-southeast2": "australia-southeast1",
}

// SchedulerRegion is where the daily check's Scheduler job lives for jobs in
// region: region itself when Scheduler offers it, else a fixed nearby one.
func SchedulerRegion(region string) (string, error) {
	if slices.Contains(SchedulerRegions, region) {
		return region, nil
	}
	if r, ok := nearestSchedulerRegion[region]; ok {
		return r, nil
	}
	return "", fmt.Errorf("region %s has no Cloud Scheduler location fugaro knows of; pass --scheduler-region with one of %s", region, strings.Join(SchedulerRegions, ", "))
}

// JobSADisplayName is a job service account's display name, its ownership
// mark (service accounts carry no labels). At most 11+63+1+20 = 95
// characters, within the 100 IAM allows.
func JobSADisplayName(slug, workflow string) string {
	return "Fugaro job " + slug + " " + workflow
}

// LegacyJobSADisplayName is the display name the M4 bootstrap gave job
// accounts. It is accepted as a mark, and kept on adoption, so an adopted
// account is never renamed.
func LegacyJobSADisplayName(slug, workflow string) string {
	return "Fugaro M4 job " + slug + " " + workflow
}

// BuildSADisplayName is a build service account's display name.
func BuildSADisplayName(slug string) string { return "Fugaro build " + slug }

// JobBucketPrefixes are the runs bucket areas a job account may use, and
// BuildBucketPrefixes the build account's (its build records).
var (
	JobBucketPrefixes   = []string{"runs", "cache", "locks"}
	BuildBucketPrefixes = []string{"builds"}
)

// BucketCondition is the IAM condition that limits an objectUser grant on
// bucket to the slug's objects under each prefix. The trailing slashes keep
// a slug from reaching another slug it is a string prefix of (design
// §6.1). A condition that differs in one byte is another binding, so this
// is its one definition.
func BucketCondition(bucket string, prefixes []string, slug string) string {
	conds := make([]string, len(prefixes))
	for i, p := range prefixes {
		conds[i] = fmt.Sprintf(`resource.name.startsWith("projects/_/buckets/%s/objects/%s/%s/")`, bucket, p, slug)
	}
	return strings.Join(conds, " || ")
}

// BucketConditionTitle is the title of the bucket condition of the account
// saID, as the bootstrap set it.
func BucketConditionTitle(saID string) string { return "fugaro-" + saID }

// LauncherBucketConditionTitle is the title of the launchers' objectUser
// grant on the runs bucket (docs/design/bucket-iam.md H5).
const LauncherBucketConditionTitle = "fugaro-launchers-runs"

// LauncherBucketCondition limits the launchers' objectUser grant on bucket
// to runs/, every repository's (H2). Every launcher carries this exact
// string and LauncherBucketConditionTitle, with no description, so IAM keeps
// them in one conditional binding. The installation module, the install
// guard and doctor all compare against it: this is its one definition.
func LauncherBucketCondition(bucket string) string {
	return fmt.Sprintf(`resource.name.startsWith("projects/_/buckets/%s/objects/runs/")`, bucket)
}
