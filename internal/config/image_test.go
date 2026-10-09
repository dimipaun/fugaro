package config

import (
	"os"
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

func TestImageProblems(t *testing.T) {
	jvm := strings.Replace(webYAML, "web-node", "java-services", 1)
	cases := []struct{ name, yaml, path, msg string }{
		{"image and dockerfile", webYAML + "    image: { node: \"24\" }\n    dockerfile: .fugaro/web.Dockerfile\n", "workflows.web.dockerfile", "mutually exclusive"},
		{"jdk on web-node", webYAML + "    image: { jdk: \"21\" }\n", "workflows.web.image.jdk", "is not supported"},
		{"node on java-services", jvm + "    image: { node: \"24\" }\n", "workflows.web.image.node", "only applies to base web-node"},
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
// checkout: baked. image.tools is not in this table: it does not exist yet
// in config.Image (it ships with the base-image plan's own Task 9), so it
// cannot be refused here; add it to this table once that field lands.
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
	cfg, problems := Parse([]byte(webYAML))
	if len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if cfg.Workflows["web"].Checkout != CheckoutBaked {
		t.Fatalf("default checkout = %q, want %q", cfg.Workflows["web"].Checkout, CheckoutBaked)
	}
}

// TestCheckoutCloneAlone is the companion to TestCloneRefusesImageBuildKeys:
// checkout: clone with no build keys set is valid.
func TestCheckoutCloneAlone(t *testing.T) {
	cfg, problems := Parse([]byte(webYAML + "    checkout: clone\n"))
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
