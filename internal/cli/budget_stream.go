package cli

import "io"

// budget show's --stream feed. The document `budget show --json` prints is a
// list of per-repository rows, the project's own figures beside them and the
// project's kill switch in the global block -- and the project's kill switch is
// a read of its own, the config node, so it is a row of the feed's own rather
// than a member a consumer waits for the whole document to read (D2906, O3).
//
// The rows are written through the one writer the CLI's line-delimited
// documents share (runs_stream.go): a `row` line per repository, each carrying
// one element of the document's own `repos` array unchanged, then a `row` line
// carrying the project's kill switch, then the `end` line. Nothing is buffered
// -- each line is one write -- so a consumer drawing the caps table appends a
// repository as its line arrives instead of holding the document.
//
// The reads behind the rows do not arrive one repository at a time: every row
// draws on the same four whole-node reads of the database (the config, the
// project's counters, the repositories' counters, the agent registry) and, with
// --compute, on one listing of the runs bucket, and all of them complete before
// the first row can be built -- a repository's headroom is a fact about the
// project's counters as much as about its own. What the flag buys a consumer is
// the same thing it buys one from `runs ls --stream`: a document it renders as
// its lines arrive rather than one it must hold whole, and an `end` line that
// tells a short list from a read that was interrupted.

// budgetStreamDocVersion is the version of the feed's own document: the shape
// of the lines `budget show --stream` writes, which the `end` line carries for
// a reader to check before it reads a feed.
//
// It is not the caps document's version, because the caps document has none to
// carry: the document `budget show --json` prints without --stream is unchanged
// by this flag, member for member and byte for byte, so no member -- a version
// among them -- could be added to it. The feed is a document of its own -- the
// rows are the caps document's, the line shape is runs_stream.go's -- and its
// first version is 1, the same first version RS1's runs feed carries. A reader
// that meets an `end` line with no version at all (an older or a different
// writer's feed) reads the feed as finished either way: the line's presence is
// what says the command completed, and its figures are annotations of it.
const budgetStreamDocVersion = 1

// budgetKillRowSlug is the row of the project's own kill switch. A repository's
// row is identified by its slug, which budget.Key's slugs always spell with a
// provider prefix (`github/acme/app`), so the word is a scope no repository's
// slug can be and a consumer tells the two kinds of row apart by it.
const budgetKillRowSlug = "kill"

// budgetKillRow is the project's own kill switch as a row of the feed: the one
// thing about the project's caps that is read on its own, and the row a
// repository's own kill switch is not -- that one is a member of the
// repository's row, as it is a member of its element of the document.
//
// Kill is null rather than absent when the switch is off: a consumer reads the
// row's presence as "this is the project's switch" and null as "it is not on",
// where a row that vanished would leave it unable to tell an unfired switch
// from a feed that never carried one.
type budgetKillRow struct {
	Slug string    `json:"slug"`
	Kill *showKill `json:"kill"`
}

// streamBudgetRows writes the caps listing as a --stream feed: one row line per
// repository, in the document's own order (by slug), then the project's kill
// switch as a row of its own, then the end line. The rows are the document's
// rows, so a feed of a listing and the document of that listing carry the same
// repositories under the same members.
//
// What the feed does not carry is the rest of the document: the project's own
// counters, the defaults, the mode, the runs in flight and the warnings are not
// rows, and this feed's end line carries a count and a shape version and none
// of the index facts a listing of runs puts there (streamEndLine). A consumer
// that needs them reads the whole document; the feed is the caps table drawn as
// it arrives.
func streamBudgetRows(w io.Writer, rows []showRepo, kill *showKill) error {
	sw := newStreamWriter(w)
	for _, r := range rows {
		if err := sw.row(r); err != nil {
			return sw.failed(err)
		}
	}
	if err := sw.row(budgetKillRow{Slug: budgetKillRowSlug, Kill: kill}); err != nil {
		return sw.failed(err)
	}
	if err := sw.end(budgetStreamDocVersion, nil); err != nil {
		return sw.failed(err)
	}
	return nil
}
