package credsvc

// The session's minter (P4, O8, D2783): the one-hour Firebase custom token a
// phone exchanges for an ID token.
//
// It is a type of its own and not a method of the service, because what it
// holds is a GRANT and not a fact of the installation: the public service signs
// with a device-only signer account, and an installation's private service must
// not be able to mint a phone's session with the account it already holds for
// its runs. A store that could mint would need the grant (C1's Minter doc), and
// so would a handler that held a signer: the seam is the interface, and the
// grant lives on the far side of it.
//
// What it signs is a Firebase custom token and never an assertion. The token
// names one uid, which is the device node's key: minting for another uid is
// minting a session for another device, which is why the uid is checked rather
// than trusted (token.MintDeviceForUID, O8's "no route takes a caller-chosen
// uid").
//
// The Firebase refresh token the app derives from this token is never returned
// by this service, and never seen by it: the exchange is between the phone and
// Firebase (D2783, C1's SessionResponse doc).

import (
	"context"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/mobilesession"
)

// DeviceMinter is C1's Minter over the device-only signer account.
type DeviceMinter struct {
	// Signer is the device-only signer (O8): the account whose
	// iam.serviceAccounts.signJwt the service's own account holds, on the runs'
	// signer account, and never the installation's model signer. A nil one
	// mints nothing, and Mint refuses rather than answering an empty token.
	Signer token.Signer
	// Now is the clock; nil is time.Now. It is read for the token's own iat and
	// exp and for the ExpiresAt this service reports, so the two agree.
	Now func() time.Time
}

// Mint answers the custom token for one device's uid (C1's Minter).
func (m DeviceMinter) Mint(ctx context.Context, req mobilesession.MintRequest) (mobilesession.CustomToken, error) {
	if m.Signer == nil {
		return mobilesession.CustomToken{}, fmt.Errorf("credsvc: the device minter has no signer, so no session can be minted (O8's device-only signer account is what `fugaro update` grants this service)")
	}
	if req.Role != "" && !req.Role.Valid() {
		return mobilesession.CustomToken{}, fmt.Errorf("credsvc: %q is not a role of the roster, so no token can report it", req.Role)
	}
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	dev, err := token.MintDeviceForUID(ctx, m.Signer, req.UID, string(req.Role), now)
	if err != nil {
		return mobilesession.CustomToken{}, err
	}
	// The life is the contract's own figure and not the token's: Firebase's
	// maximum is token.Lifetime, the app is told CustomTokenLifetime, and the
	// two are one hour (C1's RefreshResponse.ExpiresIn). A token minted here is
	// one the app exchanges at once, so the shorter of the two is what it
	// reports and what the phone plans against.
	return mobilesession.CustomToken{
		Token:     dev.Token,
		ExpiresAt: now.Add(mobilesession.CustomTokenLifetime),
	}, nil
}
