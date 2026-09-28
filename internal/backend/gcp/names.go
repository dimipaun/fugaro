// Package gcp implements the backend on Google Cloud: Cloud Run jobs,
// Cloud Logging, Secret Manager and Cloud Build (design §3.4, §8).
package gcp

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
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
//
// Each name carries the widest hash its limit allows while keeping a
// readable prefix: 12 hex for jobs, 8 for service accounts (only 30
// characters), 16 for secrets and images. A hash stops accidental
// collisions; the control against a chosen one is the ownership check the
// bootstrap (and M5's onboarding) makes before reusing a job, service
// account or secret.
const (
	maxJobName  = 49
	maxSAID     = 30
	maxSecretID = 255
	maxImage    = 128

	jobHashHex    = 12
	saHashHex     = 8
	secretHashHex = 16
	imageHashHex  = 16
)

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
