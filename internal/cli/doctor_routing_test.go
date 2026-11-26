package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// doctorRoutingRepo is the project's own repository, in the local config's
// spelling: the table's admit lists name a repository that way, and the bundle
// entry a source is served by is derived from the provider recorded beside it
// (task.Slug).
const doctorRoutingRepo = "repos:\n  acme/app: { provider: github, github_app_id: \"12345\", workflows: [app] }\n"

// doctorRoutingServes is a table that governs this project's repositories: its
// one source admits acme/app, which is what puts the table in force for their
// runs at all (routingTableGoverns). Every other table here is read beside it,
// because a table that serves nobody is a project on the bridge and reports
// nothing.
const doctorRoutingServes = `model_routing:
  - match: claude-*
    chain:
      - kind: anthropic
        admit: [acme/app]
`

// doctorRoutingEmptyChain is the table RT3 refuses whatever model it answers:
// one entry names no source at all, beside the one that serves the project.
const doctorRoutingEmptyChain = doctorRoutingRepo + `model_routing:
  - match: claude-haiku-4-5
    chain: []
  - match: claude-*
    chain:
      - kind: anthropic
        admit: [acme/app]
`

// doctorRoutingLoneToken is a project whose one subscription token is in no
// pool: no entry of the table can be served by it, and its quota is spent by
// nothing (X2, reported in release 1).
const doctorRoutingLoneToken = doctorRoutingRepo + `credentials:
  - name: claude-work
    type: claude-oauth
` + doctorRoutingServes

// doctorRoutingCredentialed is this project with one pool, holding its one
// subscription token: the state a lone token is reported against.
const doctorRoutingCredentialed = doctorRoutingRepo + `credentials:
  - name: claude-work
    type: claude-oauth
pools:
  - name: team
    strategy: failover
    members: [claude-work]
`

// doctorRoutingSources is a table that goes out on the two credentials the
// bundle check reads: the project's Anthropic key and a provider's key, each
// admitted to this one repository.
const doctorRoutingSources = doctorRoutingRepo + `providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["deepseek/*"]
model_routing:
  - match: claude-*
    chain:
      - kind: anthropic
        admit: [acme/app]
      - kind: provider
        provider: openrouter
        admit: [acme/app]
`

// doctorRoutingLauncherPool is a chain that goes out on the launcher's pool,
// which is what the project has to declare a pool for: a launcher-pool source
// names no pool of its own (D3176).
const doctorRoutingLauncherPool = doctorRoutingRepo + `model_routing:
  - match: claude-*
    chain:
      - kind: launcher-pool
      - kind: anthropic
        admit: [acme/app]
`

// doctorRoutingLauncherPoolDeclared is that chain on a project that declares
// pools: there is one for a designation to name, which is all doctor can answer
// about it from here (the store is PL4's).
const doctorRoutingLauncherPoolDeclared = doctorRoutingCredentialed + `model_routing:
  - match: claude-*
    chain:
      - kind: launcher-pool
      - kind: pool
        pool: team
`

// doctorRoutingPausing is a table that can pause: it goes out on a pool, and a
// pool is the kind that runs out at a reset rather than at a balance.
const doctorRoutingPausing = doctorRoutingCredentialed + `model_routing:
  - match: claude-*
    chain:
      - kind: pool
        pool: team
`

// doctorRoutingRig is doctor's rig for a project's own routing table: the
// ordinary installation with this project's one repository and the blocks the
// sub-test is about.
func doctorRoutingRig(t *testing.T, lines string) *doctorRig {
	t.Helper()
	r := newDoctorRig(t)
	addLocalLines(t, r, lines)
	return r
}

