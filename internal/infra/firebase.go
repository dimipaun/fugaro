package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	firebasedatabase "google.golang.org/api/firebasedatabase/v1beta"
	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/place"
)

// The Firebase side's singletons, in the Firebase project (the FP) and in
// the installation (the history account). The modules check their shapes.
const (
	SignerAccountID = "fugaro-token-signer"
	MinterRoleID    = "fugaroTokenMinter"
	APIKeyID        = "fugaro-web"

	HistoryAccountID = "fugaro-history"
	// CoordinatorAccountID is the coordinator's writer account (D194): the
	// aggregation job's own, which the installation root creates in
	// coordinator part P5, as it creates the history account's. Once it
	// exists the Firebase root grants it roles/firebasedatabase.admin on the
	// FP, so it can write the coordinator's subtree under the rules' floor.
	CoordinatorAccountID = "fugaro-coordinator"
	// HistoryJob must not start with fugaro-: ls and max_parallel count
	// every fugaro-* job as a workflow's.
	HistoryJob          = "fugarohist"
	HistorySchedulerJob = "fugaro-history-sweep"
	// CoordinatorJob is the aggregation job's name (P5), and it must not
	// start with fugaro- for HistoryJob's reason. The Scheduler job's name
	// may: no listing counts a Scheduler job.
	CoordinatorJob          = "fugarocoord"
	CoordinatorSchedulerJob = "fugaro-coordinator-sweep"
	// HistoryImagePackage is the history image's package in the base
	// registry (<registry host>/fugaro-base/history:latest).
	HistoryImagePackage = "history"
)

// HistorySpec is the installation root's history variable.
type HistorySpec struct {
	AccountID       string `json:"account_id"`
	Job             string `json:"job"`
	Image           string `json:"image"`
	SchedulerJob    string `json:"scheduler_job"`
	SchedulerRegion string `json:"scheduler_region"`
	// DeployJob is set once the Firebase root's outputs are known and the
	// image exists: the job needs both.
	DeployJob       bool   `json:"deploy_job"`
	FirebaseProject string `json:"firebase_project,omitempty"`
	RTDBURL         string `json:"rtdb_url,omitempty"`
}

// HistoryImage is the history job's image in the installation's base
// registry.
func HistoryImage(registryHost string) string {
	return registryHost + "/" + BaseRegistry + "/" + HistoryImagePackage + ":latest"
}

// History is the history job's spec for lc, with the job not yet deployed.
// The Firebase project and the database's URL are the config's, when it
// records them.
func History(lc *localcfg.Config) (HistorySpec, error) {
	host, err := RegistryHost(lc)
	if err != nil {
		return HistorySpec{}, err
	}
	region := lc.SchedulerRegion
	if region == "" {
		if region, err = gcp.SchedulerRegion(lc.Region); err != nil {
			return HistorySpec{}, userErr("%v", err)
		}
	}
	h := HistorySpec{AccountID: HistoryAccountID, Job: HistoryJob, Image: HistoryImage(host), SchedulerJob: HistorySchedulerJob, SchedulerRegion: region}
	if b := lc.Budget; b != nil {
		h.FirebaseProject, h.RTDBURL = b.FirebaseProject, b.RTDBURL
	}
	return h, nil
}

// HistoryImageExists reports whether the history image's latest tag exists
// in the base registry.
func HistoryImageExists(ctx context.Context, c *Clients, lc *localcfg.Config, h HistorySpec) (bool, error) {
	return baseImageExists(ctx, c, lc, h.Image, "history")
}

// CoordinatorImageExists is HistoryImageExists for the aggregation job, whose
// image IS the history image (Coordinator): one CLI binary serves both jobs,
// so the two jobs are deployed and refused together, and neither can be
// deployed against an image that does not exist.
func CoordinatorImageExists(ctx context.Context, c *Clients, lc *localcfg.Config, cs CoordinatorSpec) (bool, error) {
	return baseImageExists(ctx, c, lc, cs.Image, "coordinator")
}

// baseImageExists reports whether image's latest tag exists in the
// installation's base registry. what names the image in a failure message.
func baseImageExists(ctx context.Context, c *Clients, lc *localcfg.Config, image, what string) (bool, error) {
	host, err := RegistryHost(lc)
	if err != nil {
		return false, err
	}
	m := registryHostRE.FindStringSubmatch(host)
	if m == nil {
		return false, userErr("registry host %q is not <region>-docker.pkg.dev/<project>", host)
	}
	if image != HistoryImage(host) {
		return false, fmt.Errorf("the %s image %s is not %s", what, image, HistoryImage(host))
	}
	name := "projects/" + m[2] + "/locations/" + m[1] + "/repositories/" + BaseRegistry + "/packages/" + HistoryImagePackage + "/tags/latest"
	_, err = c.AR.Projects.Locations.Repositories.Packages.Tags.Get(name).Context(ctx).Do()
	switch {
	case absent(err, serviceArtifactRegistry, target{project: m[2]}):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("reading tag %s: %w", name, err)
	}
	return true, nil
}

