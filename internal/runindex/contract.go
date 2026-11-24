// Package runindex is the index of runs the console's list views read: one
// Firestore document per run, one summary document per installation, and the
// Reader that pages them with the RTDB overlay merged in.
//
// It exists because the reads those views make are history-linear today: `runs
// ls` lists every run in its window, `plan status` reads 90 days of run objects
// and `runs stats` reads up to 5,000 of them (F1, F2, F32). The index turns
// each of them into a bounded number of requests, and it is written by the
// history job's sweep alone -- the one account that already holds
// `datastore.user` and reads the runs bucket, so no new IAM (D3201, F8).
//
// This file is the contract and nothing else: the shapes the writer, the
// readers and the Terraform indexes are all written against, with the rules
// they carry stated in docs/console-fixes/contract-index.md. The reader the
// commands compile against until the real one lands is stub.go; the real one,
// the fold that fills the documents, the lease and the reindex are their own
// tasks (RI3, RI4, RI5).
//
// Two rules are what the shapes are shaped by. **A field never changes
// meaning**: the entry carries the run's own records whole rather than a
// selection of them, so a field the record gains is a field the index carries
// without anyone editing this file, and a row a reader builds from an entry is
// the row it built from the bucket. **Nothing here decides anything**: an entry
// is a fold of what a run's own records say, so the values that come out of a
// run's own service account (its title, its plan task, its launcher) are
// display text -- shape-checked and sanitised where they are read, never
// trusted here.
package runindex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/estimate"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/tier"
)

// Version is the index's own schema version, carried by the summary document
// and by nothing else: the summary is the one document a reader reads whole
// and acts on (its counts, its completeness and the estimate table), and the
// history job's image may lag the CLI a person runs, so a reader has to be
// able to tell a summary this release wrote from one it cannot read. The
// entry needs no version of its own: it carries the run's own records, whose
// versions are the record's and the launch's.
const Version = 1

// The two collections the index lives in, named as contract-index.md fixes
// them. They are declared here because a reader and the writer are not always
// the same release -- the history job's image may lag the CLI a person runs --
// and a collection name one of them spells differently is a reader that finds
// nothing where the writer put it.
const (
	// EntryCollection holds one Entry per run, the document id being the run id.
	EntryCollection = "runIndex"
	// SummaryCollection and SummaryDocument name the installation's one summary
	// document, `runIndexMeta/summary`.
	SummaryCollection = "runIndexMeta"
	SummaryDocument   = "summary"
)

