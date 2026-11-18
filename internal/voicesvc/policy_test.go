package voicesvc

// The enforcement's tests: which node is read, in which order, and what each
// answer means. The project's node is the narrow answer and the shared one is
// the fallback; an unreadable node is a refusal and never a fall-through.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestRTDBVoiceReadsTheNodesProjectpolicyNames: the paths are that package's
// functions and not strings written here, so a node the publisher writes is the
// node the hop reads.
func TestRTDBVoiceReadsTheNodesProjectpolicyNames(t *testing.T) {
	db := newTestDB(t)
	db.Set(projectpolicy.PathVoiceRepo(testSlug), map[string]any{
		"providers": []any{"deepgram"},
		"direction": map[string]any{"stt": true},
	})

	v, err := (RTDBVoice{DB: db.Client}).Voice(context.Background(), testSlug)
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if !v.AdmitsSTT("deepgram") || v.AdmitsTTS("deepgram") {
		t.Fatalf("the project's node was not the one read: %+v", v)
	}
}

// TestRTDBVoicePrefersTheProjectsNodeThenTheSharedOne: a project that has a
// node is never widened by the installation's default, and a project that has
// none is served the default.
func TestRTDBVoicePrefersTheProjectsNodeThenTheSharedOne(t *testing.T) {
	db := newTestDB(t)
	db.Set(projectpolicy.PathVoiceShared(), map[string]any{
		"providers": []any{"shared-engine"},
		"direction": map[string]any{"stt": true, "tts": true},
	})
	r := RTDBVoice{DB: db.Client}

	shared, err := r.Voice(context.Background(), testSlug)
	if err != nil {
		t.Fatalf("Voice with no project node: %v", err)
	}
	if !shared.AdmitsSTT("shared-engine") {
		t.Fatalf("the shared node was not the fallback: %+v", shared)
	}

	db.Set(projectpolicy.PathVoiceRepo(testSlug), map[string]any{
		"providers": []any{"project-engine"},
		"direction": map[string]any{"stt": true},
	})
	project, err := r.Voice(context.Background(), testSlug)
	if err != nil {
		t.Fatalf("Voice with a project node: %v", err)
	}
	if project.AdmitsSTT("shared-engine") {
		t.Fatalf("the shared node widened a project that has its own: %+v", project)
	}
	if !project.AdmitsSTT("project-engine") {
		t.Fatalf("the project's own node was not read: %+v", project)
	}
}

// TestAnAbsentPolicyIsTheLocalAnswer: a project nothing was published for is
// served locally, and that is an ANSWER and not an error — §4.3's four answers
// are one, and the app shows "local" rather than a failure.
func TestAnAbsentPolicyIsTheLocalAnswer(t *testing.T) {
	db := newTestDB(t)
	r := RTDBVoice{DB: db.Client}

	v, err := r.Voice(context.Background(), testSlug)
	if err != nil {
		t.Fatalf("Voice: %v", err)
	}
	if !v.LocalOnly() {
		t.Fatalf("an absent node is not local: %+v", v)
	}

	e, err := NewEnforcer(r)
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	if got := ReasonOf(e.Decide(context.Background(), testSlug, "deepgram", STT)); got != ReasonNoAdmission {
		t.Fatalf("Decide = %q, want %q", got, ReasonNoAdmission)
	}
}

// TestAnUnreadablePolicyIsARefusalAndNotAFallThrough is the trap this file
// exists to close: a project node that exists and cannot be read must NOT be
// answered from the shared list. Falling through would serve the installation's
// default to a project whose operator wrote something the hop could not
// understand — the one case where "I could not read it" and "it said nothing"
// must not be the same answer.
func TestAnUnreadablePolicyIsARefusalAndNotAFallThrough(t *testing.T) {
	db := newTestDB(t)
	db.Set(projectpolicy.PathVoiceShared(), map[string]any{
		"providers": []any{"shared-engine"},
		"direction": map[string]any{"stt": true},
	})
	db.Set(projectpolicy.PathVoiceRepo(testSlug), map[string]any{
		"providers":        []any{"project-engine"},
		"direction":        map[string]any{"stt": true},
		"admit_everything": true,
	})

	e, err := NewEnforcer(RTDBVoice{DB: db.Client})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	if err := e.Decide(context.Background(), testSlug, "shared-engine", STT); ReasonOf(err) != ReasonPolicyUnreadable {
		t.Fatalf("Decide = %v (%q), want %q", err, ReasonOf(err), ReasonPolicyUnreadable)
	}
	ans, err := e.Answer(context.Background(), testSlug)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if ans.Remote || ans.Reason != ReasonPolicyUnreadable || ans.Detail == "" {
		t.Fatalf("the answer reads %+v", ans)
	}
	if strings.ContainsAny(ans.Detail, "\n\x1b") {
		t.Fatalf("the detail is not one printable line: %q", ans.Detail)
	}
}

