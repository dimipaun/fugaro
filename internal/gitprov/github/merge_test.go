package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// The E8 calls against this host. What the tests pin is the binding: a merge
// carries the head the caller named and nothing else, every write is made with
// the token the gate minted rather than the provider's own installation token,
// and a mint that refuses (an App on the base's bypass list, E16) reaches the
// caller with no merge sent.

const (
	mergeHead  = "1111111111111111111111111111111111111111"
	mergeOther = "2222222222222222222222222222222222222222"
	mintToken  = "ghs_minted_merge_token_1234"
)

// testMint is the gitprov.MergeMinter a test installs: it records every call
// and hands back a token that is plainly not the provider's own.
type testMint struct {
	err   error
	asked []mintCall
}

type mintCall struct {
	repo string
	pr   int
	head string
}

func (m *testMint) MintMerge(_ context.Context, repo string, pr int, headSHA string) (string, time.Time, error) {
	m.asked = append(m.asked, mintCall{repo, pr, headSHA})
	if m.err != nil {
		return "", time.Time{}, m.err
	}
	return mintToken, time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC), nil
}

// pullJSON is pull request number as this adapter reads it, at sha, open.
func pullJSON(number int, title, sha string, draft bool) string {
	return pullStateJSON("open", false, number, title, sha, draft)
}

// pullStateJSON is the same answer for a pull request that is no longer open.
// This host reports one with state "closed" and a merged flag — the flag is
// what tells a merged pull request from a declined one (github.go:539) — and
// both are refused the same way by every write.
func pullStateJSON(state string, merged bool, number int, title, sha string, draft bool) string {
	return fmt.Sprintf(`{"number": %d, "html_url": "https://github.com/acme/web/pull/%d", "draft": %t, `+
		`"node_id": "PR_kwDOA", "title": %q, "body": "Adds search.", "state": %q, "merged": %t, `+
		`"head": {"ref": %q, "sha": %q}, "base": {"ref": "main"}}`,
		number, number, draft, title, state, merged, branchName, sha)
}

// TestGithubMergeSendsExpectedSHAAndNoBypass is E8's core assertion for this
// host (G2, G14): the merge body is the expected head and nothing else — no
// merge method of Fugaro's choosing, no commit message, and above all no admin
// or bypass flag — and the call is authenticated with the token the mint handed
// back, not with the provider's own installation token.
func TestGithubMergeSendsExpectedSHAAndNoBypass(t *testing.T) {
	var auth string
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/acme/web/pulls/12":
			io.WriteString(w, pullJSON(12, "Add search", mergeHead, false))
			return true
		case r.Method == "PUT" && r.URL.Path == "/repos/acme/web/pulls/12/merge":
			auth = r.Header.Get("Authorization")
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			var sent map[string]any
			if err := json.Unmarshal(body, &sent); err != nil {
				t.Errorf("the merge body is not JSON: %v (%s)", err, body)
			}
			if len(sent) != 1 || sent["sha"] != mergeHead {
				t.Errorf("the merge body is %v; want exactly the expected head and nothing else", sent)
			}
			if r.URL.RawQuery != "" {
				t.Errorf("the merge call carries the query %q", r.URL.RawQuery)
			}
			io.WriteString(w, `{"merged": true, "sha": "`+mergeHead+`"}`)
			return true
		}
		return false
	})
	m := &testMint{}
	p.UseMergeMint(m)
	if err := p.Merge(ctx, 12, mergeHead); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer "+mintToken {
		t.Errorf("the merge was made with %q; want the minted token", auth)
	}
	if len(m.asked) != 1 || m.asked[0] != (mintCall{repo: "acme/web", pr: 12, head: mergeHead}) {
		t.Errorf("the mint was asked for %+v; want one call for acme/web #12 at the expected head", m.asked)
	}
}

// TestGithubMergeRefusesWhenTheHeadMoved is G2 on the write itself: the pull
// request is at another commit than the call names, so nothing is merged. The
// handler serves no merge endpoint, and fakeAPI fails the test if one is asked
// for anyway.
//
// The mint here hands back a token without looking at the head, which is the
// one thing a real mint does not do (TestGithubWriteReportsTheMintsRefusal
// below is the path a stale head really takes): this test is the provider's own
// read, the second of the two, and it is the one that catches a push landing
// after the mint answered.
func TestGithubMergeRefusesWhenTheHeadMoved(t *testing.T) {
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/repos/acme/web/pulls/12" {
			io.WriteString(w, pullJSON(12, "Add search", mergeOther, false))
			return true
		}
		return false
	})
	p.UseMergeMint(&testMint{})
	err := p.Merge(ctx, 12, mergeHead)
	if !errors.Is(err, gitprov.ErrHeadMoved) {
		t.Fatalf("Merge at a moved head = %v, want ErrHeadMoved", err)
	}
}

