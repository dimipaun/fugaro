package gateway

// W7: the advisory headroom at the member's end (docs/workspaces/spec.md §5,
// O1). What these tests hold is that the number is DELIVERED, that it stays
// inside the run's own process, and that it is ADVISORY: it arrives on the
// answer of a call the workspace's service already proxies, it is written into
// no node of the member's database and it is dropped from the answer the agent
// is handed, and the comparison this gateway makes on it never decides the
// workspace's cap.
//
// The cap itself is the workspace service's: it refuses the call at the ceiling
// whether or not this gateway ever read a header (TestWorkspaceCapRefusesThe-
// CallThatWouldExceedIt, internal/credsvc). So the two directions of "never
// decides the cap" are both here: a gateway told nothing stops nothing however
// much the run has spent, and the comparison made here is a decision the run
// would be told about rather than a halt this gateway imposes — the stop that
// acts on it is W8's, and WorkspaceStop's own doc says so.

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gateway/anthropicfake"
	"github.com/dimipaun/fugaro/internal/pricing"
)

// wsMicros is an amount of dollars, the one way this project converts money.
func wsMicros(t *testing.T, usd float64) pricing.Micros {
	t.Helper()
	m, err := pricing.FromUSD(usd)
	if err != nil {
		t.Fatalf("pricing.FromUSD(%v): %v", usd, err)
	}
	return m
}

// wsHeadroomValue is an amount of dollars as the wire carries it: µ$, decimal.
func wsHeadroomValue(t *testing.T, usd float64) string {
	t.Helper()
	return strconv.FormatInt(int64(wsMicros(t, usd)), 10)
}

// wsNoteHeadroom tells a gateway what the workspace's service delivered, for a
// test whose subject is the decision and not the delivery. The delivery itself
// is the first test's.
func wsNoteHeadroom(t *testing.T, h *harness, usd float64) {
	t.Helper()
	hdr := http.Header{}
	hdr.Set(WorkspaceHeadroomHeader, wsHeadroomValue(t, usd))
	h.gw.noteWorkspaceHeadroom(hdr)
}

// wsServiceOK is a served Messages call whose answer carries the room the
// workspace's service has left, exactly as the service writes it: the model's
// answer, the tier and credential name the contract carries, and the advisory
// number beside them.
func wsServiceOK(t *testing.T, usd float64) anthropicfake.Reply {
	t.Helper()
	rep := serviceOK(TierA, "cred-1")
	rep.Header.Set(WorkspaceHeadroomHeader, wsHeadroomValue(t, usd))
	return rep
}

// The number reaches the member on the service's own answer, and it is the only
// thing this gateway knows about the workspace's cap: a call whose worst case
// passes it is reported as a stop, a call that fits is not, and neither answer
// halts the run or decides anything about the cap.
func TestHeadroomIsAdvisoryAndTheGatewayNeverDecidesTheCap(t *testing.T) {
	// The stand-in service answers with the number on a call it serves, so the
	// header is read off a real answer that travelled the forwarding path.
	h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		return wsServiceOK(t, 1)
	}, nil)

	if _, ok := h.gw.DeliveredHeadroom(); ok {
		t.Fatal("the gateway holds a workspace figure before the service ever delivered one")
	}
	if _, stop := h.gw.WorkspaceStop(wsMicros(t, 1000)); stop {
		t.Fatal("a gateway told nothing stopped a run: the workspace's cap is not a figure this gateway may assume")
	}

	resp, _ := h.post(msg(sonnet, 64))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the served call answered %d, want the model's own answer", resp.StatusCode)
	}
	room, ok := h.gw.DeliveredHeadroom()
	if !ok {
		t.Fatalf("the gateway read no headroom off the answer that carried %s", WorkspaceHeadroomHeader)
	}
	if room != wsMicros(t, 1) {
		t.Fatalf("the delivered headroom is %v, want $1", room.USD())
	}
	// And the number stays in this run: the answer the agent was handed does not
	// carry it. It is the workspace's figure across all of its members, written
	// for the member's job and for no one else (spec §5), so it is dropped like
	// the tier, the credential, the switch and the probe (D143) — the agent is
	// handed the model's answer, and the runner already has the number
	// in-process.
	if v := resp.Header.Get(WorkspaceHeadroomHeader); v != "" {
		t.Fatalf("the agent's own answer carried %s: %s", WorkspaceHeadroomHeader, v)
	}

	// A call that fits is not a stop, and a call that passes the delivered
	// number is — with the number it passed in the sentence the run is told.
	if _, stop := h.gw.WorkspaceStop(wsMicros(t, 1)); stop {
		t.Fatal("a call that needs exactly the room that is left was stopped")
	}
	why, stop := h.gw.WorkspaceStop(wsMicros(t, 2))
	if !stop {
		t.Fatal("a $2 call with $1 of workspace room left was not stopped")
	}
	if !strings.Contains(why, "$1.00") || !strings.Contains(why, "$2.00") {
		t.Fatalf("the stop reads %q, and it names neither the room that is left nor what the call needs", why)
	}

	// It is a decision and not an enforcement: no halt is recorded, the run's
	// ledger is untouched, and the stop is the caller's to act on. The service
	// refuses the call itself at the ceiling, whatever this gateway believes.
	if halt, ok := h.halted(); ok {
		t.Fatalf("the advisory stop halted the run: %+v", halt)
	}
	if l := h.gw.Ledger(); l.Used != 0 || l.Reserved != 0 {
		t.Fatalf("the advisory stop moved the run's ledger: %+v", l)
	}
}

