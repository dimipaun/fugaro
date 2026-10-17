package managed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/api/cloudasset/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// The whole-project inventory (Task 4.1, design D59).
//
// Discovery runs the other way. infra.DiscoverInstallation finds Fugaro's own
// resources by label, out of a fixed set of services, so a resource type this
// program has never heard of is invisible to it by construction — and that is
// exactly the case the spec's "single purpose" safeguard is about ("if Fugaro
// finds resources it didn't create, it warns and falls back to
// confirmations"), which the design records as finding 20. The check here
// therefore reads the project whole: through the Cloud Asset API, which
// enumerates a project's resources by type without this program knowing the
// types in advance (design U7).
//
// What it does with a resource the installation's own state does not account
// for is the decision design D59 answered on 2026-10-16: fall back to today's
// confirmations for that run, warn, and say why — never halt, never delete.
// Both halves of that direction are load-bearing. A false positive must not
// stop the operator's own work, because a check that halts on an unrelated
// service in the project is a check the operator turns off; and an inventory
// that cannot be read at all is the fail-closed case, the same direction the
// marker's own read takes (Global Constraint 3).
//
// Nothing here writes anything to the project, and nothing here can: the read
// is an interface whose only method reads.

// ServiceCloudAsset is the Cloud Asset API, which the whole-project read is
// made through. The installation module enables it (apis.tf): a whole-project
// query is the one shape discovery cannot be built out of, because it must see
// resource types nobody has listed yet.
const ServiceCloudAsset = "cloudasset.googleapis.com"

// RoleCloudAssetViewer is the read role the whole-project read needs: the
// narrowest role that lists a project's assets, and a read role, so the
// identity that runs the inventory gains no write from holding it. It is named
// once, here, so the grant, the gate that admits it and the owner-run check
// that exercises it (Check 67a) all refer to one string.
//
// The installation module grants it to the operators (iam.tf's
// operator_cloudasset_viewer): the operator running a managed apply is the
// identity that makes the read. The module grant and this role's entry in
// internal/infra/tf's grantRules landed together, because that table is an
// allowlist — an installation grant of a role the table does not name makes
// fugaro init refuse the installation's own plan. The apply's own credential
// becomes the impersonated deployer account (D75, Task 1.5), and the grant
// that account needs of this role is that task's to make beside the roles the
// account is created with.
const RoleCloudAssetViewer = "roles/cloudasset.viewer"

// inventoryPageSize is how many assets the read asks for per page. The read
// follows the next-page token, so it is a request-size knob rather than a
// bound on what is read.
const inventoryPageSize = 500

// inventoryNamed is how many foreign resources the warning names before it
// counts the rest. A project can hold hundreds of resources no state accounts
// for, and a warning nobody can read to the end is one nobody reads.
const inventoryNamed = 5

// Asset is one resource of the project, as the whole-project read returns it.
type Asset struct {
	// Type is the asset type GCP files it under, e.g.
	// "storage.googleapis.com/Bucket".
	Type string
	// Name is its full resource name, e.g.
	// "//storage.googleapis.com/projects/_/buckets/fugaro-runs-acme".
	Name string
}

// Assets is the whole-project read: the Cloud Asset API in production
// (CloudAssets), a fake in tests. The only method it has reads, so the check
// cannot write to the project it is judging whatever a caller passes.
type Assets interface {
	// List returns everything in gcpProject. A read that cannot be made
	// returns an error, and the caller falls back to confirmations: a check
	// that could not run is never a clean project.
	List(ctx context.Context, gcpProject string) ([]Asset, error)
}

// Inventory is the "single purpose" check of a managed project: everything the
// project holds, against what the installation's own state accounts for.
type Inventory struct {
	// Project is the Fugaro project's name and GCPProject its GCP project ID.
	// They name the project in the warning; GCPProject is what is read.
	Project, GCPProject string
	// Assets is the read. A nil read falls back rather than reading as clean:
	// a check that could not run means ask for confirmation (Global
	// Constraint 3).
	Assets Assets
	// Accounted is what the installation's own state accounts for, by resource
	// name: each one the id the state stores for a resource it manages — the
	// resource's API path ("projects/acme/locations/us-east5/jobs/check"), or
	// its bare name where GCP names the resource that way, as a bucket's is.
	// The caller reads them out of the state it is about to apply into;
	// nothing here reads the state, because what a state accounts for is the
	// caller's to know (the managed apply of Task 3.1 is the first caller).
	//
	// An empty list accounts for nothing, and that is the honest answer for a
	// root whose state holds nothing yet: every resource the read returns is
	// then foreign and the run keeps its confirmations. The safe direction,
	// and not an accident.
	Accounted []string
}

// Cause is why a decision fell back. It is a string so a caller can print it,
// and the causes below are the ones an operator acts differently on: the first
// is the project's own contents, and the rest are the ways the read itself
// fails — three an operator does something about (enable the API, grant the
// role, wait out a quota) and two that are simply "it did not run".
type Cause string

