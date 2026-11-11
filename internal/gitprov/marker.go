package gitprov

import (
	"regexp"
	"strings"
)

// Every comment Fugaro posts on a pull request ends with a marker naming
// the run that posted it, so a later follow-up can tell Fugaro's own
// comments apart and see which run last updated the pull request.
const (
	markerPrefix = "<!-- fugaro:report run="
	// transitionPrefix opens the second kind of marker, the one a ladder
	// transition comment ends with (E8, E10a): it names the run that wrote
	// the comment AND the rung the item moved to, so the comment is
	// Fugaro's own — a transition an agent's quoted text carries is not —
	// and a reader that sees only the marker still knows the rung.
	transitionPrefix = "<!-- fugaro:escalation run="
	markerSuffix     = " -->"
	// legacyHeading opens the report of a run from before markers existed.
	legacyHeading = "### Fugaro run"
	// legacyNotReady opens the not-ready note of a run from before
	// markers existed; it doesn't name its run.
	legacyNotReady = "**Fugaro:** this pull request is not ready"
)

var (
	// markerRE is a marker as ReportMarker writes it, with a well-formed
	// run ID, so FugaroRun never returns an ID that could escape a path.
	markerRE = regexp.MustCompile(`<!-- fugaro:report run=([0-9]{8}-[0-9]{6}-[0-9a-f]{4}) -->`)
	// legacyHeadingRE is the old report's first line.
	legacyHeadingRE = regexp.MustCompile("^### Fugaro run `([0-9]{8}-[0-9]{6}-[0-9a-f]{4})`")
	// transitionRE is a transition marker as TransitionMarker writes it. Its
	// rung is the ladder's own spelling (R0-R4), so a body naming a rung the
	// ladder has no name for is not a transition this code reads.
	transitionRE = regexp.MustCompile(`<!-- fugaro:escalation run=([0-9]{8}-[0-9]{6}-[0-9a-f]{4}) rung=(R[0-4]) -->`)
	// anyMarkerRE is looser than markerRE: StripMarkers removes anything
	// that looks like a marker of either kind, whatever its spacing, case or
	// run ID.
	anyMarkerRE = regexp.MustCompile(`(?is)<!--\s*fugaro:(?:report|escalation).*?-->`)
)

// ReportMarker is the marker that ends every comment run runID posts.
func ReportMarker(runID string) string { return markerPrefix + runID + markerSuffix }

// TransitionMarker is the marker that ends the comment the ladder posts when
// an item moves to rung: the run that wrote it and the rung it moved to.
// rung is one of the ladder's own names (R0-R4); a marker carrying anything
// else is not one TransitionOf reads.
func TransitionMarker(runID, rung string) string {
	return transitionPrefix + runID + " rung=" + rung + markerSuffix
}

// TransitionOf reports whether body is a ladder transition comment Fugaro
// posted, naming the run that posted it and the rung it moved the item to.
// The last transition marker in the body wins, for FugaroRun's reason.
func TransitionOf(body string) (runID, rung string, ok bool) {
	if ms := transitionRE.FindAllStringSubmatch(body, -1); len(ms) > 0 {
		last := ms[len(ms)-1]
		return last[1], last[2], true
	}
	return "", "", false
}

// FugaroRun reports whether body is a comment Fugaro posted, and which run
// posted it. The last marker in the body wins (a report quoting the
// agent's text puts its own marker after it), and it is the last marker of
// EITHER kind: a ladder transition comment is Fugaro's own and names the
// run that wrote it (E8), and a body that quotes a transition marker
// earlier cannot hand its reader another run. Without a marker, a body
// opening with the old report heading names its run, and one opening with
// the old not-ready note is Fugaro's with an unknown run (""). Whether the
// comment is really Fugaro's is the caller's call: anyone can type a marker.
func FugaroRun(body string) (runID string, ok bool) {
	if id, found := lastMarkerRun(body); found {
		return id, true
	}
	return legacyReportRun(body)
}

// ReportRun reports whether body is a run's REPORT — the comment a run posts
// when it finishes — and which run posted it. It is deliberately narrower than
// FugaroRun, which answers for every kind of comment Fugaro posts: a ladder
// transition comment is Fugaro's own and names the run that wrote it (E10a),
// but it is not a report, and a caller asking "has THIS run already posted its
// report" (the runner's before-posting check) must not be answered yes by one.
// A transition write can name a run that has posted nothing, so the two
// questions have to be two functions.
//
// It reads the marker ReportMarker writes, and, for a report from before
// markers existed, the heading such a report opened with (and the older
// not-ready note, which names no run).
func ReportRun(body string) (runID string, ok bool) {
	if ms := markerRE.FindAllStringSubmatch(body, -1); len(ms) > 0 {
		last := ms[len(ms)-1]
		return last[1], true
	}
	return legacyReportRun(body)
}

// legacyReportRun reads a report written before markers existed: one opening
// with the old heading names its run, and one opening with the old not-ready
// note is Fugaro's with an unknown run ("").
func legacyReportRun(body string) (runID string, ok bool) {
	start := strings.TrimLeft(body, " \t\r\n")
	if m := legacyHeadingRE.FindStringSubmatch(start); m != nil {
		return m[1], true
	}
	if strings.HasPrefix(start, legacyNotReady) {
		return "", true
	}
	return "", false
}

// lastMarkerRun finds the run ID of the last marker of either kind in body.
// One search over both expressions rather than one after the other, because
// "the last marker wins" is a rule about the body and not about which kind is
// looked for first: a report quoting a transition marker must not be read as
// the transition's.
func lastMarkerRun(body string) (runID string, found bool) {
	at := -1
	for _, re := range [...]*regexp.Regexp{markerRE, transitionRE} {
		for _, loc := range re.FindAllStringSubmatchIndex(body, -1) {
			if loc[0] >= at {
				at, runID, found = loc[0], body[loc[2]:loc[3]], true
			}
		}
	}
	return runID, found
}

// StripMarkers removes every marker and every line starting with the
// report heading from s, so text Fugaro quotes (an agent's answer) can't
// forge a report or name another run.
func StripMarkers(s string) string {
	// Removing one marker can join the text around it into another, so
	// repeat until nothing changes; each pass shortens s, so it ends.
	for {
		t := anyMarkerRE.ReplaceAllString(s, "")
		if t == s {
			break
		}
		s = t
	}
	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimLeft(l, " \t"), legacyHeading) {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

// SameCommit reports whether a and b name the same commit: equal, or one
// an abbreviation of the other. Both must be at least 7 hex digits, so an
// empty or too-short value never matches.
func SameCommit(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if !isHex(a) || !isHex(b) || len(a) < 7 || len(b) < 7 {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return strings.HasPrefix(b, a)
}

func isHex(s string) bool {
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}
