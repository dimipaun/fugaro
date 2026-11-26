package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	runapi "google.golang.org/api/run/v2"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/policy"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/secretname"
	"github.com/dimipaun/fugaro/internal/task"
)

// The secrets-brokering checks (plan B1.9, decisions D503b-d): the
// installation's credentials service and who may call it, the Bitbucket token
// the service holds and the token a job still mounts itself, how old the
// stored credentials are, and the model credentials a workflow's agent can
// reach.
//
// doctor runs as the operator, and it is no job account: the service refuses a
// caller it cannot bind to a repository before it reads anything, so what the
// service is and who may call it is read from the Cloud Run admin API and the
// IAM policies the module that deploys it writes, never by calling it (F20).
//
// Two of the checks read a VALUE — the bundle's Bitbucket token — and both are
// behind --check-bitbucket-token, which is refused in a coding agent's session:
// `creds ls` reads the same document with the operator's own identity for the
// same reason. The value is used in memory, to build one HTTP request per
// proxied repository, and is never printed, logged, put in an argument or
// carried by an error.
// checkBitbucketTokenFlag is the flag that allows the bundle's read.
const checkBitbucketTokenFlag = "--check-bitbucket-token"

// runInvokeRoles are the predefined roles whose permissions include
// run.routes.invoke, the one permission that admits a caller to a Cloud Run
// service. They are recognized by name the way doctorSecretManagerChecks' own
// lists are (a role of Google's name is Google's to define, and a project that
// defined a role of the same name could answer for it). The workflow module
// grants each job account
// roles/run.invoker on the service and nothing else (modules/workflow/sa.tf),
// which is the first entry; the rest are named because an installation that
// granted one of them instead would still be admitted by Cloud Run IAM, and a
// check that called that missing would send the operator to re-apply a
// repository that works.
//
// They are matched by NAME and never expanded through the IAM API: a doctor
// run resolves a custom role once, for the reads that need it
// (TestDoctorSecretManagerChecksReadsACustomRoleOnce), and this check is not
// worth a second answer to a question doctor already asked. A custom role on
// the service's policy is therefore reported as not read rather than as a role
// that carries nothing.
var runInvokeRoles = []string{"roles/run.invoker", "roles/run.admin", "roles/run.developer", "roles/owner", "roles/editor"}

// secretExpiryWarnWithin is how long before the expiry an operator recorded for
// a secret doctor starts warning. A rotation is a manual step and a re-apply,
// and the date is one the operator typed rather than one Fugaro can read
// (Bitbucket shows an access token's expiry in its UI and its API does not
// expose it: U3, F19), so the warning is the operator's own reminder to act.
const secretExpiryWarnWithin = 14 * 24 * time.Hour

// doctorNow is the clock the age checks measure against. Tests replace it,
// since Secret Manager's own version times are the server's.
var doctorNow = time.Now

// bitbucketAPIURL is the API root the token check asks when the installation
// lists none of its own, and bitbucketAPIClient the HTTP client it uses; tests
// replace both. A root the installation does list is asked instead of this one
// (bitbucketTokenRoots), and the root is never whatever an environment variable
// names: a run's root is the published policy's to choose (bitbucket_api_roots,
// internal/runner's checkBitbucketAPIRoot), and a check that reads a credential
// with the operator's own identity asks the forge the token was issued for.
var (
	bitbucketAPIURL    = bitbucket.DefaultBaseURL
	bitbucketAPIClient *http.Client
)

// refuseBitbucketTokenReadInAgent is the refusal of the flag under a coding
// agent's environment, before anything is read.
func refuseBitbucketTokenReadInAgent(getenv func(string) string) error {
	return refuseKeyReadInAgent(getenv, checkBitbucketTokenFlag, "the credentials service's bundle, which holds the Bitbucket token")
}

// credentialsServiceDeploys is the set of workflow variants repo's own root
// deploys a job for — the variants a job account, and so a roles/run.invoker
// grant, can exist for — or nil where the local config does not carry enough
// to say which those are.
//
// A repository whose entry records the switch ON is one whose own apply was
// made with an auth the switch serves: infra.CredentialsServiceServes is what
// wrote that record, and its auth condition admits the cascade always and
// `api-key` or `oauth` under a gateway mode (B2.3). The list is
// infra.WorkflowAccounts', the function RepoSpec deploys the jobs from, asked
// with the cascade rather than with a rule restated here — under cascade it is
// the default variant of each workflow and no other, so the table's named
// variants are variants this repository has no job, and no job account, for.
// The cascade is the one auth this reads for a switched repository, and the
// local config is the INSTALLATION's side, which never holds a repository's
// fugaro.yaml: a switched `api-key` or `oauth` repository whose own file names
// Claude accounts may have deployed the variants of those accounts too, and
// this then counts none of them. That is the direction this row is written to
// fail in: a variant it does not count is one it does not warn about, where
// counting one the root never deployed is the false warning the live Check 191
// stopped on.
//
// Every other repository — record false, or no record at all, which the
// switch's readers treat alike — may be running under any agent.auth: the auth
// is not knowable here, so the answer is nil and the row keeps the service's
// whole table for that repository, which is what it compared before. That is
// not a guess about the repository: the sentence the row prints says in its own
// words that the switch follows the repository's own agent.auth, and a
// repository the service is held off for is named there so an operator can see
// which one the fix has to be run for.
func credentialsServiceDeploys(lc *localcfg.Config, repo string) map[string]bool {
	r, ok := lc.Repos[repo]
	if !ok || !r.CredentialsServiceOn() {
		return nil
	}
	cfg := &config.Config{Agent: config.Agent{Auth: config.AuthCascade}}
	out := map[string]bool{}
	for _, w := range r.Workflows {
		for _, acct := range infra.WorkflowAccounts(cfg, lc, repo) {
			out[gcp.WorkflowVariant(w, acct)] = true
		}
	}
	return out
}

