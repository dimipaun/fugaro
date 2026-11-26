package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/routing"
)

// poolEveryModelTableYAML is a table that routes every model a run can call —
// the wildcard entry is what lets these tests say nothing about which models
// the fixture's rungs happen to name — with a chain that names the launcher's
// pool beside a project pool: the chain `--pool` needs, since a name resolves
// only where a chain of the run's models names that source (D3176).
const poolEveryModelTableYAML = `model_routing:
    - match: "*"
      chain:
        - kind: pool
          pool: team
        - kind: launcher-pool
`

// poolWithoutLauncherTableYAML is the same table with the launcher's pool left
// out of the chain: a run launched against it from a named pool has nowhere for
// the name to resolve, which is the state the launch has to refuse with the
// fix rather than start a run nothing serves from that pool.
const poolWithoutLauncherTableYAML = `model_routing:
    - match: "*"
      chain:
        - kind: pool
          pool: team
`

// poolModelRepoYAML is the cascade checkout with the run's model written down:
// the launcher reads the models a chain is judged against out of the file the
// run will check out (launchRunModels), so a fixture that leaves agent.model to
// the installation's default names a model this test cannot spell, and every
// arm below would read an empty model set.
//
// agent.model is a lifted key (R1, D3237), and the arms therefore move the
// fixture's base image to l8Since, the release that stops reading it: a
// repository's own model on an older image is L8's own refusal, which is not
// the refusal these tests read.
const poolModelRepoYAML = `version: 1
project: aurora
git:
  provider: github
  base_branch: main
agent:
  auth: cascade
  model: claude-sonnet-5-5
workflows:
  web:
    checkout: clone
    commands:
      build: sh build.sh
      test: sh test.sh
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
`

// poolFixture is the switched-on installation these arms launch on: the cascade
// fixture whose repository names its model, the model-bearing image, the
// table and the credentials service's address. Every `--pool` arm reads the
// same two facts from it, and a refusal that is not the flag's own — an image
// too old for agent.model, a service the launch cannot reach — would be a test
// of something else.
func poolFixture(t *testing.T, table string) *projectJobFixture {
	t.Helper()
	f := newCascadeProjectJobFixture(t, poolModelRepoYAML)
	f.rewriteConfig(t, "fugaro-base/fugaro-base:"+nodeRelease, "fugaro-base/fugaro-base:"+l8Since)
	f.appendConfig(t, table)
	f.appendConfig(t, "credentials_service:\n  url: "+credentialsServiceFixtureURL+"\n")
	markRepoCredentialsServiceReady(t, f.cloudFixture)
	return f
}

// TestTokenFlagIsRejectedWithTheFix: `--token` is retired where the project's
// table serves the run — naming a Claude account by hand is what the launcher's
// pool does now — and the refusal names `--pool` as the flag in its place
// (D3176 as amended, CS12), before the claim and before the task, exactly as
// the table's own gate is.
//
// The second arm is the release-1 bridge and the reason this is a refusal of
// the table and not of the flag: a repository no table serves goes out on the
// release-1 configuration (R25, D3196), where `--token` still names the account
// its job mounts, and refusing it there would refuse a configuration no
// migration has reached — the plan's own reason a lone subscription token is
// reported in release 1 rather than refused.
//
// Mutation (verified, run and restored): refusing `--token` unconditionally
// fails the second arm, which is a launch every other test of this package
// makes (62 call sites in 13 files, none of them CS12's to change).
func TestTokenFlagIsRejectedWithTheFix(t *testing.T) {
	t.Run("a repository the table serves", func(t *testing.T) {
		f := newProjectJobFixtureMode(t, "enforce")
		// The fixture's base image is the release the policy node needs, which
		// is older than the one a repository's own agent.model needs (L8): the
		// refusal this arm reads is the flag's, so the image moves first.
		f.rewriteConfig(t, "fugaro-base/fugaro-base:"+nodeRelease, "fugaro-base/fugaro-base:"+l8Since)
		f.appendConfig(t, poolEveryModelTableYAML)
		projectJobCheckoutYAML(t, chainLaunchYAML)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261126-090000-c512",
			"--token", config.AnthropicKeyAccount, "Add a feature")
		if ExitCode(err) != ExitUserError {
			t.Fatalf("a launch naming an account on a repository the table serves: err = %v, want the retirement of the flag", err)
		}
		for _, want := range []string{"--token", "--pool"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Errorf("the refused launch started %d executions, want none: the gate runs before the claim", n)
		}
	})

	t.Run("a repository no table serves", func(t *testing.T) {
		f := newProjectJobServingFixture(t)
		projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261126-090001-c512",
			"--token", "acme-personal", "Add a feature"); err != nil {
			t.Fatalf("a launch naming an account on the release-1 bridge: %v: the flag is retired where the table serves the run and not everywhere, or no repository no migration has reached could still be launched", err)
		}
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261126-090002-c512",
			"--pool", "team", "Add a feature"); ExitCode(err) != ExitUserError {
			t.Fatalf("a launch naming a pool on a project that publishes no table: err = %v, want a refusal: no chain of this run's models names the launcher's pool, so the name resolves to nothing whatever the service would make of it", err)
		} else if !strings.Contains(err.Error(), string(routing.KindLauncherPool)) || !strings.Contains(err.Error(), "team") {
			t.Errorf("the refusal does not name the pool asked for and the source it needs: %v", err)
		}
		if n := len(f.run.Executions()); n != 1 {
			t.Errorf("the two launches started %d executions, want one: the bridge's own, and none for the refused pool", n)
		}
	})
}

