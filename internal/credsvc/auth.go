// Package credsvc is the credentials service: the one process that holds an
// installation's model credentials and hands them to a run's own gateway
// (docs/credentials-service/implementation-plan.md, spec.md and
// reconciliation.md). It is deployed separately from every job (D146, D147),
// a job reaches it with its own service account's Google ID token, and the
// run it acts for is proved by the run's budget identity (D134).
//
// This file is the service's front door: nothing else in the package may act
// on a call before Authenticator.Verify has admitted it. It is
// SECURITY-SENSITIVE and is reviewed as such; three properties are the whole
// of it, and a test pins each:
//
//   - the caller is an installation job service account, not merely an
//     identity that Cloud Run IAM let in: its ID token's signature is checked
//     against Google's own certificates, and its email claim against the
//     installation's job service accounts (the owner's answer of 2026-10-19),
//     and an account that belongs to a *shared* project job is refused whole
//     until the phase-3 shared-job rules are on main (C3.9, Global Constraint
//     6). That last check is decided from the verified email and never from
//     the caller's own X-Fugaro-Run-Job header: one service account is
//     dedicated to one Cloud Run job, so the email is what says which job
//     called, while the header is text the caller chose;
//   - the run identity is a Firebase ID token this installation's project
//     issued, signed by a key Google publishes, for uid r~<slug>~<run>, with
//     its fx claim still ahead of us (D134);
//   - the run has not ended, which is read from the write-once
//     outcomes/$day/$slug/$run and never from agents/<slug>/<run> — the rules
//     give the run's own token .write on the registry entry, so a hostile
//     agent can rewrite or recreate it and it proves nothing (review finding
//     W2).
//
// No value a caller presents is ever echoed: an error names what is wrong
// with the identity, never the token, and never the caller's own text. That
// includes the key id inside a token, which is caller text like any other and
// is 8 KiB of attacker-chosen bytes if the caller wants it to be — so a
// refusal says the key id is not one Google publishes and does not repeat it
// (a test pins that).
package credsvc

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/task"
)

// The refusals. Each is a sentinel so a caller can answer the wire contract's
// error type without reading a message: an unverified run identity, an
// unverified caller, and state the service cannot read (which is a refusal
// too — D143's "unreachable halts, never falls back").
var (
	// ErrUnverified is a run identity this service does not admit.
	ErrUnverified = errors.New("credsvc: the run identity is not verified")
	// ErrUnverifiedJob is a caller this service does not admit.
	ErrUnverifiedJob = errors.New("credsvc: the job identity is not verified")
	// ErrUnavailable is the service being unable to read the state the check
	// needs. It is not an admission and not a refusal of the caller.
	ErrUnavailable = errors.New("credsvc: the state the identity must be checked against is unavailable")
	// ErrUnknownKeyID is a token whose key id Google does not publish. It is
	// a refusal of the token, not a failure of the service: the key set was
	// read and does not hold it.
	ErrUnknownKeyID = errors.New("credsvc: no published key for the token's key id")
)

const (
	// maxTokenBytes bounds a presented token before it is parsed.
	maxTokenBytes = 8 << 10
	// maxKeysBytes bounds Google's published key set.
	maxKeysBytes = 1 << 20
	// clockSkew is how far ahead of our clock a token's iat may be.
	clockSkew = 2 * time.Minute
	// maxRunDays bounds the liveness scan. A run's identity lives until its
	// fx, which token.validate holds to at most 30 days from the mint, so a
	// run that is live now cannot have been launched more than 30 days ago
	// (plus a day of day-number rounding, and the scan's own extra day above
	// today, so at most 34 reads). A run id claiming an older launch
	// is refused rather than scanned: the scan is one database read per day,
	// and an identity that could ask for thousands of them is not one this
	// service admits.
	maxRunDays = 32
	// keyCacheDefault and the bounds around Google's Cache-Control.
	keyCacheDefault = time.Hour
	keyCacheMin     = time.Minute
	keyCacheMax     = 24 * time.Hour
)

// Request is what a call to the service presents: the run's budget identity,
// the Google ID token Cloud Run IAM admitted, and the job the run is in (the
// wire contract's headers; wire.go).
type Request struct {
	// RunIDToken is HeaderRunIdentity: the run's Firebase ID token.
	RunIDToken string
	// JobIDToken is the Authorization header's bearer token: the job service
	// account's Google ID token.
	JobIDToken string
	// Job is HeaderRunJob: the Cloud Run job the run reports it is in. It is
	// the caller's claim, held to the job-name rule (ValidJobName — the shape
	// every Cloud Run job of this installation has, which is narrower than the
	// header rule because the value is recorded and displayed; review finding
	// m3) and recorded, and nothing is decided from it (which job called is
	// JobAccount's, in Identity).
	Job string
}

