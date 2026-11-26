package preflight

import (
	"context"
	"errors"
	"net/http"
	"strings"

	billing "google.golang.org/api/cloudbilling/v1"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/shellword"
)

const serviceCloudBilling = "cloudbilling.googleapis.com"

// Billing reads project's billing info once, billed to project itself (the
// caller names it as the client's quota project). A SERVICE_DISABLED answer
// therefore means the Cloud Billing API is off on project, which this reports
// with the line that turns it on; otherwise it reports whether project has a
// linked billing account.
func Billing(ctx context.Context, c *billing.APIService, project string) []Check {
	bi, err := c.Projects.GetBillingInfo("projects/" + project).Context(ctx).Do()
	if billingAPIOff(err) {
		return []Check{{ID: "billing-api",
			Problem: "the Cloud Billing API is disabled on " + project,
			Fix:     "gcloud services enable cloudbilling.googleapis.com --project " + shellword.Quote(project)}}
	}
	var ge *googleapi.Error
	switch {
	case errors.As(err, &ge) && (ge.Code == http.StatusForbidden || ge.Code == http.StatusNotFound):
		return []Check{{
			ID:      "billing-linked",
			Problem: "you can't read project " + project + "'s billing",
			Fix:     "ask an owner to grant you billing.resourceAssociations.list (or Owner) on the project",
		}}
	case err != nil:
		return []Check{{ID: "billing-linked", Problem: "reading project " + project + "'s billing: " + firstLine(err.Error()), Fix: "check your network and credentials, then retry"}}
	case !bi.BillingEnabled:
		return []Check{{
			ID:      "billing-linked",
			Problem: "project " + project + " has no linked billing account",
			Fix:     "gcloud billing projects link " + shellword.Quote(project) + " --billing-account=ACCOUNT_ID",
		}}
	}
	return []Check{{ID: "billing-linked", OK: true}}
}

// billingAPIOff reports whether err is the Cloud Billing API's
// SERVICE_DISABLED answer, read from its structured detail as
// infra.CheckFirebaseProject reads it (mirrored here, not reused, to keep
// preflight's Google surface to the one client each check needs).
func billingAPIOff(err error) bool {
	var ae *googleapi.Error
	if !errors.As(err, &ae) || ae.Code != http.StatusForbidden {
		return false
	}
	for _, d := range ae.Details {
		m, ok := d.(map[string]any)
		if !ok || m["@type"] != "type.googleapis.com/google.rpc.ErrorInfo" || m["reason"] != "SERVICE_DISABLED" {
			continue
		}
		md, _ := m["metadata"].(map[string]any)
		if consumer, _ := md["consumer"].(string); md["service"] == serviceCloudBilling && consumer != "" {
			return true
		}
	}
	return false
}

// IAMPolicy reads project's IAM policy once (every later stage needs the
// same read) and reports two things from it: whether the caller could read
// it at all (a missing role refuses every later stage, so the fix names
// the role, design §3.1's "ask an owner to run the converge") and, in the
// same-project layout (sameProjectFirebase: the Firebase project is the
// installation's own), whether a default service account holds a
// primitive role (friction log #5).
func IAMPolicy(ctx context.Context, c *crm.Service, project string, sameProjectFirebase bool) []Check {
	req := &crm.GetIamPolicyRequest{Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3}}
	p, err := c.Projects.GetIamPolicy(project, req).Context(ctx).Do()
	if err != nil {
		return []Check{{
			ID:      "iam-policy",
			Problem: "can't read project " + project + "'s IAM policy: " + firstLine(err.Error()),
			Fix:     "ask an owner to grant you roles/resourcemanager.projectIamAdmin (or roles/owner) on " + project + ", or ask them to run fugaro setup",
		}}
	}
	out := []Check{{ID: "iam-policy", OK: true}}
	if !sameProjectFirebase {
		return out
	}
	risks := infra.DefaultAccountRisks(p)
	if len(risks) == 0 {
		return append(out, Check{ID: "default-service-accounts", OK: true})
	}
	return append(out, Check{
		ID:      "default-service-accounts",
		Problem: "the default service account(s) hold a primitive role: " + strings.Join(risks, ", "),
		Fix:     "gcloud projects remove-iam-policy-binding " + shellword.Quote(project) + " --member=serviceAccount:<account> --role=roles/editor",
	})
}
