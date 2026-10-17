package managed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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
// follows the next-page token, so it is a request-size knob; the bounds on
// what is read are inventoryMaxPages and inventoryMaxAssets.
const inventoryPageSize = 500

// The read's bounds. The SDK's own page loop follows whatever next-page token
// the server answers with and never notices one it has already followed, so a
// misbehaving server, or a project far larger than any a managed project is,
// would loop or grow memory until the context ends — and an apply whose
// context has no deadline would hang, which D59's "never halt" rules out. Each
// bound ends the read with an error, and an error falls the run back to its
// confirmations; none of them reads as clean.
const (
	// inventoryMaxPages is the most pages one read follows: at
	// inventoryPageSize per page, room for ten times inventoryMaxAssets even
	// when a server answers with short pages.
	inventoryMaxPages = 400
	// inventoryMaxAssets is the most assets one read holds. A managed project
	// is single-purpose, so its listing is in the hundreds; a listing past this
	// is not a project the check can vouch for.
	inventoryMaxAssets = 20000
	// inventoryTimeout is the deadline the check applies when its caller's
	// context has none, so the read ends whatever the server does.
	inventoryTimeout = 2 * time.Minute
)

// withInventoryDeadline is ctx with inventoryTimeout applied when it carries no
// deadline of its own; a caller's own deadline is kept as it is.
func withInventoryDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, inventoryTimeout)
}

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
	// Accounted is what the installation's own state accounts for: each one a
	// resource the state manages, by the asset type the Cloud Asset API files
	// it under and the name the state stores for it. The caller reads them out
	// of the state it is about to apply into; nothing here reads the state,
	// because what a state accounts for is the caller's to know (the managed
	// apply of Task 3.1 is the first caller).
	//
	// An empty list accounts for nothing, and that is the honest answer for a
	// root whose state holds nothing yet: every resource the read returns is
	// then foreign and the run keeps its confirmations. The safe direction,
	// and not an accident.
	Accounted []StateResource
}

// StateResource is one resource the installation's state accounts for, in the
// terms the whole-project read answers in, so the two can be compared without
// guessing.
//
// Type is the Cloud Asset type ("run.googleapis.com/Job"), and it is required:
// an entry without one accounts for nothing, because a name alone cannot tell
// a bucket from an instance that happens to share it.
//
// Name is one of three shapes, and only these three:
//   - the full resource name, "//run.googleapis.com/projects/p/locations/l/jobs/j",
//     compared whole;
//   - the API path, "projects/p/locations/l/jobs/j", compared whole against
//     the asset's path — the project and location segments are part of it, so
//     the same job in another project or another location is not this one;
//   - a bare name with no "/", for a resource GCP names that way (a bucket, a
//     service account), which accounts only for a top-level resource of the
//     inventory's own project: "//<service>/<name>" or
//     "projects/<this project or _>/<collection>/<name>".
//
// A partial path ("jobs/check") is none of these and accounts for nothing: it
// names no project and no location, so it would account for the same name
// anywhere — the safe direction is that it falls back.
type StateResource struct {
	Type, Name string
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
	// CauseEmpty is a read that answered with nothing. A real project always
	// lists at least itself and the services it has enabled, so an empty answer
	// is a read that went wrong, never a clean project.
	CauseEmpty Cause = "the whole-project read returned no resources at all"
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
	ctx, cancel := withInventoryDeadline(ctx)
	defer cancel()
	listed, err := in.Assets.List(ctx, in.GCPProject)
	if err != nil {
		return in.fallback(classify(err), err.Error())
	}
	if len(listed) == 0 {
		return in.fallback(CauseEmpty, "a real project lists at least itself and its enabled services")
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
		if !in.accounted(a) {
			out = append(out, a)
		}
	}
	return out
}

// accounted reports whether a listed asset is a resource the state accounts
// for: the same asset type, and the same whole path — never a name that merely
// ends the asset's path. The read always answers with the full resource name
// ("//<service>/<path>"); an entry matches it in one of the three shapes
// StateResource names, and in no other.
//
// The rule is deliberately narrow, because the cost of each direction differs:
// a resource wrongly accounted for is one the run applies next to without a
// confirmation, and a resource wrongly left foreign is one confirmation. So a
// name filed under another type, a job of the same name in another project or
// location, an execution under an accounted job, and a partial path that names
// no project are all foreign.
func (in Inventory) accounted(asset Asset) bool {
	path := assetPath(asset.Name)
	if path == "" || asset.Type == "" {
		return false
	}
	for _, r := range in.Accounted {
		if r.Type != asset.Type || r.Name == "" {
			continue
		}
		switch {
		case strings.HasPrefix(r.Name, "//"):
			if r.Name == asset.Name {
				return true
			}
		case strings.HasPrefix(r.Name, "projects/"):
			if path == strings.TrimSuffix(r.Name, "/") {
				return true
			}
		case !strings.Contains(r.Name, "/"):
			if in.topLevel(path, r.Name) {
				return true
			}
		}
	}
	return false
}

// topLevel reports whether path is a top-level resource called name in the
// inventory's own project: "<name>" (the Cloud Asset API's form for a bucket,
// "//storage.googleapis.com/<name>") or "projects/<project>/<collection>/<name>",
// where the project is this inventory's or the "_" GCP writes for a resource
// whose name is global.
func (in Inventory) topLevel(path, name string) bool {
	if path == name {
		return true
	}
	seg := strings.Split(path, "/")
	return len(seg) == 4 && seg[0] == "projects" &&
		(seg[1] == in.GCPProject || seg[1] == "_") && seg[2] != "" && seg[3] == name
}

// assetPath is an asset's full resource name without its "//<service>/"
// prefix, which is the part the state's API paths can be compared with.
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
// name and the asset type, within the read's bounds (inventoryMaxPages,
// inventoryMaxAssets, and inventoryTimeout when ctx has no deadline). The read is the whole project and not a query
// narrowed to types this program knows, which is the point of it (finding 20).
func (c *CloudAssets) List(ctx context.Context, gcpProject string) ([]Asset, error) {
	if c == nil || c.svc == nil {
		return nil, errors.New("no Cloud Asset API client")
	}
	if gcpProject == "" {
		return nil, errors.New("no GCP project to read")
	}
	ctx, cancel := withInventoryDeadline(ctx)
	defer cancel()
	fail := func(err error) ([]Asset, error) {
		return nil, fmt.Errorf("reading project %s through the %s API: %w", gcpProject, ServiceCloudAsset, err)
	}
	call := c.svc.V1.SearchAllResources("projects/" + gcpProject).PageSize(inventoryPageSize)
	var out []Asset
	seen := map[string]bool{}
	token := ""
	for pages := 1; ; pages++ {
		res, err := call.PageToken(token).Context(ctx).Do()
		if err != nil {
			return fail(err)
		}
		for _, r := range res.Results {
			// A null or nameless element is not a resource: reading the listing
			// as complete without it would be a guess, so the read fails.
			if r == nil || r.Name == "" {
				return fail(errors.New("the listing holds an element with no resource name"))
			}
			out = append(out, Asset{Name: r.Name, Type: r.AssetType})
		}
		if len(out) > inventoryMaxAssets {
			return fail(fmt.Errorf("the listing holds more than %d assets", inventoryMaxAssets))
		}
		token = res.NextPageToken
		switch {
		case token == "":
			return out, nil
		case seen[token]:
			return fail(fmt.Errorf("the server repeated page token %q after %d pages", token, pages))
		case pages >= inventoryMaxPages:
			return fail(fmt.Errorf("the listing runs past %d pages", inventoryMaxPages))
		}
		seen[token] = true
	}
}