// TestGithubMergeRefusedByGitHub covers the answer that is not an HTTP error: a
// 200 whose body says merged false. A caller that read only the status would
// report a merge that never happened.
func TestGithubMergeRefusedByGitHub(t *testing.T) {
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/acme/web/pulls/12":
			io.WriteString(w, pullJSON(12, "Add search", mergeHead, false))
			return true
		case r.Method == "PUT" && r.URL.Path == "/repos/acme/web/pulls/12/merge":
			io.WriteString(w, `{"merged": false, "message": "Pull Request is not mergeable"}`)
			return true
		}
		return false
	})
	p.UseMergeMint(&testMint{})
	err := p.Merge(ctx, 12, mergeHead)
	if err == nil || !strings.Contains(err.Error(), "not mergeable") {
		t.Fatalf("Merge = %v; want the host's own refusal", err)
	}
}

// TestMergeRefusesWhenActorOnBypassList is G14's refusal as this adapter sees
// it: the mint is where the bypass list is read (E16 reads both the rulesets
// covering the base and its classic protection), it refuses when the App may
// bypass either, and that refusal reaches the caller of the merge with no merge
// sent at all. The provider is opened with no pull request handler, so a call
// that got past the mint would fail here rather than merge.
func TestMergeRefusesWhenActorOnBypassList(t *testing.T) {
	errBypass := errors.New("the App may bypass the base's rules")
	p := fakeAPI(t, func(http.ResponseWriter, *http.Request) bool { return false })
	p.UseMergeMint(&testMint{err: errBypass})
	err := p.Merge(ctx, 12, mergeHead)
	if !errors.Is(err, errBypass) {
		t.Fatalf("Merge = %v; want the mint's refusal", err)
	}
	if !strings.Contains(err.Error(), "#12") {
		t.Errorf("the refusal does not name the pull request: %v", err)
	}
}

// TestGithubWriteReportsTheMintsRefusal pins the order the two head reads run
// in, because it decides which error a caller sees. A head that was already
// stale when the call began is refused by the mint, before this provider reads
// anything: the refusal it reports is the mint's own error rather than
// ErrHeadMoved (the service answers every refusal of its route with one code,
// so the client cannot tell a moved head from another refusal — ErrHeadMoved's
// doc in internal/gitprov/merge.go says so). The provider adds nothing to it
// and writes nothing, and a real mergeMinter is what this refusal comes from in
// production: internal/creds.ServiceMergeMint wraps the service's answer in
// creds.ErrMergeMint.
func TestGithubWriteReportsTheMintsRefusal(t *testing.T) {
	errMoved := errors.New("the merge mint refused: the pull request's head is not the commit the call named")
	p := fakeAPI(t, func(http.ResponseWriter, *http.Request) bool { return false })
	p.UseMergeMint(&testMint{err: errMoved})
	for _, c := range []struct {
		name string
		call func() error
	}{
		{"Merge", func() error { return p.Merge(ctx, 12, mergeHead) }},
		{"MarkReady", func() error { return p.MarkReady(ctx, 12, mergeHead) }},
	} {
		err := c.call()
		if !errors.Is(err, errMoved) {
			t.Errorf("%s = %v; want the mint's refusal, which is what a stale head produces", c.name, err)
		}
		if errors.Is(err, gitprov.ErrHeadMoved) {
			t.Errorf("%s = %v; the provider cannot know the mint refused over the head, so it must not claim it did", c.name, err)
		}
	}
}

// TestGithubWriteWithoutAMintRefuses pins the whole point of the mint: a
// provider that was never given one writes nothing. There is no fallback to the
// installation token every other call uses, because that fallback would be a
// merge outside the gate.
func TestGithubWriteWithoutAMintRefuses(t *testing.T) {
	p := fakeAPI(t, func(http.ResponseWriter, *http.Request) bool { return false })
	if err := p.Merge(ctx, 12, mergeHead); !errors.Is(err, gitprov.ErrNoMergeMint) {
		t.Errorf("Merge = %v, want ErrNoMergeMint", err)
	}
	if err := p.MarkReady(ctx, 12, mergeHead); !errors.Is(err, gitprov.ErrNoMergeMint) {
		t.Errorf("MarkReady = %v, want ErrNoMergeMint", err)
	}
}

// TestGithubMarkReadyRefusesOnHeadChange is MarkReady's half of G2: a draft is
// readied only at the head the gate read. The handler serves no GraphQL, so a
// write would fail the test.
func TestGithubMarkReadyRefusesOnHeadChange(t *testing.T) {
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/repos/acme/web/pulls/12" {
			io.WriteString(w, pullJSON(12, "Add search", mergeOther, true))
			return true
		}
		return false
	})
	p.UseMergeMint(&testMint{})
	err := p.MarkReady(ctx, 12, mergeHead)
	if !errors.Is(err, gitprov.ErrHeadMoved) {
		t.Fatalf("MarkReady at a moved head = %v, want ErrHeadMoved", err)
	}
}

