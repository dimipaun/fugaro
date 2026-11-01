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

	"google.golang.org/api/googleapi"
	runapi "google.golang.org/api/run/v2"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/runner"
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

// doctorCredentialsServiceDeployed is doctor's "credentials-service-deployed"
// check: the installation opted into a credentials service (the block is
// there), the service exists, and every job account that may call it holds a
// role that can invoke it.
//
// Both halves are read from the service's own IAM policy, which is where the
// module writes the grant: one member, the job's own account, roles/run.invoker
// on that one service. The project's policy is deliberately not read a second
// time — doctor reads it once, for the reads that ask it a question no other
// answer covers (TestDoctorSecretManagerChecksReadsTheProjectPolicyOnce) — so a
// job account granted the role at project level instead reads as missing, which
// is not what an installation writes and is said in the fix. A service that is
// not there is answered by Cloud Run as NOT_FOUND, which is the only way to see
// it: the fake this check is tested against serves no service get, and the real
// API's get is the same question asked twice.
//
// It is a warning and not an ordinary failure: a run whose job cannot invoke
// the service cannot fetch a model credential, but an installation mid-opt-in
// (the block written, the apply that creates the service still to come) is a
// legitimate state, and --strict is where that is a failure.
func doctorCredentialsServiceDeployed(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	if lc.CredentialsService == nil {
		return nil
	}
	const id = "credentials-service-deployed"
	unreadable := func(what string) []doctorCheck {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the installation has opted into the credentials service (%s), and %s could not be read: whether it is deployed and whether its callers may invoke it could not be checked", infra.CredentialsServiceName, what),
			Fix:     "rerun doctor with your credentials and the Cloud Run API available, or check by hand: " + selfCommand() + " update --provision-credentials re-applies the installation and its service"}}
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
				Problem: fmt.Sprintf("the local config opted this installation into the credentials service (%s), and Cloud Run has no service %s in project %s, region %s: every run of it would fail to fetch a model credential", infra.CredentialsServiceName, infra.CredentialsServiceName, pluginwire.Printable(lc.GCPProject), pluginwire.Printable(lc.Region)),
				Fix:     selfCommand() + " update --provision-credentials (re-applies the installation root, which creates the service)"}}
		}
		return unreadable("the IAM policy of the Cloud Run service " + infra.CredentialsServiceName + " (" + oneLineCLI(err.Error()) + ")")
	}
	bindings := bindingsOf(pol.Bindings, runBindingFields)
	var (
		missing []string
		unread  []string
	)
	for _, j := range jobs {
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
		}
	}
	switch {
	case len(missing) == 0 && len(unread) == 0:
		return []doctorCheck{{ID: id, OK: true}}
	case len(missing) == 0:
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("the credentials service %s is deployed, and whether %s may invoke it could not be checked: a custom role on its or the project's policy could not be read", infra.CredentialsServiceName, strings.Join(unread, ", ")),
			Fix:     "rerun doctor with the IAM API available, or read the policy yourself: gcloud run services get-iam-policy " + infra.CredentialsServiceName}}
	}
	return []doctorCheck{{ID: id, Severity: "warning",
		Problem: fmt.Sprintf("the credentials service %s is deployed and grants no %s to %s: a run of %s would fail to fetch a model credential%s",
			infra.CredentialsServiceName, runInvokeRoles[0], strings.Join(missing, ", "), strings.Join(missing, ", "),
			noteIfAny("; not read: "+strings.Join(unread, ", "), len(unread) > 0)),
		Fix: selfCommand() + " update --repo <each repository named above> (grants each of its job accounts " + runInvokeRoles[0] + " on the service)"}}
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

