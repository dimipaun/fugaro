// Package billing reads the Cloud Billing accounts an operator can link a
// project to, so the setup wizard can offer them by number instead of asking
// the operator to look the ID up. It is the one place in Fugaro that asks for
// a caller's billing accounts.
//
// It is a deliberate, narrow reversal of the rule
// internal/infra/projectcreate.go writes down for the project stage ("init
// does not list the caller's billing accounts (billingAccounts.list) even to
// guide, because that reveals every account the user can reach and the
// account is theirs to name"). The owner reversed it for the wizard, and the
// reversal is bounded by all of the following. A change to any of them is a
// change to the privacy story, not to this package's convenience:
//
//   - List is called from exactly one place: internal/cli's billing question,
//     and only where canType() says yes — a real terminal, not
//     --non-interactive, not --json, and not a coding agent's session. Under
//     any of those the wizard prints the guided `gcloud billing projects link`
//     line and asks this package nothing.
//   - Open accounts only. A closed account cannot be linked, so it is not
//     offered, and nothing about it is returned.
//   - An account's ID and display name, and nothing else: no spend, no
//     currency, no budget, no IAM policy — and never the account a project is
//     already linked to, which is a different permission and is not read
//     here.
//   - No cache, no file, no log. The list lives in List's return value and
//     nowhere else, and the client is built with the Google SDK's logger
//     discarded (discardLogger), so a body full of account names cannot reach
//     stderr when GOOGLE_SDK_GO_LOGGING_LEVEL is set.
//   - The operator's own Application Default Credentials: no service account,
//     no impersonated identity, no token of this package's own, and no way for
//     a caller to hand it another one.
//
// Everywhere else the rule stands: no other command lists a caller's billing
// accounts, and none of them prints the account a project is already linked
// to.
package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	cloudbilling "google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/internal/shellword"
)

// serviceCloudBilling is the API these calls need, spelled as Google's
// SERVICE_DISABLED answer and the gcloud line spell it.
const serviceCloudBilling = "cloudbilling.googleapis.com"

// listPageSize is the page billingAccounts.list is asked for: the API's own
// maximum, and its default.
const listPageSize = 100

// discardLogger keeps the client from logging requests and their answers.
// Without it a Google client logs the response body when
// GOOGLE_SDK_GO_LOGGING_LEVEL is set, and here that body is the whole list of
// the caller's accounts.
var discardLogger = slog.New(slog.DiscardHandler)

// Account is one billing account the caller may link a project to. ID is the
// account's ID, the shape `gcloud billing projects link --billing-account`
// takes; DisplayName is the name its owner gave it; and Open is whether it can
// still be linked — true for every account List returns, carried so a caller
// reads the flag rather than assuming it.
type Account struct {
	ID          string
	DisplayName string
	Open        bool
}

// Credentials is the identity List asks with, and where the call goes. The
// zero value is the operator's own Application Default Credentials against
// Google's own Cloud Billing endpoint, and that is what the wizard passes:
// this package has no other identity.
type Credentials struct {
	// Endpoint overrides the API's root, so a fake can stand in. Empty is
	// Google's own.
	Endpoint string
	// HTTPClient, when set, is the transport the calls go through. Nil is the
	// default one.
	HTTPClient *http.Client
	// NoAuth makes the calls carry no credentials at all. Only a test sets it,
	// and only with Endpoint: the pair is what keeps a credential-less call
	// from leaving for Google, and newService refuses the combination without
	// one.
	NoAuth bool
}

// ErrDisabled is the Cloud Billing API being off on the credentials' quota
// project. QuotaProject is that project — the credentials' own, never the
// project being set up — and EnableCommand is the exact line that turns the
// API on there, so a caller prints either without re-deriving them.
type ErrDisabled struct {
	QuotaProject  string
	EnableCommand string
}

func (e *ErrDisabled) Error() string {
	return "the Cloud Billing API is disabled on " + e.QuotaProject + ", the quota project of your credentials; enable it there and retry: " + e.EnableCommand
}

// ErrNoPermission is the caller not being allowed to read the accounts: they
// lack billing.accounts.list. There is nothing to retry — no second call, no
// other credential, no other project — and a caller falls back to the guided
// gcloud line.
type ErrNoPermission struct{}

func (e *ErrNoPermission) Error() string {
	return "you can't list billing accounts: the caller needs billing.accounts.list (roles/billing.viewer, or an owner role, on the billing accounts)"
}

// List reads the caller's billing accounts with the credentials creds names
// and returns the open ones, in the order the API answered them. It is one
// walk of billingAccounts.list — one call, plus one per further page — and a
// refusal is never retried: a caller that cannot read the list is told so once,
// with ErrDisabled or ErrNoPermission.
func List(ctx context.Context, creds Credentials) ([]Account, error) {
	svc, err := newService(ctx, creds)
	if err != nil {
		return nil, err
	}
	var out []Account
	for token := ""; ; {
		call := svc.BillingAccounts.List().PageSize(listPageSize)
		if token != "" {
			call = call.PageToken(token)
		}
		resp, err := call.Context(ctx).Do()
		if err != nil {
			return nil, listErr(err)
		}
		for _, a := range resp.BillingAccounts {
			if a == nil || !a.Open {
				continue
			}
			out = append(out, Account{ID: strings.TrimPrefix(a.Name, "billingAccounts/"), DisplayName: a.DisplayName, Open: a.Open})
		}
		if token = resp.NextPageToken; token == "" {
			return out, nil
		}
	}
}

// newService connects to Cloud Billing with creds. It is the same shape as the
// other clients' constructors (internal/infra/projectcreate.go's
// NewProjectClients): the logger discarded, an endpoint a fake can stand in at,
// no credentials when creds says so, and otherwise whatever the Application
// Default Credentials are — including their own quota project, which the Cloud
// Billing API needs and which is never the project being set up.
func newService(ctx context.Context, creds Credentials) (*cloudbilling.APIService, error) {
	if creds.NoAuth && creds.Endpoint == "" {
		// Without this, NoAuth with no endpoint is a call to Google with no
		// credentials — refused, but a call all the same. The sibling
		// constructors refuse the same combination for the same reason.
		return nil, errors.New("credentials: no_auth is set but the endpoint is not")
	}
	opts := []option.ClientOption{option.WithLogger(discardLogger)}
	if creds.Endpoint != "" {
		opts = append(opts, option.WithEndpoint(creds.Endpoint))
	}
	if creds.NoAuth {
		opts = append(opts, option.WithoutAuthentication())
	}
	if creds.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(creds.HTTPClient))
	}
	svc, err := cloudbilling.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Billing: %w", err)
	}
	return svc, nil
}

// listErr types the two refusals the wizard has an answer for, and leaves
// every other failure as it came — a 401 among them: credentials that are
// missing or expired are not a missing permission, and the API's own message
// says which.
func listErr(err error) error {
	if quota, ok := quotaProjectIfDisabled(err); ok {
		return &ErrDisabled{
			QuotaProject:  quota,
			EnableCommand: "gcloud services enable " + serviceCloudBilling + " --project " + shellword.Quote(quota),
		}
	}
	var ge *googleapi.Error
	if errors.As(err, &ge) && ge.Code == http.StatusForbidden {
		return &ErrNoPermission{}
	}
	return fmt.Errorf("listing billing accounts: %w", err)
}

// quotaProjectIfDisabled reports the quota project the Cloud Billing API is
// disabled on, read from err's structured detail exactly as
// internal/preflight/google.go's check of the same API reads it (mirrored, not
// reused, to keep each package's Google surface to the one client it needs).
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
