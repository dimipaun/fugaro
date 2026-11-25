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
// with them: LV4.3 publishes the sections its level owes in the run's own
// account (handBackPart, internal/rigor's hand-back table) and LV5.1 reads
// the sections it requires, and both ask this parser only for what the file
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
// summary — and none of that is one of the six. A heading the six do not name
// ends the section being read, and what is inside a fence is the agent's text
// rather than the file's structure.
func handBackEntries(redacted string) (map[string][]string, error) {
	entries := make(map[string][]string, len(handBackHeadings))
	seen := make(map[string]bool, len(handBackHeadings))
	var (
		section string
		entry   []string
		inEntry bool
		inFence bool
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
		fence := handBackFence(line)
		if fence {
			inFence = !inFence
		}
		// A fence line is part of the entry it was written under, and so is
		// everything between the fences: a quoted `## Deferred` opens no
		// section and a quoted bullet starts no entry, so the quote stays the
		// text the agent wrote under the section it wrote it in.
		if fence || inFence {
			if inEntry {
				entry = append(entry, strings.TrimRight(line, " \t\r"))
			}
			continue
		}
		if name, level, ok := handBackHeading(line); ok {
			flush()
			// Only the contract's own heading opens a section. Any other one
			// closes the section being read: a level-two heading the six do not
			// name (`## Summary`, `## Testing`) and a level-one heading of the
			// document's own both leave what follows them outside the hand-back,
			// where reading it as the section above would file a run's summary
			// as its deferrals.
			section = ""
			if level == 2 {
				if canonical, isSection := handBackSection(name); isSection {
					if seen[canonical] {
						return nil, fmt.Errorf("the hand-back names %q twice", canonical)
					}
					seen[canonical], section = true, canonical
				}
			}
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

// handBackHeading returns the name a heading line carries and its level, and
// whether the line is a heading at all. Only a level-one or level-two heading
// is one — `#` or `##`, and the name after it, which is what a body writes for
// a section and its title. A deeper heading is part of the entry above it
// rather than a heading of its own: `### Deferred` names no section, and the
// text under it belongs to the entry that holds it.
func handBackHeading(line string) (string, int, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if rest, ok := strings.CutPrefix(trimmed, "## "); ok {
		return strings.TrimSpace(rest), 2, true
	}
	if rest, ok := strings.CutPrefix(trimmed, "# "); ok {
		return strings.TrimSpace(rest), 1, true
	}
	return "", 0, false
}

// handBackSection returns the section a heading's name is one of, whatever its
// case: the agent copies the names its prompt spells, and a file that
// capitalised one differently still names the section its writer meant.
func handBackSection(name string) (string, bool) {
	for _, h := range handBackHeadings {
		if strings.EqualFold(name, h) {
			return h, true
		}
	}
	return "", false
}

// handBackFence reports whether line opens or closes a fenced code block: the
// three backticks a body fences a quoted block with. What a fence holds is
// whatever the agent pasted — a prompt, a diff, a section it is comparing its
// own text against — and a heading or a bullet inside it is that text rather
// than this file's structure.
func handBackFence(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "```")
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
