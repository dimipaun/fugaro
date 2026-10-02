package gcpfake

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// IAMCredentials is a fake of the IAM Credentials v1 call the launcher
// makes to mint a Firebase custom token: serviceAccounts.signJwt. It
// implements nothing else (a generateAccessToken, say, fails the test), so a
// mint that asked for an access token would be noticed. A signature is an
// HMAC under a per-fake secret, so the Identity Toolkit fake can tell a
// token this fake signed from a forged one; the header is the real one
// (RS256, a key id) and the signature is not a real RSA signature.
type IAMCredentials struct {
	*Server

	mu      sync.Mutex
	secret  []byte
	signers map[string]bool
	calls   []SignCall
}

// SignCall is one signJwt call: the signer it named, the payload, and the
// Authorization header of the caller (the launcher's own credential).
type SignCall struct {
	Signer, Payload, Authorization string
}

var signJWTRE = regexp.MustCompile(`^/v1/projects/-/serviceAccounts/([^/:]+):signJwt$`)

// NewIAMCredentials starts an IAM Credentials fake that lives until the test
// ends.
func NewIAMCredentials(t *testing.T) *IAMCredentials {
	t.Helper()
	f := &IAMCredentials{secret: []byte("fake-iamcredentials-" + t.Name()), signers: map[string]bool{}}
	f.Server = newServer(t, f.handle)
	return f
}

// AddSigner makes the service account email exist and be signable.
func (f *IAMCredentials) AddSigner(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signers[email] = true
}

// SignCalls returns the signJwt calls received, in order.
func (f *IAMCredentials) SignCalls() []SignCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SignCall(nil), f.calls...)
}

func (f *IAMCredentials) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	m := signJWTRE.FindStringSubmatch(r.URL.Path)
	if m == nil || r.Method != http.MethodPost {
		f.unhandled(w, r)
		return
	}
	signer := m[1]
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") || len(auth) < len("Bearer x") {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "missing credential")
		return
	}
	var req struct {
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Payload == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "payload is required")
		return
	}
	var claims map[string]any
	if err := json.Unmarshal([]byte(req.Payload), &claims); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "payload must be a JSON object")
		return
	}
	if _, ok := claims["exp"]; !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "the JWT claim set needs exp")
		return
	}
	f.mu.Lock()
	known := f.signers[signer]
	f.calls = append(f.calls, SignCall{Signer: signer, Payload: req.Payload, Authorization: auth})
	f.mu.Unlock()
	if !known {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "service account "+signer+" does not exist")
		return
	}
	header := b64(`{"alg":"RS256","kid":"fake-key-id","typ":"JWT"}`)
	input := header + "." + b64(req.Payload)
	writeJSON(w, http.StatusOK, map[string]string{"keyId": "fake-key-id", "signedJwt": input + "." + f.sign(signer, input)})
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func (f *IAMCredentials) sign(signer, input string) string {
	mac := hmac.New(sha256.New, f.secret)
	mac.Write([]byte(signer + "|" + input))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Verify checks that jwt was signed by this fake as its own iss, and returns
// its claims and the signer.
func (f *IAMCredentials) Verify(jwt string) (claims map[string]any, signer string, err error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil, "", errors.New("not a JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, "", errors.New("bad payload encoding")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&claims); err != nil {
		return nil, "", errors.New("bad payload")
	}
	iss, _ := claims["iss"].(string)
	f.mu.Lock()
	known := f.signers[iss]
	f.mu.Unlock()
	if !known || !hmac.Equal([]byte(f.sign(iss, parts[0]+"."+parts[1])), []byte(parts[2])) {
		return nil, "", errors.New("bad signature")
	}
	return claims, iss, nil
}
