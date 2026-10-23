package preflight

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	billing "google.golang.org/api/cloudbilling/v1"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/shellword"
)

const serviceCloudBilling = "cloudbilling.googleapis.com"

// Billing reads project's billing info once. A SERVICE_DISABLED answer
// names the credentials' quota project (friction log #4: a project that
// does not exist yet can't be its own quota project, so a creation run
// checks an existing one, named by the caller, and this is where that
// surfaces); otherwise it reports whether project itself has a linked
// billing account.
//
// crmSvc is read only where that answer comes back, to tell the quota project
// apart from project (billingAPIDisabled): every other path here is the one
// call it documents. The disabled answer is reported in D3's cases (W4.4):
// where the quota project is the project being checked, the fix is this
// project's own to enable and the line says so; where it is another project,
// the fix names that project and who to ask, because turning an API on in a
// project nobody named is not this reader's to do — the same distinction the
// wizard's billing question draws (internal/cli/billing_prompt.go).
func Billing(ctx context.Context, c *billing.APIService, crmSvc *crm.Service, project string) []Check {
	bi, err := c.Projects.GetBillingInfo("projects/" + project).Context(ctx).Do()
	if quota, ok := quotaProjectIfDisabled(err); ok {
		return []Check{billingAPIDisabled(ctx, crmSvc, quota, project)}
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

// billingAPIDisabled is the Cloud Billing API's disabled answer read in D3's
// cases (W4.4). The one thing to settle is whether quota — the project Google's
// answer named — is the project being checked, and Google names that project by
// *number* (projects/123456789012) while project here is an *ID*: the two are
// compared as numbers, the way the wizard's own question compares them
// (internal/cli/billing_prompt.go's billingAPIOff) and the way internal/infra
// compares the same field with the project it asked about (target.consumes, whose
// reading of an ID-spelled consumer is kept here too). Comparing the two strings
// would answer "not this project" every time, which sends a reader to ask an
// owner about their own project — a reader told nothing.
//
// Where the number cannot be read, the check says so and names the quota project
// as the answer did: it has not established that the two projects differ, and
// the line to run is the quota project's either way, because that is the project
// the API is off on.
func billingAPIDisabled(ctx context.Context, crmSvc *crm.Service, quota, project string) Check {
	line := "gcloud services enable cloudbilling.googleapis.com --project "
	// The one wording both readings of "this project" share — the ID spelling
	// above and the number below — so a reworded half cannot drift from the other.
	thisProject := Check{ID: "billing-api",
		Problem: "the Cloud Billing API is disabled on " + project + ", the quota project of your credentials — this project",
		Fix:     line + shellword.Quote(project)}
	if quota == project {
		return thisProject
	}
	num, err := infra.ProjectNumber(ctx, &infra.Clients{CRM: crmSvc}, project)
	switch {
	case err != nil:
		return Check{ID: "billing-api",
			Problem: "the Cloud Billing API is disabled on " + quota + ", the quota project of your credentials; this check could not read project " + project + "'s number, so it cannot say whether that is this project",
			Fix:     line + shellword.Quote(quota)}
	case quota == strconv.FormatUint(num, 10):
		return thisProject
	}
	return Check{ID: "billing-api",
		Problem: "the Cloud Billing API is disabled on " + quota + ", the quota project of your credentials, which is not " + project,
		Fix:     line + shellword.Quote(quota) + "   # on that project: ask an owner of it"}
}

// quotaProjectIfDisabled reports the quota project the Cloud Billing API is
// disabled on, read from err's structured detail exactly as
// infra.CheckFirebaseProject reads the same answer (mirrored here, not
// reused, to keep preflight's Google surface to the one client each check
// needs).
func quotaProjectIfDisabled(err error) (string, bool) {
	var ae *googleapi.Error
	if !errors.As(err, &ae) || ae.Code != http.StatusForbidden {
		return "", false
	}
	for _, d := range ae.Details {
		m, ok := d.(map[string]any)
		if !ok || m["@type"] != "type.googleapis.com/google.rpc.ErrorInfo" || m["reason"] != "SERVICE_DISABLED" {
			continue
		}
		md, _ := m["metadata"].(map[string]any)
		if consumer, _ := md["consumer"].(string); md["service"] == serviceCloudBilling && consumer != "" {
			return strings.TrimPrefix(consumer, "projects/"), true
		}
	}
	return "", false
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
