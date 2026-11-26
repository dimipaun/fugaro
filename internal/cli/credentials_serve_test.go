package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	secretmanager "google.golang.org/api/secretmanager/v1"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	infraTF "github.com/dimipaun/fugaro/internal/infra/tf"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/workspace"
)

// The credentials service's entry point (plan task C1.14) is not a command
// anyone is offered: it is what the image's own entrypoint runs, in the Cloud
// Run service the installation module creates, and it takes no argument, no
// flag and no config file. So it is hidden twice over -- the group and the
// subcommand -- and the root help is where that is checked, because a hidden
// cobra command is one that does not appear there and nowhere else.
func TestCredentialsServeHidden(t *testing.T) {
	out, _, err := execute(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if offersCommand(out, "credentials") {
		t.Fatalf("fugaro --help lists the credentials service's command:\n%s", out)
	}
	out, _, err = execute(t, "credentials", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if offersCommand(out, "serve") {
		t.Fatalf("fugaro credentials --help lists serve:\n%s", out)
	}
	// And the hiding is the commands' own Hidden field, not an accident of
	// cobra's rules. The help output above would still be clean without it: a
	// group with no Run of its own and only hidden subcommands is left out of
	// its parent's list anyway, and the day this group gains a visible
	// subcommand `fugaro --help` would list it. The field is what the image's
	// entrypoint is held to.
	root := NewRootCmd()
	group := findSub(t, root, "credentials")
	if !group.Hidden {
		t.Error("the credentials group is not hidden: fugaro --help would list it as soon as it gains a visible subcommand")
	}
	if !findSub(t, group, "serve").Hidden {
		t.Error("credentials serve is not hidden: fugaro credentials --help would list it")
	}
}

// offersCommand reports whether a help page offers a command called name:
// cobra lists a command as an indented line whose first field is its name
// ("  credentials  Run the credentials service ..."). It is a line test and
// not a substring test on purpose -- the `creds` command's own Short names the
// credentials service ("Set and list the credentials service's one bundle
// secret"), so a substring test would fail on a tree where nothing is wrong
// (this test failed exactly that way when C1.25's command landed after C1.14
// was written).
func offersCommand(help, name string) bool {
	for _, line := range strings.Split(help, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == name {
			return true
		}
	}
	return false
}

// findSub returns the named direct subcommand of cmd, failing when there is
// none: a test that walked an empty list instead would pass for the wrong
// reason.
func findSub(t *testing.T, cmd *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("%s has no %q subcommand", cmd.CommandPath(), name)
	return nil
}

// The service's environment is the installation module's: credentials.tf sets
// seven names on the service's container, internal/infra/tf's
// credentialsServiceEnv is the allow-list that pins exactly those, and
// credentialsServiceEnvNames is what this command reads. A service started
// without one of them cannot say which installation it is serving, so the
// command refuses by name before anything else -- the same refusal the history
// job makes for the same variables, and one that never quotes the value it
// found (or did not find).
//
// It walks the seven REQUIRED names and not the eighth: the module renders
// FUGARO_VERIFIER_ACCOUNTS only on an installation that named a verifier, so
// requiring it here would refuse exactly the deployments this list exists to
// keep working (TestCredentialsServeStartsWithoutTheVerifierList is the other
// half of that).
func TestCredentialsServeRefusesWithoutItsEnvironment(t *testing.T) {
	for _, k := range credentialsServiceEnvNames {
		t.Run(k, func(t *testing.T) {
			// Every name is set, then the one under test is emptied: an empty
			// variable is not a value the module can set (Terraform writes
			// each of these from an input the installation has), so the
			// refusal must be about that name and not about another.
			for _, n := range credentialsServiceEnvNames {
				t.Setenv(n, credentialsServeEnv[n])
			}
			t.Setenv(k, "")
			_, _, err := execute(t, "credentials", "serve")
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error (exit 1)", err)
			}
			if !strings.Contains(err.Error(), k) {
				t.Fatalf("err = %v, want a refusal naming %s", err, k)
			}
		})
	}
}

// credentialsServeEnv is a complete, valid service environment: the values the
// installation module would set, with the Fugaro project, GCP project and
// Firebase project the golden fixtures use (internal/infra/testdata's
// installation-budget-job.tfvars.json) so the derived names here are the names
// a real installation derives.
var credentialsServeEnv = map[string]string{
	envProject:               "aurora",
	envGCPProject:            "proj-1234",
	envRegion:                "us-east5",
	envRTDBURL:               "https://aurora-fp-default-rtdb.firebaseio.com",
	envFirebaseFP:            "aurora-fp",
	envCredentialsServiceURL: "https://fugaro-credentials-abcde-uc.a.run.app",
	envJobAccounts:           `[{"provider":"github","repo":"acme/webapp","workflow":"api"}]`,
	envVerifierAccounts:      `["person-1@example.com"]`,
	// Two review-only rows over the profile a real installation publishes, one
	// per spelling of the account axis: the empty string is the default account
	// (config.ProjectJobAccounts) and "team-a" is a named one, so a document
	// the two readings disagree about fails here rather than at a caller. They
	// are the triples CSR7 deploys the variant's jobs over, which is why they
	// carry no repository: a review job's account names none (C3.9a).
	envReviewJobAccounts: `[{"profile":"standard","provider":"github","account":""},{"profile":"standard","provider":"github","account":"team-a"}]`,
}

// TestCredentialsServeStartsWithoutTheVerifierList is what the eighth name's
// being OPTIONAL rests on (C3.9g2, D1068). credentials.tf renders
// FUGARO_VERIFIER_ACCOUNTS only on an installation that named a verifier, so
// an installation that never did must start the same service it always did:
// the seven names, an empty verifier list, and a verify route that refuses
// every caller -- rather than a process that will not bind a port, which is
// what an eighth name inside credentialsServiceEnvNames would have bought.
//
// Both halves of that are here, and both are the CLI's to hold: the
// configuration builds with the name absent, and the route is reached on the
// host this deployment is served by, where a caller the service cannot verify
// gets the route's own typed refusal. Which of the class's refusals a caller
// with a VERIFIED but unlisted identity gets is internal/credsvc's own test,
// because producing such a caller takes Google's key set.
func TestCredentialsServeStartsWithoutTheVerifierList(t *testing.T) {
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	fakeCredentialsService(t)

	cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("credentialsServiceConfig with no %s: %v (the module renders that entry only on an installation that names a verifier, so an installation that never did must start the same service it always did)", envVerifierAccounts, err)
	}
	srv, err := credsvc.NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer with an empty verifier list: %v", err)
	}
	addr, _ := serveForTest(t, srv)
	resp, err := http.Post(addr+credsvc.VerifyPath, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("the verify route did not answer: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the verify refusal: %v", err)
	}
	if resp.StatusCode != credsvc.StatusUnverified || !strings.Contains(string(body), credsvc.ErrorUnverifiedJobIdentity) {
		t.Fatalf("verify answered %d %s, want %d and the route's typed refusal (%s)", resp.StatusCode, body, credsvc.StatusUnverified, credsvc.ErrorUnverifiedJobIdentity)
	}
}

// TestCredentialsServiceVerifierListReachesTheAuthenticator is the one test
// that can tell the list the environment carries from no list at all, which a
// surviving mutant showed was not covered: the configuration can build, and the
// service can start, with the verifier list quietly dropped on the way to
// AuthConfig -- the empty list and the installation that named nobody look
// identical from the outside, and only the route's answer would differ
// (C3.9g2, D1068).
//
// The entry below is one credsvc's own fold refuses: a verifier account is a
// printable-ASCII email address, because it is matched against a Google ID
// token's email claim. So the refusal is reachable ONLY if the entry arrived at
// NewAuthenticator -- a service built with the list dropped starts, and one
// built with the list refuses -- which is what makes this a test of the wiring
// and not of the refusal.
//
// The second half is the same no-echo rule the other refusals are held to: this
// process's log is a line an operator reads, so the refusal names no account.
//
// Mutation (verified, run and restored): VerifierAccounts: verifiers[:0] in
// credentialsServiceConfig -- this test fails, the service starting with no
// verifier at all instead of refusing the entry it cannot fold.
func TestCredentialsServiceVerifierListReachesTheAuthenticator(t *testing.T) {
	const entry = "pérson-1@example.com"
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	t.Setenv(envVerifierAccounts, `["`+entry+`"]`)
	fakeCredentialsService(t)

	_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: an entry the verifier cannot fold must refuse the process rather than be dropped on the way to it", err)
	}
	if !strings.Contains(err.Error(), "cannot verify its callers") {
		t.Fatalf("err = %v, want the refusal that says the service cannot verify its callers, which is reachable only if %s reached the authenticator", err, envVerifierAccounts)
	}
	if strings.Contains(err.Error(), entry) {
		t.Errorf("err = %v, which carries the verifier entry itself: the refusal names the variable and never an account", err)
	}
}

// TestCredentialsServiceCoordinatorAccountReachesTheAuthenticator is the same
// test for the read mint's second class (BB5), and it exists for the same
// reason the verifier list's does: an empty account and a value quietly
// dropped on the way to AuthConfig look identical from the outside, and only
// the read mint's answer would differ -- which takes Google's key set to
// reach.
//
// The entry is again one credsvc's own fold refuses (an account is matched
// against a Google ID token's email claim), so the refusal is reachable ONLY
// if the value arrived at NewAuthenticator, and the refusal names the variable
// and never the account.
//
// The second half is the optional half of the same wiring (F24): with the
// name absent -- the state every installation that runs no coordinator is in
// -- the configuration builds and the service starts.
func TestCredentialsServiceCoordinatorAccountReachesTheAuthenticator(t *testing.T) {
	const entry = "cóordinator@example.com"
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	t.Setenv(envCoordinatorAccount, entry)
	fakeCredentialsService(t)

	_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: an account the coordinator class cannot fold must refuse the process rather than be dropped on the way to it", err)
	}
	if !strings.Contains(err.Error(), "cannot verify its callers") {
		t.Fatalf("err = %v, want the refusal that says the service cannot verify its callers, which is reachable only if %s reached the authenticator", err, envCoordinatorAccount)
	}
	if strings.Contains(err.Error(), entry) {
		t.Errorf("err = %v, which carries the coordinator account itself: the refusal names the variable and never an account", err)
	}

	t.Setenv(envCoordinatorAccount, "")
	cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("credentialsServiceConfig with no %s: %v (the module renders that entry only on an installation whose coordinator is on, so an installation that runs none must start the same service it always did)", envCoordinatorAccount, err)
	}
	if _, err := credsvc.NewServer(cfg); err != nil {
		t.Fatalf("NewServer with no coordinator account: %v", err)
	}
}

// TestCredentialsServiceReviewJobAccountsReachTheAuthenticator is the same
// wiring test for the review-only class (C3.9a), and it takes the overlap rule
// to make it, because the class's own entries cannot be the ones the tests
// above use: a review job's service account is DERIVED from its row
// (credsvc.ReviewJobAccountFor over the triple), so every row this command can
// build is a foldable address, and an unfoldable one -- the trick the verifier
// and the coordinator entries are read with -- is not reachable through the
// environment at all.
//
// What is reachable is the rule credsvc.NewAuthenticator holds the class to: a
// person account may not also be a review job service account, because one
// caller would then be two classes. The address below is the SECOND row of
// `credentialsServeEnv`'s review document, derived, and the refusal it draws
// exists ONLY if the rows arrived at the authenticator -- a service built with
// ReviewJobAccounts dropped admits the person and starts, and one built with
// the rows refuses. The refusal names no account, which is the process's own
// no-echo rule.
//
// The second half is the optional and the empty half of the same wiring: with
// the document absent -- the state of an installation whose module does not
// render the entry yet -- and with an empty array, the configuration builds and
// the service starts with no review job at all. That is what makes the absent
// value a service that refuses the class rather than a process that will not
// bind a port.
//
// Mutation (verified, run and restored): dropping ReviewJobAccounts from the
// AuthConfig literal in credentialsServiceConfig -- this test fails on the
// first half, the configuration building with the person admitted and no
// review class configured. Mutation (verified, run and restored): dropping
// ReviewAccounts from that function's returned credsvc.Config -- the headline
// test above fails, which is that direction's own test, since a server with an
// empty review table refuses every caller of the class with no way to say so
// from the outside.
func TestCredentialsServiceReviewJobAccountsReachTheAuthenticator(t *testing.T) {
	// The address credsvc.ReviewJobAccountFor derives for
	// {"profile":"standard","provider":"github","account":"team-a"} -- the row
	// the headline test pins by value, so this is the deployment's own account
	// and not a second spelling of it.
	const reviewAccount = "fugaro-p-standard-git-b5f51775@proj-1234.iam.gserviceaccount.com"
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	t.Setenv(envReviewJobAccounts, credentialsServeEnv[envReviewJobAccounts])
	t.Setenv(envPersonAccounts, `["`+reviewAccount+`"]`)
	fakeCredentialsService(t)

	_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: one address that is both a person and a review job's service account must refuse the process rather than be dropped on the way to it", err)
	}
	if !strings.Contains(err.Error(), "cannot verify its callers") || !strings.Contains(err.Error(), "review job service account") {
		t.Fatalf("err = %v, want the refusal that says the service cannot verify its callers because the address is also a review job service account, which is reachable only if %s reached the authenticator", err, envReviewJobAccounts)
	}
	if strings.Contains(err.Error(), reviewAccount) {
		t.Errorf("err = %v, which carries the derived review account itself: the refusal names the variable and never an account", err)
	}

	t.Setenv(envPersonAccounts, "")
	for _, raw := range []string{"", "[]"} {
		t.Setenv(envReviewJobAccounts, raw)
		cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
		if err != nil {
			t.Fatalf("credentialsServiceConfig with %s = %q: %v (the entry arrives with the deployment of the variant's jobs, so an installation that has none must start the same service it always did)", envReviewJobAccounts, raw, err)
		}
		if len(cfg.ReviewAccounts) != 0 {
			t.Errorf("review accounts with %s = %q: %+v, want none", envReviewJobAccounts, raw, cfg.ReviewAccounts)
		}
		if _, err := credsvc.NewServer(cfg); err != nil {
			t.Fatalf("NewServer with %s = %q: %v", envReviewJobAccounts, raw, err)
		}
	}
}

