package credsvc

// LM1's other half: the issuance record's store (identity_record.go) over the
// installation's database.
//
// The store is where this contract's three decisions are actually made — whose
// record a run is, whether its ticket was claimed, and that a claim's retry is
// the same claim — and every one of them is a read-then-write on one node. So
// these tests drive the real store over internal/gcpfake's Realtime Database,
// which honours the conditional write the way the deployment's does (a PUT
// under If-Match that does not hold answers 412 and changes nothing): a fixture
// store that wrote unconditionally would make every test here pass on a store
// that hands two callers one run.
//
// Everything is hermetic (Convention 9), and every value that stands for a
// secret is drawn at run time or a digest of a name (Convention 2): a ticket
// that appears here is one newTicket drew in the test that drew it.

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/rtdb"
)

// ticketTestSlug is the repository every record in this file is written under:
// one slug and one run, because the store's whole job is one node.
const ticketTestSlug = "acme-aurora"

// ticketTestRun is a real run id of the shape the launcher mints, so the node
// this file reads back is spelled the way a deployment's node is.
func ticketTestRun() string { return testNow().Format("20060102-150405") + "-ab12" }

// ticketStore is the real store over a fake database, wired the way
// internal/cli/credentials_serve.go wires it (NewIssuanceStore over the
// service's own *rtdb.Client and no other client). The fake is returned beside
// it because what a call left on the node is a fact about the database and not
// only about the store.
func ticketStore(t *testing.T) (*rtdb.Client, *gcpfake.RTDB, IssuanceStore) {
	t.Helper()
	db := gcpfake.NewRTDB(t)
	client, err := rtdb.New(db.URL, rtdb.Auth{IDToken: func() string { return "" }})
	if err != nil {
		t.Fatalf("the fake database's client: %v", err)
	}
	return client, db, NewIssuanceStore(client)
}

// ticketRecord is a record of the shape the route writes: the verified caller,
// the claims the service would sign, the account the claim must come from and
// the digest of the ticket. Only the caller and the ticket's digest are ever
// changed by a test, which is what the store's decisions are made from.
func ticketRecord(caller string, hashes ...string) identitywire.Record {
	hash := identitywire.Digest("the-ticket-this-record-is-about")
	if len(hashes) > 0 {
		hash = hashes[0]
	}
	return identitywire.Record{
		Caller: caller,
		Class:  identitywire.ClassPerson,
		At:     testNow(),
		Claims: token.Claims{
			Slug:   ticketTestSlug,
			Run:    ticketTestRun(),
			FX:     testNow().Add(time.Hour),
			FP:     testProject,
			RB:     caller,
			Branch: "fugaro/" + ticketTestRun(),
		},
		ExpectedAccount: testJobSA,
		TicketHash:      hash,
	}
}

// TestTicketRecordWrittenOncePerRun states the store's shape: one node per slug
// and run, written whole, replaced rather than joined — so a re-issue leaves
// ONE record and no second one under a path nothing serves.
func TestTicketRecordWrittenOncePerRun(t *testing.T) {
	_, db, store := ticketStore(t)
	ctx := context.Background()
	run := ticketTestRun()
	first := ticketRecord(testLauncherAccount, identitywire.Digest("the-first-ticket"))
	second := ticketRecord(testLauncherAccount, identitywire.Digest("the-second-ticket"))

	if err := store.Put(ctx, ticketTestSlug, run, first); err != nil {
		t.Fatalf("writing the first record: %v", err)
	}
	if err := store.Put(ctx, ticketTestSlug, run, second); err != nil {
		t.Fatalf("the re-issue of the same caller's record is refused: %v", err)
	}
	got, found, err := store.Get(ctx, ticketTestSlug, run)
	if err != nil || !found {
		t.Fatalf("reading the record back: found=%v err=%v", found, err)
	}
	if got.TicketHash != second.TicketHash {
		t.Errorf("the record holds ticket %q, want the re-issued one %q: R6 is the replacement, and the old ticket dies with it", got.TicketHash, second.TicketHash)
	}
	node, _ := db.Value(identitywire.PathRecord(ticketTestSlug, run)).(map[string]any)
	if node == nil {
		t.Fatalf("the record is not at its own path")
	}
	if len(node) == 0 {
		t.Errorf("the node is empty: %v", node)
	}
	under, _ := db.Value("creds/issuance/" + ticketTestSlug).(map[string]any)
	if len(under) != 1 {
		t.Errorf("the slug's node holds %d runs, want the one that was asked for: %v", len(under), under)
	}
}

