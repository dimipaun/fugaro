package watch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/rtdb"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// The kill and resume prompts (design §7, plan W10) as a pure state machine:
// no terminal, no network. The model feeds it keys and runs the Request it
// returns through Execute. It flips the project's kill switch, so its rules
// are deliberately plain:
//
//   - all-project actions (K kill project, R resume project) need the PROJECT
//     NAME typed, exactly like `fugaro budget kill|resume --all`;
//   - killing one repository needs one y or Enter (as the CLI's one-repo kill
//     needs nothing);
//   - resuming one repository needs the repository's name typed;
//   - the typed word is compared as the CLI does: trimmed, exact, case
//     sensitive. Empty never matches;
//   - the target is captured when the key is pressed, never re-read from the
//     cursor, and the prompt names it;
//   - one write at a time, and keys are off while the view is offline or stale.

// DefaultReason is recorded when the user gives none.
const DefaultReason = "from fugaro watch"

// MaxReason bounds a reason, as `fugaro budget kill --reason` does.
const MaxReason = 200

// Kind is what a prompt will do.
type Kind int

const (
	KillAll Kind = iota + 1
	KillRepo
	ResumeAll
	ResumeRepo
)

func (k Kind) kill() bool { return k == KillAll || k == KillRepo }
func (k Kind) all() bool  { return k == KillAll || k == ResumeAll }

// Target is the repository a repo action was started on: Slug keys the node,
// Name is what the user knows it by.
type Target struct{ Slug, Name string }

// Request is a confirmed action, ready for Execute. A value copy of what was
// captured at the key press.
type Request struct {
	Kind    Kind
	Project string
	Target  Target // zero for the all-project kinds
	Reason  string
}

type flowPhase int

const (
	idle    flowPhase = iota
	askYes            // kill one repository: y or Enter
	askWord           // type the project's or repository's name
	askReason
)

// Flow is the prompt state. The zero value is not usable; use NewFlow.
type Flow struct {
	project  string
	ph       flowPhase
	kind     Kind
	target   Target
	input    string
	inflight bool
	notice   string
}

// NewFlow is a Flow for the named project.
func NewFlow(project string) *Flow { return &Flow{project: project} }

// Active is true while a prompt is showing, so the model sends keystrokes
// here instead of treating them as commands (k and K are kill, not movement).
func (f *Flow) Active() bool { return f.ph != idle }

// InFlight is true between a confirmed Request and Done.
func (f *Flow) InFlight() bool { return f.inflight }

// Notice is the last refusal or cancellation, "" if none; it clears on the
// next Start.
func (f *Flow) Notice() string { return f.notice }

// Start opens the prompt for kind on t (ignored for the all-project kinds).
// live is false while the view is offline or stale: the write would race a
// state nobody sees, so nothing starts. It returns the refusal, "" when the
// prompt opened.
func (f *Flow) Start(kind Kind, t Target, live bool) string {
	f.notice = ""
	switch {
	case kind < KillAll || kind > ResumeRepo:
		return f.refuse("unknown action")
	case f.ph != idle:
		return f.refuse("finish or cancel (Esc) the open prompt first")
	case f.inflight:
		return f.refuse("a write is still in progress; wait for it")
	case !live:
		return f.refuse("keys are off while the data is offline or stale")
	case f.project == "":
		return f.refuse("the project's name is unknown")
	case !kind.all() && (t.Slug == "" || t.Name == ""):
		return f.refuse("no repository is selected")
	}
	// The name compared is the name shown: one with unprintable characters
	// would be compared as something other than what the user reads.
	if CleanLine(f.project, 0) != f.project || (!kind.all() && CleanLine(t.Name, 0) != t.Name) {
		return f.refuse("the name contains unprintable characters; use fugaro budget kill|resume instead")
	}
	f.kind, f.target, f.input = kind, Target{}, ""
	if !kind.all() {
		f.target = t
	}
	switch kind {
	case KillRepo:
		f.ph = askYes
	default:
		f.ph = askWord
	}
	return ""
}

