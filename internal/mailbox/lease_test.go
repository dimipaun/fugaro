package mailbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"golang.org/x/oauth2"
)

// The consumer lease's tests (mobile P4.1, Task T3).
//
// Two halves, and the task's verification names both:
//
//   * **The module's rules, replayed from committed data.** `shared/lease` is
//     the one merged module with no vector file, so `shared/lease/parity.test.js`
//     (a test file only — the module is never edited) writes the module's own
//     answers to `internal/mailbox/testdata/lease-parity.json` and
//     TestLeaseCorpusReplay compares this package's answers with them, case by
//     case, with no Node, no network and no clock (ruling M9). The bounds are
//     held to the module's own source as well, by
//     TestLeaseBoundsMatchTheSharedModule, so a corpus that was hand-edited
//     cannot quietly become the contract.
//   * **The store: the ETag compare-and-swap the lease rests on** (M1). Two
//     consumers read one empty heartbeat, both get `ok: true` from the module's
//     take, and the second write is refused `rtdb.ErrPrecondition` with nothing
//     moved — TestTwoConsumersOneLease, the task's headline test. The retry that
//     follows a lost race is bounded; the read and the write carry the
//     operator's ADC (C0); a heartbeat this consumer cannot read, or a
//     heartbeat it cannot sign, means no write and therefore no lease (M4b).
//
// Everything here runs on `internal/gcpfake`'s Realtime Database fake: no live
// cloud, no clock (every instant is a value), no key and no secret.
//
// **The red step that was run and watched fail** (T3's, and the one this file
// exists for): in lease.go's take, force the write after a lost race — re-read
// the ETag and write anyway, which is what "drop the if-match" means in code —
// and TestTwoConsumersOneLease fails with both consumers' heartbeats landing,
// the second overwriting the first. Restored; see the pull request.

const leaseCorpusPath = "testdata/lease-parity.json"

// TestLeaseCorpusReplay is M9's replay: for every case the module's own test
// generator wrote, this package must give the module's answer — the same
// outcome, the same refusal code (and, for an unreadable claim, the same inner
// code), the same lease, the same state.
func TestLeaseCorpusReplay(t *testing.T) {
	constants, codes, cases := loadLeaseCorpus(t)
	if len(cases) < 30 {
		t.Fatalf("the corpus carries %d cases: the file, not the replay, is what changed", len(cases))
	}

	// The bounds, held to the corpus the module generated.
	for name, want := range map[string]int64{
		"CLAIM_TTL":         ClaimTTL,
		"MAX_CLOCK_SKEW":    MaxClockSkew,
		"MAX_TTL":           MaxTTL,
		"MAX_TIMESTAMP":     MaxTimestamp,
		"MAX_HOLDER_LENGTH": MaxHolderLength,
	} {
		got, ok := constants[name]
		if !ok {
			t.Errorf("the corpus no longer carries %s: this package's bound has nothing to be held to", name)
			continue
		}
		if value, isInteger := intValue(got); !isInteger || value != want {
			t.Errorf("%s is %v in the module and %d here: the two ends disagree about the lease", name, got, want)
		}
	}
	if len(constants) != 5 {
		t.Errorf("the corpus carries %d constants and this test binds 5: a new one is a bound to bind, not to ignore", len(constants))
	}

	// The refusal vocabulary, held to the corpus the module generated.
	declared := map[string]bool{}
	for _, value := range codes {
		declared[text(t, value, "codes")] = true
	}
	here := map[string]bool{}
	for _, code := range LeaseCodes {
		here[string(code)] = true
	}
	for code := range declared {
		if !here[code] {
			t.Errorf("the module declares the lease code %q, which this package does not bind: a refusal one end speaks and the other cannot is two protocols", code)
		}
	}
	for code := range here {
		if !declared[code] {
			t.Errorf("this package declares the lease code %q, which the module does not: a code with nothing on the other end is a code that drifted", code)
		}
	}

	for _, c := range cases {
		name := text(t, c["name"], "name")
		op := text(t, c["op"], "op")
		state := leaseInput(t, c["state"], name)
		want := object(t, c["expect"], "expect")
		var got LeaseResult
		switch op {
		case "read":
			got = ReadLease(state)
		case "take":
			got = TakeLease(state, leaseInput(t, c["request"], name))
		case "release":
			got = ReleaseLease(state, leaseInput(t, c["request"], name))
		default:
			t.Fatalf("%s: the corpus carries the operation %q, which this replay does not know", name, op)
		}
		if g, w := mapCanonical(t, leaseAnswer(op, got)), mapCanonical(t, want); g != w {
			t.Errorf("%s (%s):\n  this package: %s\n  the module:   %s", name, op, g, w)
		}
	}
}

