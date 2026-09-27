package config

import (
	"strings"
	"testing"
)

const webYAML = `
version: 1
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

func TestImageProblems(t *testing.T) {
	jvm := strings.Replace(webYAML, "web-node", "server-jvm", 1)
	cases := []struct{ name, yaml, path, msg string }{
		{"image and dockerfile", webYAML + "    image: { node: \"24\" }\n    dockerfile: .fugaro/web.Dockerfile\n", "workflows.web.dockerfile", "mutually exclusive"},
		{"jdk on web-node", webYAML + "    image: { jdk: \"21\" }\n", "workflows.web.image.jdk", "only applies to base server-jvm"},
		{"node on server-jvm", jvm + "    image: { node: \"24\" }\n", "workflows.web.image.node", "only applies to base web-node"},
		{"node minor only", webYAML + "    image: { node: \"24.19\" }\n", "workflows.web.image.node", "major version such as 24"},
		{"node alias", webYAML + "    image: { node: lts }\n", "workflows.web.image.node", "major version such as 24"},
		{"jdk version", jvm + "    image: { jdk: \"21.0.4\" }\n", "workflows.web.image.jdk", "major version such as 21"},
		{"apt injection", webYAML + "    image: { apt: [\"curl; rm -rf /\"] }\n", "workflows.web.image.apt[0]", "package name"},
		{"apt upper case", webYAML + "    image: { apt: [LibVips] }\n", "workflows.web.image.apt[0]", "package name"},
		{"setup empty", webYAML + "    image: { setup: [\"  \"] }\n", "workflows.web.image.setup[0]", "must not be empty"},
		{"setup multi-line", webYAML + "    image: { setup: [\"echo a\\necho b\"] }\n", "workflows.web.image.setup[0]", "single line"},
		{"setup heredoc", webYAML + "    image: { setup: [\"cat <<EOF > notes.txt\"] }\n", "workflows.web.image.setup[0]", "must not contain <<"},
		{"setup trailing backslash", webYAML + "    image: { setup: [\"echo a \\\\\"] }\n", "workflows.web.image.setup[0]", "must not end with a backslash"},
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
