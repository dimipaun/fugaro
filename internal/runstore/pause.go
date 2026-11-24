package runstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitops"
)

// This file is C3 of docs/model-credentials-routing: the pause's shapes, in
// the one package the job, the credentials service, the resident orchestrator
// and every reader of a run already import. It holds the spelling — a name, a
// type, a version and a path — and nothing else: who writes each shape, who
// may read it, which request a run makes and what a reader of another version
// does is docs/model-credentials-routing/contract-pause.md, which is the
// contract this file serves.
//
// R6 says which half of a pause lives where, and it is why both halves are
// here. The durable record a suspended run leaves for the run that resumes it
// is a bucket object of its own prefix (pause.json), written inside the job,
// like the record a follow-up already trusts (R26). The mark that says a run
// is suspended, and until when, is a Realtime Database node under paused/,
// written by the credentials service alone (R19, D3189): the job cannot write
// it, and the service never writes the record.

// StatusPaused is the word a run that suspended itself ends with (D3185, R15).
// It is a status of its own and not a reuse of an existing one: a paused run
// is in flight — it will be resumed — where a halted one has stopped, and a
// reader that could not tell the two apart would count a paused chain as lost.
//
// It is deliberately not in Statuses yet. That slice is the vocabulary a
// finished run's record is held to, and four readers of it learn the word
// together (result.schema.json's enum, internal/digest's closed set,
// internal/budget's outcome words and the console's set): PR1 of this plan
// adds it to them at once, which is where the word becomes one a record and a
// rule may carry. Until then nothing writes it.
//
// Nothing is needed in Status.Terminal for it. The execution that ends paused
// has ended — its container stops and its lease is released (R12) — and
// Terminal's own rule (a status that is not running has ended) already says
// so; the word is in no outcome vocabulary until PR1 puts it there.
const StatusPaused Status = "paused"

// PauseFile is the object a suspending run writes in its own prefix, and the
// only thing the run that resumes it reads to know where to pick up (D3185).
// It is written inside the job, so it is trusted exactly as far as the run
// record a follow-up already trusts is (R23, R26) — and no further: a reader
// validates what it needs before it acts on any of it.
const PauseFile = "pause.json"

// PauseVersion is PauseFile's format version. A reader that does not know the
// version it finds refuses the record rather than guessing at it (R18): the
// run then ends with a named outcome and starts no stage afresh. The version
// is checked on both sides, so a writer cannot produce a record no reader of
// this release would take.
const PauseVersion = 1

// PauseRecord is pause.json: where the suspended run was and what it left
// behind for the run that resumes it.
type PauseRecord struct {
	Version int `json:"version"` // PauseVersion
	// Stage is the stage the run was in when it suspended, so that the run
	// that resumes it enters that stage and not the first one: the work it
	// interrupts is the work it must carry on.
	Stage string `json:"stage"`
	// Session is the name of the Claude Code session the run saved, which the
	// resumed run restores (F34). It names an object of the run's own prefix
	// (session/<id>.jsonl), so it is held to the session rule before any
	// reader builds a name out of it — a value that could not be a session is
	// refused here and never looked up (ReadSession's own rule).
	Session string `json:"session"`
	// Checkpoint is the commit the run pushed before it exited (F34): the work
	// in progress, which the resumed run checks out. Uncommitted work is lost
	// unless it is pushed first (docs/checkpoint-pushes), so a resume that
	// finds its checkout at another commit refuses the record rather than
	// starting from what it finds (R18). It is a full commit id and nothing
	// else (gitops.ValidCommitSHA): the resumed run's checkout is only safe —
	// and only meaningful — against a fixed commit, and this member is written
	// by the job, so a ref name, HEAD or an option-shaped value is a value a
	// hostile agent chose.
	Checkpoint string `json:"checkpoint"`
}

var (
	// ErrInvalidPause means pause.json names no usable pause: a version this
	// reader does not know, a member it needs and does not find, or a member
	// this release does not know at all. It is the name R18's outcome carries
	// — the run that meets it ends saying so and starts nothing afresh — and
	// it is what the writer refuses too, so no side of the record can produce
	// what the other would not take.
	ErrInvalidPause = errors.New("pause.json names no usable pause")
	// ErrInvalidPauseNode means a paused node is not one this contract
	// describes: a member it must have is missing or out of range, or it
	// carries a member this release does not know. The node is display data to
	// every launcher and an input only to the service and the orchestrator
	// (R26), and neither of those acts on half a node: the orchestrator
	// resumes nothing it could not read, and the service refuses rather than
	// overwrite what it cannot place.
	ErrInvalidPauseNode = errors.New("the paused node names no usable pause")
)

