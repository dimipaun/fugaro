package config

import (
	"os"
	"reflect"
	"slices"
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
    base: web-node
    commands:
      build: npm run build
      test: npm test
`

func TestParseImage(t *testing.T) {
	cfg, problems := Parse([]byte(webYAML + `    image:
      node: 24
      apt: [libvips-dev, fonts-liberation=1:2.1.5-3]
      setup: ["npx playwright install --with-deps chromium"]
`))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	img := cfg.Workflows["web"].Image
	if img.Node != "24" || len(img.Apt) != 2 || img.Setup[0] != "npx playwright install --with-deps chromium" || img.IsZero() {
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
	jvm := strings.Replace(webYAML, "web-node", "java-services", 1)
	if _, problems := Parse([]byte(jvm + "    image: { skip_build_scripts: false }\n")); len(problems) > 0 {
		t.Fatalf("an explicit false on java-services: %v", problems)
	}
}

// imageFieldReasons is config.Image's fields that never run as shell and so
// stay off ExecutableKeys, with why: Node is a version string checked
// against nodeVersionRE, never interpolated into a command, and JDK is
// refused on every base by validateImage, so it never reaches a Dockerfile.
var imageFieldReasons = map[string]string{
	"Node": "a version string, matched against nodeVersionRE, never run as shell",
	"JDK":  "refused on every base by validateImage; it never reaches a Dockerfile",
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
	jvm := strings.Replace(webYAML, "web-node", "java-services", 1)
	cases := []struct{ name, yaml, path, msg string }{
		{"image and dockerfile", webYAML + "    image: { node: \"24\" }\n    dockerfile: .fugaro/web.Dockerfile\n", "workflows.web.dockerfile", "mutually exclusive"},
		// Image.IsZero must count Tools: before it did, a workflow with
		// only image.tools set and a dockerfile: slipped past this check.
		{"tools and dockerfile", webYAML + "    image: { tools: { node: \"24.19.0\" } }\n    dockerfile: .fugaro/web.Dockerfile\n", "workflows.web.dockerfile", "mutually exclusive"},
		{"jdk on web-node", webYAML + "    image: { jdk: \"21\" }\n", "workflows.web.image.jdk", "is not supported"},
		{"node on java-services", jvm + "    image: { node: \"24\" }\n", "workflows.web.image.node", "only applies to base web-node"},
		// code review finding: the new default base (base: omitted) was the
		// one base kind the JSON schema's allOf block never covered, so an
		// editor using it showed no error for a document this refuses.
		{"node with no base at all (the default base)", "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web:\n    commands: { build: go build ./..., test: go test ./... }\n    image: { node: \"24\" }\n", "workflows.web.image.node", "only applies to base web-node"},
		{"node minor only", webYAML + "    image: { node: \"24.19\" }\n", "workflows.web.image.node", "major version such as 24"},
		{"node alias", webYAML + "    image: { node: lts }\n", "workflows.web.image.node", "major version such as 24"},
		{"jdk on java-services", jvm + "    image: { jdk: \"21\" }\n", "workflows.web.image.jdk", "is not supported"},
		{"apt injection", webYAML + "    image: { apt: [\"curl; rm -rf /\"] }\n", "workflows.web.image.apt[0]", "package name"},
		{"apt upper case", webYAML + "    image: { apt: [LibVips] }\n", "workflows.web.image.apt[0]", "package name"},
		{"setup empty", webYAML + "    image: { setup: [\"  \"] }\n", "workflows.web.image.setup[0]", "must not be empty"},
		{"setup multi-line", webYAML + "    image: { setup: [\"echo a\\necho b\"] }\n", "workflows.web.image.setup[0]", "single line"},
		{"setup heredoc", webYAML + "    image: { setup: [\"cat <<EOF > notes.txt\"] }\n", "workflows.web.image.setup[0]", "must not contain <<"},
		{"setup trailing backslash", webYAML + "    image: { setup: [\"echo a \\\\\"] }\n", "workflows.web.image.setup[0]", "must not end with a backslash"},
		{"setup flag-like (RUN mount)", webYAML + "    image: { setup: [\"--mount=type=secret,id=git-credentials,target=/tmp/c cp /tmp/c /work/repo/.leak\"] }\n", "workflows.web.image.setup[0]", "must not start with - or ["},
		{"setup exec form", webYAML + "    image: { setup: [\"[\\\"sh\\\", \\\"-c\\\", \\\"echo hi\\\"]\"] }\n", "workflows.web.image.setup[0]", "must not start with - or ["},
		{"setup leading whitespace then flag", webYAML + "    image: { setup: [\"  --mount=type=bind,target=/x\"] }\n", "workflows.web.image.setup[0]", "must not start with - or ["},
		{"skip_build_scripts on java-services", jvm + "    image: { skip_build_scripts: true }\n", "workflows.web.image.skip_build_scripts", "only applies to base web-node"},
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
		{"image.skip_build_scripts", "    image: { skip_build_scripts: true }\n", "workflows.web.image.skip_build_scripts"},
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
	// image.tools needs the Fugaro base (BaseKind), not web-node, so it is
	// its own case rather than a loop entry over webYAML (base: web-node):
	// combining tools with a legacy base would also trip the unrelated
	// "applies to the Fugaro base only" rule, muddying what this pins.
	t.Run("image.tools", func(t *testing.T) {
		yaml := "\nversion: 1\nproject: aurora\ngit:\n  provider: github\nworkflows:\n  web:\n    checkout: clone\n    image: { tools: { node: \"24\" } }\n    commands:\n      build: npm run build\n      test: npm test\n"
		_, problems := Parse([]byte(yaml))
		if !hasProblem(problems, "workflows.web.image.tools", msg, 0) {
			t.Errorf("problems = %v, want one at workflows.web.image.tools: %s", problems, msg)
		}
	})
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

// TestCheckoutInvalidValue pins that checkout: is an enum of baked and clone.
func TestCheckoutInvalidValue(t *testing.T) {
	_, problems := Parse([]byte(webYAML + "    checkout: sometimes\n"))
	if !hasProblem(problems, "workflows.web.checkout", "must be baked or clone", 0) {
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
// clone always runs the Fugaro base image, so a legacy base: (go,
// java-services, web-node) alongside it is refused rather than silently
// ignored. base: base (the explicit modern kind, equivalent to leaving
// base: out) is still allowed, since it names the same kind clone uses
// anyway.
//
// Mutation (run, restore): remove the `if w.Base != "" && w.Base != BaseKind`
// block from validateCheckout — this test's first case then wrongly accepts
// base: go with checkout: clone.
func TestCheckoutCloneRefusesLegacyBase(t *testing.T) {
	const msg = "checkout: clone always runs the Fugaro base image"
	for _, base := range []string{"go", "java-services", "web-node"} {
		yaml := "\nversion: 1\nproject: aurora\ngit:\n  provider: github\nworkflows:\n  web:\n    base: " + base +
			"\n    checkout: clone\n    commands:\n      build: x\n      test: x\n"
		_, problems := Parse([]byte(yaml))
		if !hasProblem(problems, "workflows.web.base", msg, 0) {
			t.Errorf("base %s: problems = %v, want one at workflows.web.base: %s", base, problems, msg)
		}
	}
	// base: base (explicit, equivalent to no base:) is allowed with clone.
	cfg, problems := Parse([]byte(cloneYAML + "    base: base\n    checkout: clone\n"))
	if len(problems) > 0 {
		t.Fatalf("base: base with checkout: clone: unexpected problems: %v", problems)
	}
	if cfg.Workflows["web"].Checkout != CheckoutClone {
		t.Fatalf("checkout = %q, want %q", cfg.Workflows["web"].Checkout, CheckoutClone)
	}
}

// TestCheckoutCloneLegacyBaseCorpus is
// testdata/config/invalid/checkout-clone-legacy-base.yaml, read from disk
// like TestCheckoutCloneSkipBuildCorpus.
func TestCheckoutCloneLegacyBaseCorpus(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/invalid/checkout-clone-legacy-base.yaml")
	if err != nil {
		t.Fatal(err)
	}
	const msg = "checkout: clone always runs the Fugaro base image"
	if _, ps := Parse(data); len(ps) != 1 || ps[0].Path != "workflows.web.base" || !strings.Contains(ps[0].Message, msg) {
		t.Fatalf("problems = %v, want exactly one at workflows.web.base: %s", ps, msg)
	}
}

func TestGoBase(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/valid/go.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, problems := Parse(data)
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	w := cfg.Workflows["go"]
	if w.Resources.CPU != 4 || w.Resources.Memory != "8Gi" || len(w.Commands.Reports) == 0 {
		t.Errorf("defaults = %+v, reports %v", w.Resources, w.Commands.Reports)
	}
	for file, path := range map[string]string{"image-node-on-go": "workflows.go.image.node"} {
		data, err := os.ReadFile("../../testdata/config/invalid/" + file + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		if _, ps := Parse(data); len(ps) != 1 || ps[0].Path != path {
			t.Errorf("%s: problems = %v, want one at %s", file, ps, path)
		}
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

func TestJavaServicesBase(t *testing.T) {
	data, err := os.ReadFile("../../testdata/config/valid/java-services.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, problems := Parse(data)
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	w := cfg.Workflows["server"]
	if w.Resources.CPU != 4 || w.Resources.Memory != "16Gi" ||
		len(w.Commands.Reports) != 1 || w.Commands.Reports[0] != "**/build/test-results/**/*.xml" {
		t.Errorf("defaults = %+v, reports %v", w.Resources, w.Commands.Reports)
	}
	for file, path := range map[string]string{
		"image-node-on-java-services": "workflows.server.image.node",
		"image-jdk-on-java-services":  "workflows.server.image.jdk",
	} {
		data, err := os.ReadFile("../../testdata/config/invalid/" + file + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		if _, ps := Parse(data); len(ps) != 1 || ps[0].Path != path {
			t.Errorf("%s: problems = %v, want one at %s", file, ps, path)
		}
	}
}