// CoordinatorSpec is the installation root's coordinator variable (P5): the
// aggregation job, and the account it runs as (D194).
type CoordinatorSpec struct {
	AccountID       string            `json:"account_id"`
	Job             string            `json:"job"`
	Image           string            `json:"image"`
	SchedulerJob    string            `json:"scheduler_job"`
	SchedulerRegion string            `json:"scheduler_region"`
	AppKeySecret    string            `json:"app_key_secret"`
	GitHubAppID     string            `json:"github_app_id"`
	Repos           []CoordinatorRepo `json:"repos"`
	// DeployJob is set once the Firebase root's outputs are known, the image
	// exists and the credential this sweep's repositories need is recorded:
	// a GitHub App ID where it reconciles a GitHub repository, which is the
	// one credential its GitHub path can mint from, and the credentials
	// service's URL where every repository it has is Bitbucket's (F24). The
	// App ID is therefore empty on such an installation and it is not a spec
	// that cannot be planned — internal/cli's coordinatorJob is what decides
	// which of the two credentials a run is looking at.
	DeployJob       bool   `json:"deploy_job"`
	FirebaseProject string `json:"firebase_project,omitempty"`
	RTDBURL         string `json:"rtdb_url,omitempty"`
}

// CoordinatorRepo is one repository a sweep reconciles, as the job's
// FUGARO_COORDINATOR_REPOS carries it: the provider kind and the name the host
// spells it with. The slug is deliberately not here — it is a function of the
// two (task.Slug), and the command derives it where it knows both, which is
// also what keeps the runs bucket's layout in one place.
type CoordinatorRepo struct {
	Provider string `json:"provider"`
	Repo     string `json:"repo"`
}

// Coordinator is the aggregation job's spec for lc, with the job not yet
// deployed. Its image is the history image's: one CLI binary serves both jobs
// (fugaro budget history and fugaro coordinator sweep are two commands of it),
// so the coordinator needs no image of its own, no second push and no second
// entry in the mirror that fills the base registry.
func Coordinator(lc *localcfg.Config) (CoordinatorSpec, error) {
	host, err := RegistryHost(lc)
	if err != nil {
		return CoordinatorSpec{}, err
	}
	region := lc.SchedulerRegion
	if region == "" {
		if region, err = gcp.SchedulerRegion(lc.Region); err != nil {
			return CoordinatorSpec{}, userErr("%v", err)
		}
	}
	appID, err := recordedGitHubAppID(lc)
	if err != nil {
		return CoordinatorSpec{}, err
	}
	c := CoordinatorSpec{
		AccountID:       CoordinatorAccountID,
		Job:             CoordinatorJob,
		Image:           HistoryImage(host),
		SchedulerJob:    CoordinatorSchedulerJob,
		SchedulerRegion: region,
		AppKeySecret:    config.GitHubAppKeySecret,
		GitHubAppID:     appID,
		Repos:           CoordinatorRepos(lc),
	}
	if b := lc.Budget; b != nil {
		c.FirebaseProject, c.RTDBURL = b.FirebaseProject, b.RTDBURL
	}
	return c, nil
}

// CoordinatorRepos are the repositories one sweep reconciles: every repository
// the local config records a provider kind for, in the config's own order.
//
// A repository the config records no provider for is left out, and it is the
// one thing here that is not simply "every repository": the job runs with no
// checkout, so nothing on its machine can read a provider out of fugaro.yaml,
// and the sweep derives each repository's slug from the provider and the name
// together (task.Slug). A provider the config does not name is therefore a
// repository the job could not address at all.
//
// Every other provider is carried, bitbucket included: which of them a sweep
// can read is not this function's to decide. A Bitbucket repository is read
// through the installation's credentials service where it has one (Q12's
// amendment of 2026-11-01, BB5) and recorded as one the installation cannot
// reconcile where it has none, rather than dropped from the view.
func CoordinatorRepos(lc *localcfg.Config) []CoordinatorRepo {
	out := make([]CoordinatorRepo, 0, len(lc.Repos))
	for _, name := range slices.Sorted(maps.Keys(lc.Repos)) {
		if p := lc.Repos[name].Provider; slices.Contains(config.Providers, p) {
			out = append(out, CoordinatorRepo{Provider: p, Repo: name})
		}
	}
	return out
}

