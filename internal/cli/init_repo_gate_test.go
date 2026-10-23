package cli

import (
	"bufio"
	"context"
	"io"
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

// TestUnknownDetailNamesTheRouteTheRunHas: the opt-in clause of the gate's
// sentence is the caller's, because the way in is not the same in every run
// (W5.1). --yes covers onboarding an unlisted repository in a converge whose
// origin's identity is the one it claims (D12), and it does not for a rewritten
// URL or a host that is not a provider's own; a coding agent's session covers
// nothing and is handed no flag to paste; and the standalone --repo gate's
// question IS the identity, so its sentence says --yes cannot give it here. A
// single sentence for all four would name a route three of them do not have —
// the clause that used to be printed everywhere said "not covered by --yes"
// where --yes did cover it.
func TestUnknownDetailNamesTheRouteTheRunHas(t *testing.T) {
	plain := originInfo{Repo: "acme/app", Host: "github.com"}
	rewritten := originInfo{Repo: "acme/app", Host: "github.com", Raw: "https://github.com/acme/app.git", Effective: "https://github.com/acme/other.git", Rewritten: true}
	other := originInfo{Repo: "acme/app", Host: "gitlab.example"}
	// The project's config knows this name at another host: onboardCovered
	// refuses it, so --yes does not cover it either.
	lc := &localcfg.Config{Repos: map[string]localcfg.Repo{"acme/app": {Provider: "bitbucket"}}}
	none := &localcfg.Config{}

	t.Setenv("CLAUDECODE", "")
	if got := convergeOptIn(none, plain); !strings.Contains(got, "--yes covers it") {
		t.Errorf("an unambiguous origin does not name --yes: %q", got)
	}
	for _, oi := range []originInfo{rewritten, other} {
		got := convergeOptIn(none, oi)
		if !strings.Contains(got, "--yes does not give it") || strings.Contains(got, "--yes covers it") {
			t.Errorf("an origin whose identity is not the one it claims names --yes as the way in: %q", got)
		}
	}
	if got := convergeOptIn(lc, plain); !strings.Contains(got, "--yes does not give it here") {
		t.Errorf("a name the config knows at another host names --yes as the way in: %q", got)
	}
	// A coding agent's session confirms nothing, so the sentence names no flag
	// for it to paste: the ordinary origin's clause says so rather than
	// offering --yes, and the other two keep denying it.
	t.Setenv("CLAUDECODE", "1")
	if got := convergeOptIn(&localcfg.Config{}, plain); !strings.Contains(got, "confirms nothing") || strings.Contains(got, "covers it") {
		t.Errorf("an agent session is handed --yes: %q", got)
	}
	for _, oi := range []originInfo{rewritten, other} {
		if got := convergeOptIn(none, oi); strings.Contains(got, "covers it") {
			t.Errorf("an agent session is told --yes covers the step: %q", got)
		}
	}
	t.Setenv("CLAUDECODE", "")
	// And the clause is part of the sentence the reader sees, once.
	d := unknownDetail("aurora", plain, convergeOptIn(none, plain))
	if strings.Count(d, "--yes") != 1 || !strings.Contains(d, "needs your own opt-in — --yes covers it") {
		t.Errorf("the detail names the route %d times: %q", strings.Count(d, "--yes"), d)
	}
}

// What a hostile origin prints is cut, in the text and in --json.
func TestHostileOriginIsTruncatedInDetail(t *testing.T) {
	long := strings.Repeat("x", 5000)
	oi := originInfo{Repo: "acme/" + long, Host: "evil.example", Raw: "https://evil.example/" + long, Effective: "https://github.com/" + long, Rewritten: true}
	d := unknownDetail("aurora", oi, convergeOptIn(&localcfg.Config{}, oi))
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

// gateRig is the start of a converge: the engine, the checkout the gate asks
// about, and the question gateEarly asks it, with a terminal (when one is
// asked for) and stdout collected. The local config lists acme/sandbox
// (bitbucket) and nothing else, so an origin of a github.com repository is the
// unlisted case the gate exists for. Nothing here reaches a cloud: gateEarly
// asks before the first stage plans.
func gateRig(t *testing.T, door, origin, yaml, stdin string, o *initOptions, terminal bool) (*initEngine, *strings.Builder) {
	t.Helper()
	r := newInitRig(t)
	r.stateBucket()
	dir := repoCheckout(t, origin, yaml)
	t.Chdir(dir)
	if terminal {
		fakeTerminal(t)
	} else {
		stdinIsAPipe(t)
	}
	if o == nil {
		o = &initOptions{}
	}
	o.door = door
	out := &strings.Builder{}
	run := &initRun{o: o, w: out, in: bufio.NewReader(strings.NewReader(stdin)), cmd: NewRootCmd()}
	e := &initEngine{r: run, lc: &localcfg.Config{Name: "aurora", Repos: map[string]localcfg.Repo{
		"acme/sandbox": {Provider: "bitbucket", Workflows: []string{"web"}},
	}}}
	e.repo = newRepositoryStage(e)
	e.gateEarly(t.Context())
	return e, out
}

// stdinIsAPipe makes stdin not a terminal for this test: the reader a run gets
// from a shell pipeline or a script. It is set explicitly, because a rig this
// test built earlier may have left the faked terminal in place.
func stdinIsAPipe(t *testing.T) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return false }
	t.Cleanup(func() { stdinIsTerminal = old })
}

