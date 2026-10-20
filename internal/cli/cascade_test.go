package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// cascadeCLI is the fugaro.yaml a repository asking for the cascade writes:
// agent.auth: cascade and nothing else about it. What the cascade is *for* is
// the operator's answer (O2) and lives in the project policy, never here.
const cascadeCLI = `version: 1
project: aurora
git: { provider: github }
agent:
  auth: cascade
  model: claude-sonnet-5-5
  models: { background: claude-haiku-4-5 }
workflows:
  app: { commands: { build: sh build.sh, test: sh test.sh } }
`

// cascadePolicy is the published policy that turns the cascade on for aurora's
// cascade workflows: O3's spelling, routing.claude.via, which needs no provider
// route while tier C for Claude is off (C3.6). The version is the one this
// binary's reader expects, read from the package rather than restated.
func cascadePolicy() string {
	return fmt.Sprintf(`{"version":%d,"allow_repos":["*"],"routing":{"claude":{"via":"cascade"}}}`, projectpolicy.RulesVersion)
}

// TestValidateRefusesCascadeWithoutPolicySwitch is O2 — the owner's ruling of
// 2026-10-17: a repository may *ask* for the cascade, and only the
// operator-written project policy enables it. `fugaro validate` is the surface
// that tells the repository's author so, before any run: a cascade file whose
// published policy does not carry the switch is a problem naming exactly what
// to set and that only the operator can set it. A policy the command cannot
// read at all (none published here, no database to read one from) is refused
// the same way rather than passed silently: a cascade workflow nobody has
// enabled is not a valid file, whichever side failed to answer.
//
// Mutation (run, restored), one per refusal this function makes: drop the
// `!readable` early return and the "no project config" subtest fails; judge
// the gateway rule from the file's own mode before readability is known
// (config.CheckCascadeGateway over policy.Merge(policy.Layer{}, …)) and the
// same subtest fails on the gateway line it may not carry; drop the `refuse`
// call from the `!found || p == nil` case and both the "no policy published"
// and the "no database" subtests fail (they would report the file valid);
// make the routing case unreachable and the "published policy without the
// switch" subtest fails. What is left is the case that must pass — the
// published switch — and the other auth, which never reaches any of them.
func TestValidateRefusesCascadeWithoutPolicySwitch(t *testing.T) {
	t.Run("a published policy without the switch", func(t *testing.T) {
		f := newLaunchBudget(t, "observe")
		publishPolicy(t, f, `{"version":`+fmt.Sprint(projectpolicy.RulesVersion)+`,"allow_repos":["*"]}`)
		out, _, err := execute(t, "validate", writeConfig(t, cascadeCLI))
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit = %d, want %d (err %v, out %q)", ExitCode(err), ExitUserError, err, out)
		}
		for _, want := range []string{
			"agent.auth",
			"routing.claude: {via: cascade}",
			"only the operator",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, out)
			}
		}
	})

	t.Run("the policy's switch is published", func(t *testing.T) {
		f := newLaunchBudget(t, "observe")
		publishPolicy(t, f, cascadePolicy())
		out, errOut, err := execute(t, "validate", writeConfig(t, cascadeCLI))
		if err != nil {
			t.Fatalf("validate: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
		}
		if !strings.Contains(out, "is valid") {
			t.Fatalf("the switch was published and the file was not valid:\n%s\n%s", out, errOut)
		}
	})

	t.Run("no policy published", func(t *testing.T) {
		f := newLaunchBudget(t, "observe")
		markRules(t, f, projectpolicy.RulesVersion)
		out, _, err := execute(t, "validate", writeConfig(t, cascadeCLI))
		if ExitCode(err) != ExitUserError || !strings.Contains(out, "routing.claude: {via: cascade}") {
			t.Fatalf("an unpublished policy passed a cascade file: exit %d, out %q, err %v", ExitCode(err), out, err)
		}
	})

	t.Run("no database to read one from", func(t *testing.T) {
		// The project config names no database URL at all: there is no
		// published policy this command could see, and it says so rather
		// than reading the silence as an enabled cascade.
		path := budgetProject(t, "budget: { mode: observe, per_run_usd: 5 }\n", "  auth: cascade")
		out, _, err := execute(t, "validate", path)
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit = %d, want %d (out %q, err %v)", ExitCode(err), ExitUserError, out, err)
		}
		for _, want := range []string{"agent.auth", "routing.claude: {via: cascade}", "only the operator"} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, out)
			}
		}
	})

	t.Run("no project config for this project is selected", func(t *testing.T) {
		// The checkout names a project this machine has no config for: there
		// is no policy this command could read, and it refuses the file
		// naming the project whose config it would have read one from — the
		// state a repository's author is in on a machine that is not the
		// operator's.
		isolateProjects(t, t.TempDir())
		out, _, err := execute(t, "validate", writeConfig(t, cascadeCLI))
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit = %d, want %d (out %q, err %v)", ExitCode(err), ExitUserError, out, err)
		}
		for _, want := range []string{"agent.auth", "aurora", "routing.claude: {via: cascade}", "only the operator"} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal does not say %q:\n%s", want, out)
			}
		}
		// And the O2 refusal is the whole of it here: with no project config
		// selected this command cannot read the installation's ceiling either,
		// and the file's own mode is only a lower bound on the run's — a file
		// that writes no budget under a ceiling that enforces has the gateway
		// on. So the gateway rule is not judged from it: the line this state
		// used to carry was the file's own mode read as the run's, a problem no
		// run of this configuration necessarily has.
		if strings.Contains(out, "needs the budget gateway") {
			t.Errorf("the gateway rule was judged without the ceiling that decides the run's mode:\n%s", out)
		}
	})

	t.Run("the selected project config could not be read", func(t *testing.T) {
		// The other state selectedProjectConfigErr answers nil for: the config
		// is selected and was refused at load (its switch names a model no
		// entry claims), so there is no policy this command could read. The
		// refusal says that, and not "no project config for aurora is selected
		// here", which is false of it — the reason is the problem the loader's
		// own line carries.
		isolateProjects(t, t.TempDir())
		projectRouting(t, routedSwitchBlock)
		out, _, err := execute(t, "validate", writeConfig(t, cascadeCLI))
		if ExitCode(err) != ExitUserError {
			t.Fatalf("exit = %d, want %d (out %q, err %v)", ExitCode(err), ExitUserError, out, err)
		}
		if !strings.Contains(out, "agent.auth: is refused: the project config selected here could not be read") {
			t.Errorf("the refusal does not name the state it is in:\n%s", out)
		}
		if strings.Contains(out, "no project config for aurora is selected here") {
			t.Errorf("a config that is there and unreadable is reported as none selected:\n%s", out)
		}
	})

	t.Run("another auth is not this rule's business", func(t *testing.T) {
		f := newLaunchBudget(t, "observe")
		publishPolicy(t, f, `{"version":`+fmt.Sprint(projectpolicy.RulesVersion)+`,"allow_repos":["*"]}`)
		other := strings.Replace(cascadeCLI, "auth: cascade", "auth: api-key", 1)
		out, errOut, err := execute(t, "validate", writeConfig(t, other))
		if err != nil {
			t.Fatalf("an api-key file was refused for want of the cascade switch: %v\n%s\n%s", err, out, errOut)
		}
	})
}

