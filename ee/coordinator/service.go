// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package coordinator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

// Service is the coordinator's resident service: the process the installation's
// front door runs (mcp-server plan task M4), and the surface every route of the
// front door is mounted on. The Terraform that creates it lives in
// deploy/terraform/gcp/modules/installation/coordinator.tf — one Cloud Run
// service, running as the front door's own account (`mcp_server`, which is
// deliberately not the coordinator's), admitted to by anyone
// (D920) — and this type is the Go half: what that revision answers with.
//
// It is deliberately thin. M4 creates the service and nothing that answers on
// it; M5 mounts the authorization server's two metadata documents and its four
// endpoints here, and M7 mounts the MCP endpoint, and neither needs this file
// to know anything about tokens, clients or tools. What it holds is the seam
// they mount on (Handle) and the host that serves it (Serve), because those are
// the two things that have to exist before a route can: a service whose routes
// are decided by the task that adds them cannot be built by the one that
// creates the service.
//
// THE TWO PROPERTIES THIS TYPE EXISTS TO KEEP.
//
//   - It holds no credential of any kind. spec.md §1 asks for "no second
//     identity to grant" and §9 states the prohibition the whole design rests
//     on: the coordinator never handles a model credential — not to a caller,
//     not in a log line, not in its own environment. The module's container
//     declares five credential-free names and no secret mount (held by
//     deploy/terraform/gcp/modules/installation/coordinator_service_test.go),
//     and RefuseModelCredential is the same rule at this layer: a revision
//     whose environment carries a model credential does not serve at all, which
//     is the fail-closed direction (an unreachable front door refuses a call,
//     while a front door holding a key is the one thing this design must not
//     ship).
//   - It is off until the owner turns it on. D934's switch is the module's
//     (var.mcp_server, null on every installation that has not asked), and
//     there is deliberately no second switch here: a process that declined to
//     serve on a variable of its own would be a front door whose state two
//     places disagree about, and the plan's operator note names `fugaro update`
//     as the one thing that turns it on. FrontDoor below is that rule as a
//     function: what the installation module renders onto the revision is what
//     the process mounts, and a revision with no entry serves exactly what this
//     service served before M12 — a mux with nothing on it.
type Service struct {
	mux *http.ServeMux
}

// NewService is the front door's HTTP surface with nothing on it yet.
func NewService() *Service { return &Service{mux: http.NewServeMux()} }

// Door is the server whose routes a front door that is ON serves: M5's
// authorization server, M7's resource server and M10's audit, as the one
// object that mounts them on this service.
//
// It is an interface and not *mcpauth.Server because this package is what
// mcpauth mounts ON: ee/coordinator/mcpauth imports ee/coordinator for exactly
// that, so naming it here would be the cycle that makes the front door
// unbuildable. What this type needs is the one method a door has, and M12 is
// the caller that passes the real one.
type Door interface {
	// Mount adds this door's routes to the service. It is srv.Mount in
	// mcpauth/server.go, and the pattern language underneath is net/http's
	// own mux (Handle's comment).
	Mount(*Service)
}

// FrontDoorSpec is what a front door is built out of: the installation's own
// answer to whether this revision runs a door, the build's own answer to
// whether it carries M12b's delivery, and the door itself.
//
// The three are one struct rather than three arguments because the switch has
// two settings and two facts behind it, and a caller that transposed two
// booleans would be a front door whose state nothing can read off the call
// site. Every field is named where the door is built, which is the one place
// the decision is made.
type FrontDoorSpec struct {
	// Enabled is D934's switch as this installation rendered it: true when the
	// module wrote the front door's own configuration onto the revision, and
	// that entry is written only under var.mcp_server.
	Enabled bool
	// Delivered is whether this binary carries M12b's delivery of a sign-in
	// flow to the paired phone. The wiring passes mcpauth.DeviceDeliveryBuilt
	// and never a variable of its own.
	Delivered bool
	// Door is the server whose routes the enabled front door serves. It is nil
	// exactly when the door is off.
	Door Door
}

