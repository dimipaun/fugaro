package cli

import (
	"bufio"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
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

// gateRig is the start of a converge: the engine, the checkout the gate asks
// about, and the question gateEarly asks it, with a terminal this test's and
// stdout collected. The local config lists acme/sandbox (bitbucket) and
// nothing else, so an origin of a github.com repository is the unlisted case
// the gate exists for. Nothing here reaches a cloud: gateEarly asks before the
// first stage plans.
func gateRig(t *testing.T, door, origin, yaml, stdin string, o *initOptions) (*initEngine, *strings.Builder) {
	t.Helper()
	r := newInitRig(t)
	r.stateBucket()
	dir := repoCheckout(t, origin, yaml)
	t.Chdir(dir)
	fakeTerminal(t)
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
	e, out := gateRig(t, setupDoor.name, gateOrigin(), gateCheckoutYAML(), "\n"+gateRepo+"\n", nil)
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
	e, out = gateRig(t, setupDoor.name, gateOrigin(), gateCheckoutYAML(), onboardAnswerNo+"\n", nil)
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

// TestSetupStillTypesAnUnlistedRepositoryUnderYes is today's row, on purpose
// (W3.4): onboarding a repository the run was not told about is Typed before
// 2026-10-20 and stays so until W5.1 lands, which flips the call site. The
// wizard's question does not stand in for it, and --yes does not cover it.
func TestSetupStillTypesAnUnlistedRepositoryUnderYes(t *testing.T) {
	r, dir := hostileRepoRig(t, "https://github.com/acme/hostile.git")
	out, errOut, err := executeStdin(t, "", "setup", "--repo", "--github-app-id", "42", "--yes", dir)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v:\n%s%s", ExitCode(err), err, out, errOut)
	}
	noCloudTouched(t, r, "an unlisted repository under --yes")
	all := out + errOut + err.Error()
	if !strings.Contains(all, "Type acme/hostile to onboard it") && !strings.Contains(all, "acme/hostile") {
		t.Errorf("the refusal does not name the repository whose owner/name is typed:\n%s", all)
	}
	if !strings.Contains(all, "--onboard-repo") {
		t.Errorf("the refusal does not name the one-line opt-in:\n%s", all)
	}
}

// TestSetupOnboardingIsASeparateAnswerFromTheProjectQuestion: the wizard asks
// two questions about two different things, and neither answers the other. The
// project's is the wizard's own (setup.go's askProjectAnswer, asked with the
// other first-run inputs); this one is the gate's, asked at the start of the
// converge about the checkout's repository (W3.4, D5). A run whose answer here
// is no still carries the project answers it was given.
func TestSetupOnboardingIsASeparateAnswerFromTheProjectQuestion(t *testing.T) {
	e, out := gateRig(t, setupDoor.name, gateOrigin(), gateCheckoutYAML(), onboardAnswerNo+"\n",
		&initOptions{createProject: true, name: "aurora", cloud: cloudOptions{gcpProject: "proj-1234", region: defaultRegion}})
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
