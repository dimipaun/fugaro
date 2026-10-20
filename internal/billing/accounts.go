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
//   - The operator's own Application Default Credentials. The one caller
//     (W4.3) passes the zero Credentials; the struct's Endpoint and HTTPClient
//     fields are the same test seam the sibling constructors carry (see
//     internal/infra/projectcreate.go's NewProjectClients), there so a test
//     can point the call at a fake. Nothing in this package builds an identity
//     of its own: no service account, no impersonation, no token.
//
// Everywhere else the rule stands: no other command lists a caller's billing
// accounts, and none of them prints the account a project is already linked
// to.
//
// One name collision to know about before importing this package: four files
// in this tree already call google.golang.org/api/cloudbilling/v1 "billing"
// (internal/cli/doctor.go, internal/infra/discover.go,
// internal/infra/projectcreate.go, internal/preflight/google.go), and this
// package's import path ends in the same word. A file that needs both — W4.3's
// billing question is one — aliases one of the two; this package's own file
// calls the generated client "cloudbilling" for that reason.
package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	cloudbilling "google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/dimipaun/fugaro/internal/safetext"
	"github.com/dimipaun/fugaro/internal/shellword"
)

// serviceCloudBilling is the API these calls need, spelled as Google's
// SERVICE_DISABLED answer and the gcloud line spell it.
const serviceCloudBilling = "cloudbilling.googleapis.com"

// listPageSize is the page billingAccounts.list is asked for: the API's own
// maximum, and its default.
const listPageSize = 100

// maxPages bounds one walk of billingAccounts.list: 100 pages, 10,000
// accounts. A list longer than that is not a list an operator picks from, and
// an answer that never stops is a bug in the walk's counterpart, not something
// to follow.
const maxPages = 100

// googleOwnedQuotaProject is the project Google bills a call to when the
// credentials name no quota project of their own — gcloud's own default
// project. It is not the operator's, so it is never offered as one to enable
// an API on (see ErrNoQuotaProject); internal/infra/firebase.go treats the
// same answer the same way (NoQuotaProjectProblem).
const googleOwnedQuotaProject = "764086051850"

// setQuotaProjectCommand names a quota project of the operator's own. It is
// what ErrNoQuotaProject carries: there is no enable line to print, because
// the project the API is off on is not theirs.
const setQuotaProjectCommand = "gcloud auth application-default set-quota-project <your-project>"

// billingAccountIDRE is a billing account's ID: three groups of six uppercase
// hexadecimal digits, the shape `gcloud billing projects link
// --billing-account` takes and internal/infra/projectcreate.go's
// billingAccountRE accepts. An ID that is not one is dropped: it ends up in a
// typed confirmation and in a printed gcloud line, so an answer this package
// cannot read is not passed on.
var billingAccountIDRE = regexp.MustCompile(`^[0-9A-F]{6}-[0-9A-F]{6}-[0-9A-F]{6}$`)

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
// lack billing.accounts.list. A 403 that names no reason at all lands here
// too, a bare refusal being most often the missing role. There is nothing to
// retry — no second call, no other credential, no other project — and a caller
// falls back to the guided gcloud line.
type ErrNoPermission struct{}

func (e *ErrNoPermission) Error() string {
	return "you can't list billing accounts: the caller needs billing.accounts.list (roles/billing.viewer, or an owner role, on the billing accounts)"
}

// ErrNoQuotaProject is the Cloud Billing API being off while the credentials
// name no quota project of their own. Google then bills the call to its own
// project (googleOwnedQuotaProject), and the SERVICE_DISABLED answer names
// that project as the consumer. It is never the operator's to enable the API
// on, so this is not an ErrDisabled: SetCommand is the line that gives the
// credentials a quota project of the operator's own, and the caller prints
// that instead of any enable line.
type ErrNoQuotaProject struct {
	SetCommand string
}

func (e *ErrNoQuotaProject) Error() string {
	return "you can't list billing accounts: your credentials name no quota project, so Google bills this call to its own project (" + googleOwnedQuotaProject + "), which is not yours to enable the Cloud Billing API on; name one of your own projects and retry: " + e.SetCommand
}

// ErrRefused is a refusal this package has no answer of its own for: the API
// said no and named a reason that is not a missing permission — the quota
// project rejected, a token without the scope, or a reason this package has
// never seen. Reason is that reason, made safe to print (safetext), and a
// caller falls back to the guided gcloud line.
type ErrRefused struct {
	Reason string
}

