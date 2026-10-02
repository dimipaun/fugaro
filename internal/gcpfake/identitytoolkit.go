package gcpfake

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// IdentityToolkit is a fake of the two Firebase Auth calls the runner makes:
// Identity Toolkit accounts:signInWithCustomToken and Secure Token's
// /v1/token refresh. One server answers both (the clients are pointed at it
// with separate base URLs). It checks what Google checks of a custom token:
// the web API key, a signature this test's IAMCredentials fake made, iss ==
// sub, the audience, exp in the future and at most an hour after iat, a uid
// of at most 128 bytes, no reserved claim names, claims under 1000 bytes.
// An ID token is a JWT whose claims are the custom claims, flattened as
// Firebase does, signed with a secret of this fake (see IDTokenClaims).
//
// Hooks: SetClock, SetLifetime, RotateRefresh, DisableUser, DeleteUser and,
// from Server, Refuse (an outage).
type IdentityToolkit struct {
	*Server

	iam     *IAMCredentials
	apiKey  string
	project string
	secret  []byte

	mu        sync.Mutex
	now       func() time.Time
	lifetime  time.Duration
	rotate    bool
	users     map[string]*fbUser
	refresh   map[string]string // refresh token → uid
	exchanges int
	refreshes int
	nextID    int
}

type fbUser struct {
	claims   map[string]any
	disabled bool
}

// IdentityToolkitAudience is the aud a custom token must carry.
const IdentityToolkitAudience = "https://identitytoolkit.googleapis.com/google.identity.identitytoolkit.v1.IdentityToolkit"

var reservedClaims = map[string]bool{
	"acr": true, "amr": true, "at_hash": true, "aud": true, "auth_time": true, "azp": true, "cnf": true,
	"c_hash": true, "exp": true, "firebase": true, "iat": true, "iss": true, "jti": true, "nbf": true,
	"nonce": true, "sub": true,
}

// NewIdentityToolkit starts the fake for the Firebase project project, which
// accepts apiKey and trusts tokens signed by iam.
func NewIdentityToolkit(t *testing.T, iam *IAMCredentials, apiKey, project string) *IdentityToolkit {
	t.Helper()
	f := &IdentityToolkit{iam: iam, apiKey: apiKey, project: project, secret: []byte("fake-idtoolkit-" + t.Name()),
		now: time.Now, lifetime: time.Hour, users: map[string]*fbUser{}, refresh: map[string]string{}}
	f.Server = newServer(t, f.handle)
	return f
}

// SetClock sets the time the fake judges tokens by and stamps ID tokens with.
func (f *IdentityToolkit) SetClock(now func() time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = now
}

// SetLifetime sets the lifetime of the ID tokens it issues (default 1 h).
func (f *IdentityToolkit) SetLifetime(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lifetime = d
}

// RotateRefresh makes every refresh answer a new refresh token (the old one
// then stays valid, as Firebase's does).
func (f *IdentityToolkit) RotateRefresh(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rotate = on
}

// DisableUser makes uid's refreshes answer USER_DISABLED.
func (f *IdentityToolkit) DisableUser(uid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u := f.users[uid]; u != nil {
		u.disabled = true
	}
}

// DeleteUser removes uid; its refreshes answer USER_NOT_FOUND.
func (f *IdentityToolkit) DeleteUser(uid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.users, uid)
}

// Users lists the uids that signed in.
func (f *IdentityToolkit) Users() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for uid := range f.users {
		out = append(out, uid)
	}
	return out
}

// Exchanges and Refreshes count the accepted sign-ins and refreshes.
func (f *IdentityToolkit) Exchanges() int { f.mu.Lock(); defer f.mu.Unlock(); return f.exchanges }

// Refreshes counts the accepted refreshes.
func (f *IdentityToolkit) Refreshes() int { f.mu.Lock(); defer f.mu.Unlock(); return f.refreshes }

