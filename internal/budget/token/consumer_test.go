package token_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

// consumerMailbox is a mailbox of the family MintDevice mints and keychain.Cloud
// files a pairing's key under: m~ and one unpadded base64url group. It is
// written out rather than drawn, because the mint's own shape check is what
// several cases below are about.
const consumerMailbox = "m~AAAAAAAAAAAAAAAAAAAAAA"

// TestConsumerTokenIsTheMcClaimTheRulesRead pins the whole of what this mint
// produces, field by field: the uid and the one claim are the mailbox the token
// serves (rules.go's CONSUMER is `auth.token.mc == $mb` and reads no other
// claim), the issuer is the signer, the audience is the identity toolkit's, and
// the lifetime is Firebase's maximum — a consumer whose token expired mid-pass
// would take no lease and answer no message.
func TestConsumerTokenIsTheMcClaimTheRulesRead(t *testing.T) {
	got, err := token.MintConsumer(context.Background(), pairingSigner{email: signerEmail}, consumerMailbox, t0)
	if err != nil {
		t.Fatalf("MintConsumer: %v", err)
	}
	if got.Mailbox != consumerMailbox {
		t.Errorf("Mailbox = %q, want %q", got.Mailbox, consumerMailbox)
	}
	m := pairingPayload(t, got.Token)
	if uid := m["uid"]; uid != consumerMailbox {
		t.Errorf("uid = %#v, want the mailbox the token serves: CONSUMER reads mc and never auth.uid, so the uid is the one string the rules compare", uid)
	}
	claims, ok := m["claims"].(map[string]any)
	if !ok {
		t.Fatalf("claims = %#v, want an object", m["claims"])
	}
	if len(claims) != 1 || claims["mc"] != consumerMailbox {
		t.Errorf("claims = %#v, want exactly {mc: %q}: a consumer that carried a second claim (mu, pu, fp) would be an identity the rules admit somewhere else", claims, consumerMailbox)
	}
	if iss := m["iss"]; iss != signerEmail {
		t.Errorf("iss = %#v, want the signer", iss)
	}
	if aud := m["aud"]; aud != token.Audience {
		t.Errorf("aud = %#v, want the identity toolkit's", aud)
	}
	if span := time.Duration(pairingNumber(t, m, "exp")-pairingNumber(t, m, "iat")) * time.Second; span != token.Lifetime {
		t.Errorf("exp - iat = %s, want %s", span, token.Lifetime)
	}
}

// TestConsumerTokenKeepsItselfOutOfPrintf: the token is a credential for the
// mailbox's outbox and heartbeat, so it never reaches a log line, a screen or a
// --json document — the same rule DeviceToken and Session's keep.
func TestConsumerTokenKeepsItselfOutOfPrintf(t *testing.T) {
	got := token.ConsumerToken{Mailbox: consumerMailbox, Token: "h.body.s"}
	for _, s := range []string{got.String(), got.GoString(), strings.Join([]string{got.String()}, " ")} {
		if strings.Contains(s, "h.body.s") {
			t.Errorf("the token's own spelling shows the credential: %q", s)
		}
	}
}

// TestConsumerTokenRefusesAMailboxThatIsNotADeviceUID is the shape check: the
// mailbox is the phone's own m~ uid (MintDevice's, and the one the pairing key
// is filed under), so anything else is a wiring mistake that would mint a token
// CONSUMER never admits. The refusal states the shape and never the value.
func TestConsumerTokenRefusesAMailboxThatIsNotADeviceUID(t *testing.T) {
	for _, tc := range []struct{ what, mailbox string }{
		{"empty", ""},
		{"a bare name", "aurora"},
		{"a pairing uid", "p~AAAAAAAAAAAAAAAAAAAAAA"},
		{"a run uid", "r~acme~1234"},
		{"the prefix alone", "m~"},
		{"a standard-alphabet spelling", "m~AAAAAAAAAAAAAAAAAAAAA+"},
		{"a padded spelling", "m~AAAAAAAAAAAAAAAAAAAAAA=="},
		{"a short group", "m~AAAAAAAAAAAAAAAAAAAAA"},
		{"a uid past Firebase's bound", "m~" + strings.Repeat("A", 130)},
	} {
		got, err := token.MintConsumer(context.Background(), pairingSigner{email: signerEmail}, tc.mailbox, t0)
		if err == nil {
			t.Errorf("%s (%q): MintConsumer minted a token for it", tc.what, tc.mailbox)
		}
		if got.Token != "" || got.Mailbox != "" {
			t.Errorf("%s: a refused mint returned a token or a mailbox", tc.what)
		}
		// The shape the refusal states is the one the prefix names, so the
		// bare-prefix case is skipped: "m~" is inside it.
		if len(tc.mailbox) > 4 && strings.Contains(err.Error(), tc.mailbox) {
			t.Errorf("%s: the refusal quotes the mailbox it refused (%v)", tc.what, err)
		}
	}
}

// TestConsumerTokenRefusesNoSigner: a signer that names no account is refused
// before anything is minted, as MintDevice and MintPairing refuse one.
func TestConsumerTokenRefusesNoSigner(t *testing.T) {
	if _, err := token.MintConsumer(context.Background(), pairingSigner{}, consumerMailbox, t0); err == nil {
		t.Error("MintConsumer minted a token for a signer with no email")
	}
}

// renamingConsumerSigner hands back a token whose uid, or whose mc claim, it
// rewrote: CONSUMER is `mc == $mb`, so a token naming another mailbox is a
// consumer of that mailbox's outbox and heartbeat, and the mint's own read-back
// must refuse it rather than return it.
type renamingConsumerSigner struct {
	email string
	field string
}

func (s renamingConsumerSigner) Email() string { return s.email }

func (s renamingConsumerSigner) SignJWT(_ context.Context, payload []byte) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return "", err
	}
	if s.field == "uid" {
		m["uid"] = "m~BBBBBBBBBBBBBBBBBBBBBB"
	} else {
		m["claims"] = map[string]any{"mc": "m~BBBBBBBBBBBBBBBBBBBBBB"}
	}
	body, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return "h." + base64.RawURLEncoding.EncodeToString(body) + ".s", nil
}

func TestConsumerTokenRefusesSignerThatRenamesTheMailbox(t *testing.T) {
	for _, field := range []string{"uid", "mc"} {
		got, err := token.MintConsumer(context.Background(), renamingConsumerSigner{email: signerEmail, field: field}, consumerMailbox, t0)
		if !errors.Is(err, token.ErrTokenMismatch) {
			t.Errorf("a signer that rewrote %s: err = %v, want ErrTokenMismatch", field, err)
		}
		if got.Token != "" || got.Mailbox != "" {
			t.Errorf("a signer that rewrote %s: MintConsumer returned a token or a mailbox alongside its refusal", field)
		}
	}
}

// TestConsumerTokenRefusesANonJWT: a signer that answers with something that is
// not a JWT is refused rather than passed on (MintDevice's own read-back).
func TestConsumerTokenRefusesANonJWT(t *testing.T) {
	if _, err := token.MintConsumer(context.Background(), opaqueSigner{}, consumerMailbox, t0); err == nil {
		t.Error("MintConsumer returned a signer's non-JWT answer as a token")
	}
}

// opaqueSigner answers a bare string, which is not a JWT.
type opaqueSigner struct{}

func (opaqueSigner) Email() string { return signerEmail }
func (opaqueSigner) SignJWT(context.Context, []byte) (string, error) {
	return "not-a-jwt", nil
}
