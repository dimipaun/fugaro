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
