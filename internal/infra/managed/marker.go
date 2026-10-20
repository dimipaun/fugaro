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
	"io"
	"net/http"
	"strconv"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/safetext"
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

	// ProjectLabelKey and ProjectLabelValue are the second half of what makes
	// a project managed: a label on the GCP project itself, written by the
	// run that created it, in the same step that writes the object above
	// (Task 3.0; the plan's own spelling, `fugaro_managed=true`, and the
	// design's §5.1 item 3 records the spec's `fugaro-managed` as the name
	// not taken).
	//
	// It is a second mark and never a substitute: the object stays the whole
	// authority (D56/D57), and the label can only ever narrow what the object
	// says — a project is managed only when both agree, so an absent label, a
	// label with any other value and a project the read cannot fetch are all
	// "not managed". A label that could widen would be a way for anything
	// holding resourcemanager.projects.update to talk a run out of its
	// confirmations, which is the boundary this whole feature rests on
	// (design finding 14).
	//
	// It is not gcp.LabelManaged/gcp.ManagedValue, which are the
	// installation's label on the *runs bucket* (internal/backend/gcp) and a
	// different key on a different kind of object. A project label key lives
	// for the life of the project, which is why the two are declared apart.
	ProjectLabelKey   = "fugaro_managed"
	ProjectLabelValue = "true"
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

// ErrAlreadyMarked is what a marker writer returns when the object is already
// there and is not one that write may replace. The two writers mean slightly
// different things by it, and each puts its own sentence around it: for the run
// that created the project, a second creation fact is a fact written once; for
// the owner's flag, the object is either one that already records a budget or
// one this build cannot read as this project's version-1 creation fact. It is a
// sentinel so a caller can tell a refused write from a failed one, and so what
// a person reads is the writer's sentence rather than a shared and vaguer one.
var ErrAlreadyMarked = errors.New("the project already records a marker")

// markerAt is MarkerObject as it is now: its bytes, and the generation those
// bytes are. ok is false when nothing is there, which is generation 0 — the
// figure IfGenerationMatch(0) means "write only if nothing is there" — so an
// absent object's generation is the create-only precondition rather than a
// second case for the caller to carry.
//
// The generation comes out of the download's own answer, so the bytes and the
// generation are one reading: through two calls a caller could be handed one
// version's bytes and another's generation, and the write that followed would
// condition on an object it never saw.
//
// An object the caller may not read is an error and never "nothing is there":
// the value of reading before writing is that the write may not clobber what it
// could not look at, and treating an unreadable object as an absent one would
// do exactly that.
func markerAt(ctx context.Context, s *storage.Service, bucket, project string) (data []byte, gen int64, ok bool, err error) {
	resp, err := s.Objects.Get(bucket, MarkerObject).Context(ctx).Download()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
			return nil, 0, false, nil
		}
		if errors.As(err, &ge) && ge.Code == http.StatusForbidden {
			return nil, 0, false, refusedf("gs://%s/%s cannot be read, so this write cannot tell whether %s already records whether it is managed, and it writes nothing it could not look at",
				bucket, MarkerObject, project)
		}
		return nil, 0, false, fmt.Errorf("reading gs://%s/%s for %s: %w", bucket, MarkerObject, project, err)
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, markerMaxBytes+1))
	if err != nil {
		return nil, 0, false, fmt.Errorf("reading gs://%s/%s for %s: %w", bucket, MarkerObject, project, err)
	}
	g, err := strconv.ParseInt(resp.Header.Get("X-Goog-Generation"), 10, 64)
	if err != nil || g <= 0 {
		// No generation is no precondition: a caller that carried on would
		// either clobber whatever is there or read "create only" out of a
		// zero, and both are writes this package must not make blind.
		return nil, 0, false, fmt.Errorf("reading gs://%s/%s for %s: the answer carries no object generation, so a write cannot be conditioned on the object this read saw", bucket, MarkerObject, project)
	}
	return data, g, true, nil
}