// doctorRoutingOrchestratorRig is the same rig on an installation that records
// both halves a resident orchestrator needs — the credentials service and the
// Firebase budget backend — which is the state where the orchestrator's own
// block is something `fugaro update` can change rather than a fact about the
// installation.
func doctorRoutingOrchestratorRig(t *testing.T, lines string) *doctorRig {
	t.Helper()
	b := testutil.NewBudget(t)
	b.DB.Set("fugaro/project", "aurora")
	b.DB.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
	gen, err := rules.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b.DB.SetRules(gen)
	return doctorRoutingRig(t, "credentials_service: { url: https://fugaro-credentials-abc-uc.a.run.app }\n"+
		"budget: { mode: enforce, per_run_usd: 5, firebase_project: proj-1234, firebase_api_key: "+
		testutil.BudgetAPIKey+", rtdb_url: "+b.URL()+", token_signer: "+testutil.BudgetSigner+" }\n"+lines)
}

// doctorRoutingOutput runs doctor over the rig's installation and answers the
// parsed document beside the bytes it read, so a failure can print the run.
func doctorRoutingOutput(t *testing.T) (doctorOutput, string) {
	t.Helper()
	out, _, _ := execute(t, "doctor", "--json", "--dir", t.TempDir())
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, out)
	}
	return o, out
}

// doctorRoutingEntry is the bundle key one repository's credential is held
// under, read from the rule that names it rather than spelled here.
func doctorRoutingEntry(t *testing.T, slug, credential string) string {
	t.Helper()
	key, err := credsvc.RepoCredentialKey(slug, credential)
	if err != nil {
		t.Fatalf("credsvc.RepoCredentialKey(%s, %s): %v", slug, credential, err)
	}
	return key
}

