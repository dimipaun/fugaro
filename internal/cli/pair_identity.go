package cli

// Where `fugaro pair`'s two tokens come from (LM10 of
// docs/launcher-does-not-mint): the installation's credentials service, which
// mints both of them from its own draw (D3126, R14), and the operator's own
// signer, which is what this command did before the service had an identity
// half and what it still does where the service says it has none (R7).
//
// The two asks are C1's person routes — identitywire.PairingPath and
// identitywire.DevicePath — and they carry no field: the pairing id and the
// device's claims are the issuer's to derive, so a request that named one would
// be a caller choosing the name this project's database path, the phone's
// session and the delivery's AAD are all built from. The answer's ids are
// therefore held to the shape internal/budget/token draws before this command
// carries them anywhere: an id becomes a path in this installation's database
// (pairPath, rules.PathDevices), and a name that is not one path segment is a
// write whose place somebody else chose.
//
// The bridge is R7's, and it is exactly three answers: no service configured,
// the service's own "this revision holds no issuer"
// (identitywire.RefusalNotServed, answered 503), and the answer of a release
// behind, which is the 404/405 slipRouteAbsent already reads. Nothing else
// bridges. A refusal is carried as a refusal with the fix, because a CLI that
// signed locally on one would mint with a role `fugaro update` has taken away
// and stop with IAM's own error in place of the sentence that names the fix.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/identitywire"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/safetext"
)

const (
	// maxPairAnswerBytes bounds what the two identity routes may answer, as
	// maxSlipAnswerBytes bounds the grant route's: the service is another
	// writer, and an answer nothing bounds is an answer this command renders
	// into memory.
	maxPairAnswerBytes = 16 << 10
	// maxPairTokenBytes bounds a token this command will carry. A pairing token
	// and a device token are both IAM-signed JWTs of a few hundred bytes; the
	// bound is this side's own and generous, and its purpose is that no answer
	// is carried whose size this command has not read.
	maxPairTokenBytes = 8 << 10
	// pairUIDBytes and pairDeviceUIDBytes are the two draws behind the ids this
	// command writes under: internal/budget/token's pairingUIDBytes and
	// deviceUIDBytes. That package keeps them, and its checkers, unexported,
	// and this command takes the names from another writer now — so the shape
	// is respelled here, where the name stops being the mint's own business and
	// becomes this installation's database path.
	pairUIDBytes       = 32
	pairDeviceUIDBytes = 16
	// pairDeviceUIDPrefix is internal/budget/token's deviceUIDPrefix, in the
	// same place and for the same reason: the delivery's keychain item is named
	// after the device id with this prefix trimmed (pairItemName).
	pairDeviceUIDPrefix = "m~"
)

var (
	// errPairIssuerAbsent means this installation has no issuer to ask: no
	// service is configured, or the service answered that it mints nothing
	// (identitywire.RefusalNotServed), or it is a release behind and answered
	// an unknown path (slipRouteAbsent). Those three are R7's whole bridge and
	// they are told apart from every refusal by errors.Is — a CLI that read a
	// refusal as this one would sign locally with a credential the installation
	// no longer grants.
	errPairIssuerAbsent = errors.New("this installation's credentials service mints no pairing tokens")
	// errPairIssuerRefused means the service answered a refusal: this caller is
	// not one it admits, or the ask itself was refused. The operator's fix is
	// the service's own roster or release, never a local mint.
	errPairIssuerRefused = errors.New("the installation's credentials service refused the pairing")
	// errPairIssuerAnswer means the ask was made and the answer cannot be used:
	// the service could not be reached, its answer was not read back, or the
	// token and the id it carried are not ones this command will write into a
	// QR code and a database path. Nothing was minted and nothing was written.
	errPairIssuerAnswer = errors.New("the installation's credentials service's answer cannot be used for a pairing")
)

// pairIdentity mints the operator's own Google ID token for serviceURL, which
// is the identity the service's person class verifies (C1, LM3). It is the same
// plain mint the slip asks with, and a package variable for the same reason
// slipIdentity is one: no test may run gcloud.
var pairIdentity = func(ctx context.Context, serviceURL string) (string, error) {
	return personIdentity(ctx, serviceURL, "a pairing token is asked for")
}