// FirebaseSpec is the Firebase root's tfvars: exactly its variables.
type FirebaseSpec struct {
	// Project is the Firebase project's ID (the FP).
	Project string `json:"project"`
	// DatabaseRegion is the location a NEW Realtime Database instance is
	// created in: the mapping's answer for the installation's own region
	// (place.For(inst.Region).RealtimeDatabase, D3089), which is us-central1
	// for a US installation as D4 chose and one of Google's three Realtime
	// Database locations for every other. It is NOT read for an instance that
	// already exists — the module ignores the field, and discovery imports the
	// instance at the location it really is in (D3091, F8) — which is why the
	// rule that gives it a value lives here and not at the plan.
	DatabaseRegion string        `json:"database_region"`
	FugaroProject  string        `json:"fugaro_project"`
	Names          FirebaseNames `json:"names"`
	ManageAPIs     bool          `json:"manage_apis"`
	// FirestoreDatabaseExists is whether the FP's Firestore (default) database
	// is there, as the run reads it before this root is applied
	// (FirestoreStep.DatabaseExists). The module creates the run index's
	// composite indexes only when it is true (RI2, the module's
	// firestore_indexes.tf): an index cannot be created under a database that
	// does not exist, and `fugaro update` ensures that database LAST (D3027), so
	// a fresh installation's own apply would be refused and could never
	// converge. An installation that has its database gets the indexes in the
	// same update that carries this field; one that has not gets them on the
	// next update, once the database is there.
	FirestoreDatabaseExists bool `json:"firestore_database_exists"`
	// Launchers and Operators mint run tokens and read the database;
	// Admins (the GCP project's owners and editors) and BudgetAdmins write
	// it. The history account is the sweeper's, which holds its own roles.
	// CoordinatorAccount is the aggregation job's (D194, P5): the account
	// the installation root creates and this root grants
	// roles/firebasedatabase.admin to, because the coordinator's records are
	// written under the rules' floor and a path cannot be expressed as an
	// IAM binding. It is empty, and nothing is granted, on an installation
	// whose owner has not opted in to the coordinator (InstallOptions.
	// Coordinator), which is every installation by default.
	Launchers []string `json:"launchers"`
	Operators []string `json:"operators"`
	// Viewers read the database and the spend history and write nothing
	// (roles.md §4, RS4): the Firebase module grants them a read of this
	// project's database, and they are NOT part of the people the token
	// signer mints for, which is what keeps a viewer unable to claim anyone's
	// identity. Left out of the tfvars when empty, like the installation's.
	Viewers            []string `json:"viewers,omitempty"`
	Admins             []string `json:"admins"`
	BudgetAdmins       []string `json:"budget_admins"`
	HistoryAccount     string   `json:"history_account"`
	CoordinatorAccount string   `json:"coordinator_account"`
	// IssuerAccount is the credentials service's own account, which this
	// root grants iam.serviceAccounts.signJwt on the runs' signer
	// (launcher-does-not-mint plan task LM13; D3121, F7): the ONE row that
	// moves a run's identity off every launcher and onto the service. It is
	// an output of the installation root's own apply
	// (InstallationOutputs.CredentialsService.Account), never a derived
	// name, so it is passed only once that apply has created the account —
	// IAM refuses a member it cannot find, and a fresh setup's first apply
	// is an installation whose root creates no credentials service yet
	// (U9). Empty — the state of every installation with no service — plans
	// no row at all, and is left out of the tfvars, which is exactly what
	// this root planned before the variable existed.
	IssuerAccount string `json:"issuer_account,omitempty"`
	// WorkspaceServiceAccount is the workspace service account the member's
	// own workspace_pin names, and the ONLY principal this root grants a
	// read of this project's database to (docs/workspaces/spec.md §5, W7):
	// the workspace's credentials service reads the member's ledger with it,
	// for the reconciliation alarm that compares the workspace's own count
	// against the member's. Empty — the state of every installation that
	// belongs to no workspace — creates no role and no binding. It is never
	// the member's own account: the pin names a workspace, which is another
	// project's Fugaro installation, so no job account of this one appears
	// here.
	WorkspaceServiceAccount string `json:"workspace_service_account"`
	// SkipAPIs are the APIs the Firebase root leaves to the installation
	// root: set (to SharedAPIs) only when the Firebase project is the
	// installation's own project, and left out of the tfvars otherwise.
	SkipAPIs []string `json:"skip_apis,omitempty"`
	// PeopleMinter is the launcher-does-not-mint switch (LM11, LM12, LM14):
	// the root's var.people_minter, which keeps the people's minter rows
	// (modules/firebase/iam.tf's google_service_account_iam_member.minter)
	// while it is on. It is a POINTER for the reason InstallationSpec's is:
	// nil is the key's absence and the root's default (on), and only `false`
	// is ever written. nil on every run that is not the one moving the grant
	// (LM14's stage); every later run carries the key forward into the document
	// (applyFirebaseRoot's carry, CarriedPeopleMinter).
	PeopleMinter *bool `json:"people_minter,omitempty"`
}

