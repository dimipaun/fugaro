package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	iam "google.golang.org/api/iam/v1"
	runapi "google.golang.org/api/run/v2"
	storage "google.golang.org/api/storage/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// doctorOrchestratorIdentity is the orchestrator-identity check: the resident
// orchestrator's actual IAM, read back against the table
// docs/mobile/design/always-on-orchestrator.md section 4.3 fixes as the whole
// of what the account may hold (mobile plan task P5.1b).
//
// Why it is a doctor check at all, when the plan's live check (design section 7,
// "the identity is what it claims") measures the same account. The live check
// needs a real project and a real phone and is owner-run; this one runs where
// the account is created and where every other IAM drift is reported, and it
// answers the question the plan review can only answer about a PLAN: the cover
// refuses a plan that widens the account, and nothing else reads what the
// account actually holds afterwards. The two absences the design names are the
// sharpest, and both are rows a later apply or a person's own grant could add:
// `roles/firebasedatabase.viewer` on the Firebase project — the IAM read that
// bypasses the Realtime Database rules and reaches every mailbox of the
// installation, which is what membership of var.launchers buys (section 6 rule
// 11) — and any role carrying setIamPolicy, which would let the account widen
// its own envelope.
//
// It is read-only, like every check of this class: it reads the account, four
// policies and the jobs' own policies, and it changes nothing. It prints, it
// never asks, and there is no branch of it that waits for anything.
//
// The table is checked in both directions. A row MISSING is an account that
// cannot do what section 4.3 says it does — no launch, no `fugaro ls`/`watch`/
// `cancel`, no mailbox write, or no mint — and a row EXTRA is a reach the
// design has not priced, on the one account whose whole point is that its
// envelope is enumerated. The roles are compared as they are BOUND rather than
// as they resolve, so a predefined role that happens to carry the same
// permissions is not the row: the design's table names its sources, and a
// substitution is a change of source rather than a spelling of one.
//
// What it does not read, and says so in its own line: folder- and
// organization-inherited bindings and deny policies (nothing in the installation
// is granted there), and — for an installation whose Firebase backend is not in
// the local config, or a configuration that reads no Cloud Run or no storage —
// the rows that live in those scopes. An unread row is named as unread, never
// counted as absent: a check that answered "the token-minter row is missing"
// because it could not read the policy would send an owner to re-apply an
// installation that is fine.
func doctorOrchestratorIdentity(ctx context.Context, lc *localcfg.Config, c *crm.Service) []doctorCheck {
	const id = "orchestrator-identity"
	if lc.Endpoints.NoAuth && lc.Endpoints.IAM == "" {
		return nil
	}
	account := infra.OrchestratorAccountID + "@" + lc.GCPProject + ".iam.gserviceaccount.com"
	psa := pluginwire.Printable(account)
	unread := func(what string, err error) []doctorCheck {
		return []doctorCheck{{ID: id, Severity: "warning", unread: true,
			Problem: "could not check the resident orchestrator's identity: " + what + " was not read (" + oneLineCLI(err.Error()) + "), so what " + psa + " holds is unknown rather than absent",
			Fix:     "rerun fugaro doctor with access to project " + pluginwire.Printable(lc.GCPProject) + ", or as an owner of it"}}
	}
	iamSvc, err := iam.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.IAM)...)
	if err != nil {
		return unread("the service account", err)
	}
	if _, err := iamSvc.Projects.ServiceAccounts.Get("projects/" + lc.GCPProject + "/serviceAccounts/" + account).Context(ctx).Do(); err != nil {
		if !isNotFound(err) {
			return unread("the service account", err)
		}
		// `fugaro update` gives every installation that can carry one its
		// resident orchestrator (update_orchestrator.go), so the account's absence
		// is a finding exactly where an update should have made it: an installation
		// whose config records both the credentials service (the account's caller
		// class) and the Firebase backend (what its process reads). Anywhere else the
		// installation cannot carry one, and the line says why instead of asking for
		// an update that would only repeat it.
		if lc.CredentialsService != nil && lc.Budget != nil && lc.Budget.FirebaseProject != "" && lc.Budget.RTDBURL != "" {
			return []doctorCheck{{ID: id, Severity: "warning",
				Problem: "this installation has no resident orchestrator: " + psa + " is not in " + pluginwire.Printable(lc.GCPProject) + ", though the installation has the credentials service and the Firebase backend one needs, so the update that gives it one has not run to the end here",
				Fix:     "run fugaro update: it writes the orchestrator's block and applies it, and nothing else is needed"}}
		}
		return []doctorCheck{{ID: id, Severity: "info",
			Problem: "this installation has no resident orchestrator: " + psa + " is not in " + pluginwire.Printable(lc.GCPProject) + ", and the installation has no " + orchestratorMissing(lc) + " for one to use, so no row of the design's section 4.3 table exists to check",
			Fix:     "no action: fugaro update gives the installation a resident orchestrator once it has the credentials service and the Firebase backend, and says on every run what is still missing"}}
	}

	var (
		got     []orchestratorGrant
		notRead []string
	)
	pol, err := projectPolicy(ctx, lc, c)
	if err != nil {
		return unread("the installation project's IAM policy", err)
	}
	got = append(got, orchestratorGrantsIn(crmBindings(pol.Bindings), account, "the installation project")...)

	fp := ""
	if lc.Budget != nil {
		fp = lc.Budget.FirebaseProject
	}
	if fp != "" && fp != lc.GCPProject {
		p, err := c.Projects.GetIamPolicy(fp, &crm.GetIamPolicyRequest{Options: &crm.GetPolicyOptions{RequestedPolicyVersion: 3}}).Context(ctx).Do()
		switch {
		case err == nil:
			got = append(got, orchestratorGrantsIn(crmBindings(p.Bindings), account, "the Firebase project")...)
		case isAccessDenied(err):
			notRead = append(notRead, "the Firebase project's own policy, which is where the launcher list's rules-bypassing database read would appear")
		default:
			notRead = append(notRead, "the Firebase project's own policy")
		}
	} else if fp == "" {
		notRead = append(notRead, "the Firebase project's policy (this configuration names no Firebase backend)")
	}

	bucket := lc.RunsBucketName()
	switch {
	case bucket == "":
		notRead = append(notRead, "the runs bucket's policy (this configuration names no runs bucket)")
	case lc.Endpoints.NoAuth && lc.Endpoints.Storage == "":
		notRead = append(notRead, "the runs bucket's policy (this configuration reads no storage)")
	default:
		// The two bucket rows are the write under the launchers' condition and
		// the unconditioned read, so the condition's bytes are read here
		// exactly as the module pins them (gcp.LauncherBucketCondition).
		svc, err := storage.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Storage)...)
		if err != nil {
			notRead = append(notRead, "the runs bucket's policy")
			break
		}
		p, err := svc.Buckets.GetIamPolicy(bucket).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
		if err != nil {
			notRead = append(notRead, "the runs bucket's policy")
			break
		}
		got = append(got, orchestratorGrantsIn(storageBindings(p.Bindings), account, "the runs bucket")...)
	}

	signer := ""
	if fp != "" {
		signer = infra.SignerAccountID + "@" + fp + ".iam.gserviceaccount.com"
		if lc.Budget != nil && lc.Budget.TokenSigner != "" {
			signer = lc.Budget.TokenSigner
		}
		p, err := iamSvc.Projects.ServiceAccounts.GetIamPolicy("projects/" + fp + "/serviceAccounts/" + signer).
			OptionsRequestedPolicyVersion(3).Context(ctx).Do()
		switch {
		case err == nil:
			got = append(got, orchestratorGrantsIn(iamBindings(p.Bindings), account, "the signer account")...)
		case isNotFound(err), isAccessDenied(err):
			notRead = append(notRead, "the signer account's policy ("+pluginwire.Printable(signer)+")")
		default:
			notRead = append(notRead, "the signer account's policy")
		}
	} else {
		notRead = append(notRead, "the signer account's policy (this configuration names no signer)")
	}

	// The launch itself, which lives per job rather than per project: what the
	// account holds is the job runner's custom role on the installation's jobs,
	// and any other role on a job is a reach the table does not have. Unlike
	// every other scope, a count is not the question here — an installation
	// with no project job has nothing to launch and the account holds nothing —
	// so what a job row is checked for is its ROLE, and the count is reported.
	listed, jread := 0, 0
	if lc.Endpoints.NoAuth && lc.Endpoints.Run == "" {
		notRead = append(notRead, "the Cloud Run jobs' policies (this configuration reads no Cloud Run)")
	} else if svc, err := runapi.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Run)...); err != nil {
		notRead = append(notRead, "the Cloud Run jobs' policies")
	} else if jobs, err := doctorListedJobs(ctx, svc, doctorJobsParent(lc)); err != nil {
		notRead = append(notRead, "the Cloud Run jobs' policies")
	} else {
		listed = len(jobs)
		for _, lj := range jobs {
			p, err := svc.Projects.Locations.Jobs.GetIamPolicy(lj.Name).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
			if err != nil {
				notRead = append(notRead, "the IAM policy of a Cloud Run job (the caller may not read it)")
				continue
			}
			jread++
			got = append(got, orchestratorGrantsIn(runBindings(p.Bindings), account, "a Cloud Run job")...)
		}
	}

	want := []orchestratorGrant{
		{scope: "the installation project", role: "projects/" + lc.GCPProject + "/roles/" + infra.RoleLauncher},
	}
	if fp != "" {
		want = append(want, orchestratorGrant{scope: "the signer account", role: "projects/" + fp + "/roles/" + infra.MinterRoleID})
	}
	if bucket != "" {
		want = append(want,
			orchestratorGrant{scope: "the runs bucket", role: "roles/storage.objectViewer"},
			orchestratorGrant{scope: "the runs bucket", role: "roles/storage.objectUser", cond: gcp.LauncherBucketConditionTitle, expr: gcp.LauncherBucketCondition(bucket)})
	}

	// The job rows are checked for their role and counted, never compared as a
	// set: the account holds the runner role on the installation's jobs, whose
	// names this check does not know, and one row per job is the shape the root
	// creates.
	var (
		jobRows, otherJobRows []string
		events                []orchestratorGrant
	)
	for _, g := range got {
		if g.scope != "a Cloud Run job" {
			events = append(events, g)
			continue
		}
		if g.role == "projects/"+lc.GCPProject+"/roles/"+infra.RoleJobRunner && g.cond == "" {
			jobRows = append(jobRows, g.role)
			continue
		}
		otherJobRows = append(otherJobRows, g.show())
	}
	missing, extra := orchestratorDifference(want, events)

	// The bucket's write bound under another condition is neither the row nor a
	// missing one: it is the same role fenced differently, and it reads as both
	// an extra and a missing row. Said as one finding rather than two, because
	// what the owner has to see is the fence and not the role's name twice —
	// and it is the finding that matters most of all of them: under any other
	// condition the account writes wherever that condition reaches, and the
	// launchers' condition is what withholds the bucket root, `cache/`,
	// `locks/` and `builds/` (design section 6 rule 2).
	var (
		rewrites []string
		remains  []string
	)
	for _, m := range missing {
		if m.scope == "the runs bucket" && m.role == "roles/storage.objectUser" {
			var other []string
			for _, e := range extra {
				if e.scope == "the runs bucket" && e.role == "roles/storage.objectUser" {
					other = append(other, e.show())
				}
			}
			if len(other) > 0 {
				rewrites = append(rewrites, "the runs bucket's write is bound as "+strings.Join(other, " and ")+" rather than under the launchers' own condition ("+gcp.LauncherBucketConditionTitle+"), so the account writes wherever that condition reaches — the launchers' is what withholds the bucket root, cache/, locks/ and builds/")
				var kept []orchestratorGrant
				for _, e := range extra {
					if e.scope == "the runs bucket" && e.role == "roles/storage.objectUser" {
						continue
					}
					kept = append(kept, e)
				}
				extra = kept
				continue
			}
		}
		remains = append(remains, m.show())
	}

	var findings []string
	if jread > 0 && listed > 0 && len(jobRows) == 0 {
		findings = append(findings, fmt.Sprintf("it holds the job runner's role on none of the %d Cloud Run %s of this project, so it cannot start a run", listed, plural(listed, "job", "jobs")))
	}
	if len(remains) > 0 {
		findings = append(findings, "missing "+strings.Join(remains, ", "))
	}
	if len(otherJobRows) > 0 {
		findings = append(findings, "a Cloud Run job carrying "+strings.Join(otherJobRows, ", ")+" rather than the job runner's role")
	}
	findings = append(findings, rewrites...)
	for _, e := range extra {
		if neverRole(e.role) {
			findings = append(findings, e.show()+" — a role the launcher list's own membership grants, so the account has been added to var.launchers rather than granted its rows one binding at a time (design section 6 rule 11: the database read here bypasses the Realtime Database rules and reaches every mailbox of the installation)")
			continue
		}
		findings = append(findings, e.show())
	}

	head := psa + " does not hold the design's section 4.3 table"
	verdict := func(sev, problem, fix string) []doctorCheck {
		out := []doctorCheck{{ID: id, Severity: sev, Problem: problem, Fix: fix}}
		if len(notRead) > 0 {
			out = append(out, doctorCheck{ID: id + "-not-read", Severity: "info", unread: true,
				Problem: "what this check did not read: " + strings.Join(notRead, ", ") + " — an unread scope is unknown here, not absent; folder- and organization-inherited bindings and deny policies are read by nothing in this installation",
				Fix:     "no action"})
		}
		return out
	}
	table := "the launcher's project role (run.jobs.get/list, run.executions.get/list/cancel, run.operations.get and the two secret-list permissions), run.jobs.run and run.jobs.runWithOverrides on the installation's Cloud Run jobs, the runs bucket's write under the launchers' condition and its unconditioned read, and the token-minter role on the signer account"
	if len(findings) == 0 {
		reach := " — the jobs' own policies were not read, so the launch reach is unknown rather than absent"
		if jread > 0 {
			reach = fmt.Sprintf(" — the launch reach is on %d of this project's %d Cloud Run %s", len(jobRows), listed, plural(listed, "job", "jobs"))
		}
		summary := psa + " holds exactly the design's section 4.3 table: " + table + reach
		if len(notRead) == 0 {
			return []doctorCheck{{ID: id, OK: true, Problem: summary}}
		}
		return verdict("info", summary, "no action")
	}
	return verdict("warning", head+": "+strings.Join(findings, "; ")+". The table is the design's whole answer for this account — a row missing is a launch, a `fugaro ls`, a mailbox write or a mint the resident orchestrator cannot make, and a row beside it is a reach the design has not priced",
		"review the bindings above before trusting the account: `fugaro update` in "+pluginwire.Printable(lc.GCPProject)+" re-applies the rows section 4.3 has and removes none, so a row the table does not have is one to remove by hand (gcloud projects get-iam-policy "+pluginwire.Printable(lc.GCPProject)+" shows the project's policy; the bucket's and each job's own policies carry theirs)")
}

