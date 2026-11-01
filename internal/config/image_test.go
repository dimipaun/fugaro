package config

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const webYAML = `
version: 1
project: aurora
git:
  provider: github
workflows:
  web:
    commands:
      build: npm run build
      test: npm test
`

func TestParseImage(t *testing.T) {
	cfg, problems := Parse([]byte(webYAML + `    image:
      apt: [libvips-dev, fonts-liberation=1:2.1.5-3]
      setup: ["npx playwright install --with-deps chromium"]
`))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	img := cfg.Workflows["web"].Image
	if len(img.Apt) != 2 || img.Setup[0] != "npx playwright install --with-deps chromium" || img.IsZero() {
		t.Fatalf("image = %+v", img)
	}
	if !(Image{}).IsZero() {
		t.Fatal("the zero Image is not IsZero")
	}
}

func TestParseImageSkipBuildScripts(t *testing.T) {
	cfg, problems := Parse([]byte(webYAML + "    image: { skip_build_scripts: true }\n"))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	img := cfg.Workflows["web"].Image
	if !img.SkipBuildScripts || img.IsZero() {
		t.Fatalf("image = %+v, IsZero %v", img, img.IsZero())
	}
	if cfg, _ := Parse([]byte(webYAML)); cfg.Workflows["web"].Image.SkipBuildScripts {
		t.Fatal("skip_build_scripts defaults to true")
	}
}

// imageFieldReasons is config.Image's fields that never run as shell and so
// stay off ExecutableKeys, with why: Node and JDK were removed in 0.7.0 and
// are refused unconditionally by validateImage, so neither ever reaches a
// Dockerfile.
var imageFieldReasons = map[string]string{
	"Node": "removed in 0.7.0; refused unconditionally by validateImage, never run as shell",
	"JDK":  "removed in 0.7.0; refused unconditionally by validateImage, never reaches a Dockerfile",
	"KeyFiles": "repository paths the image checks out before the source layer, each written as one " +
		"single-quoted word into the checkout loop; validateImage refuses anything but a clean relative path",
}

// TestImageFieldsAreClassified is decision L7's reflection-style guard: a
// field added to config.Image later must either run as shell (and so be
// gated, an entry of ExecutableKeys, named "workflows.*.image.<yaml tag>")
// or have a reason in imageFieldReasons that it is not. A field with
// neither is caught here, in CI, instead of landing ungated on a live
// publish.
func TestImageFieldsAreClassified(t *testing.T) {
	typ := reflect.TypeOf(Image{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		key := "workflows.*.image." + tag
		executable := slices.Contains(ExecutableKeys, key)
		_, reasoned := imageFieldReasons[f.Name]
		switch {
		case executable && reasoned:
			t.Errorf("Image.%s (%s): is both in ExecutableKeys and in imageFieldReasons; it cannot be both gated and exempt", f.Name, key)
		case !executable && !reasoned:
			t.Errorf("Image.%s (%s): is in neither ExecutableKeys nor imageFieldReasons; classify it as executable (gated) or add a reason it is not", f.Name, key)
		}
	}
}

func TestImageProblems(t *testing.T) {
	cases := []struct{ name, yaml, path, msg string }{
		{"image and dockerfile", webYAML + "    image: { apt: [libvips-dev] }\n    dockerfile: .fugaro/web.Dockerfile\n", "workflows.web.dockerfile", "mutually exclusive"},
		// Image.IsZero must count Tools: before it did, a workflow with
		// only image.tools set and a dockerfile: slipped past this check.
		{"tools and dockerfile", webYAML + "    image: { tools: { node: \"24.19.0\" } }\n    dockerfile: .fugaro/web.Dockerfile\n", "workflows.web.dockerfile", "mutually exclusive"},
		{"apt injection", webYAML + "    image: { apt: [\"curl; rm -rf /\"] }\n", "workflows.web.image.apt[0]", "package name"},
		{"apt upper case", webYAML + "    image: { apt: [LibVips] }\n", "workflows.web.image.apt[0]", "package name"},
		{"setup empty", webYAML + "    image: { setup: [\"  \"] }\n", "workflows.web.image.setup[0]", "must not be empty"},
		{"setup multi-line", webYAML + "    image: { setup: [\"echo a\\necho b\"] }\n", "workflows.web.image.setup[0]", "single line"},
		{"setup heredoc", webYAML + "    image: { setup: [\"cat <<EOF > notes.txt\"] }\n", "workflows.web.image.setup[0]", "must not contain <<"},
		{"setup trailing backslash", webYAML + "    image: { setup: [\"echo a \\\\\"] }\n", "workflows.web.image.setup[0]", "must not end with a backslash"},
		{"setup flag-like (RUN mount)", webYAML + "    image: { setup: [\"--mount=type=secret,id=git-credentials,target=/tmp/c cp /tmp/c /work/repo/.leak\"] }\n", "workflows.web.image.setup[0]", "must not start with - or ["},
		{"setup exec form", webYAML + "    image: { setup: [\"[\\\"sh\\\", \\\"-c\\\", \\\"echo hi\\\"]\"] }\n", "workflows.web.image.setup[0]", "must not start with - or ["},
		{"setup leading whitespace then flag", webYAML + "    image: { setup: [\"  --mount=type=bind,target=/x\"] }\n", "workflows.web.image.setup[0]", "must not start with - or ["},
		{"skip_build_scripts and dockerfile", webYAML + "    image: { skip_build_scripts: true }\n    dockerfile: .fugaro/web.Dockerfile\n", "workflows.web.dockerfile", "mutually exclusive"},
		{"dockerfile absolute", webYAML + "    dockerfile: /etc/Dockerfile\n", "workflows.web.dockerfile", "relative path inside the repository"},
		{"dockerfile parent", webYAML + "    dockerfile: .fugaro/../../x.Dockerfile\n", "workflows.web.dockerfile", "relative path inside the repository"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, problems := Parse([]byte(tc.yaml))
			if cfg != nil {
				t.Fatal("expected no config when there are problems")
			}
			if !hasProblem(problems, tc.path, tc.msg, 0) {
				t.Fatalf("want problem path=%q msg~%q, got %v", tc.path, tc.msg, problems)
			}
		})
	}
}

