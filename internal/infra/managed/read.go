package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// markerMaxBytes bounds the marker read: the object is a few dozen bytes, and
// anyone holding objectAdmin on the bucket could replace it.
// internal/infra/project.go bounds its own marker read at the same figure, for
// the same reason.
const markerMaxBytes = 4 << 10

// runsBucketName is where an installation's runs bucket lives: the name the
// installation module gives it, the name init's marker write insists on
// (initEngine.markCreated in internal/cli/init.go refuses to mark a project
// whose runs bucket is anything else, for exactly this reason), and the name
// this read looks in.
// One name, so a marker cannot be written somewhere the read does not look —
// and, the other way round, so a marker in a bucket of another name is simply
// not found, which is the fail-closed direction.
func runsBucketName(gcpProject string) string { return "fugaro-runs-" + gcpProject }

// IsManaged reports whether project — the Fugaro project of the installation
// whose GCP project is gcpProject — was created by a Fugaro run, and so is
// managed: the state in which typed confirmations are skipped
// (docs/design/managed-projects.md, D55). It reads the creation fact
// (MarkerObject) from the installation's runs bucket, and believes it only
// after proving the bucket is that project's own.
//
// The proof is the whole point, and it is internal/infra/project.go's rule:
// **a name is global, so a mark proves nothing by itself.** Anyone can make a
// bucket called fugaro-runs-<gcpProject> in a project of their own and put a
// perfect marker in it. So the read takes the GCP project's own number from
// Resource Manager, requires the bucket's own project number to be that
// number, and requires the bucket to carry the installation's fugaro=managed
// label; only then does it look at the object, and only then does it believe
// it. (The number is read here rather than taken as a parameter because
// IsManaged's interface is (project, gcpProject) — and this package must not
// import internal/infra, whose ProjectNumber answers it; the six lines below
// are that function's read.)
//
// It fails closed, and every way of not being managed is an answer rather
// than a failure: no bucket, a bucket in another project, a bucket without
// the label, an absent object, an object that cannot be read, an object this
// build cannot parse, a gcp_project field that is not the one asked about, a
// version this build does not know — all false, each with a one-line reason
// (the shape initflow's Status.Detail uses) a person can act on. 404 and 403
// are answers: Google answers a project the caller may not read the same way
// it answers one that is not there, so the reason says the number cannot be
// read rather than guessing which it was, and a Resource Manager API that is
// disabled answers 403 too — which reads as "not managed", the direction in
// which a caller asks its confirmation rather than skipping it.
//
// The fact is scoped to the GCP project and not to the name: the bucket that
// carries it is the GCP project's own, and an installation is one Fugaro
// project in one GCP project (init's owner() and the runs bucket's name say
// the same). project is therefore the name the answer is reported about — it
// appears in every reason and in nothing else — and asking about the same GCP
// project under another name answers the same way. The read does not compare
// it against anything, and a later task that made it a condition would be
// adding a check this design does not have.
//
// **The verdict therefore rests entirely on gcpProject, so every caller
// (Task 0.3 and the 3.x tasks) MUST take it from the installation's own
// trusted configuration — the local config, or the shared config the
// installation published — and never from a repository's fugaro.yaml, from a
// local-config field a run can write, or from an environment variable.** A
// caller that lets a repository or a run choose gcpProject has handed the
// answer away: pointing this read at a managed project of the attacker's own
// is all it takes to be told true, and the confirmations are then skipped for
// a project that was never created by a Fugaro run.
//
// err is set only when the read could not be made at all: a caller's own
// mistake (no clients, no project), or an API that did not answer (a 5xx,
// which the storage client does not retry into an answer). It is never the
// signal: managed is true only with a nil error, and an error is never a
// reason to proceed. A caller that wants to skip a confirmation needs both
// err == nil and managed == true, and a caller that wants to explain itself
// prints the reason whenever managed is false, error or not.
func IsManaged(ctx context.Context, s *storage.Service, c *crm.Service, project, gcpProject string) (ok bool, reason string, err error) {
	// The reason and the error are one bounded field wherever a caller prints
	// them, and both a name out of a config file and an API's error text can
	// carry a control character, a newline (a 5xx body does), a bidi override
	// or ten thousand characters: safetext.Field is the boundary, so no return
	// below has to remember it. The error keeps its chain (safetext.FieldErr),
	// so a caller that reads a *googleapi.Error out of it still does.
	defer func() {
		reason = safetext.Field(reason)
		err = safetext.FieldErr(err)
	}()
	_, ok, reason, err = readFact(ctx, s, c, project, gcpProject)
	return ok, reason, err
}

