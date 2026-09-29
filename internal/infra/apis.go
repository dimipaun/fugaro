package infra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/api/googleapi"
	serviceusage "google.golang.org/api/serviceusage/v1"
)

// ServiceResourceManager is the Cloud Resource Manager API, through which
// fugaro init reads the project's number. Terraform enables it with the
// installation's other APIs, but init reads the number before the first
// apply, so on a fresh project init enables it itself (EnableService).
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

// serviceDisabled reports whether err is Google's answer to a call to
// service's API while that API is disabled in the project: a 403 whose
// details carry a google.rpc.ErrorInfo with domain googleapis.com, reason
// SERVICE_DISABLED, and service in its metadata. Only that structured
// detail counts: not the message, and not the legacy accessNotConfigured
// reason, which other refusals share.
//
// The rule for lookups (see absent): a disabled API holds nothing that can
// be read, so its resources count as missing and the plan creates them
// (its apply enables the API first). Any other 403, such as a missing
// permission (IAM_PERMISSION_DENIED), and any 5xx still fail closed.
func serviceDisabled(err error, service string) bool {
	var ae *googleapi.Error
	if !errors.As(err, &ae) || ae.Code != http.StatusForbidden {
		return false
	}
	for _, d := range ae.Details {
		m, ok := d.(map[string]any)
		if !ok || m["@type"] != "type.googleapis.com/google.rpc.ErrorInfo" || m["reason"] != "SERVICE_DISABLED" || m["domain"] != "googleapis.com" {
			continue
		}
		if md, ok := m["metadata"].(map[string]any); ok && md["service"] == service {
			return true
		}
	}
	return false
}

// absent reports whether a lookup's err means the resource isn't there:
// the API's 404, or service's API being disabled in the project (see
// serviceDisabled). An API disabled after its resources were made hides
// them; the plan's create then fails on the existing name, loudly, rather
// than adopting something unchecked.
func absent(err error, service string) bool {
	return notFound(err) || serviceDisabled(err, service)
}

// enablePoll is how often EnableService reads an enable's operation.
var enablePoll = 2 * time.Second

// enableTimeout bounds an enable's wait for its operation.
const enableTimeout = 5 * time.Minute

// EnableService enables service in project through Service Usage, and
// waits for the operation to finish. Enabling an enabled service is a
// no-op that succeeds. Callers may still see SERVICE_DISABLED for a few
// minutes while the enable propagates.
func EnableService(ctx context.Context, c *Clients, project, service string) error {
	if c.ServiceUsage == nil {
		return errors.New("no Service Usage client: endpoints.no_auth is set but endpoints.service_usage is not")
	}
	ctx, cancel := context.WithTimeout(ctx, enableTimeout)
	defer cancel()
	op, err := c.ServiceUsage.Services.Enable("projects/"+project+"/services/"+service, &serviceusage.EnableServiceRequest{}).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("enabling %s in project %s: %w", service, project, err)
	}
	for !op.Done {
		name := op.Name
		select {
		case <-ctx.Done():
			return fmt.Errorf("enabling %s in project %s: waiting for operation %s: %w", service, project, name, ctx.Err())
		case <-time.After(enablePoll):
		}
		if op, err = c.ServiceUsage.Operations.Get(name).Context(ctx).Do(); err != nil {
			return fmt.Errorf("enabling %s in project %s: reading operation %s: %w", service, project, name, err)
		}
	}
	if op.Error != nil {
		return fmt.Errorf("enabling %s in project %s: %s (code %d)", service, project, op.Error.Message, op.Error.Code)
	}
	return nil
}
