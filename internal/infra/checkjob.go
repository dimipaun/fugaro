package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
	run "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
)

// The daily image check job's base, set directly (design image-refresh.md,
// "The check job and Terraform"). fugaro image refresh moves the job's
// container image and FUGARO_CHECK_SPEC's base_images together, to exactly
// what Repo renders from the same local config, so the next init --repo
// plans no change. Terraform's module is unchanged: it cannot ignore one env
// entry, and the bytes it would write are these.
//
// The write is a read-modify-write of the job's raw JSON, never of
// run.GoogleCloudRunV2Job: the Go type drops zero values (omitempty, so
// template.template.maxRetries = 0 would go back absent and the server would
// apply its default of 3) and every field this client version does not
// model, and jobs.patch, which has no update mask, would reset them all.
// So the job goes back exactly as read, every field and number kept as its
// JSON text, except the container's image and FUGARO_CHECK_SPEC's value.

// ErrCheckJobShape marks a check job that is not as fugaro init --repo made
// it: a user error whose fix is init --repo.
var ErrCheckJobShape = errors.New("the check job is not as fugaro init --repo made it")

// checkJobPoll is the wait between reads of a job update's operation; tests
// set it to 0.
var checkJobPoll = 2 * time.Second

const checkJobPolls = 60

// maxJobJSON bounds a jobs get or patch answer read into memory.
const maxJobJSON = 4 << 20

// RewriteCheckSpec is the check job's FUGARO_CHECK_SPEC raw and image with
// every base kind the spec names moved to bases[kind] (a kind bases lacks
// keeps its base; a kind the spec does not name is not added). The image
// becomes the new base of the kind whose old base it was. The spec is
// marshalled as Repo marshals it.
//
// When no base moves, raw and image come back unchanged, whatever raw's
// formatting: a spec that differs from Repo's rendering only in form is not
// a change. When a base moves, raw must be exactly what Repo renders
// (json.Marshal of a CheckJobSpec, byte for byte), so that changing it
// through CheckJobSpec changes base_images and nothing else. A spec with a
// key CheckJobSpec lacks (written by another fugaro version) or in another
// form (edited by hand) is refused with ErrCheckJobShape, telling the
// operator to run init --repo first, rather than silently dropping or
// rewriting what this binary does not understand.
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
	moved := maps.Clone(s.BaseImages)
	for k := range moved {
		if b := bases[k]; b != "" {
			moved[k] = b
		}
	}
	if maps.Equal(moved, s.BaseImages) {
		return raw, image, nil
	}
	strict := json.NewDecoder(strings.NewReader(raw))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&CheckJobSpec{}); err != nil {
		return "", "", fmt.Errorf("%w: its %s has keys this fugaro does not know (%v), so rewriting it would drop them; run fugaro init --repo in the checkout with this fugaro first", ErrCheckJobShape, CheckSpecEnv, err)
	}
	if canon, err := json.Marshal(s); err != nil || string(canon) != raw {
		return "", "", fmt.Errorf("%w: its %s is not in the form fugaro init --repo writes, so it cannot be rewritten as Terraform would render it; run fugaro init --repo in the checkout first", ErrCheckJobShape, CheckSpecEnv)
	}
	s.BaseImages = moved
	out, err := json.Marshal(s)
	if err != nil {
		return "", "", err
	}
	return string(out), moved[kind], nil
}

// CheckJobOwner is the repository whose check job a plan may touch: the
// job must carry its labels and its spec must name it.
type CheckJobOwner struct {
	Repo  string // the repository as the check spec names it (RepoSpec.Name)
	Label string // its gcp.LabelRepo value (RepoSpec.Label)
}

// CheckJobUpdate is a planned direct update of a repository's daily check
// job: its container's image and FUGARO_CHECK_SPEC, nothing else. Only
// PlanCheckJob makes one, and ApplyCheckJob writes it at most once.
type CheckJobUpdate struct {
	job                string
	oldImage, newImage string
	oldSpec, newSpec   string
	body               []byte // the PATCH body: the job as read, image and spec changed; nil without changes
	applied            bool
}

