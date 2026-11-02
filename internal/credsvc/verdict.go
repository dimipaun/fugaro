package credsvc

// The sign route (C3.9e, D1067, D1073, D1074): a review run hashes its artifact
// with the artifact writer's own rules and asks this service to sign the DIGEST
// — the reviewer's prose never leaves the run (D892) — and the service answers
// the HMAC-SHA-256 of a FRAME that binds the digest to the repository and the
// run it verified, under that account's review-signing key.
//
// Three things are the whole of the route's design, and each is why the others
// are what they are:
//
//   - The frame (D1073). A digest signed bare would be a signature any run of
//     any repository of that account could present; the frame names the run
//     identity's own slug and run, so a signature made for one review run of
//     one repository cannot be presented as another's, and the verify route
//     (C3.9f) can refuse a cross-run replay by itself. The request has no field
//     that could name either: a caller-writable field is the one thing the
//     frame exists to exclude.
//   - The key comes BEFORE the mark (§5.2, §6.4). The account's key is read
//     first and fails closed — a missing entry, an entry that is not a usable
//     key, an unreadable bundle is a refusal with nothing written — so a run
//     whose key does not exist yet is refused with no mark taken, and the
//     operator's `creds set` and a re-run sign the head. A mark taken and then
//     found unanswerable would be an unsigned review and a re-review.
//   - The mark comes BEFORE the answer (§5.6, D1074). It is a create-only write
//     on one node per (slug, run), so a run is signed once and the service
//     enforces that rather than trusting the caller to ask once. A mark that
//     cannot be taken is a refusal and no signature comes back; the direction
//     the failure is allowed to go is the other one — a run marked and not
//     answered is an unsigned review, and the remedy is re-reviewing the head.
//
// The route reads no policy and consults no cascade: a signature is not a model
// credential, and no admission, tier or budget has anything to say about it.
// Its whole admission is the class, the pairing and the stop flag ServeHTTP has
// already made before this file is reached.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
)

// signAlgorithm is the construction the answer names and the one this service
// signs with (D892). Naming it is what keeps either end from quietly accepting
// a weaker one, and the request has no algorithm field at all: a caller that
// could name one could name a weaker one.
const signAlgorithm = "HMAC-SHA-256"

// frameVersion is the frame's first field: the tag that makes these bytes a
// version of this contract rather than another. A second version of the frame
// takes a different tag, so a signature made under one is not one under the
// other.
const frameVersion = "v1"

// digestRE is the shape of the digest a sign call carries: the lowercase hex of
// the 32 bytes of a SHA-256. The digest is the one field of the request and it
// is held to this shape before anything is read or written: a frame built over
// anything else would be a signature over bytes no artifact writer computed.
var digestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// SignRequest is a sign call's body: the SHA-256 of the artifact's exact bytes,
// computed by the artifact writer (CSR3).
type SignRequest struct {
	Digest string `json:"digest"`
}

// SignResponse is the sign route's answer (D892's shape): the algorithm, named,
// and the MAC rendered lowercase hex.
type SignResponse struct {
	Alg       string `json:"alg"`
	Signature string `json:"signature"`
}

// signFrame builds the message a verdict's signature is taken over (D1073):
//
//	v1\n<repository slug>\n<review run>\n<digest>
//
// The slug and the run are the VERIFIED identity's own (its fs and fr), and
// never a request field — which is also why the request has no such field to
// carry. The digest is the request's.
//
// Every field is bounded before the message is built, the slug and the run by
// the rule the identity itself was read with and the digest by its own shape,
// so two different triples cannot frame to the same bytes: a framing that could
// be padded into ambiguity would be worse than no framing at all.
//
// It is written once so that it has one spelling: the verify route (C3.9f)
// recomputes these bytes, and a second spelling of the frame in that handler
// would be a signature this service makes and refuses to read.
func signFrame(slug, run, digest string) ([]byte, error) {
	switch {
	case !token.ValidSegment(slug):
		return nil, errors.New("the run identity's repository is not a usable frame field")
	case !token.ValidSegment(run):
		return nil, errors.New("the run identity's run id is not a usable frame field")
	case !digestRE.MatchString(digest):
		return nil, errors.New("the digest is not the 64 lowercase hex characters of a SHA-256")
	}
	return []byte(frameVersion + "\n" + slug + "\n" + run + "\n" + digest), nil
}