// WritePause stores the record as pause.json. It is written by the run that is
// suspending, in its own prefix, and by nobody else.
func (s *Store) WritePause(ctx context.Context, p PauseRecord) error {
	if err := validatePause(p); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return s.PutFile(ctx, PauseFile, data, "application/json")
}

// ReadPause reads the run's pause.json: ErrNotFound when the run never
// suspended. The object is written by the run's own job, so nothing in it is
// trusted: it is decoded strictly, so a record carrying a member this release
// does not know is refused rather than read as one it does (R18), and every
// member a reader acts on is checked before it is returned. On a version this
// reader does not know the record is returned beside the error, so the run's
// outcome can name the version it could not read.
func (s *Store) ReadPause(ctx context.Context) (*PauseRecord, error) {
	raw, err := s.readRecordObject(ctx, PauseFile)
	if err != nil {
		return nil, err
	}
	var p PauseRecord
	if err := decodePauseObject(raw, &p); err != nil {
		// A member this release cannot place and a second JSON value are
		// refusals of the record, not of its decoding: they carry the same
		// sentinel a bad version does, because R18's outcome is one name and
		// the runner asks it with errors.Is (the node side wraps its own the
		// same way).
		return nil, fmt.Errorf("%w: decoding %s: %w", ErrInvalidPause, PauseFile, err)
	}
	if err := validatePause(p); err != nil {
		return &p, err
	}
	return &p, nil
}

// validatePause is the record's one rule, asked by the writer before it writes
// and by the reader after it reads.
func validatePause(p PauseRecord) error {
	switch {
	case p.Version != PauseVersion:
		return fmt.Errorf("%w: version %d", ErrInvalidPause, p.Version)
	case p.Stage == "":
		return fmt.Errorf("%w: it names no stage", ErrInvalidPause)
	case !agent.ValidSessionID(p.Session):
		return fmt.Errorf("%w: the session is not a lower-case UUID", ErrInvalidPause)
	case !gitops.ValidCommitSHA(p.Checkpoint):
		// The checkpoint is a commit id and nothing else: a ref name, HEAD or
		// an option-shaped value would reach the resumed run's checkout, whose
		// whole point is that the checkout is the commit the suspended run
		// pushed (R18). The job writes this member, so it is a value a hostile
		// agent chooses, and git accepts an option where a rev is expected
		// (gitops' own rule for the revs it fetches).
		return fmt.Errorf("%w: the checkpoint is not a commit id", ErrInvalidPause)
	}
	return nil
}

// PauseNodeRoot is the database subtree the paused marks live under. Every
// launcher of a project can read it and it is never an authorization input to
// anything but the service and the orchestrator (R26).
const PauseNodeRoot = "paused"

// PauseNodePath is where the credentials service records one paused run:
// paused/<slug>/<run>, the repository's slug and the run id escaped for the
// database. It is built here because four sides need the same path and only
// one of them writes it: the service writes the node, the orchestrator reads
// the subtree to find what is due, and the views read one node to draw a
// paused run.
func PauseNodePath(slug, run string) string {
	return PauseNodeRoot + "/" + pauseSeg("slug", slug) + "/" + pauseSeg("run id", run)
}

// pauseSeg escapes one slug or run id for the database exactly as
// internal/budget's Key does, byte for byte: RTDB forbids . $ # [ ] / and
// ASCII control characters in a key, each becomes %XX (uppercase hex), and %
// itself is escaped so no two inputs collide. The rule is restated here and not
// called because internal/budget imports this package and this one cannot call
// it back (internal/projectpolicy's escKey restates it for the same reason).
//
// A segment is never empty: an empty one collapses the path onto another node
// (paused//r1 is paused/r1 once the database cleans the URL), and a slug or a
// run id is validated long before a path is built, so an empty one is a
// programming error and not a value to escape (budget's seg panics for the
// same reason).
func pauseSeg(what, v string) string {
	if v == "" {
		panic("runstore: empty " + what + " in a paused node's path")
	}
	return pauseKey(v)
}