// Job is the job's full resource name.
func (u *CheckJobUpdate) Job() string { return u.job }

// OldImage and NewImage are the container's image before and after.
func (u *CheckJobUpdate) OldImage() string { return u.oldImage }
func (u *CheckJobUpdate) NewImage() string { return u.newImage }

// OldSpec and NewSpec are FUGARO_CHECK_SPEC before and after.
func (u *CheckJobUpdate) OldSpec() string { return u.oldSpec }
func (u *CheckJobUpdate) NewSpec() string { return u.newSpec }

// Changes reports whether the update changes anything.
func (u *CheckJobUpdate) Changes() bool {
	return u != nil && (u.oldImage != u.newImage || u.oldSpec != u.newSpec)
}

// PlanCheckJob reads the check job and plans moving it to bases
// (RewriteCheckSpec). It is read-only; nil, nil when there is no such job.
// The job must be owner's check job as init --repo made it: labelled
// fugaro=managed, fugaro_role=check and fugaro_repo=owner.Label, with
// exactly one container, one plain FUGARO_CHECK_SPEC whose repo is
// owner.Repo, and an etag; anything else is ErrCheckJobShape.
func PlanCheckJob(ctx context.Context, c *Clients, gcpProject, region, job string, owner CheckJobOwner, bases map[string]string) (*CheckJobUpdate, error) {
	if owner.Repo == "" || owner.Label == "" {
		return nil, errors.New("planning the check job: no owner repository")
	}
	full := "projects/" + gcpProject + "/locations/" + region + "/jobs/" + job
	data, err := runJSON(ctx, c, http.MethodGet, full, nil)
	switch {
	case notFound(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading Cloud Run job %s: %w", job, err)
	}
	j, err := decodeJSONObject(data)
	if err != nil {
		return nil, fmt.Errorf("reading Cloud Run job %s: %w", job, err)
	}
	shape := func(format string, a ...any) error {
		return fmt.Errorf("%w: job %s %s; run fugaro init --repo in the checkout", ErrCheckJobShape, job, fmt.Sprintf(format, a...))
	}
	labels := map[string]string{}
	if m, ok := j["labels"].(map[string]any); ok {
		for k, v := range m {
			if s, ok := v.(string); ok {
				labels[k] = s
			}
		}
	}
	want := map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRole: gcp.RoleCheck, gcp.LabelRepo: owner.Label}
	for k, v := range want {
		if labels[k] != v {
			return nil, shape("is not %s's check job by its labels (%s, want %s)", owner.Repo, marks(labels, want), marks(want, want))
		}
	}
	tmpl, _ := j["template"].(map[string]any)
	task, _ := tmpl["template"].(map[string]any)
	cs, _ := task["containers"].([]any)
	if len(cs) != 1 {
		return nil, shape("has %d containers, not one", len(cs))
	}
	ct, _ := cs[0].(map[string]any)
	image, _ := ct["image"].(string)
	if image == "" {
		return nil, shape("has a container with no image")
	}
	envs, _ := ct["env"].([]any)
	var entry map[string]any
	for _, e := range envs {
		if m, ok := e.(map[string]any); ok && m["name"] == CheckSpecEnv {
			if entry != nil {
				return nil, shape("has %s twice", CheckSpecEnv)
			}
			entry = m
		}
	}
	if entry == nil {
		return nil, shape("has no %s", CheckSpecEnv)
	}
	old, ok := entry["value"].(string)
	if _, src := entry["valueSource"]; !ok || src {
		return nil, shape("has a %s that is not a plain value", CheckSpecEnv)
	}
	var s struct {
		Repo string `json:"repo"`
	}
	if err := json.Unmarshal([]byte(old), &s); err != nil {
		return nil, shape("has a %s that is not a check spec (%v)", CheckSpecEnv, err)
	}
	if s.Repo != owner.Repo {
		return nil, shape("checks repository %q, not %s", s.Repo, owner.Repo)
	}
	spec, newImage, err := RewriteCheckSpec(old, image, bases)
	if err != nil {
		return nil, err
	}
	u := &CheckJobUpdate{job: full, oldImage: image, newImage: newImage, oldSpec: old, newSpec: spec}
	if !u.Changes() {
		return u, nil
	}
	if etag, _ := j["etag"].(string); etag == "" {
		return nil, fmt.Errorf("reading Cloud Run job %s: it has no etag, so a concurrent change could not be refused", job)
	}
	ct["image"] = newImage
	entry["value"] = spec
	if u.body, err = json.Marshal(j); err != nil {
		return nil, err
	}
	return u, nil
}

