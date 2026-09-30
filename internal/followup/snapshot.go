package followup

import (
	"maps"
	"slices"
	"time"
)

// SnapshotVersion is comments.json's format version.
const SnapshotVersion = 1

// Snapshot is comments.json: the selection a follow-up's agent got,
// redacted and clipped, with what was left out and why.
type Snapshot struct {
	Version          int               `json:"version"`
	PR               int               `json:"pr"`
	Since            time.Time         `json:"since"`
	Fetched          time.Time         `json:"fetched_at"`
	Comments         []SnapshotComment `json:"comments"`
	Authors          map[string]int    `json:"authors"`
	UntrustedAuthors []string          `json:"untrusted_authors,omitempty"`
	// UntrustedAuthorCount counts every distinct untrusted author, named or not.
	UntrustedAuthorCount int            `json:"untrusted_author_count,omitempty"`
	Omitted              map[string]int `json:"omitted"`
}

// SnapshotComment is one comment in comments.json.
type SnapshotComment struct {
	Kind      string    `json:"kind"`
	Author    string    `json:"author"`
	AuthorID  string    `json:"author_id,omitempty"`
	Path      string    `json:"path,omitempty"`
	URL       string    `json:"url,omitempty"`
	Line      int       `json:"line,omitempty"`
	Outdated  bool      `json:"outdated,omitempty"`
	Truncated bool      `json:"truncated,omitempty"` // its thread had more comments than were read
	CreatedAt time.Time `json:"created_at"`
	Body      string    `json:"body"` // cleaned, redacted and clipped; the block also replaces delimiter lookalikes, which this keeps
}

// NewSnapshot is comments.json for pull request pr, whose comments were
// fetched at fetched. Its slices and maps are never nil, so they encode as
// [] and {}.
func NewSnapshot(pr int, sel Selection, fetched time.Time) Snapshot {
	s := Snapshot{
		Version:              SnapshotVersion,
		PR:                   pr,
		Since:                sel.Since,
		Fetched:              fetched,
		Comments:             make([]SnapshotComment, 0, len(sel.Comments)),
		Authors:              map[string]int{},
		UntrustedAuthors:     slices.Clone(sel.UntrustedAuthors),
		UntrustedAuthorCount: sel.UntrustedAuthorCount,
		Omitted:              map[string]int{},
	}
	maps.Copy(s.Authors, sel.Authors)
	maps.Copy(s.Omitted, sel.Omitted)
	for _, c := range sel.Comments {
		s.Comments = append(s.Comments, SnapshotComment{
			Kind:      string(c.Kind),
			Author:    c.Author,
			AuthorID:  c.AuthorID,
			Path:      c.Path,
			URL:       c.URL,
			Line:      c.Line,
			Outdated:  c.Outdated,
			Truncated: c.Truncated,
			CreatedAt: c.CreatedAt,
			Body:      c.Body,
		})
	}
	return s
}
