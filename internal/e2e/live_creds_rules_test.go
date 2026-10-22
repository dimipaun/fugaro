//go:build live

// The live credentials-rules check the credentials-service plan numbers 92:
// what the rules deployed on a real installation do with a run's own identity
// around /creds and around the credentials children the cascade writes (task
// C1.4; C1.5 writes the check's section into docs/gcp-live-checklist.md).
//
// It is the owner's, at the owner's terminal, against one installation, and it
// costs nothing: no model call, no image, no bucket. It is what C1.5's section
// runs:
//
//	FUGARO_LIVE_CREDS_RULES_OK=1 \
//	FUGARO_LIVE_INSTALLATION=fugaro \
//	FUGARO_LIVE_RTDB_URL=https://fugaro-dev-default-rtdb.firebaseio.com \
//	FUGARO_LIVE_FIREBASE_API_KEY=<the restricted web API key> \
//	FUGARO_LIVE_TOKEN_SIGNER=<the token-signer service account email> \
//	  go test -tags live -run TestLiveCredsRules -v ./internal/e2e/
//
// with `gcloud auth application-default login` holding the firebase.database,
// userinfo.email and cloud-platform scopes and your user holding
// fugaroTokenMinter on the signer and firebaseauth.users.delete on the project
// — the same variables and credentials as check 21's helper
// (FUGARO_LIVE_REQUESTED_BY is optional and is the rb claim of the identities
// it mints). It skips, rather than fails, unless the environment aims it at a
// database, so `go test -tags live ./internal/e2e/` stays usable on a machine
// that is not running the check; an environment that *is* aimed at a database
// without FUGARO_LIVE_CREDS_RULES_OK=1 is refused.
//
// It is pinned to the sandbox. crTargetFrom (creds_rules_guard_test.go, no
// build tag, covered by ordinary tests) refuses, before a single request: an
// aimed run from a coding agent's session (F33 — an unaimed one skips like any
// other unaimed run, so `-run TestLive` at an agent's or an IDE's terminal is
// a skip, not a failure); any installation but `fugaro`; any database
// host but the sandbox project's, checked with rtdb.ValidateURL as well, so
// the owner's credential can never be sent to `evil.example.com` or to a
// `.firebaseio.co` look-alike; any signer that is not a service account of the
// sandbox project; and a URL carrying userinfo, a path, a query or a fragment.
// The owner's ruling F33 is the authority: "every live step is owner-run, on
// installation `fugaro` and a sandbox repository only, never `belong`".
//
// What it writes, and what survives. One scratch repository slug
// (live92-<hex>-rules), two throwaway run ids, three scratch credential names:
// the registry entries of both runs and their new children, the by-tier
// counters under them, the nodes under /creds, and — as the owner, because
// D166's USER conjunct reads it — one device allowlist node for a scratch
// phone uid. Every one of them is deleted as the owner — by a cleanup
// registered before the first write, so a failure halfway through still cleans
// up — and the deletion is read back and asserted. The Firebase Auth users the
// exchanges create (each identity's uid) are deleted too, and the project's
// user list is read back to prove it. No project-wide counter is written at
// all: the reservation a positive by-tier write on the run's *own* ledger
// would need is a multi-path write into the global `counted` counters, so the
// check refuses to perform it and writes the zero the leaf accepts (see
// crCases). The two positive counter writes are under the scratch
// repository's own day node: its tier-B and its tier-C leaf.
//
// It never prints a secret. The custom tokens, the ID tokens, the refresh
// tokens and the web API key are registered with the FACT recorder as they are
// created, every error it prints is scrubbed through the same list, and the
// FACT lines it has collected are checked against those secrets before the
// test ends. A database URL is never printed at all — only its pinned host:
// a URL can carry a credential in its userinfo or its query.
//
// It works in the database's day, not this machine's: DAYOK compares $day with
// the server's TODAY and YESTERDAY, so the day comes from the owner client's
// ServerNow() — the same source budget.Session reads it from, filled by the
// check's own first read, so it costs no request of its own. A machine whose
// day differs from the database's is the day boundary, not a failure: the
// check says so in a FACT line and goes on in the database's day. A machine
// whose *clock* is minutes off is refused before anything is written, because
// the rules judge a run token's fx by the database's clock while
// token.Exchange judges the custom token's exp by this machine's.
//
// The accepted writes are what makes the refusals mean anything: a database
// that refused everything (a dead URL, a token the rules do not honour) would
// fail this check on its positive controls, not pass it. Every identity the
// check mints has one: the run's own identity by the accepted cases of the
// table, and each of the five other families (another repository's run,
// another run of this repository, a phone, a consumer, a pairing session) by
// the claim its ID token must carry and, where the rules grant it a read, by
// that read.
//
// Every answer is logged as a FACT: line, to paste into the checklist.
package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/budget/rules"
	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// crScopes are the scopes the owner's Application Default Credentials need:
// the same two check 21's helper uses, because this check makes the same two
// calls (the REST API as the owner, signJwt as the signer).
var crScopes = []string{"https://www.googleapis.com/auth/firebase.database", "https://www.googleapis.com/auth/userinfo.email"}

// crAuthScope is the scope the owner's credential needs for the one
// administrative call this check makes: deleting the Firebase Auth users its
// own exchanges created (budget.AuthAdmin, the API the history job's sweeper
// uses).
const crAuthScope = "https://www.googleapis.com/auth/cloud-platform"

// crFacts records every FACT: line the check prints, so that the lines the
// owner pastes can be checked against the secrets the run handled before the
// test ends.
type crFacts struct {
	t       *testing.T
	lines   []string
	secrets []string
}

