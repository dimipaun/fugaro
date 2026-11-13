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

		// The verifier's own answer at the same two terms, over a key built by
		// hand: the minter's refusal has to be the verifier's bound and not a
		// second one that happens to be nearby.
		issued := f.now
		at := handMinted(t, f, productionKeyID, licence.Claims{
			Tier: tier, Features: []string{"seats"}, Seats: 1, Organisation: "acme.example",
			Issued:  issued.Unix(),
			Expires: issued.Add(time.Duration(ceiling) * day).Unix(),
		})
		if _, state, err := licence.VerifyAgainst([]byte(at), f.now, f.ring); err != nil || state != licence.Valid {
			t.Errorf("a key at the minter's ceiling (%d days, tier %q) reads %v, %v, want valid at its own instant", ceiling, tier, state, err)
		}
		past := handMinted(t, f, productionKeyID, licence.Claims{
			Tier: tier, Features: []string{"seats"}, Seats: 1, Organisation: "acme.example",
			Issued:  issued.Unix(),
			Expires: issued.Add(time.Duration(ceiling+1) * day).Unix(),
		})
		if _, _, err := licence.VerifyAgainst([]byte(past), f.now, f.ring); err == nil {
			t.Errorf("the verifier accepts %d days for tier %q while the minter refuses it: the two bounds are one bound (S2.1)", ceiling+1, tier)
		}
	}

	// And what the minter does mint is a key a released verifier reads: the
	// claims are the request's, and the id names the environment.
	minted, err := minter.Mint(context.Background(), request(licence.TierTeam, 365))
	if err != nil {
		t.Fatalf("minting a year for the team tier: %v", err)
	}
	claims, _, err := licence.VerifyAgainst([]byte(minted.Key), f.now, f.ring)
	if err != nil {
		t.Fatalf("the minted key does not verify: %v", err)
	}
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

// handMinted is one key text built the way a minter builds one, so that a test
// can hold the verifier to a claim set the minter would not mint. The key is
// the fixture's own, generated in this process (.claude/rules/ee.md, R5).
func handMinted(t *testing.T, f *fixture, id string, claims licence.Claims) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("encoding the claims: %v", err)
	}
	signature := ed25519.Sign(f.signer.priv, payload)
	return id + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
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
				if _, _, err := licence.VerifyAgainst([]byte(minted.Key), f.now, f.ring); err != nil {
					t.Errorf("the minted key does not verify: %v", err)
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
				if _, _, err := licence.VerifyAgainst([]byte(minted.Key), f.now, f.ring); err != nil {
					t.Errorf("the key the route answered does not verify: %v", err)
				}
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

// TestMintTermNeverExceedsTheRow: F30 — a request may name the instant its key
// is for, because a term is not always "so many days from now": a reveal ends on
// the predecessor's `paid_through` and must never extend it, and an entitlement
// is paid for to a date rather than for a count of days (D2197, P3.1, P3.2).
// What the minter does with that instant is cap it: the key it signs runs at
// most as long as the row allows, whatever instant the caller named, so §2's
// rule — a key cannot outlive what its row is sold for — holds on this path too.
func TestMintTermNeverExceedsTheRow(t *testing.T) {
	f := newFixture(t)
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: Production, Now: f.clock}
	// Three years out, which is past every row of the table: a minter that
	// passed the instant on would sign a key the verifier's own ceiling refuses.
	far := f.now.Add(3 * 365 * day)

	for _, row := range defaultPolicies {
		t.Run(row.Name, func(t *testing.T) {
			minted, err := minter.MintV1(context.Background(), RequestV1{
				Environment:  Production,
				Policy:       row.Name,
				WorkspaceID:  workspaceID,
				Organisation: "acme.example",
				Seats:        1,
				RequestedEnd: far,
			})
			if err != nil {
				t.Fatalf("the minter refused an end %s out, want the row's own term: %v", far.Sub(f.now), err)
			}
			claims := v1Verified(t, f, minted.Key)
			run := claims.PaidThrough.Sub(claims.IssuedAt)
			if run > row.Term {
				t.Errorf("the key runs %s from its issue, want at most the row's own %s: the end a caller names is capped at the row and never passed on (F30, section 2)", run, row.Term)
			}
			// D2202: the row's own term is the bound, and a row whose key
			// checks the server is bound tighter still — its thirty days.
			want := row.Term
			if row.CheckInterval != nil && want > 30*day {
				want = 30 * day
			}
			if run != want {
				t.Errorf("the key runs %s, want %s: an end past the row is capped at the longest key the row mints", run, want)
			}
			if !claims.PaidThrough.Equal(f.now.Add(want)) {
				t.Errorf("the key is paid through %s, want %s: `issued` is still the minter's own clock, and only the end it was asked for moved (section 2)", claims.PaidThrough, f.now.Add(want))
			}
		})
	}
}

