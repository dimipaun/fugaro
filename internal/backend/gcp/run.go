package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
	run "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/backend"
)

var _ backend.Backend = (*Backend)(nil)

// location is projects/<p>/locations/<r>, the parent of the jobs.
func (b *Backend) location() string {
	return "projects/" + b.o.Project + "/locations/" + b.o.Region
}

// JobPath is the full resource name of the Cloud Run job of (slug, workflow).
func (b *Backend) JobPath(slug, workflow string) string {
	return b.location() + "/jobs/" + JobName(slug, workflow)
}

// canonical rebuilds an execution name with the backend's project ID, so a
// project number the API sends never reaches a caller (plan C-1, I-9).
func (b *Backend) canonical(name string) (backend.ExecID, error) {
	id, ok := backend.ParseExecution(name)
	if !ok {
		return backend.ExecID{}, fmt.Errorf("%q is not a Cloud Run execution name (projects/<p>/locations/<r>/jobs/<j>/executions/<e>)", name)
	}
	id.Project = b.o.Project
	return id, nil
}

// Launch starts one execution of the workflow's job with FUGARO_RUN set.
// It returns once Cloud Run has accepted the execution; the operation it
// returns completes only when the execution ends, so Launch never waits.
func (b *Backend) Launch(ctx context.Context, spec backend.LaunchSpec) (backend.ExecutionRef, error) {
	job := b.JobPath(spec.Repo.Slug, spec.Workflow)
	req := &run.GoogleCloudRunV2RunJobRequest{Overrides: &run.GoogleCloudRunV2Overrides{
		ContainerOverrides: []*run.GoogleCloudRunV2ContainerOverride{{
			Env: []*run.GoogleCloudRunV2EnvVar{{Name: "FUGARO_RUN", Value: spec.Repo.Slug + "/" + spec.RunID}},
		}},
	}}
	op, err := b.run.Projects.Locations.Jobs.Run(job, req).Context(ctx).Do()
	if err != nil {
		return backend.ExecutionRef{}, launchError(job, err)
	}
	var meta struct {
		Name   string `json:"name"`
		LogURI string `json:"logUri"`
	}
	if err := json.Unmarshal(op.Metadata, &meta); err != nil || meta.Name == "" {
		return backend.ExecutionRef{}, fmt.Errorf("Cloud Run started %s but did not report the execution (operation %s)", job, op.Name)
	}
	id, err := b.canonical(meta.Name)
	if err != nil {
		return backend.ExecutionRef{}, fmt.Errorf("Cloud Run started %s (operation %s): %w", job, op.Name, err)
	}
	return backend.ExecutionRef{Name: id.String(), Job: id.Job, LogURL: meta.LogURI}, nil
}

// launchError classifies a jobs.run failure. Only a definitive refusal (a
// 4xx other than 408 and 429) wraps ErrRejected: nothing can have started.
// Anything else, including timeouts and transport errors, is ambiguous.
func launchError(job string, err error) error {
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		switch {
		case ge.Code == http.StatusNotFound:
			return fmt.Errorf("launching: job %s does not exist; create it with the bootstrap (M5: fugaro init) (%v): %w: %w", job, err, backend.ErrNotFound, backend.ErrRejected)
		case ge.Code >= 400 && ge.Code < 500 && ge.Code != http.StatusRequestTimeout && ge.Code != http.StatusTooManyRequests:
			return fmt.Errorf("launching %s: %w: %w", job, backend.ErrRejected, err)
		}
	}
	return fmt.Errorf("launching %s (the execution may have started): %w", job, err)
}

