package batch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// maxJobID is Batch's own limit on a job id: "at most 63 characters, must
// start with lowercase characters, only lowercase characters, numbers and -
// are accepted, and - cannot be the first or the last character" (design
// compute-backend.md §2.4, checked against the Batch REST reference).
const maxJobID = 63

// jobHashHex is how much of the run id's hash a job id carries: the same 12
// hex the rest of Fugaro's names use (gcp's jobHashHex), wide enough that two
// runs of one workflow never share an id.
const jobHashHex = 12

// The marker every Fugaro resource carries, reused verbatim from gcp's
// constants: the label set is one set, so the installation's arm of the log
// sink and the guard that refuses to adopt a foreign resource read the same
// values on every backend (design §2.4, C13).
const (
	labelManaged = gcp.LabelManaged
	managedValue = gcp.ManagedValue
)

// RunLabel is the per-run label (design §2.4, C13): the reaper's and a
// human's way to tell one run's VMs from another's without reading every
// job. It is the one label the existing Fugaro set does not have, because
// Cloud Run's job is reused by every run and Batch's is not.
const RunLabel = "fugaro_run"

// runLabelHashHex is how much of the run's hash the label keeps when the
// slug and the run id together are too long for 63 characters. A plain
// truncation could make two runs of a long slug equal, and the reaper's
// whole job is telling one run's VM from another's (design §2.4).
const runLabelHashHex = 8

// labelValueRE is what a GCP label value accepts: lowercase letters, digits,
// "_" and "-", at most 63 characters.
var labelValueRE = regexp.MustCompile(`^[a-z0-9_-]{1,63}$`)

// unsafeLabelRE matches every byte outside a label value's charset.
var unsafeLabelRE = regexp.MustCompile(`[^a-z0-9_-]`)

// JobID is the Batch job id of one run: fugaro-<slug>-<workflow>-<12 hex of
// the run id> (design §2.4), within Batch's 63 characters and charset.
//
// The design's "12 hex of the run id" is realised as 12 hex of a hash of the
// run id, domain-separated by the (slug, workflow) pair, which is the shape
// gcp's derive already gives every other Fugaro name. A run id carries only
// four hex characters of its own ("20260927-100000-abcd"), and the readable
// part is lossy -- it is sanitized and truncated to fit -- so hashing the
// whole triple is what keeps two different runs, and two long slugs that
// truncate alike, from landing on one job id. Batch creates a job per run
// and a job is single-use, so a collision would be a run refused with
// ALREADY_EXISTS.
func JobID(slug, workflow, runID string) string {
	readable := sanitizeName("fugaro-" + readableSlug(slug) + "-" + workflow)
	sum := sha256.Sum256([]byte(slug + "\x00" + workflow + "\x00" + runID))
	suffix := "-" + hex.EncodeToString(sum[:])[:jobHashHex]
	if len(readable) > maxJobID-len(suffix) {
		readable = readable[:maxJobID-len(suffix)]
	}
	readable = strings.TrimRight(readable, "-")
	if readable == "" {
		return suffix[1:]
	}
	return readable + suffix
}

// unsafeNameRE matches every byte outside the job id charset.
var unsafeNameRE = regexp.MustCompile(`[^a-z0-9-]+`)

// sanitizeName is gcp's rule for a name part (its unexported sanitize, which
// this restates because Batch's job id charset is the same and gcp keeps it
// to itself): lowercase, every run of bytes outside [a-z0-9-] becomes one
// "-", and no name starts or ends with one.
func sanitizeName(s string) string {
	s = unsafeNameRE.ReplaceAllString(strings.ToLower(s), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// slugHashRE is a repository slug's own 16-hex hash suffix, which the
// readable part of a name leaves out so it does not spend the name's budget
// (gcp.readableSlug's rule). The name's own hash still covers the full slug,
// so dropping a genuine tail never makes two names collide.
var slugHashRE = regexp.MustCompile(`-[0-9a-f]{16}$`)

func readableSlug(slug string) string { return slugHashRE.ReplaceAllString(slug, "") }

// Labels is the Fugaro label set of one run, which goes in BOTH Job.labels
// and AllocationPolicy.labels (design §2.4, C13). They are not
// interchangeable: Job.labels reach the job "and any Cloud Logging LogEntry
// that it generates", while only AllocationPolicy.labels reach the Compute
// Engine resources. A job marked in one field only leaves every VM of the
// run unlabelled, so the reaper -- and the check that looks for a leaked VM
// -- finds nothing precisely when a VM has leaked.
//
// The set is the one every other Fugaro resource carries (gcp's label
// constants, reused verbatim) plus the per-run label. workflow is the
// workflow variant, which is a label value as it stands: it is already
// constrained to [a-z0-9_-]{1,63} by the Terraform module that writes it
// (gcp.WorkflowVariant's doc comment). An error means a value cannot be a
// label at all, which is a name the installation must not adopt against.
func Labels(slug, workflow, runID string) (map[string]string, error) {
	repo, err := gcp.RepoLabel(slug)
	if err != nil {
		return nil, err
	}
	if !labelValueRE.MatchString(workflow) {
		return nil, fmt.Errorf("workflow %q is not a GCP label value ([a-z0-9_-], at most 63 characters)", workflow)
	}
	run, err := RunLabelValue(slug, runID)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		labelManaged:      managedValue,
		gcp.LabelRepo:     repo,
		gcp.LabelWorkflow: workflow,
		RunLabel:          run,
	}, nil
}

// RunLabelValue is the value of RunLabel for one run: "<slug>-<run id>",
// sanitized to the label charset, and truncated to 63 characters with a
// short hash of the pair when the two together are too long (design §2.4).
func RunLabelValue(slug, runID string) (string, error) {
	if slug == "" || runID == "" {
		return "", fmt.Errorf("a run label needs both a slug and a run id (got %q, %q)", slug, runID)
	}
	raw := slug + "-" + runID
	v := unsafeLabelRE.ReplaceAllString(strings.ToLower(raw), "_")
	if len(v) <= 63 {
		return v, nil
	}
	sum := sha256.Sum256([]byte(raw))
	suffix := "_" + hex.EncodeToString(sum[:])[:runLabelHashHex]
	return strings.TrimRight(v[:63-len(suffix)], "_-") + suffix, nil
}
