package budget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// AuthUser is one Firebase Authentication user: a run's identity, which the
// launcher's custom token creates at the run's first sign-in.
type AuthUser struct {
	UID     string
	Created time.Time
}

// AuthAdmin is the Identity Toolkit admin API of one Firebase project
// (accounts:batchGet and accounts:batchDelete), called with HTTP, an
// authenticated client holding firebaseauth.admin on that project. The history
// job's account is the only caller.
type AuthAdmin struct {
	// Endpoint is the API root (https://identitytoolkit.googleapis.com), or a
	// loopback fake's.
	Endpoint string
	// Project is the Firebase project ID.
	Project string
	HTTP    *http.Client
}

const (
	authPageSize    = 1000
	authDeleteBatch = 1000
	authMaxPages    = 1000
	authBodyLimit   = 8 << 20
)

func (a *AuthAdmin) url(op string, q url.Values) (string, error) {
	if a.Project == "" || strings.ContainsAny(a.Project, "/?#% ") {
		return "", fmt.Errorf("auth admin: %q is not a Firebase project id", a.Project)
	}
	u := strings.TrimRight(a.Endpoint, "/") + "/v1/projects/" + a.Project + "/accounts:" + op
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u, nil
}

func (a *AuthAdmin) do(ctx context.Context, method, u string, body []byte) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		rd = strings.NewReader(string(body))
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth admin: %s %s: %w", method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, authBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("auth admin: %s %s: %w", method, req.URL.Path, err)
	}
	if resp.StatusCode/100 != 2 {
		msg := string(b)
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return nil, fmt.Errorf("auth admin: %s %s: HTTP %d: %s", method, req.URL.Path, resp.StatusCode, strings.TrimSpace(msg))
	}
	return b, nil
}

// ListUsers returns every user of the project, following the pages.
func (a *AuthAdmin) ListUsers(ctx context.Context) ([]AuthUser, error) {
	var out []AuthUser
	token := ""
	for range authMaxPages {
		q := url.Values{"maxResults": {strconv.Itoa(authPageSize)}}
		if token != "" {
			q.Set("nextPageToken", token)
		}
		u, err := a.url("batchGet", q)
		if err != nil {
			return nil, err
		}
		b, err := a.do(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Users []struct {
				LocalID   string `json:"localId"`
				CreatedAt string `json:"createdAt"`
			} `json:"users"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(b, &page); err != nil {
			return nil, fmt.Errorf("auth admin: unreadable user list: %w", err)
		}
		for _, p := range page.Users {
			ms, err := strconv.ParseInt(p.CreatedAt, 10, 64)
			if p.LocalID == "" || err != nil || ms <= 0 {
				// No creation time, no deletion: age is the only evidence.
				continue
			}
			out = append(out, AuthUser{UID: p.LocalID, Created: time.UnixMilli(ms)})
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		token = page.NextPageToken
	}
	return nil, errors.New("auth admin: the user list did not end")
}

// UserFailure is a user the Identity Toolkit reported it did not delete.
type UserFailure struct{ UID, Message string }

// DeleteUsers deletes the users in batches and returns the users the API
// answered it could not delete (a 200 can carry per-user errors). It stops at
// the first failed request.
func (a *AuthAdmin) DeleteUsers(ctx context.Context, uids []string) ([]UserFailure, error) {
	var failed []UserFailure
	for len(uids) > 0 {
		n := min(len(uids), authDeleteBatch)
		batch := uids[:n]
		body, _ := json.Marshal(map[string]any{"localIds": batch, "force": true})
		u, err := a.url("batchDelete", nil)
		if err != nil {
			return failed, err
		}
		resp, err := a.do(ctx, http.MethodPost, u, body)
		if err != nil {
			return failed, err
		}
		var res struct {
			Errors []struct {
				Index   int    `json:"index"`
				LocalID string `json:"localId"`
				Message string `json:"message"`
			} `json:"errors"`
		}
		if json.Unmarshal(resp, &res) == nil {
			for _, e := range res.Errors {
				uid := e.LocalID
				if uid == "" && e.Index >= 0 && e.Index < len(batch) {
					uid = batch[e.Index]
				}
				failed = append(failed, UserFailure{UID: uid, Message: e.Message})
			}
		}
		uids = uids[n:]
	}
	return failed, nil
}
