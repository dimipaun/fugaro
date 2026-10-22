package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// hostileRepoRig is a rig and a checkout of a third-party repository whose
// default-branch fugaro.yaml names this project, which the project's local
// config does not list.
func hostileRepoRig(t *testing.T, origin string) (*initRig, string) {
	t.Helper()
	r := newInitRig(t)
	r.stateBucket()
	dir := repoCheckout(t, origin, checkoutYAML("github", "api-key", "aurora", ""))
	t.Chdir(t.TempDir())
	return r, dir
}

func noCloudTouched(t *testing.T, r *initRig, what string) {
	t.Helper()
	if calls := r.calls(t); len(calls) != 0 {
		t.Errorf("%s: terraform ran: %q", what, calls)
	}
	for _, rq := range r.gcs.Requests() {
		t.Errorf("%s: Cloud Storage was reached: %s %s", what, rq.Method, rq.Path)
	}
}

// update --repo on its own is the converge's hostile-checkout gate too: a
// repository the local config does not list is never onboarded by one
// command with --yes, in a pipe, with --json or by an agent; it takes the
// typed owner/name, or --onboard-repo in a terminal with no agent marker.
func TestInitRepoAloneIsBehindTheHostileGate(t *testing.T) {
	for name, tc := range map[string]struct {
		args  []string
		env   string
		stdin string
		tty   bool
	}{
		"--yes":                      {[]string{"--yes"}, "", "", false},
		"--yes at a terminal":        {[]string{"--yes"}, "", "acme/hostile\n", true},
		"--json":                     {[]string{"--json"}, "", "", false},
		"--non-interactive":          {[]string{"--non-interactive", "--yes"}, "", "", false},
		"a coding agent, --yes":      {[]string{"--yes"}, "CLAUDECODE", "", true},
		"a terminal, wrong typed":    {nil, "", "acme/other\n", true},
		"a terminal, name not typed": {nil, "", "yes\n", true},
		"--onboard-repo, an agent":   {[]string{"--yes", "--onboard-repo", "acme/hostile"}, "CLAUDECODE", "", true},
		"--onboard-repo, wrong repo": {[]string{"--yes", "--onboard-repo", "acme/else"}, "", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			r, dir := hostileRepoRig(t, "https://github.com/acme/hostile.git")
			if tc.tty {
				fakeTerminal(t)
			}
			if tc.env != "" {
				t.Setenv(tc.env, "1")
			}
			out, errOut, err := executeStdin(t, tc.stdin, append([]string{"setup", "--repo", "--github-app-id", "42"}, append(tc.args, dir)...)...)
			if err == nil {
				t.Fatalf("an unlisted repository was onboarded:\n%s", out)
			}
			noCloudTouched(t, r, name)
			all := out + errOut + err.Error()
			if tc.env != "" && strings.Contains(strings.ReplaceAll(all, "--onboard-repo opts a repository in to cloud changes and is not accepted", ""), "--onboard-repo acme/hostile") {
				t.Errorf("an agent session was handed --onboard-repo to paste:\n%s", all)
			}
			if tc.env != "" && !strings.Contains(all, "your own terminal") {
				t.Errorf("no typed route:\n%s", all)
			}
		})
	}
}

