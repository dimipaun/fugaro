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

// basicRoles are the project's basic roles that read a secret without being
// granted one. Owner is the only one: it carries secretmanager.versions.access
// itself and bypasses secret-level IAM whatever a container's own policy says,
// so its holders are named once rather than per container (F3).
//
// Editor is deliberately not here. Secret Manager grants Editor
// secretmanager.secrets.getIamPolicy and delete but not versions.access: an
// Editor reads a container directly only through a secretAccessor grant of its
// own, which is a grant like any other and the per-container half reports it.
// Naming Editors as readers would be a claim the live role does not support,
// and it would hide exactly those grants. (roles/editor also carries
// iam.serviceAccounts.actAs and run.services.update, so an Editor reaches a
// value indirectly too — by deploying code that runs as an account holding a
// grant. That is a deploy, not a container grant, and this check is about the
// latter.)
var basicRoles = []string{"roles/owner"}

// runServiceRoles and actAsRoles are the predefined roles that carry
// run.services.update (deploying a Cloud Run service) and
// iam.serviceAccounts.actAs (deploying one that runs as another account),
// recognized by name for secretReaderRoles' reason. The two lists differ by
// run.admin and run.developer, which update a service without carrying actAs:
// a holder of one of them on an account's policy is not a deployer of that
// account through an actAs grant. Editor is in both, because roles/editor carries
// actAs as well as run.services.update; the two reads produce the same
// member-and-role pair for a project-level Editor, and the line deduplicates
// them, so listing it twice costs nothing. Running as an account needs actAs
// from a serviceAccountUser grant of its own, or from Owner or Editor.
var (
	runServiceRoles = []string{"roles/owner", "roles/editor", "roles/run.admin", "roles/run.developer"}
	actAsRoles      = []string{"roles/owner", "roles/editor", "roles/iam.serviceAccountUser"}
)

// doctorSecretManagerChecks is the plan's B0.3 pair of reads, taken from one
// pass over the two things they share: the project's IAM policy and the cache
// of what a custom role carries. Both halves ask that policy a different
// question, and a second read of it is a second answer to the same question.
//
// The reader sweep needs a Secret Manager client and is left out without one;
// the deployer read does not, so a test configuration still gets its line.
func doctorSecretManagerChecks(ctx context.Context, lc *localcfg.Config, sm *gcp.Secrets, crmSvc *crm.Service) []doctorCheck {
	if sm == nil && lc.CredentialsService == nil {
		return nil
	}
	roles := newIAMRoles(ctx, lc)
	pol, polErr := projectPolicy(ctx, lc, crmSvc)
	var checks []doctorCheck
	if sm != nil {
		checks = append(checks, doctorSecretReaderChecks(ctx, lc, sm, pol, polErr, roles)...)
	}
	return append(checks, doctorCredentialsServiceDeployers(ctx, lc, pol, polErr, roles)...)
}