// doctorCredentialsServiceDeployed is doctor's "credentials-service-deployed"
// check: what the installation's credentials service is, said in one line —
// converged on or not, deployed or not, and, when it is deployed and every job
// account may invoke it, whether it has anything to serve with.
//
// The ruling of 2026-11-05 (docs/credentials-service/default-on.md) is what
// makes the states behind that one AHEAD of it doctor's business: the release
// ends with the service deployed on every installation and a run switched to it
// only when it can serve that run, so an installation that carries no service at
// all (DON1's own state: `fugaro update` deploys one on every installation that
// can hold it — it needs the installation's Firebase backend and at least one
// repository job), one whose bundle holds no version (or only one that is
// switched off) and one whose verifier list is empty are states a release leaves
// behind rather than faults in an installation. The first is what DON1 turns the
// opt-in into, and that is what its one line has to say: nothing there is broken
// and nothing is behind, an installation that cannot carry the service runs the
// way it did before the service existed. That state and
// the emptied verify route are measurements: one line each, --all and --json
// are where a reader who wants the service used finds which half is missing,
// and neither is an item of the default view. The other four are warnings: the
// block written and no service behind it, a job account the service does not
// admit, and the two where the service has nothing to serve with (an empty
// bundle, a newest version Secret Manager will
// not serve) all refuse something a run needs, so they print and --strict is
// where they become a failure.
//
// The no-service state's fix is not an instruction to run something: DON1's
// deploy is the ordinary `fugaro update`, and it switches nothing on by
// itself — a run reaches the service only where the bundle holds what its auth
// needs and that repository's own root has been re-applied with the switch — so
// the line says what the deploy does and what the switch then costs the
// repository rather than presenting a run that can break workflows as the
// routine step a doctor line reads as. What it costs is not the same sentence
// for every auth: the switch admits api-key and oauth repositories too (B2.3),
// the flip (B2.7) is what drops their two Claude mounts — a provider key
// staying with the job (R6, D1634) — and a GitHub run of such a repository
// refuses to mint until the bundle holds the App's key. The two states whose
// fix is a re-apply
// name the same ordinary command: NOT_FOUND, where the installation root is
// re-applied and the service created, and the empty verify route, whose list is
// written into the service by that apply and not by a publish.
//
// Both halves of the deployed service's state are read from the service's own
// IAM policy, which is where the module writes the grant: one member, the
// job's own account, roles/run.invoker on that one service. The project's
// policy is deliberately not read a second time — doctor reads it once, for
// the reads that ask it a question no other answer covers
// (TestDoctorSecretManagerChecksReadsTheProjectPolicyOnce) — so a job account
// granted the role at project level instead reads as missing, which is not
// what an installation writes and is said in the fix. A service that is not
// there is answered by Cloud Run as NOT_FOUND, which is the only way to see
// it: the fake this check is tested against serves no service get, and the
// real API's get is the same question asked twice.
//
// It is a warning and not an ordinary failure where it is a warning at all: a
// run whose job cannot invoke the service cannot fetch a model credential, but
// an installation mid-opt-in (the block written, the apply that creates the
// service still to come) is a legitimate state, and --strict is where that is
// a failure.
//
// The table it compares is the service's own (infra.CredentialsJobAccounts),
// which is deliberately every account a repository MIGHT run under: the
// service is the fail-closed side, and a row for an account no job exists for
// costs it nothing, while a missing row refuses a real caller. The grants are
// not that table: they are one per job a repository's root deploys
// (modules/workflow/sa.tf), so a row whose repository deploys no such job can
// never hold one and is not a missing grant. credentialsServiceDeploys is
// where the two are told apart, and it is what keeps the line from naming a
// switched repository for a variant its cascade root does not deploy.
func doctorCredentialsServiceDeployed(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	const id = "credentials-service-deployed"
	if lc.CredentialsService == nil {
		return []doctorCheck{{ID: id, OK: true, Severity: "info",
			Problem: fmt.Sprintf("the installation has no credentials service (%s): an ordinary %s deploys one on every installation that can hold it — it needs the installation's Firebase backend and at least one repository job — and writes the block this check reads, so this installation has either not run an update of this release or cannot carry one: either way its runs fetch their model credential the way they did before the service existed, and nothing here is broken or behind", infra.CredentialsServiceName, selfCommand()+" update"),
			Fix:     "nothing to run for this: where " + selfCommand() + " update does deploy one, the deploy alone switches nothing on — a run reaches it only where the bundle holds the credential its auth needs and that repository's own root has been re-applied with the switch, which a cascade repository of any installation that has a service is on, and an api-key or oauth one only where its budget mode is observe or enforce and the bundle holds that credential (the credential is dropped from the job's mounts by the apply that turns the switch on: from the flip a switched api-key or oauth repository's job mounts neither the Anthropic key nor the Claude OAuth token, its provider keys staying its own), and a GitHub run of such a repository refuses to mint until the bundle holds the App's key (" + selfCommand() + " creds set github-app-key)"}}
	}
	unreadable := func(what string) []doctorCheck {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the credentials service could not be read. The installation has opted into the credentials service (%s), and %s could not be read: whether it is deployed and whether its callers may invoke it could not be checked", infra.CredentialsServiceName, what),
			Fix:     "rerun doctor with your credentials and the Cloud Run API available, or check by hand: " + selfCommand() + " update re-applies the installation and its service"}}
	}
	jobs, err := infra.CredentialsJobAccounts(lc)
	if err != nil {
		return unreadable("the job accounts that may call it (" + oneLineCLI(err.Error()) + ")")
	}
	if lc.Endpoints.NoAuth && lc.Endpoints.Run == "" {
		return unreadable("the Cloud Run service " + infra.CredentialsServiceName + ", in a test configuration")
	}
	svc, err := runapi.NewService(ctx, doctorAPIOpts(lc, lc.Endpoints.Run)...)
	if err != nil {
		return unreadable("the Cloud Run API")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/services/%s", lc.GCPProject, lc.Region, infra.CredentialsServiceName)
	pol, err := svc.Projects.Locations.Services.GetIamPolicy(name).OptionsRequestedPolicyVersion(3).Context(ctx).Do()
	var ge *googleapi.Error
	if err != nil {
		if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
			return []doctorCheck{{ID: id, Severity: "warning",
				Problem: fmt.Sprintf("the credentials service is not deployed in this project. The local config opted this installation into the credentials service (%s), and Cloud Run has no service %s in project %s, region %s: every run of it would fail to fetch a model credential", infra.CredentialsServiceName, infra.CredentialsServiceName, pluginwire.Printable(lc.GCPProject), pluginwire.Printable(lc.Region)),
				Fix:     selfCommand() + " update (re-applies the installation root, which creates the service)"}}
		}
		return unreadable("the IAM policy of the Cloud Run service " + infra.CredentialsServiceName + " (" + oneLineCLI(err.Error()) + ")")
	}
	bindings := bindingsOf(pol.Bindings, runBindingFields)
	var (
		missing []string
		// missingRepos is the repositories those accounts belong to, once each
		// and in the order they first appear: one repository may have several
		// workflows whose accounts the service does not admit, and the fix is
		// one command per repository, not one per workflow.
		missingRepos []string
		unread       []string
	)
	deployed := map[string]map[string]bool{}
	for _, j := range jobs {
		if _, cached := deployed[j.Repo]; !cached {
			deployed[j.Repo] = credentialsServiceDeploys(lc, j.Repo)
		}
		if set := deployed[j.Repo]; set != nil && !set[j.Workflow] {
			continue
		}
		slug, err := task.Slug(j.Provider, j.Repo)
		if err != nil {
			// A repository whose provider the local config does not name: the
			// same entry refuses the credentials stage, which is where it is
			// worth saying, and a job account cannot be derived here.
			continue
		}
		member := "serviceAccount:" + gcp.ServiceAccountID(slug, j.Workflow) + "@" + lc.GCPProject + ".iam.gserviceaccount.com"
		granted, unknown := invokerGranted(bindings, member)
		switch {
		case granted:
		case unknown:
			unread = append(unread, pluginwire.Printable(j.Repo)+" ("+pluginwire.Printable(j.Workflow)+")")
		default:
			missing = append(missing, pluginwire.Printable(j.Repo)+" ("+pluginwire.Printable(j.Workflow)+")")
			if !slices.Contains(missingRepos, j.Repo) {
				missingRepos = append(missingRepos, j.Repo)
			}
		}
	}
	switch {
	case len(missing) == 0 && len(unread) == 0:
		return doctorCredentialsServiceServing(ctx, lc, id)
	case len(missing) == 0:
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the credentials service's invoker grant could not be read. The credentials service %s is deployed, and whether %s may invoke it could not be checked: a custom role on its or the project's policy could not be read", infra.CredentialsServiceName, strings.Join(unread, ", ")),
			Fix:     "rerun doctor with the IAM API available, or read the policy yourself: gcloud run services get-iam-policy " + infra.CredentialsServiceName}}
	}
	// The fix names the other half of the grant as well, because an operator
	// who reads the command alone would run it at a repository the switch is
	// held off for and get no grant: the grant is written by a repository's own
	// root and only where CredentialsServiceServes says the switch is on, so a
	// repository has to hold both conditions the switch adds for its own auth
	// first — the mode, for the two auths the service serves besides the
	// cascade, and the bundle entry that auth's runs are served under. That
	// state is worth saying in the problem too, and it is the one where the
	// published policy's proxy: true reads as a promise the installation
	// keeps: nothing about such a repository is brokered, and its runs hold the
	// Bitbucket token on their own job.
	// The fix names the repositories rather than a placeholder, and the sentence
	// above it names them too, because both are the check's own and the one
	// screen prints only these two lines: a fix saying "each repository named
	// above" pointed at names no line printed. Each name goes in the words
	// around the command and never as its argument: update
	// --repo takes the checkout's PATH, so a name there is refused
	// (loadCheckoutResolved, image.go); the command that runs is --repo alone,
	// from inside that repository's checkout, which is what doctor.go and
	// doctor_bucket.go say too. One command serves a repository however many of
	// its workflows are missing, and missingRepos holds each repository once.
	names := make([]string, len(missingRepos))
	for i, repo := range missingRepos {
		names[i] = pluginwire.Printable(repo)
	}
	where := "in the checkout of each of " + strings.Join(names, ", ")
	sentence := "the credentials service does not admit job accounts of " + strings.Join(names, ", ") + "."
	if len(names) == 1 {
		where = "in the checkout of " + names[0]
		sentence = "the credentials service does not admit " + names[0] + "'s job account."
	}
	return []doctorCheck{{ID: id, Severity: "warning",
		Problem: fmt.Sprintf("%s The credentials service %s is deployed and grants no %s to %s: a run of %s that is on the switch would fail to fetch a model credential%s. The grant follows the switch, and the switch follows the repository's own agent.auth: a repository that runs with agent.auth: cascade reaches the service wherever the installation has one, and one that runs with agent.auth api-key or oauth reaches it only where its budget mode is observe or enforce AND the service's bundle holds the credential its auth needs — %s for api-key, and the token of every account its workflows may run under for oauth. So a repository of that list that is held off has no grant: the published policy's proxy: true is inert for it — it brokers nothing, and its runs hold their own Bitbucket token instead of being served one",
			sentence, infra.CredentialsServiceName, runInvokeRoles[0], strings.Join(missing, ", "), strings.Join(missing, ", "),
			noteIfAny("; not read: "+strings.Join(unread, ", "), len(unread) > 0), config.AnthropicSecret),
		Fix: selfCommand() + " update --repo " + where + " (grants each of its job accounts " + runInvokeRoles[0] + " on the service). For one of them the switch is held off until it holds: an api-key or oauth repository needs its budget mode at observe or enforce and the service's bundle to hold the credential its auth needs (" + selfCommand() + " creds set " + config.AnthropicSecret + " for api-key; " + selfCommand() + " creds set " + config.ClaudeOAuthTokenPrefix + "<account> for oauth), and a repository of any other agent.auth has to run with agent.auth: cascade first (with a model credential in the service's bundle, " + selfCommand() + " creds set). Until then the switch is off for that repository, proxy: true is inert, and its runs hold their own Bitbucket token"}}
}

// doctorCredentialsServiceServing is the rest of
// doctorCredentialsServiceDeployed's one line: the service is deployed and
// every job account may invoke it, and this is what it has to serve with — the
// bundle it reads a credential out of, and the verifier account the verify
// route admits.
//
// Both are read and never guessed, and each is read the way the thing that
// uses it reads it: the bundle through the container's own version metadata,
// which is two answers in one read — whether a version is there at all, and
// whether the newest of them is ENABLED, since a version that is not is one
// Secret Manager will not serve and so one the loader cannot read either (the
// value is never touched here, and never is: the document is the service's, and
// doctor reads a value only behind --check-bitbucket-token) — and the verify
// route through the list the service is deployed with —
// localcfg's one keep rule for the key, this checkout's verifier_accounts when
// it states one and the published policy node's otherwise, which is what the
// credentials stage hands the module's own variable and therefore what the
// service's verify route holds. Its three states are the module's: absent
// means the installation's own verifier account, and a list written EMPTY is
// the one state that admits nobody, which is why the empty list is what this
// reports rather than a key nobody wrote. The line names the layer that states
// the empty list, because that is the file an operator has to change.
//
// The node is read here and only here, and only when this checkout's config
// says nothing: an installation with no budget backend answers "nothing
// published" without a call, and every other state of the service returns
// before this one.
//
// Two of the four are warnings and two are measurements, and the line between
// them is whether a run can work around them. An installation that opted in and
// whose bundle holds nothing to serve out of — no version at all, or a newest
// version Secret Manager will not serve — has every model call of every run
// refused: a measurement there would print "everything checked is ok" and exit
// 0 under --strict for an installation whose runs all fail, which is the silent
// pass in the one place it matters most. Both keep the warning the block's own
// state has (a legitimate state — the opt-in is taken and the bundle is not
// ready yet — that is a failure under --strict and never without it). The
// installation that never opted in and the one whose verify route was emptied
// are states rather than faults — the first runs the way it did before the
// service existed, the second is a list someone wrote EMPTY on purpose — so
// they are measurements, and the one line each of them produces is where a
// reader who wants the service used learns which half is missing.
func doctorCredentialsServiceServing(ctx context.Context, lc *localcfg.Config, id string) []doctorCheck {
	// A read that did not answer is not a state of the service: it is said, and
	// it is said as the info note of a read that failed, which no view prints
	// and every view counts — a document nothing could read is never part of a
	// screen that says everything checked is ok.
	unreadable := func(what, question, fix string) []doctorCheck {
		return []doctorCheck{{ID: id, Severity: "info", unread: true,
			Problem: fmt.Sprintf("the credentials service %s is deployed and every job account may invoke it, and %s could not be read: %s", infra.CredentialsServiceName, what, question),
			Fix:     fix}}
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return unreadable("the bundle ("+oneLineCLI(err.Error())+")",
			"whether there is a document for it to serve a run's credential out of could not be checked",
			"rerun doctor with the Secret Manager API available, or read the bundle's names yourself: "+selfCommand()+" creds ls")
	}
	meta, err := store.newest(ctx)
	switch {
	case errors.Is(err, errNoBundleVersion):
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the credentials service has nothing in its bundle to serve a run. The credentials service %s is deployed and serving, and it admits nobody: every job account may invoke it, and the bundle it serves from (%s) holds no version, so the loader fails closed and every model call of a run against it is refused; %s is the writer", infra.CredentialsServiceName, pluginwire.Printable(store.id), selfCommand()+" creds set NAME"),
			Fix:     selfCommand() + " creds set NAME (the only writer of the bundle: no update writes a credential)"}}
	case err != nil:
		return unreadable("the bundle's newest version ("+oneLineCLI(err.Error())+")",
			"whether there is a document for it to serve a run's credential out of could not be checked",
			"rerun doctor with the Secret Manager API available, or read the bundle's names yourself: "+selfCommand()+" creds ls")
	}
	// The version's own state is the rest of that read, and it is not the empty
	// container: a version that is not ENABLED is there, its document is there,
	// and Secret Manager refuses to serve either — the loader reads `latest`
	// and is refused the same way, so the service has nothing to hand a run
	// however full the bundle is. `creds ls` and `creds set` read this same
	// metadata (newestIsDisabled, credsDisabledRefusal) and refuse it, which is
	// why the fix is the enable first: a `creds set` would refuse too until the
	// version is served again.
	if meta.state != "ENABLED" {
		fix := selfCommand() + " creds set NAME (writes a new version: no update writes a credential)"
		if meta.state == "DISABLED" {
			fix = fmt.Sprintf("gcloud secrets versions enable %s --secret=%s (the document is there; Secret Manager will not serve a disabled version), then %s creds set NAME writes a new one", pluginwire.Printable(meta.number), pluginwire.Printable(store.id), selfCommand())
		}
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the credentials service has nothing in its bundle to serve a run. The credentials service %s is deployed and serving, and it admits nobody: every job account may invoke it, and the newest version of the bundle it serves from (%s, version %s) is %s, so the loader's read of it is refused and every model call of a run against it fails", infra.CredentialsServiceName, pluginwire.Printable(store.id), pluginwire.Printable(meta.number), pluginwire.Printable(meta.state)),
			Fix:     fix}}
	}
	verifiers, published, err := doctorDeployedVerifierAccounts(ctx, lc)
	if err != nil {
		return unreadable("the verifier list the service is deployed with ("+oneLineCLI(err.Error())+")",
			"whether its verify route admits anyone could not be checked, and this checkout's config states no list of its own",
			"rerun doctor with the Realtime Database available, or state the list in this checkout's config (verifier_accounts), which is what the service is deployed from whether the node can be read or not, and re-apply it: "+selfCommand()+" update re-applies the service with it")
	}
	if verifiers != nil && len(verifiers) == 0 {
		where, fix := "this checkout's config writes verifier_accounts: []", fmt.Sprintf("name the installation's own verifier account in this checkout's config (%s@%s.iam.gserviceaccount.com) and re-apply it (%s update writes the service's list from this config), or take the key out of the config to keep the module's own account admitted", infra.VerifierAccountID, pluginwire.Printable(lc.GCPProject), selfCommand())
		if published {
			where = "the published policy's verifier_accounts is an empty list and this checkout's config states none"
			fix = fmt.Sprintf("name the account the verify route should admit in this checkout's config (%s@%s.iam.gserviceaccount.com) and re-apply it (%s update): a config that states a list is the one the service is deployed with, and %s project policy publish carries that same list into the node the other machines read", infra.VerifierAccountID, pluginwire.Printable(lc.GCPProject), selfCommand(), selfCommand())
		}
		return []doctorCheck{{ID: id, OK: true, Severity: "info",
			Problem: fmt.Sprintf("the credentials service %s is deployed and serving, and it admits nobody on the verify route: every job account may invoke it and its bundle holds a version, and the verifier list it is deployed with is empty (%s), so nothing can ask whether a verdict's signature is this service's", infra.CredentialsServiceName, where),
			Fix:     fix}}
	}
	return []doctorCheck{{ID: id, OK: true}}
}