// TestTicketRecordRefusesAnotherCallersRecord is the red step's subject: a
// record written by one caller may not be replaced by another's ask for the
// same run (R5). The store refuses it from the value it read, inside the same
// read-then-write that would have written it — so the refusal cannot be raced
// by a second ask of the real owner.
func TestTicketRecordRefusesAnotherCallersRecord(t *testing.T) {
	_, _, store := ticketStore(t)
	ctx := context.Background()
	run := ticketTestRun()
	mine := ticketRecord(testLauncherAccount, identitywire.Digest("my-ticket"))

	if err := store.Put(ctx, ticketTestSlug, run, mine); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	err := store.Put(ctx, ticketTestSlug, run, ticketRecord(testOtherLauncher, identitywire.Digest("someone-elses-ticket")))
	if !errors.Is(err, identitywire.ErrAnotherCaller) {
		t.Fatalf("another caller's ask is answered %v, want %v", err, identitywire.ErrAnotherCaller)
	}
	got, _, err := store.Get(ctx, ticketTestSlug, run)
	if err != nil {
		t.Fatalf("reading the record back: %v", err)
	}
	if got.Caller != testLauncherAccount || got.TicketHash != mine.TicketHash {
		t.Errorf("the refused ask changed the record: %+v", got)
	}
}

// TestTicketRecordRefusesToReplaceAClaimedRecord is the other half of R5: after
// the claim the record is what the identity was signed from, and no ask — the
// same caller's included — may replace it. A new attempt takes a new run id.
func TestTicketRecordRefusesToReplaceAClaimedRecord(t *testing.T) {
	_, _, store := ticketStore(t)
	ctx := context.Background()
	run := ticketTestRun()
	rec := ticketRecord(testLauncherAccount, identitywire.Digest("the-claimed-ticket"))

	if err := store.Put(ctx, ticketTestSlug, run, rec); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	claim := identitywire.Claim{Account: testJobSA, At: testNow(), NonceHash: identitywire.Digest("a-nonce-the-run-drew")}
	if _, err := store.Claim(ctx, ticketTestSlug, run, claim); err != nil {
		t.Fatalf("claiming the record: %v", err)
	}
	err := store.Put(ctx, ticketTestSlug, run, ticketRecord(testLauncherAccount, identitywire.Digest("a-later-ticket")))
	if !errors.Is(err, identitywire.ErrClaimed) {
		t.Fatalf("the same caller's ask after a claim is answered %v, want %v", err, identitywire.ErrClaimed)
	}
	got, _, err := store.Get(ctx, ticketTestSlug, run)
	if err != nil {
		t.Fatalf("reading the record back: %v", err)
	}
	if got.TicketHash != rec.TicketHash || got.Claimed.Account != testJobSA {
		t.Errorf("the refused ask changed the claimed record: %+v", got)
	}
}

// countingDB counts the writes and the reads of the store's loop, which is how
// a test states that a call decided nothing: the retry of a claim must write
// NOTHING (a write that fails would fail a call the record already answers),
// and a refused claim must not have written before it refused.
type countingDB struct {
	creds.DB
	reads  int
	writes int
}

func (c *countingDB) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	c.reads++
	return c.DB.GetETag(ctx, path, out)
}

func (c *countingDB) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	c.writes++
	return c.DB.PutIfMatch(ctx, path, etag, v)
}

