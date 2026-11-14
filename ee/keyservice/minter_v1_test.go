// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The minter's v1 half: a key built from a row of the default policy, the
// renewal secret that goes out beside it while only its hash goes into the key,
// and the air-gapped row whose key never contacts the server at all. Every key
// minted here is read back through ee/licence's Verify, so what these tests
// hold is what a released build accepts.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// workspaceID is the Firebase project id of a workspace, which every v1 key
// binds to and every verifier compares (D2134 as amended by TN2). It is
// obviously not a real project's id.
const workspaceID = "acme-widgets"

// Both halves of the split are the intake's route to a v1 key: the minter
// signs, and the client asks it to. A half that stopped satisfying MintsV1
// would leave the service with no way to mint a v1 key at all.
var (
	_ MintsV1 = Minter{}
	_ MintsV1 = RemoteMints{}
)

// v1Request is the request the service builds for one row of the policy table.
func v1Request(policy string) RequestV1 {
	return RequestV1{
		Environment:  Production,
		Policy:       policy,
		WorkspaceID:  workspaceID,
		Organisation: "acme.example",
		Seats:        3,
		TermDays:     30,
	}
}

// v1Payload is a minted key's payload, decoded the way a verifier decodes it.
// A test reads the payload as well as the claim set because "the key carries
// this and not that" is a fact about the text a verifier signs over.
func v1Payload(t *testing.T, key string) []byte {
	t.Helper()
	rest, ok := strings.CutPrefix(key, keyPrefixV1)
	if !ok {
		t.Fatalf("the minted key %q does not carry the prefix a v1 key is written with", key)
	}
	encoded, _, ok := strings.Cut(rest, ".")
	if !ok {
		t.Fatalf("the minted key %q is not a payload and a signature", key)
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("the minted key's payload does not decode: %v", err)
	}
	return payload
}

// v1Verified is a minted key read the way a released verifier reads it, refused
// here when it is not valid at the instant it was minted.
func v1Verified(t *testing.T, f *fixture, key string) licence.Claims {
	t.Helper()
	claims, state, err := licence.Verify([]byte(key), f.now, f.ring, workspaceID)
	if err != nil {
		t.Fatalf("the minted key does not verify: %v", err)
	}
	if state != licence.Active {
		t.Fatalf("the minted key reads %s at the instant it was minted, want active", state)
	}
	return claims
}

