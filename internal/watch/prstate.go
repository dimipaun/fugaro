package watch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The "Ready for your review" list holds what the runs bucket last recorded:
// a run whose result.json says its pull request is ready. That is a statement
// about the run, not about the pull request, and it never goes stale on its
// own — so a ready PR the owner reviewed and merged this morning keeps asking
// for a look this afternoon (owner request, 2026-10-16).
//
// PRState answers the missing half: what the pull request's *state* is now.
// The owner decided (2026-10-16) that the answer comes from the operator's own
// `gh`, when it is installed; nothing here ever holds a credential of its own,
// and nothing is ever sent anywhere but the operator's own authenticated gh.
//
// The rules, in one place:
//   - only a URL of exactly the form https://github.com/<owner>/<repo>/pull/<n>
//     is ever passed on (strict, anchored matching below); anything else —
//     a Bitbucket URL, a URL with a space, a newline, a `;`, a `$(...)`, a
//     `..` — is listed as before and never reaches a subprocess;
//   - the answer is cached per URL: a merged or closed PR never un-merges, so
//     it is remembered for the life of the process; an open one for a short
//     TTL;
//   - lookups run in the background, off the render loop: the list draws as
//     today and a merged PR drops off on the next refresh;
//   - everything that is not a clean state word fails OPEN. gh missing, not
//     logged in, rate-limited, failing or timing out leaves the PR listed,
//     which is exactly today's behaviour. At most one note says why, once.

const (
	// PRStateTimeout bounds one `gh` call.
	PRStateTimeout = 3 * time.Second
	// PRStateOpenTTL is how long an OPEN answer is trusted. A merged PR never
	// un-merges, so MERGED and CLOSED carry no TTL at all.
	PRStateOpenTTL = 5 * time.Minute
	// PRStateRetry is how long a failed or unreadable answer is kept before
	// the URL is asked about again. It exists so a broken gh is not spawned
	// on every frame.
	PRStateRetry = time.Minute
	// PRStateConcurrency bounds how many gh processes run at once.
	PRStateConcurrency = 4
)

// The two reasons a note can carry. Neither holds anything gh printed: the
// note is safe to draw verbatim.
var (
	errGHNotFound = errors.New("gh not found")
	errGHNoAnswer = errors.New("gh could not tell the pull request's state")
)

// ghPRURL matches exactly a GitHub pull request URL. Anchored at both ends
// (\A, \z: not ^ and $, which a trailing newline would satisfy): the whole
// string must be the URL, so a URL with anything appended to it — a `;rm -rf`,
// a `$(...)`, a space, another path segment — never matches. The owner is
// GitHub's login charset (alphanumerics and inner hyphens), the repository
// GitHub's name charset, and the number is digits with no leading zero.
var ghPRURL = regexp.MustCompile(`\Ahttps://github\.com/([A-Za-z0-9](?:[A-Za-z0-9-]{0,38})?)/([A-Za-z0-9_](?:[A-Za-z0-9._-]{0,98}[A-Za-z0-9_])?)/pull/([1-9][0-9]{0,9})\z`)

// prRef is one parsed GitHub pull request URL.
type prRef struct {
	url    string
	owner  string
	repo   string
	number int
}

