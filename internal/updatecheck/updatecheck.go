// Package updatecheck asks GitHub, at most once a day, whether a newer
// stable fugaro release exists, caches the answer in the user's cache
// directory, and composes the one-line stderr nag and the comparison
// fugaro doctor's cli-version check reports.
//
// Every read here is best effort: a network failure, an oversized or
// malformed response, or a corrupt cache file is treated as "nothing to
// say", never an error a caller must handle. Nothing sent carries any
// identifying data: a plain GET to GitHub's "latest release" endpoint,
// which never returns a draft or a pre-release.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// Repo is the GitHub repository releases are checked against.
const Repo = "dimipaun/fugaro"

// BrewCommand is the fix line's upgrade command: a constant, never built
// from a response value.
const BrewCommand = "brew upgrade dimipaun/tap/fugaro"

// Endpoint is GitHub's "latest release" endpoint: drafts and pre-releases
// are never returned by it, so Fetch needs no filtering of its own for
// either.
const Endpoint = "https://api.github.com/repos/" + Repo + "/releases/latest"

// CheckTTL is how long a cached check stands before the daily check asks
// GitHub again.
const CheckTTL = 24 * time.Hour

// WarnTTL is how long the nag line, once printed for a given latest
// version, stays silent before it may print again for that same version.
const WarnTTL = 24 * time.Hour

// FetchTimeout bounds the HTTP round trip Refresh makes.
const FetchTimeout = time.Second

// maxBody bounds the response GitHub might send: a real release object is a
// few KB; anything bigger is refused unread rather than buffered.
const maxBody = 64 << 10

