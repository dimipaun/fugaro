package images_test

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// credentialsStage returns the instructions of the credentials Dockerfile
// after its last FROM, with the FROM line first.
func credentialsStage(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("credentials/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if strings.HasPrefix(l, "FROM ") {
			lines = lines[:0]
		}
		lines = append(lines, l)
	}
	return lines
}

// The credentials service holds every model credential the installation has:
// its image is the binary in a distroless static image, running as a numeric
// non-root user, with no step that could add a shell, a package or a
// credential. The bundle secret is read at run time as the service's own
// account and is never baked into a layer.
func TestCredentialsImageIsMinimal(t *testing.T) {
	stage := credentialsStage(t)
	if !regexp.MustCompile(`^FROM gcr\.io/distroless/static-debian12:nonroot@sha256:[0-9a-f]{64}$`).MatchString(stage[0]) {
		t.Errorf("final stage is %q, want distroless static nonroot pinned by digest", stage[0])
	}
	var user string
	for _, l := range stage[1:] {
		switch kw, _, _ := strings.Cut(l, " "); kw {
		case "COPY":
			if !strings.HasPrefix(l, "COPY --from=fugaro /out/fugaro ") {
				t.Errorf("final stage copies %q; only the fugaro binary belongs in the image", l)
			}
		case "USER":
			user = strings.TrimSpace(strings.TrimPrefix(l, "USER "))
		case "ENTRYPOINT", "CMD":
		default:
			t.Errorf("final stage has %q; it may only COPY the binary, set USER, ENTRYPOINT and CMD", l)
		}
	}
	if !regexp.MustCompile(`^[1-9][0-9]*(:[1-9][0-9]*)?$`).MatchString(user) {
		t.Errorf("USER = %q, want a numeric non-root user", user)
	}
}

// TestCredentialsImageBasesArePinnedByDigest: both of this image's bases --
// the golang builder and the distroless static layer the service runs -- are
// pinned by digest, the way images/base/Dockerfile pins Debian. A tag is a
// name the registry may repoint between two builds of the same commit, and a
// rebuild from the same commit could then produce different bytes than the
// image whose digest the release signed and `fugaro update` pinned into the
// service's Terraform variable. This is the test that keeps a tag from coming
// back: it names both bases, so it also fails if a FROM is dropped or its
// repository is swapped.
func TestCredentialsImageBasesArePinnedByDigest(t *testing.T) {
	b, err := os.ReadFile("credentials/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	var froms []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "FROM ") {
			froms = append(froms, strings.TrimSpace(l))
		}
	}
	want := []string{
		"golang:1.27-bookworm@sha256:",
		"gcr.io/distroless/static-debian12:nonroot@sha256:",
	}
	if len(froms) != len(want) {
		t.Fatalf("credentials/Dockerfile has %d FROM lines (%v), want %d", len(froms), froms, len(want))
	}
	for i, w := range want {
		if !strings.Contains(froms[i], w) {
			t.Errorf("FROM %q does not name %q", froms[i], w)
		}
		if !regexp.MustCompile(`@sha256:[0-9a-f]{64}( AS [A-Za-z0-9_.-]+)?$`).MatchString(froms[i]) {
			t.Errorf("FROM %q is not pinned by a full sha256 digest: a tag would let the same commit build different bytes", froms[i])
		}
	}
}

// TestCredentialsImageRunsTheServeCommand: the service is a Cloud Run
// *service*, so its container sets neither a command nor args and the image's
// own entrypoint is what runs (internal/infra/tf's google_cloud_run_v2_service
// case refuses a plan whose container sets either). The CMD therefore has to
// be exactly the hidden `credentials serve` subcommand -- a Dockerfile whose
// CMD drifted to `version`, or that named a command the CLI does not have,
// would deploy a revision that answers nothing while looking healthy.
func TestCredentialsImageRunsTheServeCommand(t *testing.T) {
	stage := credentialsStage(t)
	var cmd string
	for _, l := range stage[1:] {
		if strings.HasPrefix(l, "CMD ") {
			cmd = strings.TrimSpace(strings.TrimPrefix(l, "CMD "))
		}
	}
	if cmd != `["credentials", "serve"]` {
		t.Fatalf("CMD = %s, want [\"credentials\", \"serve\"]", cmd)
	}
	// And the entrypoint is the binary itself, not a shell or a wrapper: the
	// image has neither.
	if !containsLine(stage, `ENTRYPOINT ["/usr/local/bin/fugaro"]`) {
		t.Errorf("the final stage's entrypoint is not the fugaro binary: %v", stage)
	}
	// The subcommand the CMD names is the one internal/cli registers and
	// hides: the two `Use:` strings internal/cli/credentials_serve.go declares,
	// in order (the group, then the subcommand it adds). A CMD that drifted to
	// `version`, or that named a command the CLI never registers, would deploy
	// a revision that answers nothing while looking healthy, and it would fail
	// only inside that revision -- so the comparison is against the strings the
	// registrations carry, not against words that merely appear in the file
	// (its own doc comment names both). TestCredentialsServeHidden is the same
	// claim from the CLI's own side: it walks the tree cobra builds.
	src, err := os.ReadFile("../internal/cli/credentials_serve.go")
	if err != nil {
		t.Fatal(err)
	}
	var uses []string
	for _, m := range credentialUseRE.FindAllSubmatch(src, -1) {
		uses = append(uses, string(m[1]))
	}
	if want := `["` + strings.Join(uses, `", "`) + `"]`; want != cmd {
		t.Errorf("CMD = %s, but internal/cli/credentials_serve.go registers %v: a CMD naming a subcommand the CLI does not register fails only inside the deployed revision", cmd, uses)
	}
}

