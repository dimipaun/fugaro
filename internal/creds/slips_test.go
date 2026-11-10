package creds

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The slip store's tests (DS1.1): the record under creds/slips/<id>, the claim
// that is once and only once, the hash a claim secret is kept as, the closed
// shape that holds no value (G9), and the max runtime that ends issuance.
//
// They run over the fake database (state_test.go), so the ETag discipline the
// claim is built on is the real one: the fake refuses an empty precondition and
// compares the ETag a writer hands it, which is what makes a claim that is not
// a compare-and-set fail here rather than in production.

// slipAccount is the job account a test's slip expects: the email the service
// verifies from the job's own token (auth.go). An account is not a secret and
// not a value, and the string is the shape of a service account's address.
const slipAccount = "fugaro-job-gh-acme-app@acme.iam.gserviceaccount.com"

// slipOwner is the launcher's verified `sub` (D1938, U2): the digits of a
// Google account ID, which is what the name grammar and the intake are keyed
// on. A slip records it so an operator can see who asked for the run.
const slipOwner = "104729113355"

// slipSecretID is the one secret ID a test's slip allows. It is a NAME — the
// only thing a slip records about a credential (G9, F19) — spelled in the new
// grammar's user prefix, so a reader sees the shape the record carries.
const slipSecretID = "fugaro_u_104729113355_anthropic-api-key"

// testSlip is an unclaimed slip for the test's run, with every field the record
// carries set: a name-only record, and a max runtime inside the service's cap.
func testSlip(id string) Slip {
	return Slip{
		ID:              id,
		Owner:           slipOwner,
		Slug:            run.Slug,
		Run:             run.ID,
		ExpectedAccount: slipAccount,
		Allowed:         []string{slipSecretID},
		Pool:            "team-pool",
		MaxRuntime:      90 * time.Minute,
	}
}

// slipStore is the store over the fake database: the ETag path a claim is
// decided on, without a network.
func slipStore(db *fakeDB) *RTDBStore { return &RTDBStore{DB: db} }

// rawNode answers one node's JSON as it stands in the database, so a test can
// state what the record holds rather than only what a reader made of it.
func rawNode(t *testing.T, db *fakeDB, path string) json.RawMessage {
	t.Helper()
	var raw json.RawMessage
	if _, found, err := db.GetETag(ctx, path, &raw); err != nil || !found {
		t.Fatalf("reading /%s: found=%v err=%v", path, found, err)
	}
	return raw
}

