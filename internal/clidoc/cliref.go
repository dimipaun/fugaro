// Command clidoc generates the documents the website renders from the trees
// and files of this repository.
//
// Today it generates two: website/content/security.md from SECURITY.md
// (secdoc.go) and website/content/subscription-terms.md from the draft
// Subscription Terms (termsdoc.go).
//
// The CLI reference -- website/content/cli-reference.md -- used to be the
// third, and it is not generated here any more. The reference describes the
// binary that ships, and that binary is the tagged one, so it is written from
// the tagged command tree by `go run ./ee/clidoc` (design
// docs/design/ee-boundary-moves.md §9). Two generators writing one file would
// overwrite each other, whichever ran last, so this command no longer writes
// it at all. The renderer both generators read is internal/clidoc/cliref.
//
// Run it from the repository root:
//
//	go run ./internal/clidoc
package main
