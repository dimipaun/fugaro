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
	// MarkerVersion is the only version this package reads or writes.
	MarkerVersion = 1
)

// Marker is what MarkerObject holds: the fact, and the project it is a fact
// about. Nothing else — the value is never a credential, and the project it
// names is what keeps a copy of the object in another bucket from being
// believed (with the bucket's own project number, which is the read's job
// and internal/infra/project.go's rule: a bucket name is global).
type Marker struct {
	Version    int    `json:"version"`
	GCPProject string `json:"gcp_project"`
}

// Bytes is the object's content. A marker that does not say which project
// it is about is not one and is refused rather than written.
func (m Marker) Bytes() ([]byte, error) {
	if m.Version != MarkerVersion {
		return nil, fmt.Errorf("%s: version %d is not the one this package writes (%d)", MarkerObject, m.Version, MarkerVersion)
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
// The caller warns rather than fails when this returns an error: the
// project exists either way, an unmarked project is the fail-closed
// direction (nothing is skipped for it), and the owner-run flagging of an
// existing project (D58, Task 6.1) is the way back.
func WriteMarker(ctx context.Context, s *storage.Service, bucket, created, project string, number uint64) error {
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
	data, err := Marker{Version: MarkerVersion, GCPProject: project}.Bytes()
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
