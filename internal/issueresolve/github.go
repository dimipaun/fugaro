// Package issueresolve asks GitHub what became of the issues a project
// submitted, one issue per request, so `fugaro update` can list the ones
// the release it installed fixes and mark them resolved.
//
// Everything here is best effort against a source the machine does not
// control: the response is untrusted (bounded, whitelisted, decoded into a
// handful of fields, redirects refused) and no text it carries - title,
// body, comment - is ever returned, let alone printed; the summary a user
// sees is the local record's. A number that is a pull request, a close
// reason this reader does not know, or a milestone that is not a release
// version is reported as no verdict, never guessed at.
package issueresolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// DefaultBaseURL is GitHub's API root. Every request URL is built from it,
// from updatecheck.Repo and from the issue number; no stored value ever
// reaches it.
const DefaultBaseURL = "https://api.github.com"

// FetchTimeout bounds one request: three seconds is far past what a single
// small issue needs, and a pass of twenty asks for less.
const FetchTimeout = 3 * time.Second

// maxBody bounds a response before it is decoded. A real issue is about
// 10 KB; this leaves room for a long discussion attached to one.
const maxBody = 256 << 10

// ErrRateLimited marks a 403 or 429 answer: GitHub's anonymous limit for
// this machine's address is spent, so every later request in the pass would
// fail the same way and the pass must stop.
var ErrRateLimited = errors.New("GitHub rate limit reached")

// ErrTransport marks an answer that could not be used at all: no route to
// GitHub, a status this reader does not expect, a redirect it refused, a
// body past its bound or one that does not decode. The pass stops on the
// first one, since a machine that cannot reach GitHub will not reach it
// for the next issue either.
var ErrTransport = errors.New("GitHub unreachable")

// Client is the minimal HTTP surface Fetch needs, so a test can inject a
// fake; *http.Client satisfies it.
type Client interface {
	Do(req *http.Request) (*http.Response, error)
}

// State is what GitHub shows about one issue, as Fetch reports it: exactly
// one of Open, ClosedCompleted, ClosedNotPlanned, ClosedDuplicate or
// NoVerdict. The unexported method keeps that set closed.
type State interface{ state() }

// Open is an issue GitHub still shows open: nothing has changed for it.
type Open struct{}

// ClosedCompleted is an issue closed as completed. Milestone is the
// milestone's title as a canonical release version (the fix's release);
// an issue closed as completed without a usable one is NoVerdict instead,
// so a Milestone here is always a strict X.Y.Z.
type ClosedCompleted struct{ Milestone string }

// ClosedNotPlanned is an issue closed as not planned: it will not be fixed.
type ClosedNotPlanned struct{}

// ClosedDuplicate is an issue closed as a duplicate of another one.
type ClosedDuplicate struct{}

// NoVerdict is an answer that decides nothing: a pull request (the same
// endpoint serves them), a close reason this reader does not know, or a
// completed close whose milestone is not a release version. The issue is
// left exactly as it was.
type NoVerdict struct{}

func (Open) state()             {}
func (ClosedCompleted) state()  {}
func (ClosedNotPlanned) state() {}
func (ClosedDuplicate) state()  {}
func (NoVerdict) state()        {}

// defaultClient never follows a redirect. GitHub's API issues none for
// this endpoint, so any redirect is a misconfiguration or a hostile
// responder, and following one would GET whatever host it names next, with
// a Referer header naming the first.
var defaultClient Client = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Reader reads single issues from one GitHub API root. It is safe for
// sequential use by one pass; the pass is the only caller.
type Reader struct {
	client  Client
	baseURL string
}

// NewReader returns a Reader over baseURL (DefaultBaseURL in production,
// a test server in tests), asking through client; a nil client means the
// default one, which refuses redirects.
func NewReader(client Client, baseURL string) *Reader {
	if client == nil {
		client = defaultClient
	}
	return &Reader{client: client, baseURL: strings.TrimSuffix(baseURL, "/")}
}