func pauseKey(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' || c == '.' || c == '$' || c == '#' || c == '[' || c == ']' || c == '/' || c < 0x20 || c == 0x7f {
			b = append(b, fmt.Sprintf("%%%02X", c)...)
			continue
		}
		b = append(b, c)
	}
	return string(b)
}

// PauseNode is what the credentials service records at PauseNodePath. Every
// member of it is the service's own record of the run's grant and of the pause
// it decided (R8, D3189): the launcher and the pool a resume is bound to, and
// the instant and the chain the resume is measured by. The job's word about who
// launched it, which pool it draws on or when it should come back is never read
// from anywhere — a run's agent could name another person there and spend their
// tokens (F37).
type PauseNode struct {
	// Launcher is the verified principal the run's slip named (R7, R8): a
	// person's Google subject, or a verified service account. It is an
	// identifier and never display text — the run's own launch record holds
	// that, and the two are different facts — and a run that resumes this one
	// draws on this principal's pool (D3191).
	Launcher string `json:"launcher"`
	// Pool is the pool the run's launch named (fugaro run --pool), empty when
	// the launch named none and the launcher's own designation decides. A
	// resume keeps it: a run that resumes a paused one draws on the pool its
	// launch named and not on one its own launcher would resolve to.
	Pool string `json:"pool,omitempty"`
	// ResumeAt is the instant the run will be resumed at, in epoch
	// milliseconds UTC — the unit the database keeps instants in (budget's
	// AgentEntry and the wire's reset_at are the same figure in the same unit)
	// — computed by the service and never named by the job.
	ResumeAt int64 `json:"resume_at"`
	// Chain is the id of the chain the paused run belongs to: runstore.Chain's
	// Root, the run id the chain is rooted at (D1045). The resume continues
	// that chain, and the chain's paused time is what bounds how often one may
	// pause (D3190).
	Chain string `json:"chain"`
	// PausedSeconds is the chain's paused time so far, in seconds, this pause
	// included — the same unit the chain's Time dimension is measured in, so
	// the figure a resume reads is the figure the limit is stated in (D3190).
	// The service computes it from its own records; a chain whose node carries
	// no figure has paused for no measurable time.
	PausedSeconds Amount `json:"paused_seconds,omitempty"`
	// Cancelled is the mark that stops the pause ever being resumed: the
	// paused run's launcher sets it (D3197), the service writes it, and the
	// orchestrator resumes no node carrying it. It is a mark and not a
	// deletion, so a cancelled pause is still readable as the pause it was.
	Cancelled bool `json:"cancelled,omitempty"`
}

// Validate is the node's one rule, asked by the writer before it stores the
// node and by the reader after it reads one.
func (n PauseNode) Validate() error {
	switch {
	case n.Launcher == "":
		return fmt.Errorf("%w: it names no launcher", ErrInvalidPauseNode)
	case n.ResumeAt <= 0:
		return fmt.Errorf("%w: it names no resume time", ErrInvalidPauseNode)
	case n.Chain == "":
		return fmt.Errorf("%w: it names no chain", ErrInvalidPauseNode)
	case n.PausedSeconds < 0:
		return fmt.Errorf("%w: its paused time is negative", ErrInvalidPauseNode)
	}
	return nil
}

// Bytes renders the node as the object the service stores: it validates first,
// so a node this contract does not describe is refused here rather than written
// where the orchestrator and every launcher's view will read it.
func (n PauseNode) Bytes() ([]byte, error) {
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(n)
}

// ParsePauseNode reads one paused node. It is the reader every side that is not
// the service uses, so that the orchestrator, the cancel route and the views
// hold a node to the same rules: a member this release does not know is refused
// rather than ignored, because the node is an input to the orchestrator and
// half of a node is not a pause it may resume.
func ParsePauseNode(raw []byte) (*PauseNode, error) {
	var n PauseNode
	if err := decodePauseObject(raw, &n); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidPauseNode, err)
	}
	if err := n.Validate(); err != nil {
		return &n, err
	}
	return &n, nil
}

