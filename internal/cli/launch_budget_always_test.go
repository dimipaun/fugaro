package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
)

// AB4 says the launcher always mints: on an installation whose config names a
// budget database, every launch is given the run its identity, whatever the
// budget mode is spelled and whether or not a policy is published (F7); on one
// that names none, nothing is launched at all, with the fix and one wording for
// who runs it (F5); and on one whose repository job was deployed before the
// database existed, the launch is refused with that job's own fix (R6, U6).
//
// The three tests here are the three shapes, and each one is a world a launch
// really meets: the fixture is the harness's own installation (its database,
// the signer it mints through, the caps the pre-check reads, and the job image
// the seal gate judges), and what varies is the one thing the case is about.

// launchFixtureForMode is that installation with the budget block spelled as
// the case means, "" being the harness's own block, which names no mode at all
// — the shape the plan's red step starts from ("a launch with an unset mode
// mints nothing").
func launchFixtureForMode(t *testing.T, mode string) *launchBudgetFixture {
	t.Helper()
	f := &launchBudgetFixture{cloudFixture: newCloudFixture(t)}
	f.db, f.iam = f.cloudFixture.budget.DB, f.cloudFixture.iam
	if mode == "" {
		return f
	}
	f.appendConfig(t, "budget: { mode: "+mode+", per_run_usd: 5, firebase_project: proj-1234, rtdb_url: "+f.db.URL+
		", token_signer: "+testSigner+", firebase_api_key: "+settingsFakeAPIKey+" }\n")
	f.db.Set("config/mode", "enforce")
	return f
}

// TestEveryLaunchMints is F7's rule at the launcher: a launch of an
// installation with a budget database mints the run's identity, and the mode —
// unset, off, observe, enforce — and the absence of a published policy decide
// nothing about it. The token object is sealed with the launch's own key (the
// marker's), and its claims are the repository, the run and the project, so
// what is asserted is the run's identity and not merely a write.
//
// Mutation (run, restored): read the local mode in budgetBackendOn — the
// unset and off cases mint nothing and fail here. Mutation (run, restored):
// make the mint depend on a published policy (the old decision) — the cases
// with nothing published mint nothing and fail here.
func TestEveryLaunchMints(t *testing.T) {
	policy := `{"version":9,"budget":{"mode":"enforce","per_run_usd":5},"allow_repos":["github/acme/*"]}`
	for _, mode := range []string{"", "off", "observe", "enforce"} {
		for _, published := range []bool{false, true} {
			name := "mode " + mode
			if mode == "" {
				name = "no mode at all"
			}
			if published {
				name += ", a published policy"
			} else {
				name += ", nothing published"
			}
			t.Run(name, func(t *testing.T) {
				f := launchFixtureForMode(t, mode)
				if published {
					f.db.Set(projectpolicy.PathRulesMarker(), projectpolicy.RulesVersion)
					f.db.Set(projectpolicy.PathShared(), json.RawMessage(policy))
				}
				var env map[string]string
				f.run.OnRun = func(c gcpfake.RunCall) { env = c.Env }
				if _, _, err := runBudget(t); err != nil {
					t.Fatalf("err = %v, want a launch: every launch of a configuration that names a budget database mints (AB4, F7)", err)
				}
				if n := len(f.iam.SignCalls()); n != 1 {
					t.Fatalf("sign calls = %d, want the one mint every launch makes", n)
				}
				raw := readToken(t, f.cloudFixture, budgetRun)
				if raw == "" {
					t.Fatal("the launch minted no budget token")
				}
				// The object is the launch's own: sealed under the key this
				// execution was handed, and about this run.
				c := claimsOf(t, f, unsealed(t, markerKey(t, env), []byte(raw), budgetRun))
				if c["fs"] != appSlug || c["fr"] != budgetRun || c["fp"] != "aurora" {
					t.Fatalf("claims = %v, want this repository, this run and this project", c)
				}
				if n := len(f.run.Executions()); n != 1 {
					t.Fatalf("%d executions were created, want 1", n)
				}
			})
		}
	}
}

