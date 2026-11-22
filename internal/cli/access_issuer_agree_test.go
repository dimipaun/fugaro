package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	accesslib "github.com/dimipaun/fugaro/internal/access"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// This file is R16's own property, run: the launch-scope check the ticket route
// asks (internal/credsvc/identity_scope.go, LM23) gives the answer the CLI's
// launch gate gives, ARM BY ARM — one row per arm, each row built once and put
// to both sides, with the outage row the one that is meant to disagree.
//
// It lives here and not beside the check because the gate is unexported in this
// package, which is the side that imports the other: `launchPolicyGates` is
// what a launcher refuses a run with today, and it is a courtesy (a launcher
// can call jobs.run directly, and the launcher is the side a modified binary is
// on), while the issuer is the first server-side place that can enforce the
// same thing (D3130 as amended). A divergence between the two is then either a
// launch the gate refuses and the issuer signs — the hole the check exists to
// close — or a launch the gate permits and the issuer refuses, which stops a
// person's work for a rule nothing states. Neither may happen silently, and
// nothing but a table that runs both catches it.
//
// Both sides read ONE world: the same fixture database (the CLI through its own
// budget client, the issuer through credsvc.RTDBAccess and RTDBPolicy), the
// same configuration's implicit admins (the gate reads them itself and the
// check is handed what userImplicitAdmins answers), and the same published
// policy. What the rows vary is what the arms are about: the records, the
// allow-list, the caller's class and the contexts.

// agreeBoss is the address R16's first arm is about: the implicit admin the
// machine's configuration names, which `fugaro update` renders into the
// service's environment for the issuer to read.
const agreeBoss = "user:boss@example.com"

// agreeWorld is one row's installation and the ask put to both sides.
type agreeWorld struct {
	// grants are the records the fixture's database holds. Empty writes no
	// node, which is the installation whose lists have never been migrated —
	// unless migrated says otherwise.
	grants []accesslib.Grant
	// migrated writes the migration marker alone: a roster of nobody, which is
	// judged rather than skipped (access.PathMigrated).
	migrated bool
	// brokenNode writes a node that answers and cannot be held whole.
	brokenNode bool
	// policy is the shared node's own document. "" publishes nothing.
	policy string
	// implicit names the config's implicit admin, which is R16's first arm.
	implicit bool
	// unreachable points the fixture's config at a port nothing listens on.
	unreachable bool
	// serviceAccount asks as a roster service account's credential.
	serviceAccount bool
	// member is the credential the CLI judges with, in the member form
	// access.Caller answers. address is the same caller as the issuer sees it:
	// the verified address its own ID token carried.
	member  string
	address string
	class   identitywire.CallerClass
	// contextRepos are the repositories the ask names for the run to read.
	contextRepos []string
	// refused is what both sides must say. fault says the issuer's refusal is
	// the service's own failure (a *ScopeFault) rather than a refusal of the
	// caller, and difference marks R16's sixth arm, where the two sides are
	// meant to say different things.
	refused    bool
	fault      bool
	difference bool
	why        string
}

