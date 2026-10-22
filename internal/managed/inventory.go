package managed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/cloudasset/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	inframanaged "github.com/dimipaun/fugaro/internal/infra/managed"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/safetext"
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

// RoleCloudAssetReader is the read role the whole-project read needs: the
// installation's own custom role holding exactly cloudasset.assets.searchAllResources,
// the one permission the read's SearchAllResources call makes, and a read
// permission, so the identity that runs the inventory gains no write from
// holding it. It is the same role ID the gate pins (tf.CloudAssetRole, whose
// permissions tf.RolePermissions holds to that one element), so the role the
// module creates, the role the gate admits a grant of, the role this read
// needs, and the owner-run check that exercises it (Check 67a) cannot drift
// apart.
//
// The owner's ruling of 2026-10-17 (Task 4.1a) replaced the predefined
// roles/cloudasset.viewer this read first named: viewer also carries
// cloudasset.assets.listResource, which no call here makes — the read is
// SearchAllResources alone — and more besides, so holding it was a read of a
// wider kind than the inventory is.
//
// The installation module grants it to the operators (iam.tf's
// operator_cloud_asset_reader): the operator running a managed apply is the
// identity that makes the read. The module grant and this role's entry in
// internal/infra/tf's grantRules landed together, because that table is an
// allowlist — an installation grant of a role the table does not name makes
// fugaro update refuse the installation's own plan.
//
// The impersonated deployer is NOT an identity that holds it, and nothing
// here may assume one. The deployer is created with its two custom roles and
// no other (D75, Task 1.5; deployer.tf), and the table above grants this
// role to people only, so a managed plan cannot grant it to an account either:
// a read made under the deployer's credential answers CauseRoleMissing and
// falls the run back to its confirmations, which is the fail-closed direction
// (the fallback a call that fails for any reason takes) rather than a clean
// read. The credential the caller makes the read with is the operator's own,
// which is the identity the installation grants this role to: internal/cli's
// managed apply makes the read before it assumes the deployer, and hands the
// answer in (Task 4.5).
const RoleCloudAssetReader = tf.CloudAssetRole

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
	// ProjectNumber is GCPProject's number, from the caller's own Resource
	// Manager read (the read every managed command already makes to prove a
	// bucket is the project's own). The service accounts GCP names by the
	// number are ignored only when the number in the name IS this one; with
	// a zero ProjectNumber none of them is, so they are foreign and the run
	// falls back to confirmations — the safe direction.
	ProjectNumber int64
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
	// fugaro update enables with the installation's other APIs.
	CauseAPIDisabled Cause = "the Cloud Asset API is disabled in the project"
	// CauseRoleMissing is the identity running the read without
	// RoleCloudAssetReader.
	CauseRoleMissing Cause = "the identity running the read does not hold " + RoleCloudAssetReader
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
	// CauseStateUnread is the installation's own state not being readable
	// (Unread, Task 4.5): the accounted side of the comparison could not be
	// built, so nothing is accounted for and the run keeps its confirmations.
	// It is the same direction as a read that failed, and it is a cause of its
	// own because what an operator does about it is different — the state
	// bucket's objects are what could not be listed or parsed, not the
	// project's assets.
	CauseStateUnread Cause = "the installation's own state could not be read"
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
//
// The detail is one bounded field before it goes in, for the reason the
// warning's names are (foreignWhy): it is an API's error text or a caller's
// own words about a read that failed, and the sentence is printed.
func (in Inventory) fallback(cause Cause, detail string) Decision {
	return Decision{Fallback: true, Cause: cause,
		Why: fmt.Sprintf("the whole-project inventory of %s could not be checked: %s (%s); this run falls back to confirmations, and nothing is deleted (D59)",
			in.what(), cause, safetext.Field(detail))}
}

// foreign is the listed assets nothing about this installation can claim — not
// the state (accounted), not GCP itself (gcpCreated), and not the owner-run
// commands that made the project Fugaro's own (installationCreated) — in the
// order the read returned them.
func (in Inventory) foreign(listed []Asset) []Asset {
	var out []Asset
	for _, a := range listed {
		if !in.gcpCreated(a) && !in.installationCreated(a) && !in.accounted(a) {
			out = append(out, a)
		}
	}
	return out
}