// TestLeaseCorpusCarriesThePlanBoundaries keeps the generator from shrinking:
// the cases T3's verification names are in the corpus, by name, and the corpus
// says the same thing about each of them as the module does.
func TestLeaseCorpusCarriesThePlanBoundaries(t *testing.T) {
	_, _, cases := loadLeaseCorpus(t)
	named := map[string]bool{}
	for _, c := range cases {
		named[text(t, c["name"], "name")] = true
	}
	for _, want := range []string{
		"a lease is fresh one millisecond before its TTL",
		"a lease is stale at exactly its TTL",
		"a lease exactly MAX_CLOCK_SKEW ahead is fresh",
		"a lease one millisecond past the skew is taken over",
		"a lease ahead to the ceiling cannot block a mailbox for ever",
		"a take on an unreadable state is refused, never taken over",
	} {
		if !named[want] {
			t.Errorf("the corpus no longer carries %q: the boundary the plan's verification names is not being compared", want)
		}
	}
}

// TestLeaseBoundsMatchTheSharedModule reads the module's own source, so the
// corpus is not the only thing this package's bounds rest on: a bound that
// moved in index.js and was not followed here is a failing test even if nobody
// ran the JavaScript suite (which has no CI job — P4.3 is owner-made).
func TestLeaseBoundsMatchTheSharedModule(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "shared", "lease", "index.js"))
	if err != nil {
		t.Fatalf("reading the module: %v", err)
	}
	declared := map[string]int64{}
	for _, m := range jsLeaseBound.FindAllStringSubmatch(string(source), -1) {
		value, ok := jsBoundValue(m[2])
		if !ok {
			t.Fatalf("reading %s out of the module: %q is not a bound this test can evaluate", m[1], m[2])
		}
		declared[m[1]] = value
	}
	for name, want := range map[string]int64{
		"CLAIM_TTL":         ClaimTTL,
		"MAX_CLOCK_SKEW":    MaxClockSkew,
		"MAX_TTL":           MaxTTL,
		"MAX_TIMESTAMP":     MaxTimestamp,
		"MAX_HOLDER_LENGTH": MaxHolderLength,
	} {
		got, ok := declared[name]
		if !ok {
			t.Errorf("the module no longer declares %s: this package's bound has nothing to be held to", name)
			continue
		}
		if got != want {
			t.Errorf("%s is %d in this package and %d in the module: the two ends disagree about a bound", name, want, got)
		}
	}
	if len(declared) != 5 {
		t.Errorf("the module declares %d lease bounds and this test knows 5: a new one is a bound to bind, not to ignore", len(declared))
	}

	// The codes: the module's LEASE_CODES is an object of name → code, and the
	// half this package binds is the code.
	object := jsLeaseCodes.FindStringSubmatch(string(source))
	if object == nil {
		t.Fatal("the module no longer declares LEASE_CODES as a frozen object: this package's vocabulary has nothing to be held to")
	}
	var module []string
	for _, m := range jsLeaseCode.FindAllStringSubmatch(object[1], -1) {
		module = append(module, m[2])
	}
	var mine []string
	for _, code := range LeaseCodes {
		mine = append(mine, string(code))
	}
	sort.Strings(module)
	sort.Strings(mine)
	if strings.Join(module, ",") != strings.Join(mine, ",") {
		t.Errorf("LEASE_CODES is %v in the module and %v here: a refusal one end spells and the other cannot is two protocols", module, mine)
	}
}

var (
	// The bounds are written as expressions rather than numbers: `10 * 60 *
	// 1000` for ten minutes, and Date.UTC for the ceiling. Both are evaluated
	// below, so the test binds the module's arithmetic, not a copy of its
	// result.
	jsLeaseBound = regexp.MustCompile(`(?m)^export const (CLAIM_TTL|MAX_CLOCK_SKEW|MAX_TTL|MAX_TIMESTAMP|MAX_HOLDER_LENGTH) = (.+);$`)
	jsLeaseCodes = regexp.MustCompile(`(?s)export const LEASE_CODES = Object\.freeze\(\{(.*?)\n\}\);`)
	jsLeaseCode  = regexp.MustCompile(`(?m)^\s*([A-Z_]+): '([^']*)',$`)
)