func (f *crFacts) fact(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	f.lines = append(f.lines, line)
	f.t.Log("FACT: " + line)
}

// secret registers a value that must never appear in a FACT line or in an
// error the check prints. token.Config.Register's callback is behind this, so
// every token the exchange mints (the custom token, the ID token, the refresh
// token) and the web API key are registered as they are created.
func (f *crFacts) secret(s string) {
	if s != "" {
		f.secrets = append(f.secrets, s)
	}
}

// errText is an error's text with every registered secret replaced: the token
// package's URL carries the web API key, and a transport error quotes the URL.
func (f *crFacts) errText(err error) string {
	if err == nil {
		return ""
	}
	return crScrub(err.Error(), f.secrets)
}

// checkSecrets reports one error per FACT line that carries a secret. It is a
// method so the check runs on the lines the whole test collected, including
// the ones the cleanup emits after the body is done.
func (f *crFacts) checkSecrets() {
	for _, line := range crSecretsIn(f.lines, f.secrets) {
		f.t.Error(line)
	}
}

// TestLiveCredsRules is check 92's helper: it proves on a live installation
// that the deployed document denies a run's identity every read and every
// write under /creds, accepts its own well-formed writes of the credentials
// children the cascade adds, and refuses everything else those children admit.
func TestLiveCredsRules(t *testing.T) {
	target, err := crTargetFrom(os.Getenv)
	if errors.Is(err, crSkip) {
		t.Skip("the credentials-service plan's check 92 is yours, against the sandbox installation: set " + crOKVar + "=1 and the variables this file's comment names")
	}
	if err != nil {
		t.Fatal(err)
	}
	facts := &crFacts{t: t}
	// Registered first so it runs last: the cleanup's own FACT lines are
	// checked against the secrets too.
	defer facts.checkSecrets()
	ctx := context.Background()
	now := time.Now()

	owner, err := crOwner(ctx, target)
	if err != nil {
		t.Fatal(facts.errText(err))
	}

	// 1. The installation, stated twice: what the owner said, and what the
	// database says. The rules compare fp with /fugaro/project, so a
	// disagreement here would make every case below fail for a reason that has
	// nothing to do with what the check is about. crTargetFrom has already
	// pinned the owner's side to the sandbox; this is the database's.
	install, err := crInstall(ctx, facts, owner)
	if err != nil {
		t.Fatal(err)
	}
	if install != target.installation {
		t.Fatalf("the database at %s names the installation %q, but the check is pinned to %q: the sandbox's database is the only one this check may write to (F33), and the rules compare every run token's fp claim with %s exactly", target.host, install, crSandboxInstall, budget.PathProject)
	}
	facts.fact("the database at %s names the installation %q, the sandbox installation this check is pinned to (%s)", target.host, install, crSandboxInstall)

	// 2. The database's clock, which is the one the rules judge by: DAYOK
	// compares $day with the server's TODAY and YESTERDAY, and the RUN macro
	// compares a run token's fx with the server's now. The owner client's clock
	// is filled by the read above, so this costs no request of its own — it is
	// the same source budget.Session reads the day from.
	serverNow, ok := owner.ServerNow()
	if !ok {
		t.Fatal("the database has answered nothing yet, so the owner client has no clock reading: the check cannot know which day DAYOK will judge by")
	}
	if crClockTooFar(serverNow, now) {
		t.Fatalf("this machine's clock is %s from the database's (%s against %s): fix the clock and run again — the rules judge a run token's fx with the database's clock while token.Exchange judges the custom token's exp with this machine's, so a clock this far off would fail every case below for a reason that is not the rules",
			serverNow.Sub(now).Round(time.Second), now.UTC().Format(time.RFC3339), serverNow.UTC().Format(time.RFC3339))
	}
	day := budget.Day(serverNow)
	if day != budget.Day(now) {
		// Not a failure: near midnight the two days differ while the clocks
		// agree, and which side of the boundary a write lands on is the
		// database's to say. The rtdb package documents the same thing as a
		// retry with the fresh day rather than a refusal.
		facts.fact("this machine's day is %d and the database's is %d: the check works in the database's day, as DAYOK does", budget.Day(now), day)
	}
	n := crNewNames(now, day)
	// Every Firebase Auth user this check's exchanges can create, registered
	// before the first exchange is made: an Exchange can create the account and
	// then fail (a refused adopt, a claim the ID token does not carry), and a
	// uid learned from a session that came back would be learned too late —
	// crAuthUsers.run looks for the uids it knows, so the survivor would not
	// even be reported. crAuthUIDs derives them from the names alone; the
	// users.add calls below stay, and are no-ops, because a session whose uid
	// is not in this set would mean the derivation is wrong.
	users := &crAuthUsers{uids: crAuthUIDs(n)}

	// The cleanups are registered before the first write and are idempotent: a
	// failure anywhere below still leaves the installation as it was found,
	// and the users it created out of the project.
	clean := &crCleanup{paths: crWritten(n)}
	defer clean.run(t, target, facts)
	defer users.run(t, facts)

	facts.fact("the check works under two scratch repositories (slugs %s and %s, runs %s and %s) and three scratch credential names (%s, %s, %s), in the database's day %d: no real repository's counters and no real credential's mark are touched", n.slug, n.otherSlug, n.run, n.otherRun, n.n1, n.n2, n.n3, n.day)

	// 3. The two generation markers, as the owner: the credentials document
	// has to be deployed, or every case below would be about another document.
	credsMarker, err := crMarker(ctx, facts, owner, rules.PathCredsRulesMarker())
	if err != nil {
		t.Fatal(err)
	}
	if credsMarker != rules.CredsRulesVersion {
		t.Fatalf("%s reads %d, want %d: the credentials rules are not deployed on this installation, so nothing below would be about them (run `fugaro update --firebase-project` from a release that deploys them)", rules.PathCredsRulesMarker(), credsMarker, rules.CredsRulesVersion)
	}
	policyMarker, err := crMarker(ctx, facts, owner, projectpolicy.PathRulesMarker())
	if err != nil {
		t.Fatal(err)
	}
	facts.fact("%s reads %d (this release's rules.CredsRulesVersion) and %s reads %d (projectpolicy.RulesVersion is %d)",
		rules.PathCredsRulesMarker(), credsMarker, projectpolicy.PathRulesMarker(), policyMarker, projectpolicy.RulesVersion)
	if policyMarker != projectpolicy.RulesVersion {
		t.Errorf("%s reads %d, want %d: the deployed document is not the one this release generates, so what the cases below prove is another document's behaviour (check 82 is the deploy's own check)", projectpolicy.PathRulesMarker(), policyMarker, projectpolicy.RulesVersion)
	}

	// 4. The run's identity, minted and exchanged as the launcher does.
	signer, err := crSigner(ctx, target, facts)
	if err != nil {
		t.Fatal(err)
	}
	custom, err := crMint(ctx, facts, signer, target, n.slug, n.run, now)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := crExchange(ctx, facts, target, custom)
	if err != nil {
		t.Fatal(err)
	}
	users.add(sess.UID())
	run, err := rtdb.New(target.rtdbURL, rtdb.Auth{IDToken: sess.Token})
	if err != nil {
		t.Fatal(facts.errText(err))
	}
	if c := sess.Claims(); c.Slug != budget.Key(n.slug) || c.Run != budget.Key(n.run) || c.FP != install || c.RB != target.requestedBy || c.FX.IsZero() {
		t.Errorf("the identity carries %+v, want fs=%s fr=%s fp=%s rb=%s and an fx", c, budget.Key(n.slug), budget.Key(n.run), install, target.requestedBy)
	}
	facts.fact("the exchange produced the identity %s (expires %s), carrying fs/fr/fx/fp/rb as minted, with no sign-in provider enabled and the restricted web key", sess.UID(), sess.Expiry().UTC().Format(time.RFC3339))

	// 5. The markers as the run: a cascade launch reads them with the run's own
	// identity (C1.19), and neither is a run's to write.
	maxResPath := budget.PathLimits + "/maxReserveMicros"
	maxRes, err := crMaxReserve(ctx, facts, run)
	if err != nil {
		t.Fatalf("%s", facts.errText(err))
	}
	facts.fact("the run's own identity read %s (=%d micro-dollars, the bound every counter leaf carries)", maxResPath, maxRes)
	if runCredsMarker, err := crMarker(ctx, facts, run, rules.PathCredsRulesMarker()); err != nil {
		t.Errorf("a run's identity could not read %s: %s", rules.PathCredsRulesMarker(), facts.errText(err))
	} else {
		facts.fact("the run's own identity read %s (=%d, the gate a cascade launch reads before it starts)", rules.PathCredsRulesMarker(), runCredsMarker)
	}

	// 6. The table, and its own safety invariants, before the first write.
	cases := crCases(n, maxRes, target.requestedBy)
	if err := crCheckCases(cases, budget.Key(n.slug), budget.Key(n.otherSlug)); err != nil {
		t.Fatalf("the case table is not safe to run: %v", err)
	}

	// 7. Seed, as the owner, what the denials are proven against: a read that
	// came back empty because there was nothing to read would not be a denial.
	if err := crSeed(t, ctx, facts, owner, n, now); err != nil {
		t.Fatal(err)
	}
	facts.fact("seeded as the owner: %s, and the device node %s the phone's own identity needs (D166)", strings.Join(crCredsPaths(n).seeded(), ", "), crDevicePath(crPhoneUID(n)))

	// 8. Every read a run's identity must not be able to make under /creds.
	reads := crCredsReads(n)
	if denied := crDeniedReads(t, ctx, facts, run, reads); denied != len(reads) {
		t.Errorf("%d of %d reads under /creds were refused; every one must be", denied, len(reads))
	} else {
		facts.fact("a run's identity read nothing under /creds: %d reads refused (permission denied), including the marks, the leases, its own sticky credential and the round-robin counter", denied)
	}

	// 9. Every write: accepted where the run's own identity must be able to
	// write, refused everywhere else, with the node read back as the owner
	// before and after each attempt.
	accepted, refused := crRunCases(t, ctx, facts, owner, run, cases)
	for _, c := range accepted {
		facts.fact("the rules accepted /%s = %s (%s; %s)", c.path, crJSON(c.value), c.name, c.why)
	}
	if len(refused) > 0 {
		facts.fact("the rules refused %d writes a run's identity must not make: %s", len(refused), crCaseNames(refused))
	}

	// 10. The denial is the node's, not the run's: the five other identities
	// the emulator suite walks (another repository's run, another run of this
	// repository, a phone, a consumer, a pairing session) are refused exactly
	// as this run is, and each one reaches the family the rules do grant it, so
	// that a refusal cannot be the answer of a token the rules ignore.
	if err := crOtherIdentities(t, ctx, facts, target, signer, owner, n, now, users); err != nil {
		t.Error(err)
	}

	// 11. The launcher's rb: the entry's children are tied to the launcher the
	// entry names (RBOK), not to the run. The other launcher's own run writes
	// its own entry's children; a token of *this* run carrying the other
	// launcher's rb is refused this run's credential child and then, once it
	// has named itself in the entry, is allowed it. The accepted writes above
	// are what makes the refusals mean something; this pair is what makes them
	// mean *this run's*.
	if err := crOtherLauncher(t, ctx, facts, target, n, owner, now, users); err != nil {
		t.Error(err)
	}
}