// Entry is one run's document, `runIndex/<run id>`.
//
// The document id is the run id, which is what makes the index one collection
// rather than one per repository: a run id is minted with the moment of its
// launch (task.NewRunID) and two repositories of one installation do not mint
// the same one, so a point read needs no slug beside it. Slug is a field
// because a page is read per repository and the display name is not the
// storage key.
//
// The fields above the two record blocks are the index's own: the keys it is
// queried, ordered and joined by, and the few facts of the run's task a
// listing prints, which the task itself is too large to carry (its prompt).
// They are written by the same fold write that carries the records, from those
// same records, and the records stay the authority for everything a row
// prints: a reader that wants to show a run reads Record and Launch, and one
// that wants to filter, sort or group reads a projection.
type Entry struct {
	// RunID is the run's id, and the document's own id.
	RunID string `json:"run_id"`
	// Slug is the repository's storage key, the name the runs bucket holds it
	// under (`runs/<slug>/`). Repo is its display name, which is the task's
	// own "owner/name" and falls back to the slug for a run whose task nothing
	// recorded.
	Slug string `json:"slug"`
	Repo string `json:"repo,omitempty"`
	// UpdatedAt is when the run last changed, as the fold read it: the record's
	// own finish where it has one, else its start, else the launch's moment,
	// else the run id's mint time (runstore.RunTime). The last fallback is what
	// a run that was claimed and never launched has -- a task.json and nothing
	// else -- and it is not optional: an entry with no sort key sorts nowhere,
	// so a page that has no window to order by could not place it.
	// It is the sort key of a page with no window, and it is the only field of
	// the entry the writer moves forward on its own -- a run's records do not
	// change after the run ends, so a settled run's entry is written once.
	UpdatedAt time.Time `json:"updated_at"`

	// Status is the run's settled status (runstore.Statuses), and is absent
	// for a run whose record settled none: an unlaunched run, and one that
	// died before finalize. The overlay is what says such a run is alive
	// (D3202); this is what the run's own record says it ended as.
	Status runstore.Status `json:"status,omitempty"`
	// PlanTask is the plan task the run works on, from its launch record
	// (runstore.Launch.PlanTask, task.Spec.PlanTask at launch): the join key
	// `plan status` and `graph` match a run to a node by. It is the same
	// display-only hint runview.Row carries, with the same rule -- it decides
	// nothing, and a reader shape-checks it (task.PlanTaskProblem) before it
	// is shown or joined.
	PlanTask string `json:"plan_task,omitempty"`
	// PR is the pull request number the run's record names, else its task's
	// (runview.Row.PR): what `--pr` filters by and what a pull request's runs
	// are grouped by.
	PR int `json:"pr,omitempty"`
	// Branch is the branch the run is on: the record's, else its task's
	// (runview.Row.Branch), the same order coordinatorRun applies. It is
	// projected beside the records rather than read from Record alone because
	// a run whose record is absent or unreadable still carries the branch its
	// task named, and the view derives a record's release from it
	// (coordinator.finish): a reader of the index that took the branch from
	// the record would draw that run differently from the bucket read.
	Branch string `json:"branch,omitempty"`
	// RequestedBy is the identity the run's task names, and LaunchedBy the
	// identity its launch record names. They are two facts and not one: the
	// task is written by whoever filed it and the launch by whoever started
	// it, and `runs stats` groups by the launcher the run recorded (its own
	// rule, userstats.IdentityOf) while `--mine` keeps the runs a task asked
	// for. Both are untrusted display text, sanitised where they are read.
	RequestedBy string `json:"requested_by,omitempty"`
	LaunchedBy  string `json:"launched_by,omitempty"`
	// Batch is the batch the run's task belongs to, and Tier the expectation
	// tier the run was dispatched at (runstore.TierRecord.Tier): `--batch`
	// filters by the first, and the estimate table groups by the second with
	// Rigor.
	Batch string    `json:"batch,omitempty"`
	Tier  tier.Tier `json:"tier,omitempty"`
	// Rigor is the level the run resolved (runstore.RigorRecord.LevelOf), not
	// the block: the estimate table groups by it, and the block a row prints
	// is in the record beside it.
	Rigor rigor.Rigor `json:"rigor,omitempty"`
	// StartedAt and FinishedAt are the run's own window, from its record, and
	// are what the estimate table's durations are measured from. FinishedAt is
	// absent for a run that has not finished, and StartedAt is absent for one
	// that never started: the record's zero start is written as none, because
	// a listing prints "not started" for a zero and no reader should have to
	// know that the zero time is the way it is said.
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	// Workflow, Title, Recipe, OAuthAccount, PreviousRun, FollowUp and
	// FollowUpOf are the run's task as a listing prints it, joined with the
	// record the way runview.Join joins them: the record's value where it has
	// one, the task's otherwise. The task itself is not here -- its prompt is
	// the largest thing about a run and no reader of the index shows it.
	Workflow     string           `json:"workflow,omitempty"`
	Title        string           `json:"title,omitempty"`
	Recipe       string           `json:"recipe,omitempty"`
	OAuthAccount string           `json:"oauth_account,omitempty"`
	PreviousRun  string           `json:"previous_run,omitempty"`
	FollowUp     bool             `json:"follow_up,omitempty"`
	FollowUpOf   *task.FollowUpOf `json:"follow_up_of,omitempty"`

	// Launch and Record are the run's own two records, whole, exactly as the
	// run's objects carry them (runstore.Launch and runstore.Record, decoded
	// with their own strict readers). They are the reason the index can stand
	// in for the bucket read: everything a row prints that is not a fact of
	// the moment -- the cost block with its notional split, the changed files,
	// the owner decisions, the rigor and tier blocks, the artifacts, the halt,
	// the reason, the branch and the pull request -- is here, and a field
	// either record gains later arrives without this file changing.
	//
	// A record that could not be read is absent rather than zero: a run whose
	// result.json is corrupt and one that never finalized are the same absence
	// here, and a reader that has to tell them apart reads the bucket (the
	// listing's own "record unreadable" row does, and it is not this reader's
	// job to invent one).
	Launch *runstore.Launch `json:"launch,omitempty"`
	Record *runstore.Record `json:"record,omitempty"`
}

