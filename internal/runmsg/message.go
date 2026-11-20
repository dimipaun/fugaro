// Package runmsg is the channel a person uses to steer a run that is already
// going (docs/run-messages). It holds the format of one message, the store
// that reads and writes message objects under tokens/, and the block the run
// record carries about them.
//
// Where a message lives is the whole of its authentication (D2941, F2): a job
// account holds roles/storage.objectUser on runs/ and only a conditioned
// objectViewer on tokens/, so an inbox under runs/ could be planted by the
// very agent it is meant to steer — or, on a shared project job, into another
// repository's run. Nothing here writes under runs/: the prefix is always
// token.ObjectPrefix.
package runmsg

import (
	"errors"
	"fmt"
	"time"
)

// Format is the message format this build writes and reads, the highest one it
// knows. A message carries its own version, so a runner older than the sender
// refuses a shape it does not understand instead of reading it as one it does
// (F25); the run record advertises this number in its messages block, and a
// sender writes the version the run advertises.
const Format = 1

// Suffix ends every message object's name. It is what the runs bucket's
// lifecycle rule matches to reap a message after the owner's seven days
// (F11), so the rule and this store cannot disagree about which objects are
// messages.
const Suffix = ".msg.json"

// MaxText caps a message's text in bytes: 4 KiB, the owner's answer (O1, F12).
// A text at the cap is written; one over it is refused at the store, so the
// runner never has to read and then discard an object a sender should not have
// written.
const MaxText = 4096

// MaxMessages is how many messages one run reads: the 21st is refused at the
// store and no number above this one is ever read (F12). The cap is on what a
// run takes in, not on what a bucket may hold.
const MaxMessages = 20

// MaxSender bounds the sender's name, which the record keeps for display only:
// a name longer than this is not one a person typed, and the record and the
// message's schema both hold it to this.
const MaxSender = 256

// MaxObjectBytes is the largest message object this store reads. A 4 KiB text
// is at most about 24 KiB of JSON once every byte is escaped, and the fields
// around it are bounded, so an object above this is not one this store wrote
// and is refused without being read whole.
const MaxObjectBytes = 64 << 10

// The flags a message may carry. They are a closed set the runner advertises
// in its record (F25): a sender writes only what the run lists, so a release
// that carries the command before the runner honours a flag never sends one
// the runner would treat as ordinary text.
const (
	// FlagNow asks for the message at the agent's next quiet point, inside the
	// coder stage that is running (D2945, rule 2).
	FlagNow = "now"
	// FlagWrapUp asks the run to finish: commit what it has and stop, and the
	// coder stage ends (D2946, rule 3).
	FlagWrapUp = "wrap_up"
)

// State is what became of one message, as the run recorded it (F13). The
// record is an audit trail: the runner's in-memory position is the only
// authority for delivery, and nothing reads a state back to decide anything.
type State string

const (
	// StateDelivered means the message reached the agent in the stage the
	// record names.
	StateDelivered State = "delivered"
	// StateUndelivered means the run ended before a coder stage could carry
	// it (D2944, rule 5).
	StateUndelivered State = "undelivered"
	// StateRefused means the object is there and is not a message this build
	// reads: unparseable, over the cap, or a format it does not know (rule 6).
	StateRefused State = "refused"
	// StateMissing means the number was never written, or was written and
	// deleted: the runner saw a later number and recorded the hole rather than
	// stalling on it (F10).
	StateMissing State = "missing"
)

// The refusals a sender or a reader gets. They are separate values because
// each is a different thing to say to the person who typed the message, and
// each is a different reason in the record.
var (
	// ErrEmpty means a message with no text.
	ErrEmpty = errors.New("the message has no text")
	// ErrTooLong means the text, or the sender's name, is over its cap.
	ErrTooLong = errors.New("the message is over its size cap")
	// ErrShape means the message is not one this build writes: a version it
	// does not have, a flag it does not know, or no send time.
	ErrShape = errors.New("the message is not one this build writes")
	// ErrTooMany means the run already holds MaxMessages.
	ErrTooMany = errors.New("the run has as many messages as it reads")
	// ErrGap means the listing has a number missing below its highest, so a
	// sender cannot know which number is next without papering over the hole.
	ErrGap = errors.New("the listing has a gap")
	// ErrRefused means an object under the prefix is not a message this build
	// reads. The runner records it as StateRefused and never shows it to the
	// agent (rule 6).
	ErrRefused = errors.New("the object is not a message this build reads")
)

