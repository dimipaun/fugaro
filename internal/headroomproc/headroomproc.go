// Package headroomproc supervises a sidecar process: it starts a binary on its
// own ephemeral loopback port, waits for a readiness probe that proves the
// listener is the process it launched, keeps checking that listener while the
// process runs, bounds the process's address space, and stops it with a
// process-group kill. It knows nothing about what the sidecar does — no Claude
// Code, no gateway, no configuration.
package headroomproc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/logtail"
)

// addrEnv and tokenEnv are how a sidecar learns where to listen and what to
// answer its health path with: the whole contract between this package and the
// binary it supervises. A listener that does not echo the token cannot pass
// the readiness probe, which is what makes an impersonator on the port
// detectable — nothing else can tell "the process Start launched" from
// "something that took the port first".
const (
	addrEnv  = "HEADROOMPROC_ADDR"
	tokenEnv = "HEADROOMPROC_TOKEN"
)

const (
	// probePoll is how often the readiness probe retries, and probeTimeout
	// bounds one probe's own request. A liveness probe's timeout is the
	// smaller of probeTimeout and its period, so the probe itself can never
	// overrun the interval it is scheduled on.
	probePoll    = 25 * time.Millisecond
	probeTimeout = 2 * time.Second
	// maxProbeBody bounds how much of a probe's answer is read: an
	// impersonator's body is not this package's to trust or to buffer.
	maxProbeBody = 4 << 10
	// waitDelay is exec's own bound on draining the child's output after it
	// exits, so a descendant holding the pipe cannot make Wait — and so Stop
	// — hang. internal/procgroup's cmdWaitDelay is the same 2s for the same
	// reason.
	waitDelay = 2 * time.Second

	defaultReadyTimeout   = 10 * time.Second
	defaultGrace          = 10 * time.Second
	defaultLivenessMisses = 3
)

// Options configure a sidecar process. Env must be non-nil, the same refusal
// agent.Claude.Run already makes for its own Env: a nil Env inherits the
// launcher's full environment, which must never happen here.
//
// The sidecar is told where to listen and what to answer its health path with
// through addrEnv and tokenEnv, which Start appends to Env: any other
// configuration reaches it through Args or Env, and nothing of this package's
// own environment does.
type Options struct {
	Bin            string          // the pinned binary's path
	Args           []string        // confirmed once a real binary is pinned; a config file path is likely, not argv
	Env            []string        // explicit allowlist; never nil
	ReadyPath      string          // a health-check path on the sidecar's own HTTP server
	ReadyTimeout   time.Duration   // how long Start waits for a verified-origin 200 from ReadyPath; 0 is 10s
	LivenessPeriod time.Duration   // how often, after readiness, the same check repeats; 0 disables it (tests only)
	LivenessMisses int             // consecutive failures before Dead() closes for a hang, not only an exit; 0 is 3
	MemoryLimitAS  int64           // RLIMIT_AS bytes for the child; 0 means none. Best effort: see Start
	Grace          time.Duration   // SIGTERM -> SIGKILL grace on Stop, mirroring procgroup's shape; 0 is 10s
	LogTail        *logtail.Writer // a bounded, caller-owned sink for a redacted stdout/stderr tail; nil discards
}

// Process is a running sidecar.
type Process struct {
	cmd   *exec.Cmd
	url   string
	tail  *logtail.Writer
	grace time.Duration

	// exited is closed when the process itself is gone, and dead when it is
	// no longer usable — which is exited, or a liveness probe that has failed
	// LivenessMisses times in a row. Stop ends the process too, so it closes
	// dead as well: the channel reports "not usable", not "crashed".
	exited    chan struct{}
	dead      chan struct{}
	closeOnce sync.Once
	stopOnce  sync.Once
	stopping  chan struct{}
}