// Summary is the installation's one summary document,
// `runIndexMeta/summary`.
//
// It is the document every reader that needs a figure rather than a page
// reads: the totals `runs ls` prints beside its rows, the completeness every
// listing has to know before it prints a short list, and the estimate table
// `plan status` draws from (F13, F15). The sweep writes it as deltas -- each
// folded run's state change moves the counts and the money by its own figures
// -- and a reindex recomputes it from the index documents (RI4, RI5).
type Summary struct {
	Version int `json:"version"`
	// Complete is true once a walk has reached the end of history (RI5,
	// D3215). Every reader that prints a list prints the fact when it is
	// false: a short list that does not say why reads as the whole truth, and
	// a console that drew it would show a person a project with three runs.
	Complete bool `json:"complete"`
	// FoldedThrough is the moment the index is folded through: every run that
	// changed after it is missing from the index and comes from the overlay
	// (D3202). It is absent before the first fold, and a reader that has none
	// reads the overlay alone. It moves only on a fold that succeeded, so a
	// failed fold leaves the stamp where it was and the next sweep re-reads
	// what it could not fold.
	FoldedThrough *time.Time `json:"folded_through,omitempty"`
	// UpdatedAt is when the summary was last written.
	UpdatedAt time.Time `json:"updated_at"`
	// Runs is every run the index carries, and States counts them by how they
	// ended: one key per runstore.Statuses value, every key present so a
	// reader reads one without a nil check. A run whose record settled no
	// status is in Runs and in no state -- Runs is at least the sum of States
	// -- and a state count is never negative, because a figure below zero is
	// not a count.
	Runs   int64            `json:"runs"`
	States map[string]int64 `json:"states"`
	// SpentMicros is the model money the caps count and NotionalMicros the
	// part of it a subscription paid (counted toward a plan's limits, nobody's
	// bill): the two halves of the run's model figure, summed, which are never
	// added to one another (P12, D140). They are integer micro-dollars, the
	// unit the deltas are applied in and the unit `runs summary` already
	// publishes, so a fold moves them by an exact amount rather than by a
	// float that drifts.
	SpentMicros    int64 `json:"spent_micros"`
	NotionalMicros int64 `json:"notional_micros"`
	// ComputeMicros is what the runs whose compute was priced came to, and is
	// absent when no run's compute is known: "not estimated" is not "free".
	// ComputeRuns says how many runs the figure covers, and
	// ComputeNotEstimated how many carry no figure at all.
	ComputeMicros       *int64 `json:"compute_micros,omitempty"`
	ComputeRuns         int64  `json:"compute_runs"`
	ComputeNotEstimated int64  `json:"compute_not_estimated"`
	// Estimate is the table `plan status` reads instead of scanning history
	// (F15), in estimate.Table's own shape: the same document `fugaro plan
	// status` caches, so the two cannot drift into two spellings of one
	// history. Absent before the first fold -- a plan with no table says "no
	// history yet", never a zero duration.
	Estimate *estimate.Table `json:"estimate,omitempty"`
}