// jsBoundValue evaluates the two spellings the module's bounds use: a product
// of decimal integers, and `Date.UTC(year, month, day)`.
func jsBoundValue(expression string) (int64, bool) {
	if m := jsDateUTC.FindStringSubmatch(expression); m != nil {
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		return time.Date(year, time.Month(month+1), day, 0, 0, 0, 0, time.UTC).UnixMilli(), true
	}
	var product int64 = 1
	parts := strings.Split(expression, " * ")
	if len(parts) == 0 {
		return 0, false
	}
	for _, part := range parts {
		n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return 0, false
		}
		product *= n
	}
	return product, true
}

var jsDateUTC = regexp.MustCompile(`^Date\.UTC\((\d+), (\d+), (\d+)\)$`)

// loadLeaseCorpus reads the corpus with the package's own numeric conventions
// (UseNumber), so a fixture and the wire are read the same way.
func loadLeaseCorpus(t *testing.T) (map[string]any, map[string]any, []map[string]any) {
	t.Helper()
	data, err := os.ReadFile(leaseCorpusPath)
	if err != nil {
		t.Fatalf("reading the lease parity corpus: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var file map[string]any
	if err := dec.Decode(&file); err != nil {
		t.Fatalf("decoding the lease parity corpus: %v", err)
	}
	module := text(t, file["module"], "module")
	if module != "shared/lease/index.js" {
		t.Fatalf("the corpus says its answers come from %q; this replay reads shared/lease/index.js", module)
	}
	constants := object(t, file["constants"], "constants")
	codes := object(t, file["codes"], "codes")
	raw := list(t, file["cases"], "cases")
	cases := make([]map[string]any, 0, len(raw))
	for _, c := range raw {
		cases = append(cases, object(t, c, "cases"))
	}
	return constants, codes, cases
}

// leaseInput converts a corpus value into the argument this package's lease
// functions take. A Go record is a map, so a corpus value that is not one —
// `null`, which the module refuses as not-an-object — is a nil record, and
// anything else is a corpus this replay cannot express: the test says so rather
// than passing a value the typed API would have coerced into something else.
func leaseInput(t *testing.T, value any, where string) Record {
	t.Helper()
	switch v := value.(type) {
	case nil:
		return nil
	case map[string]any:
		return Record(v)
	case Record:
		return v
	default:
		t.Fatalf("%s: the corpus carries a lease input this Go API cannot express (%T)", where, value)
		return nil
	}
}

// leaseAnswer is this package's result in the module's shape, field for field —
// so the replay compares decisions and never a Go type's own spelling.
func leaseAnswer(op string, result LeaseResult) map[string]any {
	out := map[string]any{"ok": result.OK}
	if !result.OK {
		out["code"] = string(result.Code)
		if result.Because != "" {
			out["because"] = string(result.Because)
		}
	} else {
		switch op {
		case "read", "take":
			out["lease"] = leaseShape(result.Lease)
		case "release":
			out["released"] = leaseShape(result.Released)
		}
	}
	if result.Existing != nil {
		out["existing"] = leaseShape(result.Existing)
	}
	if result.State != nil {
		out["state"] = result.State
	}
	return out
}

func leaseShape(lease *Lease) any {
	if lease == nil {
		return nil
	}
	return Record{"holder": lease.Holder, "at": lease.At}
}

// leaseCanonical renders a result canonically for comparison: keys in byte
// order, integers as integers, so a float64 from the corpus and an int64 from
// this package compare as the same number.
func leaseCanonical(t *testing.T, value any) string { return mapCanonical(t, value) }

func mapCanonical(t *testing.T, value any) string {
	t.Helper()
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(v)
	case string:
		return strconv.Quote(v)
	case json.Number:
		return v.String()
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	case float64:
		if v != math.Trunc(v) || math.Abs(v) > math.MaxInt64 {
			t.Fatalf("%v is not an integer the lease contract speaks", v)
		}
		return strconv.FormatInt(int64(v), 10)
	case Record:
		return mapCanonical(t, map[string]any(v))
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, strconv.Quote(key)+":"+mapCanonical(t, v[key]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		t.Fatalf("%T is not a value the lease contract speaks", value)
		return ""
	}
}

// --- the store: the ETag compare-and-swap (M1) -------------------------------

// leaseMailbox is the phone whose mailbox the tests hold a lease on. A mailbox
// name is an identity the schema accepts as a `sender`.
const leaseMailbox = "phone-uid"

// leaseHolder and otherHolder are two consumers' boot ids.
const (
	leaseHolder = "boot-a"
	otherHolder = "boot-b"
	leaseAt     = int64(1_700_000_000_000)
)

// adc is the operator's application-default credential: an OAuth2 source, which
// internal/rtdb sends as a Bearer header. The phone's credential is the other
// spelling (`?auth=`), and the lease may not use it (C0).
func adc(token string) oauth2.TokenSource {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
}

// leaseClient opens the database the way the command does, with the operator's
// credential and no Firebase ID token.
func leaseClient(t *testing.T, f *gcpfake.RTDB) *rtdb.Client {
	t.Helper()
	c, err := rtdb.New(f.URL, rtdb.Auth{Source: adc("ya29.operator-secret")})
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	return c
}

// stubSigner is a stand-in for T2a's `Sign`: the tag it returns is a real
// HMAC-SHA-256 shape (64 lowercase hex characters, which the schema requires),
// derived from the heartbeat so a test can tell two holders' writes apart.
func stubSigner(salt string) LeaseSigner {
	return func(hb Heartbeat) (string, error) {
		sum := sha256.Sum256([]byte(salt + "|" + hb.Boot + "|" + strconv.FormatInt(hb.At, 10)))
		return hex.EncodeToString(sum[:]), nil
	}
}

func leaseOptions(holder string, at int64) LeaseOptions {
	return LeaseOptions{
		Mailbox: leaseMailbox,
		Holder:  holder,
		At:      at,
		Sign:    stubSigner(holder),
	}
}

// countingStore wraps the real client so a test can see what the lease asked of
// the store — how many reads, how many conditional writes, and what the store
// answered — and can hold one read open, which is how two consumers are made to
// read one ETag before either writes.
type countingStore struct {
	inner  HeartbeatStore
	gets   int
	puts   int
	absent bool
	errs   []error
	// afterRead runs inside GetETag, after the inner call answered and before
	// this package sees the answer. It runs on the first read only: it is how a
	// test makes this consumer hold the ETag it read while another writes.
	afterRead func()
	held      bool
}

func (s *countingStore) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	etag, found, err := s.inner.GetETag(ctx, path, out)
	s.gets++
	if err == nil && !found {
		s.absent = true
	}
	if s.afterRead != nil && !s.held {
		s.held = true
		s.afterRead()
	}
	return etag, found, err
}

func (s *countingStore) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	s.puts++
	err := s.inner.PutIfMatch(ctx, path, etag, v)
	if err != nil {
		s.errs = append(s.errs, err)
	}
	return err
}

