package terraform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scanFake is the body of both fakes, a trivy and a docker: each records the
// arguments it was given and the Trivy cache directory it was handed under
// FAKE_LOG_DIR, named after itself, and exits FAKE_EXIT.
const scanFake = `#!/bin/sh
log="$FAKE_LOG_DIR/$(basename "$0")"
printf '%s\n' "${TRIVY_CACHE_DIR-}" > "$log.env"
printf '%s\n' "$*" > "$log"
exit "${FAKE_EXIT:-0}"
`

// scanResult is what one route's program left behind: the arguments it was
// given, and the cache directory it read TRIVY_CACHE_DIR as.
type scanResult struct {
	args  string
	cache string
}

// scanRan is whether the program ran at all, which is how the tests tell the
// route that was taken from the one that was not.
func (s scanResult) scanRan() bool { return s.args != "" }

func readScan(t *testing.T, dir, name string) scanResult {
	t.Helper()
	args, err := os.ReadFile(filepath.Join(dir, name))
	if os.IsNotExist(err) {
		return scanResult{}
	}
	if err != nil {
		t.Fatal(err)
	}
	cache, err := os.ReadFile(filepath.Join(dir, name+".env"))
	if err != nil {
		t.Fatal(err)
	}
	return scanResult{args: strings.TrimSpace(string(args)), cache: strings.TrimSpace(string(cache))}
}

// scanRoute runs deploy/terraform/scan.sh with the fakes above on PATH, and
// returns what the fake trivy and the fake docker were given. withTrivy is
// whether the script finds a trivy on PATH, which is the whole of which route
// it takes; a PATH without one is built by dropping the entries that carry a
// real trivy, so the test decides the route and not the machine.
func scanRoute(t *testing.T, withTrivy bool, exit string) (trivy, docker scanResult, cache string, err error) {
	t.Helper()
	fakes := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakes, "docker"), []byte(scanFake), 0o755); err != nil {
		t.Fatal(err)
	}
	if withTrivy {
		if err := os.WriteFile(filepath.Join(fakes, "trivy"), []byte(scanFake), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	logs := t.TempDir()
	cache = filepath.Join(logs, "cache")
	cmd := exec.Command("sh", "scan.sh")
	cmd.Env = append(os.Environ(),
		"PATH="+fakes+":"+pathWithoutTrivy(),
		"FAKE_LOG_DIR="+logs, "FAKE_EXIT="+exit, "TRIVY_CACHE_DIR="+cache)
	out, err := cmd.CombinedOutput()
	if err != nil && exit == "0" {
		t.Fatalf("scan.sh failed: %v\n%s", err, out)
	}
	return readScan(t, logs, "trivy"), readScan(t, logs, "docker"), cache, err
}

// pathWithoutTrivy is this process's PATH minus every directory that holds an
// executable named trivy.
func pathWithoutTrivy() string {
	var kept []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "trivy")); err == nil {
			continue
		}
		kept = append(kept, dir)
	}
	return strings.Join(kept, string(filepath.ListSeparator))
}

// TestScanRunsTheTrivyOnPath: a run's job image bakes Trivy and has no Docker
// daemon, so with one on PATH the script must run it -- with the flags, the
// .trivyignore and the cache the docker route gives the image, and without
// touching Docker at all.
func TestScanRunsTheTrivyOnPath(t *testing.T) {
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	direct, docker, cache, err := scanRoute(t, true, "0")
	if err != nil {
		t.Fatal(err)
	}
	if docker.scanRan() {
		t.Errorf("the docker route ran with a trivy on PATH: %s", docker.args)
	}
	want := "config --skip-version-check --exit-code 1 --severity HIGH,CRITICAL --ignorefile " +
		filepath.Join(here, ".trivyignore") + " --skip-dirs **/.terraform " + here
	if direct.args != want {
		t.Errorf("the trivy on PATH was run as\n%s\nwant\n%s", direct.args, want)
	}
	if direct.cache != cache {
		t.Errorf("the trivy on PATH was handed TRIVY_CACHE_DIR %q, want the docker route's %q", direct.cache, cache)
	}
}

// TestScanRunsThePinnedImageWithoutTrivy: with no trivy on PATH the script is
// the docker route it always was -- images/scan.sh's pinned image, over the
// /src mount.
func TestScanRunsThePinnedImageWithoutTrivy(t *testing.T) {
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := os.ReadFile(filepath.Join(here, "..", "..", "images", "scan.sh"))
	if err != nil {
		t.Fatal(err)
	}
	pin := ""
	for _, line := range strings.Split(string(pinned), "\n") {
		if strings.HasPrefix(line, "trivy_image=") {
			pin = strings.TrimPrefix(line, "trivy_image=")
		}
	}
	if pin == "" {
		t.Fatal("images/scan.sh carries no trivy_image pin")
	}
	trivy, docker, cache, err := scanRoute(t, false, "0")
	if err != nil {
		t.Fatal(err)
	}
	if trivy.scanRan() {
		t.Errorf("a trivy was run with none on PATH: %s", trivy.args)
	}
	for _, want := range []string{
		"run --rm -v " + here + ":/src:ro -v " + cache + ":/root/.cache/trivy " + pin,
		"config --skip-version-check --exit-code 1 --severity HIGH,CRITICAL",
		"--ignorefile /src/.trivyignore",
		"--skip-dirs **/.terraform /src",
	} {
		if !strings.Contains(docker.args, want) {
			t.Errorf("the docker route lacks %q: %s", want, docker.args)
		}
	}
}

// TestScanRoutesAreOneCommandLine: the routes differ in the program and in the
// paths of the mount the image route makes, and in nothing else. That is what
// makes a finding one route reports a finding the other reports -- the same
// flags, the same .trivyignore, the same Trivy cache.
func TestScanRoutesAreOneCommandLine(t *testing.T) {
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	direct, notDocker, _, err := scanRoute(t, true, "0")
	if err != nil {
		t.Fatal(err)
	}
	if notDocker.scanRan() {
		t.Errorf("the docker route ran with a trivy on PATH: %s", notDocker.args)
	}
	_, docker, cache, err := scanRoute(t, false, "0")
	if err != nil {
		t.Fatal(err)
	}
	// Everything up to the scanned directory is the route's own: the docker
	// invocation, its mounts and images/scan.sh's pinned image.
	fields := strings.Fields(docker.args)
	if len(fields) < 8 {
		t.Fatalf("the docker route is not the invocation the script builds: %s", docker.args)
	}
	want := "run --rm -v " + here + ":/src:ro -v " + cache + ":/root/.cache/trivy"
	if got := strings.Join(fields[:6], " "); got != want {
		t.Errorf("the docker route's invocation is\n%s\nwant\n%s", got, want)
	}
	args := fields[7:]
	args[len(args)-1] = here
	for i, a := range args {
		if a == "/src/.trivyignore" {
			args[i] = filepath.Join(here, ".trivyignore")
		}
	}
	if got := strings.Join(args, " "); got != direct.args {
		t.Errorf("the two routes run different command lines:\nPATH:   %s\ndocker: %s", direct.args, got)
	}
}

// TestScanRoutesFailOnAFinding: a finding is scan.sh's own exit code, on
// either route -- the exit-code 1 the flags ask Trivy for has to reach the
// caller.
func TestScanRoutesFailOnAFinding(t *testing.T) {
	for _, withTrivy := range []bool{true, false} {
		if _, _, _, err := scanRoute(t, withTrivy, "1"); err == nil {
			t.Errorf("trivy on PATH=%v: a finding passed", withTrivy)
		}
	}
}
