package imagecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// The decisions of the daily check.
const (
	Rebuild = "rebuild" // a trigger fired: a build is submitted
	Skip    = "skip"    // nothing changed that a build would pick up
	// RebuildFailedLast: a trigger fired, but the last build of these very
	// inputs failed, or ended without clearing the trigger, so it isn't
	// paid for again until something changes.
	RebuildFailedLast = "rebuild-failed-last"
	// CheckFailed: the check itself could not decide.
	CheckFailed = "check-failed"
	// NotInstalled: head's fugaro.yaml and the installed check job
	// disagree about the workflow (fugaro update fixes it).
	NotInstalled = "not-installed"
)

// The reasons a decision records. The triggers come first, in the order
// they are evaluated; the rest are notes that never rebuild alone.
const (
	ReasonForce           = "force"
	ReasonNoRecord        = "no-record"
	ReasonImageConfig     = "image-config"
	ReasonLockfiles       = "lockfiles"
	ReasonBase            = "base"
	ReasonPaths           = "paths"
	ReasonMaxAge          = "max-age"
	ReasonBuiltCommitGone = "built-commit-gone"
	ReasonLatestDrift     = "latest-drift"

	// ReasonBaseUnknown: the base image's digest couldn't be read, so the
	// base trigger was skipped.
	ReasonBaseUnknown = "base-unknown"
	// ReasonLatestUnknown: the image's latest digest couldn't be read, so
	// the latest-drift trigger was skipped.
	ReasonLatestUnknown = "latest-unknown"
	// ReasonBuildRunning: the last build of these inputs hasn't finished.
	ReasonBuildRunning = "build-running"
	// ReasonLastBuildIneffective: the last build of these inputs ended
	// without failing, but didn't clear the trigger.
	ReasonLastBuildIneffective = "last-build-ineffective"
)

// FailedBuild reports whether a Cloud Build status is a failure the check
// backs off from: the build ran and did not succeed. A cancelled or
// expired build is not one: somebody stopped it, it didn't fail.
func FailedBuild(status string) bool {
	return status == "FAILURE" || status == "TIMEOUT" || status == "INTERNAL_ERROR"
}

// RunningBuild reports whether a Cloud Build status is not final yet.
func RunningBuild(status string) bool {
	return status == "PENDING" || status == "QUEUED" || status == "WORKING"
}

// FinalBuild reports whether a Cloud Build status can no longer change.
func FinalBuild(status string) bool {
	return FailedBuild(status) || status == "SUCCESS" || status == "CANCELLED" || status == "EXPIRED"
}

// InputsFingerprint is what a build would be built from: the back-off
// compares it with the one the last failed build ran with.
type InputsFingerprint struct {
	SourceCommit    string            `json:"source_commit"`
	ImageConfigHash string            `json:"image_config_hash"`
	KeyFiles        map[string]string `json:"key_files"`
	BaseDigest      string            `json:"base_digest"`
}

// Equal reports whether f and o are the same inputs.
func (f InputsFingerprint) Equal(o InputsFingerprint) bool {
	return f.SourceCommit == o.SourceCommit && f.ImageConfigHash == o.ImageConfigHash &&
		f.BaseDigest == o.BaseDigest && maps.Equal(f.KeyFiles, o.KeyFiles)
}

// Inputs are what one workflow's decision is made from.
type Inputs struct {
	// Config is head's fugaro.yaml, and Workflow the workflow decided.
	Config   *config.Config
	Workflow string
	// Record is the workflow's image.json; nil when there is none.
	Record *Record
	// LastCheck is the previous check.json; nil when there is none.
	LastCheck *CheckState
	// LastBuildStatus is the Cloud Build status of LastCheck's build.
	LastBuildStatus string
	// Head is the base branch's head commit, and Tree its tree.
	Head string
	Tree Tree
	// BuiltCommitReachable: the record's commit is an ancestor of Head.
	BuiltCommitReachable bool
	// Changed reports whether a file matching one of globs changed
	// between the commits from and to, and which.
	Changed func(from, to string, globs []string) (bool, []string, error)
	// BaseRef is the base image the build would be given, and BaseDigest
	// its current digest ("" when it couldn't be read).
	BaseRef, BaseDigest string
	// LatestDigest is the digest the image's latest tag points at (""
	// when unknown); LatestMissing means the tag doesn't exist.
	LatestDigest  string
	LatestMissing bool
	// TemplateSalt is gcp.TemplateSalt of the build a rebuild would run;
	// "" leaves the salt out of the image-config trigger.
	TemplateSalt string
	Now          time.Time
	// Rebuild is the workflow's rebuild: block, with defaults filled in.
	Rebuild config.Rebuild
	Force   bool
}