// TestTwoConsumersOneLease is the fence, and T3's headline test.
//
// Two consumers read **one empty heartbeat** — the loser's read is the real read,
// held open until the winner has written, so both read the same empty node and
// the same ETag, and both get `ok: true` from the module's take — and then both
// try to write. Exactly one write lands: the loser's is refused
// `rtdb.ErrPrecondition`, the heartbeat still names the winner, and the loser
// holds no lease and acts on nothing. That is why the lease needs no fourth node
// and no separate grant: the store's if-match is the fence (M1), and a consumer
// that forced its write past the 412 would put two consumers on one mailbox.
func TestTwoConsumersOneLease(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	path, err := HeartbeatPath(leaseMailbox)
	if err != nil {
		t.Fatalf("building the heartbeat path: %v", err)
	}

	// The loser's store: the real read, then a wait until the winner has
	// written. Both consumers therefore formed their take from the same (empty)
	// state and the same ETag — which is the race that matters, because the
	// module says `ok` to both.
	read := make(chan struct{})
	hold := make(chan struct{})
	loser := &countingStore{
		inner:     leaseClient(t, f),
		afterRead: func() { close(read); <-hold },
	}
	winner := &countingStore{inner: leaseClient(t, f)}

	type outcome struct {
		run LeaseRun
		err error
	}
	loserDone := make(chan outcome, 1)
	go func() {
		run, err := Take(context.Background(), loser, leaseOptions(otherHolder, leaseAt))
		loserDone <- outcome{run, err}
	}()

	<-read
	winnerRun, err := Take(context.Background(), winner, leaseOptions(leaseHolder, leaseAt))
	if err != nil {
		t.Fatalf("the winner's take: %v", err)
	}
	close(hold)

	var loserOutcome outcome
	select {
	case loserOutcome = <-loserDone:
	case <-time.After(30 * time.Second):
		t.Fatal("the loser's take never finished: a lost race must not spin")
	}

	if winnerRun.Verdict != LeaseHeld {
		t.Fatalf("the winner holds %q, not held: %+v", winnerRun.Verdict, winnerRun)
	}
	if loserOutcome.err != nil {
		t.Fatalf("the loser's take ended in an error rather than a lost race: %v", loserOutcome.err)
	}
	if loserOutcome.run.Verdict != LeaseLost {
		t.Errorf("the loser holds %q after losing the race: two consumers on one mailbox", loserOutcome.run.Verdict)
	}
	if loserOutcome.run.Holder != leaseHolder {
		t.Errorf("the loser is told the mailbox is held by %q, not by %q", loserOutcome.run.Holder, leaseHolder)
	}

	// The loser's take said `ok` — its write was attempted — and the store
	// refused it: that refusal is the fence.
	if loser.puts != 1 {
		t.Errorf("the loser wrote %d times; a lost race is one refused write, then a re-read", loser.puts)
	}
	if len(loser.errs) != 1 || !errors.Is(loser.errs[0], rtdb.ErrPrecondition) {
		t.Errorf("the loser's write was refused with %v, not the store's precondition error", loser.errs)
	}
	if !loser.absent {
		t.Error("the loser did not read an empty heartbeat, so the race this test stages was not the race it ran")
	}

	// Nothing moved: the heartbeat is still the winner's own.
	value := object(t, f.Value(path), "the heartbeat")
	if got := text(t, value["boot"], "boot"); got != leaseHolder {
		t.Errorf("the heartbeat names %q after the race, not the winner: both writes landed", got)
	}
	if got := text(t, value["mac"], "mac"); got != mustSign(t, leaseOptions(leaseHolder, leaseAt)) {
		t.Errorf("the heartbeat's tag is not the winner's, so the loser's write landed: %s", got)
	}
	if got, ok := intValue(value["at"]); !ok || got != leaseAt {
		t.Errorf("the heartbeat is stamped %v, not %d", value["at"], leaseAt)
	}

	// And the loser's next take is refused by the state, not by the ETag: the
	// lease is fresh and it is not the loser's, so the loser writes nothing.
	again, err := Take(context.Background(), loser, leaseOptions(otherHolder, leaseAt+1))
	if err != nil {
		t.Fatalf("the loser's second take: %v", err)
	}
	if again.Verdict != LeaseLost || again.Holder != leaseHolder {
		t.Errorf("the loser's second take holds the mailbox anyway: %+v", again)
	}
	if loser.puts != 1 {
		t.Errorf("the loser wrote %d times in total; a fresh lease it does not hold is never written", loser.puts)
	}
}