// pairIssuer is where this command's two tokens come from, and it is one value
// for the whole pairing: the two asks are made minutes apart (the phone's key
// and the typed code are between them) and the bridge's own signer is read once
// for both.
type pairIssuer struct {
	lc      *localcfg.Config
	now     time.Time
	timeout time.Duration
	// service is the installation's credentials service, and "" where the
	// config carries no block: R7's first bridge answer. The URL is the block
	// `fugaro update` writes, which the loader has already held to the one
	// shape an identity token may be sent to.
	service string
	// signer is the operator's own signer, read when the bridge first fires.
	signer token.Signer
}

func newPairIssuer(lc *localcfg.Config, now time.Time, timeout time.Duration) *pairIssuer {
	p := &pairIssuer{lc: lc, now: now, timeout: timeout}
	if lc.CredentialsService != nil {
		p.service = lc.CredentialsService.URL
	}
	return p
}

// pairMinted is the pairing route's answer as this command uses it: the
// one-shot token the QR code carries, the id the pairing session is written
// under, and when the token stops working.
type pairMinted struct {
	Token     string
	UID       string
	ExpiresAt time.Time
}

// Pairing mints the one-shot pairing token and the id it names.
//
// ExpiresAt is what the caller reports as expires_at (the QR payload and the
// done document). Where the service answered, its own figure is reported: that
// is the token's real exp, and the wait the operator asked for does not reach
// the ask (the request has no field for it), so a locally computed figure would
// be this command's guess at what the service did. Where the answer carries
// none — a field this release's reader requires, and one it reads as absent
// rather than as an expiry — the figure this command reported before it had a
// service to ask is used, which is the same value the local mint's token has.
func (p *pairIssuer) Pairing(ctx context.Context) (pairMinted, error) {
	var ans identitywire.PairingResponse
	switch err := p.ask(ctx, identitywire.PairingPath, identitywire.PairingRequest{}, &ans); {
	case errors.Is(err, errPairIssuerAbsent):
		return p.signPairing(ctx)
	case err != nil:
		return pairMinted{}, err
	}
	if !pairUIDUsable(ans.Pairing, token.PairingPrefix, pairUIDBytes) {
		return pairMinted{}, remote(fmt.Errorf("%w: the pairing route answered a pairing id this command will not write under (one %q and one unpadded base64url string of %d bytes, which is what a path in this installation's database, the phone's session and the delivery's AAD are all built from), so nothing was written; run '%s update' — the CLI and the service its installation runs are two halves of one release — and if the answer is still not one this command will carry, the service's own log has what it minted", errPairIssuerAnswer, token.PairingPrefix, pairUIDBytes, selfCommand()))
	}
	if !pairCustomTokenUsable(ans.Token) {
		return pairMinted{}, remote(fmt.Errorf("%w: the pairing route answered a token that is not a Firebase custom token (three dot-separated parts on one printable line, within %d bytes), which is what the phone signs in with, so nothing was written; run '%s update' — the CLI and the service its installation runs are two halves of one release — and if the answer is still not one this command will carry, the service's own log has what it minted", errPairIssuerAnswer, maxPairTokenBytes, selfCommand()))
	}
	if ans.ExpiresAt.IsZero() {
		ans.ExpiresAt = p.now.Add(token.PairingLifetime(p.timeout))
	}
	return pairMinted{Token: ans.Token, UID: ans.Pairing, ExpiresAt: ans.ExpiresAt}, nil
}

