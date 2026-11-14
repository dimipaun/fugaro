package buildtoken

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// jsonKeys is the encoded shape's own field names, so a rename that either side
// made on its own is a failure here rather than a field the other side reads as
// absent.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encoding %T: %v", v, err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decoding %T: %v", v, err)
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// TestBuildTokenStubShape: C2 is a contract and nothing else, so what it has to
// hold is that the stub returns the declared shape — either of the two answers,
// unchanged — and reaches nothing to do it. Both answers are checked, because
// which one a build gets is the service's decision (R6) and the caller has to
// read either. The two sides of this contract are different build steps, so the
// wire names are part of the shape: a field renamed on one side alone is a
// change to this contract, not a change to that side's own code.
func TestBuildTokenStubShape(t *testing.T) {
	req := Request{
		Entry:        "app",
		BuildID:      "8f3c1a2b-0000-4000-8000-000000000001",
		Provider:     gitprov.KindGitHub,
		Repositories: []string{"acme/app", "acme/lib"},
		TTLSeconds:   900,
	}
	token := Response{Token: &Token{
		Value:     "fixture-build-token",
		User:      "x-access-token",
		Host:      "github.com",
		ExpiresAt: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
	}}
	got, err := Stub{Answer: token}.Mint(context.Background(), req)
	if err != nil {
		t.Fatalf("the stub refused the request: %v", err)
	}
	if !reflect.DeepEqual(got, token) {
		t.Errorf("the stub answered %+v, want the token it was made with", got)
	}
	grant := Response{Proxy: &ProxyGrant{
		URL:          "https://creds.example/git/",
		Repositories: []string{"acme/app", "acme/lib"},
		ExpiresAt:    time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
	}}
	got, err = Stub{Answer: grant}.Mint(context.Background(), req)
	if err != nil {
		t.Fatalf("the stub refused the request: %v", err)
	}
	if !reflect.DeepEqual(got, grant) {
		t.Errorf("the stub answered %+v, want the proxy grant it was made with", got)
	}
	var client Client = Stub{}
	if _, err := client.Mint(context.Background(), Request{}); err != nil {
		t.Errorf("a zero stub refused: %v", err)
	}
	if want := []string{"build_id", "entry", "provider", "repositories", "ttl_seconds"}; !reflect.DeepEqual(jsonKeys(t, req), want) {
		t.Errorf("the request encodes as %v, want %v", jsonKeys(t, req), want)
	}
	if want := []string{"expires_at", "host", "token", "user"}; !reflect.DeepEqual(jsonKeys(t, token.Token), want) {
		t.Errorf("a token encodes as %v, want %v", jsonKeys(t, token.Token), want)
	}
	if want := []string{"expires_at", "repositories", "url"}; !reflect.DeepEqual(jsonKeys(t, grant.Proxy), want) {
		t.Errorf("a proxy grant encodes as %v, want %v", jsonKeys(t, grant.Proxy), want)
	}
	if want := []string{"token"}; !reflect.DeepEqual(jsonKeys(t, token), want) {
		t.Errorf("a token answer encodes as %v, want %v: the two answers are exclusive", jsonKeys(t, token), want)
	}
	if want := []string{"proxy"}; !reflect.DeepEqual(jsonKeys(t, grant), want) {
		t.Errorf("a proxy grant answer encodes as %v, want %v: the two answers are exclusive", jsonKeys(t, grant), want)
	}
}