// TestDoctorRouting is RT7's own reading of the project's routing table: one
// sub-test per report, plus the one report it no longer makes (the repository's
// auth mode).
func TestDoctorRouting(t *testing.T) {
	// The red step: a table with an entry that reaches nothing. It fails doctor
	// and not --strict alone, because a launch of this project is refused on it
	// (R22).
	t.Run("table_problem", func(t *testing.T) {
		doctorRoutingRig(t, doctorRoutingEmptyChain)
		o, out := doctorRoutingOutput(t)
		cs := checksByID(o, doctorRoutingTableID)
		if len(cs) != 1 {
			t.Fatalf("routing-table emitted %d check(s), want the entry that names no source:\n%s", len(cs), out)
		}
		c := cs[0]
		if c.OK || c.Severity != "" {
			t.Errorf("routing-table = %+v, want a failure and not a warning: a launch is refused on it", c)
		}
		if !strings.Contains(c.Problem, "names no source at all") {
			t.Errorf("the line does not say what is wrong with the entry: %q", c.Problem)
		}
		if !strings.Contains(c.Fix, "routing set") {
			t.Errorf("the fix does not name the command that writes the entry: %q", c.Fix)
		}
		if !checksFail(cs, false) {
			t.Error("an entry that reaches nothing does not fail doctor")
		}
	})

	// X2's report: a subscription token in no pool, which release 1 reports and
	// refuses nothing about (D3176, PL1).
	t.Run("subscription_token_in_no_pool", func(t *testing.T) {
		doctorRoutingRig(t, doctorRoutingLoneToken)
		o, out := doctorRoutingOutput(t)
		cs := checksByID(o, doctorRoutingLoneSubscriptionID)
		if len(cs) != 1 {
			t.Fatalf("routing-lone-subscription emitted %d check(s), want one:\n%s", len(cs), out)
		}
		c := cs[0]
		if c.OK || c.Severity != "warning" {
			t.Errorf("routing-lone-subscription = %+v, want a warning", c)
		}
		if !strings.Contains(c.Problem, "claude-work") || !strings.Contains(c.Problem, "in no pool") {
			t.Errorf("the line does not name the token and the state: %q", c.Problem)
		}
		if !strings.Contains(c.Fix, "accounts pool add") {
			t.Errorf("the fix does not name the command that puts it in one: %q", c.Fix)
		}
		// Reported and not refused: it is an item of --strict, and doctor itself
		// passes.
		if checksFail(cs, false) {
			t.Error("a token in no pool fails doctor, and release 1 reports it without refusing anything")
		}
		if !checksFail(cs, true) {
			t.Error("a token in no pool does not fail doctor under --strict")
		}
	})

	// The bundle's half: whether this installation holds the secret each source
	// of the table is served by.
	t.Run("source_missing_from_the_bundle", func(t *testing.T) {
		t.Run("the bundle holds neither", func(t *testing.T) {
			r := doctorRoutingRig(t, doctorRoutingSources)
			// A bundle that holds a credential of the project and neither of
			// the two the table goes out on: the state an owner cannot see from
			// either side alone.
			bundleFixtureFor(t, r.sm, "aurora", []byte(config.BitbucketSecret+": "+credsValue(t, "token")+"\n"))

			o, out := doctorRoutingOutput(t)
			cs := checksByID(o, doctorRoutingBundleID)
			if len(cs) != 2 {
				t.Fatalf("routing-bundle-source emitted %d check(s), want one per source of the chain:\n%s", len(cs), out)
			}
			problems, fixes := "", ""
			for _, c := range cs {
				if c.OK || c.Severity != "warning" {
					t.Errorf("routing-bundle-source = %+v, want a warning", c)
				}
				problems += c.Problem + "\n"
				fixes += c.Fix + "\n"
			}
			for _, want := range []string{"the project's Anthropic key", "the openrouter key", "acme/app"} {
				if !strings.Contains(problems, want) {
					t.Errorf("the lines do not name %s:\n%s", want, problems)
				}
			}
			for _, want := range []string{
				"update copies the repository's own Anthropic key",
				"secrets set openrouter --repo acme/app",
			} {
				if !strings.Contains(fixes, want) {
					t.Errorf("the fixes do not carry %q:\n%s", want, fixes)
				}
			}
			if checksFail(cs, false) {
				t.Error("a source the bundle does not hold fails doctor, and a run falls through to the chain's next source")
			}
		})

		t.Run("the bundle holds both", func(t *testing.T) {
			r := doctorRoutingRig(t, doctorRoutingSources)
			slug := mustSlug("github", "acme/app")
			doc := config.BitbucketSecret + ": " + credsValue(t, "token") + "\n" +
				copyEntryDoc(doctorRoutingEntry(t, slug, config.AnthropicKeyAccount), credsValue(t, "own"), anthropicAPIURL, providerAuthXAPIKey, credsvc.ProviderKeySource{}) +
				copyEntryDoc(doctorRoutingEntry(t, slug, "openrouter"), credsValue(t, "provider"), "https://openrouter.ai/api", providerAuthBearer, credsvc.ProviderKeySource{})
			bundleFixtureFor(t, r.sm, "aurora", []byte(doc))

			o, out := doctorRoutingOutput(t)
			cs := checksByID(o, doctorRoutingBundleID)
			if len(cs) != 1 || !cs[0].OK || cs[0].Severity != "info" {
				t.Fatalf("routing-bundle-source = %+v, want one measurement:\n%s", cs, out)
			}
			if !strings.Contains(cs[0].Problem, "holds every one of them") {
				t.Errorf("the measurement does not say the bundle holds them: %q", cs[0].Problem)
			}
		})
	})

	// The launcher's pool where the project declares none: no designation could
	// name a pool the project does not have (D3176, D3180).
	t.Run("launcher_pool_with_no_pool", func(t *testing.T) {
		doctorRoutingRig(t, doctorRoutingLauncherPool)
		o, out := doctorRoutingOutput(t)
		cs := checksByID(o, doctorRoutingLauncherPoolID)
		if len(cs) != 1 {
			t.Fatalf("routing-launcher-pool emitted %d check(s), want one:\n%s", len(cs), out)
		}
		c := cs[0]
		if c.OK || c.Severity != "warning" {
			t.Errorf("routing-launcher-pool = %+v, want a warning", c)
		}
		if !strings.Contains(c.Problem, "launcher's pool") || !strings.Contains(c.Problem, "declares no pool") {
			t.Errorf("the line does not name the source and the state: %q", c.Problem)
		}
		if !strings.Contains(c.Fix, "accounts pool add") {
			t.Errorf("the fix does not name the command that declares one: %q", c.Fix)
		}
		if checksFail(cs, false) {
			t.Error("the chain falls through to the next source, so the state is a warning and not a failure")
		}
	})

	t.Run("a project that declares pools", func(t *testing.T) {
		doctorRoutingRig(t, doctorRoutingLauncherPoolDeclared)
		o, out := doctorRoutingOutput(t)
		if cs := checksByID(o, doctorRoutingLauncherPoolID); len(cs) != 0 {
			t.Errorf("routing-launcher-pool = %+v, want none: the project has a pool a designation could name (the store is PL4's):\n%s", cs, out)
		}
	})

	// The pause: a table that goes out on a pool where nothing would resume a
	// run that paused on it (D3186).
	t.Run("pause_with_no_orchestrator", func(t *testing.T) {
		t.Run("the installation records none", func(t *testing.T) {
			doctorRoutingOrchestratorRig(t, doctorRoutingPausing)
			o, out := doctorRoutingOutput(t)
			cs := checksByID(o, doctorRoutingPauseID)
			if len(cs) != 1 {
				t.Fatalf("routing-pause-orchestrator emitted %d check(s), want one:\n%s", len(cs), out)
			}
			c := cs[0]
			if c.OK || c.Severity != "warning" {
				t.Errorf("routing-pause-orchestrator = %+v, want a warning: an update gives the installation one", c)
			}
			if !strings.Contains(c.Problem, "resident orchestrator") {
				t.Errorf("the line does not name what is missing: %q", c.Problem)
			}
			if !strings.Contains(c.Fix, "fugaro update") {
				t.Errorf("the fix does not name the command that writes it: %q", c.Fix)
			}
			if checksFail(cs, false) {
				t.Error("a missing orchestrator fails doctor, and the run's own halt is what the state costs")
			}
		})

		t.Run("the installation records it", func(t *testing.T) {
			doctorRoutingOrchestratorRig(t, doctorRoutingPausing)
			holdOrchestratorWorkdir(t, "proj-1234", true)
			o, out := doctorRoutingOutput(t)
			if cs := checksByID(o, doctorRoutingPauseID); len(cs) != 0 {
				t.Errorf("routing-pause-orchestrator = %+v, want none: the installation's own record holds the block:\n%s", cs, out)
			}
		})

		// An installation that cannot carry one at all is the same fact with no
		// command behind it: information, and never a warning an update would
		// only repeat.
		t.Run("the installation cannot carry one", func(t *testing.T) {
			doctorRoutingRig(t, doctorRoutingPausing)
			o, out := doctorRoutingOutput(t)
			cs := checksByID(o, doctorRoutingPauseID)
			if len(cs) != 1 || cs[0].Severity != "info" {
				t.Fatalf("routing-pause-orchestrator = %+v, want one informational line:\n%s", cs, out)
			}
			if !strings.Contains(cs[0].Problem, "credentials service or Firebase budget backend recorded") {
				t.Errorf("the line does not name what the installation lacks: %q", cs[0].Problem)
			}
			if !strings.HasPrefix(cs[0].Fix, "no action") {
				t.Errorf("the fix sends the reader to a command that changes nothing: %q", cs[0].Fix)
			}
			if checksFail(cs, true) {
				t.Errorf("the line fails doctor under --strict: %+v", cs)
			}
		})
	})

	// What doctor no longer reports: a repository's auth mode. The check that
	// read it is gone, and neither the run nor the registries the views read
	// carry it.
	t.Run("no auth mode", func(t *testing.T) {
		// The rig that used to report it: a proxied repository of an opted-in
		// installation, run the way its own tests ran it.
		r := newBrokeringRig(t)
		o, _ := r.doctor(t)
		if len(o.Checks) == 0 {
			t.Fatal("doctor emitted no check at all, so the absence below proves nothing")
		}
		for _, c := range o.Checks {
			if c.ID == "model-key-in-agent" {
				t.Errorf("doctor still reports the repository's auth mode: %+v", c)
			}
		}
		if doctorShownSecurityIDs["model-key-in-agent"] {
			t.Error("the sweep still holds the auth mode's check as an item it prints")
		}
	})
}