// Device mints the device token of a pairing whose typed code matched, and the
// id the device node and the keychain item are written under.
//
// The answer's expiry is not used here: the device token's own exp is its
// claim, nothing this command writes records it, and the device node — not a
// clock — is what revokes a pairing (D166).
func (p *pairIssuer) Device(ctx context.Context) (token.DeviceToken, error) {
	var ans identitywire.DeviceResponse
	switch err := p.ask(ctx, identitywire.DevicePath, identitywire.DeviceRequest{}, &ans); {
	case errors.Is(err, errPairIssuerAbsent):
		return p.signDevice(ctx)
	case err != nil:
		return token.DeviceToken{}, err
	}
	if !pairUIDUsable(ans.Device, pairDeviceUIDPrefix, pairDeviceUIDBytes) {
		return token.DeviceToken{}, remote(fmt.Errorf("%w: the device route answered a device id this command will not write under (one %q and one unpadded base64url string of %d bytes, which is what the device node's key and the keychain item's name are built from), so no device node, no keychain item and no delivery were written; run '%s update' — the CLI and the service its installation runs are two halves of one release — and if the answer is still not one this command will carry, the service's own log has what it minted", errPairIssuerAnswer, pairDeviceUIDPrefix, pairDeviceUIDBytes, selfCommand()))
	}
	if !pairCustomTokenUsable(ans.Token) {
		return token.DeviceToken{}, remote(fmt.Errorf("%w: the device route answered a token that is not a Firebase custom token (three dot-separated parts on one printable line, within %d bytes), which is the credential the phone stores, so no device node, no keychain item and no delivery were written; run '%s update' — the CLI and the service its installation runs are two halves of one release — and if the answer is still not one this command will carry, the service's own log has what it minted", errPairIssuerAnswer, maxPairTokenBytes, selfCommand()))
	}
	return token.DeviceToken{UID: ans.Device, Token: ans.Token}, nil
}

// signPairing is R7's bridge for the first token: the mint this command made
// before the service had an identity half, with the operator's own credential.
func (p *pairIssuer) signPairing(ctx context.Context) (pairMinted, error) {
	signer, err := p.signerFor(ctx)
	if err != nil {
		return pairMinted{}, err
	}
	tok, uid, err := token.MintPairing(ctx, signer, p.timeout, p.now)
	if err != nil {
		return pairMinted{}, err
	}
	return pairMinted{Token: tok, UID: uid, ExpiresAt: p.now.Add(token.PairingLifetime(p.timeout))}, nil
}

// signDevice is the bridge's second token, minted the same way and for the same
// reason.
func (p *pairIssuer) signDevice(ctx context.Context) (token.DeviceToken, error) {
	signer, err := p.signerFor(ctx)
	if err != nil {
		return token.DeviceToken{}, err
	}
	return token.MintDevice(ctx, signer, p.now)
}

// signerFor reads the operator's own signer, once: a bridged pairing mints
// twice with it, and a credential this command could not read is one refusal
// rather than two.
func (p *pairIssuer) signerFor(ctx context.Context) (token.Signer, error) {
	if p.signer != nil {
		return p.signer, nil
	}
	signer, err := (&cloudEnv{lc: p.lc}).tokenSigner(ctx)
	if err != nil {
		return nil, err
	}
	p.signer = signer
	return signer, nil
}

// ask makes one call of the identity contract, and answers errPairIssuerAbsent
// where there is nothing to ask.
func (p *pairIssuer) ask(ctx context.Context, path string, req, out any) error {
	if p.service == "" {
		return errPairIssuerAbsent
	}
	return pairAskOverService(ctx, p.service, path, req, out)
}