// parseGHPRURL parses a ready row's PR URL. ok is false for everything that is
// not exactly a GitHub pull request URL, including URLs whose owner or
// repository name contains "..": the charset above allows a dot inside a
// repository name, and a traversal-looking one is refused rather than handed
// to gh as `--repo a/..`.
func parseGHPRURL(u string) (prRef, bool) {
	m := ghPRURL.FindStringSubmatch(u)
	if m == nil {
		return prRef{}, false
	}
	owner, repo := m[1], m[2]
	if strings.Contains(owner, "..") || strings.Contains(repo, "..") {
		return prRef{}, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil {
		return prRef{}, false
	}
	return prRef{url: u, owner: owner, repo: repo, number: n}, true
}

// prStateKind is what gh said about a pull request.
type prStateKind int

const (
	prUnknown prStateKind = iota // not asked yet, or no usable answer
	prOpen
	prMerged
	prClosed
)

// gone is true for a state that takes the row off the list, and that never
// goes back: caching it for the life of the process is not a shortcut.
func (s prStateKind) gone() bool { return s == prMerged || s == prClosed }

type prAnswer struct {
	state prStateKind
	at    time.Time
}

// PRState is the cache of gh's answers, one per pull request URL.
type PRState struct {
	disabled bool
	timeout  time.Duration
	openTTL  time.Duration
	retry    time.Duration
	now      func() time.Time
	lookPath func(string) (string, error)
	getenv   func(string) string

	mu      sync.Mutex
	seen    map[string]prAnswer
	busy    map[string]bool
	noGH    bool // gh is not installed: stop asking, for the life of the process
	note    string
	version uint64
	wg      sync.WaitGroup
	sem     chan struct{}
}

// PRStateOptions tune a PRState; the zero value is the production setting.
type PRStateOptions struct {
	// Disabled turns the whole thing off (--no-pr-state): no lookup runs and
	// every ready row is listed.
	Disabled    bool
	Timeout     time.Duration // PRStateTimeout
	OpenTTL     time.Duration // PRStateOpenTTL
	Retry       time.Duration // PRStateRetry
	Concurrency int           // PRStateConcurrency
	// Now, LookPath and Getenv replace the clock, exec.LookPath and os.Getenv
	// (tests).
	Now      func() time.Time
	LookPath func(string) (string, error)
	Getenv   func(string) string
}

// NewPRState returns a resolver. It spawns nothing until Filter asks it to.
func NewPRState(o PRStateOptions) *PRState {
	p := &PRState{
		disabled: o.Disabled, timeout: o.Timeout, openTTL: o.OpenTTL, retry: o.Retry,
		now: o.Now, lookPath: o.LookPath, getenv: o.Getenv,
		seen: map[string]prAnswer{}, busy: map[string]bool{},
	}
	if p.timeout <= 0 {
		p.timeout = PRStateTimeout
	}
	if p.openTTL <= 0 {
		p.openTTL = PRStateOpenTTL
	}
	if p.retry <= 0 {
		p.retry = PRStateRetry
	}
	if p.now == nil {
		p.now = func() time.Time { return time.Now().UTC() }
	}
	if p.lookPath == nil {
		p.lookPath = exec.LookPath
	}
	if p.getenv == nil {
		p.getenv = os.Getenv
	}
	n := o.Concurrency
	if n <= 0 {
		n = PRStateConcurrency
	}
	p.sem = make(chan struct{}, n)
	return p
}

// Filter returns the ready rows to draw: every row whose pull request is not
// known to be merged or closed. A row whose state is not known, or whose URL
// is not a GitHub pull request URL, is returned unchanged (fail open); the
// lookups a row still needs are started in the background, so this call never
// blocks and never spawns a process in the caller's goroutine.
func (p *PRState) Filter(rows []ReadyRow) []ReadyRow {
	if p == nil || p.disabled {
		return rows
	}
	now := p.now()
	var out []ReadyRow
	var todo []prRef
	p.mu.Lock()
	if p.noGH {
		p.mu.Unlock()
		return rows
	}
	for _, row := range rows {
		ref, ok := parseGHPRURL(row.Run.PRURL)
		if !ok {
			out = append(out, row)
			continue
		}
		a, ok := p.seen[ref.url]
		if ok && a.state.gone() {
			continue // merged or closed: off the list for good
		}
		if (!ok || now.Sub(a.at) >= p.ttl(a)) && !p.busy[ref.url] {
			p.busy[ref.url] = true
			todo = append(todo, ref)
		}
		out = append(out, row)
	}
	p.mu.Unlock()
	for _, ref := range todo {
		p.start(ref)
	}
	return out
}

// ttl is how long an answer is kept: forever for a merged or closed one, the
// short TTL for an open one, and Retry for a failure, so a broken gh is not
// spawned once per frame.
func (p *PRState) ttl(a prAnswer) time.Duration {
	switch a.state {
	case prMerged, prClosed:
		return 0
	case prOpen:
		return p.openTTL
	}
	return p.retry
}

// Note is the one-line reason PR states could not be read, "" when none
// happened. It is set once, never per URL, and never holds gh's own output.
func (p *PRState) Note() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.note
}

// Version counts answers landed; a caller redrawing on a timer compares it to
// notice that the list may have changed.
func (p *PRState) Version() uint64 {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version
}

// Settle waits for the lookups in flight to answer, at most d. --once uses it
// so one snapshot's count is the truth, not a race; the live screen never
// waits.
func (p *PRState) Settle(d time.Duration) {
	if p == nil || p.disabled {
		return
	}
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

// start runs one lookup in the background, at most Concurrency at a time.
func (p *PRState) start(ref prRef) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.sem <- struct{}{}
		defer func() { <-p.sem }()
		p.look(ref)
	}()
}

// look asks gh once and records the answer. Only the state word leaves gh's
// output: everything else is discarded here.
func (p *PRState) look(ref prRef) {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	state, err := p.ask(ctx, ref)

	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.busy, ref.url)
	p.version++
	if err != nil {
		if errors.Is(err, errGHNotFound) {
			// gh will not appear mid-process: stop asking entirely, and say
			// so once.
			p.noGH = true
		}
		if p.note == "" {
			p.note = "PR state unavailable: " + err.Error()
		}
		p.seen[ref.url] = prAnswer{state: prUnknown, at: p.now()}
		return
	}
	p.seen[ref.url] = prAnswer{state: state, at: p.now()}
}

// ask runs `gh pr view <n> --repo <owner>/<repo> --json state --jq .state`.
// There is no shell and no interpolation: the number and the repository are
// separate argv elements, and only ever come from a URL that matched ghPRURL.
func (p *PRState) ask(ctx context.Context, ref prRef) (prStateKind, error) {
	bin, err := p.lookPath("gh")
	if err != nil {
		return prUnknown, errGHNotFound
	}
	cmd := exec.CommandContext(ctx, bin, "pr", "view", strconv.Itoa(ref.number),
		"--repo", ref.owner+"/"+ref.repo, "--json", "state", "--jq", ".state")
	cmd.Env = ghEnv(p.getenv)
	out, err := cmd.Output()
	if err != nil {
		return prUnknown, errGHNoAnswer
	}
	switch strings.ToUpper(strings.TrimSpace(string(out))) {
	case "OPEN":
		return prOpen, nil
	case "MERGED":
		return prMerged, nil
	case "CLOSED":
		return prClosed, nil
	}
	return prUnknown, errGHNoAnswer
}

// ghEnvKeep is the whole environment a gh of ours may inherit: what it needs
// to find its configuration and its token, and the proxy variables, on the
// off chance that the network only works through one. Nothing else of the
// operator's (or the run's) environment reaches it.
var ghEnvKeep = []string{
	"PATH", "HOME", "GH_TOKEN", "GITHUB_TOKEN",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "no_proxy",
}

func ghEnv(getenv func(string) string) []string {
	var env []string
	for _, k := range ghEnvKeep {
		if v := getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}
