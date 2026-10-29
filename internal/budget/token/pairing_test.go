package token_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

// pairingSigner is a Signer that signs nothing: it wraps the claim set it is
// handed in a JWT-shaped string, which is all MintPairing's own re-parse reads.
type pairingSigner struct{ email string }

func (s pairingSigner) Email() string { return s.email }

func (s pairingSigner) SignJWT(ctx context.Context, payload []byte) (string, error) {
	return "h." + base64.RawURLEncoding.EncodeToString(payload) + ".s", nil
}

// pairingPayload is the claim set of a token this package minted, decoded
// without verifying it (the signer is the fake above).
func pairingPayload(t *testing.T, tok string) map[string]any {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token is not a JWT: %q", tok)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func pairingNumber(t *testing.T, m map[string]any, k string) int64 {
	t.Helper()
	v, ok := m[k].(float64)
	if !ok {
		t.Fatalf("claim %s = %#v, want a number", k, m[k])
	}
	return int64(v)
}

// The pairing token: a p~ uid carrying pu alone, whose exp covers the wait and
// never outlives D165's five minutes (plan §4.1, §4.4, §7.1 step 3).
func TestPairingTokenShape(t *testing.T) {
	ctx := context.Background()
	timeouts := []time.Duration{30 * time.Second, 2 * time.Minute, 4*time.Minute + 30*time.Second}
	for _, timeout := range timeouts {
		tok, uid, err := token.MintPairing(ctx, pairingSigner{email: signerEmail}, timeout, t0)
		if err != nil {
			t.Fatalf("timeout %s: %v", timeout, err)
		}
		if !strings.HasPrefix(uid, "p~") {
			t.Fatalf("timeout %s: uid = %q, want p~…", timeout, uid)
		}
		if strings.HasPrefix(uid, "r~") || strings.HasPrefix(uid, "m~") {
			t.Fatalf("timeout %s: uid = %q, want neither a run's nor a device's prefix", timeout, uid)
		}
		got := pairingPayload(t, tok)
		if got["uid"] != uid {
			t.Fatalf("timeout %s: uid claim = %v, want %q", timeout, got["uid"], uid)
		}
		extra, ok := got["claims"].(map[string]any)
		if !ok {
			t.Fatalf("timeout %s: no claims object in %v", timeout, got)
		}
		if len(extra) != 1 || extra["pu"] != uid {
			t.Fatalf("timeout %s: claims = %v, want pu alone and equal to the uid", timeout, extra)
		}
		if pu, _ := extra["pu"].(string); !strings.HasPrefix(pu, "p~") {
			t.Fatalf("timeout %s: pu = %q, want p~…", timeout, pu)
		}
		iat, exp := pairingNumber(t, got, "iat"), pairingNumber(t, got, "exp")
		if span := time.Duration(exp-iat) * time.Second; span > token.PairingMaxLifetime {
			t.Fatalf("timeout %s: exp - iat = %s, above %s", timeout, span, token.PairingMaxLifetime)
		}
		if time.Duration(exp-iat)*time.Second < timeout {
			t.Fatalf("timeout %s: exp - iat = %ds, shorter than the wait", timeout, exp-iat)
		}
	}
}