// issueAnswer is the subset of GitHub's issue object this reader reads.
// The many other fields of the answer are ignored on purpose: decoding
// strictly would refuse every real response. Nothing read here is ever
// printed - the callers print the record's own summary, not GitHub's text.
type issueAnswer struct {
	State       string          `json:"state"`
	StateReason string          `json:"state_reason"`
	PullRequest json.RawMessage `json:"pull_request"`
	Milestone   *struct {
		Title string `json:"title"`
	} `json:"milestone"`
}

// verdict maps a decoded answer to a State, deciding nothing GitHub did not
// say: an unknown state or close reason is no verdict.
func verdict(a issueAnswer) State {
	if isPullRequest(a.PullRequest) {
		return NoVerdict{}
	}
	switch a.State {
	case "open":
		// GitHub sends no reason for an open issue (a reopened one carries
		// "reopened"); anything else is an answer talking past itself, and
		// no verdict is the honest report of it.
		if a.StateReason != "" {
			return NoVerdict{}
		}
		return Open{}
	case "closed":
		switch a.StateReason {
		case "completed":
			if a.Milestone == nil {
				return NoVerdict{}
			}
			release, ok := updatecheck.CanonicalRelease(a.Milestone.Title)
			if !ok {
				return NoVerdict{}
			}
			return ClosedCompleted{Milestone: release}
		case "not_planned":
			return ClosedNotPlanned{}
		case "duplicate":
			return ClosedDuplicate{}
		}
	}
	return NoVerdict{}
}

// isPullRequest reports whether the answer carries the pull_request field
// the issues endpoint adds for a pull request.
func isPullRequest(raw json.RawMessage) bool {
	return len(raw) != 0 && string(raw) != "null"
}

// Fetch asks GitHub about issue number and reports what it shows. number
// must be positive: it is the only thing that decides the URL, and a number
// that is not one (zero, negative) is refused before a request is made.
// The request is bounded by FetchTimeout, its answer by maxBody, and the
// error is ErrRateLimited or ErrTransport - never an untyped error and
// never a state the reader guessed.
//
// A number that is not positive is the one refusal that is neither: it is
// a caller's bug, not an unreachable GitHub (ErrTransport) and not an
// exhausted limit (ErrRateLimited), and a caller that stops its pass on
// those must not stop on this.
func (r *Reader) Fetch(ctx context.Context, number int) (State, error) {
	if number < 1 {
		return NoVerdict{}, fmt.Errorf("refusing issue number %d", number)
	}
	url := r.baseURL + "/repos/" + updatecheck.Repo + "/issues/" + strconv.Itoa(number)

	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return NoVerdict{}, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "fugaro-issue-check")

	resp, err := r.client.Do(req)
	if err != nil {
		return NoVerdict{}, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	defer resp.Body.Close()

	// A response that came from anywhere but the URL asked for is refused
	// even when a caller injected a client that follows redirects.
	if resp.Request != nil && resp.Request.URL != nil && resp.Request.URL.String() != url {
		return NoVerdict{}, fmt.Errorf("%w: the answer did not come from %s", ErrTransport, url)
	}
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusTooManyRequests:
		return NoVerdict{}, fmt.Errorf("%w: GitHub answered %d", ErrRateLimited, resp.StatusCode)
	case http.StatusOK:
	case http.StatusNotFound:
		// The issue was deleted or transferred since it was submitted.
		// GitHub answered, and the answer is that there is no such issue:
		// nothing to decide, and emphatically not an unreachable GitHub,
		// which would end the pass before it reached the issues behind this
		// one (the case the cursor exists for).
		return NoVerdict{}, nil
	default:
		// The reason phrase is the responder's text and is not repeated.
		return NoVerdict{}, fmt.Errorf("%w: GitHub answered %d", ErrTransport, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return NoVerdict{}, fmt.Errorf("%w: the answer is not within %d bytes", ErrTransport, maxBody)
	}
	var a issueAnswer
	if err := json.Unmarshal(data, &a); err != nil {
		return NoVerdict{}, fmt.Errorf("%w: the answer does not decode", ErrTransport)
	}
	return verdict(a), nil
}
