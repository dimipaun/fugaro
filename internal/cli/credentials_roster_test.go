package cli

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// credentialsRosterImage is the digest-pinned reference infra.Credentials
// accepts: what the deploy's own resolve-and-verify step hands it, spelled here
// because this test is about the roster and not about the image.
func credentialsRosterImage() string {
	return "us-east5-docker.pkg.dev/proj-1234/fugaro-ghcr/dimipaun/fugaro-credentials:1.2.3@sha256:" + strings.Repeat("ab", 32)
}

// The three service accounts of the fixture: two in the shape a repository's
// job account has (modules/workflow/sa.tf), one in no shape at all, so that the
// test cannot pass by matching a prefix.
const (
	rosterSAJob   = "fugaro-p-acme-sandbox-0123abcd@proj-1234.iam.gserviceaccount.com"
	rosterSASweep = "fugaro-p-acme-webapp-5b8bba58@proj-1234.iam.gserviceaccount.com"
	rosterSAOther = "fugaro-reports@proj-1234.iam.gserviceaccount.com"
)

// credentialsRosterLCTemplate is a project config the credentials service can
// be deployed into at all (a budget backend and one repository with a workflow,
// which infra.Credentials refuses to write the block without) with a roster
// that names every member class at once: a person, a service account, a group
// and a domain on each of the two lists the launcher class comes from, and the
// same on budget_admins, whose `user:` entries are the LAUNCH GATE's implicit
// admins (userImplicitAdmins below).
const credentialsRosterLCTemplate = `version: 3
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
build: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }
user: test@example.com
repos:
  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }
budget: {mode: observe, per_run_usd: 5, firebase_project: aurora-fp, rtdb_url: "https://aurora-fp-default-rtdb.firebaseio.com"}
terraform:
  operators: ['user:ops@example.com', 'serviceAccount:%s', 'group:eng@example.com']
  admins: ['serviceAccount:%s', 'serviceAccount:%s', 'domain:example.com']
  budget_admins: ['user:Boss@Example.COM', 'group:eng@example.com', 'user:second@example.com', 'serviceAccount:%s']
`

func credentialsRosterLC(t *testing.T) *localcfg.Config {
	t.Helper()
	lc, err := localcfg.Parse([]byte(fmt.Sprintf(credentialsRosterLCTemplate, rosterSAJob, rosterSAJob, rosterSASweep, rosterSAOther)))
	if err != nil {
		t.Fatal(err)
	}
	return lc
}

// TestLauncherServiceAccountsRendered is LM13's roster read at the size the
// service receives it (launcher-does-not-mint plan task LM13; D3123, F25): the
// roster's `serviceAccount:` entries reach the service as the BARE addresses a
// Google ID token's email claim carries, the roster is the same merged list the
// installation root's launcher class is derived from (an operator runs, watches
// and cancels runs like a launcher, launchersWithOperators), and the entries
// that hold no address at all — a `group:` and a `domain:` — are not resolved
// into it.
//
// Why the bare spelling is the whole point. The service compares this list
// against the email claim of the caller's ID token (internal/credsvc's
// foldLauncherAccounts), and the credential half of that claim is the account's
// own address: a `serviceAccount:` member written through unchanged would match
// nobody, so a repository job that passes IAM's invoker check and this class
// check today would be refused by the process — the exact split F31 exists to
// close. A `group:` is the fail-closed direction on purpose: neither IAM's
// grant nor this list can place a group the config names, and admitting a whole
// group is a decision the roster makes by naming its members.
//
// ... and the second half, the implicit admins, is pinned against the GATE's
// own reader rather than against a literal: userImplicitAdmins is what
// `fugaro user grant` admits a caller with when the roles node places them
// nowhere, so the service's list has to BE that list with the member prefix
// taken off (access.Member's normalisation, group and serviceAccount entries
// dropped). The two derivations are one behaviour in two packages, and this is
// what keeps them from drifting: an address the gate admits and the service
// does not is a launch the service refuses, and one the service admits and the
// gate does not is a grant the gate refuses after the service already issued.
//
// Mutation (run, restore): leave the member spelling on (drop the
// strings.CutPrefix in launcherServiceAccounts) and the roster assertion fails;
// lower-case the whole address rather than going through access.Member and the
// implicit-admin assertion fails on the local part's case.
func TestLauncherServiceAccountsRendered(t *testing.T) {
	lc := credentialsRosterLC(t)
	svc, err := infra.Credentials(lc, credentialsRosterImage(), nil)
	if err != nil {
		t.Fatal(err)
	}

	wantRoster := []string{rosterSAJob, rosterSASweep}
	if !slices.Equal(svc.RosterServiceAccounts, wantRoster) {
		t.Errorf("roster_service_accounts = %v, want %v: the roster's service accounts as the bare addresses an ID token's email claim carries — the operators' and the admins' lists merged, sorted, each once — and a `group:` or `domain:` entry not resolved into one (D3123, F25)", svc.RosterServiceAccounts, wantRoster)
	}
	for _, s := range svc.RosterServiceAccounts {
		if strings.Contains(s, ":") {
			t.Errorf("roster_service_accounts holds %q: a member spelling never matches a token's email claim, so the service would refuse the account IAM just admitted (F31)", s)
		}
	}
	if slices.Contains(svc.RosterServiceAccounts, rosterSAOther) {
		t.Errorf("roster_service_accounts holds %q, which no launcher list names: only budget_admins names it, and the implicit admins are a list of their own", rosterSAOther)
	}

	// The gate's own list, read through the gate's own function, and the
	// service's is that list with the member prefix off — sorted and deduped,
	// which the gate's order does not promise.
	gate := userImplicitAdmins(lc)
	var wantAdmins []string
	for _, m := range gate {
		addr, ok := strings.CutPrefix(m, "user:")
		if !ok {
			t.Errorf("the gate's implicit admins hold %q, which is not a `user:` member: this fixture's budget_admins names a group and a service account too, and neither is a person the gate can place", m)
			continue
		}
		wantAdmins = append(wantAdmins, addr)
	}
	slices.Sort(wantAdmins)
	wantAdmins = slices.Compact(wantAdmins)
	if !slices.Equal(svc.ImplicitAdmins, wantAdmins) {
		t.Errorf("implicit_admins = %v, want %v (the gate's own userImplicitAdmins list, bare): the two are one behaviour in two packages, and an address one admits and the other refuses is a launch that passes one check and fails the next", svc.ImplicitAdmins, wantAdmins)
	}
	if want := []string{"Boss@example.com", "second@example.com"}; !reflect.DeepEqual(svc.ImplicitAdmins, want) {
		t.Errorf("implicit_admins = %v, want %v: `user:` off, the group and the service account dropped, and the address normalised the way the gate normalises it (access.Member: the domain lowered, the local part left as written)", svc.ImplicitAdmins, want)
	}
}
