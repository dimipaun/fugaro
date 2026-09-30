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
	markerSuffix = " -->"
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
	// anyMarkerRE is looser than markerRE: StripMarkers removes anything
	// that looks like a marker, whatever its spacing, case or run ID.
	anyMarkerRE = regexp.MustCompile(`(?is)<!--\s*fugaro:report.*?-->`)
)

// ReportMarker is the marker that ends every comment run runID posts.
func ReportMarker(runID string) string { return markerPrefix + runID + markerSuffix }

// FugaroRun reports whether body is a comment Fugaro posted, and which run
// posted it. The last marker in the body wins (a report quoting the
// agent's text puts its own marker after it); without one, a body opening
// with the old report heading names its run, and one opening with the old
// not-ready note is Fugaro's with an unknown run (""). Whether the comment
// is really Fugaro's is the caller's call: anyone can type a marker.
func FugaroRun(body string) (runID string, ok bool) {
	if ms := markerRE.FindAllStringSubmatch(body, -1); len(ms) > 0 {
		return ms[len(ms)-1][1], true
	}
	start := strings.TrimLeft(body, " \t\r\n")
	if m := legacyHeadingRE.FindStringSubmatch(start); m != nil {
		return m[1], true
	}
	if strings.HasPrefix(start, legacyNotReady) {
		return "", true
	}
	return "", false
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
