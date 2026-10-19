// Package managed holds the fact that makes a project Fugaro-managed: the
// object the run that created the project writes at the root of the runs
// bucket, and (the task after this one) the read that fails closed over it.
// The object is the authority for whether confirmations may be skipped
// (docs/design/managed-projects.md, decisions D56 and D57), so no
// credential a run holds may have a write path to it — the shape of that,
// and the tests that assert it, are in this file's WriteMarker and its
// test.
package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/api/googleapi"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

const (
	// MarkerObject is the creation fact: an object at the root of the runs
	// bucket, beside fugaro/project.json. The root is the whole point — the
	// bucket's grants to a job account, a build account and a launcher are
	// all conditioned to a prefix below it (gcp.JobBucketPrefixes,
	// gcp.BuildBucketPrefixes, gcp.LauncherBucketCondition), so the account
	// a run's agent holds has no write path to this key, and the identity
	// that does is the operator's own.
	//
	// It is an object rather than a Terraform resource for D74's reason:
	// fugaro init rewrites the installation's terraform.tfvars.json from its
	// own flags on every run, so a variable-driven object would be planned
	// to destroy on the next ordinary init — silently, with the apply that
	// follows reading "not managed".
	MarkerObject = "fugaro/managed.json"
	// MarkerVersion is the version of the object written when the creation
	// fact is all there is to record: a run that created the project and
	// chose no budget amount. It is what a run that is not managed writes
	// (Task 4.2: the amount is required for a managed creation and nothing
	// else changes), and it is what the runs before that task wrote, so it
	// stays readable and stays managed.
	MarkerVersion = 1
	// MarkerVersionBudget is the version written when the run also records
	// the monthly budget amount it chose, which a managed creation is
	// required to choose (Task 4.2, and D64's answer: the amount is required
	// at creation, with no silent default). The extension is **a version of
	// the same object rather than a second object** — one thing to find, one
	// thing to prove the bucket for, one thing for a reader to check — which
	// is the shape the plan's Task 4.2 asks for: the read of Task 0.2 is
	// taught this version, and any version it does not know stays refused.
	MarkerVersionBudget = 2
)

// Budget is the monthly amount and the currency a managed creation was
// required to choose, as the creation fact records them (Task 4.2). The two
// travel together because an amount is not an amount without the currency it
// is in — the Terraform budget refuses one without the other too
// (deploy/terraform/gcp/modules/installation/variables.tf's budget variable,
// the same three figures) — and what is recorded here is what the owner
// chose at creation, for a later run and a restore to read rather than
// guess. It is never a credential and never an estimate.
type Budget struct {
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
}

// The range a recorded amount lies in, in whole units of its currency — the
// unit the Terraform budget takes (`specified_amount.units`,
// deploy/terraform/gcp/modules/installation/budget.tf) and the one init's
// --budget is given in, not minor units. The floor is the Terraform budget's
// own (a whole amount above 0); the ceiling is Fugaro's: a monthly line of
// ten million whole units is above any budget a managed project is created
// with, and a figure beyond it is a typo or a value written by someone else,
// not a choice — it is what keeps an amount such as 9223372036854775807 from
// being believed as a line anything is compared against.
const (
	MinBudgetAmount int64 = 1
	MaxBudgetAmount int64 = 10_000_000
)

// Check refuses a budget that is not one a managed creation records: an
// amount outside [MinBudgetAmount, MaxBudgetAmount] whole units, or a
// currency that is not exactly three ASCII uppercase letters — the shape of
// an ISO 4217 code (USD, EUR), and the shape the Terraform budget's own
// validation requires. It is a shape check only: there is no embedded list
// of codes, because the code the budget is created with must also be the
// billing account's own currency, which only the Budget API knows, and a
// list here would be a second, staler answer to that. It is the same rule on
// both sides — the write (Marker.Bytes, and init before anything is created)
// and the read (readFact), so a value the write would refuse is never
// believed when read.
func (b Budget) Check() error {
	if b.Amount < MinBudgetAmount || b.Amount > MaxBudgetAmount {
		return fmt.Errorf("the budget amount %d is outside %d to %d whole units of its currency", b.Amount, MinBudgetAmount, MaxBudgetAmount)
	}
	if !isCurrencyCode(b.Currency) {
		return fmt.Errorf("the budget currency %q is not three ASCII uppercase letters (the ISO 4217 code shape, such as USD)", b.Currency)
	}
	return nil
}

// isCurrencyCode is the ISO 4217 code shape: exactly three bytes, each
// A to Z. Byte by byte rather than a Unicode class, so a letter outside
// ASCII, a lower-case one, a space and a newline are all refused.
func isCurrencyCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

// Marker is what MarkerObject holds: the fact, the project it is a fact
// about, and — in the version a managed creation writes — the amount that
// creation was required to choose. Nothing else: the value is never a
// credential, and the project it names is what keeps a copy of the object in
// another bucket from being believed (with the bucket's own project number,
// which is the read's job and internal/infra/project.go's rule: a bucket
// name is global).
type Marker struct {
	Version    int     `json:"version"`
	GCPProject string  `json:"gcp_project"`
	Budget     *Budget `json:"budget,omitempty"`
}

