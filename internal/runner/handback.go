package runner

import (
	"fmt"
	"slices"
	"strings"
)

// A run's agent hands back what it left for the people who read its work
// after it: what it deferred, what it left risky, how it verified the
// change, how to undo it, what a hotfix owes the morning after, and the
// questions it could not settle itself (docs/rigor-modes/levels-spec.md §2,
// §6).
//
// The sections live in the agent's own hand-back file — pr.md for a first
// run, followup.md for a follow-up (followupFile) — and never in the task
// text: the runner's fallback body embeds the task under `## Task`
// (rawPRText), and a brief that happens to carry a `## Revert plan` heading
// would otherwise satisfy a check only the agent's own account may
// (docs/rigor-modes/levels-reconciliation.md §3b item 13). The parser is
// handed the redacted text (agent.Redact, as rawPRText applies it) and a
// flag saying whether that text is the agent's file at all, so the fallback
// can never be read as one.
//
// A level uses a subset of the six, so a section the file does not carry is
// empty rather than an error, and nothing here decides what a level does
// with them: LV4.3 files the entries as backlog items and LV5.1 reads the
// sections it requires, and both ask this parser only for what the file
// carried.

// The six sections, in the order the contract names them. The spelling is
// the one the follow-up prompt writes (internal/followup's
// SystemPromptLines) and the one a test in this package holds that prompt
// to; a heading matches one of them whatever its case, since the agent
// copies the names its prompt spells and a file that capitalised one
// differently still names the section its writer meant.
const (
	handBackDeferred      = "Deferred"
	handBackOpenRisks     = "Open risks"
	handBackVerification  = "Verification"
	handBackRevertPlan    = "Revert plan"
	handBackMorningNote   = "Morning note"
	handBackOpenQuestions = "Open questions"
)

// handBackHeadings is the six names as one list, which is what a heading is
// matched against.
var handBackHeadings = []string{
	handBackDeferred,
	handBackOpenRisks,
	handBackVerification,
	handBackRevertPlan,
	handBackMorningNote,
	handBackOpenQuestions,
}

// HandBack is the sections the agent's hand-back file carried, each with the
// entries it held and how many the bound left out.
type HandBack struct {
	Deferred      HandBackSection
	OpenRisks     HandBackSection
	Verification  HandBackSection
	RevertPlan    HandBackSection
	MorningNote   HandBackSection
	OpenQuestions HandBackSection
}

// HandBackSection is one section of a hand-back: the entries the agent wrote
// under it, in the order it wrote them, and Dropped, how many whole entries
// the section's bound left out.
//
// An entry is the agent's own text with its bullet marker removed, and it
// may run over several lines: the lines under the bullet are the entry's, so
// a deferred item whose reasons are a nested list is one entry and not
// several. The text is redacted before it reaches this package — the parser
// is handed what the runner may publish — so nothing here has to strip
// anything out of it.
type HandBackSection struct {
	Entries []string
	Dropped int
}

// parseHandBack reads the hand-back sections out of redacted, the text of
// the file the agent wrote. fromAgent says whether redacted is that file at
// all: when the runner fell back to the body built from the task text
// (rawPRText finds no pr.md, followupFile no followup.md) the hand-back is
// empty, whatever headings the task happens to carry, because only the
// agent's own account may answer what the agent left open.
//
// A section the text does not carry is empty, not an error — every level
// uses a subset of the six — while a section named twice is an error: two
// lists under one name are two answers to one question, and merging them
// would be the runner guessing which answer the agent meant.
func parseHandBack(redacted string, fromAgent bool) (HandBack, error) {
	if !fromAgent {
		return HandBack{}, nil
	}
	entries, err := handBackEntries(redacted)
	if err != nil {
		return HandBack{}, err
	}
	return HandBack{
		Deferred:      boundedHandBackSection(entries[handBackDeferred]),
		OpenRisks:     boundedHandBackSection(entries[handBackOpenRisks]),
		Verification:  boundedHandBackSection(entries[handBackVerification]),
		RevertPlan:    boundedHandBackSection(entries[handBackRevertPlan]),
		MorningNote:   boundedHandBackSection(entries[handBackMorningNote]),
		OpenQuestions: boundedHandBackSection(entries[handBackOpenQuestions]),
	}, nil
}