// TestTicketRecordClaimIsOnceAndItsRetryWritesNothing is R2's and R5's rule on
// the node: the expected account's claim lands once, the SAME account with the
// SAME nonce digest gets the record back and writes nothing (that is the retry
// a lost answer is retried with, and a second write would spend the node's ETag
// and could fail a call already served), and every other claim on a claimed
// record — another nonce, another account — is refused.
func TestTicketRecordClaimIsOnceAndItsRetryWritesNothing(t *testing.T) {
	client, _, _ := ticketStore(t)
	counted := &countingDB{DB: client}
	store := NewIssuanceStore(counted)
	ctx := context.Background()
	run := ticketTestRun()

	if err := store.Put(ctx, ticketTestSlug, run, ticketRecord(testLauncherAccount)); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	counted.writes = 0

	// The floor under the claim route: an account that is not the record's
	// expected one cannot claim it, and the refusal writes nothing at all.
	wrong := identitywire.Claim{Account: testSiblingSA, At: testNow(), NonceHash: identitywire.Digest("a-nonce-the-run-drew")}
	if _, err := store.Claim(ctx, ticketTestSlug, run, wrong); !errors.Is(err, identitywire.ErrAnotherCaller) {
		t.Fatalf("a claim from another account is answered %v, want %v", err, identitywire.ErrAnotherCaller)
	}
	if counted.writes != 0 {
		t.Errorf("the refused claim wrote %d times, want none: a claim is refused from the value it read and writes nothing", counted.writes)
	}

	claim := identitywire.Claim{Account: testJobSA, At: testNow(), NonceHash: identitywire.Digest("a-nonce-the-run-drew")}
	got, err := store.Claim(ctx, ticketTestSlug, run, claim)
	if err != nil {
		t.Fatalf("the expected account's claim: %v", err)
	}
	if got.Claimed.Account != testJobSA || got.Claimed.NonceHash != claim.NonceHash || !got.Claimed.At.Equal(testNow()) {
		t.Errorf("the claimed record says %+v, want the claim that was made", got.Claimed)
	}
	if counted.writes != 1 {
		t.Fatalf("the claim wrote %d times, want 1", counted.writes)
	}

	again, err := store.Claim(ctx, ticketTestSlug, run, claim)
	if err != nil {
		t.Fatalf("the same claim again is refused: %v: R2's retry gets the same answer, not a refusal", err)
	}
	if again.Claimed != got.Claimed {
		t.Errorf("the retry answered %+v, want the record as it stands %+v", again.Claimed, got.Claimed)
	}
	if counted.writes != 1 {
		t.Errorf("the retry wrote %d times, want the record left alone: %d writes in all", counted.writes-1, counted.writes)
	}

	other := identitywire.Claim{Account: testJobSA, At: testNow(), NonceHash: identitywire.Digest("a-nonce-drawn-later")}
	if _, err := store.Claim(ctx, ticketTestSlug, run, other); !errors.Is(err, identitywire.ErrClaimed) {
		t.Errorf("another nonce after a success is answered %v, want %v", err, identitywire.ErrClaimed)
	}
	if counted.writes != 1 {
		t.Errorf("the refused second claim wrote %d times, want none", counted.writes-1)
	}
}

// TestTicketRecordClaimFoldsTheAccount: the store's account floor folds as the
// claim route's own check does (sameAccount). The ticket route admits the
// account an ask names folded and writes the ask's OWN spelling on the record
// (ticketAccountAdmitted, identity_ticket.go), while the claim's account is the
// CONFIGURED spelling the verifier answers (auth.go's verifyJob) — so a
// byte-for-byte comparison here refuses a run whose ticket names a
// differently-cased account that the route itself admitted, which is the review
// of LM2's finding. Both the fence and the retry fold, so the same claim retried
// in another spelling is still R2's retry and not a second claim.
func TestTicketRecordClaimFoldsTheAccount(t *testing.T) {
	client, _, _ := ticketStore(t)
	counted := &countingDB{DB: client}
	store := NewIssuanceStore(counted)
	ctx := context.Background()
	run := ticketTestRun()

	// The record holds the ask's own spelling, which differs from the claim's
	// configured one only in case: the two are one account.
	rec := ticketRecord(testLauncherAccount)
	rec.ExpectedAccount = strings.ToUpper(testJobSA)
	if err := store.Put(ctx, ticketTestSlug, run, rec); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	counted.writes = 0

	claim := identitywire.Claim{Account: testJobSA, At: testNow(), NonceHash: identitywire.Digest("a-nonce-the-run-drew")}
	got, err := store.Claim(ctx, ticketTestSlug, run, claim)
	if err != nil {
		t.Fatalf("a claim whose account differs from the record's only in case is answered %v, want the record claimed", err)
	}
	if !sameAccount(got.Claimed.Account, testJobSA) || got.Claimed.NonceHash != claim.NonceHash {
		t.Errorf("the claimed record says %+v, want the claim that was made", got.Claimed)
	}
	if counted.writes != 1 {
		t.Fatalf("the claim wrote %d times, want 1", counted.writes)
	}

	// The same claim, retried in the record's own spelling: the retry's fold has
	// to agree with the fence's, or a retry of a lost answer would be refused as
	// a second claim.
	again := identitywire.Claim{Account: strings.ToUpper(testJobSA), At: testNow(), NonceHash: identitywire.Digest("a-nonce-the-run-drew")}
	retried, err := store.Claim(ctx, ticketTestSlug, run, again)
	if err != nil {
		t.Fatalf("the same claim retried in another spelling is answered %v, want the record as it stands", err)
	}
	if retried.Claimed != got.Claimed {
		t.Errorf("the retry answered %+v, want the record as it stands %+v", retried.Claimed, got.Claimed)
	}
	if counted.writes != 1 {
		t.Errorf("the retry wrote %d times, want the record left alone", counted.writes)
	}

	// The fold makes two spellings of one address one account and never two
	// accounts one: a DIFFERENT account is still refused, on a record of its own.
	otherRun := ticketTestRun() + "b"
	if err := store.Put(ctx, ticketTestSlug, otherRun, ticketRecord(testLauncherAccount)); err != nil {
		t.Fatalf("writing the second record: %v", err)
	}
	other := identitywire.Claim{Account: testSiblingSA, At: testNow(), NonceHash: identitywire.Digest("a-nonce-the-run-drew")}
	if _, err := store.Claim(ctx, ticketTestSlug, otherRun, other); !errors.Is(err, identitywire.ErrAnotherCaller) {
		t.Errorf("another account is answered %v, want %v", err, identitywire.ErrAnotherCaller)
	}
}