// TestRequestedEndCapsTheTerm: F30 — the end a request names is honoured where
// the row reaches it and capped where it does not, and `issued_at` is the
// minter's own clock in both cases. That is what makes the end safe to accept at
// all: a reveal hands back the instant the predecessor ends (D2182, P3.2), which
// a caller must be able to state exactly and must never be able to extend.
func TestRequestedEndCapsTheTerm(t *testing.T) {
	f := newFixture(t)
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: Production, Now: f.clock}
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		// wantDays is the whole days the record is to stand for: the term the
		// issuance itself covers, which an end the caller named shortens and
		// D2202's cap on the key never does. A record that said a year over a
		// key ten days long would rebuild a subscription ending a year out, and
		// one that said a month over a year-long entitlement would rebuild a
		// subscription ending a month out — the cap moves the key and not the
		// thing it is a month of (F30, D2202, S3.5).
		wantDays int
		policy   string
		end      time.Time
		want     time.Duration
		exact    bool
		refused  bool
	}{
		{name: "an end inside the row", policy: PolicyTeamAnnual, end: f.now.Add(10*day + 7*time.Second), want: 10*day + 7*time.Second, wantDays: 11, exact: true},
		{name: "an end past the row", policy: PolicyTeamAnnual, end: f.now.Add(3 * 365 * day), want: connectedTerm, wantDays: 365},
		{name: "an end inside the air-gapped row's year", policy: PolicyEnterpriseAirGapped, end: f.now.Add(200 * day), want: 200 * day, wantDays: 200, exact: true},
		{name: "an end past the air-gapped row's year", policy: PolicyEnterpriseAirGapped, end: f.now.Add(3 * 365 * day), want: 365 * day, wantDays: 365},
		{name: "an end behind the minter's clock", policy: PolicyTeamAnnual, end: f.now.Add(-day), refused: true},
		{name: "an end on the minter's clock", policy: PolicyTeamAnnual, end: f.now, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			minted, err := minter.MintV1(ctx, RequestV1{
				Environment:  Production,
				Policy:       tc.policy,
				WorkspaceID:  workspaceID,
				Organisation: "acme.example",
				Seats:        1,
				RequestedEnd: tc.end,
			})
			if tc.refused {
				if err == nil {
					t.Fatalf("the minter minted a key to an end %s out, want a refusal: a key minted to an instant the clock has reached is past before anybody reads it (F30)", tc.end.Sub(f.now))
				}
				return
			}
			if err != nil {
				t.Fatalf("minting a key to an end %s out: %v", tc.end.Sub(f.now), err)
			}
			claims := v1Verified(t, f, minted.Key)
			if !claims.IssuedAt.Equal(f.now) {
				t.Errorf("the key is issued at %s, want the minter's own clock %s: the end moves and the issue never does (section 2)", claims.IssuedAt, f.now)
			}
			if run := claims.PaidThrough.Sub(claims.IssuedAt); run != tc.want {
				t.Errorf("the key runs %s, want %s: an end the row reaches is honoured and one past it is capped at the longest key the row mints (F30)", run, tc.want)
			}
			if tc.exact && !claims.PaidThrough.Equal(tc.end) {
				t.Errorf("the key is paid through %s, want the end it was asked for, %s, to the second: days cannot state the instant a predecessor ends (D2182)", claims.PaidThrough, tc.end)
			}
			if !minted.Record.Expires.Equal(claims.PaidThrough) {
				t.Errorf("the record expires %s and the key %s, want one instant: the record is written from the key it names (K2.1)", minted.Record.Expires, claims.PaidThrough)
			}
			if minted.Record.TermDays != tc.wantDays {
				t.Errorf("the record stands for %d day(s), want %d: it is the term the issuance really has, and an end the caller named is part of it (F30, S3.5)", minted.Record.TermDays, tc.wantDays)
			}
			// The record states whole days and the end is an instant, so a
			// subscription rebuilt from it lands on that end or up to a day
			// past it — the round is the package's own, and a rebuild short of
			// the end would hand back less than the entitlement holds (S3.5).
			if tc.exact {
				if rebuilt := entitlementEnd(minted.Record); rebuilt.Before(tc.end) || !rebuilt.Before(tc.end.Add(24*time.Hour)) {
					t.Errorf("a subscription rebuilt from the record ends at %s, want the end the caller named (%s) and inside a day of it: the record's term is whole days rounded up, which is the direction that never takes a day from the customer", rebuilt, tc.end)
				}
			}
		})
	}

	// Both halves at once is a caller that has not decided: what it gets is the
	// tighter of the two, so naming an end can only ever shorten a key.
	minted, err := minter.MintV1(ctx, RequestV1{
		Environment: Production, Policy: PolicyTeamAnnual, WorkspaceID: workspaceID,
		Organisation: "acme.example", Seats: 1,
		TermDays: 90, RequestedEnd: f.now.Add(5 * day),
	})
	if err != nil {
		t.Fatalf("minting with both a term and an end: %v", err)
	}
	if run := v1Verified(t, f, minted.Key).PaidThrough.Sub(f.now); run != 5*day {
		t.Errorf("the key runs %s, want the shorter of the term and the end (%s): an end caps and never extends (F30)", run, 5*day)
	}
	if minted.Record.TermDays != 5 {
		t.Errorf("the record stands for %d day(s), want the 5 the end leaves and not the 90 the request named: a subscription rebuilt from the record would otherwise end 90 days out", minted.Record.TermDays)
	}
}

