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
	sessionVersion  = 1
)

func sessionFileName(id string) string { return "session/" + id + ".jsonl" }

// PutSession stores a session's file as session/<id>.jsonl, then its meta
// as session/session.json.
func (s *Store) PutSession(ctx context.Context, m SessionMeta, data []byte) error {
	if !agent.ValidSessionID(m.ID) {
		return fmt.Errorf("%w: the session ID is not a lower-case UUID", ErrInvalidSession)
	}
	meta, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := s.PutFile(ctx, sessionFileName(m.ID), data, "application/x-ndjson"); err != nil {
		return err
	}
	return s.PutFile(ctx, sessionMetaName, meta, "application/json")
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