// doctorDeployedVerifierAccounts is the verifier list the installation's
// credentials service is deployed with, and whether it came from the published
// policy node: localcfg's one keep rule for the key (VerifierAccountsEffective,
// which the publisher and the credentials stage both apply), this checkout's
// own verifier_accounts when it states one — a written empty list included,
// which is how the route is turned off on purpose — and the node's otherwise.
//
// The node is what keeps the line true on a machine whose config says nothing:
// such a machine's apply is deployed with the node's list, so reading the file
// alone would answer for the wrong document — a plain ok for a service the
// node had emptied, and "admits nobody" for one the node still admits. An
// installation with no budget backend has no node to read
// (projectPolicyRead's answer), and nothing published and a node that is not
// there are both nil, which is the module's own verifier account: the healthy
// state, and never the empty list.
func doctorDeployedVerifierAccounts(ctx context.Context, lc *localcfg.Config) (accounts []string, published bool, err error) {
	if lc.VerifierAccounts != nil {
		return *lc.VerifierAccounts, false, nil
	}
	p, _, err := projectPolicyRead(ctx, lc)
	if err != nil {
		return nil, false, err
	}
	if p == nil {
		return nil, false, nil
	}
	return p.VerifierAccounts, true, nil
}

// noteIfAny returns note when any is true, and "" otherwise: a parenthesised
// aside that is left out rather than written empty.
func noteIfAny(note string, any bool) string {
	if !any {
		return ""
	}
	return note
}

// invokerGranted reports whether the policy grants member one of
// runInvokeRoles, and whether a role on the way could not be judged —
// "not read" and "carries nothing" are not the same answer, which is the rule
// every other role question doctor asks is written by.
//
// Only a binding that names member is a question about them: a custom role
// granted to somebody else may or may not carry run.invoke, and it says nothing
// about whether this member may invoke the service. Marking the member unread
// for it is doctor reporting "could not be checked" about a policy that answers
// the question it asked.
func invokerGranted(bindings []iamBinding, member string) (granted, unread bool) {
	for _, b := range bindings {
		if !slices.Contains(b.members, member) {
			continue
		}
		if isCustomRole(b.role) {
			unread = true
			continue
		}
		if slices.Contains(runInvokeRoles, b.role) {
			return true, unread
		}
	}
	return false, unread
}

// doctorSecretRotationAge is doctor's "secret-rotation-age" check: the newest
// enabled version of each of the installation's stored secrets, against the
// rotation period the operator recorded (secret_rotation_days), and against
// the expiry date they recorded for a name (secret_expires, D503c).
//
// The operator's date is keyed by a secret's logical name, and a logical name
// may be stored in the project-wide container or in the repository's own —
// bitbucket-token is per-repository by default — so both listings are
// measured: project is the installation's project-wide containers, and repo is
// this checkout's own (doctorSecrets, empty when doctor is not in one). A date
// that matches neither is looked for in the rest of the installation's
// containers (set, doctorManagedSecrets: another repository's own, which this
// checkout's listing does not reach), and is a warning of its own only when
// nothing carries it: a name nothing carries is a typo, or a secret that has
// gone, and a check that quietly skipped it would leave the operator believing
// a date was watched. When the rest could not be listed, the warning is that
// instead — a name doctor could not look for is not a name nothing carries.
//
// Nothing here reads a value, and nothing here runs unless the operator
// recorded something: with no period and no date there is no age to measure
// against, and doctor adds no Secret Manager call at all — which matters,
// since the version listing is one call per container.
//
// The container's own creation time is not the answer (a rotated secret's
// container is as old as its first value), so each container's versions are
// listed and the newest ENABLED one's createTime is the age. A container with
// no enabled version is skipped: another check owns that state, and it is not
// an age.
//
// Both halves are warnings, never failures: an old credential still works, and
// what the check is for is that the operator asked to be told.
func doctorSecretRotationAge(ctx context.Context, lc *localcfg.Config, project []gcp.SecretInfo, ok bool, repo []secretEntry, set doctorManagedSet) []doctorCheck {
	if lc.SecretRotationDays <= 0 && len(lc.SecretExpires) == 0 {
		return nil
	}
	const id = "secret-rotation-age"
	if !ok {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: "the local config records a rotation period or an expiry (secret_rotation_days, secret_expires), and the installation's secrets could not be listed, so no age could be measured",
			Fix:     "rerun doctor with Secret Manager reachable and your credentials available"}}
	}
	sm, err := gcp.NewSecretManager(ctx, gcpOptions(lc))
	if err != nil {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: "the local config records a rotation period or an expiry (secret_rotation_days, secret_expires), and Secret Manager could not be reached: " + oneLineCLI(err.Error()),
			Fix:     "rerun doctor with your credentials available"}}
	}
	now := doctorNow()
	var out []doctorCheck
	carried := map[string]bool{}
	measure := func(s gcp.SecretInfo) {
		name := s.Labels[gcp.LabelSecret]
		if name != "" {
			carried[name] = true
		}
		printable := pluginwire.Printable(cmp.Or(name, s.ID))
		if lc.SecretRotationDays > 0 && s.Latest != "" {
			created, dated, err := newestEnabledVersion(ctx, sm, lc.GCPProject, s.ID)
			switch {
			case err != nil:
				// The listing itself failed, which is not the container having
				// nothing to date: the same fix, a different sentence, because
				// doctor may say what it could not do and may not report a read
				// it never made as an answer about the secret.
				out = append(out, doctorCheck{ID: id, Severity: "warning",
					Problem: fmt.Sprintf("the versions of %s could not be read, so its age against the %d-day rotation period (secret_rotation_days) could not be measured", printable, lc.SecretRotationDays),
					Fix:     "read the versions by hand: gcloud secrets versions list " + pluginwire.Printable(s.ID)})
				return
			case !dated:
				out = append(out, doctorCheck{ID: id, Severity: "warning",
					Problem: fmt.Sprintf("%s holds an enabled version whose creation time could not be read, so its age against the %d-day rotation period (secret_rotation_days) could not be measured", printable, lc.SecretRotationDays),
					Fix:     "read the versions by hand: gcloud secrets versions list " + pluginwire.Printable(s.ID)})
				return
			}
			if age := now.Sub(created); age > time.Duration(lc.SecretRotationDays)*24*time.Hour {
				out = append(out, doctorCheck{ID: id, Severity: "warning",
					Problem: fmt.Sprintf("%s's newest version is %s old and the operator recorded a %d-day rotation period (secret_rotation_days): rotate it and store the new value", printable, ageDays(age), lc.SecretRotationDays),
					Fix:     "rotate it at the provider and store the new value: " + selfCommand() + " creds set " + printable + " (or " + selfCommand() + " secrets set " + printable + " --project-wide for a secret the service does not hold)"})
			}
		}
		date, recorded := lc.SecretExpires[name]
		if !recorded {
			return
		}
		expires, err := time.Parse(localcfg.SecretExpiryLayout, date)
		if err != nil {
			// Parse refuses a date it cannot read, so this is a config that
			// reached doctor some other way; say so rather than guess at a day.
			out = append(out, doctorCheck{ID: id, Severity: "warning",
				Problem: fmt.Sprintf("secret_expires records %s for %s, which is not a date (YYYY-MM-DD)", pluginwire.Printable(date), printable),
				Fix:     "fix secret_expires in the local config"})
			return
		}
		switch left := expires.Sub(now); {
		case left < 0:
			out = append(out, doctorCheck{ID: id, Severity: "warning",
				Problem: fmt.Sprintf("%s expired on %s (secret_expires), %s ago: every run that uses it is failing or about to", printable, date, ageDays(-left)),
				Fix:     "rotate it at the provider and store the new value, then update the date in secret_expires"})
		case left <= secretExpiryWarnWithin:
			out = append(out, doctorCheck{ID: id, Severity: "warning",
				Problem: fmt.Sprintf("%s expires on %s (secret_expires), in %s: rotate it and store the new value before then", printable, date, ageDays(left)),
				Fix:     "rotate it at the provider and store the new value: " + selfCommand() + " creds set " + printable + " (or " + selfCommand() + " secrets set " + printable + " --project-wide for a secret the service does not hold)"})
		}
	}
	for _, s := range project {
		measure(s)
	}
	for _, e := range repo {
		measure(e.SecretInfo)
	}
	// The rest of the installation's containers, for the names the two scopes
	// above do not carry: a name may be stored in another repository's own
	// container, and doctor run in this checkout would otherwise report the
	// operator's correct date as a typo.
	for name := range set.Names {
		carried[name] = true
	}
	var unmatched []string
	for _, name := range slices.Sorted(maps.Keys(lc.SecretExpires)) {
		if !carried[name] {
			unmatched = append(unmatched, pluginwire.Printable(name))
		}
	}
	if len(unmatched) > 0 {
		if !set.Read {
			// A name is unmatched and the containers it may live in could not
			// be listed: "no container carries that name" would be doctor
			// reporting its own blind spot as the operator's mistake, which is
			// the false warning this arm exists to keep out.
			out = append(out, doctorCheck{ID: id, Severity: "warning",
				Problem: fmt.Sprintf("secret_expires records a date for %s, and the containers of every repository this installation knows could not be listed, so whether anything carries those names could not be checked", strings.Join(unmatched, ", ")),
				Fix:     "rerun doctor with Secret Manager reachable and your credentials available"})
		} else {
			out = append(out, doctorCheck{ID: id, Severity: "warning",
				Problem: fmt.Sprintf("secret_expires records a date for %s, and no secret container this run could list carries that name (the project-wide ones, this checkout's own, and the ones of every other repository this installation knows): a date nothing matches is watched by nothing, so a name that is a typo, or a secret that has gone, is reported instead of skipped",
					strings.Join(unmatched, ", ")),
				Fix: "fix the name in secret_expires, or drop the entry: the key is a secret's logical name, which is what " + selfCommand() + " secrets ls prints beside its container"})
		}
	}
	if len(out) == 0 {
		// The measurement: what the operator recorded was checked and nothing is
		// outside it. It is said rather than left silent, because "is anything
		// about to expire" is a question only an answer answers — and it is ok
		// with severity "info", which is the measurement class: --all and --json
		// carry it, and the default view neither prints it nor counts it.
		inside := "no date in secret_expires is within " + ageDays(secretExpiryWarnWithin)
		if lc.SecretRotationDays > 0 {
			inside = fmt.Sprintf("no newest enabled version is older than the recorded %d-day period, and %s", lc.SecretRotationDays, inside)
		}
		out = append(out, doctorCheck{ID: id, OK: true, Severity: "info",
			Problem: "every secret this run could date is inside what the local config records (secret_rotation_days, secret_expires): " + inside})
	}
	return out
}

