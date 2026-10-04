package infra

// Creating the GCP project, adding Firebase to it and linking billing
// (M11 task 16, design m11-setup-and-skills.md §3.1 "Project", §14 items 3
// and 6). Everything here is the user's own call, with the user's
// credentials, and nothing here confirms anything: the CLI's project stage
// owns the two typed confirmations (the project's ID, then the billing
// account's), and --yes reaches neither.
//
// The documented path is Cloud Resource Manager v3 projects.create (a
// long-running operation, polled), then Firebase Management v1beta1
// projects.addFirebase (also an operation), then, only when the user passes
// --link-billing and types the account's ID, Cloud Billing
// projects.updateBillingInfo. The parent is set only from --parent; an
// organization is never guessed. These clients set no quota project of their
// own: the project being created cannot pay for its own creation, so the
// calls use the one in the user's Application Default Credentials (or
// GOOGLE_CLOUD_QUOTA_PROJECT), which the confirmation states.
//
// UNVERIFIED against the real services (the design's §14 items 3 and 6;
// the fakes in internal/gcpfake encode what the documentation says, not
// what was observed):
//   - how long a new project takes to become readable (reads answer 403
//     meanwhile: WaitProject retries for a bounded time);
//   - addFirebase's operation polling and the time it takes;
//   - which organization-policy constraints refuse project creation, and the
//     exact text of their errors (surfaced verbatim, with a likely cause);
//   - the billing quota per account (an account linked to its limit of
//     projects refuses; the error is surfaced verbatim);
//   - whether a project ID is reusable or reserved after a failed create,
//     and the full list of restricted words in an ID (ValidateNewProjectID
//     refuses only the ones the documentation names).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	billing "google.golang.org/api/cloudbilling/v1"
	crm3 "google.golang.org/api/cloudresourcemanager/v3"
	firebase "google.golang.org/api/firebase/v1beta1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// ProjectClients are the clients of the project stage: Resource Manager v3,
// Firebase Management and Cloud Billing. No quota project is set on them.
type ProjectClients struct {
	CRM      *crm3.Service
	Firebase *firebase.Service
	Billing  *billing.APIService
}

// ProjectEndpoints override the APIs' roots, so fakes can stand in.
type ProjectEndpoints struct{ ResourceManager, Firebase, Billing string }

// NewProjectClients connects with the user's credentials (noAuth: none, for
// fakes, which then need every endpoint).
func NewProjectClients(ctx context.Context, e ProjectEndpoints, noAuth bool, hc *http.Client) (*ProjectClients, error) {
	if noAuth {
		for name, v := range map[string]string{"resource_manager": e.ResourceManager, "firebase_management": e.Firebase, "cloud_billing": e.Billing} {
			if v == "" {
				return nil, fmt.Errorf("endpoints: no_auth is set but the %s endpoint is not", name)
			}
		}
	}
	opts := func(endpoint string) []option.ClientOption {
		out := []option.ClientOption{option.WithLogger(discardLogger)}
		if endpoint != "" {
			out = append(out, option.WithEndpoint(endpoint))
		}
		if noAuth {
			out = append(out, option.WithoutAuthentication())
		}
		if hc != nil {
			out = append(out, option.WithHTTPClient(hc))
		}
		return out
	}
	var c ProjectClients
	var err error
	if c.CRM, err = crm3.NewService(ctx, opts(e.ResourceManager)...); err != nil {
		return nil, fmt.Errorf("connecting to Resource Manager: %w", err)
	}
	if c.Firebase, err = firebase.NewService(ctx, opts(e.Firebase)...); err != nil {
		return nil, fmt.Errorf("connecting to Firebase Management: %w", err)
	}
	if c.Billing, err = billing.NewService(ctx, opts(e.Billing)...); err != nil {
		return nil, fmt.Errorf("connecting to Cloud Billing: %w", err)
	}
	return &c, nil
}

