package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	iam "google.golang.org/api/iam/v1"
	runapi "google.golang.org/api/run/v2"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// The three permissions the two reader checks turn on: a role carrying the
// first lets its holder read a secret's value, one carrying the second lets
// its holder deploy a revision of the credentials service, and one carrying
// the third lets a deployment run as another account.
const (
	versionsAccess    = "secretmanager.versions.access"
	runServicesUpdate = "run.services.update"
	actAs             = "iam.serviceAccounts.actAs"
)

// secretReaderRoles are the predefined roles whose permissions include
// secretmanager.versions.access — the two Secret Manager ones. They are
// recognized by name and never expanded through the IAM API: what a role of
// Google's own name carries is Google's to say, and a project that defined a
// role of the same name could otherwise answer for it.
var secretReaderRoles = []string{"roles/secretmanager.admin", "roles/secretmanager.secretAccessor"}

// basicRoles are the project's basic roles. They bypass secret-level IAM
// whatever a container's own policy says, so their holders are named once
// rather than per container (F3): it is not a secretAccessor grant that makes
// an owner able to read a secret.
var basicRoles = []string{"roles/owner", "roles/editor"}

// runServiceRoles and actAsRoles are the predefined roles that carry
// run.services.update (deploying a Cloud Run service) and
// iam.serviceAccounts.actAs (deploying one that runs as another account),
// recognized by name for secretReaderRoles' reason.
var (
	runServiceRoles = []string{"roles/owner", "roles/editor", "roles/run.admin", "roles/run.developer"}
	actAsRoles      = append(slices.Clone(runServiceRoles), "roles/iam.serviceAccountUser")
)

// doctorSecretReaderChecks is doctor's "secret-readers-unexpected" sweep (plan
// B0.3, D503a): every member holding secretAccessor on any container of the
// installation's project that is not one of the account classes Fugaro
// creates for it, and every service account holding, at the project level, a
// role that carries secretmanager.versions.access other than the classes
// Fugaro grants. A member Fugaro never created is what an over-granted or
// compromised principal looks like from outside, and no other doctor check
// reads a container this installation did not create.
//
// A warning, never red: a grant can be a leftover of a hand change or a
// revocation still in flight, and red would fail an installation mid-repair.
// Read-only and metadata only — container names, their IAM policies and the
// project's, never a version's value: the sweep must not become a reader of
// the thing it watches (the B0.2 guard's rule for a new reader).
//
// Owners and Editors are reported once, as information (F3): they can read
// every container whatever its own policy says, and naming them per container
// would bury the grants that mean something.
func doctorSecretReaderChecks(ctx context.Context, lc *localcfg.Config, sm *gcp.Secrets, crmSvc *crm.Service) []doctorCheck {
	if sm == nil {
		return nil
	}
	var (
		ownersEditors []string
		checks        []doctorCheck
	)
	containers, err := sm.List(ctx, nil)
	if err != nil {
		// The listing is one of the sweep's two reads, and its failure must not
		// hide the other half: a project-level grant is a read of its own.
		checks = append(checks, doctorCheck{ID: "secret-readers-unexpected", Severity: "warning",
			Problem: "could not list the secret containers of project " + lc.GCPProject + ", so who can read them was not checked: " + oneLineCLI(err.Error()),
			Fix:     "rerun fugaro doctor with access to the project, or as an operator"})
	} else {
		slices.SortFunc(containers, func(a, b gcp.SecretInfo) int { return strings.Compare(a.ID, b.ID) })
	}
	known := fugaroSecretReaderMembers(lc)
	roles := newIAMRoles(ctx, lc)
	// The project's own policy first: its basic roles are named once, and its
	// reader roles are a finding of their own, since a project-level grant is
	// every container at once.
	pol, err := projectPolicy(ctx, lc, crmSvc)
	if err != nil {
		checks = append(checks, doctorCheck{ID: "secret-readers-unexpected", Severity: "warning",
			Problem: "could not read the IAM policy of project " + lc.GCPProject + ", so whether it grants anyone a role that reads every secret is unknown, not no: " + oneLineCLI(err.Error()),
			Fix:     "rerun fugaro doctor with access to the project, or as an operator"})
	} else {
		ownersEditors = basicRoleMembers(pol)
		checks = append(checks, projectLevelReaderChecks(pol, lc.GCPProject, known, roles)...)
	}

	var unreadPolicy []string
	for _, s := range containers {
		members, err := sm.SecretAccessors(ctx, s.ID)
		if err != nil {
			// Fail closed, as the launcher check does for a job's policy: a
			// container nobody read is not one nobody can read.
			unreadPolicy = append(unreadPolicy, containerName(s))
			continue
		}
		var extra []string
		for _, m := range members {
			if known[m] || slices.Contains(ownersEditors, m) ||
				(strings.HasPrefix(s.ID, tf.ManagedPrefix) && managedAccountMember(m, lc.GCPProject)) {
				continue
			}
			extra = append(extra, m)
		}
		if len(extra) == 0 {
			continue
		}
		checks = append(checks, doctorCheck{ID: "secret-readers-unexpected", Severity: "warning",
			Problem: fmt.Sprintf("secret %s grants %s to %s, which Fugaro does not create for it: each of them can read every version of it",
				containerName(s), gcp.SecretAccessorRole, strings.Join(printableNames(extra), ", ")),
			Fix: "review each member and remove the ones Fugaro did not create: gcloud secrets remove-iam-policy-binding SECRET --member=MEMBER --role=" + gcp.SecretAccessorRole +
				" (" + selfCommand() + " secrets ls --json prints each container's id)"})
	}
	if len(unreadPolicy) > 0 {
		checks = append(checks, doctorCheck{ID: "secret-readers-unexpected", Severity: "warning",
			Problem: fmt.Sprintf("could not read the IAM policy of %d secret container(s): ", len(unreadPolicy)) + strings.Join(unreadPolicy, ", ") +
				", so whether they grant secretAccessor to anyone else is unknown, not no",
			Fix: "rerun fugaro doctor with access to the project, or as an operator who can read each container (gcloud secrets get-iam-policy SECRET)"})
	}
	if len(ownersEditors) > 0 {
		checks = append(checks, doctorCheck{ID: "secret-readers-unexpected", Severity: "info",
			Problem: "Project Owners and Editors read every secret of " + lc.GCPProject + " whatever a container's own policy says, because the basic roles bypass secret-level IAM, so they are named once here and not per container: " + strings.Join(printableNames(ownersEditors), ", "),
			Fix:     "no grant takes this away: review who holds a basic role (gcloud projects get-iam-policy " + quoteWord(lc.GCPProject) + ") and keep that list short, since every holder can read every credential the installation stores"})
	}
	return checks
}