// TestCredentialsServiceSharedJobAccountsReachTheService is the same wiring
// test for the shared coding class (B5.1), and it is the class's own overlap
// rule that makes it reachable: a person account may not also be a shared job's
// service account, because one caller would then be two classes (auth.go). The
// address below is derived from the document this test sets, by the function
// the command derives it with — so the refusal exists ONLY if the rows arrived
// at the authenticator, which a service built with SharedJobAccounts dropped
// would not draw, and it names no account, which is the process's own no-echo
// rule.
//
// The row is also the one the class's binding needs: a shared caller names no
// repository, so its row carries the profile and the repositories the profile
// serves, and the second half of the test pins that the same rows reach the
// server's own table (Config.SharedAccounts) with them — the two sides a
// deployment derives from one axis (credsvc.SharedJobAccountsFor), which is
// what makes a caller's class and the repositories it is served for one
// statement rather than two.
//
// The third half is the optional and the empty half of the wiring: with the
// document absent — an installation carried from a release whose module did
// not render the entry yet — and with an empty array, the configuration builds
// and the service starts with no shared job at all. That is what makes the
// absent value a class refused whole rather than a
// process that will not bind a port.
//
// Mutation (verified, run and restored): dropping SharedJobAccounts from the
// AuthConfig literal in credentialsServiceConfig -- this test fails on the
// first half, the configuration building with the person admitted and no shared
// class configured. Mutation (verified, run and restored): dropping
// SharedAccounts from that function's returned credsvc.Config -- the second
// half fails, and with it the class's binding: a caller the authenticator
// answers as shared would be paired against a table that holds no row of it.
func TestCredentialsServiceSharedJobAccountsReachTheService(t *testing.T) {
	// One row over the profile a real installation publishes, with the empty
	// string for the account (the axis's spelling of the default account) and
	// the repository the fixture's job serves.
	const document = `[{"profile":"standard","provider":"github","account":"","repos":["github/acme/webapp"]}]`
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	row := credsvc.SharedJobAccountFor("standard", "github", "", []string{"github/acme/webapp"}, credentialsServeEnv[envGCPProject])
	t.Setenv(envSharedJobAccounts, document)
	t.Setenv(envPersonAccounts, `["`+row.Email+`"]`)
	fakeCredentialsService(t)

	_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: one address that is both a person and a shared job's service account must refuse the process rather than be dropped on the way to it", err)
	}
	if !strings.Contains(err.Error(), "cannot verify its callers") || !strings.Contains(err.Error(), "shared job service account") {
		t.Fatalf("err = %v, want the refusal that says the service cannot verify its callers because the address is also a shared job service account, which is reachable only if %s reached the authenticator", err, envSharedJobAccounts)
	}
	if strings.Contains(err.Error(), row.Email) {
		t.Errorf("err = %v, which carries the derived shared account itself: the refusal names the variable and never an account", err)
	}

	t.Setenv(envPersonAccounts, "")
	cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("credentialsServiceConfig with %s: %v", envSharedJobAccounts, err)
	}
	if len(cfg.SharedAccounts) != 1 {
		t.Fatalf("the server's shared table is %+v, want the document's one row: the class the authenticator answers is paired against the table this side builds", cfg.SharedAccounts)
	}
	if got := cfg.SharedAccounts[0]; got.Email != row.Email || got.Profile != "standard" || !slices.Equal(got.Repos, []string{"github/acme/webapp"}) {
		t.Errorf("the server's shared row is %+v, want the derived account, its profile and the repositories the profile is bound to", got)
	}
	if _, err := credsvc.NewServer(cfg); err != nil {
		t.Fatalf("NewServer with %s: %v", envSharedJobAccounts, err)
	}

	for _, raw := range []string{"", "[]"} {
		t.Setenv(envSharedJobAccounts, raw)
		cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
		if err != nil {
			t.Fatalf("credentialsServiceConfig with %s = %q: %v (the entry arrives with the deployment of the shared jobs, so an installation whose module does not render it must start the same service it always did)", envSharedJobAccounts, raw, err)
		}
		if len(cfg.SharedAccounts) != 0 {
			t.Errorf("shared accounts with %s = %q: %+v, want none", envSharedJobAccounts, raw, cfg.SharedAccounts)
		}
		if _, err := credsvc.NewServer(cfg); err != nil {
			t.Fatalf("NewServer with %s = %q: %v", envSharedJobAccounts, raw, err)
		}
	}
}

// The value of FUGARO_SHARED_JOB_ACCOUNTS is written by ONE half of this
// program and read by the other, through a file, a Terraform variable and a
// Cloud Run container — internal/infra renders the installation's shared jobs
// into the tfvars (CredentialsSharedJobAccounts) and the module carries them
// into this service's environment, and this command parses them back at
// start-up (credentialsServiceSharedJobAccounts). Nothing in between is typed
// by hand, so the two halves of the document have no compiler between them:
// the writer's json tags and the reader's are string-matched, the reader
// refuses an unknown field and a row without repos, and a member either side
// renames is a service that refuses to bind a port — or, worse, one that
// starts with a table that names no account the deployment created.
//
// The round trip is therefore asserted end to end: a real local config is
// derived into rows, the rows are marshalled exactly as they are written (the
// struct, not a hand-built document), and the command's own reader is handed
// the result. Every row has to come back, carrying the same repositories — a
// pattern among them, because allow_repos is matched as a pattern and a list
// this side rewrote or dropped would be a shared caller refused for a
// repository its profile admits — and each account has to be the service
// account the deployer creates for that triple (gcp.ProjectServiceAccountID),
// which is the whole of what the caller's class rests on.
func TestCredentialsSharedJobAccountsRoundTrip(t *testing.T) {
	// Two profiles and both providers: the GitHub one names an allow_repos list
	// with a named account beside its default, the Bitbucket one names none, so
	// its row takes the repositories this machine's config records for
	// Bitbucket — the two derivations the writer makes, and the two the reader
	// has to receive.
	const local = `version: 1
name: aurora
gcp_project: proj-1234
region: us-east5
runs_bucket: fugaro-runs-proj-1234
registry: us-east5-docker.pkg.dev/proj-1234/fugaro
user: test@example.com
repos:
  acme/webapp: { provider: github, base_branch: main, workflows: [web], github_app_id: "123456789" }
  acme/sandbox: { provider: bitbucket, base_branch: master, workflows: [web] }
claude_accounts:
  acme-personal: { allow_data_to: [acme/webapp] }
project_secrets:
  claude-oauth-token: { allow_data_to: [acme/webapp] }
profiles:
  standard: { provider: github, cpu: 1, memory: 2Gi, timeout_class: 1h, allow_repos: [github/acme/webapp, github/acme/*], claude_accounts: [acme-personal, default] }
  bbshared: { provider: bitbucket, cpu: 2, memory: 4Gi, timeout_class: 6h }
`
	lc, err := localcfg.Parse([]byte(local))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := infra.CredentialsSharedJobAccounts(lc)
	if err != nil {
		t.Fatal(err)
	}
	wantRows := []infra.CredentialsSharedJob{
		{Profile: "bbshared", Provider: "bitbucket", Account: "", Repos: []string{"bitbucket/acme/sandbox"}},
		{Profile: "standard", Provider: "github", Account: "", Repos: []string{"github/acme/webapp", "github/acme/*"}},
		{Profile: "standard", Provider: "github", Account: "acme-personal", Repos: []string{"github/acme/webapp", "github/acme/*"}},
	}
	if !slices.EqualFunc(rows, wantRows, func(a, b infra.CredentialsSharedJob) bool {
		return a.Profile == b.Profile && a.Provider == b.Provider && a.Account == b.Account && slices.Equal(a.Repos, b.Repos)
	}) {
		t.Fatalf("the installation's shared jobs = %+v, want %+v", rows, wantRows)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := credentialsServiceSharedJobAccounts(string(raw), credentialsServeEnv[envGCPProject])
	if err != nil {
		t.Fatalf("the service refused the value this program renders for its own installation (%s): %v", raw, err)
	}
	if len(accounts) != len(wantRows) {
		t.Fatalf("the service read %d rows out of %d: %+v", len(accounts), len(wantRows), accounts)
	}
	gcpProject := credentialsServeEnv[envGCPProject]
	slices.SortFunc(accounts, func(a, b credsvc.SharedJobAccount) int { return strings.Compare(a.Email, b.Email) })
	for _, r := range wantRows {
		// The account the deployer creates for this triple, spelled here the
		// way the reader derives it (credsvc.SharedJobAccountFor is the one
		// derivation both sides share) and checked against the email the row
		// really carries: a table naming an account no job runs as is a class
		// no caller can be verified into.
		want := gcp.ProjectServiceAccountID(r.Profile, r.Provider, r.Account) + "@" + gcpProject + ".iam.gserviceaccount.com"
		i := slices.IndexFunc(accounts, func(a credsvc.SharedJobAccount) bool { return a.Email == want })
		if i < 0 {
			t.Fatalf("the service read %+v and none of them is %s, the account the deployer creates for (%s, %s, %q)", accounts, want, r.Profile, r.Provider, r.Account)
		}
		if got := accounts[i]; got.Profile != r.Profile || !slices.Equal(got.Repos, r.Repos) {
			t.Errorf("row %s came back as %+v, want the profile %s and the repositories %v it was written with: the writer's list IS the admission the service binds a shared caller to", got.Email, got, r.Profile, r.Repos)
		}
	}
}

// TestCredentialsServiceRepoBuildAccountsReachTheAuthenticator is the same
// wiring test for the repository build class (B4.3, R5, D2076, G20): the rows
// the build-credentials route admits are derived from FUGARO_JOB_ACCOUNTS —
// the same document the job bindings come from, read a third way — and a class
// quietly dropped on the way to AuthConfig would look exactly like an
// installation that runs no repository's build. Only the route's answer would
// differ, and reaching it takes a Google ID token.
//
// The class's own overlap rule is what makes the wiring reachable: one address
// may not be both a repository's build service account and one of the
// installation's verifiers, because one caller would then be two classes
// (auth.go). The address below is DERIVED from the document this file's
// environment holds, by the function the command derives it with, so the
// refusal exists ONLY if the rows arrived at the authenticator — a service
// built with RepoBuildAccounts dropped starts, and one built with the rows
// refuses. The refusal names the variable and never an account, which is this
// process's own no-echo rule.
//
// The second half is the empty half of the same wiring: an empty job document
// is refused by name (a table that binds nothing admits nobody), and the
// derivation is over the SAME per-provider list the job accounts come from, so
// there is no state in which a repository has a build class and no jobs.
//
// Mutation (verified, run and restored): dropping RepoBuildAccounts from the
// AuthConfig literal in credentialsServiceConfig — this test fails on the
// first half, the configuration building with the verifier admitted and no
// build class configured.
func TestCredentialsServiceRepoBuildAccountsReachTheAuthenticator(t *testing.T) {
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	// The one repository this file's environment names, read the way the
	// command reads it: the derived row is the deployment's own account and not
	// a second spelling of it.
	rows, err := credsvc.RepoBuildAccountsFor(credentialsServeEnv[envGCPProject], "github",
		[]credsvc.RepoJob{{Repo: "acme/webapp", Workflow: "api"}})
	if err != nil {
		t.Fatalf("RepoBuildAccountsFor over the document this file's environment holds: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("the derived rows = %+v, want one for the one repository the document names", rows)
	}
	buildAccount := rows[0].Email
	t.Setenv(envVerifierAccounts, `["`+buildAccount+`"]`)
	fakeCredentialsService(t)

	_, err = credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: one address that is both a verifier and a repository's build service account must refuse the process rather than be dropped on the way to it", err)
	}
	if !strings.Contains(err.Error(), "cannot verify its callers") || !strings.Contains(err.Error(), "build service account") {
		t.Fatalf("err = %v, want the refusal that says the service cannot verify its callers because the address is also a repository's build service account, which is reachable only if the build rows reached the authenticator", err)
	}
	if strings.Contains(err.Error(), buildAccount) {
		t.Errorf("err = %v, which carries the derived build account itself: the refusal names the variable and never an account", err)
	}

	t.Setenv(envVerifierAccounts, "")
	t.Setenv(envJobAccounts, "[]")
	if _, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard); err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: a job document that names no job binds no run to a repository and is refused by name", err)
	}
}