// newestEnabledVersion answers the createTime of the container's newest
// enabled version: the highest version number in state ENABLED, which is what
// Secret Manager's own `latest` resolves to.
//
// dated is false when the container has no version this function can date —
// none in state ENABLED, or none whose name and creation time it can read —
// and the error is the listing failing, which is a different answer: a
// container whose versions could not be read is not a container with nothing
// enabled in it, and the caller says so.
func newestEnabledVersion(ctx context.Context, sm *secretmanager.Service, project, id string) (created time.Time, dated bool, err error) {
	var (
		best  string
		at    time.Time
		found bool
	)
	err = sm.Projects.Secrets.Versions.List("projects/"+project+"/secrets/"+id).Pages(ctx, func(p *secretmanager.ListSecretVersionsResponse) error {
		for _, v := range p.Versions {
			if v.State != "ENABLED" {
				continue
			}
			n, err := versionNumber(v.Name)
			if err != nil || (found && !newerVersion(n, best)) {
				continue
			}
			created, err := time.Parse(time.RFC3339Nano, v.CreateTime)
			if err != nil {
				continue
			}
			best, at, found = n, created, true
		}
		return nil
	})
	if err != nil {
		return time.Time{}, false, err
	}
	return at, found, nil
}

// doctorBundleChecks are the checks that read the bundle's value, all behind
// --check-bitbucket-token and all from ONE read of the document: what it holds
// (bundle-bitbucket-token), which stored token serves each proxied repository
// and whether Bitbucket still accepts it (bitbucket-token-valid, one line per
// token the walk asked about, T10's F4), which proxied repositories no stored
// token covers (bitbucket-tokens-uncovered), and which stored token the
// credential state holds as broken (bitbucket-token-broken).
//
// The document is the service's own: `creds ls` reads it with the operator's
// identity, the read is in memory, and each value is handed to one Bitbucket
// client per repository that token serves and nowhere else. A repository counts
// as proxied when the operator's project_secrets entry admits it with
// proxy: true — the local half of B1.8's flip, which is what decides whether
// the service forwards this repository's git credential — and the repositories
// are the ones the local config records, since a wildcard pattern admits a
// repository nobody can enumerate.
func doctorBundleChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	const idBundle = "bundle-bitbucket-token"
	if lc.CredentialsService == nil {
		return []doctorCheck{{ID: idBundle, Severity: "info",
			Problem: "the installation has not opted into the credentials service, so it has no bundle for a Bitbucket token to live in: runs of a proxied repository have nowhere to fetch one",
			Fix:     "to broker the Bitbucket token the installation needs its credentials service, which an ordinary " + selfCommand() + " update deploys on every installation that can carry one (it needs the installation's Firebase backend and at least one repository job)"}}
	}
	proxied := proxiedBitbucketRepos(lc)
	unreadable := func(what string) []doctorCheck {
		return []doctorCheck{{ID: idBundle, Severity: "warning",
			Problem: "the bundle could not be read (" + what + "), so whether it holds a Bitbucket token, and whether Bitbucket accepts it, could not be checked",
			Fix:     "rerun doctor with your credentials available, or read the bundle's names yourself: " + selfCommand() + " creds ls"}}
	}
	store, err := openBundleStore(ctx, lc)
	if err != nil {
		return unreadable(oneLineCLI(err.Error()))
	}
	if err := store.check(ctx); err != nil {
		return unreadable(oneLineCLI(err.Error()))
	}
	meta, err := store.newest(ctx)
	switch {
	case errors.Is(err, errNoBundleVersion):
		return []doctorCheck{{ID: idBundle, Severity: bundleSeverity(len(proxied)),
			Problem: bundleMissingProblem(store.id, len(proxied)),
			Fix:     selfCommand() + " creds set " + config.BitbucketSecret}}
	case err != nil:
		return unreadable(oneLineCLI(err.Error()))
	}
	if meta.state == "DISABLED" {
		return unreadable("its newest version is DISABLED")
	}
	doc, err := store.access(ctx, meta.number)
	if err != nil {
		return unreadable(oneLineCLI(err.Error()))
	}
	b, err := credsvc.ParseBundle(doc.raw)
	if err != nil {
		return unreadable("the document is not one the service accepts: " + oneLineCLI(err.Error()))
	}
	_, held := b.BitbucketToken()
	tokens := bitbucketBundleTokens(b)
	if !held && len(tokens) == 0 {
		return []doctorCheck{{ID: idBundle, Severity: bundleSeverity(len(proxied)),
			Problem: bundleMissingProblem(store.id, len(proxied)),
			Fix:     selfCommand() + " creds set " + config.BitbucketSecret}}
	}
	// The bundle's own answer is a measurement and not doctor's bookkeeping: the
	// question the flag was passed for is whether the service holds a token, and
	// the line that answers it is what makes an installation with nothing wrong
	// readable under --all. No token's value is in it, and never is: what is
	// said is that the bundle holds the credential, which is why a name and not
	// a value is what every message here carries.
	out := []doctorCheck{{ID: idBundle, OK: true, Severity: "info",
		Problem: bundleHoldsProblem(store.id, tokens)}}
	asks, err := bitbucketTokenAsks(lc, proxied, tokens)
	if err != nil {
		// The records this machine holds are the coverage half of every question
		// below, and the node's own rule is what refuses them here: which token
		// serves which repository, and whether any repository is uncovered, is
		// then a question that was not asked rather than one answered wrongly.
		return append(out, doctorCheck{ID: idUncovered, Severity: "info", unread: true,
			Problem: "the repositories the stored Bitbucket tokens cover could not be read from this machine's records (" + oneLineCLI(err.Error()) + "), so which token serves which proxied repository, and whether any of them no token covers, could not be checked",
			Fix:     "fix the bitbucket_tokens block of this machine's project config"})
	}
	return append(out, doctorBitbucketTokenChecks(ctx, lc, asks, tokens)...)
}

// bundleHoldsProblem is the bundle's own line when it holds at least one
// Bitbucket token: the legacy credential's sentence where it holds one (this
// check's own text, unchanged), and what the typed tokens beside it are — a
// repository is served by the narrowest of them that covers it (R9), so a
// reader who meets a per-token line further down knows which token it was
// about.
func bundleHoldsProblem(id string, tokens []credsvc.Entry) string {
	var family []string
	legacy := false
	for _, e := range tokens {
		if e.Key == config.BitbucketSecret {
			legacy = true
			continue
		}
		family = append(family, pluginwire.Printable(e.Key))
	}
	switch {
	case !legacy:
		return fmt.Sprintf("the credentials service's bundle %s holds no %s credential and holds %d typed Bitbucket token(s), %s: a call is served by the narrowest of them that covers its repository, and a repository none of them covers is refused",
			pluginwire.Printable(id), config.BitbucketSecret, len(family), strings.Join(family, ", "))
	case len(family) == 0:
		return fmt.Sprintf("the credentials service's bundle %s holds a %s credential", pluginwire.Printable(id), config.BitbucketSecret)
	}
	return fmt.Sprintf("the credentials service's bundle %s holds a %s credential and %d typed Bitbucket token(s), %s: a call is served by the narrowest of them that covers its repository, and the legacy credential is the widest",
		pluginwire.Printable(id), config.BitbucketSecret, len(family), strings.Join(family, ", "))
}

// bundleMissingProblem says the bundle holds no Bitbucket token, in the words
// of whether that matters: a repository the owner proxied has nowhere to fetch
// a credential from, and an installation that proxies none is simply one that
// has not put the token there.
func bundleMissingProblem(id string, proxied int) string {
	if proxied == 0 {
		return fmt.Sprintf("the credentials service's bundle %s holds no %s credential, and no repository of this installation is admitted with proxy: true: nothing fetches one yet", pluginwire.Printable(id), config.BitbucketSecret)
	}
	return fmt.Sprintf("the credentials service's bundle %s holds no %s credential, and %d repository(ies) of this installation are admitted with proxy: true: a run of each of them is refused when it asks the service for its git credential", pluginwire.Printable(id), config.BitbucketSecret, proxied)
}

// bundleSeverity is what a bundle with no Bitbucket token is worth: a warning
// when a proxied repository is waiting for one, information when none is.
func bundleSeverity(proxied int) string {
	if proxied == 0 {
		return "info"
	}
	return "warning"
}

// The token checks' own IDs: one line per stored token Bitbucket was asked
// about, one for the repositories no stored token covers, one for the tokens
// the credential state holds as broken, and one for a typed record's expiry.
const (
	idTokenValid  = "bitbucket-token-valid"
	idUncovered   = "bitbucket-tokens-uncovered"
	idTokenBroken = "bitbucket-token-broken"
	idTokenExpiry = "bitbucket-token-expiry"
)

// bitbucketAsk is one proxied repository and the stored tokens whose coverage
// admits it, narrowest first: the order the credentials service selects in
// (bbtokens.rankOf), so the token doctor asks Bitbucket about is the token a run
// of that repository would be served by, and not one the service would never
// reach for it.
type bitbucketAsk struct {
	repo   string
	tokens []string
}

// bitbucketTokenAsks assigns every proxied repository the stored tokens that
// cover it, narrowest first, with the legacy credential last: it is R3's
// implicit untyped record, and the widest reach of the four (an untyped record
// ranks last in the service's own order).
//
// The coverage is the NODE's, derived from the operator's records by the
// function the publisher uses (projectpolicy.DeriveBitbucketTokens), so the
// spelling a repository is matched against is the one the published node will
// carry and never a second reading of the config. The legacy key's coverage is
// not a record's: R3 makes it the project_secrets entry's allow_data_to list,
// which is also what decides which repositories are proxied at all, so the
// legacy credential covers every one of them by construction.
func bitbucketTokenAsks(lc *localcfg.Config, proxied []string, tokens []credsvc.Entry) ([]bitbucketAsk, error) {
	patterns := map[string][]string{}
	types := map[string]string{}
	for _, rec := range lc.BitbucketTokens {
		types[config.BitbucketTokenKey(rec.Name)] = rec.Type
	}
	if len(lc.BitbucketTokens) > 0 {
		derived, err := projectpolicy.DeriveBitbucketTokens(lc.BitbucketTokens, nil)
		if err != nil {
			return nil, err
		}
		if derived != nil {
			for _, e := range derived.Entries {
				patterns[e.Name] = e.Coverage
			}
		}
	}
	out := make([]bitbucketAsk, 0, len(proxied))
	for _, repo := range proxied {
		canonical := "bitbucket/" + strings.ToLower(repo)
		ask := bitbucketAsk{repo: repo}
		for _, e := range tokens {
			if e.Key == config.BitbucketSecret {
				ask.tokens = append(ask.tokens, e.Key)
				continue
			}
			for _, pattern := range patterns[e.Key] {
				if projectpolicy.MatchRepo(pattern, canonical) {
					ask.tokens = append(ask.tokens, e.Key)
					break
				}
			}
		}
		slices.SortStableFunc(ask.tokens, func(a, b string) int {
			if n := cmp.Compare(bitbucketTokenRank(types[a]), bitbucketTokenRank(types[b])); n != 0 {
				return n
			}
			return strings.Compare(a, b)
		})
		out = append(out, ask)
	}
	return out, nil
}