// Identity is a verified run: who is spending, on what, and until when.
type Identity struct {
	// Slug is the repository's slug (the fs claim).
	Slug string
	// Run is the run id (the fr claim).
	Run string
	// Project is the Fugaro project (the fp claim), which must be this
	// installation's.
	Project string
	// FX is when the identity stops being honoured (the fx claim). Until an
	// outcome is written it is the only bound on a replayed token (W2).
	FX time.Time
	// Launcher is the identity that minted the run (the rb claim).
	Launcher string
	// Job is the Cloud Run job the run reported — the caller's claim, which is
	// why JobAccount, the verified service account, is the one of the two that
	// identifies the job — and JobAccount is the account that called: the
	// audit line's "who" (C1.13).
	Job        string
	JobAccount string
}

// KeySource returns the RSA public key a token's kid names. A kid that is not
// in the set is ErrUnknownKeyID; a set that could not be read is ErrUnavailable.
type KeySource interface {
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// OutcomeReader answers whether a run's write-once outcome exists on a day.
// It reads outcomes/$day/$slug/$run and nothing else: that node is written
// once and no run token can rewrite or delete it (the rules' RUN && DAYOK &&
// !data.exists()), which is what makes it evidence a run has ended.
type OutcomeReader interface {
	OutcomeExists(ctx context.Context, day int64, slug, run string) (bool, error)
}

// Getter is the one database call the outcome reader needs; *rtdb.Client is
// one.
type Getter interface {
	Get(ctx context.Context, path string, out any) (found bool, err error)
}

// RTDBOutcomes reads the outcomes from the installation's database, as the
// service's own account. It is a read of one node per day of a run's life,
// and it is the only read this package makes of the installation's state.
type RTDBOutcomes struct {
	DB Getter
}

// OutcomeExists is the OutcomeReader over budget.PathOutcome: the day is the
// UTC epoch day the rules' TODAY computes, and the path escapes slug and run
// exactly as the writer did.
func (o RTDBOutcomes) OutcomeExists(ctx context.Context, day int64, slug, run string) (bool, error) {
	if !token.ValidSegment(slug) || !token.ValidSegment(run) {
		// budget.PathOutcome panics on an empty segment; a reader is not the
		// place to find that out.
		return false, errors.New("credsvc: the run's slug or run id is not a usable database path segment")
	}
	var raw json.RawMessage
	found, err := o.DB.Get(ctx, budget.PathOutcome(day, slug, run), &raw)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", budget.PathOutcome(day, slug, run), err)
	}
	return found, nil
}

// AuthConfig is the service's identity configuration. Everything a check
// depends on is required: a service that cannot check one of these does not
// start, because the alternative is a service that admits what it cannot
// check. SharedJobAccounts is the one set that may be empty, and an empty one
// is a statement — this installation has no shared project jobs.
type AuthConfig struct {
	// Project is the Fugaro project (fp), e.g. "fugaro".
	Project string
	// FirebaseProject is the Firebase project whose ID tokens are accepted:
	// the aud and the iss of a run's identity. It is not the Fugaro project:
	// the launcher puts the Fugaro project in fp (internal/cli's launch) and
	// Firebase issues the token for its own project id.
	FirebaseProject string
	// Audience is the audience the job's Google ID token must carry — the
	// service's own URL, which is what Cloud Run IAM checked it against.
	Audience string
	// JobAccounts are the installation's per-repository job service accounts,
	// by email: the identities that may call the service at all (the owner's
	// answer of 2026-10-19). One service account is dedicated to one Cloud Run
	// job (internal/backend/gcp's ServiceAccountID), so a verified email here
	// is also what says which job a call comes from.
	//
	// Each must be a printable ASCII email address, and a claim is matched
	// against them by ASCII lower case (foldEmail): an address that is not
	// ASCII, or two entries that are the same address in different cases, is a
	// configuration error rather than a gate. The spelling configured here is
	// what Identity.JobAccount carries and what the audit line writes, so the
	// "who" an operator reads is the "who" they configured.
	JobAccounts []string
	// Keys is Google's published key set for Firebase ID tokens: the run
	// identities this service admits.
	Keys KeySource
	// JobKeys is Google's published key set for Google ID tokens: the callers
	// this service admits (GoogleOAuthCertsURL). It is a second source
	// because the two token kinds are signed by different Google keys, and
	// because checking the caller's signature here means the service does not
	// depend on its ingress having enforced IAM for the *identity* check to
	// mean anything — only for the request to have arrived at all.
	JobKeys KeySource
	// Outcomes reads the write-once outcomes.
	Outcomes OutcomeReader
	// SharedJobAccounts are the service accounts of the installation's shared
	// project jobs (internal/backend/gcp's ProjectServiceAccountID over the
	// published policy's profiles). Until the phase-3 shared-job rules are on
	// main the service admits runs in per-repository jobs only (C3.9, Global
	// Constraint 6), so a caller that is one of these is refused whole.
	//
	// It is asked about the *verified* email claim, never about the
	// X-Fugaro-Run-Job header: the header is the caller's own text, and a gate
	// a caller can satisfy — or escape — by writing a header is not a gate.
	// One account is dedicated to one Cloud Run job, so the email is the
	// signal a caller cannot forge. An installation with no shared project
	// jobs has an empty set, which is the ordinary configuration under Global
	// Constraint 6; a caller whose account is in both this set and
	// JobAccounts is refused, because the refusal is the safe answer.
	SharedJobAccounts []string
	// Now is the clock, for tests; nil means time.Now.
	Now func() time.Time
}

