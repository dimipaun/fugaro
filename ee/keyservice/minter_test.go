// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The minter and the wire the intake reaches it over (subscriptions plan S3.2;
// the design docs/design/key-signing-service.md §2, §3.2). The two facts these
// tests hold are the ones no other task states: the minter dates every key
// itself, and its ceiling is the verifier's ceiling.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// TestTheMinterTakesNoDateFromItsCaller: §2 — a caller supplies the tier, the
// claims and the term, and it cannot supply a date, which is the one rule this
// design adds to S2.1's. A field of the request that is an instant is how that
// hole would come back through a wire the service itself serves.
func TestTheMinterTakesNoDateFromItsCaller(t *testing.T) {
	instant := reflect.TypeOf(time.Time{})
	request := reflect.TypeOf(Request{})
	for i := 0; i < request.NumField(); i++ {
		field := request.Field(i)
		if field.Type == instant {
			t.Errorf("Request.%s is an instant: the minter computes `issued` and `expires` itself, and a caller that supplies one can date a key past its tier's maximum (§2, S2.1)", field.Name)
		}
	}
	duration := reflect.TypeOf(time.Duration(0))
	if request.Field(0).Type == duration {
		t.Error("Request carries a duration in nanoseconds, want the term in days: the wire a caller reads should not spell a term as a number nobody means")
	}
}

// TestTheMinersCeilingsAreTheVerifiers: S2.1's tier maximum is the verifier's
// rule and the minter refuses the same bound, so both are held here — the
// minter's largest term is found by asking it rather than written down, because
// a test that repeated the table would agree with the code and not with the
// verifier a released build carries.
func TestTheMinersCeilingsAreTheVerifiers(t *testing.T) {
	f := newFixture(t)
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: Production, Now: f.clock}
	request := func(tier string, days int) Request {
		return Request{
			Environment:  Production,
			Tier:         tier,
			Features:     []string{"seats"},
			Seats:        1,
			Organisation: "acme.example",
			Plan:         paidPlanName,
			TermDays:     days,
		}
	}

	for _, tier := range []string{licence.TierCommunity, licence.TierTeam, licence.TierEnterprise} {
		ceiling := 0
		for days := 1; days <= 900; days++ {
			if _, err := minter.Mint(context.Background(), request(tier, days)); err != nil {
				break
			}
			ceiling = days
		}
		if ceiling == 0 {
			t.Fatalf("the minter mints no term at all for tier %q", tier)
		}
		if _, err := minter.Mint(context.Background(), request(tier, ceiling+1)); err == nil {
			t.Errorf("the minter minted %d days for tier %q, one day past what it accepts (%d)", ceiling+1, tier, ceiling)
		}

		// The verifier's own answer at the same two terms, over a v1 key built by
		// hand: the minter's refusal has to be the bound a released build carries
		// and not a second one that happens to be nearby. The bound is read out of
		// a v1 key because the format the intake's own route writes is one the
		// licence package no longer reads (K7.3): the table the two sides share is
		// this one, and a v1 key is what can be held to it.
		if _, state, err := licence.Verify([]byte(handMintedV1(t, f, tier, f.now, time.Duration(ceiling)*day)), f.now, f.ring, workspaceID); err != nil || state != licence.Active {
			t.Errorf("a v1 key at the minter's ceiling (%d days, tier %q) reads %v, %v, want active at its own instant", ceiling, tier, state, err)
		}
		if _, _, err := licence.Verify([]byte(handMintedV1(t, f, tier, f.now, time.Duration(ceiling+1)*day)), f.now, f.ring, workspaceID); !errors.Is(err, licence.ErrLifetime) {
			t.Errorf("the verifier answers %v for %d days at tier %q while the minter refuses it, want ErrLifetime: the two bounds are one bound (S2.1)", err, ceiling+1, tier)
		}
	}

	// And what the minter does mint carries the request's own claims: the tier,
	// the seats and the organisation the intake asked for, dated by the minter's
	// own clock and running for the term it was given.
	minted, err := minter.Mint(context.Background(), request(licence.TierTeam, 365))
	if err != nil {
		t.Fatalf("minting a year for the team tier: %v", err)
	}
	claims := intakeMinted(t, f, minted.Key)
	if claims.Tier != licence.TierTeam || claims.Seats != 1 || claims.Organisation != "acme.example" {
		t.Errorf("the minted claims are %+v, want the request's own", claims)
	}
	if claims.Issued != f.now.Unix() || claims.Expires != f.now.Add(365*day).Unix() {
		t.Errorf("the minted claims run %d..%d, want the minter's own clock and the term it was asked for", claims.Issued, claims.Expires)
	}
	if minted.Record.Source != "" {
		t.Errorf("the minter named the source %q: what minted a key is the caller's to say, and the record is written where the store is (§5.2)", minted.Record.Source)
	}
}