// bitbucketTokenRank is the narrowest-first order of the three types, with the
// legacy credential last: the service's own (bbtokens.rankOf), restated here
// because this order decides which token doctor asks about, and asking in
// another order would report a token the runs of that repository never reach.
func bitbucketTokenRank(typ string) int {
	switch typ {
	case config.BitbucketTokenTypeRepo:
		return 0
	case config.BitbucketTokenTypeProject:
		return 1
	case config.BitbucketTokenTypeWorkspace:
		return 2
	}
	return 3
}

// bitbucketWalk is one pass of the token check's questions: which stored token
// Bitbucket refused for which proxied repository, which questions went
// unanswered, which tokens answered for at least one repository, and which
// proxied repositories no stored token covers at all.
type bitbucketWalk struct {
	refused   map[string][]string
	unasked   map[string][]string
	accepted  map[string]bool
	uncovered []string
	records   map[string]config.BitbucketToken
}

// walkBitbucketTokens asks Bitbucket about every proxied repository with the
// token that repository's runs would be served by, and never with more than
// one: the narrowest covering token is asked first and the walk stops at its
// answer, because that is the token the credentials service selects, and a
// wider token that also happens to work says nothing about the call that would
// have been made.
//
// The one answer the walk steps over is a project token's 404. Bitbucket
// answers it for a repository of another project, which is the token working
// exactly as it should, so the walk falls through to the next covering token —
// the rule the service's own lookup keeps (bbtokens.ProjectLookup) — and a
// repository every covering project token misses is uncovered rather than
// refused. A repository or workspace token's 404 is a refusal: Bitbucket
// answers it for a repository the token may not see, and the run served by it
// fails at its first git call.
//
// A token covering no proxied repository is not asked about at all. There is
// nothing to ask — the question is one GET /repositories/<repo> — and spending
// a credential on a repository this installation records no job for is not what
// the flag is for.
func walkBitbucketTokens(ctx context.Context, lc *localcfg.Config, asks []bitbucketAsk, tokens []credsvc.Entry) bitbucketWalk {
	w := bitbucketWalk{
		refused:  map[string][]string{},
		unasked:  map[string][]string{},
		accepted: map[string]bool{},
		records:  bitbucketTokenRecords(lc),
	}
	values := make(map[string]string, len(tokens))
	for _, e := range tokens {
		values[e.Key] = e.Value()
	}
	roots := bitbucketTokenRoots(lc)
	for _, ask := range asks {
		repo := pluginwire.Printable(ask.repo)
		if len(ask.tokens) == 0 {
			w.uncovered = append(w.uncovered, repo)
			continue
		}
		workspace, slug, ok := strings.Cut(ask.repo, "/")
		if !ok || workspace == "" || slug == "" {
			// A repository this installation records that is not owner/name:
			// there is nothing to ask Bitbucket about, and it is not a coverage
			// finding either — whatever token covers the entry covers what it
			// names.
			w.unasked[ask.tokens[0]] = append(w.unasked[ask.tokens[0]], repo)
			continue
		}
		decided := false
		for _, key := range ask.tokens {
			switch askTokenAtRoots(ctx, roots, workspace, slug, values[key], bitbucketNotFoundIsMiss(key, w.records)) {
			case tokenAccepted:
				w.accepted[key] = true
				decided = true
			case tokenRefused:
				w.refused[key] = append(w.refused[key], repo)
				decided = true
			case tokenNotCovered:
				continue
			default:
				w.unasked[key] = append(w.unasked[key], repo)
				decided = true
			}
			break
		}
		if !decided {
			w.uncovered = append(w.uncovered, repo)
		}
	}
	return w
}

// bitbucketNotFoundIsMiss reports whether a 404 about this token is the token
// not reaching the repository rather than the repository being refused it: only
// a project token's reach is decided by a lookup, and Bitbucket answers 404 for
// a repository of another project (R2, bbtokens.ProjectLookup).
func bitbucketNotFoundIsMiss(key string, records map[string]config.BitbucketToken) bool {
	return records[key].Type == config.BitbucketTokenTypeProject
}

// doctorBitbucketTokenChecks is the walk's own lines: one per stored token
// Bitbucket was asked about (bitbucket-token-valid for the legacy credential,
// which is the ID this check has always had, and one ID per typed token beside
// it), and one for the proxied repositories no stored token covers.
func doctorBitbucketTokenChecks(ctx context.Context, lc *localcfg.Config, asks []bitbucketAsk, tokens []credsvc.Entry) []doctorCheck {
	w := walkBitbucketTokens(ctx, lc, asks, tokens)
	var out []doctorCheck
	for _, key := range w.asked() {
		switch refused, unasked := w.refused[key], w.unasked[key]; {
		case len(refused) > 0:
			out = append(out, doctorCheck{ID: bitbucketTokenCheckID(key), Severity: "warning",
				Problem: bitbucketRefusedProblem(key, refused, unasked),
				Fix:     bitbucketTokenStoreFix(key, w.records[key])})
		case len(unasked) > 0:
			// unread, because that is what this state is: a question Bitbucket
			// was asked and did not answer. The marker is set by the check that
			// could not read, and the class reads it as the unknown it is — the
			// alternative is a fact left unknown that reads like one doctor
			// measured and found nothing in.
			out = append(out, doctorCheck{ID: bitbucketTokenCheckID(key), Severity: "info", unread: true,
				Problem: bitbucketUnansweredProblem(key, unasked),
				Fix:     "rerun doctor when Bitbucket is reachable"})
		case w.accepted[key]:
			out = append(out, doctorCheck{ID: bitbucketTokenCheckID(key), OK: true})
		}
	}
	if len(w.uncovered) > 0 {
		out = append(out, doctorCheck{ID: idUncovered, Severity: "warning",
			Problem: fmt.Sprintf("the repositories %s are admitted with proxy: true and no stored Bitbucket token covers them: the credentials service refuses a run of each with token_not_covered before its first git call, and no token this installation holds can serve it",
				strings.Join(w.uncovered, ", ")),
			Fix: "cover them with a typed token: " + selfCommand() + " creds set " + config.BitbucketTokenPrefix + "NAME --type " + config.BitbucketTokenTypeRepo + " --coverage " + strings.Join(w.uncovered, " --coverage ")})
	}
	return out
}

// asked is every token a question was put to, in a stable order: the refused
// and the unanswered first by their document key, then the ones that answered,
// so two runs of doctor print the same lines in the same order.
func (w bitbucketWalk) asked() []string {
	keys := map[string]bool{}
	for key := range w.refused {
		keys[key] = true
	}
	for key := range w.unasked {
		keys[key] = true
	}
	for key := range w.accepted {
		keys[key] = true
	}
	return slices.Sorted(maps.Keys(keys))
}

// bitbucketTokenCheckID is the ID one stored token's line carries: the legacy
// credential's own (bitbucket-token-valid, the ID this check has always had),
// and a typed token's beside it, named by its account — the name doctor speaks
// it by, and the one its own record and the node carry (R5).
func bitbucketTokenCheckID(key string) string {
	if name, ok := config.BitbucketTokenAccount(key); ok && name != "" {
		return idTokenValid + "--" + name
	}
	return idTokenValid
}

// bitbucketRefusedProblem is what a refusal is worth: every run of the
// repositories named fails at its first git call, and the token that fails them
// is named — for the legacy credential in the words this check has always used,
// and for a typed token by its document key, which is the name its record, the
// bundle entry and the state node all carry.
func bitbucketRefusedProblem(key string, refused, unasked []string) string {
	note := noteIfAny("; not asked about "+strings.Join(unasked, ", "), len(unasked) > 0)
	if key == config.BitbucketSecret {
		return fmt.Sprintf("Bitbucket refuses the bundle's %s credential for %s: a run of each of those repositories fails at its first git call%s",
			config.BitbucketSecret, strings.Join(refused, ", "), note)
	}
	return fmt.Sprintf("Bitbucket refuses the Bitbucket token %s for %s, and it is the token those repositories' runs are served by: a run of each fails at its first git call%s",
		pluginwire.Printable(key), strings.Join(refused, ", "), note)
}

// bitbucketUnansweredProblem is a question Bitbucket did not answer. The token
// may be perfectly good — a network that is down, an outage, a rate limit —
// which is why this is never the refusal beside it.
func bitbucketUnansweredProblem(key string, unasked []string) string {
	if key == config.BitbucketSecret {
		return fmt.Sprintf("Bitbucket could not be asked about %s, so whether the bundle's %s credential is still accepted there could not be checked",
			strings.Join(unasked, ", "), config.BitbucketSecret)
	}
	return fmt.Sprintf("Bitbucket could not be asked about %s with the Bitbucket token %s, so whether it is still accepted there could not be checked",
		strings.Join(unasked, ", "), pluginwire.Printable(key))
}

// bitbucketTokenStoreFix is the command that replaces one stored token: the
// legacy credential's own fix, which names the scopes Bitbucket has to grant it,
// and for a typed token the record's own facts spelled as flags — an operator
// who pastes it writes the same type and coverage back, which is what a
// replacement has to keep.
func bitbucketTokenStoreFix(key string, rec config.BitbucketToken) string {
	if key == config.BitbucketSecret {
		return "store a token Bitbucket accepts, with Repositories read and Pull requests read/write: " + selfCommand() + " creds set " + config.BitbucketSecret
	}
	return "store a token Bitbucket accepts for those repositories: " + bitbucketTokenSetCommand(key, rec)
}

// bitbucketTokenSetCommand is the command that stores or replaces one token
// with the record's own facts as flags.
func bitbucketTokenSetCommand(key string, rec config.BitbucketToken) string {
	cmd := selfCommand() + " creds set " + pluginwire.Printable(key)
	if rec.Type != "" {
		cmd += " --type " + rec.Type
	}
	for _, pattern := range rec.Coverage {
		cmd += " --coverage " + pluginwire.Printable(pattern)
	}
	return cmd
}