// TestSetupOnboardsUnderYesWithoutATypedConfirmation: D12's answer of
// 2026-10-20 makes onboarding an unlisted repository an ordinary covered row of
// BOTH front doors' converge, so `setup --yes` onboards one with no typed
// question and no --onboard-repo. What it does not do is widen the gate: an
// origin git config rewrites, a host that is not a provider's, and a name the
// config knows at another host are all still the gate's own question, because
// none of them says which repository the run is being asked about.
//
// The step is taken in the converge (onboardCovered), so it is driven through
// the stage with the engine stubbed, as the gate's other tests are.
func TestSetupOnboardsUnderYesWithoutATypedConfirmation(t *testing.T) {
	releaseBuild(t, "0.2.0")
	rewritten := func(t *testing.T) string {
		t.Helper()
		dir := repoCheckout(t, "https://evil.example/acme/app.git", checkoutYAML("github", "oauth", "aurora", ""))
		f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("[url \"https://github.com/\"]\n\tinsteadOf = https://evil.example/\n"); err != nil {
			t.Fatal(err)
		}
		f.Close()
		if got := testutil.Git(t, dir, "remote", "get-url", "origin"); got != githubOrigin {
			t.Fatalf("the fixture does not rewrite: %q", got)
		}
		return dir
	}
	for _, tc := range []struct {
		name  string
		dir   func(t *testing.T) string
		lc    func(e *initEngine)
		cover bool
	}{
		{name: "an unlisted repository on a provider's host", cover: true,
			dir: func(t *testing.T) string {
				return repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
			}},
		{name: "a host that is not a provider's",
			dir: func(t *testing.T) string {
				return repoCheckout(t, "https://evil.example/acme/app.git", checkoutYAML("github", "oauth", "aurora", ""))
			}},
		{name: "an origin git config rewrites", dir: rewritten},
		{name: "a name the config knows at another host", lc: func(e *initEngine) {
			e.lc.Repos = map[string]localcfg.Repo{"acme/app": {Provider: "bitbucket"}}
		}, dir: func(t *testing.T) string {
			return repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(tc.dir(t))
			fakeTerminal(t)
			// --yes on a pipe: the covered step takes no terminal at all.
			e, out := stageEngine(t, "acme/app\n", &initOptions{yes: true, githubAppID: "12345"})
			unknownRepo(e)
			if tc.lc != nil {
				tc.lc(e)
			}
			ran := false
			rs := newRepositoryStage(e)
			rs.engineStage.run = func(context.Context) error { ran = true; return nil }
			res, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options())
			if err != nil {
				t.Fatal(err)
			}
			if tc.cover {
				if !ran || stateOf(res, "repository") != initflow.Done {
					t.Fatalf("--yes did not onboard it: ran %v %+v\n%s", ran, res.Stages, out)
				}
				if strings.Contains(out.String(), "Type ") || len(res.Left) != 0 {
					t.Errorf("--yes asked for a confirmation:\n%s%+v", out, res.Left)
				}
				return
			}
			if ran || stateOf(res, "repository") != initflow.NeedsYou || len(res.Left) != 1 {
				t.Fatalf("--yes onboarded an origin it cannot name: ran %v %+v %v\n%s", ran, res.Stages, res.Left, out)
			}
			if strings.Contains(res.Left[0].Text, "Type ") {
				t.Errorf("the gate's line is a prompt under --yes: %q", res.Left[0].Text)
			}
		})
	}
}

// The gate's typed route names the door the reader is at. The route is a
// command line the reader is asked to run in their own terminal, and it is
// reached from either door: a `fugaro setup --repo` run in an agent's session
// (or any run that cannot ask) was told to run `fugaro update --repo`, the one
// command it is not running. Task W2.1: the reader's own door, and nothing
// else, appears in the line.
func TestTheHostileGatesTypedRouteNamesTheReadersDoor(t *testing.T) {
	for _, door := range []string{"setup", "update"} {
		t.Run(door, func(t *testing.T) {
			_, dir := hostileRepoRig(t, "https://github.com/acme/hostile.git")
			t.Setenv("CLAUDECODE", "1")
			out, errOut, err := executeStdin(t, "", door, "--repo", "--github-app-id", "42", "--yes", dir)
			all := out + errOut + ErrorText(err)
			if !strings.Contains(all, " "+door+" --repo and type ") {
				t.Errorf("the typed route does not name fugaro %s --repo, the door this run is:\n%s", door, all)
			}
			if strings.Contains(all, "run fugaro "+otherDoorName(door)+" --repo") {
				t.Errorf("the typed route names the other door:\n%s", all)
			}
		})
	}
}

// otherDoorName is the door that is not this one, for the tests that hold a
// line to the reader's own door by name.
func otherDoorName(door string) string {
	if door == "setup" {
		return "update"
	}
	return "setup"
}

// pastGate: the engine went on beyond the gate to its own first check (this
// rig's local config has no base image for the repository's workflow, which it
// refuses before any cloud call).
func pastGate(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "no base_images entry") {
		t.Errorf("the engine did not run past the gate: %v", err)
	}
}

// The ways in: the typed owner/name at a terminal, or --onboard-repo outside
// an agent. Past the gate the engine runs as it always did (here it stops at
// the fake installation, which is not what is under test).
func TestInitRepoAloneOptIns(t *testing.T) {
	gate := "is not one of project"
	_, dir := hostileRepoRig(t, "https://github.com/acme/hostile.git")
	fakeTerminal(t)
	_, _, err := executeStdin(t, "acme/hostile\n", "setup", "--repo", "--github-app-id", "42", dir)
	if err != nil && strings.Contains(err.Error(), gate) {
		t.Errorf("the typed opt-in did not pass the gate: %v", err)
	}
	pastGate(t, err)
	_, dir = hostileRepoRig(t, "https://github.com/acme/hostile.git")
	_, _, err = executeStdin(t, "", "setup", "--repo", "--yes", "--github-app-id", "42", "--onboard-repo", "acme/hostile", dir)
	if err != nil && strings.Contains(err.Error(), gate) {
		t.Errorf("--onboard-repo did not pass the gate: %v", err)
	}
	pastGate(t, err)
	// A listed repository is as before: no gate.
	r := newInitRig(t)
	r.stateBucket()
	dir = repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", strings.Replace(checkoutYAML("bitbucket", "api-key", "aurora", ""), "app:", "web:", 1))
	t.Chdir(t.TempDir())
	_, _, err = executeStdin(t, "", "setup", "--repo", "--yes", dir)
	if err != nil && strings.Contains(err.Error(), gate) {
		t.Errorf("a listed repository was gated: %v", err)
	}
	pastGate(t, err)
	// --plan-only changes nothing and is not gated.
	_, dir = hostileRepoRig(t, "https://github.com/acme/hostile.git")
	_, _, err = executeStdin(t, "", "setup", "--repo", "--plan-only", "--github-app-id", "42", dir)
	if err != nil && strings.Contains(err.Error(), gate) {
		t.Errorf("--plan-only was gated: %v", err)
	}
}