// A slip is claimed once and once only, and the claim is a compare-and-set
// rather than a read-then-write: the run that claims first keeps the claim, and
// every other claim — a second call, and a caller that arrived at the same
// instant — is refused.
//
// Three halves, and each catches a shape the others do not. The second call
// catches a claim that never checks; the lost race catches a claim that checks
// and then writes anyway (or writes without a precondition), which is the
// read-then-write the database's missing transactions make fatal here; and the
// callers that arrive at once state the property under real concurrency. The
// lost race is the one that is deterministic — the fake lets the rival's claim
// land between this claim's read and its write, which is what another instance
// claiming the same slip does — so a claim that is not a compare-and-set fails
// this test every time rather than on a scheduler's whim.
func TestClaimIsOnce(t *testing.T) {
	db := newFakeDB()
	st := slipStore(db)
	sl := testSlip("s-1")
	if err := st.Create(ctx, sl); err != nil {
		t.Fatalf("creating the slip: %v", err)
	}

	claimed, secret, err := st.Claim(ctx, sl.ID, run, slipAccount, now)
	if err != nil {
		t.Fatalf("the first claim was refused: %v", err)
	}
	if secret == "" {
		t.Fatal("the claim answered no claim secret: the runner has nothing to present on its calls")
	}
	if !claimed.Claimed.At.Equal(now) || claimed.Claimed.Account != slipAccount {
		t.Fatalf("the claimed slip records %+v; want the account and the instant of the claim", claimed.Claimed)
	}
	if claimed.ID != sl.ID {
		t.Fatalf("the claimed slip carries the id %q; want %q, the node it was read from", claimed.ID, sl.ID)
	}

	// The second claim is refused, and it changes nothing: the first claim's
	// secret still matches, so a caller that claimed first is not locked out by
	// the caller that came second.
	if _, second, err := st.Claim(ctx, sl.ID, run, slipAccount, now.Add(time.Second)); !errors.Is(err, ErrSlipClaimed) {
		t.Fatalf("the second claim answered %v; want ErrSlipClaimed", err)
	} else if second != "" {
		t.Fatal("the refused claim answered a claim secret anyway")
	}
	after, found, err := st.Slip(ctx, sl.ID)
	if err != nil || !found {
		t.Fatalf("reading the claimed slip: found=%v err=%v", found, err)
	}
	if !after.ClaimMatches(secret) {
		t.Fatal("the refused claim replaced the claim secret: the runner that claimed first can no longer prove its claim")
	}
	if !after.Claimed.At.Equal(now) {
		t.Fatalf("the refused claim moved the claim to %s; want %s", after.Claimed.At, now)
	}

	// The lost race: the rival's claim is on the node between this claim's read
	// and its write, and this caller is refused rather than answered a second
	// secret for a slip another run already holds. The winner's claim stands.
	rivalSecret, err := newClaimSecret()
	if err != nil {
		t.Fatal(err)
	}
	raced := testSlip("s-race")
	if err := st.Create(ctx, raced); err != nil {
		t.Fatalf("creating the raced slip: %v", err)
	}
	winner := raced
	winner.ClaimHash = ClaimSecretHash(rivalSecret)
	winner.Claimed = Claim{Account: slipAccount, At: now}
	db.conflicts = 1
	db.rival = winner
	if _, losing, err := st.Claim(ctx, raced.ID, run, slipAccount, now); !errors.Is(err, ErrSlipClaimed) {
		t.Fatalf("a claim that lost the race answered %v; want ErrSlipClaimed — a claim is a compare-and-set, so a lost race re-reads and re-decides instead of writing what it read", err)
	} else if losing != "" {
		t.Fatal("a claim that lost the race answered a claim secret")
	}
	db.conflicts, db.rival = 0, nil
	won, found, err := st.Slip(ctx, raced.ID)
	if err != nil || !found {
		t.Fatalf("reading the raced slip: found=%v err=%v", found, err)
	}
	if !won.ClaimMatches(rivalSecret) {
		t.Fatal("the losing claim overwrote the winner's: the two claims were both written, which is the lost update the claim exists to prevent")
	}

	// And the same property from every caller at once.
	racing := testSlip("s-racing")
	if err := st.Create(ctx, racing); err != nil {
		t.Fatalf("creating the racing slip: %v", err)
	}
	const racers = 8
	var wg sync.WaitGroup
	errs := make([]error, racers)
	secrets := make([]string, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, secrets[i], errs[i] = st.Claim(ctx, racing.ID, run, slipAccount, now)
		}(i)
	}
	wg.Wait()

	winners := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
			if secrets[i] == "" {
				t.Error("a claim that won the race answered no claim secret")
			}
		case errors.Is(err, ErrSlipClaimed):
			if secrets[i] != "" {
				t.Error("a refused claim answered a claim secret")
			}
		default:
			t.Errorf("a claim that lost the race answered %v; want ErrSlipClaimed", err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d of %d claims arriving at once won; want exactly one", winners, racers)
	}
	// The slip holds exactly one claim, and it is the winner's.
	racedSlip, found, err := st.Slip(ctx, racing.ID)
	if err != nil || !found {
		t.Fatalf("reading the raced slip: found=%v err=%v", found, err)
	}
	matches := 0
	for _, s := range secrets {
		if s != "" && racedSlip.ClaimMatches(s) {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("%d of the answered secrets match the slip's claim hash; want the winner's one", matches)
	}
}