// TestLeaseRetryIsBounded: a store that keeps answering 412 ends the take
// rather than spinning. The loser re-reads and re-decides a bounded number of
// times, and the take ends in ErrLeaseContention with nothing written by this
// consumer.
func TestLeaseRetryIsBounded(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	path, _ := HeartbeatPath(leaseMailbox)

	// Every write is refused the way a moved ETag is, and every read answers an
	// empty heartbeat — so the take re-decides and re-writes, forever if it
	// were not bounded.
	always := &refusingStore{inner: leaseClient(t, f)}
	run, err := Take(context.Background(), always, LeaseOptions{
		Mailbox: leaseMailbox, Holder: leaseHolder, At: leaseAt,
		Sign: stubSigner("x"), Attempts: 3,
	})
	if !errors.Is(err, ErrLeaseContention) {
		t.Fatalf("a store that always forgets the ETag ended as %v (%+v), not ErrLeaseContention", err, run)
	}
	if always.puts != 3 || always.gets != 3 {
		t.Errorf("the take made %d reads and %d writes for a bound of 3: the retry is not the bound it says it is", always.gets, always.puts)
	}
	if f.Value(path) != nil {
		t.Errorf("the heartbeat was written (%v) by a take that lost every race", f.Value(path))
	}
}

// refusingStore answers every conditional write with the store's precondition
// error, and every read with an empty heartbeat.
type refusingStore struct {
	inner HeartbeatStore
	gets  int
	puts  int
}

func (s *refusingStore) GetETag(ctx context.Context, path string, out any) (string, bool, error) {
	s.gets++
	// The read is the real one, so the ETag is real and the state is empty; it
	// is the write below that always loses.
	return s.inner.GetETag(ctx, path, out)
}