// crOwner is the client the owner's own credentials give: Application Default
// Credentials with the Firebase database and userinfo scopes, over IAM, which
// bypasses the rules exactly as it does for `fugaro update`.
func crOwner(ctx context.Context, target crTarget) (*rtdb.Client, error) {
	ts, err := google.DefaultTokenSource(ctx, crScopes...)
	if err != nil {
		return nil, fmt.Errorf("no Application Default Credentials with the Firebase scopes (gcloud auth application-default login --scopes=https://www.googleapis.com/auth/cloud-platform,%s,%s): %w", crScopes[0], crScopes[1], err)
	}
	return rtdb.New(target.rtdbURL, rtdb.Auth{Source: ts})
}

// crSigner is the token signer, reached with the owner's ADC cloud-platform
// scope: signJwt as the signer, and no role on the signer for the signer
// itself.
func crSigner(ctx context.Context, target crTarget, facts *crFacts) (token.Signer, error) {
	ts, err := google.DefaultTokenSource(ctx, crAuthScope)
	if err != nil {
		return nil, fmt.Errorf("no Application Default Credentials for signJwt: %w", err)
	}
	s, err := token.NewIAMSigner(target.signer, ts)
	if err != nil {
		return nil, errors.New(facts.errText(err))
	}
	return s, nil
}