// ReadBudget is the monthly budget amount and the currency that a managed
// project's creation fact records — the amount Task 4.2 requires a managed
// creation to choose, and the figures a later read has to compare a spend
// against (Task 4.4's apply-time read, the caller this exists for: the apply
// path reaches the fact of a project it did not create, and a restore reads
// what was chosen before).
//
// It is the same read as IsManaged, over the same object and the same proof
// — one read, so the amount can never be read from an object IsManaged would
// refuse — and it answers the third question that read can: not only whether
// the project is managed, but what its creation said the monthly line is. ok
// is true only when the fact was read *and* it records an amount; a project
// that is managed under a version that records none (MarkerVersion: a
// creation before Task 4.2, or one that was not managed) is ok=false with a
// nil error and a reason saying so, which is the fail-closed answer for a
// caller that needs a line to compare against — an absent amount is not an
// unlimited one.
//
// One ok=false answer carries a figure: a version-2 marker whose recorded
// budget fails Budget.Check comes back with that refused figure (and the
// reason), so the caller can report "the recorded budget is invalid" rather
// than "not managed". The figure is never valid when ok is false; a caller
// that needs a line reads it only when ok is true.
func ReadBudget(ctx context.Context, s *storage.Service, c *crm.Service, project, gcpProject string) (budget Budget, ok bool, reason string, err error) {
	a, err := ReadBudgetAnswer(ctx, s, c, project, gcpProject)
	return a.Budget, a.OK, a.Reason, err
}

// BudgetAnswer is ReadBudget's answer with the one thing its four return
// values cannot say: Invalid is true exactly when the object is a version-2
// marker about this project whose recorded figure fails Budget.Check —
// including an empty one ("budget":{}), whose figure is the zero Budget and so
// cannot be told from "no figure" by the figure alone. Budget then holds the
// refused figure; it is never valid unless OK.
type BudgetAnswer struct {
	Budget  Budget
	OK      bool
	Invalid bool
	Reason  string
}

// ReadBudgetAnswer is ReadBudget, answering with a BudgetAnswer. The reason
// and the error are bounded the way IsManaged's are: this is the same read,
// and what it prints is the same text.
func ReadBudgetAnswer(ctx context.Context, s *storage.Service, c *crm.Service, project, gcpProject string) (a BudgetAnswer, err error) {
	defer func() {
		a.Reason = safetext.Field(a.Reason)
		err = safetext.FieldErr(err)
	}()
	m, managed, reason, err := readFact(ctx, s, c, project, gcpProject)
	if err != nil || !managed {
		if err == nil && m.Budget != nil && m.Budget.Check() != nil {
			return BudgetAnswer{Budget: *m.Budget, Invalid: true, Reason: reason}, nil
		}
		return BudgetAnswer{Reason: reason}, err
	}
	if m.Budget == nil {
		return BudgetAnswer{Reason: fmt.Sprintf("gs://%s/%s records no budget amount for %s: the creation fact is version %d, which is a creation this task's amount was never required for", runsBucketName(gcpProject), MarkerObject, project, m.Version)}, nil
	}
	return BudgetAnswer{Budget: *m.Budget, OK: true}, nil
}