var (
	// billingAccountRE is a billing account's ID: three groups of six
	// uppercase hexadecimal digits.
	billingAccountRE = regexp.MustCompile(`^[0-9A-F]{6}-[0-9A-F]{6}-[0-9A-F]{6}$`)
	parentRE         = regexp.MustCompile(`^(organizations|folders)/[0-9]{1,20}$`)
	displayNameRE    = regexp.MustCompile(`^[A-Za-z0-9'"!\- ]{4,30}$`)
	// restrictedInID are the words a project ID may not contain.
	restrictedInID = []string{"google", "ssl", "null", "undefined"}
)

// ValidateNewProjectID refuses an ID Google would refuse (6 to 30
// characters of a-z, 0-9 and -, starting with a letter and not ending with a
// hyphen, no restricted word), before any call is made.
func ValidateNewProjectID(id string) error {
	if !ValidProjectID(id) {
		return userErr("%q is not a project ID (6 to 30 characters of a-z, 0-9 and '-', starting with a letter and not ending with '-')", id)
	}
	for _, w := range restrictedInID {
		if strings.Contains(id, w) {
			return userErr("project ID %q contains %q, which Google does not allow in an ID", id, w)
		}
	}
	return nil
}

// ValidateParent accepts organizations/N or folders/N (and nothing else).
func ValidateParent(p string) error {
	if !parentRE.MatchString(p) {
		return userErr("--parent %q is not organizations/<number> or folders/<number>", p)
	}
	return nil
}

// ValidateDisplayName accepts what Google does for a project's name.
func ValidateDisplayName(n string) error {
	if !displayNameRE.MatchString(n) {
		return userErr("--display-name %q must be 4 to 30 letters, digits, spaces, quotes, hyphens or '!'", n)
	}
	return nil
}

// ValidateBillingAccount accepts a billing account's bare ID, such as
// 0123AB-4567CD-89EF01 (no billingAccounts/ prefix).
func ValidateBillingAccount(a string) error {
	if !billingAccountRE.MatchString(a) {
		return userErr("--link-billing %q is not a billing account ID (three groups of six uppercase hex digits, such as 0123AB-4567CD-89EF01)", a)
	}
	return nil
}

// ProjectErrorKind says what a project call's failure means for the stage.
type ProjectErrorKind string

const (
	ProjectTaken   ProjectErrorKind = "taken"        // the ID is held by a project the caller cannot read
	ProjectDeleted ProjectErrorKind = "deleted"      // the project is DELETE_REQUESTED
	ProjectAPIOff  ProjectErrorKind = "api-disabled" // an API is off on the credentials' quota project
	ProjectDenied  ProjectErrorKind = "denied"       // a permission or an organization policy refused
	ProjectOther   ProjectErrorKind = "other"
)

// ProjectError is a failed project call: the service's own text, verbatim,
// and the likely cause and the one-line fix where there is one.
type ProjectError struct {
	Kind ProjectErrorKind
	Err  error
	// Hint is the likely cause; Fix is the one line to run or do.
	Hint, Fix string
}

func (e *ProjectError) Error() string {
	if e.Hint == "" {
		return e.Err.Error()
	}
	return e.Err.Error() + "; likely cause: " + e.Hint
}
func (e *ProjectError) Unwrap() error { return e.Err }

// ProjectInfo is what a read of the project found.
type ProjectInfo struct {
	// Exists is whether the caller can read the project. A project that does
	// not exist and one the caller may not read look the same (Google
	// answers 403 for both), which is why a create's own refusal, not this,
	// says "taken".
	Exists   bool
	State    string // ACTIVE, DELETE_REQUESTED, ...
	Firebase bool
	// BillingKnown is false when the caller cannot read the project's
	// billing; BillingEnabled is meaningful only when it is true. The
	// account the project is linked to is deliberately not read out.
	BillingKnown, BillingEnabled bool
}