// crMint mints one run's custom token as the launcher would, and registers it
// as a secret before anything can print it.
func crMint(ctx context.Context, facts *crFacts, signer token.Signer, target crTarget, slug, run string, now time.Time) (string, error) {
	claims := token.Claims{Slug: budget.Key(slug), Run: budget.Key(run), FX: token.ExpiryFX(now, 2*time.Hour), FP: target.installation, RB: target.requestedBy}
	custom, err := token.MintAt(ctx, signer, claims, now)
	if err != nil {
		return "", errors.New(facts.errText(err))
	}
	facts.secret(custom)
	return custom, nil
}

// crMintClaims mints a custom token for an identity that is not a run's: the
// uid and the claims are the check's, and they are the ones the rules read for
// the mailbox and pairing families (D112, D113b, D165). token.MintAt cannot
// express one — its uid is always r~<slug>~<run> — so the payload is built
// here, with the audience and lifetime the exchange requires, and signed by
// the same signer every run token is.
func crMintClaims(ctx context.Context, facts *crFacts, signer token.Signer, id crIdentity, now time.Time) (string, error) {
	email := signer.Email()
	payload, err := json.Marshal(map[string]any{
		"iss": email, "sub": email, "aud": token.Audience,
		"iat": now.Unix(), "exp": now.Add(token.Lifetime).Unix(),
		"uid": id.uid, "claims": id.claims,
	})
	if err != nil {
		return "", errors.New("building the custom token's payload: " + err.Error())
	}
	custom, err := signer.SignJWT(ctx, payload)
	if err != nil {
		return "", errors.New(facts.errText(err))
	}
	facts.secret(custom)
	return custom, nil
}

// crExchange trades a custom token for the run's live Firebase identity. Every
// token the exchange creates is registered with the FACT recorder through
// Register, so none of them can reach a FACT line or a printed error.
func crExchange(ctx context.Context, facts *crFacts, target crTarget, custom string) (*token.Session, error) {
	facts.secret(target.apiKey)
	sess, err := token.Exchange(ctx, token.Config{APIKey: target.apiKey, Register: facts.secret}, custom)
	if err != nil {
		return nil, fmt.Errorf("exchanging the custom token: %s", facts.errText(err))
	}
	facts.secret(sess.Token())
	return sess, nil
}

// crIdentitySession mints one of the five other identities and exchanges it.
// It returns the session and the claims its ID token carries, which the caller
// asserts: an identity the exchange did not really produce would be refused
// /creds for a reason that is not the rules.
func crIdentitySession(ctx context.Context, facts *crFacts, target crTarget, signer token.Signer, id crIdentity, now time.Time) (*token.Session, map[string]any, error) {
	var custom string
	var err error
	if id.uid == "" {
		custom, err = crMint(ctx, facts, signer, target, id.slug, id.run, now)
	} else {
		custom, err = crMintClaims(ctx, facts, signer, id, now)
	}
	if err != nil {
		return nil, nil, err
	}
	sess, err := crExchange(ctx, facts, target, custom)
	if err != nil {
		return nil, nil, err
	}
	claims, err := crIDTokenClaims(sess.Token())
	if err != nil {
		return nil, nil, fmt.Errorf("%s: reading the ID token's claims: %v", id.name, err)
	}
	return sess, claims, nil
}

// crInstall reads /fugaro/project: the installation name every run token's fp
// claim must equal.
func crInstall(ctx context.Context, facts *crFacts, owner *rtdb.Client) (string, error) {
	var install string
	found, err := owner.Get(ctx, budget.PathProject, &install)
	if err != nil {
		return "", fmt.Errorf("reading %s as the owner: %s", budget.PathProject, facts.errText(err))
	}
	if !found || install == "" {
		return "", fmt.Errorf("%s is not set: the database was not initialised by `fugaro update --firebase-project`", budget.PathProject)
	}
	if strings.EqualFold(install, "belong") {
		return "", fmt.Errorf("%s names the 'belong' installation: this check never touches it (D147, F33)", budget.PathProject)
	}
	return install, nil
}

