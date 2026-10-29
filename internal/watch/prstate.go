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

	"github.com/dimipaun/fugaro/internal/procgroup"
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
//     which is exactly today's behaviour. At most one note says why, once, and
//     it names a fixed reason — never a word of what gh printed;
//   - the note describes the lookups failing NOW: it is dropped as soon as
//     they all answer, and a URL that keeps failing is backed off (PRStateRetry
//     doubling to PRStateRetryMax), so a broken gh costs a handful of processes
//     an hour rather than one a minute per pull request.

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
	// PRStateRetryMax caps the backoff: a URL that keeps failing is asked
	// about every PRStateRetry, then twice that, and so on, but never less
	// often than this — a gh that starts working again (a token restored, a
	// rate limit expiring) is noticed within PRStateRetryMax, and a gh that
	// stays broken costs one process per PRStateRetryMax per URL however long
	// the screen stays open.
	PRStateRetryMax = 8 * time.Minute
	// PRStateConcurrency bounds how many gh processes run at once.
	PRStateConcurrency = 4
	// ghKillGrace is how long gh has to exit after the context ends, before
	// it is killed outright. It only ever matters for a gh that ignores
	// SIGTERM; the group it leaves behind is killed either way.
	ghKillGrace = 500 * time.Millisecond
)

// The reasons a note can carry. None of them holds anything gh printed: a note
// is one of these fixed sentences, and is safe to draw verbatim.
var (
	errGHNotFound = errors.New("gh not found")
	errGHNoAnswer = errors.New("gh could not tell the pull request's state")
	// These two say why, without quoting gh: the exit text is read only to
	// recognise the complaint, never to carry it further (classify).
	errGHRateLimited = errors.New("gh is rate limited")
	errGHNotLoggedIn = errors.New("gh is not logged in")
)

// ghHost is the one GitHub a ready row's URL may name, and the host every
// lookup is pinned to (ask). It is written once so the two cannot drift: the
// URL that is parsed and the repository gh is asked for name the same host.
const ghHost = "github.com"

// ghPRURL matches exactly a GitHub pull request URL. Anchored at both ends
// (\A, \z: not ^ and $, which a trailing newline would satisfy): the whole
// string must be the URL, so a URL with anything appended to it — a `;rm -rf`,
// a `$(...)`, a space, another path segment — never matches. The owner is
// GitHub's login charset (alphanumerics and inner hyphens), the repository
// GitHub's name charset, and the number is digits with no leading zero. The
// host is ghHost, the only host a lookup may be sent to.
var ghPRURL = regexp.MustCompile(`\Ahttps://` + regexp.QuoteMeta(ghHost) + `/([A-Za-z0-9](?:[A-Za-z0-9-]{0,38})?)/([A-Za-z0-9_](?:[A-Za-z0-9._-]{0,98}[A-Za-z0-9_])?)/pull/([1-9][0-9]{0,9})\z`)

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
	// fails counts the consecutive lookups that did not answer: it is what the
	// backoff is computed from, and it resets the moment one answers.
	fails int
}