// secretHash is the digest of a renewal secret, recomputed here rather than read
// from the package's own helper: what the claim carries is the scheme a renewal
// will be checked with (K2.3), and a test that asked the minter for it would
// agree with the code instead of with the server.
func secretHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// TestKeyHoldsOnlyTheSecretsHash: D2135 — a key carries the hash of its renewal
// secret and never the secret. The hash is in the key, which every reader of the
// runs bucket holds; the secret is what a renewal authenticates with, so a key
// that carried it would let any bucket reader renew, and the record — written to
// a store and read by every renewal afterwards — must not carry it either.
func TestKeyHoldsOnlyTheSecretsHash(t *testing.T) {
	f := newFixture(t)
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: Production, Now: f.clock}
	minted, err := minter.MintV1(context.Background(), v1Request(PolicyTeamMonthly))
	if err != nil {
		t.Fatalf("minting a team key: %v", err)
	}

	// The secret is 256 bits of the system's randomness, whole: it is the only
	// thing that authenticates a renewal, and a secret derived from anything a
	// reader of the key holds is not one.
	raw, err := base64.RawURLEncoding.DecodeString(minted.RenewalSecret)
	if err != nil {
		t.Fatalf("the renewal secret is not the base64url the bundle carries: %v", err)
	}
	if len(raw)*8 != 256 {
		t.Errorf("the renewal secret is %d bit(s), want 256 (D2135)", len(raw)*8)
	}

	claims := v1Verified(t, f, minted.Key)
	if claims.RenewalSecretHash != secretHash(minted.RenewalSecret) {
		t.Errorf("the key carries %q as its renewal secret's hash, want the digest of the secret it was answered with", claims.RenewalSecretHash)
	}
	if claims.RenewalSecretHash == minted.RenewalSecret {
		t.Error("the key carries the renewal secret itself, want only its hash (D2135)")
	}
	for what, text := range map[string]string{
		"the key text":  minted.Key,
		"the payload":   string(v1Payload(t, minted.Key)),
		"the claim set": string(mustJSON(t, claims)),
		"the record":    string(mustJSON(t, minted.Record)),
	} {
		if strings.Contains(text, minted.RenewalSecret) {
			t.Errorf("%s carries the renewal secret: it goes to the holder as the second line of the bundle and is stored nowhere, and the key, the payload and the record are all read by callers the secret is not for (D2135, §7.3)", what)
		}
	}

	// The record is the key's own identity and not the key ring's: a revocation
	// and a renewal both name the key (K2.2, K2.3), and a reissue is a new key
	// under a new id.
	if minted.Record.KeyID != claims.KeyID {
		t.Errorf("the record names the key %q and the key names itself %q, want one id: the store's revocations and renewals are both by it", minted.Record.KeyID, claims.KeyID)
	}
	if minted.Record.KeyID == productionKeyID {
		t.Errorf("the record names the signing key id %q as the key's own id, want the minted key's id", productionKeyID)
	}
	if !strings.HasPrefix(minted.Record.KeyID, keyIDPrefix) {
		t.Errorf("the minted key id %q does not carry the %q prefix a support ticket is read by", minted.Record.KeyID, keyIDPrefix)
	}
	if minted.Record.Plan != PolicyTeamMonthly || minted.Record.Tier != licence.TierTeam || minted.Record.Seats != 3 {
		t.Errorf("the record is %+v, want the row it was minted from and the seats it was asked for", minted.Record)
	}
	if minted.Record.Issued != f.now || minted.Record.Expires.Sub(minted.Record.Issued) != 30*day {
		t.Errorf("the record runs %s..%s, want the minter's own clock and the term the request named", minted.Record.Issued, minted.Record.Expires)
	}

	// Nothing about the pair is derived from the row: two keys of one tier are
	// two keys, with two ids and two secrets, which is what makes revoking one
	// of them a thing that can be done.
	second, err := minter.MintV1(context.Background(), v1Request(PolicyTeamMonthly))
	if err != nil {
		t.Fatalf("minting a second team key: %v", err)
	}
	if second.Record.KeyID == minted.Record.KeyID {
		t.Error("two keys of one tier share a key id, want one id per key: a revocation is by id (K2.2)")
	}
	if second.RenewalSecret == minted.RenewalSecret {
		t.Error("two keys of one tier share a renewal secret, want one per key: revoking one would otherwise stop the other renewing (D2135)")
	}
	if got := v1Verified(t, f, second.Key).RenewalSecretHash; got != secretHash(second.RenewalSecret) {
		t.Errorf("the second key carries %q as its secret's hash, want its own secret's digest", got)
	}
}