// TestValidateRefusesCascadeWithBudgetOff is the gateway rule at the surface
// that reports it: a cascade run's calls are made by the gateway, so the mode
// that reaches the run — the effective one, policy.Merge of the installation's
// ceiling and the file — has to be observe or enforce. Here the ceiling is off
// and the file says off too, which is the state the refusal names.
//
// Mutation (run, restored): judge runner.FileLayer(cfg).Mode alone, without
// the ceiling — the last part of this test fails, refusing a cascade file
// whose installation observes, which is the file's own mode read as the
// run's.
func TestValidateRefusesCascadeWithBudgetOff(t *testing.T) {
	f := newLaunchBudget(t, "off")
	publishPolicy(t, f, cascadePolicy())
	path := writeConfig(t, strings.Replace(cascadeCLI, "workflows:", "budget: { mode: off }\nworkflows:", 1))
	out, _, err := execute(t, "validate", path)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit = %d, want %d (out %q, err %v)", ExitCode(err), ExitUserError, out, err)
	}
	for _, want := range []string{"agent.auth", "budget.mode", "off"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "routing.claude: {via: cascade}") {
		t.Errorf("the gateway refusal also claimed the policy does not enable the cascade, which it does:\n%s", out)
	}

	// The mode judged is the run's effective one, not the file's: a file that
	// writes off under a ceiling that observes still runs with the gateway on
	// (policy.Merge keeps the stricter of the two), so its cascade workflow is
	// valid. A check reading the file's own mode would refuse it.
	observing := newLaunchBudget(t, "observe")
	publishPolicy(t, observing, cascadePolicy())
	out, errOut, err := execute(t, "validate", path)
	if err != nil {
		t.Fatalf("a cascade file whose installation observes was refused: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
}

// TestValidateCascadeSwitchReasonNeverBlamesTheGateway: a cascade run's
// gateway is the credentials service's, not a model API's credential, and
// runner.GatewayOn answers false for `cascade` because it knows api-key and
// vertex only (the change that teaches it the value is plan Task C1.19). What
// the switch's reach makes of a cascade run is therefore right today — no run
// of this configuration has a gateway, so the pins keep Claude's own IDs —
// but the reason validate printed for it was not: "the gateway needs a model
// API's credential" is false of a cascade run, whose whole point is a gateway
// carrying a credential that is not the job's own (D133, D143).
//
// The state is the one the switch's reach is asked about: the project config's
// switch names a Claude model this checkout carries no route for, so the pin
// has no fall-back (D1b) and the line says what the run does with it.
//
// Mutation (run, restored): drop the cascade arm from routingInertWhy's
// RoutingInertNoGateway case — the line then blames the gateway's credential
// rule and this test fails on that sentence.
func TestValidateCascadeSwitchReasonNeverBlamesTheGateway(t *testing.T) {
	f := newLaunchBudget(t, "enforce")
	publishPolicy(t, f, cascadePolicy())
	// The switch is the operator's own file, and it names a model only a route
	// this repository may not send code to claims (routedProjectBlock admits
	// acme/sandbox; the checkout is acme/other).
	f.appendConfig(t, routedProjectBlock+routedSwitchBlock)
	inCheckoutOf(t, "acme/other")
	out, errOut, err := execute(t, "validate", writeConfig(t, cascadeCLI))
	if err != nil {
		t.Fatalf("validate refused a configuration whose runs do not reach the switch: %v\n%s%s", err, out, errOut)
	}
	if !strings.Contains(errOut, "routing.claude") {
		t.Fatalf("want the note about the switch's reach:\n%s%s", out, errOut)
	}
	if strings.Contains(out+errOut, "the gateway needs a model API's credential") {
		t.Fatalf("the reason blames a gateway rule that is false of a cascade run:\n%s%s", out, errOut)
	}
	if !strings.Contains(errOut, "credentials service") {
		t.Fatalf("the reason does not name the gateway a cascade run's calls are carried by:\n%s%s", out, errOut)
	}
	if strings.Contains(out, "the project's routing switch is not usable") {
		t.Fatalf("the halt is asserted for a run that does not reach the switch:\n%s", out)
	}
}

// shiftRelease returns rel with a release number moved by delta: a release
// below cascadeSince is the image the gate has to refuse, and one above it the
// image it has to pass. Written as a shift of the constant rather than as a
// literal so the boundary this test pins is the constant's own: a row written
// as "0.9.5" beside a constant of "0.9.6" is the same number typed twice, and
// it stays green when the constant moves — which is the skew the test exists
// to catch.
//
// The constant's own patch series takes precedence — 0.9.6's neighbours are
// 0.9.5 and 0.9.7 — and a release that opens its minor (a patch of 0, as
// 0.10.0 does) has no patch below it at all, so the minor moves instead and
// the patch stays: 0.9.0 below, 0.11.0 above. Both are releases the gate's own
// numeric compare puts on the right side of the constant, which is all this
// fixture has to be; a constant whose minor cannot shift either is not a
// release this gate can judge, and that is what the fatal says.
func shiftRelease(t *testing.T, rel string, delta int) string {
	t.Helper()
	parts := strings.Split(rel, ".")
	if len(parts) != 3 {
		t.Fatalf("cascadeSince = %q is not a X.Y.Z release, so no boundary can be read from it", rel)
	}
	major, minor, patch := 0, 0, 0
	var err error
	if major, err = strconv.Atoi(parts[0]); err != nil {
		t.Fatalf("cascadeSince = %q is not a X.Y.Z release, so no boundary can be read from it", rel)
	}
	if minor, err = strconv.Atoi(parts[1]); err != nil {
		t.Fatalf("cascadeSince = %q is not a X.Y.Z release, so no boundary can be read from it", rel)
	}
	if patch, err = strconv.Atoi(parts[2]); err != nil {
		t.Fatalf("cascadeSince = %q is not a X.Y.Z release, so no boundary can be read from it", rel)
	}
	switch {
	case patch+delta >= 0:
		return fmt.Sprintf("%d.%d.%d", major, minor, patch+delta)
	case minor+delta >= 0:
		return fmt.Sprintf("%d.%d.%d", major, minor+delta, patch)
	}
	t.Fatalf("cascadeSince = %q has no release %d below/above it", rel, delta)
	return ""
}

// TestInitRepoRefusesCascadeOnOlderBaseImage is Task C1.1's skew gate: a
// fugaro.yaml value is a version-skew break in a strictly decoded config, so
// `fugaro init --repo` refuses to deploy a cascade repository against an
// installation whose base image predates the release that reads the value —
// before any cloud effect. cascadeSince is the release that ships it: an image
// at it (or past it) passes, a ref that is not a release image is only warned
// about (nothing can tell what its runner knows), and an installation with no
// base image at all passes, since its job is built from this release's own
// image.
//
// The boundary is the constant's own (shiftRelease): the image refused is one
// release below cascadeSince and the images that pass are cascadeSince itself
// and one release above it, so a constant moved to a release no base image
// could be — or one that no longer sits where the number in docs/release.md
// and the schemas says — is what this test's rows are written to catch rather
// than to follow.
//
// Mutation (run, restored): move cascadeSince below the base image's release
// (0.9.4) and both this test's refusal and the repoEngine one fail — the gate
// passes an image whose runner has never heard of the value; refuse the empty
// base image and the "" row fails; drop the warning in the `!ok` case and the
// dev-ref row fails, with an installation whose base image cannot be judged
// passing in silence.
func TestInitRepoRefusesCascadeOnOlderBaseImage(t *testing.T) {
	cascade := &config.Config{Agent: config.Agent{Auth: config.AuthCascade}}
	below, above := shiftRelease(t, cascadeSince, -1), shiftRelease(t, cascadeSince, 1)
	lc := func(ref string) *localcfg.Config {
		if ref == "" {
			return &localcfg.Config{}
		}
		return &localcfg.Config{BaseImages: map[string]string{"base": ref}}
	}
	var out, errOut bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	r := &initRun{o: &initOptions{}, cmd: cmd, w: &out}

	older := "ghcr.io/dimipaun/fugaro-base:" + below
	err := r.gateCascadeAuth(r.o, "acme/app", lc(older), cascade, config.SourceRepo)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v, want refused", err)
	}
	for _, want := range []string{
		"fugaro " + cascadeSince + " or later",
		older + ", release " + below,
		"fugaro init --base base",
		"agent.auth: cascade",
		// The subject names where this row's value came from (SourceRepo):
		// the same gate under a layer's default says so instead (see
		// TestCascadeSkewNamesTheLayerTheValueCameFrom).
		"fugaro.yaml's agent.auth: cascade",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want none (refused, not warned)", errOut.String())
	}

	// A base image at cascadeSince or past it passes, and one that is not a
	// release image only warns.
	for _, ref := range []string{"ghcr.io/dimipaun/fugaro-base:" + cascadeSince, "ghcr.io/dimipaun/fugaro-base:" + above} {
		var warn bytes.Buffer
		if err := refuseCascadeSkew("acme/app", config.AuthCascade, config.SourceRepo, ref, &warn); err != nil || warn.Len() != 0 {
			t.Errorf("%s: err = %v, warn = %q, want neither", ref, err, warn.String())
		}
	}
	for _, ref := range []string{"", "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-1a2b3c"} {
		var warn bytes.Buffer
		if err := refuseCascadeSkew("acme/app", config.AuthCascade, config.SourceRepo, ref, &warn); err != nil {
			t.Errorf("%q: err = %v, want none", ref, err)
		}
		if ref != "" && (!strings.Contains(warn.String(), "not a release base image") || !strings.Contains(warn.String(), "is not checked")) {
			t.Errorf("%q: warn = %q", ref, warn.String())
		}
	}

	// Another auth asks nothing of the image: the gate is about the value the
	// image has to read, and a vertex file is read by every release.
	for _, auth := range []string{"vertex", "api-key", "oauth"} {
		var warn bytes.Buffer
		err := refuseCascadeSkew("acme/app", auth, config.SourceRepo, older, &warn)
		if err != nil || warn.Len() != 0 {
			t.Errorf("auth %q: err = %v, warn = %q, want neither", auth, err, warn.String())
		}
	}

	// --forget tears the repository down and --plan-only shows the plan
	// without applying it: neither deploys the value, so neither is refused
	// (the same escape the route-switch gate leaves). --print-vars is judged
	// like a deploy would be — the values it prints are the ones a deploy
	// would use — and nothing of it lands on stdout.
	old := lc(older)
	if err := r.gateCascadeAuth(r.o, "acme/app", old, cascade, config.SourceRepo); err == nil {
		t.Fatal("a plain deploy was not refused")
	}
	for _, o := range []*initOptions{{planOnly: true}, {forget: true}} {
		if err := (&initRun{o: o, cmd: cmd, w: &out}).gateCascadeAuth(o, "acme/app", old, cascade, config.SourceRepo); err != nil {
			t.Errorf("%+v: err = %v, want none", o, err)
		}
	}
	out.Reset()
	pv := &initOptions{printVars: true}
	if err := (&initRun{o: pv, cmd: cmd, w: io.Discard}).gateCascadeAuth(pv, "acme/app", old, cascade, config.SourceRepo); err == nil {
		t.Fatal("--print-vars: want refused")
	}
	if out.Len() != 0 {
		t.Errorf("--print-vars wrote to stdout: %q", out.String())
	}
}