// PRState is the cache of gh's answers, one per pull request URL.
type PRState struct {
	disabled bool
	timeout  time.Duration
	openTTL  time.Duration
	retry    time.Duration
	retryMax time.Duration
	now      func() time.Time
	lookPath func(string) (string, error)
	getenv   func(string) string
	// askFn is the lookup every caller goes through — Filter's background ones
	// and MergeNow's synchronous one — and is ask itself in production. A test
	// replaces it to hold a lookup open without a process, so a claim about the
	// semaphore is not also a claim about how fast the machine forks a shell.
	askFn func(context.Context, prRef) (prStateKind, error)

	mu   sync.Mutex
	seen map[string]prAnswer
	busy map[string]bool
	// fail is why the lookups that have not answered did not: one entry per
	// URL whose last lookup failed and which is still on the list. The note is
	// computed from it, so a failure that stops failing takes its note away
	// with it. An entry is dropped when that URL answers, and when it leaves
	// the list of ready rows.
	fail    map[string]error
	noGH    bool // gh is not installed: stop asking, for the life of the process
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
	RetryMax    time.Duration // PRStateRetryMax
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
		retryMax: o.RetryMax,
		now:      o.Now, lookPath: o.LookPath, getenv: o.Getenv,
		seen: map[string]prAnswer{}, busy: map[string]bool{}, fail: map[string]error{},
	}
	p.askFn = p.ask
	if p.timeout <= 0 {
		p.timeout = PRStateTimeout
	}
	if p.openTTL <= 0 {
		p.openTTL = PRStateOpenTTL
	}
	if p.retry <= 0 {
		p.retry = PRStateRetry
	}
	if p.retryMax < p.retry {
		p.retryMax = max(PRStateRetryMax, p.retry)
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
	var listed map[string]bool // the URLs this frame holds, when a failure may be stale
	p.mu.Lock()
	if len(p.fail) > 0 {
		listed = make(map[string]bool, len(p.fail))
	}
	for _, row := range rows {
		ref, ok := parseGHPRURL(row.Run.PRURL)
		if !ok {
			out = append(out, row)
			continue
		}
		if listed != nil {
			listed[ref.url] = true
		}
		a, ok := p.seen[ref.url]
		if ok && a.state.gone() {
			continue // merged or closed: off the list for good
		}
		// noGH means gh will not appear while this process lives, so nothing
		// more is attempted — but it is no reason to forget what gh already
		// said (the answers above are still applied) or to stop drawing the
		// note about the rows on screen (the pruning below still runs).
		if !p.noGH && (!ok || now.Sub(a.at) >= p.ttl(a)) && !p.busy[ref.url] {
			p.busy[ref.url] = true
			todo = append(todo, ref)
		}
		out = append(out, row)
	}
	// The note is about the rows on the screen: a URL that has left the list
	// stopped failing as far as anyone can see, so its complaint goes with it.
	for u := range p.fail {
		if !listed[u] {
			delete(p.fail, u)
		}
	}
	p.mu.Unlock()
	for _, ref := range todo {
		p.start(ref)
	}
	return out
}

// ttl is how long an answer is kept: forever for a merged or closed one, the
// short TTL for an open one, and a growing, capped backoff for a failure, so a
// broken gh is not spawned once per frame — nor once per minute per pull
// request for as long as the screen stays open.
func (p *PRState) ttl(a prAnswer) time.Duration {
	switch a.state {
	case prMerged, prClosed:
		return 0
	case prOpen:
		return p.openTTL
	}
	d := p.retry
	for i := 1; i < a.fails; i++ {
		if d >= p.retryMax/2 {
			return p.retryMax
		}
		d *= 2
	}
	return min(d, p.retryMax)
}

// Note is the one-line reason the ready list's PR states cannot be read, ""
// when none is missing. It describes the lookups failing NOW — it goes away as
// soon as they all answer — says one thing for the whole list rather than one
// per URL, and holds a fixed sentence of ours, never gh's own output.
func (p *PRState) Note() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.noteLocked()
}

// noteLocked derives the note from the failures in hand. The order is by how
// much the operator can do about it: a missing gh first, then the two
// complaints gh names itself, then the catch-all.
func (p *PRState) noteLocked() string {
	if len(p.fail) == 0 {
		return ""
	}
	for _, err := range []error{errGHNotFound, errGHRateLimited, errGHNotLoggedIn} {
		if p.anyFailedWith(err) {
			return "PR state unavailable: " + err.Error()
		}
	}
	return "PR state unavailable: " + errGHNoAnswer.Error()
}

// anyFailedWith is true when some URL's last lookup failed with err.
func (p *PRState) anyFailedWith(err error) bool {
	for _, e := range p.fail {
		if errors.Is(e, err) {
			return true
		}
	}
	return false
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
// output, and only a fixed reason for a failure (classify): everything gh
// printed is discarded with this call.
func (p *PRState) look(ref prRef) {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	state, err := p.askFn(ctx, ref)

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
		fails := 1
		if prev, ok := p.seen[ref.url]; ok && prev.state == prUnknown {
			fails = prev.fails + 1
		}
		// This URL is failing now; Note is derived from this map, so the note
		// appears when a lookup fails and disappears when none is failing.
		p.fail[ref.url] = err
		p.seen[ref.url] = prAnswer{state: prUnknown, at: p.now(), fails: fails}
		return
	}
	delete(p.fail, ref.url)
	p.seen[ref.url] = prAnswer{state: state, at: p.now()}
}