// The slip keeps the claim secret as a HASH and never as the secret itself: the
// node is read by every call, may be cached, and is one of the things an
// operator reads at the database, so a reader of the record must not be handed
// the thing that proves the claim (D1941).
//
// What the test pins is the two-sided fact that makes the hash useful: the
// secret the claim answered matches the stored hash, and nothing else does —
// and the secret itself appears nowhere in the node.
func TestSlipStoresOnlyClaimSecretHash(t *testing.T) {
	db := newFakeDB()
	st := slipStore(db)
	sl := testSlip("s-hash")
	if err := st.Create(ctx, sl); err != nil {
		t.Fatalf("creating the slip: %v", err)
	}

	// Before the claim there is no hash at all: a slip created carrying one would
	// be a claim nobody made.
	created, found, err := st.Slip(ctx, sl.ID)
	if err != nil || !found {
		t.Fatalf("reading the created slip: found=%v err=%v", found, err)
	}
	if created.ClaimHash != "" {
		t.Fatal("a slip was created carrying a claim hash: the claim is what writes one")
	}
	if !created.Claimed.At.IsZero() {
		t.Fatal("a slip was created already claimed")
	}

	claimed, secret, err := st.Claim(ctx, sl.ID, run, slipAccount, now)
	if err != nil {
		t.Fatalf("claiming the slip: %v", err)
	}
	if len(secret) < 2*ClaimSecretBytes {
		t.Fatalf("the claim secret is %d characters; want at least %d, the hex of %d drawn bytes", len(secret), 2*ClaimSecretBytes, ClaimSecretBytes)
	}
	if claimed.ClaimHash == secret {
		t.Fatal("the slip stores the claim secret itself")
	}
	if claimed.ClaimHash != ClaimSecretHash(secret) {
		t.Fatalf("the slip stores %q as the claim hash; want the hash of the secret the claim answered", claimed.ClaimHash)
	}
	if !claimed.ClaimMatches(secret) {
		t.Fatal("the secret the claim answered does not match the slip's own hash")
	}
	for _, other := range []string{"", secret + "x", secret[:len(secret)-1], ClaimSecretHash(secret)} {
		if claimed.ClaimMatches(other) {
			t.Fatalf("the slip accepted %q as its claim secret", other)
		}
	}

	// The node itself, as the database holds it, carries no copy of the secret.
	raw := string(rawNode(t, db, PathSlip(sl.ID)))
	if strings.Contains(raw, secret) {
		t.Fatal("the claim secret is in the slip node: only its hash may be")
	}
	if !strings.Contains(raw, claimed.ClaimHash) {
		t.Fatal("the slip node does not carry the claim hash the reader was given")
	}

	// Two claims draw two secrets: a constant would pass every check above and
	// hand every run the same thing to present.
	second := testSlip("s-hash-2")
	if err := st.Create(ctx, second); err != nil {
		t.Fatalf("creating the second slip: %v", err)
	}
	_, other, err := st.Claim(ctx, second.ID, run, slipAccount, now)
	if err != nil {
		t.Fatalf("claiming the second slip: %v", err)
	}
	if other == secret {
		t.Fatal("two claims answered the same secret")
	}
}