// TestCloneRefusesImageBuildKeys pins design generic-tool.md §2 (G3): a
// checkout: clone workflow runs the installation's base image with no
// build, so a key that only a build can honor is refused, naming
// checkout: baked.
func TestCloneRefusesImageBuildKeys(t *testing.T) {
	const msg = "checkout: clone runs the base image with no build; use checkout: baked to build an image"
	cases := []struct{ name, extra, path string }{
		{"image.apt", "    image: { apt: [jq] }\n", "workflows.web.image.apt"},
		{"image.setup", "    image: { setup: [\"make deps\"] }\n", "workflows.web.image.setup"},
		{"image.tools", "    image: { tools: { node: \"24\" } }\n", "workflows.web.image.tools"},
		{"image.skip_build_scripts", "    image: { skip_build_scripts: true }\n", "workflows.web.image.skip_build_scripts"},
		{"image.key_files", "    image: { key_files: [patches] }\n", "workflows.web.image.key_files"},
		{"dockerfile", "    dockerfile: .fugaro/web.Dockerfile\n", "workflows.web.dockerfile"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Parse([]byte(webYAML + "    checkout: clone\n" + tc.extra))
			if !hasProblem(problems, tc.path, msg, 0) {
				t.Errorf("problems = %v, want one at %s: %s", problems, tc.path, msg)
			}
		})
	}
	cfg, problems := Parse([]byte(webYAML))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if cfg.Workflows["web"].Checkout != CheckoutBaked {
		t.Fatalf("default checkout = %q, want %q", cfg.Workflows["web"].Checkout, CheckoutBaked)
	}
}

// cloneYAML is webYAML without base: web-node: checkout: clone always runs
// the Fugaro base image, so combining it with a legacy base is refused
// (TestCheckoutCloneRefusesLegacyBase), and these two tests must not
// trip that rule while pinning something else.
const cloneYAML = `
version: 1
project: aurora
git:
  provider: github
workflows:
  web:
    commands:
      build: npm run build
      test: npm test
`

// TestCheckoutCloneAlone is the companion to TestCloneRefusesImageBuildKeys:
// checkout: clone with no build keys set is valid.
func TestCheckoutCloneAlone(t *testing.T) {
	cfg, problems := Parse([]byte(cloneYAML + "    checkout: clone\n"))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if cfg.Workflows["web"].Checkout != CheckoutClone {
		t.Fatalf("checkout = %q, want %q", cfg.Workflows["web"].Checkout, CheckoutClone)
	}
}