// ApplyCheckJob writes u: the job exactly as read, with only its container's
// image and FUGARO_CHECK_SPEC changed. Cloud Run v2's jobs.patch takes the
// whole job (it has no update mask), so the read JSON goes back, with the
// read etag: a change made since is refused. It waits for the operation. A
// plan not made by PlanCheckJob, or already applied, is refused.
func ApplyCheckJob(ctx context.Context, c *Clients, u *CheckJobUpdate) error {
	if u == nil || u.job == "" {
		return errors.New("updating the check job: not a plan from PlanCheckJob")
	}
	if !u.Changes() {
		return nil
	}
	if u.applied {
		return fmt.Errorf("updating Cloud Run job %s: this plan was already applied; plan again", u.job)
	}
	if u.body == nil {
		return errors.New("updating the check job: not a plan from PlanCheckJob")
	}
	u.applied = true
	data, err := runJSON(ctx, c, http.MethodPatch, u.job, u.body)
	if err != nil {
		var ae *googleapi.Error
		if errors.As(err, &ae) && (ae.Code == http.StatusConflict || ae.Code == http.StatusPreconditionFailed) {
			return fmt.Errorf("updating Cloud Run job %s: it changed since it was read; rerun: %w", u.job, err)
		}
		return fmt.Errorf("updating Cloud Run job %s: %w", u.job, err)
	}
	var op run.GoogleLongrunningOperation
	if err := json.Unmarshal(data, &op); err != nil {
		return fmt.Errorf("updating Cloud Run job %s: reading its operation: %w", u.job, err)
	}
	for i := 0; !op.Done; i++ {
		if i == checkJobPolls {
			return fmt.Errorf("updating Cloud Run job %s: not done after %d checks; see gcloud run jobs describe", u.job, checkJobPolls)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(checkJobPoll):
		}
		next, err := c.Run.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("updating Cloud Run job %s: %w", u.job, err)
		}
		op = *next
	}
	if op.Error != nil {
		return fmt.Errorf("updating Cloud Run job %s: %s", u.job, op.Error.Message)
	}
	return nil
}

// runJSON sends a raw Cloud Run Admin v2 call on name, through the client
// and endpoint the typed c.Run uses, and returns the answer's body. A
// non-2xx answer is a *googleapi.Error, as from a typed call.
func runJSON(ctx context.Context, c *Clients, method, name string, body []byte) ([]byte, error) {
	if c.RunHTTP == nil || c.Run == nil {
		return nil, errors.New("no Cloud Run client")
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, googleapi.ResolveRelative(c.Run.BasePath, "v2/"+name)+"?alt=json&prettyPrint=false", rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.RunHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if err := googleapi.CheckResponse(res); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxJobJSON+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxJobJSON {
		return nil, fmt.Errorf("the answer is over %d bytes", maxJobJSON)
	}
	return data, nil
}

// decodeJSONObject decodes a JSON object without loss: numbers stay
// json.Number, so they go back as the same text.
func decodeJSONObject(data []byte) (map[string]any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		return nil, err
	}
	if d.More() {
		return nil, errors.New("trailing data after the job")
	}
	if m == nil {
		return nil, errors.New("not a job")
	}
	return m, nil
}
