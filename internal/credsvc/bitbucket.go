package credsvc

// The Bitbucket read mint (BB4, B1/B2): a PERSON asks this service for a
// read-scoped Bitbucket token, and the service mints it from the read consumer
// its bundle holds. It is the first route of this service whose caller is not a
// run of the installation, and the second whose caller holds no job at all
// (C3.9f's verify route is the first).
//
// Four things are the whole of the route's design, and each is why the others
// are what they are:
//
//   - The caller is a person, and the class is C3.9f's mechanism rather than a
//     second way to be one (BB4, B2): VerifyPerson is the same token check as
//     the verifier class with a different list, and the route is answered ahead
//     of RequestFromHTTP for the same reason the verify route is — a person
//     holds no run identity, and the shared parse refuses a call that has none.
//     A caller that presents one anyway is refused by name: the class takes no
//     run identity, so a whole identity beside the token is a call of a shape
//     this route does not have.
//   - The token is minted from the READ consumer and from nothing else
//     (BitbucketReadConsumerSecret), through bitbucket.MintAccessToken's
//     client_credentials exchange. The consumer's key and secret never leave
//     this service (G5), the refresh token a grant may carry is never read
//     (oauth.go) and the answer carries no consumer field at all.
//   - The route names ONE repository, and the service resolves its workspace
//     from the installation's own inventory and refuses a repository of another
//     workspace BY NAME (G11). What it refuses is a token for a repository the
//     installation does not hold, or for one outside the read consumer's
//     workspace — never a token whose reach is narrower: an access token minted
//     this way reaches the whole workspace, and the mint scopes WHO RECEIVES a
//     token, never what it reaches (U3).
//   - The exchange goes to bitbucket.org and to nothing else (T2.5, G10): the
//     root is the constant in internal/gitprov/bitbucket, and no environment
//     value and no field of the call can move it. A consumer's key and secret
//     are presented to that host alone, so a root a caller could name would be
//     the whole of the consumer's custody handed over.
//
// The route reads no policy and consults no cascade: a Bitbucket access token
// is not a model credential, and no tier, admission or budget has anything to
// say about it. Its whole admission is the class, the one repository it names
// and the operator's stop flag.
//
// Every act of it is audited once through C1.13's auditor (audit.go,
// PurposeBitbucketRead, no tier): the line names the person who asked and the
// repository the token was for, and it carries no run and no job, because the
// caller is neither (callerWithoutJobPurpose, repositoryOnlyPurpose). A mint
// whose line cannot be written hands the token to nobody (D143).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/task"
)

// BitbucketReadPath is the path a person asks for a read-scoped Bitbucket token
// on. It is not an Anthropic path: the request and the answer are this service's
// own contract, and it is served ahead of the shared request parse (ServeHTTP)
// because its caller holds no run identity.
//
// bitbucket.ServiceReadPath is the same path spelled by the client that calls
// it, and TestNewRoutesAuditConsumerKind holds the two to each other, so a
// rename of either cannot quietly move the route.
const BitbucketReadPath = "/v1/bitbucket/read-token"

// BitbucketReadRequest is a read mint's body: the one repository the token is
// asked for, in the installation's own spelling ("workspace/name").
//
// It is the whole of the request and it is one field on purpose. There is no
// token-URL field, no scope field and no workspace field: the endpoint the
// consumer is presented to is this service's constant (G10), the scopes are the
// consumer's own, and the workspace is resolved from the installation's
// inventory rather than taken from the caller (G11). A body that carries any of
// them is refused by the strict decoder rather than read for the fields it does
// know (Convention 6), which is what makes "no caller can move the token
// endpoint" a property of the route and not a check inside it.
type BitbucketReadRequest struct {
	Repository string `json:"repository"`
}

// BitbucketReadResponse is the read mint's answer: the token, when it expires,
// and the repository it was minted for.
//
// Repository is the repository the CALLER named, in the same "workspace/name"
// spelling the request carried — not the canonical name a mint's answer carries
// (MintResponse.Repository). The difference is deliberate: there the caller
// named nothing and the answer is where it learns what the token is for, while
// here the caller named the repository and the answer's field is what the client
// checks its own request against before it uses the token. A caller that asked
// for one repository and was answered about another has been handed a token it
// must not use.
//
// Token is the credential's value and the reason this endpoint exists. Nothing
// in this package logs it, and a caller must not either.
type BitbucketReadResponse struct {
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	Repository string    `json:"repository"`
}