// readFact is the read both above are: the creation fact of gcpProject, as
// this build believes it (IsManaged) or not, with the reason why when it does
// not. Its doc is IsManaged's — every decision in the body below is that
// comment's, and each return is one of the ways it lists of being "not
// managed", answered rather than failed.
func readFact(ctx context.Context, s *storage.Service, c *crm.Service, project, gcpProject string) (m Marker, ok bool, reason string, err error) {
	bucket := runsBucketName(gcpProject)
	switch {
	case project == "":
		return Marker{}, false, "no project name, so nothing says what a creation fact would be about",
			fmt.Errorf("managed: IsManaged: no project name")
	case gcpProject == "":
		return Marker{}, false, "no GCP project, so there is no runs bucket to read a creation fact from",
			fmt.Errorf("managed: IsManaged: no GCP project for %s", project)
	case s == nil:
		return Marker{}, false, fmt.Sprintf("no storage client to read gs://%s/%s with", bucket, MarkerObject),
			fmt.Errorf("managed: IsManaged: no storage client for %s", project)
	case c == nil:
		return Marker{}, false, fmt.Sprintf("no Resource Manager client to check gs://%s against the number of %s with", bucket, gcpProject),
			fmt.Errorf("managed: IsManaged: no Resource Manager client for %s", project)
	}

	// The number first: without it there is nothing to check the bucket
	// against, and a bucket of the right name in a stranger's project would
	// be believed.
	number, err := gcpProjectNumber(ctx, c, gcpProject)
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && (ge.Code == http.StatusForbidden || ge.Code == http.StatusNotFound) {
			return Marker{}, false, fmt.Sprintf("GCP project %s cannot be read, so gs://%s cannot be proved to be its own runs bucket (%s is not managed)", gcpProject, bucket, project), nil
		}
		return Marker{}, false, fmt.Sprintf("reading project %s for %s: %v", gcpProject, project, err), err
	}
	// A project whose number came back as zero is a project the read cannot
	// check anything against — and zero is the one number a bucket of the
	// same name in a stranger's project could also report, so believing it
	// would make the check pass by itself. internal/infra/project.go refuses
	// the same value for the same reason.
	if number == 0 {
		return Marker{}, false, fmt.Sprintf("GCP project %s came back with no number, so gs://%s cannot be proved to be its own runs bucket (%s is not managed)", gcpProject, bucket, project), nil
	}

	b, err := s.Buckets.Get(bucket).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && (ge.Code == http.StatusForbidden || ge.Code == http.StatusNotFound) {
			return Marker{}, false, fmt.Sprintf("gs://%s is not there, so %s has no creation fact (an installation that never made its runs bucket recorded none)", bucket, project), nil
		}
		return Marker{}, false, fmt.Sprintf("reading gs://%s for %s: %v", bucket, project, err), err
	}
	if b.ProjectNumber != number {
		return Marker{}, false, fmt.Sprintf("gs://%s belongs to project number %d, not %d (%s), so its contents are not a fact about %s", bucket, b.ProjectNumber, number, gcpProject, project), nil
	}
	if b.Labels[gcp.LabelManaged] != gcp.ManagedValue {
		return Marker{}, false, fmt.Sprintf("gs://%s carries no %s=%s label, so it is not %s's runs bucket", bucket, gcp.LabelManaged, gcp.ManagedValue, project), nil
	}

	resp, err := s.Objects.Get(bucket, MarkerObject).Context(ctx).Download()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && (ge.Code == http.StatusForbidden || ge.Code == http.StatusNotFound) {
			return Marker{}, false, fmt.Sprintf("gs://%s/%s is not there (or cannot be read), so %s was not created by a managed run", bucket, MarkerObject, project), nil
		}
		return Marker{}, false, fmt.Sprintf("reading gs://%s/%s for %s: %v", bucket, MarkerObject, project, err), err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, markerMaxBytes+1))
	if err != nil {
		return Marker{}, false, fmt.Sprintf("reading gs://%s/%s for %s: %v", bucket, MarkerObject, project, err), err
	}
	// The object has to be exactly one marker and nothing else, and the shape
	// is parseMarker's — one function, because the flag's writer has to make
	// the same judgement before it may replace an object (marker.go, and its
	// own doc says why the shape is shared rather than written twice): what
	// counts as this project's version-1 creation fact is one answer or it is
	// two.
	parsed, perr := parseMarker(data, bucket, project)
	if perr != nil {
		return Marker{}, false, perr.Error(), nil
	}
	m = parsed
	// The project it names first: a fact about another project is that,
	// whatever else is wrong with it, so its budget is never judged (or
	// handed back as this project's invalid figure) before the name is.
	if m.GCPProject != gcpProject {
		return Marker{}, false, fmt.Sprintf("gs://%s/%s names project %s, not %s: the fact is about another project, so %s is not managed", bucket, MarkerObject, m.GCPProject, gcpProject, project), nil
	}
	// The versions this build knows, each with what it has to carry. Any
	// other version is refused rather than partly believed, which is the
	// fail-closed direction for an object someone else may have written: a
	// later version is a shape this build has not been taught, and reading
	// its first two fields as if it had is exactly the mutation this branch
	// exists to stop (Task 0.2's rule, and Task 4.2's: the read is taught the
	// new version rather than made lenient about versions).
	switch m.Version {
	case MarkerVersion:
		// Version 1 carries no budget. checkMarkerShape already refuses the
		// key for this version; this says it again here, where the versions
		// are decided, so the rule does not rest on the shape check alone.
		if m.Budget != nil {
			return Marker{}, false, fmt.Sprintf("gs://%s/%s is version %d and records a budget, which only version %d carries, so %s is not managed by it", bucket, MarkerObject, m.Version, MarkerVersionBudget, project), nil
		}
	case MarkerVersionBudget:
		// The amount is what version 2 is for, so an object of this version
		// without one is not a fact this build wrote — and believing it would
		// be a managed project with no line to compare a spend against (the
		// state Task 4.4's read fails closed on). A budget of 0 or of no
		// currency is the same: an amount is above 0 and a pair.
		if m.Budget == nil {
			return Marker{}, false, fmt.Sprintf("gs://%s/%s is version %d and records no budget amount, and version %d is a creation that was required to choose one, so %s is not managed by it", bucket, MarkerObject, m.Version, MarkerVersionBudget, project), nil
		}
		// The same rule the write holds (Budget.Check): a currency that is
		// not the ISO 4217 shape or an amount outside the range is not a
		// figure this build wrote, so the object is refused whole rather
		// than believed with a line nothing should be compared against.
		if err := m.Budget.Check(); err != nil {
			// The refused figure is handed back beside ok=false (and only
			// here) so ReadBudget's caller can say the recorded budget is
			// invalid rather than that the project is not managed; IsManaged
			// ignores it, and it is never believed as a line.
			return Marker{Budget: m.Budget}, false, fmt.Sprintf("gs://%s/%s is version %d and records no budget amount a managed creation could have chosen (%v), so %s is not managed by it", bucket, MarkerObject, m.Version, err, project), nil
		}
	default:
		return Marker{}, false, fmt.Sprintf("gs://%s/%s is version %d, and this build knows versions %d and %d, so %s is not managed by it", bucket, MarkerObject, m.Version, MarkerVersion, MarkerVersionBudget, project), nil
	}
	return m, true, "", nil
}