// Authenticator verifies the identities a call to the service presents.
type Authenticator struct {
	cfg AuthConfig
	// jobAccounts maps a presented email, folded to ASCII lower case, to the
	// configured spelling of that account: the fold is how a claim is matched
	// and the spelling is what the identity carries.
	jobAccounts    map[string]string
	sharedAccounts map[string]bool
	now            func() time.Time
}

// NewAuthenticator validates the configuration and returns the verifier.
func NewAuthenticator(cfg AuthConfig) (*Authenticator, error) {
	switch {
	case cfg.Project == "":
		return nil, errors.New("credsvc: no Fugaro project: the fp claim could not be checked")
	case cfg.FirebaseProject == "":
		return nil, errors.New("credsvc: no Firebase project: the run identity's aud and iss could not be checked")
	case cfg.Audience == "":
		return nil, errors.New("credsvc: no audience: the caller's Google ID token could not be checked")
	case len(cfg.JobAccounts) == 0:
		return nil, errors.New("credsvc: no job service accounts: any identity IAM admits would be admitted here too")
	case cfg.Keys == nil:
		return nil, errors.New("credsvc: no key source: the run identity's signature could not be checked")
	case cfg.JobKeys == nil:
		return nil, errors.New("credsvc: no job key source: the caller's signature could not be checked")
	case cfg.Outcomes == nil:
		return nil, errors.New("credsvc: no outcome reader: the run's liveness could not be checked")
	}
	// The accounts are held to the rule the presented email is held to (a
	// printable ASCII address), and the map carries the configured spelling
	// beside the folded key: the spelling is what the identity's JobAccount and
	// the audit line carry, and the fold is what a presented claim is matched
	// by. An account that is not ASCII could never be matched — verifyJob
	// refuses a non-ASCII claim rather than folding it — so it is a
	// configuration error here rather than a gate that silently admits nobody.
	accounts := make(map[string]string, len(cfg.JobAccounts))
	for _, email := range cfg.JobAccounts {
		folded, ok := foldEmail(email)
		if !ok {
			return nil, errors.New("credsvc: a job service account is not a printable ASCII email address")
		}
		if prev, dup := accounts[folded]; dup {
			if prev != email {
				return nil, errors.New("credsvc: two job service accounts are the same address in different cases")
			}
			continue
		}
		accounts[folded] = email
	}
	// An empty SharedJobAccounts is not a missing gate: under Global
	// Constraint 6 an installation with no shared project jobs has none, and
	// the set is what the policy's profiles say, so there is nothing to
	// require of it here.
	shared := make(map[string]bool, len(cfg.SharedJobAccounts))
	for _, email := range cfg.SharedJobAccounts {
		folded, ok := foldEmail(email)
		if !ok {
			return nil, errors.New("credsvc: a shared job service account is not a printable ASCII email address")
		}
		shared[folded] = true
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Authenticator{cfg: cfg, jobAccounts: accounts, sharedAccounts: shared, now: now}, nil
}

// Verify admits a call, or refuses it with ErrUnverifiedJob (the caller is
// not one of the installation's job service accounts, or is a shared project
// job's, which Global Constraint 6 refuses whole until C3.9), ErrUnverified
// (the run identity is not this installation's, is past its fx, or names a run
// that has ended) or ErrUnavailable (the service could not read the outcome).
// It never returns a partially verified identity.
func (a *Authenticator) Verify(ctx context.Context, req Request) (Identity, error) {
	now := a.now()
	account, err := a.verifyJob(ctx, req.JobIDToken, now)
	if err != nil {
		return Identity{}, err
	}
	// The run's job is checked before the run identity: a run the service
	// cannot place is one it may not admit.
	//
	// X-Fugaro-Run-Job is the caller's *claim* about which Cloud Run job the
	// run is in — the platform sets it, but a launcher may override any
	// environment variable (internal/runner's jobName) — so it is kept for the
	// run's record and held to the job-name rule (ValidJobName), and it decides
	// nothing. Which job actually called is decided in verifyJob, from the
	// verified account, and that is where Global Constraint 6's shared-job gate
	// is (C3.9).
	//
	// The rule is the job-name rule and not the general header rule (review
	// finding m3): every job name this installation can have is
	// internal/backend/gcp's sanitized [a-z0-9-] at most 49 bytes, while the
	// header rule would accept a quote, a backslash, a '$' or a ';' — values no
	// job of this installation can be named, carried into the run's record and
	// its audit line. The run's placement is a fact about the platform, so a
	// value that could not be one is refused rather than recorded.
	if !ValidJobName(req.Job) {
		return Identity{}, fmt.Errorf("%w: the call names no Cloud Run job name, so the run cannot be placed", ErrUnverified)
	}
	id, err := a.verifyRun(ctx, req.RunIDToken, now)
	if err != nil {
		return Identity{}, err
	}
	id.Job, id.JobAccount = req.Job, account
	return id, nil
}

// verifyJob checks the caller's Google ID token and returns the account that
// called.
//
// Two checks, and the plan asks for both ("checked by Cloud Run IAM **and** by
// this code"):
//
//   - the signature, against Google's own published certificates. Cloud Run
//     IAM also checks it — a request whose bearer token is not a Google ID
//     token for an identity holding run.invoker on the service never reaches
//     this container (C1.18 deploys it with ingress requiring IAM) — but the
//     service does not rely on that: an installation deployed with
//     --allow-unauthenticated, or a request that arrived from inside the
//     network, would otherwise be admitted on a claim set alone.
//   - *which* identity called, which is what IAM cannot say: its bit says an
//     authorised identity did. The email claim must be one of the
//     installation's job service accounts (the owner's answer of 2026-10-19),
//     verified, for this service's own audience, unexpired, and issued by
//     Google. An account that belongs to a *shared* project job is refused
//     here too, before that membership check, because the account is the only
//     part of "which job is this run in" that the caller cannot write (Global
//     Constraint 6, C3.9).
func (a *Authenticator) verifyJob(ctx context.Context, tok string, now time.Time) (string, error) {
	head, claims, err := decodeJWT(tok)
	if err != nil {
		return "", fmt.Errorf("%w: the caller's ID token is not a JWT", ErrUnverifiedJob)
	}
	if alg := claimString(head, "alg"); alg != "RS256" {
		return "", fmt.Errorf("%w: the caller's ID token is not signed with RS256", ErrUnverifiedJob)
	}
	key, err := a.cfg.JobKeys.Key(ctx, claimString(head, "kid"))
	if err != nil {
		if errors.Is(err, ErrUnknownKeyID) {
			return "", fmt.Errorf("%w: %w", ErrUnverifiedJob, err)
		}
		return "", err
	}
	if err := verifySignature(tok, key); err != nil {
		return "", fmt.Errorf("%w: the caller's ID token's signature does not verify", ErrUnverifiedJob)
	}
	if aud := claimString(claims, "aud"); aud != a.cfg.Audience {
		return "", fmt.Errorf("%w: the caller's ID token is for another audience", ErrUnverifiedJob)
	}
	if iss := claimString(claims, "iss"); iss != "https://accounts.google.com" && iss != "accounts.google.com" {
		return "", fmt.Errorf("%w: the caller's ID token was not issued by Google", ErrUnverifiedJob)
	}
	email := claimString(claims, "email")
	// The email is folded to lower case *as ASCII* before it is matched, and an
	// address that is not printable ASCII is refused rather than folded
	// (review nit). strings.ToLower is Unicode-aware, so it folds characters
	// that merely look like the ones a service account's address is made of —
	// U+212A KELVIN SIGN lowercases to 'k', U+0130 to 'i' — and a claim whose
	// spelling the issuer let the caller choose would then be matched against
	// an account it is not. Every Google service account address is ASCII, so
	// nothing a real caller presents is lost by the rule.
	folded, ok := foldEmail(email)
	if !ok {
		return "", fmt.Errorf("%w: the caller's email claim is not a service account address", ErrUnverifiedJob)
	}
	// The shared-job gate of Global Constraint 6 (C3.9) comes first, and it is
	// decided from this verified email and never from the caller's own
	// X-Fugaro-Run-Job header. One service account is dedicated to one Cloud
	// Run job (internal/backend/gcp's ServiceAccountID and
	// ProjectServiceAccountID), so the account is the job; the header is text
	// the caller writes, and a launcher may override any environment variable,
	// so a gate read off it would be one a caller could satisfy by writing a
	// header. An account that is in both sets is refused rather than admitted:
	// the refusal is the safe answer.
	if a.sharedAccounts[folded] {
		// The reason is named and the account is not repeated: it is the
		// caller's own claim set, and the package doc's no-echo rule is that no
		// refusal repeats what a caller presented.
		return "", fmt.Errorf("%w: the caller is a shared project job's service account, and this service admits runs in per-repository jobs only until the phase-3 shared-job rules are on main (C3.9)", ErrUnverifiedJob)
	}
	account, ok := a.jobAccounts[folded]
	if !ok {
		return "", fmt.Errorf("%w: the caller is not one of the installation's job service accounts", ErrUnverifiedJob)
	}
	if verified, ok := claims["email_verified"].(bool); !ok || !verified {
		return "", fmt.Errorf("%w: the caller's email is not verified", ErrUnverifiedJob)
	}
	if exp, ok := claimInt(claims, "exp"); !ok || !now.Before(time.Unix(exp, 0)) {
		return "", fmt.Errorf("%w: the caller's ID token has expired", ErrUnverifiedJob)
	}
	// The token's own validity window, checked as verifyRun checks the run
	// identity's: a token issued in the future, or one that is not valid yet,
	// is not one this service admits (review nit). Google's ID tokens carry an
	// iat; a nbf is checked when it is there.
	if iat, ok := claimInt(claims, "iat"); ok && time.Unix(iat, 0).After(now.Add(clockSkew)) {
		return "", fmt.Errorf("%w: the caller's ID token is issued in the future", ErrUnverifiedJob)
	}
	if nbf, ok := claimInt(claims, "nbf"); ok && time.Unix(nbf, 0).After(now.Add(clockSkew)) {
		return "", fmt.Errorf("%w: the caller's ID token is not valid yet", ErrUnverifiedJob)
	}
	// The account this returns is the configured spelling, not the claim's:
	// the two are the same address in whatever case the token carries, and the
	// one this service writes to its audit line (C1.13) is the one an operator
	// configured and can search for (review nit).
	return account, nil
}

// foldEmail folds an email address to lower case as ASCII, reporting false for
// anything that is not printable ASCII (no space, 0x21 to 0x7e) within the
// header bound. The ASCII-only rule is printableHeaderValue's, and it is why
// this is not strings.ToLower alone: that folds by Unicode rules, and a Unicode
// fold turns characters that merely look like a letter of an address into that
// letter.
func foldEmail(email string) (string, bool) {
	if !printableHeaderValue(email, maxHeaderTokenBytes) {
		return "", false
	}
	return strings.ToLower(email), true
}

// verifyRun checks the run's budget identity and its liveness.
func (a *Authenticator) verifyRun(ctx context.Context, tok string, now time.Time) (Identity, error) {
	head, claims, err := decodeJWT(tok)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: the run identity is not a JWT", ErrUnverified)
	}
	if alg := claimString(head, "alg"); alg != "RS256" {
		return Identity{}, fmt.Errorf("%w: the run identity is not signed with RS256", ErrUnverified)
	}
	key, err := a.cfg.Keys.Key(ctx, claimString(head, "kid"))
	if err != nil {
		if errors.Is(err, ErrUnknownKeyID) {
			// A kid Google does not publish is a refusal of the token, not a
			// service failure. The key source's own message is what says so,
			// and it names no kid: the kid is the caller's text, and the
			// package doc's no-echo rule is that no refusal repeats it.
			return Identity{}, fmt.Errorf("%w: %w", ErrUnverified, err)
		}
		return Identity{}, err
	}
	if err := verifySignature(tok, key); err != nil {
		return Identity{}, fmt.Errorf("%w: the run identity's signature does not verify", ErrUnverified)
	}
	if aud := claimString(claims, "aud"); aud != a.cfg.FirebaseProject {
		return Identity{}, fmt.Errorf("%w: the run identity was issued for another Firebase project", ErrUnverified)
	}
	if iss := claimString(claims, "iss"); iss != "https://securetoken.google.com/"+a.cfg.FirebaseProject {
		return Identity{}, fmt.Errorf("%w: the run identity was not issued by this project's Firebase", ErrUnverified)
	}
	if exp, ok := claimInt(claims, "exp"); !ok || !now.Before(time.Unix(exp, 0)) {
		return Identity{}, fmt.Errorf("%w: the run identity's ID token has expired", ErrUnverified)
	}
	if iat, ok := claimInt(claims, "iat"); ok && time.Unix(iat, 0).After(now.Add(clockSkew)) {
		return Identity{}, fmt.Errorf("%w: the run identity's ID token is issued in the future", ErrUnverified)
	}

	// The uid names the run, and the claims must agree with it: a token for
	// one run may never be read as another's.
	//
	// Both of the claims that carry the uid are required, and they must be the
	// same value: sub is the uid the rules honour (auth.uid) and user_id is
	// the copy Firebase writes beside it. Reading whichever one is present
	// would admit a token that names the run in one claim and nothing (or
	// something else) in the other, so a missing claim is a refusal rather
	// than a fallback.
	sub, uid := claimString(claims, "sub"), claimString(claims, "user_id")
	if sub == "" || uid == "" {
		return Identity{}, fmt.Errorf("%w: the run identity carries no uid (sub and user_id are both required)", ErrUnverified)
	}
	if sub != uid {
		return Identity{}, fmt.Errorf("%w: the run identity names two different uids", ErrUnverified)
	}
	slug, run, ok := splitRunUID(sub)
	if !ok {
		return Identity{}, fmt.Errorf("%w: the run identity's uid is not r~<slug>~<run>", ErrUnverified)
	}
	if claimString(claims, "fs") != slug || claimString(claims, "fr") != run {
		return Identity{}, fmt.Errorf("%w: the run identity's claims name another run than its uid", ErrUnverified)
	}
	if fp := claimString(claims, "fp"); fp != a.cfg.Project {
		return Identity{}, fmt.Errorf("%w: the run identity names another Fugaro project", ErrUnverified)
	}
	fxMS, ok := claimInt(claims, "fx")
	if !ok {
		return Identity{}, fmt.Errorf("%w: the run identity carries no fx", ErrUnverified)
	}
	fx := time.UnixMilli(fxMS).UTC()
	if !fx.After(now) {
		return Identity{}, fmt.Errorf("%w: the run identity's fx has passed", ErrUnverified)
	}
	if err := a.checkLive(ctx, slug, run, now); err != nil {
		return Identity{}, err
	}
	return Identity{
		Slug:     slug,
		Run:      run,
		Project:  a.cfg.Project,
		FX:       fx,
		Launcher: claimString(claims, "rb"),
	}, nil
}