const (
	// CauseForeign is a resource the state does not account for.
	CauseForeign Cause = "a resource the state does not account for"
	// CauseAPIDisabled is the Cloud Asset API not enabled in the project, which
	// fugaro init enables with the installation's other APIs.
	CauseAPIDisabled Cause = "the Cloud Asset API is disabled in the project"
	// CauseRoleMissing is the identity running the read without
	// RoleCloudAssetViewer.
	CauseRoleMissing Cause = "the identity running the read does not hold " + RoleCloudAssetViewer
	// CauseQuota is the read refused for quota.
	CauseQuota Cause = "the read is over its quota"
	// CauseUnreadable is any other failure of the read.
	CauseUnreadable Cause = "the whole-project read failed"
	// CauseNoReader is no read at all: none was given, or there is no project
	// to read.
	CauseNoReader Cause = "there is no whole-project read to make"
)

// Decision is what the inventory decided — a decision rather than an error on
// purpose, because D59's answer is that a foreign resource falls the run back
// to its confirmations and never that it halts.
//
// Fallback is true when the run must keep today's confirmations, and then Why
// is the sentence a person reads: it names the project, the cause, and — for a
// foreign resource — the resources themselves. A decision that does not fall
// back is the zero value: no cause, nothing foreign, and nothing to say.
// Foreign is every resource the state does not account for, in the order the
// read returned them, whether or not the warning could name them all.
type Decision struct {
	Fallback bool
	Cause    Cause
	Why      string
	Foreign  []Asset
}

// Check reads the whole project and decides. It never returns an error: every
// way the read can fail is a fallback with its own cause and its own sentence,
// and the caller's only permitted action on Fallback is to keep this run's
// confirmations (D59, and Global Constraint 3's direction for every read this
// feature makes).
func (in Inventory) Check(ctx context.Context) Decision {
	if in.Assets == nil || in.GCPProject == "" {
		return in.fallback(CauseNoReader, "no whole-project read was made")
	}
	listed, err := in.Assets.List(ctx, in.GCPProject)
	if err != nil {
		return in.fallback(classify(err), err.Error())
	}
	foreign := in.foreign(listed)
	if len(foreign) == 0 {
		return Decision{}
	}
	return Decision{
		Fallback: true,
		Cause:    CauseForeign,
		Foreign:  foreign,
		Why:      in.foreignWhy(foreign),
	}
}

// fallback is a decision that keeps the run's confirmations, with the project,
// the cause and what happens next in one sentence.
func (in Inventory) fallback(cause Cause, detail string) Decision {
	return Decision{Fallback: true, Cause: cause,
		Why: fmt.Sprintf("the whole-project inventory of %s could not be checked: %s (%s); this run falls back to confirmations, and nothing is deleted (D59)",
			in.what(), cause, detail)}
}

// foreign is the listed assets the state does not account for, in the order
// the read returned them.
func (in Inventory) foreign(listed []Asset) []Asset {
	var out []Asset
	for _, a := range listed {
		if !in.accounted(a.Name) {
			out = append(out, a)
		}
	}
	return out
}

// accounted reports whether a listed asset's full resource name is what the
// state accounts for. The state stores a resource by the name GCP knows it by
// — its API path, or its bare name for a resource GCP names that way (a
// bucket) — and the read always answers with the full resource name
// ("//<service>/<path>"), so the comparison is on that path: equal to an
// accounted name, or ending in it at a path boundary.
//
// The boundary is the whole point of the rule: the name has to be the
// resource's own, never a string that merely appears in it, so a bucket called
// not-runs is not accounted for by an id of "runs", and an execution of an
// accounted job is not accounted for by the job. What the rule cannot see is a
// resource GCP names identically to another of a different type; that is the
// one ambiguity a name-level comparison has, and it is why the accounted names
// are the state's own ids rather than anything derived here.
func (in Inventory) accounted(name string) bool {
	path := assetPath(name)
	if path == "" {
		return false
	}
	for _, a := range in.Accounted {
		a = strings.Trim(a, "/")
		if a == "" {
			continue
		}
		if path == a || strings.HasSuffix(path, "/"+a) {
			return true
		}
	}
	return false
}

// assetPath is an asset's full resource name without its "//<service>/"
// prefix, which is the part the state's own ids can be compared with.
func assetPath(name string) string {
	s, ok := strings.CutPrefix(name, "//")
	if !ok {
		return strings.Trim(name, "/")
	}
	_, path, _ := strings.Cut(s, "/")
	return path
}