// TestCascadeSkewNamesTheLayerTheValueCameFrom: the subject of both messages
// names the thing that put the cascade in effect, and a resolved value is not
// proof that the repository's own file asked for it. agent.auth resolves like
// every other key — the repository's value, else the workflow's profile's,
// else the project layer's defaults (config.Resolve) — so an installation
// whose layer defaults agent.auth to cascade runs every repository it covers
// through the cascade with no fugaro.yaml saying so, and a subject that
// always said "fugaro.yaml's" would send that operator to a line their file
// does not have (the misattribution annotate() avoids on a problem, and
// doctor's oauth-token-allowed check avoids in its message).
//
// src is config.Resolution.SourceOf("agent.auth"), which is what repoEngine
// passes; a source this cannot name is named rather than guessed at, so a
// value that reaches the config some new way is still not attributed to a
// file. The pair of messages is checked through refuseCascadeSkew, not just
// cascadeSubject: the warning is the other place the subject is printed.
//
// Mutation (run, restored): make cascadeSubject ignore src and always return
// the fugaro.yaml wording — the layer rows fail, and the refusal tells the
// operator of a layered installation to edit a line their file does not
// have. Mutation (pass config.SourceRepo where repoEngine has
// rf.Res.SourceOf("agent.auth")): TestRepoEngineNamesTheProjectLayerThatAsksForTheCascade
// fails, which is the call site this test cannot see.
func TestCascadeSkewNamesTheLayerTheValueCameFrom(t *testing.T) {
	older := "ghcr.io/dimipaun/fugaro-base:" + shiftRelease(t, cascadeSince, -1)
	for _, c := range []struct{ src, subject string }{
		{config.SourceRepo, "fugaro.yaml's agent.auth: " + config.AuthCascade + " of acme/app"},
		{config.SourceProject, "the project layer's defaults.agent.auth: " + config.AuthCascade + " of acme/app"},
	} {
		if got := cascadeSubject("acme/app", c.src); got != c.subject {
			t.Errorf("cascadeSubject(%q) = %q, want %q", c.src, got, c.subject)
		}
		err := refuseCascadeSkew("acme/app", config.AuthCascade, c.src, older, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), c.subject) {
			t.Errorf("src %q: the refusal does not name %q: %v", c.src, c.subject, err)
		}
		var warn bytes.Buffer
		if err := refuseCascadeSkew("acme/app", config.AuthCascade, c.src, "not-a-release-ref", &warn); err != nil || !strings.Contains(warn.String(), c.subject) {
			t.Errorf("src %q: the warning does not name %q (err = %v, warn = %q)", c.src, c.subject, err, warn.String())
		}
	}
	// The value the layer set is never blamed on the repository's file, and a
	// source this cannot name says so instead of picking one.
	for _, src := range []string{config.SourceProject, config.SourceProfile("shared"), config.SourceDefault} {
		got := cascadeSubject("acme/app", src)
		if strings.Contains(got, "fugaro.yaml") {
			t.Errorf("cascadeSubject(%q) = %q: a value this source set is attributed to the repository's own file", src, got)
		}
	}
	if got := cascadeSubject("acme/app", config.SourceProfile("shared")); !strings.Contains(got, "shared") {
		t.Errorf("cascadeSubject(SourceProfile) = %q: an unnamed source is not named at all", got)
	}
}