func (s *refusingStore) PutIfMatch(ctx context.Context, path, etag string, v any) error {
	s.puts++
	return fmt.Errorf("the ETag moved: %w", rtdb.ErrPrecondition)
}

// TestLeaseSkewBoundariesOverTheStore is the plan's skew rule at the store: a
// heartbeat five minutes ahead of this consumer's clock is honoured (its holder
// is doing the work, and two clocks disagreeing is not a dead holder), and one
// five minutes and one millisecond ahead is taken over — so no timestamp can
// hold a mailbox for ever.
func TestLeaseSkewBoundariesOverTheStore(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ahead   int64
		verdict LeaseVerdict
	}{
		{"five minutes ahead is held", MaxClockSkew, LeaseLost},
		{"five minutes and a millisecond ahead is taken over", MaxClockSkew + 1, LeaseTakenOver},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := gcpfake.NewRTDB(t)
			path, _ := HeartbeatPath(leaseMailbox)
			// The other consumer's heartbeat, stamped ahead of our clock.
			f.Set(path, map[string]any{
				"at":   leaseAt + tc.ahead,
				"boot": otherHolder,
				"mac":  mustSign(t, leaseOptions(otherHolder, leaseAt+tc.ahead)),
			})

			store := &countingStore{inner: leaseClient(t, f)}
			run, err := Take(context.Background(), store, leaseOptions(leaseHolder, leaseAt))
			if err != nil {
				t.Fatalf("the take: %v", err)
			}
			if run.Verdict != tc.verdict {
				t.Fatalf("the take answered %q; this lease should be %q", run.Verdict, tc.verdict)
			}
			value := object(t, f.Value(path), "the heartbeat")
			switch tc.verdict {
			case LeaseLost:
				if store.puts != 0 {
					t.Errorf("a lease we do not hold was written %d times", store.puts)
				}
				if got := text(t, value["boot"], "boot"); got != otherHolder {
					t.Errorf("the heartbeat now names %q: a fresh lease was overwritten", got)
				}
			case LeaseTakenOver:
				if store.puts != 1 {
					t.Errorf("the takeover wrote %d times, not once", store.puts)
				}
				if got := text(t, value["boot"], "boot"); got != leaseHolder {
					t.Errorf("the heartbeat still names %q: the takeover did not happen", got)
				}
				if run.ExpiresAt != leaseAt+ClaimTTL {
					t.Errorf("the taken lease expires at %d, not %d", run.ExpiresAt, leaseAt+ClaimTTL)
				}
			}
		})
	}
}

// TestLeaseConfirmBeforeActingPastTheBound is the module's caller obligation:
// a holder stops acting by `claim.at + ttl − MAX_CLOCK_SKEW`, or re-reads the
// lease before it acts. ActionBound is that instant, and Confirm is that
// re-read: it answers held while the heartbeat still names this consumer's own
// fresh claim, and lost as soon as it does not — which is what stops a
// consumer acting on a lease another consumer may already hold.
func TestLeaseConfirmBeforeActingPastTheBound(t *testing.T) {
	hold := Lease{Holder: leaseHolder, At: leaseAt}
	bound := ActionBound(hold, ClaimTTL)
	if want := leaseAt + ClaimTTL - MaxClockSkew; bound != want {
		t.Fatalf("ActionBound is %d, and claim.at + ttl − MAX_CLOCK_SKEW is %d", bound, want)
	}
	if bound >= leaseAt+ClaimTTL {
		t.Fatal("the bound is not before the claim goes stale: re-reading there would be too late")
	}

	f := gcpfake.NewRTDB(t)
	path, _ := HeartbeatPath(leaseMailbox)
	ours := map[string]any{
		"at":   leaseAt,
		"boot": leaseHolder,
		"mac":  mustSign(t, leaseOptions(leaseHolder, leaseAt)),
	}
	f.Set(path, ours)

	store := &countingStore{inner: leaseClient(t, f)}
	run, err := Confirm(context.Background(), store, leaseOptions(leaseHolder, bound))
	if err != nil {
		t.Fatalf("the re-read at the bound: %v", err)
	}
	if run.Verdict != LeaseHeld {
		t.Fatalf("our own fresh claim is %q at the bound, not held: %+v", run.Verdict, run)
	}
	if store.puts != 0 {
		t.Errorf("the re-read wrote %d times: it is a read, and the heartbeat is not written until the next take", store.puts)
	}

	// Another consumer takes the mailbox over, and the same re-read now stops
	// this one acting.
	f.Set(path, map[string]any{
		"at":   bound + 1,
		"boot": otherHolder,
		"mac":  mustSign(t, leaseOptions(otherHolder, bound+1)),
	})
	lost, err := Confirm(context.Background(), store, leaseOptions(leaseHolder, bound+2))
	if err != nil {
		t.Fatalf("the re-read after a takeover: %v", err)
	}
	if lost.Verdict != LeaseLost || lost.Holder != otherHolder {
		t.Errorf("the re-read answered %+v after another consumer took the mailbox", lost)
	}
	if store.puts != 0 {
		t.Errorf("the losing re-read wrote %d times", store.puts)
	}

	// And a claim of our own that has gone stale is not ours to act on either:
	// another consumer may already hold it.
	f.Set(path, ours)
	stale, err := Confirm(context.Background(), store, leaseOptions(leaseHolder, leaseAt+ClaimTTL+1))
	if err != nil {
		t.Fatalf("the re-read of a stale claim: %v", err)
	}
	if stale.Verdict != LeaseLost {
		t.Errorf("a stale claim of our own answered %q, and past its TTL it is not the holder's to act on", stale.Verdict)
	}
}