// handBackEntries reads the entries of every section the text carries, keyed
// by the section's name. Everything before the first heading is ignored: a
// first run's text is a pr.md body, which opens with whatever the agent put
// above its first section — the pull request's own title, quoted lines, a
// summary — and none of that is one of the six.
func handBackEntries(redacted string) (map[string][]string, error) {
	entries := make(map[string][]string, len(handBackHeadings))
	seen := make(map[string]bool, len(handBackHeadings))
	var (
		section string
		entry   []string
		inEntry bool
		col     int
	)
	// flush ends the entry being read, keeping it when it holds anything but
	// space: an empty bullet is a bullet and not an entry.
	flush := func() {
		if inEntry {
			if text := strings.TrimSpace(strings.Join(entry, "\n")); text != "" {
				entries[section] = append(entries[section], text)
			}
		}
		entry, inEntry, col = nil, false, 0
	}
	for _, line := range strings.Split(redacted, "\n") {
		if name, ok := handBackHeading(line); ok {
			flush()
			if seen[name] {
				return nil, fmt.Errorf("the hand-back names %q twice", name)
			}
			seen[name], section = true, name
			continue
		}
		if text, indent, ok := handBackBullet(line); ok {
			if section == "" {
				continue
			}
			// A bullet deeper than the one being read belongs to it — the
			// sub-points of a deferred item are part of that item, not items
			// of their own — while one at the same column or shallower is
			// the next entry.
			if inEntry && indent > col {
				entry = append(entry, strings.TrimRight(line, " \t\r"))
				continue
			}
			flush()
			entry, inEntry, col = []string{text}, true, indent
			continue
		}
		if inEntry {
			entry = append(entry, strings.TrimRight(line, " \t\r"))
		}
	}
	flush()
	return entries, nil
}

// handBackHeading returns the section a heading line opens, and whether it
// opens one at all. Only a level-two heading does — `##`, and the name after
// it — because that is what the contract writes: a deeper heading is part of
// the entry above it rather than a section of its own.
func handBackHeading(line string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimLeft(line, " \t"), "## ")
	if !ok {
		return "", false
	}
	name := strings.TrimSpace(rest)
	for _, h := range handBackHeadings {
		if strings.EqualFold(name, h) {
			return h, true
		}
	}
	return "", false
}

// handBackBullet returns the first line of an entry — a list bullet's text,
// its marker and the space after it removed — and the column its marker sits
// at, which decides whether the next bullet continues this entry or starts
// its own.
func handBackBullet(line string) (string, int, bool) {
	col := len(line) - len(strings.TrimLeft(line, " \t"))
	s := line[col:]
	if len(s) == 0 || (s[0] != '-' && s[0] != '*' && s[0] != '+') {
		return "", 0, false
	}
	// "-x" is a word with a dash in it, not a bullet: a marker needs the
	// space (or nothing at all) after it.
	if len(s) > 1 && s[1] != ' ' && s[1] != '\t' {
		return "", 0, false
	}
	return strings.TrimSpace(s[1:]), col, true
}

// A section's own bounds, both checked while the entries are kept in the
// order the agent wrote them: the count, so a runaway list — an agent that
// bullets every file it touched — stays a list something can be done with,
// and the bytes, so one section cannot swallow the published body it is
// quoted in (clipBody is that body's own outer bound). A hand-back of
// ordinary size meets neither.
const (
	maxHandBackEntries      = 100
	maxHandBackSectionBytes = 32 << 10
)

// boundedHandBackSection is one section as it is kept: the entries that fit,
// and how many whole entries the bound left out.
//
// An entry is never cut in half. A reader given half a deferred item, or a
// revert plan whose last step is missing, has been told something the agent
// did not say, and the record of the drop is what lets a body say the list
// is longer than the one it shows instead of quietly carrying less than the
// agent wrote.
func boundedHandBackSection(entries []string) HandBackSection {
	kept, size := 0, 0
	for _, e := range entries {
		if kept >= maxHandBackEntries || size+len(e) > maxHandBackSectionBytes {
			break
		}
		size += len(e)
		kept++
	}
	// Cloned so a caller cannot append into the section's own backing array
	// and change what a later reader of the same parse sees.
	return HandBackSection{Entries: slices.Clone(entries[:kept]), Dropped: len(entries) - kept}
}