// TestOperationsCheckIntervalIsBounded: F28 — an operations mint names the row,
// the seats and an end, and may name the interval its key checks at inside
// `P1D`–`P30D`. The bound is what keeps a revocation reachable: a key that checks
// less often than its own term is one the server cannot withdraw before it
// expires, and the air-gapped row is the only row a caller may not change,
// because its key never contacts the server at all.
func TestOperationsCheckIntervalIsBounded(t *testing.T) {
	f := newFixture(t)
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: Production, Now: f.clock}
	ctx := context.Background()

	for _, tc := range []struct {
		interval string
		bounded  bool
	}{
		{interval: "P1D", bounded: true},
		{interval: "P7D", bounded: true},
		{interval: "P1W", bounded: true},
		{interval: "P2W", bounded: true},
		{interval: "P30D", bounded: true},
		{interval: "P0D"},
		{interval: "P31D"},
		{interval: "P5W"},
		{interval: "PT1H"},
		{interval: "P1M"},
		{interval: "P1Y"},
		{interval: "P1d"},
		{interval: "1 day"},
	} {
		t.Run(tc.interval, func(t *testing.T) {
			minted, err := minter.MintV1(ctx, RequestV1{
				Environment: Production, Policy: PolicyTeamMonthly, WorkspaceID: workspaceID,
				Organisation: "acme.example", Seats: 1, CheckInterval: tc.interval,
			})
			if !tc.bounded {
				if err == nil {
					t.Fatalf("the minter minted a key checking every %q, want a refusal: the interval a key may carry is P1D to P30D and a spelling no verifier reads is not one of them (F28, F16)", tc.interval)
				}
				return
			}
			if err != nil {
				t.Fatalf("minting a key checking every %q: %v", tc.interval, err)
			}
			claims := v1Verified(t, f, minted.Key)
			if claims.CheckInterval == nil || *claims.CheckInterval != tc.interval {
				t.Errorf("the key checks at %v, want the interval the request named, %q", claims.CheckInterval, tc.interval)
			}
		})
	}

	// The air-gapped row has none, and a caller that names one there is asking
	// for a key the row does not describe: its installation has no route to the
	// server, so a key that checks is a key that cannot be issued at all (F28).
	if _, err := minter.MintV1(ctx, RequestV1{
		Environment: Production, Policy: PolicyEnterpriseAirGapped, WorkspaceID: workspaceID,
		Organisation: "acme.example", Seats: 1, CheckInterval: dailyCheck,
	}); err == nil {
		t.Error("the air-gapped row took a check interval from a caller, want a refusal: only that row has none, and it has none because its key never contacts the server (F28)")
	}

	// And no spelling in the request gives a connected row a key with none: the
	// null interval is the air-gapped row's alone, whatever a caller asks for.
	for _, row := range defaultPolicies {
		t.Run("the "+row.Name+" row", func(t *testing.T) {
			minted, err := minter.MintV1(ctx, RequestV1{
				Environment: Production, Policy: row.Name, WorkspaceID: workspaceID,
				Organisation: "acme.example", Seats: 1,
			})
			if err != nil {
				t.Fatalf("minting a %s key: %v", row.Name, err)
			}
			claims := v1Verified(t, f, minted.Key)
			if (claims.CheckInterval == nil) != (row.CheckInterval == nil) {
				t.Errorf("the key checks at %v and its row carries %v, want the row's own: a key with no interval is one the server can never withdraw (F28, F29)", claims.CheckInterval, row.CheckInterval)
			}
		})
	}
}