// projectPolicy reads the installation project's IAM policy at version 3, the
// version the two reader checks need: a conditional binding is a grant like
// any other, and a lower version would drop it silently.
func projectPolicy(ctx context.Context, lc *localcfg.Config, crmSvc *crm.Service) (*crm.Policy, error) {
	return crmSvc.Projects.GetIamPolicy(lc.GCPProject,
		&crm.GetIamPolicyRequest{Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3}}).Context(ctx).Do()
}

// basicRoleMembers is the project policy's holders of a basic role, sorted and
// deduplicated: the people and accounts F3 names.
func basicRoleMembers(pol *crm.Policy) []string {
	var out []string
	for _, b := range pol.Bindings {
		if b == nil || !slices.Contains(basicRoles, b.Role) {
			continue
		}
		for _, m := range b.Members {
			if !strings.HasPrefix(m, "deleted:") {
				out = append(out, m)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// projectLevelReaderChecks is the project-level half of the sweep: every
// service account the project's own policy lets read a secret's value. Only
// service accounts are named — a project-level grant to a person or a group
// is a human's, and F3's basic roles are the ones doctor reports as such —
// and only a role that carries the permission, so a launcher's
// secretmanager.secrets.list is not mistaken for a reader's.
//
// A custom role is expanded through the IAM API, since only its definition
// says what it carries; one that could not be read is a finding of its own,
// because whether it reads secrets is then unknown rather than no.
func projectLevelReaderChecks(pol *crm.Policy, project string, known map[string]bool, roles *iamRoles) []doctorCheck {
	var found, unread []string
	for _, b := range pol.Bindings {
		if b == nil {
			continue
		}
		carries, errText := secretReaderRole(b.Role, roles)
		if errText != "" {
			unread = append(unread, pluginwire.Printable(b.Role)+" ("+errText+")")
			continue
		}
		if !carries {
			continue
		}
		for _, m := range b.Members {
			if !strings.HasPrefix(m, "serviceAccount:") || known[m] || strings.HasPrefix(m, "deleted:") {
				continue
			}
			found = append(found, m+" ("+b.Role+")")
		}
	}
	slices.Sort(found)
	found = slices.Compact(found)
	slices.Sort(unread)
	unread = slices.Compact(unread)
	var out []doctorCheck
	if len(found) > 0 {
		out = append(out, doctorCheck{ID: "secret-readers-unexpected", Severity: "warning",
			Problem: "project " + project + " grants a role that reads every secret to " + strings.Join(printableNames(found), ", ") + ", which Fugaro does not create: a project-level grant is every container at once",
			Fix:     "review each grant and remove the ones Fugaro did not create: gcloud projects remove-iam-policy-binding " + quoteWord(project) + " --member=MEMBER --role=ROLE"})
	}
	if len(unread) > 0 {
		out = append(out, doctorCheck{ID: "secret-readers-unexpected", Severity: "warning",
			Problem: "could not read what " + strings.Join(unread, ", ") + " carries, so whether it reads every secret is unknown, not no",
			Fix:     "rerun fugaro doctor with access to the project, or as an operator who can read each role (gcloud iam roles describe ROLE)"})
	}
	return out
}

// secretReaderRole reports whether role carries secretmanager.versions.access:
// a predefined Secret Manager role by name, a custom one from its own
// definition. errText is set when a custom role could not be read.
func secretReaderRole(role string, roles *iamRoles) (bool, string) {
	if slices.Contains(secretReaderRoles, role) {
		return true, ""
	}
	if !isCustomRole(role) {
		// A predefined role that is not one of the two carries no
		// secretmanager.versions.access, and asking the IAM API about a name
		// Google defines would let a project's own definition answer for it.
		return false, ""
	}
	perms, errText := roles.permissions(role)
	if errText != "" {
		return false, errText
	}
	return slices.Contains(perms, versionsAccess), ""
}

// isCustomRole reports whether name is a role this project or its organization
// defines, rather than one of Google's predefined names.
func isCustomRole(name string) bool { return strings.Contains(name, "/roles/") }

// containerName is how a message names a container: its logical name when it
// carries one (every container Fugaro creates does), else its id — the sweep
// reads every container of the project, and one of something else has no
// logical name.
func containerName(s gcp.SecretInfo) string {
	if n := s.Labels[gcp.LabelSecret]; n != "" {
		return pluginwire.Printable(n)
	}
	return pluginwire.Printable(s.ID)
}

// managedAccountMember reports whether member is a service account of project
// carrying the managed prefix: the shape of every account a managed plan
// creates. Such an account may read a container carrying the same prefix,
// which only a managed plan creates, and the plan's deployer holds
// secretmanager.secrets.setIamPolicy over exactly those names.
func managedAccountMember(member, project string) bool {
	id, ok := strings.CutPrefix(member, "serviceAccount:")
	if !ok {
		return false
	}
	id, ok = strings.CutSuffix(id, "@"+project+".iam.gserviceaccount.com")
	return ok && strings.HasPrefix(id, tf.ManagedPrefix)
}

// fugaroSecretReaderMembers is every IAM member string Fugaro creates for a
// container of this installation's project: the per-repository accounts
// projectSecretAccountOwners computes forward from the local config's own
// repositories (each workflow's job account, each named Claude account's
// variant of it, the repository's build account), the shared project jobs'
// accounts, the coordinator's writer, the Batch backend's VM account and the
// credentials service's own account.
//
// Only the classes the config records are known: a repository the local
// config does not list contributes no account, and its job's grant on a
// container then reads as unexpected. That is the same forward computation,
// and the same limit, the project-secret accessor check makes — a slug
// hashes, so an account cannot be decoded back to a repository.
func fugaroSecretReaderMembers(lc *localcfg.Config) map[string]bool {
	out := map[string]bool{}
	for m := range projectSecretAccountOwners(lc) {
		out[m] = true
	}
	email := func(id string) string {
		return "serviceAccount:" + id + "@" + lc.GCPProject + ".iam.gserviceaccount.com"
	}
	for _, name := range slices.Sorted(maps.Keys(lc.Profiles)) {
		pr := lc.Profiles[name]
		for _, account := range config.ProjectJobAccounts(pr) {
			out[email(gcp.ProjectServiceAccountID(name, pr.Provider, account))] = true
		}
	}
	if lc.Terraform.Coordinator {
		out[email(infra.CoordinatorAccountID)] = true
	}
	if lc.Batch != nil && lc.Batch.ServiceAccount != "" {
		out["serviceAccount:"+lc.Batch.ServiceAccount] = true
	}
	if lc.CredentialsService != nil {
		out[email(infra.CredentialsAccountID)] = true
	}
	return out
}

// doctorCredentialsServiceDeployers is doctor's "credentials-service-deployers"
// read (plan B0.3, D501): who may deploy the credentials service — the
// principals holding, on the project or on the service itself, a role that
// updates a Cloud Run service, and those holding iam.serviceAccounts.actAs on
// the account it runs as. It is silent for an installation that has not opted
// in, which is what the presence of the credentials_service block means.
//
// Information, never a failure: every one of these grants is a legitimate way
// to operate the installation — an operator deploys the service, the owner
// applies the installation — and what the line is for is that the surface is
// small and known. The service scales to zero, so this is a reading rather
// than a finding (D501).
//
// The service's own policy and its account's are read beside the project's,
// because a deploy needs run.services.update on the service AND actAs on its
// account; a grant of either is named, and a policy that could not be read is
// named as unread rather than left out.
func doctorCredentialsServiceDeployers(ctx context.Context, lc *localcfg.Config, crmSvc *crm.Service) []doctorCheck {
	if lc.CredentialsService == nil {
		return nil
	}
	roles := newIAMRoles(ctx, lc)
	var (
		deployers []string
		unread    []string
	)
	pol, err := projectPolicy(ctx, lc, crmSvc)
	if err != nil {
		unread = append(unread, "the IAM policy of project "+lc.GCPProject)
	} else {
		deployers = append(deployers, deployerMembers(bindingsOf(pol.Bindings, crmBindingFields), roles, runServiceRoles, runServicesUpdate)...)
	}
	// A test configuration (no_auth with no run endpoint) has nowhere to read
	// the service from: building the client would point it at the real Cloud
	// Run API, which a rig must never call. The line names it unread rather
	// than leaving it out, so "nothing grants a deploy" is never said about a
	// policy nobody looked at.
	if !lc.Endpoints.NoAuth || lc.Endpoints.Run != "" {
		svc, err := runapi.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Run)...)
		if err != nil {
			unread = append(unread, "the IAM policy of the Cloud Run service "+infra.CredentialsServiceName)
		} else {
			name := fmt.Sprintf("projects/%s/locations/%s/services/%s", lc.GCPProject, lc.Region, infra.CredentialsServiceName)
			sp, err := svc.Projects.Locations.Services.GetIamPolicy(name).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
			if err != nil {
				// A caller who may not read the service's policy is the
				// ordinary case — a launcher's own doctor run — and what could
				// not be read is named either way.
				unread = append(unread, "the IAM policy of the Cloud Run service "+infra.CredentialsServiceName)
			} else {
				deployers = append(deployers, deployerMembers(bindingsOf(sp.Bindings, runBindingFields), roles, runServiceRoles, runServicesUpdate)...)
			}
		}
	} else {
		unread = append(unread, "the IAM policy of the Cloud Run service "+infra.CredentialsServiceName+", in a test configuration")
	}
	account := infra.CredentialsAccountID + "@" + lc.GCPProject + ".iam.gserviceaccount.com"
	if !lc.Endpoints.NoAuth || lc.Endpoints.IAM != "" {
		iamSvc, err := iam.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.IAM)...)
		if err != nil {
			unread = append(unread, "the IAM policy of "+account)
		} else {
			ap, err := iamSvc.Projects.ServiceAccounts.GetIamPolicy("projects/" + lc.GCPProject + "/serviceAccounts/" + account).
				OptionsRequestedPolicyVersion(3).Context(ctx).Do()
			if err != nil {
				unread = append(unread, "the IAM policy of "+account)
			} else {
				deployers = append(deployers, deployerMembers(bindingsOf(ap.Bindings, saBindingFields), roles, actAsRoles, actAs)...)
			}
		}
	} else {
		unread = append(unread, "the IAM policy of "+account+", in a test configuration")
	}
	slices.Sort(deployers)
	deployers = slices.Compact(deployers)
	slices.Sort(unread)
	problem := fmt.Sprintf("who may deploy the credentials service (%s): %d principal(s)", infra.CredentialsServiceName, len(deployers))
	switch {
	case len(deployers) > 0:
		problem += ": " + strings.Join(printableNames(deployers), ", ")
	case len(unread) == 0:
		problem += " — nothing it read grants a deploy"
	}
	if len(unread) > 0 {
		problem += " (not read: " + strings.Join(unread, "; ") + ")"
	}
	return []doctorCheck{{ID: "credentials-service-deployers", Severity: "info", Problem: problem,
		Fix: "no action is required: this is who could deploy the service, and the installation needs at least one of them. Keep it to the operators who run " + selfCommand() + " update, and remember that a holder can deploy a revision that runs as the service's account, which reads every credential in the bundle"}}
}

