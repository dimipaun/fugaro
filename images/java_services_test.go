package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func javaServicesDockerfile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("java-services/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var javaServicesPins = []string{"CLAUDE_CODE_VERSION", "GH_VERSION", "NODE_VERSION", "JDK_VERSION", "POSTGRES_MAJOR", "FIREBASE_TOOLS_VERSION"}

// runSmokeJavaServices runs smoke.sh against the java-services base with a
// fake docker; the fake image reports the Dockerfile's pins unless fakeEnv
// overrides them.
func runSmokeJavaServices(t *testing.T, missing string, fakeEnv ...string) (string, error) {
	t.Helper()
	df := javaServicesDockerfile(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fakeDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "smoke.sh", "fake-image", "java-services")
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		pinned := false
		for _, p := range javaServicesPins {
			pinned = pinned || name == p
		}
		if !pinned {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+dir+":"+os.Getenv("PATH"),
		"FAKE_CLAUDE_VERSION="+argIn(t, df, "CLAUDE_CODE_VERSION"),
		"FAKE_GH_VERSION="+argIn(t, df, "GH_VERSION"),
		"FAKE_NODE_VERSION="+argIn(t, df, "NODE_VERSION"),
		"FAKE_JDK_VERSION="+argIn(t, df, "JDK_VERSION"),
		"FAKE_POSTGRES_VERSION="+argIn(t, df, "POSTGRES_MAJOR"),
		"FAKE_FIREBASE_VERSION="+argIn(t, df, "FIREBASE_TOOLS_VERSION"))
	cmd.Env = append(cmd.Env, fakeEnv...)
	if missing != "" {
		cmd.Env = append(cmd.Env, "FAKE_DOCKER_MISSING="+missing)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestSmokeJavaServicesSucceedsAgainstAHealthyImage(t *testing.T) {
	out, err := runSmokeJavaServices(t, "")
	if err != nil || !strings.Contains(out, "smoke: fake-image ok") || !strings.Contains(out, "services ok") {
		t.Fatalf("smoke.sh java-services: %v\n%s", err, out)
	}
}

func TestSmokeJavaServicesFailsWhenAToolIsMissing(t *testing.T) {
	for _, missing := range []string{"fugaro", "claude", "gh", "java", "node", "firebase", "psql", "redis-server"} {
		t.Run(missing, func(t *testing.T) {
			out, err := runSmokeJavaServices(t, missing)
			if err == nil || strings.Contains(out, "smoke: fake-image ok") {
				t.Fatalf("smoke.sh passed with %s missing:\n%s", missing, out)
			}
		})
	}
}

func TestSmokeJavaServicesChecksThePins(t *testing.T) {
	df := javaServicesDockerfile(t)
	for _, tc := range []struct{ fake, pin string }{
		{"FAKE_JDK_VERSION=21.0.1", "JDK_VERSION=" + argIn(t, df, "JDK_VERSION")},
		{"FAKE_POSTGRES_VERSION=16", "POSTGRES_MAJOR=" + argIn(t, df, "POSTGRES_MAJOR")},
		{"FAKE_FIREBASE_VERSION=1.0.0", "FIREBASE_TOOLS_VERSION=" + argIn(t, df, "FIREBASE_TOOLS_VERSION")},
		{"FAKE_NODE_VERSION=7", "NODE_VERSION major=" + argIn(t, df, "NODE_VERSION")},
		{"FAKE_CLAUDE_VERSION=0.0.1", "CLAUDE_CODE_VERSION=" + argIn(t, df, "CLAUDE_CODE_VERSION")},
		{"FAKE_GH_VERSION=0.0.1", "GH_VERSION=" + argIn(t, df, "GH_VERSION")},
	} {
		t.Run(tc.fake, func(t *testing.T) {
			out, err := runSmokeJavaServices(t, "", tc.fake)
			if err == nil || !strings.Contains(out, tc.pin) {
				t.Fatalf("smoke.sh with %s: err=%v\n%s", tc.fake, err, out)
			}
		})
	}
}

func TestSmokeJavaServicesNeedsEveryEmulatorDownload(t *testing.T) {
	out, err := runSmokeJavaServices(t, "", "FAKE_EMULATOR_JARS=firebase-database-emulator-v4.jar")
	if err == nil || !strings.Contains(out, "no cloud-firestore-emulator download") {
		t.Fatalf("smoke.sh with most jars missing: err=%v\n%s", err, out)
	}
}

func TestSmokeJavaServicesFailsWhenTheServicesDoNotStart(t *testing.T) {
	out, err := runSmokeJavaServices(t, "", "FAKE_SERVICES_FAIL=1")
	if err == nil || !strings.Contains(out, "fugaro-services failed") {
		t.Fatalf("smoke.sh with failing services: err=%v\n%s", err, out)
	}
}

// The java-services base repeats web-node's gh and Claude Code install and
// uses its Node installer and Node pin; they must not drift apart.
func TestJavaServicesSharesWebNodePins(t *testing.T) {
	web, err := os.ReadFile("web-node/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	df := javaServicesDockerfile(t)
	for _, name := range []string{"CLAUDE_CODE_VERSION", "GH_VERSION", "NODE_VERSION"} {
		if got, want := argIn(t, df, name), argIn(t, string(web), name); got != want {
			t.Errorf("java-services %s = %s, web-node has %s", name, got, want)
		}
	}
	for _, sum := range regexp.MustCompile(`[0-9a-f]{64}`).FindAllString(string(web), -1) {
		if !strings.Contains(df, sum) {
			t.Errorf("web-node's checksum %s is missing from the java-services base", sum)
		}
	}
}

// The JDK is Temurin 25 with a sha256 per architecture, and every emulator
// the pinned firebase-tools can download has a setup step.
func TestJavaServicesPinsAreChecksummed(t *testing.T) {
	df := javaServicesDockerfile(t)
	if jv := argIn(t, df, "JDK_VERSION"); !strings.HasPrefix(jv, "25.") {
		t.Errorf("JDK_VERSION %s is not JDK 25", jv)
	}
	if pg := argIn(t, df, "POSTGRES_MAJOR"); !regexp.MustCompile(`^\d+$`).MatchString(pg) {
		t.Errorf("POSTGRES_MAJOR %q is not a major version", pg)
	}
	for _, want := range []string{"sha256sum -c -", "B97B0AFCAA1A47F044F244A07FCC7D46ACCC4CF8", "postgis", "pgvector", "redis-server"} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile lacks %q", want)
		}
	}
	m := regexp.MustCompile(`for e in ([a-z ]+); do`).FindStringSubmatch(df)
	if m == nil {
		t.Fatal("no emulator setup loop")
	}
	if got := strings.Fields(m[1]); strings.Join(got, " ") != "database firestore storage pubsub ui dataconnect" {
		t.Errorf("emulator downloads = %v", got)
	}
	if !strings.Contains(df, "FIREBASE_EMULATORS_PATH=/opt/firebase-emulators") {
		t.Error("the emulator jars are not outside $HOME")
	}
}
