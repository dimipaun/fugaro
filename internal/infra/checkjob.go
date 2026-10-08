package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	run "google.golang.org/api/run/v2"
)

// The daily image check job's base, set directly (design image-refresh.md,
// "The check job and Terraform"). fugaro image refresh moves the job's
// container image and FUGARO_CHECK_SPEC's base_images together, to exactly
// what Repo renders from the same local config, so the next init --repo
// plans no change. Terraform's module is unchanged: it cannot ignore one env
// entry, and the bytes it would write are these.

// ErrCheckJobShape marks a check job that is not as fugaro init --repo made
// it: a user error whose fix is init --repo.
var ErrCheckJobShape = errors.New("the check job is not as fugaro init --repo made it")

// checkJobPoll is the wait between reads of a job update's operation; tests
// set it to 0.
var checkJobPoll = 2 * time.Second

const checkJobPolls = 60

// RewriteCheckSpec is the check job's FUGARO_CHECK_SPEC raw and image with
// every base kind the spec names moved to bases[kind] (a kind bases lacks
// keeps its base; a kind the spec does not name is not added). The image
// becomes the new base of the kind whose old base it was. The spec is
// marshalled as Repo marshals it.
func RewriteCheckSpec(raw, image string, bases map[string]string) (spec, newImage string, err error) {
	var s CheckJobSpec
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return "", "", fmt.Errorf("%w: its %s is not a check spec (%v); run fugaro init --repo in the checkout", ErrCheckJobShape, CheckSpecEnv, err)
	}
	kind := ""
	for _, k := range slices.Sorted(maps.Keys(s.BaseImages)) {
		if s.BaseImages[k] == image {
			kind = k
			break
		}
	}
	if kind == "" {
		return "", "", fmt.Errorf("%w: its image %s is none of its spec's base images (%s); run fugaro init --repo in the checkout", ErrCheckJobShape, image, strings.Join(slices.Sorted(maps.Values(s.BaseImages)), ", "))
	}
	for k := range s.BaseImages {
		if b := bases[k]; b != "" {
			s.BaseImages[k] = b
		}
	}
	out, err := json.Marshal(s)
	if err != nil {
		return "", "", err
	}
	return string(out), s.BaseImages[kind], nil
}

// CheckJobUpdate is a planned direct update of a repository's daily check
// job: its container's image and FUGARO_CHECK_SPEC, nothing else.
type CheckJobUpdate struct {
	Job                string
	OldImage, NewImage string
	OldSpec, NewSpec   string
	job                *run.GoogleCloudRunV2Job
}

// Changes reports whether the update changes anything.
func (u *CheckJobUpdate) Changes() bool { return u.OldImage != u.NewImage || u.OldSpec != u.NewSpec }

// PlanCheckJob reads the check job and plans moving it to bases
// (RewriteCheckSpec). It is read-only; nil, nil when there is no such job.
func PlanCheckJob(ctx context.Context, c *Clients, gcpProject, region, job string, bases map[string]string) (*CheckJobUpdate, error) {
	full := "projects/" + gcpProject + "/locations/" + region + "/jobs/" + job
	j, err := c.Run.Projects.Locations.Jobs.Get(full).Context(ctx).Do()
	switch {
	case notFound(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading Cloud Run job %s: %w", job, err)
	}
	if j.Template == nil || j.Template.Template == nil || len(j.Template.Template.Containers) == 0 {
		return nil, fmt.Errorf("%w: job %s has no container; run fugaro init --repo in the checkout", ErrCheckJobShape, job)
	}
	ct := j.Template.Template.Containers[0]
	var raw *run.GoogleCloudRunV2EnvVar
	for _, e := range ct.Env {
		if e.Name == CheckSpecEnv {
			raw = e
		}
	}
	if raw == nil {
		return nil, fmt.Errorf("%w: job %s has no %s; run fugaro init --repo in the checkout", ErrCheckJobShape, job, CheckSpecEnv)
	}
	spec, image, err := RewriteCheckSpec(raw.Value, ct.Image, bases)
	if err != nil {
		return nil, err
	}
	return &CheckJobUpdate{Job: full, OldImage: ct.Image, NewImage: image, OldSpec: raw.Value, NewSpec: spec, job: j}, nil
}

// ApplyCheckJob writes u: the job exactly as read, with only its container's
// image and FUGARO_CHECK_SPEC changed. Cloud Run v2's jobs.patch takes the
// whole job (it has no update mask), so the read object goes back, with the
// read etag: a change made since is refused. It waits for the operation.
func ApplyCheckJob(ctx context.Context, c *Clients, u *CheckJobUpdate) error {
	if !u.Changes() {
		return nil
	}
	ct := u.job.Template.Template.Containers[0]
	ct.Image = u.NewImage
	for _, e := range ct.Env {
		if e.Name == CheckSpecEnv {
			e.Value = u.NewSpec
		}
	}
	op, err := c.Run.Projects.Locations.Jobs.Patch(u.Job, u.job).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("updating Cloud Run job %s (refused if it changed since it was read: rerun): %w", u.Job, err)
	}
	for i := 0; !op.Done; i++ {
		if i == checkJobPolls {
			return fmt.Errorf("updating Cloud Run job %s: not done after %d checks; see gcloud run jobs describe", u.Job, checkJobPolls)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(checkJobPoll):
		}
		if op, err = c.Run.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do(); err != nil {
			return fmt.Errorf("updating Cloud Run job %s: %w", u.Job, err)
		}
	}
	if op.Error != nil {
		return fmt.Errorf("updating Cloud Run job %s: %s", u.Job, op.Error.Message)
	}
	return nil
}
