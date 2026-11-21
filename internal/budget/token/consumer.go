package token

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The mailbox consumer's token (mobile plan task P5.1f; the design is
// docs/mobile/design/always-on-orchestrator.md, its section 4.2 and its table
// of the `orch` account's grants in section 4.3).
//
// **What it is for.** The laptop consumer writes the outbox and the heartbeat
// with the operator's own admin credential, which bypasses the rules
// entirely (D113b says so out loud). The resident orchestrator is a service
// account, so it has no such credential: the only way it may write those two
// nodes at all is the rules' CONSUMER macro, `auth.token.mc != null &&
// auth.token.mc == $mb` — a claim this package mints and nothing else in the
// tree does. So the cloud consumer is the first mailbox writer the rules can
// hold to "the outbox and the heartbeat and nothing else" (the design's
// section 4.3), and this file is the mint that makes it so.
//
// **The uid is the mailbox, and that is not a shorthand.** CONSUMER reads `mc`
// and never `auth.uid`: the claim names the mailbox the token serves, so the
// uid is the one string the rules compare and a second uid would be a second
// name for one identity — which is the drift this package refuses everywhere
// else (mint.go's read-back, device.go's m~, pairing.go's p~). A mailbox is a
// Firebase uid already (outbox.go's mailboxKey: at most 128 characters), so
// the token's uid is a uid the database holds, and the claims a mailbox
// owner's token would need to be one (`mu == auth.uid` and the device node)
// are absent: this identity is admitted by CONSUMER and by nothing else.
//
// **The mailbox is the device's, and the shape is checked.** The mailbox is
// the phone's own uid — the `m~` uid MintDevice minted and the pairing the
// cloud copy of the key is filed under (keychain.Cloud's versions are keyed
// by it) — so a mint is held to that family rather than to "some non-empty
// string": a typo in an installation's wiring would otherwise mint a token
// that passes nothing, and a caller would read the empty mailbox as a phone
// that never speaks.

// ConsumerClaim is M4's claim for the consumer identity: a custom claim `mc`
// that names the mailbox its holder serves, which is the whole of what
// rules.go's CONSUMER reads. It is read and never minted by the rules, like
// `mu` and `pu`.
const ConsumerClaim = "mc"

// ConsumerToken is a minted consumer token and the mailbox it serves. Its
// Token is a Firebase custom token and it is a credential for that mailbox's
// outbox and heartbeat: it reaches no screen, no log line and no --json
// document. Its String and GoString keep it out of %v and %#v, as DeviceToken
// and Session's do.
type ConsumerToken struct {
	Mailbox string
	Token   string
}

func (c ConsumerToken) String() string {
	return "token.ConsumerToken{mailbox: " + c.Mailbox + ", token redacted}"
}
func (c ConsumerToken) GoString() string { return c.String() }

// MintConsumer mints the consumer's custom token for the mailbox it serves.
//
// exp is now + Lifetime, Firebase's maximum, for MintDevice's reason: the
// token is the process's own and the process is what refreshes it, so what
// ends a consumer is the pairing's revocation (the key's cloud copy destroyed,
// `fugaro pair revoke` step 2b) rather than a clock — a consumer whose token
// had expired mid-pass would take no lease and answer no message until
// somebody thought to mint again.
//
// **Why the signer and not an OAuth scope.** No Google identity carries a
// custom claim, and the rules read `mc`, so the token has to be a Firebase
// custom token signed by the project's signer account (signJwt, section 4.3's
// first row). The exchange that follows — `Exchange`, which needs the
// project's web API key — is the caller's step, because the key is a fact of
// the deployment and this package holds no configuration.
func MintConsumer(ctx context.Context, signer Signer, mailbox string, now time.Time) (ConsumerToken, error) {
	email := signer.Email()
	if email == "" {
		return ConsumerToken{}, errors.New("budget token: no signer")
	}
	if err := checkMailbox(mailbox); err != nil {
		return ConsumerToken{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"iss": email, "sub": email, "aud": Audience,
		"iat": now.Unix(), "exp": now.Add(Lifetime).Unix(),
		"uid": mailbox,
		"claims": map[string]any{
			ConsumerClaim: mailbox,
		},
	})
	if err != nil {
		return ConsumerToken{}, err
	}
	tok, err := signer.SignJWT(ctx, payload)
	if err != nil {
		return ConsumerToken{}, err
	}
	// The signer is trusted, but a token whose claim names another mailbox
	// must never leave this function (MintDevice's own read-back, and the
	// reason is sharper here: CONSUMER is `mc == $mb`, so a token naming a
	// different mailbox is a consumer of that mailbox's outbox and heartbeat
	// and of nothing of this caller's).
	got, err := parseJWT(tok)
	if err != nil {
		return ConsumerToken{}, errors.New("budget token: the signer returned something that is not a JWT")
	}
	claims, _ := got["claims"].(map[string]any)
	if claimString(got, "uid") != mailbox || claimString(claims, ConsumerClaim) != mailbox {
		return ConsumerToken{}, fmt.Errorf("%w: the signer returned a consumer token for another mailbox", ErrTokenMismatch)
	}
	return ConsumerToken{Mailbox: mailbox, Token: tok}, nil
}

// checkMailbox refuses a mailbox that is not a device uid this package's own
// minting produces: deviceUIDPrefix and deviceUIDBytes of unpadded base64url,
// which is what MintDevice writes and what keychain.Cloud files a pairing's
// key under. The refusal states the shape and never the value.
func checkMailbox(mailbox string) error {
	const want = "an m~ uid of one unpadded base64url group"
	if !strings.HasPrefix(mailbox, deviceUIDPrefix) {
		return fmt.Errorf("budget token: a consumer's mailbox is %s (%s)", want, "the uid MintDevice minted for the phone")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(mailbox[len(deviceUIDPrefix):])
	if err != nil || len(raw) != deviceUIDBytes || len(mailbox) > maxUIDBytes {
		return fmt.Errorf("budget token: a consumer's mailbox is %s (%s)", want, "the uid MintDevice minted for the phone")
	}
	return nil
}