// TestReviewRecipeLaunchRefusesTheTokenFlag: a review-mode launch reached
// through `fugaro run` is refused over `--token` exactly like any other run
// where the table names the launcher's pool. CS12 exempted `recipe.ModeReview`
// from the retirement because `fugaro review --token` was the only way to name
// the account the review job ran as, and CS15 took that flag away: the review
// job reaches its model by the table and its account is the repository's own,
// so no launch of a review recipe has an account to name either.
//
// The fixture is the review one with a table that names the launcher's pool in
// a chain matching every model, so this launch reads the same table the arms
// above do and is refused by the same sentence.
func TestReviewRecipeLaunchRefusesTheTokenFlag(t *testing.T) {
	f := newReviewFixture(t)
	f.appendConfig(t, poolEveryModelTableYAML)
	projectJobCheckoutYAML(t, projectJobOAuthRepoYAML)
	// The recipe is spelled out rather than taken from reviewOnlyRecipe: the
	// runid guard reads a review-mode launch out of the literal name
	// (runid_test.go's reviewModePR), and this launch reads no recency window.
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261126-090004-c512",
		"--recipe", "review-only", "--pr", "7", "--token", "acme-personal")
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a review-mode launch naming an account where the table names the launcher's pool: err = %v, want the retirement of the flag", err)
	}
	for _, want := range []string{"--token", "--pool"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Errorf("the refused launch started %d executions, want none: the gate runs before the claim", n)
	}
}

