package gitprov

import (
	"errors"
	"strings"
	"testing"
)

const (
	fullSHA = "0123456789abcdef0123456789abcdef01234567"
	abbrev  = "0123456789ab"
	other   = "fedcba9876543210fedcba9876543210fedcba98"
)

// TestCheckExpectedHead pins the comparison every head-bound call makes (G2).
// It is deliberately the same tolerance SameCommit has and no more: a host may
// report an abbreviated head, and a call that carries an empty or malformed one
// is refused rather than treated as carrying no expectation.
func TestCheckExpectedHead(t *testing.T) {
	for _, c := range []struct {
		name          string
		current, want string
		refused       bool
	}{
		{"the commit asked for", fullSHA, fullSHA, false},
		{"an abbreviation of it", abbrev, fullSHA, false},
		{"the full form of an abbreviated head", fullSHA, abbrev, false},
		{"another commit", other, fullSHA, true},
		{"no head at all", "", fullSHA, true},
		{"nothing asked for", fullSHA, "", true},
		{"both empty", "", "", true},
		{"too short to name a commit", "012345", "012345", true},
		{"not a commit", "not-a-sha", fullSHA, true},
	} {
		err := CheckExpectedHead(c.current, c.want)
		if c.refused && !errors.Is(err, ErrHeadMoved) {
			t.Errorf("%s: CheckExpectedHead = %v, want ErrHeadMoved", c.name, err)
		}
		if !c.refused && err != nil {
			t.Errorf("%s: CheckExpectedHead = %v, want nil", c.name, err)
		}
	}
}

// TestCheckExpectedHeadNamesBothCommits keeps the refusal readable in a log:
// the sentence has to say which commit the pull request is at and which one the
// call named, or an operator cannot tell a push from a wrong record.
func TestCheckExpectedHeadNamesBothCommits(t *testing.T) {
	err := CheckExpectedHead(other, fullSHA)
	msg := err.Error()
	for _, want := range []string{other, fullSHA} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal %q does not name %s", msg, want)
		}
	}
	if msg := CheckExpectedHead("", fullSHA).Error(); !strings.Contains(msg, "no commit") {
		t.Errorf("a pull request with no head reads as %q", msg)
	}
}