// TestCascadeSinceIsTheReleaseTheDocsAndSchemasName: the constant's number is
// stated to three other readers, and each of them acts on it. docs/release.md's
// "Before you tag" bullet is what a release verifies the constant against
// ("currently `\"0.10.0\"`"), and the two schemas' agent.auth descriptions tell
// an author editing a fugaro.yaml which release stops refusing the value
// ("Fugaro before 0.10.0 refuses the value"). A constant that moves alone leaves
// one of those readers a number that is not the one the gate judges with — the
// release checklist verifying a release that does not read the value, or a
// repository author told their file is fine on an image whose runner refuses it
// — and nothing else in this package reads any of the three.
//
// The boundary the gate itself judges is pinned in the test above, off the
// constant; this is the other half: the number a person reads is the number the
// gate uses, so moving the constant to a release that no base image can be, or
// to one the docs do not name, fails here rather than silently refusing every
// cascade install.
//
// Mutation (run, restored): change cascadeSince to "0.10.1" and nothing else —
// this fails naming the release checklist and both schemas, which still say
// 0.10.0.
func TestCascadeSinceIsTheReleaseTheDocsAndSchemasName(t *testing.T) {
	var bullet string
	for _, line := range strings.Split(readDoc(t, "docs/release.md"), "\n") {
		if strings.Contains(line, "`cascadeSince`") {
			bullet = line
			break
		}
	}
	if bullet == "" {
		t.Fatal("docs/release.md's Before-you-tag list has no cascadeSince bullet: the checklist is where a release verifies the constant")
	}
	if !strings.Contains(bullet, "currently `\""+cascadeSince+"\"`") {
		t.Errorf("docs/release.md's cascadeSince bullet does not carry the constant's release (%q): the checklist would verify a release the gate does not judge with", cascadeSince)
	}
	// The schemas' own line, not the file: the enum line is the one that
	// carries the description an editor of fugaro.yaml or a project layer reads.
	for _, path := range []string{"../../schemas/fugaro.schema.json", "../../schemas/project-layer.schema.json"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var line string
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, `"enum"`) && strings.Contains(l, `"cascade"`) {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("%s: no agent.auth enum line names cascade", path)
		}
		if !strings.Contains(line, "before "+cascadeSince) {
			t.Errorf("%s's agent.auth description does not say Fugaro before %s refuses the value: the schema tells an author a release the gate does not judge with", path, cascadeSince)
		}
	}
}