// foreignWhy is the warning: the project, how many resources the state does
// not account for, the first few of them with their types, and what happens
// next.
func (in Inventory) foreignWhy(foreign []Asset) string {
	named := make([]string, 0, inventoryNamed)
	for _, a := range foreign[:min(len(foreign), inventoryNamed)] {
		named = append(named, a.Name+" ("+a.Type+")")
	}
	more := ""
	if n := len(foreign) - len(named); n > 0 {
		more = fmt.Sprintf(", and %d more", n)
	}
	return fmt.Sprintf("%s holds %d resource(s) the installation's state does not account for: %s%s; this run falls back to confirmations, and nothing is deleted (D59)",
		in.what(), len(foreign), strings.Join(named, ", "), more)
}

// what names the project in the warning, in the shape the gate's own refusals
// use.
func (in Inventory) what() string {
	switch {
	case in.Project != "" && in.GCPProject != "":
		return in.Project + " (" + in.GCPProject + ")"
	case in.Project != "":
		return in.Project
	case in.GCPProject != "":
		return in.GCPProject
	}
	return "this project"
}

// classify says which of the read's failures this is, because the three are
// three different things for an operator to do: enable the API (fugaro init
// does), grant RoleCloudAssetViewer, or wait out a quota. It reads the
// structured answer, not the message, in the discipline internal/infra's own
// serviceDisabled keeps (apis.go): a 403 whose details carry an ErrorInfo with
// reason SERVICE_DISABLED *for this API* is a disabled API, any other 403 is a
// missing permission, and 429 is quota.
//
// The API the detail names has to be the one the read goes through, which is
// the part of that discipline a message-level reading gets wrong: a project can
// have a dozen APIs disabled, and answering "the Cloud Asset API is disabled in
// the project" for a SERVICE_DISABLED about another service is a wrong sentence
// about the operator's project — one that sends them to fugaro init for
// something that is not the read's problem. So a SERVICE_DISABLED naming
// another service is neither this API's answer nor a missing role, and it is
// reported as what it is: the read failed, with the answer's own words in the
// warning. internal/infra's check also matches the consumer project of the
// detail (apis.go's target.consumes); this read cannot, because a detail's
// consumer is the project's *number* and the read knows only its ID — the
// service is the part of the detail it can hold the answer to.
func classify(err error) Cause {
	var ae *googleapi.Error
	if !errors.As(err, &ae) {
		return CauseUnreadable
	}
	switch {
	case ae.Code == http.StatusTooManyRequests:
		return CauseQuota
	case ae.Code != http.StatusForbidden:
		return CauseUnreadable
	}
	otherDisabled := false
	for _, d := range ae.Details {
		m, ok := d.(map[string]any)
		if !ok || m["@type"] != "type.googleapis.com/google.rpc.ErrorInfo" || m["domain"] != "googleapis.com" {
			continue
		}
		switch m["reason"] {
		case "SERVICE_DISABLED":
			if detailService(m) == ServiceCloudAsset {
				return CauseAPIDisabled
			}
			otherDisabled = true
		case "RESOURCE_EXHAUSTED":
			return CauseQuota
		}
	}
	if otherDisabled {
		return CauseUnreadable
	}
	return CauseRoleMissing
}

// detailService is the service an ErrorInfo detail is about, from its
// metadata, in the shape Google's answers carry it (internal/infra's
// serviceDisabled reads the same field).
func detailService(m map[string]any) string {
	md, _ := m["metadata"].(map[string]any)
	s, _ := md["service"].(string)
	return s
}

// CloudAssets is the whole-project read through the Cloud Asset API. It only
// ever reads: SearchAllResources is a GET, and no other call is made.
type CloudAssets struct {
	svc *cloudasset.Service
}

// NewCloudAssets connects to the Cloud Asset API. The identity the returned
// read runs as is the caller's — the one that applies a managed plan — and it
// needs RoleCloudAssetViewer on the project, which is a read role.
func NewCloudAssets(ctx context.Context, opts ...option.ClientOption) (*CloudAssets, error) {
	svc, err := cloudasset.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("connecting to the %s API: %w", ServiceCloudAsset, err)
	}
	return &CloudAssets{svc: svc}, nil
}

// List implements Assets: every resource of gcpProject, page by page, as the
// name and the asset type. The read is the whole project and not a query
// narrowed to types this program knows, which is the point of it (finding 20).
func (c *CloudAssets) List(ctx context.Context, gcpProject string) ([]Asset, error) {
	if c == nil || c.svc == nil {
		return nil, errors.New("no Cloud Asset API client")
	}
	if gcpProject == "" {
		return nil, errors.New("no GCP project to read")
	}
	var out []Asset
	err := c.svc.V1.SearchAllResources("projects/"+gcpProject).
		PageSize(inventoryPageSize).
		Pages(ctx, func(res *cloudasset.SearchAllResourcesResponse) error {
			for _, r := range res.Results {
				out = append(out, Asset{Name: r.Name, Type: r.AssetType})
			}
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("reading project %s through the %s API: %w", gcpProject, ServiceCloudAsset, err)
	}
	return out, nil
}