// orchestratorGrant is one grant the check found on the account, in the shape
// the design's table is written in: the scope the binding is on, the role, and
// the condition when the row carries one.
type orchestratorGrant struct {
	scope string
	role  string
	cond  string
	expr  string
}

// show is the grant as one clause of a finding, with everything read from IAM
// made printable: a role or a condition is a name a policy carries, and a
// doctor line is a terminal's.
func (g orchestratorGrant) show() string {
	s := pluginwire.Printable(g.role) + " on " + g.scope
	if g.cond != "" || g.expr != "" {
		s += " under the condition " + pluginwire.Printable(g.cond) + " (" + pluginwire.Printable(oneLineCLI(g.expr)) + ")"
	}
	return s
}

// policyBinding is one binding of any of the four policies this check reads,
// in the one shape the comparison works on: Resource Manager's, IAM's, Cloud
// Storage's and Cloud Run's own binding types are four different structs with
// the same four fields.
type policyBinding struct {
	role    string
	members []string
	cond    string
	expr    string
}

func crmBindings(bs []*crm.Binding) []policyBinding {
	out := make([]policyBinding, 0, len(bs))
	for _, b := range bs {
		if b == nil {
			continue
		}
		p := policyBinding{role: b.Role, members: b.Members}
		if b.Condition != nil {
			p.cond, p.expr = b.Condition.Title, b.Condition.Expression
		}
		out = append(out, p)
	}
	return out
}