// TestCheckoutCloneAllowsEmptyBuildKeys pins that checkout: clone checks a
// build key's value, not its mere presence: an empty image.apt, image.setup
// or image.tools, or an empty dockerfile:, means nothing to refuse (schema
// corpus: testdata/config/valid/checkout-clone-empty-apt.yaml exercises the
// same boundary against fugaro.schema.json).
func TestCheckoutCloneAllowsEmptyBuildKeys(t *testing.T) {
	cfg, problems := Parse([]byte(cloneYAML + "    checkout: clone\n    image: { apt: [], setup: [], tools: {}, skip_build_scripts: false }\n"))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if cfg.Workflows["web"].Checkout != CheckoutClone {
		t.Fatalf("checkout = %q, want %q", cfg.Workflows["web"].Checkout, CheckoutClone)
	}
}

// sharedYAML is webYAML with the dependency-layer keys a checkout: shared
// workflow declares: the layer the shared image holds is the tools, the
// packages, the key files and the warm-up (design
// forge-authorship-images/shared-images.md §1, §12), and every one of them
// is refused under checkout: clone, so this is the shape only the new kind
// makes expressible.
const sharedYAML = webYAML + `    checkout: shared
    image:
      apt: [libvips-dev]
      tools: { node: "24.19.0" }
      key_files: [patches]
      skip_build_scripts: true
`

// sharedShipped turns the 0.15.0 launch gate on for the duration of one test
// and restores it: validation refuses every checkout: shared today
// (TestValidateRefusesCheckoutSharedUntil0150), so the rules the release that
// ships shared images will apply are reachable only behind the flag. The
// release that flips sharedCheckoutShipped makes these tests run it open.
func sharedShipped(t *testing.T) {
	t.Helper()
	was := sharedCheckoutShipped
	sharedCheckoutShipped = true
	t.Cleanup(func() { sharedCheckoutShipped = was })
}

// TestSharedCheckoutShippedIsTheGateValidationRefusesBy: internal/infra reads
// the exported accessor to decide whether a job whose environment carries
// FUGARO_CHECKOUT=shared may have its image moved, so it has to be the same
// flag validateCheckout refuses a config by. An accessor reading anything else
// would let the cloud side accept the mark while the release that builds the
// image has not run, or refuse it after the flip.
func TestSharedCheckoutShippedIsTheGateValidationRefusesBy(t *testing.T) {
	if SharedCheckoutShipped() {
		t.Fatal("this build ships shared images; this test pins the gate while it is off and moves with the flip")
	}
	if _, problems := Parse([]byte(sharedYAML)); !hasProblem(problems, "workflows.web.checkout", SharedCheckoutRefusal, 0) {
		t.Fatalf("problems = %v, want the refusal the gate names", problems)
	}
	sharedShipped(t)
	if !SharedCheckoutShipped() {
		t.Error("the accessor did not follow the flag")
	}
	if _, problems := Parse([]byte(sharedYAML)); len(problems) > 0 {
		t.Fatalf("problems = %v, want the gated rules to accept it", problems)
	}
}

// TestValidateCheckoutSharedAllowsDependencyKeys (design
// forge-authorship-images/shared-images.md §6, route A): checkout: shared
// runs a shared dependency-layer image — one image per resolved dependency
// set, holding no repository's source — and clones the repository's own
// source at launch, so the keys that describe that layer are exactly the
// ones it accepts. Behind sharedShipped, because the kind is refused until
// 0.15.0 ships the images it would run.
func TestValidateCheckoutSharedAllowsDependencyKeys(t *testing.T) {
	sharedShipped(t)
	cfg, problems := Parse([]byte(sharedYAML))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if cfg.Workflows["web"].Checkout != CheckoutShared {
		t.Fatalf("checkout = %q, want %q", cfg.Workflows["web"].Checkout, CheckoutShared)
	}
}