// TestCascadeSinceIsTheReleaseTheCredentialsServiceShipsIn: the four readers
// above are held to the constant, and this one holds the constant to the
// release the credentials service ships in — 0.10.0, which is not tagged yet
// and is where the owner's ruling of 2026-10-20 places the whole service (this
// value included; the line named 0.9.6 while the next release was expected to
// be 0.9.6, and the owner renumbered it to 0.10.0).
//
// It is written out rather than read from anything because nothing else in the
// tree knows which release this is: the constant, docs/release.md's bullet and
// both schemas' descriptions can move together to a number that is not the
// ruled one and stay green in TestCascadeSinceIsTheReleaseTheDocsAndSchemasName,
// which compares them with each other. What a wrong number costs is the gate
// itself: a cascade repository on an installation whose base image is 0.9.5 or
// older would be refused by a constant naming a release that ships later than
// the one that actually reads the value, and admitted by one naming an earlier
// release, in both cases against the release the owner ruled for.
//
// Mutation (run, restored): change cascadeSince to "0.11.0" — this fails, and
// the docs/schemas test above stays green with the labels moved to match.
func TestCascadeSinceIsTheReleaseTheCredentialsServiceShipsIn(t *testing.T) {
	if cascadeSince != "0.10.0" {
		t.Errorf("cascadeSince = %q: the credentials service ships in 0.10.0 (the owner's ruling of 2026-10-20), so the gate would judge the base image against a release the value does not ship in", cascadeSince)
	}
}

