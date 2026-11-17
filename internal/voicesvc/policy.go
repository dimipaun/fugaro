package voicesvc

// The hop's enforcement: the project policy is read here and obeyed here, and
// nowhere else in this tree may a device decide for itself.
//
// Why the decision cannot live on the phone (§2, §4.1, Q3 answered
// 2026-10-19): a device holds no grant on config/policy/**, so a stale cache, a
// modified client and a device whose owner flipped its own switch all reach
// this refusal. What a device *can* do is choose to speak locally, which needs
// no permission — so the enforcement is one-directional by construction: the
// hop can refuse what the device wanted and can never grant what the project
// forbade.
//
// Two things are read and both are the policy's own node: the project's child
// (projectpolicy.PathVoiceRepo) and, when that child does not exist, the
// installation's shared allow-list (PathVoiceShared) — the same fallback
// internal/credsvc's RTDBPolicy makes for the carried-forward policy. The
// reader is internal/projectpolicy's, which is P5.3.2's default deny, and this
// file adds no rule of its own: a node that is absent, empty, malformed or
// unreadable is the local answer, and the only thing this file decides is
// WHICH reason the refusal carries.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// Getter is the one database read this package performs: an RTDB node by path.
// It is internal/rtdb.Client's own Get, narrowed to the method used so this
// package's tests need no client, no URL and no token.
//
// `out` is what json.Unmarshal decodes into, and json.RawMessage is what the
// policy reader wants: DecodeVoice reads bytes and refuses a member it does not
// model, which a decode into a struct here would have already dropped.
type Getter interface {
	Get(ctx context.Context, path string, out any) (found bool, err error)
}

// VoiceReader answers what one project's voice policy is. It is an interface so
// the server's tests can decide a project's answer without a database.
type VoiceReader interface {
	// Voice returns the policy in force for slug, and the refusal when the
	// node could not be read or decoded. An absent node is NOT an error: it is
	// LocalVoice, which is the same answer as an empty one (§4.3).
	Voice(ctx context.Context, slug string) (projectpolicy.Voice, error)
}

// RTDBVoice reads the voice policy out of the installation's Realtime Database
// over IAM.
//
// It is the hop's own reader and not a run's: the rules are not consulted at
// all here, because this process holds an IAM read of the whole instance
// (voice.tf says exactly what that reaches). What narrows it to the policy
// nodes is this file's two paths and nothing else — which is why they are
// projectpolicy's functions and not strings written here.
type RTDBVoice struct {
	DB Getter
}

// Voice reads the project's child, then the shared allow-list, and answers the
// local policy when neither exists.
//
// The order matters and is the design's (§4.3): the project's node is the
// narrow answer and the shared one is the fallback the installation sets, so a
// project that has a node is never widened by the shared one. A project node
// that exists but cannot be read is a REFUSAL rather than a fall-through to the
// shared list: falling through would serve the installation's default to a
// project whose operator wrote something the hop could not understand, which is
// the one case where "I could not read it" and "it said nothing" must not be
// the same answer.
func (r RTDBVoice) Voice(ctx context.Context, slug string) (projectpolicy.Voice, error) {
	for _, path := range []string{projectpolicy.PathVoiceRepo(slug), projectpolicy.PathVoiceShared()} {
		v, found, err := r.read(ctx, path)
		if err != nil {
			return projectpolicy.LocalVoice(), err
		}
		if found {
			return v, nil
		}
	}
	return projectpolicy.LocalVoice(), nil
}

// read reads one node and decodes it. A node that is absent is found=false and
// no error; a node that is present and cannot be read or decoded is a refusal
// with ReasonPolicyUnreadable and the reader's own sentence.
//
// The reader's error is carried as the DETAIL and never as a decision: it names
// the member a hand-edited node got wrong, which is what an operator needs, and
// it is bounded and made one line before it reaches the wire (safeDetail).
func (r RTDBVoice) read(ctx context.Context, path string) (projectpolicy.Voice, bool, error) {
	if r.DB == nil {
		return projectpolicy.LocalVoice(), false, refuse(ReasonStateUnavailable, errors.New("voicesvc: the voice policy reader holds no database"))
	}
	var raw json.RawMessage
	found, err := r.DB.Get(ctx, path, &raw)
	if err != nil {
		return projectpolicy.LocalVoice(), false, refuse(ReasonStateUnavailable, fmt.Errorf("voicesvc: reading the voice policy at %s: %w", path, err))
	}
	if !found {
		return projectpolicy.LocalVoice(), false, nil
	}
	v, err := projectpolicy.DecodeVoice(raw)
	if err != nil {
		return projectpolicy.LocalVoice(), false, refuse(ReasonPolicyUnreadable, err)
	}
	return v, true, nil
}

// Direction is which way the speech is going. It is an int and not a bool
// because a bool at a call site reads as nothing: `Decide(ctx, slug, name,
// false)` is a call no reviewer can check, and the two directions are two
// different privacy propositions (§3).
type Direction int