// IDTokenClaims verifies an ID token this fake issued (signature and expiry
// by the fake's clock) and returns its claims.
func (f *IdentityToolkit) IDTokenClaims(idToken string) (map[string]any, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 || !hmac.Equal([]byte(f.sign(parts[0]+"."+parts[1])), []byte(parts[2])) {
		return nil, errors.New("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, err
	}
	f.mu.Lock()
	now := f.now()
	f.mu.Unlock()
	if exp, _ := claims["exp"].(float64); int64(exp) <= now.Unix() {
		return nil, errors.New("expired")
	}
	return claims, nil
}

func (f *IdentityToolkit) sign(input string) string {
	mac := hmac.New(sha256.New, f.secret)
	mac.Write([]byte(input))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (f *IdentityToolkit) handle(w http.ResponseWriter, r *http.Request, body []byte) {
	if r.Method != http.MethodPost {
		f.unhandled(w, r)
		return
	}
	if r.URL.Query().Get("key") != f.apiKey {
		writeFirebaseError(w, http.StatusBadRequest, "API key not valid. Please pass a valid API key.")
		return
	}
	switch r.URL.Path {
	case "/v1/accounts:signInWithCustomToken":
		f.signIn(w, body)
	case "/v1/token":
		f.refreshToken(w, body)
	default:
		f.unhandled(w, r)
	}
}

func writeFirebaseError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func (f *IdentityToolkit) signIn(w http.ResponseWriter, body []byte) {
	var req struct {
		Token             string `json:"token"`
		ReturnSecureToken bool   `json:"returnSecureToken"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Token == "" {
		writeFirebaseError(w, http.StatusBadRequest, "MISSING_CUSTOM_TOKEN")
		return
	}
	claims, signer, err := f.iam.Verify(req.Token)
	if err != nil {
		writeFirebaseError(w, http.StatusBadRequest, "INVALID_CUSTOM_TOKEN : "+err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	num := func(k string) (int64, bool) {
		n, ok := claims[k].(json.Number)
		if !ok {
			return 0, false
		}
		v, err := n.Int64()
		return v, err == nil
	}
	exp, okExp := num("exp")
	iat, okIat := num("iat")
	uid, _ := claims["uid"].(string)
	extra, _ := claims["claims"].(map[string]any)
	extraJSON, _ := json.Marshal(extra)
	bad := func(why string) {
		writeFirebaseError(w, http.StatusBadRequest, "INVALID_CUSTOM_TOKEN : "+why)
	}
	switch {
	case claims["sub"] != signer:
		bad("iss and sub must both be the signing service account")
	case claims["aud"] != IdentityToolkitAudience:
		bad("wrong audience")
	case !okExp || !okIat:
		bad("exp and iat are required")
	case exp <= now.Unix():
		bad("the token has expired")
	case iat > now.Unix()+300:
		bad("iat is in the future")
	case exp-iat > 3600:
		bad("exp is more than one hour after iat")
	case uid == "" || len(uid) > 128:
		bad("uid must be 1 to 128 characters")
	case len(extraJSON) > 1000:
		bad("developer claims exceed 1000 bytes")
	default:
		for k := range extra {
			if reservedClaims[k] {
				bad("reserved claim " + k)
				return
			}
		}
		f.exchanges++
		f.users[uid] = &fbUser{claims: extra}
		f.nextID++
		rt := "rt-" + strconv.Itoa(f.nextID) + "-" + uid
		f.refresh[rt] = uid
		writeJSON(w, http.StatusOK, map[string]any{"kind": "identitytoolkit#VerifyCustomTokenResponse",
			"idToken": f.idToken(uid, extra, now), "refreshToken": rt, "expiresIn": strconv.Itoa(int(f.lifetime / time.Second)), "isNewUser": true})
	}
}

// idToken builds a signed ID token; the caller holds f.mu.
func (f *IdentityToolkit) idToken(uid string, extra map[string]any, now time.Time) string {
	p := map[string]any{"iss": "https://securetoken.google.com/" + f.project, "aud": f.project, "sub": uid, "user_id": uid,
		"auth_time": now.Unix(), "iat": now.Unix(), "exp": now.Add(f.lifetime).Unix(),
		"firebase": map[string]any{"identities": map[string]any{}, "sign_in_provider": "custom"}}
	for k, v := range extra {
		p[k] = v
	}
	raw, _ := json.Marshal(p)
	in := b64(`{"alg":"RS256","kid":"fake-id-key","typ":"JWT"}`) + "." + base64.RawURLEncoding.EncodeToString(raw)
	return in + "." + f.sign(in)
}

func (f *IdentityToolkit) refreshToken(w http.ResponseWriter, body []byte) {
	form, err := url.ParseQuery(string(body))
	if err != nil {
		writeFirebaseError(w, http.StatusBadRequest, "INVALID_REQUEST")
		return
	}
	if form.Get("grant_type") != "refresh_token" {
		writeFirebaseError(w, http.StatusBadRequest, "INVALID_GRANT_TYPE")
		return
	}
	rt := form.Get("refresh_token")
	if rt == "" {
		writeFirebaseError(w, http.StatusBadRequest, "MISSING_REFRESH_TOKEN")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	uid, ok := f.refresh[rt]
	if !ok {
		writeFirebaseError(w, http.StatusBadRequest, "INVALID_REFRESH_TOKEN")
		return
	}
	u := f.users[uid]
	switch {
	case u == nil:
		writeFirebaseError(w, http.StatusBadRequest, "USER_NOT_FOUND")
		return
	case u.disabled:
		writeFirebaseError(w, http.StatusBadRequest, "USER_DISABLED")
		return
	}
	f.refreshes++
	out := rt
	if f.rotate {
		f.nextID++
		out = fmt.Sprintf("rt-%d-%s", f.nextID, uid)
		f.refresh[out] = uid
	}
	id := f.idToken(uid, u.claims, f.now())
	writeJSON(w, http.StatusOK, map[string]any{"access_token": id, "expires_in": strconv.Itoa(int(f.lifetime / time.Second)),
		"token_type": "Bearer", "refresh_token": out, "id_token": id, "user_id": uid, "project_id": "123456789012"})
}