// Start launches the sidecar on an ephemeral loopback port, waits for a
// readiness probe that proves the answering process is the one Start itself
// launched (a per-start random token echoed back), and returns a handle. It
// fails if the process exits before becoming ready, if ReadyTimeout elapses
// first, or if something else answers the port without the expected token
// (impersonation). A failure ends the process it started before returning.
//
// The port is chosen by listening on 127.0.0.1:0 and closing the listener
// again, so the sidecar can bind it. That leaves a window in which another
// process could take the port first, and the token, not the port, is what
// catches it.
//
// MemoryLimitAS is best effort, and it is address space rather than resident
// memory: the value must clear whatever the sidecar's own runtime reserves at
// startup. It is applied with prlimit once the sidecar is ready, not before
// exec, because a runtime that reserves that space up front — Go's own, and
// the pattern the design names for Python and garbage-collected Rust — cannot
// start at all under a pre-exec RLIMIT_AS small enough to be a useful bound,
// and cannot finish starting under one applied in the milliseconds after exec
// either. It is therefore not a cgroup and not a guarantee: it bounds what the
// sidecar allocates from readiness on, and it does nothing where the platform
// has no prlimit.
func Start(ctx context.Context, o Options) (*Process, error) {
	if o.Env == nil {
		return nil, errors.New("headroomproc: refusing to start a sidecar with a nil Env, which would inherit the launcher's full environment")
	}
	if o.Bin == "" {
		return nil, errors.New("headroomproc: no binary to start")
	}
	if o.ReadyPath == "" {
		return nil, errors.New("headroomproc: no readiness path to probe")
	}
	ready := o.ReadyTimeout
	if ready <= 0 {
		ready = defaultReadyTimeout
	}
	grace := o.Grace
	if grace <= 0 {
		grace = defaultGrace
	}
	misses := o.LivenessMisses
	if misses <= 0 {
		misses = defaultLivenessMisses
	}
	addr, err := loopbackAddr()
	if err != nil {
		return nil, err
	}
	token, err := probeToken()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(o.Bin, o.Args...)
	cmd.Env = append(append([]string{}, o.Env...), addrEnv+"="+addr, tokenEnv+"="+token)
	if o.LogTail != nil {
		cmd.Stdout, cmd.Stderr = o.LogTail, o.LogTail
	}
	setProcGroup(cmd)
	cmd.WaitDelay = waitDelay
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("headroomproc: start %s: %w", o.Bin, err)
	}
	p := &Process{
		cmd:      cmd,
		url:      "http://" + addr,
		tail:     o.LogTail,
		grace:    grace,
		exited:   make(chan struct{}),
		dead:     make(chan struct{}),
		stopping: make(chan struct{}),
	}
	// The exit watch starts first: every failure path below ends the process,
	// and kill waits for the reaping this goroutine does.
	go p.wait()
	client := probeClient()
	if err := p.awaitReady(ctx, client, o.ReadyPath, token, ready); err != nil {
		p.kill()
		return nil, err
	}
	if o.MemoryLimitAS > 0 {
		if err := limitAddressSpace(cmd.Process.Pid, o.MemoryLimitAS); err != nil {
			p.kill()
			return nil, fmt.Errorf("headroomproc: memory limit: %w", err)
		}
	}
	if o.LivenessPeriod > 0 {
		go p.watchLiveness(client, o.ReadyPath, token, o.LivenessPeriod, misses)
	}
	return p, nil
}

// URL is the sidecar's own loopback base URL.
func (p *Process) URL() string { return p.url }

// Dead returns a channel closed when the sidecar is no longer usable: it
// exited on its own (a crash), or LivenessMisses consecutive liveness probes
// failed (a hang). Stop ends the process, so it closes this channel too; the
// caller is what distinguishes a stop it asked for from a crash. Either way
// this package assigns no status of its own.
func (p *Process) Dead() <-chan struct{} { return p.dead }

