package credsvc

import (
	"context"
	"net/http"
)

// The label route (D2439, F5, F18, F19, F26): the installation's orchestrator
// daemon asks this service to read and write the status and spec labels of the
// pull requests its runs opened, and the SERVICE performs the act itself — it
// mints its own short-lived token, which never leaves this process.
//
// That is the whole reason the route exists rather than a token being handed
// over: GitHub has no label-only permission, so any credential that could write
// a label could also approve, retitle or close any pull request of the
// repository (F5), and the daemon is the one caller this service already knows
// how to verify without a run identity.
//
// The caller class is the merge mint's (E16, D2049): the installation's
// orchestrator accounts, matched against a Google ID token minted for THIS
// SERVICE'S OWN URL and for nothing else, so the token a plain `gcloud auth
// print-identity-token` produces — and therefore the token a coding-agent
// session on the daemon's own machine produces — is refused before any list is
// consulted (F19, F26).
//
// The act behind the gate is T3's: the name check, the pull request's
// corroboration, the token's mint and the rate limit all belong to it, and this
// file holds the route, the gate and the refusal this build ships with.

// LabelPath is the path the daemon asks for a label read or a label write on.
// It is this service's own contract and not a forge path: the call names a
// repository, a pull request and a label, and the service is what reaches
// GitHub.
const LabelPath = "/v1/github/labels"

// labelRoute answers one label call. It is reached from ServeHTTP's branch
// ahead of RequestFromHTTP, and for the merge mint's reason: the daemon is not
// a run of this installation and holds no run identity, so the shared parse
// would refuse the call — for the missing identity headers — before the class
// ever read the token.
//
// The order below is the whole of the gate, and it is why it is where it is:
//
//   - the caller, first, and on its own token. This is the route's admission
//     and nothing else is read before it: a body cannot widen it, and a refusal
//     here is typed and has no line, because nothing is verified yet and there
//     is no caller for a line to name (the merge mint's class refusal, the read
//     route's person refusal and this service's rule that a line names a
//     VERIFIED caller all say the same thing).
//   - the run identity the class does not take, refused by name before anything
//     else is read: the daemon holds no run identity, so a call presenting one
//     is a call of a shape this route does not have.
//   - the call itself, which is T3's: every call the gate admits is refused
//     here until that handler exists, and the refusal is recorded, because the
//     caller IS verified and a refusal after verification writes exactly one
//     line.
func (s *Server) labelRoute(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	tok, err := bearerFromHTTP(r)
	if err != nil {
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	caller, err := s.cfg.Auth.VerifyOrchestrator(ctx, tok)
	if err != nil {
		s.refuseVerifier(w, err)
		return
	}
	if id := r.Header.Get(HeaderRunIdentity); id != "" {
		s.refuseLabel(ctx, w, caller, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it performs label calls for the orchestrator daemon, and a caller presenting a run is a call of a shape it does not have")
		return
	}
	if job := r.Header.Get(HeaderRunJob); job != "" {
		s.refuseLabel(ctx, w, caller, StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it performs label calls for the orchestrator daemon, and a caller naming a job is a call of a shape it does not have")
		return
	}
	s.refuseLabel(ctx, w, caller, StatusUnavailable, ErrorStateUnavailable,
		"this service does not perform label calls: the route is registered and its caller class is enforced, and the act itself is not built")
}

// refuseLabel answers one label refusal and records it, in the shape
// refuseMergeMint already has: one typed refusal to the caller, one line to the
// operator, and the caller's answer is the same whether or not the line lands
// (D143).
//
// It is reached only once the caller IS verified — the refusals above that
// point are answered by refuseVerifier alone, with no line, because an
// unverified caller has no name for one.
func (s *Server) refuseLabel(ctx context.Context, w http.ResponseWriter, caller string, status int, typ, message string) {
	if err := s.auditLabel(ctx, caller, typ); err != nil {
		s.log.Error("credsvc: a refused label call could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}

// auditLabel writes one label route's line: the daemon as its caller, the App
// key as its credential, and NEITHER a run nor a job — the caller is the
// installation's orchestrator daemon, which holds no run identity and is not a
// job of this installation. A refusal taken before the call was read has no
// repository to name, and the repository is the one thing a line for the act
// itself adds.
//
// It does not go through Identity.Audit because there is no Identity: the route
// is answered ahead of Verify, which is the only thing that builds one.
// purpose and credential come from routeAct, the ONE rule.
func (s *Server) auditLabel(ctx context.Context, caller, status string) error {
	purpose, credential := routeAct(LabelPath)
	return s.audit.Write(ctx, AuditEvent{
		Caller:     caller,
		Credential: credential,
		Purpose:    purpose,
		Status:     status,
	})
}
