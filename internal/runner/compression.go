package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/headroomproc"
	"github.com/dimipaun/fugaro/internal/logtail"
)

// SidecarProcess is a running context-compression sidecar, as the runner
// uses one: the three methods startCompression calls, and nothing else.
// *headroomproc.Process satisfies it as it stands, and Deps.StartHeadroom's
// test fake does too.
type SidecarProcess interface {
	URL() string
	Dead() <-chan struct{}
	Stop(context.Context) error
}

// headroomBin is where the base image's pin installs the sidecar: the
// root-owned, read-only /usr/local/bin every other pinned single-binary
// harness lands in (images/base/Dockerfile), which the run's own user cannot
// write, so the agent can never swap the compressor for one of its own
// (design §5). The image does not bake it yet -- pinning the real artifact
// is a later task -- and no run reaches this path before the config key's
// refusal is lifted, so nothing today starts a process here.
const headroomBin = "/usr/local/bin/headroom"

// headroomUpstreamEnv is the one variable the sidecar's own environment
// carries: where it forwards what it is asked to compress, which is the
// gateway's loopback URL and never the provider's. The exact name a real
// Headroom reads is confirmed when the binary is pinned; what this task
// fixes is that the sidecar is told one thing, and that thing is the
// gateway.
const headroomUpstreamEnv = "ANTHROPIC_BASE_URL"

// headroomReadyPath is the health path the readiness probe checks before the
// sidecar is trusted, and the liveness probe repeats: the same check answers
// "is it up" and, later, "is it still answering" (P8).
const headroomReadyPath = "/healthz"

// headroomLivenessPeriod and headroomLivenessMisses are P8's bound on a
// hang: the sidecar is probed every period, and misses failures in a row
// before it is treated as dead. A sidecar that is up but has stopped
// answering is invisible to the process exit watch, and must fail the stage
// it is under rather than let it wait out its deadline.
const (
	headroomLivenessPeriod = 30 * time.Second
	headroomLivenessMisses = 3
)

// sidecarStopTimeout bounds stopping the sidecar at run end, mirroring
// gatewayCloseTimeout's own bound on closing the gateway.
const sidecarStopTimeout = 10 * time.Second

// startCompression starts the run's sidecar, once per run (P4). It runs
// after startGateway has returned -- the sidecar's upstream is the gateway's
// loopback URL, which does not exist before that -- and before the agent's
// environment is built, which needs the sidecar's URL in the same breath.
//
// It is the one place the budget-gateway requirement is enforced: the
// effective budget mode is the tightest of the installation's ceiling, the
// default branch's file and the run's own, so no config layer can answer it
// (P9). A run that asks for compression with the gateway off is refused
// here, at bootstrap, before any stage.
func (r *run) startCompression(ctx context.Context) error {
	if r.cfg.Agent.Compression == "" {
		return nil
	}
	if !r.gatewayOn() {
		return errors.New("agent.compression needs the budget gateway on (budget.mode: observe or enforce): the sidecar holds no credential of its own")
	}
	gw := r.gateway()
	if gw == nil {
		// Belt and braces: gatewayOn means startGateway ran and returned, so
		// this cannot fire today -- but the URL below is what the sidecar
		// would be told to forward to, and a nil gateway has no URL to give
		// it.
		return errors.New("agent.compression is on and the budget gateway is on, but no gateway is running")
	}
	tail := logtail.New(logtail.DefaultLines, logtail.DefaultLineBytes)
	p, err := r.startSidecar(ctx, headroomproc.Options{
		Bin: headroomBin,
		// The sidecar's whole environment (P7): its own upstream and
		// nothing else. Never r.d.Env, which holds the run's real
		// credentials, and never the gateway's token -- Claude Code already
		// presents that on every call it makes, and an ordinary reverse
		// proxy forwards the header it was sent.
		Env:            []string{headroomUpstreamEnv + "=" + gw.BaseURL},
		ReadyPath:      headroomReadyPath,
		LivenessPeriod: headroomLivenessPeriod,
		LivenessMisses: headroomLivenessMisses,
		LogTail:        tail,
	})
	if err != nil {
		// The reason a startup failed is the only thing that makes it
		// diagnosable, and Start's own error already carries the sidecar's
		// bounded, clipped tail when it has one.
		return fmt.Errorf("starting the Headroom sidecar: %s", r.redact(err.Error()))
	}
	r.mu.Lock()
	r.sidecar = p
	r.compression = &agent.CompressionProxy{BaseURL: p.URL()}
	r.mu.Unlock()
	return nil
}

// startSidecar is headroomproc.Start, or the fake Deps.StartHeadroom hands
// back: the seam that keeps every test out of a real process.
func (r *run) startSidecar(ctx context.Context, o headroomproc.Options) (SidecarProcess, error) {
	if r.d.StartHeadroom != nil {
		return r.d.StartHeadroom(ctx, o)
	}
	return headroomproc.Start(ctx, o)
}

// sidecarProc is the run's sidecar, nil when compression is off.
func (r *run) sidecarProc() SidecarProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sidecar
}

// stopCompression stops the run's sidecar, once, wherever the run ended: a
// sidecar left running would hold its port and its process group after the
// run's record was written. It mirrors closeGateway's own guard, and the
// same two call sites use it -- the ordinary path before finalize, and the
// deferred cleanup that also covers a panic.
func (r *run) stopCompression() {
	r.mu.Lock()
	p, stopped := r.sidecar, r.sidecarStopped
	r.sidecarStopped = true
	r.mu.Unlock()
	if p == nil || stopped {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sidecarStopTimeout)
	defer cancel()
	if err := p.Stop(ctx); err != nil {
		r.d.Log.Warn("stopping the Headroom sidecar failed", "err", r.redact(err.Error()))
	}
}

// watchSidecar fails the stage the sidecar dies under. A crash and a
// detected hang both close Dead(), and both are reported through the
// ordinary stage failure -- the reason is recorded here, and the stage is
// cancelled so a call waiting on a sidecar that will never answer ends now
// rather than at the stage's own deadline. Nothing about the run's status is
// assigned here (P10): the run ends failed with a draft pull request, as any
// other failed stage does.
func (r *run) watchSidecar(stageCtx context.Context, done <-chan struct{}, stage string, p SidecarProcess) {
	select {
	case <-p.Dead():
		r.fail(fmt.Sprintf("stage %s: the Headroom sidecar died or stopped responding", stage))
		r.stopStageForSidecar()
	case <-done:
	case <-stageCtx.Done():
	}
}

// stopStageForSidecar cancels the running stage because the compression
// sidecar died under it. The cause is neither a halt nor a cancel, so the
// run records no halt and no cancellation: the stage fails, with the reason
// watchSidecar already recorded.
func (r *run) stopStageForSidecar() {
	r.mu.Lock()
	cancel := r.haltStage
	r.mu.Unlock()
	if cancel != nil {
		cancel(errors.New("the Headroom sidecar died or stopped responding"))
	}
}