func storageBindings(bs []*storage.PolicyBindings) []policyBinding {
	out := make([]policyBinding, 0, len(bs))
	for _, b := range bs {
		if b == nil {
			continue
		}
		p := policyBinding{role: b.Role, members: b.Members}
		if b.Condition != nil {
			p.cond, p.expr = b.Condition.Title, b.Condition.Expression
		}
		out = append(out, p)
	}
	return out
}

func iamBindings(bs []*iam.Binding) []policyBinding {
	out := make([]policyBinding, 0, len(bs))
	for _, b := range bs {
		if b == nil {
			continue
		}
		p := policyBinding{role: b.Role, members: b.Members}
		if b.Condition != nil {
			p.cond, p.expr = b.Condition.Title, b.Condition.Expression
		}
		out = append(out, p)
	}
	return out
}

func runBindings(bs []*runapi.GoogleIamV1Binding) []policyBinding {
	out := make([]policyBinding, 0, len(bs))
	for _, b := range bs {
		if b == nil {
			continue
		}
		p := policyBinding{role: b.Role, members: b.Members}
		if b.Condition != nil {
			p.cond, p.expr = b.Condition.Title, b.Condition.Expression
		}
		out = append(out, p)
	}
	return out
}

// orchestratorGrantsIn is the bindings of a policy that name the account, as
// grants of the scope they were found in.
func orchestratorGrantsIn(bs []policyBinding, account, scope string) []orchestratorGrant {
	member := "serviceAccount:" + account
	var out []orchestratorGrant
	for _, b := range bs {
		if !slices.Contains(b.members, member) {
			continue
		}
		out = append(out, orchestratorGrant{scope: scope, role: b.role, cond: b.cond, expr: b.expr})
	}
	return out
}