// crMarker reads one generation marker with c's identity.
func crMarker(ctx context.Context, facts *crFacts, c *rtdb.Client, path string) (int, error) {
	var v int
	found, err := c.Get(ctx, path, &v)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %s", path, facts.errText(err))
	}
	if !found {
		return 0, fmt.Errorf("%s is not set: the deploy that writes it has not run, or the rules under it are older than it", path)
	}
	return v, nil
}

// crMaxReserve reads the largest lease the counter leaves are bounded by, with
// the run's own identity: it is under config/limits, which every run token of
// the project reads. It has to be there: every installation `fugaro update
// --firebase` writes it, and the leaves the check writes a positive amount to
// are bounded by it, so its absence would turn those writes into refusals for
// a reason that is not what this check is about.
func crMaxReserve(ctx context.Context, facts *crFacts, run *rtdb.Client) (int64, error) {
	var v int64
	found, err := run.Get(ctx, budget.PathLimits+"/maxReserveMicros", &v)
	if err != nil {
		return 0, fmt.Errorf("reading %s/maxReserveMicros as the run: %s", budget.PathLimits, facts.errText(err))
	}
	if !found {
		return 0, fmt.Errorf("%s/maxReserveMicros is not set: the counter leaves are bounded by it, and an installation without it can lease nothing (an operator runs `fugaro update --firebase-project` again)", budget.PathLimits)
	}
	return v, nil
}

// crSeed writes, as the owner, the nodes a denial has to be proven against.
// The device allowlist node is the owner's too (D166: no client may write it),
// and it is what makes the phone's denial a denial *of /creds* rather than of
// everything.
func crSeed(t *testing.T, ctx context.Context, facts *crFacts, owner *rtdb.Client, n crNames, now time.Time) error {
	t.Helper()
	cp := crCredsPaths(n)
	mark := crCredsMark(now)
	updates := map[string]any{
		cp.mark1:                    mark,
		cp.mark2:                    mark,
		cp.lease:                    map[string]any{"credential": n.n1, "expires": now.Add(time.Minute).UnixMilli()},
		cp.current:                  n.n1,
		cp.roundrobin:               3,
		crDevicePath(crPhoneUID(n)): map[string]any{"pairedAt": now.UnixMilli()},
	}
	if err := owner.Patch(ctx, "", updates); err != nil {
		return fmt.Errorf("seeding /creds and the device node as the owner: %s", facts.errText(err))
	}
	// The seed is read back: a denial is a denial, but a seed that never
	// landed would make the reads below prove nothing.
	for _, p := range append(cp.seeded(), crDevicePath(crPhoneUID(n))) {
		var raw json.RawMessage
		found, err := owner.Get(ctx, p, &raw)
		if err != nil {
			return fmt.Errorf("reading /%s back after seeding it: %s", p, facts.errText(err))
		}
		if !found {
			return fmt.Errorf("/%s is not there after the owner wrote it", p)
		}
	}
	return nil
}

// crDeniedReads attempts each path with an identity and returns how many were
// refused with a permission error. A read that succeeded, or that failed for
// any other reason, is reported.
func crDeniedReads(t *testing.T, ctx context.Context, facts *crFacts, c *rtdb.Client, paths []string) int {
	t.Helper()
	denied := 0
	for _, p := range paths {
		var raw json.RawMessage
		_, err := c.Get(ctx, p, &raw)
		switch {
		case err == nil:
			t.Errorf("an identity read /%s, which the rules must refuse", p)
		case !errors.Is(err, rtdb.ErrPermission):
			t.Errorf("reading /%s: want a permission error, got %s", p, facts.errText(err))
		default:
			denied++
		}
	}
	return denied
}

// crRunCases attempts every case with the run's own identity, reading the path
// back as the owner before and after each attempt: a refused write must have
// changed nothing, and an accepted one must have left exactly what it wrote.
func crRunCases(t *testing.T, ctx context.Context, facts *crFacts, owner, run *rtdb.Client, cases []crCase) (accepted, refused []crCase) {
	t.Helper()
	for _, c := range cases {
		if c.seed != nil {
			if err := owner.Patch(ctx, "", map[string]any{c.path: c.seed}); err != nil {
				t.Fatalf("seeding /%s as the owner before %q: %s", c.path, c.name, facts.errText(err))
			}
		}
		before, beforeFound := crRead(t, ctx, facts, owner, c.path)
		err := run.Patch(ctx, "", map[string]any{c.path: c.value})
		after, afterFound := crRead(t, ctx, facts, owner, c.path)
		switch {
		case c.allow && err != nil:
			t.Errorf("%s (/ %s, %s): the deployed rules refused a write a run's own identity must be able to make: %s", c.name, c.path, c.why, facts.errText(err))
		case c.allow && (!afterFound || !crSameJSON(after, c.value)):
			t.Errorf("%s (/ %s): the write was accepted but /%s reads %s, want %s", c.name, c.path, c.path, crJSON(after), crJSON(c.value))
		case c.allow:
			accepted = append(accepted, c)
		case err == nil:
			t.Errorf("%s (/ %s, %s): the deployed rules accepted a write a run's identity must not make; /%s now reads %s", c.name, c.path, c.why, c.path, crJSON(after))
			crUndo(t, ctx, facts, owner, c.path, before, beforeFound)
		case !errors.Is(err, rtdb.ErrPermission):
			t.Errorf("%s (/ %s): want a permission error, got %s", c.name, c.path, facts.errText(err))
		case afterFound != beforeFound || !crSameJSON(after, before):
			t.Errorf("%s (/ %s): refused, but the node changed from %s to %s", c.name, c.path, crJSON(before), crJSON(after))
		default:
			refused = append(refused, c)
		}
	}
	return accepted, refused
}