// TestGithubMarkReadyUndrafts is E8's MarkReady for this host (F24): a draft at
// the head the caller named is readied through GitHub's own mutation, with the
// minted token, and it is not ApplyReady — no reviewers, no labels, and no
// title change for a pull request that carries no draft prefix.
func TestGithubMarkReadyUndrafts(t *testing.T) {
	var gql struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	var auth string
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/acme/web/pulls/12":
			io.WriteString(w, pullJSON(12, "Add search", mergeHead, true))
			return true
		case r.Method == "POST" && r.URL.Path == "/graphql":
			auth = r.Header.Get("Authorization")
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &gql); err != nil {
				t.Errorf("the GraphQL body is not JSON: %v", err)
			}
			io.WriteString(w, `{"data": {"markPullRequestReadyForReview": {"pullRequest": {"isDraft": false}}}}`)
			return true
		}
		return false
	})
	p.UseMergeMint(&testMint{})
	if err := p.MarkReady(ctx, 12, mergeHead); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gql.Query, "markPullRequestReadyForReview") || gql.Variables["id"] != "PR_kwDOA" {
		t.Fatalf("the ready call sent %+v", gql)
	}
	if auth != "Bearer "+mintToken {
		t.Errorf("the ready call was made with %q; want the minted token", auth)
	}
}

// TestGithubMarkReadyOnAReadyPRWritesNothing: MarkReady is idempotent, and a
// pull request that is already ready sends no write at all.
func TestGithubMarkReadyOnAReadyPRWritesNothing(t *testing.T) {
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "GET" && r.URL.Path == "/repos/acme/web/pulls/12" {
			io.WriteString(w, pullJSON(12, "Add search", mergeHead, false))
			return true
		}
		return false
	})
	p.UseMergeMint(&testMint{})
	if err := p.MarkReady(ctx, 12, mergeHead); err != nil {
		t.Fatal(err)
	}
}

// TestGithubHeadBoundCallRefusesAClosedOrMergedPR keeps these two writes on the
// same footing as every other write this provider makes: UpdatePR (github.go),
// ApplyReady and Bitbucket's MarkReady all refuse a pull request that is not
// open, and the fake does too, so a production path that wrote one anyway would
// be the one adapter out of step — and the tests E10b writes against the double
// would certify a path production handles differently.
//
// The head is the same commit the call names in every case, and that is the
// point: a closed pull request keeps its head, so the head check passes and
// nothing but the state stands between this call and the write. The handler
// serves the read and no write at all, so a call that got past the refusal
// fails the test rather than landing.
func TestGithubHeadBoundCallRefusesAClosedOrMergedPR(t *testing.T) {
	for _, c := range []struct {
		name string
		call func(p *Provider) error
	}{
		{"Merge", func(p *Provider) error { return p.Merge(ctx, 12, mergeHead) }},
		{"MarkReady", func(p *Provider) error { return p.MarkReady(ctx, 12, mergeHead) }},
	} {
		for _, s := range []struct {
			state  string
			merged bool
			want   string
		}{
			{"closed", true, "merged"},
			{"closed", false, "closed"},
		} {
			p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == "GET" && r.URL.Path == "/repos/acme/web/pulls/12" {
					io.WriteString(w, pullStateJSON(s.state, s.merged, 12, "Add search", mergeHead, true))
					return true
				}
				return false
			})
			p.UseMergeMint(&testMint{})
			err := c.call(p)
			if !errors.Is(err, gitprov.ErrPRNotOpen) {
				t.Errorf("%s on a %s pull request = %v, want ErrPRNotOpen", c.name, s.want, err)
				continue
			}
			if !strings.Contains(err.Error(), s.want) {
				t.Errorf("%s on a %s pull request = %v; the refusal does not say it is %s", c.name, s.want, err, s.want)
			}
		}
	}
}

// TestGithubIsNotExemptFromTheMergeMint is the other half of Bitbucket's
// exemption: this is the host whose App can sit on a base's bypass list (F26),
// so its ready and merge calls take the mint and nothing else. It has to say
// that by NOT claiming the exemption — a gate that found no answer either way
// would have to guess, and the guess that lets a merge through is the one that
// matters.
func TestGithubIsNotExemptFromTheMergeMint(t *testing.T) {
	var prov gitprov.Provider = fakeAPI(t, func(http.ResponseWriter, *http.Request) bool { return false })
	if exempt, ok := prov.(gitprov.MintExempt); ok {
		t.Fatalf("the GitHub provider claims exemption from the merge mint: %v", exempt.MergeMintExemption())
	}
	if _, ok := prov.(gitprov.MergeMintSetter); !ok {
		t.Fatal("the GitHub provider does not take a merge mint at all")
	}
}