// installationCreated is the third thing that is not foreign: a resource
// Fugaro's own owner-run commands created and NO Terraform state accounts for.
//
// Why it is needed, and why it is not Accounted: the resources the
// installation makes without Terraform. Its own state buckets are created by
// infra.CreateStateBucket (the installation's fugaro-tfstate-<gcp-project> and
// D77's fugaro-managed-tfstate-<gcp-project>) and the managed creation's own
// step, and the Firebase project is added by infra.AddFirebase, which is an
// API call and no root's plan — the Firestore default database Firebase
// provisions with it is in no state either.
//
// The Firebase project is the one a creating run always adds and a root only
// sometimes declares: `fugaro setup --create-project` adds Firebase to every
// project it makes (init_project.go's addFirebase), while the Firebase ROOT —
// which holds google_firebase_project, the resource a state would account for
// — runs only under --firebase-project (init_stages.go). So a managed project created
// without --firebase-project holds a Firebase project no state has ever seen, and an
// inventory that did not name it here would read every such project as
// foreign and keep the confirmations of every apply forever. The
// whole-project read lists all of them, so without this the accounted side
// could never cover them, every real managed project would fall back, and the
// agent apply the budget gate opens would ask for a confirmation on every run
// (Check 67a's "no fallback" step). They are not Accounted entries because
// nothing in the state says a word about them: calling them accounted would
// make the state read out to be something it is not, and the caller builds
// Accounted out of the state alone.
//
// The match is by the exact names these resources carry, which are functions
// of this project's id — the same functions the rest of the program creates
// them with (InstallationsStateBucket, ManagedStateBucketName) — and never by
// a prefix or by type alone:
//
//   - a bucket called fugaro-tfstate-<this id> or
//     fugaro-managed-tfstate-<this id>. Bucket names are globally unique, so
//     there is exactly one of each and an agent cannot make a second under
//     that name; a bucket called fugaro-tfstate-<another id> is another
//     project's and stays foreign.
//   - the Firebase project AddFirebase added to this one,
//     firebase.googleapis.com/FirebaseProject at "projects/<this id>". A
//     Firebase project is named by the GCP project it belongs to, so there is
//     exactly one of this project's; another project's is another project's
//     and stays foreign. The path is compared whole, like the database's: a
//     resource filed deeper under this project is not the project itself.
//   - the DEFAULT Firestore database of this project,
//     firestore.googleapis.com/Database at "projects/<this id>/databases/(default)".
//     Any other database in the project is one somebody created and stays
//     foreign.
//   - the two objects the creating run writes into the runs bucket itself,
//     storage.googleapis.com/Object at
//     "projects/_/buckets/fugaro-runs-<this id>/objects/fugaro/managed.json"
//     and ".../fugaro/allow-delete.json". They are Fugaro's own records and
//     not a run's output: the marker is the creation fact
//     (internal/infra/managed.MarkerObject, written by the run that created
//     the project) and the list is the owner's own approval of the deletes
//     (AllowDeleteObject). Both are named by key under the runs bucket this
//     program derives from the project id — the same bucket
//     internal/infra/managed reads them back from (its runsBucketName), which
//     is what makes the name a fact about this project and not a guess. Every
//     other object stays foreign, including every other key under fugaro/ and
//     a run's own records under runs/.
//
// Anything else is foreign, and a project is single-purpose (D59) whatever
// else it holds. What this still cannot claim, because no name identifies it,
// is listed where the check's own limits are: the records a run writes into
// the runs bucket, which is itself accounted for by the installation's state.
func (in Inventory) installationCreated(a Asset) bool {
	if in.GCPProject == "" {
		return false
	}
	switch a.Type {
	case "storage.googleapis.com/Bucket":
		name := lastSegment(a.Name)
		return name == InstallationsStateBucket(in.GCPProject) || name == ManagedStateBucketName(in.GCPProject)
	case "firebase.googleapis.com/FirebaseProject":
		return assetPath(a.Name) == "projects/"+in.GCPProject
	case "firestore.googleapis.com/Database":
		return assetPath(a.Name) == "projects/"+in.GCPProject+"/databases/(default)"
	case "storage.googleapis.com/Object":
		root := "projects/_/buckets/fugaro-runs-" + in.GCPProject + "/objects/"
		key, ok := strings.CutPrefix(assetPath(a.Name), root)
		if !ok {
			return false
		}
		return key == inframanaged.MarkerObject || key == inframanaged.AllowDeleteObject
	}
	return false
}