// apiError wraps a Cloud Run error, mapping 404 to ErrNotFound.
func apiError(what string, err error) error {
	var ge *googleapi.Error
	if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
		return fmt.Errorf("%s: %w: %w", what, backend.ErrNotFound, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Execution reads one execution by its full resource name.
func (b *Backend) Execution(ctx context.Context, name string) (backend.Execution, error) {
	id, err := b.canonical(name)
	if err != nil {
		return backend.Execution{}, err
	}
	e, err := b.run.Projects.Locations.Jobs.Executions.Get(id.String()).Context(ctx).Do()
	if err != nil {
		return backend.Execution{}, apiError("reading execution "+id.String(), err)
	}
	return b.toExecution(e)
}

// List returns executions newest first: of every Fugaro job, or of f.Jobs.
func (b *Backend) List(ctx context.Context, f backend.ListFilter) ([]backend.Execution, error) {
	parents := []string{b.location() + "/jobs/-"}
	if len(f.Jobs) > 0 {
		parents = parents[:0]
		for _, j := range f.Jobs {
			parents = append(parents, b.location()+"/jobs/"+j)
		}
	}
	var out []backend.Execution
	for _, parent := range parents {
		got, err := b.list(ctx, parent, len(f.Jobs) == 0, f)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	if len(parents) > 1 {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	}
	return out, nil
}

func (b *Backend) list(ctx context.Context, parent string, onlyFugaro bool, f backend.ListFilter) ([]backend.Execution, error) {
	var out []backend.Execution
	token := ""
	for {
		resp, err := b.run.Projects.Locations.Jobs.Executions.List(parent).PageSize(b.listPageSize).PageToken(token).Context(ctx).Do()
		if err != nil {
			return nil, apiError("listing executions of "+parent, err)
		}
		for _, e := range resp.Executions {
			// The API sorts by creation time, newest first: everything after
			// the first execution older than Since is older too.
			if !f.Since.IsZero() {
				if c, err := parseTime(e.CreateTime); err == nil && !c.IsZero() && c.Before(f.Since) {
					return out, nil
				}
			}
			if id, ok := backend.ParseExecution(e.Name); onlyFugaro && (!ok || !strings.HasPrefix(id.Job, "fugaro-")) {
				continue
			}
			x, err := b.toExecution(e)
			if err != nil {
				return nil, err
			}
			if f.ActiveOnly && x.State.Terminal() {
				continue
			}
			out = append(out, x)
		}
		if resp.NextPageToken == "" {
			return out, nil
		}
		token = resp.NextPageToken
	}
}

// Cancel asks Cloud Run to stop the execution. It does not wait.
func (b *Backend) Cancel(ctx context.Context, name string) error {
	id, err := b.canonical(name)
	if err != nil {
		return err
	}
	if _, err := b.run.Projects.Locations.Jobs.Executions.Cancel(id.String(), &run.GoogleCloudRunV2CancelExecutionRequest{}).Context(ctx).Do(); err != nil {
		return apiError("cancelling execution "+id.String(), err)
	}
	return nil
}

// stateOf maps an execution's times and task counts to a State. An
// execution that ended without a succeeded task and with no failed or
// cancelled one is failed, never succeeded.
func stateOf(e *run.GoogleCloudRunV2Execution) backend.State {
	switch {
	case e.CompletionTime != "" && e.CancelledCount > 0:
		return backend.StateCancelled
	case e.CompletionTime != "" && e.SucceededCount > 0 && e.FailedCount == 0:
		return backend.StateSucceeded
	case e.CompletionTime != "":
		return backend.StateFailed
	case e.StartTime != "" || e.RunningCount > 0:
		return backend.StateRunning
	default:
		return backend.StatePending
	}
}

func (b *Backend) toExecution(e *run.GoogleCloudRunV2Execution) (backend.Execution, error) {
	id, err := b.canonical(e.Name)
	if err != nil {
		return backend.Execution{}, fmt.Errorf("Cloud Run returned an execution: %w", err)
	}
	x := backend.Execution{Name: id.String(), Job: id.Job, State: stateOf(e), LogURL: e.LogUri}
	for _, t := range []struct {
		dst *time.Time
		src string
	}{{&x.Created, e.CreateTime}, {&x.Started, e.StartTime}, {&x.Completed, e.CompletionTime}} {
		if *t.dst, err = parseTime(t.src); err != nil {
			return backend.Execution{}, fmt.Errorf("execution %s: %w", x.Name, err)
		}
	}
	if e.Template != nil && len(e.Template.Containers) > 0 && e.Template.Containers[0].Resources != nil {
		limits := e.Template.Containers[0].Resources.Limits
		if s := limits["cpu"]; s != "" {
			if x.CPU, err = parseCPU(s); err != nil {
				return backend.Execution{}, fmt.Errorf("execution %s: %w", x.Name, err)
			}
		}
		if s := limits["memory"]; s != "" {
			if x.MemoryGiB, err = backend.MemoryGiB(s); err != nil {
				return backend.Execution{}, fmt.Errorf("execution %s: %w", x.Name, err)
			}
		}
	}
	return x, nil
}

// parseTime parses an API timestamp; empty is the zero time.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp %q: %w", s, err)
	}
	return t, nil
}

// parseCPU parses a CPU limit: "4", "0.5" or millicores like "4000m".
func parseCPU(s string) (float64, error) {
	num, div := s, 1.0
	if n, ok := strings.CutSuffix(s, "m"); ok {
		num, div = n, 1000
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || !(v > 0) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("CPU limit %q must look like 4, 0.5 or 4000m", s)
	}
	return v / div, nil
}
