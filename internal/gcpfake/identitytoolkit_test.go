package gcpfake

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func post(t *testing.T, u, ct, auth string, body []byte) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

func sign(t *testing.T, f *IAMCredentials, signer string, claims map[string]any) string {
	t.Helper()
	p, _ := json.Marshal(claims)
	body, _ := json.Marshal(map[string]string{"payload": string(p)})
	code, m := post(t, f.URL+"/v1/projects/-/serviceAccounts/"+url.PathEscape(signer)+":signJwt", "application/json", "Bearer x", body)
	if code != 200 {
		t.Fatalf("signJwt %d %v", code, m)
	}
	return m["signedJwt"].(string)
}

func TestIAMCredentialsFake(t *testing.T) {
	f := NewIAMCredentials(t)
	f.AddSigner("s@p.iam.gserviceaccount.com")
	path := f.URL + "/v1/projects/-/serviceAccounts/s@p.iam.gserviceaccount.com:signJwt"
	body := []byte(`{"payload":"{\"exp\":1}"}`)
	if code, _ := post(t, path, "application/json", "", body); code != 401 {
		t.Fatalf("no credential: %d", code)
	}
	if code, _ := post(t, path, "application/json", "Bearer x", []byte(`{"payload":"nope"}`)); code != 400 {
		t.Fatalf("non-JSON payload: %d", code)
	}
	if code, _ := post(t, path, "application/json", "Bearer x", []byte(`{"payload":"{}"}`)); code != 400 {
		t.Fatalf("no exp: %d", code)
	}
	if code, _ := post(t, f.URL+"/v1/projects/-/serviceAccounts/nobody@p.iam.gserviceaccount.com:signJwt", "application/json", "Bearer x", body); code != 404 {
		t.Fatalf("unknown signer: %d", code)
	}
	jwt := sign(t, f, "s@p.iam.gserviceaccount.com", map[string]any{"iss": "s@p.iam.gserviceaccount.com", "exp": 1})
	if _, signer, err := f.Verify(jwt); err != nil || signer != "s@p.iam.gserviceaccount.com" {
		t.Fatalf("verify: %v", err)
	}
	parts := strings.Split(jwt, ".")
	if _, _, err := f.Verify(parts[0] + "." + parts[1] + ".AAAA"); err == nil {
		t.Fatal("a bad signature verified")
	}
	// Only the unknown signer and the good call got as far as signing.
	if n := len(f.SignCalls()); n != 2 {
		t.Fatalf("signing calls = %d, want 2", n)
	}
}

