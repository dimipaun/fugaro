package credsvc

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/identitywire"
)

// testRosterSA is the roster's service account in these tests (D3123): a
// `serviceAccount:` member of an installation's roster, which launches runs
// through the CLI's own gate today (F4) and whose class at the issuer is what
// LM3 adds. It is a name and not a credential, like every identity in these
// files, and it is deliberately not one of the fixture's other service accounts
// — a job's, a shared job's, the daemon's — because NewAuthenticator refuses a
// roster service account that is one of those, and this one has to be able to
// be a class of its own.
const testRosterSA = "fugaro-pipeline@aurora-dev.iam.gserviceaccount.com"

// testGroupAddress is the address a roster `group:` entry would name, which the
// ticket route may never admit: a group holds no token to present, so no caller
// can be a group, and `update` names such entries before it moves the grant
// (D3123, D3126). It is here to be presented — the one thing a group cannot do
// — so that "a group is not a class" is a fact a test holds and not a reading
// of the code.
const testGroupAddress = "team-launchers@example.invalid"

// errKeyEndpoint is a key set that cannot answer at all (errKeys): the shape of
// Google's key endpoint being unreachable, which is the service's own failure
// and not a caller's refusal.
var errKeyEndpoint = errors.New("the key set could not be read")

// testCallerSub is the account id the fixture's caller tokens name: jobClaims'
// own `sub` claim, which is what every class of this service answers with
// (launcherSub). The launcher class's tests replace it with the launcher's own
// (launcherToken), and a service-account token keeps it.
const testCallerSub = "1234567890"

// ticketCallerFixture is the fixture with the ticket route's three classes
// wired (LM3): the roster's people and its service accounts, and the resident
// orchestrator's own account. The three lists are disjoint, which is what
// NewAuthenticator's own rules keep true — a person may not be a service
// account address, and a roster service account may not be a run's own account
// or the daemon's — so a caller of this fixture is in exactly one class.
func ticketCallerFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWith(t, func(cfg *AuthConfig) {
		cfg.LauncherAccounts = []string{testLauncherAccount}
		cfg.LauncherServiceAccounts = []string{testRosterSA}
		cfg.OrchestratorAccounts = []string{testOrchestratorSA}
	})
}

// serviceAccountToken is the Google ID token a service-account caller presents:
// the caller's-token shape with the account's own address, the fixture's
// numeric sub, and the audience a service account's token is obtained with —
// this service's own URL. It is jobToken with the address replaced, because
// that is exactly what the difference is: every class of this service reads the
// same token shape, and which class a caller is in is the list and the entry
// point that admitted it.
func (f *fixture) serviceAccountToken(email string, mutate ...func(map[string]any)) string {
	return f.jobToken(append([]func(map[string]any){func(c map[string]any) { c["email"] = email }}, mutate...)...)
}

// launcherClass is the class's own construction over the fixture's key set and
// clock: the intake's Launchers, which holds no store and no run identity, with
// the fixture's instant for its clock so that a token the fixture minted at
// that instant is judged there and not against the wall clock.
func launcherClass(f *fixture, cfg LauncherConfig) (*Launchers, error) {
	cfg.Keys = f.jobKeys
	cfg.Audience = testAudience
	cfg.Now = func() time.Time { return f.now }
	return NewLaunchers(cfg)
}

