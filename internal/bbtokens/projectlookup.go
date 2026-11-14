package bbtokens

// The project resolver (T5, R2): which project a repository belongs to, asked
// of the candidate project token itself.
//
// Bitbucket has no endpoint that lists what an access token reaches (F7), so a
// project token's coverage is knowable in one way only: asking the repository
// API with THAT token. GET /2.0/repositories/{workspace}/{repo} answers the
// repository's project, a 403 or a 404 says this token cannot see the
// repository, and nothing else about the token's reach can be read off an
// endpoint.
//
// Four things are the whole of its design.
//
//   - The lookup spends the candidate project token and no other kind (R2).
//     A workspace token would put a WIDER credential on a narrower question,
//     and a repository token would never be left to ask — the selector ranks it
//     before any project token. A record of another kind, an unknown name, a
//     record the bundle holds no value for, and a record that states no project
//     UUID are all answered "not covered" without a call: an empty project UUID
//     never matches a record, and a question this lookup cannot ask must never
//     spend a token to ask it.
//   - One repository's answer is held in this instance's memory for
//     ProjectLookupTTL (O1). The answer is a fact about the REPOSITORY — a
//     token that can read it belongs to that project, and a token of another
//     project is refused by the API — so one project token's positive answer
//     serves the next asker of the same repository without a second call. A
//     stale entry costs one refused call, never a wider grant: Decide has
//     admitted the repository before any of this is asked, and the caller
//     compares the answer with the record's own UUID.
//   - A 403 or a 404 is an answer about the TOKEN that asked and not about the
//     repository, so it is never held: the entry is evicted and the next call
//     asks again. A caller that learns the same thing from its own proxied call
//     (the proxy's 403/404, T9) evicts through Evict for that reason.
//   - The upstream is Bitbucket's own constant API root and nothing else (F22,
//     G10): no option, no environment value and no record can name a host, and
//     a redirect is not followed, so a token is never presented to a host the
//     adapter's constant does not name.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
)

const (
	// ProjectLookupTTL is how long one repository's project answer is served
	// from this instance's memory before bitbucket.org is asked again (O1, the
	// owner's ten minutes). It is short enough that a repository moved between
	// projects is re-read within one working session and long enough that a
	// run's many calls to one repository ask once.
	ProjectLookupTTL = 10 * time.Minute

	// projectLookupTimeout bounds one lookup. A lookup is a short call — one
	// request, one small document, no streaming — and it is made on the
	// proxy's request path, so it must not inherit the hour-long bound a push
	// is given (credsvc's gitUpstreamTimeout).
	projectLookupTimeout = 15 * time.Second

	// maxProjectAnswerBytes bounds the repository document this lookup reads. A
	// repository document is a few kilobytes; this is the bound that keeps an
	// answer that is not one from being held whole.
	maxProjectAnswerBytes = 64 << 10

	// maxProjectCacheEntries bounds the cache. One proxy instance serves one
	// project's repositories, so a few hundred entries cover every repository a
	// TTL window can hold; the bound is what keeps a stream of distinct
	// canonical repositories from growing the map without end.
	maxProjectCacheEntries = 256
)

// ProjectLookup is the contract's ProjectResolver over a node's records and the
// bundle's entries.
type ProjectLookup struct {
	records []Record
	tokens  BundleTokens
	client  *http.Client
	now     func() time.Time

	mu      sync.Mutex
	entries map[string]lookupEntry
}

// lookupEntry is one repository's held project answer and when it stops being
// served.
type lookupEntry struct {
	uuid    string
	expires time.Time
}

// lookupAnswer is the part of Bitbucket's repository document this lookup
// reads: the project the repository belongs to. The project's KEY is not read
// here — a record matches by UUID (R2), and the key a record also carries is
// the operator's label for it, resolved once when the token is added (T11).
type lookupAnswer struct {
	Project struct {
		UUID string `json:"uuid"`
	} `json:"project"`
}

var _ ProjectResolver = (*ProjectLookup)(nil)

// NewProjectLookup answers repository-to-project questions from records and the
// bundle's entries. client is the caller's HTTP client; nil means
// http.DefaultClient.
//
// The client is copied, so the redirect rule the lookup needs is its own and
// the caller's client keeps whatever it had: this service follows no redirect
// anywhere (G10), and a lookup that followed one would present a token to a
// host the API root does not name.
func NewProjectLookup(records []Record, tokens BundleTokens, client *http.Client) *ProjectLookup {
	if client == nil {
		client = http.DefaultClient
	}
	c := *client
	c.Jar = nil
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ProjectLookup{
		records: records,
		tokens:  tokens,
		client:  &c,
		now:     time.Now,
		entries: map[string]lookupEntry{},
	}
}

