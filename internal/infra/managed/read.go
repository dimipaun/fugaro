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
)

// markerMaxBytes bounds the marker read: the object is a few dozen bytes, and
// anyone holding objectAdmin on the bucket could replace it.
// internal/infra/project.go bounds its own marker read at the same figure, for
// the same reason.
const markerMaxBytes = 4 << 10

// runsBucketName is where an installation's runs bucket lives: the name the
// installation module gives it, the name init's marker write insists on
// (internal/cli/init.go:672 refuses to mark a project whose runs bucket is
// anything else, for exactly this reason), and the name this read looks in.
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
// err is set only when the read could not be made at all: a caller's own
// mistake (no clients, no project), or an API that did not answer (a 5xx,
// which the storage client does not retry into an answer). It is never the
// signal: managed is true only with a nil error, and an error is never a
// reason to proceed. A caller that wants to skip a confirmation needs both
// err == nil and managed == true, and a caller that wants to explain itself
// prints the reason whenever managed is false, error or not.
func IsManaged(ctx context.Context, s *storage.Service, c *crm.Service, project, gcpProject string) (ok bool, reason string, err error) {
	bucket := runsBucketName(gcpProject)
	switch {
	case project == "":
		return false, "no project name, so nothing says what a creation fact would be about",
			fmt.Errorf("managed: IsManaged: no project name")
	case gcpProject == "":
		return false, "no GCP project, so there is no runs bucket to read a creation fact from",
			fmt.Errorf("managed: IsManaged: no GCP project for %s", project)
	case s == nil:
		return false, fmt.Sprintf("no storage client to read gs://%s/%s with", bucket, MarkerObject),
			fmt.Errorf("managed: IsManaged: no storage client for %s", project)
	case c == nil:
		return false, fmt.Sprintf("no Resource Manager client to check gs://%s against the number of %s with", bucket, gcpProject),
			fmt.Errorf("managed: IsManaged: no Resource Manager client for %s", project)
	}

	// The number first: without it there is nothing to check the bucket
	// against, and a bucket of the right name in a stranger's project would
	// be believed.
	number, err := gcpProjectNumber(ctx, c, gcpProject)
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && (ge.Code == http.StatusForbidden || ge.Code == http.StatusNotFound) {
			return false, fmt.Sprintf("GCP project %s cannot be read, so gs://%s cannot be proved to be its own runs bucket (%s is not managed)", gcpProject, bucket, project), nil
		}
		return false, fmt.Sprintf("reading project %s for %s: %v", gcpProject, project, err), err
	}

	b, err := s.Buckets.Get(bucket).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && (ge.Code == http.StatusForbidden || ge.Code == http.StatusNotFound) {
			return false, fmt.Sprintf("gs://%s is not there, so %s has no creation fact (an installation that never made its runs bucket recorded none)", bucket, project), nil
		}
		return false, fmt.Sprintf("reading gs://%s for %s: %v", bucket, project, err), err
	}
	if b.ProjectNumber != number {
		return false, fmt.Sprintf("gs://%s belongs to project number %d, not %d (%s), so its contents are not a fact about %s", bucket, b.ProjectNumber, number, gcpProject, project), nil
	}
	if b.Labels[gcp.LabelManaged] != gcp.ManagedValue {
		return false, fmt.Sprintf("gs://%s carries no %s=%s label, so it is not %s's runs bucket", bucket, gcp.LabelManaged, gcp.ManagedValue, project), nil
	}

	resp, err := s.Objects.Get(bucket, MarkerObject).Context(ctx).Download()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && (ge.Code == http.StatusForbidden || ge.Code == http.StatusNotFound) {
			return false, fmt.Sprintf("gs://%s/%s is not there (or cannot be read), so %s was not created by a managed run", bucket, MarkerObject, project), nil
		}
		return false, fmt.Sprintf("reading gs://%s/%s for %s: %v", bucket, MarkerObject, project, err), err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, markerMaxBytes+1))
	if err != nil {
		return false, fmt.Sprintf("reading gs://%s/%s for %s: %v", bucket, MarkerObject, project, err), err
	}
	if len(data) > markerMaxBytes {
		return false, fmt.Sprintf("gs://%s/%s is %d bytes, bigger than any marker (%d), so it is not %s's creation fact", bucket, MarkerObject, len(data), markerMaxBytes, project), nil
	}

	// The object has to be exactly one marker and nothing else: a second
	// value after it, a field this build does not know, a version it does
	// not know — none of them are the fact this package writes, and all of
	// them are refused rather than partly believed.
	var m Marker
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return false, fmt.Sprintf("gs://%s/%s is not a marker: %v", bucket, MarkerObject, err), nil
	}
	if _, err := dec.Token(); err != io.EOF {
		return false, fmt.Sprintf("gs://%s/%s is not a marker: it holds more than one JSON value", bucket, MarkerObject), nil
	}
	if m.Version != MarkerVersion {
		return false, fmt.Sprintf("gs://%s/%s is version %d, and this build knows version %d, so %s is not managed by it", bucket, MarkerObject, m.Version, MarkerVersion, project), nil
	}
	if m.GCPProject != gcpProject {
		return false, fmt.Sprintf("gs://%s/%s names project %s, not %s: the fact is about another project, so %s is not managed", bucket, MarkerObject, m.GCPProject, gcpProject, project), nil
	}
	return true, "", nil
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