// readSignRequest reads a sign call's body out of the call ServeHTTP already
// read (readCall), which is where the caller's own two tokens were registered
// with the service's redaction for the length of this call (D1074).
//
// The bound is the mint's 4 KiB and not the gateway's, and it is checked here
// rather than by the reader: the whole request is one 64-character field, so a
// body larger than this is not a sign call, and a body that is a sign call
// never approaches it. The decode is strict, as every body of this contract is:
// a field this code does not know is refused rather than ignored — which is
// what makes a request that tries to name the run or the repository a refusal
// rather than a request whose extra field was quietly dropped.
func readSignRequest(body []byte) (SignRequest, error) {
	if len(body) > maxMintRequestBytes {
		return SignRequest{}, fmt.Errorf("the body is larger than the %d bytes this service accepts", maxMintRequestBytes)
	}
	var req SignRequest
	if err := decodeStrict(body, &req); err != nil {
		return SignRequest{}, errors.New("the body is not one JSON object of one field")
	}
	if !digestRE.MatchString(req.Digest) {
		return SignRequest{}, errors.New("the call carries no digest (64 lowercase hex characters)")
	}
	return req, nil
}

// SignMarks is the create-only write the one-signature mark is taken with
// (D1074). It is Config.Probes' own pair of calls — read the node for its ETag,
// write it back under that ETag — and it is a type of its own because it is a
// different node and a different fact: a claim on signing one run's verdict,
// where the probe's is a claim on asking one credential. *rtdb.Client is one.
type SignMarks interface {
	GetETag(ctx context.Context, path string, out any) (etag string, found bool, err error)
	PutIfMatch(ctx context.Context, path, etag string, v any) error
}

// pathReviewSigns is where the one-signature marks live: a sibling of the probe
// leases and of the marks, under the subtree the database rules deny every run
// token. The node therefore needs no rule of its own to be unreachable from a
// run (O1, C1.3): the subtree's own denial is what keeps a run from reading its
// mark and from clearing it to sign again.
const pathReviewSigns = "creds/review-signs"

// reviewSign is one run's mark. Its EXISTENCE is the fact the rule is made of —
// the conditional write cannot create a second one — and the fields are there
// for the operator reading the subtree: which run holds the mark, and when it
// was taken.
type reviewSign struct {
	Slug string    `json:"slug"`
	Run  string    `json:"run"`
	At   time.Time `json:"at"`
}

// reviewSignPath is one run's mark's node, with each segment escaped the one
// way every node under creds/ escapes one (budget.Key), so a slug and the node
// it names cannot disagree with the marks' spelling.
func reviewSignPath(slug, run string) string {
	return pathReviewSigns + "/" + budget.Key(slug) + "/" + budget.Key(run)
}

// errSignedAlready is the state a second sign meets: the run's mark is already
// there, so this run has signed. It is not a failure of the service's — it is
// the answer the rule exists to give — and the handler reads it to write the
// warning line §5.6 asks for beside the refusal.
var errSignedAlready = errors.New("credsvc: this run has already been signed")

