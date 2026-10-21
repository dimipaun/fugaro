package infra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
	serviceusage "google.golang.org/api/serviceusage/v1"
)

// ServiceResourceManager is the Cloud Resource Manager API, through which
// fugaro setup reads the project's number. Terraform enables it with the
// installation's other APIs, but setup reads the number before the first
// apply, so on a fresh project setup enables it itself (EnableService).
const ServiceResourceManager = "cloudresourcemanager.googleapis.com"

// The APIs discovery and the readiness gates read, by service name.
const (
	serviceStorage          = "storage.googleapis.com"
	serviceIAM              = "iam.googleapis.com"
	serviceArtifactRegistry = "artifactregistry.googleapis.com"
	serviceRun              = "run.googleapis.com"
	serviceSecretManager    = "secretmanager.googleapis.com"
	serviceLogging          = "logging.googleapis.com"
	serviceScheduler        = "cloudscheduler.googleapis.com"
)

// ServiceDisabledError is a call refused because its API is disabled in
// the project (Google's SERVICE_DISABLED).
type ServiceDisabledError struct {
	Service, Project string
	Err              error
}

func (e *ServiceDisabledError) Error() string {
	return fmt.Sprintf("the %s API is disabled in project %s: %v", e.Service, e.Project, e.Err)
}

func (e *ServiceDisabledError) Unwrap() error { return e.Err }

// target is the project a lookup reads, for matching a SERVICE_DISABLED
// answer's consumer. number is 0 until the project's number is known.
type target struct {
	project string
	number  uint64
}

// consumes reports whether consumer (ErrorInfo metadata, projects/<number>
// in Google's answers) is the target project. Before its number is known
// (the read of the number itself) any projects/ consumer passes: the
// quota project is always the target project, so that is the target.
func (t target) consumes(consumer string) bool {
	id, ok := strings.CutPrefix(consumer, "projects/")
	switch {
	case !ok || id == "":
		return false
	case id == t.project:
		return true
	case t.number == 0:
		return true
	}
	return id == strconv.FormatUint(t.number, 10)
}

// serviceDisabled reports whether err is Google's answer to a call to
// service's API while that API is disabled in the target project: a 403
// whose details carry a google.rpc.ErrorInfo with domain googleapis.com,
// reason SERVICE_DISABLED, service in its metadata, and the target project
// as its consumer. Only that structured detail counts: not the message,
// and not the legacy accessNotConfigured reason, which other refusals
// share.
//
// The rule for lookups (see absent): a disabled API holds nothing that can
// be read, so its resources count as missing and the plan creates them
// (its apply enables the API first). Any other 403, such as a missing
// permission (IAM_PERMISSION_DENIED), and any 5xx still fail closed.
func serviceDisabled(err error, service string, t target) bool {
	var ae *googleapi.Error
	if !errors.As(err, &ae) || ae.Code != http.StatusForbidden {
		return false
	}
	for _, d := range ae.Details {
		m, ok := d.(map[string]any)
		if !ok || m["@type"] != "type.googleapis.com/google.rpc.ErrorInfo" || m["reason"] != "SERVICE_DISABLED" || m["domain"] != "googleapis.com" {
			continue
		}
		md, _ := m["metadata"].(map[string]any)
		consumer, _ := md["consumer"].(string)
		if md["service"] == service && t.consumes(consumer) {
			return true
		}
	}
	return false
}

// absent reports whether a lookup's err means the resource isn't there:
// the API's 404, or service's API being disabled in the target project
// (see serviceDisabled). An API disabled after its resources were made
// hides them; the plan's create then fails on the existing name, loudly,
// rather than adopting something unchecked.
//
// It is for lookups of what the plan would create. The state bucket, the
// Terraform backend, is not one: there a disabled Storage API is an
// environment error (CheckStateBucket).
func absent(err error, service string, t target) bool {
	return notFound(err) || serviceDisabled(err, service, t)
}

// enablePoll is how often EnableService reads an enable's operation.
var enablePoll = 2 * time.Second

// enableTimeout bounds an enable's wait for its operation.
const enableTimeout = 5 * time.Minute

// enableDeactivatingRetries are the waits between attempts at an enable
// that fails because the service is still being turned off. Enabling a
// service whose disable has not finished is refused until it has, and the
// compute backend's stage enables Compute, which an operator may have just
// disabled: measured 2026-10-15 (Check 57a's setup, design compute-backend
// §7.2, §8.1 task 6), a Compute disable/re-enable pair fails with
// "currently being deactivated" and then succeeds. Tests replace them.
var enableDeactivatingRetries = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second, 30 * time.Second}

// beingDeactivated reports whether err is Google's answer to enabling a
// service whose disable is still running. It is matched on the 400 and that
// one phrase because that is what the measurement recorded: unlike a
// disabled API's refusal (serviceDisabled), this one carries no ErrorInfo
// reason to match on, so the message is the whole of the evidence. A false
// positive costs a bounded wait and one more enable, which is why the loose
// match is acceptable here and not in serviceDisabled, where it would hide
// a real failure.
func beingDeactivated(err error) bool {
	var ae *googleapi.Error
	return errors.As(err, &ae) && ae.Code == http.StatusBadRequest &&
		strings.Contains(ae.Message, "is currently being deactivated")
}

// EnableService enables service in project through Service Usage, and
// waits for the operation to finish. Enabling an enabled service is a
// no-op that succeeds, and an enable refused because the service is still
// being deactivated is retried for a few minutes (enableDeactivatingRetries).
// Callers may still see SERVICE_DISABLED for a few minutes while the enable
// propagates.
func EnableService(ctx context.Context, c *Clients, project, service string) error {
	if c.ServiceUsage == nil {
		return errors.New("no Service Usage client: endpoints.no_auth is set but endpoints.service_usage is not")
	}
	ctx, cancel := context.WithTimeout(ctx, enableTimeout)
	defer cancel()
	var op *serviceusage.Operation
	for attempt := 0; ; attempt++ {
		var err error
		op, err = c.ServiceUsage.Services.Enable("projects/"+project+"/services/"+service, &serviceusage.EnableServiceRequest{}).Context(ctx).Do()
		if err == nil {
			break
		}
		if !beingDeactivated(err) || attempt >= len(enableDeactivatingRetries) {
			return fmt.Errorf("enabling %s in project %s: %w", service, project, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("enabling %s in project %s: %w", service, project, err)
		case <-time.After(enableDeactivatingRetries[attempt]):
		}
	}
	for !op.Done {
		name := op.Name
		select {
		case <-ctx.Done():
			return fmt.Errorf("enabling %s in project %s: waiting for operation %s: %w", service, project, name, ctx.Err())
		case <-time.After(enablePoll):
		}
		var err error
		if op, err = c.ServiceUsage.Operations.Get(name).Context(ctx).Do(); err != nil {
			return fmt.Errorf("enabling %s in project %s: reading operation %s: %w", service, project, name, err)
		}
	}
	if op.Error != nil {
		return fmt.Errorf("enabling %s in project %s: %s (code %d)", service, project, op.Error.Message, op.Error.Code)
	}
	return nil
}