// TestCallerClassesAdmitPersonServiceAccountOrchestrator is LM3's first fact:
// the ticket route admits a roster person, a roster service account and the
// orchestrator's account, each by its OWN verified Google ID token, and names
// the class it admitted each as — which is what the issuance record's `class`
// is written from (R3) and what tells the classes apart where their checks
// differ (LM23 judges the person, D3137 leaves the roster's service account
// unscoped, and LM24 judges the orchestrator by its own record).
//
// The last thing it holds is that the three are the CONTRACT's three: the
// classes this check admits are read back against identitywire's own table for
// the ticket path, so a class added to one side and not the other fails here
// rather than being served to a caller the contract does not admit, or refused
// where it does.
func TestCallerClassesAdmitPersonServiceAccountOrchestrator(t *testing.T) {
	f := ticketCallerFixture(t)
	c, err := NewIdentityCallers(f.auth)
	if err != nil {
		t.Fatalf("NewIdentityCallers: %v", err)
	}
	ctx := context.Background()

	admitted := make([]identitywire.CallerClass, 0, 3)
	for _, tc := range []struct {
		name string
		tok  string
		want IdentityCaller
	}{
		{
			// A person's token is the launcher class's token: the roster's
			// `user:` entries are verified by VerifyLauncher, and the audience
			// is gcloud's own client id, which is the only one a user
			// account's credentials can mint at all (GcloudAudience).
			name: "a person on the roster",
			tok:  f.launcherToken(),
			want: IdentityCaller{Address: testLauncherAccount, Class: identitywire.ClassPerson},
		},
		{
			// A roster service account's token is minted for this service's
			// own URL, which is the only audience its class admits: its token
			// is obtained on purpose rather than produced by a person's
			// ambient credentials.
			name: "a service account on the roster",
			tok:  f.serviceAccountToken(testRosterSA),
			want: IdentityCaller{Address: testRosterSA, Class: identitywire.ClassServiceAccount},
		},
		{
			// The daemon's own account, admitted by its own entry point
			// (VerifyOrchestrator) over its own list.
			name: "the resident orchestrator",
			tok:  f.serviceAccountToken(testOrchestratorSA),
			want: IdentityCaller{Address: testOrchestratorSA, Class: identitywire.ClassOrchestrator},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.TicketCaller(ctx, tc.tok)
			if err != nil {
				t.Fatalf("TicketCaller refused %s, which the ticket route admits: %v", tc.name, err)
			}
			if got != tc.want {
				t.Errorf("TicketCaller = %+v, want %+v: the address is the configuration's own spelling and the class is the entry point that admitted it", got, tc.want)
			}
		})
		admitted = append(admitted, tc.want.Class)
	}

	route, ok := identitywire.RouteFor(identitywire.TicketPath)
	if !ok {
		t.Fatalf("the contract holds no route at %s", identitywire.TicketPath)
	}
	want := slices.Clone(route.Admits)
	slices.Sort(want)
	slices.Sort(admitted)
	if !slices.Equal(admitted, want) {
		t.Errorf("the ticket route's classes are %v and the check admits %v: a class on one side and not the other is a route serving a caller the contract does not admit, or refusing one it does", want, admitted)
	}
}

// TestCallerRefusesJobAccountAndUnlisted is LM3's second fact, and it is the
// other half of "who may ask": an address on no roster is refused, a run's own
// job account is refused, and a `group:` address is refused — the last of them
// because a group holds no token to present, so the address a group would be
// named by is an address no roster class can match.
//
// The refusal is one code (ErrCallerNotAdmitted,
// identitywire.RefusalCallerNotAdmitted) for all of them: a caller that is on
// none of the three lists learns nothing from being told which list it is
// missing from, and the contract's own reading of the codes is that a code a
// caller may act on differently would have to be a decision. What the test also
// holds is that the refusal repeats no address (the package's no-echo rule: the
// address is the caller's own claim set) and that a class which could not
// answer AT ALL is not read as one of these refusals — a caller must never be
// told it is unadmitted because the service is unwell.
func TestCallerRefusesJobAccountAndUnlisted(t *testing.T) {
	f := ticketCallerFixture(t)
	c, err := NewIdentityCallers(f.auth)
	if err != nil {
		t.Fatalf("NewIdentityCallers: %v", err)
	}
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		tok  string
	}{
		{
			// The run's own identity: it is the CLAIM route's class, and a job
			// account asking for a ticket is the one ask the claim route
			// exists to answer instead (R4).
			name: "a job account of this installation",
			tok:  f.jobToken(),
		},
		{
			name: "an account on no roster",
			tok:  f.serviceAccountToken("stranger@another-project.iam.gserviceaccount.com"),
		},
		{
			name: "a person on no roster",
			tok:  f.launcherToken(func(c map[string]any) { c["email"] = "stranger@example.invalid" }),
		},
		{
			// The address a `group:` entry names, presented as a token's email
			// claim: nothing can present it in truth — a group holds no token
			// — and the class it is refused by is the same one every other
			// address on no list is refused by.
			name: "a group's address",
			tok:  f.serviceAccountToken(testGroupAddress),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.TicketCaller(ctx, tc.tok)
			if !errors.Is(err, ErrCallerNotAdmitted) {
				t.Fatalf("TicketCaller answered %+v/%v for %s, want ErrCallerNotAdmitted", got, err, tc.name)
			}
			if got != (IdentityCaller{}) {
				t.Errorf("the refusal carries a caller: %+v", got)
			}
			for _, address := range []string{testJobSA, testGroupAddress, "stranger@another-project.iam.gserviceaccount.com", "stranger@example.invalid"} {
				if strings.Contains(err.Error(), address) {
					t.Errorf("the refusal is %q, and it repeats an address the caller presented", err)
				}
			}
		})
	}

	// A class that could not answer is the service's own failure and stops the
	// check: with a key set that cannot be read, the person class's own error
	// comes back as it stands rather than as "not admitted" (the rule the
	// issuance store's errors are told apart by, R16's ScopeFault rule).
	broken := newFixtureWith(t, func(cfg *AuthConfig) {
		cfg.LauncherAccounts = []string{testLauncherAccount}
		cfg.JobKeys = errKeys{err: errKeyEndpoint}
	})
	bc, err := NewIdentityCallers(broken.auth)
	if err != nil {
		t.Fatalf("NewIdentityCallers: %v", err)
	}
	if _, err := bc.TicketCaller(ctx, broken.launcherToken()); !errors.Is(err, errKeyEndpoint) {
		t.Errorf("a caller whose key set could not be read was answered %v, want the key source's own failure and not a refusal of the caller", err)
	}
}