// doctorBundleChecks are the two checks that read the bundle's value, both
// behind --check-bitbucket-token and both from ONE read of the document: what
// it holds (bundle-bitbucket-token) and whether Bitbucket still accepts the
// token it holds (bitbucket-token-valid).
//
// The document is the service's own: `creds ls` reads it with the operator's
// identity, the read is in memory, and the value is handed to one Bitbucket
// client per proxied repository and nowhere else. A repository counts as
// proxied when the operator's project_secrets entry admits it with proxy: true
// — the local half of B1.8's flip, which is what decides whether the service
// forwards this repository's git credential — and the repositories are the
// ones the local config records, since a wildcard pattern admits a repository
// nobody can enumerate.
func doctorBundleChecks(ctx context.Context, lc *localcfg.Config) []doctorCheck {
	const (
		idBundle = "bundle-bitbucket-token"
		idValid  = "bitbucket-token-valid"
	)
	if lc.CredentialsService == nil {
		return []doctorCheck{{ID: idBundle, Severity: "info",
			Problem: "the installation has not opted into the credentials service, so it has no bundle for a Bitbucket token to live in: runs of a proxied repository have nowhere to fetch one",
			Fix:     "to broker the Bitbucket token, opt in first: " + selfCommand() + " update --provision-credentials"}}
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
	entry, held := b.BitbucketToken()
	if !held {
		return []doctorCheck{{ID: idBundle, Severity: bundleSeverity(len(proxied)),
			Problem: bundleMissingProblem(store.id, len(proxied)),
			Fix:     selfCommand() + " creds set " + config.BitbucketSecret}}
	}
	// The bundle's own answer is a measurement and not doctor's bookkeeping: the
	// question the flag was passed for is whether the service holds a token, and
	// the line that answers it is what makes an installation with nothing wrong
	// readable under --all. The token's value is not in it, and never is: what
	// is said is that the bundle holds the credential, which is why a name and
	// not a value is what every message here carries.
	out := []doctorCheck{{ID: idBundle, OK: true, Severity: "info",
		Problem: fmt.Sprintf("the credentials service's bundle %s holds a %s credential", pluginwire.Printable(store.id), config.BitbucketSecret)}}
	out = append(out, doctorBitbucketTokenValid(ctx, lc, proxied, entry.Value())...)
	return out
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

// doctorBitbucketTokenValid is the one check that asks Bitbucket: one
// GET /repositories/<repo> per proxied repository and per root, with the
// bundle's token, in memory. A token that Bitbucket refuses (401, 403) or
// cannot see a repository with (404) fails the check as a warning, because
// every run of that repository fails at its first git call; anything else — a
// network that is down, a Bitbucket outage, a rate limit — is reported as not
// checked rather than as a bad token, since sending the operator to rotate a
// working credential is worse than saying the question was not answered.
//
// The roots are bitbucketTokenRoots': Bitbucket's own API, or the installation's
// own allow-list when it lists one. Asking Bitbucket's API about a token issued
// for a root the installation listed is the false "refused, rotate" this
// answers: the runs of that installation send the token to its own root, and
// that is where the question belongs.
func doctorBitbucketTokenValid(ctx context.Context, lc *localcfg.Config, repos []string, token string) []doctorCheck {
	if len(repos) == 0 {
		return nil
	}
	roots := bitbucketTokenRoots(lc)
	const id = "bitbucket-token-valid"
	var (
		refused []string
		unknown []string
	)
	for _, repo := range repos {
		workspace, slug, ok := strings.Cut(repo, "/")
		if !ok || workspace == "" || slug == "" {
			unknown = append(unknown, pluginwire.Printable(repo))
			continue
		}
		switch askTokenAtRoots(ctx, roots, workspace, slug, token) {
		case tokenAccepted:
		case tokenRefused:
			refused = append(refused, pluginwire.Printable(repo))
		default:
			unknown = append(unknown, pluginwire.Printable(repo))
		}
	}
	switch {
	case len(refused) == 0 && len(unknown) == 0:
		return []doctorCheck{{ID: id, OK: true}}
	case len(refused) == 0:
		// unread, because that is what this state is: a question Bitbucket was
		// asked and did not answer. The marker is set by the check that could
		// not read, and the class reads it as the unknown it is — the alternative
		// is a fact left unknown that reads like one doctor measured and found
		// nothing in.
		return []doctorCheck{{ID: id, Severity: "info", unread: true,
			Problem: fmt.Sprintf("Bitbucket could not be asked about %s, so whether the bundle's %s credential is still accepted there could not be checked", strings.Join(unknown, ", "), config.BitbucketSecret),
			Fix:     "rerun doctor when Bitbucket is reachable"}}
	}
	return []doctorCheck{{ID: id, Severity: "warning",
		Problem: fmt.Sprintf("Bitbucket refuses the bundle's %s credential for %s: a run of each of those repositories fails at its first git call%s",
			config.BitbucketSecret, strings.Join(refused, ", "), noteIfAny("; not asked about "+strings.Join(unknown, ", "), len(unknown) > 0)),
		Fix: "store a token Bitbucket accepts, with Repositories read and Pull requests read/write: " + selfCommand() + " creds set " + config.BitbucketSecret}}
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
func bitbucketTokenRoots(lc *localcfg.Config) []string {
	if lc != nil && len(lc.BitbucketAPIRoots) > 0 {
		return lc.BitbucketAPIRoots
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
)

// askTokenAtRoots asks each root the token may be sent to, and answers accepted
// as soon as one accepts it: an installation may list more than one root, and
// the token is good for the runs that use that one. Refused is *every* root
// refusing — a single root in the common case — so a listed root that is down
// leaves the answer unanswered rather than refused, which is what keeps a
// working token from being sent to be rotated. Each root is asked with the
// client of the check, whose timeout bounds the request.
func askTokenAtRoots(ctx context.Context, roots []string, workspace, slug, token string) tokenAnswer {
	refused, answered := 0, 0
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
		case isBitbucketRefusal(err):
			refused++
		}
	}
	if answered > 0 && refused == answered {
		return tokenRefused
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

// doctorBitbucketDirect is doctor's "bitbucket-direct" check: this checkout's
// repository is a Bitbucket one, and what its jobs hold of the token.
//
// Before the flip — the repository not admitted with proxy: true, or
// config.ProxiedGitSecretNames not naming the token yet — a job mounting
// bitbucket-token is the state the installation is in, and the line is
// information: the run holds a repository access token directly, which is
// D462's reach. After the flip, for a repository the owner proxied, that same
// mount is a job applied before the flip: the next deploy of it would fail on
// a mount whose accessor is gone, and the run would keep reaching the token
// the service is supposed to hold. That is the warning.
func doctorBitbucketDirect(ctx context.Context, lc *localcfg.Config, repo, provider string, facts doctorRepoSecrets) []doctorCheck {
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
	proxied := ok && entry.Proxy && entry.AllowsData(repo)
	flipped := slices.Contains(config.ProxiedGitSecretNames, config.BitbucketSecret)
	if !proxied {
		return []doctorCheck{{ID: id, Severity: "info",
			Problem: fmt.Sprintf("%s is a Bitbucket repository that is not proxied: its jobs mount their own %s, so a run of it holds a credential that reaches the repository itself (D462)", printable, config.BitbucketSecret),
			Fix:     "to broker it instead, add it to the local config's project_secrets entry for " + config.BitbucketSecret + " (allow_data_to with " + printable + ", proxy: true), publish the policy, then re-apply the repository: " + selfCommand() + " update --repo " + quoteWord(repo)}}
	}
	if !flipped {
		return []doctorCheck{{ID: id, Severity: "info",
			Problem: fmt.Sprintf("%s is admitted with proxy: true and its jobs still mount their own %s: the release that fills the proxy's flip list has not shipped, so the service does not forward the credential yet", printable, config.BitbucketSecret),
			Fix:     "nothing to do: re-apply the repository with " + selfCommand() + " update --repo " + quoteWord(repo) + " once the release that flips the proxy is installed"}}
	}
	if lc.Endpoints.NoAuth && lc.Endpoints.Run == "" {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("%s is proxied and flipped, and the deployed jobs could not be read (a test configuration), so whether a job still mounts %s could not be checked", printable, config.BitbucketSecret),
			Fix:     "rerun doctor with the Cloud Run API available"}}
	}
	be, err := gcp.New(ctx, gcp.Options{GCPProject: lc.GCPProject, Region: lc.Region, LogView: lc.LogView,
		Endpoints: gcp.Endpoints{Run: lc.Endpoints.Run, NoAuth: lc.Endpoints.NoAuth}})
	if err != nil {
		return []doctorCheck{{ID: id, Severity: "warning",
			Problem: fmt.Sprintf("%s is proxied and flipped, and the Cloud Run API could not be reached (%s), so whether a job still mounts %s could not be checked", printable, oneLineCLI(err.Error()), config.BitbucketSecret),
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
			Problem: fmt.Sprintf("%s is proxied and flipped, and whether %s still mounts %s could not be checked: the deployed jobs could not be read", printable, strings.Join(unknown, ", "), config.BitbucketSecret),
			Fix:     "rerun doctor with the Cloud Run API available"}}
	}
	return []doctorCheck{{ID: id, Severity: "warning",
		Problem: fmt.Sprintf("%s is proxied and flipped, and %s still mounts %s: the job was applied before the flip, so its account's accessor is gone and the next deploy of it fails on the mount", printable, strings.Join(mounting, ", "), config.BitbucketSecret),
		Fix:     "re-apply the repository so the jobs are deployed without the mount: " + selfCommand() + " update --repo " + quoteWord(repo)}}
}

// doctorModelKeyInAgent is doctor's "model-key-in-agent" check: a model
// credential this workflow's agent holds in its own environment (D503d).
//
// The workflow's auth is what decides it: api-key mounts ANTHROPIC_API_KEY and
// oauth mounts CLAUDE_CODE_OAUTH_TOKEN into the agent's environment unless
// something holds the credential away from it — the budget gateway, which
// carries an api-key run's key itself (runner.GatewayOn: oauth never goes
// through it, and vertex and cascade carry no credential of the job's own at
// all). An installation opted into the credentials service holds every model
// credential too, and the jobs give their mounts up.
//
// Information, never a failure: an installation that runs without the budget
// is a legitimate choice, and the line is the reading an owner asked for — the
// agent's own environment is where a leaked key is worth the most.
func doctorModelKeyInAgent(lc *localcfg.Config, rf *resolvedFile) *doctorCheck {
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
	if runner.GatewayOn(auth, lc.BudgetMode()) || lc.CredentialsService != nil {
		return nil
	}
	why := "the installation has no credentials service to hold it"
	if auth == "api-key" {
		why = "budget.mode is off, so no gateway holds it"
	}
	return &doctorCheck{ID: "model-key-in-agent", Severity: "info",
		Problem: fmt.Sprintf("this repository's workflows (%s) run with agent.auth %s, and the model credential reaches the agent's own environment as %s (%s)",
			strings.Join(printableNames(slices.Sorted(maps.Keys(rf.Cfg.Workflows))), ", "), auth, env, why),
		Fix: "to take it out of the agent's reach, turn the budget on (budget.mode observe or enforce: the gateway holds the key), or opt the installation into the credentials service (" + selfCommand() + " update --provision-credentials)"}
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
	if !ok || !entry.Proxy {
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
