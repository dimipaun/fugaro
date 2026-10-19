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

	"github.com/dimipaun/fugaro/internal/config"
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

// projectStem is the readable part of a project job's name:
// fugaro-p-<profile>-<provider>-<account>, with the account segment left off
// when there is none (an api-key or Vertex profile runs as no Claude
// account). Sanitized like every other name, so a name outside Cloud Run's
// charset becomes a name it accepts rather than a refused plan.
//
// It is written, never read back: there is no ParseProjectJobName (R2-5).
// derive truncates the readable part, profile names hold hyphens, the account
// segment may be empty and "fugaro-p-" is also the legacy stem of an owner
// named "p" — so the name is a *name*, and the (profile, provider, account)
// set a reader needs comes from the policy, matched by computing these names
// (Task 3.3 step 1).
func projectStem(profile, provider, account string) string {
	s := "fugaro-p-" + profile + "-" + provider
	if account != "" {
		s += "-" + account
	}
	return sanitize(s)
}

// projectKey is derive's hash input for a project name. The three parts are
// joined with NUL, which no profile, provider or account name may hold, so
// two different triples can never hash the same: the readable part above is
// lossy on purpose (truncation, sanitizing, and profile/provider/account
// names that hold hyphens merge distinct triples).
func projectKey(profile, provider, account string) (slug, second string) {
	return profile + "\x00" + provider, account
}

// ProjectJobName is the shared job of (profile, provider, account) (design
// §4.1, plan task 3.2 step 1): fugaro-p-<profile>-<provider>-<account>, plus a
// hash of the triple, built with derive exactly as JobName is so the length
// limit and the collision behaviour are the same.
//
// The provider is part of the name and is never an env var: a launcher may
// override any variable through run.jobs.runWithOverrides, so the provider —
// which decides which repository the runner may clone and which git
// credential the job holds — must come from something only Cloud Run sets.
// The account is the Claude OAuth account the job's token belongs to.
func ProjectJobName(profile, provider, account string) string {
	slug, second := projectKey(profile, provider, account)
	return derive(projectStem(profile, provider, account), slug, second, maxJobName, jobHashHex)
}

// ProjectServiceAccountID is a project job's dedicated service account ID
// (design §4.1): one account per (profile, provider, account), because the
// secrets a job mounts are only readable by the runs using that job, built
// like ProjectJobName within 30 characters.
func ProjectServiceAccountID(profile, provider, account string) string {
	slug, second := projectKey(profile, provider, account)
	return derive(projectStem(profile, provider, account), slug, second, maxSAID, saHashHex)
}

// SecretID is the Secret Manager secret holding a repository's logical
// secret (a workflow secret's name, or one of config.ReservedSecrets),
// built like JobName.
func SecretID(slug, logical string) string {
	return derive(sanitize("fugaro-"+readableSlug(slug)+"-"+logical), slug, logical, maxSecretID, secretHashHex)
}