// TestServiceAccountLauncherIsItsOwnClass is LM3's third fact and its red step:
// the roster's service accounts are a class of their own BESIDE the launcher
// class, with their own entry point, their own list and their own audience —
// and the launcher class is not widened by it, because its class is people and
// it refuses the service-account shape by name (D1938, G13, F4).
//
// The red step is the first block: before this task there was no entry point to
// admit a roster service account at all, and the construction refused the
// address — NewLaunchers, the intake's own, is the one the plan names. That
// refusal is still the rule for the launcher list (a service account is not a
// person), so what the class adds is the second list and the second entry point
// and not a widening of the first.
//
// What the two entry points buy is that a caller's class is the entry point
// that admitted it: the roster's service-account class refuses a person's
// token, the launcher class refuses a service account's, and the two lists
// cannot hold one address because each refuses the other's shape.
func TestServiceAccountLauncherIsItsOwnClass(t *testing.T) {
	f := ticketCallerFixture(t)
	ctx := context.Background()
	personToken := f.launcherToken()
	serviceAccountToken := f.serviceAccountToken(testRosterSA)

	// The launcher list is people, and it refuses the shape — the red step's
	// own refusal, kept.
	if _, err := launcherClass(f, LauncherConfig{Accounts: []string{testRosterSA}}); err == nil || !strings.Contains(err.Error(), "service account address") {
		t.Errorf("NewLaunchers answered %v for a service account on the launcher list, want the configuration error the class's own rule writes", err)
	}
	// The class's list refuses the other shape, which is what makes the two
	// lists disjoint rather than merely different.
	if _, err := launcherClass(f, LauncherConfig{ServiceAccounts: []string{testLauncherAccount}}); err == nil || !strings.Contains(err.Error(), "not a service account address") {
		t.Errorf("NewLaunchers answered %v for a person on the roster service-account list, want the configuration error the class's own rule writes", err)
	}

	people, err := launcherClass(f, LauncherConfig{Accounts: []string{testLauncherAccount}})
	if err != nil {
		t.Fatalf("the launcher class cannot be configured: %v", err)
	}
	accounts, err := launcherClass(f, LauncherConfig{ServiceAccounts: []string{testRosterSA}})
	if err != nil {
		t.Fatalf("the roster service-account class cannot be configured: %v", err)
	}

	got, err := accounts.VerifyServiceAccountLauncher(ctx, serviceAccountToken)
	if err != nil {
		t.Fatalf("the roster service-account class refused its own caller: %v", err)
	}
	if got.Account != testRosterSA || got.Sub != testCallerSub {
		t.Errorf("the class answered %+v, want the configured address and the token's own account id", got)
	}
	if _, err := accounts.VerifyServiceAccountLauncher(ctx, personToken); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("the roster service-account class admitted a person's token: %v", err)
	}
	if _, err := people.VerifyLauncher(ctx, serviceAccountToken); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("the launcher class admitted a roster service account's token, so the two entry points are one list read twice: %v", err)
	}
	if _, err := people.VerifyLauncher(ctx, personToken); err != nil {
		t.Errorf("the launcher class refused its own caller: %v", err)
	}

	// The audience is the class's own line: a service account's token is
	// obtained on purpose with the audience asked for, so the audience a
	// person's ambient credentials mint (GcloudAudience) is refused here —
	// though the launcher class admits exactly that audience for a person.
	gcloud := f.serviceAccountToken(testRosterSA, func(c map[string]any) { c["aud"] = GcloudAudience })
	if _, err := accounts.VerifyServiceAccountLauncher(ctx, gcloud); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("the roster service-account class admitted a token minted at gcloud's own client id: %v", err)
	}

	// An empty list is a statement rather than a missing gate, for this class
	// as for the launcher's: an installation whose roster names no service
	// account admits nobody as one.
	none, err := launcherClass(f, LauncherConfig{})
	if err != nil {
		t.Fatalf("an installation with no roster service accounts cannot be configured: %v", err)
	}
	if _, err := none.VerifyServiceAccountLauncher(ctx, serviceAccountToken); !errors.Is(err, ErrUnverifiedJob) {
		t.Errorf("an empty roster service-account list admitted a caller: %v", err)
	}
}

