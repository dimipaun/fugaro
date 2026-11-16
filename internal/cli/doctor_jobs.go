package cli

import (
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// doctorRepoBoundJobs is doctor's line for each repository this machine's
// config still launches on a per-repository job, naming the shared project job
// that replaces it (M3, R3). The decision is the update nag's own
// (repoBoundJobs, updatewarn.go), so the two surfaces R3 names cannot print
// different replacements for one repository.
//
// The severity is "info" and the class is the actionable one
// (doctorActionableInfoIDs, doctor_render.go): a repository on a per-repository
// job is nothing wrong with the installation — the state is legal until the
// jobs are retired, and doctor must not fail on it with or without --strict
// ("nothing else changes", M3's acceptance). It is work all the same: one
// command moves the repository, and hiding the line would hide it, which is
// why its ID is in the actionable list beside gcp-project-line and
// issues-pending rather than left to the audit class's pointer line.
func doctorRepoBoundJobs(lc *localcfg.Config) []doctorCheck {
	jobs := repoBoundJobs(lc)
	out := make([]doctorCheck, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, doctorCheck{ID: "repo-bound-jobs", Severity: "info",
			Problem: repoBoundJobLine(j),
			Fix:     "run " + selfCommand() + " update: it writes the default profile for the repository's provider and deploys the shared job it names"})
	}
	return out
}