// Bytes is the object's content. A marker that does not say which project
// it is about is not one and is refused rather than written; neither is one
// whose version and budget disagree — version 1 carries no amount (a
// creation that was not managed never records one, whatever budget it asked
// Terraform for), and version 2 carries one, because that is the whole of
// what version 2 means. The two refusals are what keeps the version a fact
// rather than a decoration: the read believes version 2 for its budget, and
// a writer that set the version without one would record a managed creation
// with no amount — the state Task 4.2 exists to make impossible.
func (m Marker) Bytes() ([]byte, error) {
	switch m.Version {
	case MarkerVersion:
		if m.Budget != nil {
			return nil, fmt.Errorf("%s: version %d records the creation fact alone, and this one carries a budget", MarkerObject, MarkerVersion)
		}
	case MarkerVersionBudget:
		if m.Budget == nil {
			return nil, fmt.Errorf("%s: version %d records the budget amount a managed creation was required to choose, and this one carries none", MarkerObject, MarkerVersionBudget)
		}
		if err := m.Budget.Check(); err != nil {
			return nil, fmt.Errorf("%s: version %d records a budget the creation could not have chosen: %v", MarkerObject, MarkerVersionBudget, err)
		}
	default:
		return nil, fmt.Errorf("%s: version %d is not one this package writes (%d and %d)", MarkerObject, m.Version, MarkerVersion, MarkerVersionBudget)
	}
	if m.GCPProject == "" {
		return nil, fmt.Errorf("%s: no GCP project, so nothing says what the fact is about", MarkerObject)
	}
	return json.Marshal(m)
}

// WriteMarker records the creation fact — "this run created this project" —
// in the runs bucket of that project, for a run that created it.
//
// created is the fact as the run holds it: the ID of the project this run
// created (init's r.res.Created), or "" for a run that created none. It is
// the only thing the write is gated on, and the gate is the first check
// below: a run that created no project writes nothing at all — not the
// object, not a read of the bucket. Adding Firebase is not creating
// (init --firebase does it on projects that already exist, D73), so a write
// gated on anything else would mark the owner's own projects and belong.
//
// project is the project the marker would be about, and number the number
// the caller believes that project has (infra.ProjectNumber's answer, which
// the caller reads because this package knows nothing of internal/infra).
// They are the same value as created in the one run that may call this —
// init passes r.res.Created and r.gcpProject — and a call naming another
// project is an error rather than a marker for either: the fact is written
// once, from the one place that knows it, and a second call is an error
// rather than an update (IfGenerationMatch(0) on an object that is there).
//
// Before writing, the bucket must be proved to be that project's own — its
// own project number, and the fugaro=managed label the installation sets —
// exactly as internal/infra/project.go's ReadProjectMarker proves a bucket
// before believing an object in it. A bucket name is global, so a marker
// planted in a stranger's bucket would otherwise read as a fact about this
// project.
//
// budget is the monthly amount and the currency the run was required to
// choose for a managed creation (Task 4.2, D64), or nil for a creation that
// is not managed — an ordinary `fugaro init --create-project --budget …`
// records no amount, because the fact it writes is the one it wrote before
// this version existed. It chooses the object's version and nothing else: a
// write with a budget is version 2, one without is version 1, and there is
// no third state and no default between them.
//
// The caller warns rather than fails when this returns an error: the
// project exists either way, an unmarked project is the fail-closed
// direction (nothing is skipped for it), and the owner-run flagging of an
// existing project (D58, Task 6.1) is the way back.
func WriteMarker(ctx context.Context, s *storage.Service, bucket, created, project string, number uint64, budget *Budget) error {
	if created == "" {
		return nil
	}
	switch {
	case project == "" || project != created:
		return fmt.Errorf("refusing to mark project %q as managed: this run created %q, and the marker records a creation — add Firebase to an existing project, a project this run did not create, is not one", project, created)
	case bucket == "" || number == 0:
		return fmt.Errorf("refusing to write %s for %s: no runs bucket, or no project number to check it against", MarkerObject, project)
	case s == nil:
		return fmt.Errorf("refusing to write %s for %s: no storage client", MarkerObject, project)
	}
	b, err := s.Buckets.Get(bucket).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("reading gs://%s before writing %s for %s: %w", bucket, MarkerObject, project, err)
	}
	if b.ProjectNumber != number || b.Labels[gcp.LabelManaged] != gcp.ManagedValue {
		return fmt.Errorf("refusing to write %s for %s into gs://%s: it is not that project's own runs bucket (project number %d, label %s=%s)",
			MarkerObject, project, bucket, number, gcp.LabelManaged, gcp.ManagedValue)
	}
	m := Marker{Version: MarkerVersion, GCPProject: project}
	if budget != nil {
		// The version follows the budget rather than being set beside it:
		// Bytes refuses a disagreement either way, and this is the one place
		// that decides, so a caller cannot write version 2 with no amount.
		m.Version, m.Budget = MarkerVersionBudget, budget
	}
	data, err := m.Bytes()
	if err != nil {
		return err
	}
	_, err = s.Objects.Insert(bucket, &storage.Object{Name: MarkerObject, ContentType: "application/json"}).
		IfGenerationMatch(0).Media(bytes.NewReader(data)).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == http.StatusPreconditionFailed {
			return fmt.Errorf("gs://%s/%s is already there: the creation fact is written once, by the run that created the project", bucket, MarkerObject)
		}
		return fmt.Errorf("writing gs://%s/%s for %s: %w", bucket, MarkerObject, project, err)
	}
	return nil
}