// TestTicketRecordRefusesWhatItCannotRead is the store's fail-closed floor: a
// node that is not one record of this release — a member this build does not
// model, a value that is not an object at all — is the SERVICE's failure and
// never a decision about the caller. The distinction matters beyond tidiness: a
// record decoded with an unknown member dropped is a record whose next write
// loses that member, and the zero Record is an unclaimed ticket for no account.
func TestTicketRecordRefusesWhatItCannotRead(t *testing.T) {
	_, db, store := ticketStore(t)
	ctx := context.Background()
	run := ticketTestRun()

	// A run nobody asked for: a fact about the run, not a failure.
	if _, found, err := store.Get(ctx, ticketTestSlug, run); found || err != nil {
		t.Errorf("an absent record reads found=%v err=%v, want no record and no failure", found, err)
	}

	for _, tc := range []struct {
		name string
		node any
	}{
		{"a member this release does not model", map[string]any{
			"caller": testLauncherAccount, "class": "person", "at": testNow(),
			"claims": map[string]any{}, "expected_account": testJobSA,
			"ticket_hash": identitywire.Digest("a-ticket"), "claimed": map[string]any{},
			"a_member_of_a_later_release": 1,
		}},
		{"a value that is not an object", "not-a-record"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db.Set(identitywire.PathRecord(ticketTestSlug, run), tc.node)
			_, found, err := store.Get(ctx, ticketTestSlug, run)
			var serr *identitywire.StoreError
			if !errors.As(err, &serr) {
				t.Fatalf("reading it answers found=%v err=%v, want a *identitywire.StoreError: a node this release cannot read is the service's own state", found, err)
			}
			if !strings.Contains(serr.Path, run) {
				t.Errorf("the failure names path %q, want the node it could not read: an operator needs to know which record is unreadable", serr.Path)
			}
		})
	}

	// The store's floor under a caller that skipped the route's shape checks:
	// identitywire.PathRecord PANICS on an empty segment, and a panic is not a
	// refusal. It must be an error and no call to the path builder at all.
	if err := store.Put(ctx, ticketTestSlug, "", ticketRecord(testLauncherAccount)); err == nil {
		t.Errorf("a write with no run id is accepted, want a refusal: an empty segment collapses two runs onto one node")
	}
	if err := store.Put(ctx, "", run, ticketRecord(testLauncherAccount)); err == nil {
		t.Errorf("a write with no repository slug is accepted, want a refusal")
	}
}

// TestTicketRecordGivesUpOnContention is the loop's bound: a node whose ETag
// moves under every attempt is refused with the contention error and never
// written unconditionally. An unconditional write is exactly the lost update
// the compare-and-set exists to prevent — it would overwrite a claim another
// writer made between the read and the write.
func TestTicketRecordGivesUpOnContention(t *testing.T) {
	client, _, _ := ticketStore(t)
	counted := &countingDB{DB: staleDB{DB: client}}
	store := NewIssuanceStore(counted)

	err := store.Put(context.Background(), ticketTestSlug, ticketTestRun(), ticketRecord(testLauncherAccount))
	var serr *identitywire.StoreError
	if !errors.As(err, &serr) {
		t.Fatalf("a node that never stops moving answers %v, want a *identitywire.StoreError", err)
	}
	if !errors.Is(err, creds.ErrContention) {
		t.Errorf("the failure is %v, want it to wrap %v: a caller is told the service could not serve, and an operator is told why", err, creds.ErrContention)
	}
	if counted.reads != creds.DefaultAttempts {
		t.Errorf("the loop read the node %d times, want the %d attempts the cascade's loop is bounded by", counted.reads, creds.DefaultAttempts)
	}
	if counted.writes != creds.DefaultAttempts {
		t.Errorf("the loop wrote %d times, want one attempt per pass (%d)", counted.writes, creds.DefaultAttempts)
	}
}