// TestValidateRefusesCheckoutSharedUntil0150: nothing in 0.14.0 builds or
// refreshes a shared dependency-layer image, so a repository that declares
// checkout: shared would get a job that cannot run. Validation refuses the
// key where the repository typed it, names the release that lifts the
// refusal, and names the two kinds that work today — including for a
// workflow that also declares the dependency-layer keys, which are the ones
// only this kind accepts and so the ones a reader reaches for first.
func TestValidateRefusesCheckoutSharedUntil0150(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"bare", webYAML + "    checkout: shared\n"},
		{"with the dependency-layer keys", sharedYAML},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Parse([]byte(tc.yaml))
			if !hasProblem(problems, "workflows.web.checkout", "shared images arrive in 0.15.0", 0) {
				t.Fatalf("problems = %v, want a refusal at workflows.web.checkout naming 0.15.0", problems)
			}
			for _, want := range []string{"checkout: baked", "checkout: clone"} {
				if p := problems[0]; !strings.Contains(p.Message, want) {
					t.Errorf("the refusal %q does not name %q", p.Message, want)
				}
			}
		})
	}
}

// TestValidateCheckoutSharedRefusesSetup (design
// forge-authorship-images/shared-images.md §1, §2.2): a shared image is run
// by every repository on the same dependency set, so a key that would put
// this repository's own content into it is refused — image.setup, which
// runs in the repository's checkout, and a workflow's own dockerfile, which
// builds a per-repository image at all. Each refusal names its key (the
// problem's path) and the reason, in one sentence, and names checkout:
// baked as the way out — which is how a workflow gets a per-repository
// step at all. Behind sharedShipped for the same reason as the test above.
func TestValidateCheckoutSharedRefusesSetup(t *testing.T) {
	sharedShipped(t)
	const msg = "checkout: shared runs an image shared by every repository on the same dependency set, which holds no repository's own content; use checkout: baked to build a per-repository image"
	for _, tc := range []struct{ name, extra, path string }{
		{"image.setup", "    image: { setup: [\"npx playwright install --with-deps chromium\"] }\n", "workflows.web.image.setup"},
		{"dockerfile", "    dockerfile: .fugaro/web.Dockerfile\n", "workflows.web.dockerfile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Parse([]byte(webYAML + "    checkout: shared\n" + tc.extra))
			if !hasProblem(problems, tc.path, msg, 0) {
				t.Errorf("problems = %v, want one at %s: %s", problems, tc.path, msg)
			}
		})
	}
}

// TestCheckoutInvalidValue pins that checkout: is an enum of baked, clone
// and shared.
func TestCheckoutInvalidValue(t *testing.T) {
	_, problems := Parse([]byte(webYAML + "    checkout: sometimes\n"))
	if !hasProblem(problems, "workflows.web.checkout", "must be baked or clone or shared; shared runs a shared dependency-layer image", 0) {
		t.Fatalf("problems = %v", problems)
	}
}

// TestCheckoutBadValueCorpus is testdata/config/invalid/checkout-bad-value.yaml
// (the schema corpus: fugaro.schema.json's checkout enum had no corpus case),
// read from disk like TestGoBase's and TestJavaServicesBase's corpus cases:
// it must fail for exactly the one reason the file exists to pin.
func TestCheckoutBadValueCorpus(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/invalid/checkout-bad-value.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, ps := Parse(data); len(ps) != 1 || ps[0].Path != "workflows.web.checkout" || !strings.Contains(ps[0].Message, "must be baked or clone") {
		t.Fatalf("problems = %v, want exactly one at workflows.web.checkout: must be baked or clone", ps)
	}
}

// TestCheckoutCloneToolsCorpus is testdata/config/invalid/checkout-clone-tools.yaml:
// checkout: clone refuses image.tools exactly like image.apt and
// image.setup, read from disk like TestCheckoutBadValueCorpus.
func TestCheckoutCloneToolsCorpus(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/invalid/checkout-clone-tools.yaml")
	if err != nil {
		t.Fatal(err)
	}
	const msg = "checkout: clone runs the base image with no build; use checkout: baked to build an image"
	if _, ps := Parse(data); len(ps) != 1 || ps[0].Path != "workflows.web.image.tools" || !strings.Contains(ps[0].Message, msg) {
		t.Fatalf("problems = %v, want exactly one at workflows.web.image.tools: %s", ps, msg)
	}
}