// Message is one message object: tokens/<slug>/<run>/messages/NNNN.msg.json.
// It is the person's own words and the little needed to place them — who sent
// it, when, and whether it asks for anything beyond the next coder stage. The
// sender's name is self-declared and display only (D2940): the bucket's IAM,
// not this field, is what makes a message unforgeable.
type Message struct {
	// Version is the format the sender wrote, which is at most Format. A
	// reader refuses a version above the one it knows rather than guessing.
	Version int `json:"version"`
	// Seq is the number in the object's own name. The name and the field must
	// agree: the number is the identity a later stage and the record use, and
	// a name that disagrees with the field is refused.
	Seq int `json:"seq"`
	// SentAt is when the object was written. Put stamps it when the caller
	// leaves it zero.
	SentAt time.Time `json:"sent_at"`
	// SentBy names the sender as it states itself. Display only, never a
	// decision: no per-object writer metadata exists to check it against
	// (F1, D2940).
	SentBy string `json:"sent_by,omitempty"`
	// Flags are the flags the sender asked for, from the set above.
	Flags []string `json:"flags,omitempty"`
	// Text is the person's words. The runner redacts, cleans and fences it
	// before the agent sees it; the store only bounds it (F12).
	Text string `json:"text"`
}

// Delivery is what the run recorded about one message (F13). It carries no
// text: the record is written by the run, which a hostile agent can rewrite,
// so it is an audit trail and not the message. The text is in the object under
// tokens/ and in the report the runner posts from its own memory.
type Delivery struct {
	// Seq is the message's number.
	Seq int `json:"seq"`
	// SentAt is when the object was written, absent for a number the runner
	// found missing (there is no object to read it from).
	SentAt *time.Time `json:"sent_at,omitempty"`
	// SentBy is the self-declared sender, display only.
	SentBy string `json:"sent_by,omitempty"`
	// Flags are the flags the message carried.
	Flags []string `json:"flags,omitempty"`
	// State is what became of it.
	State State `json:"state"`
	// Stage is the coder stage it reached, for a delivered message.
	Stage string `json:"stage,omitempty"`
	// DeliveredAt is when it reached that stage.
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
	// Reason says why, for a refused or undelivered message.
	Reason string `json:"reason,omitempty"`
}

// Record is the run record's messages block (F13, F24, F25): whether this run
// reads messages at all, the format and the flags its runner knows, and one
// entry per message it saw. It is additive beside the record's other blocks —
// a record written before it decodes unchanged and the format's version does
// not move — and it is written at bootstrap, so a sender can tell a run whose
// runner predates the feature from one that will read the message (F9).
//
// Accepting is false for a run whose recipe has no coder stage (a review-only,
// artifact, merge-judge, evaluate or rebase-only run): a message would have
// nowhere to go, and the sender is refused with that reason rather than
// writing an object nobody reads (F24).
type Record struct {
	// Accepting is whether this run reads messages.
	Accepting bool `json:"accepting"`
	// Format is the highest message format this run's runner reads, which a
	// sender writes into the message it sends (F25).
	Format int `json:"format"`
	// Flags are the flags this run's runner honours, absent when it honours
	// none. A sender refuses a flag the run does not list rather than sending
	// one the runner would treat as ordinary text (F25).
	Flags []string `json:"flags,omitempty"`
	// Messages is what happened to each message, in number order.
	Messages []Delivery `json:"messages,omitempty"`
}

// ValidFlag reports whether s is a flag this build knows. The set is closed:
// a flag nobody honours is a message the runner would read as plain text, so
// it is refused at the store instead.
func ValidFlag(s string) bool { return s == FlagNow || s == FlagWrapUp }

// check refuses a message this build does not write, whatever side of the
// store it is on: the write side checks what a caller hands it, and the read
// side checks the same shape, because the prefix is writable by hand.
func check(m Message) error {
	switch {
	case m.Version < 1 || m.Version > Format:
		return fmt.Errorf("%w: version %d is not one this build reads (it reads up to %d)", ErrShape, m.Version, Format)
	case m.SentAt.IsZero():
		return fmt.Errorf("%w: it carries no send time", ErrShape)
	case m.Text == "":
		return fmt.Errorf("%w: a message with no text says nothing", ErrEmpty)
	case len(m.Text) > MaxText:
		return fmt.Errorf("%w: the text is %d bytes and the cap is %d", ErrTooLong, len(m.Text), MaxText)
	case len(m.SentBy) > MaxSender:
		return fmt.Errorf("%w: the sender is %d bytes and the cap is %d", ErrTooLong, len(m.SentBy), MaxSender)
	case len(m.Flags) > 2:
		return fmt.Errorf("%w: %d flags, and a message asks for at most two", ErrShape, len(m.Flags))
	}
	seen := map[string]bool{}
	for _, f := range m.Flags {
		if !ValidFlag(f) {
			return fmt.Errorf("%w: %q is not a flag this build knows", ErrShape, f)
		}
		if seen[f] {
			return fmt.Errorf("%w: %q is listed twice", ErrShape, f)
		}
		seen[f] = true
	}
	return nil
}
