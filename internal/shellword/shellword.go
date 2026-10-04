// Package shellword quotes a value as one shell word, for the commands fugaro
// prints to be pasted into a terminal.
package shellword

import (
	"regexp"
	"strings"
)

var safeRE = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,][A-Za-z0-9_./:@%+=,-]*$`)

// Quote is s as one shell word: unchanged when it is made of safe characters
// and cannot be read as an option (no leading '-'), else in single quotes. A
// branch or repository name may hold ; $ ( | & ' and more, and the text is
// printed to be copied into a shell.
func Quote(s string) string {
	if safeRE.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