// TestAirGappedKeyNeverChecks: spec section 8 with D2138 — a key whose
// `check_interval` is null is never checked at all, so the air-gapped row is the
// one row that mints a key nothing contacts the server about; it renews by hand
// or not at all, and its 30 days of grace are the whole of what stands between
// the end of its term and a lapse nobody could have prevented.
func TestAirGappedKeyNeverChecks(t *testing.T) {
	f := newFixture(t)
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: Production, Now: f.clock}

	request := v1Request(PolicyEnterpriseAirGapped)
	request.TermDays = 365
	minted, err := minter.MintV1(context.Background(), request)
	if err != nil {
		t.Fatalf("minting an air-gapped enterprise key: %v", err)
	}
	claims := v1Verified(t, f, minted.Key)
	if claims.CheckInterval != nil {
		t.Errorf("the air-gapped key checks every %q, want no check interval at all: it never contacts the server (section 8)", *claims.CheckInterval)
	}
	if !strings.Contains(string(v1Payload(t, minted.Key)), `"check_interval":null`) {
		t.Error("the air-gapped key does not say `check_interval: null`: a key that never checks says so in the format's own words, and a reader that found the field absent could read it either way")
	}
	if claims.RenewalMode != licence.RenewalNone || claims.RenewBeforeDays != 0 {
		t.Errorf("the air-gapped key renews %s %d day(s) early, want none and no window: the job that would ask the server is what the key says not to run (section 7)", claims.RenewalMode, claims.RenewBeforeDays)
	}
	if claims.GraceDays != 30 {
		t.Errorf("the air-gapped key carries %d day(s) of grace, want 30 (D2138)", claims.GraceDays)
	}
	if claims.Tier != licence.TierEnterprise || claims.Kind != licence.KindPaid {
		t.Errorf("the air-gapped key is a %s %s key, want a paid enterprise one", claims.Kind, claims.Tier)
	}

	// The connected row of the same tier is the contrast the row exists for: it
	// checks daily, and it is reached by the same request with one other policy.
	connected, err := minter.MintV1(context.Background(), v1Request(PolicyEnterprise))
	if err != nil {
		t.Fatalf("minting a connected enterprise key: %v", err)
	}
	enabled := v1Verified(t, f, connected.Key)
	if enabled.CheckInterval == nil || *enabled.CheckInterval != dailyCheck {
		t.Errorf("the connected enterprise key checks %v, want %s: revocation is separate from expiry, and a connected key is checked (section 8)", enabled.CheckInterval, dailyCheck)
	}
	if enabled.RenewalMode != licence.RenewalAuto {
		t.Errorf("the connected enterprise key renews %s, want %s", enabled.RenewalMode, licence.RenewalAuto)
	}

	// A trial is the other row D2138 names: no grace at all, and it checks
	// daily. A request that names no term is minted for the row's own lifetime,
	// which is what a first issue of that plan is.
	trial, err := minter.MintV1(context.Background(), RequestV1{
		Environment:  Production,
		Policy:       PolicyTrial,
		WorkspaceID:  workspaceID,
		Organisation: "acme.example",
		Seats:        1,
	})
	if err != nil {
		t.Fatalf("minting a trial key: %v", err)
	}
	trialClaims := v1Verified(t, f, trial.Key)
	if trialClaims.GraceDays != 0 {
		t.Errorf("the trial carries %d day(s) of grace, want none: a trial that has run out is over (D2138)", trialClaims.GraceDays)
	}
	if trialClaims.Kind != licence.KindTrial || trialClaims.RenewalMode != licence.RenewalNone {
		t.Errorf("the trial key is a %s key renewing %s, want a trial that nothing renews", trialClaims.Kind, trialClaims.RenewalMode)
	}
	if trialClaims.CheckInterval == nil || *trialClaims.CheckInterval != dailyCheck {
		t.Errorf("the trial checks %v, want %s (section 12.1)", trialClaims.CheckInterval, dailyCheck)
	}
	if got := trialClaims.PaidThrough.Sub(trialClaims.IssuedAt); got != 14*day {
		t.Errorf("the trial runs %s, want the row's fourteen days: a request that names no term is minted for the row's own lifetime", got)
	}
	if trialClaims.Warnings != nil {
		t.Errorf("the trial carries %d warning window(s), want none: its warnings count from a grace it does not have", len(trialClaims.Warnings))
	}
}