// crOtherIdentities walks the five other families the emulator suite denies
// /creds to (creds_emulator_test.go, TestCredsTokenOfAnotherRepositoryDenied).
// Each is refused every read under /creds and a write there, and each is shown
// to be an identity the rules really honour: the ID token carries the claim
// its family is read by, and where that family has a read grant, the identity
// makes it.
func crOtherIdentities(t *testing.T, ctx context.Context, facts *crFacts, target crTarget, signer token.Signer, owner *rtdb.Client, n crNames, now time.Time, users *crAuthUsers) error {
	t.Helper()
	cp := crCredsPaths(n)
	reads := crCredsReads(n)
	mark := crCredsMark(now)
	for _, id := range crIdentities(n) {
		sess, claims, err := crIdentitySession(ctx, facts, target, signer, id, now)
		if err != nil {
			return fmt.Errorf("%s: %w", id.name, err)
		}
		users.add(sess.UID())
		if got, want := claims["user_id"], sess.UID(); got != want {
			return fmt.Errorf("%s: the ID token's user_id is %v, want %s: the exchange did not produce the identity the check minted", id.name, got, want)
		}
		var control string
		for k, want := range id.expected() {
			if got := claims[k]; !crSameJSON(got, want) {
				return fmt.Errorf("%s (uid %s): the ID token's %s claim is %v, want %v: the identity is not the one the rules read", id.name, sess.UID(), k, got, want)
			}
			control = k + "=" + crJSON(claims[k])
		}
		c, err := rtdb.New(target.rtdbURL, rtdb.Auth{IDToken: sess.Token})
		if err != nil {
			return errors.New(facts.errText(err))
		}
		// The positive control: the family the rules grant this identity, read
		// before the denials, so that a refusal below cannot be the answer of a
		// token the rules ignore.
		if id.reach != "" {
			var raw json.RawMessage
			if _, err := c.Get(ctx, id.reach, &raw); err != nil {
				t.Errorf("%s (uid %s) could not read /%s, which the rules grant its family: %s", id.name, sess.UID(), id.reach, facts.errText(err))
			} else {
				control += ", read /" + id.reach
			}
		}
		// The denials: every read under /creds, and a write there.
		for _, p := range reads {
			var raw json.RawMessage
			if _, err := c.Get(ctx, p, &raw); !errors.Is(err, rtdb.ErrPermission) {
				t.Errorf("%s (uid %s) read /%s: want a permission error, got %v", id.name, sess.UID(), p, facts.errText(err))
			}
		}
		before, beforeFound := crRead(t, ctx, facts, owner, cp.mark1)
		err = c.Patch(ctx, "", map[string]any{cp.mark1: mark})
		after, afterFound := crRead(t, ctx, facts, owner, cp.mark1)
		switch {
		case err == nil:
			t.Errorf("%s (uid %s) wrote /%s, which the rules must refuse", id.name, sess.UID(), cp.mark1)
			crUndo(t, ctx, facts, owner, cp.mark1, before, beforeFound)
		case !errors.Is(err, rtdb.ErrPermission):
			t.Errorf("%s (uid %s) writing /%s: want a permission error, got %s", id.name, sess.UID(), cp.mark1, facts.errText(err))
		case afterFound != beforeFound || !crSameJSON(after, before):
			t.Errorf("%s (uid %s) was refused /%s but the node changed from %s to %s", id.name, sess.UID(), cp.mark1, crJSON(before), crJSON(after))
		}
		facts.fact("%s (uid %s, %s) was refused all %d reads under /creds and a write there", id.name, sess.UID(), control, len(reads))
	}
	return nil
}