// pairAskOverService is one ask of the identity contract: the operator's own
// Google ID token presented to a person route of the installation's credentials
// service, and the answer read back strictly.
//
// The whole of it — the mint and the call — is bounded once, as the slip's
// grant call is, and nothing here retries, waits or asks a question: the one
// wait a pairing has is the phone's key and the typed code, and this task adds
// none (the no-prompt policy's table names LM10 for it).
//
// Nothing here can put a value a caller sent into an error: the identity token
// is never formatted into one, the request body is the contract's own empty
// ask, and the answer is read for its status, its size and its shape alone.
func pairAskOverService(ctx context.Context, serviceURL, path string, req, out any) error {
	// The loader has held this URL to https with a host and nothing else, and
	// the rule is asked again here because the token presented to it is a
	// bearer credential: over plain http it would travel in cleartext.
	if err := checkSlipServiceURL(serviceURL); err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, slipGrantTimeout)
	defer cancel()
	identity, err := pairIdentity(sctx, serviceURL)
	if err != nil {
		return err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return errors.New("the identity call's request could not be built")
	}
	hreq, err := http.NewRequestWithContext(sctx, http.MethodPost, strings.TrimSuffix(serviceURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("the identity call's request could not be built")
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+identity)
	hreq.Header.Set(credsvc.HeaderPersonIdentity, identity)
	// The slip's own client, and not a second one: no redirect (the request
	// carries this operator's ID token, and a followed 307 would carry it to
	// whatever host a Location named), no cookie jar, one bound.
	resp, err := slipClient().Do(hreq)
	if err != nil {
		return remote(fmt.Errorf("%w: the installation's credentials service could not be reached, and a pairing on an installation that has one mints both of its tokens there: nothing was written and no token exists; check that the service is deployed and answering — '%s doctor' reads the installation's service and '%s update' re-applies it — and ask again", errPairIssuerAnswer, selfCommand(), selfCommand()))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPairAnswerBytes+1))
	if err != nil {
		return remote(fmt.Errorf("%w: the service's answer could not be read, and nothing was written: ask again, and '%s doctor' reads the installation if it happens twice", errPairIssuerAnswer, selfCommand()))
	}
	// Read before the status is judged, and bounded: a refusal's body is where
	// the code the operator acts on lives, and nothing reads an unbounded body
	// to find it.
	if slipRouteAbsent(resp.StatusCode) {
		return fmt.Errorf("%w: the service answered %d, which is the answer of a service whose release does not serve this route", errPairIssuerAbsent, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return pairServiceRefusal(resp.StatusCode, data)
	}
	if len(data) > maxPairAnswerBytes {
		return remote(fmt.Errorf("%w: the service's answer is larger than a pairing token's, and nothing was written", errPairIssuerAnswer))
	}
	if err := identitywire.DecodeStrict(data, out); err != nil {
		return remote(fmt.Errorf("%w: the service's answer is not one of this contract's (it is read strictly: one JSON object, and no field this release does not know), so nothing was written; run '%s update' — the CLI and the service its installation runs are two halves of one release", errPairIssuerAnswer, selfCommand()))
	}
	return nil
}

// pairServiceRefusal reads a refusal body and turns it into the sentence the
// operator is owed: what the service refused, and the fix.
//
// The service's own message is never repeated. It is another writer's text,
// it reaches a terminal and a log line, and the code is what this CLI acts on —
// the sentence beside it is written for the service's log (internal/credsvc's
// own rule for its refusal bodies, and internal/gitprov's read mint drops it
// for the same reason). The code is another writer's text too, so it is printed
// through safetext's rule for one.
func pairServiceRefusal(status int, body []byte) error {
	e, err := credsvc.ReadWireError(status, body)
	if err != nil {
		return fmt.Errorf("%w: the service refused the ask (%d) and the refusal is not one of this contract's bodies (one JSON object naming a code): run '%s doctor' — it reads the installation's service and what it was deployed with — and '%s update', which re-applies it; the service's own log has the answer it wrote", errPairIssuerRefused, status, selfCommand(), selfCommand())
	}
	code := safetext.Field(e.Error)
	switch e.Error {
	case identitywire.RefusalNotServed:
		// The service's own answer for a revision whose identity half is not
		// configured (I1): it holds no issuer, which is R7's first bridge, and
		// the sentence below is never printed — the command signs locally
		// instead, as it did before this route existed.
		return fmt.Errorf("%w: the service answered %s", errPairIssuerAbsent, code)
	case identitywire.RefusalCallerNotAdmitted:
		return fmt.Errorf("%w: %s — a pairing is asked for by this installation's own people, whom the roster the service verifies against names, and this machine's credential is not one of them; '%s update' is what renders that roster into the service, and '%s doctor' prints the service's own answer if the refusal stays", errPairIssuerRefused, code, selfCommand(), selfCommand())
	case identitywire.RefusalIdentityRefused:
		return fmt.Errorf("%w: %s — the ask itself was refused, and a pairing's ask names nothing a caller could get wrong (C1's requests have no field): run '%s update', which converges a CLI and the service its installation runs — they are two halves of one release — and the service's own log has the reason it wrote", errPairIssuerRefused, code, selfCommand())
	default:
		return fmt.Errorf("%w: %s, which this release knows no pairing refusal by: run '%s update', which converges a CLI and the service its installation runs — they are two halves of one release — and the service's own log has the refusal it wrote", errPairIssuerRefused, code, selfCommand())
	}
}