// A slip holds no credential value (G9, F19), and the test states it as the
// record's closed shape rather than as an absence: every member of the node is
// one of the names below, so a field that could carry a value is an edit here
// that a reviewer sees. A value a hand-edit or a newer release left in the node
// is refused by the read, because the decoder refuses a member this release
// does not model — the fail-closed direction, and the reason a slip that cannot
// be read is not a slip that is half read.
func TestSlipHoldsNoValue(t *testing.T) {
	db := newFakeDB()
	st := slipStore(db)
	sl := testSlip("s-shape")
	if err := st.Create(ctx, sl); err != nil {
		t.Fatalf("creating the slip: %v", err)
	}
	if _, _, err := st.Claim(ctx, sl.ID, run, slipAccount, now); err != nil {
		t.Fatalf("claiming the slip: %v", err)
	}

	var node map[string]json.RawMessage
	if err := json.Unmarshal(rawNode(t, db, PathSlip(sl.ID)), &node); err != nil {
		t.Fatalf("decoding the slip node: %v", err)
	}
	got := make([]string, 0, len(node))
	for k := range node {
		got = append(got, k)
	}
	slices.Sort(got)
	want := []string{
		"allowed", "claim_hash", "claimed", "expected_account",
		"max_runtime", "owner", "pool", "run", "slug",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the slip node carries %v; want exactly %v — a slip names what a run may use and holds no value", got, want)
	}

	// The names it does carry are the ones the caller named, unchanged.
	read, found, err := st.Slip(ctx, sl.ID)
	if err != nil || !found {
		t.Fatalf("reading the slip: found=%v err=%v", found, err)
	}
	if !slices.Equal(read.Allowed, []string{slipSecretID}) {
		t.Fatalf("the slip allows %v; want the secret ID it was created with", read.Allowed)
	}

	// A node carrying one more member is refused rather than read as the slip it
	// half is: this is the field a value would have to arrive in, and the read
	// does not accept it.
	var withValue map[string]any
	if err := json.Unmarshal(rawNode(t, db, PathSlip(sl.ID)), &withValue); err != nil {
		t.Fatalf("decoding the slip node: %v", err)
	}
	withValue["value"] = "x"
	db.put(PathSlip(sl.ID), withValue)
	if _, _, err := st.Slip(ctx, sl.ID); err == nil {
		t.Fatal("a slip node carrying a member this release does not model was read as a slip: an unreadable record must fail closed")
	}
}

// A slip's issuance ends at its max runtime, and the boundary is the instant it
// arrives: a slip whose runtime has passed serves nothing, and a claim of one
// is refused with the expiry rather than with the claim flag — a dead slip is
// dead whatever else it is, and a caller reading the refusal learns the one
// fact that is actionable.
//
// An unclaimed slip is not live either, which is R9's fail-closed rule at the
// store: nothing is served from a slip no run has claimed, so a call that
// arrives without a claim finds a slip that answers no.
//
// The last part is the other way a slip ends: the run's outcome (or the
// service's own housekeeping) removes the node, and an ended slip is refused
// whole — the claim included.
func TestExpiredSlipRefused(t *testing.T) {
	db := newFakeDB()
	st := slipStore(db)
	sl := testSlip("s-expiry")
	if err := st.Create(ctx, sl); err != nil {
		t.Fatalf("creating the slip: %v", err)
	}

	// Unclaimed: not live, whatever the clock says.
	unclaimed, found, err := st.Slip(ctx, sl.ID)
	if err != nil || !found {
		t.Fatalf("reading the unclaimed slip: found=%v err=%v", found, err)
	}
	if unclaimed.Live(now) {
		t.Fatal("an unclaimed slip is live: nothing is served from a slip no run has claimed (R9)")
	}

	claimed, _, err := st.Claim(ctx, sl.ID, run, slipAccount, now)
	if err != nil {
		t.Fatalf("claiming the slip: %v", err)
	}
	ends := now.Add(sl.MaxRuntime)
	if !claimed.Expires().Equal(ends) {
		t.Fatalf("the slip ends at %s; want %s, its max runtime from the claim", claimed.Expires(), ends)
	}
	if !claimed.Live(now.Add(sl.MaxRuntime - time.Nanosecond)) {
		t.Fatal("a slip one nanosecond inside its max runtime is not live")
	}
	if claimed.Live(ends) {
		t.Fatal("a slip whose max runtime has arrived is still live: the runtime ends at its instant, not after it")
	}
	if claimed.Live(ends.Add(time.Hour)) {
		t.Fatal("a slip past its max runtime is live")
	}
	if _, secret, err := st.Claim(ctx, sl.ID, run, slipAccount, ends); !errors.Is(err, ErrSlipExpired) {
		t.Fatalf("claiming a slip past its max runtime answered %v; want ErrSlipExpired", err)
	} else if secret != "" {
		t.Fatal("a claim of an expired slip answered a claim secret")
	}

	// Ended: the node is gone, and every use of the id is refused.
	ended := testSlip("s-ended")
	if err := st.Create(ctx, ended); err != nil {
		t.Fatalf("creating the slip to end: %v", err)
	}
	if err := st.End(ctx, ended.ID); err != nil {
		t.Fatalf("ending the slip: %v", err)
	}
	if _, found, err := st.Slip(ctx, ended.ID); err != nil || found {
		t.Fatalf("an ended slip is still in the database: found=%v err=%v", found, err)
	}
	if _, secret, err := st.Claim(ctx, ended.ID, run, slipAccount, now); !errors.Is(err, ErrNoSlip) {
		t.Fatalf("claiming an ended slip answered %v; want ErrNoSlip", err)
	} else if secret != "" {
		t.Fatal("a claim of an ended slip answered a claim secret")
	}
	// Ending a slip twice is not an error: the caller that ends it may be the
	// run's own handler racing the service's housekeeping, and a node that is
	// already gone is the state it wanted.
	if err := st.End(ctx, ended.ID); err != nil {
		t.Fatalf("ending an ended slip: %v", err)
	}
}