// ErrDeviceDeliveryMissing refuses to enable a front door whose build carries
// no delivery of a sign-in flow to the paired device (the coordinator plan's
// D2126, answered 2026-11-11).
//
// It is a sentinel because the refusal is a fact about the installation rather
// than about a request, and an operator reads it once, at start-up: the door is
// served by a revision that never becomes ready, which is the fail-closed
// direction the process's other refusals take. What it is not is a door that
// serves and admits nobody: an enabled door without the delivery is a page a
// person is asked to approve a flow on with nothing to type the number into,
// and a half-built door on the public internet is the one thing D2126 forbids
// switching on.
var ErrDeviceDeliveryMissing = errors.New("the coordinator's front door cannot be enabled: this build carries no delivery of a sign-in flow to the paired device")

// FrontDoor returns the service a `fugaro coordinator serve` revision runs:
// the door's routes when the installation turned it on, and an empty mux when
// it did not.
//
// **The off direction is the whole of this function.** D934's answer is that
// an installation which never opted in acquires no front door, and that one
// which asked for a front door still gets it off until a person turns it on
// (the amended answer, of 2026-11-04: off is the absence of the allUsers
// binding, and the module writes that binding under the same var.mcp_server
// that puts this entry on the revision — one fact, two renderings, so the two
// cannot disagree). A service whose mux is empty answers 404 on every path of
// the front door, which is what this process served before M12 existed.
//
// The two refusals are about states that must not serve at all. A door that is
// enabled with nothing to mount is a wiring mistake — the routes would be
// missing while the revision looked healthy — and one enabled without M12b's
// delivery is the state D2126 forbids (ErrDeviceDeliveryMissing). Both are
// answered here rather than at the first request, because a front door that
// serves a 404 where its own metadata should be is a client that cannot tell a
// broken installation from one that never turned the door on.
func FrontDoor(spec FrontDoorSpec) (*Service, error) {
	if !spec.Enabled {
		if spec.Door != nil {
			return nil, errors.New("the coordinator's front door was handed a door and told it is off: the switch and the server have to agree about which of the two this revision is")
		}
		return NewService(), nil
	}
	if spec.Door == nil {
		return nil, errors.New("the coordinator's front door is enabled with no server to mount: the installation turned it on and the process built no door, which is a revision that would serve a 404 where its own metadata document belongs")
	}
	if !spec.Delivered {
		return nil, fmt.Errorf("%w (deploy/terraform/gcp/modules/installation/coordinator.tf, and the coordinator plan's D2126)", ErrDeviceDeliveryMissing)
	}
	svc := NewService()
	spec.Door.Mount(svc)
	return svc, nil
}

// Handle mounts a route. It is the seam M5's authorization server and M7's MCP
// endpoint are added through, and it is net/http's own mux underneath: the
// routes of this service are exact paths and one prefix (the MCP endpoint), so
// the pattern language the standard library already has is the whole of what is
// needed, and a second router would be a dependency for nothing.
func (s *Service) Handle(pattern string, h http.Handler) { s.mux.Handle(pattern, h) }

// Handler is the service's whole surface, for a caller that wants to drive it
// without a listener (the tests below, and internal/gcpfake-shaped fakes).
func (s *Service) Handler() http.Handler { return s.mux }