// TestTheIntakesMintedPayloadCarriesTheNamesItsReadersParse: what the intake's
// own route signs is the claim set of the format licence-key-v2 removed (K7.3),
// and the things that read it are not this package: the desktop console's own
// verifier of that format and the mobile app's both parse the payload by field
// name, in another language, and this side decodes no key text at all. The tags are
// therefore a wire format, and nothing on this side would notice one of them
// moving: the fixture round-trip that held them (the licence package's
// TestVectorsRoundTrip over the committed testdata/vectors.json) read the
// format K7.3 removed and went with it. The names are pinned here instead, as
// the text of a payload rather than by decoding into the very struct the
// producer marshals, which would move both ends together.
//
// A renamed tag is the mutation this holds: the free-seat, open-source and
// request-form routes would go on answering keys that both readers refuse, and
// no Go test would fail.
func TestTheIntakesMintedPayloadCarriesTheNamesItsReadersParse(t *testing.T) {
	f := newFixture(t)
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: Production, Now: f.clock}
	minted, err := minter.Mint(context.Background(), Request{
		Environment:  Production,
		Tier:         licence.TierTeam,
		Features:     []string{"seats", "sso"},
		Seats:        3,
		Organisation: "acme.example",
		Plan:         paidPlanName,
		TermDays:     365,
	})
	if err != nil {
		t.Fatalf("minting a year for the team tier: %v", err)
	}
	parts := strings.Split(minted.Key, ".")
	if len(parts) != 3 {
		t.Fatalf("the minted key %q is not the three parts the intake's route writes", minted.Key)
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		t.Fatalf("the minted key's payload is not unpadded base64url: %v", err)
	}
	want := fmt.Sprintf(`{"tier":"team","features":["seats","sso"],"seats":3,"organisation":"acme.example","issued":%d,"expires":%d}`,
		f.now.Unix(), f.now.Add(365*day).Unix())
	if string(payload) != want {
		t.Errorf("the intake's route minted the payload\n%s\nand the six names the console's verifier and the app's parse are\n%s\nthe two read this text by field name in another language, and a name that moved would refuse every key those routes answer with no Go test failing", payload, want)
	}
}

// TestAMinterSignsForItsOwnEnvironmentOnly: D459(a) rules one project and one
// key ring for both environments, so the two key ids are the whole of what
// keeps a staging key out of production and a production key out of a test
// (Check 179). The minter's environment is therefore its own and never the
// request's: a minter that read it off the caller would sign a production key
// for whoever could reach it, and a minter built with none signs nothing at all.
func TestAMinterSignsForItsOwnEnvironmentOnly(t *testing.T) {
	f := newFixture(t)
	request := func(env Environment) Request {
		return Request{
			Environment:  env,
			Tier:         licence.TierTeam,
			Features:     []string{"seats"},
			Seats:        1,
			Organisation: "acme.example",
			Plan:         paidPlanName,
			TermDays:     365,
		}
	}
	staging := Minter{Signer: f.signer, Keys: f.keys, Environment: Staging, Now: f.clock}

	if _, err := staging.Mint(context.Background(), request(Production)); err == nil {
		t.Error("a staging minter served a production request, want a refusal: the key id it would sign under is the whole of what keeps the pairs apart (§2, §5.4)")
	}
	if _, err := staging.Mint(context.Background(), request("")); err == nil {
		t.Error("a staging minter served a request naming no environment, want a refusal")
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("the refused requests signed %d key(s), want none", got)
	}

	minted, err := staging.Mint(context.Background(), request(Staging))
	if err != nil {
		t.Fatalf("minting for the minter's own environment: %v", err)
	}
	if id, _, _ := strings.Cut(minted.Key, "."); id != stagingKeyID {
		t.Errorf("the staging minter answered a key under the id %q, want %q: the id is what a released verifier reads, and the production one is what it accepts (§2, §5.4)", id, stagingKeyID)
	}
	if minted.Record.KeyID != stagingKeyID {
		t.Errorf("the record names the key id %q, want the staging one", minted.Record.KeyID)
	}

	// A minter built with no environment of its own is the shape a shared
	// staging minter would be, and it mints nothing at all.
	bare := Minter{Signer: f.signer, Keys: f.keys, Now: f.clock}
	for _, env := range []Environment{Production, Staging, ""} {
		if _, err := bare.Mint(context.Background(), request(env)); err == nil {
			t.Errorf("a minter with no environment served a %q request, want a refusal", env)
		}
	}
}