// TestCredentialsServiceReviewJobAccountsAreOneTableWhateverTheDocumentsOrder:
// the document is a deployment's own text, so its order is not a fact about the
// installation and the same rows written twice are one account -- the table
// this command builds is therefore the policy read's table, one row per account
// in the table's own order (credsvc.ReviewJobAccountsFor, which is why the two
// derivations share one function). Rows that name one account under two Claude
// accounts are the one thing left alone here: that is a deployment disagreeing
// with itself, and credsvc.NewAuthenticator is where it is refused.
//
// Mutation (verified, run and restored): dropping the de-duplication, or the
// ordering, from credentialsServiceReviewJobAccounts -- FAIL.
func TestCredentialsServiceReviewJobAccountsAreOneTableWhateverTheDocumentsOrder(t *testing.T) {
	got, err := credentialsServiceReviewJobAccounts(`[
		{"profile":"standard","provider":"github","account":"team-a"},
		{"profile":"standard","provider":"github","account":""},
		{"profile":"standard","provider":"github","account":"team-a"}]`, "proj-1234")
	if err != nil {
		t.Fatalf("credentialsServiceReviewJobAccounts: %v", err)
	}
	// The two rows the headline test pins by value, written in the other order
	// and with one of them twice.
	want := []credsvc.ReviewJobAccount{
		{Email: "fugaro-p-standard-git-04644ca9@proj-1234.iam.gserviceaccount.com", Account: config.DefaultClaudeAccount},
		{Email: "fugaro-p-standard-git-b5f51775@proj-1234.iam.gserviceaccount.com", Account: "team-a"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("credentialsServiceReviewJobAccounts = %+v, want the table the policy read makes, %+v", got, want)
	}
}

// TestLauncherAccountsReadFromEnv is the wiring of the launcher class's own
// list (DS1.2, D1938, F36): FUGARO_LAUNCHER_ACCOUNTS is the fifth name this
// command reads beside the seven credentials.tf always sets, and it is read
// exactly the way FUGARO_PERSON_ACCOUNTS is -- as an OPTIONAL value, because
// the module renders the entry only on an installation whose roster names a
// launcher, so one that names none must start with a class that admits nobody
// rather than a process that will not bind a port.
//
// The entry the first half uses is one credsvc's own fold refuses (a launcher
// is matched against a Google ID token's email claim), so the refusal is
// reachable ONLY if the entry arrived at NewAuthenticator -- which is the whole
// point of the test: an empty list and a list quietly dropped on the way to
// AuthConfig look identical from the outside, and only the grant route's
// answer would differ. Mutation (verified, run and restored): dropping
// LauncherAccounts from the AuthConfig literal in credentialsServiceConfig --
// this test fails, the process starting with the list dropped.
//
// The second half is the shape rule and this process's own no-echo rule: a
// value that is present and is not the document the module renders is refused
// by the variable's name and never by an entry.
func TestLauncherAccountsReadFromEnv(t *testing.T) {
	const entry = "launchér-1@example.invalid"
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	t.Setenv(envLauncherAccounts, `["`+entry+`"]`)
	fakeCredentialsService(t)

	_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: an entry the launcher class cannot fold must refuse the process rather than be dropped on the way to it", err)
	}
	if !strings.Contains(err.Error(), "cannot verify its callers") || !strings.Contains(err.Error(), "launcher") {
		t.Fatalf("err = %v, want the refusal that says the service cannot verify its callers because a launcher entry could not be folded, which is reachable only if %s reached the authenticator", err, envLauncherAccounts)
	}
	if strings.Contains(err.Error(), entry) {
		t.Errorf("err = %v, which carries the launcher entry itself: the refusal names the variable and never an account", err)
	}

	// A value that is not the array the module renders is a deployment that is
	// wrong, refused by name.
	for _, raw := range []string{`"` + entry + `"`, `[{"email":"` + entry + `"}]`, `["ok@example.invalid",3]`, "not json"} {
		got, err := credentialsServiceLaunchers(raw)
		if err == nil {
			t.Errorf("credentialsServiceLaunchers(%q) = %v, want a refusal: the value is the document the module renders or nothing", raw, got)
			continue
		}
		if !strings.Contains(err.Error(), envLauncherAccounts) {
			t.Errorf("the refusal for %q is %q, want it to name %s", raw, err, envLauncherAccounts)
		}
		if strings.Contains(err.Error(), entry) {
			t.Errorf("the refusal for %q carries an entry: %q", raw, err)
		}
	}

	// The optional half: absent and empty are the empty list, and the service
	// starts with a launcher class that admits nobody.
	for _, raw := range []string{"", "[]"} {
		t.Setenv(envLauncherAccounts, raw)
		cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
		if err != nil {
			t.Fatalf("credentialsServiceConfig with %s = %q: %v (the entry is rendered only on an installation whose roster names a launcher, so one that names none must start the same service it always did)", envLauncherAccounts, raw, err)
		}
		if _, err := credsvc.NewServer(cfg); err != nil {
			t.Fatalf("NewServer with %s = %q: %v", envLauncherAccounts, raw, err)
		}
	}
}

// TestBuildAccountsReadFromEnv is the wiring of the build class's own list (B5,
// D2301): FUGARO_BUILD_ACCOUNTS is the name credentials.tf renders from the
// account the installation module creates beside the service, so the identity a
// catalog build runs as is the identity the build route admits -- one value,
// rendered by the module and read here, and never a second spelling in Go.
//
// The entry the first half uses is one credsvc's own fold refuses (a build
// identity is matched against a Google ID token's email claim, as every class's
// address is), so the refusal is reachable ONLY if the entry arrived at
// NewAuthenticator -- the launcher list's own proof, and the one this class
// needs most: a list parsed and then dropped on the way to AuthConfig would
// leave the class empty, the route refusing every build, and nothing about the
// parser able to tell.
//
// The second half is the optional-value rule this class shares with the
// verifier list: the module renders the entry whenever it deploys the service,
// so a service deployed by a release before this one renders none at all and
// must start the service it always did, with a build class that admits nobody,
// rather than refuse to bind a port.
func TestBuildAccountsReadFromEnv(t *testing.T) {
	const entry = "buiłd@example.invalid"
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	t.Setenv(envBuildAccounts, `["`+entry+`"]`)
	fakeCredentialsService(t)

	_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: an entry the build class cannot fold must refuse the process rather than be dropped on the way to it", err)
	}
	if !strings.Contains(err.Error(), "cannot verify its callers") || !strings.Contains(err.Error(), "build") {
		t.Fatalf("err = %v, want the refusal that says the service cannot verify its callers because a build entry could not be folded, which is reachable only if %s reached the authenticator", err, envBuildAccounts)
	}
	if strings.Contains(err.Error(), entry) {
		t.Errorf("err = %v, which carries the build entry itself: the refusal names the variable and never an account", err)
	}

	// A value that is not the array the module renders is a deployment that is
	// wrong, refused by name.
	for _, raw := range []string{`"` + entry + `"`, `[{"email":"` + entry + `"}]`, `["ok@example.invalid",3]`, "not json"} {
		got, err := credentialsServiceBuildAccounts(raw)
		if err == nil {
			t.Errorf("credentialsServiceBuildAccounts(%q) = %v, want a refusal: the value is the document the module renders or nothing", raw, got)
			continue
		}
		if !strings.Contains(err.Error(), envBuildAccounts) {
			t.Errorf("the refusal for %q is %q, want it to name %s", raw, err, envBuildAccounts)
		}
		if strings.Contains(err.Error(), entry) {
			t.Errorf("the refusal for %q carries an entry: %q", raw, err)
		}
	}

	// The optional half: absent and empty are the empty class, and the service
	// starts with a build route that admits nobody.
	for _, raw := range []string{"", "[]"} {
		t.Setenv(envBuildAccounts, raw)
		cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
		if err != nil {
			t.Fatalf("credentialsServiceConfig with %s = %q: %v (the entry arrives with the deployment of the build identity the module creates, so a release that renders none must start the same service it always did)", envBuildAccounts, raw, err)
		}
		if _, err := credsvc.NewServer(cfg); err != nil {
			t.Fatalf("NewServer with %s = %q: %v", envBuildAccounts, raw, err)
		}
	}

	// The build class is held against every other one (B1): the identity the
	// class admits is the project's build account and never one of the
	// installation's own callers, because a job or an operator that could also
	// be a build would hold a build's reach over a project's catalog entries.
	// The refusal below is reachable only if the parsed list arrived at the
	// authenticator's cross-check, which is the second half of the proof above.
	t.Setenv(envVerifierAccounts, credentialsServeEnv[envVerifierAccounts])
	t.Setenv(envBuildAccounts, credentialsServeEnv[envVerifierAccounts])
	if _, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard); err == nil {
		t.Errorf("%s carrying the installation's own verifier is accepted: the build class is the one list held against every other, and one caller in two classes is a class decided by which route read the token", envBuildAccounts)
	} else if !strings.Contains(err.Error(), "build") || !strings.Contains(err.Error(), "verifier") {
		t.Errorf("err = %v, want the refusal that names the two classes an address on both lists would be", err)
	}

	// ... and the module's own value is a value this process starts on: an
	// installation that renders the entry gets a service that serves the build
	// route, and the empty class above is for the installations that render
	// none. The address is derived from the account ID the module creates the
	// account under, so it is not a second spelling of anything.
	t.Setenv(envBuildAccounts, `["`+infra.BuildIdentityEmail(credentialsServeEnv[envGCPProject])+`"]`)
	cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("credentialsServiceConfig with %s = the installation's build identity: %v", envBuildAccounts, err)
	}
	if _, err := credsvc.NewServer(cfg); err != nil {
		t.Fatalf("NewServer with the installation's build identity as its build class: %v", err)
	}
}