// Stop sends SIGTERM to the process group, escalating to SIGKILL after Grace,
// mirroring procgroup.Run's own shutdown shape. The group is SIGKILLed even
// when the direct child has already exited, so a daemon it left behind does
// not survive. Stop is safe to call more than once.
func (p *Process) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() { close(p.stopping) })
	_ = terminateGroup(p.cmd)
	select {
	case <-p.exited:
	case <-ctx.Done():
	case <-time.After(p.grace):
	}
	_ = killGroup(p.cmd)
	select {
	case <-p.exited:
	case <-time.After(waitDelay + time.Second):
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// wait reaps the process and reports it dead.
func (p *Process) wait() {
	_ = p.cmd.Wait()
	close(p.exited)
	p.closeOnce.Do(func() { close(p.dead) })
}

// kill ends the process and everything in its group, and waits for it: the
// failure paths of Start must not leave a sidecar behind.
func (p *Process) kill() {
	_ = terminateGroup(p.cmd)
	select {
	case <-p.exited:
	case <-time.After(p.grace):
	}
	_ = killGroup(p.cmd)
	select {
	case <-p.exited:
	case <-time.After(waitDelay + time.Second):
	}
}

// awaitReady polls the origin-proof probe until it succeeds, the process
// exits, the caller's context ends, or the timeout runs out.
func (p *Process) awaitReady(ctx context.Context, client *http.Client, path, token string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		err := checkOrigin(probeCtx, client, p.url, path, token)
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-p.exited:
			return fmt.Errorf("headroomproc: sidecar exited before it was ready: %w%s", err, tailNote(p.tail))
		case <-ctx.Done():
			return fmt.Errorf("headroomproc: waiting for readiness: %w", ctx.Err())
		case <-time.After(time.Until(deadline)):
			return fmt.Errorf("headroomproc: sidecar not ready within %s: %w%s", timeout, err, tailNote(p.tail))
		case <-time.After(probePoll):
		}
	}
}

// watchLiveness repeats the readiness probe on an interval: a process that
// stays up and stops answering is invisible to the exit watch, and is treated
// exactly like a crash once misses probes in a row have failed.
func (p *Process) watchLiveness(client *http.Client, path, token string, period time.Duration, misses int) {
	timeout := min(period, probeTimeout)
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	failed := 0
	for {
		select {
		case <-p.stopping:
			return
		case <-p.exited:
			return
		case <-ticker.C:
		}
		probeCtx, cancel := context.WithTimeout(context.Background(), timeout)
		err := checkOrigin(probeCtx, client, p.url, path, token)
		cancel()
		if err == nil {
			failed = 0
			continue
		}
		failed++
		if failed >= misses {
			p.closeOnce.Do(func() { close(p.dead) })
			return
		}
	}
}

// checkOrigin is the readiness/liveness probe's own logic: it GETs path on
// base, and reports ready only if the response is 200 with the exact
// wantToken body.
func checkOrigin(ctx context.Context, client *http.Client, base, path, wantToken string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return fmt.Errorf("headroomproc: probe request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("headroomproc: probe answered %s, want 200", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	if err != nil {
		return fmt.Errorf("headroomproc: read probe answer: %w", err)
	}
	if string(body) != wantToken {
		return errors.New("headroomproc: the listener did not echo the probe's token, so it is not the process Start launched")
	}
	return nil
}

// probeClient is the probe's own HTTP client: no proxy (a loopback probe must
// not be routed anywhere else by the environment) and no redirects (a
// listener that answers by pointing elsewhere has not answered).
func probeClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// loopbackAddr reserves a free loopback port and gives it back, so the sidecar
// can bind it. The port is the sidecar's own: nothing else on the machine is
// told about it.
func loopbackAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("headroomproc: reserve a loopback port: %w", err)
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

func probeToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("headroomproc: probe token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// tailNote adds what the sidecar last said, when the caller asked for a tail:
// a startup or crash reason is the only thing that makes a failure
// diagnosable.
func tailNote(t *logtail.Writer) string {
	if t == nil {
		return ""
	}
	if s := strings.TrimSpace(t.String()); s != "" {
		return "; last output: " + s
	}
	return ""
}