// RefuseModelCredential refuses to serve when this process's own environment
// carries a model credential's variable.
//
// It is spec.md §9's one prohibition, checked rather than assumed. The module
// mounts no secret on the front door's container and the plan-level test holds
// it to that, but the module is not the only thing that can put a variable in a
// revision's environment: a hand-edited Terraform, a second deployment, or an
// image whose own ENV names one would all reach this process, and the front
// door becomes the one place in the product where a model credential sits
// behind a public endpoint. So the process refuses to bind a port instead, and
// a revision that fails to start is what tells its owner (the same fail-closed
// direction the credentials service's entry point takes: D143, a service that
// answers every call with a refusal would look healthy).
//
// The predicate is internal/config's (IsModelSecretEnv: the Claude OAuth
// token's variable, the Anthropic key's, and any provider key's namespace), so
// this check and the rule that keeps those names off a job's mounts are one
// rule rather than two that can drift.
//
// The refusal names the variable and never its value: the environment is this
// process's own and its values are the secrets in question, so the message an
// operator reads in Cloud Logging has to be actionable without being a copy of
// what it is about.
func RefuseModelCredential(environ func() []string) error {
	for _, kv := range environ() {
		name, _, _ := strings.Cut(kv, "=")
		if config.IsModelSecretEnv(name) {
			return fmt.Errorf("the front door's environment carries %s, which is a model credential's variable, and the front door holds no model credential at all: it never passes one to a caller and nothing it does needs one (the run's own credential reaches the run the way every run's does, mounted into the job by the platform). The service was not started. Remove %s from the revision's environment", name, name)
		}
	}
	return nil
}

// The host's bounds. The front door is not the credentials service: it proxies
// nothing and streams no model completion, so the two bounds that matter there
// (a long-lived stream and a large body) are not the ones here. What it does
// answer is JSON-RPC over a public endpoint, where the one thing a caller must
// not be able to do is hold a connection and send nothing.
const (
	// serviceReadHeaderTimeout bounds a caller that connects and then says
	// nothing. Every request this service answers is a small JSON body whose
	// headers arrive at once.
	serviceReadHeaderTimeout = 30 * time.Second
	// serviceIdleTimeout is how long a kept-alive connection with no request on
	// it is held. An MCP client reuses one connection for a whole session of
	// tool calls, so it is generous rather than tight.
	serviceIdleTimeout = 2 * time.Minute
	// serviceMaxHeaderBytes bounds what a caller may send before the body. The
	// front door's own headers are a bearer token and a content type; this is
	// half Go's default because nothing here has a header near it.
	serviceMaxHeaderBytes = 1 << 19
	// serviceShutdownGrace is how long a SIGTERM lets the requests in flight
	// finish. Cloud Run sends SIGTERM and kills the container ten seconds later,
	// so this is under that: a tool call cut off mid-answer is a caller with a
	// result it cannot tell from a refusal, and one that ends inside the grace
	// is a caller that reads a whole answer.
	serviceShutdownGrace = 8 * time.Second
	// serviceDefaultPort is what PORT falls back to when it is unset. Cloud Run
	// always sets PORT; the fallback is for a developer running the binary by
	// hand, and it is the port the platform's own documentation uses.
	serviceDefaultPort = "8080"
)

// Serve runs the service on the port the platform gives it (PORT) until ctx is
// cancelled, then drains the requests in flight.
//
// The listener is bound here rather than by ListenAndServe so that the port is
// known to be this process's before anything is served: a revision whose PORT
// is taken has to fail at startup, not answer the platform's health check on a
// socket it does not own.
//
// getenv is a parameter rather than os.Getenv so the port and its refusals can
// be tested without a process environment (the credentials service's entry
// point takes it the same way).
func Serve(ctx context.Context, s *Service, getenv func(string) string, errw io.Writer) error {
	port, err := servicePort(getenv)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("the coordinator's front door cannot listen on PORT %s: %v", port, err)
	}
	return serveUntilStop(ctx, serviceHTTPServer(ctx, s.Handler(), errw), ln, errw)
}

// servicePort reads PORT -- the port Cloud Run gives the container, and the one
// the platform's health check and its front end reach this process on. A value
// that is not a port is a deployment that is wrong, so it is refused by name
// and never defaulted: a service listening somewhere the front end does not
// reach is one that looks started and answers nothing.
func servicePort(getenv func(string) string) (string, error) {
	raw := getenv("PORT")
	if raw == "" {
		return serviceDefaultPort, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("PORT is not a TCP port, and the front door was not started: Cloud Run gives the revision the port it must listen on, and a service listening elsewhere is one the platform's front end cannot reach")
	}
	// The number is read back out as the canonical port rather than passed on
	// as it arrived: Atoi accepts a leading sign, and ":+80" is an address
	// net.Listen refuses while "+80" is a port this function accepted, so
	// handing the raw string to the listener would turn an accepted value into
	// a bind failure further from the reason.
	return strconv.Itoa(n), nil
}