// TestPoolFlagNeedsALaunchersPoolInAChain: `fugaro run --pool <name>` carries
// the name on the run's grant request as the pool the launcher's pool resolves
// to for this run (C2's named_pool, D3176 as amended), beside the sources the
// run's models reach by the table; and a launch whose table gives the chain no
// launcher-pool source is refused with the fix, because the name would resolve
// to nothing and the run would start on a pool no chain holds.
//
// The name travels as a name and no more: which pool it is, and whether this
// launcher may be served from it, is the service's own read of the
// entitlement (CS14, PL4), and a launcher that decided it here would be a
// second answer to a question the service answers against the verified
// principal.
//
// Mutation (verified, run and restored): dropping req.NamedPool from runSlipEnv
// sends a grant that names no pool at all, and the first arm's own assertion
// fails.
func TestPoolFlagNeedsALaunchersPoolInAChain(t *testing.T) {
	t.Run("the grant carries the name", func(t *testing.T) {
		poolFixture(t, poolEveryModelTableYAML)
		asked, grants := stubSlipGrant(t)
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261126-090003-c512",
			"--pool", "team", "Add a feature"); err != nil {
			t.Fatalf("a launch whose table names the launcher's pool, from a pool this launcher owns: %v", err)
		}
		if grants() != 1 {
			t.Fatalf("the launch made %d grant calls, want exactly one: a run has one slip", grants())
		}
		req := asked()[0]
		if req.NamedPool != "team" {
			t.Errorf("the grant asked for the pool %q, want %q: the name is what \"the launcher's pool\" resolves to for this run", req.NamedPool, "team")
		}
		if want := []creds.GrantSource{{Kind: string(routing.KindPool), Pool: "team"}, {Kind: string(routing.KindLauncherPool)}}; !slices.Equal(req.Sources, want) {
			t.Errorf("the grant asked for the sources %+v, want %+v: the table's own chain", req.Sources, want)
		}
		// The wire member's name is the contract's (C2), and a service reads it
		// there: a launcher that sent the pool under another name would be a
		// service that saw no pool named at all.
		b, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"named_pool":"team"`) {
			t.Errorf("the grant request carries %s, want the pool under the contract's own member name", b)
		}
	})

	t.Run("the chain names no launcher's pool", func(t *testing.T) {
		f := poolFixture(t, poolWithoutLauncherTableYAML)
		_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261126-090004-c512",
			"--pool", "team", "Add a feature")
		if ExitCode(err) != ExitUserError {
			t.Fatalf("a launch naming a pool no chain of its models names: err = %v, want a refusal naming the pool and the fix", err)
		}
		for _, want := range []string{"team", string(routing.KindLauncherPool), "fugaro routing set", "name the account with --token"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q: %v", want, err)
			}
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Errorf("the refused launch started %d executions, want none: the gate runs before the claim", n)
		}
	})

	// The launcher reads a run's models out of the file it will check out, and
	// a repository whose file names none leaves that set empty (the
	// installation's default fills in at run time). The flag is then judged
	// against the chains that may serve the repository, which is the same
	// question read conservatively: a table that names the launcher's pool in
	// some chain still holds the name, and only a table that names it nowhere
	// refuses it — a repository whose file happens to name no model is not a
	// launch nobody may name a pool for.
	t.Run("the run names no model the launcher can read", func(t *testing.T) {
		f := newCascadeProjectJobFixture(t, projectJobCascadeRepoYAML)
		f.appendConfig(t, poolEveryModelTableYAML)
		f.appendConfig(t, "credentials_service:\n  url: "+credentialsServiceFixtureURL+"\n")
		markRepoCredentialsServiceReady(t, f.cloudFixture)
		asked, grants := stubSlipGrant(t)
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261126-090005-c512",
			"--pool", "team", "Add a feature"); err != nil {
			t.Fatalf("a launch naming a pool where the launcher could read no model of the run: %v: the table names the launcher's pool, so the name resolves", err)
		}
		if grants() != 1 || asked()[0].NamedPool != "team" {
			t.Errorf("the launch made %d grant calls and named the pool %q, want one grant naming %q", grants(), asked()[0].NamedPool, "team")
		}
	})
}

// TestAFollowUpUsesItsOwnLaunchersPool: a follow-up launched by a person names
// its own pool with `--pool`, and a follow-up that names none carries no pool
// at all — the pool a run was launched from is not a property its successors
// inherit. The chain it continues is the pull request's, and the person
// continuing it is the launcher whose entitlement the service reads
// (D3176, D3180), so a name carried over from the earlier run's grant would be
// a service asked for a pool this launcher never named.
//
// Only a resume keeps the pool its launch named (D3191), and that is the
// service's binding of a paused run to its own grant (PR3), not a read of a
// stored task: nothing on this side stores a pool to inherit, which is what the
// second arm pins.
//
// Mutation (verified, run and restored): carrying the previous run's pool into
// the follow-up's grant — the field has no such reader today — fails the second
// arm, which is the launch a person makes after a run that named one.
func TestAFollowUpUsesItsOwnLaunchersPool(t *testing.T) {
	f := poolFixture(t, poolEveryModelTableYAML)
	// Two pull requests, because a chain admits one run at a time: the arm that
	// names no pool is the second launch, and a second run on the first pull
	// request would be refused as one the run just started still owns.
	rootNamed, named := wallRunIDAt(3, "090000", "aaaa"), wallRunIDAt(0, "120000", "f00d")
	rootPlain, plain := wallRunIDAt(3, "090000", "bbbb"), wallRunIDAt(0, "130000", "beef")
	for _, r := range []struct {
		root string
		pr   int
	}{{rootNamed, 7}, {rootPlain, 8}} {
		seedLaunchedOn(t, f.cloudFixture, appSlug, r.root, f.job)
		writeRecord(t, f.cloudFixture, r.root, prRecord(r.root, "", r.pr, 1))
	}
	asked, grants := stubSlipGrant(t)

	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", named,
		"--pool", "team", "also rename x"); err != nil {
		t.Fatalf("a follow-up naming its own pool: %v", err)
	}
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "8", "--run-id", plain, "also rename x"); err != nil {
		t.Fatalf("a follow-up naming no pool: %v", err)
	}
	if grants() != 2 {
		t.Fatalf("the two follow-up launches made %d grant calls, want exactly one each", grants())
	}
	if got := asked()[0].NamedPool; got != "team" {
		t.Errorf("the follow-up that named a pool carried %q, want %q: the pool this launch named is the one its own launcher is entitled to", got, "team")
	}
	if got := asked()[1].NamedPool; got != "" {
		t.Errorf("a follow-up that named no pool carried %q, want none: the pool is the launcher's own and never the run it continues", got)
	}
	// The member is absent from the wire and not sent empty: a service one
	// release behind refuses an unknown member whole (C2's strict decode), so a
	// launch that named no pool asks for the grant it always asked for.
	b, err := json.Marshal(asked()[1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "named_pool") {
		t.Errorf("a follow-up that named no pool carries %s, want no named-pool member at all", b)
	}
}

// stubSlipGrant replaces the grant call for the rest of the test and answers
// every request the fixture's service would have answered, so the request the
// launcher really makes is the one asserted (the seam run_slip_test.go's rig
// uses). It returns a reader of the requests in order and a counter.
func stubSlipGrant(t *testing.T) (func() []credsvc.SlipGrantRequest, func() int) {
	t.Helper()
	var asked []credsvc.SlipGrantRequest
	prev := slipGrant
	slipGrant = func(_ context.Context, _ string, req credsvc.SlipGrantRequest) (credsvc.SlipGrantResponse, error) {
		asked = append(asked, req)
		return credsvc.SlipGrantResponse{Slip: fakeSlipID, MaxRuntimeSeconds: req.MaxRuntimeSeconds}, nil
	}
	t.Cleanup(func() { slipGrant = prev })
	return func() []credsvc.SlipGrantRequest { return asked }, func() int { return len(asked) }
}