// decodePauseObject decodes exactly one JSON object into out, refusing a member
// this release does not know and anything after the value. Both of the pause's
// stored shapes are read this way: a record and a node are written by one
// release and read by another (a pause spans a fugaro update, F36), and a
// member this reader cannot place is refused rather than dropped, so a set of
// shapes never drifts into "the members I happened to know" (R18, R25's own
// rule for the policy).
func decodePauseObject(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("it carries more than one JSON value")
	}
	return nil
}

// PauseAction is what the credentials service tells a run to do when every
// source its chain can reach is exhausted (D3184-D3187). The choice is the
// service's and never the job's (R19): the job follows the action it is given
// and decides nothing about time itself. The one rule the runner owns is R10 —
// a hotfix run treats wait and pause as halt — which is the runner's because it
// is the side that knows the mode.
type PauseAction string

const (
	// ActionWait is the in-job wait: the reset is nearer than pause.wait_in_job,
	// so the run waits for it inside the job as it always has (D3184, F28). It
	// is the action a run takes while the answer's own reset_at is what it
	// waits for.
	ActionWait PauseAction = "wait"
	// ActionPause is the suspend: the run saves its session, pushes its work in
	// progress as a checkpoint, writes pause.json, tells the service it has
	// saved, ends paused and releases its lease. The service resumes it later
	// as a new run in the same chain (D3185).
	ActionPause PauseAction = "pause"
	// ActionHalt is the end: nothing is worth waiting for — the reset is beyond
	// pause.max_pause, the installation has no resident orchestrator, the
	// chain's paused time is used up — so the run ends as a halted run does,
	// with HaltNoCredential and the reset time in the reason.
	ActionHalt PauseAction = "halt"
)

// Known reports whether a is one of the three. Both ends of the contract ask
// it, the way the wire's own closed sets are asked: an action outside them is
// not a value to guess at but a value neither side wrote.
func (a PauseAction) Known() bool {
	return a == ActionWait || a == ActionPause || a == ActionHalt
}

// The member names of the pause. They are here, with the shapes, because the
// pause is read in more than one format and a name spelled twice is a name that
// drifts: the answer's JSON, the paused node and a run's row must all call the
// resume instant the same thing, or a reader of one draws a figure from
// another's meaning.
const (
	// PauseMemberAction is the member an exhausted answer carries the action
	// under. It is one of the PauseAction values, and it is added to the answer
	// only for a caller that sent the stage header (R24): a gateway of a
	// release behind decodes the refusal strictly (F57), so a member it does
	// not know would halt a run this release means to pause. The header itself
	// is the stage's, and belongs to the chain's own contract (C1's
	// credsvc.HeaderStage) and not to this file.
	PauseMemberAction = "action"
	// PauseMemberResumeAt is the member the resume instant is carried under in
	// all three formats: the answer (with action pause), the node and a run's
	// row. It is one instant with one name, in each format's own unit — epoch
	// milliseconds UTC in the answer and in the database, RFC 3339 UTC in a
	// run's row, which spells every instant that way (watch's waiting_until).
	// The job never chooses it: it is the service's figure (R8).
	PauseMemberResumeAt = "resume_at"
	// PauseMemberResumedBy is the member a run's row carries in `fugaro watch
	// --json` and `runs ls --json`: the run that resumed the paused one,
	// absent while nothing has. A reader finds it in the chain — the run whose
	// task names PauseMemberResumes is the one that resumed this pause — and
	// not on the node, which is the service's record of the pause and holds
	// nothing about a resume that had not happened when it was written.
	PauseMemberResumedBy = "resumed_by"
	// PauseMemberResumes is the task member a resumed run is launched with: the
	// id of the paused run whose pause.json it picks up. The orchestrator
	// writes it (D3185), the run reads it, and it is the whole of what a
	// resumed run is told about the run it continues — which is what keeps the
	// resume out of the pool question: who the resumed run draws on is the
	// node's record of the paused run's grant and never this member's.
	PauseMemberResumes = "resumes"
)

// PausePath is the request a run makes once it has saved what a resume needs:
// it has written the session, pushed the checkpoint and written pause.json, and
// this is how it tells the service so. It carries no member of its own — the
// run is the identity the service already verified, and everything about the
// pause (the launcher, the pool, the resume time) is the service's own record
// of the run's grant (R8). It is this service's own path and not an Anthropic
// one (server.go draws the same distinction for the sign route), and it is
// spelled as the plan names it.
const PausePath = "/pause"