func (f *Flow) refuse(s string) string { f.notice = s; return s }

// Prompt is the line to show while a prompt is active, "" otherwise. Names
// are sanitized, so a hostile repository name cannot inject escapes.
func (f *Flow) Prompt() string {
	switch f.ph {
	case askYes:
		return fmt.Sprintf("kill repository %s? y/Enter = yes, n/Esc = no", CleanLine(f.target.Name, 80))
	case askWord:
		switch f.kind {
		case KillAll:
			return fmt.Sprintf("kill the WHOLE PROJECT: halts every run of %s and refuses new ones. Type %s to confirm (Esc cancels): %s", CleanLine(f.project, 80), CleanLine(f.project, 80), f.input)
		case ResumeAll:
			return fmt.Sprintf("resume the WHOLE PROJECT %s. Type %s to confirm (Esc cancels): %s", CleanLine(f.project, 80), CleanLine(f.project, 80), f.input)
		default:
			return fmt.Sprintf("resume repository %s. Type %s to confirm (Esc cancels): %s", CleanLine(f.target.Name, 80), CleanLine(f.target.Name, 80), f.input)
		}
	case askReason:
		return fmt.Sprintf("reason (Enter = %q, Esc cancels): %s", DefaultReason, f.input)
	}
	return ""
}

// Type adds typed (live as for Enter: a y while the view is not live cancels) or pasted text to the open prompt. Control characters,
// newlines and invisible format characters are dropped, so a paste cannot
// submit or smuggle. In the y/n prompt it answers: y confirms (returned as a
// Request), n cancels, anything else is ignored.
func (f *Flow) Type(s string, live bool) *Request {
	if f.ph == askYes && !live && (s == "y" || s == "Y") {
		f.Esc()
		f.notice = "cancelled: the data went offline or stale; nothing changed"
		return nil
	}
	switch f.ph {
	case askYes:
		// One keystroke only: a paste that happens to contain a y answers nothing.
		switch s {
		case "y", "Y":
			return f.submit(DefaultReason)
		case "n", "N":
			f.Esc()
		}
	case askWord, askReason:
		f.input += CleanLine(s, 0)
		if f.ph == askReason {
			f.input = safetext.Clip(f.input, MaxReason)
		} else {
			f.input = safetext.Clip(f.input, 200)
		}
	}
	return nil
}

// Backspace removes the last typed character.
func (f *Flow) Backspace() {
	if f.ph == askWord || f.ph == askReason {
		if _, n := utf8.DecodeLastRuneInString(f.input); n > 0 {
			f.input = f.input[:len(f.input)-n]
		}
	}
}

// Esc cancels the open prompt; nothing is written.
func (f *Flow) Esc() {
	if f.ph != idle {
		f.notice = "cancelled; nothing changed"
	}
	f.ph, f.input = idle, ""
}

// Enter submits the open prompt. live is the current connection state: a
// prompt left open while the data went stale is cancelled, not submitted. The
// Request, when not nil, must be run through Execute and answered with Done.
func (f *Flow) Enter(live bool) *Request {
	if f.ph == idle {
		return nil
	}
	if !live {
		f.Esc()
		f.notice = "cancelled: the data went offline or stale; nothing changed"
		return nil
	}
	switch f.ph {
	case askYes:
		return f.submit(DefaultReason)
	case askWord:
		want := f.project
		if !f.kind.all() {
			want = f.target.Name
		}
		if want == "" || strings.TrimSpace(f.input) != want {
			f.input = ""
			f.notice = "not confirmed: that is not the name; nothing changed (Esc cancels)"
			return nil
		}
		if f.kind == KillAll {
			f.ph, f.input, f.notice = askReason, "", ""
			return nil
		}
		reason := DefaultReason
		return f.submit(reason)
	case askReason:
		reason := strings.TrimSpace(f.input)
		if reason == "" {
			reason = DefaultReason
		}
		return f.submit(reason)
	}
	return nil
}