func TestIdentityToolkitFakeRefusesWhatGoogleRefuses(t *testing.T) {
	iam := NewIAMCredentials(t)
	const signer = "s@p.iam.gserviceaccount.com"
	iam.AddSigner(signer)
	itk := NewIdentityToolkit(t, iam, "KEY", "proj")
	now := time.Now()
	itk.SetClock(func() time.Time { return now })
	good := func() map[string]any {
		return map[string]any{"iss": signer, "sub": signer, "aud": IdentityToolkitAudience, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
			"uid": "r~a~b", "claims": map[string]any{"fs": "a"}}
	}
	exchange := func(key string, claims map[string]any) (int, map[string]any) {
		jwt := sign(t, iam, signer, claims)
		b, _ := json.Marshal(map[string]any{"token": jwt, "returnSecureToken": true})
		return post(t, itk.URL+"/v1/accounts:signInWithCustomToken?key="+key, "application/json", "", b)
	}
	if code, _ := exchange("KEY", good()); code != 200 {
		t.Fatalf("good token: %d", code)
	}
	if code, _ := exchange("WRONG", good()); code != 400 {
		t.Fatalf("wrong key: %d", code)
	}
	for name, mut := range map[string]func(map[string]any){
		"aud":        func(c map[string]any) { c["aud"] = "other" },
		"sub":        func(c map[string]any) { c["sub"] = "x@p.iam.gserviceaccount.com" },
		"expired":    func(c map[string]any) { c["exp"] = now.Add(-time.Minute).Unix() },
		"2h":         func(c map[string]any) { c["exp"] = now.Add(2 * time.Hour).Unix() },
		"no uid":     func(c map[string]any) { delete(c, "uid") },
		"long uid":   func(c map[string]any) { c["uid"] = strings.Repeat("u", 129) },
		"reserved":   func(c map[string]any) { c["claims"] = map[string]any{"exp": 1} },
		"big claims": func(c map[string]any) { c["claims"] = map[string]any{"fs": strings.Repeat("x", 1001)} },
	} {
		c := good()
		mut(c)
		if code, _ := exchange("KEY", c); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	// A token signed by an untrusted account never verifies.
	forged := "eyJhbGciOiJSUzI1NiJ9." + strings.Split(sign(t, iam, signer, good()), ".")[1] + ".AAAA"
	b, _ := json.Marshal(map[string]any{"token": forged})
	if code, _ := post(t, itk.URL+"/v1/accounts:signInWithCustomToken?key=KEY", "application/json", "", b); code != 400 {
		t.Fatalf("forged: %d", code)
	}
	if itk.Exchanges() != 1 {
		t.Fatalf("exchanges = %d", itk.Exchanges())
	}
}

func TestIdentityToolkitRefresh(t *testing.T) {
	iam := NewIAMCredentials(t)
	const signer = "s@p.iam.gserviceaccount.com"
	iam.AddSigner(signer)
	itk := NewIdentityToolkit(t, iam, "KEY", "proj")
	now := time.Now()
	itk.SetClock(func() time.Time { return now })
	jwt := sign(t, iam, signer, map[string]any{"iss": signer, "sub": signer, "aud": IdentityToolkitAudience, "iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(), "uid": "r~a~b", "claims": map[string]any{"fs": "a", "fx": 1700000000000}})
	b, _ := json.Marshal(map[string]any{"token": jwt})
	_, m := post(t, itk.URL+"/v1/accounts:signInWithCustomToken?key=KEY", "application/json", "", b)
	rt := m["refreshToken"].(string)
	form := func(v url.Values) (int, map[string]any) {
		return post(t, itk.URL+"/v1/token?key=KEY", "application/x-www-form-urlencoded", "", []byte(v.Encode()))
	}
	if code, m := form(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}); code != 200 {
		t.Fatalf("refresh: %d %v", code, m)
	} else if cl, err := itk.IDTokenClaims(m["id_token"].(string)); err != nil || cl["fs"] != "a" || cl["user_id"] != "r~a~b" {
		t.Fatalf("claims %v %v", cl, err)
	}
	for name, v := range map[string]url.Values{
		"grant":   {"grant_type": {"password"}, "refresh_token": {rt}},
		"missing": {"grant_type": {"refresh_token"}},
		"unknown": {"grant_type": {"refresh_token"}, "refresh_token": {"nope"}},
	} {
		if code, _ := form(v); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	itk.DisableUser("r~a~b")
	if code, m := form(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}); code != 400 || !strings.Contains(m["error"].(map[string]any)["message"].(string), "USER_DISABLED") {
		t.Fatalf("disabled: %d %v", code, m)
	}
	if _, err := itk.IDTokenClaims("a.b.c"); err == nil {
		t.Fatal("garbage verified")
	}
}

func TestIdentityToolkitAdminListAndDelete(t *testing.T) {
	f := NewIdentityToolkit(t, NewIAMCredentials(t), "key", "aurora-fp")
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, u := range []string{"r~a~1", "r~a~2", "r~a~3"} {
		f.AddUser(u, old)
	}
	get := func(q string) map[string]any {
		resp, err := http.Get(f.URL + "/v1/projects/aurora-fp/accounts:batchGet" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return m
	}
	p1 := get("?maxResults=2")
	users := p1["users"].([]any)
	if len(users) != 2 || p1["nextPageToken"] == nil || users[0].(map[string]any)["createdAt"] != strconv.FormatInt(old.UnixMilli(), 10) {
		t.Fatalf("page 1 = %v", p1)
	}
	if p2 := get("?maxResults=2&nextPageToken=" + p1["nextPageToken"].(string)); len(p2["users"].([]any)) != 1 || p2["nextPageToken"] != nil {
		t.Fatalf("page 2 = %v", p2)
	}
	if code, _ := post(t, f.URL+"/v1/projects/aurora-fp/accounts:batchDelete", "application/json", "Bearer x", []byte(`{"localIds":["r~a~1"]}`)); code != 400 {
		t.Fatalf("delete without force = %d", code)
	}
	if code, _ := post(t, f.URL+"/v1/projects/aurora-fp/accounts:batchDelete", "application/json", "Bearer x", []byte(`{"localIds":["r~a~1","r~a~2"],"force":true}`)); code != 200 {
		t.Fatalf("delete = %d", code)
	}
	if got := f.Users(); len(got) != 1 || got[0] != "r~a~3" {
		t.Fatalf("users = %v", got)
	}
	if code, _ := post(t, f.URL+"/v1/projects/other-fp/accounts:batchDelete", "application/json", "Bearer x", []byte(`{"localIds":["r~a~3"],"force":true}`)); code != 403 {
		t.Fatalf("another project = %d", code)
	}
}