// checkLive refuses a run whose write-once outcome exists on any day from the
// day below its launch day to the day after this service's today: the window
// is the database's, whose clock may sit either side of this one, so every day
// an outcome can be written under is read whatever the skew (the two ends are
// argued at the loop). It is not agents/<slug>/<run>: the rules let the run's
// own token write there, so it is not evidence (W2).
func (a *Authenticator) checkLive(ctx context.Context, slug, run string, now time.Time) error {
	launch, ok := runDay(run)
	if !ok {
		return fmt.Errorf("%w: the run id names no launch time, so its outcome cannot be looked for", ErrUnverified)
	}
	today := budget.Day(now)
	if launch > today {
		// A launch day ahead of ours is this service's clock being behind the
		// minter's, not evidence that the run has not started. The scan then
		// starts a day earlier than today rather than at today: the day an
		// outcome is written under is the *database's* (the rules' DAYOK allows
		// today or yesterday, and todayExpr is the rules server's now), so a
		// run id minted a day ahead of our clock can have its outcome on the
		// day our clock calls yesterday — and a scan clamped to today alone
		// would look at one of the two days an outcome can be written on and
		// admit a run that has ended (review finding m1). Widening the window
		// can only find an outcome that is there: the outcome is written once
		// and never removed, so an extra day read can never admit a live run.
		launch = today - 1
	}
	if today-launch > maxRunDays {
		return fmt.Errorf("%w: the run id names a launch older than any identity's fx can reach", ErrUnverified)
	}
	// The scan ends one day ABOVE today, not at it: the day an outcome is
	// written under is the database's (the rules' todayExpr is the rules
	// server's now), and the database's clock can be ahead of this service's
	// just as it can be behind, so a run that ended moments before the
	// database's midnight writes its outcome under a day this clock still calls
	// tomorrow. A scan that stopped at today would admit that run (review nit).
	// Reading the extra day can only find an outcome that is there — it is
	// written once and never removed — so it can never admit a live run, the
	// same argument that widens the window at the launch end above.
	//
	// The scan ends one day BELOW the launch day for the same reason read the
	// other way: the launch day is the run id's, but the day the outcome
	// carries is the database's, and DAYOK admits the database's today or its
	// YESTERDAY — so a run launched today, whose outcome is written as the
	// database's yesterday (the slot DAYOK keeps for a run that spans the
	// database's midnight, and the day a clock behind this one puts a
	// launch-day end on), lands on the day below its own launch day. The end
	// itself is within a day of the launch (fx, and maxRunDays here), so the
	// day below the launch day is the lowest day an outcome of this run can
	// carry and the scan stops there. A scan that stopped at the launch day
	// would admit that ended run, and the write-once revocation W2 rests on
	// cannot have that hole. Reading the extra day can only find an outcome
	// that is there, the argument above.
	for day := today + 1; day >= launch-1; day-- {
		exists, err := a.cfg.Outcomes.OutcomeExists(ctx, day, slug, run)
		if err != nil {
			return fmt.Errorf("%w: the run's outcome could not be read", ErrUnavailable)
		}
		if exists {
			return fmt.Errorf("%w: the run has ended: its outcome is written", ErrUnverified)
		}
	}
	return nil
}