// credentialUseRE matches a cobra command's registered name in
// internal/cli/credentials_serve.go: `Use: "credentials"`, `Use: "serve"`.
var credentialUseRE = regexp.MustCompile(`(?m)^\s*Use:\s*"([^"]+)"\s*,`)

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// build-base.sh builds any directory of images/ that has a Dockerfile, so the
// credentials image builds with images/build-base.sh credentials.
func TestCredentialsImageBuildsWithBuildBase(t *testing.T) {
	if _, err := os.Stat("credentials/Dockerfile"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("build-base.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"$root/images/$base/Dockerfile"`) {
		t.Fatal("build-base.sh no longer builds images/<base>/Dockerfile")
	}
}

// TestCredentialsSmokeScriptChecksTheEntrypoint: the smoke script is what
// proves the built image is the service's process -- non-root, no shell, no
// baked credential, and the entrypoint refusing by name without the module's
// environment. It must not assert the configured refusal (that `credentials
// serve` declines to start because the deployment's three names are not enough
// to build the service's configuration): that is a fact about the deployment
// rather than about the image, and would have to be rewritten the day the
// container carries the four facts internal/cli/credentials_serve.go's config
// builder needs, which is not what a smoke test is for.
func TestCredentialsSmokeScriptChecksTheEntrypoint(t *testing.T) {
	b, err := os.ReadFile("smoke-credentials.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	for _, want := range []string{
		`credentials serve`,   // the entrypoint the service runs
		`FUGARO_PROJECT`,      // the environment refusal, by name
		`{{.Config.User}}`,    // non-root
		`--entrypoint sh`,     // no shell
		`FUGARO_GCP_PROJECT`,  // no baked environment
		`*CREDENTIAL*`,        // nothing credential-shaped baked in
		`smoke-credentials: `, // its own ok line
	} {
		if !strings.Contains(script, want) {
			t.Errorf("smoke-credentials.sh lacks %q", want)
		}
	}
	if strings.Contains(script, "C1.11") {
		t.Error("smoke-credentials.sh pins the build-state refusal (that the service's own code is absent), which is not this build's state: internal/credsvc carries the service")
	}
}

// The credentials image's size budget, in MB of 10^6 bytes: the fugaro binary
// (about 56 MB with -s -w) in a distroless static layer (a few MB), with room
// for the binary to grow by decision. They are constants here rather than
// defaults in size-budget.sh for the reason the base image's own budget is
// passed on the command line: the 4000 MB default is a budget for the base
// image and checks nothing at all for a ~60 MB one, so a build that shipped
// the module cache, a second toolchain or the repository itself would pass it.
// The workflow's credentials job passes these two numbers and the docker test
// runs the same check locally, so the number CI enforces and the number a
// developer's `-tags docker` run enforces are one number.
const (
	credentialsSizeBudgetMB = 100
	credentialsSizeWarnMB   = 85
)

// TestCredentialsImageSizeBudgetIsReal: the size budget the workflow's
// credentials job enforces is the one above, and it is a budget that could
// actually fail -- a step that fell back to size-budget.sh's 4000 MB default
// would look like a check in the run's log while checking nothing, on the one
// image that holds every model credential the installation has.
func TestCredentialsImageSizeBudgetIsReal(t *testing.T) {
	wf := parseImagesWorkflow(t, readImagesWorkflow(t))
	job, ok := wf.Jobs["credentials"]
	if !ok {
		t.Fatal("no credentials job")
	}
	var step *workflowStep
	for i := range job.Steps {
		if strings.HasPrefix(strings.TrimSpace(job.Steps[i].Run), "images/size-budget.sh") {
			step = &job.Steps[i]
		}
	}
	if step == nil {
		t.Fatal("the credentials job runs no images/size-budget.sh step")
	}
	want := fmt.Sprintf("images/size-budget.sh fugaro-credentials:ci %d %d", credentialsSizeBudgetMB, credentialsSizeWarnMB)
	if got := strings.TrimSpace(step.Run); got != want {
		t.Errorf("the credentials job's size budget is %q, want %q: without the two numbers size-budget.sh uses its 4000 MB default, which no build of this image could fail", got, want)
	}
	if step.If == "" || !strings.Contains(step.If, ",credentials,") {
		t.Errorf("the size budget step's if is %q, want it gated on the credentials kind like its neighbours", step.If)
	}
	// And the budget itself is a real one: over the binary's measured size
	// (55.4 MB with -s -w, CGO off, on the amd64 build this release ships) and
	// well under the default it replaces.
	if credentialsSizeBudgetMB <= 60 || credentialsSizeBudgetMB >= 4000 {
		t.Errorf("credentialsSizeBudgetMB = %d, want a budget above the ~60 MB image and far below size-budget.sh's 4000 MB default", credentialsSizeBudgetMB)
	}
	if credentialsSizeWarnMB >= credentialsSizeBudgetMB {
		t.Errorf("credentialsSizeWarnMB = %d is not below credentialsSizeBudgetMB = %d: the warning line has to come first", credentialsSizeWarnMB, credentialsSizeBudgetMB)
	}
}