// gateCheckoutYAML is the fugaro.yaml a checkout the gate asks about carries,
// for the project these tests name.
func gateCheckoutYAML() string { return checkoutYAML("github", "api-key", "aurora", "") }

const gateRepo = "acme/hostile"

func gateOrigin() string { return "https://github.com/" + gateRepo + ".git" }

// TestSetupAsksToOnboardTheCheckoutsRepository is Task W3.4's red step: in a
// checkout whose repository the project's local config does not list yet,
// `fugaro setup` asks the wizard's first-repository question — "onboard
// <owner/name>?" — at the start of the converge, before any stage plans, and
// Enter takes yes (D5, answered 2026-10-20 as recommended).
//
// The question is the wizard's and it authorizes nothing: it is asked in
// front of the gate, not instead of it. An answer of yes goes on to the same
// typed owner/name confirmation an unlisted repository always needed, and an
// answer of no onboards nothing, skips the repository stage — nothing about
// the repository is left for the user, they answered — and says how to enrol
// it later.
func TestSetupAsksToOnboardTheCheckoutsRepository(t *testing.T) {
	// Yes, the answer Enter takes: the question is asked first, the typed
	// owner/name behind it is unchanged, and the repository is opted in.
	e, out := gateRig(t, setupDoor.name, gateOrigin(), gateCheckoutYAML(), "\n"+gateRepo+"\n", nil, true)
	asked := "Onboard " + gateRepo
	if !strings.Contains(out.String(), asked) {
		t.Errorf("setup did not ask whether to onboard the checkout's repository:\n%s", out)
	}
	if !strings.Contains(out.String(), "["+onboardAnswerYes+"]") {
		t.Errorf("the question does not offer %s as the answer Enter takes:\n%s", onboardAnswerYes, out)
	}
	if strings.Index(out.String(), asked) > strings.Index(out.String(), "Type "+gateRepo+" to onboard it") {
		t.Errorf("the question came after the typed confirmation it is asked in front of:\n%s", out)
	}
	if !e.authorized[gateRepo] {
		t.Errorf("the answer %s and the owner/name typed did not opt the repository in:\n%s", onboardAnswerYes, out)
	}

	// No: nothing is onboarded, the typed confirmation is not asked at all,
	// and the stage is skipped rather than left for the user — they answered.
	e, out = gateRig(t, setupDoor.name, gateOrigin(), gateCheckoutYAML(), onboardAnswerNo+"\n", nil, true)
	if !strings.Contains(out.String(), asked) {
		t.Errorf("setup did not ask whether to onboard the checkout's repository:\n%s", out)
	}
	if strings.Contains(out.String(), "Type "+gateRepo+" to onboard it") {
		t.Errorf("the answer %s still reached the typed confirmation:\n%s", onboardAnswerNo, out)
	}
	if e.authorized[gateRepo] {
		t.Errorf("the answer %s opted the repository in:\n%s", onboardAnswerNo, out)
	}
	st, err := e.repo.Check(t.Context())
	if err != nil {
		t.Fatalf("the stage's check failed: %v", err)
	}
	if st.State != initflow.Skipped {
		t.Errorf("state %q, want %q: the answer was not to onboard it, so there is nothing left for the user\n%s", st.State, initflow.Skipped, out)
	}
	if !strings.Contains(st.Detail, gateRepo) {
		t.Errorf("the skip does not name the repository it is about: %q", st.Detail)
	}
	if !strings.Contains(st.Detail, "--repo") {
		t.Errorf("the skip does not say how to onboard the repository later: %q", st.Detail)
	}

	// A run that does not ask the question is not asked it: --yes, --json and
	// --non-interactive never cover onboarding an unlisted repository, a pipe
	// is not a terminal to ask at, and `fugaro update` is not the wizard.
	for name, tc := range map[string]struct {
		door     string
		o        *initOptions
		terminal bool
	}{
		"--yes":             {door: setupDoor.name, o: &initOptions{yes: true}, terminal: true},
		"--json":            {door: setupDoor.name, o: &initOptions{asJSON: true}, terminal: true},
		"--non-interactive": {door: setupDoor.name, o: &initOptions{nonInteractive: true}, terminal: true},
		"a pipe":            {door: setupDoor.name, o: &initOptions{}},
		"the update door":   {door: updateDoor.name, o: &initOptions{}, terminal: true},
	} {
		t.Run(name, func(t *testing.T) {
			_, out := gateRig(t, tc.door, gateOrigin(), gateCheckoutYAML(), "\n", tc.o, tc.terminal)
			if strings.Contains(out.String(), asked) {
				t.Errorf("a run that does not ask was asked whether to onboard the repository:\n%s", out)
			}
		})
	}
}

