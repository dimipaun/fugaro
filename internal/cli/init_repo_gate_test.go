package cli

import (
	"strings"
	"testing"
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

// init --repo on its own is the converge's hostile-checkout gate too: a
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
			out, errOut, err := executeStdin(t, tc.stdin, append([]string{"init", "--repo", "--github-app-id", "42"}, append(tc.args, dir)...)...)
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
	_, _, err := executeStdin(t, "acme/hostile\n", "init", "--repo", "--github-app-id", "42", dir)
	if err != nil && strings.Contains(err.Error(), gate) {
		t.Errorf("the typed opt-in did not pass the gate: %v", err)
	}
	pastGate(t, err)
	_, dir = hostileRepoRig(t, "https://github.com/acme/hostile.git")
	_, _, err = executeStdin(t, "", "init", "--repo", "--yes", "--github-app-id", "42", "--onboard-repo", "acme/hostile", dir)
	if err != nil && strings.Contains(err.Error(), gate) {
		t.Errorf("--onboard-repo did not pass the gate: %v", err)
	}
	pastGate(t, err)
	// A listed repository is as before: no gate.
	r := newInitRig(t)
	r.stateBucket()
	dir = repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", strings.Replace(checkoutYAML("bitbucket", "api-key", "aurora", ""), "app:", "web:", 1))
	t.Chdir(t.TempDir())
	_, _, err = executeStdin(t, "", "init", "--repo", "--yes", dir)
	if err != nil && strings.Contains(err.Error(), gate) {
		t.Errorf("a listed repository was gated: %v", err)
	}
	pastGate(t, err)
	// --plan-only changes nothing and is not gated.
	_, dir = hostileRepoRig(t, "https://github.com/acme/hostile.git")
	_, _, err = executeStdin(t, "", "init", "--repo", "--plan-only", "--github-app-id", "42", dir)
	if err != nil && strings.Contains(err.Error(), gate) {
		t.Errorf("--plan-only was gated: %v", err)
	}
}

// --onboard-repo is refused outright in an agent's environment, for the
// converge and for --repo alike; and the needs-you for an agent session never
// prints it.
func TestOnboardRepoRefusedInAnAgentSession(t *testing.T) {
	for _, args := range [][]string{
		{"init", "--onboard-repo", "acme/app", "--yes"},
		{"init", "--repo", "--onboard-repo", "acme/app", "--yes"},
	} {
		t.Setenv("CLAUDECODE", "1")
		_, _, err := execute(t, args...)
		if err == nil || ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not accepted in a coding agent's session") || strings.Contains(err.Error(), "pass --onboard-repo") {
			t.Errorf("%v: %v", args, err)
		}
	}
	t.Setenv("CLAUDECODE", "")
	oi := originInfo{Repo: "acme/app", Host: "github.com"}
	if lf := onboardLeft(oi); !strings.Contains(strings.Join(lf.Commands, " ")+lf.Text, "init --onboard-repo") {
		t.Errorf("outside an agent session the flag line is the route: %+v", lf)
	}
	t.Setenv("CLAUDECODE", "1")
	lf := onboardLeft(oi)
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
