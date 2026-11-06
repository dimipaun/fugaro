package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestLegacyBaseRefusedWithTheMigrationDoc(t *testing.T) {
	for _, base := range []string{"go", "web-node", "java-services", "base"} {
		y := strings.Replace(baseYAML, "    commands:", "    base: "+base+"\n    commands:", 1)
		_, problems := Parse([]byte(y))
		if len(problems) != 1 || problems[0].Path != "workflows.app.base" ||
			!strings.Contains(problems[0].Message, "removed in 0.7.0") || !strings.Contains(problems[0].Message, MigrationDoc) {
			t.Errorf("base: %s: %v", base, problems)
		}
	}
}

// TestLegacyImageNodeAndJDKRefused pins both 0.7.0 sentences whole, token by
// token and in order, rather than by the value they echo: what one of them
// stops naming matters as much as what it names, and the 0.20.0 removal took
// the image.tools alternative out of it, because a decoder that sends a person
// to a key it refuses in the next breath is worse than one that names only the
// path left. That reader meets the removal's own sentence either way -- it
// names `fugaro image add` -- so what this pins is that mise.toml is the one
// alternative an image.node or image.jdk refusal still offers.
func TestLegacyImageNodeAndJDKRefused(t *testing.T) {
	for _, tc := range []struct{ image, path, want string }{
		{`image: { node: "24.19.0" }`, "workflows.app.image.node",
			fmt.Sprintf("was removed in 0.7.0: declare node in mise.toml ([tools] node = %q) (%s)", "24.19.0", MigrationDoc)},
		{`image: { jdk: "21" }`, "workflows.app.image.jdk",
			fmt.Sprintf("was removed in 0.7.0: declare Java in mise.toml ([tools] java = %q) (%s)", "temurin-21", MigrationDoc)},
	} {
		y := strings.Replace(baseYAML, "    commands:", "    "+tc.image+"\n    commands:", 1)
		_, problems := Parse([]byte(y))
		if len(problems) != 1 || problems[0].Path != tc.path || problems[0].Message != tc.want {
			t.Errorf("%s: problems = %v, want exactly one at %s: %s", tc.image, problems, tc.path, tc.want)
		}
	}
}

// TestLegacyImageNodeAndJDKEchoIsBounded pins that the legacy value these
// refusals echo is bounded in length and stripped of control, Cf and
// line-separator characters (U+2028/2029): image.node and image.jdk reach
// this from a project layer's profile, which is other repositories' text,
// so an unbounded, unescaped echo would let one profile fill another
// repository's `fugaro validate` output or a dashboard.
func TestLegacyImageNodeAndJDKEchoIsBounded(t *testing.T) {
	long := strings.Repeat("9", 3000)
	y := strings.Replace(baseYAML, "    commands:", "    image: { node: \""+long+"\" }\n    commands:", 1)
	_, problems := Parse([]byte(y))
	if len(problems) != 1 {
		t.Fatalf("image.node: %v", problems)
	}
	if strings.Contains(problems[0].Message, long) {
		t.Errorf("image.node: echoed the full %d-byte value", len(long))
	}
	if len(problems[0].Message) >= len(long) {
		t.Errorf("image.node: message (%d bytes) is not bounded: %v", len(problems[0].Message), problems[0])
	}

	bidi := "24‮"
	y = strings.Replace(baseYAML, "    commands:", "    image: { jdk: \""+bidi+"\" }\n    commands:", 1)
	_, problems = Parse([]byte(y))
	if len(problems) != 1 || strings.Contains(problems[0].Message, bidi) || !strings.Contains(problems[0].Message, "202e") {
		t.Errorf("image.jdk: %v, want the bidi character escaped", problems)
	}
}

func TestLintNamesTheMigrationDocForALegacyFrom(t *testing.T) {
	df := []byte("FROM ghcr.io/dimipaun/fugaro-web-node:0.6.0\nRUN git clone x /work/repo\n")
	msgs := LintDockerfile(df)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "a base removed in 0.7.0") || !strings.Contains(msgs[0], MigrationDoc) {
		t.Errorf("LintDockerfile = %v", msgs)
	}
	if msgs := LintDockerfile([]byte("FROM ghcr.io/dimipaun/fugaro-base:0.7.0\nRUN git clone x /work/repo\n")); len(msgs) != 0 {
		t.Errorf("fugaro-base: %v", msgs)
	}
}

func TestOnlyTheBaseKind(t *testing.T) {
	if len(Bases) != 1 || Bases[0] != BaseKind || (Workflow{Base: "go"}).BaseKind() != BaseKind {
		t.Errorf("Bases = %v", Bases)
	}
}