// takeSignMark takes one run's mark, create-only, or reports that the run has
// already signed.
//
// Create-only is the ETag's and not the database rules': the service's own
// writes go through IAM and bypass the rules, so a write-once rule would be a
// guarantee this path never consults. The node is read for its ETag first and
// the write is conditional on it, which is create-only because the ETag that
// came back is the ABSENT node's — a node created in between carries a
// different one and this write fails. The pair is the discipline the probe's
// lease already uses (claimProbe), down to the retry and to its having no pause
// in it: an ETag the write keeps losing is read again, immediately, up to the
// number of attempts every conditional write under creds/ uses — creds' own cas
// loop and claimProbe are the other two, and neither waits between attempts
// either. The wait would be for a contention that this node cannot have for
// long: the writer that moved it is another instance taking the SAME run's
// mark, and the fifth read answers the same refusal a pause would have reached.
// The loop can then only end two ways — the mark is this call's, or somebody
// else's.
//
// The one-shot property is the whole of what the caller may rely on, and it is
// why there is no read of the node anywhere else in this service: a reader
// would be a second path that could disagree with the write.
func (s *Server) takeSignMark(ctx context.Context, run creds.Run) error {
	path := reviewSignPath(run.Slug, run.ID)
	mark := reviewSign{Slug: run.Slug, Run: run.ID, At: s.now()}
	var last error
	for i := 0; i < creds.DefaultAttempts; i++ {
		var cur reviewSign
		etag, found, err := s.cfg.Signs.GetETag(ctx, path, &cur)
		if err != nil {
			return err
		}
		if found {
			return errSignedAlready
		}
		if err := s.cfg.Signs.PutIfMatch(ctx, path, etag, mark); err == nil {
			return nil
		} else {
			last = err
		}
	}
	return fmt.Errorf("the one-signature mark could not be taken: %w", last)
}

// signUnauditedRefusal is what a sign the service could not record is refused
// with: this file's own sentence and never the auditor's error text, which
// names the field it refused.
const signUnauditedRefusal = "the service could not record the signature, so it signed nothing"