// TestCredentialsServiceOrchestratorClassReachesTheAuthenticator is the
// daemon's class at this end (F19, O2; E16, D2049): the value credentials.tf
// renders for an installation that runs a resident orchestrator — a JSON array
// holding the one address orchestrator.tf creates — is a value this process
// STARTS on, and the class it builds is the daemon's alone.
//
// The three halves are the build list's, one class over. The module's own
// value builds a configuration the server accepts, which is what makes the
// service an update deploys a service that serves the label route and the
// merge mint rather than a container that will not bind a port. An address the
// class may not hold — one of the installation's own job accounts, and one of
// its operators — is refused where the configuration is read, and those two
// refusals exist ONLY if the value arrived at credsvc.NewAuthenticator: a
// service built with OrchestratorAccounts dropped starts and admits nobody,
// and a job admitted here is the credential C2.2 and F28 keep out of the
// merge mint. And absent or empty is the empty class, which is the state every
// service deployed by a release before this one is in: it starts, and both
// routes answer every caller with the class's own refusal rather than refuse
// to serve (credsvc/mergemint_test.go's TestPreviousInstallationConverges is
// the route-level half of that).
//
// Mutation (verified, run and restored): drop OrchestratorAccounts from the
// AuthConfig literal in credentialsServiceConfig, and both refusals below are
// gone — the configuration builds, with the daemon class empty and a job's or
// a person's address admitted to it.
func TestCredentialsServiceOrchestratorClassReachesTheAuthenticator(t *testing.T) {
	// The installation's own operator, and the job account its job table
	// derives — the address TestCredentialsServiceConfigBuildsFromAFullEnvironment
	// pins by value. Neither may be the daemon: the class is an automation of
	// its own.
	const (
		daemonPerson   = "operator-1@example.com"
		daemonJobEmail = "fugaro-acme-webapp-ap-7daad322@proj-1234.iam.gserviceaccount.com"
	)
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	t.Setenv(envPersonAccounts, `["`+daemonPerson+`"]`)
	fakeCredentialsService(t)

	// The module's own value, read through the configuration the way the
	// service reads it: an installation that opted in gets a service that
	// serves both of the daemon's routes.
	daemonEmail := infra.OrchestratorAccountID + "@" + credentialsServeEnv[envGCPProject] + ".iam.gserviceaccount.com"
	t.Setenv(envOrchestratorAccounts, `["`+daemonEmail+`"]`)
	cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("credentialsServiceConfig with the installation's own daemon account: %v (credentials.tf renders this entry on every installation that runs a resident orchestrator, and a value this process cannot read is a daemon whose service never starts)", err)
	}
	if _, err := credsvc.NewServer(cfg); err != nil {
		t.Fatalf("NewServer with the installation's own daemon account: %v", err)
	}

	// A run's own account, which is what a list dropped on the way to the
	// authenticator would admit in the daemon's place ...
	t.Setenv(envOrchestratorAccounts, `["`+daemonJobEmail+`"]`)
	_, err = credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: an installation's job account on the daemon class is what C2.2 and F28 keep out of the merge mint", err)
	}
	for _, want := range []string{"cannot verify its callers", "orchestrator", "job service accounts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want the refusal that says a run's account may not be the daemon", err)
			break
		}
	}
	if strings.Contains(err.Error(), daemonJobEmail) {
		t.Errorf("err = %v, which carries the account itself: the refusal names the classes and never an address", err)
	}

	// ... and one of the installation's operators, for the person list's own
	// reason: the daemon is an automation and its class is not a person's, which
	// is what keeps a person's ambient credentials out of both routes whatever
	// the audience check does.
	t.Setenv(envOrchestratorAccounts, `["`+daemonPerson+`"]`)
	_, err = credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error: an operator on the daemon class is a person admitted to the merge mint", err)
	}
	for _, want := range []string{"cannot verify its callers", "orchestrator", "people"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want the refusal that says a person may not be the daemon", err)
			break
		}
	}

	// The optional half: absent and empty are the empty class, which is the
	// state of every service deployed before this release and of every
	// installation that runs no daemon. Both start, and both routes answer
	// every caller with the class's refusal.
	for _, raw := range []string{"", "[]"} {
		t.Setenv(envOrchestratorAccounts, raw)
		cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
		if err != nil {
			t.Fatalf("credentialsServiceConfig with %s = %q: %v (the entry arrives with the account the module creates, so a service deployed by a release that renders none must start the same service it always did)", envOrchestratorAccounts, raw, err)
		}
		if _, err := credsvc.NewServer(cfg); err != nil {
			t.Fatalf("NewServer with %s = %q: %v", envOrchestratorAccounts, raw, err)
		}
	}
}

// TestCredentialsServeReadsTheIdentityEnv is LM22's reading end (D3121, D3123,
// F29/R16): the three names of the identity half are read out of the
// deployment's environment into C1's configuration.
//
// The three are the values the issuer is configured with — the account a
// run's identity is signed as, the roster's `serviceAccount:` entries and the
// implicit admins the CLI's own gate reads out of its configuration — and each
// is read here as an OPTIONAL value, because the module that renders them is
// LM12: an installation whose module does not render one yet must start the
// service it always did, on the bridge the identity routes answer "not served"
// from (R7), rather than refuse to bind a port.
//
// What is pinned here is the read and not what the routes do with it: the
// values arrive in the fields credsvc.IdentityConfig names for them, and a
// deployment that is wrong is refused by name and never by value. Which route
// reads which field is credsvc's own (LM3, LM4) and is asserted there.
//
// Mutation (verified, run and restored): change credentialsServiceSigner's last
// line to return the empty string — the first half below fails with an empty
// Signer, which no other assertion here could tell from an installation whose
// module renders no entry.
func TestCredentialsServeReadsTheIdentityEnv(t *testing.T) {
	const (
		signer = "fugaro-token-signer@proj-1234.iam.gserviceaccount.com"
		ci     = "ci-deploy@proj-1234.iam.gserviceaccount.com"
		admin  = "owner@example.com"
	)
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	fakeCredentialsService(t)

	// Half one: the three values reach C1's configuration, each in its own
	// field and unchanged. The roster's list and the admins' are pinned as
	// lists and not as "non-empty", because the two are different questions —
	// one address where the module renders a list is a document this reader
	// must not repair, and a list that arrived as one string would serve a
	// caller class nobody deployed.
	t.Setenv(envSignerAccount, signer)
	t.Setenv(envRosterServiceAccounts, `["`+ci+`"]`)
	t.Setenv(envImplicitAdmins, `["`+admin+`"]`)
	cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("credentialsServiceConfig with the identity half set: %v", err)
	}
	if cfg.Identity.Signer != signer {
		t.Errorf("the configuration's signer is %q, want the address %s carries: a service that dropped it would serve an installation with no issuer", cfg.Identity.Signer, envSignerAccount)
	}
	if !slices.Equal(cfg.Identity.ServiceAccounts, []string{ci}) {
		t.Errorf("the configuration's service accounts are %q, want %q: %s is the roster's one serviceAccount: entry", cfg.Identity.ServiceAccounts, []string{ci}, envRosterServiceAccounts)
	}
	if !slices.Equal(cfg.Identity.ImplicitAdmins, []string{admin}) {
		t.Errorf("the configuration's implicit admins are %q, want %q: %s is what the machine's own configuration names", cfg.Identity.ImplicitAdmins, []string{admin}, envImplicitAdmins)
	}
	if _, err := credsvc.NewServer(cfg); err != nil {
		t.Fatalf("NewServer with the identity half configured: %v (an installation whose module renders the three must be a service that starts)", err)
	}

	// Half two: absent and empty are the zero value, and the service starts —
	// the state every installation is in until LM12 renders the entries. The
	// three are read outside credentialsServiceEnvNames for exactly this
	// reason, which is the verifier list's rule (TestCredentialsServeStartsWithoutTheVerifierList).
	for _, raw := range []string{"", "[]"} {
		t.Setenv(envSignerAccount, "")
		t.Setenv(envRosterServiceAccounts, raw)
		t.Setenv(envImplicitAdmins, raw)
		cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
		if err != nil {
			t.Fatalf("credentialsServiceConfig with %s and %s = %q: %v (the module renders none of the three yet, so a deployment that carries none must start the same service it always did)", envSignerAccount, envRosterServiceAccounts, raw, err)
		}
		if cfg.Identity.Signer != "" || len(cfg.Identity.ServiceAccounts) != 0 || len(cfg.Identity.ImplicitAdmins) != 0 {
			t.Errorf("the configuration carries %+v with the three names absent: an installation whose module renders none has no issuer, and a value here would be one this process invented", cfg.Identity)
		}
		if _, err := credsvc.NewServer(cfg); err != nil {
			t.Fatalf("NewServer with the three names absent: %v", err)
		}
	}

	// Half three: a value that is present and is not the document the module
	// renders is a deployment that is wrong, refused by name and never by
	// value. The signer is held to one address — the coordinator's own rule,
	// because a single value is not a document: what is refused is a space or
	// a control byte around the address and not its spelling, which is left to
	// whoever consumes it — and the two lists to the array every class
	// list is read as, which refuses a quoted string, a row and any element
	// that is not one.
	for _, tc := range []struct {
		name  string
		entry string
		read  func(string) error
		raw   []string
	}{
		{envSignerAccount, signer, func(raw string) error { _, err := credentialsServiceSigner(raw); return err },
			[]string{signer + " ", " " + signer, "\t" + signer, signer + "\n", signer + " " + signer, signer + "\r\n"}},
		{envRosterServiceAccounts, ci, func(raw string) error { _, err := credentialsServiceRosterServiceAccounts(raw); return err },
			[]string{"not json", `"` + ci + `"`, `[{"email":"` + ci + `"}]`, `["` + ci + `",3]`}},
		{envImplicitAdmins, admin, func(raw string) error { _, err := credentialsServiceImplicitAdmins(raw); return err },
			[]string{"not json", `"` + admin + `"`, `[{"user":"` + admin + `"}]`, `["` + admin + `",3]`}},
	} {
		for _, raw := range tc.raw {
			err := tc.read(raw)
			if err == nil {
				t.Errorf("reading %s = %q is accepted: the value is the document the module renders or nothing", tc.name, raw)
				continue
			}
			if !strings.Contains(err.Error(), tc.name) {
				t.Errorf("the refusal for %s = %q is %q, want it to name the variable", tc.name, raw, err)
			}
			if strings.Contains(err.Error(), tc.entry) {
				t.Errorf("the refusal for %s = %q carries the account itself: %q (this process's log is a line an operator reads)", tc.name, raw, err)
			}
		}
		// ... and the same value through the builder, which is where the
		// refusal reaches an operator: a malformed entry is a deployment that
		// is wrong and not a value to be read as empty.
		t.Setenv(tc.name, tc.raw[0])
		if _, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard); err == nil || ExitCode(err) != ExitUserError {
			t.Errorf("err = %v for %s = %q, want a user error (exit 1)", err, tc.name, tc.raw[0])
		} else if !strings.Contains(err.Error(), tc.name) {
			t.Errorf("the builder's refusal for %s = %q is %q, want it to name the variable", tc.name, tc.raw[0], err)
		}
		t.Setenv(tc.name, "")
	}
}

// TestCredentialsServiceConfigBuildsFromAFullEnvironment is the headline: the
// whole of this task is that the service can be configured at all, so a
// complete environment must build a credsvc.Config that NewServer accepts --
// the object the host below serves. Until this task the builder could not: it
// read the three names the module set and refused, because the four facts a
// Config needs (the database, the Firebase project, the audience and the job
// accounts) were not in the container.
//
// The two things the builder needs that reach Google as the service itself --
// the token source the database is read with and the Secret Manager client the
// bundle is read through -- are the test seam (credentialsTest); nothing else
// here is faked, so the derivation of the job accounts and every validation
// the verifier makes are the production ones.
//
// The derived table is pinned by value and not by comparison with the
// function that made it: these emails are the ones internal/infra deploys
// (gcp.ServiceAccountID over task.Slug), so a change in either derivation
// fails here rather than in a run that cannot authenticate.
func TestCredentialsServiceConfigBuildsFromAFullEnvironment(t *testing.T) {
	for _, n := range credentialsServiceEnvNames {
		t.Setenv(n, credentialsServeEnv[n])
	}
	// The optional entries this installation's module would render beside them:
	// the verify route's list and the review class's document. They are set
	// here rather than in the loop above because the command reads them outside
	// credentialsServiceEnvNames (an installation that has neither must start
	// the same service it always did), and this is the test that pins what a
	// complete environment builds.
	for _, n := range []string{envVerifierAccounts, envReviewJobAccounts} {
		t.Setenv(n, credentialsServeEnv[n])
	}
	fakeCredentialsService(t)

	cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("credentialsServiceConfig: %v", err)
	}
	// The workflow the row names ("api", credentialsServeEnv's own entry) is
	// pinned with the rest of the row: it is what the workflow-secrets route
	// serves a call of this job by (B3.2), so a table that lost it would leave
	// that route unable to say which workflow's secrets a call is for.
	want := []credsvc.JobAccount{{
		Email:    "fugaro-acme-webapp-ap-7daad322@proj-1234.iam.gserviceaccount.com",
		Slug:     "acme-webapp-cf67624405f4121a",
		Repo:     "github/acme/webapp",
		Workflow: "api",
	}}
	if len(cfg.JobAccounts) != 1 || cfg.JobAccounts[0] != want[0] {
		t.Fatalf("job accounts = %+v, want %+v", cfg.JobAccounts, want)
	}
	// The repository table is the same rows read the other way round
	// (RepositoryTableFor), and it is pinned by value for the same reason the
	// accounts are: it is what resolves a review caller's slug to the
	// repository the pairing is made against, so a table that lost a row would
	// refuse review calls for a repository this installation serves.
	wantRepos := map[string]string{"acme-webapp-cf67624405f4121a": "github/acme/webapp"}
	if len(cfg.Repos) != len(wantRepos) {
		t.Fatalf("repository table = %v, want %v", cfg.Repos, wantRepos)
	}
	for slug, repo := range wantRepos {
		if got := cfg.Repos[slug]; got != repo {
			t.Errorf("repository table[%q] = %q, want %q", slug, got, repo)
		}
	}
	// Every piece NewServer requires is built here, and the one set that is
	// deliberately left empty is the shared project jobs' accounts (Global
	// Constraint 6: per-repository jobs only until C3.9).
	for _, missing := range []struct {
		name string
		set  bool
	}{
		{"Auth", cfg.Auth != nil},
		{"Bundles", cfg.Bundles != nil},
		{"Policy", cfg.Policy != nil},
		{"State", cfg.State != nil},
		{"Kills", cfg.Kills != nil},
		{"DB", cfg.DB != nil},
		{"Probes", cfg.Probes != nil},
		{"Slips", cfg.Slips != nil},
		{"Signs", cfg.Signs != nil},
		{"AuditLog", cfg.AuditLog != nil},
		{"Log", cfg.Log != nil},
	} {
		if !missing.set {
			t.Errorf("the configuration has no %s: the service cannot serve without it", missing.name)
		}
	}
	// The review class's table (C3.9a), pinned by value for the reason the job
	// accounts above are: these are the accounts internal/infra deploys the
	// review-only variant as (credsvc.ReviewServiceAccountID over
	// config.ReviewVariantAccount's spelling of the axis), and the account each
	// call is served by is the one the published admissions speak. So a change
	// in either derivation fails here rather than in a review run that cannot
	// authenticate. The order is the table's own (by email), which is what puts
	// the default account's row first.
	wantReview := []credsvc.ReviewJobAccount{
		{
			Email:   "fugaro-p-standard-git-04644ca9@proj-1234.iam.gserviceaccount.com",
			Account: config.DefaultClaudeAccount,
		},
		{
			Email:   "fugaro-p-standard-git-b5f51775@proj-1234.iam.gserviceaccount.com",
			Account: "team-a",
		},
	}
	if len(cfg.ReviewAccounts) != len(wantReview) {
		t.Fatalf("review accounts = %+v, want %+v", cfg.ReviewAccounts, wantReview)
	}
	for i, ra := range wantReview {
		if cfg.ReviewAccounts[i] != ra {
			t.Errorf("review accounts[%d] = %+v, want %+v", i, cfg.ReviewAccounts[i], ra)
		}
	}
	if _, err := credsvc.NewServer(cfg); err != nil {
		t.Fatalf("NewServer refused the configuration a complete environment builds: %v", err)
	}
}