// runDay is the UTC day a run id was minted. The rule the id is held to is
// internal/task's (ValidRunID, the shape the launcher's NewRunID mints and the
// shape internal/runstore reads), so a string that is not a run id is refused
// whole rather than half-read: fifteen bytes that happen to parse as a
// timestamp are not a run id. The day is then parsed from the id's head here
// rather than through internal/runstore's RunTime, which would pull a blob
// store into the service's binary for those fifteen bytes.
func runDay(run string) (int64, bool) {
	if !task.ValidRunID(run) {
		return 0, false
	}
	t, err := time.Parse("20060102-150405", run[:15])
	if err != nil {
		return 0, false
	}
	return budget.Day(t), true
}

// splitRunUID splits r~<slug>~<run>. Both parts must be the segments the
// launcher's own minter accepts (token.ValidSegment: no '~', no '/', no
// control, no space), because they become a database path
// (budget.PathOutcome) and an audit line, and the rule for what may be in
// them is one rule, held in one place.
func splitRunUID(uid string) (slug, run string, ok bool) {
	rest, found := strings.CutPrefix(uid, "r~")
	if !found {
		return "", "", false
	}
	slug, run, found = strings.Cut(rest, "~")
	if !found || !token.ValidSegment(slug) || !token.ValidSegment(run) {
		return "", "", false
	}
	return slug, run, true
}