// An answer that carries no number changes nothing: a call the workspace's
// service did not pay for (a provider route, a token count) says nothing about
// the workspace's room, and reading its silence as "no room" would stop a run
// the workspace had room for. A value that is not a number is the same silence.
func TestHeadroomIsKeptWhenAnAnswerDoesNotCarryOne(t *testing.T) {
	var calls int
	h, _ := newServiceStub(t, func(*http.Request, []byte) anthropicfake.Reply {
		calls++
		switch calls {
		case 1:
			return wsServiceOK(t, 1)
		case 2:
			return serviceOK(TierA, "cred-1") // no header at all
		default:
			rep := serviceOK(TierA, "cred-1")
			rep.Header.Set(WorkspaceHeadroomHeader, "not-a-number")
			return rep
		}
	}, nil)

	for i := 0; i < 3; i++ {
		if resp, _ := h.post(msg(sonnet, 64)); resp.StatusCode != http.StatusOK {
			t.Fatalf("call %d answered %d", i+1, resp.StatusCode)
		}
		room, ok := h.gw.DeliveredHeadroom()
		if !ok || room != wsMicros(t, 1) {
			t.Fatalf("after call %d the gateway holds (%v, %v), want the $1 the first answer delivered", i+1, room.USD(), ok)
		}
	}

	// And a header that is not a non-negative integer is no number at all: the
	// gateway reports none rather than guessing at one.
	for _, v := range []string{"", "-1", "1.5", "many"} {
		hdr := http.Header{}
		hdr.Set(WorkspaceHeadroomHeader, v)
		if m, ok := WorkspaceHeadroom(hdr); ok {
			t.Fatalf("the wire read %q as %v, want no number", v, m.USD())
		}
	}
}

// The workspace's figure and the run's own cap are two different numbers, and
// neither widens the other: the stop is a function of the delivered number
// alone. A run whose own cap is huge is stopped exactly where one whose cap is
// tiny is, and a workspace with room to spare does not raise the run's cap.
func TestWorkspaceCapIsNotWidenedByAMembersOwnCap(t *testing.T) {
	for _, own := range []pricing.Micros{wsMicros(t, 0.5), wsMicros(t, 100)} {
		h := newHarnessWith(t, enforceCap(own))
		wsNoteHeadroom(t, h, 1)
		if _, stop := h.gw.WorkspaceStop(wsMicros(t, 2)); !stop {
			t.Fatalf("a $2 call was not stopped although the workspace left $1, with the run's own cap at %v", own.USD())
		}
		if _, stop := h.gw.WorkspaceStop(wsMicros(t, 0.5)); stop {
			t.Fatalf("a $0.50 call was stopped although the workspace left $1, with the run's own cap at %v", own.USD())
		}
	}

	// The other direction: the workspace's room is not the run's cap. A member
	// whose own cap is half a dollar is not granted more of it by a workspace
	// with $100 left — the run's cap is the ledger's rule and stays there.
	h := newHarnessWith(t, enforceCap(wsMicros(t, 0.5)))
	wsNoteHeadroom(t, h, 100)
	if _, stop := h.gw.WorkspaceStop(wsMicros(t, 2)); stop {
		t.Fatal("a $2 call was stopped although the workspace had $100 left: the workspace's room is not the run's cap")
	}
	if h.gw.o.Cap != wsMicros(t, 0.5) {
		t.Fatalf("the run's own cap moved to %v on a delivered headroom of $100", h.gw.o.Cap.USD())
	}
}

// The figure the header carries is the workspace's own, so the agent never sees
// it: the header is dropped from every answer copied on, like the tier, the
// credential, the switch and the probe
// (TestProbeHeaderNeverReachesTheAgent). A run's own process reads it from the
// answer before it is dropped (Server.serviceAnswer →
// Server.noteWorkspaceHeadroom), which is what DeliveredHeadroom reports, so
// nothing in the run is given it by way of the agent's conversation.
func TestWorkspaceHeadroomNeverReachesTheAgent(t *testing.T) {
	if !dropResponseHeaders[WorkspaceHeadroomHeader] {
		t.Errorf("%s is not in dropResponseHeaders: the agent would see the workspace's remaining room, which is not a figure it is delivered to", WorkspaceHeadroomHeader)
	}
}