// TestALaunchWithNoDatabaseIsRefusedWithTheFix is the other half of F7, and
// F5's one wording: an installation whose config names no budget database
// launches nothing, and the refusal says what is missing, what it costs and the
// one command that creates it — the same words for both shapes a config can
// have (a block with a mode and no address, and no block at all), and the same
// words with --no-budget-check, because the database is not a check the flag
// can skip: it is where the run's identity comes from.
//
// The refusal is a launch's own, before anything is written (no task, no
// object, no execution), and it is budgetMintOn's answer too — the function the
// seal gate and `doctor` read, so the gate, the pre-check and the mint cannot
// disagree about which installations launch.
//
// Mutation (run, restored): give budgetBackendOn the old early return for a
// mode that is not off — the "a mode and no database" case launches and fails.
func TestALaunchWithNoDatabaseIsRefusedWithTheFix(t *testing.T) {
	shapes := []struct {
		name  string
		block string
	}{
		{"a mode and no database", "budget: { mode: observe, per_run_usd: 5 }\n"},
		{"no budget block at all", ""},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			f := launchFixtureForMode(t, "")
			// The installation has no database at all: the config names none
			// and its jobs were deployed without one (noBudget).
			f.noBudget(t)
			f.appendConfig(t, shape.block)
			for _, args := range [][]string{nil, {"--no-budget-check"}} {
				_, _, err := runBudget(t, args...)
				if err == nil || ExitCode(err) != ExitUserError {
					t.Fatalf("err = %v (exit %d), want the launch refused, not launched", err, ExitCode(err))
				}
				for _, want := range []string{
					"no budget database",
					"every run has a budget identity, so nothing is launched",
					// F5: one wording for who runs it, and the one command.
					"a project owner runs fugaro update",
					"which creates the database",
				} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("the refusal does not say %q: %v", want, err)
					}
				}
				if launchedAny(f) || readToken(t, f.cloudFixture, budgetRun) != "" {
					t.Fatal("a launch with no database minted a token or launched the run")
				}
				if len(f.iam.SignCalls()) != 0 {
					t.Fatal("a launch with no database signed a budget identity")
				}
				if storedTask(t, f.cloudFixture) {
					t.Error("the refused launch stored a task: the refusal is before anything of the launch is written")
				}
			}
			// The same answer from the function a launch, the seal gate and
			// doctor decide by.
			on, err := memEnv(t, f.cloudFixture).budgetMintOn(context.Background())
			if on || err == nil || !strings.Contains(err.Error(), "no budget database") {
				t.Fatalf("budgetMintOn = (%v, %v), want no mint and the refusal that names the database", on, err)
			}
		})
	}
}

// TestALaunchOnAJobThatPredatesTheDatabaseIsRefused is R6 and U6: the
// installation has a database now, but this repository's job was applied by an
// update from before it existed, so the job carries no address in its
// environment and the identity this launch would mint is one the run could not
// use. The launch is refused before the mint, with the job named and the one
// command that re-deploys it — `fugaro update --repo`, in that repository's
// checkout — rather than at the runner, after a claim, an object and an
// execution.
//
// Mutation (run, restored): drop the job read from mintBudgetToken
// (budgetJobBackend) — the launch mints, writes its object and its execution,
// and fails here.
func TestALaunchOnAJobThatPredatesTheDatabaseIsRefused(t *testing.T) {
	f := launchFixtureForMode(t, "")
	// The repository's own job, as an installation that had no database
	// deployed it: no address, no key. The config names the database, which is
	// the state `fugaro update --firebase-project` leaves behind for one
	// repository at a time.
	f.run.SetJobEnv(gcp.JobName(appSlug, "web"), nil)
	for _, args := range [][]string{nil, {"--no-budget-check"}} {
		_, _, err := runBudget(t, args...)
		if err == nil || ExitCode(err) != ExitUserError {
			t.Fatalf("err = %v (exit %d), want the launch refused", err, ExitCode(err))
		}
		for _, want := range []string{
			gcp.JobName(appSlug, "web"),
			runner.RTDBURLEnv,
			"fugaro update --repo",
			"acme/app",
			"before this installation had a database",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q: %v", want, err)
			}
		}
		if launchedAny(f) || readToken(t, f.cloudFixture, budgetRun) != "" {
			t.Fatal("a launch onto a job with no database minted a token or launched the run")
		}
		if len(f.iam.SignCalls()) != 0 {
			t.Fatal("a launch onto a job with no database signed a budget identity")
		}
	}
	// And the same repository, once the job carries the database its config
	// names: the launch mints and starts, which is what makes the refusal
	// about the job rather than about the repository.
	g := launchFixtureForMode(t, "")
	if _, _, err := runBudget(t); err != nil {
		t.Fatalf("the same launch on a job deployed with the database was refused too: %v", err)
	}
	if len(g.iam.SignCalls()) != 1 || readToken(t, g.cloudFixture, budgetRun) == "" {
		t.Fatal("a launch on a job that carries the database minted no token")
	}
}