// iamBinding is one binding of the three policies the deployer read joins:
// Resource Manager, Cloud Run v2 and IAM spell a binding as three types with
// the same two fields, and the read asks the same question of all three.
type iamBinding struct {
	role    string
	members []string
}

// bindingsOf adapts a policy's bindings to iamBinding.
func bindingsOf[T any](bs []*T, fields func(*T) (string, []string)) []iamBinding {
	out := make([]iamBinding, 0, len(bs))
	for _, b := range bs {
		role, members := fields(b)
		out = append(out, iamBinding{role: role, members: members})
	}
	return out
}

func crmBindingFields(b *crm.Binding) (string, []string)               { return b.Role, b.Members }
func runBindingFields(b *runapi.GoogleIamV1Binding) (string, []string) { return b.Role, b.Members }
func saBindingFields(b *iam.Binding) (string, []string)                { return b.Role, b.Members }

// deployerMembers is the members of bs that hold a role carrying perm, each
// named with the role that makes it one.
func deployerMembers(bs []iamBinding, roles *iamRoles, predefined []string, perm string) []string {
	var out []string
	for _, b := range bs {
		if !roleCarries(b.role, predefined, perm, roles) {
			continue
		}
		for _, m := range b.members {
			if strings.HasPrefix(m, "deleted:") {
				continue
			}
			out = append(out, m+" ("+b.role+")")
		}
	}
	return out
}

