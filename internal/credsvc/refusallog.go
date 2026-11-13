package credsvc

// refusallog.go is the one line a refusal leaves for an operator besides its
// audit line.
//
// The run is answered a typed refusal, and the gateway turns that body into one
// generic sentence for the agent, so nothing the run writes says which of this
// service's reads failed or with what. What that costs was seen twice: on
// 2026-11-12 and again on 2026-11-13 the owner's run had its first /v1/messages
// forward answered 503 state_unavailable by a warm service, and the service's
// log held nothing but the HTTP line and an audit line carrying no credential
// and no tier — which says only that the refusal happened BEFORE a credential
// was chosen, and leaves the policy read, the kill switches, the bundle load,
// the candidates and admissions and the selector's own reads equally likely. A
// failure an operator cannot diagnose is a product defect, so the refusals of a
// model call whose run identity this service verified write this line: the
// refusal's type and status, the run and the repository it was about, the same
// class-only sentence the run is sent, and the two facts only the service has —
// which read failed, and the Go type of the error it failed with. Where the
// refusal is not one read's failure but a step's — the candidate list's own
// build — the sentence itself names what failed and the two fields are empty
// (the label block below).
//
// A refusal taken BEFORE the identity is verified is the exception, and it is
// the one refusal that cannot be written here at all: there is no verified run
// to name and no identity to key an audit by (refuseIdentity, server.go), so
// neither line is written and the runbook's §3 sends the operator to §1 for
// that shape.
//
// It is written BESIDE the audit line and never instead of one: the audit is
// the record of what the service did with a credential (C1.13), this is the
// diagnosis of what went wrong, and the two are separate acts with separate
// messages, so an operator greps `credsvc: audit` for the acts and
// `credsvc: refused` for the refusals.
//
// What the line may carry is bounded by two rules, both the audit file's:
//
//   - the error's TEXT is never written. An RTDB error embeds the path it read
//     (internal/rtdb), and a read's error can embed the value it was reading
//     (dynamicsecrets.go's read), so the cause is carried as its Go type
//     (error_class, a %T and nothing else) and as the label of the read that
//     produced it. The message field is not the error's: it is the sentence the
//     run is already sent, which every refusal on this path builds as a class.
//   - no credential, token, bundle value or request body is written. There is
//     no field one could be put in — the message is the run's own sentence, the
//     read is a label this file owns, and the class is a type name — and the
//     message and the two identity fields are put through internal/safetext, so
//     a sentence or a name carrying an escape sequence reaches no log viewer as
//     one.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// refusedMessage is the message of the one line every refusal writes. It is
// this file's own text and greppable, like the audit file's two.
const refusedMessage = "credsvc: refused"

// The reads a refused forward call can fail on, as the labels its line carries.
//
// They are one closed set of this file's own text, so the label an operator
// greps is never another package's spelling of its own read: a read that failed
// is named here and nowhere else. Every read of the service's OWN that a
// refused model call can fail on is one of them, with two exceptions, and both
// are refusals whose own sentence already names what went wrong rather than a
// read this file would have to name: the caller's request body is not a read of
// anything this service holds (readCall's failure is refused with the caller's
// own sentence), and the candidate list is a step and not a read of one node
// (candidates: the crafts that narrow it and the admissions that fill it), so a
// call the candidate list refuses is written with an empty read and an empty
// class beside its sentence. The selector's three
// are separate on purpose: the marks, the leases and the run's own placement
// are three nodes and three different questions, and "the selection's read
// failed" would leave an operator to guess which of them to look at.
const (
	// readKills is the operator's two stop switches (budget.ReadKills).
	readKills = "kills"
	// readPolicyShared is the project's published shared node (C1.2).
	readPolicyShared = "policy_shared"
	// readPolicyPrices is the project's model prices, read out of the shared
	// node into the table the request check bounds a call by (requestPriceTable,
	// request.go): the one read of the policy's own block that is not the
	// allow-list, and a build of the table this service cannot answer a call
	// without.
	readPolicyPrices = "policy_prices"
	// readSlip is the slip store (creds.SlipStore): the record a slip-carrying
	// call is admitted by, read when this service's in-memory copy does not hold
	// it.
	readSlip = "slip_read"
	// readBundle is the one bundle secret (C1.11's loader).
	readBundle = "bundle"
	// readSelectorMarks, readSelectorLeases and readSelectorCurrent are the
	// three reads creds.Select makes of the cascade's shared state before it
	// decides anything: every credential's mark, every lease (the cap), and the
	// credential this run is on (D154's stickiness).
	readSelectorMarks   = "selector_marks"
	readSelectorLeases  = "selector_leases"
	readSelectorCurrent = "selector_current"
	// stateWrite is the state a selection WRITES rather than reads: the mark a
	// 401 or a usage limit leaves, the run's own placement, and the round-robin
	// counter a rotation advances. It is labelled for the reason the three reads
	// are — the store's failures are one class of text — and it is named
	// separately because a write that failed is not a read that failed: an
	// operator who greps `read=selector_marks` must not be handed a call whose
	// mark could not be written.
	stateWrite = "state_write"
	// readSecret is the per-secret container read that resolves a candidate's
	// value ahead of the bundle's entry (R6's first half).
	readSecret = "secret_read"
	// readUpstream is the call to the upstream itself (forward.go's send).
	readUpstream = "upstream_send"
)