// TestCheckoutCloneSkipBuildCorpus is
// testdata/config/invalid/checkout-clone-skip-build.yaml: checkout: clone
// refuses image.skip_build_scripts, read from disk like
// TestCheckoutCloneToolsCorpus. It closes a schema corpus gap: fugaro.schema.json's
// checkout: clone branch has a skip_build_scripts: {const: false} clause
// with no corpus file exercising it (every other clause in that branch had
// one).
func TestCheckoutCloneSkipBuildCorpus(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/invalid/checkout-clone-skip-build.yaml")
	if err != nil {
		t.Fatal(err)
	}
	const msg = "checkout: clone runs the base image with no build; use checkout: baked to build an image"
	if _, ps := Parse(data); len(ps) != 1 || ps[0].Path != "workflows.web.image.skip_build_scripts" || !strings.Contains(ps[0].Message, msg) {
		t.Fatalf("problems = %v, want exactly one at workflows.web.image.skip_build_scripts: %s", ps, msg)
	}
}

// TestCheckoutCloneRefusesLegacyBase (review finding 4, LOW): checkout:
// clone always runs the Fugaro base image, so a base: alongside it is
// refused rather than silently ignored. Since the 0.7.0 cut every base:
// (the legacy go, java-services, web-node, and base itself) is refused by
// the shared legacyBaseMessage; this pins that the refusal still holds with
// checkout: clone, and that it is exactly one problem (not doubled by a
// clone-specific copy).
//
// Mutation (run, restore): remove the `if w.Base != ""` refusal from
// validate.go's workflow loop — this test then wrongly accepts every base:
// with checkout: clone.
func TestCheckoutCloneRefusesLegacyBase(t *testing.T) {
	for _, base := range []string{"go", "java-services", "web-node", "base"} {
		yaml := "\nversion: 1\nproject: aurora\ngit:\n  provider: github\nworkflows:\n  web:\n    base: " + base +
			"\n    checkout: clone\n    commands:\n      build: x\n      test: x\n"
		_, ps := Parse([]byte(yaml))
		if len(ps) != 1 || ps[0].Path != "workflows.web.base" || !strings.Contains(ps[0].Message, "was removed in 0.7.0") {
			t.Errorf("base %s: problems = %v, want exactly one at workflows.web.base: was removed in 0.7.0", base, ps)
		}
	}
	// No base: at all is the clone shape.
	cfg, problems := Parse([]byte(cloneYAML + "    checkout: clone\n"))
	if len(problems) > 0 {
		t.Fatalf("checkout: clone with no base: unexpected problems: %v", problems)
	}
	if cfg.Workflows["web"].Checkout != CheckoutClone {
		t.Fatalf("checkout = %q, want %q", cfg.Workflows["web"].Checkout, CheckoutClone)
	}
}

// TestCheckoutCloneLegacyBaseCorpus is
// testdata/config/invalid/checkout-clone-legacy-base.yaml, read from disk
// like TestCheckoutCloneSkipBuildCorpus: exactly one problem, the cut's
// base: refusal.
func TestCheckoutCloneLegacyBaseCorpus(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/invalid/checkout-clone-legacy-base.yaml")
	if err != nil {
		t.Fatal(err)
	}
	const msg = "was removed in 0.7.0"
	if _, ps := Parse(data); len(ps) != 1 || ps[0].Path != "workflows.web.base" || !strings.Contains(ps[0].Message, msg) {
		t.Fatalf("problems = %v, want exactly one at workflows.web.base: %s", ps, msg)
	}
}

// TestValidMiseTool is a table test of the single gate validateImage uses
// for every image.tools entry. Every image.tools key or version reaches a
// Dockerfile or a generated mise config verbatim (design base-image.md
// section 4), so this charset is a strict allowlist, not a denylist: each
// bad case below is a valid prefix plus exactly one character (or, for the
// length caps, one byte) that must not be let through. A regex or cap
// change that lets any of these pass is a real security regression
// (arbitrary code execution through a mise backend such as cargo:, go:,
// npm:, asdf:/vfox: or ubi:/github:/aqua:, per the ExecutableKeys ruling
// above).
func TestValidMiseTool(t *testing.T) {
	for _, name := range []string{"npm:@scope/pkg", "aqua:owner/repo", "node"} {
		if !ValidMiseTool(name, "1.0.0") {
			t.Errorf("ValidMiseTool(%q, \"1.0.0\") = false, want true", name)
		}
	}
	for _, version := range []string{"temurin-25", "24.19.0"} {
		if !ValidMiseTool("node", version) {
			t.Errorf("ValidMiseTool(\"node\", %q) = false, want true", version)
		}
	}
	badVersions := []string{
		"24\"", "24'", "24\\", "24\n", "24 ", "24;", "24$x", "24`x`",
		"24/x", "24@x", "24%", "24\x00", "２４", "-24",
	}
	for _, v := range badVersions {
		if ValidMiseTool("node", v) {
			t.Errorf("ValidMiseTool(\"node\", %q) = true, want false", v)
		}
	}
	badNames := []string{
		`node"`, "node\n", "node x", "node=", "node]", "!node", "Node", "-node", "ｎode",
	}
	for _, name := range badNames {
		if ValidMiseTool(name, "1.0.0") {
			t.Errorf("ValidMiseTool(%q, \"1.0.0\") = true, want false", name)
		}
	}
}