// fakeCredentialsService replaces the two constructors that would need
// Application Default Credentials with ones that build nothing: the token
// source is never asked for a token (nothing in the configuration reads the
// database) and the Secret Manager client is never asked for a version.
func fakeCredentialsService(t *testing.T) {
	t.Helper()
	credentialsTest = &credentialsWiring{
		tokens: func(context.Context) (oauth2.TokenSource, error) {
			return oauth2.StaticTokenSource(&oauth2.Token{}), nil
		},
		secrets: func(context.Context) (*secretmanager.Service, error) { return &secretmanager.Service{}, nil },
	}
	t.Cleanup(func() { credentialsTest = nil })
}

// TestCredentialsServiceConfigRefusesAMalformedVariable: every name has a
// shape, and a value that is not it is refused by name -- before the
// configuration is built, so a deployment that is wrong never reaches a
// database read or a caller. The three that matter most are the ones a wrong
// value would silently mis-serve with: a database URL that is another
// installation's (the pair with the Firebase project, which is what a copied
// environment looks like), an audience that is not this service's URL (a
// service that admits nobody, or the wrong somebody), and a job account table
// that cannot be derived (a service whose account names do not exist).
func TestCredentialsServiceConfigRefusesAMalformedVariable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   map[string]string
		names []string // the variables the refusal must name
	}{
		{
			name:  "the database URL is not one",
			set:   map[string]string{envRTDBURL: "aurora-fp-default-rtdb.firebaseio.com"},
			names: []string{envRTDBURL},
		},
		{
			name:  "the database URL has a path",
			set:   map[string]string{envRTDBURL: "https://aurora-fp-default-rtdb.firebaseio.com/fugaro"},
			names: []string{envRTDBURL},
		},
		{
			name:  "the database is another Firebase project's",
			set:   map[string]string{envRTDBURL: "https://other-default-rtdb.firebaseio.com"},
			names: []string{envRTDBURL, envFirebaseFP},
		},
		{
			name:  "the audience has no scheme",
			set:   map[string]string{envCredentialsServiceURL: "fugaro-credentials-abcde-uc.a.run.app"},
			names: []string{envCredentialsServiceURL},
		},
		{
			name:  "the audience is http",
			set:   map[string]string{envCredentialsServiceURL: "http://fugaro-credentials-abcde-uc.a.run.app"},
			names: []string{envCredentialsServiceURL},
		},
		{
			name:  "the audience carries a path",
			set:   map[string]string{envCredentialsServiceURL: "https://fugaro-credentials-abcde-uc.a.run.app/v1/messages"},
			names: []string{envCredentialsServiceURL},
		},
		{
			name:  "the job accounts are not JSON",
			set:   map[string]string{envJobAccounts: "acme/webapp api"},
			names: []string{envJobAccounts},
		},
		{
			name:  "the job accounts are an object",
			set:   map[string]string{envJobAccounts: `{"provider":"github"}`},
			names: []string{envJobAccounts},
		},
		{
			name:  "the job accounts are empty",
			set:   map[string]string{envJobAccounts: `[]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "a job has no workflow",
			set:   map[string]string{envJobAccounts: `[{"provider":"github","repo":"acme/webapp"}]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "a job carries a field nothing reads",
			set:   map[string]string{envJobAccounts: `[{"provider":"github","repo":"acme/webapp","workflow":"api","project":"aurora"}]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "a job's provider is not a provider kind",
			set:   map[string]string{envJobAccounts: `[{"provider":"GitHub","repo":"acme/webapp","workflow":"api"}]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "a job's repository cannot be named",
			set:   map[string]string{envJobAccounts: `[{"provider":"github","repo":"acme","workflow":"api"}]`},
			names: []string{envJobAccounts},
		},
		{
			name:  "the verifier list is not a list",
			set:   map[string]string{envVerifierAccounts: "person-1@example.com"},
			names: []string{envVerifierAccounts},
		},
		{
			name:  "the verifier list carries something that is not an address",
			set:   map[string]string{envVerifierAccounts: `[{"email":"person-1@example.com"}]`},
			names: []string{envVerifierAccounts},
		},
		{
			// One address is what the module renders, and the class matches it
			// against a claim: two of them is a deployment that is wrong rather
			// than an account that admits nobody (BB5).
			name:  "the coordinator account is more than one address",
			set:   map[string]string{envCoordinatorAccount: "coordinator-1@example.com coordinator-2@example.com"},
			names: []string{envCoordinatorAccount},
		},
		{
			name:  "the review job accounts are not JSON",
			set:   map[string]string{envReviewJobAccounts: "standard github team-a"},
			names: []string{envReviewJobAccounts},
		},
		{
			name:  "the review job accounts are an object",
			set:   map[string]string{envReviewJobAccounts: `{"profile":"standard","provider":"github","account":"team-a"}`},
			names: []string{envReviewJobAccounts},
		},
		{
			// The variant's axis has no repository, so a row that carries one is
			// an owner expecting a binding the review class does not make: it is
			// refused rather than ignored (the job table's own rule).
			name:  "a review row carries a field nothing reads",
			set:   map[string]string{envReviewJobAccounts: `[{"profile":"standard","provider":"github","account":"team-a","repo":"acme/webapp"}]`},
			names: []string{envReviewJobAccounts},
		},
		{
			name:  "a review row has no profile",
			set:   map[string]string{envReviewJobAccounts: `[{"provider":"github","account":"team-a"}]`},
			names: []string{envReviewJobAccounts},
		},
		{
			name:  "a review row has no provider",
			set:   map[string]string{envReviewJobAccounts: `[{"profile":"standard","account":"team-a"}]`},
			names: []string{envReviewJobAccounts},
		},
		{
			name:  "a review row's provider is not a provider kind",
			set:   map[string]string{envReviewJobAccounts: `[{"profile":"standard","provider":"GitHub","account":"team-a"}]`},
			names: []string{envReviewJobAccounts},
		},
		{
			name:  "the shared job accounts are not JSON",
			set:   map[string]string{envSharedJobAccounts: "standard github team-a"},
			names: []string{envSharedJobAccounts},
		},
		{
			name:  "the shared job accounts are an object",
			set:   map[string]string{envSharedJobAccounts: `{"profile":"standard","provider":"github","account":"team-a","repos":["github/acme/webapp"]}`},
			names: []string{envSharedJobAccounts},
		},
		{
			// A shared row carries the repositories its profile is bound to, so
			// a row written with a single repository under the per-repository
			// job table's key is an owner expecting a binding this table does
			// not make: refused rather than ignored (the review table's rule).
			name:  "a shared row carries a field nothing reads",
			set:   map[string]string{envSharedJobAccounts: `[{"profile":"standard","provider":"github","account":"team-a","repos":["github/acme/webapp"],"repo":"acme/webapp"}]`},
			names: []string{envSharedJobAccounts},
		},
		{
			name:  "a shared row has no profile",
			set:   map[string]string{envSharedJobAccounts: `[{"provider":"github","account":"team-a","repos":["github/acme/webapp"]}]`},
			names: []string{envSharedJobAccounts},
		},
		{
			name:  "a shared row has no provider",
			set:   map[string]string{envSharedJobAccounts: `[{"profile":"standard","account":"team-a","repos":["github/acme/webapp"]}]`},
			names: []string{envSharedJobAccounts},
		},
		{
			name:  "a shared row's provider is not a provider kind",
			set:   map[string]string{envSharedJobAccounts: `[{"profile":"standard","provider":"GitHub","account":"team-a","repos":["github/acme/webapp"]}]`},
			names: []string{envSharedJobAccounts},
		},
		{
			// The member is REQUIRED and an empty array is not the same thing:
			// an empty array is a profile bound to no repository, the deny-all
			// statement, while a row that omits it is a document nobody could
			// have written on purpose — read as deny-all it would be a job
			// every call of which this service refuses, silently.
			name:  "a shared row names no repositories at all",
			set:   map[string]string{envSharedJobAccounts: `[{"profile":"standard","provider":"github","account":"team-a"}]`},
			names: []string{envSharedJobAccounts},
		},
		{
			name:  "a shared row has an empty entry in repos",
			set:   map[string]string{envSharedJobAccounts: `[{"profile":"standard","provider":"github","account":"team-a","repos":["github/acme/webapp",""]}]`},
			names: []string{envSharedJobAccounts},
		},
		{
			name:  "the build accounts are not JSON",
			set:   map[string]string{envBuildAccounts: "fugaro-build-identity@proj-1234.iam.gserviceaccount.com"},
			names: []string{envBuildAccounts},
		},
		{
			name:  "the build accounts are an object",
			set:   map[string]string{envBuildAccounts: `{"email":"fugaro-build-identity@proj-1234.iam.gserviceaccount.com"}`},
			names: []string{envBuildAccounts},
		},
		{
			// A list of addresses is what the module renders from the account it
			// creates, so one carrying rows is a deployment that is wrong
			// rather than a class admitting nobody -- the verifier list's rule,
			// one class over.
			name:  "the build list carries a row rather than an address",
			set:   map[string]string{envBuildAccounts: `[{"email":"fugaro-build-identity@proj-1234.iam.gserviceaccount.com"}]`},
			names: []string{envBuildAccounts},
		},
		{
			name:  "the orchestrator list is not JSON",
			set:   map[string]string{envOrchestratorAccounts: "fugaro-orch@proj-1234.iam.gserviceaccount.com"},
			names: []string{envOrchestratorAccounts},
		},
		{
			// The daemon's address is what the module renders, so a list of
			// rows here is a deployment that is wrong rather than a class
			// admitting nobody — the build list's rule, one class over.
			name:  "the orchestrator list carries a row rather than an address",
			set:   map[string]string{envOrchestratorAccounts: `[{"email":"fugaro-orch@proj-1234.iam.gserviceaccount.com"}]`},
			names: []string{envOrchestratorAccounts},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, n := range credentialsServiceEnvNames {
				t.Setenv(n, credentialsServeEnv[n])
			}
			for k, v := range tc.set {
				t.Setenv(k, v)
			}
			// No seam: every refusal below is made before the configuration
			// reaches Google, which is the property this pins -- a deployment
			// that is wrong costs nothing but the refusal.
			_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
			if err == nil || ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v, want a user error (exit 1)", err)
			}
			for _, n := range tc.names {
				if !strings.Contains(err.Error(), n) {
					t.Fatalf("err = %v, want a refusal naming %s", err, n)
				}
			}
		})
	}
}