// TestRosterServiceAccountOverlapIsRefused is the other half of the class's
// being a class of its own (LM3, D3123): an address the roster names as a
// service account may not be one another class admits, because one caller would
// then be two classes and the class it is served as would be decided by which
// route read its token first.
//
// Two of these refusals are load-bearing beyond the class rule. A run's own
// identity — its job account, a shared project job's, a review job's — may
// never be a roster service account: the ticket route must refuse a job account
// (R4, because the claim route is the one that answers it), and a deployment
// that named one here would serve that run's own token a ticket at the route
// that exists to refuse it. The other three are the same rule one step out: the
// orchestrator's account and the two build identities are automations of their
// own routes, and an address on two of these lists is a deployment that
// disagrees with itself.
//
// Every row is a configuration that is valid except for its one overlap, so the
// refusal each row reads is the overlap rule's and not another list's rule
// firing first.
func TestRosterServiceAccountOverlapIsRefused(t *testing.T) {
	f := ticketCallerFixture(t)
	for _, tc := range []struct {
		name    string
		overlap func(cfg *AuthConfig)
	}{
		{
			name: "the run's own job account",
			overlap: func(cfg *AuthConfig) {
				cfg.LauncherServiceAccounts = []string{testJobSA}
			},
		},
		{
			name: "a shared project job's account",
			overlap: func(cfg *AuthConfig) {
				cfg.LauncherServiceAccounts = []string{testSharedSA}
			},
		},
		{
			name: "a review job's account",
			overlap: func(cfg *AuthConfig) {
				cfg.ReviewJobAccounts = []ReviewJobAccount{{Email: testReviewSA, Account: testReviewAccount}}
				cfg.LauncherServiceAccounts = []string{testReviewSA}
			},
		},
		{
			name: "the resident orchestrator",
			overlap: func(cfg *AuthConfig) {
				cfg.LauncherServiceAccounts = []string{testOrchestratorSA}
			},
		},
		{
			name: "the installation's catalog-build identity",
			overlap: func(cfg *AuthConfig) {
				cfg.BuildAccounts = []string{testBuildSA}
				cfg.LauncherServiceAccounts = []string{testBuildSA}
			},
		},
		{
			name: "a repository's build account",
			overlap: func(cfg *AuthConfig) {
				cfg.RepoBuildAccounts = []RepoBuildAccount{{
					Email:    testBuildSA,
					Slug:     testSlug(t, testRepoPath),
					Repo:     testRepo,
					Provider: gitprov.KindGitHub,
				}}
				cfg.LauncherServiceAccounts = []string{testBuildSA}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := f.auth.cfg
			tc.overlap(&cfg)
			if _, err := NewAuthenticator(cfg); err == nil || !strings.Contains(err.Error(), "two classes") {
				t.Errorf("NewAuthenticator answered %v for a roster service account that is also %s, want the configuration error the overlap rule writes", err, tc.name)
			}
		})
	}
}