// doctorBitbucketTokenBroken names every stored token the credential state
// holds as broken (C1.21): Bitbucket answered 401 for it, the selection skips a
// broken token, so every repository only it covers is refused, and a repository
// a wider token also covers is served by that one with nothing saying this
// token is dead.
//
// The state is read from the project's own credential state store — the
// subtree the service writes (creds/tokens, T6), and not the model marks beside
// it, which are doctor's own creds-broken — through the database the policy
// checks already opened. Like creds-broken it needs no flag: a token that is
// dead is a fact the operator has to act on, and the one command that reads the
// bundle's value is not where a fact about the state store belongs.
//
// It is read only where a service could have written one: an installation with
// no credentials service and no typed record has no Bitbucket token for a mark
// to be about. A store that could not be read is information rather than
// silence — which tokens are broken is a fact doctor owes an answer about, and
// a read that failed is not a token that works — and it is not a warning
// either, because the installation whose backend could not be reached has not
// been shown to hold a broken token.
func doctorBitbucketTokenBroken(ctx context.Context, db *rtdb.Client, lc *localcfg.Config) []doctorCheck {
	if lc.CredentialsService == nil && len(lc.BitbucketTokens) == 0 {
		return nil
	}
	statuses, err := (&creds.RTDBStore{DB: db}).TokenStatuses(ctx)
	if err != nil {
		return []doctorCheck{{ID: idTokenBroken, Severity: "info", unread: true,
			Problem: "the Bitbucket tokens' state could not be read (" + oneLineCLI(err.Error()) + "), so whether any stored token is broken is unknown",
			Fix:     "check that the credentials doctor uses can read the budget database, then run doctor again"}}
	}
	var broken []string
	for _, key := range slices.Sorted(maps.Keys(statuses)) {
		if statuses[key].State == creds.StateBroken {
			broken = append(broken, pluginwire.Printable(key))
		}
	}
	if len(broken) == 0 {
		return nil
	}
	return []doctorCheck{{ID: idTokenBroken, Severity: "warning",
		Problem: fmt.Sprintf("Bitbucket refused the Bitbucket token(s) %s with 401 and the project's credential state holds them as broken: the selection skips a broken token, so every repository only one of them covers is refused, and a repository a wider token also covers is served by that one with nothing saying this token is dead",
			strings.Join(broken, ", ")),
		Fix: "replace each broken token and store the new value: " + selfCommand() + " creds set " + config.BitbucketTokenPrefix + "NAME, with the record's own --type and --coverage"}}
}

// doctorBitbucketTokenExpiry is O3 over the typed tokens: a record whose
// expires is inside the lead doctor already uses for secret_expires
// (secretExpiryWarnWithin), or past it, is a warning — the credentials service
// stops serving the repositories that token covers the day it lapses, and
// nothing else in the installation notices.
//
// It reads this machine's records and nothing else, so it is offline like the
// rest of doctor's local checks and does not need --check-bitbucket-token: the
// date is the operator's own, written into the project config by the same
// `creds set` that wrote the record.
//
// The legacy credential's date is not here. It is a secret_expires entry keyed
// by bitbucket-token, and doctorSecretRotationAge already watches every one of
// those with this same lead; a second line about it would be one fact told
// twice, in two places that could disagree.
func doctorBitbucketTokenExpiry(lc *localcfg.Config) []doctorCheck {
	if lc == nil || len(lc.BitbucketTokens) == 0 {
		return nil
	}
	now := doctorNow()
	var out []doctorCheck
	for _, rec := range lc.BitbucketTokens {
		if rec.Expires == "" {
			continue
		}
		key := config.BitbucketTokenKey(rec.Name)
		expires, err := time.Parse(localcfg.SecretExpiryLayout, rec.Expires)
		if err != nil {
			// localcfg refuses a date it cannot read at load, so this is a
			// config that reached doctor some other way; say so rather than
			// guess at a day.
			out = append(out, doctorCheck{ID: idTokenExpiry, Severity: "warning",
				Problem: fmt.Sprintf("the record %s carries expires %s, which is not a date (%s)", pluginwire.Printable(key), pluginwire.Printable(rec.Expires), localcfg.SecretExpiryLayout),
				Fix:     "fix the record's expires in the local config"})
			continue
		}
		switch left := expires.Sub(now); {
		case left < 0:
			out = append(out, doctorCheck{ID: idTokenExpiry, Severity: "warning",
				Problem: fmt.Sprintf("the Bitbucket token %s expired on %s, %s ago: every repository only it covers is refused from its first git call", pluginwire.Printable(key), rec.Expires, ageDays(-left)),
				Fix:     "replace it and store the new value: " + bitbucketTokenSetCommand(key, rec)})
		case left <= secretExpiryWarnWithin:
			out = append(out, doctorCheck{ID: idTokenExpiry, Severity: "warning",
				Problem: fmt.Sprintf("the Bitbucket token %s expires on %s, in %s: replace it and store the new value before then", pluginwire.Printable(key), rec.Expires, ageDays(left)),
				Fix:     "replace it and store the new value: " + bitbucketTokenSetCommand(key, rec)})
		}
	}
	return out
}

// bitbucketTokenRoots are the API roots the bundle's token is asked about: the
// installation's own allow-list when it lists one (bitbucket_api_roots), and
// Bitbucket's own API otherwise. It is the rule a cloud run is held to — the
// token goes only to Bitbucket or to a root the owner listed (internal/runner's
// checkBitbucketAPIRoot) — applied to the one check that spends the credential
// from the operator's machine.
//
// Never whatever an environment variable names: an installation whose runs
// point FUGARO_BITBUCKET_API_URL somewhere lists that root here, and a
// variable nothing lists is a host the owner never allowed, which no read of a
// credential may be pointed at.
//
// A listed root is held to the node's own rule (projectpolicy.
// ValidBitbucketAPIRoots) before it is asked, because this file is not where a
// root is validated: the publish that builds the node is. One the rule refuses
// — http, which would send the token in clear, or userinfo, which is a
// credential in the file — is a host no read of the bundle's token may be
// pointed at, so it is dropped, and a repository whose every listed root was
// dropped is left unanswered rather than refused: nothing was asked.
func bitbucketTokenRoots(lc *localcfg.Config) []string {
	if lc != nil && len(lc.BitbucketAPIRoots) > 0 {
		out := make([]string, 0, len(lc.BitbucketAPIRoots))
		for _, root := range lc.BitbucketAPIRoots {
			if projectpolicy.ValidBitbucketAPIRoots([]string{root}) == nil {
				out = append(out, root)
			}
		}
		return out
	}
	return []string{bitbucketAPIURL}
}

// tokenAnswer is what asking a repository's roots said about the token.
type tokenAnswer int

const (
	// tokenUnanswered: no root accepted the token and none refused it — every
	// root the check reached failed to answer (a network that is down, an
	// outage, a rate limit). It is a fact doctor reports, never an answer
	// about the credential.
	tokenUnanswered tokenAnswer = iota
	tokenAccepted
	tokenRefused
	// tokenNotCovered: every root answered 404 about a token whose reach a 404
	// decides, which is a project token's alone — Bitbucket answers it for a
	// repository of another project, and the service's own lookup reads it as
	// "this token does not reach that repository" and goes on to the next
	// candidate (bbtokens.ProjectLookup). For a repository or workspace token a
	// 404 is a refusal, and never this.
	tokenNotCovered
)

// askTokenAtRoots asks each root the token may be sent to, and answers accepted
// as soon as one accepts it: an installation may list more than one root, and
// the token is good for the runs that use that one. Refused is *every* root
// refusing — a single root in the common case — so a listed root that is down
// leaves the answer unanswered rather than refused, which is what keeps a
// working token from being sent to be rotated. Each root is asked with the
// client of the check, whose timeout bounds the request.
//
// notCoveredOn404 is the one thing the caller decides, and it is the record's
// own rule: true for a project token, whose reach a 404 decides (tokenNotCovered
// says why), and false for every other record, whose 404 is a refusal.
func askTokenAtRoots(ctx context.Context, roots []string, workspace, slug, token string, notCoveredOn404 bool) tokenAnswer {
	refused, missed, answered := 0, 0, 0
	for _, root := range roots {
		p, err := bitbucket.New(bitbucket.Options{Workspace: workspace, Slug: slug, Token: token,
			BaseURL: root, HTTP: bitbucketAPIClient})
		if err != nil {
			continue
		}
		answered++
		switch _, err = p.Repository(ctx); {
		case err == nil:
			return tokenAccepted
		case isBitbucketNotFound(err) && notCoveredOn404:
			missed++
		case isBitbucketRefusal(err):
			refused++
		}
	}
	if answered == 0 {
		return tokenUnanswered
	}
	switch {
	case refused == answered:
		return tokenRefused
	case missed == answered:
		return tokenNotCovered
	}
	return tokenUnanswered
}

// isBitbucketRefusal reports whether Bitbucket answered about the credential
// rather than about itself: 401 and 403 are the token being rejected, and 404
// is a repository the token may not see, which is what Bitbucket answers for
// one it cannot reach — and a run that cannot see its repository fails at its
// first git call exactly as one with a rejected token does. Every other status
// is Bitbucket failing to answer (a 5xx, a 429, a redirect that did not land),
// and reporting one of those as a refused token would send the operator to
// rotate a credential that works.
//
// The body is never part of the message: it is Bitbucket's text about a
// request that carried a credential.
func isBitbucketRefusal(err error) bool {
	var se *httpjson.StatusError
	if !errors.As(err, &se) {
		return false
	}
	switch se.Status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	}
	return false
}

// isBitbucketNotFound reports whether Bitbucket answered 404: the repository is
// not there, or the credential asking cannot see it. For a project token the two
// are one answer — the token does not reach that repository — which is what
// separates it from the refusals isBitbucketRefusal names.
func isBitbucketNotFound(err error) bool {
	var se *httpjson.StatusError
	return errors.As(err, &se) && se.Status == http.StatusNotFound
}

