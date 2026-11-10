package pluginwire

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxPrintable is the most Printable keeps of a string, in runes.
const MaxPrintable = 200

// Printable makes a string from a repository's files safe to print to a
// terminal: control characters (escape sequences) and invisible or
// direction-changing format characters (bidi overrides) become \uXXXX, and
// what is longer than MaxPrintable runes is cut, so a hostile file cannot
// fill a screen or the --json result.
func Printable(s string) string { return PrintableN(s, MaxPrintable) }

// PrintableN is Printable with its own rune limit instead of MaxPrintable,
// for a field already bounded at parse time to a different size (recipe.
// MaxUseWhen is bytes, so its rune count is never more than that byte
// count): it would be lossy to show less of it than the field's own limit
// already allows, and pointless to allow more.
func PrintableN(s string, maxRunes int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n++; n > maxRunes {
			b.WriteString("... (truncated)")
			break
		}
		if (unicode.IsControl(r) && r != '\t') || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ' || r == utf8.RuneError {
			fmt.Fprintf(&b, "\\u%04x", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Location is where a checkout's project settings are.
type Location struct {
	Root     string // the checkout's top directory
	Settings string // Root/.claude/settings.json, or the nearest one below Root
	Exists   bool   // whether Settings exists
}

// Locate finds the project settings for start: walking up, the nearest
// directory that holds .claude/settings.json, provided the walk then finds
// .git (a file in a worktree) at or above it; or else the top of the checkout,
// whose .claude/settings.json is the path a pin left by an older fugaro is in.
// The walk stops at the checkout's top, and the user's own home directory is
// never a project. ok is false outside a checkout, whatever settings files lie
// above.
func Locate(start string) (loc Location, ok bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return Location{}, false
	}
	home, _ := os.UserHomeDir()
	var found *Location
	for {
		if home != "" && sameDir(dir, home) {
			return Location{}, false
		}
		s := filepath.Join(dir, ".claude", "settings.json")
		if _, err := os.Lstat(s); err == nil && found == nil {
			found = &Location{Root: dir, Settings: s, Exists: true}
		}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			// A settings file counts only inside a checkout: .git at or above it.
			if found != nil {
				return *found, true
			}
			return Location{Root: dir, Settings: s}, true
		}
		up := filepath.Dir(dir)
		if up == dir {
			return Location{}, false
		}
		dir = up
	}
}

func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}