// Decision is one workflow's decision.
type Decision struct {
	Workflow string   `json:"workflow"`
	Decision string   `json:"decision"`
	Reasons  []string `json:"reasons"`
	// Error is why the check failed (CheckFailed only).
	Error string `json:"error,omitempty"`
	// Inputs are what a build submitted now would be built from.
	Inputs InputsFingerprint `json:"inputs"`
}

// Decide evaluates the workflow's rebuild triggers, in order, recording
// every one that fires, and then the back-off, unless forced: a rebuild
// whose inputs are those the last build ran with is RebuildFailedLast when
// that build ended (failed, or finished without clearing the trigger), and
// Skip while it is still running. It makes no calls: the caller reads everything first.
func Decide(_ context.Context, in Inputs) Decision {
	d := Decision{Workflow: in.Workflow, Decision: Skip}
	failed := func(err error) Decision {
		d.Decision, d.Reasons, d.Error = CheckFailed, nil, err.Error()
		return d
	}
	if in.Config == nil || in.Tree == nil {
		return failed(errors.New("the check has no fugaro.yaml or tree to evaluate"))
	}
	keys, err := KeyFiles(in.Config, in.Workflow, in.Tree)
	if err != nil {
		return failed(err)
	}
	hash, err := ImageConfigHash(in.Config, in.Workflow, in.Tree)
	if err != nil {
		return failed(err)
	}
	d.Inputs = InputsFingerprint{SourceCommit: in.Head, ImageConfigHash: hash, KeyFiles: keys, BaseDigest: in.BaseDigest}

	fired, notes, err := triggers(in, d.Inputs)
	if err != nil {
		return failed(err)
	}
	d.Reasons = append(fired, notes...)
	if len(fired) == 0 {
		return d
	}
	d.Decision = Rebuild
	if in.Force || in.LastCheck == nil || in.LastCheck.BuildID == "" || in.LastCheck.BuildInputs == nil {
		return d
	}
	last := *in.LastCheck.BuildInputs
	cur := d.Inputs
	// A base digest that couldn't be read tonight is no change: otherwise
	// a registry hiccup would resubmit a build known to fail.
	if cur.BaseDigest == "" {
		cur.BaseDigest = last.BaseDigest
	}
	if !last.Equal(cur) {
		return d
	}
	// The last build ran on these very inputs.
	switch {
	case RunningBuild(in.LastBuildStatus):
		d.Decision = Skip
		d.Reasons = append(d.Reasons, ReasonBuildRunning)
	case FailedBuild(in.LastBuildStatus):
		d.Decision = RebuildFailedLast
	case FinalBuild(in.LastBuildStatus):
		// It ended without failing, yet a trigger still fires: it didn't
		// clear it (a gate that kept a newer record after a force-push to
		// an older commit, say). Another build of the same inputs would do
		// the same, every night: a human is needed.
		d.Decision = RebuildFailedLast
		d.Reasons = append(d.Reasons, ReasonLastBuildIneffective)
	}
	return d
}

// ReasonCatalogDefinition is the catalog trigger: the definition a catalog
// image was built from is not the one in the runs bucket now. A catalog
// entry belongs to no repository — there are no commits of its own to watch
// — so the copied definition's sha256 is what says the image is stale, and
// this trigger, with the max-age one, is what lets an entry be rebuilt when
// nothing else about it moved (F4).
const ReasonCatalogDefinition = "catalog-definition"

// CatalogRecordState is one catalog entry's build record, as
// internal/backend/gcp's record step writes it to
// CatalogRecordKey(slug, entry) and this check reads it: which copy the
// build read (the sha256 of the bytes it built, the object's generation and
// the commit PIC3's copy took it from), the base it was built FROM and the
// image it produced. Its field names are the record's JSON keys, and the
// ones whose meaning matches a workflow Record's are spelled the same.
type CatalogRecordState struct {
	Version              int       `json:"version"`
	Entry                string    `json:"entry"`
	SourceCommit         string    `json:"source_commit"`
	DefinitionGeneration int64     `json:"definition_generation"`
	DefinitionSHA256     string    `json:"definition_sha256"`
	BaseRef              string    `json:"base_ref"`
	BaseDigest           string    `json:"base_digest"`
	Image                string    `json:"image"`
	ContentTag           string    `json:"content_tag"`
	ImageDigest          string    `json:"image_digest"`
	BuildID              string    `json:"build_id"`
	BuiltAt              time.Time `json:"built_at"`
}