// TestCredentialsServiceRefusalsNeverCarryAValue: this process is the one that
// holds the bundle's values in memory, and every refusal below is a line an
// operator reads in a log, so none of them may quote what the environment
// held. The values here are shaped like a credential (and deliberately not
// like the names the installation really uses), and every refusal path is
// walked with them set: the variable that is wrong is the one that gets
// named, never the text that was in it, and never the text in the others.
func TestCredentialsServiceRefusalsNeverCarryAValue(t *testing.T) {
	secret := func(n string) string { return "s3cr3t-" + strings.ToLower(strings.TrimPrefix(n, "FUGARO_")) }
	values := map[string]string{
		envProject:               secret(envProject),
		envGCPProject:            secret(envGCPProject),
		envRegion:                secret(envRegion),
		envRTDBURL:               "https://" + secret(envFirebaseFP) + "-default-rtdb.firebaseio.com",
		envFirebaseFP:            secret(envFirebaseFP),
		envCredentialsServiceURL: "https://" + secret(envCredentialsServiceURL) + ".run.app",
		envJobAccounts:           `[{"provider":"github","repo":"` + secret("repo") + `","workflow":"api"}]`,
		envVerifierAccounts:      `["` + secret("verifier") + `@example.com"]`,
		envCoordinatorAccount:    secret("coordinator") + "@example.com",
		envReviewJobAccounts:     `[{"profile":"standard","provider":"github","account":"` + secret("account") + `"}]`,
	}
	for _, tc := range []struct {
		name string
		set  map[string]string
	}{
		{"every name is missing", map[string]string{
			envProject: "", envGCPProject: "", envRegion: "", envRTDBURL: "",
			envFirebaseFP: "", envCredentialsServiceURL: "", envJobAccounts: "",
		}},
		{"the database URL is not one", map[string]string{envRTDBURL: "s3cr3t-db"}},
		{"the database is another project's", map[string]string{envRTDBURL: "https://s3cr3t-other-default-rtdb.firebaseio.com"}},
		{"the audience is malformed", map[string]string{envCredentialsServiceURL: "s3cr3t-url"}},
		{"the job accounts are malformed", map[string]string{envJobAccounts: "s3cr3t-jobs"}},
		{"a job cannot be derived", map[string]string{envJobAccounts: `[{"provider":"github","repo":"s3cr3t-nope","workflow":"api"}]`}},
		{"the verifier list is malformed", map[string]string{envVerifierAccounts: "s3cr3t-verifiers"}},
		{"the coordinator account carries a space", map[string]string{envCoordinatorAccount: "s3cr3t-coordinator s3cr3t"}},
		{"the review job accounts are malformed", map[string]string{envReviewJobAccounts: "s3cr3t-reviews"}},
		{"a review row's provider cannot be derived", map[string]string{envReviewJobAccounts: `[{"profile":"standard","provider":"s3cr3t-nope","account":"team-a"}]`}},
		{"a review row has no profile", map[string]string{envReviewJobAccounts: `[{"profile":"","provider":"github","account":"s3cr3t-account"}]`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for n, v := range values {
				t.Setenv(n, v)
			}
			for n, v := range tc.set {
				t.Setenv(n, v)
			}
			_, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
			if err == nil {
				t.Fatal("want a refusal")
			}
			for n, v := range values {
				if v == "" {
					continue
				}
				if strings.Contains(err.Error(), v) {
					t.Errorf("err = %v, which carries the value of %s", err, n)
				}
			}
		})
	}
}

// TestCredentialsServiceEnvIsTheDeploymentsOwn: the names this command reads
// are the names credentials.tf sets on the service's container, and neither
// side may move without the other. The direction that matters is the module
// setting a name this command does not read: an operator reading
// credentials.tf would believe the fact was in effect while the service
// refused to start without it (or, worse, served with a value it never read).
// The other direction is a name this command reads that nothing sets, which is
// a fact the service silently does not have.
//
// The list is the seven REQUIRED names plus the eleven optional ones, which is
// the whole of what the module renders and what this command reads: the
// optional ones belong here because a name the module sets and this command
// does not read is the same mistake whether the command requires it or reads it
// outside the loop. Which of the names is which is pinned by the tests around
// this one, not by this comparison.
//
// All eleven are rendered conditionally — the module sets each of them only on
// an installation where it applies — and the reason they belong here rather than
// in credentialsServiceEnvNames is pinned by the tests around this one. The
// conditionality is also what lets a reader arrive before the module that
// renders it — FUGARO_SHARED_JOB_ACCOUNTS (B5.1) was read here for a release
// before credentials.tf set it, the same cover-first order
// FUGARO_REVIEW_JOB_ACCOUNTS followed — and
// CredentialsServiceEnvAwaitingTheModule (internal/infra/tf) was the list that
// held such a name. FUGARO_SHARED_JOB_ACCOUNTS was its last member and the
// module renders it now, so the list is empty and the loop below requires every
// name this command reads to be in credentials.tf.
//
// internal/infra/tf's credentialsServiceEnv is the third side of this, and it
// is held to the module by that package's own test (the cover's allow-list
// refuses a container that sets a name outside it, which is what makes the
// module's list a boundary rather than a convention).
func TestCredentialsServiceEnvIsTheDeploymentsOwn(t *testing.T) {
	tf := readRepoFile(t, "deploy/terraform/gcp/modules/installation/credentials.tf")
	inModule := map[string]bool{}
	for _, m := range regexp.MustCompile(`name\s*=\s*"(FUGARO_[A-Z0-9_]+)"`).FindAllStringSubmatch(tf, -1) {
		inModule[m[1]] = true
	}
	pending := infraTF.CredentialsServiceEnvAwaitingTheModule()
	read := append(slices.Clone(credentialsServiceEnvNames), envVerifierAccounts, envPersonAccounts, envLauncherAccounts, envCoordinatorAccount, envReviewJobAccounts, envSharedJobAccounts, envBuildAccounts, envOrchestratorAccounts, envSignerAccount, envRosterServiceAccounts, envImplicitAdmins)
	for _, n := range read {
		if slices.Contains(pending, n) {
			if inModule[n] {
				t.Errorf("credentials.tf sets %s, so it is no longer a name the module does not render yet: drop it from internal/infra/tf's CredentialsServiceEnvAwaitingTheModule, which this test reads", n)
			}
			continue
		}
		if !inModule[n] {
			t.Errorf("this command reads %s, which credentials.tf does not set on the service: the service would refuse to start on a deployment that is right", n)
		}
		delete(inModule, n)
	}
	for n := range inModule {
		t.Errorf("credentials.tf sets %s, which this command does not read: an operator would believe the fact was in effect and the service would refuse to start without it", n)
	}
}

// serveForTest runs the host's own server -- credentialsHTTPServer over a real
// listener, driven by serveUntilStop -- with a test's handler in place of the
// service's, and returns its address and the server object. Every protocol test
// below is written against this and not against httptest.NewServer, because the
// object they pin is the one the deployment is served by: a protocol set on the
// standard library's own test server would be a different server.
func serveForTest(t *testing.T, h http.Handler) (string, *http.Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := credentialsHTTPServer(ctx, h, io.Discard)
	done := make(chan error, 1)
	go func() { done <- serveUntilStop(ctx, ln, srv, io.Discard) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("the host did not stop")
		}
	})
	return "http://" + ln.Addr().String(), srv
}

// h2cClient is a caller that speaks HTTP/2 to a cleartext address with prior
// knowledge: it opens the connection, sends the HTTP/2 preface and no TLS
// handshake at all. That is what Cloud Run's front end does to the container
// once the service's port is named h2c, and it is why the service must serve
// unencrypted HTTP/2 rather than merely announce it.
//
// Only HTTP/2 is offered, so a server that answers over HTTP/1.1 cannot pass
// by negotiation: the client would fail before any request was made.
func h2cClient() *http.Client {
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	return &http.Client{Transport: &http.Transport{Protocols: &protocols}}
}

// TestCredentialsServeSpeaksH2C is what B1.0 exists for (owner-run Check 190,
// 2026-10-30): a 33 MiB pack over HTTP/1.1 was cut by the platform's front end
// with a 413 before the service saw four bytes of it, and the front end speaks
// HTTP/2 cleartext to a container whose port is named h2c. So the server this
// command builds must answer an HTTP/2 client that does no TLS at all -- and
// answer it AS HTTP/2, because a server that merely ignored the preface would
// be serving HTTP/1.1 to a front end that has already committed to h2c.
func TestCredentialsServeSpeaksH2C(t *testing.T) {
	addr, _ := serveForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "answered over HTTP/%d.%d", r.ProtoMajor, r.ProtoMinor)
	}))
	resp, err := h2cClient().Get(addr + "/v1/messages")
	if err != nil {
		t.Fatalf("an h2c call failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the h2c call is %d %q, want 200", resp.StatusCode, body)
	}
	if resp.ProtoMajor != 2 {
		t.Fatalf("the answer is HTTP/%d.%d (%q), want HTTP/2: the front end speaks h2c to this container, and a large push is cut at the HTTP/1 limit otherwise", resp.ProtoMajor, resp.ProtoMinor, body)
	}
}

// TestCredentialsServeStillSpeaksHTTP1 is the other half of the protocol set,
// and the half that keeps this task from halting an installation: every caller
// the service has today speaks HTTP/1.1 (the runner's gateway is a plain
// net/http client), so a server that answered only an h2c client would refuse
// every model call. The client below offers HTTP/1.1 alone, so h2c cannot carry
// this request by negotiation: what it pins is that the same listener still
// serves the older protocol, and that the h2c addition did not replace it.
func TestCredentialsServeStillSpeaksHTTP1(t *testing.T) {
	addr, srv := serveForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "answered over HTTP/%d.%d", r.ProtoMajor, r.ProtoMinor)
	}))
	// The protocol set is stated, not inferred from an answer: both protocols
	// on, TLS-based HTTP/2 off (this process terminates no TLS; Cloud Run's
	// front end does, and it speaks h2c to the container).
	if srv.Protocols == nil || !srv.Protocols.HTTP1() || !srv.Protocols.UnencryptedHTTP2() {
		t.Fatalf("the host's protocols = %+v, want HTTP/1.1 and unencrypted HTTP/2", srv.Protocols)
	}
	if srv.Protocols.HTTP2() {
		t.Error("the host offers TLS-based HTTP/2: it serves no TLS, and the front end's h2c is the unencrypted protocol")
	}
	// The HTTP/2 side of the server is the block net/http reads when it builds
	// the h2 handler, and neither number in it may fall back to a default: Go's
	// own MaxConcurrentStreams is 250 (net/http/internal/http2/server.go, whose
	// comment there is a TODO to make it 100 "as the GFE seems to"), and its
	// WriteByteTimeout default of 0 would let a peer that stopped reading hold a
	// goroutine and an upstream connection carrying a model credential for as
	// long as the route's own timeout allows. Read off the server object because
	// both are invisible to a client that behaves, and written as literals
	// rather than as the constants: the two numbers are the decision, and a test
	// that compared them to the constants would follow a change of one
	// silently.
	if srv.HTTP2 == nil {
		t.Fatal("the host has no HTTP2 config: the h2c server takes net/http's defaults for the stream count and the write bound")
	}
	if srv.HTTP2.MaxConcurrentStreams != 100 {
		t.Errorf("the host allows %d concurrent streams, want 100, the limit the front end in front of it allows a caller", srv.HTTP2.MaxConcurrentStreams)
	}
	if srv.HTTP2.WriteByteTimeout != 2*time.Minute {
		t.Errorf("the host's write-byte timeout = %v, want 2m: the bound on a peer that has stopped reading, not on a stream still moving", srv.HTTP2.WriteByteTimeout)
	}
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	client := &http.Client{Transport: &http.Transport{Protocols: &protocols}}
	resp, err := client.Get(addr + "/v1/messages")
	if err != nil {
		t.Fatalf("an HTTP/1.1 call failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.ProtoMajor != 1 {
		t.Fatalf("the HTTP/1.1 call is %d over HTTP/%d.%d (%q), want 200 over HTTP/1", resp.StatusCode, resp.ProtoMajor, resp.ProtoMinor, body)
	}
}