// Create refuses what it cannot record, and it refuses at the writer rather
// than leaving a reader to meet the record later. Every field a slip cannot be
// decided from is a write this store does not make (checkWritable): a record
// missing its run could never be claimed, the expected account is what the
// claim is checked against, a runtime that is not positive would leave a
// claimed slip that never ends, and a record created already claimed would be a
// claim nobody made.
//
// Each refusal is checked together with the state it leaves behind, because an
// error that wrote anyway would pass a test that read only the error, and the
// node it half-wrote is exactly the record every reader afterwards would have
// to defend against.
//
// An id that is already taken is the other half: it is another run's grant, so
// it is refused rather than overwritten, and the test states that as the record
// still being the first grant's rather than as the error alone.
func TestCreateRefusesWhatItCannotRecord(t *testing.T) {
	db := newFakeDB()
	st := slipStore(db)

	// An id that is empty is a refusal and never a path builder: PathSlip panics
	// on an empty segment, and the id arrives from a request (DS1.3).
	if err := st.Create(ctx, Slip{}); !errors.Is(err, ErrNoSlip) {
		t.Fatalf("creating a slip with no id answered %v; want ErrNoSlip", err)
	}

	// One field at a time, so a checkWritable that lost one of them is a failure
	// here and not a hole the next reader has to close.
	for _, c := range []struct {
		name string
		drop func(s *Slip)
	}{
		{"no owner", func(s *Slip) { s.Owner = "" }},
		{"no slug", func(s *Slip) { s.Slug = "" }},
		{"no run", func(s *Slip) { s.Run = "" }},
		{"no expected account", func(s *Slip) { s.ExpectedAccount = "" }},
		{"no runtime", func(s *Slip) { s.MaxRuntime = 0 }},
		{"a negative runtime", func(s *Slip) { s.MaxRuntime = -time.Minute }},
		{"a claim hash nobody made", func(s *Slip) { s.ClaimHash = ClaimSecretHash("a claim hash no run wrote") }},
		{"a claim nobody made", func(s *Slip) { s.Claimed = Claim{Account: slipAccount, At: now} }},
		// Half a claim is a claim nobody made too, and it is the shape the
		// instant-only test let through: Live keys on At, so an account with no
		// instant would be a slip that is never live yet names a claimant.
		{"a claimant with no instant", func(s *Slip) { s.Claimed = Claim{Account: slipAccount} }},
		{"an instant with no claimant", func(s *Slip) { s.Claimed = Claim{At: now} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			sl := testSlip("s-unwritable")
			c.drop(&sl)
			if err := st.Create(ctx, sl); !errors.Is(err, ErrSlipMalformed) {
				t.Fatalf("creating a slip with %s answered %v; want ErrSlipMalformed", c.name, err)
			}
			if _, found, err := st.Slip(ctx, sl.ID); err != nil || found {
				t.Fatalf("a refused create left a node behind: found=%v err=%v", found, err)
			}
		})
	}

	// An id that is taken is refused, and the record it would have replaced is
	// untouched: the second create names a different grant, and the slip a run
	// may already have claimed must not be swapped under it.
	first := testSlip("s-taken")
	first.Pool = "the first grant's pool"
	if err := st.Create(ctx, first); err != nil {
		t.Fatalf("creating the first slip: %v", err)
	}
	second := testSlip("s-taken")
	second.Pool = "the second grant's pool"
	if err := st.Create(ctx, second); !errors.Is(err, ErrSlipExists) {
		t.Fatalf("creating a slip over an existing id answered %v; want ErrSlipExists", err)
	}
	kept, found, err := st.Slip(ctx, first.ID)
	if err != nil || !found {
		t.Fatalf("reading the first slip: found=%v err=%v", found, err)
	}
	if kept.Pool != first.Pool {
		t.Fatalf("the refused create replaced the record: the pool is %q; want the first grant's %q", kept.Pool, first.Pool)
	}
}