// parseMarker is the shape every reader and writer of the object agrees on:
// no bigger than markerMaxBytes (the object is a few dozen bytes, and anyone
// holding objectAdmin on the bucket could replace it), holding exactly one
// marker and nothing else — a second JSON value after it, a field this build
// does not know, a version it does not know are all refused rather than
// partly believed. Each message names the bucket and the object, so a caller
// prints the answer as it stands, and the project it is asked about, so the
// sentence is about the fact rather than about JSON.
//
// It is one function because there are two callers that must agree: the read
// (readFact) and the flag's writer (WriteFlag), which may replace an object
// only if it has recognised it as this project's version-1 creation fact. A
// second copy of the shape would be a second answer to what counts as a
// marker, and the writer's copy is the one that decides what may be
// overwritten.
func parseMarker(data []byte, bucket, project string) (Marker, error) {
	if len(data) > markerMaxBytes {
		return Marker{}, fmt.Errorf("gs://%s/%s is %d bytes, bigger than any marker (%d), so it is not %s's creation fact", bucket, MarkerObject, len(data), markerMaxBytes, project)
	}
	var m Marker
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Marker{}, fmt.Errorf("gs://%s/%s is not a marker for %s: %v", bucket, MarkerObject, project, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Marker{}, fmt.Errorf("gs://%s/%s is not a marker for %s: it holds more than one JSON value", bucket, MarkerObject, project)
	}
	if err := checkMarkerShape(data, m.Version); err != nil {
		return Marker{}, fmt.Errorf("gs://%s/%s is not a marker for %s: %v", bucket, MarkerObject, project, err)
	}
	return m, nil
}