// SharedAPIs are the APIs both the installation module and the Firebase
// module enable (a test pins this to the Terraform). In a same-project
// layout only the installation root enables them.
var SharedAPIs = []string{"cloudresourcemanager.googleapis.com", "iam.googleapis.com"}

// FirebaseNames are the Firebase root's singleton names.
type FirebaseNames struct {
	SignerAccountID string `json:"signer_account_id"`
	MinterRoleID    string `json:"minter_role_id"`
	APIKey          string `json:"api_key"`
}

// FirebaseInputs are what the Firebase root's spec is built from.
type FirebaseInputs struct {
	// FP is the Firebase project's ID.
	FP string
	// Admins are the GCP project's owners and editors (AdminsFromPolicy).
	Admins []string
	// BudgetAdmins are the further admins, from the flags or the local
	// config.
	BudgetAdmins []string
	// HistoryAccount is the history account's email, an output of the
	// installation's first apply. Empty (print-vars before any apply) is
	// the account's address in the installation's project.
	HistoryAccount string
	// CoordinatorAccount is the coordinator's writer account's email, the
	// same kind of output of the same apply (P5 creates it once the owner
	// opts in). It is read only when the installation spec carries the
	// coordinator, and empty is then the same address derivation
	// HistoryAccount's is.
	CoordinatorAccount string
	// WorkspaceServiceAccount is the account the local config's
	// workspace_pin names (localcfg's WorkspacePin.ServiceAccount), empty
	// when it names no workspace. It is the member's own value and never
	// the workspace's: the record a workspace publishes names no service
	// account, so nothing the workspace says can widen its reach here.
	WorkspaceServiceAccount string
	// FirestoreDatabaseExists is whether the FP's Firestore (default) database
	// is there, which the run reads before this root is applied
	// (FirestoreStep.DatabaseExists) and hands over here: it gates the run
	// index's composite indexes (FirebaseSpec.FirestoreDatabaseExists, RI2).
	// It is false wherever no such read was made — --print-vars makes none —
	// and a plan that carries no index is the one that cannot fail.
	FirestoreDatabaseExists bool
	// IssuerAccount is the credentials service's own account, read back from
	// the installation root's output (CredentialsService.Account), and empty
	// on an installation whose root created no such account — a fresh
	// setup's first apply, and every installation that carries no service
	// (U9). It is passed rather than derived from the credentials account's
	// name: a name this package could compose is an account nothing has
	// created yet, and this root's row is the one grant of the file that
	// names its member as a literal plan string (issuerMinterGrant is what
	// holds it to that account of a run whose plans name it at all).
	IssuerAccount string
	// PeopleMinter is the launcher-does-not-mint switch as the caller knows
	// it (LM14): nil is the key's absence and the root's default (on), and
	// only `false` is ever passed. It is what the run that moves the grant
	// hands over, and the carry of the document the workdir already held
	// otherwise.
	PeopleMinter *bool
}

