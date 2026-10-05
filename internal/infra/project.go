package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// ProjectMarkerObject is the object in the runs bucket that names the
// installation's Fugaro project. The installation's Terraform writes it,
// beside the bucket's fugaro_project label and the project_name output;
// launchers can't read a label, so every cloud command reads this object
// to check its project config (design §2.5). It is a safety label, not a
// boundary: no job account can write it, but a launcher could.
const ProjectMarkerObject = "fugaro/project.json"

// ProjectMarker is ProjectMarkerObject's content.
type ProjectMarker struct {
	Version    int    `json:"version"`
	Name       string `json:"name"`
	GCPProject string `json:"gcp_project"`
}

// markerMaxBytes bounds the marker read: the object is a few dozen bytes, and
// anyone holding objectAdmin on the bucket could replace it.
const markerMaxBytes = 4 << 10

// ReadProjectMarker reads the runs bucket's fugaro/project.json, read-only,
// but only from a bucket that belongs to the project: the bucket's own project
// number must be projectNumber and it must carry Fugaro's managed label. A
// bucket name is global, so a mark proves nothing by itself: anyone can make a
// bucket with a perfect marker in a project of their own.
// It returns nil, nil for no marker (no bucket, no object, no access, or an
// object that is not a version 1 marker): "not marked" is an answer. Any other
// failure is an error; whoever asks (init's one confirmation) treats both as
// "not Fugaro's", failing closed.
func ReadProjectMarker(ctx context.Context, c *Clients, bucket string, projectNumber uint64) (*ProjectMarker, error) {
	if bucket == "" || projectNumber == 0 || c == nil || c.Storage == nil {
		return nil, nil
	}
	b, err := c.Storage.Buckets.Get(bucket).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && (ge.Code == http.StatusNotFound || ge.Code == http.StatusForbidden) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading bucket gs://%s: %w", bucket, err)
	}
	if b.ProjectNumber != projectNumber || b.Labels[gcp.LabelManaged] != gcp.ManagedValue {
		return nil, nil
	}
	resp, err := c.Storage.Objects.Get(bucket, ProjectMarkerObject).Context(ctx).Download()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && (ge.Code == http.StatusNotFound || ge.Code == http.StatusForbidden) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading gs://%s/%s: %w", bucket, ProjectMarkerObject, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, markerMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading gs://%s/%s: %w", bucket, ProjectMarkerObject, err)
	}
	var m ProjectMarker
	if len(data) > markerMaxBytes || json.Unmarshal(data, &m) != nil || m.Version != 1 || m.Name == "" || m.GCPProject == "" {
		return nil, nil
	}
	return &m, nil
}
