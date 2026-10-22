package images_test

import (
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
	if !regexp.MustCompile(`^FROM gcr\.io/distroless/static-debian12:nonroot$`).MatchString(stage[0]) {
		t.Errorf("final stage is %q, want distroless static nonroot", stage[0])
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
// environment. It must not assert the build-state refusal (that a configured
// `credentials serve` declines to start because C1.11 is absent): that is true
// of this build alone and would have to be rewritten the day the service
// lands, which is not what a smoke test is for.
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
		t.Error("smoke-credentials.sh pins the build-state refusal (C1.11 is absent), which the service's own merge invalidates")
	}
}