// TestReissueKeepsTheSecretHash: D2200, amending D2135 — a reissue under an
// entitlement that already has a renewal secret keeps that secret, so a seat
// change or a renewal does not lock an installation out of renewing with the
// secret it holds; only a reveal rotates, because a reveal is what a leaked
// secret makes necessary. What a caller may hand back is a hash and never a
// secret, and only one of the shape this service writes.
func TestReissueKeepsTheSecretHash(t *testing.T) {
	f := newFixture(t)
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: Production, Now: f.clock}
	ctx := context.Background()
	issue := func(hash string) RequestV1 {
		return RequestV1{
			Environment: Production, Policy: PolicyTeamAnnual, WorkspaceID: workspaceID,
			Organisation: "acme.example", Seats: 3, RenewalSecretHash: hash,
		}
	}

	issued, err := minter.MintV1(ctx, issue(""))
	if err != nil {
		t.Fatalf("minting the entitlement's first key: %v", err)
	}
	if issued.RenewalSecret == "" {
		t.Fatal("the first issue answered no renewal secret, so there is none for a reissue to keep (D2135)")
	}
	hash := secretHash(issued.RenewalSecret)
	first := v1Verified(t, f, issued.Key)

	reissued, err := minter.MintV1(ctx, issue(hash))
	if err != nil {
		t.Fatalf("reissuing with the secret's own hash: %v", err)
	}
	claims := v1Verified(t, f, reissued.Key)
	if claims.RenewalSecretHash != hash {
		t.Errorf("the reissued key carries the hash %q, want the one it was given, %q: the installation's own secret is what renews it (D2200)", claims.RenewalSecretHash, hash)
	}
	if reissued.RenewalSecret != "" {
		t.Error("the reissue answered a renewal secret, want none: the holder's secret is the one it already has, and a second one handed out is a second thing that renews the key (D2200)")
	}
	if claims.KeyID == first.KeyID || !strings.HasPrefix(claims.KeyID, keyIDPrefix) {
		t.Errorf("the reissued key claims the key id %q and the first claims %q, want an id of its own under %q: a revocation names one key (K2.2)", claims.KeyID, first.KeyID, keyIDPrefix)
	}

	// A hash and nothing else: the shape is the whole of what the minter can
	// check, since which entitlement owns the secret is the store's to know
	// (P3.6) — and the shape is what keeps a secret from being written into a
	// claim every reader of the runs bucket holds (D2135).
	for _, tc := range []struct{ name, hash string }{
		{name: "the secret itself where its hash belongs", hash: issued.RenewalSecret},
		{name: "a hash a digit short", hash: strings.Repeat("a", 63)},
		{name: "a hash in the wrong case", hash: strings.ToUpper(hash)},
		{name: "a hash that is not hex", hash: strings.Repeat("z", 64)},
		{name: "a hash with a space in it", hash: strings.Repeat("a", 63) + " "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := minter.MintV1(ctx, issue(tc.hash)); err == nil {
				t.Errorf("the minter wrote %s into a key's renewal secret hash, want a refusal: a reissue may only keep a hash this service could have written (D2135, D2200)", tc.name)
			}
		})
	}
}

