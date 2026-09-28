// Package gcp implements the backend on Google Cloud: Cloud Run jobs,
// Cloud Logging, Secret Manager and Cloud Build (design §3.4, §8).
package gcp

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Name limits: Cloud Run job names are at most 49 characters (the
// execution's short name adds a suffix); service account IDs 6 to 30;
// Secret Manager IDs 255.
const (
	maxJobName  = 49
	maxSAID     = 30
	maxSecretID = 255
	maxImage    = 128 // one repository path component, well within registry limits
)

var unsafeNameRE = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitize(s string) string {
	s = unsafeNameRE.ReplaceAllString(strings.ToLower(s), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// derive is readable, truncated to fit limit, then "-" and 8 hex of
// sha256(slug NUL second). The readable part is lossy (sanitizing and
// joining with '-' merge distinct inputs); the hash, over the raw pair, is
// what keeps distinct (slug, second) pairs apart. It always ends in a hex
// digit, and starts with readable's first character.
func derive(readable, slug, second string, limit int) string {
	sum := sha256.Sum256([]byte(slug + "\x00" + second))
	suffix := "-" + hex.EncodeToString(sum[:4])
	if len(readable) > limit-len(suffix) {
		readable = readable[:limit-len(suffix)]
	}
	readable = strings.TrimRight(readable, "-")
	if readable == "" {
		return suffix[1:]
	}
	return readable + suffix
}

// slugHashRE is the hash suffix of task.Slug. The readable part of a name
// leaves it out; the name's own hash, over the full slug, keeps it unique.
var slugHashRE = regexp.MustCompile(`-[0-9a-f]{8}$`)

func readableSlug(slug string) string { return slugHashRE.ReplaceAllString(slug, "") }

func stem(slug, workflow string) string {
	return sanitize("fugaro-" + readableSlug(slug) + "-" + workflow)
}

// JobName is the Cloud Run job of (slug, workflow) (design §3.2):
// fugaro-<slug>-<workflow>, sanitized and truncated, plus a hash of the
// pair, so two repositories never share a job.
func JobName(slug, workflow string) string {
	return derive(stem(slug, workflow), slug, workflow, maxJobName)
}

// ServiceAccountID is the job's dedicated service account ID (design §6.1),
// built like JobName within 30 characters.
func ServiceAccountID(slug, workflow string) string {
	return derive(stem(slug, workflow), slug, workflow, maxSAID)
}

// SecretID is the Secret Manager secret holding a repository's logical
// secret (a workflow secret's name, or one of config.ReservedSecrets),
// built like JobName.
func SecretID(slug, logical string) string {
	return derive(sanitize("fugaro-"+readableSlug(slug)+"-"+logical), slug, logical, maxSecretID)
}

// ImageName is the derived image of (slug, workflow) in registry, untagged,
// built like JobName without the fugaro- prefix.
func ImageName(registry, slug, workflow string) string {
	return strings.TrimSuffix(registry, "/") + "/" + derive(sanitize(readableSlug(slug)+"-"+workflow), slug, workflow, maxImage)
}
