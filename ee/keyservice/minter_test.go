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