// TestSetupConvergeAfterNoLeavesTheRepositoryAlone drives a whole converge in a
// checkout of a repository the project does not list and answers no to the
// wizard's question (W3.4). The answer is the run's, not the repository
// stage's: the stages behind the same gate take it through the engine's
// declined set — so the secrets stage (7) and the plugin wiring (8), which
// write into the project and the checkout, do not offer to ask for the
// owner/name again — and the repository stage (9) is the one that says it is
// not onboarding it. Without that the answer is asked again straight away, and
// typing the name there stores the repository's secrets and wires its checkout
// while the repository stage reports that nothing is being onboarded.
func TestSetupConvergeAfterNoLeavesTheRepositoryAlone(t *testing.T) {
	releaseBuild(t, "0.2.0")
	r := newInitRig(t)
	r.stateBucket()
	t.Chdir(repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", initProjectName, "")))
	fakeTerminal(t)
	e := rigEngine(t, r, &initOptions{planOnly: true, githubAppID: "12345", door: setupDoor.name})
	var out syncBuf
	e.r.in = bufio.NewReader(strings.NewReader(onboardAnswerNo + "\n"))
	e.r.w = &out
	_ = e.converge(t.Context())
	if !strings.Contains(out.String(), "Onboard acme/app") {
		t.Fatalf("the converge did not ask whether to onboard the checkout's repository:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Type acme/app") {
		t.Errorf("a stage behind the gate asked for the owner/name again:\n%s", out.String())
	}
	for _, willAsk := range []string{
		"after you confirm the repository",       // the secrets stage
		"asks you to type the repository's name", // the plugin wiring
	} {
		if strings.Contains(out.String(), willAsk) {
			t.Errorf("a stage still means to ask for the repository (%q):\n%s", willAsk, out.String())
		}
	}
	if !strings.Contains(out.String(), "not onboarding acme/app") {
		t.Errorf("the repository stage does not say it is not onboarding it:\n%s", out.String())
	}
	if len(e.authorized) != 0 {
		t.Errorf("the answer no opted the repository in: %v", e.authorized)
	}
}

// TestSetupOutsideACheckoutSaysHowToEnrol: outside a checkout there is no
// repository to ask about, so the question is skipped and the run says in one
// line how to enrol one later, naming `fugaro setup` (W3.4, D5). A run that
// cannot ask — --yes, --json, a pipe — prints nothing: it was not asked for a
// repository, and a line about one is noise in a script's output. The update
// door is not the wizard and says nothing either.
func TestSetupOutsideACheckoutSaysHowToEnrol(t *testing.T) {
	old := selfCommand
	selfCommand = func() string { return "fugaro" }
	t.Cleanup(func() { selfCommand = old })
	for name, tc := range map[string]struct {
		o        *initOptions
		terminal bool
		want     bool
	}{
		"setup at a terminal": {o: &initOptions{door: setupDoor.name}, terminal: true, want: true},
		"--yes":               {o: &initOptions{door: setupDoor.name, yes: true}, terminal: true},
		"--json":              {o: &initOptions{door: setupDoor.name, asJSON: true}, terminal: true},
		"a pipe":              {o: &initOptions{door: setupDoor.name}},
		"the update door":     {o: &initOptions{door: updateDoor.name}, terminal: true},
	} {
		t.Run(name, func(t *testing.T) {
			firstRunEnv(t) // outside any checkout
			if tc.terminal {
				fakeTerminal(t)
			}
			out := &strings.Builder{}
			run := &initRun{o: tc.o, w: out, in: bufio.NewReader(strings.NewReader("")), cmd: NewRootCmd()}
			e := &initEngine{r: run, lc: &localcfg.Config{Name: "aurora"}}
			e.repo = newRepositoryStage(e)
			e.gateEarly(t.Context())
			line := strings.TrimSpace(out.String())
			if !tc.want {
				if line != "" {
					t.Errorf("the run said something about a repository it was not asked for:\n%s", out)
				}
				return
			}
			if line == "" {
				t.Fatal("a run outside a checkout says nothing about the repository it cannot onboard")
			}
			if strings.Contains(line, "\n") {
				t.Errorf("the run printed more than one line about it:\n%s", out)
			}
			if !strings.Contains(line, "fugaro setup") || !strings.Contains(line, "--repo") {
				t.Errorf("the line does not say how to enrol a repository later, naming fugaro setup:\n%s", out)
			}
		})
	}
}

// TestSetupRepoOnboardsAnUnlistedRepositoryUnderYes is W3.4's row inverted by
// W5.1, which is the change that flips the call site (D12's answer of
// 2026-10-20): onboarding a repository the run was not told about was Typed
// before, and is covered by --yes now — in this mode too, the standalone --repo
// engine, not only in the converge. The wizard's question does not stand in for
// it (a run that cannot ask never asks it), and what --yes does not cover is the
// origin whose identity is not the one it claims, which the next test pins.
func TestSetupRepoOnboardsAnUnlistedRepositoryUnderYes(t *testing.T) {
	r, dir := hostileRepoRig(t, "https://github.com/acme/hostile.git")
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--github-app-id", "42", "--yes", dir)
	all := out + errOut
	if strings.Contains(all, "is not one of project") {
		t.Errorf("--yes was refused at the gate:\n%s", all)
	}
	if strings.Contains(all, "type "+gateRepo+" at the prompt") {
		t.Errorf("--yes still asked for the owner/name to be typed:\n%s", all)
	}
	if strings.Contains(all, "Onboard "+gateRepo) {
		t.Errorf("--yes asked the wizard's question, which a run that cannot ask never does:\n%s", all)
	}
	if strings.Contains(all, "--onboard-repo") {
		t.Errorf("--yes offered the opt-in flag for a step it already covered:\n%s", all)
	}
	// Past the gate the engine runs as it always did (this rig's local config
	// has no base image, which it refuses before any cloud call) — that is what
	// says the repository was let through.
	pastGate(t, err)
	noCloudTouched(t, r, "an unlisted repository under --yes")
}

// TestSetupOnboardingIsASeparateAnswerFromTheProjectQuestion: the wizard asks
// two questions about two different things, and neither answers the other. The
// project's is the wizard's own (setup.go's askProjectAnswer, asked with the
// other first-run inputs); this one is the gate's, asked at the start of the
// converge about the checkout's repository (W3.4, D5). A run whose answer here
// is no still carries the project answers it was given.
func TestSetupOnboardingIsASeparateAnswerFromTheProjectQuestion(t *testing.T) {
	e, out := gateRig(t, setupDoor.name, gateOrigin(), gateCheckoutYAML(), onboardAnswerNo+"\n",
		&initOptions{createProject: true, name: "aurora", cloud: cloudOptions{gcpProject: "proj-1234", region: defaultRegion}}, true)
	if strings.Contains(out.String(), setupAskProject) {
		t.Errorf("the gate asked the project's question, which is the wizard's own:\n%s", out)
	}
	if !e.r.o.createProject || e.r.o.name != "aurora" || e.r.o.cloud.gcpProject != "proj-1234" {
		t.Errorf("the answer about the repository changed the project's answers: create %v name %q project %q",
			e.r.o.createProject, e.r.o.name, e.r.o.cloud.gcpProject)
	}
	// The reverse: the wizard's own question flow never asks this one. Its
	// rows are the first-run inputs, and a repository is not one of them — the
	// question needs a checkout, and the answer is asked where the checkout is
	// read.
	for _, q := range setupQuestions {
		if strings.Contains(strings.ToLower(q.label), "nboard") || strings.Contains(q.flag, "onboard") {
			t.Errorf("%s is the wizard's own table row, and onboarding the checkout's repository is the gate's question", q.label)
		}
	}
}