// TestTheV1MinterRefusesWhatItCannotMint: every request the minter will not sign
// is refused **before** the key ring is called, and each case is one guard. What
// the guards keep out is a key that verifies nowhere: a v1 key with no workspace
// id is refused by every verifier that reads it (TN2), a tier no row names has
// no ceiling, and the counted signer calls are what say a refusal came before a
// signature rather than after it.
func TestTheV1MinterRefusesWhatItCannotMint(t *testing.T) {
	f := newFixture(t)
	valid := v1Request(PolicyTeamMonthly)
	for _, tc := range []struct {
		name string
		// termDays overrides the request's term; the zero value is left to the
		// case's own edit, so "names no term" is stated where it is meant.
		termDays int
		// size is what the key ring answers instead of a signature, and err what
		// it answers instead of one at all.
		size      int
		signerErr error
		edit      func(*RequestV1, *Minter)
		// signed is how many times the signer must have been asked, and mints
		// whether the request is one the minter serves.
		signed int
		mints  bool
		// term is the term the minted key must run for.
		term time.Duration
		// notTheCeiling says the refusal has to be the guard's own and not the
		// tier ceiling's: a policy no row names has no tier, so a missing guard
		// reads the ceiling as zero and refuses by a rule that is not true of
		// the request — and the two have different repairs. ceiling says the
		// opposite: the refusal has to be the ceiling's.
		notTheCeiling bool
		ceiling       bool
	}{
		{name: "the request the service builds", termDays: 30, signed: 1, mints: true, term: 30 * day},
		{name: "a request that names no term", termDays: 0, signed: 1, mints: true, term: 30 * day},
		{name: "a policy no key is issued under", termDays: 30, edit: func(r *RequestV1, _ *Minter) { r.Policy = "team-plus" }, notTheCeiling: true},
		{name: "no policy at all", termDays: 30, edit: func(r *RequestV1, _ *Minter) { r.Policy = "" }, notTheCeiling: true},
		{name: "a staging minter and a production request", termDays: 30, edit: func(_ *RequestV1, m *Minter) { m.Environment = Staging }},
		{name: "a request naming no environment", termDays: 30, edit: func(r *RequestV1, _ *Minter) { r.Environment = "" }},
		{name: "no workspace id", termDays: 30, edit: func(r *RequestV1, _ *Minter) { r.WorkspaceID = "" }},
		{name: "a workspace id of whitespace", termDays: 30, edit: func(r *RequestV1, _ *Minter) { r.WorkspaceID = " \t " }},
		{name: "no seats", termDays: 30, edit: func(r *RequestV1, _ *Minter) { r.Seats = 0 }},
		{name: "a negative number of seats", termDays: 30, edit: func(r *RequestV1, _ *Minter) { r.Seats = -3 }},
		{name: "no organisation", termDays: 30, edit: func(r *RequestV1, _ *Minter) { r.Organisation = "" }},
		{name: "an organisation of whitespace", termDays: 30, edit: func(r *RequestV1, _ *Minter) { r.Organisation = " \t " }},
		{name: "a term a day past the tier's ceiling", termDays: 396, edit: func(r *RequestV1, _ *Minter) { r.Policy = PolicyTeamAnnual }, ceiling: true},
		// The term arrives as an int from a wire, so it is a term that cannot be
		// turned into a duration at all: `time.Duration(days) * 24 * time.Hour`
		// wraps, and a wrapped term is a short one that passes a ceiling it
		// should fail — minting a key whose `paid_through` is before its
		// `issued_at`.
		{name: "a term too large for a duration", termDays: math.MaxInt, ceiling: true},
		{name: "a negative term", termDays: -30},
		// A row nothing renews is minted for its own lifetime: a trial is
		// D2136's fourteen days and an air-gapped key is replaced by hand when
		// the next year is paid for, so a caller that named either a longer span
		// would be granting what the row says it does not (section 7, D2138).
		// The row's own term is still taken, because a caller may say it.
		// The trial row names its own seat, so the requests below that mint from
		// it name that one: what those cases are about is the row's term, and the
		// seat a trial carries is the row's by §10.3 (B6).
		{name: "a trial for a term of its own choosing", termDays: 30, edit: func(r *RequestV1, _ *Minter) { r.Policy, r.Seats = PolicyTrial, trialSeats }, notTheCeiling: true},
		{name: "a trial for its own fourteen days", termDays: 14, edit: func(r *RequestV1, _ *Minter) { r.Policy, r.Seats = PolicyTrial, trialSeats }, signed: 1, mints: true, term: 14 * day},
		// A row whose seats are its own takes none other: the trial row names
		// one, so a request naming more is minted for nobody (D2184, §10.3).
		{name: "a trial for more seats than its row names", termDays: 14, edit: func(r *RequestV1, _ *Minter) { r.Policy, r.Seats = PolicyTrial, 5 }},
		{name: "an air-gapped key for more than its own year", termDays: 366, edit: func(r *RequestV1, _ *Minter) { r.Policy = PolicyEnterpriseAirGapped }, notTheCeiling: true},
		{name: "an air-gapped key for its own year", termDays: 365, edit: func(r *RequestV1, _ *Minter) { r.Policy = PolicyEnterpriseAirGapped }, signed: 1, mints: true, term: 365 * day},
		{name: "no key id for the environment", termDays: 30, edit: func(_ *RequestV1, m *Minter) { m.Keys = Keys{Staging: stagingKeyID} }},
		{name: "no signer", termDays: 30, edit: func(_ *RequestV1, m *Minter) { m.Signer = nil }},
		{name: "a key ring that fails", termDays: 30, signerErr: errors.New("the key ring is unreachable"), signed: 1},
		{name: "a key ring answering a short signature", termDays: 30, size: 32, signed: 1},
		{name: "a key ring answering a long signature", termDays: 30, size: 128, signed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &signerSpy{priv: f.signer.priv, size: tc.size, err: tc.signerErr}
			minter := Minter{Signer: spy, Keys: f.keys, Environment: Production, Now: f.clock}
			request := valid
			request.TermDays = tc.termDays
			if tc.edit != nil {
				tc.edit(&request, &minter)
			}
			minted, err := minter.MintV1(context.Background(), request)
			if spy.calls != tc.signed {
				t.Errorf("the minter asked the key ring %d time(s) for %s, want %d: a request it will not sign is refused before the signer is reached", spy.calls, tc.name, tc.signed)
			}
			if tc.mints {
				if err != nil {
					t.Fatalf("the minter refused the request the service builds: %v", err)
				}
				if got := v1Verified(t, f, minted.Key).PaidThrough.Sub(minted.Record.Issued); got != tc.term {
					t.Errorf("the minted key runs %s, want %s", got, tc.term)
				}
				return
			}
			if err == nil {
				t.Errorf("the minter served %s, want a refusal before the signer is reached", tc.name)
				return
			}
			if tc.notTheCeiling && errors.Is(err, licence.ErrLifetime) {
				t.Errorf("the minter refused %s by the tier ceiling, want the guard's own refusal: %v", tc.name, err)
			}
			if tc.ceiling && !errors.Is(err, licence.ErrLifetime) {
				t.Errorf("the minter refused %s by some other rule, want the tier ceiling: %v", tc.name, err)
			}
		})
	}
}