// decodeJWT decodes a compact JWT's header and payload WITHOUT verifying it.
// It is a decoder and nothing else: the proof a token needs is the caller's to
// demand, and both callers demand it. verifyRun holds the run identity's RS256
// signature against the keys Google publishes; verifyJob holds the caller's ID
// token to the same RS256 check against JobKeys, on top of the Cloud Run IAM
// admission that got the call here at all. Nothing this returns is trusted on
// its own.
func decodeJWT(tok string) (head, claims map[string]any, err error) {
	if tok == "" || len(tok) > maxTokenBytes {
		return nil, nil, errors.New("credsvc: not a JWT of a usable size")
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, nil, errors.New("credsvc: not a compact JWT")
	}
	if head, err = decodeSegment(parts[0]); err != nil {
		return nil, nil, err
	}
	if claims, err = decodeSegment(parts[1]); err != nil {
		return nil, nil, err
	}
	return head, claims, nil
}

// decodeSegment decodes one JWT segment: base64url, then exactly one JSON
// object. A segment that carries anything after the object is refused rather
// than half-read (review nit): a token whose payload is one claim set and then
// another is not a token whose claims are the ones that were signed, and the
// decoder would otherwise hand back the first and drop the rest without a word.
//
// "Anything after the object" is asked with Token, not with More: More reports
// whether the next non-space byte is not ']' or '}', so a segment ending in a
// closing brace or bracket — the first claim set followed by `}` — reads as
// nothing left. Token returns io.EOF exactly when only whitespace remains.
func decodeSegment(s string) (map[string]any, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, errors.New("credsvc: a JWT segment is not base64url")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, errors.New("credsvc: a JWT segment is not a JSON object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("credsvc: a JWT segment carries more than one JSON value")
	}
	return out, nil
}

