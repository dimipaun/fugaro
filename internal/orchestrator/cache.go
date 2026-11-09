// The session-start cache: what the daemon writes after every tick so a print
// that runs at the start of every session reads a file and reaches no network
// (D1243).
//
// The reader is internal/cli's (`orchestrator state --brief --offline`): one
// file, one shape, two ends of it. The reader is the one that decides whether
// a file is current — it refuses a document this release does not read, a
// version from another release and a stamp that is missing — and this is the
// writer that has to produce that document: the generated brief (fenced and
// cleaned, G3), the server time it was written and the cache format's own
// version.
//
// Nothing here fails a tick. The cache is what a print reads, and a tick that
// could not write it is a tick that still did its work: the failure goes to
// the log the daemon was built with and the cycle carries on.

package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
)

// BriefCacheVersion is the version of the cached brief's own format, written
// beside the document it carries so that a reader tells an older file from a
// current one rather than reading a shape that moved. It is deliberately its
// own number: the state's schema_version answers a different question, and the
// cache outlives the node it was generated from.
const BriefCacheVersion = 1

// BriefCache is the file's shape on the wire. The reader (internal/cli's
// orchestratorBriefCache) decodes exactly these three fields with unknown
// fields refused, so a fourth one here would stop every print.
type BriefCache struct {
	Version   int       `json:"schema_version"`
	Brief     string    `json:"brief"`
	WrittenAt time.Time `json:"written_at"`
}

// BriefCachePath is where the daemon's cached brief for one orchestrator id
// lives: $XDG_CACHE_HOME/fugaro/orchestrator/<id>.json, placed through
// localcfg.UserDir the way the name check's cache is. goos is a parameter
// rather than a read of runtime.GOOS for the same reason UserDir takes one:
// the Windows rule stays testable from a Linux checkout.
func BriefCachePath(getenv func(string) string, goos, id string) (string, error) {
	if err := checkAgentID(id); err != nil {
		return "", err
	}
	dir, err := localcfg.UserDir(getenv, goos, localcfg.DirCache)
	if err != nil {
		return "", fmt.Errorf("the daemon's cached brief cannot be placed: %w", err)
	}
	return filepath.Join(dir, "fugaro", "orchestrator", id+".json"), nil
}

// BriefCacheWriter writes one orchestrator id's cache. It is a value rather
// than three arguments to a function because the daemon builds it once, at
// start-up, and every tick then writes through the same one.
type BriefCacheWriter struct {
	// Getenv reads the environment the cache's directory is placed from, and
	// GOOS is the platform its fallback is chosen for. A test passes its own,
	// so no write here can reach a developer's own cache.
	Getenv func(string) string
	GOOS   string
	// ID is the orchestrator id: the `<id>` of orchestrator/<id>, and the
	// cache file's own name.
	ID string
	// Log is where a best-effort failure goes, and nil is a writer that
	// reports nothing. It is handed the error and never a string, so a caller
	// decides how much of it to print.
	Log func(error)
}

// Write writes the brief of s, stamped at the time the caller read.
//
// It never returns an error: every failure — a path that cannot be placed, a
// directory that cannot be created, a rename that a kill interrupted — is
// handed to Log, because the cache is what a session-start print reads and
// never what a tick decides on (D1243). The write is atomic: the document goes
// to a temp file in the cache's own directory and is renamed over the file the
// reader may be holding, so a reader sees either the previous brief or this
// one and never half of either. The temp file is mode 0600 and is removed if
// it never gets renamed.
func (w *BriefCacheWriter) Write(s State, at time.Time) {
	if err := w.write(s, at); err != nil {
		w.fail(err)
	}
}

func (w *BriefCacheWriter) write(s State, at time.Time) error {
	path, err := BriefCachePath(w.Getenv, w.GOOS, w.ID)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating the directory of the cached brief at %s: %w", dir, err)
	}
	data, err := json.Marshal(BriefCache{Version: BriefCacheVersion, Brief: Summarize(s), WrittenAt: at.UTC()})
	if err != nil {
		return fmt.Errorf("encoding the cached brief: %w", err)
	}
	f, err := os.CreateTemp(dir, ".brief-*.tmp")
	if err != nil {
		return fmt.Errorf("writing the cached brief beside %s: %w", path, err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("setting the mode of the cached brief: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing the cached brief: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing the cached brief: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("placing the cached brief at %s: %w", path, err)
	}
	return nil
}

func (w *BriefCacheWriter) fail(err error) {
	if w.Log != nil {
		w.Log(err)
	}
}

// StepWithCache runs one tick and then writes the session-start cache of the
// state the tick left (O8).
//
// It is what the daemon's loop calls instead of Tick.Step: the cache is
// written after every cycle, and a cycle that failed wrote nothing to cache —
// a brief generated from a state the tick could not read would be a stamp
// saying the daemon is further along than it is. The read it takes for the
// brief is a read of the node the tick has just written, which the store's own
// Load answers from the document it validated.
//
// A failure of either the read or the write is logged and never returned: the
// tick's own error is the only one the loop acts on.
func StepWithCache(ctx context.Context, t *Tick, w *BriefCacheWriter) (Report, error) {
	rep, err := t.Step(ctx)
	if err != nil {
		return rep, err
	}
	s, _, lerr := t.Store.Load(ctx)
	if lerr != nil {
		w.fail(fmt.Errorf("reading the state for the session-start cache: %w", lerr))
		return rep, nil
	}
	now, ok := t.Now()
	if !ok {
		w.fail(ErrNoServerTime)
		return rep, nil
	}
	w.Write(s, stamp(now))
	return rep, nil
}