// doctorBitbucketDirect is doctor's "bitbucket-direct" check: this checkout's
// repository is a Bitbucket one, and what its jobs hold of the token.
//
// The repository not admitted with proxy: true: a job mounting bitbucket-token
// is the state the installation is in, and the line is information — the run
// holds a repository access token directly, which is D462's reach.
//
// A repository the owner DID proxy is only half the answer, because the flip
// that takes the mount and the accessor away fires where the whole switch is
// on (CredentialsServiceServes): the installation's block, this repository's
// own record of a root applied with the service on, and its agent.auth being
// the cascade. A proxied repository the switch holds off — no record, or an
// auth the service does not serve — keeps its mount and its accessor by
// design, exactly as a repository the node does not admit, and its line is
// information for the same reason.
//
// Only where the switch is on is a job still mounting bitbucket-token a job
// applied before the flip: the next deploy of it would fail on a mount whose
// accessor is gone, and the run would keep reaching the token the service is
// supposed to hold. That is the warning.
func doctorBitbucketDirect(ctx context.Context, lc *localcfg.Config, repo, provider, auth string, facts doctorRepoSecrets) []doctorCheck {
	if provider != gitprov.KindBitbucket || lc.CredentialsService == nil {
		return nil
	}
	const id = "bitbucket-direct"
	printable := pluginwire.Printable(repo)
	repoSlug, err := task.Slug(provider, repo)
	if err != nil {
		// A repository no job name can be derived from: the same entry refuses
		// the credentials stage, which is where it is worth saying.
		return nil
	}
	entry, ok := lc.ProjectSecrets[config.BitbucketSecret]
	proxied := ok && entry.Proxying() && entry.AllowsData(repo)
	if !proxied {
		return []doctorCheck{{ID: id, Severity: "info",
			Problem: fmt.Sprintf("%s is a Bitbucket repository that is not proxied: its jobs mount their own %s, so a run of it holds a credential that reaches the repository itself (D462)", printable, config.BitbucketSecret),
			Fix:     "to broker it instead, add it to the local config's project_secrets entry for " + config.BitbucketSecret + " (allow_data_to with " + printable + ", proxy: true), publish the policy, then re-apply the repository: " + selfCommand() + " update --repo " + quoteWord(repo)}}
	}
	if !flipServes(lc, repo, auth) {
		return []doctorCheck{{ID: id, Severity: "info",
			Problem: fmt.Sprintf("%s is admitted with proxy: true and the credentials service does not serve it — %s — so its jobs keep their own %s: the flip is per repository, and it drops a mount only where the whole switch is on", printable, flipNotServed(lc, repo, auth), config.BitbucketSecret),
			Fix:     "nothing to do: these jobs mount and reach their own token, exactly as a repository the policy does not proxy"}}
	}
	if lc.Endpoints.NoAuth && lc.Endpoints.Run == "" {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("%s is proxied, and the deployed jobs could not be read (a test configuration), so whether a job still mounts %s could not be checked", printable, config.BitbucketSecret),
			Fix:     "rerun doctor with the Cloud Run API available"}}
	}
	be, err := gcp.New(ctx, gcp.Options{GCPProject: lc.GCPProject, Region: lc.Region, LogView: lc.LogView,
		Endpoints: gcp.Endpoints{Run: lc.Endpoints.Run, NoAuth: lc.Endpoints.NoAuth}})
	if err != nil {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("%s is proxied, and the Cloud Run API could not be reached (%s), so whether a job still mounts %s could not be checked", printable, oneLineCLI(err.Error()), config.BitbucketSecret),
			Fix:     "rerun doctor with your credentials available"}}
	}
	// Every job the repository was deployed as, not only each workflow's
	// default-account one: a named Claude account runs the same workflow as
	// gcp.WorkflowVariant's job (infra.Workflow builds one job per (workflow,
	// account)), it mounts the same token, and after the flip it is the same
	// stale mount. The workflows are the local config's own record of what was
	// deployed, unioned with the checkout's, since a job outlives the
	// fugaro.yaml entry that made it.
	workflows := slices.Clone(lc.Repos[repo].Workflows)
	for _, w := range slices.Sorted(maps.Keys(facts.jobs)) {
		if !slices.Contains(workflows, w) {
			workflows = append(workflows, w)
		}
	}
	jobs := map[string]bool{}
	for _, w := range workflows {
		for _, acct := range config.AllowedClaudeAccounts(lc.ClaudeAccounts, repo) {
			variant, ok := jobWorkflow(w, acct)
			if !ok {
				continue
			}
			jobs[gcp.JobName(repoSlug, variant)] = true
		}
	}
	env := config.ReservedSecrets[config.BitbucketSecret]
	var (
		mounting []string
		unknown  []string
	)
	for _, job := range slices.Sorted(maps.Keys(jobs)) {
		refs, exists, err := be.JobSecretRefs(ctx, job)
		switch {
		case err != nil:
			unknown = append(unknown, pluginwire.Printable(job))
		case !exists:
			// Not deployed: nothing of it can still mount the token, and the
			// deploy that creates it is the one that reads the flip.
		case refs[env] != "":
			mounting = append(mounting, pluginwire.Printable(job))
		}
	}
	switch {
	case len(mounting) == 0 && len(unknown) == 0:
		return []doctorCheck{{ID: id, OK: true}}
	case len(mounting) == 0:
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("%s is proxied, and whether %s still mounts %s could not be checked: the deployed jobs could not be read", printable, strings.Join(unknown, ", "), config.BitbucketSecret),
			Fix:     "rerun doctor with the Cloud Run API available"}}
	}
	return []doctorCheck{{ID: id, Severity: "warning",
		Problem: fmt.Sprintf("%s is proxied, and %s still mounts %s: the job was applied before the flip, so its account's accessor is gone and the next deploy of it fails on the mount", printable, strings.Join(mounting, ", "), config.BitbucketSecret),
		Fix:     "re-apply the repository so the jobs are deployed without the mount: " + selfCommand() + " update --repo " + quoteWord(repo)}}
}

// flipServes is the flip's own switch for one repository, judged through the
// one predicate the plan a re-apply writes is built with
// (infra.CredentialsServiceServes), so doctor cannot report a stale mount for
// a repository whose jobs the flip deliberately leaves alone. The switch's own
// two conditions are read here — the local config's per-repository record of a
// root applied with the service on, and the checkout's agent.auth — and the
// installation's block, which the predicate takes first, is the caller's own
// early return: a caller that reached the check at all holds it.
func flipServes(lc *localcfg.Config, repo, auth string) bool {
	return infra.CredentialsServiceServes(true, lc.Repos[repo].CredentialsServiceOn(), auth)
}

// flipNotServed names the condition that holds the switch off, in the words of
// the state rather than of the predicate: the record first, since a root that
// was never applied with the service on is the state an operator can act on.
// The budget mode is no longer one of those conditions (AB15, D3009), so an
// `api-key` or `oauth` repository reaches this function only with its record
// off, and the auth the service does not serve at all is the other state here.
func flipNotServed(lc *localcfg.Config, repo, auth string) string {
	if !lc.Repos[repo].CredentialsServiceOn() {
		return "the local config records no apply of its root with the service on"
	}
	return fmt.Sprintf("its agent.auth is %q, and the service serves the %q, %q and %q ones", auth, config.AuthCascade, config.AuthAPIKey, config.AuthOAuth)
}

// doctorModelKeyInAgent is doctor's "model-key-in-agent" check: a model
// credential this workflow's agent holds in its own environment (D503d).
//
// The workflow's auth is what decides it: api-key mounts ANTHROPIC_API_KEY and
// oauth mounts CLAUDE_CODE_OAUTH_TOKEN into the agent's environment unless
// something holds the credential away from it — the budget gateway, which
// carries an api-key run's key itself (runner.GatewayOn: oauth never goes
// through it, and vertex and cascade carry no credential of the job's own at
// all), or the installation's credentials service.
//
// The service's half is the SWITCH's own predicate and not the installation's
// block (B2.2, B2.3, D2071): an `api-key` or `oauth` repository reaches the
// service where the record `fugaro update` left for it says its root was
// applied with the switch on — which is what a bundle holding the credential
// its auth needs leaves behind; the budget mode was the second condition there
// and is none any more (AB15, D3009). A switched run's agent environment is
// built as the cascade's
// (runner's servedAuth), so no credential of the job's reaches the agent, and
// no mount of the two Claude credentials is left for it to read either once the
// root carrying the switch has been applied since the flip (B2.7, G17).
// A repository off the switch keeps both, and the line names what the switch
// is missing: the mode, or the bundle entry the auth's runs are served under.
//
// What it may NOT say is that the bundle lacks an entry: the bundle's document
// is read only behind --check-bitbucket-token (doctorBundleChecks), so the
// entry is named as the one the switch waits for and never as one doctor
// found absent.
//
// Information, never a failure: an installation that runs without the budget
// is a legitimate choice, and the line is the reading an owner asked for — the
// agent's own environment is where a leaked key is worth the most.
func doctorModelKeyInAgent(lc *localcfg.Config, rf *resolvedFile, repo string) *doctorCheck {
	if rf == nil || rf.Cfg == nil {
		return nil
	}
	auth := rf.Cfg.Agent.Auth
	if auth == "" {
		auth = config.DefaultAgentAuth
	}
	var env string
	switch auth {
	case "api-key":
		env = config.ReservedSecrets[config.AnthropicSecret]
	case "oauth":
		env = config.ReservedSecrets[config.ClaudeOAuthToken]
	default:
		return nil
	}
	// C1's reading of the local config's mode (AB3, D3009): an unset mode, or
	// one a hand edit left as the old word, is observe, which is the mode the
	// switch below admits — so no branch of this check has a mode that is
	// neither of the two any more.
	mode := policy.Mode(lc.BudgetMode()).Effective()
	if runner.GatewayOn(auth, mode) {
		return nil
	}
	// The bundle entries this auth's runs are served under, in the bundle's own
	// spelling (the document key `fugaro creds set` takes): the Anthropic key
	// for api-key, and every account an oauth repository's workflows may run
	// under, resolved through this machine's aliases first — exactly the set
	// the switch's readiness reads (credentialsBundleServes).
	entries := []string{config.AnthropicSecret}
	if auth == "oauth" {
		entries = oauthSwitchBundleEntries(rf.Cfg, lc, repo)
	}
	needs := "`" + strings.Join(entries, "`, `") + "`"
	installed := lc.CredentialsService != nil && lc.CredentialsService.URL != ""
	local, _ := lc.RepoFor(repo)
	if runner.ServiceSwitchedAuth(auth) && infra.CredentialsServiceServes(installed, local.CredentialsServiceOn(), auth) {
		return nil
	}
	var why string
	switch {
	case !installed:
		why = fmt.Sprintf("the installation has no credentials service to hold it, and the switch would need %s in its bundle", needs)
	default:
		why = fmt.Sprintf("the credentials service is held off for this repository: %s records a root as applied with the switch on only where the service's bundle holds %s, and this repository's record does not say it was", selfCommand()+" update --repo", needs)
	}
	// The two routes are the auth's own. An api-key run reaches the service
	// THROUGH the gateway, so turning the budget on holds the key whether or not
	// a service is deployed; an oauth run never starts a gateway (F30), so the
	// service is the only route there. The budget mode is a condition of
	// neither route to the service (AB15, D3009): what the switch waits for is
	// the bundle.
	route := fmt.Sprintf("put the installation's credentials service in front of it (an ordinary %s update deploys it, and its own %s update --repo switches this repository onto it once the service's bundle holds %s: %s creds set %s)",
		selfCommand(), selfCommand(), needs, selfCommand(), entries[0])
	fix := "to take it out of the agent's reach, " + route + " — the gateway is no route of its own, because an oauth run never starts one"
	if auth == "api-key" {
		fix = "to take it out of the agent's reach, turn the budget on (budget.mode observe or enforce: the gateway holds the key), or " + route
	}
	// The two facts the update record carries beside the switch (KS19) are said
	// in the line that reports the switch, because a fact the record says is on
	// while the switch is held off is the reading worth having: the bundle, the
	// service and the allow-list hold for it, so the half that is missing is the
	// one named above and nothing else.
	facts := fmt.Sprintf("the record %s leaves for this repository says its provider keys are %s and its stage account is %s",
		selfCommand()+" update --repo", servedFact(local.ProviderKeysServedOn()), servedFact(local.StageAccountServedOn()))
	return &doctorCheck{ID: "model-key-in-agent", Severity: "info",
		Problem: fmt.Sprintf("this repository's workflows (%s) run with agent.auth %s, and the model credential reaches the agent's own environment as %s (%s, and %s)",
			strings.Join(printableNames(slices.Sorted(maps.Keys(rf.Cfg.Workflows))), ", "), auth, env, why, facts),
		Fix: fix}
}

// servedFact is what the update record says about one of the two facts it
// decides beside the switch, in the record's own words: the field is written as
// a switch (localcfg.RecordSwitch), so a fact written off reads exactly as one
// that was never written.
func servedFact(on bool) string {
	if on {
		return "served"
	}
	return "not served"
}