// TestRepoEngineRefusesCascadeOnAnOlderBaseImage is the gate at its call site:
// the checks above test gateCascadeAuth in isolation, and this one pins that
// repoEngine reaches it at all — before the plan, the apply or any other cloud
// call — from a checkout that asks for the cascade against an installation
// whose base image predates cascadeSince. A gate nothing calls is a comment.
//
// Mutation (run, restored): drop the gateCascadeAuth call from repoEngine —
// this test then fails on the refusal's own words, and what it is left
// holding is the state bucket the run never reaches (the check above would
// not have noticed: it calls the gate itself).
func TestRepoEngineRefusesCascadeOnAnOlderBaseImage(t *testing.T) {
	below := shiftRelease(t, cascadeSince, -1)
	r := newInitRig(t)
	r.appendConfig(t, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:"+below+"}\n")
	r.save(t)
	isolateCache(t)
	noProjectLayerBucket(t)
	yaml := "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: bitbucket }\n" +
		"agent: { auth: cascade }\nworkflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", yaml)
	t.Chdir(dir)
	e := rigEngine(t, r, &initOptions{})
	err := e.r.repoEngine(t.Context(), ".", "", true)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit = %d, err = %v: want the cascade refused on this installation's base image", ExitCode(err), err)
	}
	for _, want := range []string{cascadeSince, below, "fugaro init --base base", "agent.auth: cascade", "fugaro.yaml's agent.auth: cascade"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if applies := r.ran(t, "apply"); len(applies) != 0 {
		t.Errorf("the refusal applied anyway: %v", applies)
	}
}