// TestDecideRefusesByReason walks §4.3's four answers and the per-call one.
// The order matters: force_local wins over every other member, and the engine
// and the direction are one answer (not_admitted) because neither is a fact
// about the project as a whole.
func TestDecideRefusesByReason(t *testing.T) {
	for _, tc := range []struct {
		name     string
		node     string
		provider string
		dir      Direction
		want     string
	}{
		{
			name:     "force_local wins over everything",
			node:     `{"force_local":true,"providers":["deepgram"],"direction":{"stt":true,"tts":true}}`,
			provider: "deepgram",
			dir:      STT,
			want:     ReasonForceLocal,
		},
		{
			name:     "force_local refuses the other direction too",
			node:     `{"force_local":true,"providers":["deepgram"],"direction":{"stt":true,"tts":true}}`,
			provider: "deepgram",
			dir:      TTS,
			want:     ReasonForceLocal,
		},
		{
			name:     "an empty list admits nobody",
			node:     `{"providers":[],"direction":{"stt":true}}`,
			provider: "deepgram",
			dir:      STT,
			want:     ReasonNoAdmission,
		},
		{
			name:     "both switches off admits nobody",
			node:     `{"providers":["deepgram"],"direction":{"stt":false,"tts":false}}`,
			provider: "deepgram",
			dir:      STT,
			want:     ReasonNoAdmission,
		},
		{
			name:     "the direction asked for is off",
			node:     `{"providers":["deepgram"],"direction":{"stt":false,"tts":true}}`,
			provider: "deepgram",
			dir:      STT,
			want:     ReasonNotAdmitted,
		},
		{
			name:     "the engine is not on the list",
			node:     `{"providers":["deepgram"],"direction":{"stt":true}}`,
			provider: "other-engine",
			dir:      STT,
			want:     ReasonNotAdmitted,
		},
		{
			name:     "the engine is named in another case",
			node:     `{"providers":["deepgram"],"direction":{"stt":true}}`,
			provider: "Deepgram",
			dir:      STT,
			want:     ReasonNotAdmitted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := testEnforcer(t, []byte(tc.node))
			err := e.Decide(context.Background(), testSlug, tc.provider, tc.dir)
			if err == nil {
				t.Fatalf("the call was admitted, want %q", tc.want)
			}
			if got := ReasonOf(err); got != tc.want {
				t.Fatalf("ReasonOf = %q, want %q (%v)", got, tc.want, err)
			}
		})
	}
}

// TestDecideAdmitsWhatThePolicyAdmits is the other half, and it is here so that
// a change that refused everything would fail rather than pass.
func TestDecideAdmitsWhatThePolicyAdmits(t *testing.T) {
	e := testEnforcer(t, []byte(`{"providers":["deepgram","other-engine"],"direction":{"stt":true,"tts":false}}`))
	if err := e.Decide(context.Background(), testSlug, "deepgram", STT); err != nil {
		t.Fatalf("Decide(STT) = %v", err)
	}
	if err := e.Decide(context.Background(), testSlug, "other-engine", STT); err != nil {
		t.Fatalf("Decide(STT, second engine) = %v", err)
	}
	if err := e.Decide(context.Background(), testSlug, "deepgram", TTS); ReasonOf(err) != ReasonNotAdmitted {
		t.Fatalf("Decide(TTS) = %v, want %q", err, ReasonNotAdmitted)
	}
}

// TestDecidePassesTheSlugToTheReader: the project is a claim and the only thing
// that happens to it is a read of its node, under its own key.
func TestDecidePassesTheSlugToTheReader(t *testing.T) {
	v := &fakeVoices{raw: []byte(`{"providers":["deepgram"],"direction":{"stt":true}}`)}
	e, err := NewEnforcer(v)
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	if err := e.Decide(context.Background(), testSlug, "deepgram", STT); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if v.slug != testSlug {
		t.Fatalf("the reader was asked about %q, want %q", v.slug, testSlug)
	}
}

