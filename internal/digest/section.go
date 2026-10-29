package digest

import (
	"errors"
	"fmt"
	"strings"
)

// The digest's extra-section seam (design ee-boundary-moves section 5.4): a
// build that carries the business-licensed features adds sections to a report
// through it -- T8.3's per-tier calibration is the first -- and a build that
// does not prints the report it printed before the seam existed (D528, G3).
// The digest stays MIT: it names the hook and renders what the hook returns,
// and imports nothing.

// Section is one block a hook adds to the report: its heading and the markdown
// under it.
type Section struct {
	// Title is the section's heading, printed as a level-two heading. Empty
	// prints the body with no heading of its own.
	Title string
	// Body is the section's markdown, printed as the hook wrote it. A hook
	// that fills it from a record writes text the record's own hand could have
	// written, so the caller that owns the record is the caller that redacts.
	Body string
}

var (
	// ErrSectionHookSet is a second registration: the slot is taken once, so
	// nothing can replace the sections a tagged build installed (section 5.5).
	ErrSectionHookSet = errors.New("digest: the extra-section hook is already set")
	// ErrSectionHookNil is a nil hook, which would take the one slot and print
	// no section at all: not registering is the same thing, and saying so is
	// better than a build that believes it registered (G10).
	ErrSectionHookNil = errors.New("digest: SetSections needs a hook")
)

// sectionHook is the registered hook, nil in a build that registered none.
var sectionHook func(Digest) []Section

// SetSections registers the extra-section hook, once per process: ee/register
// registers the tagged build's own at init, and a core caller has no reason to
// call it at all.
func SetSections(extra func(Digest) []Section) error {
	if extra == nil {
		return ErrSectionHookNil
	}
	if sectionHook != nil {
		return ErrSectionHookSet
	}
	sectionHook = extra
	return nil
}

// sectionsMarkdown is the hook's sections as the report's tail, or "" when
// there is nothing to print -- no hook, or a hook that returned none -- so a
// report of a build with no hook ends where it ended before.
//
// A section prints after the block the pattern rules read, because a later
// report's rules parse that block and a section between the period's figures
// and the rules would read as one of them.
func sectionsMarkdown(d Digest) string {
	if sectionHook == nil {
		return ""
	}
	var b strings.Builder
	for _, s := range sectionHook(d) {
		if s.Title == "" && s.Body == "" {
			continue
		}
		b.WriteString("\n")
		if s.Title != "" {
			fmt.Fprintf(&b, "## %s\n\n", s.Title)
		}
		if body := strings.TrimRight(s.Body, "\n"); body != "" {
			b.WriteString(body)
			b.WriteString("\n")
		}
	}
	return b.String()
}