// ask runs `gh pr view <n> --repo github.com/<owner>/<repo> --json state --jq
// .state`. There is no shell and no interpolation: the number and the
// repository are separate argv elements, and only ever come from a URL that
// matched ghPRURL.
//
// The repository carries the host, and it is ghHost, the host of the URL that
// was parsed. A bare `--repo <owner>/<repo>` would be resolved against gh's
// *default* host instead, and GH_HOST is what sets that default — a variable
// ghEnv forwards, so an operator whose gh is set up for an enterprise host
// keeps the setup. The lookup would then ask that other GitHub for a
// repository of the same name and take its answer: a MERGED there drops a row
// whose github.com pull request is still open. With the host written out,
// GH_HOST cannot move the question; a gh that is not logged in to ghHost
// answers nothing, which fails open like every other bad answer.
//
// gh runs the way every other command this tool spawns does (internal
// procgroup): in a process group of its own, with pipes of our own, and a
// deadline that is enforced on the whole group. That matters because killing
// gh alone is not enough — a gh that starts a credential helper or a pager,
// or that leaves any child behind holding the pipe, would keep a plain
// exec.Cmd.Output blocked long past its context.
func (p *PRState) ask(ctx context.Context, ref prRef) (prStateKind, error) {
	bin, err := p.lookPath("gh")
	if err != nil {
		return prUnknown, errGHNotFound
	}
	// Both streams are capped: gh's stdout is read for one word, and its
	// stderr is read only to recognise the complaint (classify). Neither is
	// ever kept, logged or drawn.
	var out, complaint boundedBuffer
	code, err := procgroup.Run(ctx, procgroup.Cmd{
		Name: bin,
		Args: []string{"pr", "view", strconv.Itoa(ref.number),
			"--repo", ghHost + "/" + ref.owner + "/" + ref.repo, "--json", "state", "--jq", ".state"},
		Env:    ghEnv(p.getenv),
		Stdout: &out, Stderr: &complaint,
		Grace: ghKillGrace,
	})
	if err != nil || code != 0 {
		return prUnknown, classify(complaint.String())
	}
	switch strings.ToUpper(strings.TrimSpace(out.String())) {
	case "OPEN":
		return prOpen, nil
	case "MERGED":
		return prMerged, nil
	case "CLOSED":
		return prClosed, nil
	}
	return prUnknown, errGHNoAnswer
}

// classify turns what gh complained about into one of our own fixed sentences,
// so the note can say more than "it did not work" without ever carrying a word
// gh printed. gh's text is matched here and dropped here.
func classify(complaint string) error {
	s := strings.ToLower(complaint)
	switch {
	case strings.Contains(s, "rate limit"):
		return errGHRateLimited
	case strings.Contains(s, "authentication"), strings.Contains(s, "not logged in"),
		strings.Contains(s, "gh auth login"), strings.Contains(s, "bad credentials"):
		return errGHNotLoggedIn
	}
	return errGHNoAnswer
}

// boundedBuffer keeps the first boundedMax bytes written to it and counts, but
// drops, the rest: a runaway gh cannot grow this process's memory, and a write
// never fails, so the child is never left blocked on a full pipe. It has a
// mutex of its own because the writer is not always done when the lookup is:
// procgroup gives up on a child that escaped the process group after its own
// drain timeout, and that child's copy goroutine may still be writing here.
type boundedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const boundedMax = 4096

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := boundedMax - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// ghEnvKeep is the whole environment a gh of ours may inherit: what it needs
// to find its configuration and its credentials — the two tokens, the
// configuration directory (GH_CONFIG_DIR, else XDG_CONFIG_HOME), the host (so
// a gh set up for an enterprise host is the same gh the operator uses, though
// it cannot change which host is asked: ask pins that) and the certificate
// store, since a gh that cannot verify the server cannot answer — plus the
// terminal it may want to draw in, and the proxy variables, on the off chance
// that the network only works through one. Nothing else of the operator's (or
// the run's) environment reaches it.
//
// Variables are forwarded, never read: no value here is inspected, compared or
// logged. They are named in ghEnvKeep in the order gh is given them.
var ghEnvKeep = []string{
	"PATH", "HOME", "GH_TOKEN", "GITHUB_TOKEN",
	"GH_CONFIG_DIR", "XDG_CONFIG_HOME", "GH_HOST", "GH_ENTERPRISE_TOKEN",
	"SSL_CERT_FILE", "TERM",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "no_proxy",
}

// ghEnv builds gh's environment. The result is never nil: a nil Env means
// "inherit everything" to both exec and procgroup, which is the one thing this
// function exists to prevent.
func ghEnv(getenv func(string) string) []string {
	env := make([]string, 0, len(ghEnvKeep))
	for _, k := range ghEnvKeep {
		if v := getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}