// TestTheIntakeMintsAV1KeyThroughTheMinterService: §3.2 — the intake reaches the
// v1 mint over the minter's own route with an ID token, exactly as it reaches
// the older format's, and the renewal secret is what crosses that call. An
// answer without one is refused at the intake's half rather than handed on: a
// key whose secret nobody received is a key that can never renew (D2135).
func TestTheIntakeMintsAV1KeyThroughTheMinterService(t *testing.T) {
	f := newFixture(t)
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: Production, Now: f.clock}
	var presented, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, path = r.Header.Get("Authorization"), r.URL.Path
		minter.Handler().ServeHTTP(w, r)
	}))
	defer srv.Close()

	mints := RemoteMints{URL: srv.URL, Client: srv.Client(), Tokens: staticTokens("id-token-for-the-minter")}
	request := v1Request(PolicyTeamAnnual)
	request.TermDays = 365
	minted, err := mints.MintV1(context.Background(), request)
	if err != nil {
		t.Fatalf("minting through the intake's half: %v", err)
	}
	if presented != "Bearer id-token-for-the-minter" {
		t.Errorf("the intake presented %q to the minter, want the ID token the platform's check reads", presented)
	}
	if path != RouteMintV1 {
		t.Errorf("the intake called %q, want the v1 route %q: the older format's route reads a different body and refuses this one", path, RouteMintV1)
	}
	claims := v1Verified(t, f, minted.Key)
	if minted.RenewalSecret == "" {
		t.Fatal("the minter answered no renewal secret, and the bundle an issue path shows is the key and the secret")
	}
	if claims.RenewalSecretHash != secretHash(minted.RenewalSecret) {
		t.Errorf("the key crossed the wire carrying %q as its secret's hash, want the digest of the secret that crossed beside it", claims.RenewalSecretHash)
	}
	if got := f.signer.calls; got != 1 {
		t.Errorf("the minter signed %d key(s), want one", got)
	}

	t.Run("the route refuses a claim the caller names itself", func(t *testing.T) {
		body := `{"environment":"production","policy":"team-monthly","workspace_id":"` + workspaceID +
			`","organisation":"acme.example","seats":3,"term_days":30,"grace_days":9000}`
		resp, err := srv.Client().Post(srv.URL+RouteMintV1, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("calling the v1 route: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("the route answered %d for a request carrying a grace of its own, want 400: a caller that could name a row's own fields would mint a key that never lapses, and they are the row's (D2133, D2138)", resp.StatusCode)
		}
	})

	t.Run("an answer without a renewal secret is refused", func(t *testing.T) {
		bare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, `{"key":"fgk_a.b","record":{"key_id":"lk_acme"}}`)
		}))
		defer bare.Close()
		mints := RemoteMints{URL: bare.URL, Client: bare.Client(), Tokens: staticTokens("id-token")}
		if _, err := mints.MintV1(context.Background(), v1Request(PolicyTeamMonthly)); err == nil {
			t.Error("the intake's half accepted a mint with no renewal secret, want a refusal: a key nobody can renew is not one this service issues (D2135)")
		}
	})

	// The one answer with no secret the intake's half does pass on, and the lane
	// it exists for: a reissue that keeps the entitlement's existing hash is
	// answered with no new secret (D2200), and a client that refused it would
	// leave that reissue unable to reach the minter anywhere but a test's own
	// in-process wiring.
	t.Run("a reissue that keeps the entitlement's hash crosses with no secret", func(t *testing.T) {
		kept := digest("the secret this entitlement was issued with")
		request := v1Request(PolicyTeamMonthly)
		request.RenewalSecretHash = kept
		reissued, err := mints.MintV1(context.Background(), request)
		if err != nil {
			t.Fatalf("the intake's half refused the minter's reissue answer: %v", err)
		}
		if reissued.RenewalSecret != "" {
			t.Errorf("the reissue answered the secret %q, want none: the holder keeps the one they were sent (D2200)", reissued.RenewalSecret)
		}
		if got := v1Verified(t, f, reissued.Key).RenewalSecretHash; got != kept {
			t.Errorf("the reissued key crossed the wire carrying %q, want the hash the request kept (%q)", got, kept)
		}
	})
}

