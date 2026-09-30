package agent

import (
	"bytes"
	"errors"
	"path/filepath"
	"regexp"
)

// SessionDir is where Claude Code keeps workDir's sessions under home:
// home/.claude/projects/<escaped workDir>, where every byte of workDir
// outside [A-Za-z0-9] becomes "-" (/work/repo → -work-repo), Claude Code's
// project-directory naming as observed.
func SessionDir(home, workDir string) string {
	esc := []byte(workDir)
	for i, c := range esc {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9') {
			esc[i] = '-'
		}
	}
	return filepath.Join(home, ".claude", "projects", string(esc))
}

var sessionIDRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidSessionID reports whether id is a lower-case UUID, the form
// NewSessionID makes and Claude Code reports. Only such an ID may name a
// session file or object.
func ValidSessionID(id string) bool { return sessionIDRE.MatchString(id) }

// ErrNoSession means a --resume run found no session to resume.
var ErrNoSession = errors.New("claude found no conversation to resume")

// noSessionText is what Claude Code prints on stderr when --resume names a
// session it doesn't have.
const noSessionText = "No conversation found with session ID"

// stderrMatch watches a stream for noSessionText, across writes.
type stderrMatch struct {
	tail  []byte
	found bool
}

func (m *stderrMatch) Write(p []byte) (int, error) {
	if m.found {
		return len(p), nil
	}
	buf := append(m.tail, p...)
	if bytes.Contains(buf, []byte(noSessionText)) {
		m.found, m.tail = true, nil
		return len(p), nil
	}
	// Keep just enough to catch the text split across two writes.
	keep := min(len(buf), len(noSessionText)-1)
	m.tail = append(m.tail[:0:0], buf[len(buf)-keep:]...)
	return len(p), nil
}
