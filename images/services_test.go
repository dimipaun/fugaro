package images_test

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fugaro-services is run with bash from the image's PATH; these runs need no
// services installed.
func runServices(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"common/fugaro-services"}, args...)...)
	cmd.Env = append(os.Environ(), "FUGARO_SERVICES_DIR="+t.TempDir())
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestFugaroServicesUsageAndStatus(t *testing.T) {
	if out, err := runServices(t, nil); err == nil || !strings.Contains(out, "usage: fugaro-services start|stop|status") {
		t.Errorf("no arguments: err=%v\n%s", err, out)
	}
	if out, err := runServices(t, []string{"FUGARO_SERVICES=mysql"}, "start"); err == nil || !strings.Contains(out, "unknown service 'mysql'") {
		t.Errorf("unknown service: err=%v\n%s", err, out)
	}
	// Nothing listens on port 1, so status reports redis down and exits 1.
	out, err := runServices(t, []string{"FUGARO_SERVICES=redis", "FUGARO_REDIS_PORT=1"}, "status")
	if err == nil || !strings.Contains(out, "redis: down") {
		t.Errorf("status with nothing running: err=%v\n%s", err, out)
	}
	if out, err := runServices(t, nil, "stop"); err != nil || !strings.Contains(out, "nothing to stop") {
		t.Errorf("stop with nothing started: err=%v\n%s", err, out)
	}
}

// With every port already taken, start leaves the services alone and returns
// once they answer, which is what makes it idempotent and safe to run twice.
func TestFugaroServicesStartLeavesAnAnsweringServiceAlone(t *testing.T) {
	ln := listenOnFreePort(t)
	defer ln.Close()
	out, err := runServices(t, []string{"FUGARO_SERVICES=redis", "FUGARO_REDIS_PORT=" + portOf(ln), "FUGARO_SERVICES_TIMEOUT=10"}, "start")
	if err != nil || !strings.Contains(out, "redis: port "+portOf(ln)+" already open") || !strings.Contains(out, "redis: up") {
		t.Errorf("start: err=%v\n%s", err, out)
	}
}

// A failing initdb makes start fail at once with its own message, and a
// postgres that never opens its port makes it fail with the timeout's. The
// fake initdb and pg_ctl live under FUGARO_POSTGRES_LIB/17/bin, as pg_bin
// expects to find an installed major (design base-image.md section 5).
func TestFugaroServicesStartReportsItsFailures(t *testing.T) {
	lib := t.TempDir()
	bin := filepath.Join(lib, "17", "bin")
	write := func(name, body string) {
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"FUGARO_SERVICES=postgres", "FUGARO_POSTGRES_PORT=1", "FUGARO_SERVICES_TIMEOUT=2", "FUGARO_POSTGRES_LIB=" + lib}

	write("initdb", "echo boom >&2; exit 1\n")
	if out, err := runServices(t, env, "start"); err == nil || !strings.Contains(out, "postgres: initdb failed") {
		t.Errorf("failing initdb: err=%v\n%s", err, out)
	}

	// initdb and pg_ctl succeed (the data directory gets its marker), but
	// nothing ever listens on the port.
	write("initdb", "mkdir -p \"$2\" && echo 17 > \"$2/PG_VERSION\"\n")
	write("pg_ctl", "mkdir -p \"$FUGARO_SERVICES_DIR/postgres\"; echo 1 > \"$FUGARO_SERVICES_DIR/postgres/postmaster.pid\"\n")
	out, err := runServices(t, env, "start")
	if err == nil || !strings.Contains(out, "timed out after 2s waiting for: postgres") && !strings.Contains(out, "postgres exited before it was ready") {
		t.Errorf("a postgres that never listens: err=%v\n%s", err, out)
	}
}

// A selected service that is not installed fails start at once, naming what
// to install (design base-image.md section 5).
func TestFugaroServicesNamesTheMissingPackage(t *testing.T) {
	empty := t.TempDir()
	out, err := runServices(t, []string{"FUGARO_SERVICES=postgres", "FUGARO_POSTGRES_LIB=" + empty}, "start")
	if err == nil || !strings.Contains(out, "postgres: not installed; add postgresql-17 to image.apt") {
		t.Errorf("postgres missing: err=%v\n%s", err, out)
	}
	if _, err := exec.LookPath("firebase"); err == nil {
		t.Skip("this machine has firebase on PATH")
	}
	out, err = runServices(t, []string{"FUGARO_SERVICES=firebase"}, "start")
	if err == nil || !strings.Contains(out, `firebase: not installed; add "npm:firebase-tools" to mise.toml`) {
		t.Errorf("firebase missing: err=%v\n%s", err, out)
	}
}

// The newest PostgreSQL major installed is the one used.
func TestFugaroServicesFindsTheNewestPostgres(t *testing.T) {
	lib := t.TempDir()
	marker := filepath.Join(t.TempDir(), "which")
	for _, major := range []string{"16", "17"} {
		bin := filepath.Join(lib, major, "bin")
		testutilWrite(t, filepath.Join(bin, "initdb"), "#!/bin/sh\necho "+major+" > "+marker+"\nexit 1\n")
		testutilWrite(t, filepath.Join(bin, "pg_ctl"), "#!/bin/sh\nexit 1\n")
	}
	out, err := runServices(t, []string{"FUGARO_SERVICES=postgres", "FUGARO_POSTGRES_LIB=" + lib, "FUGARO_SERVICES_DIR=" + t.TempDir()}, "start")
	if err == nil || !strings.Contains(out, "postgres: initdb failed") {
		t.Fatalf("err=%v\n%s", err, out)
	}
	if got, _ := os.ReadFile(marker); strings.TrimSpace(string(got)) != "17" {
		t.Errorf("ran initdb of %q, want 17", got)
	}
}

func testutilWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func listenOnFreePort(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func portOf(ln net.Listener) string {
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}