// roleCarries reports whether role carries perm: a predefined role of the
// table by name, a custom one from its own definition. A custom role that
// could not be read is not claimed either way — this read is information, and
// the reader sweep's own unread line is what names a role nobody could read.
func roleCarries(role string, predefined []string, perm string, roles *iamRoles) bool {
	if slices.Contains(predefined, role) {
		return true
	}
	if !isCustomRole(role) {
		return false
	}
	perms, errText := roles.permissions(role)
	return errText == "" && slices.Contains(perms, perm)
}

// iamRoles answers what a custom role carries, from the IAM API, once per role
// name: only the role's own definition says what it includes. A role the API
// refuses is reported unread, never as a role that carries nothing.
//
// With nowhere to ask — a test configuration, where an empty endpoint under
// no_auth would send the call to Google — every role is unread, so nothing is
// claimed about a role nobody read.
type iamRoles struct {
	ctx      context.Context
	svc      *iam.Service
	buildErr string
	perms    map[string][]string
	errs     map[string]string
}

func newIAMRoles(ctx context.Context, lc *localcfg.Config) *iamRoles {
	r := &iamRoles{ctx: ctx, perms: map[string][]string{}, errs: map[string]string{}}
	if lc.Endpoints.NoAuth && lc.Endpoints.IAM == "" {
		return r
	}
	svc, err := iam.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.IAM)...)
	if err != nil {
		r.buildErr = oneLineCLI(err.Error())
		return r
	}
	r.svc = svc
	return r
}

// permissions is role's included permissions as the IAM API resolves them,
// and why they could not be read.
func (r *iamRoles) permissions(role string) ([]string, string) {
	if p, ok := r.perms[role]; ok {
		return p, ""
	}
	if msg, ok := r.errs[role]; ok {
		return nil, msg
	}
	if r.svc == nil {
		msg := r.buildErr
		if msg == "" {
			msg = "no IAM endpoint is configured, so no role can be read"
		}
		r.errs[role] = msg
		return nil, msg
	}
	var (
		got *iam.Role
		err error
	)
	switch {
	case strings.HasPrefix(role, "projects/"):
		got, err = r.svc.Projects.Roles.Get(role).Context(r.ctx).Do()
	case strings.HasPrefix(role, "organizations/"):
		got, err = r.svc.Organizations.Roles.Get(role).Context(r.ctx).Do()
	default:
		err = fmt.Errorf("%q is not a role name", role)
	}
	if err != nil {
		r.errs[role] = oneLineCLI(err.Error())
		return nil, r.errs[role]
	}
	r.perms[role] = got.IncludedPermissions
	return got.IncludedPermissions, ""
}