// orchestratorDifference is want minus got and got minus want, as sets: the two
// lists the finding is read out of. Nothing here is ordered — IAM answers a
// policy's bindings in no order a check may rely on.
func orchestratorDifference(want, got []orchestratorGrant) (missing, extra []orchestratorGrant) {
	seen := func(list []orchestratorGrant) map[orchestratorGrant]bool {
		m := map[orchestratorGrant]bool{}
		for _, g := range list {
			m[g] = true
		}
		return m
	}
	wantSet, gotSet := seen(want), seen(got)
	for _, g := range want {
		if !gotSet[g] {
			missing = append(missing, g)
		}
	}
	for _, g := range got {
		if !wantSet[g] {
			extra = append(extra, g)
		}
	}
	return missing, extra
}

// neverRole is the three roles the design's eleventh rule names as what
// membership of var.launchers would add: the Firebase project's database
// viewer, whose IAM read bypasses the Realtime Database rules and reaches every
// mailbox of the installation, the datastore viewer and the service-usage
// consumer. A check that finds one of them is looking at a joined list rather
// than at a widened row.
func neverRole(role string) bool {
	switch role {
	case "roles/firebasedatabase.viewer", "roles/datastore.viewer", "roles/serviceusage.serviceUsageConsumer":
		return true
	}
	return false
}

// orchestratorMissing names what an installation lacks of the two things a
// resident orchestrator needs, for the doctor's info line.
func orchestratorMissing(lc *localcfg.Config) string {
	noService := lc.CredentialsService == nil
	noBackend := lc.Budget == nil || lc.Budget.FirebaseProject == "" || lc.Budget.RTDBURL == ""
	switch {
	case noService && noBackend:
		return "credentials service or Firebase budget backend recorded"
	case noService:
		return "credentials service recorded"
	}
	return "Firebase budget backend recorded"
}