// Firebase is the Firebase root's spec for the installation spec, which
// supplies the project's name, launchers, operators and API management.
// Every member that gets a role on the FP is checked (CheckFirebaseMembers).
func Firebase(inst InstallationSpec, in FirebaseInputs) (FirebaseSpec, error) {
	if !fpIDRE.MatchString(in.FP) {
		return FirebaseSpec{}, userErr("%q is not a Firebase project ID (6-30 characters of a-z, 0-9 and -, starting with a letter)", in.FP)
	}
	history := in.HistoryAccount
	if history == "" {
		history = serviceAccountEmail(HistoryAccountID, inst.Project)
	}
	// The coordinator's account exists only on an installation whose owner
	// opted in (inst.Coordinator, P5): the installation root's first apply
	// creates it, which every path to this root runs first, so this is then
	// the address of an account that exists by the time the grant is planned
	// — the same claim history's is. Without the opt-in it stays empty and
	// the Firebase root grants nothing: IAM refuses a grant to a member that
	// does not exist, and an installation that did not ask for the database
	// admin's writer must not get one.
	coordinator := ""
	if inst.Coordinator != nil {
		coordinator = in.CoordinatorAccount
		if coordinator == "" {
			coordinator = serviceAccountEmail(CoordinatorAccountID, inst.Project)
		}
	}
	s := FirebaseSpec{
		Project: in.FP,
		// The installation's own region decides where a new budget database
		// goes, through the product's table rather than a rule of this file's
		// (docs/location-preference/reconciliation.md, "The mapping"): Google
		// offers the Realtime Database in three locations for the whole world,
		// so the region itself is the answer only for the three that are one of
		// them. The region a run in flight has is whatever its installation was
		// created with, and an installation whose region is under europe-west1
		// gets europe-west1 here.
		DatabaseRegion:          place.For(inst.Region).RealtimeDatabase.Where,
		FugaroProject:           inst.FugaroProject,
		Names:                   FirebaseNames{SignerAccountID: SignerAccountID, MinterRoleID: MinterRoleID, APIKey: APIKeyID},
		ManageAPIs:              inst.ManageAPIs,
		FirestoreDatabaseExists: in.FirestoreDatabaseExists,
		Launchers:               members(inst.Launchers, nil),
		Operators:               members(inst.Operators, nil),
		Viewers:                 members(inst.Viewers, nil),
		Admins:                  sortedUnique(in.Admins),
		BudgetAdmins:            sortedUnique(in.BudgetAdmins),
		HistoryAccount:          history,
		CoordinatorAccount:      coordinator,
		PeopleMinter:            in.PeopleMinter,
	}
	// The workspace's account is the member's own pin and needs no rule of
	// this root's to exist (it is not created here, and it is not on the
	// review screen: a workspace is another project's installation). What is
	// checked is its SHAPE, and here rather than at the plan: the module's
	// variable refuses anything but a service account email, and a value the
	// module would refuse is refused before the apply instead.
	if in.WorkspaceServiceAccount != "" {
		if !saEmailRE.MatchString(in.WorkspaceServiceAccount) {
			return FirebaseSpec{}, userErr("the workspace service account %q is not a service account email (<id>@<project>.iam.gserviceaccount.com); the workspace's read of this project's database is bound to it and IAM refuses a member it cannot find", in.WorkspaceServiceAccount)
		}
		s.WorkspaceServiceAccount = in.WorkspaceServiceAccount
	}
	// The issuer's account is checked for its shape for the workspace's
	// reason: the module's own validation is reached at that root's plan,
	// which this run makes AFTER it has applied the installation root, so a
	// value the module refuses is refused here instead — the member is the
	// one identity that mints every run's token through this root's signer,
	// and a refusal that waited for the plan would come with an apply
	// already behind it.
	if in.IssuerAccount != "" {
		if !saEmailRE.MatchString(in.IssuerAccount) {
			return FirebaseSpec{}, userErr("the issuer account %q is not a service account email (<id>@<project>.iam.gserviceaccount.com): the credentials service's account mints every run's identity through this root's signer, and a member IAM cannot resolve is a row the apply that plans it fails on", in.IssuerAccount)
		}
		s.IssuerAccount = in.IssuerAccount
	}
	if in.FP == inst.Project {
		// One project holds the installation and the Firebase backend (design
		// D3, revised 2026-10-04): the installation root, applied first,
		// enables the APIs both roots need, so the Firebase root skips them
		// and no API is owned by two states.
		s.SkipAPIs = slices.Clone(SharedAPIs)
	}
	if err := CheckFirebaseMembers(inst.Project, in.FP, map[string][]string{
		"launchers": s.Launchers, "operators": s.Operators, "viewers": s.Viewers,
		"admins": s.Admins, "budget admins": s.BudgetAdmins,
	}); err != nil {
		return FirebaseSpec{}, err
	}
	return s, nil
}

// FirebaseVars is the Firebase root's terraform.tfvars.json.
func FirebaseVars(spec FirebaseSpec) ([]byte, error) {
	spec.Launchers = launchersWithOperators(spec.Launchers, spec.Operators)
	spec.Operators = sortedUnique(spec.Operators)
	spec.Viewers = sortedUnique(spec.Viewers)
	return sortedJSON(spec)
}

func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return append([]string{}, slices.Compact(out)...)
}

// ValidProjectID reports whether s is a GCP project ID.
func ValidProjectID(s string) bool { return fpIDRE.MatchString(s) }