// TestValidMiseToolLengthCaps pins the exact boundary of each cap: the
// longest accepted key or version, and one byte past it.
func TestValidMiseToolLengthCaps(t *testing.T) {
	longName := strings.Repeat("a", maxMiseToolNameLen)
	if !ValidMiseTool(longName, "1.0.0") {
		t.Errorf("a %d-byte name must be valid", maxMiseToolNameLen)
	}
	if ValidMiseTool(longName+"a", "1.0.0") {
		t.Errorf("a %d-byte name must be refused", maxMiseToolNameLen+1)
	}
	longVersion := strings.Repeat("1", maxMiseToolVersionLen)
	if !ValidMiseTool("node", longVersion) {
		t.Errorf("a %d-byte version must be valid", maxMiseToolVersionLen)
	}
	if ValidMiseTool("node", longVersion+"1") {
		t.Errorf("a %d-byte version must be refused", maxMiseToolVersionLen+1)
	}
}

// TestImageToolsVersionMustBeAQuotedString pins the ruling that an
// image.tools version must be a YAML string scalar: "python: 3.10" is an
// unquoted YAML float (a human reading it expects the string "3.10"), and
// schemas/fugaro.schema.json already requires a JSON string there, so Go
// must refuse it rather than silently stringify it (looser than the
// schema). A plain scalar YAML does not resolve as a number, bool or null,
// such as temurin-25, stays accepted without quotes.
func TestImageToolsVersionMustBeAQuotedString(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"3.10", "must be a quoted string version"},
		{"24", "must be a quoted string version"},
		{"25.00", "must be a quoted string version"},
		{"true", "must be a quoted string version"},
		{`"24.19.0"`, ""},
		{"temurin-25", ""},
	} {
		y := strings.Replace(baseYAML, "    commands:", "    image: { tools: { node: "+tc.value+" } }\n    commands:", 1)
		_, problems := Parse([]byte(y))
		got := ""
		for _, p := range problems {
			got += p.String() + "\n"
		}
		if (tc.want == "") != (got == "") || (tc.want != "" && !strings.Contains(got, tc.want)) {
			t.Errorf("node: %s: problems %q, want %q", tc.value, got, tc.want)
		}
	}
}

// TestServicesCorpusGetsUniformDefaults: testdata/config/valid/services.yaml
// (the java-services replacement, design base-image.md §5) gets the uniform
// defaults (B10), not the old java-services base's 16Gi.
func TestServicesCorpusGetsUniformDefaults(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/valid/services.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, problems := Parse(data)
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	w := cfg.Workflows["server"]
	if w.Resources.CPU != 4 || w.Resources.Memory != "8Gi" ||
		!slices.Equal(w.Commands.Reports, []string{"**/junit*.xml", "**/build/test-results/**/*.xml"}) {
		t.Errorf("defaults = %+v, reports %v", w.Resources, w.Commands.Reports)
	}
}

// keyFilesYAML is webYAML with an image.key_files list, each entry written as
// the JSON string a YAML parser reads back as itself (so a control character
// or a backslash is the one under test, not a YAML escape).
func keyFilesYAML(entries ...string) string {
	quoted := make([]string, len(entries))
	for i, e := range entries {
		quoted[i] = strconv.Quote(e)
	}
	return webYAML + "    image: { key_files: [" + strings.Join(quoted, ", ") + "] }\n"
}