// --onboard-repo is refused outright in an agent's environment, for the
// converge and for --repo alike; and the needs-you for an agent session never
// prints it.
func TestOnboardRepoRefusedInAnAgentSession(t *testing.T) {
	for _, args := range [][]string{
		{"setup", "--onboard-repo", "acme/app", "--yes"},
		{"setup", "--repo", "--onboard-repo", "acme/app", "--yes"},
	} {
		t.Setenv("CLAUDECODE", "1")
		_, _, err := execute(t, args...)
		if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not accepted in a coding agent's session") || strings.Contains(err.Error(), "pass --onboard-repo") {
			t.Errorf("%v: %v", args, err)
		}
	}
	t.Setenv("CLAUDECODE", "")
	oi := originInfo{Repo: "acme/app", Host: "github.com"}
	if lf := onboardLeft(oi, "setup"); !strings.Contains(strings.Join(lf.Commands, " ")+lf.Text, "setup --onboard-repo") {
		t.Errorf("outside an agent session the flag line is the route: %+v", lf)
	}
	t.Setenv("CLAUDECODE", "1")
	lf := onboardLeft(oi, "setup")
	if strings.Contains(strings.Join(lf.Commands, " ")+lf.Text, "--onboard-repo") || !strings.Contains(lf.Text, "your own terminal window") || len(lf.Commands) != 1 {
		t.Errorf("an agent session is handed the flag: %+v", lf)
	}
}

// What a hostile origin prints is cut, in the text and in --json.
func TestHostileOriginIsTruncatedInDetail(t *testing.T) {
	long := strings.Repeat("x", 5000)
	oi := originInfo{Repo: "acme/" + long, Host: "evil.example", Raw: "https://evil.example/" + long, Effective: "https://github.com/" + long, Rewritten: true}
	d := unknownDetail("aurora", oi)
	if len(d) > 1500 || !strings.Contains(d, "(truncated)") {
		t.Errorf("%d bytes: %.80q", len(d), d)
	}
}

// TestInitRepoRefusesCatalogSwitches (price-catalog plan Task 4 step 2): a
// route that sets require_zdr or price_policy is refused at the converge
// --repo before any cloud call, because the job it would deploy carries
// FUGARO_MODEL_PROVIDERS with a key no release reads yet, and an older
// runner fails the run at bootstrap on it. The same command with the switch
// unset goes on to its own next refusal (this rig has no base image for the
// workflow), which is what proves the gate is the new one and not something
// earlier.
func TestInitRepoRefusesCatalogSwitches(t *testing.T) {
	init := func(t *testing.T, extra string) (*initRig, error, string, string) {
		t.Helper()
		r := newInitRig(t)
		r.stateBucket()
		// A repository the local config lists (acme/sandbox, provider
		// bitbucket, workflow web), so the onboarding gate is not what
		// refuses.
		dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", strings.Replace(checkoutYAML("bitbucket", "api-key", "aurora", ""), "app:", "web:", 1))
		r.appendConfig(t, validateProviderBlock+extra)
		t.Chdir(t.TempDir())
		out, errOut, err := executeStdin(t, "", "setup", "--repo", "--yes", dir)
		return r, err, out, errOut
	}
	r, err, out, errOut := init(t, "    require_zdr: false\n")
	if err == nil {
		t.Fatalf("a route with require_zdr set was deployed:\n%s%s", out, errOut)
	}
	noCloudTouched(t, r, "a route with require_zdr set")
	for _, want := range []string{
		"fugaro update --repo refuses to deploy acme/sandbox's provider openrouter",
		"require_zdr or price_policy",
		"fugaro " + catalogSince,
		"model_prices",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	// Unset: the same command gets past this gate to its own next refusal.
	_, err, _, _ = init(t, "")
	pastGate(t, err)
}
