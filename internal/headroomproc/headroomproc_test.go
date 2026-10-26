//go:build unix

package headroomproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The fake sidecar is this test binary, re-executed with helperEnv set: no nc,
// no shell one-liner, no external tool, and nothing but loopback.
const (
	helperEnv  = "HEADROOMPROC_HELPER"
	pidFileEnv = "HEADROOMPROC_PIDFILE"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		runHelper(mode)
		return
	}
	os.Exit(m.Run())
}

// runHelper is the fake sidecar. It reads the address and the token Start put
// in its environment -- addrEnv and tokenEnv, the same contract a real sidecar
// has -- and behaves as the mode names.
func runHelper(mode string) {
	switch mode {
	case "exit1":
		os.Exit(1)
	case "nolisten":
		select {}
	case "sleeper":
		// The grandchild of "daemon": it ignores SIGTERM, so only a signal to
		// the whole process group can end it.
		signal.Ignore(syscall.SIGTERM)
		if path := os.Getenv(pidFileEnv); path != "" {
			if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
				helperFail("write pid file: %v", err)
			}
		}
		select {}
	case "daemon":
		signal.Ignore(syscall.SIGTERM)
		child := exec.Command(os.Args[0], "-test.run=TestMain")
		child.Env = []string{helperEnv + "=sleeper", pidFileEnv + "=" + os.Getenv(pidFileEnv)}
		if err := child.Start(); err != nil {
			helperFail("spawn grandchild: %v", err)
		}
		serve(false)
		select {}
	case "ready":
		serve(false)
		select {}
	case "crash":
		serve(false)
		// Long enough that readiness is answered and Start has returned; then
		// the process ends on its own, which is what Dead must report.
		time.Sleep(150 * time.Millisecond)
		os.Exit(2)
	case "hang":
		// Answers the readiness probe, then stops answering while staying up:
		// the process is alive and the port accepts, so only the liveness
		// probe can notice.
		serve(true)
		select {}
	case "balloon":
		serve(false)
		// The cap is applied once the sidecar is ready, so the helper lets
		// that land before it allocates: a sidecar does not balloon the
		// instant it answers its first probe.
		time.Sleep(200 * time.Millisecond)
		balloon()
	default:
		helperFail("unknown helper mode %q", mode)
	}
}

// serve answers the health path with the token Start expects. With answerOnce,
// only the first answer is written and every later request is left hanging
// without a response: that is how "hang" stays up and reachable while going
// silent, which is the failure only the liveness probe can see.
func serve(answerOnce bool) {
	token := os.Getenv(tokenEnv)
	ln, err := net.Listen("tcp", os.Getenv(addrEnv))
	if err != nil {
		helperFail("listen: %v", err)
	}
	var once sync.Once
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if answerOnce {
			first := false
			once.Do(func() { first = true })
			if !first {
				select {}
			}
		}
		_, _ = io.WriteString(w, token)
	})}
	go func() { _ = srv.Serve(ln) }()
}

// balloon allocates past whatever MemoryLimitAS allows, touching every page so
// the runtime cannot hand back untouched memory, until the limit kills it.
func balloon() {
	var keep [][]byte
	for {
		b := make([]byte, 1<<20)
		for i := 0; i < len(b); i += 4096 {
			b[i] = 1
		}
		keep = append(keep, b)
	}
}

func helperFail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "headroomproc test helper: "+format+"\n", args...)
	os.Exit(3)
}

// helper returns the fake sidecar's binary, its arguments and the environment
// Start is given: a minimal one, so nothing of the test process's own
// environment reaches the child.
func helper(t *testing.T, mode string, extra ...string) (bin string, args, env []string) {
	t.Helper()
	return os.Args[0], []string{"-test.run=TestMain"}, append([]string{
		"PATH=/usr/bin:/bin",
		helperEnv + "=" + mode,
	}, extra...)
}

