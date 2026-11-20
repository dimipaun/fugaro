package plan

// One task's section of a plan document, and the pin an orchestrator brief
// carries over it (D2737, D2932).
//
// Both live here rather than in internal/orchestrator because two programs read
// them: the daemon, which writes a brief that pins the hash of the section a
// person approved, and the review stage inside a run's own job, which reads
// that section back to check a pull request against the task it was written for
// (D2932). The job's binary does not link the orchestrator, and one parser is
// what keeps the two readings the same section — a second one beside it is a
// section the reviewer and the approver could disagree about.

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode/utf8"
)

// SectionDigest is the SHA-256 of the section of a plan document that describes
// one task, in the spelling Digest uses everywhere else, or false when the
// document carries no such section.
//
// The section is found by its heading: the first heading whose text names the
// task id, and everything under it down to the next heading at the same level
// or above. That is what a reader of a plan would call the task's section, and
// it is the whole of the rule — a plan that names its tasks in its own style
// still has one heading per task, and what the hash covers is that heading and
// the text beneath it rather than anything about the document around it.
func SectionDigest(doc, node string) (string, bool) {
	section, ok := Section(doc, node)
	if !ok {
		return "", false
	}
	sum := sha256.Sum256([]byte(section))
	return "sha256:" + hex.EncodeToString(sum[:]), true
}

// Section is that section as text: its heading's line through the last line
// before the next heading at the same level or above, with the blank lines
// after the last of them dropped and the whole ended by one newline.
//
// The lines inside a fenced code block are never read as headings. A `#` at
// column 0 in a shell example is a comment, and a section that ended at one
// would be hashed only down to the fence: everything below it — the rest of
// what the person approves — could then change with the digest standing still,
// which is exactly what D2745's re-import rule reads as an unchanged section
// and what the run's own read of the plan would disagree with. A fence is
// tracked rather than escaped because the plans this reads are Markdown, and a
// plan that shows a command is showing a `#`.
//
// The blank lines at the end are dropped because they are the whitespace before
// the next heading rather than anything the plan says, and a plan edited by
// adding a blank line to a section is the same section. The newline at the end
// is kept because every line of the text has one, the last included.
func Section(doc, node string) (string, bool) {
	lines := strings.Split(doc, "\n")
	start, level := -1, 0
	fence := ""
	for i, line := range lines {
		if marker, rest, ok := fenceAt(line); ok {
			switch {
			case fence == "":
				fence = marker
			case marker[0] == fence[0] && len(marker) >= len(fence) && strings.TrimSpace(rest) == "":
				// A closing fence carries no info string: a ```` ```go ```` inside
				// a fence is a line of the code it shows.
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		lvl, text, ok := atxHeading(line)
		if !ok {
			continue
		}
		if start < 0 {
			if planHeadingNames(text, node) {
				start, level = i, lvl
			}
			continue
		}
		if lvl <= level {
			return joinSection(lines[start:i]), true
		}
	}
	if start < 0 {
		return "", false
	}
	return joinSection(lines[start:]), true
}

// joinSection is those lines as the hash reads them.
func joinSection(lines []string) string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return strings.Join(lines[:end], "\n") + "\n"
}

// fenceAt reads a line as a code-fence delimiter: the marker it is written with
// — three or more backticks or tildes, indented by at most three spaces, which
// is what makes it a fence rather than indented code — and whatever follows the
// marker. It reports false for a line that is not one.
//
// It does not decide whether the line opens a fence, closes the one in force or
// is a line of code: that is the walk's, which is the only place the state is.
func fenceAt(line string) (string, string, bool) {
	s := line
	for i := 0; i < 3 && strings.HasPrefix(s, " "); i++ {
		s = s[1:]
	}
	if len(s) < 3 || (s[0] != '`' && s[0] != '~') {
		return "", "", false
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	if n < 3 {
		return "", "", false
	}
	return s[:n], s[n:], true
}

// atxHeading reads a line as an ATX heading: how many `#` it opens with and the
// text after them, or false for a line that is not one. A heading is unindented
// and followed by a space or a tab.
//
// A `#` at column 0 in a fenced example is a heading to this function, because
// nothing on the line says otherwise. Keeping code out of the walk is the
// reader's job and not this one's (Section), which is why this is a rule about
// one line rather than about a document.
func atxHeading(line string) (int, string, bool) {
	i := 0
	for i < len(line) && i < 7 && line[i] == '#' {
		i++
	}
	if i == 0 || i > 6 || i >= len(line) || (line[i] != ' ' && line[i] != '\t') {
		return 0, "", false
	}
	return i, strings.TrimSpace(line[i:]), true
}

// planHeadingNames reports whether a heading's text names one task. The task id
// is the heading's first word — what precedes a separator or the end of the
// heading — so `### G1: the import` is G1's section and `### G10: ...` is not.
func planHeadingNames(text, node string) bool {
	rest, ok := strings.CutPrefix(text, node)
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	switch r {
	case ' ', '\t', ':', ',', '.', ';', '—', '–':
		return true
	}
	return false
}

// DocumentPath is the plan document a graph artifact describes: ArtifactPath's
// pairing read the other way. The extension ArtifactPath replaced is not
// recorded anywhere, so the document is the artifact's stem with Markdown's
// extension — every plan this reads is Markdown, and the section parser reads
// Markdown headings.
func DocumentPath(artifactPath string) string {
	return strings.TrimSuffix(artifactPath, ArtifactSuffix) + ".md"
}

// Pin is what an orchestrator brief pins (D2737): the plan it names, the node
// whose section, and the commit and section digest the brief was written at.
type Pin struct {
	Plan   string
	Node   string
	Commit string
	Digest string
}

// briefHeadRE matches the head line PlanBrief writes, read for the two names
// the pin carries beside the commit and the digest: "# Task: <plan> <node>: ".
var briefHeadRE = regexp.MustCompile(`^# Task: (\S+) (\S+): `)

// briefPinRE matches the pin sentence PlanBrief writes. The commit is what
// git reports (7 to 64 hex digits), and the digest is in the spelling
// SectionDigest answers in.
var briefPinRE = regexp.MustCompile(` at commit ([0-9a-f]{7,64}), whose SHA-256 is (sha256:[0-9a-f]{64}): `)

// BriefPin reads a brief that is an orchestrator pointer (internal/orchestrator's
// PlanBrief) back into its four pins, or false for a brief that is not one — a
// text a person wrote, or one an earlier release wrote before the section was
// hashed. It is how a reader of a task text that is a pointer knows which plan
// and node the section belongs to, and it is the whole of what such a text
// tells it: the plan is matched against the plans the revision really holds
// (IDFromPath), and the path the text spells is never read as one (D2932).
func BriefPin(brief string) (Pin, bool) {
	head := briefHeadRE.FindStringSubmatch(brief)
	if head == nil {
		return Pin{}, false
	}
	pin := briefPinRE.FindStringSubmatch(brief)
	if pin == nil {
		return Pin{}, false
	}
	return Pin{Plan: head[1], Node: head[2], Commit: pin[1], Digest: pin[2]}, true
}
