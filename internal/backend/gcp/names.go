// Package gcp implements the backend on Google Cloud: Cloud Run jobs,
// Cloud Logging, Secret Manager and Cloud Build (design §3.4, §8).
package gcp

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Name limits: Cloud Run job names are at most 63 characters; service
// account IDs 6 to 30.
const (
	maxJobName = 63
	maxSAID    = 30
)

var unsafeNameRE = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitize(s string) string {
	s = unsafeNameRE.ReplaceAllString(strings.ToLower(s), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// fit returns name, or when it is longer than limit, its first characters
// and a 6-hex hash of the full name, so distinct long names stay distinct.
func fit(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return strings.TrimRight(name[:limit-7], "-") + "-" + hex.EncodeToString(sum[:3])
}

func stem(slug, workflow string) string { return sanitize("fugaro-" + slug + "-" + workflow) }

// JobName is the Cloud Run job of (slug, workflow) (design §3.2).
func JobName(slug, workflow string) string { return fit(stem(slug, workflow), maxJobName) }

// ServiceAccountID is the job's dedicated service account ID (design §6.1).
func ServiceAccountID(slug, workflow string) string { return fit(stem(slug, workflow), maxSAID) }

// SecretID is the Secret Manager secret holding a repository's logical
// secret (a workflow secret's name, or one of config.ReservedSecrets).
func SecretID(slug, logical string) string { return sanitize("fugaro-" + slug + "-" + logical) }

// ImageName is the derived image of (slug, workflow) in registry, untagged.
func ImageName(registry, slug, workflow string) string {
	return strings.TrimSuffix(registry, "/") + "/" + sanitize(slug+"-"+workflow)
}