// checkMarkerShape walks the object's keys and refuses anything but the
// Marker writes of the version the object declares, each once, spelled as
// this package writes them — and, for the version that carries one, the same
// over the budget's own fields. DisallowUnknownFields alone does not: Go's
// decoder matches a key to a field case-insensitively ("VERSION" fills
// Version) and lets the last of two duplicate keys win, so
// `{"VERSION":1,"GCP_PROJECT":"<p>"}` and a repeated gcp_project both decode
// into a Marker this build never wrote — and, with a duplicate, into one
// whose deciding field is not the one a reader of the object sees.
//
// The version comes from the decoded object and only widens the shape for
// the versions this build knows: a version it does not know is checked
// against the first version's fields, so the object is refused by the shape
// check or by the version check that follows, and never by both with the
// version left unsaid. What version 2 must *carry* is the caller's check
// (readFact's), because a missing key is not a key this walk sees.
func checkMarkerShape(data []byte, version int) error {
	allowed := objectShape{
		"version":     nil, // the whole shape, spelled as Marker writes it
		"gcp_project": nil,
	}
	if version == MarkerVersionBudget {
		allowed["budget"] = objectShape{"amount": nil, "currency": nil}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := checkObjectShape(dec, allowed); err != nil {
		return err
	}
	// Nothing may follow the object: the caller checks that too (its own
	// reason names the second value), and this is the boundary of the walk.
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("it holds more than one JSON value")
	}
	return nil
}

// objectShape is what fields one JSON object of the fact may carry: a name
// maps to the shape of its value when that value is an object this build
// writes (the budget and its two fields), and to nil when it is a value the
// walk does not descend into (the version and the project).
type objectShape map[string]objectShape

// checkObjectShape walks one JSON object at the decoder's position: every key
// must be one the caller allows, each appears once, and a key whose allowed
// value is a set of sub-keys carries an object of that shape — the budget's
// own fields, checked the same way and for the same reasons. A key this build
// does not write, a key spelled differently and a key that appears twice are
// all refused with what the read of Task 0.2 refuses them with.
func checkObjectShape(dec *json.Decoder, allowed objectShape) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("it is not a JSON object")
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		name, ok := tok.(string)
		if !ok {
			return errors.New("it has a key that is not a name")
		}
		sub, ok := allowed[name]
		if !ok {
			return fmt.Errorf("it has a field this build does not write (%q)", name)
		}
		if seen[name] {
			return fmt.Errorf("it has %q twice", name)
		}
		seen[name] = true
		if sub != nil {
			if err := checkObjectShape(dec, sub); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			continue
		}
		var v any
		if err := dec.Decode(&v); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return err
	}
	return nil
}

// gcpProjectNumber is the GCP project's own number, which its buckets carry.
// It is internal/infra.ProjectNumber's read, repeated because this package
// knows nothing of internal/infra (see marker.go's WriteMarker, which takes
// the number as a parameter for the same reason), and wrapped the same way so
// a failure reads the same whichever side of the boundary it is reported on.
func gcpProjectNumber(ctx context.Context, c *crm.Service, gcpProject string) (uint64, error) {
	p, err := c.Projects.Get(gcpProject).Context(ctx).Do()
	if err != nil {
		return 0, fmt.Errorf("reading project %s: %w", gcpProject, err)
	}
	return uint64(p.ProjectNumber), nil
}
