package runstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/agent"
)

// MaxSessionBytes caps a saved Claude Code session file
// (session/<id>.jsonl), both when the runner saves one and when a
// follow-up reads it back.
const MaxSessionBytes = 64 << 20

// SessionMeta is session/session.json: which Claude Code session a run
// saved, and where it stood. It is written after the session file, so its
// presence means the file is complete.
type SessionMeta struct {
	Version int    `json:"version"` // 1
	ID      string `json:"id"`
	// HeadSHA is the commit the session's run pushed (else its HEAD).
	HeadSHA string `json:"head_sha"`
	// Pushed is whether the run pushed HeadSHA. When it didn't, HeadSHA is
	// its local HEAD, a commit the branch never had.
	Pushed bool `json:"pushed"`
	// WorkDir is the checkout the session ran in; Claude Code keys its
	// sessions on it.
	WorkDir string `json:"workdir"`
	Bytes   int64  `json:"bytes"`
}

// ErrInvalidSession means session.json names no usable session: its
// version is unknown or its ID isn't a lower-case UUID.
var ErrInvalidSession = errors.New("session.json names no usable session")

const (
	sessionMetaName = "session/session.json"
	// reviewMetaName is where a review stage's session goes (C1.17, D138):
	// beside the work session, never in its place. A review runs in a session
	// of its own, and a follow-up resumes the work (session/session.json) —
	// the conversation that holds what the run built, not the one that read
	// it.
	reviewMetaName = "session/review.json"
	sessionVersion = 1
)

func sessionFileName(id string) string { return "session/" + id + ".jsonl" }

func reviewSessionFileName(id string) string { return "session/review/" + id + ".jsonl" }

// PutSession stores a session's file as session/<id>.jsonl, then its meta
// as session/session.json.
func (s *Store) PutSession(ctx context.Context, m SessionMeta, data []byte) error {
	return s.putSessionAt(ctx, m, data, sessionFileName(m.ID), sessionMetaName)
}

// PutReviewSession stores a review stage's session as
// session/review/<id>.jsonl, then its meta as session/review.json. It is the
// review's session saved too, not instead (C1.17, D138): the work session
// keeps its own objects, and both are redacted and capped the same way.
func (s *Store) PutReviewSession(ctx context.Context, m SessionMeta, data []byte) error {
	return s.putSessionAt(ctx, m, data, reviewSessionFileName(m.ID), reviewMetaName)
}

func (s *Store) putSessionAt(ctx context.Context, m SessionMeta, data []byte, file, metaName string) error {
	if !agent.ValidSessionID(m.ID) {
		return fmt.Errorf("%w: the session ID is not a lower-case UUID", ErrInvalidSession)
	}
	meta, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := s.PutFile(ctx, file, data, "application/x-ndjson"); err != nil {
		return err
	}
	return s.PutFile(ctx, metaName, meta, "application/json")
}

// ReadSession reads the run's saved session: its meta (capped at
// MaxRecordBytes; ErrNotFound when the run saved none) and then its file
// (capped at MaxSessionBytes). Both objects are writable by the run's
// agent, so nothing in the meta is trusted: an ID that isn't a lower-case
// UUID is never used to name an object (ErrInvalidSession). When the meta
// is readable but the file is not, the meta is returned with the error,
// so the caller can say which check failed.
func (s *Store) ReadSession(ctx context.Context) (*SessionMeta, []byte, error) {
	raw, err := s.readRecordObject(ctx, sessionMetaName)
	if err != nil {
		return nil, nil, err
	}
	var m SessionMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil, fmt.Errorf("decoding %s: %w", sessionMetaName, err)
	}
	switch {
	case m.Version != sessionVersion:
		return &m, nil, fmt.Errorf("%w: version %d", ErrInvalidSession, m.Version)
	case !agent.ValidSessionID(m.ID):
		return &m, nil, fmt.Errorf("%w: the session ID is not a lower-case UUID", ErrInvalidSession)
	}
	data, err := s.read(ctx, sessionFileName(m.ID), MaxSessionBytes)
	if err != nil {
		return &m, nil, err
	}
	return &m, data, nil
}