// maxErrorDepth bounds errorClass's walk down the wrap chain. A chain that deep
// is not one this service builds, and a log line is not worth a hung service if
// some error ever unwraps to itself.
const maxErrorDepth = 16

// readFailure is an error labelled with the read that produced it. The label
// travels ON the error rather than beside it because the code that fails a read
// is the code that knows which read it was, and the code that refuses the call
// (server.go's cascade step, cascade.go's refuseSelection) is a different one:
// a caller that took the error would otherwise have to guess the label from
// where it caught it, and a guess is exactly what an operator cannot check.
//
// The wrapper changes no error's text, and Unwrap keeps errors.Is and errors.As
// working through it, so labelling a failure is invisible to every reader but
// this file's.
type readFailure struct {
	read string
	err  error
}

func (e *readFailure) Error() string { return e.err.Error() }
func (e *readFailure) Unwrap() error { return e.err }

// labelRead wraps err with the read it failed on, and answers a nil err as nil:
// a read that succeeded is not a cause, and a refusal that carries one would
// name a read nothing was wrong with.
func labelRead(read string, err error) error {
	if err == nil {
		return nil
	}
	return &readFailure{read: read, err: err}
}

// readOf is the label err carries, or "" for an error no read labelled. The
// INNERMOST label wins, because the more specific read is the one an operator
// acts on: an error a read labels and a caller labels again is about the read
// underneath, and a failure wrapped in a sentence of this service's own is
// still that read's failure.
func readOf(err error) string {
	for err != nil {
		var rf *readFailure
		if !errors.As(err, &rf) {
			return ""
		}
		if inner := readOf(rf.err); inner != "" {
			return inner
		}
		return rf.read
	}
	return ""
}

// errorClass is the Go type of err's root cause — the underlying error, and
// never its text (the rule at the top of this file): an error this service
// wraps twice carries the class of the failure underneath it, which is what
// tells an RTDB read (*rtdb.Error) from a decode, a transport or a sentinel of
// this package's own.
func errorClass(err error) string {
	if err == nil {
		return ""
	}
	for i := 0; i < maxErrorDepth; i++ {
		next := errors.Unwrap(err)
		if next == nil || next == err {
			break
		}
		err = next
	}
	return fmt.Sprintf("%T", err)
}

// stateReads is the cascade's shared state as the SELECTOR of one call reads it,
// with each of the three reads it makes labelled (readSelector*). The store's
// own error cannot say which of them failed — creds' reader wraps Marks and
// Leases in the same class of failure, and a selection that could not read the
// leases is a different incident from one that could not read the run's own
// placement — so the label is put on the error here, where the read happens.
//
// It is built once, in NewServer, and holds nothing of its own: the label
// travels on the error the read answered, so several calls selecting at the
// same moment each carry their own and no refusal can be answered with the read
// some OTHER call failed.
//
// The three writes are labelled too (stateWrite), and not because the line
// names them as reads: their text is the store's, exactly as the reads' is, and
// a refusal that repeats the store's own words is the one thing this file
// exists to keep out of the log (see refuseSelection).
type stateReads struct {
	creds.Store
}

func (r *stateReads) Marks(ctx context.Context) (map[string]creds.Mark, error) {
	m, err := r.Store.Marks(ctx)
	return m, labelRead(readSelectorMarks, err)
}

func (r *stateReads) Leases(ctx context.Context, now time.Time) ([]creds.Lease, error) {
	l, err := r.Store.Leases(ctx, now)
	return l, labelRead(readSelectorLeases, err)
}

func (r *stateReads) Current(ctx context.Context, run creds.Run) (string, bool, error) {
	name, found, err := r.Store.Current(ctx, run)
	return name, found, labelRead(readSelectorCurrent, err)
}

func (r *stateReads) UpdateMark(ctx context.Context, name string, f func(creds.Mark, bool) (creds.Mark, error)) (creds.Mark, error) {
	m, err := r.Store.UpdateMark(ctx, name, f)
	return m, labelRead(stateWrite, err)
}

func (r *stateReads) SetPlacement(ctx context.Context, run creds.Run, name string, l creds.Lease) error {
	return labelRead(stateWrite, r.Store.SetPlacement(ctx, run, name, l))
}

func (r *stateReads) RoundRobin(ctx context.Context) (int64, error) {
	n, err := r.Store.RoundRobin(ctx)
	return n, labelRead(stateWrite, err)
}

// logRefusal writes the one server-side line a refusal leaves.
//
// cause is the error the refusal was taken on, or nil for a refusal that is not
// a failure of anything — the operator's stop switch being ON, a request this
// service's own rules refuse, an answer whose headers cannot be built. A nil
// cause writes an empty read and an empty error_class rather than omitting the
// fields, so the line's shape is one shape and a reader does not have to tell
// "not known" from "not there" (the rule the audit line keeps).
func (s *Server) logRefusal(ctx context.Context, id Identity, typ string, status int, message string, cause error) {
	s.log.WarnContext(ctx, refusedMessage,
		slog.String("type", safetext.OneLine(typ)),
		slog.Int("status", status),
		slog.String("repository", safetext.OneLine(id.Slug)),
		slog.String("run", safetext.OneLine(id.Run)),
		slog.String("message", safetext.OneLine(message)),
		slog.String("read", readOf(cause)),
		slog.String("error_class", errorClass(cause)),
	)
}