func TestIssuerAgreesWithTheCLIGate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		world agreeWorld
	}{
		{"a person the records place as an operator", agreeWorld{
			grants: []accesslib.Grant{userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA)},
			policy: `{"version":%d,"allow_repos":["*"]}`, member: accessOperator, address: "operator@example.com",
			class: identitywire.ClassPerson,
		}},
		{"a person the records place as a viewer", agreeWorld{
			grants: []accesslib.Grant{userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA)},
			policy: `{"version":%d,"allow_repos":["*"]}`, member: accessViewer, address: "viewer@example.com",
			class: identitywire.ClassPerson, refused: true,
		}},
		{"the owner the configuration names as an implicit admin", agreeWorld{
			grants: []accesslib.Grant{userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA)},
			policy: `{"version":%d,"allow_repos":["*"]}`, implicit: true,
			member: agreeBoss, address: "boss@example.com", class: identitywire.ClassPerson,
		}},
		{"a roster service account", agreeWorld{
			grants: []accesslib.Grant{userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA)},
			policy: `{"version":%d,"allow_repos":["*"]}`, serviceAccount: true,
			address: "fugaro-run@example.iam.gserviceaccount.com", class: identitywire.ClassServiceAccount,
		}},
		{"a service account's context repository", agreeWorld{
			grants:         []accesslib.Grant{userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA)},
			policy:         `{"version":%d,"allow_repos":["*"]}`,
			serviceAccount: true, contextRepos: []string{"acme/other"},
			address: "fugaro-run@example.iam.gserviceaccount.com", class: identitywire.ClassServiceAccount,
		}},
		{"a service account the published policy does not admit", agreeWorld{
			grants: []accesslib.Grant{userStored(t, accessViewer, accesslib.RoleViewer, accessRepoA)},
			policy: `{"version":%d,"allow_repos":["github/acme/sibling"]}`, serviceAccount: true,
			address: "fugaro-run@example.iam.gserviceaccount.com", class: identitywire.ClassServiceAccount,
			refused: true,
		}},
		{"an installation whose lists have never moved into a node", agreeWorld{
			policy: `{"version":%d,"allow_repos":["*"]}`, member: accessOperator, address: "operator@example.com",
			class: identitywire.ClassPerson,
		}},
		{"a roster of nobody", agreeWorld{
			migrated: true, policy: `{"version":%d,"allow_repos":["*"]}`,
			member: accessOperator, address: "operator@example.com",
			class: identitywire.ClassPerson, refused: true,
		}},
		{"a repository the published policy does not admit", agreeWorld{
			grants: []accesslib.Grant{userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA)},
			policy: `{"version":%d,"allow_repos":["github/acme/sibling"]}`,
			member: accessOperator, address: "operator@example.com",
			class: identitywire.ClassPerson, refused: true,
		}},
		{"a context repository the caller may not read", agreeWorld{
			grants: []accesslib.Grant{userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA)},
			policy: `{"version":%d,"allow_repos":["*"]}`, contextRepos: []string{"acme/other"},
			member: accessOperator, address: "operator@example.com",
			class: identitywire.ClassPerson, refused: true,
		}},
		{"a context repository the caller may read", agreeWorld{
			grants: []accesslib.Grant{
				userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA),
				userStored(t, accessOperator, accesslib.RoleViewer, accessRepoB),
			},
			policy: `{"version":%d,"allow_repos":["*"]}`, contextRepos: []string{"acme/other"},
			member: accessOperator, address: "operator@example.com", class: identitywire.ClassPerson,
		}},
		{"a context repository the published policy does not admit", agreeWorld{
			grants:       []accesslib.Grant{userStored(t, accessOperator, accesslib.RoleOperator, "all-repos")},
			policy:       `{"version":%d,"allow_repos":["github/acme/app"]}`,
			contextRepos: []string{"acme/other"},
			member:       accessOperator, address: "operator@example.com",
			class: identitywire.ClassPerson, refused: true,
		}},
		{"a context repository with no policy published", agreeWorld{
			grants:       []accesslib.Grant{userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA)},
			contextRepos: []string{"acme/other"},
			member:       accessOperator, address: "operator@example.com",
			class: identitywire.ClassPerson, refused: true,
		}},
		{"a roles node that does not decode", agreeWorld{
			brokenNode: true, policy: `{"version":%d,"allow_repos":["*"]}`,
			member: accessOperator, address: "operator@example.com",
			class: identitywire.ClassPerson, refused: true, fault: true,
		}},
		{"a policy node that does not decode", agreeWorld{
			grants: []accesslib.Grant{userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA)},
			policy: `"not a policy"`,
			member: accessOperator, address: "operator@example.com",
			class: identitywire.ClassPerson, refused: true, fault: true,
		}},
		{"the records cannot be reached", agreeWorld{
			grants: []accesslib.Grant{userStored(t, accessOperator, accesslib.RoleOperator, accessRepoA)},
			policy: `{"version":%d,"allow_repos":["*"]}`, unreachable: true,
			member: accessOperator, address: "operator@example.com",
			class: identitywire.ClassPerson,
			// R16's sixth arm, and the whole of the difference: the gate does
			// not judge and the launch goes on (an outage is not a statement
			// about the caller, and the launch meets the same outage at its
			// own budget read where a run really cannot start without an
			// answer), while the issuer refuses, because it cannot write the
			// issuance record there either.
			difference: true, fault: true,
			why: "the gate does not judge an unreachable roles database and the issuer refuses it (R16's sixth arm)",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noAgentSession(t)
			f := newLaunchBudget(t, "off")
			w := tc.world
			if w.implicit {
				f.appendConfig(t, "terraform:\n  budget_admins: ['"+agreeBoss+"']\n")
			}
			switch {
			case w.brokenNode:
				f.db.Set(accesslib.PathNode, map[string]any{"grants": "not-a-list"})
			case len(w.grants) > 0:
				accessRunNode(t, f, w.grants...)
			case w.migrated:
				f.db.Set(accesslib.PathMigrated, true)
			}
			if w.policy != "" {
				publishPolicy(t, f, strings.ReplaceAll(w.policy, "%d", itoa(projectpolicy.Version)))
			}
			if w.unreachable {
				accessRunUnreachableDB(t, f)
			}
			if w.serviceAccount {
				accessRunAsServiceAccount(t)
			} else {
				accessRunAs(t, w.member)
			}

			ctx := context.Background()
			env := fileEnv(t, f.cloudFixture)
			spec := accessRunSpec("acme/app")
			spec.ContextRepos = w.contextRepos

			// The gate, and for the one row where the gate never reaches its
			// scope check, the scope check's own arm: a launch meets an
			// unreachable roles database at its policy read one step later and
			// fails there for its own reasons, which is not what this row is
			// about (access_run_gate_test.go observes the same outage the same
			// way).
			var gateErr error
			if w.unreachable {
				gateErr = requireAccess(ctx, env, accesslib.ActionLaunch, accessRepoA, "nothing was launched")
			} else {
				gateErr = launchPolicyGates(ctx, env, appSlug, spec, "web", false, false, io.Discard)
			}

			ask := identitywire.TicketRequest{
				Slug: appSlug, Run: budgetRun, Branch: "main", MaxRuntimeSeconds: 3600,
				Account: "fugaro-run@example.iam.gserviceaccount.com", ContextRepos: w.contextRepos,
			}
			issuerErr := agreeCheck(t, f, env, w).Check(ctx,
				credsvc.IdentityCaller{Address: w.address, Class: w.class}, ask)

			gateRefused, issuerRefused := gateErr != nil, issuerErr != nil
			// What the row says the gate does is asserted first: a row is one
			// arm of R16 as the gate implements it, and a row whose world has
			// drifted so far that the gate now answers the other way would make
			// "the two agree" true about the wrong thing.
			want := "admits"
			if w.refused {
				want = "refuses"
			}
			if gateRefused != w.refused {
				t.Fatalf("the gate refused = %v (%v); this row is one the gate %s (R16), so the table no longer describes the gate", gateRefused, gateErr, want)
			}
			if w.difference && !issuerRefused {
				t.Fatalf("%s: the issuer admitted it (gate = %v): want the issuer to refuse", w.why, gateErr)
			}
			if !w.difference && gateRefused != issuerRefused {
				t.Fatalf("the gate refused = %v (%v) and the issuer refused = %v (%v): the two must give one answer (R16)", gateRefused, gateErr, issuerRefused, issuerErr)
			}
			switch {
			case w.fault:
				// A world the SERVICE cannot read: the issuer's refusal is its
				// own failure and must never be answered as a caller who is not
				// admitted (R16, ScopeFault: a run's gateway halts a 403 as a
				// missing invoke grant), and the route writes its audit line on
				// that answer and not on the caller's.
				var fault *credsvc.ScopeFault
				if !errors.As(issuerErr, &fault) {
					t.Fatalf("the issuer's refusal = %v, want a *ScopeFault: the service could not judge this installation, which is not a caller who is not admitted", issuerErr)
				}
				if errors.Is(issuerErr, credsvc.ErrNoLaunchScope) {
					t.Errorf("the fault carries the caller's own refusal code, which the route would answer as a caller refusal: %v", issuerErr)
				}
			case issuerRefused && !errors.Is(issuerErr, credsvc.ErrNoLaunchScope):
				t.Errorf("the issuer's refusal = %v, want ErrNoLaunchScope: the route answers the code and writes one audit line", issuerErr)
			}
			// What the CLI reads as a refusal is a refusal this build words.
			// The gate's own rows here are user errors and remote failures (a
			// node it cannot hold whole is remote: dbErr), and neither is
			// ExitOK — a refusal that exits 0 would be a launch that goes on.
			if gateRefused && ExitCode(gateErr) == ExitOK {
				t.Errorf("the gate's refusal exits 0: %v", gateErr)
			}
		})
	}
}

// agreeCheck is the issuer side of one row: the check over the SAME
// installation the gate was just run against, through the real readers (the
// records and the policy over one database client, which is what production
// does with its own account).
//
// The implicit admins are the gate's own read of the machine's configuration
// (userImplicitAdmins), which is R16's first arm seen from both ends: the
// launcher reads them from its own config, and `fugaro update` renders exactly
// those to the service (LM12, LM22). A row here is therefore about the wiring
// as much as about the judgement — an issuer handed some other list would
// refuse an owner the gate admits, and that is a row below.
func agreeCheck(t *testing.T, f *launchBudgetFixture, env *cloudEnv, w agreeWorld) credsvc.ScopeCheck {
	t.Helper()
	url := f.db.URL
	if w.unreachable {
		url = "http://127.0.0.1:1"
	}
	db, err := rtdb.New(url, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	return credsvc.NewLaunchScope(credsvc.LaunchScopeConfig{
		Repos:          map[string]string{appSlug: accessRepoA},
		Records:        credsvc.RTDBAccess{DB: db},
		Policy:         credsvc.RTDBPolicy{DB: db},
		ImplicitAdmins: userImplicitAdmins(env.lc),
	})
}