func (e *ErrRefused) Error() string {
	if e.Reason == "" {
		return "you can't list billing accounts: the API refused the call"
	}
	return "you can't list billing accounts: the API refused the call (" + e.Reason + ")"
}

// List reads the caller's billing accounts with the credentials creds names
// and returns the open ones, in the order the API answered them. It is one
// walk of billingAccounts.list — one call, plus one per further page, up to
// maxPages — and a refusal is never retried: a caller that cannot read the
// list is told so once, with ErrDisabled, ErrNoQuotaProject, ErrNoPermission
// or ErrRefused.
func List(ctx context.Context, creds Credentials) ([]Account, error) {
	svc, err := newService(ctx, creds)
	if err != nil {
		return nil, err
	}
	var out []Account
	seen := map[string]bool{}
	token := ""
	for page := 1; ; page++ {
		if page > maxPages {
			return nil, fmt.Errorf("listing billing accounts: the API answered more than %d pages", maxPages)
		}
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
			id := strings.TrimPrefix(a.Name, "billingAccounts/")
			if !billingAccountIDRE.MatchString(id) {
				continue
			}
			out = append(out, Account{ID: id, DisplayName: a.DisplayName, Open: a.Open})
		}
		if token = resp.NextPageToken; token == "" {
			return out, nil
		}
		if seen[token] {
			return nil, fmt.Errorf("listing billing accounts: the API repeated the page token %s", safetext.OneLine(token))
		}
		seen[token] = true
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

// listErr types the refusals the wizard has an answer for, and leaves every
// other failure as it came — a 401 among them: credentials that are missing or
// expired are not a missing permission, and the API's own message says which.
func listErr(err error) error {
	var ae *googleapi.Error
	if !errors.As(err, &ae) || ae.Code != http.StatusForbidden {
		return fmt.Errorf("listing billing accounts: %w", err)
	}
	reason, metadata := errorInfo(ae)
	switch reason {
	case reasonServiceDisabled:
		service, _ := metadata["service"].(string)
		consumer, _ := metadata["consumer"].(string)
		quota := strings.TrimPrefix(consumer, "projects/")
		switch {
		case service != serviceCloudBilling || quota == "":
			// An API is off, but not one this package can print an enable line
			// for — another API's answer, or one naming no project at all. It
			// is not a missing permission, so it stays as it came.
			return fmt.Errorf("listing billing accounts: %w", err)
		case quota == googleOwnedQuotaProject:
			// The credentials name no quota project of their own, and Google
			// billed the call to its own. Never an enable line: that project is
			// not the operator's.
			return &ErrNoQuotaProject{SetCommand: setQuotaProjectCommand}
		default:
			return &ErrDisabled{
				QuotaProject:  quota,
				EnableCommand: "gcloud services enable " + serviceCloudBilling + " --project " + shellword.Quote(quota),
			}
		}
	case "", reasonPermissionDenied:
		// The missing permission, and the 403 that names no reason at all: a
		// bare refusal is most often the missing role, and billing.accounts.list
		// is the one this package needs.
		return &ErrNoPermission{}
	default:
		return &ErrRefused{Reason: safetext.OneLine(reason)}
	}
}

// The reasons this package reads out of a refusal's structured detail. Every
// other reason is carried by ErrRefused rather than guessed at.
const (
	reasonServiceDisabled  = "SERVICE_DISABLED"
	reasonPermissionDenied = "IAM_PERMISSION_DENIED"
)

// errorInfo is the reason and metadata of the first google.rpc.ErrorInfo
// detail in a Google error, "" and nil when there is none. It reads the detail
// exactly as internal/preflight/google.go's check of the same API reads it
// (mirrored, not reused, to keep each package's Google surface to the one
// client it needs).
func errorInfo(ae *googleapi.Error) (string, map[string]any) {
	for _, d := range ae.Details {
		m, ok := d.(map[string]any)
		if !ok || m["@type"] != "type.googleapis.com/google.rpc.ErrorInfo" {
			continue
		}
		reason, _ := m["reason"].(string)
		metadata, _ := m["metadata"].(map[string]any)
		return reason, metadata
	}
	return "", nil
}