// verifySignature checks a compact JWT's RS256 signature against key.
func verifySignature(tok string, key *rsa.PublicKey) error {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return errors.New("credsvc: not a compact JWT")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return errors.New("credsvc: the signature is not base64url")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig)
}

func claimString(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func claimInt(m map[string]any, k string) (int64, bool) {
	switch v := m[k].(type) {
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case float64:
		return int64(v), true
	}
	return 0, false
}

// GoogleKeysURL is where Google publishes the certificates that sign Firebase
// ID tokens (a run's identity).
const GoogleKeysURL = "https://www.googleapis.com/robot/v1/metadata/x509/securetoken@system.gserviceaccount.com"

// GoogleOAuthCertsURL is where Google publishes the certificates that sign its
// own ID tokens (a caller's identity). It is the same document shape as
// GoogleKeysURL, so AuthConfig.JobKeys is a GoogleKeys pointed here.
const GoogleOAuthCertsURL = "https://www.googleapis.com/oauth2/v1/certs"

// GoogleKeys is the KeySource over Google's published certificates, cached
// for as long as Google's own Cache-Control says (clamped to a minute and a
// day). The set is one document, so it is fetched whole and the lock is held
// across the fetch: two calls arriving on a cold instance wait for one fetch
// rather than racing two.
type GoogleKeys struct {
	url     string
	hc      *http.Client
	now     func() time.Time
	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	expires time.Time
	fetched bool
	// forced is when this source last read the set outside the cache's life,
	// because a token named a kid the cached set did not hold (Key). It is
	// rate-limited by keyCacheMin.
	forced time.Time
}

// GoogleKeysOption configures GoogleKeys.
type GoogleKeysOption func(*GoogleKeys)

// WithKeysEndpoint points the key source at another URL (tests).
func WithKeysEndpoint(u string) GoogleKeysOption { return func(g *GoogleKeys) { g.url = u } }

// WithKeysHTTPClient sets the HTTP client (tests).
func WithKeysHTTPClient(c *http.Client) GoogleKeysOption { return func(g *GoogleKeys) { g.hc = c } }

// WithKeysClock sets the clock the cache's life is measured with (tests).
func WithKeysClock(now func() time.Time) GoogleKeysOption { return func(g *GoogleKeys) { g.now = now } }

// NewGoogleKeys returns the key source over Google's published set.
func NewGoogleKeys(opts ...GoogleKeysOption) *GoogleKeys {
	g := &GoogleKeys{url: GoogleKeysURL, hc: &http.Client{Timeout: 10 * time.Second}, now: time.Now}
	for _, o := range opts {
		o(g)
	}
	return g
}

// Key returns the public key for kid, reading Google's set when it is not
// cached. A kid the published set does not hold is ErrUnknownKeyID; a set
// that could not be read is ErrUnavailable.
//
// The kid is the caller's own text — it comes out of a token this service was
// handed, and may be any length and any bytes — so no error here names it.
// The refusal says what is wrong with the token, never what the token said:
// an error from this function reaches the refusal body, a halt message and an
// audit line, and none of those is a place for a value a caller chose. An
// operator diagnosing a rotation reads the refusal type, and a kid worth
// keeping belongs in C1.13's audit line, through internal/safetext.
//
// A kid that is not in the cached set is also the one signal that Google has
// rotated its keys, so the set is read once more before the token is refused:
// without that, a rotation would refuse every run identity and every caller
// until the cache's life ran out, which is up to a day. The extra read is
// rate-limited to one per keyCacheMin, so a caller naming random kids cannot
// make this service read Google's endpoint in a loop.
func (g *GoogleKeys) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if kid == "" {
		return nil, fmt.Errorf("%w: the token names no key id", ErrUnknownKeyID)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.fetched || !g.now().Before(g.expires) {
		if err := g.fetch(ctx); err != nil {
			return nil, err
		}
	}
	if key, ok := g.keys[kid]; ok {
		return key, nil
	}
	if g.now().Sub(g.forced) >= keyCacheMin {
		g.forced = g.now()
		if err := g.fetch(ctx); err != nil {
			return nil, err
		}
		if key, ok := g.keys[kid]; ok {
			return key, nil
		}
	}
	return nil, fmt.Errorf("%w: the token names a key id Google does not publish", ErrUnknownKeyID)
}

// fetch reads and parses the published set, and installs it. Nothing is
// installed on error: a failed fetch leaves the previous set (and its expiry)
// alone.
func (g *GoogleKeys) fetch(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.url, nil)
	if err != nil {
		// A URL this code cannot build a request for is the key set being
		// unreadable, which is ErrUnavailable and not an untyped error: every
		// caller of Key switches on that sentinel to tell a service failure
		// from a refusal of the token (review nit).
		return fmt.Errorf("%w: Google's key endpoint is not a usable URL", ErrUnavailable)
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: reading Google's public keys failed", ErrUnavailable)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxKeysBytes))
	if err != nil {
		return fmt.Errorf("%w: reading Google's public keys failed", ErrUnavailable)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: Google's key endpoint answered %d", ErrUnavailable, resp.StatusCode)
	}
	var published map[string]string
	if err := json.Unmarshal(body, &published); err != nil {
		return fmt.Errorf("%w: Google's key set is not JSON", ErrUnavailable)
	}
	keys := make(map[string]*rsa.PublicKey, len(published))
	for kid, cert := range published {
		key, err := rsaKeyFromCertificate([]byte(cert))
		if err != nil {
			return fmt.Errorf("%w: a published certificate could not be read", ErrUnavailable)
		}
		keys[kid] = key
	}
	if len(keys) == 0 {
		return fmt.Errorf("%w: Google published no keys", ErrUnavailable)
	}
	g.keys, g.fetched = keys, true
	g.expires = g.now().Add(cacheLife(resp.Header.Get("Cache-Control")))
	return nil
}

// cacheLife is how long Google's set may be cached: its own max-age, clamped
// to a minute and a day, an hour when it says nothing.
//
// A directive that says not to store the set at all — no-store, no-cache, or
// a max-age of zero — is not a licence to fall back to the hour: it means the
// shortest life this code allows, which is the floor. So every directive is
// taken at its word, and the clamp decides. A max-age this code cannot read
// is the one thing ignored, because there is nothing to take at its word.
//
// The directives are read in the order the header spells them, so a max-age
// later in the same header must not undo one that says the set may not be
// kept: `no-store, max-age=3600` is still `no-store`, and the decision is
// made once, after the whole header has been read.
func cacheLife(cacheControl string) time.Duration {
	life := keyCacheDefault
	mayKeep := true
	for _, part := range strings.Split(cacheControl, ",") {
		part = strings.TrimSpace(part)
		if part == "no-store" || part == "no-cache" {
			mayKeep = false
			continue
		}
		value, ok := strings.CutPrefix(part, "max-age=")
		if !ok {
			continue
		}
		secs, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		life = time.Duration(secs) * time.Second
	}
	if !mayKeep {
		life = 0
	}
	return min(max(life, keyCacheMin), keyCacheMax)
}

// rsaKeyFromCertificate reads one PEM certificate of the published set.
func rsaKeyFromCertificate(cert []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(cert)
	if block == nil {
		return nil, errors.New("credsvc: not PEM")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("credsvc: the certificate holds no RSA key")
	}
	return key, nil
}
