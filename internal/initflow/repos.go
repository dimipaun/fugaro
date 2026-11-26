package initflow

// How a stage tells the run about each repository it read: one line per
// repository, and the stage's own answer to the loop when it is asked.
//
// The type lives here and not in the CLI because the loop reads the lines off
// a stage and carries them into the result and the event stream, and
// initflow cannot import the CLI (F35). A stage builds its lines through the
// CLI's newRepoLine, which sanitises them (C1, R12).

// RepoLine is one stage's line about one repository: the repository (its
// canonical owner/name), how the stage left it, and one line of detail a
// person reads.
type RepoLine struct {
	Repo   string    `json:"repo"`
	State  RepoState `json:"state"`
	Detail string    `json:"detail,omitempty"`
}

// RepoState is where a stage left one repository. The four words are the
// output's, and they are deliberately not State's: a stage is done, changed
// or skipped as a whole, while one of its repositories can be done with one
// and needs-you with another.
type RepoState string

const (
	// RepoDone: the stage's work for this repository is done, or it had
	// nothing to do.
	RepoDone RepoState = "done"
	// RepoNeedsYou: only the owner can take the next step, and Detail says
	// which and how.
	RepoNeedsYou RepoState = "needs you"
	// RepoUnread: the repository could not be read, and Detail carries the
	// reason and the fix. A stage never guesses at one of these.
	RepoUnread RepoState = "unread"
	// RepoSkipped: the stage does not apply to this repository, and Detail
	// says why.
	RepoSkipped RepoState = "skipped"
)

// RepoReporter is a stage that reports one line per repository. The loop
// reads the lines of a stage that implements it and carries them into the
// stage's result and its event (U13); a stage that reports nothing does not
// implement it.
type RepoReporter interface {
	RepoLines() []RepoLine
}

// StageNote is a stage that has one line of its own to add to the result the
// loop settles about it, beside the lines it reported: what it did not run
// here, and why. The lines the stage reports say how each repository was left
// and never which step was dropped for one of them, so a stage whose step was
// skipped for a repository has no other place to say it — and the loop puts
// the note after the lines' summary, so the run's own line for the stage
// carries both ("1 repository: 1 unread; skipped here: acme/app: cloud (…)").
//
// A stage with no such line does not implement it.
type StageNote interface {
	Note() string
}