// ProjectOf answers the project UUID of the repository canonical names, as the
// candidate token tok sees it.
//
// "" is the answer "this token does not cover the repository", and it is the
// answer for every case in which the question cannot be put or is refused: the
// name is not a project record, the record states no project UUID, the bundle
// holds no value for it, bitbucket.org answered 403 or 404, or the answer
// itself states no project UUID. A caller that cannot be told yes must read no.
//
// An error is the LOOKUP failing — a canonical that names no Bitbucket
// repository, bitbucket.org unreachable, or a status that is neither an answer
// nor a refusal — and no error carries the token's value.
func (l *ProjectLookup) ProjectOf(ctx context.Context, canonical string, tok Name) (string, error) {
	workspace, repo, err := splitCanonicalRepo(canonical)
	if err != nil {
		return "", err
	}
	if !l.statesAProject(tok) {
		return "", nil
	}
	if uuid, ok := l.held(canonical); ok {
		return uuid, nil
	}
	if l.tokens == nil {
		return "", nil
	}
	value, ok := l.tokens.Value(tok)
	if !ok {
		return "", nil
	}
	uuid, status, err := l.ask(ctx, workspace, repo, value)
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusOK:
		if uuid == "" {
			return "", nil
		}
		l.hold(canonical, uuid)
		return uuid, nil
	case http.StatusForbidden, http.StatusNotFound:
		l.Evict(canonical)
		return "", nil
	}
	return "", fmt.Errorf("bbtokens: bitbucket.org answered %d for %s's project lookup of %s", status, tok, canonical)
}

// Evict drops one repository's held answer, so the next ProjectOf asks
// bitbucket.org again.
func (l *ProjectLookup) Evict(canonical string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, canonical)
}

// held answers one repository's project UUID from memory, if it is still
// within its TTL. An entry that has run out is dropped as it is found, so a
// repository that is not asked about again does not keep its answer alive.
func (l *ProjectLookup) held(canonical string) (string, bool) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[canonical]
	if !ok {
		return "", false
	}
	if !now.Before(entry.expires) {
		delete(l.entries, canonical)
		return "", false
	}
	return entry.uuid, true
}

// hold keeps one repository's answer for the TTL. A full cache drops the entry
// with the least time left: the repository that was asked for once and never
// again is the one whose answer is worth losing.
func (l *ProjectLookup) hold(canonical, uuid string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.entries[canonical]; !ok && len(l.entries) >= maxProjectCacheEntries {
		delete(l.entries, soonest(l.entries))
	}
	l.entries[canonical] = lookupEntry{uuid: uuid, expires: l.now().Add(ProjectLookupTTL)}
}

// soonest is the repository whose held answer runs out first. It is called only
// on a cache that holds at least one entry.
func soonest(entries map[string]lookupEntry) string {
	var earliest time.Time
	var first string
	for canonical, entry := range entries {
		if first == "" || entry.expires.Before(earliest) {
			first, earliest = canonical, entry.expires
		}
	}
	return first
}

// statesAProject reports whether tok is a token this lookup may spend: a
// project record that states the project UUID the answer is confirmed against.
func (l *ProjectLookup) statesAProject(tok Name) bool {
	for _, rec := range l.records {
		if rec.Name == tok {
			return rec.Type == TypeProject && rec.ProjectUUID != ""
		}
	}
	return false
}

// ask makes the one call: the repository document at Bitbucket's constant API
// root, with the candidate project token as a Bearer credential.
//
// The status is returned rather than judged, because "refused" and "answered
// nothing" are the caller's business (a 403 and a 404 are coverage answers; a
// 500 is not).
func (l *ProjectLookup) ask(ctx context.Context, workspace, repo, value string) (uuid string, status int, err error) {
	ctx, cancel := context.WithTimeout(ctx, projectLookupTimeout)
	defer cancel()
	target := bitbucket.DefaultBaseURL + "/repositories/" + url.PathEscape(workspace) + "/" + url.PathEscape(repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", 0, errors.New("bbtokens: the project lookup could not be built")
	}
	req.Header.Set("Authorization", "Bearer "+value)
	req.Header.Set("Accept", "application/json")
	resp, err := l.client.Do(req)
	if err != nil {
		return "", 0, errors.New("bbtokens: bitbucket.org could not be reached for a project lookup")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxProjectAnswerBytes))
	if err != nil {
		return "", 0, errors.New("bbtokens: bitbucket.org's repository answer could not be read")
	}
	var ans lookupAnswer
	if err := json.Unmarshal(raw, &ans); err != nil {
		return "", 0, errors.New("bbtokens: bitbucket.org's repository answer is not JSON")
	}
	return ans.Project.UUID, http.StatusOK, nil
}

// splitCanonicalRepo reads a pairing's canonical repository
// ("bitbucket/<workspace>/<repo>") into the two path segments the repository
// API takes. A canonical that names another provider, or that is not
// "<workspace>/<repo>", is refused rather than guessed at: this lookup addresses
// one provider's API and must not be handed a repository it cannot place.
func splitCanonicalRepo(canonical string) (workspace, repo string, err error) {
	provider, rest, ok := strings.Cut(canonical, "/")
	if !ok || provider != "bitbucket" {
		return "", "", errors.New("bbtokens: the project lookup names no Bitbucket repository")
	}
	workspace, repo, ok = strings.Cut(rest, "/")
	if !ok || workspace == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", errors.New("bbtokens: the project lookup names no Bitbucket repository")
	}
	return workspace, repo, nil
}
