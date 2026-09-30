package runstore

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"
)

const sessionID = "3f2a9c1e-0000-4000-8000-000000000001"

var headSHA = strings.Repeat("a", 40)

func TestSessionRoundTrip(t *testing.T) {
	s := newStore(t)
	if _, _, err := s.ReadSession(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadSession on an empty store err = %v, want ErrNotFound", err)
	}
	data := []byte(`{"prompt":"hi"}` + "\n")
	m := SessionMeta{Version: 1, ID: sessionID, HeadSHA: headSHA, WorkDir: "/work/repo", Bytes: int64(len(data))}
	if err := s.PutSession(ctx, m, data); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"session/" + sessionID + ".jsonl", "session/session.json"} {
		if _, err := s.ReadFile(ctx, name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	got, gotData, err := s.ReadSession(ctx)
	if err != nil || *got != m || !bytes.Equal(gotData, data) {
		t.Fatalf("ReadSession = %+v, %q, %v", got, gotData, err)
	}
}

func TestPutSessionRefusesBadID(t *testing.T) {
	s := newStore(t)
	if err := s.PutSession(ctx, SessionMeta{Version: 1, ID: "../../x"}, nil); err == nil {
		t.Fatal("PutSession accepted a session ID that isn't a UUID")
	}
}

func TestReadSessionCap(t *testing.T) {
	b := memblob.OpenBucket(nil)
	defer b.Close()
	s := Open(b, "acme-app", runID)
	big := bytes.Repeat([]byte("x"), MaxSessionBytes+1)
	if err := s.PutFile(ctx, "session/"+sessionID+".jsonl", big, "application/x-ndjson"); err != nil {
		t.Fatal(err)
	}
	if err := s.PutFile(ctx, "session/session.json", []byte(`{"version":1,"id":"`+sessionID+`","head_sha":"`+headSHA+`","workdir":"/work/repo"}`), "application/json"); err != nil {
		t.Fatal(err)
	}
	m, data, err := s.ReadSession(ctx)
	if !errors.Is(err, ErrTooLarge) || data != nil || m == nil || m.ID != sessionID {
		t.Fatalf("ReadSession = %+v, %d bytes, %v; want the meta and ErrTooLarge", m, len(data), err)
	}
	// The meta is capped too.
	if err := s.PutFile(ctx, "session/session.json", bytes.Repeat([]byte(" "), MaxRecordBytes+1), "application/json"); err != nil {
		t.Fatal(err)
	}
	if m, _, err := s.ReadSession(ctx); !errors.Is(err, ErrTooLarge) || m != nil {
		t.Fatalf("oversized meta: %+v, %v", m, err)
	}
}

func TestReadSessionMetaWithoutData(t *testing.T) {
	s := newStore(t)
	if err := s.PutFile(ctx, "session/session.json", []byte(`{"version":1,"id":"`+sessionID+`","head_sha":"`+headSHA+`","workdir":"/work/repo"}`), "application/json"); err != nil {
		t.Fatal(err)
	}
	m, data, err := s.ReadSession(ctx)
	if !errors.Is(err, ErrNotFound) || data != nil || m == nil || m.WorkDir != "/work/repo" {
		t.Fatalf("ReadSession = %+v, %q, %v; want the meta and ErrNotFound", m, data, err)
	}
}

func TestReadSessionBadID(t *testing.T) {
	s := newStore(t)
	if err := s.PutFile(ctx, "session/session.json", []byte(`{"version":1,"id":"../../result","workdir":"/work/repo"}`), "application/json"); err != nil {
		t.Fatal(err)
	}
	m, data, err := s.ReadSession(ctx)
	if err == nil || errors.Is(err, ErrNotFound) || data != nil || m == nil {
		t.Fatalf("ReadSession = %+v, %q, %v; want the meta and an invalid-ID error", m, data, err)
	}
}