// TestARequestLeavesTheFieldsItNamesNothingInOffTheWire: the two halves of the
// split are deployed one after the other, and the minter reads its request
// strictly — a field it has never heard of is a refusal (the route's own
// `DisallowUnknownFields`). So a request that carries the zero value of a field
// a minter from before this release does not know is a 400 on every mint for as
// long as the deploy takes, which is the one window in which the intake is
// newer than the minter it asks. What a request names nothing in is left out,
// and the three are the row's own until a caller names them.
func TestARequestLeavesTheFieldsItNamesNothingInOffTheWire(t *testing.T) {
	fields := []string{"requested_end", "check_interval", "renewal_secret_hash"}
	unnamed := string(mustJSON(t, v1Request(PolicyTeamAnnual)))
	for _, field := range fields {
		if strings.Contains(unnamed, field) {
			t.Errorf("a request that names no %s carries the key anyway (%s), want it left out: a minter from before this release refuses a field it does not know, and the refusal is every mint", field, unnamed)
		}
	}

	ask := v1Request(PolicyTeamAnnual)
	ask.RequestedEnd = time.Date(2026, 12, 10, 12, 0, 0, 0, time.UTC)
	ask.CheckInterval = "P1D"
	ask.RenewalSecretHash = digest(freshSecret(t))
	carried := string(mustJSON(t, ask))
	for _, field := range fields {
		if !strings.Contains(carried, field) {
			t.Errorf("a request that names a %s leaves it off the wire (%s), want it carried: what a caller names is what the minter bounds and applies", field, carried)
		}
	}
}

// mustJSON is a value as JSON, for the tests that read what a key, a claim set
// or a record does and does not carry.
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %T: %v", value, err)
	}
	return encoded
}