// crOtherLauncher is the RBOK pair, and it takes two tokens to be one.
//
// The first is the other launcher's own run (n.otherRun, minted with another
// launcher's rb): it creates that run's registry entry and writes its
// credential child, which the rules accept. That is the positive control for
// the other launcher's identity — the rules honour it where it launched.
//
// The second is *this* run (n.run), minted with the same other launcher's rb.
// Its fs and fr are this run's, so RUN admits it at this run's entry and the
// entry's `.write` is granted: RBOK is the only rule left that can refuse the
// entry's credential child, and it does, because the entry names the first
// launcher. The second token then writes the entry's own requestedBy to
// itself — accepted, since that is exactly what RBOK compares — and the same
// credential write is then accepted too. The only thing that changed between
// the refusal and the acceptance is the entry's requestedBy, which is what
// makes the refusal RBOK's and not RUN's, not the token's, and not the node's.
//
// Minting the second token for n.otherRun instead would have made the refusal
// the entry node's `.write` (RUN: fr == $run), and the FACT line below would
// have recorded evidence the check never gathered.
func crOtherLauncher(t *testing.T, ctx context.Context, facts *crFacts, target crTarget, n crNames, owner *rtdb.Client, now time.Time, users *crAuthUsers) error {
	t.Helper()
	other := target
	other.requestedBy = target.requestedBy + "-other-launcher"
	signer, err := crSigner(ctx, target, facts)
	if err != nil {
		return err
	}

	// The other launcher's own run: its entry, and the entry's credential
	// child, which RBOK ties to the launcher that wrote the entry — this one.
	custom, err := crMint(ctx, facts, signer, other, n.slug, n.otherRun, now)
	if err != nil {
		return err
	}
	ownSess, err := crExchange(ctx, facts, other, custom)
	if err != nil {
		return err
	}
	users.add(ownSess.UID())
	ownClient, err := rtdb.New(target.rtdbURL, rtdb.Auth{IDToken: ownSess.Token})
	if err != nil {
		return errors.New(facts.errText(err))
	}
	own := budget.PathAgent(n.slug, n.otherRun)
	entry := map[string]any{"repo": n.repo, "auth": "cascade", "startedAt": now.UnixMilli(), "requestedBy": other.requestedBy}
	if err := ownClient.Patch(ctx, "", map[string]any{own: entry}); err != nil {
		return fmt.Errorf("the other launcher's identity could not create its own run's registry entry /%s: %s", own, facts.errText(err))
	}
	if err := ownClient.Patch(ctx, "", map[string]any{own + "/credential": n.n3}); err != nil {
		return fmt.Errorf("the other launcher's identity could not write its own entry's credential child /%s/credential: %s", own, facts.errText(err))
	}
	if got, found := crRead(t, ctx, facts, owner, own+"/credential"); !found || !crSameJSON(got, n.n3) {
		return fmt.Errorf("/%s/credential reads %s after the other launcher wrote %s", own, crJSON(got), crJSON(n.n3))
	}

	// This run, minted with the same other launcher's rb: RUN admits it here,
	// so what follows is RBOK's answer alone.
	custom, err = crMint(ctx, facts, signer, other, n.slug, n.run, now)
	if err != nil {
		return err
	}
	sess, err := crExchange(ctx, facts, other, custom)
	if err != nil {
		return err
	}
	users.add(sess.UID())
	// The token has to be what the argument below rests on: this run's fs and
	// fr, so RUN admits it here, and the other launcher's rb, so RBOK is what
	// refuses the child. Minting it for another run (n.otherRun) would make the
	// refusal the entry node's .write instead, and the FACT line would record
	// evidence the check never gathered.
	if c := sess.Claims(); c.Slug != budget.Key(n.slug) || c.Run != budget.Key(n.run) || c.RB != other.requestedBy {
		return fmt.Errorf("the RBOK pair's second token carries fs=%s fr=%s rb=%s, want fs=%s fr=%s rb=%s: it has to be this run, minted by the other launcher, or RBOK is not the rule the refusal comes from", c.Slug, c.Run, c.RB, budget.Key(n.slug), budget.Key(n.run), other.requestedBy)
	}
	second, err := rtdb.New(target.rtdbURL, rtdb.Auth{IDToken: sess.Token})
	if err != nil {
		return errors.New(facts.errText(err))
	}
	first := budget.PathAgent(n.slug, n.run)

	// The refusal: this run's credential child, which the first launcher's rb
	// ties to the first launcher.
	before, beforeFound := crRead(t, ctx, facts, owner, first+"/credential")
	err = second.Patch(ctx, "", map[string]any{first + "/credential": n.n2})
	after, afterFound := crRead(t, ctx, facts, owner, first+"/credential")
	switch {
	case err == nil:
		return fmt.Errorf("a token of this run carrying another launcher's rb (%s) wrote /%s/credential: the rules accepted a write RBOK must refuse; it now reads %s", other.requestedBy, first, crJSON(after))
	case !errors.Is(err, rtdb.ErrPermission):
		return fmt.Errorf("the other launcher's write to /%s/credential: want a permission error, got %s", first, facts.errText(err))
	case afterFound != beforeFound || !crSameJSON(after, before):
		return fmt.Errorf("the other launcher's write was refused but /%s/credential changed from %s to %s", first, crJSON(before), crJSON(after))
	}

	// The same token, the same write, the one thing changed: the entry now
	// names it. RUN granted the entry's write all along — this is the write
	// that shows it — and RBOK follows the entry.
	if err := second.Patch(ctx, "", map[string]any{first + "/requestedBy": other.requestedBy}); err != nil {
		return fmt.Errorf("a token of this run carrying another launcher's rb could not write the entry's own requestedBy to itself (/ %s/requestedBy), which RUN grants and RBOK admits once the entry names it: %s", first, facts.errText(err))
	}
	if got, found := crRead(t, ctx, facts, owner, first+"/requestedBy"); !found || !crSameJSON(got, other.requestedBy) {
		return fmt.Errorf("/%s/requestedBy reads %s after the other launcher took the entry over", first, crJSON(got))
	}
	if err := second.Patch(ctx, "", map[string]any{first + "/credential": n.n2}); err != nil {
		return fmt.Errorf("the same token that was refused /%s/credential was refused it again after the entry named it: %s — so the first refusal was not RBOK's", first, facts.errText(err))
	}
	if got, found := crRead(t, ctx, facts, owner, first+"/credential"); !found || !crSameJSON(got, n.n2) {
		return fmt.Errorf("/%s/credential reads %s after the entry named the other launcher, want %s", first, crJSON(got), crJSON(n.n2))
	}

	facts.fact("the other launcher's identity (uid %s, rb %s) created its own run's entry /%s and its credential child; a token of this run carrying the same rb (uid %s) was refused /%s/credential (RBOK: the entry named %s), then wrote the entry's requestedBy to itself and the same credential child was accepted: the entry's children follow the launcher the entry names, and RUN admits the token either way", ownSess.UID(), other.requestedBy, own, sess.UID(), first, target.requestedBy)
	return nil
}

// crUndo puts a path back the way the owner found it. It is for the one way
// this check could write something outside its scratch names: a case it
// expects the rules to refuse and a live installation accepts — a rules hole,
// or a document older than this release. The nodes the check means to write
// are deleted by crCleanup; these are the nodes it must never write at all,
// among them another repository's day counter and the two generation markers,
// which no cleanup may delete.
func crUndo(t *testing.T, ctx context.Context, facts *crFacts, owner *rtdb.Client, path string, before any, beforeFound bool) {
	t.Helper()
	value := before
	if !beforeFound {
		value = nil // the node was not there: the write created it
	}
	if err := owner.Patch(ctx, "", map[string]any{path: value}); err != nil {
		t.Errorf("putting /%s back as the owner after the rules accepted a write they must refuse: %s", path, facts.errText(err))
	}
}