// TestLeaseNoHeartbeatTakesNoLease: `--no-heartbeat` takes no lease and writes
// nothing — the store is never touched at all — so a consumer in that mode acts
// on nothing. The verdict says so, and nothing is left for a caller to
// misinterpret as a lease.
func TestLeaseNoHeartbeatTakesNoLease(t *testing.T) {
	store := &failingStore{t: t, why: "no heartbeat mode must not touch the store"}
	for _, tc := range []struct {
		name string
		call func(context.Context, HeartbeatStore, LeaseOptions) (LeaseRun, error)
	}{
		{"take", Take},
		{"confirm", Confirm},
	} {
		opts := leaseOptions(leaseHolder, leaseAt)
		opts.NoHeartbeat = true
		run, err := tc.call(context.Background(), store, opts)
		if err != nil {
			t.Fatalf("%s with --no-heartbeat: %v", tc.name, err)
		}
		if run.Verdict != LeaseNone || run.Lease != nil || run.Attempts != 0 {
			t.Errorf("%s with --no-heartbeat answered %+v, not the no-lease verdict", tc.name, run)
		}
	}
}

// failingStore fails the test if the lease touches it at all.
type failingStore struct {
	t   *testing.T
	why string
}

func (s *failingStore) GetETag(context.Context, string, any) (string, bool, error) {
	s.t.Errorf("the lease read the store: %s", s.why)
	return "", false, errors.New("unreachable")
}

func (s *failingStore) PutIfMatch(context.Context, string, string, any) error {
	s.t.Errorf("the lease wrote the store: %s", s.why)
	return errors.New("unreachable")
}

// TestLeaseSignerFailureWritesNothing: the heartbeat write IS the lease, so a
// heartbeat this consumer cannot sign is not written — and therefore no lease
// is taken and nothing may be acted on (M4b). A take with no signer at all is
// the same refusal.
func TestLeaseSignerFailureWritesNothing(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	path, _ := HeartbeatPath(leaseMailbox)
	store := &countingStore{inner: leaseClient(t, f)}

	failing := leaseOptions(leaseHolder, leaseAt)
	failing.Sign = func(Heartbeat) (string, error) { return "", errors.New("the key is gone") }
	if _, err := Take(context.Background(), store, failing); err == nil {
		t.Fatal("a take whose heartbeat cannot be signed succeeded")
	}
	if store.puts != 0 || f.Value(path) != nil {
		t.Errorf("an unsignable heartbeat was written (%v) after %d writes", f.Value(path), store.puts)
	}

	unsigned := leaseOptions(leaseHolder, leaseAt)
	unsigned.Sign = nil
	if _, err := Take(context.Background(), store, unsigned); !errors.Is(err, ErrLeaseNoSigner) {
		t.Errorf("a take with no signer ended as %v, not ErrLeaseNoSigner", err)
	}
	if store.puts != 0 || f.Value(path) != nil {
		t.Errorf("a signerless take wrote the heartbeat (%v)", f.Value(path))
	}

	// And a signature the schema refuses is not written either: the value is
	// validated before the write, so the store never sees a heartbeat the other
	// end would refuse.
	bad := leaseOptions(leaseHolder, leaseAt)
	bad.Sign = func(Heartbeat) (string, error) { return "not-a-tag", nil }
	if _, err := Take(context.Background(), store, bad); err == nil {
		t.Fatal("a heartbeat with a tag the schema refuses was written")
	}
	if store.puts != 0 || f.Value(path) != nil {
		t.Errorf("a malformed heartbeat was written (%v)", f.Value(path))
	}
}