const (
	// STT is a remote speech-to-text call: raw audio leaves the device.
	STT Direction = iota
	// TTS is a remote text-to-speech call: reply text leaves the device, after
	// the redactor has seen it.
	TTS
)

// String names the direction the way the node does, for a refusal and a log
// line.
func (d Direction) String() string {
	if d == TTS {
		return "tts"
	}
	return "stt"
}

// Enforcer decides whether one call may leave the device.
type Enforcer struct {
	voices VoiceReader
}

// NewEnforcer returns the enforcer over a policy reader.
func NewEnforcer(voices VoiceReader) (*Enforcer, error) {
	if voices == nil {
		return nil, errors.New("voicesvc: the enforcer needs a voice policy reader")
	}
	return &Enforcer{voices: voices}, nil
}

// Decide answers the one question the hop asks per call: may this device's
// speech go to this engine, in this direction, for this project? A nil error is
// the admission; anything else is a refusal carrying the reason the app acts
// on.
//
// The four reasons it can answer with are §4.3's, in the order the design
// weighs them, and the order is the point:
//
//  1. the node could not be read → ReasonPolicyUnreadable. Nothing is admitted
//     on a node nobody could read, and this is the one a caller is most tempted
//     to soften into "the node said nothing".
//  2. `force_local` → ReasonForceLocal. It wins over the device's own setting
//     and over every other member of the node.
//  3. nobody is admitted at all (an absent node, an empty list, both switches
//     off) → ReasonNoAdmission.
//  4. this engine, in this direction → ReasonNotAdmitted.
//
// The engine name is matched exactly and never folded (projectpolicy.admits'
// argument): a provider name is lowercase by this tree's own rule, so a name
// that differs in case is not the engine the list names.
func (e *Enforcer) Decide(ctx context.Context, slug, provider string, dir Direction) error {
	v, err := e.voices.Voice(ctx, slug)
	if err != nil {
		return err
	}
	switch {
	case v.ForceLocal:
		return refuse(ReasonForceLocal, fmt.Errorf("voicesvc: the project policy for this project forbids remote for every device, so this %s call is served locally", dir))
	case v.LocalOnly():
		return refuse(ReasonNoAdmission, fmt.Errorf("voicesvc: the project policy for this project admits no remote %s call", dir))
	}
	admitted := v.AdmitsSTT(provider)
	if dir == TTS {
		admitted = v.AdmitsTTS(provider)
	}
	if !admitted {
		return refuse(ReasonNotAdmitted, fmt.Errorf("voicesvc: the project policy for this project does not admit this %s engine, or has this direction off", dir))
	}
	return nil
}

// Answer builds the project-status answer for one device and one project
// (§4.4 point 7): what is effective, and why when nothing is.
//
// It is an ANSWER and never an error: the app asks "what is the policy here"
// and "the policy here is local" is a fact about the project, not a failure of
// the request. Only state this service could not reach at all is an error, and
// then the handler answers ReasonStateUnavailable — which the app treats as
// local for the same reason.
//
// The answer carries no copy of the node. It names the engines (which are names
// by projectpolicy's own rule, so nothing here can be a destination) and the
// two switches, and the DETAIL is the decode refusal's sentence and nothing
// else: the app caches this, and a cached copy of a policy that a later write
// changed is harmless precisely because the hop refuses on the call itself.
func (e *Enforcer) Answer(ctx context.Context, slug string) (StatusAnswer, error) {
	v, err := e.voices.Voice(ctx, slug)
	if err != nil {
		if ReasonOf(err) == ReasonPolicyUnreadable {
			// The project-status query exists to report this, so it is an
			// answer here and a refusal per call (Decide's first row). The app
			// shows the sentence; the reason is what it branches on.
			return StatusAnswer{
				Project: slug,
				Remote:  false,
				Reason:  ReasonPolicyUnreadable,
				Detail:  refusalDetail(err),
			}, nil
		}
		return StatusAnswer{}, err
	}
	ans := StatusAnswer{
		Project: slug,
		Remote:  !v.LocalOnly(),
		Direction: StatusDirection{
			STT: v.Direction.STT,
			TTS: v.Direction.TTS,
		},
	}
	switch {
	case v.ForceLocal:
		ans.Reason = ReasonForceLocal
	case v.LocalOnly():
		ans.Reason = ReasonNoAdmission
	default:
		// Sorted and copied: the node's own order is a JSON object's, which is
		// not an order, and the app caches this answer — an unstable list would
		// make two equal answers look different.
		ans.Providers = append([]string(nil), v.Providers...)
		sort.Strings(ans.Providers)
	}
	return ans, nil
}

// refusalDetail is a refusal's sentence without its reason prefix, for the
// answer's Detail: the reason is the answer's own field, and repeating it in
// the sentence would be a second spelling of the same fact.
func refusalDetail(err error) string {
	var r *RefusalError
	if errors.As(err, &r) && r.Err != nil {
		return r.Err.Error()
	}
	return err.Error()
}