// pairIssuerCode names the case a token source's failure is, in the wire code a
// console switches on (§4.6). A service that refused is not a database that
// cannot be reached, and neither is an answer that could not be used, so each
// gets its own name; what the local mint fails with keeps the codes it had —
// the missing role first (pairRefusedMinter, whose refusal sentence is the one
// that carries the fix), and everything else the cloud's, which is §4.6's
// unreachable row.
func pairIssuerCode(err error) string {
	switch {
	case errors.Is(err, token.ErrNotMinter):
		return pairRefusedMinter
	case errors.Is(err, errPairIssuerRefused):
		return pairRefusedIssuer
	case errors.Is(err, errPairIssuerAbsent), errors.Is(err, errPairIssuerAnswer):
		// errPairIssuerAbsent is bridged where it is raised, so reaching here
		// means nothing at all was minted: the answer row is what an operator
		// reads for that, and it is not the database's.
		return pairRefusedIssuerAnswer
	default:
		return pairRefusalCode(err, pairRefusedUnreachable)
	}
}

// pairMinterRefusal is the bridge's own failure, told as the fix it has. The
// local mint signs with the operator's own credential, which needs the signer
// account's fugaroTokenMinter role — and an installation whose credentials
// service mints these two tokens has no such role to hold any more (LM14 takes
// the people's row away once the issuer answers its probe), so what an operator
// meets here is a release skew and not a missing grant. IAM's own words are
// kept, because they are what the role's absence looks like, and the sentence
// that names what to run is this one.
func pairMinterRefusal(lc *localcfg.Config, err error) error {
	return fmt.Errorf("%s; pairing mints the one-shot pairing token with your own credential, which needs the fugaroTokenMinter role on the signer account %s (the installation's launchers and operators hold it) — and an installation whose credentials service mints these two tokens has no such role to hold: run '%s update', which is what reaches the release whose service mints them, or ask an owner of this installation for the role where it really has no service to ask", oneLine(err.Error()), lc.Budget.TokenSigner, selfCommand())
}

// pairCustomTokenUsable reports whether tok is one of the Firebase custom
// tokens both routes mint: IAM's signed JWT, which is what the phone signs in
// with and what it stores as a device. The shape is asked here — three
// dot-separated parts, one printable line, within this command's bound — so an
// answer that is not one is refused before a QR code carries it to a phone that
// can only say "invalid token".
func pairCustomTokenUsable(tok string) bool {
	return tok != "" && len(tok) <= maxPairTokenBytes && strings.Count(tok, ".") == 2 && reviewPrintableToken(tok)
}

// pairUIDUsable reports whether uid is an id this command will write under:
// prefix and one unpadded base64url string of size bytes, which is the one
// spelling internal/budget/token draws and joins back together (a pairing id is
// bound into the delivery's AAD, and the rules read it as the node's own name).
//
// It is asked of an answer from another writer, and it is asked before anything
// is written, because the id becomes a database path in THIS project
// (pairPath, rules.PathDevices): a name carrying a slash or a dot would be a
// write outside the pairing subtree, chosen by the service rather than by this
// command. The strict decoder refuses both, and refuses the non-canonical
// spellings of the same bytes with them.
func pairUIDUsable(uid, prefix string, size int) bool {
	body, ok := strings.CutPrefix(uid, prefix)
	if !ok || len(body) > maxPairAnswerBytes {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(body)
	return err == nil && len(raw) == size
}