// ReadProject reads the project, its Firebase resource and its billing
// state. It changes nothing.
func ReadProject(ctx context.Context, pc *ProjectClients, id string) (ProjectInfo, error) {
	var info ProjectInfo
	p, err := pc.CRM.Projects.Get("projects/" + id).Context(ctx).Do()
	switch {
	case isAbsent(err):
		return info, nil
	case err != nil:
		return info, classify(err, "reading project "+id)
	}
	info.Exists, info.State = true, p.State
	if p.State != "" && p.State != "ACTIVE" {
		return info, nil
	}
	if _, err := pc.Firebase.Projects.Get("projects/" + id).Context(ctx).Do(); err == nil {
		info.Firebase = true
	} else if !isAbsent(err) {
		return info, classify(err, "reading project "+id+"'s Firebase resource")
	}
	bi, err := pc.Billing.Projects.GetBillingInfo("projects/" + id).Context(ctx).Do()
	switch {
	case err == nil:
		info.BillingKnown, info.BillingEnabled = true, bi.BillingEnabled
	case isAbsent(err):
		// Billing cannot be read: unknown, never assumed on or off.
	default:
		return info, classify(err, "reading project "+id+"'s billing")
	}
	return info, nil
}

func isAbsent(err error) bool {
	var ge *googleapi.Error
	if !errors.As(err, &ge) || ge.Code == 0 {
		return false
	}
	if ge.Code == http.StatusNotFound {
		return true
	}
	if ge.Code == http.StatusForbidden {
		_, off := disabledAny(err)
		return !off
	}
	return false
}

// disabledAny is the service a SERVICE_DISABLED answer names and the
// consumer (the credentials' quota project) it names.
func disabledAny(err error) (service string, off bool) {
	var ge *googleapi.Error
	if !errors.As(err, &ge) || ge.Code != http.StatusForbidden {
		return "", false
	}
	for _, d := range ge.Details {
		m, ok := d.(map[string]any)
		if !ok || m["@type"] != "type.googleapis.com/google.rpc.ErrorInfo" || m["reason"] != "SERVICE_DISABLED" {
			continue
		}
		md, _ := m["metadata"].(map[string]any)
		s, _ := md["service"].(string)
		return s, true
	}
	return "", false
}

// classify turns a Google error into a ProjectError, keeping its text.
func classify(err error, what string) error {
	var ge *googleapi.Error
	wrapped := fmt.Errorf("%s: %w", what, err)
	if !errors.As(err, &ge) {
		return &ProjectError{Kind: ProjectOther, Err: wrapped}
	}
	if svc, off := disabledAny(err); off {
		consumer := ""
		for _, d := range ge.Details {
			if m, ok := d.(map[string]any); ok {
				if md, ok := m["metadata"].(map[string]any); ok {
					consumer, _ = md["consumer"].(string)
				}
			}
		}
		quota := strings.TrimPrefix(consumer, "projects/")
		hint := "the " + svc + " API is disabled on the quota project of your credentials"
		fix := "enable it on the quota project of your credentials (gcloud auth application-default set-quota-project <a project of yours>, then gcloud services enable " + svc + " --project <that project>)"
		if quota != "" {
			hint = "the " + svc + " API is disabled on " + quota + ", the quota project of your credentials"
			fix = "gcloud services enable " + svc + " --project " + quota
		}
		return &ProjectError{Kind: ProjectAPIOff, Err: wrapped, Hint: hint, Fix: fix}
	}
	switch ge.Code {
	case http.StatusForbidden, http.StatusBadRequest, http.StatusTooManyRequests:
		return &ProjectError{Kind: ProjectDenied, Err: wrapped}
	}
	return &ProjectError{Kind: ProjectOther, Err: wrapped}
}

// CreateSpec is what the user confirmed.
type CreateSpec struct {
	ID, DisplayName string
	// Parent is organizations/N or folders/N; empty means no parent (the
	// project is created outside any organization). It is never guessed.
	Parent string
}