// TestTheIntakeMintsThroughTheMinterService: §3.2 — the intake reaches the
// minter over its own route with an ID token, and the two halves' wire shape is
// this package's. The minter's route is unauthenticated at the application
// layer on purpose: Cloud Run's IAM check runs before the container, and the
// Terraform that holds it is S3.5's (Check 174).
func TestTheIntakeMintsThroughTheMinterService(t *testing.T) {
	f := newFixture(t)
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: Production, Now: f.clock}
	var presented string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented = r.Header.Get("Authorization")
		minter.Handler().ServeHTTP(w, r)
	}))
	defer srv.Close()

	f.deps.Mints = RemoteMints{URL: srv.URL, Client: srv.Client(), Tokens: staticTokens("id-token-for-the-minter")}
	f.start()
	f.apply(applicantAddress)
	f.verify(f.mailedToken())

	if presented != "Bearer id-token-for-the-minter" {
		t.Errorf("the intake presented %q to the minter, want the ID token the platform's check reads", presented)
	}
	records := f.records()
	if len(records) != 1 {
		t.Fatalf("the application recorded %d key(s), want one", len(records))
	}
	if records[0].Tier != licence.TierTeam || records[0].KeyID != productionKeyID {
		t.Errorf("the record is %+v, want the plan's claims under the production key id", records[0])
	}
	if got := f.signer.calls; got != 1 {
		t.Errorf("the minter signed %d key(s), want one", got)
	}
}

// TestTheMinterClientTakesItsTokenFromTheMetadataServer: the intake's own
// account mints the ID token the minter's platform check reads (§3.3), and it
// asks the project's metadata server for it rather than holding a secret.
func TestTheMinterClientTakesItsTokenFromTheMetadataServer(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.RawQuery
		if r.Header.Get("Metadata-Flavor") != "Google" {
			t.Errorf("the metadata request carries %q, want the header the server requires", r.Header.Get("Metadata-Flavor"))
		}
		w.Write([]byte("the-project-token"))
	}))
	defer srv.Close()

	tokens := MetadataTokens{URL: srv.URL, Client: srv.Client()}
	token, err := tokens.IDToken(context.Background(), "https://minter.example")
	if err != nil {
		t.Fatalf("asking the metadata server for an ID token: %v", err)
	}
	if token != "the-project-token" {
		t.Errorf("the token is %q, want what the server answered", token)
	}
	if !strings.Contains(asked, "audience=https%3A%2F%2Fminter.example") || !strings.Contains(asked, "format=full") {
		t.Errorf("the metadata request asks %q, want the audience the minter's platform check verifies", asked)
	}
}

// handMintedV1 is one v1 key text built by hand: a claim set of the format for
// one tier and one term, so that a test can hold a released build's verifier to
// the very term the minter's own ceiling is measured at. The key is the
// fixture's own, generated in this process (.claude/rules/ee.md, R5).
func handMintedV1(t *testing.T, f *fixture, tier string, issued time.Time, term time.Duration) string {
	t.Helper()
	claims := licence.Claims{
		V:            versionV1,
		KID:          productionKeyID,
		KeyID:        "lk_hand_minted",
		WorkspaceID:  workspaceID,
		Organisation: "acme.example",
		Tier:         tier,
		Seats:        1,
		IssuedAt:     issued,
		PaidThrough:  issued.Add(term),
		GraceDays:    30,
		RenewalMode:  licence.RenewalAuto,
		Kind:         licence.KindPaid,
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("encoding the claims: %v", err)
	}
	return keyTextV1(payload, ed25519.Sign(f.signer.priv, payload))
}