// The read mint's own refusals. They are sentinels so a test and a reader can
// tell them apart, and every one is a sentence written here: neither
// bitbucket.org's answer nor the exchange's own error text reaches a caller,
// because the request that failed carried the consumer's secret.
var (
	// ErrReadBadRequest is a call whose body is not this contract's: not one
	// JSON object of one field, or a repository that is not "workspace/name".
	ErrReadBadRequest = errors.New("credsvc: the read mint call is not one this service reads")
	// ErrReadUnknownRepository is a repository this installation's inventory
	// does not hold. It is answered by name and never guessed at: the token
	// this route would mint reaches a whole workspace, so the list of
	// repositories that may be asked about is the installation's own.
	ErrReadUnknownRepository = errors.New("credsvc: the repository is not one of this installation's")
	// ErrReadOtherWorkspace is a repository of this installation that is not in
	// the read consumer's workspace (G11). It is the one refusal of this route
	// that is about the consumer's reach rather than about the call.
	ErrReadOtherWorkspace = errors.New("credsvc: the repository is not in the Bitbucket workspace this service's read consumer is scoped to")
	// ErrReadNoConsumer is a bundle that holds no read consumer. It is the
	// fail-closed case BB2's release note describes: an installation that has
	// not run `fugaro creds set bitbucket-read-consumer` has no read mint, and
	// the person class is honoured only while the bundle holds one. A fallback
	// to any other entry would be a token minted from a credential the operator
	// never chose.
	ErrReadNoConsumer = errors.New("credsvc: the credentials bundle holds no Bitbucket read consumer")
	// ErrReadWriteScopes is a token the endpoint granted write scopes to. It is
	// refused rather than handed on (U11): the read mint exists to hand out a
	// credential that cannot write, and a consumer the installing person
	// created with write scopes — or a workspace whose grant was widened under
	// it — would otherwise make this route the very thing it is written to
	// avoid. The refusal names the scope word and never a credential.
	ErrReadWriteScopes = errors.New("credsvc: the Bitbucket token minted carries a write scope")
	// ErrReadBitbucket is the exchange failing or bitbucket.org refusing it.
	ErrReadBitbucket = errors.New("credsvc: bitbucket.org did not mint a read token")
	// ErrReadUnaudited is a token bitbucket.org granted and C1.13's auditor
	// could not record. The token exists at bitbucket.org and reaches nobody:
	// this service will not hand out a credential it could not write a line for
	// (D143).
	ErrReadUnaudited = errors.New("credsvc: the read mint could not be audited, so no Bitbucket token was issued")
)