// sign answers one sign call. purpose is the route's own (routeAct), which the
// caller has already read, because every refusal below is recorded as the act
// the run asked for and not as the branch it was answered in.
//
// The order of the checks is the design's (§5.2) and each step is why the next
// one is where it is:
//
//   - the body, as the contract reads it, and the class. A caller that is not a
//     review job's account is refused here rather than deeper in, because the
//     key this route reads is the KEY OF THE CALLER'S ACCOUNT: a per-repository
//     job has no review-signing entry, and a route that looked one up for it
//     would be naming a credential the deployment never configured.
//   - the key, BEFORE anything is written, failing closed. Only a key in hand
//     reaches the mark.
//   - the mark, before the answer, which is what makes the signature the run's
//     first and only one.
func (s *Server) sign(ctx context.Context, w http.ResponseWriter, r *http.Request, id Identity, req Request, purpose string) {
	ra, account := s.reviewAccount(id.JobAccount)
	// The credential every line below may name, derived from the VERIFIED
	// identity and not from the request: it is the entry the account's key
	// would be at, so a call whose body this contract refuses still records a
	// sign an operator can account for — the shape a mint's lines already have,
	// where the App key's name is known before the mint's body is looked at.
	// A caller whose account is in no row of the review table has no entry to
	// name, and its lines carry none.
	credential := ""
	if account {
		credential = config.ReviewSigningKeySecret(ra.Account)
	}
	cl, err := readCall(r, []string{req.RunIDToken, req.JobIDToken})
	if err != nil {
		s.refuseCall(ctx, w, id, purpose, credential, "", StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	signReq, err := readSignRequest(cl.body)
	if err != nil {
		// The refusal names the field and never what the call sent, and it is
		// answered before the bundle is read: a request the contract refuses
		// reads no credential at all.
		s.refuseCall(ctx, w, id, purpose, credential, "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if id.JobClass != ClassReview {
		s.refuseCall(ctx, w, id, purpose, "", "", StatusUnverified, ErrorUnverifiedRunIdentity,
			"the sign route is served to a review job's account only")
		return
	}
	if !account {
		// Unreachable through ServeHTTP — pairReview refuses a caller whose
		// account is in no row of the table — and kept because the account
		// name is what the key's entry is derived from: an account guessed
		// from the email would be a key nobody's.
		s.refuseCall(ctx, w, id, purpose, "", "", StatusUnverified, ErrorUnverifiedRunIdentity,
			"the calling account is not one of this service's review job accounts")
		return
	}
	if s.cfg.Signs == nil {
		// A service that cannot take the mark cannot sign: the one-signature
		// rule is what the mark is for, and a signature without one would be a
		// run that can sign as often as it likes. This is a deployment that
		// wired no writer, so it is the wiring's refusal and not the caller's.
		s.refuseCall(ctx, w, id, purpose, credential, "", StatusUnavailable, ErrorStateUnavailable,
			"the service cannot record the one-signature mark, so it signs nothing")
		return
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseCall(ctx, w, id, purpose, credential, "", StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	name, key, err := b.ReviewSigningKey(ra.Account, false)
	if err != nil {
		// Fail closed, and with NOTHING written: a missing entry, an entry
		// that is not a usable key, and an unreadable bundle are all refusals
		// here, and never a signature under anything else (D143, §6.4). It is
		// also what leaves the run's one signature unspent: the operator sets
		// the key and a re-run signs the head.
		s.refuseCall(ctx, w, id, purpose, credential, "", StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	frame, err := signFrame(id.Slug, id.Run, signReq.Digest)
	if err != nil {
		// The digest was held to its shape above and the slug and the run are
		// the verified identity's own, so this is unreachable through a served
		// call — and it is answered rather than asserted because the frame is
		// the one input a signature is made of.
		s.refuseCall(ctx, w, id, purpose, name, "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	if err := s.takeSignMark(ctx, creds.Run{Slug: id.Slug, ID: id.Run}); err != nil {
		if errors.Is(err, errSignedAlready) {
			// §5.6's security event, and the line is the service's own log
			// rather than an audit field added for it: a stage re-run is the
			// honest reading and something inside the container asking is the
			// other, and an operator's alerting selects a warning it can name
			// today. Nothing here is a value: the credential is the entry's
			// NAME and the run and the repository are the verified ones.
			s.log.Warn("credsvc: a run asked for a second signature",
				"purpose", purpose, "credential", name, "repository", id.Slug, "run", id.Run)
			s.refuseCall(ctx, w, id, purpose, name, "", StatusRequestRefused, ErrorRequestRefused,
				"this run has already been signed, and a run is signed once")
			return
		}
		// The mark could not be taken: a refusal, and never a signature the
		// mark would have bounded.
		s.log.Warn("credsvc: the one-signature mark could not be taken",
			"purpose", purpose, "credential", name, "repository", id.Slug, "run", id.Run, "error", err)
		s.refuseCall(ctx, w, id, purpose, name, "", StatusUnavailable, ErrorStateUnavailable,
			"the one-signature mark could not be taken, so the service signed nothing")
		return
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(frame)
	body, err := json.Marshal(SignResponse{Alg: signAlgorithm, Signature: hex.EncodeToString(mac.Sum(nil))})
	if err != nil {
		// Unreachable: the answer is two strings, and a body the service
		// cannot build is refused rather than answered with half of itself.
		s.log.Error("credsvc: the sign route's answer could not be built", "error", err)
		s.refuseCall(ctx, w, id, purpose, name, "", StatusUnavailable, ErrorStateUnavailable, signUnauditedRefusal)
		return
	}
	if err := s.auditCall(ctx, id, purpose, name, "", StatusOK); err != nil {
		// An act this service cannot record is not made (D143): the signature
		// reaches nobody. The MARK stays, deliberately — this is the one place
		// the two failures differ from the missing key's — because the run has
		// already been signed as far as the service is concerned, and the
		// remedy §5.6 picks is re-reviewing the head rather than a second
		// signature.
		s.log.Error("credsvc: the sign route's audit line was not written", "error", err)
		s.refuseCall(ctx, w, id, purpose, name, "", StatusUnavailable, ErrorStateUnavailable, signUnauditedRefusal)
		return
	}
	writeSignAnswer(w, body)
}

// writeSignAnswer writes the sign route's answer, from the body sign already
// audited. The body carries a MAC and no credential, and it is no-store
// anyway: an intermediary that cached a verdict's signature would be handing
// the next caller a signature this service did not make for it.
func writeSignAnswer(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------------------
// The verify route (C3.9f, D1068, §5.3–§5.6)
// ---------------------------------------------------------------------------

// signatureRE is the shape of the signature a verify call presents: the
// lowercase hex of the 32 bytes of an HMAC-SHA-256, which is what the sign
// route renders. It is held to this shape before any key is read, so a
// signature this service could not have made is a refused CALL rather than a
// `valid:false` about an artifact — the distinction §5.3 exists for.
var signatureRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// VerifyRequest is a verify call's body: the account whose key the answer is
// recomputed under, the artifact's digest, the signature it was given, and the
// two frame fields the signature was made over.
//
// The run and the repository are here because a VERIFIER HOLDS NO RUN IDENTITY
// (D1068, D1073): the service recomputes the HMAC over the frame those two
// fields build, so a caller that names the wrong run gets `valid:false` and not
// a match. Naming them cannot make a signature valid that was not made for
// them: the frame is the message, and the key is the account's.
//
// The account is named for the same reason and with the same limit: it selects
// WHICH key to recompute under. It cannot make an invalid signature valid, and
// the design chose naming over trying every account's key precisely so that the
// route never answers WHICH key matched — an account oracle, and a signature
// valid under another account of the same installation would verify (D1068).
type VerifyRequest struct {
	Account    string `json:"account"`
	Digest     string `json:"digest"`
	Signature  string `json:"signature"`
	Run        string `json:"run"`
	Repository string `json:"repository"`
}

// VerifyResponse is the verify route's answer (§5.3): ONE refusal bit and
// nothing else — no reason, no key id, no account echo, and no distinguisher
// between "wrong key" and "wrong bytes". Everything the service cannot answer
// at all is a typed refusal instead, because a bare false would tell a
// coordinator that a verdict is not ours when the truth is that we could not
// tell.
type VerifyResponse struct {
	Valid bool `json:"valid"`
}

// readVerifyRequest reads a verify call's body out of the call ServeHTTP's
// verify branch already read.
//
// The bound is the mint's 4 KiB and not the gateway's: the whole request is five
// short fields, so a body larger than this is not a verify call, and a body that
// is one never approaches it. The decode is strict, as every body of this
// contract is, so a field this code does not know is a refusal rather than an
// ignored field.
//
// Every field is held to its shape HERE, before a key is read, and each shape is
// the one its own part of the contract is: the account to the bundle's
// credential-name rule (ValidCredentialName — the name an entry could have), the
// digest and the signature to the 64 lowercase hex characters they are, and the
// run and the repository to token.ValidSegment, the rule the identity itself was
// read with (auth.go) and the rule the frame's own fields are bounded by. A
// request any of them refuses is `request_refused`, which is the section's
// "your call was bad" — never `valid:false`, which has one meaning only.
func readVerifyRequest(body []byte) (VerifyRequest, error) {
	if len(body) > maxMintRequestBytes {
		return VerifyRequest{}, fmt.Errorf("the body is larger than the %d bytes this service accepts", maxMintRequestBytes)
	}
	var req VerifyRequest
	if err := decodeStrict(body, &req); err != nil {
		return VerifyRequest{}, errors.New("the body is not one JSON object of the fields this route takes")
	}
	switch {
	case !ValidCredentialName(req.Account):
		return VerifyRequest{}, errors.New("the call names no account this service may be asked about")
	case !digestRE.MatchString(req.Digest):
		return VerifyRequest{}, errors.New("the call carries no digest (64 lowercase hex characters)")
	case !signatureRE.MatchString(req.Signature):
		return VerifyRequest{}, errors.New("the call carries no signature (64 lowercase hex characters)")
	case !token.ValidSegment(req.Repository):
		return VerifyRequest{}, errors.New("the call's repository is not a usable frame field")
	case !token.ValidSegment(req.Run):
		return VerifyRequest{}, errors.New("the call's run is not a usable frame field")
	}
	return req, nil
}

// verifyEqual is the comparison this route makes between the MAC it recomputed
// and the one the caller presented. It is crypto/hmac's Equal and never `==` on
// the hex strings (F7): the signature is an attacker-supplied value, and a
// comparison that stopped at the first differing byte would let a caller learn a
// valid prefix from how long the answer took.
//
// It is a variable rather than a call written in place so that the rule is one a
// test can hold rather than one a comment asserts:
// TestVerifyComparesTheSignatureInConstantTime wraps it, asserts that every
// comparison the route makes is handed two 32-byte MACs — which is what makes
// hmac.Equal's guarantee apply, since it is constant-time for equal lengths —
// and delegates to hmac.Equal, so what the test observes is the route's
// behaviour and not this line's spelling.
var verifyEqual = hmac.Equal

// signatureMatches reports whether the presented signature is the MAC this
// service recomputed. The presented value is decoded from its hex to the raw
// bytes, so the comparison is over the MACs themselves: both sides are then the
// 32 bytes the shape rule above has already made them, and the hex spelling —
// which two callers could disagree about while agreeing about the MAC — decides
// nothing.
func signatureMatches(want []byte, presented string) bool {
	got, err := hex.DecodeString(presented)
	if err != nil {
		return false
	}
	return verifyEqual(want, got)
}

// auditVerify writes one verify line: the verifier as its caller and the
// review-signing key as its credential, and NEITHER a run nor a job (§5.5) —
// a verifier holds no run identity and is not a job of this installation, so
// there is nothing else the line could say. It does not go through
// Identity.Audit because there is no Identity: the verify path is answered
// ahead of Verify, which is the only thing that builds one.
func (s *Server) auditVerify(ctx context.Context, caller, purpose, credential, status string) error {
	return s.audit.Write(ctx, AuditEvent{
		Caller:     caller,
		Credential: credential,
		Purpose:    purpose,
		Status:     status,
	})
}

// refuseVerify answers one verify refusal and records it, in the shape
// refuseCall already has for the routes whose caller holds an Identity: one
// typed refusal to the caller, one line to the operator, and the caller's answer
// is the same whether or not the line lands (D143).
//
// It is reached only once the caller IS verified — the refusals above that point
// are answered by refuse alone, with no line, exactly as the shared parse's
// identity refusals are: a line's "who" is what this service verified, and there
// is no who to name before then.
func (s *Server) refuseVerify(ctx context.Context, w http.ResponseWriter, caller, credential string, status int, typ, message string) {
	if err := s.auditVerify(ctx, caller, PurposeReviewVerify, credential, typ); err != nil {
		// The service's own log line is where an operator reads an act it could
		// not record, for the reason refuseCall gives: the answer to the caller
		// is a refusal either way.
		s.log.Error("credsvc: a refused verify call could not be audited", "error", err)
	}
	s.refuse(w, status, typ, message)
}

// refuseVerifier answers a refusal of the CALLER, which is the one refusal on
// this route that has no line: nothing has been verified yet, so there is no
// caller for a line to name, and the class's own error type is the wire's
// (ErrUnverifiedJob is `unverified_job_identity`; ErrUnverified is
// `unverified_run_identity`, which this class never returns but the vocabulary
// maps; anything else is the state the token had to be checked against being
// unreadable, a refusal the caller halts on and never a fallback).
func (s *Server) refuseVerifier(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnverifiedJob):
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
	case errors.Is(err, ErrUnverified):
		s.refuse(w, StatusUnverified, ErrorUnverifiedRunIdentity, err.Error())
	default:
		s.refuse(w, StatusUnavailable, ErrorStateUnavailable, err.Error())
	}
}

// verify answers one verify call (§5.3): whether the presented signature is the
// HMAC this service recomputes over the frame the caller names, under the named
// account's key. It is reached from ServeHTTP's first branch, ahead of
// RequestFromHTTP, and it does there what every other route does here: it
// verifies its caller, reads its call, reads the key the answer is made with,
// and records the act.
//
// The order below is why each step is where it is:
//
//   - the caller, first, and on its own token: this is the route's whole
//     admission, and it is the reason the route is where it is in ServeHTTP. A
//     refusal here is typed and has no line — nothing is verified yet.
//   - the call's body, whose read is ALSO the registration of the verifier's own
//     token with the call-scoped scrub (§5.6, D1074). readCall builds that list,
//     and the shared parse that builds it for every other route never runs for
//     this one: the handler therefore registers the one token it carries and
//     only that one, with the same scrub the model path and the sign route use.
//   - the key, read through C3.9c's accessor and FAILING CLOSED: an account this
//     installation holds no review-signing entry for is `state_unavailable` and
//     never `valid:false`, because a bare false would answer "the artifact is
//     not that account's" to a service that could not tell (§5.3). A name that
//     is not a legal credential name never reaches here at all: it is
//     `request_refused` above.
//   - the frame, recomputed through C3.9e's builder and not a second spelling of
//     it, and compared in constant time.
//
// There is no mark and no cascade here: a verify takes no state, writes none,
// and consults no policy — the answer is a function of the body and the key, and
// a route that could write would be a route a verifier could spend.
func (s *Server) verify(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	purpose, _ := routeAct(r.URL.Path)
	tok, err := bearerFromHTTP(r)
	if err != nil {
		// No line: the call is refused before its caller was verified, which is
		// the one refusal on this route with nothing to name.
		s.refuse(w, StatusUnverified, ErrorUnverifiedJobIdentity, err.Error())
		return
	}
	caller, err := s.cfg.Auth.VerifyVerifier(ctx, tok)
	if err != nil {
		s.refuseVerifier(w, err)
		return
	}
	cl, err := readCall(r, []string{tok})
	if err != nil {
		s.refuseVerify(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable, err.Error())
		return
	}
	req, err := readVerifyRequest(cl.body)
	if err != nil {
		// The refusal names the field and never what the call sent, and it is
		// answered before the bundle is read: a request this contract refuses
		// reads no key at all, and it names no credential because none was
		// chosen — the account is the CALLER'S text until an entry holds it.
		s.refuseVerify(ctx, w, caller, "", StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	b, err := s.cfg.Bundles.Load(ctx)
	if err != nil {
		s.refuseVerify(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	name, key, err := b.ReviewSigningKey(req.Account, false)
	if err != nil {
		s.refuseVerify(ctx, w, caller, "", StatusUnavailable, ErrorStateUnavailable, BundleRefusal(err))
		return
	}
	frame, err := signFrame(req.Repository, req.Run, req.Digest)
	if err != nil {
		// Every field was held to its shape by readVerifyRequest above, so this
		// is unreachable through a served call — and it is answered rather than
		// asserted because the frame is the one message the answer is made of.
		s.refuseVerify(ctx, w, caller, name, StatusRequestRefused, ErrorRequestRefused, requestRefused+err.Error())
		return
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(frame)
	valid := signatureMatches(mac.Sum(nil), req.Signature)
	body, err := json.Marshal(VerifyResponse{Valid: valid})
	if err != nil {
		// Unreachable: the answer is one bit, and a body the service cannot
		// build is refused rather than answered with half of itself.
		s.log.Error("credsvc: the verify route's answer could not be built", "error", err)
		s.refuseVerify(ctx, w, caller, name, StatusUnavailable, ErrorStateUnavailable, verifyUnauditedRefusal)
		return
	}
	// A `valid:false` answer is an ANSWER and not a refusal: the service
	// recomputed the MAC with the account's key and the two differ, which is
	// exactly what StatusOK records — a credential was used. The refusal statuses
	// are for the cases where the service could not tell (§5.3).
	if err := s.auditVerify(ctx, caller, purpose, name, StatusOK); err != nil {
		// An act this service cannot record is not made (D143): the bit reaches
		// nobody, and the caller gets the state-as-unavailable refusal its
		// coordinator already reads as "do not ship".
		s.log.Error("credsvc: the verify route's audit line was not written", "error", err)
		s.refuseVerify(ctx, w, caller, name, StatusUnavailable, ErrorStateUnavailable, verifyUnauditedRefusal)
		return
	}
	writeVerifyAnswer(w, body)
}

// verifyUnauditedRefusal is what a verify the service could not record is
// refused with: this file's own sentence and never the auditor's error text,
// which names the field it refused.
const verifyUnauditedRefusal = "the service could not record the answer, so it answered nothing"

// writeVerifyAnswer writes the verify route's answer, from the body verify
// already audited. The body is one bit and no-store: an answer about a
// signature is about one artifact at one instant, and a cached one would be
// handed to the next caller as if the service had recomputed it for them.
func writeVerifyAnswer(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