func start(t *testing.T, o Options) *Process {
	t.Helper()
	if o.Bin == "" {
		o.Bin, o.Args, o.Env = helper(t, "ready")
	}
	p, err := Start(context.Background(), o)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.Stop(stopCtx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	return p
}

func TestStartWaitsForReadyThenReturns(t *testing.T) {
	bin, args, env := helper(t, "ready")
	p, err := Start(context.Background(), Options{Bin: bin, Args: args, Env: env, ReadyPath: "/healthz", ReadyTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop(context.Background())
	if p.URL() == "" {
		t.Fatal("a ready Process has no URL")
	}
	if !strings.HasPrefix(p.URL(), "http://127.0.0.1:") {
		t.Fatalf("URL = %q, want a loopback base URL", p.URL())
	}
}

func TestStartRefusesNilEnv(t *testing.T) {
	bin, args, _ := helper(t, "ready")
	if _, err := Start(context.Background(), Options{Bin: bin, Args: args, ReadyPath: "/healthz", ReadyTimeout: time.Second}); err == nil {
		t.Fatal("a nil Env was accepted, which would inherit the launcher's whole environment")
	}
}

func TestStartFailsIfProcessExitsBeforeReady(t *testing.T) {
	bin, args, env := helper(t, "exit1")
	if _, err := Start(context.Background(), Options{Bin: bin, Args: args, Env: env, ReadyPath: "/healthz", ReadyTimeout: time.Second}); err == nil {
		t.Fatal("a binary that exits immediately was accepted as ready")
	}
}

func TestStartFailsWhenNothingAnswers(t *testing.T) {
	bin, args, env := helper(t, "nolisten")
	started := time.Now()
	if _, err := Start(context.Background(), Options{Bin: bin, Args: args, Env: env, ReadyPath: "/healthz", ReadyTimeout: 300 * time.Millisecond}); err == nil {
		t.Fatal("a sidecar that never answered was accepted as ready")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("Start waited %v for a %v readiness timeout", elapsed, 300*time.Millisecond)
	}
}

// TestReadinessProbeRejectsImpersonator tests checkOrigin directly against an
// httptest.Server standing in for a port-squatter: no process is spawned,
// because the function under test is the origin check itself, not process
// supervision -- isolating this from the OS's own ephemeral-port allocation,
// which the test cannot control deterministically.
//
// Mutation: accept a body that merely contains the token (strings.Contains,
// or a prefix test) instead of requiring it exactly. The suffix case below
// then passes and this test fails.
func TestReadinessProbeRejectsImpersonator(t *testing.T) {
	const want = "tok1"
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{name: "wrong token", status: http.StatusOK, body: "not-the-real-token", wantErr: true},
		{name: "token with more after it", status: http.StatusOK, body: want + "-and-more", wantErr: true},
		{name: "empty body", status: http.StatusOK, body: "", wantErr: true},
		{name: "not a 200", status: http.StatusServiceUnavailable, body: want, wantErr: true},
		{name: "the real token", status: http.StatusOK, body: want, wantErr: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			err := checkOrigin(context.Background(), srv.Client(), srv.URL, "/healthz", want)
			if tc.wantErr && err == nil {
				t.Fatal("an impersonating listener was accepted as the real sidecar")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("the real sidecar's own answer was refused: %v", err)
			}
		})
	}
}

func TestDeadClosesOnCrash(t *testing.T) {
	bin, args, env := helper(t, "crash")
	p := start(t, Options{Bin: bin, Args: args, Env: env, ReadyPath: "/healthz", ReadyTimeout: 2 * time.Second})
	select {
	case <-p.Dead():
	case <-time.After(2 * time.Second):
		t.Fatal("Dead() never closed after the process crashed")
	}
}

func TestDeadClosesOnLivenessFailure(t *testing.T) {
	bin, args, env := helper(t, "hang")
	p := start(t, Options{Bin: bin, Args: args, Env: env, ReadyPath: "/healthz", ReadyTimeout: 2 * time.Second, LivenessPeriod: 50 * time.Millisecond, LivenessMisses: 2})
	select {
	case <-p.Dead():
	case <-time.After(5 * time.Second):
		t.Fatal("Dead() never closed for a sidecar that stopped answering without exiting")
	}
}

func TestStopKillsTheWholeProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	bin, args, env := helper(t, "daemon", pidFileEnv+"="+pidFile)
	p, err := Start(context.Background(), Options{Bin: bin, Args: args, Env: env, ReadyPath: "/healthz", ReadyTimeout: 5 * time.Second, Grace: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := waitForPID(t, pidFile)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the grandchild %d is not running before Stop: %v", pid, err)
	}
	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild %d survived Stop: only the direct child was killed", pid)
}

func TestMemoryLimitKillsAnOverBudgetSidecar(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RLIMIT_AS enforcement is Linux-specific; Cloud Run is Linux, so this still covers the real target")
	}
	bin, args, env := helper(t, "balloon")
	p := start(t, Options{Bin: bin, Args: args, Env: env, ReadyPath: "/healthz", ReadyTimeout: 2 * time.Second, MemoryLimitAS: 64 << 20})
	select {
	case <-p.Dead():
	case <-time.After(10 * time.Second):
		t.Fatal("an over-budget sidecar was not killed by MemoryLimitAS")
	}
	// Not a guarantee for every runtime (design §2: RLIMIT_AS is unreliable
	// for Python/GC'd Rust); this pins the mechanism for a plain allocator,
	// which is what the helper's "balloon" mode exercises.
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatalf("pid file %s: %v", path, err)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return 0
}