// crRead reads a node as the owner, which bypasses the rules: it is how a
// refusal is shown to have changed nothing and an acceptance to have landed.
func crRead(t *testing.T, ctx context.Context, facts *crFacts, owner *rtdb.Client, path string) (any, bool) {
	t.Helper()
	var raw json.RawMessage
	found, err := owner.Get(ctx, path, &raw)
	if err != nil {
		t.Fatalf("reading /%s as the owner: %s", path, facts.errText(err))
	}
	if !found {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("reading /%s: %s is not JSON: %s", path, raw, facts.errText(err))
	}
	return v, true
}

// crCleanup deletes every node the check wrote, as the owner, and reads each
// one back to prove it is gone. It runs on the way out of the test whether the
// test passed or failed, and it is idempotent: the nodes it deletes are the
// ones crWritten names, whether or not the run got as far as writing them.
type crCleanup struct {
	paths []string
	done  bool
}

func (c *crCleanup) run(t *testing.T, target crTarget, facts *crFacts) {
	if c.done {
		return
	}
	c.done = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	owner, err := crOwner(ctx, target)
	if err != nil {
		t.Errorf("the check's nodes are still on the installation: %s", facts.errText(err))
		return
	}
	updates := make(map[string]any, len(c.paths))
	for _, p := range c.paths {
		updates[p] = nil
	}
	if err := owner.Patch(ctx, "", updates); err != nil {
		t.Errorf("deleting the check's nodes as the owner: %s", facts.errText(err))
		return
	}
	var survivors []string
	for _, p := range c.paths {
		var raw json.RawMessage
		found, err := owner.Get(ctx, p, &raw)
		if err != nil {
			t.Errorf("reading /%s back after the cleanup: %s", p, facts.errText(err))
			continue
		}
		if found {
			survivors = append(survivors, p)
		}
	}
	if len(survivors) > 0 {
		t.Errorf("these nodes survived the cleanup and are still on the installation: %s", strings.Join(survivors, ", "))
	}
	facts.fact("the check's %d nodes were deleted as the owner and read back absent: %s", len(c.paths), strings.Join(c.paths, ", "))
}

// crAuthUsers is the Firebase Auth users the check's exchanges create: a run's
// identity is one (r~<slug>~<run>, made at the first sign-in), and so is each
// of the five other identities. The list starts as crAuthUIDs(n) — every uid
// the exchanges *can* create, derived from the names before the first of them
// is made, so that an Exchange which creates the account and then fails still
// leaves a uid the cleanup knows. They are deleted as the owner with the API
// the history job's sweeper uses (budget.AuthAdmin: accounts:batchDelete), and
// the project's user list is read back: a database the check left as it found
// it, with the users it created still in the project, is a cleanup that only
// looks finished. The read-back is the verdict — crAuthSurvivors — and the
// delete call's per-user answers are context for a read-back that failed, not
// evidence of a survivor.
//
// The scratch run ids are what they are (live92-<hex>-run) for the same
// reason: if this ever fails, what is left is unmistakably the check's.
type crAuthUsers struct {
	uids []string
	done bool
}

func (u *crAuthUsers) add(uid string) {
	if uid == "" {
		return
	}
	for _, have := range u.uids {
		if have == uid {
			return
		}
	}
	u.uids = append(u.uids, uid)
}

func (u *crAuthUsers) run(t *testing.T, facts *crFacts) {
	if u.done || len(u.uids) == 0 {
		return
	}
	u.done = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ts, err := google.DefaultTokenSource(ctx, crAuthScope)
	if err != nil {
		t.Errorf("the %d Firebase Auth users this check's exchanges created are still in project %s (%s): no Application Default Credentials to delete them: %s", len(u.uids), crSandboxProject, strings.Join(u.uids, ", "), facts.errText(err))
		return
	}
	admin := &budget.AuthAdmin{Endpoint: "https://identitytoolkit.googleapis.com", Project: crSandboxProject, HTTP: oauth2.NewClient(ctx, ts)}
	// Every uid the check can name is deleted, including the uids of exchanges
	// that failed before they made the account: deleting an account that is not
	// there is nothing to delete. Where the API answers per-user that it could
	// not delete one of them, that answer is not the check's evidence of a
	// survivor — a uid whose exchange never signed in has nothing to delete —
	// and it is reported only as context for a read-back that could not say.
	// What proves the accounts are gone is the user list, below.
	failed, err := admin.DeleteUsers(ctx, u.uids)
	if err != nil {
		t.Errorf("the check's Firebase Auth users could not be deleted as the owner: %s (the account needs firebaseauth.users.delete, which the project's owner role holds)", facts.errText(err))
	}
	left, err := admin.ListUsers(ctx)
	if err != nil {
		t.Errorf("the check's Firebase Auth users may still be in project %s, and its user list could not be read back to say: %s (accounts:batchDelete answered: %s)", crSandboxProject, facts.errText(err), crDeleteAnswers(failed))
		return
	}
	if survivors := crAuthSurvivors(u.uids, left); len(survivors) > 0 {
		t.Errorf("these Firebase Auth users survived the cleanup and are still in project %s: %s", crSandboxProject, strings.Join(survivors, ", "))
		return
	}
	facts.fact("the %d Firebase Auth uids the check's exchanges can create (%s) were deleted as the owner with accounts:batchDelete and are absent from project %s's user list", len(u.uids), strings.Join(u.uids, ", "), crSandboxProject)
}