// serviceHTTPServer is the host's own shape, and a function of the handler
// rather than of the service so that a test can drive a real listener, request
// and shutdown through it without a service's worth of fakes
// (TestTheCoordinatorServiceServesAndStops).
//
// Server.ReadTimeout and Server.WriteTimeout stay zero on purpose: a tool call
// is answered in one JSON response, but `stream_logs` (M8) answers over the
// same connection, and net/http's deadline for a response is a deadline for the
// whole body -- a fixed one would cut a log stream a caller is still reading.
// What is bounded is what cannot be long-lived: the headers of a request, and a
// connection with nothing on it.
//
// ErrorLog is a logger over the writer the process was given, and it carries
// Go's own connection-level lines only (a client that hung up mid-request) and
// never a header or a body: the bearer token of an MCP caller is a header, and
// this process is the one place it must not be printed from.
func serviceHTTPServer(ctx context.Context, h http.Handler, errw io.Writer) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: serviceReadHeaderTimeout,
		IdleTimeout:       serviceIdleTimeout,
		MaxHeaderBytes:    serviceMaxHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          slog.NewLogLogger(slog.NewTextHandler(errw, nil), slog.LevelWarn),
	}
}

// serveUntilStop serves ln until ctx is cancelled, then drains the requests in
// flight.
//
// The cancellation is the platform's SIGTERM (the command's own context) or a
// test's, and the drain is the whole point of it: a tool call can be answering
// when the revision is replaced, and a process that returned at once would cut
// it. What it does not do is report a drain that ran out of time as a failure:
// the platform kills the container seconds after the signal whatever this says,
// the caller reads its own refusal from the closed connection, and a non-zero
// exit from a revision that is being replaced anyway is a log line saying a
// healthy shutdown failed. A listener that stopped for a reason of its own is
// not that: it is reported whether it failed before the cancellation or in the
// same instant as it.
func serveUntilStop(ctx context.Context, srv *http.Server, ln net.Listener, errw io.Writer) error {
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	fmt.Fprintf(errw, "coordinator front door: listening on %s\n", ln.Addr())
	select {
	case err := <-done:
		// Serve returned on its own: the listener failed or was closed under
		// it. Either way the service is not serving, and the process must not
		// pretend otherwise.
		return serveResult(err)
	case <-ctx.Done():
	}
	return drainAndReport(srv, done, errw)
}

// drainAndReport drains the requests in flight and then reports what Serve
// returned. It is the second half of serveUntilStop and a function of its own
// because the two halves answer different questions, and because this one is
// where a failure that raced the cancellation is still read: a test drives it
// with the failure already in the channel rather than hoping for the coin flip
// that puts it there.
func drainAndReport(srv *http.Server, done <-chan error, errw io.Writer) error {
	shutCtx, cancel := context.WithTimeout(context.Background(), serviceShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		fmt.Fprintf(errw, "coordinator front door: shutdown did not drain every request in %s: %v\n", serviceShutdownGrace, err)
	}
	// Serve's own result is read here whatever the drain did, because the
	// select above picks between two ready cases at random: a listener that
	// failed at the same instant the signal arrived would leave its failure in
	// the channel, and the host would report a shutdown that never happened.
	// Shutdown closes this listener before it drains, so Serve has returned by
	// now and this receive cannot block (a listener that failed first has
	// untracked itself already, and Serve returned then too).
	return serveResult(<-done)
}

// serveResult is what Serve's return value means to this process:
// http.ErrServerClosed is the shutdown itself and is not a failure, and every
// other error is one. Both reads above ask the same question, and a second copy
// of the rule is a second place for the two answers to differ.
func serveResult(err error) error {
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("the coordinator's front door stopped: %w", err)
	}
	return nil
}