// Filter is one page's question.
//
// Every member is an equality or a bound the index serves with its own
// composite index (contract-index.md), and the reader answers all of them
// together: a filter applied after the page was cut would print a page shorter
// than the caller asked for, which is the one thing this whole package exists
// to stop.
type Filter struct {
	// Slugs restricts the page to these repositories, by storage slug; empty
	// is every repository of the installation. It is what a listing in a
	// checkout sets (the local config's repositories).
	Slugs []string
	// Since is the earliest moment a run may have started at, inclusive -- the
	// run id's own mint time (runstore.RunTime), not UpdatedAt; the zero time
	// is no bound. It is `runs ls --since` and `runs stats --since` ("only runs
	// started within this long"), and it bounds the read by the window rather
	// than by history: with it, every run of the window is listed (D3203).
	//
	// It is also the page's order: a window is the run's start, so a windowed
	// page is ordered by the run id and stops at the first run older than the
	// window, exactly as the bucket read it replaces lists one
	// (runstore.ListRunIDs). The two reads of a window then hold the same runs
	// in the same order, which is what makes `index_complete` a statement about
	// where the figures came from and not about which runs they are over.
	Since time.Time
	// States keeps the runs that ended in one of these statuses; empty keeps
	// every run. It is `runs ls --state`.
	States []runstore.Status
	// PlanTask keeps the runs of one plan task. It is how `plan status` and
	// `graph` join a plan's tasks to the runs that worked on them.
	PlanTask string
	// PR keeps the runs of one pull request number, as the run's record or its
	// task names it. It is `runs ls --pr`, which reads the index rather than
	// the bucket (D3204).
	PR int
	// Mine keeps the runs one identity asked for, compared with the run's task
	// as RequestedBy carries it. It is `runs ls --mine`.
	Mine string
	// Batch keeps the runs of one launch batch. It is `runs ls --batch`.
	Batch string
	// Active keeps the runs that have not ended, whatever their age: the
	// second half of the default listing (D3203). A run is active when its own
	// status is running, or when the overlay says a run of that key is alive
	// and its entry is older than the overlay's own account of it -- which is
	// what makes a run that finished after the last fold still listable.
	Active bool
	// Limit caps the page; 0 is every match. The reader pages internally to
	// answer it, so a limit larger than one Firestore page costs more requests
	// but never a wrong answer, and a caller that wants one page of a fixed
	// size passes that size.
	Limit int
	// After starts the page after one run: the run id the caller was given,
	// which the reader resolves to that run's sort key -- from its index
	// document, or from the overlay when it is a run the index does not carry
	// yet (IR1). It is a run id rather than a token because `runs ls --after`
	// is allow-listed as one by the console, and a caller pages by handing
	// back the last run id it printed. An unknown run id is an error and never
	// an empty page: silently starting from the beginning would repeat rows
	// for ever.
	After string
}

// Cursor is a position a walk resumes from.
//
// It is one type for the two walks the index has, because both are the same
// thing said at different granularities: the page a reader printed, which
// continues from the sort key of its last row, and the reindex walk, which
// resumes from the last run it upserted (F39). A walk that resumed anywhere
// else would fold history twice or skip it, and the second is invisible, so
// the position is written down rather than recomputed.
type Cursor struct {
	// UpdatedAt is the run's sort key in the index: the entry's own UpdatedAt,
	// which the page continuation carries. It is absent for a page with a
	// window (Filter.Since), whose order is the run id alone, and for a cursor
	// that names a position in the reindex's walk, which is the same order.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	// Slug is the repository the run belongs to, set by the reindex walk
	// alone, which walks one repository's runs at a time and resumes inside
	// one. A page cursor leaves it empty: the index is one collection and its
	// order is not per repository.
	Slug string `json:"slug,omitempty"`
	// RunID is the run the walk ended on: the last row a page printed, or the
	// last run a reindex upserted. It is required -- a position that names no
	// run is not a position.
	RunID string `json:"run_id"`
}