// gcpCreated is the second half of what a clean project is, and it is the
// half that makes the check usable at all (Task 4.4's required precursor,
// recorded on #489's review of 2026-10-17).
//
// A whole-project read lists everything, and a real project always holds
// resources GCP created and no Terraform state can account for. One of them is
// always the project itself and one is each service it has enabled, so
// without this list EVERY read of a real managed project falls back — and D59's
// own "never halt, ask for a confirmation" would turn the managed apply's gate
// into a prompt on every run, which is the failure mode the gate exists to
// remove. Several of these are named by the project NUMBER, which no state id
// carries, so no Accounted entry could ever match them.
//
// The list is CLOSED, and that is the direction that matters: a type GCP adds
// later is foreign until it is named here, and a foreign resource is one
// confirmation. The two shared types — a service account, a log bucket or sink
// — are ignored by the NAME GCP gives its own, never by type alone, because a
// service account or a sink an operator created in the project is exactly the
// "single purpose" case D59 is about. Anything that is not one of these is
// still foreign.
func (in Inventory) gcpCreated(a Asset) bool {
	switch a.Type {
	case
		// The project itself: "//cloudresourcemanager.googleapis.com/projects/<p>".
		// Created by whoever created the project, and the resource the whole
		// read is made about.
		"cloudresourcemanager.googleapis.com/Project",
		// The services the project has enabled, which fugaro update enables with
		// the installation and which the deployment in progress enables for
		// itself: "//serviceusage.googleapis.com/projects/<number>/services/<api>".
		// Named by the project NUMBER, never by its id.
		"serviceusage.googleapis.com/Service",
		// Cloud Run's revisions and executions: created by the service or job
		// they belong to, never by a plan of their own, and unaccountable — a
		// revision's name carries a generated suffix.
		"run.googleapis.com/Revision",
		"run.googleapis.com/Execution",
		// The images a build pushes into the project's own Artifact Registry:
		// the output of the pipeline, not a resource a plan declares, and named
		// by digest.
		"artifactregistry.googleapis.com/DockerImage":
		return true
	case
		// The "_Default" and "_Required" log buckets and sinks: GCP reserves
		// both names, so neither can be an operator's. A bucket or sink of
		// another name is not this and stays foreign.
		"logging.googleapis.com/LogBucket",
		"logging.googleapis.com/LogSink":
		return lastSegment(a.Name) == "_Default" || lastSegment(a.Name) == "_Required"
	case "iam.googleapis.com/ServiceAccount":
		return in.gcpServiceAccount(a)
	}
	return false
}

// gcpServiceAccount reports whether a service account is one GCP itself
// created for THIS project — the default ones and the Google-managed agents,
// whose names GCP assigns and which a project of the installation's kind has
// whether or not any plan mentions it. Each form is matched exactly, against
// this project's own id and number, and nothing else is: an account an
// operator or an agent created — even one whose local part borrows an agent's
// shape, such as service-1@<p>.iam.gserviceaccount.com — and a default
// account of another project are resources the state does not account for and
// stay foreign, which is what keeps D59's fallback to confirmations reachable.
//
// The forms, with <N> this project's number (Inventory.ProjectNumber) and <p>
// its id (Inventory.GCPProject):
//
//   - <N>-compute@developer.gserviceaccount.com — the Compute Engine default.
//   - <N>@cloudservices.gserviceaccount.com — the Google APIs service agent:
//     the identity the API calls made on the project's behalf run as.
//   - service-<N>@gcp-sa-<agent>.iam.gserviceaccount.com — the Google-managed
//     agents GCP creates as services are used (Firebase's, Artifact
//     Registry's), and service-<N>@<d>.iam.gserviceaccount.com for the closed
//     list of older agent domains in legacyAgentDomains (Cloud Run's
//     serverless-robot-prod among them). The domain is never <p>'s own: an
//     account under <p>.iam.gserviceaccount.com is one somebody created in
//     the project, whatever it is called.
//   - <p>@appspot.gserviceaccount.com — the App Engine default, which the
//     project has once Firebase is added to it; <p> must be this project's id
//     and the asset must be filed under it.
//
// The number in the first three is why no Accounted entry could carry them:
// the installation's state knows the project by its id, and these are named by
// its number — and why the number is checked: any run of digits would also
// match another project's default account.
func (in Inventory) gcpServiceAccount(a Asset) bool {
	local, domain, ok := strings.Cut(lastSegment(a.Name), "@")
	if !ok || local == "" {
		return false
	}
	number := ""
	if in.ProjectNumber > 0 {
		number = strconv.FormatInt(in.ProjectNumber, 10)
	}
	ours := func(n string) bool { return number != "" && n == number }
	switch domain {
	case "developer.gserviceaccount.com":
		n, ok := strings.CutSuffix(local, "-compute")
		return ok && ours(n)
	case "cloudservices.gserviceaccount.com":
		return ours(local)
	case "appspot.gserviceaccount.com":
		// The id is checked twice: it is this project's, and the asset is
		// filed under it ("projects/<p>/...").
		return in.GCPProject != "" && local == in.GCPProject && pathSegment(a.Name, "projects") == in.GCPProject
	}
	sub, ok := strings.CutSuffix(domain, ".iam.gserviceaccount.com")
	if !ok || sub == "" || sub == in.GCPProject {
		return false
	}
	if !strings.HasPrefix(sub, "gcp-sa-") && !legacyAgentDomains[sub] {
		return false
	}
	n, ok := strings.CutPrefix(local, "service-")
	return ok && ours(n)
}