// The claim is bound to the run AND to the account, and the id alone is never
// enough (G3, R8): a slip found or guessed by another run is refused whole, and
// this is the fence D1941 stands on — the id travels to the job in its
// environment, so what keeps another launcher out is the run identity and the
// account the record names rather than the secrecy of the id.
//
// Each field of the binding is dropped one at a time, because a check that
// forgot the run, the slug or the account would leave the others green. What
// the test also states is that the refusal is whole: no claim secret, and no
// change to the record, so a caller that merely guessed an id cannot use the
// store to tell one refusal from another.
func TestClaimRefusesASlipThatIsNotThisRuns(t *testing.T) {
	db := newFakeDB()
	st := slipStore(db)
	sl := testSlip("s-bound")
	if err := st.Create(ctx, sl); err != nil {
		t.Fatalf("creating the slip: %v", err)
	}

	for _, c := range []struct {
		name    string
		run     Run
		account string
	}{
		{"another run", Run{Slug: run.Slug, ID: "r2"}, slipAccount},
		{"another slug", Run{Slug: "gh-other-app", ID: run.ID}, slipAccount},
		{"another account", run, "fugaro-job-gh-other-app@acme.iam.gserviceaccount.com"},
		{"no account at all", run, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, secret, err := st.Claim(ctx, sl.ID, c.run, c.account, now); !errors.Is(err, ErrSlipIdentity) {
				t.Fatalf("a claim by %s answered %v; want ErrSlipIdentity", c.name, err)
			} else if secret != "" {
				t.Fatal("a claim the slip does not name answered a claim secret")
			}
			after, found, err := st.Slip(ctx, sl.ID)
			if err != nil || !found {
				t.Fatalf("reading the slip: found=%v err=%v", found, err)
			}
			if after.ClaimHash != "" || !after.Claimed.At.IsZero() {
				t.Fatalf("a refused claim wrote the record: claimed=%+v hash=%q", after.Claimed, after.ClaimHash)
			}
		})
	}

	// An id that names no node, and an id that is nothing at all: both are
	// ErrNoSlip, and neither reaches a path builder (PathSlip panics on an empty
	// segment). The empty id is checked on every method, because each of them
	// takes one straight from a request.
	if _, secret, err := st.Claim(ctx, "s-nobody", run, slipAccount, now); !errors.Is(err, ErrNoSlip) {
		t.Fatalf("claiming an id that names no node answered %v; want ErrNoSlip", err)
	} else if secret != "" {
		t.Fatal("a claim of a slip that is not there answered a claim secret")
	}
	if _, _, err := st.Claim(ctx, "", run, slipAccount, now); !errors.Is(err, ErrNoSlip) {
		t.Fatalf("claiming with no id answered %v; want ErrNoSlip", err)
	}
	if _, _, err := st.Slip(ctx, ""); !errors.Is(err, ErrNoSlip) {
		t.Fatalf("reading a slip with no id answered %v; want ErrNoSlip", err)
	}
	if err := st.End(ctx, ""); !errors.Is(err, ErrNoSlip) {
		t.Fatalf("ending a slip with no id answered %v; want ErrNoSlip", err)
	}

	// And the run the slip DOES name is still served: the fence refuses the
	// others, and a test that only ever saw refusals would pass on a store that
	// refuses everyone.
	if _, secret, err := st.Claim(ctx, sl.ID, run, slipAccount, now); err != nil || secret == "" {
		t.Fatalf("the run the slip names was refused: secret=%q err=%v", secret, err)
	}
}