// TestConnectedKeysRunAtMostThirtyDays: D2202 — a key of a row whose
// installation contacts the server runs thirty days to `paid_through` whatever
// the row is sold for, because the key's own end is the only thing that ends a
// rebound installation's access (F38). The air-gapped row keeps the term it is
// sold for: nothing about its key ever reaches the server, so there is no window
// to close.
func TestConnectedKeysRunAtMostThirtyDays(t *testing.T) {
	f := newFixture(t)
	minter := Minter{Signer: f.signer, Keys: f.keys, Environment: Production, Now: f.clock}
	ctx := context.Background()

	for _, row := range defaultPolicies {
		t.Run(row.Name, func(t *testing.T) {
			want := row.Term
			if row.CheckInterval != nil && want > connectedTerm {
				want = connectedTerm
			}
			for _, tc := range []struct {
				name string
				req  RequestV1
			}{
				{name: "no term named", req: RequestV1{}},
				{name: "the row's own days named", req: RequestV1{TermDays: int(row.Term / (24 * time.Hour))}},
				{name: "an end three years out", req: RequestV1{RequestedEnd: f.now.Add(3 * 365 * day)}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					req := tc.req
					req.Environment, req.Policy, req.WorkspaceID = Production, row.Name, workspaceID
					req.Organisation, req.Seats = "acme.example", 1
					minted, err := minter.MintV1(ctx, req)
					if err != nil {
						t.Fatalf("minting a %s key with %s: %v", row.Name, tc.name, err)
					}
					claims := v1Verified(t, f, minted.Key)
					if run := claims.PaidThrough.Sub(claims.IssuedAt); run != want {
						t.Errorf("a %s key minted with %s runs %s, want %s: a connected row's key runs the thirty days of D2202 and the air-gapped row its own term", row.Name, tc.name, run, want)
					}
				})
			}
			for _, days := range []int{30, 90, 365} {
				// Nothing renews a row whose mode is `none`, so the days of one
				// are its own and naming others is another test's refusal.
				if row.RenewalMode == licence.RenewalNone && days != int(row.Term/(24*time.Hour)) {
					continue
				}
				t.Run(fmt.Sprintf("%d days named", days), func(t *testing.T) {
					minted, err := minter.MintV1(ctx, RequestV1{
						Environment: Production, Policy: row.Name, WorkspaceID: workspaceID,
						Organisation: "acme.example", Seats: 1, TermDays: days,
					})
					if err != nil {
						t.Fatalf("minting a %s key for %d day(s): %v", row.Name, days, err)
					}
					claims := v1Verified(t, f, minted.Key)
					if run := claims.PaidThrough.Sub(claims.IssuedAt); run != want {
						t.Errorf("a %s key asked for %d day(s) runs %s, want %s (D2202)", row.Name, days, run, want)
					}
				})
			}
		})
	}

	// The other door to a key is the installation's own renewal, and it is the
	// same minter: a successor runs its thirty days from the day it is asked for
	// however long the renewal asked (T26's successor covers the rest of the term
	// and the next period, which is longer than the cap and is capped).
	//
	// The free seat is left out of this one: `heldKey` asks for three seats and
	// that row is one seat (D2184), which is its own test's refusal.
	for _, row := range defaultPolicies {
		if row.CheckInterval == nil || row.Term <= connectedTerm || row.Seats != 0 {
			continue
		}
		t.Run("the renewal of a "+row.Name+" key", func(t *testing.T) {
			f, _ := renewing(t)
			keyID, secret, _ := heldKey(t, f, row.Name)
			f.inTheRenewalWindow(t, keyID)
			asked := f.now
			answer := answerOf(t, f.call(RouteRenew, RenewalCall{KeyID: keyID, WorkspaceID: workspaceID, RenewalSecret: secret}))
			if answer.Key == "" {
				t.Fatalf("the renewal inside the window answered no key: %+v", answer)
			}
			claims := v1Verified(t, f, answer.Key)
			if run := claims.PaidThrough.Sub(claims.IssuedAt); run != connectedTerm {
				t.Errorf("the successor of a %s key runs %s, want %s: the renewal is the same minter and the same cap (D2202)", row.Name, run, connectedTerm)
			}
			if !claims.IssuedAt.Equal(asked) {
				t.Errorf("the successor is issued at %s, want the instant it was asked for, %s", claims.IssuedAt, asked)
			}
		})
	}
}