// semverRE matches a strict X.Y.Z release version, an optional "v" prefix,
// no pre-release or build suffix; each component is bounded to 9 digits
// (pluginwire's own rule) so a hostile value can't overflow an int.
var semverRE = regexp.MustCompile(`^v?(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

type semver [3]int

func parseSemver(s string) (semver, bool) {
	m := semverRE.FindStringSubmatch(s)
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func (a semver) less(b semver) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func (a semver) String() string { return fmt.Sprintf("%d.%d.%d", a[0], a[1], a[2]) }

// Valid reports whether v parses strictly as X.Y.Z (with or without a "v"
// prefix, no pre-release or build suffix): the shape both the running
// binary's version and a fetched release tag must have before they are
// ever compared.
func Valid(v string) bool {
	_, ok := parseSemver(v)
	return ok
}

// Newer reports whether latest is a strictly newer release than current.
// Either failing to parse as plain X.Y.Z (a dev build, a pre-release, a
// corrupt cache value) makes the answer false: there is nothing safe to
// compare.
func Newer(latest, current string) bool {
	l, ok := parseSemver(latest)
	if !ok {
		return false
	}
	c, ok := parseSemver(current)
	if !ok {
		return false
	}
	return c.less(l)
}

// Entry is the cached state of the daily check, read and written as JSON.
type Entry struct {
	// CheckedAt is the last time GitHub was asked, success or failure: it
	// throttles the daily check regardless of the outcome, so a network
	// failure doesn't retry on every command.
	CheckedAt time.Time `json:"checked_at"`
	// Latest is the last release version actually learned (X.Y.Z, no "v"),
	// kept across a failed refresh: "" means none has ever been learned.
	Latest string `json:"latest,omitempty"`
	// WarnedAt and Warned are the last time the nag line printed and the
	// Latest it printed for, throttling the nag independently of the
	// check: a command that only reads the cache (never refreshes it)
	// still only nags once a day for a given release.
	WarnedAt time.Time `json:"warned_at,omitempty"`
	Warned   string    `json:"warned,omitempty"`
}

// Due reports whether the daily check should ask GitHub again: never
// checked, or checked CheckTTL or more ago. A timestamp from the future (a
// clock that moved back) counts as due too.
func (e Entry) Due(now time.Time) bool {
	if e.CheckedAt.IsZero() {
		return true
	}
	age := now.Sub(e.CheckedAt)
	return age < 0 || age >= CheckTTL
}

// cachePath is $XDG_CACHE_HOME/fugaro/update-check.json, else under the
// platform's own cache directory (localcfg.UserDir): %LOCALAPPDATA%\fugaro
// on Windows, ~/.cache/fugaro elsewhere. One file, not per project, since
// whether this binary is current has nothing to do with which project a
// command acts on.
func cachePath(getenv func(string) string) (string, error) {
	dir, err := localcfg.UserDir(getenv, runtime.GOOS, localcfg.DirCache)
	if err != nil {
		return "", fmt.Errorf("%w, so the update-check cache can't be written", err)
	}
	return filepath.Join(dir, "fugaro", "update-check.json"), nil
}

// readBounded reads all of r, up to 2*maxBody bytes; ok is false when that
// bound is hit (r had more to give) or the read itself failed. Pulled out
// of Load so a test can prove the bound with a counting io.Reader wrapping
// an arbitrarily large (even synthetic, never-ending) source, without
// needing a real oversized file on disk: an attacker- or
// corruption-controlled cache file must never be buffered in full before
// this decides to give up on it.
func readBounded(r io.Reader) (data []byte, ok bool) {
	data, err := io.ReadAll(io.LimitReader(r, 2*maxBody+1))
	if err != nil || len(data) > 2*maxBody {
		return nil, false
	}
	return data, true
}

// Load is the cached entry, the zero Entry when there is none or it can't
// be read: a missing, truncated or corrupt cache is treated as no cache,
// never an error.
func Load(getenv func(string) string) Entry {
	path, err := cachePath(getenv)
	if err != nil {
		return Entry{}
	}
	// Lstat, not Stat: a symlink (to /dev/zero, to a FIFO, to anything)
	// must never be followed. Save only ever creates a regular file by
	// renaming a temp file over the path, so anything else here is
	// outside tampering, treated the same as no cache at all.
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return Entry{}
	}
	f, err := os.Open(path)
	if err != nil {
		return Entry{}
	}
	defer f.Close()
	data, ok := readBounded(f)
	if !ok {
		return Entry{}
	}
	var e Entry
	if json.Unmarshal(data, &e) != nil {
		return Entry{}
	}
	return e
}

// Save writes the entry atomically, mode 0600 in a 0700 directory, through
// a temporary file renamed over it. Best effort: callers ignore the error,
// and the next command tries again.
func Save(getenv func(string) string, e Entry) error {
	path, err := cachePath(getenv)
	if err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// A process exit (crash, kill, or the normal exit WaitForUpdateCheck's
	// abandonment can lead to) between here and the Rename below skips the
	// deferred Remove, leaving a stray, harmless (a few dozen bytes) .tmp-*
	// file behind; not cleaned up, since nothing else in this package ever
	// lists the directory to do so.
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Client is the minimal HTTP client surface Fetch needs, so tests can
// inject a fake without reaching the real network; *http.Client satisfies
// it.
type Client interface {
	Do(req *http.Request) (*http.Response, error)
}

// release is the subset of GitHub's release object Fetch reads.
type release struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Fetch asks endpoint (GitHub's "latest release" API, which never returns
// a draft or a pre-release) for the latest stable release and returns its
// version, strictly parsed as X.Y.Z; ok is false on any network failure,
// non-200 status, oversized or unparseable body, or a tag that isn't
// strictly X.Y.Z. ctx bounds the request; callers wrap it with
// FetchTimeout.
func Fetch(ctx context.Context, client Client, endpoint string) (version string, ok bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "fugaro-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return "", false
	}
	var r release
	if json.Unmarshal(data, &r) != nil {
		return "", false
	}
	if r.Draft || r.Prerelease {
		// Belt and suspenders: /releases/latest never returns either.
		return "", false
	}
	v, ok := parseSemver(r.TagName)
	if !ok {
		return "", false
	}
	return v.String(), true
}

// Refresh runs the daily check when due (Load, Entry.Due): it asks
// endpoint, bounded by FetchTimeout, and saves the result. CheckedAt
// always moves to now, success or failure, so a failure doesn't retry on
// every command; Latest only changes on success, keeping the last known
// good value otherwise. A cache that can't be loaded or saved is treated
// as best effort, same as everywhere else in this package. Refresh does
// nothing when the cache is already fresh.
//
// The entry is re-Loaded right before the final Save, rather than reusing
// the snapshot Due was checked against: the HTTP round trip can take up to
// FetchTimeout, and WaitForUpdateCheck may have already given up on this
// very call and let the command finish — printing the nag (Warn, which
// also Loads then Saves) from the stale snapshot's state. Re-reading keeps
// whatever Warn already wrote (Warned, WarnedAt) instead of clobbering it
// with this call's older view; it narrows the overwrite race to the gap
// between that re-read and this Save, rather than eliminating it (a
// concurrent writer in that exact instant could still lose an update),
// which is an acceptable best-effort tradeoff for a cache this disposable.
func Refresh(ctx context.Context, getenv func(string) string, client Client, endpoint string, now time.Time) {
	e := Load(getenv)
	if !e.Due(now) {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	v, ok := Fetch(cctx, client, endpoint)
	e = Load(getenv)
	if ok {
		e.Latest = v
	}
	e.CheckedAt = now
	_ = Save(getenv, e)
}

// Warn is the one stderr line to print for current (this running binary's
// version), "" when there is nothing to say: no release has ever been
// learned, the cached latest isn't a strictly newer release than current
// (Newer, which also covers a dev or unparseable current version), or the
// line already printed for that latest release less than WarnTTL ago.
// Printing it (a non-"" return) updates the cache so later commands stay
// quiet today; that update is best effort, same as Refresh. Warn makes no
// network call: it only reads the cache Refresh last wrote.
func Warn(getenv func(string) string, now time.Time, current string) string {
	e := Load(getenv)
	if e.Latest == "" || !Newer(e.Latest, current) {
		return ""
	}
	if e.Warned == e.Latest {
		if age := now.Sub(e.WarnedAt); age >= 0 && age < WarnTTL {
			return ""
		}
	}
	e.Warned, e.WarnedAt = e.Latest, now
	_ = Save(getenv, e)
	return fmt.Sprintf("fugaro %s is available (you have %s): %s", e.Latest, current, BrewCommand)
}
