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
	return runServicesRaw(t, nil, append([]string{"FUGARO_SERVICES_DIR=" + t.TempDir()}, env...), nil, args...)
}

// runServicesRaw also takes bash options and the names of inherited variables
// to drop, so a test can run with a setting unset.
func runServicesRaw(t *testing.T, bashOpts, env, unset []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append(append(bashOpts, "common/fugaro-services"), args...)...)
	for _, kv := range os.Environ() {
		drop := false
		for _, u := range unset {
			drop = drop || strings.HasPrefix(kv, u+"=")
		}
		if !drop {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "FUGARO_SERVICES_DIR="+t.TempDir())
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// toolsPath returns a PATH directory holding links to just the named tools,
// skipping the test when one is not on this machine.
func toolsPath(t *testing.T, tools ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range tools {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s is not on PATH", tool)
		}
		if err := os.Symlink(p, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
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
// to install (design base-image.md section 5). The services run with a PATH
// holding only the tools the script needs before it gets there, so the result
// does not depend on what the machine has installed; the ports are pinned to
// ones nothing listens on (1), so a service already running here cannot make
// start leave it alone instead.
func TestFugaroServicesNamesTheMissingPackage(t *testing.T) {
	path := toolsPath(t, "mkdir", "chmod", "rm", "cat", "node")
	empty := t.TempDir()
	out, err := runServices(t, []string{"FUGARO_SERVICES=postgres", "FUGARO_POSTGRES_PORT=1", "FUGARO_POSTGRES_LIB=" + empty, "PATH=" + path}, "start")
	if err == nil || !strings.Contains(out, "postgres: not installed; add postgresql-17 to image.apt") {
		t.Errorf("postgres missing: err=%v\n%s", err, out)
	}
	out, err = runServices(t, []string{"FUGARO_SERVICES=redis", "FUGARO_REDIS_PORT=1", "PATH=" + path}, "start")
	if err == nil || !strings.Contains(out, "redis: not installed; add redis-server to image.apt") {
		t.Errorf("redis missing: err=%v\n%s", err, out)
	}
	cfg := filepath.Join(t.TempDir(), "firebase.json")
	testutilWrite(t, cfg, `{"emulators":{"database":{"port":1}}}`)
	out, err = runServices(t, []string{"FUGARO_SERVICES=firebase", "FUGARO_FIREBASE_EMULATORS=database", "FUGARO_FIREBASE_CONFIG=" + cfg, "PATH=" + path}, "start")
	if err == nil || !strings.Contains(out, `firebase: not installed; add "npm:firebase-tools" to mise.toml`) {
		t.Errorf("firebase missing: err=%v\n%s", err, out)
	}
}

// With FUGARO_POSTGRES_LIB unset the script looks under /usr/lib/postgresql,
// where Debian's postgresql-<major> packages put each major.
func TestFugaroServicesDefaultsToDebiansPostgresLibrary(t *testing.T) {
	out, _ := runServicesRaw(t, []string{"-x"}, nil, []string{"FUGARO_POSTGRES_LIB"}, "status")
	if !strings.Contains(out, "PG_LIB=/usr/lib/postgresql\n") {
		t.Errorf("default library not /usr/lib/postgresql:\n%s", out)
	}
}

// stop finds the PostgreSQL major the way start does and stops the cluster
// with its pg_ctl, and looks for stray emulator processes under the
// firebase-tools cache in HOME.
func TestFugaroServicesStopUsesTheInstalledPostgresAndTheDefaultEmulatorCache(t *testing.T) {
	state, lib, home := t.TempDir(), t.TempDir(), t.TempDir()
	fake := t.TempDir()
	pgArgs, pkillArgs := filepath.Join(fake, "pg_ctl.args"), filepath.Join(fake, "pkill.args")
	testutilWrite(t, filepath.Join(lib, "17", "bin", "initdb"), "#!/bin/sh\nexit 1\n")
	testutilWrite(t, filepath.Join(lib, "17", "bin", "pg_ctl"), "#!/bin/sh\necho \"$@\" > "+pgArgs+"\n")
	testutilWrite(t, filepath.Join(fake, "pkill"), "#!/bin/sh\necho \"$@\" > "+pkillArgs+"\nexit 1\n")
	testutilWrite(t, filepath.Join(state, "run", "keep"), "")
	testutilWrite(t, filepath.Join(state, "postgres", "postmaster.pid"), "1\n")
	env := []string{"FUGARO_SERVICES_DIR=" + state, "FUGARO_POSTGRES_LIB=" + lib, "HOME=" + home, "PATH=" + fake + ":" + os.Getenv("PATH")}
	out, err := runServicesRaw(t, nil, env, []string{"FIREBASE_EMULATORS_PATH"}, "stop")
	if err != nil || !strings.Contains(out, "stopped") {
		t.Fatalf("stop: err=%v\n%s", err, out)
	}
	if got, _ := os.ReadFile(pgArgs); !strings.Contains(string(got), "stop") {
		t.Errorf("pg_ctl of the installed major was not run to stop the cluster: %q", got)
	}
	if got, _ := os.ReadFile(pkillArgs); !strings.Contains(string(got), home+"/.cache/firebase/emulators/") {
		t.Errorf("pkill did not look under $HOME/.cache/firebase/emulators: %q", got)
	}
}

// The newest PostgreSQL major installed is the one used, compared as version
// numbers (17 over 9.6, which a plain sort would get backwards).
func TestFugaroServicesFindsTheNewestPostgres(t *testing.T) {
	lib := t.TempDir()
	marker := filepath.Join(t.TempDir(), "which")
	for _, major := range []string{"9.6", "16", "17"} {
		bin := filepath.Join(lib, major, "bin")
		testutilWrite(t, filepath.Join(bin, "initdb"), "#!/bin/sh\necho "+major+" > "+marker+"\nexit 1\n")
		testutilWrite(t, filepath.Join(bin, "pg_ctl"), "#!/bin/sh\nexit 1\n")
	}
	out, err := runServices(t, []string{"FUGARO_SERVICES=postgres", "FUGARO_POSTGRES_PORT=1", "FUGARO_POSTGRES_LIB=" + lib, "FUGARO_SERVICES_DIR=" + t.TempDir()}, "start")
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
