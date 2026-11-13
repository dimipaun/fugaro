// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The intake's half of the split: the call it makes to the minter's own service
// (§3.2). This is the whole of how the internet-facing half gets a key, and it
// gets one the way any other client would — over the network, with an ID token
// the platform verifies — because a shorter path would be a path to the signer
// (Check 174).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Tokens is where the ID token for the mint call comes from. It is a seam
// because the token's source is the platform's: on Cloud Run it is the metadata
// server, and in a test it is a constant.
type Tokens interface {
	IDToken(ctx context.Context, audience string) (string, error)
}

// RemoteMints is the intake's Mints: it asks the minter's service and holds no
// signer of its own.
type RemoteMints struct {
	// URL is the minter service's own address, and it is also the audience the
	// ID token is minted for: the platform verifies the token against the
	// service it was presented to (§3.2).
	URL    string
	Client *http.Client
	Tokens Tokens
}

// Mint sends one request to the minter and answers what it minted.
func (r RemoteMints) Mint(ctx context.Context, req Request) (Minted, error) {
	var minted Minted
	if err := r.call(ctx, RouteMint, req, &minted); err != nil {
		return Minted{}, err
	}
	if minted.Key == "" || minted.Record.KeyID == "" {
		return Minted{}, errors.New("the minter answered no key")
	}
	return minted, nil
}

// MintV1 sends one v1 request to the minter and answers what it minted, with the
// renewal secret beside the key.
//
// The secret crosses this call and nowhere else: the two halves are one
// deployment's pair, the call is the platform-authenticated one (Check 174), and
// the secret is the second line of the bundle an issue path shows once and never
// stores (D2135). An answer without one is refused here rather than passed on,
// because a key whose secret the intake never saw is a key nobody can renew —
// with one exception, and it is the request's own: a reissue that keeps the
// entitlement's existing hash is answered with no secret at all (D2200), because
// the holder already has the one they were sent, and refusing that answer would
// leave the reissue lane with no way to reach the minter in a deployment at all.
func (r RemoteMints) MintV1(ctx context.Context, req RequestV1) (MintedV1, error) {
	var minted MintedV1
	if err := r.call(ctx, RouteMintV1, req, &minted); err != nil {
		return MintedV1{}, err
	}
	if minted.Key == "" || minted.Record.KeyID == "" {
		return MintedV1{}, errors.New("the minter answered no key")
	}
	if minted.RenewalSecret == "" && req.RenewalSecretHash == "" {
		return MintedV1{}, errors.New("the minter answered no renewal secret, and a key that cannot renew is not one this service issues (D2135)")
	}
	return minted, nil
}

// call is the one call both mints make: the request as JSON, the ID token the
// minter's platform check reads, and an answer decoded strictly. The two routes
// differ in the shape of the values and in nothing else, and a second copy of
// this would be a second place for the token to be forgotten.
func (r RemoteMints) call(ctx context.Context, route string, request, answer any) error {
	if strings.TrimSpace(r.URL) == "" {
		return errors.New("the intake has no minter URL, and this half cannot sign")
	}
	if r.Tokens == nil {
		return errors.New("the intake has no source of ID tokens, so the minter's own check would refuse it")
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("the mint request: %w", err)
	}
	token, err := r.Tokens.IDToken(ctx, r.URL)
	if err != nil {
		return fmt.Errorf("the ID token for the minter: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(r.URL, "/")+route, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("the call to the minter: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("the call to the minter: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRequestBytes))
	if err != nil {
		return fmt.Errorf("the minter's answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// The minter's refusal is its own text and it names no claim: what it
		// refused is a claim set this service built, so its words are this
		// service's to read and nobody else's.
		return fmt.Errorf("the minter refused the request with %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(answer); err != nil {
		return fmt.Errorf("the minter's answer is not one this service reads: %w", err)
	}
	return nil
}

// MetadataTokens reads the ID token from the platform's metadata server, which
// is the deploy's own source on Cloud Run: the token is minted for the
// audience, it lives about an hour, and nothing here holds a secret to mint one
// instead.
type MetadataTokens struct {
	// URL is the identity endpoint, `…/instance/service-accounts/default/identity`.
	URL    string
	Client *http.Client
}

const metadataIdentityURL = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity"

// IDToken asks the metadata server for one token naming the audience.
func (m MetadataTokens) IDToken(ctx context.Context, audience string) (string, error) {
	endpoint := m.URL
	if endpoint == "" {
		endpoint = metadataIdentityURL
	}
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	address, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("the metadata endpoint %q: %w", endpoint, err)
	}
	query := address.Query()
	query.Set("audience", audience)
	// `full` asks for the token itself rather than a reference to it.
	query.Set("format", "full")
	address.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		return "", fmt.Errorf("the metadata request: %w", err)
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("the metadata server: %w", err)
	}
	defer resp.Body.Close()
	token, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return "", fmt.Errorf("the metadata server's answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the metadata server answered %d for an ID token", resp.StatusCode)
	}
	return strings.TrimSpace(string(token)), nil
}