// TestValidateImageKeyFilesRefusesEscapes: an image.key_files entry is one
// path inside the repository, and validateImage refuses everything that could
// name something else: an absolute path, a .. segment, .git, the repository
// root, a path that is not clean, a backslash (git reads it as an escape in a
// pathspec) and a control character (the entry reaches a Dockerfile line).
// Each refusal names the entry by its index, and never echoes the value.
func TestValidateImageKeyFilesRefusesEscapes(t *testing.T) {
	for _, tc := range []struct{ name, entry, msg string }{
		{"absolute", "/etc/passwd", "not an absolute one"},
		{"a parent segment", "../outside", ".. segment"},
		{"a parent segment in the middle", "a/../../etc/passwd", ".. segment"},
		{"a bare parent", "..", ".. segment"},
		{".git/config", ".git/config", "inside .git"},
		{"the .git directory", ".git", "inside .git"},
		{"the repository root", ".", "repository root"},
		{"a trailing slash", "patches/", "clean path"},
		{"a doubled slash", "a//b", "clean path"},
		{"a dot segment", "./patches", "clean path"},
		{"a dot segment in the middle", "a/./b", "clean path"},
		{"empty", "", "must not be empty"},
		{"only spaces", "   ", "must not be empty"},
		{"a backslash", `a\b`, "no backslash"},
		{"an escape sequence", "a\x1bb", "control character"},
		{"a newline", "a\nb", "control character"},
		{"over the length cap", strings.Repeat("a", maxImageKeyFilePath+1), "at most 200 characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Parse([]byte(keyFilesYAML(tc.entry)))
			if !hasProblem(problems, "workflows.web.image.key_files[0]", tc.msg, 0) {
				t.Fatalf("problems = %v, want one at image.key_files[0] ~ %q", problems, tc.msg)
			}
			for _, p := range problems {
				if strings.ContainsAny(p.Message, "\x1b\n\r") {
					t.Errorf("the refusal echoes the value into the message: %q", p.Message)
				}
			}
		})
	}
}

// TestValidateImageKeyFilesRefusesGlobsAndDuplicates: an entry is the path it
// is, not a pattern (the rebuild key turns it into a doublestar glob, so a
// pattern would key the image on files the install never reads), and the list
// carries each path once, within its cap. A path that is only unusual is
// accepted: the point is a strict shape, not a short list of blessed names.
func TestValidateImageKeyFilesRefusesGlobsAndDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		msg     string
	}{
		{"a star", []string{"*.patch"}, "no glob"},
		{"a doublestar", []string{"**/*.patch"}, "no glob"},
		{"a character class", []string{"p[abc]"}, "no glob"},
		{"a brace group", []string{"p{a,b}"}, "no glob"},
		{"a question mark", []string{"patch?"}, "no glob"},
		{"a duplicate", []string{"patches", "vendor/x", "patches"}, "duplicates image.key_files[0]"},
		{"over the cap", caps(maxImageKeyFiles + 1), "at most 32 entries"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Parse([]byte(keyFilesYAML(tc.entries...)))
			if !hasProblem(problems, "workflows.web.image.key_files", tc.msg, 0) &&
				!hasProblem(problems, "workflows.web.image.key_files[2]", tc.msg, 0) &&
				!hasProblem(problems, "workflows.web.image.key_files[0]", tc.msg, 0) {
				t.Fatalf("problems = %v, want one ~ %q", problems, tc.msg)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		entries []string
	}{
		{"a directory", []string{"patches"}},
		{"a file in a directory", []string{".yarn/patches/left-pad.patch"}},
		{"several", []string{"patches", "vendor/toolchain.toml", ".pnpmfile.cjs"}},
		{"a space", []string{"vendor/my tool"}},
		{"a non-ASCII path", []string{"vendor/café/tool"}},
		{"at the cap", caps(maxImageKeyFiles)},
		{"at the length cap", []string{strings.Repeat("a", maxImageKeyFilePath)}},
	} {
		t.Run("valid "+tc.name, func(t *testing.T) {
			cfg, problems := Parse([]byte(keyFilesYAML(tc.entries...)))
			if len(problems) > 0 {
				t.Fatalf("unexpected problems: %v", problems)
			}
			if !slices.Equal(cfg.Workflows["web"].Image.KeyFiles, tc.entries) {
				t.Fatalf("image.key_files = %v, want %v", cfg.Workflows["web"].Image.KeyFiles, tc.entries)
			}
			if cfg.Workflows["web"].Image.IsZero() {
				t.Fatal("image.key_files alone leaves Image.IsZero")
			}
		})
	}
}

// caps is n distinct key-file entries, each valid.
func caps(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("patches/%02d.patch", i)
	}
	return out
}