var (
	fpIDRE       = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	fpMemberRE   = regexp.MustCompile(`^(user|group|serviceAccount):([^\s*:]+)$`)
	saMemberHost = ".iam.gserviceaccount.com"
	// saEmailRE is one service account's email, the shape the modules'
	// account variables accept. It is stated here as well as in the HCL so a
	// value terraform would refuse at plan time — with a plan that has
	// already been written and a state file already touched — is refused
	// before the run.
	saEmailRE = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\.iam\.gserviceaccount\.com$`)
)

// CheckFirebaseMembers refuses a member that must not hold a role on the
// Firebase project: anything but a user, group or service account with one
// address (no domain:, no wildcard), and any account Fugaro itself manages,
// whose names start with fugaro- in the installation's project or the FP
// (job, build, scheduler, history and signer accounts). The installation
// module still accepts domain: launchers; they are refused here, before any
// apply. The "no job holds a role on the Firebase project" invariant rests
// on this list as much as on the modules.
func CheckFirebaseMembers(gcpProject, fp string, lists map[string][]string) error {
	var problems []string
	for _, name := range slices.Sorted(mapKeys(lists)) {
		for _, m := range lists[name] {
			sub := fpMemberRE.FindStringSubmatch(m)
			switch {
			case sub == nil:
				problems = append(problems, fmt.Sprintf("%s: %q is not a user:, group: or serviceAccount: member with one address (no domain: and no wildcard: these members get roles on the Firebase project)", name, m))
			case sub[1] == "serviceAccount" && fugaroManaged(sub[2], gcpProject, fp):
				problems = append(problems, fmt.Sprintf("%s: %s is a Fugaro-managed account (a job, build, scheduler, history or signer account); none holds a role on the Firebase project beyond the grants the Firebase root makes itself", name, m))
			}
		}
	}
	if len(problems) > 0 {
		return userErr("%s", strings.Join(problems, "\n"))
	}
	return nil
}

func mapKeys(m map[string][]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func fugaroManaged(email, gcpProject, fp string) bool {
	local, host, ok := strings.Cut(email, "@")
	if !ok || !strings.HasPrefix(local, "fugaro-") {
		return false
	}
	return host == gcpProject+saMemberHost || host == fp+saMemberHost
}

// AdminsFromPolicy is the budget admins D6 names: the users and groups that
// hold roles/owner or roles/editor on the GCP project. Everything else is
// left out, and said: service accounts (Google's default accounts such as
// the Compute and App Engine ones carry roles/editor and must not get the
// database), domain: and wildcard members, deleted members and conditional
// bindings. skipped says what was left out, for the operator, who may add
// an account with --budget-admin.
func AdminsFromPolicy(p *crm.Policy) (admins, skipped []string) {
	seen := map[string]bool{}
	for _, b := range p.Bindings {
		if b.Role != "roles/owner" && b.Role != "roles/editor" {
			continue
		}
		for _, m := range b.Members {
			switch sub := fpMemberRE.FindStringSubmatch(m); {
			case b.Condition != nil:
				skipped = append(skipped, fmt.Sprintf("%s (%s, conditional)", m, b.Role))
			case sub == nil, sub[1] == "serviceAccount":
				skipped = append(skipped, fmt.Sprintf("%s (%s)", m, b.Role))
			case !seen[m]:
				seen[m] = true
				admins = append(admins, m)
			}
		}
	}
	slices.Sort(admins)
	slices.Sort(skipped)
	return admins, slices.Compact(skipped)
}

var defaultAccountRE = regexp.MustCompile(`^serviceAccount:([0-9]+-compute@developer|[0-9]+@cloudbuild|[a-z][a-z0-9-]+@appspot)\.gserviceaccount\.com$`)

// DefaultAccountRisks lists the project's default Compute Engine, Cloud
// Build and App Engine service accounts that hold a primitive role
// (roles/editor or roles/owner). In a project that also holds the budget
// backend, anything that runs as such an account (a build submitted without
// a serviceAccount, a job or function deployed without one) reaches the
// database, Firestore and Identity Toolkit through it. It reports, never
// refuses: the policy is the person's.
func DefaultAccountRisks(p *crm.Policy) []string {
	var out []string
	for _, b := range p.Bindings {
		if b.Role != "roles/editor" && b.Role != "roles/owner" {
			continue
		}
		for _, m := range b.Members {
			if defaultAccountRE.MatchString(m) {
				out = append(out, fmt.Sprintf("%s (%s)", strings.TrimPrefix(m, "serviceAccount:"), b.Role))
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ProjectAdmins reads the GCP project's IAM policy and returns its budget
// admins (AdminsFromPolicy) and its default service accounts that hold a
// primitive role (DefaultAccountRisks).
func ProjectAdmins(ctx context.Context, c *Clients, gcpProject string) (admins, skipped, risks []string, err error) {
	req := &crm.GetIamPolicyRequest{Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3}}
	p, err := c.CRM.Projects.GetIamPolicy(gcpProject, req).Context(ctx).Do()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading the IAM policy of project %s (its owners and editors become budget admins): %w", gcpProject, err)
	}
	admins, skipped = AdminsFromPolicy(p)
	return admins, skipped, DefaultAccountRisks(p), nil
}

// CheckFirebaseProject refuses a Firebase project that doesn't exist (or
// can't be read), isn't active, or has no billing. It may be the
// installation's own GCP project (design D3, revised 2026-10-04). It reads
// only: it never creates the project or links billing (the project stage
// does, only behind --create-project and --link-billing).
func CheckFirebaseProject(ctx context.Context, c *Clients, fp string) error {
	p, err := c.CRM.Projects.Get(fp).Context(ctx).Do()
	var ge *googleapi.Error
	switch {
	case errors.As(err, &ge) && (ge.Code == 403 || ge.Code == 404):
		return userErr("project %s does not exist, or you can't read it. Create it as a new GCP project and link a billing account (Blaze), or, when it is the installation's own --gcp-project, let fugaro setup --create-project do it (typed confirmation)", fp)
	case err != nil:
		return fmt.Errorf("reading project %s: %w", fp, err)
	case p.LifecycleState != "" && p.LifecycleState != "ACTIVE":
		return userErr("project %s is %s, not ACTIVE", fp, p.LifecycleState)
	}
	if c.Billing == nil {
		return errors.New("no Cloud Billing client (endpoints.cloud_billing is not set)")
	}
	bi, err := c.Billing.Projects.GetBillingInfo("projects/" + fp).Context(ctx).Do()
	if _, off := disabledConsumer(err, serviceCloudBilling); off && c.QuotaProject != "" && c.ServiceUsage != nil {
		// The read is billed to the installation's own project, so that is
		// where the API is turned on: the update converges by itself.
		if eerr := EnableService(ctx, c, c.QuotaProject, serviceCloudBilling); eerr != nil {
			return userErr("the Cloud Billing API is off in %s and could not be enabled: gcloud services enable %s --project %s", c.QuotaProject, serviceCloudBilling, c.QuotaProject)
		}
		bi, err = c.Billing.Projects.GetBillingInfo("projects/" + fp).Context(ctx).Do()
	}
	if _, off := disabledConsumer(err, serviceCloudBilling); off {
		return userErr("the Cloud Billing API is off in %s: gcloud services enable %s --project %s", c.QuotaProject, serviceCloudBilling, c.QuotaProject)
	}
	switch {
	case errors.As(err, &ge) && (ge.Code == 403 || ge.Code == 404):
		return userErr("you can't read project %s's billing; it needs a linked billing account (Blaze), and you need billing.resourceAssociations.list or Owner on the project", fp)
	case err != nil:
		return fmt.Errorf("reading project %s's billing: %w", fp, err)
	case !bi.BillingEnabled:
		return userErr("project %s has no billing. Link a billing account (Blaze: the Spark plan's 100 connection limit would make a busy fleet fail closed); fugaro setup links one only for its own --gcp-project, with --create-project and --link-billing <account>, each typed", fp)
	}
	return nil
}

// disabledConsumer reports whether err is a 403 SERVICE_DISABLED for service,
// and the consumer (the credentials' quota project, projects/<number>) it
// names.
func disabledConsumer(err error, service string) (string, bool) {
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
		if consumer, _ := md["consumer"].(string); md["service"] == service && consumer != "" {
			return consumer, true
		}
	}
	return "", false
}

// DatabaseURLs lists the URLs of the Firebase project's Realtime Databases
// (none when the project has no Firebase yet, or its management API is not
// enabled). init checks each before anything is granted or created on the
// project, so a database that is not ours is refused on a first run too.
func DatabaseURLs(ctx context.Context, c *Clients, fp string) ([]string, error) {
	if c.FirebaseDB == nil {
		return nil, errors.New("no Realtime Database management client (endpoints.firebase_database is not set)")
	}
	var urls []string
	call := c.FirebaseDB.Projects.Locations.Instances.List("projects/" + fp + "/locations/-").Context(ctx)
	err := call.Pages(ctx, func(r *firebasedatabase.ListDatabaseInstancesResponse) error {
		for _, i := range r.Instances {
			if i.DatabaseUrl != "" {
				urls = append(urls, i.DatabaseUrl)
			}
		}
		return nil
	})
	switch {
	case absent(err, "firebasedatabase.googleapis.com", target{project: fp}):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("listing project %s's databases: %w", fp, err)
	}
	return urls, nil
}

// DatabaseCreatorPermissions are the project's own IAM changes the Firebase
// root makes: it creates the token signer's custom role and the account that
// wears it, and it grants the role to it (modules/firebase/iam.tf). They are
// what the caller of a run that would CREATE the budget database is asked for
// (R2): a person who holds them can make the project's IAM changes and the
// stage may go ahead, and one who does not gets the stage's one line naming a
// project owner instead of a failed apply halfway through the root.
//
// The list is the permissions and not a role: roles/editor holds none of the
// project's policy write, and whether an operator's roles suffice is exactly
// the question (U1, AB7's probe). roles/owner holds all four.
var DatabaseCreatorPermissions = []string{
	"resourcemanager.projects.getIamPolicy",
	"resourcemanager.projects.setIamPolicy",
	"iam.roles.create",
	"iam.serviceAccounts.create",
}

// CanMakeIAMChanges reports whether the caller holds every permission the
// Firebase root's own IAM changes need on project fp. It reads the caller's
// OWN permissions (projects.testIamPermissions) and never the project's policy,
// which is the API's own division: the policy says who may do what, and this
// says what this credential, here, may.
//
// A project the caller cannot read at all is an error rather than a no: the two
// are different answers, and the caller is the one that knows which of them its
// own line names.
func CanMakeIAMChanges(ctx context.Context, c *Clients, fp string) (bool, error) {
	if c.CRM == nil {
		return false, errors.New("no Resource Manager client (endpoints.cloud_resource_manager is not set)")
	}
	res, err := c.CRM.Projects.TestIamPermissions(fp, &crm.TestIamPermissionsRequest{Permissions: DatabaseCreatorPermissions}).Context(ctx).Do()
	if err != nil {
		return false, fmt.Errorf("testing your permissions on project %s: %w", fp, err)
	}
	held := map[string]bool{}
	for _, p := range res.Permissions {
		held[p] = true
	}
	for _, p := range DatabaseCreatorPermissions {
		if !held[p] {
			return false, nil
		}
	}
	return true, nil
}

// ErrNoFirebaseOutputs means the Firebase root's state holds no outputs.
var ErrNoFirebaseOutputs = errors.New("the Firebase root's state has no outputs; it has not been applied")

// FirebaseOutputs are the Firebase root's outputs.
type FirebaseOutputs struct {
	RTDBURL         string `json:"rtdb_url"`
	FirebaseAPIKey  string `json:"firebase_api_key"`
	TokenSigner     string `json:"token_signer"`
	FirebaseProject string `json:"firebase_project"`
}

// DecodeFirebaseOutputs reads the Firebase root's `terraform output -json`.
func DecodeFirebaseOutputs(raw map[string]json.RawMessage) (FirebaseOutputs, error) {
	var o FirebaseOutputs
	if len(raw) == 0 {
		return o, ErrNoFirebaseOutputs
	}
	for name, dst := range map[string]*string{"rtdb_url": &o.RTDBURL, "firebase_api_key": &o.FirebaseAPIKey, "token_signer": &o.TokenSigner, "firebase_project": &o.FirebaseProject} {
		val, ok := raw[name]
		if !ok {
			return FirebaseOutputs{}, fmt.Errorf("the Firebase root's outputs have no %s", name)
		}
		var v string
		if err := json.Unmarshal(val, &v); err != nil || v == "" {
			return FirebaseOutputs{}, fmt.Errorf("the Firebase root's output %s is not a string", name)
		}
		*dst = v
	}
	return o, nil
}

// CheckFirebaseOutputsFor refuses outputs that do not all belong to one
// Firebase project fp: the database must be exactly
// https://<fp>-default-rtdb.firebaseio.com or
// https://<fp>-default-rtdb.<region>.firebasedatabase.app, the signer exactly
// fugaro-token-signer@<fp>.iam.gserviceaccount.com (the account the Firebase
// module creates), and firebase_project fp itself. fp is the project the user
// named (--firebase-project) or, when "", the one the outputs name, which then has to
// be what the database and signer say too. They come from a state bucket, so
// a foreign but well-formed triple must not be taken as ours. loopbackOK is
// for the fakes' loopback database.
func CheckFirebaseOutputsFor(o FirebaseOutputs, fp string, loopbackOK bool) error {
	if fp == "" {
		fp = o.FirebaseProject
	}
	if !ValidProjectID(fp) {
		return fmt.Errorf("the Firebase project %q is not a project ID", fp)
	}
	if o.FirebaseProject != fp {
		return fmt.Errorf("the Firebase root's firebase_project is %s, not %s", o.FirebaseProject, fp)
	}
	if want := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", SignerAccountID, fp); o.TokenSigner != want {
		return fmt.Errorf("the Firebase root's token_signer is %s, not %s", o.TokenSigner, want)
	}
	u, err := url.Parse(o.RTDBURL)
	if err != nil {
		return fmt.Errorf("the Firebase root's rtdb_url is not a URL")
	}
	if loopbackOK && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1") {
		return nil
	}
	legacy := fp + "-default-rtdb.firebaseio.com"
	regional := regexp.MustCompile(`^` + regexp.QuoteMeta(fp) + `-default-rtdb\.[a-z0-9-]+\.firebasedatabase\.app$`)
	if u.Scheme != "https" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" ||
		!(u.Host == legacy || regional.MatchString(u.Host)) {
		return fmt.Errorf("the Firebase root's rtdb_url %s is not project %s's database (https://%s or https://%s-default-rtdb.<region>.firebasedatabase.app)", o.RTDBURL, fp, legacy, fp)
	}
	return nil
}