// largeBodyReader produces n bytes of a repeating pattern without ever holding
// them, and counts how many it has handed out: what the test below needs is a
// body big enough to matter that the test itself never allocates (a 40 MiB
// []byte here would make the test's own footprint the largest thing in the
// room, which is the measurement the assertion below turns into a claim about
// the server).
type largeBodyReader struct {
	left int64
	next byte
	sent *atomic.Int64
}

func (r *largeBodyReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > r.left {
		n = r.left
	}
	for i := int64(0); i < n; i++ {
		r.next++
		p[i] = r.next
	}
	r.left -= n
	r.sent.Add(n)
	return int(n), nil
}

// TestCredentialsServeH2CStreamsALargeBody is B1.0's own claim, measured: a
// 40 MiB body (a pack larger than the 33 MiB one Check 190 lost) reaches the
// handler through an h2c connection, and it reaches it as a stream rather than
// as a buffer.
//
// The assertion is about WHEN the handler sees the body, and it is the one a
// buffering front end cannot pass: the client counts every byte it has handed
// to its transport, and the handler records that count as it reads its first
// chunk. HTTP/2 flow control bounds how far a truthful peer may run ahead of a
// handler that has not read yet, so a server that streams shows a count in the
// low megabytes; a server that reads the whole body into memory before it calls
// the handler shows 40 MiB there, which is exactly what the front end did to
// Check 190's push (it answered 413 having read the body it would not stream).
//
// What is deliberately not asserted is the process's heap: a delta measured
// around a transfer is a GC-timing observation and not a property of this
// server, while the count above is deterministic. The handler's own buffer is
// 32 KiB and the total is exact, so a body that arrived truncated (which is
// what a cut at the HTTP/1 limit looks like) fails here too.
func TestCredentialsServeH2CStreamsALargeBody(t *testing.T) {
	const body = 40 << 20
	var sent, read, sentAtFirstRead atomic.Int64
	addr, _ := serveForTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 32<<10)
		first := true
		for {
			n, err := r.Body.Read(buf)
			if n > 0 {
				if first {
					sentAtFirstRead.Store(sent.Load())
					first = false
				}
				read.Add(int64(n))
			}
			if err != nil {
				break
			}
		}
		io.WriteString(w, "read")
	}))
	src := &largeBodyReader{left: body, sent: &sent}
	resp, err := h2cClient().Post(addr+"/v1/messages", "application/octet-stream", src)
	if err != nil {
		t.Fatalf("an h2c push failed: %v", err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || resp.ProtoMajor != 2 {
		t.Fatalf("the push is %d over HTTP/%d.%d (%q), want 200 over HTTP/2", resp.StatusCode, resp.ProtoMajor, resp.ProtoMinor, answer)
	}
	if got := read.Load(); got != body {
		t.Fatalf("the handler read %d bytes, want %d: a body that arrives short is what a cut at the HTTP/1 limit looks like", got, body)
	}
	if got := sentAtFirstRead.Load(); got >= body/4 {
		t.Fatalf("the handler's first chunk arrived after the client had handed over %d of %d bytes: this server read the whole body before it served it, which is the behaviour that lost Check 190's 33 MiB push", got, body)
	}
}

// TestCredentialsServePort: PORT is the platform's, and a value that is not a
// port is a deployment that is wrong -- refused by name rather than defaulted,
// because a service listening where the front end does not reach it looks
// started and answers nothing.
func TestCredentialsServePort(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		bad      bool
	}{
		{in: "", want: defaultServePort}, // Cloud Run always sets it; a developer's run does not
		{in: "8080", want: "8080"},
		{in: "1", want: "1"},
		{in: "65535", want: "65535"},
		{in: "0", bad: true},
		{in: "65536", bad: true},
		{in: "-1", bad: true},
		{in: "http", bad: true},
		{in: "8080 ", bad: true}, // strconv.Atoi refuses the space; a port is digits
	} {
		got, err := servePort(func(string) string { return tc.in })
		switch {
		case tc.bad && err == nil:
			t.Errorf("servePort(%q) = %q, want a refusal", tc.in, got)
		case tc.bad:
			if ExitCode(err) != ExitUserError {
				t.Errorf("servePort(%q) err = %v, want a user error", tc.in, err)
			}
		case err != nil:
			t.Errorf("servePort(%q): %v", tc.in, err)
		case got != tc.want:
			t.Errorf("servePort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCredentialsServeHostStopsOnSignal drives the host's own machinery: a
// real listener, a real request and a real shutdown, with the handler of a
// test rather than the service's (the service needs the deployment's facts
// above, and none of them is what this asserts). What it pins is what the
// owner asked this task for -- the port the platform gives (PORT), the bounds
// a long-lived streamed call allows, and a SIGTERM that drains instead of
// dying -- and it pins them on the object the service is served by, so a
// timeout added to it fails here.
func TestCredentialsServeHostStopsOnSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs bytes.Buffer
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := credentialsHTTPServer(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "answered")
	}), &logs)
	// The bounds, stated as the values and not as "non-zero": a
	// ReadHeaderTimeout is what stops a caller that connects and says nothing
	// from holding a connection for ever, and a WriteTimeout (or a
	// ReadTimeout) would cut a model call in flight, which is a run that fails
	// for a reason nothing in its record explains.
	if srv.ReadHeaderTimeout != serveReadHeaderTimeout || srv.IdleTimeout != serveIdleTimeout {
		t.Errorf("host bounds = read-header %s, idle %s, want %s and %s", srv.ReadHeaderTimeout, srv.IdleTimeout, serveReadHeaderTimeout, serveIdleTimeout)
	}
	if srv.WriteTimeout != 0 || srv.ReadTimeout != 0 {
		t.Errorf("the host sets WriteTimeout %s / ReadTimeout %s: a proxied streaming call is long-lived and either would cut it", srv.WriteTimeout, srv.ReadTimeout)
	}
	done := make(chan error, 1)
	go func() { done <- serveUntilStop(ctx, ln, srv, &logs) }()
	// The listener is serving: a request is answered before the signal.
	resp, err := http.Get("http://" + ln.Addr().String() + "/v1/messages")
	if err != nil {
		t.Fatalf("the host did not answer: %v\n%s", err, logs.String())
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "answered" {
		t.Fatalf("host answered %d %q", resp.StatusCode, body)
	}
	// The signal: the context ends, and the host returns with no error -- a
	// platform that is replacing this revision is not a failure.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveUntilStop: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serveUntilStop did not return after the signal")
	}
	if !strings.Contains(logs.String(), "listening on") {
		t.Errorf("the host's own line does not say what it listens on: %q", logs.String())
	}
}

// TestCredentialsServeRefusesAnUnbuildableConfiguration: NewServer names the
// piece a configuration is missing (credsvc.NewServer's own refusals), and the
// host turns that into a user error -- a revision whose deployment is wrong
// must fail to start, not serve a service that answers every call with a
// refusal.
func TestCredentialsServeRefusesAnUnbuildableConfiguration(t *testing.T) {
	err := hostCredentialsService(context.Background(), credsvc.Config{}, func(string) string { return "" }, io.Discard)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want a user error", err)
	}
	if !strings.Contains(err.Error(), "verifier") {
		t.Fatalf("err = %v, want NewServer's own refusal (the missing verifier)", err)
	}
}

// GODEBUG=http2debug makes Go print every HTTP request it handles, headers
// included, to stderr -- and this process handles the calls that carry a
// model credential. The refusal is the one every command that touches a
// credential makes (refuseHTTP2Debug), and it comes before the environment is
// read, so nothing at all happens under it.
func TestCredentialsServeRefusesHTTP2Debug(t *testing.T) {
	t.Setenv("GODEBUG", "http2debug=2")
	_, _, err := execute(t, "credentials", "serve")
	if err == nil || !strings.Contains(err.Error(), "http2debug") {
		t.Fatalf("err = %v, want the http2debug refusal", err)
	}
}

// TestCredentialsServeRefusesHTTP2DebugStill is the same refusal held against
// the h2c path this task adds, and it is the test that has to exist because the
// new path is the one http2debug would be loudest about: at 2 it prints HTTP/2
// frames, and the frames of a proxied call are a model credential and, from
// B1.5, the credential a git push carries. So the refusal has to be true of
// the process and not merely of the HTTP/1.1 branch of it.
//
// What "still" means here is the order, and it is asserted from both ends: the
// refusal comes before the environment is read (so an EMPTY environment is
// refused for http2debug and not for its first missing variable), and it comes
// before anything is served (so a COMPLETE environment -- the one that builds a
// configuration NewServer accepts -- is refused too, with the h2c server never
// built). The context is bounded so that a version of this code that served
// instead of refusing fails the test rather than hanging it.
func TestCredentialsServeRefusesHTTP2DebugStill(t *testing.T) {
	t.Setenv("GODEBUG", "http2debug=2")
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{name: "empty", env: map[string]string{}},
		{name: "complete", env: credentialsServeEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"GODEBUG": "http2debug=2"}
			for k, v := range tc.env {
				env[k] = v
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := serveCredentials(ctx, newCredentialsServeCmd(), func(k string) string { return env[k] })
			if err == nil || !strings.Contains(err.Error(), "http2debug") {
				t.Fatalf("err = %v, want the http2debug refusal: a process that serves the h2c path prints the frames of a call that carries a credential", err)
			}
		})
	}
}

// The workspace's personal half (C2, WC14) is what a workspace-mode process
// hands C1's resolver beside the front door, the node reader and the meter:
// the reader of the workspace's OWN roster node, the launcher verifier that
// roster admits its people with, and the slip store its own grants live in.
// All three are built over the workspace's own database — the one
// FUGARO_RTDB_URL names — and all three travel to the scope through C2's one
// package variable, `credsvc.PersonScopeConfigForServe`, which the process sets
// BEFORE the resolver is built (C2's "Two package-level variables are how the
// sides meet").
//
// What these tests can and cannot see is worth stating, because WC4 (the scope
// that reads the configuration) is written in parallel with this task and has
// not landed: what they pin is the WIRING — the three readers, over which
// database, and set before the resolver — which is the whole of what this task
// produces. The scope's use of them is WC4's own tests'.

// workspaceRosterDocument is a roster as C1's own encoder writes it: the
// version this build reads, the service-account mechanism (which owns no folder
// id), and the people and admins the caller names. It validates nothing itself,
// so a test that means to publish a roster this build REFUSES publishes one —
// which is exactly how the refusal is driven below.
func workspaceRosterDocument(people []workspace.Person, admins []string) *workspace.Roster {
	return &workspace.Roster{
		Version:   workspace.RosterVersion,
		Mechanism: workspace.MechanismSA,
		People:    people,
		Admins:    admins,
	}
}

// publishRoster writes a roster into the workspace's own database at the node
// C1's store reads, through C1's own encoder: the document the process decodes
// is the one a `fugaro workspace` command writes, and not a shape these tests
// invented.
func publishRoster(t *testing.T, fake *gcpfake.RTDB, client *rtdb.Client, name string, people []workspace.Person, admins []string) {
	t.Helper()
	store, err := workspace.NewRosterStore(client, name)
	if err != nil {
		t.Fatalf("the roster store these tests publish through: %v", err)
	}
	raw, err := workspace.EncodeRoster(workspaceRosterDocument(people, admins))
	if err != nil {
		t.Fatalf("the roster these tests publish is not one C1's encoder writes, so they would prove nothing about the read: %v", err)
	}
	fake.Set(store.Path(), json.RawMessage(raw))
}