// TestAnswerCarriesNoPolicyCopy: the answer is what is effective and why, and
// never the node. There is no member an app could read a rule out of — which is
// what makes a cached answer harmless.
func TestAnswerCarriesNoPolicyCopy(t *testing.T) {
	t.Run("nothing admitted", func(t *testing.T) {
		ans, err := testEnforcer(t, []byte(`{"force_local":true}`)).Answer(context.Background(), testSlug)
		if err != nil {
			t.Fatalf("Answer: %v", err)
		}
		if ans.Remote || ans.Reason != ReasonForceLocal || len(ans.Providers) != 0 {
			t.Fatalf("the answer reads %+v", ans)
		}
	})
	t.Run("admitted", func(t *testing.T) {
		ans, err := testEnforcer(t, []byte(`{"providers":["zzz","aaa"],"direction":{"stt":true,"tts":true}}`)).Answer(context.Background(), testSlug)
		if err != nil {
			t.Fatalf("Answer: %v", err)
		}
		if !ans.Remote || ans.Reason != "" {
			t.Fatalf("the answer reads %+v", ans)
		}
		if strings.Join(ans.Providers, ",") != "aaa,zzz" {
			t.Fatalf("providers = %v, want them sorted", ans.Providers)
		}
		if !ans.Direction.STT || !ans.Direction.TTS {
			t.Fatalf("direction = %+v", ans.Direction)
		}
	})
	t.Run("the slug is echoed", func(t *testing.T) {
		ans, err := testEnforcer(t, []byte(`{"force_local":true}`)).Answer(context.Background(), testSlug)
		if err != nil {
			t.Fatalf("Answer: %v", err)
		}
		if ans.Project != testSlug {
			t.Fatalf("Project = %q, want %q", ans.Project, testSlug)
		}
	})
}

// TestPolicyReadFailureIsStateAndNotAnAnswer: a database this service cannot
// reach is not "the project is served locally" — it is state unavailable, which
// the app treats the same way but which an operator is told about, and which is
// the only difference the two answers have.
func TestPolicyReadFailureIsStateAndNotAnAnswer(t *testing.T) {
	db := newTestDB(t)
	db.DenyRead(projectpolicy.PathVoiceRepo(testSlug))
	db.DenyRead(projectpolicy.PathVoiceShared())

	e, err := NewEnforcer(RTDBVoice{DB: db.Client})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	if err := e.Decide(context.Background(), testSlug, "deepgram", STT); ReasonOf(err) != ReasonStateUnavailable {
		t.Fatalf("Decide = %v (%q), want %q", err, ReasonOf(err), ReasonStateUnavailable)
	}
	if _, err := e.Answer(context.Background(), testSlug); ReasonOf(err) != ReasonStateUnavailable {
		t.Fatalf("Answer = %v (%q), want %q", err, ReasonOf(err), ReasonStateUnavailable)
	}
}

// TestEnforcerNeedsAReader: an enforcer with nothing to read from would answer
// every call from an absent node, which reads as "admits nobody" and hides the
// misconfiguration behind the safest-looking answer.
func TestEnforcerNeedsAReader(t *testing.T) {
	if _, err := NewEnforcer(nil); err == nil {
		t.Fatal("an enforcer with no reader was built")
	}
}

// TestRTDBVoiceWithoutADatabaseIsState: the reader is built from the database
// the process holds, and a nil one is a wiring bug this refuses rather than a
// panic a call would take down.
func TestRTDBVoiceWithoutADatabaseIsState(t *testing.T) {
	_, err := (RTDBVoice{}).Voice(context.Background(), testSlug)
	if ReasonOf(err) != ReasonStateUnavailable {
		t.Fatalf("Voice = %v (%q), want %q", err, ReasonOf(err), ReasonStateUnavailable)
	}
}

// TestDirectionNamesAreTheNodes: the direction is the node's own spelling, so a
// refusal says which switch refused the call.
func TestDirectionNamesAreTheNodes(t *testing.T) {
	if STT.String() != "stt" || TTS.String() != "tts" {
		t.Fatalf("STT = %q, TTS = %q", STT.String(), TTS.String())
	}
}

// testEnforcer returns an enforcer over a node's bytes, read by the real
// reader.
func testEnforcer(t *testing.T, raw []byte) *Enforcer {
	t.Helper()
	e, err := NewEnforcer(&fakeVoices{raw: raw})
	if err != nil {
		t.Fatalf("NewEnforcer: %v", err)
	}
	return e
}

// TestRefusalDetailDropsTheReasonPrefix: the reason is the refusal's own field,
// and a detail that repeated it would be a second spelling of the same fact.
func TestRefusalDetailDropsTheReasonPrefix(t *testing.T) {
	err := refuse(ReasonNotAdmitted, errors.New("the engine is not on the list"))
	if got := refusalDetail(err); got != "the engine is not on the list" {
		t.Fatalf("refusalDetail = %q", got)
	}
}