// readScopeRE is the whole shape of a scope word this service will name back.
// bitbucket.org writes scopes as one string of lowercase words, each optionally
// suffixed with ":write", ":admin", ":delete" or another verb. A word that is
// anything else is text this exchange did not write — the answer is another
// endpoint's bytes — so it is dropped with the rest of the body rather than
// repeated into a refusal, which is the rule oauthErrorCode already keeps for
// the exchange's error field.
var readScopeRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}(:[a-z][a-z0-9_]{0,31})?$`)

// writeScopeOf returns the first scope in scopes that grants more than a read,
// or "" when every one is a read.
//
// A scope is a write one when it is QUALIFIED and its verb is not "read":
// bitbucket.org's read scopes are the bare nouns ("repository", "pullrequest",
// "account") and, for a grant that spells it out, the noun with ":read";
// everything else it documents — ":write", ":admin", ":delete" — reaches
// beyond a read. An empty list is not a write scope and is not treated as one:
// oauth.go's Minted.Scopes documents that an answer which listed none is the
// "the endpoint did not say" case, and refusing every token from an endpoint
// that says less than this code hopes would break the mint rather than tighten
// it. What stands against that case is the consumer's own scope, which the
// operator sets when they create it (BB2's release note) and which Check 145
// (U11) reads on the real endpoint.
func writeScopeOf(scopes []string) string {
	for _, s := range scopes {
		verb, qualified := "", false
		if i := strings.LastIndexByte(s, ':'); i >= 0 {
			verb, qualified = s[i+1:], true
		}
		if qualified && verb != "read" && readScopeRE.MatchString(s) {
			return s
		}
	}
	return ""
}

// readRefusalMessage is a read refusal's message with the call's own repository
// attached, and the text is attached only while it is one this contract may
// repeat.
//
// req.Repository is the caller's own text: readReadRequest holds it to
// gitprov.SplitRepo, which is deliberately permissive about what a repository
// path may contain (it accepts control characters, escape sequences and ".."),
// so echoing it unconditionally would put whatever a caller sent into an
// answer, the operator's log and the audit line beside it — a newline forges a
// line in a text log, an escape sequence rewrites the terminal an operator
// reads the refusal in, and a relative path names something this service never
// looked at. task.CanonicalRepo is this program's own shape rule for a
// repository name (ASCII owner/name, no "." or ".." segment) and the echo is
// held to it: a text that is not that shape is refused all the same, and the
// refusal names the field rather than the value, which is the package's no-echo
// rule for a caller's own claim.
func readRefusalMessage(err error, repository string) string {
	if _, cerr := task.CanonicalRepo(repository); cerr != nil {
		return err.Error() + ": the call's repository is not a name this service reads"
	}
	return err.Error() + ": " + repository
}

// readReadRequest reads a read mint's body out of the call the handler already
// read (readCall), which is where the person's own ID token was registered with
// the service's redaction for the length of this call.
//
// The bound is the mint's 4 KiB and not the gateway's: the whole request is one
// short field, so a body larger than this is not a read mint, and a body that is
// one never approaches it. The decode is strict, as every body of this contract
// is, which is what makes a request that tries to name the token endpoint, the
// scopes or the workspace a refusal rather than a request whose extra field was
// quietly dropped.
func readReadRequest(body []byte) (BitbucketReadRequest, error) {
	if len(body) > maxMintRequestBytes {
		return BitbucketReadRequest{}, fmt.Errorf("the body is larger than the %d bytes this service accepts", maxMintRequestBytes)
	}
	var req BitbucketReadRequest
	if err := decodeStrict(body, &req); err != nil {
		return BitbucketReadRequest{}, errors.New("the body is not one JSON object of one field")
	}
	if _, _, ok := gitprov.SplitRepo(req.Repository); !ok {
		return BitbucketReadRequest{}, errors.New("the call names no repository (workspace/name)")
	}
	return req, nil
}

// workspaceOf resolves the workspace of one repository from the installation's
// inventory: the slug the repository's runs are keyed by (task.Slug, the one
// derivation the launcher, the runner and this service share), the canonical
// name the inventory holds for it, and the workspace that name is under.
//
// It answers ErrReadUnknownRepository for a repository no row holds, and it
// refuses a row whose value is not a Bitbucket canonical name: a table entry
// this code cannot read is one it must not guess a workspace out of, because
// the workspace is what the mint is then checked against.
func workspaceOf(repos map[string]string, repo string) (slug, workspace string, err error) {
	slug, err = task.Slug(gitprov.KindBitbucket, repo)
	if err != nil {
		return "", "", ErrReadUnknownRepository
	}
	canonical, ok := repos[slug]
	if !ok {
		return "", "", ErrReadUnknownRepository
	}
	provider, rest, ok := strings.Cut(canonical, "/")
	if !ok || provider != gitprov.KindBitbucket {
		return "", "", ErrReadUnknownRepository
	}
	workspace, name, ok := strings.Cut(rest, "/")
	if !ok || workspace == "" || name == "" || strings.Contains(name, "/") {
		return "", "", ErrReadUnknownRepository
	}
	return slug, workspace, nil
}

// bitbucketRead answers one read mint call. It is reached from ServeHTTP's
// branch ahead of RequestFromHTTP, and it does there what every other route does
// here: it verifies its caller, reads its call, resolves what the call names,
// reads the credential it mints from, mints, and records the act.
//
// The order below is why each step is where it is:
//
//   - the caller, first, and on its own token: this is the route's whole
//     admission, and it is the reason the route is where it is in ServeHTTP. A
//     refusal here is typed and has no line — nothing is verified yet, so there
//     is no caller for a line to name.
//   - the run identity the class does not take, before anything else is read: a
//     person who presents one is refused as a shape of call this route does not
//     have, which is what keeps the class from being a second way in for a run.
//   - the call's body, whose read is ALSO the registration of the person's own
//     token with the call-scoped scrub. readCall builds that list, and the
//     shared parse that builds it for every other route never runs for this one.
//   - the operator's stop flag, read BEFORE the bundle, so a stopped service
//     reads no credential at all. It is the global switch alone, with no slug
//     (F34): this route has no run, and a repository's own switch is a fact
//     about that repository's runs rather than about a person's read.
//   - the bundle and the read consumer, failing closed. An absent consumer is a
//     refusal and never a fallback to another entry (D143).
//   - the repository, resolved through the installation's inventory and held to
//     the consumer's workspace by name (G11).
//   - the exchange, against the constant root and no other (G10).
//   - the answer, built, audited and handed over in that order (answerRead): an
//     answer the service cannot build is a refusal that writes its own line and
//     no issuance line at all, and an issuance whose line cannot be written
//     reaches nobody (C1.13, D143).
func (s *Server) bitbucketRead(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	purpose, credential := routeAct(r.URL.Path)
	tok, err := bearerFromHTTP(r)
	if err != nil {
		// No line: the call is refused before its caller was verified, which is
		// the one refusal on this route with nothing to name.
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	caller, err := s.cfg.Auth.VerifyPerson(ctx, tok)
	if err != nil {
		// The class's own refusal, answered the way the verify route answers
		// the verifier class's: a typed refusal and no line, because a line's
		// "who" is what this service verified and there is none yet.
		s.refuseVerifier(w, err)
		return
	}
	if id := r.Header.Get(HeaderRunIdentity); id != "" {
		s.refuseRead(ctx, w, caller, "", StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it mints for a person, and a caller presenting a run is a call of a shape it does not have")
		return
	}
	if job := r.Header.Get(HeaderRunJob); job != "" {
		s.refuseRead(ctx, w, caller, "", StatusUnverified, ErrorUnverifiedRunIdentity,
			"this route takes no run identity: it mints for a person, and a caller naming a job is a call of a shape it does not have")
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		// A body this service will not hold is a refused REQUEST and not an
		// unavailable service, which is the reading the verify route gives the
		// same error. The read is also what registers the person's own token
		// with the call's scrub, so nothing below this line can echo it.
		s.refuseRead(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	req, err := readReadRequest(cl.body)
	if err != nil {
		// The refusal names the field and never what the call sent, and it is
		// answered before the bundle is read: a request this contract refuses
		// reads no credential at all.
		s.refuseRead(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	on, which, err := s.cfg.Kills.Stop(ctx, "")
	if err != nil {
		// An unreadable switch is not an off switch (budget's own rule), so the
		// call is refused rather than served.
		s.refuseRead(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable, "the operator's stop switches could not be read")
		return
	}
	if on {
		s.refuseRead(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable,
			"the credentials service is stopped by the "+which+" kill switch")
		return
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseRead(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	key, secret, consumerWorkspace, ok := b.BitbucketReadConsumer()
	if !ok {
		s.refuseRead(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable, ErrReadNoConsumer.Error())
		return
	}
	slug, workspace, err := workspaceOf(s.cfg.Repos, req.Repository)
	if err != nil {
		// The refusal names the repository, which is the caller's own text and
		// not a credential: an operator reading it has to know which repository
		// was asked about — and only while that text is a repository NAME, so
		// nothing a caller sent that is not one is repeated (readRefusalMessage).
		s.refuseRead(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused,
			readRefusalMessage(err, req.Repository))
		return
	}
	if workspace != consumerWorkspace {
		// G11's refusal, BY NAME: the repository is named and the consumer's
		// workspace is not, which is all a caller needs to act on and one fact
		// less about the installation than it already had.
		s.refuseRead(ctx, w, caller, slug, StatusRequestRefused, ErrorRequestRefused,
			readRefusalMessage(ErrReadOtherWorkspace, req.Repository))
		return
	}
	minted, err := bitbucket.MintAccessToken(ctx, bitbucket.Consumer{Key: key, Secret: secret}, bitbucket.DefaultOAuthRoot, s.cfg.Client)
	if err != nil {
		// The operator gets the class of the failure and, when the endpoint
		// answered, its status -- and never a byte of its text: the request that
		// failed carried the consumer's secret, so an endpoint that echoed the
		// request would be putting a credential in this service's log. The
		// exchange's own refusal already keeps that rule (oauth.go's refusal).
		attrs := []any{"purpose", purpose, "credential", credential, "repository", slug}
		s.log.Warn("credsvc: the Bitbucket read token could not be minted", append(attrs, "error", err.Error())...)
		s.refuseRead(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable, ErrReadBitbucket.Error())
		return
	}
	if scope := writeScopeOf(minted.Scopes); scope != "" {
		// U11's guard, and it is the one refusal of this route that is about
		// what BUCKETBORG granted rather than about what was asked for: the
		// endpoint's own list says the token can write, so it is not a read
		// token and this route does not hand it on.
		s.log.Warn("credsvc: the Bitbucket token minted for a read carries a write scope",
			"purpose", purpose, "credential", credential, "repository", slug, "scope", scope)
		s.refuseRead(ctx, w, caller, slug, StatusUnavailable, ErrorStateUnavailable,
			fmt.Sprintf("%s (%s): recreate the read consumer with read scopes only, and run `fugaro creds set bitbucket-read-consumer` again", ErrReadWriteScopes.Error(), scope))
		return
	}
	s.answerRead(ctx, w, caller, slug, BitbucketReadResponse{Token: minted.Token, ExpiresAt: minted.ExpiresAt, Repository: req.Repository})
}

// answerRead builds the read mint's answer, audits the issuance and writes it.
//
// The order is the point of this function being one, and it is answerMint's:
//
//   - the answer is built FIRST, and a body the service cannot build is a
//     refusal answered through refuseRead — which writes the refusal's OWN line
//     — with no issuance line written at all. The issuance's line says a token
//     was issued, so a line written for an answer that carries no token is a
//     mint an operator reads as served. Building the body is pure (json.Marshal
//     over three fields) and touches no credential, so nothing is spent by
//     doing it before the line;
//   - the audit line is written BEFORE the token is handed over, and a line
//     that cannot be written is a refusal (C1.13, D143: an issuance that cannot
//     be audited is not made). A Bitbucket access token cannot be unminted, so
//     what "not made" means here is that it reaches nobody: no answer carries
//     it, no log line and no error does either.
func (s *Server) answerRead(ctx context.Context, w http.ResponseWriter, caller, repository string, ans BitbucketReadResponse) {
	body, err := json.Marshal(ans)
	if err != nil {
		// Unreachable: the answer is three fields, two of them strings and the
		// third a time encoding/json writes. It is kept rather than dropped
		// because the alternative to answering is a panic inside the one act
		// that hands out a credential.
		s.log.Error("credsvc: the read mint's answer could not be built", "error", err)
		s.refuseRead(ctx, w, caller, repository, StatusUnavailable, ErrorStateUnavailable, readAnswerRefusal)
		return
	}
	if err := s.auditRead(ctx, caller, repository, StatusOK); err != nil {
		s.log.Error("credsvc: the read mint's audit line was not written", "error", err)
		s.refuseRead(ctx, w, caller, repository, StatusUnavailable, ErrorStateUnavailable, ErrReadUnaudited.Error())
		return
	}
	writeReadAnswer(w, body)
}

// readAnswerRefusal is what a read mint whose own answer cannot be built is
// refused with: this file's own sentence, and never a word of json's error,
// which names the byte it choked on and can quote the value it was handed.
const readAnswerRefusal = "the service could not build the read mint's answer"

// auditRead writes one read mint's line: the person as its caller, the
// repository the token was for, the read consumer as its credential, and
// NEITHER a run nor a job — the caller is a person, who holds no run identity
// and is not a job of this installation.
//
// It does not go through Identity.Audit because there is no Identity: the read
// path is answered ahead of Verify, which is the only thing that builds one.
// The repository is the installation's SLUG for it and not the "workspace/name"
// the caller typed, so the line is keyed the way every other line of this log is
// and an operator greps one spelling. purpose and credential come from routeAct,
// the ONE rule, so this line and the refusals ServeHTTP and this file answer
// cannot name the act or the credential two ways.
func (s *Server) auditRead(ctx context.Context, caller, repository, status string) error {
	purpose, credential := routeAct(BitbucketReadPath)
	return s.audit.Write(ctx, AuditEvent{
		Caller:     caller,
		Repository: repository,
		Credential: credential,
		Purpose:    purpose,
		Status:     status,
	})
}

// refuseRead answers one read-mint refusal and records it, in the shape
// refuseVerify already has for the routes whose caller holds no Identity: one
// typed refusal to the caller, one line to the operator, and the caller's answer
// is the same whether or not the line lands (D143).
//
// It is reached only once the caller IS verified — the refusals above that point
// are answered by refuse alone, with no line, exactly as the verify route's are.
// An empty repository is a refusal taken before the repository was resolved: the
// run-identity and body refusals, the stop switch and the bundle are all
// answered before anything named one.
func (s *Server) refuseRead(ctx context.Context, w http.ResponseWriter, caller, repository string, status int, typ, message string) {
	if err := s.auditRead(ctx, caller, repository, typ); err != nil {
		// The service's own log line is where an operator reads an act it could
		// not record, for the reason refuseCall gives: the answer to the caller
		// is a refusal either way.
		s.log.Error("credsvc: a refused read mint could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}

// writeReadAnswer writes the read mint's answer, from the body answerRead
// already built and audited. It is no-store: the body carries a credential's
// value, and an intermediary that cached it would be handing the next caller a
// token this service did not mint for it.
func writeReadAnswer(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
