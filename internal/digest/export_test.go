package digest

// SetCore restores the digest an untagged build has: no hook registered, so
// Markdown prints the report this package printed before the seam existed. It
// lives here, and not beside the seam, because a seam that switches behaviour
// off must not be reachable from production -- a core caller could otherwise
// clear the sections a tagged build registered (design ee-boundary-moves
// section 5.5, G10). This file is compiled into this package's tests only.
func SetCore() { sectionHook = nil }