// ProjectSecretID is the Secret Manager secret holding a project-wide
// logical secret (design docs/design/project-secrets.md §4), built like
// SecretID but keyed by the Fugaro project name instead of a repository
// slug. The hash input is domain-separated with a literal "project\x00"
// prefix so a Fugaro project name that happened to equal some repository's
// own slug could never make this collide with SecretID's hash of the
// identical (name, logical) pair.
func ProjectSecretID(fugaroProject, logical string) string {
	return derive(sanitize("fugaro-project-"+fugaroProject+"-"+logical), "project\x00"+fugaroProject, logical, maxSecretID, secretHashHex)
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

// validWorkflowVariantToken reports whether token is a name WorkflowVariant
// accepts for the account half of a variant key: config.ClaudeAccountNameRE
// (the hyphen-adjacency structure) and config.MaxClaudeAccountNameLen (the
// length cap config.ClaudeAccountNameRE's own `{0,30}` quantifier cannot
// express — it counts repetition units, not characters, so the regex alone
// accepts up to 61 characters; see that var's doc comment in
// internal/config/claude_accounts.go) both hold. This package already
// imports internal/config (build.go, limits.go), so there is nothing to
// mirror here: calling config's symbols directly means this check and
// Task 1's own rule can never drift apart.
func validWorkflowVariantToken(token string) bool {
	return len(token) <= config.MaxClaudeAccountNameLen && config.ClaudeAccountNameRE.MatchString(token)
}

// WorkflowVariant is the key JobName, ServiceAccountID and SecretID derive
// a token-scoped workflow's resources from: workflow unchanged for the
// repository's default account ("" or "default", config.DefaultClaudeAccount),
// so an installation with no named accounts configured gets byte-identical
// names (design docs/design/named-oauth-tokens.md §5); otherwise
// workflow+"_"+token. It is a plain string key, not a secret: never
// log-redacted.
//
// This string is also the map key of the repository's tfvars
// `workflows` object (decision T5: Go-only, zero Terraform diff) — Terraform
// passes it straight through as `each.key` (modules/repo/workflows.tf) into
// modules/workflow's `name` variable, which validates `^[a-z0-9_-]{1,63}$`
// and uses it as the job's `fugaro_workflow` GCP label
// (modules/workflow/variables.tf, modules/workflow/job.tf): GCP label
// values may hold only lowercase letters, digits, '_' and '-', so a
// separator outside that charset (a literal "." was tried first and
// reverted — see git history) would make every named-account `terraform
// apply` fail validation before any resource is even planned.
// TestWorkflowVariantMatchesTerraformWorkflowLabel pins this by reading the
// module's own regex out of variables.tf, so a future change to either
// side is caught here, not at apply time.
//
// The join must also be exactly injective over (workflow, token): this
// string becomes the "second" argument JobName, ServiceAccountID and
// SecretID hash together with the slug (derive, above), so two different
// pairs that WorkflowVariant collapsed into one string would collapse onto
// the same Cloud Run job, the same dedicated service account and the same
// secret, defeating this feature's whole point (one secretAccessor grant
// per named account, design docs/design/named-oauth-tokens.md §4 option
// B). A plain "--" join is not injective here even though it looks like
// the rest of this file's names: workflow and token both already allow
// hyphens, including consecutive ones, so WorkflowVariant("ab--cd", "ef")
// and WorkflowVariant("ab", "cd--ef") would both read "ab--cd--ef". "_" is
// never a byte either charset allows (config.WorkflowNameRE,
// config.ClaudeAccountNameRE), so workflow+"_"+token has exactly one
// (workflow, token) reading: the standard "separator outside both
// alphabets" argument, not merely a hash that makes collisions improbable.
// The charset check panics rather than silently reproduce that ambiguity
// if some future caller ever passes a workflow or token outside the
// charset this function trusts (fail closed, the same posture
// LauncherBucketCondition takes below for the same reason: a name that
// looks plausible but secretly is not what it claims is worse than a
// crash).
func WorkflowVariant(workflow, token string) string {
	if token == "" || token == config.DefaultClaudeAccount {
		return workflow
	}
	if !config.WorkflowNameRE.MatchString(workflow) || !validWorkflowVariantToken(token) {
		panic(fmt.Sprintf("gcp.WorkflowVariant: workflow %q or token %q is outside its expected charset, so a \"_\" join would not be unambiguous", workflow, token))
	}
	return workflow + "_" + token
}

// jobSADisplayNameMaxLen is IAM's own display-name limit (checked
// 2026-09-28, like the rest of this file's name limits).
const jobSADisplayNameMaxLen = 100

// jobSADisplayNameHashHex is the width of the hash suffix JobSADisplayName
// falls back to once the natural concatenation would be too long: wide
// enough that two distinct (slug, workflow) pairs truncating to the same
// readable text essentially never also share a hash, narrow enough to
// leave most of the 100-character budget for the readable part.
const jobSADisplayNameHashHex = 8

// JobSADisplayName is a job service account's display name, its ownership
// mark (service accounts carry no labels).
//
// For today's one real caller (internal/infra/spec.go), which always
// passes the plain fugaro.yaml workflow name (config.WorkflowNameRE, <= 20
// characters), this is always exactly "Fugaro job "+slug+" "+workflow,
// unchanged: at most 11+63+1+20 = 95 characters, within IAM's 100-character
// limit, so the fallback below never triggers for that caller (confirmed
// by internal/infra's existing golden tfvars tests, byte-identical before
// and after this function changed).
//
// Once a caller starts deriving workflow from gcp.WorkflowVariant (up to
// config.WorkflowNameRE's 20 + "_" + config.MaxClaudeAccountNameLen's 31 =
// 52 characters), the natural concatenation can reach 11+63+1+52 = 127,
// over the limit — with a 63-character slug alone, truncation already
// starts once the variant passes roughly 25 characters. When that happens,
// this function truncates the readable "slug workflow" part and appends a
// short, stable hash of the full (slug, workflow) pair, the same
// derive-style pattern JobName/ServiceAccountID/SecretID already use, so
// the result is always <= 100 characters and deterministic. A display name
// is its ownership mark: internal/infra/discover.go compares it EXACTLY and
// reports any other value as a ForeignError, so the truncated form below
// must stay stable once a named-account service account exists (changing
// the hash width or its input would make discovery treat existing named-
// account service accounts as foreign). TestJobSADisplayNameMaxLengthIsPinned
// pins the exact maximum-length value. Distinct variants still get distinct
// display names in practice through the hash, but uniqueness here is a
// cosmetic guard: each service account's email and ID are its real identity.
func JobSADisplayName(slug, workflow string) string {
	const prefix = "Fugaro job "
	name := prefix + slug + " " + workflow
	if len(name) <= jobSADisplayNameMaxLen {
		return name
	}
	sum := sha256.Sum256([]byte(slug + "\x00" + workflow))
	suffix := " " + hex.EncodeToString(sum[:])[:jobSADisplayNameHashHex]
	readable := slug + " " + workflow
	if budget := jobSADisplayNameMaxLen - len(prefix) - len(suffix); len(readable) > budget {
		readable = readable[:budget]
	}
	readable = strings.TrimRight(readable, " ")
	return prefix + readable + suffix
}

// LegacyJobSADisplayName is the display name the M4 bootstrap gave job
// accounts. It is accepted as a mark, and kept on adoption, so an adopted
// account is never renamed.
func LegacyJobSADisplayName(slug, workflow string) string {
	return "Fugaro M4 job " + slug + " " + workflow
}

// ProjectSADisplayName is a project job service account's display name, its
// ownership mark (service accounts carry no labels), in the shape
// JobSADisplayName uses for a per-repository job.
//
// It needs the same fallback for the same reason, and more often: the natural
// "Fugaro project job <profile> <provider> <account>" reaches 19+40+1+9+1+31 =
// 101 characters — one over IAM's limit, with a project name at
// config.ProjectNameRE's 40, the longest provider (bitbucket) and a Claude
// account at config.MaxClaudeAccountNameLen — so a valid local config would
// plan cleanly and fail the apply. Over the limit it truncates the readable
// part and appends a short hash of the triple, like JobSADisplayName. Nothing
// compares this name (internal/infra/discover.go compares job accounts'
// display names, not project jobs'), so the hash is the same cosmetic guard:
// an account's ID and email are its real identity.
func ProjectSADisplayName(profile, provider, account string) string {
	const prefix = "Fugaro project job "
	readable := profile + " " + provider
	if account != "" {
		readable += " " + account
	}
	if name := prefix + readable; len(name) <= jobSADisplayNameMaxLen {
		return name
	}
	sum := sha256.Sum256([]byte(profile + "\x00" + provider + "\x00" + account))
	suffix := " " + hex.EncodeToString(sum[:])[:jobSADisplayNameHashHex]
	if budget := jobSADisplayNameMaxLen - len(prefix) - len(suffix); len(readable) > budget {
		readable = readable[:budget]
	}
	return prefix + strings.TrimRight(readable, " ") + suffix
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
//
// slug is a repository's own and must not be empty: this is the
// *per-repository* condition, and an empty slug has nothing to scope the
// grant to — it panics rather than return the widened, project-wide clause
// (ProjectBucketCondition, which a project job's grant uses and which says so
// in its name).
func BucketCondition(bucket string, prefixes []string, slug string) string {
	conds := make([]string, len(prefixes))
	for i, p := range prefixes {
		conds[i] = bucketPrefixClause(bucket, p, slug)
	}
	return strings.Join(conds, " || ")
}

// ProjectBucketCondition is the IAM condition that limits a *project job's*
// objectUser grant on bucket to each prefix, with no slug (plan task 3.2
// step 2). A project job serves every repository admitted to its profile, so
// the object it may write is no longer inside one repository's prefix:
//
//	resource.name.startsWith("projects/_/buckets/<b>/objects/runs/") ||
//	resource.name.startsWith("projects/_/buckets/<b>/objects/cache/") ||
//	resource.name.startsWith("projects/_/buckets/<b>/objects/locks/")
//
// This is a *widening*, and a documented regression of a 0.7.0 property:
// SECURITY.md and docs/design/bucket-iam.md state it (D4, D13). It is a
// separate builder from BucketCondition rather than that one called with an
// empty slug, so a reader of a per-repository caller cannot mistake this
// condition for it and so the per-repository path keeps its per-slug
// condition, byte for byte, until Task 4.4 drops it.
func ProjectBucketCondition(bucket string, prefixes []string) string {
	conds := make([]string, len(prefixes))
	for i, p := range prefixes {
		conds[i] = projectBucketPrefixClause(bucket, p)
	}
	return strings.Join(conds, " || ")
}

// projectBucketPrefixClause is one clause of the widened condition: the
// bucket's objects directly under prefix, with no slug segment. It is a
// builder of its own rather than bucketPrefixClause with an empty slug, so
// that an empty slug reaching the per-repository builder is refused (below)
// instead of quietly producing this clause — which is what it used to do, and
// which made BucketCondition(b, prefixes, "") byte for byte the widened
// condition of a project job rather than a condition that matches nothing.
func projectBucketPrefixClause(bucket, prefix string) string {
	return fmt.Sprintf(`resource.name.startsWith("projects/_/buckets/%s/objects/%s/")`, bucket, prefix)
}

// bucketPrefixClause is one clause of a per-repository bucket condition: the
// bucket's objects under prefix, inside slug's own prefix. The trailing
// slashes keep a slug from reaching another slug it is a string prefix of
// (design §6.1).
//
// slug must be a repository's own (task.Slug's output): an empty one is a
// caller that has no slug to scope the grant to, and the clause it would
// build is the *project job's* widened one — every repository's objects under
// prefix, which is the opposite of what a per-repository caller asks for.
// This panics rather than widen, as LauncherBucketCondition panics rather
// than return a condition that is not the one every comparison expects byte
// for byte: a grant that is wider than the one asked for is the failure this
// whole function exists to avoid, and it is silent. A caller that wants the
// widened condition calls ProjectBucketCondition, which says so in its name.
func bucketPrefixClause(bucket, prefix, slug string) string {
	if slug == "" {
		panic(fmt.Sprintf("gcp.BucketCondition: empty slug: a per-repository condition with no slug would be the project job's widened one (ProjectBucketCondition builds that), granting every repository's objects under %q", prefix))
	}
	return fmt.Sprintf(`resource.name.startsWith("projects/_/buckets/%s/objects/%s/%s/")`, bucket, prefix, slug)
}

// BucketConditionTitlePrefix is the fixed part of BucketConditionTitle: a
// caller that must recognise the title rather than build it (internal/infra/tf
// reads a plan's condition titles) takes it from here, so the two spellings
// cannot drift apart.
const BucketConditionTitlePrefix = "fugaro-"

// BucketConditionTitle is the title of the bucket condition of the account
// saID, as the bootstrap set it.
func BucketConditionTitle(saID string) string { return BucketConditionTitlePrefix + saID }

// TokenBucketPrefix is the runs bucket prefix the budget token objects live
// under (internal/budget/token's ObjectPrefix, pinned equal by a test): out of
// runs/, so that a job account, which may write runs/, may only read it
// (docs/design/budget-token-channel.md §7.2, D16). It is not one of
// JobBucketPrefixes, and must never be: those are objectUser.
const TokenBucketPrefix = "tokens"

// TokenBucketCondition is the condition of a job account's read-only
// (objectViewer) grant on the runs bucket: the repository's own token
// objects, tokens/<slug>/, and nothing else. The per-run object cannot be
// named by a condition without a binding per run (the design's rejected C3),
// so the repository is the narrowest scope; what keeps one run from using
// another's token is the seal (§7.1). Same shape and trailing slash as
// BucketCondition, so a slug cannot reach another it is a prefix of.
func TokenBucketCondition(bucket, slug string) string {
	return BucketCondition(bucket, []string{TokenBucketPrefix}, slug)
}

// TokenBucketConditionTitlePrefix is the fixed part of
// TokenBucketConditionTitle, for a caller that must recognise the title rather
// than build it (internal/infra/tf, as for the project job's): the expression
// a grant carries does not say whose grant it is, the title does.
//
// It is not a prefix of ProjectTokenBucketConditionTitlePrefix ("fugaro-project-tokens-"),
// and the two must stay apart: a project job's whole-tokens/ binding must
// never read as the per-repository job's own.
const TokenBucketConditionTitlePrefix = "fugaro-tokens-"

// TokenBucketConditionTitle is the title of that grant's condition for the
// account saID: distinct from BucketConditionTitle, so the two bindings are
// told apart by title as well as by role.
func TokenBucketConditionTitle(saID string) string { return TokenBucketConditionTitlePrefix + saID }

// ProjectTokenBucketCondition is the condition of a *project job's* read-only
// (objectViewer) grant on the runs bucket: the whole of tokens/, with no slug,
// the same widening ProjectBucketCondition makes on runs/, cache/ and locks/,
// and for the same reason — a project job serves every repository admitted to
// its profile, so the token object it must read is no longer inside one
// repository's prefix (plan task 3.2 step 2; budget-token-channel.md §7.2,
// where the sentence about Task 3.2's shared job names this grant).
//
//	resource.name.startsWith("projects/_/buckets/<b>/objects/tokens/")
//
// Read-only is what keeps it from being a widening of the same kind as the
// objectUser one: objectViewer carries no create, no delete and no update, so
// a project job can read a queued run's sealed token and can neither replace
// nor remove it. What it reads is ciphertext, because the object is sealed
// under a key only the execution the launch minted it for receives (§7.1), so
// the reach is a capability over bytes and not over tokens. It is a separate
// builder from TokenBucketCondition, which names one slug: the two must not be
// confused, and neither may be built from the other by passing an empty slug.
func ProjectTokenBucketCondition(bucket string) string {
	return projectBucketPrefixClause(bucket, TokenBucketPrefix)
}

// ProjectTokenBucketConditionTitlePrefix is the fixed part of
// ProjectTokenBucketConditionTitle, for a caller that must recognise the title
// rather than build it (internal/infra/tf).
const ProjectTokenBucketConditionTitlePrefix = "fugaro-project-tokens-"

// ProjectTokenBucketConditionTitle is the title of that grant's condition for
// the project job account saID, as ProjectBucketCondition's is for its own
// grant: distinct from TokenBucketConditionTitle, so a project job's tokens/
// binding is told apart from a per-repository job's by title as well as by
// expression.
func ProjectTokenBucketConditionTitle(saID string) string {
	return ProjectTokenBucketConditionTitlePrefix + saID
}

// LauncherBucketConditionTitle is the title of the launchers' objectUser
// grant on the runs bucket (docs/design/bucket-iam.md H5). It names both
// prefixes since the budget token moved to tokens/ (budget-token-channel.md
// §7.2, D16): a new title, because the expression widened, so an
// installation still carrying the runs/-only binding reads as a different
// one rather than as this one.
const LauncherBucketConditionTitle = "fugaro-launchers-runs-tokens"

// LegacyLauncherBucketConditionTitle and LegacyLauncherBucketCondition are
// the launchers' objectUser grant as every release before 0.9.0 wrote it:
// the runs/-only expression under the old title (H5, before the widening
// above). No release writes them any more — the installation module's
// precondition refuses them, so an apply replaces the binding — but a
// reader that must judge an *installed* policy meets them for as long as an
// installation has not been re-initialised, and must read them as this
// grant's old form rather than as a stranger's condition
// (internal/infra's RunsBucketFindings; a removal suggested for it would
// take away the runs/ write a launcher is supposed to hold). They are
// defined here, beside their replacement, so the two spellings cannot drift
// apart.
const LegacyLauncherBucketConditionTitle = "fugaro-launchers-runs"

// LegacyLauncherBucketCondition is the expression LauncherBucketCondition
// built before the budget token moved to tokens/, and the title above is its
// title. It embeds bucket unescaped exactly as LauncherBucketCondition does,
// and panics for the same reason on a name outside [a-z0-9._-].
func LegacyLauncherBucketCondition(bucket string) string {
	if !bucketNameRE.MatchString(bucket) {
		panic(fmt.Sprintf("gcp.LegacyLauncherBucketCondition: bucket name %q has a byte outside [a-z0-9._-]", bucket))
	}
	return fmt.Sprintf(`resource.name.startsWith("projects/_/buckets/%s/objects/runs/")`, bucket)
}

// bucketNameRE is the charset LauncherBucketCondition accepts: narrower than
// GCS's own bucket name rules, but every bucket Fugaro creates or accepts
// already fits it (tfvars.go's "fugaro-runs-" prefix check does not enforce
// the rest, so this is the one place that does).
var bucketNameRE = regexp.MustCompile(`^[a-z0-9._-]+$`)

// ValidBucketName reports whether bucket is the charset
// LauncherBucketCondition accepts. A caller that did not itself validate a
// configured bucket name (a local config's bucket_url, for instance, which
// localcfg only charset-checks when runs_bucket, not bucket_url, is set)
// should check this before calling LauncherBucketCondition with it, since
// that function panics rather than widen on a bad one.
func ValidBucketName(bucket string) bool { return bucketNameRE.MatchString(bucket) }

// LauncherBucketCondition limits the launchers' objectUser grant on bucket
// to runs/ and tokens/, every repository's (H2): runs/ for the task, the
// claim, the launch record and the cancel marker, tokens/ for the sealed
// budget token the launcher writes, replaces on a retry and deletes after a
// launch that did not start (budget-token-channel.md §7.2: the deliberate
// widening D16 names, still one condition on two prefixes and not a role on
// the bucket). Every launcher carries this exact
// string and LauncherBucketConditionTitle, with no description, so IAM keeps
// them in one conditional binding. The installation module, the install
// guard and doctor all compare against it: this is its one definition.
//
// bucket is embedded in the CEL expression unescaped, so it must already be
// a plain bucket name: a byte outside [a-z0-9._-] (a quote, in particular)
// could otherwise break out of the string literal and widen the condition.
// LauncherBucketCondition panics rather than return a condition that looks
// plausible but is not the one every comparison expects byte for byte.
func LauncherBucketCondition(bucket string) string {
	if !bucketNameRE.MatchString(bucket) {
		panic(fmt.Sprintf("gcp.LauncherBucketCondition: bucket name %q has a byte outside [a-z0-9._-]", bucket))
	}
	return fmt.Sprintf(`resource.name.startsWith("projects/_/buckets/%s/objects/runs/") || resource.name.startsWith("projects/_/buckets/%s/objects/%s/")`, bucket, bucket, TokenBucketPrefix)
}

// LauncherBucketConditionRunsTitle and LauncherBucketConditionRuns are that
// same grant as every release before the seal wrote it: title
// "fugaro-launchers-runs" and runs/ alone (docs/design/bucket-iam.md H5, as
// the 0.7.0 hardening had it). Nothing deploys this pair any more — the title
// changed with the expression on purpose, so an installation still carrying
// the old binding reads as a different one rather than as the current one
// (budget-token-channel.md §7.2, D16) — but an installation not re-applied
// since the seal still holds it, and doctor must tell it apart from a
// conditional write nobody planned: it is the launchers' own, narrower than
// the current grant and never wider, so its fix is `fugaro init` and never a
// removal (hardening.go's RunsBucketFindings).
const LauncherBucketConditionRunsTitle = "fugaro-launchers-runs"

// LauncherBucketConditionRuns is that binding's expression: the same CEL
// clause LauncherBucketCondition opens with, for runs/ alone. It panics on a
// bucket outside the same charset, for the same reason: a comparison against
// it is byte for byte.
func LauncherBucketConditionRuns(bucket string) string {
	if !bucketNameRE.MatchString(bucket) {
		panic(fmt.Sprintf("gcp.LauncherBucketConditionRuns: bucket name %q has a byte outside [a-z0-9._-]", bucket))
	}
	return fmt.Sprintf(`resource.name.startsWith("projects/_/buckets/%s/objects/runs/")`, bucket)
}

// BatchJobPrefix is the prefix every Fugaro Batch job's name starts with
// (infra.JobPrefix reads it): the launchers' Batch Job Editor grant is
// narrowed to it, and the reaper's sweep is defined over it.
const BatchJobPrefix = "fugaro-"

// LauncherJobsConditionTitle and LauncherJobsConditionDescription are the
// title and description of the launchers' roles/batch.jobsEditor grant, as
// the compute module (modules/compute/iam.tf) sets them.
const (
	LauncherJobsConditionTitle       = "fugaro-jobs-only"
	LauncherJobsConditionDescription = "Batch jobs whose name starts with the Fugaro job prefix, in this region."
)

var (
	jobsProjectRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	jobsRegionRE  = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+$`)
)

// LauncherJobsCondition narrows the launchers' roles/batch.jobsEditor grant
// to Fugaro-shaped job names in project and region (design compute-backend
// §3.7, Check 57a step 9). The compute module renders it, and the compute
// stage's grant check (tf.Cover.GrantsNotCovered over the compute root's plan,
// with that root's project and region) compares against it byte for byte:
// this is its one definition. project and region are embedded in a CEL string literal
// unescaped, so it panics on one outside their charsets rather than return
// a condition that could break out of the literal.
func LauncherJobsCondition(project, region string) string {
	if !jobsProjectRE.MatchString(project) || !jobsRegionRE.MatchString(region) {
		panic(fmt.Sprintf("gcp.LauncherJobsCondition: project %q or region %q is outside its charset", project, region))
	}
	return fmt.Sprintf(`resource.name.startsWith("projects/%s/locations/%s/jobs/%s")`, project, region, BatchJobPrefix)
}

// ValidJobsConditionScope reports whether LauncherJobsCondition accepts
// project and region.
func ValidJobsConditionScope(project, region string) bool {
	return jobsProjectRE.MatchString(project) && jobsRegionRE.MatchString(region)
}
