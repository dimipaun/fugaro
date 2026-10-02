package infra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// The Firebase side's singletons, in the Firebase project (the FP) and in
// the installation (the history account). The modules check their shapes.
const (
	SignerAccountID = "fugaro-token-signer"
	MinterRoleID    = "fugaroTokenMinter"
	APIKeyID        = "fugaro-web"

	HistoryAccountID = "fugaro-history"
	// HistoryJob must not start with fugaro-: ls and max_parallel count
	// every fugaro-* job as a workflow's.
	HistoryJob          = "fugarohist"
	HistorySchedulerJob = "fugaro-history-sweep"
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
	host, err := RegistryHost(lc)
	if err != nil {
		return false, err
	}
	m := registryHostRE.FindStringSubmatch(host)
	if m == nil {
		return false, userErr("registry host %q is not <region>-docker.pkg.dev/<project>", host)
	}
	if h.Image != HistoryImage(host) {
		return false, fmt.Errorf("the history image %s is not %s", h.Image, HistoryImage(host))
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

// FirebaseSpec is the Firebase root's tfvars: exactly its variables.
type FirebaseSpec struct {
	// Project is the Firebase project's ID (the FP).
	Project       string        `json:"project"`
	FugaroProject string        `json:"fugaro_project"`
	Names         FirebaseNames `json:"names"`
	ManageAPIs    bool          `json:"manage_apis"`
	// Launchers and Operators mint run tokens and read the database;
	// Admins (the GCP project's owners and editors) and BudgetAdmins write
	// it. The history account is the sweeper's, which holds its own roles.
	Launchers      []string `json:"launchers"`
	Operators      []string `json:"operators"`
	Admins         []string `json:"admins"`
	BudgetAdmins   []string `json:"budget_admins"`
	HistoryAccount string   `json:"history_account"`
}

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
}

// Firebase is the Firebase root's spec for the installation spec, which
// supplies the project's name, launchers, operators and API management.
// Every member that gets a role on the FP is checked (CheckFirebaseMembers).
func Firebase(inst InstallationSpec, in FirebaseInputs) (FirebaseSpec, error) {
	if !fpIDRE.MatchString(in.FP) {
		return FirebaseSpec{}, userErr("%q is not a Firebase project ID (6-30 characters of a-z, 0-9 and -, starting with a letter)", in.FP)
	}
	if in.FP == inst.Project {
		return FirebaseSpec{}, userErr("the Firebase project must be a project of its own, not the installation's GCP project %s (design D3)", inst.Project)
	}
	history := in.HistoryAccount
	if history == "" {
		history = serviceAccountEmail(HistoryAccountID, inst.Project)
	}
	s := FirebaseSpec{
		Project:        in.FP,
		FugaroProject:  inst.FugaroProject,
		Names:          FirebaseNames{SignerAccountID: SignerAccountID, MinterRoleID: MinterRoleID, APIKey: APIKeyID},
		ManageAPIs:     inst.ManageAPIs,
		Launchers:      members(inst.Launchers, nil),
		Operators:      members(inst.Operators, nil),
		Admins:         sortedUnique(in.Admins),
		BudgetAdmins:   sortedUnique(in.BudgetAdmins),
		HistoryAccount: history,
	}
	if err := CheckFirebaseMembers(inst.Project, in.FP, map[string][]string{
		"launchers": s.Launchers, "operators": s.Operators, "admins": s.Admins, "budget admins": s.BudgetAdmins,
	}); err != nil {
		return FirebaseSpec{}, err
	}
	return s, nil
}

// FirebaseVars is the Firebase root's terraform.tfvars.json.
func FirebaseVars(spec FirebaseSpec) ([]byte, error) {
	spec.Launchers = launchersWithOperators(spec.Launchers, spec.Operators)
	spec.Operators = sortedUnique(spec.Operators)
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

// ProjectAdmins reads the GCP project's IAM policy and returns its budget
// admins (AdminsFromPolicy).
func ProjectAdmins(ctx context.Context, c *Clients, gcpProject string) (admins, skipped []string, err error) {
	req := &crm.GetIamPolicyRequest{Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3}}
	p, err := c.CRM.Projects.GetIamPolicy(gcpProject, req).Context(ctx).Do()
	if err != nil {
		return nil, nil, fmt.Errorf("reading the IAM policy of project %s (its owners and editors become budget admins): %w", gcpProject, err)
	}
	admins, skipped = AdminsFromPolicy(p)
	return admins, skipped, nil
}

// CheckFirebaseProject refuses a Firebase project that doesn't exist (or
// can't be read), isn't active, is the installation's own project, or has
// no billing. It reads only: fugaro never creates the project or links
// billing (design D3).
func CheckFirebaseProject(ctx context.Context, c *Clients, gcpProject, fp string) error {
	if fp == gcpProject {
		return userErr("the Firebase project must be a project of its own, not the installation's GCP project %s (design D3)", gcpProject)
	}
	p, err := c.CRM.Projects.Get(fp).Context(ctx).Do()
	var ge *googleapi.Error
	switch {
	case errors.As(err, &ge) && (ge.Code == 403 || ge.Code == 404):
		return userErr("project %s does not exist, or you can't read it. Create it as a new GCP project and link a billing account (Blaze); fugaro init never creates a project or enables billing", fp)
	case err != nil:
		return fmt.Errorf("reading project %s: %w", fp, err)
	case p.LifecycleState != "" && p.LifecycleState != "ACTIVE":
		return userErr("project %s is %s, not ACTIVE", fp, p.LifecycleState)
	}
	if c.Billing == nil {
		return errors.New("no Cloud Billing client (endpoints.cloud_billing is not set)")
	}
	bi, err := c.Billing.Projects.GetBillingInfo("projects/" + fp).Context(ctx).Do()
	switch {
	case errors.As(err, &ge) && (ge.Code == 403 || ge.Code == 404):
		return userErr("you can't read project %s's billing; it needs a linked billing account (Blaze), and you need billing.resourceAssociations.list or Owner on the project", fp)
	case err != nil:
		return fmt.Errorf("reading project %s's billing: %w", fp, err)
	case !bi.BillingEnabled:
		return userErr("project %s has no billing. Link a billing account (Blaze: the Spark plan's 100 connection limit would make a busy fleet fail closed); fugaro init never enables billing", fp)
	}
	return nil
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