// Wait is how the operation polls and the reads wait for a new project to
// become readable. Tests replace it.
var Wait = struct {
	Interval time.Duration
	Tries    int
}{Interval: 3 * time.Second, Tries: 100}

func sleep(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(Wait.Interval):
		return nil
	}
}

// Suffix is the random part of the ID suggested when one is taken. Tests
// replace it.
var Suffix = func() string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// SuggestProjectID is an ID near id that is valid and unlikely to be taken:
// id (cut to fit) and a random suffix.
func SuggestProjectID(id string) string {
	suf := Suffix()
	if max := 30 - 1 - len(suf); len(id) > max {
		id = id[:max]
	}
	return strings.TrimRight(id, "-") + "-" + suf
}

// CreateProject creates the project and waits for the operation. An ID held
// by a project the caller cannot read is a ProjectTaken error.
func CreateProject(ctx context.Context, pc *ProjectClients, s CreateSpec) error {
	if err := ValidateNewProjectID(s.ID); err != nil {
		return err
	}
	if s.Parent != "" {
		if err := ValidateParent(s.Parent); err != nil {
			return err
		}
	}
	op, err := pc.CRM.Projects.Create(&crm3.Project{ProjectId: s.ID, DisplayName: s.DisplayName, Parent: s.Parent}).Context(ctx).Do()
	var ge *googleapi.Error
	if errors.As(err, &ge) && ge.Code == http.StatusConflict {
		return &ProjectError{Kind: ProjectTaken, Err: fmt.Errorf("creating project %s: %w", s.ID, err),
			Hint: "the ID is held by another project (possibly one in someone else's account, or one deleted in the last 30 days), and project IDs are global and never reused",
			Fix:  "choose another --gcp-project, such as " + SuggestProjectID(s.ID)}
	}
	if err != nil {
		return orgPolicyHint(classify(err, "creating project "+s.ID))
	}
	for i := 0; !op.Done; i++ {
		if i >= Wait.Tries {
			return &ProjectError{Kind: ProjectOther, Err: fmt.Errorf("creating project %s: the operation %s is not done yet", s.ID, op.Name),
				Fix: "rerun fugaro init --create-project to continue once it is: it adopts the project when it exists"}
		}
		if err := sleep(ctx); err != nil {
			return err
		}
		if op, err = pc.CRM.Operations.Get(op.Name).Context(ctx).Do(); err != nil {
			return classify(err, "waiting for project "+s.ID+"'s creation")
		}
	}
	if op.Error != nil {
		e := fmt.Errorf("creating project %s: %s (code %d)", s.ID, op.Error.Message, op.Error.Code)
		if op.Error.Code == 6 { // ALREADY_EXISTS
			return &ProjectError{Kind: ProjectTaken, Err: e, Hint: "the ID is held by another project"}
		}
		return orgPolicyHint(&ProjectError{Kind: ProjectDenied, Err: e})
	}
	return nil
}

// orgPolicyHint adds the likely cause to a refusal that reads like an
// organization policy or a missing permission; the text stays verbatim.
func orgPolicyHint(err error) error {
	var pe *ProjectError
	if !errors.As(err, &pe) || pe.Kind != ProjectDenied || pe.Hint != "" {
		return err
	}
	m := strings.ToLower(pe.Err.Error())
	switch {
	case strings.Contains(m, "polic") || strings.Contains(m, "constraint"):
		pe.Hint = "an organization policy forbids creating a project here; ask your organization's administrator, or create it under a --parent that allows it"
	default:
		pe.Hint = "you may lack resourcemanager.projects.create (on the --parent organization or folder, or on your account's own organization); ask your administrator"
	}
	return pe
}