// TestWorkspaceModeIsHandedTheRosterAndItsSlips is WC14's acceptance: a
// workspace-mode process builds the roster's reader, the launcher verifier and
// a slip store over the workspace's own database, and hands them to the scope's
// resolver through C2's configuration, before that resolver is built.
//
// The launcher class is the half a test cannot read off the configuration: the
// verifier's account list is unexported, and verifying a caller would need a
// Google ID token and Google's key set, neither of which is here. So the class
// is observed from the other end, by what the process DOES with a roster — and
// the two rosters below are the pair that pins it exactly:
//
//   - a roster naming a human AND a machine starts: the machine's address is
//     one the class's own construction refuses (foldLauncherAccounts), so the
//     process starting is what says the machine never reached it — R3's "people
//     only" (F12).
//   - a roster whose only person is a HUMAN with a service account's address
//     starts too, and the person half is set. A human's address is held to the
//     address grammar and not to its domain (workspace.CanonicalAddress), so
//     C1's roster admits this document, and a class built straight off its
//     people would have refused the whole deployment at the class's own
//     construction. It starts because the list builder leaves the shape out
//     (workspaceRosterLaunchers): one entry a member's roster could plant, or
//     an operator could mis-kind, must not stop every member's routes, and an
//     address of that shape could never be verified as a person anyway.
func TestWorkspaceModeIsHandedTheRosterAndItsSlips(t *testing.T) {
	const workspaceName = "aurora-ws"
	// The configuration is the package's, and the process is one deployment:
	// a test that left it set would hand the next test in this binary another
	// workspace's launcher class.
	before := credsvc.PersonScopeConfigForServe
	t.Cleanup(func() { credsvc.PersonScopeConfigForServe = before })

	t.Run("the roster's reader, launcher class and slips are the workspace's own", func(t *testing.T) {
		for n, v := range workspaceServeEnv(workspaceName) {
			t.Setenv(n, v)
		}
		fakeCredentialsService(t)
		fake, client := fakeWorkspaceDatabase(t, workspaceName)
		credentialsTest.database = func(context.Context, string, oauth2.TokenSource) (*rtdb.Client, error) {
			return client, nil
		}
		publishRoster(t, fake, client, workspaceName, []workspace.Person{
			// A machine of the workspace, and its address is one the launcher
			// class refuses: it is here to prove the class never saw it.
			{Address: workspaceServiceAccount, Kind: workspace.KindSA},
			{Address: "person-1@example.com", Kind: workspace.KindHuman},
		}, []string{"person-1@example.com"})

		// The order C2 fixes, recorded where it can be seen: the configuration
		// is what the scope reads when it is BUILT, so a resolver built over a
		// configuration that arrived afterwards would serve every grant of this
		// deployment with no launcher class and no slips.
		var atBuild credsvc.PersonScopeConfig
		credentialsWorkspaceTest = func(cfg credsvc.WorkspaceScopeConfig) (credsvc.ScopeResolver, error) {
			atBuild = credsvc.PersonScopeConfigForServe
			return credsvc.NewWorkspaceScopeResolver(cfg)
		}
		t.Cleanup(func() { credentialsWorkspaceTest = nil })

		cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
		if err != nil {
			t.Fatalf("credentialsServiceConfig with a roster published: %v", err)
		}
		if cfg.Resolver == nil {
			t.Fatal("the workspace's configuration handed the Server no resolver")
		}
		if atBuild.Roster == nil || atBuild.Launchers == nil || atBuild.Slips == nil {
			t.Fatalf("C1's constructor was handed Roster=%v Launchers=%v Slips=%v, and all three are set before it is built: the roster's reader, the verifier its people are admitted by, and the store this workspace's grants live in", atBuild.Roster != nil, atBuild.Launchers != nil, atBuild.Slips != nil)
		}
		person := credsvc.PersonScopeConfigForServe
		if person.Roster == nil || person.Launchers == nil || person.Slips == nil {
			t.Fatalf("the process left Roster=%v Launchers=%v Slips=%v for the scope to read", person.Roster != nil, person.Launchers != nil, person.Slips != nil)
		}

		// The reader is the workspace's own roster node, and it answers what is
		// THERE: the scope re-reads through it on the sixty-second rule the
		// published node has (C2's rule 3), so the person the owner removes is
		// refused without this service restarting (R3, D3146) — which a
		// snapshot taken at startup could not do.
		rosterNode, found, err := person.Roster.Read(context.Background())
		if err != nil {
			t.Fatalf("the roster reader: %v", err)
		}
		if !found {
			t.Fatal("the roster reader found no node at the path it answers, and this test published one there")
		}
		document, err := workspace.DecodeRoster(rosterNode.Raw)
		if err != nil {
			t.Fatalf("the document the roster reader answered is not one C1's reader reads: %v", err)
		}
		addresses := []string{}
		for _, p := range document.People {
			addresses = append(addresses, p.Address)
		}
		if !slices.Equal(addresses, []string{workspaceServiceAccount, "person-1@example.com"}) {
			t.Fatalf("the roster reader answered the people %v, want the roster published at the workspace's own node", addresses)
		}

		// ... and it reads the node NOW: the same reader, over the node the
		// owner has since moved.
		publishRoster(t, fake, client, workspaceName, []workspace.Person{
			{Address: "person-2@example.com", Kind: workspace.KindHuman},
		}, []string{"person-2@example.com"})
		rosterNode, found, err = person.Roster.Read(context.Background())
		if err != nil || !found {
			t.Fatalf("the second read of the roster: found=%v err=%v", found, err)
		}
		document, err = workspace.DecodeRoster(rosterNode.Raw)
		if err != nil {
			t.Fatalf("the second read's document: %v", err)
		}
		if len(document.People) != 1 || document.People[0].Address != "person-2@example.com" {
			t.Fatalf("the roster reader answered %+v after the node moved: a reader that answered the roster it was built over would be a snapshot, and a person removed from the roster is refused within a minute (R3, D3146) by the read this one makes", document.People)
		}

		// The slips are the workspace's OWN database's, which is the database
		// this process was handed and never a member's (R3): the grant writes
		// here and the claim reads here, and a slip of a member's is another
		// service's (WC8).
		//
		// What is asked of the store is only that it is a working store over
		// THIS database, and neither question needs the slip half's own names:
		// internal/creds' boundary test keeps `creds.Slip` and the rest out of
		// every file of this package but the service's, and it is right to —
		// no operator command grants, claims or ends a slip (G10). So the store
		// is read through the interface the process handed the scope, and the
		// database it reached is the one that answers: a read of an id nothing
		// wrote answers "no such slip" out of the workspace's own fake, and the
		// same read fails when that fake refuses every call.
		if _, found, err := person.Slips.Slip(context.Background(), "slip-wc14"); err != nil {
			t.Fatalf("the slip store the process handed the scope could not read the workspace's own database: %v", err)
		} else if found {
			t.Fatal("the slip store found a slip at an id nothing has written, and this workspace's database holds none")
		}
		fake.Refuse(http.StatusServiceUnavailable, "UNAVAILABLE", "", "the workspace's own database is out")
		if _, _, err := person.Slips.Slip(context.Background(), "slip-wc14"); err == nil {
			t.Error("the slip store answered while the workspace's own database refused every call: the store the process handed the scope is not this database's, and the workspace's slips are the workspace's own (R3)")
		}
		fake.Refuse(0, "", "", "")

		// ... and the workspace's deployment is still the one Server (D2980):
		// the personal half is additive and changes nothing about the
		// installation's path (C2's rule 1).
		if _, err := credsvc.NewServer(cfg); err != nil {
			t.Fatalf("NewServer refused the workspace's configuration with a roster published: %v", err)
		}
	})

	t.Run("a roster whose human is a service account still starts", func(t *testing.T) {
		// A HUMAN with a service account's address: C1's roster admits the
		// document (a person's address is held to the address grammar, and its
		// domain is not the roster's business), and the launcher class would
		// refuse it — so the process starting, with the class built and handed
		// to the scope, is the observation that the list builder left the entry
		// out rather than handing it on. A deployment stopped here would stop
		// every member's model and forge routes over one entry that could never
		// be verified as a person (C2's "not a failure to start").
		const machine = "svc-1@proj-1234.iam.gserviceaccount.com"
		for n, v := range workspaceServeEnv(workspaceName) {
			t.Setenv(n, v)
		}
		fakeCredentialsService(t)
		fake, client := fakeWorkspaceDatabase(t, workspaceName)
		credentialsTest.database = func(context.Context, string, oauth2.TokenSource) (*rtdb.Client, error) {
			return client, nil
		}
		publishRoster(t, fake, client, workspaceName, []workspace.Person{
			{Address: machine, Kind: workspace.KindHuman},
		}, []string{machine})

		cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
		if err != nil {
			t.Fatalf("credentialsServiceConfig with a roster whose only person is a human holding a service account's address: %v", err)
		}
		if cfg.Resolver == nil {
			t.Fatal("the workspace's configuration handed the Server no resolver: this workspace's owner HAS published a roster, so the personal half is the workspace's own and a nil resolver would be the installation's")
		}
		person := credsvc.PersonScopeConfigForServe
		if person.Roster == nil || person.Launchers == nil || person.Slips == nil {
			t.Fatalf("the process left Roster=%v Launchers=%v Slips=%v: the half is set whether or not the roster named anybody this class can admit, and the class being BUILT and non-refusing is what says the service-account-shaped entry never reached it", person.Roster != nil, person.Launchers != nil, person.Slips != nil)
		}
		if _, err := credsvc.NewServer(cfg); err != nil {
			t.Fatalf("NewServer refused the workspace's configuration: %v", err)
		}
	})

	t.Run("a roster that cannot be read refuses the process", func(t *testing.T) {
		// The roster lives in the same database as the published node this
		// function read a moment earlier, so a read of it that fails is a
		// deployment that cannot serve its people — and a process that started
		// anyway would refuse every one of their grants while looking healthy
		// (D143's fail-closed direction). The read that failed is an error and
		// never "no roster" (C2's rule 3).
		for n, v := range workspaceServeEnv(workspaceName) {
			t.Setenv(n, v)
		}
		fakeCredentialsService(t)
		fake, client := fakeWorkspaceDatabase(t, workspaceName)
		credentialsTest.database = func(context.Context, string, oauth2.TokenSource) (*rtdb.Client, error) {
			return client, nil
		}
		store, err := workspace.NewRosterStore(client, workspaceName)
		if err != nil {
			t.Fatal(err)
		}
		fake.DenyRead(store.Path())

		_, err = credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
		if err == nil || ExitCode(err) != ExitRemoteError {
			t.Fatalf("err = %v, want a remote error: a roster nobody could read has no people to serve, and a service that started anyway would answer every grant of this workspace with a refusal it cannot explain (the published node's own read above is refused the same way, out of the same database)", err)
		}
	})
}

// TestWorkspaceModeWithoutARosterGrantsNothing is the other half of WC14's
// acceptance: a workspace whose owner has published no roster is a process that
// STARTS and whose scope serves no grant and no personal candidate, which is
// today's behaviour (C2's "What each side may rely on", WC14's paragraph). It
// is not a failure to start, and it is not the same state as a roster that
// cannot be read — the test above pins that one.
//
// The workspace's published credentials node IS there, so the process reaches
// the personal half's own read rather than refusing on the node above it, and
// the assertions WS5 makes about that node's half still hold: the resolver is
// built and the Server accepts the configuration.
func TestWorkspaceModeWithoutARosterGrantsNothing(t *testing.T) {
	const workspaceName = "aurora-ws"
	before := credsvc.PersonScopeConfigForServe
	t.Cleanup(func() { credsvc.PersonScopeConfigForServe = before })
	// A launcher class left over from another test would make this test pass
	// for the wrong reason, so the variable is cleared before the process runs:
	// what is asserted below is that THIS process left the zero configuration.
	credsvc.PersonScopeConfigForServe = credsvc.PersonScopeConfig{}

	for n, v := range workspaceServeEnv(workspaceName) {
		t.Setenv(n, v)
	}
	fakeCredentialsService(t)
	fake, client := fakeWorkspaceDatabase(t, workspaceName)
	credentialsTest.database = func(context.Context, string, oauth2.TokenSource) (*rtdb.Client, error) {
		return client, nil
	}
	// The database really is one the roster could have been published in, and
	// the node this test does not publish is absent rather than unreadable: a
	// test that proved "no roster" from a database that was never reachable
	// would prove nothing.
	store, err := workspace.NewRosterStore(client, workspaceName)
	if err != nil {
		t.Fatal(err)
	}
	if fake.Value(store.Path()) != nil {
		t.Fatalf("%s already holds a roster, and this test is about a workspace whose owner has published none", store.Path())
	}

	cfg, err := credentialsServiceConfig(context.Background(), os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("credentialsServiceConfig for a workspace with no roster node: %v (a workspace that published no roster is a state of the deployment and not a failure to start)", err)
	}
	if cfg.Resolver == nil {
		t.Fatal("the workspace's configuration handed the Server no resolver: the personal half is additive, and a workspace with no roster still serves everything the node above it binds")
	}
	person := credsvc.PersonScopeConfigForServe
	if person.Roster != nil || person.Launchers != nil || person.Slips != nil {
		t.Fatalf("the process handed the scope Roster=%v Launchers=%v Slips=%v for a workspace with no roster node: C2's zero configuration is the state every deployment is in until one is wired, and half of one is a scope that would read a roster it was never handed", person.Roster != nil, person.Launchers != nil, person.Slips != nil)
	}
	if _, err := credsvc.NewServer(cfg); err != nil {
		t.Fatalf("NewServer refused the configuration of a workspace with no roster node: %v", err)
	}
}