// TestRepoEngineNamesTheProjectLayerThatAsksForTheCascade is the subject's
// call site: the repository's fugaro.yaml here sets no agent.auth at all, and
// the project layer it resolves over defaults it to cascade, so the cascade
// is in effect without the file asking for it. repoEngine has to hand the
// gate the source of the value it read (rf.Res.SourceOf), not the fact that
// the value exists: a gate told "the file" would send this operator to edit a
// line their fugaro.yaml does not have, when the fix is in the layer or in
// an agent.auth line they would add to their own file.
//
// The layer is the one findLayer reads (the local config's own bucket_url,
// written as a file:// bucket with config.LayerKey in it), the same seam
// doctor's layer tests use, so the resolution really runs over it.
//
// Mutation (run, restored): pass config.SourceRepo instead of
// rf.Res.SourceOf("agent.auth") in repoEngine — this test fails on the
// refusal naming fugaro.yaml for a value the file does not carry, and
// cascadeSubject's own test cannot see it (it calls the function directly).
func TestRepoEngineNamesTheProjectLayerThatAsksForTheCascade(t *testing.T) {
	below := shiftRelease(t, cascadeSince, -1)
	r := newInitRig(t)
	isolateCache(t)
	runs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(runs, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	layer := "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  agent: { auth: cascade }\n"
	if err := os.WriteFile(filepath.Join(runs, config.LayerKey), []byte(layer), 0o644); err != nil {
		t.Fatal(err)
	}
	r.appendConfig(t, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:"+below+"}\nbucket_url: file://"+runs+"\n")
	r.save(t)
	yaml := "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: bitbucket }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", yaml)
	t.Chdir(dir)
	e := rigEngine(t, r, &initOptions{})
	err := e.r.repoEngine(t.Context(), ".", "", true)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit = %d, err = %v: want the layer's cascade refused on this installation's base image", ExitCode(err), err)
	}
	got := err.Error()
	for _, want := range []string{cascadeSince, "fugaro init --base base", "agent.auth: cascade", "the project layer's defaults.agent.auth: cascade"} {
		if !strings.Contains(got, want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if strings.Contains(got, "fugaro.yaml's agent.auth") {
		t.Errorf("the refusal blames the repository's file for a value the layer set: %v", err)
	}
	if applies := r.ran(t, "apply"); len(applies) != 0 {
		t.Errorf("the refusal applied anyway: %v", applies)
	}
}