// insertMarker writes data at MarkerObject under a generation precondition: 0
// writes only if nothing is there, and a generation writes only if the object
// is still exactly the version the caller read. A precondition that does not
// hold is ErrAlreadyMarked rather than a failure — someone else wrote first, or
// the object changed under the caller — and the object's own name is carried
// with the sentinel, so each writer says in its own words what that means for
// it.
func insertMarker(ctx context.Context, s *storage.Service, bucket string, gen int64, data []byte) error {
	_, err := s.Objects.Insert(bucket, &storage.Object{Name: MarkerObject, ContentType: "application/json"}).
		IfGenerationMatch(gen).Media(bytes.NewReader(data)).Context(ctx).Do()
	if err == nil {
		return nil
	}
	var ge *googleapi.Error
	if errors.As(err, &ge) && ge.Code == http.StatusPreconditionFailed {
		return fmt.Errorf("%w: gs://%s/%s", ErrAlreadyMarked, bucket, MarkerObject)
	}
	return fmt.Errorf("writing gs://%s/%s: %w", bucket, MarkerObject, err)
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
// rather than an update (insertMarker's IfGenerationMatch(0)).
//
// Before writing, the bucket must be proved to be that project's own
// (proveRunsBucket): its own project number, and the fugaro=managed label the
// installation sets. A bucket name is global, so a marker planted in a
// stranger's bucket would otherwise read as a fact about this project. A
// bucket that cannot be read is refused rather than treated as an absent one,
// so nothing is written anywhere this call could not look.
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
// existing project (D58, Task 6.1) is the way back — including for the
// version-1 fact this writes, which WriteFlag may replace with the version
// that records an amount.
// The error is one bounded field (safetext.FieldErr) before it is returned:
// what it carries is a project name out of a configuration, a bucket name and
// the words of an API's answer, and the caller prints it. The bound is on the
// message alone — the chain stays, so a caller that matches a *googleapi.Error
// inside it still does.
func WriteMarker(ctx context.Context, s *storage.Service, bucket, created, project string, number uint64, budget *Budget) error {
	return safetext.FieldErr(writeMarker(ctx, s, bucket, created, project, number, budget))
}

func writeMarker(ctx context.Context, s *storage.Service, bucket, created, project string, number uint64, budget *Budget) error {
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
	if err := proveRunsBucket(ctx, s, bucket, project, MarkerObject, number); err != nil {
		return err
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
	// Create only, and never a replacement: this is the one write that knows
	// the project is the run's own creation, and a second call — this run's
	// or any other writer's — is ErrAlreadyMarked.
	if err := insertMarker(ctx, s, bucket, 0, data); err != nil {
		if errors.Is(err, ErrAlreadyMarked) {
			return fmt.Errorf("gs://%s/%s is already there: the creation fact is written once, by the run that created the project", bucket, MarkerObject)
		}
		return err
	}
	return nil
}

// WriteManagedLabel writes the other half of the creation fact: the
// ProjectLabelKey=ProjectLabelValue label on the GCP project itself (Task
// 3.0). It is called from the same place and the same moment WriteMarker is —
// the operator-run path that created the project (init's markCreated) — so
// that the two marks are one step and cannot disagree about whether the run
// tried. It is deliberately not reachable from anywhere else: nothing a
// coding agent's session runs writes it (no agent session can create a
// project at all: projectStage.Apply needs a terminal and a typed
// confirmation), and neither does `fugaro managed apply`, which only reads.
//
// created and project are WriteMarker's, with the same gate and for the same
// reason: a run that created no project (created == "") writes nothing, and a
// call naming a project the run did not create is an error rather than a
// label for either. Adding Firebase to an existing project is not creating
// one (D73), so this is not a way to label the owner's own projects — which
// matters more here than it does for the object, because a project label is
// visible to everyone who can read the project. An owner who wants one of
// those projects labelled uses WriteManagedLabelForFlag, below, whose gate is
// the command's own.
//
// It is idempotent in the one direction that is not dangerous: a project that
// already carries exactly this label is left alone (the mark is written once,
// and a second run writing the same value changes nothing). A project
// carrying the key with any other value is refused rather than overwritten —
// a label someone else put there is not this package's to replace, and
// `fugaro_managed=false` is a statement, not an empty slot. The other labels
// the project carries are preserved: a Resource Manager v1 update replaces
// the whole label map, so this reads it, adds its own key and sends the
// result back.
//
// Like WriteMarker, the caller warns rather than fails: the project exists
// either way, and a project with the object and no label is the fail-closed
// direction — the read answers "not managed" for it, with a reason naming the
// label (docs/gcp-setup.md carries the owner-run backfill). And like
// WriteMarker's, the error is one bounded field (safetext.FieldErr) before it
// is returned: what it carries is a project name out of a configuration and the
// words of a Resource Manager answer, and `fugaro init` prints it in a warning.
// The bound is on the message alone, so a *googleapi.Error inside it is still
// matched.
func WriteManagedLabel(ctx context.Context, c *crm.Service, created, project string) error {
	if created == "" {
		return nil
	}
	if project == "" || project != created {
		return safetext.FieldErr(fmt.Errorf("refusing to label project %q as %s: this run created %q, and the label records a creation — adding Firebase to an existing project, a project this run did not create, is not one", project, ProjectLabelKey, created))
	}
	return safetext.FieldErr(writeManagedLabel(ctx, c, project, "the run that created the project"))
}

// WriteManagedLabelForFlag writes the same label under the other gate a
// managed project can be made by: `fugaro managed flag` (Task 6.1), where the
// project is an EXISTING one — the owner's, or a project an earlier run
// created without --managed — and no run of ours ever created it.
//
// That is the one difference from WriteManagedLabel, and it is the whole
// reason this entry point exists: the creation write refuses a project it did
// not create (there, "created" is evidence; here it would be false, and
// requiring it would make the command unable to do the thing it is for). The
// gate that replaces it belongs to the command, not to this package, and it
// is a stronger one than a string comparison: managed_flag.go refuses a
// coding agent's session and any Fugaro run's process before this is reached
// (agentMarker and managedFlagRunMarkers), and the confirmation is the
// project's exact GCP project ID typed at the owner's own terminal
// (confirmTypedGCPProject) — D58's answer, and the reason the label is
// written only ever by an owner-run command (design finding 14: a label is a
// write to the privilege boundary the feature rests on).
//
// Everything else is writeManagedLabel's, shared and unchanged: the same
// value, the same refusals, the same labels kept, and the same fail-closed
// direction when it cannot write (the object is the authority and the label
// narrows; an unlabelled project reads as not managed). Nothing but
// managed_flag.go may call this — the flag's typed confirmation is a
// parameter of the command, not of this function, so a caller that skips the
// command skips the gate.
//
// The error is one bounded field (safetext.FieldErr), exactly as
// WriteManagedLabel's is: the project name is the operator's own argument and
// the rest is a Resource Manager answer, and the owner-run command prints it.
func WriteManagedLabelForFlag(ctx context.Context, c *crm.Service, project string) error {
	if project == "" {
		return safetext.FieldErr(fmt.Errorf("refusing to write the %s label: no project name", ProjectLabelKey))
	}
	return safetext.FieldErr(writeManagedLabel(ctx, c, project, "the owner flagging the project"))
}

// writeManagedLabel is the write both gates above end in, so that the label
// itself has exactly one implementation: read the project, leave a project
// that already carries the label alone, refuse one carrying the key with
// another value, keep every other label, write once. who names the writer in
// that refusal — the two callers are the two hands a project becomes managed
// by, and the operator reading it should be told which of them it was.
func writeManagedLabel(ctx context.Context, c *crm.Service, project, who string) error {
	if c == nil {
		return fmt.Errorf("refusing to label %s as managed: no Resource Manager client", project)
	}
	p, err := c.Projects.Get(project).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("reading project %s before labelling it %s=%s: %w", project, ProjectLabelKey, ProjectLabelValue, err)
	}
	// Absent is the ordinary case — nothing has ever written this label but
	// this function — and "" is treated as absent: the key with an empty
	// value is not a value this package writes, and it is not the label.
	if v := p.Labels[ProjectLabelKey]; v != "" {
		if v == ProjectLabelValue {
			return nil
		}
		return fmt.Errorf("refusing to label %s as %s=%s: it already carries %s=%s, and the label is written once, by %s", project, ProjectLabelKey, ProjectLabelValue, ProjectLabelKey, v, who)
	}
	labels := make(map[string]string, len(p.Labels)+1)
	for k, v := range p.Labels {
		labels[k] = v
	}
	labels[ProjectLabelKey] = ProjectLabelValue
	// The project read above, with one field changed, is what goes back, and
	// the whole object rather than the labels alone: projects.update replaces
	// the project's editable state — the display name and the label map — with
	// the request's, so a body holding only labels takes the owner's display
	// name off the project. gcloud and Terraform send the object they read for
	// the same reason. The labels go in as the copy built above rather than as
	// p.Labels itself, so that nothing here edits the map the read answered
	// with.
	p.Labels = labels
	if _, err := c.Projects.Update(project, p).Context(ctx).Do(); err != nil {
		return fmt.Errorf("labelling project %s %s=%s: %w", project, ProjectLabelKey, ProjectLabelValue, err)
	}
	return nil
}

// WriteFlag records the owner's statement that an EXISTING project is
// managed (docs/design/managed-projects.md, D58; the plan's Task 6.1) — the
// second way a project becomes managed, and the only one that does not need
// a creation.
//
// It is the creation fact's object and the creation fact's discipline, and
// the one thing it does not need is the fact: WriteMarker is gated on
// "this run created this project", and this write is gated on nothing but
// the owner having said so — which is D58's answer, and the reason its
// refusal lives in the command rather than here (the operator's own
// terminal, the exact project ID typed, and a coding agent's session
// refused). What DOES live here is everything the object itself must be:
//
//   - the project's own runs bucket, by its number and the installation's
//     fugaro=managed label (proveRunsBucket), exactly as WriteMarker proves it
//     and as the read proves it before believing an object in it. A bucket
//     name is global, so without that proof a mark written into a stranger's
//     bucket would read as a fact about this project.
//   - the version that carries a budget, and never version 1. A managed
//     project whose marker records no amount is refused by the apply-time
//     budget read (Task 4.4: ReadBudget answers ok=false for version 1 and
//     the apply gate stays closed), so a flag that wrote the bare fact would
//     lower the guard on a project Fugaro did not create and buy nothing at
//     all. The amount is the owner's, required and never defaulted (D64), and
//     it is checked by the marker's own Budget.Check before anything is
//     written.
//   - one replacement, of one object, and of nothing else. An object that is
//     there and is the version-1 creation fact of THIS project — the state
//     every `fugaro init --create-project` without --managed leaves, and the
//     state internal/managed's gate refusal names this command as the way out
//     of — is replaced by the version-2 one, because adding the budget to an
//     existing fact is what this command is for, and a flag that refused it
//     would leave that project with no path at all. Everything else that is
//     there is ErrAlreadyMarked: a marker that already records an amount (this
//     command adds a budget, it does not edit one), an object about another
//     project, and an object this build cannot read as a marker. The
//     replacement is conditioned on the generation this call read
//     (insertMarker), so a writer that got there first is never clobbered.
//
// budget is the monthly amount and the currency the owner states, and it is a
// value rather than a pointer because there is no version of this write
// without one. replaced says which of the two writes happened — the object was
// created, or this project's version-1 fact was replaced — because the
// command's own output has to say what it did.
//
// The error is one bounded field (safetext.FieldErr), as WriteMarker's is: it
// carries the project, the bucket's name, the object's own content and an
// API's words, and the owner-run command prints it. The bound is on the
// message alone, so ErrAlreadyMarked, ErrRefused and a *googleapi.Error inside
// it are still matched.
func WriteFlag(ctx context.Context, s *storage.Service, bucket, project string, number uint64, budget Budget) (replaced bool, err error) {
	replaced, err = writeFlag(ctx, s, bucket, project, number, budget)
	return replaced, safetext.FieldErr(err)
}

func writeFlag(ctx context.Context, s *storage.Service, bucket, project string, number uint64, budget Budget) (replaced bool, err error) {
	switch {
	case bucket == "" || number == 0:
		return false, refusedf("to write %s for %s: no runs bucket, or no project number to check it against", MarkerObject, project)
	case s == nil:
		return false, refusedf("to write %s for %s: no storage client", MarkerObject, project)
	}
	// Before the bucket is read, and before anything else: a pair that is not
	// one writes nothing at all, and it is refused without an API call. The
	// rule is the marker's own rather than a copy of it — Bytes applies it
	// again, which is what makes this an early refusal and not the only one.
	if err := budget.Check(); err != nil {
		return false, refusedf("to flag %s as managed: the budget the flag would record is not a managed creation's: %v", project, err)
	}
	if err := proveRunsBucket(ctx, s, bucket, project, MarkerObject, number); err != nil {
		return false, err
	}
	// What is there now decides what this write may do, and it is read before
	// the write rather than discovered by it: the one object this may replace
	// is this project's version-1 creation fact, and anything else is refused
	// with what to look at rather than overwritten.
	cur, gen, ok, err := markerAt(ctx, s, bucket, project)
	if err != nil {
		return false, err
	}
	if ok {
		existing, perr := parseMarker(cur, bucket, project)
		if perr != nil || existing.Version != MarkerVersion || existing.GCPProject != project {
			return false, alreadyFlagged(bucket, project, cur)
		}
		replaced = true
	}
	m := Marker{Version: MarkerVersionBudget, GCPProject: project, Budget: &budget}
	data, err := m.Bytes()
	if err != nil {
		return false, err
	}
	if err := insertMarker(ctx, s, bucket, gen, data); err != nil {
		if errors.Is(err, ErrAlreadyMarked) {
			// cur is nil here: the object changed between the read above and
			// this write, so what is there is not what was judged.
			return false, alreadyFlagged(bucket, project, nil)
		}
		return false, err
	}
	return replaced, nil
}

// alreadyFlagged is what the flag says when the object is there and is not the
// version-1 fact it may replace: what is there is named, and so is the command
// that reads it. cur nil is the other case — the object was written between
// this call's read and its write — which is the same answer for a different
// reason, because in both the object is one this command did not write.
func alreadyFlagged(bucket, project string, cur []byte) error {
	what := "is not this project's version-1 creation fact, and this command replaces nothing else"
	if cur == nil {
		what = "was written by someone else between this command's read and its write"
	} else if m, err := parseMarker(cur, bucket, project); err == nil {
		switch {
		case m.GCPProject != project:
			what = fmt.Sprintf("names project %s, not %s", safetext.Field(m.GCPProject), safetext.Field(project))
		case m.Version == MarkerVersionBudget:
			what = "already records whether it is managed and the budget amount it records"
		default:
			what = fmt.Sprintf("is a version %d marker", m.Version)
		}
	}
	return fmt.Errorf("%w: gs://%s/%s %s, so %s was not flagged and nothing was written. This command adds the budget to a project whose marker records none, once; to read what is there, use `gcloud storage cat gs://%s/%s` as the operator",
		ErrAlreadyMarked, bucket, MarkerObject, what, project, bucket, MarkerObject)
}