// oauthSwitchBundleEntries are the bundle's own DOCUMENT keys for an oauth
// repository's runs: one per account infra.WorkflowAccounts gives it — the
// accounts its jobs exist for, which are the accounts a launch may name — with
// this machine's aliases resolved first, because a name that is an alias would
// otherwise be held off for an account the bundle does hold
// (credentialsBundleServes resolves the same way). The spelling is the document
// key and not the account, because it is what `fugaro creds set` takes and what
// the service's loader reads back as that account. The set is never empty:
// AllowedClaudeAccounts always names the default account, which is why the
// caller may read its first element as a name `fugaro creds set` takes.
func oauthSwitchBundleEntries(cfg *config.Config, lc *localcfg.Config, repo string) []string {
	accounts := infra.WorkflowAccounts(cfg, lc, repo)
	out := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if resolved, _ := lc.ResolveAccount(account); resolved != "" {
			account = resolved
		}
		out = append(out, config.ClaudeAccountSecret(account))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// proxiedBitbucketRepos are the repositories of this installation's local
// config that its project_secrets entry admits with proxy: true — the
// repositories the credentials service forwards the git credential for once
// the flip list names it (B1.8, D500). A wildcard pattern admits repositories
// nobody can enumerate, so the ones named here are the local config's own
// records; a repository the pattern admits and the config does not name is not
// among them.
func proxiedBitbucketRepos(lc *localcfg.Config) []string {
	entry, ok := lc.ProjectSecrets[config.BitbucketSecret]
	if !ok || !entry.Proxying() {
		return nil
	}
	var out []string
	for _, repo := range slices.Sorted(maps.Keys(lc.Repos)) {
		if lc.Repos[repo].Provider != gitprov.KindBitbucket {
			continue
		}
		if entry.AllowsData(repo) {
			out = append(out, repo)
		}
	}
	return out
}

// doctorIntakeChecks is DS4.2's read of the dynamic class's own account: the
// secrets intake is deployed, DS2.2 gives it no way to read a value, its write
// role is bound under the name fence, and no container of the project carries a
// prefix of the class that no grammar admits.
//
// It reads the project's IAM policy the two reader checks already read — the
// caller passes the one answer down rather than asking again
// (doctorSecretManagerChecks' rule) — and the account is named by the id
// modules/installation/intake.tf creates (tf.IntakeAccountID), which is the
// only thing that says which member is the intake's at all. A policy that
// could not be read is left to the line that already reports it, the sweep's or
// the deployers': a second sentence about one failed read is noise, and the
// checks that read the policy each ask it a question of their own.
//
// The accessor is a warning and never a failure: the account is granted one by
// a hand change or by a root applied before the class existed, and an
// installation mid-repair must not be failed by doctor (the sweep's own rule).
// So is a missing write grant, which is the same class of state read the other
// way round: doctor reports it rather than reading it as a fence that holds.
// A grant that reads values is the worst of the two states the check reports —
// it lets the account read what it wrote — and an unfenced write grant is the
// other: it lets the intake create, version and delete ANY container of the
// project, which is what the fence exists to bound (R3).
func doctorIntakeChecks(ctx context.Context, lc *localcfg.Config, sm *gcp.Secrets, pol *crm.Policy, polErr error, roles *iamRoles) []doctorCheck {
	const id = "secrets-intake"
	checks := doctorDynamicSecretNameChecks(ctx, sm)
	if polErr != nil {
		return checks
	}
	member := intakeAccountMember(lc.GCPProject)
	writeRole := "projects/" + lc.GCPProject + "/roles/" + tf.IntakeSecretsRole
	var (
		deployed   bool
		writeBound bool
		readers    []string
		unread     []string
		unfenced   []string
	)
	for _, b := range pol.Bindings {
		if b == nil || !slices.Contains(b.Members, member) {
			continue
		}
		deployed = true
		carries, errText := intakeReaderRole(b.Role, roles)
		switch {
		case errText != "":
			unread = append(unread, pluginwire.Printable(b.Role))
		case carries:
			readers = append(readers, pluginwire.Printable(b.Role))
		}
		if b.Role == writeRole {
			writeBound = true
			if !fencedUnderSecretNames(b.Condition) {
				unfenced = append(unfenced, pluginwire.Printable(b.Role))
			}
		}
	}
	for _, list := range [][]string{readers, unread, unfenced} {
		slices.Sort(list)
	}
	if !deployed {
		checks = append(checks, doctorCheck{ID: id, OK: true, Severity: "info",
			Problem: "no secrets intake is deployed in " + lc.GCPProject + ": no binding of its project policy names " + member})
	}
	// A binding names the account but none of them is the write role: half an
	// installation, or a grant taken off by hand. Nothing of the class works in
	// that state — the account is what creates, versions and deletes the
	// containers — and the fence is not bound at all, which is the fact the
	// measurement below would otherwise claim.
	if deployed && !writeBound {
		checks = append(checks, doctorCheck{ID: id, Severity: "warning",
			Problem: "the intake's account " + member + " is deployed but no binding of " + lc.GCPProject + "'s policy gives it " + pluginwire.Printable(writeRole) + ": the account is the one that creates, versions and deletes the dynamic containers (DS2.2), so no credential can be written through it",
			Fix:     "re-apply the installation root that writes the grant: " + selfCommand() + " update   # modules/installation/intake.tf"})
	}
	// One line per role, so each carries the removal command for the grant it
	// names: gcloud takes one --role per call, and an account holding two of
	// them is two grants to take off.
	for _, role := range readers {
		checks = append(checks, doctorCheck{ID: id, Severity: "warning",
			Problem: "the intake's account " + member + " holds " + role + ", a grant that reads secret values: the intake creates, versions and deletes the dynamic containers and its design gives it no way to read one (DS2.2, R3), so a holder of this grant can read what it writes",
			Fix:     "remove the grant: gcloud projects remove-iam-policy-binding " + quoteWord(lc.GCPProject) + " --member=" + member + " --role=" + role})
	}
	if len(unfenced) > 0 {
		checks = append(checks, doctorCheck{ID: id, Severity: "warning",
			Problem: "the intake's write grant " + strings.Join(unfenced, ", ") + " is not bound under the per-user and project-shared name-prefix condition: the role creates, versions and deletes containers, so without the fence the account reaches every secret of " + lc.GCPProject,
			Fix:     "re-apply the installation root that writes this grant: " + selfCommand() + " update   # modules/installation/intake.tf binds it under the " + secretname.UserPrefix + "/" + secretname.ProjectPrefix + " fence"})
	}
	if len(unread) > 0 {
		checks = append(checks, doctorCheck{ID: id, Severity: "info", unread: true,
			Problem: "could not read what " + strings.Join(unread, ", ") + " carries, so whether the intake's account can read a secret's value through it is unknown, not no",
			Fix:     "rerun fugaro doctor with access to the project, or as an operator who can read each role (gcloud iam roles describe ROLE)"})
	}
	if writeBound && len(readers) == 0 && len(unread) == 0 && len(unfenced) == 0 {
		checks = append(checks, doctorCheck{ID: id, OK: true, Severity: "info",
			Problem: "the secrets intake is deployed in " + lc.GCPProject + ", its account " + member + " holds no role that reads a secret's value, and its write grant is bound under the " + secretname.UserPrefix + "/" + secretname.ProjectPrefix + " name-prefix condition"})
	}
	return checks
}

// intakeReaderRole is secretReaderRole plus the project's basic roles: an
// owner reads a secret's value through the project's own grant, so an account
// holding one is an account that reads whatever it wrote, which is the fact
// this check reports. The sweep leaves basic roles to F3's line, which names
// every holder of one project-wide; here the question is about one account, and
// a measurement that said it held no reading role while the same run's F3 line
// listed it as an owner would be doctor contradicting itself.
func intakeReaderRole(role string, roles *iamRoles) (bool, string) {
	if slices.Contains(basicRoles, role) {
		return true, ""
	}
	return secretReaderRole(role, roles)
}

// intakeAccountMember is the intake's own account as an IAM policy spells it:
// the account modules/installation/intake.tf creates in the installation's
// project (tf.IntakeAccountID), which is what every one of the class's grants
// names it by.
func intakeAccountMember(project string) string {
	return "serviceAccount:" + tf.IntakeAccountID + "@" + project + ".iam.gserviceaccount.com"
}

// fencedUnderSecretNames reports whether a binding's condition bounds it to the
// class's names: the expression has to carry both prefixes the fence is made
// of, which is the whole of what the condition is for (R3). The title is not
// read — it names the condition, never what it admits — and the exact
// expression is pinned where it is written, by the plan-time check on
// modules/installation/intake.tf (internal/infra/tf's fence matcher), so this
// read answers "is there a fence here at all" rather than re-deciding the
// module's own text.
func fencedUnderSecretNames(cond *crm.Expr) bool {
	if cond == nil {
		return false
	}
	return strings.Contains(cond.Expression, secretname.UserPrefix) && strings.Contains(cond.Expression, secretname.ProjectPrefix)
}

// doctorDynamicSecretNameChecks is DS4.2's name read: a container of the
// installation's project whose name carries one of the class's prefixes and
// which secretname.Parse refuses.
//
// Such a name is in the fence's reach and outside every reader's: the intake's
// write grant admits it — the condition is a prefix test — while the routes
// (DS2.1) and the credentials service (DS3.3) both refuse a name the grammar
// does not admit, so the container can be versioned and deleted by the intake
// and can never be resolved by anyone. It is a hand-written container or a
// leftover of a rename, and it is what a member's "my credential is not
// reaching the run" looks like from the outside.
//
// The listing is metadata only and asks the project once more, which the reader
// sweep also lists: the sweep's listing answers "who can read these", and a
// name is not a reader. A listing that fails is left to the sweep, which
// reports that failure in its own words.
func doctorDynamicSecretNameChecks(ctx context.Context, sm *gcp.Secrets) []doctorCheck {
	if sm == nil {
		return nil
	}
	containers, err := sm.List(ctx, nil)
	if err != nil {
		return nil
	}
	slices.SortFunc(containers, func(a, b gcp.SecretInfo) int { return strings.Compare(a.ID, b.ID) })
	var out []doctorCheck
	for _, c := range containers {
		if prefixOf(c.ID) == "" {
			continue
		}
		_, err := secretname.Parse(c.ID)
		if err == nil {
			continue
		}
		out = append(out, doctorCheck{ID: "dynamic-secret-name", Severity: "warning",
			Problem: "secret " + pluginwire.Printable(c.ID) + " is named as a dynamic one and the name grammar refuses it, so no reader can ever resolve it: " + err.Error(),
			Fix:     "delete it if nothing uses it (gcloud secrets delete SECRET), or write the credential again under a name the grammar admits: " + secretname.UserPrefix + "<account id>_<name> or " + secretname.ProjectPrefix + "<name>"})
	}
	return out
}

// prefixOf is the class's prefix a container's name carries, "" for one of
// neither: it is the fence's own test, in the one place this package asks it.
func prefixOf(id string) string {
	for _, p := range []string{secretname.UserPrefix, secretname.ProjectPrefix} {
		if strings.HasPrefix(id, p) {
			return p
		}
	}
	return ""
}

// doctorLegacyOpenPoolChecks is D1942's line: a pool the migration marked
// legacy_open is served to whoever asks (config.Pool.Serves), so it is the one
// kind of pool nobody owns, and the migration is what put it in that state — it
// marks the pools that predate owners rather than guessing one for them.
//
// The published node is where a pool is served from, so it is what the check
// reads, through the same read doctor's other node questions use. D1942 asks
// the line to ask an admin to name an owner, and it does: no command of this
// release writes a pool's owner — `fugaro accounts pool` writes the members of
// the installation's own config, and publishing carries the node's pools
// forward — so the line names the member to write (the node's
// pools.<name>.owner, with the people the pool may be served to as
// pools.<name>.allowed_users) rather than a command that would not write it.
//
// A node that is absent, unreadable or older than this binary is not this
// check's to report: the policy checks own those states, and a pool question
// asked of a node nobody read would answer nothing.
func doctorLegacyOpenPoolChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	p, _, err := projectPolicyRead(ctx, lc)
	if err != nil || p == nil {
		return nil
	}
	var names []string
	for _, pool := range p.CraftPools {
		if pool.LegacyOpen {
			names = append(names, pluginwire.Printable(pool.Name))
		}
	}
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names)
	return []doctorCheck{{ID: "pools-legacy-open", Severity: "warning",
		Problem: "the published policy serves " + strings.Join(names, ", ") + " as legacy-open: a pool the migration left with no owner is served to everyone the roster admits, so no one answers for it (D1942)",
		Fix:     "ask a project admin to name each one an owner in the published policy (pools.<name>.owner, and the people it may be served to as pools.<name>.allowed_users)"}}
}
