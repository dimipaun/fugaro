package gcp

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/backendtest"
)

// TestGCPBackendPassesConformance runs the shared suite against this
// backend on the existing Cloud Run and Logging fakes: "a contributor's
// pull request is passes backendtest" (design m11-setup-and-skills.md §7).
func TestGCPBackendPassesConformance(t *testing.T) {
	job := JobName(backendtest.Slug, backendtest.Workflow)
	backendtest.Run(t, func(t *testing.T) backend.Backend {
		b, fr, _ := newTestBackend(t)
		fr.AddJob(job, "4", "8Gi")
		return b
	})
}