// TestLeaseUnreadableHeartbeatIsRefused: a heartbeat this consumer cannot read
// is not a claim it may take over. An unreadable claim is an ambiguity about
// who holds the mailbox, and the module's answer to an ambiguity is a refusal —
// this end fails closed and writes nothing.
func TestLeaseUnreadableHeartbeatIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"no tag", map[string]any{"at": leaseAt, "boot": otherHolder}},
		{"no boot", map[string]any{"at": leaseAt, "mac": strings.Repeat("a", 64)}},
		{"a field the schema does not name", map[string]any{"at": leaseAt, "boot": otherHolder, "mac": strings.Repeat("a", 64), "holder": otherHolder}},
		{"not an object", "a string where the heartbeat should be"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := gcpfake.NewRTDB(t)
			path, _ := HeartbeatPath(leaseMailbox)
			f.Set(path, tc.value)
			store := &countingStore{inner: leaseClient(t, f)}

			if _, err := Take(context.Background(), store, leaseOptions(leaseHolder, leaseAt)); err == nil {
				t.Fatal("a heartbeat this consumer cannot read was taken over")
			}
			if store.puts != 0 {
				t.Errorf("the unreadable heartbeat was written over %d times", store.puts)
			}
			if got := f.Value(path); mapCanonical(t, got) != mapCanonical(t, tc.value) {
				t.Errorf("the heartbeat changed: %v", got)
			}
		})
	}
}

// TestLeaseReadAndWriteAreTheOperatorCredential is C0's half that this task can
// assert: the ETag read and the conditional write carry the operator's ADC —
// the Bearer credential — and never the phone's ID-token spelling. The other
// half, that a token bearing `mc == $mb` is refused the heartbeat GET by the
// merged rules, is the rules emulator's, and is not written here.
func TestLeaseReadAndWriteAreTheOperatorCredential(t *testing.T) {
	f := gcpfake.NewRTDB(t)
	if _, err := Take(context.Background(), leaseClient(t, f), leaseOptions(leaseHolder, leaseAt)); err != nil {
		t.Fatalf("the take: %v", err)
	}
	// A take that wins on its first attempt is exactly two requests: the ETag
	// read and the conditional write.
	creds := f.Credentials()
	if len(creds) != 2 {
		t.Fatalf("the take made %d requests; a lease is one read and one conditional write", len(creds))
	}
	for _, cred := range creds {
		if !strings.HasPrefix(cred, "bearer:") {
			t.Errorf("a request carried %q, not the operator's ADC (a Bearer credential): the lease's read is the operator's (C0), and the lease may never be taken with the phone's credential", cred)
		}
		if !strings.Contains(cred, "ya29.operator-secret") {
			t.Errorf("a request carried %q, not the credential the client was opened with", cred)
		}
	}
}

// TestLeaseHeartbeatPath: the lease lives in the one node M1 names, and a
// mailbox name this command would build a path from is a name the schema
// accepts — anything else is refused rather than escaped.
func TestLeaseHeartbeatPath(t *testing.T) {
	path, err := HeartbeatPath(leaseMailbox)
	if err != nil {
		t.Fatalf("the heartbeat path: %v", err)
	}
	if want := "mailbox/" + leaseMailbox + "/heartbeat"; path != want {
		t.Errorf("the heartbeat path is %q, not %q", path, want)
	}
	for _, bad := range []string{"", " ", "a/b", "a.b", "a#b", "a[b]", "$mb", "..", "%20", "a\nb", "a\x7fb", strings.Repeat("x", SenderMax+1)} {
		if _, err := HeartbeatPath(bad); err == nil {
			t.Errorf("%q is accepted as a mailbox name", bad)
		}
	}
}

// mustSign is what the stub signer produces for a heartbeat: the tag a test
// expects to find on the node.
func mustSign(t *testing.T, opts LeaseOptions) string {
	t.Helper()
	tag, err := opts.Sign(Heartbeat{At: opts.At, Boot: opts.Holder})
	if err != nil {
		t.Fatalf("signing the heartbeat: %v", err)
	}
	return tag
}