// CatalogInputs are what one entry's decision is made from.
type CatalogInputs struct {
	// Entry is the entry's name and Record its last build, nil when there
	// is none.
	Entry  string
	Record *CatalogRecordState
	// DefinitionSHA256 is the sha256 of the definition in the runs bucket
	// now: config.CatalogDockerfileKey's object, as the caller read it.
	DefinitionSHA256 string
	// BaseRef is the base the entry would be built FROM and BaseDigest its
	// current digest ("" when it couldn't be read).
	BaseRef, BaseDigest string
	// MaxAge is the entry's rebuild.max_age; 0 is off.
	MaxAge time.Duration
	Now    time.Time
	Force  bool
}

// CatalogDecision is one entry's decision: Decision's shape under the
// entry's name, since an entry has no workflow.
type CatalogDecision struct {
	Entry    string   `json:"entry"`
	Decision string   `json:"decision"`
	Reasons  []string `json:"reasons"`
}

// DecideCatalog evaluates one catalog entry's triggers, in the order the
// workflow check evaluates them: force, the definition the last build read,
// the base, then the image's age. Every trigger that fires is recorded, and
// the reasons keep that order, so a night when both the definition and the
// base moved records catalog-definition before base. It makes no calls: the
// caller reads everything first.
//
// An entry is not backed off: a back-off compares the inputs of a build
// that just failed, and an entry has no per-entry check state to hold them.
// Its definition and max-age triggers are what a nightly rebuild runs on,
// and a definition that does not change is not rebuilt twice.
func DecideCatalog(in CatalogInputs) CatalogDecision {
	d := CatalogDecision{Entry: in.Entry, Decision: Skip}
	var fired, notes []string
	if in.Force {
		fired = append(fired, ReasonForce)
	}
	rec := in.Record
	if rec == nil {
		// Nothing to compare with and nothing to age: the image has to be
		// built (or, on a forced run, rebuilt) before there is anything to
		// decide about.
		d.Decision = Rebuild
		d.Reasons = append(fired, ReasonNoRecord)
		return d
	}
	if in.DefinitionSHA256 != rec.DefinitionSHA256 {
		fired = append(fired, ReasonCatalogDefinition)
	}
	switch {
	case in.BaseRef != rec.BaseRef:
		fired = append(fired, ReasonBase)
	case in.BaseDigest == "":
		notes = append(notes, ReasonBaseUnknown)
	case in.BaseDigest != rec.BaseDigest:
		fired = append(fired, ReasonBase)
	}
	if age := in.MaxAge; age > 0 && in.Now.Sub(rec.BuiltAt) > age {
		fired = append(fired, ReasonMaxAge)
	}
	d.Reasons = append(fired, notes...)
	if len(fired) > 0 {
		d.Decision = Rebuild
	}
	return d
}

// CheckVersion is the CheckState schema version this package writes.
const CheckVersion = 1

// CheckState is the last check of one workflow, as the check job writes
// it to builds/<slug>/<workflow>/check.json.
type CheckState struct {
	Version   int       `json:"version"`
	CheckedAt time.Time `json:"checked_at"`
	Decision  string    `json:"decision"`
	Reasons   []string  `json:"reasons"`
	Error     string    `json:"error,omitempty"`
	// BuildID is the last build a check submitted, and BuildInputs what
	// it was built from; both carry over until the next submission.
	BuildID     string             `json:"build_id,omitempty"`
	BuildInputs *InputsFingerprint `json:"build_inputs,omitempty"`
	// LastBuildStatus is BuildID's status as this check read it (the
	// status at submission when this check submitted it), and LastBuildAt
	// when it was submitted.
	LastBuildStatus string     `json:"last_build_status,omitempty"`
	LastBuildAt     *time.Time `json:"last_build_at,omitempty"`
}

// CheckKey is the workflow's check.json in the runs bucket.
func CheckKey(slug, workflow string) string {
	return "builds/" + slug + "/" + workflow + "/check.json"
}

// Marshal encodes s as check.json.
func (s *CheckState) Marshal() ([]byte, error) { return json.MarshalIndent(s, "", "  ") }

// ParseCheckState decodes a check.json. Unknown fields are ignored.
func ParseCheckState(data []byte) (*CheckState, error) {
	var s CheckState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("the image check state: %w", err)
	}
	if s.Version < 1 {
		return nil, errors.New("the image check state has no version")
	}
	return &s, nil
}

// MatchPaths returns the files matching one of globs, in files' order.
func MatchPaths(globs, files []string) []string {
	var out []string
	for _, f := range files {
		if slices.ContainsFunc(globs, func(g string) bool { return matchGlob(g, f) }) {
			out = append(out, f)
		}
	}
	return out
}