// legacyAgentDomains are the Google-managed service agents' domains that
// predate the gcp-sa-* naming, closed as gcpCreated's list is: an agent whose
// domain is not here and not gcp-sa-* is foreign until it is named.
var legacyAgentDomains = map[string]bool{
	"serverless-robot-prod": true, // Cloud Run
	"gcf-admin-robot":       true, // Cloud Functions
	"firebase-rules":        true, // Firebase Rules
	"compute-system":        true, // Compute Engine
	"containerregistry":     true, // Container Registry
}

// pathSegment is the segment of a full resource name that follows key: with
// the name "//iam.googleapis.com/projects/<p>/serviceAccounts/<email>", the
// project id is pathSegment(name, "projects"). It is empty when the name does
// not carry the key, which makes a comparison against it fail.
func pathSegment(name, key string) string {
	seg := strings.Split(assetPath(name), "/")
	for i, s := range seg {
		if s == key && i+1 < len(seg) {
			return seg[i+1]
		}
	}
	return ""
}

// digits reports whether s is a non-empty run of ASCII digits: a project
// number, and never the empty string a name with no number would leave.
func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// lastSegment is the part of a full resource name after its last "/": the
// bucket, sink or service account's own name.
func lastSegment(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
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
//
// Each name and type is one bounded field (safetext.Field) before it is joined
// into the sentence, and that is not decoration: this sentence is printed —
// by the managed apply, whose reader is the session that applied — and a
// resource name is a string the project's own API answered with, so a name
// carrying an escape sequence, a line break or ten thousand characters would
// be this program printing text of someone else's choosing, in text its reader
// acts on. The count is the sentence's own and is not bounded.
func (in Inventory) foreignWhy(foreign []Asset) string {
	named := make([]string, 0, inventoryNamed)
	for _, a := range foreign[:min(len(foreign), inventoryNamed)] {
		named = append(named, safetext.Field(a.Name)+" ("+safetext.Field(a.Type)+")")
	}
	more := ""
	if n := len(foreign) - len(named); n > 0 {
		more = fmt.Sprintf(", and %d more", n)
	}
	return fmt.Sprintf("%s holds %d resource(s) the installation's state does not account for: %s%s; this run falls back to confirmations, and nothing is deleted (D59)",
		in.what(), len(foreign), strings.Join(named, ", "), more)
}

// what names the project in the warning, in the shape the gate's own refusals
// use. Both halves are bounded: the warning is printed, and the inventory's
// project reaches it from the caller's configuration and, through the marker,
// from a cloud object.
func (in Inventory) what() string {
	switch {
	case in.Project != "" && in.GCPProject != "":
		return safetext.Field(in.Project) + " (" + safetext.Field(in.GCPProject) + ")"
	case in.Project != "":
		return safetext.Field(in.Project)
	case in.GCPProject != "":
		return safetext.Field(in.GCPProject)
	}
	return "this project"
}

// classify says which of the read's failures this is, because the three are
// three different things for an operator to do: enable the API (fugaro update
// does), grant RoleCloudAssetReader, or wait out a quota. It reads the
// structured answer, not the message, in the discipline internal/infra's own
// serviceDisabled keeps (apis.go): a 403 whose details carry an ErrorInfo with
// reason SERVICE_DISABLED *for this API* is a disabled API, any other 403 is a
// missing permission, and 429 is quota.
//
// The API the detail names has to be the one the read goes through, which is
// the part of that discipline a message-level reading gets wrong: a project can
// have a dozen APIs disabled, and answering "the Cloud Asset API is disabled in
// the project" for a SERVICE_DISABLED about another service is a wrong sentence
// about the operator's project — one that sends them to fugaro update for
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
// needs RoleCloudAssetReader on the project, which is a read role.
func NewCloudAssets(ctx context.Context, opts ...option.ClientOption) (*CloudAssets, error) {
	svc, err := cloudasset.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("connecting to the %s API: %w", ServiceCloudAsset, safetext.FieldErr(err))
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
		return nil, fmt.Errorf("reading project %s through the %s API: %w", safetext.Field(gcpProject), ServiceCloudAsset, safetext.FieldErr(err))
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
			return fail(fmt.Errorf("the server repeated page token %q after %d pages", safetext.Field(token), pages))
		case pages >= inventoryMaxPages:
			return fail(fmt.Errorf("the listing runs past %d pages", inventoryMaxPages))
		}
		seen[token] = true
	}
}