// cursorPrefix marks an encoded cursor, so a token that is not one is refused
// rather than read as the beginning of the listing.
const cursorPrefix = "runindex1:"

// Encode renders the cursor as the opaque token a lease document carries (F39)
// and a caller may keep across processes.
//
// It is opaque on purpose: the fields behind it are the index's own and may
// grow a member without the value meaning something else, and a token that
// named them in the clear would invite a reader to build one by hand.
func (c Cursor) Encode() string {
	// The struct is three fields of known types, so this cannot fail; a
	// failure would be a bug here rather than a bad cursor, and returning ""
	// for it would make DecodeCursor refuse the position instead of a caller
	// writing a lease with none.
	data, err := json.Marshal(c)
	if err != nil {
		panic(fmt.Sprintf("runindex: encoding a cursor: %v", err))
	}
	return cursorPrefix + base64.RawURLEncoding.EncodeToString(data)
}

// ErrBadCursor is what DecodeCursor returns for a token that is not one.
var ErrBadCursor = errors.New("not a run index cursor")

// DecodeCursor reads a token Encode wrote, and refuses anything else: a
// truncated token, one of another release's, and one that names no run.
//
// The refusal is the point. A caller that read a bad token as "start at the
// beginning" would page a listing from its first row for ever, and a reindex
// that did would re-walk history it had already folded -- so this fails closed
// and the caller decides what a walk does about it (the reindex restarts, and
// says so).
func DecodeCursor(token string) (Cursor, error) {
	if token == "" || len(token) <= len(cursorPrefix) || token[:len(cursorPrefix)] != cursorPrefix {
		return Cursor{}, fmt.Errorf("runindex: %w", ErrBadCursor)
	}
	data, err := base64.RawURLEncoding.DecodeString(token[len(cursorPrefix):])
	if err != nil {
		return Cursor{}, fmt.Errorf("runindex: %w", ErrBadCursor)
	}
	var c Cursor
	if err := json.Unmarshal(data, &c); err != nil {
		return Cursor{}, fmt.Errorf("runindex: %w", ErrBadCursor)
	}
	if c.RunID == "" {
		return Cursor{}, fmt.Errorf("runindex: %w", ErrBadCursor)
	}
	return c, nil
}

// Page is one answer of List.
type Page struct {
	// Entries are the rows, with the overlay's rows merged in, in the page's
	// own order: the run id descending for a page with a window (the run's
	// start, Filter.Since), and UpdatedAt descending then the run id for one
	// without. An overlay row that the index does not carry yet takes its place
	// in that order rather than being appended.
	Entries []Entry
	// Next is where the page after this one starts, and is nil when this is
	// the last one: the caller hands Next.RunID back as Filter.After. It is a
	// cursor rather than a run id so that a caller that keeps it can resume
	// without a second read.
	Next *Cursor
	// IndexComplete and FoldedThrough are the summary's own two fields as they
	// stood when the page was read, so a caller that prints one list prints
	// why it may be short without a second request. They are the summary's
	// values and not the page's: a page is never "incomplete", the index is.
	IndexComplete bool
	FoldedThrough *time.Time
}

// Reader answers the two questions the index is read with: a page of runs, and
// the installation's summary.
//
// List is one Firestore query plus, in parallel, the RTDB overlay the page
// needs (the live set and the outcomes since FoldedThrough, D3202) -- a
// constant number of requests whatever the history behind it, which is the
// property every reader of this interface is bought for (I-INDEX's
// TestIndexedReadsConstantRequests).
//
// Summary is one document read, and is what a caller asks for when it wants a
// figure rather than rows.
type Reader interface {
	List(ctx context.Context, f Filter) (Page, error)
	Summary(ctx context.Context) (Summary, error)
}