// intakeMinted reads a key the intake's own route minted: its three parts, its
// payload decoded, and its signature held to the key ring the fixture mints
// under. The claim set is the format licence-key-v2 removed (K7.3), which the
// licence package no longer reads, so what a test can hold about such a key is
// its shape and its signature and not a verification.
func intakeMinted(t *testing.T, f *fixture, key string) intakeClaims {
	t.Helper()
	parts := strings.Split(key, ".")
	if len(parts) != 3 {
		t.Fatalf("the minted key %q is not the three parts the intake's route writes", key)
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		t.Fatalf("the minted key's payload is not unpadded base64url: %v", err)
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil {
		t.Fatalf("the minted key's signature is not unpadded base64url: %v", err)
	}
	if !ed25519.Verify(f.signer.priv.Public().(ed25519.PublicKey), payload, signature) {
		t.Fatal("the minted key's signature is not the key ring's over the payload it carries")
	}
	var claims intakeClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("the minted key's payload is not a claim set: %v", err)
	}
	return claims
}

// TestTheMinterRefusesWhatItCannotSign: every claim the minter will not sign is
// refused **before** the signer is called, and this is the half of the service
// where that matters most — it is the half that holds the key ring, so a guard
// that is missing here is a key minted under an environment's id with a claim
// set nothing downstream checks again. `Seats < 1` is the one the verifier does
// not catch either way: its own validation does not range-check the seats, so a
// key for nobody's seat count verifies everywhere.
//
// Each case is one guard, and the count of the signer's calls is what says the
// refusal came first rather than after a signature was already made. A key ring
// that answers something that is not a signature is the one case where the
// signer *is* called and its answer is still refused, because that answer would
// otherwise be written into a key text no verifier accepts.
func TestTheMinterRefusesWhatItCannotSign(t *testing.T) {
	f := newFixture(t)
	valid := Request{
		Environment:  Production,
		Tier:         licence.TierTeam,
		Features:     []string{"seats"},
		Seats:        1,
		Organisation: "acme.example",
		Plan:         paidPlanName,
		TermDays:     365,
	}
	for _, tc := range []struct {
		name string
		// size is what the key ring answers instead of a signature, when a case
		// is about the answer rather than about the request.
		size int
		edit func(*Request, *Minter)
		// signed is how many times the signer must have been asked, and mints
		// whether the request is one the minter serves.
		signed int
		mints  bool
		// notTheCeiling says the refusal has to be the guard's own and not the
		// tier ceiling's. An unknown tier has no entry in the ceiling table, so a
		// missing guard reads the ceiling as zero and refuses the same request by
		// a rule that is not true of it — and the two have different repairs, a
		// tier to name and a term to shorten.
		notTheCeiling bool
	}{
		{name: "the request the intake builds", signed: 1, mints: true},
		{
			name:          "a tier no key is issued for",
			edit:          func(r *Request, _ *Minter) { r.Tier = "team-plus" },
			signed:        0,
			notTheCeiling: true,
		},
		{
			name:   "no tier at all",
			edit:   func(r *Request, _ *Minter) { r.Tier = "" },
			signed: 0,
		},
		{
			name:   "a term of no days",
			edit:   func(r *Request, _ *Minter) { r.TermDays = 0 },
			signed: 0,
		},
		{
			name:   "a term that is negative",
			edit:   func(r *Request, _ *Minter) { r.TermDays = -30 },
			signed: 0,
		},
		{
			name:   "no seats",
			edit:   func(r *Request, _ *Minter) { r.Seats = 0 },
			signed: 0,
		},
		{
			name:   "a negative number of seats",
			edit:   func(r *Request, _ *Minter) { r.Seats = -3 },
			signed: 0,
		},
		{
			name:   "no organisation",
			edit:   func(r *Request, _ *Minter) { r.Organisation = "" },
			signed: 0,
		},
		{
			name:   "an organisation of whitespace",
			edit:   func(r *Request, _ *Minter) { r.Organisation = " \t " },
			signed: 0,
		},
		{
			name:   "no key id for the environment",
			edit:   func(_ *Request, m *Minter) { m.Keys = Keys{Staging: stagingKeyID} },
			signed: 0,
		},
		{
			name:   "no signer",
			edit:   func(_ *Request, m *Minter) { m.Signer = nil },
			signed: 0,
		},
		{name: "a key ring answering a short signature", size: 32, signed: 1},
		{name: "a key ring answering a long signature", size: 128, signed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &signerSpy{priv: f.signer.priv, size: tc.size}
			minter := Minter{Signer: spy, Keys: f.keys, Environment: Production, Now: f.clock}
			request := valid
			if tc.edit != nil {
				tc.edit(&request, &minter)
			}
			minted, err := minter.Mint(context.Background(), request)
			if spy.calls != tc.signed {
				t.Errorf("the minter asked the signer %d time(s) for %s, want %d: a claim it will not sign is refused before the key ring is called", spy.calls, tc.name, tc.signed)
			}
			if tc.mints {
				if err != nil {
					t.Fatalf("the minter refused the request the intake builds: %v", err)
				}
				if claims := intakeMinted(t, f, minted.Key); claims.Tier != request.Tier || claims.Seats != request.Seats {
					t.Errorf("the minted claims are %+v, want the request's own", claims)
				}
				if minted.Record.Seats != 1 || minted.Record.Expires.Sub(minted.Record.Issued) != 365*day {
					t.Errorf("the record is %+v, want the request's seats and term", minted.Record)
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
		})
	}
}

// TestTheIntakeRefusesWhatItsMinterCannotAnswer: the intake holds no signer
// (Check 174), so every state in which the call to the minter's service cannot
// be made or cannot be read is a refusal — never a key minted locally and never
// a half-read answer. Each of them is reachable from a deploy's own
// configuration, which is why each is guarded where the call is built rather
// than left to fail as a nil pointer further in.
func TestTheIntakeRefusesWhatItsMinterCannotAnswer(t *testing.T) {
	request := Request{
		Environment:  Production,
		Tier:         licence.TierTeam,
		Features:     []string{"seats"},
		Seats:        1,
		Organisation: "acme.example",
		Plan:         paidPlanName,
		TermDays:     365,
	}
	answer := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(body))
		}
	}
	// A whole key in a body: what a minter answering a refusal carries anyway is
	// the shape the status is the only guard against, and a 200 with no key in it
	// is the shape the key is.
	minted := `{"key":"a.b.c","record":{"source":"","key_id":"production","plan":"","tier":"","features":null,"seats":0,"organisation":"","email":"","issued":"0001-01-01T00:00:00Z","expires":"0001-01-01T00:00:00Z"}}`
	// Every case whose refusal is a state the call cannot be made in is given a
	// client that counts being asked to make it, so "refused before the call" is
	// read rather than inferred from an error the network happened to answer.
	for _, tc := range []struct {
		name   string
		url    string
		tokens Tokens
		answer http.HandlerFunc
		before bool
	}{
		{name: "no minter URL at all", tokens: staticTokens("id-token"), before: true},
		{name: "a minter URL of whitespace", url: "  ", tokens: staticTokens("id-token"), before: true},
		{name: "no source of ID tokens", url: "https://minter.example", before: true},
		{name: "a token source that is down", url: "https://minter.example", tokens: failingTokens(), before: true},
		{name: "a minter that refuses the call", tokens: staticTokens("id-token"), answer: answer(http.StatusForbidden, minted)},
		{name: "a minter that fails", tokens: staticTokens("id-token"), answer: answer(http.StatusInternalServerError, minted)},
		{name: "a minter that answers no key", tokens: staticTokens("id-token"), answer: answer(http.StatusOK, `{}`)},
		{name: "a minter that answers a key under no id", tokens: staticTokens("id-token"), answer: answer(http.StatusOK, `{"key":"a.b.c","record":{"key_id":""}}`)},
		{name: "a minter whose answer is not one this service reads", tokens: staticTokens("id-token"), answer: answer(http.StatusOK, `{"key":`)},
		{name: "a minter whose answer carries a field this service does not know", tokens: staticTokens("id-token"), answer: answer(http.StatusOK, `{"key":"a.b.c","record":{"key_id":"production"},"extra":true}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &clientSpy{}
			mints := RemoteMints{URL: tc.url, Tokens: tc.tokens, Client: &http.Client{Transport: client}}
			if tc.answer != nil {
				srv := httptest.NewServer(tc.answer)
				defer srv.Close()
				mints.URL, mints.Client = srv.URL, srv.Client()
			}
			if _, err := mints.Mint(context.Background(), request); err == nil {
				t.Errorf("the intake's minter client served %s, want a refusal: this half holds no signer, so a call it cannot make is never a key (Check 174)", tc.name)
			}
			if tc.before && client.calls != 0 {
				t.Errorf("the minter client made %d call(s) for %s, want none: the state is one the call cannot be made in, and sending it anyway is how a refusal turns into a mint somewhere else", client.calls, tc.name)
			}
		})
	}
}

// TestTheMinersRouteRefusesWhatItCannotServe: the minter's one route is the
// service's entry point, and everything it will not serve is a 400 with nothing
// signed — the shape the intake's half is written against (§3.2, Check 174).
// The body is read under a cap because the route is reachable by whoever the
// platform admits, and a request it cannot read is refused rather than
// half-read: the only 200 here is the request the intake builds, and the key it
// answers is one a released verifier accepts.
func TestTheMinersRouteRefusesWhatItCannotServe(t *testing.T) {
	f := newFixture(t)
	encoded, err := json.Marshal(Request{
		Environment:  Production,
		Tier:         licence.TierTeam,
		Features:     []string{"seats"},
		Seats:        1,
		Organisation: "acme.example",
		Plan:         paidPlanName,
		TermDays:     365,
	})
	if err != nil {
		t.Fatalf("encoding the request the intake builds: %v", err)
	}
	for _, tc := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "the request the intake builds", body: string(encoded), status: http.StatusOK},
		{name: "an empty body", body: "", status: http.StatusBadRequest},
		{name: "a body that is not a request this service reads", body: "tier=team", status: http.StatusBadRequest},
		{name: "a body carrying a field this service does not know", body: `{"tier":"team","extra":true}`, status: http.StatusBadRequest},
		{name: "a request the minter will not sign", body: `{"environment":"production","tier":"team-plus","seats":1,"organisation":"acme.example","term_days":365}`, status: http.StatusBadRequest},
		{name: "a body longer than the route reads", body: strings.Repeat("x", maxRequestBytes+1), status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &signerSpy{priv: f.signer.priv}
			minter := Minter{Signer: spy, Keys: f.keys, Environment: Production, Now: f.clock}
			rec := httptest.NewRecorder()
			minter.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, RouteMint, strings.NewReader(tc.body)))
			if rec.Code != tc.status {
				t.Fatalf("the minter's route answered %d for %s, want %d: %s", rec.Code, tc.name, tc.status, rec.Body.String())
			}
			if tc.status == http.StatusOK {
				if spy.calls != 1 {
					t.Errorf("the minter's route signed %d key(s) for the request the intake builds, want one", spy.calls)
				}
				var minted Minted
				if err := json.Unmarshal(rec.Body.Bytes(), &minted); err != nil {
					t.Fatalf("reading the answer back: %v", err)
				}
				intakeMinted(t, f, minted.Key)
				return
			}
			if spy.calls != 0 {
				t.Errorf("the minter's route answered %d for %s after asking the key ring %d time(s), want a refusal before the signer is reached", rec.Code, tc.name, spy.calls)
			}
			if rec.Body.Len() == 0 {
				t.Error("the route refused with an empty body, and its words are what the intake's half reads")
			}
		})
	}
}

// TestTheMetadataServerRefusalsAreNotAToken: the token's source is the
// platform's and every way it can fail is a refusal, never an empty string
// presented to the minter as a bearer token — a call made without one is a call
// the minter's own check refuses, and a token read out of an error's body is no
// token at all.
func TestTheMetadataServerRefusalsAreNotAToken(t *testing.T) {
	answer := func(status int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(body))
		}
	}
	for _, tc := range []struct {
		name   string
		answer http.HandlerFunc
	}{
		{name: "a metadata server that refuses the call", answer: answer(http.StatusForbidden, "the caller is not the instance")},
		{name: "a metadata server that fails", answer: answer(http.StatusInternalServerError, "")},
		{name: "a metadata server whose answer cannot be read", answer: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "64")
			w.Write([]byte("half a token"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.answer)
			defer srv.Close()
			tokens := MetadataTokens{URL: srv.URL, Client: srv.Client()}
			token, err := tokens.IDToken(context.Background(), "https://minter.example")
			if err == nil {
				t.Fatalf("the metadata server's source answered the token %q for %s, want a refusal: a call the minter's check refuses is one the intake must not make", token, tc.name)
			}
			if token != "" {
				t.Errorf("the refusal carries the token %q, want none", token)
			}
		})
	}
}

// clientSpy is the transport of a RemoteMints call that must never leave the
// process: what it answers is never read, and its count is what a test reads to
// say a refusal came before the request rather than after the network declined
// it.
type clientSpy struct{ calls int }

func (c *clientSpy) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls++
	return nil, errors.New("this client must not be reached")
}

// failingTokens is a Tokens whose source is down, which is a state a deploy can
// be in: the metadata server is unset, unreachable or answering an error, and
// what the intake must not do is call the minter with no token and then mint
// locally when it is refused.
func failingTokens() Tokens {
	return tokenFunc(func(context.Context, string) (string, error) {
		return "", errors.New("the metadata server answered nothing")
	})
}