func (f *Flow) submit(reason string) *Request {
	if f.inflight {
		return nil
	}
	req := &Request{Kind: f.kind, Project: f.project, Target: f.target, Reason: safetext.Clip(CleanLine(reason, 0), MaxReason)}
	f.ph, f.input, f.notice, f.inflight = idle, "", "", true
	return req
}

// Done ends the in-flight write; the next action may start.
func (f *Flow) Done() { f.inflight = false }

// ---------------------------------------------------------------- Execute

// Outcome is what Execute reports; Notice is one safe line for the footer.
type Outcome struct {
	Notice  string
	Written bool
	Already bool
	Denied  bool  // the database refused the write (401, 403)
	Err     error // set for anything that did not end in a clean answer
}

// ExecuteTimeout bounds one Execute, so a hung database call cannot keep the
// flow in flight forever: Execute always returns and the caller calls Done.
var ExecuteTimeout = 15 * time.Second

// Execute runs a confirmed request through budget.SetKill, the writer the
// CLI uses too. by is the signed-in identity. There is no client-side check
// of rights: the database decides, and a refusal says so.
func Execute(ctx context.Context, db interface {
	budget.KillDB
	Get(ctx context.Context, path string, out any) (bool, error)
}, req Request, by string) Outcome {
	ctx, cancel := context.WithTimeout(ctx, ExecuteTimeout)
	defer cancel()
	path, what := budget.PathKillGlobal, "project "+CleanLine(req.Project, 80)
	if !req.Kind.all() {
		path, what = budget.PathKillRepo(req.Target.Slug), "repository "+CleanLine(req.Target.Name, 80)
	}
	res, err := budget.SetKill(ctx, db, path, req.Kind.kill(), by, req.Reason, budget.KillHooks{})
	var ke *budget.KillError
	switch {
	case errors.As(err, &ke) && ke.Write && errors.Is(err, rtdb.ErrPermission):
		return Outcome{Denied: true, Err: err, Notice: budget.AdminDeniedText(CleanLine(req.Project, 80))}
	case errors.As(err, &ke):
		return Outcome{Err: err, Notice: "could not " + verb(req.Kind) + " " + what + ": " + CleanLine(err.Error(), 160)}
	case errors.Is(err, budget.ErrKillContention):
		return Outcome{Err: err, Notice: "the kill switch kept changing under the write; try again"}
	case err != nil:
		return Outcome{Err: err, Notice: CleanLine(err.Error(), 160)}
	}
	if res.Already {
		if req.Kind.kill() {
			return Outcome{Already: true, Notice: fmt.Sprintf("%s is already killed (by %s: %s); nothing changed", what, CleanLine(res.Previous.By, 80), CleanLine(res.Previous.Reason, 80))}
		}
		return Outcome{Already: true, Notice: what + " is not killed; nothing changed"}
	}
	o := Outcome{Written: true}
	if req.Kind.kill() {
		o.Notice = "killed " + what + " (by " + CleanLine(by, 80) + "); waiting for the stream"
	} else {
		o.Notice = "resumed " + what + " (by " + CleanLine(by, 80) + "); waiting for the stream"
		if req.Kind == ResumeRepo {
			var g budget.Kill
			if _, err := db.Get(ctx, budget.PathKillGlobal, &g); err == nil && g.On {
				o.Notice += fmt.Sprintf("; warning: the project-wide kill switch is still on (by %s): %s stays halted until the project is resumed", CleanLine(g.By, 80), what)
			}
		}
	}
	return o
}

func verb(k Kind) string {
	if k.kill() {
		return "kill"
	}
	return "resume"
}

// ----------------------------------------------------------- sanitizing

// CleanLine is s as one safe line: escape sequences, controls, newlines and
// invisible format characters (BiDi, zero width) are dropped (safetext.Strip)
// and, when max > 0, it is cut to max runes.
func CleanLine(s string, max int) string {
	s = safetext.Strip(s)
	if max > 0 {
		return safetext.Clip(s, max)
	}
	return s
}