// TestTicketRecordReadFailureIsTheServices is the other half of the store's
// error contract: a node nobody could READ is wrapped the same way, so the
// route answers it the service's own failure and never a refusal of the caller.
func TestTicketRecordReadFailureIsTheServices(t *testing.T) {
	client, _, _ := ticketStore(t)
	store := NewIssuanceStore(errDB{DB: client})
	if _, err := store.Claim(context.Background(), ticketTestSlug, ticketTestRun(), identitywire.Claim{Account: testJobSA, At: testNow()}); err == nil {
		t.Fatalf("a claim on a database that cannot be read is accepted")
	}
	var serr *identitywire.StoreError
	if err := store.Put(context.Background(), ticketTestSlug, ticketTestRun(), ticketRecord(testLauncherAccount)); !errors.As(err, &serr) {
		t.Errorf("a write against an unreadable database answers %v, want a *identitywire.StoreError", err)
	}
}

// staleDB answers every conditional write with the precondition failure and
// changes nothing, which is what a node another writer wins the race on looks
// like from inside the store's loop.
type staleDB struct{ creds.DB }

func (s staleDB) PutIfMatch(context.Context, string, string, any) error { return rtdb.ErrPrecondition }

// errDB is a database nobody can read.
type errDB struct{ creds.DB }

func (e errDB) GetETag(context.Context, string, any) (string, bool, error) {
	return "", false, errors.New("the database could not be reached")
}

// TestTicketDrawnIsASecret pins the ticket's secret: drawn from crypto/rand at
// the width a credential needs, spelled in hex, carried beside the record's own
// two names, and different every time. A secret two callers could predict would
// make the ticket claimable by either of them, and the only thing left between
// it and the record would be the account the claim checks — a fence this route
// must not have to lean on.
func TestTicketDrawnIsASecret(t *testing.T) {
	f := newTickets(t)
	seen := map[string]bool{}
	for range 32 {
		tkt, err := newTicket(f.slug, f.run)
		if err != nil {
			t.Fatalf("drawing a ticket: %v", err)
		}
		// The ticket is the record's own two names beside the secret
		// (joinIdentityTicket, identity_claim.go): what this test pins is the
		// SECRET, the part of it a caller could otherwise predict.
		parts := strings.Split(tkt, identityTicketSeparator)
		if len(parts) != 3 || parts[0] != f.slug || parts[1] != f.run {
			t.Fatalf("the ticket %q does not name the record it was drawn for", tkt)
		}
		if len(parts[2]) != ticketBytes*2 {
			t.Fatalf("the ticket's secret is %d characters, want %d: %d bytes in hex", len(parts[2]), ticketBytes*2, ticketBytes)
		}
		if _, err := hex.DecodeString(parts[2]); err != nil {
			t.Fatalf("the ticket's secret is not hex: %v", err)
		}
		if seen[tkt] {
			t.Fatalf("the ticket %q was drawn twice", tkt)
		}
		seen[tkt] = true
	}
}

// TestTicketLifetimeIsInsideTheIdentity pins the one relation the ticket's life
// has to have: it is shorter than the identity it produces, whatever runtime a
// launcher asks for, and longer than the queue the execution waits in. A ticket
// that outlived its identity would be a second claim after the run was served;
// one that died inside the queue would fail a launch that did everything right.
func TestTicketLifetimeIsInsideTheIdentity(t *testing.T) {
	now := testNow()
	for _, runtime := range []time.Duration{time.Second, time.Hour, 24 * time.Hour} {
		rec := ticketRecord(testLauncherAccount)
		rec.At = now
		rec.Claims.FX = token.ExpiryFX(now, runtime)
		if !ticketExpiry(rec).Before(rec.Claims.FX) {
			t.Errorf("with a runtime of %s the ticket is claimable until %s, want it dead before the identity's %s", runtime, ticketExpiry(rec), rec.Claims.FX)
		}
		if got := ticketExpiry(rec).Sub(now); got != ticketLifetime {
			t.Errorf("the ticket's life is %s, want %s", got, ticketLifetime)
		}
	}
	if ticketLifetime < token.QueueAllowance {
		t.Errorf("a ticket lives %s, shorter than the %s an execution may wait to be placed: a launch that did everything right would find its ticket dead", ticketLifetime, token.QueueAllowance)
	}
}