// WaitReadable reads the project until it is ACTIVE: a new project answers
// 403 for a while. It gives up after Wait.Tries reads.
func WaitReadable(ctx context.Context, pc *ProjectClients, id string) error {
	for i := 0; ; i++ {
		p, err := pc.CRM.Projects.Get("projects/" + id).Context(ctx).Do()
		switch {
		case err == nil && p.State == "ACTIVE":
			return nil
		case err == nil:
			return &ProjectError{Kind: ProjectOther, Err: fmt.Errorf("project %s is %s, not ACTIVE", id, p.State)}
		case !isAbsent(err):
			return classify(err, "reading project "+id)
		case i >= Wait.Tries:
			return &ProjectError{Kind: ProjectOther, Err: fmt.Errorf("project %s was created but cannot be read yet: %w", id, err),
				Fix: "rerun fugaro init --create-project in a few minutes: it adopts the project once it can be read"}
		}
		if err := sleep(ctx); err != nil {
			return err
		}
	}
}

// AddFirebase adds Firebase to the project and waits for the operation.
func AddFirebase(ctx context.Context, pc *ProjectClients, id string) error {
	op, err := pc.Firebase.Projects.AddFirebase("projects/"+id, &firebase.AddFirebaseRequest{}).Context(ctx).Do()
	if err != nil {
		return classify(err, "adding Firebase to project "+id)
	}
	for i := 0; !op.Done; i++ {
		if i >= Wait.Tries {
			return &ProjectError{Kind: ProjectOther, Err: fmt.Errorf("adding Firebase to project %s: the operation %s is not done yet", id, op.Name),
				Fix: "rerun fugaro init --create-project to continue once it is"}
		}
		if err := sleep(ctx); err != nil {
			return err
		}
		if op, err = pc.Firebase.Operations.Get(op.Name).Context(ctx).Do(); err != nil {
			return classify(err, "waiting for Firebase on project "+id)
		}
	}
	if op.Error != nil {
		return &ProjectError{Kind: ProjectDenied, Err: fmt.Errorf("adding Firebase to project %s: %s (code %d)", id, op.Error.Message, op.Error.Code)}
	}
	return nil
}

// VerifyFirebase refuses unless the project has its Firebase resource.
func VerifyFirebase(ctx context.Context, pc *ProjectClients, id string) error {
	if _, err := pc.Firebase.Projects.Get("projects/" + id).Context(ctx).Do(); err != nil {
		return classify(err, "verifying Firebase on project "+id)
	}
	return nil
}

// LinkBilling links the billing account to the project: the one call that
// can commit the user's money. It never relinks: a project that already has
// billing is the caller's to have refused before this.
func LinkBilling(ctx context.Context, pc *ProjectClients, id, account string) error {
	if err := ValidateBillingAccount(account); err != nil {
		return err
	}
	_, err := pc.Billing.Projects.UpdateBillingInfo("projects/"+id, &billing.ProjectBillingInfo{BillingAccountName: "billingAccounts/" + account}).Context(ctx).Do()
	if err != nil {
		pe, _ := classify(err, "linking billing to project "+id).(*ProjectError)
		if pe != nil && pe.Kind == ProjectDenied && pe.Hint == "" {
			pe.Hint = "you need billing.resourceAssociations.create on the billing account (ask its billing administrator) and the Owner or Project Billing Manager role on the project; or the account is closed, or it has reached its limit of linked projects (a per-account quota)"
		}
		return pe
	}
	return nil
}

// VerifyBilling refuses unless the project reports billing enabled.
func VerifyBilling(ctx context.Context, pc *ProjectClients, id string) error {
	bi, err := pc.Billing.Projects.GetBillingInfo("projects/" + id).Context(ctx).Do()
	if err != nil {
		return classify(err, "verifying project "+id+"'s billing")
	}
	if !bi.BillingEnabled {
		return &ProjectError{Kind: ProjectOther, Err: fmt.Errorf("project %s still reports no billing after the link", id)}
	}
	return nil
}