// doctorSecretReaderChecks is doctor's "secret-readers-unexpected" sweep (plan
// B0.3, D503a): every member of a role that reads a container's values on any
// container of the installation's project that is not one of the account
// classes Fugaro creates for it, and every principal holding, at the project
// level, a role that carries secretmanager.versions.access other than the
// classes Fugaro grants. A member Fugaro never created is what an over-granted
// or compromised principal looks like from outside, and no other doctor check
// reads a container this installation did not create.
//
// A warning, never red: a grant can be a leftover of a hand change or a
// revocation still in flight, and red would fail an installation mid-repair.
// Read-only and metadata only — container names, their IAM policies and the
// project's, never a version's value: the sweep must not become a reader of
// the thing it watches (the B0.2 guard's rule for a new reader).
//
// Owners are reported once, as information (F3): they can read every
// container whatever its own policy says, and naming them per container would
// bury the grants that mean something. An Editor is not a reader of its own —
// roles/editor carries no secretmanager.versions.access — so an Editor's
// container grant is a finding like any other member's.
//
// What the sweep advises depends on whose container it is (the owner's ruling
// of 2026-10-29): a container Fugaro created may be cleaned up, and its
// readers that Fugaro did not create are removed by hand; a container Fugaro
// did not create is only reviewed, since its readers may be another
// application's and are not Fugaro's to remove.
func doctorSecretReaderChecks(ctx context.Context, lc *localcfg.Config, sm *gcp.Secrets, pol *crm.Policy, polErr error, roles *iamRoles) []doctorCheck {
	var (
		owners []string
		checks []doctorCheck
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
	// The project's own policy first: its basic roles are named once, and its
	// reader roles are a finding of their own, since a project-level grant is
	// every container at once.
	if polErr != nil {
		checks = append(checks, doctorCheck{ID: "secret-readers-unexpected", Severity: "warning",
			Problem: "could not read the IAM policy of project " + lc.GCPProject + ", so whether it grants anyone a role that reads every secret is unknown, not no: " + oneLineCLI(polErr.Error()),
			Fix:     "rerun fugaro doctor with access to the project, or as an operator"})
	} else {
		owners = basicRoleMembers(pol)
		checks = append(checks, projectLevelReaderChecks(pol, lc.GCPProject, known, owners, roles)...)
	}

	var (
		unreadPolicy []string
		unreadRoles  []string
	)
	for _, s := range containers {
		bindings, err := sm.SecretPolicy(ctx, s.ID)
		if err != nil {
			// Fail closed, as the launcher check does for a job's policy: a
			// container nobody read is not one nobody can read.
			unreadPolicy = append(unreadPolicy, containerName(s))
			continue
		}
		var extra []string
		for _, b := range bindings {
			carries, errText := secretReaderRole(b.Role, roles)
			if errText != "" {
				unreadRoles = append(unreadRoles, "what "+pluginwire.Printable(b.Role)+" carries, on secret "+containerName(s))
				continue
			}
			if !carries {
				continue
			}
			for _, m := range b.Members {
				if strings.HasPrefix(m, "deleted:") || known[m] || slices.Contains(owners, m) ||
					(strings.HasPrefix(s.ID, tf.ManagedPrefix) && managedAccountMember(m, lc.GCPProject)) {
					continue
				}
				extra = append(extra, m+" ("+b.Role+")")
			}
		}
		if len(extra) == 0 {
			continue
		}
		slices.Sort(extra)
		extra = slices.Compact(extra)
		problem := fmt.Sprintf("secret %s grants a role that reads every version to %s: each of them can read every version of it",
			containerName(s), strings.Join(printableNames(extra), ", "))
		check := doctorCheck{ID: "secret-readers-unexpected", Severity: "warning", Problem: problem}
		// The managed label chooses only the line's wording — its Problem text
		// and its fix — and never who is reported: anyone who may add a label to
		// the container (secretmanager.secrets.update) can write `fugaro=managed`
		// on one Fugaro did not create, and the worst that buys is the removal
		// advice in place of the review one, with the ownership clause that goes
		// with it. The reader is named either way, so the label can never hide
		// one — which is why the sweep decides its wording from the label rather
		// than trusting it to bound the finding.
		if fugaroContainer(s) {
			check.Problem += ", and Fugaro does not create them for it"
			check.Fix = "review each member and remove the ones Fugaro did not create: gcloud secrets remove-iam-policy-binding SECRET --member=MEMBER --role=ROLE" +
				" (" + selfCommand() + " secrets ls --json prints each container's id)"
		} else {
			check.Problem += ", and Fugaro did not create this container, so a reader on it may be another application's"
			check.Fix = "review each member against the application or plan that owns this container: Fugaro did not create it, so its readers are not Fugaro's to remove" +
				" (gcloud secrets get-iam-policy SECRET prints them)"
		}
		checks = append(checks, check)
	}
	if len(unreadPolicy) > 0 {
		checks = append(checks, doctorCheck{ID: "secret-readers-unexpected", Severity: "warning",
			Problem: fmt.Sprintf("could not read the IAM policy of %d secret container(s): ", len(unreadPolicy)) + strings.Join(unreadPolicy, ", ") +
				", so whether they grant a role that reads them to anyone else is unknown, not no",
			Fix: "rerun fugaro doctor with access to the project, or as an operator who can read each container (gcloud secrets get-iam-policy SECRET)"})
	}
	if len(unreadRoles) > 0 {
		slices.Sort(unreadRoles)
		unreadRoles = slices.Compact(unreadRoles)
		checks = append(checks, doctorCheck{ID: "secret-readers-unexpected", Severity: "warning",
			Problem: "could not read " + strings.Join(unreadRoles, ", ") + ", so whether they read every version of those containers is unknown, not no",
			Fix:     "rerun fugaro doctor with access to the project, or as an operator who can read each role (gcloud iam roles describe ROLE)"})
	}
	if len(owners) > 0 {
		checks = append(checks, doctorCheck{ID: "secret-readers-unexpected", Severity: "info",
			Problem: "Project Owners read every secret of " + lc.GCPProject + " whatever a container's own policy says, because roles/owner carries secretmanager.versions.access and bypasses secret-level IAM, so they are named once here and not per container: " + strings.Join(printableNames(owners), ", "),
			Fix:     "no grant takes this away: review who holds roles/owner (gcloud projects get-iam-policy " + quoteWord(lc.GCPProject) + ") and keep that list short, since every holder can read every credential the installation stores"})
	}
	return checks
}

// fugaroContainer reports whether the container is one Fugaro created, by the
// managed label it writes on every container it makes. A container without it
// may belong to another application in the same project: its readers are that
// application's to decide, so the sweep reviews them instead of advising a
// removal Fugaro has no business making.
func fugaroContainer(s gcp.SecretInfo) bool {
	return s.Labels[gcp.LabelManaged] == gcp.ManagedValue
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
// principal the project's own policy lets read a secret's value — a service
// account of this project or another, and, since a project-level grant is
// every container at once, a person or a group too. Owners are left out: F3's
// line names them once, and a holder of roles/owner is on it whatever else
// the policy binds. Only a role that carries the permission is reported, so a
// launcher's secretmanager.secrets.list is not mistaken for a reader's.
//
// A custom role is expanded through the IAM API, since only its definition
// says what it carries; one that could not be read is a finding of its own,
// because whether it reads secrets is then unknown rather than no.
func projectLevelReaderChecks(pol *crm.Policy, project string, known map[string]bool, owners []string, roles *iamRoles) []doctorCheck {
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
			if strings.HasPrefix(m, "deleted:") || known[m] || slices.Contains(owners, m) {
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
			Problem: "project " + project + " grants a role that reads every secret to " + strings.Join(printableNames(found), ", ") + ", which Fugaro does not grant: a project-level grant is every container at once",
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
// the account it runs as or, through a project-level grant, on every account
// of the project, its own included. It is silent for an installation that has
// not opted in, which is what the presence of the credentials_service block
// means.
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
// named as unread rather than left out — as is a custom role nobody could
// resolve, since "not read" and "carries nothing" are not the same answer.
func doctorCredentialsServiceDeployers(ctx context.Context, lc *localcfg.Config, pol *crm.Policy, polErr error, roles *iamRoles) []doctorCheck {
	if lc.CredentialsService == nil {
		return nil
	}
	var (
		deployers []deployer
		unread    []string
	)
	if polErr != nil {
		unread = append(unread, "the IAM policy of project "+lc.GCPProject)
	} else {
		// The project's policy is asked twice: once for a role that updates a
		// service, once for one that acts as an account. A holder of the
		// latter can deploy a revision that runs as the service's account,
		// which reads every credential in the bundle — so it is a deployer of
		// it, even though the deploy itself still needs the update grant
		// somewhere (the owner's ruling, 2026-10-29).
		projectBindings := bindingsOf(pol.Bindings, crmBindingFields)
		got, unknown := deployerMembers(projectBindings, roles, runServiceRoles, runServicesUpdate, "the project's policy")
		deployers = append(deployers, got...)
		unread = append(unread, unknown...)
		got, unknown = deployerMembers(projectBindings, roles, actAsRoles, actAs, "the project's policy")
		deployers = append(deployers, got...)
		unread = append(unread, unknown...)
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
				got, unknown := deployerMembers(bindingsOf(sp.Bindings, runBindingFields), roles, runServiceRoles, runServicesUpdate, "the Cloud Run service's policy")
				deployers = append(deployers, got...)
				unread = append(unread, unknown...)
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
				got, unknown := deployerMembers(bindingsOf(ap.Bindings, saBindingFields), roles, actAsRoles, actAs, "the service account's policy")
				deployers = append(deployers, got...)
				unread = append(unread, unknown...)
			}
		}
	} else {
		unread = append(unread, "the IAM policy of "+account+", in a test configuration")
	}
	slices.SortFunc(deployers, func(a, b deployer) int {
		if c := strings.Compare(a.member, b.member); c != 0 {
			return c
		}
		return strings.Compare(a.role, b.role)
	})
	deployers = slices.Compact(deployers)
	slices.Sort(unread)
	// The project's policy is asked two questions, so one unread role or
	// policy can be named twice: the line lists what could not be read, and
	// the same thing read once is the same gap.
	unread = slices.Compact(unread)
	// The count is of principals, not of grants: one person holding two roles
	// that can deploy is one person who can deploy, and the line exists to say
	// how many of them there are.
	principals := map[string]bool{}
	names := make([]string, 0, len(deployers))
	for _, d := range deployers {
		principals[d.member] = true
		names = append(names, d.String())
	}
	problem := fmt.Sprintf("who may deploy the credentials service (%s): %d principal(s)", infra.CredentialsServiceName, len(principals))
	switch {
	case len(names) > 0:
		problem += ": " + strings.Join(printableNames(names), ", ")
	case len(unread) == 0:
		problem += " — nothing it read grants a deploy"
	}
	if len(unread) > 0 {
		problem += " (not read: " + strings.Join(unread, "; ") + ")"
	}
	return []doctorCheck{{ID: "credentials-service-deployers", Severity: "info", unread: len(unread) > 0, Problem: problem,
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

// deployer is one grant that can deploy: a member and the role it holds it
// through. The two are kept apart so that the line names each holder with the
// role that makes it one, and still counts holders rather than grants.
type deployer struct {
	member string
	role   string
}

func (d deployer) String() string { return d.member + " (" + d.role + ")" }

// deployerMembers is the members of bs that hold a role carrying perm, each
// named with the role that makes it one, and what could not be resolved: a
// custom role nobody could read, described with where it is bound. A role that
// could not be read is never silently dropped — the caller puts it on the
// line's "not read" list, so that a policy holding one is never reported as a
// policy with nothing on it.
func deployerMembers(bs []iamBinding, roles *iamRoles, predefined []string, perm, where string) (members []deployer, unread []string) {
	for _, b := range bs {
		carries, errText := roleCarries(b.role, predefined, perm, roles)
		if errText != "" {
			unread = append(unread, "what "+pluginwire.Printable(b.role)+" carries, on "+where)
			continue
		}
		if !carries {
			continue
		}
		for _, m := range b.members {
			if strings.HasPrefix(m, "deleted:") {
				continue
			}
			members = append(members, deployer{member: m, role: b.role})
		}
	}
	return members, unread
}

// roleCarries reports whether role carries perm: a predefined role of the
// table by name, a custom one from its own definition. errText is set when a
// custom role could not be read, so the caller can say so rather than count
// the binding as one that carries nothing.
func roleCarries(role string, predefined []string, perm string, roles *iamRoles) (bool, string) {
	if slices.Contains(predefined, role) {
		return true, ""
	}
	if !isCustomRole(role) {
		return false, ""
	}
	perms, errText := roles.permissions(role)
	if errText != "" {
		return false, errText
	}
	return slices.Contains(perms, perm), ""
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
